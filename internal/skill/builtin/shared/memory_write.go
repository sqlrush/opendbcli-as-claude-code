/*-------------------------------------------------------------------------
 *
 * memory_write.go
 *	  MemoryWriteSkill allows the LLM to persist a memory entry for the
 *	  current instance.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/memory_write.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/skill"
)

// MemoryWriteSkill allows the LLM to persist a memory entry for the current instance.
type MemoryWriteSkill struct {
	store *memory.Store
}

func NewMemoryWriteSkill(store *memory.Store) *MemoryWriteSkill {
	return &MemoryWriteSkill{store: store}
}

func (s *MemoryWriteSkill) Name() string        { return "memory_write" }
func (s *MemoryWriteSkill) Description() string  { return "保存一条关于当前数据库实例的记忆到文件" }
func (s *MemoryWriteSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *MemoryWriteSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "memory_write",
		Description: "保存一条关于当前数据库实例的记忆。记忆类型: incident(问题), solution(方案), preference(偏好), workload(负载), pattern(模式)",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type": map[string]any{
					"type":        "string",
					"enum":        []string{"incident", "solution", "preference", "workload", "pattern"},
					"description": "记忆类型",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "一行摘要标题",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "详细内容，包含时间、因果、结论",
				},
			},
			"required": []string{"type", "title", "content"},
		},
	}
}

func (s *MemoryWriteSkill) CLIDef() skill.CLIDef { return skill.CLIDef{} }

func (s *MemoryWriteSkill) Validate(params skill.Params) error {
	memType, err := params.String("type")
	if err != nil {
		return fmt.Errorf("缺少 type 参数")
	}
	if !memory.MemoryType(memType).IsValid() {
		return fmt.Errorf("无效的记忆类型: %s", memType)
	}
	if _, err := params.String("title"); err != nil {
		return fmt.Errorf("缺少 title 参数")
	}
	if _, err := params.String("content"); err != nil {
		return fmt.Errorf("缺少 content 参数")
	}
	return nil
}

func (s *MemoryWriteSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	memType, _ := params.String("type")
	title, _ := params.String("title")
	content, _ := params.String("content")

	filename, err := s.store.Write(memory.MemoryType(memType), title, content)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("记忆保存失败: %v", err),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("记忆已保存: %s (类型: %s)", filename, memType),
	}, nil
}
