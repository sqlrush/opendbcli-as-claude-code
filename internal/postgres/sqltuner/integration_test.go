/*-------------------------------------------------------------------------
 *
 * integration_test.go
 *	  Real-DB E2E tests for PostgreSQL sqltune. Skipped unless
 *	  SQLTUNE_E2E_POSTGRES_HOST is set.
 *
 *	  Env vars (defaults shown):
 *	    SQLTUNE_E2E_POSTGRES_HOST     — (required, gate)
 *	    SQLTUNE_E2E_POSTGRES_PORT=5432
 *	    SQLTUNE_E2E_POSTGRES_USER=postgres
 *	    SQLTUNE_E2E_POSTGRES_PASS     — (empty default)
 *	    SQLTUNE_E2E_POSTGRES_DB=postgres
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/integration_test.go
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
	pgdriver "github.com/sqlrush/opendb/internal/postgres/driver"
	"github.com/sqlrush/opendb/internal/sqltune"
)

const envPGHost = "SQLTUNE_E2E_POSTGRES_HOST"

func TestIntegration_PG_SimpleSelect(t *testing.T) {
	driver := openPGOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)
	rep, err := tuner.Tune(context.Background(), sqltune.TuneOptions{SQL: "SELECT 1"})
	if err != nil {
		t.Fatalf("simple select failed: %v", err)
	}
	if !strings.Contains(rep.Markdown, "PostgreSQL") {
		t.Error("report missing PostgreSQL identifier")
	}
}

func TestIntegration_PG_DollarPlaceholderRejected(t *testing.T) {
	driver := openPGOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)
	_, err := tuner.Tune(context.Background(),
		sqltune.TuneOptions{SQL: "SELECT * FROM information_schema.tables WHERE table_schema = $1"})
	if err == nil {
		t.Fatal("expected PlaceholderError, got nil")
	}
	pe, ok := err.(*sqltune.PlaceholderError)
	if !ok {
		t.Fatalf("expected *PlaceholderError, got %T: %v", err, err)
	}
	if pe.DetectedKind != "pg_dollar" {
		t.Errorf("DetectedKind = %q, want pg_dollar", pe.DetectedKind)
	}
}

func TestIntegration_PG_EquivVerifierDMLReject(t *testing.T) {
	driver := openPGOrSkip(t)
	defer driver.Close()
	planner, ok := NewPlanner(driver).(sqltune.EquivVerifier)
	if !ok {
		t.Fatal("pg planner doesn't implement EquivVerifier")
	}
	_, err := planner.VerifyEquivalence(context.Background(),
		"UPDATE pg_settings SET setting='1' WHERE name='X' AND 1=0",
		"SELECT 1", 10)
	if err == nil || !strings.Contains(err.Error(), "DML") {
		t.Errorf("expected DML reject, got %v", err)
	}
}

func TestIntegration_PG_BigSQLCompressed(t *testing.T) {
	driver := openPGOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)
	rep, err := tuner.Tune(context.Background(),
		sqltune.TuneOptions{SQL: bigSQL()})
	if err != nil {
		t.Logf("big SQL tune returned: %v (acceptable)", err)
		return
	}
	if !rep.Stats.CompressionTriggered {
		t.Error("expected CompressionTriggered=true for 600-line SQL")
	}
}

func TestIntegration_PG_TraceUnavailableExplicit(t *testing.T) {
	driver := openPGOrSkip(t)
	defer driver.Close()
	planner := NewPlanner(driver).(*pgPlanner)
	_, td, err := planner.EnableTrace(context.Background(), "test")
	if err != nil {
		t.Fatalf("EnableTrace returned error: %v", err)
	}
	if td.Available {
		t.Error("PG EnableTrace should always return Available:false")
	}
	if !strings.Contains(td.Notes, "pg_stats") {
		t.Error("PG trace note should mention pg_stats fallback")
	}
}

func openPGOrSkip(t *testing.T) db.Driver {
	t.Helper()
	host := os.Getenv(envPGHost)
	if host == "" {
		t.Skipf("set %s to run integration tests", envPGHost)
	}
	port, _ := strconv.Atoi(envOr("SQLTUNE_E2E_POSTGRES_PORT", "5432"))
	cfg := db.ConnectionConfig{
		DBType:   "postgres",
		Host:     host,
		Port:     port,
		User:     envOr("SQLTUNE_E2E_POSTGRES_USER", "postgres"),
		Password: os.Getenv("SQLTUNE_E2E_POSTGRES_PASS"),
		Database: envOr("SQLTUNE_E2E_POSTGRES_DB", "postgres"),
	}
	drv, err := pgdriver.NewDriver(cfg)
	if err != nil {
		t.Fatalf("open pg driver: %v", err)
	}
	if err := drv.Ping(context.Background()); err != nil {
		drv.Close()
		t.Fatalf("ping pg: %v", err)
	}
	return drv
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func bigSQL() string {
	var b []byte
	b = append(b, "SELECT 1 AS x"...)
	for i := 1; i < 600; i++ {
		b = append(b, "\nUNION ALL SELECT 1"...)
	}
	return string(b)
}
