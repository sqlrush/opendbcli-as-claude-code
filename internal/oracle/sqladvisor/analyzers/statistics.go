/*-------------------------------------------------------------------------
 *
 * statistics.go
 *	  Statistics detects stale or missing table/column statistics.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqladvisor/analyzers/statistics.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Statistics detects stale or missing table/column statistics.
type Statistics struct{}

// NewStatistics creates a statistics analyzer.
func NewStatistics() *Statistics { return &Statistics{} }

func (s *Statistics) Name() string { return "statistics" }

func (s *Statistics) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	for _, stat := range ctx.TableStats {
		findings = append(findings, s.checkStaleOrMissing(stat)...)
	}
	findings = append(findings, s.checkRowEstimateMismatch(ctx)...)
	return findings
}

func (s *Statistics) checkStaleOrMissing(stat *sadv.TableStat) []sadv.Finding {
	// Never analyzed
	if stat.NumRows == 0 && stat.DaysSinceStats == 0 {
		return []sadv.Finding{{
			Severity: "P1",
			Category: "statistics",
			Summary:  fmt.Sprintf("表 %s.%s 从未收集统计信息", stat.Owner, stat.TableName),
			Detail:   "NumRows=0 且无上次分析记录,优化器可能使用动态采样产生不准确的计划",
			Suggestions: []sadv.Suggestion{{
				Action: "立即收集统计信息",
				SQL: fmt.Sprintf("EXEC DBMS_STATS.GATHER_TABLE_STATS('%s','%s', method_opt=>'FOR ALL COLUMNS SIZE AUTO');",
					stat.Owner, stat.TableName),
				Risk: "低", Impact: "高 — 准确统计信息是优化器选择最优计划的基础",
			}},
		}}
	}

	// Stale
	if stat.DaysSinceStats > 90 || (stat.StaleStats && stat.DaysSinceStats > 30) {
		sev := "P2"
		if stat.DaysSinceStats > 90 {
			sev = "P1"
		}
		return []sadv.Finding{{
			Severity: sev,
			Category: "statistics",
			Summary:  fmt.Sprintf("统计信息严重过期(%d天)", stat.DaysSinceStats),
			Detail: fmt.Sprintf("表 %s.%s 上次分析 %s, 距今 %d 天",
				stat.Owner, stat.TableName, stat.LastAnalyzed, stat.DaysSinceStats),
			Suggestions: []sadv.Suggestion{{
				Action: "重新收集统计信息",
				SQL: fmt.Sprintf("EXEC DBMS_STATS.GATHER_TABLE_STATS('%s','%s', method_opt=>'FOR ALL COLUMNS SIZE AUTO');",
					stat.Owner, stat.TableName),
				Risk: "低", Impact: "高 — 过期统计信息导致优化器估算偏差",
			}},
		}}
	}

	if stat.DaysSinceStats > 30 || stat.StaleStats {
		return []sadv.Finding{{
			Severity: "P2",
			Category: "statistics",
			Summary:  fmt.Sprintf("统计信息过期(%d天)", stat.DaysSinceStats),
			Detail: fmt.Sprintf("表 %s.%s 上次分析 %s, 距今 %d 天",
				stat.Owner, stat.TableName, stat.LastAnalyzed, stat.DaysSinceStats),
			Suggestions: []sadv.Suggestion{{
				Action: "收集统计信息",
				SQL: fmt.Sprintf("EXEC DBMS_STATS.GATHER_TABLE_STATS('%s','%s', method_opt=>'FOR ALL COLUMNS SIZE AUTO');",
					stat.Owner, stat.TableName),
				Risk: "低", Impact: "中",
			}},
		}}
	}

	return nil
}

func (s *Statistics) checkRowEstimateMismatch(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding
	walkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
		if n.ActualRows == nil || n.Rows <= 0 {
			return
		}
		actual := *n.ActualRows
		if actual <= 0 {
			return
		}
		ratio := float64(actual) / float64(n.Rows)
		if ratio < 0.1 || ratio > 10 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "statistics",
				Summary:  "估算行数与实际行数偏差>10倍,统计信息不准",
				Detail: fmt.Sprintf("Plan ID %d (%s): 估算 %d 行, 实际 %d 行 (偏差 %.1f倍)",
					n.ID, n.Operation, n.Rows, actual, ratio),
				Suggestions: []sadv.Suggestion{{
					Action: "收集含直方图的统计信息以修正基数估算",
					SQL:    "EXEC DBMS_STATS.GATHER_TABLE_STATS(ownname=>USER, tabname=>'TABLE', method_opt=>'FOR ALL COLUMNS SIZE AUTO');",
					Risk:   "低", Impact: "高 — 消除基数估算偏差可让优化器选择正确计划",
				}},
			})
		}
	})
	return findings
}
