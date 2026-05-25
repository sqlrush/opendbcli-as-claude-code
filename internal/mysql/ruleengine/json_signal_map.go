/*-------------------------------------------------------------------------
 *
 * json_signal_map.go
 *	  MapJSONSignalType converts a JSON signal type and key to an
 *	  internal SignalType. Priority: 1. If key is a numeric MySQL error
 *	  code → SignalErrorCode 2. If key matches a known sentinel metric
 *	  → SignalMetric 3. If type is in signalTypeMap → mapped type 4.
 *	  Fallback → SignalKeyword
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/json_signal_map.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// ─── Signal Type Mapping ──────────────────────────────────────────────────
//
// Maps JSON signal types to internal SignalType values for MySQL.

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
//  1. If key is a numeric MySQL error code → SignalErrorCode
//  2. If key matches a known sentinel metric → SignalMetric
//  3. If type is in signalTypeMap → mapped type
//  4. Fallback → SignalKeyword
func MapJSONSignalType(jsonType, key string) SignalType {
	// Priority 1: MySQL error codes (numeric or "ERROR XXXX")
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, "ERROR ") || isNumericErrorCode(key) {
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

// isNumericErrorCode checks if a key looks like a MySQL error code (all digits).
func isNumericErrorCode(key string) bool {
	if len(key) == 0 || len(key) > 5 {
		return false
	}
	for _, c := range key {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isKnownMetric checks if a key matches one of the MySQL sentinel metric names.
var knownMetrics = map[string]bool{
	// Session/Thread metrics
	"active_sessions":   true,
	"threads_running":   true,
	"threads_connected": true,
	"connections_pct":   true,

	// Lock metrics
	"lock_waits":            true,
	"innodb_row_lock_waits": true,
	"deadlocks":             true,

	// Query metrics
	"long_queries": true,
	"qps":          true,
	"tps":          true,

	// InnoDB metrics
	"buffer_pool_hit_pct":  true,
	"history_list_length":  true,
	"redo_rate":            true,
	"tmp_disk_tables_pct":  true,

	// Replication metrics
	"replication_lag": true,

	// Extended MySQL metrics (from JSON rules)
	"innodb_buffer_pool_pages_dirty":    true,
	"innodb_buffer_pool_wait_free":      true,
	"innodb_log_waits":                  true,
	"innodb_data_reads":                 true,
	"innodb_data_writes":                true,
	"innodb_os_log_written":             true,
	"innodb_rows_read":                  true,
	"innodb_rows_inserted":              true,
	"innodb_rows_updated":               true,
	"innodb_rows_deleted":               true,
	"select_full_join":                  true,
	"select_scan":                       true,
	"sort_merge_passes":                 true,
	"created_tmp_disk_tables":           true,
	"slow_queries":                      true,
	"table_locks_waited":                true,
	"open_tables":                       true,
	"opened_tables":                     true,
	"table_open_cache_misses":           true,
	"aborted_connects":                  true,
	"aborted_clients":                   true,
	"com_select":                        true,
	"com_insert":                        true,
	"com_update":                        true,
	"com_delete":                        true,
	"bytes_received":                    true,
	"bytes_sent":                        true,
	"handler_read_rnd_next":             true,
	"handler_read_first":                true,
}

func isKnownMetric(key string) bool {
	return knownMetrics[strings.ToLower(key)]
}

// ─── Trigger Source Mapping ────────────────────────────────────────────────

// MapTriggerSource converts a JSON trigger condition source to an internal source string.
// JSON rules use MySQL source names like "performance_schema"; internal engine uses
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
	case "summary":
		return "summary"
	case "filesystem", "os_metric", "os_config":
		return "metrics"
	}

	// MySQL source mappings → metrics or wait_profile
	switch {
	case strings.Contains(lower, "performance_schema"),
		strings.Contains(lower, "information_schema"),
		strings.Contains(lower, "innodb_status"),
		strings.Contains(lower, "show_engine_innodb"),
		strings.Contains(lower, "processlist"),
		strings.Contains(lower, "global_status"),
		strings.Contains(lower, "global_variables"),
		strings.Contains(lower, "innodb_metrics"),
		strings.Contains(lower, "innodb_cmp"),
		strings.Contains(lower, "innodb_trx"),
		strings.Contains(lower, "innodb_locks"),
		strings.Contains(lower, "innodb_buffer"),
		strings.Contains(lower, "innodb_lock_waits"),
		strings.Contains(lower, "sys."),
		strings.Contains(lower, "sys_schema"):
		return "metrics"

	case strings.Contains(lower, "events_waits"),
		strings.Contains(lower, "events_statements"),
		strings.Contains(lower, "events_stages"):
		return "wait_profile"

	case strings.Contains(lower, "slow_log"),
		strings.Contains(lower, "general_log"):
		return "metrics"

	case lower == "status",
		strings.Contains(lower, "status_variable"),
		strings.Contains(lower, "system_variable"),
		strings.Contains(lower, "variable"):
		return "metrics"

	case strings.Contains(lower, "replication"),
		strings.Contains(lower, "slave_status"),
		strings.Contains(lower, "replica_status"),
		strings.Contains(lower, "show_slave"),
		strings.Contains(lower, "show_replica"):
		return "metrics"

	// Oracle view references in MySQL rules (cross-DB rule format compatibility)
	case strings.Contains(lower, "v$"),
		strings.Contains(lower, "gv$"),
		strings.Contains(lower, "dba_"):
		return "metrics"
	}

	// Sources that can't be evaluated from burst data
	switch lower {
	case "error_log", "slow_query_log", "general_log",
		"user_request", "requirement", "operation", "schedule",
		"project_planning", "migration_planning",
		"architecture_review", "config",
		"alert_log", "trace_file":
		return "metrics"
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
	case subLower == "memory" || subLower == "buffer_pool" || subLower == "innodb_buffer":
		return "memory"
	case subLower == "io" || subLower == "storage":
		return "io_storage"
	case subLower == "replication" || subLower == "binlog":
		return "replication"
	case subLower == "innodb" || subLower == "undo":
		return "innodb"
	case subLower == "sql" || subLower == "sql_tuning" || subLower == "query_optimization":
		return "sql_perf"
	case subLower == "session" || subLower == "connection":
		return "session"
	case subLower == "tablespace" || subLower == "space" || subLower == "disk":
		return "space"
	}

	switch {
	case lower == "performance":
		return "wait_event"
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
