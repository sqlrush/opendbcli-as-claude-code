/*-------------------------------------------------------------------------
 *
 * panel.go
 *	  DisplayWidth returns the visual width of a string, ignoring ANSI
 *	  escape codes. CJK characters count as 2.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/panel.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"fmt"
	"regexp"
	"strings"

	runewidth "github.com/mattn/go-runewidth"
)

// ansiPattern matches ANSI escape sequences for width calculation.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// DisplayWidth returns the visual width of a string, ignoring ANSI escape codes.
// CJK characters count as 2.
func DisplayWidth(s string) int {
	clean := ansiPattern.ReplaceAllString(s, "")
	return runewidth.StringWidth(clean)
}

// PadRight pads a string to the given display width using spaces.
// Handles ANSI escape codes and CJK characters correctly.
func PadRight(s string, width int) string {
	dw := DisplayWidth(s)
	if dw >= width {
		return s
	}
	return s + strings.Repeat(" ", width-dw)
}

// TruncateWidth truncates a string to fit within maxW display columns.
// ANSI escape codes pass through without counting toward width.
func TruncateWidth(s string, maxW int) string {
	if DisplayWidth(s) <= maxW {
		return s
	}
	clean := ansiPattern.ReplaceAllString(s, "")
	w := 0
	for i, r := range clean {
		rw := runewidth.RuneWidth(r)
		if w+rw > maxW {
			return clean[:i]
		}
		w += rw
	}
	return clean
}

// PanelSection represents a named section within a panel.
type PanelSection struct {
	Header string   // section header (empty = no separator)
	Lines  []string // content lines (may contain ANSI codes)
}

// Panel renders a box-drawing panel with title, sections, and consistent right-border alignment.
//
//	┌─ Title ─────────────────────────┐
//	│  content line                    │
//	├─ Section ───────────────────────┤
//	│  content line                    │
//	└─────────────────────────────────┘
func Panel(title string, sections []PanelSection) string {
	// Collect all content lines and compute max width.
	var allLines []string
	for _, sec := range sections {
		allLines = append(allLines, sec.Lines...)
	}

	// Title width: "─ " + title + " " + trailing "─"s
	titleW := DisplayWidth(title) + 4 // "─ " + title + " ─"

	maxContentW := titleW
	for _, line := range allLines {
		w := DisplayWidth(line)
		if w > maxContentW {
			maxContentW = w
		}
	}
	// Also consider section headers.
	for _, sec := range sections {
		if sec.Header != "" {
			hw := DisplayWidth(sec.Header) + 4
			if hw > maxContentW {
				maxContentW = hw
			}
		}
	}

	// Inner width = content + left padding(2) + right padding(1)
	innerW := maxContentW + 3

	var b strings.Builder

	// Top border: ┌─ Title ──...──┐
	b.WriteString(indent)
	b.WriteString("┌─ ")
	b.WriteString(title)
	b.WriteString(" ")
	remaining := innerW - DisplayWidth(title) - 3
	if remaining > 0 {
		b.WriteString(strings.Repeat("─", remaining))
	}
	b.WriteString("┐\n")

	for i, sec := range sections {
		// Section separator (skip for first section if no header).
		if sec.Header != "" && i > 0 {
			b.WriteString(indent)
			b.WriteString("├─ ")
			b.WriteString(sec.Header)
			b.WriteString(" ")
			rem := innerW - DisplayWidth(sec.Header) - 3
			if rem > 0 {
				b.WriteString(strings.Repeat("─", rem))
			}
			b.WriteString("┤\n")
		} else if sec.Header != "" && i == 0 {
			// First section with header: add content line for header text.
			// Already covered by title, so render header as separator.
			b.WriteString(indent)
			b.WriteString("├─ ")
			b.WriteString(sec.Header)
			b.WriteString(" ")
			rem := innerW - DisplayWidth(sec.Header) - 3
			if rem > 0 {
				b.WriteString(strings.Repeat("─", rem))
			}
			b.WriteString("┤\n")
		}

		for _, line := range sec.Lines {
			// Split embedded newlines so each sub-line gets proper borders.
			subLines := strings.Split(line, "\n")
			for _, sub := range subLines {
				// Truncate if wider than box interior.
				if DisplayWidth(sub) > innerW-2 {
					sub = TruncateWidth(sub, innerW-5) + "..."
				}
				b.WriteString(indent)
				b.WriteString("│ ")
				b.WriteString(PadRight(sub, innerW-2))
				b.WriteString(" │\n")
			}
		}
	}

	// Bottom border.
	b.WriteString(indent)
	b.WriteString("└")
	b.WriteString(strings.Repeat("─", innerW))
	b.WriteString("┘\n")

	return b.String()
}

// ProgressBar renders a visual progress bar.
// width is the total character width of the bar.
// Example: ████████░░░░░░░░░░  (18 chars)
func ProgressBar(pct float64, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(pct / 100 * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return strings.Repeat("█", filled) + strings.Repeat("░", empty)
}

// StatusIcon returns a colored status icon.
func StatusIcon(status string) string {
	switch strings.ToUpper(status) {
	case "OK":
		return "\033[32m✓\033[0m"
	case "WARN", "WARNING":
		return "\033[33m⚠\033[0m"
	case "FAIL", "CRITICAL":
		return "\033[1;31m✗\033[0m"
	default:
		return " "
	}
}

// StatusLabel returns a colored status text.
func StatusLabel(status string) string {
	switch strings.ToUpper(status) {
	case "OK":
		return "\033[32mOK\033[0m"
	case "WARN", "WARNING":
		return "\033[33mWARNING\033[0m"
	case "FAIL", "CRITICAL":
		return "\033[1;31mCRITICAL\033[0m"
	default:
		return status
	}
}

// SepLine renders a thin separator line of the given width.
// Example: ──────────────────────────────
func SepLine(width int) string {
	return strings.Repeat("─", width)
}

// TruncDisplayWidth truncates a string to maxW display width, adding "…" if truncated.
// Only handles plain text (no ANSI codes in the string).
func TruncDisplayWidth(s string, maxW int) string {
	if DisplayWidth(s) <= maxW {
		return s
	}
	if maxW <= 1 {
		return "…"
	}
	target := maxW - 1 // reserve 1 for "…"
	w := 0
	var b strings.Builder
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > target {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteString("…")
	return b.String()
}

// PadLeft pads a string to the given display width with leading spaces.
// Handles ANSI escape codes correctly.
func PadLeft(s string, width int) string {
	dw := DisplayWidth(s)
	if dw >= width {
		return s
	}
	return strings.Repeat(" ", width-dw) + s
}

// FormatBytes formats bytes into human-readable format (KB, MB, GB, TB).
func FormatBytes(mb float64) string {
	switch {
	case mb >= 1024*1024:
		return fmt.Sprintf("%.1fT", mb/1024/1024)
	case mb >= 1024:
		return fmt.Sprintf("%.1fG", mb/1024)
	case mb >= 1:
		return fmt.Sprintf("%.0fM", mb)
	default:
		return fmt.Sprintf("%.1fM", mb)
	}
}
