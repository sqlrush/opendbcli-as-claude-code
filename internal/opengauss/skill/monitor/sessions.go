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
 *	  internal/opengauss/skill/monitor/sessions.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const sessionsSQL = `SELECT
  pid, usename, client_addr::text, datname,
  state,
  CASE WHEN waiting THEN 'Lock' ELSE NULL END AS wait_type,
  CASE WHEN waiting THEN 'lock_wait' WHEN enqueue != '' THEN enqueue ELSE NULL END AS wait_event,
  LEFT(query, 80) AS query,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::int AS query_sec
FROM pg_stat_activity
WHERE pid != pg_backend_pid()
ORDER BY query_start NULLS LAST`

type SessionsSkill struct{ driver db.Driver }

func NewSessionsSkill(driver db.Driver) *SessionsSkill { return &SessionsSkill{driver: driver} }

func (s *SessionsSkill) Name() string                          { return "sessions" }
func (s *SessionsSkill) Description() string                    { return "所有连接概览" }
func (s *SessionsSkill) SecurityLevel() skill.SecurityLevel     { return skill.LevelReadOnly }
func (s *SessionsSkill) Validate(_ skill.Params) error          { return nil }
func (s *SessionsSkill) CLIDef() skill.CLIDef                   { return skill.CLIDef{Usage: "/sessions"} }
func (s *SessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sessions", Description: "List all OpenGauss connections with state, wait event, and current query"}
}

func (s *SessionsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, sessionsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	summary := fmt.Sprintf("全部会话概览 — %d 个", len(result.Rows))
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: summary,
		Summary:  fmt.Sprintf("全部会话 — %d 个", len(result.Rows)),
	}, nil
}
