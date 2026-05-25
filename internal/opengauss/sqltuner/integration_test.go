/*-------------------------------------------------------------------------
 *
 * integration_test.go
 *	  Real-DB E2E tests for openGauss sqltune. Skipped unless
 *	  SQLTUNE_E2E_OPENGAUSS_HOST is set.
 *
 *	  Env vars (defaults shown):
 *	    SQLTUNE_E2E_OPENGAUSS_HOST       — (required, gate)
 *	    SQLTUNE_E2E_OPENGAUSS_PORT=15432
 *	    SQLTUNE_E2E_OPENGAUSS_USER=gauss
 *	    SQLTUNE_E2E_OPENGAUSS_PASS       — (empty default)
 *	    SQLTUNE_E2E_OPENGAUSS_DB=postgres
 *
 *	  Uses og's existing Tuner (not GenericTuner) since og has its own
 *	  600-line tuner with memory injection / token compress / upgrade.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/integration_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	ogdriver "github.com/sqlrush/opendb/internal/opengauss/driver"
	"github.com/sqlrush/opendb/internal/sqltune"
)

const envOGHost = "SQLTUNE_E2E_OPENGAUSS_HOST"

func TestIntegration_OG_SimpleSelect(t *testing.T) {
	driver := openOGOrSkip(t)
	defer driver.Close()

	// og uses its own complex Tuner (not GenericTuner) — verify Phase A
	// plan collector works against real og instance.
	planner := NewPlanner(driver)
	plan, err := planner.ExplainPlan(context.Background(), "SELECT 1",
		sqltune.ExplainOptions{Analyze: sqltune.AnalyzeSkip})
	if err != nil {
		t.Fatalf("ExplainPlan failed: %v", err)
	}
	if plan == nil || plan.Root == nil {
		t.Fatal("nil plan returned")
	}
}

func TestIntegration_OG_ExplainPerformanceWorks(t *testing.T) {
	driver := openOGOrSkip(t)
	defer driver.Close()

	planner, ok := NewPlanner(driver).(sqltune.PerformancePlanner)
	if !ok {
		t.Fatal("og planner doesn't implement PerformancePlanner")
	}
	td, err := planner.ExplainPerformance(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("ExplainPerformance failed: %v", err)
	}
	if !td.Available {
		t.Errorf("Available = false, expected true; notes=%q", td.Notes)
	}
	if td.Format != "og_explain_performance" {
		t.Errorf("Format = %q, want og_explain_performance", td.Format)
	}
	if td.Body == "" {
		t.Error("Body is empty — EXPLAIN PERFORMANCE returned nothing")
	}
}

func TestIntegration_OG_EquivVerifierDMLReject(t *testing.T) {
	driver := openOGOrSkip(t)
	defer driver.Close()
	planner, ok := NewPlanner(driver).(sqltune.EquivVerifier)
	if !ok {
		t.Fatal("og planner doesn't implement EquivVerifier")
	}
	_, err := planner.VerifyEquivalence(context.Background(),
		"UPDATE pg_settings SET setting='1' WHERE name='X' AND 1=0",
		"SELECT 1", 10)
	if err == nil || !strings.Contains(err.Error(), "DML") {
		t.Errorf("expected DML reject, got %v", err)
	}
}

func openOGOrSkip(t *testing.T) db.Driver {
	t.Helper()
	host := os.Getenv(envOGHost)
	if host == "" {
		t.Skipf("set %s to run integration tests", envOGHost)
	}
	port, _ := strconv.Atoi(envOr("SQLTUNE_E2E_OPENGAUSS_PORT", "15432"))
	cfg := db.ConnectionConfig{
		DBType:   "opengauss",
		Host:     host,
		Port:     port,
		User:     envOr("SQLTUNE_E2E_OPENGAUSS_USER", "gauss"),
		Password: os.Getenv("SQLTUNE_E2E_OPENGAUSS_PASS"),
		Database: envOr("SQLTUNE_E2E_OPENGAUSS_DB", "postgres"),
	}
	drv, err := ogdriver.NewDriver(cfg)
	if err != nil {
		t.Fatalf("open og driver: %v", err)
	}
	if err := drv.Ping(context.Background()); err != nil {
		drv.Close()
		t.Fatalf("ping og: %v", err)
	}
	return drv
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
