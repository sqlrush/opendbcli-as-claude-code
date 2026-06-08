/*-------------------------------------------------------------------------
 *
 * prompt_builder_test.go
 *	  Tests for NativeFCBuilder (zero-impact default) and
 *	  SelectPromptBuilder dispatch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/prompt_builder_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import "testing"

func TestNativeFCBuilder_PreservesNativeToolCalls(t *testing.T) {
	b := NativeFCBuilder{}
	base := "你是 OpenDB 诊断专家..."
	tools := []ToolSchema{
		{Name: "health", Description: "health check"},
		{Name: "topsql", Description: "top sql"},
	}

	// BuildSystemPrompt must return base unchanged.
	if got := b.BuildSystemPrompt(base, tools); got != base {
		t.Errorf("BuildSystemPrompt must be identity for native, got %q", got)
	}

	// PrepareRequest must not mutate req.
	req := &Request{
		Tools:     tools,
		MaxTokens: 1000,
	}
	b.PrepareRequest(req)
	if len(req.Tools) != 2 || req.MaxTokens != 1000 {
		t.Errorf("PrepareRequest must not mutate req; got Tools=%d MaxTokens=%d", len(req.Tools), req.MaxTokens)
	}

	// PostProcessResponse must return resp unchanged.
	resp := &Response{
		Content:   "hello",
		ToolCalls: []ToolCall{{ID: "tc1", Name: "health"}},
	}
	out := b.PostProcessResponse(resp)
	if out.Content != "hello" || len(out.ToolCalls) != 1 {
		t.Errorf("PostProcessResponse must be identity; got %+v", out)
	}

	if b.Mode() != "native" {
		t.Errorf("Mode: got %q, want native", b.Mode())
	}
}

func TestNativeFCBuilder_PostProcessResponse_ParsesJSONContentFallback(t *testing.T) {
	b := NativeFCBuilder{}
	resp := &Response{
		Content: `{"tool_calls":[{"name":"health","args":{}}]}`,
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" {
		t.Errorf("name: got %q, want health", out.ToolCalls[0].Name)
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when fallback ToolCalls populated, got %q", out.Content)
	}
}

func TestNativeFCBuilder_PostProcessResponse_ParsesXMLContentFallback(t *testing.T) {
	b := NativeFCBuilder{}
	resp := &Response{
		Content: `<tool><name>health</name><args>{}</args></tool>`,
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" {
		t.Errorf("name: got %q, want health", out.ToolCalls[0].Name)
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when fallback ToolCalls populated, got %q", out.Content)
	}
}

func TestNativeFCBuilder_PostProcessResponse_ParsesSingularJSONContentFallback(t *testing.T) {
	b := NativeFCBuilder{}
	resp := &Response{
		Content: "```json\n" + `{"tool_call":{"name":"health","arguments":{}}}` + "\n```",
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" || out.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("unexpected tool call: %+v", out.ToolCalls[0])
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when fallback ToolCalls populated, got %q", out.Content)
	}
}

func TestNativeFCBuilder_PostProcessResponse_ParsesFunctionXMLContentFallback(t *testing.T) {
	b := NativeFCBuilder{}
	resp := &Response{
		Content: `<tool_call><function=health></function></tool_call>`,
	}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d", len(out.ToolCalls))
	}
	if out.ToolCalls[0].Name != "health" || out.ToolCalls[0].Arguments != "{}" {
		t.Fatalf("unexpected tool call: %+v", out.ToolCalls[0])
	}
	if out.Content != "" {
		t.Errorf("Content should be cleared when fallback ToolCalls populated, got %q", out.Content)
	}
}

func TestNativeFCBuilder_PostProcessResponse_PreservesPlainContent(t *testing.T) {
	b := NativeFCBuilder{}
	resp := &Response{Content: "这是普通回答，不是工具调用"}
	out := b.PostProcessResponse(resp)
	if len(out.ToolCalls) != 0 {
		t.Fatalf("plain content should not produce tool calls: %v", out.ToolCalls)
	}
	if out.Content != resp.Content {
		t.Errorf("plain content should be preserved, got %q", out.Content)
	}
}

func TestSelectPromptBuilder_DefaultsToNative(t *testing.T) {
	cases := map[string]string{
		"":               "native",
		"native":         "native",
		"auto":           "native",
		"nonsense_value": "native",
	}
	for in, want := range cases {
		got := SelectPromptBuilder(in)
		if got == nil {
			t.Errorf("SelectPromptBuilder(%q) returned nil — caller would need to handle, but for non-prompt modes nil is wrong", in)
			continue
		}
		if got.Mode() != want {
			t.Errorf("SelectPromptBuilder(%q): got Mode=%q, want %q", in, got.Mode(), want)
		}
	}
}

func TestSelectPromptBuilder_PromptReturnsNilForCallerWiring(t *testing.T) {
	// "prompt" needs the caller (openaicompat constructor) to build the
	// full PromptModeBuilder with ToolFilter / few-shot. SelectPromptBuilder
	// returns nil as a sentinel.
	if got := SelectPromptBuilder("prompt"); got != nil {
		t.Errorf("SelectPromptBuilder(prompt) should return nil sentinel for caller wiring, got %+v", got)
	}
}
