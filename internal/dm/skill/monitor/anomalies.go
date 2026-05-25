/*-------------------------------------------------------------------------
 *
 * anomalies.go
 *	  Package monitor — DM anomalies skill:
 *	  一次性采集"当前异常上下文", 给 LLM
 *	  诊断起手用。轻量化版 sentinel: 不开后台 goroutine,
 *	  不算基线, 只跑 6 条小 SQL 并行 + 阈值判断 + summary
 *	  banner.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/monitor/anomalies.go
 *
 *-------------------------------------------------------------------------
 */
// Package monitor — DM anomalies skill: 一次性采集"当前异常上下文",
// 给 LLM 诊断起手用。轻量化版 sentinel: 不开后台 goroutine, 不算基线,
// 只跑 6 条小 SQL 并行 + 阈值判断 + summary banner.
//
// 设计动机 (参考 docs/dm-llm-benchmark.md P2 项):
// - LLM 启动诊断时缺"当前异常上下文" → 先看一眼现场再决定查哪些 skill
// - 现场指标 [blocked, active_long, deadlock_recent, danger_recent, runtime_err_recent]
// - 阈值二值化 → is_anomaly: true/false 给 LLM 直接决策
package monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/sqlrush/opendb/internal/db"
	dmutil "github.com/sqlrush/opendb/internal/dm/skill/util"
	"github.com/sqlrush/opendb/internal/skill"
)

// AnomaliesSkill: DM 当前异常上下文快照
type AnomaliesSkill struct{ driver db.Driver }

func NewAnomaliesSkill(driver db.Driver) *AnomaliesSkill { return &AnomaliesSkill{driver: driver} }

func (s *AnomaliesSkill) Name() string                       { return "anomalies" }
func (s *AnomaliesSkill) Description() string                { return "当前异常上下文快照 (诊断起手, 轻量 sentinel)" }
func (s *AnomaliesSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }
func (s *AnomaliesSkill) Validate(_ skill.Params) error      { return nil }
func (s *AnomaliesSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{Name: "anomalies", Description: "DM current anomaly context snapshot"}
}
func (s *AnomaliesSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Command: "anomalies", Usage: "/anomalies"}
}

// 阈值: 触发 is_anomaly 的下限。保守值, 避免误报。
const (
	thresholdBlockedSessions = 1   // 任何阻塞都算
	thresholdLongActiveSec   = 30  // 活跃 SQL > 30s
	thresholdLongExecSQLs    = 1   // V$LONG_EXEC_SQLS 任何记录
	thresholdRecentDeadlocks = 1   // 最近 1 小时任意死锁
	thresholdRecentDangers   = 1   // 最近 1 小时任意危险事件
	thresholdRecentErrors    = 10  // 最近 1 小时 > 10 条运行错误
)

type anomalyMetric struct {
	key      string
	val      any
	signal   string // 非空表示触发了异常信号
	rendered string // 单行人类可读
}

func (s *AnomaliesSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	type result struct {
		idx int
		m   anomalyMetric
	}

	queries := []struct {
		key       string
		sql       string
		threshold int
		signal    string
		fmtFn     func(val any) string
	}{
		{
			key:       "blocked_sessions",
			sql:       `SELECT COUNT(*) FROM V$LOCK WHERE BLOCKED = 1`,
			threshold: thresholdBlockedSessions,
			signal:    "blocked",
			fmtFn:     func(v any) string { return fmt.Sprintf("阻塞会话数: %v", v) },
		},
		{
			key:       "active_sessions",
			sql:       `SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'ACTIVE' AND USER_NAME IS NOT NULL`,
			threshold: -1, // 不当 anomaly, 仅参考
			fmtFn:     func(v any) string { return fmt.Sprintf("活跃会话数: %v", v) },
		},
		{
			key:       "longest_active_sec",
			sql:       `SELECT NVL(MAX(ROUND((SYSDATE - CREATE_TIME) * 86400)), 0) FROM V$SESSIONS WHERE STATE = 'ACTIVE' AND USER_NAME IS NOT NULL`,
			threshold: thresholdLongActiveSec,
			signal:    "active_long",
			fmtFn:     func(v any) string { return fmt.Sprintf("最长活跃 SQL: %vs", v) },
		},
		{
			key:       "long_exec_sqls",
			sql:       `SELECT COUNT(*) FROM V$LONG_EXEC_SQLS`,
			threshold: thresholdLongExecSQLs,
			signal:    "long_sql",
			fmtFn:     func(v any) string { return fmt.Sprintf("长执行 SQL 数: %v", v) },
		},
		{
			key:       "deadlocks_recent_1h",
			sql:       `SELECT COUNT(*) FROM V$DEADLOCK_HISTORY WHERE HAPPEN_TIME > SYSDATE - 1/24`,
			threshold: thresholdRecentDeadlocks,
			signal:    "deadlock_recent",
			fmtFn:     func(v any) string { return fmt.Sprintf("最近1小时死锁: %v 次", v) },
		},
		{
			// V$DANGER_EVENT 实测列: OPTIME / OPERATION / OPUSER (不是 HAPPEN_TIME)
			key:       "dangers_recent_1h",
			sql:       `SELECT COUNT(*) FROM V$DANGER_EVENT WHERE OPTIME > SYSDATE - 1/24`,
			threshold: thresholdRecentDangers,
			signal:    "danger_recent",
			fmtFn:     func(v any) string { return fmt.Sprintf("最近1小时危险事件: %v 次", v) },
		},
		{
			// V$RUNTIME_ERR_HISTORY 的时间列名待真机确认 (alert.go 只取累计无时间过滤);
			// 暂用累计值, 阈值放宽到 50, 后续 P1 真机确认列名后改为 1h 窗口.
			key:       "errors_total",
			sql:       `SELECT COUNT(*) FROM V$RUNTIME_ERR_HISTORY`,
			threshold: 50,
			signal:    "errors_high",
			fmtFn:     func(v any) string { return fmt.Sprintf("累计运行错误: %v 条", v) },
		},
	}

	results := make([]anomalyMetric, len(queries))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, q := range queries {
		wg.Add(1)
		go func(idx int, key, sqlStr, signal string, threshold int, fmtFn func(any) string) {
			defer wg.Done()
			r, err := s.driver.Query(ctx, sqlStr)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				results[idx] = anomalyMetric{key: key, val: "ERR", rendered: fmt.Sprintf("%s: query failed (%v)", key, err)}
				return
			}
			val := dmutil.FirstString(r.Rows)
			if val == "" {
				val = "0"
			}
			m := anomalyMetric{key: key, val: val, rendered: fmtFn(val)}
			if threshold > 0 && exceedsThreshold(val, threshold) {
				m.signal = signal
			}
			results[idx] = m
		}(i, q.key, q.sql, q.signal, q.threshold, q.fmtFn)
	}
	wg.Wait()

	var b strings.Builder
	b.WriteString("=== DM 异常上下文快照 ===\n")
	for _, m := range results {
		marker := "  "
		if m.signal != "" {
			marker = "⚠ "
		}
		b.WriteString(fmt.Sprintf("%s%s\n", marker, m.rendered))
	}

	entries := []dmutil.SummaryEntry{
		{Key: "data_window", Val: "real-time snapshot + recent 1 hour history"},
	}
	signals := []string{}
	for _, m := range results {
		entries = append(entries, dmutil.SummaryEntry{Key: m.key, Val: m.val})
		if m.signal != "" {
			signals = append(signals, m.signal)
		}
	}
	if len(signals) > 0 {
		entries = append(entries,
			dmutil.SummaryEntry{Key: "is_anomaly", Val: true},
			dmutil.SummaryEntry{Key: "anomaly_signals", Val: strings.Join(signals, ",")},
			dmutil.SummaryEntry{Key: "next_step_hint", Val: anomalyHint(signals)},
		)
	} else {
		entries = append(entries, dmutil.SummaryEntry{Key: "is_anomaly", Val: false})
	}

	b.WriteString(dmutil.FormatSummary(entries))

	summary := "无异常"
	if len(signals) > 0 {
		summary = fmt.Sprintf("异常: %s", strings.Join(signals, ","))
	}

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("DM 异常快照 — %s", summary),
	}, nil
}

// exceedsThreshold parses val 为 int, 比较 >= threshold.
func exceedsThreshold(val string, threshold int) bool {
	var n int
	_, err := fmt.Sscanf(val, "%d", &n)
	if err != nil {
		return false
	}
	return n >= threshold
}

// anomalyHint 给 LLM 一个下一步 skill 建议
func anomalyHint(signals []string) string {
	hints := map[string]string{
		"blocked":         "blocktree+locks",
		"active_long":     "activesessions+slowsql",
		"long_sql":        "slowsql+explain",
		"deadlock_recent": "deadlock",
		"danger_recent":   "alert",
		"errors_high":     "alert",
	}
	seen := map[string]bool{}
	parts := []string{}
	for _, sig := range signals {
		if h, ok := hints[sig]; ok && !seen[h] {
			seen[h] = true
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, ",")
}
