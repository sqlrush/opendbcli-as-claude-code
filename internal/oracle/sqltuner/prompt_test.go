/*-------------------------------------------------------------------------
 *
 * prompt_test.go
 *	  Oracle PromptBuilder content sanity checks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/prompt_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOraclePromptBuilder_Interface(t *testing.T) {
	var _ sqltune.PromptBuilder = (*oraclePromptBuilder)(nil)
}

func TestOraclePromptBuilder_ContainsDialectKeywords(t *testing.T) {
	b := NewPromptBuilder()
	if !strings.Contains(b.RoleTag(), "Oracle") {
		t.Errorf("RoleTag should mention Oracle")
	}
	cbo := b.CBOKnowledge()
	for _, kw := range []string{"optimizer_mode", "optimizer_index_cost_adj", "10053 trace", "NESTED LOOPS", "HASH JOIN", "bind peeking"} {
		if !strings.Contains(cbo, kw) {
			t.Errorf("CBOKnowledge missing %q", kw)
		}
	}
	pr := b.PlanReading()
	for _, kw := range []string{"TABLE ACCESS FULL", "INDEX RANGE SCAN", "Predicate Information", "10053"} {
		if !strings.Contains(pr, kw) {
			t.Errorf("PlanReading missing %q", kw)
		}
	}
	hs := b.HintSyntax()
	for _, kw := range []string{"/*+ INDEX", "USE_NL", "USE_HASH", "LEADING", "PARALLEL"} {
		if !strings.Contains(hs, kw) {
			t.Errorf("HintSyntax missing %q", kw)
		}
	}
}
