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
	"regexp"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const slowSQLTemplate = `SELECT
  unique_sql_id,
  LEFT(REGEXP_REPLACE(query, E'\\s+', ' ', 'g'), 180) AS query,
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

func (s *SlowSQLSkill) Name() string                       { return "slowsql" }
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

var slowSQLWhitespace = regexp.MustCompile(`\s+`)

// renderOGSlowSQL builds a compact, one-line-per-SQL list. /slowsql is a
// locator view: it should stay scannable and hand users a SQL_ID for /sqltune
// or /sqlfetch, rather than dumping multi-line DO/WITH bodies into a table.
func renderOGSlowSQL(result *db.QueryResult, thresholdMs int) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("慢 SQL (>%dms) — 0 条", thresholdMs)
	}

	// Columns: unique_sql_id, query, calls, avg_ms, total_sec, rows
	lines := []string{fmt.Sprintf(" %2s  %-12s %8s %11s %10s %8s  %s",
		"#", "SQL_ID", "CALLS", "AVG_MS", "TOTAL_S", "ROWS", "SQL 摘要")}

	for i, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		sqlID := ogCellStr(row[0])
		query := ogOneLineSQL(ogCellStr(row[1]), 64)
		calls := format.HumanNumber(ogCellFloat(row[2]))
		avgMs := ogCellStr(row[3])
		totalSec := ogCellStr(row[4])
		rows := format.HumanNumber(ogCellFloat(row[5]))

		line := fmt.Sprintf(" %2d  %-12s %8s %11s %10s %8s  %s",
			i+1, sqlID, calls, avgMs, totalSec, rows, query)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("慢 SQL (>%dms, dbe_perf.statement) — %d 条", thresholdMs, len(result.Rows))
	sections := []format.PanelSection{
		{Lines: lines},
		{Header: "下一步", Lines: []string{
			"用 /sqltune <SQL_ID> 分析具体 SQL；用 /sqlfetch <SQL_ID> 查看可 EXPLAIN 的 SQL 文本。",
			"多行 SQL 已折叠为单行摘要，避免 DO/WITH/INSERT SELECT 撑破终端表格。",
		}},
	}

	return format.Panel(title, sections)
}

func ogOneLineSQL(sqlText string, maxWidth int) string {
	query := strings.TrimSpace(slowSQLWhitespace.ReplaceAllString(sqlText, " "))
	if query == "" {
		return "-"
	}
	if format.DisplayWidth(query) > maxWidth {
		return format.TruncateWidth(query, maxWidth-1) + "…"
	}
	return query
}
