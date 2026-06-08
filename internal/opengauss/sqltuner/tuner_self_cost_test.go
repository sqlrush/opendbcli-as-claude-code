package sqltuner

import (
	"strings"
	"testing"
)

func TestBuildSelfCostHotspotsRanksBySelfCostAndFiltersInheritedCost(t *testing.T) {
	orderItems := &PlanNode{
		Operator:     "Seq Scan",
		RelationName: "order_items",
		Alias:        "oi",
		TotalCost:    9206,
		PlanRows:     1000000,
	}
	products := &PlanNode{
		Operator:     "Seq Scan",
		RelationName: "products",
		Alias:        "p",
		TotalCost:    284,
		PlanRows:     1000,
	}
	inheritedSubtree := &PlanNode{
		Operator:  "Aggregate",
		TotalCost: 19553,
	}
	hashJoin := &PlanNode{
		Operator:  "Hash Join",
		TotalCost: 29043,
		Children:  []*PlanNode{orderItems, inheritedSubtree, products},
	}
	sortNode := &PlanNode{
		Operator:  "Sort",
		TotalCost: 29174,
		Children:  []*PlanNode{hashJoin},
	}
	plan := &PlanInfo{TotalCost: 29174, Root: sortNode}

	hotspots := buildSelfCostHotspots(plan, 12)
	if len(hotspots) == 0 {
		t.Fatal("expected at least one hotspot")
	}
	if got := hotspots[0].Node.RelationName; got != "order_items" {
		t.Fatalf("expected order_items self-cost hotspot first, got %q: %#v", got, hotspots)
	}
	if hotspots[0].SelfCost != 9206 {
		t.Fatalf("unexpected self cost for order_items: %.0f", hotspots[0].SelfCost)
	}
	for _, h := range hotspots {
		if h.Node == sortNode {
			t.Fatalf("sort inherited most cost and should not be a hotspot: %#v", hotspots)
		}
		if h.Node == products {
			t.Fatalf("low-share products scan should not be forced into hotspots: %#v", hotspots)
		}
	}
	rendered := formatSelfCostHotspots(plan, 12)
	if !strings.Contains(rendered, "Seq Scan on order_items: self_cost=9206") {
		t.Fatalf("rendered hotspots should expose self_cost-ranked node:\n%s", rendered)
	}
}
