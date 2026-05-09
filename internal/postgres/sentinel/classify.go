/*-------------------------------------------------------------------------
 *
 * classify.go
 *	  Classify applies deterministic rules to determine the root cause
 *	  of a PG anomaly. Rules are evaluated in priority order; the first
 *	  match with sufficient confidence wins.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/classify.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "fmt"

// Classify applies deterministic rules to determine the root cause of a PG anomaly.
// Rules are evaluated in priority order; the first match with sufficient
// confidence wins.
func Classify(report BurstReport) Classification {
	// Standby detection: pg_is_in_recovery >= 1.0 means this is a standby.
	// Standby databases cannot VACUUM, so bloat/vacuum/autovacuum/xid alerts
	// are not actionable. Route to standby-specific classification.
	if metricMax(report, string(MetricPGIsInRecovery)) >= 1.0 {
		return classifyStandby(report)
	}

	// ── Primary classification rules (original order) ──

	// Rule 1: Lock contention (highest priority).
	if c := classifyLockContention(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 2: Single slow SQL dominates.
	if c := classifySlowQuery(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 3: Vacuum lag (dead tuple ratio high).
	if c := classifyVacuumLag(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 4: XID wraparound risk.
	if c := classifyXIDWraparound(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 5: WAL bottleneck.
	if c := classifyWALBottleneck(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 6: Connection storm.
	if c := classifyConnectionStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 7: Replication lag.
	if c := classifyReplicationLag(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 8: Checkpoint storm.
	if c := classifyCheckpointStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 9: IO subsystem (NEW).
	if c := classifyIOSubsystem(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 10: Memory pressure (NEW).
	if c := classifyMemoryPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 11: Temp spill (NEW).
	if c := classifyTempSpill(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 12: Bloat pressure (NEW).
	if c := classifyBloatPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Rule 13: Autovacuum lag (NEW).
	if c := classifyAutovacuumLag(report); c.Confidence > 0.3 {
		return c
	}

	// Rules 14-30: Extended classification rules.
	if c := classifyExtended(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 31: Database hang (elimination — nothing above matched) (NEW).
	if c := classifyDatabaseHang(report); c.Confidence > 0.3 {
		return c
	}

	// Fallback: infer from trigger metric.
	if c := classifyFromTrigger(report.TriggerEvent); c.Confidence > 0 {
		return c
	}

	return Classification{Cause: CauseUnknown, Confidence: 0}
}

// classifyStandby applies classification rules appropriate for standby databases.
// Standby databases are in recovery mode (pg_is_in_recovery = true) and cannot
// run VACUUM, so bloat_pressure, vacuum_lag, autovacuum_lag, and xid_wraparound
// are skipped as they are not actionable on a standby.
//
// Priority order: replication_lag > lock_contention > connection_storm >
// io_subsystem > slow_query > memory_pressure > temp_spill > checkpoint_storm >
// wal_bottleneck > database_hang.
func classifyStandby(report BurstReport) Classification {
	// Standby priority 1: Replication lag (most critical for standby health).
	if c := classifyReplicationLag(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 2: Lock contention.
	if c := classifyLockContention(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 3: Connection storm.
	if c := classifyConnectionStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 4: IO subsystem.
	if c := classifyIOSubsystem(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 5: Slow query.
	if c := classifySlowQuery(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 6: Memory pressure.
	if c := classifyMemoryPressure(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 7: Temp spill.
	if c := classifyTempSpill(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 8: Checkpoint storm.
	if c := classifyCheckpointStorm(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 9: WAL bottleneck.
	if c := classifyWALBottleneck(report); c.Confidence > 0.3 {
		return c
	}
	// Standby priority 10: Database hang (elimination).
	if c := classifyDatabaseHang(report); c.Confidence > 0.3 {
		return c
	}

	// Fallback: infer from trigger metric.
	if c := classifyFromTrigger(report.TriggerEvent); c.Confidence > 0 {
		return c
	}

	return Classification{Cause: CauseUnknown, Confidence: 0}
}

func classifyLockContention(r BurstReport) Classification {
	lockWaits := metricMax(r, string(MetricLockWaitSessions))
	blockers := metricMax(r, string(MetricBlockerCount))

	if lockWaits < 3 && blockers < 1 {
		return Classification{}
	}

	confidence := 0.6
	if lockWaits >= 5 || blockers >= 2 {
		confidence = 0.8
	}

	evidence := []string{
		formatEvidence("锁等待会话峰值: %.0f", lockWaits),
	}
	if blockers > 0 {
		evidence = append(evidence,
			formatEvidence("阻塞者数: %.0f", blockers))
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifySlowQuery(r BurstReport) Classification {
	longQueries := metricMax(r, string(MetricLongQueries))
	active := metricMax(r, string(MetricActiveSessions))

	if longQueries < 2 {
		return Classification{}
	}

	confidence := 0.5
	if longQueries >= 3 && active > 0 && longQueries/active > 0.5 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("慢查询(>30s)峰值: %.0f, 活跃会话: %.0f", longQueries, active),
		},
	}
}

func classifyVacuumLag(r BurstReport) Classification {
	deadRatio := metricMax(r, string(MetricDeadTupleRatio))

	if deadRatio < 20 {
		return Classification{}
	}

	confidence := 0.6
	if deadRatio >= 50 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseVacuumLag,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("最大死元组比例: %.1f%%", deadRatio),
		},
	}
}

func classifyXIDWraparound(r BurstReport) Classification {
	xidAge := metricMax(r, string(MetricXIDAgeRatio))

	if xidAge < 50 {
		return Classification{}
	}

	confidence := 0.7
	if xidAge >= 80 {
		confidence = 0.95
	}

	return Classification{
		Cause:      CauseXIDWraparound,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("XID 年龄已达 %.1f%% (2^31上限)", xidAge),
		},
	}
}

func classifyWALBottleneck(r BurstReport) Classification {
	walRate := metricMax(r, string(MetricWALBytesRate))

	if walRate < 100*1024*1024 {
		return Classification{}
	}

	confidence := 0.6
	if walRate >= 500*1024*1024 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseWALBottleneck,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("WAL 生成速率: %.1f MB/s", walRate/1024/1024),
		},
	}
}

func classifyConnectionStorm(r BurstReport) Classification {
	connPct := metricMax(r, string(MetricConnectionsPct))

	if connPct < 80 {
		return Classification{}
	}

	confidence := 0.7
	if connPct >= 95 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseConnectionStorm,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("连接使用率: %.1f%%", connPct),
		},
	}
}

func classifyReplicationLag(r BurstReport) Classification {
	lag := metricMax(r, string(MetricReplicationLag))

	if lag < 10 {
		return Classification{}
	}

	confidence := 0.6
	if lag >= 60 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseReplicationLag,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("复制延迟: %.1f 秒", lag),
		},
	}
}

func classifyCheckpointStorm(r BurstReport) Classification {
	checkpoints := metricMax(r, string(MetricCheckpointsReq))

	if checkpoints < 10 {
		return Classification{}
	}

	confidence := 0.6
	if checkpoints >= 50 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseCheckpointStorm,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("请求 Checkpoint 数: %.0f", checkpoints),
		},
	}
}

// ── New rules ──

func classifyIOSubsystem(r BurstReport) Classification {
	ioWait := metricMax(r, string(MetricIOWaitSessions))
	active := metricMax(r, string(MetricActiveSessions))

	if ioWait < 3 {
		return Classification{}
	}

	// IO wait should be a significant fraction of active sessions.
	if active > 0 && ioWait/active < 0.3 {
		return Classification{}
	}

	confidence := 0.5
	if ioWait >= 5 {
		confidence = 0.7
	}
	if active > 0 && ioWait/active > 0.5 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseIOSubsystem,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("I/O 等待会话: %.0f (活跃 %.0f)", ioWait, active),
		},
	}
}

func classifyMemoryPressure(r BurstReport) Classification {
	cacheHit := metricMax(r, string(MetricCacheHitPct))
	sharedBuf := metricMax(r, string(MetricSharedBufferPct))

	// Cache hit drops below 90% or shared buffer usage very high.
	if cacheHit > 90 && sharedBuf < 90 {
		return Classification{}
	}

	confidence := 0.0
	evidence := []string{}

	if cacheHit > 0 && cacheHit < 90 {
		confidence += 0.4
		evidence = append(evidence, formatEvidence("缓存命中率: %.1f%%", cacheHit))
	}
	if sharedBuf >= 90 {
		confidence += 0.3
		evidence = append(evidence, formatEvidence("Shared Buffer使用率: %.1f%%", sharedBuf))
	}

	if confidence < 0.4 {
		return Classification{}
	}

	return Classification{
		Cause:      CauseMemoryPressure,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyTempSpill(r BurstReport) Classification {
	tempRate := metricMax(r, string(MetricTempBytesRate))
	tempFiles := metricMax(r, string(MetricTempFilesRate))

	// > 50 MB/s temp writes.
	if tempRate < 52428800 && tempFiles < 5 {
		return Classification{}
	}

	confidence := 0.5
	if tempRate > 209715200 { // > 200 MB/s
		confidence = 0.75
	}

	evidence := []string{}
	if tempRate > 0 {
		evidence = append(evidence, formatEvidence("临时空间写入: %.1f MB/s", tempRate/1024/1024))
	}
	if tempFiles > 0 {
		evidence = append(evidence, formatEvidence("临时文件生成: %.1f/s", tempFiles))
	}
	evidence = append(evidence, "work_mem 可能不足，排序/哈希溢出到磁盘")

	return Classification{
		Cause:      CauseTempSpill,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyBloatPressure(r BurstReport) Classification {
	bloat := metricMax(r, string(MetricTableBloatPct))
	deadRatio := metricMax(r, string(MetricDeadTupleRatio))

	if bloat < 40 && deadRatio < 30 {
		return Classification{}
	}

	confidence := 0.5
	if bloat >= 60 || deadRatio >= 50 {
		confidence = 0.75
	}

	evidence := []string{}
	if bloat > 0 {
		evidence = append(evidence, formatEvidence("表膨胀: %.1f%%", bloat))
	}
	if deadRatio > 0 {
		evidence = append(evidence, formatEvidence("死元组比例: %.1f%%", deadRatio))
	}

	return Classification{
		Cause:      CauseBloatPressure,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyAutovacuumLag(r BurstReport) Classification {
	workers := metricMax(r, string(MetricAutovacuumWorkers))
	xidAge := metricMax(r, string(MetricXIDAgeRatio))
	oldestXact := metricMax(r, string(MetricOldestXactAgeSec))

	if workers < 2 && xidAge < 30 {
		return Classification{}
	}

	confidence := 0.0
	evidence := []string{}

	if workers >= 3 {
		confidence += 0.4
		evidence = append(evidence, formatEvidence("Autovacuum 工作进程: %.0f (满载)", workers))
	}
	if xidAge >= 30 {
		confidence += 0.3
		evidence = append(evidence, formatEvidence("XID 年龄: %.1f%%", xidAge))
	}
	if oldestXact > 3600 {
		confidence += 0.2
		evidence = append(evidence, formatEvidence("最老事务: %.0f 秒", oldestXact))
	}

	if confidence < 0.4 {
		return Classification{}
	}

	return Classification{
		Cause:      CauseAutovacuumLag,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

func classifyDatabaseHang(r BurstReport) Classification {
	active := metricMax(r, string(MetricActiveSessions))
	commitRate := metricMax(r, string(MetricXactCommitRate))
	cpuSessions := metricMax(r, string(MetricCPUSessions))

	// High active sessions but zero commit rate and zero CPU sessions.
	if active < 10 || commitRate > 5 || cpuSessions > 2 {
		return Classification{}
	}

	confidence := 0.5
	if active > 30 && commitRate == 0 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseDatabaseHang,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("活跃会话 %.0f 但提交速率 %.1f/s, CPU会话 %.0f", active, commitRate, cpuSessions),
			"大量会话活跃但无进展，疑似数据库 Hang",
		},
	}
}

func classifyFromTrigger(trigger TriggerEvent) Classification {
	if trigger.Metric == "" {
		return Classification{}
	}

	causeMap := map[MetricName]RootCauseType{
		MetricLockWaitSessions:      CauseLockContention,
		MetricLongQueries:           CauseSlowQuery,
		MetricDeadTupleRatio:        CauseVacuumLag,
		MetricXIDAgeRatio:           CauseXIDWraparound,
		MetricWALBytesRate:          CauseWALBottleneck,
		MetricConnectionsPct:        CauseConnectionStorm,
		MetricReplicationLag:        CauseReplicationLag,
		MetricCheckpointsReq:        CauseCheckpointStorm,
		MetricActiveSessions:        CauseSlowQuery,
		MetricCPUSessions:           CauseSlowQuery,
		MetricIOWaitSessions:        CauseIOSubsystem,
		MetricIdleInTransaction:     CauseLockContention,
		MetricBlockerCount:          CauseLockContention,
		MetricDeadlocks:             CauseLockContention,
		MetricCacheHitPct:           CauseMemoryPressure,
		MetricTempBytesRate:         CauseTempSpill,
		MetricXactCommitRate:        CauseSlowQuery,
		MetricLWLockWaitSessions:    CauseLockContention,
		MetricTableBloatPct:         CauseBloatPressure,
		MetricAutovacuumWorkers:     CauseAutovacuumLag,
		MetricSharedBufferPct:       CauseMemoryPressure,
		MetricArchiveFailCnt:        CauseWALBottleneck,
		MetricOldestXactAgeSec:      CauseAutovacuumLag,
		MetricBackendErrorRate:      CauseSlowQuery,
		MetricXactRollbackRate:      CauseSlowQuery,
		MetricRowExclusiveLocks:     CauseLockContention,
		MetricPGIsInRecovery:        CauseReplicationLag,
		MetricBufferPinWaitSessions: CauseIOSubsystem,
		MetricTablespaceUsedPct:     CauseBloatPressure,
		MetricTempFilesRate:         CauseTempSpill,
	}

	cause, ok := causeMap[MetricName(trigger.Metric)]
	if !ok {
		return Classification{}
	}

	return Classification{
		Cause:      cause,
		Confidence: 0.4,
		Evidence: []string{
			formatEvidence("基于触发指标推断: %s 从 %.1f -> %.1f (阈值 %.1f)",
				trigger.Metric, trigger.Baseline, trigger.Current, trigger.Threshold),
		},
	}
}

// metricMax returns the max value for a metric across burst frames.
func metricMax(r BurstReport, key string) float64 {
	if m, ok := r.Metrics[key]; ok {
		return m.Max
	}
	return 0
}

// formatEvidence is a helper to build evidence strings.
func formatEvidence(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
