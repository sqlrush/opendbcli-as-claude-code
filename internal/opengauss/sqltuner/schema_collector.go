/*-------------------------------------------------------------------------
 *
 * schema_collector.go
 *	  SchemaCollector pulls table / index / stats / FK info for tables
 *	  involved in a SQL.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/schema_collector.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/sqlrush/opendb/internal/db"
)

// SchemaCollector pulls table / index / stats / FK info for tables involved in a SQL.
//
// 4 queries run in parallel (errgroup-style) per design doc §6 M2.
type SchemaCollector struct {
	driver db.Driver
}

func NewSchemaCollector(d db.Driver) *SchemaCollector { return &SchemaCollector{driver: d} }

// Collect extracts table names from SQL via regex, then queries 4 metadata sources in parallel.
func (s *SchemaCollector) Collect(ctx context.Context, sql string) (*SchemaInfo, []string, error) {
	tables := ExtractTableNames(sql)
	if len(tables) == 0 {
		return &SchemaInfo{}, nil, nil
	}

	info := &SchemaInfo{
		Tables:  make(map[string]*TableInfo),
		Indexes: make(map[string][]IndexInfo),
		Stats:   make(map[string][]ColStat),
	}

	// Run 4 metadata queries concurrently
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var errs []error
	collect := func(fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				errMu.Lock()
				errs = append(errs, err)
				errMu.Unlock()
			}
		}()
	}

	collect(func() error { return s.collectTables(ctx, tables, info) })
	collect(func() error { return s.collectIndexes(ctx, tables, info) })
	collect(func() error { return s.collectStats(ctx, tables, info) })
	collect(func() error { return s.collectFKs(ctx, tables, info) })

	wg.Wait()

	if len(errs) > 0 {
		// Don't hard-fail — partial schema is usable. Return first err for visibility.
		return info, tables, fmt.Errorf("partial schema collect failure: %v", errs[0])
	}
	return info, tables, nil
}

// ExtractTableNames pulls table names from FROM/JOIN clauses via regex.
// MVP-grade: catches simple cases (FROM tbl, JOIN tbl, FROM schema.tbl). Doesn't do full SQL parse.
func ExtractTableNames(sql string) []string {
	// Strip comments to avoid matching in /* ... */
	sql = stripComments(sql)

	// Match: FROM <ident>, JOIN <ident>, UPDATE <ident>, INSERT INTO <ident>, DELETE FROM <ident>
	re := regexp.MustCompile(`(?i)\b(?:FROM|JOIN|UPDATE|INSERT\s+INTO|DELETE\s+FROM)\s+([a-zA-Z_][a-zA-Z0-9_]*(?:\.[a-zA-Z_][a-zA-Z0-9_]*)?)`)
	matches := re.FindAllStringSubmatch(sql, -1)

	seen := make(map[string]bool)
	var tables []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		t := m[1]
		// Strip schema prefix if present (use simple table name for pg_class lookup)
		if dot := strings.LastIndex(t, "."); dot >= 0 {
			t = t[dot+1:]
		}
		t = strings.ToLower(t)
		// Skip SQL keywords that may follow FROM (rare false positives)
		if isSQLKeyword(t) {
			continue
		}
		if !seen[t] {
			seen[t] = true
			tables = append(tables, t)
		}
	}
	return tables
}

func stripComments(sql string) string {
	// Remove /* ... */ block comments
	blockRe := regexp.MustCompile(`/\*[\s\S]*?\*/`)
	sql = blockRe.ReplaceAllString(sql, " ")
	// Remove -- line comments
	lineRe := regexp.MustCompile(`--[^\n]*`)
	sql = lineRe.ReplaceAllString(sql, "")
	return sql
}

func isSQLKeyword(s string) bool {
	switch strings.ToLower(s) {
	case "select", "where", "group", "order", "limit", "having", "union", "lateral", "values":
		return true
	}
	return false
}

func (s *SchemaCollector) collectTables(ctx context.Context, tables []string, info *SchemaInfo) error {
	q := fmt.Sprintf(`
SELECT n.nspname AS schema, c.relname AS name,
       c.relpages AS pages, c.reltuples::bigint AS tuples,
       c.relkind AS kind,
       pg_total_relation_size(c.oid) / 1024.0 / 1024.0 AS size_mb
FROM pg_class c
LEFT JOIN pg_namespace n ON c.relnamespace = n.oid
WHERE c.relname IN (%s)
  AND c.relkind IN ('r','v','p','m')`, sqlInList(tables))

	res, err := s.driver.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("collectTables: %w", err)
	}
	for _, row := range res.Rows {
		if len(row) < 6 {
			continue
		}
		t := &TableInfo{
			Schema: asString(row[0]),
			Name:   asString(row[1]),
			Pages:  asInt64(row[2]),
			Tuples: asInt64(row[3]),
			Kind:   asString(row[4]),
			SizeMB: asFloat(row[5]),
		}
		info.Tables[t.Name] = t
	}
	return nil
}

func (s *SchemaCollector) collectIndexes(ctx context.Context, tables []string, info *SchemaInfo) error {
	q := fmt.Sprintf(`
SELECT t.relname AS table_name,
       i.relname AS idx_name,
       ix.indisunique AS is_unique,
       ix.indisprimary AS is_primary,
       array_agg(att.attname ORDER BY att.attnum) AS columns,
       pg_get_indexdef(ix.indexrelid) AS def
FROM pg_class t
JOIN pg_index ix ON t.oid = ix.indrelid
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_attribute att ON att.attrelid = t.oid AND att.attnum = ANY(ix.indkey)
WHERE t.relname IN (%s)
GROUP BY t.relname, i.relname, ix.indisunique, ix.indisprimary, ix.indexrelid`, sqlInList(tables))

	res, err := s.driver.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("collectIndexes: %w", err)
	}
	for _, row := range res.Rows {
		if len(row) < 6 {
			continue
		}
		tbl := asString(row[0])
		idx := IndexInfo{
			Name:       asString(row[1]),
			Unique:     asBool(row[2]),
			Primary:    asBool(row[3]),
			Columns:    asStringSlice(row[4]),
			Definition: asString(row[5]),
		}
		info.Indexes[tbl] = append(info.Indexes[tbl], idx)
	}
	return nil
}

func (s *SchemaCollector) collectStats(ctx context.Context, tables []string, info *SchemaInfo) error {
	q := fmt.Sprintf(`
SELECT tablename, attname, n_distinct, null_frac, avg_width,
       most_common_vals::text, most_common_freqs::text, correlation
FROM pg_stats
WHERE tablename IN (%s)`, sqlInList(tables))

	res, err := s.driver.Query(ctx, q)
	if err != nil {
		return fmt.Errorf("collectStats: %w", err)
	}
	for _, row := range res.Rows {
		if len(row) < 8 {
			continue
		}
		tbl := asString(row[0])
		st := ColStat{
			Table:           tbl,
			Column:          asString(row[1]),
			NDistinct:       asFloat(row[2]),
			NullFrac:        asFloat(row[3]),
			AvgWidth:        int(asInt64(row[4])),
			MostCommonVals:  asString(row[5]),
			MostCommonFreqs: asString(row[6]),
			Correlation:     asFloat(row[7]),
		}
		info.Stats[tbl] = append(info.Stats[tbl], st)
	}
	return nil
}

func (s *SchemaCollector) collectFKs(ctx context.Context, tables []string, info *SchemaInfo) error {
	q := fmt.Sprintf(`
SELECT (conrelid::regclass)::text AS table_name,
       conname AS name,
       pg_get_constraintdef(oid) AS def
FROM pg_constraint
WHERE contype = 'f'
  AND (conrelid::regclass)::text IN (%s)`, sqlInListQuoted(tables))

	res, err := s.driver.Query(ctx, q)
	if err != nil {
		// FK collection is optional — don't fail
		return nil
	}
	for _, row := range res.Rows {
		if len(row) < 3 {
			continue
		}
		info.FKs = append(info.FKs, ForeignKey{
			Table:      asString(row[0]),
			Name:       asString(row[1]),
			Definition: asString(row[2]),
		})
	}
	return nil
}

// ── helpers ──

func sqlInList(items []string) string {
	if len(items) == 0 {
		return "''"
	}
	parts := make([]string, len(items))
	for i, s := range items {
		parts[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return strings.Join(parts, ",")
}

func sqlInListQuoted(items []string) string {
	// For regclass::text matching, we want bare names
	return sqlInList(items)
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	}
	return fmt.Sprintf("%v", v)
}

func asInt64(v any) int64 {
	if v == nil {
		return 0
	}
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

func asFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func asBool(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "t" || x == "true" || x == "TRUE"
	}
	return false
}

func asStringSlice(v any) []string {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case []string:
		return x
	case string:
		// PG array literal: {a,b,c}
		s := strings.TrimPrefix(x, "{")
		s = strings.TrimSuffix(s, "}")
		if s == "" {
			return nil
		}
		return strings.Split(s, ",")
	}
	return nil
}
