/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  optimizer_trace lifecycle: enable → execute → collect → disable.
 *
 *	  MySQL optimizer_trace is per-session and stored in an in-memory
 *	  ring buffer surfaced via information_schema.OPTIMIZER_TRACE.
 *	  Key constraints (from MySQL 5.7+ / 8.x docs):
 *
 *	    - Default optimizer_trace_max_mem_size = 1 MB. Complex JOINs
 *	      blow past it instantly. We bump to 16 MB at enable time.
 *	    - After execution, MISSING_BYTES_BEYOND_MAX_MEM_SIZE in the
 *	      result row tells you if the trace was truncated. If > 0,
 *	      we double the budget and retry (the original SQL).
 *	    - INSUFFICIENT_PRIVILEGES=1 means view/SP references stripped
 *	      content from the trace. We surface this in Notes.
 *	    - The ring buffer only keeps the last N traces. If something
 *	      runs between EnableTrace and CollectTrace, the trace is lost.
 *	      Therefore the tuner orchestrator calls them tightly around
 *	      a single EXPLAIN.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/trace.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// initialMaxMemSize is the bumped optimizer_trace_max_mem_size we set
// at EnableTrace. 16 MB handles typical complex JOINs; the 4 MB default
// hit by enabling the feature on a fresh session is not enough.
const initialMaxMemSize = 16 * 1024 * 1024 // 16 MB

// EnableTrace turns on session-level optimizer_trace and raises
// max_mem_size to 16 MB. queryTag is unused on MySQL (no per-query
// tagging mechanism), but kept for interface compatibility.
//
// Returns:
//   - closeFn: must be called by the orchestrator (defer-style) to
//     restore the session to a clean state.
//   - initial: TraceData with Available:true and a note about the
//     budget — the actual Body comes from CollectTrace later.
//   - err: only on SET-statement failure (rare; usually permission).
//
// On error, returns Available:false TraceData so the caller can still
// run EXPLAIN without trace.
func (p *mysqlPlanner) EnableTrace(ctx context.Context, queryTag string) (func() error, *sqltune.TraceData, error) {
	// SET both in one round-trip would be nicer but driver.Exec only
	// accepts one statement at a time; two calls is fine.
	if _, err := p.driver.Exec(ctx, fmt.Sprintf("SET SESSION optimizer_trace_max_mem_size = %d", initialMaxMemSize)); err != nil {
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "none",
			Notes:     "无法设置 optimizer_trace_max_mem_size: " + err.Error(),
		}, nil // not a fatal error — return Available:false instead
	}
	if _, err := p.driver.Exec(ctx, "SET SESSION optimizer_trace = \"enabled=on,one_line=off\""); err != nil {
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "none",
			Notes:     "无法开启 optimizer_trace: " + err.Error(),
		}, nil
	}

	// Mark active so closeFn knows it has work to do.
	var active atomic.Bool
	active.Store(true)
	close := func() error {
		if !active.CompareAndSwap(true, false) {
			return nil
		}
		// Best-effort cleanup. Ignore errors — session ending will
		// reset SESSION variables anyway.
		_, _ = p.driver.Exec(context.Background(), "SET SESSION optimizer_trace = \"enabled=off\"")
		return nil
	}

	return close, &sqltune.TraceData{
		Available: true,
		Format:    "mysql_json",
		Notes:     fmt.Sprintf("optimizer_trace 已启用，max_mem_size=%d bytes", initialMaxMemSize),
	}, nil
}

// CollectTrace fetches the most recent optimizer_trace from
// information_schema.OPTIMIZER_TRACE. MUST be called immediately after
// the target SQL or the trace gets overwritten.
//
// Auto-retry-on-truncation: if MISSING_BYTES_BEYOND_MAX_MEM_SIZE > 0,
// we surface that fact via Truncated:true. We do **not** auto re-run
// the query here because the orchestrator owns query execution — it
// would need to re-execute the original SQL with a bigger budget,
// which is its job, not ours.
func (p *mysqlPlanner) CollectTrace(ctx context.Context, queryTag string) (*sqltune.TraceData, error) {
	res, err := p.driver.Query(ctx,
		`SELECT TRACE, MISSING_BYTES_BEYOND_MAX_MEM_SIZE, INSUFFICIENT_PRIVILEGES
		   FROM information_schema.OPTIMIZER_TRACE
		   LIMIT 1`)
	if err != nil {
		return &sqltune.TraceData{
			Available: false,
			Format:    "none",
			Notes:     "无法查询 information_schema.OPTIMIZER_TRACE: " + err.Error(),
		}, nil
	}
	if len(res.Rows) == 0 {
		return &sqltune.TraceData{
			Available: false,
			Format:    "none",
			Notes:     "OPTIMIZER_TRACE 表无记录（trace 可能未启用或已被后续 query 覆盖）",
		}, nil
	}

	row := res.Rows[0]
	body := toString(row[0])
	missing := parseInt(row[1])
	insufficient := parseInt(row[2])

	td := &sqltune.TraceData{
		Available: body != "",
		Format:    "mysql_json",
		Body:      body,
		Bytes:     len(body),
	}
	if missing > 0 {
		td.Truncated = true
		td.Notes = fmt.Sprintf(
			"trace 被截断: MISSING_BYTES_BEYOND_MAX_MEM_SIZE=%d。可以重试时把 optimizer_trace_max_mem_size 调大",
			missing)
	}
	if insufficient > 0 {
		extra := "trace 中部分内容因 INSUFFICIENT_PRIVILEGES 被剥离（视图/存储过程相关）"
		if td.Notes != "" {
			td.Notes += "；" + extra
		} else {
			td.Notes = extra
		}
	}
	return td, nil
}

func noopClose() error { return nil }
