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
 *	  internal/oracle/ruleengine/engine.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

// ─── EvalContext ─────────────────────────────────────────────────────────────

// EvalContext is the runtime context that decision trees and trigger conditions
// read from. It is populated from the DiagInput before rule evaluation.
type EvalContext struct {
	// Raw input
	Input *DiagInput

	// Parsed from BurstReport
	WaitProfile    []sentinel.WaitBucket             // sorted by TotalMs desc
	Metrics        map[string]sentinel.MetricSummary  // metric name → summary
	TopSQLs        []sentinel.SQLProfile
	BlockingChains []sentinel.BlockingChain
	SpaceDetails   []sentinel.SpaceDetail
	ParamDetails   []sentinel.ParamDetail

	// Scalar summaries for quick condition checks
	PeakActive     int
	BaselineActive float64
	DurationSec    float64

	// Extended fields from BurstReport
	DBVersion    string // e.g., "19.21.0.0.0"
	WorkloadType string // "oltp" or "olap"

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

// GetWaitPct returns the percentage of the given wait event in the wait profile.
// Returns 0 if not found.
func (ctx *EvalContext) GetWaitPct(event string) float64 {
	for _, w := range ctx.WaitProfile {
		if strings.EqualFold(w.Event, event) {
			return w.Percentage
		}
	}
	return 0
}

// WaitPct is an alias for GetWaitPct for concise usage in rule definitions.
func (ctx *EvalContext) WaitPct(event string) float64 {
	return ctx.GetWaitPct(event)
}

// WaitAvgMs returns the average wait time in milliseconds for the named event.
// Returns 0 if no executor is available or the metric is not found.
func (ctx *EvalContext) WaitAvgMs(event string) float64 {
	// Check if we have a metric with _avg_ms suffix
	key := strings.ReplaceAll(strings.ToLower(event), " ", "_") + "_avg_ms"
	if m, ok := ctx.Metrics[key]; ok {
		return m.Avg
	}
	return 0
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
// Source can be "metrics", "ash", "wait_profile", etc.
// Returns 0 if not found.
func (ctx *EvalContext) GetFloat(source, field string) float64 {
	switch source {
	case "metrics":
		if m, ok := ctx.Metrics[field]; ok {
			return m.Avg
		}
	case "wait_profile":
		return ctx.WaitPct(field)
	case "ash":
		// ASH-derived values stored as metrics with ash_ prefix or direct field names
		if m, ok := ctx.Metrics["ash_"+field]; ok {
			return m.Avg
		}
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
	case "params":
		for _, p := range ctx.ParamDetails {
			if strings.EqualFold(p.Name, field) {
				return p.Value
			}
		}
	case "summary":
		switch field {
		case "db_version":
			return ctx.DBVersion
		case "workload_type":
			return ctx.WorkloadType
		}
	}
	return ""
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

// ExtractHardParsePct extracts the hard parse percentage from a QueryParseStats result.
// QueryParseStats returns v$sysstat rows with name/value columns.
// Returns -1 if the result cannot be parsed.
func ExtractHardParsePct(result interface{}) float64 {
	m, ok := result.(map[string]interface{})
	if !ok {
		return -1
	}
	rows, ok := m["rows"].([]map[string]interface{})
	if !ok {
		return -1
	}
	var total, hard float64
	for _, row := range rows {
		name, _ := row["name"].(string)
		val := rowValueToFloat(row["value"])
		switch name {
		case "parse count (total)":
			total = val
		case "parse count (hard)":
			hard = val
		}
	}
	if total > 0 {
		return hard / total * 100
	}
	return 0
}

// rowValueToFloat converts a db row value (string or numeric) to float64.
func rowValueToFloat(v interface{}) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	case string:
		var f float64
		fmt.Sscanf(n, "%f", &f)
		return f
	default:
		return 0
	}
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

	var output *DiagOutput

	if len(matched) == 0 {
		// No rules matched — check fallbacks.
		output = &DiagOutput{}
		if capDiag := checkCapacityFallback(ctx); capDiag != nil {
			output.Primary = capDiag
		} else if rmDiag := checkResourceManagerFallback(ctx); rmDiag != nil {
			output.Primary = rmDiag
		} else if tempDiag := checkTempUndoFallback(ctx); tempDiag != nil {
			output.Primary = tempDiag
		} else if sessDiag := checkSessionLimitFallback(ctx); sessDiag != nil {
			output.Primary = sessDiag
		} else if paramDiag := checkParamFallback(ctx); paramDiag != nil {
			output.Primary = paramDiag
		}
	} else {
		// Stage 3: evaluate decision trees to produce diagnoses
		results := evaluateTrees(matched, ctx, e.config)
		if len(results) == 0 {
			// Rules matched but trees produced nothing → try fallbacks.
			output = &DiagOutput{}
			if capDiag := checkCapacityFallback(ctx); capDiag != nil {
				output.Primary = capDiag
			} else if rmDiag := checkResourceManagerFallback(ctx); rmDiag != nil {
				output.Primary = rmDiag
			} else if tempDiag := checkTempUndoFallback(ctx); tempDiag != nil {
				output.Primary = tempDiag
			} else if sessDiag := checkSessionLimitFallback(ctx); sessDiag != nil {
				output.Primary = sessDiag
			} else if paramDiag := checkParamFallback(ctx); paramDiag != nil {
				output.Primary = paramDiag
			}
		} else {
			// Stage 3.5: boost severity based on affected session percentage
			if len(ctx.WaitProfile) > 0 {
				topWaitPct := ctx.WaitProfile[0].Percentage
				for _, d := range results {
					BoostSeverityByImpact(d, topWaitPct)
				}
			}
			// Stage 4: resolve conflicts
			output = Resolve(results, e.rules)
		}
	}

	// Stage 5: enrich with SQL performance insights (runs on ALL paths with Primary)
	enrichSQLPerfInsights(output, ctx)

	return output
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

	// Stage 3.5: boost severity based on affected session percentage
	if len(ctx.WaitProfile) > 0 {
		topWaitPct := ctx.WaitProfile[0].Percentage
		for _, d := range results {
			BoostSeverityByImpact(d, topWaitPct)
		}
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
			Signal{Type: SignalCategory, Key: "wait_event"},
			Signal{Type: SignalCategory, Key: "sql_perf"},
			Signal{Type: SignalCategory, Key: "memory"},
			Signal{Type: SignalCategory, Key: "io_storage"},
			Signal{Type: SignalCategory, Key: "lock"},
			Signal{Type: SignalCategory, Key: "redo"},
			Signal{Type: SignalCategory, Key: "space"},
			Signal{Type: SignalCategory, Key: "undo"},
			Signal{Type: SignalCategory, Key: "session"},
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

	// ── Wait event signals ──
	// Use low threshold (1%) so trigger conditions do the precise filtering.
	// Previous 5% threshold caused rules with 3% triggers to never become candidates.
	for _, w := range report.WaitProfile {
		if w.Percentage >= 1.0 {
			signals = append(signals, Signal{
				Type: SignalWaitEvent,
				Key:  strings.ToLower(w.Event),
			})
		}
	}

	// ── Metric signals (selective) ──
	// Only emit signals for anomalous metrics, not every metric with Max > 0.
	// This prevents hundreds of false-positive candidate matches.
	triggerMetric := report.TriggerEvent.Metric
	for name, summary := range report.Metrics {
		isAnomalous := summary.Trend == "spike" || summary.Trend == "rising"
		isTrigger := name == triggerMetric
		isHighPct := isPercentageMetric(name) && summary.Max > 80
		if isAnomalous || isTrigger || isHighPct {
			signals = append(signals, Signal{
				Type: SignalMetric,
				Key:  name,
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

	// ── Category inference from wait events ──
	// When Classification is Unknown, infer categories from dominant wait patterns.
	inferCategoriesFromWaits(report.WaitProfile, addCategory)

	// ── Category from blocking chains ──
	if len(report.BlockingChains) > 0 {
		addCategory("lock")
	}

	// ── Category from top SQLs ──
	if len(report.TopSQLs) > 0 {
		addCategory("sql_perf")
	}

	// ── Category from space details ──
	for _, sd := range report.SpaceDetails {
		if sd.UsedPct > 80 {
			addCategory("space")
			break
		}
	}

	// ── Category from undo/temp metrics ──
	if m, ok := report.Metrics[metricUndoUsedPct]; ok && m.Max > 70 {
		addCategory("undo")
	}
	if m, ok := report.Metrics[metricTempUsedPct]; ok && m.Max > 70 {
		addCategory("space")
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

// inferCategoriesFromWaits derives category signals from wait event patterns.
// This provides a fallback when sentinel Classification is Unknown.
func inferCategoriesFromWaits(waits []sentinel.WaitBucket, addCategory func(string)) {
	for _, w := range waits {
		if w.Percentage < 3.0 {
			continue // only infer from meaningful wait events
		}
		lower := strings.ToLower(w.Event)
		switch {
		case strings.Contains(lower, "log file sync") ||
			strings.Contains(lower, "log file parallel write") ||
			strings.Contains(lower, "log buffer space"):
			addCategory("redo")
		case strings.Contains(lower, "db file sequential") ||
			strings.Contains(lower, "db file scattered") ||
			strings.Contains(lower, "db file parallel") ||
			strings.Contains(lower, "direct path read"):
			addCategory("io_storage")
		case strings.Contains(lower, "latch") ||
			strings.Contains(lower, "mutex") ||
			strings.Contains(lower, "cursor: pin") ||
			strings.Contains(lower, "library cache"):
			addCategory("memory")
		case strings.Contains(lower, "enq: tx") ||
			strings.Contains(lower, "enq: tm") ||
			strings.Contains(lower, "row lock"):
			addCategory("lock")
		case strings.Contains(lower, "direct path write temp") ||
			strings.Contains(lower, "direct path read temp"):
			addCategory("space")
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

	// Split on common delimiters to get individual words/phrases
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '?' || r == '!' ||
			r == ';' || r == ':' || r == '\n' || r == '\t'
	})

	for _, w := range words {
		if len(w) >= 3 { // skip very short tokens
			signals = append(signals, Signal{
				Type: SignalKeyword,
				Key:  w,
			})
		}
	}

	// Check for ORA error codes in the question
	if idx := strings.Index(lower, "ora-"); idx >= 0 {
		// Extract ORA-XXXXX pattern
		end := idx + 4
		for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
			end++
		}
		if end > idx+4 {
			signals = append(signals, Signal{
				Type: SignalErrorCode,
				Key:  strings.ToUpper(question[idx:end]),
			})
		}
	}

	return signals
}

// extractErrorSignals extracts signals from an ORA error code.
func extractErrorSignals(errCode string) []Signal {
	if errCode == "" {
		return nil
	}

	upper := strings.ToUpper(strings.TrimSpace(errCode))
	signals := []Signal{
		{Type: SignalErrorCode, Key: upper},
	}

	// Also add keyword signal for the error code
	signals = append(signals, Signal{
		Type: SignalKeyword,
		Key:  strings.ToLower(upper),
	})

	return signals
}

// rootCauseToCategory maps sentinel root cause types to rule engine categories.
func rootCauseToCategory(cause sentinel.RootCauseType) string {
	switch cause {
	case sentinel.CauseBadSQL:
		return "sql_perf"
	case sentinel.CauseIOSubsystem:
		return "io_storage"
	case sentinel.CauseLatchStorm:
		return "memory"
	case sentinel.CauseRedoBottleneck:
		return "redo"
	case sentinel.CauseLockContention:
		return "lock"
	case sentinel.CauseTrafficStorm:
		return "session"
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
			ctx.WaitProfile = report.WaitProfile
			ctx.Metrics = report.Metrics
			ctx.TopSQLs = report.TopSQLs
			ctx.BlockingChains = report.BlockingChains
			ctx.SpaceDetails = report.SpaceDetails
			ctx.ParamDetails = report.ParamDetails
			ctx.PeakActive = report.PeakActive
			ctx.BaselineActive = report.BaselineActive
			ctx.DurationSec = report.DurationSec
			ctx.DBVersion = report.DBVersion
			ctx.WorkloadType = report.WorkloadType
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
		// No tree: produce a basic diagnosis from the rule metadata
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

	// If no severity was set by any branch, default to medium
	if diag.Severity == 0 {
		diag.Severity = SeverityMedium
	}
	// If no confidence was set, default based on findings count
	if diag.Confidence == 0 {
		diag.Confidence = computeConfidence(diag)
	}
	// If no cause was set, use rule name
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

	// Step 1: compute the check value or execute a query
	var value interface{}

	if node.Check != nil {
		value = node.Check(ctx)
	} else if node.Query != "" {
		result, err := ctx.ExecuteQuery(node.Query, nil)
		if err != nil {
			// Query failed; try branches with nil value
			value = nil
		} else {
			value = result
		}
	}

	// Step 2: evaluate branches
	if node.EliminationMethod {
		// Elimination mode: evaluate all branches, report eliminated vs confirmed
		walkTreeElimination(node.Branches, value, ctx, diag, maxDepth, depth)
	} else {
		// Normal mode: take first matching branch
		for _, branch := range node.Branches {
			if branch.Match == nil || branch.Match(value) {
				collectBranch(branch, value, ctx, diag, maxDepth, depth)
				return // only take first matching branch
			}
		}
	}
}

// collectBranch processes a matched branch: collects findings, actions, severity,
// confidence, and recurses into subtree.
func collectBranch(branch Branch, value interface{}, ctx *EvalContext, diag *Diagnosis, maxDepth, depth int) {
	diag.Findings = append(diag.Findings, branch.Findings...)
	diag.Actions = append(diag.Actions, branch.Actions...)

	// If the check value carries dynamic findings (e.g., from SQL Advisor), extract them.
	if ar, ok := value.(*advisorResult); ok && ar != nil {
		diag.Findings = append(diag.Findings, ar.findings...)
		diag.Actions = append(diag.Actions, ar.actions...)
	}

	// Generate dynamic findings from branch function if provided.
	if branch.DynFindings != nil {
		diag.Findings = append(diag.Findings, branch.DynFindings(ctx)...)
	}

	if branch.Severity != 0 {
		diag.Severity = branch.Severity
	}

	// Set cause from branch label. Deeper branches with severity (conclusion branches)
	// override shallow intermediate labels.
	if branch.Label != "" {
		if diag.Cause == "" || branch.Severity != 0 {
			diag.Cause = branch.Label
		}
	}

	// Extract confidence from findings (JSON rules embed "置信度 XX%")
	for _, f := range branch.Findings {
		if c := ParseConfidence(f.Desc); c > 0 && c > diag.Confidence {
			diag.Confidence = c
		}
	}

	if branch.Then != nil {
		walkTree(branch.Then, ctx, diag, maxDepth, depth+1)
	}
}

// walkTreeElimination evaluates all branches to implement 排除法 (elimination method).
// Non-matching branches are reported as "eliminated", the last matching (or default) wins.
func walkTreeElimination(branches []Branch, value interface{}, ctx *EvalContext, diag *Diagnosis, maxDepth, depth int) {
	var matchedBranch *Branch

	for i := range branches {
		branch := &branches[i]
		if branch.Match == nil || branch.Match(value) {
			// This branch matched — could be the confirmed root cause
			matchedBranch = branch
		} else {
			// This branch was eliminated — add as evidence
			if branch.Label != "" {
				diag.Findings = append(diag.Findings, Finding{
					Desc: "已排除: " + branch.Label,
				})
			}
		}
	}

	// Process the last matched branch (or none)
	if matchedBranch != nil {
		collectBranch(*matchedBranch, value, ctx, diag, maxDepth, depth)
	}
}

// computeConfidence estimates confidence based on the amount of evidence collected.
// This is the fallback when no explicit confidence is set by JSON rules.
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

// checkCapacityFallback generates a diagnosis when no rules matched but
// tablespace capacity issues exist in SpaceDetails (e.g., idle DB with full tablespace).
func checkCapacityFallback(ctx *EvalContext) *Diagnosis {
	if len(ctx.SpaceDetails) == 0 {
		return nil
	}

	var criticalSpaces []string
	var highSpaces []string
	for _, sd := range ctx.SpaceDetails {
		if sd.UsedPct >= 95 {
			criticalSpaces = append(criticalSpaces, fmt.Sprintf("%s (%.1f%%)", sd.Name, sd.UsedPct))
		} else if sd.UsedPct >= 85 {
			highSpaces = append(highSpaces, fmt.Sprintf("%s (%.1f%%)", sd.Name, sd.UsedPct))
		}
	}

	if len(criticalSpaces) == 0 && len(highSpaces) == 0 {
		return nil
	}

	sev := SeverityHigh
	if len(criticalSpaces) > 0 {
		sev = SeverityCritical
	}

	findings := []Finding{}
	if len(criticalSpaces) > 0 {
		findings = append(findings, Finding{Desc: "表空间使用率 >= 95%（紧急）: " + strings.Join(criticalSpaces, ", ")})
	}
	if len(highSpaces) > 0 {
		findings = append(findings, Finding{Desc: "表空间使用率 >= 85%（预警）: " + strings.Join(highSpaces, ", ")})
	}

	return &Diagnosis{
		RuleID:     "CAPACITY_CHECK",
		RuleName:   "表空间容量告警",
		Cause:      "表空间容量不足",
		Severity:   sev,
		Confidence: 0.90,
		Findings:   findings,
		Actions: []Action{
			{Type: ActionUrgent, Desc: "检查表空间并扩展", SkillCommand: "/space check",
				RawSQL: "SELECT tablespace_name, ROUND(used_percent,1) used_pct FROM dba_tablespace_usage_metrics WHERE used_percent > 85 ORDER BY used_percent DESC"},
			{Type: ActionFix, Desc: "清理历史数据或添加数据文件",
				RawSQL: "ALTER TABLESPACE {ts_name} ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G;"},
		},
		Specificity: 0.80,
		Score:       severityWeight(sev) * 0.90 * 0.80,
		Weight:      1.0,
	}
}

// checkSessionLimitFallback detects when sessions or processes approach their limits.
func checkSessionLimitFallback(ctx *EvalContext) *Diagnosis {
	sessPct := ctx.MetricValue("sessions_used_pct")
	procPct := ctx.MetricValue("processes_used_pct")

	// Trigger on percentage (>60%) OR high absolute count from active sessions.
	activeCount := ctx.MetricValue("active_sessions")
	highAbsolute := activeCount > 50

	if sessPct < 60 && procPct < 60 && !highAbsolute {
		return nil
	}

	sev := SeverityMedium
	if sessPct >= 80 || procPct >= 80 {
		sev = SeverityHigh
	}
	if sessPct >= 95 || procPct >= 95 {
		sev = SeverityCritical
	}

	var findings []Finding
	if sessPct >= 60 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("会话使用率 %.1f%%，需关注 sessions 参数配置", sessPct)})
	}
	if procPct >= 60 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("进程使用率 %.1f%%，需关注 processes 参数配置", procPct)})
	}
	if highAbsolute && sessPct < 60 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("当前用户会话数较多（活跃 %.0f），检查是否有连接泄漏", activeCount)})
	}

	return &Diagnosis{
		RuleID:     "SESSION_LIMIT",
		RuleName:   "会话/进程接近上限",
		Cause:      "会话或连接数异常",
		Severity:   sev,
		Confidence: 0.85,
		Findings:   findings,
		Actions: []Action{
			{Type: ActionUrgent, Desc: "检查连接来源，是否有连接泄漏",
				RawSQL: "SELECT username, machine, program, COUNT(*) cnt FROM v$session WHERE type='USER' GROUP BY username, machine, program ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY"},
			{Type: ActionFix, Desc: "增大 sessions/processes 参数",
				RawSQL: "ALTER SYSTEM SET PROCESSES=500 SCOPE=SPFILE;\nALTER SYSTEM SET SESSIONS=600 SCOPE=SPFILE;\n-- 需要重启数据库生效"},
		},
		Specificity: 0.80,
		Score:       severityWeight(sev) * 0.85 * 0.80,
		Weight:      1.0,
	}
}

// checkParamFallback detects obviously misconfigured key parameters.
func checkParamFallback(ctx *EvalContext) *Diagnosis {
	var findings []Finding
	var actions []Action

	// Check UNDO_RETENTION
	if v := ctx.MetricValue("param_undo_retention"); v > 0 && v < 300 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("UNDO_RETENTION = %.0f 秒（偏小），可能导致 ORA-01555", v)})
		actions = append(actions, Action{Type: ActionFix, Desc: "增大 UNDO_RETENTION 到 900 秒以上",
			RawSQL: "ALTER SYSTEM SET UNDO_RETENTION=900 SCOPE=BOTH;"})
	}

	// Check OPEN_CURSORS
	if v := ctx.MetricValue("param_open_cursors"); v > 0 && v < 300 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("OPEN_CURSORS = %.0f（偏小），高并发时可能触发 ORA-01000", v)})
		actions = append(actions, Action{Type: ActionFix, Desc: "增大 OPEN_CURSORS 到 500 以上",
			RawSQL: "ALTER SYSTEM SET OPEN_CURSORS=500 SCOPE=BOTH;"})
	}

	// Check OPTIMIZER_INDEX_COST_ADJ
	if v := ctx.MetricValue("param_optimizer_index_cost_adj"); v > 0 && v != 100 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("OPTIMIZER_INDEX_COST_ADJ = %.0f（非默认值 100），可能影响执行计划选择", v)})
		actions = append(actions, Action{Type: ActionInvestigate, Desc: "评估是否需要恢复默认值",
			RawSQL: "ALTER SYSTEM SET OPTIMIZER_INDEX_COST_ADJ=100 SCOPE=BOTH;"})
	}

	// Check CURSOR_SHARING
	if v := ctx.GetStr("metrics", "param_cursor_sharing"); v == "FORCE" {
		findings = append(findings, Finding{Desc: "CURSOR_SHARING = FORCE（非默认值），可能导致执行计划次优"})
		actions = append(actions, Action{Type: ActionInvestigate, Desc: "评估是否可恢复为 EXACT",
			RawSQL: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH;"})
	}

	// Check DB_FILE_MULTIBLOCK_READ_COUNT
	if v := ctx.MetricValue("param_db_file_multiblock_read_count"); v > 256 {
		findings = append(findings, Finding{Desc: fmt.Sprintf("DB_FILE_MULTIBLOCK_READ_COUNT = %.0f（过大），可能误导优化器偏好全表扫描", v)})
		actions = append(actions, Action{Type: ActionInvestigate, Desc: "考虑恢复默认值（让 Oracle 自动调整）",
			RawSQL: "ALTER SYSTEM RESET DB_FILE_MULTIBLOCK_READ_COUNT SCOPE=BOTH;"})
	}

	// Check FILESYSTEMIO_OPTIONS (for performance)
	if v := ctx.GetStr("metrics", "param_filesystemio_options"); v != "" && v != "SETALL" && v != "setall" {
		findings = append(findings, Finding{Desc: fmt.Sprintf("FILESYSTEMIO_OPTIONS = %s（建议 SETALL 获得最佳 IO 性能）", v)})
		actions = append(actions, Action{Type: ActionFix, Desc: "设置 FILESYSTEMIO_OPTIONS=SETALL",
			RawSQL: "ALTER SYSTEM SET FILESYSTEMIO_OPTIONS='SETALL' SCOPE=SPFILE;"})
	}

	if len(findings) == 0 {
		return nil
	}

	return &Diagnosis{
		RuleID:      "PARAM_CHECK",
		RuleName:    "关键参数配置检查",
		Severity:    SeverityMedium,
		Confidence:  0.75,
		Findings:    findings,
		Actions:     actions,
		Specificity: 0.70,
		Score:       severityWeight(SeverityMedium) * 0.75 * 0.70,
		Weight:      1.0,
	}
}

// checkResourceManagerFallback detects Resource Manager CPU throttling.
func checkResourceManagerFallback(ctx *EvalContext) *Diagnosis {
	pct := ctx.WaitPct("resmgr:cpu quantum")
	if pct < 5 {
		return nil
	}
	sev := SeverityMedium
	if pct >= 30 {
		sev = SeverityCritical
	} else if pct >= 10 {
		sev = SeverityHigh
	}
	return &Diagnosis{
		RuleID:     "RM_CHECK",
		RuleName:   "Resource Manager CPU 限流",
		Cause:      "Resource Manager 限制 CPU 使用",
		Severity:   sev,
		Confidence: 0.85,
		Findings: []Finding{
			{Desc: fmt.Sprintf("resmgr:cpu quantum 占比 %.1f%%，Resource Manager 正在限制 CPU 配额", pct)},
		},
		Actions: []Action{
			{Type: ActionInvestigate, Desc: "检查被限流的消费组",
				RawSQL: "SELECT s.username, s.resource_consumer_group, COUNT(*) cnt FROM v$session s WHERE s.event='resmgr:cpu quantum' AND s.state='WAITING' GROUP BY s.username, s.resource_consumer_group ORDER BY cnt DESC"},
			{Type: ActionFix, Desc: "临时关闭 Resource Manager",
				RawSQL: "ALTER SYSTEM SET RESOURCE_MANAGER_PLAN = '' SCOPE=BOTH;",
				Risk:   "所有消费组限制取消", Rollback: "ALTER SYSTEM SET RESOURCE_MANAGER_PLAN = '原PLAN' SCOPE=BOTH"},
		},
		Specificity: 0.80,
		Score:       severityWeight(sev) * 0.85 * 0.80,
		Weight:      1.0,
	}
}

// checkTempUndoFallback detects TEMP/UNDO tablespace issues via query.
func checkTempUndoFallback(ctx *EvalContext) *Diagnosis {
	// Check for direct path temp waits indicating TEMP issues.
	tempPct := ctx.WaitPct("direct path read temp") + ctx.WaitPct("direct path write temp")
	if tempPct > 5 {
		return &Diagnosis{
			RuleID:     "TEMP_CHECK",
			RuleName:   "TEMP 表空间排序溢出",
			Cause:      "SQL 排序/Hash Join 溢出到 TEMP 表空间",
			Severity:   SeverityHigh,
			Confidence: 0.80,
			Findings: []Finding{
				{Desc: fmt.Sprintf("direct path temp 等待占比 %.1f%%，SQL 操作溢出到磁盘", tempPct)},
				{Desc: "可能是 PGA_AGGREGATE_TARGET 不足或存在大排序/Hash Join"},
			},
			Actions: []Action{
				{Type: ActionInvestigate, Desc: "查看 TEMP 使用详情",
					RawSQL: "SELECT s.sid, s.username, s.sql_id, t.blocks*8/1024 mb_used FROM v$tempseg_usage t JOIN v$session s ON t.session_num=s.serial# AND t.session_addr=s.saddr ORDER BY t.blocks DESC FETCH FIRST 10 ROWS ONLY"},
				{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET",
					RawSQL: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=2G SCOPE=BOTH;",
					Risk:   "占用更多内存"},
			},
			Specificity: 0.75,
			Score:       severityWeight(SeverityHigh) * 0.80 * 0.75,
			Weight:      1.0,
		}
	}
	return nil
}

// enrichSQLPerfInsights adds SQL-level performance findings to the primary diagnosis.
// This runs as a supplementary layer AFTER the resolver, so it never replaces the
// primary wait-event diagnosis — it only adds actionable SQL optimization insights.
func enrichSQLPerfInsights(output *DiagOutput, ctx *EvalContext) {
	if output == nil || output.Primary == nil {
		return
	}

	if len(ctx.TopSQLs) == 0 {
		return
	}

	var sqlFindings []Finding
	var sqlActions []Action

	for _, sql := range ctx.TopSQLs {
		if sql.MaxConcurrent < 3 {
			continue
		}

		execs := sql.Executions
		if execs == 0 {
			execs = 1
		}
		bufPerExec := float64(sql.BufferGets) / float64(execs)
		rowsPerExec := float64(sql.RowsProcessed) / float64(execs)

		// Full table scan on large table with high selectivity → should use index.
		if sql.HasFullScan && bufPerExec > 5000 && rowsPerExec < 100 && rowsPerExec > 0 {
			sqlFindings = append(sqlFindings, Finding{
				Desc: fmt.Sprintf("SQL %s 对大表执行全表扫描但仅返回 %.0f 行/次（逻辑读 %.0f/次），应使用索引",
					sql.SQLID, rowsPerExec, bufPerExec),
			})
			sqlActions = append(sqlActions, Action{
				Type: ActionFix,
				Desc: fmt.Sprintf("检查 SQL %s 的 WHERE 条件列是否有索引", sql.SQLID),
				RawSQL: fmt.Sprintf("SELECT index_name, column_name, column_position FROM user_ind_columns WHERE table_name IN (SELECT object_name FROM v$sql_plan WHERE sql_id='%s' AND operation='TABLE ACCESS' AND options='FULL') ORDER BY index_name, column_position", sql.SQLID),
			})
		}

		// Full table scan on large table, bulk reads → may be OK but note it.
		if sql.HasFullScan && bufPerExec > 5000 && (rowsPerExec >= 100 || rowsPerExec == 0) {
			sqlFindings = append(sqlFindings, Finding{
				Desc: fmt.Sprintf("SQL %s 全表扫描大量数据（逻辑读 %.0f/次），全扫可能合理但频率高时考虑分区或并行",
					sql.SQLID, bufPerExec),
			})
		}

		// Plan drift: multiple plans with significant regression.
		if sql.PlanCount > 1 && sql.BestPlanAvgSec > 0 {
			ratio := sql.CurrentPlanAvgSec / sql.BestPlanAvgSec
			if ratio >= 3.0 {
				sqlFindings = append(sqlFindings, Finding{
					Desc: fmt.Sprintf("SQL %s 执行计划回退：当前计划 %.3fs/次 vs 历史最优 %.3fs/次（慢 %.1f 倍），共 %d 个计划",
						sql.SQLID, sql.CurrentPlanAvgSec, sql.BestPlanAvgSec, ratio, sql.PlanCount),
				})
				sqlActions = append(sqlActions, Action{
					Type: ActionUrgent,
					Desc: fmt.Sprintf("使用 SPM 固定 SQL %s 的历史最优计划", sql.SQLID),
					RawSQL: fmt.Sprintf("SELECT plan_hash_value, executions, ROUND(elapsed_time/GREATEST(executions,1)/1e6,3) avg_sec FROM v$sql WHERE sql_id='%s' ORDER BY avg_sec", sql.SQLID),
				})
			} else if ratio >= 1.5 {
				sqlFindings = append(sqlFindings, Finding{
					Desc: fmt.Sprintf("SQL %s 执行计划轻微回退（慢 %.1f 倍），建议监控", sql.SQLID, ratio),
				})
			}
		}

		// Hot SQL: high concurrent, slow per execution.
		if sql.MaxConcurrent >= 20 && sql.AvgElapsedSec > 1.0 {
			diskPerExec := float64(sql.DiskReads) / float64(execs)
			if diskPerExec > 1000 {
				sqlFindings = append(sqlFindings, Finding{
					Desc: fmt.Sprintf("SQL %s 高并发(%d) + 单次慢(%.1fs) + 物理读多(%.0f/次)，可能缺索引",
						sql.SQLID, sql.MaxConcurrent, sql.AvgElapsedSec, diskPerExec),
				})
			} else if bufPerExec > 10000 {
				sqlFindings = append(sqlFindings, Finding{
					Desc: fmt.Sprintf("SQL %s 高并发(%d) + 单次慢(%.1fs) + 高逻辑读(%.0f/次)，SQL 效率需优化",
						sql.SQLID, sql.MaxConcurrent, sql.AvgElapsedSec, bufPerExec),
				})
			}
		}

		// High frequency, fast SQL → app cache suggestion.
		if sql.MaxConcurrent >= 30 && sql.AvgElapsedSec < 0.1 {
			sqlFindings = append(sqlFindings, Finding{
				Desc: fmt.Sprintf("SQL %s 单次快(%.0fms)但并发极高(%d)，建议应用层缓存减少调用频率",
					sql.SQLID, sql.AvgElapsedSec*1000, sql.MaxConcurrent),
			})
		}
	}

	if len(sqlFindings) == 0 {
		return
	}

	// Append SQL insights to primary diagnosis.
	output.Primary.Findings = append(output.Primary.Findings, Finding{Desc: "── SQL 性能分析 ──"})
	output.Primary.Findings = append(output.Primary.Findings, sqlFindings...)
	output.Primary.Actions = append(output.Primary.Actions, sqlActions...)
}

