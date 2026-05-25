/*-------------------------------------------------------------------------
 *
 * json_test.go
 *	  Test cases for json.go (format package): TestFormatJSON,
 *	  TestFormatJSON_Empty, TestFormatJSON_Nil.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/json_test.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"encoding/json"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
)

func TestFormatJSON(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"SID", "USERNAME", "STATUS"},
		Rows: [][]any{
			{142, "APP_USER", "ACTIVE"},
			{287, "BATCH", "INACTIVE"},
		},
	}

	out := FormatJSON(result)

	// Verify it's valid JSON
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output should be valid JSON: %v", err)
	}

	if len(parsed) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(parsed))
	}

	// Verify column names as keys
	row0 := parsed[0]
	if _, ok := row0["SID"]; !ok {
		t.Error("first row should have key SID")
	}
	if _, ok := row0["USERNAME"]; !ok {
		t.Error("first row should have key USERNAME")
	}
	if _, ok := row0["STATUS"]; !ok {
		t.Error("first row should have key STATUS")
	}

	// Verify values
	if row0["USERNAME"] != "APP_USER" {
		t.Errorf("expected USERNAME=APP_USER, got %v", row0["USERNAME"])
	}
	if row0["STATUS"] != "ACTIVE" {
		t.Errorf("expected STATUS=ACTIVE, got %v", row0["STATUS"])
	}

	row1 := parsed[1]
	if row1["USERNAME"] != "BATCH" {
		t.Errorf("expected USERNAME=BATCH, got %v", row1["USERNAME"])
	}
}

func TestFormatJSON_Empty(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID"},
		Rows:    [][]any{},
	}

	out := FormatJSON(result)

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output should be valid JSON: %v", err)
	}

	if len(parsed) != 0 {
		t.Errorf("expected empty array, got %d elements", len(parsed))
	}
}

func TestFormatJSON_Nil(t *testing.T) {
	out := FormatJSON(nil)

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("nil result should produce valid JSON: %v", err)
	}

	if len(parsed) != 0 {
		t.Errorf("expected empty array for nil result, got %d elements", len(parsed))
	}
}

func TestFormatJSON_NilValues(t *testing.T) {
	result := &db.QueryResult{
		Columns: []string{"ID", "NAME"},
		Rows: [][]any{
			{1, nil},
		},
	}

	out := FormatJSON(result)

	var parsed []map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output should be valid JSON: %v", err)
	}

	if parsed[0]["NAME"] != nil {
		t.Errorf("nil value should be JSON null, got %v", parsed[0]["NAME"])
	}
}
