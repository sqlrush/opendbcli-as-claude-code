/*-------------------------------------------------------------------------
 *
 * community.go
 *	  CommunityProvider supplies the basic free rule set for PostgreSQL.
 *	  PG rules are primarily JSON-based (from ailinkdb); no hardcoded Go
 *	  rules.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/community.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// CommunityProvider supplies the basic free rule set for PostgreSQL.
// PG rules are primarily JSON-based (from ailinkdb); no hardcoded Go rules.
type CommunityProvider struct{}

// Version returns the community rule set version.
func (p *CommunityProvider) Version() string { return "0.1.0" }

// Edition returns "community".
func (p *CommunityProvider) Edition() string { return "community" }

// Rules returns all hardcoded diagnostic rules for PostgreSQL.
func (p *CommunityProvider) Rules() []*Rule {
	var rules []*Rule
	rules = append(rules, coreRules()...)     // 25 core PG rules (PG-001 ~ PG-025)
	rules = append(rules, extendedRules()...) // 55 extended PG rules (PG-026 ~ PG-080)
	return rules
}
