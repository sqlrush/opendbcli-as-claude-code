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
 *	  internal/mysql/skill/monitor/segments.go
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
  TABLE_SCHEMA,
  TABLE_NAME,
  ENGINE,
  ROUND((DATA_LENGTH + INDEX_LENGTH) / 1048576, 2) AS size_mb,
  TABLE_ROWS
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY DATA_LENGTH + INDEX_LENGTH DESC
LIMIT %d`

// SegmentsSkill shows top tables by size.
type SegmentsSkill struct {
	driver db.Driver
}

// NewSegmentsSkill creates a SegmentsSkill backed by the given driver.
func NewSegmentsSkill(driver db.Driver) *SegmentsSkill {
	return &SegmentsSkill{driver: driver}
}

func (s *SegmentsSkill) Name() string                       { return "segments" }
func (s *SegmentsSkill) Description() string                { return "表空间大小排行" }
func (s *SegmentsSkill) Category() string                   { return "monitor" }
func (s *SegmentsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SegmentsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:    "/segments [limit]",
		Examples: []string{"/segments", "/segments 50"},
	}
}

func (s *SegmentsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "segments",
		Description: "Show top tables by size (data + index) across all user schemas",
		Parameters:  map[string]any{"limit": map[string]any{"type": "integer", "description": "Number of tables to show (default 20)"}},
	}
}

func (s *SegmentsSkill) Validate(_ skill.Params) error { return nil }

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
