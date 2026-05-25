/*-------------------------------------------------------------------------
 *
 * blocktree_test.go
 *	  Test cases for blocktree.go (monitor package):
 *	  TestBlockTreeSkill_Metadata, TestBlockTreeSkill_NoBlocks,
 *	  TestBlockTreeSkill_WithChains.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/blocktree_test.go
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

func TestBlockTreeSkill_Metadata(t *testing.T) {
	s := NewBlockTreeSkill(makeRoutedDriver())
	if s.Name() != "blocktree" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestBlockTreeSkill_NoBlocks(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$LOCK",
		result: &db.QueryResult{
			Columns: []string{"BLOCKED_SESS", "BLOCKED_USER", "BLOCKED_SQL", "BLOCKER_SESS", "BLOCKER_USER", "LTYPE", "TABLE_ID"},
			Rows:    [][]any{},
		},
	})
	r, err := NewBlockTreeSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertSummaryContains(t, r.Rendered, "block_chains: 0")
	if r.Summary != "no block chains" {
		t.Errorf("Summary = %q", r.Summary)
	}
}

func TestBlockTreeSkill_WithChains(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$LOCK",
		result: &db.QueryResult{
			Columns: []string{"BLOCKED_SESS", "BLOCKED_USER", "BLOCKED_SQL", "BLOCKER_SESS", "BLOCKER_USER", "LTYPE", "TABLE_ID"},
			Rows: [][]any{
				{int64(140304100), "USER_A", "UPDATE bench_dm_a SET v=v+1", int64(140304200), "USER_B", "TX", int64(1234)},
				{int64(140304300), "USER_C", "UPDATE bench_dm_a SET v=v-1", int64(140304200), "USER_B", "TX", int64(1234)},
			},
		},
	})
	r, _ := NewBlockTreeSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "block_chains: 2")
	assertSummaryContains(t, r.Rendered, "unique_blockers: 1")
	assertSummaryContains(t, r.Rendered, "first_blocked_sess: 140304100")
	assertSummaryContains(t, r.Rendered, "first_blocker_sess: 140304200")
	// 关键回归: kill_blocker_cmd 必须含 SP_CLOSE_SESSION + 具体 PID (DM 杀会话约束)
	assertSummaryContains(t, r.Rendered, "kill_blocker_cmd: CALL SP_CLOSE_SESSION(140304200)")
}

// 关键回归: blocktree SQL 必须含 BLOCKED=0 过滤防 OOM (参考 OG blocktree 教训).
func TestBlockTreeSkill_SQL_HasBlocked0Filter(t *testing.T) {
	if !contains(blocktreeSQL, "BLOCKED  = 0") && !contains(blocktreeSQL, "BLOCKED = 0") {
		t.Errorf("blocktreeSQL missing BLOCKED=0 filter on holder side. Risk: N×N OOM. SQL:\n%s", blocktreeSQL)
	}
}
