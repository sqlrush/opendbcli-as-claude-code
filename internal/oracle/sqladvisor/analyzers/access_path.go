/*-------------------------------------------------------------------------
 *
 * access_path.go
 *	  AccessPath detects inefficient table/index access patterns.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/access_path.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"
	"strings"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// AccessPath detects inefficient table/index access patterns.
type AccessPath struct{}

// NewAccessPath creates an access-path analyzer.
func NewAccessPath() *AccessPath { return &AccessPath{} }

func (a *AccessPath) Name() string { return "access_path" }

func (a *AccessPath) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		findings = append(findings, a.checkFullScan(n, ctx)...)
		findings = append(findings, a.checkCartesian(n)...)
		findings = append(findings, a.checkSkipScan(n)...)
	})
	return findings
}

func (a *AccessPath) checkFullScan(n *sadv.PlanNode, ctx *sadv.AnalyzeContext) []sadv.Finding {
	if !n.IsFullScan() {
		return nil
	}
	key := tableKey(n.ObjectOwner, n.ObjectName)
	stat, ok := ctx.TableStats[key]
	if !ok || stat.NumRows <= 10000 {
		return nil
	}

	// Use plan node E-Rows (optimizer estimate for THIS step) to judge selectivity,
	// NOT AvgRowsProc which is distorted by aggregate functions (COUNT/SUM return 1 row).
	estimatedRows := n.Rows
	if n.ActualRows != nil {
		estimatedRows = *n.ActualRows
	}

	if stat.NumRows > 100000 && estimatedRows < stat.NumRows/10 {
		return []sadv.Finding{a.fullScanHighSelectivity(n, stat, estimatedRows)}
	}
	if stat.NumRows > 100000 {
		return []sadv.Finding{{
			Severity: "P3",
			Category: "access_path",
			Summary:  "全表扫描大表,返回行比例高,全扫可能合理",
			Detail: fmt.Sprintf("表 %s 共 %d 行, 估算返回 %d 行(%.0f%%)",
				key, stat.NumRows, estimatedRows, float64(estimatedRows)*100/float64(stat.NumRows)),
			Suggestions: []sadv.Suggestion{{
				Action: "确认全表扫描是否为最优路径,必要时添加索引",
				SQL:    fmt.Sprintf("-- 检查表大小\nSELECT num_rows, blocks FROM dba_tables WHERE owner='%s' AND table_name='%s';", stat.Owner, stat.TableName),
				Risk:   "低", Impact: "需评估",
			}},
		}}
	}

	// 10000-100000: medium table
	return []sadv.Finding{{
		Severity: "P2",
		Category: "access_path",
		Summary:  fmt.Sprintf("中等表 %s 全表扫描(%d行)", n.ObjectName, stat.NumRows),
		Detail:   fmt.Sprintf("表 %s 行数 %d, 建议评估是否需要索引", key, stat.NumRows),
		Suggestions: []sadv.Suggestion{{
			Action: "评估是否需要添加索引以避免全表扫描",
			SQL:    fmt.Sprintf("SELECT index_name, column_name FROM dba_ind_columns WHERE table_owner='%s' AND table_name='%s' ORDER BY index_name, column_position;", stat.Owner, stat.TableName),
			Risk:   "低", Impact: "中",
		}},
	}}
}

func (a *AccessPath) fullScanHighSelectivity(n *sadv.PlanNode, stat *sadv.TableStat, estimatedRows int64) sadv.Finding {
	key := tableKey(n.ObjectOwner, n.ObjectName)
	col := extractColumnFromPred(n.FilterPred)
	suggestSQL := fmt.Sprintf("SELECT index_name, column_name FROM dba_ind_columns WHERE table_owner='%s' AND table_name='%s' ORDER BY index_name, column_position;", stat.Owner, stat.TableName)
	if col != "" {
		suggestSQL = fmt.Sprintf("CREATE INDEX idx_%s_%s ON %s.%s(%s);",
			strings.ToLower(stat.TableName), strings.ToLower(col),
			stat.Owner, stat.TableName, col)
	}
	return sadv.Finding{
		Severity: "P1",
		Category: "access_path",
		Summary:  "全表扫描大表,选择度高,应使用索引",
		Detail: fmt.Sprintf("表 %s 共 %d 行, 估算返回 %d 行, 过滤条件: %s",
			key, stat.NumRows, estimatedRows, n.FilterPred),
		Suggestions: []sadv.Suggestion{{
			Action: "创建索引以消除全表扫描",
			SQL:    suggestSQL,
			Risk:   "中 — 索引会增加DML开销", Impact: "高 — 可大幅减少逻辑读",
		}},
	}
}

func (a *AccessPath) checkCartesian(n *sadv.PlanNode) []sadv.Finding {
	if n.Operation == "MERGE JOIN" && n.Options == "CARTESIAN" {
		return []sadv.Finding{{
			Severity: "P1",
			Category: "access_path",
			Summary:  "笛卡尔积,缺少连接条件",
			Detail:   fmt.Sprintf("Plan ID %d: MERGE JOIN CARTESIAN, 估算行数 %d", n.ID, n.Rows),
			Suggestions: []sadv.Suggestion{{
				Action: "添加正确的JOIN连接条件消除笛卡尔积",
				SQL:    "-- 检查SQL中是否遗漏了JOIN ON条件\n-- 示例: SELECT ... FROM a JOIN b ON a.id = b.a_id;",
				Risk:   "低", Impact: "高 — 消除笛卡尔积可大幅减少结果集",
			}},
		}}
	}
	return nil
}

func (a *AccessPath) checkSkipScan(n *sadv.PlanNode) []sadv.Finding {
	if n.Operation == "INDEX" && strings.Contains(n.Options, "SKIP SCAN") && n.Cost > 1000 {
		return []sadv.Finding{{
			Severity: "P2",
			Category: "access_path",
			Summary:  "INDEX SKIP SCAN 效率低,考虑复合索引前导列",
			Detail: fmt.Sprintf("索引 %s SKIP SCAN, Cost=%d",
				n.ObjectName, n.Cost),
			Suggestions: []sadv.Suggestion{{
				Action: "创建以查询条件列为前导列的复合索引",
				SQL: fmt.Sprintf("-- 检查当前索引结构\nSELECT index_name, column_name, column_position FROM dba_ind_columns WHERE index_name='%s' ORDER BY column_position;",
					n.ObjectName),
				Risk: "中", Impact: "高 — 正确前导列可消除SKIP SCAN",
			}},
		}}
	}
	return nil
}

// extractColumnFromPred tries to extract a column name from a filter predicate.
// Handles patterns like: "COL"='value' or "SCHEMA"."TABLE"."COL"=...
func extractColumnFromPred(pred string) string {
	if pred == "" {
		return ""
	}
	// Look for the last quoted identifier before an operator
	start := strings.LastIndex(pred, "\"")
	if start < 1 {
		return ""
	}
	end := strings.LastIndex(pred[:start], "\"")
	if end < 0 {
		return ""
	}
	col := pred[end+1 : start]
	if col == "" || strings.Contains(col, ".") {
		return ""
	}
	return col
}
