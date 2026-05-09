/*-------------------------------------------------------------------------
 *
 * rules_extended.go
 *	  MySQL rule engine — extended classification rules (newer than the core set, kept separate to ease review).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/rules_extended.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// extendedRules returns 55 additional MySQL diagnostic rules (MY-026 through MY-080)
// covering InnoDB deep, query optimization, replication, security/config, space/storage,
// wait event analysis, and operational categories.
func extendedRules() []*Rule {
	return []*Rule{
		// InnoDB Deep (10)
		ruleMY026UndoTablespaceBloat(),
		ruleMY027InnoDBLogWaits(),
		ruleMY028BufferPoolWarmup(),
		ruleMY029AutoIncLockContention(),
		ruleMY030InnoDBDiskSort(),
		ruleMY031DataFileFsync(),
		ruleMY032PageSplits(),
		ruleMY033PurgeLag(),
		ruleMY034ChangeBufferMerge(),
		ruleMY035InnoDBAsyncIO(),

		// Query Optimization (10)
		ruleMY036IndexSelectivity(),
		ruleMY037FileSortExcessive(),
		ruleMY038JoinWithoutIndex(),
		ruleMY039RangeScanExcessive(),
		ruleMY040SubqueryInefficient(),
		ruleMY041UnionReplaceOr(),
		ruleMY042LargeTransaction(),
		ruleMY043UnusedIndex(),
		ruleMY044QueryCacheInvalidation(),
		ruleMY045TableCacheInsufficient(),

		// Replication Deep (8)
		ruleMY046ParallelReplicationLag(),
		ruleMY047GTIDConsistency(),
		ruleMY048BinlogFormatIssue(),
		ruleMY049ReplicationDataInconsistency(),
		ruleMY050ReplicationFilter(),
		ruleMY051RelayLogBloat(),
		ruleMY052MultiSourceConflict(),
		ruleMY053SemiSyncTimeout(),

		// Security / Config (7)
		ruleMY054WeakPasswordPolicy(),
		ruleMY055AuditLogDisabled(),
		ruleMY056OverSizedMaxConnections(),
		ruleMY057UnsafeSQLMode(),
		ruleMY058SSLNotEnabled(),
		ruleMY059DefaultStorageEngine(),
		ruleMY060CharacterSetInconsistency(),

		// Space / Storage (5)
		ruleMY061TableFragmentation(),
		ruleMY062BinlogSpaceBloat(),
		ruleMY063TmpdirSpace(),
		ruleMY064RedoLogSizeInadequate(),
		ruleMY065SlowLogBloat(),

		// Wait Event Analysis (10)
		ruleMY066MutexContention(),
		ruleMY067BufferPoolMutex(),
		ruleMY068LogBufferWait(),
		ruleMY069FileIOWait(),
		ruleMY070NetworkWait(),
		ruleMY071TableLockWait(),
		ruleMY072MDLWait(),
		ruleMY073GlobalReadLock(),
		ruleMY074BinlogGroupCommit(),
		ruleMY075AdaptiveFlush(),

		// Operational (5)
		ruleMY076StaleStatistics(),
		ruleMY077OnlineDDLBlocking(),
		ruleMY078LargeTableNoPartition(),
		ruleMY079ForeignKeyOverhead(),
		ruleMY080EventSchedulerIssue(),

		// P1: New session/SQL rules
		ruleMY081IdleConnectionPileup(),
		ruleMY082ActiveThreadsHigh(),
		ruleMY083AbortedClientsSurge(),
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// InnoDB Deep (10): MY-026 ~ MY-035
// ═══════════════════════════════════════════════════════════════════════════════

// MY-026: Undo 表空间膨胀
func ruleMY026UndoTablespaceBloat() *Rule {
	return &Rule{
		ID:       "MY-026",
		Name:     "Undo 表空间膨胀",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "history_list_length"},
			{Type: SignalKeyword, Key: "undo"},
			{Type: SignalKeyword, Key: "purge"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "history_list_length", Op: OpGT, Value: 50000},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 History List Length",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("history_list_length") },
			Branches: []Branch{
				{
					Label: "HLL > 200000 — Undo 严重膨胀",
					Match: MatchGT(200000),
					Then: &TreeNode{
						Step:  "检查是否有长事务阻止 Purge",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
						Branches: []Branch{
							{
								Label:    "存在长事务 — 长事务阻止 Undo Purge",
								Match:    MatchGT(0),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "History List Length > 200000，Undo 表空间严重膨胀"},
									{Desc: "存在长事务阻止 Purge 线程清理旧版本数据"},
									{Desc: "可能导致查询性能下降、磁盘空间耗尽"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找运行时间最长的事务",
										SkillCommand: "/sql \"SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND,trx_started,NOW()) duration_sec, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5\"",
										RawSQL:       "SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND,trx_started,NOW()) duration_sec, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "终止阻塞 Purge 的长事务",
										SkillCommand: "/kill {thread_id}",
										RawSQL:       "KILL {thread_id}",
										Risk: "终止事务会回滚未提交数据", Rollback: "应用重新发起事务"},
								},
							},
							{
								Label:    "无长事务 — Purge 线程性能不足",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "History List Length > 200000，无明显长事务"},
									{Desc: "Purge 线程处理速度跟不上写入速度"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增加 Purge 线程数量",
										SkillCommand: "/sql \"SET GLOBAL innodb_purge_threads=4\"",
										RawSQL:       "SET GLOBAL innodb_purge_threads=4",
										Risk: "需要重启生效（5.7+可动态设置）", Rollback: "SET GLOBAL innodb_purge_threads=1"},
								},
							},
						},
					},
				},
				{
					Label:    "HLL 50000-200000 — Undo 中度膨胀",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "History List Length 50000-200000，Undo 表空间持续增长"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Purge 线程状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 TRANSACTIONS 部分的 Purge 信息",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-015"},
		Tags:     []string{"innodb", "undo", "purge", "history_list"},
		Versions: "5.6+",
	}
}

// MY-027: InnoDB 日志等待
func ruleMY027InnoDBLogWaits() *Rule {
	return &Rule{
		ID:       "MY-027",
		Name:     "InnoDB 日志等待",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_log_waits"},
			{Type: SignalKeyword, Key: "log wait"},
			{Type: SignalKeyword, Key: "redo log"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_log_waits", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 InnoDB Log Waits",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_log_waits") },
			Branches: []Branch{
				{
					Label: "log_waits > 10 — 频繁日志等待",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查 Redo 写入速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
						Branches: []Branch{
							{
								Label:    "redo_rate 高 — 写入量大导致日志缓冲不足",
								Match:    MatchGT(50),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "InnoDB Log Waits > 10/s，日志缓冲区频繁等待刷盘"},
									{Desc: "Redo 写入速率高，innodb_log_buffer_size 不足"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 innodb_log_buffer_size",
										SkillCommand: "/sql \"SET GLOBAL innodb_log_buffer_size=64*1024*1024\"",
										RawSQL:       "SET GLOBAL innodb_log_buffer_size=67108864",
										Risk: "增加内存使用（8.0+可动态设置）", Rollback: "SET GLOBAL innodb_log_buffer_size=16777216"},
									{Type: ActionFix, Desc: "增大 innodb_log_file_size 减少刷盘频率",
										RawSQL: "-- 需要重启\n-- innodb_log_file_size=1G (my.cnf)",
										Risk:   "需要重启 MySQL", Rollback: "恢复原 innodb_log_file_size"},
								},
							},
							{
								Label:    "redo_rate 正常 — 刷盘策略过严格",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "InnoDB Log Waits > 10/s，但 redo 写入速率不高"},
									{Desc: "可能 innodb_flush_log_at_trx_commit=1 导致每次提交都刷盘"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查刷盘策略",
										SkillCommand: "/sql \"SELECT @@innodb_flush_log_at_trx_commit, @@innodb_log_buffer_size\"",
										RawSQL:       "SELECT @@innodb_flush_log_at_trx_commit, @@innodb_log_buffer_size",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "log_waits 1-10 — 偶发日志等待",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "InnoDB Log Waits 偶发，暂不影响性能"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控 innodb_log_waits 趋势",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_log_waits'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_log_waits'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-019"},
		CausesOf: []string{},
		Tags:     []string{"innodb", "log", "redo", "flush"},
		Versions: "5.5+",
	}
}

// MY-028: Buffer Pool 预热不足
func ruleMY028BufferPoolWarmup() *Rule {
	return &Rule{
		ID:       "MY-028",
		Name:     "Buffer Pool 预热不足",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "buffer_pool_hit_pct"},
			{Type: SignalKeyword, Key: "warmup"},
			{Type: SignalKeyword, Key: "预热"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "buffer_pool_hit_pct", Op: OpLT, Value: 90},
			},
			SkipWhen: []SkipCondition{
				{Desc: "命中率 < 95% 已由 MY-013 处理", Check: func(ctx *EvalContext) bool {
					// Only trigger if hit rate is very low (warmup scenario) and MY-013 handles moderate
					return ctx.MetricValue("buffer_pool_hit_pct") >= 80
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Pool 命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
			Branches: []Branch{
				{
					Label: "< 70% — Buffer Pool 严重冷启动",
					Match: MatchLT(70),
					Then: &TreeNode{
						Step:  "检查是否有大量磁盘读取",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_data_pending_reads") },
						Branches: []Branch{
							{
								Label:    "大量 pending reads — 磁盘 IO 压力大",
								Match:    MatchGT(5),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Buffer Pool 命中率 < 70%，大量数据需要从磁盘读取"},
									{Desc: "可能是实例刚重启、Buffer Pool 未预热"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "开启 Buffer Pool 预加载",
										SkillCommand: "/sql \"SET GLOBAL innodb_buffer_pool_dump_at_shutdown=ON; SET GLOBAL innodb_buffer_pool_load_at_startup=ON\"",
										RawSQL:       "SET GLOBAL innodb_buffer_pool_dump_at_shutdown=ON;\nSET GLOBAL innodb_buffer_pool_load_at_startup=ON",
										Risk: "关闭时 dump 会增加少量时间", Rollback: "SET GLOBAL innodb_buffer_pool_dump_at_shutdown=OFF"},
									{Type: ActionFix, Desc: "立即触发 Buffer Pool 加载",
										SkillCommand: "/sql \"SET GLOBAL innodb_buffer_pool_load_now=ON\"",
										RawSQL:       "SET GLOBAL innodb_buffer_pool_load_now=ON",
										Risk: "加载期间增加 IO 负载", Rollback: "SET GLOBAL innodb_buffer_pool_load_abort=ON"},
								},
							},
							{
								Label:    "pending reads 正常 — 数据集超出缓存",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Buffer Pool 命中率低，但磁盘 pending reads 不高"},
									{Desc: "工作集可能超出 Buffer Pool 大小"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "评估是否需要增大 Buffer Pool",
										SkillCommand: "/sql \"SELECT @@innodb_buffer_pool_size/1024/1024/1024 bp_size_gb, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_total') total_pages\"",
										RawSQL:       "SELECT @@innodb_buffer_pool_size/1024/1024/1024 bp_size_gb",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "70-80% — Buffer Pool 命中率偏低",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Pool 命中率 70-80%，预热中或工作集偏大"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 Buffer Pool 状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 BUFFER POOL AND MEMORY 部分",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-013"},
		Tags:     []string{"buffer_pool", "warmup", "memory"},
		Versions: "5.6+",
	}
}

// MY-029: 自增锁争用
func ruleMY029AutoIncLockContention() *Rule {
	return &Rule{
		ID:       "MY-029",
		Name:     "自增锁争用",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "auto_increment"},
			{Type: SignalKeyword, Key: "自增"},
			{Type: SignalKeyword, Key: "autoinc"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_row_lock_waits", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 innodb_autoinc_lock_mode 配置",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label: "检查行锁等待率",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查行锁等待速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_row_lock_waits") },
						Branches: []Branch{
							{
								Label:    "行锁等待高 — 可能存在自增锁争用",
								Match:    MatchGT(50),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "行锁等待 > 50/s，可能存在自增锁争用"},
									{Desc: "innodb_autoinc_lock_mode=0/1 时批量 INSERT 会持有表级自增锁"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将 innodb_autoinc_lock_mode 设为 2（交错模式）",
										RawSQL: "-- my.cnf:\n-- innodb_autoinc_lock_mode=2\n-- 注意：需要 binlog_format=ROW",
										Risk:   "自增值可能不连续（对大多数应用无影响）", Rollback: "恢复 innodb_autoinc_lock_mode=1"},
									{Type: ActionInvestigate, Desc: "检查当前自增锁模式",
										SkillCommand: "/sql \"SELECT @@innodb_autoinc_lock_mode\"",
										RawSQL:       "SELECT @@innodb_autoinc_lock_mode",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "行锁等待中度",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "行锁等待存在，建议检查 innodb_autoinc_lock_mode 配置"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查自增锁模式配置",
										SkillCommand: "/sql \"SELECT @@innodb_autoinc_lock_mode, @@binlog_format\"",
										RawSQL:       "SELECT @@innodb_autoinc_lock_mode, @@binlog_format",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-001"},
		Tags:     []string{"innodb", "autoinc", "lock"},
		Versions: "5.5+",
	}
}

// MY-030: InnoDB 磁盘排序
func ruleMY030InnoDBDiskSort() *Rule {
	return &Rule{
		ID:       "MY-030",
		Name:     "InnoDB 磁盘排序冲高",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "sort_merge_passes"},
			{Type: SignalKeyword, Key: "磁盘排序"},
			{Type: SignalKeyword, Key: "merge sort"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "sort_merge_passes", Op: OpGT, Value: 50},
			},
			SkipWhen: []SkipCondition{
				{Desc: "MY-009 已处理排序溢出", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("sort_merge_passes") > 100
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查磁盘排序与 InnoDB IO 关联",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("sort_merge_passes") },
			Branches: []Branch{
				{
					Label: "sort_merge_passes 高 — InnoDB 相关排序溢出",
					Match: MatchGT(50),
					Then: &TreeNode{
						Step:  "检查 Buffer Pool 命中率是否受排序影响",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
						Branches: []Branch{
							{
								Label:    "命中率低 — 排序导致缓存压力",
								Match:    MatchLT(95),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Sort_merge_passes 高且 Buffer Pool 命中率下降"},
									{Desc: "大量磁盘排序消耗 IO 资源，影响 InnoDB 缓存效率"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 sort_buffer_size 减少磁盘排序",
										SkillCommand: "/sql \"SET GLOBAL sort_buffer_size=4*1024*1024\"",
										RawSQL:       "SET GLOBAL sort_buffer_size=4194304",
										Risk: "每个连接额外分配排序缓存", Rollback: "SET GLOBAL sort_buffer_size=262144"},
									{Type: ActionInvestigate, Desc: "查找排序量最大的 SQL",
										SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SORT_MERGE_PASSES, SUM_SORT_ROWS FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_MERGE_PASSES > 0 ORDER BY SUM_SORT_MERGE_PASSES DESC LIMIT 10\"",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_SORT_MERGE_PASSES, SUM_SORT_ROWS FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_MERGE_PASSES > 0 ORDER BY SUM_SORT_MERGE_PASSES DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "命中率正常 — 排序未影响缓存",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Sort_merge_passes 偏高但 Buffer Pool 命中率正常"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "优化排序相关 SQL，添加合适的索引",
										SkillCommand: "/topsql",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_SORT_MERGE_PASSES FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_MERGE_PASSES > 0 ORDER BY SUM_SORT_MERGE_PASSES DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{"MY-009"},
		Tags:     []string{"innodb", "sort", "disk"},
		Versions: "5.5+",
	}
}

// MY-031: 数据文件扩展 — innodb_data_pending_fsyncs 高
func ruleMY031DataFileFsync() *Rule {
	return &Rule{
		ID:       "MY-031",
		Name:     "InnoDB 数据文件 Fsync 高",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_data_pending_fsyncs"},
			{Type: SignalKeyword, Key: "fsync"},
			{Type: SignalKeyword, Key: "data file"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_data_pending_fsyncs", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 InnoDB Pending Fsyncs",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_data_pending_fsyncs") },
			Branches: []Branch{
				{
					Label: "pending fsyncs > 20 — IO 子系统严重滞后",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step:  "检查 Redo 写入速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
						Branches: []Branch{
							{
								Label:    "redo_rate 高 — 高写入负载导致 IO 滞后",
								Match:    MatchGT(50),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "InnoDB Pending Fsyncs > 20，IO 子系统严重滞后"},
									{Desc: "高 Redo 写入速率导致磁盘 fsync 操作排队"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "检查 innodb_flush_method 配置",
										SkillCommand: "/sql \"SELECT @@innodb_flush_method, @@innodb_io_capacity, @@innodb_io_capacity_max\"",
										RawSQL:       "SELECT @@innodb_flush_method, @@innodb_io_capacity, @@innodb_io_capacity_max",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增大 innodb_io_capacity 匹配磁盘能力",
										SkillCommand: "/sql \"SET GLOBAL innodb_io_capacity=2000; SET GLOBAL innodb_io_capacity_max=4000\"",
										RawSQL:       "SET GLOBAL innodb_io_capacity=2000; SET GLOBAL innodb_io_capacity_max=4000",
										Risk: "增加 IO 操作频率", Rollback: "SET GLOBAL innodb_io_capacity=200; SET GLOBAL innodb_io_capacity_max=2000"},
								},
							},
							{
								Label:    "redo_rate 正常 — 磁盘性能不足",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Pending Fsyncs 高但写入负载不大，磁盘性能可能不足"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查磁盘 IO 延迟",
										SkillCommand: "/sql \"SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 10\"",
										RawSQL:       "SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 10",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "pending fsyncs 5-20 — IO 轻度滞后",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB Pending Fsyncs 5-20，IO 有轻度压力"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 IO 状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_data_pending%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_data_pending%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-019"},
		CausesOf: []string{},
		Tags:     []string{"innodb", "io", "fsync"},
		Versions: "5.5+",
	}
}

// MY-032: 页分裂频繁
func ruleMY032PageSplits() *Rule {
	return &Rule{
		ID:       "MY-032",
		Name:     "InnoDB 页分裂频繁",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_pages_splits"},
			{Type: SignalKeyword, Key: "page split"},
			{Type: SignalKeyword, Key: "页分裂"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_pages_splits", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 InnoDB 页分裂速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_pages_splits") },
			Branches: []Branch{
				{
					Label: "> 1000/s — 严重页分裂",
					Match: MatchGT(1000),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "InnoDB 页分裂 > 1000/s，非顺序 INSERT 导致频繁页分裂"},
						{Desc: "页分裂增加 IO 开销，降低插入性能，并导致碎片化"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否使用了 UUID 或随机主键",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE FROM information_schema.COLUMNS WHERE COLUMN_KEY='PRI' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY TABLE_SCHEMA, TABLE_NAME\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, COLUMN_NAME, COLUMN_TYPE FROM information_schema.COLUMNS WHERE COLUMN_KEY='PRI' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "将 UUID 主键改为有序 UUID 或自增主键",
							RawSQL: "-- 考虑使用 UUID_TO_BIN(UUID(), 1) (8.0+) 生成有序 UUID",
							Risk:   "需要表结构变更", Rollback: "无"},
					},
				},
				{
					Label:    "100-1000/s — 中度页分裂",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB 页分裂 100-1000/s，存在一定的页分裂"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查主键设计和插入模式",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_pages%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_pages%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-061"},
		Tags:     []string{"innodb", "page_split", "fragmentation"},
		Versions: "5.6+",
	}
}

// MY-033: Purge 滞后
func ruleMY033PurgeLag() *Rule {
	return &Rule{
		ID:       "MY-033",
		Name:     "Purge 滞后",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "history_list_length"},
			{Type: SignalKeyword, Key: "purge lag"},
			{Type: SignalKeyword, Key: "purge"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "history_list_length", Op: OpGT, Value: 10000},
			},
			SkipWhen: []SkipCondition{
				{Desc: "HLL > 50000 由 MY-026 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("history_list_length") > 50000
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查 History List Length 增长趋势",
			Check: func(ctx *EvalContext) interface{} {
				m, ok := ctx.GetMetric("history_list_length")
				if !ok {
					return "stable"
				}
				return m.Trend
			},
			Branches: []Branch{
				{
					Label: "持续增长 — Purge 跟不上写入",
					Match: MatchEquals("rising"),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "History List Length 持续增长，Purge 线程跟不上写入速度"},
						{Desc: "可能导致 Undo 表空间膨胀，查询性能下降"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增加 Purge 线程",
							SkillCommand: "/sql \"SET GLOBAL innodb_purge_threads=4\"",
							RawSQL:       "SET GLOBAL innodb_purge_threads=4",
							Risk: "需要重启生效（5.7+可动态设置）", Rollback: "SET GLOBAL innodb_purge_threads=1"},
						{Type: ActionInvestigate, Desc: "检查是否有长事务阻止 Purge",
							SkillCommand: "/sql \"SELECT trx_id, trx_started, trx_mysql_thread_id FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5\"",
							RawSQL:       "SELECT trx_id, trx_started, trx_mysql_thread_id FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "稳定 — Purge 基本跟上",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "History List Length 10000-50000，Purge 基本跟上但有积压"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控 HLL 趋势",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 History list length",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-026"},
		Tags:     []string{"innodb", "purge", "undo"},
		Versions: "5.6+",
	}
}

// MY-034: Change Buffer 合并慢
func ruleMY034ChangeBufferMerge() *Rule {
	return &Rule{
		ID:       "MY-034",
		Name:     "Change Buffer 合并慢",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_ibuf_merges"},
			{Type: SignalKeyword, Key: "change buffer"},
			{Type: SignalKeyword, Key: "ibuf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_ibuf_merges", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Change Buffer 合并速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_ibuf_merges") },
			Branches: []Branch{
				{
					Label: "> 500/s — 大量 Change Buffer 合并",
					Match: MatchGT(500),
					Then: &TreeNode{
						Step:  "检查 Buffer Pool 命中率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
						Branches: []Branch{
							{
								Label:    "命中率低 — 频繁读取触发合并",
								Match:    MatchLT(95),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Change Buffer 合并 > 500/s，Buffer Pool 命中率低"},
									{Desc: "大量读取操作触发 Change Buffer 合并，增加 IO 负担"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 Change Buffer 配置",
										SkillCommand: "/sql \"SELECT @@innodb_change_buffering, @@innodb_change_buffer_max_size\"",
										RawSQL:       "SELECT @@innodb_change_buffering, @@innodb_change_buffer_max_size",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "如果是 SSD 磁盘，考虑关闭 Change Buffer",
										SkillCommand: "/sql \"SET GLOBAL innodb_change_buffering='none'\"",
										RawSQL:       "SET GLOBAL innodb_change_buffering='none'",
										Risk: "关闭后每次辅助索引修改都直接写磁盘", Rollback: "SET GLOBAL innodb_change_buffering='all'"},
								},
							},
							{
								Label:    "命中率正常 — Change Buffer 工作正常",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "Change Buffer 合并量大但 Buffer Pool 命中率正常"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控 Change Buffer 大小趋势",
										SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_ibuf%'\"",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_ibuf%'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "100-500/s — 中度合并",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Change Buffer 合并 100-500/s，属于正常范围"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_ibuf%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_ibuf%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"innodb", "change_buffer", "ibuf"},
		Versions: "5.5+",
	}
}

// MY-035: InnoDB AIO 等待
func ruleMY035InnoDBAsyncIO() *Rule {
	return &Rule{
		ID:       "MY-035",
		Name:     "InnoDB 异步 IO 等待高",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_data_pending_reads"},
			{Type: SignalKeyword, Key: "async io"},
			{Type: SignalKeyword, Key: "aio"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_data_pending_reads", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 InnoDB Pending Reads",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_data_pending_reads") },
			Branches: []Branch{
				{
					Label: "> 50 — 大量异步读等待",
					Match: MatchGT(50),
					Then: &TreeNode{
						Step:  "检查 Buffer Pool 命中率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
						Branches: []Branch{
							{
								Label:    "命中率低 — 大量数据需要从磁盘读取",
								Match:    MatchLT(90),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "InnoDB Pending Reads > 50，Buffer Pool 命中率低"},
									{Desc: "磁盘 IO 子系统严重过载，大量读请求排队"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 Buffer Pool 减少磁盘读",
										RawSQL: "-- my.cnf: innodb_buffer_pool_size = 物理内存的 70-80%",
										Risk:   "增加内存使用", Rollback: "恢复原 innodb_buffer_pool_size"},
									{Type: ActionFix, Desc: "确认 innodb_use_native_aio 已开启",
										SkillCommand: "/sql \"SELECT @@innodb_use_native_aio\"",
										RawSQL:       "SELECT @@innodb_use_native_aio",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "命中率正常 — 短暂 IO 峰值",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "InnoDB Pending Reads > 50，但 Buffer Pool 命中率尚可"},
									{Desc: "可能是突发大量读请求导致的短暂 IO 峰值"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 IO 子系统状态",
										SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_data%'\"",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_data%'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "10-50 — 中度异步 IO 等待",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB Pending Reads 10-50，磁盘 IO 有一定压力"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 IO 延迟",
							SkillCommand: "/sql \"SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 5\"",
							RawSQL:       "SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 5",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-007"},
		CausesOf: []string{},
		Tags:     []string{"innodb", "io", "aio", "pending_reads"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Query Optimization (10): MY-036 ~ MY-045
// ═══════════════════════════════════════════════════════════════════════════════

// MY-036: 索引选择性差
func ruleMY036IndexSelectivity() *Rule {
	return &Rule{
		ID:       "MY-036",
		Name:     "索引选择性差",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_next"},
			{Type: SignalMetric, Key: "handler_read_key"},
			{Type: SignalKeyword, Key: "索引选择性"},
			{Type: SignalKeyword, Key: "index selectivity"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_next", Op: OpGT, Value: 100000},
			},
		},
		Tree: &TreeNode{
			Step: "检查 handler_read_next / handler_read_key 比率",
			Check: func(ctx *EvalContext) interface{} {
				readNext := ctx.MetricValue("handler_read_next")
				readKey := ctx.MetricValue("handler_read_key")
				if readKey < 1 {
					return readNext
				}
				return readNext / readKey
			},
			Branches: []Branch{
				{
					Label: "比率 > 100 — 索引选择性极差",
					Match: MatchGT(100),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "handler_read_next / handler_read_key 比率 > 100"},
						{Desc: "索引选择性差，每次索引查找后需扫描大量相邻行"},
						{Desc: "常见原因：索引列区分度低（如性别、状态等枚举值）"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找使用低选择性索引的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_ROWS_EXAMINED, SUM_ROWS_SENT, SUM_ROWS_EXAMINED/NULLIF(SUM_ROWS_SENT,0) ratio FROM performance_schema.events_statements_summary_by_digest WHERE SUM_ROWS_EXAMINED > 0 ORDER BY SUM_ROWS_EXAMINED/NULLIF(SUM_ROWS_SENT,0) DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_ROWS_EXAMINED, SUM_ROWS_SENT, SUM_ROWS_EXAMINED/NULLIF(SUM_ROWS_SENT,0) ratio FROM performance_schema.events_statements_summary_by_digest WHERE SUM_ROWS_EXAMINED > 0 ORDER BY SUM_ROWS_EXAMINED/NULLIF(SUM_ROWS_SENT,0) DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "使用复合索引提高选择性，或改用覆盖索引",
							RawSQL: "-- 检查索引区分度: SELECT COUNT(DISTINCT col)/COUNT(*) FROM table",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label: "比率 10-100 — 索引选择性偏低",
					Match: MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "handler_read_next / handler_read_key 比率偏高，部分索引选择性不佳"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查索引统计信息",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Handler_read%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Handler_read%'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "比率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "索引使用效率基本正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-007"},
		Tags:     []string{"index", "selectivity", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-037: 文件排序过多
func ruleMY037FileSortExcessive() *Rule {
	return &Rule{
		ID:       "MY-037",
		Name:     "文件排序过多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "sort_rows"},
			{Type: SignalKeyword, Key: "filesort"},
			{Type: SignalKeyword, Key: "文件排序"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "sort_rows", Op: OpGT, Value: 50000},
			},
		},
		Tree: &TreeNode{
			Step: "检查 sort_rows / qps 比率",
			Check: func(ctx *EvalContext) interface{} {
				sortRows := ctx.MetricValue("sort_rows")
				qps := ctx.MetricValue("qps")
				if qps < 1 {
					return sortRows
				}
				return sortRows / qps
			},
			Branches: []Branch{
				{
					Label: "每条查询排序 > 100 行 — 排序量过大",
					Match: MatchGT(100),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "平均每条查询排序行数 > 100，文件排序开销大"},
						{Desc: "大量排序操作消耗 CPU 和临时空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找排序量最大的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SORT_ROWS, COUNT_STAR, SUM_SORT_ROWS/NULLIF(COUNT_STAR,0) avg_sort_rows FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_ROWS > 0 ORDER BY SUM_SORT_ROWS DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_SORT_ROWS, COUNT_STAR, SUM_SORT_ROWS/NULLIF(COUNT_STAR,0) avg_sort_rows FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_ROWS > 0 ORDER BY SUM_SORT_ROWS DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "为 ORDER BY 列添加索引避免 filesort",
							RawSQL: "-- 为 ORDER BY 的列添加合适的复合索引",
							Risk:   "增加索引维护成本", Rollback: "DROP INDEX"},
					},
				},
				{
					Label:    "排序量中等",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "文件排序量偏高，建议优化排序相关 SQL"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看排序状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Sort%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Sort%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-009"},
		Tags:     []string{"sort", "filesort", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-038: 连接查询无索引
func ruleMY038JoinWithoutIndex() *Rule {
	return &Rule{
		ID:       "MY-038",
		Name:     "连接查询无索引",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "select_full_join_rate"},
			{Type: SignalKeyword, Key: "full join"},
			{Type: SignalKeyword, Key: "连接查询"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "select_full_join_rate", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查无索引连接查询速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("select_full_join_rate") },
			Branches: []Branch{
				{
					Label: "> 100/s — 大量无索引连接查询",
					Match: MatchGT(100),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Select_full_join > 100/s，大量 JOIN 操作未使用索引"},
						{Desc: "无索引 JOIN 导致嵌套循环扫描，性能极差"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查找无索引 JOIN 的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SELECT_FULL_JOIN, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_JOIN > 0 ORDER BY SUM_SELECT_FULL_JOIN DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_SELECT_FULL_JOIN, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_JOIN > 0 ORDER BY SUM_SELECT_FULL_JOIN DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "为 JOIN 条件列添加索引",
							RawSQL: "-- 为 JOIN ON 条件中的列添加索引\n-- ALTER TABLE t ADD INDEX idx_join_col (col)",
							Risk:   "DDL 操作期间可能锁表", Rollback: "DROP INDEX"},
					},
				},
				{
					Label:    "10-100/s — 中度无索引 JOIN",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Select_full_join 10-100/s，部分 JOIN 未使用索引"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查无索引 JOIN 的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SELECT_FULL_JOIN FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_JOIN > 0 ORDER BY SUM_SELECT_FULL_JOIN DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_SELECT_FULL_JOIN FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_JOIN > 0 ORDER BY SUM_SELECT_FULL_JOIN DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-007"},
		Tags:     []string{"join", "index", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-039: 范围扫描过多
func ruleMY039RangeScanExcessive() *Rule {
	return &Rule{
		ID:       "MY-039",
		Name:     "范围扫描过多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_rnd_next"},
			{Type: SignalKeyword, Key: "range scan"},
			{Type: SignalKeyword, Key: "范围扫描"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_rnd_next", Op: OpGT, Value: 500000},
			},
			SkipWhen: []SkipCondition{
				{Desc: "全表扫描由 MY-007 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("handler_read_rnd_next") > 1000000
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查随机读取速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("handler_read_rnd_next") },
			Branches: []Branch{
				{
					Label: "500K-1M/s — 大量范围扫描",
					Match: MatchGT(500000),
					Then: &TreeNode{
						Step:  "检查 TPS 是否正常",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tps") },
						Branches: []Branch{
							{
								Label:    "TPS 低 — 查询效率差",
								Match:    MatchLT(100),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Handler_read_rnd_next 高但 TPS 低，查询效率差"},
									{Desc: "大量行扫描但产出少，索引可能不适合当前查询模式"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查找扫描行数最多的 SQL",
										SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_ROWS_EXAMINED, SUM_ROWS_SENT FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_ROWS_EXAMINED DESC LIMIT 10\"",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_ROWS_EXAMINED, SUM_ROWS_SENT FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_ROWS_EXAMINED DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "TPS 正常 — 业务特性导致",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "范围扫描量大但 TPS 正常，可能是业务特性"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控扫描行数趋势",
										SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Handler_read%'\"",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Handler_read%'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-007"},
		Tags:     []string{"range_scan", "handler", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-040: 子查询效率低
func ruleMY040SubqueryInefficient() *Rule {
	return &Rule{
		ID:       "MY-040",
		Name:     "子查询效率低",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "select_full_range_join"},
			{Type: SignalKeyword, Key: "subquery"},
			{Type: SignalKeyword, Key: "子查询"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "select_full_range_join", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查全范围连接速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("select_full_range_join") },
			Branches: []Branch{
				{
					Label: "> 50/s — 大量低效子查询/范围连接",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Select_full_range_join > 50/s，大量子查询或范围 JOIN 效率低"},
						{Desc: "MySQL 5.6 以下版本子查询优化器较弱，可能导致全表扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找低效子查询",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SELECT_FULL_RANGE_JOIN, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_RANGE_JOIN > 0 ORDER BY SUM_SELECT_FULL_RANGE_JOIN DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_SELECT_FULL_RANGE_JOIN, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SELECT_FULL_RANGE_JOIN > 0 ORDER BY SUM_SELECT_FULL_RANGE_JOIN DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "将子查询改写为 JOIN 或使用 EXISTS",
							RawSQL: "-- 将 IN (SELECT ...) 改写为 JOIN 或 EXISTS",
							Risk:   "需要验证改写后语义一致", Rollback: "恢复原 SQL"},
					},
				},
				{
					Label:    "10-50/s — 中度低效查询",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Select_full_range_join 10-50/s，部分查询效率不佳"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查查询执行计划",
							SkillCommand: "/topsql",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Select_full_range_join'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006"},
		Tags:     []string{"subquery", "range_join", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-041: UNION 替代 OR 优化
func ruleMY041UnionReplaceOr() *Rule {
	return &Rule{
		ID:       "MY-041",
		Name:     "OR 条件导致索引失效",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_rnd_next"},
			{Type: SignalKeyword, Key: "union"},
			{Type: SignalKeyword, Key: "index merge"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_rnd_next", Op: OpGT, Value: 100000},
			},
		},
		Tree: &TreeNode{
			Step: "检查 handler_read_rnd_next 和全表扫描比率",
			Check: func(ctx *EvalContext) interface{} {
				rndNext := ctx.MetricValue("handler_read_rnd_next")
				readKey := ctx.MetricValue("handler_read_key")
				if readKey < 1 {
					return rndNext
				}
				return rndNext / readKey
			},
			Branches: []Branch{
				{
					Label: "比率高 — 可能 OR 条件导致索引失效",
					Match: MatchGT(50),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "大量随机读取，可能存在 OR 条件导致索引失效的 SQL"},
						{Desc: "MySQL 在 OR 条件跨列时可能放弃索引使用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找全表扫描的 SQL 检查是否有 OR 条件",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "将 OR 改写为 UNION ALL",
							RawSQL: "-- 将 SELECT ... WHERE a=1 OR b=2 改写为\n-- SELECT ... WHERE a=1 UNION ALL SELECT ... WHERE b=2 AND a<>1",
							Risk:   "需验证语义一致", Rollback: "恢复原 SQL"},
					},
				},
				{
					Label:    "比率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "索引使用情况基本正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"or_condition", "union", "index", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-042: 大事务
func ruleMY042LargeTransaction() *Rule {
	return &Rule{
		ID:       "MY-042",
		Name:     "大事务持锁时间长",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "innodb_row_lock_waits"},
			{Type: SignalKeyword, Key: "大事务"},
			{Type: SignalKeyword, Key: "large transaction"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有阻塞链",
			Check: func(ctx *EvalContext) interface{} { return ctx.HasBlockingChains() },
			Branches: []Branch{
				{
					Label: "存在阻塞链 — 大事务/长事务持锁阻塞",
					Match: MatchBool(true),
					Then: &TreeNode{
						Step:  "检查行锁等待速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_row_lock_waits") },
						Branches: []Branch{
							{
								Label:    "行锁等待高 — blocker 持有大量行锁",
								Match:    MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "存在阻塞链且行锁等待高，root blocker 持锁不释放"},
									{Desc: "大事务或未提交事务阻塞其他会话"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位 root blocker 并查看其 SQL",
										RawSQL: "SELECT b.BLOCKING_THREAD_ID, t.PROCESSLIST_USER, t.PROCESSLIST_TIME, LEFT(t.PROCESSLIST_INFO, 200) AS blocker_sql, COUNT(*) AS victim_count FROM performance_schema.data_lock_waits b JOIN performance_schema.threads t ON t.THREAD_ID = b.BLOCKING_THREAD_ID GROUP BY b.BLOCKING_THREAD_ID ORDER BY victim_count DESC LIMIT 5",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "查找运行时间最长的事务",
										RawSQL: "SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND,trx_started,NOW()) duration_sec, trx_rows_locked, trx_rows_modified, trx_mysql_thread_id FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "KILL 阻塞源会话或等待其事务提交",
										RawSQL: "-- KILL {blocker_thread_id}",
										Risk:   "KILL 会导致事务回滚", Rollback: "无"},
								},
							},
							{
								Label:    "行锁等待低 — blocker 可能在等 IO 或空闲未提交",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在阻塞链但行锁争用速率不高，blocker 可能是空闲未提交事务"},
									{Desc: "需定位 root blocker 状态（Sleep/IO等待/执行中）"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位 root blocker 及其状态",
										RawSQL: "SELECT b.BLOCKING_THREAD_ID, t.PROCESSLIST_USER, t.PROCESSLIST_COMMAND, t.PROCESSLIST_STATE, t.PROCESSLIST_TIME, LEFT(t.PROCESSLIST_INFO, 200) AS sql_text, COUNT(*) AS victims FROM performance_schema.data_lock_waits b JOIN performance_schema.threads t ON t.THREAD_ID = b.BLOCKING_THREAD_ID GROUP BY b.BLOCKING_THREAD_ID ORDER BY victims DESC LIMIT 5",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 InnoDB 锁状态",
										RawSQL: "SHOW ENGINE INNODB STATUS",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "无阻塞链 — 检查 TPS",
					Match: MatchBool(false),
					Then: &TreeNode{
						Step:  "检查 TPS 是否异常低",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tps") },
						Branches: []Branch{
							{
								Label:    "TPS 低 — 大事务或批量操作占锁",
								Match:    MatchLT(50),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "锁等待存在且 TPS 低，可能有大事务在执行"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查找运行时间最长的事务",
										RawSQL: "SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND,trx_started,NOW()) duration_sec, trx_rows_locked, trx_mysql_thread_id FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "TPS 正常 — 轻度锁争用",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "存在轻度锁等待但 TPS 未受影响"},
								},
								Actions: []Action{},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-001", "MY-004"},
		Tags:     []string{"transaction", "large_trx", "lock", "blocker"},
		Versions: "5.5+",
	}
}

// MY-043: 未使用索引
func ruleMY043UnusedIndex() *Rule {
	return &Rule{
		ID:       "MY-043",
		Name:     "未使用索引的查询多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_rnd"},
			{Type: SignalMetric, Key: "handler_read_first"},
			{Type: SignalKeyword, Key: "unused index"},
			{Type: SignalKeyword, Key: "未使用索引"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_rnd", Op: OpGT, Value: 10000},
			},
		},
		Tree: &TreeNode{
			Step:  "检查随机读和首行读比率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("handler_read_rnd") },
			Branches: []Branch{
				{
					Label: "> 100000/s — 大量随机读",
					Match: MatchGT(100000),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Handler_read_rnd > 100000/s，大量查询未有效使用索引"},
						{Desc: "随机读表示 MySQL 在排序后读取行，通常意味着 filesort"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找未使用索引的查询",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, SUM_NO_GOOD_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 OR SUM_NO_GOOD_INDEX_USED > 0 ORDER BY SUM_NO_INDEX_USED DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, SUM_NO_GOOD_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 OR SUM_NO_GOOD_INDEX_USED > 0 ORDER BY SUM_NO_INDEX_USED DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "为频繁查询添加合适的索引",
							SkillCommand: "/explain {digest}",
							RawSQL:       "EXPLAIN FORMAT=TREE {sql_text}",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "10K-100K/s — 中度随机读",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Handler_read_rnd 偏高，部分查询可能缺少索引"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 handler 读取统计",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Handler_read%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Handler_read%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-007"},
		Tags:     []string{"index", "handler_read", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-044: 查询缓存失效频繁 (5.7)
func ruleMY044QueryCacheInvalidation() *Rule {
	return &Rule{
		ID:       "MY-044",
		Name:     "查询缓存失效频繁",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "qcache_lowmem_prunes"},
			{Type: SignalKeyword, Key: "query cache"},
			{Type: SignalKeyword, Key: "查询缓存"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "qcache_lowmem_prunes", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查查询缓存淘汰速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("qcache_lowmem_prunes") },
			Branches: []Branch{
				{
					Label: "> 1000/s — 频繁查询缓存淘汰",
					Match: MatchGT(1000),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Qcache_lowmem_prunes > 1000/s，查询缓存频繁因内存不足淘汰"},
						{Desc: "查询缓存在高写入场景下反而降低性能（mutex 争用）"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "关闭查询缓存（5.7 推荐，8.0 已移除）",
							SkillCommand: "/sql \"SET GLOBAL query_cache_type=OFF; SET GLOBAL query_cache_size=0\"",
							RawSQL:       "SET GLOBAL query_cache_type=OFF; SET GLOBAL query_cache_size=0",
							Risk: "关闭后查询不再缓存结果", Rollback: "SET GLOBAL query_cache_type=ON; SET GLOBAL query_cache_size=原值"},
					},
				},
				{
					Label:    "100-1000/s — 中度缓存淘汰",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "查询缓存淘汰速率偏高，建议评估是否关闭查询缓存"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查查询缓存状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Qcache%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Qcache%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"query_cache", "memory", "5.7"},
		Versions: "5.7",
	}
}

// MY-045: 表缓存不足
func ruleMY045TableCacheInsufficient() *Rule {
	return &Rule{
		ID:       "MY-045",
		Name:     "表缓存不足",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "opened_tables"},
			{Type: SignalKeyword, Key: "table cache"},
			{Type: SignalKeyword, Key: "表缓存"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "opened_tables", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Opened_tables 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("opened_tables") },
			Branches: []Branch{
				{
					Label: "> 500/s — 表缓存严重不足",
					Match: MatchGT(500),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Opened_tables > 500/s，频繁打开表文件"},
						{Desc: "table_open_cache 不足导致频繁关闭和重新打开表"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 table_open_cache",
							SkillCommand: "/sql \"SET GLOBAL table_open_cache=4000\"",
							RawSQL:       "SET GLOBAL table_open_cache=4000",
							Risk: "增加文件描述符使用", Rollback: "SET GLOBAL table_open_cache=2000"},
						{Type: ActionInvestigate, Desc: "检查当前表缓存配置",
							SkillCommand: "/sql \"SELECT @@table_open_cache, @@table_open_cache_instances\"",
							RawSQL:       "SELECT @@table_open_cache, @@table_open_cache_instances",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "100-500/s — 表缓存偏小",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Opened_tables 100-500/s，表缓存可能不足"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查表缓存命中率",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Open%tables%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Open%tables%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"table_cache", "opened_tables", "sql_perf"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Replication Deep (8): MY-046 ~ MY-053
// ═══════════════════════════════════════════════════════════════════════════════

// MY-046: 并行复制延迟
func ruleMY046ParallelReplicationLag() *Rule {
	return &Rule{
		ID:       "MY-046",
		Name:     "并行复制延迟",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag"},
			{Type: SignalKeyword, Key: "parallel replication"},
			{Type: SignalKeyword, Key: "并行复制"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag", Op: OpGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "复制延迟由 MY-016 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("replication_lag") > 60
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟程度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "延迟 10-60s — 并行复制配置可能不当",
					Match: MatchBetween(10, 60),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "复制延迟 10-60s，可能并行复制配置不当"},
						{Desc: "slave_parallel_workers 过少或 slave_parallel_type 配置不当"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查并行复制配置",
							SkillCommand: "/sql \"SELECT @@slave_parallel_workers, @@slave_parallel_type, @@slave_preserve_commit_order\"",
							RawSQL:       "SELECT @@slave_parallel_workers, @@slave_parallel_type, @@slave_preserve_commit_order",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "优化并行复制配置",
							RawSQL: "-- STOP SLAVE;\n-- SET GLOBAL slave_parallel_workers=8;\n-- SET GLOBAL slave_parallel_type='LOGICAL_CLOCK';\n-- START SLAVE;",
							Risk:   "需要停止复制线程", Rollback: "恢复原并行复制配置"},
					},
				},
				{
					Label:    "延迟 < 10s — 轻度延迟",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "复制延迟 < 10s，建议检查并行复制配置"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查复制状态",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-016"},
		Tags:     []string{"replication", "parallel", "lag"},
		Versions: "5.7+",
	}
}

// MY-047: GTID 一致性问题
func ruleMY047GTIDConsistency() *Rule {
	return &Rule{
		ID:       "MY-047",
		Name:     "GTID 一致性问题",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "gtid"},
			{Type: SignalKeyword, Key: "GTID"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 GTID 模式",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label: "检查 GTID 配置",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查复制延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
						Branches: []Branch{
							{
								Label:    "有复制延迟 — GTID 不一致风险",
								Match:    MatchGT(5),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在复制延迟，主从 GTID 集合不一致"},
									{Desc: "GTID 不一致可能导致主从切换失败"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 GTID 状态",
										SkillCommand: "/sql \"SELECT @@gtid_mode, @@enforce_gtid_consistency, @@gtid_executed\"",
										RawSQL:       "SELECT @@gtid_mode, @@enforce_gtid_consistency, @@gtid_executed",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "对比主从 GTID 差异",
										SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
										RawSQL:       "SHOW SLAVE STATUS -- 对比 Retrieved_Gtid_Set 和 Executed_Gtid_Set",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "无复制延迟 — GTID 基本一致",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "复制正常，GTID 状态基本一致"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "定期检查 GTID 一致性",
										SkillCommand: "/sql \"SELECT @@gtid_mode, @@enforce_gtid_consistency\"",
										RawSQL:       "SELECT @@gtid_mode, @@enforce_gtid_consistency",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"MY-016"},
		CausesOf: []string{},
		Tags:     []string{"replication", "gtid"},
		Versions: "5.6+",
	}
}

// MY-048: Binlog 格式问题
func ruleMY048BinlogFormatIssue() *Rule {
	return &Rule{
		ID:       "MY-048",
		Name:     "Binlog 格式不当",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "binlog_format"},
			{Type: SignalKeyword, Key: "binlog format"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 Binlog 格式和复制状态",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label: "检查复制延迟关联",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查复制延迟",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
						Branches: []Branch{
							{
								Label:    "有延迟 — binlog 格式可能影响复制性能",
								Match:    MatchGT(5),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在复制延迟，binlog_format 可能配置不当"},
									{Desc: "STATEMENT 格式可能导致主从数据不一致，MIXED 是推荐选择"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 binlog 格式",
										SkillCommand: "/sql \"SELECT @@binlog_format, @@binlog_row_image\"",
										RawSQL:       "SELECT @@binlog_format, @@binlog_row_image",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "切换为 ROW 格式（推荐）",
										SkillCommand: "/sql \"SET GLOBAL binlog_format='ROW'\"",
										RawSQL:       "SET GLOBAL binlog_format='ROW'",
										Risk: "ROW 格式 binlog 体积更大", Rollback: "SET GLOBAL binlog_format='MIXED'"},
								},
							},
							{
								Label:    "无延迟 — 格式暂无影响",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "复制正常，binlog 格式暂无性能影响"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "建议使用 ROW 格式以确保数据一致性",
										SkillCommand: "/sql \"SELECT @@binlog_format\"",
										RawSQL:       "SELECT @@binlog_format",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-049"},
		Tags:     []string{"replication", "binlog", "format"},
		Versions: "5.5+",
	}
}

// MY-049: 主从数据不一致
func ruleMY049ReplicationDataInconsistency() *Rule {
	return &Rule{
		ID:       "MY-049",
		Name:     "主从数据不一致风险",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag"},
			{Type: SignalKeyword, Key: "数据不一致"},
			{Type: SignalKeyword, Key: "checksum"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查复制状态和延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "有延迟 — 数据不一致风险高",
					Match: MatchGT(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在复制延迟，主从数据可能不一致"},
						{Desc: "建议使用 pt-table-checksum 工具验证数据一致性"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "使用 pt-table-checksum 校验数据一致性",
							RawSQL: "-- pt-table-checksum --host=master --databases=db1,db2",
							Risk:   "校验过程会增加主库负载", Rollback: "无"},
						{Type: ActionFix, Desc: "如发现不一致使用 pt-table-sync 修复",
							RawSQL: "-- pt-table-sync --execute --databases=db1 h=master,D=db1 h=slave",
							Risk:   "修复过程会修改从库数据", Rollback: "从全量备份恢复"},
					},
				},
				{
					Label:    "无延迟 — 风险较低",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "复制无延迟，数据一致性风险较低"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期校验主从数据一致性",
							RawSQL: "-- 建议每周执行 pt-table-checksum 校验",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-048"},
		CausesOf: []string{},
		Tags:     []string{"replication", "consistency", "checksum"},
		Versions: "5.5+",
	}
}

// MY-050: 复制过滤器问题
func ruleMY050ReplicationFilter() *Rule {
	return &Rule{
		ID:       "MY-050",
		Name:     "复制过滤器配置风险",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "replicate_do"},
			{Type: SignalKeyword, Key: "replicate_ignore"},
			{Type: SignalKeyword, Key: "复制过滤"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "有延迟 — 复制过滤器可能导致问题",
					Match: MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在复制延迟，复制过滤器可能导致跨库操作被过滤"},
						{Desc: "replicate_do_db 只过滤默认库匹配的事件，跨库 SQL 可能被错误忽略"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查复制过滤器配置",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS -- 检查 Replicate_Do_DB, Replicate_Ignore_DB 等字段",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "建议使用 replicate_wild_do_table 替代 replicate_do_db",
							RawSQL: "-- STOP SLAVE;\n-- CHANGE REPLICATION FILTER REPLICATE_WILD_DO_TABLE=('db1.%');\n-- START SLAVE;",
							Risk:   "需要停止复制线程", Rollback: "恢复原过滤器配置"},
					},
				},
				{
					Label:    "无延迟 — 过滤器暂时正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "复制正常，但建议检查过滤器配置是否合理"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "审查复制过滤器配置",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-049"},
		Tags:     []string{"replication", "filter", "config"},
		Versions: "5.5+",
	}
}

// MY-051: 中继日志膨胀
func ruleMY051RelayLogBloat() *Rule {
	return &Rule{
		ID:       "MY-051",
		Name:     "中继日志膨胀",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag"},
			{Type: SignalKeyword, Key: "relay log"},
			{Type: SignalKeyword, Key: "中继日志"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag", Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟程度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "延迟 > 300s — 中继日志可能大量积压",
					Match: MatchGT(300),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "复制延迟 > 300s，中继日志大量积压"},
						{Desc: "中继日志膨胀可能占用大量磁盘空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查中继日志空间使用",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS -- 检查 Relay_Log_Space",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "限制中继日志空间",
							SkillCommand: "/sql \"SET GLOBAL relay_log_space_limit=16*1024*1024*1024\"",
							RawSQL:       "SET GLOBAL relay_log_space_limit=17179869184",
							Risk: "限制过小可能导致复制停止", Rollback: "SET GLOBAL relay_log_space_limit=0"},
					},
				},
				{
					Label:    "延迟 30-300s — 轻度积压",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "复制延迟 30-300s，中继日志有一定积压"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查中继日志状态",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-016"},
		CausesOf: []string{},
		Tags:     []string{"replication", "relay_log", "space"},
		Versions: "5.5+",
	}
}

// MY-052: 多源复制冲突
func ruleMY052MultiSourceConflict() *Rule {
	return &Rule{
		ID:       "MY-052",
		Name:     "多源复制冲突",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "multi-source"},
			{Type: SignalKeyword, Key: "多源复制"},
			{Type: SignalKeyword, Key: "channel"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "有延迟 — 多源复制可能有冲突",
					Match: MatchGT(5),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "多源复制场景下存在延迟，可能有 channel 间冲突"},
						{Desc: "不同 channel 写入相同表可能导致复制错误"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查各 channel 状态",
							SkillCommand: "/sql \"SELECT CHANNEL_NAME, SERVICE_STATE FROM performance_schema.replication_connection_status\"",
							RawSQL:       "SELECT CHANNEL_NAME, SERVICE_STATE FROM performance_schema.replication_connection_status",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查各 channel 应用延迟",
							SkillCommand: "/sql \"SELECT CHANNEL_NAME, LAST_APPLIED_TRANSACTION_END_APPLY_TIMESTAMP FROM performance_schema.replication_applier_status_by_worker\"",
							RawSQL:       "SELECT CHANNEL_NAME, LAST_APPLIED_TRANSACTION_END_APPLY_TIMESTAMP FROM performance_schema.replication_applier_status_by_worker",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "无明显延迟",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "多源复制各 channel 运行正常"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期检查 channel 状态",
							RawSQL: "SELECT * FROM performance_schema.replication_connection_status",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-016"},
		Tags:     []string{"replication", "multi_source", "channel"},
		Versions: "5.7+",
	}
}

// MY-053: 半同步超时
func ruleMY053SemiSyncTimeout() *Rule {
	return &Rule{
		ID:       "MY-053",
		Name:     "半同步复制超时",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag"},
			{Type: SignalKeyword, Key: "semi-sync"},
			{Type: SignalKeyword, Key: "半同步"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "延迟 > 1s — 半同步可能已降级",
					Match: MatchGT(1),
					Then: &TreeNode{
						Step:  "检查 TPS 是否受影响",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tps") },
						Branches: []Branch{
							{
								Label:    "TPS 低 — 半同步等待影响写入",
								Match:    MatchLT(100),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "复制延迟且 TPS 低，半同步复制可能在等待 ACK"},
									{Desc: "半同步超时后会降级为异步，数据安全性降低"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查半同步状态",
										SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Rpl_semi_sync%'\"",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Rpl_semi_sync%'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "调整半同步超时时间",
										SkillCommand: "/sql \"SET GLOBAL rpl_semi_sync_master_timeout=3000\"",
										RawSQL:       "SET GLOBAL rpl_semi_sync_master_timeout=3000",
										Risk: "超时过短可能频繁降级", Rollback: "SET GLOBAL rpl_semi_sync_master_timeout=10000"},
								},
							},
							{
								Label:    "TPS 正常 — 半同步已降级为异步",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "TPS 正常但有延迟，半同步可能已降级为异步"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查半同步是否已降级",
										SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Rpl_semi_sync_master_status'\"",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Rpl_semi_sync_master_status'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "无明显延迟",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "半同步复制工作正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-016"},
		Tags:     []string{"replication", "semi_sync", "timeout"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Security / Config (7): MY-054 ~ MY-060
// ═══════════════════════════════════════════════════════════════════════════════

// MY-054: 弱密码策略
func ruleMY054WeakPasswordPolicy() *Rule {
	return &Rule{
		ID:       "MY-054",
		Name:     "弱密码策略",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "password policy"},
			{Type: SignalKeyword, Key: "密码策略"},
			{Type: SignalKeyword, Key: "validate_password"},
			{Type: SignalCategory, Key: "security"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 validate_password 插件",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label: "检查连接数作为代理指标",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查连接使用率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
						Branches: []Branch{
							{
								Label:    "连接多 — 更需要强密码策略",
								Match:    MatchGT(50),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "连接数较多，弱密码策略增加安全风险"},
									{Desc: "建议开启 validate_password 插件强制密码复杂度"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查密码策略配置",
										SkillCommand: "/sql \"SHOW VARIABLES LIKE 'validate_password%'\"",
										RawSQL:       "SHOW VARIABLES LIKE 'validate_password%'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "安装 validate_password 插件",
										SkillCommand: "/sql \"INSTALL PLUGIN validate_password SONAME 'validate_password.so'\"",
										RawSQL:       "INSTALL PLUGIN validate_password SONAME 'validate_password.so'",
										Risk: "安装后现有弱密码用户不受影响", Rollback: "UNINSTALL PLUGIN validate_password"},
								},
							},
							{
								Label:    "连接少 — 风险较低",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "建议开启密码复杂度验证"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "检查密码策略",
										SkillCommand: "/sql \"SHOW VARIABLES LIKE 'validate_password%'\"",
										RawSQL:       "SHOW VARIABLES LIKE 'validate_password%'",
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
		Tags:     []string{"security", "password", "config"},
		Versions: "5.6+",
	}
}

// MY-055: 未开启审计
func ruleMY055AuditLogDisabled() *Rule {
	return &Rule{
		ID:       "MY-055",
		Name:     "审计日志未开启",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "audit"},
			{Type: SignalKeyword, Key: "审计"},
			{Type: SignalCategory, Key: "security"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查审计相关信息",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label:    "检查审计配置",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "审计日志可能未开启，无法追踪数据库操作记录"},
						{Desc: "生产环境建议开启审计日志以满足合规要求"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查审计插件状态",
							SkillCommand: "/sql \"SELECT PLUGIN_NAME, PLUGIN_STATUS FROM INFORMATION_SCHEMA.PLUGINS WHERE PLUGIN_NAME LIKE '%audit%'\"",
							RawSQL:       "SELECT PLUGIN_NAME, PLUGIN_STATUS FROM INFORMATION_SCHEMA.PLUGINS WHERE PLUGIN_NAME LIKE '%audit%'",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "开启通用查询日志作为简单审计（非生产环境）",
							SkillCommand: "/sql \"SET GLOBAL general_log=ON\"",
							RawSQL:       "SET GLOBAL general_log=ON",
							Risk: "general_log 会记录所有 SQL，性能开销大", Rollback: "SET GLOBAL general_log=OFF"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "audit", "compliance"},
		Versions: "5.5+",
	}
}

// MY-056: 过大的 max_connections
func ruleMY056OverSizedMaxConnections() *Rule {
	return &Rule{
		ID:       "MY-056",
		Name:     "max_connections 配置过大",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalKeyword, Key: "max_connections"},
			{Type: SignalCategory, Key: "session"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpLT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查连接使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label: "< 5% — max_connections 严重过大",
					Match: MatchLT(5),
					Then: &TreeNode{
						Step:  "检查 threads_running",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("threads_running") },
						Branches: []Branch{
							{
								Label:    "活跃线程少 — 连接数配置远超实际需求",
								Match:    MatchLT(10),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "连接使用率 < 5% 且活跃线程 < 10，max_connections 远超实际需求"},
									{Desc: "过大的 max_connections 浪费内存（每个连接预分配 sort_buffer、read_buffer 等）"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "调低 max_connections 为实际峰值的 2 倍",
										SkillCommand: "/sql \"SELECT @@max_connections, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Max_used_connections') max_used\"",
										RawSQL:       "SELECT @@max_connections, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Max_used_connections') max_used",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "活跃线程正常",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "连接使用率低，但活跃线程正常，可能是连接池配置"},
								},
								Actions: []Action{},
							},
						},
					},
				},
				{
					Label:    "5-10% — 连接使用率偏低",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "连接使用率偏低，建议适当调低 max_connections"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "检查历史最大连接数",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Max_used_connections'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Max_used_connections'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"config", "max_connections", "memory"},
		Versions: "5.5+",
	}
}

// MY-057: 不安全的 SQL 模式
func ruleMY057UnsafeSQLMode() *Rule {
	return &Rule{
		ID:       "MY-057",
		Name:     "SQL 模式过于宽松",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "sql_mode"},
			{Type: SignalKeyword, Key: "sql mode"},
			{Type: SignalCategory, Key: "security"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 SQL 模式",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label:    "检查 sql_mode 配置",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "sql_mode 可能过于宽松，允许不安全的 SQL 行为"},
						{Desc: "建议至少包含 STRICT_TRANS_TABLES, NO_ENGINE_SUBSTITUTION"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 sql_mode",
							SkillCommand: "/sql \"SELECT @@sql_mode\"",
							RawSQL:       "SELECT @@sql_mode",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "设置推荐的 sql_mode",
							SkillCommand: "/sql \"SET GLOBAL sql_mode='ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'\"",
							RawSQL:       "SET GLOBAL sql_mode='ONLY_FULL_GROUP_BY,STRICT_TRANS_TABLES,NO_ZERO_IN_DATE,NO_ZERO_DATE,ERROR_FOR_DIVISION_BY_ZERO,NO_ENGINE_SUBSTITUTION'",
							Risk: "严格模式可能导致现有不规范 SQL 报错", Rollback: "SET GLOBAL sql_mode='原值'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "sql_mode", "config"},
		Versions: "5.5+",
	}
}

// MY-058: 未开启 SSL
func ruleMY058SSLNotEnabled() *Rule {
	return &Rule{
		ID:       "MY-058",
		Name:     "未开启 SSL 加密连接",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "ssl"},
			{Type: SignalKeyword, Key: "secure_transport"},
			{Type: SignalCategory, Key: "security"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 SSL 配置",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label:    "检查 SSL 状态",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "MySQL 可能未开启 SSL 加密连接"},
						{Desc: "明文传输存在数据泄露风险，特别是跨网络连接"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 SSL 状态",
							SkillCommand: "/sql \"SHOW VARIABLES LIKE '%ssl%'\"",
							RawSQL:       "SHOW VARIABLES LIKE '%ssl%'",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "开启强制 SSL",
							RawSQL: "-- my.cnf:\n-- [mysqld]\n-- require_secure_transport=ON\n-- ssl-ca=ca.pem\n-- ssl-cert=server-cert.pem\n-- ssl-key=server-key.pem",
							Risk:   "未配置 SSL 证书的客户端将无法连接", Rollback: "SET GLOBAL require_secure_transport=OFF"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "ssl", "encryption"},
		Versions: "5.7+",
	}
}

// MY-059: 默认存储引擎非 InnoDB
func ruleMY059DefaultStorageEngine() *Rule {
	return &Rule{
		ID:       "MY-059",
		Name:     "默认存储引擎非 InnoDB",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "storage engine"},
			{Type: SignalKeyword, Key: "存储引擎"},
			{Type: SignalKeyword, Key: "default_storage_engine"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查默认存储引擎",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label: "检查表锁等待作为代理指标",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "检查表锁等待",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("table_locks_waited") },
						Branches: []Branch{
							{
								Label:    "表锁等待高 — 可能有 MyISAM 表",
								Match:    MatchGT(5),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "表锁等待高，可能存在 MyISAM 表"},
									{Desc: "默认存储引擎应为 InnoDB，MyISAM 不支持事务和行级锁"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查默认存储引擎",
										SkillCommand: "/sql \"SELECT @@default_storage_engine\"",
										RawSQL:       "SELECT @@default_storage_engine",
										Risk: "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "查找非 InnoDB 表",
										SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE != 'InnoDB' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')\"",
										RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE != 'InnoDB' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "表锁等待正常",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "建议确认默认存储引擎为 InnoDB"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "检查默认存储引擎配置",
										SkillCommand: "/sql \"SELECT @@default_storage_engine\"",
										RawSQL:       "SELECT @@default_storage_engine",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-005"},
		Tags:     []string{"innodb", "storage_engine", "config"},
		Versions: "5.5+",
	}
}

// MY-060: 字符集不一致
func ruleMY060CharacterSetInconsistency() *Rule {
	return &Rule{
		ID:       "MY-060",
		Name:     "字符集不一致",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "character set"},
			{Type: SignalKeyword, Key: "字符集"},
			{Type: SignalKeyword, Key: "charset"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查字符集配置",
			Query: QueryGlobalVariables,
			Branches: []Branch{
				{
					Label:    "检查字符集一致性",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "数据库各层级字符集可能不一致"},
						{Desc: "字符集不一致导致 JOIN 时隐式转换，索引失效"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查各层级字符集",
							SkillCommand: "/sql \"SELECT @@character_set_server, @@character_set_database, @@character_set_connection, @@character_set_client, @@character_set_results\"",
							RawSQL:       "SELECT @@character_set_server, @@character_set_database, @@character_set_connection, @@character_set_client, @@character_set_results",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查表级字符集",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "统一使用 utf8mb4",
							SkillCommand: "/sql \"SET GLOBAL character_set_server='utf8mb4'; SET GLOBAL collation_server='utf8mb4_unicode_ci'\"",
							RawSQL:       "SET GLOBAL character_set_server='utf8mb4'; SET GLOBAL collation_server='utf8mb4_unicode_ci'",
							Risk: "已有数据需要单独转换", Rollback: "恢复原字符集配置"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-007"},
		Tags:     []string{"charset", "collation", "config"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Space / Storage (5): MY-061 ~ MY-065
// ═══════════════════════════════════════════════════════════════════════════════

// MY-061: 碎片化严重
func ruleMY061TableFragmentation() *Rule {
	return &Rule{
		ID:       "MY-061",
		Name:     "表碎片化严重",
		Category: "space",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "fragmentation"},
			{Type: SignalKeyword, Key: "碎片化"},
			{Type: SignalKeyword, Key: "data_free"},
			{Type: SignalCategory, Key: "space"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Pool 命中率作为碎片化代理",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
			Branches: []Branch{
				{
					Label: "命中率低 — 碎片化可能影响性能",
					Match: MatchLT(95),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Buffer Pool 命中率低，表碎片化可能是原因之一"},
						{Desc: "碎片化导致数据分散存储，增加磁盘 IO"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找碎片化严重的表",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, DATA_LENGTH/1024/1024 data_mb, DATA_FREE/1024/1024 free_mb, ROUND(DATA_FREE/(DATA_LENGTH+DATA_FREE)*100,1) frag_pct FROM information_schema.TABLES WHERE DATA_FREE > 100*1024*1024 AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY DATA_FREE DESC LIMIT 10\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, DATA_LENGTH/1024/1024 data_mb, DATA_FREE/1024/1024 free_mb, ROUND(DATA_FREE/(DATA_LENGTH+DATA_FREE)*100,1) frag_pct FROM information_schema.TABLES WHERE DATA_FREE > 100*1024*1024 AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY DATA_FREE DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对碎片化严重的表执行 OPTIMIZE TABLE",
							SkillCommand: "/sql \"OPTIMIZE TABLE {db}.{table}\"",
							RawSQL:       "OPTIMIZE TABLE {db}.{table}",
							Risk: "OPTIMIZE 期间表会被锁定，建议低峰期执行", Rollback: "无"},
					},
				},
				{
					Label:    "命中率正常",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "建议定期检查表碎片化情况"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看碎片化最严重的表",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, ROUND(DATA_FREE/1024/1024,1) free_mb FROM information_schema.TABLES WHERE DATA_FREE > 10*1024*1024 ORDER BY DATA_FREE DESC LIMIT 10\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, ROUND(DATA_FREE/1024/1024,1) free_mb FROM information_schema.TABLES WHERE DATA_FREE > 10*1024*1024 ORDER BY DATA_FREE DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-032"},
		CausesOf: []string{},
		Tags:     []string{"space", "fragmentation", "optimize"},
		Versions: "5.5+",
	}
}

// MY-062: Binlog 空间膨胀
func ruleMY062BinlogSpaceBloat() *Rule {
	return &Rule{
		ID:       "MY-062",
		Name:     "Binlog 空间膨胀",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: "redo_rate"},
			{Type: SignalKeyword, Key: "binlog space"},
			{Type: SignalKeyword, Key: "binlog"},
			{Type: SignalCategory, Key: "space"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "redo_rate", Op: OpGT, Value: 500},
			},
			SkipWhen: []SkipCondition{
				{Desc: "锁等待已由锁规则处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("lock_waits") > 10
				}},
				{Desc: "全表扫描已由 MY-007 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("handler_read_rnd_next") > 1000000
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Redo 写入速率（Binlog 生成代理指标）",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
			Branches: []Branch{
				{
					Label: "redo_rate > 200 MB/s — Binlog 快速增长",
					Match: MatchGT(200),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Redo 写入速率极高 (> 200 MB/s)，Binlog 快速增长"},
						{Desc: "如未及时清理，Binlog 可能占满磁盘空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Binlog 保留策略",
							SkillCommand: "/sql \"SELECT @@binlog_expire_logs_seconds, @@expire_logs_days\"",
							RawSQL:       "SELECT @@binlog_expire_logs_seconds, @@expire_logs_days",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "设置 Binlog 过期时间",
							SkillCommand: "/sql \"SET GLOBAL binlog_expire_logs_seconds=259200\"",
							RawSQL:       "SET GLOBAL binlog_expire_logs_seconds=259200 -- 3 天",
							Risk: "过短的保留期可能影响从库追赶", Rollback: "SET GLOBAL binlog_expire_logs_seconds=604800"},
						{Type: ActionFix, Desc: "手动清理旧 Binlog",
							SkillCommand: "/sql \"PURGE BINARY LOGS BEFORE DATE_SUB(NOW(), INTERVAL 3 DAY)\"",
							RawSQL:       "PURGE BINARY LOGS BEFORE DATE_SUB(NOW(), INTERVAL 3 DAY)",
							Risk: "清理后从库无法从已删除 binlog 追赶", Rollback: "无法恢复已删除的 binlog"},
					},
				},
				{
					Label:    "redo_rate 50-200 — Binlog 增长中等",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Redo 写入速率偏高，注意 Binlog 空间增长"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Binlog 文件列表",
							SkillCommand: "/sql \"SHOW BINARY LOGS\"",
							RawSQL:       "SHOW BINARY LOGS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"space", "binlog", "disk"},
		Versions: "5.5+",
	}
}

// MY-063: 临时目录空间
func ruleMY063TmpdirSpace() *Rule {
	return &Rule{
		ID:       "MY-063",
		Name:     "临时目录空间压力",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: "tmp_disk_tables_pct"},
			{Type: SignalKeyword, Key: "tmpdir"},
			{Type: SignalKeyword, Key: "临时目录"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "tmp_disk_tables_pct", Op: OpGT, Value: 30},
			},
			SkipWhen: []SkipCondition{
				{Desc: "磁盘临时表由 MY-008 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("tmp_disk_tables_pct") > 50
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查磁盘临时表比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tmp_disk_tables_pct") },
			Branches: []Branch{
				{
					Label: "30-50% — 临时目录有压力",
					Match: MatchBetween(30, 50),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "磁盘临时表比例 30-50%，临时目录空间有压力"},
						{Desc: "大量磁盘临时表可能填满 tmpdir"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 tmpdir 配置",
							SkillCommand: "/sql \"SELECT @@tmpdir\"",
							RawSQL:       "SELECT @@tmpdir",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 tmp_table_size 减少磁盘临时表",
							SkillCommand: "/sql \"SET GLOBAL tmp_table_size=128*1024*1024\"",
							RawSQL:       "SET GLOBAL tmp_table_size=134217728",
							Risk: "增加内存使用", Rollback: "SET GLOBAL tmp_table_size=16777216"},
					},
				},
				{
					Label:    "< 30%",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "磁盘临时表比例正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{"MY-008"},
		CausesOf: []string{},
		Tags:     []string{"space", "tmpdir", "tmp_table"},
		Versions: "5.5+",
	}
}

// MY-064: Redo Log 大小不当
func ruleMY064RedoLogSizeInadequate() *Rule {
	return &Rule{
		ID:       "MY-064",
		Name:     "Redo Log 大小不当",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "redo_rate"},
			{Type: SignalKeyword, Key: "redo log size"},
			{Type: SignalKeyword, Key: "innodb_log_file_size"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "redo_rate", Op: OpGT, Value: 20},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Redo 写入速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
			Branches: []Branch{
				{
					Label: "> 100 MB/s — Redo Log 可能过小",
					Match: MatchGT(100),
					Then: &TreeNode{
						Step:  "检查 innodb_log_waits",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_log_waits") },
						Branches: []Branch{
							{
								Label:    "有 log waits — Redo Log 确实过小",
								Match:    MatchGT(0),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Redo 写入速率 > 100 MB/s 且有 log waits"},
									{Desc: "innodb_log_file_size 过小导致频繁 checkpoint"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 innodb_log_file_size",
										RawSQL: "-- my.cnf: innodb_log_file_size=1G\n-- 需要重启 MySQL\n-- 8.0.30+ 支持 ALTER INSTANCE SET innodb_redo_log_capacity=2G",
										Risk:   "需要重启（8.0.30 以下版本）", Rollback: "恢复原值"},
									{Type: ActionInvestigate, Desc: "查看当前 Redo Log 配置",
										SkillCommand: "/sql \"SELECT @@innodb_log_file_size/1024/1024 log_file_size_mb, @@innodb_log_files_in_group\"",
										RawSQL:       "SELECT @@innodb_log_file_size/1024/1024 log_file_size_mb, @@innodb_log_files_in_group",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "无 log waits — Redo Log 大小尚可",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Redo 写入速率高但无 log waits，Redo Log 大小勉强够用"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "建议增大 Redo Log 以应对峰值",
										SkillCommand: "/sql \"SELECT @@innodb_log_file_size/1024/1024 log_file_size_mb\"",
										RawSQL:       "SELECT @@innodb_log_file_size/1024/1024 log_file_size_mb",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "20-100 MB/s — Redo 速率中等",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Redo 写入速率中等，Redo Log 大小基本合理"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-027"},
		Tags:     []string{"innodb", "redo_log", "config"},
		Versions: "5.5+",
	}
}

// MY-065: 慢查询日志膨胀
func ruleMY065SlowLogBloat() *Rule {
	return &Rule{
		ID:       "MY-065",
		Name:     "慢查询日志膨胀",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalKeyword, Key: "slow log"},
			{Type: SignalKeyword, Key: "慢查询日志"},
			{Type: SignalCategory, Key: "space"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "> 50/s — 大量慢查询写入日志",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "慢查询 > 50/s，慢查询日志快速膨胀"},
						{Desc: "日志文件过大会占用磁盘空间，影响日志轮转"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "调高 long_query_time 减少日志量",
							SkillCommand: "/sql \"SET GLOBAL long_query_time=2\"",
							RawSQL:       "SET GLOBAL long_query_time=2",
							Risk: "可能遗漏部分慢查询", Rollback: "SET GLOBAL long_query_time=1"},
						{Type: ActionFix, Desc: "设置慢查询日志轮转",
							RawSQL: "-- 定期执行: mysqladmin flush-logs\n-- 或配置 logrotate 自动轮转",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "10-50/s — 中度慢查询日志增长",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "慢查询日志增长中等，建议关注日志文件大小"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查慢查询日志配置",
							SkillCommand: "/sql \"SELECT @@slow_query_log, @@slow_query_log_file, @@long_query_time\"",
							RawSQL:       "SELECT @@slow_query_log, @@slow_query_log_file, @@long_query_time",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{},
		Tags:     []string{"space", "slow_log", "disk"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Wait Event Analysis (10): MY-066 ~ MY-075
// ═══════════════════════════════════════════════════════════════════════════════

// MY-066: Mutex 争用
func ruleMY066MutexContention() *Rule {
	return &Rule{
		ID:       "MY-066",
		Name:     "InnoDB Mutex 争用",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_mutex_os_waits"},
			{Type: SignalWaitEvent, Key: "wait/synch/mutex/innodb"},
			{Type: SignalKeyword, Key: "mutex"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_mutex_os_waits", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Mutex OS Waits",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_mutex_os_waits") },
			Branches: []Branch{
				{
					Label: "> 1000/s — 严重 Mutex 争用",
					Match: MatchGT(1000),
					Then: &TreeNode{
						Step:  "检查并发线程数",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("threads_running") },
						Branches: []Branch{
							{
								Label:    "高并发 — 并发导致 Mutex 争用",
								Match:    MatchGT(30),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "InnoDB Mutex OS Waits > 1000/s，并发线程 > 30"},
									{Desc: "高并发导致 InnoDB 内部 Mutex 争用严重"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看 Mutex 等待详情",
										SkillCommand: "/sql \"SHOW ENGINE INNODB MUTEX\"",
										RawSQL:       "SHOW ENGINE INNODB MUTEX",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增加 Buffer Pool 实例数减少争用",
										RawSQL: "-- my.cnf: innodb_buffer_pool_instances=8\n-- 需要重启（Buffer Pool > 1GB 时有效）",
										Risk:   "需要重启", Rollback: "恢复原值"},
								},
							},
							{
								Label:    "低并发 — 特定热点导致",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Mutex 争用高但并发不高，可能是特定热点资源"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 Mutex 详情定位热点",
										SkillCommand: "/sql \"SHOW ENGINE INNODB MUTEX\"",
										RawSQL:       "SHOW ENGINE INNODB MUTEX",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "100-1000/s — 中度 Mutex 争用",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB Mutex 争用中等，持续监控"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Mutex 状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB MUTEX\"",
							RawSQL:       "SHOW ENGINE INNODB MUTEX",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-011"},
		CausesOf: []string{},
		Tags:     []string{"mutex", "innodb", "concurrency"},
		Versions: "5.5+",
	}
}

// MY-067: Buffer Pool Mutex 争用
func ruleMY067BufferPoolMutex() *Rule {
	return &Rule{
		ID:       "MY-067",
		Name:     "Buffer Pool Mutex 争用",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "wait/synch/mutex/innodb/buf_pool_mutex"},
			{Type: SignalKeyword, Key: "buffer pool mutex"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_mutex_os_waits", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Pool 相关等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
			Branches: []Branch{
				{
					Label: "命中率低 — Buffer Pool 压力大",
					Match: MatchLT(95),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Buffer Pool 命中率低，可能导致 buf_pool_mutex 争用"},
						{Desc: "Buffer Pool 实例数不足时高并发读写会在 mutex 上排队"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增加 Buffer Pool 实例数",
							RawSQL: "-- my.cnf: innodb_buffer_pool_instances=8\n-- 需要 Buffer Pool >= 1GB 才有意义",
							Risk:   "需要重启", Rollback: "恢复原值"},
						{Type: ActionFix, Desc: "增大 Buffer Pool 减少争用",
							RawSQL: "-- SET GLOBAL innodb_buffer_pool_size=原值*2 (8.0 可在线调整)",
							Risk:   "增加内存使用", Rollback: "恢复原 innodb_buffer_pool_size"},
					},
				},
				{
					Label:    "命中率正常 — 轻度争用",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Pool 命中率正常，Mutex 争用轻度"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控 Buffer Pool 争用趋势",
							SkillCommand: "/sql \"SHOW ENGINE INNODB MUTEX\"",
							RawSQL:       "SHOW ENGINE INNODB MUTEX",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-013"},
		CausesOf: []string{"MY-066"},
		Tags:     []string{"buffer_pool", "mutex", "memory"},
		Versions: "5.5+",
	}
}

// MY-068: Log Buffer 等待
func ruleMY068LogBufferWait() *Rule {
	return &Rule{
		ID:       "MY-068",
		Name:     "Log Buffer 等待",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_log_waits"},
			{Type: SignalWaitEvent, Key: "wait/synch/mutex/innodb/log_sys_mutex"},
			{Type: SignalKeyword, Key: "log buffer"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_log_waits", Op: OpGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "innodb_log_waits 由 MY-027 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("innodb_log_waits") > 10
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Log Buffer 等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_log_waits") },
			Branches: []Branch{
				{
					Label: "log_waits 1-10 — 偶发 Log Buffer 等待",
					Match: MatchBetween(1, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB Log Waits 偶发，log buffer 偶尔不够用"},
						{Desc: "事务提交时需等待 log buffer 空间，影响写入性能"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 innodb_log_buffer_size",
							SkillCommand: "/sql \"SET GLOBAL innodb_log_buffer_size=32*1024*1024\"",
							RawSQL:       "SET GLOBAL innodb_log_buffer_size=33554432",
							Risk: "增加内存使用（8.0+可动态调整）", Rollback: "SET GLOBAL innodb_log_buffer_size=16777216"},
						{Type: ActionInvestigate, Desc: "检查当前 log buffer 配置",
							SkillCommand: "/sql \"SELECT @@innodb_log_buffer_size/1024/1024 log_buffer_mb\"",
							RawSQL:       "SELECT @@innodb_log_buffer_size/1024/1024 log_buffer_mb",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "无等待",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Log Buffer 状态正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{"MY-019"},
		CausesOf: []string{"MY-027"},
		Tags:     []string{"innodb", "log_buffer", "wait"},
		Versions: "5.5+",
	}
}

// MY-069: 文件 IO 等待
func ruleMY069FileIOWait() *Rule {
	return &Rule{
		ID:       "MY-069",
		Name:     "文件 IO 等待高",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_data_pending_fsyncs"},
			{Type: SignalWaitEvent, Key: "wait/io/file/innodb/innodb_data_file"},
			{Type: SignalKeyword, Key: "io wait"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_data_pending_fsyncs", Op: OpGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "高 pending fsyncs 由 MY-031 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("innodb_data_pending_fsyncs") > 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 IO 等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_data_pending_fsyncs") },
			Branches: []Branch{
				{
					Label: "pending fsyncs 3-5 — 轻度 IO 等待",
					Match: MatchBetween(3, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB 文件 IO 有轻度等待，磁盘响应偏慢"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 IO 性能指标",
							SkillCommand: "/sql \"SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 10\"",
							RawSQL:       "SELECT * FROM sys.io_global_by_wait_by_latency LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "检查 innodb_flush_method 配置",
							SkillCommand: "/sql \"SELECT @@innodb_flush_method\"",
							RawSQL:       "SELECT @@innodb_flush_method",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "IO 等待正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-031"},
		Tags:     []string{"io", "fsync", "wait_event"},
		Versions: "5.5+",
	}
}

// MY-070: 网络等待
func ruleMY070NetworkWait() *Rule {
	return &Rule{
		ID:       "MY-070",
		Name:     "网络传输异常",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "bytes_sent"},
			{Type: SignalMetric, Key: "bytes_received"},
			{Type: SignalKeyword, Key: "network"},
			{Type: SignalKeyword, Key: "网络"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "bytes_sent", Op: OpGT, Value: 500000000},
			},
		},
		Tree: &TreeNode{
			Step: "检查网络传输量",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("bytes_sent") + ctx.MetricValue("bytes_received")
			},
			Branches: []Branch{
				{
					Label: "> 1GB/s — 网络传输量极大",
					Match: MatchGT(1000000000),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "网络传输 > 1GB/s，可能有大结果集查询或大量数据传输"},
						{Desc: "大结果集传输可能导致网络拥塞和客户端超时"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找传输量最大的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_ROWS_SENT, COUNT_STAR, SUM_ROWS_SENT/NULLIF(COUNT_STAR,0) avg_rows FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_ROWS_SENT DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_ROWS_SENT, COUNT_STAR, SUM_ROWS_SENT/NULLIF(COUNT_STAR,0) avg_rows FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_ROWS_SENT DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "为大结果集查询添加 LIMIT 或分页",
							RawSQL: "-- 为不带 LIMIT 的 SELECT 添加合理的 LIMIT",
							Risk:   "需验证业务逻辑", Rollback: "无"},
					},
				},
				{
					Label:    "500MB-1GB/s — 网络传输量偏高",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "网络传输量偏高，关注大结果集查询"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查网络流量",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Bytes%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Bytes%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{},
		Tags:     []string{"network", "bytes", "session"},
		Versions: "5.5+",
	}
}

// MY-071: 表锁等待
func ruleMY071TableLockWait() *Rule {
	return &Rule{
		ID:       "MY-071",
		Name:     "表锁等待事件高",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "table_locks_waited"},
			{Type: SignalWaitEvent, Key: "wait/lock/table"},
			{Type: SignalKeyword, Key: "table lock wait"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "table_locks_waited", Op: OpGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "表锁争用由 MY-005 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("table_locks_waited") > 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查表锁等待速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("table_locks_waited") },
			Branches: []Branch{
				{
					Label: "3-5/s — 轻度表锁等待",
					Match: MatchBetween(3, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "表锁等待 3-5/s，存在轻度表锁争用"},
						{Desc: "可能有 LOCK TABLES 操作或 MyISAM 表"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查表锁来源",
							SkillCommand: "/sql \"SHOW OPEN TABLES WHERE In_use > 0\"",
							RawSQL:       "SHOW OPEN TABLES WHERE In_use > 0",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "表锁等待正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-005"},
		Tags:     []string{"lock", "table_lock", "wait_event"},
		Versions: "5.5+",
	}
}

// MY-072: MDL 等待
func ruleMY072MDLWait() *Rule {
	return &Rule{
		ID:       "MY-072",
		Name:     "MDL 等待事件高",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalWaitEvent, Key: "wait/lock/metadata/sql/mdl"},
			{Type: SignalKeyword, Key: "metadata lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "MDL 阻塞由 MY-002 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("long_queries") > 0 && ctx.MetricValue("lock_waits") > 5
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 MDL 等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label: "lock_waits 3-5 — 轻度 MDL 等待",
					Match: MatchBetween(3, 5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "锁等待 3-5，可能存在 MDL 争用"},
						{Desc: "DDL 操作与长时间查询竞争 MDL 锁"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 MDL 锁状态",
							SkillCommand: "/sql \"SELECT * FROM performance_schema.metadata_locks WHERE LOCK_STATUS='PENDING'\"",
							RawSQL:       "SELECT * FROM performance_schema.metadata_locks WHERE LOCK_STATUS='PENDING'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "MDL 等待正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-002"},
		Tags:     []string{"lock", "mdl", "wait_event"},
		Versions: "5.7+",
	}
}

// MY-073: 全局锁等待
func ruleMY073GlobalReadLock() *Rule {
	return &Rule{
		ID:       "MY-073",
		Name:     "全局读锁等待",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "threads_running"},
			{Type: SignalKeyword, Key: "global read lock"},
			{Type: SignalKeyword, Key: "FTWRL"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 5},
				{Source: "metrics", Field: "threads_running", Op: OpGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "有明确阻塞链则由 MY-042 处理", Check: func(ctx *EvalContext) bool {
					return ctx.HasBlockingChains() && ctx.MetricValue("innodb_row_lock_waits") > 10
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待与线程堆积",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label: "lock_waits > 50 — 可能存在全局锁",
					Match: MatchGT(50),
					Then: &TreeNode{
						Step:  "检查并发线程堆积情况",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("threads_running") },
						Branches: []Branch{
							{
								Label:    "大量线程堆积 — FTWRL 或备份导致",
								Match:    MatchGT(50),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大面积锁等待 + 大量线程堆积，可能存在全局读锁（FTWRL）"},
									{Desc: "备份工具执行 FLUSH TABLES WITH READ LOCK 会阻塞所有写操作"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找持有全局锁的会话",
										SkillCommand: "/sql \"SELECT * FROM performance_schema.metadata_locks WHERE LOCK_TYPE='SHARED' AND LOCK_DURATION='EXPLICIT'\"",
										RawSQL:       "SELECT * FROM performance_schema.metadata_locks WHERE LOCK_TYPE='SHARED' AND LOCK_DURATION='EXPLICIT'",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "如确认是备份导致，等待备份完成或终止备份",
										SkillCommand: "/sql \"SELECT id, user, host, command, time, state FROM information_schema.processlist WHERE command='Query' AND state LIKE '%lock%'\"",
										RawSQL:       "SELECT id, user, host, command, time, state FROM information_schema.processlist WHERE command='Query' AND state LIKE '%lock%'",
										Risk: "终止备份可能导致备份不完整", Rollback: "无"},
								},
							},
							{
								Label:    "线程未大量堆积",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大面积锁等待但线程堆积不严重，非全局锁"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查锁等待来源",
										SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
										RawSQL:       "SHOW ENGINE INNODB STATUS",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "lock_waits 20-50",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "锁等待 20-50，关注锁来源"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查锁状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-004", "MY-011"},
		Tags:     []string{"lock", "global_lock", "FTWRL", "backup"},
		Versions: "5.5+",
	}
}

// MY-074: Binlog 组提交等待
func ruleMY074BinlogGroupCommit() *Rule {
	return &Rule{
		ID:       "MY-074",
		Name:     "Binlog 组提交延迟",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "tps"},
			{Type: SignalKeyword, Key: "group commit"},
			{Type: SignalKeyword, Key: "binlog group"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "tps", Op: OpGT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 TPS 和 Redo 写入速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tps") },
			Branches: []Branch{
				{
					Label: "TPS > 1000 — 高 TPS 下组提交很重要",
					Match: MatchGT(1000),
					Then: &TreeNode{
						Step:  "检查 Redo 速率是否匹配 TPS",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
						Branches: []Branch{
							{
								Label:    "redo_rate 高 — 组提交效率影响性能",
								Match:    MatchGT(50),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "高 TPS (> 1000) 且 redo 速率高，binlog 组提交配置可能不当"},
									{Desc: "适当的组提交延迟可以减少 fsync 次数，提升吞吐量"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查组提交配置",
										SkillCommand: "/sql \"SELECT @@binlog_group_commit_sync_delay, @@binlog_group_commit_sync_no_delay_count\"",
										RawSQL:       "SELECT @@binlog_group_commit_sync_delay, @@binlog_group_commit_sync_no_delay_count",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "优化组提交延迟",
										SkillCommand: "/sql \"SET GLOBAL binlog_group_commit_sync_delay=1000; SET GLOBAL binlog_group_commit_sync_no_delay_count=10\"",
										RawSQL:       "SET GLOBAL binlog_group_commit_sync_delay=1000; SET GLOBAL binlog_group_commit_sync_no_delay_count=10",
										Risk: "增加单次事务延迟（微秒级）", Rollback: "SET GLOBAL binlog_group_commit_sync_delay=0"},
								},
							},
							{
								Label:    "redo_rate 正常",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "组提交配置基本合理"},
								},
								Actions: []Action{},
							},
						},
					},
				},
				{
					Label:    "TPS 100-1000 — 中等 TPS",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "TPS 中等，组提交影响不大"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"binlog", "group_commit", "replication"},
		Versions: "5.7+",
	}
}

// MY-075: 自适应刷脏 — innodb_buffer_pool_wait_free 高
func ruleMY075AdaptiveFlush() *Rule {
	return &Rule{
		ID:       "MY-075",
		Name:     "Buffer Pool 无空闲页等待",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_buffer_pool_wait_free"},
			{Type: SignalKeyword, Key: "wait free"},
			{Type: SignalKeyword, Key: "刷脏"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_buffer_pool_wait_free", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Pool Wait Free",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_buffer_pool_wait_free") },
			Branches: []Branch{
				{
					Label: "> 10 — 频繁等待空闲页",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查 redo 速率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("redo_rate") },
						Branches: []Branch{
							{
								Label:    "redo_rate 高 — 写入密集导致刷脏不及时",
								Match:    MatchGT(50),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Buffer Pool Wait Free > 10，频繁等待空闲页"},
									{Desc: "写入密集导致脏页刷盘不及时，新读取无空闲页可用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 innodb_io_capacity 加速刷脏",
										SkillCommand: "/sql \"SET GLOBAL innodb_io_capacity=2000; SET GLOBAL innodb_io_capacity_max=4000\"",
										RawSQL:       "SET GLOBAL innodb_io_capacity=2000; SET GLOBAL innodb_io_capacity_max=4000",
										Risk: "增加 IO 操作", Rollback: "SET GLOBAL innodb_io_capacity=200"},
									{Type: ActionFix, Desc: "增大 Buffer Pool",
										RawSQL: "-- SET GLOBAL innodb_buffer_pool_size=更大的值 (8.0 可在线调整)",
										Risk:   "增加内存使用", Rollback: "恢复原值"},
								},
							},
							{
								Label:    "redo_rate 正常 — Buffer Pool 过小",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Wait Free 高但写入不密集，Buffer Pool 可能过小"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 Buffer Pool",
										RawSQL: "-- SET GLOBAL innodb_buffer_pool_size=更大的值",
										Risk:   "增加内存使用", Rollback: "恢复原值"},
								},
							},
						},
					},
				},
				{
					Label:    "1-10 — 偶发等待",
					Match:    MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Pool Wait Free 偶发，建议关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Buffer Pool 和 IO 配置",
							SkillCommand: "/sql \"SELECT @@innodb_buffer_pool_size/1024/1024/1024 bp_gb, @@innodb_io_capacity, @@innodb_io_capacity_max\"",
							RawSQL:       "SELECT @@innodb_buffer_pool_size/1024/1024/1024 bp_gb, @@innodb_io_capacity, @@innodb_io_capacity_max",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-014"},
		CausesOf: []string{},
		Tags:     []string{"buffer_pool", "flush", "wait_free", "memory"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Operational (5): MY-076 ~ MY-080
// ═══════════════════════════════════════════════════════════════════════════════

// MY-076: 统计信息过期
func ruleMY076StaleStatistics() *Rule {
	return &Rule{
		ID:       "MY-076",
		Name:     "统计信息过期",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "statistics"},
			{Type: SignalKeyword, Key: "统计信息"},
			{Type: SignalKeyword, Key: "analyze table"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "锁等待已由锁规则处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("lock_waits") > 5
				}},
				{Desc: "高 redo 已由 MY-062 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("redo_rate") > 200
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询数",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "有慢查询 — 可能统计信息过期导致",
					Match: MatchGT(1),
					Then: &TreeNode{
						Step:  "检查全表扫描情况",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("handler_read_rnd_next") },
						Branches: []Branch{
							{
								Label:    "全表扫描多 — 统计信息可能过期",
								Match:    MatchGT(100000),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在慢查询且全表扫描多，统计信息可能过期"},
									{Desc: "过期的统计信息导致优化器选择错误的执行计划"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查统计信息最后更新时间",
										SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, UPDATE_TIME FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') AND UPDATE_TIME < DATE_SUB(NOW(), INTERVAL 7 DAY) ORDER BY UPDATE_TIME LIMIT 10\"",
										RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, UPDATE_TIME FROM information_schema.TABLES WHERE TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') AND UPDATE_TIME < DATE_SUB(NOW(), INTERVAL 7 DAY) ORDER BY UPDATE_TIME LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "更新统计信息",
										SkillCommand: "/sql \"ANALYZE TABLE {db}.{table}\"",
										RawSQL:       "ANALYZE TABLE {db}.{table}",
										Risk: "ANALYZE 期间会短暂锁表", Rollback: "无"},
								},
							},
							{
								Label:    "全表扫描正常",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "有慢查询但全表扫描不多，检查其他原因"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 innodb_stats_persistent 配置",
										SkillCommand: "/sql \"SELECT @@innodb_stats_persistent, @@innodb_stats_auto_recalc, @@innodb_stats_persistent_sample_pages\"",
										RawSQL:       "SELECT @@innodb_stats_persistent, @@innodb_stats_auto_recalc, @@innodb_stats_persistent_sample_pages",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "无明显慢查询，统计信息可能正常"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-007"},
		Tags:     []string{"statistics", "analyze", "sql_perf"},
		Versions: "5.6+",
	}
}

// MY-077: 在线 DDL 阻塞
func ruleMY077OnlineDDLBlocking() *Rule {
	return &Rule{
		ID:       "MY-077",
		Name:     "在线 DDL 阻塞",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "threads_running"},
			{Type: SignalKeyword, Key: "online ddl"},
			{Type: SignalKeyword, Key: "DDL"},
			{Type: SignalKeyword, Key: "alter table"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 3},
				{Source: "metrics", Field: "threads_running", Op: OpGT, Value: 5},
			},
			SkipWhen: []SkipCondition{
				{Desc: "有阻塞链则由 MY-042 处理", Check: func(ctx *EvalContext) bool {
					return ctx.HasBlockingChains() && ctx.MetricValue("innodb_row_lock_waits") > 10
				}},
				{Desc: "全表扫描明显则由 MY-007 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("handler_read_rnd_next") > 5000000
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待和线程堆积",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label: "lock_waits > 30 — 可能有 DDL 阻塞",
					Match: MatchGT(30),
					Then: &TreeNode{
						Step:  "检查长时间运行的查询",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
						Branches: []Branch{
							{
								Label:    "有长查询 — DDL 等待 MDL 写锁",
								Match:    MatchGT(0),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "锁等待 > 30 且有长查询，可能 DDL 在等待 MDL 写锁"},
									{Desc: "DDL 等待期间阻塞所有后续 DML 操作"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找等待 MDL 的 DDL 操作",
										SkillCommand: "/sql \"SELECT * FROM information_schema.processlist WHERE state LIKE '%Waiting for table metadata lock%'\"",
										RawSQL:       "SELECT * FROM information_schema.processlist WHERE state LIKE '%Waiting for table metadata lock%'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "使用 pt-online-schema-change 替代原生 DDL",
										RawSQL: "-- pt-online-schema-change --alter 'ADD COLUMN ...' D=db,t=table --execute",
										Risk:   "pt-osc 会创建触发器和影子表", Rollback: "pt-online-schema-change --drop-old-table"},
								},
							},
							{
								Label:    "无长查询 — 其他阻塞原因",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大面积锁等待但无长查询，检查是否有大批量 DML"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查当前活跃操作",
										SkillCommand: "/activesessions",
										RawSQL:       "SELECT * FROM information_schema.processlist WHERE command != 'Sleep' ORDER BY time DESC",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "lock_waits 10-30",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "锁等待偏高，关注是否有 DDL 操作"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否有 DDL 操作",
							SkillCommand: "/sql \"SELECT * FROM information_schema.processlist WHERE info LIKE 'ALTER%' OR info LIKE 'CREATE INDEX%' OR info LIKE 'DROP%'\"",
							RawSQL:       "SELECT * FROM information_schema.processlist WHERE info LIKE 'ALTER%' OR info LIKE 'CREATE INDEX%' OR info LIKE 'DROP%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-002"},
		CausesOf: []string{"MY-004", "MY-011"},
		Tags:     []string{"ddl", "online_ddl", "lock", "pt-osc"},
		Versions: "5.6+",
	}
}

// MY-078: 大表无分区
func ruleMY078LargeTableNoPartition() *Rule {
	return &Rule{
		ID:       "MY-078",
		Name:     "大表无分区",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "partition"},
			{Type: SignalKeyword, Key: "分区"},
			{Type: SignalKeyword, Key: "大表"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查是否有慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "有慢查询 — 大表可能需要分区",
					Match: MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在慢查询，大表（> 1000 万行）未分区可能是原因之一"},
						{Desc: "大表全表扫描、DDL 操作、备份都会受影响"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找行数超过 1000 万的大表",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_ROWS, ROUND(DATA_LENGTH/1024/1024,1) data_mb, PARTITION_METHOD FROM information_schema.TABLES LEFT JOIN information_schema.PARTITIONS USING(TABLE_SCHEMA, TABLE_NAME) WHERE TABLE_ROWS > 10000000 AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') GROUP BY TABLE_SCHEMA, TABLE_NAME ORDER BY TABLE_ROWS DESC LIMIT 10\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_ROWS, ROUND(DATA_LENGTH/1024/1024,1) data_mb FROM information_schema.TABLES WHERE TABLE_ROWS > 10000000 AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys') ORDER BY TABLE_ROWS DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对时间序列数据按时间分区",
							RawSQL: "-- ALTER TABLE t PARTITION BY RANGE (TO_DAYS(create_time)) (\n--   PARTITION p202601 VALUES LESS THAN (TO_DAYS('2026-02-01')),\n--   ...\n-- )",
							Risk:   "分区操作会重建表", Rollback: "ALTER TABLE t REMOVE PARTITIONING"},
					},
				},
				{
					Label:    "无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "无明显慢查询，大表暂时不影响性能"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "建议对大表评估是否需要分区",
							RawSQL: "SELECT TABLE_SCHEMA, TABLE_NAME, TABLE_ROWS FROM information_schema.TABLES WHERE TABLE_ROWS > 10000000 ORDER BY TABLE_ROWS DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-007"},
		Tags:     []string{"partition", "large_table", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-079: 外键约束检查开销
func ruleMY079ForeignKeyOverhead() *Rule {
	return &Rule{
		ID:       "MY-079",
		Name:     "外键约束检查开销",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_row_lock_waits"},
			{Type: SignalKeyword, Key: "foreign key"},
			{Type: SignalKeyword, Key: "外键"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_row_lock_waits", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查行锁等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_row_lock_waits") },
			Branches: []Branch{
				{
					Label: "行锁等待高 — 外键可能是原因之一",
					Match: MatchGT(20),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "行锁等待高，外键约束检查可能增加额外锁等待"},
						{Desc: "外键检查需要额外的共享锁，在高并发 DML 时可能加剧锁争用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找有外键约束的表",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, CONSTRAINT_NAME, REFERENCED_TABLE_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_NAME IS NOT NULL AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, CONSTRAINT_NAME, REFERENCED_TABLE_NAME FROM information_schema.KEY_COLUMN_USAGE WHERE REFERENCED_TABLE_NAME IS NOT NULL AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "批量数据导入时临时关闭外键检查",
							SkillCommand: "/sql \"SET FOREIGN_KEY_CHECKS=0\"",
							RawSQL:       "SET FOREIGN_KEY_CHECKS=0 -- 仅在当前会话有效",
							Risk: "关闭期间不检查外键一致性", Rollback: "SET FOREIGN_KEY_CHECKS=1"},
					},
				},
				{
					Label:    "行锁等待正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "行锁等待不高，外键影响可忽略"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-001"},
		Tags:     []string{"foreign_key", "lock", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-080: 事件调度器问题
func ruleMY080EventSchedulerIssue() *Rule {
	return &Rule{
		ID:       "MY-080",
		Name:     "事件调度器问题",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "event scheduler"},
			{Type: SignalKeyword, Key: "事件调度器"},
			{Type: SignalKeyword, Key: "event_scheduler"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查活跃线程",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("threads_running") },
			Branches: []Branch{
				{
					Label: "活跃线程偏高 — 事件调度器可能有影响",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step:  "检查 TPS",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tps") },
						Branches: []Branch{
							{
								Label:    "TPS 低 — 事件调度器可能执行低效任务",
								Match:    MatchLT(50),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "活跃线程高但 TPS 低，事件调度器可能执行低效的定时任务"},
									{Desc: "event_scheduler 中的事件可能在高峰期执行大量操作"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查事件调度器状态和已注册事件",
										SkillCommand: "/sql \"SELECT @@event_scheduler; SELECT EVENT_SCHEMA, EVENT_NAME, STATUS, LAST_EXECUTED, INTERVAL_VALUE, INTERVAL_FIELD FROM information_schema.EVENTS\"",
										RawSQL:       "SELECT @@event_scheduler; SELECT EVENT_SCHEMA, EVENT_NAME, STATUS, LAST_EXECUTED, INTERVAL_VALUE, INTERVAL_FIELD FROM information_schema.EVENTS",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "调整高峰期事件执行时间",
										RawSQL: "-- ALTER EVENT {event_name} ON SCHEDULE EVERY 1 HOUR STARTS '2026-01-01 03:00:00'",
										Risk:   "修改调度时间需要业务确认", Rollback: "恢复原调度配置"},
								},
							},
							{
								Label:    "TPS 正常",
								Match:    MatchDefault(),
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "事件调度器对性能影响不大"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "检查事件调度器配置",
										SkillCommand: "/sql \"SELECT @@event_scheduler\"",
										RawSQL:       "SELECT @@event_scheduler",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "活跃线程正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "活跃线程正常，事件调度器无明显影响"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "检查事件调度器状态",
							SkillCommand: "/sql \"SELECT @@event_scheduler\"",
							RawSQL:       "SELECT @@event_scheduler",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"event_scheduler", "operations"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// P1 New Rules: MY-081 ~ MY-082
// ═══════════════════════════════════════════════════════════════════════════════

// MY-081: 空闲连接堆积（sleep_sessions 高 + connections_pct 低说明大量 idle 连接占资源）
func ruleMY081IdleConnectionPileup() *Rule {
	return &Rule{
		ID:       "MY-081",
		Name:     "空闲连接堆积",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "sleep_sessions"},
			{Type: SignalMetric, Key: "threads_connected"},
			{Type: SignalKeyword, Key: "空闲连接"},
			{Type: SignalKeyword, Key: "idle connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "sleep_sessions", Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查空闲连接数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("sleep_sessions") },
			Branches: []Branch{
				{
					Label: "空闲连接 > 100 — 严重堆积",
					Match: MatchGT(100),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "空闲连接（Sleep）超过 100，大量连接未释放"},
						{Desc: "空闲连接占用内存和文件描述符，可能导致新连接被拒绝"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看空闲时间最长的连接",
							RawSQL: "SELECT id, user, host, db, time, state FROM information_schema.processlist WHERE command='Sleep' ORDER BY time DESC LIMIT 20",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "设置 wait_timeout 自动清理空闲连接",
							RawSQL: "SET GLOBAL wait_timeout=300; SET GLOBAL interactive_timeout=300;",
							Risk:   "长连接应用可能受影响", Rollback: "SET GLOBAL wait_timeout=28800"},
						{Type: ActionPrevent, Desc: "引入连接池（ProxySQL/HikariCP）复用连接",
							Risk: "需要应用层改造"},
					},
				},
				{
					Label: "空闲连接 50-100 — 偏高",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "空闲连接 50-100，连接未被有效回收"},
						{Desc: "检查应用连接池 min_idle 配置和 wait_timeout 设置"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "按来源分析空闲连接分布",
							RawSQL: "SELECT user, LEFT(host, LOCATE(':', host)-1) AS client_ip, COUNT(*) cnt FROM information_schema.processlist WHERE command='Sleep' GROUP BY user, client_ip ORDER BY cnt DESC",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "减小 wait_timeout",
							RawSQL: "SET GLOBAL wait_timeout=600;",
							Risk:   "长空闲连接会被断开", Rollback: "SET GLOBAL wait_timeout=28800"},
					},
				},
				{
					Label:    "空闲连接 30-50 — 轻度堆积",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "空闲连接 30-50，关注增长趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查连接来源和 wait_timeout 配置",
							RawSQL: "SELECT @@wait_timeout, @@interactive_timeout; SELECT user, COUNT(*) cnt FROM information_schema.processlist WHERE command='Sleep' GROUP BY user ORDER BY cnt DESC",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-010"},
		Tags:     []string{"connection", "idle", "sleep", "session"},
		Versions: "5.5+",
	}
}

// MY-082: 活跃线程冲高（threads_running 高但无具体锁/IO 等待，通用 SQL 负载高）
func ruleMY082ActiveThreadsHigh() *Rule {
	return &Rule{
		ID:       "MY-082",
		Name:     "活跃线程冲高",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "threads_running"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalKeyword, Key: "活跃线程"},
			{Type: SignalKeyword, Key: "threads_running"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "threads_running", Op: OpGT, Value: 3},
			},
			SkipWhen: []SkipCondition{
				{Desc: "锁等待已由 MY-004/MY-042 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("lock_waits") > 10
				}},
				{Desc: "空闲连接已由 MY-081 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("sleep_sessions") > 30
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查活跃线程数",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("threads_running") },
			Branches: []Branch{
				{
					Label: "> 30 — 大量活跃线程",
					Match: MatchGT(30),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Threads_running > 30，大量线程同时活跃"},
						{Desc: "CPU 密集型查询或并发过高，可能导致吞吐下降"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看当前活跃线程的 SQL",
							RawSQL: "SELECT id, user, host, db, time, LEFT(info, 200) AS sql_text FROM information_schema.processlist WHERE command='Query' AND info IS NOT NULL ORDER BY time DESC LIMIT 20",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "限制并发度或优化高频 SQL",
							RawSQL: "-- 检查 Top SQL: SELECT DIGEST_TEXT, COUNT_STAR, AVG_TIMER_WAIT/1e12 avg_sec FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_TIMER_WAIT DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label: "3-30 — 中度活跃",
					Match: MatchDefault(),
					Then: &TreeNode{
						Step:  "区分负载类型",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("qps") },
						Branches: []Branch{
							{
								Label:    "QPS > 1000 — SQL 密集型",
								Match:    MatchGT(1000),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "活跃线程偏高且 QPS 高，SQL 密集型负载"},
									{Desc: "查看 Top SQL 定位高频或缺索引��查询"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看 Top SQL",
										RawSQL: "SELECT DIGEST_TEXT, COUNT_STAR, ROUND(AVG_TIMER_WAIT/1e12, 3) avg_sec, SUM_ROWS_EXAMINED FROM performance_schema.events_statements_summary_by_digest WHERE LAST_SEEN > DATE_SUB(NOW(), INTERVAL 1 MINUTE) ORDER BY SUM_TIMER_WAIT DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "为高频全扫 SQL 添加索引",
										RawSQL: "-- EXPLAIN FORMAT=TREE {sql}\n-- ALTER TABLE t ADD INDEX idx_xxx (col)",
										Risk: "创建索引期间可能锁表", Rollback: "DROP INDEX"},
								},
							},
							{
								Label:    "QPS 正常 — 慢查询或资源争用",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "活跃线程偏高但 QPS 不高，可能有慢查询或资源争用"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看当前活跃线程 SQL",
										RawSQL: "SELECT id, user, time, LEFT(info, 200) FROM information_schema.processlist WHERE command='Query' ORDER BY time DESC LIMIT 20",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"MY-006", "MY-007"},
		CausesOf: []string{},
		Tags:     []string{"threads_running", "sql_perf", "cpu"},
		Versions: "5.5+",
	}
}

// MY-083: Aborted Clients 冲高（客户端异常断开）
func ruleMY083AbortedClientsSurge() *Rule {
	return &Rule{
		ID:       "MY-083",
		Name:     "客户端异常断开冲高",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "aborted_clients"},
			{Type: SignalMetric, Key: "aborted_clients_rate"},
			{Type: SignalKeyword, Key: "aborted_clients"},
			{Type: SignalKeyword, Key: "异常断开"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "aborted_clients", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Aborted_clients 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("aborted_clients") },
			Branches: []Branch{
				{
					Label: "> 50/s — 大量客户端异常断开",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Aborted_clients > 50/s，大量已建立的连接被异常关闭"},
						{Desc: "可能原因：网络不稳定、客户端超时、wait_timeout 过短、应用未正确关闭连接"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 Aborted 连接状态和 wait_timeout",
							RawSQL: "SHOW GLOBAL STATUS LIKE 'Aborted%'; SELECT @@wait_timeout, @@interactive_timeout, @@net_read_timeout, @@net_write_timeout",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "适当增大超时参数",
							RawSQL: "SET GLOBAL net_read_timeout=60; SET GLOBAL net_write_timeout=120;",
							Risk:   "可能掩盖应用层问题", Rollback: "SET GLOBAL net_read_timeout=30; SET GLOBAL net_write_timeout=60"},
					},
				},
				{
					Label:    "5-50/s — 中度异常断开",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Aborted_clients 5-50/s，存在客户端异常断开"},
						{Desc: "检查网络质量和应用连接管理逻辑"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查连接状态",
							RawSQL: "SHOW GLOBAL STATUS LIKE 'Aborted%'; SHOW GLOBAL STATUS LIKE 'Connection%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"connection", "aborted_clients", "network"},
		Versions: "5.5+",
	}
}
