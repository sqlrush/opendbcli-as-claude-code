/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Message represents a conversation message sent to or received from
 *	  an LLM.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/types.go
 *
 *-------------------------------------------------------------------------
 */
package provider

// ═══════════════════════════════════════════════════════
// Shared types used by Engine, Context, Tool, and Adapter.
// Placed in provider package (leaf package) to avoid circular imports.
// ═══════════════════════════════════════════════════════

// Message represents a conversation message sent to or received from an LLM.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`

	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`

	Thinking       string          `json:"thinking,omitempty"`
	ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`

	IsMeta       bool         `json:"is_meta,omitempty"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// ThinkingBlock represents a structured thinking block (Anthropic format).
type ThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

// CacheControl marks content for prompt caching.
type CacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSchema defines a tool's API description for the LLM.
type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}

// SystemPromptBlock is one block of the system prompt, with optional caching.
type SystemPromptBlock struct {
	Text         string        `json:"text"`
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

// Request holds all parameters for an LLM API call.
type Request struct {
	Messages     []Message
	Tools        []ToolSchema
	SystemPrompt []SystemPromptBlock
	MaxTokens    int
	Temperature  *float64
	Stream       bool
	Extra        map[string]any
}

// Response holds the parsed result of an LLM API call.
type Response struct {
	Content        string
	Thinking       string
	ThinkingBlocks []ThinkingBlock
	ToolCalls      []ToolCall
	StopReason     string
	Usage          Usage
	CacheStats     CacheStats
	Truncated      bool
	RawHeaders     map[string]string
}

// Usage tracks token consumption across all providers.
type Usage struct {
	InputTokens         int
	OutputTokens        int
	ThinkingTokens      int
	CacheCreationTokens int
	CacheReadTokens     int
	CacheMissTokens     int
}

// TotalInputCost returns the effective input token cost.
func (u Usage) TotalInputCost() int {
	return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens/10
}

// Add returns a new Usage with all fields summed. Does not mutate the receiver.
func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens + other.InputTokens,
		OutputTokens:        u.OutputTokens + other.OutputTokens,
		ThinkingTokens:      u.ThinkingTokens + other.ThinkingTokens,
		CacheCreationTokens: u.CacheCreationTokens + other.CacheCreationTokens,
		CacheReadTokens:     u.CacheReadTokens + other.CacheReadTokens,
		CacheMissTokens:     u.CacheMissTokens + other.CacheMissTokens,
	}
}

// CacheStats summarizes prompt cache performance.
type CacheStats struct {
	Hit        bool
	HitTokens  int
	MissTokens int
}

// HitRate returns the fraction of prompt tokens served from cache, in [0,1].
// Returns 0 when the provider did not report cache stats.
func (c CacheStats) HitRate() float64 {
	total := c.HitTokens + c.MissTokens
	if total == 0 {
		return 0
	}
	return float64(c.HitTokens) / float64(total)
}

// HitRate returns the fraction of prompt tokens served from cache, in [0,1].
// Returns 0 when no cache tokens are recorded.
//
// OpenAI/GLM/DeepSeek style: InputTokens already includes cached tokens,
// and CacheMissTokens is the uncached portion → Read / (Read+Miss).
//
// Anthropic style: InputTokens, CacheReadTokens, and CacheCreationTokens
// are disjoint buckets → Read / (Input+Read+Create).
func (u Usage) HitRate() float64 {
	if u.CacheMissTokens > 0 {
		total := u.CacheReadTokens + u.CacheMissTokens
		return float64(u.CacheReadTokens) / float64(total)
	}
	if u.CacheReadTokens == 0 {
		return 0
	}
	denom := u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
	if denom == 0 {
		return 0
	}
	return float64(u.CacheReadTokens) / float64(denom)
}

// StreamEventType classifies streaming events from the LLM.
type StreamEventType uint8

const (
	StreamTextDelta     StreamEventType = iota
	StreamThinkingDelta
	StreamToolCallDelta
	StreamToolResult
	StreamDone
	StreamError
)

// StreamEvent is a single event from an LLM streaming response.
type StreamEvent struct {
	Type         StreamEventType
	Content      string
	ToolCall     *ToolCall
	Error        error
	FinishReason string // "stop", "length", "max_tokens", "tool_calls"
}

// Stream provides incremental access to an LLM streaming response.
type Stream interface {
	Next() (StreamEvent, error)
	Close() error
}
