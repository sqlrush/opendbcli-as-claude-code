/*-------------------------------------------------------------------------
 *
 * dashboard_bench_test.go
 *	  dashboard_bench_test.go benchmarks the Cerebrate dashboard HTTP
 *	  API handlers. Uses httptest.NewRecorder to measure handler latency
 *	  without network overhead.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  tests/benchmark/dashboard_bench_test.go
 *
 *-------------------------------------------------------------------------
 */
// dashboard_bench_test.go benchmarks the Cerebrate dashboard HTTP API handlers.
// Uses httptest.NewRecorder to measure handler latency without network overhead.
package benchmark_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sqlrush/opendb/internal/cerebrate"
	pb "github.com/sqlrush/opendb/internal/cluster/proto"
)

// newBenchDashboard creates a WebDashboard pre-populated with 6 overlords
// (each with WorkerCount=200, OnlineCount=190), eventCount timeline events,
// and reportCount report entries.
func newBenchDashboard(eventCount, reportCount int) *cerebrate.WebDashboard {
	fm := cerebrate.NewFleetManager("bench-cerebrate")

	for i := 1; i <= 6; i++ {
		id := fmt.Sprintf("overlord-%d", i)
		region := fmt.Sprintf("region-%d", i)
		fm.SetOverlordForBench(id, region, fmt.Sprintf("%s:9200", id),
			pb.AgentState_STATE_RUNNING,
			&pb.HealthScore{Score: 95, Summary: "190/200 online"},
			200, 190,
		)
	}

	wd := cerebrate.NewWebDashboard(fm, ":0")

	// Populate timeline events.
	if eventCount > 0 {
		ts := cerebrate.NewTimelineStore(eventCount + 100)
		for i := 0; i < eventCount; i++ {
			ts.Add(cerebrate.TimelineEvent{
				ID:          fmt.Sprintf("evt-%d", i),
				EventType:   "fault",
				Severity:    "warning",
				OverlordID:  fmt.Sprintf("overlord-%d", (i%6)+1),
				Description: fmt.Sprintf("Benchmark event %d", i),
			})
		}
		wd.SetTimeline(ts)
	}

	// Populate report store.
	if reportCount > 0 {
		rs := cerebrate.NewReportStore(reportCount + 100)
		for i := 0; i < reportCount; i++ {
			sev := "info"
			if i%3 == 0 {
				sev = "warning"
			}
			if i%7 == 0 {
				sev = "critical"
			}
			rs.Add(&cerebrate.ReportEntry{
				ID:       fmt.Sprintf("rpt-%05d", i),
				Severity: sev,
				Source:   fmt.Sprintf("worker:w-%d", i),
				Summary:  fmt.Sprintf("Benchmark report %d", i),
				Markdown: fmt.Sprintf("## Report %d\nSample content for benchmark.", i),
			})
		}
		wd.SetReports(rs)
	}

	return wd
}

func BenchmarkFleetAPI(b *testing.B) {
	wd := newBenchDashboard(0, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/fleet", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		wd.HandleFleetStatusForBench(rec, req)
	}
}

func BenchmarkTopologyAPI(b *testing.B) {
	wd := newBenchDashboard(0, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/topology", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		wd.HandleTopologyForBench(rec, req)
	}
}

func BenchmarkTimelineAPI(b *testing.B) {
	wd := newBenchDashboard(1000, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/timeline?limit=50", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		wd.HandleTimelineForBench(rec, req)
	}
}

func BenchmarkReportList(b *testing.B) {
	wd := newBenchDashboard(0, 500)
	req := httptest.NewRequest(http.MethodGet, "/api/reports?limit=20", nil)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec := httptest.NewRecorder()
		wd.HandleReportListForBench(rec, req)
	}
}
