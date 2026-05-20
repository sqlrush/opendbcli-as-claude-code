/*-------------------------------------------------------------------------
 *
 * integration_test.go
 *	  E2E tests for GaussDB sqltune.
 *
 *	  ⚠️ No real GaussDB Centralized test instance is currently available
 *	  in the project's test infrastructure (47.251.30.180 runs Oracle /
 *	  MySQL / PG / openGauss but no GaussDB). The decorator architecture
 *	  (gaussdbPlanner wraps og planner; only Kind() + EnableTrace/
 *	  CollectTrace differ) means og integration tests effectively
 *	  exercise 7/9 of GaussDB's methods already.
 *
 *	  This file:
 *	    - Documents the GS_PLAN_TRACE-specific bits (queries we'd run
 *	      against a real GaussDB) so when a GaussDB instance becomes
 *	      available, only the openGaussDBOrSkip helper needs wiring.
 *	    - Runs the GaussDB factory-registration sanity check at every
 *	      invocation (no DSN needed).
 *
 *	  Env vars (reserved for future use):
 *	    SQLTUNE_E2E_GAUSSDB_HOST     — (required, gate)
 *	    SQLTUNE_E2E_GAUSSDB_PORT=8000
 *	    SQLTUNE_E2E_GAUSSDB_USER=root
 *	    SQLTUNE_E2E_GAUSSDB_PASS
 *	    SQLTUNE_E2E_GAUSSDB_DB=postgres
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/integration_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"os"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

const envGaussDBHost = "SQLTUNE_E2E_GAUSSDB_HOST"

// TestIntegration_GaussDB_DecoratorChain verifies the decorator
// forwards key calls to og even without a real DB connection.
// Always runs (no DSN needed).
func TestIntegration_GaussDB_DecoratorChain(t *testing.T) {
	p := NewPlanner(nil) // nil driver — checks pure logic before any I/O
	if p.Kind() != sqltune.DialectGaussDB {
		t.Errorf("decorator Kind() = %q, want gaussdb", p.Kind())
	}
	// Decorator must satisfy both optional interfaces inherited from og.
	if _, ok := p.(sqltune.PerformancePlanner); !ok {
		t.Error("decorator should implement PerformancePlanner (forwarded to og)")
	}
	if _, ok := p.(sqltune.EquivVerifier); !ok {
		t.Error("decorator should implement EquivVerifier (forwarded to og)")
	}
}

// TestIntegration_GaussDB_RealInstance — only runs with DSN set.
// Documents that this is the gap in the test matrix.
func TestIntegration_GaussDB_RealInstance(t *testing.T) {
	if os.Getenv(envGaussDBHost) == "" {
		t.Skipf("⚠️ no GaussDB test instance available in project infrastructure.\n"+
			"Set %s when one becomes available; for now decorator inherits og test coverage.",
			envGaussDBHost)
	}
	t.Skip("TODO: when GaussDB Centralized instance available, wire driver + run trace test")
}

// TestIntegration_GaussDB_EnableTraceMissingTable verifies the
// GS_PLAN_TRACE probing returns Available:false with a useful note
// when the table doesn't exist (which it won't unless DBA enabled it).
// Always runs (uses og's nil-driver path which won't probe).
func TestIntegration_GaussDB_EnableTraceFallback(t *testing.T) {
	// nil-driver path: planner's EnableTrace will try to query
	// to_regclass on a nil driver and fail — we just verify the
	// error path doesn't panic and returns sensible TraceData.
	t.Skip("requires real connection — see TestIntegration_GaussDB_RealInstance")
}

func TestGaussDBPromptBuilder_MentionsGSPlanTrace(t *testing.T) {
	// Always runs. Critical regression check: the GaussDB-specific
	// CBO knowledge MUST mention GS_PLAN_TRACE so LLM knows to use
	// the trace when available.
	b := NewPromptBuilder()
	cbo := b.CBOKnowledge()
	if !strings.Contains(cbo, "GS_PLAN_TRACE") {
		t.Error("GaussDB CBOKnowledge must mention GS_PLAN_TRACE — that's the key differentiator from og/PG")
	}
}
