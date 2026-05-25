/*-------------------------------------------------------------------------
 *
 * plan_parser.go
 *	  ParsePlanTree builds a PlanNode tree from a flat slice of PlanRow.
 *	  Returns the root node (ID=0 or the node with no parent).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/plan_parser.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// ParsePlanTree builds a PlanNode tree from a flat slice of PlanRow.
// Returns the root node (ID=0 or the node with no parent).
func ParsePlanTree(rows []PlanRow) *sadv.PlanNode {
	if len(rows) == 0 {
		return nil
	}

	nodes := make(map[int]*sadv.PlanNode, len(rows))
	for _, r := range rows {
		nodes[r.ID] = &sadv.PlanNode{
			ID:          r.ID,
			ParentID:    r.ParentID,
			Operation:   r.Operation,
			Options:     r.Options,
			ObjectName:  r.ObjectName,
			ObjectOwner: r.ObjectOwner,
			Rows:        r.Rows,
			ActualRows:  r.ActualRows,
			Starts:      r.Starts,
			Cost:        r.Cost,
			Bytes:       r.Bytes,
			FilterPred:  r.FilterPred,
			AccessPred:  r.AccessPred,
			OtherXML:    r.OtherXML,
		}
	}

	// Link children to parents.
	var root *sadv.PlanNode
	for _, node := range nodes {
		parent, hasParent := nodes[node.ParentID]
		if hasParent && node.ID != node.ParentID {
			parent.Children = append(parent.Children, node)
		} else {
			root = node
		}
	}

	return root
}

// ExtractTables walks the plan tree and collects unique table references
// from nodes whose Operation contains "TABLE ACCESS".
func ExtractTables(root *sadv.PlanNode) []TableRef {
	if root == nil {
		return nil
	}

	seen := make(map[string]struct{})
	var tables []TableRef
	collectTables(root, seen, &tables)

	return tables
}

func collectTables(node *sadv.PlanNode, seen map[string]struct{}, tables *[]TableRef) {
	if node == nil {
		return
	}

	if node.ObjectName != "" && strings.Contains(node.Operation, "TABLE ACCESS") {
		key := node.ObjectOwner + "." + node.ObjectName
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			*tables = append(*tables, TableRef{
				Owner: node.ObjectOwner,
				Name:  node.ObjectName,
			})
		}
	}

	for _, child := range node.Children {
		collectTables(child, seen, tables)
	}
}
