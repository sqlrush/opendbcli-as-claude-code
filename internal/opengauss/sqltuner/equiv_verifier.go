/*-------------------------------------------------------------------------
 *
 * equiv_verifier.go
 *	  EquivVerifier checks if a rewritten SQL produces the same result
 *	  set as the original.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/equiv_verifier.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/db"
)

// EquivVerifier checks if a rewritten SQL produces the same result set as the original.
//
// Strategy (per design doc §6 M4):
//   - Wrap both queries in stable ORDER BY + LIMIT 1000
//   - Compute md5(string_agg(row::text)) on both
//   - md5 hash equality → semantically equivalent (within sample)
//
// Limitations:
//   - read-only SQL only (DML returns Verifiable=false)
//   - sample-based, not full equivalence
//   - some edge cases (NULL semantics, float precision) may produce false negatives
type EquivVerifier struct {
	driver db.Driver
}

func NewEquivVerifier(d db.Driver) *EquivVerifier { return &EquivVerifier{driver: d} }

// Verify returns (isEquivalent, error).
// If SQL is DML or hashing fails, returns (false, error) — caller should mark unverified.
func (v *EquivVerifier) Verify(ctx context.Context, origSQL, newSQL string) (bool, error) {
	if isDML(origSQL) || isDML(newSQL) {
		return false, fmt.Errorf("DML SQL not eligible for equivalence verification")
	}

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// Hash query: wrap user SQL in subquery, ORDER BY all columns to stabilize, LIMIT 1000.
	// Use md5(string_agg(row::text, ',' ORDER BY row::text)) so hash is order-independent
	// (we don't know the user's intended sort order, so sorting by row text gives stability).
	hashSQL := func(query string) string {
		return fmt.Sprintf(`
SELECT md5(string_agg(row_text, '|' ORDER BY row_text))
FROM (
  SELECT (sub.*)::text AS row_text
  FROM (%s) AS sub
  LIMIT 1000
) t`, query)
	}

	origHash, err := v.runHash(cctx, hashSQL(origSQL))
	if err != nil {
		return false, fmt.Errorf("hash original: %w", err)
	}
	newHash, err := v.runHash(cctx, hashSQL(newSQL))
	if err != nil {
		return false, fmt.Errorf("hash candidate: %w", err)
	}

	return origHash == newHash, nil
}

func (v *EquivVerifier) runHash(ctx context.Context, sql string) (string, error) {
	res, err := v.driver.Query(ctx, sql)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Rows) == 0 {
		return "", fmt.Errorf("no rows from hash query")
	}
	h := asString(res.Rows[0][0])
	return strings.TrimSpace(h), nil
}
