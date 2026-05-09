/*-------------------------------------------------------------------------
 *
 * plan_parser.go
 *	  ParsePGExplainJSON parses PG EXPLAIN (FORMAT JSON) output into a
 *	  PlanNode tree.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/plan_parser.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"encoding/json"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// pgExplainResult represents the top-level EXPLAIN (FORMAT JSON) output.
// PG returns: [{"Plan": {...}}]
type pgExplainResult struct {
	Plan *pgPlanNode `json:"Plan"`
}

// pgPlanNode represents one node in PG's JSON plan output.
type pgPlanNode struct {
	NodeType     string        `json:"Node Type"`
	RelationName string        `json:"Relation Name"`
	IndexName    string        `json:"Index Name"`
	Alias        string        `json:"Alias"`
	StartupCost  float64       `json:"Startup Cost"`
	TotalCost    float64       `json:"Total Cost"`
	PlanRows     int64         `json:"Plan Rows"`
	PlanWidth    int64         `json:"Plan Width"`
	Filter       string        `json:"Filter"`
	IndexCond    string        `json:"Index Cond"`
	JoinFilter   string        `json:"Join Filter"`
	HashCond     string        `json:"Hash Cond"`
	MergeCond    string        `json:"Merge Cond"`
	RecheckCond  string        `json:"Recheck Cond"`
	SortKey      []string      `json:"Sort Key"`
	Plans        []*pgPlanNode `json:"Plans"`
}

// ParsePGExplainJSON parses PG EXPLAIN (FORMAT JSON) output into a PlanNode tree.
func ParsePGExplainJSON(jsonData []byte) (*sadv.PlanNode, error) {
	var results []pgExplainResult
	if err := json.Unmarshal(jsonData, &results); err != nil {
		return nil, err
	}
	if len(results) == 0 || results[0].Plan == nil {
		return nil, nil
	}

	idSeq := 0
	root := convertPGNode(results[0].Plan, &idSeq, 0)
	return root, nil
}

func convertPGNode(pg *pgPlanNode, idSeq *int, parentID int) *sadv.PlanNode {
	*idSeq++

	op, opts := mapNodeType(pg.NodeType)
	objectName := pg.RelationName
	if objectName == "" {
		objectName = pg.IndexName
	}

	filterPred := pg.Filter
	accessPred := pg.IndexCond
	if accessPred == "" {
		accessPred = pg.HashCond
	}
	if accessPred == "" {
		accessPred = pg.MergeCond
	}
	if accessPred == "" {
		accessPred = pg.RecheckCond
	}

	node := &sadv.PlanNode{
		ID:         *idSeq,
		ParentID:   parentID,
		Operation:  op,
		Options:    opts,
		ObjectName: objectName,
		Rows:       pg.PlanRows,
		Cost:       int64(pg.TotalCost),
		FilterPred: filterPred,
		AccessPred: accessPred,
	}

	for _, child := range pg.Plans {
		childNode := convertPGNode(child, idSeq, node.ID)
		node.Children = append(node.Children, childNode)
	}

	return node
}

// mapNodeType converts PG node types to Oracle-style operation/options
// so the shared analyzers (IsFullScan, IsIndexScan, etc.) work correctly.
func mapNodeType(nodeType string) (operation, options string) {
	upper := strings.ToUpper(nodeType)

	switch upper {
	// Table access patterns
	case "SEQ SCAN":
		return "TABLE ACCESS", "FULL"
	case "INDEX SCAN":
		return "INDEX", "RANGE SCAN"
	case "INDEX ONLY SCAN":
		return "INDEX", "FAST FULL SCAN"
	case "BITMAP HEAP SCAN":
		return "TABLE ACCESS", "BY INDEX ROWID BATCHED"
	case "BITMAP INDEX SCAN":
		return "INDEX", "BITMAP"
	case "TID SCAN":
		return "TABLE ACCESS", "BY ROWID"

	// Join patterns
	case "NESTED LOOP":
		return "NESTED LOOPS", ""
	case "HASH JOIN":
		return "HASH JOIN", ""
	case "MERGE JOIN":
		return "MERGE JOIN", ""

	// Sort and aggregate
	case "SORT":
		return "SORT", ""
	case "INCREMENTAL SORT":
		return "SORT", "INCREMENTAL"
	case "AGGREGATE":
		return "GROUP", "AGGREGATE"
	case "GROUP AGGREGATE":
		return "GROUP", "AGGREGATE"
	case "HASH AGGREGATE":
		return "GROUP", "HASH AGGREGATE"

	// Materialization
	case "MATERIALIZE":
		return "MATERIALIZE", ""
	case "MEMOIZE":
		return "MATERIALIZE", "MEMOIZE"
	case "HASH":
		return "HASH", ""

	// Subquery
	case "SUBQUERY SCAN":
		return "SUBQUERY", ""

	// Set operations
	case "APPEND":
		return "APPEND", ""
	case "MERGE APPEND":
		return "MERGE APPEND", ""
	case "UNIQUE":
		return "UNIQUE", ""
	case "SETOP":
		return "SETOP", ""

	// CTE
	case "CTE SCAN":
		return "CTE SCAN", ""

	// Result / limit
	case "RESULT":
		return "RESULT", ""
	case "LIMIT":
		return "LIMIT", ""

	default:
		return upper, ""
	}
}
