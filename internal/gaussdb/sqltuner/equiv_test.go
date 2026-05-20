/*-------------------------------------------------------------------------
 *
 * equiv_test.go
 *	  GaussDB EquivVerifier tests — verifies decorator forwards the
 *	  call to og's implementation (which has the actual logic).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/equiv_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/sqltune"
)

func TestGaussDBPlanner_SatisfiesEquivVerifier(t *testing.T) {
	// Critical: decorator must implement the interface so type-assert
	// in og's tuner.verifyOne triggers.
	var _ sqltune.EquivVerifier = (*gaussdbPlanner)(nil)
}

func TestGaussDBVerifyEquivalence_ForwardsToOG_DMLReject(t *testing.T) {
	// Pass nil driver: og's DML guard runs first → forwarding works.
	p := NewPlanner(nil).(*gaussdbPlanner)
	_, err := p.VerifyEquivalence(context.Background(),
		"UPDATE t SET a = 1", "SELECT 1", 100)
	if err == nil {
		t.Error("expected DML reject from forwarded og call, got nil")
	}
}
