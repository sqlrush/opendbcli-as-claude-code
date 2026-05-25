/*-------------------------------------------------------------------------
 *
 * helpers.go
 *	  WalkTree performs depth-first traversal of a plan tree.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqladvisor/analyzers/helpers.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// WalkTree performs depth-first traversal of a plan tree.
func WalkTree(node *sadv.PlanNode, fn func(*sadv.PlanNode)) {
	if node == nil {
		return
	}
	fn(node)
	for _, child := range node.Children {
		WalkTree(child, fn)
	}
}

// TableKey returns the lookup key for table stats/indexes.
func TableKey(table string) string {
	return table
}

// IsAggregate returns true if the node is an aggregation operation.
func IsAggregate(n *sadv.PlanNode) bool {
	upper := strings.ToUpper(n.Operation)
	return upper == "GROUP" || strings.Contains(upper, "AGGREGATE") ||
		strings.Contains(upper, "HASH GROUP")
}

// MaxTableAccessRows returns the largest E-Rows from TABLE ACCESS nodes.
func MaxTableAccessRows(root *sadv.PlanNode) int64 {
	var maxRows int64
	WalkTree(root, func(n *sadv.PlanNode) {
		if n.Operation == "TABLE ACCESS" && n.Rows > maxRows {
			maxRows = n.Rows
		}
	})
	return maxRows
}
