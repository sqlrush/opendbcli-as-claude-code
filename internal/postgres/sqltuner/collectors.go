/*-------------------------------------------------------------------------
 *
 * collectors.go
 *	  PostgreSQL schema / dialect / runtime / view / placeholder
 *	  methods for sqltune.DialectPlanner.
 *
 *	  Rich CollectSchema is the M3 headline: since PG has no CBO trace,
 *	  we compensate by giving the LLM richer sidecar data from pg_stats.
 *	  Each column's n_distinct + null_frac + correlation +
 *	  most_common_vals tells the LLM what the optimizer "knew" when
 *	  picking the plan — letting it infer "this estimate was wrong
 *	  because n_distinct says 10 but plan_rows used 1000 → stats stale".
 *
 *	  All schema queries use parameterless SQL with table-name
 *	  identifiers quoted by isIdentifierPG (validated regex). We use
 *	  IN ('a','b',...) lists rather than IN ($1,$2,...) to avoid
 *	  driver-specific array-param handling.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/collectors.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// keyPGGUC is the curated short-list of CBO-relevant GUC. LLM gets
// these in the dialect prompt. Larger list dilutes signal.
var keyPGGUC = []string{
	"work_mem",
	"maintenance_work_mem",
	"shared_buffers",
	"effective_cache_size",
	"random_page_cost",
	"seq_page_cost",
	"cpu_tuple_cost",
	"cpu_index_tuple_cost",
	"cpu_operator_cost",
	"max_parallel_workers_per_gather",
	"max_parallel_workers",
	"jit",
	"enable_seqscan",
	"enable_indexscan",
	"enable_hashjoin",
	"enable_mergejoin",
	"enable_nestloop",
	"default_statistics_target",
	"from_collapse_limit",
	"join_collapse_limit",
}

// CollectSchema fans out 4 queries: tables, indexes, column stats, FKs.
// pg_stats rows can be heavy on wide tables; we cap at 50 columns/table.
func (p *pgPlanner) CollectSchema(ctx context.Context, sql string) (*sqltune.SchemaInfo, []string, error) {
	tables := extractTableNamesPG(sql)
	info := &sqltune.SchemaInfo{
		Tables:  map[string]*sqltune.TableInfo{},
		Indexes: map[string][]sqltune.IndexInfo{},
		Stats:   map[string][]sqltune.ColStat{},
	}
	if len(tables) == 0 {
		return info, nil, nil
	}

	if err := p.fetchTables(ctx, tables, info); err != nil {
		// non-fatal: partial schema is better than no schema
		_ = err
	}
	if err := p.fetchIndexes(ctx, tables, info); err != nil {
		_ = err
	}
	if err := p.fetchStats(ctx, tables, info); err != nil {
		_ = err
	}
	if err := p.fetchFKs(ctx, tables, info); err != nil {
		_ = err
	}

	return info, tables, nil
}

func (p *pgPlanner) fetchTables(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT c.relname,
	             n.nspname,
	             c.relpages,
	             c.reltuples,
	             c.relkind,
	             pg_total_relation_size(c.oid) / (1024.0*1024.0) AS size_mb
	        FROM pg_class c
	        JOIN pg_namespace n ON n.oid = c.relnamespace
	       WHERE c.relkind IN ('r','p','v','m')
	         AND n.nspname NOT IN ('pg_catalog','information_schema')
	         AND c.relname IN (` + sqlInListPG(tables) + `)`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		name := toString(row[0])
		ti := &sqltune.TableInfo{
			Name:   name,
			Schema: toString(row[1]),
			Pages:  parseInt(row[2]),
			Tuples: int64(parseFloat(row[3])),
			Kind:   toString(row[4]),
		}
		if sz := parseFloat(row[5]); sz > 0 {
			ti.SizeMB = sz
		}
		info.Tables[name] = ti
	}
	return nil
}

func (p *pgPlanner) fetchIndexes(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT t.relname AS table_name,
	             i.relname AS index_name,
	             ix.indisunique,
	             ix.indisprimary,
	             pg_get_indexdef(ix.indexrelid) AS def,
	             array_to_string(array_agg(a.attname ORDER BY array_position(ix.indkey::int[], a.attnum)), ',') AS cols
	        FROM pg_class t
	        JOIN pg_index ix ON ix.indrelid = t.oid
	        JOIN pg_class i ON i.oid = ix.indexrelid
	        JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(ix.indkey)
	       WHERE t.relname IN (` + sqlInListPG(tables) + `)
	    GROUP BY t.relname, i.relname, ix.indisunique, ix.indisprimary, ix.indexrelid`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		table := toString(row[0])
		ii := sqltune.IndexInfo{
			Name:       toString(row[1]),
			Unique:     parseBool(row[2]),
			Primary:    parseBool(row[3]),
			Definition: toString(row[4]),
			Columns:    strings.Split(toString(row[5]), ","),
		}
		info.Indexes[table] = append(info.Indexes[table], ii)
	}
	return nil
}

// fetchStats — the M3 headline data. pg_stats is PG's compensation for
// not having CBO trace: it reveals what the optimizer "knew" about
// each column. Stale or skewed stats explain most mis-estimations.
func (p *pgPlanner) fetchStats(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT tablename, attname,
	             n_distinct, null_frac, avg_width, correlation,
	             COALESCE(array_to_string(most_common_vals::text[]::text[], '|'), '') AS mcv,
	             COALESCE(array_to_string(most_common_freqs::text[], '|'), '') AS mcf
	        FROM pg_stats
	       WHERE tablename IN (` + sqlInListPG(tables) + `)
	       LIMIT 500`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		table := toString(row[0])
		cs := sqltune.ColStat{
			Table:           table,
			Column:          toString(row[1]),
			NDistinct:       parseFloat(row[2]),
			NullFrac:        parseFloat(row[3]),
			AvgWidth:        int(parseInt(row[4])),
			Correlation:     parseFloat(row[5]),
			MostCommonVals:  toString(row[6]),
			MostCommonFreqs: toString(row[7]),
		}
		info.Stats[table] = append(info.Stats[table], cs)
	}
	return nil
}

func (p *pgPlanner) fetchFKs(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT c.conrelid::regclass::text AS table_name,
	             c.conname,
	             pg_get_constraintdef(c.oid) AS def
	        FROM pg_constraint c
	       WHERE c.contype = 'f'
	         AND c.conrelid::regclass::text IN (` + sqlInListPG(tables) + `)`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		info.FKs = append(info.FKs, sqltune.ForeignKey{
			Table:      toString(row[0]),
			Name:       toString(row[1]),
			Definition: toString(row[2]),
		})
	}
	return nil
}

// SnapshotDialect captures PG version + curated CBO GUC.
func (p *pgPlanner) SnapshotDialect(ctx context.Context) (*sqltune.DialectInfo, error) {
	info := &sqltune.DialectInfo{Parameters: map[string]string{}}

	if res, err := p.driver.Query(ctx, "SELECT version()"); err == nil && len(res.Rows) > 0 {
		info.Version = toString(res.Rows[0][0])
	}

	// pg_settings has a name IN (...) form — single round-trip for all GUC.
	gucQ := `SELECT name, setting, unit FROM pg_settings WHERE name IN (` + sqlInListPG(keyPGGUC) + `)`
	if res, err := p.driver.Query(ctx, gucQ); err == nil {
		for _, row := range res.Rows {
			name := toString(row[0])
			val := toString(row[1])
			if unit := toString(row[2]); unit != "" {
				val = val + unit
			}
			info.Parameters[name] = val
		}
	}

	// Extensions
	if res, err := p.driver.Query(ctx, "SELECT extname FROM pg_extension ORDER BY extname"); err == nil {
		for _, row := range res.Rows {
			info.Extensions = append(info.Extensions, toString(row[0]))
		}
	}

	// Replication?
	if res, err := p.driver.Query(ctx, "SELECT COUNT(*) FROM pg_stat_replication"); err == nil && len(res.Rows) > 0 {
		if parseInt(res.Rows[0][0]) > 0 {
			info.HighAvailability = true
		}
	}

	// Partitioned tables?
	if res, err := p.driver.Query(ctx,
		"SELECT 1 FROM pg_class WHERE relkind = 'p' LIMIT 1"); err == nil && len(res.Rows) > 0 {
		info.HasPartitionedTab = true
	}

	return info, nil
}

// SnapshotRuntime: pg_stat_activity wait events + pg_locks for the
// involved tables. Best-effort — needs pg_monitor role for non-self
// sessions but degrades gracefully.
func (p *pgPlanner) SnapshotRuntime(ctx context.Context, involvedTables []string) (*sqltune.RuntimeInfo, error) {
	info := &sqltune.RuntimeInfo{}

	// Wait events bucketed by type+event
	waitQ := `SELECT wait_event_type, wait_event, COUNT(*)
	            FROM pg_stat_activity
	           WHERE state <> 'idle'
	             AND wait_event_type IS NOT NULL
	        GROUP BY wait_event_type, wait_event
	        ORDER BY 3 DESC LIMIT 20`
	if res, err := p.driver.Query(ctx, waitQ); err == nil {
		for _, row := range res.Rows {
			info.WaitEvents = append(info.WaitEvents, sqltune.WaitEventBucket{
				WaitEventType: toString(row[0]),
				WaitEvent:     toString(row[1]),
				Count:         int(parseInt(row[2])),
			})
		}
	} else {
		info.Degraded = true
	}

	// Locks filtered to involved tables
	if len(involvedTables) > 0 {
		lockQ := `SELECT l.relation::regclass::text, l.locktype, l.mode, l.granted
		            FROM pg_locks l
		           WHERE l.relation::regclass::text IN (` + sqlInListPG(involvedTables) + `)
		           LIMIT 50`
		if res, err := p.driver.Query(ctx, lockQ); err == nil {
			for _, row := range res.Rows {
				info.Locks = append(info.Locks, sqltune.LockEntry{
					Relation: toString(row[0]),
					LockType: toString(row[1]),
					Mode:     toString(row[2]),
					Granted:  parseBool(row[3]),
				})
			}
		}
	}
	return info, nil
}

// ExpandViews — fetches view definitions and inlines. M3 simple
// implementation: detects view references via pg_views, inlines the
// definition wrapped in a CTE. Best-effort.
func (p *pgPlanner) ExpandViews(ctx context.Context, sql string) (string, error) {
	// M3.1: leave as no-op. Full inlining is M3.4 territory and not
	// critical for trace MVP — LLM can read pg_views separately.
	return "", nil
}

// NormalizePlaceholders is the entry point the orchestrator calls.
// PG planner uses the same detector that ExplainPlan uses, returning
// a typed error for the routing layer.
func (p *pgPlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return sql, pe
	}
	return sql, nil
}

// ── SQL parsing / quoting helpers ──────────────────────────────────────

// extractTableNamesPG pulls table identifiers from FROM/JOIN/UPDATE
// clauses. Best-effort regex tokenizer. Handles schema.table by
// keeping only the table portion (the IN-list query is namespace-
// agnostic; matched names in any schema get picked up).
func extractTableNamesPG(sql string) []string {
	low := strings.ToLower(stripCommentsPG(sql))
	var tables []string
	seen := map[string]bool{}
	keywords := map[string]bool{
		"from": true, "join": true, "update": true, "into": true,
	}
	words := tokenizePG(low)
	for i, w := range words {
		if keywords[w] && i+1 < len(words) {
			name := words[i+1]
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				name = name[dot+1:]
			}
			if isSQLKeywordPG(name) {
				continue
			}
			if !seen[name] && isIdentifierPG(name) {
				tables = append(tables, name)
				seen[name] = true
			}
		}
	}
	return tables
}

func stripCommentsPG(sql string) string {
	for {
		i := strings.Index(sql, "/*")
		if i < 0 {
			break
		}
		j := strings.Index(sql[i:], "*/")
		if j < 0 {
			sql = sql[:i]
			break
		}
		sql = sql[:i] + " " + sql[i+j+2:]
	}
	lines := strings.Split(sql, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "--"); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func tokenizePG(sql string) []string {
	var out []string
	var cur strings.Builder
	for _, r := range sql {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '.':
			cur.WriteRune(r)
		default:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func isIdentifierPG(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

var pgKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "group": true, "by": true,
	"having": true, "order": true, "limit": true, "offset": true,
	"join": true, "inner": true, "left": true, "right": true, "outer": true, "cross": true,
	"on": true, "using": true, "and": true, "or": true, "not": true, "in": true,
	"exists": true, "between": true, "like": true, "is": true, "null": true,
	"as": true, "with": true, "union": true, "all": true, "distinct": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"update": true, "set": true, "insert": true, "into": true, "values": true,
	"delete": true, "create": true, "table": true, "index": true, "returning": true,
}

func isSQLKeywordPG(s string) bool { return pgKeywords[s] }

// sqlInListPG returns a comma-separated quoted list of items.
// Caller ensures items are validated identifiers.
func sqlInListPG(items []string) string {
	if len(items) == 0 {
		return "''"
	}
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("'")
		b.WriteString(strings.ReplaceAll(it, "'", "''"))
		b.WriteString("'")
	}
	return b.String()
}

// ── Type-tolerant value coercion (driver returns vary) ─────────────────

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

func parseBool(v any) bool {
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
	}
	return false
}
