/*-------------------------------------------------------------------------
 *
 * slowsql.go
 *	  SlowSQLSkill queries v$sql for slow queries exceeding a given
 *	  threshold.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/slowsql.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const defaultSlowThresholdMS = 1000

const slowSQLQuery = `SELECT sql_id, ROUND(elapsed_time/1000, 2) as elapsed_ms, executions,
       rows_processed, buffer_gets, SUBSTR(sql_text, 1, 100) as sql_text
FROM v$sql
WHERE elapsed_time/1000 > :1
ORDER BY elapsed_time DESC
FETCH FIRST 20 ROWS ONLY`

// SlowSQLSkill queries v$sql for slow queries exceeding a given threshold.
type SlowSQLSkill struct {
	driver db.Driver
}

// NewSlowSQLSkill creates a SlowSQLSkill backed by the given driver.
func NewSlowSQLSkill(driver db.Driver) *SlowSQLSkill {
	return &SlowSQLSkill{driver: driver}
}

func (s *SlowSQLSkill) Name() string        { return "slowsql" }
func (s *SlowSQLSkill) Description() string  { return "Find slow SQL queries exceeding a threshold" }
func (s *SlowSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SlowSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "slowsql",
		Description: "Find slow SQL queries exceeding a threshold (default 1000ms)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Threshold in milliseconds (default 1000)",
				},
			},
		},
	}
}

func (s *SlowSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "slowsql",
		Aliases:  []string{"slow"},
		Usage:    "/slowsql [threshold_ms]",
		Examples: []string{"/slowsql", "/slowsql 5000"},
	}
}

func (s *SlowSQLSkill) Validate(_ skill.Params) error { return nil }

func (s *SlowSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	threshold := parseSlowThreshold(params.StringOr("args", ""))

	result, err := s.driver.Query(ctx, slowSQLQuery, threshold)
	if err != nil {
		return nil, fmt.Errorf("slow SQL query failed: %w", err)
	}

	// Batch FTS check for Oracle slow SQL.
	ftsSet := queryFTSSQLIDs(ctx, s.driver, result)

	// Build enhanced rendered output.
	rendered := renderSlowSQLWithPlan(result, ftsSet, threshold)

	summary := fmt.Sprintf("慢 SQL (累计耗时>%dms, 来自v$sql缓存) — %d 条",
		threshold, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
		Metadata: map[string]string{
			"threshold_ms": strconv.Itoa(threshold),
		},
	}, nil
}

// renderSlowSQLWithPlan builds the enhanced rendered string for slow SQL with Plan column.
func renderSlowSQLWithPlan(result *db.QueryResult, ftsSet map[string]bool, threshold int) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("慢 SQL (>%dms) — 0 条", threshold)
	}

	// Columns: sql_id, elapsed_ms, executions, rows_processed, buffer_gets, sql_text
	var lines []string
	var ftsWarnings []string

	for i, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		sqlID := cellStr(row[0])
		elapsedMs := cellStr(row[1])
		exec := cellStr(row[2])
		rowsProc := format.HumanNumber(cellFloat(row[3]))
		bufGets := format.HumanNumber(cellFloat(row[4]))
		sqlText := cellStr(row[5])

		planText := "IDX   "
		if ftsSet[sqlID] {
			planText = "\033[33mFTS \u26a0\033[0m  "
			ftsWarnings = append(ftsWarnings, fmt.Sprintf("#%d %s", i+1, sqlID))
		}

		line := fmt.Sprintf(" %2d  %-14s %10s %8s %8s %10s  %s%s",
			i+1, sqlID, elapsedMs, exec, rowsProc, bufGets, planText, sqlText)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("慢 SQL (>%dms) — %d 条", threshold, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-14s %10s %8s %8s %10s  %-6s %s",
		"#", "SQL_ID", "ELAPSED_MS", "EXEC", "ROWS", "BUF_GETS", "PLAN", "SQL_TEXT")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	if len(ftsWarnings) > 0 {
		footer := fmt.Sprintf("\033[33m⚠ 全表扫描: %s\033[0m \033[90m— /explain <sql_id> 查看执行计划\033[0m",
			strings.Join(ftsWarnings, ", "))
		sections = append(sections, format.PanelSection{
			Header: "提示",
			Lines:  []string{footer},
		})
	}

	return format.Panel(title, sections)
}

// parseSlowThreshold extracts a numeric threshold from the args string.
// Falls back to defaultSlowThresholdMS if the value is empty or non-numeric.
func parseSlowThreshold(args string) int {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return defaultSlowThresholdMS
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n <= 0 {
		return defaultSlowThresholdMS
	}
	return n
}
