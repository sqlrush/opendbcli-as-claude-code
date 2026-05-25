/*-------------------------------------------------------------------------
 *
 * prompts_test.go
 *	  Test cases for prompts.go (agent package):
 *	  TestDiagnoseMode_MaxRounds, TestDiagnoseMode_IsValid,
 *	  TestSystemPromptForDiagnose_Playbook.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/prompts_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"strings"
	"testing"
)

func TestDiagnoseMode_MaxRounds(t *testing.T) {
	tests := []struct {
		mode DiagnoseMode
		want int
	}{
		{ModePlaybook, 1},
		{ModeAssist, DefaultMaxRounds},  // 0 in map → capped at DefaultMaxRounds
		{ModeAuto, DefaultMaxRounds},    // 0 in map → capped at DefaultMaxRounds
		{DiagnoseMode("invalid"), DefaultMaxRounds},
	}
	for _, tt := range tests {
		if got := tt.mode.MaxRounds(); got != tt.want {
			t.Errorf("%s.MaxRounds() = %d, want %d", tt.mode, got, tt.want)
		}
	}
}

func TestDiagnoseMode_IsValid(t *testing.T) {
	tests := []struct {
		mode DiagnoseMode
		want bool
	}{
		{ModePlaybook, true},
		{ModeAssist, true},
		{ModeAuto, true},
		{DiagnoseMode("invalid"), false},
		{DiagnoseMode(""), false},
	}
	for _, tt := range tests {
		if got := tt.mode.IsValid(); got != tt.want {
			t.Errorf("%q.IsValid() = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestSystemPromptForDiagnose_Playbook(t *testing.T) {
	prompt := SystemPromptForDiagnose(ModePlaybook)

	if !strings.Contains(prompt, "数据库诊断专家") {
		t.Error("missing base role")
	}
	if !strings.Contains(prompt, "playbook") {
		t.Error("missing mode identifier")
	}
	if !strings.Contains(prompt, "单次分析") {
		t.Error("missing playbook constraint")
	}
	if !strings.Contains(prompt, "根因分析") {
		t.Error("missing output format")
	}
}

func TestSystemPromptForDiagnose_Assist(t *testing.T) {
	prompt := SystemPromptForDiagnose(ModeAssist)

	if !strings.Contains(prompt, "assist") {
		t.Error("missing mode identifier")
	}
	if !strings.Contains(prompt, "3轮") {
		t.Error("missing round limit")
	}
	if !strings.Contains(prompt, "查询类工具") {
		t.Error("missing tool restriction")
	}
}

func TestSystemPromptForDiagnose_Auto(t *testing.T) {
	prompt := SystemPromptForDiagnose(ModeAuto)

	if !strings.Contains(prompt, "auto") {
		t.Error("missing mode identifier")
	}
	if !strings.Contains(prompt, "10轮") {
		t.Error("missing round limit")
	}
	if !strings.Contains(prompt, "所有工具") {
		t.Error("missing full tool access")
	}
}

func TestSystemPromptForDiagnose_Unknown(t *testing.T) {
	prompt := SystemPromptForDiagnose(DiagnoseMode("unknown"))

	// Should still return base prompt
	if !strings.Contains(prompt, "数据库诊断专家") {
		t.Error("unknown mode should still have base prompt")
	}
}

func TestSentinelAlertPrompt(t *testing.T) {
	report := "=== 性能异常报告 ===\n触发: active=50"
	prompt := SentinelAlertPrompt(report)

	if !strings.Contains(prompt, "Sentinel") {
		t.Error("missing sentinel reference")
	}
	if !strings.Contains(prompt, "active=50") {
		t.Error("missing report content")
	}
	if !strings.Contains(prompt, "根因分析") {
		t.Error("missing output format instruction")
	}
}

func TestDiagnoseUserPrompt_WithReport(t *testing.T) {
	prompt := DiagnoseUserPrompt("数据库很慢", "=== report ===")

	if !strings.Contains(prompt, "数据库很慢") {
		t.Error("missing user input")
	}
	if !strings.Contains(prompt, "=== report ===") {
		t.Error("missing report")
	}
}

func TestDiagnoseUserPrompt_WithoutReport(t *testing.T) {
	prompt := DiagnoseUserPrompt("检查数据库状态", "")

	if !strings.Contains(prompt, "检查数据库状态") {
		t.Error("missing user input")
	}
	if strings.Contains(prompt, "异常报告") {
		t.Error("should not mention report when empty")
	}
}
