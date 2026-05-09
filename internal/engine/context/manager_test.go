/*-------------------------------------------------------------------------
 *
 * manager_test.go
 *	  Test cases for manager.go (context package): TestTokenUsage,
 *	  TestRemainingTokens, TestShouldBlockUnder95.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/manager_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"strings"
	"testing"


)

func messagesOfSize(tokenTarget int) []Message {
	// Each char ≈ 0.25 tokens (English), so 4*tokenTarget chars ≈ tokenTarget tokens
	content := strings.Repeat("x", tokenTarget*4)
	return []Message{
		{Role: "system", Content: content[:len(content)/3]},
		{Role: "user", Content: content[len(content)/3:]},
	}
}

func TestTokenUsage(t *testing.T) {
	m := NewManager(32000, &SimpleTokenCounter{})
	messages := messagesOfSize(10000)

	info := m.TokenUsage(messages)
	if info.Limit != 32000 {
		t.Errorf("expected limit 32000, got %d", info.Limit)
	}
	if info.Used < 8000 || info.Used > 12000 {
		t.Errorf("expected ~10000 used, got %d", info.Used)
	}
	if info.Remaining <= 0 {
		t.Error("expected positive remaining")
	}
	if info.Percentage < 25 || info.Percentage > 40 {
		t.Errorf("expected ~31%%, got %.1f%%", info.Percentage)
	}
}

func TestRemainingTokens(t *testing.T) {
	m := NewManager(32000, &SimpleTokenCounter{})
	messages := []Message{
		{Role: "user", Content: "short"},
	}
	remaining := m.RemainingTokens(messages)
	// 32000 - ~5 tokens - 2000 buffer ≈ 29995
	if remaining < 28000 || remaining > 31000 {
		t.Errorf("expected ~30000 remaining, got %d", remaining)
	}
}

func TestShouldBlockUnder95(t *testing.T) {
	m := NewManager(32000, &SimpleTokenCounter{})
	messages := messagesOfSize(10000) // ~31% usage
	if m.ShouldBlock(messages) {
		t.Error("should not block at 31% usage")
	}
}

func TestShouldBlockOver95(t *testing.T) {
	m := NewManager(1000, &SimpleTokenCounter{})
	messages := messagesOfSize(960) // ~96% of 1000
	if !m.ShouldBlock(messages) {
		t.Error("should block at >95% usage")
	}
}

func TestMaybeCompressNoAction(t *testing.T) {
	m := NewManager(100000, &SimpleTokenCounter{})
	messages := messagesOfSize(10000) // ~10% usage

	result, compressed := m.MaybeCompress(messages)
	if compressed {
		t.Error("should not compress at 10% usage")
	}
	if len(result) != len(messages) {
		t.Error("messages should be unchanged")
	}
}

func TestMaybeCompressTriggersCollapse(t *testing.T) {
	// Create a small context window where many turns will trigger 80% threshold
	m := NewManager(3000, &SimpleTokenCounter{})

	// Build messages that fill ~85% of 3000 tokens
	messages := []Message{
		{Role: "system", Content: strings.Repeat("s", 400)},
		{Role: "user", Content: strings.Repeat("u", 400)},
	}
	// Add several turns to create compressible content
	for i := 0; i < 6; i++ {
		messages = append(messages,
			Message{Role: "assistant", Content: strings.Repeat("a", 200),
				ToolCalls: []ToolCall{{ID: "tc", Name: "waits"}}},
			Message{Role: "tool", Content: strings.Repeat("r", 200), ToolCallID: "tc"},
		)
	}

	result, compressed := m.MaybeCompress(messages)
	if !compressed {
		info := m.TokenUsage(messages)
		t.Errorf("expected compression at %.1f%% usage (%d/%d)", info.Percentage, info.Used, info.Limit)
		return
	}
	if len(result) >= len(messages) {
		t.Errorf("compressed (%d msgs) should be fewer than original (%d msgs)", len(result), len(messages))
	}
}

func TestMaybeCompressEmergency(t *testing.T) {
	// Very small context → forces emergency truncate
	m := NewManager(500, &SimpleTokenCounter{})

	messages := makeTurnMessages(5) // Many messages in tiny window

	result, compressed := m.MaybeCompress(messages)
	if !compressed {
		t.Error("expected compression")
	}
	// Emergency truncate: first + notice + last 3 = 5
	if len(result) > 6 {
		t.Errorf("expected ≤6 messages after emergency, got %d", len(result))
	}
}

func TestForceCompress(t *testing.T) {
	m := NewManager(2000, &SimpleTokenCounter{})
	messages := makeTurnMessages(8)

	result := m.ForceCompress(messages)
	if len(result) >= len(messages) {
		t.Errorf("force compress should reduce messages (was %d, got %d)", len(messages), len(result))
	}
}

func TestForceCompressImmutability(t *testing.T) {
	m := NewManager(2000, &SimpleTokenCounter{})
	messages := makeTurnMessages(8)
	originalLen := len(messages)

	m.ForceCompress(messages)

	if len(messages) != originalLen {
		t.Error("original messages were mutated")
	}
}
