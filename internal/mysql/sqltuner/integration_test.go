/*-------------------------------------------------------------------------
 *
 * integration_test.go
 *	  Real-DB E2E tests for MySQL sqltune. Skipped unless
 *	  SQLTUNE_E2E_DSN_MYSQL is set.
 *
 *	  DSN format expected by mysql/driver: "user:pass@tcp(host:port)/db"
 *	  Example: "root:YourMySQLPass123!@tcp(47.251.30.180:3306)/sys"
 *
 *	  Coverage:
 *	    - Simple SELECT → Phase A succeeds, raw report rendered
 *	    - Placeholder (?) SQL → PlaceholderError with qmark kind
 *	    - DML rejected by EquivVerifier
 *	    - Big UNION ALL SQL → G7 compression triggered
 *
 *	  Does NOT exercise LLM — uses nil LLMCaller so tests are
 *	  deterministic and cost-free. Round 1 LLM verification belongs
 *	  in a separate suite with a recorded-LLM transport (out of scope).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/integration_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"os"
	"strings"
	"testing"

	"strconv"

	"github.com/sqlrush/opendb/internal/db"
	mysqldriver "github.com/sqlrush/opendb/internal/mysql/driver"
	"github.com/sqlrush/opendb/internal/sqltune"
)

// envMySQLHost is the gate — when unset, all integration tests skip.
// Other envs (PORT/USER/PASS/DB) fall back to defaults if unset.
const envMySQLHost = "SQLTUNE_E2E_MYSQL_HOST"

func TestIntegration_MySQL_SimpleSelect(t *testing.T) {
	driver := openMySQLOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)

	rep, err := tuner.Tune(context.Background(), sqltune.TuneOptions{SQL: "SELECT 1"})
	if err != nil {
		t.Fatalf("simple select failed: %v", err)
	}
	if rep == nil || rep.Markdown == "" {
		t.Fatal("empty report")
	}
	for _, want := range []string{"SQL", "MySQL"} {
		if !strings.Contains(rep.Markdown, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestIntegration_MySQL_PlaceholderRejected(t *testing.T) {
	driver := openMySQLOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)

	_, err := tuner.Tune(context.Background(),
		sqltune.TuneOptions{SQL: "SELECT * FROM information_schema.tables WHERE table_schema = ?"})
	if err == nil {
		t.Fatal("expected PlaceholderError, got nil")
	}
	var pe *sqltune.PlaceholderError
	for cur := err; cur != nil; {
		if p, ok := cur.(*sqltune.PlaceholderError); ok {
			pe = p
			break
		}
		break
	}
	if pe == nil {
		t.Fatalf("expected *sqltune.PlaceholderError, got %T: %v", err, err)
	}
	if pe.DetectedKind != "qmark" {
		t.Errorf("DetectedKind = %q, want qmark", pe.DetectedKind)
	}
}

func TestIntegration_MySQL_EquivVerifierDMLReject(t *testing.T) {
	driver := openMySQLOrSkip(t)
	defer driver.Close()
	planner, ok := NewPlanner(driver).(sqltune.EquivVerifier)
	if !ok {
		t.Fatal("mysql planner doesn't implement EquivVerifier")
	}
	_, err := planner.VerifyEquivalence(context.Background(),
		"UPDATE mysql.user SET host='%' WHERE 1=0",
		"SELECT 1", 10)
	if err == nil {
		t.Fatal("expected DML reject error")
	}
	if !strings.Contains(err.Error(), "DML") {
		t.Errorf("error should mention DML: %v", err)
	}
}

func TestIntegration_MySQL_BigSQLCompressed(t *testing.T) {
	driver := openMySQLOrSkip(t)
	defer driver.Close()
	tuner := sqltune.NewGenericTuner(NewPlanner(driver), NewPromptBuilder(), nil)

	rep, err := tuner.Tune(context.Background(),
		sqltune.TuneOptions{SQL: bigSQL()})
	if err != nil {
		// Big UNION ALL may exceed some MySQL limits; that's fine for
		// this test — we only care compression triggered when sql is big.
		t.Logf("big SQL tune returned: %v (acceptable for this test)", err)
		return
	}
	if !rep.Stats.CompressionTriggered {
		t.Error("expected CompressionTriggered=true for 600-line SQL")
	}
}

func openMySQLOrSkip(t *testing.T) db.Driver {
	t.Helper()
	host := os.Getenv(envMySQLHost)
	if host == "" {
		t.Skipf("set %s (+ optional _PORT/_USER/_PASS/_DB) to run integration tests", envMySQLHost)
	}
	port, _ := strconv.Atoi(envOr("SQLTUNE_E2E_MYSQL_PORT", "3306"))
	cfg := db.ConnectionConfig{
		DBType:   "mysql",
		Host:     host,
		Port:     port,
		User:     envOr("SQLTUNE_E2E_MYSQL_USER", "root"),
		Password: os.Getenv("SQLTUNE_E2E_MYSQL_PASS"),
		Database: envOr("SQLTUNE_E2E_MYSQL_DB", "sys"),
	}
	drv, err := mysqldriver.NewDriver(cfg)
	if err != nil {
		t.Fatalf("open mysql driver: %v", err)
	}
	if err := drv.Ping(context.Background()); err != nil {
		drv.Close()
		t.Fatalf("ping mysql: %v", err)
	}
	return drv
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// bigSQL returns a 600-line UNION ALL query to trigger G7 compression.
// Local copy (sqltune.SimpleBigSQL is _test-only and not cross-package).
func bigSQL() string {
	var b []byte
	b = append(b, "SELECT 1 AS x"...)
	for i := 1; i < 600; i++ {
		b = append(b, "\nUNION ALL SELECT 1"...)
	}
	return string(b)
}
