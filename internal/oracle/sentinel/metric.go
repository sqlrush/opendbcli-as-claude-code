/*-------------------------------------------------------------------------
 *
 * metric.go
 *	  ProbeTier defines the probe frequency for a metric.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/metric.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// ProbeTier defines the probe frequency for a metric.
type ProbeTier int

const (
	ProbeFast   ProbeTier = iota // 1s — session/load, lock/concurrency
	ProbeMedium                  // 10s — throughput, wait/latency, SQL performance
	ProbeSlow                    // 30s — memory/cache, storage, redo/archive, system
)

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

// MetricCategory groups related metrics.
type MetricCategory string

const (
	CatSessionLoad     MetricCategory = "session_load"
	CatThroughput      MetricCategory = "throughput"
	CatWaitLatency     MetricCategory = "wait_latency"
	CatMemoryCache     MetricCategory = "memory_cache"
	CatStorage         MetricCategory = "storage"
	CatRedoArchive     MetricCategory = "redo_archive"
	CatLockConcurrency MetricCategory = "lock_concurrency"
	CatSQLPerformance  MetricCategory = "sql_performance"
	CatSystemPattern   MetricCategory = "system_pattern"
)

// ──────────────────────────────────────────────────────────────────
// MetricName constants — additional 41 (original 7 are in types.go)
// ──────────────────────────────────────────────────────────────────

// Session/Load (Fast).
const (
	MetricTotalSessions       MetricName = "total_sessions"
	MetricPQSessions          MetricName = "pq_sessions"
	MetricSessionCreationRate MetricName = "session_creation_rate"
	MetricBackgroundWait      MetricName = "background_wait"
	MetricOSLoad              MetricName = "os_load"
)

// Throughput (Medium).
const (
	MetricPhysicalReadRate MetricName = "physical_read_rate"
	MetricLogicalReadRate  MetricName = "logical_read_rate"
	MetricCommitRate       MetricName = "commit_rate"
)

// Wait/Latency (Medium).
const (
	MetricLogFileSyncUs    MetricName = "log_file_sync_avg_us"
	MetricDbFileSeqReadUs  MetricName = "db_file_seq_read_avg_us"
	MetricDbFileScatReadUs MetricName = "db_file_scat_read_avg_us"
	MetricEnqueueWaitMs    MetricName = "enqueue_wait_time_ms"
	MetricLatchFreeRate    MetricName = "latch_free_rate"
	MetricNetworkRoundTrip MetricName = "network_round_trip_us"
)

// Memory/Cache (Slow).
const (
	MetricBufferCacheHit    MetricName = "buffer_cache_hit_pct"
	MetricLibraryCacheHit   MetricName = "library_cache_hit_pct"
	MetricPGAUsedPct        MetricName = "pga_used_pct"
	MetricSharedPoolFreePct MetricName = "shared_pool_free_pct"
)

// Storage/Capacity (Slow).
const (
	MetricTablespaceUsedPct MetricName = "tablespace_used_pct"
	MetricTempUsedPct       MetricName = "temp_used_pct"
	MetricUndoUsedPct       MetricName = "undo_used_pct"
	MetricFRAUsedPct        MetricName = "fra_used_pct"
	MetricASMUsedPct        MetricName = "asm_diskgroup_used_pct"
)

// Redo/Archive (Slow).
const (
	MetricLogSwitchRate    MetricName = "log_switch_rate"
	MetricArchiveLagSec    MetricName = "archive_lag_sec"
	MetricStandbyApplyLag  MetricName = "standby_apply_lag_sec"
	MetricRedoLogSpaceWait MetricName = "redo_log_space_wait"
)

// Lock/Concurrency (Fast).
const (
	MetricBlockingChains   MetricName = "blocking_chains"
	MetricEnqueueDeadlocks MetricName = "enqueue_deadlocks"
	MetricMutexWait        MetricName = "mutex_wait_sessions"
	MetricRowLockWaitMs    MetricName = "row_lock_wait_time_ms"
)

// SQL Performance (Medium).
const (
	MetricTopSQLDrift     MetricName = "top_sql_elapsed_drift"
	MetricFullScanRate    MetricName = "full_scan_rate"
	MetricPlanChangeCount MetricName = "plan_change_count"
	MetricSQLThrottle     MetricName = "sql_throttle_count"
)

// System/Pattern (Slow).
const (
	MetricInstanceStatus        MetricName = "instance_status"
	MetricDataGuardStatus       MetricName = "dataguard_status"
	MetricAlertLogErrors        MetricName = "alert_log_ora_errors"
	MetricJobFailureRate        MetricName = "job_failure_rate"
	MetricResourceLimitPct      MetricName = "resource_limit_pct"
	MetricCheckpointNotComplete MetricName = "checkpoint_not_complete"
)

// ──────────────────────────────────────────────────────────────────
// MetricDef — per-metric detection configuration
// ──────────────────────────────────────────────────────────────────

// MetricDef describes a single metric's behavior and detection configuration.
type MetricDef struct {
	Name       MetricName
	Label      string // Chinese+English display name for alerts
	Unit       string // display unit (e.g., "个", "KB/s", "%", "μs")
	Category   MetricCategory
	Tier       ProbeTier
	Strategies []StrategyType

	// T1/T2: override sustained count to 1 (e.g., deadlocks).
	ImmediateTrigger bool

	// T5: compound condition — all these metrics must also be in alert.
	CompoundWith []MetricName

	// T6: capacity thresholds (percentage values, e.g., 85.0).
	CapacityYellow float64
	CapacityRed    float64
	InvertCapacity bool // true for "free %" metrics (trigger when LOW)

	// T8: regression floor (trigger when value drops below this).
	RegressionFloor   float64
	RegressionSustain int // sustained ticks (default 5)

	// T9: absence detection.
	DropThreshold   float64 // percentage drop to trigger (e.g., 0.8 = 80%)
	AbsenceSustain  int     // sustained ticks (default 5)
	MinBaseline     float64 // minimum baseline rate to enable T9
	CheckActiveSync bool    // also check active_sessions alignment

	// T3: trend window count for confirmation (default 3).
	TrendWindows int
}

// HasStrategy returns true if the metric uses the given strategy.
func (d *MetricDef) HasStrategy(s StrategyType) bool {
	for _, st := range d.Strategies {
		if st == s {
			return true
		}
	}
	return false
}

// ──────────────────────────────────────────────────────────────────
// Registry — all 48 metric definitions
// ──────────────────────────────────────────────────────────────────

var allMetrics = map[MetricName]*MetricDef{
	// ── Session/Load (Fast, 1s) ──────────────────────────────────
	MetricActive: {
		Name: MetricActive, Label: "活跃会话Active Sessions", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricCPU: {
		Name: MetricCPU, Label: "活跃会话On CPU", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricIO: {
		Name: MetricIO, Label: "活跃会话I/O Wait", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLock: {
		Name: MetricLock, Label: "活跃会话Lock Wait", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLongSQL: {
		Name: MetricLongSQL, Label: "活跃会话Long SQL(>30s)", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricTotalSessions: {
		Name: MetricTotalSessions, Label: "总会话数Total Sessions", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricPQSessions: {
		Name: MetricPQSessions, Label: "并行查询会话PQ Sessions", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricSessionCreationRate: {
		Name: MetricSessionCreationRate, Label: "新建会话速率Session Creation", Unit: "/s", Category: CatSessionLoad,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricBackgroundWait: {
		Name: MetricBackgroundWait, Label: "后台进程等待Background Wait", Unit: "个", Category: CatSessionLoad,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1},
	},
	MetricOSLoad: {
		Name: MetricOSLoad, Label: "OS负载OS Load", Unit: "", Category: CatSessionLoad,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},

	// ── Throughput (Fast for existing, Medium for new) ───────────
	MetricRedoRate: {
		Name: MetricRedoRate, Label: "Redo生成速率Redo Rate", Unit: "KB/s", Category: CatThroughput,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricHardParse: {
		Name: MetricHardParse, Label: "硬解析速率Hard Parse", Unit: "/s", Category: CatThroughput,
		Tier: ProbeFast, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricPhysicalReadRate: {
		Name: MetricPhysicalReadRate, Label: "物理读速率Physical Reads", Unit: "/s", Category: CatThroughput,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricLogicalReadRate: {
		Name: MetricLogicalReadRate, Label: "逻辑读速率Logical Reads", Unit: "/s", Category: CatThroughput,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricCommitRate: {
		Name: MetricCommitRate, Label: "提交速度TPS", Unit: "/s", Category: CatThroughput,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT9},
		DropThreshold: 0.8, AbsenceSustain: 5, MinBaseline: 20,
		CheckActiveSync: true,
	},

	// ── Wait/Latency (Medium, 10s) ──────────────────────────────
	MetricLogFileSyncUs: {
		Name: MetricLogFileSyncUs, Label: "日志同步延迟Log File Sync", Unit: "μs", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricDbFileSeqReadUs: {
		Name: MetricDbFileSeqReadUs, Label: "单块读延迟db file sequential read", Unit: "μs", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricDbFileScatReadUs: {
		Name: MetricDbFileScatReadUs, Label: "多块读延迟db file scattered read", Unit: "μs", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricEnqueueWaitMs: {
		Name: MetricEnqueueWaitMs, Label: "队列等待Enqueue Wait", Unit: "ms", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricLatchFreeRate: {
		Name: MetricLatchFreeRate, Label: "Latch命中率Latch Hit Ratio", Unit: "%", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT8},
		RegressionFloor: 5.0, RegressionSustain: 5,
	},
	MetricNetworkRoundTrip: {
		Name: MetricNetworkRoundTrip, Label: "网络往返延迟SQL*Net Round-trip", Unit: "μs", Category: CatWaitLatency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT7},
	},

	// ── Memory/Cache (Slow, 30s) ────────────────────────────────
	MetricBufferCacheHit: {
		Name: MetricBufferCacheHit, Label: "Buffer Cache命中率", Unit: "%", Category: CatMemoryCache,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT8},
		RegressionFloor: 90.0, RegressionSustain: 5,
	},
	MetricLibraryCacheHit: {
		Name: MetricLibraryCacheHit, Label: "Library Cache命中率", Unit: "%", Category: CatMemoryCache,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT8},
		RegressionFloor: 95.0, RegressionSustain: 5,
	},
	MetricPGAUsedPct: {
		Name: MetricPGAUsedPct, Label: "PGA使用率", Unit: "%", Category: CatMemoryCache,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricSharedPoolFreePct: {
		Name: MetricSharedPoolFreePct, Label: "Shared Pool空闲率", Unit: "%", Category: CatMemoryCache,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6, StrategyT8},
		CapacityYellow: 15, CapacityRed: 5, InvertCapacity: true,
		RegressionFloor: 50.0, RegressionSustain: 5,
	},

	// ── Storage/Capacity (Slow, 30s) ────────────────────────────
	MetricTablespaceUsedPct: {
		Name: MetricTablespaceUsedPct, Label: "表空间Tablespace使用率", Unit: "%", Category: CatStorage,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricTempUsedPct: {
		Name: MetricTempUsedPct, Label: "临时表空间Temp使用率", Unit: "%", Category: CatStorage,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6, StrategyT4},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricUndoUsedPct: {
		Name: MetricUndoUsedPct, Label: "Undo表空间使用率", Unit: "%", Category: CatStorage,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricFRAUsedPct: {
		Name: MetricFRAUsedPct, Label: "快速恢复区FRA使用率", Unit: "%", Category: CatStorage,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricASMUsedPct: {
		Name: MetricASMUsedPct, Label: "ASM磁盘组使用率", Unit: "%", Category: CatStorage,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},

	// ── Redo/Archive (Slow, 30s) ────────────────────────────────
	MetricLogSwitchRate: {
		Name: MetricLogSwitchRate, Label: "日志切换频率Log Switch", Unit: "次/h", Category: CatRedoArchive,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricArchiveLagSec: {
		Name: MetricArchiveLagSec, Label: "归档延迟Archive Lag", Unit: "秒", Category: CatRedoArchive,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 300, CapacityRed: 600,
	},
	MetricStandbyApplyLag: {
		Name: MetricStandbyApplyLag, Label: "备库同步延迟Standby Lag", Unit: "秒", Category: CatRedoArchive,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 120, CapacityRed: 600,
	},
	MetricRedoLogSpaceWait: {
		Name: MetricRedoLogSpaceWait, Label: "Redo空间等待Redo Log Space Wait", Unit: "次", Category: CatRedoArchive,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1, StrategyT5},
		CompoundWith: []MetricName{MetricLogSwitchRate},
	},

	// ── Lock/Concurrency (Fast, 1s) ─────────────────────────────
	MetricBlockingChains: {
		Name: MetricBlockingChains, Label: "阻塞链Blocking Chains", Unit: "层", Category: CatLockConcurrency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricEnqueueDeadlocks: {
		Name: MetricEnqueueDeadlocks, Label: "死锁Deadlock", Unit: "次", Category: CatLockConcurrency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1},
		ImmediateTrigger: true,
	},
	MetricMutexWait: {
		Name: MetricMutexWait, Label: "Mutex等待会话", Unit: "个", Category: CatLockConcurrency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1},
	},
	MetricRowLockWaitMs: {
		Name: MetricRowLockWaitMs, Label: "行锁等待Row Lock Wait", Unit: "ms", Category: CatLockConcurrency,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── SQL Performance (Medium, 10s) ───────────────────────────
	MetricTopSQLDrift: {
		Name: MetricTopSQLDrift, Label: "Top SQL耗时偏移Elapsed Drift", Unit: "倍", Category: CatSQLPerformance,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT7},
	},
	MetricFullScanRate: {
		Name: MetricFullScanRate, Label: "全表扫描速率Full Table Scan", Unit: "/s", Category: CatSQLPerformance,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricPlanChangeCount: {
		Name: MetricPlanChangeCount, Label: "执行计划变更Plan Change", Unit: "次", Category: CatSQLPerformance,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1},
	},
	MetricSQLThrottle: {
		Name: MetricSQLThrottle, Label: "SQL限流Resource Manager Throttle", Unit: "次", Category: CatSQLPerformance,
		Tier: ProbeMedium, Strategies: []StrategyType{StrategyT1},
	},

	// ── System/Pattern (Slow, 30s) ──────────────────────────────
	MetricInstanceStatus: {
		Name: MetricInstanceStatus, Label: "实例状态Instance Status", Unit: "", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT5},
		ImmediateTrigger: true,
	},
	MetricDataGuardStatus: {
		Name: MetricDataGuardStatus, Label: "DataGuard状态", Unit: "", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT5},
	},
	MetricAlertLogErrors: {
		Name: MetricAlertLogErrors, Label: "告警日志ORA错误Alert Log", Unit: "条", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricJobFailureRate: {
		Name: MetricJobFailureRate, Label: "作业失败率Job Failure", Unit: "%", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1},
	},
	MetricResourceLimitPct: {
		Name: MetricResourceLimitPct, Label: "资源限制使用率Resource Limit", Unit: "%", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricCheckpointNotComplete: {
		Name: MetricCheckpointNotComplete, Label: "Checkpoint未完成", Unit: "次", Category: CatSystemPattern,
		Tier: ProbeSlow, Strategies: []StrategyType{StrategyT1},
	},
}

// GetMetricDef returns the definition for a metric, or nil if unknown.
func GetMetricDef(name MetricName) *MetricDef {
	return allMetrics[name]
}

// MetricsByTier returns all metrics for a given probe tier.
func MetricsByTier(tier ProbeTier) []*MetricDef {
	var result []*MetricDef
	for _, def := range allMetrics {
		if def.Tier == tier {
			result = append(result, def)
		}
	}
	return result
}

// AllMetricDefs returns all metric definitions.
func AllMetricDefs() map[MetricName]*MetricDef {
	return allMetrics
}

// metricPriorityOrder defines the deterministic detection priority.
// Fast-tier metrics checked first, then medium, then slow.
var metricPriorityOrder = []MetricName{
	// Fast — Session/Load
	MetricActive, MetricCPU, MetricIO, MetricLock, MetricLongSQL,
	// Fast — Throughput (already in fast probe)
	MetricRedoRate, MetricHardParse,

	// Medium — Session/Load extended
	MetricTotalSessions, MetricPQSessions, MetricSessionCreationRate,
	MetricBackgroundWait, MetricOSLoad,
	// Medium — Throughput
	MetricPhysicalReadRate, MetricLogicalReadRate, MetricCommitRate,
	// Medium — Lock/Concurrency
	MetricBlockingChains, MetricEnqueueDeadlocks, MetricMutexWait, MetricRowLockWaitMs,
	// Medium — Wait/Latency
	MetricLogFileSyncUs, MetricDbFileSeqReadUs, MetricDbFileScatReadUs,
	MetricEnqueueWaitMs, MetricLatchFreeRate, MetricNetworkRoundTrip,
	// Medium — SQL Performance
	MetricTopSQLDrift, MetricFullScanRate, MetricPlanChangeCount, MetricSQLThrottle,

	// Slow — Memory/Cache
	MetricBufferCacheHit, MetricLibraryCacheHit, MetricPGAUsedPct, MetricSharedPoolFreePct,
	// Slow — Storage
	MetricTablespaceUsedPct, MetricTempUsedPct, MetricUndoUsedPct, MetricFRAUsedPct, MetricASMUsedPct,
	// Slow — Redo/Archive
	MetricLogSwitchRate, MetricArchiveLagSec, MetricStandbyApplyLag, MetricRedoLogSpaceWait,
	// Slow — System/Pattern
	MetricInstanceStatus, MetricDataGuardStatus, MetricAlertLogErrors,
	MetricJobFailureRate, MetricResourceLimitPct, MetricCheckpointNotComplete,
}
