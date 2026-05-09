/*-------------------------------------------------------------------------
 *
 * memory_recall.go
 *	  MemoryRecallSkill allows the LLM to read a memory file's full
 *	  content.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/memory_recall.go
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

// MemoryRecallSkill allows the LLM to read a memory file's full content.
type MemoryRecallSkill struct {
	store *memory.Store
}

func NewMemoryRecallSkill(store *memory.Store) *MemoryRecallSkill {
	return &MemoryRecallSkill{store: store}
}

func (s *MemoryRecallSkill) Name() string        { return "memory_recall" }
func (s *MemoryRecallSkill) Description() string  { return "读取当前数据库实例的一条历史记忆文件详情" }
func (s *MemoryRecallSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *MemoryRecallSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "memory_recall",
		Description: "读取当前数据库实例的一条历史记忆文件的完整内容",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "记忆文件名（从记忆索引中获得）",
				},
			},
			"required": []string{"filename"},
		},
	}
}

func (s *MemoryRecallSkill) CLIDef() skill.CLIDef { return skill.CLIDef{} }

func (s *MemoryRecallSkill) Validate(params skill.Params) error {
	if _, err := params.String("filename"); err != nil {
		return fmt.Errorf("缺少 filename 参数")
	}
	return nil
}

func (s *MemoryRecallSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	filename, _ := params.String("filename")

	content, err := s.store.Read(filename)
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("读取记忆失败: %v", err),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: content,
	}, nil
}
