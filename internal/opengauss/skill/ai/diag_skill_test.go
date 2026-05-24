/*-------------------------------------------------------------------------
 *
 * diag_skill_test.go
 *	  Test cases for diag_skill.go (ai package):
 *	  TestDiagnoseSkill_Metadata, TestDiagnoseSkill_HasLLM_False,
 *	  TestDiagnoseSkill_Validate_EmptyMode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/diag_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/skill"
)

// newTestDiagSkill wires up a DiagnoseSkill with no active LLM provider. This
// is the common fixture for metadata-level tests — deeper execution paths rely
// on heavy LLM/agent machinery and are intentionally out of scope here.
func newTestDiagSkill(t *testing.T) *DiagnoseSkill {
	t.Helper()

	drv := mock.NewMockDriver()
	sentinelSkill := NewSentinelSkill(drv, config.SentinelConfig{})
	ruleSkill := NewRuleSkill(sentinelSkill, drv)

	// No-LLM manager; executor/registry use safe defaults (nil guard would
	// be exercised only on Execute() paths we don't touch here).
	mgr := model.NewManagerForTest(nil, "small")
	registry := skill.NewRegistry()

	return NewDiagnoseSkill(mgr, nil, registry, sentinelSkill, ruleSkill)
}

func TestDiagnoseSkill_Metadata(t *testing.T) {
	s := newTestDiagSkill(t)

	if got := s.Name(); got != "llm" {
		t.Errorf("Name() = %q, want %q", got, "llm")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should be non-empty")
	}

	td := s.ToolDef()
	if td.Name != "diag" {
		t.Errorf("ToolDef().Name = %q, want %q", td.Name, "diag")
	}
	if td.Description == "" {
		t.Error("ToolDef().Description should be non-empty")
	}

	cd := s.CLIDef()
	if cd.Command != "llm" {
		t.Errorf("CLIDef().Command = %q, want %q", cd.Command, "llm")
	}
	if len(cd.Examples) == 0 {
		t.Error("CLIDef().Examples should be non-empty")
	}
}

func TestDiagnoseSkill_HasLLM_False(t *testing.T) {
	s := newTestDiagSkill(t)
	if s.HasLLM() {
		t.Error("HasLLM() = true, want false (manager has no provider)")
	}
}

func TestDiagnoseSkill_Validate_EmptyMode(t *testing.T) {
	s := newTestDiagSkill(t)
	if err := s.Validate(skill.ParamsFromMap(map[string]any{})); err != nil {
		t.Errorf("Validate(empty) = %v, want nil (default mode should pass)", err)
	}
}

func TestDiagnoseSkill_Validate_PlaybookNoLLM(t *testing.T) {
	s := newTestDiagSkill(t)
	// playbook mode works without LLM (rule-based only).
	if err := s.Validate(p("mode", "playbook")); err != nil {
		t.Errorf("Validate(playbook, no-LLM) = %v, want nil", err)
	}
}

func TestDiagnoseSkill_Validate_AssistRequiresLLM(t *testing.T) {
	s := newTestDiagSkill(t)
	// assist and auto require LLM; no provider => error.
	if err := s.Validate(p("mode", "assist")); err == nil {
		t.Error("Validate(assist, no-LLM) = nil, want error")
	}
	if err := s.Validate(p("mode", "auto")); err == nil {
		t.Error("Validate(auto, no-LLM) = nil, want error")
	}
}

func TestDiagnoseSkill_Validate_InvalidMode(t *testing.T) {
	s := newTestDiagSkill(t)
	if err := s.Validate(p("mode", "nonsense")); err == nil {
		t.Error("Validate(nonsense) = nil, want error for unknown mode")
	}
}

func TestDiagnoseSkill_ConstructorNoPanic(t *testing.T) {
	// Minimal construction path with nil executor/registry — matches the
	// real wiring in register.go where registry is always provided but
	// executor may be nil early in startup.
	drv := mock.NewMockDriver()
	sentinelSkill := NewSentinelSkill(drv, config.SentinelConfig{})
	ruleSkill := NewRuleSkill(sentinelSkill, drv)

	s := NewDiagnoseSkill(
		model.NewManagerForTest(nil, "small"),
		nil,
		skill.NewRegistry(),
		sentinelSkill,
		ruleSkill,
	)
	if s == nil {
		t.Fatal("NewDiagnoseSkill returned nil")
	}
	if s.Name() != "llm" {
		t.Errorf("Name() = %q, want llm", s.Name())
	}
}

func TestShouldDirectWDRList(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want bool
	}{
		{name: "current reports", q: "当前有哪几个wdr报告", want: true},
		{name: "snapshot list", q: "列出 WDR 快照列表", want: true},
		{name: "awr list", q: "有哪些 AWR snapshot", want: true},
		{name: "analyze report", q: "分析下这个 WDR 报告", want: false},
		{name: "analyze snapshot pair", q: "分析 快照76和77之间生成的wdr报告", want: false},
		{name: "non wdr", q: "当前数据库存在什么问题", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDirectWDRList(tc.q); got != tc.want {
				t.Fatalf("shouldDirectWDRList(%q) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

func TestShouldDirectWDRAnalyze(t *testing.T) {
	cases := []struct {
		name     string
		q        string
		wantArgs string
		want     bool
	}{
		{name: "snapshot pair", q: "分析 快照76和77之间生成的wdr报告", wantArgs: "76 77", want: true},
		{name: "plain pair", q: "对比 76 到 77 的 WDR 报告", wantArgs: "76 77", want: true},
		{name: "latest", q: "分析最近的 WDR 报告", wantArgs: "latest", want: true},
		{name: "list only", q: "当前有哪几个wdr报告", want: false},
		{name: "non wdr", q: "当前数据库存在什么问题", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotArgs, got := shouldDirectWDRAnalyze(tc.q)
			if got != tc.want || gotArgs != tc.wantArgs {
				t.Fatalf("shouldDirectWDRAnalyze(%q) = (%q, %v), want (%q, %v)", tc.q, gotArgs, got, tc.wantArgs, tc.want)
			}
		})
	}
}
