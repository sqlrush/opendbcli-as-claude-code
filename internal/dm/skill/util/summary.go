/*-------------------------------------------------------------------------
 *
 * summary.go
 *	  Package util — DM skill 公用工具。
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dm/skill/util/summary.go
 *
 *-------------------------------------------------------------------------
 */
// Package util — DM skill 公用工具。
//
// AppendSummary 给 skill 输出末尾追加 [summary] 段（key:value 形式），让 LLM
// 尤其是中小模型能直接复读具体值（参见 docs/design-local-model-optimization.md
// Tier 1 不对称帮助原则）。
package util

import (
	"fmt"
	"strings"
)

// SummaryEntry 一条 summary 行。
type SummaryEntry struct {
	Key string
	Val any
}

// FormatSummary 格式化为 [summary] 块字符串。
func FormatSummary(entries []SummaryEntry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n[summary]\n")
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("%s: %v\n", e.Key, e.Val))
	}
	return b.String()
}

// AppendSummary 将 [summary] 块追加到已有 rendered 文本。
func AppendSummary(rendered string, entries []SummaryEntry) string {
	tail := FormatSummary(entries)
	if tail == "" {
		return rendered
	}
	return rendered + tail
}

// FirstString 安全从结果第一行第一列取字符串。
func FirstString(rows [][]any) string {
	if len(rows) == 0 || len(rows[0]) == 0 || rows[0][0] == nil {
		return ""
	}
	return fmt.Sprintf("%v", rows[0][0])
}

// CountByCol 统计某列的值分布（用于 sessions by state 之类）。
func CountByCol(rows [][]any, colIdx int) map[string]int {
	m := make(map[string]int)
	for _, row := range rows {
		if colIdx >= len(row) || row[colIdx] == nil {
			continue
		}
		key := fmt.Sprintf("%v", row[colIdx])
		m[key]++
	}
	return m
}

// FormatTableSummary 把 db.QueryResult 渲染成 text 表格 + [summary] 段。
// 用于 LLM 看的 Rendered field（REPL 仍可用 Data 渲染表）。
func FormatTableWithSummary(table string, entries []SummaryEntry) string {
	return table + FormatSummary(entries)
}
