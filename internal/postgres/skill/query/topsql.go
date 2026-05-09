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
 *	  internal/postgres/skill/query/topsql.go
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

type pgSortInfo struct {
	col   string
	label string
}

var pgSortCols = map[string]pgSortInfo{
	"el": {"total_exec_time DESC", "总执行时间"},
	"ae": {"mean_exec_time DESC", "平均执行时间"},
	"ex": {"calls DESC", "执行次数"},
	"lr": {"shared_blks_hit + shared_blks_read DESC", "逻辑读"},
	"rw": {"rows DESC", "返回行数"},
	"mx": {"max_exec_time DESC", "单次最长执行时间"}, // P06: recent/max sort
}

// P06: Added max_exec_time column for "single longest execution" analysis.
const topSQLTemplPG = `SELECT
  queryid, LEFT(query, 120) AS query,
  calls,
  ROUND(total_exec_time::numeric / 1000, 2) AS total_sec,
  ROUND(mean_exec_time::numeric / 1000, 4) AS avg_sec,
  ROUND(max_exec_time::numeric / 1000, 2) AS max_sec,
  shared_blks_hit + shared_blks_read AS total_blks,
  rows
FROM pg_stat_statements
WHERE calls > 0
ORDER BY %s
LIMIT 20`

type TopSQLSkill struct{ driver db.Driver }

func NewTopSQLSkill(driver db.Driver) *TopSQLSkill { return &TopSQLSkill{driver: driver} }

func (s *TopSQLSkill) Name() string                      { return "topsql" }
func (s *TopSQLSkill) Description() string                { return "Top SQL (支持多种排序)" }
func (s *TopSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *TopSQLSkill) Validate(_ skill.Params) error      { return nil }

func (s *TopSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:          "/topsql [sort_key]",
		Examples:       []string{"/topsql", "/topsql ae", "/topsql lr"},
		ArgCompletions: []string{"el", "ae", "ex", "lr", "rw", "mx"},
	}
}

func (s *TopSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "topsql",
		Description: "Show top SQL by elapsed/avg/exec/reads/rows from pg_stat_statements",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[sort_key: el|ae|ex|lr|rw|mx] mx=单次最长执行时间",
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
		if _, ok := pgSortCols[p]; ok {
			sortKey = p
		}
	}

	sortInfo := pgSortCols[sortKey]
	sqlStr := fmt.Sprintf(topSQLTemplPG, sortInfo.col)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := renderPGTopSQL(result, sortInfo.label)
	summary := fmt.Sprintf("Top SQL (pg_stat_statements, 按%s排序) — %d 条", sortInfo.label, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderPGTopSQL builds the enhanced rendered string with HumanNumber formatting.
func renderPGTopSQL(result *db.QueryResult, sortLabel string) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("Top SQL (pg_stat_statements, 按%s) — 0 条", sortLabel)
	}

	// Columns: queryid, query, calls, total_sec, avg_sec, max_sec, total_blks, rows
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 8 {
			continue
		}
		queryID := pgCellStr(row[0])
		query := pgCellStr(row[1])
		calls := pgCellStr(row[2])
		totalSec := pgCellStr(row[3])
		avgSec := pgCellStr(row[4])
		maxSec := pgCellStr(row[5])
		totalBlks := format.HumanNumber(pgCellFloat(row[6]))
		rows := format.HumanNumber(pgCellFloat(row[7]))

		line := fmt.Sprintf(" %2d  %-20s %8s %8s %10s %8s %10s %10s  %s",
			i+1, queryID, calls, totalSec, avgSec, maxSec, totalBlks, rows, query)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("Top SQL (pg_stat_statements, 按%s) — %d 条", sortLabel, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-20s %8s %8s %10s %8s %10s %10s  %s",
		"#", "QUERYID", "CALLS", "TOTAL_S", "AVG_S", "MAX_S", "BLKS", "ROWS", "QUERY")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}

func pgCellStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func pgCellFloat(v any) float64 {
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
