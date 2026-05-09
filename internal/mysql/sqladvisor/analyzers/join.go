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
 *	  internal/mysql/sqladvisor/analyzers/join.go
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

func NewJoin() *Join       { return &Join{} }
func (j *Join) Name() string { return "join" }

func (j *Join) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}

	var findings []sadv.Finding

	// Count table access nodes — if > 1, there's a join
	var tableNodes []*sadv.PlanNode
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.Operation == "TABLE ACCESS" || n.Operation == "INDEX" {
			tableNodes = append(tableNodes, n)
		}
	})

	if len(tableNodes) < 2 {
		return nil
	}

	// Check for full scan on driven tables (all except first)
	for i := 1; i < len(tableNodes); i++ {
		n := tableNodes[i]
		if n.Options == "FULL" && n.Rows > 1000 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "join",
				Summary:  fmt.Sprintf("JOIN 被驱动表 %s 全表扫描 (%d 行)", n.ObjectName, n.Rows),
				Detail:   fmt.Sprintf("被驱动表 %s 在 JOIN 中走全表扫描，每次循环都要扫描 %d 行", n.ObjectName, n.Rows),
				Suggestions: []sadv.Suggestion{
					{
						Action: fmt.Sprintf("为 JOIN 条件列添加索引: %s", n.ObjectName),
						SQL:    fmt.Sprintf("-- ALTER TABLE %s ADD INDEX idx_join_col (join_column)", n.ObjectName),
						Risk:   "创建索引可能短暂锁表",
						Impact: "将全表扫描变为索引查找，大幅提升 JOIN 性能",
					},
				},
			})
		}
	}

	// Check for too many tables in join
	if len(tableNodes) > 5 {
		findings = append(findings, sadv.Finding{
			Severity: "P2",
			Category: "join",
			Summary:  fmt.Sprintf("JOIN 涉及 %d 张表，过多", len(tableNodes)),
			Detail:   "MySQL 优化器在超过 5 张表 JOIN 时可能选错执行计划",
			Suggestions: []sadv.Suggestion{
				{
					Action: "考虑拆分为多个小 JOIN 或使用临时表",
					Risk:   "需要改写 SQL",
				},
			},
		})
	}

	// Check for Cartesian product (nested loop with no join condition)
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if strings.Contains(strings.ToUpper(n.Options), "CARTESIAN") ||
			(n.Operation == "TABLE ACCESS" && n.Options == "FULL" && n.FilterPred == "" && n.AccessPred == "") {
			if n.Rows > 10000 {
				findings = append(findings, sadv.Finding{
					Severity: "P1",
					Category: "join",
					Summary:  fmt.Sprintf("疑似笛卡尔积: %s", n.ObjectName),
					Detail:   "表访问无过滤条件且行数大，可能缺少 JOIN 条件",
					Suggestions: []sadv.Suggestion{
						{Action: "检查 SQL 是否缺少 JOIN ON 条件"},
					},
				})
			}
		}
	})

	return findings
}
