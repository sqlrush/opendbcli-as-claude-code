/*-------------------------------------------------------------------------
 *
 * planhistory.go
 *	  PlanHistorySkill shows recent execution plans for a
 *	  unique_query_id, with the goal of detecting plan regressions (the
 *	  same SQL running with a different plan between executions).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/planhistory.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

// planHistorySQL pulls recent plan executions for a query_id (unique_sql_id
// in OG terminology). dbe_perf.statement_history is the WDR-backed view.
//
// OG 5.0 column names (verified against live schema 2026-04-23):
//   no total_elapse_time — use db_time or execution_time
//   no hard_parse       — use n_hard_parse
//   no rows_processed   — use n_returned_rows + n_tuples_fetched
const planHistorySQLTmpl = `SELECT
  start_time::text,
  unique_query_id,
  LEFT(query, 60) AS query_head,
  db_time,
  execution_time,
  cpu_time,
  n_hard_parse,
  n_returned_rows,
  n_tuples_fetched,
  query_plan
FROM dbe_perf.statement_history
WHERE unique_query_id = %s
ORDER BY start_time DESC
LIMIT 10`

// PlanHistorySkill shows recent execution plans for a unique_query_id, with
// the goal of detecting plan regressions (the same SQL running with a
// different plan between executions).
type PlanHistorySkill struct{ driver db.Driver }

// NewPlanHistorySkill creates a PlanHistorySkill.
func NewPlanHistorySkill(driver db.Driver) *PlanHistorySkill {
	return &PlanHistorySkill{driver: driver}
}

func (s *PlanHistorySkill) Name() string                       { return "planhistory" }
func (s *PlanHistorySkill) Description() string                { return "执行计划历史（检测计划回归）" }
func (s *PlanHistorySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *PlanHistorySkill) Validate(_ skill.Params) error      { return nil }

func (s *PlanHistorySkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/planhistory <unique_query_id>",
		Examples: []string{"/planhistory 1234567890"},
	}
}

func (s *PlanHistorySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "planhistory",
		Description: "Show plan history for a unique_query_id to detect regressions (from dbe_perf.statement_history)",
		Parameters: map[string]any{
			"query_id": map[string]any{"type": "string", "description": "unique_query_id (from /topsql)"},
		},
	}
}

func (s *PlanHistorySkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	id := strings.TrimSpace(params.StringOr("args", ""))
	if id == "" {
		id = strings.TrimSpace(params.StringOr("query_id", ""))
	}
	if id == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "usage: /planhistory <unique_query_id>"}, nil
	}

	// Use fmt.Sprintf; the value is expected to be a numeric id from /topsql,
	// which the Go driver validates.
	result, err := s.driver.Query(ctx, fmt.Sprintf(planHistorySQLTmpl, id))
	if err != nil {
		msg := fmt.Sprintf("查询计划历史失败: %v\n提示: 需要 dbe_perf schema 和 enable_wdr_snapshot=on。", err)
		return &skill.Result{Type: skill.ResultText, Rendered: msg, Summary: "planhistory unavailable"}, nil
	}
	rows := 0
	if result != nil {
		rows = len(result.Rows)
	}
	if rows == 0 {
		return &skill.Result{
			Type: skill.ResultText,
			Rendered: fmt.Sprintf(
				"unique_query_id=%s 在 dbe_perf.statement_history 中无记录。\n\n可能原因：\n"+
					"  · WDR 采样未开启（SHOW enable_wdr_snapshot）\n"+
					"  · WDR 快照已被清理（默认保留 7 天，见 wdr_snapshot_retention_days）\n"+
					"  · unique_query_id 错误；从 /topsql 获取最新 ID",
				id,
			),
			Summary: "no plan history",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("计划历史 unique_query_id=%s — %d 条", id, rows),
		Summary:  fmt.Sprintf("%d history entries", rows),
	}, nil
}
