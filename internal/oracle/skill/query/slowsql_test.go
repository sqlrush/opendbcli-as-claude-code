/*-------------------------------------------------------------------------
 *
 * slowsql_test.go
 *	  Test cases for slowsql.go (query package):
 *	  TestSlowSQLSkill_Metadata, TestSlowSQLSkill_Validate,
 *	  TestSlowSQLSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/slowsql_test.go
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

func TestSlowSQLSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSlowSQLSkill(drv)

	if s.Name() != "slowsql" {
		t.Errorf("Name() = %q, want %q", s.Name(), "slowsql")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "slowsql" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "slowsql")
	}
	if s.CLIDef().Command != "slowsql" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "slowsql")
	}
}

func TestSlowSQLSkill_Validate(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSlowSQLSkill(drv)

	// Validate always succeeds (threshold is optional).
	if err := s.Validate(skill.ParamsFromMap(map[string]any{})); err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestSlowSQLSkill_Execute(t *testing.T) {
	tests := []struct {
		name          string
		args          string
		wantThreshold int
		queryRows     [][]any
		queryErr      error
		wantErr       bool
		wantRowCount  int
	}{
		{
			name:          "default threshold",
			args:          "",
			wantThreshold: 1000,
			queryRows: [][]any{
				{"sql1", 5000.0, 10, 100, 500, "SELECT ..."},
			},
			wantRowCount: 1,
		},
		{
			name:          "custom threshold",
			args:          "5000",
			wantThreshold: 5000,
			queryRows: [][]any{
				{"sql1", 8000.0, 5, 50, 200, "UPDATE ..."},
				{"sql2", 6000.0, 3, 30, 150, "INSERT ..."},
			},
			wantRowCount: 2,
		},
		{
			name:          "invalid threshold falls back to default",
			args:          "abc",
			wantThreshold: 1000,
			queryRows:     [][]any{},
			wantRowCount:  0,
		},
		{
			name:          "negative threshold falls back to default",
			args:          "-100",
			wantThreshold: 1000,
			queryRows:     [][]any{},
			wantRowCount:  0,
		},
		{
			name:          "zero threshold falls back to default",
			args:          "0",
			wantThreshold: 1000,
			queryRows:     [][]any{},
			wantRowCount:  0,
		},
		{
			name:     "query error",
			args:     "",
			queryErr: fmt.Errorf("ORA-00942: table or view does not exist"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedArgs []any
			drv := mock.NewMockDriver()
			drv.QueryFunc = func(_ context.Context, sql string, args ...any) (*db.QueryResult, error) {
				// The FTS batch query (v$sql_plan) has no bind args; skip capturing for it.
				if strings.Contains(sql, "v$sql_plan") {
					return &db.QueryResult{
						Columns: []string{"sql_id"},
						Rows:    nil,
					}, nil
				}
				capturedArgs = args
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return &db.QueryResult{
					Columns:  []string{"sql_id", "elapsed_ms", "executions", "rows_processed", "buffer_gets", "sql_text"},
					Rows:     tt.queryRows,
					Duration: 42 * time.Millisecond,
				}, nil
			}

			s := NewSlowSQLSkill(drv)
			params := skill.ParamsFromMap(map[string]any{})
			if tt.args != "" {
				params = skill.ParamsFromMap(map[string]any{"args": tt.args})
			}

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

			// Verify threshold was passed to the query.
			if len(capturedArgs) != 1 {
				t.Fatalf("expected 1 query arg, got %d", len(capturedArgs))
			}
			if got, ok := capturedArgs[0].(int); ok && got != tt.wantThreshold {
				t.Errorf("threshold = %d, want %d", got, tt.wantThreshold)
			}

			if result.Type != skill.ResultText {
				t.Errorf("result type = %v, want ResultText", result.Type)
			}

			qr, ok := result.Data.(*db.QueryResult)
			if !ok {
				t.Fatal("result data is not *db.QueryResult")
			}
			if len(qr.Rows) != tt.wantRowCount {
				t.Errorf("row count = %d, want %d", len(qr.Rows), tt.wantRowCount)
			}

			if result.Metadata["threshold_ms"] != fmt.Sprintf("%d", tt.wantThreshold) {
				t.Errorf("metadata threshold_ms = %q, want %q",
					result.Metadata["threshold_ms"], fmt.Sprintf("%d", tt.wantThreshold))
			}
		})
	}
}

func TestParseSlowThreshold(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", defaultSlowThresholdMS},
		{"  ", defaultSlowThresholdMS},
		{"abc", defaultSlowThresholdMS},
		{"-1", defaultSlowThresholdMS},
		{"0", defaultSlowThresholdMS},
		{"500", 500},
		{"  2000  ", 2000},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("input=%q", tt.input), func(t *testing.T) {
			got := parseSlowThreshold(tt.input)
			if got != tt.want {
				t.Errorf("parseSlowThreshold(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}
