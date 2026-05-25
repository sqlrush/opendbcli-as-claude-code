/*-------------------------------------------------------------------------
 *
 * planner.go
 *	  ogPlanner wraps og's existing collectors as a sqltune.DialectPlanner
 *	  implementation.
 *
 *	  Strategy: thin adapter. Each method delegates to the corresponding
 *	  og collector with minimal type munging (TuneOptions ↔ ExplainOptions,
 *	  PlaceholderSQLError ↔ sqltune.PlaceholderError). The collectors
 *	  themselves are unchanged — this file is the only seam to migrate
 *	  them to the neutral package in future milestones.
 *
 *	  Trace capabilities (EnableTrace/CollectTrace): openGauss open
 *	  source has NO CBO trace mechanism (no plan_trace; auto_explain
 *	  only logs final plan, no rejected paths). EnableTrace returns
 *	  TraceData{Available:false} with a note explaining the fallback.
 *	  GaussDB centralized version (GS_PLAN_TRACE) gets its own planner
 *	  in M4b.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/planner.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"errors"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// castProvider type-asserts the opaque TunerDeps.Provider into
// llm.Provider. nil deps.Provider returns nil (skill must check).
// Wrong type returns nil so og Tuner gracefully degrades to no-LLM mode.
func castProvider(p any) llm.Provider {
	if p == nil {
		return nil
	}
	if cast, ok := p.(llm.Provider); ok {
		return cast
	}
	return nil
}

// castMemStore type-asserts the opaque TunerDeps.MemStore into
// *memory.Store. nil OK — memory features degrade gracefully.
func castMemStore(m any) *memory.Store {
	if m == nil {
		return nil
	}
	if cast, ok := m.(*memory.Store); ok {
		return cast
	}
	return nil
}

// ogPlanner is the openGauss / GaussDB (non-trace path) implementation
// of sqltune.DialectPlanner.
type ogPlanner struct {
	driver      db.Driver
	plan        *PlanCollector
	schema      *SchemaCollector
	dialect     *DialectCollector
	runtime     *RuntimeCollector
	views       *ViewExpander
	placeholder *PlaceholderSubstituter
}

// NewPlanner constructs an og planner bound to driver. Used by both
// the og Tuner (internal) and by sqltune.Lookup factory dispatch.
func NewPlanner(driver db.Driver) sqltune.DialectPlanner {
	return &ogPlanner{
		driver:      driver,
		plan:        NewPlanCollector(driver),
		schema:      NewSchemaCollector(driver),
		dialect:     NewDialectCollector(driver),
		runtime:     NewRuntimeCollector(driver),
		views:       NewViewExpander(driver),
		placeholder: NewPlaceholderSubstituter(driver),
	}
}

func (p *ogPlanner) Kind() sqltune.DialectKind { return sqltune.DialectOpenGauss }

func (p *ogPlanner) ExplainPlan(ctx context.Context, sql string, opts sqltune.ExplainOptions) (*sqltune.PlanInfo, error) {
	// Map neutral ExplainOptions.Analyze tri-state → og TuneOptions.
	// AnalyzeAuto lets og's decideAnalyze() pick based on SQL size.
	t := sqltune.TuneOptions{SQL: sql}
	switch opts.Analyze {
	case sqltune.AnalyzeForce:
		t.ForceAnalyze = true
	case sqltune.AnalyzeSkip:
		t.NoAnalyze = true
	}
	info, err := p.plan.Collect(ctx, sql, t)
	if err != nil {
		return nil, wrapPlaceholderErr(err, sql)
	}
	return info, nil
}

func (p *ogPlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	return p.plan.QuickPlanCost(ctx, sql)
}

func (p *ogPlanner) CollectSchema(ctx context.Context, sql string) (*sqltune.SchemaInfo, []string, error) {
	return p.schema.Collect(ctx, sql)
}

func (p *ogPlanner) SnapshotDialect(ctx context.Context) (*sqltune.DialectInfo, error) {
	return p.dialect.Snapshot(ctx)
}

func (p *ogPlanner) SnapshotRuntime(ctx context.Context, involvedTables []string) (*sqltune.RuntimeInfo, error) {
	return p.runtime.Snapshot(ctx, involvedTables)
}

func (p *ogPlanner) ExpandViews(ctx context.Context, sql string) (string, error) {
	expanded, _, err := p.views.Expand(ctx, sql)
	return expanded, err
}

// EnableTrace is a no-op on openGauss open source: no CBO trace
// mechanism exists. Returns Available:false with a note so the LLM
// prompt knows not to expect "why other plans weren't chosen" data.
//
// GaussDB (商业版) with GS_PLAN_TRACE will need a distinct planner
// (M4b) since it's registered under sqltune.DialectGaussDB.
func (p *ogPlanner) EnableTrace(ctx context.Context, queryTag string) (func() error, *sqltune.TraceData, error) {
	td := &sqltune.TraceData{
		Available: false,
		Format:    "none",
		Notes:     "openGauss open source 不支持 CBO 决策跟踪（无 plan_trace；auto_explain 只记录最终 plan，无候选路径）。CBO 分析依赖 EXPLAIN ANALYZE 实际行数 vs 估算行数差异 + pg_stats 旁路推断。",
	}
	return func() error { return nil }, td, nil
}

func (p *ogPlanner) CollectTrace(ctx context.Context, queryTag string) (*sqltune.TraceData, error) {
	// Same notes as EnableTrace — no trace was captured.
	return &sqltune.TraceData{
		Available: false,
		Format:    "none",
		Notes:     "openGauss open source 无 CBO trace；本次诊断未采集 trace 数据。",
	}, nil
}

// NormalizePlaceholders runs og's substituter. When substitution
// succeeds, returns the rewritten SQL. When the SQL has placeholders
// the substituter can't satisfy, returns *sqltune.PlaceholderError
// to guide the caller to the right history source.
func (p *ogPlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	rewritten, _, err := p.placeholder.Substitute(ctx, sql, "")
	if err != nil {
		return sql, err
	}
	// Detect remaining placeholders after substitution — that's the
	// recoverable-error path the routing layer uses to fall back to
	// dbe_perf.statement_history.
	if pe := detectPlaceholders(rewritten); pe != nil {
		return rewritten, &sqltune.PlaceholderError{
			SQL:          rewritten,
			Placeholders: pe.Samples,
			DetectedKind: classifyPlaceholderKind(pe.Samples),
			Suggestion:   pe.Error(),
			Recoverable:  true,
		}
	}
	return rewritten, nil
}

// ── helpers ────────────────────────────────────────────────────────────

// wrapPlaceholderErr promotes og's local PlaceholderSQLError into the
// neutral sqltune.PlaceholderError so callers outside the og package
// can pattern-match without importing og.
func wrapPlaceholderErr(err error, sql string) error {
	if err == nil {
		return nil
	}
	var pe *PlaceholderSQLError
	if !errors.As(err, &pe) {
		return err
	}
	return &sqltune.PlaceholderError{
		SQL:          sql,
		Placeholders: pe.Samples,
		DetectedKind: classifyPlaceholderKind(pe.Samples),
		Suggestion:   pe.Error(),
		Recoverable:  true,
	}
}

// classifyPlaceholderKind guesses the placeholder style from a sample.
// Used to populate sqltune.PlaceholderError.DetectedKind so the
// routing layer can pick the right substitution strategy per dialect.
func classifyPlaceholderKind(ps []string) string {
	if len(ps) == 0 {
		return "unknown"
	}
	first := ps[0]
	switch {
	case len(first) > 0 && first[0] == '$':
		return "pg_dollar" // $1, $2 — PG / openGauss
	case len(first) > 0 && first[0] == ':':
		return "oracle_colon" // :1, :B1 — Oracle
	case first == "?":
		return "qmark" // ? — MySQL / JDBC default
	}
	return fmt.Sprintf("unknown(%s)", first)
}

// Register the og planner factory at init so the neutral routing
// layer (sqltune.Lookup) can find it without importing this package.
// Also registers the og Tuner under sqltune.RegisterTuner so
// dialect-free skill code can call sqltune.BuildTuner(DialectOpenGauss).
func init() {
	sqltune.Register(sqltune.DialectOpenGauss, func(deps sqltune.PlannerDeps) (sqltune.DialectPlanner, error) {
		d, ok := deps.Driver.(db.Driver)
		if !ok || d == nil {
			return nil, fmt.Errorf("og planner: expected db.Driver in PlannerDeps.Driver, got %T", deps.Driver)
		}
		return NewPlanner(d), nil
	})

	sqltune.RegisterTuner(sqltune.DialectOpenGauss, ogTunerFactory)
}

// ogTunerFactory is the sqltune.TunerFactory wiring og's Tuner. Lives
// alongside the planner registration so both are visible at one site.
func ogTunerFactory(deps sqltune.TunerDeps) (sqltune.TunerEngine, error) {
	driver, ok := deps.Driver.(db.Driver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("og tuner: expected db.Driver in TunerDeps.Driver, got %T", deps.Driver)
	}
	provider := castProvider(deps.Provider)
	memStore := castMemStore(deps.MemStore)
	return NewTuner(driver, provider, memStore), nil
}
