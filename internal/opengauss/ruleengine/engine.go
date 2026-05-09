/*-------------------------------------------------------------------------
 *
 * engine.go
 *	  EvalContext is the runtime context that decision trees and trigger
 *	  conditions read from. It is populated from the DiagInput before
 *	  rule evaluation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/engine.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/opengauss/sentinel"
)

// ─── EvalContext ─────────────────────────────────────────────────────────────

// EvalContext is the runtime context that decision trees and trigger conditions
// read from. It is populated from the DiagInput before rule evaluation.
type EvalContext struct {
	// Raw input
	Input *DiagInput

	// Parsed from BurstReport
	Metrics map[string]sentinel.MetricSummary // metric name → summary

	// Burst collection data
	TopSQLs        []sentinel.SQLProfile    // top active SQL statements
	WaitProfile    []sentinel.WaitBucket    // wait event distribution
	BlockingChains []sentinel.BlockingChain // blocking relationships

	// Scalar summaries for quick condition checks
	PeakActive     int
	BaselineActive float64
	DurationSec    float64

	// Query results cache (populated lazily by decision tree steps)
	QueryResults map[QueryID]interface{}

	// Reference back to executor for tree queries
	executor QueryExecutor
	config   Config
}

// GetMetric returns the MetricSummary for a given metric name.
// Returns zero value if not found.
func (ctx *EvalContext) GetMetric(name string) (sentinel.MetricSummary, bool) {
	m, ok := ctx.Metrics[name]
	return m, ok
}

// MetricValue returns the average value of a named metric.
// Returns 0 if not found.
func (ctx *EvalContext) MetricValue(name string) float64 {
	if m, ok := ctx.Metrics[name]; ok {
		return m.Avg
	}
	return 0
}

// GetFloat returns a float64 from the given source and field.
// Source can be "metrics", "summary", etc.
// Returns 0 if not found.
func (ctx *EvalContext) GetFloat(source, field string) float64 {
	switch source {
	case "metrics":
		if m, ok := ctx.Metrics[field]; ok {
			return m.Avg
		}
	case "summary":
		switch field {
		case "peak_active":
			return float64(ctx.PeakActive)
		case "baseline_active":
			return ctx.BaselineActive
		case "duration_sec":
			return ctx.DurationSec
		}
	}
	return 0
}

// GetStr returns a string value from the given source and field.
// Returns "" if not found.
func (ctx *EvalContext) GetStr(source, field string) string {
	switch source {
	case "metrics":
		if m, ok := ctx.Metrics[field]; ok {
			return m.Trend
		}
	case "summary":
		switch field {
		case "db_version":
			return ""
		}
	}
	return ""
}

// GetWaitPct returns the percentage of a specific wait event from the wait profile.
// Returns 0 if not found.
func (ctx *EvalContext) GetWaitPct(event string) float64 {
	for _, b := range ctx.WaitProfile {
		if b.WaitEvent == event || b.WaitEventType == event {
			return b.Percentage
		}
	}
	return 0
}

// WaitPct is an alias for GetWaitPct.
func (ctx *EvalContext) WaitPct(event string) float64 {
	return ctx.GetWaitPct(event)
}

// WaitAvgMs returns 0 for OpenGauss (wait_status doesn't carry timing info).
func (ctx *EvalContext) WaitAvgMs(_ string) float64 {
	return 0
}

// HasBlockingChains returns true if any blocking chains were detected.
func (ctx *EvalContext) HasBlockingChains() bool {
	return len(ctx.BlockingChains) > 0
}

// RuleCount returns the number of rules loaded in the engine.
func (e *Engine) RuleCount() int {
	return len(e.rules)
}

// Rules returns all loaded rules.
func (e *Engine) Rules() []*Rule {
	return e.rules
}

// ExecuteQuery runs a predefined diagnostic query and caches the result.
func (ctx *EvalContext) ExecuteQuery(qid QueryID, params map[string]string) (interface{}, error) {
	if cached, ok := ctx.QueryResults[qid]; ok {
		return cached, nil
	}
	if ctx.executor == nil {
		return nil, nil
	}
	result, err := ctx.executor.Execute(qid, params)
	if err != nil {
		return nil, err
	}
	ctx.QueryResults[qid] = result
	return result, nil
}

// ─── Engine ──────────────────────────────────────────────────────────────────

// Engine is the deterministic, decision-tree-based diagnosis engine.
// It evaluates rules without LLM and serves as fallback when LLM
// chain-of-thought fails to identify the root cause.
type Engine struct {
	rules    []*Rule
	index    *SignalIndex
	config   Config
	executor QueryExecutor
}

// New creates an Engine from a rule provider, query executor, and config.
func New(provider RuleProvider, executor QueryExecutor, cfg Config) *Engine {
	rules := provider.Rules()
	return &Engine{
		rules:    rules,
		index:    buildIndex(rules),
		config:   cfg,
		executor: executor,
	}
}

// Diagnose runs the full 4-stage diagnosis pipeline and returns the output.
//
// Stage 1: Extract signals from input -> index lookup -> candidate rules
// Stage 2: Evaluate trigger conditions -> matched rules
// Stage 3: Evaluate decision trees -> diagnosis results
// Stage 4: Resolve conflicts -> DiagOutput (1-2 core causes)
func (e *Engine) Diagnose(input *DiagInput) *DiagOutput {
	if input == nil {
		return &DiagOutput{}
	}

	// Stage 1: extract signals and find candidate rules
	signals := extractSignals(input)
	if len(signals) == 0 {
		return &DiagOutput{}
	}
	candidates := e.index.Match(signals)
	if len(candidates) == 0 {
		return &DiagOutput{}
	}

	// Build evaluation context
	ctx := buildEvalContext(input, e.executor, e.config)

	// Stage 2: evaluate trigger conditions to filter candidates
	matched := evaluateTriggers(candidates, ctx)
	if len(matched) == 0 {
		return &DiagOutput{}
	}

	// Stage 3: evaluate decision trees to produce diagnoses
	results := evaluateTrees(matched, ctx, e.config)
	if len(results) == 0 {
		return &DiagOutput{}
	}

	// Stage 4: resolve conflicts and focus on 1-2 core causes
	return Resolve(results, e.rules)
}

// DiagnoseDebug runs the diagnosis pipeline and returns debug info at each stage.
func (e *Engine) DiagnoseDebug(input *DiagInput) (output *DiagOutput, debug string) {
	if input == nil {
		return &DiagOutput{}, "输入为空"
	}

	var b strings.Builder

	// Stage 1: extract signals
	signals := extractSignals(input)
	b.WriteString(fmt.Sprintf("── 信号提取 (%d 个) ──\n", len(signals)))
	for i, s := range signals {
		typeName := [...]string{"WaitEvent", "ErrorCode", "Metric", "Category", "Keyword"}[s.Type]
		b.WriteString(fmt.Sprintf("  %d. [%s] %q\n", i+1, typeName, s.Key))
	}

	if len(signals) == 0 {
		return &DiagOutput{}, b.String() + "\n⚠ 无信号提取，终止\n"
	}

	// Stage 1b: index lookup
	candidates := e.index.Match(signals)
	b.WriteString(fmt.Sprintf("\n── 候选规则 (%d 条) ──\n", len(candidates)))
	for i, r := range candidates {
		b.WriteString(fmt.Sprintf("  %d. [%s] %s (%s)\n", i+1, r.Category, r.Name, r.ID))
		if i >= 9 {
			b.WriteString(fmt.Sprintf("  ... 还有 %d 条\n", len(candidates)-10))
			break
		}
	}

	if len(candidates) == 0 {
		return &DiagOutput{}, b.String() + "\n⚠ 无候选规则匹配，终止\n"
	}

	// Build context
	ctx := buildEvalContext(input, e.executor, e.config)

	// Stage 2: trigger evaluation
	matched := evaluateTriggers(candidates, ctx)
	b.WriteString(fmt.Sprintf("\n── 触发过滤 (%d → %d) ──\n", len(candidates), len(matched)))
	for i, r := range matched {
		b.WriteString(fmt.Sprintf("  %d. [%s] %s\n", i+1, r.Category, r.Name))
		if i >= 9 {
			break
		}
	}

	if len(matched) == 0 {
		return &DiagOutput{}, b.String() + "\n⚠ 所有候选规则触发条件不满足，终止\n"
	}

	// Stage 3: decision trees
	results := evaluateTrees(matched, ctx, e.config)
	b.WriteString(fmt.Sprintf("\n── 决策树结果 (%d 个诊断) ──\n", len(results)))
	for i, d := range results {
		b.WriteString(fmt.Sprintf("  %d. %s [%s] 置信度=%.0f%%\n", i+1, d.Cause, d.Severity, d.Confidence*100))
		if i >= 9 {
			break
		}
	}

	if len(results) == 0 {
		return &DiagOutput{}, b.String() + "\n⚠ 决策树无输出，终止\n"
	}

	// Stage 4: resolve
	output = Resolve(results, e.rules)
	b.WriteString("\n── 最终输出 ──\n")
	if output.Primary != nil {
		b.WriteString(fmt.Sprintf("  根因: %s\n", output.Primary.Cause))
	}
	if output.Secondary != nil {
		b.WriteString(fmt.Sprintf("  次因: %s\n", output.Secondary.Cause))
	}

	return output, b.String()
}

// ─── Signal extraction ───────────────────────────────────────────────────────

// extractSignals parses the DiagInput to produce a set of signals for index lookup.
func extractSignals(input *DiagInput) []Signal {
	var signals []Signal

	switch input.Type {
	case InputBurstReport:
		signals = extractBurstSignals(input)
	case InputUserQuestion:
		signals = extractQuestionSignals(input.Question)
	case InputErrorCode:
		signals = extractErrorSignals(input.Error)
	case InputHealthCheck:
		// Health check matches all category signals
		signals = append(signals,
			Signal{Type: SignalCategory, Key: "vacuum"},
			Signal{Type: SignalCategory, Key: "lock"},
			Signal{Type: SignalCategory, Key: "wal"},
			Signal{Type: SignalCategory, Key: "connection"},
			Signal{Type: SignalCategory, Key: "replication"},
			Signal{Type: SignalCategory, Key: "sql_perf"},
			Signal{Type: SignalCategory, Key: "memory"},
			Signal{Type: SignalCategory, Key: "checkpoint"},
		)
	}

	return signals
}

// extractBurstSignals extracts signals from a sentinel BurstReport.
func extractBurstSignals(input *DiagInput) []Signal {
	report, ok := input.Report.(*sentinel.BurstReport)
	if !ok || report == nil {
		return nil
	}

	var signals []Signal
	categorySet := make(map[string]bool)
	addCategory := func(cat string) {
		if !categorySet[cat] {
			categorySet[cat] = true
			signals = append(signals, Signal{Type: SignalCategory, Key: cat})
		}
	}

	// ── Metric signals ──
	// Emit signals for all non-zero metrics (JSON rules use metric names as signals).
	for name, summary := range report.Metrics {
		if summary.Max > 0 || summary.Avg > 0 {
			signals = append(signals, Signal{
				Type: SignalMetric,
				Key:  name,
			})
		}
	}

	// ── Wait event signals from WaitProfile ──
	for _, b := range report.WaitProfile {
		if b.WaitEvent != "" && b.WaitEvent != "Running" && b.WaitEvent != "CPU" {
			signals = append(signals, Signal{
				Type: SignalWaitEvent,
				Key:  b.WaitEvent,
			})
		}
	}

	// ── Category from sentinel classification ──
	if report.Classification.Cause.IsValid() && report.Classification.Cause != sentinel.CauseUnknown {
		cat := rootCauseToCategory(report.Classification.Cause)
		if cat != "" {
			addCategory(cat)
		}
	}

	// ── Category inference from metrics ──
	inferCategoriesFromMetrics(report.Metrics, addCategory)

	// ── Category inference from blocking chains ──
	if len(report.BlockingChains) > 0 {
		addCategory("lock")
	}

	// ── Category inference from wait profile ──
	for _, b := range report.WaitProfile {
		if strings.Contains(strings.ToLower(b.WaitEvent), "lock") {
			addCategory("lock")
		}
	}

	// ── Broad category signals for live report ──
	if len(report.Metrics) > 0 {
		for _, cat := range []string{
			"vacuum", "lock", "wal", "connection", "replication",
			"checkpoint", "sql_perf", "memory", "io_storage",
			"query_optimization", "performance", "monitoring",
			"maintenance", "concurrency", "configuration",
			"high_availability", "storage", "operations",
		} {
			addCategory(cat)
		}
	}

	return signals
}

// isPercentageMetric returns true for metrics that represent percentages (0-100).
func isPercentageMetric(name string) bool {
	return strings.HasSuffix(name, "_pct") ||
		strings.HasSuffix(name, "_hit_pct") ||
		strings.HasSuffix(name, "_free_pct") ||
		strings.HasSuffix(name, "_used_pct") ||
		strings.Contains(name, "_limit_pct")
}

// inferCategoriesFromMetrics derives category signals from metric patterns.
func inferCategoriesFromMetrics(metrics map[string]sentinel.MetricSummary, addCategory func(string)) {
	for name, summary := range metrics {
		if summary.Trend != "spike" && summary.Trend != "rising" {
			continue
		}
		switch {
		case strings.Contains(name, "lock") || strings.Contains(name, "deadlock") || strings.Contains(name, "blocker"):
			addCategory("lock")
		case strings.Contains(name, "wal") || strings.Contains(name, "checkpoint"):
			addCategory("wal")
			addCategory("checkpoint")
		case strings.Contains(name, "replication") || strings.Contains(name, "lag"):
			addCategory("replication")
		case strings.Contains(name, "connection") || strings.Contains(name, "active_session"):
			addCategory("connection")
		case strings.Contains(name, "dead_tuple") || strings.Contains(name, "xid_age"):
			addCategory("vacuum")
		case strings.Contains(name, "cache_hit") || strings.Contains(name, "temp_bytes"):
			addCategory("memory")
		case strings.Contains(name, "long_quer"):
			addCategory("sql_perf")
		}
	}
}

// extractQuestionSignals extracts keyword signals from a user question.
func extractQuestionSignals(question string) []Signal {
	if question == "" {
		return nil
	}

	var signals []Signal
	lower := strings.ToLower(question)

	words := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '?' || r == '!' ||
			r == ';' || r == ':' || r == '\n' || r == '\t'
	})

	for _, w := range words {
		if len(w) >= 3 {
			signals = append(signals, Signal{
				Type: SignalKeyword,
				Key:  w,
			})
		}
	}

	return signals
}

// extractErrorSignals extracts signals from an error code.
func extractErrorSignals(errCode string) []Signal {
	if errCode == "" {
		return nil
	}

	upper := strings.ToUpper(strings.TrimSpace(errCode))
	signals := []Signal{
		{Type: SignalErrorCode, Key: upper},
		{Type: SignalKeyword, Key: strings.ToLower(upper)},
	}

	return signals
}

// rootCauseToCategory maps sentinel root cause types to rule engine categories.
func rootCauseToCategory(cause sentinel.RootCauseType) string {
	switch cause {
	case sentinel.CauseSlowQuery:
		return "sql_perf"
	case sentinel.CauseLockContention:
		return "lock"
	case sentinel.CauseVacuumLag:
		return "vacuum"
	case sentinel.CauseXIDWraparound:
		return "vacuum"
	case sentinel.CauseWALBottleneck:
		return "wal"
	case sentinel.CauseConnectionStorm:
		return "connection"
	case sentinel.CauseReplicationLag:
		return "replication"
	case sentinel.CauseCheckpointStorm:
		return "checkpoint"
	default:
		return ""
	}
}

// ─── EvalContext builder ─────────────────────────────────────────────────────

// buildEvalContext constructs an EvalContext from a DiagInput.
func buildEvalContext(input *DiagInput, executor QueryExecutor, cfg Config) *EvalContext {
	ctx := &EvalContext{
		Input:        input,
		Metrics:      make(map[string]sentinel.MetricSummary),
		QueryResults: make(map[QueryID]interface{}),
		executor:     executor,
		config:       cfg,
	}

	if input.Type == InputBurstReport {
		report, ok := input.Report.(*sentinel.BurstReport)
		if ok && report != nil {
			ctx.Metrics = report.Metrics
			ctx.PeakActive = report.PeakActive
			ctx.BaselineActive = report.BaselineActive
			ctx.DurationSec = report.DurationSec
			ctx.TopSQLs = report.TopSQLs
			ctx.WaitProfile = report.WaitProfile
			ctx.BlockingChains = report.BlockingChains
		}
	}

	return ctx
}

// ─── Decision tree evaluation ────────────────────────────────────────────────

// evaluateTrees runs the decision tree for each matched rule and produces diagnoses.
func evaluateTrees(matched []*Rule, ctx *EvalContext, cfg Config) []*Diagnosis {
	var results []*Diagnosis

	for _, rule := range matched {
		diag := evaluateOneTree(rule, ctx, cfg.MaxTreeDepth)
		if diag != nil {
			results = append(results, diag)
		}
	}

	return results
}

// evaluateOneTree runs a single rule's decision tree and returns a Diagnosis.
func evaluateOneTree(rule *Rule, ctx *EvalContext, maxDepth int) *Diagnosis {
	if rule.Tree == nil {
		return &Diagnosis{
			RuleID:     rule.ID,
			RuleName:   rule.Name,
			Cause:      rule.Name,
			Severity:   SeverityMedium,
			Confidence: 0.5,
		}
	}

	diag := &Diagnosis{
		RuleID:   rule.ID,
		RuleName: rule.Name,
	}

	walkTree(rule.Tree, ctx, diag, maxDepth, 0)

	if diag.Severity == 0 {
		diag.Severity = SeverityMedium
	}
	if diag.Confidence == 0 {
		diag.Confidence = computeConfidence(diag)
	}
	if diag.Cause == "" {
		diag.Cause = rule.Name
	}

	return diag
}

// walkTree recursively traverses the decision tree, collecting findings and actions.
func walkTree(node *TreeNode, ctx *EvalContext, diag *Diagnosis, maxDepth, depth int) {
	if node == nil || depth >= maxDepth {
		return
	}

	var value interface{}

	if node.Check != nil {
		value = node.Check(ctx)
	} else if node.Query != "" {
		result, err := ctx.ExecuteQuery(node.Query, nil)
		if err != nil {
			value = nil
		} else {
			value = result
		}
	}

	if node.EliminationMethod {
		walkTreeElimination(node.Branches, value, ctx, diag, maxDepth, depth)
	} else {
		for _, branch := range node.Branches {
			if branch.Match == nil || branch.Match(value) {
				collectBranch(branch, ctx, diag, maxDepth, depth)
				return
			}
		}
	}
}

// collectBranch processes a matched branch.
func collectBranch(branch Branch, ctx *EvalContext, diag *Diagnosis, maxDepth, depth int) {
	diag.Findings = append(diag.Findings, branch.Findings...)
	diag.Actions = append(diag.Actions, branch.Actions...)

	if branch.Severity != 0 {
		diag.Severity = branch.Severity
	}

	if branch.Label != "" {
		if diag.Cause == "" || branch.Severity != 0 {
			diag.Cause = branch.Label
		}
	}

	for _, f := range branch.Findings {
		if c := ParseConfidence(f.Desc); c > 0 && c > diag.Confidence {
			diag.Confidence = c
		}
	}

	if branch.Then != nil {
		walkTree(branch.Then, ctx, diag, maxDepth, depth+1)
	}
}

// walkTreeElimination evaluates all branches (排除法).
func walkTreeElimination(branches []Branch, value interface{}, ctx *EvalContext, diag *Diagnosis, maxDepth, depth int) {
	var matchedBranch *Branch

	for i := range branches {
		branch := &branches[i]
		if branch.Match == nil || branch.Match(value) {
			matchedBranch = branch
		} else {
			if branch.Label != "" {
				diag.Findings = append(diag.Findings, Finding{
					Desc: "已排除: " + branch.Label,
				})
			}
		}
	}

	if matchedBranch != nil {
		collectBranch(*matchedBranch, ctx, diag, maxDepth, depth)
	}
}

// computeConfidence estimates confidence based on evidence quantity, root cause
// identification, elimination method usage, and actionable remediation steps.
func computeConfidence(diag *Diagnosis) float64 {
	findingCount := len(diag.Findings)

	// Base confidence from evidence quantity.
	var base float64
	switch {
	case findingCount >= 5:
		base = 0.90
	case findingCount >= 4:
		base = 0.85
	case findingCount >= 3:
		base = 0.75
	case findingCount >= 2:
		base = 0.65
	case findingCount >= 1:
		base = 0.55
	default:
		return 0.4
	}

	// Boost for root cause identification in findings.
	// JSON rules embed root cause as "根因: name — desc".
	rootCauseBoost := 0.0
	eliminatedCount := 0
	for _, f := range diag.Findings {
		if strings.HasPrefix(f.Desc, "根因: ") {
			rootCauseBoost = 0.08
		}
		if strings.HasPrefix(f.Desc, "已排除: ") {
			eliminatedCount++
		}
	}

	// Boost for elimination method: more eliminated alternatives = higher confidence.
	eliminationBoost := 0.0
	if eliminatedCount >= 3 {
		eliminationBoost = 0.05
	} else if eliminatedCount >= 1 {
		eliminationBoost = 0.02
	}

	// Boost for actionable remediation steps.
	actionBoost := 0.0
	for _, a := range diag.Actions {
		if a.RawSQL != "" || a.SkillCommand != "" {
			actionBoost = 0.03
			break
		}
	}

	confidence := base + rootCauseBoost + eliminationBoost + actionBoost
	if confidence > 0.98 {
		confidence = 0.98
	}
	return confidence
}
