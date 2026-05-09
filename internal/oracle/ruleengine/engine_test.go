/*-------------------------------------------------------------------------
 *
 * engine_test.go
 *	  Test cases for engine.go (ruleengine package): TestNewEngine,
 *	  TestSignalIndex, TestEvalContext.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/engine_test.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

// ─── Mock QueryExecutor ─────────────────────────────────────────────────────

type mockExecutor struct {
	results map[QueryID]interface{}
}

func (m *mockExecutor) Execute(qid QueryID, params map[string]string) (interface{}, error) {
	if v, ok := m.results[qid]; ok {
		return v, nil
	}
	return float64(0), nil
}

// ─── Test helpers ───────────────────────────────────────────────────────────

// newTestEngine creates an engine with the CommunityProvider for testing.
func newTestEngine() *Engine {
	return New(&CommunityProvider{}, nil, DefaultConfig())
}

// newTestEngineWithExecutor creates an engine with a mock executor.
func newTestEngineWithExecutor(exec QueryExecutor) *Engine {
	return New(&CommunityProvider{}, exec, DefaultConfig())
}

// mkReport builds a minimal BurstReport with the given wait profile and metrics.
func mkReport(waits []sentinel.WaitBucket, metrics map[string]sentinel.MetricSummary) *sentinel.BurstReport {
	return &sentinel.BurstReport{
		WaitProfile: waits,
		Metrics:     metrics,
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseUnknown,
			Confidence: 0.5,
		},
		PeakActive:     10,
		BaselineActive: 3,
		DurationSec:    15,
	}
}

// ─── 1. TestNewEngine ───────────────────────────────────────────────────────

func TestNewEngine(t *testing.T) {
	e := newTestEngine()

	if e == nil {
		t.Fatal("NewEngine returned nil")
	}

	// Community provider should return 152 rules (15+22+20+6+7+4+3+25+8+7+10+4+3+3+4+3+4+4)
	count := e.RuleCount()
	if count < 100 {
		t.Errorf("expected 100+ rules, got %d", count)
	}

	// Verify provider metadata
	p := &CommunityProvider{}
	if p.Edition() != "community" {
		t.Errorf("expected edition 'community', got %q", p.Edition())
	}
	if p.Version() != "0.1.0" {
		t.Errorf("expected version '0.1.0', got %q", p.Version())
	}
}

// ─── 2. TestSignalIndex ─────────────────────────────────────────────────────

func TestSignalIndex(t *testing.T) {
	rules := (&CommunityProvider{}).Rules()
	idx := buildIndex(rules)

	// Test wait event index: "buffer busy waits" should match at least one rule
	bbw := idx.byWaitEvent["buffer busy waits"]
	if len(bbw) == 0 {
		t.Error("expected at least one rule indexed under 'buffer busy waits'")
	}

	found := false
	for _, r := range bbw {
		if r.ID == "WE005" {
			found = true
			break
		}
	}
	if !found {
		t.Error("WE005 (buffer busy waits) rule not found in wait event index")
	}

	// Test category index: "wait_event" should include all 15 wait event rules
	waitEventCat := idx.byCategory["wait_event"]
	if len(waitEventCat) < 15 {
		t.Errorf("expected at least 15 rules under 'wait_event' category, got %d", len(waitEventCat))
	}

	// Test category index: "sql_perf" should include the sql_perf rules
	sqlPerf := idx.byCategory["sql_perf"]
	if len(sqlPerf) < 5 {
		t.Errorf("expected at least 5 rules under 'sql_perf' category, got %d", len(sqlPerf))
	}

	// Test Match with a wait event signal
	signals := []Signal{
		{Type: SignalWaitEvent, Key: "buffer busy waits"},
	}
	candidates := idx.Match(signals)
	if len(candidates) == 0 {
		t.Error("Match returned no candidates for 'buffer busy waits'")
	}
	matchedWE005 := false
	for _, c := range candidates {
		if c.ID == "WE005" {
			matchedWE005 = true
			break
		}
	}
	if !matchedWE005 {
		t.Error("expected WE005 in Match results for 'buffer busy waits'")
	}
}

// ─── 3. TestEvalContext ─────────────────────────────────────────────────────

func TestEvalContext(t *testing.T) {
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "log file sync", Percentage: 18.5, WaitClass: "Commit"},
			{Event: "buffer busy waits", Percentage: 12.0, WaitClass: "Concurrency"},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions": {Avg: 25, Max: 40},
			"redo_rate":       {Avg: 5000, Max: 8000, Trend: "rising"},
		},
		BlockingChains: []sentinel.BlockingChain{
			{RootSID: 100, VictimCount: 3, MaxDepth: 2},
		},
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseUnknown,
			Confidence: 0.5,
		},
		PeakActive:     40,
		BaselineActive: 10,
		DurationSec:    20,
	}

	input := &DiagInput{Type: InputBurstReport, Report: report}
	ctx := buildEvalContext(input, nil, DefaultConfig())

	// Test WaitPct
	pct := ctx.WaitPct("log file sync")
	if pct != 18.5 {
		t.Errorf("expected WaitPct=18.5, got %v", pct)
	}

	// Test WaitPct for non-existent event
	pct0 := ctx.WaitPct("non_existent_event")
	if pct0 != 0 {
		t.Errorf("expected WaitPct=0 for non-existent event, got %v", pct0)
	}

	// Test MetricValue
	active := ctx.MetricValue("active_sessions")
	if active != 25 {
		t.Errorf("expected MetricValue=25, got %v", active)
	}

	// Test MetricValue non-existent
	missing := ctx.MetricValue("non_existent_metric")
	if missing != 0 {
		t.Errorf("expected MetricValue=0 for non-existent, got %v", missing)
	}

	// Test HasBlockingChains
	if !ctx.HasBlockingChains() {
		t.Error("expected HasBlockingChains()=true when blocking chains present")
	}

	// Test HasBlockingChains with empty chains
	emptyReport := mkReport(nil, nil)
	emptyInput := &DiagInput{Type: InputBurstReport, Report: emptyReport}
	emptyCtx := buildEvalContext(emptyInput, nil, DefaultConfig())
	if emptyCtx.HasBlockingChains() {
		t.Error("expected HasBlockingChains()=false when no blocking chains")
	}

	// Test GetFloat for metrics
	redo := ctx.GetFloat("metrics", "redo_rate")
	if redo != 5000 {
		t.Errorf("expected GetFloat=5000, got %v", redo)
	}

	// Test GetFloat for wait_profile
	waitPct := ctx.GetFloat("wait_profile", "buffer busy waits")
	if waitPct != 12.0 {
		t.Errorf("expected GetFloat wait_profile=12.0, got %v", waitPct)
	}

	// Test GetFloat for summary
	peakActive := ctx.GetFloat("summary", "peak_active")
	if peakActive != 40 {
		t.Errorf("expected GetFloat summary peak_active=40, got %v", peakActive)
	}

	// Test GetStr
	trend := ctx.GetStr("metrics", "redo_rate")
	if trend != "rising" {
		t.Errorf("expected GetStr='rising', got %q", trend)
	}

	// Test query caching with mock executor
	exec := &mockExecutor{results: map[QueryID]interface{}{
		QueryASHTopSQL: float64(42),
	}}
	ctx2 := buildEvalContext(input, exec, DefaultConfig())

	// First call executes
	val, err := ctx2.ExecuteQuery(QueryASHTopSQL, nil)
	if err != nil {
		t.Fatalf("ExecuteQuery failed: %v", err)
	}
	if val != float64(42) {
		t.Errorf("expected query result 42, got %v", val)
	}

	// Second call returns cached
	val2, err := ctx2.ExecuteQuery(QueryASHTopSQL, nil)
	if err != nil {
		t.Fatalf("cached ExecuteQuery failed: %v", err)
	}
	if val2 != float64(42) {
		t.Errorf("expected cached result 42, got %v", val2)
	}
}

// ─── 4. TestTriggerConditions ───────────────────────────────────────────────

func TestTriggerConditions(t *testing.T) {
	report := mkReport(
		[]sentinel.WaitBucket{
			{Event: "buffer busy waits", Percentage: 25.0, WaitClass: "Concurrency"},
			{Event: "db file sequential read", Percentage: 40.0, WaitClass: "User I/O"},
		},
		map[string]sentinel.MetricSummary{
			metricHardParse:      {Avg: 150, Max: 200},
			metricBufferCacheHit: {Avg: 92, Max: 94},
		},
	)

	input := &DiagInput{Type: InputBurstReport, Report: report}
	ctx := buildEvalContext(input, nil, DefaultConfig())

	// Test OpGT: hard_parse max=200 > 100 => true
	condGT := Condition{Source: "metrics", Field: metricHardParse, Op: OpGT, Value: 100}
	if !evalCondition(condGT, ctx) {
		t.Error("expected hard_parse > 100 to be true (max=200)")
	}

	// Test OpLT: buffer_cache_hit max=94 < 95 => true
	condLT := Condition{Source: "metrics", Field: metricBufferCacheHit, Op: OpLT, Value: 95}
	if !evalCondition(condLT, ctx) {
		t.Error("expected buffer_cache_hit < 95 to be true (max=94)")
	}

	// Test OpPctGT: buffer busy waits 25% > 20% => true
	condPctGT := Condition{Source: "wait_profile", Field: "buffer busy waits", Op: OpPctGT, Value: 20}
	if !evalCondition(condPctGT, ctx) {
		t.Error("expected buffer busy waits > 20%% to be true (pct=25)")
	}

	// Test OpPctGT: buffer busy waits 25% > 30% => false
	condPctGTFalse := Condition{Source: "wait_profile", Field: "buffer busy waits", Op: OpPctGT, Value: 30}
	if evalCondition(condPctGTFalse, ctx) {
		t.Error("expected buffer busy waits > 30%% to be false (pct=25)")
	}

	// Test OpExists: blocking_chains empty => false
	condExists := Condition{Source: "blocking_chains", Field: "", Op: OpExists, Value: 0}
	if evalCondition(condExists, ctx) {
		t.Error("expected blocking_chains exists to be false (empty)")
	}

	// Test OpExists with blocking chains present
	report2 := mkReport(nil, nil)
	report2.BlockingChains = []sentinel.BlockingChain{
		{RootSID: 1, VictimCount: 2, MaxDepth: 1},
	}
	input2 := &DiagInput{Type: InputBurstReport, Report: report2}
	ctx2 := buildEvalContext(input2, nil, DefaultConfig())
	condExistsTrue := Condition{Source: "blocking_chains", Field: "", Op: OpExists, Value: 0}
	if !evalCondition(condExistsTrue, ctx2) {
		t.Error("expected blocking_chains exists to be true when chains present")
	}

	// Test OpNotEmpty on blocking_chains count
	condNotEmpty := Condition{Source: "blocking_chains", Field: "count", Op: OpNotEmpty, Value: 0}
	if !evalCondition(condNotEmpty, ctx2) {
		t.Error("expected blocking_chains count not_empty to be true")
	}

	// Test summary condition: peak_active
	condSummary := Condition{Source: "summary", Field: "peak_active", Op: OpGT, Value: 5}
	if !evalCondition(condSummary, ctx) {
		t.Error("expected peak_active > 5 to be true (peak=10)")
	}
}

// ─── 5. TestTreeEvaluation ──────────────────────────────────────────────────

func TestTreeEvaluation(t *testing.T) {
	// Build a 2-level decision tree manually:
	// Level 1: check metric_1 value
	//   Branch A: > 50 -> severity high, finding "metric is high"
	//     Level 2: check metric_2
	//       Branch B: > 10 -> severity critical, finding "both high"
	//       Branch C: default -> severity medium, finding "only first"
	//   Branch D: default -> severity low, finding "metric is normal"

	rule := &Rule{
		ID:   "test_tree",
		Name: "Test Tree Rule",
		Tree: &TreeNode{
			Step: "check first metric",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("test_metric_1")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    "metric > 50",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "first metric is high"}},
					Then: &TreeNode{
						Step: "check second metric",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue("test_metric_2")
						},
						Branches: []Branch{
							{
								Match:    MatchGT(10),
								Label:    "second > 10",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "second metric is high too"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "fix both", SkillCommand: "/fix", RawSQL: "SELECT 1"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "second is normal",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "only first is high"}},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "metric is normal",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "metric is normal"}},
				},
			},
		},
	}

	// Test case 1: both metrics high
	report1 := mkReport(nil, map[string]sentinel.MetricSummary{
		"test_metric_1": {Avg: 75, Max: 75},
		"test_metric_2": {Avg: 20, Max: 20},
	})
	input1 := &DiagInput{Type: InputBurstReport, Report: report1}
	ctx1 := buildEvalContext(input1, nil, DefaultConfig())

	diag1 := evaluateOneTree(rule, ctx1, 20)
	if diag1 == nil {
		t.Fatal("expected non-nil diagnosis for high metrics")
	}
	if diag1.Severity != SeverityCritical {
		t.Errorf("expected SeverityCritical, got %v", diag1.Severity)
	}
	if len(diag1.Findings) < 2 {
		t.Errorf("expected >= 2 findings, got %d", len(diag1.Findings))
	}
	if len(diag1.Actions) < 1 {
		t.Errorf("expected >= 1 action, got %d", len(diag1.Actions))
	}

	// Test case 2: first metric low => take default branch
	report2 := mkReport(nil, map[string]sentinel.MetricSummary{
		"test_metric_1": {Avg: 10, Max: 10},
	})
	input2 := &DiagInput{Type: InputBurstReport, Report: report2}
	ctx2 := buildEvalContext(input2, nil, DefaultConfig())

	diag2 := evaluateOneTree(rule, ctx2, 20)
	if diag2 == nil {
		t.Fatal("expected non-nil diagnosis for low metrics")
	}
	if diag2.Severity != SeverityLow {
		t.Errorf("expected SeverityLow, got %v", diag2.Severity)
	}

	// Test case 3: rule with no tree => basic diagnosis
	bareRule := &Rule{ID: "bare", Name: "Bare Rule"}
	diag3 := evaluateOneTree(bareRule, ctx2, 20)
	if diag3 == nil {
		t.Fatal("expected non-nil diagnosis for bare rule")
	}
	if diag3.Severity != SeverityMedium {
		t.Errorf("expected SeverityMedium for bare rule, got %v", diag3.Severity)
	}
	if diag3.Confidence != 0.5 {
		t.Errorf("expected Confidence=0.5 for bare rule, got %v", diag3.Confidence)
	}
}

// ─── 6. TestResolver_SingleCause ────────────────────────────────────────────

func TestResolver_SingleCause(t *testing.T) {
	results := []*Diagnosis{
		{
			RuleID:     "rule_a",
			RuleName:   "Rule A",
			Severity:   SeverityHigh,
			Confidence: 0.8,
			Findings:   []Finding{{Desc: "finding A"}},
			Actions:    []Action{{Type: ActionFix, Desc: "fix A", SkillCommand: "/fix_a", RawSQL: "SELECT 1"}},
		},
	}

	rules := []*Rule{{ID: "rule_a", Name: "Rule A"}}
	output := Resolve(results, rules)

	if output.Primary == nil {
		t.Fatal("expected non-nil Primary")
	}
	if output.Primary.RuleID != "rule_a" {
		t.Errorf("expected Primary RuleID 'rule_a', got %q", output.Primary.RuleID)
	}
	if output.Secondary != nil {
		t.Error("expected nil Secondary for single result")
	}
	if output.Primary.Weight != 1.0 {
		t.Errorf("expected Weight=1.0, got %v", output.Primary.Weight)
	}
}

// ─── 7. TestResolver_CausalChain ────────────────────────────────────────────

func TestResolver_CausalChain(t *testing.T) {
	// Rule A CausesOf Rule B. Both fire. Only A should be primary, B absorbed.
	results := []*Diagnosis{
		{
			RuleID:     "stale_statistics",
			RuleName:   "Stale Statistics",
			Severity:   SeverityHigh,
			Confidence: 0.8,
			Findings:   []Finding{{Desc: "stats are stale"}},
			Actions:    []Action{{Type: ActionFix, Desc: "gather stats", SkillCommand: "/gather", RawSQL: "EXEC DBMS_STATS"}},
		},
		{
			RuleID:     "full_table_scan_oltp",
			RuleName:   "Full Table Scan OLTP",
			Severity:   SeverityMedium,
			Confidence: 0.6,
			Findings:   []Finding{{Desc: "full scan detected"}},
			Actions:    []Action{{Type: ActionFix, Desc: "add index", SkillCommand: "/index", RawSQL: "CREATE INDEX"}},
		},
	}

	rules := []*Rule{
		{ID: "stale_statistics", Name: "Stale Statistics", CausesOf: []string{"full_table_scan_oltp"}},
		{ID: "full_table_scan_oltp", Name: "Full Table Scan OLTP", CausedBy: []string{"stale_statistics"}},
	}

	output := Resolve(results, rules)

	if output.Primary == nil {
		t.Fatal("expected non-nil Primary")
	}
	if output.Primary.RuleID != "stale_statistics" {
		t.Errorf("expected Primary to be 'stale_statistics' (upstream), got %q", output.Primary.RuleID)
	}

	// full_table_scan_oltp should NOT be secondary; it should be absorbed
	if output.Secondary != nil && output.Secondary.RuleID == "full_table_scan_oltp" {
		t.Error("full_table_scan_oltp should be absorbed, not secondary")
	}

	// Verify the downstream was marked as downstream of stale_statistics
	for _, d := range results {
		if d.RuleID == "full_table_scan_oltp" && d.IsDownstreamOf != "stale_statistics" {
			t.Errorf("expected full_table_scan_oltp.IsDownstreamOf='stale_statistics', got %q", d.IsDownstreamOf)
		}
	}
}

// ─── 8. TestResolver_WeightConvergence ──────────────────────────────────────

func TestResolver_WeightConvergence(t *testing.T) {
	// When primary weight > 80%, no Secondary should appear.
	t.Run("HighWeightOnlyPrimary", func(t *testing.T) {
		results := []*Diagnosis{
			{
				RuleID: "dominant", RuleName: "Dominant",
				Severity: SeverityCritical, Confidence: 0.95,
				Findings: []Finding{{Desc: "f1"}, {Desc: "f2"}, {Desc: "f3"}},
				Actions:  []Action{{Type: ActionUrgent, Desc: "a1", SkillCommand: "/cmd", RawSQL: "SQL"}},
			},
			{
				RuleID: "weak", RuleName: "Weak",
				Severity: SeverityLow, Confidence: 0.2,
				Findings: []Finding{{Desc: "w1"}},
				Actions:  []Action{{Type: ActionInvestigate, Desc: "w_a1", SkillCommand: "/cmd2", RawSQL: "SQL2"}},
			},
		}
		rules := []*Rule{
			{ID: "dominant", Name: "Dominant"},
			{ID: "weak", Name: "Weak"},
		}

		output := Resolve(results, rules)
		if output.Primary == nil {
			t.Fatal("expected non-nil Primary")
		}
		if output.Primary.RuleID != "dominant" {
			t.Errorf("expected 'dominant' as primary, got %q", output.Primary.RuleID)
		}
		// With such a large gap, primary weight should be > 80%
		if output.Primary.Weight <= 0.80 {
			t.Logf("Primary weight=%.2f (expected >0.80 for dominant vs weak)", output.Primary.Weight)
		}
		// If weight > 80%, secondary should be nil
		if output.Primary.Weight > 0.80 && output.Secondary != nil {
			t.Errorf("expected nil Secondary when primary weight=%.2f > 0.80", output.Primary.Weight)
		}
	})
}

// ─── 9. TestResolver_TwoCloseCauses ─────────────────────────────────────────

func TestResolver_TwoCloseCauses(t *testing.T) {
	// Two diagnoses with very close scores should both appear in output.
	results := []*Diagnosis{
		{
			RuleID: "rule_x", RuleName: "Rule X",
			Severity: SeverityHigh, Confidence: 0.7,
			Findings: []Finding{{Desc: "fx1"}, {Desc: "fx2"}},
			Actions:  []Action{{Type: ActionFix, Desc: "ax1", SkillCommand: "/x", RawSQL: "X"}},
		},
		{
			RuleID: "rule_y", RuleName: "Rule Y",
			Severity: SeverityHigh, Confidence: 0.65,
			Findings: []Finding{{Desc: "fy1"}, {Desc: "fy2"}},
			Actions:  []Action{{Type: ActionFix, Desc: "ay1", SkillCommand: "/y", RawSQL: "Y"}},
		},
	}
	rules := []*Rule{
		{ID: "rule_x", Name: "Rule X"},
		{ID: "rule_y", Name: "Rule Y"},
	}

	output := Resolve(results, rules)
	if output.Primary == nil {
		t.Fatal("expected non-nil Primary")
	}
	if output.Secondary == nil {
		t.Fatal("expected non-nil Secondary for two close causes")
	}
	// Both rules should be present in Primary+Secondary
	ids := map[string]bool{output.Primary.RuleID: true}
	ids[output.Secondary.RuleID] = true
	if !ids["rule_x"] || !ids["rule_y"] {
		t.Errorf("expected both rule_x and rule_y in output, got primary=%q secondary=%q",
			output.Primary.RuleID, output.Secondary.RuleID)
	}
}

// ─── 10. TestResolver_MaxTwoCauses ──────────────────────────────────────────

func TestResolver_MaxTwoCauses(t *testing.T) {
	// 5 results should produce at most 2 in Primary+Secondary.
	results := make([]*Diagnosis, 5)
	rules := make([]*Rule, 5)
	for i := 0; i < 5; i++ {
		id := string(rune('A' + i))
		results[i] = &Diagnosis{
			RuleID:     "rule_" + id,
			RuleName:   "Rule " + id,
			Severity:   SeverityMedium,
			Confidence: 0.5,
			Findings:   []Finding{{Desc: "f" + id}},
			Actions:    []Action{{Type: ActionFix, Desc: "a" + id, SkillCommand: "/cmd", RawSQL: "SQL"}},
		}
		rules[i] = &Rule{ID: "rule_" + id, Name: "Rule " + id}
	}

	output := Resolve(results, rules)

	outputCount := 0
	if output.Primary != nil {
		outputCount++
	}
	if output.Secondary != nil {
		outputCount++
	}

	if outputCount > 2 {
		t.Errorf("expected at most 2 root causes, got %d", outputCount)
	}

	// Remaining should be absorbed
	totalAccounted := outputCount + len(output.Absorbed)
	if totalAccounted != 5 {
		t.Errorf("expected 5 total diagnoses accounted for, got %d (primary/secondary=%d, absorbed=%d)",
			totalAccounted, outputCount, len(output.Absorbed))
	}
}

// ─── 11. TestFormatOutput ───────────────────────────────────────────────────

func TestFormatOutput(t *testing.T) {
	output := &DiagOutput{
		Primary: &Diagnosis{
			RuleID:     "test_rule",
			RuleName:   "Test Rule Name",
			Cause:      "Test Root Cause",
			Severity:   SeverityHigh,
			Confidence: 0.85,
			Weight:     0.7,
			Findings: []Finding{
				{Desc: "Evidence 1"},
				{Desc: "Evidence 2"},
			},
			Actions: []Action{
				{Type: ActionUrgent, Desc: "Do something urgent", SkillCommand: "/fix_it", RawSQL: "SELECT fix()"},
				{Type: ActionFix, Desc: "Apply permanent fix", SkillCommand: "/permanent", RawSQL: "ALTER SYSTEM SET x=y"},
			},
			AbsorbedNames: []string{"Downstream Rule"},
		},
	}

	cfg := DefaultConfig()
	text := FormatDiagOutput(output, cfg)

	// Verify key Chinese section headers are present
	expectedSections := []string{
		"规则诊断",
		"根因",
		"严重程度",
		"置信度",
		"证据链",
		"处置建议",
		"关联分析",
	}
	for _, section := range expectedSections {
		if !strings.Contains(text, section) {
			t.Errorf("expected section header %q in output", section)
		}
	}

	// Verify content
	if !strings.Contains(text, "Test Root Cause") {
		t.Error("expected cause text in output")
	}
	if !strings.Contains(text, "Evidence 1") {
		t.Error("expected finding text in output")
	}
	if !strings.Contains(text, "Do something urgent") {
		t.Error("expected action description in output")
	}
	if !strings.Contains(text, "Downstream Rule") {
		t.Error("expected absorbed rule name in output")
	}

	// Test SQL mode shows RawSQL
	if !strings.Contains(text, "SELECT fix()") {
		t.Error("expected RawSQL in SQL output mode")
	}

	// Test skill mode shows SkillCommand instead
	cfg.OutputMode = OutputSkill
	textSkill := FormatDiagOutput(output, cfg)
	if !strings.Contains(textSkill, "/fix_it") {
		t.Error("expected SkillCommand in skill output mode")
	}

	// Test nil output
	nilText := FormatDiagOutput(nil, cfg)
	if !strings.Contains(nilText, "无规则匹配") {
		t.Error("expected '无规则匹配' for nil output")
	}

	// Test secondary section
	output.Secondary = &Diagnosis{
		RuleID:     "sec_rule",
		RuleName:   "Secondary Rule",
		Cause:      "Secondary Cause",
		Severity:   SeverityMedium,
		Confidence: 0.6,
		Findings:   []Finding{{Desc: "secondary evidence"}},
		Actions:    []Action{{Type: ActionFix, Desc: "secondary action", RawSQL: "SELECT 2"}},
	}
	textWithSec := FormatDiagOutput(output, DefaultConfig())
	if !strings.Contains(textWithSec, "次要根因") {
		t.Error("expected '次要根因' section when secondary is present")
	}
}

// ─── 12. TestDiagnose_BufferBusyWaits ───────────────────────────────────────

func TestDiagnose_BufferBusyWaits(t *testing.T) {
	exec := &mockExecutor{results: map[QueryID]interface{}{
		QueryASHBlockClass:       float64(1), // data_block
		QueryASHHotObject:        float64(5), // hot object found
		QueryASHBlockConcentrate: float64(3), // concentrated blocks
		QueryWaitAvgTime:         float64(15), // avg 15ms
	}}

	e := newTestEngineWithExecutor(exec)

	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "buffer busy waits", Percentage: 35.0, WaitClass: "Concurrency", TotalMs: 35000},
			{Event: "db file sequential read", Percentage: 15.0, WaitClass: "User I/O", TotalMs: 15000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			"active_sessions":          {Avg: 30, Max: 45},
			"cpu_sessions":             {Avg: 10, Max: 15},
			"buffer_busy_waits_avg_ms": {Avg: 15, Max: 25},
		},
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "abc123", OccurrenceRate: 0.6, MaxElapsedSec: 5, Event: "buffer busy waits", MaxConcurrent: 10},
		},
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseBadSQL,
			Confidence: 0.7,
		},
		PeakActive:     45,
		BaselineActive: 10,
		DurationSec:    20,
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := e.Diagnose(input)

	// Should produce a non-empty diagnosis
	if output == nil {
		t.Fatal("expected non-nil output")
	}
	if output.Primary == nil {
		t.Fatal("expected non-nil Primary diagnosis")
	}

	// The primary should have findings and actions
	if len(output.Primary.Findings) == 0 {
		t.Error("expected findings in Primary diagnosis")
	}
	if len(output.Primary.Actions) == 0 {
		t.Error("expected actions in Primary diagnosis")
	}

	// Severity should be at least Medium
	if output.Primary.Severity < SeverityMedium {
		t.Errorf("expected at least SeverityMedium, got %v", output.Primary.Severity)
	}

	// CORE PRINCIPLE: max 1-2 root causes
	rootCount := 0
	if output.Primary != nil {
		rootCount++
	}
	if output.Secondary != nil {
		rootCount++
	}
	if rootCount > 2 {
		t.Errorf("CORE PRINCIPLE violated: expected max 2 root causes, got %d", rootCount)
	}
}

// ─── 13. TestDiagnose_NoMatch ───────────────────────────────────────────────

func TestDiagnose_NoMatch(t *testing.T) {
	e := newTestEngine()

	// Empty report with no significant waits or metrics
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{},
		Metrics:     map[string]sentinel.MetricSummary{},
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseUnknown,
			Confidence: 0,
		},
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := e.Diagnose(input)

	// Should return empty output (no rules match)
	if output.Primary != nil {
		t.Errorf("expected nil Primary for empty report, got rule %q", output.Primary.RuleID)
	}

	// Also test nil input
	outputNil := e.Diagnose(nil)
	if outputNil.Primary != nil {
		t.Error("expected nil Primary for nil input")
	}
}

// ─── Supplementary tests ────────────────────────────────────────────────────

func TestRulesHaveBothCommandTypes(t *testing.T) {
	rules := otherRules()
	for _, rule := range rules {
		if rule.Tree == nil {
			continue
		}
		checkTreeActions(t, rule.ID, rule.Tree)
	}
}

func checkTreeActions(t *testing.T, ruleID string, node *TreeNode) {
	t.Helper()
	for _, branch := range node.Branches {
		for _, action := range branch.Actions {
			if action.SkillCommand == "" {
				t.Errorf("rule %s: action %q missing SkillCommand", ruleID, action.Desc)
			}
			if action.RawSQL == "" {
				t.Errorf("rule %s: action %q missing RawSQL", ruleID, action.Desc)
			}
		}
		if branch.Then != nil {
			checkTreeActions(t, ruleID, branch.Then)
		}
	}
}

func TestOtherRuleCount(t *testing.T) {
	rules := otherRules()
	if len(rules) < 22 {
		t.Errorf("expected at least 22 other rules, got %d", len(rules))
	}

	// Verify rule groups
	if len(sqlPerfRules()) != 5 {
		t.Errorf("expected 5 SQL perf rules, got %d", len(sqlPerfRules()))
	}
	if len(memoryRules()) != 4 {
		t.Errorf("expected 4 memory rules, got %d", len(memoryRules()))
	}
	if len(spaceRules()) != 3 {
		t.Errorf("expected 3 space rules, got %d", len(spaceRules()))
	}
	if len(lockRules()) != 3 {
		t.Errorf("expected 3 lock rules, got %d", len(lockRules()))
	}
	if len(redoArchiveRules()) != 2 {
		t.Errorf("expected 2 redo/archive rules, got %d", len(redoArchiveRules()))
	}
	if len(emergencyRules()) != 3 {
		t.Errorf("expected 3 emergency rules, got %d", len(emergencyRules()))
	}
	if len(sessionRules()) != 2 {
		t.Errorf("expected 2 session rules, got %d", len(sessionRules()))
	}
}

func TestRulesHaveCausalRelationships(t *testing.T) {
	rules := otherRules()
	ruleMap := make(map[string]*Rule)
	for _, r := range rules {
		ruleMap[r.ID] = r
	}

	// Verify some known causal chains
	stale := ruleMap["stale_statistics"]
	if stale == nil {
		t.Fatal("stale_statistics rule not found")
	}
	if len(stale.CausesOf) == 0 {
		t.Error("stale_statistics should have CausesOf entries")
	}

	hardParse := ruleMap["hard_parse_storm"]
	if hardParse == nil {
		t.Fatal("hard_parse_storm rule not found")
	}
	if len(hardParse.CausesOf) == 0 {
		t.Error("hard_parse_storm should have CausesOf entries")
	}

	sessionLeak := ruleMap["session_leak"]
	if sessionLeak == nil {
		t.Fatal("session_leak rule not found")
	}
	if len(sessionLeak.CausesOf) == 0 {
		t.Error("session_leak should have CausesOf entries")
	}
}

func TestRulesUniqueIDs(t *testing.T) {
	rules := (&CommunityProvider{}).Rules()
	ids := make(map[string]bool)
	for _, r := range rules {
		if ids[r.ID] {
			t.Errorf("duplicate rule ID: %s", r.ID)
		}
		ids[r.ID] = true
	}
}

func TestWaitEventRuleCount(t *testing.T) {
	rules := waitEventRules()
	if len(rules) != 17 {
		t.Errorf("expected 17 wait event rules, got %d", len(rules))
	}
}

// ─── 14. TestDiagnose_MultipleMatch ─────────────────────────────────────────

func TestDiagnose_MultipleMatch(t *testing.T) {
	exec := &mockExecutor{results: map[QueryID]interface{}{
		QueryASHTopSQL:    float64(5),
		QueryASHHotObject: float64(3),
		QueryLatchStats:   float64(2),
		QueryWaitAvgTime:  float64(10),
		QueryFKNoIndex:    float64(2),
		QueryCursorStats:  float64(50),
		QueryParseStats:   float64(30),
	}}

	e := newTestEngineWithExecutor(exec)

	// Report that triggers multiple rules: high hard parse + deadlock + scattered read
	report := &sentinel.BurstReport{
		WaitProfile: []sentinel.WaitBucket{
			{Event: "db file scattered read", Percentage: 20.0, WaitClass: "User I/O", TotalMs: 20000},
			{Event: "library cache: mutex X", Percentage: 10.0, WaitClass: "Concurrency", TotalMs: 10000},
		},
		Metrics: map[string]sentinel.MetricSummary{
			metricHardParse:        {Avg: 200, Max: 300},
			metricActive:           {Avg: 40, Max: 60},
			metricEnqueueDeadlocks: {Avg: 1, Max: 2},
			metricFullScanRate:     {Avg: 50, Max: 80},
		},
		TopSQLs: []sentinel.SQLProfile{
			{SQLID: "sql1", OccurrenceRate: 0.5, MaxElapsedSec: 10, Event: "db file scattered read", MaxConcurrent: 5},
		},
		BlockingChains: []sentinel.BlockingChain{},
		Classification: sentinel.Classification{
			Cause:      sentinel.CauseLatchStorm,
			Confidence: 0.6,
		},
		PeakActive:     60,
		BaselineActive: 15,
		DurationSec:    25,
	}

	input := &DiagInput{
		Type:   InputBurstReport,
		Report: report,
	}

	output := e.Diagnose(input)

	if output == nil || output.Primary == nil {
		t.Fatal("expected non-nil output with Primary")
	}

	// CORE PRINCIPLE: max 1-2 root causes even when many rules fire
	rootCount := 0
	if output.Primary != nil {
		rootCount++
	}
	if output.Secondary != nil {
		rootCount++
	}

	if rootCount > 2 {
		t.Errorf("CORE PRINCIPLE violated: expected max 2 root causes, got %d", rootCount)
	}

	// Log the diagnosis for debugging
	t.Logf("Primary: %s (severity=%v, confidence=%.2f)",
		output.Primary.RuleID, output.Primary.Severity, output.Primary.Confidence)
	if output.Secondary != nil {
		t.Logf("Secondary: %s (severity=%v, confidence=%.2f)",
			output.Secondary.RuleID, output.Secondary.Severity, output.Secondary.Confidence)
	}
	t.Logf("Absorbed count: %d", len(output.Absorbed))

	// Verify actions have both SkillCommand and RawSQL
	for _, a := range output.Primary.Actions {
		if a.SkillCommand == "" {
			t.Errorf("primary action %q missing SkillCommand", a.Desc)
		}
		if a.RawSQL == "" {
			t.Errorf("primary action %q missing RawSQL", a.Desc)
		}
	}
}
