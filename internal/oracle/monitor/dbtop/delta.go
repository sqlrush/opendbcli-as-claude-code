/*-------------------------------------------------------------------------
 *
 * delta.go
 *	  RawSample holds raw cumulative values from a single Oracle query.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/monitor/dbtop/delta.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "time"

// RawSample holds raw cumulative values from a single Oracle query.
type RawSample struct {
	CPUTime   int64     // v$sys_time_model DB CPU (microseconds)
	DBTime    int64     // v$sys_time_model DB time (microseconds)
	Commits   int64     // v$sysstat user commits
	Rollbacks int64     // v$sysstat user rollbacks
	Executes  int64     // v$sysstat execute count
	RedoSize  int64     // v$sysstat redo size (bytes)
	Timestamp time.Time
}

// DeltaResult holds computed per-second rates.
type DeltaResult struct {
	HasDelta   bool
	TPS        float64
	QPS        float64
	RedoKBs    float64
	DBPercent  float64
	WTRPercent float64
}

// ComputeDeltas calculates delta-based metrics from a raw sample.
// First call stores baseline and returns HasDelta=false.
// Subsequent calls compute per-second rates.
func ComputeDeltas(state *DeltaState, raw RawSample) DeltaResult {
	if !state.Initialized {
		savePrev(state, raw, 0)
		state.Initialized = true
		return DeltaResult{}
	}

	elapsed := raw.Timestamp.Sub(state.PrevTimestamp).Seconds()
	if elapsed <= 0 {
		return DeltaResult{HasDelta: true}
	}

	commitsDelta := float64(raw.Commits - state.PrevCommits)
	rollbacksDelta := float64(raw.Rollbacks - state.PrevRollbacks)
	executesDelta := float64(raw.Executes - state.PrevExecutes)
	redoDelta := float64(raw.RedoSize - state.PrevRedoSize)
	cpuDelta := float64(raw.CPUTime - state.PrevCPUTime)
	dbDelta := float64(raw.DBTime - state.PrevDBTime)

	result := DeltaResult{
		HasDelta: true,
		TPS:      (commitsDelta + rollbacksDelta) / elapsed,
		QPS:      executesDelta / elapsed,
		RedoKBs:  redoDelta / 1024.0 / elapsed,
	}

	wallMicro := elapsed * 1e6
	if wallMicro > 0 {
		result.DBPercent = cpuDelta / wallMicro * 100
	}
	if dbDelta > 0 {
		result.WTRPercent = (dbDelta - cpuDelta) / dbDelta * 100
	}

	savePrev(state, raw, result.TPS)

	return result
}

// savePrev updates the DeltaState with the current sample values.
func savePrev(state *DeltaState, raw RawSample, tps float64) {
	state.PrevCPUTime = raw.CPUTime
	state.PrevDBTime = raw.DBTime
	state.PrevCommits = raw.Commits
	state.PrevRollbacks = raw.Rollbacks
	state.PrevExecutes = raw.Executes
	state.PrevRedoSize = raw.RedoSize
	state.PrevTPS = tps
	state.PrevTimestamp = raw.Timestamp
}
