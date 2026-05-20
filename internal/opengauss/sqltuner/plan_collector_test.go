/*-------------------------------------------------------------------------
 *
 * plan_collector_test.go
 *	  Unit tests for placeholder detection in PlanCollector.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/plan_collector_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import "testing"

func TestDetectPlaceholders(t *testing.T) {
	tests := []struct {
		name      string
		sql       string
		wantCount int
	}{
		{
			name:      "executable SQL with literals — no placeholders",
			sql:       "SELECT * FROM customers WHERE email LIKE '%@gmail.com' AND id = 42",
			wantCount: 0,
		},
		{
			name:      "normalized SQL from dbe_perf.statement — JDBC ?",
			sql:       "SELECT * FROM customers WHERE email LIKE ? AND id = ?",
			wantCount: 2,
		},
		{
			name:      "PG numbered $N",
			sql:       "SELECT * FROM customers WHERE id = $1 AND email = $2",
			wantCount: 2,
		},
		{
			name:      "Oracle :N numbered binds",
			sql:       "SELECT * FROM dual WHERE id = :1 AND name = :2",
			wantCount: 2,
		},
		{
			name:      "? inside string literal — must NOT count",
			sql:       "SELECT * FROM t WHERE col = '? in string'",
			wantCount: 0,
		},
		{
			name:      "? inside line comment — must NOT count",
			sql:       "SELECT 1 -- this has a ? in comment\nFROM dual",
			wantCount: 0,
		},
		{
			name:      "? inside block comment — must NOT count",
			sql:       "SELECT 1 /* ? hidden */ FROM dual",
			wantCount: 0,
		},
		{
			name:      "$N inside string literal — must NOT count",
			sql:       "SELECT '$1 placeholder text'",
			wantCount: 0,
		},
		{
			name:      "PG :: cast — must NOT count as :N",
			sql:       "SELECT 'abc'::text, 123::int, current_date::timestamp",
			wantCount: 0,
		},
		{
			name:      "mixed real-world og normalized SQL",
			sql:       `SELECT c.name FROM customers c WHERE UPPER(c.email) LIKE ? AND TO_CHAR(c.created_at,?) = ? AND c.id NOT IN (SELECT id FROM orders WHERE status = ?)`,
			wantCount: 4,
		},
		{
			name:      "doubled single quotes inside string — must NOT count",
			sql:       "SELECT 'it''s ? working' FROM dual",
			wantCount: 0,
		},
		{
			name:      "real og demo SQL (the actual tracked form)",
			sql: `SELECT c.name, p.product_name, SUM(oi.quantity * oi.unit_price) AS revenue, COUNT(*) AS cnt
FROM customers c, orders o, order_items oi, products p
WHERE c.customer_id = o.customer_id
  AND o.order_id = oi.order_id
  AND oi.product_id = p.product_id
  AND (UPPER(c.email) LIKE ? OR UPPER(c.email) LIKE ?)
  AND TO_CHAR(o.order_date,?) = ?
  AND o.order_id NOT IN (SELECT order_id FROM orders WHERE status = ?)
GROUP BY c.name, p.product_name
ORDER BY revenue DESC
LIMIT ?`,
			wantCount: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectPlaceholders(tt.sql)
			if tt.wantCount == 0 {
				if got != nil {
					t.Errorf("expected no placeholders, got %d (samples=%v)", got.Count, got.Samples)
				}
				return
			}
			if got == nil {
				t.Errorf("expected %d placeholders, got nil", tt.wantCount)
				return
			}
			if got.Count != tt.wantCount {
				t.Errorf("count mismatch: want %d, got %d (samples=%v)", tt.wantCount, got.Count, got.Samples)
			}
		})
	}
}

func TestPlaceholderSQLErrorMessage(t *testing.T) {
	e := &PlaceholderSQLError{Count: 4, Samples: []string{"?", "?", "?"}}
	msg := e.Error()
	if msg == "" {
		t.Fatal("error message should not be empty")
	}
	// Spot-check key phrases that the LLM / user needs to see.
	for _, want := range []string{"4 unbound", "dbe_perf.statement_history", "literal"} {
		if !contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestParseMissingRelation(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want string
	}{
		{
			name: "og standard error",
			err:  `ERROR: relation "customers" does not exist`,
			want: "customers",
		},
		{
			name: "wrapped via pg driver",
			err:  `pq: relation "sqltune_demo.foo" does not exist`,
			want: "sqltune_demo.foo",
		},
		{
			name: "different error — should return empty",
			err:  "syntax error at or near 'FROM'",
			want: "",
		},
		{
			name: "no relation in message",
			err:  "permission denied for table customers",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMissingRelation(tt.err)
			if got != tt.want {
				t.Errorf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestQualifyTableName(t *testing.T) {
	tests := []struct {
		name   string
		sql    string
		table  string
		schema string
		want   string
		wantOK bool
	}{
		{
			name:   "single FROM clause",
			sql:    "SELECT * FROM customers WHERE id = 1",
			table:  "customers", schema: "sqltune_demo",
			want:   "SELECT * FROM sqltune_demo.customers WHERE id = 1",
			wantOK: true,
		},
		{
			name:   "multiple occurrences with alias",
			sql:    "SELECT c.name FROM customers c JOIN orders o ON c.id = o.customer_id WHERE c.id IN (SELECT customer_id FROM customers)",
			table:  "customers", schema: "demo",
			want:   "SELECT c.name FROM demo.customers c JOIN orders o ON c.id = o.customer_id WHERE c.id IN (SELECT customer_id FROM demo.customers)",
			wantOK: true,
		},
		{
			name:   "already qualified — must NOT touch",
			sql:    "SELECT * FROM public.customers",
			table:  "customers", schema: "demo",
			want:   "SELECT * FROM public.customers",
			wantOK: false,
		},
		{
			name:   "alias.column reference — must NOT requalify",
			sql:    "SELECT c.customers FROM customers c",
			table:  "customers", schema: "demo",
			want:   "SELECT c.customers FROM demo.customers c", // FROM gets qualified, c.customers (column ref) does not
			wantOK: true,
		},
		{
			name:   "table name inside string literal — must NOT touch",
			sql:    "SELECT 'customers' FROM customers",
			table:  "customers", schema: "demo",
			want:   "SELECT 'customers' FROM demo.customers",
			wantOK: true,
		},
		{
			name:   "case-insensitive match",
			sql:    "SELECT * FROM Customers",
			table:  "customers", schema: "demo",
			want:   "SELECT * FROM demo.Customers",
			wantOK: true,
		},
		{
			name:   "longer identifier ending in same suffix — must NOT match",
			sql:    "SELECT * FROM new_customers", // 'new_customers' ends with 'customers' but is its own ident
			table:  "customers", schema: "demo",
			want:   "SELECT * FROM new_customers",
			wantOK: false,
		},
		{
			name:   "multi-table demo SQL",
			sql:    "SELECT * FROM customers c, orders o WHERE c.customer_id = o.customer_id",
			table:  "customers", schema: "demo",
			want:   "SELECT * FROM demo.customers c, orders o WHERE c.customer_id = o.customer_id",
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := qualifyTableName(tt.sql, tt.table, tt.schema)
			if got != tt.want {
				t.Errorf("rewrite mismatch:\n  want: %s\n  got:  %s", tt.want, got)
			}
			if ok != tt.wantOK {
				t.Errorf("ok mismatch: want %v, got %v", tt.wantOK, ok)
			}
		})
	}
}

// TestDetectUnsupportedStatement: og statement_history contains a lot of
// DDL / utility SQL (CREATE INDEX, ANALYZE, SET, SHOW); EXPLAIN'ing them
// returns opaque "syntax error at or near INDEX". v1.1.50 detects these
// up-front so the caller can surface a clear "skipped — not a plannable
// statement" instead.
func TestDetectUnsupportedStatement(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want string // empty = should be allowed; non-empty = expected Kind
	}{
		{"plain SELECT", "SELECT 1", ""},
		{"WITH CTE", "WITH a AS (SELECT 1) SELECT * FROM a", ""},
		{"INSERT (DML allowed)", "INSERT INTO t VALUES (1)", ""},
		{"UPDATE (DML allowed)", "UPDATE t SET x=1", ""},
		{"DELETE (DML allowed)", "DELETE FROM t", ""},
		{"CREATE INDEX", "CREATE INDEX IF NOT EXISTS i ON t(c)", "CREATE"},
		{"CREATE TABLE", "create table foo(id int)", "CREATE"},
		{"DROP TABLE", "DROP TABLE foo", "DROP"},
		{"ALTER TABLE", "ALTER TABLE t ADD COLUMN c int", "ALTER"},
		{"ANALYZE", "ANALYZE bench_orders;", "ANALYZE"},
		{"SET", "SET work_mem = '64MB'", "SET"},
		{"SHOW", "SHOW enable_wdr_snapshot", "SHOW"},
		{"VACUUM", "VACUUM ANALYZE t", "VACUUM"},
		{"GRANT", "GRANT SELECT ON t TO u", "GRANT"},
		{"with leading comment", "-- explain this\nCREATE INDEX i ON t(c)", "CREATE"},
		{"with block comment", "/* hint */ DROP TABLE foo", "DROP"},
		{"whitespace only", "   ", ""},
		{"empty", "", ""},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := detectUnsupportedStatement(tt.sql)
			if tt.want == "" && got != nil {
				t.Errorf("expected supported, got Kind=%s", got.Kind)
			}
			if tt.want != "" && got == nil {
				t.Errorf("expected Kind=%s, got nil (allowed)", tt.want)
			}
			if tt.want != "" && got != nil && got.Kind != tt.want {
				t.Errorf("Kind: got %s, want %s", got.Kind, tt.want)
			}
		})
	}
}
