/*-------------------------------------------------------------------------
 *
 * format.go
 *	  FormatType represents the output format type.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/format.go
 *
 *-------------------------------------------------------------------------
 */
package format

import "github.com/sqlrush/opendb/internal/db"

// FormatType represents the output format type.
type FormatType string

const (
	FormatTypeTable FormatType = "table"
	FormatTypeJSON  FormatType = "json"
	FormatTypeCSV   FormatType = "csv"
)

// FormatOptions controls how query results are formatted.
type FormatOptions struct {
	MaxRows    int
	FormatType FormatType
}

// Format dispatches to the correct formatter based on FormatOptions.
func Format(result *db.QueryResult, opts FormatOptions) string {
	switch opts.FormatType {
	case FormatTypeJSON:
		return FormatJSON(result)
	case FormatTypeCSV:
		return FormatCSV(result)
	case FormatTypeTable:
		if opts.MaxRows > 0 {
			return FormatTableWithLimit(result, opts.MaxRows)
		}
		return FormatTable(result)
	default:
		if opts.MaxRows > 0 {
			return FormatTableWithLimit(result, opts.MaxRows)
		}
		return FormatTable(result)
	}
}
