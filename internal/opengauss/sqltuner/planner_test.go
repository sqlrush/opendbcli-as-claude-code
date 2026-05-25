/*-------------------------------------------------------------------------
 *
 * planner_test.go
 *	  Smoke test that og's planner factory is registered with the
 *	  neutral sqltune Registry at init. Catches accidental drift if
 *	  someone moves the init() call or renames the DialectKind.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/planner_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOGPlannerFactory_Registered(t *testing.T) {
	f := sqltune.Lookup(sqltune.DialectOpenGauss)
	if f == nil {
		t.Fatalf("og planner factory not registered at init — sqltune.Lookup(DialectOpenGauss) returned nil")
	}
}

func TestOGPlannerFactory_RejectsBadDriver(t *testing.T) {
	f := sqltune.Lookup(sqltune.DialectOpenGauss)
	// Passing a non-driver value should produce a typed error, not panic.
	_, err := f(sqltune.PlannerDeps{Driver: "not a driver"})
	if err == nil {
		t.Errorf("expected factory to reject non-Driver value, got nil error")
	}
}

func TestClassifyPlaceholderKind(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "unknown"},
		{[]string{"$1", "$2"}, "pg_dollar"},
		{[]string{":1", ":2"}, "oracle_colon"},
		{[]string{":B1"}, "oracle_colon"},
		{[]string{"?", "?", "?"}, "qmark"},
	}
	for _, c := range cases {
		got := classifyPlaceholderKind(c.in)
		if got != c.want {
			t.Errorf("classifyPlaceholderKind(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOGPlannerKind(t *testing.T) {
	p := NewPlanner(nil)
	if p.Kind() != sqltune.DialectOpenGauss {
		t.Errorf("og planner Kind() = %q, want opengauss", p.Kind())
	}
}
