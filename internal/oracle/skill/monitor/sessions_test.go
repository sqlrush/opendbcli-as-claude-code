/*-------------------------------------------------------------------------
 *
 * sessions_test.go
 *	  Test cases for sessions.go (monitor package):
 *	  TestSessionsSkill_Metadata, TestSessionsSkill_Execute.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/sessions_test.go
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

func TestSessionsSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSessionsSkill(drv)

	if s.Name() != "sessions" {
		t.Errorf("Name() = %q, want %q", s.Name(), "sessions")
	}
	if s.Description() != "Show all database sessions" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Show all database sessions")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "sessions" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "sessions")
	}
	if s.CLIDef().Command != "sessions" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "sessions")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestSessionsSkill_Execute(t *testing.T) {
	tests := []struct {
		name        string
		queryResult *db.QueryResult
		queryErr    error
		wantRows    int
		wantSummary string
		wantErr     bool
	}{
		{
			name: "returns sessions",
			queryResult: &db.QueryResult{
				Columns: []string{"sid", "serial#", "username", "status", "osuser", "machine", "program", "sql_id", "event", "wait_class", "seconds_in_wait"},
				Rows: [][]any{
					{1, 100, "SYS", "ACTIVE", "oracle", "db-host", "sqlplus", "abc123", "db file sequential read", "User I/O", 0},
					{2, 200, "APP_USER", "INACTIVE", "appuser", "app-host", "jdbc", nil, "SQL*Net message from client", "Idle", 120},
				},
				Duration: 50 * time.Millisecond,
			},
			wantRows:    2,
			wantSummary: "全部会话 — 2 个",
		},
		{
			name: "empty result",
			queryResult: &db.QueryResult{
				Columns: []string{"sid", "serial#", "username", "status", "osuser", "machine", "program", "sql_id", "event", "wait_class", "seconds_in_wait"},
				Rows:    [][]any{},
				Duration: 10 * time.Millisecond,
			},
			wantRows:    0,
			wantSummary: "全部会话 — 0 个",
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
				if sql != sessionsSQL {
					t.Errorf("unexpected SQL: %s", sql)
				}
				if tt.queryErr != nil {
					return nil, tt.queryErr
				}
				return tt.queryResult, nil
			}

			s := NewSessionsSkill(drv)
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
