/*-------------------------------------------------------------------------
 *
 * markdown_test.go
 *	  Test cases for markdown.go (ui package):
 *	  TestFormatLine_HeadersNoEmbeddedNewline,
 *	  TestFormatLine_H2ProducesBlankLineBefore,
 *	  TestFormatLine_RegularLineNoNewline.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/markdown_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"strings"
	"testing"
)

func TestFormatLine_HeadersNoEmbeddedNewline(t *testing.T) {
	f := newDiagStreamFormatter(120)

	tests := []struct {
		name  string
		input string
	}{
		{"h1", "# 总结"},
		{"h2", "## 根因分析"},
		{"h3", "### 详情"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := f.formatLine(tt.input)
			for i, line := range lines {
				if strings.Contains(line, "\n") {
					t.Errorf("line[%d] contains embedded newline: %q", i, line)
				}
			}
		})
	}
}

func TestFormatLine_H2ProducesBlankLineBefore(t *testing.T) {
	f := newDiagStreamFormatter(120)

	lines := f.formatLine("## 根因分析")

	// Should produce at least 2 entries: a blank line + the header.
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines (blank + header), got %d: %v", len(lines), lines)
	}
	if lines[0] != "" {
		t.Errorf("first line should be blank, got %q", lines[0])
	}
	if !strings.Contains(lines[1], "根因分析") {
		t.Errorf("second line should contain header text, got %q", lines[1])
	}
}

func TestFormatLine_RegularLineNoNewline(t *testing.T) {
	f := newDiagStreamFormatter(120)

	lines := f.formatLine("这是一行普通文字，罪魁祸首 SQL 是 abc123")

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if strings.Contains(lines[0], "\n") {
		t.Errorf("regular line should not contain embedded newline: %q", lines[0])
	}
}

func TestFormatLine_Blockquote(t *testing.T) {
	f := newDiagStreamFormatter(120)
	lines := f.formatLine("> 注意：此操作需要停机")

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	// Should contain the dim bar character ▎
	if !strings.Contains(lines[0], "▎") {
		t.Errorf("blockquote should have ▎ prefix, got %q", lines[0])
	}
	if !strings.Contains(lines[0], "注意") {
		t.Errorf("blockquote should contain original text, got %q", lines[0])
	}
}

func TestHighlightLine_NestedBoldAndCode(t *testing.T) {
	result := mdHighlightLine("**建议执行 `ALTER SEQUENCE` 命令**")

	// Should contain both bold and dim (inline code) ANSI sequences.
	if !strings.Contains(result, ansiBold) {
		t.Error("nested bold not rendered")
	}
	if !strings.Contains(result, ansiDim) {
		t.Error("nested inline code not rendered")
	}
	// The inline code content should be present.
	if !strings.Contains(result, "ALTER SEQUENCE") {
		t.Error("inline code content missing")
	}
	t.Logf("rendered: %q", result)
}

func TestFlushTable_VerticalFallback(t *testing.T) {
	f := newDiagStreamFormatter(60) // narrow terminal

	// Build a table that would overflow 60 cols in horizontal mode.
	f.tableLines = []string{
		"| 问题 | 影响 | 修复命令 |",
		"|------|------|----------|",
		"| NOARCHIVELOG | 数据丢失风险 | SHUTDOWN IMMEDIATE; STARTUP MOUNT; ALTER DATABASE ARCHIVELOG; ALTER DATABASE OPEN |",
	}

	lines := f.flushTable()
	if len(lines) == 0 {
		t.Fatal("expected non-empty output")
	}

	// Should fall back to vertical format (contains ─── prefix).
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "───") {
		t.Errorf("expected vertical format with ─── header, got:\n%s", joined)
	}
	t.Logf("vertical output:\n%s", joined)
}

func TestFormatLine_ActionBlockSuppressed(t *testing.T) {
	f := newDiagStreamFormatter(120)

	// Start action block.
	lines := f.formatLine("```action")
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for action start, got %d", len(lines))
	}

	// Action content should be suppressed.
	lines = f.formatLine(`{"skill": "activesessions", "args": ""}`)
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for action content, got %d", len(lines))
	}

	// End action block.
	lines = f.formatLine("```")
	if len(lines) != 0 {
		t.Errorf("expected 0 lines for action end, got %d", len(lines))
	}
}
