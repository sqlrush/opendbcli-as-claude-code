/*-------------------------------------------------------------------------
 *
 * json_signal_map.go
 *	  MapJSONSignalType converts a JSON signal type and key to an
 *	  internal SignalType.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/json_signal_map.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// ─── Signal Type Mapping ──────────────────────────────────────────────────
//
// Maps JSON signal types to internal SignalType values.
// OpenGauss uses pg_stat_* views, same as PostgreSQL.

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
func MapJSONSignalType(jsonType, key string) SignalType {
	// Priority 1: Known sentinel metrics
	if isKnownMetric(key) {
		return SignalMetric
	}

	// Priority 2: Explicit type mapping
	lower := strings.ToLower(jsonType)
	if st, ok := signalTypeMap[lower]; ok {
		return st
	}

	// Priority 3: Fallback to keyword
	return SignalKeyword
}

// isKnownMetric checks if a key matches one of the OpenGauss sentinel metric names.
var knownMetrics = map[string]bool{
	"active_sessions":    true,
	"idle_in_transaction": true,
	"lock_waits":         true,
	"long_queries":       true,
	"xact_commit_rate":   true,
	"cache_hit_pct":      true,
	"dead_tuple_ratio":   true,
	"xid_age_pct":        true,
	"replication_lag_sec": true,
	"temp_bytes_rate":    true,
	"checkpoints_req":    true,
	"wal_bytes_rate":     true,
	"connections_pct":    true,
	"blocker_count":      true,
	"deadlocks":          true,
}

func isKnownMetric(key string) bool {
	return knownMetrics[strings.ToLower(key)]
}

// ─── Trigger Source Mapping ────────────────────────────────────────────────

// MapTriggerSource converts a JSON trigger condition source to an internal source string.
// OpenGauss uses pg_stat_* views. We map everything to "metrics" or "summary".
func MapTriggerSource(jsonSource string) string {
	lower := strings.ToLower(jsonSource)

	// Direct mappings
	switch lower {
	case "metrics":
		return "metrics"
	case "summary":
		return "summary"
	case "wait_profile":
		return "metrics" // OpenGauss has no WaitProfile in BurstReport
	case "blocking_chains":
		return "metrics" // OpenGauss has no BlockingChains in BurstReport
	case "top_sqls":
		return "metrics" // OpenGauss has no TopSQLs in BurstReport
	case "space_details":
		return "metrics" // OpenGauss has no SpaceDetails in BurstReport
	case "filesystem", "os_metric", "os_config":
		return "metrics"
	}

	// PostgreSQL view mappings → metrics
	switch {
	case strings.Contains(lower, "pg_stat"),
		strings.Contains(lower, "pg_catalog"),
		strings.Contains(lower, "pg_settings"),
		strings.Contains(lower, "pg_class"),
		strings.Contains(lower, "pg_index"),
		strings.Contains(lower, "pg_replication"),
		strings.Contains(lower, "pg_wal"),
		strings.Contains(lower, "pg_locks"),
		strings.Contains(lower, "pg_database"),
		strings.Contains(lower, "pg_tablespace"),
		strings.Contains(lower, "information_schema"),
		strings.Contains(lower, "performance_schema"),
		strings.Contains(lower, "system"):
		return "metrics"
	}

	// Sources that can't be evaluated from burst data
	switch lower {
	case "alert_log", "trace_file", "error_log",
		"user_request", "requirement", "operation", "schedule",
		"project_planning", "migration_planning",
		"architecture_review", "config":
		return "metrics"
	}

	return ""
}

// ─── Category Mapping ──────────────────────────────────────────────────────

// MapJSONCategory converts a JSON rule category to an internal engine category.
func MapJSONCategory(category, subcategory string) string {
	lower := strings.ToLower(category)
	subLower := strings.ToLower(subcategory)

	switch {
	case subLower == "locking" || subLower == "lock":
		return "lock"
	case subLower == "memory":
		return "memory"
	case subLower == "io" || subLower == "storage":
		return "io_storage"
	case subLower == "wal" || subLower == "archivelog":
		return "wal"
	case subLower == "vacuum" || subLower == "autovacuum":
		return "vacuum"
	case subLower == "sql" || subLower == "sql_tuning":
		return "sql_perf"
	case subLower == "session" || subLower == "connection":
		return "connection"
	case subLower == "tablespace" || subLower == "space":
		return "space"
	case subLower == "replication":
		return "replication"
	case subLower == "checkpoint":
		return "checkpoint"
	}

	switch {
	case lower == "performance":
		return "sql_perf"
	case lower == "availability" || lower == "ha":
		return "replication"
	case lower == "configuration" || lower == "operations":
		return "operations"
	case lower == "security":
		return "security"
	default:
		return lower
	}
}
