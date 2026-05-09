/*-------------------------------------------------------------------------
 *
 * locks.go
 *	  locks — LocksSkill plus helpers (NewLocksSkill) used by the
 *	  monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/locks.go
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

// locksSQL uses pg_locks self-join instead of pg_blocking_pids() for OpenGauss.
const locksSQL = `SELECT
  blocked.pid AS blocked_pid,
  blocked.usename AS blocked_user,
  LEFT(blocked.query, 80) AS blocked_query,
  blocker.pid AS blocker_pid,
  blocker.usename AS blocker_user,
  LEFT(blocker.query, 80) AS blocker_query,
  CASE WHEN blocked.waiting THEN 'Lock' ELSE NULL END AS wait_type,
  CASE WHEN blocked.waiting THEN 'lock_wait' WHEN blocked.enqueue != '' THEN blocked.enqueue ELSE NULL END AS wait_event
FROM pg_locks bl
JOIN pg_stat_activity blocked ON blocked.pid = bl.pid
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid
JOIN pg_stat_activity blocker ON blocker.pid = kl.pid
WHERE NOT bl.granted`

type LocksSkill struct{ driver db.Driver }

func NewLocksSkill(driver db.Driver) *LocksSkill { return &LocksSkill{driver: driver} }

func (s *LocksSkill) Name() string                      { return "locks" }
func (s *LocksSkill) Description() string                { return "行锁/表锁/Advisory Lock" }
func (s *LocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LocksSkill) Validate(_ skill.Params) error      { return nil }
func (s *LocksSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/locks"} }
func (s *LocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "locks", Description: "Show lock waits with blocker and blocked session info using pg_locks self-join"}
}

func (s *LocksSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, locksSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无锁等待",
			Summary:  "0 locks",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("锁等待 (当前快照) — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("锁等待 (当前快照) — %d 个", len(result.Rows)),
	}, nil
}
