/*-------------------------------------------------------------------------
 *
 * burst_test.go
 *	  Test cases for burst.go (sentinel package):
 *	  TestBurstController_CollectsFrames,
 *	  TestBurstController_EarlyExitOnCalm,
 *	  TestBurstController_ContextCancellation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/burst_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
)

func TestBurstController_CollectsFrames(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BurstInterval = 10 * time.Millisecond
	cfg.BurstDuration = 100 * time.Millisecond
	cfg.BurstCalmDelay = 1 * time.Hour // disable calm detection

	var seq int32
	probe := func(_ context.Context) dbtop.Snapshot {
		n := atomic.AddInt32(&seq, 1)
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: int(n) + 20, // stays above calm threshold
		}
	}

	trigger := TriggerEvent{
		Baseline:  10,
		Current:   30,
		Threshold: 25,
	}

	bc := NewBurstController(cfg, probe)
	result := bc.Run(context.Background(), trigger)

	if len(result.Frames) < 5 {
		t.Errorf("frames = %d, want >= 5 in 100ms at 10ms interval", len(result.Frames))
	}
	if result.PeakActive < 20 {
		t.Errorf("peak = %d, want >= 20", result.PeakActive)
	}
	if result.EndTime.IsZero() {
		t.Error("EndTime should be set")
	}
	if result.StartTime.IsZero() {
		t.Error("StartTime should be set")
	}
}

func TestBurstController_EarlyExitOnCalm(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BurstInterval = 10 * time.Millisecond
	cfg.BurstDuration = 5 * time.Second // long, but should exit early
	cfg.BurstCalmDelay = 30 * time.Millisecond

	var callCount int32
	probe := func(_ context.Context) dbtop.Snapshot {
		n := atomic.AddInt32(&callCount, 1)
		active := 50 // spike
		if n > 3 {
			active = 5 // calm down after 3 frames
		}
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: active,
		}
	}

	trigger := TriggerEvent{
		Baseline: 10,
		Current:  50,
	}

	bc := NewBurstController(cfg, probe)
	start := time.Now()
	result := bc.Run(context.Background(), trigger)
	elapsed := time.Since(start)

	// Should exit well before 5s
	if elapsed > 2*time.Second {
		t.Errorf("burst took %v, should have exited early on calm", elapsed)
	}
	if result.RecoverTime.IsZero() {
		t.Error("RecoverTime should be set on calm exit")
	}
}

func TestBurstController_ContextCancellation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BurstInterval = 10 * time.Millisecond
	cfg.BurstDuration = 10 * time.Second
	cfg.BurstCalmDelay = 1 * time.Hour

	probe := func(_ context.Context) dbtop.Snapshot {
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: 100,
		}
	}

	trigger := TriggerEvent{Baseline: 10, Current: 100}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	bc := NewBurstController(cfg, probe)
	start := time.Now()
	result := bc.Run(ctx, trigger)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Errorf("burst took %v, should exit on context cancel", elapsed)
	}
	if len(result.Frames) == 0 {
		t.Error("should have collected at least some frames before cancel")
	}
}

func TestBurstController_TracksPeakActive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BurstInterval = 10 * time.Millisecond
	cfg.BurstDuration = 80 * time.Millisecond
	cfg.BurstCalmDelay = 1 * time.Hour

	var callCount int32
	probe := func(_ context.Context) dbtop.Snapshot {
		n := atomic.AddInt32(&callCount, 1)
		// Peak at frame 3
		active := 20
		if n == 3 {
			active = 100
		}
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: active,
		}
	}

	trigger := TriggerEvent{Baseline: 10, Current: 50}
	bc := NewBurstController(cfg, probe)
	result := bc.Run(context.Background(), trigger)

	if result.PeakActive != 100 {
		t.Errorf("peak = %d, want 100", result.PeakActive)
	}
}

func TestIsCalm(t *testing.T) {
	cfg := DefaultConfig()
	bc := NewBurstController(cfg, nil)

	trigger := TriggerEvent{Baseline: 10}

	tests := []struct {
		active int
		calm   bool
	}{
		{5, true},   // well below 1.5x baseline
		{10, true},  // at baseline
		{12, true},  // at floor (baseline+2)
		{15, true},  // at 1.5x baseline (threshold=15, 15<=15)
		{16, false}, // above 1.5x baseline
		{20, false}, // well above threshold
		{50, false}, // spike
	}

	for _, tt := range tests {
		frame := BurstFrame{
			Snapshot: dbtop.Snapshot{ActiveCount: tt.active},
		}
		got := bc.isCalm(frame, trigger)
		if got != tt.calm {
			t.Errorf("isCalm(active=%d, baseline=10) = %v, want %v", tt.active, got, tt.calm)
		}
	}
}

func TestBurstController_FrameSequencing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.BurstInterval = 10 * time.Millisecond
	cfg.BurstDuration = 60 * time.Millisecond
	cfg.BurstCalmDelay = 1 * time.Hour

	probe := func(_ context.Context) dbtop.Snapshot {
		return dbtop.Snapshot{
			Timestamp:   time.Now(),
			ActiveCount: 50,
		}
	}

	trigger := TriggerEvent{Baseline: 10}
	bc := NewBurstController(cfg, probe)
	result := bc.Run(context.Background(), trigger)

	for i, f := range result.Frames {
		if f.Seq != i {
			t.Errorf("frame[%d].Seq = %d, want %d", i, f.Seq, i)
		}
	}
}
