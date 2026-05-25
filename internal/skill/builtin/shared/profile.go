/*-------------------------------------------------------------------------
 *
 * profile.go
 *	  ProfileSkill displays the active instance's PROFILE.md — a
 *	  human-readable quick view of what the LLM knows about the current
 *	  database instance (version, deployment shape, workload
 *	  characteristics, historical diagnoses, known issues).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/profile.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"

	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/skill"
)

// ProfileSkill displays the active instance's PROFILE.md — a
// human-readable quick view of what the LLM knows about the current
// database instance (version, deployment shape, workload characteristics,
// historical diagnoses, known issues).
//
// It is a thin read wrapper around memory.Store.LoadProfile so operators
// can inspect the profile without having to `cat ~/.opendb/memory/<inst>/
// PROFILE.md` by hand.
type ProfileSkill struct {
	store *memory.Store
}

// NewProfileSkill creates a ProfileSkill.
func NewProfileSkill(store *memory.Store) *ProfileSkill {
	return &ProfileSkill{store: store}
}

func (s *ProfileSkill) Name() string                       { return "profile" }
func (s *ProfileSkill) Description() string                { return "查看当前实例画像 (PROFILE.md)" }
func (s *ProfileSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ProfileSkill) Validate(_ skill.Params) error      { return nil }

func (s *ProfileSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "profile",
		Usage:   "/profile",
	}
}

func (s *ProfileSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "profile",
		Description: "Show the active instance's PROFILE.md contents (LLM-maintained instance snapshot)",
	}
}

func (s *ProfileSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	if s.store == nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "画像系统未初始化（请先 /login 连接到某个实例）",
			Summary:  "profile store nil",
		}, nil
	}
	content, err := s.store.LoadProfile()
	if err != nil || content == "" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前实例暂无 PROFILE.md — LLM 诊断后会自动创建。\n可用 `/llm 帮我全面检查一下数据库` 触发首次诊断。",
			Summary:  "no profile yet",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: content,
		Summary:  "instance profile",
	}, nil
}
