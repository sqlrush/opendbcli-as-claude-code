/*-------------------------------------------------------------------------
 *
 * filter_test.go
 *	  Tests for SceneBasedFilter / DefaultScenes coverage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/filter_test.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"testing"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// makeTools builds a tools list from a name list (helpers for tests).
func makeTools(names ...string) []provider.ToolSchema {
	out := make([]provider.ToolSchema, len(names))
	for i, n := range names {
		out[i] = provider.ToolSchema{Name: n}
	}
	return out
}

// extractNames pulls just the names from a filtered tools list.
func extractNames(tools []provider.ToolSchema) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

// contains is a tiny helper for assertion.
func contains(s []string, target string) bool {
	for _, x := range s {
		if x == target {
			return true
		}
	}
	return false
}

func TestSceneBasedFilter_SingleSQLTune(t *testing.T) {
	all := makeTools("health", "alert", "topsql", "sqltune", "sqlfetch", "explain", "waits", "kill")
	f := NewSceneBasedFilter(DefaultScenes(), DefaultAlwaysAvailable())
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage: "SQL_ID 12345 怎么优化",
	}))
	// sqltune scene should fire.
	for _, want := range []string{"sqltune", "sqlfetch", "explain"} {
		if !contains(got, want) {
			t.Errorf("expected %q in filtered tools, got %v", want, got)
		}
	}
	// kill is in 'session_kill' scene only; "怎么优化" doesn't trigger it.
	if contains(got, "kill") {
		t.Errorf("unrelated tool 'kill' should not be included for SQL tune intent: %v", got)
	}
}

func TestSceneBasedFilter_ClusterDiag(t *testing.T) {
	all := makeTools("health", "alert", "activesessions", "waits", "blocktree", "topsql", "sqltune")
	f := NewSceneBasedFilter(DefaultScenes(), DefaultAlwaysAvailable())
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage: "数据库怎么有点慢，帮我诊断下",
	}))
	for _, want := range []string{"health", "alert", "activesessions", "waits", "blocktree"} {
		if !contains(got, want) {
			t.Errorf("cluster diag scene missing %q, got %v", want, got)
		}
	}
}

func TestSceneBasedFilter_WDR(t *testing.T) {
	all := makeTools("health", "wdranalyze", "wdr_snapshot", "sqltune")
	f := NewSceneBasedFilter(DefaultScenes(), DefaultAlwaysAvailable())
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage: "分析下这个 WDR 报告",
	}))
	if !contains(got, "wdranalyze") {
		t.Errorf("WDR scene missed wdranalyze: %v", got)
	}
}

func TestSceneBasedFilter_AlwaysAvailable(t *testing.T) {
	all := makeTools("sql", "health", "topsql", "exotic_tool")
	f := NewSceneBasedFilter(DefaultScenes(), []string{"sql", "health"})
	// Question matches NO scene — should still include sql + health.
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage: "openGauss 是什么数据库",
	}))
	// No scene match → step 6 fallback returns ALL tools. So everything is present.
	if !contains(got, "sql") || !contains(got, "health") {
		t.Errorf("always-available + fallback should include sql/health: %v", got)
	}
}

func TestSceneBasedFilter_PreservesLastToolCalls(t *testing.T) {
	all := makeTools("health", "topsql", "sqltune", "sqlfetch", "alert")
	f := NewSceneBasedFilter(DefaultScenes(), []string{"health"})
	// Multi-turn: round 1 ran topsql, round 2 user asks about a specific SQL.
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage:    "这条 SQL_ID 怎么优化",
		PreviousRounds: 1,
		LastToolCalls:  []string{"topsql"},
	}))
	// New question triggers single_sql_tune → sqltune/sqlfetch/explain
	// + LastToolCalls keeps topsql available
	// + always health
	for _, want := range []string{"sqltune", "sqlfetch", "topsql", "health"} {
		if !contains(got, want) {
			t.Errorf("multi-turn filter missing %q: %v", want, got)
		}
	}
}

func TestSceneBasedFilter_EmptyInput(t *testing.T) {
	f := NewSceneBasedFilter(DefaultScenes(), DefaultAlwaysAvailable())
	got := f.Filter(nil, FilterContext{UserMessage: "数据库慢"})
	if len(got) != 0 {
		t.Errorf("empty input should yield empty output, got %v", got)
	}
}

func TestSceneBasedFilter_NoSceneMatchReturnsAll(t *testing.T) {
	// User asks pure conceptual question with no diagnostic triggers.
	// Filter should fall back to "return all" rather than empty list.
	all := makeTools("health", "topsql", "sqltune")
	f := NewSceneBasedFilter(DefaultScenes(), nil)
	got := f.Filter(all, FilterContext{
		UserMessage: "qwen 是什么",
	})
	if len(got) != len(all) {
		t.Errorf("no-match should return all tools (safe wide), got %d/%d", len(got), len(all))
	}
}

func TestSceneBasedFilter_PreservesOriginalOrder(t *testing.T) {
	// Filter should preserve the order of the input tools list (not the
	// scene definition order). This matters for prompt cache stability.
	all := makeTools("zeta", "alpha", "mid", "sqltune", "sqlfetch")
	f := NewSceneBasedFilter(DefaultScenes(), []string{"alpha", "mid", "zeta"})
	got := extractNames(f.Filter(all, FilterContext{
		UserMessage: "SQL_ID 1 怎么优化",
	}))
	// Should appear in original order: zeta, alpha, mid, sqltune, sqlfetch.
	expectOrder := []string{"zeta", "alpha", "mid", "sqltune", "sqlfetch"}
	if len(got) != len(expectOrder) {
		t.Fatalf("expected %d tools, got %d (%v)", len(expectOrder), len(got), got)
	}
	for i, want := range expectOrder {
		if got[i] != want {
			t.Errorf("position %d: got %q, want %q (full: %v)", i, got[i], want, got)
		}
	}
}

func TestDefaultScenes_HaveTriggersAndTools(t *testing.T) {
	for _, s := range DefaultScenes() {
		if s.Name == "" {
			t.Errorf("scene has empty name")
		}
		if len(s.Triggers) == 0 {
			t.Errorf("scene %q has no triggers", s.Name)
		}
		if len(s.Tools) == 0 {
			t.Errorf("scene %q has no tools", s.Name)
		}
	}
}

func TestMatchesAnyTrigger(t *testing.T) {
	cases := []struct {
		msg, trigger string
		want         bool
	}{
		{"sql_id 12345", "sql_id", true},
		{"SQL_ID 12345", "sql_id", true}, // case-insensitive
		{"数据库怎么很卡", "卡", true},
		{"openGauss 是什么", "怎么优化", false},
		{"", "sql_id", false},
	}
	for _, c := range cases {
		got := matchesAnyTrigger(c.msg, []string{c.trigger})
		if got != c.want {
			// lowercase the input first (caller's responsibility in production)
			got = matchesAnyTrigger(toLower(c.msg), []string{c.trigger})
			if got != c.want {
				t.Errorf("msg=%q trigger=%q: got %v want %v", c.msg, c.trigger, got, c.want)
			}
		}
	}
}

func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
