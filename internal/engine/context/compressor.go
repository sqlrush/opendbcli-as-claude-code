/*-------------------------------------------------------------------------
 *
 * compressor.go
 *	  Turn represents a group of messages forming one reasoning turn
 *	  (assistant message + tool results).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/compressor.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"fmt"
	"strings"

)

// Turn represents a group of messages forming one reasoning turn
// (assistant message + tool results).
type Turn struct {
	Messages []Message
}

// Compressor implements the 3-layer compression strategy.
type Compressor struct{}

// NewCompressor creates a Compressor.
func NewCompressor() *Compressor {
	return &Compressor{}
}

// ── Layer 1: Turn Collapse ──
// Folds early tool-calling turns into a structured summary.
// Cost: zero (pure string extraction, no API calls).
// Keeps: first turn (initial context) + last 3 turns (recent progress).

// CollapseTurns folds middle turns into a summary.
// Returns a new message slice; does not mutate the input.
func (c *Compressor) CollapseTurns(messages []Message) []Message {
	turns := SplitIntoTurns(messages)

	if len(turns) <= 4 {
		return messages // Not enough to collapse
	}

	keepFirst := turns[0]
	keepLast := turns[len(turns)-3:]
	collapsible := turns[1 : len(turns)-3]

	summary := c.summarizeTurns(collapsible)

	result := make([]Message, 0)
	result = append(result, keepFirst.Messages...)
	result = append(result, Message{
		Role: "user",
		Content: fmt.Sprintf(
			"<system-reminder>以下是之前 %d 轮诊断的摘要：\n%s\n请基于此继续分析。</system-reminder>",
			len(collapsible), summary,
		),
		IsMeta: true,
	})
	for _, turn := range keepLast {
		result = append(result, turn.Messages...)
	}

	return result
}

// summarizeTurns extracts key info from each turn without calling an LLM.
func (c *Compressor) summarizeTurns(turns []Turn) string {
	var buf strings.Builder

	for _, turn := range turns {
		for _, msg := range turn.Messages {
			if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
				names := make([]string, len(msg.ToolCalls))
				for i, tc := range msg.ToolCalls {
					names[i] = tc.Name
				}
				buf.WriteString(fmt.Sprintf("- 调用了 %s\n", strings.Join(names, ", ")))
			}

			if msg.Role == "assistant" && msg.Content != "" {
				content := msg.Content
				if len(content) > 200 {
					content = content[:200] + "..."
				}
				buf.WriteString(fmt.Sprintf("  分析: %s\n", content))
			}

			if msg.Role == "tool" {
				content := msg.Content
				if len(content) > 100 {
					content = content[:100] + "..."
				}
				buf.WriteString(fmt.Sprintf("  结果: %s\n", content))
			}
		}
	}

	return buf.String()
}

// ── Layer 2: Emergency Truncate ──
// Last resort: drops all middle messages, keeps first + last 3.
// Cost: zero.

// EmergencyTruncate keeps only the first message and the last 3.
// Returns a new message slice.
func (c *Compressor) EmergencyTruncate(messages []Message) []Message {
	if len(messages) <= 4 {
		return messages
	}

	result := make([]Message, 0, 5)
	result = append(result, messages[0])
	result = append(result, Message{
		Role:    "user",
		Content: "<system-reminder>由于上下文限制，中间的诊断历史已被截断。请基于以下最近的信息继续分析。</system-reminder>",
		IsMeta:  true,
	})
	result = append(result, messages[len(messages)-3:]...)
	return result
}

// ── Helpers ──

// SplitIntoTurns groups messages into logical turns.
// A turn starts with an assistant message and includes subsequent tool messages.
// The first turn captures all messages before the first assistant message.
func SplitIntoTurns(messages []Message) []Turn {
	if len(messages) == 0 {
		return nil
	}

	var turns []Turn
	var current Turn

	for _, msg := range messages {
		if msg.Role == "assistant" && len(current.Messages) > 0 {
			turns = append(turns, current)
			current = Turn{}
		}
		current.Messages = append(current.Messages, msg)
	}

	if len(current.Messages) > 0 {
		turns = append(turns, current)
	}

	return turns
}
