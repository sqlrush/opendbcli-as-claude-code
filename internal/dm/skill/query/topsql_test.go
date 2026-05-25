/*-------------------------------------------------------------------------
 *
 * topsql_test.go
 *	  Test cases for topsql.go (query package):
 *	  TestTopSQLSkill_Metadata, TestTopSQLSkill_Execute_Success,
 *	  TestTopSQLSkill_Execute_Empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/topsql_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestTopSQLSkill_Metadata(t *testing.T) {
	s := NewTopSQLSkill(mock.NewMockDriver())
	if s.Name() != "topsql" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
	if s.CLIDef().Command != "topsql" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestTopSQLSkill_Execute_Success(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		// 关键回归: 必须查 V$SQL_HISTORY 不能误用 Oracle V$SQLAREA
		if !strings.Contains(sql, "V$SQL_HISTORY") {
			t.Errorf("topsql SQL must query V$SQL_HISTORY (DM 视图). Got:\n%s", sql)
		}
		if strings.Contains(sql, "V$SQLAREA") {
			t.Errorf("topsql SQL must not query V$SQLAREA (Oracle 视图). Got:\n%s", sql)
		}
		// 必须 GROUP BY SQL_ID (V$SQL_HISTORY 是单次执行历史)
		if !strings.Contains(sql, "GROUP BY SQL_ID") {
			t.Errorf("topsql SQL missing GROUP BY SQL_ID. Got:\n%s", sql)
		}
		return &db.QueryResult{
			Columns: []string{"SQL_ID", "EXEC_COUNT", "TOTAL_TIME_MS", "AVG_TIME_MS", "SAMPLE_SQL"},
			Rows: [][]any{
				{int64(2353), int64(4977), int64(124611000), int64(25030), "SELECT COUNT(*) FROM bench_users WHERE status=3"},
				{int64(4435), int64(1200), int64(60000), int64(50), "UPDATE bench_a SET v=v+1"},
			},
		}, nil
	}
	r, err := NewTopSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if r.Type != skill.ResultText {
		t.Errorf("Type = %v, want ResultText", r.Type)
	}
	// summary 必须含 hottest_* 关键字段, LLM 用来定位最热 SQL
	want := []string{
		"unique_sql_count: 2",
		"hottest_sql_id: 2353",
		"hottest_exec_count: 4977",
		"hottest_avg_time_ms: 25030",
	}
	for _, w := range want {
		if !strings.Contains(r.Rendered, w) {
			t.Errorf("Rendered missing %q. Got:\n%s", w, r.Rendered)
		}
	}
}

func TestTopSQLSkill_Execute_Empty(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"SQL_ID", "EXEC_COUNT", "TOTAL_TIME_MS", "AVG_TIME_MS", "SAMPLE_SQL"},
			Rows:    [][]any{},
		}, nil
	}
	r, _ := NewTopSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(r.Rendered, "unique_sql_count: 0") {
		t.Errorf("Rendered missing unique_sql_count: 0\n%s", r.Rendered)
	}
	// 没数据时不应该有 hottest_* 字段
	if strings.Contains(r.Rendered, "hottest_") {
		t.Errorf("Rendered should not have hottest_* on empty result")
	}
}

func TestTopSQLSkill_Execute_QueryError(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("DM-2007: V$SQL_HISTORY not accessible")
	}
	_, err := NewTopSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error")
	}
}
