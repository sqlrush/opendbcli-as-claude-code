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
 *	  internal/postgres/ruleengine/json_signal_map.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// ─── Signal Type Mapping ──────────────────────────────────────────────────
//
// Maps JSON signal types to internal SignalType values for PostgreSQL.

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
	// Priority 1: Known PG sentinel metrics
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

// isKnownMetric checks if a key matches one of the PG sentinel metric names.
var knownMetrics = map[string]bool{
	// PG sentinel metrics (from sentinel/types.go)
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

	// PG stat view derived metrics
	"active_connections":     true,
	"idle_in_transaction_count": true,
	"lock_count":             true,
	"cache_hit_ratio":        true,
	"tup_returned":           true,
	"tup_fetched":            true,
	"tup_inserted":           true,
	"tup_updated":            true,
	"tup_deleted":            true,
	"blks_read":              true,
	"blks_hit":               true,
	"xact_commit":            true,
	"xact_rollback":          true,
	"temp_files":             true,
	"temp_bytes":             true,
	"deadlock_count":         true,
	"seq_scan_ratio":         true,
	"idx_scan_ratio":         true,
	"vacuum_count":           true,
	"autovacuum_count":       true,
	"analyze_count":          true,
	"autoanalyze_count":      true,
	"n_dead_tup":             true,
	"n_live_tup":             true,
	"buffers_checkpoint":     true,
	"buffers_clean":          true,
	"buffers_backend":        true,
	"maxwritten_clean":       true,
	"wal_records":            true,
	"wal_bytes":              true,
}

func isKnownMetric(key string) bool {
	return knownMetrics[strings.ToLower(key)]
}

// ─── Category Mapping ──────────────────────────────────────────────────────

// MapJSONCategory converts a JSON rule category to an internal engine category.
func MapJSONCategory(category, subcategory string) string {
	lower := strings.ToLower(category)
	subLower := strings.ToLower(subcategory)

	switch {
	case subLower == "locking" || subLower == "lock" || subLower == "deadlock":
		return "lock"
	case subLower == "memory" || subLower == "shared_buffers" || subLower == "work_mem":
		return "memory"
	case subLower == "io" || subLower == "storage" || subLower == "disk":
		return "io_storage"
	case subLower == "wal" || subLower == "archive" || subLower == "archivelog":
		return "wal"
	case subLower == "vacuum" || subLower == "autovacuum" || subLower == "bloat":
		return "vacuum"
	case subLower == "sql" || subLower == "sql_tuning" || subLower == "query":
		return "sql_perf"
	case subLower == "session" || subLower == "connection" || subLower == "pool":
		return "connection"
	case subLower == "replication" || subLower == "standby" || subLower == "streaming":
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
	case lower == "maintenance":
		return "vacuum"
	default:
		return lower
	}
}
