/*-------------------------------------------------------------------------
 *
 * slow_probe.go
 *	  SlowProbeCollector collects slow-tier metrics every 30 seconds.
 *	  These queries are heavier and involve aggregate views.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/slow_probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

// SlowProbeCollector collects slow-tier metrics every 30 seconds.
// These queries are heavier and involve aggregate views.
type SlowProbeCollector struct {
	driver db.Driver
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
	slowCheckpointSQL = `SELECT
  checkpoints_req
FROM pg_stat_bgwriter`

	// Blocker count using pg_locks self-join (OpenGauss lacks pg_blocking_pids).
	slowBlockerSQL = `SELECT
  COUNT(DISTINCT kl.pid) AS blocker_count
FROM pg_locks bl
JOIN pg_locks kl ON kl.transactionid = bl.transactionid AND kl.pid != bl.pid
WHERE NOT bl.granted`
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
	s.collectBlockers(ctx, metrics)

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
		metrics[MetricCheckpointsReq] = toFloat64(rows.Rows[0][0])
	}
}

func (s *SlowProbeCollector) collectBlockers(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowBlockerSQL)
	if err != nil {
		// pg_locks join may fail; skip gracefully.
		return
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		metrics[MetricBlockerCount] = toFloat64(rows.Rows[0][0])
	}
}
