/*-------------------------------------------------------------------------
 *
 * expr_eval.go
 *	  ExprCheck is a declarative replacement for TreeNode.Check lambda.
 *	  It describes what data to read from the EvalContext.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/expr_eval.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ─── Declarative Expression Types ───────────────────────────────────────────
//
// These replace Go lambda functions for JSON-loaded rules.

// ExprCheck is a declarative replacement for TreeNode.Check lambda.
// It describes what data to read from the EvalContext.
type ExprCheck struct {
	Source   string // "metrics", "wait_profile", "ash", "summary", "query_result", "params"
	Field    string // metric name, event name, query field
	QueryRef string // reference to diagnostic_queries key (triggers query execution)
}

// ExprMatch is a declarative replacement for Branch.Match lambda.
// It describes the condition to check against the tree node's computed value.
type ExprMatch struct {
	IsDefault bool        // true = catch-all branch (always matches)
	Field     string      // for extracting sub-field from query result map
	Op        string      // normalized operator (gt, lt, eq, etc.)
	Value     interface{} // threshold (float64, string, bool, etc.)
}

// ─── Expression Evaluators ─────────────────────────────────────────────────

// EvalTreeCheck evaluates a declarative check expression against the context.
// Returns the computed value (similar to TreeNode.Check lambda).
func EvalTreeCheck(expr *ExprCheck, ctx *EvalContext) interface{} {
	if expr == nil || ctx == nil {
		return nil
	}

	// If QueryRef is set, execute the referenced diagnostic query.
	if expr.QueryRef != "" {
		result, err := ctx.ExecuteQuery(QueryID(expr.QueryRef), nil)
		if err != nil {
			return nil
		}
		return result
	}

	// Read from EvalContext based on source.
	switch expr.Source {
	case "metrics":
		if m, ok := ctx.Metrics[expr.Field]; ok {
			return m.Max
		}
		return nil
	case "wait_profile":
		pct := ctx.WaitPct(expr.Field)
		if pct > 0 {
			return pct
		}
		return nil
	case "ash":
		return ctx.GetFloat("ash", expr.Field)
	case "summary":
		return ctx.GetFloat("summary", expr.Field)
	case "params":
		val := ctx.GetStr("params", expr.Field)
		if val != "" {
			return val
		}
		return nil
	default:
		// Try GetFloat as generic fallback
		val := ctx.GetFloat(expr.Source, expr.Field)
		if val != 0 {
			return val
		}
		return nil
	}
}

// EvalBranchMatch evaluates a declarative match expression against a value.
// Returns true if the branch should be taken (similar to Branch.Match lambda).
func EvalBranchMatch(expr *ExprMatch, value interface{}) bool {
	if expr == nil {
		return true // nil expression = no condition = always match
	}
	if expr.IsDefault {
		return true
	}

	// If field is specified, extract it from value (when value is a map).
	actual := value
	if expr.Field != "" {
		actual = extractField(value, expr.Field)
	}

	return EvalOp(expr.Op, actual, expr.Value)
}

// EvalSkipWhen evaluates a declarative skip expression string against the context.
// Returns true if the rule should be skipped.
// Expression format: "field op value [AND field op value]*"
// If parsing fails, returns false (safe default: do not skip).
func EvalSkipWhen(expr string, ctx *EvalContext) bool {
	if expr == "" || ctx == nil {
		return false
	}

	// Split on AND (case-insensitive)
	parts := splitAND(expr)

	// All parts must be true (AND logic)
	for _, part := range parts {
		if !evalSimpleExpr(part, ctx) {
			return false
		}
	}
	return true
}

// ─── Internal Helpers ──────────────────────────────────────────────────────

// splitAND splits an expression string on " AND " (case-insensitive).
func splitAND(expr string) []string {
	// Use regex for case-insensitive split
	re := regexp.MustCompile(`(?i)\s+AND\s+`)
	return re.Split(expr, -1)
}

// evalSimpleExpr evaluates a simple "field op value" expression.
// Supported formats:
//   - "metric_name > 100"
//   - "metric_name < 5"
//   - "no_lc_contention" (boolean flag, interpreted as "field exists and is falsy")
func evalSimpleExpr(expr string, ctx *EvalContext) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}

	// Try to parse "field op value" with regex
	re := regexp.MustCompile(`^(\w+)\s*([><=!]+|gt|lt|gte|lte|eq|ne)\s*(.+)$`)
	matches := re.FindStringSubmatch(expr)
	if matches != nil {
		field := matches[1]
		op := NormalizeOp(matches[2])
		valueStr := strings.TrimSpace(matches[3])

		// Parse value
		var value interface{}
		if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
			value = f
		} else if b, err := strconv.ParseBool(valueStr); err == nil {
			value = b
		} else {
			value = valueStr
		}

		// Get actual value from context (try multiple sources)
		actual := resolveField(field, ctx)
		return EvalOp(op, actual, value)
	}

	// No operator found — treat as boolean flag check.
	// "no_lc_contention" means "lc_contention does not exist or is zero".
	field := expr
	if strings.HasPrefix(field, "no_") {
		// "no_X" means "X is absent/zero"
		actual := resolveField(field[3:], ctx)
		return !opExists(actual, nil)
	}
	// Plain field name means "field exists and is truthy"
	actual := resolveField(field, ctx)
	return opExists(actual, nil)
}

// resolveField tries to find a field value across multiple context sources.
func resolveField(field string, ctx *EvalContext) interface{} {
	// Try metrics first
	if m, ok := ctx.Metrics[field]; ok {
		return m.Max
	}
	// Try wait profile (for fields ending in _pct)
	if strings.HasSuffix(field, "_pct") {
		eventName := strings.TrimSuffix(field, "_pct")
		eventName = strings.ReplaceAll(eventName, "_", " ")
		pct := ctx.WaitPct(eventName)
		if pct > 0 {
			return pct
		}
	}
	// Try params
	val := ctx.GetStr("params", field)
	if val != "" {
		return val
	}
	// Try summary fields
	switch field {
	case "peak_active":
		return float64(ctx.PeakActive)
	case "baseline_active":
		return ctx.BaselineActive
	case "duration_sec":
		return ctx.DurationSec
	}
	return nil
}

// extractField extracts a named field from a value.
// Supports map[string]interface{} and map[string]float64.
func extractField(value interface{}, field string) interface{} {
	switch m := value.(type) {
	case map[string]interface{}:
		return m[field]
	case map[string]float64:
		v, ok := m[field]
		if ok {
			return v
		}
		return nil
	default:
		return value
	}
}

// ─── Confidence Parsing ────────────────────────────────────────────────────

// ParseConfidence extracts a confidence value from a string like "置信度 92%: 4个独立证据".
// Returns the confidence as 0.0-1.0, or 0 if parsing fails.
func ParseConfidence(s string) float64 {
	if s == "" {
		return 0
	}
	re := regexp.MustCompile(`(\d+)%`)
	matches := re.FindStringSubmatch(s)
	if matches == nil {
		return 0
	}
	pct, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0
	}
	return pct / 100.0
}

// ParseProbability extracts a probability from "35%" format.
// Returns 0.35 for "35%".
func ParseProbability(s string) float64 {
	return ParseConfidence(s)
}

// ─── Severity Parsing ──────────────────────────────────────────────────────

// ParseSeverity converts a JSON severity string to Severity constant.
func ParseSeverity(s string) Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low", "低":
		return SeverityLow
	case "medium", "中":
		return SeverityMedium
	case "high", "高":
		return SeverityHigh
	case "critical", "严重", "紧急":
		return SeverityCritical
	default:
		return 0
	}
}

// ─── Action Type Parsing ───────────────────────────────────────────────────

// ParseActionType converts a JSON action type string to ActionType.
func ParseActionType(s string) ActionType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "urgent", "紧急", "emergency":
		return ActionUrgent
	case "fix", "修复":
		return ActionFix
	case "investigate", "排查", "排查分析":
		return ActionInvestigate
	case "preventive", "预防", "prevent":
		return ActionPrevent
	case "monitor", "监控":
		return ActionInvestigate
	case "recommendation", "建议":
		return ActionFix
	case "caution", "警告":
		return ActionInvestigate
	case "alternative", "替代":
		return ActionFix
	default:
		return ActionInvestigate
	}
}

// FormatConfidenceStr builds a confidence display string.
func FormatConfidenceStr(confidence float64, evidenceCount int) string {
	return fmt.Sprintf("置信度 %d%%: %d个独立证据", int(confidence*100), evidenceCount)
}
