/*-------------------------------------------------------------------------
 *
 * json_provider_test.go
 *	  Test cases for json_provider.go (ruleengine package):
 *	  TestJSONProviderLoadsAllRules,
 *	  TestJSONProviderRulesHaveRequiredFields,
 *	  TestJSONProviderRegistersQueries.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/json_provider_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/oracle/ruleengine/rules_json"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

// ─── JSON Provider Loading Tests ──────────────────────────────────────────

func TestJSONProviderLoadsAllRules(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")

	if provider.RuleCount() == 0 {
		t.Fatal("expected JSON rules to be loaded, got 0")
	}
	t.Logf("loaded %d JSON rules, %d errors", provider.RuleCount(), len(provider.Errors()))

	// We expect at least 100 Oracle rules to load successfully
	if provider.RuleCount() < 100 {
		t.Errorf("expected at least 100 rules, got %d", provider.RuleCount())
		for _, e := range provider.Errors() {
			t.Logf("  error: %s", e)
		}
	}

	// Check basic properties
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

func TestJSONProviderRegistersQueries(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	registry := provider.QueryRegistry()

	// ORA_086 should have diagnostic queries registered
	qid := MakeDynamicQueryID("ORA_086", "step1_victim_impact")
	sql, ok := registry.Lookup(qid)
	if !ok {
		t.Fatal("expected ORA_086 step1_victim_impact query to be registered")
	}
	if !strings.Contains(sql, "v$session") {
		t.Errorf("expected query SQL to contain v$session, got: %s", sql[:min(len(sql), 100)])
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

	// Combined should have at least as many as the larger set
	if combinedCount < communityCount && combinedCount < jsonCount {
		t.Errorf("combined count %d is less than both community %d and json %d",
			combinedCount, communityCount, jsonCount)
	}

	// Combined should have at most community + json (could be less due to ID overlap)
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

func TestExprOpsString(t *testing.T) {
	tests := []struct {
		op       string
		actual   interface{}
		expected interface{}
		want     bool
	}{
		{"contains", "hello world", "world", true},
		{"contains", "hello", "world", false},
		{"not_contains", "hello", "world", true},
		{"starts_with", "hello world", "hello", true},
		{"ends_with", "hello world", "world", true},
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
		{"is_null", nil, nil, true},
		{"is_null", "x", nil, false},
		{"is_true", true, nil, true},
		{"is_true", false, nil, false},
		{"is_false", false, nil, true},
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

func TestExprOpsBetween(t *testing.T) {
	got := EvalOp("between", 5.0, []interface{}{1.0, 10.0})
	if !got {
		t.Error("expected 5 between [1, 10] to be true")
	}
	got = EvalOp("between", 15.0, []interface{}{1.0, 10.0})
	if got {
		t.Error("expected 15 between [1, 10] to be false")
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
		{"严重", SeverityCritical},
	}

	for _, tt := range tests {
		got := ParseSeverity(tt.input)
		if got != tt.want {
			t.Errorf("ParseSeverity(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// ─── Signal Mapping Tests ─────────────────────────────────────────────────

func TestMapJSONSignalType(t *testing.T) {
	tests := []struct {
		jsonType string
		key      string
		want     SignalType
	}{
		{"wait_event", "enq: TX - row lock contention", SignalWaitEvent},
		{"error", "ORA-04031", SignalErrorCode},
		{"metric", "active_sessions", SignalMetric},
		{"ash_data", "some data", SignalKeyword},
		{"whatever", "ORA-00600", SignalErrorCode}, // key overrides type
	}

	for _, tt := range tests {
		got := MapJSONSignalType(tt.jsonType, tt.key)
		if got != tt.want {
			t.Errorf("MapJSONSignalType(%q, %q) = %v, want %v", tt.jsonType, tt.key, got, tt.want)
		}
	}
}

// ─── EvalSkipWhen Tests ───────────────────────────────────────────────────

func TestEvalSkipWhen(t *testing.T) {
	ctx := &EvalContext{
		Metrics: map[string]sentinel.MetricSummary{
			"hard_parse": {Avg: 3, Max: 5},
		},
		PeakActive:     10,
		BaselineActive: 5,
	}

	// Should skip: hard_parse < 10
	if !EvalSkipWhen("hard_parse < 10", ctx) {
		t.Error("expected 'hard_parse < 10' to skip (max=5)")
	}

	// Should not skip: hard_parse > 100
	if EvalSkipWhen("hard_parse > 100", ctx) {
		t.Error("expected 'hard_parse > 100' to not skip (max=5)")
	}

	// Empty expression should not skip
	if EvalSkipWhen("", ctx) {
		t.Error("expected empty expression to not skip")
	}
}

// ─── Integration Test: JSON Engine Diagnose ───────────────────────────────

func TestJSONEngineRowLockDiagnosis(t *testing.T) {
	provider := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	if provider.RuleCount() == 0 {
		t.Skip("no JSON rules loaded")
	}

	cfg := DefaultConfig()
	engine := New(provider, nil, cfg)

	// Create a report with TX row lock contention
	report := mkReport(
		[]sentinel.WaitBucket{
			{Event: "enq: TX - row lock contention", WaitClass: "Application", Percentage: 25, TotalMs: 5000},
			{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 15, TotalMs: 3000},
		},
		map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 20, Max: 35, Trend: "spike"},
		},
	)

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for TX row lock contention")
	}

	t.Logf("primary: %s (confidence=%.0f%%, severity=%s)",
		output.Primary.Cause, output.Primary.Confidence*100, output.Primary.Severity)
	t.Logf("findings: %d, actions: %d", len(output.Primary.Findings), len(output.Primary.Actions))
}

func TestCombinedEngineDoesNotBreakExisting(t *testing.T) {
	// The combined engine should still produce results for scenarios
	// that only the community provider covers.
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	cfg := DefaultConfig()
	engine := New(combined, nil, cfg)

	// db file sequential read - covered by community WE001
	report := mkReport(
		[]sentinel.WaitBucket{
			{Event: "db file sequential read", WaitClass: "User I/O", Percentage: 30, TotalMs: 6000},
		},
		map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 15, Max: 25, Trend: "spike"},
		},
	)

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for db file sequential read with combined provider")
	}

	t.Logf("combined engine: %s (rule=%s)", output.Primary.Cause, output.Primary.RuleID)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
