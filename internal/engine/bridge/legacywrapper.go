/*-------------------------------------------------------------------------
 *
 * legacywrapper.go
 *	  LegacyProviderWrapper wraps the old llm.Provider interface to
 *	  satisfy the new provider.ProviderAdapter interface. This allows
 *	  existing callers (diag_skill.go) to keep passing llm.Provider
 *	  without changes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/legacywrapper.go
 *
 *-------------------------------------------------------------------------
 */
package bridge

import (
	"context"
	"net/http"

	"github.com/sqlrush/opendb/internal/engine/provider"
	"github.com/sqlrush/opendb/internal/llm"
)

// LegacyProviderWrapper wraps the old llm.Provider interface to satisfy
// the new provider.ProviderAdapter interface. This allows existing callers
// (diag_skill.go) to keep passing llm.Provider without changes.
//
// v1.2.0: holds an optional PromptBuilder for tool-mode adaptation. The
// default NativeFCBuilder is a no-op (zero impact on existing behavior);
// pass PromptModeBuilder via WithPromptBuilder for non-FC LLM deployments.
type LegacyProviderWrapper struct {
	inner   llm.Provider
	caps    *provider.ProviderCapability
	builder provider.PromptBuilder
}

// WrapOption configures LegacyProviderWrapper at construction time.
type WrapOption func(*LegacyProviderWrapper)

// WithPromptBuilder injects a PromptBuilder for tool-mode adaptation.
// Use provider.NewPromptModeBuilder(...) for prompt-mode LLMs.
func WithPromptBuilder(b provider.PromptBuilder) WrapOption {
	return func(w *LegacyProviderWrapper) {
		if b != nil {
			w.builder = b
		}
	}
}

// WrapLegacyProvider creates a ProviderAdapter from an old llm.Provider.
// The capability is inferred from the provider name (ollama/openai).
// v1.2.0: accepts optional WrapOptions for PromptBuilder injection.
func WrapLegacyProvider(p llm.Provider, opts ...WrapOption) provider.ProviderAdapter {
	caps := inferCapability(p.Name())
	w := &LegacyProviderWrapper{
		inner:   p,
		caps:    caps,
		builder: provider.NativeFCBuilder{}, // default = pre-v1.2.0 behavior
	}
	for _, o := range opts {
		o(w)
	}
	return w
}

func (w *LegacyProviderWrapper) Name() string { return w.inner.Name() }

func (w *LegacyProviderWrapper) Capability() *provider.ProviderCapability { return w.caps }

func (w *LegacyProviderWrapper) EnhanceRequest(req *provider.Request) {}

func (w *LegacyProviderWrapper) ParseRateLimitHeaders(h http.Header) *provider.RateLimitInfo {
	return nil
}

// applyPromptBuilder runs the v1.2.0 PromptBuilder hooks (BuildSystemPrompt,
// PrepareRequest) before request conversion. NativeFCBuilder is a no-op so
// this is free for existing FC users.
func (w *LegacyProviderWrapper) applyPromptBuilder(req *provider.Request) {
	if w.builder == nil {
		return
	}
	// BuildSystemPrompt: rewrites SystemPrompt[0].Text. PromptModeBuilder
	// appends tool descriptions + Format A/B rules + few-shot to it.
	if len(req.SystemPrompt) > 0 {
		req.SystemPrompt[0].Text = w.builder.BuildSystemPrompt(req.SystemPrompt[0].Text, req.Tools)
	} else if len(req.Tools) > 0 {
		// Edge case: caller passed Tools but no SystemPrompt. Synthesize one
		// so PromptModeBuilder has somewhere to put tool descriptions.
		req.SystemPrompt = []provider.SystemPromptBlock{{
			Text: w.builder.BuildSystemPrompt("", req.Tools),
		}}
	}
	// PrepareRequest: PromptMode clears req.Tools so vLLM doesn't reject.
	w.builder.PrepareRequest(req)
}

// Chat converts new Request to old ChatRequest, calls inner, converts Response back.
// v1.2.0: PromptBuilder hooks run before/after the conversion so prompt-mode
// LLMs see tool descriptions in the system prompt and parsed tool_calls
// flow back to the engine.
func (w *LegacyProviderWrapper) Chat(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	w.applyPromptBuilder(req)
	oldReq := toOldRequest(req)
	oldResp, err := w.inner.Chat(ctx, oldReq)
	if err != nil {
		return nil, err
	}
	resp := fromOldResponse(oldResp)
	if w.builder != nil {
		resp = w.builder.PostProcessResponse(resp)
	}
	return resp, nil
}

// ChatStream converts and delegates to the old provider. v1.2.1: when the
// active PromptBuilder is PromptModeBuilder, wrap the resulting stream
// with promptStreamAdapter so chunks get routed through StreamingParser
// (Format A buffers + parses to ToolCalls; Format B passes through).
//
// Native FC mode: legacyStreamWrapper as before, zero overhead.
func (w *LegacyProviderWrapper) ChatStream(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	w.applyPromptBuilder(req)
	oldReq := toOldRequest(req)
	oldStream, err := w.inner.ChatStream(ctx, oldReq)
	if err != nil {
		return nil, err
	}
	legacy := &legacyStreamWrapper{inner: oldStream}
	// Only wrap with StreamingParser when running prompt mode. Native FC
	// streams emit structured ToolCalls already; we'd add latency for
	// nothing.
	if w.builder != nil && w.builder.Mode() == "prompt" {
		return newPromptStreamAdapter(legacy, w.toolNamesFromReq(req)), nil
	}
	return legacy, nil
}

// toolNamesFromReq extracts the list of tool names from req.Tools so the
// StreamingParser can do Levenshtein correction on tool names mid-stream.
// Empty list if no tools were declared (uncommon in prompt mode but safe
// to handle).
func (w *LegacyProviderWrapper) toolNamesFromReq(req *provider.Request) []string {
	if len(req.Tools) == 0 {
		return nil
	}
	names := make([]string, len(req.Tools))
	for i, t := range req.Tools {
		names[i] = t.Name
	}
	return names
}

// ── Conversion helpers ──

func toOldRequest(req *provider.Request) llm.ChatRequest {
	msgs := make([]llm.Message, 0, len(req.SystemPrompt)+len(req.Messages))

	// System prompt as first message
	if len(req.SystemPrompt) > 0 {
		var parts []string
		for _, b := range req.SystemPrompt {
			parts = append(parts, b.Text)
		}
		msgs = append(msgs, llm.Message{
			Role:    "system",
			Content: joinStrings(parts, "\n\n"),
		})
	}

	// Conversation messages
	for _, m := range req.Messages {
		msg := llm.Message{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.Thinking,
			ToolCallID:       m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
				ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			})
		}
		msgs = append(msgs, msg)
	}

	// Convert tools to old format
	var oldTools []any
	for _, t := range req.Tools {
		oldTools = append(oldTools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		})
	}

	return llm.ChatRequest{
		Messages:    msgs,
		Tools:       oldTools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}
}

func fromOldResponse(r *llm.Response) *provider.Response {
	resp := &provider.Response{
		Content:    r.Content,
		Thinking:   r.Thinking,
		StopReason: r.StopReason,
		Usage: provider.Usage{
			InputTokens:  r.Usage.InputTokens,
			OutputTokens: r.Usage.OutputTokens,
		},
	}

	if r.ReasoningContent != "" && resp.Thinking == "" {
		resp.Thinking = r.ReasoningContent
	}

	for _, tc := range r.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, provider.ToolCall{
			ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
		})
	}

	resp.Truncated = (r.StopReason == "length" || r.StopReason == "max_tokens")

	return resp
}

func inferCapability(providerName string) *provider.ProviderCapability {
	switch providerName {
	case "ollama":
		return &provider.ProviderCapability{
			Name:             "ollama",
			MaxContextWindow: 32_768,
			MaxOutputTokens:  4096,
			Thinking: provider.ThinkingCapability{
				Supported: true, Mode: provider.ThinkingAutoTags,
				MultiTurnPolicy: provider.ThinkingStripAll,
			},
			ToolCalling: provider.ToolCallingCapability{
				Supported: true, Format: provider.ToolFormatOpenAICompatible,
				TextFallback: true,
			},
			RateLimit: provider.RateLimitCapability{IsLocal: true},
		}
	default:
		return &provider.ProviderCapability{
			Name:             providerName,
			MaxContextWindow: 128_000,
			MaxOutputTokens:  8000,
			ToolCalling: provider.ToolCallingCapability{
				Supported: true, Format: provider.ToolFormatOpenAICompatible,
			},
		}
	}
}

// ── Legacy stream wrapper ──

type legacyStreamWrapper struct {
	inner llm.Stream
}

func (s *legacyStreamWrapper) Next() (provider.StreamEvent, error) {
	ev, err := s.inner.Next()
	if err != nil {
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: ev.FinishReason}, err
	}
	switch {
	case ev.ReasoningContent != "":
		// Kimi/MiMo/DeepSeek: reasoning_content → StreamThinkingDelta
		return provider.StreamEvent{Type: provider.StreamThinkingDelta, Content: ev.ReasoningContent}, nil
	case ev.Type == llm.StreamTextDelta:
		return provider.StreamEvent{Type: provider.StreamTextDelta, Content: ev.Content}, nil
	case ev.Type == llm.StreamToolCallDelta:
		var tc *provider.ToolCall
		if ev.ToolCall != nil {
			tc = &provider.ToolCall{ID: ev.ToolCall.ID, Name: ev.ToolCall.Name, Arguments: ev.ToolCall.Arguments}
		}
		return provider.StreamEvent{Type: provider.StreamToolCallDelta, ToolCall: tc}, nil
	case ev.Type == llm.StreamDone:
		return provider.StreamEvent{Type: provider.StreamDone, FinishReason: ev.FinishReason}, nil
	default:
		return provider.StreamEvent{Type: provider.StreamDone}, nil
	}
}

func (s *legacyStreamWrapper) Close() error { return s.inner.Close() }

func joinStrings(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for _, p := range parts[1:] {
		result += sep + p
	}
	return result
}
