/*-------------------------------------------------------------------------
 *
 * lwlocks.go
 *	  LWLocksSkill shows LWLock contention grouped by tranche.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/lwlocks.go
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

// lwlocksSQL aggregates thread waiters from pg_thread_wait_status — OG's
// native view for concurrent wait analysis. Unlike vanilla PG which put
// everything on pg_stat_activity.wait_event_type='LWLock', OG exposes a
// richer taxonomy through wait_status (one of: none / "acquire lwlock" /
// "acquire lock" / "wait event" / data-structure-specific statuses like
// "HashAgg - build hash") alongside a wait_event string.
//
// We surface all non-idle waits so the LLM / user can see exactly what
// the contention is, not just LWLock waits. The skill name is kept as
// /lwlocks for compatibility with the P0 skill plan even though the view
// covers broader OG wait signals.
// lwlocksSQL filters out our own collector session (it naturally shows up
// with "HashAgg - build hash" while running this very SELECT) so the skill
// doesn't pretend there's a wait when the instance is idle.
const lwlocksSQL = `SELECT
  thread_name,
  wait_status,
  COALESCE(NULLIF(wait_event, ''), '-') AS wait_event,
  COUNT(*) AS waiters,
  MIN(tid) AS sample_tid
FROM pg_thread_wait_status
WHERE wait_status IS NOT NULL
  AND wait_status <> 'none'
  AND sessionid <> pg_backend_pid()
GROUP BY thread_name, wait_status, wait_event
ORDER BY waiters DESC, thread_name`

// LWLocksSkill shows LWLock contention grouped by tranche.
type LWLocksSkill struct{ driver db.Driver }

// NewLWLocksSkill creates an LWLocksSkill.
func NewLWLocksSkill(driver db.Driver) *LWLocksSkill { return &LWLocksSkill{driver: driver} }

func (s *LWLocksSkill) Name() string                       { return "lwlocks" }
func (s *LWLocksSkill) Description() string                { return "LWLock 轻量锁争用" }
func (s *LWLocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LWLocksSkill) Validate(_ skill.Params) error      { return nil }
func (s *LWLocksSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/lwlocks"} }

func (s *LWLocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "lwlocks",
		Description: "Show LWLock contention grouped by tranche (BufferContent, WALInsert, ProcArray, etc.)",
	}
}

func (s *LWLocksSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, lwlocksSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无 LWLock 等待",
			Summary:  "no LWLock contention",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("LWLock 争用 — %d 种", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 种 LWLock 有等待", len(result.Rows)),
	}, nil
}
