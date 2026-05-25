/*-------------------------------------------------------------------------
 *
 * rewrite.go
 *	  Rewrite detects SQL patterns that can be improved by rewriting.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/analyzers/rewrite.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Rewrite detects SQL patterns that can be improved by rewriting.
type Rewrite struct{}

func NewRewrite() *Rewrite     { return &Rewrite{} }
func (r *Rewrite) Name() string { return "rewrite" }

func (r *Rewrite) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	sqlText := strings.ToUpper(ctx.Report.SQLText)

	findings = append(findings, r.checkSelectStar(sqlText)...)
	findings = append(findings, r.checkNotIn(sqlText)...)
	findings = append(findings, r.checkLargeOffset(sqlText)...)
	findings = append(findings, r.checkDependentSubquery(ctx)...)
	findings = append(findings, r.checkOrderByWithoutLimit(sqlText, ctx)...)

	return findings
}

func (r *Rewrite) checkSelectStar(sqlText string) []sadv.Finding {
	if !strings.Contains(sqlText, "SELECT *") {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P3",
		Category: "rewrite",
		Summary:  "使用 SELECT * 返回所有列",
		Detail:   "SELECT * 可能导致不必要的回表和网络传输",
		Suggestions: []sadv.Suggestion{
			{Action: "只 SELECT 需要的列，减少回表和网络开销，也有利于 Index Only Scan"},
		},
	}}
}

func (r *Rewrite) checkNotIn(sqlText string) []sadv.Finding {
	if !strings.Contains(sqlText, "NOT IN") || !strings.Contains(sqlText, "SELECT") {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "rewrite",
		Summary:  "NOT IN 子查询可能性能差",
		Detail:   "NOT IN 遇到 NULL 值时语义与预期不同，且可能无法优化为 Anti Join",
		Suggestions: []sadv.Suggestion{
			{
				Action: "改写为 NOT EXISTS 或 LEFT JOIN ... IS NULL",
				SQL:    "-- WHERE id NOT IN (SELECT ...) 改为 WHERE NOT EXISTS (SELECT 1 FROM ... WHERE ...)",
				Impact: "避免 NULL 值陷阱，优化器可选择更好的执行计划",
			},
		},
	}}
}

func (r *Rewrite) checkLargeOffset(sqlText string) []sadv.Finding {
	if !strings.Contains(sqlText, "OFFSET") {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "rewrite",
		Summary:  "使用 OFFSET 分页可能性能差",
		Detail:   "大 OFFSET 值会导致数据库扫描并丢弃大量行",
		Suggestions: []sadv.Suggestion{
			{
				Action: "改用 keyset pagination（基于游标分页）",
				SQL:    "-- WHERE id > $last_id ORDER BY id LIMIT 20  替代  OFFSET 10000 LIMIT 20",
				Impact: "keyset 分页性能稳定，不随页码增长而退化",
			},
		},
	}}
}

func (r *Rewrite) checkDependentSubquery(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}

	var findings []sadv.Finding
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation == "SUBQUERY" && n.Options == "DEPENDENT" {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "rewrite",
				Summary:  "相关子查询（DEPENDENT SUBQUERY）",
				Detail:   "相关子查询对外层每一行都执行一次，N+1 问题",
				Suggestions: []sadv.Suggestion{
					{
						Action: "改写为 JOIN 或 LATERAL JOIN",
						SQL:    "-- SELECT *, (SELECT ... FROM t2 WHERE t2.id = t1.id) 改为 SELECT * FROM t1 LEFT JOIN t2 ON ...",
						Impact: "消除 N+1 问题，大幅提升性能",
					},
				},
			})
		}
	})

	return findings
}

func (r *Rewrite) checkOrderByWithoutLimit(sqlText string, ctx *sadv.AnalyzeContext) []sadv.Finding {
	if !strings.Contains(sqlText, "ORDER BY") || strings.Contains(sqlText, "LIMIT") {
		return nil
	}
	if ctx.PlanTree == nil {
		return nil
	}

	var findings []sadv.Finding
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation == "SORT" && n.Rows > 10000 {
			findings = append(findings, sadv.Finding{
				Severity: "P3",
				Category: "rewrite",
				Summary:  fmt.Sprintf("大结果集排序: %d 行", n.Rows),
				Detail:   "ORDER BY 对大结果集排序但未使用 LIMIT，检查是否需要排序",
				Suggestions: []sadv.Suggestion{
					{Action: "如果不需要全排序，添加 LIMIT 或移除 ORDER BY"},
				},
			})
		}
	})
	return findings
}
