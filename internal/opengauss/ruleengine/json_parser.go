/*-------------------------------------------------------------------------
 *
 * json_parser.go
 *	  ParseJSONRule converts a JSONRule into a *Rule.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/json_parser.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─── JSON Rule Parser ──────────────────────────────────────────────────────
//
// Converts a complete JSONRule into a *Rule that the engine can evaluate.

// ParseJSONRule converts a JSONRule into a *Rule.
func ParseJSONRule(jr *JSONRule, registry *DynamicQueryRegistry) *Rule {
	if jr == nil || jr.RuleID == "" {
		return nil
	}

	rule := &Rule{
		ID:       jr.RuleID,
		Name:     jr.Name,
		Category: MapJSONCategory(jr.Category, jr.Subcategory),
		Tags:     jr.Tags,
		Versions: jr.Versions,
	}

	rule.Signals = convertSignals(jr.Signals)
	rule.Trigger = convertTrigger(jr.Trigger)

	var queryMap map[string]QueryID
	if registry != nil && len(jr.DiagQueries) > 0 {
		queryMap = registry.RegisterRuleQueries(jr.RuleID, jr.DiagQueries)
	}

	rootCauses := make(map[string]JSONRootCause, len(jr.RootCauses))
	for _, rc := range jr.RootCauses {
		rootCauses[rc.ID] = rc
	}

	rule.Tree = BuildTreeFromJSON(jr.Tree.Node, queryMap, rootCauses)

	rule.CausedBy = extractRuleIDs(jr.Related.CausedBy)
	rule.CausesOf = extractRuleIDs(jr.Related.CausesOf)
	rule.Related = extractRuleIDs(jr.Related.Related)

	return rule
}

// ParseJSONRuleBytes parses a JSON byte slice into a *Rule.
func ParseJSONRuleBytes(data []byte, registry *DynamicQueryRegistry) (*Rule, error) {
	var jr JSONRule
	if err := json.Unmarshal(data, &jr); err != nil {
		return nil, fmt.Errorf("unmarshal JSON rule: %w", err)
	}
	rule := ParseJSONRule(&jr, registry)
	if rule == nil {
		return nil, fmt.Errorf("failed to parse rule %s", jr.RuleID)
	}
	return rule, nil
}

// ─── Signal Conversion ─────────────────────────────────────────────────────

func convertSignals(jsignals []JSONSignal) []Signal {
	var signals []Signal
	seen := make(map[string]bool)

	for _, js := range jsignals {
		key := strings.ToLower(js.Key)
		sigType := MapJSONSignalType(js.Type, js.Key)

		dedup := fmt.Sprintf("%d:%s", sigType, key)
		if seen[dedup] {
			continue
		}
		seen[dedup] = true

		signals = append(signals, Signal{
			Type: sigType,
			Key:  key,
		})
	}

	return signals
}

// ─── Trigger Conversion ────────────────────────────────────────────────────

func convertTrigger(jt JSONTrigger) Trigger {
	trigger := Trigger{
		Mode: parseTriggerMode(jt.Mode),
	}

	for _, jc := range jt.Conditions {
		cond := convertCondition(jc)
		if cond != nil {
			trigger.Conditions = append(trigger.Conditions, *cond)
		}
	}

	for _, js := range jt.SkipWhen {
		skip := convertSkipWhen(js)
		trigger.SkipWhen = append(trigger.SkipWhen, skip)
	}

	return trigger
}

func parseTriggerMode(mode string) TriggerMode {
	switch strings.ToLower(mode) {
	case "auto":
		return TriggerAuto
	case "query":
		return TriggerQuery
	case "manual":
		return TriggerManual
	default:
		return TriggerAuto
	}
}

func convertCondition(jc JSONCondition) *Condition {
	source := MapTriggerSource(jc.Source)
	if source == "" {
		return nil
	}

	op := NormalizeOp(jc.Op)
	value := 0.0
	if f, ok := toFloatAny(jc.Value); ok {
		value = f
	}

	return &Condition{
		Source: source,
		Field:  jc.Field,
		Op:     CondOp(op),
		Value:  value,
	}
}

func convertSkipWhen(js JSONSkipWhen) SkipCondition {
	expr := js.Condition

	return SkipCondition{
		Desc: js.Desc,
		Check: func(ctx *EvalContext) bool {
			return EvalSkipWhen(expr, ctx)
		},
	}
}

// ─── Related Rules Extraction ──────────────────────────────────────────────

func extractRuleIDs(relations []JSONRelation) []string {
	var ids []string
	for _, r := range relations {
		if r.RuleID != "" {
			ids = append(ids, r.RuleID)
		}
	}
	return ids
}
