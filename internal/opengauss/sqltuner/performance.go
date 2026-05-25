/*-------------------------------------------------------------------------
 *
 * performance.go
 *	  og implementation of sqltune.PerformancePlanner — surfaces
 *	  openGauss's EXPLAIN PERFORMANCE per-operator detail to the
 *	  neutral Tuner so it can be rendered into the report and consumed
 *	  by the LLM.
 *
 *	  EXPLAIN PERFORMANCE format (openGauss 3.0+):
 *	  An 11-column table per operator with columns:
 *	    id, operation, A-time, A-rows, E-rows, E-distinct,
 *	    Peak Memory, E-memory, A-width, E-width, E-costs
 *	  Plus per-datanode breakdown rows when on cluster deployments.
 *
 *	  Why it's not a real CBO trace:
 *	    - EXPLAIN PERFORMANCE shows what HAPPENED (runtime stats),
 *	      not what the planner CONSIDERED (rejected paths). The latter
 *	      requires GS_PLAN_TRACE which exists only in GaussDB商业版
 *	      centralized deployments.
 *	    - We still surface it via the trace pathway because it's the
 *	      best execution-side detail openGauss offers, and the LLM can
 *	      use A-rows vs E-rows divergence to infer stats staleness
 *	      similarly to how it'd use rejected-path costs.
 *
 *	  Safety:
 *	    - DML/DDL EXPLAIN PERFORMANCE WOULD execute the statement
 *	      (PERFORMANCE implies ANALYZE in og). We refuse for non-SELECT
 *	      SQL with Available:false + a note rather than wrap in tx
 *	      (the og Tuner already wraps for the JSON ANALYZE path; this
 *	      method intentionally stays simple).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/performance.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// ExplainPerformance runs `EXPLAIN PERFORMANCE <sql>` and returns the
// raw text output as a TraceData. Implements sqltune.PerformancePlanner.
//
// The text output is NOT structured-parsed — it's handed to the LLM
// (or rendered to the user) verbatim. Parsing the 11-column table per
// operator into a typed structure is M9 work if needed; for now the
// LLM is fully capable of reading the text representation.
func (p *ogPlanner) ExplainPerformance(ctx context.Context, sql string) (*sqltune.TraceData, error) {
	if !isSelectish(sql) {
		return &sqltune.TraceData{
			Available: false,
			Format:    "og_explain_performance",
			Notes:     "EXPLAIN PERFORMANCE 会真实执行 SQL（DML 会修改数据），仅 SELECT/WITH 查询自动采集；DML 请通过事务回滚路径手动跑。",
		}, nil
	}

	res, err := p.driver.Query(ctx, "EXPLAIN PERFORMANCE "+sql)
	if err != nil {
		// Not a fatal error — return Available:false with diagnostic.
		// Common cause: permission denied on EXPLAIN PERFORMANCE for
		// certain object types, or query timeout.
		return &sqltune.TraceData{
			Available: false,
			Format:    "og_explain_performance",
			Notes:     "EXPLAIN PERFORMANCE 失败: " + err.Error(),
		}, nil
	}
	if len(res.Rows) == 0 {
		return &sqltune.TraceData{
			Available: false,
			Format:    "og_explain_performance",
			Notes:     "EXPLAIN PERFORMANCE 返回空（应该不会发生；可能 driver 协议问题）",
		}, nil
	}

	// EXPLAIN PERFORMANCE returns a single column (QUERY PLAN) with one
	// row per output line. Concatenate to reconstruct the textual report.
	var b strings.Builder
	for i, row := range res.Rows {
		if i > 0 {
			b.WriteString("\n")
		}
		if len(row) > 0 {
			b.WriteString(stringify(row[0]))
		}
	}
	body := b.String()

	return &sqltune.TraceData{
		Available: true,
		Format:    "og_explain_performance",
		Body:      body,
		Bytes:     len(body),
		Notes: "openGauss EXPLAIN PERFORMANCE 算子级执行画像。包含 A-time / A-rows / E-rows / " +
			"Peak Memory / E-memory / E-costs 等列。**不是 CBO 决策 dump**（PG 系无 rejected " +
			"paths），但可对比 A-rows vs E-rows 推断 stats 失真。如需真正 CBO trace，请用 " +
			"GaussDB 商业版集中式部署的 GS_PLAN_TRACE。",
	}, nil
}

// isSelectish returns true for SQL safe to run EXPLAIN PERFORMANCE on
// without modifying data. We accept SELECT and WITH ... SELECT (CTE).
// WITH ... INSERT/UPDATE/DELETE is conservatively rejected even though
// some are read-only-equivalent (returning subqueries) — let users
// who need DML run the JSON ANALYZE path which wraps in tx.
func isSelectish(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < 4 {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "VALUES") {
		return true
	}
	if strings.HasPrefix(upper, "WITH ") {
		// Reject if any DML keyword appears as a standalone word.
		for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
			if containsWord(upper, kw) {
				return false
			}
		}
		return true
	}
	return false
}

// containsWord returns true if needle is in haystack as a standalone
// word (non-identifier characters or string boundaries on both sides).
// Local copy to keep this file self-contained; pg has an identical one.
func containsWord(haystack, needle string) bool {
	i := 0
	for {
		idx := strings.Index(haystack[i:], needle)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(needle)
		leftOK := start == 0 || !isIdentByte(haystack[start-1])
		rightOK := end == len(haystack) || !isIdentByte(haystack[end])
		if leftOK && rightOK {
			return true
		}
		i = end
	}
}

func isIdentByte(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
}

// stringify coerces a driver value (string / []byte / other) to string.
func stringify(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return ""
}
