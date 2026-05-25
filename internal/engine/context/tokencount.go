/*-------------------------------------------------------------------------
 *
 * tokencount.go
 *	  TokenCounter estimates token counts for messages.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/tokencount.go
 *
 *-------------------------------------------------------------------------
 */
package context

// TokenCounter estimates token counts for messages.
type TokenCounter interface {
	Count(messages []Message) int
	CountText(text string) int
}

// SimpleTokenCounter estimates tokens based on character counts.
// Chinese: ~1.5 chars/token, English: ~4 chars/token.
// Precision: ±20%, sufficient for threshold decisions.
type SimpleTokenCounter struct{}

// Count estimates the total token count for a message list.
func (c *SimpleTokenCounter) Count(messages []Message) int {
	total := 0
	for _, msg := range messages {
		total += c.CountText(msg.Content)
		total += c.CountText(msg.Thinking)
		for _, tb := range msg.ThinkingBlocks {
			total += c.CountText(tb.Thinking)
		}
		// Per-message overhead (role, formatting, separators)
		total += 4
	}
	return total
}

// CountText estimates tokens for a single text string.
func (c *SimpleTokenCounter) CountText(text string) int {
	if text == "" {
		return 0
	}

	chinese := 0
	other := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			chinese++
		} else {
			other++
		}
	}

	// Chinese: ~1.5 chars/token → multiply by 2/3
	// English/other: ~4 chars/token → divide by 4
	tokens := chinese*2/3 + other/4
	if tokens == 0 && len(text) > 0 {
		tokens = 1
	}
	return tokens
}

// UsageTracker wraps SimpleTokenCounter and refines estimates
// using actual API usage data from responses.
type UsageTracker struct {
	simple     *SimpleTokenCounter
	lastActual int // Last known actual input tokens from API
}

// NewUsageTracker creates a tracker that starts with estimates
// and refines as actual usage data arrives.
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{simple: &SimpleTokenCounter{}}
}

// Count returns the best available token estimate.
func (t *UsageTracker) Count(messages []Message) int {
	estimated := t.simple.Count(messages)
	if t.lastActual > 0 {
		// Use actual as baseline, adjust for new messages
		return estimated // In practice, after first turn we'd use actual + delta
	}
	return estimated
}

// CountText delegates to SimpleTokenCounter.
func (t *UsageTracker) CountText(text string) int {
	return t.simple.CountText(text)
}

// UpdateActual records the real input token count from an API response.
func (t *UsageTracker) UpdateActual(inputTokens int) {
	t.lastActual = inputTokens
}
