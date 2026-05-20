/*-------------------------------------------------------------------------
 *
 * prompt_test.go
 *	  MySQL PromptBuilder content sanity checks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/prompt_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestMySQLPromptBuilder_Interface(t *testing.T) {
	var _ sqltune.PromptBuilder = (*mysqlPromptBuilder)(nil)
}

func TestMySQLPromptBuilder_ContainsDialectKeywords(t *testing.T) {
	b := NewPromptBuilder()
	if !strings.Contains(b.RoleTag(), "MySQL") {
		t.Errorf("RoleTag should mention MySQL")
	}
	cbo := b.CBOKnowledge()
	for _, kw := range []string{"optimizer_switch", "Hash Join", "Nested Loop", "optimizer_trace", "information_schema.OPTIMIZER_TRACE"} {
		if !strings.Contains(cbo, kw) {
			t.Errorf("CBOKnowledge missing %q", kw)
		}
	}
	pr := b.PlanReading()
	for _, kw := range []string{"access_type", "Using filesort", "Using temporary"} {
		if !strings.Contains(pr, kw) {
			t.Errorf("PlanReading missing %q", kw)
		}
	}
	hs := b.HintSyntax()
	for _, kw := range []string{"HASH_JOIN", "JOIN_ORDER", "USE_INDEX"} {
		if !strings.Contains(hs, kw) {
			t.Errorf("HintSyntax missing %q", kw)
		}
	}
}
