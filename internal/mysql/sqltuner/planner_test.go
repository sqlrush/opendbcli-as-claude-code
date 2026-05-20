/*-------------------------------------------------------------------------
 *
 * planner_test.go
 *	  Unit tests for MySQL sqltuner — focus on pieces that don't need
 *	  a live MySQL connection: placeholder detection, EXPLAIN JSON
 *	  parsing, table name extraction, factory registration.
 *
 *	  Live-DB tests (real EXPLAIN, real optimizer_trace) belong in an
 *	  integration suite gated by a MYSQL_DSN env var — out of scope
 *	  for M2.6 unit tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/planner_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestMySQLPlannerFactory_Registered(t *testing.T) {
	f := sqltune.Lookup(sqltune.DialectMySQL)
	if f == nil {
		t.Fatalf("mysql planner factory not registered at init")
	}
}

func TestMySQLTunerFactory_Registered(t *testing.T) {
	f := sqltune.LookupTuner(sqltune.DialectMySQL)
	if f == nil {
		t.Fatalf("mysql tuner factory not registered at init")
	}
}

func TestMySQLPlannerKind(t *testing.T) {
	p := NewPlanner(nil)
	if p.Kind() != sqltune.DialectMySQL {
		t.Errorf("Kind = %q, want mysql", p.Kind())
	}
}

func TestDetectPlaceholders_Qmark(t *testing.T) {
	cases := []struct {
		sql       string
		wantCount int
	}{
		{"SELECT 1", 0},
		{"SELECT * FROM t WHERE id = ?", 1},
		{"SELECT * FROM t WHERE a = ? AND b = ?", 2},
		// `?` inside string literal should be ignored
		{"SELECT * FROM t WHERE name = 'who?'", 0},
		{`SELECT * FROM t WHERE name = "what?" AND id = ?`, 1},
		// escaped quote handling
		{`SELECT 'it\'s ok?'`, 0},
		// multi-line, mixed
		{"SELECT a, b\nFROM t\nWHERE id IN (?, ?, ?)", 3},
	}
	for _, c := range cases {
		got := detectPlaceholders(c.sql)
		gotCount := 0
		if got != nil {
			gotCount = len(got.Placeholders)
		}
		if gotCount != c.wantCount {
			t.Errorf("detectPlaceholders(%q) = %d placeholders, want %d", c.sql, gotCount, c.wantCount)
		}
		if c.wantCount > 0 && got != nil && got.DetectedKind != "qmark" {
			t.Errorf("DetectedKind = %q, want qmark", got.DetectedKind)
		}
	}
}

func TestExtractTableNamesMySQL(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT * FROM orders", []string{"orders"}},
		{"SELECT * FROM orders o JOIN users u ON o.uid = u.id", []string{"orders", "users"}},
		{"SELECT * FROM s.orders", []string{"orders"}},
		{"UPDATE customers SET name = 'x' WHERE id = 1", []string{"customers"}},
		{"INSERT INTO logs (msg) VALUES ('x')", []string{"logs"}},
		// keyword after FROM should NOT be treated as a table name
		{"SELECT (SELECT 1)", nil},
	}
	for _, c := range cases {
		got := extractTableNamesMySQL(c.sql)
		if !equalStringSlices(got, c.want) {
			t.Errorf("extractTableNamesMySQL(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestParseMySQLBlock_SimpleTable(t *testing.T) {
	// Synthetic single-table EXPLAIN JSON.
	raw := `{
        "table": {
            "table_name": "orders",
            "access_type": "ALL",
            "rows_examined_per_scan": 1000,
            "cost_info": {"read_cost": "100.50", "eval_cost": "10.05"},
            "attached_condition": "uid = 1"
        }
    }`
	var qb map[string]any
	if err := json.Unmarshal([]byte(raw), &qb); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	n := parseMySQLBlock(qb)
	if n == nil {
		t.Fatalf("parseMySQLBlock returned nil")
	}
	if n.RelationName != "orders" {
		t.Errorf("RelationName = %q, want orders", n.RelationName)
	}
	if n.PlanRows != 1000 {
		t.Errorf("PlanRows = %d, want 1000", n.PlanRows)
	}
	if n.TotalCost < 110 || n.TotalCost > 111 {
		t.Errorf("TotalCost = %.2f, want ~110.55", n.TotalCost)
	}
	if !strings.Contains(n.Operator, "ALL") || !strings.Contains(n.Operator, "orders") {
		t.Errorf("Operator = %q, want to contain ALL + orders", n.Operator)
	}
	if n.Filter != "uid = 1" {
		t.Errorf("Filter = %q, want uid = 1", n.Filter)
	}
}

func TestParseMySQLBlock_NestedLoopJoin(t *testing.T) {
	raw := `{
        "nested_loop": [
            {"table": {"table_name": "a", "access_type": "ref", "rows_examined_per_scan": 10, "cost_info":{"read_cost":"5","eval_cost":"1"}}},
            {"table": {"table_name": "b", "access_type": "eq_ref", "rows_examined_per_scan": 1, "cost_info":{"read_cost":"2","eval_cost":"0.5"}}}
        ]
    }`
	var qb map[string]any
	_ = json.Unmarshal([]byte(raw), &qb)
	n := parseMySQLBlock(qb)
	if n == nil || n.Operator != "Nested Loop Join" {
		t.Fatalf("expected Nested Loop Join root, got %+v", n)
	}
	if len(n.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(n.Children))
	}
	if n.Children[0].RelationName != "a" || n.Children[1].RelationName != "b" {
		t.Errorf("children names = [%s, %s], want [a, b]", n.Children[0].RelationName, n.Children[1].RelationName)
	}
}

func TestParseMySQLBlock_OrderBy(t *testing.T) {
	raw := `{
        "ordering_operation": {
            "using_filesort": true,
            "table": {"table_name": "t", "access_type": "index", "rows_examined_per_scan": 50, "cost_info":{"read_cost":"10","eval_cost":"5"}}
        }
    }`
	var qb map[string]any
	_ = json.Unmarshal([]byte(raw), &qb)
	n := parseMySQLBlock(qb)
	if n == nil || n.Operator != "ORDER BY" {
		t.Fatalf("expected ORDER BY root, got %+v", n)
	}
	if len(n.SortKey) == 0 || n.SortKey[0] != "<filesort>" {
		t.Errorf("expected filesort marker in SortKey, got %v", n.SortKey)
	}
	if len(n.Children) != 1 || n.Children[0].RelationName != "t" {
		t.Errorf("expected child table t, got %+v", n.Children)
	}
}

func TestExplainAccessType(t *testing.T) {
	cases := []struct {
		access string
		want   string
	}{
		{"ALL", "Full Table Scan"},
		{"index", "Full Index Scan"},
		{"range", "Range Scan"},
		{"ref", "Index Lookup"},
		{"eq_ref", "Index Lookup"},
		{"const", "Constant"},
		{"", "Table Access"},
		{"unique_subquery", "unique_subquery"}, // unknown passes through
	}
	for _, c := range cases {
		got := explainAccessType(map[string]any{"access_type": c.access})
		if got != c.want {
			t.Errorf("explainAccessType(%q) = %q, want %q", c.access, got, c.want)
		}
	}
}

func TestParseFloat_AllTypes(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{1.5, 1.5},
		{int64(7), 7},
		{int(3), 3},
		{"2.5", 2.5},
		{[]byte("4.5"), 4.5},
		{nil, 0},
		{"not a number", 0},
	}
	for _, c := range cases {
		if got := parseFloat(c.in); got != c.want {
			t.Errorf("parseFloat(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsReadOnlyQuery(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":               true,
		"  select 1":             true,
		"WITH x AS (SELECT 1) SELECT * FROM x": true,
		"UPDATE t SET a=1":       false,
		"INSERT INTO t VALUES":   false,
		"DELETE FROM t":          false,
		"":                       false,
	}
	for sql, want := range cases {
		if got := isReadOnlyQuery(sql); got != want {
			t.Errorf("isReadOnlyQuery(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestSQLInListMySQL(t *testing.T) {
	got := sqlInListMySQL([]string{"orders", "users"})
	want := "'orders','users'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if sqlInListMySQL(nil) != "''" {
		t.Errorf("nil case mismatch")
	}
}

// equalStringSlices compares slices ignoring nil vs empty equivalence.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
