/*-------------------------------------------------------------------------
 *
 * alert_test.go
 *	  Test cases for alert.go (admin package): TestAlertSkill_Metadata,
 *	  TestAlertSkill_Execute, TestParseAlertHours.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/alert_test.go
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

func TestAlertSkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewAlertSkill(d)

	if s.Name() != "alert" {
		t.Errorf("Name() = %q, want %q", s.Name(), "alert")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "alert" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "alert")
	}
	if s.CLIDef().Command != "alert" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "alert")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestAlertSkill_Execute(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantRows  int
		wantHours string
		wantErr   bool
	}{
		{
			name:   "default 24 hours",
			params: map[string]any{},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				hours, ok := args[0].(int)
				if !ok || hours != 24 {
					return nil, errors.New("expected 24 hours")
				}
				return &db.QueryResult{
					Columns: []string{"ORIGINATING_TIMESTAMP", "MESSAGE_TEXT", "MESSAGE_LEVEL"},
					Rows: [][]any{
						{"2024-01-15 10:00:00", "ORA-00060: deadlock detected", 1},
					},
					Duration: 15 * time.Millisecond,
				}, nil
			},
			wantRows:  1,
			wantHours: "24",
		},
		{
			name:   "custom hours",
			params: map[string]any{"args": "48"},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				hours, ok := args[0].(int)
				if !ok || hours != 48 {
					return nil, errors.New("expected 48 hours")
				}
				return &db.QueryResult{
					Columns: []string{"ORIGINATING_TIMESTAMP", "MESSAGE_TEXT", "MESSAGE_LEVEL"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows:  0,
			wantHours: "48",
		},
		{
			name:   "invalid hours falls back to default",
			params: map[string]any{"args": "abc"},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				hours, ok := args[0].(int)
				if !ok || hours != 24 {
					return nil, errors.New("expected default 24 hours for invalid input")
				}
				return &db.QueryResult{
					Columns: []string{"ORIGINATING_TIMESTAMP", "MESSAGE_TEXT", "MESSAGE_LEVEL"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows:  0,
			wantHours: "24",
		},
		{
			name:   "query error",
			params: map[string]any{},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return nil, errors.New("ORA-01031: insufficient privileges")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewAlertSkill(d)

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
			if result.Type != skill.ResultText {
				t.Errorf("Type = %v, want ResultText", result.Type)
			}
			if tt.wantRows > 0 {
				qr, ok := result.Data.(*db.QueryResult)
				if !ok {
					t.Fatalf("Data is not *db.QueryResult: %T", result.Data)
				}
				if len(qr.Rows) != tt.wantRows {
					t.Errorf("got %d rows, want %d", len(qr.Rows), tt.wantRows)
				}
			}
			if result.Rendered == "" {
				t.Error("Rendered should not be empty")
			}
			if tt.wantHours != "" && result.Metadata != nil && result.Metadata["hours"] != tt.wantHours {
				t.Errorf("hours metadata = %q, want %q", result.Metadata["hours"], tt.wantHours)
			}
		})
	}
}

func TestParseAlertHours(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 24},
		{"48", 48},
		{"0", 24},
		{"-5", 24},
		{"abc", 24},
		{"  12  ", 12},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseAlertHours(tt.input)
			if got != tt.want {
				t.Errorf("parseAlertHours(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestAlertSQL_IsConst(t *testing.T) {
	if !strings.Contains(alertSQL, "v$diag_alert_ext") {
		t.Error("alertSQL should query v$diag_alert_ext")
	}
}
