/*-------------------------------------------------------------------------
 *
 * classify.go
 *	  Classify applies deterministic rules to determine the root cause
 *	  of a OG anomaly. Rules are evaluated in priority order; the first
 *	  match with sufficient confidence wins.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sentinel/classify.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import "fmt"

// Classify applies deterministic rules to determine the root cause of a OG anomaly.
// Rules are evaluated in priority order; the first match with sufficient
// confidence wins.
func Classify(report BurstReport) Classification {
	// Rule 1: Lock contention (highest priority).
	if c := classifyLockContention(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 2: IO bottleneck or temp spill.
	if c := classifyIOBottleneck(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 3: Single slow SQL dominates.
	if c := classifySlowQuery(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 4: Vacuum lag (dead tuple ratio high).
	if c := classifyVacuumLag(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 5: XID wraparound risk.
	if c := classifyXIDWraparound(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 6: WAL bottleneck.
	if c := classifyWALBottleneck(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 7: Connection storm.
	if c := classifyConnectionStorm(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 8: Replication lag.
	if c := classifyReplicationLag(report); c.Confidence > 0.3 {
		return c
	}

	// Rule 9: Checkpoint storm.
	if c := classifyCheckpointStorm(report); c.Confidence > 0.3 {
		return c
	}

	// Rules 9-24: Extended classification rules.
	if c := classifyExtended(report); c.Confidence > 0.3 {
		return c
	}

	// Fallback: infer from trigger metric.
	if c := classifyFromTrigger(report.TriggerEvent); c.Confidence > 0 {
		return c
	}

	return Classification{Cause: CauseUnknown, Confidence: 0}
}

func classifyLockContention(r BurstReport) Classification {
	lockWaits := metricMax(r, string(MetricLockWaits))
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

func classifyIOBottleneck(r BurstReport) Classification {
	tempRate := metricMax(r, string(MetricTempBytesRate))
	cacheHit := metricMax(r, string(MetricCacheHitPct))
	active := metricMax(r, string(MetricActiveSessions))
	ioWaitPct := ioWaitPercentage(r)

	if tempRate < 50*1024*1024 && ioWaitPct < 30 && !(cacheHit > 0 && cacheHit < 70 && active >= 10) {
		return Classification{}
	}

	confidence := 0.55
	if tempRate >= 200*1024*1024 || ioWaitPct >= 50 {
		confidence = 0.75
	}
	if ioWaitPct >= 70 || (cacheHit > 0 && cacheHit < 50 && active >= 20) {
		confidence = 0.85
	}

	evidence := []string{}
	if tempRate >= 50*1024*1024 {
		evidence = append(evidence, formatEvidence("临时空间写入速率 %.1f MB/s", tempRate/1024/1024))
	}
	if ioWaitPct >= 30 {
		evidence = append(evidence, formatEvidence("IO 等待占比 %.1f%%", ioWaitPct))
	}
	if cacheHit > 0 && cacheHit < 70 {
		evidence = append(evidence, formatEvidence("缓存命中率 %.1f%%, 活跃会话 %.0f", cacheHit, active))
	}
	if len(evidence) == 0 {
		evidence = append(evidence, "IO 指标异常")
	}

	return Classification{
		Cause:      CauseIOBottleneck,
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

	// WAL rate > 100MB/s is concerning.
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

func classifyFromTrigger(trigger TriggerEvent) Classification {
	if trigger.Metric == "" {
		return Classification{}
	}

	causeMap := map[MetricName]RootCauseType{
		MetricLockWaits:         CauseLockContention,
		MetricLongQueries:       CauseSlowQuery,
		MetricDeadTupleRatio:    CauseVacuumLag,
		MetricXIDAgeRatio:       CauseXIDWraparound,
		MetricWALBytesRate:      CauseWALBottleneck,
		MetricTempBytesRate:     CauseIOBottleneck,
		MetricCacheHitPct:       CauseIOBottleneck,
		MetricConnectionsPct:    CauseConnectionStorm,
		MetricReplicationLag:    CauseReplicationLag,
		MetricCheckpointsReq:    CauseCheckpointStorm,
		MetricActiveSessions:    CauseSlowQuery,
		MetricIdleInTransaction: CauseLockContention,
		MetricBlockerCount:      CauseLockContention,
		MetricDeadlocks:         CauseLockContention,
		MetricXactCommitRate:    CauseSlowQuery,
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

func ioWaitPercentage(r BurstReport) float64 {
	var pct float64
	for _, w := range r.WaitProfile {
		if w.WaitEventType == "IO" {
			pct += w.Percentage
		}
	}
	return pct
}

// metricMax returns the max value for a metric across burst frames.
// Falls back to the Metrics summary if available.
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
