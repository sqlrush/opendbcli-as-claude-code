/*-------------------------------------------------------------------------
 *
 * builder_test.go
 *	  Test cases for builder.go (context package):
 *	  TestBuildSystemPromptOracle, TestBuildSystemPromptNoCaching,
 *	  TestBuildMessagesWithReport.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/builder_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/profile"
	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestBuildSystemPromptOracle(t *testing.T) {
	b := NewBuilder(
		profile.NewProfile("oracle"),
		&provider.ProviderCapability{Caching: provider.CachingCapability{Mode: provider.CachingExplicit}},
		"",
	)

	input := BuildInput{Mode: "auto", Product: "oracle", Version: "19c"}
	result := b.Build(input)

	if len(result.SystemPrompt) < 3 {
		t.Errorf("expected at least 3 system prompt blocks, got %d", len(result.SystemPrompt))
	}
	if !strings.Contains(result.SystemPrompt[0].Text, "OpenDB 数据库诊断专家") {
		t.Error("first block should contain identity")
	}
	if result.SystemPrompt[0].CacheControl == nil {
		t.Error("expected cache control on first block for Anthropic")
	}
	if !strings.Contains(result.SystemPrompt[1].Text, "Oracle") {
		t.Error("second block should contain Oracle rules")
	}

	found := false
	for _, block := range result.SystemPrompt {
		if strings.Contains(block.Text, "auto（自动诊断）") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected auto mode modifier")
	}
}

func TestBuildSystemPromptNoCaching(t *testing.T) {
	b := NewBuilder(
		profile.NewProfile("mysql"),
		&provider.ProviderCapability{Caching: provider.CachingCapability{Mode: provider.CachingNone}},
		"",
	)

	input := BuildInput{Mode: "assist"}
	result := b.Build(input)

	for _, block := range result.SystemPrompt {
		if block.CacheControl != nil {
			t.Error("should not have cache control when caching mode is None")
		}
	}
}

func TestBuildMessagesWithReport(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")

	input := BuildInput{
		UserMessage:      "数据库响应变慢",
		CompressedReport: "触发: db%=85.3, 活跃会话=250",
		Product:          "oracle",
		Version:          "19c",
		Instance:         "orcl",
		Mode:             "auto",
		MaxTurns:         20,
	}

	result := b.Build(input)

	if len(result.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(result.Messages))
	}

	env := result.Messages[0]
	if !env.IsMeta {
		t.Error("environment message should be IsMeta")
	}
	if !strings.Contains(env.Content, "oracle") {
		t.Error("env should contain product")
	}
	if !strings.Contains(env.Content, "orcl") {
		t.Error("env should contain instance")
	}

	diag := result.Messages[1]
	if diag.IsMeta {
		t.Error("diagnose message should not be IsMeta")
	}
	if !strings.Contains(diag.Content, "数据库响应变慢") {
		t.Error("should contain user message")
	}
	if !strings.Contains(diag.Content, "db%=85.3") {
		t.Error("should contain report")
	}
}

func TestBuildMessagesWithoutReport(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")
	input := BuildInput{UserMessage: "查看数据库状态", Product: "oracle"}
	result := b.Build(input)

	diag := result.Messages[1]
	if !strings.Contains(diag.Content, "查看数据库状态") {
		t.Error("should contain user message")
	}
	if strings.Contains(diag.Content, "异常报告") {
		t.Error("should not contain report section")
	}
}

func TestInjectTurnContextEarlyTurn(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")
	messages := []Message{{Role: "user", Content: "test"}}

	result := b.InjectTurnContext(messages, 3, 20, nil)
	if len(result) != len(messages) {
		t.Error("should not inject at early turn")
	}
}

func TestInjectTurnContextConvergence(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")
	messages := []Message{{Role: "user", Content: "test"}}

	result := b.InjectTurnContext(messages, 18, 20, []string{"health", "waits", "health"})
	// 3 messages: original + tool history + convergence hint
	if len(result) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result))
	}
	// Message 1: tool history (deduplicated)
	if !result[1].IsMeta {
		t.Error("tool history should be IsMeta")
	}
	if !strings.Contains(result[1].Content, "health, waits") {
		t.Errorf("tool history should contain deduplicated tools, got %q", result[1].Content)
	}
	// Message 2: convergence hint
	if !result[2].IsMeta {
		t.Error("convergence hint should be IsMeta")
	}
	if !strings.Contains(result[2].Content, "19/20") {
		t.Error("convergence hint should contain turn count")
	}
}

func TestInjectTurnContextToolHistoryEveryRound(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")
	messages := []Message{{Role: "user", Content: "test"}}

	// Early turn (turn 2 of 20) with tools — should inject tool history but NOT convergence
	result := b.InjectTurnContext(messages, 2, 20, []string{"health", "activesessions"})
	if len(result) != 2 {
		t.Errorf("expected 2 messages (original + tool history), got %d", len(result))
	}
	if !strings.Contains(result[1].Content, "health, activesessions") {
		t.Errorf("should contain tool history, got %q", result[1].Content)
	}
	if strings.Contains(result[1].Content, "轮") {
		t.Error("should NOT contain convergence hint at early turn")
	}
}

func TestInjectTurnContextNoToolsEarlyRound(t *testing.T) {
	b := NewBuilder(profile.NewProfile("oracle"), nil, "")
	messages := []Message{{Role: "user", Content: "test"}}

	// Early turn with no tools — no injection at all
	result := b.InjectTurnContext(messages, 0, 20, nil)
	if len(result) != len(messages) {
		t.Error("should not inject when no tools and early turn")
	}
}

func TestPrepareMessagesPreserveAll(t *testing.T) {
	b := NewBuilder(
		profile.NewProfile("oracle"),
		&provider.ProviderCapability{
			Thinking: provider.ThinkingCapability{MultiTurnPolicy: provider.ThinkingPreserveAll},
		},
		"",
	)
	messages := []Message{{Role: "assistant", Thinking: "reasoning", Content: "result"}}
	result := b.PrepareMessagesForNextTurn(messages, true)
	if result[0].Thinking != "reasoning" {
		t.Error("PreserveAll should keep thinking")
	}
}

func TestPrepareMessagesStripBetweenTurns(t *testing.T) {
	b := NewBuilder(
		profile.NewProfile("oracle"),
		&provider.ProviderCapability{
			Thinking: provider.ThinkingCapability{MultiTurnPolicy: provider.ThinkingStripBetweenTurns},
		},
		"",
	)
	messages := []Message{{Role: "assistant", Thinking: "reasoning", Content: "result"}}

	stripped := b.PrepareMessagesForNextTurn(messages, true)
	if stripped[0].Thinking != "" {
		t.Error("should strip on new user turn")
	}

	kept := b.PrepareMessagesForNextTurn(messages, false)
	if kept[0].Thinking != "reasoning" {
		t.Error("should keep within tool chain")
	}
}

func TestPrepareMessagesImmutability(t *testing.T) {
	b := NewBuilder(
		profile.NewProfile("oracle"),
		&provider.ProviderCapability{
			Thinking: provider.ThinkingCapability{MultiTurnPolicy: provider.ThinkingStripAll},
		},
		"",
	)
	messages := []Message{{Role: "assistant", Thinking: "reasoning"}}
	b.PrepareMessagesForNextTurn(messages, true)
	if messages[0].Thinking != "reasoning" {
		t.Error("original mutated")
	}
}

func TestUniversalSystemPromptContent(t *testing.T) {
	// Strict variant (default / large model) — full v1.1.15 prompt.
	strict := universalSystemPrompt("large")
	for _, check := range []string{"核心原则", "推理流程", "工具使用", "输出格式", "全局约束", "禁止编造", "完成标准"} {
		if !strings.Contains(strict, check) {
			t.Errorf("strict variant should contain %q", check)
		}
	}

	// Templated variant (small / medium model) — v1.1.16 fill-in patterns.
	templated := universalSystemPrompt("small")
	for _, check := range []string{"核心原则", "主动深挖", "输出模板", "自检清单", "禁止编造"} {
		if !strings.Contains(templated, check) {
			t.Errorf("templated variant should contain %q", check)
		}
	}

	// Both variants must differ — otherwise capability split is broken.
	if strict == templated {
		t.Error("strict and templated variants should differ")
	}

	// Empty / unknown capability falls back to strict.
	if universalSystemPrompt("") != strict {
		t.Error("empty capability should default to strict variant")
	}
}

func TestModeModifier(t *testing.T) {
	if !strings.Contains(modeModifier("playbook"), "playbook") {
		t.Error("playbook missing")
	}
	if !strings.Contains(modeModifier("assist"), "assist") {
		t.Error("assist missing")
	}
	if !strings.Contains(modeModifier("auto"), "auto") {
		t.Error("auto missing")
	}
	if modeModifier("unknown") != "" {
		t.Error("unknown should return empty")
	}
}
