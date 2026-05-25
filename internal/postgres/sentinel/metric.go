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
 *	  internal/postgres/sentinel/metric.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// ProbeTier defines the probe frequency for a metric.
type ProbeTier int

const (
	ProbeFast   ProbeTier = iota // 1s — session/load, lock/concurrency
	ProbeMedium                  // 10s — throughput, wait/latency
	ProbeSlow                    // 30s — memory/cache, storage, vacuum/xid, system
)

// MetricCategory groups related metrics.
type MetricCategory string

const (
	CatSessionLoad     MetricCategory = "session_load"
	CatThroughput      MetricCategory = "throughput"
	CatWaitLatency     MetricCategory = "wait_latency"
	CatMemoryCache     MetricCategory = "memory_cache"
	CatStorage         MetricCategory = "storage"
	CatWALArchive      MetricCategory = "wal_archive"
	CatLockConcurrency MetricCategory = "lock_concurrency"
	CatVacuumXID       MetricCategory = "vacuum_xid"
	CatSystemPattern   MetricCategory = "system_pattern"
)

// ──────────────────────────────────────────────────────────────────
// MetricDef — per-metric detection configuration
// ──────────────────────────────────────────────────────────────────

// MetricDef describes a single metric's behavior and detection configuration.
type MetricDef struct {
	Name       MetricName
	Label      string // Chinese+English display name
	Unit       string // display unit
	Category   MetricCategory
	Tier       ProbeTier
	Strategies []StrategyType

	// T1/T2: override sustained count to 1 (e.g., deadlocks).
	ImmediateTrigger bool

	// T5: compound — all these metrics must also be in alert.
	CompoundWith []MetricName

	// T6: capacity thresholds.
	CapacityYellow float64
	CapacityRed    float64
	InvertCapacity bool // true for "higher is better" metrics (trigger when LOW)

	// T8: regression floor.
	RegressionFloor   float64
	RegressionSustain int // sustained ticks (default 5)

	// T9: absence detection.
	DropThreshold   float64 // percentage drop to trigger (e.g., 0.8 = 80%)
	AbsenceSustain  int     // sustained ticks (default 5)
	MinBaseline     float64 // minimum baseline to enable T9
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
// Registry — all 33 metric definitions
// ──────────────────────────────────────────────────────────────────

var allMetrics = map[MetricName]*MetricDef{
	// ── Category 1: Session/Load (Fast, 1s) ───────────────────────
	MetricActiveSessions: {
		Name: MetricActiveSessions, Label: "活跃会话Active Sessions", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricCPUSessions: {
		Name: MetricCPUSessions, Label: "CPU会话On CPU", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricIOWaitSessions: {
		Name: MetricIOWaitSessions, Label: "I/O等待会话IO Wait", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLockWaitSessions: {
		Name: MetricLockWaitSessions, Label: "锁等待会话Lock Wait", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLongQueries: {
		Name: MetricLongQueries, Label: "慢查询Long SQL(>30s)", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricIdleInTransaction: {
		Name: MetricIdleInTransaction, Label: "空闲事务Idle in Transaction", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricConnectionsPct: {
		Name: MetricConnectionsPct, Label: "连接使用率Connections", Unit: "%",
		Category: CatSessionLoad, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},

	// ── Category 2: Throughput (Medium, 10s) ──────────────────────
	MetricXactCommitRate: {
		Name: MetricXactCommitRate, Label: "提交速率TPS", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies:      []StrategyType{StrategyT9},
		DropThreshold:   0.8,
		AbsenceSustain:  5,
		MinBaseline:     20,
		CheckActiveSync: true,
	},
	MetricXactRollbackRate: {
		Name: MetricXactRollbackRate, Label: "回滚速率Rollback", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricTupReturnedRate: {
		Name: MetricTupReturnedRate, Label: "行返回速率Tup Returned", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies:   []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricTupFetchedRate: {
		Name: MetricTupFetchedRate, Label: "行获取速率Tup Fetched", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies:   []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricTempBytesRate: {
		Name: MetricTempBytesRate, Label: "临时空间速率Temp Bytes", Unit: "B/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── Category 3: Wait/Latency (Medium, 10s) ───────────────────
	MetricLWLockWaitSessions: {
		Name: MetricLWLockWaitSessions, Label: "LWLock等待会话", Unit: "个",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricBufferPinWaitSessions: {
		Name: MetricBufferPinWaitSessions, Label: "BufferPin等待会话", Unit: "个",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1},
	},
	MetricAvgQueryTimeMs: {
		Name: MetricAvgQueryTimeMs, Label: "平均查询耗时Avg Query Time", Unit: "ms",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT7},
	},
	MetricDeadlocks: {
		Name: MetricDeadlocks, Label: "死锁Deadlock", Unit: "次",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies:       []StrategyType{StrategyT1},
		ImmediateTrigger: true,
	},

	// ── Category 4: Memory/Cache (Slow, 30s) ─────────────────────
	MetricCacheHitPct: {
		Name: MetricCacheHitPct, Label: "缓存命中率Cache Hit", Unit: "%",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies:        []StrategyType{StrategyT8},
		RegressionFloor:   90.0,
		RegressionSustain: 5,
	},
	MetricTempFilesRate: {
		Name: MetricTempFilesRate, Label: "临时文件数Temp Files", Unit: "/s",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricSharedBufferPct: {
		Name: MetricSharedBufferPct, Label: "Shared Buffer使用率", Unit: "%",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},

	// ── Category 5: Storage/Capacity (Slow, 30s) ─────────────────
	MetricTablespaceUsedPct: {
		Name: MetricTablespaceUsedPct, Label: "表空间使用率Tablespace", Unit: "%",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricDeadTupleRatio: {
		Name: MetricDeadTupleRatio, Label: "死元组比例Dead Tuple", Unit: "%",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6, StrategyT4},
		CapacityYellow: 30, CapacityRed: 50,
	},
	MetricTableBloatPct: {
		Name: MetricTableBloatPct, Label: "表膨胀Table Bloat", Unit: "%",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 40, CapacityRed: 60,
	},

	// ── Category 6: WAL/Archive (Slow, 30s) ──────────────────────
	MetricWALBytesRate: {
		Name: MetricWALBytesRate, Label: "WAL生成速率WAL Rate", Unit: "B/s",
		Category: CatWALArchive, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricCheckpointsReq: {
		Name: MetricCheckpointsReq, Label: "请求Checkpoint", Unit: "次",
		Category: CatWALArchive, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricArchiveFailCnt: {
		Name: MetricArchiveFailCnt, Label: "归档失败Archive Fail", Unit: "次",
		Category: CatWALArchive, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1},
	},
	MetricReplicationLag: {
		Name: MetricReplicationLag, Label: "复制延迟Replication Lag", Unit: "s",
		Category: CatWALArchive, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 30, CapacityRed: 120,
	},

	// ── Category 7: Lock/Concurrency (Medium) ────────────────────
	MetricBlockerCount: {
		Name: MetricBlockerCount, Label: "阻塞者数Blocker", Unit: "个",
		Category: CatLockConcurrency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricRowExclusiveLocks: {
		Name: MetricRowExclusiveLocks, Label: "行排他锁RowExclusive", Unit: "个",
		Category: CatLockConcurrency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── Category 8: Vacuum/XID (Slow, 30s) ───────────────────────
	MetricXIDAgeRatio: {
		Name: MetricXIDAgeRatio, Label: "XID年龄XID Age", Unit: "%",
		Category: CatVacuumXID, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 50, CapacityRed: 80,
	},
	MetricAutovacuumWorkers: {
		Name: MetricAutovacuumWorkers, Label: "Autovacuum工作进程", Unit: "个",
		Category: CatVacuumXID, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 2, CapacityRed: 3, // default max_autovacuum_workers=3
	},
	MetricOldestXactAgeSec: {
		Name: MetricOldestXactAgeSec, Label: "最老事务时长Oldest Xact", Unit: "s",
		Category: CatVacuumXID, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 3600, CapacityRed: 7200, // 1h / 2h
	},

	// ── Category 9: System/Pattern (Slow, 30s) ───────────────────
	MetricPGIsInRecovery: {
		Name: MetricPGIsInRecovery, Label: "实例恢复状态Recovery", Unit: "",
		Category: CatSystemPattern, Tier: ProbeSlow,
		Strategies:       []StrategyType{StrategyT5},
		ImmediateTrigger: true,
	},
	MetricBackendErrorRate: {
		Name: MetricBackendErrorRate, Label: "后端错误速率Backend Errors", Unit: "/s",
		Category: CatSystemPattern, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
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
// Fast-tier first, then medium, then slow.
var metricPriorityOrder = []MetricName{
	// Fast — Session/Load
	MetricActiveSessions, MetricCPUSessions, MetricIOWaitSessions,
	MetricLockWaitSessions, MetricLongQueries, MetricIdleInTransaction,

	// Medium — Throughput
	MetricXactCommitRate, MetricXactRollbackRate,
	MetricTupReturnedRate, MetricTupFetchedRate, MetricTempBytesRate,
	// Medium — Wait/Latency
	MetricLWLockWaitSessions, MetricBufferPinWaitSessions,
	MetricAvgQueryTimeMs, MetricDeadlocks,
	// Medium — Lock/Concurrency
	MetricBlockerCount, MetricRowExclusiveLocks,

	// Slow — Memory/Cache
	MetricCacheHitPct, MetricTempFilesRate, MetricSharedBufferPct,
	// Slow — Storage
	MetricTablespaceUsedPct, MetricDeadTupleRatio, MetricTableBloatPct,
	// Slow — WAL/Archive
	MetricWALBytesRate, MetricCheckpointsReq, MetricArchiveFailCnt, MetricReplicationLag,
	// Slow — Vacuum/XID
	MetricXIDAgeRatio, MetricAutovacuumWorkers, MetricOldestXactAgeSec,
	// Slow — Session/Load (connections is slow-tier)
	MetricConnectionsPct,
	// Slow — System/Pattern
	MetricPGIsInRecovery, MetricBackendErrorRate,
}
