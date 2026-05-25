/*-------------------------------------------------------------------------
 *
 * types.go
 *	  Shared types for /wdranalyze: WDR report structure, findings,
 *	  Top SQL tuning results, and full analysis output.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/types.go
 *
 *-------------------------------------------------------------------------
 */

// Package wdranalyze implements /wdranalyze: long-window WDR report
// interpretation for openGauss / GaussDB. Pipeline: collect → parse →
// rule engine → Top SQL drill-down (via /sqlfetch + /sqltune) → optional
// LLM synthesis → render → persist. Design doc: docs/wdr/plan-wdranalyze.md.
package wdranalyze

import (
	"time"
)

// Severity classifies findings by urgency.
type Severity int

const (
	SeverityInfo     Severity = iota // 🟢 observed but normal
	SeverityWarning                  // 🟡 should address soon
	SeverityCritical                 // 🔴 immediate action needed
)

func (s Severity) String() string {
	switch s {
	case SeverityCritical:
		return "🔴 严重"
	case SeverityWarning:
		return "🟡 警告"
	case SeverityInfo:
		return "🟢 提示"
	default:
		return "?"
	}
}

// ReportHeader is the meta-info at the top of every WDR.
type ReportHeader struct {
	InstanceHost    string // e.g. "82.4.89.165:5432"
	InstanceID      string // e.g. "instance_id=1"
	DBVersion       string // e.g. "openGauss-lite 5.0.3"
	SnapshotIDStart int64  // -1 if unknown (file mode)
	SnapshotIDEnd   int64  // -1 if unknown
	WindowStart     time.Time
	WindowEnd       time.Time
}

func (h *ReportHeader) WindowDuration() time.Duration {
	return h.WindowEnd.Sub(h.WindowStart)
}

// TimeModelStats summarizes DB Time and its breakdown (CPU / Wait).
type TimeModelStats struct {
	DBTimeSec      float64 // total DB Time in seconds
	CPUTimeSec     float64 // CPU on DB
	WaitTimeSec    float64 // sum of all waits
	ParseTimeSec   float64 // hard + soft parse
	ExecTimeSec    float64 // execution
	HardParseCount int64
	SoftParseCount int64
}

// HardParseRatio returns hard_parse / (hard_parse + soft_parse). 0 if no parses.
func (t *TimeModelStats) HardParseRatio() float64 {
	total := t.HardParseCount + t.SoftParseCount
	if total == 0 {
		return 0
	}
	return float64(t.HardParseCount) / float64(total)
}

// WaitEvent is one row in the Top Waits section.
type WaitEvent struct {
	Name        string // e.g. "lock_wait_acquire"
	Category    string // "Lock" / "IO" / "Network" / "CPU" / ...
	WaitCount   int64
	WaitTimeMS  float64
	AvgWaitMS   float64
	PctOfDBTime float64 // percentage of total DB Time
}

// TopSQLEntry is one row aggregated from the various Top SQL sections in WDR
// (by elapsed_time, by buffer_reads, by exec_count, by CPU, etc.). We
// deduplicate by SQL_ID and track which view(s) it came from.
type TopSQLEntry struct {
	SQLID        string   // unique_sql_id
	Sources      []string // ["elapsed", "io", "exec_count", "cpu"]
	UserName     string
	DBName       string
	SchemaName   string // best-effort
	Calls        int64
	AvgTimeMS    float64
	TotalTimeMS  float64 // sum across executions
	AvgIO        int64
	TotalIO      int64
	RowsReturned int64
	QueryPrefix  string // first ~120 chars (display only; full SQL via sqlfetch)
}

// PctOfDBTime returns how much of total DB Time this SQL consumed.
func (e *TopSQLEntry) PctOfDBTime(dbTimeSec float64) float64 {
	if dbTimeSec == 0 {
		return 0
	}
	return (e.TotalTimeMS / 1000.0) / dbTimeSec * 100.0
}

// IOStats summarizes block reads/writes and buffer hit ratios.
type IOStats struct {
	BlocksRead       int64
	BlocksHit        int64
	WALWritesMB      float64 // total WAL written in window
	TempFilesMB      float64 // sort/hash spill
	ReadWriteIOPSAvg float64
}

// BufferHitRatio returns hits / (hits + reads). 0 if no IO.
func (i *IOStats) BufferHitRatio() float64 {
	total := i.BlocksHit + i.BlocksRead
	if total == 0 {
		return 0
	}
	return float64(i.BlocksHit) / float64(total)
}

// MemoryStats summarizes gs_total_memory and session memory.
type MemoryStats struct {
	TotalMemoryMB   int64 // max_process_memory in MB
	UsedMemoryMB    int64
	DynamicUsedMB   int64
	SharedBuffersMB int64
	WorkMemMB       int64
}

// UsageRatio returns used / total. 0 if total is 0.
func (m *MemoryStats) UsageRatio() float64 {
	if m.TotalMemoryMB == 0 {
		return 0
	}
	return float64(m.UsedMemoryMB) / float64(m.TotalMemoryMB)
}

// LockStats summarizes lock acquisition and wait stats.
type LockStats struct {
	LockWaitCount   int64
	LockWaitTimeMS  float64
	DeadlockCount   int64
	LWLockWaitCount int64
	LWLockWaitMS    float64
}

// ReplicationStats summarizes master-standby state.
type ReplicationStats struct {
	StandbyCount  int
	MaxLagSeconds float64
	SyncMode      string // "async" / "sync" / "quorum"
}

// WDRReport is the fully-parsed structured form of a WDR file.
type WDRReport struct {
	Header      ReportHeader
	TimeModel   TimeModelStats
	Waits       []WaitEvent   // sorted by PctOfDBTime desc
	TopSQLs     []TopSQLEntry // deduplicated across views, sorted by TotalTimeMS desc
	IO          IOStats
	Memory      MemoryStats
	Locks       LockStats
	Replication ReplicationStats
	Settings    map[string]string // selected key GUCs from the WDR's "Config" section
	Raw         string            // original WDR text (kept for LLM context if needed)
	Format      string            // "text" / "html"
	// v1.1.51: og 5.0.3 generate_wdr_report includes many sections that
	// don't fit the legacy WDR struct shape (Database Stat is per-db, not
	// per-instance; Load Profile uses Per Sec/Txn/Exec table layout, etc).
	// Keep them as semi-structured strings so the evaluator can apply
	// targeted rules and the LLM can read the raw cells.
	RawSections map[string]string // section_key → htmlToText'd content
	// SectionScores holds the deterministic rule-engine evaluation for each
	// section (Database Stat / Load Profile / Instance Efficiency / IO
	// Profile / TopSQL). The synthesizer ships these to the LLM as a
	// scorecard so the LLM doesn't have to invent severity ratings — keeps
	// outputs consistent across runs.
	SectionScores []SectionScore
}

// SectionLevel is the deterministic severity rating produced by the rule
// engine for one WDR section. Order: SectionGood < SectionWarning < SectionRisk.
type SectionLevel string

const (
	SectionGood    SectionLevel = "good"    // ✅
	SectionWarning SectionLevel = "warning" // 🟡
	SectionRisk    SectionLevel = "risk"    // 🔴
)

// Icon returns the emoji marker for a SectionLevel.
func (l SectionLevel) Icon() string {
	switch l {
	case SectionRisk:
		return "🔴"
	case SectionWarning:
		return "🟡"
	default:
		return "✅"
	}
}

// SectionRule is one triggered rule on a section. The evaluator records all
// triggered rules; the section's Level is max(rule.Level) across them.
type SectionRule struct {
	ID        string       // "soft_parse_low"
	Level     SectionLevel // this rule's severity contribution
	Metric    string       // "Soft Parse %"
	Observed  string       // "11"     (raw value as displayed)
	Threshold string       // "< 30"   (what the rule fires on)
	Reason    string       // one-line explanation for prompt context
}

// SectionScore is the rule-engine evaluation of one WDR section. Used by
// synthesizer to build a Layer-1 scorecard the LLM is asked to honor.
type SectionScore struct {
	Name       string            // "Database Stat"
	Level      SectionLevel      // max(Rules.Level), or Good if no triggers
	KeyMetrics map[string]string // small map of headline values for the scorecard
	Rules      []SectionRule     // every triggered rule
	Summary    string            // one-line risk preview (e.g. "R1: 临时空间溢出")
}

// Finding is one issue identified by the rule engine.
type Finding struct {
	ID         string // stable rule ID, e.g. "buffer_hit_low"
	Severity   Severity
	Category   string   // "buffer" / "wait" / "sql" / "config" / "lock" / "memory" / "io" / "replication" / "general"
	Title      string   // human-readable one-liner
	Evidence   []string // bullet points of numeric evidence
	Suggestion string   // remediation hint (rule engine layer; sqltune handles SQL-level)
	// EvidenceData carries structured data for the LLM synthesizer to chain
	// findings. Keys are rule-defined (e.g. "current_value", "threshold").
	EvidenceData map[string]any
}

// SQLTuneResult is the deep-dive optimization for one Top SQL, produced by
// calling /sqlfetch + /sqltune. May be nil if sqltune failed for that SQL.
type SQLTuneResult struct {
	SQLID        string
	FullSQL      string // from sqlfetch (post-substitute)
	Schema       string // from sqlfetch
	HasLiterals  bool
	OriginalCost float64
	BestNewCost  float64
	BestSpeedup  float64
	Candidates   []TuneCandidate
	FromMemory   bool   // true if result was reused from memory fingerprint match
	Error        string // empty on success
	// v1.1.52: maintenance SQL (CREATE/ALTER/ANALYZE/SET/SHOW) is skipped
	// before sqltune runs. Renderer shows these as informational, not as
	// failures — they have no plan to optimize.
	Skipped    bool
	SkipReason string
}

// TuneCandidate is one rewritten SQL or DDL recommendation from sqltune.
type TuneCandidate struct {
	Type         string // "rewrite" / "index" / "hint" / "schema" / "stats"
	Rationale    string
	SQL          string
	OldCost      float64
	NewCost      float64
	Speedup      float64
	Verifiable   bool // false for DDL (no EXPLAIN possible)
	EquivOK      bool
	RiskLevel    string // "low" / "medium" / "high"
	ExpectedGain string
}

// Analysis is the final assembled output of /wdranalyze.
type Analysis struct {
	Report         *WDRReport
	Findings       []Finding       // sorted by severity desc
	SQLTunes       []SQLTuneResult // for top N entries (default 5)
	LLMSynthesis   string          // optional LLM-generated prose; "" if disabled/failed
	GeneratedAt    time.Time
	Duration       time.Duration
	ReportPath     string        // persisted markdown path, shown in report metadata when known
	HistoryCompare *HistoryDelta // optional: comparison with previous wdranalyze
}

// HistoryDelta tracks change since the previous wdranalyze in the same window.
type HistoryDelta struct {
	PreviousFile   string // path to previous report markdown
	PreviousAt     time.Time
	SeverityCounts map[Severity]int // delta: current - previous
	NewFindingIDs  []string         // findings present now but not before
	GoneFindingIDs []string         // resolved since last run
}

// Options control /wdranalyze execution.
type Options struct {
	Mode        string // "latest" / "snapshot" / "timerange" / "file"
	SnapshotA   int64  // for "snapshot" mode
	SnapshotB   int64
	Window      time.Duration // for "timerange" mode
	FilePath    string        // for "file" mode
	TopN        int           // # of TopSQL to drill-down (default 5)
	SkipSQLTune bool          // skip Phase 4 entirely
	SkipLLM     bool          // skip Phase 5 entirely
	OutputDir   string        // override default ~/.opendb/wdr_reports
}

// CountBySeverity returns a map of severity → count for the findings.
func CountBySeverity(findings []Finding) map[Severity]int {
	out := map[Severity]int{
		SeverityCritical: 0,
		SeverityWarning:  0,
		SeverityInfo:     0,
	}
	for _, f := range findings {
		out[f.Severity]++
	}
	return out
}
