/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Package ruleengine provides a deterministic, decision-tree-based
 *	  database diagnosis engine for MySQL. It works without LLM and
 *	  serves as fallback when LLM chain-of-thought fails to identify the
 *	  root cause.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/types.go
 *
 *-------------------------------------------------------------------------
 */
// Package ruleengine provides a deterministic, decision-tree-based database
// diagnosis engine for MySQL. It works without LLM and serves as fallback when
// LLM chain-of-thought fails to identify the root cause.
package ruleengine

// ─── Signal types (for rule indexing) ───────────────────────────────────────

// SignalType classifies what observable event activates a rule.
type SignalType int

const (
	SignalWaitEvent SignalType = iota // MySQL wait event name (performance_schema)
	SignalErrorCode                   // MySQL error code (e.g., 1213, 1205)
	SignalMetric                      // Metric name (from sentinel)
	SignalCategory                    // Broad category tag
	SignalKeyword                     // Keyword for user-question matching
)

// Signal is a single observable event that a rule declares as its trigger.
type Signal struct {
	Type SignalType
	Key  string // e.g., "wait/io/file/innodb/innodb_data_file", "1213", "threads_running"
}

// ─── Trigger conditions ─────────────────────────────────────────────────────

// TriggerMode controls how a rule is activated.
type TriggerMode string

const (
	TriggerAuto   TriggerMode = "auto"   // Activated by sentinel anomaly
	TriggerQuery  TriggerMode = "query"  // Activated by /diag with active queries
	TriggerManual TriggerMode = "manual" // Activated by user keyword match
)

// CondOp is a comparison operator for trigger conditions.
type CondOp string

const (
	OpGT       CondOp = "gt"        // >
	OpLT       CondOp = "lt"        // <
	OpGTE      CondOp = "gte"       // >=
	OpLTE      CondOp = "lte"       // <=
	OpEQ       CondOp = "eq"        // ==
	OpNE       CondOp = "ne"        // !=
	OpPctGT    CondOp = "pct_gt"    // percentage >
	OpPctLT    CondOp = "pct_lt"    // percentage <
	OpExists   CondOp = "exists"    // field is non-empty/non-zero
	OpNotEmpty CondOp = "not_empty" // slice is non-empty
	OpContains CondOp = "contains"  // string contains
)

// Condition is a single trigger predicate evaluated against EvalContext.
type Condition struct {
	Source string  // "wait_profile" / "metrics" / "blocking_chains" / "top_sqls"
	Field  string  // event name, metric name, etc.
	Op     CondOp  // comparison operator
	Value  float64 // threshold value
}

// SkipCondition defines when a rule should NOT activate even if signals match.
type SkipCondition struct {
	Desc  string                     // human-readable reason
	Check func(ctx *EvalContext) bool // returns true to skip
}

// Trigger defines the full activation condition for a rule.
type Trigger struct {
	Mode       TriggerMode
	Conditions []Condition     // all must be true (AND)
	SkipWhen   []SkipCondition // any true → skip rule
}

// ─── Decision tree ──────────────────────────────────────────────────────────

// QueryID identifies a predefined query that the evaluator can execute.
type QueryID string

// Common query IDs used across MySQL rules.
const (
	QueryProcesslist      QueryID = "processlist"
	QueryInnoDBStatus     QueryID = "innodb_status"
	QueryLockWaits        QueryID = "lock_waits"
	QuerySlowQueries      QueryID = "slow_queries"
	QueryTableStats       QueryID = "table_stats"
	QueryIndexStats       QueryID = "index_stats"
	QueryInnoDBMetrics    QueryID = "innodb_metrics"
	QueryReplicationStatus QueryID = "replication_status"
	QueryGlobalVariables  QueryID = "global_variables"
	QueryGlobalStatus     QueryID = "global_status"
)

// TreeNode is one step in a decision tree.
type TreeNode struct {
	Step              string                            // human-readable description of this step
	Check             func(ctx *EvalContext) interface{} // compute from existing data (no DB query)
	Query             QueryID                           // execute a predefined DB query
	Branches          []Branch                          // evaluate branches in order
	EliminationMethod bool                              // if true, evaluate all branches (排除法)
}

// Branch is one possible path from a TreeNode.
type Branch struct {
	Match    func(value interface{}) bool // condition to take this branch
	Label    string                       // human-readable branch label
	Then     *TreeNode                    // subtree (nil = leaf node)
	Severity Severity                     // override severity (0 = inherit)
	Findings []Finding                    // evidence collected at this branch
	Actions  []Action                     // remediation steps at this branch
}

// ─── Match helpers ──────────────────────────────────────────────────────────

// MatchEquals returns a matcher that checks string equality.
func MatchEquals(want string) func(interface{}) bool {
	return func(v interface{}) bool {
		s, ok := v.(string)
		return ok && s == want
	}
}

// MatchGT returns a matcher that checks float64 > threshold.
func MatchGT(threshold float64) func(interface{}) bool {
	return func(v interface{}) bool {
		f, ok := toFloat(v)
		return ok && f > threshold
	}
}

// MatchLT returns a matcher that checks float64 < threshold.
func MatchLT(threshold float64) func(interface{}) bool {
	return func(v interface{}) bool {
		f, ok := toFloat(v)
		return ok && f < threshold
	}
}

// MatchGTE returns a matcher that checks float64 >= threshold.
func MatchGTE(threshold float64) func(interface{}) bool {
	return func(v interface{}) bool {
		f, ok := toFloat(v)
		return ok && f >= threshold
	}
}

// MatchLTE returns a matcher that checks float64 <= threshold.
func MatchLTE(threshold float64) func(interface{}) bool {
	return func(v interface{}) bool {
		f, ok := toFloat(v)
		return ok && f <= threshold
	}
}

// MatchBetween returns a matcher that checks lo <= value <= hi.
func MatchBetween(lo, hi float64) func(interface{}) bool {
	return func(v interface{}) bool {
		f, ok := toFloat(v)
		return ok && f >= lo && f <= hi
	}
}

// MatchDefault returns a matcher that always matches (catch-all branch).
func MatchDefault() func(interface{}) bool {
	return func(interface{}) bool { return true }
}

// MatchBool returns a matcher that checks for a boolean value.
func MatchBool(want bool) func(interface{}) bool {
	return func(v interface{}) bool {
		b, ok := v.(bool)
		return ok && b == want
	}
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// ─── Severity ───────────────────────────────────────────────────────────────

// Severity indicates the criticality of the diagnosed issue.
type Severity int

const (
	SeverityLow      Severity = 1
	SeverityMedium   Severity = 2
	SeverityHigh     Severity = 3
	SeverityCritical Severity = 4
)

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "低"
	case SeverityMedium:
		return "中"
	case SeverityHigh:
		return "高"
	case SeverityCritical:
		return "严重"
	default:
		return "未知"
	}
}

// Label returns a visual severity indicator.
func (s Severity) Label() string {
	switch s {
	case SeverityLow:
		return "■□□□"
	case SeverityMedium:
		return "■■□□"
	case SeverityHigh:
		return "■■■□"
	case SeverityCritical:
		return "■■■■"
	default:
		return "□□□□"
	}
}

// ─── Finding & Action ───────────────────────────────────────────────────────

// Finding is one piece of evidence in the diagnosis.
type Finding struct {
	Desc string
}

// ActionType categorizes remediation urgency.
type ActionType string

const (
	ActionInvestigate ActionType = "排查"
	ActionUrgent      ActionType = "紧急"
	ActionFix         ActionType = "修复"
	ActionPrevent     ActionType = "预防"
)

// Action is one remediation step.
type Action struct {
	Type         ActionType
	Desc         string // what to do
	SkillCommand string // opendb skill command: /explain {digest}
	RawSQL       string // raw SQL: SHOW ENGINE INNODB STATUS
	Risk         string // risk description
	Rollback     string // rollback steps
}

// ─── Diagnosis (single rule result) ─────────────────────────────────────────

// Diagnosis is the evaluation result of one rule.
type Diagnosis struct {
	RuleID     string
	RuleName   string
	Cause      string   // root cause description
	Severity   Severity
	Confidence float64  // 0.0~1.0
	Findings   []Finding
	Actions    []Action

	// Scoring (computed by resolver)
	Score       float64
	Weight      float64 // normalized weight among all results
	Specificity float64 // how specific the match is

	// Causal chain (computed by resolver)
	IsDownstreamOf string // rule ID of upstream cause
	AbsorbedCount  int    // number of downstream rules absorbed
	AbsorbedNames  []string
}

// ─── DiagOutput (final output to user) ──────────────────────────────────────

// DiagOutput is the final rule engine output, focused on 1-2 core causes.
type DiagOutput struct {
	Primary   *Diagnosis   // core root cause (always present if any rule matched)
	Secondary *Diagnosis   // secondary cause (only when weight is close to primary)
	Absorbed  []*Diagnosis // downstream symptoms absorbed into primary/secondary
}

// ─── Rule ───────────────────────────────────────────────────────────────────

// Rule is a complete diagnostic rule with trigger, decision tree, and causal info.
type Rule struct {
	ID       string
	Name     string
	Category string // "wait_event" / "sql_perf" / "memory" / "io_storage" / "lock" / "replication" / "space" / "innodb" / "emergency" / "session"

	Signals []Signal // observable events that activate this rule
	Trigger Trigger  // full activation condition

	Tree *TreeNode // decision tree root

	// Causal relationships for root-cause focusing
	CausedBy []string // I'm usually a downstream symptom of these rule IDs
	CausesOf []string // I usually trigger these rule IDs

	Tags     []string
	Versions string   // e.g., "5.7+" or "8.0+"
	Related  []string // related rule IDs for cross-reference
}

// ─── Configuration ──────────────────────────────────────────────────────────

// OutputMode controls whether actions show skill commands or raw SQL.
type OutputMode string

const (
	OutputSQL   OutputMode = "sql"   // show raw SQL (default)
	OutputSkill OutputMode = "skill" // show opendb skill commands
)

// Config holds rule engine configuration.
type Config struct {
	OutputMode      OutputMode // "sql" (default) or "skill"
	MaxQueryTimeout int        // max seconds per query (default 5)
	MaxTreeDepth    int        // max decision tree depth (default 20)
}

// DefaultConfig returns production defaults.
func DefaultConfig() Config {
	return Config{
		OutputMode:      OutputSQL,
		MaxQueryTimeout: 5,
		MaxTreeDepth:    20,
	}
}

// ─── Provider ───────────────────────────────────────────────────────────────

// RuleProvider supplies rules to the engine. Community edition provides basic
// rules; enterprise edition provides all expert rules.
type RuleProvider interface {
	Rules() []*Rule
	Version() string
	Edition() string // "community" or "enterprise"
}

// ─── QueryExecutor ──────────────────────────────────────────────────────────

// QueryExecutor executes predefined diagnostic queries against the database.
// The implementation bridges to the opendb skill system.
type QueryExecutor interface {
	Execute(qid QueryID, params map[string]string) (interface{}, error)
}

// ─── DiagInput ──────────────────────────────────────────────────────────────

// InputType classifies the input to the engine.
type InputType int

const (
	InputBurstReport InputType = iota // sentinel anomaly data
	InputUserQuestion                 // user-typed question
	InputErrorCode                    // specific MySQL error
	InputHealthCheck                  // proactive health check
)

// DiagInput is the input to Engine.Diagnose().
type DiagInput struct {
	Type     InputType
	Report   interface{} // *sentinel.BurstReport for InputBurstReport
	Question string      // user question for InputUserQuestion
	Error    string      // error code for InputErrorCode
}
