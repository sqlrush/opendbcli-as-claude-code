/*-------------------------------------------------------------------------
 *
 * join.go
 *	  Join detects inefficient join strategies.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/join.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Join detects inefficient join strategies.
type Join struct{}

// NewJoin creates a join analyzer.
func NewJoin() *Join { return &Join{} }

func (j *Join) Name() string { return "join" }

func (j *Join) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		findings = append(findings, j.checkNestedLoops(n)...)
		findings = append(findings, j.checkHashJoin(n)...)
		findings = append(findings, j.checkSortMerge(n)...)
	})
	return findings
}

func (j *Join) checkNestedLoops(n *sadv.PlanNode) []sadv.Finding {
	if n.Operation != "NESTED LOOPS" || len(n.Children) == 0 {
		return nil
	}
	driverRows := n.Children[0].Rows
	if driverRows <= 10000 {
		return nil
	}

	var findings []sadv.Finding
	sev, summary := "P2", fmt.Sprintf("NL Join驱动表返回%d行,驱动表行数偏多", driverRows)
	if driverRows > 100000 {
		sev = "P1"
		summary = fmt.Sprintf("NL Join驱动表返回%d行,应改HASH JOIN", driverRows)
	}
	findings = append(findings, sadv.Finding{
		Severity: sev,
		Category: "join",
		Summary:  summary,
		Detail:   fmt.Sprintf("Plan ID %d: NESTED LOOPS, 驱动端估算 %d 行", n.ID, driverRows),
		Suggestions: []sadv.Suggestion{{
			Action: "改用HASH JOIN或收集统计信息让优化器选择更优计划",
			SQL:    "-- 使用HASH JOIN提示\nSELECT /*+ USE_HASH(t) */ ... FROM ...;",
			Risk:   "低", Impact: "高 — 大驱动表下HASH JOIN通常远优于NL",
		}},
	})

	// Check cardinality misestimate if ActualRows available
	driver := n.Children[0]
	if driver.ActualRows != nil && driver.Rows > 0 {
		actual := *driver.ActualRows
		ratio := float64(actual) / float64(driver.Rows)
		if ratio > 10 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "join",
				Summary:  fmt.Sprintf("NL Join驱动表实际行数是估算的%.0f倍", ratio),
				Detail: fmt.Sprintf("Plan ID %d: 估算 %d 行, 实际 %d 行",
					driver.ID, driver.Rows, actual),
				Suggestions: []sadv.Suggestion{{
					Action: "收集直方图统计信息以修正基数估算",
					SQL:    "EXEC DBMS_STATS.GATHER_TABLE_STATS(ownname=>USER, tabname=>'TABLE', method_opt=>'FOR ALL COLUMNS SIZE AUTO');",
					Risk:   "低", Impact: "高 — 准确的基数估算可帮助优化器选择正确的JOIN方式",
				}},
			})
		}
	}
	return findings
}

func (j *Join) checkHashJoin(n *sadv.PlanNode) []sadv.Finding {
	if n.Operation != "HASH JOIN" || len(n.Children) == 0 {
		return nil
	}
	buildRows := n.Children[0].Rows
	if buildRows <= 1000000 {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "join",
		Summary:  fmt.Sprintf("HASH JOIN构建端行数多(%d行),可能溢出TEMP", buildRows),
		Detail:   fmt.Sprintf("Plan ID %d: HASH JOIN 构建端估算 %d 行", n.ID, buildRows),
		Suggestions: []sadv.Suggestion{{
			Action: "增大PGA或交换JOIN输入端以减少构建端大小",
			SQL:    "-- 检查PGA配置\nSHOW PARAMETER pga_aggregate_target;\n-- 交换JOIN输入: SELECT /*+ SWAP_JOIN_INPUTS(t) */ ...;",
			Risk:   "中", Impact: "中 — 避免TEMP表空间溢出",
		}},
	}}
}

func (j *Join) checkSortMerge(n *sadv.PlanNode) []sadv.Finding {
	if n.Operation != "SORT" || n.Options != "JOIN" {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P3",
		Category: "join",
		Summary:  "SORT MERGE JOIN通常不如HASH JOIN",
		Detail:   fmt.Sprintf("Plan ID %d: SORT JOIN, 估算行数 %d", n.ID, n.Rows),
		Suggestions: []sadv.Suggestion{{
			Action: "对等值连接考虑使用HASH JOIN替代",
			SQL:    "-- 使用HASH JOIN提示\nSELECT /*+ USE_HASH(t1 t2) */ ... FROM t1 JOIN t2 ON ...;",
			Risk:   "低", Impact: "低",
		}},
	}}
}
