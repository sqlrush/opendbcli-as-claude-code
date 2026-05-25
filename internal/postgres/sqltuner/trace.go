/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  CBO decision trace lifecycle for PostgreSQL — explicitly NOT
 *	  available.
 *
 *	  PG (10-16, open source) has NO equivalent to Oracle 10053 /
 *	  MySQL optimizer_trace / GaussDB GS_PLAN_TRACE. The planner does
 *	  not dump rejected paths; you can only see the final chosen plan
 *	  via EXPLAIN.
 *
 *	  Nearest substitutes considered and rejected for M3:
 *	    - `auto_explain` extension: logs final plans + optional ANALYZE
 *	      output to server log. NO rejected paths. Reading the log
 *	      requires pg_read_server_files role. Same blindspot, more
 *	      effort. Net value over EXPLAIN(SETTINGS) is small.
 *	    - `debug_print_plan` GUC: raw plan tree dump (post-decision),
 *	      hugely verbose, no decision rationale. Worse than EXPLAIN.
 *	    - `pg_hint_plan`: lets you FORCE specific plans, doesn't reveal
 *	      what the optimizer would have picked.
 *
 *	  So: EnableTrace and CollectTrace return Available:false with a
 *	  note explaining the limitation. The neutral tuner sees this and
 *	  knows not to ask the LLM "why was this plan chosen over X" —
 *	  instead it leans on EXPLAIN ANALYZE actual-vs-estimated row
 *	  counts + pg_stats sidecar data to infer planner reasoning.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/trace.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// pgUnavailableTraceNote — kept as a const so both EnableTrace and
// CollectTrace surface identical wording. Used in the prompt-facing
// markdown report as well, so it must read clearly to both human and LLM.
const pgUnavailableTraceNote = "PostgreSQL 开源版无 CBO 决策跟踪机制（无 plan_trace；planner 不 dump 候选路径与其 cost）。" +
	"CBO 分析依赖 EXPLAIN ANALYZE 的实际行数 vs 估算行数差异 + pg_stats 旁路 (n_distinct/null_frac/correlation/histogram_bounds) " +
	"+ 关键 GUC (work_mem/random_page_cost/effective_cache_size) 让 LLM 推断 planner 决策。"

func (p *pgPlanner) EnableTrace(ctx context.Context, queryTag string) (func() error, *sqltune.TraceData, error) {
	return noopClose, &sqltune.TraceData{
		Available: false,
		Format:    "none",
		Notes:     pgUnavailableTraceNote,
	}, nil
}

func (p *pgPlanner) CollectTrace(ctx context.Context, queryTag string) (*sqltune.TraceData, error) {
	return &sqltune.TraceData{
		Available: false,
		Format:    "none",
		Notes:     pgUnavailableTraceNote,
	}, nil
}

func noopClose() error { return nil }
