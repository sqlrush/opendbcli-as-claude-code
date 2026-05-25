/*-------------------------------------------------------------------------
 *
 * activesessions_test.go
 *	  Test cases for activesessions.go (monitor package):
 *	  TestActiveSessionsSkill_Metadata, TestActiveSessionsSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/activesessions_test.go
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

func TestActiveSessionsSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewActiveSessionsSkill(drv)

	if s.Name() != "activesessions" {
		t.Errorf("Name() = %q, want %q", s.Name(), "activesessions")
	}
	if s.Description() != "Show active database sessions" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show active database sessions")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "activesessions" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "activesessions")
	}
	if s.CLIDef().Command != "activesessions" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "activesessions")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestActiveSessionsSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns active sessions",
			queryResult: &db.QueryResult{
				Columns: []string{"sid", "serial#", "username", "status", "osuser", "machine", "program", "sql_id", "event", "wait_class", "seconds_in_wait"},
				Rows: [][]any{
					{1, 100, "SYS", "ACTIVE", "oracle", "db-host", "sqlplus", "abc123", "db file sequential read", "User I/O", 5},
					{3, 300, "APP_USER", "ACTIVE", "appuser", "app-host", "jdbc", "def456", "buffer busy waits", "Concurrency", 2},
				},
				Duration: 30 * time.Millisecond,
			},
			wantRows:    2,
			wantSummary: "活跃会话 (排除后台进程) — 2 个",
		},
		{
			name: "no active sessions",
			queryResult: &db.QueryResult{
				Columns:  []string{"sid", "serial#", "username", "status", "osuser", "machine", "program", "sql_id", "event", "wait_class", "seconds_in_wait"},
				Rows:     [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "活跃会话 (排除后台进程) — 0 个",
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
				if sql != activeSessionsSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewActiveSessionsSkill(drv)
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
