/*-------------------------------------------------------------------------
 *
 * serializer_test.go
 *	  Tests for the compact tool serializer used by PromptToolAdapter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/serializer_test.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestSerializeToolsCompact_NoParams(t *testing.T) {
	tools := []provider.ToolSchema{
		{
			Name:        "health",
			Description: "查询数据库整体健康状态",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
	out := SerializeToolsCompact(tools)
	if !strings.Contains(out, "## health") {
		t.Errorf("missing heading: %s", out)
	}
	if !strings.Contains(out, "查询数据库整体健康状态") {
		t.Errorf("missing description: %s", out)
	}
	if !strings.Contains(out, "参数: 无") {
		t.Errorf("no-params marker missing: %s", out)
	}
}

func TestSerializeToolsCompact_WithRequiredParam(t *testing.T) {
	tools := []provider.ToolSchema{
		{
			Name:        "sqltune",
			Description: "对单条 SQL 做 5 维度调优分析",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"args": map[string]any{
						"type":        "string",
						"description": "完整可 EXPLAIN 的 SQL 文本",
					},
				},
				"required": []any{"args"},
			},
		},
	}
	out := SerializeToolsCompact(tools)
	if !strings.Contains(out, "args(string, 必填)") {
		t.Errorf("required marker missing: %s", out)
	}
	if !strings.Contains(out, "完整可 EXPLAIN") {
		t.Errorf("param description missing: %s", out)
	}
}

func TestSerializeToolsCompact_SortedDeterministic(t *testing.T) {
	tools := []provider.ToolSchema{
		{Name: "zeta", InputSchema: map[string]any{"properties": map[string]any{}}},
		{Name: "alpha", InputSchema: map[string]any{"properties": map[string]any{}}},
		{Name: "mid", InputSchema: map[string]any{"properties": map[string]any{}}},
	}
	out := SerializeToolsCompact(tools)
	idxA := strings.Index(out, "## alpha")
	idxM := strings.Index(out, "## mid")
	idxZ := strings.Index(out, "## zeta")
	if idxA == -1 || idxM == -1 || idxZ == -1 {
		t.Fatalf("missing headings: %s", out)
	}
	if !(idxA < idxM && idxM < idxZ) {
		t.Errorf("expected alpha < mid < zeta, got positions %d %d %d", idxA, idxM, idxZ)
	}
}

func TestSerializeToolsCompact_LongDescriptionTruncated(t *testing.T) {
	long := strings.Repeat("非常长的描述", 50) // 250+ chars
	tools := []provider.ToolSchema{
		{Name: "verbose", Description: long, InputSchema: map[string]any{"properties": map[string]any{}}},
	}
	out := SerializeToolsCompact(tools)
	if !strings.Contains(out, "...") {
		t.Errorf("long description should be truncated with '...': %s", out)
	}
}

func TestSerializeToolsCompact_Empty(t *testing.T) {
	out := SerializeToolsCompact(nil)
	if out != "(无可用工具)" {
		t.Errorf("empty list should say no tools, got %q", out)
	}
}

func TestSerializeToolsCompact_BudgetUnder60Tools(t *testing.T) {
	// Synthesize 60 tools with realistic-sized descriptions.
	tools := make([]provider.ToolSchema, 60)
	for i := range tools {
		tools[i] = provider.ToolSchema{
			Name:        "tool_" + string(rune('a'+i%26)) + "_xyz",
			Description: "这是一个示例工具，用于演示长度，大约 50 字左右描述",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"args": map[string]any{
						"type":        "string",
						"description": "参数描述大约 30 字左右",
					},
				},
				"required": []any{"args"},
			},
		}
	}
	out := SerializeToolsCompact(tools)
	// Rough byte budget: 60 tools × ~150 bytes/tool = 9KB; allow 12KB
	// safety margin. (Token estimates vary too much by tokenizer to be
	// reliable in unit tests; verify token cost in benchmark instead.)
	const maxBytes = 12000
	if len(out) > maxBytes {
		t.Errorf("60 tools should fit in %d bytes, got %d", maxBytes, len(out))
	}
	t.Logf("60-tool serialization: %d bytes", len(out))
}

func TestSerializeToolsCompactSummary(t *testing.T) {
	tools := []provider.ToolSchema{
		{Name: "health"}, {Name: "alert"}, {Name: "topsql"},
	}
	got := SerializeToolsCompactSummary(tools)
	want := "tools: alert, health, topsql"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Many tools → +N more suffix.
	many := make([]provider.ToolSchema, 10)
	for i := range many {
		many[i] = provider.ToolSchema{Name: "t" + string(rune('0'+i))}
	}
	got = SerializeToolsCompactSummary(many)
	if !strings.Contains(got, "+5 more") {
		t.Errorf("10 tools should show +5 more, got %q", got)
	}
}

func TestCompactDescription(t *testing.T) {
	in := "Line 1\n\nLine 2\n   Line 3 with extra spaces"
	out := compactDescription(in)
	want := "Line 1 Line 2 Line 3 with extra spaces"
	if out != want {
		t.Errorf("got %q, want %q", out, want)
	}
}
