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
 *	  internal/mysql/skill/monitor/locks.go
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

const locksSQL = `SELECT
  r.PROCESSLIST_ID AS blocked_pid,
  r.PROCESSLIST_USER AS blocked_user,
  r.PROCESSLIST_INFO AS blocked_query,
  b.PROCESSLIST_ID AS blocker_pid,
  b.PROCESSLIST_USER AS blocker_user,
  b.PROCESSLIST_INFO AS blocker_query,
  w.REQUESTING_ENGINE_LOCK_ID AS lock_id,
  w.BLOCKING_ENGINE_LOCK_ID AS blocking_lock_id
FROM performance_schema.data_lock_waits w
JOIN performance_schema.threads r ON r.THREAD_ID = w.REQUESTING_THREAD_ID
JOIN performance_schema.threads b ON b.THREAD_ID = w.BLOCKING_THREAD_ID`

type LocksSkill struct {
	driver db.Driver
}

func NewLocksSkill(driver db.Driver) *LocksSkill {
	return &LocksSkill{driver: driver}
}

func (s *LocksSkill) Name() string        { return "locks" }
func (s *LocksSkill) Description() string  { return "InnoDB行锁/MDL锁" }
func (s *LocksSkill) Category() string     { return "monitor" }
func (s *LocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *LocksSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/locks", Examples: []string{"/locks"}}
}

func (s *LocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "locks",
		Description: "Show InnoDB row lock waits with blocker and blocked thread info",
	}
}

func (s *LocksSkill) Validate(_ skill.Params) error { return nil }

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
		Rendered: fmt.Sprintf("InnoDB 锁等待 (当前快照) — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("锁等待 (当前快照) — %d 个", len(result.Rows)),
	}, nil
}
