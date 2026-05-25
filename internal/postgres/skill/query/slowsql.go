/*-------------------------------------------------------------------------
 *
 * slowsql.go
 *	  slowsql — SlowSQLSkill plus helpers (NewSlowSQLSkill) used by
 *	  the query package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/query/slowsql.go
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

const slowSQLTemplate = `SELECT
  queryid, LEFT(query, 120) AS query,
  calls,
  ROUND(mean_exec_time::numeric, 2) AS avg_ms,
  ROUND(max_exec_time::numeric, 2) AS max_ms,
  rows
FROM pg_stat_statements
WHERE mean_exec_time > %d
  AND calls > 0
ORDER BY mean_exec_time DESC
LIMIT 20`

type SlowSQLSkill struct{ driver db.Driver }

func NewSlowSQLSkill(driver db.Driver) *SlowSQLSkill { return &SlowSQLSkill{driver: driver} }

func (s *SlowSQLSkill) Name() string                      { return "slowsql" }
func (s *SlowSQLSkill) Description() string                { return "慢查询 (需 pg_stat_statements)" }
func (s *SlowSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SlowSQLSkill) Validate(_ skill.Params) error      { return nil }
func (s *SlowSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/slowsql [threshold_ms]", Examples: []string{"/slowsql", "/slowsql 5000"}}
}
func (s *SlowSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "slowsql",
		Description: "Show slow SQL exceeding threshold from pg_stat_statements",
		Parameters:  map[string]any{"threshold_ms": map[string]any{"type": "integer", "description": "Threshold in ms (default 1000)"}},
	}
}

func (s *SlowSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	thresholdMs := 1000
	if args != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(args)); err == nil {
			thresholdMs = v
		}
	} else {
		thresholdMs = params.IntOr("threshold_ms", 1000)
	}
	result, err := s.driver.Query(ctx, fmt.Sprintf(slowSQLTemplate, thresholdMs))
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := renderPGSlowSQL(result, thresholdMs)
	summary := fmt.Sprintf("慢 SQL (>%dms, pg_stat_statements) — %d 条", thresholdMs, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderPGSlowSQL builds the enhanced rendered string with HumanNumber formatting.
func renderPGSlowSQL(result *db.QueryResult, thresholdMs int) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("慢 SQL (>%dms) — 0 条", thresholdMs)
	}

	// Columns: queryid, query, calls, avg_ms, max_ms, rows
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		queryID := pgCellStr(row[0])
		query := pgCellStr(row[1])
		calls := pgCellStr(row[2])
		avgMs := pgCellStr(row[3])
		maxMs := pgCellStr(row[4])
		rows := format.HumanNumber(pgCellFloat(row[5]))

		line := fmt.Sprintf(" %2d  %-20s %8s %10s %10s %10s  %s",
			i+1, queryID, calls, avgMs, maxMs, rows, query)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("慢 SQL (>%dms, pg_stat_statements) — %d 条", thresholdMs, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-20s %8s %10s %10s %10s  %s",
		"#", "QUERYID", "CALLS", "AVG_MS", "MAX_MS", "ROWS", "QUERY")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}
