/*-------------------------------------------------------------------------
 *
 * sortusage.go
 *	  SortUsageSkill shows sort segment usage and top temp consumers.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/sortusage.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const sortSegmentSQL = `SELECT tablespace_name,
       total_blocks, used_blocks, free_blocks,
       ROUND(used_blocks * 100.0 / NULLIF(total_blocks, 0), 1) AS used_pct
FROM v$sort_segment`

const sortConsumersSQL = `SELECT NVL(username, 'N/A') AS username, segtype,
       COUNT(*) AS segments,
       SUM(blocks) * (SELECT value FROM v$parameter WHERE name='db_block_size') / 1048576 AS mb
FROM v$tempseg_usage
GROUP BY username, segtype
ORDER BY mb DESC
FETCH FIRST 20 ROWS ONLY`

// SortUsageSkill shows sort segment usage and top temp consumers.
type SortUsageSkill struct {
	driver db.Driver
}

// NewSortUsageSkill creates a SortUsageSkill backed by the given driver.
func NewSortUsageSkill(driver db.Driver) *SortUsageSkill {
	return &SortUsageSkill{driver: driver}
}

func (s *SortUsageSkill) Name() string                      { return "sortusage" }
func (s *SortUsageSkill) Description() string               { return "Show sort segment usage and top temp consumers" }
func (s *SortUsageSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *SortUsageSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "sortusage",
		Description: "Show sort segment usage and top temp consumers",
	}
}

func (s *SortUsageSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command: "sortusage",
		Usage:   "/sortusage",
	}
}

func (s *SortUsageSkill) Validate(_ skill.Params) error { return nil }

func (s *SortUsageSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	segResult, err := s.driver.Query(ctx, sortSegmentSQL)
	if err != nil {
		return nil, fmt.Errorf("querying sort segments: %w", err)
	}

	consResult, err := s.driver.Query(ctx, sortConsumersSQL)
	if err != nil {
		return nil, fmt.Errorf("querying temp segment consumers: %w", err)
	}

	rendered := suRender(segResult, consResult)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d sort segments, %d consumers", len(segResult.Rows), len(consResult.Rows)),
	}, nil
}

func suRender(segResult, consResult *db.QueryResult) string {
	opts := format.TableOptions{MaxRows: 50, TermWidth: 120}
	var b strings.Builder

	b.WriteString("── 排序段使用 ──\n\n")
	if len(segResult.Rows) > 0 {
		b.WriteString(format.FormatTableOpts(segResult, opts))
	} else {
		b.WriteString("  (无排序段数据)\n")
	}

	b.WriteString("\n── 排序段详情 ──\n\n")
	if len(consResult.Rows) > 0 {
		b.WriteString(format.FormatTableOpts(consResult, opts))
	} else {
		b.WriteString("  (无临时段使用)\n")
	}

	b.WriteString("\n提示: /tempsess 查看对应会话\n")

	return b.String()
}
