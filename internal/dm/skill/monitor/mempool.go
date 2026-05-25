/*-------------------------------------------------------------------------
 *
 * mempool.go
 *	  mempool — MemPoolSkill plus helpers (NewMemPoolSkill) used by
 *	  the monitor package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/mempool.go
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

// V$MEM_POOL 实测列: NAME, IS_OVERFLOW, ORG_SIZE, TOTAL_SIZE, RESERVED_SIZE, ...
const memPoolSQL = `SELECT NAME, IS_OVERFLOW,
       ROUND(TOTAL_SIZE/1024/1024) AS TOTAL_MB,
       ROUND(RESERVED_SIZE/1024/1024) AS RESERVED_MB
FROM V$MEM_POOL
ORDER BY TOTAL_SIZE DESC
LIMIT 20`

// V$BUFFERPOOL 实测列: NAME, N_PAGES, FREE, RECYCLED, RAT_HIT, ...
const bufferPoolSQL = `SELECT NAME, N_PAGES, FREE, RECYCLED, RAT_HIT
FROM V$BUFFERPOOL`

// V$DICT_CACHE 实测列: TYPE_NAME, USED, MAX_USED, RAT_HIT, ...
const dictCacheSQL = `SELECT TYPE_NAME, USED, MAX_USED, RAT_HIT
FROM V$DICT_CACHE
ORDER BY USED DESC`

type MemPoolSkill struct{ driver db.Driver }

func NewMemPoolSkill(driver db.Driver) *MemPoolSkill { return &MemPoolSkill{driver: driver} }

func (s *MemPoolSkill) Name() string                       { return "mempool" }
func (s *MemPoolSkill) Description() string                { return "内存池 + 缓冲池 + 字典缓存命中率 (V$MEM_POOL / V$BUFFERPOOL / V$DICT_CACHE)" }
func (s *MemPoolSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *MemPoolSkill) Validate(_ skill.Params) error      { return nil }

func (s *MemPoolSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "mempool", Description: "Show DM memory pool, buffer pool, dict cache"}
}
func (s *MemPoolSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "mempool", Usage: "/mempool"}
}

func (s *MemPoolSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	mem, err := s.driver.Query(ctx, memPoolSQL)
	if err != nil {
		return nil, fmt.Errorf("dm mempool: %w", err)
	}
	buf, bufErr := s.driver.Query(ctx, bufferPoolSQL)
	dict, dictErr := s.driver.Query(ctx, dictCacheSQL)

	var b strings.Builder
	b.WriteString("=== 内存池 Top 20 (V$MEM_POOL) ===\n")
	b.WriteString(format.FormatTable(mem))
	if bufErr == nil && buf != nil {
		b.WriteString("\n=== 缓冲池 (V$BUFFERPOOL) ===\n")
		b.WriteString(format.FormatTable(buf))
	}
	if dictErr == nil && dict != nil {
		b.WriteString("\n=== 字典缓存 (V$DICT_CACHE) ===\n")
		b.WriteString(format.FormatTable(dict))
	}

	entries := []dmutil.SummaryEntry{
		{Key: "mem_pool_count", Val: len(mem.Rows)},
	}
	if len(mem.Rows) > 0 && len(mem.Rows[0]) >= 4 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "largest_pool", Val: fmt.Sprintf("%v", mem.Rows[0][0])},
			dmutil.SummaryEntry{Key: "largest_pool_mb", Val: fmt.Sprintf("%v", mem.Rows[0][2])},
		)
	}
	if bufErr == nil && len(buf.Rows) > 0 && len(buf.Rows[0]) >= 5 {
		// 找命中率最低的 buffer pool
		minHit := ""
		minHitName := ""
		for _, row := range buf.Rows {
			if len(row) < 5 {
				continue
			}
			hit := fmt.Sprintf("%v", row[4])
			name := fmt.Sprintf("%v", row[0])
			if minHit == "" || hit < minHit {
				minHit = hit
				minHitName = name
			}
		}
		entries = append(entries,
			dmutil.SummaryEntry{Key: "min_buf_hit_pool", Val: minHitName},
			dmutil.SummaryEntry{Key: "min_buf_hit_rate", Val: minHit},
		)
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     mem,
		Rendered: dmutil.FormatTableWithSummary(b.String(), entries),
		Summary:  fmt.Sprintf("内存池 %d 个", len(mem.Rows)),
	}, nil
}
