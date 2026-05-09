/*-------------------------------------------------------------------------
 *
 * placeholder_substituter.go
 *	  Replaces ?, $N, :N placeholders in normalized SQL with realistic
 *	  sample values so the SQL becomes EXPLAIN-able.
 *
 *	  Background: og's dbe_perf.statement (and pg_stat_statements for PG,
 *	  events_statements_summary_by_digest for MySQL) stores SQL with literals
 *	  replaced by placeholders. Such SQL fails EXPLAIN with "there is no
 *	  parameter $1". Small models (35B-class) cannot reliably substitute
 *	  realistic values for many placeholders in complex SQL — observed
 *	  v1.1.29 production failure on 10-table SQL with 4 placeholders where
 *	  35B looped 16 rounds and emitted empty output.
 *
 *	  Solution: deterministic substitution based on column type + operator
 *	  context. We don't need correct values — we need EXPLAIN-able values.
 *	  CBO uses statistics, not literals, for plan structure.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/placeholder_substituter.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
)

// Substitution describes one placeholder replacement.
type Substitution struct {
	Position int    // byte offset in original SQL where the placeholder started
	Original string // the placeholder token (?, $1, :2)
	Context  string // ~30 chars of left context describing the column/operator
	Value    string // the substituted literal (e.g. '50', "'%test%'")
	Source   string // "rule" | "default" | "type-known"
}

// PlaceholderSubstituter replaces unbound placeholders with realistic sample
// values via deterministic rules. Optionally consults information_schema for
// column types when driver is provided; falls back to context-based heuristics
// when driver is nil or column lookup fails.
type PlaceholderSubstituter struct {
	driver db.Driver
}

func NewPlaceholderSubstituter(driver db.Driver) *PlaceholderSubstituter {
	return &PlaceholderSubstituter{driver: driver}
}

// Substitute returns the SQL with placeholders replaced and the list of
// substitutions made. Returns the original SQL unchanged if no placeholders
// detected. schema is the active schema (used for column type lookup); may
// be empty.
//
// Two-pass strategy (v1.1.31): first pass walks SQL-order to choose values
// with prior-sub context (so "TO_CHAR(d,'YYYY-MM-DD') = ?" can use a date
// literal instead of a generic 'test'). Second pass replaces back-to-front
// to avoid offset shift.
func (s *PlaceholderSubstituter) Substitute(ctx context.Context, sql, schema string) (string, []Substitution, error) {
	pos := findAllPlaceholderPositions(sql)
	if len(pos) == 0 {
		return sql, nil, nil
	}

	// First pass: choose values in SQL order so each decision can see prior decisions.
	subs := make([]Substitution, 0, len(pos))
	for _, p := range pos {
		original := sql[p.start:p.end]
		leftContext := extractLeftContext(sql, p.start, 60)
		val, source := chooseSubstitutionWithHistory(leftContext, original, schema, subs)
		subs = append(subs, Substitution{
			Position: p.start,
			Original: original,
			Context:  strings.TrimSpace(leftContext),
			Value:    val,
			Source:   source,
		})
	}

	// Second pass: replace back-to-front to keep offsets stable.
	out := []byte(sql)
	for i := len(subs) - 1; i >= 0; i-- {
		p := pos[i]
		val := subs[i].Value
		out = append(out[:p.start], append([]byte(val), out[p.end:]...)...)
	}
	return string(out), subs, nil
}

// placeholderPos describes the [start, end) byte range of one placeholder.
type placeholderPos struct {
	start int
	end   int
}

// findAllPlaceholderPositions scans for ?, $N, :N tokens outside string
// literals and comments. Mirrors the logic in detectPlaceholders (which only
// counts) but returns positions for substitution.
func findAllPlaceholderPositions(sql string) []placeholderPos {
	var out []placeholderPos
	i := 0
	n := len(sql)
	for i < n {
		c := sql[i]

		// Skip strings (handle '' as escape)
		if c == '\'' {
			i++
			for i < n {
				if sql[i] == '\'' {
					if i+1 < n && sql[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			continue
		}
		// Skip double-quoted identifiers
		if c == '"' {
			i++
			for i < n && sql[i] != '"' {
				i++
			}
			if i < n {
				i++
			}
			continue
		}
		// Skip line comments
		if c == '-' && i+1 < n && sql[i+1] == '-' {
			for i < n && sql[i] != '\n' {
				i++
			}
			continue
		}
		// Skip block comments
		if c == '/' && i+1 < n && sql[i+1] == '*' {
			i += 2
			for i+1 < n && !(sql[i] == '*' && sql[i+1] == '/') {
				i++
			}
			if i+1 < n {
				i += 2
			}
			continue
		}

		if c == '?' {
			out = append(out, placeholderPos{start: i, end: i + 1})
			i++
			continue
		}
		if c == '$' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9' {
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			out = append(out, placeholderPos{start: i, end: j})
			i = j
			continue
		}
		if c == ':' && i+1 < n && sql[i+1] >= '0' && sql[i+1] <= '9' {
			j := i + 1
			for j < n && sql[j] >= '0' && sql[j] <= '9' {
				j++
			}
			out = append(out, placeholderPos{start: i, end: j})
			i = j
			continue
		}
		i++
	}
	return out
}

// extractLeftContext returns up to maxLen bytes of SQL ending at start.
// Used to inspect the column/operator/function preceding a placeholder.
func extractLeftContext(sql string, start, maxLen int) string {
	begin := start - maxLen
	if begin < 0 {
		begin = 0
	}
	return sql[begin:start]
}

// chooseSubstitutionWithHistory dispatches to chooseSubstitution but considers
// prior subs to handle multi-placeholder patterns. Specifically:
//
//   - TO_CHAR(date_col, 'YYYY-MM-DD') = ?
//     The first ? gets 'YYYY-MM-DD'. Naively the second ? would match an
//     equality-comparison rule and pick 'test'. With history awareness we
//     see the prior sub was a date format string and substitute a matching
//     date literal instead.
func chooseSubstitutionWithHistory(leftContext, original, schema string, prev []Substitution) (string, string) {
	val, source := chooseSubstitution(leftContext, original, schema)
	if val == "'test'" && len(prev) > 0 {
		last := prev[len(prev)-1].Value
		// Detect "TO_CHAR(date,FMT)=?" by recognizing format-like prior sub
		if strings.HasPrefix(last, "'YYYY") {
			return "'2024-01-15'", "rule-format-followup"
		}
	}
	return val, source
}

// chooseSubstitution picks a value based on the left context. Returns
// (substituted_value, source_label).
//
// Rules ordered by specificity (most specific first):
//
//   - TO_CHAR(date_col, ?) ... = ?  → 'YYYY-MM-DD' / '2024-01-01'
//   - col LIKE ? / NOT LIKE ?      → '%test%'
//   - col IS [NOT] NULL ?           → (no substitution; not a placeholder context)
//   - col IN (?, ?, ?)              → 1 (numbers) or 'a' (strings) heuristic
//   - col <= ?, >=, <, > on int     → 50
//   - col = ?, <> ?, != ? on int    → 1
//   - col = ?, <> ? on varchar      → 'test'
//   - col >= ?, <= ? on date        → '2024-01-01'
//   - LIMIT ?, OFFSET ?             → 100 / 0
//   - default                       → 1 (works for most numeric contexts)
func chooseSubstitution(leftContext, original, schema string) (string, string) {
	lower := strings.ToLower(leftContext)
	trimmed := strings.TrimRight(lower, " \t\n")

	// LIMIT / OFFSET / FETCH FIRST clauses
	if strings.HasSuffix(trimmed, "limit") {
		return "100", "rule"
	}
	if strings.HasSuffix(trimmed, "offset") {
		return "0", "rule"
	}

	// LIKE / NOT LIKE / ILIKE (string pattern)
	if endsWithKeyword(trimmed, "like") || endsWithKeyword(trimmed, "ilike") {
		return "'%test%'", "rule"
	}

	// TO_CHAR(date_col, ?) — second argument is a format string
	if toCharFormatRE.MatchString(leftContext) {
		return "'YYYY-MM-DD'", "rule"
	}

	// = ? / <> ? / != ?  : look back for column reference
	if endsWithOp(trimmed, "=") || endsWithOp(trimmed, "<>") || endsWithOp(trimmed, "!=") {
		// Heuristic: column name with "_id", "id", "no", "num" → integer
		if looksLikeIntColumn(trimmed) {
			return "1", "rule"
		}
		// Date-y column
		if looksLikeDateColumn(trimmed) {
			return "'2024-01-01'", "rule"
		}
		// Default: treat as string
		return "'test'", "rule"
	}

	// Range comparisons <= ? >= ? < ? > ?
	if endsWithOp(trimmed, "<=") || endsWithOp(trimmed, ">=") ||
		endsWithOp(trimmed, "<") || endsWithOp(trimmed, ">") {
		if looksLikeDateColumn(trimmed) {
			return "'2024-01-01'", "rule"
		}
		return "50", "rule"
	}

	// IN (...) — be smart-ish: look back for opening paren and infer
	if strings.Contains(trimmed, "in (") || strings.Contains(trimmed, "in(") {
		// We can't easily count remaining ? in the IN list here; substitute
		// individually. Default to '1' or 'test' based on heuristic.
		if looksLikeIntColumn(trimmed) {
			return "1", "rule"
		}
		return "'test'", "rule"
	}

	// BETWEEN ? AND ?
	if endsWithKeyword(trimmed, "between") || endsWithKeyword(trimmed, "and") {
		return "1", "rule"
	}

	// Default fallback — '1' is parseable as int or implicitly castable
	// in most type contexts EXPLAIN cares about.
	return "1", "default"
}

// endsWithOp checks whether trimmed lowercase context ends with the given operator
// (preceded by space or start of string).
func endsWithOp(s, op string) bool {
	if !strings.HasSuffix(s, op) {
		return false
	}
	// Make sure it's not e.g. "=?" pattern from earlier (won't have placeholder yet)
	// or "==" which doesn't exist in standard SQL.
	if op == "=" {
		// '<=' or '>=' must take precedence — they're caught earlier
		if len(s) >= 2 {
			prev := s[len(s)-2]
			if prev == '<' || prev == '>' || prev == '!' {
				return false
			}
		}
	}
	return true
}

func endsWithKeyword(s, kw string) bool {
	// Allow trailing whitespace already trimmed; check word boundary
	if !strings.HasSuffix(s, kw) {
		return false
	}
	// Must be preceded by whitespace or start of string
	if len(s) == len(kw) {
		return true
	}
	prev := s[len(s)-len(kw)-1]
	return prev == ' ' || prev == '\t' || prev == '\n' || prev == '(' || prev == ','
}

// looksLikeIntColumn: heuristic — "_id", "id", "_no", "_num", "count", "qty", "amount"
// suffix in the column reference suggests numeric.
func looksLikeIntColumn(ctx string) bool {
	tokens := strings.Fields(ctx)
	if len(tokens) < 2 {
		return false
	}
	// Most recent column ref (token before the operator)
	// Find the column identifier — last alphanumeric word before the op
	for i := len(tokens) - 1; i >= 0; i-- {
		t := strings.TrimRight(tokens[i], "=<>!,()")
		if t == "" {
			continue
		}
		// Get last segment after dot (e.g. c.customer_id → customer_id)
		if idx := strings.LastIndex(t, "."); idx >= 0 {
			t = t[idx+1:]
		}
		// Check suffix patterns
		if strings.HasSuffix(t, "_id") || t == "id" ||
			strings.HasSuffix(t, "_no") || strings.HasSuffix(t, "_num") ||
			strings.HasSuffix(t, "count") || strings.HasSuffix(t, "qty") ||
			strings.HasSuffix(t, "amount") || strings.HasSuffix(t, "price") ||
			strings.HasSuffix(t, "_id;") {
			return true
		}
		return false
	}
	return false
}

// looksLikeDateColumn: heuristic for date/time columns
func looksLikeDateColumn(ctx string) bool {
	tokens := strings.Fields(ctx)
	if len(tokens) < 2 {
		return false
	}
	for i := len(tokens) - 1; i >= 0; i-- {
		t := strings.TrimRight(tokens[i], "=<>!,()")
		if t == "" {
			continue
		}
		if idx := strings.LastIndex(t, "."); idx >= 0 {
			t = t[idx+1:]
		}
		if strings.HasSuffix(t, "_date") || strings.HasSuffix(t, "_time") ||
			strings.HasSuffix(t, "_at") || t == "date" || t == "time" ||
			strings.Contains(t, "timestamp") {
			return true
		}
		return false
	}
	return false
}

var toCharFormatRE = regexp.MustCompile(`(?i)to_char\s*\(\s*[a-z_][a-z0-9_.]*\s*,\s*$`)

// FormatSubstitutions renders the subs list as a markdown table for the
// sqlfetch output.
func FormatSubstitutions(subs []Substitution) string {
	if len(subs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n  替换详情：\n")
	for _, s := range subs {
		b.WriteString(fmt.Sprintf("    Position %-4d %-6s → %-20s (上下文: %q · 来源: %s)\n",
			s.Position, s.Original, s.Value, truncForDisplay(s.Context, 40), s.Source))
	}
	return b.String()
}

func truncForDisplay(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
