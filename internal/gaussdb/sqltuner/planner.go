/*-------------------------------------------------------------------------
 *
 * planner.go
 *	  gaussdbPlanner implements sqltune.DialectPlanner for GaussDB
 *	  Centralized (V2.0-8.x), by **decorating** og's planner.
 *
 *	  Why decorator pattern:
 *	    - GaussDB(for openGauss) Centralized is binary-compatible with
 *	      openGauss for almost everything sqltune touches: pg_class,
 *	      pg_stats, pg_settings, EXPLAIN (FORMAT JSON), EXPLAIN
 *	      PERFORMANCE — all identical to og.
 *	    - The ONLY real differentiator for sqltune is GS_PLAN_TRACE
 *	      (CBO decision dump, GaussDB商业版 exclusive).
 *	    - Duplicating 1000+ lines of og's collectors / EXPLAIN parsing
 *	      / dialect snapshot would be silly. Instead we forward 8 of 9
 *	      DialectPlanner methods to og and override only the 2 trace
 *	      methods (EnableTrace / CollectTrace) with GS_PLAN_TRACE logic.
 *
 *	  Important constraints (from M2 design research):
 *	    1. GS_PLAN_TRACE is the ONLY real CBO trace in the PG family.
 *	       Other dialects in the family (og open source, PG open source)
 *	       don't have anything equivalent.
 *	    2. Requires sysadmin role to access gs_plan_trace catalog table.
 *	    3. Only available in centralized deployments. Distributed
 *	       (DWS) — explicitly excluded per project decision.
 *	    4. Trace enablement GUC is **not publicly documented** by
 *	       Huawei. We assume DBA pre-enabled it (it's typically off by
 *	       default). We do NOT attempt to enable via SET because we
 *	       don't know the correct GUC name.
 *	    5. plan_trace column is text up to 300 MB. We cap reads at
 *	       1 MB to keep LLM token budget sane.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/planner.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/llm"
	ogsqltuner "github.com/sqlrush/opendb/internal/opengauss/sqltuner"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// gaussdbPlanner is the GaussDB Centralized implementation of
// sqltune.DialectPlanner + sqltune.PerformancePlanner. It decorates
// the og planner so 8 of 9 DialectPlanner methods inherit og's
// implementation; only Kind / EnableTrace / CollectTrace are overridden.
type gaussdbPlanner struct {
	og     sqltune.DialectPlanner // og's planner — forwarded to for most methods
	driver db.Driver              // direct access for GS_PLAN_TRACE queries
}

// NewPlanner returns a GaussDB planner that decorates an og planner.
func NewPlanner(driver db.Driver) sqltune.DialectPlanner {
	return &gaussdbPlanner{
		og:     ogsqltuner.NewPlanner(driver),
		driver: driver,
	}
}

// Kind returns DialectGaussDB so the routing layer can keep GaussDB
// memory / prompt config separate from og.
func (g *gaussdbPlanner) Kind() sqltune.DialectKind { return sqltune.DialectGaussDB }

// ── 8 methods forwarded verbatim to og planner ─────────────────────────

func (g *gaussdbPlanner) ExplainPlan(ctx context.Context, sql string, opts sqltune.ExplainOptions) (*sqltune.PlanInfo, error) {
	return g.og.ExplainPlan(ctx, sql, opts)
}

func (g *gaussdbPlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	return g.og.QuickPlanCost(ctx, sql)
}

func (g *gaussdbPlanner) CollectSchema(ctx context.Context, sql string) (*sqltune.SchemaInfo, []string, error) {
	return g.og.CollectSchema(ctx, sql)
}

func (g *gaussdbPlanner) SnapshotDialect(ctx context.Context) (*sqltune.DialectInfo, error) {
	di, err := g.og.SnapshotDialect(ctx)
	if di != nil && di.Version != "" {
		// Mark version as GaussDB so prompts can branch on it. The
		// underlying SELECT version() may return openGauss-like banner;
		// the og planner doesn't know GaussDB even exists.
		di.Version = "GaussDB-compat: " + di.Version
	}
	return di, err
}

func (g *gaussdbPlanner) SnapshotRuntime(ctx context.Context, involvedTables []string) (*sqltune.RuntimeInfo, error) {
	return g.og.SnapshotRuntime(ctx, involvedTables)
}

func (g *gaussdbPlanner) ExpandViews(ctx context.Context, sql string) (string, error) {
	return g.og.ExpandViews(ctx, sql)
}

func (g *gaussdbPlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	return g.og.NormalizePlaceholders(ctx, sql)
}

// ── PerformancePlanner: also forward to og (EXPLAIN PERFORMANCE works on both) ──

// ExplainPerformance forwards to og's implementation. EXPLAIN PERFORMANCE
// is identical syntax/semantics on GaussDB centralized — same 11-column
// per-operator table.
func (g *gaussdbPlanner) ExplainPerformance(ctx context.Context, sql string) (*sqltune.TraceData, error) {
	if pp, ok := g.og.(sqltune.PerformancePlanner); ok {
		return pp.ExplainPerformance(ctx, sql)
	}
	// Should never reach: og is a PerformancePlanner. Defensive fallback.
	return &sqltune.TraceData{
		Available: false,
		Format:    "og_explain_performance",
		Notes:     "og planner does not implement PerformancePlanner — unexpected",
	}, nil
}

// VerifyEquivalence forwards to og's implementation. GaussDB is PG-
// compatible for md5/string_agg/::text so identical SQL works.
func (g *gaussdbPlanner) VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error) {
	if ev, ok := g.og.(sqltune.EquivVerifier); ok {
		return ev.VerifyEquivalence(ctx, origSQL, candidateSQL, limit)
	}
	return false, fmt.Errorf("og planner does not implement EquivVerifier — unexpected")
}

// ── 2 methods overridden for GS_PLAN_TRACE ─────────────────────────────
//	Implementations live in trace.go.

// ── Init: register both factories with neutral routing ──────────────────

func init() {
	sqltune.Register(sqltune.DialectGaussDB, func(deps sqltune.PlannerDeps) (sqltune.DialectPlanner, error) {
		d, ok := deps.Driver.(db.Driver)
		if !ok || d == nil {
			return nil, fmt.Errorf("gaussdb planner: expected db.Driver in PlannerDeps.Driver, got %T", deps.Driver)
		}
		return NewPlanner(d), nil
	})

	sqltune.RegisterTuner(sqltune.DialectGaussDB, gaussdbTunerFactory)
}

// gaussdbTunerFactory wires the GaussDB planner into the same Tuner
// orchestration the og dialect uses. Reuses og's NewTunerFromPlanner
// so we don't duplicate the Phase A / Round 1 / verify flow.
// gaussdbTunerFactory now wires the neutral GenericTuner (M7.4) with
// GaussDB-specific prompt builder. Previously delegated to og's complex
// tuner; switching to the simpler neutral tuner means we lose memory
// injection / token compression / auto-upgrade — those features only
// matter for og's mature workload and can be added back via
// GaussDB-specific extension later if needed.
func gaussdbTunerFactory(deps sqltune.TunerDeps) (sqltune.TunerEngine, error) {
	driver, ok := deps.Driver.(db.Driver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("gaussdb tuner: expected db.Driver in TunerDeps.Driver, got %T", deps.Driver)
	}
	planner := NewPlanner(driver)
	llm := newLLMAdapter(castProvider(deps.Provider))
	return sqltune.NewGenericTuner(planner, NewPromptBuilder(), llm), nil
}

func castProvider(p any) llm.Provider {
	if p == nil {
		return nil
	}
	if v, ok := p.(llm.Provider); ok {
		return v
	}
	return nil
}

func castMemStore(m any) *memory.Store {
	if m == nil {
		return nil
	}
	if v, ok := m.(*memory.Store); ok {
		return v
	}
	return nil
}
