/*-------------------------------------------------------------------------
 *
 * classify_new.go
 *	  Anomaly classifier (new) for Oracle Sentinel — turns raw
 *	  probe deltas into typed events the engine routes to the right
 *	  diagnosis path.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/classify_new.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// classify_new.go — 7 new classification functions for root cause analysis.
// Each returns Classification{Cause, Confidence, Evidence}.

// classifyPlanDrift detects execution plan drift: a single SQL has high elapsed
// time with low concurrency, and its plan_hash_value has changed over time.
// Distinguished from CauseBadSQL which requires high concurrency (>=3).
func classifyPlanDrift(r BurstReport) Classification {
	if len(r.TopSQLs) == 0 {
		return Classification{}
	}

	top := r.TopSQLs[0]
	// Plan drift: moderate occurrence rate but NOT high concurrency
	if top.OccurrenceRate < 0.3 || top.MaxConcurrent >= 5 {
		return Classification{}
	}

	// Evidence 1: Multiple plan_hash_values for the same sql_id
	if plans, ok := r.PlanHistory[top.SQLID]; ok && len(plans) >= 2 {
		return Classification{
			Cause:      CausePlanDrift,
			Confidence: 0.8,
			Evidence: []string{
				formatEvidence("SQL %s 有 %d 个不同执行计划, 疑似计划漂移",
					top.SQLID, len(plans)),
				formatEvidence("最大耗时 %.1fs, 峰值并发 %d (低并发高耗时模式)",
					top.MaxElapsedSec, top.MaxConcurrent),
			},
		}
	}

	// Evidence 2: Single plan but high elapsed with very low concurrency
	if top.MaxElapsedSec > 30 && top.MaxConcurrent <= 2 {
		return Classification{
			Cause:      CausePlanDrift,
			Confidence: 0.6,
			Evidence: []string{
				formatEvidence("SQL %s 耗时 %.1fs, 并发仅 %d, 疑似计划退化",
					top.SQLID, top.MaxElapsedSec, top.MaxConcurrent),
			},
		}
	}

	return Classification{}
}

// classifyMemoryPressure detects memory-level contention: buffer busy waits,
// free buffer waits, or latch: cache buffers chains in WaitProfile, combined
// with high PGA/Shared Pool usage in Metrics.
// Distinguished from IO: IO is disk-level, Memory is cache-level.
func classifyMemoryPressure(r BurstReport) Classification {
	memWaitPct := waitEventPct(r.WaitProfile, "buffer busy waits") +
		waitEventPct(r.WaitProfile, "free buffer waits") +
		waitEventPct(r.WaitProfile, "latch: cache buffers chains")

	if memWaitPct < 10 {
		return Classification{}
	}

	confidence := 0.5
	evidence := []string{
		formatEvidence("内存相关等待事件占 %.1f%% (buffer busy/free buffer/cache buffers chains)", memWaitPct),
	}

	// Boost confidence with metric evidence
	if pga, ok := r.Metrics[string(MetricPGAUsedPct)]; ok && pga.Max > 85 {
		confidence += 0.15
		evidence = append(evidence,
			formatEvidence("PGA 使用率 %.1f%% (高)", pga.Max))
	}
	if sp, ok := r.Metrics[string(MetricSharedPoolFreePct)]; ok && sp.Min < 10 {
		confidence += 0.15
		evidence = append(evidence,
			formatEvidence("Shared Pool 空闲率仅 %.1f%% (低)", sp.Min))
	}

	if memWaitPct > 25 {
		confidence += 0.1
	}

	if confidence < 0.5 {
		confidence = 0.5
	}
	if confidence > 0.9 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseMemoryPressure,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// classifyConnectionExhaust detects when total sessions approach the hard limit.
// Distinguished from TrafficStorm: TrafficStorm = active sessions spike,
// ConnectionExhaust = total sessions near hard limit from v$resource_limit.
func classifyConnectionExhaust(r BurstReport) Classification {
	// Check ResourceLimits for sessions approaching limit
	for _, rl := range r.ResourceLimits {
		if rl.ResourceName != "sessions" {
			continue
		}
		if rl.LimitValue <= 0 {
			continue
		}
		usedPct := rl.CurrentUtilization / rl.LimitValue * 100
		if usedPct < 85 {
			continue
		}

		confidence := 0.6
		if usedPct > 95 {
			confidence = 0.85
		} else if usedPct > 90 {
			confidence = 0.75
		}

		return Classification{
			Cause:      CauseConnectionExhaust,
			Confidence: confidence,
			Evidence: []string{
				formatEvidence("会话数 %.0f/%.0f (%.1f%%), 逼近硬限制",
					rl.CurrentUtilization, rl.LimitValue, usedPct),
			},
		}
	}

	// Fallback: check resource_limit_pct metric
	if rlm, ok := r.Metrics[string(MetricResourceLimitPct)]; ok && rlm.Max > 85 {
		confidence := 0.55
		if rlm.Max > 95 {
			confidence = 0.75
		}
		return Classification{
			Cause:      CauseConnectionExhaust,
			Confidence: confidence,
			Evidence: []string{
				formatEvidence("资源限制使用率 %.1f%%, 接近上限", rlm.Max),
			},
		}
	}

	return Classification{}
}

// classifyArchiveDelay detects archive log destination full / archiver hung.
// Triggered by "log file switch (archiving needed)" wait event.
// Distinguished from Redo: Redo is "log file sync", Archive is "archiving needed".
func classifyArchiveDelay(r BurstReport) Classification {
	archPct := waitEventPct(r.WaitProfile, "log file switch (archiving needed)")
	if archPct < 5 {
		return Classification{}
	}

	confidence := 0.7
	evidence := []string{
		formatEvidence("\"log file switch (archiving needed)\" 等待占 %.1f%%", archPct),
	}

	if archPct > 20 {
		confidence = 0.85
	}

	// Boost if FRA usage is high
	for _, sd := range r.SpaceDetails {
		if sd.UsedPct > 90 {
			confidence += 0.1
			evidence = append(evidence,
				formatEvidence("FRA空间 %s 已用 %.1f%%", sd.Name, sd.UsedPct))
			break
		}
	}

	if fra, ok := r.Metrics[string(MetricFRAUsedPct)]; ok && fra.Max > 90 {
		confidence += 0.05
		evidence = append(evidence,
			formatEvidence("FRA 使用率 %.1f%%", fra.Max))
	}

	if confidence > 0.95 {
		confidence = 0.95
	}

	return Classification{
		Cause:      CauseArchiveDelay,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// classifyDGSyncDelay detects Data Guard synchronization delay.
// Triggered by high "log file sync" combined with standby apply lag.
// Distinguished from Redo: DG has standby lag evidence.
func classifyDGSyncDelay(r BurstReport) Classification {
	// Require log file sync wait to be notable
	syncPct := waitEventPct(r.WaitProfile, "log file sync")
	if syncPct < 15 {
		return Classification{}
	}

	// Check dataguard_status metric — 0 = OK, non-zero = abnormal
	dgStatus, hasDG := r.Metrics[string(MetricDataGuardStatus)]
	lagMetric, hasLag := r.Metrics[string(MetricStandbyApplyLag)]

	// Need at least one DG indicator
	dgAbnormal := hasDG && dgStatus.Max > 0
	lagHigh := hasLag && lagMetric.Max > 120

	if !dgAbnormal && !lagHigh {
		return Classification{}
	}

	confidence := 0.6
	evidence := []string{
		formatEvidence("\"log file sync\" 等待占 %.1f%%", syncPct),
	}

	if dgAbnormal {
		confidence += 0.15
		evidence = append(evidence,
			formatEvidence("DataGuard 状态异常 (status=%.0f)", dgStatus.Max))
	}
	if lagHigh {
		confidence += 0.15
		evidence = append(evidence,
			formatEvidence("备库同步延迟 %.0f 秒", lagMetric.Max))
	}

	if confidence > 0.9 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseDGSyncDelay,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// classifyUndoPressure detects undo tablespace pressure.
// Triggered by "enq: US - contention" wait event and/or high undo usage.
func classifyUndoPressure(r BurstReport) Classification {
	usPct := waitEventPct(r.WaitProfile, "enq: US - contention")

	undoMetric, hasUndo := r.Metrics[string(MetricUndoUsedPct)]
	undoHigh := hasUndo && undoMetric.Max > 85

	if usPct < 3 && !undoHigh {
		return Classification{}
	}

	confidence := 0.0
	evidence := []string{}

	if usPct >= 3 {
		confidence += 0.5
		evidence = append(evidence,
			formatEvidence("\"enq: US - contention\" 等待占 %.1f%%", usPct))
	}

	if undoHigh {
		confidence += 0.3
		evidence = append(evidence,
			formatEvidence("Undo 表空间使用率 %.1f%%", undoMetric.Max))
	}

	if confidence > 0.9 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseUndoPressure,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// classifyTempPressure detects TEMP tablespace exhaustion.
// Triggered by "direct path read temp" or "direct path write temp" wait events
// combined with high temp usage.
func classifyTempPressure(r BurstReport) Classification {
	tempReadPct := waitEventPct(r.WaitProfile, "direct path read temp")
	tempWritePct := waitEventPct(r.WaitProfile, "direct path write temp")
	tempWaitPct := tempReadPct + tempWritePct

	tempMetric, hasTemp := r.Metrics[string(MetricTempUsedPct)]
	tempHigh := hasTemp && tempMetric.Max > 85

	// Also check SpaceDetails for TEMP
	for _, sd := range r.SpaceDetails {
		if sd.Name == "TEMP" && sd.UsedPct > 85 {
			tempHigh = true
			break
		}
	}

	if tempWaitPct < 10 && !tempHigh {
		return Classification{}
	}

	confidence := 0.0
	evidence := []string{}

	if tempWaitPct >= 10 {
		confidence += 0.5
		evidence = append(evidence,
			formatEvidence("TEMP 相关等待占 %.1f%% (direct path read/write temp)", tempWaitPct))
	}

	if tempHigh {
		confidence += 0.3
		if hasTemp {
			evidence = append(evidence,
				formatEvidence("TEMP 表空间使用率 %.1f%%", tempMetric.Max))
		} else {
			evidence = append(evidence, "TEMP 表空间使用率 >85%")
		}
	}

	if confidence > 0.9 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseTempPressure,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// waitEventPct returns the percentage for a specific wait event name in the WaitProfile.
func waitEventPct(profile []WaitBucket, event string) float64 {
	for _, wb := range profile {
		if wb.Event == event {
			return wb.Percentage
		}
	}
	return 0
}
