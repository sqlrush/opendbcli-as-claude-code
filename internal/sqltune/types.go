/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Neutral, dialect-agnostic types for the /sqltune engine.
 *
 *	  These types are shared across all DB dialects (Oracle / MySQL /
 *	  PostgreSQL / openGauss / GaussDB). Per-dialect implementations
 *	  produce these types via the DialectPlanner interface (dialect.go).
 *
 *	  Historical note: these types originated in
 *	  internal/opengauss/sqltuner/types.go and were extracted here during
 *	  M1 of the multi-DB sqltune refactor. The og package retains type
 *	  aliases for source-compatibility during the transition.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/types.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import "time"

// PlanNode represents one operator in an EXPLAIN tree. Field names are
// chosen to be dialect-neutral; per-dialect collectors map their own
// terminology onto these fields (e.g. Oracle PLAN_TABLE row → PlanNode).
type PlanNode struct {
	Operator        string      `json:"operator"` // "Seq Scan", "Hash Join", "NESTED LOOPS" ...
	RelationName    string      `json:"relation_name,omitempty"`
	Alias           string      `json:"alias,omitempty"`
	StartupCost     float64     `json:"startup_cost"`
	TotalCost       float64     `json:"total_cost"`
	PlanRows        int64       `json:"plan_rows"` // estimate
	PlanWidth       int         `json:"plan_width"`
	ActualStartup   float64     `json:"actual_startup_ms,omitempty"` // ANALYZE-only
	ActualTotal     float64     `json:"actual_total_ms,omitempty"`
	ActualRows      int64       `json:"actual_rows,omitempty"`
	ActualLoops     int64       `json:"actual_loops,omitempty"`
	SharedHit       int64       `json:"shared_hit_blocks,omitempty"`
	SharedRead      int64       `json:"shared_read_blocks,omitempty"`
	Filter          string      `json:"filter,omitempty"`
	JoinFilter      string      `json:"join_filter,omitempty"`
	HashCondition   string      `json:"hash_condition,omitempty"`
	IndexCondition  string      `json:"index_condition,omitempty"`
	SortKey         []string    `json:"sort_key,omitempty"`
	SortMethod      string      `json:"sort_method,omitempty"`     // "external merge" = spill
	SortSpaceType   string      `json:"sort_space_type,omitempty"` // "Memory" / "Disk"
	SortSpaceUsed   int64       `json:"sort_space_used,omitempty"`
	Children        []*PlanNode `json:"children,omitempty"`
}

// PlanInfo captures top-level plan metadata and the root operator.
type PlanInfo struct {
	Root          *PlanNode `json:"root"`
	TotalCost     float64   `json:"total_cost"`
	PlanningTime  float64   `json:"planning_time_ms,omitempty"`
	ExecutionTime float64   `json:"execution_time_ms,omitempty"`
	HasAnalyze    bool      `json:"has_analyze"` // false → estimates only
}

// SchemaInfo aggregates schema/stats/index/FK for tables in the SQL.
type SchemaInfo struct {
	Tables  map[string]*TableInfo  `json:"tables"`
	Indexes map[string][]IndexInfo `json:"indexes"` // key: table name
	Stats   map[string][]ColStat   `json:"stats"`   // key: table name
	FKs     []ForeignKey           `json:"fks"`
}

type TableInfo struct {
	Name    string  `json:"name"`
	Schema  string  `json:"schema"`
	Pages   int64   `json:"pages"`             // pg_class.relpages / dba_tables.blocks
	Tuples  int64   `json:"tuples"`            // pg_class.reltuples / dba_tables.num_rows
	Kind    string  `json:"kind"`              // 'r' table, 'v' view, 'p' partitioned
	Storage string  `json:"storage,omitempty"` // 行存/列存 (OG); IOT/heap (Oracle)
	SizeMB  float64 `json:"size_mb,omitempty"`
}

type IndexInfo struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	Unique     bool     `json:"unique"`
	Primary    bool     `json:"primary"`
	Definition string   `json:"def"`
}

type ColStat struct {
	Table           string  `json:"table"`
	Column          string  `json:"column"`
	NDistinct       float64 `json:"n_distinct"` // negative = ratio of table rows
	NullFrac        float64 `json:"null_frac"`
	AvgWidth        int     `json:"avg_width"`
	MostCommonVals  string  `json:"most_common_vals,omitempty"`
	MostCommonFreqs string  `json:"most_common_freqs,omitempty"`
	Correlation     float64 `json:"correlation"`
}

type ForeignKey struct {
	Table      string `json:"table"`
	Name       string `json:"name"`
	Definition string `json:"def"`
}

// DialectInfo captures DB version, extensions/options, key parameters,
// and feature flags relevant to query planning. Per-dialect collectors
// fill this with whatever their CBO surfaces (work_mem on PG/OG,
// optimizer_mode on Oracle, optimizer_switch on MySQL, etc.).
type DialectInfo struct {
	Version           string            `json:"version"` // e.g. "OpenGauss 5.0.0", "Oracle 19.3.0", "MySQL 8.0.34"
	Extensions        []string          `json:"extensions"`
	Parameters        map[string]string `json:"parameters"`         // CBO-relevant settings
	HighAvailability  bool              `json:"high_availability"`  // replication active
	HasPartitionedTab bool              `json:"has_partitioned_tab"`
	UnsupportedFeats  []string          `json:"unsupported_feats"`
	SupportedFeats    []string          `json:"supported_feats"`
}

// RuntimeInfo captures active sessions / locks snapshot at tuner start
// so the LLM can reason about contention contributing to slow SQL.
type RuntimeInfo struct {
	WaitEvents []WaitEventBucket `json:"wait_events"`
	Locks      []LockEntry       `json:"locks"`
	Degraded   bool              `json:"degraded"` // permission-limited → partial data
}

type WaitEventBucket struct {
	WaitEventType string `json:"wait_event_type"`
	WaitEvent     string `json:"wait_event"`
	Count         int    `json:"count"`
}

type LockEntry struct {
	Relation string `json:"relation"`
	LockType string `json:"lock_type"`
	Mode     string `json:"mode"`
	Granted  bool   `json:"granted"`
}

// MemoryEntry is a relevant historical tuning record retrieved via
// fingerprint similarity from the per-instance memory store.
type MemoryEntry struct {
	Title      string    `json:"title"`
	Type       string    `json:"type"` // incident|solution|preference|workload|pattern
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	Similarity float64   `json:"similarity,omitempty"` // 0..1 fingerprint Jaccard
}

// TraceData carries CBO-decision trace output for dialects that support
// it (Oracle 10053, MySQL optimizer_trace, GaussDB GS_PLAN_TRACE).
// Dialects without rejected-paths dumps (PG/openGauss open source)
// populate only Notes with the reason.
type TraceData struct {
	Available bool   `json:"available"`        // false → trace unsupported / disabled
	Format    string `json:"format"`           // "oracle_10053" | "mysql_json" | "gaussdb_plan_trace" | "none"
	Body      string `json:"body,omitempty"`   // raw trace text or JSON; may be large
	Truncated bool   `json:"truncated,omitempty"`
	Bytes     int    `json:"bytes,omitempty"`
	Notes     string `json:"notes,omitempty"`  // why unavailable / what was used instead
}

// CollectedContext is the full Phase A output fed to Round 1 LLM.
type CollectedContext struct {
	OrigSQL        string        `json:"orig_sql"`
	Plan           *PlanInfo     `json:"plan"`
	Schema         *SchemaInfo   `json:"schema"`
	ExpandedSQL    string        `json:"expanded_sql,omitempty"` // view-expanded SQL
	Dialect        *DialectInfo  `json:"dialect"`
	Runtime        *RuntimeInfo  `json:"runtime"`
	Memory         []MemoryEntry `json:"memory,omitempty"`
	Trace          *TraceData    `json:"trace,omitempty"` // M1: nil until dialect-trace work lands (M2+)
	InvolvedTables []string      `json:"involved_tables"`
	Notes          []string      `json:"notes,omitempty"` // collection-time warnings
}

// Round1Output is the strict JSON the Round 1 LLM must produce.
type Round1Output struct {
	Confidence         float64     `json:"confidence"`
	CBOAnalysis        string      `json:"cbo_analysis"` // G2/G5 soul ability
	Candidates         []Candidate `json:"candidates"`
	ExploredDimensions []string    `json:"explored_dimensions"`
	UncertaintyNotes   []string    `json:"uncertainty_notes,omitempty"`
}

// Candidate is one optimization proposal from Round 1.
type Candidate struct {
	ID           int      `json:"id"`
	Type         string   `json:"type"` // rewrite|index|hint|schema|stats
	SQL          string   `json:"sql"`
	Rationale    string   `json:"rationale"`
	ExpectedGain string   `json:"expected_gain"`
	AppliesTo    []string `json:"applies_to,omitempty"`
	RiskLevel    string   `json:"risk_level"` // low|medium|high
}

// VerifyResult is per-candidate verification outcome (Round 2 input).
type VerifyResult struct {
	CandID     int     `json:"cand_id"`
	OldCost    float64 `json:"old_cost"`
	NewCost    float64 `json:"new_cost,omitempty"` // 0 if not applicable
	Speedup    float64 `json:"speedup,omitempty"`  // OldCost / NewCost
	EquivOK    *bool   `json:"equiv_ok,omitempty"` // nil = not run
	Verifiable bool    `json:"verifiable"`         // false for DDL candidates
	Error      string  `json:"error,omitempty"`
	Note       string  `json:"note,omitempty"`
}

// TuneOptions controls /sqltune behavior. Same shape across dialects;
// per-dialect implementations may ignore irrelevant flags.
type TuneOptions struct {
	SQL          string
	Verify       bool // default true
	MaxRounds    int  // agent loop cap (default 10)
	DryRun       bool // skip EXPLAIN
	Simple       bool // skip M6/M8
	NoAnalyze    bool // force no ANALYZE (auto for 1000+ row SQL)
	ForceAnalyze bool // force ANALYZE (DBA assumes risk)
	SkipUpgrade  bool // 2 rounds only, no auto-upgrade (--fast)
	ForceDeep    bool // skip 4-signal check, go deep directly (--deep)

	// QuickMode (v1.1.31) skips Round 2 LLM markdown generation and
	// renders Round 1 via deterministic fallback. Used with SkipUpgrade
	// to cap total time at 30-90s on large-table SQL where Round 2 +
	// Auto-upgrade tend to hit 600s timeout.
	QuickMode bool

	// EnableTrace requests dialect-level CBO trace (Oracle 10053, MySQL
	// optimizer_trace, GaussDB GS_PLAN_TRACE). Best-effort: degraded
	// silently on dialects/permissions where unavailable. (M2+: wiring;
	// M1: field reserved.)
	EnableTrace bool
}

// FinalReport is what the user sees after /sqltune completes.
type FinalReport struct {
	Markdown string         // primary deliverable
	JSON     map[string]any // optional, for CI consumption
	Stats    *ReportStats
}

type ReportStats struct {
	TotalDuration        time.Duration
	Rounds               int
	CandidateCount       int
	VerifiedCount        int
	BestSpeedup          float64
	UpgradeTriggered     bool
	UpgradeReasons       []string
	CompressionTriggered bool
	CompressionReason    string
}

// PlaceholderError is returned by plan collectors when the input SQL
// contains unsubstitutable placeholders (Oracle :N, PG/OG $N, MySQL ?)
// that prevent EXPLAIN. Per-dialect collectors populate this with the
// detected placeholder style so the routing layer can guide the user
// to the right history source (dbe_perf.statement_history for OG,
// v$sqltext for Oracle, performance_schema.events_statements_history
// for MySQL, etc.).
type PlaceholderError struct {
	SQL            string   // original SQL
	Placeholders   []string // detected placeholders, e.g. ["$1", "$2"] or [":1", ":2"]
	DetectedKind   string   // "pg_dollar" | "oracle_colon" | "qmark"
	Suggestion     string   // user-facing hint where to fetch the literal SQL
	Recoverable    bool     // true → can be retried with substituted SQL
}

func (e *PlaceholderError) Error() string {
	if e == nil {
		return ""
	}
	return e.Suggestion
}
