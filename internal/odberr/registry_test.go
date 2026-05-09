/*-------------------------------------------------------------------------
 *
 * registry_test.go
 *	  Test cases for registry.go (odberr package):
 *	  TestRegistry_DefaultsPresent,
 *	  TestLookup_UnknownFallsBackToUnknown, TestCounter_Concurrent.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/registry_test.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"sync"
	"testing"
)

func TestRegistry_DefaultsPresent(t *testing.T) {
	t.Parallel()
	entries := AllEntries()
	if len(entries) < 20 {
		t.Fatalf("expected at least 20 default entries, got %d", len(entries))
	}

	// Spot-check a few known codes.
	for _, code := range []string{ErrCoreMainPanic, ErrUIDiagRender, ErrUnknown} {
		if _, ok := Lookup(code); !ok {
			t.Errorf("missing default entry: %s", code)
		}
	}
}

func TestLookup_UnknownFallsBackToUnknown(t *testing.T) {
	t.Parallel()
	e, ok := Lookup("ERR-990001")
	if ok {
		t.Fatalf("expected ok=false for unregistered code")
	}
	// Fallback carries the queried code (not the ErrUnknown constant).
	if e.Code != "ERR-990001" {
		t.Fatalf("fallback code = %q, want ERR-990001", e.Code)
	}
}

func TestCounter_Concurrent(t *testing.T) {
	t.Parallel()
	code := "ERR-030001"
	before := Count(code)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			Increment(code)
		}()
	}
	wg.Wait()

	if got := Count(code); got != before+100 {
		t.Fatalf("counter race: want %d, got %d", before+100, got)
	}
}
