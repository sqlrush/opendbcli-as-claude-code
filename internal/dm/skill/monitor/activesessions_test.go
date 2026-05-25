/*-------------------------------------------------------------------------
 *
 * activesessions_test.go
 *	  Test cases for activesessions.go (monitor package):
 *	  TestActiveSessionsSkill_Metadata,
 *	  TestActiveSessionsSkill_NoActive,
 *	  TestActiveSessionsSkill_WithActive.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/activesessions_test.go
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

func TestActiveSessionsSkill_Metadata(t *testing.T) {
	s := NewActiveSessionsSkill(makeRoutedDriver())
	if s.Name() != "activesessions" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestActiveSessionsSkill_NoActive(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SESSIONS",
		result: &db.QueryResult{
			Columns: []string{"SESS_ID", "USER_NAME", "STATE", "TRX_ID", "CLNT_IP", "SQL_TEXT", "ELAPSED_SEC"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewActiveSessionsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "active_count: 0")
	assertSummaryContains(t, r.Rendered, "data_window: real-time snapshot (V$SESSIONS WHERE STATE='ACTIVE')")
}

func TestActiveSessionsSkill_WithActive(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SESSIONS",
		result: &db.QueryResult{
			Columns: []string{"SESS_ID", "USER_NAME", "STATE", "TRX_ID", "CLNT_IP", "SQL_TEXT", "ELAPSED_SEC"},
			Rows: [][]any{
				{int64(140999111), "OPENDB", "ACTIVE", int64(2458068), "127.0.0.1", "SELECT * FROM bench_users WHERE status = 3", int64(420)},
				{int64(140999222), "OPENDB", "ACTIVE", int64(2458069), "127.0.0.1", "UPDATE bench_a SET v=v+1 WHERE id=1", int64(60)},
			},
		},
	})
	r, _ := NewActiveSessionsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "active_count: 2")
	assertSummaryContains(t, r.Rendered, "oldest_active_sess_id: 140999111")
	assertSummaryContains(t, r.Rendered, "oldest_active_user: OPENDB")
	assertSummaryContains(t, r.Rendered, "oldest_active_elapsed_sec: 420")
	// 关键回归: kill_oldest_cmd 必须给具体 PID + DM 杀会话语法
	assertSummaryContains(t, r.Rendered, "kill_oldest_cmd: CALL SP_CLOSE_SESSION(140999111)")
	assertSummaryContains(t, r.Rendered, "user_OPENDB: 2")
}
