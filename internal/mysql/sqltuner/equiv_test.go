/*-------------------------------------------------------------------------
 *
 * equiv_test.go
 *	  MySQL EquivVerifier tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/equiv_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestMySQLPlanner_SatisfiesEquivVerifier(t *testing.T) {
	var _ sqltune.EquivVerifier = (*mysqlPlanner)(nil)
}

func TestMySQLVerifyEquivalence_RejectsDML(t *testing.T) {
	p := &mysqlPlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"REPLACE INTO t (id) VALUES (1)", "SELECT 1", 100)
	if err == nil {
		t.Error("expected error for REPLACE (MySQL DML), got nil")
	}
}

func TestMySQLVerifyEquivalence_RejectsPlaceholders(t *testing.T) {
	p := &mysqlPlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"SELECT * FROM t WHERE id = ?", "SELECT 1", 100)
	if err == nil {
		t.Error("expected PlaceholderError for ?, got nil")
	}
}

func TestIsDMLMySQL(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                false,
		"UPDATE t SET a=1":        true,
		"INSERT INTO t VALUES":    true,
		"DELETE FROM t":           true,
		"REPLACE INTO t":          true,
		"CREATE TABLE t":          true,
		"DROP TABLE t":            true,
		"ALTER TABLE t":           true,
		"TRUNCATE TABLE t":        true,
		"GRANT SELECT":            true,
		"REVOKE SELECT":           true,
		"":                        false,
	}
	for sql, want := range cases {
		if got := isDMLMySQL(sql); got != want {
			t.Errorf("isDMLMySQL(%q) = %v, want %v", sql, got, want)
		}
	}
}
