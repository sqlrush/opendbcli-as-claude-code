/*-------------------------------------------------------------------------
 *
 * planner.go
 *	  oraclePlanner implements sqltune.DialectPlanner for Oracle 11g-23c.
 *
 *	  Oracle is the **hardest** dialect for sqltune because:
 *	    1. EXPLAIN output is text (DBMS_XPLAN), not JSON. We work around
 *	       by querying PLAN_TABLE directly for structured rows.
 *	    2. 10053 trace requires a hard parse (cursor cache hit skips it).
 *	       We force a hard parse by appending a unique opt_param hint
 *	       comment to the SQL.
 *	    3. 10053 trace lands in an OS file (Default Trace File). Reading
 *	       it via JDBC requires V$DIAG_TRACE_FILE_CONTENTS (19c+) or
 *	       UTL_FILE with a DBA-granted directory. We try V$DIAG first,
 *	       degrade to instructional message if neither works.
 *	    4. tracefile_identifier must be set per-session to isolate the
 *	       opendb-generated trace from concurrent traces.
 *
 *	  Implementation files:
 *	    planner.go (this file)  — struct + factory + Kind
 *	    explain.go              — EXPLAIN PLAN + PLAN_TABLE → PlanNode
 *	    trace.go                — 10053 enable/collect with V$DIAG fallback
 *	    collectors.go           — schema/dialect/runtime/views/placeholders
 *	    tuner.go                — Phase A orchestrator (no LLM yet)
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/planner.go
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

// oraclePlanner is the Oracle implementation of sqltune.DialectPlanner.
type oraclePlanner struct {
	driver db.Driver

	// traceTag is set by EnableTrace and used by CollectTrace to find
	// the right tracefile. Cleared by closeFn. Single trace at a time
	// per planner instance — concurrent /sqltune on the same connection
	// would interfere; that's the documented constraint.
	traceTag string
}

// NewPlanner returns an Oracle planner bound to driver.
func NewPlanner(driver db.Driver) sqltune.DialectPlanner {
	return &oraclePlanner{driver: driver}
}

func (p *oraclePlanner) Kind() sqltune.DialectKind { return sqltune.DialectOracle }

// ── Init: register factory ─────────────────────────────────────────────

func init() {
	sqltune.Register(sqltune.DialectOracle, func(deps sqltune.PlannerDeps) (sqltune.DialectPlanner, error) {
		d, ok := deps.Driver.(db.Driver)
		if !ok || d == nil {
			return nil, fmt.Errorf("oracle planner: expected db.Driver in PlannerDeps.Driver, got %T", deps.Driver)
		}
		return NewPlanner(d), nil
	})

	sqltune.RegisterTuner(sqltune.DialectOracle, oracleTunerFactory)
}

// oracleTunerFactory now wires the neutral GenericTuner (M7.4).
func oracleTunerFactory(deps sqltune.TunerDeps) (sqltune.TunerEngine, error) {
	driver, ok := deps.Driver.(db.Driver)
	if !ok || driver == nil {
		return nil, fmt.Errorf("oracle tuner: expected db.Driver in TunerDeps.Driver, got %T", deps.Driver)
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
