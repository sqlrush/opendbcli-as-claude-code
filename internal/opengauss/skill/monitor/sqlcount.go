/*-------------------------------------------------------------------------
 *
 * sqlcount.go
 *	  SQLCountSkill shows SQL type statistics from gs_sql_count.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/sqlcount.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// sqlCountSQL adds the SELECT avg/max latency columns gs_sql_count exposes
// so users get both volume and response-time signals in one view.
//
// gs_sql_count has per-type total/avg/max/min elapse columns but no real
// p95 / p99 percentile — OG 5.0 simply doesn't track that (older WDR
// versions only compute mean+extrema). We surface avg + max as the closest
// latency proxies available. mergeinto_count was missing from v1 and is
// now included.
const sqlCountSQL = `SELECT user_name,
  select_count, update_count, insert_count, delete_count,
  mergeinto_count, ddl_count, dml_count, dcl_count,
  select_count + update_count + insert_count + delete_count + mergeinto_count AS total_dml,
  avg_select_elapse AS avg_sel_us,
  max_select_elapse AS max_sel_us
FROM gs_sql_count
ORDER BY select_count + update_count + insert_count + delete_count + mergeinto_count DESC
LIMIT 20`

// SQLCountSkill shows SQL type statistics from gs_sql_count.
type SQLCountSkill struct{ driver db.Driver }

// NewSQLCountSkill creates a SQLCountSkill backed by the given driver.
func NewSQLCountSkill(driver db.Driver) *SQLCountSkill {
	return &SQLCountSkill{driver: driver}
}

func (s *SQLCountSkill) Name() string                       { return "sqlcount" }
func (s *SQLCountSkill) Description() string                { return "SQL类型统计" }
func (s *SQLCountSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SQLCountSkill) Validate(_ skill.Params) error      { return nil }
func (s *SQLCountSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/sqlcount"} }

func (s *SQLCountSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sqlcount",
		Description: "Show SQL type statistics per user from gs_sql_count (SELECT, INSERT, UPDATE, DELETE, DDL, DML, DCL)",
	}
}

func (s *SQLCountSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, sqlCountSQL)
	if err != nil {
		return &skill.Result{
			Type:    skill.ResultError,
			Summary: fmt.Sprintf("gs_sql_count not accessible: %s", err.Error()),
		}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无 SQL 统计数据",
			Summary:  "no sql count data",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("SQL 类型统计 — %d 个用户", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个用户的 SQL 统计", len(result.Rows)),
	}, nil
}
