/*-------------------------------------------------------------------------
 *
 * anomalies_test.go
 *	  Test cases for anomalies.go (monitor package):
 *	  TestAnomaliesSkill_Metadata, TestAnomaliesSkill_NoAnomaly,
 *	  TestAnomaliesSkill_TriggerLongSQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/anomalies_test.go
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

// 7 个 SQL 路由对应 anomalies skill 的 7 条并发查询.
// scalarQR(value) 构造单行单列 QueryResult, 模拟 SELECT COUNT(*) 返回值.
func scalarQR(v any) *db.QueryResult {
	return &db.QueryResult{
		Columns: []string{"VAL"},
		Rows:    [][]any{{v}},
	}
}

func TestAnomaliesSkill_Metadata(t *testing.T) {
	s := NewAnomaliesSkill(makeRoutedDriver())
	if s.Name() != "anomalies" {
		t.Errorf("Name() = %q", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v", s.SecurityLevel())
	}
}

func TestAnomaliesSkill_NoAnomaly(t *testing.T) {
	// 全部指标都在阈值下 → is_anomaly: false
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$LOCK", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$LONG_EXEC_SQLS", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$DEADLOCK_HISTORY", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$DANGER_EVENT", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$RUNTIME_ERR_HISTORY", result: scalarQR(int64(0))},
		sqlMatcher{contains: "STATE = 'ACTIVE'", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$SESSIONS", result: scalarQR(int64(0))}, // 兜底
	)
	r, err := NewAnomaliesSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertSummaryContains(t, r.Rendered, "is_anomaly: false")
	assertSummaryContains(t, r.Rendered, "blocked_sessions: 0")
	assertSummaryContains(t, r.Rendered, "errors_total: 0")
	// data_window banner 必须出现
	assertSummaryContains(t, r.Rendered, "data_window: real-time snapshot + recent 1 hour history")
}

func TestAnomaliesSkill_TriggerLongSQL(t *testing.T) {
	// 长 SQL 数 > threshold (1) → 触发 long_sql 信号
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$LONG_EXEC_SQLS", result: scalarQR(int64(1000))},
		sqlMatcher{contains: "V$LOCK", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$DEADLOCK_HISTORY", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$DANGER_EVENT", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$RUNTIME_ERR_HISTORY", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$SESSIONS", result: scalarQR(int64(0))},
	)
	r, _ := NewAnomaliesSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "is_anomaly: true")
	assertSummaryContains(t, r.Rendered, "long_sql")
	assertSummaryContains(t, r.Rendered, "long_exec_sqls: 1000")
	// next_step_hint 必须给出
	assertSummaryContains(t, r.Rendered, "next_step_hint:")
}

func TestAnomaliesSkill_TriggerMultiple(t *testing.T) {
	// 阻塞 + 死锁 + 错误三个信号同时触发
	drv := makeRoutedDriver(
		sqlMatcher{contains: "V$LOCK", result: scalarQR(int64(5))},
		sqlMatcher{contains: "V$DEADLOCK_HISTORY", result: scalarQR(int64(3))},
		sqlMatcher{contains: "V$RUNTIME_ERR_HISTORY", result: scalarQR(int64(770))},
		sqlMatcher{contains: "V$DANGER_EVENT", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$LONG_EXEC_SQLS", result: scalarQR(int64(0))},
		sqlMatcher{contains: "V$SESSIONS", result: scalarQR(int64(0))},
	)
	r, _ := NewAnomaliesSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	assertSummaryContains(t, r.Rendered, "is_anomaly: true")
	// 至少包含 blocked / deadlock_recent / errors_high 中的一个
	if !contains(r.Rendered, "blocked") && !contains(r.Rendered, "deadlock_recent") && !contains(r.Rendered, "errors_high") {
		t.Errorf("expected at least one of blocked/deadlock_recent/errors_high signal, got:\n%s", r.Rendered)
	}
}

// 关键回归: 之前 V$DANGER_EVENT 用 HAPPEN_TIME 列报错, 修成 OPTIME.
// 这个测试断言查询包含 "OPTIME" 而不是 "HAPPEN_TIME" (在 danger_event 上下文里).
// anomalies skill 用 7 goroutine 并发查询, captured 必须加锁.
func TestAnomaliesSkill_DangerEvent_OPTIME(t *testing.T) {
	var (
		mu           sync.Mutex
		capturedSQLs []string
	)
	drv := makeRoutedDriver()
	drv.QueryFunc = func(ctx context.Context, sql string, _ ...any) (*db.QueryResult, error) {
		mu.Lock()
		capturedSQLs = append(capturedSQLs, sql)
		mu.Unlock()
		return scalarQR(int64(0)), nil
	}
	_, _ = NewAnomaliesSkill(drv).Execute(context.Background(), skill.ParamsFromMap(nil))
	// 找 V$DANGER_EVENT 那条 SQL
	mu.Lock()
	defer mu.Unlock()
	for _, sql := range capturedSQLs {
		if contains(sql, "V$DANGER_EVENT") {
			if !contains(sql, "OPTIME") {
				t.Errorf("V$DANGER_EVENT query missing OPTIME column. SQL:\n%s", sql)
			}
			if contains(sql, "HAPPEN_TIME") {
				t.Errorf("V$DANGER_EVENT query incorrectly uses HAPPEN_TIME (祖传 bug 复现). SQL:\n%s", sql)
			}
			return
		}
	}
	t.Error("V$DANGER_EVENT query was never issued")
}

// exceedsThreshold 是 anomalies skill 内核的字符串解析逻辑.
// 80% 覆盖率剩 20% 是 Sscanf err 分支 (非数字字符串).
func TestExceedsThreshold(t *testing.T) {
	tests := []struct {
		val       string
		threshold int
		want      bool
	}{
		// 正常路径
		{"5", 1, true},      // 5 >= 1
		{"0", 1, false},     // 0 < 1
		{"100", 100, true},  // 100 >= 100 (>=, 不是 >)
		{"99", 100, false},  // 99 < 100
		// 错误路径: 非数字字符串 Sscanf 失败 → false
		{"abc", 1, false},
		{"", 1, false},
		{"ERR: query failed", 1, false},
		// 边界: 负数解析正常, 但负数永远小于正阈值
		{"-5", 1, false},
	}
	for _, tt := range tests {
		got := exceedsThreshold(tt.val, tt.threshold)
		if got != tt.want {
			t.Errorf("exceedsThreshold(%q, %d) = %v, want %v",
				tt.val, tt.threshold, got, tt.want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
