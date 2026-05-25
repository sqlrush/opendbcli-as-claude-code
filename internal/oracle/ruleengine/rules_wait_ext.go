/*-------------------------------------------------------------------------
 *
 * rules_wait_ext.go
 *	  Oracle rule engine — wait-event extensions (cluster waits, gc events, library cache pin).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_wait_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// extWaitEventRules returns 15 extended wait event diagnostic rules covering
// RAC multi-block gc, PQ redistribution, library cache load lock, enqueue
// contention (SQ/CF/FB), control file I/O, Exadata Smart Scan, and OS thread
// startup overhead.
func extWaitEventRules() []*Rule {
	return []*Rule{
		ruleGCCurrentMultiBlock(),      // gc current multi block request — RAC full scan cross-instance
		ruleGCCRMultiBlock(),           // gc cr multi block request — RAC CR multi block
		ruleGCFreelistWait(),           // gc freelist — RAC freelist contention
		rulePXDeqCreditSendBlkd(),      // PX Deq Credit: send blkd — PQ data redistribution blocked
		rulePXDeqExecuteReply(),        // PX Deq: Execute Reply — QC waiting for PX slaves
		ruleLibraryCacheLoadLock(),     // library cache: load lock — concurrent first-load
		ruleEnqSQContention(),          // enq: SQ — sequence ordering in RAC
		ruleEnqSQContentionSingle(),    // enq: SQ — sequence cache contention (single instance)
		ruleEnqCFContention(),          // enq: CF — controlfile access contention
		ruleEnqFBContention(),          // enq: FB — format block contention
		ruleDBFileParallelReadRecov(),  // db file parallel read — media recovery prefetch
		ruleControlFileSeqRead(),       // control file sequential read — frequent control file reads
		ruleControlFileParallelWrite(), // control file parallel write — controlfile write slow
		ruleCellSmartTableScan(),       // cell smart table scan — Exadata Smart Scan
		ruleCellSmartIndexScan(),       // cell smart index scan — Exadata index Smart Scan
		ruleOSThreadStartup(),          // os thread startup — process creation overhead
	}
}

// ─── 1. gc current multi block request (RAC) ─────────────────────────────────

func ruleGCCurrentMultiBlock() *Rule {
	return &Rule{
		ID:       "WE2-001",
		Name:     "gc current multi block request 诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc current multi block request"},
			{Type: SignalWaitEvent, Key: "gc current block 2-way"},
			{Type: SignalWaitEvent, Key: "gc current block 3-way"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc current multi block request", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 gc current multi block 平均传输时间",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("gc current multi block request") },
			Branches: []Branch{
				{
					Label: "avg > 5ms — 跨实例多块传输严重延迟",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查互联网络延迟",
						Query: QueryInterconnectStats,
						Branches: []Branch{
							{
								Label: "interconnect > 1ms — 网络带宽瓶颈",
								Match: MatchGT(1),
								Then: &TreeNode{
									Step:  "检查触发多块请求的 SQL（全表扫描 / IFFS）",
									Query: QueryASHTopSQL,
									Branches: []Branch{
										{
											Label: "找到全表扫描 SQL — 跨实例全扫是根因",
											Match: MatchDefault(),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "gc current multi block request 平均 > 5ms，互联网络延迟 > 1ms"},
												{Desc: "全表扫描或索引快速全扫描在 RAC 环境产生大量跨实例多块请求"},
												{Desc: "每个多块请求需通过互联网络传输 db_file_multiblock_read_count 个块"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "优化全表扫描 SQL，添加索引或分区裁剪",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "添加索引影响 DML 性能", Rollback: "DROP INDEX {index_name}"},
												{Type: ActionUrgent, Desc: "检查并升级互联网络带宽到 10GbE+",
													SkillCommand: "/rac interconnect",
													RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
													Risk: "网络变更需要维护窗口", Rollback: "无"},
												{Type: ActionFix, Desc: "配置 Service Affinity 将全扫描查询集中到单实例",
													SkillCommand: "/rac service check",
													RawSQL:       "SELECT name, inst_id, goal, clb_goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
													Risk: "需要应用端配合连接切换", Rollback: "恢复原 Service 配置"},
											},
										},
									},
								},
							},
							{
								Label: "interconnect <= 1ms — LMS 处理或对象分布问题",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查热点对象跨实例分布",
									Query: QueryASHHotObject,
									Branches: []Branch{
										{
											Label: "找到热点对象 — 对象被多实例并发全扫描",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "网络正常但 gc current multi block 传输慢"},
												{Desc: "热点对象被多实例并发全表扫描，LMS 需批量构建并传输 current 块"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "对热点大表使用分区或物化视图减少全扫描范围",
													SkillCommand: "/partition recommend {table_name}",
													RawSQL:       "SELECT o.object_name, o.object_type, h.inst_id, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event='gc current multi block request' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type, h.inst_id ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "分区改造需要维护窗口", Rollback: "无"},
												{Type: ActionFix, Desc: "增加 GCS_SERVER_PROCESSES 提高 LMS 处理能力",
													SkillCommand: "/param gcs_server_processes",
													RawSQL:       "ALTER SYSTEM SET GCS_SERVER_PROCESSES=4 SCOPE=SPFILE\n-- 需要重启实例",
													Risk: "需要重启实例", Rollback: "ALTER SYSTEM SET GCS_SERVER_PROCESSES=原值 SCOPE=SPFILE"},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Label: "avg 2-5ms — 中等延迟，关注趋势",
					Match: MatchBetween(2, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "gc current multi block request 平均 2-5ms，RAC 多块传输延迟中等"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控 gc multi block 延迟趋势",
							SkillCommand: "/wait trend event='gc current multi block request'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE 'gc current multi%' AND wait_class<>'Idle'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 2ms — 正常",
					Match: MatchLT(2),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "gc current multi block request 平均 < 2ms，RAC 多块传输性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/rac check",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'gc current multi%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"WD017"}, // gc current request
		CausesOf: []string{"WE011"}, // gc buffer busy
		Tags:     []string{"rac", "gc", "multi_block", "full_scan", "interconnect"},
		Versions: "10g+ RAC",
		Related:  []string{"WD017", "WD018", "WE011"},
	}
}

// ─── 2. gc cr multi block request (RAC) ──────────────────────────────────────

func ruleGCCRMultiBlock() *Rule {
	return &Rule{
		ID:       "WE2-002",
		Name:     "gc cr multi block request 诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc cr multi block request"},
			{Type: SignalWaitEvent, Key: "gc cr block 2-way"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc cr multi block request", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 gc cr multi block 平均传输时间",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("gc cr multi block request") },
			Branches: []Branch{
				{
					Label: "avg > 5ms — CR 多块传输延迟严重",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查触发 CR 多块读的 SQL",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Label: "找到全扫描查询 — 跨实例 CR 多块读",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查互联网络延迟",
									Query: QueryInterconnectStats,
									Branches: []Branch{
										{
											Label: "interconnect > 1ms — 网络瓶颈放大 CR 多块传输",
											Match: MatchGT(1),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "gc cr multi block request 平均 > 5ms，互联网络延迟 > 1ms"},
												{Desc: "全表扫描查询在 RAC 中产生大量跨实例 CR 多块请求"},
												{Desc: "远程实例需要批量构建 CR 副本并通过互联网络传输"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "优化全表扫描 SQL 减少跨实例 CR 请求",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "无", Rollback: "无"},
												{Type: ActionUrgent, Desc: "升级互联网络带宽",
													SkillCommand: "/rac interconnect",
													RawSQL:       "SELECT inst_id, name, ip_address, is_public FROM gv$cluster_interconnects",
													Risk: "网络变更需要维护窗口", Rollback: "无"},
												{Type: ActionFix, Desc: "配置 Service Affinity 减少跨实例访问",
													SkillCommand: "/rac service check",
													RawSQL:       "SELECT name, inst_id, goal, clb_goal FROM gv$active_services WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')",
													Risk: "需要应用配合", Rollback: "恢复原 Service 配置"},
											},
										},
										{
											Label: "interconnect <= 1ms — CR 构建延迟或 undo apply 过多",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "网络正常但 CR 多块传输慢，远程 CR 构建复杂度高"},
												{Desc: "可能是长事务导致 undo apply 量大，CR 版本构建耗时"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "缩短长事务减少 CR 构建复杂度",
													SkillCommand: "/lock check",
													RawSQL:       "SELECT s.sid, s.serial#, s.username, t.start_time, t.used_ublk undo_blocks FROM v$session s JOIN v$transaction t ON s.saddr=t.ses_addr ORDER BY t.used_ublk DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "无", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "定位产生大量 CR 多块请求的 SQL",
													SkillCommand: "/ash top_sql event='gc cr multi block request'",
													RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM gv$active_session_history h WHERE h.event LIKE 'gc cr multi%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg 2-5ms — 可接受但需关注",
					Match: MatchBetween(2, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "gc cr multi block request 平均 2-5ms，RAC CR 多块传输中等延迟"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控 gc cr multi block 趋势",
							SkillCommand: "/wait trend event='gc cr multi block request'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE 'gc cr multi%' AND wait_class<>'Idle'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 2ms — 正常",
					Match: MatchLT(2),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "gc cr multi block request 平均 < 2ms，性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/rac check",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'gc cr multi%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"WD018"}, // gc cr request
		CausesOf: []string{"WE011"}, // gc buffer busy
		Tags:     []string{"rac", "gc", "cr_multi_block", "full_scan", "consistent_read"},
		Versions: "10g+ RAC",
		Related:  []string{"WD017", "WD018", "WE2-001"},
	}
}

// ─── 3. gc freelist (RAC freelist contention) ────────────────────────────────

func ruleGCFreelistWait() *Rule {
	return &Rule{
		ID:       "WE2-003",
		Name:     "gc freelist 争用诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "gc freelist"},
			{Type: SignalWaitEvent, Key: "gc buffer busy acquire"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "gc freelist", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查热点段的 freelist 管理方式",
			Query: QuerySegmentStats,
			Branches: []Branch{
				{
					Label: "存在手动 freelist 管理段 — 经典 freelist 争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查热点对象是否使用 ASSM",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "segment_space_mgmt") },
						Branches: []Branch{
							{
								Label: "MANUAL — 手动 freelist 管理",
								Match: MatchEquals("MANUAL"),
								Then: &TreeNode{
									Step:  "检查并发 INSERT 频率",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "insert_session_count") },
									Branches: []Branch{
										{
											Label: "并发 INSERT > 20 — 大量并发插入争用 freelist",
											Match: MatchGT(20),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "gc freelist 由 RAC 环境中手动 freelist 管理的段引起"},
												{Desc: "多实例并发 INSERT 时争用 segment header 的 freelist 链"},
												{Desc: "MANUAL freelist 管理在 RAC 下性能极差，应迁移到 ASSM"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "将表空间迁移为 ASSM（Automatic Segment Space Management）",
													SkillCommand: "/space tablespace_info",
													RawSQL:       "SELECT tablespace_name, segment_space_management, extent_management FROM dba_tablespaces WHERE segment_space_management='MANUAL'",
													Risk: "需要重建表空间和移动数据", Rollback: "无"},
												{Type: ActionFix, Desc: "临时增加 FREELIST GROUPS 缓解争用",
													SkillCommand: "/sql \"SELECT table_name, freelists, freelist_groups FROM dba_tables WHERE table_name='{table_name}'\"",
													RawSQL:       "ALTER TABLE {table_name} STORAGE(FREELIST GROUPS {instance_count})",
													Risk: "增加存储空间", Rollback: "ALTER TABLE {table_name} STORAGE(FREELIST GROUPS 1)"},
											},
										},
										{
											Label: "并发 INSERT 适中 — 争用可控",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "gc freelist 争用存在但并发不高，可计划迁移到 ASSM"},
											},
											Actions: []Action{
												{Type: ActionPrevent, Desc: "计划迁移到 ASSM 表空间",
													SkillCommand: "/space tablespace_info",
													RawSQL:       "SELECT tablespace_name, segment_space_management FROM dba_tablespaces ORDER BY tablespace_name",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "AUTO (ASSM) — 非典型 freelist 争用",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "表空间使用 ASSM 但仍出现 gc freelist 等待，可能是段头 bitmap 争用"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查热点段的 L1/L2/L3 bitmap 块分布",
										SkillCommand: "/ash hot_object event='gc freelist'",
										RawSQL:       "SELECT o.object_name, o.object_type, h.inst_id, COUNT(*) waits FROM gv$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event LIKE 'gc freelist%' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type, h.inst_id ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE005"}, // buffer busy waits
		Tags:     []string{"rac", "gc", "freelist", "assm", "insert", "contention"},
		Versions: "9i+ RAC",
		Related:  []string{"WE005", "WD001", "WE011"},
	}
}

// ─── 4. PX Deq Credit: send blkd ────────────────────────────────────────────

func rulePXDeqCreditSendBlkd() *Rule {
	return &Rule{
		ID:       "WE2-004",
		Name:     "PX Deq Credit: send blkd 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "PX Deq Credit: send blkd"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "PX Deq Credit: send blkd", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 2%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("PX Deq Credit: send blkd") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 PQ 数据分配倾斜度",
			Query: QueryPQSkew,
			Branches: []Branch{
				{
					Label: "skew_ratio > 3 — PQ 数据分配严重倾斜",
					Match: MatchGT(3),
					Then: &TreeNode{
						Step:  "检查分发方式（hash / broadcast / range）",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "pq_distribution_method") },
						Branches: []Branch{
							{
								Label: "HASH — Hash 分发倾斜",
								Match: MatchEquals("HASH"),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "PX Deq Credit: send blkd 由并行查询数据重分配阻塞引起"},
									{Desc: "生产者进程等待消费者进程的 credit 才能发送数据"},
									{Desc: "Hash 分发因数据倾斜导致某些消费者过载，无法及时处理"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "检查数据倾斜并考虑使用 PQ_DISTRIBUTE hint",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST +PARTITION'))",
										Risk: "hint 可能影响计划选择", Rollback: "去除 hint"},
									{Type: ActionInvestigate, Desc: "检查 PX 进程等待和分配统计",
										SkillCommand: "/sql \"SELECT qcsid, server_group, server_set, server#, degree, req_degree FROM v$px_session WHERE qcsid IS NOT NULL ORDER BY qcsid, server_group, server#\"",
										RawSQL:       "SELECT qcsid, server_group, server_set, server#, degree, req_degree FROM v$px_session WHERE qcsid IS NOT NULL ORDER BY qcsid, server_group, server#",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "BROADCAST — 广播分发导致 consumer 过载",
								Match: MatchEquals("BROADCAST"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Broadcast 分发将所有数据发送给所有消费者，数据量大时消费者阻塞"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用 PQ_DISTRIBUTE(HASH, HASH) 替代 Broadcast",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
										Risk: "需要验证执行计划变化", Rollback: "去除 hint"},
								},
							},
							{
								Label: "其他分发方式",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "PQ 数据分发倾斜但非典型场景，需进一步分析"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析 PQ 进程详细分布和等待",
										SkillCommand: "/ash top_sql event='PX Deq Credit: send blkd'",
										RawSQL:       "SELECT h.sql_id, h.session_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='PX Deq Credit: send blkd' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.session_id ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "skew_ratio <= 3 — 消费者处理慢（非倾斜）",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查消费者等待事件",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("PX Deq Credit: send blkd") },
						Branches: []Branch{
							{
								Label: "avg > 50ms — 消费者严重阻塞",
								Match: MatchGT(50),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "PX 消费者处理缓慢阻塞生产者，非数据倾斜问题"},
									{Desc: "消费者可能在等待 I/O、排序或 hash join 溢出"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查消费者进程的等待事件",
										SkillCommand: "/sql \"SELECT s.sid, s.event, s.p1text, s.p1, s.seconds_in_wait FROM v$session s WHERE s.sid IN (SELECT server_session FROM v$px_session) AND s.state='WAITING'\"",
										RawSQL:       "SELECT s.sid, s.event, s.p1text, s.p1, s.seconds_in_wait FROM v$session s WHERE s.sid IN (SELECT sid FROM v$px_session WHERE qcsid IS NOT NULL) AND s.state='WAITING'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增大 PGA_AGGREGATE_TARGET 减少临时表空间溢出",
										SkillCommand: "/param pga_aggregate_target",
										RawSQL:       "SELECT name, value FROM v$parameter WHERE name='pga_aggregate_target'",
										Risk: "增加内存使用", Rollback: "ALTER SYSTEM SET PGA_AGGREGATE_TARGET=原值 SCOPE=BOTH"},
								},
							},
							{
								Label: "avg <= 50ms — 短暂阻塞，可接受",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "PX Deq Credit: send blkd 存在但延迟不高，PQ 运行基本正常"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控 PQ 性能趋势",
										SkillCommand: "/wait trend event='PX Deq Credit: send blkd'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'PX Deq%'",
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
		Tags:     []string{"parallel_query", "pq", "data_skew", "distribution", "credit"},
		Versions: "10g+",
		Related:  []string{"WE2-005"},
	}
}

// ─── 5. PX Deq: Execute Reply ────────────────────────────────────────────────

func rulePXDeqExecuteReply() *Rule {
	return &Rule{
		ID:       "WE2-005",
		Name:     "PX Deq: Execute Reply 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "PX Deq: Execute Reply"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "PX Deq: Execute Reply", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 2%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("PX Deq: Execute Reply") < 2
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 PX slave 的实际执行时间",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("PX Deq: Execute Reply") },
			Branches: []Branch{
				{
					Label: "avg > 5000ms — QC 等待 PX slaves 时间过长",
					Match: MatchGT(5000),
					Then: &TreeNode{
						Step:  "检查 DOP 降级情况",
						Query: QueryPQDOPDowngrade,
						Branches: []Branch{
							{
								Label: "存在 DOP 降级 — PX 资源不足导致降级",
								Match: MatchGT(0),
								Then: &TreeNode{
									Step:  "检查 parallel_max_servers 使用率",
									Query: QueryPQResourceUsage,
									Branches: []Branch{
										{
											Label: "使用率 > 80% — PQ 资源饱和",
											Match: MatchGT(80),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "PX Deq: Execute Reply 由 QC 等待 PX slaves 完成引起"},
												{Desc: "parallel_max_servers 使用率 > 80%，DOP 被降级"},
												{Desc: "PX slave 不足导致查询被串行化或降低并行度，执行时间大增"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "增大 parallel_max_servers",
													SkillCommand: "/param parallel_max_servers",
													RawSQL:       "ALTER SYSTEM SET PARALLEL_MAX_SERVERS=320 SCOPE=BOTH",
													Risk: "增加系统资源消耗", Rollback: "ALTER SYSTEM SET PARALLEL_MAX_SERVERS=原值 SCOPE=BOTH"},
												{Type: ActionFix, Desc: "使用 Resource Manager 限制单 SQL 的 DOP",
													SkillCommand: "/sql \"SELECT plan, group_or_subplan, parallel_degree_limit_p1 FROM dba_rsrc_plan_directives WHERE plan IN (SELECT name FROM dba_rsrc_plans WHERE is_top_plan='TRUE')\"",
													RawSQL:       "SELECT plan, group_or_subplan, parallel_degree_limit_p1 FROM dba_rsrc_plan_directives WHERE plan IN (SELECT name FROM dba_rsrc_plans WHERE is_top_plan='TRUE')",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "使用率 <= 80% — 降级原因为其他",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "DOP 降级但 PQ 资源不饱和，可能是 Resource Manager 或参数限制"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 PARALLEL_ADAPTIVE_MULTI_USER 和 Resource Manager 配置",
													SkillCommand: "/param parallel_adaptive_multi_user",
													RawSQL:       "SELECT name, value FROM v$parameter WHERE name IN ('parallel_adaptive_multi_user','parallel_degree_policy','parallel_min_time_threshold')",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "无 DOP 降级 — PX slaves 本身执行慢",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查 PX slave 的主要等待事件",
									Query: QueryASHTopSQL,
									Branches: []Branch{
										{
											Label: "找到慢 SQL — PX slave 在执行复杂操作",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "QC 等待 PX slaves 完成，slaves 本身执行时间长"},
												{Desc: "PX slave 可能在等待 I/O、排序溢出或 hash join"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "优化并行查询 SQL 减少 PX slave 工作量",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "无", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "检查 PX slave 详细等待",
													SkillCommand: "/sql \"SELECT s.sid, s.event, s.seconds_in_wait, px.degree FROM v$session s JOIN v$px_session px ON s.sid=px.sid WHERE s.state='WAITING' AND px.qcsid IS NOT NULL ORDER BY s.seconds_in_wait DESC\"",
													RawSQL:       "SELECT s.sid, s.event, s.seconds_in_wait, px.degree FROM v$session s JOIN v$px_session px ON s.sid=px.sid WHERE s.state='WAITING' AND px.qcsid IS NOT NULL ORDER BY s.seconds_in_wait DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg 1000-5000ms — 中等等待",
					Match: MatchBetween(1000, 5000),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "QC 等待 PX slaves 1-5 秒，并行查询执行时间偏长"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "定位慢并行查询 SQL",
							SkillCommand: "/ash top_sql event='PX Deq: Execute Reply'",
							RawSQL:       "SELECT h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='PX Deq: Execute Reply' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 1000ms — 正常并行查询等待",
					Match: MatchLT(1000),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "PX Deq: Execute Reply < 1s，QC 等待 PX slaves 时间正常"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控并行查询性能",
							SkillCommand: "/wait trend event='PX Deq: Execute Reply'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event LIKE 'PX Deq%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"parallel_query", "pq", "qc", "px_slave", "dop"},
		Versions: "10g+",
		Related:  []string{"WE2-004"},
	}
}

// ─── 6. library cache: load lock ─────────────────────────────────────────────

func ruleLibraryCacheLoadLock() *Rule {
	return &Rule{
		ID:       "WE2-006",
		Name:     "library cache: load lock 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "library cache: load lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "library cache: load lock", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("library cache: load lock") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Shared Pool free memory 比例",
			Query: QuerySPFreeMemory,
			Branches: []Branch{
				{
					Label: "free < 10% — Shared Pool 不足导致频繁加载",
					Match: MatchLT(10),
					Then: &TreeNode{
						Step:  "检查硬解析率",
						Query: QueryParseStats,
						Branches: []Branch{
							{
								Label: "硬解析率 > 30% — 大量首次加载争用 load lock",
								Match: MatchGT(30),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "library cache: load lock 由 Shared Pool 不足和高硬解析率共同引起"},
									{Desc: "Shared Pool 剩余 < 10%，对象被频繁驱逐后需重新加载"},
									{Desc: "多个会话同时首次加载同一对象时争用 load lock"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大 SHARED_POOL_SIZE",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "ALTER SYSTEM SET SHARED_POOL_SIZE=2G SCOPE=BOTH",
										Risk: "增大 SGA 影响 PGA 可用内存", Rollback: "ALTER SYSTEM SET SHARED_POOL_SIZE=原值 SCOPE=BOTH"},
									{Type: ActionFix, Desc: "减少硬解析：使用绑定变量或 CURSOR_SHARING",
										SkillCommand: "/param cursor_sharing",
										RawSQL:       "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
										Risk: "可能导致部分 SQL 计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查 Shared Pool 使用分布",
										SkillCommand: "/sql \"SELECT pool, name, ROUND(bytes/1024/1024,1) mb FROM v$sgastat WHERE pool='shared pool' AND bytes > 1048576 ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "SELECT pool, name, ROUND(bytes/1024/1024,1) mb FROM v$sgastat WHERE pool='shared pool' AND bytes > 1048576 ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "硬解析率正常 — Shared Pool 碎片化",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Shared Pool 剩余不足但硬解析率正常，可能是内存碎片化"},
									{Desc: "大对象（PL/SQL 包体）加载时找不到连续空间，触发清理和 load lock"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 SHARED_POOL_SIZE 并考虑 pin 大对象",
										SkillCommand: "/param shared_pool_size",
										RawSQL:       "-- Pin 大 PL/SQL 包到 Shared Pool\nEXEC DBMS_SHARED_POOL.KEEP('{package_name}','P')",
										Risk: "pin 对象占用 Shared Pool 固定空间", Rollback: "EXEC DBMS_SHARED_POOL.UNKEEP('{package_name}','P')"},
									{Type: ActionInvestigate, Desc: "检查 load lock 等待的对象",
										SkillCommand: "/ash detail event='library cache: load lock'",
										RawSQL:       "SELECT h.sql_id, h.current_obj#, h.p1, h.p2, COUNT(*) waits FROM v$active_session_history h WHERE h.event='library cache: load lock' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.current_obj#, h.p1, h.p2 ORDER BY 5 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "free >= 10% — 非 Shared Pool 不足，并发加载争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查争用的对象类型（SQL / PL/SQL / Java）",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "load_lock_object_type") },
						Branches: []Branch{
							{
								Label: "PACKAGE BODY — 大 PL/SQL 包并发编译",
								Match: MatchEquals("PACKAGE BODY"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "library cache: load lock 由多个会话并发首次加载同一 PL/SQL 包引起"},
									{Desc: "第一个会话加载时持有 load lock，其他会话排队等待"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "在启动时 pin 常用 PL/SQL 包",
										SkillCommand: "/sql \"SELECT owner, name, type, sharable_mem FROM v$db_object_cache WHERE type IN ('PACKAGE','PACKAGE BODY') AND sharable_mem > 1048576 ORDER BY sharable_mem DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "-- 在数据库启动脚本中 pin 大包\nEXEC DBMS_SHARED_POOL.KEEP('{owner}.{package_name}','P')",
										Risk: "pin 对象占用 Shared Pool 固定空间", Rollback: "EXEC DBMS_SHARED_POOL.UNKEEP('{owner}.{package_name}','P')"},
									{Type: ActionPrevent, Desc: "避免在高峰时段进行 DDL 或重编译",
										SkillCommand: "/ash trend event='library cache: load lock'",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event='library cache: load lock' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "其他对象类型",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "library cache: load lock 由并发首次加载非 PL/SQL 对象引起"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位 load lock 的具体对象",
										SkillCommand: "/sql \"SELECT kglnaown owner, kglnaobj name, kglobtyp type, kglobhs0 heap0_size FROM x$kglob WHERE kglobhs0 > 0 ORDER BY kglobhs0 DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "SELECT h.p1, h.p2, h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event='library cache: load lock' AND h.sample_time>SYSDATE-1/24 GROUP BY h.p1, h.p2, h.sql_id ORDER BY 4 DESC FETCH FIRST 10 ROWS ONLY",
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
		Tags:     []string{"library_cache", "load_lock", "shared_pool", "parse", "plsql"},
		Versions: "9i+",
		Related:  []string{"WE006", "WE010", "WD004"},
	}
}

// ─── 7. enq: SQ — sequence ordering in RAC ──────────────────────────────────

func ruleEnqSQContention() *Rule {
	return &Rule{
		ID:       "WE2-007",
		Name:     "enq: SQ — 序列跨实例排序争用诊断 (RAC)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: SQ - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: SQ - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 RAC 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") <= 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查热点序列的 ORDER/NOORDER 属性",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "ordered_sequence_count") },
			Branches: []Branch{
				{
					Label: "存在 ORDER 序列 — RAC 下 ORDER 序列需要跨实例同步",
					Match: MatchGT(0),
					Then: &TreeNode{
						Step:  "检查序列 CACHE 大小",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "min_sequence_cache") },
						Branches: []Branch{
							{
								Label: "CACHE < 100 — 序列缓存过小加剧 SQ 争用",
								Match: MatchLT(100),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "enq: SQ 由 RAC 环境中 ORDER 序列的跨实例排序同步引起"},
									{Desc: "ORDER 序列要求所有实例的序列号严格递增，每次缓存用完都需跨实例协调"},
									{Desc: "CACHE < 100 导致缓存频繁耗尽，跨实例协调非常频繁"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大序列 CACHE 到 1000+",
										SkillCommand: "/sql \"SELECT sequence_owner, sequence_name, cache_size, order_flag, last_number FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') AND order_flag='Y' ORDER BY cache_size\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} CACHE 1000",
										Risk: "更大的序列号间隙", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} CACHE 原值"},
									{Type: ActionFix, Desc: "评估是否可改为 NOORDER 序列",
										SkillCommand: "/sql \"SELECT sequence_owner, sequence_name, order_flag, cache_size FROM dba_sequences WHERE order_flag='Y' AND sequence_owner NOT IN ('SYS','SYSTEM')\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} NOORDER",
										Risk: "NOORDER 序列号在各实例间不保证顺序", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} ORDER"},
								},
							},
							{
								Label: "CACHE >= 100 — 缓存够但 ORDER 争用不可避免",
								Match: MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "序列 CACHE >= 100 但 ORDER 属性导致跨实例同步"},
									{Desc: "RAC 中 ORDER 序列是 SQ 争用的根本原因"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "改为 NOORDER 并增大 CACHE（根本方案）",
										SkillCommand: "/sql \"SELECT sequence_name, cache_size, order_flag FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') AND order_flag='Y'\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} NOORDER CACHE 5000",
										Risk: "序列号不保证全局单调递增", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} ORDER CACHE 原值"},
									{Type: ActionPrevent, Desc: "考虑使用 UUID 或其他方案替代序列",
										SkillCommand: "/ash trend event='enq: SQ - contention'",
										RawSQL:       "SELECT TO_CHAR(sample_time,'HH24:MI') tm, COUNT(*) waits FROM v$active_session_history WHERE event LIKE 'enq: SQ%' AND sample_time>SYSDATE-1/24 GROUP BY TO_CHAR(sample_time,'HH24:MI') ORDER BY 1",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "无 ORDER 序列 — NOORDER 下仍争用 SQ",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查序列 CACHE 大小",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "min_sequence_cache") },
						Branches: []Branch{
							{
								Label: "CACHE < 20 — NOCACHE 或 CACHE 过小",
								Match: MatchLT(20),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "NOORDER 序列 CACHE 过小或 NOCACHE，跨实例分配仍需频繁协调"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大序列 CACHE 到 1000+",
										SkillCommand: "/sql \"SELECT sequence_owner, sequence_name, cache_size, order_flag FROM dba_sequences WHERE sequence_owner NOT IN ('SYS','SYSTEM') AND cache_size < 20\"",
										RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} CACHE 1000",
										Risk: "更大的序列号间隙", Rollback: "ALTER SEQUENCE {owner}.{sequence_name} CACHE 原值"},
								},
							},
							{
								Label: "CACHE >= 20 — 其他原因",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "enq: SQ 争用但序列配置合理，可能是并发极高或其他 SQ 子类型"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "详细分析 SQ 锁类型和对象",
										SkillCommand: "/ash detail event='enq: SQ - contention'",
										RawSQL:       "SELECT h.p1, h.p2, h.p3, h.sql_id, COUNT(*) waits FROM v$active_session_history h WHERE h.event LIKE 'enq: SQ%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.p1, h.p2, h.p3, h.sql_id ORDER BY 5 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WD015"}, // row cache lock (sequence)
		Tags:     []string{"rac", "sequence", "sq_enqueue", "order", "contention"},
		Versions: "9i+ RAC",
		Related:  []string{"WD015"},
	}
}

// ─── 7b. enq: SQ — sequence cache contention (single instance) ──────────────

func ruleEnqSQContentionSingle() *Rule {
	return &Rule{
		ID:       "WE2-007b",
		Name:     "enq: SQ — 序列 CACHE 争用诊断 (单实例)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: SQ - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: SQ - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "RAC 环境由 WE2-007 处理", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "instance_count") > 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查序列 CACHE 配置",
			Query: QueryCursorStats, // 复用已有 query，实际检查序列
			Branches: []Branch{
				{
					Label:    "序列 CACHE 过小或 NOCACHE 导致 SQ 争用",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "enq: SQ - contention 在单实例环境下通常由序列 CACHE 值过小引起"},
						{Desc: "每次 CACHE 用完需要更新数据字典 seq$，引发 row cache lock 和 SQ enqueue 争用"},
						{Desc: "高并发 INSERT 使用 NOCACHE 或小 CACHE 序列时尤为严重"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看争用序列并增大 CACHE",
							SkillCommand: "/sql \"SELECT s.sequence_owner, s.sequence_name, s.cache_size, s.order_flag, s.last_number FROM dba_sequences s WHERE s.sequence_owner NOT IN ('SYS','SYSTEM','AUDSYS','DBSNMP') ORDER BY s.cache_size ASC FETCH FIRST 20 ROWS ONLY\"",
							RawSQL:       "ALTER SEQUENCE {owner}.{sequence_name} CACHE 1000",
							Risk:         "序列号间隙增大（不影响唯一性）",
							Rollback:     "ALTER SEQUENCE {owner}.{sequence_name} CACHE 原值"},
						{Type: ActionFix, Desc: "查看 ASH 确认争用序列对象",
							SkillCommand: "/sql \"SELECT h.current_obj#, o.object_name, COUNT(*) waits FROM v$active_session_history h LEFT JOIN dba_objects o ON h.current_obj# = o.object_id WHERE h.event = 'enq: SQ - contention' AND h.sample_time > SYSDATE - 1/24 GROUP BY h.current_obj#, o.object_name ORDER BY waits DESC FETCH FIRST 10 ROWS ONLY\"",
							RawSQL:       "SELECT current_obj#, COUNT(*) FROM v$active_session_history WHERE event LIKE 'enq: SQ%' AND sample_time > SYSDATE-1/24 GROUP BY current_obj# ORDER BY 2 DESC",
							Risk:         "无",
							Rollback:     "无"},
						{Type: ActionPrevent, Desc: "对高频 INSERT 表考虑用 UUID 或应用层生成 ID 替代序列",
							Risk:     "需要应用改造",
							Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WD015"},
		Tags:     []string{"sequence", "sq_enqueue", "cache", "contention", "single_instance"},
		Versions: "9i+",
		Related:  []string{"WE2-007", "WD015"},
	}
}

// ─── 8. enq: CF — controlfile access contention ─────────────────────────────

func ruleEnqCFContention() *Rule {
	return &Rule{
		ID:       "WE2-008",
		Name:     "enq: CF — 控制文件争用诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: CF - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: CF - contention", Op: OpPctGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 0.5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: CF - contention") < 0.5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查控制文件 I/O 延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("control file sequential read") },
			Branches: []Branch{
				{
					Label: "avg > 10ms — 控制文件 I/O 延迟高",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查控制文件存储位置",
						Query: QueryControlfileInfo,
						Branches: []Branch{
							{
								Label: "控制文件信息已获取",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查归档日志目的地状态",
									Query: QueryArchiveStatus,
									Branches: []Branch{
										{
											Label: "存在归档延迟 — 归档操作频繁更新控制文件",
											Match: MatchGT(0),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "enq: CF 由控制文件争用引起，控制文件 I/O > 10ms"},
												{Desc: "归档日志操作频繁更新控制文件中的归档记录"},
												{Desc: "控制文件 I/O 慢导致归档更新排队，CF 锁等待增加"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "将控制文件迁移到低延迟存储（SSD/NVMe）",
													SkillCommand: "/sql \"SELECT name, status, block_size, file_size_blks FROM v$controlfile\"",
													RawSQL:       "SELECT name, status, block_size, file_size_blks FROM v$controlfile",
													Risk: "迁移控制文件需要停库", Rollback: "迁移回原位置"},
												{Type: ActionFix, Desc: "减少控制文件更新频率（增大 CONTROL_FILE_RECORD_KEEP_TIME）",
													SkillCommand: "/param control_file_record_keep_time",
													RawSQL:       "SELECT name, value FROM v$parameter WHERE name='control_file_record_keep_time'",
													Risk: "保留时间过短可能影响 RMAN", Rollback: "ALTER SYSTEM SET CONTROL_FILE_RECORD_KEEP_TIME=原值 SCOPE=BOTH"},
											},
										},
										{
											Label: "归档正常 — 备份或其他操作争用控制文件",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "控制文件 I/O 慢但归档正常，可能是备份或频繁 checkpoint 更新控制文件"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "迁移控制文件到低延迟存储",
													SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
													RawSQL:       "SELECT name FROM v$controlfile",
													Risk: "需要停库", Rollback: "迁移回原位置"},
												{Type: ActionInvestigate, Desc: "检查 RMAN 备份是否影响控制文件",
													SkillCommand: "/sql \"SELECT operation, status, start_time, end_time FROM v$rman_status WHERE start_time > SYSDATE-1 ORDER BY start_time DESC\"",
													RawSQL:       "SELECT operation, status, start_time, end_time FROM v$rman_status WHERE start_time > SYSDATE-1 ORDER BY start_time DESC FETCH FIRST 10 ROWS ONLY",
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
					Label: "avg <= 10ms — 控制文件 I/O 正常，并发访问争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查是否 RAC 环境下多实例争用控制文件",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "instance_count") },
						Branches: []Branch{
							{
								Label: "RAC 多实例 — 跨实例控制文件争用",
								Match: MatchGT(1),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "RAC 环境多实例并发更新控制文件产生 CF 锁争用"},
									{Desc: "控制文件是共享资源，多实例 checkpoint/归档/备份操作串行化"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "错开各实例的备份和维护操作时间窗口",
										SkillCommand: "/sql \"SELECT inst_id, event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE '%control file%' OR event LIKE 'enq: CF%' ORDER BY time_waited_micro DESC\"",
										RawSQL:       "SELECT inst_id, event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM gv$system_event WHERE event LIKE '%control file%' OR event LIKE 'enq: CF%' ORDER BY time_waited_micro DESC",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "单实例 — 并发操作争用",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "单实例 CF 争用较轻，通常由备份和频繁 checkpoint 引起"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 CF 锁等待详细信息",
										SkillCommand: "/ash detail event='enq: CF - contention'",
										RawSQL:       "SELECT h.sql_id, h.p1, h.p2, h.p3, COUNT(*) waits FROM v$active_session_history h WHERE h.event LIKE 'enq: CF%' AND h.sample_time>SYSDATE-1/24 GROUP BY h.sql_id, h.p1, h.p2, h.p3 ORDER BY 5 DESC FETCH FIRST 10 ROWS ONLY",
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
		Tags:     []string{"controlfile", "cf_enqueue", "io", "archive", "backup"},
		Versions: "9i+",
		Related:  []string{"WE2-011", "WE2-012"},
	}
}

// ─── 9. enq: FB — format block contention ────────────────────────────────────

func ruleEnqFBContention() *Rule {
	return &Rule{
		ID:       "WE2-009",
		Name:     "enq: FB — 格式化块争用诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: FB - contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: FB - contention", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("enq: FB - contention") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查段空间扩展情况",
			Query: QuerySegmentStats,
			Branches: []Branch{
				{
					Label: "段正在扩展 — 新块格式化争用",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查是否使用 ASSM",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "segment_space_mgmt") },
						Branches: []Branch{
							{
								Label: "MANUAL — 手动空间管理加剧 FB 争用",
								Match: MatchEquals("MANUAL"),
								Then: &TreeNode{
									Step:  "检查并发 INSERT 会话数",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("ash", "insert_session_count") },
									Branches: []Branch{
										{
											Label: "并发 INSERT > 10 — 高并发格式化新块争用",
											Match: MatchGT(10),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "enq: FB 由段扩展时大量新块需要格式化引起"},
												{Desc: "MANUAL 空间管理下，格式化新块需要串行持有 FB 锁"},
												{Desc: "高并发 INSERT 导致频繁扩展和格式化，FB 锁成为瓶颈"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "迁移到 ASSM 表空间减少格式化争用",
													SkillCommand: "/space tablespace_info",
													RawSQL:       "SELECT tablespace_name, segment_space_management FROM dba_tablespaces WHERE segment_space_management='MANUAL'",
													Risk: "需要重建表空间和迁移数据", Rollback: "无"},
												{Type: ActionFix, Desc: "预分配足够空间避免频繁扩展",
													SkillCommand: "/sql \"SELECT segment_name, segment_type, extents, blocks, ROUND(bytes/1024/1024,1) mb FROM dba_segments WHERE segment_name='{table_name}'\"",
													RawSQL:       "ALTER TABLE {table_name} ALLOCATE EXTENT (SIZE 100M)",
													Risk: "预分配占用存储空间", Rollback: "无（已分配无法释放）"},
											},
										},
										{
											Label: "并发 INSERT 适中 — 争用可控",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "FB 争用存在但并发不高，建议计划迁移到 ASSM"},
											},
											Actions: []Action{
												{Type: ActionPrevent, Desc: "计划迁移到 ASSM 和预分配空间",
													SkillCommand: "/space tablespace_info",
													RawSQL:       "SELECT tablespace_name, segment_space_management FROM dba_tablespaces ORDER BY tablespace_name",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "AUTO (ASSM) — ASSM 下的 FB 争用较少见",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "ASSM 下仍出现 FB 争用，可能是 extent 分配时的格式化瓶颈"},
									{Desc: "通常发生在极高并发 INSERT 且段频繁扩展时"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用 UNIFORM extent size 减少 extent 分配频率",
										SkillCommand: "/space tablespace_info",
										RawSQL:       "SELECT tablespace_name, allocation_type, initial_extent, next_extent FROM dba_tablespaces WHERE contents='PERMANENT'",
										Risk: "UNIFORM 可能浪费空间", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "定位 FB 争用的热点段",
										SkillCommand: "/ash hot_object event='enq: FB - contention'",
										RawSQL:       "SELECT o.object_name, o.object_type, COUNT(*) waits FROM v$active_session_history h JOIN dba_objects o ON h.current_obj#=o.object_id WHERE h.event LIKE 'enq: FB%' AND h.sample_time>SYSDATE-1/24 GROUP BY o.object_name, o.object_type ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE005"}, // buffer busy waits
		Tags:     []string{"format_block", "fb_enqueue", "assm", "segment_extend", "insert"},
		Versions: "9i+",
		Related:  []string{"WE005", "WE2-003"},
	}
}

// ─── 10. db file parallel read — media recovery prefetch ────────────────────

func ruleDBFileParallelReadRecov() *Rule {
	return &Rule{
		ID:       "WE2-010",
		Name:     "db file parallel read — 介质恢复预读诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file parallel read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file parallel read", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "无恢复进程活跃", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("ash", "recovery_session_active") == 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查恢复操作类型",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "recovery_type") },
			Branches: []Branch{
				{
					Label: "media_recovery — Data Guard 或 RMAN 恢复中",
					Match: MatchEquals("media_recovery"),
					Then: &TreeNode{
						Step:  "检查恢复延迟（apply lag）",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "dg_apply_lag_seconds") },
						Branches: []Branch{
							{
								Label: "apply lag > 300s — 恢复落后较多",
								Match: MatchGT(300),
								Then: &TreeNode{
									Step:  "检查 parallel read 延迟",
									Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("db file parallel read") },
									Branches: []Branch{
										{
											Label: "avg > 10ms — 存储 I/O 拖慢恢复",
											Match: MatchGT(10),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "db file parallel read 由 Data Guard 介质恢复预读引起"},
												{Desc: "MRP 进程并行读取数据块进行 redo apply，存储延迟 > 10ms"},
												{Desc: "apply lag > 5 分钟，备库恢复严重落后"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "改善备库存储 I/O 性能",
													SkillCommand: "/io check",
													RawSQL:       "SELECT name, phyrds, ROUND(readtim/GREATEST(phyrds,1),2) avg_read_ms FROM v$filestat ORDER BY avg_read_ms DESC FETCH FIRST 10 ROWS ONLY",
													Risk: "存储变更需要维护窗口", Rollback: "无"},
												{Type: ActionFix, Desc: "增加 RECOVERY_PARALLELISM 加速恢复",
													SkillCommand: "/param recovery_parallelism",
													RawSQL:       "ALTER SYSTEM SET RECOVERY_PARALLELISM=8 SCOPE=SPFILE",
													Risk: "增加 CPU 和 I/O 负载", Rollback: "ALTER SYSTEM SET RECOVERY_PARALLELISM=0 SCOPE=SPFILE"},
												{Type: ActionInvestigate, Desc: "检查恢复进度",
													SkillCommand: "/sql \"SELECT opname, target, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork\"",
													RawSQL:       "SELECT opname, target, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "avg <= 10ms — I/O 正常，redo 量大导致落后",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "存储 I/O 正常但 apply lag > 5 分钟，redo 生成速率超过恢复能力"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增加 RECOVERY_PARALLELISM 提高恢复吞吐",
													SkillCommand: "/param recovery_parallelism",
													RawSQL:       "ALTER SYSTEM SET RECOVERY_PARALLELISM=8 SCOPE=SPFILE",
													Risk: "增加 CPU 负载", Rollback: "ALTER SYSTEM SET RECOVERY_PARALLELISM=0 SCOPE=SPFILE"},
												{Type: ActionInvestigate, Desc: "检查 MRP 进程状态和等待",
													SkillCommand: "/sql \"SELECT process, status, thread#, sequence#, block# FROM v$managed_standby WHERE process LIKE 'MRP%'\"",
													RawSQL:       "SELECT process, status, thread#, sequence#, block# FROM v$managed_standby WHERE process LIKE 'MRP%'",
													Risk: "无", Rollback: "无"},
											},
										},
									},
								},
							},
							{
								Label: "apply lag <= 300s — 恢复基本同步",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "介质恢复中 db file parallel read 出现但 apply lag 可接受"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控 apply lag 趋势",
										SkillCommand: "/sql \"SELECT name, value, datum_time FROM v$dataguard_stats WHERE name='apply lag'\"",
										RawSQL:       "SELECT name, value, datum_time FROM v$dataguard_stats WHERE name='apply lag'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "instance_recovery — 实例恢复",
					Match: MatchEquals("instance_recovery"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "db file parallel read 由实例恢复引起，属正常恢复过程"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查恢复进度",
							SkillCommand: "/sql \"SELECT opname, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork\"",
							RawSQL:       "SELECT opname, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, time_remaining FROM v$session_longops WHERE opname LIKE '%Recovery%' AND sofar<totalwork",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "其他恢复类型",
					Match: MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "db file parallel read 由恢复操作的预读引起"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查恢复操作详情",
							SkillCommand: "/sql \"SELECT s.sid, s.program, s.event FROM v$session s WHERE s.program LIKE '%recover%' OR s.program LIKE '%RMAN%' OR s.program LIKE '%MRP%'\"",
							RawSQL:       "SELECT s.sid, s.program, s.event FROM v$session s WHERE s.program LIKE '%recover%' OR s.program LIKE '%RMAN%' OR s.program LIKE '%MRP%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io", "parallel_read", "media_recovery", "data_guard", "mrp"},
		Versions: "9i+",
		Related:  []string{"WD020", "WE001"},
	}
}

// ─── 11. control file sequential read ────────────────────────────────────────

func ruleControlFileSeqRead() *Rule {
	return &Rule{
		ID:       "WE2-011",
		Name:     "control file sequential read 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "control file sequential read"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "control file sequential read", Op: OpPctGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("control file sequential read") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 control file sequential read 平均延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("control file sequential read") },
			Branches: []Branch{
				{
					Label: "avg > 10ms — 控制文件读取延迟高",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查控制文件存储类型（ASM / 文件系统 / NFS）",
						Query: QueryControlfileInfo,
						Branches: []Branch{
							{
								Label: "获取到控制文件信息",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查日志切换频率",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "log_switches_per_hour") },
									Branches: []Branch{
										{
											Label: "日志切换 > 6/小时 — 频繁切换增加控制文件读取",
											Match: MatchGT(6),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "control file sequential read > 10ms，控制文件存储延迟高"},
												{Desc: "每小时日志切换 > 6 次，每次切换都读取控制文件获取归档信息"},
												{Desc: "频繁读取叠加高延迟导致控制文件成为瓶颈"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "将控制文件迁移到低延迟存储",
													SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
													RawSQL:       "SELECT name, status, is_recovery_dest_file FROM v$controlfile",
													Risk: "迁移控制文件需要停库", Rollback: "迁移回原位置"},
												{Type: ActionFix, Desc: "增大 Redo Log 减少切换频率",
													SkillCommand: "/redo check",
													RawSQL:       "SELECT group#, bytes/1024/1024 size_mb, status FROM v$log ORDER BY group#",
													Risk: "增大 Redo 占用空间", Rollback: "无"},
											},
										},
										{
											Label: "日志切换正常 — 纯存储延迟问题",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "控制文件读取延迟 > 10ms，非日志切换频繁引起"},
												{Desc: "控制文件所在存储性能不佳"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "迁移控制文件到 SSD/NVMe 存储",
													SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
													RawSQL:       "SELECT name FROM v$controlfile",
													Risk: "需要停库", Rollback: "迁移回原位置"},
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
						{Desc: "control file sequential read 延迟 5-10ms，可接受但有改善空间"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "考虑迁移控制文件到低延迟存储",
							SkillCommand: "/sql \"SELECT name, status FROM v$controlfile\"",
							RawSQL:       "SELECT name, status FROM v$controlfile",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查控制文件读取频率趋势",
							SkillCommand: "/wait trend event='control file sequential read'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='control file sequential read'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 5ms — 延迟正常，等待次数多",
					Match: MatchLT(5),
					Then: &TreeNode{
						Step:  "检查控制文件读取次数是否异常高",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "controlfile_reads_per_sec") },
						Branches: []Branch{
							{
								Label: "reads > 100/s — 异常频繁",
								Match: MatchGT(100),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "控制文件读取延迟正常但每秒 > 100 次，读取频率异常高"},
									{Desc: "可能是监控工具或备份操作频繁查询控制文件"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "定位频繁读取控制文件的会话",
										SkillCommand: "/sql \"SELECT s.sid, s.program, s.event, s.sql_id FROM v$session s WHERE s.event='control file sequential read' AND s.state='WAITING'\"",
										RawSQL:       "SELECT s.sid, s.program, s.event, s.sql_id FROM v$session s WHERE s.event='control file sequential read' AND s.state='WAITING'",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "reads <= 100/s — 正常",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "control file sequential read 延迟和频率均正常"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "持续监控",
										SkillCommand: "/wait trend event='control file sequential read'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='control file sequential read'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE2-008"}, // enq: CF contention
		Tags:     []string{"controlfile", "io", "sequential_read", "storage"},
		Versions: "9i+",
		Related:  []string{"WE2-008", "WE2-012"},
	}
}

// ─── 12. control file parallel write ─────────────────────────────────────────

func ruleControlFileParallelWrite() *Rule {
	return &Rule{
		ID:       "WE2-012",
		Name:     "control file parallel write 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "control file parallel write"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "control file parallel write", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("control file parallel write") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 control file parallel write 平均延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("control file parallel write") },
			Branches: []Branch{
				{
					Label: "avg > 10ms — 控制文件写入延迟高",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查控制文件多重镜像数量",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "controlfile_count") },
						Branches: []Branch{
							{
								Label: "控制文件 > 3 个 — 过多镜像拖慢并行写",
								Match: MatchGT(3),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "control file parallel write > 10ms，控制文件镜像 > 3 个"},
									{Desc: "Oracle 并行写入所有控制文件镜像，最慢的镜像决定整体延迟"},
									{Desc: "过多镜像或镜像分布在不同速度的存储上导致写入慢"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "减少不必要的控制文件镜像（保留 2-3 个）",
										SkillCommand: "/sql \"SELECT name, status, is_recovery_dest_file FROM v$controlfile\"",
										RawSQL:       "SELECT name, status, is_recovery_dest_file FROM v$controlfile",
										Risk: "减少镜像降低冗余", Rollback: "重新添加镜像"},
									{Type: ActionFix, Desc: "确保所有镜像在同速度存储上",
										SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
										RawSQL:       "SELECT name FROM v$controlfile",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "控制文件 <= 3 个 — 存储 I/O 慢",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查日志切换频率（每次切换更新控制文件）",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "log_switches_per_hour") },
									Branches: []Branch{
										{
											Label: "切换 > 6/小时 — 频繁写入加剧延迟",
											Match: MatchGT(6),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "控制文件写入延迟 > 10ms 且日志切换频繁（> 6/小时）"},
												{Desc: "每次日志切换、checkpoint、归档都更新控制文件"},
												{Desc: "频繁写入叠加高延迟严重影响数据库整体性能"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "迁移控制文件到低延迟存储",
													SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
													RawSQL:       "SELECT name FROM v$controlfile",
													Risk: "需要停库", Rollback: "迁移回原位置"},
												{Type: ActionFix, Desc: "增大 Redo Log 减少切换频率",
													SkillCommand: "/redo check",
													RawSQL:       "SELECT group#, bytes/1024/1024 size_mb, status FROM v$log ORDER BY group#",
													Risk: "增大 Redo 占用空间", Rollback: "无"},
											},
										},
										{
											Label: "切换正常 — 纯存储延迟",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "控制文件写入延迟 > 10ms，存储 I/O 需要改善"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "迁移控制文件到 SSD/NVMe",
													SkillCommand: "/sql \"SELECT name FROM v$controlfile\"",
													RawSQL:       "SELECT name FROM v$controlfile",
													Risk: "需要停库", Rollback: "迁移回原位置"},
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
						{Desc: "control file parallel write 延迟 5-10ms，可接受但有改善空间"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控控制文件写入趋势",
							SkillCommand: "/wait trend event='control file parallel write'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='control file parallel write'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 5ms — 正常",
					Match: MatchLT(5),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "control file parallel write < 5ms，控制文件写入性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/wait trend event='control file parallel write'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='control file parallel write'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"WE2-008"}, // enq: CF contention
		Tags:     []string{"controlfile", "io", "parallel_write", "storage", "checkpoint"},
		Versions: "9i+",
		Related:  []string{"WE2-008", "WE2-011"},
	}
}

// ─── 13. cell smart table scan (Exadata) ─────────────────────────────────────

func ruleCellSmartTableScan() *Rule {
	return &Rule{
		ID:       "WE2-013",
		Name:     "cell smart table scan 诊断 (Exadata)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cell smart table scan"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cell smart table scan", Op: OpPctGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 Exadata 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "exadata_cell_count") == 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 cell offload efficiency（Smart Scan 卸载效率）",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "cell_offload_efficiency_pct") },
			Branches: []Branch{
				{
					Label: "offload < 50% — Smart Scan 卸载效率低",
					Match: MatchLT(50),
					Then: &TreeNode{
						Step:  "检查是否存在 storage index 被绕过的情况",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "cell_storage_index_saved_pct") },
						Branches: []Branch{
							{
								Label: "storage index saved < 10% — 存储索引未生效",
								Match: MatchLT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "cell smart table scan 卸载效率 < 50%，Storage Index 几乎未过滤数据"},
									{Desc: "大量数据从 cell 传输回 DB 节点，未发挥 Exadata Smart Scan 优势"},
									{Desc: "可能是查询无法使用 predicate offload（如函数、NLS 不兼容）"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "检查 SQL 的 predicate 是否支持 offload",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST +PREDICATE'))",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 cell offload 统计",
										SkillCommand: "/sql \"SELECT name, value FROM v$mystat NATURAL JOIN v$statname WHERE name LIKE '%cell%offload%' OR name LIKE '%cell%smart%' ORDER BY name\"",
										RawSQL:       "SELECT name, value FROM v$sysstat WHERE name LIKE '%cell%offload%' OR name LIKE '%cell%smart%' ORDER BY name",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "确保表使用 Exadata 友好的存储格式（HCC 压缩）",
										SkillCommand: "/sql \"SELECT table_name, compression, compress_for FROM dba_tables WHERE table_name='{table_name}'\"",
										RawSQL:       "SELECT table_name, compression, compress_for, num_rows, blocks FROM dba_tables WHERE owner NOT IN ('SYS','SYSTEM') AND blocks > 10000 AND (compression='DISABLED' OR compress_for IS NULL) ORDER BY blocks DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "压缩改造需要维护窗口", Rollback: "ALTER TABLE {table_name} NOCOMPRESS"},
								},
							},
							{
								Label: "storage index saved >= 10% — offload 本身效率低",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Storage Index 有一定过滤效果，但整体 offload 效率仍不足"},
									{Desc: "可能是 WHERE 条件中有不支持 offload 的函数"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查热点 SQL 的 offload 支持情况",
										SkillCommand: "/ash top_sql event='cell smart table scan'",
										RawSQL:       "SELECT sql_id, COUNT(*) waits FROM v$active_session_history WHERE event='cell smart table scan' AND sample_time>SYSDATE-1/24 GROUP BY sql_id ORDER BY 2 DESC FETCH FIRST 10 ROWS ONLY",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "改写 SQL 避免不支持 offload 的谓词",
										SkillCommand: "/explain {sql_id}",
										RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
										Risk: "需要修改 SQL", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "offload >= 50% — Smart Scan 正常工作",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查 cell smart scan 延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("cell smart table scan") },
						Branches: []Branch{
							{
								Label: "avg > 10ms — cell 处理慢",
								Match: MatchGT(10),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Smart Scan offload 正常但 cell 处理延迟 > 10ms"},
									{Desc: "Cell 节点磁盘 I/O 负载高或 Flash Cache 未命中"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 cell 节点健康状态",
										SkillCommand: "/sql \"SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util','cpu_util')\"",
										RawSQL:       "SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util','cpu_util')",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "为热点表配置 CELL_FLASH_CACHE KEEP",
										SkillCommand: "/sql \"SELECT segment_name, cell_flash_cache FROM dba_segments WHERE segment_type='TABLE' AND cell_flash_cache='DEFAULT' AND owner NOT IN ('SYS','SYSTEM') ORDER BY blocks DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "ALTER TABLE {hot_table} STORAGE(CELL_FLASH_CACHE KEEP)",
										Risk: "占用 Flash Cache 空间", Rollback: "ALTER TABLE {hot_table} STORAGE(CELL_FLASH_CACHE DEFAULT)"},
								},
							},
							{
								Label: "avg <= 10ms — 性能良好",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "cell smart table scan offload >= 50% 且延迟 <= 10ms，Exadata Smart Scan 工作正常"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "持续监控 Exadata 性能",
										SkillCommand: "/wait trend event='cell smart table scan'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='cell smart table scan'",
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
		Tags:     []string{"exadata", "smart_scan", "offload", "cell", "full_scan"},
		Versions: "11g+ Exadata",
		Related:  []string{"WD019", "WE2-014"},
	}
}

// ─── 14. cell smart index scan (Exadata) ─────────────────────────────────────

func ruleCellSmartIndexScan() *Rule {
	return &Rule{
		ID:       "WE2-014",
		Name:     "cell smart index scan 诊断 (Exadata)",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "cell smart index scan"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "cell smart index scan", Op: OpPctGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "非 Exadata 环境", Check: func(ctx *EvalContext) bool {
					return ctx.GetFloat("metrics", "exadata_cell_count") == 0
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 cell smart index scan 延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("cell smart index scan") },
			Branches: []Branch{
				{
					Label: "avg > 5ms — 索引 Smart Scan 延迟高",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查是否为 Index Fast Full Scan（IFFS）触发的 Smart Scan",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Label: "找到热点 SQL",
								Match: MatchDefault(),
								Then: &TreeNode{
									Step:  "检查 cell offload 效率",
									Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "cell_offload_efficiency_pct") },
									Branches: []Branch{
										{
											Label: "offload < 50% — 索引 offload 效率低",
											Match: MatchLT(50),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "cell smart index scan 延迟 > 5ms 且 offload 效率 < 50%"},
												{Desc: "Index Fast Full Scan 的 Smart Scan 未有效利用 storage index"},
												{Desc: "大量索引数据从 cell 传回 DB 节点"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "检查是否可改为 Index Range Scan 避免 IFFS",
													SkillCommand: "/explain {sql_id}",
													RawSQL:       "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))",
													Risk: "无", Rollback: "无"},
												{Type: ActionInvestigate, Desc: "检查索引统计信息",
													SkillCommand: "/sql \"SELECT index_name, leaf_blocks, distinct_keys, clustering_factor, last_analyzed FROM dba_indexes WHERE table_name='{table_name}'\"",
													RawSQL:       "SELECT index_name, leaf_blocks, distinct_keys, clustering_factor, last_analyzed FROM dba_indexes WHERE table_name='{table_name}'",
													Risk: "无", Rollback: "无"},
											},
										},
										{
											Label: "offload >= 50% — cell I/O 或 Flash 问题",
											Match: MatchDefault(),
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "索引 Smart Scan offload 正常但延迟偏高，cell 层 I/O 需检查"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查 cell 节点 I/O 状态",
													SkillCommand: "/sql \"SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util')\"",
													RawSQL:       "SELECT cell_name, metric_name, metric_value FROM v$cell_global WHERE metric_name IN ('disk_util','flash_util')",
													Risk: "无", Rollback: "无"},
												{Type: ActionFix, Desc: "为热点索引配置 CELL_FLASH_CACHE KEEP",
													SkillCommand: "/sql \"SELECT index_name, table_name, cell_flash_cache FROM dba_indexes WHERE cell_flash_cache='DEFAULT' AND owner NOT IN ('SYS','SYSTEM') ORDER BY leaf_blocks DESC FETCH FIRST 20 ROWS ONLY\"",
													RawSQL:       "ALTER INDEX {index_name} STORAGE(CELL_FLASH_CACHE KEEP)",
													Risk: "占用 Flash Cache 空间", Rollback: "ALTER INDEX {index_name} STORAGE(CELL_FLASH_CACHE DEFAULT)"},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Label: "avg 2-5ms — 中等延迟",
					Match: MatchBetween(2, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "cell smart index scan 延迟 2-5ms，Exadata 环境可接受"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控 smart index scan 趋势",
							SkillCommand: "/wait trend event='cell smart index scan'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='cell smart index scan'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "avg < 2ms — 正常",
					Match: MatchLT(2),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "cell smart index scan < 2ms，Exadata 索引 Smart Scan 性能良好"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/wait trend event='cell smart index scan'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='cell smart index scan'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"exadata", "smart_scan", "index", "iffs", "cell"},
		Versions: "11g+ Exadata",
		Related:  []string{"WD019", "WE2-013"},
	}
}

// ─── 15. os thread startup ───────────────────────────────────────────────────

func ruleOSThreadStartup() *Rule {
	return &Rule{
		ID:       "WE2-015",
		Name:     "os thread startup 诊断",
		Category: "wait_event",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "os thread startup"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "os thread startup", Op: OpPctGT, Value: 2},
			},
			SkipWhen: []SkipCondition{
				{Desc: "pct < 1%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("os thread startup") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查每秒新建进程/线程数",
			Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "process_creates_per_sec") },
			Branches: []Branch{
				{
					Label: "creates > 10/s — 频繁创建进程",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查连接池使用情况",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetFloat("metrics", "session_count") },
						Branches: []Branch{
							{
								Label: "sessions > 500 且频繁创建 — 连接风暴或未使用连接池",
								Match: MatchGT(500),
								Then: &TreeNode{
									Step:  "检查是否使用 DRCP 或中间件连接池",
									Query: QueryDRCPStats,
									Branches: []Branch{
										{
											Label: "DRCP 未启用",
											Match: MatchLTE(0),
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "os thread startup 由频繁创建数据库连接引起（> 10/s）"},
												{Desc: "会话数 > 500 且未使用 DRCP 连接池"},
												{Desc: "每次新连接需要 fork 进程/创建线程，OS 开销显著"},
												{Desc: "大量短连接导致数据库不断创建和销毁进程"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "启用 DRCP（Database Resident Connection Pooling）",
													SkillCommand: "/sql \"SELECT num_cbrok, maxsize, pool_size, num_open_servers, num_busy_servers FROM dba_cpool_info\"",
													RawSQL:       "-- 启用 DRCP\nEXEC DBMS_CONNECTION_POOL.START_POOL;\n-- 配置连接池大小\nEXEC DBMS_CONNECTION_POOL.ALTER_PARAM('', 'MAX_SIZE', '100');",
													Risk: "应用需要使用 :POOLED 连接串", Rollback: "EXEC DBMS_CONNECTION_POOL.STOP_POOL"},
												{Type: ActionFix, Desc: "推动应用端使用连接池",
													SkillCommand: "/sql \"SELECT program, COUNT(*) sessions FROM v$session WHERE type='USER' GROUP BY program ORDER BY 2 DESC FETCH FIRST 20 ROWS ONLY\"",
													RawSQL:       "SELECT program, COUNT(*) sessions FROM v$session WHERE type='USER' GROUP BY program ORDER BY 2 DESC FETCH FIRST 20 ROWS ONLY",
													Risk: "需要修改应用配置", Rollback: "无"},
											},
										},
										{
											Label: "DRCP 已启用但池不足",
											Match: MatchDefault(),
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "DRCP 已启用但仍频繁创建进程，连接池大小不足"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "增大 DRCP 连接池大小",
													SkillCommand: "/sql \"SELECT num_cbrok, maxsize, pool_size, num_open_servers, num_busy_servers FROM dba_cpool_info\"",
													RawSQL:       "EXEC DBMS_CONNECTION_POOL.ALTER_PARAM('', 'MAX_SIZE', '200')",
													Risk: "增加内存使用", Rollback: "EXEC DBMS_CONNECTION_POOL.ALTER_PARAM('', 'MAX_SIZE', '原值')"},
											},
										},
									},
								},
							},
							{
								Label: "sessions <= 500 — 中小规模频繁重连",
								Match: MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "数据库会话不多但频繁创建新连接（> 10/s），典型短连接模式"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用应用端连接池减少频繁创建/销毁",
										SkillCommand: "/sql \"SELECT program, machine, COUNT(*) sessions FROM v$session WHERE type='USER' GROUP BY program, machine ORDER BY 3 DESC FETCH FIRST 20 ROWS ONLY\"",
										RawSQL:       "SELECT program, machine, COUNT(*) sessions FROM v$session WHERE type='USER' GROUP BY program, machine ORDER BY 3 DESC FETCH FIRST 20 ROWS ONLY",
										Risk: "需要修改应用配置", Rollback: "无"},
									{Type: ActionPrevent, Desc: "考虑使用 DRCP",
										SkillCommand: "/sql \"SELECT num_cbrok, maxsize, pool_size FROM dba_cpool_info\"",
										RawSQL:       "SELECT num_cbrok, maxsize, pool_size, num_open_servers, num_busy_servers FROM dba_cpool_info",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "creates 2-10/s — 中等频率",
					Match: MatchBetween(2, 10),
					Then: &TreeNode{
						Step:  "检查 os thread startup 平均延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("os thread startup") },
						Branches: []Branch{
							{
								Label: "avg > 100ms — OS 创建线程慢",
								Match: MatchGT(100),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "os thread startup 平均 > 100ms，操作系统创建线程/进程缓慢"},
									{Desc: "可能是 OS 内存不足、swap 活跃或安全模块（SELinux）开销"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 OS 内存和 swap 使用",
										SkillCommand: "/sql \"SELECT stat_name, value FROM v$osstat WHERE stat_name IN ('FREE_MEMORY_BYTES','PHYSICAL_MEMORY_BYTES','NUM_CPUS')\"",
										RawSQL:       "SELECT stat_name, value FROM v$osstat WHERE stat_name IN ('FREE_MEMORY_BYTES','PHYSICAL_MEMORY_BYTES','NUM_CPUS','NUM_CPU_CORES','LOAD')",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "检查 /etc/security/limits.conf 中 nproc 限制",
										SkillCommand: "/sql \"SELECT resource_name, current_utilization, max_utilization, limit_value FROM v$resource_limit WHERE resource_name='processes'\"",
										RawSQL:       "SELECT resource_name, current_utilization, max_utilization, limit_value FROM v$resource_limit WHERE resource_name='processes'",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label: "avg <= 100ms — 正常范围",
								Match: MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "os thread startup 延迟正常，进程创建频率中等"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控进程创建趋势",
										SkillCommand: "/wait trend event='os thread startup'",
										RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='os thread startup'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "creates < 2/s — 偶发创建",
					Match: MatchLT(2),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "os thread startup 偶发出现，进程创建频率低"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/wait trend event='os thread startup'",
							RawSQL:       "SELECT event, total_waits, ROUND(time_waited_micro/GREATEST(total_waits,1)/1e3,2) avg_ms FROM v$system_event WHERE event='os thread startup'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"os", "thread", "process", "connection_pool", "drcp"},
		Versions: "9i+",
		Related:  []string{},
	}
}
