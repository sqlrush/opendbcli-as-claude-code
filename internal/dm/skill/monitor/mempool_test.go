/*-------------------------------------------------------------------------
 *
 * mempool_test.go
 *	  Test cases for mempool.go (monitor package):
 *	  TestMemPoolSkill_Metadata, TestMemPoolSkill_AllThreeViews.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/mempool_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestMemPoolSkill_Metadata(t *testing.T) {
	s := NewMemPoolSkill(makeRoutedDriver())
	if s.Name() != "mempool" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestMemPoolSkill_AllThreeViews(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$MEM_POOL",
			result: &db.QueryResult{
				Columns: []string{"NAME", "IS_OVERFLOW", "TOTAL_MB", "RESERVED_MB"},
				Rows: [][]any{
					{"BUFFER_POOL", "N", int64(2048), int64(512)},
					{"DICT_POOL", "N", int64(128), int64(32)},
				},
			},
		},
		sqlMatcher{
			contains: "V$BUFFERPOOL",
			result: &db.QueryResult{
				Columns: []string{"NAME", "N_PAGES", "FREE", "RECYCLED", "RAT_HIT"},
				Rows: [][]any{
					{"NORMAL", int64(262144), int64(15000), int64(247144), "0.95"},
					{"FAST", int64(8192), int64(8000), int64(192), "0.10"},
				},
			},
		},
		sqlMatcher{
			contains: "V$DICT_CACHE",
			result: &db.QueryResult{
				Columns: []string{"TYPE_NAME", "USED", "MAX_USED", "RAT_HIT"},
				Rows: [][]any{
					{"TABLE", int64(120), int64(150), "0.98"},
				},
			},
		},
	)
	r, _ := NewMemPoolSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "mem_pool_count: 2")
	assertSummaryContains(t, r.Rendered, "largest_pool: BUFFER_POOL")
	assertSummaryContains(t, r.Rendered, "largest_pool_mb: 2048")
	// 命中率最低的池: FAST (0.10)
	assertSummaryContains(t, r.Rendered, "min_buf_hit_pool: FAST")
	assertSummaryContains(t, r.Rendered, "min_buf_hit_rate: 0.10")
}
