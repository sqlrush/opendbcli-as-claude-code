/*-------------------------------------------------------------------------
 *
 * trigger.go
 *	  Trigger evaluator for the Oracle rule engine — runs the
 *	  registered rule set against the latest probe data and emits
 *	  classified anomaly events that Sentinel and /alert consume.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/trigger.go
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
// An empty condition list is treated as "always pass" (for manual rules).
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
// It reads the appropriate data source based on cond.Source and cond.Field,
// then applies the comparison operator.
func evalCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Source {
	case "wait_profile":
		return evalWaitProfileCondition(cond, ctx)
	case "metrics":
		return evalMetricsCondition(cond, ctx)
	case "blocking_chains":
		return evalBlockingChainsCondition(cond, ctx)
	case "top_sqls":
		return evalTopSQLsCondition(cond, ctx)
	case "space_details":
		return evalSpaceDetailsCondition(cond, ctx)
	case "summary":
		return evalSummaryCondition(cond, ctx)
	default:
		return false
	}
}

// evalWaitProfileCondition checks a condition against the wait profile.
// Field is the wait event name; value is compared against the event's percentage.
func evalWaitProfileCondition(cond Condition, ctx *EvalContext) bool {
	field := strings.ToLower(cond.Field)

	for _, w := range ctx.WaitProfile {
		if strings.ToLower(w.Event) == field || strings.Contains(strings.ToLower(w.Event), field) {
			return compareOp(cond.Op, w.Percentage, cond.Value)
		}
	}

	// For "exists" operator, the event not being found means false
	if cond.Op == OpExists {
		return false
	}

	return false
}

// evalMetricsCondition checks a condition against the metrics summary.
// Field is the metric name; value is compared against the metric's max or avg.
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

	// Compare against the max value by default for threshold checks
	return compareOp(cond.Op, metric.Max, cond.Value)
}

// evalBlockingChainsCondition checks conditions against blocking chains.
// Field can be: "count" (number of chains), "victim_count" (max victims),
// "max_depth" (deepest chain).
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
		// Use the maximum victim count across all chains
		maxVictims := 0
		for _, chain := range ctx.BlockingChains {
			if chain.VictimCount > maxVictims {
				maxVictims = chain.VictimCount
			}
		}
		return compareOp(cond.Op, float64(maxVictims), cond.Value)

	case "max_depth":
		if len(ctx.BlockingChains) == 0 {
			return false
		}
		maxDepth := 0
		for _, chain := range ctx.BlockingChains {
			if chain.MaxDepth > maxDepth {
				maxDepth = chain.MaxDepth
			}
		}
		return compareOp(cond.Op, float64(maxDepth), cond.Value)

	default:
		if cond.Op == OpNotEmpty || cond.Op == OpExists {
			return len(ctx.BlockingChains) > 0
		}
		return false
	}
}

// evalTopSQLsCondition checks conditions against top SQL profiles.
// Field can be: "count", "max_concurrent", "occurrence_rate", "max_elapsed_sec".
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
		// Check the top SQL's max concurrent sessions
		maxConc := 0
		for _, sql := range ctx.TopSQLs {
			if sql.MaxConcurrent > maxConc {
				maxConc = sql.MaxConcurrent
			}
		}
		return compareOp(cond.Op, float64(maxConc), cond.Value)

	case "occurrence_rate":
		if len(ctx.TopSQLs) == 0 {
			return false
		}
		// Check the top SQL's occurrence rate
		return compareOp(cond.Op, ctx.TopSQLs[0].OccurrenceRate, cond.Value)

	case "max_elapsed_sec":
		if len(ctx.TopSQLs) == 0 {
			return false
		}
		maxElapsed := 0.0
		for _, sql := range ctx.TopSQLs {
			if sql.MaxElapsedSec > maxElapsed {
				maxElapsed = sql.MaxElapsedSec
			}
		}
		return compareOp(cond.Op, maxElapsed, cond.Value)

	default:
		if cond.Op == OpNotEmpty || cond.Op == OpExists {
			return len(ctx.TopSQLs) > 0
		}
		return false
	}
}

// evalSpaceDetailsCondition checks conditions against space details.
// Field can be: "count", "max_used_pct", a specific space name.
func evalSpaceDetailsCondition(cond Condition, ctx *EvalContext) bool {
	switch cond.Field {
	case "count":
		if cond.Op == OpNotEmpty {
			return len(ctx.SpaceDetails) > 0
		}
		return compareOp(cond.Op, float64(len(ctx.SpaceDetails)), cond.Value)

	case "max_used_pct":
		if len(ctx.SpaceDetails) == 0 {
			return false
		}
		maxPct := 0.0
		for _, sd := range ctx.SpaceDetails {
			if sd.UsedPct > maxPct {
				maxPct = sd.UsedPct
			}
		}
		return compareOp(cond.Op, maxPct, cond.Value)

	default:
		// Field is a specific space name
		for _, sd := range ctx.SpaceDetails {
			if strings.EqualFold(sd.Name, cond.Field) {
				if cond.Op == OpExists {
					return true
				}
				return compareOp(cond.Op, sd.UsedPct, cond.Value)
			}
		}
		if cond.Op == OpExists {
			return false
		}
		return false
	}
}

// evalSummaryCondition checks conditions against scalar summary fields.
// Field can be: "peak_active", "baseline_active", "duration_sec".
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
		// Floating-point equality with small epsilon
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
		// Fall back to expression evaluator for extended operators
		return EvalOp(string(op), actual, threshold)
	}
}
