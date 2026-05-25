/*-------------------------------------------------------------------------
 *
 * indexhealth_test.go
 *	  Test cases for indexhealth.go (monitor package):
 *	  TestIndexHealthSkill_Metadata, TestIndexHealthSkill_ThreeQueries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/indexhealth_test.go
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

func TestIndexHealthSkill_Metadata(t *testing.T) {
	s := NewIndexHealthSkill(makeRoutedDriver())
	if s.Name() != "indexhealth" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestIndexHealthSkill_ThreeQueries(t *testing.T) {
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		switch {
		case strings.Contains(sql, "STATUS != 'VALID'"):
			return &db.QueryResult{
				Columns: []string{"OWNER", "INDEX_NAME", "TABLE_NAME", "INDEX_TYPE", "STATUS"},
				Rows: [][]any{
					{"OPENDB", "IDX_BAD", "BENCH_USERS", "NORMAL", "INVALID"},
				},
			}, nil
		case strings.Contains(sql, "SEGMENT_TYPE = 'INDEX'"):
			return &db.QueryResult{
				Columns: []string{"OWNER", "INDEX_NAME", "TABLESPACE_NAME", "SIZE_MB"},
				Rows: [][]any{
					{"OPENDB", "PK_BIG_TABLE", "MAIN", int64(120)},
					{"OPENDB", "IDX_STATUS", "MAIN", int64(50)},
				},
			}, nil
		case strings.Contains(sql, "DBA_IND_COLUMNS"):
			return &db.QueryResult{
				Columns: []string{"OWNER", "INDEX_NAME", "TABLE_NAME"},
				Rows:    [][]any{},
			}, nil
		}
		return &db.QueryResult{}, nil
	}
	r, _ := NewIndexHealthSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "invalid_count: 1")
	assertSummaryContains(t, r.Rendered, "large_count: 2")
	assertSummaryContains(t, r.Rendered, "unused_count: 0")
	assertSummaryContains(t, r.Rendered, "largest_index_mb: 120")
}
