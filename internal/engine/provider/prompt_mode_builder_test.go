/*-------------------------------------------------------------------------
 *
 * prompt_mode_builder_test.go
 *	  Tests for the PromptModeBuilder construction + per-stage behavior.
 *	  End-to-end LLM tests with a real model live in
 *	  benchmark/prompt_mode/.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/prompt_mode_builder_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"strings"
	"testing"
)

// trivialSerializer is the minimal serializer for unit tests — just
// concatenates tool names. Production wires tool.SerializeToolsCompact.
func trivialSerializer(tools []ToolSchema) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return strings.Join(names, ", ")
}

func TestPromptModeBuilder_Mode(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	if b.Mode() != "prompt" {
		t.Errorf("Mode: got %q, want prompt", b.Mode())
	}
}

func TestPromptModeBuilder_BuildSystemPrompt_IncludesTools(t *testing.T) {
	b := NewPromptModeBuilder(nil, WithToolSerializer(trivialSerializer))
	tools := []ToolSchema{
		{Name: "health"},
		{Name: "alert"},
	}
	out := b.BuildSystemPrompt("BASE_PROMPT", tools)

	for _, want := range []string{
		"BASE_PROMPT",
		"# 可用工具",
		"health, alert",
		"# 输出格式规则",
		"# 示例",
		"```json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("BuildSystemPrompt missing %q", want)
		}
	}
}

func TestPromptModeBuilder_BuildSystemPrompt_NoSerializerReturnsBase(t *testing.T) {
	// Defensive: no serializer = degraded mode, just return base.
	b := NewPromptModeBuilder(nil) // no WithToolSerializer
	tools := []ToolSchema{{Name: "health"}}
	out := b.BuildSystemPrompt("BASE", tools)
	if out != "BASE" {
		t.Errorf("without serializer should return base unchanged; got %q", out)
	}
}

func TestPromptModeBuilder_BuildSystemPrompt_AppliesFilter(t *testing.T) {
	calls := 0
	var filteredArg []ToolSchema
	filter := func(all []ToolSchema, ctx FilterContext) []ToolSchema {
		calls++
		// Only keep the first unique-prefix tool name to make verification trivial
		out := []ToolSchema{}
		for _, t := range all {
			if strings.HasPrefix(t.Name, "filteredprefix_") {
				out = append(out, t)
			}
		}
		filteredArg = out
		return out
	}
	b := NewPromptModeBuilder(nil,
		WithToolSerializer(trivialSerializer),
		WithToolFilter(filter),
	)
	b.SetTurnContext(FilterContext{UserMessage: "test"})

	tools := []ToolSchema{
		{Name: "filteredprefix_one"},
		{Name: "unrelated_two"},
		{Name: "unrelated_three"},
	}
	out := b.BuildSystemPrompt("BASE", tools)

	if calls != 1 {
		t.Errorf("filter should be called once, got %d", calls)
	}
	if len(filteredArg) != 1 || filteredArg[0].Name != "filteredprefix_one" {
		t.Errorf("filter saw wrong tools: %+v", filteredArg)
	}
	// The 可用工具 section (between "# 可用工具" and the next "#" line)
	// should contain only the filtered tool name.
	availStart := strings.Index(out, "# 可用工具")
	nextHeader := strings.Index(out[availStart+10:], "\n# ")
	availSection := out[availStart : availStart+10+nextHeader]
	if !strings.Contains(availSection, "filteredprefix_one") {
		t.Errorf("avail section missing filtered tool: %q", availSection)
	}
	if strings.Contains(availSection, "unrelated_two") || strings.Contains(availSection, "unrelated_three") {
		t.Errorf("avail section leaked unfiltered tools: %q", availSection)
	}
}

func TestPromptModeBuilder_PrepareRequest_ClearsTools(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	req := &Request{
		Tools: []ToolSchema{{Name: "health"}},
		// Other fields should remain.
		MaxTokens: 8000,
	}
	b.PrepareRequest(req)
	if req.Tools != nil {
		t.Errorf("Tools should be cleared, got %v", req.Tools)
	}
	if req.MaxTokens != 8000 {
		t.Errorf("MaxTokens should be preserved, got %d", req.MaxTokens)
	}
}

func TestPromptModeBuilder_PostProcessResponse_ExtractsToolCalls(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	resp := &Response{
		Content: "```json\n" + `{"tool_calls":[{"name":"health","args":{}}]}` + "\n```",
	}
	out := b.PostProcessResponse(resp)

	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" {
		t.Errorf("name: got %q, want health", out.ToolCalls[0].Name)
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when ToolCalls populated, got %q", out.Content)
	}
}

func TestPromptModeBuilder_PostProcessResponse_ExtractsICBCSimpleXMLToolCall(t *testing.T) {
	b := NewPromptModeBuilder([]string{"health"})
	resp := &Response{
		Content: `<think>
用户问"当前数据库有什么问题"，这是聚类层问题。
</think>

我先检查数据库整体健康状态。

<tool>
<name>health</name>
<args>
{}
</args>
</tool>`,
	}
	out := b.PostProcessResponse(resp)

	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 XML tool call, got %d; content=%q", len(out.ToolCalls), out.Content)
	}
	if out.ToolCalls[0].Name != "health" {
		t.Errorf("name: got %q, want health", out.ToolCalls[0].Name)
	}
	if out.ToolCalls[0].Arguments != "{}" {
		t.Errorf("args: got %q, want {}", out.ToolCalls[0].Arguments)
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when XML ToolCalls populated, got %q", out.Content)
	}
}

func TestPromptModeBuilder_PostProcessResponse_PreservesFormatB(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	resp := &Response{
		Content: "## 根因分析\nshared_buffers 偏小, 建议调到 4GB",
	}
	out := b.PostProcessResponse(resp)

	if len(out.ToolCalls) != 0 {
		t.Errorf("Format B should produce no tool calls, got %v", out.ToolCalls)
	}
	if !strings.Contains(out.Content, "根因分析") {
		t.Errorf("Format B content should be preserved, got %q", out.Content)
	}
}

func TestPromptModeBuilder_PostProcessResponse_NativeToolCallsPassthrough(t *testing.T) {
	// Hybrid backend gave structured ToolCalls already — don't re-parse Content.
	b := NewPromptModeBuilder(nil)
	resp := &Response{
		ToolCalls: []ToolCall{{ID: "abc", Name: "health"}},
		Content:   "some unrelated text the LLM also produced",
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].ID != "abc" {
		t.Errorf("native ToolCalls should pass through, got %v", out.ToolCalls)
	}
	if !strings.Contains(out.Content, "unrelated") {
		t.Errorf("Content should be untouched when ToolCalls preserved, got %q", out.Content)
	}
}

func TestPromptModeBuilder_PostProcessResponse_EmptyContentNoop(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	resp := &Response{Content: ""}
	out := b.PostProcessResponse(resp)
	if out != resp {
		t.Errorf("empty content should be no-op")
	}
}

func TestPromptModeBuilder_PostProcessResponse_NilSafe(t *testing.T) {
	b := NewPromptModeBuilder(nil)
	if got := b.PostProcessResponse(nil); got != nil {
		t.Errorf("nil response should pass through; got %+v", got)
	}
}

func TestPromptModeBuilder_LevenshteinCorrectionWiredThrough(t *testing.T) {
	// PromptModeBuilder should plumb knownToolNames into the parser so
	// LLM typos get corrected end-to-end.
	b := NewPromptModeBuilder([]string{"health", "topsql"})
	resp := &Response{
		Content: `{"tool_calls":[{"name":"heath","args":{}}]}`, // 'heath' typo
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" {
		t.Errorf("typo should be corrected to health, got %q", out.ToolCalls[0].Name)
	}
}

func TestPromptModeBuilder_FormatPromptOverheadSize(t *testing.T) {
	// Sanity check: format rules + few-shot should be < 5KB.
	overhead := FormatPromptBytes()
	if overhead < 500 {
		t.Errorf("overhead suspiciously small (%d bytes); likely prompt got truncated", overhead)
	}
	if overhead > 8000 {
		t.Errorf("overhead too large (%d bytes); trim few-shot examples", overhead)
	}
	t.Logf("Static prompt overhead: %d bytes", overhead)
}
