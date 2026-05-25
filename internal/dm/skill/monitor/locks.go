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
 *	  internal/dm/skill/monitor/locks.go
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

const locksSQL = `SELECT TRX_ID, TID, LTYPE, LMODE, BLOCKED, TABLE_ID, ROW_IDX, THRD_ID
FROM V$LOCK
ORDER BY BLOCKED DESC, TID`

type LocksSkill struct{ driver db.Driver }

func NewLocksSkill(driver db.Driver) *LocksSkill { return &LocksSkill{driver: driver} }

func (s *LocksSkill) Name() string                       { return "locks" }
func (s *LocksSkill) Description() string                { return "锁信息 (V$LOCK, BLOCKED=1 表示阻塞中)" }
func (s *LocksSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LocksSkill) Validate(_ skill.Params) error      { return nil }
func (s *LocksSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "locks", Description: "List DM locks (BLOCKED=1 means waiting)"}
}
func (s *LocksSkill) CLIDef() skill.CLIDef { return skill.CLIDef{Command: "locks", Usage: "/locks"} }

func (s *LocksSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, locksSQL)
	if err != nil {
		return nil, fmt.Errorf("dm locks: %w", err)
	}

	// TRX_ID(0), TID(1), LTYPE(2), LMODE(3), BLOCKED(4), TABLE_ID(5)
	blocked := 0
	ltypes := dmutil.CountByCol(r.Rows, 2)
	lmodes := dmutil.CountByCol(r.Rows, 3)
	for _, row := range r.Rows {
		if len(row) > 4 && fmt.Sprintf("%v", row[4]) == "1" {
			blocked++
		}
	}

	entries := []dmutil.SummaryEntry{
		{Key: "total_locks", Val: len(r.Rows)},
		{Key: "blocked_count", Val: blocked},
	}
	for t, n := range ltypes {
		entries = append(entries, dmutil.SummaryEntry{Key: "ltype_" + t, Val: n})
	}
	for m, n := range lmodes {
		entries = append(entries, dmutil.SummaryEntry{Key: "lmode_" + m, Val: n})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("锁数 — %d (blocked %d)", len(r.Rows), blocked),
	}, nil
}
