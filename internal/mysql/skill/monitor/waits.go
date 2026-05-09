/*-------------------------------------------------------------------------
 *
 * waits.go
 *	  WaitsSkill shows wait event statistics from performance_schema.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/waits.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const waitsSQL = `SELECT
  EVENT_NAME,
  COUNT_STAR,
  ROUND(SUM_TIMER_WAIT / 1e12, 3) AS total_sec
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE EVENT_NAME NOT LIKE 'wait/idle/%'
  AND EVENT_NAME != 'idle'
  AND COUNT_STAR > 0
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 15`

// WaitsSkill shows wait event statistics from performance_schema.
type WaitsSkill struct {
	driver db.Driver
}

// NewWaitsSkill creates a WaitsSkill backed by the given driver.
func NewWaitsSkill(driver db.Driver) *WaitsSkill {
	return &WaitsSkill{driver: driver}
}

func (s *WaitsSkill) Name() string                       { return "waits" }
func (s *WaitsSkill) Description() string                { return "等待事件统计" }
func (s *WaitsSkill) Category() string                   { return "monitor" }
func (s *WaitsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *WaitsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/waits", Examples: []string{"/waits"}}
}

func (s *WaitsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "waits",
		Description: "Show top wait events from performance_schema with count and total time",
	}
}

func (s *WaitsSkill) Validate(_ skill.Params) error { return nil }

func (s *WaitsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, waitsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rendered := formatMySQLWaitsPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("等待事件统计 (非 Idle, 自上次重置累计) — %d 个", len(result.Rows)),
	}, nil
}

// MySQL waits panel column widths.
const (
	myWtNumW   = 3
	myWtEventW = 36
	myWtCountW = 10
	myWtTimeW  = 10
)

func formatMySQLWaitsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No wait events found"
	}

	header := " " + format.PadLeft("#", myWtNumW) + "  " +
		format.PadRight("Event", myWtEventW) + " " +
		format.PadLeft("Count", myWtCountW) + " " +
		format.PadLeft("Time(s)", myWtTimeW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	maxRows := 15
	displayRows := qr.Rows
	remaining := 0
	if len(displayRows) > maxRows {
		remaining = len(displayRows) - maxRows
		displayRows = displayRows[:maxRows]
	}

	// Track wait class time totals for dominant analysis.
	classTime := make(map[string]float64)
	totalTime := 0.0

	var rows []string
	for i, row := range displayRows {
		if len(row) < 3 {
			continue
		}
		event := fmtVal(row[0])
		count := toFloat64(row[1])
		timeSec := toFloat64(row[2])

		class := mysqlWaitClass(event)
		classTime[class] += timeSec
		totalTime += timeSec

		line := " " + format.PadLeft(fmt.Sprintf("%d", i+1), myWtNumW) + "  " +
			format.PadRight(format.TruncDisplayWidth(event, myWtEventW), myWtEventW) + " " +
			format.PadLeft(fmtLargeNum(count), myWtCountW) + " " +
			format.PadLeft(fmt.Sprintf("%.3f", timeSec), myWtTimeW)

		rows = append(rows, line)
	}

	// Also accumulate remaining rows for dominant analysis.
	for _, row := range qr.Rows[len(displayRows):] {
		if len(row) < 3 {
			continue
		}
		event := fmtVal(row[0])
		timeSec := toFloat64(row[2])
		class := mysqlWaitClass(event)
		classTime[class] += timeSec
		totalTime += timeSec
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	if remaining > 0 {
		lines = append(lines, fmt.Sprintf(" ... 及其他 %d 个事件", remaining))
	}

	// Dominant wait class analysis (show when >50%).
	if totalTime > 0 {
		var domClass string
		var domTime float64
		for c, t := range classTime {
			if t > domTime {
				domTime = t
				domClass = c
			}
		}
		domPct := domTime / totalTime * 100
		if domPct > 50 {
			hint := mysqlWaitClassHint(domClass)
			if hint != "" {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%) — %s", domClass, domPct, hint))
			} else {
				lines = append(lines, fmt.Sprintf(" 主要瓶颈: %s (%.1f%%)", domClass, domPct))
			}
		}
	}

	title := fmt.Sprintf("等待事件统计 (非 Idle, 自上次重置累计) — %d 个", len(qr.Rows))
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

// mysqlWaitClass maps a MySQL event name prefix to a wait class.
func mysqlWaitClass(event string) string {
	switch {
	case strings.HasPrefix(event, "wait/io"):
		return "IO"
	case strings.HasPrefix(event, "wait/lock"):
		return "Lock"
	case strings.HasPrefix(event, "wait/synch"):
		return "Concurrency"
	default:
		return "Other"
	}
}

// mysqlWaitClassHint returns a short Chinese interpretation for the dominant wait class.
func mysqlWaitClassHint(class string) string {
	switch class {
	case "IO":
		return "考虑检查存储性能或慢查询"
	case "Lock":
		return "考虑检查锁争用或长事务"
	case "Concurrency":
		return "考虑检查互斥量/信号量争用"
	default:
		return ""
	}
}

func fmtLargeNum(n float64) string {
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
