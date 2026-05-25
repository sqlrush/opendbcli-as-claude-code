/*-------------------------------------------------------------------------
 *
 * kill_test.go
 *	  Test cases for kill.go (admin package): TestKillSkill_Metadata,
 *	  TestKillSkill_Validate, TestKillSkill_Execute_ShowConfirmation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/kill_test.go
 *
 *-------------------------------------------------------------------------
 */
package admin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestKillSkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewKillSkill(d)

	if s.Name() != "kill" {
		t.Errorf("Name() = %q, want %q", s.Name(), "kill")
	}
	if s.SecurityLevel() != skill.LevelOperator {
		t.Errorf("SecurityLevel() = %v, want LevelOperator", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "kill" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "kill")
	}
	if s.CLIDef().Command != "kill" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "kill")
	}
}

func TestKillSkill_Validate(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewKillSkill(d)

	tests := []struct {
		name    string
		params  map[string]any
		wantErr bool
	}{
		{
			name:    "no args",
			params:  map[string]any{},
			wantErr: true,
		},
		{
			name:    "empty args",
			params:  map[string]any{"args": ""},
			wantErr: true,
		},
		{
			name:    "valid SID",
			params:  map[string]any{"args": "142"},
			wantErr: false,
		},
		{
			name:    "valid SID with immediate",
			params:  map[string]any{"args": "142 immediate"},
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

// mockSessionDetail returns a mock session detail row for testing.
func mockSessionDetail(sid, serial, username, status string) *db.QueryResult {
	return &db.QueryResult{
		Columns: []string{"SID", "SERIAL#", "USERNAME", "STATUS", "SQL_ID", "EVENT", "PROGRAM", "LOGIN_MIN", "SQL_TEXT"},
		Rows: [][]any{{
			sid, serial, username, status, "abc123", "db file sequential read", "sqlplus.exe", 35.2, "SELECT * FROM dual",
		}},
	}
}

func TestKillSkill_Execute_ShowConfirmation(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return mockSessionDetail("142", "12345", "TEST_USER", "ACTIVE"), nil
	}
	s := NewKillSkill(d)

	// Without confirm: should show session details and confirmation prompt.
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "142"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "Kill Session") {
		t.Error("should show session details")
	}
	if !strings.Contains(result.Rendered, "TEST_USER") {
		t.Error("should show username")
	}
	if !strings.Contains(result.Rendered, "confirm") {
		t.Error("should show confirm hint")
	}
}

func TestKillSkill_Execute_ConfirmKill(t *testing.T) {
	var execCalled bool
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return mockSessionDetail("142", "12345", "TEST_USER", "ACTIVE"), nil
	}
	d.ExecFunc = func(_ context.Context, sql string, _ ...any) (*db.ExecResult, error) {
		execCalled = true
		if !strings.Contains(sql, "'142,12345'") {
			return nil, errors.New("unexpected SQL: " + sql)
		}
		return &db.ExecResult{RowsAffected: 0}, nil
	}
	s := NewKillSkill(d)

	// With confirm: should actually kill.
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "142 confirm"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !execCalled {
		t.Error("Exec should have been called with confirm")
	}
	if !strings.Contains(result.Rendered, "已终止") {
		t.Errorf("should contain 已终止, got: %s", result.Rendered)
	}
}

func TestKillSkill_Execute_ImmediateConfirm(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return mockSessionDetail("142", "99", "SYS", "ACTIVE"), nil
	}
	d.ExecFunc = func(_ context.Context, sql string, _ ...any) (*db.ExecResult, error) {
		if !strings.Contains(sql, "IMMEDIATE") {
			return nil, errors.New("expected IMMEDIATE in SQL: " + sql)
		}
		return &db.ExecResult{RowsAffected: 0}, nil
	}
	s := NewKillSkill(d)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "142 immediate confirm"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "IMMEDIATE") {
		t.Errorf("should mention IMMEDIATE, got: %s", result.Rendered)
	}
}

func TestKillSkill_Execute_SessionNotFound(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{Columns: []string{"SID"}, Rows: [][]any{}}, nil
	}
	s := NewKillSkill(d)

	_, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "999"}))
	if err == nil {
		t.Fatal("expected error for non-existent session")
	}
}

func TestKillSkill_Execute_QueryError(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("connection lost")
	}
	s := NewKillSkill(d)

	_, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "142"}))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestKillSkill_Execute_ExecError(t *testing.T) {
	d := mock.NewMockDriver()
	d.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return mockSessionDetail("142", "100", "APP_USER", "ACTIVE"), nil
	}
	d.ExecFunc = func(_ context.Context, _ string, _ ...any) (*db.ExecResult, error) {
		return nil, errors.New("insufficient privileges")
	}
	s := NewKillSkill(d)

	_, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "142 confirm"}))
	if err == nil {
		t.Fatal("expected error from Exec")
	}
}

func TestParseKillArgs(t *testing.T) {
	tests := []struct {
		args          string
		wantSID       string
		wantImmediate bool
		wantConfirm   bool
	}{
		{"", "", false, false},
		{"142", "142", false, false},
		{"142 immediate", "142", true, false},
		{"142 IMMEDIATE", "142", true, false},
		{"142 confirm", "142", false, true},
		{"142 immediate confirm", "142", true, true},
		{"142 confirm immediate", "142", true, true},
		{"142 y", "142", false, true},
		{"  42  ", "42", false, false},
		{"42 something", "42", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.args, func(t *testing.T) {
			sid, imm, confirm := parseKillArgs(tt.args)
			if sid != tt.wantSID {
				t.Errorf("SID = %q, want %q", sid, tt.wantSID)
			}
			if imm != tt.wantImmediate {
				t.Errorf("immediate = %v, want %v", imm, tt.wantImmediate)
			}
			if confirm != tt.wantConfirm {
				t.Errorf("confirm = %v, want %v", confirm, tt.wantConfirm)
			}
		})
	}
}

func TestBuildKillSQL(t *testing.T) {
	tests := []struct {
		name      string
		sid       string
		serial    string
		immediate bool
		want      string
	}{
		{
			name:   "normal",
			sid:    "142",
			serial: "100",
			want:   "ALTER SYSTEM KILL SESSION '142,100'",
		},
		{
			name:      "immediate",
			sid:       "42",
			serial:    "200",
			immediate: true,
			want:      "ALTER SYSTEM KILL SESSION '42,200' IMMEDIATE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildKillSQL(tt.sid, tt.serial, tt.immediate)
			if got != tt.want {
				t.Errorf("buildKillSQL() = %q, want %q", got, tt.want)
			}
		})
	}
}
