/*-------------------------------------------------------------------------
 *
 * explain.go
 *	  PostgreSQL EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS, VERBOSE,
 *	  COSTS, SETTINGS) capture + parsing via the neutral PG-family
 *	  parser in internal/sqltune.
 *
 *	  Why these EXPLAIN options:
 *	    - FORMAT JSON   — structured output the neutral parser handles
 *	    - ANALYZE       — real execution; gives actual_rows vs plan_rows
 *	                      so LLM can flag stats staleness
 *	    - BUFFERS       — shared_hit / shared_read blocks → IO pattern
 *	    - VERBOSE       — output columns + qualified names
 *	    - COSTS         — startup/total cost (default but explicit for clarity)
 *	    - SETTINGS      — non-default GUC values active at plan time
 *	                      (PG 12+; older versions silently ignored)
 *
 *	  DML safety: EXPLAIN ANALYZE actually executes the SQL. For
 *	  INSERT/UPDATE/DELETE we wrap in BEGIN/ROLLBACK so the changes
 *	  don't persist. Auto-detection uses simple keyword prefix check;
 *	  if it's a CTE-form DML (WITH ... DELETE) we conservatively wrap.
 *
 *	  Placeholder handling: PG SQL fetched from pg_stat_statements
 *	  comes normalized as `$1`, `$2`, etc. (vs MySQL's `?`). We detect
 *	  both styles since callers occasionally paste JDBC-style `?` SQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/explain.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// ExplainPlan runs PG EXPLAIN (FORMAT JSON, ...) on sql.
//
// Tri-state Analyze:
//   - AnalyzeForce → ANALYZE on (even for DML, wrapped in ROLLBACK txn)
//   - AnalyzeSkip  → estimates only, no execution
//   - AnalyzeAuto  → ANALYZE for read-only queries; estimates-only for DML
func (p *pgPlanner) ExplainPlan(ctx context.Context, sql string, opts sqltune.ExplainOptions) (*sqltune.PlanInfo, error) {
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

	// Build the EXPLAIN command. SETTINGS clause requires PG 12+; older
	// versions reject the option. We optimistically include it and
	// retry without on error.
	cmd := buildExplainCmd(sql, analyze, true /*settings*/)

	// DML + ANALYZE must run in a rolled-back transaction so the
	// EXPLAIN ANALYZE side effect doesn't persist.
	jsonStr, err := p.executeExplain(ctx, cmd, analyze && !isReadOnlyQuery(sql))
	if err != nil && strings.Contains(err.Error(), "SETTINGS") {
		// PG < 12: retry without SETTINGS option.
		cmd = buildExplainCmd(sql, analyze, false)
		jsonStr, err = p.executeExplain(ctx, cmd, analyze && !isReadOnlyQuery(sql))
	}
	if err != nil {
		return nil, fmt.Errorf("pg explain: %w", err)
	}

	return parseExplainJSON(jsonStr, analyze)
}

// QuickPlanCost runs estimate-only EXPLAIN. No ANALYZE → safe for
// repeated invocation during candidate verification.
func (p *pgPlanner) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return 0, pe
	}
	cmd := buildExplainCmd(sql, false, false)
	res, err := p.driver.Query(ctx, cmd)
	if err != nil {
		return 0, err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return 0, fmt.Errorf("empty explain result")
	}
	jsonStr := toString(res.Rows[0][0])
	info, err := parseExplainJSON(jsonStr, false)
	if err != nil {
		return 0, err
	}
	return info.TotalCost, nil
}

// executeExplain runs the EXPLAIN command, optionally wrapped in a
// rolled-back transaction (for DML+ANALYZE). Result is the JSON
// string from row[0][0].
func (p *pgPlanner) executeExplain(ctx context.Context, cmd string, needTx bool) (string, error) {
	if needTx {
		// Driver-level transaction for DML safety. EXPLAIN(ANALYZE) on
		// INSERT/UPDATE/DELETE actually runs the DML; ROLLBACK undoes it.
		tx, err := p.driver.BeginTx(ctx, nil)
		if err != nil {
			return "", fmt.Errorf("begin tx: %w", err)
		}
		defer func() { _ = tx.Rollback() }()
		res, err := tx.Query(ctx, cmd)
		if err != nil {
			return "", err
		}
		if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
			return "", fmt.Errorf("empty result")
		}
		return toString(res.Rows[0][0]), nil
	}
	res, err := p.driver.Query(ctx, cmd)
	if err != nil {
		return "", err
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", fmt.Errorf("empty result")
	}
	return toString(res.Rows[0][0]), nil
}

// buildExplainCmd constructs the EXPLAIN command with the requested
// options. Format is always JSON. COSTS and VERBOSE always on; BUFFERS
// on only with ANALYZE (otherwise PG reports zeros).
func buildExplainCmd(sql string, analyze bool, settings bool) string {
	opts := []string{"FORMAT JSON", "COSTS TRUE", "VERBOSE TRUE"}
	if analyze {
		opts = append(opts, "ANALYZE TRUE", "BUFFERS TRUE")
	}
	if settings {
		opts = append(opts, "SETTINGS TRUE")
	}
	return "EXPLAIN (" + strings.Join(opts, ", ") + ") " + sql
}

// parseExplainJSON unmarshals PG EXPLAIN JSON output and walks the
// "Plan" subtree via the neutral parser. Top-level keys "Planning Time"
// and "Execution Time" are also captured.
func parseExplainJSON(jsonStr string, hasAnalyze bool) (*sqltune.PlanInfo, error) {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("parse EXPLAIN JSON: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty EXPLAIN JSON array")
	}
	root := raw[0]
	planMap, _ := root["Plan"].(map[string]any)
	if planMap == nil {
		return nil, fmt.Errorf("EXPLAIN JSON missing 'Plan' key")
	}
	node := sqltune.ParsePGStylePlanNode(planMap)
	if node == nil {
		return nil, fmt.Errorf("EXPLAIN JSON parse returned nil root")
	}
	info := &sqltune.PlanInfo{
		Root:       node,
		TotalCost:  node.TotalCost,
		HasAnalyze: hasAnalyze,
	}
	if pt, ok := root["Planning Time"].(float64); ok {
		info.PlanningTime = pt
	}
	if et, ok := root["Execution Time"].(float64); ok {
		info.ExecutionTime = et
	}
	return info, nil
}

// ── Placeholder detection ──────────────────────────────────────────────

// detectPlaceholders scans for $N (PG/JDBC numbered) or ? (MySQL/JDBC
// positional) placeholders outside string literals. Returns a typed
// PlaceholderError pointing the caller at pg_stat_statements
// remediation if found.
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
		case inSingle || inDouble:
			// inside literal — skip
		case c == '$':
			// PG positional: $N where N is digits
			j := i + 1
			for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			if j > i+1 {
				found = append(found, sql[i:j])
				i = j - 1
			}
		case c == '?':
			found = append(found, "?")
		}
	}
	if len(found) == 0 {
		return nil
	}
	kind := "pg_dollar"
	if found[0] == "?" {
		kind = "qmark"
	}
	return &sqltune.PlaceholderError{
		SQL:          sql,
		Placeholders: found,
		DetectedKind: kind,
		Suggestion: fmt.Sprintf(
			"SQL 含 %d 个未绑定占位符 %v。PG 归一化 SQL 来自 pg_stat_statements (无字面量，无法 EXPLAIN)。"+
				"\n请从应用日志或 auto_explain 日志拉带字面量的实例; 或手动把 $1/? 替换成真值后重试。"+
				"\n参考: SELECT query FROM pg_stat_statements WHERE queryid = <id> LIMIT 1; (得到的是归一化版本，仅用于参考结构)",
			len(found), found),
		Recoverable: true,
	}
}

// ── SQL helpers ────────────────────────────────────────────────────────

func isReadOnlyQuery(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < 4 {
		return false
	}
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "VALUES") {
		return true
	}
	if strings.HasPrefix(upper, "WITH ") {
		// CTE form: if any modifying keyword appears as a standalone
		// word (delimited by non-identifier chars), treat as DML.
		// Standalone-word check via containsWord catches `(DELETE`,
		// `,UPDATE`, ` INSERT\n` etc. — anything where the keyword
		// isn't embedded in a larger identifier.
		for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "MERGE"} {
			if containsWord(upper, kw) {
				return false
			}
		}
		return true
	}
	return false
}

// containsWord returns true if needle appears in haystack with
// non-identifier characters on both sides (or at start/end). Used to
// detect SQL keywords without false-positives on names containing the
// keyword as a substring (e.g. "DELETED_AT" should NOT match "DELETE").
func containsWord(haystack, needle string) bool {
	i := 0
	for {
		idx := strings.Index(haystack[i:], needle)
		if idx < 0 {
			return false
		}
		start := i + idx
		end := start + len(needle)
		leftOK := start == 0 || !isIdentChar(haystack[start-1])
		rightOK := end == len(haystack) || !isIdentChar(haystack[end])
		if leftOK && rightOK {
			return true
		}
		i = end
	}
}

func isIdentChar(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
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
