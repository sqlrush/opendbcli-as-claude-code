/*-------------------------------------------------------------------------
 *
 * main.go
 *	  opus-forwarder is a lightweight HTTP proxy that translates
 *	  OpenAI-compatible chat completion requests into Claude CLI calls
 *	  and translates the responses back. This allows opendb (which
 *	  speaks OpenAI format) to use Claude Opus 4.6 as its LLM backend
 *	  via the Claude Code subscription (no API key needed).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  cmd/opus-forwarder/main.go
 *
 *-------------------------------------------------------------------------
 */
// opus-forwarder is a lightweight HTTP proxy that translates OpenAI-compatible
// chat completion requests into Claude CLI calls and translates the responses
// back. This allows opendb (which speaks OpenAI format) to use Claude Opus 4.6
// as its LLM backend via the Claude Code subscription (no API key needed).
//
// Supports function calling: tool definitions are injected into the system prompt,
// and tool call JSON in Claude's output is parsed back to OpenAI format.
//
// Usage:
//
//	opus-forwarder [-port 11434]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ============================================================================
// OpenAI-compatible types (what opendb sends/expects)
// ============================================================================

type oaiRequest struct {
	Model       string       `json:"model"`
	Messages    []oaiMessage `json:"messages"`
	Tools       []oaiTool    `json:"tools,omitempty"`
	Stream      bool         `json:"stream"`
	MaxTokens   int          `json:"max_tokens,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolCallFunc `json:"function"`
}

type oaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiResponse struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Created int64       `json:"created"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
	Usage   oaiUsage    `json:"usage"`
}

type oaiChoice struct {
	Index        int        `json:"index"`
	Message      oaiMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

type oaiUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ============================================================================
// Claude CLI response type
// ============================================================================

type claudeCLIResponse struct {
	Type        string  `json:"type"`
	Subtype     string  `json:"subtype"`
	Result      string  `json:"result"`
	CostUSD     float64 `json:"cost_usd"`
	DurationMS  int64   `json:"duration_ms"`
	DurationAPI int64   `json:"duration_api_ms"`
	NumTurns    int     `json:"num_turns"`
	IsError     bool    `json:"is_error"`
	SessionID   string  `json:"session_id"`
}

// ============================================================================
// Forwarder
// ============================================================================

type forwarder struct {
	model     string
	claudeBin string
	callID    int // monotonic tool call ID counter
}

func newForwarder() *forwarder {
	bin := os.Getenv("CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	return &forwarder{
		model:     "claude-opus-4-6",
		claudeBin: bin,
	}
}

// handleChat handles POST /v1/chat/completions
func (f *forwarder) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var oaiReq oaiRequest
	if err := json.Unmarshal(body, &oaiReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	hasTools := len(oaiReq.Tools) > 0
	log.Printf("← opendb: model=%s messages=%d tools=%d", oaiReq.Model, len(oaiReq.Messages), len(oaiReq.Tools))

	// Build tool instruction text for system prompt injection.
	var toolInstruction string
	if hasTools {
		toolInstruction = buildToolInstruction(oaiReq.Tools)
	}

	// Call Claude CLI.
	result, err := f.callClaude(r.Context(), oaiReq.Messages, toolInstruction)
	if err != nil {
		log.Printf("✗ Claude CLI error: %v", err)
		writeError(w, http.StatusBadGateway, "claude call failed: "+err.Error())
		return
	}

	// Parse tool calls from response if tools were provided.
	oaiResp := oaiResponse{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   f.model,
	}

	if hasTools {
		if tc, remaining := f.parseToolCall(result); tc != nil {
			oaiResp.Choices = []oaiChoice{{
				Index: 0,
				Message: oaiMessage{
					Role:      "assistant",
					Content:   strings.TrimSpace(remaining),
					ToolCalls: []oaiToolCall{*tc},
				},
				FinishReason: "tool_calls",
			}}
			log.Printf("→ opendb: tool_call=%s content_len=%d", tc.Function.Name, len(remaining))
		} else {
			oaiResp.Choices = []oaiChoice{{
				Index:        0,
				Message:      oaiMessage{Role: "assistant", Content: result},
				FinishReason: "stop",
			}}
			log.Printf("→ opendb: content_len=%d (no tool call)", len(result))
		}
	} else {
		oaiResp.Choices = []oaiChoice{{
			Index:        0,
			Message:      oaiMessage{Role: "assistant", Content: result},
			FinishReason: "stop",
		}}
		log.Printf("→ opendb: content_len=%d", len(result))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(oaiResp)
}

// buildToolInstruction converts OpenAI tool definitions into a text instruction
// block that gets appended to the system prompt.
func buildToolInstruction(tools []oaiTool) string {
	var b strings.Builder
	b.WriteString("\n\n## 可用工具\n\n")
	b.WriteString("你可以调用以下工具来查询数据库。要调用工具时，输出如下格式的 JSON（独占一行，不要包含其他文字）：\n")
	b.WriteString("```\n{\"tool_call\": {\"name\": \"工具名\", \"arguments\": {参数}}}\n```\n\n")
	b.WriteString("每次只调用一个工具，等待结果后再继续分析。\n\n")
	b.WriteString("工具列表：\n")
	for _, t := range tools {
		b.WriteString(fmt.Sprintf("- **%s**: %s\n", t.Function.Name, t.Function.Description))
		if t.Function.Parameters != nil {
			if params, err := json.Marshal(t.Function.Parameters); err == nil && string(params) != "{}" && string(params) != `{"type":"object","properties":{}}` {
				b.WriteString(fmt.Sprintf("  参数: %s\n", string(params)))
			}
		}
	}
	b.WriteString("\n如果不需要调用工具，直接输出分析结果即可。\n")
	return b.String()
}

// stripCodeFences removes markdown code fences (```json ... ```) from text.
var codeFenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*\\n?(.*?)\\n?\\s*```")

// parseToolCall extracts a tool call from Claude's text response.
// Returns the parsed tool call and remaining text, or nil if no tool call found.
func (f *forwarder) parseToolCall(text string) (*oaiToolCall, string) {
	// Strip code fences if present.
	cleaned := codeFenceRe.ReplaceAllString(text, "$1")
	cleaned = strings.TrimSpace(cleaned)

	// Try to parse the entire cleaned text as tool_call JSON.
	tc := f.tryParseToolCall(cleaned)
	if tc != nil {
		return tc, ""
	}

	// Try to find {"tool_call": ...} anywhere in the text.
	idx := strings.Index(cleaned, `"tool_call"`)
	if idx < 0 {
		return nil, text
	}
	// Walk back to find the opening {.
	start := strings.LastIndex(cleaned[:idx], "{")
	if start < 0 {
		return nil, text
	}
	// Find matching closing brace using bracket counting.
	depth := 0
	end := -1
	for i := start; i < len(cleaned); i++ {
		switch cleaned[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
		if end > 0 {
			break
		}
	}
	if end <= start {
		return nil, text
	}

	candidate := cleaned[start:end]
	tc = f.tryParseToolCall(candidate)
	if tc != nil {
		remaining := cleaned[:start] + cleaned[end:]
		return tc, strings.TrimSpace(remaining)
	}
	return nil, text
}

func (f *forwarder) tryParseToolCall(s string) *oaiToolCall {
	var wrapper struct {
		ToolCall struct {
			Name      string `json:"name"`
			Arguments any    `json:"arguments"`
		} `json:"tool_call"`
	}
	if err := json.Unmarshal([]byte(s), &wrapper); err != nil {
		return nil
	}
	if wrapper.ToolCall.Name == "" {
		return nil
	}

	argsJSON := "{}"
	if wrapper.ToolCall.Arguments != nil {
		if data, err := json.Marshal(wrapper.ToolCall.Arguments); err == nil {
			argsJSON = string(data)
		}
	}

	f.callID++
	return &oaiToolCall{
		ID:   fmt.Sprintf("call_%d", f.callID),
		Type: "function",
		Function: oaiToolCallFunc{
			Name:      wrapper.ToolCall.Name,
			Arguments: argsJSON,
		},
	}
}

// callClaude invokes the claude CLI in print mode and returns the response text.
func (f *forwarder) callClaude(ctx context.Context, messages []oaiMessage, toolInstruction string) (string, error) {
	// Separate system prompt from conversation messages.
	var systemPrompt string
	var convMsgs []oaiMessage
	for _, msg := range messages {
		if msg.Role == "system" {
			systemPrompt = msg.Content
		} else {
			convMsgs = append(convMsgs, msg)
		}
	}

	// Append tool instructions to system prompt.
	if toolInstruction != "" {
		systemPrompt += toolInstruction
	}

	// Format conversation as prompt text.
	var prompt strings.Builder
	if len(convMsgs) == 1 && convMsgs[0].Role == "user" {
		// Single turn — pass user message directly.
		prompt.WriteString(convMsgs[0].Content)
	} else {
		// Multi-turn — format as labeled conversation history.
		for i, msg := range convMsgs {
			if i > 0 {
				prompt.WriteString("\n\n")
			}
			switch msg.Role {
			case "user":
				prompt.WriteString("[User]\n")
				prompt.WriteString(msg.Content)
			case "assistant":
				// Include tool calls in assistant message.
				if len(msg.ToolCalls) > 0 {
					prompt.WriteString("[Assistant]\n")
					for _, tc := range msg.ToolCalls {
						prompt.WriteString(fmt.Sprintf(`{"tool_call": {"name": "%s", "arguments": %s}}`, tc.Function.Name, tc.Function.Arguments))
					}
					if msg.Content != "" {
						prompt.WriteString("\n" + msg.Content)
					}
				} else {
					prompt.WriteString("[Assistant]\n")
					prompt.WriteString(msg.Content)
				}
			case "tool":
				// Tool result — format as labeled result.
				prompt.WriteString(fmt.Sprintf("[Tool Result: %s]\n", msg.ToolCallID))
				prompt.WriteString(msg.Content)
			}
		}
		prompt.WriteString("\n\n请作为 Assistant 继续回复。")
	}

	// Build claude CLI args.
	// --tools "" disables all Claude Code tools (Read, Write, Bash, etc.)
	// so Claude acts as a pure text completion model.
	args := []string{
		"-p",
		"--output-format", "json",
		"--tools", "",
	}
	if systemPrompt != "" {
		args = append(args, "--system-prompt", systemPrompt)
	}

	cmd := exec.CommandContext(ctx, f.claudeBin, args...)
	cmd.Stdin = strings.NewReader(prompt.String())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	startTime := time.Now()
	log.Printf("→ claude: prompt_len=%d system_len=%d tools=%v", prompt.Len(), len(systemPrompt), toolInstruction != "")

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI failed (%.1fs): %w\nstderr: %s",
			time.Since(startTime).Seconds(), err, stderr.String())
	}

	elapsed := time.Since(startTime)

	// Parse JSON response.
	var cliResp claudeCLIResponse
	if err := json.Unmarshal(stdout.Bytes(), &cliResp); err != nil {
		// Not JSON — return raw text.
		log.Printf("← claude: raw text (%.1fs) len=%d", elapsed.Seconds(), stdout.Len())
		return strings.TrimSpace(stdout.String()), nil
	}

	if cliResp.IsError {
		return "", fmt.Errorf("claude error: %s", cliResp.Result)
	}

	log.Printf("← claude: result_len=%d cost=$%.4f elapsed=%.1fs turns=%d",
		len(cliResp.Result), cliResp.CostUSD, elapsed.Seconds(), cliResp.NumTurns)

	if strings.TrimSpace(cliResp.Result) == "" {
		return "", fmt.Errorf("claude returned empty result (turns=%d, %.1fs)", cliResp.NumTurns, elapsed.Seconds())
	}

	return cliResp.Result, nil
}

// handleTags handles GET /api/tags (Ollama model listing endpoint).
// opendb may call this to verify the provider is alive.
func (f *forwarder) handleTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"models": []map[string]any{
			{
				"name":  "opus-4.6",
				"model": "opus-4.6",
				"details": map[string]any{
					"family":         "claude",
					"parameter_size": "unknown",
				},
			},
		},
	})
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "api_error",
		},
	})
}

func main() {
	port := flag.Int("port", 11434, "Listen port")
	flag.Parse()

	fwd := newForwarder()

	// Verify claude CLI is available.
	if _, err := exec.LookPath(fwd.claudeBin); err != nil {
		log.Fatalf("claude CLI not found: %v (set CLAUDE_BIN if installed elsewhere)", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", fwd.handleChat)
	mux.HandleFunc("/api/tags", fwd.handleTags)

	// Health check.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			fmt.Fprintf(w, "opus-forwarder OK (backend: claude CLI, model: %s, tools: enabled)\n", fwd.model)
			return
		}
		http.NotFound(w, r)
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", *port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute, // Long for Opus inference
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("opus-forwarder listening on :%d (backend: claude CLI, tools: text-simulation)", *port)
	log.Printf("opendb config: base_url=http://localhost:%d model=opus-4.6", *port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
