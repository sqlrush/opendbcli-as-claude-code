/*-------------------------------------------------------------------------
 *
 * locks_test.go
 *	  Test cases for locks.go (monitor package):
 *	  TestLocksSkill_Metadata, TestLocksSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/locks_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestLocksSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewLocksSkill(drv)

	if s.Name() != "locks" {
		t.Errorf("Name() = %q, want %q", s.Name(), "locks")
	}
	if s.Description() != "Show row and table locks" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show row and table locks")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "locks" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "locks")
	}
	if s.CLIDef().Command != "locks" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "locks")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestLocksSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns locks",
			queryResult: &db.QueryResult{
				Columns: []string{"sid", "type", "lmode", "request", "block", "username", "program", "object_name"},
				Rows: [][]any{
					{101, "TX", 6, 0, 1, "APP_USER", "jdbc", "ORDERS"},
					{205, "TM", 3, 0, 0, "APP_USER", "jdbc", "ORDERS"},
				},
				Duration: 35 * time.Millisecond,
			},
			wantRows:    2,
			wantSummary: "锁等待 (TX/TM) — 2 个",
		},
		{
			name: "no locks",
			queryResult: &db.QueryResult{
				Columns:  []string{"sid", "type", "lmode", "request", "block", "username", "program", "object_name"},
				Rows:     [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "0 locks",
		},
		{
			name:     "query error",
			queryErr: errors.New("ORA-01031: insufficient privileges"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if sql != locksSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewLocksSkill(drv)
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
			if tt.wantRows == 0 {
				if result.Type != skill.ResultText {
					t.Errorf("Type = %v, want ResultText for empty result", result.Type)
				}
				if result.Summary != tt.wantSummary {
					t.Errorf("Summary = %q, want %q", result.Summary, tt.wantSummary)
				}
				return
			}
			if result.Type != skill.ResultTable {
				t.Errorf("Type = %v, want ResultTable", result.Type)
			}
			qr, ok := result.Data.(*db.QueryResult)
			if !ok {
				t.Fatalf("Data type = %T, want *db.QueryResult", result.Data)
			}
			if len(qr.Rows) != tt.wantRows {
				t.Errorf("row count = %d, want %d", len(qr.Rows), tt.wantRows)
			}
			if result.Summary != tt.wantSummary {
				t.Errorf("Summary = %q, want %q", result.Summary, tt.wantSummary)
			}
		})
	}
}
