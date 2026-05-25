/*-------------------------------------------------------------------------
 *
 * result_test.go
 *	  Test cases for result.go (tool package): TestSmartTruncateNoOp,
 *	  TestSmartTruncateHeadAndTail, TestSmartTruncateTinyBudget.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/result_test.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"strings"
	"testing"
)

func TestSmartTruncateNoOp(t *testing.T) {
	content := "short content"
	result := SmartTruncate(content, 100)
	if result != content {
		t.Errorf("expected no truncation, got %q", result)
	}
}

func TestSmartTruncateHeadAndTail(t *testing.T) {
	// Create content of known length
	content := strings.Repeat("A", 200) + strings.Repeat("B", 200) + strings.Repeat("C", 200)
	// Total: 600 chars, budget: 300

	result := SmartTruncate(content, 300)

	if len(result) > 320 { // Allow some margin for the notice
		t.Errorf("truncated result too long: %d chars", len(result))
	}

	// Should start with A's (head)
	if !strings.HasPrefix(result, "AAAA") {
		t.Error("expected head to start with A's")
	}

	// Should end with C's (tail)
	if !strings.HasSuffix(result, "CCCC") {
		t.Error("expected tail to end with C's")
	}

	// Should contain omission notice
	if !strings.Contains(result, "字符已省略") {
		t.Error("expected omission notice")
	}
}

func TestSmartTruncateTinyBudget(t *testing.T) {
	content := strings.Repeat("X", 1000)
	result := SmartTruncate(content, 50)

	if len(result) > 55 {
		t.Errorf("expected ~50 chars, got %d", len(result))
	}
	if !strings.Contains(result, "truncated") {
		t.Error("expected truncated notice for tiny budget")
	}
}

func TestResultHandlerWithinBudget(t *testing.T) {
	h := NewResultHandler(4000, "")

	results := []ToolResult{
		{ToolCallID: "1", Name: "waits", Content: "short result"},
		{ToolCallID: "2", Name: "topsql", Content: "also short"},
	}

	processed := h.Process(results, 50000) // Plenty of room

	if len(processed) != 2 {
		t.Fatalf("expected 2 results, got %d", len(processed))
	}
	// Content should be unchanged
	if processed[0].Content != "short result" {
		t.Errorf("expected unchanged content, got %q", processed[0].Content)
	}
}

func TestResultHandlerDynamicBudget(t *testing.T) {
	h := NewResultHandler(4000, "")

	bigContent := strings.Repeat("X", 10000)
	results := []ToolResult{
		{ToolCallID: "1", Name: "topsql", Content: bigContent},
	}

	// 10000 remaining tokens → budget = 10000*30% = 3000, capped at maxResultSize=4000
	// But per tool = 3000/1 = 3000
	processed := h.Process(results, 10000)

	if len(processed[0].Content) >= len(bigContent) {
		t.Error("expected content to be truncated")
	}
	if len(processed[0].Content) > 3200 { // ~3000 + notice overhead
		t.Errorf("expected ~3000 chars, got %d", len(processed[0].Content))
	}
}

func TestResultHandlerMultipleTools(t *testing.T) {
	h := NewResultHandler(4000, "")

	bigContent := strings.Repeat("X", 10000)
	results := []ToolResult{
		{ToolCallID: "1", Name: "waits", Content: "small"},
		{ToolCallID: "2", Name: "topsql", Content: bigContent},
		{ToolCallID: "3", Name: "explain", Content: "also small"},
	}

	// 20000 remaining → budget = 20000*30%=6000, per tool = 6000/3=2000
	processed := h.Process(results, 20000)

	// Small results unchanged
	if processed[0].Content != "small" {
		t.Errorf("small result should be unchanged, got %q", processed[0].Content)
	}
	if processed[2].Content != "also small" {
		t.Errorf("small result should be unchanged, got %q", processed[2].Content)
	}

	// Big result truncated
	if len(processed[1].Content) >= len(bigContent) {
		t.Error("big result should be truncated")
	}
}

func TestResultHandlerErrorNotTruncated(t *testing.T) {
	h := NewResultHandler(100, "") // Very small limit

	results := []ToolResult{
		{ToolCallID: "1", Name: "bad", Error: strings.Repeat("E", 500)},
	}

	processed := h.Process(results, 1000)

	// Error messages should never be truncated
	if processed[0].Error != results[0].Error {
		t.Error("error message should not be truncated")
	}
}

func TestResultHandlerImmutability(t *testing.T) {
	h := NewResultHandler(4000, "")

	original := []ToolResult{
		{ToolCallID: "1", Name: "topsql", Content: strings.Repeat("X", 10000)},
	}

	originalContent := original[0].Content
	h.Process(original, 5000)

	// Original should not be mutated
	if original[0].Content != originalContent {
		t.Error("original result was mutated")
	}
}

func TestResultHandlerMinimumBudget(t *testing.T) {
	h := NewResultHandler(4000, "")

	bigContent := strings.Repeat("X", 10000)
	results := []ToolResult{
		{ToolCallID: "1", Name: "tool", Content: bigContent},
	}

	// Very small remaining → totalBudget floors at 2000, perTool floors at 500
	processed := h.Process(results, 100)

	if processed[0].Content == bigContent {
		t.Error("expected truncation even with tiny remaining")
	}
	// Should still have some content (minimum 500)
	if len(processed[0].Content) < 100 {
		t.Error("result too aggressively truncated")
	}
}
