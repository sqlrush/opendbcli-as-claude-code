/*-------------------------------------------------------------------------
 *
 * equiv_test.go
 *	  PG EquivVerifier tests.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/equiv_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestPGPlanner_SatisfiesEquivVerifier(t *testing.T) {
	var _ sqltune.EquivVerifier = (*pgPlanner)(nil)
}

func TestPGVerifyEquivalence_RejectsDML(t *testing.T) {
	p := &pgPlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"UPDATE t SET a = 1", "SELECT 1", 100)
	if err == nil {
		t.Error("expected error for DML, got nil")
	}
}

func TestPGVerifyEquivalence_RejectsPlaceholders(t *testing.T) {
	p := &pgPlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"SELECT * FROM t WHERE id = $1", "SELECT 1", 100)
	if err == nil {
		t.Error("expected PlaceholderError, got nil")
	}
}

func TestIsDMLPG(t *testing.T) {
	cases := map[string]bool{
		"SELECT 1":                              false,
		"  select 1":                            false,
		"WITH x AS (SELECT 1) SELECT * FROM x":  false,
		"WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x": true,
		"UPDATE t SET a = 1":                    true,
		"INSERT INTO t VALUES (1)":              true,
		"DELETE FROM t":                         true,
		"MERGE INTO t USING s ON t.id = s.id":   true,
		"CREATE TABLE t (a INT)":                true,
		"DROP TABLE t":                          true,
		"ALTER TABLE t ADD COLUMN b INT":        true,
		"TRUNCATE t":                            true,
		"":                                      false,
	}
	for sql, want := range cases {
		if got := isDMLPG(sql); got != want {
			t.Errorf("isDMLPG(%q) = %v, want %v", sql, got, want)
		}
	}
}
