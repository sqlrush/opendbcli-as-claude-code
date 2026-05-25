/*-------------------------------------------------------------------------
 *
 * os_test.go
 *	  Test cases for os.go (monitor package): TestOSSkill_Metadata,
 *	  TestOSSkill_FullSnapshot, TestOSSkill_SQL_UsesCorrectColumns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/os_test.go
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

func TestOSSkill_Metadata(t *testing.T) {
	s := NewOSSkill(makeRoutedDriver())
	if s.Name() != "os" {
		t.Errorf("Name() = %q", s.Name())
	}
	// CLI alias
	hasOsstat := false
	for _, a := range s.CLIDef().Aliases {
		if a == "osstat" {
			hasOsstat = true
		}
	}
	if !hasOsstat {
		t.Errorf("CLIDef().Aliases missing 'osstat'")
	}
}

func TestOSSkill_FullSnapshot(t *testing.T) {
	drv := makeRoutedDriver(
		sqlMatcher{
			contains: "V$INSTANCE",
			result: &db.QueryResult{
				Columns: []string{"NAME", "INSTANCE_NAME", "HOST_NAME", "VERSION", "START_TIME", "STATUS", "MODE"},
				Rows: [][]any{
					{"DAMENG", "DM01", "iZrj9ds", "DM Database Server x64 V8", "2026-05-02 08:33:43", "OPEN", "NORMAL"},
				},
			},
		},
		sqlMatcher{
			contains: "V$THREADS",
			result: &db.QueryResult{
				Columns: []string{"THREAD_TYPE", "COUNT"},
				Rows: [][]any{
					{"dm_osio_thd", int64(32)},
					{"dm_tskwrk_thd", int64(16)},
				},
			},
		},
		sqlMatcher{
			contains: "V$PROCESS",
			result: &db.QueryResult{
				Columns: []string{"PROCESS_COUNT"},
				Rows:    [][]any{{int64(5)}},
			},
		},
		sqlMatcher{
			contains: "V$MEM_POOL",
			result: &db.QueryResult{
				Columns: []string{"POOL_COUNT", "TOTAL_MB"},
				Rows:    [][]any{{int64(82), int64(3898)}},
			},
		},
	)
	r, _ := NewOSSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "instance_name: DM01")
	assertSummaryContains(t, r.Rendered, "host_name: iZrj9ds")
	assertSummaryContains(t, r.Rendered, "thread_kinds: 2")
	assertSummaryContains(t, r.Rendered, "process_count: 5")
	assertSummaryContains(t, r.Rendered, "memory_pool_total_mb: 3898")
	assertSummaryContains(t, r.Rendered, "status: OPEN")
}

// 关键回归: V$INSTANCE 必须用 SVR_VERSION (作为 VERSION 别名), 不能直接 SELECT VERSION.
// V$INSTANCE.STATUS$ 必须用别名转 STATUS (列名带 $ 后缀).
func TestOSSkill_SQL_UsesCorrectColumns(t *testing.T) {
	var captured string
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		if strings.Contains(sql, "V$INSTANCE") {
			captured = sql
		}
		return &db.QueryResult{Columns: []string{"X"}, Rows: [][]any{}}, nil
	}
	_, _ = NewOSSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if captured == "" {
		t.Fatal("V$INSTANCE was never queried")
	}
	if !strings.Contains(captured, "SVR_VERSION") {
		t.Errorf("V$INSTANCE SQL must use SVR_VERSION (DM 没有 VERSION 列). SQL:\n%s", captured)
	}
	if !strings.Contains(captured, "STATUS$") {
		t.Errorf("V$INSTANCE SQL must reference STATUS$ (列名带 $ 后缀). SQL:\n%s", captured)
	}
}
