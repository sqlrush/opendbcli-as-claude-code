/*-------------------------------------------------------------------------
 *
 * delta.go
 *	  RawSample holds raw cumulative values from a single OpenGauss
 *	  query.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/monitor/dbtop/delta.go
 *
 *-------------------------------------------------------------------------
 */
package dbtop

import "time"

// RawSample holds raw cumulative values from a single OpenGauss query.
type RawSample struct {
	Commits   int64     // pg_stat_database xact_commit
	Rollbacks int64     // pg_stat_database xact_rollback
	Queries   int64     // pg_stat_database tup_returned+fetched+inserted+updated+deleted
	WALBytes  int64     // pg_current_xlog_location() delta (0 if unavailable)
	Timestamp time.Time
}

// DeltaResult holds computed per-second rates.
type DeltaResult struct {
	TPS    float64
	QPS    float64
	WALKBs float64
}

// ComputeDeltas calculates delta-based metrics from a raw sample.
// First call stores baseline and returns zero values.
// Subsequent calls compute per-second rates.
func ComputeDeltas(state *DeltaState, raw RawSample) DeltaResult {
	if !state.Initialized {
		savePrev(state, raw, 0)
		state.Initialized = true
		return DeltaResult{}
	}

	elapsed := raw.Timestamp.Sub(state.PrevTimestamp).Seconds()
	if elapsed <= 0 {
		return DeltaResult{}
	}

	commitsDelta := float64(raw.Commits - state.PrevCommits)
	rollbacksDelta := float64(raw.Rollbacks - state.PrevRollbacks)
	queriesDelta := float64(raw.Queries - state.PrevQueries)
	walDelta := float64(raw.WALBytes - state.PrevWALBytes)

	// Guard against counter resets (e.g. pg_stat_reset).
	if commitsDelta < 0 {
		commitsDelta = 0
	}
	if rollbacksDelta < 0 {
		rollbacksDelta = 0
	}
	if queriesDelta < 0 {
		queriesDelta = 0
	}
	if walDelta < 0 {
		walDelta = 0
	}

	result := DeltaResult{
		TPS:    (commitsDelta + rollbacksDelta) / elapsed,
		QPS:    queriesDelta / elapsed,
		WALKBs: walDelta / 1024.0 / elapsed,
	}

	savePrev(state, raw, result.TPS)

	return result
}

// savePrev updates the DeltaState with the current sample values.
func savePrev(state *DeltaState, raw RawSample, tps float64) {
	state.PrevCommits = raw.Commits
	state.PrevRollbacks = raw.Rollbacks
	state.PrevQueries = raw.Queries
	state.PrevWALBytes = raw.WALBytes
	state.PrevTPS = tps
	state.PrevTimestamp = raw.Timestamp
}
