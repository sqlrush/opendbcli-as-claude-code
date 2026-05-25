/*-------------------------------------------------------------------------
 *
 * gemini.go
 *	  GeminiAdapter implements Google's Gemini generateContent API.
 *	  Completely independent wire format: parts arrays,
 *	  system_instruction, function_declarations, etc.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/gemini.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiAdapter implements Google's Gemini generateContent API.
// Completely independent wire format: parts arrays, system_instruction,
// function_declarations, etc.
type GeminiAdapter struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewGeminiAdapter creates an adapter for the Gemini API.
func NewGeminiAdapter(baseURL, model, apiKey string) *GeminiAdapter {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return &GeminiAdapter{
		baseURL:    strings.TrimRight(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (a *GeminiAdapter) Name() string { return "gemini" }

func (a *GeminiAdapter) Capability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "gemini",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  65_536,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingBudget,
			MultiTurnPolicy: ThinkingPreserveAll,
			ExtractField:    "thought_parts",
			EnableParams: func(level string) map[string]any {
				return map[string]any{
					"generationConfig": map[string]any{
						"thinkingConfig": map[string]any{"thinkingBudget": -1},
					},
				}
			},
		},
		Caching: CachingCapability{
			Mode:           CachingSeparateAPI,
			MinCacheTokens: 1024,
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatGeminiNative,
		},
		RateLimit: RateLimitCapability{
			HasRetryAfter: true,
		},
	}
}

func (a *GeminiAdapter) EnhanceRequest(req *Request) {
	caps := a.Capability()
	if caps.Thinking.Supported && caps.Thinking.EnableParams != nil {
		for k, v := range caps.Thinking.EnableParams("high") {
			req.Extra[k] = v
		}
	}
}

// Chat sends a non-streaming generateContent request.
func (a *GeminiAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("gemini: build request: %w", err)
	}

	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", a.baseURL, a.model, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemini: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, &HTTPError{StatusCode: httpResp.StatusCode, Headers: httpResp.Header, Body: string(respBody)}
	}

	var gemResp geminiResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&gemResp); err != nil {
		return nil, fmt.Errorf("gemini: decode response: %w", err)
	}

	return a.normalizeResponse(gemResp, httpResp.Header), nil
}

// ChatStream is not yet implemented for Gemini (uses non-streaming fallback).
func (a *GeminiAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	// Gemini streaming uses streamGenerateContent — defer to non-streaming for now
	resp, err := a.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	return &singleResponseStream{resp: resp, done: false}, nil
}

func (a *GeminiAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	return nil
}

// ── Request building (Gemini format) ──

func (a *GeminiAdapter) buildRequestBody(req *Request) ([]byte, error) {
	body := map[string]any{}

	// system_instruction (top-level, separate from contents)
	if len(req.SystemPrompt) > 0 {
		var parts []map[string]any
		for _, block := range req.SystemPrompt {
			parts = append(parts, map[string]any{"text": block.Text})
		}
		body["system_instruction"] = map[string]any{"parts": parts}
	}

	// contents (Gemini's message format)
	var contents []map[string]any
	for _, m := range req.Messages {
		role := m.Role
		if role == "assistant" {
			role = "model" // Gemini uses "model" not "assistant"
		}
		if role == "tool" {
			// Tool results in Gemini have a special format
			contents = append(contents, map[string]any{
				"role": "function",
				"parts": []map[string]any{{
					"functionResponse": map[string]any{
						"name":     m.ToolCallID,
						"response": map[string]any{"content": m.Content},
					},
				}},
			})
			continue
		}
		if role == "system" {
			continue // Handled by system_instruction
		}

		var parts []map[string]any
		if m.Content != "" {
			parts = append(parts, map[string]any{"text": m.Content})
		}
		for _, tc := range m.ToolCalls {
			var args any
			json.Unmarshal([]byte(tc.Arguments), &args)
			parts = append(parts, map[string]any{
				"functionCall": map[string]any{"name": tc.Name, "args": args},
			})
		}

		if len(parts) > 0 {
			contents = append(contents, map[string]any{"role": role, "parts": parts})
		}
	}
	body["contents"] = contents

	// Tools (Gemini format: function_declarations)
	if len(req.Tools) > 0 {
		var funcDecls []map[string]any
		for _, t := range req.Tools {
			funcDecls = append(funcDecls, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			})
		}
		body["tools"] = []map[string]any{{"function_declarations": funcDecls}}
	}

	// Generation config
	genConfig := map[string]any{}
	if req.MaxTokens > 0 {
		genConfig["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		genConfig["temperature"] = *req.Temperature
	}

	// Merge extra (may contain thinkingConfig via generationConfig)
	for k, v := range req.Extra {
		if k == "generationConfig" {
			if gc, ok := v.(map[string]any); ok {
				for gk, gv := range gc {
					genConfig[gk] = gv
				}
			}
		} else if !strings.HasPrefix(k, "_") {
			body[k] = v
		}
	}

	if len(genConfig) > 0 {
		body["generationConfig"] = genConfig
	}

	return json.Marshal(body)
}

// ── Response normalization ──

func (a *GeminiAdapter) normalizeResponse(resp geminiResponse, headers http.Header) *Response {
	r := &Response{
		RawHeaders: headerToMap(headers),
	}

	if len(resp.Candidates) > 0 {
		candidate := resp.Candidates[0]

		for _, part := range candidate.Content.Parts {
			if part.Thought {
				r.Thinking += part.Text
			} else if part.Text != "" {
				r.Content += part.Text
			}
			if part.FunctionCall != nil {
				args, _ := json.Marshal(part.FunctionCall.Args)
				r.ToolCalls = append(r.ToolCalls, ToolCall{
					ID:        part.FunctionCall.Name, // Gemini doesn't use separate IDs
					Name:      part.FunctionCall.Name,
					Arguments: string(args),
				})
			}
		}

		if candidate.FinishReason != "" {
			r.StopReason = candidate.FinishReason
		}
	}

	if resp.UsageMetadata != nil {
		r.Usage = Usage{
			InputTokens:   resp.UsageMetadata.PromptTokenCount,
			OutputTokens:  resp.UsageMetadata.CandidatesTokenCount,
			ThinkingTokens: resp.UsageMetadata.ThoughtsTokenCount,
		}
	}

	r.Truncated = (r.StopReason == "MAX_TOKENS")

	return r
}

// ── Gemini response types ──

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role"`
}

type geminiPart struct {
	Text         string              `json:"text,omitempty"`
	Thought      bool                `json:"thought,omitempty"`
	FunctionCall *geminiFunctionCall `json:"functionCall,omitempty"`
}

type geminiFunctionCall struct {
	Name string `json:"name"`
	Args any    `json:"args"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	ThoughtsTokenCount   int `json:"thoughtsTokenCount"`
}

// singleResponseStream wraps a Response as a Stream for non-streaming fallback.
type singleResponseStream struct {
	resp *Response
	done bool
}

func (s *singleResponseStream) Next() (StreamEvent, error) {
	if s.done {
		return StreamEvent{Type: StreamDone}, io.EOF
	}
	s.done = true
	return StreamEvent{Type: StreamTextDelta, Content: s.resp.Content}, nil
}

func (s *singleResponseStream) Close() error { return nil }
