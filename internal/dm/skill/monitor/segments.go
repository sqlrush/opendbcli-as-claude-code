/*-------------------------------------------------------------------------
 *
 * segments.go
 *	  segments — SegmentsSkill plus helpers (NewSegmentsSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/segments.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const segmentsTopSQL = `SELECT OWNER, SEGMENT_NAME, SEGMENT_TYPE,
       TABLESPACE_NAME,
       ROUND(BYTES/1048576) AS SIZE_MB,
       EXTENTS
FROM DBA_SEGMENTS
ORDER BY BYTES DESC
LIMIT 20`

const segmentsOwnerSQL = `SELECT SEGMENT_NAME, SEGMENT_TYPE,
       TABLESPACE_NAME,
       ROUND(BYTES/1048576) AS SIZE_MB,
       EXTENTS
FROM DBA_SEGMENTS
WHERE OWNER = UPPER(?)
ORDER BY BYTES DESC
LIMIT 20`

type SegmentsSkill struct{ driver db.Driver }

func NewSegmentsSkill(driver db.Driver) *SegmentsSkill { return &SegmentsSkill{driver: driver} }

func (s *SegmentsSkill) Name() string                       { return "segments" }
func (s *SegmentsSkill) Description() string                { return "段空间 Top 20 (DBA_SEGMENTS)" }
func (s *SegmentsSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *SegmentsSkill) Validate(_ skill.Params) error      { return nil }

func (s *SegmentsSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "segments", Description: "Show top segments by size"}
}
func (s *SegmentsSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "segments",
		Usage:    "/segments [owner]",
		Examples: []string{"/segments", "/segments OPENDB"},
	}
}

func (s *SegmentsSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	var (
		query    string
		bindArgs []any
		label    string
	)
	if args != "" {
		query, bindArgs, label = segmentsOwnerSQL, []any{args}, fmt.Sprintf("%s 段空间 Top 20", strings.ToUpper(args))
	} else {
		query, label = segmentsTopSQL, "段空间 Top 20"
	}

	r, err := s.driver.Query(ctx, query, bindArgs...)
	if err != nil {
		return nil, fmt.Errorf("dm segments: %w", err)
	}

	entries := []dmutil.SummaryEntry{
		{Key: "scope", Val: label},
		{Key: "row_count", Val: len(r.Rows)},
	}
	if len(r.Rows) > 0 && len(r.Rows[0]) >= 5 {
		// SIZE_MB 在第 5 列 (DBA_SEGMENTS) 或第 4 列 (按 owner 查询)
		sizeIdx := 4
		if args != "" {
			sizeIdx = 3
		}
		if sizeIdx < len(r.Rows[0]) {
			entries = append(entries, dmutil.SummaryEntry{
				Key: "largest_size_mb",
				Val: fmt.Sprintf("%v", r.Rows[0][sizeIdx]),
			})
		}
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     r,
		Rendered: dmutil.FormatTableWithSummary(format.FormatTable(r), entries),
		Summary:  fmt.Sprintf("%s — %d 个", label, len(r.Rows)),
	}, nil
}
