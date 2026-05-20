/*-------------------------------------------------------------------------
 *
 * trace.go
 *	  GS_PLAN_TRACE lifecycle for GaussDB Centralized.
 *
 *	  GS_PLAN_TRACE is the only true CBO decision dump in the PG family
 *	  (closest analog: Oracle 10053). The column "plan_trace" holds up
 *	  to 300 MB of optimizer reasoning per query — path candidates,
 *	  cost estimates, join order enumeration, statistic-based decisions.
 *
 *	  What we DO:
 *	    EnableTrace  — probe whether the gs_plan_trace catalog table
 *	                   exists in this instance + whether the role has
 *	                   sysadmin (required to SELECT it). Returns
 *	                   Available:true if both checks pass; the caller
 *	                   is then responsible for running the target SQL
 *	                   in the same session.
 *	    CollectTrace — SELECT the most recent row for this session.
 *	                   Cap returned body at 1 MB so token budget stays
 *	                   tractable (300 MB into a prompt is unworkable).
 *
 *	  What we DO NOT do:
 *	    - Attempt to enable plan_trace via SET — the enabling GUC is
 *	      not publicly documented by Huawei. We assume DBA pre-enabled.
 *	    - Touch distributed deployments (plan_trace is documented as
 *	      not supported in distributed; per project decision we don't
 *	      target distributed at all).
 *
 *	  Failure modes are graceful — anything goes wrong, we return
 *	  Available:false with a clear note so the LLM knows trace data
 *	  isn't there and falls back to the pg_stats sidecar (which is
 *	  inherited from the og decorator).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/trace.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// maxTraceBodyBytes caps the plan_trace body we read into memory and
// hand to the LLM. The column itself can be up to 300 MB; that's far
// beyond any LLM context window.
const maxTraceBodyBytes = 1 * 1024 * 1024 // 1 MB

// gsTraceEnvelope describes a row from gs_plan_trace as we use it.
// Kept here (not a public type) because gs_plan_trace is GaussDB-only.
type gsTraceRow struct {
	queryID    string
	query      string
	planText   string
	planTrace  string
	tracedAt   string // ISO timestamp string from server
}

// EnableTrace probes the GS_PLAN_TRACE catalog. queryTag is informational
// only — GaussDB's trace mechanism is session-level, not per-query-tag.
//
// Behavior:
//   - Table missing → Available:false (likely non-GaussDB or pre-V2.0).
//   - Table present but SELECT fails (permission) → Available:false with
//     "need sysadmin" guidance.
//   - All checks pass → Available:true; closeFn is no-op (we don't
//     mutate session state).
//
// The caller (Tuner orchestrator) is expected to run the target SQL
// after EnableTrace, then call CollectTrace.
func (g *gaussdbPlanner) EnableTrace(ctx context.Context, queryTag string) (func() error, *sqltune.TraceData, error) {
	// 1. Existence probe — single tiny query that fails fast if the
	//    catalog isn't there. We use to_regclass to avoid error spew.
	existsQ := `SELECT to_regclass('pg_catalog.gs_plan_trace') IS NOT NULL`
	res, err := g.driver.Query(ctx, existsQ)
	if err != nil || len(res.Rows) == 0 {
		// to_regclass should always exist on PG-family; if this fails
		// something deeper is wrong. Surface as no-trace + note.
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "gaussdb_plan_trace",
			Notes:     "GS_PLAN_TRACE 探测失败: " + fmtErr(err) + "。降级到 EXPLAIN PERFORMANCE + pg_stats 旁路。",
		}, nil
	}
	if !truthy(res.Rows[0][0]) {
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "gaussdb_plan_trace",
			Notes: "gs_plan_trace 表不存在（可能是非 GaussDB 实例、GaussDB V2.0 之前的版本、或分布式部署）。" +
				"降级到 EXPLAIN PERFORMANCE + pg_stats 旁路。",
		}, nil
	}

	// 2. Permission probe — SELECT 1 row to verify sysadmin access.
	//    LIMIT 0 keeps it cheap even on busy systems.
	if _, err := g.driver.Query(ctx, `SELECT 1 FROM pg_catalog.gs_plan_trace LIMIT 0`); err != nil {
		return noopClose, &sqltune.TraceData{
			Available: false,
			Format:    "gaussdb_plan_trace",
			Notes: "无权限访问 gs_plan_trace（需 sysadmin 角色）。错误: " + fmtErr(err) + "。" +
				"降级到 EXPLAIN PERFORMANCE + pg_stats 旁路。",
		}, nil
	}

	// All checks passed — trace is captureable.
	return noopClose, &sqltune.TraceData{
		Available: true,
		Format:    "gaussdb_plan_trace",
		Notes: "gs_plan_trace 可用（需确认 plan_trace 已通过 DBA 配置启用；启用 GUC 华为未公开）。" +
			"将在 CollectTrace 时读取最近一条记录。",
	}, nil
}

// CollectTrace fetches the most recent plan_trace row from gs_plan_trace.
// We pick the latest by modifydate (timestamp) rather than relying on
// session-binding because the trace is per-instance in PG-family
// catalogs, not per-session.
//
// Cap on body size: 1 MB. Beyond that we truncate + set Truncated:true.
func (g *gaussdbPlanner) CollectTrace(ctx context.Context, queryTag string) (*sqltune.TraceData, error) {
	// LEFT(plan_trace, N) keeps the network payload small if the trace
	// is huge. We over-fetch by 1 byte to detect "was there more".
	q := fmt.Sprintf(`
		SELECT COALESCE(unique_sql_id::text, ''),
		       COALESCE(query, ''),
		       COALESCE(plan, ''),
		       COALESCE(LEFT(plan_trace, %d), ''),
		       COALESCE(modifydate::text, ''),
		       LENGTH(plan_trace)
		  FROM pg_catalog.gs_plan_trace
		 ORDER BY modifydate DESC
		 LIMIT 1`, maxTraceBodyBytes+1)
	res, err := g.driver.Query(ctx, q)
	if err != nil {
		return &sqltune.TraceData{
			Available: false,
			Format:    "gaussdb_plan_trace",
			Notes:     "查询 gs_plan_trace 失败: " + err.Error(),
		}, nil
	}
	if len(res.Rows) == 0 {
		return &sqltune.TraceData{
			Available: false,
			Format:    "gaussdb_plan_trace",
			Notes:     "gs_plan_trace 表无记录（plan_trace 功能可能未在本会话启用；请联系 DBA 启用对应 GUC）。",
		}, nil
	}

	row := res.Rows[0]
	r := gsTraceRow{
		queryID:   toStr(row[0]),
		query:     toStr(row[1]),
		planText:  toStr(row[2]),
		planTrace: toStr(row[3]),
		tracedAt:  toStr(row[4]),
	}
	totalLen := int(toInt64(row[5]))

	body := buildTraceBody(r)
	truncated := totalLen > maxTraceBodyBytes

	notes := fmt.Sprintf("query_id=%s, traced_at=%s, plan_trace 原始大小=%d bytes",
		r.queryID, r.tracedAt, totalLen)
	if truncated {
		notes += fmt.Sprintf("，已截断到首 %d bytes 控制 LLM token 预算", maxTraceBodyBytes)
	}

	return &sqltune.TraceData{
		Available: true,
		Format:    "gaussdb_plan_trace",
		Body:      body,
		Bytes:     len(body),
		Truncated: truncated,
		Notes:     notes,
	}, nil
}

// buildTraceBody combines query / plan / plan_trace into one text
// blob suitable for the report and LLM consumption.
func buildTraceBody(r gsTraceRow) string {
	var b []byte
	if r.query != "" {
		b = append(b, "-- Captured query --\n"...)
		b = append(b, r.query...)
		b = append(b, '\n', '\n')
	}
	if r.planText != "" {
		b = append(b, "-- Final plan --\n"...)
		b = append(b, r.planText...)
		b = append(b, '\n', '\n')
	}
	if r.planTrace != "" {
		b = append(b, "-- CBO decision trace (planner reasoning) --\n"...)
		b = append(b, r.planTrace...)
		b = append(b, '\n')
	}
	return string(b)
}

func noopClose() error { return nil }

// truthy coerces a driver bool/string/int value to bool.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "t" || x == "true" || x == "TRUE" || x == "1"
	case []byte:
		s := string(x)
		return s == "t" || s == "true" || s == "TRUE" || s == "1"
	case int64:
		return x != 0
	case int:
		return x != 0
	}
	return false
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return ""
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

func fmtErr(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
