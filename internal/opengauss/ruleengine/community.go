/*-------------------------------------------------------------------------
 *
 * community.go
 *	  CommunityProvider supplies the basic free rule set for OpenGauss.
 *	  OpenGauss rules come primarily from JSON files (PG rules adapted
 *	  for OG).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/community.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// CommunityProvider supplies the basic free rule set for OpenGauss.
// OpenGauss rules come primarily from JSON files (PG rules adapted for OG).
type CommunityProvider struct{}

// Version returns the community rule set version.
func (p *CommunityProvider) Version() string { return "0.1.0" }

// Edition returns "community".
func (p *CommunityProvider) Edition() string { return "community" }

// Rules returns the community diagnostic rules.
// Includes 80 hardcoded Go rules adapted from PG for OpenGauss.
func (p *CommunityProvider) Rules() []*Rule {
	var rules []*Rule
	rules = append(rules, coreRules()...)     // 25 core OG rules (OG-001 ~ OG-025)
	rules = append(rules, extendedRules()...) // 55 extended OG rules (OG-026 ~ OG-080)
	return rules
}
