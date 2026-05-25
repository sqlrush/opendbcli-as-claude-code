/*-------------------------------------------------------------------------
 *
 * activesessions.go
 *	  activesessions — ActiveSessionsSkill plus helpers
 *	  (NewActiveSessionsSkill) used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/activesessions.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// activeSessionsSQL: 活跃会话 + SQL 文本前 80 字符
const activeSessionsSQL = `SELECT SESS_ID, USER_NAME, STATE,
       TRX_ID, CLNT_IP,
       SUBSTR(SQL_TEXT, 1, 80) AS SQL_TEXT,
       (SYSDATE - CREATE_TIME) * 86400 AS ELAPSED_SEC
FROM V$SESSIONS
WHERE STATE = 'ACTIVE'
  AND USER_NAME IS NOT NULL
ORDER BY ELAPSED_SEC DESC`

type ActiveSessionsSkill struct{ driver db.Driver }

func NewActiveSessionsSkill(driver db.Driver) *ActiveSessionsSkill {
	return &ActiveSessionsSkill{driver: driver}
}

func (s *ActiveSessionsSkill) Name() string                       { return "activesessions" }
func (s *ActiveSessionsSkill) Description() string                { return "活跃会话 (V$SESSIONS STATE=ACTIVE)" }
func (s *ActiveSessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *ActiveSessionsSkill) Validate(_ skill.Params) error      { return nil }
func (s *ActiveSessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "activesessions", Description: "List active DM sessions with SQL text"}
}
func (s *ActiveSessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "activesessions", Usage: "/activesessions"}
}

func (s *ActiveSessionsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, activeSessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("dm activesessions: %w", err)
	}

	// SESS_ID(0), USER_NAME(1), STATE(2), TRX_ID(3), CLNT_IP(4), SQL_TEXT(5), ELAPSED_SEC(6)
	entries := []dmutil.SummaryEntry{
		{Key: "active_count", Val: len(r.Rows)},
		{Key: "data_window", Val: "real-time snapshot (V$SESSIONS WHERE STATE='ACTIVE')"},
	}
	if len(r.Rows) > 0 {
		// 第一行是耗时最长的
		entries = append(entries,
			dmutil.SummaryEntry{Key: "oldest_active_sess_id", Val: r.Rows[0][0]},
			dmutil.SummaryEntry{Key: "oldest_active_user", Val: r.Rows[0][1]},
			dmutil.SummaryEntry{Key: "oldest_active_elapsed_sec", Val: r.Rows[0][6]},
			dmutil.SummaryEntry{Key: "oldest_active_sql_head", Val: r.Rows[0][5]},
			dmutil.SummaryEntry{Key: "kill_oldest_cmd", Val: fmt.Sprintf("CALL SP_CLOSE_SESSION(%v)", r.Rows[0][0])},
		)
		// 用户分布
		users := dmutil.CountByCol(r.Rows, 1)
		for u, n := range users {
			entries = append(entries, dmutil.SummaryEntry{Key: "user_" + u, Val: n})
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("活跃会话 — %d", len(r.Rows)),
	}, nil
}
