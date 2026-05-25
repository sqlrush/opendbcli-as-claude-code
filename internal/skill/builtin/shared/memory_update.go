/*-------------------------------------------------------------------------
 *
 * memory_update.go
 *	  MemoryUpdateSkill allows the LLM to update an existing memory
 *	  file. Primarily used for maintaining PROFILE.md.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/memory_update.go
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

// MemoryUpdateSkill allows the LLM to update an existing memory file.
// Primarily used for maintaining PROFILE.md.
type MemoryUpdateSkill struct {
	store *memory.Store
}

func NewMemoryUpdateSkill(store *memory.Store) *MemoryUpdateSkill {
	return &MemoryUpdateSkill{store: store}
}

func (s *MemoryUpdateSkill) Name() string        { return "memory_update" }
func (s *MemoryUpdateSkill) Description() string  { return "更新当前数据库实例的一条已有记忆文件" }
func (s *MemoryUpdateSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *MemoryUpdateSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "memory_update",
		Description: "更新当前数据库实例的一条已有记忆文件。可用于更新 PROFILE.md 或其他记忆文件",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "要更新的文件名（如 PROFILE.md 或从索引中获得的文件名）",
				},
				"title": map[string]any{
					"type":        "string",
					"description": "更新后的标题（可选，不传则保持原标题）",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "更新后的完整内容",
				},
			},
			"required": []string{"filename", "content"},
		},
	}
}

func (s *MemoryUpdateSkill) CLIDef() skill.CLIDef { return skill.CLIDef{} }

func (s *MemoryUpdateSkill) Validate(params skill.Params) error {
	if _, err := params.String("filename"); err != nil {
		return fmt.Errorf("缺少 filename 参数")
	}
	if _, err := params.String("content"); err != nil {
		return fmt.Errorf("缺少 content 参数")
	}
	return nil
}

func (s *MemoryUpdateSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	filename, _ := params.String("filename")
	title := params.StringOr("title", "")
	content, _ := params.String("content")

	// Special handling for PROFILE.md — direct write, no frontmatter
	if filename == memory.ProfileFile {
		if err := s.store.WriteProfile(content); err != nil {
			return &skill.Result{
				Type:     skill.ResultText,
				Rendered: fmt.Sprintf("更新 PROFILE.md 失败: %v", err),
			}, nil
		}
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "PROFILE.md 已更新",
		}, nil
	}

	// Regular memory file update
	if err := s.store.Update(filename, title, content); err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("更新记忆失败: %v", err),
		}, nil
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("记忆已更新: %s", filename),
	}, nil
}
