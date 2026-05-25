/*-------------------------------------------------------------------------
 *
 * debug_test.go
 *	  Test cases for debug.go (ruleengine package):
 *	  TestDiagnoseDebug_CursorPin, TestDiagnoseDebug_LockContention,
 *	  TestDiagnoseDebug_TempSpaceFull.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/debug_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"testing"

	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

func TestDiagnoseDebug_CursorPin(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "cursor: pin S wait on X", WaitClass: "Concurrency", Percentage: 39.3, TotalMs: 12000},
			{Event: "ON CPU", Percentage: 35.0, TotalMs: 10000},
			{Event: "latch free", WaitClass: "Other", Percentage: 10.0, TotalMs: 3000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 53, Max: 53},
			"cpu_sessions":    {Avg: 20, Max: 20},
		},
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "533c5xquqmqa8", OccurrenceRate: 0.8, AvgElapsedSec: 5},
			{SQLID: "0mr7azgm9psws", OccurrenceRate: 0.95, AvgElapsedSec: 0.001},
		},
		Classification: sentinel.Classification{
			Cause: sentinel.CauseBadSQL,
		},
		TriggerEvent: sentinel.TriggerEvent{
			Metric:   "active_sessions",
			Baseline: 1,
			Current:  53,
		},
	}

	provider := &CommunityProvider{}
	cfg := Config{OutputMode: OutputSkill, MaxTreeDepth: 5}
	engine := New(provider, nil, cfg)

	input := &DiagInput{Type: InputBurstReport, Report: report}
	output, debug := engine.DiagnoseDebug(input)

	fmt.Println(debug)
	fmt.Println("===== 诊断结果 =====")
	fmt.Print(FormatDiagOutput(output, cfg))

	if output.Primary == nil {
		t.Error("expected a primary diagnosis for cursor: pin S wait on X scenario")
	}
}

func TestDiagnoseDebug_LockContention(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "enq: TX - row lock contention", WaitClass: "Application", Percentage: 55.0, TotalMs: 20000},
			{Event: "ON CPU", Percentage: 25.0, TotalMs: 8000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 30, Max: 30},
			"lock_sessions":   {Avg: 15, Max: 15},
		},
		BlockingChains: []sentinel.BlockingChain{
			{RootSID: 100, VictimCount: 15, RootSQLID: "abc123"},
		},
		Classification: sentinel.Classification{
			Cause: sentinel.CauseLockContention,
		},
		TriggerEvent: sentinel.TriggerEvent{
			Metric:   "active_sessions",
			Baseline: 2,
			Current:  30,
		},
	}

	provider := &CommunityProvider{}
	cfg := Config{OutputMode: OutputSkill, MaxTreeDepth: 5}
	engine := New(provider, nil, cfg)

	input := &DiagInput{Type: InputBurstReport, Report: report}
	output, debug := engine.DiagnoseDebug(input)

	fmt.Println(debug)
	fmt.Println("===== 诊断结果 =====")
	fmt.Print(FormatDiagOutput(output, cfg))

	if output.Primary == nil {
		t.Error("expected a primary diagnosis for lock contention scenario")
	}
}

func TestDiagnoseDebug_TempSpaceFull(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "direct path write temp", WaitClass: "User I/O", Percentage: 40.0, TotalMs: 15000},
			{Event: "ON CPU", Percentage: 30.0, TotalMs: 10000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 10, Max: 10},
			"temp_used_pct":   {Avg: 100, Max: 100},
		},
		SpaceDetails: []sentinel.SpaceDetail{
			{Name: "TEMP", UsedPct: 100, TotalMB: 366, UsedMB: 366},
		},
		Classification: sentinel.Classification{
			Cause: sentinel.CauseUnknown,
		},
		TriggerEvent: sentinel.TriggerEvent{
			Metric:   "temp_used_pct",
			Baseline: 5,
			Current:  100,
		},
	}

	provider := &CommunityProvider{}
	cfg := Config{OutputMode: OutputSkill, MaxTreeDepth: 5}
	engine := New(provider, nil, cfg)

	input := &DiagInput{Type: InputBurstReport, Report: report}
	output, debug := engine.DiagnoseDebug(input)

	fmt.Println(debug)
	fmt.Println("===== 诊断结果 =====")
	fmt.Print(FormatDiagOutput(output, cfg))

	if output.Primary == nil {
		t.Error("expected a primary diagnosis for temp space full scenario")
	}
}

func TestDiagnoseDebug_CursorPinS(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "cursor: pin S", WaitClass: "Concurrency", Percentage: 52.5, TotalMs: 20000},
			{Event: "ON CPU", Percentage: 35.0, TotalMs: 10000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 59, Max: 59},
		},
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "0mr7azgm9psws", OccurrenceRate: 0.95, AvgElapsedSec: 0.001},
		},
		TriggerEvent: sentinel.TriggerEvent{
			Metric:  "active_sessions",
			Current: 59,
		},
	}

	provider := &CommunityProvider{}
	cfg := Config{OutputMode: OutputSkill, MaxTreeDepth: 5}
	engine := New(provider, nil, cfg)

	input := &DiagInput{Type: InputBurstReport, Report: report}
	output, debug := engine.DiagnoseDebug(input)

	fmt.Println(debug)
	fmt.Println("===== 诊断结果 =====")
	fmt.Print(FormatDiagOutput(output, cfg))

	if output.Primary == nil {
		t.Error("expected a primary diagnosis for cursor: pin S scenario")
	} else if output.Primary.Cause == "" {
		t.Error("primary cause should not be empty")
	}
}
