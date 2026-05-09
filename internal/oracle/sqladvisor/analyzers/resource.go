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
 *	  internal/oracle/sqladvisor/analyzers/resource.go
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

// NewResource creates a resource analyzer.
func NewResource() *Resource { return &Resource{} }

func (r *Resource) Name() string { return "resource" }

func (r *Resource) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	findings = append(findings, r.checkBufferGetsPerRow(ctx)...)
	findings = append(findings, r.checkLargeSort(ctx)...)
	findings = append(findings, r.checkHighElapsed(ctx)...)
	return findings
}

func (r *Resource) checkBufferGetsPerRow(ctx *sadv.AnalyzeContext) []sadv.Finding {
	avg := ctx.Report.AvgBufferGets
	rows := ctx.Report.AvgRowsProc
	if avg <= 0 || rows <= 0 {
		return nil
	}

	// For aggregate queries (COUNT/SUM/etc), AvgRowsProc=1 is misleading.
	// Use the largest E-Rows from plan tree's table access nodes instead.
	if rows <= 1 && ctx.PlanTree != nil {
		if isAggregate(ctx.PlanTree) {
			maxRows := maxTableAccessRows(ctx.PlanTree)
			if maxRows > 1 {
				rows = maxRows
			}
		}
	}

	ratio := avg / rows

	if ratio > 1000 {
		return []sadv.Finding{{
			Severity: "P1",
			Category: "resource",
			Summary:  fmt.Sprintf("逻辑读/返回行比 = %d:1,严重低效", ratio),
			Detail:   fmt.Sprintf("平均逻辑读 %d, 平均返回行 %d", avg, rows),
			Suggestions: []sadv.Suggestion{{
				Action: "优化访问路径,减少每行逻辑读",
				SQL:    fmt.Sprintf("-- 检查执行计划中高逻辑读的步骤\nSELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('%s', NULL, 'ALLSTATS LAST'));", ctx.Report.SQLID),
				Risk:   "低", Impact: "高 — 降低逻辑读可显著减少CPU和latch竞争",
			}},
		}}
	}
	if ratio > 100 {
		return []sadv.Finding{{
			Severity: "P2",
			Category: "resource",
			Summary:  fmt.Sprintf("逻辑读/返回行比 = %d:1,效率偏低", ratio),
			Detail:   fmt.Sprintf("平均逻辑读 %d, 平均返回行 %d", avg, rows),
			Suggestions: []sadv.Suggestion{{
				Action: "检查是否存在不必要的全表扫描或低效索引访问",
				SQL:    fmt.Sprintf("SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('%s', NULL, 'ALLSTATS LAST'));", ctx.Report.SQLID),
				Risk:   "低", Impact: "中",
			}},
		}}
	}
	return nil
}

func (r *Resource) checkLargeSort(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "SORT" || n.Rows <= 100000 {
			return
		}
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "resource",
			Summary:  fmt.Sprintf("大排序操作(%d行),可能溢出TEMP", n.Rows),
			Detail:   fmt.Sprintf("Plan ID %d: %s %s, 估算 %d 行排序", n.ID, n.Operation, n.Options, n.Rows),
			Suggestions: []sadv.Suggestion{{
				Action: "检查PGA配置,考虑去除不必要的ORDER BY",
				SQL:    "-- 检查PGA配置\nSHOW PARAMETER pga_aggregate_target;\n-- 检查排序溢出\nSELECT * FROM v$sql_workarea WHERE sql_id='" + ctx.Report.SQLID + "';",
				Risk:   "低", Impact: "中 — 避免TEMP溢出可减少磁盘I/O",
			}},
		})
	})
	return findings
}

// isAggregate checks if the plan tree contains a SORT AGGREGATE or HASH GROUP BY at the top.
func isAggregate(node *sadv.PlanNode) bool {
	if node == nil {
		return false
	}
	for _, c := range node.Children {
		if c.Operation == "SORT" && c.Options == "AGGREGATE" {
			return true
		}
		if c.Operation == "HASH" && c.Options == "GROUP BY" {
			return true
		}
	}
	return false
}

// maxTableAccessRows finds the largest E-Rows from TABLE ACCESS nodes in the plan.
func maxTableAccessRows(node *sadv.PlanNode) int64 {
	var maxR int64
	walkTree(node, func(n *sadv.PlanNode) {
		if n.Operation == "TABLE ACCESS" && n.Rows > maxR {
			maxR = n.Rows
		}
		if n.Operation == "INDEX" && n.Rows > maxR {
			maxR = n.Rows
		}
	})
	return maxR
}

func (r *Resource) checkHighElapsed(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.Report.AvgElapsedSec <= 10 {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "resource",
		Summary:  fmt.Sprintf("SQL平均耗时%.1f秒,属于慢SQL", ctx.Report.AvgElapsedSec),
		Detail: fmt.Sprintf("SQL ID %s 平均执行时间 %.3f 秒, 共执行 %d 次",
			ctx.Report.SQLID, ctx.Report.AvgElapsedSec, ctx.Report.ExecCount),
		Suggestions: []sadv.Suggestion{{
			Action: "分析执行计划,优化访问路径和连接方式",
			SQL:    fmt.Sprintf("SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('%s', NULL, 'ALLSTATS LAST'));", ctx.Report.SQLID),
			Risk:   "低", Impact: "高 — 慢SQL优化可直接改善用户体验",
		}},
	}}
}
