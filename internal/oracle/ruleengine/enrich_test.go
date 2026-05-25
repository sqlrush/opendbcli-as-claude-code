/*-------------------------------------------------------------------------
 *
 * enrich_test.go
 *	  Test cases for enrich.go (ruleengine package):
 *	  TestEnrichSQLPerfInsights.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/enrich_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"testing"

	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

func TestEnrichSQLPerfInsights(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "cursor: pin S", Percentage: 50, WaitClass: "Concurrency"},
		},
		TopSQLs: []sentinel.SQLProfile{
			{
				SQLID:         "test123",
				MaxConcurrent: 30,
				AvgElapsedSec: 0.05,
				HasFullScan:   true,
				BufferGets:    500000,
				Executions:    100,
				RowsProcessed: 50,
			},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 30, Max: 30},
		},
	}

	e := New(&CommunityProvider{}, nil, Config{})
	input := &DiagInput{Type: InputBurstReport, Report: report}
	output := e.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("No primary diagnosis")
	}

	t.Logf("Primary cause: %s", output.Primary.Cause)
	t.Logf("Findings count: %d", len(output.Primary.Findings))
	for i, f := range output.Primary.Findings {
		t.Logf("  Finding %d: %s", i+1, f.Desc)
	}

	// Check if SQL performance insights exist.
	found := false
	for _, f := range output.Primary.Findings {
		if f.Desc == "── SQL 性能分析 ──" {
			found = true
		}
	}
	if !found {
		t.Error("SQL 性能分析 header not found in findings")
	}
}
