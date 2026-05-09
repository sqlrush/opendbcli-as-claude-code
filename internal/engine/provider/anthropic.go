/*-------------------------------------------------------------------------
 *
 * anthropic.go
 *	  AnthropicAdapter implements the Anthropic Messages API. Completely
 *	  independent wire format from OpenAI — messages use content
 *	  arrays, system is a top-level parameter, tools use input_schema,
 *	  etc.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/anthropic.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AnthropicAdapter implements the Anthropic Messages API.
// Completely independent wire format from OpenAI — messages use content arrays,
// system is a top-level parameter, tools use input_schema, etc.
type AnthropicAdapter struct {
	baseURL    string
	apiKey     string
	model      string
	version    string   // anthropic-version header
	betas      []string // anthropic-beta features
	httpClient *http.Client
}

// NewAnthropicAdapter creates an adapter for the Anthropic Messages API.
func NewAnthropicAdapter(baseURL, model, apiKey string) *AnthropicAdapter {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &AnthropicAdapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		version: "2023-06-01",
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (a *AnthropicAdapter) Name() string { return "anthropic" }

func (a *AnthropicAdapter) Capability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "anthropic",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  128_000,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAdaptive,
			MultiTurnPolicy: ThinkingPreserveAll,
			ExtractField:    "thinking_blocks",
			EnableParams: func(level string) map[string]any {
				return map[string]any{
					"thinking": map[string]any{"type": "adaptive"},
				}
			},
		},
		Caching: CachingCapability{
			Mode:             CachingExplicit,
			MaxBreakpoints:   4,
			MinCacheTokens:   1024,
			CacheReadField:   "cache_read_input_tokens",
			CacheCreateField: "cache_creation_input_tokens",
		},
		ToolCalling: ToolCallingCapability{
			Supported:           true,
			Format:              ToolFormatAnthropicNative,
			SupportsStrict:      true,
			ThinkingConstraints: []string{"tool_choice must be auto or none with thinking"},
		},
		RateLimit: RateLimitCapability{
			HeaderPrefix:  "anthropic-ratelimit",
			HasRetryAfter: true,
			OverloadCode:  529,
		},
		Output: OutputCapability{
			SupportsEffort:     true,
			EffortLevels:       []string{"low", "medium", "high", "max"},
			SupportsSpeed:      true,
			SupportsTaskBudget: true,
		},
	}
}

func (a *AnthropicAdapter) EnhanceRequest(req *Request) {
	caps := a.Capability()
	if caps.Thinking.Supported && caps.Thinking.EnableParams != nil {
		for k, v := range caps.Thinking.EnableParams("high") {
			req.Extra[k] = v
		}
	}
	if caps.Output.SupportsEffort {
		req.Extra["effort"] = "high"
	}
}

// Chat sends a non-streaming request to Anthropic Messages API.
func (a *AnthropicAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Headers: httpResp.Header, Body: string(respBody)}
	}

	var antResp anthropicResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&antResp); err != nil {
		return nil, fmt.Errorf("anthropic: decode response: %w", err)
	}

	return a.normalizeResponse(antResp, httpResp.Header), nil
}

// ChatStream sends a streaming request.
func (a *AnthropicAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	req.Stream = true
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: build stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create stream request: %w", err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: stream request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Headers: httpResp.Header, Body: string(respBody)}
	}

	return &sseStream{reader: bufio.NewReader(httpResp.Body), body: httpResp.Body}, nil
}

func (a *AnthropicAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	return nil // Handled by retry package via capability.HeaderPrefix
}

// ── Request building (Anthropic format) ──

func (a *AnthropicAdapter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", a.apiKey)
	req.Header.Set("anthropic-version", a.version)
	if len(a.betas) > 0 {
		req.Header.Set("anthropic-beta", strings.Join(a.betas, ","))
	}
}

func (a *AnthropicAdapter) buildRequestBody(req *Request) ([]byte, error) {
	body := map[string]any{
		"model": a.model,
	}

	// System prompt as top-level parameter (NOT in messages)
	if len(req.SystemPrompt) > 0 {
		var sysBlocks []map[string]any
		for _, block := range req.SystemPrompt {
			b := map[string]any{"type": "text", "text": block.Text}
			if block.CacheControl != nil {
				cc := map[string]any{"type": block.CacheControl.Type}
				if block.CacheControl.TTL != "" {
					cc["ttl"] = block.CacheControl.TTL
				}
				b["cache_control"] = cc
			}
			sysBlocks = append(sysBlocks, b)
		}
		body["system"] = sysBlocks
	}

	// Messages with content arrays
	var messages []map[string]any
	for _, m := range req.Messages {
		msg := map[string]any{"role": m.Role}

		// Anthropic uses content arrays, not a single string
		var content []map[string]any

		// Thinking blocks (must be preserved in multi-turn)
		for _, tb := range m.ThinkingBlocks {
			content = append(content, map[string]any{
				"type":      tb.Type,
				"thinking":  tb.Thinking,
				"signature": tb.Signature,
			})
		}

		// Text content
		if m.Content != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": m.Content,
			})
		}

		// Tool use blocks
		for _, tc := range m.ToolCalls {
			var input any
			json.Unmarshal([]byte(tc.Arguments), &input)
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Name,
				"input": input,
			})
		}

		// Tool result
		if m.ToolCallID != "" {
			msg["role"] = "user"
			content = []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
			}}
		}

		if len(content) > 0 {
			msg["content"] = content
		} else if m.Content != "" {
			msg["content"] = m.Content
		}

		messages = append(messages, msg)
	}
	body["messages"] = messages

	// Max tokens
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	} else {
		body["max_tokens"] = 8192
	}

	// Tools (Anthropic format: name + input_schema, no function wrapper)
	if len(req.Tools) > 0 {
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.InputSchema,
			})
		}
		body["tools"] = tools
	}

	// Stream
	if req.Stream {
		body["stream"] = true
	}

	// Extra params (thinking, effort, speed, task_budget)
	for k, v := range req.Extra {
		if !strings.HasPrefix(k, "_") {
			body[k] = v
		}
	}

	return json.Marshal(body)
}

// ── Response normalization ──

func (a *AnthropicAdapter) normalizeResponse(resp anthropicResponse, headers http.Header) *Response {
	r := &Response{
		StopReason: resp.StopReason,
		Usage: Usage{
			InputTokens:         resp.Usage.InputTokens,
			OutputTokens:        resp.Usage.OutputTokens,
			CacheCreationTokens: resp.Usage.CacheCreationInputTokens,
			CacheReadTokens:     resp.Usage.CacheReadInputTokens,
		},
		CacheStats: CacheStats{
			Hit:       resp.Usage.CacheReadInputTokens > 0,
			HitTokens: resp.Usage.CacheReadInputTokens,
		},
		RawHeaders: headerToMap(headers),
	}

	// Parse content array: extract text, thinking blocks, and tool calls
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			r.Content += block.Text
		case "thinking":
			r.Thinking += block.Thinking
			r.ThinkingBlocks = append(r.ThinkingBlocks, ThinkingBlock{
				Type:      "thinking",
				Thinking:  block.Thinking,
				Signature: block.Signature,
			})
		case "tool_use":
			args, _ := json.Marshal(block.Input)
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: string(args),
			})
		}
	}

	r.Truncated = (resp.StopReason == "max_tokens")

	return r
}

// ── Anthropic response types ──

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Usage      anthropicUsage          `json:"usage"`
}

type anthropicContentBlock struct {
	Type      string `json:"type"` // "text", "thinking", "tool_use"
	Text      string `json:"text,omitempty"`
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Input     any    `json:"input,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}
