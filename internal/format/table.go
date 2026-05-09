/*-------------------------------------------------------------------------
 *
 * table.go
 *	  TableOptions controls table rendering behavior.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/table.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"fmt"
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sqlrush/opendb/internal/db"
)

const (
	// nullDisplay is the placeholder used for NULL / missing cells in
	// human-readable table output. CLAUDE.md mandates a dash here so empty
	// cells don't get visually mistaken for the string "NULL". JSON/CSV
	// exports keep their own null encoding (null / empty).
	nullDisplay = "-"
	// indent is the left margin for the table.
	indent = "  "
	// minColWidth is the minimum display width for any column.
	minColWidth = 4
	// overhead per column: "│" + space + content + space = 3 chars of padding + 1 border.
	// Total table width = indent(2) + border(1) + sum(colWidth+3) + ... last border already counted.
	// Per column overhead = 3 (space + space + one │). First │ is 1 extra.
)

// TableOptions controls table rendering behavior.
type TableOptions struct {
	MaxRows    int  // 0 = unlimited
	TermWidth  int  // terminal width; 0 = no limit
	Vertical   bool // MySQL \G style vertical output
}

// FormatTable renders a QueryResult as a Unicode box-drawing table.
func FormatTable(result *db.QueryResult) string {
	return FormatTableOpts(result, TableOptions{})
}

// FormatTableWithLimit renders a table, truncated to maxRows with a summary message.
func FormatTableWithLimit(result *db.QueryResult, maxRows int) string {
	return FormatTableOpts(result, TableOptions{MaxRows: maxRows})
}

// FormatTableFit renders a table that fits within termWidth.
func FormatTableFit(result *db.QueryResult, maxRows, termWidth int) string {
	return FormatTableOpts(result, TableOptions{MaxRows: maxRows, TermWidth: termWidth})
}

// FormatTableOpts renders a table with full options.
func FormatTableOpts(result *db.QueryResult, opts TableOptions) string {
	if result == nil || len(result.Columns) == 0 {
		return "No rows"
	}
	if len(result.Rows) == 0 {
		return "No rows"
	}

	rows := result.Rows
	truncated := false
	remaining := 0

	if opts.MaxRows > 0 && len(rows) > opts.MaxRows {
		remaining = len(rows) - opts.MaxRows
		rows = rows[:opts.MaxRows]
		truncated = true
	}

	// Convert all rows to string cells.
	allCells := make([][]string, len(rows))
	for i, row := range rows {
		allCells[i] = formatRowCells(result.Columns, row)
	}

	columns := result.Columns

	// Vertical mode: user explicitly requested \G.
	if opts.Vertical {
		return renderVertical(columns, allCells, opts.TermWidth, truncated, remaining)
	}

	// If too many columns to fit, truncate with "...(N列)" indicator.
	// droppedCols captures the names of columns that got hidden so we can
	// print them as a footer tip — the old "...(N列)" placeholder left the
	// user with no way to know which diagnostic fields vanished.
	var droppedCols []string
	if opts.TermWidth > 0 {
		visibleCols := countFittingColumns(columns, allCells, opts.TermWidth)
		if visibleCols < len(columns) {
			keep := visibleCols - 1
			if keep < 1 {
				keep = 1
			}
			droppedCols = append([]string(nil), columns[keep:]...)
			columns, allCells = truncateColumns(columns, allCells, visibleCols, len(columns))
		}
	}

	// Compute natural widths then fit to terminal.
	widths := computeNaturalWidths(columns, allCells)
	if opts.TermWidth > 0 {
		widths = fitWidths(widths, opts.TermWidth)
	}

	// Truncate cell content to fit widths.
	allCells = truncateCells(allCells, widths)
	// Also truncate column headers.
	truncatedCols := make([]string, len(columns))
	for i, col := range columns {
		truncatedCols[i] = truncateStr(col, widths[i])
	}

	var buf strings.Builder

	writeHorizontal(&buf, widths, "┌", "┬", "┐")
	writeRow(&buf, widths, truncatedCols)
	writeHorizontal(&buf, widths, "├", "┼", "┤")

	for _, cells := range allCells {
		writeRow(&buf, widths, cells)
	}

	writeHorizontal(&buf, widths, "└", "┴", "┘")

	if truncated {
		buf.WriteString(fmt.Sprintf("  ... and %d more rows\n", remaining))
	}
	if len(droppedCols) > 0 {
		// Spell out which columns were hidden so users can decide whether
		// to widen the terminal or switch to /G vertical mode.
		buf.WriteString(fmt.Sprintf("  ⚠ 隐藏 %d 列（宽度不够）: %s\n",
			len(droppedCols), strings.Join(droppedCols, ", ")))
	}

	return buf.String()
}

// ── Natural width computation ─────────────────────────────────

func computeNaturalWidths(columns []string, allCells [][]string) []int {
	widths := make([]int, len(columns))
	for i, col := range columns {
		widths[i] = runewidth.StringWidth(col)
	}
	for _, cells := range allCells {
		for i, cell := range cells {
			if i < len(widths) {
				w := runewidth.StringWidth(cell)
				if w > widths[i] {
					widths[i] = w
				}
			}
		}
	}
	return widths
}

// ── Smart width fitting ───────────────────────────────────────

// tableWidth calculates total table width from column widths.
// Formula: indent(2) + first│(1) + sum(1+colW+1 + │(1)) for each col.
// = 2 + 1 + numCols*(colW+3)  — but colW varies, so:
// = 2 + 1 + sum(colW+3)
func tableWidth(widths []int) int {
	total := 3 // indent(2) + first │(1)
	for _, w := range widths {
		total += w + 3 // space + content + space + │
	}
	return total
}

// fitWidths shrinks column widths to fit within termWidth.
// Shrinks the widest columns first, fairly.
func fitWidths(widths []int, termWidth int) []int {
	for tableWidth(widths) > termWidth {
		// Find the widest column.
		maxIdx := 0
		for i, w := range widths {
			if w > widths[maxIdx] {
				maxIdx = i
			}
		}
		if widths[maxIdx] <= minColWidth {
			break // Can't shrink further.
		}
		widths[maxIdx]--
	}
	return widths
}

// countFittingColumns returns how many columns can fit at minimum width.
func countFittingColumns(columns []string, allCells [][]string, termWidth int) int {
	// Each column needs at least: header width or minColWidth + 3 overhead.
	used := 3 // indent(2) + first │(1)
	count := 0
	for i, col := range columns {
		colW := runewidth.StringWidth(col)
		if colW < minColWidth {
			colW = minColWidth
		}
		// Also consider data width (use min of natural and reasonable cap).
		for _, cells := range allCells {
			if i < len(cells) {
				dw := runewidth.StringWidth(cells[i])
				if dw < colW {
					// data is narrower, column header width dominates.
				}
			}
		}
		needed := colW + 3
		if used+needed > termWidth {
			// Reserve space for "..." column if we're cutting.
			if count == 0 {
				count = 1 // At least show one column.
			}
			break
		}
		used += needed
		count++
	}
	if count == 0 {
		count = 1
	}
	return count
}

// truncateColumns keeps the first n-1 columns and adds a "...(N列)" placeholder.
func truncateColumns(columns []string, allCells [][]string, n, totalCols int) ([]string, [][]string) {
	if n >= totalCols {
		return columns, allCells
	}
	keep := n - 1
	if keep < 1 {
		keep = 1
	}
	hidden := totalCols - keep
	placeholder := fmt.Sprintf("...(%d列)", hidden)

	newCols := make([]string, keep+1)
	copy(newCols, columns[:keep])
	newCols[keep] = placeholder

	newCells := make([][]string, len(allCells))
	for i, cells := range allCells {
		row := make([]string, keep+1)
		for j := 0; j < keep && j < len(cells); j++ {
			row[j] = cells[j]
		}
		row[keep] = ""
		newCells[i] = row
	}
	return newCols, newCells
}

// ── Cell truncation ───────────────────────────────────────────

func truncateCells(allCells [][]string, widths []int) [][]string {
	result := make([][]string, len(allCells))
	for i, cells := range allCells {
		row := make([]string, len(cells))
		for j, cell := range cells {
			if j < len(widths) {
				row[j] = truncateStr(cell, widths[j])
			} else {
				row[j] = cell
			}
		}
		result[i] = row
	}
	return result
}

func truncateStr(s string, maxW int) string {
	if runewidth.StringWidth(s) <= maxW {
		return s
	}
	if maxW <= 3 {
		return s[:maxW] // edge case: very narrow
	}
	// Truncate rune by rune.
	w := 0
	target := maxW - 3 // reserve space for "..."
	var b strings.Builder
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > target {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	b.WriteString("...")
	return b.String()
}

// ── Vertical (MySQL \G) rendering ─────────────────────────────

func renderVertical(columns []string, allCells [][]string, termWidth int, truncated bool, remaining int) string {
	// Find max column name width for alignment.
	maxNameW := 0
	for _, col := range columns {
		w := runewidth.StringWidth(col)
		if w > maxNameW {
			maxNameW = w
		}
	}

	valueW := termWidth - 2 - maxNameW - 3 - 2 // indent + name + " : " + margin
	if valueW < 10 {
		valueW = 10
	}

	var buf strings.Builder
	for i, cells := range allCells {
		buf.WriteString(fmt.Sprintf("  *** Row %d ***\n", i+1))
		for j, col := range columns {
			val := ""
			if j < len(cells) {
				val = cells[j]
			}
			val = truncateStr(val, valueW)
			// Right-align column name.
			nameW := runewidth.StringWidth(col)
			pad := maxNameW - nameW
			buf.WriteString(indent)
			buf.WriteString(strings.Repeat(" ", pad))
			buf.WriteString(col)
			buf.WriteString(" : ")
			buf.WriteString(val)
			buf.WriteString("\n")
		}
	}

	if truncated {
		buf.WriteString(fmt.Sprintf("  ... and %d more rows\n", remaining))
	}

	return buf.String()
}

// ── Table drawing ─────────────────────────────────────────────

func formatRowCells(columns []string, row []any) []string {
	cells := make([]string, len(columns))
	for i := range columns {
		if i < len(row) {
			cells[i] = cellToString(row[i])
		} else {
			cells[i] = nullDisplay
		}
	}
	return cells
}

func cellToString(v any) string {
	if v == nil {
		return nullDisplay
	}
	s := fmt.Sprintf("%v", v)
	// Replace newlines and tabs with spaces to prevent table row breakage.
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return s
}

func writeHorizontal(buf *strings.Builder, widths []int, left, mid, right string) {
	buf.WriteString(indent)
	buf.WriteString(left)
	for i, w := range widths {
		buf.WriteString(strings.Repeat("─", w+2))
		if i < len(widths)-1 {
			buf.WriteString(mid)
		}
	}
	buf.WriteString(right)
	buf.WriteString("\n")
}

func writeRow(buf *strings.Builder, widths []int, cells []string) {
	buf.WriteString(indent)
	buf.WriteString("│")
	for i, cell := range cells {
		w := widths[i]
		cellW := runewidth.StringWidth(cell)
		pad := w - cellW
		if pad < 0 {
			pad = 0
		}
		buf.WriteString(" ")
		buf.WriteString(cell)
		buf.WriteString(strings.Repeat(" ", pad))
		buf.WriteString(" ")
		buf.WriteString("│")
	}
	buf.WriteString("\n")
}
