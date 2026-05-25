/*-------------------------------------------------------------------------
 *
 * termwidth.go
 *	  Package termwidth provides unified display-width calculation for
 *	  terminal output. All width measurement, truncation, padding, and
 *	  line wrapping in the UI layer should use this package to avoid
 *	  inconsistencies between different files calculating widths
 *	  differently.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/ui/termwidth/termwidth.go
 *
 *-------------------------------------------------------------------------
 */
// Package termwidth provides unified display-width calculation for terminal
// output. All width measurement, truncation, padding, and line wrapping in
// the UI layer should use this package to avoid inconsistencies between
// different files calculating widths differently.
package termwidth

import (
	"strings"
	"unicode/utf8"

	runewidth "github.com/mattn/go-runewidth"
)

func init() {
	// Ambiguous-width characters (box-drawing, etc.) are treated as width 1.
	// This matches the behavior of most modern terminal emulators.
	runewidth.DefaultCondition.EastAsianWidth = false
}

// StringWidth returns the display width of s, ignoring ANSI escape sequences.
func StringWidth(s string) int {
	return runewidth.StringWidth(StripANSI(s))
}

// RuneWidth returns the display width of a plain string (no ANSI codes).
// Use this when the string is known to be ANSI-free to avoid the strip cost.
func RuneWidth(s string) int {
	return runewidth.StringWidth(s)
}

// PadRight pads s with spaces to reach the given display width.
// Width is measured after stripping ANSI codes.
func PadRight(s string, width int) string {
	dw := StringWidth(s)
	if dw >= width {
		return s
	}
	return s + strings.Repeat(" ", width-dw)
}

// Truncate truncates s to fit within maxW display columns, preserving ANSI
// escape codes (they pass through without counting toward width).
// A trailing \033[0m reset is appended to ensure no style leaks.
func Truncate(s string, maxW int) string {
	if StringWidth(s) <= maxW {
		return s
	}
	var b strings.Builder
	w := 0
	i := 0
	for i < len(s) {
		// Pass through ANSI escape sequences without counting width.
		if s[i] == '\033' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && s[j] != 'm' {
					j++
				}
				if j < len(s) {
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			}
		}
		ru, size := utf8.DecodeRuneInString(s[i:])
		rw := runewidth.RuneWidth(ru)
		if w+rw > maxW {
			break
		}
		b.WriteRune(ru)
		w += rw
		i += size
	}
	b.WriteString("\033[0m")
	return b.String()
}

// WrapLine splits a line into multiple lines, each fitting within maxW
// visible columns. ANSI escape codes are preserved and carried across
// splits. This prevents terminal-level wrapping which breaks scroll regions.
func WrapLine(line string, maxW int) []string {
	if maxW <= 0 {
		return []string{line}
	}
	if StringWidth(line) <= maxW {
		return []string{line}
	}

	var result []string
	var cur strings.Builder
	w := 0
	i := 0
	for i < len(line) {
		// Pass through ANSI escape sequences without counting width.
		if line[i] == '\033' {
			j := i + 1
			if j < len(line) && line[j] == '[' {
				j++
				for j < len(line) && line[j] != 'm' {
					j++
				}
				if j < len(line) {
					j++
				}
				cur.WriteString(line[i:j])
				i = j
				continue
			}
		}
		ru, size := utf8.DecodeRuneInString(line[i:])
		rw := runewidth.RuneWidth(ru)
		if w+rw > maxW {
			// End current segment, reset ANSI, start new line.
			cur.WriteString("\033[0m")
			result = append(result, cur.String())
			cur.Reset()
			w = 0
		}
		cur.WriteRune(ru)
		w += rw
		i += size
	}
	if cur.Len() > 0 {
		result = append(result, cur.String())
	}
	if len(result) == 0 {
		return []string{line}
	}
	return result
}

// StripANSI removes all ANSI CSI escape sequences (e.g. \033[1;32m) from s.
func StripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\033' {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && s[j] != 'm' {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
