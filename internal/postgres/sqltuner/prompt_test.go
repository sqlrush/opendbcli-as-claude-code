/*-------------------------------------------------------------------------
 *
 * prompt_test.go
 *	  PG PromptBuilder content sanity checks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/prompt_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestPGPromptBuilder_Interface(t *testing.T) {
	var _ sqltune.PromptBuilder = (*pgPromptBuilder)(nil)
}

func TestPGPromptBuilder_ContainsDialectKeywords(t *testing.T) {
	b := NewPromptBuilder()
	if !strings.Contains(b.RoleTag(), "PostgreSQL") {
		t.Errorf("RoleTag should mention PostgreSQL")
	}
	cbo := b.CBOKnowledge()
	for _, kw := range []string{"random_page_cost", "pg_stats", "from_collapse_limit", "rejected paths"} {
		if !strings.Contains(cbo, kw) {
			t.Errorf("CBOKnowledge missing %q", kw)
		}
	}
	pr := b.PlanReading()
	for _, kw := range []string{"Seq Scan", "Nested Loop", "Hash", "Index Cond"} {
		if !strings.Contains(pr, kw) {
			t.Errorf("PlanReading missing %q", kw)
		}
	}
	hs := b.HintSyntax()
	for _, kw := range []string{"pg_hint_plan", "Leading", "SET enable_seqscan"} {
		if !strings.Contains(hs, kw) {
			t.Errorf("HintSyntax missing %q", kw)
		}
	}
}
