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
 *	  internal/mysql/skill/monitor/activesessions.go
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
  ID, USER, HOST, DB,
  COMMAND, TIME, STATE,
  LEFT(INFO, 120) AS INFO
FROM information_schema.PROCESSLIST
WHERE COMMAND != 'Sleep'
ORDER BY TIME DESC`

type ActiveSessionsSkill struct {
	driver db.Driver
}

func NewActiveSessionsSkill(driver db.Driver) *ActiveSessionsSkill {
	return &ActiveSessionsSkill{driver: driver}
}

func (s *ActiveSessionsSkill) Name() string        { return "activesessions" }
func (s *ActiveSessionsSkill) Description() string  { return "活跃线程列表" }
func (s *ActiveSessionsSkill) Category() string     { return "monitor" }
func (s *ActiveSessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ActiveSessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/activesessions", Examples: []string{"/activesessions"}}
}

func (s *ActiveSessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "activesessions",
		Description: "List active (non-sleeping) MySQL threads with current query and wait state",
	}
}

func (s *ActiveSessionsSkill) Validate(_ skill.Params) error { return nil }

func (s *ActiveSessionsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, activeSessionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rendered := formatMySQLActiveSessionsPanel(result)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("活跃线程 (非 Sleep) — %d 个", len(result.Rows)),
	}, nil
}

// MySQL active sessions panel column widths.
const (
	myAsIDW      = 8
	myAsUserW    = 12
	myAsHostW    = 16
	myAsDBW      = 10
	myAsCmdW     = 10
	myAsTimeW    = 6
	myAsStateW   = 16
	myAsInfoW    = 30
)

func formatMySQLActiveSessionsPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "  No active threads"
	}

	header := " " + format.PadRight("ID", myAsIDW) + " " +
		format.PadRight("User", myAsUserW) + " " +
		format.PadRight("Host", myAsHostW) + " " +
		format.PadRight("DB", myAsDBW) + " " +
		format.PadRight("Cmd", myAsCmdW) + " " +
		format.PadLeft("Time", myAsTimeW) + " " +
		format.PadRight("State", myAsStateW) + " " +
		format.PadRight("Info", myAsInfoW)

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
		if len(row) < 8 {
			continue
		}
		id := fmtVal(row[0])
		user := fmtVal(row[1])
		host := fmtVal(row[2])
		dbName := fmtVal(row[3])
		cmd := fmtVal(row[4])
		timeSec := toFloat64(row[5])
		state := fmtVal(row[6])
		info := fmtVal(row[7])

		timeStr := fmtWaitTime(timeSec)

		line := " " + format.PadRight(id, myAsIDW) + " " +
			format.PadRight(format.TruncDisplayWidth(user, myAsUserW), myAsUserW) + " " +
			format.PadRight(format.TruncDisplayWidth(host, myAsHostW), myAsHostW) + " " +
			format.PadRight(format.TruncDisplayWidth(dbName, myAsDBW), myAsDBW) + " " +
			format.PadRight(format.TruncDisplayWidth(cmd, myAsCmdW), myAsCmdW) + " " +
			format.PadLeft(timeStr, myAsTimeW) + " " +
			format.PadRight(format.TruncDisplayWidth(state, myAsStateW), myAsStateW) + " " +
			format.PadRight(format.TruncDisplayWidth(info, myAsInfoW), myAsInfoW)

		rows = append(rows, line)
	}

	var lines []string
	lines = append(lines, header, sep)
	lines = append(lines, rows...)
	lines = append(lines, sep)

	if remaining > 0 {
		lines = append(lines, fmt.Sprintf(" ... and %d more threads", remaining))
	}
	lines = append(lines, " Tip: /kill ID to terminate a thread")

	title := fmt.Sprintf("Active Threads (non-Sleep) — %d total", len(qr.Rows))
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
