/*-------------------------------------------------------------------------
 *
 * json_tree.go
 *	  BuildTreeFromJSON converts a JSONTreeNode to a TreeNode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/json_tree.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "fmt"

// ─── JSON Tree Builder ──────────────────────────────────────────────────────
//
// Converts JSONTreeNode → TreeNode by generating closures that wrap
// the declarative expression evaluator.

// MaxJSONTreeDepth limits recursion during JSON tree conversion.
const MaxJSONTreeDepth = 20

// BuildTreeFromJSON converts a JSONTreeNode to a TreeNode.
func BuildTreeFromJSON(
	jnode *JSONTreeNode,
	queryMap map[string]QueryID,
	rootCauses map[string]JSONRootCause,
) *TreeNode {
	if jnode == nil {
		return nil
	}
	return buildTreeNode(jnode, queryMap, rootCauses, 0)
}

func buildTreeNode(
	jnode *JSONTreeNode,
	queryMap map[string]QueryID,
	rootCauses map[string]JSONRootCause,
	depth int,
) *TreeNode {
	if jnode == nil || depth >= MaxJSONTreeDepth {
		return nil
	}

	node := &TreeNode{
		Step:              jnode.Step,
		EliminationMethod: jnode.EliminationMethod,
	}

	if jnode.Query != "" {
		if qid, ok := queryMap[jnode.Query]; ok {
			node.Query = qid
		}
	}

	for _, jb := range jnode.Branches {
		branch := convertBranch(jb, queryMap, rootCauses, depth)
		node.Branches = append(node.Branches, branch)
	}

	return node
}

func convertBranch(
	jb JSONBranch,
	queryMap map[string]QueryID,
	rootCauses map[string]JSONRootCause,
	depth int,
) Branch {
	branch := Branch{
		Label: jb.Label,
	}

	branch.Match = buildMatchFunc(jb.Condition)

	if jb.Severity != "" {
		branch.Severity = ParseSeverity(jb.Severity)
	}

	for _, f := range jb.Findings {
		branch.Findings = append(branch.Findings, Finding{Desc: f})
	}

	if jb.RootCause != "" {
		if rc, ok := rootCauses[jb.RootCause]; ok {
			rcDesc := fmt.Sprintf("根因: %s — %s", rc.Name, rc.Desc)
			branch.Findings = append([]Finding{{Desc: rcDesc}}, branch.Findings...)
		}
	}

	if jb.Confidence != "" {
		branch.Findings = append(branch.Findings, Finding{
			Desc: jb.Confidence,
		})
	}

	for _, ja := range jb.Actions {
		branch.Actions = append(branch.Actions, Action{
			Type:     ParseActionType(ja.Type),
			Desc:     ja.Desc,
			RawSQL:   ja.SQL,
			Risk:     ja.Risk,
			Rollback: ja.Rollback,
		})
	}

	for _, v := range jb.Verification {
		branch.Actions = append(branch.Actions, Action{
			Type: ActionInvestigate,
			Desc: fmt.Sprintf("验证: %s", v),
		})
	}

	for _, ref := range jb.CrossReference.Refs {
		desc := fmt.Sprintf("关联规则 %s", ref.RuleID)
		if ref.Reason != "" {
			desc += ": " + ref.Reason
		}
		if ref.Desc != "" {
			desc += ": " + ref.Desc
		}
		branch.Findings = append(branch.Findings, Finding{Desc: desc})
	}

	if jb.Then != nil {
		branch.Then = buildTreeNode(jb.Then, queryMap, rootCauses, depth+1)
	}

	return branch
}

// buildMatchFunc creates a Match closure from a JSONBranchCond.
func buildMatchFunc(cond JSONBranchCond) func(interface{}) bool {
	if cond.IsDefault {
		return MatchDefault()
	}

	if cond.Field == "" && cond.Op == "" {
		return MatchDefault()
	}

	op := NormalizeOp(cond.Op)

	if isSemanticBoolField(cond.Field) && op == "eq" && isTrueish(cond.Value) {
		field := cond.Field
		return func(value interface{}) bool {
			if extracted := extractField(value, field); extracted != nil {
				return EvalOp(op, extracted, true)
			}
			return isNonEmptyValue(value)
		}
	}

	expr := &ExprMatch{
		IsDefault: false,
		Field:     cond.Field,
		Op:        op,
		Value:     cond.Value,
	}

	return func(value interface{}) bool {
		return EvalBranchMatch(expr, value)
	}
}

// isSemanticBoolField returns true if the field is an abstract semantic marker.
func isSemanticBoolField(field string) bool {
	semanticPrefixes := []string{
		"clear_anomaly", "hidden_anomaly", "subtle_issue", "has_issue",
		"symptom_match_", "rc", "primary_confirmed", "confirmed",
		"cross_validated", "cross_validation_match",
		"evidence_count", "elimination_result",
		"upstream_cause", "initial_diagnosis",
		"other_causes_excluded", "other_factors_found",
		"severity_indicator", "root_cause_confirmed",
	}
	for _, prefix := range semanticPrefixes {
		if field == prefix || (len(field) > len(prefix) && field[:len(prefix)] == prefix) {
			return true
		}
	}
	return false
}

// isTrueish returns true if v represents a truthy value.
func isTrueish(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true" || val == "yes" || val == "1"
	case float64:
		return val != 0
	default:
		return false
	}
}

// isNonEmptyValue returns true if value represents non-empty query result.
func isNonEmptyValue(value interface{}) bool {
	if value == nil {
		return false
	}
	switch v := value.(type) {
	case map[string]interface{}:
		if rows, ok := v["rows"]; ok {
			if arr, ok := rows.([]map[string]interface{}); ok {
				return len(arr) > 0
			}
		}
		if count, ok := v["count"]; ok {
			if f, ok := toFloatAny(count); ok {
				return f > 0
			}
		}
		return len(v) > 0
	case []map[string]interface{}:
		return len(v) > 0
	case []interface{}:
		return len(v) > 0
	case float64:
		return v != 0
	case string:
		return v != ""
	default:
		return true
	}
}
