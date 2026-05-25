/*-------------------------------------------------------------------------
 *
 * trigger.go
 *	  MapTriggerSource converts a JSON trigger condition source to an
 *	  internal source string. PG rules use pg_stat_* view names;
 *	  internal engine uses abstract source names.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/trigger.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

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

	// Manual rules only trigger for user questions, never for BurstReport/auto
	if trigger.Mode == TriggerManual {
		if ctx.Input != nil && ctx.Input.Type == InputUserQuestion {
			// For user questions, evaluate conditions if present
			if len(trigger.Conditions) == 0 {
				return true
			}
			// Fall through to evaluate conditions below
		} else {
			// BurstReport/auto: Manual rules never match
			return false
		}
	}

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
	case "metrics":
		return evalMetricsCondition(cond, ctx)
	case "summary":
		return evalSummaryCondition(cond, ctx)
	default:
		// PG has a simpler BurstReport; unknown sources return false.
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
	case OpContains:
		return false // string op not applicable to float
	default:
		return EvalOp(string(op), actual, threshold)
	}
}

// ─── Trigger source mapping ─────────────────────────────────────────────────

// MapTriggerSource converts a JSON trigger condition source to an internal source string.
// PG rules use pg_stat_* view names; internal engine uses abstract source names.
func MapTriggerSource(jsonSource string) string {
	lower := strings.ToLower(jsonSource)

	// Direct mappings
	switch lower {
	case "metrics":
		return "metrics"
	case "summary":
		return "summary"
	}

	// PG system view mappings → metrics
	switch {
	case strings.Contains(lower, "pg_stat_activity"),
		strings.Contains(lower, "pg_stat_database"),
		strings.Contains(lower, "pg_stat_bgwriter"),
		strings.Contains(lower, "pg_stat_wal"),
		strings.Contains(lower, "pg_stat_replication"),
		strings.Contains(lower, "pg_stat_statements"),
		strings.Contains(lower, "pg_stat_user_tables"),
		strings.Contains(lower, "pg_stat_user_indexes"),
		strings.Contains(lower, "pg_stat_io"),
		strings.Contains(lower, "pg_stat_progress"),
		strings.Contains(lower, "pg_locks"),
		strings.Contains(lower, "pg_settings"),
		strings.Contains(lower, "pg_class"),
		strings.Contains(lower, "pg_index"),
		strings.Contains(lower, "pg_tablespace"),
		strings.Contains(lower, "pg_database"),
		strings.Contains(lower, "pg_replication"),
		strings.Contains(lower, "information_schema"),
		strings.Contains(lower, "pg_catalog"):
		return "metrics"
	}

	// Sources that map to metrics as best-effort
	switch lower {
	case "alert_log", "postgresql.conf", "pg_hba.conf",
		"user_request", "requirement", "operation",
		"config", "os_metric", "filesystem":
		return "metrics"
	}

	return "" // unknown source
}
