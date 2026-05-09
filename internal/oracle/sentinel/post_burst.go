/*-------------------------------------------------------------------------
 *
 * post_burst.go
 *	  EnrichReport collects Block G (space details) and Block H (param
 *	  details) after the burst ends. Called once per burst, not during
 *	  the high-frequency collection window, so performance is not
 *	  critical.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/post_burst.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"fmt"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// EnrichReport collects Block G (space details) and Block H (param details)
// after the burst ends. Called once per burst, not during the high-frequency
// collection window, so performance is not critical.
func EnrichReport(ctx context.Context, driver db.Driver, report *BurstReport) {
	if driver == nil || report == nil {
		return
	}

	metric := MetricName(report.TriggerEvent.Metric)

	// Use a short timeout so post-burst enrichment doesn't block the pipeline.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Block G: space details (storage scenarios).
	report.SpaceDetails = collectSpaceDetails(ctx, driver, metric)

	// Block H: related parameters.
	report.ParamDetails = collectParamDetails(ctx, driver, metric)
}

// ──────────────────────────────────────────────────────────────────
// Block G — Space Details
// ──────────────────────────────────────────────────────────────────

// scenarioSpaceQueries maps trigger metrics to their space detail query.
var scenarioSpaceQueries = map[MetricName]string{
	MetricTablespaceUsedPct: `SELECT tablespace_name,
		ROUND(used_space * 8192 / 1048576, 1) AS used_mb,
		ROUND(tablespace_size * 8192 / 1048576, 1) AS total_mb,
		ROUND(used_percent, 1) AS used_pct
		FROM dba_tablespace_usage_metrics
		ORDER BY used_percent DESC
		FETCH FIRST 10 ROWS ONLY`,

	MetricTempUsedPct: `SELECT NVL(ts.tablespace_name, 'TEMP') AS name,
		ROUND(NVL(th.used_mb, 0), 1) AS used_mb,
		ROUND(NVL(th.total_mb, 0), 1) AS total_mb,
		ROUND(NVL(th.used_mb / NULLIF(th.total_mb, 0) * 100, 0), 1) AS used_pct
		FROM (SELECT tablespace_name,
		      SUM(bytes_used)/1048576 AS used_mb,
		      SUM(bytes_free + bytes_used)/1048576 AS total_mb
		      FROM v$temp_space_header
		      GROUP BY tablespace_name) th
		RIGHT JOIN (SELECT DISTINCT tablespace_name FROM dba_temp_files) ts
		  ON th.tablespace_name = ts.tablespace_name`,

	MetricUndoUsedPct: `SELECT tablespace_name AS name,
		ROUND(used_space * 8192 / 1048576, 1) AS used_mb,
		ROUND(tablespace_size * 8192 / 1048576, 1) AS total_mb,
		ROUND(used_percent, 1) AS used_pct
		FROM dba_tablespace_usage_metrics
		WHERE tablespace_name IN (SELECT value FROM v$parameter WHERE name = 'undo_tablespace')`,

	MetricFRAUsedPct: `SELECT file_type AS name,
		ROUND(percent_space_used, 1) AS used_mb,
		100 AS total_mb,
		ROUND(percent_space_used, 1) AS used_pct
		FROM v$flash_recovery_area_usage
		WHERE percent_space_used > 0
		ORDER BY percent_space_used DESC`,

	MetricASMUsedPct: `SELECT name,
		ROUND((total_mb - free_mb), 1) AS used_mb,
		ROUND(total_mb, 1) AS total_mb,
		ROUND((total_mb - free_mb) / NULLIF(total_mb, 0) * 100, 1) AS used_pct
		FROM v$asm_diskgroup
		ORDER BY name`,
}

func collectSpaceDetails(ctx context.Context, driver db.Driver, metric MetricName) []SpaceDetail {
	sql, ok := scenarioSpaceQueries[metric]
	if !ok {
		return nil
	}

	rows, err := driver.Query(ctx, sql)
	if err != nil || len(rows.Rows) == 0 {
		return nil
	}

	details := make([]SpaceDetail, 0, len(rows.Rows))
	for _, row := range rows.Rows {
		if len(row) < 4 {
			continue
		}
		d := SpaceDetail{
			Name:    pbStr(row[0]),
			UsedMB:  pbFloat(row[1]),
			TotalMB: pbFloat(row[2]),
			UsedPct: pbFloat(row[3]),
		}
		// For FRA, the "used_mb" column is actually percent, remap for display.
		if metric == MetricFRAUsedPct {
			d.Extra = fmt.Sprintf("%.1f%%", d.UsedPct)
			d.UsedMB = 0
			d.TotalMB = 0
		}
		details = append(details, d)
	}
	return details
}

// ──────────────────────────────────────────────────────────────────
// Block H — Related Parameters
// ──────────────────────────────────────────────────────────────────

// scenarioParams maps trigger metrics to the parameter names we should fetch.
var scenarioParams = map[MetricName][]string{
	// Memory/Cache
	MetricBufferCacheHit:    {"db_cache_size", "sga_target", "memory_target"},
	MetricLibraryCacheHit:   {"shared_pool_size", "sga_target", "memory_target"},
	MetricPGAUsedPct:        {"pga_aggregate_target", "memory_target"},
	MetricSharedPoolFreePct: {"shared_pool_size", "sga_target", "memory_target"},

	// Wait/Latency
	MetricLogFileSyncUs: {"log_buffer", "db_writer_processes"},

	// Redo/Archive
	MetricLogSwitchRate:    {"log_buffer"},
	MetricRedoLogSpaceWait: {"log_buffer"},
	MetricCheckpointNotComplete: {"log_buffer"},
}

func collectParamDetails(ctx context.Context, driver db.Driver, metric MetricName) []ParamDetail {
	paramNames, ok := scenarioParams[metric]
	if !ok {
		return nil
	}

	details := make([]ParamDetail, 0, len(paramNames))
	for _, name := range paramNames {
		val := queryParam(ctx, driver, name)
		if val != "" {
			details = append(details, ParamDetail{Name: name, Value: val})
		}
	}

	// For redo-related scenarios, also add redo log group info.
	if metric == MetricLogFileSyncUs || metric == MetricLogSwitchRate ||
		metric == MetricRedoLogSpaceWait || metric == MetricCheckpointNotComplete {
		redoInfo := queryRedoLogInfo(ctx, driver)
		details = append(details, redoInfo...)
	}

	if len(details) == 0 {
		return nil
	}
	return details
}

func queryParam(ctx context.Context, driver db.Driver, name string) string {
	rows, err := driver.Query(ctx,
		fmt.Sprintf("SELECT value FROM v$parameter WHERE name = '%s'", name))
	if err != nil || len(rows.Rows) == 0 {
		return ""
	}
	return pbStr(rows.Rows[0][0])
}

func queryRedoLogInfo(ctx context.Context, driver db.Driver) []ParamDetail {
	rows, err := driver.Query(ctx,
		`SELECT COUNT(*) AS groups,
		        ROUND(MIN(bytes)/1048576) AS min_mb,
		        ROUND(MAX(bytes)/1048576) AS max_mb
		 FROM v$log`)
	if err != nil || len(rows.Rows) == 0 {
		return nil
	}
	row := rows.Rows[0]
	if len(row) < 3 {
		return nil
	}
	return []ParamDetail{
		{Name: "redo_log_groups", Value: pbStr(row[0])},
		{Name: "redo_log_size_mb", Value: fmt.Sprintf("%s~%s", pbStr(row[1]), pbStr(row[2]))},
	}
}

// ──────────────────────────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────────────────────────

func pbStr(v interface{}) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}

func pbFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		var f float64
		fmt.Sscanf(fmt.Sprintf("%v", v), "%f", &f)
		return f
	}
}
