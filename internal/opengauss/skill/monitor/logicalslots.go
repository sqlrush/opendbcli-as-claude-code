/*-------------------------------------------------------------------------
 *
 * logicalslots.go
 *	  LogicalSlotsSkill shows logical replication slots (complements
 *	  /slots).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/logicalslots.go
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

// logicalSlotsSQL filters pg_replication_slots for logical slots and adds
// retained WAL size + xmin so we can spot slots blocking VACUUM.
//
// OG-specific column names differ from vanilla PG:
//   - pg_replication_slots.confirmed_flush (not confirmed_flush_lsn)
//   - pg_current_wal_lsn() → pg_current_xlog_location()
//   - pg_wal_lsn_diff() → pg_xlog_location_diff()
const logicalSlotsSQL = `SELECT
  slot_name,
  plugin,
  database,
  active,
  restart_lsn::text,
  confirmed_flush::text,
  catalog_xmin,
  pg_size_pretty(
    pg_xlog_location_diff(
      pg_current_xlog_location(),
      COALESCE(restart_lsn, pg_current_xlog_location())
    )::bigint
  ) AS retained_wal
FROM pg_replication_slots
WHERE slot_type = 'logical'
ORDER BY slot_name`

// LogicalSlotsSkill shows logical replication slots (complements /slots).
type LogicalSlotsSkill struct{ driver db.Driver }

// NewLogicalSlotsSkill creates a LogicalSlotsSkill.
func NewLogicalSlotsSkill(driver db.Driver) *LogicalSlotsSkill {
	return &LogicalSlotsSkill{driver: driver}
}

func (s *LogicalSlotsSkill) Name() string                       { return "logicalslots" }
func (s *LogicalSlotsSkill) Description() string                { return "逻辑复制槽" }
func (s *LogicalSlotsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *LogicalSlotsSkill) Validate(_ skill.Params) error      { return nil }
func (s *LogicalSlotsSkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/logicalslots"} }

func (s *LogicalSlotsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "logicalslots",
		Description: "List logical replication slots with retained WAL and catalog xmin (can block VACUUM)",
	}
}

func (s *LogicalSlotsSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, logicalSlotsSQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if result == nil || len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无逻辑复制槽",
			Summary:  "no logical slots",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("逻辑复制槽 — %d 个", len(result.Rows)),
		Summary:  fmt.Sprintf("%d logical slots", len(result.Rows)),
	}, nil
}
