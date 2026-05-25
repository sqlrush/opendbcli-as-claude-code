/*-------------------------------------------------------------------------
 *
 * llm_budget.go
 *	  llm_budget.go implements LLM call rate limiting and cost control.
 *	  Prevents runaway LLM usage with per-node hourly/daily limits,
 *	  same-anomaly cooldown, and automatic degradation to Rule Engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/llm_budget.go
 *
 *-------------------------------------------------------------------------
 */
// llm_budget.go implements LLM call rate limiting and cost control.
// Prevents runaway LLM usage with per-node hourly/daily limits,
// same-anomaly cooldown, and automatic degradation to Rule Engine.
package drone

import (
	"fmt"
	"sync"
	"time"
)

// LLMBudget tracks and limits LLM usage for a Worker.
type LLMBudget struct {
	mu sync.Mutex

	// Limits (configurable).
	HourlyLimit int // max calls per hour (default 10)
	DailyLimit  int // max calls per day (default 30)

	// Cooldown — same anomaly metric won't trigger LLM again within this window.
	CooldownDuration time.Duration // default 30 minutes
	MaxRetryOnFail   int           // max consecutive LLM calls for same unresolved issue (default 3)

	// Tracking.
	hourlyCalls    int
	dailyCalls     int
	hourlyResetAt  time.Time
	dailyResetAt   time.Time
	cooldowns      map[string]time.Time // metric → cooldown expiry
	retryCount     map[string]int       // metric → consecutive failure count
	degraded       bool                 // true = budget exhausted, using Rule Engine only
}

// NewLLMBudget creates a budget with default limits.
func NewLLMBudget() *LLMBudget {
	now := time.Now()
	return &LLMBudget{
		HourlyLimit:      10,
		DailyLimit:       30,
		CooldownDuration: 30 * time.Minute,
		MaxRetryOnFail:   3,
		hourlyResetAt:    now.Add(1 * time.Hour),
		dailyResetAt:     now.Truncate(24 * time.Hour).Add(24 * time.Hour),
		cooldowns:        make(map[string]time.Time),
		retryCount:       make(map[string]int),
	}
}

// CanCallLLM checks if an LLM call is allowed for the given metric.
// Returns (allowed, reason).
func (b *LLMBudget) CanCallLLM(metric string) (bool, string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	b.resetCountersIfNeeded(now)

	// Check cooldown for same metric.
	if expiry, exists := b.cooldowns[metric]; exists && now.Before(expiry) {
		remaining := expiry.Sub(now).Round(time.Second)
		return false, fmt.Sprintf("cooldown: %s (same anomaly %s, retry in %v)", metric, metric, remaining)
	}

	// Check retry limit for same unresolved metric.
	if b.retryCount[metric] >= b.MaxRetryOnFail {
		return false, fmt.Sprintf("max retries reached for %s (%d/%d), escalate to Overlord", metric, b.retryCount[metric], b.MaxRetryOnFail)
	}

	// Check hourly limit.
	if b.hourlyCalls >= b.HourlyLimit {
		b.degraded = true
		return false, fmt.Sprintf("hourly limit reached (%d/%d)", b.hourlyCalls, b.HourlyLimit)
	}

	// Check daily limit.
	if b.dailyCalls >= b.DailyLimit {
		b.degraded = true
		return false, fmt.Sprintf("daily limit reached (%d/%d)", b.dailyCalls, b.DailyLimit)
	}

	return true, ""
}

// RecordCall records an LLM call and sets cooldown for the metric.
func (b *LLMBudget) RecordCall(metric string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.hourlyCalls++
	b.dailyCalls++
	b.cooldowns[metric] = time.Now().Add(b.CooldownDuration)
}

// RecordSuccess resets the retry counter for a metric (repair succeeded).
func (b *LLMBudget) RecordSuccess(metric string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.retryCount, metric)
}

// RecordFailure increments the retry counter (repair didn't resolve the issue).
func (b *LLMBudget) RecordFailure(metric string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.retryCount[metric]++
}

// IsDegraded returns true if LLM budget is exhausted (Rule Engine only mode).
func (b *LLMBudget) IsDegraded() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.degraded
}

// Stats returns current usage statistics.
func (b *LLMBudget) Stats() BudgetStats {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetStats{
		HourlyCalls:  b.hourlyCalls,
		HourlyLimit:  b.HourlyLimit,
		DailyCalls:   b.dailyCalls,
		DailyLimit:   b.DailyLimit,
		Degraded:     b.degraded,
		CooldownCount: len(b.cooldowns),
	}
}

// BudgetStats holds LLM usage statistics.
type BudgetStats struct {
	HourlyCalls   int  `json:"hourly_calls"`
	HourlyLimit   int  `json:"hourly_limit"`
	DailyCalls    int  `json:"daily_calls"`
	DailyLimit    int  `json:"daily_limit"`
	Degraded      bool `json:"degraded"`
	CooldownCount int  `json:"cooldown_count"`
}

func (b *LLMBudget) resetCountersIfNeeded(now time.Time) {
	if now.After(b.hourlyResetAt) {
		b.hourlyCalls = 0
		b.hourlyResetAt = now.Add(1 * time.Hour)
		if b.dailyCalls < b.DailyLimit {
			b.degraded = false // recover from hourly degradation
		}
	}
	if now.After(b.dailyResetAt) {
		b.dailyCalls = 0
		b.dailyResetAt = now.Truncate(24 * time.Hour).Add(24 * time.Hour)
		b.degraded = false // new day, reset degradation

		// Clean up expired cooldowns.
		for metric, expiry := range b.cooldowns {
			if now.After(expiry) {
				delete(b.cooldowns, metric)
			}
		}
	}
}
