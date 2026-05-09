/*-------------------------------------------------------------------------
 *
 * params.go
 *	  ParamsSkill shows Oracle initialization parameters matching a
 *	  pattern.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/params.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const paramsSQL = `SELECT name, value, description FROM v$parameter
WHERE UPPER(name) LIKE UPPER(:1)
ORDER BY name`

// ParamsSkill shows Oracle initialization parameters matching a pattern.
type ParamsSkill struct {
	driver db.Driver
}

// NewParamsSkill creates a ParamsSkill backed by the given driver.
func NewParamsSkill(driver db.Driver) *ParamsSkill {
	return &ParamsSkill{driver: driver}
}

func (s *ParamsSkill) Name() string        { return "params" }
func (s *ParamsSkill) Description() string { return "Search Oracle init parameters" }
func (s *ParamsSkill) SecurityLevel() skill.SecurityLevel {
	return skill.LevelReadOnly
}

func (s *ParamsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "params",
		Description: "Search Oracle init parameters by name pattern",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "Search pattern (e.g. 'sga', 'memory')",
				},
			},
		},
	}
}

func (s *ParamsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "params",
		Usage:    "/params [pattern]",
		Examples: []string{"/params", "/params sga", "/params memory"},
	}
}

func (s *ParamsSkill) Validate(_ skill.Params) error { return nil }

func (s *ParamsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	pattern := buildParamPattern(params.StringOr("args", ""))

	result, err := s.driver.Query(ctx, paramsSQL, pattern)
	if err != nil {
		return nil, fmt.Errorf("querying parameters: %w", err)
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("参数搜索 '%s' — %d 个", pattern, len(result.Rows)),
		Summary:  fmt.Sprintf("参数查询结果 (%q) — %d 个", pattern, len(result.Rows)),
		Metadata: map[string]string{
			"pattern": pattern,
		},
	}, nil
}

// buildParamPattern wraps a search term with % wildcards.
// If the input is empty, returns "%" to match all parameters.
func buildParamPattern(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return "%"
	}
	return "%" + trimmed + "%"
}
