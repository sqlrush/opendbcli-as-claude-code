/*-------------------------------------------------------------------------
 *
 * expr_ops.go
 *	  OpFunc is a comparison function: (actual, expected) → bool.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/expr_ops.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"regexp"
	"strings"
)

// ─── Operator Registry ────────────────────────────────────────────────────────
//
// All 45+ comparison operators used in JSON rules, registered as pure functions.
// Each operator takes (actual, expected) and returns bool.

// OpFunc is a comparison function: (actual, expected) → bool.
type OpFunc func(actual, expected interface{}) bool

// opRegistry maps normalized operator names to their evaluation functions.
var opRegistry = map[string]OpFunc{
	// Numeric comparison
	"gt":  opGT,
	"lt":  opLT,
	"gte": opGTE,
	"lte": opLTE,
	"eq":  opEQ,
	"ne":  opNE,

	// Percentage (same as numeric, kept for clarity)
	"pct_gt": opGT,
	"pct_lt": opLT,

	// Existence
	"exists":    opExists,
	"not_empty": opNotEmpty,
	"is_null":   opIsNull,

	// String
	"contains":     opContains,
	"not_contains": opNotContains,
	"starts_with":  opStartsWith,
	"ends_with":    opEndsWith,
	"matches":      opMatches,
	"like":         opContains, // alias

	// Count-based
	"count_gt": opGT,
	"count_lt": opLT,
	"count_eq": opEQ,

	// Rate-based (same as numeric after extraction)
	"rate_gt":         opGT,
	"rate_per_sec_gt": opGT,
	"avg_wait_ms_gt":  opGT,
	"spike_gt":        opGT,
	"gt_factor":       opGT,

	// Boolean
	"is_true":  opIsTrue,
	"is_false": opIsFalse,

	// Range
	"between": opBetween,

	// Special detection
	"detected":  opExists,
	"flagged":   opExists,
	"requested": opExists,
	"check":     opExists,
	"concern":   opExists,
	"mismatch":  opNE,

	// Age/time
	"age_lt":     opLT,
	"older_than": opGT,
	"newer_than": opLT,

	// Collection membership
	"in":     opIn,
	"not_in": opNotIn,
}

// opAliases maps symbol operators to their canonical names.
var opAliases = map[string]string{
	">":  "gt",
	"<":  "lt",
	">=": "gte",
	"<=": "lte",
	"=":  "eq",
	"!=": "ne",
	"<>": "ne",
}

// NormalizeOp converts an operator string to its canonical form.
func NormalizeOp(op string) string {
	op = strings.TrimSpace(strings.ToLower(op))
	if canonical, ok := opAliases[op]; ok {
		return canonical
	}
	return op
}

// EvalOp evaluates operator(actual, expected) using the registry.
// Returns false if the operator is unknown.
func EvalOp(op string, actual, expected interface{}) bool {
	op = NormalizeOp(op)
	fn, ok := opRegistry[op]
	if !ok {
		return false
	}
	return fn(actual, expected)
}

// ─── Operator Implementations ────────────────────────────────────────────────

func opGT(actual, expected interface{}) bool {
	a, ok1 := toFloatAny(actual)
	b, ok2 := toFloatAny(expected)
	return ok1 && ok2 && a > b
}

func opLT(actual, expected interface{}) bool {
	a, ok1 := toFloatAny(actual)
	b, ok2 := toFloatAny(expected)
	return ok1 && ok2 && a < b
}

func opGTE(actual, expected interface{}) bool {
	a, ok1 := toFloatAny(actual)
	b, ok2 := toFloatAny(expected)
	return ok1 && ok2 && a >= b
}

func opLTE(actual, expected interface{}) bool {
	a, ok1 := toFloatAny(actual)
	b, ok2 := toFloatAny(expected)
	return ok1 && ok2 && a <= b
}

func opEQ(actual, expected interface{}) bool {
	a, ok1 := toFloatAny(actual)
	b, ok2 := toFloatAny(expected)
	if ok1 && ok2 {
		diff := a - b
		if diff < 0 {
			diff = -diff
		}
		return diff < 0.001
	}
	ab, okb1 := toBoolAny(actual)
	bb, okb2 := toBoolAny(expected)
	if okb1 && okb2 {
		return ab == bb
	}
	return fmt.Sprintf("%v", actual) == fmt.Sprintf("%v", expected)
}

func opNE(actual, expected interface{}) bool {
	return !opEQ(actual, expected)
}

func opExists(actual, _ interface{}) bool {
	if actual == nil {
		return false
	}
	switch v := actual.(type) {
	case string:
		return v != ""
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case bool:
		return v
	default:
		return true
	}
}

func opNotEmpty(actual, _ interface{}) bool {
	return opExists(actual, nil)
}

func opIsNull(actual, _ interface{}) bool {
	return !opExists(actual, nil)
}

func opContains(actual, expected interface{}) bool {
	a := strings.ToLower(fmt.Sprintf("%v", actual))
	b := strings.ToLower(fmt.Sprintf("%v", expected))
	return strings.Contains(a, b)
}

func opNotContains(actual, expected interface{}) bool {
	return !opContains(actual, expected)
}

func opStartsWith(actual, expected interface{}) bool {
	a := strings.ToLower(fmt.Sprintf("%v", actual))
	b := strings.ToLower(fmt.Sprintf("%v", expected))
	return strings.HasPrefix(a, b)
}

func opEndsWith(actual, expected interface{}) bool {
	a := strings.ToLower(fmt.Sprintf("%v", actual))
	b := strings.ToLower(fmt.Sprintf("%v", expected))
	return strings.HasSuffix(a, b)
}

func opMatches(actual, expected interface{}) bool {
	a := fmt.Sprintf("%v", actual)
	pattern := fmt.Sprintf("%v", expected)
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false
	}
	return re.MatchString(a)
}

func opIsTrue(actual, _ interface{}) bool {
	b, ok := toBoolAny(actual)
	return ok && b
}

func opIsFalse(actual, _ interface{}) bool {
	b, ok := toBoolAny(actual)
	return ok && !b
}

func opBetween(actual, expected interface{}) bool {
	a, ok := toFloatAny(actual)
	if !ok {
		return false
	}
	switch bounds := expected.(type) {
	case []interface{}:
		if len(bounds) != 2 {
			return false
		}
		lo, ok1 := toFloatAny(bounds[0])
		hi, ok2 := toFloatAny(bounds[1])
		return ok1 && ok2 && a >= lo && a <= hi
	case []float64:
		if len(bounds) != 2 {
			return false
		}
		return a >= bounds[0] && a <= bounds[1]
	default:
		return false
	}
}

func opIn(actual, expected interface{}) bool {
	aStr := strings.ToLower(fmt.Sprintf("%v", actual))
	switch list := expected.(type) {
	case []interface{}:
		for _, item := range list {
			if strings.ToLower(fmt.Sprintf("%v", item)) == aStr {
				return true
			}
		}
	case []string:
		for _, item := range list {
			if strings.ToLower(item) == aStr {
				return true
			}
		}
	}
	return false
}

func opNotIn(actual, expected interface{}) bool {
	return !opIn(actual, expected)
}

// ─── Type coercion helpers ──────────────────────────────────────────────────

// toFloatAny converts various types to float64.
func toFloatAny(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint64:
		return float64(n), true
	case string:
		var f float64
		if _, err := fmt.Sscanf(n, "%f", &f); err == nil {
			return f, true
		}
		return 0, false
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}

// toBoolAny converts various types to bool.
func toBoolAny(v interface{}) (bool, bool) {
	switch b := v.(type) {
	case bool:
		return b, true
	case float64:
		return b != 0, true
	case int:
		return b != 0, true
	case int64:
		return b != 0, true
	case string:
		switch strings.ToLower(b) {
		case "true", "yes", "1":
			return true, true
		case "false", "no", "0":
			return false, true
		}
		return false, false
	case nil:
		return false, true
	default:
		return false, false
	}
}
