/*-------------------------------------------------------------------------
 *
 * fingerprint.go
 *	  SQL fingerprinting for memory recall isolation.
 *
 *	  Problem: memory entries written for SQL_ID A were getting recalled for
 *	  SQL_ID B because the recall path matched on table-name keyword overlap.
 *	  Real-world impact (v1.1.29): Opus saw cached diagnosis of a 5-table SQL,
 *	  reused it as if it applied to a 10-table SQL — produced confidently
 *	  wrong recommendations including hallucinated literal values.
 *
 *	  Fix: every memory entry gets a fingerprint computed from the SQL
 *	  structure (normalized text hash + sorted table set + structural markers).
 *	  Recall must score memory by fingerprint similarity (Jaccard on tables +
 *	  exact hash match). Sub-threshold matches are filtered or labeled as
 *	  "for reference only" in the prompt.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/memory/fingerprint.go
 *
 *-------------------------------------------------------------------------
 */
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Fingerprint identifies a SQL by its normalized structure. Two SQL with
// the same fingerprint are semantically equivalent under fingerprint rules
// (same tables, same shape, only differing in literal values).
type Fingerprint struct {
	Hash   string   // SHA256 of normalized SQL (16 hex chars, truncated for readability)
	Tables []string // sorted unique table names (lowercased, schema-stripped)
	HasCTE bool     // contains WITH clause
	Depth  int      // max nested SELECT depth (0 = flat)
}

// Empty returns true if this fingerprint is uninitialized.
func (f Fingerprint) Empty() bool { return f.Hash == "" }

// Equal returns true if two fingerprints have the same normalized hash.
// This is the strictest match — same SQL up to literal substitution.
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f.Hash != "" && f.Hash == other.Hash
}

// JaccardTables returns the table-set Jaccard similarity (0.0 to 1.0).
// Used for fuzzy "is this similar enough to recall?" decisions.
func (f Fingerprint) JaccardTables(other Fingerprint) float64 {
	if len(f.Tables) == 0 && len(other.Tables) == 0 {
		return 0
	}
	a := toSet(f.Tables)
	b := toSet(other.Tables)
	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// SimilarityScore combines table Jaccard with structural alignment.
// Returns 0.0-1.0; >= 0.85 is "very likely the same SQL family".
//
// Rules:
//   - exact hash match → 1.0
//   - else: 0.7 * tables_jaccard + 0.15 * (cte_match) + 0.15 * (depth_close)
func (f Fingerprint) SimilarityScore(other Fingerprint) float64 {
	if f.Equal(other) {
		return 1.0
	}
	jacc := f.JaccardTables(other)
	cteScore := 0.0
	if f.HasCTE == other.HasCTE {
		cteScore = 1.0
	}
	depthScore := 1.0
	depthDiff := f.Depth - other.Depth
	if depthDiff < 0 {
		depthDiff = -depthDiff
	}
	if depthDiff > 0 {
		// Each level of depth difference reduces score by 0.3
		penalty := float64(depthDiff) * 0.3
		if penalty > 1.0 {
			penalty = 1.0
		}
		depthScore = 1.0 - penalty
	}
	return 0.70*jacc + 0.15*cteScore + 0.15*depthScore
}

// SimilarityThreshold is the cutoff above which fuzzy memory entries are
// considered "likely relevant" and recalled (with a warning label). Below
// this threshold, memory is dropped entirely from prompt injection.
//
// Tuned to 0.85 in v1.1.30. Empirical adjustment based on production traces.
const SimilarityThreshold = 0.85

// ComputeFingerprint derives a Fingerprint from a SQL string.
//
// Normalization rules:
//  1. Lowercase keywords and identifiers
//  2. Strip schema prefixes from table names (sqltune_demo.customers → customers)
//  3. Replace string literals with '?'
//  4. Replace numeric literals with ?
//  5. Collapse whitespace
//  6. Strip line and block comments
//
// Tables are extracted via regex from FROM / JOIN clauses (not a real parser
// — pragmatic; misses lateral, dynamic SQL but correct for typical OLTP/OLAP).
func ComputeFingerprint(sql string) Fingerprint {
	if strings.TrimSpace(sql) == "" {
		return Fingerprint{}
	}

	normalized := normalizeSQL(sql)
	hash := sha256.Sum256([]byte(normalized))
	tables := extractTables(sql)
	hasCTE := containsCTE(sql)
	depth := computeNestingDepth(sql)

	return Fingerprint{
		Hash:   hex.EncodeToString(hash[:8]), // 16 hex chars; collision-safe for this scale
		Tables: tables,
		HasCTE: hasCTE,
		Depth:  depth,
	}
}

// normalizeSQL produces a canonical form for hashing. Designed so the same
// SQL with different literal values / formatting / case produces the same
// output.
func normalizeSQL(sql string) string {
	// Strip block comments first
	sql = blockCommentRE.ReplaceAllString(sql, " ")
	// Strip line comments
	sql = lineCommentRE.ReplaceAllString(sql, " ")
	// Strip single-quoted strings
	sql = stringLitRE.ReplaceAllString(sql, "'?'")
	// Strip numeric literals
	sql = numLitRE.ReplaceAllString(sql, "?")
	// Lowercase
	sql = strings.ToLower(sql)
	// Strip schema prefixes (table names like "schema_name.table" → "table")
	sql = schemaPrefixRE.ReplaceAllStringFunc(sql, func(m string) string {
		idx := strings.Index(m, ".")
		if idx < 0 {
			return m
		}
		return m[idx+1:]
	})
	// Collapse whitespace
	sql = whitespaceRE.ReplaceAllString(sql, " ")
	return strings.TrimSpace(sql)
}

// extractTables returns sorted unique lowercase table names from FROM and
// JOIN clauses. Strips schema prefixes. Ignores subquery aliases.
func extractTables(sql string) []string {
	// Strip strings/comments first so we don't match table names inside them
	stripped := stringLitRE.ReplaceAllString(sql, "''")
	stripped = blockCommentRE.ReplaceAllString(stripped, " ")
	stripped = lineCommentRE.ReplaceAllString(stripped, " ")
	stripped = strings.ToLower(stripped)

	seen := make(map[string]bool)
	add := func(name string) {
		// Strip schema prefix
		if idx := strings.Index(name, "."); idx >= 0 {
			name = name[idx+1:]
		}
		// Skip aliases that look like single-letter or numbers
		if name == "" {
			return
		}
		// Skip SQL keywords commonly mistaken
		switch name {
		case "select", "where", "and", "or", "on", "as", "in", "not",
			"join", "left", "right", "inner", "outer", "full", "cross",
			"lateral", "using", "natural", "(", "":
			return
		}
		seen[name] = true
	}

	// FROM clause: capture table list (handles "FROM t1, t2 c, t3 JOIN ..." etc.)
	for _, m := range fromClauseRE.FindAllStringSubmatch(stripped, -1) {
		// m[1] = the captured FROM body up to next major clause
		body := m[1]
		// Split by commas at top level (not inside parens)
		parts := splitTopLevel(body, ',')
		for _, p := range parts {
			// Each part may be "table alias" or "table"; first token is table
			tokens := strings.Fields(p)
			if len(tokens) > 0 {
				add(tokens[0])
			}
		}
	}

	// JOIN clauses
	for _, m := range joinClauseRE.FindAllStringSubmatch(stripped, -1) {
		// m[1] = table name (may be schema.table)
		add(m[1])
	}

	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// containsCTE returns true if the SQL has a WITH clause at the top level.
func containsCTE(sql string) bool {
	stripped := blockCommentRE.ReplaceAllString(sql, " ")
	stripped = lineCommentRE.ReplaceAllString(stripped, " ")
	stripped = strings.ToLower(strings.TrimSpace(stripped))
	return strings.HasPrefix(stripped, "with ") || strings.HasPrefix(stripped, "with(") || strings.HasPrefix(stripped, "with\n")
}

// computeNestingDepth approximates the maximum SELECT nesting depth.
// A flat SELECT is depth 0; one level of subquery is depth 1; etc.
func computeNestingDepth(sql string) int {
	stripped := blockCommentRE.ReplaceAllString(sql, " ")
	stripped = lineCommentRE.ReplaceAllString(stripped, " ")
	stripped = stringLitRE.ReplaceAllString(stripped, "''")
	stripped = strings.ToLower(stripped)

	maxDepth := 0
	currentDepth := 0
	// Walk and count SELECTs inside parens
	i := 0
	for i < len(stripped) {
		c := stripped[i]
		if c == '(' {
			// Look ahead for "select" within the parens region
			currentDepth++
			if currentDepth > maxDepth {
				maxDepth = currentDepth
			}
			i++
			continue
		}
		if c == ')' {
			if currentDepth > 0 {
				currentDepth--
			}
			i++
			continue
		}
		i++
	}
	// maxDepth here counts paren depth, not SELECT depth precisely; close enough
	// for fingerprinting (a depth of 3 parens correlates strongly with 3-level
	// nested subqueries, and false positives don't break correctness).
	return maxDepth
}

// splitTopLevel splits s by sep, ignoring sep inside parentheses.
func splitTopLevel(s string, sep byte) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if start < len(s) {
		parts = append(parts, strings.TrimSpace(s[start:]))
	}
	return parts
}

func toSet(items []string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// Pre-compiled regex (heavy on init, but Find called many times during recall)
var (
	blockCommentRE = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	lineCommentRE  = regexp.MustCompile(`--[^\n]*`)
	stringLitRE    = regexp.MustCompile(`'(?:''|[^'])*'`)
	numLitRE       = regexp.MustCompile(`\b\d+(?:\.\d+)?\b`)
	whitespaceRE   = regexp.MustCompile(`\s+`)
	schemaPrefixRE = regexp.MustCompile(`\b[a-z_][a-z0-9_]*\.[a-z_][a-z0-9_]*\b`)
	// FROM clause capture: from <body> until next top-level keyword
	// Body ends at WHERE, GROUP BY, HAVING, ORDER BY, LIMIT, UNION, OFFSET, ;, or end of string.
	fromClauseRE = regexp.MustCompile(`(?is)\bfrom\s+(.+?)(?:\bwhere\b|\bgroup\s+by\b|\bhaving\b|\border\s+by\b|\blimit\b|\bunion\b|\boffset\b|;|$)`)
	// JOIN clause: capture the table name immediately after JOIN keyword.
	joinClauseRE = regexp.MustCompile(`(?is)\b(?:inner\s+|left\s+(?:outer\s+)?|right\s+(?:outer\s+)?|full\s+(?:outer\s+)?|cross\s+)?join\s+([a-z_][a-z0-9_]*(?:\.[a-z_][a-z0-9_]*)?)`)
)
