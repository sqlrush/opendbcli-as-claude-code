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
 *	  internal/mysql/sqladvisor/analyzers/predicate.go
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

func NewPredicate() *Predicate     { return &Predicate{} }
func (p *Predicate) Name() string { return "predicate" }

func (p *Predicate) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}
	var findings []sadv.Finding
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		findings = append(findings, p.checkImplicitConversion(n)...)
		findings = append(findings, p.checkFunctionOnColumn(n)...)
		findings = append(findings, p.checkLeadingWildcard(n)...)
	})
	return findings
}

func (p *Predicate) checkImplicitConversion(n *sadv.PlanNode) []sadv.Finding {
	if n.FilterPred == "" {
		return nil
	}
	pred := strings.ToLower(n.FilterPred)

	// MySQL implicit conversion patterns: cast(), convert(), or comparing varchar to number
	if strings.Contains(pred, "cast(") || strings.Contains(pred, "convert(") {
		return []sadv.Finding{{
			Severity: "P1",
			Category: "predicate",
			Summary:  fmt.Sprintf("隐式类型转换: %s", n.ObjectName),
			Detail:   fmt.Sprintf("过滤条件包含类型转换 (%s)，导致索引无法使用", truncate(n.FilterPred, 80)),
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

	functions := []string{"date(", "year(", "month(", "day(", "upper(", "lower(", "trim(", "substr(", "left(", "right("}
	for _, fn := range functions {
		if strings.Contains(pred, fn) {
			return []sadv.Finding{{
				Severity: "P2",
				Category: "predicate",
				Summary:  fmt.Sprintf("函数导致索引失效: %s", n.ObjectName),
				Detail:   fmt.Sprintf("过滤条件对列使用函数 (%s)，导致 B-tree 索引无法使用", truncate(n.FilterPred, 80)),
				Suggestions: []sadv.Suggestion{
					{
						Action: "改写为范围查询或创建函数索引（MySQL 8.0+）",
						SQL:    "-- 例: WHERE DATE(created_at) = '2026-01-01' 改为 WHERE created_at >= '2026-01-01' AND created_at < '2026-01-02'",
						Impact: "允许优化器使用 B-tree 索引",
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
	if strings.Contains(pred, "like '%") || strings.Contains(pred, "like \"%") {
		return []sadv.Finding{{
			Severity: "P2",
			Category: "predicate",
			Summary:  fmt.Sprintf("前模糊匹配无法使用索引: %s", n.ObjectName),
			Detail:   "LIKE '%xxx%' 前模糊匹配无法使用 B-tree 索引，只能全表扫描",
			Suggestions: []sadv.Suggestion{
				{
					Action: "考虑使用 FULLTEXT INDEX 或外部搜索引擎",
					SQL:    fmt.Sprintf("-- ALTER TABLE %s ADD FULLTEXT INDEX ft_xxx (col)", n.ObjectName),
					Risk:   "FULLTEXT INDEX 占用额外存储",
					Impact: "前模糊匹配可使用全文索引",
				},
			},
		}}
	}
	return nil
}
