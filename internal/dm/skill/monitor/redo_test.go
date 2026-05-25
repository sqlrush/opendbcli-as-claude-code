/*-------------------------------------------------------------------------
 *
 * redo_test.go
 *	  Test cases for redo.go (monitor package): TestRedoSkill_Metadata,
 *	  TestRedoSkill_FilesAndStatus.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/redo_test.go
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

func TestRedoSkill_Metadata(t *testing.T) {
	s := NewRedoSkill(makeRoutedDriver())
	if s.Name() != "redo" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestRedoSkill_FilesAndStatus(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$RLOGFILE",
			result: &db.QueryResult{
				Columns: []string{"GROUP_ID", "FILE_ID", "PATH", "SIZE_MB"},
				Rows: [][]any{
					{int64(0), int64(0), "/var/dm/data/DAMENG01.log", int64(2048)},
					{int64(0), int64(1), "/var/dm/data/DAMENG02.log", int64(2048)},
					{int64(0), int64(2), "/var/dm/data/DAMENG03.log", int64(2048)},
				},
			},
		},
		sqlMatcher{
			contains: "V$RLOG",
			result: &db.QueryResult{
				Columns: []string{"CUR_FILE", "FILE_LSN", "CKPT_LSN", "FREE_SPACE"},
				Rows:    [][]any{{int64(0), int64(17687462), int64(17687425), int64(536772608)}},
			},
		},
	)
	r, _ := NewRedoSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "rlog_file_count: 3")
	assertSummaryContains(t, r.Rendered, "current_file: 0")
	assertSummaryContains(t, r.Rendered, "file_lsn: 17687462")
	assertSummaryContains(t, r.Rendered, "ckpt_lsn: 17687425")
	assertSummaryContains(t, r.Rendered, "free_space: 536772608")
}
