/*-------------------------------------------------------------------------
 *
 * slow_probe.go
 *	  SlowProbeCollector collects slow-tier metrics every 30 seconds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/slow_probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// SlowProbeCollector collects slow-tier metrics every 30 seconds.
type SlowProbeCollector struct {
	driver db.Driver

	// Previous values for delta calculation.
	prevArchiveFail int64
	prevCheckpoints int64
	slowInit        bool
}

// NewSlowProbeCollector creates a new slow-tier probe collector.
func NewSlowProbeCollector(driver db.Driver) *SlowProbeCollector {
	return &SlowProbeCollector{driver: driver}
}

// SQL queries for slow-tier metrics.
const (
	// Cache hit ratio from pg_stat_database.
	slowCacheHitSQL = `SELECT
  CASE WHEN (blks_hit + blks_read) = 0 THEN 100.0
       ELSE blks_hit * 100.0 / (blks_hit + blks_read)
  END AS cache_hit_pct
FROM pg_stat_database
WHERE datname = current_database()`

	// Dead tuple ratio: worst table by dead_tup_ratio.
	slowDeadTupleSQL = `SELECT
  COALESCE(MAX(
    CASE WHEN n_live_tup + n_dead_tup > 0
         THEN n_dead_tup * 100.0 / (n_live_tup + n_dead_tup)
         ELSE 0
    END
  ), 0) AS max_dead_ratio
FROM pg_stat_user_tables
WHERE n_live_tup + n_dead_tup > 1000`

	// XID age as percentage of max (2^31 - 1 = 2147483647).
	slowXIDAgeSQL = `SELECT
  COALESCE(MAX(age(datfrozenxid)) * 100.0 / 2147483647, 0) AS xid_age_pct
FROM pg_database
WHERE datallowconn`

	// Connection usage percentage.
	slowConnectionsSQL = `SELECT
  COUNT(*) * 100.0 / NULLIF(current_setting('max_connections')::int, 0) AS conn_pct
FROM pg_stat_activity`

	// Replication lag in seconds (returns 0 if no replication).
	slowReplicationLagSQL = `SELECT
  COALESCE(MAX(EXTRACT(EPOCH FROM replay_lag)), 0) AS lag_sec
FROM pg_stat_replication`

	// Checkpoint stats: requested checkpoints since last reset.
	slowCheckpointSQL = `SELECT checkpoints_req FROM pg_stat_bgwriter`

	// Blocker count from pg_stat_activity (kept for slow-tier fallback).
	slowBlockerSQL = `SELECT
  COUNT(DISTINCT blocking.pid) AS blocker_count
FROM pg_stat_activity AS blocked
JOIN pg_stat_activity AS blocking ON blocking.pid = ANY(pg_blocking_pids(blocked.pid))
WHERE blocked.pid != blocked.pid OR TRUE`

	// ── New slow-tier queries ──

	// Autovacuum worker count.
	slowAutovacuumSQL = `SELECT COUNT(*) FROM pg_stat_activity
WHERE backend_type = 'autovacuum worker'`

	// Oldest non-idle transaction age in seconds.
	slowOldestXactSQL = `SELECT COALESCE(
  MAX(EXTRACT(EPOCH FROM clock_timestamp() - xact_start)), 0)
FROM pg_stat_activity
WHERE state != 'idle' AND xact_start IS NOT NULL
  AND backend_type = 'client backend'`

	// Archive fail count.
	slowArchiveFailSQL = `SELECT failed_count FROM pg_stat_archiver`

	// pg_is_in_recovery (1.0 = standby/recovery, 0.0 = primary).
	slowRecoverySQL = `SELECT CASE WHEN pg_is_in_recovery() THEN 1.0 ELSE 0.0 END`

	// Backend errors: conflicts from pg_stat_database.
	slowBackendErrorSQL = `SELECT COALESCE(SUM(conflicts), 0) FROM pg_stat_database`

	// Table bloat estimate (worst table with > 10000 rows).
	slowTableBloatSQL = `SELECT COALESCE(MAX(
    CASE WHEN n_live_tup + n_dead_tup > 10000
         THEN n_dead_tup * 100.0 / NULLIF(n_live_tup + n_dead_tup, 0)
         ELSE 0
    END
  ), 0) FROM pg_stat_user_tables`

	// Database size as tablespace usage estimate.
	slowTablespaceSQL = `SELECT
  COALESCE(pg_database_size(current_database()) * 100.0 /
    NULLIF((SELECT setting::bigint * 1024 FROM pg_settings WHERE name = 'effective_cache_size'), 0),
  0)`
)

// Probe runs all slow-tier SQL queries and returns metric values.
func (s *SlowProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	s.collectCacheHit(ctx, metrics)
	s.collectDeadTuple(ctx, metrics)
	s.collectXIDAge(ctx, metrics)
	s.collectConnections(ctx, metrics)
	s.collectReplicationLag(ctx, metrics)
	s.collectCheckpoint(ctx, metrics)
	s.collectAutovacuum(ctx, metrics)
	s.collectOldestXact(ctx, metrics)
	s.collectArchiveFail(ctx, metrics)
	s.collectRecovery(ctx, metrics)
	s.collectBackendErrors(ctx, metrics)
	s.collectTableBloat(ctx, metrics)
	s.collectTablespace(ctx, metrics)

	return metrics
}

func (s *SlowProbeCollector) collectCacheHit(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowCacheHitSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricCacheHitPct] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectDeadTuple(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowDeadTupleSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricDeadTupleRatio] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectXIDAge(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowXIDAgeSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricXIDAgeRatio] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectConnections(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowConnectionsSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricConnectionsPct] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectReplicationLag(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowReplicationLagSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricReplicationLag] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectCheckpoint(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowCheckpointSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		cur := toInt64(rows.Rows[0][0])
		if s.slowInit {
			delta := cur - s.prevCheckpoints
			if delta > 0 {
				metrics[MetricCheckpointsReq] = float64(delta)
			}
		}
		s.prevCheckpoints = cur
	}
}

func (s *SlowProbeCollector) collectAutovacuum(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowAutovacuumSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricAutovacuumWorkers] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectOldestXact(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowOldestXactSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricOldestXactAgeSec] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectArchiveFail(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowArchiveFailSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		cur := toInt64(rows.Rows[0][0])
		if s.slowInit {
			delta := cur - s.prevArchiveFail
			if delta > 0 {
				metrics[MetricArchiveFailCnt] = float64(delta)
			}
		}
		s.prevArchiveFail = cur
	}
	s.slowInit = true
}

func (s *SlowProbeCollector) collectRecovery(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowRecoverySQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricPGIsInRecovery] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectBackendErrors(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowBackendErrorSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricBackendErrorRate] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectTableBloat(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowTableBloatSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricTableBloatPct] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectTablespace(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowTablespaceSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricTablespaceUsedPct] = toFloat64(rows.Rows[0][0])
	}
}
