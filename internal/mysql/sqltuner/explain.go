/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  MySQL EXPLAIN FORMAT=JSON capture + tree mapping for sqltune.
 *
 *	  MySQL's JSON EXPLAIN output (5.7+) is a single JSON document
 *	  rooted at "query_block". Operators are nested via several
 *	  patterns:
 *
 *	    {"query_block":
 *	      {"select_id":1,
 *	       "cost_info":{"query_cost":"123.45"},
 *	       "nested_loop":[                  ← multi-table join
 *	         {"table": {"table_name":"t1", ...}},
 *	         {"table": {"table_name":"t2", ...}}
 *	       ]}}
 *
 *	  Single-table queries use {"query_block":{"table":{...}}}.
 *	  GROUP BY / ORDER BY wrap with {"grouping_operation":{...}}.
 *	  Subqueries appear as {"subqueries":[{"query_block":{...}}]}.
 *
 *	  We map this onto sqltune.PlanNode by treating each "table" node
 *	  as a leaf operator and nested_loop / grouping_operation /
 *	  ordering_operation as internal operators with children.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/explain.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// ExplainPlan runs MySQL EXPLAIN FORMAT=JSON on sql and parses the
// result into a neutral PlanInfo. Honors opts.Analyze tri-state:
//   - AnalyzeForce  → EXPLAIN ANALYZE (8.0.18+) actually runs the SQL
//   - AnalyzeAuto   → EXPLAIN ANALYZE for SELECT, plain EXPLAIN for DML
//   - AnalyzeSkip   → plain EXPLAIN (estimates only)
//
// Returns sqltune.PlaceholderError when sql contains unbound `?`.
func (p *mysqlPlanner) ExplainPlan(ctx context.Context, sql string, opts sqltune.ExplainOptions) (*sqltune.PlanInfo, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return nil, pe
	}

	analyze := false
	switch opts.Analyze {
	case sqltune.AnalyzeForce:
		analyze = true
	case sqltune.AnalyzeSkip:
		analyze = false
	case sqltune.AnalyzeAuto:
		analyze = isReadOnlyQuery(sql)
	}

	cmd := "EXPLAIN FORMAT=JSON " + sql
	if analyze {
		// EXPLAIN ANALYZE FORMAT=JSON is 8.0.18+; older versions just get
		// plain EXPLAIN ANALYZE which is tree-format. We fall back below.
		cmd = "EXPLAIN ANALYZE FORMAT=JSON " + sql
	}

	res, err := p.driver.Query(ctx, cmd)
	if err != nil {
		// Fall back to plain EXPLAIN FORMAT=JSON if ANALYZE syntax rejected.
		if analyze {
			res, err = p.driver.Query(ctx, "EXPLAIN FORMAT=JSON "+sql)
		}
		if err != nil {
			return nil, fmt.Errorf("mysql explain: %w", err)
		}
		analyze = false
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return nil, fmt.Errorf("mysql explain returned no rows")
	}

	rawJSON, ok := res.Rows[0][0].(string)
	if !ok {
		// MySQL driver may return []byte instead of string for JSON columns.
		if b, ok := res.Rows[0][0].([]byte); ok {
			rawJSON = string(b)
		} else {
			return nil, fmt.Errorf("mysql explain: unexpected EXPLAIN result type %T", res.Rows[0][0])
		}
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &doc); err != nil {
		return nil, fmt.Errorf("mysql explain: parse json: %w", err)
	}

	qb, _ := doc["query_block"].(map[string]any)
	if qb == nil {
		return nil, fmt.Errorf("mysql explain: missing query_block")
	}

	info := &sqltune.PlanInfo{HasAnalyze: analyze}
	info.Root = parseMySQLBlock(qb)
	if info.Root != nil {
		info.TotalCost = info.Root.TotalCost
	}
	// query_block.cost_info.query_cost overrides if present (sometimes
	// the root's cost is per-table and the block has the true total)
	if ci, ok := qb["cost_info"].(map[string]any); ok {
		if c := parseFloat(ci["query_cost"]); c > 0 {
			info.TotalCost = c
		}
	}
	return info, nil
}

// QuickPlanCost is what verify uses to compare candidate rewrites.
// Plain EXPLAIN FORMAT=JSON (no ANALYZE) — safe to call repeatedly.
func (p *mysqlPlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return 0, pe
	}
	res, err := p.driver.Query(ctx, "EXPLAIN FORMAT=JSON "+sql)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0, fmt.Errorf("empty explain result")
	}
	raw := toString(res.Rows[0][0])
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return 0, err
	}
	qb, _ := doc["query_block"].(map[string]any)
	if qb == nil {
		return 0, fmt.Errorf("missing query_block")
	}
	if ci, ok := qb["cost_info"].(map[string]any); ok {
		return parseFloat(ci["query_cost"]), nil
	}
	return 0, nil
}

// ── JSON → PlanNode mapping ───────────────────────────────────────────

// parseMySQLBlock walks a query_block subtree and returns a single
// PlanNode root. Internal wrapper operators (nested_loop /
// grouping_operation / ordering_operation) become parent nodes; each
// "table" becomes a leaf.
func parseMySQLBlock(qb map[string]any) *sqltune.PlanNode {
	// Handle wrapping operators in priority order. The first match
	// becomes the root; remaining structure goes under it.
	if ord, ok := qb["ordering_operation"].(map[string]any); ok {
		n := &sqltune.PlanNode{Operator: "ORDER BY"}
		n.SortKey = extractSortKey(ord)
		if child := parseMySQLBlock(ord); child != nil {
			n.Children = append(n.Children, child)
		}
		return n
	}
	if grp, ok := qb["grouping_operation"].(map[string]any); ok {
		n := &sqltune.PlanNode{Operator: "GROUP BY"}
		if child := parseMySQLBlock(grp); child != nil {
			n.Children = append(n.Children, child)
		}
		return n
	}
	if nl, ok := qb["nested_loop"].([]any); ok {
		n := &sqltune.PlanNode{Operator: "Nested Loop Join"}
		for _, step := range nl {
			if sm, ok := step.(map[string]any); ok {
				if leaf := parseMySQLTable(sm); leaf != nil {
					n.Children = append(n.Children, leaf)
				}
			}
		}
		// Sum cost across children as a coarse total.
		for _, c := range n.Children {
			n.TotalCost += c.TotalCost
			n.PlanRows += c.PlanRows
		}
		return n
	}
	// Single-table query_block.
	if leaf := parseMySQLTable(qb); leaf != nil {
		return leaf
	}
	return nil
}

// parseMySQLTable converts a {"table":{...}} leaf into a PlanNode.
// Falls through for blocks that aren't tables (returns nil so caller
// can try other operator types).
func parseMySQLTable(m map[string]any) *sqltune.PlanNode {
	tbl, ok := m["table"].(map[string]any)
	if !ok {
		return nil
	}
	n := &sqltune.PlanNode{
		Operator: explainAccessType(tbl),
	}
	n.RelationName, _ = tbl["table_name"].(string)
	if at, ok := tbl["access_type"].(string); ok && at != "" {
		n.Operator = at + " on " + n.RelationName
	}
	if rows := parseInt(tbl["rows_examined_per_scan"]); rows > 0 {
		n.PlanRows = rows
	}
	if ci, ok := tbl["cost_info"].(map[string]any); ok {
		n.TotalCost = parseFloat(ci["read_cost"]) + parseFloat(ci["eval_cost"])
	}
	if cond, ok := tbl["attached_condition"].(string); ok {
		n.Filter = cond
	}
	if key, ok := tbl["key"].(string); ok && key != "" {
		n.IndexCondition = "using key=" + key
	}
	// MySQL doesn't have direct "actual" rows in JSON format unless
	// using EXPLAIN ANALYZE TREE — left empty here.
	return n
}

// explainAccessType returns a human-friendly operator name from
// MySQL's terse access_type codes (ALL/ref/range/eq_ref/const).
func explainAccessType(tbl map[string]any) string {
	at, _ := tbl["access_type"].(string)
	switch at {
	case "ALL":
		return "Full Table Scan"
	case "index":
		return "Full Index Scan"
	case "range":
		return "Range Scan"
	case "ref", "eq_ref":
		return "Index Lookup"
	case "const":
		return "Constant"
	case "":
		return "Table Access"
	default:
		return at
	}
}

func extractSortKey(m map[string]any) []string {
	if uk, ok := m["using_filesort"].(bool); ok && uk {
		return []string{"<filesort>"}
	}
	return nil
}

// ── Helpers ────────────────────────────────────────────────────────────

// detectPlaceholders scans sql for `?` outside of string literals and
// returns a sqltune.PlaceholderError if any are found. MySQL's `?` is
// the only placeholder style in normalized SQL we expect to see.
func detectPlaceholders(sql string) *sqltune.PlaceholderError {
	var found []string
	inSingle, inDouble := false, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == '\\' && i+1 < len(sql):
			i++ // skip escaped char
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '?' && !inSingle && !inDouble:
			found = append(found, "?")
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &sqltune.PlaceholderError{
		SQL:          sql,
		Placeholders: found,
		DetectedKind: "qmark",
		Suggestion: fmt.Sprintf(
			"SQL 含 %d 个未绑定占位符 ?。MySQL 归一化 SQL 来自 performance_schema.events_statements_summary_by_digest，"+
				"无字面量无法 EXPLAIN。请从 events_statements_history_long 拉带字面量的样本：\n"+
				"  SELECT SQL_TEXT FROM performance_schema.events_statements_history_long\n"+
				"   WHERE DIGEST = '<digest>' AND SQL_TEXT IS NOT NULL\n"+
				"   ORDER BY TIMER_END DESC LIMIT 1;",
			len(found)),
		Recoverable: true,
	}
}

// isReadOnlyQuery returns true if the SQL is a SELECT (safe to ANALYZE).
// DML / DDL must not be auto-ANALYZE'd as they'd actually execute.
func isReadOnlyQuery(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < 6 {
		return false
	}
	return strings.EqualFold(trimmed[:6], "SELECT") ||
		strings.EqualFold(trimmed[:4], "WITH")
}

func parseFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	case []byte:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f
	}
	return 0
}

func parseInt(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	case string:
		i, _ := strconv.ParseInt(x, 10, 64)
		return i
	case []byte:
		i, _ := strconv.ParseInt(string(x), 10, 64)
		return i
	}
	return 0
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprintf("%v", x)
	}
}
