/*-------------------------------------------------------------------------
 *
 * waits.go
 *	  WaitsSkill shows wait event distribution for active sessions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/waits.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const waitsSQL = `SELECT
  CASE WHEN waiting THEN 'Lock' ELSE 'CPU' END AS wait_type,
  CASE WHEN waiting THEN 'lock_wait' WHEN enqueue != '' THEN enqueue ELSE 'On CPU' END AS wait_event,
  COUNT(*) AS sessions
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
GROUP BY wait_type, wait_event
ORDER BY sessions DESC
LIMIT 15`

// waitsContextSQL counts business vs background active sessions so /waits
// can pick a context-appropriate conclusion (e.g. avoid "CPU 密集，检查慢
// 查询" when the only activity is WLM/WDR/Asp background threads).
//
// OG 5.0 (PG 9.2 base) doesn't support the FILTER clause, so we use SUM
// with CASE WHEN for conditional aggregation. The %% escapes Sprintf
// nesting if this SQL ever goes through one.
const waitsContextSQL = `SELECT
  SUM(CASE WHEN usename IS NOT NULL
             AND client_addr IS NOT NULL
             AND query NOT LIKE '%WLM fetch%'
             AND query NOT LIKE '%pg_stat_get_wlm%'
           THEN 1 ELSE 0 END) AS business,
  SUM(CASE WHEN usename IS NULL
             OR client_addr IS NULL
             OR query LIKE '%WLM fetch%'
             OR query LIKE '%pg_stat_get_wlm%'
           THEN 1 ELSE 0 END) AS background
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()`

// WaitsSkill shows wait event distribution for active sessions.
type WaitsSkill struct{ driver db.Driver }

// NewWaitsSkill creates a WaitsSkill backed by the given driver.
func NewWaitsSkill(driver db.Driver) *WaitsSkill { return &WaitsSkill{driver: driver} }

func (s *WaitsSkill) Name() string                       { return "waits" }
func (s *WaitsSkill) Description() string                { return "等待事件分布" }
func (s *WaitsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *WaitsSkill) Validate(_ skill.Params) error      { return nil }
func (s *WaitsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/waits"} }
func (s *WaitsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "waits", Description: "Show wait event distribution snapshot from active sessions"}
}

func (s *WaitsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, waitsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	// Best-effort business-vs-background context — swallow error so the
	// primary waits view still renders even if this secondary query fails.
	bizCount, bgCount := 0, 0
	if ctxRes, ctxErr := s.driver.Query(ctx, waitsContextSQL); ctxErr == nil && ctxRes != nil && len(ctxRes.Rows) > 0 {
		if len(ctxRes.Rows[0]) >= 2 {
			bizCount = int(toFloat64(ctxRes.Rows[0][0]))
			bgCount = int(toFloat64(ctxRes.Rows[0][1]))
		}
	}

	rendered := formatOGWaitsPanelWithCtx(result, bizCount, bgCount)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("等待事件分布 (活跃会话, 当前快照) — %d 个", len(result.Rows)),
	}, nil
}

// OpenGauss waits panel column widths.
const (
	ogWtNumW      = 3
	ogWtTypeW     = 12
	ogWtEventW    = 16
	ogWtSessionsW = 10
)

// formatOGWaitsPanel keeps the old signature for callers that don't pass
// business/background counts. It defers to the context-aware version with
// unknown counts.
func formatOGWaitsPanel(qr *db.QueryResult) string {
	return formatOGWaitsPanelWithCtx(qr, -1, -1)
}

// formatOGWaitsPanelWithCtx renders the waits panel and picks a conclusion
// line based on how many active sessions are business vs. background.
// Passing negative counts disables the business/background split.
func formatOGWaitsPanelWithCtx(qr *db.QueryResult, biz, bg int) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No wait events found"
	}

	header := " " + format.PadLeft("#", ogWtNumW) + "  " +
		format.PadRight("WaitType", ogWtTypeW) + " " +
		format.PadRight("WaitEvent", ogWtEventW) + " " +
		format.PadLeft("Sessions", ogWtSessionsW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	// Track wait type session counts for dominant analysis.
	typeSessions := make(map[string]float64)
	totalSessions := 0.0

	var rows []string
	for i, row := range qr.Rows {
		if len(row) < 3 {
			continue
		}
		waitType := fmtVal(row[0])
		waitEvent := fmtVal(row[1])
		sessions := toFloat64(row[2])

		typeSessions[waitType] += sessions
		totalSessions += sessions

		line := " " + format.PadLeft(fmt.Sprintf("%d", i+1), ogWtNumW) + "  " +
			format.PadRight(format.TruncDisplayWidth(waitType, ogWtTypeW), ogWtTypeW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitEvent, ogWtEventW), ogWtEventW) + " " +
			format.PadLeft(fmt.Sprintf("%.0f", sessions), ogWtSessionsW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	// Conclusion line. Priority of interpretation:
	//   1. If the business/background split is known and there are 0 business
	//      sessions, say so directly — "主要瓶颈 CPU 密集 检查慢查询" is
	//      wrong when the only activity is WLM/WDR/Asp background threads.
	//   2. Otherwise fall through to dominant-wait-type analysis.
	if biz == 0 && bg > 0 {
		lines = append(lines, fmt.Sprintf(" 仅后台线程活跃（%d 个）— 实例处于空闲状态，不存在业务等待事件", bg))
	} else if totalSessions > 0 {
		var domType string
		var domCount float64
		for t, c := range typeSessions {
			if c > domCount {
				domCount = c
				domType = t
			}
		}
		domPct := domCount / totalSessions * 100
		if domPct > 50 {
			hint := ogWaitTypeHint(domType)
			// When we know business sessions exist, append them to the hint
			// so the reader sees the scope of the signal.
			scope := ""
			if biz >= 0 {
				scope = fmt.Sprintf("（业务 %d / 后台 %d）", biz, bg)
			}
			if hint != "" {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%) — %s%s", domType, domPct, hint, scope))
			} else {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%)%s", domType, domPct, scope))
			}
		}
	}

	title := fmt.Sprintf("等待事件分布 (活跃会话, 当前快照) — %d 个", len(qr.Rows))
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

// ogWaitTypeHint returns a short Chinese interpretation for the dominant OpenGauss wait type.
func ogWaitTypeHint(waitType string) string {
	switch waitType {
	case "CPU":
		return "CPU 密集，检查慢查询或缺少索引"
	case "Lock":
		return "锁争用，检查长事务或死锁"
	case "IO":
		return "磁盘 I/O，检查存储性能"
	default:
		return ""
	}
}
