/*-------------------------------------------------------------------------
 *
 * latches_test.go
 *	  Test cases for latches.go (monitor package):
 *	  TestLatchesSkill_Metadata, TestLatchesSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/latches_test.go
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

func TestLatchesSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewLatchesSkill(drv)

	if s.Name() != "latches" {
		t.Errorf("Name() = %q, want %q", s.Name(), "latches")
	}
	if s.Description() != "Show latch contention" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show latch contention")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "latches" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "latches")
	}
	if s.CLIDef().Command != "latches" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "latches")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestLatchesSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns latches with contention",
			queryResult: &db.QueryResult{
				Columns: []string{"name", "gets", "misses", "sleeps", "spin_gets", "miss_ratio"},
				Rows: [][]any{
					{"cache buffers chains", 5000000, 1500, 200, 1300, 0.03},
					{"shared pool", 2000000, 800, 50, 750, 0.04},
				},
				Duration: 25 * time.Millisecond,
			},
			wantRows:    2,
			wantSummary: "Latch 争用 Top 30 — 2 个",
		},
		{
			name: "no latch contention",
			queryResult: &db.QueryResult{
				Columns:  []string{"name", "gets", "misses", "sleeps", "spin_gets", "miss_ratio"},
				Rows:     [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "Latch 争用 Top 30 — 0 个",
		},
		{
			name:     "query error",
			queryErr: errors.New("ORA-00942: table or view does not exist"),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drv := mock.NewMockDriver()
			drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if sql != latchesSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewLatchesSkill(drv)
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
