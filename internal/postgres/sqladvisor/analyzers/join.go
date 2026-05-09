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
 *	  internal/postgres/sqladvisor/analyzers/join.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Join detects inefficient join strategies.
type Join struct{}

func NewJoin() *Join         { return &Join{} }
func (j *Join) Name() string { return "join" }

func (j *Join) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}

	var findings []sadv.Finding

	var tableNodes []*sadv.PlanNode
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation == "TABLE ACCESS" || n.Operation == "INDEX" {
			tableNodes = append(tableNodes, n)
		}
	})

	if len(tableNodes) < 2 {
		return nil
	}

	// Check for Nested Loop with large inner table (full scan on driven table)
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "NESTED LOOPS" {
			return
		}
		if len(n.Children) < 2 {
			return
		}
		inner := n.Children[len(n.Children)-1]
		if inner.Operation == "TABLE ACCESS" && inner.Options == "FULL" && inner.Rows > 1000 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "join",
				Summary:  fmt.Sprintf("Nested Loop 内表 %s Seq Scan (%d 行)", inner.ObjectName, inner.Rows),
				Detail:   fmt.Sprintf("Nested Loop 内表 %s 走全表扫描，每次循环扫描 %d 行", inner.ObjectName, inner.Rows),
				Suggestions: []sadv.Suggestion{
					{
						Action: fmt.Sprintf("为 JOIN 条件列添加索引: %s", inner.ObjectName),
						SQL:    fmt.Sprintf("-- CREATE INDEX idx_%s_join ON %s (join_column)", inner.ObjectName, inner.ObjectName),
						Risk:   "创建索引可能影响写入性能",
						Impact: "将 Seq Scan 变为 Index Scan，大幅提升 JOIN 性能",
					},
				},
			})
		}
	})

	// Check for Hash Join spill (high cost may indicate batches > 1)
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation != "HASH JOIN" {
			return
		}
		if n.Cost > 100000 {
			findings = append(findings, sadv.Finding{
				Severity: "P2",
				Category: "join",
				Summary:  fmt.Sprintf("Hash Join 代价高 (cost=%d)", n.Cost),
				Detail:   "Hash Join 代价较高，可能 work_mem 不足导致溢出到磁盘",
				Suggestions: []sadv.Suggestion{
					{
						Action: "增大 work_mem 或在会话级别临时调整",
						SQL:    "SET work_mem = '256MB';",
						Risk:   "增大 work_mem 会增加每个排序/哈希操作的内存使用",
						Impact: "减少 Hash Join 批次数，避免磁盘溢出",
					},
				},
			})
		}
	})

	// Check for too many tables in join
	if len(tableNodes) > 5 {
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "join",
			Summary:  fmt.Sprintf("JOIN 涉及 %d 张表，过多", len(tableNodes)),
			Detail:   "多表 JOIN 时优化器搜索空间指数增长，可能选错执行计划",
			Suggestions: []sadv.Suggestion{
				{
					Action: "考虑拆分为多个小 JOIN 或使用 CTE",
					Risk:   "需要改写 SQL",
				},
			},
		})
	}

	// Check for Cartesian product indicators
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if strings.Contains(strings.ToUpper(n.Options), "CARTESIAN") {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "join",
				Summary:  fmt.Sprintf("笛卡尔积: %s", n.ObjectName),
				Detail:   "JOIN 缺少连接条件导致笛卡尔积",
				Suggestions: []sadv.Suggestion{
					{Action: "检查 SQL 是否缺少 JOIN ON 条件"},
				},
			})
		}
	})

	return findings
}
