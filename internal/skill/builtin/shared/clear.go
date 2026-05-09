/*-------------------------------------------------------------------------
 *
 * clear.go
 *	  ClearSkill clears the terminal screen.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/clear.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"

	"github.com/sqlrush/opendb/internal/skill"
)

// ClearSkill clears the terminal screen.
type ClearSkill struct{}

// NewClearSkill creates a ClearSkill.
func NewClearSkill() *ClearSkill {
	return &ClearSkill{}
}

func (s *ClearSkill) Name() string        { return "clear" }
func (s *ClearSkill) Description() string  { return "Clear the screen" }
func (s *ClearSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ClearSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "clear",
		Description: "Clear the screen",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
}

func (s *ClearSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "clear",
		Aliases:  []string{"cls"},
		Usage:    "/clear",
		Examples: []string{"/clear"},
	}
}

func (s *ClearSkill) Validate(_ skill.Params) error { return nil }

func (s *ClearSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	return &skill.Result{
		Type: skill.ResultText,
		Data: "__clear__",
	}, nil
}
