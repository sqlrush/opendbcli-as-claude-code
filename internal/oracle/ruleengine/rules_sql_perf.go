/*-------------------------------------------------------------------------
 *
 * rules_sql_perf.go
 *	  Oracle rule engine — SQL performance rules (top SQL by elapsed, missing indexes, plan flips).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_sql_perf.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"github.com/sqlrush/opendb/internal/sqladvisor"
)

// advisorResult wraps converted SQL Advisor findings for the rule engine.
type advisorResult struct {
	findings []Finding
	actions  []Action
}

// liveSQLPerfRules returns SQL performance rules for live diagnosis that analyze
// Top SQL execution plan characteristics, plan drift, sort/temp spill, and hot SQL.
func liveSQLPerfRules() []*Rule {
	return []*Rule{
		ruleLiveFullTableScan(),
		ruleLivePlanDrift(),
		ruleLiveSortTempSpill(),
		ruleLiveHotSQL(),
	}
}

// ─── WD020: Full Table Scan diagnosis ───────────────────────────────────────

func ruleLiveFullTableScan() *Rule {
	return &Rule{
		ID:       "WD025",
		Name:     "全表扫描诊断",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{},
			SkipWhen: []SkipCondition{
				{Desc: "无 Top SQL 含全表扫描", Check: func(ctx *EvalContext) bool {
					for _, sql := range ctx.TopSQLs {
						if sql.HasFullScan && sql.MaxConcurrent >= 3 {
							return false
						}
					}
					return true
				}},
			},
		},
		Tree: &TreeNode{
			Step: "分析全表扫描 SQL 的数据量和选择性",
			Check: func(ctx *EvalContext) interface{} {
				// Find the top full-scan SQL by concurrency.
				for _, sql := range ctx.TopSQLs {
					if !sql.HasFullScan || sql.MaxConcurrent < 3 {
						continue
					}
					// Use SQL Advisor report if available.
					if sql.AdvisorReport != nil && len(sql.AdvisorReport.Findings) > 0 {
						return convertAdvisorFindings(sql.AdvisorReport)
					}
					// Fallback to heuristic logic.
					execs := sql.Executions
					if execs == 0 {
						execs = 1
					}
					bufPerExec := float64(sql.BufferGets) / float64(execs)
					rowsPerExec := float64(sql.RowsProcessed) / float64(execs)

					if bufPerExec > 5000 {
						// Large table full scan.
						if rowsPerExec < 100 && rowsPerExec > 0 {
							return "large_table_selective" // Reads many blocks but returns few rows → should use index.
						}
						return "large_table_bulk" // Reads many blocks AND returns many rows → full scan may be reasonable.
					}
					return "small_table" // Small table, full scan OK.
				}
				return "none"
			},
			Branches: []Branch{
				{
					Label: "SQL Advisor 深度诊断",
					Match: func(v interface{}) bool {
						_, ok := v.(*advisorResult)
						return ok
					},
					Severity: SeverityHigh,
				},
				{
					Label:    "大表全扫 + 高选择度 — 应该使用索引",
					Match:    MatchEquals("large_table_selective"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 对大表执行全表扫描但只返回少量行，说明查询具有高选择度"},
						{Desc: "全表扫描读取了大量不必要的数据块，应通过索引直接定位目标行"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "检查 WHERE 条件列是否有索引，没有则创建",
							RawSQL: "SELECT table_name, column_name, index_name FROM user_ind_columns WHERE table_name = '{table}' ORDER BY index_name, column_position",
							Risk:   "创建索引会短暂锁表", Rollback: "DROP INDEX {index_name}"},
						{Type: ActionInvestigate, Desc: "检查是否存在隐式类型转换导致索引失效",
							RawSQL: "SELECT sql_id, plan_hash_value, operation, options, object_name, filter_predicates, access_predicates FROM v$sql_plan WHERE sql_id = '{sql_id}' AND (operation = 'TABLE ACCESS' AND options = 'FULL') ORDER BY id",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查统计信息是否过期",
							RawSQL: "SELECT table_name, num_rows, last_analyzed, stale_stats FROM user_tab_statistics WHERE table_name = '{table}'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "大表全扫 + 低选择度 — 全扫可能合理",
					Match:    MatchEquals("large_table_bulk"),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Top SQL 对大表执行全表扫描且返回大量行，全表扫描可能是合理的执行计划"},
						{Desc: "如果查询频率高，考虑分区表裁剪或物化视图减少扫描范围"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认全扫是否必要（检查返回行比例）",
							RawSQL: "SELECT sql_id, executions, rows_processed, buffer_gets, ROUND(rows_processed/GREATEST(executions,1)) rows_per_exec, ROUND(buffer_gets/GREATEST(executions,1)) bufs_per_exec FROM v$sqlarea WHERE sql_id = '{sql_id}'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "对高频全扫考虑分区裁剪或并行查询",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "小表全扫 — 影响小",
					Match:    MatchEquals("small_table"),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "全表扫描的表数据量小（逻辑读 < 5000/次），性能影响有限"},
					},
				},
				{
					Label:    "默认 — 无明显全表扫描问题",
					Match:    MatchDefault(),
					Severity: SeverityLow,
				},
			},
		},
		Tags:     []string{"sql", "full_scan", "index", "performance"},
		Versions: "10g+",
	}
}

// ─── WD021: Plan Drift / Regression detection ──────────────────────────────

func ruleLivePlanDrift() *Rule {
	return &Rule{
		ID:       "WD021",
		Name:     "执行计划漂移/回退检测",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{},
			SkipWhen: []SkipCondition{
				{Desc: "无多计划 SQL", Check: func(ctx *EvalContext) bool {
					for _, sql := range ctx.TopSQLs {
						if sql.PlanCount > 1 {
							return false
						}
					}
					return true
				}},
			},
		},
		Tree: &TreeNode{
			Step: "比较当前计划与历史最优计划的性能差异",
			Check: func(ctx *EvalContext) interface{} {
				for _, sql := range ctx.TopSQLs {
					if sql.PlanCount <= 1 || sql.BestPlanAvgSec <= 0 {
						continue
					}
					ratio := sql.CurrentPlanAvgSec / sql.BestPlanAvgSec
					if ratio >= 3.0 {
						return "severe_regression"
					}
					if ratio >= 1.5 {
						return "mild_regression"
					}
					return "stable_or_improved"
				}
				return "no_drift"
			},
			Branches: []Branch{
				{
					Label:    "严重回退 — 当前计划比最优慢 3 倍以上",
					Match:    MatchEquals("severe_regression"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "检测到执行计划回退：同一 SQL 存在多个执行计划，当前计划比历史最优慢 3 倍以上"},
						{Desc: "可能原因：统计信息变化、绑定变量窥探、ACS 自适应游标切换"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "使用 SPM 固定历史最优计划",
							RawSQL: "-- 查看当前 SQL 的所有计划:\nSELECT sql_id, plan_hash_value, executions, ROUND(elapsed_time/GREATEST(executions,1)/1e6,3) avg_sec FROM v$sql WHERE sql_id='{sql_id}' ORDER BY avg_sec",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "创建 SQL Plan Baseline 锁定好计划",
							RawSQL: "DECLARE cnt PLS_INTEGER; BEGIN cnt := DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE(sql_id=>'{sql_id}', plan_hash_value=>{good_plan_hash}); END;",
							Risk:   "需确认好计划的 plan_hash_value", Rollback: "EXEC DBMS_SPM.DROP_SQL_PLAN_BASELINE(sql_handle=>'{handle}')"},
						{Type: ActionInvestigate, Desc: "检查统计信息是否刚被收集",
							RawSQL: "SELECT table_name, last_analyzed, stale_stats FROM user_tab_statistics WHERE last_analyzed > SYSDATE - 1 ORDER BY last_analyzed DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "轻微回退 — 当前计划比最优慢 1.5-3 倍",
					Match:    MatchEquals("mild_regression"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "执行计划有轻微回退，当前计划比历史最优慢 1.5-3 倍"},
						{Desc: "建议持续监控，如果持续恶化考虑固定好计划"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看各计划的性能对比",
							RawSQL: "SELECT sql_id, plan_hash_value, executions, ROUND(elapsed_time/GREATEST(executions,1)/1e6,3) avg_sec, buffer_gets, disk_reads FROM v$sql WHERE sql_id='{sql_id}' ORDER BY avg_sec",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "收集最新统计信息确保优化器有准确数据",
							RawSQL: "BEGIN DBMS_STATS.GATHER_TABLE_STATS('{owner}', '{table}', method_opt=>'FOR ALL COLUMNS SIZE AUTO', no_invalidate=>FALSE); END;",
							Risk:   "可能导致计划变更", Rollback: "恢复旧统计信息"},
					},
				},
				{
					Label:    "计划变更但性能持平或改善 — 无需干预",
					Match:    MatchEquals("stable_or_improved"),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "SQL 存在多个执行计划，但当前计划性能持平或优于历史计划"},
					},
				},
				{
					Label:    "默认",
					Match:    MatchDefault(),
					Severity: SeverityLow,
				},
			},
		},
		Tags:     []string{"sql", "plan_drift", "regression", "spm", "statistics"},
		Versions: "10g+",
	}
}

// ─── WD022: Sort / TEMP Spill diagnosis ─────────────────────────────────────

func ruleLiveSortTempSpill() *Rule {
	return &Rule{
		ID:       "WD022",
		Name:     "排序/TEMP 溢出诊断",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "direct path read temp"},
			{Type: SignalWaitEvent, Key: "direct path write temp"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "direct path read temp", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "temp 等待 < 3%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("direct path read temp")+ctx.WaitPct("direct path write temp") < 3
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查 PGA 使用率判断溢出原因",
			Check: func(ctx *EvalContext) interface{} {
				pgaPct := ctx.MetricValue("pga_used_pct")
				if pgaPct > 90 {
					return "pga_exhausted"
				}
				// Check if top SQL has sort.
				for _, sql := range ctx.TopSQLs {
					if sql.HasSort && sql.MaxConcurrent >= 3 {
						return "sql_sort"
					}
					if sql.HasHashJoin && sql.MaxConcurrent >= 3 {
						return "sql_hash_join"
					}
				}
				return "unknown"
			},
			Branches: []Branch{
				{
					Label:    "PGA 不足 — 排序/Hash 操作溢出到 TEMP",
					Match:    MatchEquals("pga_exhausted"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "PGA 使用率 > 90%，内存不足导致排序/Hash Join 操作溢出到 TEMP 表空间"},
						{Desc: "溢出操作使用磁盘 I/O 替代内存，性能大幅下降"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET",
							RawSQL: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=2G SCOPE=BOTH;\n-- 当前值:\nSELECT name, value/1024/1024 mb FROM v$parameter WHERE name='pga_aggregate_target'",
							Risk:   "增大 PGA 减少可用 SGA", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
						{Type: ActionInvestigate, Desc: "查看哪些 SQL 消耗 PGA 最多",
							RawSQL: "SELECT sql_id, operation_type, policy, estimated_optimal_size/1024/1024 optimal_mb, last_memory_used/1024/1024 used_mb, number_passes FROM v$sql_workarea WHERE number_passes > 0 ORDER BY last_memory_used DESC FETCH FIRST 10 ROWS ONLY",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "SQL 大排序 — 检查是否必要",
					Match:    MatchEquals("sql_sort"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 含排序操作（ORDER BY/GROUP BY）导致 TEMP 溢出"},
						{Desc: "检查排序是否业务必需，去掉不必要的 ORDER BY 可显著降低资源消耗"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查排序 SQL 的执行计划",
							RawSQL: "SELECT id, operation, options, object_name, ROUND(bytes/1024/1024) est_mb, ROUND(temp_space/1024/1024) temp_mb FROM v$sql_plan WHERE sql_id='{sql_id}' AND operation LIKE 'SORT%' ORDER BY id",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "去掉不必要的 ORDER BY 或限制返回行数",
							Risk: "需要修改应用层 SQL", Rollback: "无"},
					},
				},
				{
					Label:    "Hash Join 溢出 — build 端数据量过大",
					Match:    MatchEquals("sql_hash_join"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 含 Hash Join 操作，build 端数据量超过 PGA 可用内存导致溢出"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 PGA 或在 SQL 层面减少 join 数据量（加 WHERE 过滤）",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查 Hash Join 的实际行数 vs 预估行数",
							RawSQL: "SELECT id, operation, options, object_name, cardinality est_rows, ROUND(bytes/1024/1024) est_mb FROM v$sql_plan WHERE sql_id='{sql_id}' AND operation LIKE 'HASH%' ORDER BY id",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "默认 — TEMP 溢出原因不明",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "TEMP 表空间有溢出操作，建议检查 PGA 配置和大查询"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 TEMP 表空间消耗者",
							RawSQL: "SELECT s.sid, s.username, s.sql_id, u.tablespace, ROUND(u.blocks*8192/1024/1024) temp_mb FROM v$sort_usage u JOIN v$session s ON u.session_addr=s.saddr ORDER BY u.blocks DESC FETCH FIRST 10 ROWS ONLY",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"sql", "sort", "temp", "pga", "hash_join"},
		Versions: "10g+",
	}
}

// ─── WD023: Hot SQL diagnosis ───────────────────────────────────────────────

func ruleLiveHotSQL() *Rule {
	return &Rule{
		ID:       "WD023",
		Name:     "高并发热点 SQL 诊断",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{},
			SkipWhen: []SkipCondition{
				{Desc: "无高并发 SQL", Check: func(ctx *EvalContext) bool {
					for _, sql := range ctx.TopSQLs {
						if sql.MaxConcurrent >= 20 {
							return false
						}
					}
					return true
				}},
			},
		},
		Tree: &TreeNode{
			Step: "区分调用频繁 vs 单次执行慢",
			Check: func(ctx *EvalContext) interface{} {
				for _, sql := range ctx.TopSQLs {
					if sql.MaxConcurrent < 20 {
						continue
					}
					if sql.AvgElapsedSec > 1.0 {
						// Each execution is slow AND high concurrency → stacking.
						execs := sql.Executions
						if execs == 0 {
							execs = 1
						}
						diskPerExec := float64(sql.DiskReads) / float64(execs)
						bufPerExec := float64(sql.BufferGets) / float64(execs)
						if diskPerExec > 1000 {
							return "slow_physical_io"
						}
						if bufPerExec > 10000 {
							return "slow_logical_io"
						}
						return "slow_other"
					}
					if sql.AvgElapsedSec < 0.1 {
						return "fast_high_freq"
					}
					return "medium_cost"
				}
				return "none"
			},
			Branches: []Branch{
				{
					Label:    "慢 SQL 堆积 — 物理读多，缺索引或索引失效",
					Match:    MatchEquals("slow_physical_io"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Top SQL 单次执行慢（> 1s）且物理读多，高并发导致会话堆积"},
						{Desc: "物理读过多通常因缺少索引、索引失效、或统计信息过期导致执行计划选择全表扫描"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查该 SQL 的执行计划和索引使用情况",
							RawSQL: "SELECT id, operation, options, object_name, cost, cardinality, bytes, access_predicates, filter_predicates FROM v$sql_plan WHERE sql_id='{sql_id}' ORDER BY id",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "创建合适的索引或修复失效索引",
							RawSQL: "SELECT index_name, status FROM user_indexes WHERE table_name='{table}' AND status != 'VALID'",
							Risk:   "创建索引短暂锁表", Rollback: "DROP INDEX"},
					},
				},
				{
					Label:    "慢 SQL 堆积 — 逻辑读多，SQL 低效",
					Match:    MatchEquals("slow_logical_io"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 单次执行慢（> 1s）且逻辑读多但物理读不高，SQL 执行效率低"},
						{Desc: "可能原因：索引回表过多、不必要的全扫、或 SQL 逻辑可优化"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "优化 SQL：考虑覆盖索引减少回表、SQL 改写",
							RawSQL: "SELECT sql_id, plan_hash_value, executions, buffer_gets, ROUND(buffer_gets/GREATEST(executions,1)) bufs_per_exec FROM v$sqlarea WHERE sql_id='{sql_id}'",
							Risk:   "需修改 SQL", Rollback: "无"},
					},
				},
				{
					Label:    "慢 SQL 堆积 — 等待资源",
					Match:    MatchEquals("slow_other"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 单次执行慢（> 1s），但 IO 读不多，可能在等待锁或其他资源"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查该 SQL 的等待事件",
							RawSQL: "SELECT event, state, seconds_in_wait, sql_id FROM v$session WHERE sql_id='{sql_id}' AND status='ACTIVE'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "高频调用 — 单次快但调用密集",
					Match:    MatchEquals("fast_high_freq"),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Top SQL 单次执行快（< 100ms）但并发 > 20，应用层调用过于频繁"},
						{Desc: "高频调用消耗 CPU 和 library cache 资源，建议应用层缓存或合并请求"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "应用层增加缓存，减少重复查询",
							Risk: "需修改应用代码", Rollback: "无"},
						{Type: ActionPrevent, Desc: "考虑 Result Cache 或物化视图减少重复计算",
							RawSQL: "SELECT /*+ RESULT_CACHE */ ... -- 适用于结果稳定的查询",
							Risk:   "Result Cache 对高频 DML 表不适用", Rollback: "去掉 RESULT_CACHE hint"},
					},
				},
				{
					Label:    "中等成本 + 高并发 — 综合优化",
					Match:    MatchEquals("medium_cost"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Top SQL 单次成本适中（100ms-1s）但并发高，综合资源消耗大"},
						{Desc: "同时优化 SQL 效率和应用调用频率可获得最大收益"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "优化 SQL 降低单次成本",
							RawSQL: "SELECT sql_id, executions, ROUND(elapsed_time/GREATEST(executions,1)/1e6,3) avg_sec, buffer_gets, disk_reads FROM v$sqlarea WHERE sql_id='{sql_id}'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "应用层减少调用频率（批量、缓存、读写分离）",
							Risk: "需修改应用架构", Rollback: "无"},
					},
				},
				{
					Label:    "默认",
					Match:    MatchDefault(),
					Severity: SeverityLow,
				},
			},
		},
		Tags:     []string{"sql", "hot_sql", "concurrency", "performance"},
		Versions: "10g+",
	}
}

// convertAdvisorFindings converts SQL Advisor findings to rule engine format.
func convertAdvisorFindings(report *sqladvisor.SQLReport) *advisorResult {
	result := &advisorResult{}
	for _, f := range report.Findings {
		result.findings = append(result.findings, Finding{
			Desc: f.Summary + "：" + f.Detail,
		})
		for _, s := range f.Suggestions {
			actionType := ActionFix
			if f.Severity == "P3" {
				actionType = ActionPrevent
			}
			result.actions = append(result.actions, Action{
				Type:     actionType,
				Desc:     s.Action,
				RawSQL:   s.SQL,
				Risk:     s.Risk,
				Rollback: "",
			})
		}
	}
	return result
}
