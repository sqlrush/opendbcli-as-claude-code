/*-------------------------------------------------------------------------
 *
 * sql_test.go
 *	  Test cases for sql.go (query package): TestSQLSkill_Metadata,
 *	  TestSQLSkill_Validate, TestSQLSkill_Execute_Select.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/sql_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestSQLSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSQLSkill(drv)

	if s.Name() != "sql" {
		t.Errorf("Name() = %q, want %q", s.Name(), "sql")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "sql" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "sql")
	}
	if s.CLIDef().Command != "sql" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "sql")
	}
}

func TestSQLSkill_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "empty sql",
			params:  map[string]any{"sql": ""},
			wantErr: true,
		},
		{
			name:    "missing sql",
			params:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "whitespace only",
			params:  map[string]any{"sql": "   "},
			wantErr: true,
		},
		{
			name:    "valid sql",
			params:  map[string]any{"sql": "SELECT 1 FROM dual"},
			wantErr: false,
		},
	}

	drv := mock.NewMockDriver()
	s := NewSQLSkill(drv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(skill.ParamsFromMap(tt.params))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSQLSkill_Execute_Select(t *testing.T) {
	tests := []struct {
		name         string
		sql          string
		queryRows    [][]any
		queryErr     error
		wantErr      bool
		wantType     skill.ResultType
		wantRowCount int
	}{
		{
			name: "simple select",
			sql:  "SELECT * FROM dual",
			queryRows: [][]any{
				{"X"},
			},
			wantType:     skill.ResultTable,
			wantRowCount: 1,
		},
		{
			name: "select with multiple rows",
			sql:  "SELECT id, name FROM employees",
			queryRows: [][]any{
				{1, "Alice"},
				{2, "Bob"},
				{3, "Charlie"},
			},
			wantType:     skill.ResultTable,
			wantRowCount: 3,
		},
		{
			name:     "show statement",
			sql:      "SHOW PARAMETER db_name",
			wantType: skill.ResultTable,
		},
		{
			name:     "query error",
			sql:      "SELECT * FROM nonexistent",
			queryErr: fmt.Errorf("ORA-00942"),
			wantErr:  true,
		},
		{
			name: "with cte select",
			sql:  "WITH cte AS (SELECT 1 AS n FROM dual) SELECT * FROM cte",
			queryRows: [][]any{
				{1},
			},
			wantType:     skill.ResultTable,
			wantRowCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return &db.QueryResult{
					Columns:  []string{"col1"},
					Rows:     tt.queryRows,
					Duration: 10 * time.Millisecond,
				}, nil
			}

			s := NewSQLSkill(drv)
			params := skill.ParamsFromMap(map[string]any{"sql": tt.sql})

			result, err := s.Execute(context.Background(), params)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Execute() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() unexpected error: %v", err)
			}

			if result.Type != tt.wantType {
				t.Errorf("result type = %v, want %v", result.Type, tt.wantType)
			}

			if tt.wantType == skill.ResultTable {
				qr, ok := result.Data.(*db.QueryResult)
				if !ok {
					t.Fatal("result data is not *db.QueryResult")
				}
				if tt.wantRowCount > 0 && len(qr.Rows) != tt.wantRowCount {
					t.Errorf("row count = %d, want %d", len(qr.Rows), tt.wantRowCount)
				}
			}
		})
	}
}

func TestSQLSkill_Execute_DML(t *testing.T) {
	tests := []struct {
		name         string
		sql          string
		rowsAffected int64
		execErr      error
		wantErr      bool
	}{
		{
			name:         "insert",
			sql:          "INSERT INTO t VALUES (1, 'test')",
			rowsAffected: 1,
		},
		{
			name:         "update",
			sql:          "UPDATE t SET name = 'new' WHERE id = 1",
			rowsAffected: 1,
		},
		{
			name:         "delete",
			sql:          "DELETE FROM t WHERE id = 1",
			rowsAffected: 1,
		},
		{
			name:         "create table",
			sql:          "CREATE TABLE test (id NUMBER PRIMARY KEY)",
			rowsAffected: 0,
		},
		{
			name:    "exec error",
			sql:     "INSERT INTO nonexistent VALUES (1)",
			execErr: fmt.Errorf("ORA-00942"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			drv.ExecFunc = func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
				if tt.execErr != nil {
					return nil, tt.execErr
				}
				return &db.ExecResult{
					RowsAffected: tt.rowsAffected,
					Duration:     15 * time.Millisecond,
				}, nil
			}

			s := NewSQLSkill(drv)
			params := skill.ParamsFromMap(map[string]any{"sql": tt.sql})

			result, err := s.Execute(context.Background(), params)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Execute() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute() unexpected error: %v", err)
			}

			if result.Type != skill.ResultText {
				t.Errorf("result type = %v, want ResultText", result.Type)
			}

			text, ok := result.Data.(string)
			if !ok {
				t.Fatal("result data is not string")
			}
			if !strings.Contains(text, "affected") {
				t.Errorf("result text %q does not contain 'affected'", text)
			}
		})
	}
}

func TestSQLSkill_Execute_MultipleStatements(t *testing.T) {
	drv := mock.NewMockDriver()
	var execCalls, queryCalls int
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		queryCalls++
		return &db.QueryResult{
			Columns:  []string{"col1"},
			Rows:     [][]any{{"value"}},
			Duration: 5 * time.Millisecond,
		}, nil
	}
	drv.ExecFunc = func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
		execCalls++
		return &db.ExecResult{
			RowsAffected: 1,
			Duration:     10 * time.Millisecond,
		}, nil
	}

	s := NewSQLSkill(drv)
	params := skill.ParamsFromMap(map[string]any{
		"sql": "SELECT 1 FROM dual; INSERT INTO t VALUES (1); SELECT 2 FROM dual",
	})

	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	if result.Type != skill.ResultText {
		t.Errorf("result type = %v, want ResultText (multi-statement)", result.Type)
	}

	if queryCalls != 2 {
		t.Errorf("query calls = %d, want 2", queryCalls)
	}
	if execCalls != 1 {
		t.Errorf("exec calls = %d, want 1", execCalls)
	}

	if !strings.Contains(result.Summary, "3 statement(s)") {
		t.Errorf("summary = %q, want to contain '3 statement(s)'", result.Summary)
	}
}

func TestSQLSkill_Execute_MultipleStatements_PartialError(t *testing.T) {
	drv := mock.NewMockDriver()
	queryCallNum := 0
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		queryCallNum++
		if queryCallNum == 2 {
			return nil, fmt.Errorf("ORA-00942")
		}
		return &db.QueryResult{
			Columns:  []string{"col1"},
			Rows:     [][]any{{"ok"}},
			Duration: 5 * time.Millisecond,
		}, nil
	}

	s := NewSQLSkill(drv)
	params := skill.ParamsFromMap(map[string]any{
		"sql": "SELECT 1 FROM dual; SELECT * FROM bad_table; SELECT 3 FROM dual",
	})

	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	text, ok := result.Data.(string)
	if !ok {
		t.Fatal("result data is not string")
	}

	// Should contain an error for the second statement but still succeed overall.
	if !strings.Contains(text, "Error:") {
		t.Errorf("result text should contain 'Error:' for failed statement")
	}
	if !strings.Contains(text, "Statement 3") {
		t.Errorf("result text should contain 'Statement 3' (third statement executed)")
	}
}

func TestIsReadOnly(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT * FROM dual", true},
		{"select count(*) from t", true},
		{"SHOW PARAMETER db_name", true},
		{"show parameter db_name", true},
		{"DESCRIBE employees", true},
		{"DESC employees", true},
		{"EXPLAIN PLAN FOR SELECT 1", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO t VALUES (1)", false},
		{"UPDATE t SET x = 1", false},
		{"DELETE FROM t", false},
		{"CREATE TABLE t (id INT)", false},
		{"DROP TABLE t", false},
		{"ALTER TABLE t ADD col INT", false},
		{"GRANT SELECT ON t TO user1", false},
	}

	for _, tt := range tests {
		t.Run(tt.sql, func(t *testing.T) {
			got := isReadOnly(tt.sql)
			if got != tt.want {
				t.Errorf("isReadOnly(%q) = %v, want %v", tt.sql, got, tt.want)
			}
		})
	}
}

func TestSplitSQL(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"single statement", "SELECT 1 FROM dual", 1},
		{"two statements", "SELECT 1; SELECT 2", 2},
		{"trailing semicolon", "SELECT 1;", 1},
		{"empty between", "SELECT 1;; SELECT 2", 2},
		{"whitespace segments", "SELECT 1;   ; SELECT 2", 2},
		{"three statements", "SELECT 1; INSERT INTO t VALUES(1); SELECT 2", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitSQL(tt.input)
			if len(got) != tt.want {
				t.Errorf("splitSQL(%q) returned %d parts, want %d", tt.input, len(got), tt.want)
			}
		})
	}
}

func TestTruncateSQL(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"short", "SELECT 1", 80, "SELECT 1"},
		{"exact length", "SELECT 1", 8, "SELECT 1"},
		{"truncated", "SELECT * FROM very_long_table_name WHERE id = 123", 20, "SELECT * FROM ver..."},
		{"whitespace collapse", "SELECT  1  FROM  dual", 80, "SELECT 1 FROM dual"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateSQL(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("truncateSQL(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
		})
	}
}
