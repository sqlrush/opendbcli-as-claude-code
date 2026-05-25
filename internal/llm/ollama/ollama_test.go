/*-------------------------------------------------------------------------
 *
 * ollama_test.go
 *	  Test cases for ollama.go (ollama package):
 *	  TestOllamaProvider_Name, TestNewOllamaProvider_Defaults,
 *	  TestNewOllamaProvider_CustomValues.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/ollama/ollama_test.go
 *
 *-------------------------------------------------------------------------
 */
package ollama

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/llm"
)

// Compile-time interface check.
var _ llm.Provider = (*OllamaProvider)(nil)

func TestOllamaProvider_Name(t *testing.T) {
	p := NewOllamaProvider("", "")
	if got := p.Name(); got != "ollama" {
		t.Errorf("Name() = %q, want %q", got, "ollama")
	}
}

func TestNewOllamaProvider_Defaults(t *testing.T) {
	p := NewOllamaProvider("", "")
	if p.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", p.baseURL, defaultBaseURL)
	}
	if p.model != defaultModel {
		t.Errorf("model = %q, want %q", p.model, defaultModel)
	}
}

func TestNewOllamaProvider_CustomValues(t *testing.T) {
	p := NewOllamaProvider("http://custom:1234", "mymodel")
	if p.baseURL != "http://custom:1234" {
		t.Errorf("baseURL = %q, want %q", p.baseURL, "http://custom:1234")
	}
	if p.model != "mymodel" {
		t.Errorf("model = %q, want %q", p.model, "mymodel")
	}
}

func TestOllamaProvider_Chat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "Hello, world!"
				},
				"finish_reason": "stop"
			}],
			"usage": {
				"prompt_tokens": 10,
				"completion_tokens": 5
			}
		}`)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	if resp.Content != "Hello, world!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello, world!")
	}
	if resp.StopReason != "stop" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "stop")
	}
	if resp.Usage.InputTokens != 10 {
		t.Errorf("InputTokens = %d, want 10", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 5 {
		t.Errorf("OutputTokens = %d, want 5", resp.Usage.OutputTokens)
	}
}

func TestOllamaProvider_Chat_ToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"choices": [{
				"message": {
					"role": "assistant",
					"content": "",
					"tool_calls": [{
						"id": "call_abc123",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"city\": \"London\"}"
						}
					}]
				},
				"finish_reason": "tool_calls"
			}],
			"usage": {
				"prompt_tokens": 15,
				"completion_tokens": 20
			}
		}`)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	resp, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "What is the weather in London?"},
		},
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}

	if len(resp.ToolCalls) != 1 {
		t.Fatalf("ToolCalls length = %d, want 1", len(resp.ToolCalls))
	}

	tc := resp.ToolCalls[0]
	if tc.ID != "call_abc123" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call_abc123")
	}
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "get_weather")
	}
	if tc.Arguments != `{"city": "London"}` {
		t.Errorf("ToolCall.Arguments = %q, want %q", tc.Arguments, `{"city": "London"}`)
	}
	if resp.StopReason != "tool_calls" {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, "tool_calls")
	}
}

func TestOllamaProvider_Chat_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error": "internal server error"}`)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	_, err := p.Chat(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code 500: %v", err)
	}
}

func TestOllamaProvider_Chat_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block longer than the context deadline.
		time.Sleep(2 * time.Second)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"late"}}]}`)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	p := NewOllamaProvider(server.URL, "test-model")
	_, err := p.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if ctx.Err() == nil {
		t.Error("expected context to be done")
	}
}

func TestOllamaProvider_ChatStream(t *testing.T) {
	ssePayload := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
		"",
		`data: {"choices":[{"delta":{"content":" world"}}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ssePayload)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	stream, err := p.ChatStream(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	defer stream.Close()

	// First event: text delta "Hello"
	ev1, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() #1 error: %v", err)
	}
	if ev1.Type != llm.StreamTextDelta {
		t.Errorf("event #1 type = %v, want StreamTextDelta", ev1.Type)
	}
	if ev1.Content != "Hello" {
		t.Errorf("event #1 content = %q, want %q", ev1.Content, "Hello")
	}

	// Second event: text delta " world"
	ev2, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() #2 error: %v", err)
	}
	if ev2.Type != llm.StreamTextDelta {
		t.Errorf("event #2 type = %v, want StreamTextDelta", ev2.Type)
	}
	if ev2.Content != " world" {
		t.Errorf("event #2 content = %q, want %q", ev2.Content, " world")
	}

	// Third event: finish_reason triggers StreamDone + EOF
	ev3, err := stream.Next()
	if err != io.EOF {
		t.Fatalf("Next() #3 error = %v, want io.EOF", err)
	}
	if ev3.Type != llm.StreamDone {
		t.Errorf("event #3 type = %v, want StreamDone", ev3.Type)
	}
}

func TestOllamaProvider_ChatStream_ToolCallDelta(t *testing.T) {
	ssePayload := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"","type":"function","function":{"name":"","arguments":"\"hello\"}"}}]}}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ssePayload)
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	stream, err := p.ChatStream(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "search for hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatStream() error: %v", err)
	}
	defer stream.Close()

	// First event: tool call delta with ID and partial arguments.
	ev1, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() #1 error: %v", err)
	}
	if ev1.Type != llm.StreamToolCallDelta {
		t.Errorf("event #1 type = %v, want StreamToolCallDelta", ev1.Type)
	}
	if ev1.ToolCall == nil {
		t.Fatal("event #1 ToolCall is nil")
	}
	if ev1.ToolCall.ID != "call_1" {
		t.Errorf("event #1 ToolCall.ID = %q, want %q", ev1.ToolCall.ID, "call_1")
	}
	if ev1.ToolCall.Name != "search" {
		t.Errorf("event #1 ToolCall.Name = %q, want %q", ev1.ToolCall.Name, "search")
	}

	// Second event: continuation of tool call arguments.
	ev2, err := stream.Next()
	if err != nil {
		t.Fatalf("Next() #2 error: %v", err)
	}
	if ev2.Type != llm.StreamToolCallDelta {
		t.Errorf("event #2 type = %v, want StreamToolCallDelta", ev2.Type)
	}

	// Third event: DONE.
	ev3, err := stream.Next()
	if err != io.EOF {
		t.Fatalf("Next() #3 error = %v, want io.EOF", err)
	}
	if ev3.Type != llm.StreamDone {
		t.Errorf("event #3 type = %v, want StreamDone", ev3.Type)
	}
}

func TestOllamaProvider_ChatStream_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "bad gateway")
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "test-model")
	_, err := p.ChatStream(context.Background(), llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "user", Content: "Hi"},
		},
	})
	if err == nil {
		t.Fatal("expected error for HTTP 502, got nil")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should contain status code 502: %v", err)
	}
}
