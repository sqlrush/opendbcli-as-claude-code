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

// v1.1.48: MessageCountTrigger no longer triggers compression. Test now
// verifies that long sessions are NOT auto-compressed (only token-based
// thresholds trigger). Reason: proactive count-based collapse destroyed
// new user messages by folding them into summary turns, causing LLM to
// keep answering OLD turns[0] task. Same philosophy as v1.1.47 drift
// removal — heuristic context management hurts more than it helps.
func TestMaybeCompress_MessageCountTrigger_DoesNotFire(t *testing.T) {
	m := NewManager(100000, zeroCounter{})

	// 20 messages (10 turns) WAS triggering CollapseTurns pre-v1.1.48.
	// Now with zero token count, MaybeCompress should be a no-op even at
	// 20+ messages. Users wanting clean context use /clear (v1.1.47).
	long := buildConversation(10) // 20 messages
	if len(long) < MessageCountTrigger {
		t.Fatalf("test setup wrong: long has %d messages, want >= %d", len(long), MessageCountTrigger)
	}
	result, compressed := m.MaybeCompress(long)
	if compressed {
		t.Errorf("v1.1.48: %d messages should NOT trigger compression (count-based trigger removed)", len(long))
	}
	if len(result) != len(long) {
		t.Errorf("messages should be unchanged when no compression fires: got %d, want %d", len(result), len(long))
	}
}
