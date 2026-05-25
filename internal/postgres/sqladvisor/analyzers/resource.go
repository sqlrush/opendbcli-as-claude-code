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
 *	  internal/postgres/sqladvisor/analyzers/resource.go
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

func NewResource() *Resource   { return &Resource{} }
func (r *Resource) Name() string { return "resource" }

func (r *Resource) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	report := ctx.Report
	var findings []sadv.Finding

	// Check buffer gets per row returned ratio
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
				Summary:  fmt.Sprintf("缓冲区效率极低: 每返回 1 行读取 %.0f 块", ratio),
				Detail:   fmt.Sprintf("平均每次执行读取 %d 块返回 %d 行（比值 %.0f:1），大量无效 IO", report.AvgBufferGets, report.AvgRowsProc, ratio),
				Suggestions: []sadv.Suggestion{
					{
						Action: "优化索引减少无效 IO",
						SQL:    fmt.Sprintf("EXPLAIN (ANALYZE, BUFFERS) %s", truncateSQL(report.SQLText, 200)),
						Risk:   "EXPLAIN ANALYZE 会实际执行 SQL",
						Impact: "减少 IO 和 CPU 消耗",
					},
				},
			})
		} else if ratio > 100 {
			findings = append(findings, sadv.Finding{
				Severity: "P2",
				Category: "resource",
				Summary:  fmt.Sprintf("缓冲区效率偏低: 每返回 1 行读取 %.0f 块", ratio),
				Detail:   fmt.Sprintf("缓冲区读取与返回行数比值 %.0f:1", ratio),
				Suggestions: []sadv.Suggestion{
					{Action: "检查是否可通过索引优化减少缓冲区读取"},
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

	// Check sort operations with high cost
	if ctx.PlanTree != nil {
		WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
			if n.Operation == "SORT" && n.Cost > 10000 {
				findings = append(findings, sadv.Finding{
					Severity: "P2",
					Category: "resource",
					Summary:  fmt.Sprintf("排序代价高 (cost=%d)", n.Cost),
					Detail:   "排序代价较高，可能排序溢出到磁盘",
					Suggestions: []sadv.Suggestion{
						{
							Action: "考虑为 ORDER BY 列添加索引，或增大 work_mem",
							SQL:    "SET work_mem = '256MB';",
							Risk:   "需要评估写入影响和内存使用",
						},
					},
				})
			}
		})
	}

	// Check temp file usage indicator (high disk reads may imply temp files)
	if report.AvgDiskReads > 10000 {
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "resource",
			Summary:  fmt.Sprintf("磁盘读取量大: 平均 %d 块", report.AvgDiskReads),
			Detail:   "磁盘读取量大，可能缓冲区命中率低或排序/哈希溢出到磁盘",
			Suggestions: []sadv.Suggestion{
				{
					Action: "检查 shared_buffers 和 effective_cache_size 配置",
					SQL:    "SHOW shared_buffers; SHOW effective_cache_size;",
					Impact: "增大缓冲区可减少磁盘 IO",
				},
			},
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
