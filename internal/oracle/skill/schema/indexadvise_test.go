/*-------------------------------------------------------------------------
 *
 * indexadvise_test.go
 *	  Test cases for indexadvise.go (schema package):
 *	  TestIndexAdviseSkill_Metadata, TestIndexAdviseSkill_Validate,
 *	  TestIsSQLText.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/schema/indexadvise_test.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestIndexAdviseSkill_Metadata(t *testing.T) {
	s := NewIndexAdviseSkill(mock.NewMockDriver())

	if s.Name() != "indexadvise" {
		t.Errorf("Name() = %q, want %q", s.Name(), "indexadvise")
	}
	if s.Description() != "Analyze SQL and recommend indexes" {
		t.Errorf("Description() = %q", s.Description())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	td := s.ToolDef()
	if td.Name != "indexadvise" {
		t.Errorf("ToolDef().Name = %q", td.Name)
	}

	cd := s.CLIDef()
	if cd.Command != "indexadvise" {
		t.Errorf("CLIDef().Command = %q", cd.Command)
	}
}

func TestIndexAdviseSkill_Validate(t *testing.T) {
	s := NewIndexAdviseSkill(mock.NewMockDriver())

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "missing args",
			params:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "empty args",
			params:  map[string]any{"args": ""},
			wantErr: true,
		},
		{
			name:    "whitespace only",
			params:  map[string]any{"args": "   "},
			wantErr: true,
		},
		{
			name:    "valid SQL text",
			params:  map[string]any{"args": "SELECT * FROM orders"},
			wantErr: false,
		},
		{
			name:    "valid SQL ID",
			params:  map[string]any{"args": "abc123def"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(skill.ParamsFromMap(tt.params))
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestIsSQLText(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"abc123def", false},
		{"sqlid_no_spaces", false},
		{"SELECT * FROM dual", true},
		{"select 1 from dual", true},
		{"some random text with spaces", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isSQLText(tt.input)
			if got != tt.want {
				t.Errorf("isSQLText(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestIndexAdviseSkill_Execute_SQLText(t *testing.T) {
	tests := []struct {
		name       string
		params     map[string]any
		execFunc   func(ctx context.Context, sql string, args ...any) (*db.ExecResult, error)
		queryFunc  func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantSubs   []string
		wantErr    bool
		wantErrSub string
	}{
		{
			name:   "SQL text with full table scan detected",
			params: map[string]any{"args": "SELECT * FROM orders WHERE status = 'OPEN'"},
			execFunc: func(_ context.Context, sql string, _ ...any) (*db.ExecResult, error) {
				if !strings.HasPrefix(sql, "EXPLAIN PLAN FOR") {
					return nil, errors.New("expected EXPLAIN PLAN FOR prefix")
				}
				return &db.ExecResult{}, nil
			},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{
					Columns: []string{"PLAN_TABLE_OUTPUT"},
					Rows: [][]any{
						{"Plan hash value: 123456"},
						{"|   0 | SELECT STATEMENT  |         |"},
						{"|   1 |  TABLE ACCESS FULL| ORDERS  |"},
					},
				}, nil
			},
			wantSubs: []string{
				"=== Execution Plan ===",
				"TABLE ACCESS FULL",
				"ORDERS",
				"=== Recommendations ===",
				"1 full table scan(s) detected",
				"Consider adding an index",
			},
		},
		{
			name:   "SQL text with no full scans",
			params: map[string]any{"args": "SELECT * FROM orders WHERE order_id = 1"},
			execFunc: func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
				return &db.ExecResult{}, nil
			},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{
					Columns: []string{"PLAN_TABLE_OUTPUT"},
					Rows: [][]any{
						{"Plan hash value: 789012"},
						{"|   0 | SELECT STATEMENT       |         |"},
						{"|   1 |  TABLE ACCESS BY INDEX | ORDERS  |"},
					},
				}, nil
			},
			wantSubs: []string{
				"=== Execution Plan ===",
				"=== Recommendations ===",
				"No full table scans detected",
			},
		},
		{
			name:   "explain plan fails",
			params: map[string]any{"args": "SELECT bad syntax"},
			execFunc: func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
				return nil, errors.New("ORA-00942: table or view does not exist")
			},
			wantErr:    true,
			wantErrSub: "explain plan failed",
		},
		{
			name:   "display plan query fails",
			params: map[string]any{"args": "SELECT * FROM dual"},
			execFunc: func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
				return &db.ExecResult{}, nil
			},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return nil, errors.New("plan table not found")
			},
			wantErr:    true,
			wantErrSub: "fetching plan display failed",
		},
		{
			name:   "multiple full scans",
			params: map[string]any{"args": "SELECT * FROM orders o JOIN items i ON o.id = i.order_id"},
			execFunc: func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
				return &db.ExecResult{}, nil
			},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{
					Columns: []string{"PLAN_TABLE_OUTPUT"},
					Rows: [][]any{
						{"|   1 |  TABLE ACCESS FULL| ORDERS  |"},
						{"|   2 |  TABLE ACCESS FULL| ITEMS   |"},
					},
				}, nil
			},
			wantSubs: []string{
				"2 full table scan(s) detected",
				"Table ORDERS",
				"Table ITEMS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.ExecFunc = tt.execFunc
			d.QueryFunc = tt.queryFunc
			s := NewIndexAdviseSkill(d)

			result, err := s.Execute(context.Background(), skill.ParamsFromMap(tt.params))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.wantErrSub != "" && !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Type != skill.ResultText {
				t.Errorf("want ResultText, got %v", result.Type)
			}
			if result.Rendered == "" {
				t.Fatal("Rendered is empty")
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(result.Rendered, sub) {
					t.Errorf("output does not contain %q\noutput:\n%s", sub, result.Rendered)
				}
			}
			adviceData, ok := result.Data.(*IndexAdviceData)
			if !ok {
				t.Fatalf("Data is not *IndexAdviceData: %T", result.Data)
			}
			if adviceData.Mode != "sql" {
				t.Errorf("Data.Mode = %q, want %q", adviceData.Mode, "sql")
			}
		})
	}
}

func TestIndexAdviseSkill_Execute_SQLID(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantSubs  []string
		wantErr   bool
	}{
		{
			name:   "SQL ID with full scan and predicates",
			params: map[string]any{"args": "abc123def"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				switch {
				case strings.Contains(sql, "DISPLAY_CURSOR"):
					return &db.QueryResult{
						Columns: []string{"PLAN_TABLE_OUTPUT"},
						Rows: [][]any{
							{"|   1 |  TABLE ACCESS FULL| CUSTOMERS |"},
						},
					}, nil
				case strings.Contains(sql, "v$sql_plan"):
					return &db.QueryResult{
						Columns: []string{"FILTER_PREDICATES", "ACCESS_PREDICATES"},
						Rows:    [][]any{{"\"STATUS\"='ACTIVE'", nil}},
					}, nil
				default:
					return &db.QueryResult{}, nil
				}
			},
			wantSubs: []string{
				"=== Execution Plan ===",
				"TABLE ACCESS FULL",
				"CUSTOMERS",
				"=== Full Scan Predicates ===",
				"Filter: \"STATUS\"='ACTIVE'",
				"=== Recommendations ===",
				"1 full table scan(s) detected",
			},
		},
		{
			name:   "SQL ID no full scans",
			params: map[string]any{"args": "xyz789"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "DISPLAY_CURSOR") {
					return &db.QueryResult{
						Columns: []string{"PLAN_TABLE_OUTPUT"},
						Rows: [][]any{
							{"|   0 | SELECT STATEMENT |         |"},
							{"|   1 |  INDEX RANGE SCAN| IDX_ORD |"},
						},
					}, nil
				}
				return &db.QueryResult{}, nil
			},
			wantSubs: []string{
				"No full table scans detected",
			},
		},
		{
			name:   "cursor plan query fails",
			params: map[string]any{"args": "badid999"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "DISPLAY_CURSOR") {
					return nil, errors.New("cursor not found")
				}
				return &db.QueryResult{}, nil
			},
			wantErr: true,
		},
		{
			name:   "predicate query fails gracefully",
			params: map[string]any{"args": "pred_fail_id"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "DISPLAY_CURSOR") {
					return &db.QueryResult{
						Columns: []string{"PLAN_TABLE_OUTPUT"},
						Rows: [][]any{
							{"|   1 |  TABLE ACCESS FULL| ORDERS |"},
						},
					}, nil
				}
				if strings.Contains(sql, "v$sql_plan") {
					return nil, errors.New("insufficient privileges")
				}
				return &db.QueryResult{}, nil
			},
			wantSubs: []string{
				"1 full table scan(s) detected",
				"Table ORDERS",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewIndexAdviseSkill(d)

			result, err := s.Execute(context.Background(), skill.ParamsFromMap(tt.params))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Type != skill.ResultText {
				t.Errorf("want ResultText, got %v", result.Type)
			}
			if result.Rendered == "" {
				t.Fatal("Rendered is empty")
			}
			for _, sub := range tt.wantSubs {
				if !strings.Contains(result.Rendered, sub) {
					t.Errorf("output does not contain %q\noutput:\n%s", sub, result.Rendered)
				}
			}
			adviceData, ok := result.Data.(*IndexAdviceData)
			if !ok {
				t.Fatalf("Data is not *IndexAdviceData: %T", result.Data)
			}
			if adviceData.Mode != "sql_id" {
				t.Errorf("Data.Mode = %q, want %q", adviceData.Mode, "sql_id")
			}
		})
	}
}

func TestExtractPlanLines(t *testing.T) {
	tests := []struct {
		name  string
		input *db.QueryResult
		want  []string
	}{
		{
			name:  "nil result",
			input: nil,
			want:  []string{"(no plan output)"},
		},
		{
			name:  "empty rows",
			input: &db.QueryResult{Rows: [][]any{}},
			want:  []string{"(no plan output)"},
		},
		{
			name: "rows with nil values only",
			input: &db.QueryResult{
				Rows: [][]any{{nil}, {nil}},
			},
			want: []string{"(no plan output)"},
		},
		{
			name: "normal rows",
			input: &db.QueryResult{
				Rows: [][]any{{"line1"}, {"line2"}},
			},
			want: []string{"line1", "line2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPlanLines(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d lines, want %d", len(got), len(tt.want))
			}
			for i, line := range got {
				if line != tt.want[i] {
					t.Errorf("line[%d] = %q, want %q", i, line, tt.want[i])
				}
			}
		})
	}
}

func TestFindFullScans(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{
			name:  "no full scans",
			lines: []string{"|  INDEX RANGE SCAN | IDX_A |"},
			want:  nil,
		},
		{
			name:  "one full scan",
			lines: []string{"|  TABLE ACCESS FULL| ORDERS |"},
			want:  []string{"ORDERS"},
		},
		{
			name: "multiple full scans",
			lines: []string{
				"|  TABLE ACCESS FULL| ORDERS  |",
				"|  INDEX SCAN       | IDX_X   |",
				"|  TABLE ACCESS FULL| ITEMS   |",
			},
			want: []string{"ORDERS", "ITEMS"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findFullScans(tt.lines)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i, name := range got {
				if name != tt.want[i] {
					t.Errorf("table[%d] = %q, want %q", i, name, tt.want[i])
				}
			}
		})
	}
}

func TestExtractTableName(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			name: "standard format",
			line: "|   1 |  TABLE ACCESS FULL| ORDERS  |  1000 |",
			want: "ORDERS",
		},
		{
			name: "with extra spaces",
			line: "|   3 |  TABLE ACCESS FULL |  EMPLOYEES  |  500 |",
			want: "EMPLOYEES",
		},
		{
			name: "no table name field",
			line: "TABLE ACCESS FULL",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTableName(tt.line)
			if got != tt.want {
				t.Errorf("extractTableName(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestAnyToString(t *testing.T) {
	tests := []struct {
		name string
		row  []any
		idx  int
		want string
	}{
		{"valid string", []any{"hello"}, 0, "hello"},
		{"nil value", []any{nil}, 0, ""},
		{"out of range", []any{"a"}, 5, ""},
		{"number", []any{42}, 0, "42"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyToString(tt.row, tt.idx)
			if got != tt.want {
				t.Errorf("anyToString() = %q, want %q", got, tt.want)
			}
		})
	}
}
