/*-------------------------------------------------------------------------
 *
 * manager.go
 *	  Manager tracks token usage and triggers compression when
 *	  thresholds are reached.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/manager.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
)

// Manager tracks token usage and triggers compression when thresholds are reached.
type Manager struct {
	maxContextTokens int
	safetyBuffer     int
	counter          TokenCounter
	compressor       *Compressor
}

// NewManager creates a context manager.
func NewManager(maxContextTokens int, counter TokenCounter) *Manager {
	if maxContextTokens <= 0 {
		maxContextTokens = 32_768 // Conservative default
	}
	return &Manager{
		maxContextTokens: maxContextTokens,
		safetyBuffer:     2000,
		counter:          counter,
		compressor:       NewCompressor(),
	}
}

// TokenUsageInfo describes current token usage.
type TokenUsageInfo struct {
	Used       int
	Limit      int
	Remaining  int
	Percentage float64
}

// TokenUsage returns current token usage statistics.
func (m *Manager) TokenUsage(messages []Message) TokenUsageInfo {
	used := m.counter.Count(messages)
	remaining := m.maxContextTokens - used - m.safetyBuffer
	if remaining < 0 {
		remaining = 0
	}
	pct := 0.0
	if m.maxContextTokens > 0 {
		pct = float64(used) / float64(m.maxContextTokens) * 100
	}
	return TokenUsageInfo{
		Used:       used,
		Limit:      m.maxContextTokens,
		Remaining:  remaining,
		Percentage: pct,
	}
}

// RemainingTokens returns the estimated remaining context capacity.
func (m *Manager) RemainingTokens(messages []Message) int {
	return m.TokenUsage(messages).Remaining
}

// ShouldBlock returns true if token usage exceeds 95%, meaning
// the API call should be blocked to prevent a 413 error.
func (m *Manager) ShouldBlock(messages []Message) bool {
	return m.TokenUsage(messages).Percentage > 95.0
}

// MessageCountTrigger is the point at which we proactively compress based on
// message count alone, independent of token count. Context windows like
// GLM-5's 128K mean token thresholds rarely trigger, but a session with 20+
// messages (each containing big tool results) degrades LLM decision quality
// long before any token limit. v1.1.09 benchmark prompt 6 timeout was
// directly caused by this: the 20-turn session from prompt 5 overwhelmed
// GLM-5 on the follow-up question.
//
// 15 keeps the last ~7 full turns (user+assistant+tool) intact, which is
// enough for continuous diagnosis while pruning old tool results.
const MessageCountTrigger = 15

// MaybeCompress checks whether compression is needed and applies it.
// Returns a new message slice and whether compression was applied.
// Does not mutate the input.
//
// Thresholds (any one triggers):
//
//	< 80% tokens AND < MessageCountTrigger msgs → no action
//	80-90% tokens                               → Turn Collapse (fold early turns into summary)
//	> 90% tokens                                → Emergency Truncate (keep first + last 3)
//	>= MessageCountTrigger msgs                 → Turn Collapse (proactive, regardless of tokens)
//
// The message-count trigger is primarily for follow-up queries in large
// context-window models where token thresholds rarely kick in but the LLM
// still struggles with long histories.
func (m *Manager) MaybeCompress(messages []Message) ([]Message, bool) {
	info := m.TokenUsage(messages)
	threshold := float64(m.maxContextTokens - m.safetyBuffer)

	// Proactive message-count trigger: protects against session resume loading
	// 15+ messages from a prior /llm run. Applies Turn Collapse to fold old
	// tool results into a summary, keeping recent turns verbatim.
	if len(messages) >= MessageCountTrigger && float64(info.Used) < threshold*0.9 {
		collapsed := m.compressor.CollapseTurns(messages)
		// Only use the compressed version if it actually shrunk the list —
		// protects against pathological Collapse producing a larger output.
		if len(collapsed) < len(messages) {
			return collapsed, true
		}
	}

	if float64(info.Used) < threshold*0.8 {
		return messages, false
	}

	// 80-90%: try Turn Collapse
	if float64(info.Used) < threshold*0.9 {
		collapsed := m.compressor.CollapseTurns(messages)
		if m.counter.Count(collapsed) < int(threshold*0.8) {
			return collapsed, true
		}
	}

	// 90%+: Emergency Truncate
	truncated := m.compressor.EmergencyTruncate(messages)
	return truncated, true
}

// ForceCompress is called after a 413 error. Tries Turn Collapse first,
// falls back to Emergency Truncate.
// Returns a new message slice.
func (m *Manager) ForceCompress(messages []Message) []Message {
	collapsed := m.compressor.CollapseTurns(messages)
	if m.counter.Count(collapsed) < m.maxContextTokens-m.safetyBuffer {
		return collapsed
	}

	return m.compressor.EmergencyTruncate(messages)
}
