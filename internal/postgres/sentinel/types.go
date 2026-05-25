/*-------------------------------------------------------------------------
 *
 * types.go
 *	  StrategyType identifies a detection algorithm.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/types.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "time"

// ──────────────────────────────────────────────────────────────────
// Strategy and trigger types
// ──────────────────────────────────────────────────────────────────

// StrategyType identifies a detection algorithm.
type StrategyType string

const (
	StrategyT1 StrategyType = "T1" // threshold: 3σ + soft abs + sustained
	StrategyT2 StrategyType = "T2" // hard ceiling: abs danger + sustained
	StrategyT3 StrategyType = "T3" // trend: regression slope + sustained windows
	StrategyT4 StrategyType = "T4" // acceleration: 2nd derivative > 0 + above soft abs
	StrategyT5 StrategyType = "T5" // compound: multi-metric AND
	StrategyT6 StrategyType = "T6" // capacity: usage > yellow/red thresholds
	StrategyT7 StrategyType = "T7" // shift: window mean comparison > 2σ
	StrategyT8 StrategyType = "T8" // regression: value drops below floor
	StrategyT9 StrategyType = "T9" // absence: rate drops > 80% from baseline
)

// TriggerMode controls how anomaly thresholds are calculated.
type TriggerMode string

const (
	TriggerAdaptive TriggerMode = "adaptive" // 3σ auto-threshold (default)
	TriggerFixed    TriggerMode = "fixed"    // fixed multiplier/absolute thresholds
)

// MetricThreshold defines the fixed-mode threshold for one metric.
type MetricThreshold struct {
	Enabled    bool    `json:"enabled"`
	Multiplier float64 `json:"multiplier"` // trigger when current > baseline * multiplier (0 = use absolute)
	Absolute   float64 `json:"absolute"`   // trigger when current > absolute value (0 = use multiplier)
}

// ──────────────────────────────────────────────────────────────────
// Hardware profile
// ──────────────────────────────────────────────────────────────────

// HardwareProfile holds host hardware capabilities.
// Used to scale absolute trigger thresholds dynamically.
type HardwareProfile struct {
	CPUCores       int     `json:"cpu_cores"`
	MemoryGB       float64 `json:"memory_gb"`
	MaxConnections int     `json:"max_connections"` // from pg_settings
}

// ──────────────────────────────────────────────────────────────────
// Metric names (33 metrics across 9 categories)
// ──────────────────────────────────────────────────────────────────

// MetricName identifies a PG sentinel probe metric.
type MetricName string

// Category 1: Session/Load (Fast, 1s).
const (
	MetricActiveSessions    MetricName = "active_sessions"
	MetricCPUSessions       MetricName = "cpu_sessions"
	MetricIOWaitSessions    MetricName = "io_wait_sessions"
	MetricLockWaitSessions  MetricName = "lock_wait_sessions"
	MetricLongQueries       MetricName = "long_queries"
	MetricIdleInTransaction MetricName = "idle_in_transaction"
	MetricConnectionsPct    MetricName = "connections_pct"
)

// Category 2: Throughput (Medium, 10s).
const (
	MetricXactCommitRate   MetricName = "xact_commit_rate"
	MetricXactRollbackRate MetricName = "xact_rollback_rate"
	MetricTupReturnedRate  MetricName = "tup_returned_rate"
	MetricTupFetchedRate   MetricName = "tup_fetched_rate"
	MetricTempBytesRate    MetricName = "temp_bytes_rate"
)

// Category 3: Wait/Latency (Medium, 10s).
const (
	MetricLWLockWaitSessions    MetricName = "lwlock_wait_sessions"
	MetricBufferPinWaitSessions MetricName = "bufferpin_wait_sessions"
	MetricAvgQueryTimeMs        MetricName = "avg_query_time_ms"
	MetricDeadlocks             MetricName = "deadlocks"
)

// Category 4: Memory/Cache (Slow, 30s).
const (
	MetricCacheHitPct     MetricName = "cache_hit_pct"
	MetricTempFilesRate   MetricName = "temp_files_rate"
	MetricSharedBufferPct MetricName = "shared_buffers_pct"
)

// Category 5: Storage/Capacity (Slow, 30s).
const (
	MetricTablespaceUsedPct MetricName = "tablespace_used_pct"
	MetricDeadTupleRatio    MetricName = "dead_tuple_ratio"
	MetricTableBloatPct     MetricName = "table_bloat_pct"
)

// Category 6: WAL/Archive (Slow, 30s).
const (
	MetricWALBytesRate    MetricName = "wal_bytes_rate"
	MetricCheckpointsReq  MetricName = "checkpoints_req"
	MetricArchiveFailCnt  MetricName = "archive_fail_count"
	MetricReplicationLag  MetricName = "replication_lag_sec"
)

// Category 7: Lock/Concurrency (Fast/Medium).
const (
	MetricBlockerCount      MetricName = "blocker_count"
	MetricRowExclusiveLocks MetricName = "row_exclusive_locks"
)

// Category 8: Vacuum/XID (Slow, 30s).
const (
	MetricXIDAgeRatio       MetricName = "xid_age_pct"
	MetricAutovacuumWorkers MetricName = "autovacuum_workers"
	MetricOldestXactAgeSec  MetricName = "oldest_xact_age_sec"
)

// Category 9: System/Pattern (Slow, 30s).
const (
	MetricPGIsInRecovery  MetricName = "pg_is_in_recovery"
	MetricBackendErrorRate MetricName = "backend_errors_rate"
)

// Backward-compatible aliases.
const (
	MetricLockWaits = MetricLockWaitSessions
)

// ──────────────────────────────────────────────────────────────────
// Root cause types (14 classifications)
// ──────────────────────────────────────────────────────────────────

// RootCauseType classifies the probable root cause of a PG anomaly.
type RootCauseType string

const (
	CauseSlowQuery        RootCauseType = "slow_query"
	CauseLockContention   RootCauseType = "lock_contention"
	CauseVacuumLag        RootCauseType = "vacuum_lag"
	CauseXIDWraparound    RootCauseType = "xid_wraparound"
	CauseWALBottleneck    RootCauseType = "wal_bottleneck"
	CauseConnectionStorm  RootCauseType = "connection_storm"
	CauseReplicationLag   RootCauseType = "replication_lag"
	CauseCheckpointStorm  RootCauseType = "checkpoint_storm"
	CauseIOSubsystem      RootCauseType = "io_subsystem"
	CauseMemoryPressure   RootCauseType = "memory_pressure"
	CauseTempSpill        RootCauseType = "temp_spill"
	CauseBloatPressure    RootCauseType = "bloat_pressure"
	CauseAutovacuumLag    RootCauseType = "autovacuum_lag"
	CauseDatabaseHang     RootCauseType = "database_hang"
	CauseUnknown          RootCauseType = "unknown"
)

// String returns the Chinese display name for the root cause type.
func (r RootCauseType) String() string {
	switch r {
	case CauseSlowQuery:
		return "慢SQL冲高"
	case CauseLockContention:
		return "锁等待阻塞"
	case CauseVacuumLag:
		return "Vacuum滞后"
	case CauseXIDWraparound:
		return "XID回卷风险"
	case CauseWALBottleneck:
		return "WAL冲高"
	case CauseConnectionStorm:
		return "连接数冲高"
	case CauseReplicationLag:
		return "复制延迟"
	case CauseCheckpointStorm:
		return "Checkpoint冲高"
	case CauseIOSubsystem:
		return "I/O冲高"
	case CauseMemoryPressure:
		return "内存冲高"
	case CauseTempSpill:
		return "临时空间冲高"
	case CauseBloatPressure:
		return "表膨胀冲高"
	case CauseAutovacuumLag:
		return "Autovacuum滞后"
	case CauseDatabaseHang:
		return "数据库Hang"
	default:
		return "未知"
	}
}

// IsValid returns true if the root cause type is a known value.
func (r RootCauseType) IsValid() bool {
	switch r {
	case CauseSlowQuery, CauseLockContention, CauseVacuumLag,
		CauseXIDWraparound, CauseWALBottleneck, CauseConnectionStorm,
		CauseReplicationLag, CauseCheckpointStorm, CauseIOSubsystem,
		CauseMemoryPressure, CauseTempSpill, CauseBloatPressure,
		CauseAutovacuumLag, CauseDatabaseHang, CauseUnknown:
		return true
	default:
		return false
	}
}

// ──────────────────────────────────────────────────────────────────
// Sample and baseline types
// ──────────────────────────────────────────────────────────────────

// MetricSample holds values from one probe cycle.
type MetricSample struct {
	Timestamp time.Time              `json:"timestamp"`
	Values    map[MetricName]float64 `json:"values"`
}

// MetricBaseline holds rolling stats for a single metric.
type MetricBaseline struct {
	Avg   float64 `json:"avg"`
	Std   float64 `json:"std"`
	Ready bool    `json:"ready"`
}

// Baseline holds rolling statistics for anomaly detection.
type Baseline struct {
	Samples []MetricSample                 `json:"-"`
	Window  int                            `json:"window"`
	Ready   bool                           `json:"ready"`
	Metrics map[MetricName]*MetricBaseline `json:"metrics"`
}

// ──────────────────────────────────────────────────────────────────
// Trigger and burst types
// ──────────────────────────────────────────────────────────────────

// TriggerEvent is emitted when an anomaly exceeds the threshold.
type TriggerEvent struct {
	Timestamp  time.Time    `json:"timestamp"`
	Metric     string       `json:"metric"`
	Baseline   float64      `json:"baseline"`
	Current    float64      `json:"current"`
	Threshold  float64      `json:"threshold"`
	Multiplier float64      `json:"multiplier"`
	Strategy   StrategyType `json:"strategy,omitempty"`
}

// SQLProfile represents a top SQL from pg_stat_activity.
type SQLProfile struct {
	QueryID       string  `json:"query_id"`
	Query         string  `json:"query"`
	Calls         int     `json:"calls"`
	MeanTimeSec   float64 `json:"mean_time_sec"`
	MaxTimeSec    float64 `json:"max_time_sec"`
	WaitEventType string  `json:"wait_event_type"`
	WaitEvent     string  `json:"wait_event"`
	ActiveCount   int     `json:"active_count"`
}

// WaitBucket represents one wait event in the wait profile.
type WaitBucket struct {
	WaitEventType string  `json:"wait_event_type"`
	WaitEvent     string  `json:"wait_event"`
	Count         int     `json:"count"`
	Percentage    float64 `json:"percentage"`
}

// BlockingChain represents a blocking relationship.
type BlockingChain struct {
	BlockerPID   int    `json:"blocker_pid"`
	BlockerUser  string `json:"blocker_user"`
	BlockerQuery string `json:"blocker_query"`
	VictimCount  int    `json:"victim_count"`
	WaitEvent    string `json:"wait_event"`
}

// SpaceDetail represents one space usage entry.
type SpaceDetail struct {
	Name    string  `json:"name"`
	UsedMB  float64 `json:"used_mb"`
	TotalMB float64 `json:"total_mb"`
	UsedPct float64 `json:"used_pct"`
	Extra   string  `json:"extra,omitempty"`
}

// ParamDetail represents a database parameter relevant to the scenario.
type ParamDetail struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ResourceLimitEntry represents a resource limit check (e.g., max_connections).
type ResourceLimitEntry struct {
	ResourceName       string  `json:"resource_name"`
	CurrentUtilization float64 `json:"current_utilization"`
	LimitValue         float64 `json:"limit_value"`
}

// ──────────────────────────────────────────────────────────────────
// Burst report
// ──────────────────────────────────────────────────────────────────

// BurstReport is the aggregated analysis of a burst collection.
type BurstReport struct {
	TriggerEvent   TriggerEvent             `json:"trigger_event"`
	DurationSec    float64                  `json:"duration_sec"`
	PeakActive     int                      `json:"peak_active"`
	BaselineActive float64                  `json:"baseline_active"`
	Metrics        map[string]MetricSummary `json:"metrics"`
	Classification Classification           `json:"classification"`
	Frames         []MetricSample           `json:"-"`
	StartTime      time.Time                `json:"start_time"`
	EndTime        time.Time                `json:"end_time"`
	TopSQLs        []SQLProfile             `json:"top_sqls,omitempty"`
	WaitProfile    []WaitBucket             `json:"wait_profile,omitempty"`
	BlockingChains []BlockingChain          `json:"blocking_chains,omitempty"`

	// Enrichment (post-burst).
	SpaceDetails   []SpaceDetail        `json:"space_details,omitempty"`
	ParamDetails   []ParamDetail        `json:"param_details,omitempty"`
	ResourceLimits []ResourceLimitEntry `json:"resource_limits,omitempty"`

	RawFrameCount int `json:"raw_frame_count"`
}

// MetricSummary holds aggregated stats for one metric across burst frames.
type MetricSummary struct {
	Avg   float64 `json:"avg"`
	Max   float64 `json:"max"`
	Min   float64 `json:"min"`
	Trend string  `json:"trend"`
}

// Classification is the output of rule-based root cause analysis.
type Classification struct {
	Cause      RootCauseType `json:"cause"`
	Confidence float64       `json:"confidence"`
	Evidence   []string      `json:"evidence"`
}

// ──────────────────────────────────────────────────────────────────
// Config
// ──────────────────────────────────────────────────────────────────

// Config controls PG sentinel behavior.
type Config struct {
	// Probe.
	PollInterval   time.Duration `json:"poll_interval"`
	BaselineWindow int           `json:"baseline_window"`
	MinSamples     int           `json:"min_samples"`

	// Trigger.
	TriggerMode    TriggerMode `json:"trigger_mode"`
	SigmaThreshold float64     `json:"sigma_threshold"`
	SustainedCount int         `json:"sustained_count"`

	// Fixed-mode thresholds (only used when TriggerMode = "fixed").
	FixedThresholds map[MetricName]MetricThreshold `json:"fixed_thresholds"`

	// Hardware-based absolute thresholds.
	Hardware HardwareProfile `json:"hardware"`

	// Burst.
	BurstInterval  time.Duration `json:"burst_interval"`
	BurstDuration  time.Duration `json:"burst_duration"`
	BurstCalmDelay time.Duration `json:"burst_calm_delay"`
	CooldownPeriod time.Duration `json:"cooldown_period"`
}

// DefaultConfig returns production default PG sentinel configuration.
func DefaultConfig() Config {
	return Config{
		PollInterval:   time.Second,
		BaselineWindow: 60,
		MinSamples:     10,
		TriggerMode:    TriggerAdaptive,
		SigmaThreshold: 3.0,
		SustainedCount: 3,
		FixedThresholds: map[MetricName]MetricThreshold{
			MetricActiveSessions:    {Enabled: true, Multiplier: 2.0},
			MetricCPUSessions:       {Enabled: true, Multiplier: 2.0},
			MetricIOWaitSessions:    {Enabled: true, Multiplier: 3.0},
			MetricLockWaitSessions:  {Enabled: true, Absolute: 5},
			MetricLongQueries:       {Enabled: true, Absolute: 3},
			MetricWALBytesRate:      {Enabled: true, Multiplier: 5.0},
			MetricIdleInTransaction: {Enabled: true, Multiplier: 3.0},
		},
		BurstInterval:  200 * time.Millisecond,
		BurstDuration:  30 * time.Second,
		BurstCalmDelay: 5 * time.Second,
		CooldownPeriod: 5 * time.Minute,
	}
}

// ──────────────────────────────────────────────────────────────────
// Compatibility: MetricLabel / MetricUnit (delegate to MetricDef)
// ──────────────────────────────────────────────────────────────────

// MetricLabel returns the Chinese display label for a metric.
// Delegates to GetMetricDef; falls back to the metric name string.
func MetricLabel(metric MetricName) string {
	if def := GetMetricDef(metric); def != nil {
		return def.Label
	}
	return string(metric)
}

// MetricUnit returns the display unit for a metric.
// Delegates to GetMetricDef; falls back to empty string.
func MetricUnit(metric MetricName) string {
	if def := GetMetricDef(metric); def != nil {
		return def.Unit
	}
	return ""
}
