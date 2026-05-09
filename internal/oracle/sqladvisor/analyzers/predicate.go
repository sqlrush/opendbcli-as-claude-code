/*-------------------------------------------------------------------------
 *
 * predicate.go
 *	  Predicate detects filter predicates that prevent index usage.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/predicate.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Predicate detects filter predicates that prevent index usage.
type Predicate struct{}

// NewPredicate creates a predicate analyzer.
func NewPredicate() *Predicate { return &Predicate{} }

func (p *Predicate) Name() string { return "predicate" }

func (p *Predicate) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.FilterPred == "" {
			return
		}
		findings = append(findings, p.checkImplicitConversion(n)...)
		findings = append(findings, p.checkFunctionOnColumn(n)...)
		findings = append(findings, p.checkLeadingWildcard(n)...)
	})
	return findings
}

// checkImplicitConversion detects TO_NUMBER/TO_CHAR/INTERNAL_FUNCTION wrapping column names.
// Only flags when the function wraps a quoted column ("COL"), not bind variables (:B1) or literals.
func (p *Predicate) checkImplicitConversion(n *sadv.PlanNode) []sadv.Finding {
	pred := n.FilterPred
	// Pattern: TO_NUMBER("COL") or TO_CHAR("COL") — function wrapping a quoted column name
	hasConversion := containsConversionOnColumn(pred, "TO_NUMBER(") ||
		containsConversionOnColumn(pred, "TO_CHAR(") ||
		strings.Contains(pred, "INTERNAL_FUNCTION(")
	if !hasConversion {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P1",
		Category: "predicate",
		Summary:  "隐式类型转换导致索引失效",
		Detail: fmt.Sprintf("Plan ID %d 过滤条件存在隐式转换: %s",
			n.ID, truncate(pred, 120)),
		Suggestions: []sadv.Suggestion{{
			Action: "修改WHERE条件,使比较两端类型一致,避免隐式转换",
			SQL:    "-- 示例: 将 WHERE num_col = '123' 改为 WHERE num_col = 123\n-- 或将 WHERE char_col = 456 改为 WHERE char_col = '456'",
			Risk:   "低", Impact: "高 — 消除隐式转换后索引可正常使用",
		}},
	}}
}

// checkFunctionOnColumn detects UPPER/LOWER/TRUNC/NVL on indexed columns.
func (p *Predicate) checkFunctionOnColumn(n *sadv.PlanNode) []sadv.Finding {
	pred := n.FilterPred
	funcs := []string{"UPPER(", "LOWER(", "TRUNC(", "NVL("}
	var found []string
	for _, f := range funcs {
		if strings.Contains(pred, f) {
			found = append(found, strings.TrimSuffix(f, "("))
		}
	}
	if len(found) == 0 {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "predicate",
		Summary:  "函数调用导致索引失效",
		Detail: fmt.Sprintf("Plan ID %d 过滤条件包含函数 %s: %s",
			n.ID, strings.Join(found, "/"), truncate(pred, 120)),
		Suggestions: []sadv.Suggestion{{
			Action: "创建函数索引或改写查询以避免对列施加函数",
			SQL: fmt.Sprintf("-- 函数索引示例:\n-- CREATE INDEX idx_func ON table(%s(column_name));",
				found[0]),
			Risk:   "中 — 函数索引增加存储和DML开销",
			Impact: "高 — 可恢复索引使用",
		}},
	}}
}

// checkLeadingWildcard detects LIKE '%...' patterns.
func (p *Predicate) checkLeadingWildcard(n *sadv.PlanNode) []sadv.Finding {
	pred := n.FilterPred
	if !strings.Contains(pred, "LIKE '%") {
		return nil
	}
	return []sadv.Finding{{
		Severity: "P2",
		Category: "predicate",
		Summary:  "前缀通配符LIKE导致索引失效",
		Detail: fmt.Sprintf("Plan ID %d 过滤条件使用前缀通配符: %s",
			n.ID, truncate(pred, 120)),
		Suggestions: []sadv.Suggestion{{
			Action: "改用全文索引或反转查询逻辑以避免前缀通配符",
			SQL:    "-- 方案1: 使用Oracle Text全文索引\n-- CREATE INDEX idx_text ON table(column) INDEXTYPE IS CTXSYS.CONTEXT;\n-- 方案2: 如果只是后缀匹配, 可用REVERSE函数索引",
			Risk:   "中", Impact: "中",
		}},
	}}
}

// containsConversionOnColumn checks if a conversion function wraps a quoted column name.
// e.g. TO_NUMBER("CODE") → true (implicit conversion on column)
// e.g. TO_CHAR(:B1*100)  → false (bind variable, not a column)
func containsConversionOnColumn(pred, funcPrefix string) bool {
	idx := strings.Index(pred, funcPrefix)
	if idx < 0 {
		return false
	}
	// Check what follows the function name — if it's a quoted identifier ("COL"), it's a column
	after := pred[idx+len(funcPrefix):]
	return strings.HasPrefix(after, "\"")
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
