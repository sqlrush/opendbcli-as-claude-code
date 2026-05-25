/*-------------------------------------------------------------------------
 *
 * archive_test.go
 *	  Test cases for archive.go (monitor package):
 *	  TestArchiveSkill_Metadata, TestArchiveSkill_OffMode,
 *	  TestArchiveSkill_OnMode_With_Recent.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/archive_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestArchiveSkill_Metadata(t *testing.T) {
	s := NewArchiveSkill(makeRoutedDriver())
	if s.Name() != "archive" {
		t.Errorf("Name() = %q", s.Name())
	}
}

// 单机部署: V$DM_ARCH_INI / V$ARCH_STATUS / V$ARCHIVED_LOG 都返回空 → archive_mode=OFF
func TestArchiveSkill_OffMode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DM_ARCH_INI",
			result: &db.QueryResult{
				Columns: []string{"ARCH_NAME", "ARCH_TYPE", "ARCH_DEST", "ARCH_FILE_SIZE", "ARCH_SPACE_LIMIT"},
				Rows:    [][]any{},
			},
		},
		sqlMatcher{
			contains: "V$ARCH_STATUS",
			result: &db.QueryResult{
				Columns: []string{"ARCH_TYPE", "ARCH_DEST", "ARCH_STATUS"},
				Rows:    [][]any{},
			},
		},
		sqlMatcher{
			contains: "V$ARCHIVED_LOG",
			result: &db.QueryResult{
				Columns: []string{"ARCH_NAME", "FIRST_TIME", "NEXT_CHANGE#"},
				Rows:    [][]any{},
			},
		},
	)
	r, _ := NewArchiveSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "arch_dest_count: 0")
	assertSummaryContains(t, r.Rendered, "archive_mode: OFF (no destination configured)")
}

func TestArchiveSkill_OnMode_With_Recent(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DM_ARCH_INI",
			result: &db.QueryResult{
				Columns: []string{"ARCH_NAME", "ARCH_TYPE", "ARCH_DEST", "ARCH_FILE_SIZE", "ARCH_SPACE_LIMIT"},
				Rows: [][]any{
					{"ARCH1", "LOCAL", "/var/dm/arch", int64(67108864), int64(0)},
				},
			},
		},
		sqlMatcher{
			contains: "V$ARCH_STATUS",
			result: &db.QueryResult{
				Columns: []string{"ARCH_TYPE", "ARCH_DEST", "ARCH_STATUS"},
				Rows: [][]any{
					{"LOCAL", "/var/dm/arch", "VALID"},
				},
			},
		},
		sqlMatcher{
			contains: "V$ARCHIVED_LOG",
			result: &db.QueryResult{
				Columns: []string{"ARCH_NAME", "FIRST_TIME", "NEXT_CHANGE#"},
				Rows: [][]any{
					{"arch_2026-05-02_001.log", "2026-05-02 10:00:00", int64(17687462)},
				},
			},
		},
	)
	r, _ := NewArchiveSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "arch_dest_count: 1")
	assertSummaryContains(t, r.Rendered, "archive_mode: ON")
	assertSummaryContains(t, r.Rendered, "recent_archives: 1")
	assertSummaryContains(t, r.Rendered, "latest_archive_time: 2026-05-02 10:00:00")
	assertSummaryContains(t, r.Rendered, "status_VALID: 1")
}
