/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Package sqltuner provides complex SQL performance tuning for
 *	  OpenGauss/GaussDB.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/types.go
 *
 *-------------------------------------------------------------------------
 */
// Package sqltuner provides complex SQL performance tuning for OpenGauss/GaussDB.
//
// Architecture (see docs/sqltune/design-og-sqltuner.md):
//   Phase A (确定性收集) → Round 1 (LLM mega-analysis) → 并行验证 → Round 2 (markdown report)
//
// 7 mechanisms (M5 anti-pattern 砍掉, v0.2 再加):
//   M1 真实 EXPLAIN ANALYZE     — plan_collector.go
//   M2 Schema/Stats 三件套       — schema_collector.go
//   M3 迭代验证闭环              — tuner.go (Round 2)
//   M4 等价性验证                — equiv_verifier.go
//   M6 Memory 历史经验注入       — memory_query.go
//   M7 方言/版本知识             — dialect_context.go
//   M8 运行时上下文              — runtime_context.go
package sqltuner

import "time"

// PlanNode represents one operator in OG EXPLAIN tree.
type PlanNode struct {
	Operator       string  `json:"operator"`         // "Seq Scan", "Hash Join", ...
	RelationName   string  `json:"relation_name,omitempty"`
	Alias          string  `json:"alias,omitempty"`
	StartupCost    float64 `json:"startup_cost"`
	TotalCost      float64 `json:"total_cost"`
	PlanRows       int64   `json:"plan_rows"`        // estimate
	PlanWidth      int     `json:"plan_width"`
	ActualStartup  float64 `json:"actual_startup_ms,omitempty"` // EXPLAIN ANALYZE only
	ActualTotal    float64 `json:"actual_total_ms,omitempty"`
	ActualRows     int64   `json:"actual_rows,omitempty"`
	ActualLoops    int64   `json:"actual_loops,omitempty"`
	SharedHit      int64   `json:"shared_hit_blocks,omitempty"`
	SharedRead     int64   `json:"shared_read_blocks,omitempty"`
	Filter         string  `json:"filter,omitempty"`
	JoinFilter     string  `json:"join_filter,omitempty"`
	HashCondition  string  `json:"hash_condition,omitempty"`
	IndexCondition string  `json:"index_condition,omitempty"`
	SortKey        []string `json:"sort_key,omitempty"`
	SortMethod     string  `json:"sort_method,omitempty"`     // "external merge" = 溢出磁盘
	SortSpaceType  string  `json:"sort_space_type,omitempty"` // "Memory" / "Disk"
	SortSpaceUsed  int64   `json:"sort_space_used,omitempty"`
	Children       []*PlanNode `json:"children,omitempty"`
}

// PlanInfo captures top-level metadata + the root PlanNode.
type PlanInfo struct {
	Root          *PlanNode `json:"root"`
	TotalCost     float64   `json:"total_cost"`
	PlanningTime  float64   `json:"planning_time_ms,omitempty"`
	ExecutionTime float64   `json:"execution_time_ms,omitempty"`
	HasAnalyze    bool      `json:"has_analyze"` // false → 只有估算, 没 actual_rows
}

// SchemaInfo aggregates schema/stats/index/FK for tables involved in the SQL.
type SchemaInfo struct {
	Tables  map[string]*TableInfo  `json:"tables"`
	Indexes map[string][]IndexInfo `json:"indexes"` // key: table name
	Stats   map[string][]ColStat   `json:"stats"`   // key: table name
	FKs     []ForeignKey           `json:"fks"`
}

type TableInfo struct {
	Name      string `json:"name"`
	Schema    string `json:"schema"`
	Pages     int64  `json:"pages"`     // pg_class.relpages
	Tuples    int64  `json:"tuples"`    // pg_class.reltuples
	Kind      string `json:"kind"`      // 'r' (table), 'v' (view), 'p' (partitioned)
	Storage   string `json:"storage,omitempty"` // 行存/列存 (OG)
	SizeMB    float64 `json:"size_mb,omitempty"`
}

type IndexInfo struct {
	Name      string   `json:"name"`
	Columns   []string `json:"columns"`
	Unique    bool     `json:"unique"`
	Primary   bool     `json:"primary"`
	Definition string  `json:"def"`
}

type ColStat struct {
	Table          string  `json:"table"`
	Column         string  `json:"column"`
	NDistinct      float64 `json:"n_distinct"`        // negative = 与表行数比例
	NullFrac       float64 `json:"null_frac"`
	AvgWidth       int     `json:"avg_width"`
	MostCommonVals string  `json:"most_common_vals,omitempty"`
	MostCommonFreqs string `json:"most_common_freqs,omitempty"`
	Correlation    float64 `json:"correlation"`
}

type ForeignKey struct {
	Table        string `json:"table"`
	Name         string `json:"name"`
	Definition   string `json:"def"`
}

// DialectInfo captures OG version, extensions, and key parameters for M7 prompt injection.
type DialectInfo struct {
	Version           string            `json:"version"`            // e.g. "OpenGauss 5.0.0"
	Extensions        []string          `json:"extensions"`
	Parameters        map[string]string `json:"parameters"`         // work_mem, shared_buffers, etc
	HighAvailability  bool              `json:"high_availability"`  // pg_stat_replication 非空
	HasPartitionedTab bool              `json:"has_partitioned_tab"`
	UnsupportedFeats  []string          `json:"unsupported_feats"`  // GIN, INCLUDE, BRIN
	SupportedFeats    []string          `json:"supported_feats"`    // 列存, RETURNING, /*+ */ HINT
}

// RuntimeInfo captures pg_stat_activity + pg_locks snapshot at tuner start.
type RuntimeInfo struct {
	WaitEvents []WaitEventBucket `json:"wait_events"`
	Locks      []LockEntry       `json:"locks"`
	Degraded   bool              `json:"degraded"` // 权限不足 → 部分字段不可见
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

// MemoryEntry is a relevant historical tuning record.
type MemoryEntry struct {
	Title      string    `json:"title"`
	Type       string    `json:"type"`     // incident|solution|preference|workload|pattern
	Content    string    `json:"content"`  // markdown body, may be summarized
	CreatedAt  time.Time `json:"created_at"`
	Similarity float64   `json:"similarity,omitempty"` // (v1.1.30) fingerprint-based score, 0..1
}

// CollectedContext is the full Phase A output, fed into Round 1 LLM prompt.
type CollectedContext struct {
	OrigSQL      string         `json:"orig_sql"`
	Plan         *PlanInfo      `json:"plan"`
	Schema       *SchemaInfo    `json:"schema"`
	ExpandedSQL  string         `json:"expanded_sql,omitempty"` // 视图展开后的 SQL
	Dialect      *DialectInfo   `json:"dialect"`
	Runtime      *RuntimeInfo   `json:"runtime"`
	Memory       []MemoryEntry  `json:"memory,omitempty"`
	InvolvedTables []string     `json:"involved_tables"`
	Notes        []string       `json:"notes,omitempty"` // 收集过程中的告警/降级提示
}

// Round1Output is the strict JSON structure Round 1 LLM must produce.
type Round1Output struct {
	Confidence         float64     `json:"confidence"`
	CBOAnalysis        string      `json:"cbo_analysis"`         // G2/G5 灵魂能力
	Candidates         []Candidate `json:"candidates"`
	ExploredDimensions []string    `json:"explored_dimensions"`
	UncertaintyNotes   []string    `json:"uncertainty_notes,omitempty"`
}

// Candidate is one optimization proposal.
type Candidate struct {
	ID            int      `json:"id"`
	Type          string   `json:"type"` // rewrite|index|hint|schema|stats
	SQL           string   `json:"sql"`  // executable SQL or DDL
	Rationale     string   `json:"rationale"`
	ExpectedGain  string   `json:"expected_gain"`
	AppliesTo     []string `json:"applies_to,omitempty"`
	RiskLevel     string   `json:"risk_level"` // low|medium|high
}

// VerifyResult is per-candidate verification outcome (Round 2 input).
type VerifyResult struct {
	CandID    int     `json:"cand_id"`
	OldCost   float64 `json:"old_cost"`
	NewCost   float64 `json:"new_cost,omitempty"`   // 0 if not applicable
	Speedup   float64 `json:"speedup,omitempty"`    // OldCost/NewCost
	EquivOK   *bool   `json:"equiv_ok,omitempty"`   // nil=not run
	Verifiable bool   `json:"verifiable"`           // false for index/schema/stats DDL
	Error     string  `json:"error,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// TuneOptions controls /sqltune behavior.
type TuneOptions struct {
	SQL          string
	Verify       bool   // 是否跑等价性验证（默认 true）
	MaxRounds    int    // agent loop 上限（默认 10，预留多轮升级）
	DryRun       bool   // 不执行 EXPLAIN（默认 false）
	Simple       bool   // 跳过 M6/M8（默认 false）
	NoAnalyze    bool   // 强制不 ANALYZE（千行 SQL 默认 true）
	ForceAnalyze bool   // 强制 ANALYZE（DBA 自负责）
	SkipUpgrade  bool   // 强制只跑 2 轮, 不自动升级 (--fast 模式)
	ForceDeep    bool   // 强制深度模式 (--deep 模式, 跳过 4 信号检查直接升级)
	QuickMode    bool   // (v1.1.31) 跳过 Round 2 LLM markdown 生成, 直接 fallback render Round 1
	                    // 用于复杂 SQL (10+ 表) 场景: Round 2 + Auto-upgrade 在大表上易撞 600s 超时.
	                    // 配合 SkipUpgrade=true 使用, sqltune 总耗时降为 30-90s (vs 5-15min).
}

// FinalReport is what the user sees.
type FinalReport struct {
	Markdown string         // 主交付
	JSON     map[string]any // 可选给 CI
	Stats    *ReportStats
}

type ReportStats struct {
	TotalDuration        time.Duration
	Rounds               int
	CandidateCount       int
	VerifiedCount        int
	BestSpeedup          float64
	UpgradeTriggered     bool      // 是否触发了自动升级到深度模式
	UpgradeReasons       []string  // 升级原因
	CompressionTriggered bool      // 是否触发了 G7 token 压缩
	CompressionReason    string    // 压缩触发原因
}
