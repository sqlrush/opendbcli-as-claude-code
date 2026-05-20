/*-------------------------------------------------------------------------
 *
 * orchestrator.go
 *	  GenericTuner is the neutral LLM-orchestrated TunerEngine used by
 *	  all non-og dialects (mysql / pg / oracle / gaussdb). og keeps
 *	  its existing complex Tuner (with memory / token compression /
 *	  auto-upgrade features) in opengauss/sqltuner/tuner.go — that's
 *	  a 600-line beast worth keeping separate until M7's design has
 *	  proven itself across the 4 dialects that adopt it first.
 *
 *	  Flow (3 phases, ~30-90s wall time for typical query):
 *	    Phase A: parallel collect via DialectPlanner
 *	      ├ ExplainPlan + (optional) ExplainPerformance trace
 *	      ├ CollectSchema (tables + stats + indexes)
 *	      ├ SnapshotDialect (version + CBO GUC)
 *	      ├ SnapshotRuntime (waits + locks on involved tables)
 *	      ├ CollectTrace (M5 Oracle 10053 / M4b GaussDB GS_PLAN_TRACE)
 *	      └ NormalizePlaceholders (fast-fail on $N/?/:1)
 *	    Round 1: LLM mega-analysis
 *	      ├ Build system prompt via PromptBuilder + universal sections
 *	      ├ Build user message with collected context
 *	      ├ LLMCaller.Chat → strict JSON parsing
 *	      └ Round1Output struct (confidence + candidates + cbo_analysis)
 *	    Verify + Render:
 *	      ├ For each rewrite/hint candidate: QuickPlanCost compare
 *	      ├ For rewrite candidates: VerifyEquivalence (optional capability)
 *	      └ Deterministic markdown render (no Round 2 LLM yet — Round 2
 *	        polish is M7.x follow-up; raw verified candidates are
 *	        already DBA-usable).
 *
 *	  This is intentionally simpler than og's existing tuner:
 *	    NOT included: memory injection, token compression, deep-mode
 *	    auto-upgrade, multi-round agent loop. Those features can land
 *	    incrementally as the design proves out on the 4 new dialects.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/orchestrator.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// GenericTuner implements TunerEngine using DialectPlanner + PromptBuilder
// + LLMCaller. Pure neutral — no dialect imports.
type GenericTuner struct {
	planner  DialectPlanner
	builder  PromptBuilder
	llm      LLMCaller // may be nil → tuner falls back to deterministic render
}

// NewGenericTuner constructs the neutral Tuner. llm may be nil; when
// nil, Tune() skips Round 1 / Round 2 and renders Phase A only (still
// useful — gives DBA the raw EXPLAIN + trace + schema data).
func NewGenericTuner(planner DialectPlanner, builder PromptBuilder, llm LLMCaller) *GenericTuner {
	return &GenericTuner{planner: planner, builder: builder, llm: llm}
}

// Tune is the TunerEngine entry point — Phase A always; Round 1 + verify
// + render only if LLM is configured.
func (t *GenericTuner) Tune(ctx context.Context, opts TuneOptions) (*FinalReport, error) {
	start := time.Now()
	if strings.TrimSpace(opts.SQL) == "" {
		return nil, fmt.Errorf("empty SQL")
	}

	cc, err := t.collectPhaseA(ctx, opts)
	if err != nil {
		return nil, err // PlaceholderError propagates as-is
	}

	stats := &ReportStats{Rounds: 1}

	// G7 token compression (M8.2): mutates cc in-place for huge SQL /
	// plan / schema. No-op on small queries. Stats flow into ReportStats
	// so the final markdown surfaces the action to the user.
	compStats := Compress(cc)
	if compStats.TriggerReason != "" {
		stats.CompressionTriggered = true
		stats.CompressionReason = compStats.TriggerReason
	}

	// LLM-less fallback: render Phase A as raw markdown.
	if t.llm == nil {
		stats.TotalDuration = time.Since(start)
		return &FinalReport{
			Markdown: renderRawPhaseA(cc, t.builder, stats, "LLM 未配置, 仅输出 Phase A 原始数据"),
			Stats:    stats,
		}, nil
	}

	// Round 1: mega-analysis.
	round1, err := t.runRound1(ctx, cc)
	if err != nil {
		// Soft fail: render Phase A + the LLM error so user knows.
		stats.TotalDuration = time.Since(start)
		return &FinalReport{
			Markdown: renderRawPhaseA(cc, t.builder, stats, "Round 1 LLM 调用失败: "+err.Error()),
			Stats:    stats,
		}, nil
	}
	stats.CandidateCount = len(round1.Candidates)
	stats.Rounds = 2

	// Verify each candidate.
	verifies := t.verifyCandidates(ctx, cc.OrigSQL, round1.Candidates, opts.Verify)
	for _, vr := range verifies {
		if vr.Verifiable && vr.Error == "" {
			stats.VerifiedCount++
			if vr.Speedup > stats.BestSpeedup {
				stats.BestSpeedup = vr.Speedup
			}
		}
	}

	stats.TotalDuration = time.Since(start)
	return &FinalReport{
		Markdown: renderFinalReport(cc, t.builder, round1, verifies, stats),
		Stats:    stats,
	}, nil
}

// ── Phase A: parallel data collection ──────────────────────────────────

func (t *GenericTuner) collectPhaseA(ctx context.Context, opts TuneOptions) (*CollectedContext, error) {
	// Normalize first — short-circuit on placeholder errors (PlaceholderError
	// is the only error we want to bubble unwrapped to the skill layer).
	normSQL, err := t.planner.NormalizePlaceholders(ctx, opts.SQL)
	if err != nil {
		return nil, err
	}

	cc := &CollectedContext{OrigSQL: normSQL}
	var wg sync.WaitGroup
	var mu sync.Mutex
	addNote := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		cc.Notes = append(cc.Notes, s)
	}

	explainOpts := ExplainOptions{Analyze: AnalyzeAuto, Buffers: true, FormatJSON: true}
	switch {
	case opts.ForceAnalyze:
		explainOpts.Analyze = AnalyzeForce
	case opts.NoAnalyze:
		explainOpts.Analyze = AnalyzeSkip
	}

	// Optional: trace via PerformancePlanner (og/GaussDB EXPLAIN PERFORMANCE).
	if pp, ok := t.planner.(PerformancePlanner); ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			td, err := pp.ExplainPerformance(ctx, normSQL)
			if err != nil {
				addNote("explain_performance: " + err.Error())
				return
			}
			if td != nil {
				mu.Lock()
				cc.Trace = td
				mu.Unlock()
			}
		}()
	}

	// Optional: dialect-level CBO trace (Oracle 10053 / MySQL optimizer_trace /
	// GaussDB GS_PLAN_TRACE). EnableTrace + capture sequence is strict:
	// open trace → ExplainPlan (which forces parse) → CollectTrace.
	// We run this on the same goroutine as ExplainPlan to keep ordering.
	wg.Add(1)
	go func() {
		defer wg.Done()
		closeTrace, initial, _ := t.planner.EnableTrace(ctx, "opendb_sqltune")
		defer func() {
			if closeTrace != nil {
				_ = closeTrace()
			}
		}()

		plan, err := t.planner.ExplainPlan(ctx, normSQL, explainOpts)
		mu.Lock()
		cc.Plan = plan
		mu.Unlock()
		if err != nil {
			addNote("explain_plan: " + err.Error())
		}

		// Only call CollectTrace if EnableTrace claimed availability —
		// avoids overwriting a PerformancePlanner-provided trace with
		// a "not available" placeholder.
		if initial != nil && initial.Available {
			if td, err := t.planner.CollectTrace(ctx, "opendb_sqltune"); err == nil && td != nil {
				mu.Lock()
				if cc.Trace == nil || !cc.Trace.Available {
					cc.Trace = td
				}
				mu.Unlock()
			}
		} else if initial != nil && cc.Trace == nil {
			// No trace, but surface the explanatory note.
			mu.Lock()
			cc.Trace = initial
			mu.Unlock()
		}
	}()

	// Schema
	wg.Add(1)
	go func() {
		defer wg.Done()
		schema, tables, err := t.planner.CollectSchema(ctx, normSQL)
		mu.Lock()
		cc.Schema = schema
		cc.InvolvedTables = tables
		mu.Unlock()
		if err != nil {
			addNote("schema: " + err.Error())
		}
	}()

	// Dialect snapshot
	wg.Add(1)
	go func() {
		defer wg.Done()
		dial, err := t.planner.SnapshotDialect(ctx)
		mu.Lock()
		cc.Dialect = dial
		mu.Unlock()
		if err != nil {
			addNote("dialect_snapshot: " + err.Error())
		}
	}()

	wg.Wait()

	// Runtime depends on InvolvedTables — run after schema.
	if rt, err := t.planner.SnapshotRuntime(ctx, cc.InvolvedTables); err == nil {
		cc.Runtime = rt
	} else {
		addNote("runtime_snapshot: " + err.Error())
	}

	if cc.Plan == nil {
		return nil, fmt.Errorf("plan collection failed; cannot continue")
	}
	return cc, nil
}

// ── Round 1: LLM mega-analysis ─────────────────────────────────────────

const round1Timeout = 120 * time.Second // generous for slow models

func (t *GenericTuner) runRound1(ctx context.Context, cc *CollectedContext) (*Round1Output, error) {
	cctx, cancel := context.WithTimeout(ctx, round1Timeout)
	defer cancel()

	systemPrompt := assembleSystemPrompt(t.builder, cc.Dialect)
	userMsg := assembleUserMessage(cc)

	reply, err := t.llm.Chat(cctx, []ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userMsg},
	})
	if err != nil {
		return nil, fmt.Errorf("llm chat: %w", err)
	}

	// Strict JSON parse. Try direct first, then strip markdown code fence
	// (some models wrap JSON in ```json ``` despite the prompt's request).
	out, err := parseRound1JSON(reply)
	if err != nil {
		return nil, fmt.Errorf("parse round1 JSON: %w (raw=%q)", err, truncate(reply, 200))
	}
	return out, nil
}

func parseRound1JSON(s string) (*Round1Output, error) {
	trimmed := strings.TrimSpace(s)
	// Strip markdown fence if present.
	if strings.HasPrefix(trimmed, "```") {
		// Drop first fence line.
		if idx := strings.Index(trimmed, "\n"); idx > 0 {
			trimmed = trimmed[idx+1:]
		}
		// Drop trailing fence.
		if end := strings.LastIndex(trimmed, "```"); end > 0 {
			trimmed = trimmed[:end]
		}
		trimmed = strings.TrimSpace(trimmed)
	}

	var out Round1Output
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ── Verify ─────────────────────────────────────────────────────────────

const verifyTimeout = 30 * time.Second

func (t *GenericTuner) verifyCandidates(ctx context.Context, origSQL string, cands []Candidate, doEquiv bool) []VerifyResult {
	results := make([]VerifyResult, len(cands))

	// Get original cost once for speedup math.
	origCost, _ := t.planner.QuickPlanCost(ctx, origSQL)

	// Type-assert optional EquivVerifier (M6 interface).
	equivVerifier, _ := t.planner.(EquivVerifier)

	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, cand := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c Candidate) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, verifyTimeout)
			defer cancel()
			results[i] = verifyOne(cctx, t.planner, equivVerifier, origSQL, origCost, c, doEquiv)
		}(i, cand)
	}
	wg.Wait()
	return results
}

// verifyOne handles one candidate. Free function (not method) so unit
// tests can pass a mock planner without constructing a full Tuner.
func verifyOne(ctx context.Context, planner DialectPlanner, equiv EquivVerifier, origSQL string, origCost float64, c Candidate, doEquiv bool) VerifyResult {
	r := VerifyResult{CandID: c.ID, OldCost: origCost}
	switch c.Type {
	case "rewrite", "hint":
		r.Verifiable = true
		newCost, err := planner.QuickPlanCost(ctx, c.SQL)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.NewCost = newCost
		if newCost > 0 && origCost > 0 {
			r.Speedup = origCost / newCost
		}
		if doEquiv && c.Type == "rewrite" && equiv != nil {
			ok, err := equiv.VerifyEquivalence(ctx, origSQL, c.SQL, 1000)
			if err == nil {
				r.EquivOK = &ok
			}
		}
	case "index", "schema", "stats":
		r.Verifiable = false
		r.Note = "DDL 类方案未实跑 EXPLAIN（DDL 没改），收益基于 LLM 推理"
	default:
		r.Verifiable = false
		r.Note = "未知 type=" + c.Type + "，跳过验证"
	}
	return r
}

// ── Helpers ────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
