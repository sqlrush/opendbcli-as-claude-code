/*-------------------------------------------------------------------------
 *
 * medium_probe.go
 *	  MediumProbeCollector collects medium-tier metrics every 10
 *	  seconds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/medium_probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// MediumProbeCollector collects medium-tier metrics every 10 seconds.
type MediumProbeCollector struct {
	driver db.Driver

	// Previous cumulative values for delta calculation.
	prevXactCommit  int64
	prevXactRollbk  int64
	prevTempBytes   int64
	prevDeadlocks   int64
	prevTupReturned int64
	prevTupFetched  int64
	prevTempFiles   int64
	prevTime        time.Time
	initialized     bool

	// Previous WAL values for delta.
	prevWALBytes int64
	walInit      bool
}

// NewMediumProbeCollector creates a new medium-tier probe collector.
func NewMediumProbeCollector(driver db.Driver) *MediumProbeCollector {
	return &MediumProbeCollector{driver: driver}
}

// pg_stat_database query for throughput counters.
const mediumDBStatsSQL = `SELECT
  SUM(xact_commit) AS total_commit,
  SUM(xact_rollback) AS total_rollback,
  SUM(temp_bytes) AS total_temp_bytes,
  SUM(deadlocks) AS total_deadlocks,
  SUM(tup_returned) AS total_tup_returned,
  SUM(tup_fetched) AS total_tup_fetched,
  SUM(temp_files) AS total_temp_files
FROM pg_stat_database
WHERE datname = current_database()`

// pg_stat_wal query for WAL generation rate (PG 14+).
const mediumWALSQL = `SELECT wal_bytes FROM pg_stat_wal`

// Session wait type breakdown (medium-tier, shares pg_stat_activity).
const mediumWaitSessionsSQL = `SELECT
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'LWLock') AS lwlock_wait,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'BufferPin') AS bufferpin_wait,
  COUNT(DISTINCT blocking.pid) AS blocker_count,
  COUNT(*) FILTER (WHERE mode = 'RowExclusiveLock') AS row_exclusive_cnt
FROM pg_stat_activity
LEFT JOIN LATERAL (
  SELECT pid FROM unnest(pg_blocking_pids(pg_stat_activity.pid)) AS pid LIMIT 1
) blocking ON TRUE
LEFT JOIN pg_locks ON pg_locks.pid = pg_stat_activity.pid
WHERE pg_stat_activity.backend_type = 'client backend'`

// Simpler blocker/lock queries (separate for reliability).
const mediumBlockerSQL = `SELECT
  COUNT(DISTINCT blocking.pid) AS blocker_count
FROM pg_stat_activity AS blocked
JOIN pg_stat_activity AS blocking ON blocking.pid = ANY(pg_blocking_pids(blocked.pid))
WHERE blocked.pid != blocked.pid OR TRUE`

const mediumLWLockSQL = `SELECT
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'LWLock') AS lwlock_wait,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'BufferPin') AS bufferpin_wait
FROM pg_stat_activity WHERE backend_type = 'client backend'`

const mediumRowExclusiveSQL = `SELECT COUNT(*) FROM pg_locks WHERE mode = 'RowExclusiveLock' AND granted`

// Probe runs medium-tier queries and returns metric values.
func (m *MediumProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	now := nowFunc()
	m.collectDBStats(ctx, metrics, now)
	m.collectWAL(ctx, metrics, now)
	m.collectWaitSessions(ctx, metrics)
	m.collectBlockers(ctx, metrics)
	m.collectRowExclusiveLocks(ctx, metrics)

	return metrics
}

// collectDBStats queries pg_stat_database for delta-based throughput metrics.
func (m *MediumProbeCollector) collectDBStats(ctx context.Context, metrics map[MetricName]float64, now time.Time) {
	rows, err := m.driver.Query(ctx, mediumDBStatsSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 7 {
		return
	}

	row := rows.Rows[0]
	curCommit := toInt64(row[0])
	curRollbk := toInt64(row[1])
	curTempBytes := toInt64(row[2])
	curDeadlocks := toInt64(row[3])
	curTupReturned := toInt64(row[4])
	curTupFetched := toInt64(row[5])
	curTempFiles := toInt64(row[6])

	if m.initialized {
		elapsed := now.Sub(m.prevTime).Seconds()
		if elapsed > 0 {
			metrics[MetricXactCommitRate] = float64(curCommit-m.prevXactCommit) / elapsed
			metrics[MetricXactRollbackRate] = float64(curRollbk-m.prevXactRollbk) / elapsed
			metrics[MetricTempBytesRate] = float64(curTempBytes-m.prevTempBytes) / elapsed
			metrics[MetricTupReturnedRate] = float64(curTupReturned-m.prevTupReturned) / elapsed
			metrics[MetricTupFetchedRate] = float64(curTupFetched-m.prevTupFetched) / elapsed
			metrics[MetricTempFilesRate] = float64(curTempFiles-m.prevTempFiles) / elapsed

			deadlockDelta := curDeadlocks - m.prevDeadlocks
			if deadlockDelta > 0 {
				metrics[MetricDeadlocks] = float64(deadlockDelta)
			}
		}
	}

	m.prevXactCommit = curCommit
	m.prevXactRollbk = curRollbk
	m.prevTempBytes = curTempBytes
	m.prevDeadlocks = curDeadlocks
	m.prevTupReturned = curTupReturned
	m.prevTupFetched = curTupFetched
	m.prevTempFiles = curTempFiles
	m.prevTime = now
	m.initialized = true
}

// collectWAL queries pg_stat_wal for WAL generation rate.
func (m *MediumProbeCollector) collectWAL(ctx context.Context, metrics map[MetricName]float64, now time.Time) {
	rows, err := m.driver.Query(ctx, mediumWALSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	curWALBytes := toInt64(rows.Rows[0][0])

	if m.walInit {
		elapsed := now.Sub(m.prevTime).Seconds()
		if elapsed > 0 {
			metrics[MetricWALBytesRate] = float64(curWALBytes-m.prevWALBytes) / elapsed
		}
	}

	m.prevWALBytes = curWALBytes
	m.walInit = true
}

// collectWaitSessions queries session wait type breakdown.
func (m *MediumProbeCollector) collectWaitSessions(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumLWLockSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 2 {
		metrics[MetricLWLockWaitSessions] = toFloat64(rows.Rows[0][0])
		metrics[MetricBufferPinWaitSessions] = toFloat64(rows.Rows[0][1])
	}
}

// collectBlockers queries blocking relationship count.
func (m *MediumProbeCollector) collectBlockers(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumBlockerSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 {
		metrics[MetricBlockerCount] = toFloat64(rows.Rows[0][0])
	}
}

// collectRowExclusiveLocks queries pg_locks for row exclusive lock count.
func (m *MediumProbeCollector) collectRowExclusiveLocks(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumRowExclusiveSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 {
		metrics[MetricRowExclusiveLocks] = toFloat64(rows.Rows[0][0])
	}
}
