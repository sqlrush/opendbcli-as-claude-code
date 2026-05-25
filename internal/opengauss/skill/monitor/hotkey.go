/*-------------------------------------------------------------------------
 *
 * hotkey.go
 *	  HotKeySkill identifies hotspot tables by access counters.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/hotkey.go
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

// hotkeySQL scores tables by write pressure (updates + inserts + deletes)
// and read pressure (seq_scan + idx_scan). Rows at the top combine both —
// they are the candidates for partitioning, denormalisation, or cache layers.
var hotkeySQL = `SELECT
  schemaname || '.' || relname AS table_name,
  seq_scan,
  idx_scan,
  n_tup_ins,
  n_tup_upd,
  n_tup_del,
  n_live_tup,
  n_dead_tup,
  CASE WHEN seq_scan > 0 AND idx_scan = 0 THEN 'seq only'
       WHEN seq_scan > idx_scan * 3      THEN 'seq heavy'
       WHEN n_tup_upd > n_tup_ins * 5    THEN 'update heavy'
       ELSE ''
  END AS flag
FROM pg_stat_all_tables
WHERE ` + systemSchemaFilter + `
ORDER BY (seq_scan + idx_scan + n_tup_ins + n_tup_upd + n_tup_del) DESC
LIMIT 20`

// HotKeySkill identifies hotspot tables by access counters.
type HotKeySkill struct{ driver db.Driver }

// NewHotKeySkill creates a HotKeySkill.
func NewHotKeySkill(driver db.Driver) *HotKeySkill { return &HotKeySkill{driver: driver} }

func (s *HotKeySkill) Name() string                       { return "hotkey" }
func (s *HotKeySkill) Description() string                { return "热点表 Top 20" }
func (s *HotKeySkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *HotKeySkill) Validate(_ skill.Params) error      { return nil }
func (s *HotKeySkill) CLIDef() skill.CLIDef               { return skill.CLIDef{Usage: "/hotkey"} }

func (s *HotKeySkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "hotkey",
		Description: "Identify hotspot tables ranked by combined read/write activity with flags",
	}
}

func (s *HotKeySkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, hotkeySQL)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	rows := 0
	if result != nil {
		rows = len(result.Rows)
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("热点表 — Top %d", rows),
		Summary:  fmt.Sprintf("top %d hotspot tables", rows),
	}, nil
}
