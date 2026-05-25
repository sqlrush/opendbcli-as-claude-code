/*-------------------------------------------------------------------------
 *
 * planner_test.go
 *	  Unit tests for PG sqltuner — placeholder detection (both $N and ?),
 *	  table name extraction, EXPLAIN command builder, DML detection,
 *	  factory registration, pg_stats SQL list quoting.
 *
 *	  Live-DB tests (real EXPLAIN, real pg_stats) belong in an
 *	  integration suite gated by a POSTGRES_DSN env var — out of scope
 *	  for M3.6 unit tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/planner_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestPGPlannerFactory_Registered(t *testing.T) {
	if sqltune.Lookup(sqltune.DialectPostgreSQL) == nil {
		t.Fatalf("pg planner factory not registered at init")
	}
}

func TestPGTunerFactory_Registered(t *testing.T) {
	if sqltune.LookupTuner(sqltune.DialectPostgreSQL) == nil {
		t.Fatalf("pg tuner factory not registered at init")
	}
}

func TestPGPlannerKind(t *testing.T) {
	p := NewPlanner(nil)
	if p.Kind() != sqltune.DialectPostgreSQL {
		t.Errorf("Kind = %q, want postgres", p.Kind())
	}
}

func TestDetectPlaceholders_PG(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		wantCount int
		wantKind  string
	}{
		{"none", "SELECT 1", 0, ""},
		{"single $1", "SELECT * FROM t WHERE id = $1", 1, "pg_dollar"},
		{"multi $1 $2", "SELECT * FROM t WHERE a = $1 AND b = $2", 2, "pg_dollar"},
		{"qmark style", "SELECT * FROM t WHERE id = ?", 1, "qmark"},
		// PG numbered up to 99+
		{"high number", "SELECT $42 FROM t", 1, "pg_dollar"},
		// inside string literal — ignored
		{"in literal single", "SELECT * FROM t WHERE name = '$1 cost'", 0, ""},
		{"in literal double", `SELECT * FROM t WHERE col = "id_$1"`, 0, ""},
		// escaped quote
		{"escaped quote", `SELECT 'it\'s ok $1'`, 0, ""},
		// $$dollar-quoted string — current detector treats as outside string, so $N inside still flags.
		// Not perfect but matches og's behavior; documented limitation.
		// mixed
		{"mixed", "SELECT * FROM t WHERE a = $1 AND b = ?", 2, "pg_dollar"},
		// $$ without trailing number — should NOT be detected as placeholder
		{"bare $", "SELECT $$tag$$ FROM t", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectPlaceholders(c.sql)
			gotCount := 0
			if got != nil {
				gotCount = len(got.Placeholders)
			}
			if gotCount != c.wantCount {
				t.Errorf("count = %d, want %d (placeholders=%v)", gotCount, c.wantCount, ifNil(got))
			}
			if c.wantCount > 0 && got != nil && got.DetectedKind != c.wantKind {
				t.Errorf("DetectedKind = %q, want %q", got.DetectedKind, c.wantKind)
			}
		})
	}
}

func ifNil(p *sqltune.PlaceholderError) []string {
	if p == nil {
		return nil
	}
	return p.Placeholders
}

func TestExtractTableNamesPG(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT * FROM orders", []string{"orders"}},
		{"SELECT * FROM orders o JOIN users u ON o.uid = u.id", []string{"orders", "users"}},
		{"SELECT * FROM public.orders", []string{"orders"}},
		{"UPDATE customers SET name = 'x' WHERE id = 1", []string{"customers"}},
		{"INSERT INTO logs (msg) VALUES ('x')", []string{"logs"}},
		{"WITH x AS (SELECT 1) SELECT * FROM x", []string{"x"}},
	}
	for _, c := range cases {
		got := extractTableNamesPG(c.sql)
		if !equalSlices(got, c.want) {
			t.Errorf("extractTableNamesPG(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestIsReadOnlyQuery_PG(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":               true,
		"  select 1":             true,
		"VALUES (1), (2)":        true,
		"WITH x AS (SELECT 1) SELECT * FROM x":   true,
		"WITH x AS (SELECT 1) INSERT INTO t SELECT * FROM x": false,
		"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x": false,
		"UPDATE t SET a=1":       false,
		"INSERT INTO t VALUES":   false,
		"DELETE FROM t":          false,
		"MERGE INTO t":           false,
		"":                       false,
	}
	for sql, want := range cases {
		if got := isReadOnlyQuery(sql); got != want {
			t.Errorf("isReadOnlyQuery(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestBuildExplainCmd(t *testing.T) {
	cases := []struct {
		name        string
		sql         string
		analyze     bool
		settings    bool
		wantSubstrs []string
	}{
		{"basic", "SELECT 1", false, false,
			[]string{"FORMAT JSON", "COSTS TRUE", "VERBOSE TRUE", "SELECT 1"}},
		{"analyze + buffers", "SELECT 1", true, false,
			[]string{"FORMAT JSON", "ANALYZE TRUE", "BUFFERS TRUE", "SELECT 1"}},
		{"with settings", "SELECT 1", false, true,
			[]string{"SETTINGS TRUE", "SELECT 1"}},
		{"no buffers when no analyze", "SELECT 1", false, false,
			[]string{}}, // checks absence of BUFFERS below
	}
	for _, c := range cases {
		got := buildExplainCmd(c.sql, c.analyze, c.settings)
		for _, sub := range c.wantSubstrs {
			if !strings.Contains(got, sub) {
				t.Errorf("[%s] cmd %q missing %q", c.name, got, sub)
			}
		}
		// BUFFERS should only appear with ANALYZE
		if !c.analyze && strings.Contains(got, "BUFFERS") {
			t.Errorf("[%s] BUFFERS present without ANALYZE in %q", c.name, got)
		}
	}
}

func TestSQLInListPG(t *testing.T) {
	got := sqlInListPG([]string{"orders", "users"})
	want := "'orders','users'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if sqlInListPG(nil) != "''" {
		t.Errorf("nil case mismatch")
	}
	// Single-quote escape
	got = sqlInListPG([]string{"o'malley"})
	if got != "'o''malley'" {
		t.Errorf("escape: got %q, want 'o''malley'", got)
	}
}

func TestEnableTrace_ReturnsAvailableFalse(t *testing.T) {
	// PG planner's EnableTrace must always return Available:false
	// since open-source PG has no CBO trace. Critical that the note
	// is set so the LLM knows to fall back to sidecar reasoning.
	p := NewPlanner(nil).(*pgPlanner)
	closeFn, td, err := p.EnableTrace(context.Background(), "test")
	if err != nil {
		t.Fatalf("EnableTrace returned error: %v", err)
	}
	if td == nil {
		t.Fatalf("TraceData is nil")
	}
	if td.Available {
		t.Errorf("Available = true, expected false for PG")
	}
	if td.Format != "none" {
		t.Errorf("Format = %q, want 'none'", td.Format)
	}
	if td.Notes == "" {
		t.Error("Notes is empty; LLM won't know why trace is unavailable")
	}
	if !strings.Contains(td.Notes, "pg_stats") {
		t.Error("Notes should mention pg_stats fallback strategy")
	}
	if closeFn == nil {
		t.Error("closeFn is nil")
	}
	if err := closeFn(); err != nil {
		t.Errorf("noop closeFn returned error: %v", err)
	}
}

func TestParseBool(t *testing.T) {
	cases := []struct {
		in   any
		want bool
	}{
		{true, true},
		{false, false},
		{"t", true},
		{"f", false},
		{"true", true},
		{"TRUE", true},
		{"1", true},
		{"0", false},
		{[]byte("t"), true},
		{int64(1), true},
		{int64(0), false},
		{nil, false},
	}
	for _, c := range cases {
		if got := parseBool(c.in); got != c.want {
			t.Errorf("parseBool(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func equalSlices(a, b []string) bool {
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
