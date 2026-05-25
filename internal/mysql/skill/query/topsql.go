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
 *	  internal/mysql/skill/query/topsql.go
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

type mysqlSortInfo struct {
	col   string
	label string
}

var mysqlSortCols = map[string]mysqlSortInfo{
	"el": {"SUM_TIMER_WAIT DESC", "总耗时"},
	"ae": {"SUM_TIMER_WAIT/COUNT_STAR DESC", "平均耗时"},
	"ex": {"COUNT_STAR DESC", "执行次数"},
	"re": {"SUM_ROWS_EXAMINED DESC", "扫描行数"},
	"rs": {"SUM_ROWS_SENT DESC", "返回行数"},
}

const topSQLTemplateMySQL = `SELECT
  LEFT(DIGEST_TEXT, 120) AS sql_text,
  SCHEMA_NAME AS db_name,
  COUNT_STAR AS exec_count,
  ROUND(SUM_TIMER_WAIT / 1e12, 2) AS total_sec,
  ROUND(SUM_TIMER_WAIT / COUNT_STAR / 1e12, 4) AS avg_sec,
  SUM_ROWS_EXAMINED AS rows_examined,
  SUM_ROWS_SENT AS rows_sent
FROM performance_schema.events_statements_summary_by_digest
WHERE COUNT_STAR > 0
  AND LAST_SEEN > NOW() - INTERVAL %d MINUTE
ORDER BY %s
LIMIT 20`

type TopSQLSkill struct {
	driver db.Driver
}

func NewTopSQLSkill(driver db.Driver) *TopSQLSkill {
	return &TopSQLSkill{driver: driver}
}

func (s *TopSQLSkill) Name() string        { return "topsql" }
func (s *TopSQLSkill) Description() string  { return "Top SQL (支持多种排序)" }
func (s *TopSQLSkill) Category() string     { return "query" }
func (s *TopSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *TopSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:          "/topsql [minutes] [sort_key]",
		Examples:       []string{"/topsql", "/topsql 30", "/topsql ex", "/topsql 60 re"},
		ArgCompletions: []string{"el", "ae", "ex", "re", "rs"},
	}
}

func (s *TopSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "topsql",
		Description: "Show top SQL statements from performance_schema, sortable by elapsed/avg/exec/rows",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "[minutes] [sort_key: el|ae|ex|re|rs]",
				},
			},
		},
	}
}

func (s *TopSQLSkill) Validate(_ skill.Params) error { return nil }

func (s *TopSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := params.StringOr("args", "")
	parts := strings.Fields(args)
	minutes := 60
	sortKey := "el"

	for _, p := range parts {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			minutes = n
		} else if _, ok := mysqlSortCols[p]; ok {
			sortKey = p
		}
	}

	// Also accept minutes from tool parameter.
	if len(parts) == 0 {
		minutes = params.IntOr("minutes", 60)
	}

	sortInfo := mysqlSortCols[sortKey]
	sqlStr := fmt.Sprintf(topSQLTemplateMySQL, minutes, sortInfo.col)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := renderMySQLTopSQL(result, minutes, sortInfo.label)
	summary := fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s排序) — %d 条", minutes, sortInfo.label, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderMySQLTopSQL builds the enhanced rendered string with HumanNumber formatting.
func renderMySQLTopSQL(result *db.QueryResult, minutes int, sortLabel string) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s) — 0 条", minutes, sortLabel)
	}

	// Columns: sql_text, db_name, exec_count, total_sec, avg_sec, rows_examined, rows_sent
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 7 {
			continue
		}
		sqlText := mysqlCellStr(row[0])
		dbName := mysqlCellStr(row[1])
		execCount := mysqlCellStr(row[2])
		totalSec := mysqlCellStr(row[3])
		avgSec := mysqlCellStr(row[4])
		rowsExamined := format.HumanNumber(mysqlCellFloat(row[5]))
		rowsSent := format.HumanNumber(mysqlCellFloat(row[6]))

		line := fmt.Sprintf(" %2d  %-8s %8s %8s %10s %10s %10s  %s",
			i+1, dbName, execCount, totalSec, avgSec, rowsExamined, rowsSent, sqlText)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("Top SQL (最近 %d 分钟, 按%s) — %d 条", minutes, sortLabel, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-8s %8s %8s %10s %10s %10s  %s",
		"#", "DB", "EXEC", "TOTAL_S", "AVG_S", "EXAMINED", "SENT", "SQL_TEXT")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}

func mysqlCellStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func mysqlCellFloat(v any) float64 {
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
