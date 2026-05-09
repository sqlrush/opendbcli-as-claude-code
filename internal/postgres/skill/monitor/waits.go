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
 *	  internal/postgres/skill/monitor/waits.go
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
  COALESCE(wait_event_type, 'CPU') AS wait_type,
  COALESCE(wait_event, 'On CPU') AS wait_event,
  COUNT(*) AS sessions
FROM pg_stat_activity
WHERE state = 'active' AND backend_type = 'client backend'
GROUP BY wait_event_type, wait_event
ORDER BY sessions DESC
LIMIT 15`

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
	rendered := formatPGWaitsPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("等待事件分布 (活跃会话, 当前快照) — %d 个", len(result.Rows)),
	}, nil
}

// PostgreSQL waits panel column widths.
const (
	pgWtNumW      = 3
	pgWtTypeW     = 12
	pgWtEventW    = 16
	pgWtSessionsW = 10
)

func formatPGWaitsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No wait events found"
	}

	header := " " + format.PadLeft("#", pgWtNumW) + "  " +
		format.PadRight("WaitType", pgWtTypeW) + " " +
		format.PadRight("WaitEvent", pgWtEventW) + " " +
		format.PadLeft("Sessions", pgWtSessionsW)

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

		line := " " + format.PadLeft(fmt.Sprintf("%d", i+1), pgWtNumW) + "  " +
			format.PadRight(format.TruncDisplayWidth(waitType, pgWtTypeW), pgWtTypeW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitEvent, pgWtEventW), pgWtEventW) + " " +
			format.PadLeft(fmt.Sprintf("%.0f", sessions), pgWtSessionsW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	// Dominant wait type analysis (show when >50% of sessions).
	if totalSessions > 0 {
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
			hint := pgWaitTypeHint(domType)
			if hint != "" {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%) — %s", domType, domPct, hint))
			} else {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%)", domType, domPct))
			}
		}
	}

	title := fmt.Sprintf("等待事件分布 (活跃会话, 当前快照) — %d 个", len(qr.Rows))
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

// pgWaitTypeHint returns a short Chinese interpretation for the dominant PG wait type.
func pgWaitTypeHint(waitType string) string {
	switch waitType {
	case "CPU":
		return "CPU 密集，检查慢查询或缺少索引"
	case "IO":
		return "磁盘 I/O，检查存储性能或缓存命中率"
	case "Lock":
		return "行锁争用，检查长事务或死锁"
	case "LWLock":
		return "轻量锁争用，检查并发热点"
	case "BufferPin":
		return "缓冲区 Pin 等待，检查并发扫描"
	case "Activity":
		return "后台进程等待，通常无需关注"
	case "Client":
		return "客户端通信，检查网络或应用响应"
	case "IPC":
		return "进程间通信，检查并行查询或复制"
	default:
		return ""
	}
}
