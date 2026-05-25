/*-------------------------------------------------------------------------
 *
 * waits.go
 *	  waits — WaitsSkill plus helpers (NewWaitsSkill) used by the
 *	  monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/waits.go
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

// waitsSQL: 系统级累计等待事件 TOP 20
const waitsSQL = `SELECT EVENT, WAIT_CLASS, TOTAL_WAITS,
       TIME_WAITED, TIME_WAITED_MICRO, AVERAGE_WAIT_MICRO
FROM V$SYSTEM_EVENT
WHERE TOTAL_WAITS > 0
ORDER BY TIME_WAITED_MICRO DESC
LIMIT 20`

type WaitsSkill struct{ driver db.Driver }

func NewWaitsSkill(driver db.Driver) *WaitsSkill { return &WaitsSkill{driver: driver} }

func (s *WaitsSkill) Name() string                       { return "waits" }
func (s *WaitsSkill) Description() string                { return "等待事件 TOP (V$SYSTEM_EVENT)" }
func (s *WaitsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *WaitsSkill) Validate(_ skill.Params) error      { return nil }
func (s *WaitsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "waits", Description: "Top wait events (cumulative)"}
}
func (s *WaitsSkill) CLIDef() skill.CLIDef { return skill.CLIDef{Command: "waits", Usage: "/waits"} }

func (s *WaitsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	r, err := s.driver.Query(ctx, waitsSQL)
	if err != nil {
		return nil, fmt.Errorf("dm waits: %w", err)
	}

	// EVENT(0), WAIT_CLASS(1), TOTAL_WAITS(2), TIME_WAITED(3), TIME_WAITED_MICRO(4), AVERAGE_WAIT_MICRO(5)
	entries := []dmutil.SummaryEntry{
		{Key: "wait_event_count", Val: len(r.Rows)},
	}
	if len(r.Rows) > 0 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "top_event", Val: r.Rows[0][0]},
			dmutil.SummaryEntry{Key: "top_event_class", Val: r.Rows[0][1]},
			dmutil.SummaryEntry{Key: "top_event_total_waits", Val: r.Rows[0][2]},
			dmutil.SummaryEntry{Key: "top_event_time_micro", Val: r.Rows[0][4]},
		)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("等待事件 — %d 项", len(r.Rows)),
	}, nil
}
