/*-------------------------------------------------------------------------
 *
 * cross_ref.go
 *	  EnrichCrossReferences adds findings from cross-referenced rules to
 *	  the diagnosis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/cross_ref.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "fmt"

// ─── Cross-Reference Executor ──────────────────────────────────────────────

// EnrichCrossReferences adds findings from cross-referenced rules to the diagnosis.
func EnrichCrossReferences(diag *Diagnosis, ruleMap map[string]*Rule) {
	if diag == nil {
		return
	}

	rule := ruleMap[diag.RuleID]
	if rule == nil {
		return
	}

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
