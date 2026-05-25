/*-------------------------------------------------------------------------
 *
 * summary_test.go
 *	  Test cases for summary.go (util package): TestFormatSummary,
 *	  TestAppendSummary, TestAppendSummary_EmptyEntries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/util/summary_test.go
 *
 *-------------------------------------------------------------------------
 */
package util

import (
	"strings"
	"testing"
)

func TestFormatSummary(t *testing.T) {
	tests := []struct {
		name    string
		entries []SummaryEntry
		wantSub []string
		wantEmp bool
	}{
		{
			name:    "empty entries returns empty",
			entries: nil,
			wantEmp: true,
		},
		{
			name: "single entry",
			entries: []SummaryEntry{
				{Key: "total_sessions", Val: 5},
			},
			wantSub: []string{"\n[summary]\n", "total_sessions: 5\n"},
		},
		{
			name: "multiple entries preserve order",
			entries: []SummaryEntry{
				{Key: "blocked_sessions", Val: 3},
				{Key: "kill_session_syntax", Val: "CALL SP_CLOSE_SESSION(<sess_id>)"},
				{Key: "is_anomaly", Val: true},
			},
			wantSub: []string{
				"blocked_sessions: 3\n",
				"kill_session_syntax: CALL SP_CLOSE_SESSION(<sess_id>)\n",
				"is_anomaly: true\n",
			},
		},
		{
			name: "various value types",
			entries: []SummaryEntry{
				{Key: "int", Val: 42},
				{Key: "int64", Val: int64(140304100)},
				{Key: "float", Val: 3.14},
				{Key: "bool", Val: false},
				{Key: "nil", Val: nil},
			},
			wantSub: []string{
				"int: 42\n",
				"int64: 140304100\n",
				"float: 3.14\n",
				"bool: false\n",
				"nil: <nil>\n",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatSummary(tt.entries)
			if tt.wantEmp {
				if got != "" {
					t.Errorf("FormatSummary(nil) = %q, want empty", got)
				}
				return
			}
			for _, want := range tt.wantSub {
				if !strings.Contains(got, want) {
					t.Errorf("FormatSummary missing %q\nGot:\n%s", want, got)
				}
			}
		})
	}
}

func TestAppendSummary(t *testing.T) {
	rendered := "table_content\n"
	entries := []SummaryEntry{{Key: "k", Val: "v"}}
	got := AppendSummary(rendered, entries)
	if !strings.HasPrefix(got, "table_content\n") {
		t.Errorf("AppendSummary should preserve prefix. Got:\n%s", got)
	}
	if !strings.Contains(got, "[summary]") || !strings.Contains(got, "k: v\n") {
		t.Errorf("AppendSummary missing summary block\n%s", got)
	}
}

func TestAppendSummary_EmptyEntries(t *testing.T) {
	rendered := "original"
	got := AppendSummary(rendered, nil)
	if got != "original" {
		t.Errorf("AppendSummary(empty entries) should return rendered unchanged. Got: %q", got)
	}
}

func TestFormatTableWithSummary(t *testing.T) {
	table := "  ┌──────┐\n  │ DATA │\n  └──────┘\n"
	entries := []SummaryEntry{{Key: "row_count", Val: 1}}
	got := FormatTableWithSummary(table, entries)
	if !strings.HasPrefix(got, table) {
		t.Errorf("table content must come first. Got:\n%s", got)
	}
	if !strings.Contains(got, "row_count: 1") {
		t.Errorf("missing summary entry\n%s", got)
	}
	// summary 必须在表格之后 (LLM 阅读顺序)
	tableIdx := strings.Index(got, "└──────")
	summaryIdx := strings.Index(got, "[summary]")
	if tableIdx < 0 || summaryIdx < 0 || tableIdx >= summaryIdx {
		t.Errorf("[summary] must follow table content. Got:\n%s", got)
	}
}

func TestFirstString(t *testing.T) {
	tests := []struct {
		name string
		rows [][]any
		want string
	}{
		{"nil rows", nil, ""},
		{"empty rows", [][]any{}, ""},
		{"empty first row", [][]any{{}}, ""},
		{"nil first cell", [][]any{{nil}}, ""},
		{"int", [][]any{{int64(42)}, {int64(99)}}, "42"},
		{"string", [][]any{{"hello"}}, "hello"},
		{"float", [][]any{{3.14}}, "3.14"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FirstString(tt.rows); got != tt.want {
				t.Errorf("FirstString(%v) = %q, want %q", tt.rows, got, tt.want)
			}
		})
	}
}

func TestCountByCol(t *testing.T) {
	rows := [][]any{
		{int64(1), "ACTIVE"},
		{int64(2), "ACTIVE"},
		{int64(3), "IDLE"},
		{int64(4), "IDLE"},
		{int64(5), "IDLE"},
		{int64(6), nil}, // nil 应跳过
	}
	got := CountByCol(rows, 1)
	if got["ACTIVE"] != 2 {
		t.Errorf("ACTIVE count = %d, want 2", got["ACTIVE"])
	}
	if got["IDLE"] != 3 {
		t.Errorf("IDLE count = %d, want 3", got["IDLE"])
	}
	if len(got) != 2 {
		t.Errorf("got %d distinct values (incl. nil?), want 2", len(got))
	}
}

func TestCountByCol_OutOfRange(t *testing.T) {
	rows := [][]any{{int64(1)}, {int64(2)}}
	got := CountByCol(rows, 5) // colIdx 越界
	if len(got) != 0 {
		t.Errorf("CountByCol(out-of-range) = %v, want empty", got)
	}
}

func TestCountByCol_Empty(t *testing.T) {
	got := CountByCol(nil, 0)
	if len(got) != 0 {
		t.Errorf("CountByCol(nil) = %v, want empty", got)
	}
}
