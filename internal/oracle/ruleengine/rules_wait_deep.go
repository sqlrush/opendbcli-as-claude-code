/*-------------------------------------------------------------------------
 *
 * rules_wait_deep.go
 *	  Oracle rule engine — deep wait-event rules (latch misses, mutex sleeps, IO breakdown).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_wait_deep.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"
)

// deepWaitEventRules returns 20 deep wait event diagnostic rules that expand
// on the 15 basic wait event rules. These cover less common but important
// Oracle wait events with full multi-level decision trees.
func deepWaitEventRules() []*Rule {
	return []*Rule{
		ruleBufferBusyWaitsRAC(),      // buffer busy in RAC with gc correlation
		ruleCursorMutexX(),            // cursor: mutex X — version count bloat
		ruleCursorMutexS(),            // cursor: mutex S — v$sql access conflict
		ruleLibraryCacheMutexX(),      // library cache: mutex X — DDL/invalidation
		ruleEnqTXITLWait(),            // enq: TX mode 4 — ITL slot exhaustion
		ruleEnqTXBitmapLock(),         // enq: TX mode 4 — bitmap index DML lock
		ruleEnqTXUniqueKey(),          // enq: TX mode 4 — unique key conflict
		ruleEnqSTContention(),         // enq: ST — space transaction (temp/sort)
		ruleEnqUSContention(),         // enq: US — undo segment contention
		ruleLogBufferSpace(),          // log buffer space — log buffer too small
		ruleLogFileSwitchCheckpoint(), // log file switch (checkpoint incomplete)
		ruleLogFileSwitchArchiving(),  // log file switch (archiving needed)
		ruleDirectPathReadTemp(),      // direct path read temp — PGA spill
		ruleDirectPathWriteTemp(),     // direct path write temp — sort/hash spill
		ruleRowCacheLock(),            // row cache lock — sequence/DD contention
		ruleLatchCacheBuffersChains(), // latch: cache buffers chains
		ruleGCCurrentRequest(),        // gc current request — RAC current block
		ruleGCCRRequest(),             // gc cr request — RAC CR block
		ruleCellSingleBlockRead(),     // cell single block physical read — Exadata
		ruleDBFileParallelRead(),      // db file parallel read — recovery/prefetch
		ruleKksfbcChildCompletion(),   // kksfbc child completion — hard parse storm
	}
}

// ─── 1. buffer busy waits (RAC correlation) ─────────────────────────────────

func ruleBufferBusyWaitsRAC() *Rule {
	return &Rule{
		ID:       "WD001",
		Name:     "buffer busy waits RAC 关联诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "buffer busy waits"},
			{Type: SignalWaitEvent, Key: "gc buffer busy acquire"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "buffer busy waits", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境跳过此规则", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 gc buffer busy 是否同时出现",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("gc buffer busy acquire") + ctx.WaitPct("gc buffer busy release") },
			Branches: []Branch{
				{
					Label: "gc buffer busy > 5% — buffer busy 与 gc 争用叠加",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查互联网络延迟",
						Query: QueryInterconnectStats,
						Branches: []Branch{
							{
								Label: "interconnect avg > 1ms — 网络瓶颈放大 buffer busy",
								Match: MatchGT(1),
								Then: &TreeNode{
									Step:  "检查热点对象跨实例分布",
									Query: QueryASHHotObject,
									Branches: []Branch{
										{
											Label: "热点集中在单一对象 — 跨实例争用严重",
											Match: MatchDefault(),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "RAC 环境 buffer busy waits 与 gc buffer busy 同时出现"},
												{Desc: "互联网络延迟 > 1ms，跨实例块传输慢导致本地 buffer busy 加剧"},
												{Desc: "热点对象被多实例并发访问，形成 gc + buffer busy 叠加争用"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "配置 Service Affinity 将热点业务集中到单实例",
													SkillCommand: "/rac service check",
													RawSQL:       "SELECT name, inst_id, goal, clb_goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
													Risk: "变更 Service 需要应用端配合连接切换", Rollback: "恢复原 Service 配置"},
												{Type: ActionUrgent, Desc: "检查并升级互联网络带宽",
													SkillCommand: "/rac interconnect",
													RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
													Risk: "网络变更需要维护窗口", Rollback: "无"},
												{Type: ActionFix, Desc: "对热点表使用 HASH 分区分散块争用",
													SkillCommand: "/partition recommend {table_name}",
													RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event IN ('buffer busy waits','gc buffer busy acquire') AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "分区改造需要维护窗口", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "interconnect avg <= 1ms — 本地 buffer 争用为主因",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查 block class 分布",
									Query: QueryASHBlockClass,
									Branches: []Branch{
										{
											Label: "data_block — 数据块热点",
											Match: MatchEquals("data_block"),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "RAC 环境 buffer busy 与 gc 叠加，但网络正常"},
												{Desc: "数据块热点导致本地和远程争用同时发生"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大热点表 INITRANS 减少 ITL 争用",
													SkillCommand: "/space check {table_name}",
													RawSQL:       "SELECT table_name, ini_trans, max_trans, pct_free FROM dba_tables WHERE table_name='{table_name}'",
													Risk: "需要重建表", Rollback: "ALTER TABLE {table_name} INITRANS 原值"},
												{Type: ActionInvestigate, Desc: "分析热点 SQL 和对象",
													SkillCommand: "/ash hot_object event='buffer busy waits'",
													RawSQL:       "SELECT o.object_name, o.object_type, h.inst_id, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type, h.inst_id ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "其他 block class",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "RAC 环境 buffer busy 非数据块争用，可能是段头或 undo 块"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 block class 详细分布",
													SkillCommand: "/ash block_class event='buffer busy waits'",
													RawSQL:       "SELECT DECODE(h.p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block', 4,'segment_header', 5,'save_undo_header', 6,'free_list', 7,'extent_map', 8,'bitmap_block', h.p3) block_class, COUNT(*) waits FROM gv$active_session_history h WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY h.p3 ORDER BY 2 DESC",
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
					Label: "gc buffer busy <= 5% — 本地争用为主",
					Match: MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "RAC 环境 buffer busy waits 出现但 gc 争用不显著"},
						{Desc: "问题主要是本地热块争用，参考 WE005 基础规则处理"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "定位本地热点对象",
							SkillCommand: "/ash hot_object event='buffer busy waits'",
							RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='buffer busy waits' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"WE011"}, // gc buffer busy
		CausesOf: []string{},
		Tags:     []string{"rac", "contention", "hot_block", "gc", "buffer_busy"},
		Versions: "10g+ RAC",
		Related:  []string{"WE005", "WE011"},
	}
}

// ─── 2. cursor: mutex X ─────────────────────────────────────────────────────

func ruleCursorMutexX() *Rule {
	return &Rule{
		ID:       "WD002",
		Name:     "cursor: mutex X 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cursor: mutex X"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cursor: mutex X", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1% 且非持续趋势", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("cursor: mutex X") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 version count 最高的 SQL",
			Query: QueryCursorStats,
			Branches: []Branch{
				{
					Label: "version_count > 100 — 子游标版本膨胀",
					Match: MatchGT(100),
					Then: &TreeNode{
						Step:  "检查硬解析率和 literal SQL 比例",
						Query: QueryParseStats,
						Branches: []Branch{
							{
								Label: "literal SQL > 30% — 未使用绑定变量导致版本膨胀",
								Match: MatchGT(30),
								Then: &TreeNode{
									Step:  "检查 _cursor_obsolete_threshold 设置",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "cursor_obsolete_threshold") },
									Branches: []Branch{
										{
											Label: "threshold > 1024 — 默认值过大加剧膨胀",
											Match: MatchGT(1024),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "cursor: mutex X 由子游标版本膨胀引起，version_count > 100"},
												{Desc: "literal SQL > 30%，大量不同文本的 SQL 共享同一 parent cursor"},
												{Desc: "_cursor_obsolete_threshold 设置过大（默认 8192），允许过多版本累积"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "降低 _cursor_obsolete_threshold 到 100 加速游标淘汰",
													SkillCommand: "/param _cursor_obsolete_threshold",
													RawSQL:       "ALTER SYSTEM SET \"_cursor_obsolete_threshold\"=100 SCOPE=BOTH",
													Risk: "过低可能导致频繁硬解析", Rollback: "ALTER SYSTEM SET \"_cursor_obsolete_threshold\"=8192 SCOPE=BOTH"},
												{Type: ActionFix, Desc: "启用 CURSOR_SHARING=FORCE 紧急缓解",
													SkillCommand: "/param cursor_sharing",
													RawSQL:       "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
													Risk: "可能导致部分 SQL 执行计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
												{Type: ActionFix, Desc: "推动应用使用绑定变量（根本方案）",
													SkillCommand: "/ash top_sql event='cursor: mutex X'",
													RawSQL:       "SELECT sql_id, version_count, executions, loads, invalidations FROM v$sqlarea WHERE version_count > 100 ORDER BY version_count DESC FETCH FIRST 20 ROWS ONLY",
													Risk: "需要修改应用代码", Rollback: "无"},
											},
										},
										{
											Label: "threshold <= 1024 — 已调优但仍不足",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "cursor: mutex X 持续存在，即使 _cursor_obsolete_threshold 已调低"},
												{Desc: "根因是应用层 literal SQL 比例过高，需从应用端解决"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用绑定变量改造应用 SQL",
													SkillCommand: "/ash top_sql event='cursor: mutex X'",
													RawSQL:       "SELECT sql_id, version_count, executions, parse_calls, ROUND(parse_calls/GREATEST(executions,1)*100,1) parse_ratio FROM v$sqlarea WHERE version_count > 50 ORDER BY version_count DESC FETCH FIRST 20 ROWS ONLY",
													Risk: "需要修改应用代码", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "检查 mutex sleep 详情",
													SkillCommand: "/sql \"SELECT mutex_type, location, sleeps, wait_time FROM v$mutex_sleep WHERE mutex_type LIKE '%Cursor%' ORDER BY sleeps DESC FETCH FIRST 10 ROWS ONLY\"",
													RawSQL:       "SELECT mutex_type, location, sleeps, wait_time FROM v$mutex_sleep WHERE mutex_type LIKE '%Cursor%' ORDER BY sleeps DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "硬解析率正常 — ACS/bind peeking 导致版本膨胀",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查是否存在 Adaptive Cursor Sharing 导致版本过多",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "acs_reparses") },
									Branches: []Branch{
										{
											Label: "ACS reparses 活跃 — 自适应游标共享产生过多版本",
											Match: MatchGT(0),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "Adaptive Cursor Sharing (ACS) 频繁重解析产生大量子游标版本"},
												{Desc: "不同绑定变量值触发不同执行计划，每个计划一个版本"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "考虑固定执行计划使用 SQL Plan Baseline",
													SkillCommand: "/spm create {sql_id}",
													RawSQL:       "DECLARE l_plans PLS_INTEGER; BEGIN l_plans := DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE(sql_id=>'{sql_id}'); END;",
													Risk: "固定计划可能不适合数据倾斜场景", Rollback: "EXEC DBMS_SPM.DROP_SQL_PLAN_BASELINE(sql_handle=>'{sql_handle}')"},
												{Type: ActionInvestigate, Desc: "检查 v$sql 中版本膨胀的 SQL",
													SkillCommand: "/sql \"SELECT sql_id, child_number, is_bind_sensitive, is_bind_aware, plan_hash_value FROM v$sql WHERE sql_id='{sql_id}' ORDER BY child_number\"",
													RawSQL:       "SELECT sql_id, child_number, is_bind_sensitive, is_bind_aware, plan_hash_value FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$sqlarea WHERE version_count > 100) ORDER BY sql_id, child_number",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "无 ACS 问题 — 其他原因导致版本膨胀",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "version count > 100 但非 literal SQL 也非 ACS，可能是 optimizer env mismatch"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查版本膨胀原因（v$sql_shared_cursor）",
													SkillCommand: "/sql \"SELECT sql_id, child_number, reason FROM v$sql_shared_cursor WHERE sql_id='{sql_id}' AND reason IS NOT NULL\"",
													RawSQL:       "SELECT s.sql_id, s.child_number, sc.* FROM v$sql s JOIN v$sql_shared_cursor sc ON s.sql_id=sc.sql_id AND s.child_number=sc.child_number WHERE s.sql_id IN (SELECT sql_id FROM v$sqlarea WHERE version_count > 100 AND ROWNUM <= 5)",
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
					Label: "version_count <= 100 — 并发编译争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 mutex sleep 热点位置",
						Query: QueryMutexStats,
						Branches: []Branch{
							{
								Label: "mutex sleep 在 kks 相关位置 — 高并发编译同一 SQL",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "cursor: mutex X 由高并发编译同一 SQL 引起，version count 正常"},
									{Desc: "多个会话同时对同一 SQL 进行硬解析或执行计划生成"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位热点 SQL",
										SkillCommand: "/ash top_sql event='cursor: mutex X'",
										RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='cursor: mutex X' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "增大 session_cached_cursors 减少软解析",
										SkillCommand: "/param session_cached_cursors",
										RawSQL:       "ALTER SYSTEM SET SESSION_CACHED_CURSORS=100 SCOPE=SPFILE",
										Risk: "增加每会话内存消耗", Rollback: "ALTER SYSTEM SET SESSION_CACHED_CURSORS=原值 SCOPE=SPFILE"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE010"}, // library cache lock/pin
		CausesOf: []string{},
		Tags:     []string{"mutex", "cursor", "version_count", "parse", "shared_pool"},
		Versions: "10g+",
		Related:  []string{"WE006", "WE010", "WD003"},
	}
}

// ─── 3. cursor: mutex S ─────────────────────────────────────────────────────

func ruleCursorMutexS() *Rule {
	return &Rule{
		ID:       "WD003",
		Name:     "cursor: mutex S 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cursor: mutex S"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cursor: mutex S", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("cursor: mutex S") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有大量会话并发查询 v$sql / v$sqlarea",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "v_sql_access_sessions") },
			Branches: []Branch{
				{
					Label: "v$sql 访问活跃 — 监控工具并发查询导致 mutex 争用",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查查询 v$sql 的会话程序来源",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "monitoring_tool_sessions") },
						Branches: []Branch{
							{
								Label: "监控工具会话 > 10 — 工具查询频率过高",
								Match: MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "cursor: mutex S 由大量监控工具并发查询 v$sql/v$sqlarea 引起"},
									{Desc: "多个监控工具同时扫描 library cache 产生 S 模式 mutex 争用"},
									{Desc: "mutex S 是共享模式，但大量并发共享请求仍会产生争用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "降低监控工具查询 v$sql 的频率",
										SkillCommand: "/ash top_sql event='cursor: mutex S'",
										RawSQL:       "SELECT s.sid, s.program, s.module, s.sql_id, q.sql_text FROM v$session s LEFT JOIN v$sql q ON s.sql_id=q.sql_id AND ROWNUM=1 WHERE s.event='cursor: mutex S' AND s.state='WAITING'",
										Risk: "降低监控频率可能影响监控覆盖", Rollback: "恢复监控频率"},
									{Type: ActionFix, Desc: "合并监控工具或使用 AWR 替代实时查询",
										SkillCommand: "/sql \"SELECT program, COUNT(*) sessions FROM v$session WHERE program LIKE '%Monitor%' OR program LIKE '%agent%' OR program LIKE '%zabbix%' OR program LIKE '%prometheus%' GROUP BY program ORDER BY 2 DESC\"",
										RawSQL:       "SELECT program, module, COUNT(*) sessions FROM v$session WHERE type='USER' GROUP BY program, module HAVING COUNT(*) > 3 ORDER BY 3 DESC",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "监控工具会话正常 — 业务 SQL 访问导致",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "cursor: mutex S 由高并发访问 library cache 引起"},
									{Desc: "非监控工具导致，可能是应用内部频繁软解析"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位 mutex 争用的热点 SQL",
										SkillCommand: "/ash top_sql event='cursor: mutex S'",
										RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='cursor: mutex S' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "增大 session_cached_cursors 减少软解析",
										SkillCommand: "/param session_cached_cursors",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name='session_cached_cursors'",
										Risk: "增加每会话内存", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "v$sql 访问不活跃 — 高并发软解析争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 shared pool 大小和碎片化",
						Query: QuerySPFreeMemory,
						Branches: []Branch{
							{
								Label: "shared pool free < 15% — 内存压力导致频繁淘汰和重载",
								Match: MatchLT(15),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Shared Pool 可用空间 < 15%，cursor 频繁淘汰导致 mutex 争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 SHARED_POOL_SIZE",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "SELECT pool, name, ROUND(bytes/1024/1024) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "增大 SGA 占用更多内存", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值 SCOPE=BOTH"},
								},
							},
							{
								Label: "shared pool 充足 — 并发解析 pattern 问题",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "cursor: mutex S 在 shared pool 充足情况下出现，可能是热点 SQL 并发软解析"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析 mutex sleep 位置",
										SkillCommand: "/sql \"SELECT mutex_type, location, sleeps, wait_time FROM v$mutex_sleep WHERE mutex_type='Cursor Pin' ORDER BY sleeps DESC FETCH FIRST 10 ROWS ONLY\"",
										RawSQL:       "SELECT mutex_type, location, sleeps, wait_time FROM v$mutex_sleep WHERE mutex_type LIKE '%Cursor%' ORDER BY sleeps DESC FETCH FIRST 10 ROWS ONLY",
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
		Tags:     []string{"mutex", "cursor", "monitoring", "soft_parse"},
		Versions: "10g+",
		Related:  []string{"WE006", "WD002"},
	}
}

// ─── 4. library cache: mutex X ──────────────────────────────────────────────

func ruleLibraryCacheMutexX() *Rule {
	return &Rule{
		ID:       "WD004",
		Name:     "library cache: mutex X 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "library cache: mutex X"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "library cache: mutex X", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("library cache: mutex X") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有并发 DDL 操作",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "ddl_sessions") },
			Branches: []Branch{
				{
					Label: "存在 DDL 操作 — DDL invalidation 导致 mutex X 争用",
					Match: MatchGT(0),
					Then: &TreeNode{
						Step:  "检查 DDL 类型和频率",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "ddl_frequency_per_min") },
						Branches: []Branch{
							{
								Label: "DDL 频率 > 10/min — 频繁 DDL 导致大量 invalidation",
								Match: MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "library cache: mutex X 由频繁 DDL 操作引起（> 10/min）"},
									{Desc: "DDL 持有 library cache 排他 mutex，使所有依赖对象的 SQL 失效"},
									{Desc: "失效的 SQL 需要重编译，期间 mutex X 被持有导致大量等待"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "停止或推迟非必要 DDL 操作",
										SkillCommand: "/lock check",
										RawSQL:       "SELECT s.sid, s.serial#, s.username, s.program, s.sql_id, q.sql_text FROM v$session s LEFT JOIN v$sql q ON s.sql_id=q.sql_id AND ROWNUM=1 WHERE s.command IN (1,2,3,4,9,11,15,39,40) AND s.status='ACTIVE'",
										Risk: "停止 DDL 可能影响部署流程", Rollback: "无"},
									{Type: ActionPrevent, Desc: "将 DDL 操作移到低峰时段执行",
										SkillCommand: "/sql \"SELECT TO_CHAR(sample_time,'HH24') hr, COUNT(*) ddl_waits FROM v$active_session_history WHERE event='library cache: mutex X' AND sample_time>SYSDATE-1 GROUP BY TO_CHAR(sample_time,'HH24') ORDER BY 1\"",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24') hr, COUNT(*) ddl_waits FROM v$active_session_history WHERE event='library cache: mutex X' AND sample_time>SYSDATE-1 GROUP BY TO_CHAR(sample_time,'HH24') ORDER BY 1",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "使用 DBMS_REDEFINITION 做在线重定义避免长时间锁定",
										SkillCommand: "/redef check {table_name}",
										RawSQL:       "BEGIN DBMS_REDEFINITION.CAN_REDEF_TABLE('{owner}', '{table_name}'); END;",
										Risk: "在线重定义需额外空间", Rollback: "EXEC DBMS_REDEFINITION.ABORT_REDEF_TABLE('{owner}', '{table_name}')"},
								},
							},
							{
								Label: "DDL 频率正常 — 单个长 DDL 阻塞",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "library cache: mutex X 由单个长时间 DDL 操作引起"},
									{Desc: "该 DDL 持有排他 mutex 时间过长，阻塞所有依赖 SQL"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位正在执行的 DDL 会话",
										SkillCommand: "/lock check",
										RawSQL:       "SELECT s.sid, s.serial#, s.username, s.sql_id, s.event, s.seconds_in_wait FROM v$session s WHERE s.command IN (1,2,3,4,9,11,15,39,40) AND s.status='ACTIVE'",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "评估是否需要终止 DDL 以恢复业务",
										SkillCommand: "/session kill {sid},{serial#}",
										RawSQL:       "ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE",
										Risk: "终止 DDL 会导致回滚", Rollback: "重新执行 DDL"},
								},
							},
						},
					},
				},
				{
					Label: "无 DDL — 硬解析争用导致 mutex X",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step: "检查硬解析率",
						Check: func(ctx *EvalContext) interface{} {
							result, err := ctx.ExecuteQuery(QueryParseStats, nil)
							if err == nil && result != nil {
								if pct := ExtractHardParsePct(result); pct >= 0 {
									return pct
								}
							}
							// Fallback: use metric or estimate from wait events.
							if v := ctx.MetricValue("hard_parse_pct"); v > 0 {
								return v
							}
							if ctx.WaitPct("latch: shared pool") > 20 {
								return float64(15)
							}
							return float64(0)
						},
						Branches: []Branch{
							{
								Label:    "硬解析 > 10% — 高硬解析导致 library cache mutex 争用",
								Match:    MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "硬解析率 > 10%，大量新 SQL 编译竞争 library cache mutex X"},
									{Desc: "每次硬解析都需要获取 mutex X 在 library cache 中创建新的 cursor"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用绑定变量减少硬解析",
										SkillCommand: "/ash top_sql event='library cache: mutex X'",
										RawSQL:       "SELECT sql_id, executions, parse_calls, version_count FROM v$sqlarea WHERE parse_calls > 100 AND executions <= parse_calls ORDER BY parse_calls DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "需要修改应用代码", Rollback: "无"},
									{Type: ActionFix, Desc: "启用 CURSOR_SHARING=FORCE 临时缓解",
										SkillCommand: "/param cursor_sharing",
										RawSQL:       "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
										Risk: "可能导致部分执行计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
								},
							},
							{
								Label: "硬解析正常 — 差异化诊断",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step: "差异化诊断：排除全表扫描/TEMP溢出/plan不稳定/invalidation",
									Check: func(ctx *EvalContext) interface{} {
										mutexPct := ctx.WaitPct("library cache: mutex X")
										if mutexPct <= 15 {
											return "low"
										}
										// Phase 3 方案A: check for stronger signals before invalidation.
										// Full table scan signal.
										if ctx.WaitPct("db file scattered read") > 15 {
											return "full_scan"
										}
										// TEMP overflow signal.
										tempPct := ctx.WaitPct("direct path read temp") + ctx.WaitPct("direct path write temp")
										if tempPct > 10 {
											return "temp_overflow"
										}
										// Invalidation evidence: library cache lock co-occurrence.
										if ctx.WaitPct("library cache lock") > 5 {
											return "invalidation"
										}
										// No strong signal → plan instability / bind peeking.
										return "plan_instability"
									},
									Branches: []Branch{
										{
											Label:    "db file scattered read 高 — 全表扫描是主因",
											Match:    MatchEquals("full_scan"),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "db file scattered read 占比高，主要瓶颈是全表扫描 I/O，非 library cache 问题"},
												{Desc: "library cache: mutex X 是伴生等待，优先解决全表扫描"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "定位全表扫描 SQL 和缺失索引",
													RawSQL: "SELECT sql_id, disk_reads, buffer_gets, executions, ROUND(disk_reads/NULLIF(executions,0)) reads_per_exec FROM v$sqlarea WHERE disk_reads > 1000 ORDER BY disk_reads DESC FETCH FIRST 10 ROWS ONLY",
													Risk:   "无", Rollback: "无"},
												{Type: ActionFix, Desc: "为高频全表扫描 SQL 创建索引",
													RawSQL: "-- 根据上述查询结果分析 SQL 的 WHERE 条件列，创建对应索引",
													Risk:   "索引占空间，写入性能略降"},
											},
										},
										{
											Label:    "TEMP 溢出 — 排序/Hash Join 是主因",
											Match:    MatchEquals("temp_overflow"),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "direct path temp 等待占比高，SQL 排序或 Hash Join 溢出到磁盘"},
												{Desc: "library cache: mutex X 是伴生等待，优先解决 TEMP/PGA 问题"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 TEMP 使用 Top SQL",
													RawSQL: "SELECT s.sid, s.username, s.sql_id, t.blocks*8/1024 mb_used FROM v$tempseg_usage t JOIN v$session s ON t.session_num=s.serial# AND t.session_addr=s.saddr ORDER BY t.blocks DESC FETCH FIRST 10 ROWS ONLY",
													Risk:   "无", Rollback: "无"},
												{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET 减少磁盘排序",
													RawSQL: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=2G SCOPE=BOTH;",
													Risk:   "占用更多内存"},
											},
										},
										{
											Label:    "mutex X + library cache lock — invalidation 导致重编译",
											Match:    MatchEquals("invalidation"),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "硬解析率正常但 library cache mutex X 占比高，且 library cache lock 争用明显"},
												{Desc: "可能是统计信息收集或权限变更导致游标批量失效和重编译"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查近期 invalidation 事件",
													SkillCommand: "/sql \"SELECT sql_id, invalidations, loads, parse_calls FROM v$sqlarea WHERE invalidations > 10 ORDER BY invalidations DESC FETCH FIRST 20 ROWS ONLY\"",
													RawSQL:       "SELECT sql_id, invalidations, loads, parse_calls FROM v$sqlarea WHERE invalidations > 10 ORDER BY invalidations DESC FETCH FIRST 20 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label:    "mutex X 高但无 invalidation — 可能是执行计划不稳定",
											Match:    MatchEquals("plan_instability"),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "library cache: mutex X 争用明显但无 library cache lock 并存"},
												{Desc: "可能原因：绑定变量窥探导致执行计划不稳定、数据倾斜、或高版本计数"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查高版本计数 SQL（可能有 bind peeking 或数据倾斜）",
													RawSQL: "SELECT sql_id, version_count, executions, invalidations FROM v$sqlarea WHERE version_count > 10 ORDER BY version_count DESC FETCH FIRST 20 ROWS ONLY",
													Risk:   "无", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "检查执行计划是否因绑定变量不同而变化",
													RawSQL: "SELECT sql_id, child_number, plan_hash_value, executions, buffer_gets/GREATEST(executions,1) avg_gets FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$sqlarea WHERE version_count > 5) ORDER BY sql_id, child_number",
													Risk:   "无", Rollback: "无"},
											},
										},
										{
											Label:    "mutex X 占比低 — 非主要瓶颈",
											Match:    MatchDefault(),
											Severity: SeverityLow,
											Findings: []Finding{{Desc: "library cache: mutex X 占比低，非当前主要瓶颈，真正瓶颈可能来自其他等待事件"}},
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
		CausesOf: []string{"WE006", "WE010"}, // cursor pin S, library cache lock
		Tags:     []string{"mutex", "library_cache", "ddl", "hard_parse", "invalidation"},
		Versions: "10g+",
		Related:  []string{"WE010", "WD002"},
	}
}

// ─── 5. enq: TX mode 4 — ITL slot exhaustion ───────────────────────────────

func ruleEnqTXITLWait() *Rule {
	return &Rule{
		ID:       "WD005",
		Name:     "enq: TX mode 4 — ITL 槽位耗尽诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TX - allocate ITL entry"},
			{Type: SignalWaitEvent, Key: "enq: TX - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TX - contention", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "TX 锁模式非 mode 4", Check: func(ctx *EvalContext) bool {
					mode := ctx.GetStr("metrics", "tx_request_mode")
					return mode != "" && mode != "mode_4"
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查热点对象的 INITRANS 设置",
			Query: QueryASHHotObject,
			Branches: []Branch{
				{
					Label: "定位到热点对象",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查对象 INITRANS 值",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "hot_object_initrans") },
						Branches: []Branch{
							{
								Label: "INITRANS <= 2 — 默认值不足以支撑高并发",
								Match: MatchLTE(2),
								Then: &TreeNode{
									Step:  "检查并发 DML 会话数",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "concurrent_dml_sessions") },
									Branches: []Branch{
										{
											Label: "并发 DML > 20 — 每块 ITL 槽严重不足",
											Match: MatchGT(20),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "enq: TX mode 4 由 ITL 槽位耗尽引起"},
												{Desc: "热点对象 INITRANS 仅 1-2（默认值），并发 DML > 20 导致每块只有 1-2 个事务槽"},
												{Desc: "当块内所有 ITL 槽被占满且无法动态扩展时，新事务必须等待"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大热点表 INITRANS 到 8-16",
													SkillCommand: "/space check {table_name}",
													RawSQL:       "ALTER TABLE {table_name} INITRANS 16;\nALTER INDEX {index_name} INITRANS 16;\n-- 注意: 仅对新分配块生效，旧块需 MOVE/REBUILD",
													Risk: "需要对表执行 MOVE 使旧块也生效", Rollback: "ALTER TABLE {table_name} INITRANS 原值"},
												{Type: ActionFix, Desc: "对表执行 MOVE 使所有块应用新 INITRANS",
													SkillCommand: "/space move {table_name}",
													RawSQL:       "ALTER TABLE {table_name} MOVE;\nALTER INDEX {index_name} REBUILD;\n-- 在线操作(12c+): ALTER TABLE {table_name} MOVE ONLINE;",
													Risk: "MOVE 期间表不可 DML（12c 前）", Rollback: "无需回滚"},
												{Type: ActionInvestigate, Desc: "确认 ITL 争用的块分布",
													SkillCommand: "/ash block_concentrate event='enq: TX - contention'",
													RawSQL:       "SELECT current_obj#, current_file#, current_block#, COUNT(*) waits FROM v$active_session_history WHERE event LIKE 'enq: TX%' AND p1raw LIKE '%0004%' AND sample_time>SYSDATE-1/24 GROUP BY current_obj#, current_file#, current_block# ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "并发 DML <= 20 — 中等争用",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "ITL 槽位不足，默认 INITRANS 1-2 无法满足并发需求"},
												{Desc: "建议增大 INITRANS 到 8 预防 ITL 争用"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大 INITRANS 到 8",
													SkillCommand: "/space check {table_name}",
													RawSQL:       "ALTER TABLE {table_name} INITRANS 8;\n-- 需要 MOVE 使旧块生效",
													Risk: "需要维护窗口执行 MOVE", Rollback: "ALTER TABLE {table_name} INITRANS 原值"},
												{Type: ActionInvestigate, Desc: "检查热点对象的当前 INITRANS",
													SkillCommand: "/sql \"SELECT table_name, ini_trans, max_trans, pct_free FROM dba_tables WHERE table_name='{table_name}'\"",
													RawSQL:       "SELECT table_name, ini_trans, max_trans, pct_free FROM dba_tables WHERE table_name='{table_name}'",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "INITRANS > 2 — 已调优但仍不足或 PCTFREE 不足",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "INITRANS 已增大但 ITL 争用仍存在"},
									{Desc: "可能是 PCTFREE 过小导致块内无空间动态增加 ITL 槽"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 PCTFREE 为 ITL 动态扩展预留空间",
										SkillCommand: "/space check {table_name}",
										RawSQL:       "ALTER TABLE {table_name} PCTFREE 20;\nALTER TABLE {table_name} MOVE;\n-- PCTFREE 20% 可为更多 ITL 槽预留空间",
										Risk: "增大 PCTFREE 会增加存储消耗", Rollback: "ALTER TABLE {table_name} PCTFREE 原值"},
									{Type: ActionFix, Desc: "进一步增大 INITRANS",
										SkillCommand: "/sql \"SELECT table_name, ini_trans, max_trans, pct_free, blocks FROM dba_tables WHERE table_name='{table_name}'\"",
										RawSQL:       "SELECT table_name, ini_trans, max_trans, pct_free, blocks FROM dba_tables WHERE table_name='{table_name}'",
										Risk: "更多 ITL 槽占用更多块内空间", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "itl", "initrans", "contention", "tx_mode4"},
		Versions: "9i+",
		Related:  []string{"WE007"},
	}
}

// ─── 6. enq: TX mode 4 — bitmap index DML lock ────────────────────────────

func ruleEnqTXBitmapLock() *Rule {
	return &Rule{
		ID:       "WD006",
		Name:     "enq: TX mode 4 — Bitmap 索引 DML 锁诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TX - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TX - contention", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "热点对象无 bitmap 索引", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "bitmap_index_count_on_hot") == 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查热点对象是否存在 Bitmap 索引",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "bitmap_index_count_on_hot") },
			Branches: []Branch{
				{
					Label: "存在 Bitmap 索引 — OLTP DML 与 Bitmap 索引冲突",
					Match: MatchGT(0),
					Then: &TreeNode{
						Step:  "检查并发 DML 类型",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "concurrent_dml_sessions") },
						Branches: []Branch{
							{
								Label: "并发 DML > 10 — Bitmap 锁争用严重",
								Match: MatchGT(10),
								Then: &TreeNode{
									Step:  "检查系统负载类型",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "workload_type") },
									Branches: []Branch{
										{
											Label: "OLTP 系统 — Bitmap 索引不适合 OLTP",
											Match: MatchEquals("OLTP"),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "enq: TX mode 4 由 Bitmap 索引在 OLTP 高并发 DML 下引起"},
												{Desc: "Bitmap 索引在 DML 时锁定整个 bitmap segment（多行），不适合 OLTP"},
												{Desc: "一个 bitmap segment 覆盖多行，任意一行 DML 锁住整个 segment"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "将 Bitmap 索引改为 B-Tree 索引",
													SkillCommand: "/index analyze {index_name}",
													RawSQL:       "SELECT i.index_name, i.index_type, i.table_name, i.num_rows, i.distinct_keys FROM dba_indexes i WHERE i.index_type LIKE 'BITMAP%' AND i.table_name='{table_name}'",
													Risk: "B-Tree 索引占用更多空间", Rollback: "重建为 Bitmap 索引"},
												{Type: ActionInvestigate, Desc: "列出所有 Bitmap 索引及其表",
													SkillCommand: "/sql \"SELECT owner, index_name, table_name, distinct_keys FROM dba_indexes WHERE index_type='BITMAP' AND owner NOT IN ('SYS','SYSTEM') ORDER BY table_name\"",
													RawSQL:       "SELECT owner, index_name, table_name, distinct_keys FROM dba_indexes WHERE index_type='BITMAP' AND owner NOT IN ('SYS','SYSTEM') ORDER BY table_name",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "非 OLTP — 评估 Bitmap 索引必要性",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "非 OLTP 系统但 Bitmap 索引 DML 争用明显"},
												{Desc: "需要评估是否可以将 DML 操作与查询分离到不同时段"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "分析 DML 高峰时段和查询时段是否重叠",
													SkillCommand: "/ash trend event='enq: TX - contention'",
													RawSQL:       "SELECT TO_CHAR(sample_time,'HH24') hr, COUNT(*) waits FROM v$active_session_history WHERE event LIKE 'enq: TX%' AND sample_time>SYSDATE-1 GROUP BY TO_CHAR(sample_time,'HH24') ORDER BY 1",
													Risk: "无", Rollback: "无"},
												{Type: ActionPrevent, Desc: "将批量 DML 与 Bitmap 索引维护分时段执行",
													SkillCommand: "/index info {table_name}",
													RawSQL:       "SELECT index_name, index_type, status, last_analyzed FROM dba_indexes WHERE table_name='{table_name}'",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "并发 DML <= 10 — 轻度争用",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Bitmap 索引存在但并发 DML 不高，争用可控"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "监控趋势评估是否需要改造",
										SkillCommand: "/wait trend event='enq: TX - contention'",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event LIKE 'enq: TX%' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "无 Bitmap 索引 — 非此规则覆盖范围",
					Match: MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "热点对象无 Bitmap 索引，TX mode 4 争用可能是 ITL 或 Unique Key 问题"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "参考 ITL (WD005) 或 Unique Key (WD007) 规则",
							SkillCommand: "/ash detail event='enq: TX - contention'",
							RawSQL:       "SELECT h.sql_id, h.current_obj#, h.p1, h.p2, h.p3, COUNT(*) waits FROM v$active_session_history h WHERE h.event LIKE 'enq: TX%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.current_obj#, h.p1, h.p2, h.p3 ORDER BY 6 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "bitmap_index", "tx_mode4", "oltp", "contention"},
		Versions: "9i+",
		Related:  []string{"WE007", "WD005", "WD007"},
	}
}

// ─── 7. enq: TX mode 4 — unique key conflict ──────────────────────────────

func ruleEnqTXUniqueKey() *Rule {
	return &Rule{
		ID:       "WD007",
		Name:     "enq: TX mode 4 — 唯一键冲突诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TX - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TX - contention", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "无唯一索引相关争用信号", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "unique_constraint_violations") == 0 &&
						ctx.GetFloat("metrics", "bitmap_index_count_on_hot") > 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查热点 SQL 是否为 INSERT 且涉及唯一约束",
			Query: QueryASHTopSQL,
			Branches: []Branch{
				{
					Label: "找到热点 INSERT SQL",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查唯一键冲突频率",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "unique_constraint_violations") },
						Branches: []Branch{
							{
								Label: "唯一键冲突频繁 — INSERT...ON DUPLICATE 或重复数据",
								Match: MatchGT(10),
								Then: &TreeNode{
									Step:  "检查是否使用序列生成主键",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "sequence_usage_on_hot") },
									Branches: []Branch{
										{
											Label: "未使用序列 — 应用生成键值可能冲突",
											Match: MatchLTE(0),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "enq: TX mode 4 由唯一键冲突引起"},
												{Desc: "应用程序未使用 Oracle 序列生成主键，自行生成的键值存在冲突"},
												{Desc: "当两个事务同时 INSERT 相同唯一键值时，后者必须等待前者 COMMIT/ROLLBACK"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "改用 Oracle 序列生成主键避免冲突",
													SkillCommand: "/sql \"SELECT sequence_name, cache_size, last_number FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') ORDER BY sequence_name\"",
													RawSQL:       "-- 创建序列替代应用生成键值\nCREATE SEQUENCE {seq_name} START WITH 1 INCREMENT BY 1 CACHE 1000 NOORDER;",
													Risk: "需要修改应用代码", Rollback: "DROP SEQUENCE {seq_name}"},
												{Type: ActionInvestigate, Desc: "检查冲突的 SQL 和表",
													SkillCommand: "/ash top_sql event='enq: TX - contention'",
													RawSQL:       "SELECT sql_id, sql_text FROM v$sql WHERE sql_id IN (SELECT sql_id FROM v$active_session_history WHERE event LIKE 'enq: TX%' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY COUNT(*) DESC FETCH FIRST 5 ROWS ONLY) AND ROWNUM <= 5",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "使用序列 — 可能是业务唯一键（非主键）冲突",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "主键使用序列但其他唯一约束列存在冲突"},
												{Desc: "业务唯一键（如订单号、身份证号）并发 INSERT 导致 TX mode 4"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查冲突的唯一约束",
													SkillCommand: "/sql \"SELECT c.table_name, c.constraint_name, cc.column_name FROM dba_constraints c JOIN dba_cons_columns cc ON c.constraint_name=cc.constraint_name WHERE c.constraint_type='U' AND c.table_name='{table_name}'\"",
													RawSQL:       "SELECT c.table_name, c.constraint_name, cc.column_name FROM dba_constraints c JOIN dba_cons_columns cc ON c.constraint_name=cc.constraint_name AND c.owner=cc.owner WHERE c.constraint_type='U' AND c.table_name='{table_name}'",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "应用层使用 MERGE 或先查后插避免冲突",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "需要修改应用代码", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "唯一键冲突少 — 可能是 ITL 或其他 mode 4 原因",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "TX mode 4 但唯一键冲突不频繁，可能是 ITL 问题"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "进一步区分 mode 4 子类型",
										SkillCommand: "/ash detail event='enq: TX - contention'",
										RawSQL:       "SELECT h.current_obj#, h.current_file#, h.current_block#, h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event LIKE 'enq: TX%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.current_obj#, h.current_file#, h.current_block#, h.sql_id ORDER BY 5 DESC FETCH FIRST 10 ROWS ONLY",
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
		Tags:     []string{"lock", "unique_key", "tx_mode4", "insert", "contention"},
		Versions: "9i+",
		Related:  []string{"WE007", "WD005", "WD006"},
	}
}

// ─── 8. enq: ST — space transaction contention ─────────────────────────────

func ruleEnqSTContention() *Rule {
	return &Rule{
		ID:       "WD008",
		Name:     "enq: ST — 空间事务争用诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: ST - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: ST - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: ST - contention") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查表空间 extent 管理方式",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "dict_managed_tablespace_count") },
			Branches: []Branch{
				{
					Label: "存在字典管理表空间 — ST 锁的经典根因",
					Match: MatchGT(0),
					Then: &TreeNode{
						Step:  "检查争用的表空间",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "dict_managed_temp_tablespace") },
						Branches: []Branch{
							{
								Label: "临时表空间是字典管理 — 排序争用",
								Match: MatchGT(0),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "enq: ST 由字典管理临时表空间引起"},
									{Desc: "字典管理表空间每次 extent 分配都需要更新 UET$/FET$ 字典表，持有 ST 锁"},
									{Desc: "高并发排序时所有会话竞争同一 ST 锁进行 extent 分配"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "将临时表空间改为本地管理（locally managed）",
										SkillCommand: "/space tablespace_info",
										RawSQL:       "-- 创建新的本地管理临时表空间并替换\nCREATE TEMPORARY TABLESPACE temp2 TEMPFILE '/path/temp02.dbf' SIZE 10G AUTOEXTEND ON EXTENT MANAGEMENT LOCAL UNIFORM SIZE 1M;\nALTER DATABASE DEFAULT TEMPORARY TABLESPACE temp2;\nDROP TABLESPACE temp INCLUDING CONTENTS AND DATAFILES;",
										Risk: "需要停止使用旧表空间的会话", Rollback: "恢复旧临时表空间"},
									{Type: ActionInvestigate, Desc: "查看当前表空间管理方式",
										SkillCommand: "/sql \"SELECT tablespace_name, extent_management, allocation_type, segment_space_management, contents FROM dba_tablespaces ORDER BY tablespace_name\"",
										RawSQL:       "SELECT tablespace_name, extent_management, allocation_type, segment_space_management, contents FROM dba_tablespaces ORDER BY tablespace_name",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "数据表空间是字典管理",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "enq: ST 由字典管理数据表空间引起"},
									{Desc: "本地管理表空间可完全消除 ST 锁争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将字典管理表空间迁移为本地管理",
										SkillCommand: "/space tablespace_info",
										RawSQL:       "SELECT tablespace_name, extent_management, contents FROM dba_tablespaces WHERE extent_management='DICTIONARY'",
										Risk: "迁移需要维护窗口", Rollback: "无"},
									{Type: ActionPrevent, Desc: "所有新建表空间使用本地管理",
										SkillCommand: "/sql \"SELECT property_name, property_value FROM database_properties WHERE property_name LIKE '%TABLESPACE%'\"",
										RawSQL:       "SELECT property_name, property_value FROM database_properties WHERE property_name LIKE '%TABLESPACE%'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "全部本地管理 — ST 锁由 UNDO 或 SMON 操作引起",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 UNDO 表空间状态",
						Query: QueryUndoStats,
						Branches: []Branch{
							{
								Label: "UNDO 使用率高 — 空间回收争用",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "所有表空间已是本地管理，ST 锁可能由 UNDO 空间操作或 sort segment 管理引起"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 ST 锁等待的详细信息",
										SkillCommand: "/sql \"SELECT sid, event, p1, p2, p3, seconds_in_wait FROM v$session WHERE event='enq: ST - contention' AND state='WAITING'\"",
										RawSQL:       "SELECT sid, event, p1, p2, p3, seconds_in_wait FROM v$session WHERE event='enq: ST - contention' AND state='WAITING'",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 SMON 是否正在做空间回收",
										SkillCommand: "/sql \"SELECT s.sid, s.serial#, s.event, s.seconds_in_wait FROM v$session s WHERE s.pname='SMON'\"",
										RawSQL:       "SELECT s.sid, s.serial#, s.event, s.seconds_in_wait, s.sql_id FROM v$session s WHERE s.pname='SMON'",
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
		Tags:     []string{"lock", "space", "extent", "tablespace", "dictionary_managed"},
		Versions: "9i+",
		Related:  []string{"WE009"},
	}
}

// ─── 9. enq: US — undo segment contention ──────────────────────────────────

func ruleEnqUSContention() *Rule {
	return &Rule{
		ID:       "WD009",
		Name:     "enq: US — Undo 段争用诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: US - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: US - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: US - contention") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Undo 段争用比率",
			Query: QueryUndoSegmentRatio,
			Branches: []Branch{
				{
					Label: "contention_pct > 1% — Undo 段数量不足",
					Match: MatchGT(1),
					Then: &TreeNode{
						Step:  "检查 Undo 段数量和并发事务数",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "undo_segment_count") },
						Branches: []Branch{
							{
								Label: "Undo 段 < 10 — 明显不足",
								Match: MatchLT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "enq: US 由 Undo 段数量不足引起，争用率 > 1%"},
									{Desc: "Undo 段过少（< 10），高并发事务竞争有限的 Undo 段"},
									{Desc: "每个事务需要一个 Undo 段，段不足时必须等待其他事务释放"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 UNDO 表空间以支持更多 Undo 段自动创建",
										SkillCommand: "/undo check",
										RawSQL:       "SELECT tablespace_name, ROUND(SUM(bytes)/1024/1024) used_mb FROM dba_undo_extents GROUP BY tablespace_name",
										Risk: "增大 Undo 消耗更多存储", Rollback: "无需回滚"},
									{Type: ActionFix, Desc: "扩展 UNDO 表空间数据文件",
										SkillCommand: "/space extend undo",
										RawSQL:       "ALTER TABLESPACE UNDOTBS1 ADD DATAFILE '/path/undotbs02.dbf' SIZE 5G AUTOEXTEND ON MAXSIZE 20G",
										Risk: "占用额外存储", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 rollstat 争用详情",
										SkillCommand: "/sql \"SELECT usn, gets, waits, ROUND(waits/NULLIF(gets,0)*100,2) pct, writes, rssize/1024/1024 size_mb FROM v$rollstat ORDER BY waits DESC\"",
										RawSQL:       "SELECT usn, gets, waits, ROUND(waits/NULLIF(gets,0)*100,2) pct, writes, rssize/1024/1024 size_mb FROM v$rollstat ORDER BY waits DESC",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "Undo 段 >= 10 — 段够但空间不足",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Undo 段数量不少但争用率仍高，可能是空间不足导致段无法增长"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 UNDO 表空间容量",
										SkillCommand: "/undo check",
										RawSQL:       "SELECT f.tablespace_name, ROUND(f.bytes/1024/1024) total_mb, ROUND((f.bytes-NVL(u.bytes,0))/1024/1024) free_mb FROM dba_data_files f LEFT JOIN (SELECT tablespace_name, SUM(bytes) bytes FROM dba_undo_extents WHERE status='ACTIVE' GROUP BY tablespace_name) u ON f.tablespace_name=u.tablespace_name WHERE f.tablespace_name LIKE 'UNDO%'",
										Risk: "占用存储空间", Rollback: "无"},
									{Type: ActionFix, Desc: "降低 UNDO_RETENTION 释放过期 Undo 空间",
										SkillCommand: "/param undo_retention",
										RawSQL:       "ALTER SYSTEM SET UNDO_RETENTION=900 SCOPE=BOTH\n-- 注意: 过低可能导致 ORA-01555",
										Risk: "过低的 retention 可能导致 ORA-01555", Rollback: "ALTER SYSTEM SET UNDO_RETENTION=原值 SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Label: "contention_pct <= 1% — 轻微争用",
					Match: MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Undo 段争用率 <= 1%，影响较小"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控 Undo 争用趋势",
							SkillCommand: "/wait trend event='enq: US - contention'",
							RawSQL:       "SELECT usn, gets, waits, ROUND(waits/NULLIF(gets,0)*100,2) pct FROM v$rollstat WHERE waits > 0 ORDER BY pct DESC",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"undo", "contention", "rollback_segment"},
		Versions: "9i+",
		Related:  []string{"WE005"},
	}
}

// ─── 10. log buffer space ───────────────────────────────────────────────────

func ruleLogBufferSpace() *Rule {
	return &Rule{
		ID:       "WD010",
		Name:     "log buffer space 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "log buffer space"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "log buffer space", Op: OpPctGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 0.5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("log buffer space") < 0.5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 log buffer 大小",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "log_buffer_size_mb") },
			Branches: []Branch{
				{
					Label: "log buffer < 16MB — 可能偏小",
					Match: MatchLT(16),
					Then: &TreeNode{
						Step:  "检查 redo 生成速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "redo_mb_per_sec") },
						Branches: []Branch{
							{
								Label: "redo > 1MB/s — 高写入量溢出 log buffer",
								Match: MatchGT(1),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "log buffer space 等待由 log buffer 过小且 redo 生成速率高引起"},
									{Desc: "redo 生成速率 > 1MB/s，log buffer 容量不足以缓冲 redo 数据"},
									{Desc: "前台进程必须等待 LGWR 写出 redo 后才能继续生成新 redo"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 LOG_BUFFER 到 64MB 或更大",
										SkillCommand: "/param log_buffer",
										RawSQL:       "ALTER SYSTEM SET LOG_BUFFER=67108864 SCOPE=SPFILE\n-- 需要重启数据库生效",
										Risk: "需要重启数据库", Rollback: "ALTER SYSTEM SET LOG_BUFFER=原值 SCOPE=SPFILE"},
									{Type: ActionInvestigate, Desc: "检查 redo 生成速率趋势",
										SkillCommand: "/sql \"SELECT TO_CHAR(begin_time,'HH24:MI') tm, ROUND(value/1024/1024,2) redo_mb_per_sec FROM v$sysmetric_history WHERE metric_name='Redo Generated Per Sec' AND begin_time>SYSDATE-1/24 ORDER BY begin_time\"",
										RawSQL:       "SELECT TO_CHAR(begin_time,'HH24:MI') tm, ROUND(value/1024/1024,2) redo_mb_per_sec FROM v$sysmetric_history WHERE metric_name='Redo Generated Per Sec' AND begin_time>SYSDATE-1/24 ORDER BY begin_time",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 log file parallel write 延迟是否阻塞 LGWR",
										SkillCommand: "/wait detail event='log file parallel write'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event IN ('log buffer space','log file parallel write','log file sync')",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "redo <= 1MB/s — LGWR 写入慢导致 buffer 积压",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "redo 生成速率不高但 log buffer space 仍出现"},
									{Desc: "LGWR 写入 Redo Log 延迟高导致 log buffer 无法及时清空"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 log file parallel write 延迟",
										SkillCommand: "/wait detail event='log file parallel write'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='log file parallel write'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "将 Redo Log 迁移到低延迟存储",
										SkillCommand: "/redo check",
										RawSQL:       "SELECT group#, member, type FROM v$logfile ORDER BY group#",
										Risk: "迁移需要停库", Rollback: "迁移回原存储"},
								},
							},
						},
					},
				},
				{
					Label: "log buffer >= 16MB — 非 buffer 大小问题",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 LGWR 写延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("log file parallel write") },
						Branches: []Branch{
							{
								Label: "LGWR > 10ms — 存储 I/O 瓶颈导致 buffer 积压",
								Match: MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "log buffer 大小充足，但 LGWR 写延迟 > 10ms 导致 buffer 无法清空"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "改善 Redo Log 存储 I/O 性能",
										SkillCommand: "/redo check",
										RawSQL:       "SELECT l.group#, l.bytes/1024/1024 size_mb, l.status, lf.member FROM v$log l JOIN v$logfile lf ON l.group#=lf.group# ORDER BY l.group#",
										Risk: "存储变更需要维护窗口", Rollback: "无"},
								},
							},
							{
								Label: "LGWR 延迟正常 — redo 突发高峰",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "log buffer space 可能由 redo 突发高峰引起，log buffer 和 LGWR 性能正常"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查引起大量 redo 的 SQL",
										SkillCommand: "/ash top_sql event='log buffer space'",
										RawSQL:       "SELECT sql_id, executions, ROUND(buffer_gets/GREATEST(executions,1)) gets_per_exec FROM v$sqlarea ORDER BY buffer_gets DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "考虑增大 LOG_BUFFER 作为缓冲",
										SkillCommand: "/param log_buffer",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name='log_buffer'",
										Risk: "需要重启", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE004"}, // log file parallel write
		CausesOf: []string{"WE003"}, // log file sync
		Tags:     []string{"redo", "log_buffer", "lgwr"},
		Versions: "9i+",
		Related:  []string{"WE003", "WE004"},
	}
}

// ─── 11. log file switch (checkpoint incomplete) ────────────────────────────

func ruleLogFileSwitchCheckpoint() *Rule {
	return &Rule{
		ID:       "WD011",
		Name:     "log file switch (checkpoint incomplete) 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "log file switch (checkpoint incomplete)"},
			{Type: SignalWaitEvent, Key: "log file switch completion"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "log file switch (checkpoint incomplete)", Op: OpPctGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 0.5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("log file switch (checkpoint incomplete)") < 0.5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Redo Log 组数",
			Query: QueryRedoLogInfo,
			Branches: []Branch{
				{
					Label: "redo groups < 4 — 组数太少",
					Match: MatchLT(4),
					Then: &TreeNode{
						Step:  "检查 Redo Log 大小",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "redo_log_size_mb") },
						Branches: []Branch{
							{
								Label: "redo size < 200MB — 太小，频繁切换",
								Match: MatchLT(200),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "log file switch (checkpoint incomplete): Redo Log 组数 < 4 且每组 < 200MB"},
									{Desc: "Redo Log 切换过快，DBWR 来不及完成 checkpoint，下一组 Redo 不可用"},
									{Desc: "所有 COMMIT 操作都被阻塞直到 checkpoint 完成"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增加 Redo Log 组数到 4-6 个",
										SkillCommand: "/redo check",
										RawSQL:       "ALTER DATABASE ADD LOGFILE GROUP 4 ('/path/redo04a.log', '/path/redo04b.log') SIZE 1G;\nALTER DATABASE ADD LOGFILE GROUP 5 ('/path/redo05a.log', '/path/redo05b.log') SIZE 1G;",
										Risk: "增加 Redo 组占用存储空间", Rollback: "ALTER DATABASE DROP LOGFILE GROUP {group#}"},
									{Type: ActionUrgent, Desc: "增大每组 Redo Log 大小到 1-4GB",
										SkillCommand: "/redo resize",
										RawSQL:       "-- 新建更大的 Redo Log 组，然后删除旧组\n-- 步骤: 添加新大组 -> 切换 -> 删除旧小组\nSELECT group#, bytes/1024/1024 current_mb, status FROM v$log ORDER BY group#",
										Risk: "需要在低峰时段操作", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查当前日志切换频率",
										SkillCommand: "/redo switch_history",
										RawSQL:       "SELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) switches FROM v$log_history WHERE first_time>SYSDATE-1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "redo size >= 200MB — 组数不足是主因",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Redo Log 大小尚可但组数太少（< 4），checkpoint 来不及完成"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增加 Redo Log 组数到 4-6 个",
										SkillCommand: "/redo check",
										RawSQL:       "ALTER DATABASE ADD LOGFILE GROUP 4 SIZE 1G;\nALTER DATABASE ADD LOGFILE GROUP 5 SIZE 1G;",
										Risk: "增加存储空间", Rollback: "ALTER DATABASE DROP LOGFILE GROUP {group#}"},
									{Type: ActionInvestigate, Desc: "检查 DBWR 写入速度",
										SkillCommand: "/wait detail event='db file parallel write'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='db file parallel write'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "redo groups >= 4 — 组数够但 checkpoint 仍然慢",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 DBWR 写性能",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("db file parallel write") },
						Branches: []Branch{
							{
								Label: "DBWR > 10ms — DBWR 写慢导致 checkpoint 不完成",
								Match: MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Redo Log 组数足够，但 DBWR 写入延迟 > 10ms 导致 checkpoint 无法及时完成"},
									{Desc: "DBWR 写不完脏缓冲，当 Redo Log 轮转一圈回来时 checkpoint 仍未完成"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "改善数据文件存储 I/O 性能",
										SkillCommand: "/io check",
										RawSQL:       "SELECT name, phywrts, writetim, ROUND(writetim/GREATEST(phywrts,1),2) avg_write_ms FROM v$filestat ORDER BY avg_write_ms DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "存储变更需要维护窗口", Rollback: "无"},
									{Type: ActionFix, Desc: "增加 DB_WRITER_PROCESSES 和启用异步 I/O",
										SkillCommand: "/param db_writer_processes",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('db_writer_processes','disk_asynch_io','filesystemio_options')",
										Risk: "需要重启", Rollback: "恢复原参数"},
									{Type: ActionFix, Desc: "同时增大 Redo Log 大小延缓轮转",
										SkillCommand: "/redo info",
										RawSQL:       "SELECT group#, bytes/1024/1024 size_mb, status, archived FROM v$log ORDER BY group#",
										Risk: "增大 Redo 占用存储", Rollback: "无"},
								},
							},
							{
								Label: "DBWR 正常 — redo 生成速率太高",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "DBWR 正常但 checkpoint incomplete 仍出现，redo 生成速率过高"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 Redo Log 大小减缓切换频率",
										SkillCommand: "/redo switch_history",
										RawSQL:       "SELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) switches FROM v$log_history WHERE first_time>SYSDATE-1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1",
										Risk: "增大 Redo 占用存储", Rollback: "无"},
									{Type: ActionFix, Desc: "调整 FAST_START_MTTR_TARGET 加速增量 checkpoint",
										SkillCommand: "/param fast_start_mttr_target",
										RawSQL:       "ALTER SYSTEM SET FAST_START_MTTR_TARGET=30 SCOPE=BOTH",
										Risk: "加速 checkpoint 增加 DBWR 负载", Rollback: "ALTER SYSTEM SET FAST_START_MTTR_TARGET=原值 SCOPE=BOTH"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"WE013"}, // db file parallel write
		CausesOf: []string{"WE003"}, // log file sync
		Tags:     []string{"redo", "checkpoint", "dbwr", "log_switch"},
		Versions: "9i+",
		Related:  []string{"WE003", "WE004", "WE013", "WD012"},
	}
}

// ─── 12. log file switch (archiving needed) ─────────────────────────────────

func ruleLogFileSwitchArchiving() *Rule {
	return &Rule{
		ID:       "WD012",
		Name:     "log file switch (archiving needed) 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "log file switch (archiving needed)"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "log file switch (archiving needed)", Op: OpPctGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非归档模式", Check: func(ctx *EvalContext) bool {
					return ctx.GetStr("metrics", "log_mode") == "NOARCHIVELOG"
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查归档目的地状态",
			Query: QueryArchiveStatus,
			Branches: []Branch{
				{
					Label: "归档目的地异常",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查归档目的地磁盘空间",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "archive_dest_free_pct") },
						Branches: []Branch{
							{
								Label: "归档目的地空间 < 10% — 磁盘满",
								Match: MatchLT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "log file switch (archiving needed): 归档目的地磁盘空间不足 (< 10%)"},
									{Desc: "ARCn 进程无法写入归档日志，导致 Redo Log 无法切换"},
									{Desc: "所有 DML 操作被阻塞直到归档空间恢复"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即清理归档目的地空间",
										SkillCommand: "/sql \"SELECT dest_id, status, error, target FROM v$archive_dest WHERE status<>'INACTIVE'\"",
										RawSQL:       "SELECT dest_id, dest_name, status, error, target, space_limit, space_used FROM v$archive_dest WHERE status <> 'INACTIVE'",
										Risk: "删除归档日志可能影响恢复能力", Rollback: "无"},
									{Type: ActionUrgent, Desc: "删除过期归档日志释放空间",
										SkillCommand: "/sql \"SELECT name, completion_time, blocks*block_size/1024/1024 size_mb FROM v$archived_log WHERE completion_time < SYSDATE-7 AND deleted='NO' ORDER BY completion_time FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "-- 通过 RMAN 删除过期归档\n-- RMAN> DELETE ARCHIVELOG ALL COMPLETED BEFORE 'SYSDATE-7';\nSELECT name, completion_time, blocks*block_size/1024/1024 size_mb FROM v$archived_log WHERE completion_time < SYSDATE-7 AND deleted='NO' ORDER BY completion_time FETCH FIRST 20 ROWS ONLY",
										Risk: "删除归档日志影响 Point-in-Time Recovery", Rollback: "无法恢复已删除的归档日志"},
									{Type: ActionFix, Desc: "扩展归档目的地空间或添加新目的地",
										SkillCommand: "/param log_archive_dest_1",
										RawSQL:       "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2='LOCATION=/new_archive_path' SCOPE=BOTH",
										Risk: "需要提前准备存储空间", Rollback: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2='' SCOPE=BOTH"},
								},
							},
							{
								Label: "归档空间充足 — ARCn 进程问题",
								Match: MatchGTE(10),
								Then: &TreeNode{
									Step:  "检查 ARCn 进程状态",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "arc_process_active") },
									Branches: []Branch{
										{
											Label: "ARCn 进程不足或异常",
											Match: MatchLT(2),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "归档空间充足但 ARCn 进程不足或异常"},
												{Desc: "ARCn 进程无法跟上 Redo 生成速率"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增加 LOG_ARCHIVE_MAX_PROCESSES",
													SkillCommand: "/param log_archive_max_processes",
													RawSQL:       "ALTER SYSTEM SET LOG_ARCHIVE_MAX_PROCESSES=4 SCOPE=BOTH",
													Risk: "增加 ARCn 进程消耗更多 CPU 和 I/O", Rollback: "ALTER SYSTEM SET LOG_ARCHIVE_MAX_PROCESSES=原值 SCOPE=BOTH"},
												{Type: ActionInvestigate, Desc: "检查 ARCn 进程状态",
													SkillCommand: "/sql \"SELECT process, status, log_sequence, state FROM v$managed_standby WHERE process LIKE 'ARC%'\"",
													RawSQL:       "SELECT process, status, log_sequence, state FROM v$managed_standby WHERE process LIKE 'ARC%'",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "ARCn 正常 — 归档目的地 I/O 慢",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "ARCn 进程运行正常但归档目的地写入慢"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查归档目的地写入速度",
													SkillCommand: "/sql \"SELECT dest_id, status, error FROM v$archive_dest WHERE status<>'INACTIVE'\"",
													RawSQL:       "SELECT dest_id, dest_name, status, error, target FROM v$archive_dest WHERE status <> 'INACTIVE'",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "将归档目的地迁移到高速存储",
													SkillCommand: "/param log_archive_dest_1",
													RawSQL:       "SELECT name, value FROM v$parameter WHERE name LIKE 'log_archive_dest%' AND value IS NOT NULL",
													Risk: "迁移需要停归档", Rollback: "恢复原目的地"},
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
		CausesOf: []string{"WE003"}, // log file sync
		Tags:     []string{"redo", "archive", "disk_space", "log_switch"},
		Versions: "9i+",
		Related:  []string{"WE003", "WE004", "WD011"},
	}
}

// ─── 13. direct path read temp ──────────────────────────────────────────────

func ruleDirectPathReadTemp() *Rule {
	return &Rule{
		ID:       "WD013",
		Name:     "direct path read temp 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "direct path read temp"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "direct path read temp", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 3%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("direct path read temp") < 3
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 PGA 使用情况和磁盘排序比例",
			Query: QueryPGAAdvice,
			Branches: []Branch{
				{
					Label: "PGA advice 显示增大 PGA 可显著降低磁盘操作",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查临时表空间 I/O 延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "temp_io_avg_ms") },
						Branches: []Branch{
							{
								Label: "temp I/O > 10ms — 临时表空间存储慢",
								Match: MatchGT(10),
								Then: &TreeNode{
									Step:  "检查 PGA aggregate target 利用率",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "pga_cache_hit_pct") },
									Branches: []Branch{
										{
											Label: "PGA cache hit < 80% — PGA 严重不足",
											Match: MatchLT(80),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "direct path read temp 由 PGA 不足导致排序/HASH 溢出到临时表空间"},
												{Desc: "PGA cache hit < 80%，大量工作区操作无法在内存完成"},
												{Desc: "临时表空间 I/O 延迟 > 10ms 进一步加剧性能影响"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "增大 PGA_AGGREGATE_TARGET",
													SkillCommand: "/param pga_aggregate_target",
													RawSQL:       "SELECT PGA_TARGET_FOR_ESTIMATE/1024/1024 target_mb, ESTD_PGA_CACHE_HIT_PERCENTAGE hit_pct, ESTD_EXTRA_BYTES_RW FROM v$pga_target_advice ORDER BY 1",
													Risk: "增大 PGA 减少 SGA 可用空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
												{Type: ActionFix, Desc: "将临时表空间迁移到 SSD 存储",
													SkillCommand: "/space tablespace_info",
													RawSQL:       "SELECT tablespace_name, file_name, bytes/1024/1024 size_mb, autoextensible FROM dba_temp_files ORDER BY tablespace_name",
													Risk: "迁移需要重建临时表空间", Rollback: "恢复原临时表空间"},
												{Type: ActionInvestigate, Desc: "找出消耗大量 temp 的 SQL",
													SkillCommand: "/ash top_sql event='direct path read temp'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path read temp' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "PGA cache hit >= 80% — 个别大 SQL 溢出",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "PGA 整体利用率尚可，但个别大 SQL 排序/HASH 溢出到 temp"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "定位消耗大量 PGA 的 SQL",
													SkillCommand: "/ash top_sql event='direct path read temp'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path read temp' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "优化 SQL 减少排序和 HASH JOIN 数据量",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "SQL 优化需要测试验证", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "temp I/O <= 10ms — PGA 不足是主因",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "临时表空间 I/O 延迟正常，direct path read temp 主要由 PGA 不足引起"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET 减少磁盘排序",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT PGA_TARGET_FOR_ESTIMATE/1024/1024 target_mb, ESTD_PGA_CACHE_HIT_PERCENTAGE hit_pct FROM v$pga_target_advice ORDER BY 1",
										Risk: "增大 PGA 减少 SGA 可用空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查当前 PGA 分配",
										SkillCommand: "/sql \"SELECT name, value/1024/1024 mb FROM v$pgastat WHERE name IN ('aggregate PGA target parameter','aggregate PGA auto target','total PGA allocated','total PGA inuse','global memory bound')\"",
										RawSQL:       "SELECT name, value/1024/1024 mb FROM v$pgastat WHERE name IN ('aggregate PGA target parameter','aggregate PGA auto target','total PGA allocated','total PGA inuse','global memory bound')",
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
		Tags:     []string{"pga", "temp", "sort", "hash_join", "direct_read"},
		Versions: "9i+",
		Related:  []string{"WE014", "WD014"},
	}
}

// ─── 14. direct path write temp ─────────────────────────────────────────────

func ruleDirectPathWriteTemp() *Rule {
	return &Rule{
		ID:       "WD014",
		Name:     "direct path write temp 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "direct path write temp"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "direct path write temp", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 3%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("direct path write temp") < 3
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否同时出现 direct path read temp",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("direct path read temp") },
			Branches: []Branch{
				{
					Label: "read temp 也高 — 排序/HASH 溢出读写并存",
					Match: MatchGT(3),
					Then: &TreeNode{
						Step:  "检查 PGA cache hit percentage",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "pga_cache_hit_pct") },
						Branches: []Branch{
							{
								Label: "PGA cache hit < 70% — PGA 严重不足",
								Match: MatchLT(70),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "direct path write temp 与 read temp 同时高位"},
									{Desc: "PGA cache hit < 70%，大量排序/HASH JOIN 数据溢出到临时表空间"},
									{Desc: "写入 temp（溢出）后再读回（多路合并），形成反复磁盘 I/O"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 PGA_AGGREGATE_TARGET 到当前 2 倍",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT name, value/1024/1024 mb FROM v$pgastat WHERE name IN ('aggregate PGA target parameter','aggregate PGA auto target','total PGA allocated','maximum PGA allocated','over allocation count')",
										Risk: "增大 PGA 减少 SGA 可用空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "找出产生大量磁盘排序的 SQL",
										SkillCommand: "/ash top_sql event='direct path write temp'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event IN ('direct path write temp','direct path read temp') AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "优化 Top SQL 减少排序数据量",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST +PEEKED_BINDS'))",
										Risk: "SQL 优化需要验证", Rollback: "无"},
								},
							},
							{
								Label: "PGA cache hit >= 70% — 个别超大操作溢出",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "PGA 整体利用率可接受，个别超大排序或 HASH JOIN 操作溢出"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位超大排序 SQL",
										SkillCommand: "/ash top_sql event='direct path write temp'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path write temp' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "考虑对特定会话设置更大的 workarea_size_policy",
										SkillCommand: "/param workarea_size_policy",
										RawSQL:       "-- 对特定会话:\nALTER SESSION SET SORT_AREA_SIZE=524288000; -- 500MB\nALTER SESSION SET HASH_AREA_SIZE=524288000;",
										Risk: "大 workarea 可能消耗过多内存", Rollback: "ALTER SESSION SET SORT_AREA_SIZE=DEFAULT"},
								},
							},
						},
					},
				},
				{
					Label: "read temp 不高 — 纯写入溢出",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查临时表空间 I/O 写入性能",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "temp_write_avg_ms") },
						Branches: []Branch{
							{
								Label: "temp write > 10ms — 临时表空间写性能差",
								Match: MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "direct path write temp 写入延迟 > 10ms，临时表空间存储性能差"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将临时表空间迁移到 SSD",
										SkillCommand: "/space tablespace_info",
										RawSQL:       "SELECT tablespace_name, file_name, bytes/1024/1024 size_mb FROM dba_temp_files",
										Risk: "需要重建临时表空间", Rollback: "恢复原临时表空间"},
									{Type: ActionFix, Desc: "增大 PGA 减少溢出",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT PGA_TARGET_FOR_ESTIMATE/1024/1024 target_mb, ESTD_PGA_CACHE_HIT_PERCENTAGE hit_pct FROM v$pga_target_advice ORDER BY 1",
										Risk: "增大 PGA 减少 SGA 可用空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
								},
							},
							{
								Label: "temp write 正常 — PGA 溢出正在发生",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "临时表空间写性能正常，PGA 溢出正在写入 temp 文件"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT name, value/1024/1024 mb FROM v$pgastat WHERE name LIKE '%PGA%'",
										Risk: "增大 PGA 减少 SGA 空间", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查溢出 SQL",
										SkillCommand: "/ash top_sql event='direct path write temp'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='direct path write temp' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
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
		Tags:     []string{"pga", "temp", "sort", "hash_join", "direct_write"},
		Versions: "9i+",
		Related:  []string{"WE014", "WD013"},
	}
}

// ─── 15. row cache lock ─────────────────────────────────────────────────────

func ruleRowCacheLock() *Rule {
	return &Rule{
		ID:       "WD015",
		Name:     "row cache lock 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "row cache lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "row cache lock", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("row cache lock") < 1
				}},
				// Only fire when row cache lock is dominant (>25%).
				// At lower percentages, other concurrent issues (ITL, buffer busy)
				// are more likely the primary cause.
				{Desc: "row cache lock < 25%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("row cache lock") < 25
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查 row cache 争用的 cache 类型",
			Check: func(ctx *EvalContext) interface{} {
				// Try metric first (from burst).
				if v := ctx.GetStr("metrics", "row_cache_top_type"); v != "" {
					return v
				}
				// Fallback: query v$rowcache to find top contention cache.
				result, err := ctx.ExecuteQuery(QueryRowCacheStats, nil)
				if err != nil || result == nil {
					return "unknown"
				}
				m, ok := result.(map[string]interface{})
				if !ok {
					return "unknown"
				}
				rows, ok := m["rows"].([]map[string]interface{})
				if !ok || len(rows) == 0 {
					return "unknown"
				}
				// Distinguish sequence NEXTVAL contention vs DDL contention:
				// - NEXTVAL: high dc_sequences getmisses (cache refill)
				// - DDL: high dc_objects modifications (dictionary changes)
				// DDL also causes dc_sequences getmisses (OBJ# allocation), so
				// compare dc_objects modifications vs dc_sequences getmisses.
				var seqMisses, objMods float64
				for _, row := range rows {
					param := strings.ToLower(fmt.Sprintf("%v", row["parameter"]))
					misses := rowValueToFloat(row["getmisses"])
					mods := rowValueToFloat(row["modifications"])
					if strings.Contains(param, "dc_sequences") {
						seqMisses += misses
					}
					if strings.Contains(param, "dc_objects") || strings.Contains(param, "dc_tablespaces") || strings.Contains(param, "dc_users") {
						objMods += mods
					}
				}
				// If dc_objects has significant modifications → DDL contention.
				if objMods > seqMisses*0.3 && objMods > 100 {
					return "dc_objects"
				}
				// dc_sequences dominates — verify user sequences exist.
				if seqMisses > 0 {
					seqResult, seqErr := ctx.ExecuteQuery(QuerySequenceCacheInfo, nil)
					if seqErr == nil && seqResult != nil {
						if sm, ok := seqResult.(map[string]interface{}); ok {
							if srows, ok := sm["rows"].([]map[string]interface{}); ok && len(srows) > 0 {
								return "dc_sequences"
							}
						}
					}
					return "dc_objects" // no user sequences with small cache
				}
				return "dc_objects"
			},
			Branches: []Branch{
				{
					Label: "dc_sequences — 序列缓存不足",
					Match: MatchEquals("dc_sequences"),
					Then: &TreeNode{
						Step: "检查序列 CACHE 设置",
						Check: func(ctx *EvalContext) interface{} {
							if v := ctx.GetFloat("metrics", "min_sequence_cache"); v > 0 {
								return v
							}
							// Fallback: query dba_sequences for min cache.
							result, err := ctx.ExecuteQuery(QuerySequenceCacheInfo, nil)
							if err != nil || result == nil {
								return float64(20) // assume small cache
							}
							m, ok := result.(map[string]interface{})
							if !ok {
								return float64(20)
							}
							rows, ok := m["rows"].([]map[string]interface{})
							if !ok || len(rows) == 0 {
								return float64(200) // no small-cache sequences found
							}
							return rowValueToFloat(rows[0]["cache_size"])
						},
						Branches: []Branch{
							{
								Label: "CACHE < 100 — 序列缓存过小",
								Match: MatchLT(100),
								Severity: SeverityHigh, // BoostSeverityByImpact will escalate if row cache lock is dominant
								Findings: []Finding{
									{Desc: "row cache lock 由序列缓存过小引起（CACHE < 100）"},
									{Desc: "高并发 NEXTVAL 调用耗尽缓存后必须更新数据字典获取新范围"},
									{Desc: "更新数据字典需要持有 row cache lock，所有后续 NEXTVAL 排队等待"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大热点序列 CACHE 到 1000 以上",
										SkillCommand: "/sql \"SELECT sequence_owner, sequence_name, cache_size, last_number, increment_by FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') AND cache_size < 100 ORDER BY cache_size\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} CACHE 1000;\n-- 对所有 CACHE < 100 的业务序列执行",
										Risk: "实例崩溃时最多丢失 CACHE 大小个序列号（可接受间隙）", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} CACHE 原值"},
									{Type: ActionInvestigate, Desc: "检查 row cache 各 cache 的争用统计",
										SkillCommand: "/sql \"SELECT cache#, type, subordinate#, parameter, gets, getmisses, ROUND(getmisses/NULLIF(gets,0)*100,2) miss_pct, modifications FROM v$rowcache WHERE gets > 0 ORDER BY getmisses DESC FETCH FIRST 15 ROWS ONLY\"",
										RawSQL:       "SELECT cache#, type, parameter, gets, getmisses, ROUND(getmisses/NULLIF(gets,0)*100,2) miss_pct, modifications FROM v$rowcache WHERE gets > 0 ORDER BY getmisses DESC FETCH FIRST 15 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "CACHE >= 100 — 缓存够但并发极高",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "序列 CACHE >= 100 但 row cache lock 仍出现，并发极高"},
									{Desc: "建议进一步增大 CACHE 到 5000-10000"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "进一步增大序列 CACHE 到 5000+",
										SkillCommand: "/sql \"SELECT sequence_name, cache_size FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') ORDER BY cache_size\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} CACHE 5000",
										Risk: "更大的序列号间隙", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} CACHE 原值"},
									{Type: ActionPrevent, Desc: "考虑使用 NOORDER（RAC 环境）减少跨实例争用",
										SkillCommand: "/sql \"SELECT sequence_name, cache_size, order_flag FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') AND order_flag='Y'\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} NOORDER;\n-- 注意: NOORDER 在 RAC 中避免跨实例协调",
										Risk: "NOORDER 可能导致序列号非单调递增", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} ORDER"},
								},
							},
						},
					},
				},
				{
					Label: "dc_objects / dc_tablespaces — 数据字典争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step: "检查 row cache miss 比例",
						Check: func(ctx *EvalContext) interface{} {
							if v := ctx.GetFloat("metrics", "row_cache_miss_pct"); v > 0 {
								return v
							}
							// Fallback: extract from QueryRowCacheStats result.
							result, err := ctx.ExecuteQuery(QueryRowCacheStats, nil)
							if err != nil || result == nil {
								return float64(0.5) // assume moderate
							}
							m, ok := result.(map[string]interface{})
							if !ok {
								return float64(0.5)
							}
							rows, ok := m["rows"].([]map[string]interface{})
							if !ok || len(rows) == 0 {
								return float64(0.5)
							}
							return rowValueToFloat(rows[0]["miss_pct"])
						},
						Branches: []Branch{
							{
								Label: "miss_pct > 1% — Shared Pool 不足导致字典缓存驱逐",
								Match: MatchGT(1),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "row cache lock 由数据字典缓存 miss 率 > 1% 引起"},
									{Desc: "Shared Pool 中字典缓存被驱逐，需要重新从磁盘加载字典信息"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 SHARED_POOL_SIZE 保留更多字典缓存",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "SELECT pool, name, ROUND(bytes/1024/1024) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "增大 SGA 影响 PGA 可用", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查各 row cache 的 miss 率",
										SkillCommand: "/sql \"SELECT parameter, gets, getmisses, ROUND(getmisses/NULLIF(gets,0)*100,2) miss_pct FROM v$rowcache WHERE gets > 0 ORDER BY miss_pct DESC FETCH FIRST 15 ROWS ONLY\"",
										RawSQL:       "SELECT parameter, gets, getmisses, ROUND(getmisses/NULLIF(gets,0)*100,2) miss_pct FROM v$rowcache WHERE gets > 0 ORDER BY miss_pct DESC FETCH FIRST 15 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "miss_pct <= 1% — DDL 或权限变更导致字典修改争用",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "row cache miss 率正常，争用可能由频繁 DDL 或 GRANT 操作引起"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否有 DDL 操作修改数据字典",
										SkillCommand: "/sql \"SELECT s.sid, s.username, s.sql_id, q.sql_text FROM v$session s LEFT JOIN v$sql q ON s.sql_id=q.sql_id AND ROWNUM=1 WHERE s.command IN (1,2,3,4,9,11,15,39,40)\"",
										RawSQL:       "SELECT s.sid, s.username, s.sql_id, s.command FROM v$session s WHERE s.command IN (1,2,3,4,9,11,15,39,40) AND s.status='ACTIVE'",
										Risk: "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "将 DDL 操作移到低峰时段",
										SkillCommand: "/ash trend event='row cache lock'",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event='row cache lock' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
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
		Tags:     []string{"row_cache", "sequence", "dictionary", "shared_pool"},
		Versions: "9i+",
		Related:  []string{"WE010"},
	}
}

// ─── 16. latch: cache buffers chains ────────────────────────────────────────

func ruleLatchCacheBuffersChains() *Rule {
	return &Rule{
		ID:       "WD016",
		Name:     "latch: cache buffers chains 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "latch: cache buffers chains"},
			{Type: SignalWaitEvent, Key: "latch free"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "latch: cache buffers chains", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("latch: cache buffers chains") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 cache buffers chains latch 争用统计",
			Query: QueryLatchStats,
			Branches: []Branch{
				{
					Label: "sleeps/gets > 1% — 严重 latch 争用",
					Match: MatchGT(1),
					Then: &TreeNode{
						Step:  "检查热点块（通过 ASH p1 参数定位 latch 地址）",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Label: "找到热点对象 — 热块导致 CBC latch 争用",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查热点对象类型",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "hot_object_type") },
									Branches: []Branch{
										{
											Label: "INDEX — 索引热块",
											Match: MatchEquals("INDEX"),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "latch: cache buffers chains 由索引热块引起"},
												{Desc: "大量并发访问同一索引块，导致保护该块的 CBC latch 争用"},
												{Desc: "常见于序列主键索引的最右叶块（right-most leaf block）"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用 HASH 分区索引分散热点",
													SkillCommand: "/index analyze {index_name}",
													RawSQL:       "SELECT index_name, leaf_blocks, distinct_keys, clustering_factor FROM dba_indexes WHERE index_name='{index_name}'",
													Risk: "HASH 分区索引不支持范围扫描", Rollback: "重建为普通索引"},
												{Type: ActionFix, Desc: "考虑使用反转键索引（Reverse Key Index）",
													SkillCommand: "/sql \"SELECT index_name, index_type, uniqueness FROM dba_indexes WHERE table_name='{table_name}'\"",
													RawSQL:       "ALTER INDEX {index_name} REBUILD REVERSE",
													Risk: "反转键索引不支持范围扫描", Rollback: "ALTER INDEX {index_name} REBUILD NOREVERSE"},
												{Type: ActionInvestigate, Desc: "通过 ASH 定位热点 latch 地址对应的块",
													SkillCommand: "/sql \"SELECT p1 latch_addr, p1raw, COUNT(*) waits FROM v$active_session_history WHERE event='latch: cache buffers chains' AND sample_time>SYSDATE-1/24 GROUP BY p1, p1raw ORDER BY 3 DESC FETCH FIRST 5 ROWS ONLY\"",
													RawSQL:       "SELECT p1 latch_addr, p1raw, COUNT(*) waits FROM v$active_session_history WHERE event='latch: cache buffers chains' AND sample_time>SYSDATE-1/24 GROUP BY p1, p1raw ORDER BY 3 DESC FETCH FIRST 5 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "TABLE — 表热块",
											Match: MatchEquals("TABLE"),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "表数据块热点导致 CBC latch 争用"},
												{Desc: "大量并发读写同一表块"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用 HASH 分区表分散热点块",
													SkillCommand: "/partition recommend {table_name}",
													RawSQL:       "SELECT table_name, blocks, avg_row_len, num_rows FROM dba_tables WHERE table_name='{table_name}'",
													Risk: "分区改造需要维护窗口", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "检查热点 SQL",
													SkillCommand: "/ash top_sql event='latch: cache buffers chains'",
													RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='latch: cache buffers chains' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "其他对象类型",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "非典型对象类型导致 CBC latch 争用"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "详细分析热点块",
													SkillCommand: "/ash hot_object event='latch: cache buffers chains'",
													RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='latch: cache buffers chains' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "sleeps/gets <= 1% — 轻度争用",
					Match: MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "cache buffers chains latch 争用率 <= 1%，影响较小"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控 latch 争用趋势",
							SkillCommand: "/sql \"SELECT name, gets, misses, sleeps, ROUND(sleeps/NULLIF(gets,0)*100,4) sleep_pct FROM v$latch WHERE name='cache buffers chains'\"",
							RawSQL:       "SELECT name, gets, misses, sleeps, ROUND(sleeps/NULLIF(gets,0)*100,4) sleep_pct FROM v$latch WHERE name='cache buffers chains'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"latch", "hot_block", "buffer_cache", "contention"},
		Versions: "9i+",
		Related:  []string{"WE005", "WE012"},
	}
}

// ─── 17. gc current request ─────────────────────────────────────────────────

func ruleGCCurrentRequest() *Rule {
	return &Rule{
		ID:       "WD017",
		Name:     "gc current request 诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc current request"},
			{Type: SignalWaitEvent, Key: "gc current block 2-way"},
			{Type: SignalWaitEvent, Key: "gc current block 3-way"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc current request", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 gc current block 平均传输时间",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("gc current request") },
			Branches: []Branch{
				{
					Label: "avg > 3ms — 块传输延迟严重",
					Match: MatchGT(3),
					Then: &TreeNode{
						Step:  "检查互联网络延迟",
						Query: QueryInterconnectStats,
						Branches: []Branch{
							{
								Label: "interconnect > 1ms — 网络瓶颈",
								Match: MatchGT(1),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "gc current request 平均 > 3ms，互联网络延迟 > 1ms"},
									{Desc: "current block 传输用于修改操作（DML），网络瓶颈直接影响所有跨实例 DML"},
									{Desc: "current block 需要从远程实例获取最新版本才能修改"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并升级互联网络",
										SkillCommand: "/rac interconnect",
										RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
										Risk: "网络变更需要维护窗口", Rollback: "无"},
									{Type: ActionUrgent, Desc: "确认 Interconnect 未使用公网",
										SkillCommand: "/rac check",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name='cluster_interconnects'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "配置 Service Affinity 将修改操作集中到单实例",
										SkillCommand: "/rac service check",
										RawSQL:       "SELECT name, inst_id, goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
										Risk: "需要应用端配合", Rollback: "恢复原 Service 配置"},
								},
							},
							{
								Label: "interconnect <= 1ms — 远程实例 LMS 处理慢",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查远程实例 LMS 进程负载",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "lms_busy_pct") },
									Branches: []Branch{
										{
											Label: "LMS busy > 80% — LMS 进程过载",
											Match: MatchGT(80),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "网络延迟正常但 gc current 传输慢，远程 LMS 进程繁忙 > 80%"},
												{Desc: "LMS 进程来不及处理 current block 请求导致排队"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增加 GCS_SERVER_PROCESSES 数量",
													SkillCommand: "/param gcs_server_processes",
													RawSQL:       "ALTER SYSTEM SET GCS_SERVER_PROCESSES=4 SCOPE=SPFILE\n-- 需要重启实例",
													Risk: "需要重启实例", Rollback: "ALTER SYSTEM SET GCS_SERVER_PROCESSES=原值 SCOPE=SPFILE"},
												{Type: ActionInvestigate, Desc: "检查 LMS 进程状态",
													SkillCommand: "/sql \"SELECT inst_id, pname, event, seconds_in_wait FROM gv$session WHERE pname LIKE 'LMS%'\"",
													RawSQL:       "SELECT inst_id, pname, event, seconds_in_wait FROM gv$session WHERE pname LIKE 'LMS%'",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "LMS 不忙 — 热点对象跨实例争用",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "gc current request 慢但网络和 LMS 正常，热点对象跨实例争用"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用 Service Affinity 分离读写负载",
													SkillCommand: "/rac service check",
													RawSQL:       "SELECT name, inst_id, goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
													Risk: "需要应用配合", Rollback: "恢复原配置"},
												{Type: ActionInvestigate, Desc: "定位跨实例争用的热点对象",
													SkillCommand: "/ash hot_object event='gc current request'",
													RawSQL:       "SELECT o.object_name, o.object_type, h.inst_id, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event LIKE 'gc current%' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type, h.inst_id ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg 1-3ms — 可接受但需关注",
					Match: MatchBetween(1, 3),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "gc current request 平均 1-3ms，在 RAC 环境可接受但需关注趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控 gc 延迟趋势",
							SkillCommand: "/wait trend event='gc current request'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE 'gc current%' AND wait_class<>'Idle'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 1ms — 正常",
					Match: MatchLT(1),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "gc current request 平均 < 1ms，RAC 互联网络性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/rac check",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'gc current%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE011"}, // gc buffer busy
		Tags:     []string{"rac", "gc", "interconnect", "current_block", "dml"},
		Versions: "10g+ RAC",
		Related:  []string{"WE011", "WD001", "WD018"},
	}
}

// ─── 18. gc cr request ──────────────────────────────────────────────────────

func ruleGCCRRequest() *Rule {
	return &Rule{
		ID:       "WD018",
		Name:     "gc cr request 诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc cr request"},
			{Type: SignalWaitEvent, Key: "gc cr block 2-way"},
			{Type: SignalWaitEvent, Key: "gc cr block 3-way"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc cr request", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 gc cr block 平均传输时间",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("gc cr request") },
			Branches: []Branch{
				{
					Label: "avg > 3ms — CR 块传输延迟严重",
					Match: MatchGT(3),
					Then: &TreeNode{
						Step:  "检查互联网络延迟",
						Query: QueryInterconnectStats,
						Branches: []Branch{
							{
								Label: "interconnect > 1ms — 网络瓶颈",
								Match: MatchGT(1),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "gc cr request 平均 > 3ms，互联网络延迟 > 1ms"},
									{Desc: "CR block 传输用于一致性读（SELECT），影响所有跨实例查询性能"},
									{Desc: "远程实例需要构建 CR 副本并通过互联网络传输"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并升级互联网络到 10GbE 或更高",
										SkillCommand: "/rac interconnect",
										RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
										Risk: "网络变更需要维护窗口", Rollback: "无"},
									{Type: ActionFix, Desc: "配置查询负载的 Service Affinity",
										SkillCommand: "/rac service check",
										RawSQL:       "SELECT name, inst_id, goal, clb_goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
										Risk: "需要应用配合", Rollback: "恢复原配置"},
								},
							},
							{
								Label: "interconnect <= 1ms — CR 构建慢或热块争用",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查是否有大量 undo apply 导致 CR 构建慢",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "cr_undo_records_applied_per_sec") },
									Branches: []Branch{
										{
											Label: "undo apply > 1000/s — CR 构建复杂",
											Match: MatchGT(1000),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "网络正常但 CR 块构建慢，每秒 undo apply > 1000"},
												{Desc: "远程实例需要大量 undo 回滚来构建一致性读版本"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "缩短长事务减少 CR 构建复杂度",
													SkillCommand: "/lock check",
													RawSQL:       "SELECT s.sid, s.serial#, s.username, t.start_time, t.used_ublk undo_blocks FROM v$session s JOIN v$transaction t ON s.saddr=t.ses_addr ORDER BY t.used_ublk DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "定位产生大量 CR 请求的 SQL",
													SkillCommand: "/ash top_sql event='gc cr request'",
													RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM gv$active_session_history h WHERE h.event LIKE 'gc cr%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "undo apply 正常 — 热块跨实例读取",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "gc cr request 由热块跨实例一致性读引起"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "使用 Service Affinity 将查询集中到数据所在实例",
													SkillCommand: "/rac service check",
													RawSQL:       "SELECT name, inst_id, goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
													Risk: "需要应用配合", Rollback: "恢复原配置"},
												{Type: ActionInvestigate, Desc: "定位热点对象和实例分布",
													SkillCommand: "/ash hot_object event='gc cr request'",
													RawSQL:       "SELECT o.object_name, h.inst_id, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event LIKE 'gc cr%' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, h.inst_id ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg 1-3ms — 可接受",
					Match: MatchBetween(1, 3),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "gc cr request 平均 1-3ms，RAC 环境可接受"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控 gc cr 延迟趋势",
							SkillCommand: "/wait trend event='gc cr request'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE 'gc cr%' AND wait_class<>'Idle' ORDER BY time_waited_micro DESC",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 1ms — 正常",
					Match: MatchLT(1),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "gc cr request 平均 < 1ms，RAC CR 块传输性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/rac check",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'gc cr%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE011"}, // gc buffer busy
		Tags:     []string{"rac", "gc", "interconnect", "cr_block", "consistent_read"},
		Versions: "10g+ RAC",
		Related:  []string{"WE011", "WD001", "WD017"},
	}
}

// ─── 19. cell single block physical read (Exadata) ──────────────────────────

func ruleCellSingleBlockRead() *Rule {
	return &Rule{
		ID:       "WD019",
		Name:     "cell single block physical read 诊断 (Exadata)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cell single block physical read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cell single block physical read", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 Exadata 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "exadata_cell_count") == 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 cell single block 平均延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("cell single block physical read") },
			Branches: []Branch{
				{
					Label: "avg > 1ms — Flash Cache 命中率可能下降",
					Match: MatchGT(1),
					Then: &TreeNode{
						Step:  "检查 Flash Cache 命中率",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "flash_cache_hit_pct") },
						Branches: []Branch{
							{
								Label: "Flash Cache hit < 95% — 命中率不足",
								Match: MatchLT(95),
								Then: &TreeNode{
									Step:  "检查 Flash Cache 容量利用",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "flash_cache_used_pct") },
									Branches: []Branch{
										{
											Label: "Flash Cache 已满 > 90%",
											Match: MatchGT(90),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "cell single block read > 1ms，Exadata Flash Cache 命中率 < 95%"},
												{Desc: "Flash Cache 容量已满（> 90%），热数据被频繁驱逐"},
												{Desc: "读取必须回退到磁盘，延迟从 < 0.5ms 上升到 > 1ms"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 Flash Cache 统计",
													SkillCommand: "/sql \"SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name LIKE '%flash%'\"",
													RawSQL:       "SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name LIKE '%flash%'",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "配置 cell flash cache 优先级（KEEP/NONE）",
													SkillCommand: "/sql \"SELECT segment_name, segment_type, cell_flash_cache FROM dba_segments WHERE cell_flash_cache='DEFAULT' AND segment_type IN ('TABLE','INDEX') AND owner NOT IN ('SYS','SYSTEM') FETCH FIRST 20 ROWS ONLY\"",
													RawSQL:       "-- 对不需要 Flash Cache 的大表设置 NONE\nALTER TABLE {cold_table} STORAGE(CELL_FLASH_CACHE NONE);\n-- 对热点表设置 KEEP\nALTER TABLE {hot_table} STORAGE(CELL_FLASH_CACHE KEEP);",
													Risk: "NONE 设置会导致该表读取变慢", Rollback: "ALTER TABLE {table_name} STORAGE(CELL_FLASH_CACHE DEFAULT)"},
												{Type: ActionInvestigate, Desc: "定位热点对象",
													SkillCommand: "/ash hot_object event='cell single block physical read'",
													RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='cell single block physical read' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "Flash Cache 未满 — 数据访问模式问题",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "Flash Cache 未满但命中率低，数据访问模式导致缓存效率差"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "分析热点 SQL 的访问模式",
													SkillCommand: "/ash top_sql event='cell single block physical read'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='cell single block physical read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "优化 SQL 减少物理读",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "Flash Cache hit >= 95% — 存储 cell 本身慢",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Flash Cache 命中率 >= 95% 但平均延迟仍 > 1ms"},
									{Desc: "可能是 cell 节点内部 I/O 子系统负载高或硬件降级"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 cell 节点健康状态",
										SkillCommand: "/sql \"SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util','cpu_util')\"",
										RawSQL:       "SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util','cpu_util')",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查是否有磁盘故障告警",
										SkillCommand: "/sql \"SELECT cell_name, severity, description FROM v$cell_alerts WHERE severity IN ('critical','warning') ORDER BY alert_time DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "SELECT cell_name, severity, description FROM v$cell_alerts WHERE severity IN ('critical','warning') ORDER BY alert_time DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "avg <= 1ms — Exadata 正常范围",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 Buffer Cache Hit Ratio",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "buffer_cache_hit_ratio") },
						Branches: []Branch{
							{
								Label: "Buffer Hit < 95% — 可增大 DB Cache 减少物理读",
								Match: MatchLT(95),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "cell single block read 延迟正常（<= 1ms），但物理读比例偏高"},
									{Desc: "增大 Buffer Cache 可以减少 cell 层物理读请求"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 DB_CACHE_SIZE",
										SkillCommand: "/param db_cache_size",
										RawSQL:       "SELECT size_for_estimate, estd_physical_read_factor FROM v$db_cache_advice ORDER BY size_for_estimate",
										Risk: "增大 SGA 占用更多内存", Rollback: "ALTER SYSTEM SET DB_CACHE_SIZE=原值 SCOPE=BOTH"},
								},
							},
							{
								Label: "Buffer Hit >= 95% — 性能良好",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "cell single block read <= 1ms 且 Buffer Hit >= 95%，Exadata I/O 性能良好"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "持续监控 Exadata I/O 趋势",
										SkillCommand: "/wait trend event='cell single block physical read'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='cell single block physical read'",
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
		Tags:     []string{"exadata", "flash_cache", "io", "cell", "physical_read"},
		Versions: "11g+ Exadata",
		Related:  []string{"WE001"},
	}
}

// ─── 20. db file parallel read ──────────────────────────────────────────────

func ruleDBFileParallelRead() *Rule {
	return &Rule{
		ID:       "WD020",
		Name:     "db file parallel read 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file parallel read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file parallel read", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("db file parallel read") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 db file parallel read 平均等待时间",
			Query: QueryWaitAvgTime,
			Branches: []Branch{
				{
					Label: "avg > 10ms — 多块读延迟高",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "判断 parallel read 的来源",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "recovery_session_active") },
						Branches: []Branch{
							{
								Label: "恢复进程活跃 — instance recovery / media recovery",
								Match: MatchGT(0),
								Then: &TreeNode{
									Step:  "检查恢复类型",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "recovery_type") },
									Branches: []Branch{
										{
											Label: "instance recovery — 实例恢复中",
											Match: MatchEquals("instance_recovery"),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "db file parallel read 由实例恢复（crash recovery）引起"},
												{Desc: "Oracle 正在并行读取数据块进行前滚（redo apply），延迟 > 10ms"},
												{Desc: "实例恢复期间性能下降是正常现象，但可通过调优加速"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查恢复进度",
													SkillCommand: "/sql \"SELECT opname, target, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, elapsed_seconds, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork\"",
													RawSQL:       "SELECT opname, target, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, elapsed_seconds, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "调整 FAST_START_MTTR_TARGET 减少未来恢复时间",
													SkillCommand: "/param fast_start_mttr_target",
													RawSQL:       "ALTER SYSTEM SET FAST_START_MTTR_TARGET=60 SCOPE=BOTH",
													Risk: "加速 checkpoint 增加正常运行时 DBWR 负载", Rollback: "ALTER SYSTEM SET FAST_START_MTTR_TARGET=原值 SCOPE=BOTH"},
											},
										},
										{
											Label: "其他恢复操作",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "db file parallel read 由恢复操作引起（非 crash recovery）"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查恢复操作详情",
													SkillCommand: "/sql \"SELECT s.sid, s.program, s.event, s.sql_id FROM v$session s WHERE s.program LIKE '%recover%' OR s.program LIKE '%RMAN%'\"",
													RawSQL:       "SELECT s.sid, s.program, s.event, s.sql_id FROM v$session s WHERE s.program LIKE '%recover%' OR s.program LIKE '%RMAN%'",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "非恢复操作 — 批量预读/buffer cache warmup",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查 Buffer Cache Hit Ratio",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "buffer_cache_hit_ratio") },
									Branches: []Branch{
										{
											Label: "Buffer Hit < 90% — 冷启动或大量缺失预读",
											Match: MatchLT(90),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "db file parallel read 由 buffer cache 大量 miss 触发的预读引起"},
												{Desc: "Buffer Hit < 90%，Oracle 预读多个非连续块以加速填充 cache"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大 DB_CACHE_SIZE 减少 cache miss",
													SkillCommand: "/param db_cache_size",
													RawSQL:       "SELECT size_for_estimate, estd_physical_read_factor FROM v$db_cache_advice ORDER BY size_for_estimate",
													Risk: "增大 SGA 占用更多内存", Rollback: "ALTER SYSTEM SET DB_CACHE_SIZE=原值 SCOPE=BOTH"},
												{Type: ActionInvestigate, Desc: "检查触发 parallel read 的 SQL",
													SkillCommand: "/ash top_sql event='db file parallel read'",
													RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='db file parallel read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "Buffer Hit >= 90% — 存储 I/O 延迟问题",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "Buffer Hit 正常但 parallel read 延迟高，存储 I/O 性能需检查"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查存储 I/O 延迟",
													SkillCommand: "/io check",
													RawSQL:       "SELECT name, phyrds, ROUND(readtim/GREATEST(phyrds,1),2) avg_read_ms FROM v$filestat ORDER BY avg_read_ms DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg 5-10ms — 中等延迟",
					Match: MatchBetween(5, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "db file parallel read 平均 5-10ms，预读操作延迟中等"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查存储 I/O 性能",
							SkillCommand: "/io check",
							RawSQL:       "SELECT name, phyrds, ROUND(readtim/GREATEST(phyrds,1),2) avg_ms FROM v$filestat ORDER BY avg_ms DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "定位触发 parallel read 的 SQL",
							SkillCommand: "/ash top_sql event='db file parallel read'",
							RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='db file parallel read' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 5ms — 正常",
					Match: MatchLT(5),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "db file parallel read 平均 < 5ms，预读延迟正常"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/wait trend event='db file parallel read'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='db file parallel read'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io", "parallel_read", "recovery", "prefetch", "buffer_cache"},
		Versions: "9i+",
		Related:  []string{"WE001", "WE013"},
	}
}

// ─── kksfbc child completion — hard parse storm ─────────────────────────────

func ruleKksfbcChildCompletion() *Rule {
	return &Rule{
		ID:       "WD024",
		Name:     "kksfbc child completion — 硬解析风暴",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "kksfbc child completion"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "kksfbc child completion", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("kksfbc child completion") < 5
				}},
			},
		},
		Tree: &TreeNode{
			Step: "分析 kksfbc 等待占比和并发模式",
			Check: func(ctx *EvalContext) interface{} {
				pct := ctx.WaitPct("kksfbc child completion")
				// Check if library cache lock/pin is also significant (compounding effect).
				libLockPct := ctx.WaitPct("library cache lock") + ctx.WaitPct("library cache pin")
				if pct > 50 && libLockPct > 20 {
					return "severe_with_lib_lock"
				}
				if pct > 50 {
					return "severe"
				}
				return "moderate"
			},
			Branches: []Branch{
				{
					Label:    "kksfbc 高 + library cache lock 高 — 硬解析导致 library cache 耗尽",
					Match:    MatchEquals("severe_with_lib_lock"),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "kksfbc child completion 等待占比高，大量会话在等待子游标编译完成"},
						{Desc: "同时 library cache lock 争用严重，说明 shared pool 中 library cache 容量不足"},
						{Desc: "根因: 大量不同 SQL 文本（literal SQL）导致硬解析风暴，library cache 被快速填满后频繁 age out"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查 literal SQL 比例（未使用绑定变量的 SQL）",
							SkillCommand: "/sql \"SELECT force_matching_signature, COUNT(*) cnt, MIN(sql_id) sample_id FROM v$sqlarea WHERE force_matching_signature > 0 GROUP BY force_matching_signature HAVING COUNT(*) > 10 ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY\"",
							RawSQL:       "SELECT force_matching_signature, COUNT(*) cnt, MIN(sql_id) sample_id FROM v$sqlarea WHERE force_matching_signature > 0 GROUP BY force_matching_signature HAVING COUNT(*) > 10 ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "临时启用 CURSOR_SHARING=FORCE 减少硬解析",
							RawSQL: "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
							Risk:   "可能导致部分执行计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
						{Type: ActionFix, Desc: "增大 SHARED_POOL_SIZE 缓解 library cache 压力",
							SkillCommand: "/sga check",
							RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 15 ROWS ONLY",
							Risk:   "增大 SGA 影响 PGA", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值"},
					},
				},
				{
					Label:    "kksfbc 高 — 硬解析风暴",
					Match:    MatchEquals("severe"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "kksfbc child completion 等待占比高，大量并发硬解析"},
						{Desc: "每个新 SQL 需要编译生成子游标，高并发硬解析导致 kksfbc 串行化等待"},
						{Desc: "根因: 应用层使用 literal SQL（动态拼接而非绑定变量）"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "应用层改用绑定变量减少硬解析",
							SkillCommand: "/ash top_sql event='kksfbc child completion'",
							RawSQL:       "SELECT sql_id, parse_calls, executions, version_count FROM v$sqlarea WHERE parse_calls > 100 AND executions <= parse_calls*2 ORDER BY parse_calls DESC FETCH FIRST 20 ROWS ONLY",
							Risk: "需修改应用代码", Rollback: "无"},
						{Type: ActionFix, Desc: "临时启用 CURSOR_SHARING=FORCE",
							RawSQL: "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
							Risk:   "可能导致部分计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
					},
				},
				{
					Label:    "kksfbc 中等 — 硬解析有压力",
					Match:    MatchEquals("moderate"),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "kksfbc child completion 等待出现，存在一定程度的硬解析压力"},
						{Desc: "建议检查是否有高频 literal SQL 可以改为绑定变量"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查硬解析率",
							RawSQL: "SELECT name, value FROM v$sysstat WHERE name IN ('parse count (total)','parse count (hard)','parse count (failures)')",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE006", "WE010"}, // cursor: pin S, library cache lock
		Tags:     []string{"hard_parse", "kksfbc", "literal_sql", "shared_pool", "library_cache"},
		Versions: "10g+",
		Related:  []string{"WD004", "WE010", "MI2-005"},
	}
}
