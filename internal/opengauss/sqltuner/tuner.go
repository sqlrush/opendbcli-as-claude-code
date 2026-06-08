/*-------------------------------------------------------------------------
 *
 * tuner.go
 *	  Tuner is the main /sqltune entry point.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/tuner.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// Tuner is the main /sqltune entry point.
//
// Flow (design doc §5):
//
//	Phase A: 确定性收集（plan + schema + view + dialect + runtime + memory），全部并行
//	Round 1: LLM mega-analysis → JSON Round1Output
//	Round 2 verify: 并发跑 EXPLAIN 验证每个 candidate cost + 等价性验证 rewrite 类
//	Round 2 report: LLM 写最终 markdown 报告
type Tuner struct {
	driver   db.Driver
	provider llm.Provider
	memStore *memory.Store

	// planner is the DialectPlanner driving Phase A data collection.
	// M1.4: previously 5 separate *Collector fields, now one interface
	// to let MySQL/PG/Oracle/GaussDB plug in their own implementations.
	planner sqltune.DialectPlanner

	memQuery *MemoryQuery // memory access not in DialectPlanner (cross-cutting)
	// M6.5 deleted: verifier *EquivVerifier — now uses sqltune.EquivVerifier
	// type-assertion on t.planner.
}

// NewTuner constructs a Tuner with all sub-modules wired. Driver is
// assumed to be openGauss/GaussDB — for other dialects use
// NewTunerFromPlanner. memStore may be nil — tuner gracefully skips M6.
func NewTuner(driver db.Driver, provider llm.Provider, memStore *memory.Store) *Tuner {
	return NewTunerFromPlanner(driver, NewPlanner(driver), provider, memStore)
}

// NewTunerFromPlanner accepts any DialectPlanner. driver is still
// retained for legacy / non-planner queries; M6.5 moved equivalence
// verification onto the planner via sqltune.EquivVerifier optional
// interface, so we no longer need a separate verifier instance.
func NewTunerFromPlanner(driver db.Driver, planner sqltune.DialectPlanner, provider llm.Provider, memStore *memory.Store) *Tuner {
	return &Tuner{
		driver:   driver,
		provider: provider,
		memStore: memStore,
		planner:  planner,
		memQuery: NewMemoryQuery(memStore),
	}
}

// Tune runs the full 2-round flow.
func (t *Tuner) Tune(ctx context.Context, opts TuneOptions) (*FinalReport, error) {
	start := time.Now()
	if opts.SQL == "" {
		return nil, fmt.Errorf("empty SQL")
	}
	if opts.MaxRounds == 0 {
		opts.MaxRounds = 10
	}
	stats := &ReportStats{}

	// ── Phase A: parallel data collection ──
	cc, err := t.collectPhaseA(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("phase A: %w", err)
	}

	// G7 token compression for large SQL/plan/schema (M8.1: delegated
	// to neutral sqltune.Compress so og and GenericTuner share logic).
	compStats := sqltune.Compress(cc)
	if compStats.TriggerReason != "" {
		stats.CompressionTriggered = true
		stats.CompressionReason = compStats.TriggerReason
	}

	// ── Round 1: LLM mega-analysis ──
	round1, err := t.runRound1(ctx, cc)
	if err != nil {
		// Round 1 LLM failure (truncated JSON / connection / timeout) —
		// don't hard-fail. Return a degraded report with Phase A data
		// (plan tree + schema + dialect) + the error note. LLM agent
		// then surfaces something useful to the user instead of treating
		// this as "sqltune failed, try something else" and chasing red
		// herrings (e.g., calling /explain or /sql for a basic version
		// check). Observed in real session: 35B / opus both early-stop
		// on hard error.
		stats.TotalDuration = time.Since(start)
		degradedMD := "# /sqltune 部分结果（Round 1 LLM 失败）\n\n" +
			"> ⚠️ Round 1 综合分析失败: " + err.Error() + "\n" +
			"> 以下为 Phase A 采集到的 EXPLAIN + schema + dialect 原始数据.\n" +
			"> 可作为参考供 DBA 手动分析, 或重试 /sqltune.\n\n" +
			renderFallbackReport(cc, &Round1Output{}, nil)
		return &FinalReport{Markdown: degradedMD, Stats: stats}, nil
	}
	stats.CandidateCount = len(round1.Candidates)
	stats.Rounds = 1

	// ── Verify Round 1 candidates in parallel ──
	verifyResults := t.verifyCandidates(ctx, cc.OrigSQL, round1.Candidates, opts.Verify)
	stats.Rounds++
	for _, vr := range verifyResults {
		if vr.Verifiable && vr.Error == "" {
			stats.VerifiedCount++
			if vr.Speedup > stats.BestSpeedup {
				stats.BestSpeedup = vr.Speedup
			}
		}
	}

	// ── Auto-upgrade: 4-signal check, escalate to deep mode if needed ──
	if !opts.SkipUpgrade {
		upgradeDecision := EvaluateUpgrade(cc, round1, verifyResults)
		if upgradeDecision.ShouldUpgrade {
			stats.UpgradeTriggered = true
			stats.UpgradeReasons = upgradeDecision.Reasons
			deepMaxRounds := opts.MaxRounds - 2 // 已用 2 轮 (Round 1 + verify)
			if deepMaxRounds < 3 {
				deepMaxRounds = 3
			}
			r1Updated, vUpdated, deepRounds, derr := t.runDeepMode(ctx, cc, round1, verifyResults, deepMaxRounds)
			if derr == nil {
				round1 = r1Updated
				verifyResults = vUpdated
				stats.Rounds += deepRounds
				stats.CandidateCount = len(round1.Candidates)
				// Recompute stats
				for _, vr := range verifyResults {
					if vr.Verifiable && vr.Error == "" && vr.Speedup > stats.BestSpeedup {
						stats.BestSpeedup = vr.Speedup
					}
				}
				stats.VerifiedCount = 0
				for _, vr := range verifyResults {
					if vr.Verifiable && vr.Error == "" {
						stats.VerifiedCount++
					}
				}
			}
		}
	}

	// ── Round 2: LLM final markdown report ──
	// v1.1.31: QuickMode skips the Round 2 LLM call entirely (saves 30s-3min on
	// complex SQL) and goes straight to deterministic fallback rendering. The
	// renderFallbackReport output is structurally identical (5 维度 + verify
	// results), it just lacks the LLM's prose polish. For 10+ table SQL this
	// is a strict win — Round 2 was the call that timed out >600s on real
	// production traces.
	var markdown string
	if opts.QuickMode {
		markdown = renderFallbackReport(cc, round1, verifyResults)
	} else {
		var err error
		markdown, err = t.runRound2(ctx, cc, round1, verifyResults)
		if err != nil {
			markdown = renderFallbackReport(cc, round1, verifyResults)
		}
	}

	stats.TotalDuration = time.Since(start)

	// Prepend banner with tuner stats so user sees what triggered (升级/压缩/耗时/方案数).
	// Without this, ReportStats data is invisible to batch-mode users (they only see Rendered).
	markdown = renderStatsBanner(stats) + markdown

	return &FinalReport{
		Markdown: markdown,
		Stats:    stats,
	}, nil
}

// renderStatsBanner produces a 4-6 line block summarizing tuner activity.
// Goes at the top of every report so users can immediately see if 升级/压缩
// triggered, how many rounds ran, and best speedup found.
func renderStatsBanner(s *ReportStats) string {
	var b strings.Builder
	b.WriteString("> **Tuner Stats**: ")
	b.WriteString(fmt.Sprintf("耗时 %s · %d 轮 LLM · %d candidates · %d verified · best %.1f×",
		s.TotalDuration.Round(time.Second).String(),
		s.Rounds, s.CandidateCount, s.VerifiedCount, s.BestSpeedup))
	if s.UpgradeTriggered {
		b.WriteString("\n> **Auto-upgrade triggered**: ")
		b.WriteString(strings.Join(s.UpgradeReasons, "; "))
	}
	if s.CompressionTriggered {
		b.WriteString("\n> **G7 token 压缩已触发**: " + s.CompressionReason)
	}
	b.WriteString("\n\n---\n\n")
	return b.String()
}

// collectPhaseA runs all 6 collectors in parallel.
func (t *Tuner) collectPhaseA(ctx context.Context, opts TuneOptions) (*CollectedContext, error) {
	cc := &CollectedContext{OrigSQL: opts.SQL}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var planErr error // captured separately so caller can return it as a typed error
	addNote := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		cc.Notes = append(cc.Notes, s)
	}

	// Map TuneOptions → ExplainOptions tri-state for planner.ExplainPlan.
	explainOpts := sqltune.ExplainOptions{Analyze: sqltune.AnalyzeAuto, Buffers: true, FormatJSON: true}
	switch {
	case opts.ForceAnalyze:
		explainOpts.Analyze = sqltune.AnalyzeForce
	case opts.NoAnalyze:
		explainOpts.Analyze = sqltune.AnalyzeSkip
	}

	// 0. EXPLAIN PERFORMANCE (M4a) — optional capability.
	// Type-assert to PerformancePlanner: og + GaussDB implement it,
	// others fall through silently. Runs in parallel with JSON EXPLAIN
	// since it's a separate query (and may be skipped for DML).
	if pp, ok := t.planner.(sqltune.PerformancePlanner); ok && !opts.Simple {
		wg.Add(1)
		go func() {
			defer wg.Done()
			td, err := pp.ExplainPerformance(ctx, opts.SQL)
			if err != nil {
				addNote("explain_performance 警告: " + err.Error())
				return
			}
			if td != nil {
				mu.Lock()
				cc.Trace = td
				mu.Unlock()
			}
		}()
	}

	// 1. Plan
	wg.Add(1)
	go func() {
		defer wg.Done()
		plan, err := t.planner.ExplainPlan(ctx, opts.SQL, explainOpts)
		mu.Lock()
		cc.Plan = plan
		if err != nil {
			planErr = err
		}
		mu.Unlock()
		if err != nil {
			addNote("plan_collect 警告: " + err.Error())
		}
	}()

	// 2. Schema (depends on parsed table names; runs same as plan)
	wg.Add(1)
	go func() {
		defer wg.Done()
		schema, tables, err := t.planner.CollectSchema(ctx, opts.SQL)
		mu.Lock()
		cc.Schema = schema
		cc.InvolvedTables = tables
		mu.Unlock()
		if err != nil {
			addNote("schema_collect 警告: " + err.Error())
		}
	}()

	// 3. Dialect
	wg.Add(1)
	go func() {
		defer wg.Done()
		dialect, err := t.planner.SnapshotDialect(ctx)
		mu.Lock()
		cc.Dialect = dialect
		mu.Unlock()
		if err != nil {
			addNote("dialect_snapshot 警告: " + err.Error())
		}
	}()

	// 4. View expansion (depends on SQL only)
	if !opts.Simple {
		wg.Add(1)
		go func() {
			defer wg.Done()
			expanded, err := t.planner.ExpandViews(ctx, opts.SQL)
			if err == nil && expanded != "" && expanded != opts.SQL {
				mu.Lock()
				cc.ExpandedSQL = expanded
				cc.Notes = append(cc.Notes, "view expansion applied")
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	// 5+6. Runtime + Memory (need InvolvedTables which depends on schema; run after wg)
	if !opts.Simple {
		var wg2 sync.WaitGroup
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			rt, _ := t.planner.SnapshotRuntime(ctx, cc.InvolvedTables)
			mu.Lock()
			cc.Runtime = rt
			mu.Unlock()
		}()
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			// v1.1.30: pass SQL so memory query can fingerprint-match (prevents
			// recall of unrelated SQL diagnoses with overlapping table names).
			entries := t.memQuery.FindRelevant(opts.SQL, cc.InvolvedTables, 5)
			mu.Lock()
			cc.Memory = entries
			mu.Unlock()
		}()
		wg2.Wait()
	}

	if cc.Plan == nil {
		// If the plan collector returned a typed error (e.g. PlaceholderSQLError),
		// propagate it so the caller / skill can produce a targeted error message.
		// Otherwise fall back to the generic "plan collection failed" string.
		if planErr != nil {
			return nil, planErr
		}
		return nil, fmt.Errorf("plan collection failed; cannot continue")
	}
	return cc, nil
}

// runRound1 sends one mega-prompt to LLM, expects JSON response.
func (t *Tuner) runRound1(ctx context.Context, cc *CollectedContext) (*Round1Output, error) {
	if t.provider == nil {
		return nil, fmt.Errorf("LLM provider not configured")
	}

	systemPrompt := BuildSystemPrompt(cc.Dialect)
	userMsg := BuildUserMessage(cc)

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		MaxTokens: 8000,
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	resp, err := t.provider.Chat(cctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Content == "" {
		return nil, fmt.Errorf("empty LLM response")
	}

	out, err := ParseRound1Output(resp.Content)
	if err != nil {
		// Retry once with stricter instruction
		retry := req
		retry.Messages = append(retry.Messages,
			llm.Message{Role: "assistant", Content: resp.Content},
			llm.Message{Role: "user", Content: "你的输出不是合法 JSON。请重新输出，纯 JSON，不要任何前后缀文字，不要 markdown 代码块。"},
		)
		resp2, err2 := t.provider.Chat(cctx, retry)
		if err2 != nil {
			return nil, fmt.Errorf("round 1 parse failed and retry failed: %v", err2)
		}
		out, err = ParseRound1Output(resp2.Content)
		if err != nil {
			return nil, fmt.Errorf("round 1 parse failed twice: %w", err)
		}
	}
	return out, nil
}

// verifyCandidates runs EXPLAIN on each candidate (concurrent, ≤5 parallel) + equiv check.
func (t *Tuner) verifyCandidates(ctx context.Context, origSQL string, cands []Candidate, doEquiv bool) []VerifyResult {
	results := make([]VerifyResult, len(cands))
	if len(cands) == 0 {
		return results
	}

	// Get original cost once
	origCost, origErr := t.planner.QuickPlanCost(ctx, origSQL)
	if origErr != nil {
		origCost = 0
	}

	sem := make(chan struct{}, 5)
	var wg sync.WaitGroup
	for i, cand := range cands {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, c Candidate) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			results[i] = t.verifyOne(cctx, origSQL, origCost, c, doEquiv)
		}(i, cand)
	}
	wg.Wait()
	return results
}

// verifyOne handles a single candidate.
func (t *Tuner) verifyOne(ctx context.Context, origSQL string, origCost float64, c Candidate, doEquiv bool) VerifyResult {
	r := VerifyResult{CandID: c.ID, OldCost: origCost}

	switch c.Type {
	case "rewrite", "hint":
		// Can directly EXPLAIN the candidate SQL
		r.Verifiable = true
		newCost, err := t.planner.QuickPlanCost(ctx, c.SQL)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.NewCost = newCost
		if newCost > 0 && origCost > 0 {
			r.Speedup = origCost / newCost
		}
		// Equivalence check (rewrite only; hint preserves semantics by definition).
		// M6.5: type-assert to sqltune.EquivVerifier — works across all
		// dialects that implement the optional interface; dialects without
		// equiv support naturally skip (EquivOK stays nil = Unknown).
		if doEquiv && c.Type == "rewrite" {
			if ev, ok := t.planner.(sqltune.EquivVerifier); ok {
				equiv, err := ev.VerifyEquivalence(ctx, origSQL, c.SQL, 1000)
				if err == nil {
					r.EquivOK = &equiv
				}
			}
		}
	case "index", "schema", "stats":
		// Can't directly EXPLAIN — DDL hasn't been applied
		r.Verifiable = false
		r.Note = "DDL 类方案未实跑 EXPLAIN（DDL 没改），收益基于 LLM 推理"
	default:
		r.Note = "未知 type: " + c.Type
	}
	return r
}

// runRound2 asks LLM to write the final markdown report.
func (t *Tuner) runRound2(ctx context.Context, cc *CollectedContext, round1 *Round1Output, verifies []VerifyResult) (string, error) {
	systemPrompt := `你是 OG SQL 调优报告编辑。基于 Round 1 候选方案 + 验证结果，写最终 markdown 报告。

报告结构（严格按以下章节）：

# SQL Tuning Report

## 1. 输入 SQL
（直接贴用户原 SQL）

## 2. 执行计划分析
（plan tree 摘要 + 标注问题节点）

## 3. CBO 决策溯源
（引用 Round 1 的 cbo_analysis，说明为什么 CBO 选了当前 plan）

## 4. 关键证据
（表格：证据 / 数据 / 来源）
PG/openGauss 的 total_cost 是累积成本；计划热点必须按 self_cost = node.total_cost - 子节点 total_cost 之和排序。
不要把只继承子节点成本的父节点写成主要瓶颈；低占比 Seq Scan 不要写成“高成本顺序扫描”。
逗号隐式连接没有漏连接条件/笛卡尔积证据时，只作为风格提示，不列为性能反模式。
DISTINCT + GROUP BY 只有列集语义等价时才说冗余；NOT IN 改 NOT EXISTS 必须说明 NULL 语义风险。

## 5. 优化方案（按预期收益排序）

按 Speedup 从高到低排列已验证方案，每个方案：
**操作**: SQL/DDL
**EXPLAIN 验证**: ✅ cost X → Y (N×) 或 ⚠️ unverifiable (DDL 类)
**等价性验证**: ✅/⚠️
**⚠️ 风险**: ...
**📋 前置检查**: ...
**🔄 回滚**: ...

## 6. 已尝试但未采纳的方案
（cost 改善 < 30% 或验证失败的，简短列出 + 弃案理由）

## 7. 综合建议
（生产环境推荐执行顺序）

不要凭空发挥，基于给定数据写。`

	// Build Round 2 user message
	var b strings.Builder
	b.WriteString("# Round 1 输出\n\n")
	b.WriteString("## CBO 分析\n" + round1.CBOAnalysis + "\n\n")
	b.WriteString("## 候选方案\n\n")
	for _, c := range round1.Candidates {
		b.WriteString(fmt.Sprintf("### Candidate %d (%s, risk=%s)\n", c.ID, c.Type, c.RiskLevel))
		b.WriteString("Rationale: " + c.Rationale + "\n")
		b.WriteString("Expected gain: " + c.ExpectedGain + "\n")
		b.WriteString("```sql\n" + c.SQL + "\n```\n\n")
	}

	b.WriteString("# 验证结果\n\n")
	for _, vr := range verifies {
		b.WriteString(fmt.Sprintf("- Cand %d: ", vr.CandID))
		if !vr.Verifiable {
			b.WriteString("(DDL 类未实跑) " + vr.Note + "\n")
			continue
		}
		if vr.Error != "" {
			b.WriteString("❌ 验证失败: " + vr.Error + "\n")
			continue
		}
		b.WriteString(fmt.Sprintf("cost %.0f → %.0f (%.1f×)", vr.OldCost, vr.NewCost, vr.Speedup))
		if vr.EquivOK != nil {
			if *vr.EquivOK {
				b.WriteString(" ✅ equiv 通过")
			} else {
				b.WriteString(" ⚠️ equiv 不通过")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n# 原始上下文（参考）\n\n")
	b.WriteString("原 SQL:\n```sql\n" + cc.OrigSQL + "\n```\n\n")
	if cc.Plan != nil {
		b.WriteString(fmt.Sprintf("原 plan total_cost: %.2f\n\n", cc.Plan.TotalCost))
		if hotspots := formatSelfCostHotspots(cc.Plan, 12); hotspots != "" {
			b.WriteString("## self_cost 计划热点\n\n")
			b.WriteString(hotspots)
			b.WriteString("\n")
		}
	}

	b.WriteString("\n请按指定结构输出 markdown 报告。")

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: b.String()},
		},
		MaxTokens: 6000,
	}

	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	resp, err := t.provider.Chat(cctx, req)
	if err != nil {
		return "", err
	}
	if resp == nil {
		return "", fmt.Errorf("empty Round 2 response")
	}
	return resp.Content, nil
}

// renderFallbackReport produces a deterministic report when Round 2 LLM call fails.
func renderFallbackReport(cc *CollectedContext, round1 *Round1Output, verifies []VerifyResult) string {
	var b strings.Builder
	b.WriteString("# SQL Tuning Report (fallback render)\n\n")
	b.WriteString("> Round 2 LLM 调用失败，本报告由确定性模板渲染。\n\n")

	b.WriteString("## 1. 输入 SQL\n\n```sql\n" + cc.OrigSQL + "\n```\n\n")

	if cc.Plan != nil {
		b.WriteString(fmt.Sprintf("## 2. 执行计划\n\nTotal cost: %.2f\n\n", cc.Plan.TotalCost))
		if hotspots := formatSelfCostHotspots(cc.Plan, 12); hotspots != "" {
			b.WriteString("### Self-cost 计划热点\n\n")
			b.WriteString(hotspots)
			b.WriteString("\n")
		}
	}

	b.WriteString("## 3. CBO 分析\n\n" + round1.CBOAnalysis + "\n\n")

	b.WriteString("## 4. 优化方案\n\n")
	// Sort by speedup
	type pair struct {
		c  Candidate
		vr VerifyResult
	}
	var pairs []pair
	for _, c := range round1.Candidates {
		var vr VerifyResult
		for _, v := range verifies {
			if v.CandID == c.ID {
				vr = v
				break
			}
		}
		pairs = append(pairs, pair{c, vr})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].vr.Speedup > pairs[j].vr.Speedup
	})

	for i, p := range pairs {
		b.WriteString(fmt.Sprintf("### 方案 %d: %s (%s)\n\n", i+1, p.c.Rationale, p.c.Type))
		b.WriteString("```sql\n" + p.c.SQL + "\n```\n\n")
		if p.vr.Verifiable && p.vr.Error == "" {
			b.WriteString(fmt.Sprintf("**EXPLAIN 验证**: cost %.0f → %.0f (%.1f×)\n", p.vr.OldCost, p.vr.NewCost, p.vr.Speedup))
		} else if !p.vr.Verifiable {
			b.WriteString("**EXPLAIN 验证**: ⚠️ DDL 类未实跑\n")
		} else {
			b.WriteString("**EXPLAIN 验证**: ❌ " + p.vr.Error + "\n")
		}
		if p.vr.EquivOK != nil {
			if *p.vr.EquivOK {
				b.WriteString("**等价性验证**: ✅ 抽样通过\n")
			} else {
				b.WriteString("**等价性验证**: ⚠️ 不通过\n")
			}
		}
		b.WriteString(fmt.Sprintf("**风险等级**: %s\n", p.c.RiskLevel))
		b.WriteString("**预期收益**: " + p.c.ExpectedGain + "\n\n")
	}

	if len(round1.UncertaintyNotes) > 0 {
		b.WriteString("## 不确定的点\n\n")
		for _, n := range round1.UncertaintyNotes {
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

type selfCostHotspot struct {
	Node      *PlanNode
	SelfCost  float64
	TotalCost float64
	Share     float64
	Reason    string
}

func formatSelfCostHotspots(plan *PlanInfo, limit int) string {
	hotspots := buildSelfCostHotspots(plan, limit)
	if len(hotspots) == 0 {
		return ""
	}
	var b strings.Builder
	for _, h := range hotspots {
		if h.Node == nil {
			continue
		}
		b.WriteString(fmt.Sprintf("- %s on %s: self_cost=%.0f total_cost=%.0f share=%.1f%%, %s\n",
			h.Node.Operator, nonEmptyRelation(h.Node.RelationName), h.SelfCost, h.TotalCost, h.Share*100, h.Reason))
	}
	return b.String()
}

func buildSelfCostHotspots(plan *PlanInfo, limit int) []selfCostHotspot {
	if plan == nil || plan.Root == nil || limit <= 0 {
		return nil
	}
	rootCost := planRootTotalCost(plan)
	var items []selfCostHotspot
	walkPlanNodes(plan.Root, func(n *PlanNode) {
		if n == nil || !isSelfCostCandidate(n) {
			return
		}
		selfCost := planNodeSelfCost(n)
		share := planCostShare(selfCost, rootCost)
		if !isSelfCostHotspot(n, selfCost, share, rootCost) {
			return
		}
		items = append(items, selfCostHotspot{
			Node:      n,
			SelfCost:  selfCost,
			TotalCost: n.TotalCost,
			Share:     share,
			Reason:    selfCostHotspotReason(n, selfCost, share),
		})
	})
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SelfCost != items[j].SelfCost {
			return items[i].SelfCost > items[j].SelfCost
		}
		return items[i].Node.Operator < items[j].Node.Operator
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func walkPlanNodes(root *PlanNode, fn func(*PlanNode)) {
	if root == nil {
		return
	}
	fn(root)
	for _, child := range root.Children {
		walkPlanNodes(child, fn)
	}
}

func planRootTotalCost(plan *PlanInfo) float64 {
	if plan == nil {
		return 0
	}
	if plan.Root != nil && plan.Root.TotalCost > 0 {
		return plan.Root.TotalCost
	}
	if plan.TotalCost > 0 {
		return plan.TotalCost
	}
	return 0
}

func planNodeSelfCost(n *PlanNode) float64 {
	if n == nil || n.TotalCost <= 0 {
		return 0
	}
	childCost := 0.0
	for _, child := range n.Children {
		if child != nil && child.TotalCost > 0 {
			childCost += child.TotalCost
		}
	}
	selfCost := n.TotalCost - childCost
	if selfCost < 0 {
		return 0
	}
	return selfCost
}

func planCostShare(cost, total float64) float64 {
	if cost <= 0 || total <= 0 {
		return 0
	}
	return cost / total
}

func isSelfCostCandidate(n *PlanNode) bool {
	op := strings.ToLower(n.Operator)
	return strings.Contains(op, "seq scan") ||
		strings.Contains(op, "bitmap heap") ||
		strings.Contains(op, "sort") ||
		strings.Contains(op, "hash join") ||
		strings.Contains(op, "nested loop")
}

func isSelfCostHotspot(n *PlanNode, selfCost, share, rootCost float64) bool {
	if n == nil || selfCost <= 0 {
		return false
	}
	const (
		minAbsoluteSelfCost = 100.0
		minShare            = 0.03
		minLargeScanShare   = 0.01
		minSkewShare        = 0.005
	)
	if rootCost <= 0 {
		return selfCost >= minAbsoluteSelfCost
	}
	if selfCost >= minAbsoluteSelfCost && share >= minShare {
		return true
	}
	op := strings.ToLower(n.Operator)
	if strings.Contains(op, "seq scan") && n.PlanRows >= 10000 &&
		selfCost >= minAbsoluteSelfCost && share >= minLargeScanShare {
		return true
	}
	if n.SortSpaceType != "" && !strings.EqualFold(n.SortSpaceType, "Memory") && selfCost >= 50 {
		return true
	}
	if hasPlanRowSkew(n) && selfCost >= 50 && share >= minSkewShare {
		return true
	}
	return false
}

func selfCostHotspotReason(n *PlanNode, selfCost, share float64) string {
	if n == nil {
		return ""
	}
	op := strings.ToLower(n.Operator)
	if hasPlanRowSkew(n) {
		return "估算行数与实际行数偏差明显，优先修复统计信息"
	}
	if n.SortSpaceType != "" && !strings.EqualFold(n.SortSpaceType, "Memory") {
		return "排序落盘，检查 work_mem、排序键索引或 LIMIT 下推"
	}
	switch {
	case strings.Contains(op, "seq scan"):
		if n.PlanRows >= 10000 {
			return "顺序扫描自身成本占比显著，且扫描行数较大，优先检查过滤列/连接列索引与统计信息"
		}
		return "顺序扫描自身成本占比显著，检查谓词是否可走索引与统计信息是否准确"
	case strings.Contains(op, "bitmap heap"):
		return "Bitmap Heap 自身成本占比显著，检查索引覆盖度、回表行数与过滤选择性"
	case strings.Contains(op, "sort"):
		return "排序自身成本占比显著，检查排序键索引、LIMIT 下推或 work_mem"
	case strings.Contains(op, "hash join"):
		return "Hash Join 自身成本占比显著，检查构建端行数、连接列索引和统计信息"
	case strings.Contains(op, "nested loop"):
		return "Nested Loop 自身成本占比显著，检查内层是否可用连接列索引"
	default:
		return fmt.Sprintf("该节点 self_cost=%.0f，占总 cost %.1f%%", selfCost, share*100)
	}
}

func hasPlanRowSkew(n *PlanNode) bool {
	if n == nil || n.ActualRows <= 0 || n.PlanRows <= 0 {
		return false
	}
	ratio := float64(n.ActualRows) / float64(n.PlanRows)
	return ratio > 10 || ratio < 0.1
}

func nonEmptyRelation(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
