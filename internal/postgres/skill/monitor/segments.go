/*-------------------------------------------------------------------------
 *
 * segments.go
 *	  SegmentsSkill shows top tables by size.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/segments.go
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

const segmentsSQL = `SELECT
  schemaname,
  relname,
  pg_total_relation_size(schemaname || '.' || relname) / 1048576 AS total_mb,
  pg_table_size(schemaname || '.' || relname) / 1048576 AS table_mb,
  pg_indexes_size(schemaname || '.' || relname) / 1048576 AS index_mb
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(schemaname || '.' || relname) DESC
LIMIT %d`

// SegmentsSkill shows top tables by size.
type SegmentsSkill struct{ driver db.Driver }

// NewSegmentsSkill creates a SegmentsSkill backed by the given driver.
func NewSegmentsSkill(driver db.Driver) *SegmentsSkill {
	return &SegmentsSkill{driver: driver}
}

func (s *SegmentsSkill) Name() string                       { return "segments" }
func (s *SegmentsSkill) Description() string                { return "表空间大小排行" }
func (s *SegmentsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SegmentsSkill) Validate(_ skill.Params) error      { return nil }

func (s *SegmentsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/segments [limit]",
		Examples: []string{"/segments", "/segments 50"},
	}
}

func (s *SegmentsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "segments",
		Description: "Show top tables by total size (table + indexes)",
		Parameters: map[string]any{
			"limit": map[string]any{"type": "integer", "description": "Max tables to show (default 20)"},
		},
	}
}

func (s *SegmentsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	limit := params.IntOr("limit", 20)
	sqlStr := fmt.Sprintf(segmentsSQL, limit)

	result, err := s.driver.Query(ctx, sqlStr)
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}
	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "无用户表数据",
			Summary:  "no user tables",
		}, nil
	}
	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("表空间 Top %d (按大小降序)", len(result.Rows)),
		Summary:  fmt.Sprintf("Top %d 表 (按大小)", len(result.Rows)),
	}, nil
}
