/*-------------------------------------------------------------------------
 *
 * highlight_test.go
 *	  Test cases for highlight.go (ui package): TestHighlightCode_SQL,
 *	  TestHighlightCode_UnknownLang, TestHighlightCode_Shell.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/highlight_test.go
 *
 *-------------------------------------------------------------------------
 */
package ui

import (
	"strings"
	"testing"
)

func TestHighlightCode_SQL(t *testing.T) {
	lines := []string{
		"SELECT count(*)",
		"FROM   v$session",
		"WHERE  status = 'ACTIVE';",
	}
	result := highlightCode("sql", lines)

	if len(result) != len(lines) {
		t.Fatalf("expected %d lines, got %d", len(lines), len(result))
	}

	// Highlighted output should contain ANSI escape codes.
	joined := strings.Join(result, "\n")
	if !strings.Contains(joined, "\033[") {
		t.Error("SQL highlighting produced no ANSI codes")
		t.Logf("output: %q", joined)
	}

	// SELECT keyword should be highlighted.
	if !strings.Contains(result[0], "SELECT") {
		t.Error("SELECT keyword missing from highlighted output")
	}

	t.Logf("highlighted SQL:\n%s", joined)
}

func TestHighlightCode_UnknownLang(t *testing.T) {
	lines := []string{"some random text", "another line"}
	result := highlightCode("unknownlang", lines)

	// Should fall back to raw lines (no crash).
	if len(result) != len(lines) {
		t.Fatalf("expected %d lines, got %d", len(lines), len(result))
	}
	if result[0] != lines[0] {
		t.Errorf("expected raw fallback, got %q", result[0])
	}
}

func TestHighlightCode_Shell(t *testing.T) {
	lines := []string{
		"#!/bin/bash",
		"echo \"Hello World\"",
		"exit 0",
	}
	result := highlightCode("bash", lines)
	joined := strings.Join(result, "\n")

	if !strings.Contains(joined, "\033[") {
		t.Error("bash highlighting produced no ANSI codes")
	}
	t.Logf("highlighted bash:\n%s", joined)
}

func TestHighlightCode_Empty(t *testing.T) {
	result := highlightCode("sql", nil)
	if len(result) != 0 {
		t.Errorf("expected empty result for nil input, got %d lines", len(result))
	}
}

func TestRenderCodeBlock_WithHighlight(t *testing.T) {
	f := newDiagStreamFormatter(120)
	f.codeLines = []string{
		"SELECT count(*)",
		"FROM   v$session",
		"WHERE  status = 'ACTIVE';",
	}
	f.codeLang = "sql"

	var b strings.Builder
	f.renderCodeBlock(&b)
	output := b.String()

	// Should contain box borders.
	if !strings.Contains(output, "┌") || !strings.Contains(output, "┘") {
		t.Error("code block missing box borders")
	}
	// Should contain the language label.
	if !strings.Contains(output, "sql") {
		t.Error("code block missing language label")
	}
	// Should contain ANSI codes (from highlighting).
	if !strings.Contains(output, "\033[") {
		t.Error("code block has no syntax highlighting")
	}

	t.Logf("rendered code block:\n%s", output)
}

func TestRenderCodeBlock_WrapsLongSQLWithoutEllipsis(t *testing.T) {
	f := newDiagStreamFormatter(92)
	f.codeLang = "sql"
	f.codeLines = []string{
		"CREATE TABLE bench_customers_region_vip (LIKE bench_customers) PARTITION BY LIST (region); INSERT INTO bench_customers_region_vip SELECT * FROM bench_customers;",
	}

	var b strings.Builder
	f.renderCodeBlock(&b)
	output := b.String()
	plain := stripAnsi(output)

	if strings.Contains(plain, "..") || strings.Contains(plain, "…") {
		t.Fatalf("long SQL should wrap, not truncate with ellipsis:\n%s", output)
	}
	for _, want := range []string{"CREATE TABLE bench_customers_region_vip", "INSERT INTO", "SELECT * FROM bench_customers"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("wrapped SQL missing %q:\n%s", want, plain)
		}
	}
}
