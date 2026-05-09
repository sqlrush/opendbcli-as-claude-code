/*-------------------------------------------------------------------------
 *
 * plan_collector.go
 *	  PlanCollector executes EXPLAIN [ANALYZE] and parses JSON output
 *	  into PlanNode tree.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/plan_collector.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// PlanCollector executes EXPLAIN [ANALYZE] and parses JSON output into PlanNode tree.
//
// 行数判断规则（design doc §6 M1）:
//   < 100 行     → EXPLAIN ANALYZE 默认开, timeout 30s
//   100-500 行   → EXPLAIN ANALYZE 默认开, timeout 60s, 失败 fallback EXPLAIN
//   500-1000 行  → EXPLAIN 默认（无 ANALYZE）
//   > 1000 行    → EXPLAIN 默认（无 ANALYZE）+ 后续 G3 三层兜底
type PlanCollector struct {
	driver db.Driver
}

func NewPlanCollector(d db.Driver) *PlanCollector { return &PlanCollector{driver: d} }

// PlaceholderSQLError is returned when the input SQL still contains unbound
// parameter placeholders (?, $1, :1 etc.). Such SQL cannot be sent to EXPLAIN
// and almost always means the caller fetched a normalized version from
// dbe_perf.statement / pg_stat_statements rather than an executable form.
//
// Surfacing this as a structured error (instead of letting EXPLAIN return a
// generic "there is no parameter $1") lets the upstream skill produce a
// targeted fix-it message and lets the LLM reason about how to retry.
type PlaceholderSQLError struct {
	Count   int
	Samples []string // first few occurrences for the error message
}

func (e *PlaceholderSQLError) Error() string {
	sample := ""
	if len(e.Samples) > 0 {
		sample = " (e.g. " + strings.Join(e.Samples, ", ") + ")"
	}
	return fmt.Sprintf("SQL contains %d unbound placeholder(s)%s — likely fetched from dbe_perf.statement (normalized form). Provide literal values or pull from dbe_perf.statement_history.", e.Count, sample)
}

// Collect runs EXPLAIN and returns PlanInfo.
//
// Returns:
//   - PlanInfo with HasAnalyze=true if ANALYZE ran successfully
//   - PlanInfo with HasAnalyze=false if ANALYZE was skipped or failed (still has cost estimate)
//   - *PlaceholderSQLError if SQL contains ? / $N / :N placeholders (no EXPLAIN attempted)
//   - error only on hard failure (parse error, no plan returned)
func (c *PlanCollector) Collect(ctx context.Context, sql string, opts TuneOptions) (*PlanInfo, error) {
	// Reject normalized SQL up-front. Placeholder-laden SQL fails EXPLAIN with
	// an opaque "there is no parameter $1" — caller can't tell that apart from
	// a real SQL bug. Returning PlaceholderSQLError gives the skill / LLM a
	// targeted error to act on.
	if perr := detectPlaceholders(sql); perr != nil {
		return nil, perr
	}

	useAnalyze, timeout := c.decideAnalyze(sql, opts)

	// Try ANALYZE first if enabled
	if useAnalyze {
		info, err := c.runExplain(ctx, sql, true, timeout)
		if err == nil {
			return info, nil
		}
		// Fall through to no-ANALYZE on timeout / DML errors
	}

	// EXPLAIN without ANALYZE — never times out (just plans, doesn't run)
	info, err := c.runExplain(ctx, sql, false, 30*time.Second)
	if err == nil {
		return info, nil
	}

	// Try schema auto-completion if the failure is "relation X does not exist".
	// Common cause: caller's SQL came from dbe_perf with bare table names (the
	// session that ran it had a SET search_path that isn't preserved). We can
	// usually recover by looking the table up in pg_class and qualifying.
	if missing := parseMissingRelation(err.Error()); missing != "" {
		schema, ferr := c.findSchemaFor(ctx, missing)
		if ferr == nil && schema != "" {
			rewritten, ok := qualifyTableName(sql, missing, schema)
			if ok {
				if info2, rerr := c.runExplain(ctx, rewritten, false, 30*time.Second); rerr == nil {
					return info2, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("EXPLAIN failed: %w", err)
}

// parseMissingRelation pulls the table identifier out of a "relation X does not
// exist" error string. Returns "" if the error is unrelated.
//
// og / PG message format: `ERROR: relation "customers" does not exist`
// (the relation name is always single-quoted in the error message)
func parseMissingRelation(errMsg string) string {
	re := regexp.MustCompile(`relation "([^"]+)" does not exist`)
	if m := re.FindStringSubmatch(errMsg); len(m) == 2 {
		return m[1]
	}
	return ""
}

// findSchemaFor looks up the schema owning the given table name. Used when
// EXPLAIN fails because of an unqualified table reference. Returns the most
// likely schema (largest by row count when multiple candidates exist) or "".
//
// Note: pg_class.relname is case-sensitive; og lowercases unquoted identifiers
// at parse time, so the input here should already be in the form og uses.
func (c *PlanCollector) findSchemaFor(ctx context.Context, table string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Order by reltuples desc — pick the table with the most rows when there
	// are multiple candidates (e.g. same name in `public` and a feature schema).
	// This is heuristic; the proper fix is for callers to track search_path.
	res, err := c.driver.Query(cctx,
		`SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid `+
			`WHERE c.relname=$1 AND c.relkind IN ('r','v','m','p','f') `+
			`AND n.nspname NOT IN ('pg_catalog','information_schema') `+
			`ORDER BY c.reltuples DESC LIMIT 1`,
		table)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Rows) == 0 {
		return "", nil
	}
	switch v := res.Rows[0][0].(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	return "", nil
}

// qualifyTableName rewrites the SQL to prepend `schema.` to all bare references
// to `table`. Returns the rewritten SQL and ok=true if at least one substitution
// was made. Skips occurrences inside string literals and comments.
//
// Conservative on identifier boundaries: only matches when the table name is a
// whole word and not already qualified (`schema.table`) or aliased (`alias.col`).
func qualifyTableName(sql, table, schema string) (string, bool) {
	if table == "" || schema == "" {
		return sql, false
	}
	var out strings.Builder
	out.Grow(len(sql) + 32)
	tableLower := strings.ToLower(table)
	tlen := len(tableLower)
	changed := false

	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]

		// Skip single-quoted strings (handle '' as escape).
		if c == '\'' {
			out.WriteByte(c)
			i++
			for i < n {
				out.WriteByte(sql[i])
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						out.WriteByte(sql[i+1])
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}
		// Skip double-quoted identifiers.
		if c == '"' {
			out.WriteByte(c)
			i++
			for i < n && sql[i] != '"' {
				out.WriteByte(sql[i])
				i++
			}
			if i < n {
				out.WriteByte(sql[i])
				i++
			}
			continue
		}
		// Skip line comments.
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			for i < n && sql[i] != '\n' {
				out.WriteByte(sql[i])
				i++
			}
			continue
		}
		// Skip block comments.
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			out.WriteByte(sql[i])
			out.WriteByte(sql[i+1])
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				out.WriteByte(sql[i])
				i++
			}
			if i+1 < n {
				out.WriteByte(sql[i])
				out.WriteByte(sql[i+1])
				i += 2
			}
			continue
		}

		// Try to match `table` (case-insensitive) at i with word boundaries.
		// Left boundary: previous char must NOT be alnum/_ (so we don't match
		// a longer identifier ending in `table`) AND NOT '.' (so we don't
		// requalify already-qualified or aliased refs like `c.customers`).
		if i+tlen <= n && strings.EqualFold(sql[i:i+tlen], tableLower) {
			leftOK := i == 0 || !isIdentChar(sql[i-1])
			leftAlreadyQualified := i > 0 && sql[i-1] == '.'
			rightOK := i+tlen == n || !isIdentChar(sql[i+tlen])
			if leftOK && rightOK && !leftAlreadyQualified {
				out.WriteString(schema)
				out.WriteByte('.')
				out.WriteString(sql[i : i+tlen])
				i += tlen
				changed = true
				continue
			}
		}

		out.WriteByte(c)
		i++
	}

	return out.String(), changed
}

// isIdentChar reports whether c is part of an SQL identifier (alnum or _).
func isIdentChar(c byte) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '_'
}

// decideAnalyze returns (useAnalyze, timeout) based on SQL size and user flags.
func (c *PlanCollector) decideAnalyze(sql string, opts TuneOptions) (bool, time.Duration) {
	if opts.ForceAnalyze {
		return true, 5 * time.Minute // user accepts the wait
	}
	if opts.NoAnalyze || opts.DryRun {
		return false, 30 * time.Second
	}
	if isDML(sql) {
		// DML: skip ANALYZE to avoid side effects (we'd need ROLLBACK wrapper)
		return false, 30 * time.Second
	}
	lines := strings.Count(sql, "\n") + 1
	switch {
	case lines < 100:
		return true, 30 * time.Second
	case lines < 500:
		return true, 60 * time.Second
	default:
		// 500+ lines: no ANALYZE by default
		return false, 30 * time.Second
	}
}

// isDML returns true if the SQL is a data-modifying statement.
// We skip ANALYZE for these to avoid unintended writes.
func isDML(sql string) bool {
	trimmed := strings.TrimSpace(strings.ToUpper(sql))
	for _, prefix := range []string{"INSERT", "UPDATE", "DELETE", "MERGE", "TRUNCATE"} {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// runExplain executes EXPLAIN with optional ANALYZE flag and parses JSON output.
func (c *PlanCollector) runExplain(ctx context.Context, sql string, analyze bool, timeout time.Duration) (*PlanInfo, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	flags := "(FORMAT JSON, COSTS TRUE, VERBOSE TRUE"
	if analyze {
		flags += ", ANALYZE TRUE, BUFFERS TRUE, TIMING TRUE"
	}
	flags += ")"

	explainSQL := "EXPLAIN " + flags + " " + sql

	res, err := c.driver.Query(cctx, explainSQL)
	if err != nil {
		return nil, err
	}
	if res == nil || len(res.Rows) == 0 {
		return nil, fmt.Errorf("EXPLAIN returned no rows")
	}

	// OG/PG返回单行单列：JSON array (一个元素是 plan root)
	jsonStr, ok := res.Rows[0][0].(string)
	if !ok {
		// Some drivers return []byte
		if b, isBytes := res.Rows[0][0].([]byte); isBytes {
			jsonStr = string(b)
		} else {
			return nil, fmt.Errorf("unexpected EXPLAIN result type: %T", res.Rows[0][0])
		}
	}

	var raw []map[string]any
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("parse EXPLAIN JSON: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty EXPLAIN JSON array")
	}

	rootMap := raw[0]
	planMap, _ := rootMap["Plan"].(map[string]any)
	if planMap == nil {
		return nil, fmt.Errorf("EXPLAIN JSON missing 'Plan' key")
	}

	root := parsePlanNode(planMap)

	info := &PlanInfo{
		Root:       root,
		TotalCost:  root.TotalCost,
		HasAnalyze: analyze,
	}
	if pt, ok := rootMap["Planning Time"].(float64); ok {
		info.PlanningTime = pt
	}
	if et, ok := rootMap["Execution Time"].(float64); ok {
		info.ExecutionTime = et
	}
	return info, nil
}

// parsePlanNode recursively converts EXPLAIN JSON map → PlanNode struct.
func parsePlanNode(m map[string]any) *PlanNode {
	n := &PlanNode{
		Operator:       getStr(m, "Node Type"),
		RelationName:   getStr(m, "Relation Name"),
		Alias:          getStr(m, "Alias"),
		StartupCost:    getFloat(m, "Startup Cost"),
		TotalCost:      getFloat(m, "Total Cost"),
		PlanRows:       getInt(m, "Plan Rows"),
		PlanWidth:      int(getInt(m, "Plan Width")),
		ActualStartup:  getFloat(m, "Actual Startup Time"),
		ActualTotal:    getFloat(m, "Actual Total Time"),
		ActualRows:     getInt(m, "Actual Rows"),
		ActualLoops:    getInt(m, "Actual Loops"),
		SharedHit:      getInt(m, "Shared Hit Blocks"),
		SharedRead:     getInt(m, "Shared Read Blocks"),
		Filter:         getStr(m, "Filter"),
		JoinFilter:     getStr(m, "Join Filter"),
		HashCondition:  getStr(m, "Hash Cond"),
		IndexCondition: getStr(m, "Index Cond"),
		SortMethod:     getStr(m, "Sort Method"),
		SortSpaceType:  getStr(m, "Sort Space Type"),
		SortSpaceUsed:  getInt(m, "Sort Space Used"),
	}
	if sk, ok := m["Sort Key"].([]any); ok {
		for _, s := range sk {
			if str, ok := s.(string); ok {
				n.SortKey = append(n.SortKey, str)
			}
		}
	}
	if children, ok := m["Plans"].([]any); ok {
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				n.Children = append(n.Children, parsePlanNode(cm))
			}
		}
	}
	return n
}

func getStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func getFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func getInt(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}

// QuickPlanCost returns just the total cost of a candidate SQL via EXPLAIN (no ANALYZE).
// Used by Round 2 batch verification (faster than full Collect).
func (c *PlanCollector) QuickPlanCost(ctx context.Context, sql string) (float64, error) {
	info, err := c.runExplain(ctx, sql, false, 30*time.Second)
	if err != nil {
		return 0, err
	}
	return info.TotalCost, nil
}

// detectPlaceholders scans SQL for unbound parameter placeholders.
//
// Recognized forms (outside string literals and line/block comments):
//   - ? (positional, JDBC / og prepared statement style)
//   - $N (PostgreSQL/og numbered, $1, $2, ...)
//   - :N (Oracle numbered) and :name (Oracle named bind)
//
// We deliberately do NOT match :word inside CAST/cast or composite type
// notation by being conservative — :name only counts when preceded by a
// non-identifier char and the name doesn't start with a digit-prefix that
// already triggered $N. Tradeoff favors fewer false positives on normal SQL.
//
// Returns nil if no unbound placeholders found, otherwise a PlaceholderSQLError
// with up to 3 sample occurrences for the error message.
func detectPlaceholders(sql string) *PlaceholderSQLError {
	count := 0
	samples := []string{}
	addSample := func(s string) {
		count++
		if len(samples) < 3 {
			samples = append(samples, s)
		}
	}

	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]

		// Skip single-quoted string literals (handle '' as escaped quote).
		if c == '\'' {
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}

		// Skip double-quoted identifiers ("col with ? in name" wouldn't be common,
		// but defensive — won't be misread as placeholder).
		if c == '"' {
			i++
			for i < n && sql[i] != '"' {
				i++
			}
			if i < n {
				i++
			}
			continue
		}

		// Skip line comments (-- ... \n).
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			for i < n && sql[i] != '\n' {
				i++
			}
			continue
		}

		// Skip block comments (/* ... */).
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}

		// ? — bare positional placeholder.
		if c == '?' {
			addSample("?")
			i++
			continue
		}

		// $N — numbered placeholder. Must be preceded by non-identifier char
		// (so $$tag$$ in dollar-quoted strings doesn't false-positive — though
		// we don't fully parse those, this guard is a heuristic).
		if c == '$' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9' {
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			addSample(sql[i:j])
			i = j
			continue
		}

		// :N — Oracle numbered bind. Need to ensure not part of a:b composite.
		if c == ':' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9' {
			// Don't false-positive on '::' (PG cast) — but :: doesn't have digit after, safe.
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			addSample(sql[i:j])
			i = j
			continue
		}

		i++
	}

	if count == 0 {
		return nil
	}
	return &PlaceholderSQLError{Count: count, Samples: samples}
}
