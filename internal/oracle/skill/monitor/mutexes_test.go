/*-------------------------------------------------------------------------
 *
 * mutexes_test.go
 *	  Test cases for mutexes.go (monitor package):
 *	  TestMutexesSkill_Metadata, TestMutexesSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/mutexes_test.go
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

func TestMutexesSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewMutexesSkill(drv)

	if s.Name() != "mutexes" {
		t.Errorf("Name() = %q, want %q", s.Name(), "mutexes")
	}
	if s.Description() != "Show mutex contention" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show mutex contention")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "mutexes" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "mutexes")
	}
	if s.CLIDef().Command != "mutexes" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "mutexes")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestMutexesSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns mutexes with contention",
			queryResult: &db.QueryResult{
				Columns: []string{"mutex_type", "location", "sleeps", "wait_time"},
				Rows: [][]any{
					{"Cursor Pin", "kksLockDelete [KKSCHLPIN6]", 500, 12000},
					{"Library Cache", "kglhdgn2 106", 200, 5000},
					{"Cursor Parent", "kksfbc [KKSCHLPIN2]", 50, 1200},
				},
				Duration: 20 * time.Millisecond,
			},
			wantRows:    3,
			wantSummary: "Mutex 争用 — 3 个",
		},
		{
			name: "no mutex contention",
			queryResult: &db.QueryResult{
				Columns:  []string{"mutex_type", "location", "sleeps", "wait_time"},
				Rows:     [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "Mutex 争用 — 0 个",
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
				if sql != mutexesSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewMutexesSkill(drv)
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
