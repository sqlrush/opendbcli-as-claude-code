/*-------------------------------------------------------------------------
 *
 * bloat.go
 *	  BloatSkill shows table bloat estimation based on dead tuple ratio.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/bloat.go
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

const bloatSQL = `SELECT
  schemaname,
  relname,
  n_live_tup,
  n_dead_tup,
  CASE WHEN n_live_tup > 0
    THEN ROUND(n_dead_tup::numeric / n_live_tup * 100, 1)
    ELSE 0
  END AS dead_pct,
  pg_total_relation_size(schemaname || '.' || relname) / 1048576 AS total_mb,
  last_autovacuum::text,
  last_autoanalyze::text
FROM pg_stat_user_tables
WHERE n_dead_tup > 0
  AND n_live_tup > 0
  AND ROUND(n_dead_tup::numeric / n_live_tup * 100, 1) > %d
ORDER BY n_dead_tup DESC
LIMIT %d`

// BloatSkill shows table bloat estimation based on dead tuple ratio.
type BloatSkill struct{ driver db.Driver }

// NewBloatSkill creates a BloatSkill backed by the given driver.
func NewBloatSkill(driver db.Driver) *BloatSkill {
	return &BloatSkill{driver: driver}
}

func (s *BloatSkill) Name() string                       { return "bloat" }
func (s *BloatSkill) Description() string                { return "表膨胀估算" }
func (s *BloatSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *BloatSkill) Validate(_ skill.Params) error      { return nil }

func (s *BloatSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/bloat [threshold_pct] [limit]",
		Examples: []string{"/bloat", "/bloat 10", "/bloat 5 50"},
	}
}

func (s *BloatSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "bloat",
		Description: "Show table/index bloat estimation based on dead tuple ratio",
		Parameters: map[string]any{
			"threshold_pct": map[string]any{"type": "integer", "description": "Min dead tuple percentage (default 5)"},
			"limit":         map[string]any{"type": "integer", "description": "Max rows (default 30)"},
		},
	}
}

func (s *BloatSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	threshold := params.IntOr("threshold_pct", 5)
	limit := params.IntOr("limit", 30)

	sqlStr := fmt.Sprintf(bloatSQL, threshold, limit)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("无超过 %d%% dead tuple 比例的表", threshold),
			Summary:  "no bloated tables",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("表膨胀 (dead > %d%%) — %d 个", threshold, len(result.Rows)),
		Summary:  fmt.Sprintf("%d 个膨胀表", len(result.Rows)),
	}, nil
}
