/*-------------------------------------------------------------------------
 *
 * medium_probe.go
 *	  MediumProbeCollector runs the 10-second tier probe.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/medium_probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// mediumProbeSQL fetches cumulative counters for delta calculations.
const mediumProbeSQL = `SELECT VARIABLE_NAME, VARIABLE_VALUE
FROM performance_schema.global_status
WHERE VARIABLE_NAME IN (
  'Com_commit', 'Com_rollback', 'Queries', 'Innodb_os_log_written',
  'Slow_queries', 'Table_locks_waited', 'Select_full_join',
  'Innodb_row_lock_waits', 'Innodb_deadlocks',
  'Innodb_row_lock_time', 'Innodb_lock_wait_timeout'
)`

// MediumProbeCollector runs the 10-second tier probe.
type MediumProbeCollector struct {
	driver db.Driver

	prevCommit        int64
	prevRollback      int64
	prevQueries       int64
	prevLogWritten    int64
	prevSlowQueries   int64
	prevTableLocks    int64
	prevSelectFullJoin int64
	prevRowLockWaits  int64
	prevDeadlocks     int64
	prevRowLockTime   int64
	prevLockTimeout   int64
	prevTime          time.Time
	initialized       bool
}

// NewMediumProbeCollector creates a medium-tier probe collector.
func NewMediumProbeCollector(driver db.Driver) *MediumProbeCollector {
	return &MediumProbeCollector{driver: driver}
}

// Probe executes the medium probe and returns computed delta values.
func (m *MediumProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	result, err := m.driver.Query(ctx, mediumProbeSQL)
	if err != nil {
		return nil
	}

	values := make(map[string]int64)
	for _, row := range result.Rows {
		if len(row) < 2 {
			continue
		}
		values[toString(row[0])] = int64(toFloat64(row[1]))
	}

	now := nowFunc()
	commit := values["Com_commit"]
	rollback := values["Com_rollback"]
	queries := values["Queries"]
	logWritten := values["Innodb_os_log_written"]
	slowQueries := values["Slow_queries"]
	tableLocks := values["Table_locks_waited"]
	selectFullJoin := values["Select_full_join"]
	rowLockWaits := values["Innodb_row_lock_waits"]
	deadlocks := values["Innodb_deadlocks"]
	rowLockTime := values["Innodb_row_lock_time"]
	lockTimeout := values["Innodb_lock_wait_timeout"]

	if !m.initialized {
		m.prevCommit = commit
		m.prevRollback = rollback
		m.prevQueries = queries
		m.prevLogWritten = logWritten
		m.prevSlowQueries = slowQueries
		m.prevTableLocks = tableLocks
		m.prevSelectFullJoin = selectFullJoin
		m.prevRowLockWaits = rowLockWaits
		m.prevDeadlocks = deadlocks
		m.prevRowLockTime = rowLockTime
		m.prevLockTimeout = lockTimeout
		m.prevTime = now
		m.initialized = true
		return nil
	}

	elapsed := now.Sub(m.prevTime).Seconds()
	if elapsed <= 0 {
		return nil
	}

	metrics := make(map[MetricName]float64)

	// TPS / QPS / Redo rate.
	metrics[MetricTPS] = float64((commit+rollback)-(m.prevCommit+m.prevRollback)) / elapsed
	metrics[MetricQPS] = float64(queries-m.prevQueries) / elapsed
	metrics[MetricRedoRate] = float64(logWritten-m.prevLogWritten) / 1024 / elapsed

	// New medium-tier metrics.
	if d := slowQueries - m.prevSlowQueries; d > 0 {
		metrics[MetricSlowQueriesRate] = float64(d) / elapsed
	}
	if d := tableLocks - m.prevTableLocks; d > 0 {
		metrics[MetricTableLocksWaitedRate] = float64(d) / elapsed
	}
	if d := selectFullJoin - m.prevSelectFullJoin; d > 0 {
		metrics[MetricSelectFullJoinRate] = float64(d) / elapsed
	}

	// Row lock waits (moved from slow to medium).
	if d := rowLockWaits - m.prevRowLockWaits; d > 0 {
		metrics[MetricRowLockWaits] = float64(d) / elapsed
	}

	// Deadlocks (moved from slow to medium).
	if d := deadlocks - m.prevDeadlocks; d > 0 {
		metrics[MetricDeadlocks] = float64(d) / elapsed
	}

	// Average row lock time (ms).
	rlwDelta := rowLockWaits - m.prevRowLockWaits
	rltDelta := rowLockTime - m.prevRowLockTime
	if rlwDelta > 0 {
		metrics[MetricAvgRowLockTimeMs] = float64(rltDelta) / float64(rlwDelta)
	}

	// Lock wait timeout count.
	if d := lockTimeout - m.prevLockTimeout; d > 0 {
		metrics[MetricInnoDBLockWaitTimeout] = float64(d)
	}

	m.prevCommit = commit
	m.prevRollback = rollback
	m.prevQueries = queries
	m.prevLogWritten = logWritten
	m.prevSlowQueries = slowQueries
	m.prevTableLocks = tableLocks
	m.prevSelectFullJoin = selectFullJoin
	m.prevRowLockWaits = rowLockWaits
	m.prevDeadlocks = deadlocks
	m.prevRowLockTime = rowLockTime
	m.prevLockTimeout = lockTimeout
	m.prevTime = now

	return metrics
}
