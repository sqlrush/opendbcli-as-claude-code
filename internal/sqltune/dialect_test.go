/*-------------------------------------------------------------------------
 *
 * dialect_test.go
 *	  Smoke tests for the neutral sqltune package — verifies registry
 *	  semantics and that the dialect kinds are stable identifiers.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/dialect_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import "testing"

func TestRegistry_RegisterAndLookup(t *testing.T) {
	// Idempotent: a fresh kind starts empty.
	if Lookup("nonexistent") != nil {
		t.Fatalf("Lookup of unregistered kind should return nil")
	}

	called := false
	Register("test-dialect", func(deps PlannerDeps) (DialectPlanner, error) {
		called = true
		return nil, nil
	})

	f := Lookup("test-dialect")
	if f == nil {
		t.Fatalf("Lookup of registered kind returned nil")
	}
	if _, err := f(PlannerDeps{}); err != nil {
		t.Fatalf("factory invoke unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("factory was not actually invoked")
	}
}

func TestRegistry_RegisterReplaces(t *testing.T) {
	// Later registration wins — useful for tests swapping in mocks.
	Register("replace-test", func(deps PlannerDeps) (DialectPlanner, error) {
		return nil, nil
	})
	marker := false
	Register("replace-test", func(deps PlannerDeps) (DialectPlanner, error) {
		marker = true
		return nil, nil
	})
	_, _ = Lookup("replace-test")(PlannerDeps{})
	if !marker {
		t.Fatalf("second Register did not replace first")
	}
}

func TestDialectKinds_AreStableStrings(t *testing.T) {
	// Stability matters: these strings are written into the registry,
	// memory store keys, and prompt templates. Accidental rename would
	// break cross-version compatibility.
	cases := map[DialectKind]string{
		DialectOpenGauss:  "opengauss",
		DialectGaussDB:    "gaussdb",
		DialectPostgreSQL: "postgres",
		DialectMySQL:      "mysql",
		DialectOracle:     "oracle",
	}
	for k, want := range cases {
		if string(k) != want {
			t.Errorf("DialectKind %q changed string value to %q — breaks registry/memory keys", want, k)
		}
	}
}

func TestPlaceholderError_NilSafe(t *testing.T) {
	var e *PlaceholderError
	if e.Error() != "" {
		t.Errorf("nil PlaceholderError.Error() should be empty, got %q", e.Error())
	}
}

func TestBuildTuner_UnregisteredKindReturnsError(t *testing.T) {
	// Sanity: asking for a dialect that hasn't registered a tuner
	// factory (e.g. mysql before M2 lands) returns a clear error
	// rather than nil or panic.
	_, err := BuildTuner("never-registered", TunerDeps{})
	if err == nil {
		t.Fatalf("expected error for unregistered dialect, got nil")
	}
}

func TestRegisterTunerAndLookup(t *testing.T) {
	called := false
	RegisterTuner("test-tuner-kind", func(deps TunerDeps) (TunerEngine, error) {
		called = true
		return nil, nil
	})
	f := LookupTuner("test-tuner-kind")
	if f == nil {
		t.Fatalf("LookupTuner returned nil for just-registered kind")
	}
	if _, err := f(TunerDeps{}); err != nil {
		t.Fatalf("tuner factory invoke unexpected error: %v", err)
	}
	if !called {
		t.Fatalf("registered tuner factory was not invoked")
	}
}
