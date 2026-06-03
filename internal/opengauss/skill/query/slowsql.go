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
 *	  internal/opengauss/skill/query/slowsql.go
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
  unique_sql_id,
  LEFT(query, 120) AS query,
  n_calls AS calls,
  ROUND((total_elapse_time/NULLIF(n_calls,0))/1000::numeric, 2) AS avg_ms,
  ROUND(total_elapse_time/1000000::numeric, 2) AS total_sec,
  n_returned_rows AS rows
FROM dbe_perf.statement
WHERE (total_elapse_time/NULLIF(n_calls,0))/1000 > %d
  AND n_calls > 0
ORDER BY total_elapse_time/NULLIF(n_calls,0) DESC
LIMIT 20`

type SlowSQLSkill struct{ driver db.Driver }

func NewSlowSQLSkill(driver db.Driver) *SlowSQLSkill { return &SlowSQLSkill{driver: driver} }

func (s *SlowSQLSkill) Name() string                      { return "slowsql" }
func (s *SlowSQLSkill) Description() string                { return "慢查询 (dbe_perf.statement)" }
func (s *SlowSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SlowSQLSkill) Validate(_ skill.Params) error      { return nil }
func (s *SlowSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/slowsql [threshold_ms]", Examples: []string{"/slowsql", "/slowsql 5000"}}
}
func (s *SlowSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "slowsql",
		Description: "Show slow SQL exceeding threshold from dbe_perf.statement",
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

	rendered := renderOGSlowSQL(result, thresholdMs)
	summary := fmt.Sprintf("慢 SQL (>%dms, dbe_perf.statement) — %d 条", thresholdMs, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderOGSlowSQL builds the enhanced rendered string with HumanNumber formatting.
func renderOGSlowSQL(result *db.QueryResult, thresholdMs int) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("慢 SQL (>%dms) — 0 条", thresholdMs)
	}

	// Columns: unique_sql_id, query, calls, avg_ms, total_sec, rows
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		sqlID := ogCellStr(row[0])
		query := ogCellStr(row[1])
		calls := ogCellStr(row[2])
		avgMs := ogCellStr(row[3])
		totalSec := ogCellStr(row[4])
		rows := format.HumanNumber(ogCellFloat(row[5]))

		line := fmt.Sprintf(" %2d  %-20s %8s %10s %8s %10s  %s",
			i+1, sqlID, calls, avgMs, totalSec, rows, query)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("慢 SQL (>%dms, dbe_perf.statement) — %d 条", thresholdMs, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-20s %8s %10s %8s %10s  %s",
		"#", "SQL_ID", "CALLS", "AVG_MS", "TOTAL_S", "ROWS", "QUERY")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}
