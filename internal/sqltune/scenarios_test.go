/*-------------------------------------------------------------------------
 *
 * scenarios_test.go
 *	  End-to-end test harness for the sqltune pipeline. Defines a
 *	  Scenario struct + canned-LLM mock + assertion helpers shared
 *	  across all dialect packages' integration tests.
 *
 *	  Why a harness:
 *	    Each dialect's integration test re-runs the same 5-6 scenario
 *	    shapes (simple select, complex join, placeholder reject, DML
 *	    reject, big SQL G7 trigger, trace unavailable degrade). The
 *	    harness captures the common verification logic so each
 *	    dialect-specific test stays under ~50 lines.
 *
 *	  Real-DB integration vs mock harness:
 *	    The structs + helpers here work with EITHER a mock planner
 *	    (in unit-test mode) OR a real planner backed by db.Driver
 *	    (in *_integration_test.go gated by env var DSN). Same
 *	    Scenario shape, different planner.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/scenarios_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"context"
	"strings"
	"testing"
)

// Scenario is one end-to-end test case for the sqltune pipeline.
// Used both in the neutral package (with mockPlanner) and in each
// dialect's *_integration_test.go (with real planner).
type Scenario struct {
	Name string
	SQL  string

	// Behavior expectations:

	// ExpectError — if non-empty, Tune must return an error whose
	// message contains this substring. Used for DML rejection / SQL
	// syntax errors / placeholder routing.
	ExpectError string

	// ExpectPlaceholderKind — if non-empty, Tune must return a
	// *PlaceholderError with this DetectedKind.
	ExpectPlaceholderKind string

	// ExpectMarkdownContains — substrings the rendered markdown must
	// contain (per-scenario sanity checks).
	ExpectMarkdownContains []string

	// ExpectStats — non-nil → check specific fields are set.
	ExpectCompressionTriggered bool
	ExpectCandidateMin         int // 0 = skip check
}

// canonicalScenarios returns the shared scenario shapes that every
// dialect should pass with appropriate per-dialect SQL substitution.
// Per-dialect integration tests build on these by supplying their
// own dialect-specific SQL.
//
// Returns generic SQL shapes that work on most SQL DBs (SELECT 1,
// JOIN with mock tables). Dialect-specific scenarios (e.g. placeholder
// styles) live in each dialect's _integration_test.go.
func canonicalScenarios() []Scenario {
	return []Scenario{
		{
			Name: "trivial_select",
			SQL:  "SELECT 1",
			ExpectMarkdownContains: []string{
				"SQL", "调优",
			},
		},
		{
			Name:        "empty_sql_rejected",
			SQL:         "   ",
			ExpectError: "empty SQL",
		},
	}
}

// runScenario executes Tune with the given planner / builder / LLM
// and asserts the scenario's expectations. Returns the FinalReport
// for additional ad-hoc inspection.
func runScenario(t *testing.T, sc Scenario, planner DialectPlanner, builder PromptBuilder, llm LLMCaller) *FinalReport {
	t.Helper()
	tuner := NewGenericTuner(planner, builder, llm)
	report, err := tuner.Tune(context.Background(), TuneOptions{SQL: sc.SQL, Verify: true})

	if sc.ExpectPlaceholderKind != "" {
		var pe *PlaceholderError
		if !asPlaceholderError(err, &pe) {
			t.Fatalf("scenario %q: expected *PlaceholderError, got %T: %v", sc.Name, err, err)
		}
		if pe.DetectedKind != sc.ExpectPlaceholderKind {
			t.Errorf("scenario %q: DetectedKind = %q, want %q", sc.Name, pe.DetectedKind, sc.ExpectPlaceholderKind)
		}
		return nil
	}
	if sc.ExpectError != "" {
		if err == nil {
			t.Fatalf("scenario %q: expected error containing %q, got nil", sc.Name, sc.ExpectError)
		}
		if !strings.Contains(err.Error(), sc.ExpectError) {
			t.Errorf("scenario %q: error %q doesn't contain %q", sc.Name, err.Error(), sc.ExpectError)
		}
		return nil
	}
	if err != nil {
		t.Fatalf("scenario %q: unexpected error: %v", sc.Name, err)
	}
	if report == nil {
		t.Fatalf("scenario %q: nil report", sc.Name)
	}

	for _, want := range sc.ExpectMarkdownContains {
		if !strings.Contains(report.Markdown, want) {
			t.Errorf("scenario %q: markdown missing %q\nfirst 500 chars: %s",
				sc.Name, want, truncForLog(report.Markdown, 500))
		}
	}
	if sc.ExpectCompressionTriggered && !report.Stats.CompressionTriggered {
		t.Errorf("scenario %q: expected CompressionTriggered=true", sc.Name)
	}
	if sc.ExpectCandidateMin > 0 && report.Stats.CandidateCount < sc.ExpectCandidateMin {
		t.Errorf("scenario %q: CandidateCount = %d, want >= %d",
			sc.Name, report.Stats.CandidateCount, sc.ExpectCandidateMin)
	}
	return report
}

// asPlaceholderError is errors.As tailored to *PlaceholderError without
// importing errors in this file's prelude (already imported elsewhere
// in package, but kept local to keep test deps minimal).
func asPlaceholderError(err error, target **PlaceholderError) bool {
	for err != nil {
		if pe, ok := err.(*PlaceholderError); ok {
			*target = pe
			return true
		}
		// Unwrap by attempting common Unwrap signature
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func truncForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// ── Canned LLM responses for scenarios ─────────────────────────────────

// cannedLLM returns a mock LLMCaller that replies with a hardcoded
// Round1Output JSON. Used by canonical scenarios that need to verify
// the Round 1 path works end-to-end without burning real LLM budget.
type cannedLLM struct {
	jsonReply string
}

func (c *cannedLLM) Chat(ctx context.Context, msgs []ChatMessage) (string, error) {
	return c.jsonReply, nil
}

// defaultCannedReply is a minimal valid Round1Output for happy-path tests.
func defaultCannedReply() string {
	return `{
  "confidence": 0.85,
  "cbo_analysis": "Mock CBO analysis: planner chose Seq Scan due to small estimated rows.",
  "candidates": [
    {"id": 1, "type": "rewrite", "sql": "SELECT 1 /* rewritten */", "rationale": "use hint", "expected_gain": "5x", "risk_level": "low"},
    {"id": 2, "type": "index", "sql": "CREATE INDEX idx_t_id ON t(id);", "rationale": "selective", "expected_gain": "10x", "risk_level": "low"}
  ],
  "explored_dimensions": ["rewrite", "index"]
}`
}

// ── Tests using the harness ────────────────────────────────────────────

func TestHarness_RunCanonicalScenarios_NilLLM(t *testing.T) {
	// Run canonical scenarios with nil LLM — verifies raw Phase A path
	// works for all canonical scenarios.
	planner := &mockPlanner{
		plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", TotalCost: 100}, TotalCost: 100},
	}
	for _, sc := range canonicalScenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			runScenario(t, sc, planner, mockBuilder{}, nil)
		})
	}
}

func TestHarness_RunCanonicalScenarios_WithLLM(t *testing.T) {
	planner := &mockPlanner{
		plan:       &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", TotalCost: 100}, TotalCost: 100},
		quickCosts: map[string]float64{"SELECT 1": 100, "SELECT 1 /* rewritten */": 20, "SELECT 1 /* hint */": 10},
	}
	llm := &cannedLLM{jsonReply: defaultCannedReply()}
	for _, sc := range canonicalScenarios() {
		t.Run(sc.Name, func(t *testing.T) {
			report := runScenario(t, sc, planner, mockBuilder{}, llm)
			if report != nil && sc.ExpectError == "" {
				// Verify Round 1 ran — should have CandidateCount > 0.
				if report.Stats.CandidateCount != 2 {
					t.Errorf("scenario %q: expected 2 candidates from canned LLM, got %d",
						sc.Name, report.Stats.CandidateCount)
				}
			}
		})
	}
}

func TestHarness_PlaceholderError_Routed(t *testing.T) {
	planner := &mockPlanner{
		normalizErr: &PlaceholderError{
			SQL:          "SELECT * FROM t WHERE id=$1",
			Placeholders: []string{"$1"},
			DetectedKind: "pg_dollar",
			Suggestion:   "fetch from pg_stat_statements",
			Recoverable:  true,
		},
	}
	sc := Scenario{
		Name:                  "pg_placeholder_rejected",
		SQL:                   "SELECT * FROM t WHERE id=$1",
		ExpectPlaceholderKind: "pg_dollar",
	}
	runScenario(t, sc, planner, mockBuilder{}, nil)
}

func TestHarness_PlaceholderKinds_AllDialects(t *testing.T) {
	// Verify the harness correctly routes all three known placeholder
	// styles (pg_dollar, qmark, oracle_colon) from each dialect.
	cases := []struct {
		dialect string
		sql     string
		kind    string
	}{
		{"pg", "SELECT * FROM t WHERE id=$1", "pg_dollar"},
		{"mysql", "SELECT * FROM t WHERE id=?", "qmark"},
		{"oracle", "SELECT * FROM emp WHERE empno=:1", "oracle_colon"},
	}
	for _, c := range cases {
		t.Run(c.dialect, func(t *testing.T) {
			planner := &mockPlanner{
				normalizErr: &PlaceholderError{
					SQL:          c.sql,
					Placeholders: []string{strings.SplitN(c.sql, "=", 2)[1]},
					DetectedKind: c.kind,
				},
			}
			runScenario(t, Scenario{
				Name:                  c.dialect + "_placeholder",
				SQL:                   c.sql,
				ExpectPlaceholderKind: c.kind,
			}, planner, mockBuilder{}, nil)
		})
	}
}

func TestHarness_BigSQL_TriggersCompression(t *testing.T) {
	bigSQL := strings.Repeat("SELECT 1;\n", 600)
	planner := &mockPlanner{
		plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", TotalCost: 100}, TotalCost: 100},
	}
	sc := Scenario{
		Name:                       "big_sql_g7",
		SQL:                        bigSQL,
		ExpectCompressionTriggered: true,
	}
	runScenario(t, sc, planner, mockBuilder{}, nil)
}

func TestHarness_TraceUnavailable_DegradesGracefully(t *testing.T) {
	planner := &mockPlanner{
		plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", TotalCost: 100}, TotalCost: 100},
		traceData: &TraceData{
			Available: false,
			Format:    "none",
			Notes:     "PG 开源版无 CBO 决策跟踪机制",
		},
	}
	sc := Scenario{
		Name: "trace_unavailable",
		SQL:  "SELECT 1",
		ExpectMarkdownContains: []string{"CBO 决策跟踪", "无 CBO"},
	}
	runScenario(t, sc, planner, mockBuilder{}, nil)
}
