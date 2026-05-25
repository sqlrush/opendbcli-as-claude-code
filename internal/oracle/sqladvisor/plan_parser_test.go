/*-------------------------------------------------------------------------
 *
 * plan_parser_test.go
 *	  Test cases for plan_parser.go (sqladvisor package):
 *	  TestParsePlanTree_ThreeLevelTree,
 *	  TestParsePlanTree_WithActualRows, TestParsePlanTree_SingleNode.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/plan_parser_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqladvisor

import (
	"testing"
)

func TestParsePlanTree_ThreeLevelTree(t *testing.T) {
	// SELECT STATEMENT (id=0)
	//   NESTED LOOPS (id=1, parent=0)
	//     TABLE ACCESS FULL (id=2, parent=1) — EMP
	//     INDEX UNIQUE SCAN (id=3, parent=1) — PK_DEPT
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT", Cost: 10, Rows: 100},
		{ID: 1, ParentID: 0, Operation: "NESTED LOOPS", Cost: 8, Rows: 100},
		{ID: 2, ParentID: 1, Operation: "TABLE ACCESS", Options: "FULL", ObjectOwner: "HR", ObjectName: "EMP", Cost: 5, Rows: 50},
		{ID: 3, ParentID: 1, Operation: "INDEX", Options: "UNIQUE SCAN", ObjectOwner: "HR", ObjectName: "PK_DEPT", Cost: 1, Rows: 1},
	}

	root := ParsePlanTree(rows)
	if root == nil {
		t.Fatal("expected non-nil root")
	}

	if root.ID != 0 {
		t.Errorf("root ID = %d, want 0", root.ID)
	}
	if root.Operation != "SELECT STATEMENT" {
		t.Errorf("root Operation = %q, want %q", root.Operation, "SELECT STATEMENT")
	}
	if len(root.Children) != 1 {
		t.Fatalf("root children count = %d, want 1", len(root.Children))
	}

	nested := root.Children[0]
	if nested.ID != 1 {
		t.Errorf("nested loop ID = %d, want 1", nested.ID)
	}
	if nested.Operation != "NESTED LOOPS" {
		t.Errorf("nested Operation = %q, want %q", nested.Operation, "NESTED LOOPS")
	}
	if len(nested.Children) != 2 {
		t.Fatalf("nested children count = %d, want 2", len(nested.Children))
	}

	// Children order is not guaranteed by map iteration, find by ID.
	childByID := make(map[int]int)
	for i, c := range nested.Children {
		childByID[c.ID] = i
	}

	if idx, ok := childByID[2]; ok {
		child := nested.Children[idx]
		if child.Operation != "TABLE ACCESS" || child.Options != "FULL" {
			t.Errorf("child 2: got %q %q, want TABLE ACCESS FULL", child.Operation, child.Options)
		}
		if child.ObjectName != "EMP" {
			t.Errorf("child 2 ObjectName = %q, want EMP", child.ObjectName)
		}
	} else {
		t.Error("missing child with ID=2")
	}

	if idx, ok := childByID[3]; ok {
		child := nested.Children[idx]
		if child.Operation != "INDEX" || child.Options != "UNIQUE SCAN" {
			t.Errorf("child 3: got %q %q, want INDEX UNIQUE SCAN", child.Operation, child.Options)
		}
		if child.ObjectName != "PK_DEPT" {
			t.Errorf("child 3 ObjectName = %q, want PK_DEPT", child.ObjectName)
		}
	} else {
		t.Error("missing child with ID=3")
	}
}

func TestParsePlanTree_WithActualRows(t *testing.T) {
	actual := int64(42)
	starts := int64(1)
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT", Rows: 100, ActualRows: &actual, Starts: &starts},
	}

	root := ParsePlanTree(rows)
	if root == nil {
		t.Fatal("expected non-nil root")
	}
	if root.ActualRows == nil {
		t.Fatal("expected ActualRows to be set")
	}
	if *root.ActualRows != 42 {
		t.Errorf("ActualRows = %d, want 42", *root.ActualRows)
	}
	if root.Starts == nil {
		t.Fatal("expected Starts to be set")
	}
	if *root.Starts != 1 {
		t.Errorf("Starts = %d, want 1", *root.Starts)
	}
}

func TestParsePlanTree_SingleNode(t *testing.T) {
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT", Cost: 1},
	}

	root := ParsePlanTree(rows)
	if root == nil {
		t.Fatal("expected non-nil root")
	}
	if root.ID != 0 {
		t.Errorf("root ID = %d, want 0", root.ID)
	}
	if len(root.Children) != 0 {
		t.Errorf("root children count = %d, want 0", len(root.Children))
	}
}

func TestParsePlanTree_EmptyRows(t *testing.T) {
	root := ParsePlanTree(nil)
	if root != nil {
		t.Errorf("expected nil root for empty input, got ID=%d", root.ID)
	}

	root = ParsePlanTree([]PlanRow{})
	if root != nil {
		t.Errorf("expected nil root for zero-length input, got ID=%d", root.ID)
	}
}

func TestExtractTables_FindsTables(t *testing.T) {
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT"},
		{ID: 1, ParentID: 0, Operation: "NESTED LOOPS"},
		{ID: 2, ParentID: 1, Operation: "TABLE ACCESS", Options: "FULL", ObjectOwner: "HR", ObjectName: "EMPLOYEES"},
		{ID: 3, ParentID: 1, Operation: "TABLE ACCESS", Options: "BY INDEX ROWID", ObjectOwner: "HR", ObjectName: "DEPARTMENTS"},
		{ID: 4, ParentID: 3, Operation: "INDEX", Options: "UNIQUE SCAN", ObjectOwner: "HR", ObjectName: "PK_DEPT"},
	}

	root := ParsePlanTree(rows)
	tables := ExtractTables(root)

	if len(tables) != 2 {
		t.Fatalf("table count = %d, want 2", len(tables))
	}

	found := make(map[string]bool)
	for _, tr := range tables {
		found[tr.Owner+"."+tr.Name] = true
	}

	if !found["HR.EMPLOYEES"] {
		t.Error("missing table HR.EMPLOYEES")
	}
	if !found["HR.DEPARTMENTS"] {
		t.Error("missing table HR.DEPARTMENTS")
	}
}

func TestExtractTables_SkipsNonTableAccess(t *testing.T) {
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT"},
		{ID: 1, ParentID: 0, Operation: "INDEX", Options: "UNIQUE SCAN", ObjectOwner: "HR", ObjectName: "PK_EMP"},
	}

	root := ParsePlanTree(rows)
	tables := ExtractTables(root)

	if len(tables) != 0 {
		t.Errorf("table count = %d, want 0 (INDEX UNIQUE SCAN is not TABLE ACCESS)", len(tables))
	}
}

func TestExtractTables_DeduplicatesSameTable(t *testing.T) {
	rows := []PlanRow{
		{ID: 0, ParentID: 0, Operation: "SELECT STATEMENT"},
		{ID: 1, ParentID: 0, Operation: "UNION-ALL"},
		{ID: 2, ParentID: 1, Operation: "TABLE ACCESS", Options: "FULL", ObjectOwner: "HR", ObjectName: "EMP"},
		{ID: 3, ParentID: 1, Operation: "TABLE ACCESS", Options: "FULL", ObjectOwner: "HR", ObjectName: "EMP"},
	}

	root := ParsePlanTree(rows)
	tables := ExtractTables(root)

	if len(tables) != 1 {
		t.Errorf("table count = %d, want 1 (duplicates should be removed)", len(tables))
	}
}

func TestExtractTables_NilRoot(t *testing.T) {
	tables := ExtractTables(nil)
	if tables != nil {
		t.Errorf("expected nil for nil root, got %v", tables)
	}
}
