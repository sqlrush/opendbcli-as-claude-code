/*-------------------------------------------------------------------------
 *
 * sqlguard.go
 *	  ClassifySQL classifies a SQL statement's security level. Uses a
 *	  CONSERVATIVE strategy: unknown or unparseable statements are
 *	  classified as LevelDangerous.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/sqlguard.go
 *
 *-------------------------------------------------------------------------
 */
package security

import (
	"regexp"
	"strings"
)

// SQL classification patterns.
// Order matters: more specific patterns must be checked before generic ones.

// dangerousPatterns match statements that are always classified as LevelDangerous.
var dangerousPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*DROP\s+`),
	regexp.MustCompile(`(?i)^\s*TRUNCATE\s+`),
	regexp.MustCompile(`(?i)^\s*DELETE\s+`),
	regexp.MustCompile(`(?i)^\s*PURGE\s+`),
}

// adminPatterns match statements classified as LevelAdmin.
var adminPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*INSERT\s+`),
	regexp.MustCompile(`(?i)^\s*UPDATE\s+`),
	regexp.MustCompile(`(?i)^\s*CREATE\s+TABLE\s+`),
	regexp.MustCompile(`(?i)^\s*ALTER\s+TABLE\s+`),
	regexp.MustCompile(`(?i)^\s*CREATE\s+INDEX\s+`),
	regexp.MustCompile(`(?i)^\s*GRANT\s+`),
	regexp.MustCompile(`(?i)^\s*REVOKE\s+`),
	regexp.MustCompile(`(?i)^\s*MERGE\s+`),
}

// operatorPatterns match statements classified as LevelOperator.
var operatorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*ALTER\s+SYSTEM\s+`),
	regexp.MustCompile(`(?i)^\s*ALTER\s+SESSION\s+`),
	regexp.MustCompile(`(?i)^\s*ANALYZE\s+`),
	regexp.MustCompile(`(?i)^\s*CREATE\s+INDEX\s+`),
}

// readOnlyPatterns match statements classified as LevelReadOnly.
var readOnlyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\s*SELECT\s+`),
	regexp.MustCompile(`(?i)^\s*SHOW\s+`),
	regexp.MustCompile(`(?i)^\s*DESCRIBE\s+`),
	regexp.MustCompile(`(?i)^\s*DESC\s+`),
	regexp.MustCompile(`(?i)^\s*EXPLAIN\s+`),
}

// commentPattern matches SQL comments (both -- and /* */ styles).
var (
	blockCommentPattern = regexp.MustCompile(`/\*[\s\S]*?\*/`)
	lineCommentPattern  = regexp.MustCompile(`--[^\n]*(\n|$)`)
)

// withCTEPattern matches WITH ... AS (...) prefix to extract the trailing statement.
var withCTEPattern = regexp.MustCompile(`(?i)^\s*WITH\s+.+\)\s*`)

// plsqlPattern matches PL/SQL blocks (BEGIN ... END;).
var plsqlPattern = regexp.MustCompile(`(?i)^\s*BEGIN\s+`)

// ClassifySQL classifies a SQL statement's security level.
// Uses a CONSERVATIVE strategy: unknown or unparseable statements are classified as LevelDangerous.
func ClassifySQL(sql string) Level {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return LevelDangerous
	}

	// Handle multiple statements: split on semicolons and take the highest level.
	if containsMultipleStatements(sql) {
		return classifyMultipleStatements(sql)
	}

	return classifySingleStatement(sql)
}

// classifySingleStatement classifies a single SQL statement.
func classifySingleStatement(sql string) Level {
	// Strip comments to reveal the actual command.
	stripped := stripComments(sql)
	stripped = strings.TrimSpace(stripped)

	if stripped == "" {
		return LevelDangerous
	}

	// PL/SQL blocks are always dangerous (may contain EXECUTE IMMEDIATE, etc.)
	if plsqlPattern.MatchString(stripped) {
		return LevelDangerous
	}

	// Handle WITH CTE: classify the statement after the CTE definition.
	if afterCTE, ok := extractAfterCTE(stripped); ok {
		return classifyBareStatement(afterCTE)
	}

	return classifyBareStatement(stripped)
}

// classifyBareStatement classifies a statement that has been stripped of comments and CTE prefixes.
func classifyBareStatement(sql string) Level {
	// Check dangerous patterns first (highest priority).
	for _, p := range dangerousPatterns {
		if p.MatchString(sql) {
			return LevelDangerous
		}
	}

	// Check operator patterns before admin (ALTER SYSTEM/SESSION before ALTER TABLE).
	for _, p := range operatorPatterns {
		if p.MatchString(sql) {
			return LevelOperator
		}
	}

	// Check admin patterns.
	for _, p := range adminPatterns {
		if p.MatchString(sql) {
			return LevelAdmin
		}
	}

	// Check read-only patterns.
	for _, p := range readOnlyPatterns {
		if p.MatchString(sql) {
			return LevelReadOnly
		}
	}

	// CONSERVATIVE: unknown statements default to dangerous.
	return LevelDangerous
}

// stripComments removes SQL comments from a statement.
func stripComments(sql string) string {
	result := blockCommentPattern.ReplaceAllString(sql, " ")
	result = lineCommentPattern.ReplaceAllString(result, " ")
	return result
}

// extractAfterCTE extracts the statement after the WITH CTE clause.
// Returns the trailing statement and true if a CTE was found, or empty and false otherwise.
func extractAfterCTE(sql string) (string, bool) {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	if !strings.HasPrefix(upper, "WITH") {
		return "", false
	}

	// Find the last closing paren that ends the CTE, then get the trailing statement.
	depth := 0
	inCTE := false
	for i, ch := range sql {
		if ch == '(' {
			depth++
			inCTE = true
		} else if ch == ')' {
			depth--
			if inCTE && depth == 0 {
				rest := strings.TrimSpace(sql[i+1:])
				if rest != "" {
					return rest, true
				}
				return "", true
			}
		}
	}

	// Malformed CTE — conservative default.
	return "", false
}

// containsMultipleStatements checks if the SQL contains multiple semicolon-separated statements.
func containsMultipleStatements(sql string) bool {
	// Strip comments first to avoid false positives from semicolons in comments.
	stripped := stripComments(sql)

	// PL/SQL blocks contain semicolons but should be treated as single statements.
	trimmed := strings.TrimSpace(stripped)
	if plsqlPattern.MatchString(trimmed) {
		return false
	}

	parts := splitStatements(stripped)
	return len(parts) > 1
}

// splitStatements splits SQL on semicolons, ignoring empty segments.
func splitStatements(sql string) []string {
	raw := strings.Split(sql, ";")
	var result []string
	for _, part := range raw {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// classifyMultipleStatements classifies multiple semicolon-separated SQL statements
// and returns the highest (most dangerous) level found.
func classifyMultipleStatements(sql string) Level {
	stripped := stripComments(sql)
	parts := splitStatements(stripped)

	highest := LevelReadOnly
	for _, part := range parts {
		level := classifySingleStatement(part)
		if level > highest {
			highest = level
		}
	}
	return highest
}
