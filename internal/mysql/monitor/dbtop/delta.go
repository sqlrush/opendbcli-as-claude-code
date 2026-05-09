/*-------------------------------------------------------------------------
 *
 * delta.go
 *	  RawSample holds raw cumulative values from a single MySQL query.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/monitor/dbtop/delta.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "time"

// RawSample holds raw cumulative values from a single MySQL query.
type RawSample struct {
	Commits   int64     // Handler_commit (includes autocommit implicit commits)
	Rollbacks int64     // Handler_rollback
	Queries   int64     // Queries
	RedoBytes int64     // Innodb_os_log_written (bytes)
	Timestamp time.Time
}

// DeltaResult holds computed per-second rates.
type DeltaResult struct {
	HasDelta bool
	TPS      float64
	QPS      float64
	RedoKBs  float64
}

// ComputeDeltas calculates delta-based metrics from a raw sample.
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
	queriesDelta := float64(raw.Queries - state.PrevQueries)
	redoDelta := float64(raw.RedoBytes - state.PrevRedoBytes)

	result := DeltaResult{
		HasDelta: true,
		TPS:      (commitsDelta + rollbacksDelta) / elapsed,
		QPS:      queriesDelta / elapsed,
		RedoKBs:  redoDelta / 1024.0 / elapsed,
	}

	savePrev(state, raw, result.TPS)
	return result
}

func savePrev(state *DeltaState, raw RawSample, tps float64) {
	state.PrevCommits = raw.Commits
	state.PrevRollbacks = raw.Rollbacks
	state.PrevQueries = raw.Queries
	state.PrevRedoBytes = raw.RedoBytes
	state.PrevTPS = tps
	state.PrevTimestamp = raw.Timestamp
}
