/*-------------------------------------------------------------------------
 *
 * error_test.go
 *	  Test cases for error.go (odberr package): TestError_Format,
 *	  TestSeverity_ResolvedFromRegistry, TestWithStack_Immutable.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/error_test.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"errors"
	"strings"
	"testing"
)

func TestError_Format(t *testing.T) {
	t.Parallel()
	t.Run("no cause", func(t *testing.T) {
		e := New(ErrUIDiagRender, "renderer crashed")
		if !strings.HasPrefix(e.Error(), "[ERR-030001]") {
			t.Fatalf("missing code prefix: %q", e.Error())
		}
		if !strings.Contains(e.Error(), "renderer crashed") {
			t.Fatalf("missing message: %q", e.Error())
		}
	})

	t.Run("with cause", func(t *testing.T) {
		cause := errors.New("slice oob")
		e := Wrap(ErrUIDiagRender, cause, "renderer crashed")
		if !strings.Contains(e.Error(), "slice oob") {
			t.Fatalf("cause not included: %q", e.Error())
		}
		if !errors.Is(e, cause) {
			t.Fatalf("Unwrap broken; errors.Is(e, cause) = false")
		}
	})
}

func TestSeverity_ResolvedFromRegistry(t *testing.T) {
	t.Parallel()
	// ErrCoreMainPanic is FATAL in registry
	e := New(ErrCoreMainPanic, "explode")
	if e.Severity != SeverityFatal {
		t.Fatalf("want Fatal, got %v", e.Severity)
	}

	// Unknown code defaults to Error
	e2 := New("ERR-990000", "whatever")
	if e2.Severity != SeverityError {
		t.Fatalf("want Error default, got %v", e2.Severity)
	}
}

func TestWithStack_Immutable(t *testing.T) {
	t.Parallel()
	orig := New(ErrUIDiagRender, "x")
	with := orig.WithStack("trace...")
	if orig.Stack != "" {
		t.Fatalf("WithStack mutated original")
	}
	if with.Stack != "trace..." {
		t.Fatalf("WithStack did not set new field")
	}
	if orig == with {
		t.Fatalf("WithStack returned same pointer")
	}
}

func TestModule_Extract(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ERR-030001": "03",
		"ERR-999999": "99",
		"not-a-code": "99",
		"":           "99",
	}
	for in, want := range cases {
		if got := Module(in); got != want {
			t.Errorf("Module(%q) = %q, want %q", in, got, want)
		}
	}
}
