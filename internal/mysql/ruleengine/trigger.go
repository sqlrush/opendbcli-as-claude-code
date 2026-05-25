/*-------------------------------------------------------------------------
 *
 * trigger.go
 *	  Trigger evaluator for the MySQL rule engine — runs the
 *	  registered rule set against the latest probe data and emits
 *	  classified anomaly events that Sentinel and /alert consume.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/trigger.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// evaluateTriggers filters candidate rules by evaluating their trigger conditions
// against the current EvalContext. A rule passes if:
//   - All Conditions are met (AND logic)
//   - No SkipWhen condition returns true
//
// Rules with TriggerMode == TriggerManual are always included when the input
// is a user question, since they rely on keyword matching rather than data thresholds.
func evaluateTriggers(candidates []*Rule, ctx *EvalContext) []*Rule {
	var matched []*Rule

	for _, rule := range candidates {
		if shouldSkip(rule, ctx) {
			continue
		}

		if !conditionsMet(rule, ctx) {
			continue
		}

		matched = append(matched, rule)
	}

	return matched
}

// shouldSkip returns true if any SkipWhen condition is met.
func shouldSkip(rule *Rule, ctx *EvalContext) bool {
	for _, skip := range rule.Trigger.SkipWhen {
		if skip.Check != nil && skip.Check(ctx) {
			return true
		}
	}
	return false
}

// conditionsMet returns true if all trigger conditions are satisfied.
// For auto/query mode: empty condition list means the rule cannot trigger
// from burst report data alone (requires explicit user invocation).
func conditionsMet(rule *Rule, ctx *EvalContext) bool {
	trigger := rule.Trigger

	// Manual rules with keyword match pass without data conditions
	if trigger.Mode == TriggerManual && ctx.Input != nil && ctx.Input.Type == InputUserQuestion {
		return true
	}

	// Auto/Query rules with no conditions cannot trigger from burst data
	if len(trigger.Conditions) == 0 && ctx.Input != nil && ctx.Input.Type == InputBurstReport {
		return false
	}

	// Auto/Query rules require all conditions to be true
	for _, cond := range trigger.Conditions {
		if !evalCondition(cond, ctx) {
			return false
		}
	}

	return true
}

// evalCondition evaluates a single Condition against the EvalContext.
func evalCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Source {
	case "wait_profile":
		return evalWaitProfileCondition(cond, ctx)
	case "metrics", "status":
		return evalMetricsCondition(cond, ctx)
	case "blocking_chains":
		return evalBlockingChainsCondition(cond, ctx)
	case "top_sqls":
		return evalTopSQLsCondition(cond, ctx)
	case "summary":
		return evalSummaryCondition(cond, ctx)
	default:
		return false
	}
}

// evalWaitProfileCondition checks a condition against the wait profile.
func evalWaitProfileCondition(cond Condition, ctx *EvalContext) bool {
	field := strings.ToLower(cond.Field)

	for _, w := range ctx.WaitProfile {
		if strings.ToLower(w.EventName) == field || strings.Contains(strings.ToLower(w.EventName), field) {
			return compareOp(cond.Op, w.Percentage, cond.Value)
		}
	}

	if cond.Op == OpExists {
		return false
	}

	return false
}

// evalMetricsCondition checks a condition against the metrics summary.
func evalMetricsCondition(cond Condition, ctx *EvalContext) bool {
	field := strings.ToLower(cond.Field)
	metric, ok := ctx.Metrics[field]
	if !ok {
		if cond.Op == OpExists {
			return false
		}
		return false
	}

	if cond.Op == OpExists {
		return metric.Max > 0
	}

	return compareOp(cond.Op, metric.Max, cond.Value)
}

// evalBlockingChainsCondition checks conditions against blocking chains.
func evalBlockingChainsCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Field {
	case "count":
		if cond.Op == OpNotEmpty {
			return len(ctx.BlockingChains) > 0
		}
		return compareOp(cond.Op, float64(len(ctx.BlockingChains)), cond.Value)

	case "victim_count":
		if len(ctx.BlockingChains) == 0 {
			return false
		}
		maxVictims := 0
		for _, chain := range ctx.BlockingChains {
			if chain.VictimCount > maxVictims {
				maxVictims = chain.VictimCount
			}
		}
		return compareOp(cond.Op, float64(maxVictims), cond.Value)

	default:
		if cond.Op == OpNotEmpty || cond.Op == OpExists {
			return len(ctx.BlockingChains) > 0
		}
		return false
	}
}

// evalTopSQLsCondition checks conditions against top SQL profiles.
func evalTopSQLsCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Field {
	case "count":
		if cond.Op == OpNotEmpty {
			return len(ctx.TopSQLs) > 0
		}
		return compareOp(cond.Op, float64(len(ctx.TopSQLs)), cond.Value)

	case "max_concurrent":
		if len(ctx.TopSQLs) == 0 {
			return false
		}
		maxConc := 0
		for _, sql := range ctx.TopSQLs {
			if sql.MaxConcurrent > maxConc {
				maxConc = sql.MaxConcurrent
			}
		}
		return compareOp(cond.Op, float64(maxConc), cond.Value)

	case "max_latency_ms":
		if len(ctx.TopSQLs) == 0 {
			return false
		}
		maxLatency := 0.0
		for _, sql := range ctx.TopSQLs {
			if sql.MaxLatencyMs > maxLatency {
				maxLatency = sql.MaxLatencyMs
			}
		}
		return compareOp(cond.Op, maxLatency, cond.Value)

	default:
		if cond.Op == OpNotEmpty || cond.Op == OpExists {
			return len(ctx.TopSQLs) > 0
		}
		return false
	}
}

// evalSummaryCondition checks conditions against scalar summary fields.
func evalSummaryCondition(cond Condition, ctx *EvalContext) bool {
	var actual float64

	switch cond.Field {
	case "peak_active":
		actual = float64(ctx.PeakActive)
	case "baseline_active":
		actual = ctx.BaselineActive
	case "duration_sec":
		actual = ctx.DurationSec
	default:
		return false
	}

	if cond.Op == OpExists {
		return actual > 0
	}

	return compareOp(cond.Op, actual, cond.Value)
}

// compareOp applies the comparison operator to (actual op threshold).
func compareOp(op CondOp, actual, threshold float64) bool {
	switch op {
	case OpGT:
		return actual > threshold
	case OpLT:
		return actual < threshold
	case OpGTE:
		return actual >= threshold
	case OpLTE:
		return actual <= threshold
	case OpEQ:
		diff := actual - threshold
		if diff < 0 {
			diff = -diff
		}
		return diff < 0.001
	case OpNE:
		diff := actual - threshold
		if diff < 0 {
			diff = -diff
		}
		return diff >= 0.001
	case OpPctGT:
		return actual > threshold
	case OpPctLT:
		return actual < threshold
	case OpExists:
		return actual != 0
	case OpNotEmpty:
		return actual > 0
	default:
		return EvalOp(string(op), actual, threshold)
	}
}
