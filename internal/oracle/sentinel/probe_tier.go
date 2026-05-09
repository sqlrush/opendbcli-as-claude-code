/*-------------------------------------------------------------------------
 *
 * probe_tier.go
 *	  MediumProbeCollector collects medium-tier metrics every 10
 *	  seconds.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/probe_tier.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"context"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// toFloat64 converts any value to float64 (best-effort).
func toFloat64(v any) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case string:
		// Simple decimal parser: handles "123.45" style strings.
		var result float64
		var divisor float64
		decimal := false
		for _, c := range val {
			if c == '.' {
				decimal = true
				divisor = 1
				continue
			}
			if c >= '0' && c <= '9' {
				if decimal {
					divisor *= 10
					result += float64(c-'0') / divisor
				} else {
					result = result*10 + float64(c-'0')
				}
			}
		}
		return result
	default:
		return 0
	}
}

// ──────────────────────────────────────────────────────────────────
// waitEventPrev stores previous cumulative values for wait event
// delta calculations.
// ──────────────────────────────────────────────────────────────────

type waitEventPrev struct {
	totalWaits      int64
	timeWaitedMicro int64
}

// ──────────────────────────────────────────────────────────────────
// MediumProbeCollector — 10s tier
// ──────────────────────────────────────────────────────────────────

// MediumProbeCollector collects medium-tier metrics every 10 seconds.
type MediumProbeCollector struct {
	driver db.Driver

	// Previous cumulative values for delta calculation.
	prevPhysicalReads int64
	prevLogicalReads  int64
	prevUserCommits   int64
	prevEnqueueWaits  int64
	prevEnqDeadlocks  int64
	prevLogons        int64
	prevTime          time.Time
	initialized       bool

	// Previous wait event values for delta avg calculation.
	prevWaitEvents map[string]*waitEventPrev

	// Previous latch values for delta calculation.
	prevLatchGets   int64
	prevLatchMisses int64
}

// NewMediumProbeCollector creates a new medium-tier probe collector.
func NewMediumProbeCollector(driver db.Driver) *MediumProbeCollector {
	return &MediumProbeCollector{
		driver:         driver,
		prevWaitEvents: make(map[string]*waitEventPrev),
	}
}

// Medium-tier SQL queries.
const (
	mediumSysstatSQL = `SELECT name, value FROM v$sysstat
WHERE name IN ('physical reads', 'session logical reads', 'user commits', 'enqueue waits', 'enqueue deadlocks', 'logons cumulative')`

	mediumSessionSQL = `SELECT
  (SELECT COUNT(*) FROM v$session WHERE TYPE='USER') AS total_sessions,
  (SELECT COUNT(*) FROM v$px_session) AS pq_sessions,
  (SELECT COUNT(*) FROM v$session WHERE TYPE='BACKGROUND' AND STATUS='ACTIVE' AND WAIT_CLASS<>'Idle') AS bg_wait,
  (SELECT COUNT(*) FROM v$session WHERE BLOCKING_SESSION IS NOT NULL) AS blocking_count,
  (SELECT COUNT(*) FROM v$session WHERE STATUS='ACTIVE' AND WAIT_CLASS='Concurrency' AND WAIT_CLASS<>'Idle') AS mutex_wait
FROM dual`

	mediumWaitEventSQL = `SELECT event, total_waits, time_waited_micro
FROM v$system_event
WHERE event IN ('log file sync', 'db file sequential read', 'db file scattered read')
AND wait_class <> 'Idle'`

	mediumOSLoadSQL = `SELECT VALUE FROM v$osstat WHERE STAT_NAME = 'LOAD'`

	mediumLatchSQL = `SELECT SUM(gets) AS total_gets, SUM(misses) AS total_misses FROM v$latch`
)

// Probe runs all medium-tier SQL queries and returns metric values.
// Errors are handled gracefully — failed queries are skipped.
func (m *MediumProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	now := time.Now()

	m.collectSysstat(ctx, metrics, now)
	m.collectSessionInfo(ctx, metrics)
	m.collectWaitEvents(ctx, metrics)
	m.collectOSLoad(ctx, metrics)
	m.collectLatch(ctx, metrics)

	// Stubs for metrics not yet implemented.
	metrics[MetricRowLockWaitMs] = 0
	metrics[MetricNetworkRoundTrip] = 0
	metrics[MetricTopSQLDrift] = 0
	metrics[MetricFullScanRate] = 0
	metrics[MetricPlanChangeCount] = 0
	metrics[MetricSQLThrottle] = 0

	return metrics
}

// collectSysstat queries v$sysstat for delta-based throughput metrics.
func (m *MediumProbeCollector) collectSysstat(ctx context.Context, metrics map[MetricName]float64, now time.Time) {
	rows, err := m.driver.Query(ctx, mediumSysstatSQL)
	if err != nil {
		return
	}

	var curPhysReads, curLogReads, curCommits, curEnqWaits, curEnqDead, curLogons int64
	for _, row := range rows.Rows {
		if len(row) < 2 {
			continue
		}
		name := toString(row[0])
		val := toInt64(row[1])
		switch name {
		case "physical reads":
			curPhysReads = val
		case "session logical reads":
			curLogReads = val
		case "user commits":
			curCommits = val
		case "enqueue waits":
			curEnqWaits = val
		case "enqueue deadlocks":
			curEnqDead = val
		case "logons cumulative":
			curLogons = val
		}
	}

	if m.initialized {
		elapsed := now.Sub(m.prevTime).Seconds()
		if elapsed > 0 {
			metrics[MetricPhysicalReadRate] = float64(curPhysReads-m.prevPhysicalReads) / elapsed
			metrics[MetricLogicalReadRate] = float64(curLogReads-m.prevLogicalReads) / elapsed
			metrics[MetricCommitRate] = float64(curCommits-m.prevUserCommits) / elapsed
			metrics[MetricEnqueueWaitMs] = float64(curEnqWaits - m.prevEnqueueWaits)
			metrics[MetricEnqueueDeadlocks] = float64(curEnqDead - m.prevEnqDeadlocks)
			metrics[MetricSessionCreationRate] = float64(curLogons-m.prevLogons) / elapsed
		}
	}

	m.prevPhysicalReads = curPhysReads
	m.prevLogicalReads = curLogReads
	m.prevUserCommits = curCommits
	m.prevEnqueueWaits = curEnqWaits
	m.prevEnqDeadlocks = curEnqDead
	m.prevLogons = curLogons
	m.prevTime = now
	m.initialized = true
}

// collectSessionInfo queries session-related counts.
func (m *MediumProbeCollector) collectSessionInfo(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumSessionSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 5 {
		return
	}

	row := rows.Rows[0]
	metrics[MetricTotalSessions] = toFloat64(row[0])
	metrics[MetricPQSessions] = toFloat64(row[1])
	metrics[MetricBackgroundWait] = toFloat64(row[2])
	metrics[MetricBlockingChains] = toFloat64(row[3])
	metrics[MetricMutexWait] = toFloat64(row[4])
}

// collectWaitEvents queries v$system_event for wait latency averages.
func (m *MediumProbeCollector) collectWaitEvents(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumWaitEventSQL)
	if err != nil {
		return
	}

	eventToMetric := map[string]MetricName{
		"log file sync":           MetricLogFileSyncUs,
		"db file sequential read": MetricDbFileSeqReadUs,
		"db file scattered read":  MetricDbFileScatReadUs,
	}

	for _, row := range rows.Rows {
		if len(row) < 3 {
			continue
		}
		event := toString(row[0])
		curWaits := toInt64(row[1])
		curMicro := toInt64(row[2])

		metricName, ok := eventToMetric[event]
		if !ok {
			continue
		}

		prev, hasPrev := m.prevWaitEvents[event]
		if hasPrev {
			deltaWaits := curWaits - prev.totalWaits
			deltaMicro := curMicro - prev.timeWaitedMicro
			if deltaWaits > 0 {
				metrics[metricName] = float64(deltaMicro) / float64(deltaWaits)
			}
		}

		m.prevWaitEvents[event] = &waitEventPrev{
			totalWaits:      curWaits,
			timeWaitedMicro: curMicro,
		}
	}
}

// collectOSLoad queries v$osstat for OS load average.
func (m *MediumProbeCollector) collectOSLoad(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumOSLoadSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	metrics[MetricOSLoad] = toFloat64(rows.Rows[0][0])
}

// collectLatch queries v$latch for latch miss percentage.
func (m *MediumProbeCollector) collectLatch(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := m.driver.Query(ctx, mediumLatchSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 2 {
		return
	}

	gets := toFloat64(rows.Rows[0][0])
	misses := toFloat64(rows.Rows[0][1])
	if gets > 0 {
		metrics[MetricLatchFreeRate] = misses * 100.0 / gets
	}
}

// ──────────────────────────────────────────────────────────────────
// SlowProbeCollector — 30s tier
// ──────────────────────────────────────────────────────────────────

// SlowProbeCollector collects slow-tier metrics every 30 seconds.
type SlowProbeCollector struct {
	driver db.Driver
}

// NewSlowProbeCollector creates a new slow-tier probe collector.
func NewSlowProbeCollector(driver db.Driver) *SlowProbeCollector {
	return &SlowProbeCollector{driver: driver}
}

// Slow-tier SQL queries.
const (
	slowCacheHitSQL = `SELECT
  (SELECT 1 - (SUM(CASE WHEN name='physical reads' THEN value END) /
   NULLIF(SUM(CASE WHEN name='db block gets' THEN value END) +
          SUM(CASE WHEN name='consistent gets' THEN value END), 0))
   FROM v$sysstat WHERE name IN ('physical reads','db block gets','consistent gets')) AS buf_hit,
  (SELECT gethitratio FROM v$librarycache WHERE namespace='SQL AREA') AS lib_hit
FROM dual`

	slowPGASharedPoolSQL = `SELECT
  (SELECT value FROM v$parameter WHERE name='pga_aggregate_target') AS pga_target,
  (SELECT value FROM v$pgastat WHERE name='total PGA allocated') AS pga_used,
  (SELECT SUM(CASE WHEN name='free memory' THEN bytes ELSE 0 END) FROM v$sgastat WHERE pool='shared pool') AS sp_free,
  (SELECT SUM(bytes) FROM v$sgastat WHERE pool='shared pool') AS sp_total
FROM dual`

	slowTablespaceSQL = `SELECT MAX(used_percent) FROM dba_tablespace_usage_metrics`

	slowTempSQL = `SELECT
  NVL((SELECT SUM(bytes_used)*100.0/NULLIF(SUM(bytes_free+bytes_used),0) FROM v$temp_space_header), 0) AS temp_pct
FROM dual`

	slowFRASQL = `SELECT space_used*100.0/NULLIF(space_limit,0) FROM v$recovery_file_dest`

	slowASMSQL = `SELECT MAX((total_mb-free_mb)*100.0/NULLIF(total_mb,0)) FROM v$asm_diskgroup`

	slowLogSwitchSQL = `SELECT COUNT(*) FROM v$log_history WHERE first_time > SYSDATE - 1/24`

	slowArchiveLagSQL = `SELECT NVL(MAX((SYSDATE-next_time)*86400), 0) FROM v$archived_log WHERE dest_id=1 AND archived='YES' AND next_time > SYSDATE - 1/24`

	slowInstanceSQL = `SELECT status FROM v$instance`

	slowResourceLimitSQL = `SELECT current_utilization, max_utilization, initial_allocation FROM v$resource_limit WHERE resource_name IN ('sessions','processes')`
)

// Probe runs all slow-tier SQL queries and returns metric values.
// Errors are handled gracefully — failed queries are skipped.
func (s *SlowProbeCollector) Probe(ctx context.Context) map[MetricName]float64 {
	metrics := make(map[MetricName]float64)

	s.collectCacheHit(ctx, metrics)
	s.collectPGASharedPool(ctx, metrics)
	s.collectTablespace(ctx, metrics)
	s.collectTemp(ctx, metrics)
	s.collectFRA(ctx, metrics)
	s.collectASM(ctx, metrics)
	s.collectLogSwitch(ctx, metrics)
	s.collectArchiveLag(ctx, metrics)
	s.collectInstance(ctx, metrics)
	s.collectResourceLimit(ctx, metrics)

	// Stubs for metrics not yet implemented.
	metrics[MetricUndoUsedPct] = 0
	metrics[MetricStandbyApplyLag] = 0
	metrics[MetricRedoLogSpaceWait] = 0
	metrics[MetricDataGuardStatus] = 0
	metrics[MetricAlertLogErrors] = 0
	metrics[MetricJobFailureRate] = 0
	metrics[MetricCheckpointNotComplete] = 0

	return metrics
}

// collectCacheHit queries buffer cache and library cache hit ratios.
func (s *SlowProbeCollector) collectCacheHit(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowCacheHitSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 2 {
		return
	}

	row := rows.Rows[0]
	if row[0] != nil {
		metrics[MetricBufferCacheHit] = toFloat64(row[0]) * 100
	}
	if row[1] != nil {
		metrics[MetricLibraryCacheHit] = toFloat64(row[1]) * 100
	}
}

// collectPGASharedPool queries PGA and shared pool usage.
func (s *SlowProbeCollector) collectPGASharedPool(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowPGASharedPoolSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 4 {
		return
	}

	row := rows.Rows[0]
	pgaTarget := toFloat64(row[0])
	pgaUsed := toFloat64(row[1])
	spFree := toFloat64(row[2])
	spTotal := toFloat64(row[3])

	if pgaTarget > 0 {
		metrics[MetricPGAUsedPct] = pgaUsed / pgaTarget * 100
	}
	if spTotal > 0 {
		metrics[MetricSharedPoolFreePct] = spFree / spTotal * 100
	}
}

// collectTablespace queries the maximum tablespace usage percentage.
func (s *SlowProbeCollector) collectTablespace(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowTablespaceSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	if rows.Rows[0][0] != nil {
		metrics[MetricTablespaceUsedPct] = toFloat64(rows.Rows[0][0])
	}
}

// collectTemp queries temporary tablespace usage.
func (s *SlowProbeCollector) collectTemp(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowTempSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	metrics[MetricTempUsedPct] = toFloat64(rows.Rows[0][0])
}

// collectFRA queries Flash Recovery Area usage. Skipped if FRA is not configured.
func (s *SlowProbeCollector) collectFRA(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowFRASQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	if rows.Rows[0][0] != nil {
		metrics[MetricFRAUsedPct] = toFloat64(rows.Rows[0][0])
	}
}

// collectASM queries ASM disk group usage. Skipped if ASM is not used.
func (s *SlowProbeCollector) collectASM(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowASMSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	if rows.Rows[0][0] != nil {
		metrics[MetricASMUsedPct] = toFloat64(rows.Rows[0][0])
	}
}

// collectLogSwitch queries redo log switch frequency in the last hour.
func (s *SlowProbeCollector) collectLogSwitch(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowLogSwitchSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	metrics[MetricLogSwitchRate] = toFloat64(rows.Rows[0][0])
}

// collectArchiveLag queries the maximum archive log lag in seconds.
func (s *SlowProbeCollector) collectArchiveLag(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowArchiveLagSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	metrics[MetricArchiveLagSec] = toFloat64(rows.Rows[0][0])
}

// collectInstance queries instance status (1.0 = OPEN, 0.0 = otherwise).
func (s *SlowProbeCollector) collectInstance(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowInstanceSQL)
	if err != nil {
		return
	}
	if len(rows.Rows) == 0 || len(rows.Rows[0]) < 1 {
		return
	}

	status := toString(rows.Rows[0][0])
	if status == "OPEN" {
		metrics[MetricInstanceStatus] = 1.0
	} else {
		metrics[MetricInstanceStatus] = 0.0
	}
}

// collectResourceLimit queries resource utilization vs initial allocation.
func (s *SlowProbeCollector) collectResourceLimit(ctx context.Context, metrics map[MetricName]float64) {
	rows, err := s.driver.Query(ctx, slowResourceLimitSQL)
	if err != nil {
		return
	}

	var maxPct float64
	for _, row := range rows.Rows {
		if len(row) < 3 {
			continue
		}
		current := toFloat64(row[0])
		initial := toFloat64(row[2])
		if initial > 0 {
			pct := current / initial * 100
			if pct > maxPct {
				maxPct = pct
			}
		}
	}

	metrics[MetricResourceLimitPct] = maxPct
}
