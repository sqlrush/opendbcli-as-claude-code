/*-------------------------------------------------------------------------
 *
 * rules_core.go
 *	  openGauss rule engine — core probe-data classification rules.
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/rules_core.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Core hardcoded rules for OpenGauss ─────────────────────────────────────
//
// 25 rules adapted from PG patterns with OG-specific differences:
// - OpenGauss uses pg_thread_wait_status instead of pg_stat_activity.wait_event
// - Uses `waiting` column instead of `wait_event_type = 'Lock'`
// - gs_* system functions replace some pg_* functions
// - Thread model differs from PG's process model

// coreRules returns the 25 hardcoded OpenGauss diagnostic rules.
func coreRules() []*Rule {
	return []*Rule{
		ruleOG001(), // Vacuum 滞后: 死元组膨胀
		ruleOG002(), // Vacuum 滞后: XID 回卷风险
		ruleOG003(), // 锁等待: 行锁阻塞
		ruleOG004(), // 锁等待: 死锁
		ruleOG005(), // 锁等待: DDL 锁阻塞
		ruleOG006(), // 慢查询: 全表扫描
		ruleOG007(), // 慢查询: 执行时间冲高
		ruleOG008(), // 慢查询: 临时空间冲高
		ruleOG009(), // WAL: WAL 生成速率冲高
		ruleOG010(), // WAL: Checkpoint 频繁
		ruleOG011(), // 连接: 连接数冲高
		ruleOG012(), // 连接: Idle in Transaction 堆积
		ruleOG013(), // IO: 缓存命中率低
		ruleOG014(), // IO: 磁盘排序过多
		ruleOG015(), // 复制: 复制延迟
		ruleOG016(), // Vacuum: autovacuum 被阻塞
		ruleOG017(), // 锁等待: 长事务持锁
		ruleOG018(), // 慢查询: 索引缺失
		ruleOG019(), // WAL: 归档延迟
		ruleOG020(), // 连接: 连接泄漏
		ruleOG021(), // 内存: shared_buffers 不足
		ruleOG022(), // 内存: work_mem 不足
		ruleOG023(), // 复制: 同步复制等待
		ruleOG024(), // 慢查询: 大量排序操作
		ruleOG025(), // Vacuum: 表膨胀严重
	}
}

// ─── OG-001: Vacuum 滞后 — 死元组膨胀 ─────────────────────────────────────

func ruleOG001() *Rule {
	return &Rule{
		ID:       "OG-001",
		Name:     "Vacuum滞后: 死元组膨胀",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
			{Type: SignalKeyword, Key: "vacuum"},
			{Type: SignalKeyword, Key: "死元组"},
			{Type: SignalKeyword, Key: "膨胀"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 20},
			},
		},
		Tree: &TreeNode{
			Step: "检查死元组比例",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("dead_tuple_ratio")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    "死元组占比超过50%",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 死元组膨胀 — 表中死元组占比异常高，Vacuum未能及时清理"},
						{Desc: "死元组比例超过50%，表膨胀严重"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "执行手动VACUUM", RawSQL: "VACUUM VERBOSE;"},
						{Type: ActionFix, Desc: "调整autovacuum参数", RawSQL: "ALTER TABLE <table> SET (autovacuum_vacuum_scale_factor = 0.05);"},
						{Type: ActionInvestigate, Desc: "检查autovacuum是否被长事务阻塞", RawSQL: "SELECT pid, state, query_start, query FROM pg_stat_activity WHERE state = 'idle in transaction' ORDER BY query_start;"},
					},
				},
				{
					Match:    MatchGT(20),
					Label:    "死元组占比偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 死元组膨胀 — Vacuum清理速度跟不上DML更新速度"},
						{Desc: "死元组比例超过20%，需关注Vacuum效率"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "调整autovacuum频率", RawSQL: "ALTER TABLE <table> SET (autovacuum_vacuum_scale_factor = 0.1, autovacuum_vacuum_cost_delay = 10);"},
						{Type: ActionInvestigate, Desc: "查看表Vacuum历史", RawSQL: "SELECT relname, last_vacuum, last_autovacuum, n_dead_tup FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "死元组比例正常范围",
					Findings: []Finding{
						{Desc: "死元组比例在可接受范围内"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-006", "OG-013"},
		Tags:     []string{"vacuum", "bloat", "autovacuum"},
		Versions: "1.0+",
	}
}

// ─── OG-002: Vacuum 滞后 — XID 回卷风险 ────────────────────────────────────

func ruleOG002() *Rule {
	return &Rule{
		ID:       "OG-002",
		Name:     "Vacuum滞后: XID回卷风险",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xid_age_pct"},
			{Type: SignalCategory, Key: "vacuum"},
			{Type: SignalKeyword, Key: "xid"},
			{Type: SignalKeyword, Key: "回卷"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xid_age_pct", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step: "检查XID年龄",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("xid_age_pct")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(80),
					Label:    "XID回卷风险极高",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: XID回卷风险 — XID年龄已超过阈值80%，距回卷极近"},
						{Desc: "XID年龄占比超过80%，必须立即处理"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即执行VACUUM FREEZE", RawSQL: "VACUUM FREEZE;"},
						{Type: ActionInvestigate, Desc: "查看最老的XID", RawSQL: "SELECT datname, age(datfrozenxid) FROM pg_database ORDER BY age(datfrozenxid) DESC;"},
					},
				},
				{
					Match:    MatchGT(50),
					Label:    "XID年龄偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: XID回卷风险 — XID年龄偏高，需提前规划VACUUM FREEZE"},
						{Desc: "XID年龄占比超过50%"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "执行VACUUM FREEZE", RawSQL: "VACUUM FREEZE <table>;"},
						{Type: ActionPrevent, Desc: "降低autovacuum_freeze_max_age", RawSQL: "ALTER SYSTEM SET autovacuum_freeze_max_age = 200000000;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "XID年龄正常",
					Findings: []Finding{
						{Desc: "XID年龄在安全范围内"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "xid", "wraparound"},
		Versions: "1.0+",
	}
}

// ─── OG-003: 锁等待 — 行锁阻塞 ────────────────────────────────────────────

func ruleOG003() *Rule {
	return &Rule{
		ID:       "OG-003",
		Name:     "锁等待: 行锁阻塞",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "blocker_count"},
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "锁"},
			{Type: SignalKeyword, Key: "lock"},
			{Type: SignalKeyword, Key: "阻塞"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step: "检查锁等待数量",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("lock_waits")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "大量锁等待",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 锁等待阻塞 — 大量会话在等待行锁"},
						{Desc: "锁等待数量超过10，存在严重阻塞"},
					},
					Then: &TreeNode{
						Step: "检查是否有阻塞链",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.HasBlockingChains()
						},
						Branches: []Branch{
							{
								Match: MatchBool(true),
								Label: "检测到阻塞链",
								Findings: []Finding{
									{Desc: "存在明确的阻塞链关系"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看阻塞链并考虑终止阻塞源",
										RawSQL: "SELECT w.pid AS waiting_pid, w.query AS waiting_query, l.pid AS blocking_pid, l.query AS blocking_query FROM pg_stat_activity w JOIN pg_locks wl ON w.pid = wl.pid JOIN pg_locks bl ON wl.locktype = bl.locktype AND wl.relation = bl.relation AND wl.pid != bl.pid JOIN pg_stat_activity l ON bl.pid = l.pid WHERE w.waiting = true;"},
									{Type: ActionUrgent, Desc: "终止阻塞源会话", RawSQL: "SELECT pg_terminate_backend(<blocker_pid>);", Risk: "终止会话可能导致事务回滚"},
								},
							},
							{
								Match: MatchDefault(),
								Label: "无明确阻塞链",
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查等待中的会话",
										RawSQL: "SELECT pid, waiting, query, query_start FROM pg_stat_activity WHERE waiting = true ORDER BY query_start;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量锁等待",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 锁等待阻塞 — 存在少量锁等待"},
						{Desc: "锁等待数量偏少，可能是短暂冲突"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前锁信息",
							RawSQL: "SELECT pid, mode, relation::regclass, granted FROM pg_locks WHERE NOT granted;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无锁等待",
					Findings: []Finding{
						{Desc: "当前无锁等待"},
					},
				},
			},
		},
		CausedBy: []string{"OG-017"},
		CausesOf: []string{"OG-007", "OG-011"},
		Tags:     []string{"lock", "blocking", "row_lock"},
		Versions: "1.0+",
	}
}

// ─── OG-004: 锁等待 — 死锁 ────────────────────────────────────────────────

func ruleOG004() *Rule {
	return &Rule{
		ID:       "OG-004",
		Name:     "锁等待: 死锁",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "deadlocks"},
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "死锁"},
			{Type: SignalKeyword, Key: "deadlock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "deadlocks", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step: "检查死锁数",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("deadlocks")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "频繁死锁",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 死锁频繁 — 大量死锁说明应用存在严重的锁顺序问题"},
						{Desc: "死锁次数超过5，需立即排查应用锁顺序"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查死锁日志", RawSQL: "-- 查看OpenGauss日志中的deadlock detected信息"},
						{Type: ActionFix, Desc: "优化应用锁顺序", RawSQL: "-- 确保所有事务以相同顺序访问表和行"},
						{Type: ActionPrevent, Desc: "设置锁超时", RawSQL: "SET lock_timeout = '5s';"},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "偶发死锁",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 死锁 — 检测到死锁事件"},
						{Desc: "存在死锁，需关注应用层锁获取顺序"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看死锁统计", RawSQL: "SELECT datname, deadlocks FROM pg_stat_database WHERE deadlocks > 0;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无死锁",
					Findings: []Finding{
						{Desc: "当前无死锁"},
					},
				},
			},
		},
		CausedBy: []string{"OG-003"},
		CausesOf: []string{},
		Tags:     []string{"lock", "deadlock"},
		Versions: "1.0+",
	}
}

// ─── OG-005: 锁等待 — DDL 锁阻塞 ──────────────────────────────────────────

func ruleOG005() *Rule {
	return &Rule{
		ID:       "OG-005",
		Name:     "锁等待: DDL锁阻塞",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "ddl"},
			{Type: SignalKeyword, Key: "accessexclusive"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step: "检查是否存在AccessExclusive锁",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("blocker_count")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "DDL操作导致锁阻塞",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: DDL锁阻塞 — DDL操作获取AccessExclusive锁，阻塞所有并发访问"},
						{Desc: "存在阻塞者会话，可能为DDL操作持有排他锁"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看持有排他锁的会话",
							RawSQL: "SELECT pid, query, mode FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE mode = 'AccessExclusiveLock';"},
						{Type: ActionFix, Desc: "使用lock_timeout避免DDL长期阻塞", RawSQL: "SET lock_timeout = '3s';"},
						{Type: ActionPrevent, Desc: "DDL操作应在低峰期执行"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无DDL锁阻塞",
					Findings: []Finding{
						{Desc: "未检测到DDL级别的锁阻塞"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-003", "OG-011"},
		Tags:     []string{"lock", "ddl", "accessexclusive"},
		Versions: "1.0+",
	}
}

// ─── OG-006: 慢查询 — 全表扫描 ────────────────────────────────────────────

func ruleOG006() *Rule {
	return &Rule{
		ID:       "OG-006",
		Name:     "慢查询: 全表扫描",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "全表扫描"},
			{Type: SignalKeyword, Key: "seq scan"},
			{Type: SignalKeyword, Key: "慢查询"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step: "检查慢查询数量和缓存命中率",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("long_queries")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "大量慢查询",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 全表扫描 — 大量慢查询可能由缺失索引引起的全表扫描"},
						{Desc: "慢查询数量超过5，需检查执行计划"},
					},
					Then: &TreeNode{
						Step: "检查缓存命中率",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue("cache_hit_pct")
						},
						Branches: []Branch{
							{
								Match: MatchLT(90),
								Label: "缓存命中率低+慢查询多",
								Findings: []Finding{
									{Desc: "缓存命中率低于90%，全表扫描导致大量磁盘IO"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看Top SQL", RawSQL: "SELECT query, calls, mean_time, total_time FROM gs_stat_activity WHERE state = 'active' ORDER BY mean_time DESC LIMIT 10;"},
									{Type: ActionFix, Desc: "为慢查询创建合适的索引"},
								},
							},
							{
								Match: MatchDefault(),
								Label: "缓存命中率正常但有慢查询",
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查慢查询执行计划", RawSQL: "EXPLAIN ANALYZE <slow_query>;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量慢查询",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 全表扫描 — 存在少量慢查询，可能有索引缺失"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前活跃慢查询", RawSQL: "SELECT pid, query_start, query FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '30 seconds';"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无慢查询",
					Findings: []Finding{
						{Desc: "当前无慢查询"},
					},
				},
			},
		},
		CausedBy: []string{"OG-001", "OG-018"},
		CausesOf: []string{"OG-011", "OG-008"},
		Tags:     []string{"slow_query", "seq_scan", "full_table_scan"},
		Versions: "1.0+",
	}
}

// ─── OG-007: 慢查询 — 执行时间冲高 ────────────────────────────────────────

func ruleOG007() *Rule {
	return &Rule{
		ID:       "OG-007",
		Name:     "慢查询: 执行时间冲高",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "慢"},
			{Type: SignalKeyword, Key: "执行时间"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 5},
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 2},
			},
		},
		Tree: &TreeNode{
			Step: "检查活跃会话中慢查询占比",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("long_queries")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "大量长时间运行查询",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 执行时间冲高 — 大量查询执行时间远超正常水平"},
						{Desc: "长时间运行查询超过10个，系统可能处于查询积压状态"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看最长运行查询", RawSQL: "SELECT pid, now() - query_start AS duration, query FROM pg_stat_activity WHERE state = 'active' ORDER BY query_start LIMIT 10;"},
						{Type: ActionFix, Desc: "终止异常长查询", RawSQL: "SELECT pg_terminate_backend(<pid>);", Risk: "终止查询会导致事务回滚"},
					},
				},
				{
					Match:    MatchGT(2),
					Label:    "执行时间偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 执行时间冲高 — 多个查询执行时间偏长"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析慢查询", RawSQL: "SELECT pid, query_start, query FROM pg_stat_activity WHERE state = 'active' AND now() - query_start > interval '30 seconds';"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "查询执行时间正常",
					Findings: []Finding{
						{Desc: "查询执行时间在正常范围内"},
					},
				},
			},
		},
		CausedBy: []string{"OG-003"},
		CausesOf: []string{"OG-011"},
		Tags:     []string{"slow_query", "execution_time"},
		Versions: "1.0+",
	}
}

// ─── OG-008: 慢查询 — 临时空间冲高 ────────────────────────────────────────

func ruleOG008() *Rule {
	return &Rule{
		ID:       "OG-008",
		Name:     "慢查询: 临时空间冲高",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "临时"},
			{Type: SignalKeyword, Key: "temp"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 1048576}, // 1MB/s
			},
		},
		Tree: &TreeNode{
			Step: "检查临时空间使用速率",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("temp_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(104857600), // 100MB/s
					Label:    "临时空间使用量极高",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 临时空间冲高 — 大量查询使用磁盘排序或哈希，临时空间消耗极高"},
						{Desc: "临时空间使用速率超过100MB/s"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "增大work_mem减少磁盘排序", RawSQL: "ALTER SYSTEM SET work_mem = '256MB'; SELECT pg_reload_conf();"},
						{Type: ActionInvestigate, Desc: "查看使用临时空间的查询", RawSQL: "SELECT pid, query FROM pg_stat_activity WHERE state = 'active';"},
					},
				},
				{
					Match:    MatchGT(1048576), // 1MB/s
					Label:    "临时空间使用偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 临时空间冲高 — 部分查询使用磁盘排序"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "适当增大work_mem", RawSQL: "SET work_mem = '64MB';"},
						{Type: ActionInvestigate, Desc: "查看临时文件统计", RawSQL: "SELECT datname, temp_files, temp_bytes FROM pg_stat_database WHERE temp_bytes > 0;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "临时空间使用正常",
					Findings: []Finding{
						{Desc: "临时空间使用率正常"},
					},
				},
			},
		},
		CausedBy: []string{"OG-006", "OG-022"},
		CausesOf: []string{},
		Tags:     []string{"temp_space", "sort", "hash"},
		Versions: "1.0+",
	}
}

// ─── OG-009: WAL — WAL 生成速率冲高 ────────────────────────────────────────

func ruleOG009() *Rule {
	return &Rule{
		ID:       "OG-009",
		Name:     "WAL: WAL生成速率冲高",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
			{Type: SignalKeyword, Key: "wal"},
			{Type: SignalKeyword, Key: "xlog"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 10485760}, // 10MB/s
			},
		},
		Tree: &TreeNode{
			Step: "检查WAL生成速率",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("wal_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(104857600), // 100MB/s
					Label:    "WAL生成速率极高",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: WAL冲高 — WAL生成速率极高，可能由大批量DML或COPY操作引起"},
						{Desc: "WAL速率超过100MB/s，需立即排查"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看当前写入量大的会话", RawSQL: "SELECT pid, query, state FROM pg_stat_activity WHERE state = 'active' ORDER BY pid;"},
						{Type: ActionFix, Desc: "考虑分批执行大事务"},
					},
				},
				{
					Match:    MatchGT(10485760), // 10MB/s
					Label:    "WAL生成速率偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: WAL冲高 — WAL生成速率偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看WAL统计", RawSQL: "SELECT * FROM pg_stat_bgwriter;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "WAL生成速率正常",
					Findings: []Finding{
						{Desc: "WAL生成速率在正常范围内"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-010", "OG-015", "OG-019"},
		Tags:     []string{"wal", "write_ahead_log"},
		Versions: "1.0+",
	}
}

// ─── OG-010: WAL — Checkpoint 频繁 ─────────────────────────────────────────

func ruleOG010() *Rule {
	return &Rule{
		ID:       "OG-010",
		Name:     "WAL: Checkpoint频繁",
		Category: "checkpoint",
		Signals: []Signal{
			{Type: SignalMetric, Key: "checkpoints_req"},
			{Type: SignalCategory, Key: "checkpoint"},
			{Type: SignalKeyword, Key: "checkpoint"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step: "检查请求式Checkpoint数",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("checkpoints_req")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "Checkpoint过于频繁",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: Checkpoint冲高 — 请求式Checkpoint过多，WAL生成速度超过checkpoint_segments容量"},
						{Desc: "Checkpoint请求数超过5，IO压力大"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大max_wal_size", RawSQL: "ALTER SYSTEM SET max_wal_size = '4GB'; SELECT pg_reload_conf();"},
						{Type: ActionFix, Desc: "增大checkpoint_completion_target", RawSQL: "ALTER SYSTEM SET checkpoint_completion_target = 0.9; SELECT pg_reload_conf();"},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "Checkpoint偏多",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: Checkpoint冲高 — Checkpoint频率偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看Checkpoint统计", RawSQL: "SELECT checkpoints_timed, checkpoints_req FROM pg_stat_bgwriter;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "Checkpoint正常",
					Findings: []Finding{
						{Desc: "Checkpoint频率正常"},
					},
				},
			},
		},
		CausedBy: []string{"OG-009"},
		CausesOf: []string{},
		Tags:     []string{"checkpoint", "wal", "io"},
		Versions: "1.0+",
	}
}

// ─── OG-011: 连接 — 连接数冲高 ────────────────────────────────────────────

func ruleOG011() *Rule {
	return &Rule{
		ID:       "OG-011",
		Name:     "连接: 连接数冲高",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "连接"},
			{Type: SignalKeyword, Key: "connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step: "检查连接使用百分比",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("connections_pct")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "连接数接近上限",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 连接数冲高 — 连接数超过90%，新连接即将被拒绝"},
						{Desc: "连接使用率超过90%"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "清理空闲连接", RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND now() - state_change > interval '10 minutes';"},
						{Type: ActionFix, Desc: "增大max_connections或使用连接池", RawSQL: "-- ALTER SYSTEM SET max_connections = 500;\n-- 推荐使用连接池(如PgBouncer)"},
					},
				},
				{
					Match:    MatchGT(70),
					Label:    "连接数偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 连接数冲高 — 连接使用率偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看连接分布", RawSQL: "SELECT state, count(*) FROM pg_stat_activity GROUP BY state;"},
						{Type: ActionPrevent, Desc: "建议使用连接池管理连接"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "连接数正常",
					Findings: []Finding{
						{Desc: "连接使用率在正常范围内"},
					},
				},
			},
		},
		CausedBy: []string{"OG-003", "OG-005", "OG-007"},
		CausesOf: []string{},
		Tags:     []string{"connection", "max_connections"},
		Versions: "1.0+",
	}
}

// ─── OG-012: 连接 — Idle in Transaction 堆积 ──────────────────────────────

func ruleOG012() *Rule {
	return &Rule{
		ID:       "OG-012",
		Name:     "连接: Idle in Transaction堆积",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "idle_in_transaction"},
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "idle"},
			{Type: SignalKeyword, Key: "空闲事务"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "idle_in_transaction", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step: "检查Idle in Transaction会话数",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("idle_in_transaction")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    "大量Idle in Transaction",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: Idle in Transaction堆积 — 大量会话开启事务后未提交/回滚"},
						{Desc: "Idle in Transaction超过20，严重影响Vacuum和连接池"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "终止长时间空闲事务", RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - state_change > interval '10 minutes';"},
						{Type: ActionFix, Desc: "设置idle_in_transaction_session_timeout", RawSQL: "ALTER SYSTEM SET idle_in_transaction_session_timeout = '60s'; SELECT pg_reload_conf();"},
					},
				},
				{
					Match:    MatchGT(3),
					Label:    "Idle in Transaction偏多",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: Idle in Transaction堆积 — 存在未关闭的事务"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看空闲事务详情", RawSQL: "SELECT pid, state_change, query FROM pg_stat_activity WHERE state = 'idle in transaction' ORDER BY state_change;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "Idle in Transaction正常",
					Findings: []Finding{
						{Desc: "Idle in Transaction会话数正常"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-001", "OG-011", "OG-016"},
		Tags:     []string{"idle_in_transaction", "connection_leak"},
		Versions: "1.0+",
	}
}

// ─── OG-013: IO — 缓存命中率低 ────────────────────────────────────────────

func ruleOG013() *Rule {
	return &Rule{
		ID:       "OG-013",
		Name:     "IO: 缓存命中率低",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "cache_hit_pct"},
			{Type: SignalCategory, Key: "memory"},
			{Type: SignalKeyword, Key: "缓存"},
			{Type: SignalKeyword, Key: "命中率"},
			{Type: SignalKeyword, Key: "cache"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "cache_hit_pct", Op: OpLT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step: "检查缓存命中率",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("cache_hit_pct")
			},
			Branches: []Branch{
				{
					Match:    MatchLT(80),
					Label:    "缓存命中率严重偏低",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 缓存命中率低 — 大量数据读取需要磁盘IO，shared_buffers可能不足"},
						{Desc: "缓存命中率低于80%，IO压力大"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大shared_buffers", RawSQL: "ALTER SYSTEM SET shared_buffers = '4GB'; -- 需重启"},
						{Type: ActionInvestigate, Desc: "查看各数据库缓存命中率", RawSQL: "SELECT datname, blks_hit::float / (blks_hit + blks_read) * 100 AS hit_pct FROM pg_stat_database WHERE blks_read > 0;"},
					},
				},
				{
					Match:    MatchLT(90),
					Label:    "缓存命中率偏低",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 缓存命中率低 — 缓存命中率低于90%"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看表级IO统计", RawSQL: "SELECT relname, heap_blks_read, heap_blks_hit FROM pg_statio_user_tables ORDER BY heap_blks_read DESC LIMIT 10;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "缓存命中率正常",
					Findings: []Finding{
						{Desc: "缓存命中率在正常范围内"},
					},
				},
			},
		},
		CausedBy: []string{"OG-001", "OG-021"},
		CausesOf: []string{"OG-006"},
		Tags:     []string{"cache", "shared_buffers", "io"},
		Versions: "1.0+",
	}
}

// ─── OG-014: IO — 磁盘排序过多 ────────────────────────────────────────────

func ruleOG014() *Rule {
	return &Rule{
		ID:       "OG-014",
		Name:     "IO: 磁盘排序过多",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "排序"},
			{Type: SignalKeyword, Key: "sort"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 5242880}, // 5MB/s
			},
		},
		Tree: &TreeNode{
			Step: "检查临时空间使用(排序指标)",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("temp_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(52428800), // 50MB/s
					Label:    "大量磁盘排序",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 磁盘排序过多 — work_mem不足导致排序溢出到磁盘"},
						{Desc: "临时空间速率超过50MB/s，存在大量磁盘排序"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大work_mem", RawSQL: "ALTER SYSTEM SET work_mem = '128MB'; SELECT pg_reload_conf();"},
						{Type: ActionInvestigate, Desc: "查看排序密集的查询", RawSQL: "SELECT query FROM pg_stat_activity WHERE state = 'active';"},
					},
				},
				{
					Match:    MatchGT(5242880),
					Label:    "磁盘排序偏多",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 磁盘排序过多 — 部分查询使用磁盘排序"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "适当增大work_mem", RawSQL: "SET work_mem = '64MB';"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "排序操作正常",
					Findings: []Finding{
						{Desc: "排序操作在正常范围"},
					},
				},
			},
		},
		CausedBy: []string{"OG-022"},
		CausesOf: []string{"OG-008"},
		Tags:     []string{"sort", "disk_sort", "work_mem"},
		Versions: "1.0+",
	}
}

// ─── OG-015: 复制 — 复制延迟 ──────────────────────────────────────────────

func ruleOG015() *Rule {
	return &Rule{
		ID:       "OG-015",
		Name:     "复制: 复制延迟",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag_sec"},
			{Type: SignalCategory, Key: "replication"},
			{Type: SignalKeyword, Key: "复制"},
			{Type: SignalKeyword, Key: "延迟"},
			{Type: SignalKeyword, Key: "replication"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag_sec", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step: "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("replication_lag_sec")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(60),
					Label:    "复制延迟严重",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 复制延迟 — 备库延迟超过60秒，可能影响容灾切换"},
						{Desc: "复制延迟超过60秒"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查备库复制状态", RawSQL: "SELECT application_name, state, sent_location, write_location, flush_location, replay_location FROM pg_stat_replication;"},
						{Type: ActionInvestigate, Desc: "检查网络和IO", RawSQL: "-- 检查主备之间网络延迟和备库IO负载"},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "复制延迟偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 复制延迟 — 复制延迟偏高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看复制统计", RawSQL: "SELECT * FROM pg_stat_replication;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "复制延迟正常",
					Findings: []Finding{
						{Desc: "复制延迟在可接受范围内"},
					},
				},
			},
		},
		CausedBy: []string{"OG-009"},
		CausesOf: []string{},
		Tags:     []string{"replication", "standby", "lag"},
		Versions: "1.0+",
	}
}

// ─── OG-016: Vacuum — autovacuum 被阻塞 ───────────────────────────────────

func ruleOG016() *Rule {
	return &Rule{
		ID:       "OG-016",
		Name:     "Vacuum: autovacuum被阻塞",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalMetric, Key: "idle_in_transaction"},
			{Type: SignalCategory, Key: "vacuum"},
			{Type: SignalKeyword, Key: "autovacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 15},
				{Source: "metrics", Field: "idle_in_transaction", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step: "检查autovacuum阻塞情况",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("idle_in_transaction")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "大量空闲事务阻塞autovacuum",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: autovacuum被阻塞 — 长时间Idle in Transaction阻止autovacuum清理死元组"},
						{Desc: "空闲事务数量超过5，autovacuum无法推进"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "终止长时间空闲事务", RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle in transaction' AND now() - state_change > interval '10 minutes';"},
						{Type: ActionFix, Desc: "设置idle_in_transaction_session_timeout", RawSQL: "ALTER SYSTEM SET idle_in_transaction_session_timeout = '60s'; SELECT pg_reload_conf();"},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "少量空闲事务可能阻塞autovacuum",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: autovacuum被阻塞 — 存在空闲事务，可能影响autovacuum"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看空闲事务", RawSQL: "SELECT pid, state_change, query FROM pg_stat_activity WHERE state = 'idle in transaction' ORDER BY state_change;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "autovacuum运行正常",
					Findings: []Finding{
						{Desc: "autovacuum未被阻塞"},
					},
				},
			},
		},
		CausedBy: []string{"OG-012"},
		CausesOf: []string{"OG-001", "OG-002"},
		Tags:     []string{"autovacuum", "blocking", "idle_in_transaction"},
		Versions: "1.0+",
	}
}

// ─── OG-017: 锁等待 — 长事务持锁 ──────────────────────────────────────────

func ruleOG017() *Rule {
	return &Rule{
		ID:       "OG-017",
		Name:     "锁等待: 长事务持锁",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "长事务"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 1},
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step: "检查长时间持锁的事务",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("lock_waits")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "长事务导致大量锁等待",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 长事务持锁 — 长时间运行的事务持有锁，导致其他会话等待"},
						{Desc: "锁等待数量超过5，长事务是主要原因"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看长事务",
							RawSQL: "SELECT pid, now() - xact_start AS xact_duration, query FROM pg_stat_activity WHERE state != 'idle' ORDER BY xact_start LIMIT 10;"},
						{Type: ActionFix, Desc: "设置statement_timeout限制查询时长", RawSQL: "ALTER SYSTEM SET statement_timeout = '300s'; SELECT pg_reload_conf();"},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "少量锁等待由长事务引起",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 长事务持锁 — 存在长事务持锁"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看锁持有者", RawSQL: "SELECT l.pid, a.query, l.mode, l.granted FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE NOT l.granted;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无长事务持锁",
					Findings: []Finding{
						{Desc: "无长事务持锁问题"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-003"},
		Tags:     []string{"long_transaction", "lock_holder"},
		Versions: "1.0+",
	}
}

// ─── OG-018: 慢查询 — 索引缺失 ────────────────────────────────────────────

func ruleOG018() *Rule {
	return &Rule{
		ID:       "OG-018",
		Name:     "慢查询: 索引缺失",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalMetric, Key: "cache_hit_pct"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "索引"},
			{Type: SignalKeyword, Key: "index"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step: "检查是否存在索引缺失",
			Check: func(ctx *EvalContext) interface{} {
				hitPct := ctx.MetricValue("cache_hit_pct")
				longQ := ctx.MetricValue("long_queries")
				// 低缓存命中+慢查询=可能索引缺失
				if hitPct < 95 && longQ > 1 {
					return longQ
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "可能存在索引缺失",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 索引缺失 — 慢查询多且缓存命中率不高，大概率存在索引缺失"},
						{Desc: "多个慢查询配合低缓存命中率，指向索引缺失"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看顺序扫描多的表", RawSQL: "SELECT relname, seq_scan, idx_scan FROM pg_stat_user_tables WHERE seq_scan > 100 ORDER BY seq_scan DESC LIMIT 10;"},
						{Type: ActionFix, Desc: "使用EXPLAIN分析并创建索引", RawSQL: "EXPLAIN ANALYZE <slow_query>;"},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "轻微索引缺失可能",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 索引缺失 — 可能存在少量索引缺失"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看未使用索引的表", RawSQL: "SELECT relname, seq_scan, idx_scan FROM pg_stat_user_tables WHERE idx_scan = 0 AND seq_scan > 50;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "索引使用正常",
					Findings: []Finding{
						{Desc: "索引使用情况正常"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-006"},
		Tags:     []string{"index", "missing_index", "seq_scan"},
		Versions: "1.0+",
	}
}

// ─── OG-019: WAL — 归档延迟 ──────────────────────────────────────────────

func ruleOG019() *Rule {
	return &Rule{
		ID:       "OG-019",
		Name:     "WAL: 归档延迟",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
			{Type: SignalKeyword, Key: "归档"},
			{Type: SignalKeyword, Key: "archive"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 20971520}, // 20MB/s
			},
		},
		Tree: &TreeNode{
			Step: "检查WAL归档压力",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("wal_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(52428800), // 50MB/s
					Label:    "WAL生成过快可能导致归档延迟",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 归档延迟 — WAL生成速度可能超过归档速度"},
						{Desc: "WAL生成速率超过50MB/s，归档可能跟不上"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查归档状态", RawSQL: "SELECT archived_count, failed_count FROM pg_stat_archiver;"},
						{Type: ActionFix, Desc: "优化归档命令或增加归档带宽"},
					},
				},
				{
					Match:    MatchGT(20971520),
					Label:    "WAL生成偏快",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 归档延迟 — WAL生成速率偏高，注意归档进度"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看归档统计", RawSQL: "SELECT * FROM pg_stat_archiver;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "归档正常",
					Findings: []Finding{
						{Desc: "WAL归档状态正常"},
					},
				},
			},
		},
		CausedBy: []string{"OG-009"},
		CausesOf: []string{},
		Tags:     []string{"wal", "archive", "backup"},
		Versions: "1.0+",
	}
}

// ─── OG-020: 连接 — 连接泄漏 ──────────────────────────────────────────────

func ruleOG020() *Rule {
	return &Rule{
		ID:       "OG-020",
		Name:     "连接: 连接泄漏",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalMetric, Key: "idle_in_transaction"},
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "泄漏"},
			{Type: SignalKeyword, Key: "leak"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step: "检查连接泄漏迹象",
			Check: func(ctx *EvalContext) interface{} {
				connPct := ctx.MetricValue("connections_pct")
				idleTx := ctx.MetricValue("idle_in_transaction")
				// 高连接数+高idle_in_transaction=泄漏嫌疑
				if connPct > 50 && idleTx > 5 {
					return connPct
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Match:    MatchGT(70),
					Label:    "疑似连接泄漏",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 连接泄漏 — 连接使用率高且大量Idle in Transaction，应用可能未正确关闭连接"},
						{Desc: "高连接占用+空闲事务堆积，连接泄漏可能性大"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "按客户端IP统计连接", RawSQL: "SELECT client_addr, count(*) FROM pg_stat_activity GROUP BY client_addr ORDER BY count(*) DESC;"},
						{Type: ActionFix, Desc: "清理泄漏连接", RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND now() - state_change > interval '30 minutes';"},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "轻微连接泄漏嫌疑",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 连接泄漏 — 存在轻微连接泄漏嫌疑"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查连接状态分布", RawSQL: "SELECT state, count(*) FROM pg_stat_activity GROUP BY state;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "无连接泄漏",
					Findings: []Finding{
						{Desc: "未检测到连接泄漏"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-011"},
		Tags:     []string{"connection_leak", "idle"},
		Versions: "1.0+",
	}
}

// ─── OG-021: 内存 — shared_buffers 不足 ────────────────────────────────────

func ruleOG021() *Rule {
	return &Rule{
		ID:       "OG-021",
		Name:     "内存: shared_buffers不足",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "cache_hit_pct"},
			{Type: SignalCategory, Key: "memory"},
			{Type: SignalKeyword, Key: "shared_buffers"},
			{Type: SignalKeyword, Key: "内存"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "cache_hit_pct", Op: OpLT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step: "评估shared_buffers是否足够",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("cache_hit_pct")
			},
			Branches: []Branch{
				{
					Match:    MatchLT(70),
					Label:    "shared_buffers严重不足",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: shared_buffers不足 — 缓存命中率极低，shared_buffers远不能满足工作集"},
						{Desc: "缓存命中率低于70%，shared_buffers严重不足"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "增大shared_buffers（推荐物理内存的25%）", RawSQL: "ALTER SYSTEM SET shared_buffers = '8GB'; -- 需重启OpenGauss"},
						{Type: ActionFix, Desc: "同时调整effective_cache_size", RawSQL: "ALTER SYSTEM SET effective_cache_size = '24GB';"},
					},
				},
				{
					Match:    MatchLT(85),
					Label:    "shared_buffers偏小",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: shared_buffers不足 — 缓存命中率偏低"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "适当增大shared_buffers", RawSQL: "ALTER SYSTEM SET shared_buffers = '4GB'; -- 需重启OpenGauss"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "shared_buffers充足",
					Findings: []Finding{
						{Desc: "shared_buffers大小合理"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-013"},
		Tags:     []string{"shared_buffers", "memory", "cache"},
		Versions: "1.0+",
	}
}

// ─── OG-022: 内存 — work_mem 不足 ──────────────────────────────────────────

func ruleOG022() *Rule {
	return &Rule{
		ID:       "OG-022",
		Name:     "内存: work_mem不足",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "memory"},
			{Type: SignalKeyword, Key: "work_mem"},
			{Type: SignalKeyword, Key: "排序"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 2097152}, // 2MB/s
			},
		},
		Tree: &TreeNode{
			Step: "评估work_mem是否足够",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("temp_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(20971520), // 20MB/s
					Label:    "work_mem严重不足",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: work_mem不足 — 大量排序/哈希操作溢出到磁盘"},
						{Desc: "临时空间使用速率超过20MB/s"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大work_mem", RawSQL: "ALTER SYSTEM SET work_mem = '256MB'; SELECT pg_reload_conf();"},
						{Type: ActionPrevent, Desc: "注意: work_mem是按会话分配，总内存=work_mem*max_connections"},
					},
				},
				{
					Match:    MatchGT(2097152),
					Label:    "work_mem偏小",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: work_mem不足 — 部分操作使用磁盘排序"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "适当增大work_mem", RawSQL: "SET work_mem = '64MB';"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "work_mem充足",
					Findings: []Finding{
						{Desc: "work_mem大小合理"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OG-008", "OG-014"},
		Tags:     []string{"work_mem", "memory", "sort"},
		Versions: "1.0+",
	}
}

// ─── OG-023: 复制 — 同步复制等待 ──────────────────────────────────────────

func ruleOG023() *Rule {
	return &Rule{
		ID:       "OG-023",
		Name:     "复制: 同步复制等待",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag_sec"},
			{Type: SignalMetric, Key: "active_sessions"},
			{Type: SignalCategory, Key: "replication"},
			{Type: SignalKeyword, Key: "同步复制"},
			{Type: SignalKeyword, Key: "synchronous"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag_sec", Op: OpGT, Value: 2},
				{Source: "metrics", Field: "active_sessions", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step: "检查同步复制等待影响",
			Check: func(ctx *EvalContext) interface{} {
				lag := ctx.MetricValue("replication_lag_sec")
				active := ctx.MetricValue("active_sessions")
				// 高活跃会话+复制延迟=同步复制可能拖慢主库
				if lag > 2 && active > 5 {
					return lag
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "同步复制严重拖慢主库",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 同步复制等待 — 同步备库延迟导致主库事务提交被阻塞"},
						{Desc: "复制延迟超过10秒且活跃会话多"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查同步备库状态", RawSQL: "SELECT application_name, sync_state, state FROM pg_stat_replication;"},
						{Type: ActionFix, Desc: "临时切换为异步复制", RawSQL: "ALTER SYSTEM SET synchronous_standby_names = ''; SELECT pg_reload_conf();", Risk: "异步复制可能导致数据丢失"},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "同步复制略有影响",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 同步复制等待 — 存在同步复制等待"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看复制状态", RawSQL: "SELECT * FROM pg_stat_replication;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "同步复制正常",
					Findings: []Finding{
						{Desc: "同步复制未产生影响"},
					},
				},
			},
		},
		CausedBy: []string{"OG-015"},
		CausesOf: []string{"OG-007"},
		Tags:     []string{"synchronous_replication", "sync_standby"},
		Versions: "1.0+",
	}
}

// ─── OG-024: 慢查询 — 大量排序操作 ────────────────────────────────────────

func ruleOG024() *Rule {
	return &Rule{
		ID:       "OG-024",
		Name:     "慢查询: 大量排序操作",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "order by"},
			{Type: SignalKeyword, Key: "排序"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 1048576},
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step: "检查排序相关的慢查询",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("temp_bytes_rate")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(10485760), // 10MB/s
					Label:    "大量排序操作导致查询变慢",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 大量排序操作 — ORDER BY/GROUP BY导致大量磁盘排序，查询性能下降"},
						{Desc: "排序产生的临时空间超过10MB/s"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "为排序字段创建索引", RawSQL: "-- CREATE INDEX idx_sort ON <table>(<sort_column>);"},
						{Type: ActionFix, Desc: "增大work_mem", RawSQL: "SET work_mem = '128MB';"},
						{Type: ActionInvestigate, Desc: "查看使用排序的查询", RawSQL: "SELECT query FROM pg_stat_activity WHERE state = 'active';"},
					},
				},
				{
					Match:    MatchGT(1048576),
					Label:    "排序操作偏多",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "根因: 排序操作偏多 — 存在排序操作导致的性能影响"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析排序查询执行计划", RawSQL: "EXPLAIN ANALYZE <query>;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "排序操作正常",
					Findings: []Finding{
						{Desc: "排序操作量在正常范围"},
					},
				},
			},
		},
		CausedBy: []string{"OG-022"},
		CausesOf: []string{"OG-008"},
		Tags:     []string{"sort", "order_by", "group_by"},
		Versions: "1.0+",
	}
}

// ─── OG-025: Vacuum — 表膨胀严重 ──────────────────────────────────────────

func ruleOG025() *Rule {
	return &Rule{
		ID:       "OG-025",
		Name:     "Vacuum: 表膨胀严重",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
			{Type: SignalKeyword, Key: "膨胀"},
			{Type: SignalKeyword, Key: "bloat"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step: "检查表膨胀程度",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("dead_tuple_ratio")
			},
			Branches: []Branch{
				{
					Match:    MatchGT(60),
					Label:    "表膨胀极为严重",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "根因: 表膨胀严重 — 死元组占比超过60%，表实际使用空间远大于有效数据"},
						{Desc: "表膨胀率超过60%，需要VACUUM FULL回收空间"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "执行VACUUM FULL回收空间", RawSQL: "VACUUM FULL <table>;", Risk: "VACUUM FULL会锁表，影响在线访问"},
						{Type: ActionPrevent, Desc: "使用pg_repack替代VACUUM FULL", RawSQL: "-- pg_repack --table <table> --no-order"},
					},
				},
				{
					Match:    MatchGT(30),
					Label:    "表膨胀较严重",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "根因: 表膨胀严重 — 死元组占比超过30%"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "执行VACUUM", RawSQL: "VACUUM VERBOSE <table>;"},
						{Type: ActionInvestigate, Desc: "查看膨胀最严重的表", RawSQL: "SELECT relname, n_dead_tup, n_live_tup, round(n_dead_tup::numeric / NULLIF(n_live_tup + n_dead_tup, 0) * 100, 1) AS dead_pct FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;"},
					},
				},
				{
					Match: MatchDefault(),
					Label: "表膨胀可控",
					Findings: []Finding{
						{Desc: "表膨胀在可接受范围"},
					},
				},
			},
		},
		CausedBy: []string{"OG-001", "OG-016"},
		CausesOf: []string{"OG-013"},
		Tags:     []string{"bloat", "vacuum_full", "table_size"},
		Versions: "1.0+",
	}
}
