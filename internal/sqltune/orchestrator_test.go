/*-------------------------------------------------------------------------
 *
 * orchestrator_test.go
 *	  Tests for GenericTuner using mock DialectPlanner + mock LLMCaller.
 *	  Covers: nil-LLM fallback, Round 1 success, Round 1 JSON parse,
 *	  placeholder propagation, verifyOne for rewrite/index/unknown,
 *	  prompt assembly.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/orchestrator_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// ── Mocks ──────────────────────────────────────────────────────────────

type mockPlanner struct {
	plan        *PlanInfo
	planErr     error
	dialect     *DialectInfo
	runtime     *RuntimeInfo
	schema      *SchemaInfo
	involved    []string
	normalizErr error // returned from NormalizePlaceholders
	quickCosts  map[string]float64
	traceData   *TraceData
}

func (m *mockPlanner) Kind() DialectKind { return "mock" }
func (m *mockPlanner) ExplainPlan(ctx context.Context, sql string, opts ExplainOptions) (*PlanInfo, error) {
	return m.plan, m.planErr
}
func (m *mockPlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	if c, ok := m.quickCosts[sql]; ok {
		return c, nil
	}
	return 0, nil
}
func (m *mockPlanner) CollectSchema(ctx context.Context, sql string) (*SchemaInfo, []string, error) {
	return m.schema, m.involved, nil
}
func (m *mockPlanner) SnapshotDialect(ctx context.Context) (*DialectInfo, error) {
	return m.dialect, nil
}
func (m *mockPlanner) SnapshotRuntime(ctx context.Context, tables []string) (*RuntimeInfo, error) {
	return m.runtime, nil
}
func (m *mockPlanner) ExpandViews(ctx context.Context, sql string) (string, error) { return "", nil }
func (m *mockPlanner) EnableTrace(ctx context.Context, tag string) (func() error, *TraceData, error) {
	return func() error { return nil }, m.traceData, nil
}
func (m *mockPlanner) CollectTrace(ctx context.Context, tag string) (*TraceData, error) {
	return m.traceData, nil
}
func (m *mockPlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	return sql, m.normalizErr
}

type mockBuilder struct{}

func (mockBuilder) RoleTag() string      { return "Mock DB 调优专家" }
func (mockBuilder) CBOKnowledge() string { return "mock CBO knowledge" }
func (mockBuilder) PlanReading() string  { return "mock plan reading" }
func (mockBuilder) HintSyntax() string   { return "mock hint syntax" }

type mockLLM struct {
	reply string
	err   error
}

func (m *mockLLM) Chat(ctx context.Context, msgs []ChatMessage) (string, error) {
	return m.reply, m.err
}

// ── Tests ──────────────────────────────────────────────────────────────

func TestGenericTuner_NilLLM_FallsBackToRawPhaseA(t *testing.T) {
	p := &mockPlanner{
		plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", RelationName: "t", TotalCost: 100}, TotalCost: 100},
	}
	tuner := NewGenericTuner(p, mockBuilder{}, nil)
	rep, err := tuner.Tune(context.Background(), TuneOptions{SQL: "SELECT 1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(rep.Markdown, "raw Phase A only") {
		t.Errorf("expected raw Phase A banner, got: %s", rep.Markdown[:200])
	}
	if !strings.Contains(rep.Markdown, "LLM 未配置") {
		t.Errorf("expected LLM-missing banner")
	}
}

func TestGenericTuner_EmptySQL_Error(t *testing.T) {
	p := &mockPlanner{}
	tuner := NewGenericTuner(p, mockBuilder{}, nil)
	_, err := tuner.Tune(context.Background(), TuneOptions{SQL: "   "})
	if err == nil {
		t.Fatal("expected error for empty SQL")
	}
}

func TestGenericTuner_PlaceholderError_Propagates(t *testing.T) {
	pe := &PlaceholderError{SQL: "SELECT * FROM t WHERE id=$1", Placeholders: []string{"$1"}, DetectedKind: "pg_dollar"}
	p := &mockPlanner{normalizErr: pe}
	tuner := NewGenericTuner(p, mockBuilder{}, &mockLLM{reply: "{}"})
	_, err := tuner.Tune(context.Background(), TuneOptions{SQL: "SELECT * FROM t WHERE id=$1"})
	var got *PlaceholderError
	if !errors.As(err, &got) {
		t.Fatalf("expected PlaceholderError to propagate, got %T: %v", err, err)
	}
	if got.DetectedKind != "pg_dollar" {
		t.Errorf("DetectedKind preserved? got %q", got.DetectedKind)
	}
}

func TestGenericTuner_Round1Success_RendersCandidates(t *testing.T) {
	p := &mockPlanner{
		plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan", TotalCost: 1000}, TotalCost: 1000},
		quickCosts: map[string]float64{
			"SELECT 1":                  1000,
			"SELECT 1 /*+ INDEX(t) */":  100,
		},
	}
	llm := &mockLLM{reply: `{
        "confidence": 0.9,
        "cbo_analysis": "CBO chose Seq Scan because rows_estimate=1M",
        "candidates": [
          {"id": 1, "type": "rewrite", "sql": "SELECT 1 /*+ INDEX(t) */", "rationale": "use idx", "expected_gain": "10x", "risk_level": "low"}
        ],
        "explored_dimensions": ["rewrite"]
    }`}
	tuner := NewGenericTuner(p, mockBuilder{}, llm)
	rep, err := tuner.Tune(context.Background(), TuneOptions{SQL: "SELECT 1", Verify: true})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if rep.Stats.CandidateCount != 1 {
		t.Errorf("CandidateCount = %d, want 1", rep.Stats.CandidateCount)
	}
	if rep.Stats.VerifiedCount != 1 {
		t.Errorf("VerifiedCount = %d, want 1", rep.Stats.VerifiedCount)
	}
	if rep.Stats.BestSpeedup < 9.9 || rep.Stats.BestSpeedup > 10.1 {
		t.Errorf("BestSpeedup = %.2f, want ~10", rep.Stats.BestSpeedup)
	}
	for _, must := range []string{"#1", "[rewrite]", "10.0×", "CBO chose Seq Scan"} {
		if !strings.Contains(rep.Markdown, must) {
			t.Errorf("report missing %q", must)
		}
	}
}

func TestGenericTuner_Round1JSONParseFailure_GracefulFallback(t *testing.T) {
	p := &mockPlanner{plan: &PlanInfo{Root: &PlanNode{Operator: "Seq Scan"}}}
	llm := &mockLLM{reply: "not valid JSON at all"}
	tuner := NewGenericTuner(p, mockBuilder{}, llm)
	rep, err := tuner.Tune(context.Background(), TuneOptions{SQL: "SELECT 1"})
	if err != nil {
		t.Fatalf("should not error — fallback to Phase A: %v", err)
	}
	if !strings.Contains(rep.Markdown, "Round 1 LLM 调用失败") {
		t.Errorf("expected Round 1 failure banner, got %.200s", rep.Markdown)
	}
}

func TestParseRound1JSON_StripsMarkdownFence(t *testing.T) {
	cases := []string{
		"```json\n{\"confidence\": 0.5, \"candidates\": []}\n```",
		"```\n{\"confidence\": 0.5, \"candidates\": []}\n```",
		`{"confidence": 0.5, "candidates": []}`, // already plain
	}
	for _, s := range cases {
		out, err := parseRound1JSON(s)
		if err != nil {
			t.Errorf("parse failed for %q: %v", s, err)
			continue
		}
		if out.Confidence != 0.5 {
			t.Errorf("got %v, want 0.5", out.Confidence)
		}
	}
}

func TestAssembleSystemPrompt_ContainsBuilderSections(t *testing.T) {
	p := assembleSystemPrompt(mockBuilder{}, &DialectInfo{Version: "Mock 1.0", Parameters: map[string]string{"key1": "val1"}})
	for _, must := range []string{
		"Mock DB 调优专家",
		"mock CBO knowledge",
		"mock plan reading",
		"mock hint syntax",
		"Mock 1.0",
		"key1 = val1",
		"JSON",
		"调优原则",
		"禁用措辞",
	} {
		if !strings.Contains(p, must) {
			t.Errorf("system prompt missing %q", must)
		}
	}
}

func TestAssembleSystemPrompt_NilDialect_Safe(t *testing.T) {
	p := assembleSystemPrompt(mockBuilder{}, nil)
	if !strings.Contains(p, "未采集到 dialect snapshot") {
		t.Errorf("nil dialect fallback missing")
	}
}

func TestVerifyOne_RewriteWithEquivCheck(t *testing.T) {
	// Mock that implements both DialectPlanner and EquivVerifier
	p := &mockEquivPlanner{
		mockPlanner: mockPlanner{quickCosts: map[string]float64{"new": 50}},
		equiv:       true,
	}
	r := verifyOne(context.Background(), p, p, "orig", 100, Candidate{ID: 1, Type: "rewrite", SQL: "new"}, true)
	if !r.Verifiable {
		t.Error("rewrite candidate should be verifiable")
	}
	if r.Speedup < 1.99 || r.Speedup > 2.01 {
		t.Errorf("Speedup = %.2f, want 2.0", r.Speedup)
	}
	if r.EquivOK == nil || !*r.EquivOK {
		t.Error("EquivOK should be set to true")
	}
}

func TestVerifyOne_DDLCandidate_Unverifiable(t *testing.T) {
	p := &mockPlanner{}
	for _, ddlType := range []string{"index", "schema", "stats"} {
		r := verifyOne(context.Background(), p, nil, "orig", 100, Candidate{ID: 1, Type: ddlType, SQL: "CREATE INDEX..."}, true)
		if r.Verifiable {
			t.Errorf("%s should be unverifiable", ddlType)
		}
		if r.Note == "" {
			t.Errorf("%s should have a note", ddlType)
		}
	}
}

func TestVerifyOne_UnknownType_ReturnsNote(t *testing.T) {
	p := &mockPlanner{}
	r := verifyOne(context.Background(), p, nil, "orig", 100, Candidate{ID: 1, Type: "futuristic_type"}, true)
	if r.Verifiable {
		t.Error("unknown type should be unverifiable")
	}
	if !strings.Contains(r.Note, "futuristic_type") {
		t.Errorf("note should mention unknown type: %q", r.Note)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.n); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
}

// mockEquivPlanner implements both DialectPlanner and EquivVerifier
type mockEquivPlanner struct {
	mockPlanner
	equiv bool
}

func (m *mockEquivPlanner) VerifyEquivalence(ctx context.Context, orig, cand string, limit int) (bool, error) {
	return m.equiv, nil
}
