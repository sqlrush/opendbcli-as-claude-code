/*-------------------------------------------------------------------------
 *
 * llm_budget_test.go
 *	  Test cases for llm_budget.go (drone package):
 *	  TestLLMBudget_AllowAndRecord, TestLLMBudget_Cooldown,
 *	  TestLLMBudget_HourlyLimit.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/llm_budget_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import "testing"

func TestLLMBudget_AllowAndRecord(t *testing.T) {
	b := NewLLMBudget()

	allowed, reason := b.CanCallLLM("temp_usage")
	if !allowed {
		t.Fatalf("should be allowed: %s", reason)
	}

	b.RecordCall("temp_usage")
	stats := b.Stats()
	if stats.HourlyCalls != 1 {
		t.Errorf("hourly = %d, want 1", stats.HourlyCalls)
	}
	if stats.DailyCalls != 1 {
		t.Errorf("daily = %d, want 1", stats.DailyCalls)
	}
}

func TestLLMBudget_Cooldown(t *testing.T) {
	b := NewLLMBudget()

	b.RecordCall("temp_usage")

	// Same metric should be on cooldown.
	allowed, reason := b.CanCallLLM("temp_usage")
	if allowed {
		t.Error("should be on cooldown")
	}
	if reason == "" {
		t.Error("reason should not be empty")
	}

	// Different metric should be allowed.
	allowed, _ = b.CanCallLLM("io_latency")
	if !allowed {
		t.Error("different metric should be allowed")
	}
}

func TestLLMBudget_HourlyLimit(t *testing.T) {
	b := NewLLMBudget()
	b.HourlyLimit = 3
	b.CooldownDuration = 0 // disable cooldown for this test

	for i := 0; i < 3; i++ {
		b.RecordCall("metric_" + string(rune('a'+i)))
	}

	allowed, _ := b.CanCallLLM("new_metric")
	if allowed {
		t.Error("should be denied after hourly limit")
	}
	if !b.IsDegraded() {
		t.Error("should be degraded")
	}
}

func TestLLMBudget_RetryLimit(t *testing.T) {
	b := NewLLMBudget()
	b.MaxRetryOnFail = 2
	b.CooldownDuration = 0

	b.RecordCall("stuck_metric")
	b.RecordFailure("stuck_metric")
	b.RecordCall("stuck_metric")
	b.RecordFailure("stuck_metric")

	allowed, reason := b.CanCallLLM("stuck_metric")
	if allowed {
		t.Error("should be denied after max retries")
	}
	if reason == "" {
		t.Error("reason should mention max retries")
	}
}

func TestLLMBudget_SuccessResetsRetry(t *testing.T) {
	b := NewLLMBudget()
	b.CooldownDuration = 0

	b.RecordCall("metric_a")
	b.RecordFailure("metric_a")
	b.RecordSuccess("metric_a")

	allowed, _ := b.CanCallLLM("metric_a")
	if !allowed {
		t.Error("should be allowed after success reset")
	}
}
