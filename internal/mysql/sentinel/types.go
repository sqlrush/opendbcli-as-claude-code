/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Package sentinel implements background anomaly detection for MySQL
 *	  (InnoDB).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/types.go
 *
 *-------------------------------------------------------------------------
 */
// Package sentinel implements background anomaly detection for MySQL (InnoDB).
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
	StrategyT4 StrategyType = "T4" // acceleration: 2nd derivative
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
	Multiplier float64 `json:"multiplier"`
	Absolute   float64 `json:"absolute"`
}

// ──────────────────────────────────────────────────────────────────
// Hardware profile
// ──────────────────────────────────────────────────────────────────

// HardwareProfile holds host hardware capabilities.
type HardwareProfile struct {
	CPUCores       int     `json:"cpu_cores"`
	MemoryGB       float64 `json:"memory_gb"`
	MaxConnections int     `json:"max_connections"`
}

// ──────────────────────────────────────────────────────────────────
// Metric names (~30 metrics across 9 categories)
// ──────────────────────────────────────────────────────────────────

// MetricName identifies a MySQL sentinel probe metric.
type MetricName string

// Category 1: Session/Load (Fast, 1s).
const (
	MetricThreadsRunning      MetricName = "threads_running"
	MetricThreadsConnected    MetricName = "threads_connected"
	MetricLockWaits           MetricName = "lock_waits"
	MetricLongQueries         MetricName = "long_queries"
	MetricThreadsCached       MetricName = "threads_cached"
	MetricAbortedConnectsRate MetricName = "aborted_connects_rate"
)

// Category 2: Throughput (Medium, 10s).
const (
	MetricTPS                  MetricName = "tps"
	MetricQPS                  MetricName = "qps"
	MetricRedoRate             MetricName = "redo_rate"
	MetricSlowQueriesRate      MetricName = "slow_queries_rate"
	MetricTableLocksWaitedRate MetricName = "table_locks_waited_rate"
	MetricSelectFullJoinRate   MetricName = "select_full_join_rate"
)

// Category 3: Wait/Latency (Medium, 10s).
const (
	MetricRowLockWaits            MetricName = "innodb_row_lock_waits"
	MetricDeadlocks               MetricName = "deadlocks"
	MetricInnoDBLockWaitTimeout   MetricName = "innodb_lock_wait_timeout_count"
	MetricAvgRowLockTimeMs        MetricName = "avg_row_lock_time_ms"
)

// Category 4: Memory/Cache (Slow, 30s).
const (
	MetricBufferPoolHit      MetricName = "buffer_pool_hit_pct"
	MetricBufferPoolDirtyPct MetricName = "buffer_pool_dirty_pct"
	MetricAdaptiveHashHitPct MetricName = "adaptive_hash_hit_pct"
)

// Category 5: Storage/Capacity (Slow, 30s).
const (
	MetricTablespaceUsedPct    MetricName = "tablespace_used_pct"
	MetricInnoDBDataFileGrowth MetricName = "innodb_data_file_growth_rate"
	MetricTmpDiskPct           MetricName = "tmp_disk_tables_pct"
)

// Category 6: InnoDB/Undo (Slow, 30s).
const (
	MetricHistoryList              MetricName = "history_list_length"
	MetricInnoDBLogWaitsRate       MetricName = "innodb_log_waits_rate"
	MetricInnoDBBufferPoolWaitFree MetricName = "innodb_buffer_pool_wait_free"
)

// Category 7: Replication (Slow, 30s).
const (
	MetricReplicationLag MetricName = "replication_lag"
	MetricConnectionsPct MetricName = "connections_pct"
	MetricRelayLogSpace  MetricName = "relay_log_space"
)

// Category 8: System/Pattern (Slow, 30s).
const (
	MetricUptimeSinceFlush   MetricName = "uptime_since_flush"
	MetricHandlerReadRndRate MetricName = "handler_read_rnd_rate"
)

// Backward-compatible aliases.
const (
	MetricActiveSessions = MetricThreadsRunning
)

// ──────────────────────────────────────────────────────────────────
// Root cause types (14 classifications)
// ──────────────────────────────────────────────────────────────────

// RootCauseType classifies the probable root cause of a MySQL performance anomaly.
type RootCauseType string

const (
	CauseSlowQuery          RootCauseType = "slow_query"
	CauseRowLockContention  RootCauseType = "row_lock_contention"
	CauseBinlogBottleneck   RootCauseType = "binlog_bottleneck"
	CauseBufferPoolPressure RootCauseType = "buffer_pool_pressure"
	CauseConnectionStorm    RootCauseType = "connection_storm"
	CauseReplicationLag     RootCauseType = "replication_lag"
	CauseInnoDBHistory      RootCauseType = "innodb_history"
	CauseDeadlockStorm      RootCauseType = "deadlock_storm"
	CauseIOSubsystem        RootCauseType = "io_subsystem"
	CauseMemoryPressure     RootCauseType = "memory_pressure"
	CauseTempSpill          RootCauseType = "temp_spill"
	CauseInnoDBUndoPressure RootCauseType = "innodb_undo_pressure"
	CauseReplicationApplier RootCauseType = "replication_applier_lag"
	CauseDatabaseHang       RootCauseType = "database_hang"
	CauseUnknown            RootCauseType = "unknown"
)

// String returns the Chinese display name for the root cause type.
func (r RootCauseType) String() string {
	switch r {
	case CauseSlowQuery:
		return "慢查询冲高"
	case CauseRowLockContention:
		return "行锁争用冲高"
	case CauseBinlogBottleneck:
		return "Binlog写入冲高"
	case CauseBufferPoolPressure:
		return "缓冲池命中率下降"
	case CauseConnectionStorm:
		return "连接数冲高"
	case CauseReplicationLag:
		return "复制延迟冲高"
	case CauseInnoDBHistory:
		return "InnoDB History冲高"
	case CauseDeadlockStorm:
		return "死锁冲高"
	case CauseIOSubsystem:
		return "I/O冲高"
	case CauseMemoryPressure:
		return "内存冲高"
	case CauseTempSpill:
		return "临时表磁盘冲高"
	case CauseInnoDBUndoPressure:
		return "InnoDB Undo冲高"
	case CauseReplicationApplier:
		return "复制回放延迟"
	case CauseDatabaseHang:
		return "数据库Hang"
	default:
		return "未知"
	}
}

// IsValid returns true if the root cause type is a known value.
func (r RootCauseType) IsValid() bool {
	switch r {
	case CauseSlowQuery, CauseRowLockContention, CauseBinlogBottleneck,
		CauseBufferPoolPressure, CauseConnectionStorm, CauseReplicationLag,
		CauseInnoDBHistory, CauseDeadlockStorm, CauseIOSubsystem,
		CauseMemoryPressure, CauseTempSpill, CauseInnoDBUndoPressure,
		CauseReplicationApplier, CauseDatabaseHang, CauseUnknown:
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

// SQLProfile summarizes one SQL's behavior during the burst window.
type SQLProfile struct {
	Digest        string  `json:"digest"`
	SQLText       string  `json:"sql_text"`
	ExecCount     int64   `json:"exec_count"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	MaxLatencyMs  float64 `json:"max_latency_ms"`
	MaxConcurrent int     `json:"max_concurrent"`
	LockTimeMs    float64 `json:"lock_time_ms"`
}

// BlockingChain represents a lock blocking tree found during burst.
type BlockingChain struct {
	BlockerThreadID int64  `json:"blocker_thread_id"`
	BlockerUser     string `json:"blocker_user"`
	BlockerQuery    string `json:"blocker_query"`
	WaitType        string `json:"wait_type"`
	VictimCount     int    `json:"victim_count"`
}

// WaitBucket groups wait events by type during the burst window.
type WaitBucket struct {
	EventName  string  `json:"event_name"`
	WaitClass  string  `json:"wait_class"`
	Count      int     `json:"count"`
	TotalMs    float64 `json:"total_ms"`
	Percentage float64 `json:"percentage"`
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

// ResourceLimitEntry represents a resource limit check.
type ResourceLimitEntry struct {
	ResourceName       string  `json:"resource_name"`
	CurrentUtilization float64 `json:"current_utilization"`
	LimitValue         float64 `json:"limit_value"`
}

// ──────────────────────────────────────────────────────────────────
// Burst report
// ──────────────────────────────────────────────────────────────────

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

// BurstReport is the aggregated analysis of a burst collection.
type BurstReport struct {
	TriggerEvent   TriggerEvent             `json:"trigger_event"`
	DurationSec    float64                  `json:"duration_sec"`
	PeakActive     int                      `json:"peak_active"`
	BaselineActive float64                  `json:"baseline_active"`

	TopSQLs        []SQLProfile             `json:"top_sqls"`
	BlockingChains []BlockingChain          `json:"blocking_chains"`
	WaitProfile    []WaitBucket             `json:"wait_profile"`
	Metrics        map[string]MetricSummary `json:"metrics"`

	Classification Classification           `json:"classification"`

	// Enrichment (post-burst).
	SpaceDetails   []SpaceDetail        `json:"space_details,omitempty"`
	ParamDetails   []ParamDetail        `json:"param_details,omitempty"`
	ResourceLimits []ResourceLimitEntry `json:"resource_limits,omitempty"`

	Frames        []MetricSample `json:"-"`
	RawFrameCount int            `json:"raw_frame_count"`
	StartTime     time.Time      `json:"start_time"`
	EndTime       time.Time      `json:"end_time"`
}

// ──────────────────────────────────────────────────────────────────
// Config
// ──────────────────────────────────────────────────────────────────

// Config controls sentinel behavior.
type Config struct {
	Enable    bool `json:"enable"`
	AutoStart bool `json:"auto_start"`

	// Probe.
	PollInterval   time.Duration `json:"poll_interval"`
	BaselineWindow int           `json:"baseline_window"`
	MinSamples     int           `json:"min_samples"`

	// Trigger.
	TriggerMode    TriggerMode `json:"trigger_mode"`
	SigmaThreshold float64     `json:"sigma_threshold"`
	SustainedCount int         `json:"sustained_count"`

	// Fixed-mode thresholds.
	FixedThresholds map[MetricName]MetricThreshold `json:"fixed_thresholds"`

	// Hardware-based absolute thresholds.
	Hardware HardwareProfile `json:"hardware"`

	// Burst.
	BurstInterval  time.Duration `json:"burst_interval"`
	BurstDuration  time.Duration `json:"burst_duration"`
	BurstCalmDelay time.Duration `json:"burst_calm_delay"`
	CooldownPeriod time.Duration `json:"cooldown_period"`
}

// DefaultConfig returns production default sentinel configuration.
func DefaultConfig() Config {
	return Config{
		Enable:         true,
		AutoStart:      true,
		PollInterval:   time.Second,
		BaselineWindow: 60,
		MinSamples:     10,
		TriggerMode:    TriggerAdaptive,
		SigmaThreshold: 3.0,
		SustainedCount: 3,
		FixedThresholds: map[MetricName]MetricThreshold{
			MetricThreadsRunning:   {Enabled: true, Multiplier: 2.0},
			MetricThreadsConnected: {Enabled: true, Multiplier: 2.0},
			MetricLockWaits:        {Enabled: true, Absolute: 5},
			MetricLongQueries:      {Enabled: true, Absolute: 3},
			MetricRedoRate:         {Enabled: true, Multiplier: 5.0},
			MetricRowLockWaits:     {Enabled: true, Multiplier: 3.0},
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

// MetricLabel returns the display label for a metric name.
func MetricLabel(name MetricName) string {
	if def := GetMetricDef(name); def != nil {
		return def.Label
	}
	return string(name)
}

// MetricUnit returns the display unit for a metric name.
func MetricUnit(name MetricName) string {
	if def := GetMetricDef(name); def != nil {
		return def.Unit
	}
	return ""
}

