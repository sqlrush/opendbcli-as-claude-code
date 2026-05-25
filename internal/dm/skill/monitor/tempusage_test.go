/*-------------------------------------------------------------------------
 *
 * tempusage_test.go
 *	  Test cases for tempusage.go (monitor package):
 *	  TestTempUsageSkill_Metadata, TestTempUsageSkill_FilesAndUsage,
 *	  TestTempUsageSkill_UsageQueryFailsGracefully.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/tempusage_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"errors"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestTempUsageSkill_Metadata(t *testing.T) {
	s := NewTempUsageSkill(makeRoutedDriver())
	if s.Name() != "tempusage" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestTempUsageSkill_FilesAndUsage(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "DBA_DATA_FILES",
			result: &db.QueryResult{
				Columns: []string{"TABLESPACE_NAME", "FILE_NAME", "SIZE_MB", "AUTOEXT", "MAX_MB"},
				Rows: [][]any{
					{"TEMP", "/var/dm/data/TEMP.DBF", int64(1024), "YES", int64(8192)},
					{"HMAIN", "/var/dm/data/HMAIN.DBF", int64(2048), "YES", int64(16384)},
				},
			},
		},
		sqlMatcher{
			contains: "DBA_TABLESPACES",
			result: &db.QueryResult{
				Columns: []string{"TABLESPACE_NAME", "USED_MB"},
				Rows: [][]any{
					{"TEMP", 256.0},
					{"HMAIN", 512.0},
				},
			},
		},
	)
	r, _ := NewTempUsageSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "temp_file_count: 2")
	assertSummaryContains(t, r.Rendered, "used_mb_TEMP: 256")
	assertSummaryContains(t, r.Rendered, "used_mb_HMAIN: 512")
}

func TestTempUsageSkill_UsageQueryFailsGracefully(t *testing.T) {
	// SF_GET_TS_USED_SPACE 在某些实例不可用 → tempUsageSQL fail, 不致命
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if len(sql) > 0 && sql[0] == 'S' && sql[1] == 'E' && sql[2] == 'L' {
			// DBA_DATA_FILES (查表空间文件)
			if contains(sql, "DBA_DATA_FILES") {
				return &db.QueryResult{
					Columns: []string{"TABLESPACE_NAME", "FILE_NAME", "SIZE_MB", "AUTOEXT", "MAX_MB"},
					Rows:    [][]any{{"TEMP", "/temp.dbf", int64(1024), "YES", int64(8192)}},
				}, nil
			}
			// SF_GET_TS_USED_SPACE 不可用
			if contains(sql, "SF_GET_TS_USED_SPACE") {
				return nil, errors.New("DM-2099: function not found")
			}
		}
		return &db.QueryResult{}, nil
	}
	r, err := NewTempUsageSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("usage failure should not propagate: %v", err)
	}
	assertSummaryContains(t, r.Rendered, "temp_file_count: 1")
	if !contains(r.Rendered, "SF_GET_TS_USED_SPACE 不可用") {
		t.Errorf("Rendered should mention fallback for missing function. Got:\n%s", r.Rendered)
	}
}
