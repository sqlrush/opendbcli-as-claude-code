/*-------------------------------------------------------------------------
 *
 * dialect.go
 *	  DialectPlanner is the per-DB strategy interface for the multi-DB
 *	  /sqltune engine. Each supported dialect (Oracle / MySQL /
 *	  PostgreSQL / openGauss / GaussDB) provides an implementation in
 *	  its own package; the neutral tuner in this package consumes the
 *	  interface and is otherwise dialect-free.
 *
 *	  Design constraints:
 *	    1. **No dialect-specific SQL in the neutral tuner.** All
 *	       hardcoded `pg_class`, `dba_tables`, `information_schema`,
 *	       `dbe_perf.*` etc. lives behind this interface.
 *	    2. **Optional capabilities degrade silently.** Methods that
 *	       don't apply on a given dialect (e.g. EnableTrace on PG open
 *	       source) return TraceData{Available:false} rather than error.
 *	       The LLM prompt then knows to skip "why other paths weren't
 *	       chosen" reasoning.
 *	    3. **Stateless across calls.** Implementations may hold a
 *	       db.Driver reference but should not retain session state
 *	       between Tune invocations. Trace sessions are scoped to a
 *	       single Tune via the returned closeFn.
 *
 *	  M1: interface declared, only OpenGauss implements it.
 *	  M2: MySQL implementation lands (optimizer_trace JSON).
 *	  M3: PostgreSQL implementation lands (no trace, pg_stats sidecar).
 *	  M4a: og implementation upgraded with EXPLAIN PERFORMANCE detail.
 *	  M4b: GaussDB-distinct implementation adds GS_PLAN_TRACE on
 *	       centralized deployments.
 *	  M5:  Oracle implementation lands (10053 via UTL_FILE / V$DIAG_INFO).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/dialect.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import "context"

// DialectKind identifies which DB family an implementation serves.
// Used by the routing layer to pick the right DialectPlanner from
// a registry keyed by db.Driver's DBType().
type DialectKind string

const (
	DialectOpenGauss  DialectKind = "opengauss"
	DialectGaussDB    DialectKind = "gaussdb"
	DialectPostgreSQL DialectKind = "postgres"
	DialectMySQL      DialectKind = "mysql"
	DialectOracle     DialectKind = "oracle"
)

// DialectPlanner is implemented once per DB family. The neutral tuner
// orchestrates phases by calling these methods; it never touches the
// underlying db.Driver directly for dialect-sensitive operations.
type DialectPlanner interface {
	// Kind identifies the dialect. Used for logging and prompt context.
	Kind() DialectKind

	// ExplainPlan runs an EXPLAIN (with ANALYZE when analyze=true) and
	// parses the result into a neutral PlanInfo. The timeout caps total
	// execution time including ANALYZE; on timeout, implementations
	// should fall back to estimate-only EXPLAIN if possible.
	//
	// When the SQL contains unbound placeholders the implementation
	// must return a *PlaceholderError (wrapped as error) so the
	// routing layer can recover via history-table lookup.
	ExplainPlan(ctx context.Context, sql string, opts ExplainOptions) (*PlanInfo, error)

	// QuickPlanCost runs estimate-only EXPLAIN and returns the root
	// total_cost. Used during candidate verification (no ANALYZE so
	// it's safe to run hundreds of variants quickly).
	QuickPlanCost(ctx context.Context, sql string) (float64, error)

	// CollectSchema gathers table/index/stat/FK info for the tables
	// referenced in sql. Returns the SchemaInfo plus any warnings
	// (e.g. permission denied on pg_statistic → empty stats with note).
	CollectSchema(ctx context.Context, sql string) (info *SchemaInfo, notes []string, err error)

	// SnapshotDialect captures version / extensions / CBO parameters.
	// Output drives the M7 (dialect knowledge) prompt section.
	SnapshotDialect(ctx context.Context) (*DialectInfo, error)

	// SnapshotRuntime captures wait events and locks. Best-effort —
	// permission-restricted environments should return Degraded:true
	// with whatever fields are visible rather than failing the tune.
	//
	// involvedTables is an optimization hint: implementations may
	// filter the lock list to only entries touching these tables.
	// Pass nil or empty to get system-wide locks.
	SnapshotRuntime(ctx context.Context, involvedTables []string) (*RuntimeInfo, error)

	// ExpandViews substitutes referenced views with their definitions
	// where doing so helps the LLM see the underlying table operations.
	// Returns "" if no expansion is applicable.
	ExpandViews(ctx context.Context, sql string) (string, error)

	// EnableTrace turns on session-level CBO trace and returns a
	// closeFn to disarm it. On dialects without CBO trace
	// (PG / openGauss open source / older MySQL), returns a no-op
	// closer plus TraceData{Available:false, Format:"none", Notes:...}
	// indicating the reason — the LLM uses this note to set
	// expectations about CBO reasoning depth.
	//
	// queryTag is an opaque marker the implementation must wire into
	// the trace so CollectTrace can later filter to just this Tune's
	// SQL (e.g. Oracle TRACEFILE_IDENTIFIER, MySQL optimizer_trace
	// per-session, GaussDB query_id).
	EnableTrace(ctx context.Context, queryTag string) (closeFn func() error, initial *TraceData, err error)

	// CollectTrace fetches the trace body captured for queryTag after
	// the SQL has been executed. Should be called between SQL
	// execution and closeFn. On dialects without trace, returns the
	// same TraceData EnableTrace returned (Available:false).
	CollectTrace(ctx context.Context, queryTag string) (*TraceData, error)

	// NormalizePlaceholders inspects sql for unbound placeholders
	// (Oracle :N, PG/OG $N, MySQL ?) and either substitutes literal
	// values from a history source or returns a *PlaceholderError
	// guiding the caller to fetch the literal SQL.
	NormalizePlaceholders(ctx context.Context, sql string) (string, error)
}

// PerformancePlanner is an OPTIONAL capability that dialects can
// implement on top of DialectPlanner to expose per-operator execution
// detail beyond a basic EXPLAIN plan tree. Use it by type-asserting:
//
//	if pp, ok := planner.(sqltune.PerformancePlanner); ok {
//	    td, err := pp.ExplainPerformance(ctx, sql)
//	    // td.Body has dialect-specific detail (og PERFORMANCE table,
//	    // GaussDB equivalent, etc.); LLM consumes it as supplementary
//	    // context alongside the basic plan tree.
//	}
//
// Currently implemented by:
//   - openGauss (EXPLAIN PERFORMANCE: 11-column per-operator table with
//     A-time / A-rows / E-rows / E-distinct / Peak Memory / E-memory /
//     A-width / E-width / E-costs + per-datanode breakdown)
//   - GaussDB centralized (same EXPLAIN PERFORMANCE + GS_PLAN_TRACE
//     which is real CBO decision dump and goes through the regular
//     EnableTrace/CollectTrace interface)
//
// Not implemented by MySQL / PG / Oracle — they have other trace
// mechanisms via the standard EnableTrace flow.
//
// This pattern lives in the neutral package so the og Tuner (which is
// dialect-free after M1.4) can type-assert without importing og.
type PerformancePlanner interface {
	DialectPlanner

	// ExplainPerformance runs the dialect's enhanced EXPLAIN that
	// surfaces operator-level execution detail. Idempotent (does not
	// modify session state). Caller may invoke even on read-only
	// connections.
	//
	// On dialects/SQL where it's not applicable (DML on og, unsupported
	// version, etc.), returns *TraceData with Available:false plus a
	// Notes string explaining why — never an error for the "not
	// applicable" case. Returns error only for genuine failures
	// (connection lost, permission denied with no fallback).
	ExplainPerformance(ctx context.Context, sql string) (*TraceData, error)
}

// EquivVerifier is an OPTIONAL capability that dialects implement to
// check whether a candidate (rewritten) SQL produces semantically
// equivalent rows to the original. Critical safety net for rewrite-type
// candidates — a rewrite that changes semantics is a production bug
// waiting to happen.
//
// Strategy is dialect-specific (each has different hash + aggregation
// functions):
//   - PG/openGauss/GaussDB: md5(string_agg(row::text, ...))
//   - MySQL:                MD5(GROUP_CONCAT(...))
//   - Oracle 12c+:          STANDARD_HASH(LISTAGG(...))
//
// Common contract:
//   - Sample-based — caller passes `limit` to cap rows hashed.
//     Equivalence within the sample is NOT proof of full equivalence,
//     just strong evidence. Order-independent (the implementation
//     internally sorts by row hash so user's ORDER BY doesn't matter).
//   - Read-only — implementations must reject DML/DDL with error.
//     Running INSERT/UPDATE/DELETE twice for "verification" would
//     double-apply changes.
//   - Placeholder-rejecting — implementations should detect unbound
//     placeholders and return PlaceholderError, mirroring ExplainPlan.
//
// Returns:
//   - (true, nil)  → equivalent within the sampled rows
//   - (false, nil) → not equivalent (some rows differ)
//   - (false, err) → verification failed (connection / syntax / etc.)
//
// Use via type-assertion in the Tuner orchestrator:
//
//	if ev, ok := planner.(sqltune.EquivVerifier); ok {
//	    equiv, err := ev.VerifyEquivalence(ctx, orig, candidate, 1000)
//	}
//
// Not implemented → caller skips equivalence check and marks the
// VerifyResult with EquivOK=nil (Unknown).
type EquivVerifier interface {
	DialectPlanner

	// VerifyEquivalence compares two SELECT queries' results via
	// dialect-native row hashing. `limit` caps the sample (typical
	// 1000-10000). DML/DDL SQL must be rejected with error.
	VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error)
}

// ExplainOptions captures per-call EXPLAIN behavior knobs.
type ExplainOptions struct {
	Analyze        AnalyzeMode // Auto (default) / Force / Skip
	TimeoutSeconds int         // overall cap; implementation may shorten for ANALYZE on big plans
	Buffers        bool        // include buffer/IO stats (PG/OG)
	Performance    bool        // EXPLAIN PERFORMANCE on og (algorithm-level detail); ignored elsewhere
	FormatJSON     bool        // request JSON output when supported (PG/OG/MySQL); always false for Oracle
}

// AnalyzeMode is a tri-state for whether EXPLAIN ANALYZE should run.
// The zero value (AnalyzeAuto) lets the dialect's collector decide
// based on SQL size and safety heuristics — this is the safe default
// for the public API. Force / Skip are explicit overrides.
type AnalyzeMode int

const (
	AnalyzeAuto  AnalyzeMode = iota // dialect decides (default)
	AnalyzeForce                    // always ANALYZE (DBA accepts execution risk)
	AnalyzeSkip                     // never ANALYZE (estimates only)
)

// Registry maps dialect kind → planner factory. Per-dialect packages
// call Register(DialectKind, factory) in init() so the routing layer
// can build a planner without importing the dialect package directly.
//
// The factory receives the dialect's db.Driver (already opened by the
// connection manager) plus options like memory store / LLM provider.
type Factory func(deps PlannerDeps) (DialectPlanner, error)

// PlannerDeps bundles common dependencies the dialect packages need.
// Kept here (not in the dialect packages) so the neutral tuner can
// construct planners without importing concrete driver types.
type PlannerDeps struct {
	Driver any // concrete driver: each dialect type-asserts to its own driver
	// Future: MemoryStore, LLMProvider — currently the tuner holds
	// these and passes them via the call args, not the planner deps.
}

var registry = map[DialectKind]Factory{}

// Register makes a dialect factory available via Lookup. Idempotent
// per-kind: later registrations replace earlier ones (useful for
// tests that swap in a mock).
func Register(k DialectKind, f Factory) { registry[k] = f }

// Lookup returns the registered factory for k, or nil if no dialect
// package has registered for it.
func Lookup(k DialectKind) Factory { return registry[k] }
