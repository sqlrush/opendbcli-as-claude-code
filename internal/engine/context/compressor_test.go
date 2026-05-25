/*-------------------------------------------------------------------------
 *
 * compressor_test.go
 *	  Test cases for compressor.go (context package):
 *	  TestSplitIntoTurns, TestSplitIntoTurnsEmpty,
 *	  TestCollapseTurnsFewTurns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/compressor_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"strings"
	"testing"


)

func makeTurnMessages(numTurns int) []Message {
	messages := []Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "diagnose this"},
	}
	for i := 0; i < numTurns; i++ {
		messages = append(messages,
			Message{
				Role:    "assistant",
				Content: strings.Repeat("analysis ", 20),
				ToolCalls: []ToolCall{
					{ID: "tc", Name: "waits"},
				},
			},
			Message{
				Role:       "tool",
				Content:    strings.Repeat("result data ", 50),
				ToolCallID: "tc",
			},
		)
	}
	return messages
}

func TestSplitIntoTurns(t *testing.T) {
	messages := makeTurnMessages(3)
	turns := SplitIntoTurns(messages)

	// First turn: system + user
	// Then 3 turns: assistant + tool each
	if len(turns) != 4 {
		t.Errorf("expected 4 turns, got %d", len(turns))
	}

	if turns[0].Messages[0].Role != "system" {
		t.Error("first turn should start with system")
	}
	if turns[1].Messages[0].Role != "assistant" {
		t.Error("second turn should start with assistant")
	}
}

func TestSplitIntoTurnsEmpty(t *testing.T) {
	turns := SplitIntoTurns(nil)
	if turns != nil {
		t.Error("expected nil for empty messages")
	}
}

func TestCollapseTurnsFewTurns(t *testing.T) {
	messages := makeTurnMessages(2) // system+user + 2 turns = 3 turns total
	comp := NewCompressor()

	result := comp.CollapseTurns(messages)

	// <= 4 turns → no collapse, return as-is
	if len(result) != len(messages) {
		t.Errorf("expected no collapse (3 turns), got %d messages (was %d)", len(result), len(messages))
	}
}

func TestCollapseTurnsManyTurns(t *testing.T) {
	messages := makeTurnMessages(8) // system+user + 8 turns = 9 turns total
	comp := NewCompressor()

	result := comp.CollapseTurns(messages)

	// Should be: first turn + summary + last 3 turns
	// first turn: 2 messages (system + user)
	// summary: 1 message (IsMeta)
	// last 3 turns: 6 messages (3 * (assistant + tool))
	// Total: 9 messages
	if len(result) != 9 {
		t.Errorf("expected 9 messages after collapse, got %d", len(result))
	}

	// First message should be system
	if result[0].Role != "system" {
		t.Errorf("first message should be system, got %q", result[0].Role)
	}

	// Second should be user (original)
	if result[1].Role != "user" && !result[1].IsMeta {
		t.Error("second message should be original user message")
	}

	// Third should be the summary (IsMeta)
	summaryIdx := 2
	if !result[summaryIdx].IsMeta {
		t.Error("summary message should have IsMeta=true")
	}
	if !strings.Contains(result[summaryIdx].Content, "轮诊断的摘要") {
		t.Error("summary should contain turn count")
	}
	if !strings.Contains(result[summaryIdx].Content, "调用了 waits") {
		t.Error("summary should mention tool names")
	}

	// Fewer messages than original
	if len(result) >= len(messages) {
		t.Errorf("collapsed (%d) should be fewer than original (%d)", len(result), len(messages))
	}
}

func TestCollapseTurnsImmutability(t *testing.T) {
	messages := makeTurnMessages(8)
	originalLen := len(messages)
	comp := NewCompressor()

	comp.CollapseTurns(messages)

	if len(messages) != originalLen {
		t.Error("original messages were mutated")
	}
}

func TestEmergencyTruncateFewMessages(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "prompt"},
		{Role: "user", Content: "question"},
	}
	comp := NewCompressor()

	result := comp.EmergencyTruncate(messages)

	if len(result) != len(messages) {
		t.Errorf("expected no truncation for %d messages", len(messages))
	}
}

func TestEmergencyTruncateManyMessages(t *testing.T) {
	messages := makeTurnMessages(10) // 22 messages total
	comp := NewCompressor()

	result := comp.EmergencyTruncate(messages)

	// Should be: first message + truncation notice + last 3
	if len(result) != 5 {
		t.Errorf("expected 5 messages after emergency truncate, got %d", len(result))
	}

	// First is original system
	if result[0].Role != "system" {
		t.Error("first should be system")
	}

	// Second is truncation notice
	if !result[1].IsMeta {
		t.Error("truncation notice should be IsMeta")
	}
	if !strings.Contains(result[1].Content, "已被截断") {
		t.Error("should contain truncation notice")
	}

	// Last 3 are from original
	if result[2].Role == "system" {
		t.Error("should not have duplicate system message")
	}
}

func TestEmergencyTruncateImmutability(t *testing.T) {
	messages := makeTurnMessages(10)
	originalLen := len(messages)
	comp := NewCompressor()

	comp.EmergencyTruncate(messages)

	if len(messages) != originalLen {
		t.Error("original messages were mutated")
	}
}
