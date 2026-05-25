/*-------------------------------------------------------------------------
 *
 * health_test.go
 *	  Test cases for health.go (monitor package):
 *	  TestHealthSkill_Metadata, TestHealthSkill_AllMetricsPresent,
 *	  TestHealthSkill_PartialError.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/health_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"sync"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestHealthSkill_Metadata(t *testing.T) {
	s := NewHealthSkill(makeRoutedDriver())
	if s.Name() != "health" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
}

func TestHealthSkill_AllMetricsPresent(t *testing.T) {
	// health 并发跑 11 条 SQL. 用一个万能 driver, 每条返回 scalar
	drv := makeRoutedDriver(
		sqlMatcher{contains: "INSTANCE_NAME ||", result: scalarQR("DM01 (build x)")},
		sqlMatcher{contains: "START_TIME FROM V$INSTANCE", result: scalarQR("2026-05-02 08:33:43")},
		sqlMatcher{contains: "ROLE$", result: scalarQR("PRIMARY")},
		sqlMatcher{contains: "COUNT(*) FROM V$SESSIONS WHERE STATE", result: scalarQR(int64(3))},
		sqlMatcher{contains: "COUNT(*) FROM V$SESSIONS", result: scalarQR(int64(15))},
		sqlMatcher{contains: "COUNT(DISTINCT TRX_ID) FROM V$LOCK", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$DEADLOCK_HISTORY", result: scalarQR(int64(766))},
		sqlMatcher{contains: "V$DANGER_EVENT", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$RUNTIME_ERR_HISTORY", result: scalarQR(int64(770))},
		sqlMatcher{contains: "V$LONG_EXEC_SQLS", result: scalarQR(int64(1000))},
		sqlMatcher{contains: "V$CKPT_HISTORY", result: scalarQR("2026-05-02 11:03:45")},
	)
	r, err := NewHealthSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNotEmpty(t, r.Rendered)
	// 关键指标 summary keys 必须有
	for _, want := range []string{"实例状态", "主备角色", "会话总数", "累计死锁", "长 SQL 数"} {
		if !contains(r.Rendered, want) {
			t.Errorf("Rendered missing %q. Got:\n%s", want, r.Rendered)
		}
	}
	// 累计死锁: 766 这种值要出现
	if !contains(r.Rendered, "766") {
		t.Errorf("Rendered missing deadlock count 766")
	}
}

func TestHealthSkill_PartialError(t *testing.T) {
	// 个别查询失败不应整体崩溃
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$INSTANCE", result: scalarQR("DM01")},
		sqlMatcher{contains: "V$DATABASE", result: scalarQR("PRIMARY")},
		sqlMatcher{contains: "V$SESSIONS", result: scalarQR(int64(5))},
		// V$LOCK / V$DEADLOCK_HISTORY etc. 由 sqlMatcher 兜底返回空 QR
	)
	r, err := NewHealthSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertNotEmpty(t, r.Rendered)
}

// 关键回归: ROLE$ 数字翻译为 PRIMARY/STANDBY 字符串, 不能直接给 LLM 看到 0/1.
// health skill 用 11 goroutine 并发查询, captured slice 必须加锁.
func TestHealthSkill_RoleTranslation(t *testing.T) {
	var (
		mu       sync.Mutex
		captured []string
	)
	drv := makeRoutedDriver()
	drv.QueryFunc = func(_ context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		mu.Lock()
		captured = append(captured, sql)
		mu.Unlock()
		return scalarQR("PRIMARY"), nil
	}
	_, _ = NewHealthSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	mu.Lock()
	defer mu.Unlock()
	for _, sql := range captured {
		if contains(sql, "ROLE$") {
			if !contains(sql, "PRIMARY") || !contains(sql, "STANDBY") {
				t.Errorf("ROLE$ query missing CASE translation. SQL:\n%s", sql)
			}
			return
		}
	}
	t.Error("V$DATABASE.ROLE$ query was never issued")
}
