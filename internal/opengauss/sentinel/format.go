/*-------------------------------------------------------------------------
 *
 * format.go
 *	  TriggerMetricLabel returns the display label for a trigger metric
 *	  name.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/format.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"fmt"
	"strings"
)

// TriggerMetricLabel returns the display label for a trigger metric name.
func TriggerMetricLabel(metric string) string {
	return MetricLabel(MetricName(metric))
}

// FormatAlertDescription generates a precise alert description for a trigger event.
func FormatAlertDescription(trigger TriggerEvent, durationSec float64) string {
	label := MetricLabel(MetricName(trigger.Metric))
	unit := MetricUnit(MetricName(trigger.Metric))
	valStr := formatAlertValues(trigger.Baseline, trigger.Current, unit)
	reason := fmt.Sprintf("3-sigma阈值%.1f", trigger.Threshold)

	return fmt.Sprintf("%s %s (%s)", label, valStr, reason)
}

// formatAlertValues formats "baseline->current[unit]".
func formatAlertValues(baseline, current float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%s%%->%s%%", formatNum(baseline), formatNum(current))
	}
	if unit == "" {
		return fmt.Sprintf("%s->%s", formatNum(baseline), formatNum(current))
	}
	return fmt.Sprintf("%s->%s%s", formatNum(baseline), formatNum(current), unit)
}

// formatNum formats a number with appropriate precision.
func formatNum(v float64) string {
	if v >= 10000 {
		return fmt.Sprintf("%.1f万", v/10000)
	}
	if v == float64(int64(v)) && v < 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// FormatReportHistory formats a list of reports into a history view.
func FormatReportHistory(reports []*BurstReport) string {
	if len(reports) == 0 {
		return "暂无异常记录."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("异常记录 (共 %d 条, 最新在前):\n", len(reports)))
	for i := 0; i < len(reports); i++ {
		r := reports[len(reports)-1-i]
		b.WriteString(FormatReportSummaryLine(i+1, *r))
		b.WriteByte('\n')
	}
	b.WriteString("\n使用 /llm <编号> 查看详细诊断, 如: /llm 2")
	return b.String()
}

// FormatReportSummaryLine returns a one-line summary for a report.
func FormatReportSummaryLine(idx int, report BurstReport) string {
	ts := ""
	if !report.StartTime.IsZero() {
		ts = report.StartTime.Format("01-02 15:04:05")
	}

	trigger := report.TriggerEvent
	if trigger.Metric != "" {
		label := MetricLabel(MetricName(trigger.Metric))
		unit := MetricUnit(MetricName(trigger.Metric))
		valStr := formatAlertValues(trigger.Baseline, trigger.Current, unit)
		return fmt.Sprintf("  %d. [%s] %s %s", idx, ts, label, valStr)
	}

	// Fallback: show classification.
	cause := report.Classification.Cause.String()
	conf := report.Classification.Confidence * 100
	return fmt.Sprintf("  %d. [%s] %s (%.0f%%)", idx, ts, cause, conf)
}

// FormatRuleDiagnosis renders a BurstReport into human-readable text.
func FormatRuleDiagnosis(report BurstReport) string {
	var b strings.Builder

	// Trigger info.
	b.WriteString("  OG Sentinel 异常报告\n\n")
	if report.TriggerEvent.Metric != "" {
		label := MetricLabel(MetricName(report.TriggerEvent.Metric))
		if report.TriggerEvent.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f -> %.1f\n",
				label, report.TriggerEvent.Baseline, report.TriggerEvent.Current))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f\n",
				label, report.TriggerEvent.Current))
		}
	}
	if report.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("  持续: %.1fs\n", report.DurationSec))
	}

	// Classification.
	c := report.Classification
	if c.Cause != "" && c.Cause != CauseUnknown {
		b.WriteString(fmt.Sprintf("\n  根因分类: %s (置信度 %.0f%%)\n",
			c.Cause.String(), c.Confidence*100))
		for _, ev := range c.Evidence {
			b.WriteString(fmt.Sprintf("    - %s\n", ev))
		}
	}

	// Metric summary.
	if len(report.Metrics) > 0 {
		b.WriteString("\n  指标摘要:\n")
		order := []MetricName{
			MetricActiveSessions, MetricIdleInTransaction, MetricLockWaits,
			MetricLongQueries, MetricXactCommitRate, MetricCacheHitPct,
			MetricDeadTupleRatio, MetricXIDAgeRatio, MetricConnectionsPct,
			MetricReplicationLag, MetricWALBytesRate, MetricCheckpointsReq,
		}
		for _, m := range order {
			ms, ok := report.Metrics[string(m)]
			if !ok {
				continue
			}
			label := MetricLabel(m)
			b.WriteString(fmt.Sprintf("    %s: avg=%.1f max=%.1f\n",
				label, ms.Avg, ms.Max))
		}
	}

	return b.String()
}
