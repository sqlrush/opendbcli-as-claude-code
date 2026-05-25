/*-------------------------------------------------------------------------
 *
 * space_test.go
 *	  Test cases for space.go (admin package): TestSpaceSkill_Metadata,
 *	  TestSpaceSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/space_test.go
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

func TestSpaceSkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewSpaceSkill(d)

	if s.Name() != "space" {
		t.Errorf("Name() = %q, want %q", s.Name(), "space")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "space" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "space")
	}
	if s.CLIDef().Command != "space" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "space")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestSpaceSkill_Execute(t *testing.T) {
	tests := []struct {
		name      string
		queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantRows  int
		wantErr   bool
	}{
		{
			name: "returns tablespace data",
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{
					Columns:  []string{"TABLESPACE_NAME", "USED_MB", "TOTAL_MB", "USED_PCT"},
					Rows:     [][]any{{"SYSTEM", 1024.5, 2048.0, 50.0}, {"USERS", 512.0, 1024.0, 50.0}},
					Duration: 10 * time.Millisecond,
				}, nil
			},
			wantRows: 2,
		},
		{
			name: "empty result",
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return &db.QueryResult{
					Columns: []string{"TABLESPACE_NAME", "USED_MB", "TOTAL_MB", "USED_PCT"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows: 0,
		},
		{
			name: "query error",
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return nil, errors.New("ORA-00942: table or view does not exist")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewSpaceSkill(d)

			result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
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
				t.Errorf("Type = %v, want ResultText", result.Type)
			}
			qr, ok := result.Data.(*db.QueryResult)
			if !ok {
				t.Fatalf("Data is not *db.QueryResult: %T", result.Data)
			}
			if len(qr.Rows) != tt.wantRows {
				t.Errorf("got %d rows, want %d", len(qr.Rows), tt.wantRows)
			}
			if !strings.Contains(result.Summary, "表空间") {
				t.Errorf("Summary %q should mention 表空间", result.Summary)
			}
			if result.Rendered == "" {
				t.Error("Rendered should not be empty")
			}
		})
	}
}
