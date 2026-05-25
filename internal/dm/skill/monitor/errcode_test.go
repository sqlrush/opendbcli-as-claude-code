/*-------------------------------------------------------------------------
 *
 * errcode_test.go
 *	  Test cases for errcode.go (monitor package):
 *	  TestErrCodeSkill_Metadata, TestErrCodeSkill_ByCode_Found,
 *	  TestErrCodeSkill_ByCode_NotFound.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/errcode_test.go
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

func TestErrCodeSkill_Metadata(t *testing.T) {
	s := NewErrCodeSkill(makeRoutedDriver())
	if s.Name() != "errcode" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.CLIDef().Command != "errcode" {
		t.Errorf("CLIDef().Command = %q", s.CLIDef().Command)
	}
}

func TestErrCodeSkill_ByCode_Found(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$ERR_INFO",
		result: &db.QueryResult{
			Columns: []string{"CODE", "ERRINFO"}, // 关键: 实测列名仅 2 列
			Rows: [][]any{
				{int64(-2622), "分区名与数据库对象名称冲突"},
			},
		},
	})
	r, err := NewErrCodeSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "2622"}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertSummaryContains(t, r.Rendered, "found: true")
	assertSummaryContains(t, r.Rendered, "match_count: 1")
	assertSummaryContains(t, r.Rendered, "分区名与数据库对象名称冲突")
}

func TestErrCodeSkill_ByCode_NotFound(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$ERR_INFO",
		result: &db.QueryResult{
			Columns: []string{"CODE", "ERRINFO"},
			Rows:    [][]any{},
		},
	})
	r, _ := NewErrCodeSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "99999"}))
	assertSummaryContains(t, r.Rendered, "found: false")
	assertSummaryContains(t, r.Rendered, "searched_code: 99999")
}

func TestErrCodeSkill_ByDesc_FuzzySearch(t *testing.T) {
	drv := makeRoutedDriver(sqlMatcher{
		contains: "V$ERR_INFO",
		result: &db.QueryResult{
			Columns: []string{"CODE", "ERRINFO"},
			Rows: [][]any{
				{int64(-6403), "对象上发生死锁"},
				{int64(-6404), "事务死锁"},
			},
		},
	})
	r, _ := NewErrCodeSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "死锁"}))
	assertSummaryContains(t, r.Rendered, "match_count: 2")
}

func TestErrCodeSkill_ByDesc_RejectInvalidChars(t *testing.T) {
	r, _ := NewErrCodeSkill(makeRoutedDriver()).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "abc; DROP TABLE"}))
	if r.Type != skill.ResultError {
		t.Errorf("expected ResultError for invalid chars, got %v", r.Type)
	}
}

// 关键回归: V$ERR_INFO 必须只用 CODE 和 ERRINFO 两列, 不能用 ERR_CODE/ERR_LEVEL/ERR_TYPE/ERR_DESC.
func TestErrCodeSkill_SQL_UsesCorrectColumns(t *testing.T) {
	var captured []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = append(captured, sql)
		return &db.QueryResult{Columns: []string{"CODE", "ERRINFO"}, Rows: [][]any{}}, nil
	}
	_, _ = NewErrCodeSkill(drv).Execute(context.Background(),
		skill.ParamsFromMap(map[string]any{"args": "2622"}))

	for _, sql := range captured {
		// 错误的 Oracle-style 列名不能出现
		for _, bad := range []string{"ERR_CODE", "ERR_LEVEL", "ERR_TYPE", "ERR_DESC"} {
			if strings.Contains(sql, bad) {
				t.Errorf("V$ERR_INFO SQL contains forbidden column %s (祖传 bug). SQL:\n%s", bad, sql)
			}
		}
		// 正确列名必须出现
		if strings.Contains(sql, "V$ERR_INFO") {
			if !strings.Contains(sql, "CODE") || !strings.Contains(sql, "ERRINFO") {
				t.Errorf("V$ERR_INFO SQL missing CODE or ERRINFO. SQL:\n%s", sql)
			}
		}
	}
}
