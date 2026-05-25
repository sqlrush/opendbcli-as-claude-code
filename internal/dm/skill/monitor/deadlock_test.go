/*-------------------------------------------------------------------------
 *
 * deadlock_test.go
 *	  Test cases for deadlock.go (monitor package):
 *	  TestDeadlockSkill_Metadata, TestDeadlockSkill_NoDeadlocks,
 *	  TestDeadlockSkill_WithDeadlocks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/deadlock_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestDeadlockSkill_Metadata(t *testing.T) {
	s := NewDeadlockSkill(makeRoutedDriver())
	if s.Name() != "deadlock" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestDeadlockSkill_NoDeadlocks(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$DEADLOCK_HISTORY",
		result: &db.QueryResult{
			Columns: []string{"SEQNO", "TRX_ID", "SESS_ID", "SQL_TEXT", "HAPPEN_TIME"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewDeadlockSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(r.Rendered, "无历史死锁") {
		t.Errorf("Rendered missing empty-state. Got:\n%s", r.Rendered)
	}
	if !strings.Contains(r.Rendered, "deadlock_count: 0") {
		t.Errorf("Rendered missing deadlock_count: 0")
	}
}

func TestDeadlockSkill_WithDeadlocks(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$DEADLOCK_HISTORY",
		result: &db.QueryResult{
			Columns: []string{"SEQNO", "TRX_ID", "SESS_ID", "SQL_TEXT", "HAPPEN_TIME"},
			Rows: [][]any{
				{int64(1), int64(2458068), int64(140304100), "UPDATE bench_dm_a SET v=v+1", "2026-05-01 21:48:00"},
				{int64(2), int64(2458069), int64(140304200), "UPDATE bench_dm_b SET v=v+1", "2026-05-01 21:48:01"},
			},
		},
	})
	r, _ := NewDeadlockSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(r.Rendered, "deadlock_count: 2") {
		t.Errorf("Rendered missing deadlock_count: 2\n%s", r.Rendered)
	}
}

// 关键回归: V$DEADLOCK_HISTORY 必须用 HAPPEN_TIME (有此列), 不能用 OPTIME.
// 这跟 V$DANGER_EVENT 命名不一致 — 必须区分.
func TestDeadlockSkill_SQL_HAPPEN_TIME(t *testing.T) {
	var captured string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = sql
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewDeadlockSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(captured, "HAPPEN_TIME") {
		t.Errorf("V$DEADLOCK_HISTORY must use HAPPEN_TIME column. SQL:\n%s", captured)
	}
	if strings.Contains(captured, "OPTIME") {
		t.Errorf("V$DEADLOCK_HISTORY incorrectly uses OPTIME (那是 V$DANGER_EVENT 的列). SQL:\n%s", captured)
	}
}
