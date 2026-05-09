/*-------------------------------------------------------------------------
 *
 * classifier.go
 *	  InputType classifies the type of user input.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dispatch/classifier.go
 *
 *-------------------------------------------------------------------------
 */
package dispatch

import (
	"regexp"
	"strings"
)

// InputType classifies the type of user input.
type InputType uint8

const (
	// InputEmpty represents empty or whitespace-only input.
	InputEmpty InputType = iota
	// InputSlashCommand represents a slash command (e.g., /help, /slowsql 5000).
	InputSlashCommand
	// InputSQL represents a SQL statement.
	InputSQL
	// InputNaturalLanguage represents free-form natural language input.
	InputNaturalLanguage
)

// ── Phase 1: Unambiguous SQL first-words ─────────────────────────
// These words NEVER start a natural language sentence in DBA context.
var unambiguousSQL = map[string]struct{}{
	// DML
	"SELECT": {}, "INSERT": {}, "DELETE": {}, "MERGE": {},
	// DDL
	"CREATE": {}, "DROP": {}, "ALTER": {}, "TRUNCATE": {},
	// DCL
	"GRANT": {}, "REVOKE": {},
	// TCL
	"COMMIT": {}, "ROLLBACK": {}, "SAVEPOINT": {},
	// Oracle-specific
	"FLASHBACK": {}, "PURGE": {}, "NOAUDIT": {}, "ASSOCIATE": {}, "DISASSOCIATE": {},
	// PG-specific
	"VACUUM": {}, "REINDEX": {}, "CLUSTER": {}, "UNLISTEN": {},
	"DISCARD": {}, "REASSIGN": {},
	// MySQL-specific
	"REPLACE": {}, "CHECKSUM": {}, "UNINSTALL": {},
	// Cross-DB
	"DEALLOCATE": {}, "DECLARE": {}, "XA": {}, "HANDLER": {},
	// OpenGauss-specific
	"TIMECAPSULE": {}, "SNAPSHOT": {}, "PREDICT": {},
}

// ── Phase 2: Ambiguous first-words + second-word disambiguation ──
// These words can start both SQL and natural language.

// ambiguousSQL maps ambiguous first-words to their SQL-confirming second-words.
var ambiguousSQL = map[string]map[string]struct{}{
	"SHOW": toSet("TABLES", "TABLE", "DATABASES", "DATABASE", "PROCESSLIST",
		"CREATE", "COLUMNS", "INDEX", "INDEXES", "STATUS", "VARIABLES",
		"GRANTS", "WARNINGS", "ERRORS", "PARAMETER", "PARAMETERS",
		"SGA", "PGA", "SPFILE", "ALL", "MASTER", "SLAVE", "REPLICA",
		"BINARY", "PLUGINS", "ENGINES", "ENGINE", "CHARSET", "COLLATION",
		"PROFILES", "TRIGGERS", "EVENTS", "FUNCTION", "PROCEDURE",
		"OPEN", "FULL", "GLOBAL", "SESSION", "STORAGE", "SCHEMAS",
		"RECYCLEBIN", "CON_ID", "CON_NAME", "RELEASE", "SPPARAMETER",
		"INSTANCE", "USER", "TABLESPACE", "TABLESPACES", "PDBS"),
	"SET": toSet("ROLE", "TRANSACTION", "SESSION", "GLOBAL", "LOCAL",
		"NAMES", "CHARACTER", "AUTOCOMMIT", "TIME_ZONE", "SCHEMA",
		"SEARCH_PATH", "CONSTRAINTS", "DATESTYLE"),
	"UPDATE": toSet(), // UPDATE is almost always SQL; listed here for completeness
	"CHECK":  toSet("TABLE", "TABLES"),
	"LOCK":   toSet("TABLE", "TABLES"),
	"UNLOCK": toSet("TABLE", "TABLES"),
	"ANALYZE": toSet("TABLE", "VERBOSE", "LOCAL", "NO_WRITE_TO_BINLOG"),
	"OPTIMIZE": toSet("TABLE", "LOCAL", "NO_WRITE_TO_BINLOG"),
	"LOAD":    toSet("DATA"),
	"KILL":    toSet("QUERY", "CONNECTION"),
	"RENAME":  toSet("TABLE"),
	"STOP":    toSet("SLAVE", "GROUP_REPLICATION"),
	"CHANGE":  toSet("MASTER", "REPLICATION"),
	"RESET":   toSet("MASTER", "SLAVE", "QUERY"),
	"FLUSH":   toSet("PRIVILEGES", "TABLES", "STATUS", "LOGS", "HOSTS", "BINARY"),
	"START":   toSet("TRANSACTION", "SLAVE", "GROUP_REPLICATION"),
	"INSTALL": toSet("PLUGIN"),
	"CACHE":   toSet("INDEX"),
	"REPAIR":  toSet("TABLE"),
	"CALL":    {}, // special: checked via pattern (identifier + parenthesis)
	"EXECUTE": toSet("IMMEDIATE", "DIRECT"),
	"EXEC":    {},
	"USE":     {}, // special: bare identifier without article
	"DO":      {}, // special: $$ or function call
	"EXPLAIN": toSet("SELECT", "INSERT", "UPDATE", "DELETE", "WITH",
		"MERGE", "PLAN", "ANALYZE", "VERBOSE", "FORMAT"),
	"DESCRIBE": {},
	"DESC":     {},
	"WITH":     {}, // special: identifier + AS
	"BEGIN":    {}, // special: standalone or + TRANSACTION/WORK
	"END":      {}, // special: standalone or + TRANSACTION/WORK
	"ABORT":    {}, // special: standalone
	"COMMENT":  toSet("ON"),
	"AUDIT":    toSet("SELECT", "INSERT", "UPDATE", "DELETE", "ALL", "POLICY"),
	"LISTEN":   {}, // PG: LISTEN channel
	"NOTIFY":   {}, // PG: NOTIFY channel
	"COPY":     {}, // PG: COPY table FROM/TO
	"FETCH":    toSet("FIRST", "NEXT", "ALL", "FORWARD", "BACKWARD", "ABSOLUTE", "RELATIVE", "PRIOR"),
	"MOVE":     toSet("FIRST", "NEXT", "ALL", "FORWARD", "BACKWARD", "ABSOLUTE", "RELATIVE"),
	"CLOSE":    {}, // PG: CLOSE cursor_name
	"PREPARE":  {}, // special: PREPARE name AS/FROM
	"REFRESH":  toSet("MATERIALIZED"),
	"IMPORT":   toSet("FOREIGN"),
	"SECURITY": toSet("LABEL"),
	"TABLE":    {}, // PG: TABLE t (shorthand)
	"VALUES":   {}, // PG: VALUES (1,2)
}

// nlIndicatorWords — if the second word is one of these, the input is
// definitely natural language, not SQL.
var nlIndicatorWords = map[string]struct{}{
	"THE": {}, "A": {}, "AN": {}, "THIS": {}, "THAT": {},
	"MY": {}, "OUR": {}, "YOUR": {}, "THEIR": {}, "ITS": {},
	"ME": {}, "US": {}, "HIM": {}, "HER": {}, "THEM": {},
	"HOW": {}, "WHY": {}, "WHAT": {}, "WHEN": {}, "WHERE": {},
	"WHICH": {}, "WHO": {}, "WHOM": {},
	"IF": {}, "WHETHER": {}, "ABOUT": {}, "UP": {},
	"IS": {}, "ARE": {}, "WAS": {}, "WERE": {},
	"IT": {}, "SOME": {}, "ANY": {}, "ALL": {},
	"SOMETHING": {}, "EVERYTHING": {},
}

// ── Phase 3: SQL combination pattern scan ────────────────────────
var sqlPatterns []*regexp.Regexp

func init() {
	patterns := []string{
		`(?i)\bSELECT\b.+\bFROM\b`,
		`(?i)\bINSERT\s+INTO\b`,
		`(?i)\bUPDATE\b\s+\S+\s+SET\b`,
		`(?i)\bDELETE\s+FROM\b`,
		`(?i)\bMERGE\s+INTO\b`,
		`(?i)\bCREATE\s+(OR\s+REPLACE\s+)?(TABLE|INDEX|VIEW|SEQUENCE|TRIGGER|FUNCTION|PROCEDURE|PACKAGE|TYPE|SCHEMA|DATABASE|TABLESPACE|USER|ROLE|SYNONYM|MATERIALIZED)\b`,
		`(?i)(LEFT|RIGHT|FULL|INNER|OUTER|CROSS)\s+JOIN\b`,
		`(?i)\bJOIN\s+\S+\s+ON\b`,
		`(?i)\bGROUP\s+BY\b`,
		`(?i)\bORDER\s+BY\b`,
		`(?i)\bUNION(\s+ALL)?\b`,
		`(?i)\bINTERSECT\b`,
		`(?i)\b(EXCEPT|MINUS)\b`,
		`(?i)\bWHERE\s+\S+\s*(=|<|>|!=|<>|LIKE\b|IN\s*\(|BETWEEN\b|IS\s+(NOT\s+)?NULL)`,
		`(?i)\bFOR\s+UPDATE\b`,
		`(?i)\bCONNECT\s+BY\b`,
		`(?i)\bPARTITION\s+BY\b`,
		`(?i)\bFETCH\s+FIRST\b`,
		`(?i)\bLIMIT\s+\d+`,
	}
	sqlPatterns = make([]*regexp.Regexp, len(patterns))
	for i, p := range patterns {
		sqlPatterns[i] = regexp.MustCompile(p)
	}
}

// ── ClassifyInput ────────────────────────────────────────────────

// ClassifyInput determines the type of user input using a three-phase classifier:
//
//	Phase 0: Hybrid override — Chinese chars present → natural language
//	Phase 1: Unambiguous SQL first-word (map lookup)
//	Phase 2: Ambiguous first-word + second-word disambiguation
//	Phase 3: SQL combination pattern scan (regex)
func ClassifyInput(input string) InputType {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return InputEmpty
	}

	if strings.HasPrefix(trimmed, "/") {
		return InputSlashCommand
	}

	// Phase 0: hybrid SQL + Chinese natural language → route to /llm.
	// Real SQL keywords/identifiers don't contain Chinese characters. If the
	// input has Chinese chars anywhere (≥3 to allow occasional table comment),
	// the user is asking a natural-language question that may also contain a
	// SQL snippet — this should go through LLM (which can read both).
	// Without this, "WITH ... AS (...) sql 有没有优化空间" was misrouted to
	// /sql and surfaced as a syntax error.
	if hasChineseChars(trimmed, 3) {
		return InputNaturalLanguage
	}

	firstWord := extractFirstWord(trimmed)
	upperFirst := strings.ToUpper(firstWord)

	// Phase 1: unambiguous SQL keyword.
	if _, ok := unambiguousSQL[upperFirst]; ok {
		return InputSQL
	}

	// Phase 2: ambiguous keyword — check second word.
	if sqlSeconds, isAmbiguous := ambiguousSQL[upperFirst]; isAmbiguous {
		secondWord := extractSecondWord(trimmed)
		upperSecond := strings.ToUpper(secondWord)

		// NL indicator: articles, pronouns, question words → natural language.
		if _, isNL := nlIndicatorWords[upperSecond]; isNL {
			return InputNaturalLanguage
		}

		// SQL-confirming second word for this keyword.
		if len(sqlSeconds) > 0 {
			if _, ok := sqlSeconds[upperSecond]; ok {
				return InputSQL
			}
		}

		// Special patterns for specific ambiguous words.
		if classifyAmbiguousSpecial(upperFirst, upperSecond, trimmed) {
			return InputSQL
		}

		// UPDATE is almost always SQL — bias toward SQL.
		if upperFirst == "UPDATE" {
			return InputSQL
		}

		// Ambiguous word but unrecognized second word → fall through to Phase 3.
	}

	// Phase 3: SQL combination pattern scan.
	matches := 0
	for _, pat := range sqlPatterns {
		if pat.MatchString(trimmed) {
			matches++
			if matches >= 1 {
				return InputSQL
			}
		}
	}

	return InputNaturalLanguage
}

// classifyAmbiguousSpecial handles special disambiguation patterns for
// ambiguous first-words that can't be resolved by a simple second-word lookup.
func classifyAmbiguousSpecial(firstWord, secondWord, input string) bool {
	switch firstWord {
	case "SET":
		// SET @var=1, SET @@global.var → SQL
		if len(input) > 4 && (input[4] == '@' || strings.ContainsRune(input, '=')) {
			return true
		}
	case "CALL":
		// CALL schema.proc() or CALL proc() → SQL
		if strings.Contains(input, "(") {
			return true
		}
	case "EXEC", "EXECUTE":
		// EXEC proc or EXECUTE proc → SQL (bare identifier, no article)
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "DO":
		// DO $$ ... $$ (PG) or DO function() → SQL
		if strings.Contains(input, "$$") || strings.Contains(input, "(") {
			return true
		}
	case "SHOW":
		// SHOW followed by any non-NL word is SQL — even typos like "show tale"
		// should go to the database for native error, not LLM.
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "EXPLAIN", "DESCRIBE", "DESC":
		// If second word is not an NL indicator, it's likely SQL (table name or SQL keyword).
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "WITH":
		// WITH identifier AS → SQL (CTE)
		upper := strings.ToUpper(input)
		if strings.Contains(upper, " AS ") || strings.Contains(upper, " AS(") {
			return true
		}
	case "BEGIN", "ABORT", "END":
		// Standalone (single word) or + TRANSACTION/WORK/; → SQL
		rest := strings.TrimSpace(input[len(firstWord):])
		if rest == "" || rest == ";" ||
			strings.HasPrefix(strings.ToUpper(rest), "TRANSACTION") ||
			strings.HasPrefix(strings.ToUpper(rest), "WORK") {
			return true
		}
	case "USE":
		// USE database_name → SQL (bare identifier, no article)
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "KILL":
		// KILL 1234 → SQL (second word is a number)
		if secondWord != "" && isNumeric(secondWord) {
			return true
		}
	case "LISTEN", "NOTIFY", "UNLISTEN":
		// PG: LISTEN/NOTIFY/UNLISTEN channel → SQL (bare identifier)
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "COPY":
		// COPY table FROM/TO → SQL
		upper := strings.ToUpper(input)
		if strings.Contains(upper, " FROM ") || strings.Contains(upper, " TO ") {
			return true
		}
	case "CLOSE":
		// CLOSE cursor_name → SQL (bare identifier)
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "PREPARE":
		// PREPARE name AS/FROM → SQL
		upper := strings.ToUpper(input)
		if strings.Contains(upper, " AS ") || strings.Contains(upper, " FROM ") {
			return true
		}
	case "TABLE":
		// PG: TABLE t → SQL (bare identifier, no article/verb)
		if secondWord != "" {
			if _, isNL := nlIndicatorWords[secondWord]; !isNL {
				return true
			}
		}
	case "VALUES":
		// VALUES (...) → SQL
		if strings.Contains(input, "(") {
			return true
		}
	}
	return false
}

// ── Helpers ──────────────────────────────────────────────────────

// extractFirstWord returns the first whitespace-or-semicolon-delimited word from the input.
func extractFirstWord(s string) string {
	for i, ch := range s {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == ';' {
			return s[:i]
		}
	}
	return s
}

// extractSecondWord returns the second whitespace-delimited word from the input.
func extractSecondWord(s string) string {
	// Skip first word.
	i := 0
	for i < len(s) && s[i] != ' ' && s[i] != '\t' {
		i++
	}
	// Skip whitespace.
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	if i >= len(s) {
		return ""
	}
	// Extract second word.
	start := i
	for i < len(s) && s[i] != ' ' && s[i] != '\t' && s[i] != '\n' && s[i] != '\r' && s[i] != '(' && s[i] != ';' {
		i++
	}
	return s[start:i]
}

// toSet converts a variadic list of strings into a set (map).
func toSet(words ...string) map[string]struct{} {
	m := make(map[string]struct{}, len(words))
	for _, w := range words {
		m[w] = struct{}{}
	}
	return m
}

// hasChineseChars returns true if s contains at least minCount Chinese (CJK) characters.
// Used to detect SQL+NL hybrid inputs that should route to /llm instead of /sql.
func hasChineseChars(s string, minCount int) bool {
	count := 0
	for _, r := range s {
		// CJK Unified Ideographs: 0x4E00-0x9FFF
		// CJK Extension A: 0x3400-0x4DBF
		// Common Chinese punctuation: 0x3000-0x303F (，。、？！…—)
		if (r >= 0x4E00 && r <= 0x9FFF) ||
			(r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x3000 && r <= 0x303F) {
			count++
			if count >= minCount {
				return true
			}
		}
	}
	return false
}

// isNumeric returns true if s consists entirely of digits.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
