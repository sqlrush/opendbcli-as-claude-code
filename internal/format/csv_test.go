/*-------------------------------------------------------------------------
 *
 * csv_test.go
 *	  Test cases for csv.go (format package): TestFormatCSV,
 *	  TestFormatCSV_SpecialChars, TestFormatCSV_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/csv_test.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestFormatCSV(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"SID", "USERNAME", "STATUS"},
		Rows: [][]any{
			{142, "APP_USER", "ACTIVE"},
			{287, "BATCH", "INACTIVE"},
		},
	}

	out := FormatCSV(result)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 data rows), got %d", len(lines))
	}

	// Verify header
	if lines[0] != "SID,USERNAME,STATUS" {
		t.Errorf("expected header 'SID,USERNAME,STATUS', got %q", lines[0])
	}

	// Verify data rows
	if lines[1] != "142,APP_USER,ACTIVE" {
		t.Errorf("expected first data row '142,APP_USER,ACTIVE', got %q", lines[1])
	}
	if lines[2] != "287,BATCH,INACTIVE" {
		t.Errorf("expected second data row '287,BATCH,INACTIVE', got %q", lines[2])
	}
}

func TestFormatCSV_SpecialChars(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID", "DESCRIPTION", "NOTES"},
		Rows: [][]any{
			{1, "has,comma", "normal"},
			{2, "has \"quotes\"", "also, special"},
			{3, "has\nnewline", "plain"},
		},
	}

	out := FormatCSV(result)

	// Values with commas should be quoted
	if !strings.Contains(out, "\"has,comma\"") {
		t.Error("value with comma should be quoted")
	}

	// Values with quotes should be escaped (doubled)
	if !strings.Contains(out, "\"has \"\"quotes\"\"\"") {
		t.Error("value with quotes should have doubled quotes within quotes")
	}

	// Values with newlines should be quoted
	if !strings.Contains(out, "\"has\nnewline\"") {
		t.Error("value with newline should be quoted")
	}

	// Normal values should not be quoted
	if strings.Contains(out, "\"normal\"") {
		t.Error("normal value should not be quoted")
	}
}

func TestFormatCSV_Empty(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID", "NAME"},
		Rows:    [][]any{},
	}

	out := FormatCSV(result)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (header only), got %d", len(lines))
	}
	if lines[0] != "ID,NAME" {
		t.Errorf("expected header 'ID,NAME', got %q", lines[0])
	}
}

func TestFormatCSV_Nil(t *testing.T) {
	out := FormatCSV(nil)

	if out != "" {
		t.Errorf("nil result should produce empty string, got %q", out)
	}
}

func TestFormatCSV_NilValues(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID", "NAME"},
		Rows: [][]any{
			{1, nil},
		},
	}

	out := FormatCSV(result)

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// nil should produce empty field
	if lines[1] != "1," {
		t.Errorf("expected '1,' for nil value, got %q", lines[1])
	}
}
