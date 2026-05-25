/*-------------------------------------------------------------------------
 *
 * rules_wait_events.go
 *	  Oracle rule engine — core wait-event rules (cpu, log file sync, db file sequential read).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_wait_events.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"
)

// waitEventRules returns all 16 wait event diagnostic rules.
func waitEventRules() []*Rule {
	return []*Rule{
		ruleDBFileSequentialRead(),
		ruleDBFileScatteredRead(),
		ruleLogFileSync(),
		ruleLogFileParallelWrite(),
		ruleBufferBusyWaits(),
		ruleCursorPinSWaitOnX(),
		ruleCursorPinS(),
		ruleEnqTXRowLock(),
		ruleEnqTMContention(),
		ruleEnqHWContention(),
		ruleLibraryCacheLockPin(),
		ruleGCBufferBusy(),
		ruleReadByOtherSession(),
		ruleDBFileParallelWrite(),
		ruleDirectPathRead(),
		ruleFreeBufferWaits(),
		ruleResmgrCPUQuantum(),
	}
}

// ─── 1. db file sequential read ─────────────────────────────────────────────

func ruleDBFileSequentialRead() *Rule {
	return &Rule{
		ID:       "WE001",
		Name:     "db file sequential read 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file sequential read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file sequential read", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 1ms AND pct < 5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("db file sequential read") < 1 && ctx.WaitPct("db file sequential read") < 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 db file sequential read 平均等待时间",
			Query: QueryWaitAvgTime,
			Branches: []Branch{
				{
					Label: "avg_wait > 20ms — 存储 I/O 严重异常",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step:  "检查 Buffer Cache Hit Ratio",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "buffer_cache_hit_ratio") },
						Branches: []Branch{
							{
								Label: "Buffer Hit < 90% — 缓存不足",
								Match: MatchLT(90),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "db file sequential read 平均等待 > 20ms，存储 I/O 严重延迟"},
									{Desc: "Buffer Cache Hit Ratio < 90%，大量物理读"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 DB_CACHE_SIZE 减少物理读",
										SkillCommand: "/param db_cache_size",
										RawSQL:       "SELECT size_for_estimate, estd_physical_read_factor FROM v$db_cache_advice ORDER BY size_for_estimate",
										Risk: "内存资源占用增加", Rollback: "ALTER SYSTEM SET DB_CACHE_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查 Top SQL 是否存在大量全表扫描",
										SkillCommand: "/ash top_sql event='db file sequential read'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='db file sequential read' AND sample_time > SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "Buffer Hit >= 90% — 存储本身慢",
								Match: MatchGTE(90),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "db file sequential read 平均等待 > 20ms，但 Buffer Hit 正常"},
									{Desc: "存储子系统 I/O 延迟过高，可能是磁盘故障或 SAN 拥塞"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查存储层延迟与 IOPS",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, phywrts, readtim, writetim, ROUND(readtim/GREATEST(phyrds,1),2) avg_read_ms FROM v$filestat ORDER BY avg_read_ms DESC",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "定位热点数据文件",
										SkillCommand: "/ash hot_object event='db file sequential read'",
										RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='db file sequential read' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "avg_wait 5-20ms — 需关注",
					Match: MatchBetween(5, 20),
					Then: &TreeNode{
						Step:  "检查热点对象",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Label: "存在热点对象",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "db file sequential read 平均等待 5-20ms，性能需关注"},
									{Desc: "存在热点对象导致集中物理读"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析热点对象是否缺少索引或统计信息过期",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT sql_id, sql_text FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$active_session_history WHERE event='db file sequential read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY COUNT(*) DESC FETCH FIRST 5 ROWS ONLY)",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "对热点表收集统计信息",
										SkillCommand: "/stats gather {table_name}",
										RawSQL:       "EXEC DBMS_STATS.GATHER_TABLE_STATS(ownname=>'{owner}', tabname=>'{table_name}', estimate_percent=>DBMS_STATS.AUTO_SAMPLE_SIZE, method_opt=>'FOR ALL COLUMNS SIZE AUTO')",
										Risk: "锁定表统计期间优化器可能选错计划", Rollback: "EXEC DBMS_STATS.RESTORE_TABLE_STATS('{owner}','{table_name}',SYSTIMESTAMP-INTERVAL '1' HOUR)"},
								},
							},
						},
					},
				},
				{
					Label: "avg_wait < 5ms — SSD 正常范围",
					Match: MatchLT(5),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "db file sequential read 平均等待 < 5ms，在 SSD 环境下属于正常"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控，如占比持续上升需排查 SQL",
							SkillCommand: "/ash top_sql event='db file sequential read'",
							RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='db file sequential read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"WE013"}, // db file parallel write (DBWR slow)
		CausesOf: []string{},
		Tags:     []string{"io", "physical_read", "index_scan"},
		Versions: "9i+",
	}
}

// ─── 2. db file scattered read ──────────────────────────────────────────────

func ruleDBFileScatteredRead() *Rule {
	return &Rule{
		ID:       "WE002",
		Name:     "db file scattered read 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file scattered read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file scattered read", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 1ms AND pct < 3%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("db file scattered read") < 1 && ctx.WaitPct("db file scattered read") < 3
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "判断系统类型：OLTP 还是 OLAP",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "workload_type") },
			Branches: []Branch{
				{
					Label: "OLTP 系统 — scattered read 不应占主导",
					Match: MatchEquals("OLTP"),
					Then: &TreeNode{
						Step:  "检查热点 SQL 是否缺少索引",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Label: "找到全表扫描 SQL",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "OLTP 系统中 db file scattered read 占比过高，存在大量全表扫描"},
								},
								Then: &TreeNode{
									Step:  "检查是否统计信息过期",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "stale_stats_pct") },
									Branches: []Branch{
										{
											Label: "统计信息过期 > 20%",
											Match: MatchGT(20),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "超过 20% 的表统计信息过期，优化器可能选择错误的全表扫描"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "收集过期统计信息",
													SkillCommand: "/stats gather_stale",
													RawSQL:       "EXEC DBMS_STATS.GATHER_DATABASE_STATS(options=>'GATHER STALE', estimate_percent=>DBMS_STATS.AUTO_SAMPLE_SIZE)",
													Risk: "大量统计信息收集可能影响性能", Rollback: "无需回滚，新统计信息会更准确"},
												{Type: ActionInvestigate, Desc: "分析全表扫描的 Top SQL",
													SkillCommand: "/ash top_sql event='db file scattered read'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='db file scattered read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "统计信息正常 — 需要创建索引",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "统计信息正常但仍有全表扫描，可能缺少必要索引"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "分析 SQL 并创建缺失索引",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "创建索引需要额外存储和维护开销", Rollback: "DROP INDEX {index_name}"},
												{Type: ActionInvestigate, Desc: "使用 SQL Tuning Advisor 分析",
													SkillCommand: "/sqltune {sql_id}",
													RawSQL:       "DECLARE l_task VARCHAR2(30); BEGIN l_task := DBMS_SQLTUNE.CREATE_TUNING_TASK(sql_id=>'{sql_id}'); DBMS_SQLTUNE.EXECUTE_TUNING_TASK(l_task); END;",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Label: "OLAP / 混合系统 — scattered read 可能正常",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查平均等待时间判断存储性能",
						Query: QueryWaitAvgTime,
						Branches: []Branch{
							{
								Label: "avg_wait > 20ms — 存储性能瓶颈",
								Match: MatchGT(20),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "OLAP 场景 scattered read 正常，但存储延迟偏高"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查存储吞吐量与 IOPS",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, readtim, ROUND(readtim/GREATEST(phyrds,1),2) avg_ms FROM v$filestat ORDER BY phyrds DESC",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "考虑使用并行查询或分区裁剪减少扫描量",
										SkillCommand: "/param parallel_max_servers",
										RawSQL:       "SHOW PARAMETER parallel_max_servers",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "avg_wait <= 20ms — 正常",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "OLAP/混合负载中 scattered read 存储延迟正常，无需干预"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "持续监控，关注全表扫描是否增长",
										SkillCommand: "/ash trend event='db file scattered read'",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event='db file scattered read' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io", "full_table_scan", "missing_index"},
		Versions: "9i+",
	}
}

// ─── 3. log file sync ──────────────────────────────────────────────────────

func ruleLogFileSync() *Rule {
	return &Rule{
		ID:       "WE003",
		Name:     "log file sync 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "log file sync"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "log file sync", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 1ms — 性能优秀无需诊断", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("log file sync") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 log file sync 平均等待时间",
			Query: QueryWaitAvgTime,
			Branches: []Branch{
				{
					Label: "avg_wait > 10ms — 严重延迟",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "对比 log file parallel write 延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("log file parallel write") },
						Branches: []Branch{
							{
								Label: "parallel write 也 > 10ms — 存储 I/O 瓶颈",
								Match: MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "log file sync > 10ms 且 log file parallel write > 10ms，Redo 写入存储严重延迟"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "将 Redo Log 迁移到低延迟存储 (SSD/NVMe)",
										SkillCommand: "/redo check",
										RawSQL:       "SELECT group#, member, type FROM v$logfile ORDER BY group#",
										Risk: "迁移 Redo 需要停库", Rollback: "迁移回原存储"},
									{Type: ActionInvestigate, Desc: "检查 Redo Log 大小和切换频率",
										SkillCommand: "/redo switch_history",
										RawSQL:       "SELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) switches FROM v$log_history WHERE first_time > SYSDATE-1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "parallel write < 5ms — LGWR 发布延迟/commit 风暴",
								Match: MatchLT(5),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "log file sync > 10ms 但 parallel write < 5ms，LGWR post/wait 延迟或 commit 过于频繁"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否存在 commit 风暴（高频小事务）",
										SkillCommand: "/ash top_sql event='log file sync'",
										RawSQL:       "SELECT sql_id, executions, elapsed_time/1e6 ela_s, (SELECT sql_text FROM v$sql sq WHERE sq.sql_id=s.sql_id AND ROWNUM=1) text FROM v$sqlstats s WHERE UPPER(sql_text) LIKE '%COMMIT%' ORDER BY executions DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "减少 commit 频率，使用批量提交",
										SkillCommand: "/param commit_write",
										RawSQL:       "ALTER SYSTEM SET COMMIT_WRITE='BATCH,NOWAIT' SCOPE=BOTH -- 仅用于非关键数据",
										Risk: "NOWAIT 可能丢失少量数据", Rollback: "ALTER SYSTEM SET COMMIT_WRITE='IMMEDIATE,WAIT' SCOPE=BOTH"},
								},
							},
							{
								Label: "其他 — 综合分析",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "log file sync > 10ms，需综合分析 LGWR 和存储状态"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 LGWR trace 日志",
										SkillCommand: "/redo check",
										RawSQL:       "SELECT value FROM v$diag_info WHERE name='Diag Trace'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "avg_wait 5-10ms — 需要关注",
					Match: MatchBetween(5, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "log file sync 平均 5-10ms，性能需要关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Redo Log 大小是否充足",
							SkillCommand: "/redo info",
							RawSQL:       "SELECT group#, bytes/1024/1024 size_mb, status FROM v$log ORDER BY group#",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查 log file parallel write 延迟",
							SkillCommand: "/wait detail event='log file parallel write'",
							RawSQL:       "SELECT event, total_waits, time_waited_micro/1e3 total_ms, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event IN ('log file sync','log file parallel write')",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg_wait 3-5ms — 可接受",
					Match: MatchBetween(3, 5),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "log file sync 平均 3-5ms，在可接受范围但可以优化"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "考虑增大 Redo Log 大小减少切换频率",
							SkillCommand: "/redo switch_history",
							RawSQL:       "SELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) switches FROM v$log_history WHERE first_time>SYSDATE-1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1",
							Risk: "增大 Redo Log 占用更多存储", Rollback: "恢复原 Redo Log 大小"},
					},
				},
				{
					Label: "avg_wait 1-3ms — 良好",
					Match: MatchBetween(1, 3),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "log file sync 平均 1-3ms，性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控即可",
							SkillCommand: "/wait trend event='log file sync'",
							RawSQL:       "SELECT TO_CHAR(begin_time,'HH24:MI') tm, ROUND(average/1e3,2) avg_ms FROM dba_hist_event_histogram WHERE event_name='log file sync' AND begin_time>SYSDATE-1 ORDER BY 1",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"WE004"}, // log file parallel write
		CausesOf: []string{},
		Tags:     []string{"redo", "commit", "lgwr"},
		Versions: "9i+",
	}
}

// ─── 4. log file parallel write ─────────────────────────────────────────────

func ruleLogFileParallelWrite() *Rule {
	return &Rule{
		ID:       "WE004",
		Name:     "log file parallel write 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "log file parallel write"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "log file parallel write", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 0.5ms — 优秀无需诊断", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("log file parallel write") < 0.5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 log file parallel write 平均等待时间",
			Query: QueryWaitAvgTime,
			Branches: []Branch{
				{
					Label: "avg_wait > 10ms — 严重存储瓶颈",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查 Redo Log 文件信息",
						Query: QueryRedoLogInfo,
						Branches: []Branch{
							{
								Label: "检查完毕 — 存储 I/O 严重瓶颈",
								Match: MatchDefault(),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "log file parallel write > 10ms，LGWR 写 Redo 存储严重延迟"},
									{Desc: "直接影响所有 COMMIT 操作（log file sync）"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "将 Redo Log 迁移到 SSD/NVMe 存储",
										SkillCommand: "/redo check",
										RawSQL:       "SELECT group#, member, type, status FROM v$logfile lf JOIN v$log l ON lf.group#=l.group# ORDER BY group#",
										Risk: "迁移 Redo 需要停库操作", Rollback: "迁移回原存储路径"},
									{Type: ActionFix, Desc: "增大 Redo Log 文件大小减少切换",
										SkillCommand: "/redo resize",
										RawSQL:       "-- 每小时切换 > 6次则需增大; 建议 1-4GB\nSELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) FROM v$log_history WHERE first_time>SYSDATE-1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1",
										Risk: "增大 Redo Log 占用存储空间", Rollback: "恢复原大小"},
								},
							},
						},
					},
				},
				{
					Label: "avg_wait 5-10ms — 存储性能下降",
					Match: MatchBetween(5, 10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "log file parallel write 5-10ms，Redo 存储性能不佳"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Redo Log 是否与数据文件共享磁盘",
							SkillCommand: "/redo check",
							RawSQL:       "SELECT member FROM v$logfile UNION ALL SELECT name FROM v$datafile",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "分离 Redo Log 到独立磁盘组",
							SkillCommand: "/asm check",
							RawSQL:       "SELECT name, type, total_mb, free_mb FROM v$asm_diskgroup",
							Risk: "需要停库操作", Rollback: "恢复原配置"},
					},
				},
				{
					Label: "avg_wait 1-5ms — 可接受",
					Match: MatchBetween(1, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "log file parallel write 1-5ms，Redo 写入可接受但可优化"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "检查 Redo Log 切换频率和大小",
							SkillCommand: "/redo switch_history",
							RawSQL:       "SELECT group#, bytes/1024/1024 mb, status FROM v$log ORDER BY group#",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg_wait 0.5-1ms — 良好",
					Match: MatchBetween(0.5, 1),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "log file parallel write < 1ms，Redo 写入性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控 Redo 写入趋势",
							SkillCommand: "/wait trend event='log file parallel write'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='log file parallel write'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE003"}, // log file sync
		Tags:     []string{"redo", "lgwr", "storage_io"},
		Versions: "9i+",
	}
}

// ─── 5. buffer busy waits ───────────────────────────────────────────────────

func ruleBufferBusyWaits() *Rule {
	return &Rule{
		ID:       "WE005",
		Name:     "buffer busy waits 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "buffer busy waits"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "buffer busy waits", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 1ms AND pct < 5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("buffer busy waits") < 1 && ctx.WaitPct("buffer busy waits") < 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 ASH P3 block class 分布",
			Query: QueryASHBlockClass,
			Branches: []Branch{
				{
					Label: "data_block — 数据块争用",
					Match: MatchEquals("data_block"),
					Then: &TreeNode{
						Step:  "检查热点对象类型",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Label: "索引热块",
								Match: MatchEquals("INDEX"),
								Then: &TreeNode{
									Step:  "检查 90-10 splits 比例",
									Query: QueryIndex9010Splits,
									Branches: []Branch{
										{
											Label: "90-10 splits > 80% — 单调递增索引右侧热块",
											Match: MatchGT(80),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "索引 90-10 splits > 80%，单调递增键值导致右侧叶块争用"},
												{Desc: "常见于序列或时间戳作为主键的表"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用 HASH 分区索引或 Reverse Key Index",
													SkillCommand: "/index analyze {index_name}",
													RawSQL:       "-- 创建反转键索引\n-- ALTER INDEX {index_name} REBUILD REVERSE;\n-- 或使用 HASH 分区\nSELECT index_name, leaf_blocks, pct_direct_access FROM dba_indexes WHERE index_name='{index_name}'",
													Risk: "反转键索引不支持范围扫描", Rollback: "ALTER INDEX {index_name} REBUILD NOREVERSE"},
												{Type: ActionInvestigate, Desc: "确认是否可以使用 HASH 分区表",
													SkillCommand: "/partition check {table_name}",
													RawSQL:       "SELECT table_name, partitioning_type, partition_count FROM dba_part_tables WHERE table_name='{table_name}'",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "50-50 splits 为主 — 正常分裂",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "索引热块但非单调递增，可能是高并发更新同一范围"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查并发 DML 集中的块",
													SkillCommand: "/ash block_concentrate event='buffer busy waits'",
													RawSQL:       "SELECT current_obj#, current_file#, current_block#, COUNT(*) waits FROM v$active_session_history WHERE event='buffer busy waits' AND sample_time>SYSDATE-1/24 GROUP BY current_obj#, current_file#, current_block# ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "表热块",
								Match: MatchEquals("TABLE"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "表数据块争用，高并发 INSERT/UPDATE 集中在少数块"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "启用 ASSM 或增大 PCTFREE 分散热点",
										SkillCommand: "/space check {table_name}",
										RawSQL:       "SELECT table_name, tablespace_name, pct_free, num_rows FROM dba_tables WHERE table_name='{table_name}'",
										Risk: "增大 PCTFREE 会增加存储消耗", Rollback: "ALTER TABLE {table_name} PCTFREE 原值"},
									{Type: ActionFix, Desc: "使用 HASH 分区分散并发写入",
										SkillCommand: "/partition recommend {table_name}",
										RawSQL:       "SELECT bytes/1024/1024 mb, blocks FROM dba_segments WHERE segment_name='{table_name}'",
										Risk: "分区改造需要维护窗口", Rollback: "无"},
								},
							},
							{
								Label: "其他对象类型",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "数据块争用在非常见对象类型上"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析热点块所属对象和访问模式",
										SkillCommand: "/ash hot_object event='buffer busy waits'",
										RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "segment_header — 段头争用",
					Match: MatchEquals("segment_header"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "段头块争用，通常由 MSSM 表空间中高并发 INSERT 引起"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "将表空间从 MSSM 迁移到 ASSM",
							SkillCommand: "/space tablespace_info",
							RawSQL:       "SELECT tablespace_name, segment_space_management FROM dba_tablespaces WHERE segment_space_management='MANUAL'",
							Risk: "需要重建表空间", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 FREELISTS 参数（MSSM 环境）",
							SkillCommand: "/space check {table_name}",
							RawSQL:       "SELECT table_name, freelists, freelist_groups FROM dba_tables WHERE table_name='{table_name}'",
							Risk: "需要重建表", Rollback: "ALTER TABLE {table_name} STORAGE(FREELISTS 原值)"},
					},
				},
				{
					Label: "undo_header / undo_block — Undo 争用",
					Match: MatchEquals("undo"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Undo 段头或块争用，Undo 表空间不足或 Undo 段过少"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Undo 表空间使用率",
							SkillCommand: "/undo check",
							RawSQL:       "SELECT tablespace_name, ROUND(SUM(bytes)/1024/1024) used_mb, (SELECT ROUND(SUM(bytes)/1024/1024) FROM dba_data_files WHERE tablespace_name='UNDOTBS1') total_mb FROM dba_undo_extents GROUP BY tablespace_name",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 Undo 表空间或调整 UNDO_RETENTION",
							SkillCommand: "/param undo_retention",
							RawSQL:       "ALTER SYSTEM SET UNDO_RETENTION=1800 SCOPE=BOTH",
							Risk: "增大 Undo 消耗更多存储", Rollback: "ALTER SYSTEM SET UNDO_RETENTION=原值 SCOPE=BOTH"},
					},
				},
				{
					Label: "默认 — buffer busy waits 诊断",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step: "检查 buffer busy waits 占比判断严重程度",
						Check: func(ctx *EvalContext) interface{} {
							pct := ctx.WaitPct("buffer busy waits")
							if pct > 30 {
								return "severe"
							}
							return "moderate"
						},
						Branches: []Branch{
							{
								Label:    "buffer busy waits > 30% — 热块争用严重",
								Match:    MatchEquals("severe"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "buffer busy waits 占比超过 30%，存在严重的热块争用"},
									{Desc: "大量会话等待同一数据块，通常由高并发 DML 集中在少数块导致"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "从 V$SEGMENT_STATISTICS 找出热点段",
										SkillCommand: "/ash hot_object event='buffer busy waits'",
										RawSQL:       "SELECT owner, object_name, object_type, statistic_name, value FROM v$segment_statistics WHERE statistic_name IN ('buffer busy waits','physical reads direct') AND value > 0 ORDER BY value DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "对热点表使用 HASH 分区分散并发写入",
										RawSQL: "-- 检查热点对象后考虑:\n-- 1. HASH 分区: ALTER TABLE {table} MODIFY PARTITION BY HASH(key) PARTITIONS 8;\n-- 2. Reverse Key 索引: ALTER INDEX {idx} REBUILD REVERSE;\n-- 3. 增大 INITRANS: ALTER TABLE {table} INITRANS 16;",
										Risk:   "分区改造需维护窗口，反转键索引不支持范围扫描", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 ASH 中 block class 详细分布",
										SkillCommand: "/ash block_class event='buffer busy waits'",
										RawSQL:       "SELECT DECODE(h.p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block', 4,'segment_header', 5,'save_undo_header', 6,'free_list', 7,'extent_map', 8,'bitmap_block', 9,'bitmap_index_block', h.p3) block_class, COUNT(*) waits FROM v$active_session_history h WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY h.p3 ORDER BY 2 DESC",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "buffer busy waits 在非典型 block class 上",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "buffer busy waits 在非典型 block class 上"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 ASH 中 block class 详细分布",
										SkillCommand: "/ash block_class event='buffer busy waits'",
										RawSQL:       "SELECT DECODE(h.p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block', 4,'segment_header', 5,'save_undo_header', 6,'free_list', 7,'extent_map', 8,'bitmap_block', 9,'bitmap_index_block', h.p3) block_class, COUNT(*) waits FROM v$active_session_history h WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY h.p3 ORDER BY 2 DESC",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"contention", "hot_block", "concurrency"},
		Versions: "9i+",
	}
}

// ─── 6. cursor: pin S wait on X ─────────────────────────────────────────────

func ruleCursorPinSWaitOnX() *Rule {
	return &Rule{
		ID:       "WE006",
		Name:     "cursor: pin S wait on X 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cursor: pin S wait on X"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cursor: pin S wait on X", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 2% 且无持续趋势", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("cursor: pin S wait on X") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查硬解析率和 literal SQL 比例",
			Query: QueryParseStats,
			Branches: []Branch{
				{
					Label: "literal SQL (executions=1) > 30% — 未使用绑定变量",
					Match: MatchGT(30),
					Then: &TreeNode{
						Step:  "检查版本计数（version count）",
						Query: QueryCursorStats,
						Branches: []Branch{
							{
								Label: "version_count > 200 — 子游标爆炸",
								Match: MatchGT(200),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "literal SQL > 30% 且 version_count > 200，子游标爆炸导致严重 mutex 争用"},
									{Desc: "cursor: pin S wait on X 是执行端等待编译端释放 mutex"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启用 CURSOR_SHARING=FORCE 紧急缓解",
										SkillCommand: "/param cursor_sharing",
										RawSQL:       "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
										Risk: "可能导致部分 SQL 执行计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "修改应用使用绑定变量",
										SkillCommand: "/ash top_sql event='cursor: pin S wait on X'",
										RawSQL:       "SELECT sql_id, version_count, executions, parse_calls FROM v$sqlarea WHERE version_count > 200 ORDER BY version_count DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "需要修改应用代码", Rollback: "无"},
								},
							},
							{
								Label: "version_count 正常 — 纯 literal SQL 问题",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "literal SQL > 30%，大量硬解析导致 library cache 和 mutex 争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "推动应用端使用绑定变量",
										SkillCommand: "/ash top_sql event='cursor: pin S wait on X'",
										RawSQL:       "SELECT sql_id, executions, parse_calls, ROUND(parse_calls/GREATEST(executions,1)*100,1) parse_ratio FROM v$sqlarea WHERE executions=1 ORDER BY parse_calls DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "需要修改应用代码", Rollback: "无"},
									{Type: ActionFix, Desc: "临时启用 CURSOR_SHARING=FORCE",
										SkillCommand: "/param cursor_sharing",
										RawSQL:       "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
										Risk: "可能影响部分执行计划", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Label: "硬解析比例 > 20% — 解析压力大",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step:  "检查 shared pool 大小",
						Query: QuerySPFreeMemory,
						Branches: []Branch{
							{
								Label: "shared pool free < 10% — 内存不足",
								Match: MatchLT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "硬解析率 > 20% 且 Shared Pool Free < 10%，内存不足加剧解析争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 SHARED_POOL_SIZE",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "SELECT pool, name, ROUND(bytes/1024/1024,1) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "增大 SGA 可能影响 PGA 可用内存", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值 SCOPE=BOTH"},
								},
							},
							{
								Label: "shared pool 充足 — 应用解析模式问题",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "硬解析率偏高但 Shared Pool 充足，应用解析模式需优化"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 session_cached_cursors 设置",
										SkillCommand: "/param session_cached_cursors",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('session_cached_cursors','open_cursors')",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "解析比例正常 — 检查热点 SQL 并发度",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step: "检查 Top SQL 并发度",
						Check: func(ctx *EvalContext) interface{} {
							var maxConc float64
							for _, sql := range ctx.TopSQLs {
								if float64(sql.MaxConcurrent) > maxConc {
									maxConc = float64(sql.MaxConcurrent)
								}
							}
							return maxConc
						},
						Branches: []Branch{
							{
								Label:    "热点 SQL 高并发（> 10）— mutex 热点争用",
								Match:    MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "解析率正常，但单条 SQL 并发数高（> 10），cursor mutex 串行化瓶颈"},
									{Desc: "不是 cache 问题，是热点 SQL 被过多会话同时执行"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "应用层缓存 / Result Cache / 减少并发",
										SkillCommand: "/ash top_sql event='cursor: pin S wait on X'",
										Risk: "需要应用层改造", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查是否有数据倾斜导致执行计划不稳定（bind peeking）",
										RawSQL:   "SELECT sql_id, child_number, plan_hash_value, executions, buffer_gets/GREATEST(executions,1) avg_gets FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$sqlarea WHERE version_count > 3 AND executions > 100) ORDER BY sql_id, child_number",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查列直方图和数据分布",
										RawSQL:   "SELECT table_name, column_name, num_distinct, density, histogram FROM dba_tab_col_statistics WHERE owner = USER AND histogram != 'NONE' ORDER BY table_name, column_name",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "并发度不高 — 通用 mutex 争用",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "cursor pin 争用，需定位 mutex holder SQL"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位 mutex holder SQL",
										SkillCommand: "/ash top_sql event='cursor: pin S wait on X'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE010"}, // library cache lock/pin
		CausesOf: []string{},
		Tags:     []string{"parse", "mutex", "literal_sql", "shared_pool"},
		Versions: "10g+",
	}
}

// enrichBlockerFromQuery uses QueryBlockerDetail to populate empty blocker fields.
func enrichBlockerFromQuery(ctx *EvalContext) {
	if len(ctx.BlockingChains) == 0 {
		return
	}
	// Only enrich if fields are empty (not pre-populated from burst).
	needEnrich := false
	for _, chain := range ctx.BlockingChains {
		if chain.BlockerEvent == "" && chain.BlockerCommand == 0 {
			needEnrich = true
			break
		}
	}
	if !needEnrich {
		return
	}
	result, err := ctx.ExecuteQuery(QueryBlockerDetail, nil)
	if err != nil || result == nil {
		return
	}
	m, ok := result.(map[string]interface{})
	if !ok {
		return
	}
	rows, ok := m["rows"].([]map[string]interface{})
	if !ok {
		return
	}
	// Build SID → detail lookup.
	type blockerInfo struct {
		command int
		status  string
		event   string
	}
	lookup := make(map[int]blockerInfo, len(rows))
	for _, row := range rows {
		sid := int(rowValueToFloat(row["sid"]))
		info := blockerInfo{
			command: int(rowValueToFloat(row["command"])),
			event:   fmt.Sprintf("%v", row["event"]),
			status:  fmt.Sprintf("%v", row["status"]),
		}
		lookup[sid] = info
	}
	// Enrich blocking chains.
	for i := range ctx.BlockingChains {
		chain := &ctx.BlockingChains[i]
		if info, ok := lookup[chain.RootSID]; ok {
			if chain.BlockerCommand == 0 {
				chain.BlockerCommand = info.command
			}
			if chain.BlockerEvent == "" {
				chain.BlockerEvent = info.event
			}
			if chain.BlockerStatus == "" {
				chain.BlockerStatus = info.status
			}
		}
	}
}

// blockerEventFindings generates dynamic findings showing each blocker's current
// wait event (SID + event). This makes it clear what the blocker itself is doing.
func blockerEventFindings(ctx *EvalContext) []Finding {
	var findings []Finding
	for _, chain := range ctx.BlockingChains {
		if chain.VictimCount == 0 {
			continue
		}
		event := chain.BlockerEvent
		if event == "" {
			event = "(未知)"
		}
		desc := fmt.Sprintf("Blocker (SID=%d, User=%s) 当前等待事件: %s",
			chain.RootSID, chain.RootUser, event)
		if chain.RootSQLID != "" && chain.RootSQLID != "-" {
			desc += fmt.Sprintf(", SQL_ID=%s", chain.RootSQLID)
		}
		if chain.VictimCount > 0 {
			desc += fmt.Sprintf(", 阻塞 %d 个会话", chain.VictimCount)
		}
		findings = append(findings, Finding{Desc: desc})
	}
	return findings
}

// hasDDLVictim checks if any top SQL waiting on TX lock contention is a DDL
// statement (ALTER, DROP, CREATE, TRUNCATE). This indicates DDL-waiting-for-DML.
func hasDDLVictim(ctx *EvalContext) bool {
	for _, sql := range ctx.TopSQLs {
		evt := strings.ToLower(sql.Event)
		if !strings.Contains(evt, "enq: tx") && !strings.Contains(evt, "row lock") {
			continue
		}
		upper := strings.ToUpper(strings.TrimSpace(sql.SQLText))
		if strings.HasPrefix(upper, "ALTER ") ||
			strings.HasPrefix(upper, "DROP ") ||
			strings.HasPrefix(upper, "CREATE ") ||
			strings.HasPrefix(upper, "TRUNCATE ") {
			return true
		}
	}
	return false
}

// ─── 7. enq: TX - row lock contention ──────────────────────────────────────

func ruleEnqTXRowLock() *Rule {
	return &Rule{
		ID:       "WE007",
		Name:     "enq: TX - row lock contention 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TX - row lock contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TX - row lock contention", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 100ms 且 pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("enq: TX - row lock contention") < 100 && ctx.WaitPct("enq: TX - row lock contention") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step: "分析 blocker 状态（空闲/活跃/也被阻塞/死锁）",
			Check: func(ctx *EvalContext) interface{} {
				// Enrich blocker details from query if fields not populated.
				enrichBlockerFromQuery(ctx)

				// Step 1: Detect circular blocking (deadlock pattern).
				if len(ctx.BlockingChains) > 1 {
					blockerSIDs := make(map[int]bool)
					for _, chain := range ctx.BlockingChains {
						blockerSIDs[chain.RootSID] = true
					}
					for _, chain := range ctx.BlockingChains {
						evt := strings.ToLower(chain.BlockerEvent)
						if strings.Contains(evt, "enq: tx") || strings.Contains(evt, "row lock") {
							for _, other := range ctx.BlockingChains {
								if other.RootSID != chain.RootSID && blockerSIDs[chain.RootSID] {
									return "deadlock"
								}
							}
						}
					}
				}
				// Also check enqueue_deadlocks metric.
				if m, ok := ctx.GetMetric("enqueue_deadlocks"); ok && m.Max > 0 {
					return "deadlock"
				}

				// Step 2: Analyze single blocker state.
				for _, chain := range ctx.BlockingChains {
					if chain.VictimCount == 0 {
						continue
					}
					cmd := chain.BlockerCommand
					if cmd == 1 || cmd == 9 || cmd == 11 || cmd == 12 || cmd == 15 || cmd == 39 || cmd == 40 || cmd == 85 {
						return "ddl_blocker"
					}
					evt := strings.ToLower(chain.BlockerEvent)
					if evt == "" {
						// If still empty after enrichment, check if blocker has SQL.
						if chain.RootSQLID != "" && chain.RootSQLID != "-" {
							return "active_blocker"
						}
						if hasDDLVictim(ctx) {
							return "ddl_victim_idle_blocker"
						}
						return "idle_blocker"
					}
					if strings.Contains(evt, "pl/sql lock timer") ||
						strings.Contains(evt, "sql*net message from client") ||
						strings.Contains(evt, "pipe get") ||
						strings.Contains(evt, "jobq slave wait") ||
						chain.BlockerStatus == "INACTIVE" {
						// Check if victim is running DDL (DDL waiting for DML to release lock).
						if hasDDLVictim(ctx) {
							return "ddl_victim_idle_blocker"
						}
						return "idle_blocker"
					}
					if strings.Contains(evt, "enq:") {
						return "chained_blocker"
					}
					return "active_blocker"
				}
				return "unknown"
			},
			Branches: []Branch{
				{
					Label:    "死锁 — 多个会话循环阻塞",
					Match:    MatchEquals("deadlock"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "检测到循环阻塞模式：多个 blocker 互相等待对方持有的锁"},
						{Desc: "Oracle 通常会自动检测并回滚其中一个事务（ORA-00060），但高频死锁影响吞吐"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查 enqueue_deadlocks 统计确认死锁频率",
							SkillCommand: "/sql \"SELECT name, value FROM v$sysstat WHERE name='enqueue deadlocks'\"",
							RawSQL:       "SELECT name, value FROM v$sysstat WHERE name='enqueue deadlocks'",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "统一事务中 DML 的表/行访问顺序，避免交叉锁定",
							SkillCommand: "/ash top_sql event='enq: TX - row lock contention'",
							RawSQL:       "SELECT sql_id, COUNT(*) cnt FROM v$active_session_history WHERE event='enq: TX - row lock contention' AND sample_time > SYSDATE - 1/24 GROUP BY sql_id ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "需要修改应用逻辑", Rollback: "无"},
						{Type: ActionPrevent, Desc: "在应用层增加重试逻辑处理 ORA-00060",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "DDL 与 DML 冲突 — blocker 在执行 DDL",
					Match:    MatchEquals("ddl_blocker"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Blocker 正在执行 DDL 操作（ALTER TABLE/CREATE INDEX 等），持有排他锁阻塞 DML"},
						{Desc: "DDL 需要获取表级排他锁，所有并发 DML 必须等待 DDL 完成"},
					},
					DynFindings: blockerEventFindings,
					Actions: []Action{
						{Type: ActionUrgent, Desc: "确认 DDL 操作是否可以推迟到低峰时段",
							SkillCommand: "/lock check",
							RawSQL:       "SELECT s.sid, s.serial#, s.username, s.sql_id, s.command, s.event FROM v$session s WHERE s.command IN (1,9,11,12,15,39,40,85) AND s.status='ACTIVE'",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "使用 ONLINE DDL 减少锁定时间",
							RawSQL: "ALTER TABLE {table_name} ADD COLUMN ... ONLINE;\n-- 或使用 DBMS_REDEFINITION 做在线重定义",
							Risk:   "ONLINE DDL 可能增加执行时间", Rollback: "无"},
						{Type: ActionUrgent, Desc: "紧急时终止 DDL 释放锁",
							RawSQL: "ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE;",
							Risk:   "DDL 回滚", Rollback: "重新执行 DDL"},
					},
				},
				{
					Label:    "DDL 等待 DML 释放锁 — 未提交 DML 阻塞 DDL",
					Match:    MatchEquals("ddl_victim_idle_blocker"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "DDL 操作（ALTER/DROP/CREATE/TRUNCATE）等待未提交 DML 事务释放行锁"},
						{Desc: "Blocker 持有行锁处于空闲状态，DDL 需要表级排他锁无法获取"},
					},
					DynFindings: blockerEventFindings,
					Actions: []Action{
						{Type: ActionUrgent, Desc: "Kill 空闲 blocker 会话使 DDL 得以继续",
							SkillCommand: "/lock check",
							RawSQL:       "SELECT s.sid, s.serial#, s.username, s.event, s.last_call_et idle_sec FROM v$session s WHERE s.sid IN (SELECT DISTINCT blocking_session FROM v$session WHERE blocking_session IS NOT NULL)",
							Risk: "Kill 会话导致事务回滚", Rollback: "无"},
						{Type: ActionPrevent, Desc: "在维护窗口执行 DDL，避免与在线 DML 冲突",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "使用 ONLINE DDL 减少锁等待",
							RawSQL: "-- ALTER TABLE ... MOVE ONLINE;\n-- ALTER INDEX ... REBUILD ONLINE;\n-- 使用 DBMS_REDEFINITION 做在线重定义",
							Risk:   "ONLINE DDL 可能增加执行时间", Rollback: "无"},
					},
				},
				{
					Label:    "blocker 空闲/睡眠 — 未提交事务持锁",
					Match:    MatchEquals("idle_blocker"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Blocker 处于空闲/睡眠状态，持有未提交事务阻塞其他会话"},
						{Desc: "空闲 blocker 不会自行释放锁，必须外部干预（kill session 或通知应用提交）"},
					},
					DynFindings: blockerEventFindings,
					Actions: []Action{
						{Type: ActionUrgent, Desc: "Kill 空闲 blocker 会话释放锁",
							SkillCommand: "/lock check",
							RawSQL:       "SELECT s.sid, s.serial#, s.username, s.event, s.last_call_et idle_sec FROM v$session s WHERE s.sid IN (SELECT DISTINCT blocking_session FROM v$session WHERE event='enq: TX - row lock contention' AND blocking_session IS NOT NULL)",
							Risk: "Kill 会话导致事务回滚", Rollback: "无"},
						{Type: ActionFix, Desc: "修复应用在 DML 后及时 COMMIT，设置 IDLE_TIME profile 限制",
							RawSQL: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME 10;",
							Risk:   "影响所有使用 DEFAULT profile 的用户", Rollback: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME UNLIMITED;"},
					},
				},
				{
					Label:    "blocker 也被阻塞 — 多层阻塞链或死锁",
					Match:    MatchEquals("chained_blocker"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Blocker 本身也在等待另一个锁，形成多层阻塞链"},
						{Desc: "需要沿阻塞链追溯到最顶层 root blocker，处理中间节点无效"},
					},
					DynFindings: blockerEventFindings,
					Actions: []Action{
						{Type: ActionUrgent, Desc: "定位最顶层 root blocker 并处理",
							SkillCommand: "/lock check",
							RawSQL:       "SELECT LPAD(' ',2*(level-1)) || sid sid_tree, serial#, username, event, blocking_session FROM v$session START WITH blocking_session IS NULL AND sid IN (SELECT DISTINCT blocking_session FROM v$session WHERE blocking_session IS NOT NULL) CONNECT BY PRIOR sid = blocking_session ORDER SIBLINGS BY seconds_in_wait DESC",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查是否存在循环阻塞（死锁模式）",
							RawSQL: "SELECT a.sid, a.blocking_session FROM v$session a WHERE a.blocking_session IN (SELECT sid FROM v$session WHERE blocking_session = a.sid)",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label: "blocker 正在执行 SQL — 慢 SQL 持锁",
					Match: MatchEquals("active_blocker"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Blocker 正在执行 SQL，在 SQL 完成前不会释放行锁"},
						{Desc: "需要优化 blocker 的 SQL 或拆分大事务减少持锁时间"},
					},
					DynFindings: blockerEventFindings,
					Actions: []Action{
						{Type: ActionFix, Desc: "优化 blocker 的 SQL（检查执行计划）",
							SkillCommand: "/lock check",
							RawSQL:       "SELECT s.sid, s.sql_id, q.elapsed_time/GREATEST(q.executions,1)/1e6 avg_sec, SUBSTR(q.sql_text,1,200) FROM v$session s LEFT JOIN v$sql q ON s.sql_id=q.sql_id AND q.child_number=0 WHERE s.sid IN (SELECT DISTINCT blocking_session FROM v$session WHERE event='enq: TX - row lock contention' AND blocking_session IS NOT NULL)",
							Risk: "需分析 SQL", Rollback: "无"},
						{Type: ActionUrgent, Desc: "紧急时 Kill blocker 会话",
							RawSQL: "ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE;",
							Risk:   "事务回滚", Rollback: "无"},
					},
				},
				{
					Label: "无法确定 blocker 状态 — 回退到 ASH 分析",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 TX 锁模式（ASH P1TEXT/request mode）",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "tx_request_mode") },
						Branches: []Branch{
				{
					Label: "mode=6 — 行级锁争用",
					Match: MatchEquals("mode_6"),
					Then: &TreeNode{
						Step:  "检查 blocking session 长事务",
						Query: QueryLongTransactions,
						Branches: []Branch{
							{
								Label: "存在长事务 > 300s",
								Match: MatchGT(300),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "TX mode=6 行锁争用，存在长事务（> 5 分钟）未提交，阻塞其他会话"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并通知应用方提交或回滚长事务",
										SkillCommand: "/lock check",
										RawSQL:       "SELECT s.sid, s.serial#, s.username, s.program, t.start_time, t.used_ublk FROM v$session s JOIN v$transaction t ON s.saddr=t.ses_addr WHERE (SYSDATE - TO_DATE(t.start_time,'MM/DD/YY HH24:MI:SS'))*86400 > 300 ORDER BY t.start_time",
										Risk: "终止会话可能导致事务回滚", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "分析被锁行和涉及的 SQL",
										SkillCommand: "/ash blocking_chains",
										RawSQL:       "SELECT blocking_session, sid, sql_id, event, seconds_in_wait FROM v$session WHERE event LIKE 'enq: TX%' AND state='WAITING' ORDER BY seconds_in_wait DESC",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "短事务也有争用 — 应用设计问题",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "TX mode=6 行锁争用，多个会话并发更新相同行"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位争用热点行和 SQL",
										SkillCommand: "/ash top_sql event='enq: TX - row lock contention'",
										RawSQL:       "SELECT h.sql_id, h.current_obj#, COUNT(*) waits FROM v$active_session_history h WHERE h.event='enq: TX - row lock contention' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.current_obj# ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "优化应用逻辑减少行级锁持有时间",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
										Risk: "需要修改应用代码", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "mode=4 — ITL / Unique Key / Bitmap 争用",
					Match: MatchEquals("mode_4"),
					Then: &TreeNode{
						Step:  "检查热点对象是否有 Unique 约束或 Bitmap 索引",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Label: "存在 Bitmap 索引",
								Match: MatchEquals("BITMAP_INDEX"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "TX mode=4，Bitmap 索引在 DML 高并发时产生大量锁争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "OLTP 环境中将 Bitmap 索引改为 B-Tree 索引",
										SkillCommand: "/index analyze {index_name}",
										RawSQL:       "SELECT index_name, index_type, table_name FROM dba_indexes WHERE index_type LIKE 'BITMAP%'",
										Risk: "B-Tree 索引占用更多空间", Rollback: "重建为 Bitmap 索引"},
								},
							},
							{
								Label: "ITL 争用或唯一键冲突",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "TX mode=4，可能是 ITL 不足或唯一键冲突"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大热点表的 INITRANS 避免 ITL 争用",
										SkillCommand: "/space check {table_name}",
										RawSQL:       "SELECT table_name, ini_trans, max_trans FROM dba_tables WHERE table_name='{table_name}'",
										Risk: "需要重建表", Rollback: "ALTER TABLE {table_name} INITRANS 原值"},
									{Type: ActionInvestigate, Desc: "检查是否存在唯一键冲突",
										SkillCommand: "/ash top_sql event='enq: TX - row lock contention'",
										RawSQL:       "SELECT sql_id, sql_text FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$active_session_history WHERE event='enq: TX - row lock contention' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY COUNT(*) DESC FETCH FIRST 5 ROWS ONLY)",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "默认 — 通用 TX 争用诊断",
					Match: MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "enq: TX 争用，需进一步区分锁模式"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "从 ASH 获取 TX 锁的详细模式",
							SkillCommand: "/ash detail event='enq: TX - row lock contention'",
							RawSQL:       "SELECT DECODE(h.p1raw, NULL, 'unknown', SUBSTR(h.p1raw,1,8)) lock_mode, h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event LIKE 'enq: TX%' AND h.sample_time>SYSDATE-1/24 GROUP BY SUBSTR(h.p1raw,1,8), h.sql_id ORDER BY 3 DESC",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "row_lock", "transaction", "contention", "blocker_analysis"},
		Versions: "9i+",
	}
}

// ─── 8. enq: TM - contention ───────────────────────────────────────────────

func ruleEnqTMContention() *Rule {
	return &Rule{
		ID:       "WE008",
		Name:     "enq: TM - contention 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TM - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TM - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1% 且非持续性", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: TM - contention") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查外键是否缺少索引（60%+ 根因）",
			Query: QueryFKNoIndex,
			Branches: []Branch{
				{
					Label: "存在外键无索引",
					Match: MatchGT(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在外键约束缺少索引，DML 父表时会锁住整个子表（TM 锁）"},
						{Desc: "这是 enq: TM contention 60% 以上的根因"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "为缺少索引的外键列创建索引",
							SkillCommand: "/fk check_index",
							RawSQL:       "SELECT c.table_name child_table, cc.column_name fk_column, c.r_constraint_name parent_constraint FROM dba_constraints c JOIN dba_cons_columns cc ON c.constraint_name=cc.constraint_name WHERE c.constraint_type='R' AND NOT EXISTS (SELECT 1 FROM dba_ind_columns ic WHERE ic.table_name=cc.table_name AND ic.column_name=cc.column_name)",
							Risk: "创建索引需要额外存储空间", Rollback: "DROP INDEX {index_name}"},
						{Type: ActionInvestigate, Desc: "检查 TM 锁涉及的对象",
							SkillCommand: "/lock detail type='TM'",
							RawSQL:       "SELECT o.object_name, l.session_id, l.locked_mode FROM v$locked_object l JOIN dba_objects o ON l.object_id=o.object_id WHERE o.object_id IN (SELECT p1-1 FROM v$active_session_history WHERE event='enq: TM - contention' AND sample_time>SYSDATE-1/24)",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "外键索引完整 — DDL 冲突",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查是否有 DDL 操作导致 TM 锁",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "ddl_sessions") },
						Branches: []Branch{
							{
								Label: "存在 DDL 操作",
								Match: MatchGT(0),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "DDL 操作（如 ALTER TABLE）持有排他 TM 锁，阻塞 DML"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位正在执行的 DDL 会话",
										SkillCommand: "/lock check",
										RawSQL:       "SELECT s.sid, s.serial#, s.username, s.sql_id, q.sql_text FROM v$session s LEFT JOIN v$sql q ON s.sql_id=q.sql_id WHERE s.command IN (1,2,3,4,9,11,15,39,40) AND s.status='ACTIVE'",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "使用 DDL_LOCK_TIMEOUT 避免立即失败",
										SkillCommand: "/param ddl_lock_timeout",
										RawSQL:       "ALTER SESSION SET DDL_LOCK_TIMEOUT=30",
										Risk: "DDL 等待期间阻塞加重", Rollback: "ALTER SESSION SET DDL_LOCK_TIMEOUT=0"},
								},
							},
							{
								Label: "无明显 DDL — 检查 DML 交叉锁定",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "TM 锁争用但无明显 DDL，可能是并发 DML 交叉锁定"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析 TM 锁的持有者和等待者",
										SkillCommand: "/lock tree",
										RawSQL:       "SELECT l1.sid holder, l2.sid waiter, o.object_name FROM v$lock l1 JOIN v$lock l2 ON l1.id1=l2.id1 AND l1.id2=l2.id2 JOIN dba_objects o ON l1.id1=o.object_id WHERE l1.type='TM' AND l1.lmode>0 AND l2.request>0",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "table_lock", "foreign_key", "ddl"},
		Versions: "9i+",
	}
}

// ─── 9. enq: HW - contention ───────────────────────────────────────────────

func ruleEnqHWContention() *Rule {
	return &Rule{
		ID:       "WE009",
		Name:     "enq: HW - contention 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: HW - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: HW - contention", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "ASH 占比 < 2%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: HW - contention") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 ASH 中 HW contention 占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("enq: HW - contention") },
			Branches: []Branch{
				{
					Label: "ASH 占比 > 10% — 严重高水位争用",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查并发 INSERT 会话数量",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "concurrent_insert_sessions") },
						Branches: []Branch{
							{
								Label: "并发 INSERT > 50 — 需要 HASH 分区",
								Match: MatchGT(50),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "高水位标记(HWM)争用严重，> 50 个并发 INSERT 会话争夺段扩展"},
									{Desc: "单一表的高水位扩展成为瓶颈"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将热点表改造为 HASH 分区表分散 INSERT",
										SkillCommand: "/partition recommend {table_name}",
										RawSQL:       "SELECT segment_name, segment_type, bytes/1024/1024 mb, extents FROM dba_segments WHERE segment_name IN (SELECT object_name FROM (SELECT o.object_name, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='enq: HW - contention' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name ORDER BY 2 DESC) WHERE ROWNUM=1)",
										Risk: "分区改造需要维护窗口和数据迁移", Rollback: "无"},
									{Type: ActionFix, Desc: "使用 DBMS_SPACE_ADMIN 预分配 extent",
										SkillCommand: "/space preallocate {table_name}",
										RawSQL:       "ALTER TABLE {table_name} ALLOCATE EXTENT (SIZE 100M)",
										Risk: "占用额外存储空间", Rollback: "空间不可回收，需 SHRINK"},
								},
							},
							{
								Label: "并发 INSERT <= 50 — 调整存储参数",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "HW 争用明显，但并发量中等，调整 extent 参数可缓解"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 extent 大小减少 HWM 推进频率",
										SkillCommand: "/space check {table_name}",
										RawSQL:       "SELECT table_name, tablespace_name, initial_extent, next_extent FROM dba_tables WHERE table_name='{table_name}'",
										Risk: "增大 extent 会多占存储", Rollback: "无"},
									{Type: ActionFix, Desc: "使用 UNIFORM extent 的 ASSM 表空间",
										SkillCommand: "/space tablespace_info",
										RawSQL:       "SELECT tablespace_name, extent_management, allocation_type, segment_space_management FROM dba_tablespaces",
										Risk: "需要迁移表到新表空间", Rollback: "迁移回原表空间"},
								},
							},
						},
					},
				},
				{
					Label: "ASH 占比 3-10% — 需要关注",
					Match: MatchBetween(3, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "HW contention 占比 3-10%，存在中等程度的高水位争用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "定位争用的热点段",
							SkillCommand: "/ash hot_object event='enq: HW - contention'",
							RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='enq: HW - contention' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "预分配空间减少 extent 扩展",
							SkillCommand: "/space preallocate {table_name}",
							RawSQL:       "ALTER TABLE {table_name} ALLOCATE EXTENT (SIZE 50M)",
							Risk: "占用额外存储", Rollback: "无"},
					},
				},
				{
					Label: "ASH 占比 < 3% — 低影响",
					Match: MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "HW contention 占比较低，暂无严重影响"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控趋势，关注是否在业务高峰恶化",
							SkillCommand: "/wait trend event='enq: HW - contention'",
							RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event='enq: HW - contention' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WD015"}, // HW contention causes downstream row cache lock
		Tags:     []string{"contention", "hwm", "insert", "extent"},
		Versions: "10g+",
	}
}

// ─── 10. library cache lock / pin ───────────────────────────────────────────

func ruleLibraryCacheLockPin() *Rule {
	return &Rule{
		ID:       "WE010",
		Name:     "library cache lock/pin 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "library cache lock"},
			{Type: SignalWaitEvent, Key: "library cache pin"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "library cache lock", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "两个事件占比合计 < 2%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("library cache lock")+ctx.WaitPct("library cache pin") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查硬解析率",
			Check: func(ctx *EvalContext) interface{} {
				result, err := ctx.ExecuteQuery(QueryParseStats, nil)
				if err == nil && result != nil {
					if pct := ExtractHardParsePct(result); pct >= 0 {
						return pct
					}
				}
				if v := ctx.MetricValue("hard_parse_pct"); v > 0 {
					return v
				}
				if ctx.WaitPct("latch: shared pool") > 20 {
					return float64(10)
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Label: "硬解析 > 5% — 解析压力大",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查 RELOADS 频率",
						Query: QueryLatchStats,
						Branches: []Branch{
							{
								Label: "RELOADS > 1000/hr — 严重重载",
								Match: MatchGT(1000),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "硬解析 > 5% 且 RELOADS > 1000/hr，library cache 严重不足"},
									{Desc: "频繁的游标失效和重载导致 library cache lock/pin 争用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 SHARED_POOL_SIZE",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "SELECT pool, name, ROUND(bytes/1024/1024) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "增大 SGA 影响 PGA 可用空间", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionFix, Desc: "使用绑定变量减少硬解析",
										SkillCommand: "/ash top_sql event='library cache lock'",
										RawSQL:       "SELECT sql_id, executions, parse_calls, version_count FROM v$sqlarea WHERE parse_calls > 100 ORDER BY parse_calls DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "需要修改应用代码", Rollback: "无"},
								},
							},
							{
								Label: "RELOADS 正常 — Shared Pool 碎片化",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "硬解析偏高但 RELOADS 正常，Shared Pool 可能碎片化"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 Shared Pool 碎片情况",
										SkillCommand: "/sga check",
										RawSQL:       "SELECT ksmchcls class, COUNT(*) chunks, SUM(ksmchsiz) total_bytes FROM x$ksmsp GROUP BY ksmchcls ORDER BY 3 DESC",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "Flush Shared Pool 释放碎片（低峰时段）",
										SkillCommand: "/sga flush shared_pool",
										RawSQL:       "ALTER SYSTEM FLUSH SHARED_POOL",
										Risk: "导致所有已缓存游标失效需重解析", Rollback: "无法回滚"},
								},
							},
						},
					},
				},
				{
					Label: "硬解析 <= 5% — 检查 DDL 冲突",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查是否有 DDL 操作导致 library cache invalidation",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "ddl_sessions") },
						Branches: []Branch{
							{
								Label: "存在 DDL 操作 — DDL 导致 library cache 失效",
								Match: MatchGT(0),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "DDL 操作持有 library cache exclusive lock，阻塞所有依赖对象的 SQL 执行"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位 DDL 会话和操作",
										SkillCommand: "/lock check",
										RawSQL:       "SELECT s.sid, s.serial#, s.username, s.sql_id, s.event FROM v$session s WHERE s.command IN (1,2,3,4,9,11,15,39,40)",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "使用 DBMS_REDEFINITION 做在线重定义避免锁定",
										SkillCommand: "/redef check {table_name}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_REDEFINITION.CAN_REDEF_TABLE('{owner}', '{table_name}'))",
										Risk: "在线重定义需额外空间", Rollback: "DBMS_REDEFINITION.ABORT_REDEF_TABLE"},
								},
							},
							{
								Label: "无 DDL — 检查 library cache lock/pin 是否为主要等待",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "判断 library cache lock/pin 占比是否足以支撑诊断",
									Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("library cache lock") + ctx.WaitPct("library cache pin") },
									Branches: []Branch{
										{
											Label:    "占比 > 10% — 并发解析争用",
											Match:    MatchGT(10),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "无明显 DDL 冲突，library cache lock/pin 占比高，高并发解析同一对象"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 library cache 等待的对象",
													SkillCommand: "/ash detail event='library cache lock'",
													RawSQL:       "SELECT h.sql_id, h.current_obj#, COUNT(*) waits FROM v$active_session_history h WHERE h.event IN ('library cache lock','library cache pin') AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.current_obj# ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label:    "占比低 — 非主要瓶颈，跳过",
											Match:    MatchDefault(),
											Severity: SeverityLow,
											Findings: []Finding{{Desc: "library cache lock/pin 占比低，非当前主要瓶颈"}},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE006", "ORA_111"}, // cursor: pin S wait on X; V$ monitoring is downstream
		Tags:     []string{"parse", "library_cache", "shared_pool", "ddl"},
		Versions: "9i+",
	}
}

// ─── 11. gc buffer busy (RAC) ───────────────────────────────────────────────

func ruleGCBufferBusy() *Rule {
	return &Rule{
		ID:       "WE011",
		Name:     "gc buffer busy 诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc buffer busy acquire"},
			{Type: SignalWaitEvent, Key: "gc buffer busy release"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc buffer busy acquire", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查互联网络（Interconnect）延迟",
			Query: QueryInterconnectStats,
			Branches: []Branch{
				{
					Label: "interconnect avg > 1ms — 网络瓶颈",
					Match: MatchGT(1),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "RAC 互联网络延迟 > 1ms，跨实例块传输严重延迟"},
						{Desc: "gc buffer busy 的根因通常是网络而非数据库"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查 Interconnect 网络带宽和错误",
							SkillCommand: "/rac interconnect",
							RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "确认 Interconnect 未使用公网",
							SkillCommand: "/rac check",
							RawSQL:       "SELECT name, value FROM v$parameter WHERE name LIKE 'cluster_interconnects'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "interconnect avg <= 1ms — 热块跨实例争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查热点块和对象",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Label: "存在热点对象",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "互联网络正常，gc buffer busy 由热块跨实例访问引起"},
									{Desc: "多个实例并发读写同一数据块"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置 Service Affinity 将相关业务集中到同一实例",
										SkillCommand: "/rac service check",
										RawSQL:       "SELECT name, inst_id, goal, clb_goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
										Risk: "变更 Service 需要应用配合切换", Rollback: "恢复原 Service 配置"},
									{Type: ActionFix, Desc: "使用 HASH 分区分散热点块",
										SkillCommand: "/partition recommend {table_name}",
										RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) gc_waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event LIKE 'gc buffer busy%' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "分区改造需要维护窗口", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"rac", "interconnect", "hot_block", "gc"},
		Versions: "10g+ RAC",
	}
}

// ─── 12. read by other session ──────────────────────────────────────────────

func ruleReadByOtherSession() *Rule {
	return &Rule{
		ID:       "WE012",
		Name:     "read by other session 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "read by other session"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "read by other session", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 1ms 且 pct < 3%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("read by other session") < 1 && ctx.WaitPct("read by other session") < 3
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 db file sequential read 平均延迟（存储速度）",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("db file sequential read") },
			Branches: []Branch{
				{
					Label: "sequential read > 10ms — 存储慢导致排队",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查 Buffer Cache Hit Ratio",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "buffer_cache_hit_ratio") },
						Branches: []Branch{
							{
								Label: "Buffer Hit < 90% — 缓存严重不足",
								Match: MatchLT(90),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "存储延迟 > 10ms 且 Buffer Hit < 90%，大量物理读导致多会话排队等待同一块"},
									{Desc: "read by other session = 别的会话正在读同一块，当前会话排队等待"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 DB_CACHE_SIZE 提升命中率",
										SkillCommand: "/param db_cache_size",
										RawSQL:       "SELECT size_for_estimate, estd_physical_read_factor, estd_physical_reads FROM v$db_cache_advice ORDER BY size_for_estimate",
										Risk: "增大 SGA 减少 PGA 可用空间", Rollback: "ALTER SYSTEM SET DB_CACHE_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查存储 I/O 延迟",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, ROUND(readtim/GREATEST(phyrds,1),2) avg_ms FROM v$filestat ORDER BY avg_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "Buffer Hit >= 90% — 存储本身慢",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Buffer Hit 正常但存储延迟高，物理读慢导致 read by other session 排队"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查存储层性能",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, phywrts, readtim, ROUND(readtim/GREATEST(phyrds,1),2) avg_ms FROM v$filestat ORDER BY avg_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "定位热点对象",
										SkillCommand: "/ash hot_object event='read by other session'",
										RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='read by other session' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "sequential read <= 10ms — 高并发读同一块",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查热点块集中度",
						Query: QueryASHBlockConcentrate,
						Branches: []Branch{
							{
								Label: "热点块集中 — 同一块被大量并发访问",
								Match: MatchGT(50),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存储延迟正常，但大量会话并发读同一物理块"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析热点 SQL 是否可以缓存结果",
										SkillCommand: "/ash top_sql event='read by other session'",
										RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='read by other session' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "考虑使用 Result Cache 缓存热点查询结果",
										SkillCommand: "/param result_cache_max_size",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name LIKE 'result_cache%'",
										Risk: "Result Cache 增加内存开销", Rollback: "ALTER SYSTEM SET RESULT_CACHE_MAX_SIZE=0 SCOPE=BOTH"},
								},
							},
							{
								Label: "热点分散 — 普通并发物理读",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "read by other session 热点分散，多对象并发物理读"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查整体 I/O 负载分布",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, phywrts FROM v$filestat ORDER BY phyrds DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE001", "WE013"}, // sequential read, parallel write (DBWR)
		CausesOf: []string{},
		Tags:     []string{"io", "concurrency", "buffer_cache"},
		Versions: "10g+",
	}
}

// ─── 13. db file parallel write ─────────────────────────────────────────────

func ruleDBFileParallelWrite() *Rule {
	return &Rule{
		ID:       "WE013",
		Name:     "db file parallel write 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file parallel write"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file parallel write", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "avg wait < 2ms 且 pct < 2%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitAvgMs("db file parallel write") < 2 && ctx.WaitPct("db file parallel write") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 db file parallel write 平均等待时间",
			Query: QueryWaitAvgTime,
			Branches: []Branch{
				{
					Label: "avg_wait > 10ms — DBWR 写入严重延迟",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查是否存在 free buffer waits",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("free buffer waits") },
						Branches: []Branch{
							{
								Label: "free buffer waits 占比 > 2% — DBWR 慢导致脏缓冲积压",
								Match: MatchGT(2),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "db file parallel write > 10ms，DBWR 写入严重延迟"},
									{Desc: "同时出现 free buffer waits，脏缓冲无法及时写出，新读取无法获得空闲缓冲"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查存储 I/O 性能，DBWR 写入受阻",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phyrds, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_write_ms FROM v$filestat ORDER BY avg_write_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "启用异步 I/O 提升 DBWR 效率",
										SkillCommand: "/param disk_asynch_io",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('disk_asynch_io','filesystemio_options','db_writer_processes')",
										Risk: "变更 I/O 参数可能需要重启", Rollback: "恢复原参数值"},
								},
							},
							{
								Label: "无 free buffer waits — 单纯存储慢",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "db file parallel write > 10ms，存储写入慢但尚未严重影响缓冲池"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查数据文件写延迟分布",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_ms FROM v$filestat WHERE phywrts>0 ORDER BY avg_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增加 DB_WRITER_PROCESSES 并行写入",
										SkillCommand: "/param db_writer_processes",
										RawSQL:       "SHOW PARAMETER db_writer_processes\n-- 建议设置为 CPU 数的 1/8，最大不超过 36",
										Risk: "需要重启数据库", Rollback: "ALTER SYSTEM SET DB_WRITER_PROCESSES=原值 SCOPE=SPFILE"},
								},
							},
						},
					},
				},
				{
					Label: "avg_wait 5-10ms — DBWR 写性能下降",
					Match: MatchBetween(5, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "db file parallel write 5-10ms，DBWR 写入延迟偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查异步 I/O 配置",
							SkillCommand: "/param filesystemio_options",
							RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('disk_asynch_io','filesystemio_options')",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查存储层是否有写入瓶颈",
							SkillCommand: "/io check",
							RawSQL:       "SELECT name, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_ms FROM v$filestat WHERE phywrts>0 ORDER BY avg_ms DESC",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg_wait < 5ms — 正常",
					Match: MatchLT(5),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "db file parallel write < 5ms，DBWR 写入正常"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控写入延迟趋势",
							SkillCommand: "/wait trend event='db file parallel write'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='db file parallel write'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE001", "WE012", "WE015"}, // sequential read, read by other session, free buffer waits
		Tags:     []string{"io", "dbwr", "write", "storage"},
		Versions: "9i+",
	}
}

// ─── 14. direct path read ───────────────────────────────────────────────────

func ruleDirectPathRead() *Rule {
	return &Rule{
		ID:       "WE014",
		Name:     "direct path read 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "direct path read"},
			{Type: SignalWaitEvent, Key: "direct path read temp"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "direct path read", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 5% 且非 OLTP 系统", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("direct path read") < 5 && ctx.GetStr("metrics", "workload_type") != "OLTP"
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "判断 direct path read 类型（数据文件 vs temp）",
			Check: func(ctx *EvalContext) interface{} {
				tempPct := ctx.WaitPct("direct path read temp")
				dataPct := ctx.WaitPct("direct path read")
				if tempPct > dataPct {
					return "temp"
				}
				return "data"
			},
			Branches: []Branch{
				{
					Label: "temp — 临时表空间排序/HASH溢出",
					Match: MatchEquals("temp"),
					Then: &TreeNode{
						Step:  "检查 PGA 使用情况",
						Query: QueryPGAAdvice,
						Branches: []Branch{
							{
								Label: "PGA 不足导致磁盘排序",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "direct path read temp 占主导，大量排序/HASH JOIN 溢出到临时表空间"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET 减少磁盘排序",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT PGA_TARGET_FOR_ESTIMATE/1024/1024 target_mb, ESTD_EXTRA_BYTES_RW, ESTD_PGA_CACHE_HIT_PERCENTAGE hit_pct FROM v$pga_target_advice ORDER BY 1",
										Risk: "增大 PGA 减少 SGA 可用空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "找出消耗大量 temp 的 SQL",
										SkillCommand: "/ash top_sql event='direct path read temp'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path read temp' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "data — 数据文件 direct path read",
					Match: MatchEquals("data"),
					Then: &TreeNode{
						Step:  "检查系统负载类型",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "workload_type") },
						Branches: []Branch{
							{
								Label: "OLTP 系统 — direct path read 不应占主导",
								Match: MatchEquals("OLTP"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "OLTP 系统出现大量 direct path read，存在大表全扫绕过 buffer cache"},
									{Desc: "Oracle 11g+ 大表自动 direct path read（表 > buffer cache 2%）"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "找出触发 direct path read 的大表扫描 SQL",
										SkillCommand: "/ash top_sql event='direct path read'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "对大表创建索引避免全表扫描",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
										Risk: "创建索引需要额外存储", Rollback: "DROP INDEX {index_name}"},
								},
							},
							{
								Label: "OLAP / 混合 — direct path read",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step: "检查是否多并发全表扫描导致 IO 压力",
									Check: func(ctx *EvalContext) interface{} {
										active := float64(ctx.PeakActive)
										if active == 0 {
											active = ctx.MetricValue("active_sessions")
										}
										dprPct := ctx.WaitPct("direct path read")
										if active > 10 && dprPct > 50 {
											return "high_concurrency"
										}
										return "normal"
									},
									Branches: []Branch{
										{
											Label:    "多并发全表扫描导致 IO 压力",
											Match:    MatchEquals("high_concurrency"),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "多并发会话同时执行全表扫描（direct path read > 50%，活跃会话 > 10），导致 IO 压力"},
												{Desc: "大量并发 direct path read 绕过 buffer cache，IO 子系统成为瓶颈"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "找出并发执行全表扫描的 SQL",
													SkillCommand: "/ash top_sql event='direct path read'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits, COUNT(DISTINCT session_id) sessions FROM v$active_session_history WHERE event='direct path read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "为高频全表扫描 SQL 创建索引或分区",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "索引/分区需要额外存储和维护", Rollback: "DROP INDEX {index_name}"},
												{Type: ActionFix, Desc: "限制并行度或调度错峰执行",
													RawSQL: "ALTER SYSTEM SET PARALLEL_MAX_SERVERS=<合理值> SCOPE=BOTH;\n-- 或在 Resource Manager 中限制并发",
													Risk:   "可能影响批量作业执行时间", Rollback: "ALTER SYSTEM SET PARALLEL_MAX_SERVERS=原值 SCOPE=BOTH"},
											},
										},
										{
											Label:    "OLAP/混合 — direct path read 可能正常",
											Match:    MatchDefault(),
											Severity: SeverityLow,
											Findings: []Finding{
												{Desc: "OLAP/混合负载中 direct path read 是正常行为，大表扫描绕过 buffer cache 避免污染"},
											},
											Actions: []Action{
												{Type: ActionPrevent, Desc: "确认是否有异常大表扫描拖慢系统",
													SkillCommand: "/ash top_sql event='direct path read'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io", "full_table_scan", "temp", "pga", "direct_read"},
		Versions: "9i+",
	}
}

// ─── 15. free buffer waits ──────────────────────────────────────────────────

func ruleFreeBufferWaits() *Rule {
	return &Rule{
		ID:       "WE015",
		Name:     "free buffer waits 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "free buffer waits"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "free buffer waits", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1% — 偶发可忽略", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("free buffer waits") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Cache Hit Ratio",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "buffer_cache_hit_ratio") },
			Branches: []Branch{
				{
					Label: "Buffer Hit < 95% — 缓存不足",
					Match: MatchLT(95),
					Then: &TreeNode{
						Step:  "检查 DB Cache Advice 建议",
						Query: QueryDBCacheAdvice,
						Branches: []Branch{
							{
								Label: "增大缓存可显著降低物理读",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Buffer Hit < 95%，free buffer waits 表明无空闲缓冲可用"},
									{Desc: "新的物理读请求无法找到空闲 buffer，等待 DBWR 写出脏缓冲"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "根据 DB Cache Advice 增大 DB_CACHE_SIZE",
										SkillCommand: "/param db_cache_size",
										RawSQL:       "SELECT size_for_estimate, buffers_for_estimate, estd_physical_read_factor, estd_physical_reads FROM v$db_cache_advice ORDER BY size_for_estimate",
										Risk: "增大 SGA 占用更多内存", Rollback: "ALTER SYSTEM SET DB_CACHE_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查是否有 SQL 大量扫描污染缓存",
										SkillCommand: "/ash top_sql event='free buffer waits'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='free buffer waits' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "Buffer Hit >= 95% — DBWR 写出慢",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 DBWR 写性能（db file parallel write）",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("db file parallel write") },
						Branches: []Branch{
							{
								Label: "parallel write > 10ms — DBWR 严重延迟",
								Match: MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Buffer Hit 正常但 DBWR 写出延迟 > 10ms，脏缓冲无法及时释放"},
									{Desc: "free buffer waits 是 DBWR 慢的下游症状"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查存储写入性能",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_write_ms FROM v$filestat WHERE phywrts>0 ORDER BY avg_write_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "启用异步 I/O 并增加 DBWR 进程数",
										SkillCommand: "/param db_writer_processes",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('disk_asynch_io','filesystemio_options','db_writer_processes')",
										Risk: "变更需要重启", Rollback: "恢复原参数"},
								},
							},
							{
								Label: "parallel write 5-10ms — DBWR 性能下降",
								Match: MatchBetween(5, 10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "DBWR 写延迟 5-10ms，存储性能不佳导致 free buffer waits"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查磁盘 I/O 负载分布",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_ms FROM v$filestat WHERE phywrts>0 ORDER BY phywrts DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增加 DB_WRITER_PROCESSES",
										SkillCommand: "/param db_writer_processes",
										RawSQL:       "SHOW PARAMETER db_writer_processes",
										Risk: "需要重启", Rollback: "ALTER SYSTEM SET DB_WRITER_PROCESSES=原值 SCOPE=SPFILE"},
								},
							},
							{
								Label: "parallel write < 5ms — 检查 checkpoint 频率",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "DBWR 写入正常，free buffer waits 可能由 checkpoint 不足引起"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 checkpoint 和增量 checkpoint 配置",
										SkillCommand: "/param log_checkpoint",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name LIKE '%checkpoint%' OR name='fast_start_mttr_target'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "调整 FAST_START_MTTR_TARGET 加速增量 checkpoint",
										SkillCommand: "/param fast_start_mttr_target",
										RawSQL:       "ALTER SYSTEM SET FAST_START_MTTR_TARGET=60 SCOPE=BOTH",
										Risk: "加速 checkpoint 增加 DBWR 负载", Rollback: "ALTER SYSTEM SET FAST_START_MTTR_TARGET=原值 SCOPE=BOTH"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE013"}, // db file parallel write
		CausesOf: []string{},
		Tags:     []string{"buffer_cache", "dbwr", "io", "free_buffer"},
		Versions: "9i+",
	}
}

// ─── 16. cursor: pin S ─────────────────────────────────────────────────────

func ruleCursorPinS() *Rule {
	return &Rule{
		ID:       "WE_CURSOR_PIN_S",
		Name:     "cursor: pin S 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cursor: pin S"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cursor: pin S", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "占比 < 1% 且平均等待 < 1ms，属于正常 mutex 竞争", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("cursor: pin S") < 1 && ctx.WaitAvgMs("cursor: pin S") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "确认硬解析率 — 区分 cursor: pin S 和 cursor: pin S wait on X 的根因",
			Query: QueryParseStats,
			Branches: []Branch{
				{
					Label: "硬解析率 > 20% — 实际上是硬解析问题，应按 cursor: pin S wait on X 处理",
					Match: MatchGT(20),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "cursor: pin S 伴随高硬解析率，实质是硬解析争用"},
						{Desc: "10g+ 的 mutex 机制下，硬解析争用可能同时表现为 cursor: pin S 和 cursor: pin S wait on X"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "转入 WE006 (cursor: pin S wait on X) 决策树处理",
							RawSQL: "SELECT ROUND(hard.value/total.value*100, 2) hard_parse_pct, soft.value soft_parses, hard.value hard_parses FROM v$sysstat hard, v$sysstat soft, v$sysstat total WHERE hard.name = 'parse count (hard)' AND soft.name = 'parse count (total)' - hard.name AND total.name = 'parse count (total)'",
							Risk:   "硬解析率 > 20% 的 cursor: pin S 本质是同一个问题的不同表现", Rollback: "无"},
					},
				},
				{
					Label: "硬解析率正常（<= 20%）— 真正的 cursor: pin S 争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查热点 SQL 的并发度",
						Check: func(ctx *EvalContext) interface{} {
							// Try sqlarea first (sentinel mode), fallback to TopSQLs (live mode).
							if v := ctx.GetFloat("sqlarea", "users_executing"); v > 0 {
								return v
							}
							// In live mode, use TopSQLs max concurrent as proxy.
							var maxConc float64
							for _, sql := range ctx.TopSQLs {
								if float64(sql.MaxConcurrent) > maxConc {
									maxConc = float64(sql.MaxConcurrent)
								}
							}
							return maxConc
						},
						Branches: []Branch{
							{
								Label: "热点 SQL 高并发（> 10）— 热点 SQL mutex 争用",
								Match: MatchGT(10),
								Then: &TreeNode{
									Step:  "检查热点 SQL 是否被频繁 invalidate",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("sqlarea", "inv_per_hour") },
									Branches: []Branch{
										{
											Label:    "invalidations > 10/小时 — 频繁失效重载",
											Match:    MatchGT(10),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "热点 SQL 被频繁 invalidate（> 10 次/小时），每次重载时的短暂排他 mutex 在超高并发下导致大量 shared pin 等待"},
												{Desc: "常见原因：自动统计信息收集、DDL 操作、grants 变化、dbms_stats 并行收集"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "锁定热点表的统计信息，避免自动收集导致 invalidation",
													RawSQL:   "EXEC DBMS_STATS.LOCK_TABLE_STATS('{owner}', '{table_name}')",
													Risk:     "锁定后统计信息不会自动更新，数据分布变化大时可能导致执行计划次优",
													Rollback: "EXEC DBMS_STATS.UNLOCK_TABLE_STATS('{owner}', '{table_name}')"},
												{Type: ActionFix, Desc: "将统计信息收集窗口移到低峰期",
													RawSQL:   "BEGIN DBMS_AUTO_TASK_ADMIN.DISABLE(client_name => 'auto optimizer stats collection', operation => NULL, window_name => 'MONDAY_WINDOW'); END;",
													Risk:     "需要确保低峰期的收集窗口覆盖",
													Rollback: "ENABLE 对应的 window"},
												{Type: ActionFix, Desc: "为热点 SQL 创建 SQL Plan Baseline 减少 invalidation 后的重编译开销",
													RawSQL:   "DECLARE l_plans PLS_INTEGER; BEGIN l_plans := DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE(sql_id => '&sql_id'); END;",
													Risk:     "Baseline 锁定计划后数据变化可能需要手动演进",
													Rollback: "EXEC DBMS_SPM.DROP_SQL_PLAN_BASELINE(sql_handle => '&handle')"},
											},
										},
										{
											Label:    "invalidations 正常 — 纯并发热点问题 (RC1)",
											Match:    MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "热点 SQL 并发执行数极高（> 100），mutex CAS 竞争激烈"},
												{Desc: "这是 Oracle mutex 实现的固有瓶颈：单个 cursor 的 mutex 在极高并发下成为串行化点"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "优化热点 SQL 减少执行频率（合并请求、增加应用层缓存）",
													SkillCommand: "/ash top_sql event='cursor: pin S'",
													RawSQL:       "SELECT sql_id, executions, users_executing, elapsed_time/GREATEST(executions,1)/1000 avg_ms, SUBSTR(sql_text,1,200) FROM v$sqlarea WHERE sql_id = '&hot_sql_id'",
													Risk:         "需要应用层改造", Rollback: "无"},
												{Type: ActionFix, Desc: "调整 _mutex_spin_count 减少 kernel 等待（需测试，仅在 CPU idle > 30% 时使用）",
													RawSQL:   "ALTER SYSTEM SET \"_mutex_spin_count\" = 512 SCOPE=BOTH",
													Risk:     "增大自旋次数会消耗更多 CPU，仅在 CPU 充裕时使用",
													Rollback: "ALTER SYSTEM RESET \"_mutex_spin_count\" SCOPE=BOTH"},
												{Type: ActionInvestigate, Desc: "考虑应用连接池分流或读写分离降低单实例并发",
													RawSQL: "SELECT sql_id, COUNT(*) cnt FROM v$session WHERE event = 'cursor: pin S' AND state = 'WAITING' GROUP BY sql_id ORDER BY cnt DESC",
													Risk:   "如果单条 SQL 并发数无法降低，从架构层面分流可能是唯一出路", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "无明显超高并发热点 SQL — 检查 session cursor cache",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查 session cursor cache 命中率",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("sysstat", "session_cursor_cache_hit_pct") },
									Branches: []Branch{
										{
											Label:    "cache hit < 50% — session cursor cache 不足 (RC3)",
											Match:    MatchLT(50),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "session cursor cache 命中率低于 50%，大量 SQL 每次执行都走软解析路径"},
												{Desc: "软解析虽然不重新编译，但仍需在 Library Cache 中查找和 pin cursor，增加 mutex 竞争"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大 session_cached_cursors（建议 200-500）",
													SkillCommand: "/sql @session_cursor_cache",
													RawSQL:       "ALTER SYSTEM SET session_cached_cursors = 300 SCOPE=SPFILE",
													Risk:         "增加每个 session 的 PGA 消耗（约 200 bytes × 数量），连接数多时需计算总量",
													Rollback:     "ALTER SYSTEM SET session_cached_cursors = 原值 SCOPE=SPFILE"},
												{Type: ActionFix, Desc: "确保应用使用 Statement Cache 或 PreparedStatement（减少 parse call）",
													RawSQL:   "SELECT a.value session_cursor_cache_hits, b.value total_parse_calls, ROUND(a.value/GREATEST(b.value,1)*100, 2) cache_hit_pct FROM v$sysstat a, v$sysstat b WHERE a.name = 'session cursor cache hits' AND b.name = 'parse count (total)'",
													Risk:     "需要应用端配合",
													Rollback: "无"},
											},
										},
										{
											Label:    "cache hit 正常 — 低级别 mutex 争用 (RC2/Bug 可能)",
											Match:    MatchDefault(),
											Severity: SeverityLow,
											Findings: []Finding{
												{Desc: "cursor: pin S 等待量低、解析效率正常、无已知 Bug，可能是正常的 mutex 竞争噪声"},
												{Desc: "如果 avg_wait < 1ms 且占 DB Time < 3%，通常不需要处理；特定版本可能存在 Bug（11.2.0.3 Bug 13066610、12.1 Bug 17335294、19c Bug 28939298）"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "监控趋势，记录 baseline；检查是否为已知 Bug 版本",
													RawSQL:   "SELECT TO_CHAR(sample_time, 'HH24:MI') ts, COUNT(*) samples FROM v$active_session_history WHERE event = 'cursor: pin S' AND sample_time > SYSDATE - INTERVAL '2' HOUR GROUP BY TO_CHAR(sample_time, 'HH24:MI') ORDER BY ts",
													Risk:     "无", Rollback: "无"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Related:  []string{"WE006", "WE010"}, // cursor: pin S wait on X, library cache lock/pin
		Tags:     []string{"mutex", "cursor", "soft_parse", "high_concurrency", "shared_pool", "execution"},
		Versions: "10gR2+",
	}
}

// ─── 17. resmgr:cpu quantum (Resource Manager CPU throttling) ────────────────

func ruleResmgrCPUQuantum() *Rule {
	return &Rule{
		ID:       "WE017",
		Name:     "resmgr:cpu quantum — Resource Manager CPU 限流",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "resmgr:cpu quantum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "resmgr:cpu quantum", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("resmgr:cpu quantum") < 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 resmgr:cpu quantum 占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("resmgr:cpu quantum") },
			Branches: []Branch{
				{
					Label:    "占比 > 50% — Resource Manager 严重限流",
					Match:    MatchGT(50),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Resource Manager CPU 限流占比超过 50%，严重影响数据库性能"},
						{Desc: "当前用户或 PDB 的 CPU 配额被 Resource Manager 限制，会话被强制排队等待 CPU"},
						{Desc: "真正的性能瓶颈可能被 resmgr 等待掩盖，其他等待事件可能不可见"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查当前 Resource Manager 计划和消费组配置",
							RawSQL:   "SELECT plan, status FROM dba_rsrc_plans WHERE status='ACTIVE'",
							Risk:     "无", Rollback: "无"},
						{Type: ActionFix, Desc: "禁用 Resource Manager 或调整 CPU 配额",
							RawSQL:   "ALTER SYSTEM SET RESOURCE_MANAGER_PLAN='' SCOPE=BOTH;\n-- 或调整消费组配额:\n-- EXEC DBMS_RESOURCE_MANAGER.UPDATE_PLAN_DIRECTIVE(plan=>'DEFAULT_PLAN', group_or_subplan=>'OTHER_GROUPS', new_utilization_limit=>100)",
							Risk:     "禁用 RM 可能导致单用户占满 CPU", Rollback: "ALTER SYSTEM SET RESOURCE_MANAGER_PLAN='DEFAULT_PLAN' SCOPE=BOTH"},
					},
				},
				{
					Label:    "占比 10-50% — Resource Manager 轻度限流",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Resource Manager CPU 限流占比 10-50%，对性能有影响"},
						{Desc: "检查是否因消费组配置限制了业务用户的 CPU 使用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查被限流的会话和消费组",
							RawSQL:   "SELECT s.username, s.resource_consumer_group, COUNT(*) cnt FROM v$session s WHERE s.event='resmgr:cpu quantum' AND s.state='WAITING' GROUP BY s.username, s.resource_consumer_group ORDER BY cnt DESC",
							Risk:     "无", Rollback: "无"},
						{Type: ActionFix, Desc: "调整消费组 CPU 配额或切换消费组",
							RawSQL:   "SELECT plan, group_or_subplan, utilization_limit, cpu_p1 FROM dba_rsrc_plan_directives WHERE plan IN (SELECT plan FROM dba_rsrc_plans WHERE status='ACTIVE')",
							Risk:     "无", Rollback: "无"},
					},
				},
				{
					Label:    "占比较低 — 轻微限流",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Resource Manager 有轻微 CPU 限流，暂不影响主要性能"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控 resmgr 等待趋势",
							RawSQL:   "SELECT event, total_waits, ROUND(time_waited_micro/1e6,1) wait_sec FROM v$system_event WHERE event LIKE 'resmgr%' ORDER BY time_waited_micro DESC",
							Risk:     "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"resource_manager", "cpu", "throttling", "scheduler"},
		Versions: "10g+",
	}
}
