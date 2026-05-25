/*-------------------------------------------------------------------------
 *
 * slots.go
 *	  SlotsSkill shows replication slots and retained WAL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/slots.go
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

const slotsSQL = `SELECT
  slot_name,
  slot_type,
  active,
  restart_lsn::text
FROM pg_replication_slots
ORDER BY slot_name`

// SlotsSkill shows replication slots and retained WAL.
type SlotsSkill struct{ driver db.Driver }

// NewSlotsSkill creates a SlotsSkill backed by the given driver.
func NewSlotsSkill(driver db.Driver) *SlotsSkill {
	return &SlotsSkill{driver: driver}
}

func (s *SlotsSkill) Name() string                       { return "slots" }
func (s *SlotsSkill) Description() string                { return "复制槽状态" }
func (s *SlotsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SlotsSkill) Validate(_ skill.Params) error      { return nil }
func (s *SlotsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/slots"} }
func (s *SlotsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "slots", Description: "Show replication slots with retained WAL size"}
}

func (s *SlotsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, slotsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无复制槽",
			Summary:  "no replication slots",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("复制槽 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个复制槽", len(result.Rows)),
	}, nil
}
