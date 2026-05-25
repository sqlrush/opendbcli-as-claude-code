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
 *	  internal/postgres/agent/compress.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/postgres/sentinel"
)

// CompressReport converts a BurstReport into a compact text summary
// suitable for a single LLM call (~2000 tokens max).
func CompressReport(report sentinel.BurstReport) string {
	var b strings.Builder

	writeHeader(&b, report)
	writeClassification(&b, report.Classification)
	writeMetrics(&b, report)

	return b.String()
}

func writeHeader(b *strings.Builder, r sentinel.BurstReport) {
	b.WriteString("=== PostgreSQL 性能异常报告 ===\n")
	fmt.Fprintf(b, "触发: %s=%.1f (基线=%.1f, 阈值=%.1f, %.1f倍)\n",
		r.TriggerEvent.Metric,
		r.TriggerEvent.Current,
		r.TriggerEvent.Baseline,
		r.TriggerEvent.Threshold,
		r.TriggerEvent.Multiplier)
	fmt.Fprintf(b, "持续: %.1fs, 峰值活跃: %d, 基线活跃: %.1f\n\n",
		r.DurationSec, r.PeakActive, r.BaselineActive)
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
		{string(sentinel.MetricActiveSessions), "活跃会话"},
		{string(sentinel.MetricIdleInTransaction), "Idle in Tx"},
		{string(sentinel.MetricLockWaits), "锁等待"},
		{string(sentinel.MetricLongQueries), "慢查询"},
		{string(sentinel.MetricXactCommitRate), "TPS"},
		{string(sentinel.MetricCacheHitPct), "缓存命中%"},
		{string(sentinel.MetricDeadTupleRatio), "死元组%"},
		{string(sentinel.MetricXIDAgeRatio), "XID年龄%"},
		{string(sentinel.MetricConnectionsPct), "连接使用%"},
		{string(sentinel.MetricReplicationLag), "复制延迟秒"},
		{string(sentinel.MetricWALBytesRate), "WAL B/s"},
		{string(sentinel.MetricCheckpointsReq), "Checkpoint"},
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
