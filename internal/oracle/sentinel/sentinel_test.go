/*-------------------------------------------------------------------------
 *
 * sentinel_test.go
 *	  Test cases for sentinel.go (sentinel package): TestState_String,
 *	  TestPushSample_BuildsBaseline, TestPushSample_WindowTrimming.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/sentinel_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
)

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateIdle, "idle"},
		{StateWatch, "watch"},
		{StateBurst, "burst"},
		{StateCooldown, "cooldown"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestPushSample_BuildsBaseline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.BaselineWindow = 5

	s := New(cfg, nil, nil, nil)

	// Not ready yet
	if s.baseline.Ready {
		t.Error("baseline should not be ready with 0 samples")
	}

	for i := 0; i < 3; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 10})
	}

	if !s.baseline.Ready {
		t.Error("baseline should be ready after MinSamples")
	}
	if s.baseline.AvgActive != 10.0 {
		t.Errorf("avg = %f, want 10.0", s.baseline.AvgActive)
	}
	if s.baseline.StdActive != 0.0 {
		t.Errorf("std = %f, want 0.0 (all same)", s.baseline.StdActive)
	}
}

func TestPushSample_WindowTrimming(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BaselineWindow = 3
	cfg.MinSamples = 1

	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: i * 10})
	}

	if len(s.baseline.Samples) != 3 {
		t.Errorf("samples = %d, want 3 (window trimmed)", len(s.baseline.Samples))
	}
	// Should contain the last 3: 20, 30, 40
	if s.baseline.Samples[0].ActiveNonIdle != 20 {
		t.Errorf("oldest sample = %d, want 20", s.baseline.Samples[0].ActiveNonIdle)
	}
}

func TestRecomputeBaseline_Stddev(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 2
	s := New(cfg, nil, nil, nil)

	// Push values: 10, 20 → mean=15, variance=25, std=5
	s.pushSample(SentinelSample{ActiveNonIdle: 10})
	s.pushSample(SentinelSample{ActiveNonIdle: 20})

	if s.baseline.AvgActive != 15.0 {
		t.Errorf("avg = %f, want 15.0", s.baseline.AvgActive)
	}
	if s.baseline.StdActive != 5.0 {
		t.Errorf("std = %f, want 5.0", s.baseline.StdActive)
	}
}

func TestDetectAnomaly_NoTriggerWhenNormal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 1
	s := New(cfg, nil, nil, nil)

	// Build baseline: avg=10, std=0
	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 10})
	}

	// Normal value — below both 3σ and soft absolute min (8)
	sample := SentinelSample{ActiveNonIdle: 12}
	trigger := s.detectAnomaly(sample)
	if trigger != nil {
		t.Error("should not trigger for value within threshold")
	}
}

func TestDetectAnomaly_TriggersOnSpike(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SigmaThreshold = 3.0
	cfg.SustainedCount = 1 // immediate trigger for this test
	s := New(cfg, nil, nil, nil)

	// Build baseline: values 8,10,12 → avg=10, std≈1.63
	s.pushSample(SentinelSample{ActiveNonIdle: 8})
	s.pushSample(SentinelSample{ActiveNonIdle: 10})
	s.pushSample(SentinelSample{ActiveNonIdle: 12})

	// 50 >> threshold, and 50 >= softAbsMin(8)
	sample := SentinelSample{ActiveNonIdle: 50, Timestamp: time.Now()}
	trigger := s.detectAnomaly(sample)
	if trigger == nil {
		t.Fatal("should trigger for spike to 50")
	}
	if trigger.Metric != string(MetricActive) {
		t.Errorf("metric = %q, want %q", trigger.Metric, MetricActive)
	}
	if trigger.Current != 50 {
		t.Errorf("current = %f, want 50", trigger.Current)
	}
	if trigger.Multiplier <= 1 {
		t.Errorf("multiplier = %f, should be > 1", trigger.Multiplier)
	}
}

func TestDetectAnomaly_CooldownPrevents(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.CooldownPeriod = time.Hour

	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 10})
	}

	s.lastTrigger = time.Now()

	sample := SentinelSample{ActiveNonIdle: 100}
	trigger := s.detectAnomaly(sample)
	if trigger != nil {
		t.Error("should not trigger during cooldown")
	}
}

func TestDetectAnomaly_NotReadyReturnsNil(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 10

	s := New(cfg, nil, nil, nil)
	s.pushSample(SentinelSample{ActiveNonIdle: 10})
	s.pushSample(SentinelSample{ActiveNonIdle: 10})

	sample := SentinelSample{ActiveNonIdle: 100}
	if trigger := s.detectAnomaly(sample); trigger != nil {
		t.Error("should not trigger when baseline not ready")
	}
}

func TestSentinel_StartStop(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PollInterval = 10 * time.Millisecond
	cfg.MinSamples = 3

	callCount := 0
	var mu sync.Mutex
	probe := func(_ context.Context) dbtop.Snapshot {
		mu.Lock()
		callCount++
		mu.Unlock()
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: 5,
		}
	}

	s := New(cfg, nil, probe, &dbtop.Collector{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Start(ctx)

	time.Sleep(100 * time.Millisecond)
	s.Stop()

	mu.Lock()
	count := callCount
	mu.Unlock()

	if count < 3 {
		t.Errorf("probe called %d times, want >= 3", count)
	}
}

func TestSentinel_IdleToWatch(t *testing.T) {
	cfg := DefaultConfig()
	cfg.PollInterval = 5 * time.Millisecond
	cfg.MinSamples = 3

	probe := func(_ context.Context) dbtop.Snapshot {
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: 5,
		}
	}

	s := New(cfg, nil, probe, &dbtop.Collector{})
	if s.CurrentState() != StateIdle {
		t.Errorf("initial state = %v, want idle", s.CurrentState())
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	s.Stop()

	state := s.CurrentState()
	if state != StateWatch {
		t.Errorf("after baseline built, state = %v, want watch", state)
	}
}

func TestDetectAnomaly_MinimumFloor(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SigmaThreshold = 3.0
	cfg.SustainedCount = 1
	s := New(cfg, nil, nil, nil)

	// All zeros → avg=0, std=0
	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 0})
	}

	// Threshold floor: avg+3 = 3, but softAbsMin=8 (no CPU info)
	// ActiveNonIdle=5: exceeds floor(3) but below softAbsMin(8) → no trigger
	if trigger := s.detectAnomaly(SentinelSample{ActiveNonIdle: 5}); trigger != nil {
		t.Error("should not trigger below soft absolute min (8)")
	}

	// ActiveNonIdle=10: exceeds both floor(3) and softAbsMin(8) → trigger
	if trigger := s.detectAnomaly(SentinelSample{ActiveNonIdle: 10}); trigger == nil {
		t.Error("should trigger above both floor and soft absolute min")
	}
}

// ── New tests for sustained count, absolute thresholds, hardware scaling ──

func TestDetectAnomaly_SustainedCount(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 3 // require 3 consecutive ticks
	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 5})
	}

	spike := SentinelSample{ActiveNonIdle: 50, Timestamp: time.Now()}

	// Tick 1: count=1, no trigger
	if trigger := s.detectAnomaly(spike); trigger != nil {
		t.Error("tick 1: should not trigger (need 3 consecutive)")
	}

	// Tick 2: count=2, no trigger
	if trigger := s.detectAnomaly(spike); trigger != nil {
		t.Error("tick 2: should not trigger (need 3 consecutive)")
	}

	// Tick 3: count=3, trigger!
	trigger := s.detectAnomaly(spike)
	if trigger == nil {
		t.Fatal("tick 3: should trigger after 3 consecutive")
	}
	if trigger.Metric != string(MetricActive) {
		t.Errorf("metric = %q, want active_sessions", trigger.Metric)
	}
}

func TestDetectAnomaly_SustainedCountResetOnNormal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 3
	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 5})
	}

	spike := SentinelSample{ActiveNonIdle: 50}
	normal := SentinelSample{ActiveNonIdle: 6}

	// 2 spikes, then normal, then 2 more spikes → never reaches 3 consecutive
	s.detectAnomaly(spike) // count=1
	s.detectAnomaly(spike) // count=2
	s.detectAnomaly(normal) // count reset to 0

	s.detectAnomaly(spike) // count=1
	if trigger := s.detectAnomaly(spike); trigger != nil {
		t.Error("should not trigger after reset (only 2 consecutive)")
	}
}

func TestDetectAnomaly_SoftAbsoluteMinFilters(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 1
	// No hardware info → softAbsMin for redo_rate = 5000 KB/s
	s := New(cfg, nil, nil, nil)

	// Build baseline: redo=0
	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 1, RedoKBPerSec: 0})
	}

	// Redo 0→4 KB/s: 3σ exceeded but 4 < 5000 softAbsMin → no trigger
	sample := SentinelSample{ActiveNonIdle: 1, RedoKBPerSec: 4}
	if trigger := s.detectAnomaly(sample); trigger != nil {
		t.Errorf("redo 0→4: should NOT trigger (below soft absolute min 5000), got metric=%s", trigger.Metric)
	}

	// Redo 0→6000 KB/s: exceeds both → trigger
	sample2 := SentinelSample{ActiveNonIdle: 1, RedoKBPerSec: 6000}
	if trigger := s.detectAnomaly(sample2); trigger == nil {
		t.Error("redo 0→6000: should trigger (above soft absolute min)")
	}
}

func TestDetectAnomaly_HardCeilingTriggers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 1
	// 8 CPU cores → hard ceiling for active = max(8*1.5, 8) * 2 = 24
	cfg.Hardware = HardwareProfile{CPUCores: 8}
	s := New(cfg, nil, nil, nil)

	// Build high baseline: avg=20, std=2
	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 20})
	}

	// 25: above hard ceiling (24) → trigger via path B even though 3σ might not be exceeded
	sample := SentinelSample{ActiveNonIdle: 25, Timestamp: time.Now()}
	trigger := s.detectAnomaly(sample)
	if trigger == nil {
		t.Fatal("should trigger via hard ceiling (25 >= 24)")
	}
	if trigger.Current != 25 {
		t.Errorf("current = %f, want 25", trigger.Current)
	}
}

func TestDetectAnomaly_HardCeilingWithSustained(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 3
	cfg.Hardware = HardwareProfile{CPUCores: 8}
	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 20})
	}

	spike := SentinelSample{ActiveNonIdle: 25} // above hard ceiling 24

	// Need 3 consecutive even for hard ceiling
	s.detectAnomaly(spike) // count=1
	s.detectAnomaly(spike) // count=2
	trigger := s.detectAnomaly(spike) // count=3 → trigger
	if trigger == nil {
		t.Fatal("should trigger via hard ceiling after 3 sustained ticks")
	}
}

func TestDetectAnomaly_HardwareScaling(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MinSamples = 3
	cfg.SustainedCount = 1

	// 64 CPU cores → softAbsMin for active = max(64*1.5, 8) = 96
	cfg.Hardware = HardwareProfile{CPUCores: 64}
	s := New(cfg, nil, nil, nil)

	for i := 0; i < 5; i++ {
		s.pushSample(SentinelSample{ActiveNonIdle: 5})
	}

	// 50 active on 64-core: exceeds 3σ but 50 < softAbsMin(96) → no trigger
	sample := SentinelSample{ActiveNonIdle: 50}
	if trigger := s.detectAnomaly(sample); trigger != nil {
		t.Errorf("50 active on 64-core: should NOT trigger (below softAbsMin 96), got metric=%s", trigger.Metric)
	}

	// 100 active on 64-core: exceeds both → trigger
	sample2 := SentinelSample{ActiveNonIdle: 100}
	if trigger := s.detectAnomaly(sample2); trigger == nil {
		t.Error("100 active on 64-core: should trigger (above softAbsMin 96)")
	}
}

func TestSoftAbsoluteMin_Values(t *testing.T) {
	hw0 := HardwareProfile{CPUCores: 0} // unknown
	hw8 := HardwareProfile{CPUCores: 8}
	hw64 := HardwareProfile{CPUCores: 64}

	tests := []struct {
		metric MetricName
		hw     HardwareProfile
		want   float64
	}{
		// Unknown CPU → fixed minimums
		{MetricActive, hw0, 8},
		{MetricCPU, hw0, 4},
		{MetricIO, hw0, 4},
		{MetricLock, hw0, 3},
		{MetricLongSQL, hw0, 2},
		{MetricRedoRate, hw0, 5000},
		{MetricHardParse, hw0, 20},

		// 8 cores
		{MetricActive, hw8, 12},   // 8*1.5=12
		{MetricCPU, hw8, 6},       // 8*0.75=6
		{MetricIO, hw8, 4},        // 8*0.5=4, min 4
		{MetricHardParse, hw8, 24}, // 8*3=24

		// 64 cores
		{MetricActive, hw64, 96},    // 64*1.5=96
		{MetricCPU, hw64, 48},       // 64*0.75=48
		{MetricIO, hw64, 32},        // 64*0.5=32
		{MetricHardParse, hw64, 192}, // 64*3=192
	}

	for _, tt := range tests {
		got := SoftAbsoluteMin(tt.metric, tt.hw)
		if got != tt.want {
			t.Errorf("SoftAbsoluteMin(%s, cpu=%d) = %v, want %v",
				tt.metric, tt.hw.CPUCores, got, tt.want)
		}
	}
}

func TestHardCeiling_IsTwiceSoft(t *testing.T) {
	hw := HardwareProfile{CPUCores: 8}
	metrics := []MetricName{
		MetricActive, MetricCPU, MetricIO,
		MetricLock, MetricLongSQL, MetricRedoRate, MetricHardParse,
	}
	for _, m := range metrics {
		soft := SoftAbsoluteMin(m, hw)
		hard := HardCeiling(m, hw)
		if hard != 2*soft {
			t.Errorf("HardCeiling(%s) = %v, want 2 × SoftAbsoluteMin(%v) = %v",
				m, hard, soft, 2*soft)
		}
	}
}
