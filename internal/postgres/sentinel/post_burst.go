/*-------------------------------------------------------------------------
 *
 * post_burst.go
 *	  EnrichReport adds space details and PG parameters to a burst
 *	  report based on the trigger scenario. Safe to call with nil driver
 *	  (no-op).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/post_burst.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
)

// EnrichReport adds space details and PG parameters to a burst report
// based on the trigger scenario. Safe to call with nil driver (no-op).
func EnrichReport(ctx context.Context, driver db.Driver, report *BurstReport) {
	if driver == nil || report == nil {
		return
	}

	metric := MetricName(report.TriggerEvent.Metric)

	// Space details based on scenario.
	report.SpaceDetails = collectSpaceDetails(ctx, driver, metric)

	// Related parameters based on scenario.
	report.ParamDetails = collectParamDetails(ctx, driver, metric)

	// Resource limits (connections).
	report.ResourceLimits = collectResourceLimits(ctx, driver)
}

// ── Space details ──

func collectSpaceDetails(ctx context.Context, driver db.Driver, metric MetricName) []SpaceDetail {
	switch metric {
	case MetricTablespaceUsedPct, MetricDeadTupleRatio, MetricTableBloatPct:
		return queryTableSizes(ctx, driver)
	case MetricXIDAgeRatio:
		return queryXIDAge(ctx, driver)
	case MetricTempBytesRate, MetricTempFilesRate:
		return queryTempUsage(ctx, driver)
	case MetricConnectionsPct:
		return queryConnectionDetails(ctx, driver)
	default:
		return nil
	}
}

func queryTableSizes(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT
  schemaname || '.' || relname AS name,
  pg_total_relation_size(relid) / 1024.0 / 1024.0 AS used_mb,
  n_dead_tup,
  CASE WHEN n_live_tup + n_dead_tup > 0
       THEN n_dead_tup * 100.0 / (n_live_tup + n_dead_tup)
       ELSE 0 END AS dead_pct
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(relid) DESC
FETCH FIRST 10 ROWS ONLY`

	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}

	var details []SpaceDetail
	for _, row := range rows.Rows {
		if len(row) < 4 {
			continue
		}
		details = append(details, SpaceDetail{
			Name:    toString(row[0]),
			UsedMB:  toFloat64(row[1]),
			UsedPct: toFloat64(row[3]),
			Extra:   fmt.Sprintf("dead_tup=%.0f", toFloat64(row[2])),
		})
	}
	return details
}

func queryXIDAge(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT
  datname AS name,
  age(datfrozenxid) AS xid_age,
  age(datfrozenxid) * 100.0 / 2147483647 AS pct
FROM pg_database
WHERE datallowconn
ORDER BY age(datfrozenxid) DESC`

	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}

	var details []SpaceDetail
	for _, row := range rows.Rows {
		if len(row) < 3 {
			continue
		}
		details = append(details, SpaceDetail{
			Name:    toString(row[0]),
			UsedPct: toFloat64(row[2]),
			Extra:   fmt.Sprintf("xid_age=%.0f", toFloat64(row[1])),
		})
	}
	return details
}

func queryTempUsage(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT
  datname AS name,
  temp_bytes / 1024.0 / 1024.0 AS temp_mb,
  temp_files
FROM pg_stat_database
WHERE datname = current_database()`

	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}

	var details []SpaceDetail
	for _, row := range rows.Rows {
		if len(row) < 3 {
			continue
		}
		details = append(details, SpaceDetail{
			Name:   toString(row[0]),
			UsedMB: toFloat64(row[1]),
			Extra:  fmt.Sprintf("temp_files=%.0f", toFloat64(row[2])),
		})
	}
	return details
}

func queryConnectionDetails(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT
  COALESCE(usename, '<backend>') AS name,
  COUNT(*) AS cnt
FROM pg_stat_activity
GROUP BY usename
ORDER BY cnt DESC
FETCH FIRST 10 ROWS ONLY`

	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}

	var details []SpaceDetail
	for _, row := range rows.Rows {
		if len(row) < 2 {
			continue
		}
		details = append(details, SpaceDetail{
			Name:  toString(row[0]),
			Extra: fmt.Sprintf("connections=%.0f", toFloat64(row[1])),
		})
	}
	return details
}

// ── Parameter details ──

// scenarioParams maps trigger metrics to relevant PG parameters.
var scenarioParams = map[MetricName][]string{
	// Memory scenarios.
	MetricCacheHitPct:     {"shared_buffers", "effective_cache_size", "work_mem", "maintenance_work_mem"},
	MetricSharedBufferPct: {"shared_buffers", "effective_cache_size"},
	MetricTempBytesRate:   {"work_mem", "hash_mem_multiplier", "temp_file_limit"},
	MetricTempFilesRate:   {"work_mem", "hash_mem_multiplier", "temp_file_limit"},

	// WAL scenarios.
	MetricWALBytesRate:   {"wal_level", "max_wal_size", "min_wal_size", "checkpoint_completion_target", "wal_buffers"},
	MetricCheckpointsReq: {"max_wal_size", "checkpoint_completion_target", "checkpoint_timeout"},
	MetricArchiveFailCnt: {"archive_mode", "archive_command", "archive_timeout"},

	// Vacuum scenarios.
	MetricDeadTupleRatio:    {"autovacuum", "autovacuum_max_workers", "autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor"},
	MetricXIDAgeRatio:       {"autovacuum_freeze_max_age", "vacuum_freeze_min_age", "autovacuum_max_workers"},
	MetricAutovacuumWorkers: {"autovacuum_max_workers", "autovacuum_naptime", "autovacuum_vacuum_cost_delay"},
	MetricTableBloatPct:     {"autovacuum", "autovacuum_vacuum_threshold", "autovacuum_vacuum_scale_factor"},

	// Connection scenarios.
	MetricConnectionsPct: {"max_connections", "superuser_reserved_connections", "idle_in_transaction_session_timeout"},

	// Replication scenarios.
	MetricReplicationLag: {"max_wal_senders", "wal_keep_size", "hot_standby_feedback", "max_replication_slots"},

	// Lock scenarios.
	MetricLockWaitSessions: {"lock_timeout", "deadlock_timeout", "idle_in_transaction_session_timeout"},
	MetricDeadlocks:        {"deadlock_timeout", "lock_timeout"},
}

func collectParamDetails(ctx context.Context, driver db.Driver, metric MetricName) []ParamDetail {
	params, ok := scenarioParams[metric]
	if !ok {
		return nil
	}

	var details []ParamDetail
	for _, name := range params {
		val := queryPGParam(ctx, driver, name)
		if val != "" {
			details = append(details, ParamDetail{Name: name, Value: val})
		}
	}
	return details
}

func queryPGParam(ctx context.Context, driver db.Driver, name string) string {
	sql := fmt.Sprintf("SELECT current_setting('%s', true)", name)
	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return ""
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		return toString(rows.Rows[0][0])
	}
	return ""
}

// ── Resource limits ──

func collectResourceLimits(ctx context.Context, driver db.Driver) []ResourceLimitEntry {
	const sql = `SELECT
  'max_connections' AS resource_name,
  (SELECT COUNT(*) FROM pg_stat_activity)::float AS current_util,
  current_setting('max_connections')::float AS limit_val`

	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}

	var limits []ResourceLimitEntry
	for _, row := range rows.Rows {
		if len(row) < 3 {
			continue
		}
		limits = append(limits, ResourceLimitEntry{
			ResourceName:       toString(row[0]),
			CurrentUtilization: toFloat64(row[1]),
			LimitValue:         toFloat64(row[2]),
		})
	}
	return limits
}
