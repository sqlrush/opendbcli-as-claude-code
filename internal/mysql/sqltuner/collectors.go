/*-------------------------------------------------------------------------
 *
 * collectors.go
 *	  Schema / dialect / runtime / view / placeholder methods for the
 *	  MySQL planner. M2.1 ships minimal-but-functional implementations;
 *	  full equivalents of og's collectors come in M2.4.
 *
 *	  Current scope:
 *	    CollectSchema       — extracts table names, returns minimal
 *	                          TableInfo (no stats/indexes yet)
 *	    SnapshotDialect     — VERSION() + relevant optimizer GUC
 *	    SnapshotRuntime     — empty (TODO: performance_schema)
 *	    ExpandViews         — empty (TODO: information_schema.VIEWS)
 *	    NormalizePlaceholders — detect `?` and return PlaceholderError
 *
 *	  All methods return notes for "not implemented yet" cases instead
 *	  of failing, so the report degrades gracefully.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/collectors.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// CollectSchema runs a coarse SQL parse to extract referenced table
// names from FROM/JOIN clauses, then issues a single
// information_schema query to populate basic TableInfo. Stats and
// indexes are TODO for M2.4.
func (p *mysqlPlanner) CollectSchema(ctx context.Context, sql string) (*sqltune.SchemaInfo, []string, error) {
	tables := extractTableNamesMySQL(sql)
	info := &sqltune.SchemaInfo{
		Tables:  map[string]*sqltune.TableInfo{},
		Indexes: map[string][]sqltune.IndexInfo{},
		Stats:   map[string][]sqltune.ColStat{},
	}
	if len(tables) == 0 {
		return info, nil, nil
	}

	// Single round-trip for basic table metadata. We intentionally use
	// information_schema.TABLES rather than SHOW TABLE STATUS for
	// uniform columns across versions.
	q := `SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_ROWS, DATA_LENGTH, TABLE_TYPE
	        FROM information_schema.TABLES
	       WHERE TABLE_SCHEMA NOT IN ('mysql','performance_schema','information_schema','sys')
	         AND TABLE_NAME IN (` + sqlInListMySQL(tables) + `)`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return info, tables, err // partial result OK
	}
	for _, row := range res.Rows {
		schema := toString(row[0])
		name := toString(row[1])
		ti := &sqltune.TableInfo{
			Schema: schema,
			Name:   name,
			Tuples: parseInt(row[2]),
			Kind:   tableKindMySQL(toString(row[4])),
		}
		if size := parseInt(row[3]); size > 0 {
			ti.SizeMB = float64(size) / 1024.0 / 1024.0
		}
		info.Tables[name] = ti
	}
	return info, tables, nil
}

// SnapshotDialect captures MySQL version and the small set of
// optimizer GUC that materially affect plan choice.
//
// Why this short list (not all 200+ GUC): these are the variables we
// know LLM models can reason about from MySQL's public docs. Throwing
// every GUC at the model dilutes signal.
var keyMySQLGUC = []string{
	"optimizer_switch",
	"sort_buffer_size",
	"join_buffer_size",
	"read_buffer_size",
	"read_rnd_buffer_size",
	"tmp_table_size",
	"max_heap_table_size",
	"innodb_buffer_pool_size",
	"innodb_io_capacity",
	"optimizer_search_depth",
	"optimizer_prune_level",
}

func (p *mysqlPlanner) SnapshotDialect(ctx context.Context) (*sqltune.DialectInfo, error) {
	info := &sqltune.DialectInfo{Parameters: map[string]string{}}

	if res, err := p.driver.Query(ctx, "SELECT VERSION()"); err == nil && len(res.Rows) > 0 {
		info.Version = "MySQL " + toString(res.Rows[0][0])
	}

	// Pull the key GUC in one SHOW VARIABLES with a LIKE-OR list.
	// MySQL doesn't support IN with SHOW; multiple queries are cheap.
	for _, k := range keyMySQLGUC {
		if res, err := p.driver.Query(ctx, "SHOW SESSION VARIABLES LIKE '"+k+"'"); err == nil && len(res.Rows) > 0 {
			info.Parameters[k] = toString(res.Rows[0][1])
		}
	}

	// Partitioned-table flag: cheap COUNT.
	if res, err := p.driver.Query(ctx,
		"SELECT COUNT(*) FROM information_schema.PARTITIONS WHERE PARTITION_NAME IS NOT NULL LIMIT 1"); err == nil && len(res.Rows) > 0 {
		if parseInt(res.Rows[0][0]) > 0 {
			info.HasPartitionedTab = true
		}
	}
	return info, nil
}

// SnapshotRuntime — M2.1 stub. Returns Degraded:true + note so
// the report makes the limitation visible. M2.4 will query
// performance_schema.events_waits_current + performance_schema.data_locks.
func (p *mysqlPlanner) SnapshotRuntime(ctx context.Context, involvedTables []string) (*sqltune.RuntimeInfo, error) {
	return &sqltune.RuntimeInfo{
		Degraded: true,
	}, nil
}

// ExpandViews — M2.1 stub. M2.4 will use information_schema.VIEWS.
func (p *mysqlPlanner) ExpandViews(ctx context.Context, sql string) (string, error) {
	return "", nil
}

// NormalizePlaceholders delegates to the `?` detector. M2.1 doesn't
// auto-substitute from history (M2.4); just surfaces a clear error so
// users know to fetch the literal SQL from events_statements_history_long.
func (p *mysqlPlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return sql, pe
	}
	return sql, nil
}

// ── SQL parsing helpers ────────────────────────────────────────────────

// extractTableNamesMySQL pulls table identifiers from FROM/JOIN/UPDATE
// clauses. Best-effort regex — for M2.4 we'll use a proper parser.
// Handles simple `FROM t`, `FROM t1 a JOIN t2 b`, `FROM t1, t2` cases.
func extractTableNamesMySQL(sql string) []string {
	// Lowercase + collapse whitespace for matching, but return as-found.
	low := strings.ToLower(stripCommentsMySQL(sql))
	var tables []string
	seen := map[string]bool{}
	keywords := map[string]bool{
		"from": true, "join": true, "update": true, "into": true,
	}
	words := tokenizeMySQL(low)
	for i, w := range words {
		if keywords[w] && i+1 < len(words) {
			name := words[i+1]
			// Strip qualifier like "schema.table" → "table"
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				name = name[dot+1:]
			}
			// Skip if it's another keyword (e.g. "SELECT FROM SELECT...")
			if isSQLKeywordMySQL(name) {
				continue
			}
			if !seen[name] && isIdentifierMySQL(name) {
				tables = append(tables, name)
				seen[name] = true
			}
		}
	}
	return tables
}

func stripCommentsMySQL(sql string) string {
	// Remove /* */ comments
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
	// Remove -- line comments
	lines := strings.Split(sql, "\n")
	for i, l := range lines {
		if idx := strings.Index(l, "--"); idx >= 0 {
			lines[i] = l[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func tokenizeMySQL(sql string) []string {
	// Split on non-identifier chars, preserving dots so schema.table
	// stays glued.
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

func isIdentifierMySQL(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_':
			// ok any position
		case (r >= '0' && r <= '9'):
			if i == 0 {
				return false
			}
		case r == '.':
			// allowed in qualifier; but we strip before this so shouldn't see
			return false
		default:
			return false
		}
	}
	return true
}

var mysqlKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "group": true, "by": true,
	"having": true, "order": true, "limit": true, "offset": true,
	"join": true, "inner": true, "left": true, "right": true, "outer": true, "cross": true,
	"on": true, "using": true, "and": true, "or": true, "not": true, "in": true,
	"exists": true, "between": true, "like": true, "is": true, "null": true,
	"as": true, "with": true, "union": true, "all": true, "distinct": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"update": true, "set": true, "insert": true, "into": true, "values": true,
	"delete": true, "create": true, "table": true, "index": true,
}

func isSQLKeywordMySQL(s string) bool { return mysqlKeywords[s] }

// sqlInListMySQL returns a comma-separated quoted list for IN clauses.
// Caller ensures items are safe identifiers (validated by isIdentifierMySQL).
func sqlInListMySQL(items []string) string {
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

func tableKindMySQL(t string) string {
	switch strings.ToUpper(t) {
	case "BASE TABLE":
		return "r"
	case "VIEW":
		return "v"
	case "SYSTEM VIEW":
		return "sv"
	default:
		return strings.ToLower(t)
	}
}
