/*-------------------------------------------------------------------------
 *
 * threshold.go
 *	  SoftAbsoluteMin returns the noise-filter threshold for a metric.
 *	  Below this value, the metric is considered normal regardless of
 *	  3σ.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/threshold.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "math"

// SoftAbsoluteMin returns the noise-filter threshold for a metric.
// Below this value, the metric is considered normal regardless of 3σ.
func SoftAbsoluteMin(metric MetricName, hw HardwareProfile) float64 {
	cpu := float64(hw.CPUCores)

	switch metric {
	// Session/Load
	case MetricThreadsRunning:
		return math.Max(cpu*1.5, 5)
	case MetricThreadsConnected:
		return math.Max(cpu*5, 20)
	case MetricLockWaits:
		return 3
	case MetricLongQueries:
		return 2
	case MetricThreadsCached:
		return 0 // T3 trend only
	case MetricAbortedConnectsRate:
		return 5

	// Throughput
	case MetricTPS:
		return 0 // T9 absence
	case MetricQPS:
		return 0 // T9 absence
	case MetricRedoRate:
		return 1000 // 1 MB/s
	case MetricSlowQueriesRate:
		return 3
	case MetricTableLocksWaitedRate:
		return 5
	case MetricSelectFullJoinRate:
		return 10

	// Wait/Latency
	case MetricRowLockWaits:
		return 5
	case MetricDeadlocks:
		return 0.1 // immediate trigger
	case MetricInnoDBLockWaitTimeout:
		return 3
	case MetricAvgRowLockTimeMs:
		return 0 // T7 shift

	// Memory/Cache
	case MetricBufferPoolHit:
		return 0 // T8 regression
	case MetricBufferPoolDirtyPct:
		return 0 // T6
	case MetricAdaptiveHashHitPct:
		return 0 // T8

	// Storage
	case MetricTablespaceUsedPct:
		return 0 // T6
	case MetricInnoDBDataFileGrowth:
		return 0 // T3
	case MetricTmpDiskPct:
		return 0 // T6

	// InnoDB/Undo
	case MetricHistoryList:
		return 5000
	case MetricInnoDBLogWaitsRate:
		return 1
	case MetricInnoDBBufferPoolWaitFree:
		return 1

	// Replication
	case MetricReplicationLag:
		return 5
	case MetricConnectionsPct:
		return 0 // T6
	case MetricRelayLogSpace:
		return 0 // T6

	// System
	case MetricUptimeSinceFlush:
		return 0 // T5
	case MetricHandlerReadRndRate:
		return 0 // T3

	default:
		return 0
	}
}

// HardCeiling returns the absolute danger threshold for a metric.
// Hard ceiling = 2 × soft absolute minimum.
func HardCeiling(metric MetricName, hw HardwareProfile) float64 {
	return 2 * SoftAbsoluteMin(metric, hw)
}
