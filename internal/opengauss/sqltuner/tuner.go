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
	"regexp"
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
		round1 = mergeDeterministicCandidates(cc, &Round1Output{})
		verifyResults := t.verifyCandidates(ctx, cc.OrigSQL, round1.Candidates, opts.Verify)
		stats.Rounds = 1
		stats.CandidateCount = len(round1.Candidates)
		for _, vr := range verifyResults {
			if vr.Verifiable && vr.Error == "" {
				stats.VerifiedCount++
				if vr.Speedup > stats.BestSpeedup {
					stats.BestSpeedup = vr.Speedup
				}
			}
		}
		stats.TotalDuration = time.Since(start)
		degradedMD := "# SQL Tuning Report (deterministic safe mode)\n\n" +
			"> LLM 候选生成未返回可用结果: " + err.Error() + "\n" +
			"> 已启用确定性 Phase A 分析：基于 EXPLAIN + schema 生成低风险 index/stats 候选.\n\n" +
			renderFallbackReport(cc, round1, verifyResults, "已启用 deterministic safe mode，本报告基于 Phase A 证据渲染。")
		degradedMD = renderStatsBanner(stats) + degradedMD
		return &FinalReport{Markdown: degradedMD, Stats: stats}, nil
	}
	round1 = mergeDeterministicCandidates(cc, round1)
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
		markdown = renderFallbackReport(cc, round1, verifyResults, "Quick mode 已跳过 Round 2 LLM 润色，本报告由确定性模板渲染。")
	} else {
		var err error
		markdown, err = t.runRound2(ctx, cc, round1, verifyResults)
		if err != nil {
			markdown = renderFallbackReport(cc, round1, verifyResults, "Round 2 LLM 调用失败，本报告由确定性模板渲染。")
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
	b.WriteString(fmt.Sprintf("耗时 %s · %d 阶段 · %d candidates · %d verified",
		s.TotalDuration.Round(time.Second).String(),
		s.Rounds, s.CandidateCount, s.VerifiedCount))
	if s.VerifiedCount == 0 {
		b.WriteString(" · best 未验证")
	} else if s.BestSpeedup < 1.3 {
		b.WriteString(fmt.Sprintf(" · best %.1f×（低于采纳阈值）", s.BestSpeedup))
	} else {
		b.WriteString(fmt.Sprintf(" · best %.1f×", s.BestSpeedup))
	}
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
	if reason := invalidCandidateSQLReason(c); reason != "" {
		r.Error = reason
		r.Note = "preflight rejected before EXPLAIN"
		return r
	}

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
		r.Note = "DDL/统计类方案未落库执行，需变更后重新 EXPLAIN；quick mode 不计算收益倍率"
	case "parameter":
		// Parameter changes must be validated in a scoped session/transaction.
		r.Verifiable = false
		r.Note = "参数类方案需会话级 SET LOCAL 后重新 EXPLAIN；quick mode 不计算收益倍率"
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

## 5. 优化方案

先列已验证且 cost 改善 >= 30% 的 rewrite/hint；再列 index/stats/schema 这类待落库验证方案。
语法错误、包含省略号占位、缺少完整 SQL/DDL 的候选不要放入正式“优化方案”。
这些失败候选必须放入“模型候选被拒绝（调试信息）”，说明这是 LLM 草案不可执行或不完整，已被验证层拦截，不是可执行建议。
等价性不通过、cost 改善 < 30% 的候选也放入“模型候选被拒绝（调试信息）”，简短列出弃案理由。
每个方案：
**操作**: SQL/DDL
**验证状态**: ✅ cost X → Y (N×) 或 ⚠️ 待落库验证（DDL/统计类）
**等价性验证**: ✅/⚠️
**⚠️ 风险**: ...
**📋 前置检查**: ...
**🔄 回滚**: ...

## 6. 模型候选被拒绝（调试信息）
（语法错误、占位符、不完整、等价性不通过、收益不足的候选，简短列出 + 拒绝原因 + 不要执行）

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

type reportCandidate struct {
	c            Candidate
	vr           VerifyResult
	hasVerify    bool
	rejectReason string
}

type pendingStrategy struct {
	CandidateID int
	Type        string
	Strategy    string
	Reason      string
	NextStep    string
	AppliesTo   []string
}

type sqlTuneEvidence struct {
	PlanMarkers map[*PlanNode]planMarker
	Hotspots    []planHotspot
	ObjectRows  []objectEvidenceRow
}

type planMarker struct {
	Ref          string
	Relation     string
	Operator     string
	CandidateIDs []int
	Reason       string
}

type planHotspot struct {
	Node   *PlanNode
	Score  float64
	Reason string
}

type objectEvidenceRow struct {
	Table   string
	Rows    string
	Size    string
	Indexes string
	Stats   string
}

// renderFallbackReport produces a deterministic report when quick mode skips
// Round 2 or when the final LLM report call fails. It is deliberately stricter
// than the LLM output path: failed SQL rewrites, placeholder SQL, and unverified
// DDL benefit claims are not presented as accepted optimization plans.
func renderFallbackReport(cc *CollectedContext, round1 *Round1Output, verifies []VerifyResult, reason string) string {
	var b strings.Builder
	b.WriteString("# SQL Tuning Report\n\n")
	if reason == "" {
		reason = "本报告由确定性模板渲染。"
	}
	b.WriteString("> " + reason + "\n\n")

	b.WriteString("## 1. 输入 SQL\n\n```sql\n" + formatSQLForReport(cc.OrigSQL) + "\n```\n\n")

	accepted, rejected := classifyReportCandidates(round1.Candidates, verifies)
	evidence := buildSQLTuneEvidence(cc, accepted)
	planRefs := evidence.PlanMarkers
	if cc.Plan != nil {
		b.WriteString("## 2. 执行计划\n\n")
		b.WriteString(renderAnnotatedPlan(cc.Plan, planRefs))
	}

	b.WriteString("## 3. 关键证据\n\n")
	if len(evidence.Hotspots) > 0 {
		b.WriteString("### 代价热点\n\n")
		b.WriteString("| 标记 | 算子 | 对象 | cost | 说明 |\n")
		b.WriteString("|---|---|---|---:|---|\n")
		for _, h := range evidence.Hotspots {
			if h.Node == nil {
				continue
			}
			ref := markerRefForNode(planRefs, h.Node)
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %.0f | %s |\n",
				ref, h.Node.Operator, nonEmpty(h.Node.RelationName, "-"), h.Node.TotalCost, h.Reason))
		}
		b.WriteString("\n")
	}
	if len(evidence.ObjectRows) > 0 {
		b.WriteString("### 表 / 索引 / 统计信息\n\n")
		b.WriteString("| 表 | 行数 | 大小 | 索引 | 统计信息观察 |\n")
		b.WriteString("|---|---:|---:|---|---|\n")
		for _, row := range evidence.ObjectRows {
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				row.Table, row.Rows, row.Size, row.Indexes, row.Stats))
		}
		b.WriteString("\n")
	}
	if len(evidence.Hotspots) == 0 && len(evidence.ObjectRows) == 0 {
		b.WriteString("Phase A 未采集到足够的 plan/schema 证据；本轮只展示已验证候选和拒绝原因。\n\n")
	}

	b.WriteString("## 4. CBO 分析\n\n" + round1.CBOAnalysis + "\n\n")

	b.WriteString("## 5. 优化方案\n\n")
	if len(accepted) == 0 {
		b.WriteString("未生成可直接采纳的方案。请优先查看“模型候选被拒绝（调试信息）”和“不确定的点”。\n\n")
	}
	for i, p := range accepted {
		b.WriteString(fmt.Sprintf("### 方案 %d: %s\n\n", i+1, candidateTitle(p.c)))
		if refs := refsForCandidate(planRefs, p.c.ID); len(refs) > 0 {
			b.WriteString("**对应计划节点**: " + strings.Join(refs, ", ") + "\n\n")
		}
		if rationale := strings.TrimSpace(p.c.Rationale); rationale != "" {
			b.WriteString("**依据**: " + rationale + "\n\n")
		}
		b.WriteString("```sql\n" + formatSQLForReport(p.c.SQL) + "\n```\n\n")
		if p.vr.Verifiable {
			b.WriteString(fmt.Sprintf("**验证结果**: cost %.0f → %.0f (%.1f×)\n", p.vr.OldCost, p.vr.NewCost, p.vr.Speedup))
		}
		if p.vr.EquivOK != nil {
			if *p.vr.EquivOK {
				b.WriteString("**等价性验证**: 抽样通过\n")
			} else {
				b.WriteString("**等价性验证**: 不通过\n")
			}
		}
		if risk := strings.TrimSpace(p.c.RiskLevel); risk != "" {
			b.WriteString(fmt.Sprintf("**风险等级**: %s\n", risk))
		}
		if precheck := precheckAdvice(p.c); precheck != "" {
			b.WriteString("**执行前检查**: " + precheck + "\n")
		}
		if rollback := rollbackAdvice(p.c); rollback != "" {
			b.WriteString("**回滚方式**: " + rollback + "\n")
		}
		b.WriteString("\n")
	}

	pending := recoverPendingStrategies(rejected)
	if len(pending) > 0 {
		b.WriteString("## 6. 待验证策略（从未采纳候选恢复）\n\n")
		b.WriteString("这些策略来自 LLM 候选或验证失败候选的可取方向，但当前没有完整、等价、可验证的 SQL/DDL；因此只保留为后续补全/复测线索，不作为正式执行方案。\n\n")
		for _, st := range pending {
			b.WriteString(fmt.Sprintf("- Candidate %d (%s): %s\n", st.CandidateID, st.Type, st.Strategy))
			if len(st.AppliesTo) > 0 {
				b.WriteString("  - 涉及对象: " + strings.Join(st.AppliesTo, ", ") + "\n")
			}
			b.WriteString("  - 未采纳原因: " + st.Reason + "\n")
			b.WriteString("  - 下一步: " + st.NextStep + "\n")
		}
		b.WriteString("\n")
	}

	if len(rejected) > 0 {
		visibleRejected := diagnosticRejectedCandidates(rejected)
		if len(visibleRejected) > 0 {
			b.WriteString("## 7. 模型候选被拒绝（调试信息）\n\n")
			for _, p := range visibleRejected {
				b.WriteString(fmt.Sprintf("- Candidate %d (%s): %s\n", p.c.ID, p.c.Type, firstLine(p.c.Rationale)))
				b.WriteString("  - 拒绝原因: " + p.rejectReason + "\n")
				if diagnosis := rejectedCandidateDiagnosis(p); diagnosis != "" {
					b.WriteString("  - 判断: " + diagnosis + "\n")
				}
				if snippet := rejectedCandidateSnippet(p.c.SQL); snippet != "" {
					b.WriteString("  - 模型输出片段: `" + snippet + "`\n")
				}
			}
			b.WriteString("\n")
		}
	}

	if len(round1.UncertaintyNotes) > 0 {
		b.WriteString("## 不确定的点\n\n")
		for _, n := range round1.UncertaintyNotes {
			b.WriteString("- " + n + "\n")
		}
	}
	return b.String()
}

func diagnosticRejectedCandidates(rejected []reportCandidate) []reportCandidate {
	out := make([]reportCandidate, 0, len(rejected))
	for _, p := range rejected {
		if strings.TrimSpace(p.rejectReason) == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func recoverPendingStrategies(rejected []reportCandidate) []pendingStrategy {
	out := make([]pendingStrategy, 0)
	seen := make(map[string]bool)
	for _, p := range rejected {
		if !isRecoverableStrategyReject(p.rejectReason) {
			continue
		}
		strategy := inferCandidateStrategy(p)
		if strategy == "" {
			continue
		}
		key := strings.ToLower(strings.Join(strings.Fields(p.c.Type+" "+strategy), " "))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, pendingStrategy{
			CandidateID: p.c.ID,
			Type:        strings.TrimSpace(p.c.Type),
			Strategy:    strategy,
			Reason:      p.rejectReason,
			NextStep:    pendingStrategyNextStep(p),
			AppliesTo:   append([]string(nil), p.c.AppliesTo...),
		})
	}
	return out
}

func isRecoverableStrategyReject(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	if reason == "" {
		return false
	}
	for _, token := range []string{"省略号", "不完整片段", "完整可执行", "模板占位符", "syntax error", "语法"} {
		if strings.Contains(reason, token) {
			return true
		}
	}
	return false
}

func inferCandidateStrategy(p reportCandidate) string {
	rationale := strings.TrimSpace(p.c.Rationale)
	if rationale != "" {
		return firstLine(rationale)
	}
	lowerSQL := strings.ToLower(strings.Join(strings.Fields(p.c.SQL), " "))
	switch {
	case strings.Contains(lowerSQL, "exists"):
		return "相关 EXISTS 下推或半连接改写"
	case strings.Contains(lowerSQL, "lateral") || strings.Contains(lowerSQL, "count(*)"):
		return "标量聚合子查询改 JOIN/LATERAL"
	case strings.Contains(lowerSQL, "create index"):
		return "补充访问路径索引"
	case strings.Contains(lowerSQL, "create statistics") || strings.Contains(lowerSQL, "analyze"):
		return "修复统计信息"
	case strings.TrimSpace(p.c.Type) != "":
		return candidateTitle(p.c)
	default:
		return "保留该候选的优化方向"
	}
}

func pendingStrategyNextStep(p reportCandidate) string {
	switch strings.ToLower(strings.TrimSpace(p.c.Type)) {
	case "rewrite", "hint":
		return "补全为完整 SQL 后重新 EXPLAIN，并做结果集抽样等价性验证；通过后才可进入正式方案。"
	case "index", "stats", "schema":
		return "补全为完整 DDL，在测试库执行后重新 EXPLAIN；确认无重复对象和可接受的写入/锁影响。"
	case "parameter":
		return "用 SET LOCAL 在单会话内复测 EXPLAIN/耗时，确认有效后再评估参数落地范围。"
	default:
		return "补全候选并重新进入 SQLTune 验证链路。"
	}
}

func rejectedCandidateDiagnosis(p reportCandidate) string {
	reason := strings.ToLower(p.rejectReason)
	switch {
	case strings.Contains(reason, "syntax error") || strings.Contains(reason, "语法"):
		return "LLM 生成了当前 GaussDB/openGauss 无法解析的 SQL 或 hint；验证层已拦截，未作为正式优化方案。"
	case strings.Contains(reason, "省略号") || strings.Contains(reason, "完整可执行") || strings.Contains(reason, "模板占位符") || strings.Contains(reason, "不完整片段"):
		return "LLM 把改写方向、占位符或片段当成完整 SQL/DDL 输出；验证层已拦截，未作为正式优化方案。"
	case strings.Contains(reason, "等价性"):
		return "候选可能改变结果集语义；未作为正式优化方案。"
	case strings.Contains(reason, "改善不足"):
		return "候选能执行但收益不足，不值得作为本轮主方案。"
	case strings.Contains(reason, "缺少验证结果"):
		return "缺少可用于采纳的验证数据；未作为正式优化方案。"
	default:
		return "该候选没有通过采纳门禁，未作为正式优化方案。"
	}
}

func rejectedCandidateSnippet(sqlText string) string {
	sqlText = stripANSIArtifacts(sqlText)
	sqlText = strings.Join(strings.Fields(strings.TrimSpace(sqlText)), " ")
	if sqlText == "" {
		return ""
	}
	runes := []rune(sqlText)
	if len(runes) > 180 {
		sqlText = string(runes[:180]) + "..."
	}
	return strings.ReplaceAll(sqlText, "`", "'")
}

func findVerifyResult(verifies []VerifyResult, candID int) (VerifyResult, bool) {
	for _, vr := range verifies {
		if vr.CandID == candID {
			return vr, true
		}
	}
	return VerifyResult{}, false
}

func buildSQLTuneEvidence(cc *CollectedContext, candidates []reportCandidate) sqlTuneEvidence {
	evidence := sqlTuneEvidence{
		PlanMarkers: make(map[*PlanNode]planMarker),
	}
	if cc == nil {
		return evidence
	}
	evidence.Hotspots = buildPlanHotspots(cc.Plan, 12)
	evidence.PlanMarkers = buildPlanMarkers(cc, candidates, evidence.Hotspots)
	evidence.ObjectRows = buildObjectEvidenceRows(cc)
	return evidence
}

func buildPlanMarkers(cc *CollectedContext, candidates []reportCandidate, hotspots []planHotspot) map[*PlanNode]planMarker {
	markers := make(map[*PlanNode]planMarker)
	if cc == nil || cc.Plan == nil || cc.Plan.Root == nil {
		return markers
	}
	candidatesByTable := make(map[string][]int)
	for _, p := range candidates {
		for _, table := range p.c.AppliesTo {
			table = strings.ToLower(strings.TrimSpace(table))
			if table != "" {
				candidatesByTable[table] = appendUniqueInt(candidatesByTable[table], p.c.ID)
			}
		}
	}
	next := 1
	addMarker := func(n *PlanNode, cids []int, reason string) {
		if n == nil {
			return
		}
		if existing, ok := markers[n]; ok {
			existing.CandidateIDs = appendUniqueInt(existing.CandidateIDs, cids...)
			if existing.Reason == "" {
				existing.Reason = reason
			}
			markers[n] = existing
			return
		}
		rel := n.RelationName
		if rel == "" {
			rel = "-"
		}
		markers[n] = planMarker{
			Ref:          fmt.Sprintf("[P%d]", next),
			Relation:     rel,
			Operator:     n.Operator,
			CandidateIDs: appendUniqueInt(nil, cids...),
			Reason:       nonEmpty(reason, planMarkerReason(n)),
		}
		next++
	}
	for _, h := range hotspots {
		if h.Node != nil {
			addMarker(h.Node, nil, h.Reason)
		}
	}
	walkPlan(cc.Plan.Root, func(n *PlanNode, _ []string) {
		if n == nil || n.RelationName == "" {
			return
		}
		table := strings.ToLower(n.RelationName)
		cids := candidatesByTable[table]
		if len(cids) == 0 || !isPlanNodeWorthAnnotating(n) {
			return
		}
		addMarker(n, cids, planMarkerReason(n))
	})
	return markers
}

func buildPlanHotspots(plan *PlanInfo, limit int) []planHotspot {
	if plan == nil || plan.Root == nil || limit <= 0 {
		return nil
	}
	var items []planHotspot
	walkPlan(plan.Root, func(n *PlanNode, _ []string) {
		if n == nil || !isPlanNodeWorthAnnotating(n) {
			return
		}
		reason := planMarkerReason(n)
		score := n.TotalCost
		if strings.Contains(strings.ToLower(n.Operator), "seq scan") && n.PlanRows > 10000 {
			score *= 1.3
			reason = "大行数顺序扫描，优先核对过滤列/连接列索引与统计信息"
		}
		if n.SortSpaceType != "" && !strings.EqualFold(n.SortSpaceType, "Memory") {
			score *= 1.2
			reason = "排序落盘，检查 work_mem、排序键索引或 LIMIT 下推"
		}
		if n.ActualRows > 0 && n.PlanRows > 0 {
			ratio := float64(n.ActualRows) / float64(n.PlanRows)
			if ratio > 10 || ratio < 0.1 {
				score *= 1.5
				reason = "估算行数与实际行数偏差明显，优先修复统计信息"
			}
		}
		items = append(items, planHotspot{Node: n, Score: score, Reason: reason})
	})
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		return items[i].Node.Operator < items[j].Node.Operator
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func buildObjectEvidenceRows(cc *CollectedContext) []objectEvidenceRow {
	if cc == nil || cc.Schema == nil {
		return nil
	}
	seen := make(map[string]bool)
	var tables []string
	for _, t := range cc.InvolvedTables {
		name := strings.TrimSpace(t)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		tables = append(tables, name)
	}
	if len(tables) == 0 {
		for name := range cc.Schema.Tables {
			tables = append(tables, name)
		}
		sort.Strings(tables)
	}
	var rows []objectEvidenceRow
	for _, name := range tables {
		table := lookupTableInfo(cc.Schema, name)
		idxs := lookupIndexes(cc.Schema, name)
		stats := lookupStats(cc.Schema, name)
		row := objectEvidenceRow{
			Table:   name,
			Rows:    "-",
			Size:    "-",
			Indexes: formatIndexEvidence(idxs),
			Stats:   formatStatsEvidence(stats),
		}
		if table != nil {
			if table.Tuples > 0 {
				row.Rows = fmt.Sprintf("%d", table.Tuples)
			}
			if table.SizeMB > 0 {
				row.Size = fmt.Sprintf("%.1fMB", table.SizeMB)
			} else if table.Pages > 0 {
				row.Size = fmt.Sprintf("%d pages", table.Pages)
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func lookupTableInfo(schema *SchemaInfo, name string) *TableInfo {
	if schema == nil {
		return nil
	}
	if t := schema.Tables[name]; t != nil {
		return t
	}
	lower := strings.ToLower(name)
	for k, t := range schema.Tables {
		if strings.ToLower(k) == lower || strings.HasSuffix(strings.ToLower(k), "."+lower) {
			return t
		}
	}
	return nil
}

func lookupIndexes(schema *SchemaInfo, name string) []IndexInfo {
	if schema == nil {
		return nil
	}
	if idxs := schema.Indexes[name]; len(idxs) > 0 {
		return idxs
	}
	lower := strings.ToLower(name)
	for k, idxs := range schema.Indexes {
		if strings.ToLower(k) == lower || strings.HasSuffix(strings.ToLower(k), "."+lower) {
			return idxs
		}
	}
	return nil
}

func lookupStats(schema *SchemaInfo, name string) []ColStat {
	if schema == nil {
		return nil
	}
	if stats := schema.Stats[name]; len(stats) > 0 {
		return stats
	}
	lower := strings.ToLower(name)
	for k, stats := range schema.Stats {
		if strings.ToLower(k) == lower || strings.HasSuffix(strings.ToLower(k), "."+lower) {
			return stats
		}
	}
	return nil
}

func formatIndexEvidence(idxs []IndexInfo) string {
	if len(idxs) == 0 {
		return "未采集到索引"
	}
	parts := make([]string, 0, minInt(len(idxs), 4))
	for i, idx := range idxs {
		if i >= 4 {
			parts = append(parts, fmt.Sprintf("另 %d 个", len(idxs)-i))
			break
		}
		label := idx.Name
		if len(idx.Columns) > 0 {
			label += "(" + strings.Join(idx.Columns, ",") + ")"
		}
		if idx.Primary {
			label += " PK"
		} else if idx.Unique {
			label += " UNIQUE"
		}
		parts = append(parts, label)
	}
	return escapeMarkdownTable(strings.Join(parts, "; "))
}

func formatStatsEvidence(stats []ColStat) string {
	if len(stats) == 0 {
		return "未采集到列统计"
	}
	var suspicious []string
	for _, st := range stats {
		if st.NDistinct == 1 || st.NullFrac > 0.5 || st.Correlation < -0.8 || st.Correlation > 0.8 {
			var tags []string
			if st.NDistinct == 1 {
				tags = append(tags, "n_distinct=1")
			}
			if st.NullFrac > 0.5 {
				tags = append(tags, fmt.Sprintf("null_frac=%.2f", st.NullFrac))
			}
			if st.Correlation < -0.8 || st.Correlation > 0.8 {
				tags = append(tags, fmt.Sprintf("corr=%.2f", st.Correlation))
			}
			suspicious = append(suspicious, st.Column+"("+strings.Join(tags, ",")+")")
		}
	}
	if len(suspicious) == 0 {
		return fmt.Sprintf("%d 列统计已采集", len(stats))
	}
	if len(suspicious) > 4 {
		suspicious = append(suspicious[:4], fmt.Sprintf("另 %d 项", len(suspicious)-4))
	}
	return escapeMarkdownTable(strings.Join(suspicious, "; "))
}

func markerRefForNode(markers map[*PlanNode]planMarker, n *PlanNode) string {
	if m, ok := markers[n]; ok && m.Ref != "" {
		return m.Ref
	}
	return "-"
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func escapeMarkdownTable(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func renderAnnotatedPlan(plan *PlanInfo, markers map[*PlanNode]planMarker) string {
	if plan == nil || plan.Root == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Total cost: %.2f\n\n", plan.TotalCost))
	b.WriteString("```plan\n")
	renderPlanNode(&b, plan.Root, markers, 0)
	b.WriteString("```\n\n")
	if len(markers) == 0 {
		return b.String()
	}
	ordered := make([]planMarker, 0, len(markers))
	for _, marker := range markers {
		ordered = append(ordered, marker)
	}
	sort.Slice(ordered, func(i, j int) bool { return planRefNumber(ordered[i].Ref) < planRefNumber(ordered[j].Ref) })
	b.WriteString("标注说明:\n")
	for _, marker := range ordered {
		line := fmt.Sprintf("- %s %s on %s: %s", marker.Ref, marker.Operator, marker.Relation, marker.Reason)
		if refs := formatCandidateRefs(marker.CandidateIDs); refs != "" {
			line += "；对应" + refs
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	return b.String()
}

const (
	planMaxLineWidth   = 88
	planMaxIndentDepth = 7
)

func renderPlanNode(b *strings.Builder, n *PlanNode, markers map[*PlanNode]planMarker, depth int) {
	if n == nil {
		return
	}
	indent := planIndent(depth)
	connector := "- "
	if depth > 0 {
		indent += connector
	}
	marker := ""
	if m, ok := markers[n]; ok {
		marker = m.Ref + " "
	}
	b.WriteString(limitPlanLine(indent + marker + planNodeSummary(n)))
	b.WriteString("\n")

	if _, marked := markers[n]; marked || depth <= 1 {
		detailPrefix := planIndent(depth + 1)
		for _, detail := range planNodeDetails(n) {
			for _, line := range wrapText(limitPlanDetail(detail), planMaxLineWidth, detailPrefix) {
				b.WriteString(line + "\n")
			}
		}
	}

	for _, child := range n.Children {
		renderPlanNode(b, child, markers, depth+1)
	}
}

func planRefNumber(ref string) int {
	ref = strings.Trim(ref, "[]Pp ")
	n := 0
	fmt.Sscanf(ref, "%d", &n)
	return n
}

func planIndent(depth int) string {
	if depth > planMaxIndentDepth {
		depth = planMaxIndentDepth
	}
	return strings.Repeat("  ", depth)
}

func limitPlanLine(s string) string {
	if displayLen(s) <= planMaxLineWidth {
		return s
	}
	r := []rune(s)
	if len(r) <= planMaxLineWidth {
		return s
	}
	return string(r[:planMaxLineWidth-1]) + "…"
}

func limitPlanDetail(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if displayLen(s) <= 180 {
		return s
	}
	r := []rune(s)
	return string(r[:179]) + "…"
}

func wrapText(text string, width int, prefix string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if width < 40 {
		width = 40
	}
	prefixWidth := displayLen(prefix)
	if prefixWidth > width-20 {
		prefix = strings.Repeat(" ", maxInt(width/4, 2))
		prefixWidth = displayLen(prefix)
	}
	var out []string
	rest := text
	for prefixWidth+displayLen(rest) > width {
		cut := lastBreakBefore(rest, width-prefixWidth)
		if cut <= 0 {
			break
		}
		out = append(out, prefix+strings.TrimRight(rest[:cut], " "))
		rest = strings.TrimLeft(rest[cut:], " ")
	}
	out = append(out, prefix+rest)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func planNodeSummary(n *PlanNode) string {
	parts := []string{n.Operator}
	if n.RelationName != "" {
		rel := n.RelationName
		if n.Alias != "" && n.Alias != n.RelationName {
			rel += " " + n.Alias
		}
		parts = append(parts, "on "+rel)
	}
	parts = append(parts, fmt.Sprintf("cost=%.0f", n.TotalCost))
	if n.PlanRows > 0 {
		parts = append(parts, fmt.Sprintf("rows=%d", n.PlanRows))
	}
	if n.ActualRows > 0 {
		parts = append(parts, fmt.Sprintf("actual_rows=%d", n.ActualRows))
	}
	return strings.Join(parts, " ")
}

func planNodeDetails(n *PlanNode) []string {
	var details []string
	for _, item := range []struct {
		label string
		value string
	}{
		{"Filter", n.Filter},
		{"Index Cond", n.IndexCondition},
		{"Hash Cond", n.HashCondition},
		{"Join Filter", n.JoinFilter},
	} {
		if strings.TrimSpace(item.value) != "" {
			details = append(details, item.label+": "+strings.TrimSpace(item.value))
		}
	}
	if len(n.SortKey) > 0 {
		details = append(details, "Sort Key: "+strings.Join(n.SortKey, ", "))
	}
	if n.SortMethod != "" || n.SortSpaceType != "" || n.SortSpaceUsed > 0 {
		details = append(details, fmt.Sprintf("Sort: method=%s space=%s %d", n.SortMethod, n.SortSpaceType, n.SortSpaceUsed))
	}
	return details
}

func isPlanNodeWorthAnnotating(n *PlanNode) bool {
	if isAccessPathWorthTuning(n) {
		return true
	}
	op := strings.ToLower(n.Operator)
	return strings.Contains(op, "sort") || strings.Contains(op, "hash join") || strings.Contains(op, "nested loop")
}

func planMarkerReason(n *PlanNode) string {
	op := strings.ToLower(n.Operator)
	switch {
	case strings.Contains(op, "seq scan"):
		return "高成本顺序扫描，优先检查过滤列/连接列索引与统计信息"
	case strings.Contains(op, "bitmap heap"):
		return "Bitmap Heap 访问成本高，检查索引覆盖度与过滤选择性"
	case strings.Contains(op, "sort"):
		return "排序节点成本高，检查排序键索引、LIMIT 下推或 work_mem"
	case strings.Contains(op, "hash join"):
		return "Hash Join 成本高，检查构建端行数、连接列索引和统计信息"
	case strings.Contains(op, "nested loop"):
		return "Nested Loop 成本高，检查内层是否可用连接列索引"
	default:
		return "该节点与优化方案涉及的表或算子相关"
	}
}

func classifyReportCandidates(candidates []Candidate, verifies []VerifyResult) ([]reportCandidate, []reportCandidate) {
	verifyByID := make(map[int]VerifyResult, len(verifies))
	for _, vr := range verifies {
		verifyByID[vr.CandID] = vr
	}
	accepted := make([]reportCandidate, 0, len(candidates))
	rejected := make([]reportCandidate, 0)
	acceptedKeys := make(map[string]int)
	for _, c := range candidates {
		vr, ok := verifyByID[c.ID]
		p := reportCandidate{c: c, vr: vr, hasVerify: ok}
		p.rejectReason = rejectCandidateReason(p)
		if p.rejectReason != "" {
			rejected = append(rejected, p)
			continue
		}
		key := candidateAdoptionKey(c)
		if key != "" {
			if prevID, exists := acceptedKeys[key]; exists {
				p.rejectReason = fmt.Sprintf("与已采纳 Candidate %d 同类重复", prevID)
				rejected = append(rejected, p)
				continue
			}
			acceptedKeys[key] = c.ID
		}
		accepted = append(accepted, p)
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		iv, jv := accepted[i].vr.Verifiable, accepted[j].vr.Verifiable
		if iv != jv {
			return iv
		}
		if accepted[i].vr.Speedup != accepted[j].vr.Speedup {
			return accepted[i].vr.Speedup > accepted[j].vr.Speedup
		}
		ip, jp := candidateSortPriority(accepted[i].c), candidateSortPriority(accepted[j].c)
		if ip != jp {
			return ip < jp
		}
		ir, jr := riskSortPriority(accepted[i].c.RiskLevel), riskSortPriority(accepted[j].c.RiskLevel)
		if ir != jr {
			return ir < jr
		}
		return accepted[i].c.ID < accepted[j].c.ID
	})
	sort.SliceStable(rejected, func(i, j int) bool { return rejected[i].c.ID < rejected[j].c.ID })
	return accepted, rejected
}

func candidateSortPriority(c Candidate) int {
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "index":
		return 10
	case "stats":
		return 20
	case "rewrite":
		return 30
	case "parameter":
		return 40
	case "hint":
		return 50
	case "schema":
		return 60
	default:
		return 90
	}
}

func riskSortPriority(risk string) int {
	switch strings.ToLower(strings.TrimSpace(risk)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 4
	}
}

func candidateAdoptionKey(c Candidate) string {
	typ := strings.ToLower(strings.TrimSpace(c.Type))
	sql := strings.ToLower(strings.Join(strings.Fields(stripANSIArtifacts(c.SQL)), " "))
	if typ == "" || sql == "" {
		return ""
	}
	switch typ {
	case "index":
		if m := regexp.MustCompile(`(?i)\bon\s+([a-z_][a-z0-9_$.]*)\s*\(([^)]*)\)`).FindStringSubmatch(sql); len(m) == 3 {
			return typ + ":" + m[1] + ":" + normalizeColumnList(m[2])
		}
	case "stats":
		if m := regexp.MustCompile(`(?i)\bon\s+([a-z0-9_,\s.]+)\s+from\s+([a-z_][a-z0-9_$.]*)`).FindStringSubmatch(sql); len(m) == 3 {
			return typ + ":" + m[2] + ":" + normalizeColumnList(m[1])
		}
	case "parameter":
		if strings.Contains(sql, "set local work_mem") {
			return typ + ":work_mem"
		}
	case "rewrite", "hint", "schema":
		return typ + ":" + sql
	}
	return typ + ":" + sql
}

func normalizeColumnList(cols string) string {
	parts := strings.Split(cols, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.Trim(part, `"`))
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, ",")
}

func refsForCandidate(markers map[*PlanNode]planMarker, candidateID int) []string {
	var refs []string
	for _, marker := range markers {
		for _, id := range marker.CandidateIDs {
			if id == candidateID {
				refs = append(refs, marker.Ref)
				break
			}
		}
	}
	sort.Strings(refs)
	return refs
}

func formatCandidateRefs(ids []int) string {
	if len(ids) == 0 {
		return ""
	}
	ids = append([]int(nil), ids...)
	sort.Ints(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("方案 %d", id))
	}
	return strings.Join(parts, "/")
}

func appendUniqueInt(items []int, vals ...int) []int {
	seen := make(map[int]bool, len(items)+len(vals))
	for _, item := range items {
		seen[item] = true
	}
	for _, val := range vals {
		if val == 0 || seen[val] {
			continue
		}
		items = append(items, val)
		seen[val] = true
	}
	return items
}

func candidateTitle(c Candidate) string {
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "index":
		return "索引调整"
	case "stats":
		return "统计信息修复"
	case "rewrite":
		return "SQL 改写"
	case "hint":
		return "Hint / 参数引导"
	case "parameter":
		return "会话参数验证"
	case "schema":
		return "结构调整"
	default:
		if strings.TrimSpace(c.Type) != "" {
			return c.Type
		}
		return "优化建议"
	}
}

func precheckAdvice(c Candidate) string {
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "index":
		return "先确认不存在等价前导列索引；在测试环境执行后重新 EXPLAIN；生产执行时避开高峰并观察 IO/锁等待。"
	case "stats":
		return "先确认当前统计信息采样是否过低或过旧；在测试环境 ANALYZE 后重新 EXPLAIN，对比行数估算是否改善。"
	case "rewrite":
		return "先做结果集抽样对比和执行计划对比，确认语义等价后再替换业务 SQL。"
	case "hint":
		return "先确认 hint 在当前 GaussDB/openGauss 版本生效，并在典型参数值下重复 EXPLAIN。"
	case "parameter":
		return "只在当前会话或事务内用 SET LOCAL 验证；确认计划和耗时改善后，再评估是否需要调整应用会话参数或数据库参数。"
	case "schema":
		return "需要维护窗口和回滚脚本；先在等量数据测试库验证迁移耗时与锁影响。"
	default:
		return ""
	}
}

func rollbackAdvice(c Candidate) string {
	switch strings.ToLower(strings.TrimSpace(c.Type)) {
	case "index":
		names := extractCreateNames(c.SQL, "index")
		if len(names) == 0 {
			return "删除本方案新增索引，并重新 ANALYZE 相关表。"
		}
		drops := make([]string, 0, len(names))
		for _, name := range names {
			drops = append(drops, "DROP INDEX CONCURRENTLY IF EXISTS "+name+";")
		}
		return strings.Join(drops, " ")
	case "stats":
		names := extractCreateNames(c.SQL, "statistics")
		if len(names) == 0 {
			return "删除本方案新增扩展统计；把相关列 statistics target 恢复默认值后重新 ANALYZE。"
		}
		drops := make([]string, 0, len(names))
		for _, name := range names {
			drops = append(drops, "DROP STATISTICS IF EXISTS "+name+";")
		}
		return strings.Join(drops, " ") + "；必要时将列 statistics target 恢复默认值。"
	case "rewrite":
		return "保留原 SQL，灰度失败时切回原 SQL。"
	case "hint":
		return "移除 hint 或恢复原会话参数。"
	case "parameter":
		return "结束当前事务/会话即可回滚 SET LOCAL；若已做 ALTER SYSTEM，执行 ALTER SYSTEM RESET 对应参数并 reload/restart。"
	default:
		return ""
	}
}

func extractCreateNames(sqlText, objectType string) []string {
	pattern := `(?i)\bcreate\s+` + objectType + `(?:\s+concurrently)?\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_$.]*)`
	re := regexp.MustCompile(pattern)
	var names []string
	for _, m := range re.FindAllStringSubmatch(sqlText, -1) {
		if len(m) == 2 {
			names = append(names, m[1])
		}
	}
	return names
}

var ansiSequenceRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func stripANSIArtifacts(s string) string {
	s = ansiSequenceRE.ReplaceAllString(s, "")
	// Some terminals strip ESC but leave the reset suffix visible as "[0m".
	// Treat that as display noise, not SQL text.
	s = strings.ReplaceAll(s, "[0m", "")
	return s
}

func formatSQLForReport(sqlText string) string {
	sqlText = stripANSIArtifacts(sqlText)
	lines := strings.Split(strings.TrimSpace(sqlText), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if displayLen(line) <= 112 {
			out = append(out, line)
			continue
		}
		out = append(out, wrapCodeLine(line, 112)...)
	}
	return strings.Join(out, "\n")
}

func wrapCodeLine(line string, width int) []string {
	if width < 40 {
		width = 40
	}
	indentLen := len(line) - len(strings.TrimLeft(line, " \t"))
	indent := line[:indentLen]
	continuation := indent + "  "
	rest := strings.TrimSpace(line)
	var out []string
	for displayLen(indent)+displayLen(rest) > width {
		cut := lastBreakBefore(rest, width-displayLen(indent))
		if cut <= 0 {
			break
		}
		out = append(out, indent+strings.TrimRight(rest[:cut], " "))
		rest = strings.TrimLeft(rest[cut:], " ")
		indent = continuation
	}
	out = append(out, indent+rest)
	return out
}

func lastBreakBefore(s string, max int) int {
	if max <= 0 {
		return 0
	}
	runes := []rune(s)
	if len(runes) <= max {
		return len(s)
	}
	for i := max; i > 0; i-- {
		switch runes[i-1] {
		case ' ', '\t', ',', ')':
			return len(string(runes[:i]))
		}
	}
	return len(string(runes[:max]))
}

func displayLen(s string) int {
	return len([]rune(s))
}

func rejectCandidateReason(p reportCandidate) string {
	if reason := invalidCandidateSQLReason(p.c); reason != "" {
		return reason
	}
	if !p.hasVerify {
		return "缺少验证结果"
	}
	if !p.vr.Verifiable {
		return ""
	}
	if p.vr.Error != "" {
		return "EXPLAIN 验证失败: " + p.vr.Error
	}
	if p.vr.EquivOK != nil && !*p.vr.EquivOK {
		return "等价性验证不通过"
	}
	if p.vr.Speedup < 1.3 {
		return fmt.Sprintf("cost 改善不足 30%%，当前 %.1f×", p.vr.Speedup)
	}
	return ""
}

func invalidCandidateSQLReason(c Candidate) string {
	sql := strings.TrimSpace(stripANSIArtifacts(c.SQL))
	if sql == "" {
		return "候选 SQL/DDL 为空"
	}
	lower := strings.ToLower(sql)
	compactLower := strings.Join(strings.Fields(lower), "")
	switch {
	case strings.Contains(sql, "...") || strings.Contains(sql, "…"):
		return "候选 SQL/DDL 含省略号占位，不能执行"
	case strings.Contains(compactLower, "原sql不变"), strings.Contains(compactLower, "originalsqlunchanged"):
		return "候选没有给出完整可执行 SQL/DDL"
	case strings.Contains(lower, "<imagined_table>") || containsTemplatePlaceholder(sql):
		return "候选 SQL/DDL 含模板占位符，不能执行"
	case containsUnsafeVolatileIndexDDL(sql):
		return "候选 CREATE INDEX 含 now()/current_timestamp 等非 immutable 表达式，不能作为可执行索引方案"
	case containsCTEOnlySchemaDDL(c.Type, sql):
		return "结构候选引用 CTE/临时结果，不能作为完整落库 DDL"
	case looksLikeIncompleteSQLFragment(sql):
		return "候选 SQL/DDL 是不完整片段，不能执行"
	}
	return ""
}

func containsUnsafeVolatileIndexDDL(sqlText string) bool {
	lower := strings.ToLower(sqlText)
	if !strings.Contains(lower, "create index") {
		return false
	}
	volatile := []string{"now()", "current_timestamp", "clock_timestamp()", "statement_timestamp()", "transaction_timestamp()", "random()", "pg_sleep("}
	for _, token := range volatile {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func containsCTEOnlySchemaDDL(candidateType, sqlText string) bool {
	typ := strings.ToLower(strings.TrimSpace(candidateType))
	if typ != "" && typ != "schema" {
		return false
	}
	lower := strings.ToLower(sqlText)
	if !strings.Contains(lower, "create table") {
		return false
	}
	for _, name := range []string{"recent_orders", "top_products", "customer_spend", "review_stats"} {
		if strings.Contains(lower, "from "+name) || strings.Contains(lower, "join "+name) {
			return true
		}
	}
	return false
}

func containsTemplatePlaceholder(sqlText string) bool {
	// Catch LLM/template placeholders such as <表名>, <PID>, <begin_snap_id>,
	// while allowing normal comparison predicates like a < b AND c > d.
	re := regexp.MustCompile(`<[A-Za-z_一-龥][A-Za-z0-9_一-龥./:-]{0,80}>`)
	return re.FindString(sqlText) != ""
}

func looksLikeIncompleteSQLFragment(sqlText string) bool {
	trimmed := strings.TrimSpace(sqlText)
	if trimmed == "" {
		return true
	}
	trimmed = strings.TrimSpace(strings.TrimRight(trimmed, ";"))
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, ",") || strings.HasSuffix(lower, "(") {
		return true
	}
	lastToken := trailingAlphaToken(lower)
	switch lastToken {
	case "select", "with", "from", "where", "join", "on", "and", "or", "group", "order", "by", "having", "set", "values", "into", "create", "alter", "drop", "insert", "update", "delete", "limit", "offset", "union":
		return true
	default:
		return false
	}
}

func trailingAlphaToken(s string) string {
	end := len(s)
	for end > 0 {
		r := rune(s[end-1])
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			end--
			continue
		}
		break
	}
	if end == 0 {
		return ""
	}
	r := rune(s[end-1])
	if !((r >= 'a' && r <= 'z') || r == '_') {
		return ""
	}
	start := end
	for start > 0 {
		r = rune(s[start-1])
		if (r >= 'a' && r <= 'z') || r == '_' {
			start--
			continue
		}
		break
	}
	return s[start:end]
}

func lastAlphaToken(s string) string {
	end := len(s)
	for end > 0 {
		r := rune(s[end-1])
		if (r >= 'a' && r <= 'z') || r == '_' {
			break
		}
		end--
	}
	start := end
	for start > 0 {
		r := rune(s[start-1])
		if (r >= 'a' && r <= 'z') || r == '_' {
			start--
			continue
		}
		break
	}
	return s[start:end]
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	if len([]rune(s)) <= 120 {
		return s
	}
	r := []rune(s)
	return string(r[:120]) + "..."
}
