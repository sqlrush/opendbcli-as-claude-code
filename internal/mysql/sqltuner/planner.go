/*-------------------------------------------------------------------------
 *
 * planner.go
 *	  mysqlPlanner implements sqltune.DialectPlanner for MySQL 5.7 / 8.x.
 *
 *	  Why MySQL is the "easy" second dialect after og: MySQL's
 *	  optimizer_trace is a JSON CBO decision dump available via
 *	  SELECT — no OS file access required (unlike Oracle 10053), no
 *	  rejected-paths blindspot (unlike PG/og open source). The trace
 *	  body is the closest thing in the SQL world to Oracle's 10053
 *	  while being trivially accessible.
 *
 *	  Critical implementation details (from M2 design research):
 *	    1. Default optimizer_trace_max_mem_size is 1 MB — TOO SMALL
 *	       for complex JOINs. Always bump to 16 MB before enabling.
 *	    2. After capturing, check MISSING_BYTES_BEYOND_MAX_MEM_SIZE.
 *	       If > 0, double the budget and retry once. After that, accept
 *	       truncation and mark TraceData.Truncated=true.
 *	    3. The trace is per-session and stored in a ring buffer
 *	       (information_schema.OPTIMIZER_TRACE). One row per query.
 *	       MUST capture immediately after the target SQL or it gets
 *	       overwritten by subsequent statements in the same session.
 *	    4. EnableTrace's closeFn turns trace off + resets max_mem_size
 *	       so we don't pollute the session for subsequent skills.
 *	    5. INSUFFICIENT_PRIVILEGES=1 means view/SP references stripped
 *	       the trace. Note this in TraceData.Notes so the LLM is told.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/planner.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/engine/memory"
	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// mysqlPlanner is the MySQL implementation of sqltune.DialectPlanner.
//
// M2 scope (this commit): trace capture is the headline feature.
// Schema / dialect / runtime collectors return skeleton outputs with
// notes — full implementations land in M2.4. ExplainPlan uses
// EXPLAIN FORMAT=JSON (M2.2). MySQL /sqltune is usable from this point
// for "show me the optimizer's CBO trace" use case immediately.
type mysqlPlanner struct {
	driver db.Driver
}

// NewPlanner returns a MySQL planner bound to driver.
func NewPlanner(driver db.Driver) sqltune.DialectPlanner {
	return &mysqlPlanner{driver: driver}
}

func (p *mysqlPlanner) Kind() sqltune.DialectKind { return sqltune.DialectMySQL }

// ── Init: register factory so sqltune.BuildTuner(DialectMySQL) works ──

func init() {
	sqltune.Register(sqltune.DialectMySQL, func(deps sqltune.PlannerDeps) (sqltune.DialectPlanner, error) {
		d, ok := deps.Driver.(db.Driver)
		if !ok || d == nil {
			return nil, fmt.Errorf("mysql planner: expected db.Driver in PlannerDeps.Driver, got %T", deps.Driver)
		}
		return NewPlanner(d), nil
	})

	sqltune.RegisterTuner(sqltune.DialectMySQL, mysqlTunerFactory)
}

// mysqlTunerFactory builds a TunerEngine using the og tuner orchestrator
// (which is dialect-free now after M1.4) wrapped around mysqlPlanner.
//
// M2 design note: rather than duplicate the Tuner orchestration code
// per dialect, we reuse og's Tuner with the MySQL planner injected.
// The orchestration (Phase A → Round 1 → verify → Round 2) is identical
// across dialects; only Phase A's data collection differs, which is
// exactly what DialectPlanner abstracts.
//
// When M6 generalizes EquivVerifier or M7 moves orchestration into the
// neutral sqltune package, this factory will switch to use that.
// mysqlTunerFactory now wires the neutral GenericTuner (M7.4) with
// MySQL planner + prompt builder + LLM adapter. Old minimal newMySQLTuner
// in tuner.go is kept as a fallback path but no longer the default.
func mysqlTunerFactory(deps sqltune.TunerDeps) (sqltune.TunerEngine, error) {
	driver, ok := deps.Driver.(db.Driver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("mysql tuner: expected db.Driver in TunerDeps.Driver, got %T", deps.Driver)
	}
	planner := NewPlanner(driver)
	llm := newLLMAdapter(castProvider(deps.Provider))
	return sqltune.NewGenericTuner(planner, NewPromptBuilder(), llm), nil
}

// castProvider / castMemStore — identical pattern to og's planner.go,
// kept here (not shared) because pulling them into a util package would
// create an unnecessary import cycle between dialect packages.
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
