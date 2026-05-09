/*-------------------------------------------------------------------------
 *
 * resolver.go
 *	  Resolve takes multiple diagnosis results and the full rule set,
 *	  and produces a focused DiagOutput with at most 1-2 root causes.
 *	  This is the top-level entry point that orchestrates the three-step
 *	  resolution pipeline.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/resolver.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"sort"
	"strings"
)

// ─── Resolver: Root-Cause Focusing ──────────────────────────────────────────
//
// The resolver implements the core product principle: given N diagnosis results
// from fired rules, distill them to at most 1-2 root causes. Everything else
// is absorbed as downstream symptoms.
//
// CORE PRINCIPLE: Only output 1 core root cause, max 2. Never list a bunch of
// causes.

// Resolve takes multiple diagnosis results and the full rule set, and produces
// a focused DiagOutput with at most 1-2 root causes. This is the top-level
// entry point that orchestrates the three-step resolution pipeline.
func Resolve(results []*Diagnosis, rules []*Rule) *DiagOutput {
	if len(results) == 0 {
		return &DiagOutput{}
	}

	// Build rule map for cross-reference enrichment.
	ruleMap := BuildRuleMap(rules)

	// Step 0: Enrich each diagnosis with cross-reference findings.
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

	// Step 1: Causal chain analysis — identify upstream/downstream and find roots.
	roots := analyzeCausalChain(results, rules)

	// Step 2: Weight convergence — score, rank, and pick 1-2 core causes.
	output := convergeToCoreCause(roots)

	// Step 3: Enrich absorbed info on primary/secondary.
	enrichAbsorbed(output)

	return output
}

// ─── Step 1: Causal Chain Analysis ──────────────────────────────────────────

// analyzeCausalChain identifies causal relationships between diagnoses using
// the CausesOf / CausedBy relationships declared on each rule.
//
// For each pair (A, B): if A's rule.CausesOf contains B's rule.ID, then B is
// downstream of A. B is marked as B.IsDownstreamOf = A.RuleID, A.AbsorbedCount
// is incremented, and A.Confidence is boosted by 0.05 per absorbed downstream.
//
// Only "root" nodes (those with IsDownstreamOf == "") are returned.
func analyzeCausalChain(results []*Diagnosis, rules []*Rule) []*Diagnosis {
	// Build rule lookup: ruleID -> *Rule
	ruleMap := make(map[string]*Rule, len(rules))
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	// Build diagnosis lookup: ruleID -> *Diagnosis
	diagMap := make(map[string]*Diagnosis, len(results))
	for _, d := range results {
		diagMap[d.RuleID] = d
	}

	// Forward pass: if rule A.CausesOf contains B.ID -> B is downstream of A.
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

	// Reverse pass: if rule B.CausedBy contains A.ID -> B is downstream of A.
	for _, dB := range results {
		if dB.IsDownstreamOf != "" {
			continue // already marked
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
			break // only mark one upstream
		}
	}

	// Collect root nodes: those with IsDownstreamOf == "".
	var roots []*Diagnosis
	for _, d := range results {
		if d.IsDownstreamOf == "" {
			roots = append(roots, d)
		}
	}

	// Edge case: if circular dependencies absorbed everything, reset and
	// return all as roots.
	if len(roots) == 0 {
		for _, d := range results {
			d.IsDownstreamOf = ""
		}
		return results
	}

	return roots
}

// ─── Step 2: Weight Convergence ─────────────────────────────────────────────

// convergeToCoreCause scores and ranks root diagnoses, selecting at most
// 1-2 core causes. This enforces the hard limit: max 2 root causes in output.
//
// Score = Severity_weight x Confidence x Specificity
// (severity weights: Low=1, Medium=2, High=3, Critical=4)
//
// Selection rules (evaluated in order):
//   - If #1 weight > 80%: only Primary, no Secondary
//   - If #1 Score > #2 Score x 2: only Primary
//   - If #2 weight > 30%: Primary + Secondary
//   - Otherwise: only Primary
func convergeToCoreCause(roots []*Diagnosis) *DiagOutput {
	output := &DiagOutput{}

	if len(roots) == 0 {
		return output
	}

	// Calculate scores.
	var totalScore float64
	for _, d := range roots {
		if d.Specificity == 0 {
			d.Specificity = computeSpecificity(d)
		}
		d.Score = severityWeight(d.Severity) * d.Confidence * d.Specificity
		totalScore += d.Score
	}

	// Normalize weights.
	if totalScore > 0 {
		for _, d := range roots {
			d.Weight = d.Score / totalScore
		}
	}

	// Sort descending by score.
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].Score > roots[j].Score
	})

	first := roots[0]

	if len(roots) == 1 {
		output.Primary = first
		return output
	}

	second := roots[1]

	// Selection logic: enforce max 1-2 root causes.
	switch {
	case first.Weight > 0.80:
		// #1 dominates overwhelmingly — only Primary.
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)

	case first.Score > second.Score*2:
		// #1 is significantly stronger — only Primary.
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)

	case second.Weight > 0.30:
		// #2 is significant — Primary + Secondary.
		output.Primary = first
		output.Secondary = second
		if len(roots) > 2 {
			output.Absorbed = append(output.Absorbed, roots[2:]...)
		}

	default:
		// Default: only Primary.
		output.Primary = first
		output.Absorbed = append(output.Absorbed, roots[1:]...)
	}

	return output
}

// ─── Step 3: Enrich Absorbed Info ───────────────────────────────────────────

// enrichAbsorbed collects names of all absorbed diagnoses and attaches them
// to the Primary for display in the "关联分析" section.
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

// severityWeight maps Severity to a numeric weight for scoring.
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

// BoostSeverityByImpact adjusts diagnosis severity based on the percentage of
// affected sessions. If >80% of active sessions are affected, severity is at
// least Critical; >50% at least High; >20% at least Medium.
func BoostSeverityByImpact(d *Diagnosis, topWaitPct float64) {
	if d == nil {
		return
	}
	var minSeverity Severity
	switch {
	case topWaitPct >= 80:
		minSeverity = SeverityCritical
	case topWaitPct >= 50:
		minSeverity = SeverityHigh
	case topWaitPct >= 20:
		minSeverity = SeverityMedium
	default:
		return
	}
	if severityWeight(d.Severity) < severityWeight(minSeverity) {
		d.Severity = minSeverity
	}
}
