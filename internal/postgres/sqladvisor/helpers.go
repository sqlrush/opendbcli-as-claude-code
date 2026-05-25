/*-------------------------------------------------------------------------
 *
 * helpers.go
 *	  ExtractTables walks the plan tree and returns unique table names.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/helpers.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

func toString(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

func toFloat64(v any) float64 {
	if v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case []byte:
		var f float64
		fmt.Sscanf(string(n), "%f", &f)
		return f
	default:
		var f float64
		fmt.Sscanf(fmt.Sprint(v), "%f", &f)
		return f
	}
}

func toInt64(v any) int64 {
	return int64(toFloat64(v))
}

// ExtractTables walks the plan tree and returns unique table names.
func ExtractTables(root *sadv.PlanNode) []string {
	if root == nil {
		return nil
	}
	seen := make(map[string]bool)
	var tables []string
	walkTree(root, func(n *sadv.PlanNode) {
		if n.ObjectName != "" && !seen[n.ObjectName] {
			seen[n.ObjectName] = true
			tables = append(tables, n.ObjectName)
		}
	})
	return tables
}

func walkTree(node *sadv.PlanNode, fn func(*sadv.PlanNode)) {
	if node == nil {
		return
	}
	fn(node)
	for _, child := range node.Children {
		walkTree(child, fn)
	}
}
