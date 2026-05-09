/*-------------------------------------------------------------------------
 *
 * alert.go
 *	  AlertSkill shows recent alert log entries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/alert.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const defaultAlertHours = 24

const alertSQL = `SELECT TO_CHAR(originating_timestamp, 'MM-DD HH24:MI:SS') AS time,
       message_text, message_level
FROM v$diag_alert_ext
WHERE originating_timestamp > SYSDATE - :1/24
  AND message_text IS NOT NULL
  AND LENGTH(TRIM(message_text)) > 5
ORDER BY originating_timestamp DESC
FETCH FIRST 50 ROWS ONLY`

// AlertSkill shows recent alert log entries.
type AlertSkill struct {
	driver db.Driver
}

// NewAlertSkill creates an AlertSkill backed by the given driver.
func NewAlertSkill(driver db.Driver) *AlertSkill {
	return &AlertSkill{driver: driver}
}

func (s *AlertSkill) Name() string        { return "alert" }
func (s *AlertSkill) Description() string { return "Show recent alert log entries" }
func (s *AlertSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *AlertSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alert",
		Description: "Show recent alert log entries",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Hours to look back (default 24)",
				},
			},
		},
	}
}

func (s *AlertSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "alert",
		Usage:    "/alert [hours]",
		Examples: []string{"/alert", "/alert 48"},
	}
}

func (s *AlertSkill) Validate(_ skill.Params) error { return nil }

func (s *AlertSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	hours := parseAlertHours(params.StringOr("args", ""))

	// Use a 15s timeout to prevent v$diag_alert_ext from blocking
	// the scheduler under heavy database load.
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	result, err := s.driver.Query(queryCtx, alertSQL, hours)
	if err != nil {
		return nil, fmt.Errorf("querying alert log: %w", err)
	}

	rendered := formatAlertPanel(result, hours)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("最近 %d 小时告警 — %d 条", hours, len(result.Rows)),
		Metadata: map[string]string{
			"hours": strconv.Itoa(hours),
		},
	}, nil
}

// ANSI color codes for alert highlighting.
const (
	ansiRed   = "\033[1;31m"
	ansiAmber = "\033[1;33m"
	ansiDim   = "\033[90m"
	ansiReset = "\033[0m"
)

// alertEntry holds one parsed alert row with its derived severity.
type alertEntry struct {
	ts    string
	msg   string
	level int
}

// severityLabel returns the severity tag and ANSI color for an alert entry.
// Oracle message_level: 1 = CRITICAL, 2 = SEVERE, 8 = IMPORTANT, 16 = NORMAL.
func severityLabel(e alertEntry) (string, string) {
	if e.level <= 2 {
		return "ERROR", ansiRed
	}
	upper := strings.ToUpper(e.msg)
	if strings.Contains(upper, "ORA-") {
		return "ERROR", ansiRed
	}
	if strings.Contains(upper, "DEPRECATED") ||
		strings.Contains(upper, "WARNING") {
		return "WARN", ansiAmber
	}
	return "INFO", ansiDim
}

// collapsedEntry is a consecutive-duplicate group.
type collapsedEntry struct {
	entry alertEntry
	count int
}

// collapseAlertEntries merges consecutive entries with the same message text.
func collapseAlertEntries(entries []alertEntry) []collapsedEntry {
	if len(entries) == 0 {
		return nil
	}
	collapsed := []collapsedEntry{{entry: entries[0], count: 1}}
	for i := 1; i < len(entries); i++ {
		last := &collapsed[len(collapsed)-1]
		if entries[i].msg == last.entry.msg {
			last.count++
			// Keep the first (most recent) timestamp since rows are DESC ordered.
		} else {
			collapsed = append(collapsed, collapsedEntry{entry: entries[i], count: 1})
		}
	}
	return collapsed
}

// formatAlertPanel renders the alert log with duplicate collapsing and severity labels.
func formatAlertPanel(qr *db.QueryResult, hours int) string {
	if qr == nil || len(qr.Rows) == 0 {
		return fmt.Sprintf("最近 %d 小时无告警记录", hours)
	}

	// Parse rows into alertEntry structs.
	entries := make([]alertEntry, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 3 {
			continue
		}
		entries = append(entries, alertEntry{
			ts:    fmt.Sprintf("%v", row[0]),
			msg:   fmt.Sprintf("%v", row[1]),
			level: toAlertLevel(row[2]),
		})
	}

	collapsed := collapseAlertEntries(entries)

	var b strings.Builder
	uniqueCount := len(collapsed)
	totalCount := len(entries)
	if uniqueCount < totalCount {
		fmt.Fprintf(&b, "Alert Log — 最近 %d 小时, %d 条 (%d 条去重后)\n", hours, totalCount, uniqueCount)
	} else {
		fmt.Fprintf(&b, "Alert Log — 最近 %d 小时, %d 条\n", hours, totalCount)
	}

	var errCount, warnCount int

	// Group consecutive entries with the same timestamp into multi-line events.
	prevTS := ""
	for _, ce := range collapsed {
		label, color := severityLabel(ce.entry)
		switch label {
		case "ERROR":
			errCount += ce.count
		case "WARN":
			warnCount += ce.count
		}

		displayMsg := ce.entry.msg
		if len(displayMsg) > 80 {
			displayMsg = displayMsg[:77] + "..."
		}

		repeat := ""
		if ce.count > 1 {
			repeat = fmt.Sprintf(" (\u00d7%d)", ce.count)
		}

		if ce.entry.ts == prevTS {
			// Same timestamp as previous — continuation line, indent without label/timestamp.
			fmt.Fprintf(&b, "                       %s%s\n", displayMsg, repeat)
		} else {
			// New timestamp — full line with label and timestamp.
			fmt.Fprintf(&b, "  %s[%s]%s %s  %s%s\n",
				color, label, ansiReset,
				ce.entry.ts, displayMsg, repeat)
			prevTS = ce.entry.ts
		}
	}

	// Summary line.
	if errCount > 0 || warnCount > 0 {
		b.WriteByte('\n')
		if errCount > 0 {
			fmt.Fprintf(&b, "  %sERROR: %d%s", ansiRed, errCount, ansiReset)
		}
		if warnCount > 0 {
			if errCount > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, "  %sWARN: %d%s", ansiAmber, warnCount, ansiReset)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

func toAlertLevel(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int64:
		return int(n)
	case int:
		return n
	default:
		i, _ := strconv.Atoi(fmt.Sprintf("%v", v))
		return i
	}
}

// parseAlertHours extracts a positive integer from args.
// Falls back to defaultAlertHours if the value is empty or invalid.
func parseAlertHours(args string) int {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return defaultAlertHours
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return defaultAlertHours
	}
	return n
}
