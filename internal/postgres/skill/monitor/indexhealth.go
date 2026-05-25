/*-------------------------------------------------------------------------
 *
 * indexhealth.go
 *	  IndexHealthSkill shows unused indexes (zero scans).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/indexhealth.go
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

const indexHealthSQL = `SELECT
  schemaname,
  relname,
  indexrelname,
  idx_scan,
  pg_relation_size(indexrelid) / 1048576 AS size_mb
FROM pg_stat_user_indexes
WHERE idx_scan = 0
ORDER BY pg_relation_size(indexrelid) DESC
LIMIT %d`

// P05: Index bloat estimation query.
const indexBloatSQL = `SELECT
  schemaname, relname, indexrelname,
  pg_relation_size(indexrelid) / 1048576 AS actual_mb,
  CASE WHEN idx_scan > 0 THEN 'USED' ELSE 'UNUSED' END AS usage,
  idx_scan
FROM pg_stat_user_indexes
WHERE pg_relation_size(indexrelid) > 10485760
ORDER BY pg_relation_size(indexrelid) DESC
LIMIT 20`

// IndexHealthSkill shows unused indexes (zero scans).
type IndexHealthSkill struct{ driver db.Driver }

// NewIndexHealthSkill creates an IndexHealthSkill backed by the given driver.
func NewIndexHealthSkill(driver db.Driver) *IndexHealthSkill {
	return &IndexHealthSkill{driver: driver}
}

func (s *IndexHealthSkill) Name() string                       { return "indexhealth" }
func (s *IndexHealthSkill) Description() string                { return "未使用的索引" }
func (s *IndexHealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *IndexHealthSkill) Validate(_ skill.Params) error      { return nil }

func (s *IndexHealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/indexhealth [limit]",
		Examples: []string{"/indexhealth", "/indexhealth 50"},
	}
}

func (s *IndexHealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "indexhealth",
		Description: "Show unused indexes (zero scans since stats reset) ordered by size",
		Parameters: map[string]any{
			"limit": map[string]any{"type": "integer", "description": "Max rows (default 20)"},
		},
	}
}

func (s *IndexHealthSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	limit := params.IntOr("limit", 20)
	sqlStr := fmt.Sprintf(indexHealthSQL, limit)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "所有索引均有使用 (idx_scan > 0)",
			Summary:  "all indexes used",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("未使用索引 — %d 个 (idx_scan = 0, 按大小降序)", len(result.Rows)),
		Summary:  fmt.Sprintf("索引健康: %d 个未使用索引", len(result.Rows)),
	}, nil
}
