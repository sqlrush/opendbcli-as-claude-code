/*-------------------------------------------------------------------------
 *
 * openaicompat.go
 *	  OpenAICompatAdapter handles OpenAI-compatible APIs. Covers:
 *	  OpenAI, DeepSeek, Kimi, Qwen, GLM, MiniMax, MiMo, and generic.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/openaicompat.go
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

// OpenAICompatAdapter handles OpenAI-compatible APIs.
// Covers: OpenAI, DeepSeek, Kimi, Qwen, GLM, MiniMax, MiMo, and generic.
type OpenAICompatAdapter struct {
	baseURL    string
	model      string
	apiKey     string
	vendor     string // "openai"/"deepseek"/"kimi"/"qwen"/"glm"/"minimax"/"mimo"/"generic"
	httpClient *http.Client
}

// NewOpenAICompatAdapter creates an adapter for OpenAI-compatible providers.
func NewOpenAICompatAdapter(baseURL, model, apiKey, vendor string) *OpenAICompatAdapter {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if vendor == "" {
		vendor = DetectVendor(baseURL)
	}
	return &OpenAICompatAdapter{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		vendor:  vendor,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

func (a *OpenAICompatAdapter) Name() string { return a.vendor }

// Capability returns vendor-specific capabilities.
func (a *OpenAICompatAdapter) Capability() *ProviderCapability {
	switch a.vendor {
	case "deepseek":
		return a.deepseekCapability()
	case "kimi":
		return a.kimiCapability()
	case "qwen":
		return a.qwenCapability()
	case "glm":
		return a.glmCapability()
	case "minimax":
		return a.minimaxCapability()
	case "mimo":
		return a.mimoCapability()
	case "openai":
		return a.openaiCapability()
	default:
		return a.genericCapability()
	}
}

// EnhanceRequest injects vendor-specific parameters.
func (a *OpenAICompatAdapter) EnhanceRequest(req *Request) {
	caps := a.Capability()
	if caps.Thinking.Supported && caps.Thinking.EnableParams != nil {
		for k, v := range caps.Thinking.EnableParams("high") {
			req.Extra[k] = v
		}
	}
}

// Chat sends a non-streaming request.
func (a *OpenAICompatAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", a.vendor, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: create request: %w", a.vendor, err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: request failed: %w", a.vendor, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, &HTTPError{
			StatusCode: httpResp.StatusCode,
			Headers:    httpResp.Header,
			Body:       string(respBody),
		}
	}

	var oaiResp oaiResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", a.vendor, err)
	}

	return a.normalizeResponse(oaiResp, httpResp.Header), nil
}

// ChatStream sends a streaming request.
func (a *OpenAICompatAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	req.Stream = true
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, fmt.Errorf("%s: build stream request: %w", a.vendor, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: create stream request: %w", a.vendor, err)
	}
	a.setHeaders(httpReq)

	httpResp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: stream request failed: %w", a.vendor, err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, &HTTPError{
			StatusCode: httpResp.StatusCode,
			Headers:    httpResp.Header,
			Body:       string(respBody),
		}
	}

	return &sseStream{reader: bufio.NewReader(httpResp.Body), body: httpResp.Body}, nil
}

func (a *OpenAICompatAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	caps := a.Capability()
	if caps.RateLimit.HeaderPrefix == "" {
		return nil
	}
	// Delegate to retry package logic — for now return basic info
	info := &RateLimitInfo{}
	return info
}

// ── Request building ──

func (a *OpenAICompatAdapter) chatEndpoint() string {
	return a.baseURL + "/chat/completions"
}

func (a *OpenAICompatAdapter) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
}

func (a *OpenAICompatAdapter) buildRequestBody(req *Request) ([]byte, error) {
	messages := make([]oaiMessage, 0, len(req.Messages)+len(req.SystemPrompt))

	// System prompt as first message
	if len(req.SystemPrompt) > 0 {
		var parts []string
		for _, block := range req.SystemPrompt {
			parts = append(parts, block.Text)
		}
		messages = append(messages, oaiMessage{
			Role:    "system",
			Content: strings.Join(parts, "\n\n"),
		})
	}

	// Conversation messages
	for _, m := range req.Messages {
		msg := oaiMessage{
			Role:    m.Role,
			Content: m.Content,
		}
		if m.ToolCallID != "" {
			msg.ToolCallID = m.ToolCallID
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, oaiToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: oaiToolFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		messages = append(messages, msg)
	}

	body := map[string]any{
		"model":    a.model,
		"messages": messages,
	}
	if req.Stream {
		body["stream"] = true
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		body["tools"] = buildOAITools(req.Tools)
	}

	// Merge provider-specific Extra params
	for k, v := range req.Extra {
		if !strings.HasPrefix(k, "_") { // _ prefix = internal, don't send
			body[k] = v
		}
	}

	return json.Marshal(body)
}

func buildOAITools(tools []ToolSchema) []any {
	result := make([]any, len(tools))
	for i, t := range tools {
		result[i] = map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.InputSchema,
			},
		}
	}
	return result
}

// ── Response normalization ──

func (a *OpenAICompatAdapter) normalizeResponse(resp oaiResponse, headers http.Header) *Response {
	r := &Response{
		Usage: Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
		RawHeaders: headerToMap(headers),
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		r.Content = choice.Message.Content
		if choice.FinishReason != nil {
			r.StopReason = *choice.FinishReason
		}

		for _, tc := range choice.Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	// Extract thinking content based on vendor capability
	caps := a.Capability()
	switch caps.Thinking.ExtractField {
	case "reasoning_content":
		if len(resp.Choices) > 0 {
			r.Thinking = resp.Choices[0].Message.ReasoningContent
		}
	case "reasoning_details":
		if len(resp.Choices) > 0 {
			r.Thinking = resp.Choices[0].Message.ReasoningDetails
		}
	}

	// Auto-extract <think> tags for models that embed reasoning in content
	if caps.Thinking.Mode == ThinkingAutoTags && r.Thinking == "" && r.Content != "" {
		r.Thinking, r.Content = extractThinkTags(r.Content)
	}

	// Truncation detection
	r.Truncated = (r.StopReason == "length" || r.StopReason == "max_tokens")

	// Cache stats (vendor-specific fields). Try configured field first, then
	// fall back to common OpenAI/GLM/Qwen "cached_tokens" (flattened from
	// prompt_tokens_details.cached_tokens by oaiUsage.UnmarshalJSON).
	readField := caps.Caching.CacheReadField
	if readField == "" {
		readField = "cached_tokens"
	}
	if v, ok := resp.Usage.Extra[readField]; ok && v > 0 {
		r.CacheStats.HitTokens = v
		r.CacheStats.Hit = true
		r.Usage.CacheReadTokens = v
	}
	if caps.Caching.CacheMissField != "" {
		if v, ok := resp.Usage.Extra[caps.Caching.CacheMissField]; ok {
			r.CacheStats.MissTokens = v
			r.Usage.CacheMissTokens = v
		}
	}
	// Derive miss tokens if only hit count is reported.
	if r.CacheStats.MissTokens == 0 && r.CacheStats.HitTokens > 0 {
		if miss := resp.Usage.PromptTokens - r.CacheStats.HitTokens; miss > 0 {
			r.CacheStats.MissTokens = miss
		}
	}

	return r
}

// extractThinkTags extracts <think>...</think> blocks from content.
func extractThinkTags(content string) (thinking, clean string) {
	start := strings.Index(content, "<think>")
	if start == -1 {
		return "", content
	}
	end := strings.Index(content, "</think>")
	if end == -1 {
		return content[start+len("<think>"):], strings.TrimSpace(content[:start])
	}
	thinking = strings.TrimSpace(content[start+len("<think>") : end])
	clean = strings.TrimSpace(content[:start] + content[end+len("</think>"):])
	return
}

func headerToMap(h http.Header) map[string]string {
	m := make(map[string]string, len(h))
	for k := range h {
		m[k] = h.Get(k)
	}
	return m
}

// ── OAI types ──

// oaiMessage uses non-omitempty Content because DeepSeek V4's strict
// deserializer rejects messages without a `content` field (even for
// assistant-with-tool_calls or tool result messages where content is
// conventionally empty). Opus / V3 / OpenAI tolerate omission; V4 does not.
// Emitting `"content": ""` is safe across all providers.
type oaiMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ReasoningDetails string        `json:"reasoning_details,omitempty"`
	ToolCalls        []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolFunction `json:"function"`
}

type oaiToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiChoice struct {
	Message      oaiMessage `json:"message"`
	FinishReason *string    `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	Extra            map[string]int `json:"-"` // Vendor-specific fields populated by custom unmarshalling
}

// UnmarshalJSON captures all numeric fields into Extra (in addition to the
// typed PromptTokens/CompletionTokens), and flattens nested
// `prompt_tokens_details.cached_tokens` (OpenAI/GLM/Qwen standard) into
// Extra as "cached_tokens" so capability-driven lookup can find it.
func (u *oaiUsage) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	u.Extra = make(map[string]int, len(raw))
	for k, v := range raw {
		var n int
		if err := json.Unmarshal(v, &n); err == nil {
			u.Extra[k] = n
			switch k {
			case "prompt_tokens":
				u.PromptTokens = n
			case "completion_tokens":
				u.CompletionTokens = n
			}
			continue
		}
		// Nested object: e.g. prompt_tokens_details.cached_tokens (OpenAI/GLM/Qwen)
		var nested map[string]int
		if err := json.Unmarshal(v, &nested); err == nil {
			for nk, nv := range nested {
				u.Extra[nk] = nv
			}
		}
	}
	return nil
}

// ── SSE Stream ──

type sseStream struct {
	reader        *bufio.Reader
	body          io.ReadCloser
	pendingFinish string // finish_reason from a chunk that also carried content
}

func (s *sseStream) Next() (StreamEvent, error) {
	// Return saved finish_reason from a combined content+finish chunk.
	if s.pendingFinish != "" {
		fr := s.pendingFinish
		s.pendingFinish = ""
		return StreamEvent{Type: StreamDone, FinishReason: fr}, io.EOF
	}

	for {
		line, err := s.reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return StreamEvent{Type: StreamDone}, io.EOF
			}
			return StreamEvent{}, err
		}

		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return StreamEvent{Type: StreamDone}, io.EOF
		}

		var chunk oaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		delta := chunk.Choices[0].Message
		if delta.Content == "" && delta.ReasoningContent == "" {
			delta = oaiMessage(chunk.Choices[0].Message)
		}

		// Check for streaming delta format
		type streamChoice struct {
			Delta        oaiMessage `json:"delta"`
			FinishReason *string    `json:"finish_reason"`
		}
		var streamChunk struct {
			Choices []streamChoice `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &streamChunk); err == nil && len(streamChunk.Choices) > 0 {
			sd := streamChunk.Choices[0]
			if len(sd.Delta.ToolCalls) > 0 {
				tc := sd.Delta.ToolCalls[0]
				return StreamEvent{
					Type: StreamToolCallDelta,
					ToolCall: &ToolCall{
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}, nil
			}
			if sd.Delta.Content != "" {
				// Some providers pack finish_reason in the same chunk as the
				// last content delta. Save it so the next Next() returns it.
				if sd.FinishReason != nil {
					s.pendingFinish = *sd.FinishReason
				}
				return StreamEvent{Type: StreamTextDelta, Content: sd.Delta.Content}, nil
			}
			// Thinking-mode reasoning chunk (deepseek-v4-pro / kimi-k2.6).
			// Used to be dropped on the floor → engine's stream.Next() loop
			// kept reading without ever returning, blocking the select in
			// streamFinalResponse forever. v1.1.20 emit StreamThinkingDelta
			// so the engine sees stream is alive and can run a reasoning-only
			// watchdog (engine.go::streamReasoningOnlyTimeout).
			if sd.Delta.ReasoningContent != "" {
				return StreamEvent{
					Type:    StreamThinkingDelta,
					Content: sd.Delta.ReasoningContent,
				}, nil
			}
			if sd.FinishReason != nil {
				return StreamEvent{Type: StreamDone, FinishReason: *sd.FinishReason}, io.EOF
			}
		}
	}
}

func (s *sseStream) Close() error {
	return s.body.Close()
}

// ── Vendor capabilities ──

func (a *OpenAICompatAdapter) genericCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "generic",
		MaxContextWindow: 32_768,
		MaxOutputTokens:  4096,
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
	}
}

func (a *OpenAICompatAdapter) openaiCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "openai",
		MaxContextWindow: 272_000,
		MaxOutputTokens:  16_384,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingEffortLevel,
			MultiTurnPolicy: ThinkingPreserveAll,
			EnableParams: func(level string) map[string]any {
				return map[string]any{"reasoning": map[string]any{"effort": level}}
			},
		},
		ToolCalling: ToolCallingCapability{
			Supported:        true,
			Format:           ToolFormatOpenAICompatible,
			SupportsParallel: true,
			SupportsStrict:   true,
		},
		RateLimit: RateLimitCapability{
			HeaderPrefix:  "x-ratelimit",
			HasRetryAfter: true,
		},
		Output: OutputCapability{
			SupportsStructuredOutput: true,
			SupportsPredictedOutput:  true,
			SupportsSeed:             true,
		},
	}
}

func (a *OpenAICompatAdapter) deepseekCapability() *ProviderCapability {
	// V4 默认 256K context / 16K output, 比 V3 翻倍.
	// V3 仍按 128K/8K, 通过 model 名识别区分.
	maxCtx, maxOut := 128_000, 8_192
	lower := strings.ToLower(a.model)
	if strings.Contains(lower, "v4") {
		maxCtx, maxOut = 256_000, 16_384
	}
	return &ProviderCapability{
		Name:             "deepseek",
		MaxContextWindow: maxCtx,
		MaxOutputTokens:  maxOut,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"thinking": map[string]any{"type": "enabled"}}
			},
		},
		Caching: CachingCapability{
			Mode:           CachingAutomatic,
			CacheReadField: "prompt_cache_hit_tokens",
			CacheMissField: "prompt_cache_miss_tokens",
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
		Output: OutputCapability{FixedTemperature: true},
	}
}

func (a *OpenAICompatAdapter) kimiCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "kimi",
		MaxContextWindow: 256_000,
		MaxOutputTokens:  32_768,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingEnableFlag,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"thinking": map[string]any{"type": "enabled"}}
			},
		},
		ToolCalling: ToolCallingCapability{
			Supported:           true,
			Format:              ToolFormatOpenAICompatible,
			ThinkingConstraints: []string{"tool_choice must be auto or none"},
		},
		Output: OutputCapability{FixedTemperature: true},
	}
}

func (a *OpenAICompatAdapter) qwenCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "qwen",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  65_536,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingEnableFlag,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"enable_thinking": true}
			},
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
	}
}

func (a *OpenAICompatAdapter) glmCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "glm",
		MaxContextWindow: 200_000,
		MaxOutputTokens:  128_000,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingEnableFlag,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"thinking": map[string]any{"type": "enabled"}}
			},
		},
		Caching: CachingCapability{Mode: CachingAutomatic},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
	}
}

func (a *OpenAICompatAdapter) minimaxCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "minimax",
		MaxContextWindow: 204_800,
		MaxOutputTokens:  16_384,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingSplit,
			MultiTurnPolicy: ThinkingPreserveAll,
			ExtractField:    "reasoning_details",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"reasoning_split": true}
			},
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
		Output: OutputCapability{FixedTemperature: true},
	}
}

func (a *OpenAICompatAdapter) mimoCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "mimo",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  16_384,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingEnableFlag,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
			EnableParams: func(level string) map[string]any {
				return map[string]any{"enable_thinking": true}
			},
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
	}
}

// DetectVendor guesses the vendor from a base URL.
func DetectVendor(baseURL string) string {
	lower := strings.ToLower(baseURL)
	switch {
	case strings.Contains(lower, "deepseek.com"):
		return "deepseek"
	case strings.Contains(lower, "moonshot.ai"):
		return "kimi"
	case strings.Contains(lower, "dashscope"):
		return "qwen"
	case strings.Contains(lower, "bigmodel.cn"):
		return "glm"
	case strings.Contains(lower, "minimax.io"):
		return "minimax"
	case strings.Contains(lower, "xiaomimimo.com"):
		return "mimo"
	case strings.Contains(lower, "openai.com"):
		return "openai"
	default:
		return "generic"
	}
}
