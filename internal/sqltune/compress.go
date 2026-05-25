/*-------------------------------------------------------------------------
 *
 * compress.go
 *	  G7 千行 SQL token 压缩 — fits PromptBuilder + GenericTuner.
 *
 *	  Originally lived in internal/opengauss/sqltuner/token_compress.go
 *	  (og-only). M8 抽到 neutral 包让 GenericTuner (5 库共用) 也能用.
 *	  og 保留 wrapper 调 neutral 版本不破坏 og 现有 Tuner.
 *
 *	  千行 SQL 即使 1M context 模型也吃力 (200K+ token). 三类压缩:
 *
 *	    ❶ Plan tree 折叠: cost < 5% 的子树折成单行 "(...N nodes elided...)"
 *	    ❷ Schema 分级:    hot tables (cost top N 涉及) 给完整 stats,
 *	                       其他只给主键 + 索引数 + 行数 (drop column stats)
 *	    ❸ CTE 去重:       (TODO M8.x) 同 CTE 引用 N 次只发一份 SQL 文本
 *
 *	  触发条件: SQL > 500 行 OR plan 节点 > 50 OR 涉及表 > 15
 *	  目标: 最终塞 LLM prompt < 100K tokens
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/compress.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"fmt"
	"strings"
)

// Compression thresholds. Public so callers can override per-dialect or
// per-deployment if they have tighter / looser budgets.
var (
	CompressMaxPlanNodes  = 50  // 节点数超此值触发压缩
	CompressMaxSQLLines   = 500 // SQL 行数超此值触发压缩
	CompressMaxTables     = 15  // 涉及表数超此值触发压缩
	CompressLowCostRatio  = 0.05 // 子树 cost < total_cost × 5% → fold
	CompressTargetTokens  = 100000 // 目标 prompt token budget
)

// CompressionStats reports what Compress did. Caller can attach to
// ReportStats so the final markdown surfaces the action to the user.
type CompressionStats struct {
	TriggerReason    string // empty = no compression applied
	OrigPlanNodes    int
	FoldedNodes      int
	OrigSchemaTables int
	HotTables        int
	ColdTables       int
	EstOrigTokens    int
	EstFinalTokens   int
}

// shouldCompress decides if we need G7 compression for this context.
// Returns trigger flag + human-readable reason for stats.
func shouldCompress(cc *CollectedContext) (bool, string) {
	if cc == nil {
		return false, ""
	}
	if cc.Plan != nil {
		nodes := CountPlanNodes(cc.Plan.Root)
		if nodes > CompressMaxPlanNodes {
			return true, fmt.Sprintf("plan 节点 %d 超阈值 %d", nodes, CompressMaxPlanNodes)
		}
	}
	if len(cc.InvolvedTables) > CompressMaxTables {
		return true, fmt.Sprintf("涉及表 %d 超阈值 %d", len(cc.InvolvedTables), CompressMaxTables)
	}
	lines := strings.Count(cc.OrigSQL, "\n") + 1
	if lines > CompressMaxSQLLines {
		return true, fmt.Sprintf("SQL %d 行超阈值 %d", lines, CompressMaxSQLLines)
	}
	return false, ""
}

// Compress applies G7 compression in-place to a CollectedContext.
// Returns stats describing what was changed. Safe to call on small SQL
// too — it just no-ops if shouldCompress returns false.
//
// In-place mutation by design: callers tracking the original ctx (e.g.
// for caching) should deep-copy before calling.
func Compress(cc *CollectedContext) *CompressionStats {
	stats := &CompressionStats{}
	if cc == nil {
		return stats
	}
	if cc.Plan != nil {
		stats.OrigPlanNodes = CountPlanNodes(cc.Plan.Root)
	}
	if cc.Schema != nil {
		stats.OrigSchemaTables = len(cc.Schema.Tables)
	}

	trigger, reason := shouldCompress(cc)
	if !trigger {
		stats.EstOrigTokens = estimateTokens(cc)
		stats.EstFinalTokens = stats.EstOrigTokens
		return stats
	}
	stats.TriggerReason = reason

	// ❶ Plan tree 折叠
	if cc.Plan != nil && cc.Plan.Root != nil && cc.Plan.TotalCost > 0 {
		threshold := cc.Plan.TotalCost * CompressLowCostRatio
		stats.FoldedNodes = foldPlanNodes(cc.Plan.Root, threshold)
	}

	// ❷ Schema 分级
	if cc.Schema != nil && cc.Plan != nil {
		hotTables := identifyHotTables(cc.Plan.Root)
		stats.HotTables = len(hotTables)
		stats.ColdTables = len(cc.Schema.Tables) - len(hotTables)
		demoteColdTables(cc.Schema, hotTables)
	}

	stats.EstOrigTokens = estimateTokensRaw(cc.OrigSQL, stats.OrigPlanNodes, stats.OrigSchemaTables)
	stats.EstFinalTokens = estimateTokens(cc)

	// Surface action to user via Notes (rendered in final report).
	cc.Notes = append(cc.Notes, fmt.Sprintf(
		"G7 token 压缩已触发 (%s): plan %d 节点折叠 %d 个; schema %d hot / %d cold (~%dk → ~%dk tokens)",
		reason, stats.OrigPlanNodes, stats.FoldedNodes,
		stats.HotTables, stats.ColdTables,
		stats.EstOrigTokens/1000, stats.EstFinalTokens/1000))

	return stats
}

// ── Plan tree compression ──────────────────────────────────────────────

// CountPlanNodes recursively counts nodes in a plan tree. Exported
// because dialect-specific code (og's upgrade.go signal evaluator)
// needs the same count for orthogonal heuristics.
func CountPlanNodes(n *PlanNode) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += CountPlanNodes(c)
	}
	return total
}

// foldPlanNodes replaces subtrees whose total_cost < threshold AND have
// more than one node with a single-line placeholder PlanNode.
// Returns count of folded-away nodes (excludes the kept placeholder).
//
// Why "more than one node": single low-cost leaves don't shrink anything
// by being folded — placeholder + original would both be one line.
func foldPlanNodes(n *PlanNode, threshold float64) int {
	if n == nil {
		return 0
	}
	folded := 0
	var newChildren []*PlanNode
	for _, c := range n.Children {
		if c.TotalCost < threshold && CountPlanNodes(c) > 1 {
			elided := CountPlanNodes(c)
			folded += elided - 1 // we keep 1 placeholder
			newChildren = append(newChildren, &PlanNode{
				Operator:   fmt.Sprintf("(...elided %d low-cost nodes, total_cost=%.0f)", elided, c.TotalCost),
				TotalCost:  c.TotalCost,
				PlanRows:   c.PlanRows,
				ActualRows: c.ActualRows,
			})
		} else {
			folded += foldPlanNodes(c, threshold)
			newChildren = append(newChildren, c)
		}
	}
	n.Children = newChildren
	return folded
}

// ── Schema selectivity ──────────────────────────────────────────────────

// identifyHotTables returns table names that appear in plan nodes with
// cost ≥ root_total_cost × CompressLowCostRatio. Output keys are
// lowercased for case-insensitive matching (Oracle is case-sensitive
// in dictionary but user-typed SQL is often lower-case).
func identifyHotTables(root *PlanNode) map[string]bool {
	hot := make(map[string]bool)
	if root == nil {
		return hot
	}
	threshold := root.TotalCost * CompressLowCostRatio
	var walk func(*PlanNode)
	walk = func(n *PlanNode) {
		if n == nil {
			return
		}
		if n.TotalCost >= threshold && n.RelationName != "" {
			hot[strings.ToLower(n.RelationName)] = true
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return hot
}

// demoteColdTables strips full per-column stats from cold tables.
// Kept for cold tables: TableInfo (rows/pages/size) + IndexInfo (each is
// small one-line summary). Dropped: per-column n_distinct/null_frac/etc.
//
// Hot tables (high cost in plan) retain everything — LLM needs full
// stats to reason about access path choices.
//
// FKs kept globally (small data + hints JOIN paths valuable for both).
func demoteColdTables(s *SchemaInfo, hot map[string]bool) {
	if s == nil {
		return
	}
	for name := range s.Stats {
		if !hot[strings.ToLower(name)] {
			delete(s.Stats, name)
		}
	}
}

// ── Token estimation ───────────────────────────────────────────────────

// estimateTokens roughly estimates how many tokens cc will produce.
// Heuristic: 1 token ≈ 4 chars (English) or 1.5 chars (Chinese mixed).
// Use 0.4 chars→tokens multiplier as a middle ground.
//
// Not accurate for billing — accurate for "do we need to compress" gating.
func estimateTokens(cc *CollectedContext) int {
	if cc == nil {
		return 0
	}
	chars := 0
	chars += len(cc.OrigSQL)
	chars += len(cc.ExpandedSQL)
	if cc.Plan != nil {
		chars += CountPlanNodes(cc.Plan.Root) * 100 // ~100 chars per rendered plan node
	}
	if cc.Schema != nil {
		chars += len(cc.Schema.Tables) * 200 // table metadata block
		for _, stats := range cc.Schema.Stats {
			chars += len(stats) * 80 // per-column stat line
		}
	}
	if cc.Trace != nil && cc.Trace.Body != "" {
		chars += len(cc.Trace.Body)
	}
	for _, m := range cc.Memory {
		chars += len(m.Title) + len(m.Content)
	}
	for _, n := range cc.Notes {
		chars += len(n)
	}
	return int(float64(chars) * 0.4)
}

// estimateTokensRaw estimates pre-compression tokens given orig stats.
// Used by Compress to populate EstOrigTokens before mutating cc.
func estimateTokensRaw(sql string, planNodes, schemaTables int) int {
	chars := len(sql) + planNodes*100 + schemaTables*1000 // ~1000 chars per table when full stats
	return int(float64(chars) * 0.4)
}
