/*-------------------------------------------------------------------------
 *
 * store_bench_test.go
 *	  store_bench_test.go benchmarks the ReportStore and TimelineStore
 *	  in-memory ring buffers used by the Cerebrate dashboard.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  tests/benchmark/store_bench_test.go
 *
 *-------------------------------------------------------------------------
 */
// store_bench_test.go benchmarks the ReportStore and TimelineStore
// in-memory ring buffers used by the Cerebrate dashboard.
package benchmark_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/cerebrate"
)

func BenchmarkReportStore_Add(b *testing.B) {
	store := cerebrate.NewReportStore(10000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Add(&cerebrate.ReportEntry{
			ID:        fmt.Sprintf("rpt-%d", i),
			Timestamp: time.Now(),
			Source:    "worker:bench",
			Severity:  "info",
			Summary:   "benchmark report entry",
			Markdown:  "## Benchmark\nContent here.",
		})
	}
}

func BenchmarkReportStore_Get(b *testing.B) {
	store := cerebrate.NewReportStore(1100)
	for i := 0; i < 1000; i++ {
		store.Add(&cerebrate.ReportEntry{
			ID:       fmt.Sprintf("rpt-%05d", i),
			Severity: "info",
			Summary:  fmt.Sprintf("report %d", i),
		})
	}

	// Target a report in the middle of the store.
	targetID := "rpt-00500"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.Get(targetID)
	}
}

func BenchmarkReportStore_List(b *testing.B) {
	store := cerebrate.NewReportStore(1100)
	for i := 0; i < 1000; i++ {
		sev := "info"
		if i%3 == 0 {
			sev = "warning"
		}
		store.Add(&cerebrate.ReportEntry{
			ID:       fmt.Sprintf("rpt-%05d", i),
			Severity: sev,
			Summary:  fmt.Sprintf("report %d", i),
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.List(20, "warning")
	}
}

func BenchmarkTimelineStore_Add(b *testing.B) {
	store := cerebrate.NewTimelineStore(10000)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.Add(cerebrate.TimelineEvent{
			ID:          fmt.Sprintf("evt-%d", i),
			Timestamp:   time.Now(),
			OverlordID:  "overlord-1",
			EventType:   "fault",
			Severity:    "warning",
			Description: "benchmark event",
		})
	}
}

func BenchmarkTimelineStore_List(b *testing.B) {
	store := cerebrate.NewTimelineStore(1100)
	for i := 0; i < 1000; i++ {
		sev := "info"
		if i%2 == 0 {
			sev = "warning"
		}
		store.Add(cerebrate.TimelineEvent{
			ID:         fmt.Sprintf("evt-%05d", i),
			OverlordID: fmt.Sprintf("overlord-%d", (i%6)+1),
			Severity:   sev,
			EventType:  "fault",
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.List(50, "warning", "overlord-1")
	}
}
