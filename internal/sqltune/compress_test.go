/*-------------------------------------------------------------------------
 *
 * compress_test.go
 *	  Tests for G7 token compression: trigger thresholds, plan tree
 *	  folding, schema hot/cold classification, token estimation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/compress_test.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"strings"
	"testing"
)

func TestShouldCompress_NilContext(t *testing.T) {
	trigger, _ := shouldCompress(nil)
	if trigger {
		t.Error("nil context should not trigger compression")
	}
}

func TestShouldCompress_SmallSQL_NoTrigger(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: "SELECT * FROM t WHERE id = 1",
		Plan:    &PlanInfo{Root: &PlanNode{Operator: "Seq Scan"}},
		InvolvedTables: []string{"t"},
	}
	trigger, _ := shouldCompress(cc)
	if trigger {
		t.Error("small SQL should not trigger compression")
	}
}

func TestShouldCompress_LongSQL_Triggers(t *testing.T) {
	// 600 lines of SQL
	cc := &CollectedContext{OrigSQL: strings.Repeat("SELECT 1;\n", 600)}
	trigger, reason := shouldCompress(cc)
	if !trigger {
		t.Error("600 lines of SQL should trigger compression")
	}
	if !strings.Contains(reason, "SQL") || !strings.Contains(reason, "行") {
		t.Errorf("reason missing SQL/行: %q", reason)
	}
}

func TestShouldCompress_ManyTables_Triggers(t *testing.T) {
	tables := make([]string, 20)
	for i := range tables {
		tables[i] = "t"
	}
	cc := &CollectedContext{
		OrigSQL:        "SELECT 1",
		InvolvedTables: tables,
	}
	trigger, reason := shouldCompress(cc)
	if !trigger {
		t.Error("20 involved tables should trigger compression (threshold 15)")
	}
	if !strings.Contains(reason, "涉及表") {
		t.Errorf("reason missing 涉及表: %q", reason)
	}
}

func TestShouldCompress_ManyPlanNodes_Triggers(t *testing.T) {
	// Build a balanced plan tree with > 50 nodes
	root := makeDeepPlan(60)
	cc := &CollectedContext{
		OrigSQL: "SELECT 1",
		Plan:    &PlanInfo{Root: root},
	}
	trigger, reason := shouldCompress(cc)
	if !trigger {
		t.Errorf("60 plan nodes should trigger compression (threshold 50): %s", reason)
	}
	if !strings.Contains(reason, "plan 节点") {
		t.Errorf("reason missing plan 节点: %q", reason)
	}
}

func TestCountPlanNodes(t *testing.T) {
	cases := []struct {
		n    *PlanNode
		want int
	}{
		{nil, 0},
		{&PlanNode{}, 1},
		{&PlanNode{Children: []*PlanNode{{}, {}}}, 3},
		{&PlanNode{Children: []*PlanNode{{Children: []*PlanNode{{}, {}}}}}, 4},
	}
	for i, c := range cases {
		if got := CountPlanNodes(c.n); got != c.want {
			t.Errorf("case %d: got %d, want %d", i, got, c.want)
		}
	}
}

func TestFoldPlanNodes_LowCostSubtreesFolded(t *testing.T) {
	// Root cost 1000, threshold 50 (5%).
	// Build: root → [hot (cost 800, 3 nodes), cold (cost 30, 5 nodes)]
	root := &PlanNode{
		Operator: "Root", TotalCost: 1000,
		Children: []*PlanNode{
			{Operator: "Hot", TotalCost: 800, Children: []*PlanNode{
				{Operator: "HotChild1", TotalCost: 400},
				{Operator: "HotChild2", TotalCost: 400},
			}},
			{Operator: "Cold", TotalCost: 30, Children: []*PlanNode{
				{Operator: "ColdChild1", TotalCost: 10, Children: []*PlanNode{
					{Operator: "ColdGrandchild", TotalCost: 5},
				}},
				{Operator: "ColdChild2", TotalCost: 10},
			}},
		},
	}
	folded := foldPlanNodes(root, 50.0)
	// Cold subtree has 4 nodes (Cold + 3 descendants); folded as 1 placeholder → 3 folded
	if folded != 3 {
		t.Errorf("folded = %d, want 3", folded)
	}
	// Verify cold was replaced with placeholder
	if len(root.Children) != 2 {
		t.Fatalf("root should have 2 children, got %d", len(root.Children))
	}
	if root.Children[0].Operator != "Hot" {
		t.Errorf("hot subtree should be preserved")
	}
	if !strings.HasPrefix(root.Children[1].Operator, "(...elided") {
		t.Errorf("cold subtree should be elided placeholder, got %q", root.Children[1].Operator)
	}
	// Hot subtree should keep its children
	if len(root.Children[0].Children) != 2 {
		t.Errorf("hot subtree children dropped")
	}
}

func TestFoldPlanNodes_SingleNodeLeafNotFolded(t *testing.T) {
	// Low-cost leaf (1 node) shouldn't be folded — placeholder + leaf is same size
	root := &PlanNode{
		Operator: "Root", TotalCost: 1000,
		Children: []*PlanNode{{Operator: "Leaf", TotalCost: 1}},
	}
	folded := foldPlanNodes(root, 50.0)
	if folded != 0 {
		t.Errorf("single leaf should not be folded, got folded=%d", folded)
	}
	if root.Children[0].Operator != "Leaf" {
		t.Errorf("leaf should be preserved as-is")
	}
}

func TestIdentifyHotTables(t *testing.T) {
	root := &PlanNode{
		Operator: "Root", TotalCost: 1000,
		Children: []*PlanNode{
			{Operator: "Seq Scan", RelationName: "orders", TotalCost: 700},
			{Operator: "Seq Scan", RelationName: "users", TotalCost: 5}, // cold (< 50)
			{Operator: "Index Scan", RelationName: "logs", TotalCost: 200},
		},
	}
	hot := identifyHotTables(root)
	if !hot["orders"] {
		t.Error("orders should be hot (700 cost)")
	}
	if !hot["logs"] {
		t.Error("logs should be hot (200 cost)")
	}
	if hot["users"] {
		t.Error("users should NOT be hot (5 cost)")
	}
}

func TestIdentifyHotTables_CaseInsensitive(t *testing.T) {
	root := &PlanNode{TotalCost: 1000, Children: []*PlanNode{
		{RelationName: "Orders", TotalCost: 500},
	}}
	hot := identifyHotTables(root)
	// Keys should be lowercase for case-insensitive matching
	if !hot["orders"] {
		t.Errorf("hot keys should be lowercased; got %v", hot)
	}
}

func TestDemoteColdTables_DropsColdStats(t *testing.T) {
	schema := &SchemaInfo{
		Stats: map[string][]ColStat{
			"orders": {{Column: "id"}, {Column: "uid"}},
			"users":  {{Column: "id"}, {Column: "name"}},
		},
	}
	hot := map[string]bool{"orders": true}
	demoteColdTables(schema, hot)
	if _, ok := schema.Stats["orders"]; !ok {
		t.Error("hot table 'orders' stats should be preserved")
	}
	if _, ok := schema.Stats["users"]; ok {
		t.Error("cold table 'users' stats should be dropped")
	}
}

func TestCompress_NoOpForSmallContext(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: "SELECT 1",
		Plan:    &PlanInfo{Root: &PlanNode{Operator: "Result"}},
	}
	stats := Compress(cc)
	if stats.TriggerReason != "" {
		t.Errorf("small context shouldn't trigger; got reason %q", stats.TriggerReason)
	}
	if stats.FoldedNodes != 0 {
		t.Errorf("no folds expected; got %d", stats.FoldedNodes)
	}
	if len(cc.Notes) > 0 {
		t.Errorf("Notes shouldn't be modified for small context; got %v", cc.Notes)
	}
}

func TestCompress_TriggersAndAddsNote(t *testing.T) {
	cc := &CollectedContext{
		OrigSQL: strings.Repeat("SELECT 1;\n", 600),
		Plan: &PlanInfo{
			Root:      makeDeepPlan(10),
			TotalCost: 1000,
		},
	}
	stats := Compress(cc)
	if stats.TriggerReason == "" {
		t.Error("should have triggered (>500 lines)")
	}
	if len(cc.Notes) == 0 {
		t.Error("Notes should have G7 trigger message")
	}
	if !strings.Contains(cc.Notes[0], "G7 token 压缩") {
		t.Errorf("note should mention G7: %q", cc.Notes[0])
	}
}

func TestCompress_FoldsHugePlanWithMixedCosts(t *testing.T) {
	root := &PlanNode{
		Operator: "Root", TotalCost: 10000,
		Children: []*PlanNode{
			{Operator: "Hot1", TotalCost: 5000},
			// 5 cold subtrees, each with multiple low-cost nodes
			coldSubtree(10), coldSubtree(10), coldSubtree(10), coldSubtree(10), coldSubtree(10),
		},
	}
	cc := &CollectedContext{
		OrigSQL: "SELECT 1",
		Plan:    &PlanInfo{Root: root, TotalCost: 10000},
	}
	// Force trigger via many tables
	cc.InvolvedTables = make([]string, 20)
	for i := range cc.InvolvedTables {
		cc.InvolvedTables[i] = "t"
	}
	stats := Compress(cc)
	if stats.FoldedNodes == 0 {
		t.Error("expected some folds for cold subtrees")
	}
}

func TestEstimateTokens(t *testing.T) {
	// Token estimate should grow monotonically with content size
	small := estimateTokens(&CollectedContext{OrigSQL: "X"})
	medium := estimateTokens(&CollectedContext{OrigSQL: strings.Repeat("X", 100)})
	large := estimateTokens(&CollectedContext{OrigSQL: strings.Repeat("X", 10000)})
	if !(small < medium && medium < large) {
		t.Errorf("token estimate not monotonic: small=%d med=%d large=%d", small, medium, large)
	}
}

func TestEstimateTokens_IncludesTrace(t *testing.T) {
	noTrace := estimateTokens(&CollectedContext{OrigSQL: "X"})
	withTrace := estimateTokens(&CollectedContext{
		OrigSQL: "X",
		Trace:   &TraceData{Available: true, Body: strings.Repeat("Y", 10000)},
	})
	if withTrace <= noTrace {
		t.Errorf("trace body should add to estimate: no=%d with=%d", noTrace, withTrace)
	}
}

func TestEstimateTokens_NilSafe(t *testing.T) {
	if got := estimateTokens(nil); got != 0 {
		t.Errorf("nil context should estimate 0, got %d", got)
	}
}

// ── helpers ────────────────────────────────────────────────────────────

// makeDeepPlan returns a balanced binary tree with approximately n nodes.
func makeDeepPlan(n int) *PlanNode {
	if n <= 0 {
		return nil
	}
	root := &PlanNode{Operator: "N0", TotalCost: 100}
	remaining := n - 1
	queue := []*PlanNode{root}
	idx := 1
	for remaining > 0 && len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for k := 0; k < 2 && remaining > 0; k++ {
			child := &PlanNode{Operator: "N", TotalCost: 50}
			next.Children = append(next.Children, child)
			queue = append(queue, child)
			remaining--
			idx++
		}
	}
	return root
}

// coldSubtree creates a chain of n low-cost nodes.
func coldSubtree(depth int) *PlanNode {
	if depth <= 0 {
		return nil
	}
	root := &PlanNode{Operator: "Cold", TotalCost: 10}
	cur := root
	for i := 1; i < depth; i++ {
		child := &PlanNode{Operator: "ColdChild", TotalCost: 5}
		cur.Children = append(cur.Children, child)
		cur = child
	}
	return root
}
