/*-------------------------------------------------------------------------
 *
 * format_test.go
 *	  Test cases for format.go (sentinel package):
 *	  TestFormatRuleDiagnosis_Basic,
 *	  TestFormatRuleDiagnosis_WithTrigger,
 *	  TestFormatRuleDiagnosis_WithWaitProfile.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/format_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"strings"
	"testing"
	"time"
)

func TestFormatRuleDiagnosis_Basic(t *testing.T) {
	report := BurstReport{
		DurationSec:    5.2,
		PeakActive:     15,
		BaselineActive: 3.0,
		Classification: Classification{
			Cause:      CauseBadSQL,
			Confidence: 0.85,
			Evidence:   []string{"TOP SQL占比>60%", "等待集中在db file sequential read"},
		},
	}

	output := FormatRuleDiagnosis(report)

	// Should contain header (monitoring data, no judgment).
	if !strings.Contains(output, "监控数据") {
		t.Error("should contain header")
	}
	// Should contain duration and peak.
	if !strings.Contains(output, "5.2s") {
		t.Error("should contain duration")
	}
	if !strings.Contains(output, "15") {
		t.Error("should contain peak active")
	}
	// Should NOT contain classification (judgment removed).
	if strings.Contains(output, "根因分析") {
		t.Error("should not contain root cause analysis")
	}
	// Should contain footer.
	if !strings.Contains(output, "└") {
		t.Error("should contain footer border")
	}
}

func TestFormatRuleDiagnosis_WithTrigger(t *testing.T) {
	report := BurstReport{
		DurationSec:    30.0,
		PeakActive:     8,
		BaselineActive: 1.0,
		TriggerEvent: TriggerEvent{
			Metric:    "lock_sessions",
			Baseline:  0,
			Current:   8,
			Threshold: 5.0,
		},
		Classification: Classification{
			Cause:      CauseLockContention,
			Confidence: 0.70,
		},
	}

	output := FormatRuleDiagnosis(report)

	if !strings.Contains(output, "触发") {
		t.Error("should contain trigger line")
	}
	if !strings.Contains(output, "活跃会话Lock Wait") {
		t.Error("should contain trigger metric label")
	}
	if !strings.Contains(output, "3σ阈值") {
		t.Error("should contain strategy-based threshold info")
	}
}

func TestFormatRuleDiagnosis_WithWaitProfile(t *testing.T) {
	report := BurstReport{
		Classification: Classification{
			Cause:      CauseIOSubsystem,
			Confidence: 0.70,
		},
		WaitProfile: []WaitBucket{
			{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 45.0},
			{Event: "db file scattered read", WaitClass: "System I/O", Percentage: 25.0},
			{Event: "other wait", WaitClass: "Other", Percentage: 0.5}, // below 1%, should be skipped
		},
	}

	output := FormatRuleDiagnosis(report)

	if !strings.Contains(output, "等待事件") {
		t.Error("should contain wait event section")
	}
	if !strings.Contains(output, "db file sequential read") {
		t.Error("should contain event name, not class")
	}
	if !strings.Contains(output, "db file scattered read") {
		t.Error("should contain second event name")
	}
}

func TestFormatRuleDiagnosis_WithTopSQL(t *testing.T) {
	start := time.Date(2026, 3, 14, 22, 30, 10, 0, time.Local)
	end := start.Add(18 * time.Second)
	report := BurstReport{
		StartTime:   start,
		EndTime:     end,
		DurationSec: 18.0,
		Classification: Classification{
			Cause:      CauseBadSQL,
			Confidence: 0.80,
		},
		TopSQLs: []SQLProfile{
			{SQLID: "abc123", MaxElapsedSec: 3.0, MaxConcurrent: 8, Event: "db file sequential read",
				SQLText: "SELECT * FROM orders WHERE status = 1"},
			{SQLID: "def456", MaxElapsedSec: 1.5, MaxConcurrent: 3, Event: "DB CPU",
				SQLText: "UPDATE accounts SET balance = balance - 100"},
		},
	}

	output := FormatRuleDiagnosis(report)

	if !strings.Contains(output, "Top SQL") {
		t.Error("should contain Top SQL section")
	}
	// Should contain sampling time range.
	if !strings.Contains(output, "22:30:10") {
		t.Error("should contain start time")
	}
	if !strings.Contains(output, "22:30:28") {
		t.Error("should contain end time")
	}
	if !strings.Contains(output, "abc123") {
		t.Error("should contain SQL ID")
	}
	if !strings.Contains(output, "最长耗时") {
		t.Error("should show max elapsed time label")
	}
	if !strings.Contains(output, "最大并发") {
		t.Error("should show max concurrent label")
	}
	if !strings.Contains(output, "最多等待") {
		t.Error("should show dominant wait label")
	}
	if !strings.Contains(output, "db file sequential read") {
		t.Error("should show event name instead of class")
	}
	// Should show SQL text section.
	if !strings.Contains(output, "SQL 文本") {
		t.Error("should contain SQL text section")
	}
	if !strings.Contains(output, "SELECT * FROM orders") {
		t.Error("should contain actual SQL text")
	}
	// Should NOT contain suggestions or parameters (rule-based mode).
	if strings.Contains(output, "修复建议") {
		t.Error("should not contain suggestions in rule-based mode")
	}
	if strings.Contains(output, "相关参数") {
		t.Error("should not contain parameters in rule-based mode")
	}
}

func TestFormatRuleDiagnosis_WithBlockingChains(t *testing.T) {
	report := BurstReport{
		Classification: Classification{
			Cause:      CauseLockContention,
			Confidence: 0.90,
		},
		BlockingChains: []BlockingChain{
			{RootSID: 100, VictimCount: 5, RootSQLID: "lock_sql_1"},
		},
	}

	output := FormatRuleDiagnosis(report)

	if !strings.Contains(output, "阻塞链") {
		t.Error("should contain blocking chain section")
	}
	if !strings.Contains(output, "SID 100") {
		t.Error("should contain root SID")
	}
}

func TestFormatReportHistory_WithTrigger(t *testing.T) {
	reports := []*BurstReport{
		{
			TriggerEvent:   TriggerEvent{Metric: "active_sessions", Baseline: 1, Current: 10, Threshold: 4},
			Classification: Classification{Cause: CauseBadSQL, Confidence: 0.80},
			PeakActive: 10, DurationSec: 3.0,
		},
		{
			TriggerEvent:   TriggerEvent{Metric: "lock_sessions", Baseline: 0, Current: 8, Threshold: 5},
			Classification: Classification{Cause: CauseLockContention, Confidence: 0.92},
			PeakActive: 20, DurationSec: 8.5,
		},
	}

	output := FormatReportHistory(reports)

	if !strings.Contains(output, "2 条") {
		t.Error("should mention count")
	}
	// Newest first: lock contention trigger should be #1.
	if !strings.Contains(output, "Lock Wait") {
		t.Error("should contain trigger metric label")
	}
	if !strings.Contains(output, "Active Sessions") {
		t.Error("should contain active sessions trigger")
	}
}

func TestFormatReportHistory_FallbackClassification(t *testing.T) {
	// When no trigger info, should fall back to classification.
	reports := []*BurstReport{
		{
			Classification: Classification{Cause: CauseBadSQL, Confidence: 0.80},
			PeakActive: 10, DurationSec: 3.0,
		},
	}

	output := FormatReportHistory(reports)

	if !strings.Contains(output, "SQL并发冲高") {
		t.Error("should fall back to classification cause")
	}
}

func TestFormatReportHistory_Empty(t *testing.T) {
	output := FormatReportHistory(nil)
	if !strings.Contains(output, "暂无") {
		t.Error("empty history should say no records")
	}
}

func TestFormatRuleDiagnosis_EmptyReport(t *testing.T) {
	report := BurstReport{
		Classification: Classification{
			Cause:      CauseUnknown,
			Confidence: 0.30,
		},
	}

	output := FormatRuleDiagnosis(report)

	if !strings.Contains(output, "监控数据") {
		t.Error("should contain monitoring data header")
	}
	// Should not panic or error with empty slices.
	if output == "" {
		t.Error("should produce output even for empty report")
	}
}

func TestTriggerMetricLabel(t *testing.T) {
	cases := []struct {
		metric string
		want   string
	}{
		{"active_sessions", "活跃会话Active Sessions"},
		{"cpu_sessions", "活跃会话On CPU"},
		{"lock_sessions", "活跃会话Lock Wait"},
		{"io_sessions", "活跃会话I/O Wait"},
		{"long_sql", "活跃会话Long SQL(>30s)"},
		{"redo_rate", "Redo生成速率Redo Rate"},
		{"hard_parse_rate", "硬解析速率Hard Parse"},
		{"unknown_metric", "unknown_metric"},
	}
	for _, tc := range cases {
		got := TriggerMetricLabel(tc.metric)
		if got != tc.want {
			t.Errorf("TriggerMetricLabel(%q) = %q, want %q", tc.metric, got, tc.want)
		}
	}
}

func TestFormatAlertDescription(t *testing.T) {
	cases := []struct {
		name     string
		trigger  TriggerEvent
		duration float64
		contains []string
	}{
		{
			name: "T1 active sessions",
			trigger: TriggerEvent{
				Metric: "active_sessions", Baseline: 2, Current: 15, Threshold: 8.3,
			},
			duration: 5,
			contains: []string{"活跃会话Active Sessions", "2→15个", "3σ阈值8.3"},
		},
		{
			name: "T6 temp usage",
			trigger: TriggerEvent{
				Metric: "temp_used_pct", Baseline: 40, Current: 100, Threshold: 95,
				Strategy: StrategyT6,
			},
			duration: 5,
			contains: []string{"临时表空间Temp使用率", "40%→100%", "红线95%"},
		},
		{
			name: "T9 commit rate drop",
			trigger: TriggerEvent{
				Metric: "commit_rate", Baseline: 200, Current: 12, Threshold: 40,
				Strategy: StrategyT9,
			},
			duration: 10,
			contains: []string{"提交速度TPS", "200→12/s", "较基线下降94%"},
		},
		{
			name: "immediate trigger deadlock",
			trigger: TriggerEvent{
				Metric: "enqueue_deadlocks", Baseline: 0, Current: 2,
			},
			duration: 0,
			contains: []string{"死锁Deadlock", "0→2次", "立即触发"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatAlertDescription(tc.trigger, tc.duration)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("FormatAlertDescription() = %q, want to contain %q", got, want)
				}
			}
		})
	}
}

func TestBoxAlignment(t *testing.T) {
	report := BurstReport{
		DurationSec:    5.0,
		PeakActive:     10,
		BaselineActive: 2.0,
		TriggerEvent: TriggerEvent{
			Metric: "active_sessions", Baseline: 2, Current: 10, Threshold: 8,
		},
	}
	termWidth := 100
	output := FormatRuleDiagnosisWidth(report, termWidth)
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		w := displayWidth(line)
		if w != termWidth {
			t.Errorf("line %d width = %d, want %d: %q", i, w, termWidth, line)
		}
	}
}

func TestScenarioBlocks_LockNoWaitEvents(t *testing.T) {
	// lock_sessions scenario should NOT show wait events (it's just lock waits).
	report := BurstReport{
		DurationSec:    10.0,
		PeakActive:     5,
		BaselineActive: 0,
		TriggerEvent: TriggerEvent{
			Metric: "lock_sessions", Baseline: 0, Current: 5, Threshold: 3,
		},
		WaitProfile: []WaitBucket{
			{Event: "enq: TX - row lock contention", Percentage: 80.0},
		},
	}
	output := FormatRuleDiagnosisWidth(report, 80)

	// Should NOT contain wait events (lock scenario suppresses them).
	if strings.Contains(output, "等待事件 Top 5") {
		t.Error("lock_sessions scenario should NOT show wait events")
	}
	// Should still contain trigger info.
	if !strings.Contains(output, "活跃会话Lock Wait") {
		t.Error("should contain trigger metric label")
	}
}

func TestScenarioBlocks_StorageNoSQL(t *testing.T) {
	// Storage scenarios should NOT show Top SQL or SQL text.
	report := BurstReport{
		DurationSec: 5.0,
		TriggerEvent: TriggerEvent{
			Metric: "temp_used_pct", Baseline: 40, Current: 100, Threshold: 95,
			Strategy: StrategyT6,
		},
		TopSQLs: []SQLProfile{
			{SQLID: "test123", MaxElapsedSec: 1.0, SQLText: "SELECT 1"},
		},
	}
	output := FormatRuleDiagnosisWidth(report, 80)

	if strings.Contains(output, "Top SQL") {
		t.Error("storage scenario should NOT show Top SQL")
	}
	if strings.Contains(output, "SQL 文本") {
		t.Error("storage scenario should NOT show SQL text")
	}
}

func TestBlockG_SpaceDetails(t *testing.T) {
	report := BurstReport{
		DurationSec: 5.0,
		TriggerEvent: TriggerEvent{
			Metric: string(MetricTablespaceUsedPct), Baseline: 80, Current: 95, Threshold: 90,
			Strategy: StrategyT6,
		},
		SpaceDetails: []SpaceDetail{
			{Name: "USERS", UsedMB: 2048, TotalMB: 4096, UsedPct: 50.0},
			{Name: "SYSTEM", UsedMB: 1024, TotalMB: 2048, UsedPct: 50.0},
		},
	}
	output := FormatRuleDiagnosisWidth(report, 100)

	if !strings.Contains(output, "表空间明细") {
		t.Error("tablespace scenario should show space detail header")
	}
	if !strings.Contains(output, "USERS") {
		t.Error("should contain tablespace name")
	}
	if !strings.Contains(output, "50.0%") {
		t.Error("should contain usage percentage")
	}
}

func TestBlockG_FRADetails(t *testing.T) {
	report := BurstReport{
		DurationSec: 5.0,
		TriggerEvent: TriggerEvent{
			Metric: string(MetricFRAUsedPct), Baseline: 60, Current: 92, Threshold: 85,
			Strategy: StrategyT6,
		},
		SpaceDetails: []SpaceDetail{
			{Name: "ARCHIVELOG", UsedPct: 45.2, Extra: "45.2%"},
			{Name: "BACKUPSET", UsedPct: 30.1, Extra: "30.1%"},
		},
	}
	output := FormatRuleDiagnosisWidth(report, 100)

	if !strings.Contains(output, "FRA 使用明细") {
		t.Error("FRA scenario should show FRA detail header")
	}
	if !strings.Contains(output, "ARCHIVELOG") {
		t.Error("should contain FRA component name")
	}
}

func TestBlockH_ParamDetails(t *testing.T) {
	report := BurstReport{
		DurationSec: 5.0,
		TriggerEvent: TriggerEvent{
			Metric: string(MetricLibraryCacheHit), Baseline: 95, Current: 89, Threshold: 95,
			Strategy: StrategyT8,
		},
		ParamDetails: []ParamDetail{
			{Name: "shared_pool_size", Value: "0"},
			{Name: "sga_target", Value: "838860800"},
		},
	}
	output := FormatRuleDiagnosisWidth(report, 100)

	if !strings.Contains(output, "相关参数") {
		t.Error("should show param detail header")
	}
	if !strings.Contains(output, "shared_pool_size") {
		t.Error("should contain parameter name")
	}
}

func TestBlockGH_NotShownForSessionScenario(t *testing.T) {
	report := BurstReport{
		DurationSec:  5.0,
		PeakActive:   20,
		TriggerEvent: TriggerEvent{Metric: string(MetricActive)},
		SpaceDetails: []SpaceDetail{{Name: "USERS", UsedPct: 50}},
		ParamDetails: []ParamDetail{{Name: "sga_target", Value: "1G"}},
	}
	output := FormatRuleDiagnosisWidth(report, 80)

	if strings.Contains(output, "空间明细") || strings.Contains(output, "表空间明细") {
		t.Error("session scenario should NOT show space details")
	}
	if strings.Contains(output, "相关参数") {
		t.Error("session scenario should NOT show param details")
	}
}
