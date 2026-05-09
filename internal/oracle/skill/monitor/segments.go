/*-------------------------------------------------------------------------
 *
 * segments.go
 *	  SegmentsSkill shows segment-level space analysis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/segments.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

const segmentsTopSQL = `SELECT owner, segment_name, segment_type,
       tablespace_name,
       ROUND(bytes/1048576) AS size_mb,
       extents
FROM dba_segments
ORDER BY bytes DESC
FETCH FIRST 20 ROWS ONLY`

const segmentsOwnerSQL = `SELECT segment_name, segment_type,
       tablespace_name,
       ROUND(bytes/1048576) AS size_mb,
       extents
FROM dba_segments
WHERE owner = UPPER(:1)
ORDER BY bytes DESC
FETCH FIRST 20 ROWS ONLY`

const segmentsGrowthSQL = `SELECT s.obj#,
       o.owner, o.object_name, o.object_type,
       ROUND((s.space_used_delta)/1048576, 2) AS growth_mb
FROM dba_hist_seg_stat s
JOIN dba_objects o ON o.object_id = s.obj#
WHERE s.snap_id = (SELECT MAX(snap_id) FROM dba_hist_snapshot)
  AND s.space_used_delta > 0
ORDER BY s.space_used_delta DESC
FETCH FIRST 20 ROWS ONLY`

// SegmentsSkill shows segment-level space analysis.
type SegmentsSkill struct {
	driver db.Driver
}

// NewSegmentsSkill creates a SegmentsSkill backed by the given driver.
func NewSegmentsSkill(driver db.Driver) *SegmentsSkill {
	return &SegmentsSkill{driver: driver}
}

func (s *SegmentsSkill) Name() string                      { return "segments" }
func (s *SegmentsSkill) Description() string                { return "Show top segments by size or growth" }
func (s *SegmentsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SegmentsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "segments",
		Description: "Show top segments by size or growth",
	}
}

func (s *SegmentsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:        "segments",
		Usage:          "/segments [owner|growth]",
		Examples:       []string{"/segments", "/segments HR", "/segments growth"},
		ArgCompletions: []string{"growth"},
	}
}

func (s *SegmentsSkill) Validate(_ skill.Params) error { return nil }

func (s *SegmentsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	query, bindArgs, summary := segmentsQueryPlan(args)

	result, err := s.driver.Query(ctx, query, bindArgs...)
	if err != nil {
		return nil, fmt.Errorf("querying %s: %w", summary, err)
	}

	return &skill.Result{
		Type:     skill.ResultTable,
		Data:     result,
		Rendered: fmt.Sprintf("%s — %d 个", summary, len(result.Rows)),
		Summary:  fmt.Sprintf("%s — %d 个", summary, len(result.Rows)),
	}, nil
}

// segmentsQueryPlan returns the SQL, bind args, and summary label for the given arguments.
func segmentsQueryPlan(args string) (query string, bindArgs []any, summary string) {
	lower := strings.ToLower(args)

	if lower == "growth" {
		return segmentsGrowthSQL, nil, "段增长 Top 20 (最近 AWR 快照)"
	}

	if args != "" {
		return segmentsOwnerSQL, []any{args}, fmt.Sprintf("%s 段空间 Top 20", asString(args))
	}

	return segmentsTopSQL, nil, "段空间 Top 20 (按大小降序)"
}

// asString returns the input trimmed and uppercased for display purposes.
func asString(v string) string {
	return strings.ToUpper(strings.TrimSpace(v))
}
