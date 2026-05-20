/*-------------------------------------------------------------------------
 *
 * sqltune_skill_test.go
 *	  Tests for /sqltune SQL_ID auto-fallback path. Live DB tests cover
 *	  the actual statement_history lookup; here we cover the looksLikeSQLID
 *	  classifier and the toString coercer.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/sqltune_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package query

import "testing"

func TestLooksLikeSQLID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Real SQL IDs from /slowsql output
		{"581990336", true},
		{"2585096556", true},
		{"1", true},

		// SQL text — must return false
		{"SELECT 1", false},
		{"select 1 from dual", false},
		{"WITH cte AS (SELECT 1) SELECT * FROM cte", false},
		{"UPDATE t SET a=1", false},

		// Edge cases
		{"", false},
		{"   ", false},
		{"123 456", false},   // space → not pure digits
		{"12+3", false},      // expression → not ID
		{"1.5", false},       // decimal → not bigint ID
		{"-1", false},        // negative → unique_sql_id is unsigned
		{"abc123", false},    // mixed → not ID
		{"99999999999999999999999999999", false}, // > 25 chars defensive

		// Surrounded whitespace OK — TrimSpace inside
		{" 581990336 ", true},
		{"\t12345\n", true},
	}
	for _, c := range cases {
		if got := looksLikeSQLID(c.in); got != c.want {
			t.Errorf("looksLikeSQLID(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestContainsPlaceholders(t *testing.T) {
	cases := []struct {
		sql  string
		want bool
	}{
		// no placeholders
		{"SELECT * FROM t WHERE id = 1", false},
		{"SELECT 1", false},
		{"", false},

		// qmark
		{"SELECT * FROM t WHERE id = ?", true},
		{"SELECT * FROM t WHERE a=? AND b=?", true},

		// dollar (PG/og style)
		{"SELECT * FROM t WHERE id = $1", true},
		{"SELECT $1, $2, $3", true},

		// oracle colon
		{"SELECT * FROM t WHERE id = :1", true},
		{"SELECT * FROM t WHERE name = :p_name", true},

		// inside string literals — must NOT trigger
		{"SELECT 'who?' FROM t", false},
		{`SELECT "what$1" FROM t`, false},
		{"SELECT 'name = :foo' FROM t", false},

		// escaped quote
		{`SELECT 'it\'s ok ?' FROM t`, false},

		// real og normalized (the case driving this bug fix)
		{"WHERE o.created_at >= now() - interval ? AND status IN (?, ?, ?)", true},
	}
	for _, c := range cases {
		if got := containsPlaceholders(c.sql); got != c.want {
			t.Errorf("containsPlaceholders(%q) = %v, want %v", c.sql, got, c.want)
		}
	}
}

func TestTruncForDisplay(t *testing.T) {
	if got := truncForDisplay("short", 100); got != "short" {
		t.Errorf("short input not preserved: %q", got)
	}
	if got := truncForDisplay("abcdefghij", 5); got != "abcde ..." {
		t.Errorf("truncation wrong: %q", got)
	}
	if got := truncForDisplay("  spaced  ", 100); got != "spaced" {
		t.Errorf("whitespace not trimmed: %q", got)
	}
}

func TestBlindNullSubstitute(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"SELECT 1", "SELECT 1"},
		{"", ""},
		// Generic comparison/expression context → "0"
		{"SELECT * FROM t WHERE id = ?", "SELECT * FROM t WHERE id = 0"},
		{"a = ? AND b = ?", "a = 0 AND b = 0"},
		// inside string literals — preserved
		{"SELECT 'who?' FROM t", "SELECT 'who?' FROM t"},
		{`SELECT "name?" FROM t`, `SELECT "name?" FROM t`},
		{"WHERE c = 'go?' AND d = ?", "WHERE c = 'go?' AND d = 0"},
		// escaped quote
		{`SELECT 'it\'s ?' AND x = ?`, `SELECT 'it\'s ?' AND x = 0`},
		// interval — special syntax
		{
			"WHERE created_at >= now() - interval ?",
			"WHERE created_at >= now() - interval '1 day'",
		},
		// IN list
		{
			"AND status IN (?, ?, ?)",
			"AND status IN (0, 0, 0)",
		},
		// LIMIT / OFFSET
		{"LIMIT ?", "LIMIT 100"},
		{"OFFSET ?", "OFFSET 0"},
		// LIKE pattern
		{"WHERE name LIKE ?", "WHERE name LIKE '%'"},
		// Combined real og pattern
		{
			"WHERE created_at >= now() - interval ? AND status IN (?, ?, ?) LIMIT ?",
			"WHERE created_at >= now() - interval '1 day' AND status IN (0, 0, 0) LIMIT 100",
		},
		{
			"HAVING COUNT(*) > ? LIMIT ?",
			"HAVING COUNT(*) > 0 LIMIT 100",
		},
	}
	for _, c := range cases {
		if got := blindNullSubstitute(c.in); got != c.want {
			t.Errorf("blindNullSubstitute(%q)\n  got:  %q\n  want: %q", c.in, got, c.want)
		}
	}
}

func TestToString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{[]byte("bytes"), "bytes"},
		{nil, ""},
		{42, ""}, // unsupported types collapse to empty
	}
	for _, c := range cases {
		if got := toString(c.in); got != c.want {
			t.Errorf("toString(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
