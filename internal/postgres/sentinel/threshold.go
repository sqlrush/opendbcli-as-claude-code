/*-------------------------------------------------------------------------
 *
 * threshold.go
 *	  SoftAbsoluteMin returns the noise-filter threshold for a metric.
 *	  Below this value, the metric is considered normal regardless of
 *	  3σ. Scales with CPU cores; uses fixed minimum when CPUCores is
 *	  unknown (0).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/threshold.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "math"

// SoftAbsoluteMin returns the noise-filter threshold for a metric.
// Below this value, the metric is considered normal regardless of 3σ.
// Scales with CPU cores; uses fixed minimum when CPUCores is unknown (0).
//
// Formula: max(CPU × factor, fixedFloor)
func SoftAbsoluteMin(metric MetricName, hw HardwareProfile) float64 {
	cpu := float64(hw.CPUCores)

	switch metric {
	// ── Session/Load ──
	case MetricActiveSessions:
		return math.Max(cpu*1.5, 8)
	case MetricCPUSessions:
		return math.Max(cpu*0.75, 4)
	case MetricIOWaitSessions:
		return math.Max(cpu*0.5, 4)
	case MetricLockWaitSessions:
		return 3
	case MetricLongQueries:
		return 2
	case MetricIdleInTransaction:
		return 5
	case MetricConnectionsPct:
		return 0 // uses T6 capacity

	// ── Throughput ──
	case MetricXactCommitRate:
		return 0 // uses T9 absence
	case MetricXactRollbackRate:
		return 5
	case MetricTupReturnedRate:
		return 0 // uses T3 trend
	case MetricTupFetchedRate:
		return 0 // uses T3 trend
	case MetricTempBytesRate:
		return 50 * 1024 * 1024 // 50 MB/s

	// ── Wait/Latency ──
	case MetricLWLockWaitSessions:
		return math.Max(cpu*0.5, 3)
	case MetricBufferPinWaitSessions:
		return 3
	case MetricAvgQueryTimeMs:
		return 0 // uses T7 shift
	case MetricDeadlocks:
		return 1 // immediate trigger

	// ── Memory/Cache ──
	case MetricCacheHitPct:
		return 0 // uses T8 regression
	case MetricTempFilesRate:
		return 5
	case MetricSharedBufferPct:
		return 0 // uses T6

	// ── Storage ──
	case MetricTablespaceUsedPct:
		return 0 // uses T6
	case MetricDeadTupleRatio:
		return 0 // uses T6
	case MetricTableBloatPct:
		return 0 // uses T6

	// ── WAL/Archive ──
	case MetricWALBytesRate:
		return 100 * 1024 * 1024 // 100 MB/s
	case MetricCheckpointsReq:
		return 5
	case MetricArchiveFailCnt:
		return 1
	case MetricReplicationLag:
		return 10 // seconds

	// ── Lock/Concurrency ──
	case MetricBlockerCount:
		return 2
	case MetricRowExclusiveLocks:
		return math.Max(cpu*5, 50)

	// ── Vacuum/XID ──
	case MetricXIDAgeRatio:
		return 0 // uses T6
	case MetricAutovacuumWorkers:
		return 0 // uses T6
	case MetricOldestXactAgeSec:
		return 300 // 5 min

	// ── System/Pattern ──
	case MetricPGIsInRecovery:
		return 0 // uses T5
	case MetricBackendErrorRate:
		return 1

	default:
		return 0
	}
}

// HardCeiling returns the absolute danger threshold for a metric.
// When current value reaches this level, trigger regardless of baseline trend.
// Hard ceiling = 2 × soft absolute minimum.
func HardCeiling(metric MetricName, hw HardwareProfile) float64 {
	return 2 * SoftAbsoluteMin(metric, hw)
}
