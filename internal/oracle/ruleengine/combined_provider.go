/*-------------------------------------------------------------------------
 *
 * combined_provider.go
 *	  CombinedProvider merges rules from multiple providers.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/combined_provider.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Combined Rule Provider ─────────────────────────────────────────────────
//
// Merges rules from multiple providers. Later providers override earlier ones
// when rule IDs conflict. This enables gradual migration from hardcoded Go
// rules to JSON rules.

// CombinedProvider merges rules from multiple providers.
type CombinedProvider struct {
	providers []RuleProvider
	rules     []*Rule // cached merged result
	registry  *DynamicQueryRegistry
}

// NewCombinedProvider creates a provider that merges rules from all providers.
// Rule priority: later providers override earlier ones by rule ID.
func NewCombinedProvider(providers ...RuleProvider) *CombinedProvider {
	cp := &CombinedProvider{
		providers: providers,
	}

	seen := make(map[string]int) // rule ID → index in rules slice
	var merged []*Rule

	for _, p := range providers {
		for _, rule := range p.Rules() {
			if idx, exists := seen[rule.ID]; exists {
				// Override: later provider wins
				merged[idx] = rule
			} else {
				seen[rule.ID] = len(merged)
				merged = append(merged, rule)
			}
		}

		// Collect dynamic query registries
		if qp, ok := p.(QueryRegistryProvider); ok {
			if cp.registry == nil {
				cp.registry = qp.QueryRegistry()
			} else {
				// Merge registries
				qr := qp.QueryRegistry()
				for qid, sql := range qr.queries {
					cp.registry.Register(qid, sql)
				}
			}
		}
	}

	cp.rules = merged
	return cp
}

// Rules returns the merged rule set.
func (p *CombinedProvider) Rules() []*Rule { return p.rules }

// Version returns the combined version string.
func (p *CombinedProvider) Version() string {
	var versions []string
	for _, p := range p.providers {
		versions = append(versions, p.Edition()+":"+p.Version())
	}
	return joinVersions(versions)
}

// Edition returns "combined".
func (p *CombinedProvider) Edition() string { return "combined" }

// QueryRegistry returns the merged query registry.
func (p *CombinedProvider) QueryRegistry() *DynamicQueryRegistry {
	if p.registry == nil {
		return NewDynamicQueryRegistry()
	}
	return p.registry
}

func joinVersions(vs []string) string {
	result := ""
	for i, v := range vs {
		if i > 0 {
			result += "+"
		}
		result += v
	}
	return result
}
