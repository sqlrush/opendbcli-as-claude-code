/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Shared type definitions for the llm package: ChatRequest, Message,
 *	  Response, StreamEvent, ....
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/llm/types.go
 *
 *-------------------------------------------------------------------------
 */
package llm

type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"` // Kimi thinking mode
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ChatRequest struct {
	Messages    []Message
	Tools       []any
	ToolChoice  any
	MaxTokens   int
	Temperature *float64
}

type Response struct {
	Content          string
	ReasoningContent string // Kimi thinking mode reasoning
	Thinking         string // extracted <think>...</think> reasoning chain
	ToolCalls        []ToolCall
	Usage            Usage
	StopReason       string
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type StreamEventType uint8

const (
	StreamTextDelta StreamEventType = iota
	StreamToolCallDelta
	StreamDone
)

type StreamEvent struct {
	Type             StreamEventType
	Content          string
	ReasoningContent string // Kimi/MiMo/DeepSeek thinking tokens
	ToolCall         *ToolCall
	FinishReason     string // "stop", "length", "max_tokens", "tool_calls"
}
