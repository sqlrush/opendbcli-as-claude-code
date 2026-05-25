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
 *	  internal/mysql/sqladvisor/analyzers/rewrite.go
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

	// Check SELECT *
	if strings.Contains(sqlText, "SELECT *") || strings.Contains(sqlText, "SELECT `") {
		findings = append(findings, sadv.Finding{
			Severity: "P3",
			Category: "rewrite",
			Summary:  "使用 SELECT * 返回所有列",
			Detail:   "SELECT * 可能导致不必要的回表和网络传输",
			Suggestions: []sadv.Suggestion{
				{Action: "只 SELECT 需要的列，减少回表和网络开销"},
			},
		})
	}

	// Check unnecessary ORDER BY with LIMIT
	if strings.Contains(sqlText, "ORDER BY") && !strings.Contains(sqlText, "LIMIT") {
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
	}

	// Check NOT IN with subquery
	if strings.Contains(sqlText, "NOT IN") && strings.Contains(sqlText, "SELECT") {
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "rewrite",
			Summary:  "NOT IN 子查询可能性能差",
			Detail:   "NOT IN 遇到 NULL 值时可能退化为全量比较，且 MySQL 可能无法优化为 Anti Join",
			Suggestions: []sadv.Suggestion{
				{
					Action: "改写为 NOT EXISTS 或 LEFT JOIN ... IS NULL",
					SQL:    "-- WHERE id NOT IN (SELECT ...) 改为 WHERE NOT EXISTS (SELECT 1 FROM ... WHERE ...)",
					Impact: "避免 NULL 值陷阱，优化器可选择更好的执行计划",
				},
			},
		})
	}

	// Check dependent subquery
	if ctx.PlanTree != nil {
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
	}

	return findings
}
