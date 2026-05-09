/*-------------------------------------------------------------------------
 *
 * backup_test.go
 *	  Test cases for backup.go (admin package):
 *	  TestBackupSkill_Metadata, TestBackupSkill_Execute,
 *	  TestParseBackupDays.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/backup_test.go
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

func TestBackupSkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewBackupSkill(d)

	if s.Name() != "backup" {
		t.Errorf("Name() = %q, want %q", s.Name(), "backup")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "backup" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "backup")
	}
	if s.CLIDef().Command != "backup" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "backup")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestBackupSkill_Execute(t *testing.T) {
	tests := []struct {
		name      string
		params    map[string]any
		queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantRows  int
		wantDays  string
		wantErr   bool
	}{
		{
			name:   "default 7 days",
			params: map[string]any{},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				days, ok := args[0].(int)
				if !ok || days != 7 {
					return nil, errors.New("expected 7 days")
				}
				return &db.QueryResult{
					Columns: []string{"START_TIME", "END_TIME", "STATUS", "INPUT_TYPE", "OUTPUT_BYTES_DISPLAY", "TIME_TAKEN_DISPLAY"},
					Rows: [][]any{
						{"2024-01-15 02:00", "2024-01-15 03:30", "COMPLETED", "DB FULL", "50.2G", "01:30:00"},
						{"2024-01-14 02:00", "2024-01-14 02:45", "COMPLETED", "ARCHIVELOG", "2.1G", "00:45:00"},
					},
					Duration: 20 * time.Millisecond,
				}, nil
			},
			wantRows: 2,
			wantDays: "7",
		},
		{
			name:   "custom days",
			params: map[string]any{"args": "30"},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				days, ok := args[0].(int)
				if !ok || days != 30 {
					return nil, errors.New("expected 30 days")
				}
				return &db.QueryResult{
					Columns: []string{"START_TIME", "END_TIME", "STATUS", "INPUT_TYPE", "OUTPUT_BYTES_DISPLAY", "TIME_TAKEN_DISPLAY"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows: 0,
			wantDays: "30",
		},
		{
			name:   "invalid days falls back to default",
			params: map[string]any{"args": "xyz"},
			queryFunc: func(_ context.Context, _ string, args ...any) (*db.QueryResult, error) {
				days, ok := args[0].(int)
				if !ok || days != 7 {
					return nil, errors.New("expected default 7 days for invalid input")
				}
				return &db.QueryResult{
					Columns: []string{"START_TIME", "END_TIME", "STATUS", "INPUT_TYPE", "OUTPUT_BYTES_DISPLAY", "TIME_TAKEN_DISPLAY"},
					Rows:    [][]any{},
				}, nil
			},
			wantRows: 0,
			wantDays: "7",
		},
		{
			name:   "query error",
			params: map[string]any{},
			queryFunc: func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
				return nil, errors.New("connection timeout")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewBackupSkill(d)

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
			if tt.wantRows == 0 {
				if result.Type != skill.ResultText {
					t.Errorf("Type = %v, want ResultText for empty result", result.Type)
				}
			} else {
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
				if tt.wantDays != "" && result.Metadata["days"] != tt.wantDays {
					t.Errorf("days metadata = %q, want %q", result.Metadata["days"], tt.wantDays)
				}
			}
		})
	}
}

func TestParseBackupDays(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 7},
		{"30", 30},
		{"0", 7},
		{"-1", 7},
		{"abc", 7},
		{"  14  ", 14},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseBackupDays(tt.input)
			if got != tt.want {
				t.Errorf("parseBackupDays(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestBackupSQL_IsConst(t *testing.T) {
	if !strings.Contains(backupSQL, "v$rman_backup_job_details") {
		t.Error("backupSQL should query v$rman_backup_job_details")
	}
}
