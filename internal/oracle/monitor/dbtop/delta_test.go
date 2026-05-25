/*-------------------------------------------------------------------------
 *
 * delta_test.go
 *	  Test cases for delta.go (dbtop package):
 *	  TestComputeDeltas_FirstFrame, TestComputeDeltas_SecondFrame,
 *	  TestComputeDeltas_DBPercent.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/delta_test.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import (
	"testing"
	"time"
)

func TestComputeDeltas_FirstFrame(t *testing.T) {
	state := &DeltaState{}
	now := time.Now()
	raw := RawSample{
		CPUTime: 1000000, DBTime: 2000000,
		Commits: 100, Rollbacks: 5, Executes: 500, RedoSize: 1024000,
		Timestamp: now,
	}
	result := ComputeDeltas(state, raw)
	if result.HasDelta {
		t.Error("first frame should not have delta")
	}
	if !state.Initialized {
		t.Error("state should be initialized after first frame")
	}
}

func TestComputeDeltas_SecondFrame(t *testing.T) {
	now := time.Now()
	state := &DeltaState{
		PrevCPUTime: 1000000, PrevDBTime: 2000000,
		PrevCommits: 100, PrevRollbacks: 5,
		PrevExecutes: 500, PrevRedoSize: 1024000,
		PrevTimestamp: now,
		Initialized:  true,
	}
	raw := RawSample{
		CPUTime: 1500000, DBTime: 3000000,
		Commits: 200, Rollbacks: 10, Executes: 1500, RedoSize: 2048000,
		Timestamp: now.Add(1 * time.Second),
	}
	result := ComputeDeltas(state, raw)
	if !result.HasDelta {
		t.Error("second frame should have delta")
	}
	// TPS = (200-100 + 10-5) / 1.0 = 105
	if result.TPS < 104 || result.TPS > 106 {
		t.Errorf("TPS = %.1f, want ~105", result.TPS)
	}
	// QPS = (1500-500) / 1.0 = 1000
	if result.QPS < 999 || result.QPS > 1001 {
		t.Errorf("QPS = %.1f, want ~1000", result.QPS)
	}
	// REDO = (2048000-1024000) / 1024 / 1.0 = 1000 kB/s
	if result.RedoKBs < 999 || result.RedoKBs > 1001 {
		t.Errorf("RedoKBs = %.1f, want ~1000", result.RedoKBs)
	}
}

func TestComputeDeltas_DBPercent(t *testing.T) {
	now := time.Now()
	state := &DeltaState{
		PrevCPUTime: 0, PrevDBTime: 0,
		PrevTimestamp: now, Initialized: true,
	}
	raw := RawSample{
		CPUTime:   500000,
		DBTime:    800000,
		Timestamp: now.Add(1 * time.Second),
	}
	result := ComputeDeltas(state, raw)
	// db% = 500000 / 1000000 * 100 = 50%
	if result.DBPercent < 49 || result.DBPercent > 51 {
		t.Errorf("DBPercent = %.1f, want ~50", result.DBPercent)
	}
	// WTR% = (800000-500000)/800000*100 = 37.5%
	if result.WTRPercent < 37 || result.WTRPercent > 38 {
		t.Errorf("WTRPercent = %.1f, want ~37.5", result.WTRPercent)
	}
}

func TestComputeDeltas_ZeroElapsed(t *testing.T) {
	now := time.Now()
	state := &DeltaState{PrevTimestamp: now, Initialized: true}
	raw := RawSample{Timestamp: now} // zero elapsed
	result := ComputeDeltas(state, raw)
	if !result.HasDelta {
		t.Error("should still have delta flag")
	}
	if result.TPS != 0 {
		t.Errorf("TPS should be 0 with zero elapsed, got %.1f", result.TPS)
	}
}
