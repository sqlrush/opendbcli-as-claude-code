/*-------------------------------------------------------------------------
 *
 * task_space_test.go
 *	  Test cases for task_space.go (scheduler package):
 *	  TestLinearRegressionSlope, TestSpaceTrackerAnalyze,
 *	  TestSpaceTrackerPersistence.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/task_space_test.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"math"
	"testing"
	"time"
)

func TestLinearRegressionSlope(t *testing.T) {
	// Perfect linear: y = 2x + 1
	xs := []float64{0, 1, 2, 3, 4}
	ys := []float64{1, 3, 5, 7, 9}
	slope := linearRegressionSlope(xs, ys)
	if math.Abs(slope-2.0) > 0.001 {
		t.Errorf("expected slope ~2.0, got %f", slope)
	}

	// Flat: y = 5
	ys = []float64{5, 5, 5, 5, 5}
	slope = linearRegressionSlope(xs, ys)
	if math.Abs(slope) > 0.001 {
		t.Errorf("expected slope ~0, got %f", slope)
	}

	// Too few points.
	slope = linearRegressionSlope([]float64{1}, []float64{1})
	if slope != 0 {
		t.Errorf("expected 0 for single point, got %f", slope)
	}
}

func TestSpaceTrackerAnalyze(t *testing.T) {
	dir := t.TempDir()
	tracker := NewSpaceTracker(dir)

	now := time.Now()
	latest := SpaceRecord{
		Timestamp: now,
		Spaces: map[string]SpaceUsage{
			"USERS":  {UsedMB: 4500, TotalMB: 5000, UsedPct: 90},
			"SYSAUX": {UsedMB: 400, TotalMB: 1000, UsedPct: 40},
			"TEMP":   {UsedMB: 9800, TotalMB: 10000, UsedPct: 98},
		},
	}

	alerts := tracker.Analyze(latest, 85, 95)

	// USERS at 90% → WARN, TEMP at 98% → CRITICAL, SYSAUX at 40% → no alert.
	warnCount := 0
	critCount := 0
	for _, a := range alerts {
		switch a.Severity {
		case "WARN":
			warnCount++
			if a.Tablespace != "USERS" {
				t.Errorf("expected USERS WARN, got %s", a.Tablespace)
			}
		case "CRITICAL":
			critCount++
			if a.Tablespace != "TEMP" {
				t.Errorf("expected TEMP CRITICAL, got %s", a.Tablespace)
			}
		}
	}
	if warnCount != 1 {
		t.Errorf("expected 1 WARN, got %d", warnCount)
	}
	if critCount != 1 {
		t.Errorf("expected 1 CRITICAL, got %d", critCount)
	}
}

func TestSpaceTrackerPersistence(t *testing.T) {
	dir := t.TempDir()

	t1 := NewSpaceTracker(dir)
	t1.Add(SpaceRecord{
		Timestamp: time.Now(),
		Spaces:    map[string]SpaceUsage{"USERS": {UsedMB: 100, TotalMB: 200, UsedPct: 50}},
	})

	t2 := NewSpaceTracker(dir)
	t2.mu.Lock()
	n := len(t2.records)
	t2.mu.Unlock()
	if n != 1 {
		t.Errorf("expected 1 record after reload, got %d", n)
	}
}

func TestSpaceTrackerGrowthPrediction(t *testing.T) {
	dir := t.TempDir()
	tracker := NewSpaceTracker(dir)

	base := time.Now().Add(-3 * time.Hour)
	// Add 3 records showing linear growth: 100, 200, 300 MB used.
	for i := 0; i < 3; i++ {
		tracker.Add(SpaceRecord{
			Timestamp: base.Add(time.Duration(i) * time.Hour),
			Spaces: map[string]SpaceUsage{
				"USERS": {
					UsedMB:  float64(100 + i*100),
					TotalMB: 1000,
					UsedPct: float64(10 + i*10),
				},
			},
		})
	}

	// Now at 90%: should trigger WARN with growth prediction.
	latest := SpaceRecord{
		Timestamp: base.Add(3 * time.Hour),
		Spaces: map[string]SpaceUsage{
			"USERS": {UsedMB: 900, TotalMB: 1000, UsedPct: 90},
		},
	}
	alerts := tracker.Analyze(latest, 85, 95)
	if len(alerts) == 0 {
		t.Fatal("expected at least 1 alert")
	}
	alert := alerts[0]
	if alert.GrowthRate <= 0 {
		t.Error("expected positive growth rate")
	}
}

func TestFormatSpaceAlerts(t *testing.T) {
	alerts := []SpaceAlert{
		{Tablespace: "USERS", UsedPct: 90, Severity: "WARN"},
	}
	s := FormatSpaceAlerts(alerts)
	if s == "" {
		t.Error("expected non-empty output")
	}

	// No alerts.
	s = FormatSpaceAlerts(nil)
	if s != "" {
		t.Errorf("expected empty for no alerts, got %q", s)
	}
}
