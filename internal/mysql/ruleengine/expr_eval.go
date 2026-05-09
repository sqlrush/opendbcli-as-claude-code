/*-------------------------------------------------------------------------
 *
 * expr_eval.go
 *	  ExprCheck is a declarative replacement for TreeNode.Check lambda.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/expr_eval.go
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
type ExprCheck struct {
	Source   string // "metrics", "wait_profile", "summary", "query_result"
	Field    string // metric name, event name, query field
	QueryRef string // reference to diagnostic_queries key (triggers query execution)
}

// ExprMatch is a declarative replacement for Branch.Match lambda.
type ExprMatch struct {
	IsDefault bool        // true = catch-all branch (always matches)
	Field     string      // for extracting sub-field from query result map
	Op        string      // normalized operator (gt, lt, eq, etc.)
	Value     interface{} // threshold (float64, string, bool, etc.)
}

// ─── Expression Evaluators ─────────────────────────────────────────────────

// EvalTreeCheck evaluates a declarative check expression against the context.
func EvalTreeCheck(expr *ExprCheck, ctx *EvalContext) interface{} {
	if expr == nil || ctx == nil {
		return nil
	}

	if expr.QueryRef != "" {
		result, err := ctx.ExecuteQuery(QueryID(expr.QueryRef), nil)
		if err != nil {
			return nil
		}
		return result
	}

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
	case "summary":
		return ctx.GetFloat("summary", expr.Field)
	default:
		val := ctx.GetFloat(expr.Source, expr.Field)
		if val != 0 {
			return val
		}
		return nil
	}
}

// EvalBranchMatch evaluates a declarative match expression against a value.
func EvalBranchMatch(expr *ExprMatch, value interface{}) bool {
	if expr == nil {
		return true
	}
	if expr.IsDefault {
		return true
	}

	actual := value
	if expr.Field != "" {
		actual = extractField(value, expr.Field)
	}

	return EvalOp(expr.Op, actual, expr.Value)
}

// EvalSkipWhen evaluates a declarative skip expression string against the context.
func EvalSkipWhen(expr string, ctx *EvalContext) bool {
	if expr == "" || ctx == nil {
		return false
	}

	parts := splitAND(expr)

	for _, part := range parts {
		if !evalSimpleExpr(part, ctx) {
			return false
		}
	}
	return true
}

// ─── Internal Helpers ──────────────────────────────────────────────────────

func splitAND(expr string) []string {
	re := regexp.MustCompile(`(?i)\s+AND\s+`)
	return re.Split(expr, -1)
}

func evalSimpleExpr(expr string, ctx *EvalContext) bool {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return false
	}

	re := regexp.MustCompile(`^(\w+)\s*([><=!]+|gt|lt|gte|lte|eq|ne)\s*(.+)$`)
	matches := re.FindStringSubmatch(expr)
	if matches != nil {
		field := matches[1]
		op := NormalizeOp(matches[2])
		valueStr := strings.TrimSpace(matches[3])

		var value interface{}
		if f, err := strconv.ParseFloat(valueStr, 64); err == nil {
			value = f
		} else if b, err := strconv.ParseBool(valueStr); err == nil {
			value = b
		} else {
			value = valueStr
		}

		actual := resolveField(field, ctx)
		return EvalOp(op, actual, value)
	}

	field := expr
	if strings.HasPrefix(field, "no_") {
		actual := resolveField(field[3:], ctx)
		return !opExists(actual, nil)
	}
	actual := resolveField(field, ctx)
	return opExists(actual, nil)
}

func resolveField(field string, ctx *EvalContext) interface{} {
	if m, ok := ctx.Metrics[field]; ok {
		return m.Max
	}
	if strings.HasSuffix(field, "_pct") {
		eventName := strings.TrimSuffix(field, "_pct")
		eventName = strings.ReplaceAll(eventName, "_", " ")
		pct := ctx.WaitPct(eventName)
		if pct > 0 {
			return pct
		}
	}
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
