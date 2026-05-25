/*-------------------------------------------------------------------------
 *
 * diagnose_test.go
 *	  Test cases for diagnose.go (agent package):
 *	  TestDiagnoser_Playbook, TestDiagnoser_Playbook_NoReport,
 *	  TestDiagnoser_Assist.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/diagnose_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
	"github.com/sqlrush/opendb/internal/skill"
)

func testBurstReport() *sentinel.BurstReport {
	return &sentinel.BurstReport{
		TriggerEvent: sentinel.TriggerEvent{
			Metric:     "active_non_idle",
			Baseline:   10,
			Current:    50,
			Threshold:  25,
			Multiplier: 5.0,
		},
		PeakActive:     50,
		BaselineActive: 10,
		RawFrameCount:  75,
		DurationSec:    15.0,
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.85,
			Evidence:   []string{"SQL abc123 出现率 85%"},
		},
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "abc123", OccurrenceRate: 0.85, MaxConcurrent: 8, WaitClass: "User I/O"},
		},
		WaitProfile: []sentinel.WaitBucket{
			{WaitClass: "User I/O", Percentage: 60},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"db%":    {Avg: 25, Max: 45, Trend: "spike"},
			"active": {Avg: 35, Max: 50, Trend: "spike"},
		},
	}
}

func TestDiagnoser_Playbook(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "## 根因分析\nSQL abc123 导致 I/O 瓶颈", Usage: llm.Usage{InputTokens: 500, OutputTokens: 200}},
		},
	}

	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	d := NewDiagnoser(provider, exec, reg)

	result, err := d.Diagnose(context.Background(), ModePlaybook, testBurstReport(), "数据库慢")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Mode != ModePlaybook {
		t.Errorf("mode = %q, want playbook", result.Mode)
	}
	if result.RoundsUsed != 1 {
		t.Errorf("rounds = %d, want 1", result.RoundsUsed)
	}
	if !strings.Contains(result.Analysis, "根因分析") {
		t.Error("analysis should contain LLM output")
	}
}

func TestDiagnoser_Playbook_NoReport(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			{Content: "请提供更多信息"},
		},
	}

	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	d := NewDiagnoser(provider, exec, reg)

	result, err := d.Diagnose(context.Background(), ModePlaybook, nil, "检查数据库")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Analysis == "" {
		t.Error("should have analysis even without report")
	}
}

func TestDiagnoser_Assist(t *testing.T) {
	querySkill := &mockSkill{
		name: "sessions",
		toolDef: skill.ToolDef{
			Name:       "sessions",
			Parameters: map[string]any{"type": "object"},
		},
		secLevel: security.LevelReadOnly,
		executeResult: &skill.Result{
			Type: skill.ResultText, Data: "10 active sessions",
		},
	}

	reg := newTestRegistry(querySkill)
	exec := newTestExecutor(reg)

	// Engine V2 uses native tool calls, not action blocks.
	provider := &mockProvider{
		responses: []*llm.Response{
			// Round 1: LLM requests data via native tool call
			toolCallResponse("让我检查会话",
				makeToolCall("call-1", "sessions", `{}`),
			),
			// Round 2: LLM gives final answer
			textResponse("诊断完成: 有10个活跃会话"),
		},
	}

	d := NewDiagnoser(provider, exec, reg)
	result, err := d.Diagnose(context.Background(), ModeAssist, testBurstReport(), "诊断")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Mode != ModeAssist {
		t.Errorf("mode = %q, want assist", result.Mode)
	}
	if !strings.Contains(result.Analysis, "10个活跃会话") {
		t.Errorf("analysis = %q, should contain final answer", result.Analysis)
	}
	if result.RoundsUsed != 2 {
		t.Errorf("rounds = %d, want 2", result.RoundsUsed)
	}
}

func TestDiagnoser_Auto(t *testing.T) {
	provider := &mockProvider{
		responses: []*llm.Response{
			textResponse("全面诊断完成"),
		},
	}

	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	d := NewDiagnoser(provider, exec, reg)

	result, err := d.Diagnose(context.Background(), ModeAuto, testBurstReport(), "自动诊断")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Mode != ModeAuto {
		t.Errorf("mode = %q, want auto", result.Mode)
	}
}

func TestDiagnoser_InvalidMode(t *testing.T) {
	provider := &mockProvider{}
	reg := newTestRegistry()
	exec := newTestExecutor(reg)
	d := NewDiagnoser(provider, exec, reg)

	_, err := d.Diagnose(context.Background(), DiagnoseMode("invalid"), nil, "test")
	if err == nil {
		t.Fatal("expected error for invalid mode")
	}
}

// readOnlyFilter test removed — filtering now handled by engine/profile.PromptProfile.ToolFilter()

func TestFormatDiagnoseResult(t *testing.T) {
	dr := DiagnoseResult{
		Mode:       ModePlaybook,
		Analysis:   "## 根因分析\n问题SQL导致I/O瓶颈",
		RoundsUsed: 1,
		TokensUsed: llm.Usage{InputTokens: 500, OutputTokens: 200},
	}

	output := FormatDiagnoseResult(dr)

	checks := []string{
		"playbook",
		"根因分析",
		"tokens",
	}
	for _, check := range checks {
		if !strings.Contains(output, check) {
			t.Errorf("output missing %q", check)
		}
	}
}

func TestFormatDiagnoseResult_Empty(t *testing.T) {
	dr := DiagnoseResult{Mode: ModePlaybook, RoundsUsed: 1}
	output := FormatDiagnoseResult(dr)

	if !strings.Contains(output, "playbook") {
		t.Error("should contain mode")
	}
}

