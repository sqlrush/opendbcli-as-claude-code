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
type LegacyProviderWrapper struct {
	inner llm.Provider
	caps  *provider.ProviderCapability
}

// WrapLegacyProvider creates a ProviderAdapter from an old llm.Provider.
// The capability is inferred from the provider name (ollama/openai).
func WrapLegacyProvider(p llm.Provider) provider.ProviderAdapter {
	caps := inferCapability(p.Name())
	return &LegacyProviderWrapper{inner: p, caps: caps}
}

func (w *LegacyProviderWrapper) Name() string { return w.inner.Name() }

func (w *LegacyProviderWrapper) Capability() *provider.ProviderCapability { return w.caps }

func (w *LegacyProviderWrapper) EnhanceRequest(req *provider.Request) {}

func (w *LegacyProviderWrapper) ParseRateLimitHeaders(h http.Header) *provider.RateLimitInfo {
	return nil
}

// Chat converts new Request to old ChatRequest, calls inner, converts Response back.
func (w *LegacyProviderWrapper) Chat(ctx context.Context, req *provider.Request) (*provider.Response, error) {
	oldReq := toOldRequest(req)
	oldResp, err := w.inner.Chat(ctx, oldReq)
	if err != nil {
		return nil, err
	}
	return fromOldResponse(oldResp), nil
}

// ChatStream converts and delegates to the old provider.
func (w *LegacyProviderWrapper) ChatStream(ctx context.Context, req *provider.Request) (provider.Stream, error) {
	oldReq := toOldRequest(req)
	oldStream, err := w.inner.ChatStream(ctx, oldReq)
	if err != nil {
		return nil, err
	}
	return &legacyStreamWrapper{inner: oldStream}, nil
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
