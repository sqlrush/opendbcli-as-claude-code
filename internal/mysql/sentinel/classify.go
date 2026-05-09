/*-------------------------------------------------------------------------
 *
 * classify.go
 *	  Classify applies deterministic rules to determine the root cause.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sentinel/classify.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "fmt"

// Classify applies deterministic rules to determine the root cause.
func Classify(report BurstReport) Classification {
	if c := classifyRowLockContention(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifySlowQuery(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyBinlogBottleneck(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyBufferPoolPressure(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyConnectionStorm(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyReplicationLag(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyInnoDBHistory(report); c.Confidence > 0.3 {
		return c
	}
	// New rules.
	if c := classifyDeadlockStorm(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyIOSubsystem(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyMemoryPressure(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyTempSpill(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyInnoDBUndoPressure(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyReplicationApplier(report); c.Confidence > 0.3 {
		return c
	}
	// Extended rules.
	if c := classifyExtended(report); c.Confidence > 0.3 {
		return c
	}
	// Database hang (elimination).
	if c := classifyDatabaseHang(report); c.Confidence > 0.3 {
		return c
	}
	if c := classifyFromTrigger(report.TriggerEvent); c.Confidence > 0 {
		return c
	}
	return Classification{Cause: CauseUnknown, Confidence: 0}
}

func classifyRowLockContention(r BurstReport) Classification {
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
	evidence := make([]string, 0, len(r.BlockingChains))
	for _, chain := range r.BlockingChains {
		evidence = append(evidence, fmt.Sprintf("Thread %d 阻塞 %d 个会话, 等待: %s",
			chain.BlockerThreadID, chain.VictimCount, chain.WaitType))
	}
	return Classification{Cause: CauseRowLockContention, Confidence: confidence, Evidence: evidence}
}

func classifySlowQuery(r BurstReport) Classification {
	if len(r.TopSQLs) == 0 {
		return Classification{}
	}
	top := r.TopSQLs[0]
	if top.MaxLatencyMs < 1000 || top.MaxConcurrent < 2 {
		return Classification{}
	}
	confidence := 0.6
	if top.MaxLatencyMs > 5000 && top.MaxConcurrent >= 5 {
		confidence = 0.8
	}
	text := top.SQLText
	if len(text) > 80 {
		text = text[:80] + "..."
	}
	return Classification{
		Cause: CauseSlowQuery, Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("SQL(%s) 耗时 %.1fms, 峰值 %d 并发", top.Digest, top.MaxLatencyMs, top.MaxConcurrent),
			fmt.Sprintf("SQL: %s", text),
		},
	}
}

func classifyBinlogBottleneck(r BurstReport) Classification {
	redo := metricMax(r.Metrics, string(MetricRedoRate))
	if redo < 5000 {
		return Classification{}
	}
	confidence := 0.6
	if redo > 20000 {
		confidence = 0.8
	}
	return Classification{Cause: CauseBinlogBottleneck, Confidence: confidence,
		Evidence: []string{fmt.Sprintf("Redo/Binlog 写入速率 %.0f KB/s", redo)}}
}

func classifyBufferPoolPressure(r BurstReport) Classification {
	hit := metricMax(r.Metrics, string(MetricBufferPoolHit))
	if hit <= 0 || hit > 95 {
		return Classification{}
	}
	confidence := 0.5
	if hit < 90 {
		confidence = 0.7
	}
	if hit < 80 {
		confidence = 0.85
	}
	return Classification{Cause: CauseBufferPoolPressure, Confidence: confidence,
		Evidence: []string{fmt.Sprintf("缓冲池命中率 %.1f%%", hit)}}
}

func classifyConnectionStorm(r BurstReport) Classification {
	conn := metricMax(r.Metrics, string(MetricConnectionsPct))
	threads := metricMax(r.Metrics, string(MetricThreadsRunning))
	if conn < 70 && threads < 50 {
		return Classification{}
	}
	confidence := 0.5
	if conn > 85 {
		confidence = 0.7
	}
	if conn > 95 {
		confidence = 0.9
	}
	evidence := []string{fmt.Sprintf("连接使用率 %.1f%%", conn)}
	if threads > 0 {
		evidence = append(evidence, fmt.Sprintf("运行线程 %.0f", threads))
	}
	return Classification{Cause: CauseConnectionStorm, Confidence: confidence, Evidence: evidence}
}

func classifyReplicationLag(r BurstReport) Classification {
	lag := metricMax(r.Metrics, string(MetricReplicationLag))
	if lag < 5 {
		return Classification{}
	}
	confidence := 0.6
	if lag > 30 {
		confidence = 0.8
	}
	return Classification{Cause: CauseReplicationLag, Confidence: confidence,
		Evidence: []string{fmt.Sprintf("复制延迟 %.1f 秒", lag)}}
}

func classifyInnoDBHistory(r BurstReport) Classification {
	hist := metricMax(r.Metrics, string(MetricHistoryList))
	if hist < 10000 {
		return Classification{}
	}
	confidence := 0.5
	if hist > 100000 {
		confidence = 0.7
	}
	if hist > 1000000 {
		confidence = 0.85
	}
	return Classification{Cause: CauseInnoDBHistory, Confidence: confidence,
		Evidence: []string{fmt.Sprintf("InnoDB History List 长度 %.0f", hist)}}
}

// ── New rules ──

func classifyDeadlockStorm(r BurstReport) Classification {
	dl := metricMax(r.Metrics, string(MetricDeadlocks))
	if dl < 0.5 {
		return Classification{}
	}
	confidence := 0.6
	if dl > 2 {
		confidence = 0.8
	}
	return Classification{Cause: CauseDeadlockStorm, Confidence: confidence,
		Evidence: []string{fmt.Sprintf("死锁速率 %.1f/s", dl)}}
}

func classifyIOSubsystem(r BurstReport) Classification {
	bpHit := metricMax(r.Metrics, string(MetricBufferPoolHit))
	dirty := metricMax(r.Metrics, string(MetricBufferPoolDirtyPct))
	waitFree := metricMax(r.Metrics, string(MetricInnoDBBufferPoolWaitFree))

	if bpHit > 90 && dirty < 70 && waitFree < 1 {
		return Classification{}
	}
	confidence := 0.0
	evidence := []string{}
	if bpHit > 0 && bpHit < 90 {
		confidence += 0.3
		evidence = append(evidence, fmt.Sprintf("缓冲池命中率 %.1f%%", bpHit))
	}
	if dirty >= 70 {
		confidence += 0.25
		evidence = append(evidence, fmt.Sprintf("脏页比例 %.1f%%", dirty))
	}
	if waitFree >= 1 {
		confidence += 0.25
		evidence = append(evidence, fmt.Sprintf("Buffer Pool Wait Free %.1f/s", waitFree))
	}
	if confidence < 0.4 {
		return Classification{}
	}
	return Classification{Cause: CauseIOSubsystem, Confidence: confidence, Evidence: evidence}
}

func classifyMemoryPressure(r BurstReport) Classification {
	bpHit := metricMax(r.Metrics, string(MetricBufferPoolHit))
	ahiHit := metricMax(r.Metrics, string(MetricAdaptiveHashHitPct))

	if bpHit > 90 && ahiHit > 50 {
		return Classification{}
	}
	confidence := 0.0
	evidence := []string{}
	if bpHit > 0 && bpHit < 90 {
		confidence += 0.4
		evidence = append(evidence, fmt.Sprintf("缓冲池命中率 %.1f%%", bpHit))
	}
	if ahiHit > 0 && ahiHit < 50 {
		confidence += 0.3
		evidence = append(evidence, fmt.Sprintf("自适应哈希命中率 %.1f%%", ahiHit))
	}
	if confidence < 0.4 {
		return Classification{}
	}
	return Classification{Cause: CauseMemoryPressure, Confidence: confidence, Evidence: evidence}
}

func classifyTempSpill(r BurstReport) Classification {
	tmpPct := metricMax(r.Metrics, string(MetricTmpDiskPct))
	fullJoin := metricMax(r.Metrics, string(MetricSelectFullJoinRate))
	if tmpPct < 25 && fullJoin < 5 {
		return Classification{}
	}
	confidence := 0.5
	if tmpPct > 50 {
		confidence = 0.75
	}
	evidence := []string{}
	if tmpPct > 0 {
		evidence = append(evidence, fmt.Sprintf("临时表磁盘率 %.1f%%", tmpPct))
	}
	if fullJoin > 0 {
		evidence = append(evidence, fmt.Sprintf("全连接扫描 %.1f/s", fullJoin))
	}
	evidence = append(evidence, "tmp_table_size/max_heap_table_size 可能不足")
	return Classification{Cause: CauseTempSpill, Confidence: confidence, Evidence: evidence}
}

func classifyInnoDBUndoPressure(r BurstReport) Classification {
	hist := metricMax(r.Metrics, string(MetricHistoryList))
	logWaits := metricMax(r.Metrics, string(MetricInnoDBLogWaitsRate))
	if hist < 50000 && logWaits < 1 {
		return Classification{}
	}
	confidence := 0.0
	evidence := []string{}
	if hist >= 50000 {
		confidence += 0.4
		evidence = append(evidence, fmt.Sprintf("History List %.0f", hist))
	}
	if logWaits >= 1 {
		confidence += 0.3
		evidence = append(evidence, fmt.Sprintf("InnoDB Log Waits %.1f/s", logWaits))
	}
	if confidence < 0.4 {
		return Classification{}
	}
	return Classification{Cause: CauseInnoDBUndoPressure, Confidence: confidence, Evidence: evidence}
}

func classifyReplicationApplier(r BurstReport) Classification {
	lag := metricMax(r.Metrics, string(MetricReplicationLag))
	relay := metricMax(r.Metrics, string(MetricRelayLogSpace))
	if lag < 10 || relay < 500 {
		return Classification{}
	}
	confidence := 0.6
	if lag > 60 && relay > 2048 {
		confidence = 0.8
	}
	return Classification{Cause: CauseReplicationApplier, Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("复制延迟 %.1f秒, Relay Log %.0f MB", lag, relay),
			"Relay Log 增长 + 延迟上升，回放速度跟不上",
		}}
}

func classifyDatabaseHang(r BurstReport) Classification {
	threads := metricMax(r.Metrics, string(MetricThreadsRunning))
	tps := metricMax(r.Metrics, string(MetricTPS))
	qps := metricMax(r.Metrics, string(MetricQPS))
	if threads < 10 || tps > 5 || qps > 10 {
		return Classification{}
	}
	confidence := 0.5
	if threads > 30 && tps == 0 {
		confidence = 0.7
	}
	return Classification{Cause: CauseDatabaseHang, Confidence: confidence,
		Evidence: []string{
			fmt.Sprintf("运行线程 %.0f 但 TPS=%.1f QPS=%.1f", threads, tps, qps),
			"大量线程运行但无吞吐，疑似数据库 Hang",
		}}
}

func classifyFromTrigger(trigger TriggerEvent) Classification {
	if trigger.Metric == "" {
		return Classification{}
	}
	causeMap := map[MetricName]RootCauseType{
		MetricLockWaits:                CauseRowLockContention,
		MetricRowLockWaits:             CauseRowLockContention,
		MetricDeadlocks:                CauseDeadlockStorm,
		MetricInnoDBLockWaitTimeout:    CauseRowLockContention,
		MetricAvgRowLockTimeMs:         CauseRowLockContention,
		MetricLongQueries:              CauseSlowQuery,
		MetricThreadsRunning:           CauseSlowQuery,
		MetricSlowQueriesRate:          CauseSlowQuery,
		MetricSelectFullJoinRate:       CauseSlowQuery,
		MetricRedoRate:                 CauseBinlogBottleneck,
		MetricBufferPoolHit:            CauseBufferPoolPressure,
		MetricBufferPoolDirtyPct:       CauseIOSubsystem,
		MetricAdaptiveHashHitPct:       CauseMemoryPressure,
		MetricInnoDBBufferPoolWaitFree: CauseIOSubsystem,
		MetricThreadsConnected:         CauseConnectionStorm,
		MetricConnectionsPct:           CauseConnectionStorm,
		MetricAbortedConnectsRate:      CauseConnectionStorm,
		MetricReplicationLag:           CauseReplicationLag,
		MetricRelayLogSpace:            CauseReplicationApplier,
		MetricHistoryList:              CauseInnoDBHistory,
		MetricInnoDBLogWaitsRate:       CauseInnoDBUndoPressure,
		MetricTmpDiskPct:               CauseTempSpill,
		MetricTablespaceUsedPct:        CauseIOSubsystem,
		MetricTPS:                      CauseSlowQuery,
		MetricQPS:                      CauseSlowQuery,
		MetricTableLocksWaitedRate:     CauseRowLockContention,
		MetricHandlerReadRndRate:       CauseSlowQuery,
		MetricUptimeSinceFlush:         CauseDatabaseHang,
		MetricInnoDBDataFileGrowth:     CauseIOSubsystem,
	}
	cause, ok := causeMap[MetricName(trigger.Metric)]
	if !ok {
		cause = CauseUnknown
	}
	return Classification{Cause: cause, Confidence: 0.4,
		Evidence: []string{
			fmt.Sprintf("基于触发指标推断: %s 从 %.1f->%.1f (阈值 %.1f)",
				MetricLabel(MetricName(trigger.Metric)), trigger.Baseline, trigger.Current, trigger.Threshold),
		}}
}

func metricMax(metrics map[string]MetricSummary, key string) float64 {
	if m, ok := metrics[key]; ok {
		return m.Max
	}
	return 0
}
