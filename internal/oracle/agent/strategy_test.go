/*-------------------------------------------------------------------------
 *
 * strategy_test.go
 *	  Test cases for strategy.go (agent package):
 *	  TestModelCapability_IsValid, TestIsMaxTurnsExceeded,
 *	  TestSelectStrategy_Small.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/strategy_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

// ---------------------------------------------------------------------------
// ModelCapability
// ---------------------------------------------------------------------------

func TestModelCapability_IsValid(t *testing.T) {
	tests := []struct {
		cap  ModelCapability
		want bool
	}{
		{CapabilitySmall, true},
		{CapabilityMedium, true},
		{CapabilityLarge, true},
		{"", false},
		{"huge", false},
	}
	for _, tt := range tests {
		if got := tt.cap.IsValid(); got != tt.want {
			t.Errorf("ModelCapability(%q).IsValid() = %v, want %v", tt.cap, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// IsMaxTurnsExceeded
// ---------------------------------------------------------------------------

func TestIsMaxTurnsExceeded(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"some analysis", false},
		{"analysis\n\n" + MaxTurnsNote, true},
		{MaxTurnsNote, true},
		{"prefix " + MaxTurnsNote + " suffix", true},
		{"", false},
	}
	for _, tt := range tests {
		if got := IsMaxTurnsExceeded(tt.text); got != tt.want {
			t.Errorf("IsMaxTurnsExceeded(%q) = %v, want %v", tt.text, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// SelectStrategy
// ---------------------------------------------------------------------------

func TestSelectStrategy_Small(t *testing.T) {
	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	provider := &mockProvider{}

	strategy := SelectStrategy(CapabilitySmall, provider, exec, reg)
	if strategy.Name() != "guided" {
		t.Errorf("Name() = %q, want guided", strategy.Name())
	}
}

func TestSelectStrategy_Large(t *testing.T) {
	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	provider := &mockProvider{}

	strategy := SelectStrategy(CapabilityLarge, provider, exec, reg)
	if strategy.Name() != "autonomous" {
		t.Errorf("Name() = %q, want autonomous", strategy.Name())
	}
}

func TestSelectStrategy_UnknownDefaultsToGuided(t *testing.T) {
	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	provider := &mockProvider{}

	// Unknown / empty / small all default to guided.
	// medium now routes to autonomous (v1.1.17).
	for _, cap := range []ModelCapability{"", "unknown"} {
		strategy := SelectStrategy(cap, provider, exec, reg)
		if strategy.Name() != "guided" {
			t.Errorf("SelectStrategy(%q).Name() = %q, want guided", cap, strategy.Name())
		}
	}
	if got := SelectStrategy(CapabilityMedium, provider, exec, reg).Name(); got != "autonomous" {
		t.Errorf("SelectStrategy(medium).Name() = %q, want autonomous", got)
	}
}

// ---------------------------------------------------------------------------
// GuidedStrategy
// ---------------------------------------------------------------------------

func TestGuidedStrategy_Diagnose(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			textResponse("Guided: found SQL issue"),
		},
	}
	reg := newTestRegistry()
	exec := newTestExecutor(reg)

	strategy := SelectStrategy(CapabilitySmall, provider, exec, reg)

	result, err := strategy.Diagnose(context.Background(), testBurstReport(), "诊断数据库")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Strategy != "guided" {
		t.Errorf("Strategy = %q, want guided", result.Strategy)
	}
	if result.Mode != ModeAssist {
		t.Errorf("Mode = %q, want assist", result.Mode)
	}
	if result.FellBack {
		t.Error("should not have FellBack=true")
	}
	if !strings.Contains(result.Analysis, "Guided: found SQL issue") {
		t.Errorf("analysis = %q, should contain LLM output", result.Analysis)
	}
}

func TestGuidedStrategy_Error(t *testing.T) {
	provider := &errProvider{err: fmt.Errorf("connection refused")}
	reg := newTestRegistry()
	exec := newTestExecutor(reg)

	strategy := SelectStrategy(CapabilitySmall, provider, exec, reg)

	_, err := strategy.Diagnose(context.Background(), testBurstReport(), "诊断")
	if err == nil {
		t.Fatal("expected error")
	}
	// Engine wraps "connection refused" into localized error message.
	errMsg := err.Error()
	if !strings.Contains(errMsg, "connection refused") && !strings.Contains(errMsg, "连接被拒绝") {
		t.Errorf("error = %q, should contain cause", errMsg)
	}
}

// ---------------------------------------------------------------------------
// AutonomousStrategy — converged
// ---------------------------------------------------------------------------

func TestAutonomousStrategy_Converged(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			textResponse("Autonomous: root cause is bad SQL abc123"),
		},
	}
	reg := newTestRegistry()
	exec := newTestExecutor(reg)

	strategy := SelectStrategy(CapabilityLarge, provider, exec, reg)

	result, err := strategy.Diagnose(context.Background(), testBurstReport(), "自动诊断")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Strategy != "autonomous" {
		t.Errorf("Strategy = %q, want autonomous", result.Strategy)
	}
	if result.Mode != ModeAuto {
		t.Errorf("Mode = %q, want auto", result.Mode)
	}
	if result.FellBack {
		t.Error("should not have FellBack=true for converged result")
	}
	if !strings.Contains(result.Analysis, "root cause is bad SQL") {
		t.Error("analysis should contain LLM output")
	}
}

// ---------------------------------------------------------------------------
// AutonomousStrategy — fallback
// ---------------------------------------------------------------------------

func TestAutonomousStrategy_Fallback(t *testing.T) {
	sessionsSkill := &mockSkill{
		name: "sessions",
		toolDef: skill.ToolDef{
			Name:       "sessions",
			Parameters: map[string]any{"type": "object"},
		},
		secLevel: security.LevelReadOnly,
		executeResult: &skill.Result{
			Type: skill.ResultText, Data: "data",
		},
	}
	reg := newTestRegistry(sessionsSkill)
	exec := newTestExecutor(reg)

	// Engine V2 uses native tool calls. Build enough responses to exhaust auto rounds.
	responses := make([]*llm.Response, 0, ModeAuto.MaxRounds()+1)
	for i := 0; i < ModeAuto.MaxRounds(); i++ {
		responses = append(responses, toolCallResponse("继续查询",
			makeToolCall(fmt.Sprintf("call-%d", i), "sessions", `{}`),
		))
	}
	// Guided fallback (assist mode) gets a final answer.
	responses = append(responses, textResponse("Guided fallback: SQL contention on ORDERS table"))

	provider := &mockProvider{responses: responses}

	strategy := SelectStrategy(CapabilityLarge, provider, exec, reg)

	result, err := strategy.Diagnose(context.Background(), testBurstReport(), "诊断")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.FellBack {
		t.Error("should have FellBack=true after auto exhaustion")
	}
	if result.Strategy != "autonomous" {
		t.Errorf("Strategy = %q, want autonomous", result.Strategy)
	}
	if result.Mode != ModeAssist {
		t.Errorf("Mode = %q, want assist (from guided fallback)", result.Mode)
	}
	if !strings.Contains(result.Analysis, "Guided fallback") {
		t.Errorf("analysis = %q, should contain guided fallback result", result.Analysis)
	}
}

// ---------------------------------------------------------------------------
// AutonomousStrategy — auto error (returns error, no fallback)
// ---------------------------------------------------------------------------

func TestAutonomousStrategy_AutoError(t *testing.T) {
	provider := &errProvider{err: fmt.Errorf("rate limit")}
	reg := newTestRegistry()
	exec := newTestExecutor(reg)

	strategy := SelectStrategy(CapabilityLarge, provider, exec, reg)

	_, err := strategy.Diagnose(context.Background(), testBurstReport(), "诊断")
	if err == nil {
		t.Fatal("expected error from auto mode")
	}
	if !strings.Contains(err.Error(), "rate limit") {
		t.Errorf("error = %q, should contain cause", err.Error())
	}
}

// ---------------------------------------------------------------------------
// AutonomousStrategy — fallback also fails (returns original auto result)
// ---------------------------------------------------------------------------

func TestAutonomousStrategy_FallbackAlsoFails(t *testing.T) {
	sessionsSkill := &mockSkill{
		name: "sessions",
		toolDef: skill.ToolDef{
			Name:       "sessions",
			Parameters: map[string]any{"type": "object"},
		},
		secLevel: security.LevelReadOnly,
		executeResult: &skill.Result{
			Type: skill.ResultText, Data: "data",
		},
	}
	reg := newTestRegistry(sessionsSkill)
	exec := newTestExecutor(reg)

	// Engine V2 uses native tool calls. Exhaust all auto rounds.
	responses := make([]*llm.Response, 0, ModeAuto.MaxRounds())
	for i := 0; i < ModeAuto.MaxRounds(); i++ {
		responses = append(responses, toolCallResponse("继续查询",
			makeToolCall(fmt.Sprintf("call-%d", i), "sessions", `{}`),
		))
	}
	// No more responses → mockProvider returns error for guided fallback.

	provider := &mockProvider{responses: responses}

	strategy := SelectStrategy(CapabilityLarge, provider, exec, reg)

	result, err := strategy.Diagnose(context.Background(), testBurstReport(), "诊断")
	if err != nil {
		t.Fatalf("should not error (returns original auto result): %v", err)
	}
	// Falls back but fallback fails → returns original auto result.
	if result.FellBack {
		t.Error("FellBack should be false when fallback fails")
	}
	if result.Strategy != "autonomous" {
		t.Errorf("Strategy = %q, want autonomous", result.Strategy)
	}
	// The auto result should contain the exhausted-rounds note from PromptLoop.
	if result.Analysis == "" {
		t.Error("auto result should have non-empty analysis after rounds exhausted")
	}
}

// ---------------------------------------------------------------------------
// FormatDiagnoseResult — strategy display
// ---------------------------------------------------------------------------

func TestFormatDiagnoseResult_WithStrategy(t *testing.T) {
	dr := DiagnoseResult{
		Mode:       ModeAuto,
		Strategy:   "autonomous",
		Analysis:   "Root cause found",
		RoundsUsed: 5,
	}
	output := FormatDiagnoseResult(dr)
	if !strings.Contains(output, "autonomous") {
		t.Errorf("output missing strategy name:\n%s", output)
	}
}

func TestFormatDiagnoseResult_WithFallback(t *testing.T) {
	dr := DiagnoseResult{
		Mode:       ModeAssist,
		Strategy:   "autonomous",
		FellBack:   true,
		Analysis:   "Fallback analysis",
		RoundsUsed: 2,
	}
	output := FormatDiagnoseResult(dr)
	if !strings.Contains(output, "auto→guided") {
		t.Errorf("output should show fallback indicator:\n%s", output)
	}
}

func TestFormatDiagnoseResult_NoStrategy(t *testing.T) {
	// Direct mode call (no strategy), should show mode name.
	dr := DiagnoseResult{
		Mode:       ModePlaybook,
		RoundsUsed: 1,
		Analysis:   "Quick analysis",
	}
	output := FormatDiagnoseResult(dr)
	if !strings.Contains(output, "playbook") {
		t.Errorf("output should show mode name:\n%s", output)
	}
}
