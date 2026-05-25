/*-------------------------------------------------------------------------
 *
 * equiv.go
 *	  og implementation of sqltune.EquivVerifier.
 *
 *	  Replaces the old EquivVerifier struct (in equiv_verifier.go,
 *	  deleted at M6.2) with a method on ogPlanner that satisfies the
 *	  neutral EquivVerifier interface. Uses openGauss's PG-compatible
 *	  `md5(string_agg(... ORDER BY ...))` aggregation — same SQL works
 *	  on PG planner (M6.2 copies this verbatim into pg).
 *
 *	  Method:
 *	    Wrap each SQL in a subquery, project all columns to text
 *	    (`(sub.*)::text` cast), aggregate with `string_agg` ORDER BY
 *	    the text representation (gives order-independent hash), compute
 *	    md5 of the joined string.
 *
 *	  Limitations:
 *	    - NULL semantics: NULL ↔ '' may compare equal under string_agg
 *	    - Float precision: 1.0 ↔ 1.00 may differ in text form
 *	    - These produce false negatives (claiming non-equivalent when
 *	      semantically same); never false positives — safe failure mode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/equiv.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/sqltune"
)

// equivTimeout caps both hash queries. Sample-based comparison so
// shouldn't take long; if it does, the candidate SQL likely has a
// performance issue worth flagging separately.
const equivTimeout = 60 * time.Second

// defaultEquivLimit is used when caller passes limit<=0. 1000 rows
// is a reasonable default — large enough to catch most semantic
// differences, small enough to keep query cheap.
const defaultEquivLimit = 1000

// VerifyEquivalence implements sqltune.EquivVerifier for og. Same SQL
// works on PG (M6.2 duplicates this into pg planner for clean per-
// dialect packaging without cross-package dependencies).
func (p *ogPlanner) VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error) {
	if isDML(origSQL) || isDML(candidateSQL) {
		return false, fmt.Errorf("DML SQL not eligible for equivalence verification (would double-apply changes)")
	}
	if limit <= 0 {
		limit = defaultEquivLimit
	}
	// Reject SQL with placeholders — can't run hash query on parameterized SQL.
	// detectPlaceholders is og's local check (returns *PlaceholderSQLError).
	if pe := detectPlaceholders(origSQL); pe != nil {
		return false, &sqltune.PlaceholderError{
			SQL:          origSQL,
			Placeholders: pe.Samples,
			DetectedKind: classifyPlaceholderKind(pe.Samples),
			Suggestion:   "EquivVerifier 不能处理含占位符的 SQL — " + pe.Error(),
			Recoverable:  true,
		}
	}
	if pe := detectPlaceholders(candidateSQL); pe != nil {
		return false, &sqltune.PlaceholderError{
			SQL:          candidateSQL,
			Placeholders: pe.Samples,
			DetectedKind: classifyPlaceholderKind(pe.Samples),
			Suggestion:   "EquivVerifier 不能处理含占位符的 SQL — " + pe.Error(),
			Recoverable:  true,
		}
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

// runRowHash wraps query in (SELECT (sub.*)::text FROM (query) sub LIMIT N)
// then md5(string_agg(row_text ORDER BY row_text)) for order-independent hash.
//
// The double subquery is required because string_agg can't directly
// consume a SELECT *; we materialize each row as text first.
func (p *ogPlanner) runRowHash(ctx context.Context, query string, limit int) (string, error) {
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
	h, ok := res.Rows[0][0].(string)
	if !ok {
		if b, ok := res.Rows[0][0].([]byte); ok {
			h = string(b)
		}
	}
	return strings.TrimSpace(h), nil
}

