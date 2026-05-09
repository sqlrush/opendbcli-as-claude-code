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
 *	  internal/mysql/ruleengine/engine.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/mysql/sentinel"
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

	// Scalar summaries for quick condition checks
	PeakActive     int
	BaselineActive float64
	DurationSec    float64

	// Query results cache (populated lazily by decision tree steps)
	QueryResults map[QueryID]interface{}

	// SQL Advisor enrichment: precise EXPLAIN-level findings for Top SQL
	AdvisorFindings []AdvisorFinding

	// Reference back to executor for tree queries
	executor QueryExecutor
	config   Config
}

// AdvisorFinding is a simplified SQL Advisor result attached to EvalContext.
type AdvisorFinding struct {
	Digest   string
	SQLText  string
	Category string // access_path, join, predicate, statistics, resource, rewrite
	Severity string // P1, P2, P3
	Summary  string
	Detail   string
	SQL      string // suggested fix SQL
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
		if strings.EqualFold(w.EventName, event) {
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
// Returns 0 if the metric is not found.
func (ctx *EvalContext) WaitAvgMs(event string) float64 {
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
// Source can be "metrics", "wait_profile", etc.
// Returns 0 if not found.
func (ctx *EvalContext) GetFloat(source, field string) float64 {
	switch source {
	case "metrics":
		if m, ok := ctx.Metrics[field]; ok {
			return m.Avg
		}
	case "wait_profile":
		return ctx.WaitPct(field)
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
		// No MySQL-specific summary strings yet
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

// ─── Engine ──────────────────────────────────────────────────────────────────

// Engine is the deterministic, decision-tree-based diagnosis engine for MySQL.
// It evaluates rules without LLM and serves as fallback when LLM
// chain-of-thought fails to identify the root cause.
// AdvisorFunc analyzes a SQL digest and returns findings.
// This allows the rule engine to enrich diagnostics with EXPLAIN-level analysis
// without importing the sqladvisor package directly.
type AdvisorFunc func(digest string) []AdvisorFinding

type Engine struct {
	rules       []*Rule
	index       *SignalIndex
	config      Config
	executor    QueryExecutor
	advisorFunc AdvisorFunc
}

// SetAdvisor attaches a SQL Advisor function for EXPLAIN-level enrichment.
func (e *Engine) SetAdvisor(fn AdvisorFunc) {
	e.advisorFunc = fn
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

	// Enrich with SQL Advisor findings (EXPLAIN-level analysis of Top SQL)
	e.enrichWithAdvisor(ctx)

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
	output := Resolve(results, e.rules)

	// Stage 5: inject advisor findings into primary diagnosis
	e.injectAdvisorFindings(output, ctx)

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
	e.enrichWithAdvisor(ctx)

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

	// Stage 5: inject advisor findings
	e.injectAdvisorFindings(output, ctx)

	b.WriteString("\n── 最终输出 ──\n")
	if output.Primary != nil {
		b.WriteString(fmt.Sprintf("  根因: %s\n", output.Primary.Cause))
		if len(ctx.AdvisorFindings) > 0 {
			b.WriteString(fmt.Sprintf("  SQL Advisor: %d 个精确发现\n", len(ctx.AdvisorFindings)))
		}
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
			Signal{Type: SignalCategory, Key: "replication"},
			Signal{Type: SignalCategory, Key: "space"},
			Signal{Type: SignalCategory, Key: "innodb"},
			Signal{Type: SignalCategory, Key: "session"},
		)
	}

	return signals
}

// MySQL metric name constants.
const (
	metricTmpDiskPct    = "tmp_disk_tables_pct"
	metricConnectionPct = "connections_pct"
	metricHistoryList   = "history_list_length"
)

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
	for _, w := range report.WaitProfile {
		if w.Percentage >= 1.0 {
			signals = append(signals, Signal{
				Type: SignalWaitEvent,
				Key:  strings.ToLower(w.EventName),
			})
		}
	}

	// ── Metric signals (selective) ──
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
	inferCategoriesFromWaits(report.WaitProfile, addCategory)

	// ── Category from blocking chains ──
	if len(report.BlockingChains) > 0 {
		addCategory("lock")
	}

	// ── Category from top SQLs ──
	if len(report.TopSQLs) > 0 {
		addCategory("sql_perf")
	}

	// ── Category from InnoDB history list ──
	if m, ok := report.Metrics[metricHistoryList]; ok && m.Max > 100000 {
		addCategory("innodb")
	}

	// ── Category from connection usage ──
	if m, ok := report.Metrics[metricConnectionPct]; ok && m.Max > 70 {
		addCategory("session")
	}

	// ── Category from tmp disk tables ──
	if m, ok := report.Metrics[metricTmpDiskPct]; ok && m.Max > 70 {
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

// inferCategoriesFromWaits derives category signals from MySQL wait event patterns.
func inferCategoriesFromWaits(waits []sentinel.WaitBucket, addCategory func(string)) {
	for _, w := range waits {
		if w.Percentage < 3.0 {
			continue
		}
		lower := strings.ToLower(w.EventName)
		switch {
		// I/O wait events (performance_schema naming)
		case strings.Contains(lower, "wait/io/file") ||
			strings.Contains(lower, "io/file/innodb") ||
			strings.Contains(lower, "io/file/sql"):
			addCategory("io_storage")

		// Lock wait events
		case strings.Contains(lower, "wait/lock") ||
			strings.Contains(lower, "lock/table") ||
			strings.Contains(lower, "lock/row") ||
			strings.Contains(lower, "wait/synch/mutex/innodb"):
			addCategory("lock")

		// InnoDB internal waits
		case strings.Contains(lower, "innodb/buf") ||
			strings.Contains(lower, "innodb/log") ||
			strings.Contains(lower, "innodb/trx"):
			addCategory("innodb")

		// Replication waits
		case strings.Contains(lower, "wait/synch/mutex/sql/slave") ||
			strings.Contains(lower, "relay_log") ||
			strings.Contains(lower, "binlog"):
			addCategory("replication")

		// Memory/temp table waits
		case strings.Contains(lower, "memory") ||
			strings.Contains(lower, "tmp"):
			addCategory("memory")
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

	// Check for MySQL error codes in the question (e.g., "1213", "ERROR 1205")
	if idx := strings.Index(lower, "error "); idx >= 0 {
		end := idx + 6
		for end < len(lower) && lower[end] >= '0' && lower[end] <= '9' {
			end++
		}
		if end > idx+6 {
			signals = append(signals, Signal{
				Type: SignalErrorCode,
				Key:  question[idx+6 : end],
			})
		}
	}

	return signals
}

// extractErrorSignals extracts signals from a MySQL error code.
func extractErrorSignals(errCode string) []Signal {
	if errCode == "" {
		return nil
	}

	code := strings.TrimSpace(errCode)
	signals := []Signal{
		{Type: SignalErrorCode, Key: code},
	}

	signals = append(signals, Signal{
		Type: SignalKeyword,
		Key:  strings.ToLower(code),
	})

	return signals
}

// rootCauseToCategory maps sentinel root cause types to rule engine categories.
func rootCauseToCategory(cause sentinel.RootCauseType) string {
	switch cause {
	case sentinel.CauseSlowQuery:
		return "sql_perf"
	case sentinel.CauseRowLockContention:
		return "lock"
	case sentinel.CauseBinlogBottleneck:
		return "replication"
	case sentinel.CauseBufferPoolPressure:
		return "memory"
	case sentinel.CauseConnectionStorm:
		return "connection"
	case sentinel.CauseReplicationLag:
		return "replication"
	case sentinel.CauseInnoDBHistory:
		return "innodb"
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
			ctx.PeakActive = report.PeakActive
			ctx.BaselineActive = report.BaselineActive
			ctx.DurationSec = report.DurationSec
		}
	}

	return ctx
}

// enrichWithAdvisor runs the SQL Advisor on top digests and attaches findings to context.
func (e *Engine) enrichWithAdvisor(ctx *EvalContext) {
	if e.advisorFunc == nil || len(ctx.TopSQLs) == 0 {
		return
	}

	// Analyze top 3 SQL digests
	limit := 3
	if len(ctx.TopSQLs) < limit {
		limit = len(ctx.TopSQLs)
	}

	for i := 0; i < limit; i++ {
		digest := ctx.TopSQLs[i].Digest
		if digest == "" {
			continue
		}
		findings := e.advisorFunc(digest)
		ctx.AdvisorFindings = append(ctx.AdvisorFindings, findings...)
	}
}

// injectAdvisorFindings enriches the primary diagnosis with precise SQL Advisor findings.
func (e *Engine) injectAdvisorFindings(output *DiagOutput, ctx *EvalContext) {
	if output == nil || output.Primary == nil || len(ctx.AdvisorFindings) == 0 {
		return
	}

	d := output.Primary

	// Only enrich SQL-performance and general categories
	cat := strings.ToLower(d.RuleID)
	isSQLRule := strings.HasPrefix(cat, "my-006") || strings.HasPrefix(cat, "my-007") ||
		strings.HasPrefix(cat, "my-008") || strings.HasPrefix(cat, "my-038") ||
		strings.HasPrefix(cat, "my-039") || strings.HasPrefix(cat, "my-041") ||
		strings.HasPrefix(cat, "my-076") || strings.HasPrefix(cat, "my-082")

	if !isSQLRule {
		return
	}

	// Add top advisor findings to diagnosis
	added := 0
	for _, af := range ctx.AdvisorFindings {
		if added >= 3 {
			break
		}
		d.Findings = append(d.Findings, Finding{
			Desc: fmt.Sprintf("[SQL Advisor] %s: %s", af.Summary, af.Detail),
		})
		if af.SQL != "" {
			d.Actions = append(d.Actions, Action{
				Type:   ActionFix,
				Desc:   fmt.Sprintf("[SQL Advisor] %s", af.Summary),
				RawSQL: af.SQL,
				Risk:   "需要评估索引对写入的影响",
			})
		}
		added++
	}

	// Boost confidence if advisor confirms the diagnosis
	if added > 0 && d.Confidence < 0.9 {
		d.Confidence += 0.1
		if d.Confidence > 0.95 {
			d.Confidence = 0.95
		}
	}
}

// GetAdvisorFindings returns the advisor findings attached to the context.
func (ctx *EvalContext) GetAdvisorFindings() []AdvisorFinding {
	return ctx.AdvisorFindings
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

// walkTreeElimination evaluates all branches to implement 排除法 (elimination method).
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
