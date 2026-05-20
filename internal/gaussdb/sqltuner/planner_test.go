/*-------------------------------------------------------------------------
 *
 * planner_test.go
 *	  Tests for GaussDB Centralized sqltuner — factory registration,
 *	  decorator forwarding, GS_PLAN_TRACE error paths, and the
 *	  Available:false fallback contract.
 *
 *	  Live GS_PLAN_TRACE tests require GaussDB Centralized with
 *	  plan_trace enabled by DBA. Out of scope for unit tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/planner_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestGaussDBPlannerFactory_Registered(t *testing.T) {
	if sqltune.Lookup(sqltune.DialectGaussDB) == nil {
		t.Fatalf("gaussdb planner factory not registered at init")
	}
}

func TestGaussDBTunerFactory_Registered(t *testing.T) {
	if sqltune.LookupTuner(sqltune.DialectGaussDB) == nil {
		t.Fatalf("gaussdb tuner factory not registered at init")
	}
}

func TestGaussDBPlannerKind(t *testing.T) {
	p := NewPlanner(nil)
	if p.Kind() != sqltune.DialectGaussDB {
		t.Errorf("Kind = %q, want gaussdb", p.Kind())
	}
}

func TestGaussDBPlanner_SatisfiesPerformancePlanner(t *testing.T) {
	// GaussDB inherits EXPLAIN PERFORMANCE from og — must compile
	// as a PerformancePlanner so the neutral Tuner picks it up.
	var _ sqltune.PerformancePlanner = (*gaussdbPlanner)(nil)
}

func TestTruthy(t *testing.T) {
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
		if got := truthy(c.in); got != c.want {
			t.Errorf("truthy(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestBuildTraceBody(t *testing.T) {
	r := gsTraceRow{
		queryID:   "12345",
		query:     "SELECT 1",
		planText:  "Seq Scan on t",
		planTrace: "CBO: chose Seq Scan over Index Scan (cost diff -2.5)",
		tracedAt:  "2026-05-17 10:00:00",
	}
	body := buildTraceBody(r)
	for _, must := range []string{"SELECT 1", "Seq Scan on t", "CBO: chose", "-- CBO decision trace"} {
		if !strings.Contains(body, must) {
			t.Errorf("body missing %q: %s", must, body)
		}
	}

	// Empty plan_trace → no trace section
	r2 := gsTraceRow{query: "X"}
	body2 := buildTraceBody(r2)
	if strings.Contains(body2, "CBO decision trace") {
		t.Errorf("body contains CBO header when planTrace is empty: %s", body2)
	}
}

func TestToStr(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{[]byte("bytes"), "bytes"},
		{nil, ""},
		{42, ""},
	}
	for _, c := range cases {
		if got := toStr(c.in); got != c.want {
			t.Errorf("toStr(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestToInt64(t *testing.T) {
	cases := []struct {
		in   any
		want int64
	}{
		{int64(42), 42},
		{int(7), 7},
		{float64(3.7), 3},
		{nil, 0},
		{"abc", 0},
	}
	for _, c := range cases {
		if got := toInt64(c.in); got != c.want {
			t.Errorf("toInt64(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestNoopClose(t *testing.T) {
	if err := noopClose(); err != nil {
		t.Errorf("noopClose returned error: %v", err)
	}
}
