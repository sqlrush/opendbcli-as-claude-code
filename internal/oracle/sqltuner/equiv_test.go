/*-------------------------------------------------------------------------
 *
 * equiv_test.go
 *	  Oracle EquivVerifier tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/equiv_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOraclePlanner_SatisfiesEquivVerifier(t *testing.T) {
	var _ sqltune.EquivVerifier = (*oraclePlanner)(nil)
}

func TestOracleVerifyEquivalence_RejectsDML(t *testing.T) {
	p := &oraclePlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"MERGE INTO t USING s ON (t.id = s.id) WHEN MATCHED THEN UPDATE SET a = 1", "SELECT 1", 100)
	if err == nil {
		t.Error("expected error for MERGE (Oracle DML), got nil")
	}
}

func TestOracleVerifyEquivalence_RejectsPlaceholders(t *testing.T) {
	p := &oraclePlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"SELECT * FROM emp WHERE empno = :1", "SELECT * FROM emp WHERE empno = 100", 100)
	if err == nil {
		t.Error("expected PlaceholderError for :1, got nil")
	}
}

func TestIsDMLOracle(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1 FROM dual":                       false,
		"UPDATE emp SET sal = 1000":                true,
		"INSERT INTO emp VALUES (1)":               true,
		"DELETE FROM emp":                          true,
		"MERGE INTO t USING s ON (t.id = s.id)":    true,
		"CREATE TABLE t (a NUMBER)":                true,
		"DROP TABLE t":                             true,
		"ALTER TABLE t":                            true,
		"TRUNCATE TABLE t":                         true,
		"GRANT SELECT ON t TO scott":               true,
		"":                                         false,
	}
	for sql, want := range cases {
		if got := isDMLOracle(sql); got != want {
			t.Errorf("isDMLOracle(%q) = %v, want %v", sql, got, want)
		}
	}
}
