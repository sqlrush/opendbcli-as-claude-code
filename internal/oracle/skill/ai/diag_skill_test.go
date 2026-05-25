/*-------------------------------------------------------------------------
 *
 * diag_skill_test.go
 *	  Test cases for diag_skill.go (ai package):
 *	  TestDiagnoseSkill_Metadata,
 *	  TestDiagnoseSkill_Validate_NoProvider_Playbook,
 *	  TestDiagnoseSkill_Validate_NoProvider_Assist.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/ai/diag_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/oracle/agent"
	"github.com/sqlrush/opendb/internal/model"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

// --- mock provider ---

type mockProvider struct {
	responses []*llm.Response
	callIdx   int
}

func (m *mockProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.Response, error) {
	if m.callIdx >= len(m.responses) {
		return nil, errors.New("no more responses")
	}
	resp := m.responses[m.callIdx]
	m.callIdx++
	return resp, nil
}

func (m *mockProvider) ChatStream(_ context.Context, _ llm.ChatRequest) (llm.Stream, error) {
	return nil, errors.New("not implemented")
}

func (m *mockProvider) Name() string { return "mock" }

// --- mock guard ---

type mockGuard struct{}

func (g *mockGuard) Authorize(_ context.Context, _ security.Level, _ string) error { return nil }
func (g *mockGuard) RequestConfirmation(_ context.Context, _ string) (bool, error) {
	return true, nil
}
func (g *mockGuard) MaxLevel() security.Level { return security.LevelDangerous }

func TestDiagnoseSkill_Metadata(t *testing.T) {
	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, nil)

	if s.Name() != "llm" {
		t.Errorf("Name() = %q, want llm", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	td := s.ToolDef()
	if td.Name != "diag" {
		t.Errorf("ToolDef().Name = %q, want diag", td.Name)
	}

	cd := s.CLIDef()
	if cd.Command != "llm" {
		t.Errorf("CLIDef().Command = %q, want llm", cd.Command)
	}
	hasDialias := false
	for _, a := range cd.Aliases {
		if a == "diag" || a == "diagnose" {
			hasDialias = true
			break
		}
	}
	if !hasDialias {
		t.Error("should have 'diag' or 'diagnose' alias")
	}
}

func TestDiagnoseSkill_Validate_NoProvider_Playbook(t *testing.T) {
	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, nil)
	// playbook mode works without provider (rule-based only).
	err := s.Validate(p("mode", "playbook"))
	if err != nil {
		t.Errorf("playbook mode should work without provider, got: %v", err)
	}
}

func TestDiagnoseSkill_Validate_NoProvider_Assist(t *testing.T) {
	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, nil)
	// assist mode requires provider.
	err := s.Validate(p("mode", "assist"))
	if err == nil {
		t.Error("assist mode should error when provider is nil")
	}
}

func TestDiagnoseSkill_Validate_InvalidMode(t *testing.T) {
	provider := &mockProvider{}
	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), nil, nil, nil)
	err := s.Validate(p("mode", "invalid"))
	if err == nil {
		t.Error("should error for invalid mode")
	}
}

func TestDiagnoseSkill_Validate_ValidModes(t *testing.T) {
	provider := &mockProvider{}
	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), nil, nil, nil)

	for _, mode := range []string{"playbook", "assist", "auto"} {
		err := s.Validate(p("mode", mode))
		if err != nil {
			t.Errorf("Validate(%q) = %v, want nil", mode, err)
		}
	}
}

func TestDiagnoseSkill_Execute_Playbook(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "## 根因分析\n问题SQL", Usage: llm.Usage{InputTokens: 500, OutputTokens: 200}},
		},
	}

	reg := skill.NewRegistry()
	exec := skill.NewExecutor(reg, &mockGuard{})

	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{{
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.85,
		},
	}}

	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), exec, reg, sentSkill)

	result, err := s.Execute(context.Background(), p("args", "1", "mode", "playbook", "question", "数据库慢"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Rendered
	if !strings.Contains(text, "playbook") {
		t.Errorf("output should mention playbook mode, got %q", text)
	}
	if !strings.Contains(text, "根因分析") {
		t.Errorf("output should contain analysis, got %q", text)
	}
}

func TestDiagnoseSkill_Execute_NoReport(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "需要更多信息"},
		},
	}

	reg := skill.NewRegistry()
	exec := skill.NewExecutor(reg, &mockGuard{})
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})

	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), exec, reg, sentSkill)

	result, err := s.Execute(context.Background(), p("mode", "playbook"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Rendered
	if text == "" {
		t.Error("should have output even without report")
	}
}

func TestDiagnoseSkill_Execute_DefaultArgs(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "诊断完成"},
		},
	}

	reg := skill.NewRegistry()
	exec := skill.NewExecutor(reg, &mockGuard{})
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{{
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.75,
		},
	}}

	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), exec, reg, sentSkill)

	// /diag (no args) now shows history list.
	result, err := s.Execute(context.Background(), emptyParams())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "1 条") {
		t.Errorf("default /diag should show history, got %q", result.Rendered)
	}

	// /diag 1 with default mode and "small" capability uses GuidedStrategy (assist mode).
	result, err = s.Execute(context.Background(), p("args", "1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Summary, "assist") {
		t.Errorf("default mode with small capability should use assist, summary=%q", result.Summary)
	}
}

func TestDiagnoseSkill_Execute_Assist(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "assist模式诊断完成"},
		},
	}

	reg := skill.NewRegistry()
	exec := skill.NewExecutor(reg, &mockGuard{})
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{{
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.80,
		},
	}}

	s := NewDiagnoseSkill(model.NewManagerForTest(provider, "small"), exec, reg, sentSkill)

	result, err := s.Execute(context.Background(), p("args", "1", "mode", string(agent.ModeAssist), "question", "检查"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Summary should reflect assist mode.
	if !strings.Contains(result.Summary, "assist") {
		t.Errorf("should mention assist mode, summary=%q", result.Summary)
	}
}

func TestDiagnoseSkill_Execute_RuleBasedOnly(t *testing.T) {
	// No LLM provider — should return rule-based diagnosis only.
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{{
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseLockContention,
			Confidence: 0.90,
		},
		DurationSec:    5.0,
		PeakActive:     12,
		BaselineActive: 2.0,
	}}

	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, sentSkill)

	// /diag 1 — diagnose the latest report (rule-based only, no LLM).
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "1", "mode": "playbook"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Rendered
	if !strings.Contains(text, "监控数据") {
		t.Error("should contain monitoring data header")
	}
	if strings.Contains(text, "AI") {
		t.Error("should not mention AI when no provider configured")
	}
}

func TestDiagnoseSkill_Execute_History(t *testing.T) {
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{
		{
			TriggerEvent: sentinel.TriggerEvent{
				Metric: "active_sessions", Baseline: 1, Current: 10, Threshold: 4,
			},
			Classification: sentinel.Classification{
				Cause: sentinel.CauseBadSQL, Confidence: 0.80,
			},
			PeakActive: 10, DurationSec: 3.0,
		},
		{
			TriggerEvent: sentinel.TriggerEvent{
				Metric: "lock_sessions", Baseline: 0, Current: 8, Threshold: 5,
			},
			Classification: sentinel.Classification{
				Cause: sentinel.CauseLockContention, Confidence: 0.90,
			},
			PeakActive: 20, DurationSec: 8.0,
		},
	}

	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, sentSkill)

	result, err := s.Execute(context.Background(), p("args", "history"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Rendered
	if !strings.Contains(text, "2 条") {
		t.Errorf("should mention 2 records, got %q", text)
	}
	if !strings.Contains(text, "Lock Wait") {
		t.Error("should contain lock trigger metric (newest first)")
	}
	if !strings.Contains(text, "活跃会话") {
		t.Error("should contain active sessions trigger")
	}
}

func TestDiagnoseSkill_Execute_HistoryEmpty(t *testing.T) {
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, sentSkill)

	result, err := s.Execute(context.Background(), p("args", "history"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result.Rendered
	if !strings.Contains(text, "暂无") {
		t.Errorf("should say no records, got %q", text)
	}
}

func TestDiagnoseSkill_Execute_ByIndex(t *testing.T) {
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{
		{
			Classification: sentinel.Classification{
				Cause: sentinel.CauseBadSQL, Confidence: 0.70,
			},
			PeakActive: 8, DurationSec: 2.0,
		},
		{
			Classification: sentinel.Classification{
				Cause: sentinel.CauseLockContention, Confidence: 0.95,
			},
			PeakActive: 25, DurationSec: 10.0,
		},
	}

	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, sentSkill)

	// /diag 1 = newest (lock contention)
	result, err := s.Execute(context.Background(), p("args", "1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Rendered
	if !strings.Contains(text, "监控数据") {
		t.Error("index 1 should contain monitoring data")
	}
	if result.Summary != "monitor #1" {
		t.Errorf("summary should be monitor #1, got %q", result.Summary)
	}

	// /diag 2 = second newest (bad SQL)
	result, err = s.Execute(context.Background(), p("args", "2"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text = result.Rendered
	if !strings.Contains(text, "#2") {
		t.Error("should show report number for non-latest")
	}
}

func TestDiagnoseSkill_Execute_IndexOutOfRange(t *testing.T) {
	sentSkill := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	sentSkill.reports = []*sentinel.BurstReport{{
		Classification: sentinel.Classification{
			Cause: sentinel.CauseBadSQL, Confidence: 0.80,
		},
	}}

	s := NewDiagnoseSkill(model.NewManagerForTest(nil, "small"), nil, nil, sentSkill)

	result, err := s.Execute(context.Background(), p("args", "5"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Rendered
	if !strings.Contains(text, "超出范围") {
		t.Errorf("should say out of range, got %q", text)
	}
}

func TestSentinelSkill_ReportRingBuffer(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})

	// Simulate adding maxReports+2 reports.
	for i := 0; i < maxReports+2; i++ {
		s.mu.Lock()
		s.reports = append(s.reports, &sentinel.BurstReport{
			PeakActive: i,
		})
		if len(s.reports) > maxReports {
			s.reports = s.reports[len(s.reports)-maxReports:]
		}
		s.mu.Unlock()
	}

	if s.ReportCount() != maxReports {
		t.Errorf("ReportCount() = %d, want %d", s.ReportCount(), maxReports)
	}

	// LastReport should be the most recently added.
	last := s.LastReport()
	if last.PeakActive != maxReports+1 {
		t.Errorf("LastReport().PeakActive = %d, want %d", last.PeakActive, maxReports+1)
	}

	// ReportAt(1) = newest, ReportAt(maxReports) = oldest kept.
	newest := s.ReportAt(1)
	if newest.PeakActive != maxReports+1 {
		t.Errorf("ReportAt(1).PeakActive = %d, want %d", newest.PeakActive, maxReports+1)
	}

	oldest := s.ReportAt(maxReports)
	if oldest.PeakActive != 2 {
		t.Errorf("ReportAt(%d).PeakActive = %d, want 2", maxReports, oldest.PeakActive)
	}

	// Out of range.
	if s.ReportAt(0) != nil {
		t.Error("ReportAt(0) should be nil")
	}
	if s.ReportAt(maxReports+1) != nil {
		t.Errorf("ReportAt(%d) should be nil", maxReports+1)
	}
}
