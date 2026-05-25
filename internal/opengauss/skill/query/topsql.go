/*-------------------------------------------------------------------------
 *
 * topsql.go
 *	  topsql — TopSQLSkill plus helpers (NewTopSQLSkill) used by the
 *	  query package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/topsql.go
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

type ogSortInfo struct {
	col   string
	label string
}

var ogSortCols = map[string]ogSortInfo{
	"el": {"total_elapse_time DESC", "总执行时间"},
	"ae": {"total_elapse_time/NULLIF(n_calls,0) DESC", "平均执行时间"},
	"ex": {"n_calls DESC", "执行次数"},
	"lr": {"n_blocks_hit + n_blocks_fetched DESC", "逻辑读"},
	"rw": {"n_returned_rows DESC", "返回行数"},
}

// Query is trimmed to 80 chars at the DB side; Go-side helper further
// collapses whitespace so each /topsql entry stays on one line.
const topSQLTemplOG = `SELECT
  unique_sql_id,
  LEFT(REGEXP_REPLACE(query, E'\\s+', ' ', 'g'), 80) AS query,
  n_calls AS calls,
  ROUND(total_elapse_time/1000000::numeric, 2) AS total_sec,
  ROUND((total_elapse_time/NULLIF(n_calls,0))/1000::numeric, 2) AS avg_ms,
  n_returned_rows AS rows
FROM dbe_perf.statement
WHERE n_calls > 0
ORDER BY %s
LIMIT 20`

type TopSQLSkill struct{ driver db.Driver }

func NewTopSQLSkill(driver db.Driver) *TopSQLSkill { return &TopSQLSkill{driver: driver} }

func (s *TopSQLSkill) Name() string                      { return "topsql" }
func (s *TopSQLSkill) Description() string                { return "Top SQL (支持多种排序, dbe_perf.statement)" }
func (s *TopSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TopSQLSkill) Validate(_ skill.Params) error      { return nil }

func (s *TopSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:          "/topsql [sort_key]",
		Examples:       []string{"/topsql", "/topsql ae", "/topsql lr"},
		ArgCompletions: []string{"el", "ae", "ex", "lr", "rw"},
	}
}

func (s *TopSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "topsql",
		Description: "Show top SQL by elapsed/avg/exec/reads/rows from dbe_perf.statement",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[sort_key: el|ae|ex|lr|rw]",
				},
			},
		},
	}
}

func (s *TopSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	parts := strings.Fields(args)
	sortKey := "el"

	for _, p := range parts {
		if _, ok := ogSortCols[p]; ok {
			sortKey = p
		}
	}

	sortInfo := ogSortCols[sortKey]
	sqlStr := fmt.Sprintf(topSQLTemplOG, sortInfo.col)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := renderOGTopSQL(result, sortInfo.label)
	summary := fmt.Sprintf("Top SQL (dbe_perf.statement, 按%s排序) — %d 条", sortInfo.label, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderOGTopSQL builds the enhanced rendered string with HumanNumber formatting.
func renderOGTopSQL(result *db.QueryResult, sortLabel string) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("Top SQL (dbe_perf.statement, 按%s) — 0 条", sortLabel)
	}

	// Columns: unique_sql_id, query, calls, total_sec, avg_ms, rows
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 6 {
			continue
		}
		sqlID := ogCellStr(row[0])
		query := ogCellStr(row[1])
		// Defensive single-line normalization in case the DB-side regex
		// collapse didn't apply (e.g. older OG versions).
		query = strings.ReplaceAll(query, "\n", " ")
		query = strings.ReplaceAll(query, "\t", " ")
		if len(query) > 80 {
			query = query[:80] + "…"
		}
		calls := ogCellStr(row[2])
		totalSec := ogCellStr(row[3])
		avgMs := ogCellStr(row[4])
		rows := format.HumanNumber(ogCellFloat(row[5]))

		line := fmt.Sprintf(" %2d  %-20s %8s %8s %10s %10s  %s",
			i+1, sqlID, calls, totalSec, avgMs, rows, query)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("Top SQL (dbe_perf.statement, 按%s) — %d 条", sortLabel, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-20s %8s %8s %10s %10s  %s",
		"#", "SQL_ID", "CALLS", "TOTAL_S", "AVG_MS", "ROWS", "QUERY")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}

func ogCellStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func ogCellFloat(v any) float64 {
	if v == nil {
		return 0
	}
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
	default:
		f, _ := strconv.ParseFloat(fmt.Sprintf("%v", v), 64)
		return f
	}
}
