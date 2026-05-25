/*-------------------------------------------------------------------------
 *
 * analyze_test.go
 *	  Test cases for analyze.go (sentinel package):
 *	  TestAnalyzeSQLFrequency_Basic, TestAnalyzeSQLFrequency_Empty,
 *	  TestAnalyzeSQLFrequency_EmptySQLID.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/analyze_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
)

func makeTestFrames() []BurstFrame {
	return []BurstFrame{
		{
			Seq:       0,
			Timestamp: time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
			Snapshot: dbtop.Snapshot{
				DBPercent: 15.0, WTRPercent: 5.0,
				TPS: 1000, QPS: 5000, RedoKBs: 2048,
				ActiveCount: 20,
				Sessions: []dbtop.SessionRow{
					{SID: 100, Username: "APP", SQLID: "sql_a", Event: "db file sequential read", WaitClass: "User I/O", ElapsedSec: 2.0, SQLText: "SELECT 1"},
					{SID: 101, Username: "APP", SQLID: "sql_a", Event: "db file sequential read", WaitClass: "User I/O", ElapsedSec: 3.0, SQLText: "SELECT 1"},
					{SID: 102, Username: "APP", SQLID: "sql_b", Event: "CPU", WaitClass: "CPU", ElapsedSec: 1.0, SQLText: "UPDATE t SET x=1"},
				},
				Events: []dbtop.WaitEvent{
					{Event: "db file sequential read", WaitClass: "User I/O", DTimeMs: 500},
					{Event: "DB CPU", WaitClass: "CPU", DTimeMs: 300},
				},
			},
		},
		{
			Seq:       1,
			Timestamp: time.Date(2026, 3, 10, 12, 0, 0, 200000000, time.UTC),
			Snapshot: dbtop.Snapshot{
				DBPercent: 25.0, WTRPercent: 8.0,
				TPS: 1500, QPS: 7000, RedoKBs: 3000,
				ActiveCount: 30,
				Sessions: []dbtop.SessionRow{
					{SID: 100, Username: "APP", SQLID: "sql_a", Event: "db file sequential read", WaitClass: "User I/O", ElapsedSec: 5.0, SQLText: "SELECT 1"},
					{SID: 103, Username: "APP", SQLID: "sql_c", Event: "log file sync", WaitClass: "Commit", ElapsedSec: 0.5, SQLText: "COMMIT"},
				},
				Events: []dbtop.WaitEvent{
					{Event: "db file sequential read", WaitClass: "User I/O", DTimeMs: 800},
					{Event: "log file sync", WaitClass: "Commit", DTimeMs: 200},
				},
			},
		},
	}
}

func TestAnalyzeSQLFrequency_Basic(t *testing.T) {
	profiles := AnalyzeSQLFrequency(makeTestFrames())

	if len(profiles) == 0 {
		t.Fatal("expected profiles, got none")
	}

	// sql_a appears in both frames, should be first (highest occurrence rate)
	if profiles[0].SQLID != "sql_a" {
		t.Errorf("top SQL = %q, want sql_a", profiles[0].SQLID)
	}
	if profiles[0].OccurrenceRate != 1.0 {
		t.Errorf("sql_a occurrence rate = %f, want 1.0", profiles[0].OccurrenceRate)
	}
	if profiles[0].MaxConcurrent != 2 {
		t.Errorf("sql_a max concurrent = %d, want 2", profiles[0].MaxConcurrent)
	}
	if profiles[0].MaxElapsedSec != 5.0 {
		t.Errorf("sql_a max elapsed = %f, want 5.0", profiles[0].MaxElapsedSec)
	}
}

func TestAnalyzeSQLFrequency_Empty(t *testing.T) {
	result := AnalyzeSQLFrequency(nil)
	if result != nil {
		t.Errorf("expected nil for empty frames, got %v", result)
	}
}

func TestAnalyzeSQLFrequency_EmptySQLID(t *testing.T) {
	frames := []BurstFrame{{
		Snapshot: dbtop.Snapshot{
			Sessions: []dbtop.SessionRow{
				{SID: 1, SQLID: "", Event: "idle"},
			},
		},
	}}
	profiles := AnalyzeSQLFrequency(frames)
	if len(profiles) != 0 {
		t.Errorf("expected 0 profiles for empty SQLID, got %d", len(profiles))
	}
}

func TestAnalyzeBlockingChains_Basic(t *testing.T) {
	frames := []BurstFrame{{
		Snapshot: dbtop.Snapshot{
			Sessions: []dbtop.SessionRow{
				{SID: 100, SQLID: "root_sql", Event: "TX - row lock", WaitClass: "Application"},
				{SID: 101, SQLID: "wait_sql", Event: "enq: TX - row lock contention", WaitClass: "Application",
					Burst: &dbtop.BurstDetail{BlockingSID: 100}},
				{SID: 102, SQLID: "wait_sql2", Event: "enq: TX - row lock contention", WaitClass: "Application",
					Burst: &dbtop.BurstDetail{BlockingSID: 100}},
			},
		},
	}}

	chains := AnalyzeBlockingChains(frames)
	if len(chains) != 1 {
		t.Fatalf("expected 1 chain, got %d", len(chains))
	}
	if chains[0].RootSID != 100 {
		t.Errorf("root SID = %d, want 100", chains[0].RootSID)
	}
	if chains[0].VictimCount != 2 {
		t.Errorf("victim count = %d, want 2", chains[0].VictimCount)
	}
	if chains[0].RootSQLID != "root_sql" {
		t.Errorf("root SQL = %q, want root_sql", chains[0].RootSQLID)
	}
}

func TestAnalyzeBlockingChains_Empty(t *testing.T) {
	chains := AnalyzeBlockingChains(nil)
	if len(chains) != 0 {
		t.Errorf("expected 0 chains for empty frames, got %d", len(chains))
	}
}

func TestAnalyzeBlockingChains_NoBurst(t *testing.T) {
	frames := []BurstFrame{{
		Snapshot: dbtop.Snapshot{
			Sessions: []dbtop.SessionRow{
				{SID: 100, SQLID: "sql1"},
				{SID: 101, SQLID: "sql2"},
			},
		},
	}}
	chains := AnalyzeBlockingChains(frames)
	if len(chains) != 0 {
		t.Errorf("expected 0 chains, got %d", len(chains))
	}
}

func TestAnalyzeWaitDistribution_Basic(t *testing.T) {
	buckets := AnalyzeWaitDistribution(makeTestFrames())

	if len(buckets) == 0 {
		t.Fatal("expected buckets, got none")
	}

	// User I/O should be the top bucket (500+800=1300ms total)
	if buckets[0].WaitClass != "User I/O" {
		t.Errorf("top wait class = %q, want User I/O", buckets[0].WaitClass)
	}
	if buckets[0].TotalMs != 1300 {
		t.Errorf("User I/O total ms = %f, want 1300", buckets[0].TotalMs)
	}

	// Verify percentages sum to ~100
	totalPct := 0.0
	for _, b := range buckets {
		totalPct += b.Percentage
	}
	if totalPct < 99.9 || totalPct > 100.1 {
		t.Errorf("total percentage = %f, want ~100", totalPct)
	}
}

func TestAnalyzeWaitDistribution_Empty(t *testing.T) {
	result := AnalyzeWaitDistribution(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestAnalyzeMetrics_Basic(t *testing.T) {
	metrics := AnalyzeMetrics(makeTestFrames())

	if len(metrics) == 0 {
		t.Fatal("expected metrics, got none")
	}

	dbPct, ok := metrics["db%"]
	if !ok {
		t.Fatal("missing db% metric")
	}
	if dbPct.Avg != 20.0 {
		t.Errorf("db%% avg = %f, want 20.0", dbPct.Avg)
	}
	if dbPct.Min != 15.0 {
		t.Errorf("db%% min = %f, want 15.0", dbPct.Min)
	}
	if dbPct.Max != 25.0 {
		t.Errorf("db%% max = %f, want 25.0", dbPct.Max)
	}

	active, ok := metrics[string(MetricActive)]
	if !ok {
		t.Fatal("missing active_sessions metric")
	}
	if active.Avg != 25.0 {
		t.Errorf("active avg = %f, want 25.0", active.Avg)
	}
}

func TestAnalyzeMetrics_Empty(t *testing.T) {
	result := AnalyzeMetrics(nil)
	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name      string
		vals      []float64
		wantAvg   float64
		wantMin   float64
		wantMax   float64
		wantTrend string
	}{
		{"empty", nil, 0, 0, 0, ""},
		{"single", []float64{5.0}, 5.0, 5.0, 5.0, "stable"},
		{"stable", []float64{10, 10, 10, 10}, 10, 10, 10, "stable"},
		{"rising", []float64{10, 10, 12, 13}, 11.25, 10, 13, "rising"},
		{"spike", []float64{10, 10, 20, 25}, 16.25, 10, 25, "spike"},
		{"falling", []float64{20, 20, 17, 16}, 18.25, 16, 20, "falling"},
		{"drop", []float64{20, 20, 8, 5}, 13.25, 5, 20, "drop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarize(tt.vals)
			if got.Avg != tt.wantAvg {
				t.Errorf("avg = %f, want %f", got.Avg, tt.wantAvg)
			}
			if got.Min != tt.wantMin {
				t.Errorf("min = %f, want %f", got.Min, tt.wantMin)
			}
			if got.Max != tt.wantMax {
				t.Errorf("max = %f, want %f", got.Max, tt.wantMax)
			}
			if got.Trend != tt.wantTrend {
				t.Errorf("trend = %q, want %q", got.Trend, tt.wantTrend)
			}
		})
	}
}

func TestDetectTrend(t *testing.T) {
	tests := []struct {
		first, second float64
		want          string
	}{
		{0, 0, "stable"},
		{100, 100, "stable"},
		{100, 120, "rising"},
		{100, 160, "spike"},
		{100, 80, "falling"},
		{100, 40, "drop"},
	}
	for _, tt := range tests {
		got := detectTrend(tt.first, tt.second)
		if got != tt.want {
			t.Errorf("detectTrend(%f, %f) = %q, want %q", tt.first, tt.second, got, tt.want)
		}
	}
}

func TestAnalyze_Integration(t *testing.T) {
	trigger := TriggerEvent{
		Timestamp: time.Now(),
		Metric:    "active_non_idle",
		Baseline:  10,
		Current:   35,
		Threshold: 25,
	}
	result := BurstResult{
		Trigger:    trigger,
		Frames:     makeTestFrames(),
		StartTime:  time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		EndTime:    time.Date(2026, 3, 10, 12, 0, 0, 400000000, time.UTC),
		PeakActive: 30,
	}
	baseline := Baseline{AvgActive: 10.0}

	report := Analyze(result, baseline)

	if report.PeakActive != 30 {
		t.Errorf("PeakActive = %d, want 30", report.PeakActive)
	}
	if report.BaselineActive != 10.0 {
		t.Errorf("BaselineActive = %f, want 10.0", report.BaselineActive)
	}
	if report.RawFrameCount != 2 {
		t.Errorf("RawFrameCount = %d, want 2", report.RawFrameCount)
	}
	if report.DurationSec <= 0 {
		t.Error("DurationSec should be > 0")
	}
	if len(report.TopSQLs) == 0 {
		t.Error("TopSQLs should not be empty")
	}
	if len(report.WaitProfile) == 0 {
		t.Error("WaitProfile should not be empty")
	}
	if len(report.Metrics) == 0 {
		t.Error("Metrics should not be empty")
	}
}

func TestFindRoot_DirectBlocker(t *testing.T) {
	sessions := []dbtop.SessionRow{
		{SID: 100},
		{SID: 101, Burst: &dbtop.BurstDetail{BlockingSID: 100}},
	}
	root := findRoot(sessions, 100)
	if root != 100 {
		t.Errorf("findRoot = %d, want 100", root)
	}
}

func TestFindRoot_ChainedBlocker(t *testing.T) {
	sessions := []dbtop.SessionRow{
		{SID: 100},
		{SID: 101, Burst: &dbtop.BurstDetail{BlockingSID: 100}},
		{SID: 102, Burst: &dbtop.BurstDetail{BlockingSID: 101}},
	}
	root := findRoot(sessions, 102)
	if root != 100 {
		t.Errorf("findRoot chained = %d, want 100", root)
	}
}

func TestFindRoot_CycleDetection(t *testing.T) {
	sessions := []dbtop.SessionRow{
		{SID: 100, Burst: &dbtop.BurstDetail{BlockingSID: 101}},
		{SID: 101, Burst: &dbtop.BurstDetail{BlockingSID: 100}},
	}
	root := findRoot(sessions, 100)
	// Should not infinite loop — returns one of the cycle members
	if root != 100 && root != 101 {
		t.Errorf("findRoot cycle = %d, want 100 or 101", root)
	}
}

func TestAvgSlice(t *testing.T) {
	if avg := avgSlice(nil); avg != 0 {
		t.Errorf("avgSlice(nil) = %f, want 0", avg)
	}
	if avg := avgSlice([]float64{10, 20, 30}); avg != 20 {
		t.Errorf("avgSlice([10,20,30]) = %f, want 20", avg)
	}
}

func TestAnalyzeSQLFrequency_WithBurstDetail(t *testing.T) {
	frames := []BurstFrame{{
		Snapshot: dbtop.Snapshot{
			Sessions: []dbtop.SessionRow{
				{SID: 1, SQLID: "sql_x", WaitClass: "User I/O", ElapsedSec: 1.0,
					Burst: &dbtop.BurstDetail{PlanHashValue: 12345}},
			},
		},
	}}
	profiles := AnalyzeSQLFrequency(frames)
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}
	if profiles[0].PlanHashValue != 12345 {
		t.Errorf("PlanHashValue = %d, want 12345", profiles[0].PlanHashValue)
	}
}
