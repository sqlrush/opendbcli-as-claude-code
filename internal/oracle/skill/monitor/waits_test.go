/*-------------------------------------------------------------------------
 *
 * waits_test.go
 *	  Test cases for waits.go (monitor package):
 *	  TestWaitsSkill_Metadata, TestWaitsSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/waits_test.go
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

func TestWaitsSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewWaitsSkill(drv)

	if s.Name() != "waits" {
		t.Errorf("Name() = %q, want %q", s.Name(), "waits")
	}
	if s.Description() != "Show non-idle wait events" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show non-idle wait events")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "waits" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "waits")
	}
	if s.CLIDef().Command != "waits" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "waits")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestWaitsSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns wait events",
			queryResult: &db.QueryResult{
				Columns: []string{"event", "wait_class", "total_waits", "time_waited_sec", "avg_wait_ms"},
				Rows: [][]any{
					{"db file sequential read", "User I/O", 150000, 45.23, 0.30},
					{"log file sync", "Commit", 80000, 12.50, 0.16},
					{"buffer busy waits", "Concurrency", 5000, 3.10, 0.62},
				},
				Duration: 40 * time.Millisecond,
			},
			wantRows:    3,
			wantSummary: "非Idle等待事件 Top 30 — 3 个",
		},
		{
			name: "no wait events",
			queryResult: &db.QueryResult{
				Columns:  []string{"event", "wait_class", "total_waits", "time_waited_sec", "avg_wait_ms"},
				Rows:     [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "非Idle等待事件 Top 30 — 0 个",
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
				if sql != waitsSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewWaitsSkill(drv)
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
