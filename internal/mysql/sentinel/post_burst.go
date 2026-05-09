/*-------------------------------------------------------------------------
 *
 * post_burst.go
 *	  EnrichReport adds space details and MySQL parameters to a burst
 *	  report.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/post_burst.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
)

// EnrichReport adds space details and MySQL parameters to a burst report.
func EnrichReport(ctx context.Context, driver db.Driver, report *BurstReport) {
	if driver == nil || report == nil {
		return
	}
	metric := MetricName(report.TriggerEvent.Metric)
	report.SpaceDetails = collectSpaceDetails(ctx, driver, metric)
	report.ParamDetails = collectParamDetails(ctx, driver, metric)
	report.ResourceLimits = collectResourceLimits(ctx, driver)
}

func collectSpaceDetails(ctx context.Context, driver db.Driver, metric MetricName) []SpaceDetail {
	switch metric {
	case MetricTablespaceUsedPct, MetricInnoDBDataFileGrowth:
		return queryTableSizes(ctx, driver)
	case MetricHistoryList, MetricInnoDBLogWaitsRate:
		return queryInnoDBStatus(ctx, driver)
	case MetricConnectionsPct:
		return queryConnectionsByUser(ctx, driver)
	default:
		return nil
	}
}

func queryTableSizes(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT TABLE_SCHEMA, TABLE_NAME,
  ROUND((DATA_LENGTH + INDEX_LENGTH) / 1024 / 1024, 1) AS size_mb,
  ROUND(DATA_FREE / 1024 / 1024, 1) AS free_mb
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('information_schema','performance_schema','mysql','sys')
ORDER BY DATA_LENGTH + INDEX_LENGTH DESC LIMIT 10`

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
			Name:   fmt.Sprintf("%s.%s", toString(row[0]), toString(row[1])),
			UsedMB: toFloat64(row[2]),
			Extra:  fmt.Sprintf("free=%.1fMB", toFloat64(row[3])),
		})
	}
	return details
}

func queryInnoDBStatus(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT name, count FROM information_schema.INNODB_METRICS
WHERE name IN ('trx_rseg_history_len', 'log_lsn_current', 'log_lsn_last_flush')`
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
			Extra: fmt.Sprintf("value=%.0f", toFloat64(row[1])),
		})
	}
	return details
}

func queryConnectionsByUser(ctx context.Context, driver db.Driver) []SpaceDetail {
	const sql = `SELECT IFNULL(USER, '<internal>') AS user, COUNT(*) AS cnt
FROM information_schema.PROCESSLIST GROUP BY USER ORDER BY cnt DESC LIMIT 10`
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

// scenarioParams maps trigger metrics to relevant MySQL parameters.
var scenarioParams = map[MetricName][]string{
	MetricBufferPoolHit:            {"innodb_buffer_pool_size", "innodb_buffer_pool_instances", "innodb_old_blocks_time"},
	MetricBufferPoolDirtyPct:       {"innodb_buffer_pool_size", "innodb_max_dirty_pages_pct", "innodb_io_capacity"},
	MetricAdaptiveHashHitPct:       {"innodb_adaptive_hash_index"},
	MetricRedoRate:                 {"innodb_log_file_size", "innodb_log_buffer_size", "sync_binlog", "innodb_flush_log_at_trx_commit"},
	MetricInnoDBLogWaitsRate:       {"innodb_log_file_size", "innodb_log_buffer_size"},
	MetricHistoryList:              {"innodb_max_purge_lag", "innodb_undo_tablespaces", "innodb_purge_threads"},
	MetricLockWaits:                {"innodb_lock_wait_timeout", "innodb_deadlock_detect", "innodb_print_all_deadlocks"},
	MetricDeadlocks:                {"innodb_deadlock_detect", "innodb_lock_wait_timeout"},
	MetricConnectionsPct:           {"max_connections", "max_user_connections", "wait_timeout", "interactive_timeout"},
	MetricReplicationLag:           {"slave_parallel_workers", "slave_parallel_type", "relay_log_space_limit"},
	MetricTmpDiskPct:               {"tmp_table_size", "max_heap_table_size", "sort_buffer_size", "join_buffer_size"},
	MetricTablespaceUsedPct:        {"innodb_file_per_table", "innodb_data_file_path"},
	MetricInnoDBBufferPoolWaitFree: {"innodb_buffer_pool_size", "innodb_io_capacity", "innodb_lru_scan_depth"},
}

func collectParamDetails(ctx context.Context, driver db.Driver, metric MetricName) []ParamDetail {
	params, ok := scenarioParams[metric]
	if !ok {
		return nil
	}
	var details []ParamDetail
	for _, name := range params {
		val := queryMySQLParam(ctx, driver, name)
		if val != "" {
			details = append(details, ParamDetail{Name: name, Value: val})
		}
	}
	return details
}

func queryMySQLParam(ctx context.Context, driver db.Driver, name string) string {
	sql := fmt.Sprintf("SELECT @@%s", name)
	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return ""
	}
	if len(rows.Rows) > 0 && len(rows.Rows[0]) >= 1 && rows.Rows[0][0] != nil {
		return fmt.Sprintf("%v", rows.Rows[0][0])
	}
	return ""
}

func collectResourceLimits(ctx context.Context, driver db.Driver) []ResourceLimitEntry {
	const sql = `SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') AS cur,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_variables WHERE VARIABLE_NAME='max_connections') AS lim`
	rows, err := driver.Query(ctx, sql)
	if err != nil {
		return nil
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 2 {
		return nil
	}
	return []ResourceLimitEntry{{
		ResourceName:       "max_connections",
		CurrentUtilization: toFloat64(rows.Rows[0][0]),
		LimitValue:         toFloat64(rows.Rows[0][1]),
	}}
}
