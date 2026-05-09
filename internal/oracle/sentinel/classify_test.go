/*-------------------------------------------------------------------------
 *
 * classify_test.go
 *	  Test cases for classify.go (sentinel package):
 *	  TestClassify_LockContention_HighPriority,
 *	  TestClassify_LockContention_LowVictims, TestClassify_BadSQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/classify_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
)

func TestClassify_LockContention_HighPriority(t *testing.T) {
	report := BurstReport{
		BlockingChains: []BlockingChain{
			{RootSID: 100, RootSQLID: "sql_a", RootEvent: "TX - row lock", VictimCount: 8},
		},
	}
	c := Classify(report)
	if c.Cause != CauseLockContention {
		t.Errorf("cause = %q, want lock_contention", c.Cause)
	}
	if c.Confidence < 0.9 {
		t.Errorf("confidence = %f, want >= 0.9 for 8 victims", c.Confidence)
	}
	if len(c.Evidence) == 0 {
		t.Error("expected evidence")
	}
}

func TestClassify_LockContention_LowVictims(t *testing.T) {
	report := BurstReport{
		BlockingChains: []BlockingChain{
			{RootSID: 100, VictimCount: 1}, // below threshold of 2
		},
	}
	c := Classify(report)
	if c.Cause == CauseLockContention {
		t.Error("1 victim should not trigger lock_contention")
	}
}

func TestClassify_BadSQL(t *testing.T) {
	report := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "sql_hot", OccurrenceRate: 0.8, MaxConcurrent: 6, WaitClass: "User I/O", MaxElapsedSec: 10.0},
		},
	}
	c := Classify(report)
	if c.Cause != CauseBadSQL {
		t.Errorf("cause = %q, want bad_sql", c.Cause)
	}
	if c.Confidence < 0.8 {
		t.Errorf("confidence = %f, want >= 0.8", c.Confidence)
	}
}

func TestClassify_BadSQL_LowOccurrence(t *testing.T) {
	report := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "sql_ok", OccurrenceRate: 0.3, MaxConcurrent: 10},
		},
	}
	c := Classify(report)
	if c.Cause == CauseBadSQL {
		t.Error("low occurrence rate should not trigger bad_sql")
	}
}

func TestClassify_RedoBottleneck(t *testing.T) {
	report := BurstReport{
		WaitProfile: []WaitBucket{
			{WaitClass: "Commit", TotalMs: 5000, Percentage: 45},
		},
	}
	c := Classify(report)
	if c.Cause != CauseRedoBottleneck {
		t.Errorf("cause = %q, want redo_bottleneck", c.Cause)
	}
	if c.Confidence < 0.85 {
		t.Errorf("confidence = %f, want >= 0.85 for 45%%", c.Confidence)
	}
}

func TestClassify_LatchStorm(t *testing.T) {
	report := BurstReport{
		WaitProfile: []WaitBucket{
			{WaitClass: "Concurrency", TotalMs: 8000, Percentage: 50},
		},
		TopSQLs: make([]SQLProfile, 15), // 15 distinct SQLs
	}
	c := Classify(report)
	if c.Cause != CauseLatchStorm {
		t.Errorf("cause = %q, want latch_storm", c.Cause)
	}
	if c.Confidence < 0.8 {
		t.Errorf("confidence = %f, want >= 0.8", c.Confidence)
	}
}

func TestClassify_IOSubsystem(t *testing.T) {
	report := BurstReport{
		WaitProfile: []WaitBucket{
			{WaitClass: "User I/O", TotalMs: 7000, Percentage: 50},
			{WaitClass: "System I/O", TotalMs: 3000, Percentage: 21},
		},
		TopSQLs: []SQLProfile{
			{SQLID: "sql_1", OccurrenceRate: 0.2}, // scattered
		},
	}
	c := Classify(report)
	if c.Cause != CauseIOSubsystem {
		t.Errorf("cause = %q, want io_subsystem", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7", c.Confidence)
	}
}

func TestClassify_TrafficStorm(t *testing.T) {
	report := BurstReport{
		Metrics: map[string]MetricSummary{
			string(MetricActive): {Avg: 50, Max: 80, Trend: "spike"},
		},
		PeakActive:     80,
		BaselineActive: 10,
	}
	c := Classify(report)
	if c.Cause != CauseTrafficStorm {
		t.Errorf("cause = %q, want traffic_storm", c.Cause)
	}
	if c.Confidence < 0.7 {
		t.Errorf("confidence = %f, want >= 0.7 for 8x ratio", c.Confidence)
	}
}

func TestClassify_TrafficStorm_WithBlockers(t *testing.T) {
	report := BurstReport{
		Metrics: map[string]MetricSummary{
			string(MetricActive): {Trend: "spike"},
		},
		PeakActive:     80,
		BaselineActive: 10,
		BlockingChains: []BlockingChain{{RootSID: 1, VictimCount: 1}},
	}
	c := Classify(report)
	if c.Cause == CauseTrafficStorm {
		t.Error("should not classify as traffic_storm when blockers present")
	}
}

func TestClassify_Unknown(t *testing.T) {
	report := BurstReport{}
	c := Classify(report)
	if c.Cause != CauseUnknown {
		t.Errorf("cause = %q, want unknown", c.Cause)
	}
	if c.Confidence != 0 {
		t.Errorf("confidence = %f, want 0", c.Confidence)
	}
}

func TestClassify_PriorityOrder(t *testing.T) {
	// Lock contention should win over bad SQL when both match
	report := BurstReport{
		BlockingChains: []BlockingChain{
			{RootSID: 100, VictimCount: 5, RootSQLID: "sql_lock"},
		},
		TopSQLs: []SQLProfile{
			{SQLID: "sql_hot", OccurrenceRate: 0.9, MaxConcurrent: 10},
		},
	}
	c := Classify(report)
	if c.Cause != CauseLockContention {
		t.Errorf("cause = %q, want lock_contention (higher priority)", c.Cause)
	}
}

func TestClassifyLockContention_MediumVictims(t *testing.T) {
	r := BurstReport{
		BlockingChains: []BlockingChain{
			{RootSID: 100, VictimCount: 3, RootSQLID: "sql_a", RootEvent: "TX"},
		},
	}
	c := classifyLockContention(r)
	if c.Confidence != 0.7 {
		t.Errorf("confidence = %f, want 0.7 for 3 victims", c.Confidence)
	}
}

func TestClassifyBadSQL_MediumConcurrency(t *testing.T) {
	r := BurstReport{
		TopSQLs: []SQLProfile{
			{SQLID: "sql_m", OccurrenceRate: 0.6, MaxConcurrent: 3, WaitClass: "CPU"},
		},
	}
	c := classifyBadSQL(r)
	if c.Confidence != 0.6 {
		t.Errorf("confidence = %f, want 0.6 for medium concurrency", c.Confidence)
	}
}

func TestClassifyRedoBottleneck_LowPercentage(t *testing.T) {
	r := BurstReport{
		WaitProfile: []WaitBucket{
			{WaitClass: "Commit", Percentage: 15},
		},
	}
	c := classifyRedoBottleneck(r)
	if c.Confidence > 0 {
		t.Errorf("should not trigger for 15%% Commit wait")
	}
}

func TestFormatEvidence(t *testing.T) {
	got := formatEvidence("SID %d 阻塞 %d 个会话", 100, 5)
	want := "SID 100 阻塞 5 个会话"
	if got != want {
		t.Errorf("formatEvidence = %q, want %q", got, want)
	}
}
