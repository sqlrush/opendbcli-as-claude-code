/*-------------------------------------------------------------------------
 *
 * types_test.go
 *	  Test cases for types.go (engine package): TestMessageIsMeta,
 *	  TestUsageTotalInputCost, TestUsageAdd.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/types_test.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import "testing"

func TestMessageIsMeta(t *testing.T) {
	msg := Message{Role: "user", Content: "test", IsMeta: true}
	if !msg.IsMeta {
		t.Error("expected IsMeta true")
	}
}

func TestUsageTotalInputCost(t *testing.T) {
	u := Usage{InputTokens: 1000, CacheCreationTokens: 500, CacheReadTokens: 2000}
	if u.TotalInputCost() != 1700 {
		t.Errorf("expected 1700, got %d", u.TotalInputCost())
	}
}

func TestUsageAdd(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50}
	b := Usage{InputTokens: 200, OutputTokens: 80, CacheReadTokens: 300}
	sum := a.Add(b)
	if sum.InputTokens != 300 || sum.OutputTokens != 130 || sum.CacheReadTokens != 300 {
		t.Errorf("unexpected sum: %+v", sum)
	}
	if a.InputTokens != 100 {
		t.Error("original mutated")
	}
}

func TestStreamEventTypes(t *testing.T) {
	if StreamTextDelta != 0 {
		t.Error("StreamTextDelta should be 0")
	}
	if StreamDone != 4 {
		t.Errorf("StreamDone should be 4, got %d", StreamDone)
	}
}
