/*-------------------------------------------------------------------------
 *
 * locks_test.go
 *	  Test cases for locks.go (monitor package):
 *	  TestLocksSkill_Metadata, TestLocksSkill_NoLocks,
 *	  TestLocksSkill_WithLocks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/locks_test.go
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

func TestLocksSkill_Metadata(t *testing.T) {
	s := NewLocksSkill(makeRoutedDriver())
	if s.Name() != "locks" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestLocksSkill_NoLocks(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$LOCK",
		result: &db.QueryResult{
			Columns: []string{"TRX_ID", "TID", "LTYPE", "LMODE", "BLOCKED", "TABLE_ID", "ROW_IDX", "THRD_ID"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewLocksSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "total_locks: 0")
	assertSummaryContains(t, r.Rendered, "blocked_count: 0")
}

func TestLocksSkill_WithLocks(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$LOCK",
		result: &db.QueryResult{
			Columns: []string{"TRX_ID", "TID", "LTYPE", "LMODE", "BLOCKED", "TABLE_ID", "ROW_IDX", "THRD_ID"},
			Rows: [][]any{
				{int64(2458068), int64(140304100), "TABLE", "X", int64(0), int64(33555476), int64(0), int64(1)},
				{int64(2458069), int64(140304200), "TABLE", "X", int64(1), int64(33555476), int64(0), int64(2)},
				{int64(2458070), int64(140304300), "ROW", "S", int64(0), int64(33555477), int64(5), int64(3)},
			},
		},
	})
	r, _ := NewLocksSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "total_locks: 3")
	assertSummaryContains(t, r.Rendered, "blocked_count: 1") // 仅 1 行 BLOCKED=1
	// 类型分布
	assertSummaryContains(t, r.Rendered, "ltype_TABLE: 2")
	assertSummaryContains(t, r.Rendered, "ltype_ROW: 1")
	// 锁模式
	assertSummaryContains(t, r.Rendered, "lmode_X: 2")
	assertSummaryContains(t, r.Rendered, "lmode_S: 1")
}
