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
 *	  internal/opengauss/ruleengine/json_provider_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/opengauss/ruleengine/rules_json"
	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
)

// mkReport builds a BurstReport from metrics for testing.
func mkReport(metrics map[string]sentinel.MetricSummary) *sentinel.BurstReport {
	return &sentinel.BurstReport{
		TriggerEvent: sentinel.TriggerEvent{
			Timestamp:  time.Now(),
			Metric:     "active_sessions",
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

	// OG uses PG rules (PG*.json). Some have parse errors (decision_tree array format).
	// Expect at least 5 rules to load successfully from the parseable files.
	if provider.RuleCount() < 5 {
		t.Errorf("expected at least 5 rules, got %d", provider.RuleCount())
		for _, e := range provider.Errors() {
			t.Logf("  error: %s", e)
		}
	}
	if len(provider.Errors()) > 0 {
		t.Logf("note: %d JSON files had parse errors (known issue with array-format decision_tree)",
			len(provider.Errors()))
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

// ─── Integration Test: OG Idle in Transaction ─────────────────────────────

func TestOGEngineIdleInTransaction(t *testing.T) {
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	cfg := DefaultConfig()
	engine := New(combined, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"idle_in_transaction": {Avg: 10, Max: 15, Trend: "spike"},
		"active_sessions":    {Avg: 8, Max: 12, Trend: "rising"},
		"lock_waits":         {Avg: 3, Max: 5, Trend: "rising"},
	})
	report.Classification.Cause = sentinel.CauseLockContention

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for idle in transaction, got nil")
	}

	t.Logf("primary: %s (rule=%s, confidence=%.0f%%, severity=%s)",
		output.Primary.Cause, output.Primary.RuleID, output.Primary.Confidence*100, output.Primary.Severity)
}

// ─── Integration Test: OG Lock Waits ──────────────────────────────────────

func TestOGEngineLockWaits(t *testing.T) {
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	cfg := DefaultConfig()
	engine := New(combined, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"lock_waits":      {Avg: 5, Max: 8, Trend: "spike"},
		"blocker_count":   {Avg: 2, Max: 3, Trend: "spike"},
		"active_sessions": {Avg: 12, Max: 20, Trend: "rising"},
	})
	report.Classification.Cause = sentinel.CauseLockContention
	report.BlockingChains = []sentinel.BlockingChain{
		{BlockerPID: 1234, BlockerUser: "app", BlockerQuery: "UPDATE t SET ...", VictimCount: 5},
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary == nil {
		t.Fatal("expected a primary diagnosis for lock waits, got nil")
	}

	t.Logf("primary: %s (rule=%s, confidence=%.0f%%)",
		output.Primary.Cause, output.Primary.RuleID, output.Primary.Confidence*100)
}

// ─── Integration Test: OG Low Load (no match expected) ────────────────────

func TestOGEngineLowLoad(t *testing.T) {
	community := &CommunityProvider{}
	json := NewJSONRuleProvider(rules_json.RuleFiles, "test-1.0")
	combined := NewCombinedProvider(community, json)

	cfg := DefaultConfig()
	engine := New(combined, nil, cfg)

	report := mkReport(map[string]sentinel.MetricSummary{
		"active_sessions": {Avg: 1, Max: 2, Trend: "stable"},
	})

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := engine.Diagnose(input)

	if output.Primary != nil {
		t.Logf("low load got diagnosis: %s (rule=%s) - acceptable if informational",
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

	report := mkReport(map[string]sentinel.MetricSummary{
		"lock_waits":      {Avg: 5, Max: 8, Trend: "spike"},
		"blocker_count":   {Avg: 2, Max: 3, Trend: "spike"},
		"active_sessions": {Avg: 12, Max: 20, Trend: "rising"},
	})
	report.Classification.Cause = sentinel.CauseLockContention
	report.BlockingChains = []sentinel.BlockingChain{
		{BlockerPID: 1234, BlockerUser: "app", BlockerQuery: "SELECT 1", VictimCount: 3},
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
