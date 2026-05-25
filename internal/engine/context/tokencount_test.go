/*-------------------------------------------------------------------------
 *
 * tokencount_test.go
 *	  Test cases for tokencount.go (context package):
 *	  TestSimpleCountTextChinese, TestSimpleCountTextEnglish,
 *	  TestSimpleCountTextMixed.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/context/tokencount_test.go
 *
 *-------------------------------------------------------------------------
 */
package context

import (
	"strings"
	"testing"


)

func TestSimpleCountTextChinese(t *testing.T) {
	c := &SimpleTokenCounter{}
	// 30 Chinese characters → ~20 tokens (30 * 2/3)
	text := "这是一段中文测试文本用来验证分词估算的准确性和可靠性等等等等等等等等等"
	tokens := c.CountText(text)
	if tokens < 15 || tokens > 25 {
		t.Errorf("expected ~20 tokens for 30 Chinese chars, got %d", tokens)
	}
}

func TestSimpleCountTextEnglish(t *testing.T) {
	c := &SimpleTokenCounter{}
	// 40 English chars → ~10 tokens (40 / 4)
	text := "This is a test string for token count."
	tokens := c.CountText(text)
	if tokens < 7 || tokens > 15 {
		t.Errorf("expected ~10 tokens for English text, got %d", tokens)
	}
}

func TestSimpleCountTextMixed(t *testing.T) {
	c := &SimpleTokenCounter{}
	// Mixed: "Oracle 数据库性能" → 7 Chinese + 14 English
	text := "Oracle 数据库性能异常分析"
	tokens := c.CountText(text)
	if tokens < 5 || tokens > 15 {
		t.Errorf("expected 5-15 tokens for mixed text, got %d", tokens)
	}
}

func TestSimpleCountTextEmpty(t *testing.T) {
	c := &SimpleTokenCounter{}
	if c.CountText("") != 0 {
		t.Error("expected 0 for empty string")
	}
}

func TestSimpleCountTextMinimum(t *testing.T) {
	c := &SimpleTokenCounter{}
	// Very short text should still return at least 1
	if c.CountText("a") < 1 {
		t.Error("expected at least 1 token for non-empty string")
	}
}

func TestSimpleCountMessages(t *testing.T) {
	c := &SimpleTokenCounter{}
	messages := []Message{
		{Role: "system", Content: strings.Repeat("x", 400)}, // ~100 tokens + 4 overhead
		{Role: "user", Content: strings.Repeat("x", 200)},   // ~50 tokens + 4 overhead
	}
	total := c.Count(messages)
	// Expected: ~100 + 4 + ~50 + 4 = ~158
	if total < 100 || total > 200 {
		t.Errorf("expected ~158 tokens, got %d", total)
	}
}

func TestSimpleCountMessagesWithThinking(t *testing.T) {
	c := &SimpleTokenCounter{}
	messages := []Message{
		{
			Role:     "assistant",
			Content:  "analysis result",
			Thinking: strings.Repeat("x", 800), // ~200 tokens
		},
	}
	total := c.Count(messages)
	// Content ~4 + Thinking ~200 + overhead 4 = ~208
	if total < 150 || total > 250 {
		t.Errorf("expected ~208 tokens, got %d", total)
	}
}

func TestUsageTrackerInitialEstimate(t *testing.T) {
	tracker := NewUsageTracker()
	messages := []Message{
		{Role: "user", Content: strings.Repeat("x", 400)},
	}
	tokens := tracker.Count(messages)
	if tokens < 80 || tokens > 120 {
		t.Errorf("expected ~100 tokens, got %d", tokens)
	}
}

func TestUsageTrackerUpdateActual(t *testing.T) {
	tracker := NewUsageTracker()
	tracker.UpdateActual(3150)
	if tracker.lastActual != 3150 {
		t.Errorf("expected lastActual 3150, got %d", tracker.lastActual)
	}
}
