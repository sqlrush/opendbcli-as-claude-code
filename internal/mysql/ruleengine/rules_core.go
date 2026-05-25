/*-------------------------------------------------------------------------
 *
 * rules_core.go
 *	  MySQL rule engine — core probe-data classification rules.
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/ruleengine/rules_core.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// coreRules returns 25 hardcoded MySQL diagnostic rules covering lock/concurrency,
// slow query, connection, buffer/memory, replication, IO/redo, and general scenarios.
func coreRules() []*Rule {
	return []*Rule{
		// Lock / Concurrency (5)
		ruleMY001InnoDBRowLockWaits(),
		ruleMY002MetadataLock(),
		ruleMY003Deadlocks(),
		ruleMY004LockWaitTimeout(),
		ruleMY005TableLocksWaited(),

		// Slow Query (4)
		ruleMY006SlowQueries(),
		ruleMY007FullTableScan(),
		ruleMY008TmpDiskTables(),
		ruleMY009SortMergePasses(),

		// Connection (3)
		ruleMY010ConnectionsPct(),
		ruleMY011ThreadsRunningSpike(),
		ruleMY012AbortedConnects(),

		// Buffer / Memory (3)
		ruleMY013BufferPoolHit(),
		ruleMY014DirtyPages(),
		ruleMY015HistoryListLength(),

		// Replication (3)
		ruleMY016ReplicationLag(),
		ruleMY017SlaveThreadStopped(),
		ruleMY018SemiSyncDegraded(),

		// IO / Redo (3)
		ruleMY019RedoRateSpike(),
		ruleMY020BinlogCacheDisk(),
		ruleMY021DoubleWriteBuffer(),

		// General (4)
		ruleMY022AdaptiveHashIndex(),
		ruleMY023QueryCacheHit(),
		ruleMY024IndexEfficiency(),
		ruleMY025CheckpointAge(),
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Lock / Concurrency (5)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-001: InnoDB 行锁等待冲高
func ruleMY001InnoDBRowLockWaits() *Rule {
	return &Rule{
		ID:       "MY-001",
		Name:     "InnoDB 行锁等待冲高",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_row_lock_waits"},
			{Type: SignalKeyword, Key: "行锁"},
			{Type: SignalKeyword, Key: "row lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_row_lock_waits", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 InnoDB 行锁等待速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_row_lock_waits") },
			Branches: []Branch{
				{
					Label: "行锁等待 > 100/s — 严重争用",
					Match: MatchGT(100),
					Then: &TreeNode{
						Step:  "检查是否存在阻塞链",
						Check: func(ctx *EvalContext) interface{} { return ctx.HasBlockingChains() },
						Branches: []Branch{
							{
								Label:    "存在阻塞链 — 大事务持锁不释放",
								Match:    MatchBool(true),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "InnoDB 行锁等待 > 100/s，存在阻塞链"},
									{Desc: "大事务持锁不释放，导致后续事务排队等待"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看阻塞链详情，定位持锁事务",
										SkillCommand: "/sql \"SELECT r.trx_id waiting_trx, r.trx_mysql_thread_id waiting_thread, b.trx_id blocking_trx, b.trx_mysql_thread_id blocking_thread, b.trx_query blocking_query FROM information_schema.innodb_lock_waits w JOIN information_schema.innodb_trx b ON b.trx_id=w.blocking_trx_id JOIN information_schema.innodb_trx r ON r.trx_id=w.requesting_trx_id\"",
										RawSQL:       "SELECT r.trx_id waiting_trx, r.trx_mysql_thread_id waiting_thread, b.trx_id blocking_trx, b.trx_mysql_thread_id blocking_thread, b.trx_query blocking_query FROM information_schema.innodb_lock_waits w JOIN information_schema.innodb_trx b ON b.trx_id=w.blocking_trx_id JOIN information_schema.innodb_trx r ON r.trx_id=w.requesting_trx_id",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "终止持锁时间过长的事务",
										SkillCommand: "/kill {blocking_thread_id}",
										RawSQL:       "KILL {blocking_thread_id}",
										Risk: "终止事务会回滚未提交的数据", Rollback: "应用重新发起事务"},
								},
							},
							{
								Label:    "无阻塞链 — 高并发热点行更新",
								Match:    MatchBool(false),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "InnoDB 行锁等待 > 100/s，无明显阻塞链"},
									{Desc: "多个事务并发更新同一热点行，导致频繁锁等待"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 Top SQL 定位热点更新语句",
										SkillCommand: "/topsql",
										RawSQL:       "SELECT DIGEST_TEXT, COUNT_STAR, SUM_LOCK_TIME/1e12 lock_time_s FROM performance_schema.events_statements_summary_by_digest ORDER BY SUM_LOCK_TIME DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "拆分热点行或减少事务粒度",
										SkillCommand: "",
										RawSQL:       "",
										Risk: "需要业务层改造", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label: "行锁等待 10-100/s — 中度争用",
					Match: MatchBetween(10, 100),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "InnoDB 行锁等待 10-100/s，存在中度行锁争用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 InnoDB 锁等待状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS",
							Risk: "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "优化事务，缩短持锁时间，避免长事务",
							SkillCommand: "",
							RawSQL:       "",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-004", "MY-006"},
		Tags:     []string{"lock", "innodb", "row_lock"},
		Versions: "5.5+",
	}
}

// MY-002: 元数据锁 (MDL) 阻塞
func ruleMY002MetadataLock() *Rule {
	return &Rule{
		ID:       "MY-002",
		Name:     "元数据锁 (MDL) 阻塞",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalKeyword, Key: "metadata lock"},
			{Type: SignalKeyword, Key: "MDL"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 0},
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "慢查询 > 5 — 长事务持有 MDL 读锁",
					Match: MatchGT(5),
					Then: &TreeNode{
						Step:  "检查锁等待数量",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
						Branches: []Branch{
							{
								Label:    "lock_waits > 20 — MDL 大面积阻塞",
								Match:    MatchGT(20),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "长事务持有 MDL 读锁，DDL 操作等待 MDL 写锁"},
									{Desc: "DDL 等待导致后续所有 DML 排队，锁等待 > 20"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找持有 MDL 锁的会话",
										SkillCommand: "/sql \"SELECT * FROM performance_schema.metadata_locks WHERE LOCK_STATUS='GRANTED' ORDER BY LOCK_DURATION DESC\"",
										RawSQL:       "SELECT * FROM performance_schema.metadata_locks WHERE LOCK_STATUS='GRANTED' ORDER BY LOCK_DURATION DESC",
										Risk: "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "终止持有 MDL 过久的会话或取消 DDL",
										SkillCommand: "/kill {thread_id}",
										RawSQL:       "KILL {thread_id}",
										Risk: "终止会话会回滚事务", Rollback: "重新执行被终止的操作"},
								},
							},
							{
								Label:    "lock_waits 5-20 — MDL 轻度阻塞",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在长事务与 DDL 竞争 MDL 锁，锁等待 5-20"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否有 DDL 操作在等待 MDL",
										SkillCommand: "/sql \"SHOW PROCESSLIST\"",
										RawSQL:       "SELECT * FROM information_schema.processlist WHERE STATE LIKE '%Waiting for table metadata lock%'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "慢查询 1-5 — 潜在 MDL 风险",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在慢查询，锁等待冲高，可能有 MDL 竞争"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 MDL 锁状态",
							SkillCommand: "/sql \"SELECT * FROM performance_schema.metadata_locks\"",
							RawSQL:       "SELECT * FROM performance_schema.metadata_locks",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{"MY-010"},
		Tags:     []string{"lock", "mdl", "ddl"},
		Versions: "5.7+",
	}
}

// MY-003: 死锁频繁
func ruleMY003Deadlocks() *Rule {
	return &Rule{
		ID:       "MY-003",
		Name:     "死锁频繁",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "deadlocks"},
			{Type: SignalErrorCode, Key: "1213"},
			{Type: SignalKeyword, Key: "deadlock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "deadlocks", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查死锁频率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("deadlocks") },
			Branches: []Branch{
				{
					Label: "死锁 > 5/s — 高频死锁",
					Match: MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "死锁频率 > 5/s，业务事务设计存在严重问题"},
						{Desc: "频繁死锁会导致事务回滚，应用吞吐量下降"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看最近一次死锁详情",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 LATEST DETECTED DEADLOCK 部分",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "统一事务内表访问顺序，减少死锁概率",
							SkillCommand: "",
							RawSQL:       "",
							Risk: "需要业务层改造", Rollback: "无"},
						{Type: ActionFix, Desc: "缩短事务时间，减少持锁窗口",
							SkillCommand: "",
							RawSQL:       "",
							Risk: "需要业务层改造", Rollback: "无"},
					},
				},
				{
					Label: "死锁 > 0 — 存在死锁",
					Match: MatchGT(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "检测到死锁事件，应关注死锁涉及的表和索引"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看死锁日志定位原因",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 LATEST DETECTED DEADLOCK 部分",
							Risk: "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "开启 innodb_print_all_deadlocks 记录所有死锁到 error log",
							SkillCommand: "/sql \"SET GLOBAL innodb_print_all_deadlocks=ON\"",
							RawSQL:       "SET GLOBAL innodb_print_all_deadlocks=ON",
							Risk: "增加少量日志写入", Rollback: "SET GLOBAL innodb_print_all_deadlocks=OFF"},
					},
				},
			},
		},
		CausedBy: []string{"MY-001"},
		CausesOf: []string{},
		Tags:     []string{"lock", "deadlock", "innodb"},
		Versions: "5.5+",
	}
}

// MY-004: 锁等待持续冲高
func ruleMY004LockWaitTimeout() *Rule {
	return &Rule{
		ID:       "MY-004",
		Name:     "锁等待持续冲高",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalErrorCode, Key: "1205"},
			{Type: SignalKeyword, Key: "lock wait"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 10},
			},
			SkipWhen: []SkipCondition{
				{Desc: "行锁等待已冲高则由 MY-001 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("innodb_row_lock_waits") > 100
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待数量趋势",
			Check: func(ctx *EvalContext) interface{} {
				m, ok := ctx.GetMetric("lock_waits")
				if !ok {
					return 0.0
				}
				return m.Max
			},
			Branches: []Branch{
				{
					Label: "锁等待峰值 > 50 — 大面积锁等待",
					Match: MatchGT(50),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "锁等待峰值 > 50，大面积事务排队"},
						{Desc: "可能存在长事务、DDL 或大批量 DML 导致级联锁等待"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看当前所有锁等待",
							SkillCommand: "/sql \"SELECT * FROM information_schema.innodb_trx WHERE trx_state='LOCK WAIT'\"",
							RawSQL:       "SELECT * FROM information_schema.innodb_trx WHERE trx_state='LOCK WAIT'",
							Risk: "无", Rollback: "无"},
						{Type: ActionUrgent, Desc: "检查并终止超长事务",
							SkillCommand: "/sql \"SELECT trx_id, trx_started, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10\"",
							RawSQL:       "SELECT trx_id, trx_started, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "锁等待 10-50 — 中度锁等待",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "锁等待持续处于 10-50 区间，需定位持锁来源"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 InnoDB 状态定位锁信息",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-001", "MY-002"},
		CausesOf: []string{"MY-011"},
		Tags:     []string{"lock", "wait", "timeout"},
		Versions: "5.5+",
	}
}

// MY-005: 表锁争用
func ruleMY005TableLocksWaited() *Rule {
	return &Rule{
		ID:       "MY-005",
		Name:     "表锁争用",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "table_locks_waited"},
			{Type: SignalKeyword, Key: "table lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "table_locks_waited", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查表锁等待速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("table_locks_waited") },
			Branches: []Branch{
				{
					Label: "表锁等待 > 50/s — 严重表锁争用",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "表锁等待 > 50/s，可能使用了 MyISAM 表或显式 LOCK TABLES"},
						{Desc: "MyISAM 只有表级锁，高并发下会严重影响性能"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查找使用 MyISAM 引擎的表",
							SkillCommand: "/sql \"SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE='MyISAM' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')\"",
							RawSQL:       "SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE FROM information_schema.TABLES WHERE ENGINE='MyISAM' AND TABLE_SCHEMA NOT IN ('mysql','information_schema','performance_schema','sys')",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "将 MyISAM 表转为 InnoDB",
							SkillCommand: "/sql \"ALTER TABLE {db}.{table} ENGINE=InnoDB\"",
							RawSQL:       "ALTER TABLE {db}.{table} ENGINE=InnoDB",
							Risk: "DDL 操作期间会锁表", Rollback: "ALTER TABLE {db}.{table} ENGINE=MyISAM"},
					},
				},
				{
					Label:    "表锁等待 5-50/s — 轻度表锁争用",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在少量表锁等待，检查是否有 LOCK TABLES 语句或 MyISAM 表"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否存在 LOCK TABLES 操作",
							SkillCommand: "/sql \"SHOW PROCESSLIST\"",
							RawSQL:       "SELECT * FROM information_schema.processlist WHERE STATE LIKE '%Table lock%' OR STATE LIKE '%Locked%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-004"},
		Tags:     []string{"lock", "table_lock", "myisam"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Slow Query (4)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-006: 慢查询冲高
func ruleMY006SlowQueries() *Rule {
	return &Rule{
		ID:       "MY-006",
		Name:     "慢查询冲高",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalKeyword, Key: "慢查询"},
			{Type: SignalKeyword, Key: "slow query"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "慢查询 > 20 — 大量慢查询",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step:  "检查是否有 Top SQL 并发冲高",
						Check: func(ctx *EvalContext) interface{} {
							if len(ctx.TopSQLs) == 0 {
								return 0.0
							}
							return float64(ctx.TopSQLs[0].MaxConcurrent)
						},
						Branches: []Branch{
							{
								Label:    "Top SQL 并发 > 10 — 单条 SQL 并发冲高",
								Match:    MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "慢查询 > 20，且 Top SQL 并发 > 10"},
									{Desc: "单条 SQL 高并发执行且执行缓慢，可能缺少索引或统计信息过期"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看 Top SQL 执行计划",
										SkillCommand: "/topsql",
										RawSQL:       "SELECT DIGEST_TEXT, COUNT_STAR, AVG_TIMER_WAIT/1e12 avg_sec FROM performance_schema.events_statements_summary_by_digest ORDER BY AVG_TIMER_WAIT DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "为慢查询添加索引或优化 SQL",
										SkillCommand: "/explain {digest}",
										RawSQL:       "EXPLAIN FORMAT=TREE {sql_text}",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "多条不同慢查询",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "慢查询 > 20，多条不同 SQL 同时变慢"},
									{Desc: "可能是整体资源不足（CPU/IO/锁）导致的系统性变慢"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查系统资源是否有瓶颈",
										SkillCommand: "/health",
										RawSQL:       "SHOW GLOBAL STATUS LIKE 'Threads_running'",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "慢查询 3-20 — 中度慢查询",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "慢查询数 3-20，需关注具体慢查询语句"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看慢查询详情",
							SkillCommand: "/topsql",
							RawSQL:       "SELECT DIGEST_TEXT, COUNT_STAR, AVG_TIMER_WAIT/1e12 avg_sec, SUM_ROWS_EXAMINED FROM performance_schema.events_statements_summary_by_digest ORDER BY AVG_TIMER_WAIT DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-001", "MY-002", "MY-011"},
		Tags:     []string{"slow_query", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-007: 全表扫描过多
func ruleMY007FullTableScan() *Rule {
	return &Rule{
		ID:       "MY-007",
		Name:     "全表扫描过多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_rnd_next"},
			{Type: SignalKeyword, Key: "full table scan"},
			{Type: SignalKeyword, Key: "全表扫描"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_rnd_next", Op: OpGT, Value: 100000},
			},
			SkipWhen: []SkipCondition{
				{Desc: "空闲连接堆积已由 MY-081 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("sleep_sessions") > 30
				}},
				{Desc: "连接失败已由 MY-012 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("aborted_connects") > 20
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查全表扫描行数速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("handler_read_rnd_next") },
			Branches: []Branch{
				{
					Label: "> 1M/s — 大量全表扫描",
					Match: MatchGT(1000000),
					Then: &TreeNode{
						Step:  "检查 Buffer Pool 命中率是否受影响",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
						Branches: []Branch{
							{
								Label:    "命中率 < 95% — 全表扫描导致缓存污染",
								Match:    MatchLT(95),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Handler_read_rnd_next > 1M/s，大量全表扫描"},
									{Desc: "Buffer Pool 命中率 < 95%，全表扫描冲刷了缓存"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位全表扫描的 SQL",
										SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10\"",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "为频繁全表扫描的 SQL 添加索引",
										SkillCommand: "/explain {digest}",
										RawSQL:       "EXPLAIN FORMAT=TREE {sql_text}",
										Risk: "创建索引时可能锁表", Rollback: "DROP INDEX {index_name} ON {table}"},
								},
							},
							{
								Label:    "缓存尚可但全表扫描量大 — 需优化 SQL",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Handler_read_rnd_next > 1M/s，大量全表扫描"},
									{Desc: "虽然缓存命中率正常，但全扫消耗 CPU 且阻碍索引使用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位全表扫描的 SQL 并优化",
										SkillCommand: "/topsql",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "为高频全扫 SQL 添加索引",
										RawSQL: "-- EXPLAIN FORMAT=TREE {sql_text}\n-- CREATE INDEX idx_xxx ON table(col)",
										Risk: "创建索引期间可能锁表", Rollback: "DROP INDEX"},
								},
							},
						},
					},
				},
				{
					Label:    "100K-1M/s — 中度全表扫描",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Handler_read_rnd_next 100K-1M/s，存在不少全表扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查未使用索引的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_NO_INDEX_USED FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY SUM_NO_INDEX_USED DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY SUM_NO_INDEX_USED DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-013"},
		Tags:     []string{"full_scan", "index", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-008: 临时表磁盘溢出
func ruleMY008TmpDiskTables() *Rule {
	return &Rule{
		ID:       "MY-008",
		Name:     "临时表磁盘溢出",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "tmp_disk_tables_pct"},
			{Type: SignalKeyword, Key: "临时表"},
			{Type: SignalKeyword, Key: "tmp disk"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "tmp_disk_tables_pct", Op: OpGT, Value: 25},
			},
			SkipWhen: []SkipCondition{
				{Desc: "锁等待已由锁规则处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("lock_waits") > 10 || ctx.MetricValue("innodb_row_lock_waits") > 50
				}},
				{Desc: "活跃线程极高已由 MY-011/MY-082 处理", Check: func(ctx *EvalContext) bool {
					return ctx.MetricValue("threads_running") > 20
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查磁盘临时表比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("tmp_disk_tables_pct") },
			Branches: []Branch{
				{
					Label: "> 50% — 大量临时表溢出到磁盘",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "超过 50% 的临时表溢出到磁盘，I/O 开销大"},
						{Desc: "常见原因：SQL 使用 TEXT/BLOB 列、GROUP BY/ORDER BY 结果集过大、tmp_table_size 过小"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 tmp_table_size 和 max_heap_table_size",
							SkillCommand: "/sql \"SET GLOBAL tmp_table_size=128*1024*1024; SET GLOBAL max_heap_table_size=128*1024*1024\"",
							RawSQL:       "SET GLOBAL tmp_table_size=134217728; SET GLOBAL max_heap_table_size=134217728",
							Risk: "增加内存使用", Rollback: "SET GLOBAL tmp_table_size=16777216; SET GLOBAL max_heap_table_size=16777216"},
						{Type: ActionInvestigate, Desc: "查找产生磁盘临时表的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_CREATED_TMP_DISK_TABLES, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_CREATED_TMP_DISK_TABLES > 0 ORDER BY SUM_CREATED_TMP_DISK_TABLES DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_CREATED_TMP_DISK_TABLES, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_CREATED_TMP_DISK_TABLES > 0 ORDER BY SUM_CREATED_TMP_DISK_TABLES DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "25-50% — 中度磁盘临时表",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "磁盘临时表比例 25-50%，需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看临时表相关参数和 SQL",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Created_tmp%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Created_tmp%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{},
		Tags:     []string{"tmp_table", "disk", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-009: 排序磁盘溢出
func ruleMY009SortMergePasses() *Rule {
	return &Rule{
		ID:       "MY-009",
		Name:     "排序磁盘溢出",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "sort_merge_passes"},
			{Type: SignalKeyword, Key: "sort"},
			{Type: SignalKeyword, Key: "排序"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "sort_merge_passes", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Sort_merge_passes 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("sort_merge_passes") },
			Branches: []Branch{
				{
					Label: "> 100/s — 大量排序溢出磁盘",
					Match: MatchGT(100),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Sort_merge_passes > 100/s，大量排序操作溢出到磁盘"},
						{Desc: "sort_buffer_size 可能不足，或 SQL 排序结果集过大"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 sort_buffer_size（会话级别）",
							SkillCommand: "/sql \"SET GLOBAL sort_buffer_size=4*1024*1024\"",
							RawSQL:       "SET GLOBAL sort_buffer_size=4194304",
							Risk: "每个连接额外分配排序缓存", Rollback: "SET GLOBAL sort_buffer_size=262144"},
						{Type: ActionInvestigate, Desc: "查找产生大量排序的 SQL",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_SORT_MERGE_PASSES, SUM_SORT_ROWS FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_MERGE_PASSES > 0 ORDER BY SUM_SORT_MERGE_PASSES DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_SORT_MERGE_PASSES, SUM_SORT_ROWS FROM performance_schema.events_statements_summary_by_digest WHERE SUM_SORT_MERGE_PASSES > 0 ORDER BY SUM_SORT_MERGE_PASSES DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "10-100/s — 中度排序溢出",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Sort_merge_passes 10-100/s，部分排序溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看排序相关状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Sort%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Sort%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-006"},
		CausesOf: []string{},
		Tags:     []string{"sort", "disk", "sql_perf"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Connection (3)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-010: 连接数冲高
func ruleMY010ConnectionsPct() *Rule {
	return &Rule{
		ID:       "MY-010",
		Name:     "连接数冲高",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalKeyword, Key: "连接数"},
			{Type: SignalKeyword, Key: "max_connections"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查连接使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label: "> 95% — 即将耗尽连接",
					Match: MatchGT(95),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "连接使用率 > 95%，即将达到 max_connections 上限"},
						{Desc: "新连接将被拒绝，应用会报 Too many connections 错误"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "临时增大 max_connections",
							SkillCommand: "/sql \"SET GLOBAL max_connections=500\"",
							RawSQL:       "SET GLOBAL max_connections=500",
							Risk: "增加内存使用", Rollback: "SET GLOBAL max_connections=原值"},
						{Type: ActionUrgent, Desc: "清理空闲连接",
							SkillCommand: "/sql \"SELECT id, user, host, db, time, state FROM information_schema.processlist WHERE command='Sleep' AND time > 300 ORDER BY time DESC\"",
							RawSQL:       "SELECT id, user, host, db, time, state FROM information_schema.processlist WHERE command='Sleep' AND time > 300 ORDER BY time DESC",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "减小 wait_timeout 清理长空闲连接",
							SkillCommand: "/sql \"SET GLOBAL wait_timeout=300\"",
							RawSQL:       "SET GLOBAL wait_timeout=300",
							Risk: "短连接应用可能受影响", Rollback: "SET GLOBAL wait_timeout=28800"},
					},
				},
				{
					Label:    "80-95% — 连接数偏高",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "连接使用率 80-95%，需关注连接增长趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析连接来源分布",
							SkillCommand: "/sql \"SELECT user, host, COUNT(*) cnt FROM information_schema.processlist GROUP BY user, host ORDER BY cnt DESC\"",
							RawSQL:       "SELECT user, host, COUNT(*) cnt FROM information_schema.processlist GROUP BY user, host ORDER BY cnt DESC",
							Risk: "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "检查应用是否使用了连接池",
							SkillCommand: "",
							RawSQL:       "",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-002", "MY-006"},
		CausesOf: []string{},
		Tags:     []string{"connection", "max_connections", "session"},
		Versions: "5.5+",
	}
}

// MY-011: 连接风暴 — threads_running spike
func ruleMY011ThreadsRunningSpike() *Rule {
	return &Rule{
		ID:       "MY-011",
		Name:     "并发线程冲高（连接风暴）",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "threads_running"},
			{Type: SignalKeyword, Key: "threads running"},
			{Type: SignalKeyword, Key: "连接风暴"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "threads_running", Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Threads_running 峰值",
			Check: func(ctx *EvalContext) interface{} {
				m, ok := ctx.GetMetric("threads_running")
				if !ok {
					return 0.0
				}
				return m.Max
			},
			Branches: []Branch{
				{
					Label: "> 100 — 严重连接风暴",
					Match: MatchGT(100),
					Then: &TreeNode{
						Step:  "检查是否有锁等待导致堆积",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
						Branches: []Branch{
							{
								Label:    "锁等待 > 10 — 锁导致线程堆积",
								Match:    MatchGT(10),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Threads_running > 100，同时锁等待冲高"},
									{Desc: "锁等待导致大量线程堆积，形成连接风暴"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "优先解决锁等待问题",
										SkillCommand: "/sql \"SELECT * FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10\"",
										RawSQL:       "SELECT * FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "无明显锁等待 — 慢查询或资源不足",
								Match:    MatchDefault(),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Threads_running > 100，无明显锁等待"},
									{Desc: "可能是大量慢查询或 CPU/IO 资源耗尽"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看当前活跃会话",
										SkillCommand: "/activesessions",
										RawSQL:       "SELECT id, user, host, db, command, time, state, LEFT(info,100) query FROM information_schema.processlist WHERE command != 'Sleep' ORDER BY time DESC",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "30-100 — 并发线程偏高",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Threads_running 30-100，并发线程偏高需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析活跃线程在做什么",
							SkillCommand: "/activesessions",
							RawSQL:       "SELECT command, state, COUNT(*) cnt FROM information_schema.processlist WHERE command != 'Sleep' GROUP BY command, state ORDER BY cnt DESC",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-001", "MY-004", "MY-006"},
		CausesOf: []string{"MY-010"},
		Tags:     []string{"threads_running", "connection_storm", "session"},
		Versions: "5.5+",
	}
}

// MY-012: Aborted 连接冲高
func ruleMY012AbortedConnects() *Rule {
	return &Rule{
		ID:       "MY-012",
		Name:     "Aborted 连接冲高",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "aborted_connects"},
			{Type: SignalKeyword, Key: "aborted"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "aborted_connects", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Aborted_connects 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("aborted_connects") },
			Branches: []Branch{
				{
					Label: "> 50/s — 大量连接被拒绝",
					Match: MatchGT(50),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Aborted_connects > 50/s，大量连接尝试被拒绝"},
						{Desc: "可能原因：密码错误、权限不足、max_connections 耗尽、网络中断"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 error log 中的连接失败信息",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Aborted%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Aborted%'",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查连接使用率是否接近上限",
							SkillCommand: "/sql \"SELECT @@max_connections max_conn, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') current_conn\"",
							RawSQL:       "SELECT @@max_connections max_conn, (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') current_conn",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "10-50/s — 中度连接拒绝",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Aborted_connects 10-50/s，存在异常连接尝试"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 error log 定位失败原因",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Aborted%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Aborted%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-010"},
		CausesOf: []string{},
		Tags:     []string{"connection", "aborted", "auth"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Buffer / Memory (3)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-013: Buffer Pool 命中率低
func ruleMY013BufferPoolHit() *Rule {
	return &Rule{
		ID:       "MY-013",
		Name:     "Buffer Pool 命中率低",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "buffer_pool_hit_pct"},
			{Type: SignalKeyword, Key: "buffer pool"},
			{Type: SignalKeyword, Key: "缓冲池"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "buffer_pool_hit_pct", Op: OpLT, Value: 95},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Buffer Pool 命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("buffer_pool_hit_pct") },
			Branches: []Branch{
				{
					Label: "< 80% — Buffer Pool 命中率严重不足",
					Match: MatchLT(80),
					Then: &TreeNode{
						Step:  "检查是否有大量全表扫描",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("handler_read_rnd_next") },
						Branches: []Branch{
							{
								Label:    "全表扫描冲高 — 扫描冲刷缓存",
								Match:    MatchGT(500000),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Buffer Pool 命中率 < 80%，全表扫描 > 500K/s"},
									{Desc: "全表扫描将大量冷数据读入 Buffer Pool，挤出热数据"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位并优化全表扫描的 SQL",
										SkillCommand: "/topsql",
										RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增大 innodb_buffer_pool_size",
										SkillCommand: "/sql \"SET GLOBAL innodb_buffer_pool_size=物理内存的60-80%\"",
										RawSQL:       "SET GLOBAL innodb_buffer_pool_size=物理内存的60-80%（需要计算后设置具体值）",
										Risk: "增加内存使用", Rollback: "SET GLOBAL innodb_buffer_pool_size=原值"},
								},
							},
							{
								Label:    "全表扫描正常 — Buffer Pool 本身偏小",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Buffer Pool 命中率 < 80%，无明显全表扫描"},
									{Desc: "数据工作集大于 Buffer Pool 容量"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 innodb_buffer_pool_size",
										SkillCommand: "/sql \"SHOW VARIABLES LIKE 'innodb_buffer_pool_size'\"",
										RawSQL:       "SHOW VARIABLES LIKE 'innodb_buffer_pool_size'",
										Risk: "增加内存使用", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "80-95% — Buffer Pool 命中率偏低",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Pool 命中率 80-95%，低于理想水平(>99%)"},
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
		CausedBy: []string{"MY-007"},
		CausesOf: []string{"MY-006"},
		Tags:     []string{"buffer_pool", "memory", "cache"},
		Versions: "5.5+",
	}
}

// MY-014: Buffer Pool 脏页比例高
func ruleMY014DirtyPages() *Rule {
	return &Rule{
		ID:       "MY-014",
		Name:     "Buffer Pool 脏页比例高",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "pages_dirty_pct"},
			{Type: SignalKeyword, Key: "dirty pages"},
			{Type: SignalKeyword, Key: "脏页"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "pages_dirty_pct", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step:  "检查脏页比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("pages_dirty_pct") },
			Branches: []Branch{
				{
					Label: "> 75% — 脏页比例严重偏高",
					Match: MatchGT(75),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Buffer Pool 脏页比例 > 75%，InnoDB 刷脏压力大"},
						{Desc: "写入速率超出磁盘刷新能力，可能导致 checkpoint stall"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 InnoDB 刷脏状态",
							SkillCommand: "/sql \"SELECT name, count FROM information_schema.innodb_metrics WHERE name LIKE '%flush%' OR name LIKE '%checkpoint%'\"",
							RawSQL:       "SELECT name, count FROM information_schema.innodb_metrics WHERE name LIKE '%flush%' OR name LIKE '%checkpoint%'",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "调整 innodb_max_dirty_pages_pct 和 innodb_io_capacity",
							SkillCommand: "/sql \"SET GLOBAL innodb_max_dirty_pages_pct=50; SET GLOBAL innodb_io_capacity=2000\"",
							RawSQL:       "SET GLOBAL innodb_max_dirty_pages_pct=50; SET GLOBAL innodb_io_capacity=2000",
							Risk: "增加 IO 负担", Rollback: "SET GLOBAL innodb_max_dirty_pages_pct=75; SET GLOBAL innodb_io_capacity=200"},
					},
				},
				{
					Label:    "50-75% — 脏页比例偏高",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Pool 脏页比例 50-75%，刷脏压力需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 IO 相关参数",
							SkillCommand: "/sql \"SHOW VARIABLES LIKE 'innodb_io_capacity%'\"",
							RawSQL:       "SHOW VARIABLES LIKE 'innodb_io_capacity%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-019"},
		CausesOf: []string{"MY-025"},
		Tags:     []string{"dirty_pages", "buffer_pool", "flush"},
		Versions: "5.5+",
	}
}

// MY-015: InnoDB History List 过长
func ruleMY015HistoryListLength() *Rule {
	return &Rule{
		ID:       "MY-015",
		Name:     "InnoDB History List 过长",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "history_list_length"},
			{Type: SignalKeyword, Key: "history list"},
			{Type: SignalKeyword, Key: "purge"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "history_list_length", Op: OpGT, Value: 10000},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 History List 长度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("history_list_length") },
			Branches: []Branch{
				{
					Label: "> 100000 — History List 严重积压",
					Match: MatchGT(100000),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "History List > 100000，InnoDB purge 线程清理严重滞后"},
						{Desc: "长事务阻止了 undo log 的回收，导致 undo 表空间持续膨胀"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查找长事务并终止",
							SkillCommand: "/sql \"SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND, trx_started, NOW()) duration_s, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10\"",
							RawSQL:       "SELECT trx_id, trx_started, TIMESTAMPDIFF(SECOND, trx_started, NOW()) duration_s, trx_mysql_thread_id, trx_query FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 purge 线程数加速清理",
							SkillCommand: "/sql \"SET GLOBAL innodb_purge_threads=4\"",
							RawSQL:       "SET GLOBAL innodb_purge_threads=4 -- 需重启生效（5.7可以在线改，8.0需重启）",
							Risk: "增加 CPU 开销", Rollback: "SET GLOBAL innodb_purge_threads=1"},
					},
				},
				{
					Label: "> 10000 — History List 偏长",
					Match: MatchGT(10000),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "History List > 10000，purge 清理速度跟不上写入速度"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否有长事务未提交",
							SkillCommand: "/sql \"SELECT * FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5\"",
							RawSQL:       "SELECT * FROM information_schema.innodb_trx ORDER BY trx_started LIMIT 5",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "未超阈值",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "History List 长度在可接受范围"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-013"},
		Tags:     []string{"history_list", "purge", "undo", "innodb"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Replication (3)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-016: 主从延迟
func ruleMY016ReplicationLag() *Rule {
	return &Rule{
		ID:       "MY-016",
		Name:     "主从延迟冲高",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag"},
			{Type: SignalKeyword, Key: "复制延迟"},
			{Type: SignalKeyword, Key: "replication lag"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag") },
			Branches: []Branch{
				{
					Label: "> 60s — 严重复制延迟",
					Match: MatchGT(60),
					Then: &TreeNode{
						Step:  "检查是否有大事务",
						Check: func(ctx *EvalContext) interface{} {
							if len(ctx.TopSQLs) == 0 {
								return 0.0
							}
							return ctx.TopSQLs[0].AvgLatencyMs
						},
						Branches: []Branch{
							{
								Label:    "存在大事务 — 单线程回放瓶颈",
								Match:    MatchGT(5000),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "复制延迟 > 60s，存在执行时间 > 5s 的大事务"},
									{Desc: "大事务在从库单线程回放，导致后续事务排队"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "开启并行复制减少延迟",
										SkillCommand: "/sql \"SET GLOBAL slave_parallel_workers=8; SET GLOBAL slave_parallel_type='LOGICAL_CLOCK'\"",
										RawSQL:       "SET GLOBAL slave_parallel_workers=8; SET GLOBAL slave_parallel_type='LOGICAL_CLOCK'",
										Risk: "需要重启 SQL 线程", Rollback: "SET GLOBAL slave_parallel_workers=0"},
									{Type: ActionInvestigate, Desc: "检查大事务来源",
										SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
										RawSQL:       "SHOW SLAVE STATUS\\G",
										Risk: "无", Rollback: "无"},
								},
							},
							{
								Label:    "无明显大事务 — 写入量过大",
								Match:    MatchDefault(),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "复制延迟 > 60s，无明显大事务，可能是整体写入量过大"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "开启并行复制",
										SkillCommand: "/sql \"SET GLOBAL slave_parallel_workers=8\"",
										RawSQL:       "SET GLOBAL slave_parallel_workers=8",
										Risk: "需要重启 SQL 线程", Rollback: "SET GLOBAL slave_parallel_workers=0"},
								},
							},
						},
					},
				},
				{
					Label:    "10-60s — 复制延迟偏高",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "复制延迟 10-60s，需关注趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看从库复制状态",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS\\G",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"replication", "lag", "slave"},
		Versions: "5.5+",
	}
}

// MY-017: 复制线程停止
func ruleMY017SlaveThreadStopped() *Rule {
	return &Rule{
		ID:       "MY-017",
		Name:     "复制线程停止",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "slave_sql_running"},
			{Type: SignalKeyword, Key: "slave stopped"},
			{Type: SignalKeyword, Key: "复制停止"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "slave_sql_running", Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制线程状态",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("slave_sql_running") },
			Branches: []Branch{
				{
					Label:    "SQL 线程停止 — 复制中断",
					Match:    MatchLTE(0),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "复制 SQL 线程已停止，从库不再回放数据"},
						{Desc: "可能原因：SQL 冲突、磁盘满、表结构不一致"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看复制错误详情",
							SkillCommand: "/sql \"SHOW SLAVE STATUS\"",
							RawSQL:       "SHOW SLAVE STATUS\\G -- 查看 Last_SQL_Error 字段",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "跳过错误并恢复复制（谨慎使用）",
							SkillCommand: "/sql \"SET GLOBAL sql_slave_skip_counter=1; START SLAVE\"",
							RawSQL:       "SET GLOBAL sql_slave_skip_counter=1; START SLAVE",
							Risk: "跳过事务可能导致数据不一致", Rollback: "STOP SLAVE; 使用 pt-table-checksum 验证数据一致性"},
						{Type: ActionInvestigate, Desc: "使用 pt-table-checksum 验证数据一致性",
							SkillCommand: "",
							RawSQL:       "pt-table-checksum --replicate=percona.checksums h=master_host",
							Risk: "增加主库负载", Rollback: "无"},
					},
				},
				{
					Label:    "SQL 线程运行中",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "复制 SQL 线程正常运行"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-016"},
		Tags:     []string{"replication", "slave", "sql_thread"},
		Versions: "5.5+",
	}
}

// MY-018: 半同步复制降级
func ruleMY018SemiSyncDegraded() *Rule {
	return &Rule{
		ID:       "MY-018",
		Name:     "半同步复制降级",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "semi_sync_status"},
			{Type: SignalKeyword, Key: "semi-sync"},
			{Type: SignalKeyword, Key: "半同步"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "semi_sync_status", Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查半同步状态",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("semi_sync_status") },
			Branches: []Branch{
				{
					Label:    "半同步降级为异步",
					Match:    MatchLTE(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "半同步复制已降级为异步模式"},
						{Desc: "数据安全性降低：主库崩溃时可能丢失最近的事务"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查半同步插件状态",
							SkillCommand: "/sql \"SHOW STATUS LIKE 'Rpl_semi_sync%'\"",
							RawSQL:       "SHOW STATUS LIKE 'Rpl_semi_sync%'",
							Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查从库网络连通性",
							SkillCommand: "/sql \"SHOW SLAVE HOSTS\"",
							RawSQL:       "SHOW SLAVE HOSTS",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "等从库恢复后，半同步会自动恢复",
							SkillCommand: "/sql \"SET GLOBAL rpl_semi_sync_master_timeout=10000\"",
							RawSQL:       "SET GLOBAL rpl_semi_sync_master_timeout=10000",
							Risk: "增大超时会导致主库提交延迟", Rollback: "SET GLOBAL rpl_semi_sync_master_timeout=原值"},
					},
				},
				{
					Label:    "半同步正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "半同步复制状态正常"},
					},
				},
			},
		},
		CausedBy: []string{"MY-016"},
		CausesOf: []string{},
		Tags:     []string{"replication", "semi_sync", "data_safety"},
		Versions: "5.7+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// IO / Redo (3)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-019: Redo 写入冲高
func ruleMY019RedoRateSpike() *Rule {
	return &Rule{
		ID:       "MY-019",
		Name:     "Redo 写入冲高",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "redo_rate"},
			{Type: SignalKeyword, Key: "redo log"},
			{Type: SignalKeyword, Key: "redo"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "redo_rate", Op: OpGT, Value: 50000},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Redo 写入速率 (KB/s)",
			Check: func(ctx *EvalContext) interface{} {
				m, ok := ctx.GetMetric("redo_rate")
				if !ok {
					return 0.0
				}
				return m.Max
			},
			Branches: []Branch{
				{
					Label: "> 200 MB/s — Redo 写入极高",
					Match: MatchGT(200000),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Redo 写入 > 200 MB/s，InnoDB 写入负载极重"},
						{Desc: "可能是大批量 DML、LOAD DATA 或大事务导致"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前活跃的大事务",
							SkillCommand: "/sql \"SELECT trx_id, trx_started, trx_rows_modified FROM information_schema.innodb_trx ORDER BY trx_rows_modified DESC LIMIT 10\"",
							RawSQL:       "SELECT trx_id, trx_started, trx_rows_modified FROM information_schema.innodb_trx ORDER BY trx_rows_modified DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 redo log 文件大小减少 checkpoint 频率",
							SkillCommand: "/sql \"SHOW VARIABLES LIKE 'innodb_log_file_size'\"",
							RawSQL:       "SHOW VARIABLES LIKE 'innodb_log_file_size' -- 建议设为 1-2GB",
							Risk: "需要重启 MySQL（8.0.30+ 可在线修改）", Rollback: "恢复 innodb_log_file_size 原值"},
					},
				},
				{
					Label:    "50-200 MB/s — Redo 写入偏高",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Redo 写入 50-200 MB/s，写入负载偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 InnoDB IO 状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 LOG 部分",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-014", "MY-025"},
		Tags:     []string{"redo_log", "write", "innodb"},
		Versions: "5.5+",
	}
}

// MY-020: Binlog 磁盘缓存使用
func ruleMY020BinlogCacheDisk() *Rule {
	return &Rule{
		ID:       "MY-020",
		Name:     "Binlog 磁盘缓存使用冲高",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "binlog_cache_disk_use"},
			{Type: SignalKeyword, Key: "binlog cache"},
			{Type: SignalKeyword, Key: "binlog"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "binlog_cache_disk_use", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Binlog_cache_disk_use 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("binlog_cache_disk_use") },
			Branches: []Branch{
				{
					Label: "> 100 — 大量 binlog 溢出到磁盘",
					Match: MatchGT(100),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Binlog_cache_disk_use > 100，大量事务的 binlog 溢出到临时文件"},
						{Desc: "binlog_cache_size 不足以容纳大事务的 binlog 数据"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 binlog_cache_size",
							SkillCommand: "/sql \"SET GLOBAL binlog_cache_size=4*1024*1024\"",
							RawSQL:       "SET GLOBAL binlog_cache_size=4194304",
							Risk: "每个连接增加缓存内存", Rollback: "SET GLOBAL binlog_cache_size=32768"},
						{Type: ActionInvestigate, Desc: "查看 binlog 缓存状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Binlog_cache%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Binlog_cache%'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "10-100 — 少量 binlog 溢出",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Binlog_cache_disk_use 10-100，少量大事务溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 binlog 缓存相关状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Binlog_cache%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Binlog_cache%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-016"},
		Tags:     []string{"binlog", "cache", "disk"},
		Versions: "5.5+",
	}
}

// MY-021: InnoDB 双写缓冲争用
func ruleMY021DoubleWriteBuffer() *Rule {
	return &Rule{
		ID:       "MY-021",
		Name:     "InnoDB 双写缓冲写入冲高",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "innodb_dblwr_writes"},
			{Type: SignalKeyword, Key: "doublewrite"},
			{Type: SignalKeyword, Key: "双写"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "innodb_dblwr_writes", Op: OpGT, Value: 200},
			},
		},
		Tree: &TreeNode{
			Step:  "检查双写缓冲写入速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("innodb_dblwr_writes") },
			Branches: []Branch{
				{
					Label: "> 1000/s — 双写缓冲写入极高",
					Match: MatchGT(1000),
					Then: &TreeNode{
						Step:  "检查脏页比例是否也高",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("pages_dirty_pct") },
						Branches: []Branch{
							{
								Label:    "脏页 > 50% — 大量刷脏触发双写",
								Match:    MatchGT(50),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "innodb_dblwr_writes > 1000/s，脏页比例 > 50%"},
									{Desc: "大量刷脏页操作触发频繁双写，IO 放大约 2 倍"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查存储是否支持原子写（SSD）",
										SkillCommand: "/sql \"SHOW VARIABLES LIKE 'innodb_doublewrite'\"",
										RawSQL:       "SHOW VARIABLES LIKE 'innodb_doublewrite'",
										Risk: "无", Rollback: "无"},
									{Type: ActionFix, Desc: "如使用支持原子写的存储（如 FusionIO），可关闭双写",
										SkillCommand: "/sql \"SET GLOBAL innodb_doublewrite=OFF\"",
										RawSQL:       "SET GLOBAL innodb_doublewrite=OFF -- 需确认存储支持原子写",
										Risk: "关闭双写后部分写故障时可能损坏数据页", Rollback: "SET GLOBAL innodb_doublewrite=ON"},
								},
							},
							{
								Label:    "脏页正常 — 频繁小批量刷脏",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "innodb_dblwr_writes 偏高，但脏页比例正常"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看 IO 吞吐是否有瓶颈",
										SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
										RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 FILE I/O 部分",
										Risk: "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "200-1000/s — 双写缓冲中度写入",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "innodb_dblwr_writes 200-1000/s，属于正常偏高"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控双写写入趋势",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Innodb_dblwr%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Innodb_dblwr%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"MY-014"},
		CausesOf: []string{},
		Tags:     []string{"doublewrite", "innodb", "io"},
		Versions: "5.5+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// General (4)
// ═══════════════════════════════════════════════════════════════════════════════

// MY-022: InnoDB 自适应哈希索引效率低
func ruleMY022AdaptiveHashIndex() *Rule {
	return &Rule{
		ID:       "MY-022",
		Name:     "InnoDB 自适应哈希索引效率低",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "adaptive_hash_searches_hit_pct"},
			{Type: SignalKeyword, Key: "adaptive hash"},
			{Type: SignalKeyword, Key: "AHI"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "adaptive_hash_searches_hit_pct", Op: OpLT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 AHI 命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("adaptive_hash_searches_hit_pct") },
			Branches: []Branch{
				{
					Label: "< 10% — AHI 几乎无效",
					Match: MatchLT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "AHI 命中率 < 10%，自适应哈希索引对当前负载无效"},
						{Desc: "AHI 消耗内存但不提供收益，关闭可释放内存和减少争用"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "关闭自适应哈希索引",
							SkillCommand: "/sql \"SET GLOBAL innodb_adaptive_hash_index=OFF\"",
							RawSQL:       "SET GLOBAL innodb_adaptive_hash_index=OFF",
							Risk: "部分点查可能变慢", Rollback: "SET GLOBAL innodb_adaptive_hash_index=ON"},
						{Type: ActionInvestigate, Desc: "查看 AHI 使用统计",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 INSERT BUFFER AND ADAPTIVE HASH INDEX 部分",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "10-30% — AHI 效率偏低",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "AHI 命中率 10-30%，效率偏低，可考虑关闭"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "评估 AHI 对负载的影响",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"ahi", "adaptive_hash", "innodb", "memory"},
		Versions: "5.5+",
	}
}

// MY-023: 查询缓存命中率低 (5.7)
func ruleMY023QueryCacheHit() *Rule {
	return &Rule{
		ID:       "MY-023",
		Name:     "查询缓存命中率低",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "qcache_hit_pct"},
			{Type: SignalKeyword, Key: "query cache"},
			{Type: SignalKeyword, Key: "查询缓存"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "qcache_hit_pct", Op: OpLT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Query Cache 命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("qcache_hit_pct") },
			Branches: []Branch{
				{
					Label: "< 10% — Query Cache 几乎无效",
					Match: MatchLT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Query Cache 命中率 < 10%，对性能无正面作用"},
						{Desc: "Query Cache 的全局互斥锁反而会降低高并发性能"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "关闭 Query Cache（强烈建议）",
							SkillCommand: "/sql \"SET GLOBAL query_cache_type=OFF; SET GLOBAL query_cache_size=0\"",
							RawSQL:       "SET GLOBAL query_cache_type=OFF; SET GLOBAL query_cache_size=0",
							Risk: "依赖 QC 的慢查询可能变慢", Rollback: "SET GLOBAL query_cache_type=ON; SET GLOBAL query_cache_size=原值"},
						{Type: ActionPrevent, Desc: "升级到 MySQL 8.0（Query Cache 已移除）",
							SkillCommand: "",
							RawSQL:       "",
							Risk: "需要评估兼容性", Rollback: "无"},
					},
				},
				{
					Label:    "10-30% — Query Cache 效率偏低",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Query Cache 命中率 10-30%，效率偏低"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 Query Cache 状态",
							SkillCommand: "/sql \"SHOW STATUS LIKE 'Qcache%'\"",
							RawSQL:       "SHOW STATUS LIKE 'Qcache%'",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"query_cache", "memory"},
		Versions: "5.7",
	}
}

// MY-024: 索引使用效率低
func ruleMY024IndexEfficiency() *Rule {
	return &Rule{
		ID:       "MY-024",
		Name:     "索引使用效率低",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "handler_read_rnd"},
			{Type: SignalMetric, Key: "handler_read_key"},
			{Type: SignalKeyword, Key: "索引效率"},
			{Type: SignalKeyword, Key: "index efficiency"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "handler_read_rnd", Op: OpGT, Value: 10000},
			},
		},
		Tree: &TreeNode{
			Step: "计算 handler_read_rnd / handler_read_key 比值",
			Check: func(ctx *EvalContext) interface{} {
				rnd := ctx.MetricValue("handler_read_rnd")
				key := ctx.MetricValue("handler_read_key")
				if key < 1 {
					return rnd
				}
				return rnd / key
			},
			Branches: []Branch{
				{
					Label: "比值 > 1.0 — 随机读远多于索引读",
					Match: MatchGT(1.0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Handler_read_rnd 与 Handler_read_key 的比值 > 1.0"},
						{Desc: "大量查询使用非索引路径或排序后回表，索引使用效率低"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查未使用索引的查询",
							SkillCommand: "/sql \"SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, SUM_NO_GOOD_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 OR SUM_NO_GOOD_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10\"",
							RawSQL:       "SELECT DIGEST_TEXT, SUM_NO_INDEX_USED, SUM_NO_GOOD_INDEX_USED, COUNT_STAR FROM performance_schema.events_statements_summary_by_digest WHERE SUM_NO_INDEX_USED > 0 OR SUM_NO_GOOD_INDEX_USED > 0 ORDER BY COUNT_STAR DESC LIMIT 10",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "为高频查询添加合适的索引",
							SkillCommand: "/explain {digest}",
							RawSQL:       "EXPLAIN FORMAT=TREE {sql_text}",
							Risk: "创建索引期间可能锁表", Rollback: "DROP INDEX {index_name} ON {table}"},
					},
				},
				{
					Label: "比值 0.3-1.0 — 索引效率中等",
					Match: MatchGT(0.3),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "索引使用效率中等，存在优化空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 Handler 状态",
							SkillCommand: "/sql \"SHOW GLOBAL STATUS LIKE 'Handler%'\"",
							RawSQL:       "SHOW GLOBAL STATUS LIKE 'Handler%'",
							Risk: "无", Rollback: "无"},
					},
				},
				{
					Label:    "比值 < 0.3 — 索引效率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "索引使用效率处于正常范围"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"MY-006", "MY-007"},
		Tags:     []string{"index", "handler", "sql_perf"},
		Versions: "5.5+",
	}
}

// MY-025: InnoDB Checkpoint Age 冲高
func ruleMY025CheckpointAge() *Rule {
	return &Rule{
		ID:       "MY-025",
		Name:     "InnoDB Checkpoint Age 冲高",
		Category: "innodb",
		Signals: []Signal{
			{Type: SignalMetric, Key: "checkpoint_age_pct"},
			{Type: SignalKeyword, Key: "checkpoint"},
			{Type: SignalKeyword, Key: "redo"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "checkpoint_age_pct", Op: OpGT, Value: 75},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 Checkpoint Age 占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoint_age_pct") },
			Branches: []Branch{
				{
					Label: "> 90% — 即将触发同步刷脏",
					Match: MatchGT(90),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Checkpoint Age > 90% 的 redo log 总容量"},
						{Desc: "InnoDB 即将进入同步刷脏模式，所有写入将被阻塞"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查当前 redo log 使用情况",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 LOG 部分的 checkpoint age",
							Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 innodb_log_file_size 扩大 redo log 空间",
							SkillCommand: "/sql \"SHOW VARIABLES LIKE 'innodb_log_file_size'\"",
							RawSQL:       "SHOW VARIABLES LIKE 'innodb_log_file_size' -- 建议增大到 1-4GB",
							Risk: "需要重启 MySQL（8.0.30+ 可在线修改）", Rollback: "恢复原值"},
						{Type: ActionFix, Desc: "增大 innodb_io_capacity 加速刷脏",
							SkillCommand: "/sql \"SET GLOBAL innodb_io_capacity=4000; SET GLOBAL innodb_io_capacity_max=8000\"",
							RawSQL:       "SET GLOBAL innodb_io_capacity=4000; SET GLOBAL innodb_io_capacity_max=8000",
							Risk: "增加磁盘 IO 负担", Rollback: "SET GLOBAL innodb_io_capacity=200; SET GLOBAL innodb_io_capacity_max=2000"},
					},
				},
				{
					Label:    "75-90% — Checkpoint Age 偏高",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Checkpoint Age 占 redo log 容量的 75-90%，接近刷脏阈值"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 redo log 和刷脏状态",
							SkillCommand: "/sql \"SHOW ENGINE INNODB STATUS\"",
							RawSQL:       "SHOW ENGINE INNODB STATUS -- 查看 LOG 部分",
							Risk: "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "增大 innodb_io_capacity 预防性加速刷脏",
							SkillCommand: "/sql \"SET GLOBAL innodb_io_capacity=2000\"",
							RawSQL:       "SET GLOBAL innodb_io_capacity=2000",
							Risk: "增加磁盘 IO", Rollback: "SET GLOBAL innodb_io_capacity=200"},
					},
				},
			},
		},
		CausedBy: []string{"MY-019", "MY-014"},
		CausesOf: []string{"MY-006"},
		Tags:     []string{"checkpoint", "redo_log", "flush", "innodb"},
		Versions: "5.5+",
	}
}
