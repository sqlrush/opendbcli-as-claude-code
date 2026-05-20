/*-------------------------------------------------------------------------
 *
 * integration_helpers_test.go
 *	  Helpers consumed by each dialect's *_integration_test.go.
 *	  Defines per-dialect SQL fixtures and the env-var convention for
 *	  enabling real-DB tests.
 *
 *	  Convention: each dialect's integration_test.go does:
 *	    dsn := os.Getenv("SQLTUNE_E2E_DSN_<DIALECT>")
 *	    if dsn == "" { t.Skip(...) }
 *	    // open driver, build planner, run RealDBScenarios(t, planner)
 *
 *	  Environment variables checked:
 *	    SQLTUNE_E2E_DSN_MYSQL     — MySQL DSN
 *	    SQLTUNE_E2E_DSN_POSTGRES  — PostgreSQL DSN
 *	    SQLTUNE_E2E_DSN_OPENGAUSS — openGauss DSN
 *	    SQLTUNE_E2E_DSN_ORACLE    — Oracle DSN
 *	    SQLTUNE_E2E_DSN_GAUSSDB   — GaussDB DSN (optional;
 *	                                 inherits og decorator, og DSN suffices for most)
 *
 *	  All tests no-op when env var missing — safe to run on dev machine
 *	  without DSNs. CI/release pipeline sets DSNs to fail loudly on
 *	  real-world regressions.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/integration_helpers_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

// DialectSQLFixtures bundles per-dialect SQL strings for canonical
// integration scenarios. Each dialect's integration test instantiates
// this with its specific syntax.
//
// Why per-dialect: simple SELECT is universal, but JOIN syntax,
// placeholder style, and "big SQL" patterns differ enough that
// hardcoding generic SQL would fail on at least one dialect.
type DialectSQLFixtures struct {
	// Simple — universally-EXPLAIN-able SELECT, succeeds on a fresh DB.
	// Suggestion: "SELECT 1" (works everywhere) or "SELECT 1 FROM dual" (Oracle).
	Simple string

	// Placeholder — SQL with this dialect's unbound placeholder style,
	// expected to be rejected with PlaceholderError of matching kind.
	// PG/og: "SELECT * FROM information_schema.tables WHERE table_schema = $1"
	// MySQL: "SELECT * FROM information_schema.tables WHERE table_schema = ?"
	// Oracle: "SELECT * FROM all_tables WHERE owner = :1"
	Placeholder            string
	PlaceholderExpectedKind string

	// DML — write SQL, should be rejected by VerifyEquivalence (and is
	// safe to feed to ExplainPlan since PG-family wraps in BEGIN/ROLLBACK).
	// Choose a no-op DML: UPDATE on a known empty table or DELETE with
	// WHERE 1=0.
	DML string

	// BigSQL — SQL of 600+ lines (typically a UNION ALL series) that
	// triggers G7 token compression. Should still successfully explain
	// (CompressionTriggered=true).
	BigSQL string
}

// SimpleBigSQL generates a 600-line UNION ALL SELECT 1 query for
// dialects that accept it. Useful default for BigSQL fixture.
func SimpleBigSQL() string {
	var b []byte
	b = append(b, "SELECT 1 AS x"...)
	for i := 1; i < 600; i++ {
		b = append(b, "\nUNION ALL SELECT 1"...)
	}
	return string(b)
}
