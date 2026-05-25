/*-------------------------------------------------------------------------
 *
 * alert.go
 *	  AlertSkill shows recent error log entries from
 *	  performance_schema.error_log.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/admin/alert.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const alertSQLTemplate = `SELECT
  DATE_FORMAT(logged, '%%m-%%d %%H:%%i:%%s') AS time,
  prio,
  subsystem,
  LEFT(data, 200) AS data
FROM performance_schema.error_log
WHERE logged > NOW() - INTERVAL %d HOUR
ORDER BY logged DESC
LIMIT %d`

// ANSI color codes for alert highlighting.
const (
	alertAnsiRed   = "\033[1;31m"
	alertAnsiAmber = "\033[1;33m"
	alertAnsiDim   = "\033[90m"
	alertAnsiReset = "\033[0m"
)

// AlertSkill shows recent error log entries from performance_schema.error_log.
type AlertSkill struct {
	driver db.Driver
}

// NewAlertSkill creates an AlertSkill backed by the given driver.
func NewAlertSkill(driver db.Driver) *AlertSkill {
	return &AlertSkill{driver: driver}
}

func (s *AlertSkill) Name() string                       { return "alert" }
func (s *AlertSkill) Description() string                { return "错误日志" }
func (s *AlertSkill) Category() string                   { return "admin" }
func (s *AlertSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *AlertSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/alert [hours] [limit]",
		Examples: []string{"/alert", "/alert 24", "/alert 1 100"},
	}
}

func (s *AlertSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alert",
		Description: "Show recent entries from MySQL error log (performance_schema.error_log)",
		Parameters: map[string]any{
			"hours": map[string]any{"type": "integer", "description": "Hours to look back (default 4)"},
			"limit": map[string]any{"type": "integer", "description": "Max rows to return (default 50)"},
		},
	}
}

func (s *AlertSkill) Validate(_ skill.Params) error { return nil }

func (s *AlertSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	hours := 4
	limit := 50
	args := params.StringOr("args", "")
	if args != "" {
		parts := strings.Fields(args)
		if v, err := strconv.Atoi(parts[0]); err == nil {
			hours = v
		}
		if len(parts) > 1 {
			if v, err := strconv.Atoi(parts[1]); err == nil {
				limit = v
			}
		}
	} else {
		hours = params.IntOr("hours", 4)
		limit = params.IntOr("limit", 50)
	}

	sqlStr := fmt.Sprintf(alertSQLTemplate, hours, limit)
	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("最近 %d 小时无错误日志", hours),
			Summary:  "no error log entries",
		}, nil
	}

	rendered := formatMySQLAlertPanel(result, hours)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("最近 %d 小时 %d 条错误日志", hours, len(result.Rows)),
	}, nil
}

// mysqlAlertEntry holds one parsed MySQL error_log row.
type mysqlAlertEntry struct {
	ts        string
	prio      string
	subsystem string
	data      string
}

// mysqlSeverityLabel maps MySQL prio column to a severity label and color.
// MySQL prio values: "System", "Warning", "Error", "Note".
func mysqlSeverityLabel(prio string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(prio)) {
	case "error":
		return "ERROR", alertAnsiRed
	case "warning":
		return "WARN", alertAnsiAmber
	case "system":
		return "INFO", alertAnsiDim
	default: // "Note" and anything else
		return "INFO", alertAnsiDim
	}
}

// mysqlCollapsedEntry is a consecutive-duplicate group.
type mysqlCollapsedEntry struct {
	entry mysqlAlertEntry
	count int
}

// collapseMySQLEntries merges consecutive entries with the same data text.
func collapseMySQLEntries(entries []mysqlAlertEntry) []mysqlCollapsedEntry {
	if len(entries) == 0 {
		return nil
	}
	collapsed := []mysqlCollapsedEntry{{entry: entries[0], count: 1}}
	for i := 1; i < len(entries); i++ {
		last := &collapsed[len(collapsed)-1]
		if entries[i].data == last.entry.data && entries[i].prio == last.entry.prio {
			last.count++
		} else {
			collapsed = append(collapsed, mysqlCollapsedEntry{entry: entries[i], count: 1})
		}
	}
	return collapsed
}

// formatMySQLAlertPanel renders MySQL error log with duplicate collapsing and severity labels.
func formatMySQLAlertPanel(qr *db.QueryResult, hours int) string {
	if qr == nil || len(qr.Rows) == 0 {
		return fmt.Sprintf("最近 %d 小时无错误日志", hours)
	}

	// Parse rows: columns are time, prio, subsystem, data.
	entries := make([]mysqlAlertEntry, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 4 {
			continue
		}
		entries = append(entries, mysqlAlertEntry{
			ts:        fmt.Sprintf("%v", row[0]),
			prio:      fmt.Sprintf("%v", row[1]),
			subsystem: fmt.Sprintf("%v", row[2]),
			data:      fmt.Sprintf("%v", row[3]),
		})
	}

	collapsed := collapseMySQLEntries(entries)

	var b strings.Builder
	uniqueCount := len(collapsed)
	totalCount := len(entries)
	if uniqueCount < totalCount {
		fmt.Fprintf(&b, "Error Log — 最近 %d 小时, %d 条 (%d 条去重后)\n", hours, totalCount, uniqueCount)
	} else {
		fmt.Fprintf(&b, "Error Log — 最近 %d 小时, %d 条\n", hours, totalCount)
	}

	var errCount, warnCount int

	for _, ce := range collapsed {
		label, color := mysqlSeverityLabel(ce.entry.prio)
		switch label {
		case "ERROR":
			errCount += ce.count
		case "WARN":
			warnCount += ce.count
		}

		// Truncate long messages.
		displayMsg := ce.entry.data
		if len(displayMsg) > 80 {
			displayMsg = displayMsg[:77] + "..."
		}

		// Build repeat suffix.
		repeat := ""
		if ce.count > 1 {
			repeat = fmt.Sprintf(" (\u00d7%d)", ce.count)
		}

		fmt.Fprintf(&b, "  %s[%s]%s %s  %s%s\n",
			color, label, alertAnsiReset,
			ce.entry.ts, displayMsg, repeat)
	}

	// Summary line.
	if errCount > 0 || warnCount > 0 {
		b.WriteByte('\n')
		if errCount > 0 {
			fmt.Fprintf(&b, "  %sERROR: %d%s", alertAnsiRed, errCount, alertAnsiReset)
		}
		if warnCount > 0 {
			if errCount > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, "  %sWARN: %d%s", alertAnsiAmber, warnCount, alertAnsiReset)
		}
		b.WriteByte('\n')
	}

	return b.String()
}
