/*-------------------------------------------------------------------------
 *
 * sessions.go
 *	  sessions — SessionsSkill plus helpers (NewSessionsSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/sessions.go
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

const sessionsSQL = `SELECT SESS_ID, USER_NAME, STATE, TRX_ID, CLNT_IP,
       OSNAME, CLNT_TYPE, CREATE_TIME
FROM V$SESSIONS
WHERE USER_NAME IS NOT NULL
ORDER BY STATE, CREATE_TIME DESC`

type SessionsSkill struct{ driver db.Driver }

func NewSessionsSkill(driver db.Driver) *SessionsSkill { return &SessionsSkill{driver: driver} }

func (s *SessionsSkill) Name() string                       { return "sessions" }
func (s *SessionsSkill) Description() string                { return "全部会话概览 (V$SESSIONS)" }
func (s *SessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SessionsSkill) Validate(_ skill.Params) error      { return nil }
func (s *SessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sessions", Description: "List all DM sessions"}
}
func (s *SessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "sessions", Usage: "/sessions"}
}

func (s *SessionsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, sessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("dm sessions: %w", err)
	}

	// state 列在第 3 位 (SESS_ID, USER_NAME, STATE, ...)
	stateCount := dmutil.CountByCol(r.Rows, 2)
	entries := []dmutil.SummaryEntry{
		{Key: "total_sessions", Val: len(r.Rows)},
		{Key: "data_window", Val: "real-time snapshot (V$SESSIONS)"},
		{Key: "kill_session_syntax", Val: "CALL SP_CLOSE_SESSION(<sess_id>)"},
	}
	for state, n := range stateCount {
		entries = append(entries, dmutil.SummaryEntry{Key: "state_" + state, Val: n})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("会话总数 — %d", len(r.Rows)),
	}, nil
}
