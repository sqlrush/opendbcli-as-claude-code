/*-------------------------------------------------------------------------
 *
 * bridge_test.go
 *	  Test cases for bridge.go (bridge package): TestConvertResultText,
 *	  TestConvertResultError, TestConvertResultNil.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/bridge_test.go
 *
 *-------------------------------------------------------------------------
 */
package bridge

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

// mockSkill implements skill.Skill for testing.
type mockSkill struct {
	name          string
	securityLevel skill.SecurityLevel
	result        *skill.Result
}

func (s *mockSkill) Name() string                                            { return s.name }
func (s *mockSkill) Description() string                                     { return "" }
func (s *mockSkill) ToolDef() skill.ToolDef                                  { return skill.ToolDef{Name: s.name} }
func (s *mockSkill) CLIDef() skill.CLIDef                                    { return skill.CLIDef{Command: s.name} }
func (s *mockSkill) Validate(p skill.Params) error                           { return nil }
func (s *mockSkill) Execute(ctx context.Context, p skill.Params) (*skill.Result, error) {
	return s.result, nil
}
func (s *mockSkill) SecurityLevel() skill.SecurityLevel { return s.securityLevel }

func TestConvertResultText(t *testing.T) {
	r := &skill.Result{
		Type:     skill.ResultText,
		Data:     "some text data",
		Rendered: "rendered output",
		Summary:  "summary",
	}
	sr := convertResult(r)
	if sr.Rendered != "rendered output" {
		t.Errorf("expected rendered, got %q", sr.Rendered)
	}
	if sr.Text != "some text data" {
		t.Errorf("expected text, got %q", sr.Text)
	}
}

func TestConvertResultError(t *testing.T) {
	r := &skill.Result{
		Type: skill.ResultError,
		Data: "something failed",
	}
	sr := convertResult(r)
	if sr.Error != "something failed" {
		t.Errorf("expected error, got %q", sr.Error)
	}
}

func TestConvertResultNil(t *testing.T) {
	sr := convertResult(nil)
	if sr == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestSkillBridgeSecurityLevel(t *testing.T) {
	registry := skill.NewRegistry()
	registry.Register(&mockSkill{name: "waits", securityLevel: 0})
	registry.Register(&mockSkill{name: "kill", securityLevel: 1})

	bridge := &SkillBridge{registry: registry}

	if bridge.SecurityLevel("waits") != 0 {
		t.Error("waits should be level 0")
	}
	if bridge.SecurityLevel("kill") != 1 {
		t.Error("kill should be level 1")
	}
	if bridge.SecurityLevel("nonexistent") != 0 {
		t.Error("nonexistent should default to 0")
	}
}
