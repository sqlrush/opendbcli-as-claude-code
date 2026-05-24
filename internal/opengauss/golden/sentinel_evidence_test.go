package golden

import (
	"strings"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
)

func TestTier0SentinelEvidenceGoldenCases(t *testing.T) {
	cases := []struct {
		id     string
		report sentinel.BurstReport
		must   []string
	}{
		{
			id:     "OG-GOLDEN-SENTINEL-SLOWSQL-001",
			report: sentinelGoldenReport(sentinel.CauseSlowQuery, sentinel.MetricLongQueries),
			must:   []string{"告警指标", "Baseline vs Current", "当前快照对比", "慢SQL冲高", "/sqltune 581990336", "EXPLAIN PERFORMANCE", "回滚方案"},
		},
		{
			id:     "OG-GOLDEN-SENTINEL-LOCK-001",
			report: sentinelGoldenLockReport(),
			must:   []string{"锁等待阻塞", "blocktree", "PID 2345", "pg_locks", "pg_cancel_backend", "回滚方案"},
		},
		{
			id:     "OG-GOLDEN-SENTINEL-WAL-001",
			report: sentinelGoldenReport(sentinel.CauseWALBottleneck, sentinel.MetricWALBytesRate),
			must:   []string{"WAL冲高", "wal_buffers", "pg_stat_bgwriter", "暂停非业务批量写入", "验证 SQL"},
		},
		{
			id:     "OG-GOLDEN-SENTINEL-IO-001",
			report: sentinelGoldenReport(sentinel.CauseIOBottleneck, sentinel.MetricTempBytesRate),
			must:   []string{"IO瓶颈", "temp_bytes", "pg_stat_activity", "work_mem", "验证 SQL"},
		},
		{
			id:     "OG-GOLDEN-SENTINEL-CONNECTION-001",
			report: sentinelGoldenReport(sentinel.CauseConnectionStorm, sentinel.MetricConnectionsPct),
			must:   []string{"连接数冲高", "pg_stat_activity", "SHOW max_connections", "连接池", "当前快照对比"},
		},
		{
			id:     "OG-GOLDEN-SENTINEL-XID-001",
			report: sentinelGoldenReport(sentinel.CauseXIDWraparound, sentinel.MetricXIDAgeRatio),
			must:   []string{"XID回卷风险", "age(datfrozenxid)", "relfrozenxid", "VACUUM FREEZE", "根因修复"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			out := sentinel.FormatEvidenceDiagnosis(tc.report)
			for _, want := range tc.must {
				if !strings.Contains(out, want) {
					t.Fatalf("%s missing %q:\n%s", tc.id, want, out)
				}
			}
			for _, forbidden := range []string{"Evidence Builder", "WDR_REPORT_BEGIN", "v1_"} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("%s contains forbidden %q:\n%s", tc.id, forbidden, out)
				}
			}
		})
	}
}

func sentinelGoldenReport(cause sentinel.RootCauseType, metric sentinel.MetricName) sentinel.BurstReport {
	metrics := map[string]sentinel.MetricSummary{
		string(sentinel.MetricActiveSessions): {Avg: 16, Max: 28, Min: 5, Trend: "rising"},
		string(metric):                        {Avg: 60, Max: 90, Min: 10, Trend: "spike"},
	}
	return sentinel.BurstReport{
		TriggerEvent:   sentinel.TriggerEvent{Timestamp: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC), Metric: string(metric), Baseline: 10, Current: 90, Threshold: 30, Multiplier: 9},
		StartTime:      time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		EndTime:        time.Date(2026, 5, 24, 10, 0, 30, 0, time.UTC),
		DurationSec:    30,
		PeakActive:     28,
		BaselineActive: 5,
		Metrics:        metrics,
		Classification: sentinel.Classification{Cause: cause, Confidence: 0.84, Evidence: []string{cause.String() + " burst evidence"}},
		TopSQLs:        []sentinel.SQLProfile{{QueryID: "581990336", Query: "select * from bench_orders where created_at >= now() - interval '1 day'", ActiveCount: 6, MaxTimeSec: 90, MeanTimeSec: 20}},
		WaitProfile:    []sentinel.WaitBucket{{WaitEventType: "CPU", WaitEvent: "On CPU", Count: 6, Percentage: 75}},
	}
}

func sentinelGoldenLockReport() sentinel.BurstReport {
	report := sentinelGoldenReport(sentinel.CauseLockContention, sentinel.MetricLockWaits)
	report.WaitProfile = []sentinel.WaitBucket{{WaitEventType: "Lock", WaitEvent: "transactionid", Count: 9, Percentage: 90}}
	report.BlockingChains = []sentinel.BlockingChain{{BlockerPID: 2345, VictimCount: 7, WaitEvent: "transactionid", BlockerQuery: "update orders set status = 'paid' where id = 1"}}
	return report
}
