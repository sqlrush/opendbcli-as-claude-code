/*-------------------------------------------------------------------------
 *
 * prompt_test.go
 *	  GaussDB PromptBuilder content sanity checks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/prompt_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestGaussDBPromptBuilder_Interface(t *testing.T) {
	var _ sqltune.PromptBuilder = (*gaussdbPromptBuilder)(nil)
}

func TestGaussDBPromptBuilder_ContainsDialectKeywords(t *testing.T) {
	b := NewPromptBuilder()
	if !strings.Contains(b.RoleTag(), "GaussDB") {
		t.Errorf("RoleTag should mention GaussDB")
	}
	cbo := b.CBOKnowledge()
	// Critical: GS_PLAN_TRACE is the GaussDB unique differentiator
	for _, kw := range []string{"GS_PLAN_TRACE", "EXPLAIN PERFORMANCE", "gs_plan_trace", "300 MB"} {
		if !strings.Contains(cbo, kw) {
			t.Errorf("CBOKnowledge missing %q (GaussDB unique value)", kw)
		}
	}
	pr := b.PlanReading()
	for _, kw := range []string{"GS_PLAN_TRACE", "EXPLAIN PERFORMANCE", "Peak Memory"} {
		if !strings.Contains(pr, kw) {
			t.Errorf("PlanReading missing %q", kw)
		}
	}
	hs := b.HintSyntax()
	for _, kw := range []string{"HashJoin", "Leading", "SET enable_seqscan"} {
		if !strings.Contains(hs, kw) {
			t.Errorf("HintSyntax missing %q", kw)
		}
	}
}

func TestNewLLMAdapter_NilProvider_ReturnsNil(t *testing.T) {
	if newLLMAdapter(nil) != nil {
		t.Error("newLLMAdapter(nil) should return nil so GenericTuner falls back to raw mode")
	}
}
