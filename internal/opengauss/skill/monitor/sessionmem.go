/*-------------------------------------------------------------------------
 *
 * sessionmem.go
 *	  sessionmem — SessionMemSkill plus helpers (NewSessionMemSkill)
 *	  used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/sessionmem.go
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

// sessionMemSQL extracts the thread_id portion from sessid
// (`<session_id>.<thread_id>`) so operators can correlate back to
// pg_stat_activity.pid quickly.
const sessionMemSQL = `SELECT sessid,
  SPLIT_PART(sessid, '.', 2) AS pid,
  ROUND(SUM(usedsize)/1048576::numeric, 2) AS used_mb,
  ROUND(SUM(totalsize)/1048576::numeric, 2) AS total_mb
FROM gs_session_memory_detail
GROUP BY sessid
ORDER BY SUM(totalsize) DESC
LIMIT 20`

type SessionMemSkill struct{ driver db.Driver }

func NewSessionMemSkill(driver db.Driver) *SessionMemSkill { return &SessionMemSkill{driver: driver} }

func (s *SessionMemSkill) Name() string                      { return "sessionmem" }
func (s *SessionMemSkill) Description() string                { return "会话内存使用" }
func (s *SessionMemSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SessionMemSkill) Validate(_ skill.Params) error      { return nil }
func (s *SessionMemSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/sessionmem"} }
func (s *SessionMemSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "sessionmem", Description: "Show per-session memory usage from gs_session_memory_detail"}
}

func (s *SessionMemSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, sessionMemSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: "gs_session_memory_detail 不可用: " + err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无会话内存数据",
			Summary:  "no session memory data",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("会话内存 Top %d", len(result.Rows)),
		Summary:  fmt.Sprintf("Top %d 会话内存", len(result.Rows)),
	}, nil
}
