/*-------------------------------------------------------------------------
 *
 * post_burst_test.go
 *	  Test cases for post_burst.go (sentinel package): TestPbStr,
 *	  TestPbFloat, TestEnrichReport_NilDriver.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/post_burst_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
)

func TestPbStr(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{nil, ""},
		{"hello", "hello"},
		{42, "42"},
		{3.14, "3.14"},
	}
	for _, tt := range tests {
		got := pbStr(tt.input)
		if got != tt.want {
			t.Errorf("pbStr(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestPbFloat(t *testing.T) {
	tests := []struct {
		input interface{}
		want  float64
	}{
		{nil, 0},
		{float64(3.14), 3.14},
		{float32(2.5), 2.5},
		{42, 42},
		{int64(100), 100},
		{"99.5", 99.5},
	}
	for _, tt := range tests {
		got := pbFloat(tt.input)
		if got != tt.want {
			t.Errorf("pbFloat(%v) = %f, want %f", tt.input, got, tt.want)
		}
	}
}

func TestEnrichReport_NilDriver(t *testing.T) {
	report := &BurstReport{
		TriggerEvent: TriggerEvent{Metric: string(MetricTempUsedPct)},
	}
	// Should not panic with nil driver.
	EnrichReport(nil, nil, report)
	if len(report.SpaceDetails) != 0 {
		t.Error("should have no space details with nil driver")
	}
}

func TestScenarioSpaceQueries_Coverage(t *testing.T) {
	// Verify all storage metrics have space queries.
	storageMetrics := []MetricName{
		MetricTablespaceUsedPct, MetricTempUsedPct, MetricUndoUsedPct,
		MetricFRAUsedPct, MetricASMUsedPct,
	}
	for _, m := range storageMetrics {
		if _, ok := scenarioSpaceQueries[m]; !ok {
			t.Errorf("missing space query for %s", m)
		}
	}
}

func TestScenarioParams_Coverage(t *testing.T) {
	// Verify memory/cache + redo metrics have param configs.
	paramMetrics := []MetricName{
		MetricBufferCacheHit, MetricLibraryCacheHit, MetricPGAUsedPct,
		MetricSharedPoolFreePct, MetricLogFileSyncUs, MetricLogSwitchRate,
	}
	for _, m := range paramMetrics {
		if _, ok := scenarioParams[m]; !ok {
			t.Errorf("missing param config for %s", m)
		}
	}
}
