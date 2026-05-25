/*-------------------------------------------------------------------------
 *
 * slow_probe.go
 *	  SlowProbeCollector runs the 30-second tier probe.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/slow_probe.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"

	"github.com/sqlrush/opendb/internal/db"
)

const (
	slowBufferPoolHitSQL = `SELECT
  (1 - (
    (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_reads') /
    NULLIF((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_read_requests'), 0)
  )) * 100 AS hit_pct`

	slowBufferPoolDirtySQL = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_dirty') /
  NULLIF((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_total'), 0) * 100`

	slowAdaptiveHashSQL = `SELECT
  CASE WHEN s.val + b.val = 0 THEN 100.0
       ELSE s.val * 100.0 / (s.val + b.val) END
FROM (SELECT count AS val FROM information_schema.INNODB_METRICS WHERE name='adaptive_hash_searches') s,
     (SELECT count AS val FROM information_schema.INNODB_METRICS WHERE name='adaptive_hash_searches_btree') b`

	slowHistoryListSQL = `SELECT count FROM information_schema.INNODB_METRICS WHERE name = 'trx_rseg_history_len'`

	slowConnectionsPctSQL = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') /
  (SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME='max_connections') * 100`

	slowReplicationLagSQL = `SELECT
  IFNULL((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Seconds_Behind_Master'), -1)`

	slowTmpDiskPctSQL = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_disk_tables') /
  NULLIF((SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Created_tmp_tables'), 0) * 100`

	slowTablespaceSQL = `SELECT COALESCE(
  SUM(DATA_LENGTH + INDEX_LENGTH) * 100.0 /
    NULLIF(SUM(DATA_FREE + DATA_LENGTH + INDEX_LENGTH), 0),
  0)
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('information_schema','performance_schema','mysql','sys')`

	slowLogWaitsSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_log_waits'`

	slowBPWaitFreeSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_wait_free'`

	slowDataWrittenSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_data_written'`

	slowRelayLogSQL = `SELECT CHANNEL_NAME, SERVICE_STATE FROM performance_schema.replication_applier_status LIMIT 1`

	slowUptimeSQL = `SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Uptime'`

	slowHandlerRndSQL = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Handler_read_rnd') +
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Handler_read_rnd_next')`
)

// SlowProbeCollector runs the 30-second tier probe.
type SlowProbeCollector struct {
	driver db.Driver

	prevLogWaits    int64
	prevBPWaitFree  int64
	prevDataWritten int64
	prevHandlerRnd  int64
	slowInit        bool
}

// NewSlowProbeCollector creates a slow-tier probe collector.
func NewSlowProbeCollector(driver db.Driver) *SlowProbeCollector {
	return &SlowProbeCollector{driver: driver}
}

// Probe executes the slow probe and returns metric values.
func (s *SlowProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	s.collectScalar(ctx, slowBufferPoolHitSQL, MetricBufferPoolHit, metrics, true)
	s.collectScalar(ctx, slowBufferPoolDirtySQL, MetricBufferPoolDirtyPct, metrics, false)
	s.collectScalar(ctx, slowAdaptiveHashSQL, MetricAdaptiveHashHitPct, metrics, false)
	s.collectScalar(ctx, slowHistoryListSQL, MetricHistoryList, metrics, false)
	s.collectScalar(ctx, slowConnectionsPctSQL, MetricConnectionsPct, metrics, false)
	s.collectScalar(ctx, slowTmpDiskPctSQL, MetricTmpDiskPct, metrics, false)
	s.collectScalar(ctx, slowTablespaceSQL, MetricTablespaceUsedPct, metrics, false)
	s.collectScalar(ctx, slowUptimeSQL, MetricUptimeSinceFlush, metrics, false)

	// Replication lag (skip if -1 = not a replica).
	if r, err := s.driver.Query(ctx, slowReplicationLagSQL); err == nil && len(r.Rows) > 0 {
		val := toFloat64(r.Rows[0][0])
		if val >= 0 {
			metrics[MetricReplicationLag] = val
		}
	}

	// Delta-based metrics.
	s.collectDelta(ctx, slowLogWaitsSQL, MetricInnoDBLogWaitsRate, metrics, &s.prevLogWaits)
	s.collectDelta(ctx, slowBPWaitFreeSQL, MetricInnoDBBufferPoolWaitFree, metrics, &s.prevBPWaitFree)
	s.collectDelta(ctx, slowDataWrittenSQL, MetricInnoDBDataFileGrowth, metrics, &s.prevDataWritten)
	s.collectDelta(ctx, slowHandlerRndSQL, MetricHandlerReadRndRate, metrics, &s.prevHandlerRnd)

	s.slowInit = true
	return metrics
}

func (s *SlowProbeCollector) collectScalar(ctx context.Context, sql string, metric MetricName, metrics map[MetricName]float64, requirePositive bool) {
	r, err := s.driver.Query(ctx, sql)
	if err != nil {
		return
	}
	if len(r.Rows) > 0 && len(r.Rows[0]) >= 1 {
		val := toFloat64(r.Rows[0][0])
		if !requirePositive || val > 0 {
			metrics[metric] = val
		}
	}
}

func (s *SlowProbeCollector) collectDelta(ctx context.Context, sql string, metric MetricName, metrics map[MetricName]float64, prev *int64) {
	r, err := s.driver.Query(ctx, sql)
	if err != nil {
		return
	}
	if len(r.Rows) == 0 || len(r.Rows[0]) < 1 {
		return
	}
	cur := int64(toFloat64(r.Rows[0][0]))
	if s.slowInit {
		delta := cur - *prev
		if delta > 0 {
			metrics[metric] = float64(delta) / 30.0 // 30s interval
		}
	}
	*prev = cur
}
