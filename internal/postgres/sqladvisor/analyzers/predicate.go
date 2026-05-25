/*-------------------------------------------------------------------------
 *
 * predicate.go
 *	  Predicate detects filter conditions that prevent index usage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/analyzers/predicate.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Predicate detects filter conditions that prevent index usage.
type Predicate struct{}

func NewPredicate() *Predicate   { return &Predicate{} }
func (p *Predicate) Name() string { return "predicate" }

func (p *Predicate) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}
	var findings []sadv.Finding
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		findings = append(findings, p.checkImplicitCast(n)...)
		findings = append(findings, p.checkFunctionOnColumn(n)...)
		findings = append(findings, p.checkLeadingWildcard(n)...)
	})
	return findings
}

func (p *Predicate) checkImplicitCast(n *sadv.PlanNode) []sadv.Finding {
	if n.FilterPred == "" {
		return nil
	}
	pred := strings.ToLower(n.FilterPred)

	// PG implicit cast patterns: ::type notation in filter predicates
	if strings.Contains(pred, "::text") || strings.Contains(pred, "::integer") ||
		strings.Contains(pred, "::numeric") || strings.Contains(pred, "::bigint") ||
		strings.Contains(pred, "::varchar") {
		return []sadv.Finding{{
			Severity: "P1",
			Category: "predicate",
			Summary:  fmt.Sprintf("隐式类型转换: %s", n.ObjectName),
			Detail:   fmt.Sprintf("过滤条件包含类型转换 (%s)，可能导致索引无法使用", truncate(n.FilterPred, 80)),
			Suggestions: []sadv.Suggestion{
				{
					Action: "确保 WHERE 条件中的数据类型与列类型一致",
					SQL:    "-- 例: WHERE varchar_col = 123 应改为 WHERE varchar_col = '123'",
					Impact: "消除类型转换后可使用索引",
				},
			},
		}}
	}
	return nil
}

func (p *Predicate) checkFunctionOnColumn(n *sadv.PlanNode) []sadv.Finding {
	if n.FilterPred == "" {
		return nil
	}
	pred := strings.ToLower(n.FilterPred)

	// PG common functions that prevent index usage
	functions := []string{
		"date(", "extract(", "date_trunc(", "to_char(",
		"upper(", "lower(", "trim(", "substr(", "substring(",
		"left(", "right(", "coalesce(",
	}
	for _, fn := range functions {
		if strings.Contains(pred, fn) {
			return []sadv.Finding{{
				Severity: "P2",
				Category: "predicate",
				Summary:  fmt.Sprintf("函数导致索引失效: %s", n.ObjectName),
				Detail:   fmt.Sprintf("过滤条件对列使用函数 (%s)，导致 B-tree 索引无法使用", truncate(n.FilterPred, 80)),
				Suggestions: []sadv.Suggestion{
					{
						Action: "改写为范围查询或创建表达式索引",
						SQL:    "-- CREATE INDEX idx_expr ON table (lower(col))\n-- 或改写: WHERE date_trunc('day', ts) = '2026-01-01' 改为 WHERE ts >= '2026-01-01' AND ts < '2026-01-02'",
						Impact: "允许优化器使用 B-tree 索引或表达式索引",
					},
				},
			}}
		}
	}
	return nil
}

func (p *Predicate) checkLeadingWildcard(n *sadv.PlanNode) []sadv.Finding {
	if n.FilterPred == "" {
		return nil
	}
	pred := strings.ToLower(n.FilterPred)
	if strings.Contains(pred, "like '%") || strings.Contains(pred, "~~") {
		return []sadv.Finding{{
			Severity: "P2",
			Category: "predicate",
			Summary:  fmt.Sprintf("前模糊匹配无法使用索引: %s", n.ObjectName),
			Detail:   "LIKE '%xxx%' 前模糊匹配无法使用 B-tree 索引，只能全表扫描",
			Suggestions: []sadv.Suggestion{
				{
					Action: "考虑使用 pg_trgm 扩展的 GIN 索引",
					SQL:    fmt.Sprintf("-- CREATE EXTENSION IF NOT EXISTS pg_trgm;\n-- CREATE INDEX idx_%s_trgm ON %s USING gin (col gin_trgm_ops)", n.ObjectName, n.ObjectName),
					Risk:   "GIN 索引占用额外存储且写入较慢",
					Impact: "前模糊匹配可使用 GIN 索引",
				},
			},
		}}
	}
	return nil
}
