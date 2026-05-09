/*-------------------------------------------------------------------------
 *
 * scenario_100_test.go
 *	  Test cases for scenario_100.go (ruleengine package):
 *	  TestPG100Scenarios.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/scenario_100_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/postgres/sentinel"
)

// ─── Test Infrastructure ────────────────────────────────────────────────────

// scenario defines one test scenario from the PG 100 scenarios document.
type scenario struct {
	ID       string
	Name     string
	Category string
	Report   *sentinel.BurstReport
	// Expected root cause keywords (any match = pass for root cause)
	ExpectCauseKeywords []string
	// Expected minimum severity (0 = any)
	ExpectMinSeverity Severity
	// Expected minimum number of actions
	ExpectMinActions int
}

// mkMetric is a shorthand for building MetricSummary.
func mkMetric(avg, max, min float64, trend string) sentinel.MetricSummary {
	return sentinel.MetricSummary{Avg: avg, Max: max, Min: min, Trend: trend}
}

// baseReport builds a baseline BurstReport with normal-ish metrics.
func baseReport() *sentinel.BurstReport {
	return &sentinel.BurstReport{
		TriggerEvent: sentinel.TriggerEvent{
			Metric:   string(sentinel.MetricActiveSessions),
			Strategy: sentinel.StrategyT1,
		},
		DurationSec:    30,
		PeakActive:     15,
		BaselineActive: 3,
		StartTime:      time.Now().Add(-30 * time.Second),
		EndTime:        time.Now(),
		Metrics:        make(map[string]sentinel.MetricSummary),
		RawFrameCount:  30,
	}
}

// withMetrics copies base and adds/overrides metrics.
func withMetrics(base *sentinel.BurstReport, extra map[string]sentinel.MetricSummary) *sentinel.BurstReport {
	r := *base
	r.Metrics = make(map[string]sentinel.MetricSummary)
	for k, v := range base.Metrics {
		r.Metrics[k] = v
	}
	for k, v := range extra {
		r.Metrics[k] = v
	}
	return &r
}

// ─── mockExecutor ───────────────────────────────────────────────────────────

type mockExec struct {
	results map[QueryID]interface{}
}

func (m *mockExec) Execute(qid QueryID, _ map[string]string) (interface{}, error) {
	if v, ok := m.results[qid]; ok {
		return v, nil
	}
	return float64(0), nil
}

// ─── Score Calculation ──────────────────────────────────────────────────────

type scoreResult struct {
	ID             string
	Name           string
	Category       string
	RootCause      int // /40
	Recommendations int // /30
	Severity       int // /10
	DiagPath       int // /10
	Completeness   int // /10
	Total          int // /100
	Matched        bool
	PrimaryCause   string
	PrimaryRule    string
	Debug          string
}

func scoreOutput(s scenario, output *DiagOutput) scoreResult {
	sr := scoreResult{
		ID:       s.ID,
		Name:     s.Name,
		Category: s.Category,
	}

	if output == nil || output.Primary == nil {
		return sr
	}

	d := output.Primary
	sr.PrimaryCause = d.Cause
	sr.PrimaryRule = d.RuleID

	// 1. Root Cause (40 points)
	causeText := strings.ToLower(d.Cause)
	for _, f := range d.Findings {
		causeText += " " + strings.ToLower(f.Desc)
	}
	for _, kw := range s.ExpectCauseKeywords {
		if strings.Contains(causeText, strings.ToLower(kw)) {
			sr.Matched = true
			sr.RootCause = 40
			break
		}
	}
	// Partial credit if any rule matched but keywords don't match
	if !sr.Matched && d.Cause != "" {
		sr.RootCause = 15 // partial: rule fired but wrong root cause
	}

	// 2. Recommendations (30 points)
	actionCount := len(d.Actions)
	hasSQL := false
	for _, a := range d.Actions {
		if a.RawSQL != "" || a.SkillCommand != "" {
			hasSQL = true
		}
	}
	if actionCount >= 2 && hasSQL {
		sr.Recommendations = 30
	} else if actionCount >= 1 && hasSQL {
		sr.Recommendations = 25
	} else if actionCount >= 1 {
		sr.Recommendations = 15
	} else if d.Cause != "" {
		sr.Recommendations = 5
	}

	// 3. Severity (10 points)
	if s.ExpectMinSeverity > 0 && d.Severity >= s.ExpectMinSeverity {
		sr.Severity = 10
	} else if d.Severity >= SeverityMedium {
		sr.Severity = 7
	} else if d.Severity >= SeverityLow {
		sr.Severity = 3
	}

	// 4. Diagnostic Path (10 points) — based on Findings depth
	findingCount := len(d.Findings)
	if findingCount >= 3 {
		sr.DiagPath = 10
	} else if findingCount >= 2 {
		sr.DiagPath = 7
	} else if findingCount >= 1 {
		sr.DiagPath = 5
	}

	// 5. Completeness (10 points) — absorbed rules + secondary
	if output.Secondary != nil {
		sr.Completeness += 3
	}
	if d.AbsorbedCount > 0 {
		sr.Completeness += 3
	}
	if findingCount >= 2 && actionCount >= 2 {
		sr.Completeness += 4
	} else if findingCount >= 1 && actionCount >= 1 {
		sr.Completeness += 2
	}
	if sr.Completeness > 10 {
		sr.Completeness = 10
	}

	sr.Total = sr.RootCause + sr.Recommendations + sr.Severity + sr.DiagPath + sr.Completeness
	return sr
}

// ─── 100 Scenarios ──────────────────────────────────────────────────────────

func buildScenarios() []scenario {
	scenarios := make([]scenario, 0, 100)

	// ── Category 1: 锁与阻塞 (T001-T015) ──

	// T001: 行锁级联 blocker idle in transaction
	{
		r := baseReport()
		r.PeakActive = 20
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(15, 20, 10, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(1, 1, 1, "steady")
		r.Metrics[string(sentinel.MetricIdleInTransaction)] = mkMetric(8, 10, 5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 15, Percentage: 60},
			{WaitEventType: "Client", WaitEvent: "ClientRead", Count: 8, Percentage: 30},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 1234, BlockerUser: "app", BlockerQuery: "idle in transaction", VictimCount: 15, WaitEvent: "transactionid"},
		}
		scenarios = append(scenarios, scenario{
			ID: "T001", Name: "行锁级联: blocker idle in transaction", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block", "idle"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T002: 行锁级联 blocker 跑慢SQL
	{
		r := baseReport()
		r.PeakActive = 18
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(12, 18, 8, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(1, 1, 1, "steady")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(18, 22, 14, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 12, Percentage: 55},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 5678, BlockerUser: "app", BlockerQuery: "UPDATE big_table SET col1='x' WHERE col2 LIKE '%abc%'", VictimCount: 12, WaitEvent: "transactionid"},
		}
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q1", Query: "UPDATE big_table SET col1='x' WHERE col2 LIKE '%abc%'", Calls: 1, MeanTimeSec: 120, ActiveCount: 1, WaitEventType: "CPU"},
		}
		scenarios = append(scenarios, scenario{
			ID: "T002", Name: "行锁级联: blocker 跑慢SQL", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block", "慢"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T003: 行锁级联 blocker 等 IO
	{
		r := baseReport()
		r.PeakActive = 18
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(10, 15, 7, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(1, 1, 1, "steady")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(18, 22, 14, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 10, Percentage: 45},
			{WaitEventType: "IO", WaitEvent: "DataFileRead", Count: 5, Percentage: 25},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 9012, BlockerUser: "app", BlockerQuery: "UPDATE t SET x=1 WHERE id=5", VictimCount: 10, WaitEvent: "DataFileRead"},
		}
		scenarios = append(scenarios, scenario{
			ID: "T003", Name: "行锁级联: blocker 等 IO", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block", "io", "IO"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T004: 多层阻塞链
	{
		r := baseReport()
		r.PeakActive = 25
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(20, 25, 15, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(3, 4, 2, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(25, 30, 20, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 20, Percentage: 70},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 100, BlockerUser: "app", BlockerQuery: "idle in transaction", VictimCount: 8},
			{BlockerPID: 101, BlockerUser: "app", BlockerQuery: "SELECT ...", VictimCount: 5},
			{BlockerPID: 102, BlockerUser: "app", BlockerQuery: "UPDATE ...", VictimCount: 4},
		}
		scenarios = append(scenarios, scenario{
			ID: "T004", Name: "多层阻塞链", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block", "链"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T005: 死锁
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricDeadlocks)] = mkMetric(5, 8, 2, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "steady")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "deadlock", Count: 5, Percentage: 20},
		}
		scenarios = append(scenarios, scenario{
			ID: "T005", Name: "死锁频率判断", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"死锁", "deadlock"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T006: DDL 阻塞 DML (AccessExclusiveLock)
	{
		r := baseReport()
		r.PeakActive = 20
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(15, 20, 10, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(1, 1, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "relation", Count: 15, Percentage: 65},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 200, BlockerUser: "admin", BlockerQuery: "ALTER TABLE t ADD COLUMN new_col INT", VictimCount: 15, WaitEvent: "relation"},
		}
		scenarios = append(scenarios, scenario{
			ID: "T006", Name: "DDL 阻塞 DML", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block", "ddl", "DDL"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T007: VACUUM 与查询锁冲突
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricDeadTupleRatio)] = mkMetric(25, 30, 20, "rising")
		r.Metrics[string(sentinel.MetricAutovacuumWorkers)] = mkMetric(3, 3, 2, "steady")
		r.Metrics[string(sentinel.MetricOldestXactAgeSec)] = mkMetric(3600, 4000, 3000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "steady")
		scenarios = append(scenarios, scenario{
			ID: "T007", Name: "VACUUM 与查询锁冲突", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "事务", "长事务", "autovacuum"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T008: Advisory Lock 泄漏
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricRowExclusiveLocks)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "advisory", Count: 8, Percentage: 40},
		}
		scenarios = append(scenarios, scenario{
			ID: "T008", Name: "Advisory Lock 泄漏", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "advisory"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T009: 外键缺索引
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(5000, 8000, 2000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 8, Percentage: 40},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 300, BlockerUser: "app", BlockerQuery: "DELETE FROM parent_table WHERE id=1", VictimCount: 8},
		}
		scenarios = append(scenarios, scenario{
			ID: "T009", Name: "外键缺索引导致锁扩散", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞", "block"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T010: 并发 INSERT 热点页争用
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
		r.Metrics[string(sentinel.MetricBufferPinWaitSessions)] = mkMetric(6, 10, 3, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "LWLock", WaitEvent: "BufferContent", Count: 10, Percentage: 35},
			{WaitEventType: "IO", WaitEvent: "extend", Count: 6, Percentage: 20},
		}
		scenarios = append(scenarios, scenario{
			ID: "T010", Name: "并发 INSERT 热点页争用", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"buffer", "Buffer", "lwlock", "LWLock", "io", "IO", "争用", "等待"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T011: SHARE UPDATE EXCLUSIVE 锁阻塞 DDL
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "relation", Count: 5, Percentage: 30},
		}
		scenarios = append(scenarios, scenario{
			ID: "T011", Name: "并发索引创建被阻塞", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T012: TRUNCATE 阻塞读
	{
		r := baseReport()
		r.PeakActive = 25
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(20, 25, 15, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(25, 30, 20, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "relation", Count: 20, Percentage: 70},
		}
		scenarios = append(scenarios, scenario{
			ID: "T012", Name: "TRUNCATE 阻塞读查询", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T013: 预备事务未清理
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricXIDAgeRatio)] = mkMetric(60, 65, 55, "rising")
		r.Metrics[string(sentinel.MetricDeadTupleRatio)] = mkMetric(20, 25, 15, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		scenarios = append(scenarios, scenario{
			ID: "T013", Name: "预备事务未清理", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"xid", "XID", "回卷", "wraparound", "vacuum", "VACUUM", "prepared"},
			ExpectMinSeverity: SeverityHigh, ExpectMinActions: 1,
		})
	}

	// T014: 分区表 DDL 锁粒度
	{
		r := baseReport()
		r.PeakActive = 20
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(12, 18, 8, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "relation", Count: 12, Percentage: 50},
		}
		scenarios = append(scenarios, scenario{
			ID: "T014", Name: "分区表 DDL 锁粒度", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T015: 锁等待超时重试风暴
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(8, 15, 3, "rising")
		r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(20, 30, 10, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 18, 8, "rising")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 8, Percentage: 35},
		}
		scenarios = append(scenarios, scenario{
			ID: "T015", Name: "锁等待超时重试风暴", Category: "锁与阻塞",
			Report: r, ExpectCauseKeywords: []string{"锁", "lock", "error", "错误"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// ── Category 2: SQL 性能 (T016-T035) ──

	// T016: 全表扫描缺索引
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(500000, 800000, 200000, "rising")
		r.Metrics[string(sentinel.MetricTupFetchedRate)] = mkMetric(100, 200, 50, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q16", Query: "SELECT * FROM big_table WHERE col1 = 'val_123'", Calls: 50, MeanTimeSec: 15, ActiveCount: 5, WaitEventType: "IO", WaitEvent: "DataFileRead"},
		}
		scenarios = append(scenarios, scenario{
			ID: "T016", Name: "全表扫描缺索引", Category: "SQL性能",
			Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "全扫", "索引", "scan", "query", "查询"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T017: 全表扫描选择度低
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(4, 6, 2, "steady")
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(300000, 400000, 200000, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "steady")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q17", Query: "SELECT * FROM big_table WHERE status = 'active'", Calls: 20, MeanTimeSec: 8, ActiveCount: 3},
		}
		scenarios = append(scenarios, scenario{
			ID: "T017", Name: "全表扫描选择度低", Category: "SQL性能",
			Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "scan", "query", "查询", "io", "IO"},
			ExpectMinSeverity: SeverityLow, ExpectMinActions: 1,
		})
	}

	// T018: 执行计划漂移统计信息过期
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(500, 800, 200, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q18", Query: "SELECT * FROM orders WHERE status='pending' AND created_at > '2026-01-01'", Calls: 100, MeanTimeSec: 0.5, MaxTimeSec: 5, ActiveCount: 3},
		}
		scenarios = append(scenarios, scenario{
			ID: "T018", Name: "执行计划漂移统计过期", Category: "SQL性能",
			Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询", "计划"},
			ExpectMinSeverity: SeverityMedium, ExpectMinActions: 1,
		})
	}

	// T019-T035: More SQL performance scenarios
	for _, sc := range buildSQLPerfScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 3: VACUUM 与 MVCC (T036-T045) ──
	for _, sc := range buildVacuumScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 4: 内存与缓存 (T046-T055) ──
	for _, sc := range buildMemoryScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 5: WAL 与 Checkpoint (T056-T065) ──
	for _, sc := range buildWALScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 6: 等待事件与延迟 (T066-T075) ──
	for _, sc := range buildWaitEventScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 7: 连接与会话 (T076-T085) ──
	for _, sc := range buildConnectionScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 8: 配置与参数 (T086-T090) ──
	for _, sc := range buildConfigScenarios() {
		scenarios = append(scenarios, sc)
	}

	// ── Category 9: 系统与运维 (T091-T100) ──
	for _, sc := range buildSystemScenarios() {
		scenarios = append(scenarios, sc)
	}

	return scenarios
}

// ── SQL Performance T019-T035 ──────────────────────────────────────────────

func buildSQLPerfScenarios() []scenario {
	var ss []scenario

	// T019: CTE 物化性能差
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(6, 10, 4, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q19", Query: "WITH cte AS (SELECT * FROM big_table) SELECT * FROM cte WHERE id < 10", Calls: 5, MeanTimeSec: 30, ActiveCount: 3},
		}
		ss = append(ss, scenario{ID: "T019", Name: "CTE 物化性能差", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T020: Nested Loop 低效
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q20", Query: "SELECT * FROM orders o JOIN users u ON o.user_id = u.id WHERE o.status='pending'", Calls: 10, MeanTimeSec: 20, ActiveCount: 3, WaitEventType: "IO"},
		}
		ss = append(ss, scenario{ID: "T020", Name: "Nested Loop 低效", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询", "io", "IO"}, ExpectMinSeverity: SeverityMedium})
	}

	// T021: Hash Join 溢出到磁盘
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(50000000, 80000000, 20000000, "rising")
		r.Metrics[string(sentinel.MetricTempFilesRate)] = mkMetric(20, 30, 10, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q21", Query: "SELECT * FROM big_table a JOIN big_table b ON a.col1 = b.col2", Calls: 2, MeanTimeSec: 60, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T021", Name: "Hash Join 溢出磁盘", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "work_mem", "溢出", "排序"}, ExpectMinSeverity: SeverityMedium})
	}

	// T022: 大排序溢出
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(80000000, 120000000, 40000000, "rising")
		r.Metrics[string(sentinel.MetricTempFilesRate)] = mkMetric(30, 50, 15, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q22", Query: "SELECT * FROM big_table ORDER BY col1, col2", Calls: 3, MeanTimeSec: 45, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T022", Name: "大排序溢出", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "排序", "work_mem"}, ExpectMinSeverity: SeverityMedium})
	}

	// T023: 隐式类型转换
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(6, 10, 4, "rising")
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(400000, 600000, 200000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q23", Query: "SELECT * FROM big_table WHERE email = 123", Calls: 100, MeanTimeSec: 5, ActiveCount: 4},
		}
		ss = append(ss, scenario{ID: "T023", Name: "隐式类型转换", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T024: 函数索引失效
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(3000, 5000, 1000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q24", Query: "SELECT * FROM users WHERE LOWER(name) = 'john'", Calls: 50, MeanTimeSec: 3, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T024", Name: "函数索引失效", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T025: LIKE '%keyword%'
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q25", Query: "SELECT * FROM big_table WHERE col1 LIKE '%search%'", Calls: 20, MeanTimeSec: 10, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T025", Name: "LIKE 前模糊全扫", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T026: 分区裁剪失效
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(600000, 900000, 300000, "rising")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q26", Query: "SELECT * FROM part_table WHERE DATE_TRUNC('month', created_at) = '2026-01-01'", Calls: 10, MeanTimeSec: 15, ActiveCount: 3},
		}
		ss = append(ss, scenario{ID: "T026", Name: "分区裁剪失效", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询", "io", "IO"}, ExpectMinSeverity: SeverityMedium})
	}

	// T027: NOT IN 子查询
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q27", Query: "SELECT * FROM users WHERE id NOT IN (SELECT user_id FROM orders)", Calls: 5, MeanTimeSec: 25, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T027", Name: "NOT IN 子查询陷阱", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T028: OFFSET 大分页
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(2000, 5000, 500, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(6, 8, 4, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q28", Query: "SELECT * FROM big_table ORDER BY id LIMIT 20 OFFSET 500000", Calls: 100, MeanTimeSec: 2, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T028", Name: "OFFSET 大分页退化", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityLow})
	}

	// T029: 相关子查询 N+1
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(6, 10, 4, "rising")
		r.Metrics[string(sentinel.MetricTupFetchedRate)] = mkMetric(500000, 800000, 200000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q29", Query: "SELECT *, (SELECT count(*) FROM orders WHERE orders.user_id=users.id) FROM users", Calls: 5, MeanTimeSec: 30, ActiveCount: 3},
		}
		ss = append(ss, scenario{ID: "T029", Name: "相关子查询 N+1", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T030: JSONB 查询无 GIN
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q30", Query: "SELECT * FROM jsonb_table WHERE profile @> '{\"status\":\"active\"}'", Calls: 50, MeanTimeSec: 5, ActiveCount: 3},
		}
		ss = append(ss, scenario{ID: "T030", Name: "JSONB 无 GIN 索引", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityMedium})
	}

	// T031: 多列索引顺序不当
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(1500, 3000, 500, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(6, 8, 4, "steady")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "steady")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q31", Query: "SELECT * FROM big_table WHERE status='active' AND created_at > '2026-01-01'", Calls: 200, MeanTimeSec: 1.5, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T031", Name: "多列索引顺序不当", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询"}, ExpectMinSeverity: SeverityLow})
	}

	// T032: 过多索引写入慢
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricXactCommitRate)] = mkMetric(100, 150, 50, "falling")
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(500, 1000, 200, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q32", Query: "INSERT INTO idx_test (col1,col2,col3,col4,col5,col6) VALUES ($1,$2,$3,$4,$5,$6)", Calls: 1000, MeanTimeSec: 0.05, ActiveCount: 5},
		}
		ss = append(ss, scenario{ID: "T032", Name: "过多索引写入慢", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "提交", "commit", "吞吐"}, ExpectMinSeverity: SeverityLow})
	}

	// T033: 大表 COUNT(*)
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(3, 5, 2, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(6, 8, 4, "steady")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q33", Query: "SELECT COUNT(*) FROM big_table", Calls: 3, MeanTimeSec: 30, ActiveCount: 2, WaitEventType: "IO"},
		}
		ss = append(ss, scenario{ID: "T033", Name: "大表 COUNT(*)", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "query", "查询", "io", "IO"}, ExpectMinSeverity: SeverityLow})
	}

	// T034: SELECT DISTINCT 大结果集
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(40000000, 60000000, 20000000, "rising")
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q34", Query: "SELECT DISTINCT col1, col2 FROM big_table", Calls: 2, MeanTimeSec: 40, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T034", Name: "DISTINCT 大排序", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "慢查询", "排序"}, ExpectMinSeverity: SeverityMedium})
	}

	// T035: Window Function 无索引
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(30000000, 50000000, 15000000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q35", Query: "SELECT ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC) FROM orders", Calls: 5, MeanTimeSec: 20, ActiveCount: 2},
		}
		ss = append(ss, scenario{ID: "T035", Name: "Window Function 无索引", Category: "SQL性能", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "慢查询", "sql", "SQL"}, ExpectMinSeverity: SeverityMedium})
	}

	return ss
}

// ── VACUUM & MVCC T036-T045 ────────────────────────────────────────────────

func buildVacuumScenarios() []scenario {
	var ss []scenario

	mkVacuumReport := func(deadTupleRatio, xidAge, oldestXact float64, avWorkers int) *sentinel.BurstReport {
		r := baseReport()
		r.Metrics[string(sentinel.MetricDeadTupleRatio)] = mkMetric(deadTupleRatio, deadTupleRatio*1.2, deadTupleRatio*0.8, "rising")
		r.Metrics[string(sentinel.MetricXIDAgeRatio)] = mkMetric(xidAge, xidAge*1.1, xidAge*0.9, "rising")
		r.Metrics[string(sentinel.MetricOldestXactAgeSec)] = mkMetric(oldestXact, oldestXact*1.2, oldestXact*0.8, "rising")
		r.Metrics[string(sentinel.MetricAutovacuumWorkers)] = mkMetric(float64(avWorkers), float64(avWorkers), float64(avWorkers), "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 8, 3, "steady")
		return r
	}

	ss = append(ss,
		scenario{ID: "T036", Name: "Dead Tuple 堆积: autovacuum 跟不上", Category: "VACUUM",
			Report: mkVacuumReport(25, 30, 100, 3), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "autovacuum", "膨胀"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T037", Name: "Dead Tuple: 长事务阻止回收", Category: "VACUUM",
			Report: mkVacuumReport(35, 40, 7200, 3), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "事务", "长事务", "autovacuum"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T038", Name: "XID Wraparound 警告", Category: "VACUUM",
			Report: mkVacuumReport(15, 75, 500, 2), ExpectCauseKeywords: []string{"xid", "XID", "回卷", "wraparound", "freeze"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T039", Name: "表膨胀严重", Category: "VACUUM",
			Report: func() *sentinel.BurstReport {
				r := mkVacuumReport(10, 20, 100, 1)
				r.Metrics[string(sentinel.MetricTableBloatPct)] = mkMetric(55, 60, 50, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"膨胀", "bloat", "vacuum", "VACUUM"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T040", Name: "索引膨胀", Category: "VACUUM",
			Report: func() *sentinel.BurstReport {
				r := mkVacuumReport(8, 15, 50, 1)
				r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(200, 400, 100, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"膨胀", "bloat", "vacuum", "索引", "index"}, ExpectMinSeverity: SeverityLow},
		scenario{ID: "T041", Name: "autovacuum 被频繁取消", Category: "VACUUM",
			Report: mkVacuumReport(30, 35, 200, 2), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "autovacuum"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T042", Name: "HOT Update 链过长", Category: "VACUUM",
			Report: func() *sentinel.BurstReport {
				r := mkVacuumReport(12, 15, 50, 1)
				r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(300, 500, 100, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "膨胀", "autovacuum"}, ExpectMinSeverity: SeverityLow},
		scenario{ID: "T043", Name: "VACUUM FULL 长时间阻塞", Category: "VACUUM",
			Report: func() *sentinel.BurstReport {
				r := baseReport()
				r.PeakActive = 20
				r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(15, 20, 10, "rising")
				r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(1, 1, 1, "steady")
				r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
				r.WaitProfile = []sentinel.WaitBucket{{WaitEventType: "Lock", WaitEvent: "relation", Count: 15, Percentage: 65}}
				return r
			}(), ExpectCauseKeywords: []string{"锁", "lock", "阻塞"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T044", Name: "TOAST 表膨胀", Category: "VACUUM",
			Report: func() *sentinel.BurstReport {
				r := mkVacuumReport(18, 20, 100, 2)
				r.Metrics[string(sentinel.MetricTablespaceUsedPct)] = mkMetric(85, 88, 82, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "膨胀", "空间", "容量"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T045", Name: "autovacuum 饥饿大表占 worker", Category: "VACUUM",
			Report: mkVacuumReport(25, 30, 100, 3), ExpectCauseKeywords: []string{"vacuum", "VACUUM", "dead", "autovacuum", "worker"}, ExpectMinSeverity: SeverityMedium},
	)
	return ss
}

// ── Memory & Cache T046-T055 ──────────────────────────────────────────────

func buildMemoryScenarios() []scenario {
	var ss []scenario

	// T046: Buffer Cache 命中率下降 大查询冲刷
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCacheHitPct)] = mkMetric(85, 90, 80, "falling")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		ss = append(ss, scenario{ID: "T046", Name: "Cache 命中率下降: 大查询冲刷", Category: "内存", Report: r, ExpectCauseKeywords: []string{"cache", "缓存", "命中", "shared_buffers", "内存"}, ExpectMinSeverity: SeverityMedium})
	}
	// T047: shared_buffers 太小
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCacheHitPct)] = mkMetric(88, 92, 85, "steady")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(6, 10, 4, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "steady")
		ss = append(ss, scenario{ID: "T047", Name: "shared_buffers 太小", Category: "内存", Report: r, ExpectCauseKeywords: []string{"cache", "缓存", "shared_buffers", "内存"}, ExpectMinSeverity: SeverityMedium})
	}
	// T048: work_mem 不足大量临时文件
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTempFilesRate)] = mkMetric(60, 100, 30, "rising")
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(100000000, 200000000, 50000000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(10, 12, 8, "rising")
		ss = append(ss, scenario{ID: "T048", Name: "work_mem 不足临时文件溢出", Category: "内存", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "work_mem", "溢出"}, ExpectMinSeverity: SeverityMedium})
	}
	// T049: effective_cache_size 设错
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(400000, 600000, 200000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(4, 6, 2, "rising")
		ss = append(ss, scenario{ID: "T049", Name: "effective_cache_size 设错", Category: "内存", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "scan", "effective_cache_size", "io", "IO", "query"}, ExpectMinSeverity: SeverityLow})
	}
	// T050: maintenance_work_mem 不足
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricAutovacuumWorkers)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		ss = append(ss, scenario{ID: "T050", Name: "maintenance_work_mem 不足", Category: "内存", Report: r, ExpectCauseKeywords: []string{"vacuum", "maintenance", "慢查询"}, ExpectMinSeverity: SeverityLow})
	}
	// T051: OOM
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(15, 25, 5, "rising")
		r.Metrics[string(sentinel.MetricConnectionsPct)] = mkMetric(60, 70, 50, "falling")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 15, 2, "falling")
		ss = append(ss, scenario{ID: "T051", Name: "OOM kill", Category: "内存", Report: r, ExpectCauseKeywords: []string{"error", "错误", "连接", "connection"}, ExpectMinSeverity: SeverityHigh})
	}
	// T052: huge_pages 未启用
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(15, 25, 8, "rising")
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(8, 12, 5, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(2, 4, 1, "rising")
		ss = append(ss, scenario{ID: "T052", Name: "huge_pages 未启用", Category: "内存", Report: r, ExpectCauseKeywords: []string{"cpu", "CPU", "活跃", "会话", "慢查询", "query"}, ExpectMinSeverity: SeverityLow})
	}
	// T053: shared_buffers 过大
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricCacheHitPct)] = mkMetric(98, 99, 97, "steady")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(5, 8, 3, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "steady")
		ss = append(ss, scenario{ID: "T053", Name: "shared_buffers 过大 OS cache 不足", Category: "内存", Report: r, ExpectCauseKeywords: []string{"io", "IO", "缓存", "cache"}, ExpectMinSeverity: SeverityLow})
	}
	// T054: temp_buffers 不足
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTempBytesRate)] = mkMetric(50000000, 80000000, 20000000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(6, 8, 4, "steady")
		ss = append(ss, scenario{ID: "T054", Name: "temp_buffers 不足", Category: "内存", Report: r, ExpectCauseKeywords: []string{"临时", "temp", "溢出"}, ExpectMinSeverity: SeverityLow})
	}
	// T055: 频繁 Backend 创建
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricConnectionsPct)] = mkMetric(50, 70, 30, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 40, 5, "rising")
		ss = append(ss, scenario{ID: "T055", Name: "频繁 Backend 创建", Category: "内存", Report: r, ExpectCauseKeywords: []string{"连接", "connection", "session"}, ExpectMinSeverity: SeverityMedium})
	}
	return ss
}

// ── WAL & Checkpoint T056-T065 ────────────────────────────────────────────

func buildWALScenarios() []scenario {
	var ss []scenario

	mkWALReport := func(walRate, checkReq float64) *sentinel.BurstReport {
		r := baseReport()
		r.Metrics[string(sentinel.MetricWALBytesRate)] = mkMetric(walRate, walRate*1.3, walRate*0.7, "rising")
		r.Metrics[string(sentinel.MetricCheckpointsReq)] = mkMetric(checkReq, checkReq*1.5, checkReq*0.5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "steady")
		return r
	}

	ss = append(ss,
		scenario{ID: "T056", Name: "WAL 生成速率过高", Category: "WAL", Report: mkWALReport(100000000, 2), ExpectCauseKeywords: []string{"wal", "WAL", "日志"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T057", Name: "Checkpoint 过于频繁", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := mkWALReport(50000000, 8)
				return r
			}(), ExpectCauseKeywords: []string{"checkpoint", "Checkpoint", "wal", "WAL"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T058", Name: "Checkpoint IO 尖峰", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := mkWALReport(30000000, 3)
				r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(8, 15, 2, "rising")
				r.WaitProfile = []sentinel.WaitBucket{{WaitEventType: "IO", WaitEvent: "DataFileWrite", Count: 8, Percentage: 35}}
				return r
			}(), ExpectCauseKeywords: []string{"checkpoint", "io", "IO", "wal", "WAL"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T059", Name: "WAL Archive 失败", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := mkWALReport(20000000, 1)
				r.Metrics[string(sentinel.MetricArchiveFailCnt)] = mkMetric(5, 8, 2, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"archive", "归档", "wal", "WAL"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T060", Name: "pg_wal 目录膨胀", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := mkWALReport(20000000, 1)
				r.Metrics[string(sentinel.MetricTablespaceUsedPct)] = mkMetric(96, 98, 94, "rising")
				r.Metrics[string(sentinel.MetricReplicationLag)] = mkMetric(3600, 4000, 3000, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"wal", "WAL", "空间", "容量", "复制", "replication"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T061", Name: "Replication Slot 阻止 WAL 回收", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := mkWALReport(15000000, 1)
				r.Metrics[string(sentinel.MetricReplicationLag)] = mkMetric(7200, 8000, 6000, "rising")
				return r
			}(), ExpectCauseKeywords: []string{"复制", "replication", "wal", "WAL", "slot"}, ExpectMinSeverity: SeverityHigh},
		scenario{ID: "T062", Name: "同步提交延迟高", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := baseReport()
				r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(200, 400, 100, "rising")
				r.Metrics[string(sentinel.MetricXactCommitRate)] = mkMetric(100, 150, 50, "falling")
				r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 10, 6, "steady")
				r.WaitProfile = []sentinel.WaitBucket{{WaitEventType: "IO", WaitEvent: "WALSync", Count: 5, Percentage: 25}, {WaitEventType: "IO", WaitEvent: "WALWrite", Count: 3, Percentage: 15}}
				return r
			}(), ExpectCauseKeywords: []string{"wal", "WAL", "io", "IO", "提交", "commit"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T063", Name: "wal_level=logical 额外开销", Category: "WAL", Report: mkWALReport(60000000, 2), ExpectCauseKeywords: []string{"wal", "WAL"}, ExpectMinSeverity: SeverityLow},
		scenario{ID: "T064", Name: "WAL Sender 占满", Category: "WAL",
			Report: func() *sentinel.BurstReport {
				r := baseReport()
				r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(5, 10, 2, "rising")
				r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
				return r
			}(), ExpectCauseKeywords: []string{"error", "错误", "连接", "wal"}, ExpectMinSeverity: SeverityMedium},
		scenario{ID: "T065", Name: "full_page_writes IO 放大", Category: "WAL", Report: mkWALReport(80000000, 4), ExpectCauseKeywords: []string{"wal", "WAL", "checkpoint"}, ExpectMinSeverity: SeverityMedium},
	)
	return ss
}

// ── Wait Events T066-T075 ─────────────────────────────────────────────────

func buildWaitEventScenarios() []scenario {
	var ss []scenario

	// T066-T075: Various wait event scenarios
	waitScenarios := []struct {
		id, name    string
		waitType    string
		waitEvent   string
		waitPct     float64
		extraMetric string
		extraVal    float64
		keywords    []string
	}{
		{"T066", "LWLock:BufferMapping 争用", "LWLock", "BufferMapping", 35, string(sentinel.MetricLWLockWaitSessions), 6, []string{"lwlock", "LWLock", "buffer", "Buffer", "等待"}},
		{"T067", "LWLock:WALInsert 争用", "LWLock", "WALInsert", 30, string(sentinel.MetricLWLockWaitSessions), 5, []string{"lwlock", "LWLock", "wal", "WAL", "等待"}},
		{"T068", "IO:DataFileRead 延迟高", "IO", "DataFileRead", 45, string(sentinel.MetricIOWaitSessions), 10, []string{"io", "IO", "读", "read"}},
		{"T069", "IO:DataFileWrite 延迟高", "IO", "DataFileWrite", 35, string(sentinel.MetricIOWaitSessions), 8, []string{"io", "IO", "写", "write", "checkpoint"}},
		{"T070", "BufferPin 等待", "LWLock", "BufferPin", 25, string(sentinel.MetricBufferPinWaitSessions), 5, []string{"buffer", "Buffer", "pin", "等待"}},
		{"T071", "Client:ClientRead 等待", "Client", "ClientRead", 40, string(sentinel.MetricIdleInTransaction), 8, []string{"client", "Client", "idle", "等待", "连接"}},
		{"T072", "Lock:transactionid 等待", "Lock", "transactionid", 50, string(sentinel.MetricLockWaitSessions), 12, []string{"锁", "lock", "transaction", "等待"}},
		{"T073", "Lock:extend 争用", "LWLock", "extend", 25, string(sentinel.MetricIOWaitSessions), 8, []string{"extend", "io", "IO", "等待", "lwlock"}},
		{"T074", "CPU 自旋等待", "LWLock", "SpinDelay", 30, string(sentinel.MetricLongQueries), 4, []string{"cpu", "CPU", "lwlock", "LWLock", "等待", "慢查询", "query"}},
		{"T075", "IO:WALWrite 延迟", "IO", "WALWrite", 30, string(sentinel.MetricXactCommitRate), 80, []string{"wal", "WAL", "io", "IO", "提交"}},
	}

	for _, ws := range waitScenarios {
		r := baseReport()
		r.PeakActive = 20
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(15, 20, 10, "rising")
		r.Metrics[ws.extraMetric] = mkMetric(ws.extraVal, ws.extraVal*1.5, ws.extraVal*0.5, "rising")
		r.WaitProfile = []sentinel.WaitBucket{{WaitEventType: ws.waitType, WaitEvent: ws.waitEvent, Count: int(ws.waitPct / 5), Percentage: ws.waitPct}}
		ss = append(ss, scenario{
			ID: ws.id, Name: ws.name, Category: "等待事件",
			Report: r, ExpectCauseKeywords: ws.keywords, ExpectMinSeverity: SeverityMedium,
		})
	}
	return ss
}

// ── Connections T076-T085 ─────────────────────────────────────────────────

func buildConnectionScenarios() []scenario {
	var ss []scenario

	connScenarios := []struct {
		id, name  string
		connPct   float64
		active    float64
		idle      float64
		errRate   float64
		keywords  []string
		severity  Severity
	}{
		{"T076", "连接风暴无连接池", 85, 30, 3, 0, []string{"连接", "connection", "饱和", "风暴"}, SeverityHigh},
		{"T077", "Connections 接近上限", 92, 10, 5, 0, []string{"连接", "connection", "饱和", "上限"}, SeverityCritical},
		{"T078", "idle in transaction 堆积", 50, 8, 25, 0, []string{"idle", "transaction", "连接", "空闲"}, SeverityMedium},
		{"T079", "会话泄漏", 70, 5, 3, 0, []string{"连接", "connection", "泄漏", "leak"}, SeverityMedium},
		{"T080", "PgBouncer 池太小", 20, 3, 0, 15, []string{"error", "错误", "连接"}, SeverityMedium},
		{"T081", "idle 连接占满", 96, 5, 2, 0, []string{"连接", "connection", "饱和", "idle"}, SeverityCritical},
		{"T082", "statement_timeout 未设", 30, 8, 0, 0, []string{"慢查询", "timeout", "查询"}, SeverityMedium},
		{"T083", "superuser 连接预留不足", 100, 5, 0, 20, []string{"连接", "connection", "饱和", "error"}, SeverityCritical},
		{"T084", "Active Sessions 突增", 50, 50, 3, 0, []string{"活跃", "active", "session", "会话"}, SeverityHigh},
		{"T085", "terminate 导致回滚风暴", 40, 5, 2, 30, []string{"error", "错误", "rollback", "回滚"}, SeverityMedium},
	}

	for _, cs := range connScenarios {
		r := baseReport()
		r.PeakActive = int(cs.active * 1.2)
		r.Metrics[string(sentinel.MetricConnectionsPct)] = mkMetric(cs.connPct, cs.connPct+5, cs.connPct-5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(cs.active, cs.active*1.3, cs.active*0.7, "rising")
		r.Metrics[string(sentinel.MetricIdleInTransaction)] = mkMetric(cs.idle, cs.idle*1.5, cs.idle*0.5, "steady")
		if cs.errRate > 0 {
			r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(cs.errRate, cs.errRate*1.5, cs.errRate*0.5, "rising")
		}

		// Special metrics for specific scenarios
		switch cs.id {
		case "T082":
			r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(6, 10, 3, "rising")
		case "T084":
			r.PeakActive = 50
		case "T085":
			r.Metrics[string(sentinel.MetricXactRollbackRate)] = mkMetric(50, 80, 20, "rising")
		}

		ss = append(ss, scenario{
			ID: cs.id, Name: cs.name, Category: "连接",
			Report: r, ExpectCauseKeywords: cs.keywords, ExpectMinSeverity: cs.severity,
		})
	}
	return ss
}

// ── Config T086-T090 ──────────────────────────────────────────────────────

func buildConfigScenarios() []scenario {
	var ss []scenario

	// T086: random_page_cost 过高
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTupReturnedRate)] = mkMetric(500000, 700000, 300000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "q86", Query: "SELECT * FROM big_table WHERE email='test@test.com'", Calls: 100, MeanTimeSec: 2, ActiveCount: 3},
		}
		ss = append(ss, scenario{ID: "T086", Name: "random_page_cost 过高", Category: "配置", Report: r, ExpectCauseKeywords: []string{"慢查询", "sql", "SQL", "scan", "query"}, ExpectMinSeverity: SeverityLow})
	}
	// T087: 并行查询禁用
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "steady")
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(2, 3, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		ss = append(ss, scenario{ID: "T087", Name: "并行查询禁用", Category: "配置", Report: r, ExpectCauseKeywords: []string{"慢查询", "query", "查询", "parallel"}, ExpectMinSeverity: SeverityLow})
	}
	// T088: 慢查询日志未开
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(500, 1000, 200, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(4, 6, 2, "rising")
		ss = append(ss, scenario{ID: "T088", Name: "慢查询日志未开", Category: "配置", Report: r, ExpectCauseKeywords: []string{"慢查询", "log", "query", "查询"}, ExpectMinSeverity: SeverityLow})
	}
	// T089: default_statistics_target 低
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(800, 1200, 400, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		ss = append(ss, scenario{ID: "T089", Name: "statistics_target 过低", Category: "配置", Report: r, ExpectCauseKeywords: []string{"慢查询", "query", "查询", "统计"}, ExpectMinSeverity: SeverityLow})
	}
	// T090: shared_preload_libraries 缺扩展
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		ss = append(ss, scenario{ID: "T090", Name: "缺少关键扩展", Category: "配置", Report: r, ExpectCauseKeywords: []string{"pg_stat_statements", "扩展", "extension", "监控"}, ExpectMinSeverity: SeverityLow})
	}
	return ss
}

// ── System & Operations T091-T100 ─────────────────────────────────────────

func buildSystemScenarios() []scenario {
	var ss []scenario

	// T091: pg_stat_statements 重置
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(3, 5, 2, "rising")
		ss = append(ss, scenario{ID: "T091", Name: "pg_stat_statements 重置", Category: "系统", Report: r, ExpectCauseKeywords: []string{"pg_stat_statements", "监控"}, ExpectMinSeverity: SeverityLow})
	}
	// T092: pg_hba.conf 过于宽松
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(10, 20, 5, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(8, 12, 5, "rising")
		ss = append(ss, scenario{ID: "T092", Name: "pg_hba 过于宽松", Category: "系统", Report: r, ExpectCauseKeywords: []string{"error", "错误", "连接"}, ExpectMinSeverity: SeverityMedium})
	}
	// T093: 版本升级后统计丢失
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(1000, 2000, 500, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(5, 8, 3, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 10, "rising")
		ss = append(ss, scenario{ID: "T093", Name: "升级后统计信息丢失", Category: "系统", Report: r, ExpectCauseKeywords: []string{"慢查询", "query", "查询", "sql", "SQL"}, ExpectMinSeverity: SeverityMedium})
	}
	// T094: 扩展版本不兼容
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricBackendErrorRate)] = mkMetric(20, 30, 10, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		ss = append(ss, scenario{ID: "T094", Name: "扩展版本不兼容", Category: "系统", Report: r, ExpectCauseKeywords: []string{"error", "错误"}, ExpectMinSeverity: SeverityMedium})
	}
	// T095: 日志撑满磁盘
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricTablespaceUsedPct)] = mkMetric(96, 98, 94, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		ss = append(ss, scenario{ID: "T095", Name: "日志撑满磁盘", Category: "系统", Report: r, ExpectCauseKeywords: []string{"空间", "容量", "tablespace", "磁盘"}, ExpectMinSeverity: SeverityHigh})
	}
	// T096: 并发 ANALYZE 冲突
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricAvgQueryTimeMs)] = mkMetric(500, 1000, 100, "rising")
		r.Metrics[string(sentinel.MetricAutovacuumWorkers)] = mkMetric(3, 3, 2, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(12, 15, 8, "rising")
		r.Metrics[string(sentinel.MetricDeadTupleRatio)] = mkMetric(12, 15, 8, "rising")
		ss = append(ss, scenario{ID: "T096", Name: "并发 ANALYZE 冲突", Category: "系统", Report: r, ExpectCauseKeywords: []string{"vacuum", "VACUUM", "autovacuum", "慢查询"}, ExpectMinSeverity: SeverityLow})
	}
	// T097: REINDEX 阻塞写入
	{
		r := baseReport()
		r.PeakActive = 20
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(15, 20, 10, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(1, 1, 1, "steady")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 25, 15, "rising")
		r.WaitProfile = []sentinel.WaitBucket{{WaitEventType: "Lock", WaitEvent: "relation", Count: 15, Percentage: 65}}
		ss = append(ss, scenario{ID: "T097", Name: "REINDEX 阻塞写入", Category: "系统", Report: r, ExpectCauseKeywords: []string{"锁", "lock", "阻塞"}, ExpectMinSeverity: SeverityHigh})
	}
	// T098: 逻辑复制冲突
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricReplicationLag)] = mkMetric(3600, 5000, 2000, "rising")
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(5, 6, 4, "steady")
		ss = append(ss, scenario{ID: "T098", Name: "逻辑复制冲突", Category: "系统", Report: r, ExpectCauseKeywords: []string{"复制", "replication", "延迟", "lag"}, ExpectMinSeverity: SeverityHigh})
	}
	// T099: pg_cron 任务堆叠
	{
		r := baseReport()
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(20, 30, 5, "rising")
		r.Metrics[string(sentinel.MetricCPUSessions)] = mkMetric(10, 15, 3, "rising")
		r.Metrics[string(sentinel.MetricLongQueries)] = mkMetric(5, 8, 3, "rising")
		r.TopSQLs = []sentinel.SQLProfile{
			{QueryID: "cron1", Query: "SELECT cron_job_cleanup()", Calls: 10, MeanTimeSec: 300, ActiveCount: 5},
		}
		ss = append(ss, scenario{ID: "T099", Name: "pg_cron 任务堆叠", Category: "系统", Report: r, ExpectCauseKeywords: []string{"慢查询", "query", "查询", "活跃"}, ExpectMinSeverity: SeverityMedium})
	}
	// T100: 综合场景 IO+Lock+WAL
	{
		r := baseReport()
		r.PeakActive = 30
		r.Metrics[string(sentinel.MetricActiveSessions)] = mkMetric(25, 30, 20, "rising")
		r.Metrics[string(sentinel.MetricIOWaitSessions)] = mkMetric(10, 15, 7, "rising")
		r.Metrics[string(sentinel.MetricLockWaitSessions)] = mkMetric(10, 15, 7, "rising")
		r.Metrics[string(sentinel.MetricWALBytesRate)] = mkMetric(80000000, 120000000, 40000000, "rising")
		r.Metrics[string(sentinel.MetricBlockerCount)] = mkMetric(2, 3, 1, "steady")
		r.WaitProfile = []sentinel.WaitBucket{
			{WaitEventType: "IO", WaitEvent: "DataFileRead", Count: 10, Percentage: 30},
			{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 10, Percentage: 35},
			{WaitEventType: "IO", WaitEvent: "WALWrite", Count: 5, Percentage: 18},
		}
		r.BlockingChains = []sentinel.BlockingChain{
			{BlockerPID: 999, BlockerUser: "app", BlockerQuery: "UPDATE ...", VictimCount: 10, WaitEvent: "DataFileRead"},
		}
		ss = append(ss, scenario{ID: "T100", Name: "综合: IO+Lock+WAL", Category: "系统", Report: r, ExpectCauseKeywords: []string{"锁", "lock", "io", "IO", "wal", "WAL", "阻塞"}, ExpectMinSeverity: SeverityHigh})
	}

	return ss
}

// ─── Main Test ──────────────────────────────────────────────────────────────

func TestPG100Scenarios(t *testing.T) {
	// Build engine with all providers (community + JSON)
	community := &CommunityProvider{}
	jsonProvider := NewJSONRuleProviderFromEmbedded("1.0.0")
	provider := NewCombinedProvider(community, jsonProvider)

	cfg := Config{
		OutputMode:      OutputSQL,
		MaxQueryTimeout: 5,
		MaxTreeDepth:    20,
	}

	// Use mock executor (no live DB needed for rule logic testing)
	mock := &mockExec{results: make(map[QueryID]interface{})}
	engine := New(provider, mock, cfg)

	t.Logf("Rule engine loaded: %d rules", engine.RuleCount())

	scenarios := buildScenarios()
	t.Logf("Testing %d scenarios", len(scenarios))

	// Collect results
	var results []scoreResult
	totalScore := 0
	matchCount := 0

	for _, sc := range scenarios {
		input := &DiagInput{
			Type:   InputBurstReport,
			Report: sc.Report,
		}

		output, debug := engine.DiagnoseDebug(input)
		sr := scoreOutput(sc, output)
		sr.Debug = debug
		results = append(results, sr)
		totalScore += sr.Total

		if sr.Matched {
			matchCount++
		}

		// Log individual result
		status := "MISS"
		if sr.Matched {
			status = "HIT "
		} else if sr.RootCause > 0 {
			status = "PART"
		}
		t.Logf("[%s] %s %-35s → %3d/100  rule=%-15s cause=%s",
			status, sc.ID, sc.Name, sr.Total, sr.PrimaryRule, truncate(sr.PrimaryCause, 40))
	}

	// Print summary
	t.Logf("\n")
	t.Logf("═══════════════════════════════════════════════════════")
	t.Logf("PG 100 Scenarios — Summary")
	t.Logf("═══════════════════════════════════════════════════════")
	t.Logf("Total scenarios:    %d", len(scenarios))
	t.Logf("Root cause matched: %d (%.0f%%)", matchCount, float64(matchCount)/float64(len(scenarios))*100)
	t.Logf("Average score:      %.1f/100", float64(totalScore)/float64(len(scenarios)))
	t.Logf("")

	// Score distribution
	bands := map[string]int{"≥70": 0, "65-69": 0, "50-64": 0, "<50": 0, "0": 0}
	for _, sr := range results {
		switch {
		case sr.Total >= 70:
			bands["≥70"]++
		case sr.Total >= 65:
			bands["65-69"]++
		case sr.Total >= 50:
			bands["50-64"]++
		case sr.Total > 0:
			bands["<50"]++
		default:
			bands["0"]++
		}
	}
	t.Logf("Score distribution:")
	t.Logf("  ≥70:  %d", bands["≥70"])
	t.Logf("  65-69: %d", bands["65-69"])
	t.Logf("  50-64: %d", bands["50-64"])
	t.Logf("  <50:  %d", bands["<50"])
	t.Logf("  0:    %d", bands["0"])

	// Per-category summary
	t.Logf("")
	t.Logf("Per-category:")
	catScores := make(map[string][]int)
	catMatches := make(map[string]int)
	for _, sr := range results {
		catScores[sr.Category] = append(catScores[sr.Category], sr.Total)
		if sr.Matched {
			catMatches[sr.Category]++
		}
	}
	for cat, scores := range catScores {
		sum := 0
		for _, s := range scores {
			sum += s
		}
		avg := float64(sum) / float64(len(scores))
		t.Logf("  %-15s %d scenarios, avg %.1f, matched %d/%d",
			cat, len(scores), avg, catMatches[cat], len(scores))
	}

	// Generate markdown report
	generateMarkdownReport(t, results, scenarios)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func generateMarkdownReport(t *testing.T, results []scoreResult, scenarios []scenario) {
	var b strings.Builder

	b.WriteString("# PG Rule Engine — 100 场景测试结果\n\n")
	b.WriteString(fmt.Sprintf("测试时间: %s\n\n", time.Now().Format("2006-01-02 15:04:05")))

	// Summary table
	totalScore := 0
	matchCount := 0
	for _, r := range results {
		totalScore += r.Total
		if r.Matched {
			matchCount++
		}
	}

	b.WriteString("## 总览\n\n")
	b.WriteString(fmt.Sprintf("| 指标 | 值 |\n"))
	b.WriteString(fmt.Sprintf("|------|----|\n"))
	b.WriteString(fmt.Sprintf("| 总场景 | %d |\n", len(results)))
	b.WriteString(fmt.Sprintf("| 根因命中 | %d (%.0f%%) |\n", matchCount, float64(matchCount)/float64(len(results))*100))
	b.WriteString(fmt.Sprintf("| 平均分 | %.1f |\n", float64(totalScore)/float64(len(results))))

	// Detailed results table
	b.WriteString("\n## 详细结果\n\n")
	b.WriteString("| 场景 | 名称 | 分类 | 根因(40) | 建议(30) | 严重(10) | 路径(10) | 完整(10) | 总分 | 匹配规则 |\n")
	b.WriteString("|------|------|------|---------|---------|---------|---------|---------|------|----------|\n")

	for _, r := range results {
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %d | %d | %d | %d | %d | **%d** | %s |\n",
			r.ID, r.Name, r.Category,
			r.RootCause, r.Recommendations, r.Severity, r.DiagPath, r.Completeness,
			r.Total, r.PrimaryRule))
	}

	t.Logf("\n%s", b.String())
}
