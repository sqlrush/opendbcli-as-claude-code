/*-------------------------------------------------------------------------
 *
 * standby_test.go
 *	  Test cases for standby.go (admin package):
 *	  TestStandbySkill_Metadata, TestStandbySkill_Execute,
 *	  TestFormatStandbyOutput.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/admin/standby_test.go
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

func TestStandbySkill_Metadata(t *testing.T) {
	d := mock.NewMockDriver()
	s := NewStandbySkill(d)

	if s.Name() != "standby" {
		t.Errorf("Name() = %q, want %q", s.Name(), "standby")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should not be empty")
	}
	if s.ToolDef().Name != "standby" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "standby")
	}
	if s.CLIDef().Command != "standby" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "standby")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestStandbySkill_Execute(t *testing.T) {
	tests := []struct {
		name      string
		queryFunc func(ctx context.Context, sql string, args ...any) (*db.QueryResult, error)
		wantSubs  []string
		wantErr   bool
	}{
		{
			name: "full Data Guard status",
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "v$database") {
					return &db.QueryResult{
						Columns: []string{"DATABASE_ROLE", "DB_UNIQUE_NAME", "PROTECTION_MODE", "SWITCHOVER_STATUS", "DATAGUARD_BROKER"},
						Rows:    [][]any{{"PRIMARY", "orcl", "MAXIMUM AVAILABILITY", "TO STANDBY", "ENABLED"}},
						Duration: 5 * time.Millisecond,
					}, nil
				}
				return &db.QueryResult{
					Columns: []string{"DEST_NAME", "STATUS", "TYPE", "SRL", "GAP_STATUS"},
					Rows: [][]any{
						{"LOG_ARCHIVE_DEST_2", "VALID", "PHYSICAL", "YES", "NO GAP"},
					},
					Duration: 3 * time.Millisecond,
				}, nil
			},
			wantSubs: []string{"Database Info", "PRIMARY", "orcl", "Archive Dest Status", "LOG_ARCHIVE_DEST_2"},
		},
		{
			name: "no archive destinations",
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "v$database") {
					return &db.QueryResult{
						Columns: []string{"DATABASE_ROLE", "DB_UNIQUE_NAME", "PROTECTION_MODE", "SWITCHOVER_STATUS", "DATAGUARD_BROKER"},
						Rows:    [][]any{{"PRIMARY", "orcl", "MAXIMUM PERFORMANCE", "NOT ALLOWED", "DISABLED"}},
					}, nil
				}
				return &db.QueryResult{
					Columns: []string{"DEST_NAME", "STATUS", "TYPE", "SRL", "GAP_STATUS"},
					Rows:    [][]any{},
				}, nil
			},
			wantSubs: []string{"Database Info", "PRIMARY", "No active archive destinations"},
		},
		{
			name: "no database rows",
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "v$database") {
					return &db.QueryResult{
						Columns: []string{"DATABASE_ROLE", "DB_UNIQUE_NAME", "PROTECTION_MODE", "SWITCHOVER_STATUS", "DATAGUARD_BROKER"},
						Rows:    [][]any{},
					}, nil
				}
				return &db.QueryResult{
					Columns: []string{"DEST_NAME", "STATUS", "TYPE", "SRL", "GAP_STATUS"},
					Rows:    [][]any{},
				}, nil
			},
			wantSubs: []string{"No data available", "No active archive destinations"},
		},
		{
			name: "database query error",
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "v$database") {
					return nil, errors.New("ORA-01031: insufficient privileges")
				}
				return &db.QueryResult{}, nil
			},
			wantErr: true,
		},
		{
			name: "archive dest query error",
			queryFunc: func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
				if strings.Contains(sql, "v$database") {
					return &db.QueryResult{
						Columns: []string{"DATABASE_ROLE"},
						Rows:    [][]any{{"PRIMARY"}},
					}, nil
				}
				return nil, errors.New("connection lost")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := mock.NewMockDriver()
			d.QueryFunc = tt.queryFunc
			s := NewStandbySkill(d)

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
			rendered := result.Rendered
			for _, sub := range tt.wantSubs {
				if !strings.Contains(rendered, sub) {
					t.Errorf("output does not contain %q:\n%s", sub, rendered)
				}
			}
		})
	}
}

func TestFormatStandbyOutput(t *testing.T) {
	tests := []struct {
		name        string
		dbInfo      *db.QueryResult
		archiveDest *db.QueryResult
		wantSubs    []string
	}{
		{
			name: "with data",
			dbInfo: &db.QueryResult{
				Columns: []string{"DATABASE_ROLE", "DB_UNIQUE_NAME"},
				Rows:    [][]any{{"PRIMARY", "mydb"}},
			},
			archiveDest: &db.QueryResult{
				Columns: []string{"DEST_NAME", "STATUS"},
				Rows:    [][]any{{"DEST_1", "VALID"}, {"DEST_2", "VALID"}},
			},
			wantSubs: []string{"PRIMARY", "mydb", "DEST_1", "DEST_2"},
		},
		{
			name: "empty data",
			dbInfo: &db.QueryResult{
				Columns: []string{"DATABASE_ROLE"},
				Rows:    [][]any{},
			},
			archiveDest: &db.QueryResult{
				Columns: []string{"DEST_NAME"},
				Rows:    [][]any{},
			},
			wantSubs: []string{"No data available", "No active archive destinations"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := formatStandbyOutput(tt.dbInfo, tt.archiveDest)
			for _, sub := range tt.wantSubs {
				if !strings.Contains(output, sub) {
					t.Errorf("output does not contain %q:\n%s", sub, output)
				}
			}
		})
	}
}

func TestBuildStandbyData(t *testing.T) {
	t.Run("full data", func(t *testing.T) {
		dbInfo := &db.QueryResult{
			Columns: []string{"DATABASE_ROLE", "DB_UNIQUE_NAME", "PROTECTION_MODE", "SWITCHOVER_STATUS", "DATAGUARD_BROKER"},
			Rows:    [][]any{{"PRIMARY", "mydb", "MAXIMUM PERFORMANCE", "TO STANDBY", "ENABLED"}},
		}
		archiveDest := &db.QueryResult{
			Columns: []string{"DEST_NAME", "STATUS", "TYPE", "SRL", "GAP_STATUS"},
			Rows:    [][]any{{"LOG_ARCHIVE_DEST_2", "VALID", "PHYSICAL", "YES", "NO GAP"}},
		}
		data := buildStandbyData(dbInfo, archiveDest)
		if data.Database == nil {
			t.Fatal("Database should not be nil")
		}
		if data.Database.Role != "PRIMARY" {
			t.Errorf("Role = %q, want PRIMARY", data.Database.Role)
		}
		if data.Database.DBUniqueName != "mydb" {
			t.Errorf("DBUniqueName = %q, want mydb", data.Database.DBUniqueName)
		}
		if len(data.ArchiveDests) != 1 {
			t.Fatalf("ArchiveDests len = %d, want 1", len(data.ArchiveDests))
		}
		if data.ArchiveDests[0].DestName != "LOG_ARCHIVE_DEST_2" {
			t.Errorf("DestName = %q", data.ArchiveDests[0].DestName)
		}
	})

	t.Run("empty rows", func(t *testing.T) {
		dbInfo := &db.QueryResult{Columns: []string{"COL"}}
		archiveDest := &db.QueryResult{Columns: []string{"COL"}}
		data := buildStandbyData(dbInfo, archiveDest)
		if data.Database != nil {
			t.Error("Database should be nil for empty rows")
		}
		if data.ArchiveDests == nil {
			t.Error("ArchiveDests should be empty slice, not nil")
		}
		if len(data.ArchiveDests) != 0 {
			t.Errorf("ArchiveDests len = %d, want 0", len(data.ArchiveDests))
		}
	})
}

func TestStandbySQL_IsConst(t *testing.T) {
	if !strings.Contains(standbyDatabaseSQL, "v$database") {
		t.Error("standbyDatabaseSQL should query v$database")
	}
	if !strings.Contains(standbyArchiveDestSQL, "v$archive_dest_status") {
		t.Error("standbyArchiveDestSQL should query v$archive_dest_status")
	}
}
