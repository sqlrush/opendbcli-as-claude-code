/*-------------------------------------------------------------------------
 *
 * cross_ref.go
 *	  EnrichCrossReferences adds findings from cross-referenced rules to
 *	  the diagnosis. ruleMap provides the lookup from rule ID to *Rule.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/cross_ref.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "fmt"

// ─── Cross-Reference Executor ──────────────────────────────────────────────
//
// Enriches diagnosis results with information from related rules.
// Current implementation: shallow (inject findings from related rules).
// Future: deep (jump to related rule's tree and merge results).

// EnrichCrossReferences adds findings from cross-referenced rules to the diagnosis.
// ruleMap provides the lookup from rule ID to *Rule.
func EnrichCrossReferences(diag *Diagnosis, ruleMap map[string]*Rule) {
	if diag == nil {
		return
	}

	// Look up the rule that produced this diagnosis
	rule := ruleMap[diag.RuleID]
	if rule == nil {
		return
	}

	// Add findings from Related rules
	for _, relID := range rule.Related {
		relRule := ruleMap[relID]
		if relRule == nil {
			continue
		}
		diag.Findings = append(diag.Findings, Finding{
			Desc: fmt.Sprintf("关联规则 %s (%s) 建议一并排查", relID, relRule.Name),
		})
	}
}

// BuildRuleMap creates a lookup map from rule ID to *Rule.
func BuildRuleMap(rules []*Rule) map[string]*Rule {
	m := make(map[string]*Rule, len(rules))
	for _, r := range rules {
		m[r.ID] = r
	}
	return m
}
