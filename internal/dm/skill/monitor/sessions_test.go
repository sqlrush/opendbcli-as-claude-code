/*-------------------------------------------------------------------------
 *
 * sessions_test.go
 *	  Test cases for sessions.go (monitor package):
 *	  TestSessionsSkill_Metadata, TestSessionsSkill_Execute_Success,
 *	  TestSessionsSkill_Execute_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/sessions_test.go
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

func TestSessionsSkill_Metadata(t *testing.T) {
	s := NewSessionsSkill(mock.NewMockDriver())
	if s.Name() != "sessions" {
		t.Errorf("Name() = %q, want sessions", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.CLIDef().Command != "sessions" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestSessionsSkill_Execute_Success(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SESSIONS",
		result: &db.QueryResult{
			Columns: []string{"SESS_ID", "USER_NAME", "STATE", "TRX_ID", "CLNT_IP", "OSNAME", "CLNT_TYPE", "CREATE_TIME"},
			Rows: [][]any{
				{int64(140304461655816), "OPENDB", "ACTIVE", int64(2458068), "127.0.0.1", "linux", "GO", "2026-05-02 09:48:14"},
				{int64(140304461655900), "SYS", "IDLE", int64(0), "127.0.0.1", "linux", "DCI", "2026-05-02 09:30:00"},
			},
		},
	})
	s := NewSessionsSkill(drv)
	r, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Type != skill.ResultText {
		t.Errorf("Type = %v, want ResultText", r.Type)
	}
	assertNotEmpty(t, r.Rendered)
	assertSummaryContains(t, r.Rendered, "total_sessions: 2")
	// 关键回归: kill_session_syntax 必须出现 (DM 杀会话约束)
	assertSummaryContains(t, r.Rendered, "kill_session_syntax: CALL SP_CLOSE_SESSION(<sess_id>)")
	assertSummaryContains(t, r.Rendered, "state_ACTIVE: 1")
	assertSummaryContains(t, r.Rendered, "state_IDLE: 1")
}

func TestSessionsSkill_Execute_Empty(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SESSIONS",
		result: &db.QueryResult{
			Columns: []string{"SESS_ID", "USER_NAME", "STATE", "TRX_ID", "CLNT_IP", "OSNAME", "CLNT_TYPE", "CREATE_TIME"},
			Rows:    [][]any{},
		},
	})
	r, err := NewSessionsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertSummaryContains(t, r.Rendered, "total_sessions: 0")
	assertSummaryContains(t, r.Rendered, "kill_session_syntax: CALL SP_CLOSE_SESSION(<sess_id>)")
}

func TestSessionsSkill_Execute_QueryError(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$SESSIONS",
		err:      errors.New("DM-2007: syntax error"),
	})
	_, err := NewSessionsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
