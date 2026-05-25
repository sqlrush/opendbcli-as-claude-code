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
 *	  internal/postgres/skill/monitor/locks.go
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

// P04: Enhanced locks SQL with lock mode and danger level annotation.
const locksSQL = `SELECT
  blocked.pid AS blocked_pid,
  blocked.usename AS blocked_user,
  LEFT(blocked.query, 80) AS blocked_query,
  blocker.pid AS blocker_pid,
  blocker.usename AS blocker_user,
  LEFT(blocker.query, 80) AS blocker_query,
  blocked.wait_event_type,
  blocked.wait_event,
  l.mode AS lock_mode,
  CASE
    WHEN l.mode = 'AccessExclusiveLock' THEN '⚠️ CRITICAL: 阻塞所有操作包括SELECT'
    WHEN l.mode = 'ExclusiveLock' THEN '⚠️ HIGH: 阻塞所有写操作'
    WHEN l.mode IN ('ShareLock', 'ShareRowExclusiveLock') THEN 'MEDIUM: 阻塞写操作'
    ELSE 'LOW'
  END AS danger_level
FROM pg_stat_activity blocked
JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS bp(pid) ON true
JOIN pg_stat_activity blocker ON blocker.pid = bp.pid
LEFT JOIN pg_locks l ON l.pid = blocker.pid AND l.granted = true
  AND l.relation IS NOT NULL
WHERE blocked.wait_event_type = 'Lock'`

type LocksSkill struct{ driver db.Driver }

func NewLocksSkill(driver db.Driver) *LocksSkill { return &LocksSkill{driver: driver} }

func (s *LocksSkill) Name() string                      { return "locks" }
func (s *LocksSkill) Description() string                { return "行锁/表锁/Advisory Lock" }
func (s *LocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LocksSkill) Validate(_ skill.Params) error      { return nil }
func (s *LocksSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/locks"} }
func (s *LocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "locks", Description: "Show lock waits with blocker and blocked session info using pg_blocking_pids()"}
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
