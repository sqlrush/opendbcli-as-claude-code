/*-------------------------------------------------------------------------
 *
 * views_test.go
 *	  Test cases for views.go (monitor package):
 *	  TestViewsSkill_Metadata, TestViewsSkill_AllViews,
 *	  TestViewsSkill_KeywordFilter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/views_test.go
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

func TestViewsSkill_Metadata(t *testing.T) {
	s := NewViewsSkill(makeRoutedDriver())
	if s.Name() != "views" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestViewsSkill_AllViews(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$DYNAMIC_TABLES",
		result: &db.QueryResult{
			Columns: []string{"NAME", "SCHNAME"},
			Rows: [][]any{
				{"V$SESSIONS", "SYS"},
				{"V$LOCK", "SYS"},
				{"V$DEADLOCK_HISTORY", "SYS"},
				{"V$BUFFERPOOL", "SYS"},
				{"V$INSTANCE", "SYS"},
			},
		},
	})
	r, _ := NewViewsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "view_count: 5")
	assertSummaryContains(t, r.Rendered, "first_view: V$SESSIONS")
	// 主题分类必须有
	assertSummaryContains(t, r.Rendered, "topic_session: 1")
	assertSummaryContains(t, r.Rendered, "topic_lock: 2") // V$LOCK + V$DEADLOCK_HISTORY
	assertSummaryContains(t, r.Rendered, "topic_memory: 1")
	assertSummaryContains(t, r.Rendered, "topic_system: 1")
}

func TestViewsSkill_KeywordFilter(t *testing.T) {
	var captured string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = sql
		return &db.QueryResult{
			Columns: []string{"NAME", "SCHNAME"},
			Rows: [][]any{
				{"V$SESSIONS", "SYS"},
				{"V$SESSION_EVENT", "SYS"},
			},
		}, nil
	}
	r, _ := NewViewsSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "session"}))

	if !strings.Contains(captured, "%SESSION%") {
		t.Errorf("Filter SQL missing %%SESSION%% LIKE. SQL:\n%s", captured)
	}
	assertSummaryContains(t, r.Rendered, "keyword: session")
	assertSummaryContains(t, r.Rendered, "view_count: 2")
}

func TestViewsSkill_NoMatch(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$DYNAMIC_TABLES",
		result: &db.QueryResult{
			Columns: []string{"NAME", "SCHNAME"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewViewsSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "xyzzz"}))
	assertSummaryContains(t, r.Rendered, "view_count: 0")
	assertSummaryContains(t, r.Rendered, "keyword: xyzzz")
}

func TestViewsSkill_RejectInvalidKw(t *testing.T) {
	r, _ := NewViewsSkill(makeRoutedDriver()).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "abc;DROP"}))
	if r.Type != skill.ResultError {
		t.Errorf("expected ResultError, got %v", r.Type)
	}
}

// 关键回归: views skill 必须查 V$DYNAMIC_TABLES (380 项), 不是 SYSOBJECTS (10 项).
func TestViewsSkill_DataSourceIsV_DYNAMIC_TABLES(t *testing.T) {
	var captured string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = sql
		return &db.QueryResult{Columns: []string{"NAME", "SCHNAME"}, Rows: [][]any{}}, nil
	}
	_, _ = NewViewsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(captured, "V$DYNAMIC_TABLES") {
		t.Errorf("views skill should query V$DYNAMIC_TABLES (380 项), got:\n%s", captured)
	}
	if strings.Contains(captured, "FROM SYSOBJECTS") {
		t.Errorf("views skill incorrectly queries SYSOBJECTS (祖传 bug, 只 10 项). SQL:\n%s", captured)
	}
}
