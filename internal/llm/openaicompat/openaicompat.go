/*-------------------------------------------------------------------------
 *
 * openaicompat.go
 *	  Provider implements llm.Provider for OpenAI-compatible APIs.
 *	  Supports: OpenAI, DeepSeek, vLLM, LiteLLM, Azure OpenAI, and any
 *	  service exposing /v1/chat/completions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/openaicompat/openaicompat.go
 *
 *-------------------------------------------------------------------------
 */
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/llm"
)

// Provider implements llm.Provider for OpenAI-compatible APIs.
// Supports: OpenAI, DeepSeek, vLLM, LiteLLM, Azure OpenAI, and any
// service exposing /v1/chat/completions.
type Provider struct {
	baseURL    string
	model      string
	apiKey     string
	stripThink bool
	httpClient *http.Client
}

// ProviderOption configures a Provider.
type ProviderOption func(*Provider)

// WithStripThink enables stripping <think>...</think> blocks from responses.
func WithStripThink(v bool) ProviderOption {
	return func(p *Provider) { p.stripThink = v }
}

// NewProvider creates an OpenAI-compatible provider.
func NewProvider(baseURL, model, apiKey string, opts ...ProviderOption) *Provider {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	p := &Provider{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "openai" }

// Chat sends a non-streaming chat request.
func (p *Provider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.Response, error) {
	body, err := p.buildRequestBody(req, false)
	if err != nil {
		return nil, fmt.Errorf("openai: build request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("openai: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var oaiResp oaiResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("openai: decode response: %w", err)
	}

	r := parseResponse(oaiResp)
	if p.stripThink {
		r.Content = stripThinkTags(r.Content)
	}
	return r, nil
}

// ChatStream sends a streaming chat request.
func (p *Provider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	body, err := p.buildRequestBody(req, true)
	if err != nil {
		return nil, fmt.Errorf("openai: build stream request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("openai: create stream request: %w", err)
	}
	p.setHeaders(httpReq)

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: stream request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("openai: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return newStream(httpResp.Body), nil
}

func (p *Provider) chatEndpoint() string {
	return p.baseURL + "/chat/completions"
}

func (p *Provider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}

// ── Request / Response types ───────────────────────────────────────────

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Stream      bool         `json:"stream"`
	Tools       []any        `json:"tools,omitempty"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

// Content is non-omitempty: DeepSeek V4 strict deserializer rejects messages
// without a `content` field (assistant-with-tool_calls or tool-result). All
// other providers (Opus / V3 / OpenAI / GLM) tolerate `"content": ""`.
type oaiMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"` // Kimi thinking mode
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
	Delta        oaiMessage `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

func (p *Provider) buildRequestBody(req llm.ChatRequest, stream bool) ([]byte, error) {
	messages := make([]oaiMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := oaiMessage{
			Role:             m.Role,
			Content:          m.Content,
			ReasoningContent: m.ReasoningContent,
			ToolCallID:       m.ToolCallID,
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

	oaiReq := oaiRequest{
		Model:       p.model,
		Messages:    messages,
		Stream:      stream,
		Tools:       req.Tools,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	return json.Marshal(oaiReq)
}

func parseResponse(resp oaiResponse) *llm.Response {
	r := &llm.Response{
		Usage: llm.Usage{
			InputTokens:  resp.Usage.PromptTokens,
			OutputTokens: resp.Usage.CompletionTokens,
		},
	}

	if len(resp.Choices) > 0 {
		choice := resp.Choices[0]
		r.Content = choice.Message.Content
		r.ReasoningContent = choice.Message.ReasoningContent

		if choice.FinishReason != nil {
			r.StopReason = *choice.FinishReason
		}
		for _, tc := range choice.Message.ToolCalls {
			r.ToolCalls = append(r.ToolCalls, llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return r
}

// stripThinkTags removes <think>...</think> blocks from model output.
// Some models (MiniMax M2.7, DeepSeek R1) embed reasoning in content.
func stripThinkTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		if start == -1 {
			break
		}
		end := strings.Index(s, "</think>")
		if end == -1 {
			// Unclosed <think> — just strip the tag itself, keep content.
			// Better to show thinking text than lose the entire output.
			s = s[:start] + s[start+len("<think>"):]
			break
		}
		s = s[:start] + s[end+len("</think>"):]
	}
	return strings.TrimSpace(s)
}
