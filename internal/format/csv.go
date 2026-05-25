/*-------------------------------------------------------------------------
 *
 * csv.go
 *	  FormatCSV renders a QueryResult as standard CSV with a header row.
 *	  Values containing commas, quotes, or newlines are properly
 *	  escaped.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/csv.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"bytes"
	"encoding/csv"
	"fmt"

	"github.com/sqlrush/opendb/internal/db"
)

// FormatCSV renders a QueryResult as standard CSV with a header row.
// Values containing commas, quotes, or newlines are properly escaped.
func FormatCSV(result *db.QueryResult) string {
	if result == nil || len(result.Columns) == 0 {
		return ""
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)

	// Write header
	if err := writer.Write(result.Columns); err != nil {
		return ""
	}

	// Write data rows
	for _, row := range result.Rows {
		record := make([]string, len(result.Columns))
		for i := range result.Columns {
			if i < len(row) {
				record[i] = csvCellToString(row[i])
			}
		}
		if err := writer.Write(record); err != nil {
			return ""
		}
	}

	writer.Flush()

	if err := writer.Error(); err != nil {
		return ""
	}

	return buf.String()
}

// csvCellToString converts a cell value to a string for CSV output.
// nil values produce an empty string.
func csvCellToString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
