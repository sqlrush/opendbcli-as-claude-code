/*-------------------------------------------------------------------------
 *
 * json.go
 *	  FormatJSON renders a QueryResult as a JSON array of objects, where
 *	  each object uses column names as keys.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/format/json.go
 *
 *-------------------------------------------------------------------------
 */
package format

import (
	"encoding/json"

	"github.com/sqlrush/opendb/internal/db"
)

// FormatJSON renders a QueryResult as a JSON array of objects,
// where each object uses column names as keys.
func FormatJSON(result *db.QueryResult) string {
	if result == nil || len(result.Rows) == 0 {
		return "[]"
	}

	objects := make([]map[string]any, 0, len(result.Rows))

	for _, row := range result.Rows {
		obj := make(map[string]any, len(result.Columns))
		for i, col := range result.Columns {
			if i < len(row) {
				obj[col] = row[i]
			} else {
				obj[col] = nil
			}
		}
		objects = append(objects, obj)
	}

	data, err := json.MarshalIndent(objects, "", "  ")
	if err != nil {
		return "[]"
	}

	return string(data)
}
