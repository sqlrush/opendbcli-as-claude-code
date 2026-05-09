/*-------------------------------------------------------------------------
 *
 * sessions.go
 *	  SessionsSkill shows all database sessions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/sessions.go
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

const sessionsSQL = `SELECT sid, serial#, username, status,
       sql_id, event, seconds_in_wait AS wait_sec
FROM v$session
WHERE username IS NOT NULL
ORDER BY status, seconds_in_wait DESC`

// SessionsSkill shows all database sessions.
type SessionsSkill struct {
	driver db.Driver
}

// NewSessionsSkill creates a SessionsSkill backed by the given driver.
func NewSessionsSkill(driver db.Driver) *SessionsSkill {
	return &SessionsSkill{driver: driver}
}

func (s *SessionsSkill) Name() string        { return "sessions" }
func (s *SessionsSkill) Description() string  { return "Show all database sessions" }
func (s *SessionsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SessionsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sessions",
		Description: "Show all database sessions",
	}
}

func (s *SessionsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sessions",
		Usage:   "/sessions",
	}
}

func (s *SessionsSkill) Validate(_ skill.Params) error { return nil }

func (s *SessionsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, sessionsSQL)
	if err != nil {
		return nil, fmt.Errorf("querying sessions: %w", err)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("全部会话概览 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("全部会话 — %d 个", len(result.Rows)),
	}, nil
}
