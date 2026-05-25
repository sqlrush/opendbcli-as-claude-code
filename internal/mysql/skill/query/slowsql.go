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
 *	  internal/mysql/skill/query/slowsql.go
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
  LEFT(DIGEST_TEXT, 120) AS sql_text,
  SCHEMA_NAME AS db_name,
  COUNT_STAR AS exec_count,
  ROUND(MAX_TIMER_WAIT / 1e12, 2) AS max_sec,
  ROUND(AVG_TIMER_WAIT / 1e12, 2) AS avg_sec,
  SUM_ROWS_SENT AS rows_sent,
  SUM_ROWS_EXAMINED AS rows_examined
FROM performance_schema.events_statements_summary_by_digest
WHERE AVG_TIMER_WAIT / 1e9 > %d
  AND COUNT_STAR > 0
ORDER BY MAX_TIMER_WAIT DESC
LIMIT 20`

type SlowSQLSkill struct {
	driver db.Driver
}

func NewSlowSQLSkill(driver db.Driver) *SlowSQLSkill {
	return &SlowSQLSkill{driver: driver}
}

func (s *SlowSQLSkill) Name() string        { return "slowsql" }
func (s *SlowSQLSkill) Description() string  { return "慢查询 (按平均耗时过滤)" }
func (s *SlowSQLSkill) Category() string     { return "query" }
func (s *SlowSQLSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SlowSQLSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/slowsql [threshold_ms]",
		Examples: []string{"/slowsql", "/slowsql 5000"},
	}
}

func (s *SlowSQLSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "slowsql",
		Description: "Show slow SQL statements exceeding the given threshold from performance_schema",
		Parameters:  map[string]any{"threshold_ms": map[string]any{"type": "integer", "description": "Threshold in milliseconds (default 1000)"}},
	}
}

func (s *SlowSQLSkill) Validate(_ skill.Params) error { return nil }

func (s *SlowSQLSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	thresholdMs := 1000
	args := params.StringOr("args", "")
	if args != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(args)); err == nil {
			thresholdMs = v
		}
	} else {
		thresholdMs = params.IntOr("threshold_ms", 1000)
	}
	sqlStr := fmt.Sprintf(slowSQLTemplate, thresholdMs)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	rendered := renderMySQLSlowSQL(result, thresholdMs)
	summary := fmt.Sprintf("慢 SQL (>%dms) — %d 条", thresholdMs, len(result.Rows))

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     result,
		Rendered: rendered,
		Summary:  summary,
	}, nil
}

// renderMySQLSlowSQL builds the enhanced rendered string with HumanNumber formatting.
func renderMySQLSlowSQL(result *db.QueryResult, thresholdMs int) string {
	if result == nil || len(result.Rows) == 0 {
		return fmt.Sprintf("慢 SQL (>%dms) — 0 条", thresholdMs)
	}

	// Columns: sql_text, db_name, exec_count, max_sec, avg_sec, rows_sent, rows_examined
	var lines []string

	for i, row := range result.Rows {
		if len(row) < 7 {
			continue
		}
		sqlText := mysqlCellStr(row[0])
		dbName := mysqlCellStr(row[1])
		execCount := mysqlCellStr(row[2])
		maxSec := mysqlCellStr(row[3])
		avgSec := mysqlCellStr(row[4])
		rowsSent := format.HumanNumber(mysqlCellFloat(row[5]))
		rowsExamined := format.HumanNumber(mysqlCellFloat(row[6]))

		line := fmt.Sprintf(" %2d  %-8s %8s %8s %10s %10s %10s  %s",
			i+1, dbName, execCount, maxSec, avgSec, rowsSent, rowsExamined, sqlText)
		lines = append(lines, line)
	}

	title := fmt.Sprintf("慢 SQL (>%dms) — %d 条", thresholdMs, len(result.Rows))

	header := fmt.Sprintf(" %2s  %-8s %8s %8s %10s %10s %10s  %s",
		"#", "DB", "EXEC", "MAX_S", "AVG_S", "SENT", "EXAMINED", "SQL_TEXT")

	sections := []format.PanelSection{
		{Lines: append([]string{header}, lines...)},
	}

	return format.Panel(title, sections)
}
