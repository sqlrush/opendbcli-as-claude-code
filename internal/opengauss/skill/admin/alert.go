/*-------------------------------------------------------------------------
 *
 * alert.go
 *	  AlertSkill shows recent database conflicts, deadlocks, and temp
 *	  file usage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/admin/alert.go
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

const alertSQL = `SELECT
  datname,
  conflicts,
  deadlocks,
  temp_files,
  temp_bytes / 1048576 AS temp_mb,
  blk_read_time::numeric(12,1) AS blk_read_ms,
  blk_write_time::numeric(12,1) AS blk_write_ms,
  stats_reset::text
FROM pg_stat_database
WHERE datname NOT IN ('template0', 'template1')
  AND (conflicts > 0 OR deadlocks > 0 OR temp_files > 0)
ORDER BY deadlocks DESC, conflicts DESC`

// ANSI color codes for alert highlighting.
const (
	alertAnsiRed   = "\033[1;31m"
	alertAnsiAmber = "\033[1;33m"
	alertAnsiDim   = "\033[90m"
	alertAnsiReset = "\033[0m"
)

// AlertSkill shows recent database conflicts, deadlocks, and temp file usage.
type AlertSkill struct {
	driver db.Driver
}

// NewAlertSkill creates an AlertSkill backed by the given driver.
func NewAlertSkill(driver db.Driver) *AlertSkill {
	return &AlertSkill{driver: driver}
}

func (s *AlertSkill) Name() string                       { return "alert" }
func (s *AlertSkill) Description() string                { return "数据库冲突/死锁/临时文件" }
func (s *AlertSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *AlertSkill) Validate(_ skill.Params) error      { return nil }

func (s *AlertSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/alert", Examples: []string{"/alert"}}
}

func (s *AlertSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "alert",
		Description: "Show database conflicts, deadlocks, and temp file usage from pg_stat_database",
	}
}

func (s *AlertSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	// Try pg_current_logfile() first for log-based alerting
	logResult, logErr := s.driver.Query(ctx, "SELECT pg_current_logfile()")
	if logErr == nil && len(logResult.Rows) > 0 && asStr(logResult.Rows[0][0]) != "" {
		// Log file path is available, but we cannot read it directly via SQL
		// Fall through to pg_stat_database approach
	}

	result, err := s.driver.Query(ctx, alertSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无冲突/死锁/临时文件记录 (自 stats_reset 以来)",
			Summary:  "no alerts",
		}, nil
	}

	rendered := formatOGAlertPanel(result)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d 个数据库有异常记录", len(result.Rows)),
	}, nil
}

// ogAlertEntry holds one parsed pg_stat_database row.
type ogAlertEntry struct {
	datname    string
	conflicts  int64
	deadlocks  int64
	tempFiles  int64
	tempMB     int64
	blkReadMS  string
	blkWriteMS string
	statsReset string
}

// ogSeverityLabel returns the severity based on deadlock/conflict counts.
func ogSeverityLabel(e ogAlertEntry) (string, string) {
	if e.deadlocks > 0 {
		return "ERROR", alertAnsiRed
	}
	if e.conflicts > 0 {
		return "WARN", alertAnsiAmber
	}
	return "INFO", alertAnsiDim
}

// formatOGAlertPanel renders OpenGauss alert data with severity labels and compact layout.
func formatOGAlertPanel(qr *db.QueryResult) string {
	if qr == nil || len(qr.Rows) == 0 {
		return "无冲突/死锁/临时文件记录"
	}

	// Parse rows: datname, conflicts, deadlocks, temp_files, temp_mb,
	//             blk_read_ms, blk_write_ms, stats_reset
	entries := make([]ogAlertEntry, 0, len(qr.Rows))
	for _, row := range qr.Rows {
		if len(row) < 8 {
			continue
		}
		entries = append(entries, ogAlertEntry{
			datname:    fmt.Sprintf("%v", row[0]),
			conflicts:  toInt64(row[1]),
			deadlocks:  toInt64(row[2]),
			tempFiles:  toInt64(row[3]),
			tempMB:     toInt64(row[4]),
			blkReadMS:  fmt.Sprintf("%v", row[5]),
			blkWriteMS: fmt.Sprintf("%v", row[6]),
			statsReset: fmt.Sprintf("%v", row[7]),
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Database Alerts — %d 个数据库有异常\n", len(entries))

	var totalDeadlocks, totalConflicts int64

	for _, e := range entries {
		label, color := ogSeverityLabel(e)
		totalDeadlocks += e.deadlocks
		totalConflicts += e.conflicts

		// Build detail parts.
		var parts []string
		if e.deadlocks > 0 {
			parts = append(parts, fmt.Sprintf("deadlocks=%d", e.deadlocks))
		}
		if e.conflicts > 0 {
			parts = append(parts, fmt.Sprintf("conflicts=%d", e.conflicts))
		}
		if e.tempFiles > 0 {
			parts = append(parts, fmt.Sprintf("temp_files=%d (%dMB)", e.tempFiles, e.tempMB))
		}

		detail := strings.Join(parts, ", ")

		fmt.Fprintf(&b, "  %s[%s]%s %-20s  %s\n",
			color, label, alertAnsiReset,
			e.datname, detail)
	}

	// Summary line.
	if totalDeadlocks > 0 || totalConflicts > 0 {
		b.WriteByte('\n')
		if totalDeadlocks > 0 {
			fmt.Fprintf(&b, "  %sDeadlocks: %d%s", alertAnsiRed, totalDeadlocks, alertAnsiReset)
		}
		if totalConflicts > 0 {
			if totalDeadlocks > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, "  %sConflicts: %d%s", alertAnsiAmber, totalConflicts, alertAnsiReset)
		}
		b.WriteByte('\n')
	}

	return b.String()
}

// toInt64 converts an any value to int64.
func toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		i, _ := strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64)
		return i
	}
}
