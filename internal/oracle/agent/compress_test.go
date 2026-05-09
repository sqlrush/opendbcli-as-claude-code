/*-------------------------------------------------------------------------
 *
 * compress_test.go
 *	  Test cases for compress.go (agent package):
 *	  TestCompressReport_ContainsAllSections,
 *	  TestCompressReport_ContainsTriggerInfo,
 *	  TestCompressReport_ContainsClassification.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/compress_test.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

func testReport() sentinel.BurstReport {
	return sentinel.BurstReport{
		TriggerEvent: sentinel.TriggerEvent{
			Timestamp:  time.Now(),
			Metric:     "active_non_idle",
			Baseline:   10,
			Current:    50,
			Threshold:  25,
			Multiplier: 5.0,
		},
		DurationSec:    15.2,
		PeakActive:     50,
		BaselineActive: 10,
		RawFrameCount:  75,
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "abc123", SQLText: "SELECT * FROM orders WHERE status = 'PENDING'", OccurrenceRate: 0.85, MaxConcurrent: 8, WaitClass: "User I/O", MaxElapsedSec: 12.5, PlanHashValue: 3847291056},
			{SQLID: "def456", SQLText: "UPDATE inventory SET qty = qty - 1", OccurrenceRate: 0.3, MaxConcurrent: 2, WaitClass: "CPU", MaxElapsedSec: 1.2},
		},
		BlockingChains: []sentinel.BlockingChain{
			{RootSID: 142, RootSQLID: "abc123", RootEvent: "TX - row lock", VictimCount: 5},
		},
		WaitProfile: []sentinel.WaitBucket{
			{WaitClass: "User I/O", TotalMs: 5000, Percentage: 50},
			{WaitClass: "CPU", TotalMs: 3000, Percentage: 30},
			{WaitClass: "Commit", TotalMs: 2000, Percentage: 20},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"db%":                            {Avg: 25, Max: 45, Min: 10, Trend: "spike"},
			string(sentinel.MetricActive):    {Avg: 35, Max: 50, Min: 10, Trend: "spike"},
			string(sentinel.MetricCommitRate): {Avg: 1200, Max: 1800, Min: 800, Trend: "rising"},
			"qps":                            {Avg: 5000, Max: 8000, Min: 3000, Trend: "rising"},
			string(sentinel.MetricRedoRate):  {Avg: 2048, Max: 4096, Min: 512, Trend: "stable"},
			"wtr%":                           {Avg: 8, Max: 15, Min: 3, Trend: "rising"},
		},
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.85,
			Evidence:   []string{"SQL abc123 出现率 85%, 峰值 8 并发"},
		},
	}
}

func TestCompressReport_ContainsAllSections(t *testing.T) {
	result := CompressReport(testReport())

	sections := []string{
		"性能异常报告",
		"根因判定",
		"指标摘要",
		"等待分布",
		"Top SQL",
		"阻塞链",
	}
	for _, s := range sections {
		if !strings.Contains(result, s) {
			t.Errorf("missing section: %s", s)
		}
	}
}

func TestCompressReport_ContainsTriggerInfo(t *testing.T) {
	result := CompressReport(testReport())

	if !strings.Contains(result, "active_non_idle") {
		t.Error("missing trigger metric")
	}
	if !strings.Contains(result, "50.0") {
		t.Error("missing current value")
	}
	if !strings.Contains(result, "5.0倍") {
		t.Error("missing multiplier")
	}
}

func TestCompressReport_ContainsClassification(t *testing.T) {
	result := CompressReport(testReport())

	if !strings.Contains(result, "bad_sql") {
		t.Error("missing cause type")
	}
	if !strings.Contains(result, "85%") {
		t.Error("missing confidence")
	}
	if !strings.Contains(result, "abc123") {
		t.Error("missing evidence SQL ID")
	}
}

func TestCompressReport_ContainsMetrics(t *testing.T) {
	result := CompressReport(testReport())

	metrics := []string{"db%", "tps", "qps", "active", "redo_kb/s", "wtr%"}
	for _, m := range metrics {
		if !strings.Contains(result, m) {
			t.Errorf("missing metric: %s", m)
		}
	}
}

func TestCompressReport_LimitsTopSQL(t *testing.T) {
	report := testReport()
	// Add more than 3 SQLs
	for i := 0; i < 5; i++ {
		report.TopSQLs = append(report.TopSQLs, sentinel.SQLProfile{
			SQLID: "extra", OccurrenceRate: 0.1,
		})
	}
	result := CompressReport(report)

	// Should only show top 3
	count := strings.Count(result, "出现率=")
	if count > 3 {
		t.Errorf("shown %d SQLs, want max 3", count)
	}
}

func TestCompressReport_TruncatesLongSQL(t *testing.T) {
	report := testReport()
	report.TopSQLs = []sentinel.SQLProfile{{
		SQLID:          "long_sql",
		SQLText:        strings.Repeat("SELECT ", 20), // 140 chars
		OccurrenceRate: 0.9,
	}}
	result := CompressReport(report)

	if !strings.Contains(result, "...") {
		t.Error("long SQL text should be truncated with ...")
	}
}

func TestCompressReport_EmptyReport(t *testing.T) {
	report := sentinel.BurstReport{
		Classification: sentinel.Classification{
			Cause: sentinel.CauseUnknown,
		},
	}
	result := CompressReport(report)

	if !strings.Contains(result, "性能异常报告") {
		t.Error("empty report should still have header")
	}
	if !strings.Contains(result, "unknown") {
		t.Error("should show unknown cause")
	}
}

func TestCompressReport_TokenEstimate(t *testing.T) {
	result := CompressReport(testReport())

	// Rough estimate: 1 token ≈ 2 Chinese chars or 4 English chars
	// Target: < 2000 tokens ≈ ~4000 chars Chinese or ~8000 chars English
	if len(result) > 8000 {
		t.Errorf("compressed report too large: %d bytes, target < 8000", len(result))
	}
}
