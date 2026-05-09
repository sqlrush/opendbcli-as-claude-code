/*-------------------------------------------------------------------------
 *
 * index.go
 *	  SignalIndex is an inverted index from signals to rules. It allows
 *	  O(1) lookup of candidate rules given a set of observed signals.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/index.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "strings"

// SignalIndex is an inverted index from signals to rules.
// It allows O(1) lookup of candidate rules given a set of observed signals.
type SignalIndex struct {
	byWaitEvent map[string][]*Rule
	byErrorCode map[string][]*Rule
	byMetric    map[string][]*Rule
	byCategory  map[string][]*Rule
	byKeyword   map[string][]*Rule
}

// buildIndex constructs a SignalIndex from a slice of rules.
func buildIndex(rules []*Rule) *SignalIndex {
	idx := &SignalIndex{
		byWaitEvent: make(map[string][]*Rule),
		byErrorCode: make(map[string][]*Rule),
		byMetric:    make(map[string][]*Rule),
		byCategory:  make(map[string][]*Rule),
		byKeyword:   make(map[string][]*Rule),
	}

	for _, rule := range rules {
		for _, sig := range rule.Signals {
			key := strings.ToLower(sig.Key)
			switch sig.Type {
			case SignalWaitEvent:
				idx.byWaitEvent[key] = append(idx.byWaitEvent[key], rule)
			case SignalErrorCode:
				idx.byErrorCode[strings.ToUpper(sig.Key)] = append(
					idx.byErrorCode[strings.ToUpper(sig.Key)], rule)
			case SignalMetric:
				idx.byMetric[key] = append(idx.byMetric[key], rule)
			case SignalCategory:
				idx.byCategory[key] = append(idx.byCategory[key], rule)
			case SignalKeyword:
				idx.byKeyword[key] = append(idx.byKeyword[key], rule)
			}
		}

		// Also index by rule category so category signals can match
		if rule.Category != "" {
			cat := strings.ToLower(rule.Category)
			idx.byCategory[cat] = appendUnique(idx.byCategory[cat], rule)
		}
	}

	return idx
}

// Match returns deduplicated candidate rules that match any of the given signals.
func (idx *SignalIndex) Match(signals []Signal) []*Rule {
	seen := make(map[string]bool)
	var candidates []*Rule

	for _, sig := range signals {
		var matched []*Rule

		switch sig.Type {
		case SignalWaitEvent:
			matched = idx.byWaitEvent[strings.ToLower(sig.Key)]
		case SignalErrorCode:
			matched = idx.byErrorCode[strings.ToUpper(sig.Key)]
		case SignalMetric:
			matched = idx.byMetric[strings.ToLower(sig.Key)]
		case SignalCategory:
			matched = idx.byCategory[strings.ToLower(sig.Key)]
		case SignalKeyword:
			matched = idx.matchKeywords(sig.Key)
		}

		for _, rule := range matched {
			if !seen[rule.ID] {
				seen[rule.ID] = true
				candidates = append(candidates, rule)
			}
		}
	}

	return candidates
}

// matchKeywords performs a keyword lookup with substring matching support.
func (idx *SignalIndex) matchKeywords(keyword string) []*Rule {
	lower := strings.ToLower(keyword)

	// Exact match first
	if rules, ok := idx.byKeyword[lower]; ok {
		return rules
	}

	// Substring match
	var matched []*Rule
	seen := make(map[string]bool)
	for key, rules := range idx.byKeyword {
		if strings.Contains(lower, key) || strings.Contains(key, lower) {
			for _, r := range rules {
				if !seen[r.ID] {
					seen[r.ID] = true
					matched = append(matched, r)
				}
			}
		}
	}

	return matched
}

// appendUnique appends a rule to the slice only if it's not already present.
func appendUnique(rules []*Rule, rule *Rule) []*Rule {
	for _, r := range rules {
		if r.ID == rule.ID {
			return rules
		}
	}
	return append(rules, rule)
}
