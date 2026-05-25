/*-------------------------------------------------------------------------
 *
 * community.go
 *	  CommunityProvider supplies the basic free rule set for
 *	  demonstration. Enterprise edition extends this with 1100+ expert
 *	  rules.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/community.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// CommunityProvider supplies the basic free rule set for demonstration.
// Enterprise edition extends this with 1100+ expert rules.
type CommunityProvider struct{}

// Version returns the community rule set version.
func (p *CommunityProvider) Version() string { return "0.1.0" }

// Edition returns "community".
func (p *CommunityProvider) Edition() string { return "community" }

// Rules returns all 216 diagnostic rules across 10 categories.
func (p *CommunityProvider) Rules() []*Rule {
	var rules []*Rule
	// P0: Core (38)
	rules = append(rules, waitEventRules()...)       // 16
	rules = append(rules, otherRules()...)            // 22
	// P1: Deep (55)
	rules = append(rules, deepWaitEventRules()...)    // 20
	rules = append(rules, extWaitEventRules()...)     // 15
	rules = append(rules, deepMemoryRules()...)       // 6
	rules = append(rules, deepIOStorageRules()...)    // 7
	rules = append(rules, deepUndoRules()...)         // 4
	rules = append(rules, deepSessionRules()...)      // 3
	rules = append(rules, sessionExtRules()...)       // 2
	// P1.5: SQL performance — live diagnosis with SQL Advisor integration (4)
	rules = append(rules, liveSQLPerfRules()...)       // 4
	// P2: HA/Emergency (45) — SQL tuning replaced by SQL Advisor
	rules = append(rules, haDataGuardRules()...)      // 8
	rules = append(rules, haRACRules()...)            // 7
	rules = append(rules, deepEmergencyRules()...)    // 10
	rules = append(rules, haGoldenGateRules()...)     // 5
	rules = append(rules, haASMRules()...)            // 5
	rules = append(rules, haRMANRules()...)           // 5
	rules = append(rules, haFlashbackRules()...)      // 5
	// P3: Operations (25)
	rules = append(rules, cdbPdbRules()...)           // 4
	rules = append(rules, securityRules()...)         // 3
	rules = append(rules, partitionRules()...)        // 3
	rules = append(rules, backupRecoveryRules()...)   // 4
	rules = append(rules, upgradeRules()...)          // 3
	rules = append(rules, parameterRules()...)        // 4
	rules = append(rules, performanceBaselineRules()...) // 4
	// P4: Extended Operations (20)
	rules = append(rules, networkRules()...)          // 4
	rules = append(rules, schedulerRules()...)        // 3
	rules = append(rules, dataPumpRules()...)         // 3
	rules = append(rules, tablespaceRules()...)       // 4
	rules = append(rules, compressionRules()...)      // 3
	rules = append(rules, inMemoryRules()...)         // 3
	// P5: Extended Memory/IO + Emergency (33)
	rules = append(rules, extMemoryIORules()...)      // 15
	rules = append(rules, extEmergencyRules()...)     // 18
	return rules
}
