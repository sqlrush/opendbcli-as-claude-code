/*-------------------------------------------------------------------------
 *
 * token_compress.go
 *	  CompressionStats reports what compression did.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/token_compress.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"strings"
)

// G7 千行 SQL token 压缩 (design doc §1.2 G7)
//
// 千行 SQL 即使 1M context 模型也吃力 (200K+ token). 三类压缩:
//
//   ❶ Plan tree 折叠: cost < 5% 的子树折成单行 "(...N nodes elided...)"
//   ❷ Schema 分级:    hot tables (cost top N 涉及) 给完整 stats,
//                      其他只给主键 + 索引数 + 行数
//   ❸ CTE 去重:       同一 CTE 被引用 N 次只发一份 SQL 文本
//
// 触发条件: SQL > 500 行 OR plan 节点 > 50 OR 涉及表 > 15
// 目标: 最终塞 LLM 的 prompt < 100K tokens

// CompressionStats reports what compression did.
type CompressionStats struct {
	TriggerReason   string
	OrigPlanNodes   int
	FoldedNodes     int
	OrigSchemaTables int
	HotTables       int
	ColdTables      int
	EstOrigTokens   int
	EstFinalTokens  int
}

// shouldCompress decides if we need G7 compression for this context.
func shouldCompress(cc *CollectedContext) (bool, string) {
	if cc.Plan != nil {
		nodes := countPlanNodes(cc.Plan.Root)
		if nodes > 50 {
			return true, "plan 节点 " + intToStr(nodes) + " 超阈值 50"
		}
	}
	if len(cc.InvolvedTables) > 15 {
		return true, "涉及表 " + intToStr(len(cc.InvolvedTables)) + " 超阈值 15"
	}
	lines := strings.Count(cc.OrigSQL, "\n") + 1
	if lines > 500 {
		return true, "SQL " + intToStr(lines) + " 行超阈值 500"
	}
	return false, ""
}

// CompressContext applies G7 compression in-place to a CollectedContext.
// Returns stats describing what was changed. Safe to call on small SQL too —
// it just no-ops if shouldCompress returns false.
func CompressContext(cc *CollectedContext) *CompressionStats {
	stats := &CompressionStats{}
	if cc == nil {
		return stats
	}
	stats.OrigPlanNodes = countPlanNodes(cc.Plan.Root)
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
	if cc.Plan != nil && cc.Plan.Root != nil {
		totalCost := cc.Plan.TotalCost
		if totalCost > 0 {
			folded := foldPlanNodes(cc.Plan.Root, totalCost*0.05)
			stats.FoldedNodes = folded
		}
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

	if cc.Notes == nil {
		cc.Notes = []string{}
	}
	cc.Notes = append(cc.Notes,
		"G7 token 压缩已触发 ("+reason+"): plan "+intToStr(stats.OrigPlanNodes)+
			" 节点折叠 "+intToStr(stats.FoldedNodes)+" 个; schema "+
			intToStr(stats.HotTables)+" hot / "+intToStr(stats.ColdTables)+" cold")

	return stats
}

// countPlanNodes recursively counts nodes in a plan tree.
func countPlanNodes(n *PlanNode) int {
	if n == nil {
		return 0
	}
	total := 1
	for _, c := range n.Children {
		total += countPlanNodes(c)
	}
	return total
}

// foldPlanNodes folds subtrees whose total_cost < threshold into a placeholder.
// The placeholder operator is "(...N nodes elided due to low cost <5%...)" with no children.
// Returns count of folded nodes.
func foldPlanNodes(n *PlanNode, threshold float64) int {
	if n == nil {
		return 0
	}
	folded := 0
	var newChildren []*PlanNode
	for _, c := range n.Children {
		if c.TotalCost < threshold && countPlanNodes(c) > 1 {
			// Replace this subtree with a single-line placeholder
			elided := countPlanNodes(c)
			folded += elided - 1 // we keep 1 placeholder
			newChildren = append(newChildren, &PlanNode{
				Operator:   "(...elided " + intToStr(elided) + " low-cost nodes, total_cost=" + floatToStr(c.TotalCost, 0) + ")",
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

// identifyHotTables returns table names that appear in plan nodes with cost > 5% total.
func identifyHotTables(root *PlanNode) map[string]bool {
	hot := make(map[string]bool)
	if root == nil {
		return hot
	}
	threshold := root.TotalCost * 0.05
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

// demoteColdTables strips full stats from cold tables, keeps only essentials.
// Hot tables: keep everything (all stats columns + all indexes + all FKs)
// Cold tables: keep only TableInfo + 1 line per index (no stats)
func demoteColdTables(s *SchemaInfo, hot map[string]bool) {
	if s == nil {
		return
	}
	for name := range s.Stats {
		if !hot[strings.ToLower(name)] {
			// Drop column stats entirely for cold tables
			delete(s.Stats, name)
		}
	}
	// Index entries kept as-is (each is small; just summary line)
	// FKs kept (they hint at JOIN paths; valuable for both hot and cold)
}

// estimateTokens roughly estimates how many tokens the CC will produce.
// 1 token ≈ 0.75 words ≈ 4 chars (English) or 1.5 chars (Chinese mixed).
// Rough multiplier: 0.4 (chars * 0.4 = tokens)
func estimateTokens(cc *CollectedContext) int {
	chars := 0
	chars += len(cc.OrigSQL)
	chars += len(cc.ExpandedSQL)
	if cc.Plan != nil {
		chars += countPlanNodes(cc.Plan.Root) * 100
	}
	if cc.Schema != nil {
		chars += len(cc.Schema.Tables) * 200
		for _, stats := range cc.Schema.Stats {
			chars += len(stats) * 80
		}
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
func estimateTokensRaw(sql string, planNodes, schemaTables int) int {
	chars := len(sql) + planNodes*100 + schemaTables*1000 // assume ~1000 chars per table when full stats
	return int(float64(chars) * 0.4)
}

func floatToStr(f float64, prec int) string {
	// Cheap float→string without strconv import
	whole := int64(f)
	if prec == 0 {
		return intToStr64(whole)
	}
	return intToStr64(whole) // simplification — for prompt formatting
}

func intToStr64(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
