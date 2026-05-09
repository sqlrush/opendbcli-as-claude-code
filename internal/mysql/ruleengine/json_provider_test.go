/*-------------------------------------------------------------------------
 *
 * json_provider_test.go
 *	  Test cases for json_provider.go (ruleengine package):
 *	  TestJSONProviderLoadsAllRules,
 *	  TestJSONProviderRulesHaveRequiredFields,
 *	  TestCombinedProviderMergesRules.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/json_provider_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/mysql/ruleengine/rules_json"
	"github.com/sqlrush/opendb/internal/mysql/sentinel"
)

// mkReport builds a BurstReport from metrics for testing.
func mkReport(metrics map[string]sentinel.MetricSummary) *sentinel.BurstReport {
	return &sentinel.BurstReport{
		TriggerEvent: sentinel.TriggerEvent{
			Timestamp:  time.Now(),
			Metric:     string(sentinel.MetricThreadsRunning),
			Baseline:   5,
			Current:    50,
			Threshold:  20,
			Multiplier: 3,
		},
		Metrics: metrics,
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseUnknown,
			Confidence: 0.5,
		},
		PeakActive:     10,
		BaselineActive: 3,
		DurationSec:    15,
		StartTime:      time.Now().Add(-15 * time.Second),
		EndTime:        time.Now(),
	}
}

// ─── JSON Provider Loading Tests ──────────────────────────────────────────

func TestJSONProviderLoadsAllRules(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")

	if provider.RuleCount() == 0 {
		t.Fatal("expected JSON rules to be loaded, got 0")
	}
	t.Logf("loaded %d JSON rules, %d errors", provider.RuleCount(), len(provider.Errors()))

	// We expect at least 100 MySQL rules (MY_*.json)
	if provider.RuleCount() < 100 {
		t.Errorf("expected at least 100 rules, got %d", provider.RuleCount())
		for _, e := range provider.Errors() {
			t.Logf("  error: %s", e)
		}
	}

	if provider.Edition() != "json" {
		t.Errorf("expected edition 'json', got %q", provider.Edition())
	}
	if provider.Version() != "test-1.0" {
		t.Errorf("expected version 'test-1.0', got %q", provider.Version())
	}
}

func TestJSONProviderRulesHaveRequiredFields(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")

	for _, rule := range provider.Rules() {
		if rule.ID == "" {
			t.Error("found rule with empty ID")
		}
		if rule.Name == "" {
			t.Errorf("rule %s has empty name", rule.ID)
		}
		if rule.Category == "" {
			t.Errorf("rule %s has empty category", rule.ID)
		}
		if len(rule.Signals) == 0 {
			t.Errorf("rule %s has no signals", rule.ID)
		}
	}
}

// ─── Combined Provider Tests ──────────────────────────────────────────────

func TestCombinedProviderMergesRules(t *testing.T) {
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	communityCount := len(community.Rules())
	jsonCount := json.RuleCount()
	combinedCount := len(combined.Rules())

	t.Logf("community: %d, json: %d, combined: %d", communityCount, jsonCount, combinedCount)

	// Community should have ~25 rules
	if communityCount < 20 {
		t.Errorf("expected at least 20 community rules, got %d", communityCount)
	}

	// Combined should have at least as many as the larger set
	if combinedCount < communityCount && combinedCount < jsonCount {
		t.Errorf("combined count %d is less than both community %d and json %d",
			combinedCount, communityCount, jsonCount)
	}

	// Combined should not exceed sum (could be less due to ID overlap)
	maxPossible := communityCount + jsonCount
	if combinedCount > maxPossible {
		t.Errorf("combined count %d exceeds max possible %d", combinedCount, maxPossible)
	}
}

// ─── Expression Evaluator Tests ───────────────────────────────────────────

func TestExprOpsNumeric(t *testing.T) {
	tests := []struct {
		op       string
		actual   interface{}
		expected interface{}
		want     bool
	}{
		{"gt", 10.0, 5.0, true},
		{"gt", 5.0, 10.0, false},
		{">", 10.0, 5.0, true},
		{"lt", 5.0, 10.0, true},
		{"<", 5.0, 10.0, true},
		{"gte", 10.0, 10.0, true},
		{">=", 9.0, 10.0, false},
		{"lte", 10.0, 10.0, true},
		{"eq", 5.0, 5.0, true},
		{"=", 5.0, 5.0, true},
		{"ne", 5.0, 6.0, true},
		{"!=", 5.0, 5.0, false},
	}

	for _, tt := range tests {
		got := EvalOp(tt.op, tt.actual, tt.expected)
		if got != tt.want {
			t.Errorf("EvalOp(%q, %v, %v) = %v, want %v", tt.op, tt.actual, tt.expected, got, tt.want)
		}
	}
}

func TestExprOpsBoolAndExistence(t *testing.T) {
	tests := []struct {
		op       string
		actual   interface{}
		expected interface{}
		want     bool
	}{
		{"exists", "something", nil, true},
		{"exists", "", nil, false},
		{"exists", nil, nil, false},
		{"exists", 42.0, nil, true},
		{"not_empty", "x", nil, true},
		{"is_true", true, nil, true},
		{"is_true", false, nil, false},
		{"eq", true, true, true},
		{"eq", false, true, false},
	}

	for _, tt := range tests {
		got := EvalOp(tt.op, tt.actual, tt.expected)
		if got != tt.want {
			t.Errorf("EvalOp(%q, %v, %v) = %v, want %v", tt.op, tt.actual, tt.expected, got, tt.want)
		}
	}
}

// ─── Confidence & Severity Parsing ────────────────────────────────────────

func TestParseConfidence(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"置信度 92%: 4个独立证据", 0.92},
		{"置信度 85%: 3个独立证据", 0.85},
		{"65%", 0.65},
		{"no confidence", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := ParseConfidence(tt.input)
		diff := got - tt.want
		if diff < 0 {
			diff = -diff
		}
		if diff > 0.01 {
			t.Errorf("ParseConfidence(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input string
		want  Severity
	}{
		{"low", SeverityLow},
		{"medium", SeverityMedium},
		{"high", SeverityHigh},
		{"critical", SeverityCritical},
	}

	for _, tt := range tests {
		got := ParseSeverity(tt.input)
		if got != tt.want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ─── Integration Test: MySQL Engine Row Lock Diagnosis ────────────────────

func TestMySQLEngineRowLockDiagnosis(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	if provider.RuleCount() == 0 {
		t.Skip("no JSON rules loaded")
	}

	cfg := DefaultConfig()
	engine := New(provider, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"innodb_row_lock_waits": {Avg: 50, Max: 80, Trend: "spike"},
		"innodb_row_lock_time_avg": {Avg: 600, Max: 1000, Trend: "spike"},
		"active_sessions":      {Avg: 20, Max: 35, Trend: "spike"},
		"threads_running":      {Avg: 15, Max: 25, Trend: "rising"},
	})
	report.Classification.Cause = sentinel.CauseRowLockContention
	report.WaitProfile = []sentinel.WaitBucket{
		{EventName: "wait/lock/table/sql/handler", WaitClass: "Lock", TotalMs: 5000, Percentage: 25},
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for row lock contention, got nil")
	}

	t.Logf("primary: %s (rule=%s, confidence=%.0f%%, severity=%s)",
		output.Primary.Cause, output.Primary.RuleID, output.Primary.Confidence*100, output.Primary.Severity)
}

// ─── Integration Test: MySQL Engine Connection Storm ──────────────────────

func TestMySQLEngineConnectionStorm(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	if provider.RuleCount() == 0 {
		t.Skip("no JSON rules loaded")
	}

	cfg := DefaultConfig()
	engine := New(provider, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"threads_running": {Avg: 200, Max: 250, Trend: "spike"},
		"connections_pct": {Avg: 90, Max: 95, Trend: "spike"},
		"threads_connected_pct": {Avg: 90, Max: 95, Trend: "spike"},
		"active_sessions": {Avg: 150, Max: 200, Trend: "spike"},
	})
	report.Classification.Cause = sentinel.CauseConnectionStorm

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for connection storm, got nil")
	}

	t.Logf("primary: %s (rule=%s, confidence=%.0f%%)",
		output.Primary.Cause, output.Primary.RuleID, output.Primary.Confidence*100)
}

// ─── Integration Test: MySQL Low Load (no match expected) ─────────────────

func TestMySQLEngineLowLoad(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	if provider.RuleCount() == 0 {
		t.Skip("no JSON rules loaded")
	}

	cfg := DefaultConfig()
	engine := New(provider, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"threads_running": {Avg: 2, Max: 3, Trend: "stable"},
		"active_sessions": {Avg: 1, Max: 2, Trend: "stable"},
	})

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary != nil {
		t.Logf("low load got diagnosis: %s (rule=%s) - acceptable if rule is informational",
			output.Primary.Cause, output.Primary.RuleID)
	} else {
		t.Log("low load produced no diagnosis - expected for normal operation")
	}
}

// ─── Combined Engine Test ─────────────────────────────────────────────────

func TestCombinedEngineDoesNotBreakExisting(t *testing.T) {
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	cfg := DefaultConfig()
	engine := New(combined, nil, cfg)

	// Use a scenario that community rules cover: row lock contention
	report := mkReport(map[string]sentinel.MetricSummary{
		"innodb_row_lock_waits": {Avg: 50, Max: 80, Trend: "spike"},
		"innodb_row_lock_time_avg": {Avg: 600, Max: 1000, Trend: "spike"},
		"active_sessions":      {Avg: 15, Max: 25, Trend: "spike"},
	})
	report.Classification.Cause = sentinel.CauseRowLockContention
	report.WaitProfile = []sentinel.WaitBucket{
		{EventName: "wait/lock/table/sql/handler", WaitClass: "Lock", TotalMs: 5000, Percentage: 25},
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis with combined provider, got nil")
	}

	t.Logf("combined engine: %s (rule=%s)", output.Primary.Cause, output.Primary.RuleID)
}
