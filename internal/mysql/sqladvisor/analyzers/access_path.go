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
 *	  internal/mysql/sqladvisor/analyzers/access_path.go
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

	if numRows < 1000 {
		return nil // Small tables, full scan is fine
	}

	severity := "P3"
	if numRows > 100000 {
		severity = "P1"
	} else if numRows > 10000 {
		severity = "P2"
	}

	detail := fmt.Sprintf("表 %s 全表扫描，估算 %d 行", tableName, numRows)
	if stat != nil {
		detail += fmt.Sprintf("（实际 %d 行，%.1f MB）", stat.NumRows, stat.SizeMB)
	}

	suggestions := []sadv.Suggestion{
		{
			Action: "检查 WHERE 条件列是否有索引",
			SQL:    fmt.Sprintf("SHOW INDEX FROM %s", tableName),
			Risk:   "无",
		},
	}

	if n.FilterPred != "" {
		suggestions = append(suggestions, sadv.Suggestion{
			Action: fmt.Sprintf("考虑为过滤条件列添加索引: %s", truncate(n.FilterPred, 100)),
			SQL:    fmt.Sprintf("-- ALTER TABLE %s ADD INDEX idx_xxx (col)", tableName),
			Risk:   "创建索引可能短暂锁表",
			Impact: "减少全表扫描，提升查询速度",
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

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
