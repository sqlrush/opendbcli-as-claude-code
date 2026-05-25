/*-------------------------------------------------------------------------
 *
 * equiv_test.go
 *	  og EquivVerifier tests: interface assertion + DML rejection +
 *	  placeholder rejection. Live row-hash tests need a real og
 *	  instance and belong in an integration suite.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/equiv_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestOGPlanner_SatisfiesEquivVerifier(t *testing.T) {
	var _ sqltune.EquivVerifier = (*ogPlanner)(nil)
}

func TestOGVerifyEquivalence_RejectsDML(t *testing.T) {
	p := &ogPlanner{driver: nil} // nil driver — DML guard runs first
	cases := []string{
		"UPDATE t SET a = 1",
		"DELETE FROM t WHERE id = 5",
		"INSERT INTO t VALUES (1)",
	}
	for _, sql := range cases {
		_, err := p.VerifyEquivalence(context.Background(), sql, "SELECT 1", 100)
		if err == nil {
			t.Errorf("expected error for DML origSQL=%q, got nil", sql)
		}
		_, err = p.VerifyEquivalence(context.Background(), "SELECT 1", sql, 100)
		if err == nil {
			t.Errorf("expected error for DML candidateSQL=%q, got nil", sql)
		}
	}
}

func TestOGVerifyEquivalence_RejectsPlaceholders(t *testing.T) {
	p := &ogPlanner{driver: nil}
	_, err := p.VerifyEquivalence(context.Background(),
		"SELECT * FROM t WHERE id = $1", "SELECT * FROM t WHERE id = 1", 100)
	if err == nil {
		t.Error("expected PlaceholderError for $1 in origSQL, got nil")
	}
	if !strings.Contains(err.Error(), "占位符") {
		t.Errorf("error msg should mention 占位符: %v", err)
	}
}
