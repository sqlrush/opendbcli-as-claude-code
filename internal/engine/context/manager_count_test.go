/*-------------------------------------------------------------------------
 *
 * manager_count_test.go
 *	  Test cases for manager_count.go (context package):
 *	  TestMaybeCompress_MessageCountTrigger.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/manager_count_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import "testing"

// Fake counter that returns 0 so only message-count trigger can fire.
type zeroCounter struct{}

func (zeroCounter) Count(_ []Message) int  { return 0 }
func (zeroCounter) CountText(_ string) int { return 0 }

// buildConversation creates n user/assistant pairs (so 2n messages, n turns).
func buildConversation(n int) []Message {
	msgs := make([]Message, 0, 2*n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, Message{Role: "user", Content: "q"})
		msgs = append(msgs, Message{Role: "assistant", Content: "a"})
	}
	return msgs
}

func TestMaybeCompress_MessageCountTrigger(t *testing.T) {
	m := NewManager(100000, zeroCounter{})

	// 14 messages (7 turns) → over trigger but CollapseTurns can actually fold
	// We want to verify BELOW-trigger behavior, so use 10 messages (5 turns).
	short := buildConversation(5) // 10 messages, 5 turns
	if len(short) >= MessageCountTrigger {
		t.Fatalf("test setup wrong: short has %d messages, want < %d", len(short), MessageCountTrigger)
	}
	_, compressed := m.MaybeCompress(short)
	if compressed {
		t.Errorf("%d messages should not trigger compression", len(short))
	}

	// 20 messages (10 turns) → over trigger, and enough turns (>4) for CollapseTurns
	long := buildConversation(10) // 20 messages, 10 turns
	if len(long) < MessageCountTrigger {
		t.Fatalf("test setup wrong: long has %d messages, want >= %d", len(long), MessageCountTrigger)
	}
	result, compressed := m.MaybeCompress(long)
	if !compressed {
		t.Errorf("%d messages should trigger proactive compression", len(long))
	}
	if len(result) >= len(long) {
		t.Errorf("compressed result should be shorter: got %d, want < %d", len(result), len(long))
	}
}
