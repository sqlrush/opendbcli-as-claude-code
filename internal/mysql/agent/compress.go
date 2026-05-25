/*-------------------------------------------------------------------------
 *
 * compress.go
 *	  CompressReport converts a BurstReport into a compact text summary
 *	  suitable for a single LLM call (~2000 tokens max).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/agent/compress.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/mysql/sentinel"
)

// CompressReport converts a BurstReport into a compact text summary
// suitable for a single LLM call (~2000 tokens max).
func CompressReport(report sentinel.BurstReport) string {
	var b strings.Builder

	writeHeader(&b, report)
	writeClassification(&b, report.Classification)
	writeMetrics(&b, report)
	writeWaitProfile(&b, report.WaitProfile)
	writeTopSQL(&b, report.TopSQLs)
	writeBlockingChains(&b, report.BlockingChains)

	return b.String()
}

func writeHeader(b *strings.Builder, r sentinel.BurstReport) {
	b.WriteString("=== MySQL 性能异常报告 ===\n")
	fmt.Fprintf(b, "触发: %s=%.1f (基线=%.1f, 阈值=%.1f, %.1f倍)\n",
		sentinel.MetricLabel(sentinel.MetricName(r.TriggerEvent.Metric)),
		r.TriggerEvent.Current,
		r.TriggerEvent.Baseline,
		r.TriggerEvent.Threshold,
		r.TriggerEvent.Multiplier)
	fmt.Fprintf(b, "持续: %.1fs, 峰值活跃: %d, 基线活跃: %.1f\n",
		r.DurationSec, r.PeakActive, r.BaselineActive)
	fmt.Fprintf(b, "采集帧数: %d\n\n", r.RawFrameCount)
}

func writeClassification(b *strings.Builder, c sentinel.Classification) {
	b.WriteString("--- 根因判定 ---\n")
	fmt.Fprintf(b, "类型: %s (%s)\n", c.Cause.String(), string(c.Cause))
	fmt.Fprintf(b, "置信度: %.0f%%\n", c.Confidence*100)
	for _, ev := range c.Evidence {
		fmt.Fprintf(b, "  - %s\n", ev)
	}
	b.WriteString("\n")
}

func writeMetrics(b *strings.Builder, r sentinel.BurstReport) {
	if len(r.Metrics) == 0 {
		return
	}
	b.WriteString("--- 指标摘要 ---\n")
	order := []struct {
		key, label string
	}{
		{string(sentinel.MetricThreadsRunning), "threads_running"},
		{string(sentinel.MetricThreadsConnected), "threads_connected"},
		{string(sentinel.MetricLockWaits), "lock_waits"},
		{string(sentinel.MetricTPS), "tps"},
		{string(sentinel.MetricQPS), "qps"},
		{string(sentinel.MetricRedoRate), "redo_kb/s"},
		{string(sentinel.MetricBufferPoolHit), "buffer_pool_hit%"},
		{string(sentinel.MetricHistoryList), "history_list"},
		{string(sentinel.MetricConnectionsPct), "connections%"},
		{string(sentinel.MetricReplicationLag), "repl_lag_s"},
	}
	for _, item := range order {
		m, ok := r.Metrics[item.key]
		if !ok {
			continue
		}
		fmt.Fprintf(b, "  %s: avg=%.1f max=%.1f min=%.1f trend=%s\n",
			item.label, m.Avg, m.Max, m.Min, m.Trend)
	}
	b.WriteString("\n")
}

func writeWaitProfile(b *strings.Builder, buckets []sentinel.WaitBucket) {
	if len(buckets) == 0 {
		return
	}
	b.WriteString("--- 等待分布 ---\n")
	limit := 5
	if len(buckets) < limit {
		limit = len(buckets)
	}
	for _, wb := range buckets[:limit] {
		fmt.Fprintf(b, "  %s: %.1f%% (%.0fms)\n", wb.WaitClass, wb.Percentage, wb.TotalMs)
	}
	b.WriteString("\n")
}

func writeTopSQL(b *strings.Builder, sqls []sentinel.SQLProfile) {
	if len(sqls) == 0 {
		return
	}
	b.WriteString("--- Top SQL ---\n")
	limit := 3
	if len(sqls) < limit {
		limit = len(sqls)
	}
	for _, s := range sqls[:limit] {
		text := s.SQLText
		if len(text) > 80 {
			text = text[:80] + "..."
		}
		fmt.Fprintf(b, "  %s: 执行%d次 avg=%.1fms max=%.1fms lock=%.1fms\n",
			s.Digest, s.ExecCount, s.AvgLatencyMs, s.MaxLatencyMs, s.LockTimeMs)
		if text != "" {
			fmt.Fprintf(b, "    SQL: %s\n", text)
		}
	}
	b.WriteString("\n")
}

func writeBlockingChains(b *strings.Builder, chains []sentinel.BlockingChain) {
	if len(chains) == 0 {
		return
	}
	b.WriteString("--- 阻塞链 ---\n")
	limit := 3
	if len(chains) < limit {
		limit = len(chains)
	}
	for _, c := range chains[:limit] {
		fmt.Fprintf(b, "  Thread %d -> %d 个受害者, 用户=%s, 类型=%s\n",
			c.BlockerThreadID, c.VictimCount, c.BlockerUser, c.WaitType)
	}
	b.WriteString("\n")
}
