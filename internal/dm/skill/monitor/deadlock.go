/*-------------------------------------------------------------------------
 *
 * deadlock.go
 *	  deadlock — DeadlockSkill plus helpers (NewDeadlockSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/deadlock.go
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

const deadlockSQL = `SELECT * FROM V$DEADLOCK_HISTORY ORDER BY HAPPEN_TIME DESC LIMIT 50`

type DeadlockSkill struct{ driver db.Driver }

func NewDeadlockSkill(driver db.Driver) *DeadlockSkill { return &DeadlockSkill{driver: driver} }

func (s *DeadlockSkill) Name() string                       { return "deadlock" }
func (s *DeadlockSkill) Description() string                { return "历史死锁 (V$DEADLOCK_HISTORY)" }
func (s *DeadlockSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *DeadlockSkill) Validate(_ skill.Params) error      { return nil }
func (s *DeadlockSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "deadlock", Description: "Show DM deadlock history"}
}
func (s *DeadlockSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "deadlock", Usage: "/deadlock"}
}

func (s *DeadlockSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, deadlockSQL)
	if err != nil {
		return nil, fmt.Errorf("dm deadlock: %w", err)
	}
	if len(r.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无历史死锁\n[summary]\ndeadlock_count: 0\n",
		}, nil
	}
	entries := []dmutil.SummaryEntry{
		{Key: "deadlock_count", Val: len(r.Rows)},
		{Key: "latest_deadlock", Val: r.Rows[0]},
	}
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("历史死锁 — %d 条", len(r.Rows)),
	}, nil
}
