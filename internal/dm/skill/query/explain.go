/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  explain — ExplainSkill plus helpers (NewExplainSkill) used by
 *	  the query package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/explain.go
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

type ExplainSkill struct{ driver db.Driver }

func NewExplainSkill(driver db.Driver) *ExplainSkill { return &ExplainSkill{driver: driver} }

func (s *ExplainSkill) Name() string                       { return "explain" }
func (s *ExplainSkill) Description() string                { return "EXPLAIN <sql> 输出三元组 [代价ms, 行数, 字节数]" }
func (s *ExplainSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ExplainSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "explain",
		Description: "Run EXPLAIN <sql> on DM",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql": map[string]any{"type": "string", "description": "SQL to explain"},
			},
			"required": []string{"sql"},
		},
	}
}
func (s *ExplainSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "explain", Usage: "/explain <SQL>"}
}

func (s *ExplainSkill) Validate(p skill.Params) error {
	if strings.TrimSpace(p.StringOr("args", p.StringOr("sql", ""))) == "" {
		return fmt.Errorf("explain 需要 SQL 参数")
	}
	return nil
}

func (s *ExplainSkill) Execute(ctx context.Context, p skill.Params) (*skill.Result, error) {
	q := strings.TrimSpace(p.StringOr("args", p.StringOr("sql", "")))
	r, err := s.driver.Query(ctx, "EXPLAIN "+q)
	if err != nil {
		return nil, fmt.Errorf("dm explain: %w", err)
	}
	// Rendered 加 hint 让 LLM 知道输出格式
	rendered := fmt.Sprintf("%v\n\n[summary]\nexplain_lines: %d\nformat_note: 每行末尾 [代价ms, 估算行数, 字节数]\n", r, len(r.Rows))
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: rendered,
		Summary:  "EXPLAIN output (cost_ms, rows, bytes)",
	}, nil
}
