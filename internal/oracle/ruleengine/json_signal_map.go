/*-------------------------------------------------------------------------
 *
 * json_signal_map.go
 *	  MapJSONSignalType converts a JSON signal type and key to an
 *	  internal SignalType. Priority: 1. If key starts with "ORA-" →
 *	  SignalErrorCode 2. If key matches a known sentinel metric →
 *	  SignalMetric 3. If type is in signalTypeMap → mapped type 4.
 *	  Fallback → SignalKeyword
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/json_signal_map.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// ─── Signal Type Mapping ──────────────────────────────────────────────────
//
// Maps 135+ JSON signal types to 5 internal SignalType values.

// signalTypeMap maps known JSON signal type strings to internal SignalType.
var signalTypeMap = map[string]SignalType{
	// Direct mappings
	"wait_event": SignalWaitEvent,
	"error":      SignalErrorCode,
	"alert":      SignalErrorCode,
	"metric":     SignalMetric,
	"os_metric":  SignalMetric,

	// Category mappings
	"performance":   SignalCategory,
	"availability":  SignalCategory,
	"capacity":      SignalCategory,
	"security":      SignalCategory,
	"configuration": SignalCategory,
}

// MapJSONSignalType converts a JSON signal type and key to an internal SignalType.
// Priority:
//  1. If key starts with "ORA-" → SignalErrorCode
//  2. If key matches a known sentinel metric → SignalMetric
//  3. If type is in signalTypeMap → mapped type
//  4. Fallback → SignalKeyword
func MapJSONSignalType(jsonType, key string) SignalType {
	// Priority 1: ORA error codes
	if strings.HasPrefix(strings.ToUpper(key), "ORA-") {
		return SignalErrorCode
	}

	// Priority 2: Known sentinel metrics
	if isKnownMetric(key) {
		return SignalMetric
	}

	// Priority 3: Explicit type mapping
	lower := strings.ToLower(jsonType)
	if st, ok := signalTypeMap[lower]; ok {
		return st
	}

	// Priority 4: Fallback to keyword
	return SignalKeyword
}

// isKnownMetric checks if a key matches one of the 48 sentinel metric names.
var knownMetrics = map[string]bool{
	"active_sessions":       true,
	"cpu_sessions":          true,
	"io_sessions":           true,
	"lock_sessions":         true,
	"long_sql_sessions":     true,
	"redo_rate":             true,
	"hard_parse":            true,
	"physical_read_rate":    true,
	"logical_read_rate":     true,
	"log_file_sync_avg":     true,
	"db_file_seq_read_avg":  true,
	"enqueue_wait_avg":      true,
	"wait_time_avg":         true,
	"deadlock_count":        true,
	"session_create_rate":   true,
	"commit_rate":           true,
	"buffer_cache_hit_pct":  true,
	"library_cache_hit_pct": true,
	"pga_used_pct":          true,
	"shared_pool_used_pct":  true,
	"tablespace_used_pct":   true,
	"temp_used_pct":         true,
	"undo_used_pct":         true,
	"fra_used_pct":          true,
	"log_switch_rate":       true,
	"archive_lag_sec":       true,
	"archive_error_count":   true,
	"redo_log_space_wait":   true,
	"blocking_chains":       true,
	"enqueue_deadlocks":     true,
	"latch_free_waits":      true,
	"mutex_waits":           true,
	"top_sql_drift":         true,
	"full_scan_rate":        true,
	"plan_change":           true,
	"sort_disk_rate":        true,
	"instance_status":       true,
	"dataguard_status":      true,
	"rac_interconnect":      true,
	"alert_log_errors":      true,
	"gc_cr_block_time":      true,
	"gc_current_block_time": true,
}

func isKnownMetric(key string) bool {
	return knownMetrics[strings.ToLower(key)]
}

// ─── Trigger Source Mapping ────────────────────────────────────────────────

// MapTriggerSource converts a JSON trigger condition source to an internal source string.
// JSON rules use Oracle view names like "v$sysstat"; internal engine uses
// abstract source names like "metrics".
func MapTriggerSource(jsonSource string) string {
	lower := strings.ToLower(jsonSource)

	// Direct mappings
	switch lower {
	case "wait_profile":
		return "wait_profile"
	case "metrics":
		return "metrics"
	case "blocking_chains":
		return "blocking_chains"
	case "top_sqls":
		return "top_sqls"
	case "space_details":
		return "space_details"
	case "summary":
		return "summary"
	case "filesystem", "os_metric", "os_config":
		return "metrics"
	}

	// Oracle view mappings → metrics or wait_profile
	switch {
	case strings.Contains(lower, "v$sysstat"),
		strings.Contains(lower, "v$sysmetric"),
		strings.Contains(lower, "v$sys_time_model"),
		strings.Contains(lower, "v$sgastat"),
		strings.Contains(lower, "v$pgastat"),
		strings.Contains(lower, "v$parameter"),
		strings.Contains(lower, "v$resource_limit"),
		strings.Contains(lower, "v$result_cache"),
		strings.Contains(lower, "v$sql"),
		strings.Contains(lower, "v$transaction"),
		strings.Contains(lower, "v$lock"),
		strings.Contains(lower, "v$latch"),
		strings.Contains(lower, "v$mutex"),
		strings.Contains(lower, "v$database"),
		strings.Contains(lower, "v$instance"),
		strings.Contains(lower, "v$datafile"),
		strings.Contains(lower, "v$log"),
		strings.Contains(lower, "v$archive"),
		strings.Contains(lower, "v$recovery"),
		strings.Contains(lower, "v$rman"),
		strings.Contains(lower, "v$block_change"),
		strings.Contains(lower, "v$rollstat"),
		strings.Contains(lower, "v$undostat"),
		strings.Contains(lower, "v$session"),
		strings.Contains(lower, "v$process"),
		strings.Contains(lower, "v$open_cursor"),
		strings.Contains(lower, "v$pdbs"),
		strings.Contains(lower, "gv$"):
		return "metrics"

	case strings.Contains(lower, "v$system_event"),
		strings.Contains(lower, "v$event_histogram"),
		strings.Contains(lower, "v$active_session"):
		return "wait_profile"

	case strings.Contains(lower, "dba_hist"),
		strings.Contains(lower, "dba_tab"),
		strings.Contains(lower, "dba_ind"),
		strings.Contains(lower, "dba_segment"),
		strings.Contains(lower, "dba_object"),
		strings.Contains(lower, "dba_tablespace"),
		strings.Contains(lower, "dba_data_file"),
		strings.Contains(lower, "dba_free_space"),
		strings.Contains(lower, "dba_advisor"),
		strings.Contains(lower, "dba_constraint"),
		strings.Contains(lower, "dba_scheduler"),
		strings.Contains(lower, "dba_registry"),
		strings.Contains(lower, "dba_feature"),
		strings.Contains(lower, "dba_audit"),
		strings.Contains(lower, "dba_user"),
		strings.Contains(lower, "dba_role"),
		strings.Contains(lower, "dba_profile"),
		strings.Contains(lower, "dba_source"):
		return "metrics"

	// Performance schema / system tables
	case strings.Contains(lower, "performance_schema"),
		strings.Contains(lower, "system"):
		return "metrics"
	}

	// Sources that can't be evaluated from burst data but shouldn't cause
	// the entire rule to be skipped — map to "metrics" as best-effort.
	switch lower {
	case "alert_log", "trace_file", "crs_log", "error_log",
		"user_request", "requirement", "operation", "schedule",
		"project_planning", "migration_planning",
		"architecture_review", "config":
		return "metrics" // best-effort: condition won't match but rule still loads
	}

	return "" // truly unknown source
}

// ─── Category Mapping ──────────────────────────────────────────────────────

// MapJSONCategory converts a JSON rule category to an internal engine category.
func MapJSONCategory(category, subcategory string) string {
	lower := strings.ToLower(category)
	subLower := strings.ToLower(subcategory)

	switch {
	case subLower == "locking" || subLower == "lock":
		return "lock"
	case subLower == "memory" || subLower == "sga" || subLower == "pga":
		return "memory"
	case subLower == "io" || subLower == "storage":
		return "io_storage"
	case subLower == "redo" || subLower == "archivelog":
		return "redo"
	case subLower == "undo":
		return "undo"
	case subLower == "sql" || subLower == "sql_tuning":
		return "sql_perf"
	case subLower == "session" || subLower == "connection":
		return "session"
	case subLower == "tablespace" || subLower == "space":
		return "space"
	}

	switch {
	case lower == "performance":
		return "wait_event" // broad performance maps to wait_event
	case lower == "availability" || lower == "ha":
		return "emergency"
	case lower == "configuration" || lower == "operations":
		return "operations"
	case lower == "security":
		return "security"
	default:
		return lower
	}
}
