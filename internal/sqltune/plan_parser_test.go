/*-------------------------------------------------------------------------
 *
 * plan_parser_test.go
 *	  Tests for ParsePGStylePlanNode — the shared EXPLAIN JSON parser
 *	  used by og and pg (and future GaussDB centralized) dialects.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/plan_parser_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"encoding/json"
	"testing"
)

func TestParsePGStylePlanNode_NilInput(t *testing.T) {
	if got := ParsePGStylePlanNode(nil); got != nil {
		t.Errorf("nil input should return nil, got %+v", got)
	}
}

func TestParsePGStylePlanNode_SimpleSeqScan(t *testing.T) {
	raw := `{
        "Node Type": "Seq Scan",
        "Relation Name": "orders",
        "Alias": "o",
        "Startup Cost": 0.00,
        "Total Cost": 145.00,
        "Plan Rows": 100,
        "Plan Width": 32,
        "Filter": "(uid = 12345)"
    }`
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	n := ParsePGStylePlanNode(m)
	if n == nil {
		t.Fatalf("got nil")
	}
	if n.Operator != "Seq Scan" {
		t.Errorf("Operator = %q, want Seq Scan", n.Operator)
	}
	if n.RelationName != "orders" {
		t.Errorf("RelationName = %q, want orders", n.RelationName)
	}
	if n.TotalCost != 145.00 {
		t.Errorf("TotalCost = %v, want 145.00", n.TotalCost)
	}
	if n.PlanRows != 100 {
		t.Errorf("PlanRows = %d, want 100", n.PlanRows)
	}
	if n.Filter != "(uid = 12345)" {
		t.Errorf("Filter = %q, want (uid = 12345)", n.Filter)
	}
}

func TestParsePGStylePlanNode_NestedJoin(t *testing.T) {
	raw := `{
        "Node Type": "Hash Join",
        "Startup Cost": 1.50,
        "Total Cost": 250.00,
        "Plan Rows": 50,
        "Plan Width": 64,
        "Hash Cond": "(o.uid = u.id)",
        "Plans": [
            {"Node Type": "Seq Scan", "Relation Name": "orders", "Total Cost": 120.00, "Plan Rows": 1000},
            {"Node Type": "Hash", "Total Cost": 30.00, "Plans": [
                {"Node Type": "Seq Scan", "Relation Name": "users", "Total Cost": 25.00, "Plan Rows": 50}
            ]}
        ]
    }`
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	n := ParsePGStylePlanNode(m)
	if n == nil || n.Operator != "Hash Join" {
		t.Fatalf("expected Hash Join root, got %+v", n)
	}
	if n.HashCondition != "(o.uid = u.id)" {
		t.Errorf("HashCondition = %q", n.HashCondition)
	}
	if len(n.Children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(n.Children))
	}
	if n.Children[0].RelationName != "orders" {
		t.Errorf("child[0].RelationName = %q, want orders", n.Children[0].RelationName)
	}
	// Verify nested children: Hash → Seq Scan on users
	hash := n.Children[1]
	if hash.Operator != "Hash" {
		t.Errorf("child[1].Operator = %q, want Hash", hash.Operator)
	}
	if len(hash.Children) != 1 || hash.Children[0].RelationName != "users" {
		t.Errorf("expected Hash → Seq Scan on users, got %+v", hash.Children)
	}
}

func TestParsePGStylePlanNode_ActualValuesWhenAnalyze(t *testing.T) {
	raw := `{
        "Node Type": "Index Scan",
        "Relation Name": "orders",
        "Index Cond": "(uid = 12345)",
        "Startup Cost": 0.43,
        "Total Cost": 8.45,
        "Plan Rows": 1,
        "Actual Startup Time": 0.024,
        "Actual Total Time": 0.041,
        "Actual Rows": 3,
        "Actual Loops": 1,
        "Shared Hit Blocks": 4,
        "Shared Read Blocks": 0
    }`
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	n := ParsePGStylePlanNode(m)
	if n.IndexCondition != "(uid = 12345)" {
		t.Errorf("IndexCondition = %q", n.IndexCondition)
	}
	if n.ActualTotal != 0.041 {
		t.Errorf("ActualTotal = %v, want 0.041", n.ActualTotal)
	}
	if n.ActualRows != 3 {
		t.Errorf("ActualRows = %d, want 3", n.ActualRows)
	}
	if n.SharedHit != 4 {
		t.Errorf("SharedHit = %d, want 4", n.SharedHit)
	}
}

func TestParsePGStylePlanNode_SortKeyArray(t *testing.T) {
	raw := `{
        "Node Type": "Sort",
        "Total Cost": 100.00,
        "Plan Rows": 50,
        "Sort Key": ["created_at DESC", "id ASC"],
        "Sort Method": "quicksort",
        "Sort Space Type": "Memory",
        "Sort Space Used": 25
    }`
	var m map[string]any
	_ = json.Unmarshal([]byte(raw), &m)
	n := ParsePGStylePlanNode(m)
	if len(n.SortKey) != 2 || n.SortKey[0] != "created_at DESC" || n.SortKey[1] != "id ASC" {
		t.Errorf("SortKey = %v, want [created_at DESC, id ASC]", n.SortKey)
	}
	if n.SortMethod != "quicksort" || n.SortSpaceType != "Memory" || n.SortSpaceUsed != 25 {
		t.Errorf("Sort* fields not parsed: method=%q space=%q used=%d",
			n.SortMethod, n.SortSpaceType, n.SortSpaceUsed)
	}
}

func TestPGHelpers_TypeTolerance(t *testing.T) {
	// json.Unmarshal always gives float64 for numbers; helpers must
	// accept that AND legacy int/int64 from manually-constructed maps.
	m := map[string]any{
		"float_val":   3.14,
		"int_val":     42,
		"int64_val":   int64(100),
		"string_val":  "hi",
		"missing_key": nil,
	}
	if got := pgStr(m, "string_val"); got != "hi" {
		t.Errorf("pgStr string_val = %q", got)
	}
	if got := pgStr(m, "missing_key"); got != "" {
		t.Errorf("pgStr missing should be empty, got %q", got)
	}
	if got := pgFloat(m, "float_val"); got != 3.14 {
		t.Errorf("pgFloat float = %v", got)
	}
	if got := pgFloat(m, "int_val"); got != 42 {
		t.Errorf("pgFloat int = %v", got)
	}
	if got := pgFloat(m, "int64_val"); got != 100 {
		t.Errorf("pgFloat int64 = %v", got)
	}
	if got := pgInt(m, "float_val"); got != 3 {
		t.Errorf("pgInt float = %d", got)
	}
	if got := pgInt(m, "int_val"); got != 42 {
		t.Errorf("pgInt int = %d", got)
	}
}
