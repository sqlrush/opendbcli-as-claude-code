/*-------------------------------------------------------------------------
 *
 * statistics.go
 *	  Statistics detects stale or missing table statistics.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqladvisor/analyzers/statistics.go
 *
 *-------------------------------------------------------------------------
 */
package analyzers

import (
	"fmt"

	sadv "github.com/sqlrush/opendb/internal/sqladvisor"
)

// Statistics detects stale or missing table statistics.
type Statistics struct{}

func NewStatistics() *Statistics     { return &Statistics{} }
func (s *Statistics) Name() string { return "statistics" }

func (s *Statistics) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding

	for _, stat := range ctx.TableStats {
		if stat.NumRows == 0 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "statistics",
				Summary:  fmt.Sprintf("统计信息缺失: %s", stat.TableName),
				Detail:   fmt.Sprintf("表 %s 统计信息中行数为 0，可能从未被 ANALYZE", stat.TableName),
				Suggestions: []sadv.Suggestion{
					{
						Action: "收集统计信息",
						SQL:    fmt.Sprintf("ANALYZE TABLE %s", stat.TableName),
						Risk:   "ANALYZE TABLE 会短暂加读锁",
						Impact: "优化器可基于准确统计信息选择最优执行计划",
					},
				},
			})
			continue
		}

		if stat.DaysSinceStats > 30 || stat.StaleStats {
			severity := "P2"
			if stat.DaysSinceStats > 90 {
				severity = "P1"
			}
			findings = append(findings, sadv.Finding{
				Severity: severity,
				Category: "statistics",
				Summary:  fmt.Sprintf("统计信息过期: %s (%d 天)", stat.TableName, stat.DaysSinceStats),
				Detail:   fmt.Sprintf("表 %s 上次 ANALYZE 在 %s（%d 天前），统计信息可能不准确", stat.TableName, stat.LastAnalyzed, stat.DaysSinceStats),
				Suggestions: []sadv.Suggestion{
					{
						Action: "重新收集统计信息",
						SQL:    fmt.Sprintf("ANALYZE TABLE %s", stat.TableName),
						Risk:   "无",
						Impact: "更新统计信息，优化器可做出更好的执行计划选择",
					},
					{
						Action: "调整 InnoDB 统计采样页数",
						SQL:    "SET GLOBAL innodb_stats_persistent_sample_pages = 40",
						Risk:   "增加 ANALYZE 耗时",
						Impact: "提高统计信息准确度",
					},
				},
			})
		}
	}

	// Check row estimate mismatch
	if ctx.PlanTree != nil {
		WalkTree(ctx.PlanTree, func(n *sadv.PlanNode) {
			if n.ActualRows != nil && n.Rows > 0 {
				ratio := float64(*n.ActualRows) / float64(n.Rows)
				if ratio > 10 || (n.Rows > 1 && ratio < 0.1) {
					findings = append(findings, sadv.Finding{
						Severity: "P1",
						Category: "statistics",
						Summary:  fmt.Sprintf("行数估算偏差 %.0f 倍: %s", ratio, n.ObjectName),
						Detail:   fmt.Sprintf("优化器估算 %d 行，实际 %d 行（偏差 %.0f 倍），可能导致错误执行计划", n.Rows, *n.ActualRows, ratio),
						Suggestions: []sadv.Suggestion{
							{
								Action: "收集直方图统计信息",
								SQL:    fmt.Sprintf("ANALYZE TABLE %s UPDATE HISTOGRAM ON col WITH 100 BUCKETS", n.ObjectName),
								Risk:   "直方图收集需要时间",
								Impact: "修正基数估算��差",
							},
						},
					})
				}
			}
		})
	}

	return findings
}
