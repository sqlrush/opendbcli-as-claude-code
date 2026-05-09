/*-------------------------------------------------------------------------
 *
 * format_test.go
 *	  Test cases for format.go (agent package):
 *	  TestRenderLLMContent_Headers, TestRenderLLMContent_CodeBlock,
 *	  TestRenderLLMContent_BulletList.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/format_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"strings"
	"testing"
)

func TestRenderLLMContent_Headers(t *testing.T) {
	content := "## 根因分析\n问题SQL导致I/O问题\n### 详细信息\n具体描述"
	output := RenderLLMContent(content)

	if !strings.Contains(output, "根因") {
		t.Error("should contain header text")
	}
	if !strings.Contains(output, "详细信息") {
		t.Error("should contain sub-header text")
	}
	if !strings.Contains(output, "■") {
		t.Error("should contain section marker ■ for ## headers")
	}
	if !strings.Contains(output, "▸") {
		t.Error("should contain section marker ▸ for ### headers")
	}
}

func TestRenderLLMContent_CodeBlock(t *testing.T) {
	content := "查看SQL:\n```sql\nSELECT * FROM v$session;\n```\n结束"
	output := RenderLLMContent(content)

	if !strings.Contains(output, "v$session") {
		t.Error("should contain SQL content")
	}
	if !strings.Contains(output, "sql") {
		t.Error("should contain language label")
	}
	if !strings.Contains(output, "┌") {
		t.Error("should contain code block border")
	}
}

func TestRenderLLMContent_BulletList(t *testing.T) {
	content := "问题列表:\n- 第一个问题\n- 第二个问题\n* 第三个问题"
	output := RenderLLMContent(content)

	if strings.Count(output, "•") != 3 {
		t.Errorf("should have 3 bullet markers, got %d", strings.Count(output, "•"))
	}
}

func TestRenderLLMContent_NumberedList(t *testing.T) {
	content := "步骤:\n1. 检查会话\n2. 分析等待事件\n3. 定位SQL"
	output := RenderLLMContent(content)

	if !strings.Contains(output, "1.") {
		t.Error("should contain numbered item 1")
	}
	if !strings.Contains(output, "3.") {
		t.Error("should contain numbered item 3")
	}
}

func TestRenderLLMContent_MetricTransition(t *testing.T) {
	content := "活跃会话从 10 → 50"
	output := RenderLLMContent(content)

	if !strings.Contains(output, "10") {
		t.Error("should contain metric value")
	}
	if !strings.Contains(output, "50") {
		t.Error("should contain target value")
	}
}

func TestRenderLLMContent_Empty(t *testing.T) {
	output := RenderLLMContent("")
	if output != "\n" {
		t.Errorf("empty content should produce single newline, got %q", output)
	}
}

func TestRenderThinkingChain(t *testing.T) {
	rounds := []RoundInfo{
		{Round: 1, Action: "sessions", Summary: "查询 sessions", Thinking: "我需要先查看活跃会话"},
		{Round: 2, Summary: "输出最终诊断"},
	}
	output := RenderThinkingChain(rounds)

	if !strings.Contains(output, "推理过程") {
		t.Error("should contain reasoning header")
	}
	if !strings.Contains(output, "第1轮") {
		t.Error("should contain round 1")
	}
	if !strings.Contains(output, "第2轮") {
		t.Error("should contain round 2")
	}
	if !strings.Contains(output, "●") {
		t.Error("should have filled marker for action round")
	}
	if !strings.Contains(output, "○") {
		t.Error("should have empty marker for non-action round")
	}
}

func TestRenderThinkingChain_Empty(t *testing.T) {
	output := RenderThinkingChain(nil)
	if output != "" {
		t.Errorf("empty rounds should produce empty output, got %q", output)
	}
}

func TestExtractSections(t *testing.T) {
	content := `## 根因分析
SQL abc123 导致 I/O 问题

## 紧急措施
ALTER SYSTEM KILL SESSION '42,1';

## 根因修复
添加索引 CREATE INDEX ...`

	sections := ExtractSections(content)

	if _, ok := sections["根因分析"]; !ok {
		t.Error("should extract 根因分析 section")
	}
	if !strings.Contains(sections["根因分析"], "abc123") {
		t.Error("根因分析 section should contain SQL ID")
	}
	if _, ok := sections["紧急措施"]; !ok {
		t.Error("should extract 紧急措施 section")
	}
	if _, ok := sections["根因修复"]; !ok {
		t.Error("should extract 根因修复 section")
	}
	if len(sections) != 3 {
		t.Errorf("should have 3 sections, got %d", len(sections))
	}
}

func TestExtractSections_NoHeaders(t *testing.T) {
	content := "plain text without any headers"
	sections := ExtractSections(content)
	if len(sections) != 0 {
		t.Errorf("should have 0 sections for headerless content, got %d", len(sections))
	}
}

func TestLooksLikeSQLID(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abc123def4567", true},  // 13 chars, has both digits and letters
		{"1234567890abc", true},  // 13 chars
		{"abcdefghijklm", false}, // all letters, no digits
		{"1234567890123", false}, // all digits, no letters
		{"short", false},         // too short
		{"abc123def45678", false}, // 14 chars, too long
		{"abc 123def456", false},  // has space
	}
	for _, tt := range tests {
		if got := looksLikeSQLID(tt.input); got != tt.want {
			t.Errorf("looksLikeSQLID(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestHighlightMetrics(t *testing.T) {
	input := "redo rate 从 100 → 500"
	output := highlightMetrics(input)

	// Should contain ANSI yellow around the metric transition.
	if !strings.Contains(output, ansiYellow) {
		t.Error("should contain ANSI yellow for metric transition")
	}

	// Arrow-style transition should also work.
	input2 := "value 10 -> 20"
	output2 := highlightMetrics(input2)
	if !strings.Contains(output2, ansiYellow) {
		t.Error("should highlight -> style transition")
	}
}
