/*-------------------------------------------------------------------------
 *
 * helpers.go
 *	  Shared helpers for the Oracle sqladvisor analyzer modules —
 *	  common SQL parsers, plan-text utilities, and severity scoring
 *	  used by every analyzer (joins, indexes, stats, hints).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/helpers.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import sadv "github.com/sqlrush/opendb/internal/sqladvisor"

// walkTree visits every node in the plan tree depth-first.
func walkTree(node *sadv.PlanNode, fn func(*sadv.PlanNode)) {
	if node == nil {
		return
	}
	fn(node)
	for _, child := range node.Children {
		walkTree(child, fn)
	}
}

// tableKey builds the "OWNER.TABLE" lookup key.
func tableKey(owner, table string) string {
	if owner == "" {
		return table
	}
	return owner + "." + table
}
