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
 *	  internal/mysql/sentinel/format.go
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

// FormatAlertDescription generates a pre-formatted alert description.
func FormatAlertDescription(trigger TriggerEvent, durationSec float64) string {
	label := MetricLabel(MetricName(trigger.Metric))
	unit := MetricUnit(MetricName(trigger.Metric))
	valStr := formatAlertValues(trigger.Baseline, trigger.Current, unit)
	reason := formatTriggerReason(trigger)
	return fmt.Sprintf("%s %s (%s)", label, valStr, reason)
}

func formatTriggerReason(trigger TriggerEvent) string {
	switch trigger.Strategy {
	case StrategyT1:
		return fmt.Sprintf("3σ阈值%.1f", trigger.Threshold)
	case StrategyT2:
		return fmt.Sprintf("硬顶%.1f", trigger.Threshold)
	case StrategyT3:
		return "趋势上升"
	case StrategyT4:
		return "加速度突增"
	case StrategyT5:
		return "复合条件触发"
	case StrategyT6:
		return fmt.Sprintf("容量预警%.1f", trigger.Threshold)
	case StrategyT7:
		return "均值偏移"
	case StrategyT8:
		return fmt.Sprintf("回归跌破%.1f", trigger.Threshold)
	case StrategyT9:
		return "速率骤降"
	default:
		return fmt.Sprintf("阈值%.1f", trigger.Threshold)
	}
}

func formatAlertValues(baseline, current float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%s%%->%s%%", formatNum(baseline), formatNum(current))
	}
	if unit == "" {
		return fmt.Sprintf("%s->%s", formatNum(baseline), formatNum(current))
	}
	return fmt.Sprintf("%s->%s%s", formatNum(baseline), formatNum(current), unit)
}

func formatNum(v float64) string {
	if v >= 10000 {
		return fmt.Sprintf("%.1f万", v/10000)
	}
	if v == float64(int64(v)) && v < 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.1f", v)
}

// FormatReportSummaryLine returns a one-line summary for a report.
func FormatReportSummaryLine(idx int, report BurstReport) string {
	ts := ""
	if !report.StartTime.IsZero() {
		ts = report.StartTime.Format("01-02 15:04:05")
	} else if !report.EndTime.IsZero() {
		ts = report.EndTime.Format("01-02 15:04:05")
	}
	trigger := report.TriggerEvent
	if trigger.Metric != "" {
		label := MetricLabel(MetricName(trigger.Metric))
		unit := MetricUnit(MetricName(trigger.Metric))
		valStr := formatAlertValues(trigger.Baseline, trigger.Current, unit)
		return fmt.Sprintf("  %d. [%s] %s %s", idx, ts, label, valStr)
	}
	cause := report.Classification.Cause.String()
	conf := report.Classification.Confidence * 100
	return fmt.Sprintf("  %d. [%s] %s (%.0f%%)", idx, ts, cause, conf)
}

// FormatReportHistory formats a list of reports into a history view.
func FormatReportHistory(reports []*BurstReport) string {
	if len(reports) == 0 {
		return "暂无异常记录."
	}
	text := fmt.Sprintf("异常记录 (共 %d 条, 最新在前):\n", len(reports))
	for i := 0; i < len(reports); i++ {
		r := reports[len(reports)-1-i]
		text += FormatReportSummaryLine(i+1, *r)
		text += "\n"
	}
	text += "\n使用 /llm <编号> 查看详细诊断, 如: /llm 2"
	return text
}

// FormatRuleDiagnosis renders a BurstReport into human-readable text.
func FormatRuleDiagnosis(report BurstReport) string {
	var b strings.Builder

	b.WriteString("  MySQL Sentinel 异常报告\n\n")
	if report.TriggerEvent.Metric != "" {
		label := MetricLabel(MetricName(report.TriggerEvent.Metric))
		strategy := formatTriggerReason(report.TriggerEvent)
		if report.TriggerEvent.Baseline > 0 {
			b.WriteString(fmt.Sprintf("  触发: %s  %.1f -> %.1f [%s]\n",
				label, report.TriggerEvent.Baseline, report.TriggerEvent.Current, strategy))
		} else {
			b.WriteString(fmt.Sprintf("  当前: %s = %.0f [%s]\n",
				label, report.TriggerEvent.Current, strategy))
		}
	}
	if report.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("  持续: %.1fs  帧数: %d\n", report.DurationSec, report.RawFrameCount))
	}

	c := report.Classification
	if c.Cause != "" && c.Cause != CauseUnknown {
		b.WriteString(fmt.Sprintf("\n  根因分类: %s (置信度 %.0f%%)\n", c.Cause.String(), c.Confidence*100))
		for _, ev := range c.Evidence {
			b.WriteString(fmt.Sprintf("    - %s\n", ev))
		}
	}

	if len(report.Metrics) > 0 {
		b.WriteString("\n  指标摘要:\n")
		for _, m := range metricPriorityOrder {
			ms, ok := report.Metrics[string(m)]
			if !ok {
				continue
			}
			label := MetricLabel(m)
			b.WriteString(fmt.Sprintf("    %s: avg=%.1f max=%.1f\n", label, ms.Avg, ms.Max))
		}
	}

	if len(report.SpaceDetails) > 0 {
		b.WriteString("\n  空间详情:\n")
		for _, sd := range report.SpaceDetails {
			if sd.UsedMB > 0 {
				b.WriteString(fmt.Sprintf("    %s: %.1f MB", sd.Name, sd.UsedMB))
			} else {
				b.WriteString(fmt.Sprintf("    %s", sd.Name))
			}
			if sd.Extra != "" {
				b.WriteString(fmt.Sprintf(" [%s]", sd.Extra))
			}
			b.WriteByte('\n')
		}
	}

	if len(report.ParamDetails) > 0 {
		b.WriteString("\n  相关参数:\n")
		for _, pd := range report.ParamDetails {
			b.WriteString(fmt.Sprintf("    %s = %s\n", pd.Name, pd.Value))
		}
	}

	return b.String()
}
