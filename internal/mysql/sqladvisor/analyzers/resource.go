/*-------------------------------------------------------------------------
 *
 * resource.go
 *	  Resource detects high resource consumption patterns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqladvisor/analyzers/resource.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Resource detects high resource consumption patterns.
type Resource struct{}

func NewResource() *Resource     { return &Resource{} }
func (r *Resource) Name() string { return "resource" }

func (r *Resource) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	report := ctx.Report
	var findings []sadv.Finding

	// Check rows examined per row returned ratio
	if report.AvgRowsProc > 0 && report.AvgBufferGets > 0 {
		ratio := float64(report.AvgBufferGets) / float64(report.AvgRowsProc)

		if ctx.PlanTree != nil && IsAggregate(ctx.PlanTree) {
			maxRows := MaxTableAccessRows(ctx.PlanTree)
			if maxRows > 0 {
				ratio = float64(report.AvgBufferGets) / float64(maxRows)
			}
		}

		if ratio > 1000 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "resource",
				Summary:  fmt.Sprintf("行扫描效率极低: 每返回 1 行扫描 %.0f ��", ratio),
				Detail:   fmt.Sprintf("平均每次执行扫描 %d 行返回 %d 行（比值 %.0f:1），大量无效扫描", report.AvgBufferGets, report.AvgRowsProc, ratio),
				Suggestions: []sadv.Suggestion{
					{
						Action: "优化索引减少无效扫描",
						SQL:    fmt.Sprintf("EXPLAIN FORMAT=TREE %s", truncateSQL(report.SQLText, 200)),
						Risk:   "无",
						Impact: "减少 IO 和 CPU 消耗",
					},
				},
			})
		} else if ratio > 100 {
			findings = append(findings, sadv.Finding{
				Severity: "P2",
				Category: "resource",
				Summary:  fmt.Sprintf("行扫描效率偏低: 每返回 1 行扫描 %.0f 行", ratio),
				Detail:   fmt.Sprintf("扫描行数与返回行数比值 %.0f:1", ratio),
				Suggestions: []sadv.Suggestion{
					{Action: "检查是否可通过索引优化减少扫描行数"},
				},
			})
		}
	}

	// Check high elapsed time
	if report.AvgElapsedSec > 10 {
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "resource",
			Summary:  fmt.Sprintf("SQL 平均执行 %.1f 秒", report.AvgElapsedSec),
			Detail:   fmt.Sprintf("SQL 平均执行时间 %.1f 秒，共执行 %d 次", report.AvgElapsedSec, report.ExecCount),
			Suggestions: []sadv.Suggestion{
				{Action: "分析执行计划寻找优化点"},
			},
		})
	}

	// Check sort operations in plan tree
	if ctx.PlanTree != nil {
		WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
			if n.Operation == "SORT" && n.Options == "FILESORT" {
				findings = append(findings, sadv.Finding{
					Severity: "P2",
					Category: "resource",
					Summary:  "使用 filesort 排序",
					Detail:   "查询使用 filesort 而非索引排序，可能导致磁盘临时文件",
					Suggestions: []sadv.Suggestion{
						{
							Action: "考虑为 ORDER BY 列添加索引",
							Risk:   "需要评估写入影响",
						},
					},
				})
			}
		})
	}

	return findings
}

func truncateSQL(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
