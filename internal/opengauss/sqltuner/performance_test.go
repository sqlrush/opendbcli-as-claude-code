/*-------------------------------------------------------------------------
 *
 * performance_test.go
 *	  Tests for og's PerformancePlanner implementation: the
 *	  isSelectish gate, interface satisfaction, and the DML-rejection
 *	  path. Live EXPLAIN PERFORMANCE tests require an og instance and
 *	  belong in an integration suite.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/performance_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOGPlanner_SatisfiesPerformancePlanner(t *testing.T) {
	// Compile-time check: og planner must implement the optional
	// PerformancePlanner interface so the neutral Tuner can type-assert.
	var _ sqltune.PerformancePlanner = (*ogPlanner)(nil)
}

func TestIsSelectish(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                            true,
		"  select 1":                          true,
		"VALUES (1), (2)":                     true,
		"WITH x AS (SELECT 1) SELECT * FROM x": true,
		"WITH x AS (SELECT 1) DELETE FROM y":   false,
		"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x": false,
		"UPDATE t SET a=1":      false,
		"INSERT INTO t VALUES":  false,
		"DELETE FROM t":         false,
		"MERGE INTO t":          false,
		"CREATE TABLE t (a INT)": false,
		"":                      false,
		"   ":                   false,
	}
	for sql, want := range cases {
		if got := isSelectish(sql); got != want {
			t.Errorf("isSelectish(%q) = %v, want %v", sql, got, want)
		}
	}
}

func TestExplainPerformance_DMLReturnsAvailableFalse(t *testing.T) {
	// DML SQL should be refused with Available:false BEFORE we hit the
	// driver. Pass nil driver — if the driver were called we'd panic;
	// passing test proves the guard runs first.
	p := &ogPlanner{driver: nil} // intentionally nil; guarded
	td, err := p.ExplainPerformance(context.Background(), "DELETE FROM t WHERE id = 1")
	if err != nil {
		t.Fatalf("unexpected error for DML: %v", err)
	}
	if td == nil {
		t.Fatal("nil TraceData for DML")
	}
	if td.Available {
		t.Errorf("Available = true for DML, want false")
	}
	if td.Format != "og_explain_performance" {
		t.Errorf("Format = %q, want og_explain_performance", td.Format)
	}
	if td.Notes == "" {
		t.Errorf("Notes should explain why DML was refused")
	}
}

func TestContainsWord(t *testing.T) {
	cases := []struct {
		haystack, needle string
		want             bool
	}{
		{"WITH X AS (DELETE FROM T) SELECT 1", "DELETE", true},
		{"UPDATED_AT > NOW()", "UPDATE", false}, // substring match must NOT trigger
		{"DELETED_AT IS NULL", "DELETE", false},
		{"WITH X AS (SELECT MERGE_KEY) SELECT 1", "MERGE", false}, // MERGE_KEY is identifier
		{"FROM T MERGE INTO X", "MERGE", true},
		{"DELETE", "DELETE", true}, // whole string
		{"", "DELETE", false},
		{"DELETE FROM T", "DELETE", true}, // at start
		{"FOO,DELETE,BAR", "DELETE", true}, // comma separators
	}
	for _, c := range cases {
		if got := containsWord(c.haystack, c.needle); got != c.want {
			t.Errorf("containsWord(%q, %q) = %v, want %v",
				c.haystack, c.needle, got, c.want)
		}
	}
}

func TestStringify(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", "hello"},
		{[]byte("bytes"), "bytes"},
		{nil, ""},
		{42, ""}, // unsupported types → empty
	}
	for _, c := range cases {
		if got := stringify(c.in); got != c.want {
			t.Errorf("stringify(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
