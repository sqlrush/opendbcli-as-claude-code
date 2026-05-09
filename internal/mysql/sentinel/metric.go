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
 *	  internal/mysql/sentinel/metric.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// ProbeTier defines the probe frequency for a metric.
type ProbeTier int

const (
	ProbeFast   ProbeTier = iota // 1s — session/load
	ProbeMedium                  // 10s — throughput, wait/latency, lock
	ProbeSlow                    // 30s — memory/cache, storage, InnoDB, replication, system
)

// MetricCategory groups related metrics.
type MetricCategory string

const (
	CatSessionLoad     MetricCategory = "session_load"
	CatThroughput      MetricCategory = "throughput"
	CatWaitLatency     MetricCategory = "wait_latency"
	CatMemoryCache     MetricCategory = "memory_cache"
	CatStorage         MetricCategory = "storage"
	CatInnoDBUndo      MetricCategory = "innodb_undo"
	CatReplication     MetricCategory = "replication"
	CatLockConcurrency MetricCategory = "lock_concurrency"
	CatSystemPattern   MetricCategory = "system_pattern"
)

// ──────────────────────────────────────────────────────────────────
// MetricDef — per-metric detection configuration
// ──────────────────────────────────────────────────────────────────

// MetricDef describes a single metric's behavior and detection configuration.
type MetricDef struct {
	Name       MetricName
	Label      string
	Unit       string
	Category   MetricCategory
	Tier       ProbeTier
	Strategies []StrategyType

	ImmediateTrigger bool
	CompoundWith     []MetricName

	CapacityYellow float64
	CapacityRed    float64
	InvertCapacity bool

	RegressionFloor   float64
	RegressionSustain int

	DropThreshold   float64
	AbsenceSustain  int
	MinBaseline     float64
	CheckActiveSync bool

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
// Registry — all ~30 metric definitions
// ──────────────────────────────────────────────────────────────────

var allMetrics = map[MetricName]*MetricDef{
	// ── Category 1: Session/Load (Fast, 1s) ───────────────────────
	MetricThreadsRunning: {
		Name: MetricThreadsRunning, Label: "运行线程Threads Running", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricThreadsConnected: {
		Name: MetricThreadsConnected, Label: "连接数Threads Connected", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLockWaits: {
		Name: MetricLockWaits, Label: "锁等待Lock Waits", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricLongQueries: {
		Name: MetricLongQueries, Label: "慢查询Long Queries(>30s)", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricThreadsCached: {
		Name: MetricThreadsCached, Label: "缓存线程Threads Cached", Unit: "个",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies:   []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricAbortedConnectsRate: {
		Name: MetricAbortedConnectsRate, Label: "拒绝连接速率Aborted Connects", Unit: "/s",
		Category: CatSessionLoad, Tier: ProbeFast,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── Category 2: Throughput (Medium, 10s) ──────────────────────
	MetricTPS: {
		Name: MetricTPS, Label: "事务/秒TPS", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies:      []StrategyType{StrategyT9},
		DropThreshold:   0.8,
		AbsenceSustain:  5,
		MinBaseline:     20,
		CheckActiveSync: true,
	},
	MetricQPS: {
		Name: MetricQPS, Label: "查询/秒QPS", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies:      []StrategyType{StrategyT9},
		DropThreshold:   0.8,
		AbsenceSustain:  5,
		MinBaseline:     50,
		CheckActiveSync: true,
	},
	MetricRedoRate: {
		Name: MetricRedoRate, Label: "Redo写入Redo Rate", Unit: "KB/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT2},
	},
	MetricSlowQueriesRate: {
		Name: MetricSlowQueriesRate, Label: "慢查询速率Slow Queries", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricTableLocksWaitedRate: {
		Name: MetricTableLocksWaitedRate, Label: "表锁等待Table Locks Waited", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1},
	},
	MetricSelectFullJoinRate: {
		Name: MetricSelectFullJoinRate, Label: "全连接扫描Select Full Join", Unit: "/s",
		Category: CatThroughput, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── Category 3: Wait/Latency (Medium, 10s) ───────────────────
	MetricRowLockWaits: {
		Name: MetricRowLockWaits, Label: "行锁等待Row Lock Waits", Unit: "/s",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricDeadlocks: {
		Name: MetricDeadlocks, Label: "死锁Deadlocks", Unit: "/s",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies:       []StrategyType{StrategyT1},
		ImmediateTrigger: true,
	},
	MetricInnoDBLockWaitTimeout: {
		Name: MetricInnoDBLockWaitTimeout, Label: "锁超时Lock Wait Timeout", Unit: "次",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT1},
	},
	MetricAvgRowLockTimeMs: {
		Name: MetricAvgRowLockTimeMs, Label: "平均行锁耗时Avg Row Lock Time", Unit: "ms",
		Category: CatWaitLatency, Tier: ProbeMedium,
		Strategies: []StrategyType{StrategyT7},
	},

	// ── Category 4: Memory/Cache (Slow, 30s) ─────────────────────
	MetricBufferPoolHit: {
		Name: MetricBufferPoolHit, Label: "缓冲池命中率Buffer Pool Hit", Unit: "%",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies:        []StrategyType{StrategyT8},
		RegressionFloor:   90.0,
		RegressionSustain: 5,
	},
	MetricBufferPoolDirtyPct: {
		Name: MetricBufferPoolDirtyPct, Label: "脏页比例Buffer Pool Dirty", Unit: "%",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 70, CapacityRed: 85,
	},
	MetricAdaptiveHashHitPct: {
		Name: MetricAdaptiveHashHitPct, Label: "自适应哈希命中率AHI Hit", Unit: "%",
		Category: CatMemoryCache, Tier: ProbeSlow,
		Strategies:        []StrategyType{StrategyT8},
		RegressionFloor:   50.0,
		RegressionSustain: 5,
	},

	// ── Category 5: Storage/Capacity (Slow, 30s) ─────────────────
	MetricTablespaceUsedPct: {
		Name: MetricTablespaceUsedPct, Label: "表空间使用率Tablespace", Unit: "%",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricInnoDBDataFileGrowth: {
		Name: MetricInnoDBDataFileGrowth, Label: "InnoDB数据增长Data Growth", Unit: "KB/s",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:   []StrategyType{StrategyT3},
		TrendWindows: 3,
	},
	MetricTmpDiskPct: {
		Name: MetricTmpDiskPct, Label: "临时表磁盘率Tmp Disk Pct", Unit: "%",
		Category: CatStorage, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 25, CapacityRed: 50,
	},

	// ── Category 6: InnoDB/Undo (Slow, 30s) ──────────────────────
	MetricHistoryList: {
		Name: MetricHistoryList, Label: "History List长度", Unit: "",
		Category: CatInnoDBUndo, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 100000, CapacityRed: 1000000,
	},
	MetricInnoDBLogWaitsRate: {
		Name: MetricInnoDBLogWaitsRate, Label: "InnoDB Log等待Log Waits", Unit: "/s",
		Category: CatInnoDBUndo, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},
	MetricInnoDBBufferPoolWaitFree: {
		Name: MetricInnoDBBufferPoolWaitFree, Label: "缓冲池等待空闲页Wait Free", Unit: "/s",
		Category: CatInnoDBUndo, Tier: ProbeSlow,
		Strategies: []StrategyType{StrategyT1, StrategyT4},
	},

	// ── Category 7: Replication (Slow, 30s) ──────────────────────
	MetricReplicationLag: {
		Name: MetricReplicationLag, Label: "复制延迟Replication Lag", Unit: "s",
		Category: CatReplication, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT1, StrategyT6},
		CapacityYellow: 30, CapacityRed: 120,
	},
	MetricConnectionsPct: {
		Name: MetricConnectionsPct, Label: "连接使用率Connections Pct", Unit: "%",
		Category: CatReplication, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 85, CapacityRed: 95,
	},
	MetricRelayLogSpace: {
		Name: MetricRelayLogSpace, Label: "Relay Log空间", Unit: "MB",
		Category: CatReplication, Tier: ProbeSlow,
		Strategies:     []StrategyType{StrategyT6},
		CapacityYellow: 1024, CapacityRed: 5120, // 1GB / 5GB
	},

	// ── Category 8: System/Pattern (Slow, 30s) ───────────────────
	MetricUptimeSinceFlush: {
		Name: MetricUptimeSinceFlush, Label: "运行时间Uptime", Unit: "s",
		Category: CatSystemPattern, Tier: ProbeSlow,
		Strategies:       []StrategyType{StrategyT5},
		ImmediateTrigger: true,
	},
	MetricHandlerReadRndRate: {
		Name: MetricHandlerReadRndRate, Label: "随机读速率Handler Read Rnd", Unit: "/s",
		Category: CatSystemPattern, Tier: ProbeSlow,
		Strategies:   []StrategyType{StrategyT3},
		TrendWindows: 3,
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
var metricPriorityOrder = []MetricName{
	// Fast — Session/Load
	MetricThreadsRunning, MetricThreadsConnected,
	MetricLockWaits, MetricLongQueries,
	MetricThreadsCached, MetricAbortedConnectsRate,

	// Medium — Throughput
	MetricTPS, MetricQPS, MetricRedoRate,
	MetricSlowQueriesRate, MetricTableLocksWaitedRate, MetricSelectFullJoinRate,
	// Medium — Wait/Latency
	MetricRowLockWaits, MetricDeadlocks,
	MetricInnoDBLockWaitTimeout, MetricAvgRowLockTimeMs,

	// Slow — Memory/Cache
	MetricBufferPoolHit, MetricBufferPoolDirtyPct, MetricAdaptiveHashHitPct,
	// Slow — Storage
	MetricTablespaceUsedPct, MetricInnoDBDataFileGrowth, MetricTmpDiskPct,
	// Slow — InnoDB/Undo
	MetricHistoryList, MetricInnoDBLogWaitsRate, MetricInnoDBBufferPoolWaitFree,
	// Slow — Replication
	MetricReplicationLag, MetricConnectionsPct, MetricRelayLogSpace,
	// Slow — System
	MetricUptimeSinceFlush, MetricHandlerReadRndRate,
}
