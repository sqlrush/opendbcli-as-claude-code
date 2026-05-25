/*-------------------------------------------------------------------------
 *
 * params_test.go
 *	  Test cases for params.go (admin package):
 *	  TestParamsSkill_Metadata, TestParamsSkill_Execute,
 *	  TestBuildParamPattern.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/params_test.go
 *
 *-------------------------------------------------------------------------
 */
package admin

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

func TestParamsSkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewParamsSkill(d)

	if s.Name() != "params" {
		t.Errorf("Name() = %q, want %q", s.Name(), "params")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "params" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "params")
	}
	if s.CLIDef().Command != "params" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "params")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestParamsSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		params      map[string]any
		queryFunc   func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantRows    int
		wantPattern string
		wantErr     bool
	}{
		{
			name:   "search with pattern",
			params: map[string]any{"args": "sga"},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				pattern, ok := args[0].(string)
				if !ok || pattern != "%sga%" {
					return nil, errors.New("expected pattern %sga%")
				}
				return &db.QueryResult{
					Columns:  []string{"NAME", "VALUE", "DESCRIPTION"},
					Rows:     [][]any{{"sga_target", "4G", "SGA target size"}},
					Duration: 5 * time.Millisecond,
				}, nil
			},
			wantRows:    1,
			wantPattern: "%sga%",
		},
		{
			name:   "no args shows all",
			params: map[string]any{},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				pattern, ok := args[0].(string)
				if !ok || pattern != "%" {
					return nil, errors.New("expected pattern %")
				}
				return &db.QueryResult{
					Columns: []string{"NAME", "VALUE", "DESCRIPTION"},
					Rows: [][]any{
						{"sga_target", "4G", "SGA target size"},
						{"pga_aggregate_target", "2G", "PGA target"},
					},
					Duration: 8 * time.Millisecond,
				}, nil
			},
			wantRows:    2,
			wantPattern: "%",
		},
		{
			name:   "empty string args shows all",
			params: map[string]any{"args": ""},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				pattern, ok := args[0].(string)
				if !ok || pattern != "%" {
					return nil, errors.New("expected pattern %")
				}
				return &db.QueryResult{
					Columns: []string{"NAME", "VALUE", "DESCRIPTION"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows:    0,
			wantPattern: "%",
		},
		{
			name:   "query error",
			params: map[string]any{"args": "sga"},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return nil, errors.New("connection lost")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewParamsSkill(d)

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
			if result.Type != skill.ResultTable {
				t.Errorf("Type = %v, want ResultTable", result.Type)
			}
			qr, ok := result.Data.(*db.QueryResult)
			if !ok {
				t.Fatalf("Data is not *db.QueryResult: %T", result.Data)
			}
			if len(qr.Rows) != tt.wantRows {
				t.Errorf("got %d rows, want %d", len(qr.Rows), tt.wantRows)
			}
			if tt.wantPattern != "" && result.Metadata["pattern"] != tt.wantPattern {
				t.Errorf("pattern metadata = %q, want %q", result.Metadata["pattern"], tt.wantPattern)
			}
		})
	}
}

func TestBuildParamPattern(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "%"},
		{"  ", "%"},
		{"sga", "%sga%"},
		{"memory", "%memory%"},
		{"  pga  ", "%pga%"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := buildParamPattern(tt.input)
			if got != tt.want {
				t.Errorf("buildParamPattern(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParamsSQL_IsConst(t *testing.T) {
	if !strings.Contains(paramsSQL, "v$parameter") {
		t.Error("paramsSQL should query v$parameter")
	}
}
