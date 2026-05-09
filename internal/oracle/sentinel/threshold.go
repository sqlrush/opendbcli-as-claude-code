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
 *	  internal/oracle/sentinel/threshold.go
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
	// --- Original 7 metrics ---
	case MetricActive:
		return math.Max(cpu*1.5, 8)
	case MetricCPU:
		return math.Max(cpu*0.75, 4)
	case MetricIO:
		return math.Max(cpu*0.5, 4)
	case MetricLock:
		return 3
	case MetricLongSQL:
		return 2
	case MetricRedoRate:
		return 5000 // KB/s
	case MetricHardParse:
		return math.Max(cpu*3, 20)

	// --- Session/Load ---
	case MetricTotalSessions:
		return 0 // uses T6 capacity, not T1/T2
	case MetricPQSessions:
		return math.Max(cpu*0.5, 4)
	case MetricSessionCreationRate:
		return 10 // new sessions/s
	case MetricBackgroundWait:
		return 5
	case MetricOSLoad:
		return math.Max(cpu*2, 4)

	// --- Throughput ---
	case MetricPhysicalReadRate:
		return 1000 // reads/s
	case MetricLogicalReadRate:
		return 0 // uses T3 trend, not T1/T2
	case MetricCommitRate:
		return 0 // uses T9 absence, not T1/T2

	// --- Wait/Latency ---
	case MetricLogFileSyncUs:
		return 5000 // 5ms
	case MetricDbFileSeqReadUs:
		return 10000 // 10ms
	case MetricDbFileScatReadUs:
		return 15000 // 15ms
	case MetricEnqueueWaitMs:
		return 500 // ms
	case MetricLatchFreeRate:
		return 0 // uses T8
	case MetricNetworkRoundTrip:
		return 0 // uses T7

	// --- Memory/Cache (all use T6/T8) ---
	case MetricBufferCacheHit,
		MetricLibraryCacheHit,
		MetricPGAUsedPct,
		MetricSharedPoolFreePct:
		return 0

	// --- Storage (all use T6) ---
	case MetricTablespaceUsedPct,
		MetricTempUsedPct,
		MetricUndoUsedPct,
		MetricFRAUsedPct,
		MetricASMUsedPct:
		return 0

	// --- Redo/Archive ---
	case MetricLogSwitchRate:
		return 10 // switches/hour
	case MetricArchiveLagSec:
		return 60 // seconds
	case MetricStandbyApplyLag:
		return 120 // seconds
	case MetricRedoLogSpaceWait:
		return 1 // requests

	// --- Lock/Concurrency ---
	case MetricBlockingChains:
		return 2 // chain depth
	case MetricEnqueueDeadlocks:
		return 1 // any deadlock
	case MetricMutexWait:
		return 3 // sessions
	case MetricRowLockWaitMs:
		return 1000 // ms

	// --- SQL Performance ---
	case MetricTopSQLDrift:
		return 0 // uses T7
	case MetricFullScanRate:
		return 0 // uses T3
	case MetricPlanChangeCount:
		return 3
	case MetricSQLThrottle:
		return 1

	// --- System/Pattern ---
	case MetricInstanceStatus:
		return 0 // uses T5
	case MetricDataGuardStatus:
		return 0 // uses T5
	case MetricAlertLogErrors:
		return 1 // errors/min
	case MetricJobFailureRate:
		return 0 // uses T1 with baseline, not absolute
	case MetricResourceLimitPct:
		return 0 // uses T6
	case MetricCheckpointNotComplete:
		return 1 // per hour

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
