/*-------------------------------------------------------------------------
 *
 * planner_test.go
 *	  Unit tests for Oracle sqltuner — factory registration, Oracle
 *	  bind-variable placeholder detection (:N / :B1), PLAN_TABLE-row
 *	  → PlanNode tree reconstruction, 10053 tracefile-name derivation,
 *	  hard-parse comment wrapping.
 *
 *	  Live-DB tests (real EXPLAIN PLAN, real 10053 trace) belong in an
 *	  integration suite gated by ORACLE_DSN. Out of scope for unit tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/planner_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOraclePlannerFactory_Registered(t *testing.T) {
	if sqltune.Lookup(sqltune.DialectOracle) == nil {
		t.Fatalf("oracle planner factory not registered at init")
	}
}

func TestOracleTunerFactory_Registered(t *testing.T) {
	if sqltune.LookupTuner(sqltune.DialectOracle) == nil {
		t.Fatalf("oracle tuner factory not registered at init")
	}
}

func TestOraclePlannerKind(t *testing.T) {
	p := NewPlanner(nil)
	if p.Kind() != sqltune.DialectOracle {
		t.Errorf("Kind = %q, want oracle", p.Kind())
	}
}

func TestDetectPlaceholders_Oracle(t *testing.T) {
	cases := []struct {
		name      string
		sql       string
		wantCount int
	}{
		{"none", "SELECT 1 FROM dual", 0},
		{"single positional", "SELECT * FROM emp WHERE empno = :1", 1},
		{"multi positional", "SELECT * FROM emp WHERE a = :1 AND b = :2", 2},
		{"named bind B-style", "SELECT * FROM emp WHERE name = :B1", 1},
		{"named bind identifier", "SELECT * FROM emp WHERE name = :p_name", 1},
		// Inside string literal — ignored
		{"in literal single", "SELECT * FROM emp WHERE name = ':1 fake'", 0},
		{"in literal double", `SELECT * FROM emp WHERE col = ":B1"`, 0},
		// Mixed (PG-style cast :: should be skipped, not flagged)
		{"pg-style cast not flagged", "SELECT col::INT FROM emp WHERE x = :1", 1},
		// Two consecutive colons (defensive — Oracle doesn't use this)
		{"double colon ignored", "SELECT * FROM emp WHERE x = 1::2", 0},
		// Multi-line
		{"multi-line", "SELECT a, b\nFROM emp\nWHERE id IN (:1, :2, :3)", 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectPlaceholders(c.sql)
			gotCount := 0
			if got != nil {
				gotCount = len(got.Placeholders)
			}
			if gotCount != c.wantCount {
				t.Errorf("count = %d, want %d (placeholders=%v)", gotCount, c.wantCount, gotPlaceholders(got))
			}
			if c.wantCount > 0 && got != nil && got.DetectedKind != "oracle_colon" {
				t.Errorf("DetectedKind = %q, want oracle_colon", got.DetectedKind)
			}
		})
	}
}

func gotPlaceholders(p *sqltune.PlaceholderError) []string {
	if p == nil {
		return nil
	}
	return p.Placeholders
}

func TestExtractTableNamesOracle(t *testing.T) {
	cases := []struct {
		sql  string
		want []string
	}{
		{"SELECT * FROM emp", []string{"emp"}},
		{"SELECT * FROM emp e JOIN dept d ON e.deptno = d.deptno", []string{"emp", "dept"}},
		{"SELECT * FROM scott.emp", []string{"emp"}},
		{"UPDATE customers SET name = 'x' WHERE id = 1", []string{"customers"}},
		{"INSERT INTO logs (msg) VALUES ('x')", []string{"logs"}},
		// WITH CTE
		{"WITH x AS (SELECT 1 FROM dual) SELECT * FROM x", []string{"dual", "x"}},
	}
	for _, c := range cases {
		got := extractTableNamesOracle(c.sql)
		if !equalSlices(got, c.want) {
			t.Errorf("extractTableNamesOracle(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestSQLInListOracle(t *testing.T) {
	got := sqlInListOracle([]string{"EMP", "DEPT"})
	want := "'EMP','DEPT'"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if sqlInListOracle(nil) != "''" {
		t.Errorf("nil case mismatch")
	}
	// Single quote escape
	got = sqlInListOracle([]string{"O'BRIEN"})
	if got != "'O''BRIEN'" {
		t.Errorf("escape: got %q, want 'O''BRIEN'", got)
	}
}

func TestJoinOpAndOption(t *testing.T) {
	cases := []struct {
		op, opt, want string
	}{
		{"TABLE ACCESS", "FULL", "TABLE ACCESS FULL"},
		{"HASH JOIN", "", "HASH JOIN"},
		{"  NESTED LOOPS  ", "OUTER", "NESTED LOOPS OUTER"},
		{"SORT", "ORDER BY", "SORT ORDER BY"},
	}
	for _, c := range cases {
		if got := joinOpAndOption(c.op, c.opt); got != c.want {
			t.Errorf("joinOpAndOption(%q, %q) = %q, want %q", c.op, c.opt, got, c.want)
		}
	}
}

func TestNewStatementID(t *testing.T) {
	id1 := newStatementID()
	id2 := newStatementID()
	if !strings.HasPrefix(id1, "opendb_") {
		t.Errorf("id %q missing opendb_ prefix", id1)
	}
	if id1 == id2 {
		t.Errorf("two calls returned same id %q — collision risk", id1)
	}
	if len(id1) < 16 {
		t.Errorf("id %q too short, want at least 16 chars", id1)
	}
}

func TestGenerateTraceTag(t *testing.T) {
	t1 := generateTraceTag()
	t2 := generateTraceTag()
	if !strings.HasPrefix(t1, "opendb_") {
		t.Errorf("tag %q missing opendb_ prefix", t1)
	}
	if t1 == t2 {
		t.Errorf("tags collided: %q vs %q", t1, t2)
	}
	// Must be ≤48 chars (Oracle TRACEFILE_IDENTIFIER limit)
	if len(t1) > 48 {
		t.Errorf("tag too long for Oracle: len=%d %q", len(t1), t1)
	}
	// Must be valid for OS filename (alnum + underscore)
	for _, c := range t1 {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_'
		if !ok {
			t.Errorf("tag has invalid OS-filename char %q in %q", c, t1)
		}
	}
}

func TestHardParseHintWrap(t *testing.T) {
	wrapped := hardParseHintWrap("SELECT * FROM emp", "abc123")
	if !strings.Contains(wrapped, "/* opendb_sqltune_abc123 */") {
		t.Errorf("missing comment marker: %q", wrapped)
	}
	if !strings.Contains(wrapped, "SELECT * FROM emp") {
		t.Errorf("missing original SQL: %q", wrapped)
	}
	// Different tags must produce different wraps (point of forcing hard parse)
	w1 := hardParseHintWrap("SELECT 1", "a")
	w2 := hardParseHintWrap("SELECT 1", "b")
	if w1 == w2 {
		t.Errorf("same SQL+different tags produced same wrap: %q", w1)
	}
}

func TestTagInPath(t *testing.T) {
	cases := []struct {
		basePath, tag, want string
	}{
		{"/opt/oracle/diag/rdbms/orcl/orcl/trace/orcl_ora_12345.trc", "opendb_abc",
			"/opt/oracle/diag/rdbms/orcl/orcl/trace/orcl_ora_12345_opendb_abc.trc"},
		{"x.trc", "tag", "x_tag.trc"},
		// Defensive: no .trc suffix
		{"weird_path", "tag", "weird_path_tag"},
	}
	for _, c := range cases {
		if got := tagInPath(c.basePath, c.tag); got != c.want {
			t.Errorf("tagInPath(%q, %q) = %q, want %q",
				c.basePath, c.tag, got, c.want)
		}
	}
}

func TestIsBindIdentChar(t *testing.T) {
	cases := map[byte]bool{
		'A': true, 'z': true, '0': true, '9': true, '_': true,
		' ': false, '.': false, ':': false, '(': false, ')': false,
	}
	for c, want := range cases {
		if got := isBindIdentChar(c); got != want {
			t.Errorf("isBindIdentChar(%q) = %v, want %v", c, got, want)
		}
	}
}

func TestNoopClose(t *testing.T) {
	if err := noopClose(); err != nil {
		t.Errorf("noopClose returned error: %v", err)
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
