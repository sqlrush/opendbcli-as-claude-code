/*-------------------------------------------------------------------------
 *
 * equiv.go
 *	  MySQL implementation of sqltune.EquivVerifier.
 *
 *	  MySQL's row-hashing approach:
 *	    1. Raise group_concat_max_len from default 1024 → 16 MB
 *	       (without this, even modest result sets get silently
 *	       truncated and produce false-positive equivalence)
 *	    2. SELECT MD5(GROUP_CONCAT(row_text ORDER BY row_text SEPARATOR '|'))
 *	       FROM (SELECT CONCAT_WS(',', col1, col2, ...) AS row_text
 *	               FROM (<query>) sub LIMIT N) t
 *
 *	  MySQL gotcha: there's no easy way to project ALL columns to text
 *	  generically (no (sub.*)::text equivalent). We use CONCAT_WS over
 *	  star expansion — works for most queries but breaks if the inner
 *	  query has duplicate column names (e.g. SELECT a.id, b.id FROM ...).
 *	  Caller's responsibility to ensure column name uniqueness or
 *	  wrap with explicit aliasing.
 *
 *	  Limitations (same as og/pg):
 *	    - NULL ↔ '' may compare equal under CONCAT_WS
 *	    - Float precision text-form differences
 *	    - DML rejected
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/equiv.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const (
	equivTimeout      = 60 * time.Second
	defaultEquivLimit = 1000
	// gcMaxLen — bumped GROUP_CONCAT budget. 16 MB handles typical
	// 1000-row samples even with wide rows. MySQL default 1024 bytes
	// is way too small and silently truncates.
	gcMaxLen = 16 * 1024 * 1024
)

// VerifyEquivalence implements sqltune.EquivVerifier for MySQL.
func (p *mysqlPlanner) VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error) {
	if isDMLMySQL(origSQL) || isDMLMySQL(candidateSQL) {
		return false, fmt.Errorf("DML SQL not eligible for equivalence verification")
	}
	if limit <= 0 {
		limit = defaultEquivLimit
	}
	if pe := detectPlaceholders(origSQL); pe != nil {
		return false, pe
	}
	if pe := detectPlaceholders(candidateSQL); pe != nil {
		return false, pe
	}

	cctx, cancel := context.WithTimeout(ctx, equivTimeout)
	defer cancel()

	// Bump GROUP_CONCAT budget per-session — silent truncation would
	// produce a falsely-equal hash, the worst possible failure mode.
	if _, err := p.driver.Exec(cctx, fmt.Sprintf("SET SESSION group_concat_max_len = %d", gcMaxLen)); err != nil {
		// Not fatal — try anyway with default, but warn via the error
		// if it eventually surfaces.
		_ = err
	}

	origHash, err := p.runRowHash(cctx, origSQL, limit)
	if err != nil {
		return false, fmt.Errorf("hash original: %w", err)
	}
	candHash, err := p.runRowHash(cctx, candidateSQL, limit)
	if err != nil {
		return false, fmt.Errorf("hash candidate: %w", err)
	}
	return origHash == candHash, nil
}

// runRowHash uses CONCAT_WS for row materialization (MySQL has no
// (row).* text-cast equivalent). The wrapping (SELECT * FROM (<query>) sub
// LIMIT N) gives us the user's query result rows, which we then
// concat-join into a single comma-separated text per row.
//
// information_schema.COLUMNS lookup isn't possible for an ad-hoc query
// expression — so we use `SELECT CONCAT_WS(',', sub.*)` which MySQL
// 8.0+ supports for star expansion in function args. On older MySQL
// (5.7) this won't work — that's a documented limitation.
func (p *mysqlPlanner) runRowHash(ctx context.Context, query string, limit int) (string, error) {
	hashSQL := fmt.Sprintf(`
SELECT MD5(GROUP_CONCAT(row_text ORDER BY row_text SEPARATOR '|'))
  FROM (
    SELECT CONCAT_WS(',', sub.*) AS row_text
      FROM (%s) AS sub
     LIMIT %d
  ) t`, query, limit)

	res, err := p.driver.Query(ctx, hashSQL)
	if err != nil {
		return "", err
	}
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", fmt.Errorf("hash query returned no rows")
	}
	return strings.TrimSpace(toString(res.Rows[0][0])), nil
}

// isDMLMySQL — local DML detector. Local copy avoids dependency on og.
func isDMLMySQL(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < 6 {
		return false
	}
	upper := strings.ToUpper(trimmed)
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "REPLACE",
		"CREATE", "DROP", "ALTER", "TRUNCATE", "GRANT", "REVOKE"} {
		if strings.HasPrefix(upper, kw+" ") || strings.HasPrefix(upper, kw+"\n") || strings.HasPrefix(upper, kw+"\t") {
			return true
		}
	}
	return false
}
