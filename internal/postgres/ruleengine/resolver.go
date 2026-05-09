/*-------------------------------------------------------------------------
 *
 * resolver.go
 *	  Resolve takes multiple diagnosis results and the full rule set,
 *	  and produces a focused DiagOutput with at most 1-2 root causes.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/resolver.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"sort"
	"strings"
)

// ─── Resolver: Root-Cause Focusing ──────────────────────────────────────────

// Resolve takes multiple diagnosis results and the full rule set, and produces
// a focused DiagOutput with at most 1-2 root causes.
func Resolve(results []*Diagnosis, rules []*Rule) *DiagOutput {
	if len(results) == 0 {
		return &DiagOutput{}
	}

	ruleMap := BuildRuleMap(rules)

	for _, d := range results {
		EnrichCrossReferences(d, ruleMap)
	}

	if len(results) == 1 {
		d := results[0]
		if d.Specificity == 0 {
			d.Specificity = computeSpecificity(d)
		}
		d.Score = severityWeight(d.Severity) * d.Confidence * d.Specificity
		d.Weight = 1.0
		return &DiagOutput{Primary: d}
	}

	roots := analyzeCausalChain(results, rules)
	output := convergeToCoreCause(roots)
	enrichAbsorbed(output)

	return output
}

// ─── Step 1: Causal Chain Analysis ──────────────────────────────────────────

func analyzeCausalChain(results []*Diagnosis, rules []*Rule) []*Diagnosis {
	ruleMap := make(map[string]*Rule, len(rules))
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	diagMap := make(map[string]*Diagnosis, len(results))
	for _, d := range results {
		diagMap[d.RuleID] = d
	}

	// Forward pass
	for _, dA := range results {
		ruleA := ruleMap[dA.RuleID]
		if ruleA == nil {
			continue
		}
		for _, downstreamID := range ruleA.CausesOf {
			dB := diagMap[downstreamID]
			if dB == nil || dB.IsDownstreamOf != "" {
				continue
			}
			dB.IsDownstreamOf = dA.RuleID
			dA.AbsorbedCount++
			dA.Confidence += 0.05
			if dA.Confidence > 1.0 {
				dA.Confidence = 1.0
			}
		}
	}

	// Reverse pass
	for _, dB := range results {
		if dB.IsDownstreamOf != "" {
			continue
		}
		ruleB := ruleMap[dB.RuleID]
		if ruleB == nil {
			continue
		}
		for _, upstreamID := range ruleB.CausedBy {
			dA := diagMap[upstreamID]
			if dA == nil || dA.RuleID == dB.RuleID {
				continue
			}
			dB.IsDownstreamOf = dA.RuleID
			dA.AbsorbedCount++
			dA.Confidence += 0.05
			if dA.Confidence > 1.0 {
				dA.Confidence = 1.0
			}
			break
		}
	}

	var roots []*Diagnosis
	for _, d := range results {
		if d.IsDownstreamOf == "" {
			roots = append(roots, d)
		}
	}

	if len(roots) == 0 {
		for _, d := range results {
			d.IsDownstreamOf = ""
		}
		return results
	}

	return roots
}

// ─── Step 2: Weight Convergence ─────────────────────────────────────────────

func convergeToCoreCause(roots []*Diagnosis) *DiagOutput {
	output := &DiagOutput{}

	if len(roots) == 0 {
		return output
	}

	var totalScore float64
	for _, d := range roots {
		if d.Specificity == 0 {
			d.Specificity = computeSpecificity(d)
		}
		d.Score = severityWeight(d.Severity) * d.Confidence * d.Specificity
		totalScore += d.Score
	}

	if totalScore > 0 {
		for _, d := range roots {
			d.Weight = d.Score / totalScore
		}
	}

	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Score > roots[j].Score
	})

	first := roots[0]

	if len(roots) == 1 {
		output.Primary = first
		return output
	}

	second := roots[1]

	switch {
	case first.Weight > 0.80:
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)
	case first.Score > second.Score*2:
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)
	case second.Weight > 0.30:
		output.Primary = first
		output.Secondary = second
		if len(roots) > 2 {
			output.Absorbed = append(output.Absorbed, roots[2:]...)
		}
	default:
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)
	}

	return output
}

// ─── Step 3: Enrich Absorbed Info ───────────────────────────────────────────

func enrichAbsorbed(output *DiagOutput) {
	if output.Primary == nil {
		return
	}

	for _, d := range output.Absorbed {
		output.Primary.AbsorbedNames = append(output.Primary.AbsorbedNames, d.RuleName)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────────

// computeSpecificity estimates how specific a diagnosis is based on evidence depth,
// actionable SQL commands, root cause identification, and elimination method usage.
func computeSpecificity(d *Diagnosis) float64 {
	findingCount := len(d.Findings)
	actionCount := len(d.Actions)

	// Base specificity from evidence quantity.
	specificity := 0.3
	if findingCount >= 1 {
		specificity = 0.4
	}
	if findingCount >= 3 {
		specificity += 0.15
	}
	if findingCount >= 5 {
		specificity += 0.1
	}

	// Boost for actionable remediation with specific SQL.
	sqlActionCount := 0
	for _, a := range d.Actions {
		if a.RawSQL != "" || a.SkillCommand != "" {
			sqlActionCount++
		}
	}
	if sqlActionCount >= 2 {
		specificity += 0.15
	} else if sqlActionCount >= 1 {
		specificity += 0.08
	}

	// Boost for having multiple action types (diverse remediation).
	if actionCount >= 3 {
		specificity += 0.05
	}

	// Boost for root cause identification in findings.
	for _, f := range d.Findings {
		if strings.HasPrefix(f.Desc, "根因: ") {
			specificity += 0.08
			break
		}
	}

	// Boost for elimination method evidence.
	eliminatedCount := 0
	for _, f := range d.Findings {
		if strings.HasPrefix(f.Desc, "已排除: ") {
			eliminatedCount++
		}
	}
	if eliminatedCount >= 2 {
		specificity += 0.05
	}

	if specificity > 0.95 {
		specificity = 0.95
	}
	return specificity
}

func severityWeight(s Severity) float64 {
	switch s {
	case SeverityLow:
		return 1.0
	case SeverityMedium:
		return 2.0
	case SeverityHigh:
		return 3.0
	case SeverityCritical:
		return 4.0
	default:
		return 1.0
	}
}
