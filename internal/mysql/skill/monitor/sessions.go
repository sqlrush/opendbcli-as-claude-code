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
 *	  internal/mysql/skill/monitor/sessions.go
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
  ID, USER, HOST, DB,
  COMMAND, TIME, STATE,
  LEFT(INFO, 80) AS INFO
FROM information_schema.PROCESSLIST
ORDER BY TIME DESC`

type SessionsSkill struct {
	driver db.Driver
}

func NewSessionsSkill(driver db.Driver) *SessionsSkill {
	return &SessionsSkill{driver: driver}
}

func (s *SessionsSkill) Name() string        { return "sessions" }
func (s *SessionsSkill) Description() string  { return "所有连接概览" }
func (s *SessionsSkill) Category() string     { return "monitor" }
func (s *SessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/sessions", Examples: []string{"/sessions"}}
}

func (s *SessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sessions",
		Description: "List all MySQL connections with status, command, and current query",
	}
}

func (s *SessionsSkill) Validate(_ skill.Params) error { return nil }

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
