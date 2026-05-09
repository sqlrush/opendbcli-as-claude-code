/*-------------------------------------------------------------------------
 *
 * format.go
 *	  TriggerMetricLabel returns the display label for a trigger metric.
 *	  Uses MetricDef registry, falls back to raw metric name.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/format.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// TriggerMetricLabel returns the display label for a trigger metric.
// Uses MetricDef registry, falls back to raw metric name.
func TriggerMetricLabel(metric string) string {
	if def := GetMetricDef(MetricName(metric)); def != nil {
		return def.Label
	}
	return metric
}

// TriggerMetricUnit returns the display unit for a trigger metric.
// Uses MetricDef registry, falls back to empty string.
func TriggerMetricUnit(metric string) string {
	if def := GetMetricDef(MetricName(metric)); def != nil {
		return def.Unit
	}
	return ""
}

// FormatTriggerInfo formats a trigger event into a concise one-line description.
func FormatTriggerInfo(trigger TriggerEvent, durationSec float64) string {
	return FormatAlertDescription(trigger, durationSec)
}

// FormatAlertDescription generates a precise alert description for a trigger event.
// Format: "Label baseline→currentUnit (trigger_reason)"
// Each detection strategy (T1-T9) has its own trigger reason format.
func FormatAlertDescription(trigger TriggerEvent, durationSec float64) string {
	def := GetMetricDef(MetricName(trigger.Metric))
	if def == nil {
		return fmt.Sprintf("%s %.1f→%.1f (阈值%.1f)",
			trigger.Metric, trigger.Baseline, trigger.Current, trigger.Threshold)
	}

	label := def.Label
	valStr := formatAlertValues(trigger.Baseline, trigger.Current, def.Unit)

	// Immediate trigger (deadlock, instance status change).
	if def.ImmediateTrigger {
		return fmt.Sprintf("%s %s (立即触发)", label, valStr)
	}

	reason := formatStrategyReason(def, trigger)
	return fmt.Sprintf("%s %s (%s)", label, valStr, reason)
}

// formatAlertValues formats "baseline→current[unit]" with appropriate precision.
// For "%": "40.0%→100.0%"; for others: "2→15个", "500→8500/s".
func formatAlertValues(baseline, current float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%s%%→%s%%", formatAlertNum(baseline), formatAlertNum(current))
	}
	if unit == "" {
		return fmt.Sprintf("%s→%s", formatAlertNum(baseline), formatAlertNum(current))
	}
	return fmt.Sprintf("%s→%s%s", formatAlertNum(baseline), formatAlertNum(current), unit)
}

// formatAlertNum formats a number with appropriate precision.
// Values ≥10000 use "万" notation.
func formatAlertNum(v float64) string {
	if v >= 10000 {
		return fmt.Sprintf("%.1f万", v/10000)
	}
	if v == float64(int64(v)) && v < 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// formatStrategyReason returns the trigger reason based on detection strategy.
func formatStrategyReason(def *MetricDef, trigger TriggerEvent) string {
	strategy := trigger.Strategy
	if strategy == "" && len(def.Strategies) > 0 {
		strategy = def.Strategies[0]
	}

	switch strategy {
	case StrategyT1:
		return fmt.Sprintf("3σ阈值%.1f", trigger.Threshold)
	case StrategyT2:
		return fmt.Sprintf("上限%.1f", trigger.Threshold)
	case StrategyT3:
		windows := def.TrendWindows
		if windows == 0 {
			windows = 3
		}
		return fmt.Sprintf("持续上升, 连续%d个窗口", windows)
	case StrategyT4:
		return "加速增长"
	case StrategyT5:
		reason := fmt.Sprintf("3σ阈值%.1f", trigger.Threshold)
		if len(def.CompoundWith) > 0 {
			compLabel := string(def.CompoundWith[0])
			if compDef := GetMetricDef(def.CompoundWith[0]); compDef != nil {
				compLabel = compDef.Label
			}
			reason += fmt.Sprintf(", 同时%s冲高", compLabel)
		}
		return reason
	case StrategyT6:
		return fmt.Sprintf("红线%.0f%%", def.CapacityRed)
	case StrategyT7:
		return "窗口均值偏移 >2σ"
	case StrategyT8:
		return fmt.Sprintf("下限%.0f%%", def.RegressionFloor)
	case StrategyT9:
		if trigger.Baseline > 0 {
			pct := (1 - trigger.Current/trigger.Baseline) * 100
			return fmt.Sprintf("较基线下降%.0f%%", pct)
		}
		return "较基线骤降"
	default:
		return fmt.Sprintf("阈值%.1f", trigger.Threshold)
	}
}

// FormatReportSummaryLine returns a one-line summary for a report.
// idx is 1-based (1=newest). Used by /diag history.
// Shows trigger metric info instead of classification result.
func FormatReportSummaryLine(idx int, report BurstReport) string {
	ts := ""
	if !report.StartTime.IsZero() {
		ts = report.StartTime.Format("01-02 15:04:05")
	} else if !report.EndTime.IsZero() {
		ts = report.EndTime.Format("01-02 15:04:05")
	}

	trigger := report.TriggerEvent
	if trigger.Metric != "" {
		def := GetMetricDef(MetricName(trigger.Metric))
		label := TriggerMetricLabel(trigger.Metric)
		valStr := formatAlertValues(trigger.Baseline, trigger.Current, TriggerMetricUnit(trigger.Metric))
		reason := formatStrategyReason(def, trigger)
		return fmt.Sprintf("  %d. [%s] %s %s (%s)",
			idx, ts, label, valStr, reason)
	}

	// Fallback: show classification if no trigger info.
	cause := report.Classification.Cause.String()
	conf := report.Classification.Confidence * 100
	return fmt.Sprintf("  %d. [%s] %s (%.0f%%)",
		idx, ts, cause, conf)
}

// FormatReportHistory formats a list of reports into a history view.
// Reports are ordered newest-first (index 0 = newest).
func FormatReportHistory(reports []*BurstReport) string {
	if len(reports) == 0 {
		return "暂无异常记录."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("异常记录 (共 %d 条, 最新在前):\n", len(reports)))
	for i := 0; i < len(reports); i++ {
		// reports slice is newest-last, so reverse iteration for newest-first display.
		r := reports[len(reports)-1-i]
		b.WriteString(FormatReportSummaryLine(i+1, *r))
		b.WriteByte('\n')
	}
	b.WriteString("\n使用 /llm <编号> 查看详细诊断, 如: /llm 2")
	return b.String()
}

// FormatRuleDiagnosis renders a BurstReport into a human-readable
// rule-based diagnosis string. No LLM needed.
// boxWidth controls the right border position; 0 means auto (80 columns).
func FormatRuleDiagnosis(report BurstReport) string {
	return FormatRuleDiagnosisWidth(report, 0)
}

// FormatRuleDiagnosisWidth is implemented in format_monitor.go.
// It renders per-scenario monitoring data with consistent border alignment.

// truncStr truncates a string to maxLen runes, adding "…" if needed.
func truncStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-1]) + "…"
}

// truncByWidth truncates a string by display width.
func truncByWidth(s string, maxWidth int) string {
	w := 0
	for i, r := range s {
		rw := runeWidth(r)
		if w+rw > maxWidth {
			return s[:i]
		}
		w += rw
	}
	return s
}

// displayWidth returns the visual width of a string in a terminal.
// CJK characters count as 2, others as 1.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		w += runeWidth(r)
	}
	return w
}

// runeWidth returns 2 for CJK/fullwidth characters, 1 otherwise.
func runeWidth(r rune) int {
	if utf8.RuneLen(r) >= 3 {
		// CJK Unified Ideographs and common fullwidth ranges.
		if (r >= 0x2E80 && r <= 0x9FFF) ||
			(r >= 0xF900 && r <= 0xFAFF) ||
			(r >= 0xFE30 && r <= 0xFE4F) ||
			(r >= 0xFF00 && r <= 0xFF60) ||
			(r >= 0x20000 && r <= 0x2FA1F) {
			return 2
		}
	}
	return 1
}
