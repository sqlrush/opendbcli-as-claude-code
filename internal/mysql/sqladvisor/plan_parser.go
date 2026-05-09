/*-------------------------------------------------------------------------
 *
 * plan_parser.go
 *	  MySQLExplainJSON represents the top-level EXPLAIN FORMAT=JSON
 *	  output.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqladvisor/plan_parser.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"encoding/json"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// MySQLExplainJSON represents the top-level EXPLAIN FORMAT=JSON output.
type MySQLExplainJSON struct {
	QueryBlock *QueryBlock `json:"query_block"`
}

// QueryBlock is a node in the MySQL JSON plan tree.
type QueryBlock struct {
	SelectID       int              `json:"select_id"`
	CostInfo       *CostInfo        `json:"cost_info"`
	Table          *TableAccess     `json:"table"`
	NestedLoop     []NestedLoopItem `json:"nested_loop"`
	OrderingOp     *OrderingOp      `json:"ordering_operation"`
	GroupingOp     *GroupingOp      `json:"grouping_operation"`
	DupRemoval     *DupRemoval      `json:"duplicates_removal"`
	Subqueries     []SubqueryItem   `json:"attached_subqueries"`
	MaterializedQB *QueryBlock      `json:"materialized_from_subquery"`
	OptBlock       *QueryBlock      `json:"optimized_away_subqueries"`
	Message        string           `json:"message"`
}

// CostInfo for cost estimation.
type CostInfo struct {
	QueryCost string `json:"query_cost"`
	ReadCost  string `json:"read_cost"`
	EvalCost  string `json:"eval_cost"`
	SortCost  string `json:"sort_cost"`
}

// TableAccess represents one table access in the plan.
type TableAccess struct {
	TableName      string    `json:"table_name"`
	AccessType     string    `json:"access_type"` // ALL, ref, range, index, eq_ref, const, system, fulltext
	Key            string    `json:"key"`
	KeyLength      string    `json:"key_length"`
	PossibleKeys   []string  `json:"possible_keys"`
	Ref            []string  `json:"ref"`
	RowsExamined   int64     `json:"rows_examined_per_scan"`
	RowsProduced   int64     `json:"rows_produced_per_join"`
	Filtered       float64   `json:"filtered"`
	UsingIndex     bool      `json:"using_index"`
	CostInfo       *CostInfo `json:"cost_info"`
	AttachedCond   string    `json:"attached_condition"`
	UsingTmpTable  bool      `json:"using_temporary_table"`
	UsingFilesort  bool      `json:"using_filesort"`
	MaterializedQB *QueryBlock `json:"materialized_from_subquery"`
	UsedColumns    []string  `json:"used_columns"`
}

// NestedLoopItem wraps a table access in a nested loop.
type NestedLoopItem struct {
	Table *TableAccess `json:"table"`
}

// OrderingOp represents ORDER BY / filesort.
type OrderingOp struct {
	UsingFilesort  bool       `json:"using_filesort"`
	UsingTmpTable  bool       `json:"using_temporary_table"`
	CostInfo       *CostInfo  `json:"cost_info"`
	Table          *TableAccess `json:"table"`
	NestedLoop     []NestedLoopItem `json:"nested_loop"`
}

// GroupingOp represents GROUP BY.
type GroupingOp struct {
	UsingTmpTable  bool             `json:"using_temporary_table"`
	UsingFilesort  bool             `json:"using_filesort"`
	CostInfo       *CostInfo        `json:"cost_info"`
	Table          *TableAccess     `json:"table"`
	NestedLoop     []NestedLoopItem `json:"nested_loop"`
}

// DupRemoval represents DISTINCT.
type DupRemoval struct {
	UsingTmpTable  bool         `json:"using_temporary_table"`
	UsingFilesort  bool         `json:"using_filesort"`
	CostInfo       *CostInfo    `json:"cost_info"`
	Table          *TableAccess `json:"table"`
	NestedLoop     []NestedLoopItem `json:"nested_loop"`
}

// SubqueryItem for attached subqueries.
type SubqueryItem struct {
	Dependent  bool        `json:"dependent"`
	Cacheable  bool        `json:"cacheable"`
	QueryBlock *QueryBlock `json:"query_block"`
}

// ParseMySQLExplainJSON parses EXPLAIN FORMAT=JSON output into a PlanNode tree.
func ParseMySQLExplainJSON(jsonData []byte) (*sadv.PlanNode, error) {
	var explain MySQLExplainJSON
	if err := json.Unmarshal(jsonData, &explain); err != nil {
		return nil, err
	}
	if explain.QueryBlock == nil {
		return nil, nil
	}

	idSeq := 0
	root := convertQueryBlock(explain.QueryBlock, &idSeq, 0)
	return root, nil
}

func convertQueryBlock(qb *QueryBlock, idSeq *int, parentID int) *sadv.PlanNode {
	*idSeq++
	node := &sadv.PlanNode{
		ID:        *idSeq,
		ParentID:  parentID,
		Operation: "SELECT",
		Rows:      0,
	}

	// Single table access
	if qb.Table != nil {
		child := convertTableAccess(qb.Table, idSeq, node.ID)
		node.Children = append(node.Children, child)
	}

	// Nested loop join
	for _, nl := range qb.NestedLoop {
		if nl.Table != nil {
			child := convertTableAccess(nl.Table, idSeq, node.ID)
			node.Children = append(node.Children, child)
		}
	}

	// Ordering operation (filesort)
	if qb.OrderingOp != nil {
		*idSeq++
		sortNode := &sadv.PlanNode{
			ID:        *idSeq,
			ParentID:  node.ID,
			Operation: "SORT",
			Options:   "ORDER BY",
		}
		if qb.OrderingOp.UsingFilesort {
			sortNode.Options = "FILESORT"
		}
		if qb.OrderingOp.Table != nil {
			child := convertTableAccess(qb.OrderingOp.Table, idSeq, sortNode.ID)
			sortNode.Children = append(sortNode.Children, child)
		}
		for _, nl := range qb.OrderingOp.NestedLoop {
			if nl.Table != nil {
				child := convertTableAccess(nl.Table, idSeq, sortNode.ID)
				sortNode.Children = append(sortNode.Children, child)
			}
		}
		node.Children = append(node.Children, sortNode)
	}

	// Grouping operation
	if qb.GroupingOp != nil {
		*idSeq++
		grpNode := &sadv.PlanNode{
			ID:        *idSeq,
			ParentID:  node.ID,
			Operation: "GROUP",
		}
		if qb.GroupingOp.UsingTmpTable {
			grpNode.Options = "TEMPORARY TABLE"
		}
		if qb.GroupingOp.Table != nil {
			child := convertTableAccess(qb.GroupingOp.Table, idSeq, grpNode.ID)
			grpNode.Children = append(grpNode.Children, child)
		}
		for _, nl := range qb.GroupingOp.NestedLoop {
			if nl.Table != nil {
				child := convertTableAccess(nl.Table, idSeq, grpNode.ID)
				grpNode.Children = append(grpNode.Children, child)
			}
		}
		node.Children = append(node.Children, grpNode)
	}

	// Subqueries
	for _, sq := range qb.Subqueries {
		if sq.QueryBlock != nil {
			child := convertQueryBlock(sq.QueryBlock, idSeq, node.ID)
			child.Operation = "SUBQUERY"
			if sq.Dependent {
				child.Options = "DEPENDENT"
			}
			node.Children = append(node.Children, child)
		}
	}

	return node
}

func convertTableAccess(ta *TableAccess, idSeq *int, parentID int) *sadv.PlanNode {
	*idSeq++

	op := "TABLE ACCESS"
	opts := strings.ToUpper(ta.AccessType)
	switch ta.AccessType {
	case "ALL":
		opts = "FULL"
	case "index":
		op = "INDEX"
		opts = "FULL SCAN"
	case "ref", "eq_ref", "ref_or_null":
		op = "INDEX"
		opts = "RANGE SCAN"
	case "range":
		op = "INDEX"
		opts = "RANGE SCAN"
	case "const", "system":
		op = "INDEX"
		opts = "UNIQUE SCAN"
	}

	node := &sadv.PlanNode{
		ID:         *idSeq,
		ParentID:   parentID,
		Operation:  op,
		Options:    opts,
		ObjectName: ta.TableName,
		Rows:       ta.RowsExamined,
		FilterPred: ta.AttachedCond,
	}
	if ta.Key != "" {
		node.AccessPred = ta.Key
	}

	return node
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
