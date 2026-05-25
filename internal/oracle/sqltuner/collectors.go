/*-------------------------------------------------------------------------
 *
 * collectors.go
 *	  Oracle schema / dialect / runtime / view / placeholder methods
 *	  for sqltune.DialectPlanner.
 *
 *	  Schema source: USER_/ALL_/DBA_ data dictionary views.
 *	  - DBA_TABLES requires SYSDBA — use ALL_TABLES as default
 *	    (visible to current user with grants)
 *	  - For sqltune purposes the table info we need (num_rows / blocks /
 *	    avg_row_len) is in user_tables / all_tables / all_tab_columns
 *	    / all_indexes / all_tab_col_statistics
 *
 *	  Dialect parameters: V$PARAMETER subset relevant to CBO. Curated
 *	  list — Oracle has 1000+ init.ora params, throwing them all at
 *	  the LLM dilutes signal.
 *
 *	  Runtime: V$SESSION wait events + V$LOCK filtered to involved
 *	  tables. We don't try to be exhaustive — LLM can ask follow-up
 *	  queries if it needs more.
 *
 *	  Views expansion: ALL_VIEWS.TEXT_VC (LONG → VARCHAR2 in modern
 *	  Oracle). Best-effort.
 *
 *	  Placeholders: already implemented in explain.go.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/collectors.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// keyOracleParams is the curated CBO-relevant V$PARAMETER subset.
// Reflects what LLMs can reason about from Oracle 19c+ documentation.
var keyOracleParams = []string{
	"optimizer_mode",
	"optimizer_features_enable",
	"optimizer_index_cost_adj",
	"optimizer_index_caching",
	"optimizer_dynamic_sampling",
	"optimizer_adaptive_plans",
	"optimizer_adaptive_statistics",
	"db_file_multiblock_read_count",
	"hash_area_size",
	"sort_area_size",
	"pga_aggregate_target",
	"sga_target",
	"db_cache_size",
	"shared_pool_size",
	"parallel_max_servers",
	"parallel_degree_policy",
	"cursor_sharing",
	"session_cached_cursors",
	"open_cursors",
	"statistics_level",
}

// CollectSchema queries ALL_TABLES / ALL_INDEXES / ALL_TAB_COL_STATISTICS
// for each referenced table. Skips Oracle system schemas.
func (p *oraclePlanner) CollectSchema(ctx context.Context, sql string) (*sqltune.SchemaInfo, []string, error) {
	tables := extractTableNamesOracle(sql)
	info := &sqltune.SchemaInfo{
		Tables:  map[string]*sqltune.TableInfo{},
		Indexes: map[string][]sqltune.IndexInfo{},
		Stats:   map[string][]sqltune.ColStat{},
	}
	if len(tables) == 0 {
		return info, nil, nil
	}

	// Oracle wants UPPER table names since identifiers are case-folded.
	upperTables := make([]string, len(tables))
	for i, t := range tables {
		upperTables[i] = strings.ToUpper(t)
	}

	if err := p.fetchTablesOracle(ctx, upperTables, info); err != nil {
		_ = err // non-fatal
	}
	if err := p.fetchIndexesOracle(ctx, upperTables, info); err != nil {
		_ = err
	}
	if err := p.fetchStatsOracle(ctx, upperTables, info); err != nil {
		_ = err
	}

	return info, tables, nil
}

func (p *oraclePlanner) fetchTablesOracle(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT table_name,
	             owner,
	             NVL(num_rows, 0),
	             NVL(blocks, 0),
	             NVL(avg_row_len, 0)
	        FROM all_tables
	       WHERE owner NOT IN ('SYS','SYSTEM','SYSAUX','APEX_040000','OUTLN','DBSNMP')
	         AND table_name IN (` + sqlInListOracle(tables) + `)`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		name := toString(row[0])
		ti := &sqltune.TableInfo{
			Name:   name,
			Schema: toString(row[1]),
			Tuples: parseInt(row[2]),
			Pages:  parseInt(row[3]),
			Kind:   "r", // Oracle TABLES are heap by default
		}
		if avg := parseInt(row[4]); avg > 0 && ti.Tuples > 0 {
			ti.SizeMB = float64(ti.Tuples*avg) / (1024.0 * 1024.0)
		}
		info.Tables[name] = ti
	}
	return nil
}

func (p *oraclePlanner) fetchIndexesOracle(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	// Join with all_ind_columns to get column list per index.
	q := `SELECT i.table_name,
	             i.index_name,
	             CASE WHEN i.uniqueness = 'UNIQUE' THEN 1 ELSE 0 END,
	             LISTAGG(c.column_name, ',') WITHIN GROUP (ORDER BY c.column_position) AS cols
	        FROM all_indexes i
	        JOIN all_ind_columns c ON c.index_name = i.index_name AND c.table_name = i.table_name
	       WHERE i.table_name IN (` + sqlInListOracle(tables) + `)
	    GROUP BY i.table_name, i.index_name, i.uniqueness`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		table := toString(row[0])
		ii := sqltune.IndexInfo{
			Name:    toString(row[1]),
			Unique:  parseInt(row[2]) == 1,
			Columns: strings.Split(toString(row[3]), ","),
		}
		info.Indexes[table] = append(info.Indexes[table], ii)
	}
	return nil
}

// fetchStatsOracle gathers per-column statistics — the equivalent of
// PG's pg_stats. Oracle stores these in all_tab_col_statistics
// (which is the user-accessible projection of dba_tab_col_statistics).
func (p *oraclePlanner) fetchStatsOracle(ctx context.Context, tables []string, info *sqltune.SchemaInfo) error {
	q := `SELECT table_name,
	             column_name,
	             NVL(num_distinct, 0),
	             NVL(num_nulls, 0),
	             NVL(avg_col_len, 0),
	             NVL(density, 0)
	        FROM all_tab_col_statistics
	       WHERE table_name IN (` + sqlInListOracle(tables) + `)
	         AND ROWNUM <= 500`
	res, err := p.driver.Query(ctx, q)
	if err != nil {
		return err
	}
	for _, row := range res.Rows {
		table := toString(row[0])
		cs := sqltune.ColStat{
			Table:     table,
			Column:    toString(row[1]),
			NDistinct: parseFloat(row[2]),
			NullFrac:  parseFloat(row[3]), // Note: Oracle stores count, not fraction. LLM can compute.
			AvgWidth:  int(parseInt(row[4])),
		}
		info.Stats[table] = append(info.Stats[table], cs)
	}
	return nil
}

// SnapshotDialect: V$VERSION banner + curated V$PARAMETER subset.
func (p *oraclePlanner) SnapshotDialect(ctx context.Context) (*sqltune.DialectInfo, error) {
	info := &sqltune.DialectInfo{Parameters: map[string]string{}}

	if res, err := p.driver.Query(ctx,
		"SELECT banner FROM v$version WHERE ROWNUM = 1"); err == nil && len(res.Rows) > 0 {
		info.Version = toString(res.Rows[0][0])
	}

	// Single round-trip for all params via IN list.
	paramQ := `SELECT name, value FROM v$parameter WHERE name IN (` + sqlInListOracle(keyOracleParams) + `)`
	if res, err := p.driver.Query(ctx, paramQ); err == nil {
		for _, row := range res.Rows {
			info.Parameters[toString(row[0])] = toString(row[1])
		}
	}

	// Standby presence
	if res, err := p.driver.Query(ctx,
		"SELECT COUNT(*) FROM v$dataguard_config WHERE ROWNUM <= 1"); err == nil && len(res.Rows) > 0 {
		if parseInt(res.Rows[0][0]) > 0 {
			info.HighAvailability = true
		}
	}

	// Partitioned tables
	if res, err := p.driver.Query(ctx,
		"SELECT 1 FROM all_part_tables WHERE ROWNUM = 1"); err == nil && len(res.Rows) > 0 {
		info.HasPartitionedTab = true
	}

	return info, nil
}

// SnapshotRuntime: top wait events + locks on involved tables.
func (p *oraclePlanner) SnapshotRuntime(ctx context.Context, involvedTables []string) (*sqltune.RuntimeInfo, error) {
	info := &sqltune.RuntimeInfo{}

	waitQ := `SELECT wait_class, event, COUNT(*)
	            FROM v$session
	           WHERE wait_class <> 'Idle'
	             AND event IS NOT NULL
	        GROUP BY wait_class, event
	        ORDER BY 3 DESC FETCH FIRST 20 ROWS ONLY`
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

	if len(involvedTables) > 0 {
		upperTables := make([]string, len(involvedTables))
		for i, t := range involvedTables {
			upperTables[i] = strings.ToUpper(t)
		}
		// v$lock joined with dba_objects to map object_id → table name.
		// Filter conservatively to limit row count.
		lockQ := `SELECT o.object_name, l.type, l.lmode, l.request
		            FROM v$lock l
		            JOIN dba_objects o ON o.object_id = l.id1
		           WHERE o.object_name IN (` + sqlInListOracle(upperTables) + `)
		             AND ROWNUM <= 50`
		if res, err := p.driver.Query(ctx, lockQ); err == nil {
			for _, row := range res.Rows {
				info.Locks = append(info.Locks, sqltune.LockEntry{
					Relation: toString(row[0]),
					LockType: toString(row[1]),
					Mode:     toString(row[2]),
					Granted:  parseInt(row[3]) == 0, // request=0 means granted
				})
			}
		}
	}
	return info, nil
}

// ExpandViews — M5.1 stub. Oracle ALL_VIEWS.TEXT is LONG type which
// many Go drivers need special handling for. Leave for follow-up.
func (p *oraclePlanner) ExpandViews(ctx context.Context, sql string) (string, error) {
	return "", nil
}

func (p *oraclePlanner) NormalizePlaceholders(ctx context.Context, sql string) (string, error) {
	if pe := detectPlaceholders(sql); pe != nil {
		return sql, pe
	}
	return sql, nil
}

// ── SQL parsing helpers (Oracle case-folding aware) ────────────────────

func extractTableNamesOracle(sql string) []string {
	low := strings.ToLower(stripCommentsOracle(sql))
	var tables []string
	seen := map[string]bool{}
	keywords := map[string]bool{
		"from": true, "join": true, "update": true, "into": true,
	}
	words := tokenizeOracle(low)
	for i, w := range words {
		if keywords[w] && i+1 < len(words) {
			name := words[i+1]
			if dot := strings.LastIndex(name, "."); dot >= 0 {
				name = name[dot+1:]
			}
			if isSQLKeywordOracle(name) {
				continue
			}
			if !seen[name] && isIdentifierOracle(name) {
				tables = append(tables, name)
				seen[name] = true
			}
		}
	}
	return tables
}

func stripCommentsOracle(sql string) string {
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

func tokenizeOracle(sql string) []string {
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

func isIdentifierOracle(s string) bool {
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

var oracleKeywords = map[string]bool{
	"select": true, "from": true, "where": true, "group": true, "by": true,
	"having": true, "order": true, "fetch": true, "first": true, "rows": true,
	"only": true, "rownum": true,
	"join": true, "inner": true, "left": true, "right": true, "outer": true, "cross": true, "natural": true,
	"on": true, "using": true, "and": true, "or": true, "not": true, "in": true,
	"exists": true, "between": true, "like": true, "is": true, "null": true,
	"as": true, "with": true, "union": true, "all": true, "distinct": true,
	"case": true, "when": true, "then": true, "else": true, "end": true,
	"update": true, "set": true, "insert": true, "into": true, "values": true,
	"delete": true, "merge": true, "create": true, "table": true, "index": true,
	"connect": true, "start": true,
}

func isSQLKeywordOracle(s string) bool { return oracleKeywords[s] }

// sqlInListOracle returns a comma-separated quoted list. Caller
// ensures items are validated identifiers.
func sqlInListOracle(items []string) string {
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
