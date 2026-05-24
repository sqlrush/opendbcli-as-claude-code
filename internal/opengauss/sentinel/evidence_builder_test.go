package sentinel

import (
	"strings"
	"testing"
	"time"
)

func TestFormatEvidenceDiagnosisIncludesFullIncidentChain(t *testing.T) {
	report := BurstReport{
		TriggerEvent:   TriggerEvent{Timestamp: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC), Metric: string(MetricLockWaits), Baseline: 1, Current: 8, Threshold: 4, Multiplier: 8},
		StartTime:      time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 5, 23, 10, 0, 30, 0, time.UTC),
		DurationSec:    30,
		PeakActive:     20,
		BaselineActive: 4,
		Metrics: map[string]MetricSummary{
			string(MetricLockWaits):      {Avg: 6, Max: 8, Min: 1, Trend: "spike"},
			string(MetricActiveSessions): {Avg: 12, Max: 20, Min: 4, Trend: "rising"},
			string(MetricConnectionsPct): {Avg: 20, Max: 35, Min: 12, Trend: "stable"},
		},
		Classification: Classification{Cause: CauseLockContention, Confidence: 0.9, Evidence: []string{"锁等待会话峰值: 8"}},
		WaitProfile:    []WaitBucket{{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 8, Percentage: 80}},
		BlockingChains: []BlockingChain{{BlockerPID: 123, VictimCount: 5, WaitEvent: "transactionid", BlockerQuery: "update t set v = v + 1 where id = 1"}},
	}
	out := FormatEvidenceDiagnosis(report)
	for _, want := range []string{
		"告警指标",
		"baseline -> current",
		"Baseline vs Current",
		"Burst 时刻证据",
		"当前快照对比",
		"activesessions",
		"waits",
		"blocktree",
		"主因",
		"紧急措施",
		"根因修复",
		"验证 SQL",
		"回滚方案",
		"PID 123",
		"pg_cancel_backend",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("diagnosis missing %q:\n%s", want, out)
		}
	}
}

func TestSentinelEvidenceContainsCauseSpecificValidation(t *testing.T) {
	cases := []struct {
		name  string
		cause RootCauseType
		want  []string
	}{
		{name: "slow sql", cause: CauseSlowQuery, want: []string{"慢SQL冲高", "/sqltune 1775585557", "EXPLAIN PERFORMANCE", "DROP INDEX"}},
		{name: "wal", cause: CauseWALBottleneck, want: []string{"WAL冲高", "pg_stat_bgwriter", "wal_buffers", "ALTER SYSTEM"}},
		{name: "io", cause: CauseIOBottleneck, want: []string{"IO瓶颈", "pg_stat_database", "temp_bytes", "work_mem"}},
		{name: "connection", cause: CauseConnectionStorm, want: []string{"连接数冲高", "pg_stat_activity", "SHOW max_connections", "连接池"}},
		{name: "vacuum", cause: CauseVacuumLag, want: []string{"Vacuum滞后", "pg_stat_user_tables", "n_dead_tup", "autovacuum"}},
		{name: "xid", cause: CauseXIDWraparound, want: []string{"XID回卷风险", "age(datfrozenxid)", "VACUUM FREEZE", "relfrozenxid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := sampleReportForCause(tc.cause)
			out := FormatEvidenceDiagnosis(report)
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Fatalf("diagnosis for %s missing %q:\n%s", tc.cause, want, out)
				}
			}
		})
	}
}

func sampleReportForCause(cause RootCauseType) BurstReport {
	metric := MetricLongQueries
	summary := MetricSummary{Avg: 3, Max: 9, Min: 1, Trend: "spike"}
	metrics := map[string]MetricSummary{
		string(MetricActiveSessions): {Avg: 12, Max: 24, Min: 4, Trend: "rising"},
		string(metric):               summary,
	}
	sqls := []SQLProfile{{QueryID: "1775585557", Query: "SELECT pg_sleep(10)", ActiveCount: 4, MaxTimeSec: 120, MeanTimeSec: 60}}
	waits := []WaitBucket{{WaitEventType: "CPU", WaitEvent: "On CPU", Count: 4, Percentage: 80}}
	switch cause {
	case CauseWALBottleneck:
		metric = MetricWALBytesRate
		metrics[string(metric)] = MetricSummary{Avg: 1000000, Max: 5000000, Min: 1000, Trend: "rising"}
	case CauseIOBottleneck:
		metric = MetricTempBytesRate
		metrics[string(metric)] = MetricSummary{Avg: 80 * 1024 * 1024, Max: 240 * 1024 * 1024, Min: 20 * 1024 * 1024, Trend: "spike"}
	case CauseConnectionStorm:
		metric = MetricConnectionsPct
		metrics[string(metric)] = MetricSummary{Avg: 75, Max: 92, Min: 40, Trend: "rising"}
	case CauseVacuumLag:
		metric = MetricDeadTupleRatio
		metrics[string(metric)] = MetricSummary{Avg: 25, Max: 42, Min: 10, Trend: "rising"}
	case CauseXIDWraparound:
		metric = MetricXIDAgeRatio
		metrics[string(metric)] = MetricSummary{Avg: 60, Max: 88, Min: 30, Trend: "rising"}
	}
	return BurstReport{
		TriggerEvent:   TriggerEvent{Timestamp: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC), Metric: string(metric), Baseline: 1, Current: summary.Max, Threshold: 2, Multiplier: 3},
		StartTime:      time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 5, 23, 10, 0, 30, 0, time.UTC),
		DurationSec:    30,
		PeakActive:     24,
		BaselineActive: 4,
		Metrics:        metrics,
		Classification: Classification{Cause: cause, Confidence: 0.86, Evidence: []string{cause.String() + " evidence"}},
		TopSQLs:        sqls,
		WaitProfile:    waits,
	}
}
