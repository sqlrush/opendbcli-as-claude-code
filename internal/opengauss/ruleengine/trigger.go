/*-------------------------------------------------------------------------
 *
 * trigger.go
 *	  Trigger evaluator for the openGauss rule engine — runs the
 *	  registered rule set against the latest probe data and emits
 *	  classified anomaly events that Sentinel and /alert consume.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/trigger.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// evaluateTriggers filters candidate rules by evaluating their trigger conditions
// against the current EvalContext.
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
func conditionsMet(rule *Rule, ctx *EvalContext) bool {
	trigger := rule.Trigger

	// Manual rules with keyword match pass without data conditions
	if trigger.Mode == TriggerManual && ctx.Input != nil && ctx.Input.Type == InputUserQuestion {
		return true
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
// OpenGauss only supports "metrics" and "summary" sources.
func evalCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Source {
	case "metrics":
		return evalMetricsCondition(cond, ctx)
	case "summary":
		return evalSummaryCondition(cond, ctx)
	default:
		return false
	}
}

// evalMetricsCondition checks a condition against the metrics summary.
func evalMetricsCondition(cond Condition, ctx *EvalContext) bool {
	metric, ok := ctx.Metrics[cond.Field]
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
