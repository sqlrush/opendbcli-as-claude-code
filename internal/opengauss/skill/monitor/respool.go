/*-------------------------------------------------------------------------
 *
 * respool.go
 *	  ResPoolSkill shows resource pool status from pg_resource_pool
 *	  (Workload Manager).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/respool.go
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

const resPoolSQL = `SELECT respool_name, mem_percent, cpu_affinity, active_statements, max_dop,
  memory_limit, parentid
FROM pg_resource_pool
ORDER BY respool_name`

// ResPoolSkill shows resource pool status from pg_resource_pool (Workload Manager).
type ResPoolSkill struct{ driver db.Driver }

// NewResPoolSkill creates a ResPoolSkill backed by the given driver.
func NewResPoolSkill(driver db.Driver) *ResPoolSkill {
	return &ResPoolSkill{driver: driver}
}

func (s *ResPoolSkill) Name() string                       { return "respool" }
func (s *ResPoolSkill) Description() string                { return "资源池状态 (Workload Manager)" }
func (s *ResPoolSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ResPoolSkill) Validate(_ skill.Params) error      { return nil }
func (s *ResPoolSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/respool"} }

func (s *ResPoolSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "respool",
		Description: "Show OpenGauss resource pool status from pg_resource_pool (Workload Manager)",
	}
}

func (s *ResPoolSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, resPoolSQL)
	if err != nil {
		return &skill.Result{
			Type:    skill.ResultError,
			Summary: fmt.Sprintf("pg_resource_pool not accessible: %s", err.Error()),
		}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无资源池数据",
			Summary:  "no resource pools",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("资源池 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个资源池", len(result.Rows)),
	}, nil
}
