/*-------------------------------------------------------------------------
 *
 * classify_extended.go
 *	  Anomaly classifier (extended) for PostgreSQL Sentinel — turns raw
 *	  probe deltas into typed events the engine routes to the right
 *	  diagnosis path.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sentinel/classify_extended.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// classifyExtended runs additional classification rules beyond the core 8.
// Returns a Classification with confidence > 0 if a match is found.
func classifyExtended(report BurstReport) Classification {
	// Rule 9: Deadlock detected.
	if c := classifyDeadlock(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 10: Idle in transaction blocking.
	if c := classifyIdleInTransaction(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 11: Dead tuple ratio critical.
	if c := classifyDeadTupleCritical(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 12: XID age + vacuum compound.
	if c := classifyXIDVacuumCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 13: Temp spill heavy.
	if c := classifyTempSpillHeavy(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 14: Cache hit + slow query compound.
	if c := classifyCacheSlowQueryCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 15: Active sessions spike.
	if c := classifyActiveSessionSpike(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 16: WAL + checkpoint compound.
	if c := classifyWALCheckpointCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 17: Lock + slow query compound.
	if c := classifyLockSlowQueryCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 18: Connection storm + lock compound.
	if c := classifyConnectionLockCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 19: IO wait dominant in wait profile.
	if c := classifyIOWaitDominant(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 20: Lock wait dominant in wait profile.
	if c := classifyLockWaitProfileDominant(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 21: Replication lag + WAL compound.
	if c := classifyReplicationWALCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 22: Multiple slow SQLs.
	if c := classifyMultipleSlowSQLs(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 23: Blocker chain deep.
	if c := classifyBlockerChainDeep(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 24: Seq scan heavy with low cache hit.
	if c := classifySeqScanCacheCompound(report); c.Confidence > 0.3 {
		return c
	}

	return Classification{}
}

// ── Rule 9: Deadlock ──

func classifyDeadlock(r BurstReport) Classification {
	deadlocks := metricMax(r, string(MetricDeadlocks))
	if deadlocks <= 0 {
		return Classification{}
	}

	confidence := 0.7
	if deadlocks > 1 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("检测到死锁: %.0f 次", deadlocks),
			"死锁由交叉事务引起，建议检查事务中的 SQL 执行顺序",
		},
	}
}

// ── Rule 10: Idle in transaction ──

func classifyIdleInTransaction(r BurstReport) Classification {
	idleIT := metricMax(r, string(MetricIdleInTransaction))
	if idleIT < 5 {
		return Classification{}
	}

	confidence := 0.5
	if idleIT >= 10 {
		confidence = 0.7
	}
	if idleIT >= 20 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("Idle in Transaction 会话: %.0f 个", idleIT),
			"大量会话处于 idle in transaction 状态，持有锁阻止 VACUUM 和其他操作",
		},
	}
}

// ── Rule 11: Dead tuple ratio critical ──

func classifyDeadTupleCritical(r BurstReport) Classification {
	deadRatio := metricMax(r, string(MetricDeadTupleRatio))
	xidAge := metricMax(r, string(MetricXIDAgeRatio))

	// Only trigger if dead ratio is very high AND not already caught by vacuumLag rule.
	if deadRatio < 40 {
		return Classification{}
	}

	confidence := 0.7
	if deadRatio > 60 && xidAge > 30 {
		confidence = 0.9
	}

	evidence := []string{
		formatEvidence("死元组比例 %.1f%%", deadRatio),
	}
	if xidAge > 0 {
		evidence = append(evidence, formatEvidence("XID 年龄 %.1f%%", xidAge))
	}
	evidence = append(evidence, "死元组严重堆积伴随 XID 老化，Autovacuum 可能完全失效")

	return Classification{
		Cause:      CauseVacuumLag,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// ── Rule 12: XID + vacuum compound ──

func classifyXIDVacuumCompound(r BurstReport) Classification {
	xidAge := metricMax(r, string(MetricXIDAgeRatio))
	deadRatio := metricMax(r, string(MetricDeadTupleRatio))
	idleIT := metricMax(r, string(MetricIdleInTransaction))

	if xidAge < 40 || deadRatio < 10 {
		return Classification{}
	}

	confidence := 0.65
	if idleIT > 5 {
		confidence = 0.8
	}

	evidence := []string{
		formatEvidence("XID 年龄 %.1f%%, 死元组 %.1f%%", xidAge, deadRatio),
	}
	if idleIT > 0 {
		evidence = append(evidence, formatEvidence("Idle in Transaction %.0f 个阻止 XID 推进", idleIT))
	}

	return Classification{
		Cause:      CauseXIDWraparound,
		Confidence: confidence,
		Evidence:   evidence,
	}
}

// ── Rule 13: Temp spill ──

func classifyTempSpillHeavy(r BurstReport) Classification {
	tempRate := metricMax(r, string(MetricTempBytesRate))

	// > 50MB/s temp writes.
	if tempRate < 52428800 {
		return Classification{}
	}

	confidence := 0.5
	if tempRate > 209715200 { // > 200MB/s
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("临时空间写入速率 %.1f MB/s", tempRate/1024/1024),
			"排序或哈希操作溢出到磁盘，work_mem 可能不足",
		},
	}
}

// ── Rule 14: Cache hit + slow query compound ──

func classifyCacheSlowQueryCompound(r BurstReport) Classification {
	cacheHit := metricMax(r, string(MetricCacheHitPct))
	longQueries := metricMax(r, string(MetricLongQueries))

	if cacheHit <= 0 || cacheHit > 90 || longQueries < 2 {
		return Classification{}
	}

	confidence := 0.55
	if cacheHit < 80 && longQueries > 5 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("缓存命中率 %.1f%%, 慢查询 %.0f 个", cacheHit, longQueries),
			"缓存命中率低且伴随慢查询，全表扫描导致大量磁盘读",
		},
	}
}

// ── Rule 15: Active session spike ──

func classifyActiveSessionSpike(r BurstReport) Classification {
	active := metricMax(r, string(MetricActiveSessions))
	lockWaits := metricMax(r, string(MetricLockWaits))

	if active < 20 || lockWaits > 5 {
		return Classification{} // Lock rule will catch it.
	}

	confidence := 0.4
	if active > 50 {
		confidence = 0.6
	}
	if active > 100 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("活跃会话峰值 %.0f", active),
			"活跃会话冲高但无明显锁等待，多个查询同时消耗资源",
		},
	}
}

// ── Rule 16: WAL + checkpoint compound ──

func classifyWALCheckpointCompound(r BurstReport) Classification {
	walRate := metricMax(r, string(MetricWALBytesRate))
	checkpoints := metricMax(r, string(MetricCheckpointsReq))

	if walRate < 52428800 || checkpoints < 5 {
		return Classification{}
	}

	confidence := 0.6
	if walRate > 209715200 && checkpoints > 20 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseWALBottleneck,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("WAL 速率 %.1f MB/s, Checkpoint %.0f 次", walRate/1024/1024, checkpoints),
			"WAL 生成速率高伴随频繁 Checkpoint，IO 压力大",
		},
	}
}

// ── Rule 17: Lock + slow query compound ──

func classifyLockSlowQueryCompound(r BurstReport) Classification {
	lockWaits := metricMax(r, string(MetricLockWaits))
	longQueries := metricMax(r, string(MetricLongQueries))

	if lockWaits < 2 || longQueries < 2 {
		return Classification{}
	}

	confidence := 0.6
	if lockWaits > 5 && longQueries > 5 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("锁等待 %.0f, 慢查询 %.0f", lockWaits, longQueries),
			"锁等待伴随慢查询，慢查询可能持锁时间过长导致其他查询等待",
		},
	}
}

// ── Rule 18: Connection storm + lock compound ──

func classifyConnectionLockCompound(r BurstReport) Classification {
	connPct := metricMax(r, string(MetricConnectionsPct))
	lockWaits := metricMax(r, string(MetricLockWaits))

	if connPct < 70 || lockWaits < 3 {
		return Classification{}
	}

	confidence := 0.6
	if connPct > 85 && lockWaits > 10 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseConnectionStorm,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("连接使用率 %.1f%%, 锁等待 %.0f", connPct, lockWaits),
			"连接数冲高伴随锁等待，锁阻塞导致连接无法释放",
		},
	}
}

// ── Rule 19: IO wait dominant ──

func classifyIOWaitDominant(r BurstReport) Classification {
	if len(r.WaitProfile) == 0 {
		return Classification{}
	}

	var ioWaitPct float64
	for _, w := range r.WaitProfile {
		if w.WaitEventType == "IO" {
			ioWaitPct += w.Percentage
		}
	}

	if ioWaitPct < 30 {
		return Classification{}
	}

	confidence := 0.5
	if ioWaitPct > 50 {
		confidence = 0.7
	}
	if ioWaitPct > 70 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("IO 等待占比 %.1f%%", ioWaitPct),
			"IO 等待占主导，查询产生大量磁盘读写",
		},
	}
}

// ── Rule 20: Lock wait profile dominant ──

func classifyLockWaitProfileDominant(r BurstReport) Classification {
	if len(r.WaitProfile) == 0 {
		return Classification{}
	}

	var lockPct float64
	for _, w := range r.WaitProfile {
		if w.WaitEventType == "Lock" {
			lockPct += w.Percentage
		}
	}

	if lockPct < 30 || len(r.BlockingChains) > 0 {
		return Classification{} // Blocking chain rule already handled.
	}

	confidence := 0.5
	if lockPct > 50 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("Lock 等待占比 %.1f%%", lockPct),
			"等待事件中锁等待占主导",
		},
	}
}

// ── Rule 21: Replication lag + WAL compound ──

func classifyReplicationWALCompound(r BurstReport) Classification {
	lag := metricMax(r, string(MetricReplicationLag))
	walRate := metricMax(r, string(MetricWALBytesRate))

	if lag < 5 || walRate < 52428800 {
		return Classification{}
	}

	confidence := 0.55
	if lag > 30 && walRate > 209715200 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseReplicationLag,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("复制延迟 %.1f 秒, WAL 速率 %.1f MB/s", lag, walRate/1024/1024),
			"WAL 生成速率高导致备库回放跟不上",
		},
	}
}

// ── Rule 22: Multiple slow SQLs ──

func classifyMultipleSlowSQLs(r BurstReport) Classification {
	if len(r.TopSQLs) < 3 {
		return Classification{}
	}

	slowCount := 0
	for _, sql := range r.TopSQLs {
		if sql.MaxTimeSec > 1 {
			slowCount++
		}
	}

	if slowCount < 3 {
		return Classification{}
	}

	confidence := 0.5
	if slowCount >= 5 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("%d 个 SQL 最大耗时超过 1 秒", slowCount),
			"多个不同 SQL 同时变慢，可能是系统资源瓶颈",
		},
	}
}

// ── Rule 23: Blocker chain deep ──

func classifyBlockerChainDeep(r BurstReport) Classification {
	if len(r.BlockingChains) < 2 {
		return Classification{}
	}

	totalVictims := 0
	for _, chain := range r.BlockingChains {
		totalVictims += chain.VictimCount
	}

	if totalVictims < 5 {
		return Classification{}
	}

	confidence := 0.7
	if totalVictims > 20 {
		confidence = 0.9
	}

	return Classification{
		Cause:      CauseLockContention,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("%d 个阻塞链, 共 %d 个被阻塞会话", len(r.BlockingChains), totalVictims),
			"多层阻塞链正在扩散",
		},
	}
}

// ── Rule 24: Seq scan + cache compound ──

func classifySeqScanCacheCompound(r BurstReport) Classification {
	cacheHit := metricMax(r, string(MetricCacheHitPct))
	active := metricMax(r, string(MetricActiveSessions))

	if cacheHit <= 0 || cacheHit > 85 || active < 5 {
		return Classification{}
	}

	confidence := 0.5
	if cacheHit < 70 && active > 20 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			formatEvidence("缓存命中率 %.1f%%, 活跃会话 %.0f", cacheHit, active),
			"低缓存命中率伴随高活跃会话，可能存在大量顺序扫描",
		},
	}
}
