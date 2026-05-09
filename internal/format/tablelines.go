/*-------------------------------------------------------------------------
 *
 * tablelines.go
 *	  TableLines holds pre-formatted table lines for the scrollable
 *	  browser.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/tablelines.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"strings"

	runewidth "github.com/mattn/go-runewidth"

	"github.com/sqlrush/opendb/internal/db"
)

// TableLines holds pre-formatted table lines for the scrollable browser.
type TableLines struct {
	// Header contains the top border, column headers, and header separator (3 lines).
	Header []string
	// Rows contains one formatted line per data row.
	Rows []string
	// Footer is the bottom border line.
	Footer string
	// TotalRows is the total number of rows in the original result.
	TotalRows int
}

// FormatTableLines builds table lines suitable for a scrollable browser.
// maxRows caps how many rows to format (0 = unlimited, recommended 500).
func FormatTableLines(result *db.QueryResult, termWidth, maxRows int) *TableLines {
	if result == nil || len(result.Columns) == 0 || len(result.Rows) == 0 {
		return nil
	}

	rows := result.Rows
	total := len(rows)
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}

	// Convert to string cells.
	allCells := make([][]string, len(rows))
	for i, row := range rows {
		allCells[i] = formatRowCells(result.Columns, row)
	}

	columns := result.Columns

	// Truncate columns if too many.
	if termWidth > 0 {
		visibleCols := countFittingColumns(columns, allCells, termWidth)
		if visibleCols < len(columns) {
			columns, allCells = truncateColumns(columns, allCells, visibleCols, len(columns))
		}
	}

	// Compute and fit widths.
	widths := computeNaturalWidths(columns, allCells)
	if termWidth > 0 {
		widths = fitWidths(widths, termWidth)
	}

	// Truncate cells to fit.
	allCells = truncateCells(allCells, widths)
	truncatedCols := make([]string, len(columns))
	for i, col := range columns {
		truncatedCols[i] = truncateStr(col, widths[i])
	}

	tl := &TableLines{TotalRows: total}

	// Build header lines.
	var buf strings.Builder

	writeHorizontal(&buf, widths, "┌", "┬", "┐")
	tl.Header = append(tl.Header, strings.TrimRight(buf.String(), "\n"))

	buf.Reset()
	writeRow(&buf, widths, truncatedCols)
	tl.Header = append(tl.Header, strings.TrimRight(buf.String(), "\n"))

	buf.Reset()
	writeHorizontal(&buf, widths, "├", "┼", "┤")
	tl.Header = append(tl.Header, strings.TrimRight(buf.String(), "\n"))

	// Build data row lines.
	for _, cells := range allCells {
		buf.Reset()
		writeRow(&buf, widths, cells)
		tl.Rows = append(tl.Rows, strings.TrimRight(buf.String(), "\n"))
	}

	// Build footer.
	buf.Reset()
	writeHorizontal(&buf, widths, "└", "┴", "┘")
	tl.Footer = strings.TrimRight(buf.String(), "\n")

	// Compute visible width of a row line for alignment (not used yet but available).
	_ = runewidth.StringWidth("│")

	return tl
}
