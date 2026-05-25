/*-------------------------------------------------------------------------
 *
 * slowsql_test.go
 *	  Test cases for slowsql.go (query package):
 *	  TestSlowSQLSkill_Metadata, TestSlowSQLSkill_DataSource,
 *	  TestSlowSQLSkill_WithLongSQLs.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/query/slowsql_test.go
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

func TestSlowSQLSkill_Metadata(t *testing.T) {
	s := NewSlowSQLSkill(mock.NewMockDriver())
	if s.Name() != "slowsql" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.CLIDef().Command != "slowsql" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
}

// 关键回归: slowsql 查 V$LONG_EXEC_SQLS (实时), 不是 V$SQL_HISTORY (累积).
// 这是 task 16 benchmark 暴露的 deepseek bug 教训.
func TestSlowSQLSkill_DataSource(t *testing.T) {
	var captured string
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = sql
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewSlowSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(captured, "V$LONG_EXEC_SQLS") {
		t.Errorf("slowsql must query V$LONG_EXEC_SQLS (实时长 SQL). Got:\n%s", captured)
	}
	if strings.Contains(captured, "V$SQL_HISTORY") {
		t.Errorf("slowsql must NOT query V$SQL_HISTORY (累积值, 不是实时). Got:\n%s", captured)
	}
}

func TestSlowSQLSkill_WithLongSQLs(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{
			Columns: []string{"SESS_ID", "EXEC_TIME", "SQL_TEXT"},
			Rows: [][]any{
				{int64(140999111), int64(60), "SELECT COUNT(*) FROM bench_users WHERE status=3"},
				{int64(140999222), int64(45), "UPDATE bench_a SET v=v+1"},
			},
		}, nil
	}
	r, _ := NewSlowSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if !strings.Contains(r.Rendered, "long_sql_count: 2") {
		t.Errorf("Rendered missing long_sql_count: 2\n%s", r.Rendered)
	}
}

func TestSlowSQLSkill_Empty(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	r, _ := NewSlowSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	// 空结果走 "当前无长 SQL" 分支
	if !strings.Contains(r.Rendered, "当前无长 SQL") {
		t.Errorf("Rendered missing empty-state message\n%s", r.Rendered)
	}
	if !strings.Contains(r.Rendered, "long_sql_count: 0") {
		t.Errorf("Rendered missing long_sql_count: 0")
	}
}

func TestSlowSQLSkill_QueryError(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return nil, errors.New("DM-2111: invalid view")
	}
	_, err := NewSlowSQLSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error")
	}
}
