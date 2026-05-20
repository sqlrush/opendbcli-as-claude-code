/*-------------------------------------------------------------------------
 *
 * equiv.go
 *	  PG implementation of sqltune.EquivVerifier. Same shape as og's
 *	  (PG and openGauss share md5/string_agg/::text syntax), kept as
 *	  a separate file in the pg package to avoid cross-package
 *	  dependencies between dialect implementations.
 *
 *	  See internal/opengauss/sqltuner/equiv.go for the strategy
 *	  rationale + limitations (NULL semantics, float precision).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/equiv.go
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
)

// VerifyEquivalence implements sqltune.EquivVerifier for pg.
func (p *pgPlanner) VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error) {
	if isDMLPG(origSQL) || isDMLPG(candidateSQL) {
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

func (p *pgPlanner) runRowHash(ctx context.Context, query string, limit int) (string, error) {
	hashSQL := fmt.Sprintf(`
SELECT md5(string_agg(row_text, '|' ORDER BY row_text))
  FROM (
    SELECT (sub.*)::text AS row_text
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

// isDMLPG returns true for INSERT/UPDATE/DELETE/MERGE/DDL. Local copy
// to avoid cross-package dependency.
func isDMLPG(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) < 6 {
		return false
	}
	upper := strings.ToUpper(trimmed)
	for _, kw := range []string{"INSERT", "UPDATE", "DELETE", "MERGE",
		"CREATE", "DROP", "ALTER", "TRUNCATE", "GRANT", "REVOKE"} {
		if strings.HasPrefix(upper, kw+" ") || strings.HasPrefix(upper, kw+"\n") || strings.HasPrefix(upper, kw+"\t") {
			return true
		}
	}
	// WITH ... DML — already detected by isReadOnlyQuery in explain.go
	if strings.HasPrefix(upper, "WITH ") && !isReadOnlyQuery(sql) {
		return true
	}
	return false
}
