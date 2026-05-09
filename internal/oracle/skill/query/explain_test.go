/*-------------------------------------------------------------------------
 *
 * explain_test.go
 *	  Test cases for explain.go (query package):
 *	  TestExplainSkill_Metadata, TestExplainSkill_Validate,
 *	  TestExplainSkill_Execute_SQLMode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/explain_test.go
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

func TestExplainSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewExplainSkill(drv)

	if s.Name() != "explain" {
		t.Errorf("Name() = %q, want %q", s.Name(), "explain")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "explain" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "explain")
	}
	if s.CLIDef().Command != "explain" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "explain")
	}
}

func TestExplainSkill_Validate(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "empty args",
			params:  map[string]any{"args": ""},
			wantErr: true,
		},
		{
			name:    "missing args",
			params:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "whitespace only",
			params:  map[string]any{"args": "   "},
			wantErr: true,
		},
		{
			name:    "valid SQL",
			params:  map[string]any{"args": "SELECT * FROM dual"},
			wantErr: false,
		},
		{
			name:    "valid SQL ID",
			params:  map[string]any{"args": "abc123def"},
			wantErr: false,
		},
	}

	drv := mock.NewMockDriver()
	s := NewExplainSkill(drv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(skill.ParamsFromMap(tt.params))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestExplainSkill_Execute_SQLMode(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		execErr    error
		queryErr   error
		planRows   [][]any
		wantErr    bool
		wantInText string
	}{
		{
			name: "successful explain for SELECT",
			args: "SELECT * FROM employees WHERE salary > 50000",
			planRows: [][]any{
				{"Plan hash value: 12345"},
				{"| Id | Operation         | Name      |"},
				{"|  0 | SELECT STATEMENT  |           |"},
				{"|  1 |  TABLE ACCESS FULL| EMPLOYEES |"},
			},
			wantInText: "TABLE ACCESS FULL",
		},
		{
			name:    "exec error",
			args:    "SELECT * FROM nonexistent",
			execErr: fmt.Errorf("ORA-00942"),
			wantErr: true,
		},
		{
			name:     "query error after exec succeeds",
			args:     "SELECT 1 FROM dual",
			queryErr: fmt.Errorf("plan display failed"),
			wantErr:  true,
		},
		{
			name:       "empty plan output",
			args:       "SELECT 1 FROM dual",
			planRows:   [][]any{},
			wantInText: "(no plan output)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			var capturedExecSQL string
			drv.ExecFunc = func(_ context.Context, sql string, _ ...any) (*db.ExecResult, error) {
				capturedExecSQL = sql
				if tt.execErr != nil {
					return nil, tt.execErr
				}
				return &db.ExecResult{Duration: 10 * time.Millisecond}, nil
			}
			drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return &db.QueryResult{
					Columns:  []string{"PLAN_TABLE_OUTPUT"},
					Rows:     tt.planRows,
					Duration: 5 * time.Millisecond,
				}, nil
			}

			s := NewExplainSkill(drv)
			params := skill.ParamsFromMap(map[string]any{"args": tt.args})

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

			// Verify EXPLAIN PLAN FOR was prepended.
			if !strings.HasPrefix(capturedExecSQL, "EXPLAIN PLAN FOR ") {
				t.Errorf("exec SQL = %q, want prefix 'EXPLAIN PLAN FOR '", capturedExecSQL)
			}

			if result.Type != skill.ResultText {
				t.Errorf("result type = %v, want ResultText", result.Type)
			}
			if result.Metadata["mode"] != "sql" {
				t.Errorf("metadata mode = %q, want %q", result.Metadata["mode"], "sql")
			}

			data, ok := result.Data.(*ExplainData)
			if !ok {
				t.Fatal("result data is not *ExplainData")
			}
			if data.Mode != "sql" {
				t.Errorf("ExplainData.Mode = %q, want %q", data.Mode, "sql")
			}
			if !strings.Contains(result.Rendered, tt.wantInText) {
				t.Errorf("Rendered %q does not contain %q", result.Rendered, tt.wantInText)
			}
		})
	}
}

func TestExplainSkill_Execute_SQLIDMode(t *testing.T) {
	tests := []struct {
		name       string
		sqlID      string
		queryErr   error
		planRows   [][]any
		wantErr    bool
		wantInText string
	}{
		{
			name:  "successful cursor plan",
			sqlID: "abc123def",
			planRows: [][]any{
				{"SQL_ID: abc123def"},
				{"| Id | Operation | Name |"},
				{"|  0 | SELECT    |      |"},
			},
			wantInText: "abc123def",
		},
		{
			name:     "query error",
			sqlID:    "xyz789",
			queryErr: fmt.Errorf("cursor not found"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			var capturedQueryArgs []any
			drv.QueryFunc = func(_ context.Context, sql string, args ...any) (*db.QueryResult, error) {
				capturedQueryArgs = args
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return &db.QueryResult{
					Columns:  []string{"PLAN_TABLE_OUTPUT"},
					Rows:     tt.planRows,
					Duration: 5 * time.Millisecond,
				}, nil
			}

			s := NewExplainSkill(drv)
			params := skill.ParamsFromMap(map[string]any{"args": tt.sqlID})

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

			// Verify SQL ID was passed as query arg.
			if len(capturedQueryArgs) != 1 || capturedQueryArgs[0] != tt.sqlID {
				t.Errorf("query args = %v, want [%s]", capturedQueryArgs, tt.sqlID)
			}

			if result.Type != skill.ResultText {
				t.Errorf("result type = %v, want ResultText", result.Type)
			}
			if result.Metadata["mode"] != "sql_id" {
				t.Errorf("metadata mode = %q, want %q", result.Metadata["mode"], "sql_id")
			}
			if result.Metadata["sql_id"] != tt.sqlID {
				t.Errorf("metadata sql_id = %q, want %q", result.Metadata["sql_id"], tt.sqlID)
			}

			data, ok := result.Data.(*ExplainData)
			if !ok {
				t.Fatal("result data is not *ExplainData")
			}
			if data.Mode != "sql_id" {
				t.Errorf("ExplainData.Mode = %q, want %q", data.Mode, "sql_id")
			}
			if data.SQLID != tt.sqlID {
				t.Errorf("ExplainData.SQLID = %q, want %q", data.SQLID, tt.sqlID)
			}
			if !strings.Contains(result.Rendered, tt.wantInText) {
				t.Errorf("Rendered %q does not contain %q", result.Rendered, tt.wantInText)
			}
		})
	}
}

func TestLooksLikeSQL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"SELECT * FROM dual", true},
		{"select count(*) from t", true},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", true},
		{"INSERT INTO t VALUES (1)", true},
		{"abc123def", false},
		{"g4nz12bfmqz1h", false},
		{"singleword", false},
		{"", false},
		{"UNKNOWN something else", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeSQL(tt.input)
			if got != tt.want {
				t.Errorf("looksLikeSQL(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestDetectFTS(t *testing.T) {
	tests := []struct {
		name       string
		lines      []string
		wantTables []string
	}{
		{
			name: "single FTS",
			lines: []string{
				"| Id | Operation          | Name      |",
				"|  0 | SELECT STATEMENT   |           |",
				"|  1 |  TABLE ACCESS FULL | EMPLOYEES |",
			},
			wantTables: []string{"EMPLOYEES"},
		},
		{
			name: "multiple FTS",
			lines: []string{
				"|  1 |  TABLE ACCESS FULL | ORDERS    |",
				"|  2 |  TABLE ACCESS FULL | CUSTOMERS |",
			},
			wantTables: []string{"ORDERS", "CUSTOMERS"},
		},
		{
			name: "no FTS",
			lines: []string{
				"|  1 |  INDEX RANGE SCAN  | IDX_EMP   |",
			},
			wantTables: nil,
		},
		{
			name:       "empty lines",
			lines:      []string{},
			wantTables: nil,
		},
		{
			name: "duplicate table",
			lines: []string{
				"|  1 |  TABLE ACCESS FULL | T1 |",
				"|  2 |  TABLE ACCESS FULL | T1 |",
			},
			wantTables: []string{"T1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectFTS(tt.lines)
			if len(got) != len(tt.wantTables) {
				t.Fatalf("detectFTS() = %v, want %v", got, tt.wantTables)
			}
			for i, g := range got {
				if g != tt.wantTables[i] {
					t.Errorf("detectFTS()[%d] = %q, want %q", i, g, tt.wantTables[i])
				}
			}
		})
	}
}

func TestFTSWarning(t *testing.T) {
	lines := []string{
		"|  1 |  TABLE ACCESS FULL | EMPLOYEES |",
	}
	warning := ftsWarning(lines)
	if !strings.Contains(warning, "全表扫描") {
		t.Error("should contain 全表扫描 warning")
	}
	if !strings.Contains(warning, "EMPLOYEES") {
		t.Error("should contain table name EMPLOYEES")
	}
	if !strings.Contains(warning, "/indexadvise") {
		t.Error("should contain /indexadvise hint")
	}

	// No FTS
	noFTS := ftsWarning([]string{"|  1 |  INDEX SCAN | IDX1 |"})
	if noFTS != "" {
		t.Errorf("expected empty warning for no FTS, got: %q", noFTS)
	}
}

func TestExtractLines(t *testing.T) {
	tests := []struct {
		name     string
		qr       *db.QueryResult
		wantText string
	}{
		{
			name:     "nil result",
			qr:       nil,
			wantText: "(no plan output)",
		},
		{
			name:     "empty rows",
			qr:       &db.QueryResult{Rows: [][]any{}},
			wantText: "(no plan output)",
		},
		{
			name: "multiple rows",
			qr: &db.QueryResult{
				Rows: [][]any{{"line1"}, {"line2"}, {"line3"}},
			},
			wantText: "line1\nline2\nline3",
		},
		{
			name: "nil cell",
			qr: &db.QueryResult{
				Rows: [][]any{{"line1"}, {nil}, {"line3"}},
			},
			wantText: "line1\n\nline3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := extractLines(tt.qr)
			got := strings.Join(lines, "\n")
			if got != tt.wantText {
				t.Errorf("extractLines() joined = %q, want %q", got, tt.wantText)
			}
		})
	}
}
