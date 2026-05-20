/*-------------------------------------------------------------------------
 *
 * integration_test.go
 *	  Real-DB E2E tests for Oracle sqltune. Skipped unless
 *	  SQLTUNE_E2E_ORACLE_HOST is set.
 *
 *	  Env vars (defaults shown):
 *	    SQLTUNE_E2E_ORACLE_HOST       — (required, gate)
 *	    SQLTUNE_E2E_ORACLE_PORT=1521
 *	    SQLTUNE_E2E_ORACLE_USER=SYSTEM
 *	    SQLTUNE_E2E_ORACLE_PASS       — (empty default)
 *	    SQLTUNE_E2E_ORACLE_SERVICE=ORCLPDB1
 *
 *	  Oracle-specific coverage:
 *	    - SELECT 1 FROM dual (Oracle requires FROM in SELECT)
 *	    - :1 placeholder rejection
 *	    - MERGE statement DML rejection
 *	    - 10053 trace EnableTrace ALTER SESSION succeeds
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/integration_test.go
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
	oracledriver "github.com/sqlrush/opendb/internal/oracle/driver"
	"github.com/sqlrush/opendb/internal/sqltune"
)

const envOracleHost = "SQLTUNE_E2E_ORACLE_HOST"

func TestIntegration_Oracle_SimpleSelect(t *testing.T) {
	driver := openOracleOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)
	rep, err := tuner.Tune(context.Background(), sqltune.TuneOptions{SQL: "SELECT 1 FROM dual"})
	if err != nil {
		t.Fatalf("simple select failed: %v", err)
	}
	if !strings.Contains(rep.Markdown, "Oracle") {
		t.Error("report missing Oracle identifier")
	}
}

func TestIntegration_Oracle_ColonPlaceholderRejected(t *testing.T) {
	driver := openOracleOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)
	_, err := tuner.Tune(context.Background(),
		sqltune.TuneOptions{SQL: "SELECT * FROM all_tables WHERE owner = :1"})
	if err == nil {
		t.Fatal("expected PlaceholderError, got nil")
	}
	pe, ok := err.(*sqltune.PlaceholderError)
	if !ok {
		t.Fatalf("expected *PlaceholderError, got %T", err)
	}
	if pe.DetectedKind != "oracle_colon" {
		t.Errorf("DetectedKind = %q, want oracle_colon", pe.DetectedKind)
	}
}

func TestIntegration_Oracle_MergeDMLRejected(t *testing.T) {
	driver := openOracleOrSkip(t)
	defer driver.Close()
	planner, ok := NewPlanner(driver).(sqltune.EquivVerifier)
	if !ok {
		t.Fatal("oracle planner doesn't implement EquivVerifier")
	}
	_, err := planner.VerifyEquivalence(context.Background(),
		"MERGE INTO emp t USING dual s ON (1=0) WHEN MATCHED THEN UPDATE SET sal=1",
		"SELECT 1 FROM dual", 10)
	if err == nil || !strings.Contains(err.Error(), "DML") {
		t.Errorf("expected DML reject, got %v", err)
	}
}

func TestIntegration_Oracle_10053EnableSucceeds(t *testing.T) {
	driver := openOracleOrSkip(t)
	defer driver.Close()
	planner := NewPlanner(driver).(*oraclePlanner)
	closeFn, td, err := planner.EnableTrace(context.Background(), "test")
	if err != nil {
		t.Fatalf("EnableTrace returned error: %v", err)
	}
	defer closeFn()
	if !td.Available {
		t.Errorf("EnableTrace Available=%v want true (ALTER SESSION succeeded); notes=%q",
			td.Available, td.Notes)
	}
	if td.Format != "oracle_10053" {
		t.Errorf("Format = %q, want oracle_10053", td.Format)
	}
}

func openOracleOrSkip(t *testing.T) db.Driver {
	t.Helper()
	host := os.Getenv(envOracleHost)
	if host == "" {
		t.Skipf("set %s to run integration tests", envOracleHost)
	}
	port, _ := strconv.Atoi(envOr("SQLTUNE_E2E_ORACLE_PORT", "1521"))
	cfg := db.ConnectionConfig{
		DBType:   "oracle",
		Host:     host,
		Port:     port,
		User:     envOr("SQLTUNE_E2E_ORACLE_USER", "SYSTEM"),
		Password: os.Getenv("SQLTUNE_E2E_ORACLE_PASS"),
		Service:  envOr("SQLTUNE_E2E_ORACLE_SERVICE", "ORCLPDB1"),
	}
	drv, err := oracledriver.NewDriver(cfg)
	if err != nil {
		t.Fatalf("open oracle driver: %v", err)
	}
	if err := drv.Ping(context.Background()); err != nil {
		drv.Close()
		t.Fatalf("ping oracle: %v", err)
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
	b = append(b, "SELECT 1 FROM dual"...)
	for i := 1; i < 600; i++ {
		b = append(b, "\nUNION ALL SELECT 1 FROM dual"...)
	}
	return string(b)
}
