package wdranalyze

import (
	"strings"
	"testing"
	"time"
)

func TestEvidenceBuilderCapturesWindowWaitSQLAndActions(t *testing.T) {
	report := &WDRReport{
		Header: ReportHeader{
			WindowStart: time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC),
			WindowEnd:   time.Date(2026, 5, 23, 11, 0, 0, 0, time.UTC),
		},
		TimeModel: TimeModelStats{DBTimeSec: 1000, CPUTimeSec: 300, WaitTimeSec: 700},
		Waits:     []WaitEvent{{Name: "lock_wait", Category: "Lock", WaitTimeMS: 300000, AvgWaitMS: 50, PctOfDBTime: 30}},
		TopSQLs:   []TopSQLEntry{{SQLID: "581990336", Calls: 10, AvgTimeMS: 1000, TotalTimeMS: 300000, Sources: []string{"elapsed"}}},
		SectionScores: []SectionScore{{
			Name:       "TopSQL",
			Level:      SectionRisk,
			Summary:    "单 SQL 占比过高",
			KeyMetrics: map[string]string{"Top SQL": "581990336"},
			Rules:      []SectionRule{{ID: "p95_extreme", Level: SectionRisk, Metric: "SQL P95", Observed: "200ms", Threshold: ">=100ms"}},
		}},
	}
	findings := []Finding{{ID: "single_sql_dominant", Severity: SeverityCritical, Category: "sql", Title: "单 SQL 占比过高", Suggestion: "执行 /sqltune 581990336"}}

	md := RenderEvidenceMarkdown(BuildEvidenceBundle(report, findings, nil))
	for _, want := range []string{"结构化证据", "时间窗口", "Top Wait", "Top SQL", "根因链", "执行 /sqltune 581990336"} {
		if !strings.Contains(md, want) {
			t.Fatalf("evidence markdown missing %q:\n%s", want, md)
		}
	}
}
