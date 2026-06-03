/*-------------------------------------------------------------------------
 *
 * ollama.go
 *	  OllamaProvider implements llm.Provider using Ollama's
 *	  OpenAI-compatible API.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/ollama/ollama.go
 *
 *-------------------------------------------------------------------------
 */
package ollama

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

const (
	defaultBaseURL = "http://localhost:11434"
	defaultModel   = "ailinkdb"
)

// OllamaProvider implements llm.Provider using Ollama's OpenAI-compatible API.
type OllamaProvider struct {
	baseURL    string
	model      string
	httpClient *http.Client
}

// NewOllamaProvider creates a new OllamaProvider with the given base URL and model.
// Empty baseURL defaults to "http://localhost:11434".
// Empty model defaults to "ailinkdb".
func NewOllamaProvider(baseURL string, model string) *OllamaProvider {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if model == "" {
		model = defaultModel
	}
	return &OllamaProvider{
		baseURL: baseURL,
		model:   model,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // 27B models may take 30-60s per round
		},
	}
}

// Name returns the provider name.
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// openAIRequest is the request body for the OpenAI-compatible chat endpoint.
type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Tools       []any           `json:"tools,omitempty"`
	ToolChoice  any             `json:"tool_choice,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
}

type openAIChoice struct {
	Message      openAIMessage `json:"message"`
	Delta        openAIMessage `json:"delta"`
	FinishReason *string       `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// Chat sends a non-streaming chat request and returns the complete response.
func (p *OllamaProvider) Chat(ctx context.Context, req llm.ChatRequest) (*llm.Response, error) {
	body, err := p.buildRequestBody(req, false)
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to build request body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("ollama: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	var oaiResp openAIResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&oaiResp); err != nil {
		return nil, fmt.Errorf("ollama: failed to decode response: %w", err)
	}

	return parseResponse(oaiResp), nil
}

// ChatStream sends a streaming chat request and returns a Stream for reading events.
func (p *OllamaProvider) ChatStream(ctx context.Context, req llm.ChatRequest) (llm.Stream, error) {
	body, err := p.buildRequestBody(req, true)
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to build request body: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama: stream request failed: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		defer httpResp.Body.Close()
		respBody, _ := io.ReadAll(httpResp.Body)
		return nil, fmt.Errorf("ollama: HTTP %d: %s", httpResp.StatusCode, string(respBody))
	}

	return newOllamaStream(httpResp.Body), nil
}

func (p *OllamaProvider) chatEndpoint() string {
	return p.baseURL + "/v1/chat/completions"
}

func (p *OllamaProvider) buildRequestBody(req llm.ChatRequest, stream bool) ([]byte, error) {
	messages := make([]openAIMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		oaiMsg := openAIMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			oaiMsg.ToolCalls = append(oaiMsg.ToolCalls, openAIToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: openAIToolFunction{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		messages = append(messages, oaiMsg)
	}

	oaiReq := openAIRequest{
		Model:       p.model,
		Messages:    messages,
		Stream:      stream,
		Tools:       req.Tools,
		ToolChoice:  req.ToolChoice,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
	}

	return json.Marshal(oaiReq)
}

func parseResponse(oaiResp openAIResponse) *llm.Response {
	resp := &llm.Response{
		Usage: llm.Usage{
			InputTokens:  oaiResp.Usage.PromptTokens,
			OutputTokens: oaiResp.Usage.CompletionTokens,
		},
	}

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
		content := choice.Message.Content

		// Extract <think>...</think> reasoning block from Qwen3.5 models.
		resp.Thinking, resp.Content = extractThinking(content)

		if choice.FinishReason != nil {
			resp.StopReason = *choice.FinishReason
		}
		for _, tc := range choice.Message.ToolCalls {
			resp.ToolCalls = append(resp.ToolCalls, llm.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			})
		}
	}

	return resp
}

// extractThinking separates <think>...</think> blocks from the response content.
// Returns (thinking, cleanContent). If no think block is found, thinking is empty.
func extractThinking(content string) (thinking, clean string) {
	// Qwen3.5 format: reasoning text followed by </think>\n\nactual answer
	// The model template starts with <think>, so content may begin with reasoning
	// and end with </think> before the actual answer.
	if idx := strings.Index(content, "</think>"); idx >= 0 {
		thinking = strings.TrimSpace(content[:idx])
		// Remove leading <think> tag if present.
		thinking = strings.TrimPrefix(thinking, "<think>")
		thinking = strings.TrimSpace(thinking)
		clean = strings.TrimSpace(content[idx+len("</think>"):])
		return thinking, clean
	}
	return "", content
}
