/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  Oracle EXPLAIN PLAN capture + PLAN_TABLE → PlanNode tree.
 *
 *	  Why we query PLAN_TABLE directly (not DBMS_XPLAN.DISPLAY text):
 *	  DBMS_XPLAN renders a pretty text table for humans. Parsing that
 *	  text to extract operator/cost/rows is fragile (column widths
 *	  vary, "Predicate Information" section format isn't stable).
 *	  PLAN_TABLE has structured columns (operation, options,
 *	  object_name, cost, cardinality, bytes, id, parent_id) — much
 *	  cleaner mapping to sqltune.PlanNode.
 *
 *	  Statement isolation: PLAN_TABLE is shared across sessions and
 *	  EXPLAIN PLAN inserts rows without clearing prior data. We set a
 *	  unique STATEMENT_ID per /sqltune call so concurrent uses don't
 *	  interfere, and DELETE our rows in closeFn after collection.
 *
 *	  DML safety: EXPLAIN PLAN FOR <DML> is safe on Oracle — it only
 *	  populates PLAN_TABLE, doesn't execute the DML. No tx wrap needed.
 *
 *	  Placeholders: Oracle bind variables are `:N` (positional, e.g.,
 *	  `:1`, `:2`) or `:B1` (named). We detect both and return
 *	  PlaceholderError pointing at V$SQL_BIND_CAPTURE for recovery.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/explain.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// ExplainPlan runs `EXPLAIN PLAN SET STATEMENT_ID = '...' FOR <sql>`
// then queries PLAN_TABLE for structured row data and rebuilds a
// PlanNode tree via the id/parent_id parent links.
//
// opts.Analyze: Oracle doesn't have EXPLAIN ANALYZE the way PG does.
// The closest is `EXPLAIN PLAN FOR` (estimates only — what we use here)
// vs `SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(...))` which shows
// actual runtime stats for a *previously executed* SQL. M5.1 ships
// only the estimates-only path; cursor-based actual stats is M5.x
// follow-up if needed.
//
// opts.Performance/Buffers/FormatJSON: silently ignored on Oracle.
func (p *oraclePlanner) ExplainPlan(ctx context.Context, sql string, opts sqltune.ExplainOptions) (*sqltune.PlanInfo, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return nil, pe
	}

	stmtID := newStatementID()
	if err := p.runExplainPlan(ctx, sql, stmtID); err != nil {
		return nil, err
	}
	defer p.cleanupPlanTable(ctx, stmtID)

	return p.fetchPlanTree(ctx, stmtID, false)
}

// QuickPlanCost runs the same EXPLAIN PLAN path but returns only the
// root operator's COST. Used by candidate verification (cheap, no
// PLAN_TABLE cleanup race since we use unique STATEMENT_IDs).
func (p *oraclePlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return 0, pe
	}
	stmtID := newStatementID()
	if err := p.runExplainPlan(ctx, sql, stmtID); err != nil {
		return 0, err
	}
	defer p.cleanupPlanTable(ctx, stmtID)

	// Direct COST fetch from root row — avoids tree construction cost.
	res, err := p.driver.Query(ctx,
		"SELECT NVL(cost, 0) FROM plan_table WHERE statement_id = :1 AND id = 0",
		stmtID)
	if err != nil {
		return 0, fmt.Errorf("fetch root cost: %w", err)
	}
	if len(res.Rows) == 0 {
		return 0, nil
	}
	return parseFloat(res.Rows[0][0]), nil
}

// runExplainPlan issues `EXPLAIN PLAN SET STATEMENT_ID = '...' FOR <sql>`.
// Returns the EXPLAIN PLAN error verbatim — most often it's a parse
// error (invalid SQL) or permission issue (PLAN_TABLE not accessible).
func (p *oraclePlanner) runExplainPlan(ctx context.Context, sql, stmtID string) error {
	cmd := "EXPLAIN PLAN SET STATEMENT_ID = '" + stmtID + "' FOR " + sql
	if _, err := p.driver.Exec(ctx, cmd); err != nil {
		return fmt.Errorf("EXPLAIN PLAN failed: %w", err)
	}
	return nil
}

// cleanupPlanTable removes the rows we inserted so PLAN_TABLE doesn't
// grow unbounded. Best-effort — ignore error, the next /sqltune call
// uses a different STATEMENT_ID anyway.
func (p *oraclePlanner) cleanupPlanTable(ctx context.Context, stmtID string) {
	_, _ = p.driver.Exec(ctx, "DELETE FROM plan_table WHERE statement_id = :1", stmtID)
}

// fetchPlanTree queries PLAN_TABLE for all rows of this STATEMENT_ID
// and rebuilds a PlanNode tree by walking the id/parent_id relations.
func (p *oraclePlanner) fetchPlanTree(ctx context.Context, stmtID string, hasAnalyze bool) (*sqltune.PlanInfo, error) {
	q := `SELECT id,
	             NVL(parent_id, -1) AS parent_id,
	             operation,
	             NVL(options, '')   AS options,
	             NVL(object_name, '') AS object_name,
	             NVL(cost, 0)         AS cost,
	             NVL(cardinality, 0)  AS cardinality,
	             NVL(bytes, 0)        AS bytes,
	             NVL(access_predicates, '') AS access_pred,
	             NVL(filter_predicates, '') AS filter_pred
	        FROM plan_table
	       WHERE statement_id = :1
	       ORDER BY id`
	res, err := p.driver.Query(ctx, q, stmtID)
	if err != nil {
		return nil, fmt.Errorf("fetch plan tree: %w", err)
	}
	if len(res.Rows) == 0 {
		return nil, fmt.Errorf("PLAN_TABLE has no rows for statement_id=%s (EXPLAIN ran but inserted nothing?)", stmtID)
	}

	// Build nodes first, indexed by id; then wire children via parent_id.
	nodes := make(map[int64]*sqltune.PlanNode, len(res.Rows))
	parentOf := make(map[int64]int64, len(res.Rows))
	for _, row := range res.Rows {
		id := parseInt(row[0])
		parent := parseInt(row[1])
		op := toString(row[2])
		opts := toString(row[3])
		nodes[id] = &sqltune.PlanNode{
			Operator:     joinOpAndOption(op, opts),
			RelationName: toString(row[4]),
			TotalCost:    parseFloat(row[5]),
			PlanRows:     parseInt(row[6]),
			PlanWidth:    int(parseInt(row[7])),
			IndexCondition: toString(row[8]),
			Filter:         toString(row[9]),
		}
		parentOf[id] = parent
	}

	// Wire children. Root is the node with parent_id < 0 (we NVL'd to -1).
	var root *sqltune.PlanNode
	for id, n := range nodes {
		p := parentOf[id]
		if p < 0 {
			root = n
			continue
		}
		if parentNode, ok := nodes[p]; ok {
			parentNode.Children = append(parentNode.Children, n)
		}
	}
	if root == nil {
		// Edge case: PLAN_TABLE returned rows but no row with NULL parent_id.
		// Use lowest id as root (Oracle conventionally has id=0 as root).
		root = nodes[0]
	}

	return &sqltune.PlanInfo{
		Root:       root,
		TotalCost:  root.TotalCost,
		HasAnalyze: hasAnalyze,
	}, nil
}

// joinOpAndOption combines `operation` (e.g. "TABLE ACCESS") and
// `options` (e.g. "FULL") into a single human-friendly operator name.
func joinOpAndOption(op, opts string) string {
	op = strings.TrimSpace(op)
	opts = strings.TrimSpace(opts)
	if opts == "" {
		return op
	}
	return op + " " + opts
}

// ── Placeholder detection ──────────────────────────────────────────────

// detectPlaceholders scans for `:N` (positional bind) or `:Bxxx` /
// `:identifier` (named bind) outside string literals. PL/SQL block
// hosts (`:host`) are also caught.
//
// We do NOT flag `::` (PG-style cast operator) — Oracle doesn't use
// that syntax anyway; if a user copies PG SQL with `::` casts, the
// EXPLAIN will fail with a parse error which surfaces clearly.
func detectPlaceholders(sql string) *sqltune.PlaceholderError {
	var found []string
	inSingle, inDouble := false, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case c == '\\' && i+1 < len(sql):
			i++ // skip escaped char
		case c == '\'' && !inDouble:
			// Oracle uses '' to escape single quote inside strings, not \.
			// But '' is naturally two transitions in/out which works.
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case inSingle || inDouble:
			// in literal — skip
		case c == ':':
			// Skip :: (not Oracle syntax but defensively handle)
			if i+1 < len(sql) && sql[i+1] == ':' {
				i++
				continue
			}
			// Parse :Bxxx / :identifier / :N
			j := i + 1
			for j < len(sql) && isBindIdentChar(sql[j]) {
				j++
			}
			if j > i+1 {
				found = append(found, sql[i:j])
				i = j - 1
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	return &sqltune.PlaceholderError{
		SQL:          sql,
		Placeholders: found,
		DetectedKind: "oracle_colon",
		Suggestion: fmt.Sprintf(
			"SQL 含 %d 个未绑定占位符 %v。Oracle 归一化 SQL 来自 V$SQL（已剥离字面量），"+
				"无字面量无法 EXPLAIN PLAN。请走以下任一恢复路径：\n"+
				"  1. SELECT name, value_string FROM V$SQL_BIND_CAPTURE WHERE sql_id = '<id>' — 拿历史绑定值\n"+
				"  2. 从应用 trace 日志拉实际绑定 SQL\n"+
				"  3. 手动把 :1/:B1 替换成代表值后重试",
			len(found), found),
		Recoverable: true,
	}
}

// isBindIdentChar returns true for chars valid in an Oracle bind name.
// Oracle bind names can be alphanumeric + `_` after the colon.
func isBindIdentChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '_'
}

// ── Helpers ────────────────────────────────────────────────────────────

// newStatementID generates a unique-enough ID for PLAN_TABLE isolation.
// 16 hex chars (8 random bytes) gives ~3.4e19 distinct values —
// collision probability under sane concurrent use is essentially zero.
// Prefixed with "opendb_" so DBAs can identify our rows in PLAN_TABLE.
func newStatementID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Crypto rand should never fail in practice; if it does fall
		// back to a fixed prefix (collision-prone but safe to use once).
		return "opendb_fallback"
	}
	return "opendb_" + hex.EncodeToString(b[:])
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
	}
	return ""
}
