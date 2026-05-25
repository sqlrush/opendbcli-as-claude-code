/*-------------------------------------------------------------------------
 *
 * section_extractor.go
 *	  v1.1.51: pulls labeled sections out of an og 5.0.3 generate_wdr_report
 *	  HTML by their anchor id, returning each section as htmlToText'd flat
 *	  text. The evaluator then runs deterministic rules per section, and
 *	  the synthesizer ships the raw sections to the LLM for analysis.
 *
 *	  og's HTML uses anchors like:
 *	    <h3 ... id="Database_Stat" ...>-Database Stat</h3>
 *	    <h3 ... id="Load_Profile" ...>-Load Profile</h3>
 *	  Each section ends right before the next anchor (or EOF).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/section_extractor.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"regexp"
	"strings"
)

// SectionKey is a normalized identifier for a WDR section that downstream
// code (evaluator, synthesizer) references. Stable across og versions even
// if anchor IDs drift.
const (
	SectionDatabaseStat       = "database_stat"
	SectionLoadProfile        = "load_profile"
	SectionInstanceEfficiency = "instance_efficiency"
	SectionIOProfile          = "io_profile"
	SectionCacheIOStats       = "cache_io_stats"
	SectionUserTables         = "user_tables_stats"
	SectionUserIndexes        = "user_index_stats"
	SectionTopSQLByElapsed    = "top_sql_by_elapsed"
)

// sectionAnchorMap maps anchor IDs (or substrings) found in og HTML to the
// normalized SectionKey. Looked up in order — first match wins.
//
// og 5.0.3 anchor names observed in /tmp/wdr_report.html:
//   id="Database_Stat"
//   id="Load_Profile"
//   id="Instance_Efficiency_Percentages_(Target_100%)"  -- has parens
//   id="IO_Profile"
//   id="User_Tables_stats"
//   id="User_Index_stats"
//   id="SQL_ordered_by_Elapsed_Time"
//   href="#Cache IO Stats"  -- space in anchor (h2 link)
var sectionAnchorMap = []struct {
	contains string // substring to find in raw HTML
	key      string
}{
	{`id="Database_Stat"`, SectionDatabaseStat},
	{`id="Load_Profile"`, SectionLoadProfile},
	{`id="Instance_Efficiency_Percentages`, SectionInstanceEfficiency},
	{`id="IO_Profile"`, SectionIOProfile},
	{`id="User_Tables_stats"`, SectionUserTables},
	{`id="User_Index_stats"`, SectionUserIndexes},
	{`id="SQL_ordered_by_Elapsed_Time"`, SectionTopSQLByElapsed},
}

// nextSectionAnchorRE matches any h2/h3 anchor opening, used to find the
// boundary where one section ends and the next begins.
var nextSectionAnchorRE = regexp.MustCompile(`<h[23][^>]*\sid="[^"]+"`)

// ExtractRawSections walks the raw HTML and returns a map of SectionKey →
// htmlToText'd content. Sections that aren't found in the file are omitted
// (caller should check map presence, not key validity).
//
// For text-format WDRs (no HTML anchors) returns an empty map — the legacy
// section finder in parser.go still handles those.
func ExtractRawSections(raw string) map[string]string {
	out := make(map[string]string)
	if !strings.Contains(raw, `<h`) {
		return out
	}

	for _, anchor := range sectionAnchorMap {
		start := strings.Index(raw, anchor.contains)
		if start == -1 {
			continue
		}
		// Find next h2/h3 anchor after this one — that's the section end.
		end := len(raw)
		if m := nextSectionAnchorRE.FindStringIndex(raw[start+len(anchor.contains):]); m != nil {
			end = start + len(anchor.contains) + m[0]
		}
		fragment := raw[start:end]
		text := cleanupSectionText(htmlToText(fragment))
		if text == "" {
			continue
		}
		// Cap each section at 8KB so a single huge section can't dominate
		// the LLM prompt. User table stats can be 30KB+ on busy systems.
		if len(text) > 8192 {
			text = text[:8192] + "\n\n[...truncated, section larger than 8KB...]"
		}
		out[anchor.key] = text
	}
	return out
}

// cleanupSectionText collapses adjacent blank lines (htmlToText leaves
// many) and trims the leading anchor garbage line like:
//   id="Database_Stat" onclick="return msg(...)">-Database Stat
// down to just `-Database Stat`.
func cleanupSectionText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	prevBlank := false
	for i, l := range lines {
		l = strings.TrimSpace(l)

		// Clean up first line: strip "id=..." onclick noise, keep heading text
		if i == 0 {
			if idx := strings.LastIndex(l, ">"); idx >= 0 && idx < len(l)-1 {
				l = strings.TrimSpace(l[idx+1:])
			}
		}

		if l == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
		} else {
			prevBlank = false
		}
		out = append(out, l)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
