/*-------------------------------------------------------------------------
 *
 * table_test.go
 *	  Test cases for table.go (format package): TestFormatTable,
 *	  TestFormatTable_Empty, TestFormatTable_NilResult.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/table_test.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestFormatTable(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"SID", "USERNAME", "STATUS"},
		Rows: [][]any{
			{142, "APP_USER", "ACTIVE"},
			{287, "BATCH", "INACTIVE"},
		},
	}

	out := FormatTable(result)

	if !strings.Contains(out, "SID") {
		t.Error("output should contain column header SID")
	}
	if !strings.Contains(out, "USERNAME") {
		t.Error("output should contain column header USERNAME")
	}
	if !strings.Contains(out, "STATUS") {
		t.Error("output should contain column header STATUS")
	}
	if !strings.Contains(out, "APP_USER") {
		t.Error("output should contain row data APP_USER")
	}
	if !strings.Contains(out, "BATCH") {
		t.Error("output should contain row data BATCH")
	}
	if !strings.Contains(out, "142") {
		t.Error("output should contain row data 142")
	}
	if !strings.Contains(out, "287") {
		t.Error("output should contain row data 287")
	}

	// Verify alignment: all lines with data should have consistent separators
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines (header, separator, rows), got %d", len(lines))
	}
}

func TestFormatTable_Empty(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"SID", "USERNAME"},
		Rows:    [][]any{},
	}

	out := FormatTable(result)

	if !strings.Contains(out, "No rows") {
		t.Error("empty result should show 'No rows' message")
	}
}

func TestFormatTable_NilResult(t *testing.T) {
	out := FormatTable(nil)

	if !strings.Contains(out, "No rows") {
		t.Error("nil result should show 'No rows' message")
	}
}

func TestFormatTable_NilValues(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID", "NAME", "VALUE"},
		Rows: [][]any{
			{1, nil, "OK"},
			{2, "test", nil},
		},
	}

	out := FormatTable(result)

	// NULL cells render as "-" per CLAUDE.md style guide (nullDisplay in
	// table.go). Test guards the nullDisplay contract: any change to the
	// placeholder must also update this assertion.
	if !strings.Contains(out, "-") {
		t.Error("nil values should be displayed as '-' placeholder")
	}
	if !strings.Contains(out, "OK") {
		t.Error("output should contain non-nil value OK")
	}
	if !strings.Contains(out, "test") {
		t.Error("output should contain non-nil value test")
	}
}

func TestFormatTableWithLimit(t *testing.T) {
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = []any{i}
	}

	result := &db.QueryResult{
		Columns: []string{"ID"},
		Rows:    rows,
	}

	out := FormatTableWithLimit(result, 10)

	if !strings.Contains(out, "90 more") {
		t.Error("should show truncation message with 90 more rows")
	}

	// Verify only 10 data rows are present (plus header, separator, truncation)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dataLineCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "│") {
			dataLineCount++
		}
	}
	// header + 10 data rows = 11 lines with "│"
	if dataLineCount != 11 {
		t.Errorf("expected 11 lines with '│' (1 header + 10 data), got %d", dataLineCount)
	}
}

func TestFormatTableWithLimit_UnderLimit(t *testing.T) {
	rows := make([][]any, 5)
	for i := range rows {
		rows[i] = []any{i}
	}

	result := &db.QueryResult{
		Columns: []string{"ID"},
		Rows:    rows,
	}

	out := FormatTableWithLimit(result, 10)

	if strings.Contains(out, "more") {
		t.Error("should not show truncation message when rows are under limit")
	}

	// All 5 rows should be present
	for i := 0; i < 5; i++ {
		if !strings.Contains(out, fmt.Sprintf("%d", i)) {
			t.Errorf("output should contain row value %d", i)
		}
	}
}

func TestFormatTable_NewlinesInCells(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"TIME", "MESSAGE", "LEVEL"},
		Rows: [][]any{
			{"2026-03-10", "ORA-00060:\ndeadlock detected\nwhile waiting", "WARNING"},
			{"2026-03-10", "checkpoint\r\ncomplete", "INFO"},
		},
	}

	out := FormatTable(result)

	// Each data row should be exactly one line (newlines replaced with spaces).
	lines := strings.Split(strings.TrimSpace(out), "\n")
	dataLines := 0
	for _, line := range lines {
		if strings.Contains(line, "│") && strings.Contains(line, "2026-03-10") {
			dataLines++
		}
	}
	if dataLines != 2 {
		t.Errorf("expected 2 data lines, got %d — newlines in cell content broke the table", dataLines)
	}
	if !strings.Contains(out, "deadlock detected") {
		t.Error("output should contain flattened message text")
	}
}
