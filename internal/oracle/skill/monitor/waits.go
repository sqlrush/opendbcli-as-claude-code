/*-------------------------------------------------------------------------
 *
 * waits.go
 *	  WaitsSkill shows non-idle wait events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/waits.go
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

const waitsSQL = `SELECT event, wait_class, total_waits,
       ROUND(time_waited_micro/1000000, 2) AS time_waited_sec,
       ROUND(time_waited_micro/NULLIF(total_waits,0)/1000, 3) AS avg_wait_ms,
       ROUND(time_waited_micro*100/NULLIF(SUM(time_waited_micro) OVER(), 0), 1) AS pct
FROM v$system_event
WHERE wait_class != 'Idle'
ORDER BY time_waited_micro DESC
FETCH FIRST 30 ROWS ONLY`

// WaitsSkill shows non-idle wait events.
type WaitsSkill struct {
	driver db.Driver
}

// NewWaitsSkill creates a WaitsSkill backed by the given driver.
func NewWaitsSkill(driver db.Driver) *WaitsSkill {
	return &WaitsSkill{driver: driver}
}

func (s *WaitsSkill) Name() string                          { return "waits" }
func (s *WaitsSkill) Description() string                   { return "Show non-idle wait events" }
func (s *WaitsSkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }

func (s *WaitsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "waits",
		Description: "Show non-idle wait events",
	}
}

func (s *WaitsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "waits",
		Usage:   "/waits",
	}
}

func (s *WaitsSkill) Validate(_ skill.Params) error { return nil }

func (s *WaitsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, waitsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying wait events: %w", err)
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("等待事件统计 (非 Idle, 自实例启动累计) — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("非Idle等待事件 Top 30 — %d 个", len(result.Rows)),
	}, nil
}

// Waits panel column widths.
const (
	wtNumW   = 3
	wtEventW = 24
	wtWaitsW = 9
	wtTimeW  = 9
	wtAvgW   = 8
	wtPctW   = 6
	wtClassW = 6
)

func formatWaitsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No wait events found"
	}

	header := " " + format.PadLeft("#", wtNumW) + "  " +
		format.PadRight("Event", wtEventW) + " " +
		format.PadLeft("Waits", wtWaitsW) + " " +
		format.PadLeft("Time(s)", wtTimeW) + " " +
		format.PadLeft("AvgMs", wtAvgW) + " " +
		format.PadLeft("Pct", wtPctW) + "  " +
		format.PadRight("Class", wtClassW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	// Limit display to 15 rows.
	maxRows := 15
	displayRows := qr.Rows
	remaining := 0
	if len(displayRows) > maxRows {
		remaining = len(displayRows) - maxRows
		displayRows = displayRows[:maxRows]
	}

	// Track dominant wait class.
	classPct := make(map[string]float64)

	var rows []string
	for i, row := range displayRows {
		if len(row) < 6 {
			continue
		}
		event := fmt.Sprintf("%v", row[0])
		class := fmt.Sprintf("%v", row[1])
		waits := toFloat64(row[2])
		timeSec := toFloat64(row[3])
		avgMs := toFloat64(row[4])
		pct := toFloat64(row[5])

		classPct[class] += pct

		line := " " + format.PadLeft(fmt.Sprintf("%d", i+1), wtNumW) + "  " +
			format.PadRight(format.TruncDisplayWidth(event, wtEventW), wtEventW) + " " +
			format.PadLeft(fmtWaitsNum(waits), wtWaitsW) + " " +
			format.PadLeft(fmt.Sprintf("%.1f", timeSec), wtTimeW) + " " +
			format.PadLeft(fmtAvgMs(avgMs), wtAvgW) + " " +
			format.PadLeft(fmt.Sprintf("%.1f%%", pct), wtPctW) + "  " +
			format.PadRight(shortWaitClass(class), wtClassW)

		rows = append(rows, line)
	}

	// Also count remaining rows for dominant class.
	for _, row := range qr.Rows[len(displayRows):] {
		if len(row) < 6 {
			continue
		}
		class := fmt.Sprintf("%v", row[1])
		pct := toFloat64(row[5])
		classPct[class] += pct
	}

	// Find dominant class.
	var domClass string
	var domPct float64
	for c, p := range classPct {
		if p > domPct {
			domPct = p
			domClass = c
		}
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	if remaining > 0 {
		lines = append(lines, fmt.Sprintf(" ... 及其他 %d 个事件", remaining))
	}

	// Summary line: dominant class + interpretation.
	summary := ""
	if domClass != "" && domPct > 0 {
		hint := waitClassHint(domClass)
		if hint != "" {
			summary = fmt.Sprintf(" 主要瓶颈: %s (%.1f%%) — %s", domClass, domPct, hint)
		} else {
			summary = fmt.Sprintf(" 主要瓶颈: %s (%.1f%%)", domClass, domPct)
		}
		lines = append(lines, summary)
	}

	title := fmt.Sprintf("等待事件统计 (非 Idle, 自实例启动累计) — %d 个", len(qr.Rows))
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

// waitClassHint returns a short Chinese interpretation for the dominant wait class.
func waitClassHint(class string) string {
	switch class {
	case "User I/O":
		return "磁盘读写，检查慢查询或存储性能"
	case "System I/O":
		return "系统 I/O，检查 redo/控制文件写入"
	case "Concurrency":
		return "并发争用，检查 latch/buffer busy"
	case "Commit":
		return "提交等待，检查 redo log 写入性能"
	case "Application":
		return "应用层锁，检查行锁或 enqueue"
	case "Configuration":
		return "配置不当，检查参数或资源限制"
	case "Network":
		return "网络延迟，检查客户端连接"
	default:
		return ""
	}
}

// fmtWaitsNum formats large numbers with K/M suffixes.
func fmtWaitsNum(n float64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fG", n/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", n/1e6)
	case n >= 1e3:
		return fmt.Sprintf("%.1fK", n/1e3)
	default:
		return fmt.Sprintf("%.0f", n)
	}
}

// fmtAvgMs formats average wait milliseconds, keeping within 8 chars.
func fmtAvgMs(ms float64) string {
	switch {
	case ms >= 100000:
		return fmt.Sprintf("%.0fK", ms/1000)
	case ms >= 10000:
		return fmt.Sprintf("%.1fK", ms/1000)
	case ms >= 1000:
		return fmt.Sprintf("%.0f", ms)
	default:
		return fmt.Sprintf("%.3f", ms)
	}
}

// shortWaitClass abbreviates common Oracle wait class names.
func shortWaitClass(class string) string {
	switch class {
	case "User I/O":
		return "IO"
	case "System I/O":
		return "SysIO"
	case "Commit":
		return "Cmmt"
	case "Concurrency":
		return "Conc"
	case "Application":
		return "App"
	case "Configuration":
		return "Conf"
	case "Administrative":
		return "Admin"
	case "Network":
		return "Net"
	case "Scheduler":
		return "Sched"
	default:
		return format.TruncDisplayWidth(class, wtClassW)
	}
}
