/*-------------------------------------------------------------------------
 *
 * info_test.go
 *	  Test cases for info.go (monitor package): TestInfoSkill_Metadata,
 *	  TestInfoSkill_PrimaryRole, TestInfoSkill_StandbyRole.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/info_test.go
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

func TestInfoSkill_Metadata(t *testing.T) {
	s := NewInfoSkill(makeRoutedDriver())
	if s.Name() != "info" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
}

func TestInfoSkill_PrimaryRole(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$INSTANCE",
			result: &db.QueryResult{
				Columns: []string{"INSTANCE_NAME", "BUILD_VERSION", "START_TIME"},
				Rows:    [][]any{{"DM01", "20260301", "2026-05-02 08:33:43"}},
			},
		},
		sqlMatcher{
			contains: "V$DATABASE",
			result: &db.QueryResult{
				Columns: []string{"NAME", "ROLE$"},
				Rows:    [][]any{{"DAMENG", int64(0)}}, // 0 = PRIMARY
			},
		},
		sqlMatcher{
			contains: "V$DM_INI",
			result: &db.QueryResult{
				Columns: []string{"PARA_NAME", "PARA_VALUE"},
				Rows: [][]any{
					{"COMPATIBLE_MODE", "2"},
					{"PORT_NUM", "5237"},
					{"WORKER_THREADS", "12"},
				},
			},
		},
	)
	r, _ := NewInfoSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "instance_name: DM01")
	assertSummaryContains(t, r.Rendered, "build_version: 20260301")
	assertSummaryContains(t, r.Rendered, "db_name: DAMENG")
	// 关键: ROLE$=0 必须翻译为 PRIMARY 字符串 (LLM 不应看到数字 0)
	assertSummaryContains(t, r.Rendered, "role: PRIMARY")
	// 参数也带前缀进 summary
	assertSummaryContains(t, r.Rendered, "param_PORT_NUM: 5237")
	assertSummaryContains(t, r.Rendered, "param_COMPATIBLE_MODE: 2")
}

func TestInfoSkill_StandbyRole(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$INSTANCE",
			result: &db.QueryResult{
				Columns: []string{"INSTANCE_NAME", "BUILD_VERSION", "START_TIME"},
				Rows:    [][]any{{"DM02", "20260301", "2026-05-02"}},
			},
		},
		sqlMatcher{
			contains: "V$DATABASE",
			result: &db.QueryResult{
				Columns: []string{"NAME", "ROLE$"},
				Rows:    [][]any{{"DAMENG", int64(1)}}, // 1 = STANDBY
			},
		},
		sqlMatcher{
			contains: "V$DM_INI",
			result: &db.QueryResult{
				Columns: []string{"PARA_NAME", "PARA_VALUE"},
				Rows:    [][]any{},
			},
		},
	)
	r, _ := NewInfoSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "role: STANDBY")
}

// 关键回归: V$DATABASE 必须查 ROLE$, 不是 ROLE (Oracle).
// V$INSTANCE 必须查 BUILD_VERSION, 不是 VERSION.
func TestInfoSkill_SQL_Columns(t *testing.T) {
	var sqls []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		sqls = append(sqls, sql)
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewInfoSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	for _, sql := range sqls {
		if strings.Contains(sql, "V$DATABASE") && !strings.Contains(sql, "ROLE$") {
			t.Errorf("V$DATABASE SQL must use ROLE$ column. Got:\n%s", sql)
		}
		// V$INSTANCE 不能用 VERSION 单数 (DM 用 SVR_VERSION/DB_VERSION/BUILD_VERSION)
		if strings.Contains(sql, "V$INSTANCE") {
			// 必须用 BUILD_VERSION 或带后缀的版本字段
			if !strings.Contains(sql, "BUILD_VERSION") && !strings.Contains(sql, "SVR_VERSION") && !strings.Contains(sql, "DB_VERSION") {
				t.Errorf("V$INSTANCE SQL missing BUILD_VERSION/SVR_VERSION/DB_VERSION. Got:\n%s", sql)
			}
		}
	}
}
