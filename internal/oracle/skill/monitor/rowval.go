/*-------------------------------------------------------------------------
 *
 * rowval.go
 *	  Row-value scanner helpers for Oracle monitor skills —
 *	  converts driver.Rows into the typed cell shape that table
 *	  renderers and JSON serialisers expect.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/rowval.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// rowStr extracts a string value from a query result row at the given index.
func rowStr(row []any, idx int) string {
	if idx >= len(row) || row[idx] == nil {
		return ""
	}
	return fmt.Sprintf("%v", row[idx])
}

// rowFloat extracts a float64 value from a query result row at the given index.
// Handles float64, int64, int, and string types (go-ora may return NUMBER as string).
func rowFloat(row []any, idx int) float64 {
	if idx >= len(row) || row[idx] == nil {
		return 0
	}
	switch v := row[idx].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		s := strings.TrimSpace(v)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
		return 0
	default:
		return 0
	}
}

// rowInt extracts an int value from a query result row at the given index.
// Handles int, int64, float64, and string types.
func rowInt(row []any, idx int) int {
	if idx >= len(row) || row[idx] == nil {
		return 0
	}
	switch v := row[idx].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		s := strings.TrimSpace(v)
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
		// Try parsing as float then truncating (e.g., "3.0").
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int(f)
		}
		return 0
	default:
		var i int
		fmt.Sscanf(fmt.Sprintf("%v", v), "%d", &i)
		return i
	}
}
