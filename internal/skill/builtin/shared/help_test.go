/*-------------------------------------------------------------------------
 *
 * help_test.go
 *	  Test cases for help.go (shared package): TestHelpSkill_Execute,
 *	  TestHelpSkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/help_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

// stubSkill is a minimal Skill implementation for testing the help command.
type stubSkill struct {
	name string
	desc string
}

func (s *stubSkill) Name() string                         { return s.name }
func (s *stubSkill) Description() string                  { return s.desc }
func (s *stubSkill) ToolDef() skill.ToolDef               { return skill.ToolDef{} }
func (s *stubSkill) CLIDef() skill.CLIDef                 { return skill.CLIDef{} }
func (s *stubSkill) Validate(_ skill.Params) error        { return nil }
func (s *stubSkill) SecurityLevel() skill.SecurityLevel   { return skill.LevelReadOnly }
func (s *stubSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	return nil, nil
}

func TestHelpSkill_Execute(t *testing.T) {
	tests := []struct {
		name       string
		skills     []skill.Skill
		params     map[string]any
		wantSub    string // substring expected in Data
		wantNotSub string // substring not expected in Data (empty = skip)
	}{
		{
			name:    "empty registry",
			skills:  nil,
			params:  map[string]any{},
			wantSub: "No commands found",
		},
		{
			name: "lists all skills",
			skills: []skill.Skill{
				&stubSkill{name: "alpha", desc: "Alpha command"},
				&stubSkill{name: "beta", desc: "Beta command"},
			},
			params:  map[string]any{},
			wantSub: "alpha",
		},
		{
			name: "filter matches",
			skills: []skill.Skill{
				&stubSkill{name: "login", desc: "Connect"},
				&stubSkill{name: "logout", desc: "Disconnect"},
				&stubSkill{name: "help", desc: "Help"},
			},
			params:     map[string]any{"args": "log"},
			wantSub:    "login",
			wantNotSub: "help",
		},
		{
			name: "filter no match",
			skills: []skill.Skill{
				&stubSkill{name: "help", desc: "Help"},
			},
			params:  map[string]any{"args": "zzz"},
			wantSub: "No commands matching",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := skill.NewRegistry()
			for _, sk := range tt.skills {
				reg.Register(sk)
			}

			h := NewHelpSkill(reg)
			result, err := h.Execute(context.Background(), skill.ParamsFromMap(tt.params))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Type != skill.ResultText {
				t.Errorf("want ResultText, got %v", result.Type)
			}
			data, ok := result.Data.(string)
			if !ok {
				t.Fatalf("Data is not a string: %T", result.Data)
			}
			if !strings.Contains(data, tt.wantSub) {
				t.Errorf("output %q does not contain %q", data, tt.wantSub)
			}
			if tt.wantNotSub != "" && strings.Contains(data, tt.wantNotSub) {
				t.Errorf("output %q should not contain %q", data, tt.wantNotSub)
			}
		})
	}
}

func TestHelpSkill_Metadata(t *testing.T) {
	h := NewHelpSkill(skill.NewRegistry())

	if h.Name() != "help" {
		t.Errorf("Name() = %q, want %q", h.Name(), "help")
	}
	if h.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", h.SecurityLevel())
	}
	if err := h.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
