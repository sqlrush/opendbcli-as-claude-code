/*-------------------------------------------------------------------------
 *
 * tableinfo_test.go
 *	  Test cases for tableinfo.go (schema package):
 *	  TestTableInfoSkill_Metadata, TestTableInfoSkill_Validate,
 *	  TestParseTableRef.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/schema/tableinfo_test.go
 *
 *-------------------------------------------------------------------------
 */
package schema

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestTableInfoSkill_Metadata(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())

	if s.Name() != "tableinfo" {
		t.Errorf("Name() = %q, want %q", s.Name(), "tableinfo")
	}
	if s.Description() != "Show detailed table information" {
		t.Errorf("Description() = %q", s.Description())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	td := s.ToolDef()
	if td.Name != "tableinfo" {
		t.Errorf("ToolDef().Name = %q", td.Name)
	}

	cd := s.CLIDef()
	if cd.Command != "tableinfo" {
		t.Errorf("CLIDef().Command = %q", cd.Command)
	}
}

func TestTableInfoSkill_Validate(t *testing.T) {
	s := NewTableInfoSkill(mock.NewMockDriver())

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
			name:    "valid table name",
			params:  map[string]any{"args": "ORDERS"},
			wantErr: false,
		},
		{
			name:    "valid schema.table",
			params:  map[string]any{"args": "HR.EMPLOYEES"},
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

func TestParseTableRef(t *testing.T) {
	tests := []struct {
		input     string
		wantTable string
		wantOwner string
	}{
		{"ORDERS", "ORDERS", ""},
		{"HR.EMPLOYEES", "EMPLOYEES", "HR"},
		{"SYS.DBA_TABLES", "DBA_TABLES", "SYS"},
		{"tablename", "tablename", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			table, owner := parseTableRef(tt.input)
			if table != tt.wantTable {
				t.Errorf("table = %q, want %q", table, tt.wantTable)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner, tt.wantOwner)
			}
		})
	}
}

func TestTableInfoSkill_Execute(t *testing.T) {
	tests := []struct {
		name       string
		params     map[string]any
		queryFunc  func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantSubs   []string
		wantErr    bool
		wantErrSub string
	}{
		{
			name:   "simple table with all sections populated",
			params: map[string]any{"args": "ORDERS"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				switch {
				case strings.Contains(sql, "dba_tab_columns"):
					return &db.QueryResult{
						Columns:  []string{"COLUMN_NAME", "DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DATA_DEFAULT"},
						Rows:     [][]any{{"ORDER_ID", "NUMBER", 22, 10, 0, "N", nil}},
						Duration: time.Millisecond,
					}, nil
				case strings.Contains(sql, "dba_indexes"):
					return &db.QueryResult{
						Columns:  []string{"INDEX_NAME", "UNIQUENESS", "STATUS", "COLUMNS"},
						Rows:     [][]any{{"PK_ORDERS", "UNIQUE", "VALID", "ORDER_ID"}},
						Duration: time.Millisecond,
					}, nil
				case strings.Contains(sql, "dba_tables"):
					return &db.QueryResult{
						Columns:  []string{"NUM_ROWS", "BLOCKS", "AVG_ROW_LEN", "LAST_ANALYZED"},
						Rows:     [][]any{{1000, 50, 120, "2025-01-15"}},
						Duration: time.Millisecond,
					}, nil
				case strings.Contains(sql, "dba_constraints"):
					return &db.QueryResult{
						Columns:  []string{"CONSTRAINT_NAME", "CONSTRAINT_TYPE", "STATUS", "SEARCH_CONDITION", "R_CONSTRAINT_NAME"},
						Rows:     [][]any{{"PK_ORDERS", "P", "ENABLED", nil, nil}},
						Duration: time.Millisecond,
					}, nil
				default:
					return &db.QueryResult{}, nil
				}
			},
			wantSubs: []string{
				"── Columns ──",
				"ORDER_ID",
				"── Indexes ──",
				"PK_ORDERS",
				"── Table Statistics ──",
				"1000",
				"── Constraints ──",
				"ENABLED",
			},
		},
		{
			name:   "schema.table format",
			params: map[string]any{"args": "HR.EMPLOYEES"},
			queryFunc: func(_ context.Context, sql string, args ...any) (*db.QueryResult, error) {
				// Verify owner arg is passed correctly.
				if len(args) >= 2 {
					if owner, ok := args[1].(string); ok && owner != "HR" {
						return nil, errors.New("unexpected owner: " + owner)
					}
				}
				return &db.QueryResult{
					Columns: []string{"COL"},
					Rows:    [][]any{{"test_value"}},
				}, nil
			},
			wantSubs: []string{
				"── Columns ──",
				"── Indexes ──",
				"── Table Statistics ──",
				"── Constraints ──",
				"test_value",
			},
		},
		{
			name:   "empty results for all sections",
			params: map[string]any{"args": "NONEXISTENT"},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{Columns: []string{"COL"}}, nil
			},
			wantSubs: []string{
				"(no columns found)",
				"(no indexes found)",
				"(no statistics found)",
				"(no constraints found)",
			},
		},
		{
			name:   "columns query error",
			params: map[string]any{"args": "ORDERS"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "dba_tab_columns") {
					return nil, errors.New("permission denied")
				}
				return &db.QueryResult{}, nil
			},
			wantErr:    true,
			wantErrSub: "querying columns",
		},
		{
			name:   "indexes query error",
			params: map[string]any{"args": "ORDERS"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "dba_indexes") {
					return nil, errors.New("connection lost")
				}
				return &db.QueryResult{Columns: []string{"COL"}, Rows: [][]any{{"v"}}}, nil
			},
			wantErr:    true,
			wantErrSub: "querying indexes",
		},
		{
			name:   "stats query error",
			params: map[string]any{"args": "ORDERS"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "dba_tables") {
					return nil, errors.New("timeout")
				}
				return &db.QueryResult{Columns: []string{"COL"}, Rows: [][]any{{"v"}}}, nil
			},
			wantErr:    true,
			wantErrSub: "querying table stats",
		},
		{
			name:   "constraints query error",
			params: map[string]any{"args": "ORDERS"},
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "dba_constraints") {
					return nil, errors.New("timeout")
				}
				return &db.QueryResult{Columns: []string{"COL"}, Rows: [][]any{{"v"}}}, nil
			},
			wantErr:    true,
			wantErrSub: "querying constraints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewTableInfoSkill(d)

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
			rendered := result.Rendered
			for _, sub := range tt.wantSubs {
				if !strings.Contains(rendered, sub) {
					t.Errorf("output does not contain %q\noutput:\n%s", sub, rendered)
				}
			}
		})
	}
}

func TestTableInfoSkill_StructuredData(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case strings.Contains(sql, "dba_tab_columns"):
			return &db.QueryResult{
				Columns: []string{"COLUMN_NAME", "DATA_TYPE", "DATA_LENGTH", "DATA_PRECISION", "DATA_SCALE", "NULLABLE", "DATA_DEFAULT"},
				Rows:    [][]any{{"ID", "NUMBER", 22, 10, 0, "N", nil}},
			}, nil
		case strings.Contains(sql, "dba_indexes"):
			return &db.QueryResult{
				Columns: []string{"INDEX_NAME", "UNIQUENESS", "STATUS", "COLUMNS"},
				Rows:    [][]any{{"PK_T", "UNIQUE", "VALID", "ID"}},
			}, nil
		case strings.Contains(sql, "dba_tables"):
			return &db.QueryResult{
				Columns: []string{"NUM_ROWS", "BLOCKS", "AVG_ROW_LEN", "LAST_ANALYZED"},
				Rows:    [][]any{{500, 20, 80, "2025-06-01"}},
			}, nil
		case strings.Contains(sql, "dba_constraints"):
			return &db.QueryResult{
				Columns: []string{"CONSTRAINT_NAME", "CONSTRAINT_TYPE", "STATUS", "SEARCH_CONDITION", "R_CONSTRAINT_NAME"},
				Rows:    [][]any{{"PK_T", "P", "ENABLED", nil, nil}},
			}, nil
		default:
			return &db.QueryResult{}, nil
		}
	}
	s := NewTableInfoSkill(d)
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "T"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, ok := result.Data.(*TableInfoData)
	if !ok {
		t.Fatalf("Data is %T, want *TableInfoData", result.Data)
	}
	if data.Table != "T" {
		t.Errorf("Table = %q, want T", data.Table)
	}
	if len(data.Columns) != 1 || data.Columns[0].Name != "ID" {
		t.Errorf("Columns = %v", data.Columns)
	}
	if len(data.Indexes) != 1 || data.Indexes[0].Name != "PK_T" {
		t.Errorf("Indexes = %v", data.Indexes)
	}
	if data.Stats == nil || data.Stats.NumRows != "500" {
		t.Errorf("Stats = %v", data.Stats)
	}
	if len(data.Constraints) != 1 || data.Constraints[0].Name != "PK_T" {
		t.Errorf("Constraints = %v", data.Constraints)
	}
}

func TestTableInfoSkill_StructuredDataEmpty(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{Columns: []string{"COL"}}, nil
	}
	s := NewTableInfoSkill(d)
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "EMPTY"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, ok := result.Data.(*TableInfoData)
	if !ok {
		t.Fatalf("Data is %T, want *TableInfoData", result.Data)
	}
	// Empty slices should be [] not null in JSON.
	if data.Columns == nil {
		t.Error("Columns should be empty slice, not nil")
	}
	if data.Indexes == nil {
		t.Error("Indexes should be empty slice, not nil")
	}
	if data.Constraints == nil {
		t.Error("Constraints should be empty slice, not nil")
	}
	if data.Stats != nil {
		t.Error("Stats should be nil for empty rows")
	}
}

func TestFormatRow(t *testing.T) {
	tests := []struct {
		name    string
		columns []string
		row     []any
		want    string
	}{
		{
			name:    "single column",
			columns: []string{"NAME"},
			row:     []any{"hello"},
			want:    "NAME=hello",
		},
		{
			name:    "multiple columns",
			columns: []string{"A", "B"},
			row:     []any{1, "two"},
			want:    "A=1 | B=two",
		},
		{
			name:    "nil value",
			columns: []string{"X"},
			row:     []any{nil},
			want:    "X=",
		},
		{
			name:    "more columns than values",
			columns: []string{"A", "B", "C"},
			row:     []any{"only_one"},
			want:    "A=only_one | B= | C=",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRow(tt.columns, tt.row)
			if got != tt.want {
				t.Errorf("formatRow() = %q, want %q", got, tt.want)
			}
		})
	}
}
