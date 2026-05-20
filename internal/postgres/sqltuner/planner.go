/*-------------------------------------------------------------------------
 *
 * planner.go
 *	  pgPlanner implements sqltune.DialectPlanner for PostgreSQL 10-16.
 *
 *	  PG is the **structurally hardest** dialect for sqltune among the
 *	  ones we support because it has **no CBO rejected-paths dump**.
 *	  Unlike Oracle (10053), MySQL (optimizer_trace), and GaussDB
 *	  centralized (GS_PLAN_TRACE), PG does not surface the planner's
 *	  decision process — only the final plan. There is no way to ask
 *	  "what other plans did you consider, and why did you reject them?"
 *	  via any PG API in 16.x.
 *
 *	  Compensating strategy: rich **sidecar data** + LLM inference.
 *	  We give the LLM:
 *	    1. EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS, VERBOSE, COSTS,
 *	       SETTINGS) — final plan + actual vs estimated rows + buffer IO
 *	       + active GUC at planning time
 *	    2. pg_stats — histogram_bounds, n_distinct, null_frac,
 *	       correlation, most_common_vals/freqs for each involved column
 *	    3. pg_class — relpages, reltuples for size context
 *	    4. Key GUC — work_mem, random_page_cost, seq_page_cost,
 *	       effective_cache_size, max_parallel_workers_per_gather
 *
 *	  With these, an LLM can reason "actual rows >> estimated; n_distinct
 *	  appears stale → planner picked NestedLoop where HashJoin would win
 *	  if it had accurate stats; suggest ANALYZE + index". It can't see
 *	  the rejected HashJoin cost directly (10053 would tell you exactly),
 *	  but it can infer from the sidecar data.
 *
 *	  Why we don't read auto_explain logs in M3: requires
 *	  pg_read_server_files role + parsing mixed-format server logs.
 *	  Net value over EXPLAIN(SETTINGS,VERBOSE) is small. Out of scope.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/planner.go
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

// pgPlanner is the PostgreSQL implementation of sqltune.DialectPlanner.
type pgPlanner struct {
	driver db.Driver
}

func NewPlanner(driver db.Driver) sqltune.DialectPlanner {
	return &pgPlanner{driver: driver}
}

func (p *pgPlanner) Kind() sqltune.DialectKind { return sqltune.DialectPostgreSQL }

func init() {
	sqltune.Register(sqltune.DialectPostgreSQL, func(deps sqltune.PlannerDeps) (sqltune.DialectPlanner, error) {
		d, ok := deps.Driver.(db.Driver)
		if !ok || d == nil {
			return nil, fmt.Errorf("pg planner: expected db.Driver in PlannerDeps.Driver, got %T", deps.Driver)
		}
		return NewPlanner(d), nil
	})

	sqltune.RegisterTuner(sqltune.DialectPostgreSQL, pgTunerFactory)
}

// pgTunerFactory now wires the neutral GenericTuner (M7.4).
func pgTunerFactory(deps sqltune.TunerDeps) (sqltune.TunerEngine, error) {
	driver, ok := deps.Driver.(db.Driver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("pg tuner: expected db.Driver in TunerDeps.Driver, got %T", deps.Driver)
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
