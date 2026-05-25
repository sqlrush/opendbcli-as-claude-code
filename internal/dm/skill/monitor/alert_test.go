/*-------------------------------------------------------------------------
 *
 * alert_test.go
 *	  Test cases for alert.go (monitor package):
 *	  TestAlertSkill_Metadata, TestAlertSkill_AllCounters,
 *	  TestAlertSkill_SQL_DangerEvent_OPTIME.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/alert_test.go
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

func TestAlertSkill_Metadata(t *testing.T) {
	s := NewAlertSkill(makeRoutedDriver())
	if s.Name() != "alert" {
		t.Errorf("Name() = %q", s.Name())
	}
}

func TestAlertSkill_AllCounters(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$DEADLOCK_HISTORY", result: scalarQR(int64(766))},
		sqlMatcher{contains: "V$DANGER_EVENT", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$RUNTIME_ERR_HISTORY", result: scalarQR(int64(770))},
		sqlMatcher{contains: "V$LOCK", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$LONG_EXEC_SQLS", result: scalarQR(int64(1000))},
	)
	r, _ := NewAlertSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	for _, want := range []string{
		"deadlock_total: 766",
		"danger_event_total: 0",
		"runtime_err_total: 770",
		"blocked_session_count: 0",
		"long_sql_count: 1000",
	} {
		if !strings.Contains(r.Rendered, want) {
			t.Errorf("Rendered missing %q\n%s", want, r.Rendered)
		}
	}
}

// 关键回归: V$DANGER_EVENT 必须 ORDER BY OPTIME 不是 HAPPEN_TIME.
// 这是 task 13 真机验证暴露的祖传 bug.
func TestAlertSkill_SQL_DangerEvent_OPTIME(t *testing.T) {
	var sqls []string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		sqls = append(sqls, sql)
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewAlertSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))

	dangerSQLFound := false
	for _, sql := range sqls {
		if strings.Contains(sql, "V$DANGER_EVENT") && strings.Contains(sql, "ORDER BY") {
			dangerSQLFound = true
			if !strings.Contains(sql, "OPTIME") {
				t.Errorf("V$DANGER_EVENT must order by OPTIME. SQL:\n%s", sql)
			}
			if strings.Contains(sql, "HAPPEN_TIME") {
				t.Errorf("V$DANGER_EVENT incorrectly references HAPPEN_TIME (祖传 bug). SQL:\n%s", sql)
			}
		}
	}
	if !dangerSQLFound {
		t.Error("V$DANGER_EVENT ORDER BY query was never issued")
	}
}
