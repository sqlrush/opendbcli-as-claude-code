/*-------------------------------------------------------------------------
 *
 * classify_extended.go
 *	  Anomaly classifier (extended) for MySQL Sentinel — turns raw
 *	  probe deltas into typed events the engine routes to the right
 *	  diagnosis path.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/classify_extended.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "fmt"

// classifyExtended runs additional classification rules beyond the core 7.
// Returns a Classification with confidence > 0 if a match is found.
// Called from Classify() after the core rules.
func classifyExtended(report BurstReport) Classification {
	// Rule 8: Deadlock detected.
	if c := classifyDeadlock(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 9: Temporary disk tables overflow.
	if c := classifyTmpDiskOverflow(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 10: Active sessions spike (not covered by other rules).
	if c := classifyActiveSessionSpike(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 11: Threads running vs threads connected imbalance.
	if c := classifyThreadsRunningSpike(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 12: Row lock waits (InnoDB specific, distinct from blocking chains).
	if c := classifyRowLockWaitRate(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 13: QPS drop (throughput collapse).
	if c := classifyQPSDrop(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 14: TPS drop.
	if c := classifyTPSDrop(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 15: Wait profile dominated by IO.
	if c := classifyIOWaitDominant(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 16: Wait profile dominated by lock waits.
	if c := classifyLockWaitDominant(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 17: Connection storm with high threads_running.
	if c := classifyConnectionOverloadCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 18: Buffer pool pressure + slow query compound.
	if c := classifyBufferPoolSlowQueryCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 19: InnoDB history + row lock compound.
	if c := classifyHistoryLockCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 20: Long query blocks replication.
	if c := classifyLongQueryReplicationImpact(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 21: Replication lag + threads running compound.
	if c := classifyReplicationBackpressure(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 22: Redo rate + connection storm compound.
	if c := classifyRedoConnectionCompound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 23: Multiple slow SQLs.
	if c := classifyMultipleSlowSQLs(report); c.Confidence > 0.3 {
		return c
	}

	return Classification{}
}

// ── Rule 8: Deadlock ──

func classifyDeadlock(r BurstReport) Classification {
	deadlocks := metricMax(r.Metrics, string(MetricDeadlocks))
	if deadlocks <= 0 {
		return Classification{}
	}

	confidence := 0.7
	if deadlocks > 1 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseRowLockContention,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("检测到死锁: %.0f/s", deadlocks),
			"死锁通常由交叉更新导致，建议检查事务中的 SQL 顺序",
		},
	}
}

// ── Rule 9: Temporary disk tables ──

func classifyTmpDiskOverflow(r BurstReport) Classification {
	tmpDiskPct := metricMax(r.Metrics, string(MetricTmpDiskPct))
	if tmpDiskPct < 25 {
		return Classification{}
	}

	confidence := 0.5
	if tmpDiskPct > 50 {
		confidence = 0.7
	}
	if tmpDiskPct > 75 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("临时表磁盘溢出率 %.1f%%", tmpDiskPct),
			"大量临时表写入磁盘，建议增大 tmp_table_size/max_heap_table_size 或优化查询",
		},
	}
}

// ── Rule 10: Active sessions spike ──

func classifyActiveSessionSpike(r BurstReport) Classification {
	active := metricMax(r.Metrics, string(MetricActiveSessions))
	threadsRunning := metricMax(r.Metrics, string(MetricThreadsRunning))

	if active < 20 && threadsRunning < 20 {
		return Classification{}
	}

	// Only trigger if none of the specific causes are clearly dominant.
	lockWaits := metricMax(r.Metrics, string(MetricLockWaits))
	if lockWaits > 5 {
		return Classification{} // Lock contention will catch this.
	}

	confidence := 0.4
	if active > 50 || threadsRunning > 50 {
		confidence = 0.6
	}
	if active > 100 || threadsRunning > 100 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("活跃会话 %.0f, 运行线程 %.0f", active, threadsRunning),
			"活跃会话冲高，多个查询同时消耗资源",
		},
	}
}

// ── Rule 11: Threads running spike without connection storm ──

func classifyThreadsRunningSpike(r BurstReport) Classification {
	threadsRunning := metricMax(r.Metrics, string(MetricThreadsRunning))
	connPct := metricMax(r.Metrics, string(MetricConnectionsPct))

	// High threads_running but connection usage not critical.
	if threadsRunning < 30 || connPct > 85 {
		return Classification{}
	}

	confidence := 0.5
	if threadsRunning > 80 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseSlowQuery,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("运行线程 %.0f (连接使用率仅 %.1f%%)", threadsRunning, connPct),
			"线程运行数冲高但连接未满，SQL 执行效率低导致线程堆积",
		},
	}
}

// ── Rule 12: InnoDB row lock wait rate ──

func classifyRowLockWaitRate(r BurstReport) Classification {
	rowLockWaits := metricMax(r.Metrics, string(MetricRowLockWaits))
	if rowLockWaits < 5 {
		return Classification{}
	}

	// Skip if blocking chains already identified.
	if len(r.BlockingChains) > 0 {
		return Classification{}
	}

	confidence := 0.5
	if rowLockWaits > 20 {
		confidence = 0.7
	}
	if rowLockWaits > 50 {
		confidence = 0.85
	}

	return Classification{
		Cause:      CauseRowLockContention,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("InnoDB 行锁等待率 %.1f/s", rowLockWaits),
			"行锁等待频繁但未形成明显阻塞链，可能是热点行更新",
		},
	}
}

// ── Rule 13: QPS drop ──

func classifyQPSDrop(r BurstReport) Classification {
	qps := metricMax(r.Metrics, string(MetricQPS))
	if qps <= 0 {
		return Classification{}
	}

	// Check if there's a significant trend downward.
	if summary, ok := r.Metrics[string(MetricQPS)]; ok {
		if summary.Trend != "falling" {
			return Classification{}
		}
		// QPS dropped significantly.
		if summary.Min > summary.Max*0.5 {
			return Classification{} // Not a severe drop.
		}

		return Classification{
			Cause:      CauseSlowQuery,
			Confidence: 0.5,
			Evidence: []string{
				fmt.Sprintf("QPS 下跌: 峰值 %.0f → 谷值 %.0f", summary.Max, summary.Min),
				"吞吐量显著下降，可能由锁阻塞或资源瓶颈引起",
			},
		}
	}

	return Classification{}
}

// ── Rule 14: TPS drop ──

func classifyTPSDrop(r BurstReport) Classification {
	if summary, ok := r.Metrics[string(MetricTPS)]; ok {
		if summary.Trend != "falling" {
			return Classification{}
		}
		if summary.Min > summary.Max*0.5 {
			return Classification{}
		}

		return Classification{
			Cause:      CauseSlowQuery,
			Confidence: 0.5,
			Evidence: []string{
				fmt.Sprintf("TPS 下跌: 峰值 %.0f → 谷值 %.0f", summary.Max, summary.Min),
				"事务吞吐量下降，可能由行锁争用或 IO 瓶颈引起",
			},
		}
	}

	return Classification{}
}

// ── Rule 15: IO wait dominant in wait profile ──

func classifyIOWaitDominant(r BurstReport) Classification {
	if len(r.WaitProfile) == 0 {
		return Classification{}
	}

	var ioWaitPct float64
	for _, w := range r.WaitProfile {
		if w.WaitClass == "IO" || w.WaitClass == "io" {
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
		Cause:      CauseBufferPoolPressure,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("IO 等待占比 %.1f%%", ioWaitPct),
			"IO 等待占主导，缓冲池可能不足或存在大量磁盘读写",
		},
	}
}

// ── Rule 16: Lock wait dominant in wait profile ──

func classifyLockWaitDominant(r BurstReport) Classification {
	if len(r.WaitProfile) == 0 {
		return Classification{}
	}

	var lockWaitPct float64
	for _, w := range r.WaitProfile {
		if w.WaitClass == "Lock" || w.WaitClass == "lock" {
			lockWaitPct += w.Percentage
		}
	}

	if lockWaitPct < 30 {
		return Classification{}
	}

	// Skip if blocking chains already caught it.
	if len(r.BlockingChains) > 0 {
		return Classification{}
	}

	confidence := 0.5
	if lockWaitPct > 50 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseRowLockContention,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("Lock 等待占比 %.1f%%", lockWaitPct),
			"等待事件中锁等待占主导",
		},
	}
}

// ── Rule 17: Connection overload compound ──

func classifyConnectionOverloadCompound(r BurstReport) Classification {
	connPct := metricMax(r.Metrics, string(MetricConnectionsPct))
	threadsRunning := metricMax(r.Metrics, string(MetricThreadsRunning))
	active := metricMax(r.Metrics, string(MetricActiveSessions))

	if connPct < 80 || threadsRunning < 30 {
		return Classification{}
	}

	confidence := 0.6
	if connPct > 90 && threadsRunning > 50 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseConnectionStorm,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("连接使用率 %.1f%%, 运行线程 %.0f, 活跃会话 %.0f", connPct, threadsRunning, active),
			"连接数和运行线程同时冲高，系统负载过重",
		},
	}
}

// ── Rule 18: Buffer pool + slow query compound ──

func classifyBufferPoolSlowQueryCompound(r BurstReport) Classification {
	hitPct := metricMax(r.Metrics, string(MetricBufferPoolHit))
	longQueries := metricMax(r.Metrics, string(MetricLongQueries))

	if hitPct <= 0 || hitPct > 90 || longQueries < 2 {
		return Classification{}
	}

	confidence := 0.6
	if hitPct < 80 && longQueries > 5 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseBufferPoolPressure,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("缓冲池命中率 %.1f%%, 慢查询 %.0f 个", hitPct, longQueries),
			"缓冲池命中率低且伴随慢查询，全表扫描导致缓冲池大量换出",
		},
	}
}

// ── Rule 19: InnoDB history + row lock compound ──

func classifyHistoryLockCompound(r BurstReport) Classification {
	historyLen := metricMax(r.Metrics, string(MetricHistoryList))
	lockWaits := metricMax(r.Metrics, string(MetricLockWaits))

	if historyLen < 5000 || lockWaits < 3 {
		return Classification{}
	}

	confidence := 0.6
	if historyLen > 50000 && lockWaits > 10 {
		confidence = 0.8
	}

	return Classification{
		Cause:      CauseInnoDBHistory,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("History List %.0f, 锁等待 %.0f", historyLen, lockWaits),
			"长事务未提交导致 History List 堆积，同时产生锁争用",
		},
	}
}

// ── Rule 20: Long query blocking replication ──

func classifyLongQueryReplicationImpact(r BurstReport) Classification {
	lag := metricMax(r.Metrics, string(MetricReplicationLag))
	longQueries := metricMax(r.Metrics, string(MetricLongQueries))

	if lag < 10 || longQueries < 2 {
		return Classification{}
	}

	confidence := 0.55
	if lag > 30 && longQueries > 5 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseReplicationLag,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("复制延迟 %.1f 秒, 慢查询 %.0f 个", lag, longQueries),
			"慢查询消耗大量 CPU/IO，间接影响复制线程回放速度",
		},
	}
}

// ── Rule 21: Replication backpressure ──

func classifyReplicationBackpressure(r BurstReport) Classification {
	lag := metricMax(r.Metrics, string(MetricReplicationLag))
	threadsRunning := metricMax(r.Metrics, string(MetricThreadsRunning))

	if lag < 10 || threadsRunning < 20 {
		return Classification{}
	}

	confidence := 0.5
	if lag > 60 && threadsRunning > 50 {
		confidence = 0.7
	}

	return Classification{
		Cause:      CauseReplicationLag,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("复制延迟 %.1f 秒, 运行线程 %.0f", lag, threadsRunning),
			"复制延迟伴随高负载，主库写压力超过从库回放能力",
		},
	}
}

// ── Rule 22: Redo rate + connection storm compound ──

func classifyRedoConnectionCompound(r BurstReport) Classification {
	redoRate := metricMax(r.Metrics, string(MetricRedoRate))
	connPct := metricMax(r.Metrics, string(MetricConnectionsPct))

	if redoRate < 5000 || connPct < 70 {
		return Classification{}
	}

	confidence := 0.55
	if redoRate > 15000 && connPct > 85 {
		confidence = 0.75
	}

	return Classification{
		Cause:      CauseBinlogBottleneck,
		Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("Redo/Binlog 写入 %.0f KB/s, 连接使用率 %.1f%%", redoRate, connPct),
			"大量连接同时执行写操作导致 Binlog 写入冲高",
		},
	}
}

// ── Rule 23: Multiple slow SQLs ──

func classifyMultipleSlowSQLs(r BurstReport) Classification {
	if len(r.TopSQLs) < 3 {
		return Classification{}
	}

	slowCount := 0
	for _, sql := range r.TopSQLs {
		if sql.MaxLatencyMs > 1000 {
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
			fmt.Sprintf("%d 个 SQL 平均耗时超过 1 秒", slowCount),
			"多个不同 SQL 同时变慢，可能是系统资源瓶颈而非单条 SQL 问题",
		},
	}
}
