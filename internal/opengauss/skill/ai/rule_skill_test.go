/*-------------------------------------------------------------------------
 *
 * rule_skill_test.go
 *	  Test cases for rule_skill.go (ai package): TestRuleSkill_Metadata,
 *	  TestRuleSkill_Validate, TestRuleSkill_ConstructorNoPanic.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/rule_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestRuleSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	sentinelSkill := NewSentinelSkill(drv, config.SentinelConfig{})
	s := NewRuleSkill(sentinelSkill, drv)

	if got := s.Name(); got != "rule" {
		t.Errorf("Name() = %q, want %q", got, "rule")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should be non-empty")
	}

	td := s.ToolDef()
	if td.Name != "rule" {
		t.Errorf("ToolDef().Name = %q, want %q", td.Name, "rule")
	}
	if td.Description == "" {
		t.Error("ToolDef().Description should be non-empty")
	}

	cd := s.CLIDef()
	if cd.Command != "rule" {
		t.Errorf("CLIDef().Command = %q, want %q", cd.Command, "rule")
	}
	if len(cd.Examples) == 0 {
		t.Error("CLIDef().Examples should be non-empty")
	}
}

func TestRuleSkill_Validate(t *testing.T) {
	drv := mock.NewMockDriver()
	sentinelSkill := NewSentinelSkill(drv, config.SentinelConfig{})
	s := NewRuleSkill(sentinelSkill, drv)

	// Validate currently accepts any args (free-form: "", number, or "live").
	if err := s.Validate(skill.ParamsFromMap(map[string]any{})); err != nil {
		t.Errorf("Validate(empty) = %v, want nil", err)
	}
	if err := s.Validate(p("args", "live")); err != nil {
		t.Errorf("Validate(live) = %v, want nil", err)
	}
	if err := s.Validate(p("args", "1")); err != nil {
		t.Errorf("Validate(1) = %v, want nil", err)
	}
}

func TestRuleSkill_ConstructorNoPanic(t *testing.T) {
	// Nil driver is tolerated by NewRuleSkill (executor is simply skipped).
	sentinelSkill := NewSentinelSkill(nil, config.SentinelConfig{})
	s := NewRuleSkill(sentinelSkill, nil)
	if s == nil {
		t.Fatal("NewRuleSkill returned nil")
	}
	if s.Name() != "rule" {
		t.Errorf("Name() = %q, want rule", s.Name())
	}
}
