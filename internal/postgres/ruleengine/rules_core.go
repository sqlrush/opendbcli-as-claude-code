/*-------------------------------------------------------------------------
 *
 * rules_core.go
 *	  PostgreSQL rule engine — core probe-data classification rules.
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/rules_core.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import "fmt"

// coreRules returns 25 hardcoded PostgreSQL diagnostic rules covering the most
// common performance scenarios: vacuum/bloat, lock/concurrency, query performance,
// WAL/checkpoint, connection, and IO/memory.
func coreRules() []*Rule {
	return []*Rule{
		// ── Vacuum/Bloat (5) ──
		rulePG001AutovacuumLag(),
		rulePG002TableBloat(),
		rulePG003XIDWraparound(),
		rulePG004AutovacuumSaturation(),
		rulePG005TOASTBloat(),
		// ── Lock/Concurrency (5) ──
		rulePG006LockWaits(),
		rulePG007Deadlocks(),
		rulePG008IdleInTransaction(),
		rulePG009BlockingChainLong(),
		rulePG010AdvisoryLockLeak(),
		// ── Query Performance (5) ──
		rulePG011SlowQuerySpike(),
		rulePG012SeqScanHeavy(),
		rulePG013TempFileSpill(),
		rulePG014CacheHitLow(),
		rulePG015PlanRegression(),
		// ── WAL/Checkpoint (4) ──
		rulePG016WALSpike(),
		rulePG017CheckpointFrequent(),
		rulePG018WALArchiveLag(),
		rulePG019ReplicationLag(),
		// ── Connection (3) ──
		rulePG020ConnectionSaturation(),
		rulePG021ConnectionStorm(),
		rulePG022ConnectionLeak(),
		// ── IO/Memory (3) ──
		rulePG023SharedMemoryInsufficient(),
		rulePG024IOWaitHeavy(),
		rulePG025CommitRateDrop(),
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Vacuum/Bloat Rules (PG-001 ~ PG-005)
// ═══════════════════════════════════════════════════════════════════════════════

// vacuumSkipWhenNonVacuumAnomaly returns SkipConditions that suppress vacuum rules
// when a more specific non-vacuum anomaly is present (lock, SQL, connection, etc).
// This prevents vacuum rules from dominating when dead_tuple_ratio is incidentally elevated.
func vacuumSkipWhenNonVacuumAnomaly() []SkipCondition {
	return []SkipCondition{
		{Desc: "有锁等待（锁问题优先）", Check: func(ctx *EvalContext) bool {
			m, ok := ctx.GetMetric("lock_waits")
			return ok && m.Max > 3
		}},
		{Desc: "有慢查询（SQL 问题优先）", Check: func(ctx *EvalContext) bool {
			m, ok := ctx.GetMetric("long_queries")
			return ok && m.Max > 3
		}},
		{Desc: "active_sessions 风暴（连接问题优先）", Check: func(ctx *EvalContext) bool {
			m, ok := ctx.GetMetric("active_sessions")
			return ok && m.Max > 30
		}},
		{Desc: "LWLock 争用（IO 问题优先）", Check: func(ctx *EvalContext) bool {
			m, ok := ctx.GetMetric("lwlock_wait_sessions")
			return ok && m.Max > 3
		}},
		{Desc: "idle_in_transaction 堆积（连接问题优先）", Check: func(ctx *EvalContext) bool {
			m, ok := ctx.GetMetric("idle_in_transaction")
			return ok && m.Max > 5
		}},
	}
}

// PG-001: Autovacuum 滞后 — dead_tuple_ratio > 10%
func rulePG001AutovacuumLag() *Rule {
	return &Rule{
		ID:       "PG-001",
		Name:     "Autovacuum 滞后",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 10},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例严重程度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label: "dead_tuple_ratio > 30% — 严重膨胀",
					Match: MatchGT(30),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "死元组比例超过 30%，autovacuum 严重滞后"},
						{Desc: "表空间浪费严重，查询性能下降"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "手动对高死元组表执行 VACUUM",
							RawSQL: "SELECT schemaname, relname, n_dead_tup, n_live_tup, ROUND(n_dead_tup::numeric/(n_live_tup+n_dead_tup+1)*100,1) AS dead_pct FROM pg_stat_user_tables WHERE n_dead_tup > 10000 ORDER BY n_dead_tup DESC LIMIT 10",
							Risk:   "VACUUM 期间表会有短暂锁定", Rollback: "无需回滚"},
						{Type: ActionFix, Desc: "调整 autovacuum 参数加速回收",
							RawSQL: "ALTER TABLE {table} SET (autovacuum_vacuum_scale_factor = 0.05, autovacuum_vacuum_threshold = 50)",
							Risk:   "autovacuum 运行更频繁，消耗更多 CPU/IO", Rollback: "ALTER TABLE {table} RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold)"},
					},
				},
				{
					Label: "dead_tuple_ratio 10-30% — 需关注",
					Match: MatchBetween(10, 30),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "死元组比例 10-30%，autovacuum 回收速度不足"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 autovacuum 是否被长事务阻塞",
							RawSQL: "SELECT pid, usename, state, age(backend_xid) AS xid_age, query_start, LEFT(query, 80) AS query FROM pg_stat_activity WHERE backend_xid IS NOT NULL ORDER BY age(backend_xid) DESC LIMIT 5",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 autovacuum_max_workers",
							RawSQL: "SHOW autovacuum_max_workers; -- 建议调整到 5-8",
							Risk:   "需要重启 PostgreSQL", Rollback: "恢复原值并重启"},
					},
				},
			},
		},
		CausedBy: []string{"PG-008"}, // idle-in-transaction blocks vacuum
		CausesOf: []string{"PG-003"}, // dead tuples lead to XID risk
		Tags:     []string{"vacuum", "autovacuum", "dead_tuple", "bloat"},
		Versions: "9.6+",
	}
}

// PG-002: 表膨胀严重 — bloated_tables > 3
func rulePG002TableBloat() *Rule {
	return &Rule{
		ID:       "PG-002",
		Name:     "表膨胀严重",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "bloated_tables"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "bloated_tables", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查膨胀表数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("bloated_tables") },
			Branches: []Branch{
				{
					Label: "bloated_tables > 10 — 大面积膨胀",
					Match: MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "超过 10 张表存在严重膨胀"},
						{Desc: "磁盘空间浪费，全表扫描性能恶化"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出膨胀最严重的表",
							RawSQL: "SELECT schemaname, tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS total_size, n_dead_tup FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对膨胀表执行 VACUUM FULL (需停机窗口)",
							RawSQL: "VACUUM FULL {schema}.{table}; -- 注意: 会锁表",
							Risk:   "VACUUM FULL 会 ACCESS EXCLUSIVE LOCK", Rollback: "无需回滚，但需规划停机窗口"},
					},
				},
				{
					Label: "bloated_tables 3-10 — 初期膨胀",
					Match: MatchBetween(3, 10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "3-10 张表存在膨胀，需要优化 autovacuum"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "针对膨胀表调整 autovacuum 参数",
							RawSQL: "ALTER TABLE {table} SET (autovacuum_vacuum_scale_factor = 0.02, autovacuum_vacuum_cost_delay = 2)",
							Risk:   "IO 消耗增加", Rollback: "ALTER TABLE {table} RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_cost_delay)"},
					},
				},
			},
		},
		CausedBy: []string{"PG-001"},
		CausesOf: []string{"PG-012"}, // bloat causes more seq scans
		Tags:     []string{"vacuum", "bloat", "disk_usage"},
		Versions: "9.6+",
	}
}

// PG-003: XID 回卷风险 — xid_age_pct > 50%
func rulePG003XIDWraparound() *Rule {
	return &Rule{
		ID:       "PG-003",
		Name:     "XID 回卷风险",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xid_age_pct"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xid_age_pct", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 XID 年龄占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label: "xid_age_pct > 80% — 紧急回卷风险",
					Match: MatchGT(80),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "XID 年龄超过 autovacuum_freeze_max_age 的 80%，距离强制 VACUUM 或数据库拒绝写入很近"},
						{Desc: "置信度: 95%"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即查看哪些数据库/表 XID 年龄最大",
							RawSQL: "SELECT datname, age(datfrozenxid) AS xid_age, ROUND(age(datfrozenxid)::numeric / current_setting('autovacuum_freeze_max_age')::numeric * 100, 1) AS pct FROM pg_database ORDER BY age(datfrozenxid) DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionUrgent, Desc: "对最老的表执行 VACUUM FREEZE",
							RawSQL: "VACUUM FREEZE {schema}.{table};",
							Risk:   "长时间运行，IO 消耗大", Rollback: "无需回滚"},
						{Type: ActionInvestigate, Desc: "检查是否有长事务阻止 XID 推进",
							RawSQL: "SELECT pid, usename, xact_start, age(backend_xmin) AS xmin_age, LEFT(query, 80) AS query FROM pg_stat_activity WHERE backend_xmin IS NOT NULL ORDER BY age(backend_xmin) DESC LIMIT 5",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label: "xid_age_pct 50-80% — 预警",
					Match: MatchBetween(50, 80),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "XID 年龄占比 50-80%，需尽快安排 VACUUM FREEZE"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "降低 vacuum_freeze_min_age 加速冻结",
							RawSQL: "ALTER SYSTEM SET vacuum_freeze_min_age = 50000000; SELECT pg_reload_conf();",
							Risk:   "VACUUM 运行时间变长", Rollback: "ALTER SYSTEM RESET vacuum_freeze_min_age; SELECT pg_reload_conf();"},
						{Type: ActionPrevent, Desc: "设置监控告警，XID 年龄超过 1.5 亿时报警",
							RawSQL: "-- 建议在监控系统中配置告警阈值",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-001", "PG-008"},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "xid", "wraparound", "freeze"},
		Versions: "9.6+",
	}
}

// PG-004: Autovacuum Worker 饱和
func rulePG004AutovacuumSaturation() *Rule {
	return &Rule{
		ID:       "PG-004",
		Name:     "Autovacuum Worker 饱和",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例判断 autovacuum 是否跟不上",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label: "dead_tuple_ratio > 20% — worker 可能饱和",
					Match: MatchGT(20),
					Then: &TreeNode{
						Step: "检查 XID 年龄是否同时偏高",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue("xid_age_pct")
						},
						Branches: []Branch{
							{
								Label:    "XID 年龄也偏高 — 确认 worker 饱和",
								Match:    MatchGT(30),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "死元组比例高且 XID 年龄偏高，autovacuum worker 数量不足"},
									{Desc: "置信度: 85%"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看当前 autovacuum worker 数量和状态",
										RawSQL: "SELECT pid, datname, relid::regclass AS table_name, phase, heap_blks_total, heap_blks_scanned, heap_blks_vacuumed FROM pg_stat_progress_vacuum",
										Risk:   "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增加 autovacuum_max_workers 到 5-8",
										RawSQL: "ALTER SYSTEM SET autovacuum_max_workers = 6; -- 需重启生效",
										Risk:   "CPU/IO 资源消耗增加", Rollback: "ALTER SYSTEM RESET autovacuum_max_workers;"},
									{Type: ActionFix, Desc: "降低 autovacuum_vacuum_cost_delay 加速回收",
										RawSQL: "ALTER SYSTEM SET autovacuum_vacuum_cost_delay = '2ms'; SELECT pg_reload_conf();",
										Risk:   "IO 压力增加", Rollback: "ALTER SYSTEM RESET autovacuum_vacuum_cost_delay; SELECT pg_reload_conf();"},
								},
							},
							{
								Label:    "XID 正常 — 死元组堆积但未影响 XID",
								Match:    MatchDefault(),
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "死元组堆积但 XID 年龄尚在安全范围"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "对高死元组表单独调整 autovacuum 参数",
										RawSQL: "ALTER TABLE {table} SET (autovacuum_vacuum_cost_delay = 2, autovacuum_vacuum_scale_factor = 0.01)",
										Risk:   "IO 增加", Rollback: "ALTER TABLE {table} RESET (autovacuum_vacuum_cost_delay, autovacuum_vacuum_scale_factor)"},
								},
							},
						},
					},
				},
				{
					Label:    "dead_tuple_ratio 5-20% — 轻度堆积",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "死元组比例轻度偏高，autovacuum 可能稍有延迟"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控死元组趋势",
							RawSQL: "SELECT relname, n_dead_tup, last_autovacuum, autovacuum_count FROM pg_stat_user_tables WHERE n_dead_tup > 1000 ORDER BY n_dead_tup DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-001", "PG-003"},
		Tags:     []string{"vacuum", "autovacuum", "worker", "saturation"},
		Versions: "9.6+",
	}
}

// PG-005: TOAST 表膨胀 — dead_tuple_ratio 高且有大字段
func rulePG005TOASTBloat() *Rule {
	return &Rule{
		ID:       "PG-005",
		Name:     "TOAST 表膨胀",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 15},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例和膨胀表情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 15% — 可能存在 TOAST 膨胀",
					Match:    MatchGT(15),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "死元组比例高，可能存在 TOAST 表膨胀（含 text/jsonb/bytea 列的表）"},
						{Desc: "TOAST 表不会被普通 VACUUM 有效回收"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 TOAST 表大小",
							RawSQL: "SELECT c.relname AS table_name, pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size, pg_size_pretty(pg_relation_size(c.reltoastrelid)) AS toast_size FROM pg_class c WHERE c.reltoastrelid != 0 AND c.relkind = 'r' ORDER BY pg_relation_size(c.reltoastrelid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对 TOAST 膨胀严重的表执行 VACUUM FULL",
							RawSQL: "VACUUM FULL {schema}.{table}; -- 会重建 TOAST 表",
							Risk:   "ACCESS EXCLUSIVE LOCK 锁表", Rollback: "无需回滚，需规划停机窗口"},
						{Type: ActionPrevent, Desc: "调整 TOAST 存储策略",
							RawSQL: "ALTER TABLE {table} ALTER COLUMN {column} SET STORAGE EXTENDED; -- 根据场景选择 PLAIN/EXTERNAL/EXTENDED/MAIN",
							Risk:   "影响后续 INSERT/UPDATE 性能", Rollback: "ALTER TABLE {table} ALTER COLUMN {column} SET STORAGE EXTENDED"},
					},
				},
				{
					Label:    "低于阈值",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "死元组比例中等，TOAST 膨胀风险较低"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{"PG-001"},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "toast", "bloat", "large_object"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Lock/Concurrency Rules (PG-006 ~ PG-010)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-006: 锁等待阻塞 — lock_waits > 3
func rulePG006LockWaits() *Rule {
	return &Rule{
		ID:       "PG-006",
		Name:     "锁等待阻塞",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalWaitEvent, Key: "Lock"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待数量和阻塞链",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label: "lock_waits > 10 — 严重锁争用",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step:  "检查是否有阻塞链",
						Check: func(ctx *EvalContext) interface{} { return ctx.HasBlockingChains() },
						Branches: []Branch{
							{
								Label:    "存在阻塞链 — 级联锁阻塞",
								Match:    MatchBool(true),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "锁等待超过 10 个且存在阻塞链，发生级联锁阻塞"},
									{Desc: fmt.Sprintf("置信度: 95%%")},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看阻塞链详情并考虑终止头部阻塞者",
										RawSQL: "SELECT blocked_locks.pid AS blocked_pid, blocked_activity.usename AS blocked_user, blocking_locks.pid AS blocking_pid, blocking_activity.usename AS blocking_user, LEFT(blocked_activity.query, 60) AS blocked_query, LEFT(blocking_activity.query, 60) AS blocking_query FROM pg_catalog.pg_locks blocked_locks JOIN pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid = blocked_locks.pid JOIN pg_catalog.pg_locks blocking_locks ON blocking_locks.locktype = blocked_locks.locktype AND blocking_locks.relation = blocked_locks.relation AND blocking_locks.pid != blocked_locks.pid JOIN pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid = blocking_locks.pid WHERE NOT blocked_locks.granted",
										Risk:   "无", Rollback: "无"},
									{Type: ActionUrgent, Desc: "终止头部阻塞会话",
										RawSQL: "SELECT pg_terminate_backend({blocking_pid}); -- 请确认后执行",
										Risk:   "终止会话可能导致事务回滚", Rollback: "无"},
								},
							},
							{
								Label:    "无阻塞链 — 分散锁竞争",
								Match:    MatchBool(false),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "锁等待多但无明显阻塞链，可能是多表并发更新竞争"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析锁类型分布",
										RawSQL: "SELECT locktype, mode, COUNT(*) AS cnt FROM pg_locks WHERE NOT granted GROUP BY locktype, mode ORDER BY cnt DESC",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "lock_waits 3-10 — 中等锁争用",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "锁等待 3-10 个，存在锁争用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查等待锁的会话",
							RawSQL: "SELECT pid, usename, wait_event_type, wait_event, LEFT(query, 80) AS query, state FROM pg_stat_activity WHERE wait_event_type = 'Lock' ORDER BY query_start",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-008"}, // idle-in-transaction holds locks
		CausesOf: []string{"PG-011"}, // lock waits cause slow queries
		Tags:     []string{"lock", "blocking", "concurrency"},
		Versions: "9.6+",
	}
}

// PG-007: 死锁 — deadlocks > 0
func rulePG007Deadlocks() *Rule {
	return &Rule{
		ID:       "PG-007",
		Name:     "死锁",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "deadlocks"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "deadlocks", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查死锁数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("deadlocks") },
			Branches: []Branch{
				{
					Label: "deadlocks > 5 — 频繁死锁",
					Match: MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "采集期间发生多次死锁，应用逻辑存在严重并发问题"},
						{Desc: "置信度: 90%"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查 PostgreSQL 日志中的死锁详情",
							RawSQL: "-- 查看 pg_log 中 deadlock detected 记录\n-- grep 'deadlock detected' /var/log/postgresql/*.log",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "应用层修复: 统一资源访问顺序，缩短事务",
							RawSQL: "-- 设置 lock_timeout 避免长时间等待\nSET lock_timeout = '5s';",
							Risk:   "超时会导致语句失败", Rollback: "SET lock_timeout = '0';"},
					},
				},
				{
					Label:    "deadlocks 1-5 — 偶发死锁",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "发生死锁，需排查应用逻辑"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查死锁相关的 SQL 和表",
							RawSQL: "SELECT datname, deadlocks FROM pg_stat_database WHERE deadlocks > 0 ORDER BY deadlocks DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "设置 deadlock_timeout 参数",
							RawSQL: "SHOW deadlock_timeout; -- 默认 1s，高并发场景可适当调高",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "deadlock", "concurrency"},
		Versions: "9.6+",
	}
}

// PG-008: Idle in Transaction — idle_in_transaction > 5
func rulePG008IdleInTransaction() *Rule {
	return &Rule{
		ID:       "PG-008",
		Name:     "Idle in Transaction 过多",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "idle_in_transaction"},
			{Type: SignalCategory, Key: "connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "idle_in_transaction", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 idle in transaction 数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("idle_in_transaction") },
			Branches: []Branch{
				{
					Label: "idle_in_transaction > 20 — 严重泄漏",
					Match: MatchGT(20),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "超过 20 个会话处于 idle in transaction，事务未提交严重"},
						{Desc: "长时间未提交事务阻止 VACUUM 和 XID 推进"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查找并终止最老的 idle in transaction",
							RawSQL: "SELECT pid, usename, state, xact_start, now()-xact_start AS idle_duration, LEFT(query, 80) AS last_query FROM pg_stat_activity WHERE state = 'idle in transaction' ORDER BY xact_start LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "设置 idle_in_transaction_session_timeout",
							RawSQL: "ALTER SYSTEM SET idle_in_transaction_session_timeout = '5min'; SELECT pg_reload_conf();",
							Risk:   "超时后连接被自动终止", Rollback: "ALTER SYSTEM RESET idle_in_transaction_session_timeout; SELECT pg_reload_conf();"},
					},
				},
				{
					Label: "idle_in_transaction 5-20 — 需关注",
					Match: MatchBetween(5, 20),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "5-20 个会话 idle in transaction，占用连接和锁资源"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查这些会话的来源应用",
							RawSQL: "SELECT usename, application_name, client_addr, COUNT(*) AS cnt FROM pg_stat_activity WHERE state = 'idle in transaction' GROUP BY usename, application_name, client_addr ORDER BY cnt DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "应用层启用连接池自动回收",
							RawSQL: "-- 建议在 PgBouncer/应用连接池配置 idle_timeout",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-001", "PG-003", "PG-006"},
		Tags:     []string{"lock", "idle_in_transaction", "connection_leak"},
		Versions: "9.6+",
	}
}

// PG-009: 阻塞链过长 — blocker_count > 1 且有 BlockingChains
func rulePG009BlockingChainLong() *Rule {
	return &Rule{
		ID:       "PG-009",
		Name:     "阻塞链过长",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "blocker_count"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "blocker_count", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step: "检查阻塞链深度和影响范围",
			Check: func(ctx *EvalContext) interface{} {
				if !ctx.HasBlockingChains() {
					return 0.0
				}
				maxVictims := 0
				for _, chain := range ctx.BlockingChains {
					if chain.VictimCount > maxVictims {
						maxVictims = chain.VictimCount
					}
				}
				return float64(maxVictims)
			},
			Branches: []Branch{
				{
					Label: "单个阻塞者影响 > 5 个会话 — 严重级联",
					Match: MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "存在阻塞链且单个阻塞者影响超过 5 个会话"},
						{Desc: "级联阻塞导致大量会话堆积"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看阻塞链头部会话并终止",
							RawSQL: "SELECT pid, usename, state, wait_event_type, LEFT(query, 80) AS query, xact_start FROM pg_stat_activity WHERE pid IN (SELECT DISTINCT blocking_locks.pid FROM pg_locks blocked_locks JOIN pg_locks blocking_locks ON blocking_locks.locktype = blocked_locks.locktype AND blocking_locks.relation = blocked_locks.relation AND blocking_locks.pid != blocked_locks.pid WHERE NOT blocked_locks.granted AND blocking_locks.granted)",
							Risk:   "无", Rollback: "无"},
						{Type: ActionUrgent, Desc: "终止阻塞者",
							RawSQL: "SELECT pg_terminate_backend({blocker_pid});",
							Risk:   "终止会话将回滚其事务", Rollback: "无"},
					},
				},
				{
					Label:    "阻塞链存在但影响较小",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在多个阻塞者，需关注锁竞争"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析锁等待类型",
							RawSQL: "SELECT l.locktype, l.mode, l.relation::regclass, a.pid, a.usename, LEFT(a.query, 60) AS query FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE NOT l.granted ORDER BY l.locktype, l.mode",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-008"},
		CausesOf: []string{"PG-006"},
		Tags:     []string{"lock", "blocking_chain", "cascade"},
		Versions: "9.6+",
	}
}

// PG-010: Advisory Lock 泄漏
func rulePG010AdvisoryLockLeak() *Rule {
	return &Rule{
		ID:       "PG-010",
		Name:     "Advisory Lock 泄漏",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "无 Lock 类等待事件", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("Lock") < 1
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查是否存在 advisory lock 占比",
			Check: func(ctx *EvalContext) interface{} {
				// Check if Lock wait events are present
				return ctx.WaitPct("Lock")
			},
			Branches: []Branch{
				{
					Label: "Lock 等待占比 > 10% — 可能有 advisory lock 问题",
					Match: MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Lock 类等待事件占比较高，需排查是否有 advisory lock 泄漏"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 advisory lock 持有情况",
							RawSQL: "SELECT l.locktype, l.objid, l.pid, a.usename, a.application_name, LEFT(a.query, 60) AS query FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE l.locktype = 'advisory' ORDER BY l.objid",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "释放泄漏的 advisory lock",
							RawSQL: "SELECT pg_advisory_unlock_all(); -- 在泄漏会话中执行",
							Risk:   "可能影响依赖 advisory lock 的业务逻辑", Rollback: "无"},
					},
				},
				{
					Label:    "Lock 等待占比正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "Lock 等待占比较低，advisory lock 泄漏风险小"},
					},
					Actions: []Action{},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-006"},
		Tags:     []string{"lock", "advisory_lock", "leak"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Query Performance Rules (PG-011 ~ PG-015)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-011: 慢查询冲高 — long_queries > 3
func rulePG011SlowQuerySpike() *Rule {
	return &Rule{
		ID:       "PG-011",
		Name:     "慢查询冲高",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询数量和关联指标",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label: "long_queries > 10 — 大面积慢查询",
					Match: MatchGT(10),
					Then: &TreeNode{
						Step: "检查是否伴随锁等待",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue("lock_waits")
						},
						Branches: []Branch{
							{
								Label:    "伴随锁等待 — 慢查询由锁导致",
								Match:    MatchGT(3),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量慢查询且伴随锁等待，锁阻塞是根因"},
									{Desc: "置信度: 90%"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "优先处理锁阻塞",
										RawSQL: "SELECT pid, usename, wait_event_type, wait_event, LEFT(query, 80) AS query FROM pg_stat_activity WHERE state = 'active' AND wait_event_type = 'Lock' ORDER BY query_start LIMIT 10",
										Risk:   "无", Rollback: "无"},
								},
							},
							{
								Label:    "无锁等待 — SQL 本身效率低",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大量慢查询但无锁等待，SQL 执行效率低下"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看耗时最长的活跃查询",
										RawSQL: "SELECT pid, usename, now()-query_start AS duration, wait_event_type, wait_event, LEFT(query, 100) AS query FROM pg_stat_activity WHERE state = 'active' AND query_start < now() - interval '30 seconds' ORDER BY query_start LIMIT 10",
										Risk:   "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查 pg_stat_statements 中最慢的 SQL",
										RawSQL: "SELECT queryid, calls, ROUND((mean_exec_time/1000)::numeric, 2) AS mean_sec, ROUND((total_exec_time/1000)::numeric, 2) AS total_sec, LEFT(query, 80) AS query FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "long_queries 3-10 — 需关注",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在 3-10 个慢查询，需排查 SQL 效率"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析慢查询等待事件",
							RawSQL: "SELECT pid, wait_event_type, wait_event, LEFT(query, 80) AS query FROM pg_stat_activity WHERE state = 'active' AND query_start < now() - interval '30 seconds'",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-006", "PG-014"},
		CausesOf: []string{"PG-020"},
		Tags:     []string{"sql_perf", "slow_query", "long_running"},
		Versions: "9.6+",
	}
}

// PG-012: 全表扫描过多 — seq_scan_heavy_tables > 5
func rulePG012SeqScanHeavy() *Rule {
	return &Rule{
		ID:       "PG-012",
		Name:     "全表扫描过多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "seq_scan_heavy_tables"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "seq_scan_heavy_tables", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查全表扫描表数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("seq_scan_heavy_tables") },
			Branches: []Branch{
				{
					Label: "seq_scan_heavy > 15 — 大量全表扫描",
					Match: MatchGT(15),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "超过 15 张表存在大量全表扫描，严重影响性能"},
						{Desc: "可能缺少索引或统计信息过期"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出全表扫描最多的表",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read, idx_scan, CASE WHEN (seq_scan + idx_scan) > 0 THEN ROUND(100.0 * seq_scan / (seq_scan + idx_scan), 1) ELSE 0 END AS seq_pct FROM pg_stat_user_tables WHERE seq_scan > 100 ORDER BY seq_tup_read DESC LIMIT 15",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对高频全表扫描表添加索引",
							RawSQL: "-- 使用 EXPLAIN 分析具体 SQL，根据 WHERE/JOIN 条件创建索引\nCREATE INDEX CONCURRENTLY idx_{table}_{column} ON {table}({column});",
							Risk:   "CREATE INDEX CONCURRENTLY 不锁表但消耗 IO", Rollback: "DROP INDEX idx_{table}_{column};"},
						{Type: ActionFix, Desc: "更新统计信息",
							RawSQL: "ANALYZE {schema}.{table};",
							Risk:   "短暂 IO 消耗", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "seq_scan_heavy 5-15 — 需优化",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "5-15 张表全表扫描比例偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查缺失索引的表",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read, idx_scan FROM pg_stat_user_tables WHERE seq_scan > idx_scan AND seq_tup_read > 10000 ORDER BY seq_tup_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-002"},
		CausesOf: []string{"PG-014"},
		Tags:     []string{"sql_perf", "seq_scan", "missing_index"},
		Versions: "9.6+",
	}
}

// PG-013: 临时文件溢出 — temp_bytes > 100MB
func rulePG013TempFileSpill() *Rule {
	return &Rule{
		ID:       "PG-013",
		Name:     "临时文件溢出",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes"},
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				// temp_bytes > 100MB (100 * 1024 * 1024)
				{Source: "metrics", Field: "temp_bytes", Op: OpGT, Value: 104857600},
			},
			SkipWhen: []SkipCondition{
				{Desc: "temp_bytes_rate 也不高则跳过", Check: func(ctx *EvalContext) bool {
					// If temp_bytes metric is not available, check temp_bytes_rate
					_, hasTempBytes := ctx.GetMetric("temp_bytes")
					if hasTempBytes {
						return false
					}
					return ctx.MetricValue("temp_bytes_rate") < 1048576 // < 1MB/s
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查临时文件使用量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes") },
			Branches: []Branch{
				{
					Label: "temp_bytes > 1GB — 大量磁盘排序/hash",
					Match: MatchGT(1073741824),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "临时文件超过 1GB，SQL 中存在大量磁盘排序或 Hash Join 溢出"},
						{Desc: "work_mem 不足导致排序/聚合操作无法在内存完成"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "找出生成最多临时文件的 SQL",
							RawSQL: "SELECT queryid, calls, temp_blks_read + temp_blks_written AS temp_blks, ROUND((mean_exec_time/1000)::numeric, 2) AS mean_sec, LEFT(query, 80) AS query FROM pg_stat_statements WHERE temp_blks_read + temp_blks_written > 0 ORDER BY temp_blks_read + temp_blks_written DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 work_mem",
							RawSQL: "ALTER SYSTEM SET work_mem = '64MB'; SELECT pg_reload_conf(); -- 默认 4MB，根据内存调整",
							Risk:   "每个排序操作占用更多内存", Rollback: "ALTER SYSTEM SET work_mem = '4MB'; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "temp_bytes 100MB-1GB — 中度溢出",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "临时文件 100MB-1GB，部分 SQL 排序操作溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查临时文件使用详情",
							RawSQL: "SELECT datname, temp_files, pg_size_pretty(temp_bytes) AS temp_size FROM pg_stat_database WHERE temp_bytes > 0 ORDER BY temp_bytes DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对特定查询设置更大的 work_mem",
							RawSQL: "SET work_mem = '128MB'; -- 在会话级别调整",
							Risk:   "仅影响当前会话", Rollback: "RESET work_mem;"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-024"},
		Tags:     []string{"sql_perf", "temp_file", "work_mem", "sort"},
		Versions: "9.6+",
	}
}

// PG-014: Cache 命中率低 — cache_hit_pct < 95%
func rulePG014CacheHitLow() *Rule {
	return &Rule{
		ID:       "PG-014",
		Name:     "Cache 命中率低",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "cache_hit_pct"},
			{Type: SignalCategory, Key: "memory"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "cache_hit_pct", Op: OpLT, Value: 95},
			},
			SkipWhen: []SkipCondition{
				{Desc: "cache_hit_pct 指标不存在", Check: func(ctx *EvalContext) bool {
					_, ok := ctx.GetMetric("cache_hit_pct")
					return !ok
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查缓存命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label: "cache_hit_pct < 85% — 严重缓存不足",
					Match: MatchLT(85),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Buffer Cache 命中率低于 85%，大量物理读"},
						{Desc: "shared_buffers 可能配置不足或存在大量全表扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查数据库级别缓存命中率",
							RawSQL: "SELECT datname, blks_hit, blks_read, ROUND(100.0 * blks_hit / NULLIF(blks_hit + blks_read, 0), 1) AS hit_pct FROM pg_stat_database WHERE datname = current_database()",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 shared_buffers",
							RawSQL: "SHOW shared_buffers; -- 建议为物理内存的 25%\nALTER SYSTEM SET shared_buffers = '4GB'; -- 需重启生效",
							Risk:   "需重启 PostgreSQL", Rollback: "ALTER SYSTEM RESET shared_buffers;"},
						{Type: ActionFix, Desc: "减少不必要的全表扫描",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read FROM pg_stat_user_tables WHERE seq_scan > 100 ORDER BY seq_tup_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label: "cache_hit_pct 85-95% — 需优化",
					Match: MatchBetween(85, 95),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "缓存命中率 85-95%，存在优化空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查表级别缓存使用",
							RawSQL: "SELECT schemaname, relname, heap_blks_hit, heap_blks_read, ROUND(100.0 * heap_blks_hit / NULLIF(heap_blks_hit + heap_blks_read, 0), 1) AS hit_pct FROM pg_statio_user_tables WHERE heap_blks_read > 100 ORDER BY heap_blks_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "使用 pg_prewarm 预热热点表",
							RawSQL: "SELECT pg_prewarm('{table}'); -- 预热到 shared_buffers",
							Risk:   "短暂 IO 增加", Rollback: "无需回滚"},
					},
				},
			},
		},
		CausedBy: []string{"PG-012"},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"memory", "cache", "shared_buffers", "physical_read"},
		Versions: "9.6+",
	}
}

// PG-015: 执行计划回退
func rulePG015PlanRegression() *Rule {
	return &Rule{
		ID:       "PG-015",
		Name:     "执行计划回退",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "TopSQL 中无异常 SQL", Check: func(ctx *EvalContext) bool {
					return len(ctx.TopSQLs) == 0
				}},
				{Desc: "long_queries > 3 时让位给 PG-011 慢查询冲高", Check: func(ctx *EvalContext) bool {
					m, ok := ctx.GetMetric("long_queries")
					return ok && m.Max > 3
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查 TopSQL 中是否有高并发慢 SQL",
			Check: func(ctx *EvalContext) interface{} {
				if len(ctx.TopSQLs) == 0 {
					return 0.0
				}
				// Return the max active count from top SQLs
				maxActive := 0
				for _, sql := range ctx.TopSQLs {
					if sql.ActiveCount > maxActive {
						maxActive = sql.ActiveCount
					}
				}
				return float64(maxActive)
			},
			Branches: []Branch{
				{
					Label: "单条 SQL 并发 > 5 — 可能执行计划回退",
					Match: MatchGT(5),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在单条 SQL 大量并发执行，可能因统计信息过期导致执行计划回退"},
						{Desc: "置信度: 75%"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 pg_stat_statements 中该 SQL 的执行时间变化",
							RawSQL: "SELECT queryid, calls, ROUND(mean_exec_time::numeric, 2) AS mean_ms, ROUND(max_exec_time::numeric, 2) AS max_ms, ROUND(stddev_exec_time::numeric, 2) AS stddev_ms, LEFT(query, 80) AS query FROM pg_stat_statements ORDER BY mean_exec_time DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对相关表重新收集统计信息",
							RawSQL: "ANALYZE {schema}.{table};",
							Risk:   "短暂 IO 消耗", Rollback: "无需回滚"},
						{Type: ActionPrevent, Desc: "启用 auto_explain 记录慢查询计划",
							RawSQL: "ALTER SYSTEM SET auto_explain.log_min_duration = '1s'; ALTER SYSTEM SET auto_explain.log_analyze = true; SELECT pg_reload_conf();",
							Risk:   "日志量增加", Rollback: "ALTER SYSTEM RESET auto_explain.log_min_duration; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "单条 SQL 并发 <= 5 — 不太像计划回退",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "慢查询存在但并发不高，可能是 SQL 本身效率低"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "使用 EXPLAIN ANALYZE 分析慢 SQL",
							RawSQL: "EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) {query};",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"sql_perf", "plan_regression", "statistics"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// WAL/Checkpoint Rules (PG-016 ~ PG-019)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-016: WAL 生成量冲高 — wal_bytes_rate spike
func rulePG016WALSpike() *Rule {
	return &Rule{
		ID:       "PG-016",
		Name:     "WAL 生成量冲高",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				// wal_bytes_rate > 50MB/s
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 52428800}, // > 50MB/s
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查 WAL 生成速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label: "wal_bytes_rate > 200MB/s — WAL 生成过快",
					Match: MatchGT(209715200),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "WAL 生成速率超过 200MB/s，可能存在大批量 DML 或全页写入过多"},
						{Desc: "WAL 过多导致磁盘压力大，归档和复制可能跟不上"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否有大事务正在运行",
							RawSQL: "SELECT pid, usename, state, LEFT(query, 80) AS query, xact_start, now()-xact_start AS duration FROM pg_stat_activity WHERE state = 'active' AND xact_start IS NOT NULL ORDER BY xact_start LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "降低 full_page_writes 的影响",
							RawSQL: "SHOW full_page_writes; -- 如果是 on，每次 checkpoint 后首次修改页会写全页\n-- 增大 checkpoint_timeout 减少全页写频率",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 wal_buffers",
							RawSQL: "SHOW wal_buffers; -- 默认为 shared_buffers/32，建议至少 64MB\nALTER SYSTEM SET wal_buffers = '64MB'; -- 需重启",
							Risk:   "需重启 PostgreSQL", Rollback: "ALTER SYSTEM RESET wal_buffers;"},
					},
				},
				{
					Label:    "wal_bytes_rate 50-200MB/s — 偏高",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 生成速率偏高，需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 WAL 统计",
							RawSQL: "SELECT * FROM pg_stat_wal;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-017", "PG-018", "PG-019"},
		Tags:     []string{"wal", "write_ahead_log", "throughput"},
		Versions: "14+",
	}
}

// PG-017: Checkpoint 过于频繁 — checkpoints_req 高
func rulePG017CheckpointFrequent() *Rule {
	return &Rule{
		ID:       "PG-017",
		Name:     "Checkpoint 过于频繁",
		Category: "checkpoint",
		Signals: []Signal{
			{Type: SignalMetric, Key: "checkpoints_req"},
			{Type: SignalCategory, Key: "checkpoint"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 3},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查请求式 checkpoint 频率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoints_req") },
			Branches: []Branch{
				{
					Label: "checkpoints_req > 10 — 极频繁 checkpoint",
					Match: MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "请求式 checkpoint 过于频繁，WAL 量超过 max_wal_size 触发"},
						{Desc: "频繁 checkpoint 导致大量全页写入，IO 压力增大"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 max_wal_size",
							RawSQL: "SHOW max_wal_size; -- 默认 1GB，高写入场景建议 4-16GB\nALTER SYSTEM SET max_wal_size = '8GB'; SELECT pg_reload_conf();",
							Risk:   "WAL 磁盘占用增加", Rollback: "ALTER SYSTEM RESET max_wal_size; SELECT pg_reload_conf();"},
						{Type: ActionFix, Desc: "增大 checkpoint_timeout",
							RawSQL: "SHOW checkpoint_timeout; -- 默认 5min，建议 15-30min\nALTER SYSTEM SET checkpoint_timeout = '15min'; SELECT pg_reload_conf();",
							Risk:   "崩溃恢复时间变长", Rollback: "ALTER SYSTEM RESET checkpoint_timeout; SELECT pg_reload_conf();"},
						{Type: ActionFix, Desc: "调整 checkpoint_completion_target",
							RawSQL: "SHOW checkpoint_completion_target; -- 建议 0.9\nALTER SYSTEM SET checkpoint_completion_target = 0.9; SELECT pg_reload_conf();",
							Risk:   "无", Rollback: "ALTER SYSTEM RESET checkpoint_completion_target; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "checkpoints_req 3-10 — 偏频繁",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "请求式 checkpoint 偏频繁，max_wal_size 可能偏小"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 checkpoint 统计",
							RawSQL: "SELECT checkpoints_timed, checkpoints_req, checkpoint_write_time, checkpoint_sync_time, buffers_checkpoint FROM pg_stat_bgwriter",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-016"},
		CausesOf: []string{"PG-024"},
		Tags:     []string{"checkpoint", "max_wal_size", "full_page_writes"},
		Versions: "9.6+",
	}
}

// PG-018: WAL 归档延迟
func rulePG018WALArchiveLag() *Rule {
	return &Rule{
		ID:       "PG-018",
		Name:     "WAL 归档延迟",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				// WAL rate high enough to potentially cause archive lag
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 10485760}, // > 10MB/s
			},
		},
		Tree: &TreeNode{
			Step: "检查 WAL 速率和归档配置",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("wal_bytes_rate")
			},
			Branches: []Branch{
				{
					Label: "WAL 速率 > 100MB/s — 归档可能跟不上",
					Match: MatchGT(104857600),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "WAL 生成速率极高，归档进程可能无法及时归档"},
						{Desc: "pg_wal 目录可能持续增长，有磁盘撑满风险"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查归档状态",
							RawSQL: "SELECT archived_count, last_archived_wal, last_archived_time, failed_count, last_failed_wal, last_failed_time FROM pg_stat_archiver",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "检查 pg_wal 目录大小",
							RawSQL: "SELECT count(*) AS wal_files, pg_size_pretty(sum(size)) AS total_size FROM pg_ls_waldir()",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "优化归档命令或使用 pgBackRest",
							RawSQL: "SHOW archive_command; -- 检查归档命令效率",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "WAL 速率中等 — 关注归档延迟",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 速率偏高，需确认归档是否跟上"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查归档延迟",
							RawSQL: "SELECT last_archived_time, now() - last_archived_time AS archive_lag FROM pg_stat_archiver",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-016"},
		CausesOf: []string{},
		Tags:     []string{"wal", "archive", "backup"},
		Versions: "9.6+",
	}
}

// PG-019: 复制延迟 — replication_lag_sec > 10
func rulePG019ReplicationLag() *Rule {
	return &Rule{
		ID:       "PG-019",
		Name:     "复制延迟",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag_sec"},
			{Type: SignalCategory, Key: "replication"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag_sec", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟严重程度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag_sec") },
			Branches: []Branch{
				{
					Label: "lag > 60s — 严重复制延迟",
					Match: MatchGT(60),
					Then: &TreeNode{
						Step:  "检查 WAL 速率是否也高",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
						Branches: []Branch{
							{
								Label:    "WAL 速率高 — 主库写入过快",
								Match:    MatchGT(52428800), // > 50MB/s
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "复制延迟超过 60s 且 WAL 生成速率高，备库回放速度跟不上"},
									{Desc: "置信度: 90%"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查复制状态详情",
										RawSQL: "SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn, pg_wal_lsn_diff(sent_lsn, replay_lsn) AS replay_lag_bytes FROM pg_stat_replication",
										Risk:   "无", Rollback: "无"},
									{Type: ActionFix, Desc: "增加备库 max_parallel_apply_workers (PG16+)",
										RawSQL: "-- 在备库: ALTER SYSTEM SET max_parallel_apply_workers_per_subscription = 4; SELECT pg_reload_conf();",
										Risk:   "备库 CPU 消耗增加", Rollback: "ALTER SYSTEM RESET max_parallel_apply_workers_per_subscription;"},
								},
							},
							{
								Label:    "WAL 速率正常 — 备库或网络问题",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "复制延迟严重但 WAL 速率不高，可能是备库性能差或网络延迟"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查备库回放状态",
										RawSQL: "SELECT client_addr, state, write_lag, flush_lag, replay_lag FROM pg_stat_replication",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "lag 10-60s — 中度延迟",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "复制延迟 10-60s，需关注趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查复制槽状态",
							RawSQL: "SELECT slot_name, slot_type, active, pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn) AS retained_bytes FROM pg_replication_slots",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-016"},
		CausesOf: []string{},
		Tags:     []string{"replication", "streaming", "standby", "lag"},
		Versions: "10+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Connection Rules (PG-020 ~ PG-022)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-020: 连接数饱和 — connections_pct > 80%
func rulePG020ConnectionSaturation() *Rule {
	return &Rule{
		ID:       "PG-020",
		Name:     "连接数饱和",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalCategory, Key: "connection"},
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
					Label: "connections_pct > 95% — 即将耗尽",
					Match: MatchGT(95),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "连接使用率超过 95%，新连接即将被拒绝"},
						{Desc: "置信度: 95%"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看连接分布并终止空闲连接",
							RawSQL: "SELECT usename, application_name, client_addr, state, COUNT(*) AS cnt FROM pg_stat_activity GROUP BY usename, application_name, client_addr, state ORDER BY cnt DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionUrgent, Desc: "终止长时间空闲的连接",
							RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND state_change < now() - interval '30 minutes' AND pid != pg_backend_pid()",
							Risk:   "空闲连接被终止", Rollback: "无"},
						{Type: ActionFix, Desc: "增大 max_connections 或使用连接池",
							RawSQL: "SHOW max_connections; -- 建议使用 PgBouncer 而非直接增大\nALTER SYSTEM SET max_connections = 300; -- 需重启",
							Risk:   "增大 max_connections 消耗更多内存", Rollback: "ALTER SYSTEM RESET max_connections;"},
					},
				},
				{
					Label: "connections_pct 80-95% — 偏高",
					Match: MatchBetween(80, 95),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "连接使用率 80-95%，需优化连接管理"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析连接来源",
							RawSQL: "SELECT usename, application_name, state, COUNT(*) AS cnt FROM pg_stat_activity GROUP BY usename, application_name, state ORDER BY cnt DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "部署 PgBouncer 连接池",
							RawSQL: "-- 建议使用 PgBouncer transaction pooling 模式\n-- max_client_conn = 1000, default_pool_size = 25",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-011", "PG-008"},
		CausesOf: []string{},
		Tags:     []string{"connection", "max_connections", "pgbouncer"},
		Versions: "9.6+",
	}
}

// PG-021: 连接风暴 — active_sessions spike
func rulePG021ConnectionStorm() *Rule {
	return &Rule{
		ID:       "PG-021",
		Name:     "连接风暴",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step: "检查活跃会话数和基线的比值",
			Check: func(ctx *EvalContext) interface{} {
				if ctx.BaselineActive > 0 {
					return float64(ctx.PeakActive) / ctx.BaselineActive
				}
				return float64(ctx.PeakActive)
			},
			Branches: []Branch{
				{
					Label: "峰值/基线 > 5 — 突发连接风暴",
					Match: MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: fmt.Sprintf("活跃会话突增，峰值/基线 > 5 倍")},
						{Desc: "可能是应用重连风暴或突发流量"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查连接来源",
							RawSQL: "SELECT usename, application_name, client_addr, COUNT(*) AS cnt FROM pg_stat_activity WHERE state = 'active' GROUP BY usename, application_name, client_addr ORDER BY cnt DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionUrgent, Desc: "限制单用户连接数",
							RawSQL: "ALTER ROLE {username} CONNECTION LIMIT 30;",
							Risk:   "超过限制的连接将被拒绝", Rollback: "ALTER ROLE {username} CONNECTION LIMIT -1;"},
					},
				},
				{
					Label: "峰值/基线 2-5 — 中度增长",
					Match: MatchGT(2),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "活跃会话明显增长，需关注原因"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析活跃会话等待事件",
							RawSQL: "SELECT wait_event_type, wait_event, COUNT(*) AS cnt FROM pg_stat_activity WHERE state = 'active' GROUP BY wait_event_type, wait_event ORDER BY cnt DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "增长不明显",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "活跃会话较多但增长不明显"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否有慢查询堆积",
							RawSQL: "SELECT state, COUNT(*) FROM pg_stat_activity GROUP BY state ORDER BY COUNT(*) DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-020"},
		Tags:     []string{"connection", "storm", "spike", "active_sessions"},
		Versions: "9.6+",
	}
}

// PG-022: 连接泄漏 — idle connections 持续增长
func rulePG022ConnectionLeak() *Rule {
	return &Rule{
		ID:       "PG-022",
		Name:     "连接泄漏",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalMetric, Key: "idle_in_transaction"},
			{Type: SignalCategory, Key: "connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step: "检查连接使用率和 idle 会话比例",
			Check: func(ctx *EvalContext) interface{} {
				// High connections_pct but low active sessions → possible leak
				connPct := ctx.MetricValue("connections_pct")
				activeSessions := ctx.MetricValue("active_sessions")
				if activeSessions > 0 {
					// If connection pct is high but active sessions are low, it's likely a leak
					return connPct / activeSessions
				}
				return connPct
			},
			Branches: []Branch{
				{
					Label: "连接使用率/活跃会话比值 > 10 — 大量空闲连接",
					Match: MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "连接使用率高但活跃会话少，大量空闲连接未释放"},
						{Desc: "应用层可能存在连接泄漏"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查长时间空闲连接",
							RawSQL: "SELECT usename, application_name, client_addr, state, backend_start, state_change, now()-state_change AS idle_duration FROM pg_stat_activity WHERE state = 'idle' AND state_change < now() - interval '1 hour' ORDER BY state_change LIMIT 20",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "终止超时空闲连接",
							RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND state_change < now() - interval '2 hours' AND pid != pg_backend_pid()",
							Risk:   "空闲连接被终止", Rollback: "无"},
						{Type: ActionPrevent, Desc: "配置自动断开空闲连接",
							RawSQL: "ALTER SYSTEM SET idle_session_timeout = '30min'; SELECT pg_reload_conf(); -- PG14+",
							Risk:   "空闲会话自动终止", Rollback: "ALTER SYSTEM RESET idle_session_timeout; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "比值正常 — 活跃连接多",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "连接使用率较高，活跃会话也多，非泄漏"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "考虑使用连接池降低直连数",
							RawSQL: "-- 建议部署 PgBouncer 进行连接复用",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-020"},
		Tags:     []string{"connection", "leak", "idle"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// IO/Memory Rules (PG-023 ~ PG-025)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-023: 共享内存不足 — cache_hit_pct < 90% 且 active_sessions 高
func rulePG023SharedMemoryInsufficient() *Rule {
	return &Rule{
		ID:       "PG-023",
		Name:     "共享内存不足",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "cache_hit_pct"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "memory"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "cache_hit_pct", Op: OpLT, Value: 90},
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step: "综合评估缓存和负载",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("cache_hit_pct")
			},
			Branches: []Branch{
				{
					Label: "cache_hit_pct < 80% — 严重内存不足",
					Match: MatchLT(80),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "缓存命中率低于 80% 且并发负载高，shared_buffers 严重不足"},
						{Desc: "大量物理 IO 导致查询延迟增加"},
						{Desc: "置信度: 90%"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大 shared_buffers 至物理内存的 25%",
							RawSQL: "SHOW shared_buffers;\n-- 建议: ALTER SYSTEM SET shared_buffers = '{25% of RAM}';\n-- 需重启 PostgreSQL 生效",
							Risk:   "需重启", Rollback: "ALTER SYSTEM RESET shared_buffers;"},
						{Type: ActionFix, Desc: "调整 effective_cache_size",
							RawSQL: "SHOW effective_cache_size;\nALTER SYSTEM SET effective_cache_size = '{75% of RAM}'; SELECT pg_reload_conf();",
							Risk:   "仅影响查询计划估算", Rollback: "ALTER SYSTEM RESET effective_cache_size; SELECT pg_reload_conf();"},
					},
				},
				{
					Label: "cache_hit_pct 80-90% — 中度不足",
					Match: MatchBetween(80, 90),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "缓存命中率 80-90%，在高负载下性能受影响"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查大表的缓存使用情况",
							RawSQL: "SELECT c.relname, pg_size_pretty(pg_relation_size(c.oid)) AS size, s.heap_blks_read, s.heap_blks_hit, ROUND(100.0 * s.heap_blks_hit / NULLIF(s.heap_blks_hit + s.heap_blks_read, 0), 1) AS hit_pct FROM pg_statio_user_tables s JOIN pg_class c ON s.relid = c.oid WHERE s.heap_blks_read > 0 ORDER BY s.heap_blks_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-014", "PG-024"},
		Tags:     []string{"memory", "shared_buffers", "cache_miss"},
		Versions: "9.6+",
	}
}

// PG-024: IO Wait 严重 — WaitProfile 中 IO 类等待占比 > 30%
func rulePG024IOWaitHeavy() *Rule {
	return &Rule{
		ID:       "PG-024",
		Name:     "IO Wait 严重",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "IO"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				// Trigger when active sessions exist (IO wait checked in tree)
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 1},
			},
			SkipWhen: []SkipCondition{
				{Desc: "IO 等待占比 < 30%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("IO") < 30
				}},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 IO 类等待事件占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("IO") },
			Branches: []Branch{
				{
					Label: "IO wait > 60% — 存储严重瓶颈",
					Match: MatchGT(60),
					Then: &TreeNode{
						Step:  "检查缓存命中率判断是否内存不足引起",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
						Branches: []Branch{
							{
								Label:    "cache_hit_pct < 90% — 内存不足导致 IO 高",
								Match:    MatchLT(90),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "IO 等待超过 60% 且缓存命中率低，shared_buffers 不足是根因"},
									{Desc: "置信度: 90%"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大 shared_buffers",
										RawSQL: "SHOW shared_buffers; -- 增大到物理内存的 25%",
										Risk:   "需重启", Rollback: "ALTER SYSTEM RESET shared_buffers;"},
								},
							},
							{
								Label:    "cache_hit_pct >= 90% — 存储本身慢",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "IO 等待高但缓存命中率正常，存储子系统延迟高"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查具体 IO 等待事件",
										RawSQL: "SELECT wait_event, COUNT(*) AS cnt FROM pg_stat_activity WHERE wait_event_type = 'IO' AND state = 'active' GROUP BY wait_event ORDER BY cnt DESC",
										Risk:   "无", Rollback: "无"},
									{Type: ActionPrevent, Desc: "考虑升级存储到 SSD/NVMe",
										RawSQL: "-- 检查当前磁盘类型和 IO 延迟",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "IO wait 30-60% — 需关注",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "IO 等待占比 30-60%，存储有一定压力"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析 IO 等待事件分布",
							RawSQL: "SELECT wait_event_type, wait_event, COUNT(*) AS cnt FROM pg_stat_activity WHERE state = 'active' AND wait_event_type IS NOT NULL GROUP BY wait_event_type, wait_event ORDER BY cnt DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-023", "PG-013", "PG-017"},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"io", "storage", "wait_event", "disk"},
		Versions: "9.6+",
	}
}

// PG-025: 事务提交率骤降 — xact_commit_rate 突降
func rulePG025CommitRateDrop() *Rule {
	return &Rule{
		ID:       "PG-025",
		Name:     "事务提交率骤降",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xact_commit_rate"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xact_commit_rate", Op: OpExists},
			},
			SkipWhen: []SkipCondition{
				{Desc: "提交率正常 (> 100/s)", Check: func(ctx *EvalContext) bool {
					m, ok := ctx.GetMetric("xact_commit_rate")
					if !ok {
						return true
					}
					// Skip if min commit rate is still reasonable
					return m.Min > 100
				}},
			},
		},
		Tree: &TreeNode{
			Step: "分析提交率下降的原因",
			Check: func(ctx *EvalContext) interface{} {
				m, ok := ctx.GetMetric("xact_commit_rate")
				if !ok {
					return 0.0
				}
				if m.Avg > 0 {
					return m.Min / m.Avg
				}
				return 0.0
			},
			Branches: []Branch{
				{
					Label: "min/avg < 0.3 — 提交率骤降超过 70%",
					Match: MatchLT(0.3),
					Then: &TreeNode{
						Step: "检查伴随的异常指标",
						Check: func(ctx *EvalContext) interface{} {
							lockWaits := ctx.MetricValue("lock_waits")
							if lockWaits > 5 {
								return "lock"
							}
							ioWait := ctx.WaitPct("IO")
							if ioWait > 30 {
								return "io"
							}
							return "unknown"
						},
						Branches: []Branch{
							{
								Label:    "伴随锁等待 — 锁阻塞导致提交停滞",
								Match:    MatchEquals("lock"),
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "事务提交率骤降超过 70%，伴随锁等待增加"},
									{Desc: "锁阻塞导致事务无法提交"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "优先解决锁阻塞问题",
										RawSQL: "SELECT pid, usename, wait_event_type, wait_event, LEFT(query, 80) FROM pg_stat_activity WHERE wait_event_type = 'Lock'",
										Risk:   "无", Rollback: "无"},
								},
							},
							{
								Label:    "伴随 IO 等待 — IO 延迟导致提交变慢",
								Match:    MatchEquals("io"),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "事务提交率骤降，IO 等待占比高"},
									{Desc: "WAL 写入或数据刷盘延迟导致提交变慢"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查 WAL 写入延迟",
										RawSQL: "SELECT * FROM pg_stat_wal; -- PG14+",
										Risk:   "无", Rollback: "无"},
								},
							},
							{
								Label:    "原因不明 — 需进一步排查",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "事务提交率骤降，未发现明显锁或 IO 问题"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "全面检查等待事件",
										RawSQL: "SELECT wait_event_type, wait_event, COUNT(*) AS cnt FROM pg_stat_activity WHERE state = 'active' GROUP BY wait_event_type, wait_event ORDER BY cnt DESC LIMIT 10",
										Risk:   "无", Rollback: "无"},
									{Type: ActionInvestigate, Desc: "检查是否有长事务阻塞",
										RawSQL: "SELECT pid, usename, xact_start, now()-xact_start AS duration, state, LEFT(query, 80) FROM pg_stat_activity WHERE xact_start IS NOT NULL ORDER BY xact_start LIMIT 10",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "min/avg >= 0.3 — 轻度波动",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "事务提交率有所下降但波动不大"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "持续监控提交率趋势",
							RawSQL: "SELECT datname, xact_commit, xact_rollback FROM pg_stat_database WHERE datname = current_database()",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-006", "PG-024"},
		CausesOf: []string{},
		Tags:     []string{"sql_perf", "commit_rate", "throughput"},
		Versions: "9.6+",
	}
}
