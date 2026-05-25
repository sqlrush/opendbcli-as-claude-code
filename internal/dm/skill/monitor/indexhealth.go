/*-------------------------------------------------------------------------
 *
 * indexhealth.go
 *	  indexhealth — IndexHealthSkill plus helpers
 *	  (NewIndexHealthSkill) used by the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/indexhealth.go
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

// 失效/无效索引: SYSINDEXES.VALID = 'N'
// DM 兼容 Oracle 字典视图 DBA_INDEXES, 但 VALID 状态在 SYS.SYSINDEXES (用 ID 关联)
const indexInvalidSQL = `SELECT i.OWNER, i.INDEX_NAME, i.TABLE_NAME, i.INDEX_TYPE, i.STATUS
FROM DBA_INDEXES i
WHERE i.STATUS != 'VALID'
ORDER BY i.OWNER, i.INDEX_NAME
LIMIT 50`

const indexLargeSQL = `SELECT s.OWNER, s.SEGMENT_NAME AS INDEX_NAME, s.TABLESPACE_NAME,
       ROUND(s.BYTES/1048576) AS SIZE_MB
FROM DBA_SEGMENTS s
WHERE s.SEGMENT_TYPE = 'INDEX'
ORDER BY s.BYTES DESC
LIMIT 20`

const indexUnusedSQL = `SELECT i.OWNER, i.INDEX_NAME, i.TABLE_NAME
FROM DBA_INDEXES i
WHERE NOT EXISTS (
  SELECT 1 FROM DBA_IND_COLUMNS c
  WHERE c.INDEX_OWNER = i.OWNER AND c.INDEX_NAME = i.INDEX_NAME
)
LIMIT 30`

type IndexHealthSkill struct{ driver db.Driver }

func NewIndexHealthSkill(driver db.Driver) *IndexHealthSkill { return &IndexHealthSkill{driver: driver} }

func (s *IndexHealthSkill) Name() string                       { return "indexhealth" }
func (s *IndexHealthSkill) Description() string                { return "失效/超大/空索引 (DBA_INDEXES + DBA_SEGMENTS)" }
func (s *IndexHealthSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *IndexHealthSkill) Validate(_ skill.Params) error      { return nil }

func (s *IndexHealthSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "indexhealth", Description: "Show DM invalid/large/unused indexes"}
}
func (s *IndexHealthSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "indexhealth", Usage: "/indexhealth"}
}

func (s *IndexHealthSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	invalid, err := s.driver.Query(ctx, indexInvalidSQL)
	if err != nil {
		return nil, fmt.Errorf("dm indexhealth invalid: %w", err)
	}
	large, err := s.driver.Query(ctx, indexLargeSQL)
	if err != nil {
		return nil, fmt.Errorf("dm indexhealth large: %w", err)
	}
	unused, unusedErr := s.driver.Query(ctx, indexUnusedSQL)

	var b strings.Builder
	b.WriteString("=== 失效索引 (STATUS != VALID) ===\n")
	b.WriteString(format.FormatTable(invalid))
	b.WriteString("\n=== 大索引 Top 20 (按段大小) ===\n")
	b.WriteString(format.FormatTable(large))
	if unusedErr == nil && unused != nil {
		b.WriteString("\n=== 空索引 (无字段定义) ===\n")
		b.WriteString(format.FormatTable(unused))
	}

	entries := []dmutil.SummaryEntry{
		{Key: "invalid_count", Val: len(invalid.Rows)},
		{Key: "large_count", Val: len(large.Rows)},
	}
	if unusedErr == nil && unused != nil {
		entries = append(entries, dmutil.SummaryEntry{Key: "unused_count", Val: len(unused.Rows)})
	}
	if len(large.Rows) > 0 && len(large.Rows[0]) >= 4 {
		entries = append(entries, dmutil.SummaryEntry{
			Key: "largest_index_mb",
			Val: fmt.Sprintf("%v", large.Rows[0][3]),
		})
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     invalid,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("失效 %d / 大索引 %d", len(invalid.Rows), len(large.Rows)),
	}, nil
}
