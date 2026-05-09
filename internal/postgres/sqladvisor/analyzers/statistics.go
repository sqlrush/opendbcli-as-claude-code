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
 *	  internal/postgres/sqladvisor/analyzers/statistics.go
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

func NewStatistics() *Statistics   { return &Statistics{} }
func (s *Statistics) Name() string { return "statistics" }

func (s *Statistics) Analyze(ctx *sadv.AnalyzeContext) []sadv.Finding {
	var findings []sadv.Finding

	for _, stat := range ctx.TableStats {
		// Tables with 0 rows in stats but potentially large on disk
		if stat.NumRows == 0 && stat.SizeMB > 1 {
			findings = append(findings, sadv.Finding{
				Severity: "P1",
				Category: "statistics",
				Summary:  fmt.Sprintf("统计信息缺失: %s (%.1f MB)", stat.TableName, stat.SizeMB),
				Detail:   fmt.Sprintf("表 %s 统计信息中行数为 0 但占用 %.1f MB，可能从未被 ANALYZE", stat.TableName, stat.SizeMB),
				Suggestions: []sadv.Suggestion{
					{
						Action: "收集统计信息",
						SQL:    fmt.Sprintf("ANALYZE %s", stat.TableName),
						Risk:   "ANALYZE 会读取表数据，对大表可能耗时",
						Impact: "优化器可基于准确统计信息选择最优执行计划",
					},
				},
			})
			continue
		}

		// Stale statistics: last_analyze is empty (never analyzed)
		if stat.LastAnalyzed == "" && stat.NumRows > 0 {
			findings = append(findings, sadv.Finding{
				Severity: "P2",
				Category: "statistics",
				Summary:  fmt.Sprintf("从未手动 ANALYZE: %s", stat.TableName),
				Detail:   fmt.Sprintf("表 %s 从未执行 ANALYZE，依赖 autovacuum 自动收集", stat.TableName),
				Suggestions: []sadv.Suggestion{
					{
						Action: "手动收集统计信息",
						SQL:    fmt.Sprintf("ANALYZE %s", stat.TableName),
						Risk:   "无",
						Impact: "确保统计信息准确",
					},
				},
			})
			continue
		}

		// High dead tuple ratio indicates stale stats
		if stat.StaleStats {
			findings = append(findings, sadv.Finding{
				Severity: "P2",
				Category: "statistics",
				Summary:  fmt.Sprintf("死元组过多: %s", stat.TableName),
				Detail:   fmt.Sprintf("表 %s 死元组比例超过 20%%，统计信息可能不准确", stat.TableName),
				Suggestions: []sadv.Suggestion{
					{
						Action: "执行 VACUUM ANALYZE 清理死元组并更新统计信息",
						SQL:    fmt.Sprintf("VACUUM ANALYZE %s", stat.TableName),
						Risk:   "VACUUM 会占用 IO 资源",
						Impact: "清理死元组释放空间，更新统计信息",
					},
					{
						Action: "检查 autovacuum 配置是否合理",
						SQL:    "SELECT name, setting FROM pg_settings WHERE name LIKE 'autovacuum%'",
						Impact: "确保 autovacuum 频率满足业务需求",
					},
				},
			})
		}

		// Stale statistics based on DaysSinceStats
		if stat.DaysSinceStats > 30 {
			severity := "P2"
			if stat.DaysSinceStats > 90 {
				severity = "P1"
			}
			findings = append(findings, sadv.Finding{
				Severity: severity,
				Category: "statistics",
				Summary:  fmt.Sprintf("统计信息过期: %s (%d 天)", stat.TableName, stat.DaysSinceStats),
				Detail:   fmt.Sprintf("表 %s 上次 ANALYZE 在 %s（%d 天前）", stat.TableName, stat.LastAnalyzed, stat.DaysSinceStats),
				Suggestions: []sadv.Suggestion{
					{
						Action: "重新收集统计信息",
						SQL:    fmt.Sprintf("ANALYZE %s", stat.TableName),
						Risk:   "无",
						Impact: "更新统计信息，优化器可做出更好的执行计划选择",
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
								Action: "增大统计采样目标并重新 ANALYZE",
								SQL:    fmt.Sprintf("ALTER TABLE %s ALTER COLUMN col SET STATISTICS 1000;\nANALYZE %s;", n.ObjectName, n.ObjectName),
								Risk:   "增大 statistics target 会增加 ANALYZE 耗时",
								Impact: "修正基数估算偏差",
							},
						},
					})
				}
			}
		})
	}

	return findings
}
