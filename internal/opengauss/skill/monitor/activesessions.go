/*-------------------------------------------------------------------------
 *
 * activesessions.go
 *	  activesessions — ActiveSessionsSkill plus helpers
 *	  (NewActiveSessionsSkill) used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/activesessions.go
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

const activeSessionsSQL = `SELECT
  pid, usename, datname,
  CASE WHEN waiting THEN 'Lock' ELSE 'CPU' END AS wait_type,
  CASE WHEN waiting THEN 'lock_wait' WHEN enqueue != '' THEN enqueue ELSE 'On CPU' END AS wait_event,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::numeric(10,1) AS elapsed_sec,
  LEFT(query, 120) AS query
FROM pg_stat_activity
WHERE state = 'active'
  AND pid != pg_backend_pid()
ORDER BY query_start`

type ActiveSessionsSkill struct{ driver db.Driver }

func NewActiveSessionsSkill(driver db.Driver) *ActiveSessionsSkill {
	return &ActiveSessionsSkill{driver: driver}
}

func (s *ActiveSessionsSkill) Name() string                      { return "activesessions" }
func (s *ActiveSessionsSkill) Description() string                { return "活跃会话列表" }
func (s *ActiveSessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ActiveSessionsSkill) Validate(_ skill.Params) error      { return nil }
func (s *ActiveSessionsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/activesessions"} }
func (s *ActiveSessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "activesessions", Description: "List active OpenGauss sessions with wait events and current query"}
}

func (s *ActiveSessionsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, activeSessionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rendered := formatOGActiveSessionsPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("活跃会话 — %d 个", len(result.Rows)),
	}, nil
}

// OpenGauss active sessions panel column widths.
const (
	ogAsPidW     = 7
	ogAsUserW    = 12
	ogAsDBW      = 10
	ogAsWaitTW   = 8
	ogAsWaitEW   = 16
	ogAsElapsedW = 8
	ogAsQueryW   = 36
)

func formatOGActiveSessionsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No active sessions"
	}

	// Count waiting sessions.
	waitingCount := 0
	for _, row := range qr.Rows {
		if len(row) >= 4 {
			wt := fmtVal(row[3])
			if wt != "CPU" && wt != "-" {
				waitingCount++
			}
		}
	}

	header := " " + format.PadRight("PID", ogAsPidW) + " " +
		format.PadRight("User", ogAsUserW) + " " +
		format.PadRight("DB", ogAsDBW) + " " +
		format.PadRight("WaitType", ogAsWaitTW) + " " +
		format.PadRight("WaitEvent", ogAsWaitEW) + " " +
		format.PadLeft("Elapsed", ogAsElapsedW) + " " +
		format.PadRight("Query", ogAsQueryW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	maxRows := 20
	displayRows := qr.Rows
	remaining := 0
	if len(displayRows) > maxRows {
		remaining = len(displayRows) - maxRows
		displayRows = displayRows[:maxRows]
	}

	var rows []string
	for _, row := range displayRows {
		if len(row) < 7 {
			continue
		}
		pid := fmtVal(row[0])
		user := fmtVal(row[1])
		dbName := fmtVal(row[2])
		waitType := fmtVal(row[3])
		waitEvent := fmtVal(row[4])
		elapsed := toFloat64(row[5])
		query := fmtVal(row[6])

		elapsedStr := fmtWaitTime(elapsed)

		line := " " + format.PadRight(pid, ogAsPidW) + " " +
			format.PadRight(format.TruncDisplayWidth(user, ogAsUserW), ogAsUserW) + " " +
			format.PadRight(format.TruncDisplayWidth(dbName, ogAsDBW), ogAsDBW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitType, ogAsWaitTW), ogAsWaitTW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitEvent, ogAsWaitEW), ogAsWaitEW) + " " +
			format.PadLeft(elapsedStr, ogAsElapsedW) + " " +
			format.PadRight(format.TruncDisplayWidth(query, ogAsQueryW), ogAsQueryW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	if remaining > 0 {
		lines = append(lines, fmt.Sprintf(" ... and %d more sessions", remaining))
	}
	lines = append(lines, " Tip: /kill PID to terminate a session")

	title := fmt.Sprintf("Active Sessions (%d total, %d waiting)", len(qr.Rows), waitingCount)
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

func fmtVal(v any) string {
	if v == nil {
		return "-"
	}
	s := fmt.Sprintf("%v", v)
	if s == "" || s == "<nil>" {
		return "-"
	}
	return s
}

func fmtWaitTime(sec float64) string {
	if sec <= 0 {
		return "-"
	}
	if sec < 1 {
		return fmt.Sprintf("%.1fs", sec)
	}
	if sec < 60 {
		return fmt.Sprintf("%.0fs", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%.0fm", sec/60)
	}
	return fmt.Sprintf("%.1fh", sec/3600)
}

func toFloat64(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
}
