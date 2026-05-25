/*-------------------------------------------------------------------------
 *
 * standby_test.go
 *	  Test cases for standby.go (monitor package):
 *	  TestStandbySkill_Metadata, TestStandbySkill_PrimarySingleNode,
 *	  TestTranslateDBRole.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/standby_test.go
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

func TestStandbySkill_Metadata(t *testing.T) {
	s := NewStandbySkill(makeRoutedDriver())
	if s.Name() != "standby" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestStandbySkill_PrimarySingleNode(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$DATABASE",
			result: &db.QueryResult{
				Columns: []string{"NAME", "ROLE", "STATUS", "ARCH_MODE", "LAST_CKPT_TIME", "LAST_STARTUP_TIME"},
				Rows: [][]any{
					{"DAMENG", int64(0), int64(4), "N", "2026-05-02 11:03:45", "2026-05-02 08:33:45"},
				},
			},
		},
		sqlMatcher{
			contains: "V$RLOG",
			result: &db.QueryResult{
				Columns: []string{"CUR_FILE", "FILE_LSN", "CKPT_LSN", "FREE_SPACE"},
				Rows:    [][]any{{int64(0), int64(17687462), int64(17687425), int64(536772608)}},
			},
		},
		sqlMatcher{
			contains: "V$ARCH_SEND_INFO",
			result: &db.QueryResult{
				Columns: []string{"DEST", "ARCH_STATUS", "LAST_SEND_FILE", "LAST_SEND_LSN"},
				Rows:    [][]any{},
			},
		},
	)
	r, _ := NewStandbySkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "db_name: DAMENG")
	// 关键: ROLE$=0 必须翻译为 PRIMARY 字符串, raw 保留方便排查
	assertSummaryContains(t, r.Rendered, "role: PRIMARY")
	assertSummaryContains(t, r.Rendered, "role_raw: 0")
	// STATUS$=4 翻译为 OPEN
	assertSummaryContains(t, r.Rendered, "status: OPEN")
	assertSummaryContains(t, r.Rendered, "status_raw: 4")
	assertSummaryContains(t, r.Rendered, "arch_mode: N")
	assertSummaryContains(t, r.Rendered, "current_lsn: 17687462")
	assertSummaryContains(t, r.Rendered, "ckpt_lsn: 17687425")
}

func TestTranslateDBRole(t *testing.T) {
	tests := map[string]string{
		"0":   "PRIMARY",
		"1":   "STANDBY",
		"2":   "DBSTANDBY",
		"3":   "BACKUP_PENDING",
		"99":  "99", // 未知保留 raw
		"":    "",
		"abc": "abc",
	}
	for in, want := range tests {
		if got := translateDBRole(in); got != want {
			t.Errorf("translateDBRole(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTranslateDBStatus(t *testing.T) {
	tests := map[string]string{
		"1":  "STARTUP",
		"2":  "AFTER_REDO",
		"3":  "BACKUP",
		"4":  "OPEN",
		"5":  "SUSPEND",
		"99": "99",
	}
	for in, want := range tests {
		if got := translateDBStatus(in); got != want {
			t.Errorf("translateDBStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// 关键回归: V$DATABASE 没有 DBID 列 (Oracle 才有), 必须不能在 SQL 中引用.
func TestStandbySkill_SQL_NoDBID(t *testing.T) {
	var captured []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		captured = append(captured, sql)
		return &db.QueryResult{}, nil
	}
	_, _ = NewStandbySkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	for _, sql := range captured {
		if strings.Contains(sql, "V$DATABASE") && strings.Contains(sql, "DBID") {
			t.Errorf("V$DATABASE SQL incorrectly references DBID (DM 没有此列). SQL:\n%s", sql)
		}
	}
}
