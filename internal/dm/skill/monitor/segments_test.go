/*-------------------------------------------------------------------------
 *
 * segments_test.go
 *	  Test cases for segments.go (monitor package):
 *	  TestSegmentsSkill_Metadata, TestSegmentsSkill_TopBySize,
 *	  TestSegmentsSkill_ByOwner.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/segments_test.go
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

func TestSegmentsSkill_Metadata(t *testing.T) {
	s := NewSegmentsSkill(makeRoutedDriver())
	if s.Name() != "segments" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestSegmentsSkill_TopBySize(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "DBA_SEGMENTS",
		result: &db.QueryResult{
			Columns: []string{"OWNER", "SEGMENT_NAME", "SEGMENT_TYPE", "TABLESPACE_NAME", "SIZE_MB", "EXTENTS"},
			Rows: [][]any{
				{"OPENDB", "BENCH_DM_USERS", "TABLE", "MAIN", int64(120), int64(15)},
				{"OPENDB", "BENCH_DM_A", "TABLE", "MAIN", int64(80), int64(10)},
			},
		},
	})
	r, _ := NewSegmentsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "scope: 段空间 Top 20")
	assertSummaryContains(t, r.Rendered, "row_count: 2")
	assertSummaryContains(t, r.Rendered, "largest_size_mb: 120")
}

func TestSegmentsSkill_ByOwner(t *testing.T) {
	var captured string
	var args []any
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, a ...any) (*db.QueryResult, error) {
		captured = sql
		args = a
		return &db.QueryResult{
			Columns: []string{"SEGMENT_NAME", "SEGMENT_TYPE", "TABLESPACE_NAME", "SIZE_MB", "EXTENTS"},
			Rows: [][]any{
				{"USERS", "TABLE", "MAIN", int64(50), int64(8)},
			},
		}, nil
	}
	r, _ := NewSegmentsSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "OPENDB"}))

	// 必须用 owner-filter 的 SQL (含 WHERE OWNER = ?)
	if !strings.Contains(captured, "WHERE OWNER") {
		t.Errorf("byOwner SQL missing WHERE OWNER clause: %s", captured)
	}
	if len(args) == 0 || args[0] != "OPENDB" {
		t.Errorf("byOwner SQL bind args = %v, want [OPENDB]", args)
	}
	assertSummaryContains(t, r.Rendered, "scope: OPENDB 段空间 Top 20")
}

func TestSegmentsSkill_Empty(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "DBA_SEGMENTS",
		result: &db.QueryResult{
			Columns: []string{"OWNER", "SEGMENT_NAME", "SEGMENT_TYPE", "TABLESPACE_NAME", "SIZE_MB", "EXTENTS"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewSegmentsSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "row_count: 0")
}
