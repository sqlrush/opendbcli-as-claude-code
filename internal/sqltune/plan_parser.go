/*-------------------------------------------------------------------------
 *
 * plan_parser.go
 *	  Shared EXPLAIN JSON parser for the PostgreSQL family
 *	  (PG / openGauss / GaussDB).
 *
 *	  PG-family dialects emit EXPLAIN (FORMAT JSON) with a stable schema:
 *	  one top-level array element per "plan" containing a "Plan" map.
 *	  Each Plan map has "Node Type" + cost/rows/timing fields and an
 *	  optional "Plans" array of children. This format is identical
 *	  across PG 10-16 and openGauss 3-6 because openGauss forked from
 *	  PG and never diverged on EXPLAIN output.
 *
 *	  Centralizing the parser here:
 *	    - Prevents drift between og's parser and pg's parser (M3 found
 *	      og had a hand-rolled one; pg would have inevitably copied it
 *	      and the two would diverge on field handling over time).
 *	    - Lets future dialect implementations (GaussDB M4b) just call
 *	      ParsePGStylePlanNode + add their own dialect-specific fields
 *	      via the Raw map if needed.
 *
 *	  NOT used by MySQL (its EXPLAIN JSON has completely different shape:
 *	  query_block / nested_loop / table — MySQL's parser stays in its
 *	  own package).
 *
 *	  NOT used by Oracle (no JSON EXPLAIN — uses DBMS_XPLAN text).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/plan_parser.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

// ParsePGStylePlanNode recursively converts a PG-family EXPLAIN JSON
// map (one Plan node) into a neutral PlanNode struct. Returns nil on
// nil input so callers can chain calls safely.
//
// The input map is what you get from json.Unmarshal-ing one element
// of the EXPLAIN result's outermost array, indexed at ["Plan"].
//
// Field name choices follow PG's EXPLAIN output verbatim (capitalized
// with spaces). openGauss matches because it's a fork; if GaussDB
// diverges on any field name we'll add aliases here.
func ParsePGStylePlanNode(m map[string]any) *PlanNode {
	if m == nil {
		return nil
	}
	n := &PlanNode{
		Operator:       pgStr(m, "Node Type"),
		RelationName:   pgStr(m, "Relation Name"),
		Alias:          pgStr(m, "Alias"),
		StartupCost:    pgFloat(m, "Startup Cost"),
		TotalCost:      pgFloat(m, "Total Cost"),
		PlanRows:       pgInt(m, "Plan Rows"),
		PlanWidth:      int(pgInt(m, "Plan Width")),
		ActualStartup:  pgFloat(m, "Actual Startup Time"),
		ActualTotal:    pgFloat(m, "Actual Total Time"),
		ActualRows:     pgInt(m, "Actual Rows"),
		ActualLoops:    pgInt(m, "Actual Loops"),
		SharedHit:      pgInt(m, "Shared Hit Blocks"),
		SharedRead:     pgInt(m, "Shared Read Blocks"),
		Filter:         pgStr(m, "Filter"),
		JoinFilter:     pgStr(m, "Join Filter"),
		HashCondition:  pgStr(m, "Hash Cond"),
		IndexCondition: pgStr(m, "Index Cond"),
		SortMethod:     pgStr(m, "Sort Method"),
		SortSpaceType:  pgStr(m, "Sort Space Type"),
		SortSpaceUsed:  pgInt(m, "Sort Space Used"),
	}
	if sk, ok := m["Sort Key"].([]any); ok {
		for _, s := range sk {
			if str, ok := s.(string); ok {
				n.SortKey = append(n.SortKey, str)
			}
		}
	}
	if children, ok := m["Plans"].([]any); ok {
		for _, c := range children {
			if cm, ok := c.(map[string]any); ok {
				n.Children = append(n.Children, ParsePGStylePlanNode(cm))
			}
		}
	}
	return n
}

// pgStr / pgFloat / pgInt — type-tolerant accessors. json.Unmarshal
// returns float64 for all JSON numbers regardless of whether the value
// was originally integer; callers don't care about the distinction.
//
// Unexported because each dialect package using these would otherwise
// have to re-implement the same helpers — keeping them internal to the
// parser keeps the public API of sqltune small.

func pgStr(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func pgFloat(m map[string]any, key string) float64 {
	switch v := m[key].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

func pgInt(m map[string]any, key string) int64 {
	switch v := m[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	}
	return 0
}
