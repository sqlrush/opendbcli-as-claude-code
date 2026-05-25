/*-------------------------------------------------------------------------
 *
 * activesessions.go
 *	  ActiveSessionsSkill shows active database sessions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/activesessions.go
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

const activeSessionsSQL = `SELECT sid, serial#, username, status,
       REGEXP_SUBSTR(machine, '[^.]+') AS machine, program,
       sql_id, event, wait_class AS wclass, seconds_in_wait AS wait_sec
FROM v$session
WHERE status = 'ACTIVE' AND username IS NOT NULL
ORDER BY seconds_in_wait DESC
FETCH FIRST 50 ROWS ONLY`

// ActiveSessionsSkill shows active database sessions.
type ActiveSessionsSkill struct {
	driver db.Driver
}

// NewActiveSessionsSkill creates an ActiveSessionsSkill backed by the given driver.
func NewActiveSessionsSkill(driver db.Driver) *ActiveSessionsSkill {
	return &ActiveSessionsSkill{driver: driver}
}

func (s *ActiveSessionsSkill) Name() string                          { return "activesessions" }
func (s *ActiveSessionsSkill) Description() string                   { return "Show active database sessions" }
func (s *ActiveSessionsSkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }

func (s *ActiveSessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "activesessions",
		Description: "Show active database sessions",
	}
}

func (s *ActiveSessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "activesessions",
		Usage:   "/activesessions",
	}
}

func (s *ActiveSessionsSkill) Validate(_ skill.Params) error { return nil }

func (s *ActiveSessionsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, activeSessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying active sessions: %w", err)
	}

	// Count sessions with significant wait time.
	waitingCount := 0
	for _, row := range result.Rows {
		if len(row) >= 10 {
			waitSec := toFloat64(row[9])
			if waitSec > 1 {
				waitingCount++
			}
		}
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("Active Sessions (%d total, %d waiting)", len(result.Rows), waitingCount),
		Summary:  fmt.Sprintf("活跃会话 (排除后台进程) — %d 个", len(result.Rows)),
	}, nil
}

// Active sessions panel column widths.
const (
	asSidW    = 6
	asSerialW = 8
	asUserW   = 12
	asEventW  = 22
	asTimeW   = 6
	asSqlW    = 13
)

func formatActiveSessionsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No active sessions"
	}

	// Count sessions with significant wait time.
	waitingCount := 0
	for _, row := range qr.Rows {
		if len(row) >= 10 {
			waitSec := toFloat64(row[9])
			if waitSec > 1 {
				waitingCount++
			}
		}
	}

	header := " " + format.PadRight("SID", asSidW) + " " +
		format.PadRight("Serial#", asSerialW) + " " +
		format.PadRight("User", asUserW) + " " +
		format.PadRight("Wait Event", asEventW) + " " +
		format.PadLeft("Time", asTimeW) + " " +
		format.PadRight("SQL_ID", asSqlW)

	sepW := format.DisplayWidth(header)
	sep := " " + format.SepLine(sepW-1)

	// Limit display.
	maxRows := 20
	displayRows := qr.Rows
	remaining := 0
	if len(displayRows) > maxRows {
		remaining = len(displayRows) - maxRows
		displayRows = displayRows[:maxRows]
	}

	var rows []string
	for _, row := range displayRows {
		if len(row) < 10 {
			continue
		}

		sid := fmtVal(row[0])
		serial := fmtVal(row[1])
		user := fmtVal(row[2])
		// row[3] = status, row[4] = machine, row[5] = program (skip for compact view)
		sqlID := fmtVal(row[6])
		event := fmtVal(row[7])
		// row[8] = wclass
		waitSec := toFloat64(row[9])

		timeStr := fmtWaitTime(waitSec)

		// Highlight long waits with warning icon.
		eventStr := format.PadRight(format.TruncDisplayWidth(event, asEventW), asEventW)
		if waitSec > 10 {
			eventStr = format.PadRight(format.TruncDisplayWidth(event, asEventW-2)+" \033[33m⚠\033[0m", asEventW)
		}

		line := " " + format.PadRight(sid, asSidW) + " " +
			format.PadRight(serial, asSerialW) + " " +
			format.PadRight(format.TruncDisplayWidth(user, asUserW), asUserW) + " " +
			eventStr + " " +
			format.PadLeft(timeStr, asTimeW) + " " +
			format.PadRight(sqlID, asSqlW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	if remaining > 0 {
		lines = append(lines, fmt.Sprintf(" ... and %d more sessions", remaining))
	}
	lines = append(lines, " Tip: /kill SID SERIAL# to terminate a session")

	title := fmt.Sprintf("Active Sessions (%d total, %d waiting)", len(qr.Rows), waitingCount)
	return format.Panel(title, []format.PanelSection{
		{Lines: lines},
	})
}

// fmtVal formats a value for display, converting nil to "-".
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

// fmtWaitTime formats seconds into a human-readable time string.
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
