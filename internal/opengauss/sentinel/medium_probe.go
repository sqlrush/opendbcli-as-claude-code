/*-------------------------------------------------------------------------
 *
 * medium_probe.go
 *	  MediumProbeCollector collects medium-tier metrics every 10
 *	  seconds. Queries pg_stat_database for throughput deltas and
 *	  pg_current_xlog_location() for WAL rate.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/medium_probe.go
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
// Queries pg_stat_database for throughput deltas and pg_current_xlog_location() for WAL rate.
type MediumProbeCollector struct {
	driver db.Driver

	// Previous cumulative values for delta calculation.
	prevXactCommit int64
	prevXactRollbk int64
	prevTempBytes  int64
	prevDeadlocks  int64
	prevTime       time.Time
	initialized    bool

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
  SUM(deadlocks) AS total_deadlocks
FROM pg_stat_database
WHERE datname = current_database()`

// OpenGauss: use pg_current_xlog_location() delta for WAL generation rate.
const mediumWALSQL = `SELECT pg_xlog_location_diff(pg_current_xlog_location(), '0/0')::bigint`

// Probe runs medium-tier queries and returns metric values.
func (m *MediumProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	now := nowFunc()
	m.collectDBStats(ctx, metrics, now)
	m.collectWAL(ctx, metrics, now)

	return metrics
}

// collectDBStats queries pg_stat_database for delta-based throughput metrics.
func (m *MediumProbeCollector) collectDBStats(ctx context.Context, metrics map[MetricName]float64, now time.Time) {
	rows, err := m.driver.Query(ctx, mediumDBStatsSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 4 {
		return
	}

	row := rows.Rows[0]
	curCommit := toInt64(row[0])
	curRollbk := toInt64(row[1])
	curTempBytes := toInt64(row[2])
	curDeadlocks := toInt64(row[3])

	if m.initialized {
		elapsed := now.Sub(m.prevTime).Seconds()
		if elapsed > 0 {
			commitDelta := curCommit - m.prevXactCommit
			rollbkDelta := curRollbk - m.prevXactRollbk
			metrics[MetricXactCommitRate] = float64(commitDelta+rollbkDelta) / elapsed
			metrics[MetricTempBytesRate] = float64(curTempBytes-m.prevTempBytes) / elapsed

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
	m.prevTime = now
	m.initialized = true
}

// collectWAL uses pg_current_xlog_location() delta for WAL generation rate.
func (m *MediumProbeCollector) collectWAL(ctx context.Context, metrics map[MetricName]float64, now time.Time) {
	rows, err := m.driver.Query(ctx, mediumWALSQL)
	if err != nil {
		// pg_xlog_location_diff may not be available; skip gracefully.
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
