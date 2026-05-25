/*-------------------------------------------------------------------------
 *
 * equiv.go
 *	  Oracle implementation of sqltune.EquivVerifier.
 *
 *	  Oracle row-hashing approach (12c+):
 *	    STANDARD_HASH(LISTAGG(row_text, '|') WITHIN GROUP (ORDER BY row_text), 'MD5')
 *	  where row_text comes from XMLAGG / SYS_OP_TOSID / DBMS_LOB.SUBSTR
 *	  pattern. We use a simpler approach with rowtype-to-text:
 *
 *	    SELECT STANDARD_HASH(
 *	             LISTAGG(row_text, '|') WITHIN GROUP (ORDER BY row_text),
 *	             'MD5')
 *	      FROM (
 *	        SELECT TO_CHAR(...) AS row_text  -- column-by-column concat
 *	          FROM (<query>) sub
 *	         WHERE ROWNUM <= N
 *	      )
 *
 *	  But we don't know column names ahead of time, so we use the
 *	  ANYDATA approach via XMLELEMENT for generic projection:
 *
 *	    XMLAGG(XMLELEMENT("r", sub.*).GETCLOBVAL())
 *
 *	  Limitations:
 *	    - 11g doesn't have STANDARD_HASH → returns (false, err)
 *	    - LISTAGG has 4000 char limit → ORA-01489 on wide rows; we use
 *	      ON OVERFLOW TRUNCATE WITHOUT COUNT for 12.2+
 *	    - XMLAGG produces CLOB which can hash large content but is slow
 *
 *	  For MVP we go with the LISTAGG + STANDARD_HASH path. Failure modes:
 *	    - ORA-01489 on wide rows → returns (false, err) with explanatory note
 *	    - 11g STANDARD_HASH missing → returns (false, err) suggesting upgrade
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/equiv.go
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

// VerifyEquivalence implements sqltune.EquivVerifier for Oracle.
//
// Strategy: project each row to XML CLOB via XMLAGG + XMLELEMENT (works
// generically without knowing column names), then STANDARD_HASH the
// full concatenation. CLOB-handling avoids LISTAGG's 4000-char limit.
//
// Note that 12c+ is required for STANDARD_HASH. Older versions get a
// typed error suggesting upgrade or external verification.
func (p *oraclePlanner) VerifyEquivalence(ctx context.Context, origSQL, candidateSQL string, limit int) (bool, error) {
	if isDMLOracle(origSQL) || isDMLOracle(candidateSQL) {
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

// runRowHash projects rows via XMLAGG/XMLELEMENT for generic column
// handling, then STANDARD_HASH the resulting CLOB.
//
// XMLELEMENT("r", sub.*) renders each row as <r><col1>v1</col1>...</r>
// which gives deterministic per-row text. XMLAGG concatenates them.
// GETCLOBVAL avoids 32K varchar limit. STANDARD_HASH accepts CLOB
// since 12.2+.
//
// We ORDER BY the rendered XML inside XMLAGG so hash is order-independent.
func (p *oraclePlanner) runRowHash(ctx context.Context, query string, limit int) (string, error) {
	hashSQL := fmt.Sprintf(`
SELECT STANDARD_HASH(
         XMLAGG(
           XMLELEMENT("r", sub.*)
           ORDER BY XMLSERIALIZE(CONTENT XMLELEMENT("r", sub.*) AS CLOB)
         ).GETCLOBVAL(),
         'MD5')
  FROM (
    SELECT * FROM (%s)
     WHERE ROWNUM <= %d
  ) sub`, query, limit)

	res, err := p.driver.Query(ctx, hashSQL)
	if err != nil {
		// 11g returns ORA-00904 invalid identifier for STANDARD_HASH.
		// Surface with a note suggesting upgrade.
		return "", fmt.Errorf("oracle row hash failed (needs 12c+ for STANDARD_HASH): %w", err)
	}
	if res == nil || len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		return "", fmt.Errorf("hash query returned no rows")
	}
	return strings.TrimSpace(toString(res.Rows[0][0])), nil
}

// isDMLOracle — local DML detector for Oracle (recognizes MERGE, etc.).
func isDMLOracle(sql string) bool {
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
	return false
}
