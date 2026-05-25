/*-------------------------------------------------------------------------
 *
 * resource_test.go
 *	  Test cases for resource.go (monitor package):
 *	  TestResourceSkill_Metadata, TestResourceSkill_ParamsAndUsage,
 *	  TestResourceSkill_DoesNotQuery_V_RESOURCE_LIMIT.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/resource_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestResourceSkill_Metadata(t *testing.T) {
	s := NewResourceSkill(makeRoutedDriver())
	if s.Name() != "resource" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestResourceSkill_ParamsAndUsage(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$PARAMETER",
			result: &db.QueryResult{
				Columns: []string{"NAME", "VALUE"},
				Rows: [][]any{
					{"MAX_SESSIONS", "600"},
					{"MEMORY_TARGET", "3000"},
					{"BUFFER", "15000"},
					{"WORKER_THREADS", "12"},
				},
			},
		},
		sqlMatcher{
			contains: "FROM DUAL",
			result: &db.QueryResult{
				Columns: []string{"SESSIONS_USED", "SESSIONS_ACTIVE", "TRX_USED", "MEMORY_USED_MB"},
				Rows:    [][]any{{int64(15), int64(3), int64(8), int64(3897)}},
			},
		},
	)
	r, _ := NewResourceSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "param_count: 4")
	assertSummaryContains(t, r.Rendered, "limit_MAX_SESSIONS: 600")
	assertSummaryContains(t, r.Rendered, "limit_MEMORY_TARGET: 3000")
	assertSummaryContains(t, r.Rendered, "sessions_used: 15")
	assertSummaryContains(t, r.Rendered, "sessions_active: 3")
	assertSummaryContains(t, r.Rendered, "memory_used_mb: 3897")
	assertSummaryContains(t, r.Rendered, "sessions_limit: 600")
}

// 关键回归: V$RESOURCE_LIMIT 在 DM 不存在 (Oracle 才有).
// resource skill 必须用 V$PARAMETER, 绝不能查 V$RESOURCE_LIMIT.
func TestResourceSkill_DoesNotQuery_V_RESOURCE_LIMIT(t *testing.T) {
	var captured []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = append(captured, sql)
		return &db.QueryResult{
			Columns: []string{"NAME", "VALUE"},
			Rows:    [][]any{},
		}, nil
	}
	_, _ = NewResourceSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	for _, sql := range captured {
		if strings.Contains(sql, "V$RESOURCE_LIMIT") {
			t.Errorf("resource skill must not query V$RESOURCE_LIMIT (DM 没有此视图). SQL:\n%s", sql)
		}
	}
	// 必须有 V$PARAMETER 查询
	hasParam := false
	for _, sql := range captured {
		if strings.Contains(sql, "V$PARAMETER") {
			hasParam = true
		}
	}
	if !hasParam {
		t.Errorf("resource skill must query V$PARAMETER, got SQLs: %v", captured)
	}
}
