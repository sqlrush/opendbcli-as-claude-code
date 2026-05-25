/*-------------------------------------------------------------------------
 *
 * parser_helpers.go
 *	  Small text-extraction helpers used by parser.go. Kept separate so
 *	  parser.go is just the section-by-section parsing logic and these
 *	  general utilities can be unit-tested in isolation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/parser_helpers.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// extractSection finds a section by its title (any of the alternatives) and
// returns up to maxLines lines following the title. Stops at the next "##"
// or "──" or blank-block (3+ consecutive newlines) which typically separates
// WDR sections.
//
// v1.1.49: iterates through ALL matches (not just first). og 5.0.3 HTML has
// the same title repeated as a TOC link AND as the section heading; we want
// the heading. Heuristic: pick the match whose following window contains a
// pipe-delimited data row (`|`) — the TOC links have no data rows after.
func extractSection(text string, titles []string, maxLines int) string {
	patterns := make([]string, 0, len(titles))
	for _, t := range titles {
		patterns = append(patterns, regexp.QuoteMeta(t))
	}
	titleRE := regexp.MustCompile("(?i)(" + strings.Join(patterns, "|") + ")")

	matches := titleRE.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return ""
	}

	extractAt := func(start int) string {
		rest := text[start:]
		lines := strings.Split(rest, "\n")
		if maxLines > 0 && len(lines) > maxLines {
			lines = lines[:maxLines]
		}
		cut := len(lines)
		consecutiveBlank := 0
		sawContent := false // don't terminate on blank-run before the section body even started
		for i, l := range lines {
			trimmed := strings.TrimSpace(l)
			if i > 0 && (strings.HasPrefix(trimmed, "##") || strings.HasPrefix(trimmed, "──")) {
				cut = i
				break
			}
			if trimmed == "" {
				consecutiveBlank++
				if sawContent && consecutiveBlank >= 3 {
					cut = i - 2
					break
				}
			} else {
				consecutiveBlank = 0
				sawContent = true
			}
		}
		if cut < 0 {
			cut = 0
		}
		return strings.Join(lines[:cut], "\n")
	}

	// Try each match; first one whose body contains a `|` data row wins.
	// If none qualify (text WDR has `:` separators not `|`), fall back to
	// the first match — legacy text format never had this ambiguity.
	for _, loc := range matches {
		section := extractAt(loc[1])
		if containsDataRow(section) {
			return section
		}
	}
	return extractAt(matches[0][1])
}

// containsDataRow returns true if the section text has at least one line
// that looks like a pipe-delimited data row (3+ `|` separators). Used to
// distinguish a real section body from a TOC link entry.
func containsDataRow(section string) bool {
	for _, l := range strings.Split(section, "\n") {
		if strings.Count(l, "|") >= 3 {
			return true
		}
	}
	return false
}

// splitTableRow splits a row on common WDR delimiters: "|" (HTML table cells
// after htmlToText) or multiple-space columns (text format). Trims each cell.
func splitTableRow(line string) []string {
	if strings.Contains(line, "|") {
		parts := strings.Split(line, "|")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			t := strings.TrimSpace(p)
			if t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	// Fallback: split on runs of 2+ spaces (text-format columns)
	return multiSpaceRE.Split(strings.TrimSpace(line), -1)
}

var multiSpaceRE = regexp.MustCompile(`\s{2,}`)

// isHeaderRow returns true if the row looks like a header row (all alphabetic
// names) or a divider row (mostly dashes / equals).
func isHeaderRow(fields []string) bool {
	if len(fields) == 0 {
		return true
	}
	// Divider check: any field is mostly dashes
	for _, f := range fields {
		if len(f) >= 3 {
			dashCount := strings.Count(f, "-") + strings.Count(f, "=")
			if dashCount*2 > len(f) {
				return true
			}
		}
	}
	// Header check: no field has digits
	hasDigit := false
	for _, f := range fields {
		for _, c := range f {
			if c >= '0' && c <= '9' {
				hasDigit = true
				break
			}
		}
		if hasDigit {
			break
		}
	}
	return !hasDigit
}

// extractFloatNamed finds "Name: 123.45" or "Name | 123.45" patterns and
// returns the float. Returns 0 if not found.
func extractFloatNamed(text string, names []string) float64 {
	for _, name := range names {
		// "Name: 123.45" / "Name = 123.45" / "Name | 123.45"
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(name) + `\s*[:=|]\s*([\d.,]+)`)
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return parseFloat(m[1])
		}
	}
	return 0
}

// extractIntNamed is the integer variant.
func extractIntNamed(text string, names []string) int64 {
	for _, name := range names {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(name) + `\s*[:=|]\s*([\d,]+)`)
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return parseInt(m[1])
		}
	}
	return 0
}

// extractStringNamed returns the value as-is (trimmed).
func extractStringNamed(text string, names []string) string {
	for _, name := range names {
		re := regexp.MustCompile(`(?i)` + regexp.QuoteMeta(name) + `\s*[:=|]\s*([^\n|]+)`)
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// parseFloat is a forgiving wrapper around strconv: strips commas, ignores
// trailing units ("ms", "%", "MB"), returns 0 on error.
func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	s = strings.ReplaceAll(s, ",", "")
	// Strip trailing units like "ms", "MB", "s"
	for _, suffix := range []string{"ms", "MB", "GB", "KB", "us", "ns"} {
		s = strings.TrimSuffix(s, suffix)
	}
	s = strings.TrimSpace(s)
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseInt is the int variant of parseFloat (truncates).
func parseInt(s string) int64 {
	f := parseFloat(s)
	return int64(f)
}

// parsePercent treats "45.2%" / "45.2" / "0.452" as percent (0-100).
func parsePercent(s string) float64 {
	v := parseFloat(s)
	if v > 0 && v < 1 && !strings.Contains(s, "%") {
		// 0.452 → treat as fraction
		return v * 100
	}
	return v
}

// findSQLIDInRow scans fields for one that looks like a SQL_ID (8-20 digits,
// or hex string of that length). Returns the first match.
func findSQLIDInRow(fields []string) string {
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if isSQLID(f) {
			return f
		}
	}
	return ""
}

func isSQLID(s string) bool {
	if len(s) < 8 || len(s) > 24 {
		return false
	}
	allDigit := true
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
		if !(c >= '0' && c <= '9') {
			allDigit = false
		}
	}
	// Bare digits (most common) or hex
	_ = allDigit
	return true
}

// detectOGColumnMap looks for an og 5.0.3 "SQL ordered by ..." header row
// inside a section and returns a map[normalized_label]column_index. Returns
// nil if no recognizable og header is found (caller falls back to heuristic).
//
// Trigger: a row that contains both "Unique SQL Id" and "Total Elapse Time"
// — uniquely identifies og's WDR table header.
func detectOGColumnMap(section string) map[string]int {
	for _, line := range strings.Split(section, "\n") {
		if !strings.Contains(line, "Unique SQL Id") || !strings.Contains(line, "Total Elapse Time") {
			continue
		}
		cells := splitTableRow(line)
		m := make(map[string]int, len(cells))
		for i, c := range cells {
			key := normalizeColumnLabel(c)
			if key == "" {
				continue
			}
			if _, dup := m[key]; !dup {
				m[key] = i
			}
		}
		return m
	}
	return nil
}

// normalizeColumnLabel lowercases + strips units / whitespace so "Total Elapse
// Time(us)" and "total_elapse_time" both map to "total_elapse_time".
func normalizeColumnLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Strip parenthesized unit: "Total Elapse Time(us)" → "Total Elapse Time"
	if idx := strings.Index(s, "("); idx > 0 {
		s = s[:idx]
	}
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "_")
	return s
}

// fillTopSQLFieldsByColumn populates an entry using a pre-built column map
// from og's table header. og emits microseconds for time fields — converted
// to ms here (divide by 1000). Missing columns leave fields zero-valued.
func fillTopSQLFieldsByColumn(ent *TopSQLEntry, fields []string, col map[string]int) {
	get := func(label string) (string, bool) {
		idx, ok := col[label]
		if !ok || idx >= len(fields) {
			return "", false
		}
		return strings.TrimSpace(fields[idx]), true
	}
	getFloat := func(label string) float64 {
		v, _ := get(label)
		return parseFloat(v)
	}
	getInt := func(label string) int64 {
		v, _ := get(label)
		return parseInt(v)
	}

	// og microseconds → ms: divide by 1000
	if ent.TotalTimeMS == 0 {
		if us := getFloat("total_elapse_time"); us > 0 {
			ent.TotalTimeMS = us / 1000.0
		}
	}
	if ent.AvgTimeMS == 0 {
		if us := getFloat("avg_elapse_time"); us > 0 {
			ent.AvgTimeMS = us / 1000.0
		}
	}
	if ent.Calls == 0 {
		ent.Calls = getInt("calls")
	}
	if ent.RowsReturned == 0 {
		ent.RowsReturned = getInt("returned_rows")
	}
	if ent.UserName == "" {
		if v, ok := get("user_name"); ok {
			ent.UserName = v
		}
	}
	if ent.TotalIO == 0 {
		// og: Logical Read + Physical Read (block counts, no unit conversion)
		ent.TotalIO = getInt("logical_read") + getInt("physical_read")
	}
	if ent.QueryPrefix == "" {
		if v, ok := get("sql_text"); ok && v != "" {
			if len(v) > 200 {
				v = v[:200] + "..."
			}
			ent.QueryPrefix = v
		}
	}
}

// fillTopSQLFields populates an entry from a parsed table row. Different
// "Top SQL by ..." views have different column orders; this is heuristic.
// Common column patterns:
//   sql_id | calls | total_time | avg_time | rows | query_prefix
//   sql_id | calls | total_io   | avg_io   | rows | query_prefix
func fillTopSQLFields(ent *TopSQLEntry, fields []string) {
	for _, f := range fields {
		f = strings.TrimSpace(f)
		// Numeric fields heuristic: int → calls/rows/io, float w/ ms → time
		if ent.Calls == 0 && isAllDigits(f) && len(f) <= 12 {
			ent.Calls = parseInt(f)
			continue
		}
		if (strings.HasSuffix(f, "ms") || strings.Contains(f, ".")) && ent.AvgTimeMS == 0 {
			v := parseFloat(f)
			if v > 0 && ent.TotalTimeMS == 0 {
				ent.TotalTimeMS = v
			} else if v > 0 {
				ent.AvgTimeMS = v
			}
			continue
		}
		// Query prefix is the longest text field that's not a number
		if len(f) > 30 && !isAllDigits(f) {
			if len(f) > len(ent.QueryPrefix) {
				ent.QueryPrefix = f
				if len(ent.QueryPrefix) > 200 {
					ent.QueryPrefix = ent.QueryPrefix[:200] + "..."
				}
			}
		}
	}
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// appendUnique appends s to slice if not already present.
func appendUnique(slice []string, s string) []string {
	for _, v := range slice {
		if v == s {
			return slice
		}
	}
	return append(slice, s)
}

// sortTopSQLsByTotal sorts entries by TotalTimeMS desc.
func sortTopSQLsByTotal(entries []TopSQLEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].TotalTimeMS > entries[j].TotalTimeMS
	})
}
