/*-------------------------------------------------------------------------
 *
 * classify.go
 *	  Classify applies deterministic rules to determine the root cause.
 *	  Rules are evaluated in priority order; the first match with
 *	  sufficient confidence wins. Falls back to TriggerEvent inference
 *	  when burst data is insufficient.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/classify.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "fmt"

// Classify applies deterministic rules to determine the root cause.
// Rules are evaluated in priority order; the first match with sufficient
// confidence wins. Falls back to TriggerEvent inference when burst data
// is insufficient.
func Classify(report BurstReport) Classification {
	// Rule 1: Lock contention (highest — blocking chain is deterministic evidence)
	if c := classifyLockContention(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 2: Bad SQL (single SQL high concurrency)
	if c := classifyBadSQL(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 3: Plan drift (single SQL low concurrency high elapsed) [NEW]
	if c := classifyPlanDrift(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 4: Redo bottleneck
	if c := classifyRedoBottleneck(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 5: Archive delay [NEW]
	if c := classifyArchiveDelay(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 6: DG sync delay [NEW]
	if c := classifyDGSyncDelay(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 7: Latch storm
	if c := classifyLatchStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 8: IO subsystem
	if c := classifyIOSubsystem(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 9: Memory pressure [NEW]
	if c := classifyMemoryPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 10: Connection exhaust [NEW]
	if c := classifyConnectionExhaust(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 11: Undo pressure [NEW]
	if c := classifyUndoPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 12: TEMP pressure [NEW]
	if c := classifyTempPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 13: Traffic storm
	if c := classifyTrafficStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 14: Database Hang (elimination — nothing above matched) [NEW]
	if c := classifyDatabaseHang(report); c.Confidence > 0.3 {
		return c
	}
	// Fallback: infer from trigger metric when burst data is insufficient.
	if c := classifyFromTrigger(report.TriggerEvent); c.Confidence > 0 {
		return c
	}
	return Classification{Cause: CauseUnknown, Confidence: 0}
}

// classifyFromTrigger infers root cause from the trigger metric when
// burst analysis data is insufficient for rule-based classification.
func classifyFromTrigger(trigger TriggerEvent) Classification {
	if trigger.Metric == "" {
		return Classification{}
	}

	var cause RootCauseType
	switch MetricName(trigger.Metric) {
	case MetricLock:
		cause = CauseLockContention
	case MetricCPU:
		cause = CauseBadSQL
	case MetricIO:
		cause = CauseIOSubsystem
	case MetricRedoRate:
		cause = CauseRedoBottleneck
	case MetricHardParse:
		cause = CauseLatchStorm
	case MetricLongSQL:
		cause = CauseBadSQL
	case MetricActive:
		cause = CauseTrafficStorm
	case MetricPlanChangeCount, MetricTopSQLDrift:
		cause = CausePlanDrift
	case MetricPGAUsedPct, MetricBufferCacheHit:
		cause = CauseMemoryPressure
	case MetricResourceLimitPct, MetricTotalSessions:
		cause = CauseConnectionExhaust
	case MetricArchiveLagSec, MetricFRAUsedPct:
		cause = CauseArchiveDelay
	case MetricStandbyApplyLag, MetricDataGuardStatus:
		cause = CauseDGSyncDelay
	case MetricUndoUsedPct:
		cause = CauseUndoPressure
	case MetricTempUsedPct:
		cause = CauseTempPressure
	case MetricInstanceStatus:
		cause = CauseDatabaseHang
	default:
		return Classification{}
	}

	return Classification{
		Cause:      cause,
		Confidence: 0.4,
		Evidence: []string{
			formatEvidence("基于触发指标推断: %s 从 %.1f→%.1f (阈值 %.1f)",
				trigger.Metric, trigger.Baseline, trigger.Current, trigger.Threshold),
		},
	}
}

func classifyLockContention(r BurstReport) Classification {
	if len(r.BlockingChains) == 0 {
		return Classification{}
	}
	maxVictims := 0
	for _, chain := range r.BlockingChains {
		if chain.VictimCount > maxVictims {
			maxVictims = chain.VictimCount
		}
	}
	if maxVictims < 2 {
		return Classification{}
	}

	confidence := 0.7
	if maxVictims >= 5 {
		confidence = 0.9
	}

	evidence := []string{}
	for _, chain := range r.BlockingChains {
		evidence = append(evidence,
			formatEvidence("SID %d 阻塞 %d 个会话, SQL: %s, 等待: %s",
				chain.RootSID, chain.VictimCount, chain.RootSQLID, chain.RootEvent))
	}
	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyBadSQL(r BurstReport) Classification {
	if len(r.TopSQLs) == 0 {
		return Classification{}
	}
	top := r.TopSQLs[0]
	if top.OccurrenceRate < 0.5 || top.MaxConcurrent < 3 {
		return Classification{}
	}

	confidence := 0.6
	if top.OccurrenceRate > 0.7 && top.MaxConcurrent >= 5 {
		confidence = 0.8
	}

	waitInfo := top.Event
	if waitInfo == "" {
		waitInfo = top.WaitClass
	}
	return Classification{
		Cause:      CauseBadSQL,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("SQL %s 耗时 %.1fs, 峰值 %d 并发",
				top.SQLID, top.MaxElapsedSec, top.MaxConcurrent),
			formatEvidence("等待: %s", waitInfo),
		},
	}
}

// waitClassTotal sums percentages for a given wait class across event-level WaitProfile.
func waitClassTotal(profile []WaitBucket, class string) float64 {
	total := 0.0
	for _, wb := range profile {
		if wb.WaitClass == class {
			total += wb.Percentage
		}
	}
	return total
}

func classifyRedoBottleneck(r BurstReport) Classification {
	commitPct := waitClassTotal(r.WaitProfile, "Commit")
	if commitPct < 20 {
		return Classification{}
	}

	confidence := 0.7
	if commitPct > 40 {
		confidence = 0.85
	}
	return Classification{
		Cause:      CauseRedoBottleneck,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("Commit 类等待占 %.1f%%", commitPct),
		},
	}
}

func classifyLatchStorm(r BurstReport) Classification {
	concPct := waitClassTotal(r.WaitProfile, "Concurrency")
	if concPct < 25 {
		return Classification{}
	}

	distinctSQLs := len(r.TopSQLs)
	confidence := 0.6
	if concPct > 40 && distinctSQLs > 10 {
		confidence = 0.8
	}
	evidence := []string{
		formatEvidence("Concurrency 类等待占 %.1f%%", concPct),
	}
	if distinctSQLs > 10 {
		evidence = append(evidence,
			formatEvidence("%d 个不同 SQL_ID, 疑似硬解析冲高", distinctSQLs))
	}
	return Classification{
		Cause:      CauseLatchStorm,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyIOSubsystem(r BurstReport) Classification {
	ioTotal := waitClassTotal(r.WaitProfile, "User I/O") +
		waitClassTotal(r.WaitProfile, "System I/O")
	if ioTotal < 40 {
		return Classification{}
	}

	sqlScattered := len(r.TopSQLs) == 0 ||
		(len(r.TopSQLs) > 0 && r.TopSQLs[0].OccurrenceRate < 0.5)

	confidence := 0.5
	if ioTotal > 60 && sqlScattered {
		confidence = 0.7
	}
	if ioTotal > 70 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseIOSubsystem,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("I/O 等待占 %.1f%%", ioTotal),
		},
	}
}

func classifyTrafficStorm(r BurstReport) Classification {
	activeMetric, ok := r.Metrics[string(MetricActive)]
	if !ok {
		return Classification{}
	}

	// Check if active sessions spiked significantly
	if activeMetric.Trend != "spike" {
		return Classification{}
	}

	// Also check: no single SQL dominates and no blocking
	noBlocker := len(r.BlockingChains) == 0
	sqlScattered := len(r.TopSQLs) == 0 ||
		(len(r.TopSQLs) > 0 && r.TopSQLs[0].OccurrenceRate < 0.3)

	if !noBlocker || !sqlScattered {
		return Classification{}
	}

	confidence := 0.5
	if r.PeakActive > 0 && r.BaselineActive > 0 {
		ratio := float64(r.PeakActive) / r.BaselineActive
		if ratio > 5 {
			confidence = 0.7
		}
	}

	return Classification{
		Cause:      CauseTrafficStorm,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("活跃会话突增: 峰值 %d (基线 %.1f, %.1f倍)",
				r.PeakActive, r.BaselineActive, float64(r.PeakActive)/max(r.BaselineActive, 1)),
			formatEvidence("SQL 分散, 无明显阻塞链"),
		},
	}
}

// formatEvidence is a helper to build evidence strings.
func formatEvidence(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

// max returns the larger of two float64 values.
func max(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
