/*-------------------------------------------------------------------------
 *
 * access_path.go
 *	  AccessPath detects full table scans, missing indexes, and
 *	  inefficient access patterns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqladvisor/analyzers/access_path.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// AccessPath detects full table scans, missing indexes, and inefficient access patterns.
type AccessPath struct{}

func NewAccessPath() *AccessPath { return &AccessPath{} }
func (a *AccessPath) Name() string { return "access_path" }

func (a *AccessPath) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	if ctx.PlanTree == nil {
		return nil
	}
	var findings []sadv.Finding
	WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		findings = append(findings, a.checkFullScan(n, ctx)...)
		findings = append(findings, a.checkUnusedIndex(n, ctx)...)
	})
	return findings
}

func (a *AccessPath) checkFullScan(n *sadv.PlanNode, ctx *sadv.AnalyzeContext) []sadv.Finding {
	if n.Operation != "TABLE ACCESS" || n.Options != "FULL" {
		return nil
	}
	tableName := n.ObjectName
	stat := ctx.TableStats[TableKey(tableName)]

	var numRows int64
	if stat != nil {
		numRows = stat.NumRows
	} else {
		numRows = n.Rows
	}

	if numRows < 10000 {
		return nil // Small tables, full scan is acceptable
	}

	severity := "P3"
	if numRows > 100000 {
		severity = "P1"
	} else if numRows > 10000 {
		severity = "P2"
	}

	detail := fmt.Sprintf("表 %s Seq Scan 全表扫描，估算 %d 行", tableName, numRows)
	if stat != nil {
		detail += fmt.Sprintf("（实际 %d 行，%.1f MB）", stat.NumRows, stat.SizeMB)
	}

	suggestions := []sadv.Suggestion{
		{
			Action: "检查 WHERE 条件列是否有索引",
			SQL:    fmt.Sprintf("SELECT indexname, indexdef FROM pg_indexes WHERE tablename = '%s'", tableName),
			Risk:   "无",
		},
	}

	if n.FilterPred != "" {
		suggestions = append(suggestions, sadv.Suggestion{
			Action: fmt.Sprintf("考虑为过滤条件列添加索引: %s", truncate(n.FilterPred, 100)),
			SQL:    fmt.Sprintf("-- CREATE INDEX idx_%s_xxx ON %s (col)", tableName, tableName),
			Risk:   "创建索引期间可能影响写入性能",
			Impact: "消除全表扫描，提升查询速度",
		})
	}

	return []sadv.Finding{{
		Severity:    severity,
		Category:    "access_path",
		Summary:     fmt.Sprintf("全表扫描: %s (%d 行)", tableName, numRows),
		Detail:      detail,
		Suggestions: suggestions,
	}}
}

func (a *AccessPath) checkUnusedIndex(n *sadv.PlanNode, ctx *sadv.AnalyzeContext) []sadv.Finding {
	if n.Operation != "TABLE ACCESS" || n.Options != "FULL" {
		return nil
	}
	tableName := n.ObjectName
	indexes := ctx.IndexMap[TableKey(tableName)]

	if len(indexes) == 0 || n.FilterPred == "" {
		return nil
	}

	return []sadv.Finding{{
		Severity: "P2",
		Category: "access_path",
		Summary:  fmt.Sprintf("表 %s 有索引但仍走 Seq Scan", tableName),
		Detail:   fmt.Sprintf("表 %s 有 %d 个索引，但查询仍然走全表扫描，可能索引不匹配过滤条件", tableName, len(indexes)),
		Suggestions: []sadv.Suggestion{
			{
				Action: "检查现有索引是否覆盖 WHERE 条件列",
				SQL:    fmt.Sprintf("SELECT indexname, indexdef FROM pg_indexes WHERE tablename = '%s'", tableName),
			},
			{
				Action: "检查 random_page_cost 和 seq_page_cost 设置是否导致优化器偏好 Seq Scan",
				SQL:    "SHOW random_page_cost; SHOW seq_page_cost;",
				Impact: "SSD 上建议 random_page_cost = 1.1",
			},
		},
	}}
}
