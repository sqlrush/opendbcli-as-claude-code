/*-------------------------------------------------------------------------
 *
 * rewrite.go
 *	  Rewrite detects SQL text and plan patterns that suggest query
 *	  rewriting.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/rewrite.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Rewrite detects SQL text and plan patterns that suggest query rewriting.
type Rewrite struct{}

// NewRewrite creates a rewrite analyzer.
func NewRewrite() *Rewrite { return &Rewrite{} }

func (r *Rewrite) Name() string { return "rewrite" }

func (r *Rewrite) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	findings = append(findings, r.checkSelectStar(ctx)...)
	findings = append(findings, r.checkUnnecessaryOrderBy(ctx)...)
	findings = append(findings, r.checkPartitionPruning(ctx)...)
	findings = append(findings, r.checkViewNotMerged(ctx)...)
	return findings
}

func (r *Rewrite) checkSelectStar(ctx *sadv.AnalyzeContext) []sadv.Finding {
	text := strings.TrimSpace(ctx.Report.SQLText)
	if !strings.HasPrefix(strings.ToUpper(text), "SELECT *") {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P3",
		Category: "rewrite",
		Summary:  "使用SELECT *,建议指定需要的列",
		Detail:   "SELECT * 会读取所有列,增加网络传输和内存消耗",
		Suggestions: []sadv.Suggestion{{
			Action: "将SELECT *替换为具体需要的列名",
			SQL:    "-- 示例: SELECT col1, col2, col3 FROM table WHERE ...;",
			Risk:   "低", Impact: "低 — 减少不必要的列读取",
		}},
	}}
}

func (r *Rewrite) checkUnnecessaryOrderBy(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "SORT" || n.Options != "ORDER BY" {
			return
		}
		if ctx.Report.AvgRowsProc <= 10000 {
			return
		}
		findings = append(findings, sadv.Finding{
			Severity: "P3",
			Category: "rewrite",
			Summary:  fmt.Sprintf("排序%d行,确认ORDER BY是否必要", ctx.Report.AvgRowsProc),
			Detail:   fmt.Sprintf("Plan ID %d: SORT ORDER BY, 返回 %d 行", n.ID, ctx.Report.AvgRowsProc),
			Suggestions: []sadv.Suggestion{{
				Action: "如果应用不需要排序结果,去掉ORDER BY子句",
				SQL:    "-- 去掉ORDER BY后SQL执行无需排序,减少CPU和TEMP使用",
				Risk:   "低 — 需确认应用是否依赖排序",
				Impact: "中 — 大数据量排序消耗大量资源",
			}},
		})
	})
	return findings
}

func (r *Rewrite) checkPartitionPruning(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "PARTITION RANGE" || n.Options != "ALL" {
			return
		}
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "rewrite",
			Summary:  "分区裁剪未生效,扫描所有分区",
			Detail:   fmt.Sprintf("Plan ID %d: PARTITION RANGE ALL", n.ID),
			Suggestions: []sadv.Suggestion{{
				Action: "WHERE条件增加分区键以启用分区裁剪",
				SQL:    "-- 查看分区键\nSELECT partition_name, high_value FROM dba_tab_partitions WHERE table_name='TABLE' ORDER BY partition_position;",
				Risk:   "低", Impact: "高 — 分区裁剪可大幅减少扫描数据量",
			}},
		})
	})
	return findings
}

func (r *Rewrite) checkViewNotMerged(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "VIEW" {
			return
		}
		findings = append(findings, sadv.Finding{
			Severity: "P3",
			Category: "rewrite",
			Summary:  "视图未合并(VIEW操作),可能影响优化器选择",
			Detail:   fmt.Sprintf("Plan ID %d: VIEW %s", n.ID, n.ObjectName),
			Suggestions: []sadv.Suggestion{{
				Action: "考虑使用MERGE提示或改写为内联子查询",
				SQL:    fmt.Sprintf("-- 尝试合并视图\nSELECT /*+ MERGE(%s) */ ... FROM ...;", n.ObjectName),
				Risk:   "中 — 视图合并可能改变结果集语义",
				Impact: "中 — 合并后优化器可选择更优的连接和过滤顺序",
			}},
		})
	})
	return findings
}
