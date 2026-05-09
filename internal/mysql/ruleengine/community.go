/*-------------------------------------------------------------------------
 *
 * community.go
 *	  CommunityProvider supplies the basic free rule set for MySQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/community.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// CommunityProvider supplies the basic free rule set for MySQL.
type CommunityProvider struct{}

// Version returns the community rule set version.
func (p *CommunityProvider) Version() string { return "0.3.0" }

// Edition returns "community".
func (p *CommunityProvider) Edition() string { return "community" }

// Rules returns all 80 hardcoded MySQL diagnostic rules (25 core + 55 extended).
func (p *CommunityProvider) Rules() []*Rule {
	return append(coreRules(), extendedRules()...)
}
