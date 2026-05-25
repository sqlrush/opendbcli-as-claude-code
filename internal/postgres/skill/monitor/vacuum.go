/*-------------------------------------------------------------------------
 *
 * vacuum.go
 *	  vacuum — VacuumSkill plus helpers (NewVacuumSkill) used by the
 *	  monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/vacuum.go
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

const vacuumSQL = `SELECT
  schemaname, relname,
  n_live_tup, n_dead_tup,
  CASE WHEN n_live_tup > 0
    THEN ROUND(n_dead_tup::numeric / n_live_tup * 100, 1)
    ELSE 0 END AS dead_pct,
  last_vacuum, last_autovacuum,
  last_analyze, last_autoanalyze,
  n_mod_since_analyze
FROM pg_stat_user_tables
WHERE n_dead_tup > 0
ORDER BY n_dead_tup DESC
LIMIT 30`

type VacuumSkill struct{ driver db.Driver }

func NewVacuumSkill(driver db.Driver) *VacuumSkill { return &VacuumSkill{driver: driver} }

func (s *VacuumSkill) Name() string                      { return "vacuum" }
func (s *VacuumSkill) Description() string                { return "Vacuum状态和Dead Tuples" }
func (s *VacuumSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *VacuumSkill) Validate(_ skill.Params) error      { return nil }
func (s *VacuumSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/vacuum"} }
func (s *VacuumSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "vacuum", Description: "Show vacuum status, dead tuples, and last vacuum/analyze times per table"}
}

func (s *VacuumSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, vacuumSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无 dead tuple 数据",
			Summary:  "no dead tuples",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("Vacuum 状态 — %d 个表有 dead tuples", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个表有 dead tuples", len(result.Rows)),
	}, nil
}
