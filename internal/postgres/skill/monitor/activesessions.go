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
 *	  internal/postgres/skill/monitor/activesessions.go
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
  COALESCE(wait_event_type, 'CPU') AS wait_type,
  COALESCE(wait_event, 'ON CPU') AS wait_event,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::numeric(10,1) AS elapsed_sec,
  LEFT(query, 120) AS query,
  leader_pid
FROM pg_stat_activity
WHERE state = 'active' AND backend_type IN ('client backend', 'parallel worker')
  AND pid != pg_backend_pid()
ORDER BY COALESCE(leader_pid, pid), pid`

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
	return skill.ToolDef{Name: "activesessions", Description: "List active PostgreSQL sessions with wait events and current query"}
}

func (s *ActiveSessionsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, activeSessionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rendered := formatPGActiveSessionsPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("活跃会话 — %d 个", len(result.Rows)),
	}, nil
}

// PostgreSQL active sessions panel column widths.
const (
	pgAsPidW     = 7
	pgAsUserW    = 12
	pgAsDBW      = 10
	pgAsWaitTW   = 8
	pgAsWaitEW   = 16
	pgAsElapsedW = 8
	pgAsLeaderW  = 8
	pgAsQueryW   = 34
)

func formatPGActiveSessionsPanel(qr *db.QueryResult) string {
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

	header := " " + format.PadRight("PID", pgAsPidW) + " " +
		format.PadRight("User", pgAsUserW) + " " +
		format.PadRight("DB", pgAsDBW) + " " +
		format.PadRight("WaitType", pgAsWaitTW) + " " +
		format.PadRight("WaitEvent", pgAsWaitEW) + " " +
		format.PadLeft("Elapsed", pgAsElapsedW) + " " +
		format.PadRight("Leader", pgAsLeaderW) + " " +
		format.PadRight("Query", pgAsQueryW)

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
		leader := "-"
		if len(row) >= 8 && row[7] != nil {
			leader = fmtVal(row[7])
		}

		elapsedStr := fmtWaitTime(elapsed)

		line := " " + format.PadRight(pid, pgAsPidW) + " " +
			format.PadRight(format.TruncDisplayWidth(user, pgAsUserW), pgAsUserW) + " " +
			format.PadRight(format.TruncDisplayWidth(dbName, pgAsDBW), pgAsDBW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitType, pgAsWaitTW), pgAsWaitTW) + " " +
			format.PadRight(format.TruncDisplayWidth(waitEvent, pgAsWaitEW), pgAsWaitEW) + " " +
			format.PadLeft(elapsedStr, pgAsElapsedW) + " " +
			format.PadRight(leader, pgAsLeaderW) + " " +
			format.PadRight(format.TruncDisplayWidth(query, pgAsQueryW), pgAsQueryW)

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
