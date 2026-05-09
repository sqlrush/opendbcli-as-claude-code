/*-------------------------------------------------------------------------
 *
 * params.go
 *	  params — ParamsSkill plus helpers (NewParamsSkill) used by the
 *	  admin package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/admin/params.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

type ParamsSkill struct{ driver db.Driver }

func NewParamsSkill(driver db.Driver) *ParamsSkill { return &ParamsSkill{driver: driver} }

func (s *ParamsSkill) Name() string                      { return "params" }
func (s *ParamsSkill) Description() string                { return "搜索PostgreSQL参数" }
func (s *ParamsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ParamsSkill) Validate(_ skill.Params) error      { return nil }
func (s *ParamsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/params <pattern>", Examples: []string{"/params shared_buffers", "/params work_mem"}}
}
func (s *ParamsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "params", Description: "Search PostgreSQL parameters by pattern",
		Parameters: map[string]any{"name_filter": map[string]any{"type": "string", "description": "Parameter name filter"}}}
}

func (s *ParamsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	pattern := params.StringOr("args", "")
	if pattern == "" {
		pattern = "%"
	} else if pattern[0] != '%' {
		pattern = "%" + pattern + "%"
	}

	sqlStr := fmt.Sprintf(
		"SELECT name, setting, unit, short_desc, context FROM pg_settings WHERE name LIKE '%s' ORDER BY name",
		pattern,
	)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("未找到匹配参数 '%s'", pattern),
			Summary:  "0 matches",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("参数搜索 '%s' — %d 条", pattern, len(result.Rows)),
		Summary:  fmt.Sprintf("参数查询结果 (%q) — %d 条", pattern, len(result.Rows)),
	}, nil
}
