/*-------------------------------------------------------------------------
 *
 * indexhealth.go
 *	  IndexHealthSkill checks unused / invalid / duplicate indexes —
 *	  expanded from the old "unused only" version so it actually matches
 *	  Oracle's indexhealth coverage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/indexhealth.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// Unused indexes (no scans since stats reset). Filters OG internal
// schemas so WDR snapshot indexes don't dominate.
var ihUnusedSQL = `SELECT
  schemaname || '.' || relname       AS table_name,
  indexrelname                        AS index_name,
  idx_scan,
  pg_size_pretty(pg_relation_size(indexrelid)) AS size
FROM pg_stat_user_indexes
WHERE idx_scan = 0
  AND ` + systemSchemaFilter + `
ORDER BY pg_relation_size(indexrelid) DESC
LIMIT %d`

// Invalid / not-ready indexes — these will never be used by the planner.
const ihInvalidSQL = `SELECT
  n.nspname || '.' || t.relname     AS table_name,
  i.relname                          AS index_name,
  CASE WHEN NOT x.indisvalid THEN 'invalid'
       WHEN NOT x.indisready THEN 'not ready'
       ELSE 'other' END              AS state
FROM pg_index x
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE (NOT x.indisvalid OR NOT x.indisready)
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
ORDER BY n.nspname, t.relname
LIMIT 20`

// Duplicate indexes — same column list, different index names. They cost
// write amplification with zero read benefit.
const ihDuplicateSQL = `SELECT
  n.nspname || '.' || t.relname          AS table_name,
  array_agg(i.relname ORDER BY i.relname) AS duplicate_indexes,
  COUNT(*)                                AS dup_count
FROM pg_index x
JOIN pg_class i ON i.oid = x.indexrelid
JOIN pg_class t ON t.oid = x.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
GROUP BY n.nspname, t.relname, x.indkey
HAVING COUNT(*) > 1
ORDER BY COUNT(*) DESC
LIMIT 20`

// Bloated indexes — rough estimate via size vs. heuristic min.
var ihBloatSQL = `SELECT
  schemaname || '.' || relname AS table_name,
  indexrelname                  AS index_name,
  pg_size_pretty(pg_relation_size(indexrelid)) AS size,
  idx_scan
FROM pg_stat_user_indexes
WHERE pg_relation_size(indexrelid) > 10 * 1024 * 1024
  AND ` + systemSchemaFilter + `
ORDER BY pg_relation_size(indexrelid) DESC
LIMIT 10`

// IndexHealthSkill checks unused / invalid / duplicate indexes — expanded
// from the old "unused only" version so it actually matches Oracle's
// indexhealth coverage.
type IndexHealthSkill struct{ driver db.Driver }

// NewIndexHealthSkill creates an IndexHealthSkill.
func NewIndexHealthSkill(driver db.Driver) *IndexHealthSkill {
	return &IndexHealthSkill{driver: driver}
}

func (s *IndexHealthSkill) Name() string                       { return "indexhealth" }
func (s *IndexHealthSkill) Description() string                { return "索引健康（未用/非法/重复/膨胀）" }
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
		Description: "Multi-dimension index health: unused / invalid / duplicate / large",
		Parameters: map[string]any{
			"limit": map[string]any{"type": "integer", "description": "Max rows per section (default 20)"},
		},
	}
}

func (s *IndexHealthSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	limit := params.IntOr("limit", 20)

	unused, _ := s.driver.Query(ctx, fmt.Sprintf(ihUnusedSQL, limit))
	invalid, _ := s.driver.Query(ctx, ihInvalidSQL)
	dup, _ := s.driver.Query(ctx, ihDuplicateSQL)
	bloat, _ := s.driver.Query(ctx, ihBloatSQL)

	sections := []format.PanelSection{}

	if invalid != nil && len(invalid.Rows) > 0 {
		lines := []string{fmt.Sprintf("%d 个索引处于 invalid/not-ready 状态，planner 不会使用", len(invalid.Rows))}
		for i, r := range invalid.Rows {
			if i >= 5 {
				lines = append(lines, fmt.Sprintf("  ... 另有 %d 个", len(invalid.Rows)-5))
				break
			}
			lines = append(lines, fmt.Sprintf("  %v.%v  %v", r[0], r[1], r[2]))
		}
		sections = append(sections, format.PanelSection{Header: "⚠ Invalid / Not-Ready", Lines: lines})
	}

	if dup != nil && len(dup.Rows) > 0 {
		lines := []string{fmt.Sprintf("%d 个表存在重复索引（相同列组合）", len(dup.Rows))}
		for i, r := range dup.Rows {
			if i >= 5 {
				lines = append(lines, fmt.Sprintf("  ... 另有 %d 个", len(dup.Rows)-5))
				break
			}
			lines = append(lines, fmt.Sprintf("  %v  ×%v", r[0], r[2]))
		}
		sections = append(sections, format.PanelSection{Header: "⚠ Duplicate", Lines: lines})
	}

	if unused != nil && len(unused.Rows) > 0 {
		lines := []string{fmt.Sprintf("%d 个未使用索引（idx_scan=0）", len(unused.Rows))}
		sections = append(sections, format.PanelSection{Header: "Unused", Lines: lines})
	}
	if bloat != nil && len(bloat.Rows) > 0 {
		lines := []string{fmt.Sprintf("%d 个索引大于 10MB（可能膨胀候选）", len(bloat.Rows))}
		sections = append(sections, format.PanelSection{Header: "Large", Lines: lines})
	}

	if len(sections) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "索引健康：无异常（无未使用 / 无 invalid / 无重复 / 无明显膨胀）",
			Summary:  "all indexes healthy",
		}, nil
	}

	rendered := format.Panel("Index Health", sections)
	// Primary data is still the unused list since it's the most common
	// actionable dataset.
	primary := unused
	if primary == nil || len(primary.Rows) == 0 {
		primary = invalid
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     primary,
		Rendered: rendered,
		Summary:  fmt.Sprintf("unused=%d invalid=%d dup=%d large=%d", rowCount(unused), rowCount(invalid), rowCount(dup), rowCount(bloat)),
	}, nil
}

func rowCount(qr *db.QueryResult) int {
	if qr == nil {
		return 0
	}
	return len(qr.Rows)
}
