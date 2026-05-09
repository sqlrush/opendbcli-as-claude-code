/*-------------------------------------------------------------------------
 *
 * rules_extended.go
 *	  PostgreSQL rule engine — extended classification rules (newer than the core set, kept separate to ease review).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/ruleengine/rules_extended.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// extendedRules returns 55 additional PostgreSQL diagnostic rules (PG-026 ~ PG-080)
// covering deep vacuum, lock, query, WAL/checkpoint, connection, configuration,
// IO/storage, and monitoring scenarios.
func extendedRules() []*Rule {
	return []*Rule{
		// ── Vacuum Deep (8): PG-026 ~ PG-033 ──
		rulePG026AutovacuumMaxWorkers(),
		rulePG027VacuumCostDelay(),
		rulePG028FreezeAgePerTable(),
		rulePG029VacuumProgress(),
		rulePG030WraparoundEmergency(),
		rulePG031TOASTVacuum(),
		rulePG032AntiWraparoundVacuum(),
		rulePG033VacuumDeadTupleThreshold(),

		// ── Lock Deep (7): PG-034 ~ PG-040 ──
		rulePG034AdvisoryLockLeak(),
		rulePG035RelationLockWait(),
		rulePG036TupleLockContention(),
		rulePG037DDLLockBlocking(),
		rulePG038LockTimeoutConfig(),
		rulePG039LockQueueDepth(),
		rulePG040PgLocksAnalysis(),

		// ── Query Deep (10): PG-041 ~ PG-050 ──
		rulePG041MissingIndexes(),
		rulePG042UnusedIndexes(),
		rulePG043IndexBloat(),
		rulePG044PartitioningSuggestion(),
		rulePG045JITCompilationIssues(),
		rulePG046ParallelQueryUnderutilized(),
		rulePG047CTEPerformance(),
		rulePG048WindowFunctionSpill(),
		rulePG049PreparedStatementLeak(),
		rulePG050QueryPlanInstability(),

		// ── WAL/Checkpoint Deep (5): PG-051 ~ PG-055 ──
		rulePG051WALSegmentAccumulation(),
		rulePG052ArchiveCommandFailure(),
		rulePG053BasebackupImpact(),
		rulePG054SynchronousStandbyLag(),
		rulePG055WALLevelConfig(),

		// ── Connection Deep (5): PG-056 ~ PG-060 ──
		rulePG056PgbouncerSaturation(),
		rulePG057ConnectionAge(),
		rulePG058PreparedTransactionLeak(),
		rulePG059TwoPhaseCommitOrphan(),
		rulePG060BackendProcessLeak(),

		// ── Configuration (10): PG-061 ~ PG-070 ──
		rulePG061SharedBuffersSizing(),
		rulePG062WorkMemTuning(),
		rulePG063EffectiveCacheSize(),
		rulePG064MaintenanceWorkMem(),
		rulePG065RandomPageCost(),
		rulePG066CheckpointCompletionTarget(),
		rulePG067WALBuffers(),
		rulePG068BgwriterLruMaxpages(),
		rulePG069LogMinDurationStatement(),
		rulePG070AutovacuumNaptime(),

		// ── IO/Storage (5): PG-071 ~ PG-075 ──
		rulePG071TablespaceFull(),
		rulePG072TempTablespace(),
		rulePG073PgStatIOAnalysis(),
		rulePG074SeqScanLargeTable(),
		rulePG075WALDirectoryGrowth(),

		// ── Monitoring (5): PG-076 ~ PG-080 ──
		rulePG076PgStatStatementsMissing(),
		rulePG077AutoExplainNotConfigured(),
		rulePG078LogCheckpointsOff(),
		rulePG079TrackActivitiesOff(),
		rulePG080PgStatKcacheMissing(),
		// ── Wait Event (1): PG-081 ──
		rulePG081LWLockContention(),
		// ── Parameter Audit (5): PG-082 ~ PG-086 ──
		rulePG082SlowQueryLogDisabled(),
		rulePG083ParallelQueryDisabled(),
		rulePG084StatisticsTargetLow(),
		rulePG085RandomPageCostHigh(),
		rulePG086PgStatStatementsNotInstalled(),
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Vacuum Deep Rules (PG-026 ~ PG-033)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG026AutovacuumMaxWorkers() *Rule {
	return &Rule{
		ID:       "PG-026",
		Name:     "Autovacuum Worker 数量不足",
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
			Step:  "检查死元组堆积程度判断 worker 是否不足",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 30% — worker 严重不足",
					Match:    MatchGT(30),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "死元组比例超过 30%，autovacuum_max_workers 可能不足"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 autovacuum worker 状态",
							RawSQL: "SELECT count(*) AS running_workers FROM pg_stat_activity WHERE backend_type = 'autovacuum worker'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 autovacuum_max_workers",
							RawSQL: "ALTER SYSTEM SET autovacuum_max_workers = 6; -- 需重启",
							Risk:   "CPU/IO 消耗增加", Rollback: "ALTER SYSTEM RESET autovacuum_max_workers;"},
					},
				},
				{
					Label:    "dead_tuple_ratio 15-30% — worker 可能不足",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "死元组比例偏高，autovacuum 回收速度跟不上"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 autovacuum 配置",
							RawSQL: "SHOW autovacuum_max_workers;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-001"},
		CausesOf: []string{"PG-003"},
		Tags:     []string{"vacuum", "autovacuum", "worker"},
		Versions: "9.6+",
	}
}

func rulePG027VacuumCostDelay() *Rule {
	return &Rule{
		ID:       "PG-027",
		Name:     "Vacuum Cost Delay 过高",
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
			Step:  "检查死元组比例判断 vacuum cost delay 是否过大",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 20% — cost delay 可能过高",
					Match:    MatchGT(20),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "死元组堆积严重，autovacuum_vacuum_cost_delay 可能设置过高导致回收缓慢"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 cost delay 配置",
							RawSQL: "SHOW autovacuum_vacuum_cost_delay;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "降低 autovacuum_vacuum_cost_delay",
							RawSQL: "ALTER SYSTEM SET autovacuum_vacuum_cost_delay = '2ms'; SELECT pg_reload_conf();",
							Risk:   "IO 压力增加", Rollback: "ALTER SYSTEM RESET autovacuum_vacuum_cost_delay; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "dead_tuple_ratio 10-20%",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "死元组比例偏高，考虑降低 vacuum cost delay 加速回收"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 vacuum cost 相关参数",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE '%vacuum_cost%'",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-001"},
		Tags:     []string{"vacuum", "cost_delay", "tuning"},
		Versions: "9.6+",
	}
}

func rulePG028FreezeAgePerTable() *Rule {
	return &Rule{
		ID:       "PG-028",
		Name:     "单表 Freeze Age 偏高",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xid_age_pct"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xid_age_pct", Op: OpGT, Value: 40},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查 XID 年龄占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 60% — 高风险表需冻结",
					Match:    MatchGT(60),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "单表 freeze age 偏高，需要针对性 VACUUM FREEZE"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出 freeze age 最大的表",
							RawSQL: "SELECT n.nspname, c.relname, age(c.relfrozenxid) AS xid_age, pg_size_pretty(pg_total_relation_size(c.oid)) AS size FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid WHERE c.relkind = 'r' ORDER BY age(c.relfrozenxid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对高 age 表执行 VACUUM FREEZE",
							RawSQL: "VACUUM FREEZE {schema}.{table};",
							Risk:   "长时间运行", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "xid_age_pct 40-60%",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "XID 年龄偏高，建议提前安排 VACUUM FREEZE"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "设置较低的 vacuum_freeze_min_age",
							RawSQL: "ALTER SYSTEM SET vacuum_freeze_min_age = 50000000; SELECT pg_reload_conf();",
							Risk:   "无", Rollback: "ALTER SYSTEM RESET vacuum_freeze_min_age; SELECT pg_reload_conf();"},
					},
				},
			},
		},
		CausedBy: []string{"PG-001"},
		CausesOf: []string{"PG-003"},
		Tags:     []string{"vacuum", "freeze", "xid"},
		Versions: "9.6+",
	}
}

func rulePG029VacuumProgress() *Rule {
	return &Rule{
		ID:       "PG-029",
		Name:     "Vacuum 进度缓慢",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查 VACUUM 进度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "有活跃 VACUUM 运行中",
					Match:    MatchGT(5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "VACUUM 运行中但死元组仍偏高，可能进度缓慢"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 VACUUM 进度",
							RawSQL: "SELECT pid, datname, relid::regclass AS table_name, phase, heap_blks_total, heap_blks_scanned, heap_blks_vacuumed, CASE WHEN heap_blks_total > 0 THEN ROUND(100.0 * heap_blks_vacuumed / heap_blks_total, 1) ELSE 0 END AS pct_done FROM pg_stat_progress_vacuum",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "VACUUM 运行正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "VACUUM 进度正常"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "progress"},
		Versions: "9.6+",
	}
}

func rulePG030WraparoundEmergency() *Rule {
	return &Rule{
		ID:       "PG-030",
		Name:     "XID 回卷紧急状态",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xid_age_pct"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xid_age_pct", Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 XID 是否已进入紧急回卷区域",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 95% — 数据库即将拒绝写入",
					Match:    MatchGT(95),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "XID 年龄已达 95% 以上，数据库即将拒绝所有写操作"},
						{Desc: "需要立即停止所有非紧急业务，执行 VACUUM FREEZE"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即终止长事务并执行 VACUUM FREEZE",
							RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE backend_xid IS NOT NULL AND pid <> pg_backend_pid() AND state = 'idle in transaction' AND query_start < now() - interval '10 minutes'",
							Risk:   "终止长事务可能影响业务", Rollback: "无需回滚"},
						{Type: ActionUrgent, Desc: "对最老数据库执行 VACUUM FREEZE",
							RawSQL: "VACUUM FREEZE;",
							Risk:   "运行时间长", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "xid_age_pct 90-95% — 紧急预警",
					Match:    MatchDefault(),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "XID 年龄已达 90% 以上，处于紧急状态"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "规划 VACUUM FREEZE 窗口",
							RawSQL: "SELECT datname, age(datfrozenxid) AS xid_age, ROUND(age(datfrozenxid)::numeric / 2147483647 * 100, 1) AS pct FROM pg_database ORDER BY xid_age DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-003", "PG-028"},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "xid", "wraparound", "emergency"},
		Versions: "9.6+",
	}
}

func rulePG031TOASTVacuum() *Rule {
	return &Rule{
		ID:       "PG-031",
		Name:     "TOAST 表 Vacuum 滞后",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
			{Type: SignalKeyword, Key: "toast"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 TOAST 表膨胀状况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 10% — 可能存在 TOAST 膨胀",
					Match:    MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "TOAST 表可能存在膨胀，大字段更新频繁时 TOAST 表不会被自动回收"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 TOAST 表大小",
							RawSQL: "SELECT c.relname AS main_table, t.relname AS toast_table, pg_size_pretty(pg_relation_size(t.oid)) AS toast_size FROM pg_class c JOIN pg_class t ON c.reltoastrelid = t.oid WHERE pg_relation_size(t.oid) > 100*1024*1024 ORDER BY pg_relation_size(t.oid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对含大 TOAST 的表执行 VACUUM FULL",
							RawSQL: "VACUUM FULL {schema}.{table}; -- 会锁表",
							Risk:   "ACCESS EXCLUSIVE LOCK", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "TOAST 膨胀风险低",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "TOAST 表膨胀风险低"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-002"},
		Tags:     []string{"vacuum", "toast", "bloat"},
		Versions: "9.6+",
	}
}

func rulePG032AntiWraparoundVacuum() *Rule {
	return &Rule{
		ID:       "PG-032",
		Name:     "Anti-Wraparound Vacuum 被触发",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "xid_age_pct"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "xid_age_pct", Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有 anti-wraparound vacuum 运行",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 70% — anti-wraparound 可能已触发",
					Match:    MatchGT(70),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "XID 年龄偏高，anti-wraparound vacuum 可能已被触发"},
						{Desc: "anti-wraparound vacuum 不可被取消，会导致 IO 冲高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看正在运行的 autovacuum",
							RawSQL: "SELECT pid, datname, relid::regclass, query FROM pg_stat_activity WHERE backend_type = 'autovacuum worker' AND query LIKE '%wraparound%'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看 VACUUM 进度",
							RawSQL: "SELECT pid, relid::regclass, phase, heap_blks_total, heap_blks_vacuumed FROM pg_stat_progress_vacuum",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "未达 anti-wraparound 阈值",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "XID 年龄偏高但未达 anti-wraparound 阈值"}},
				},
			},
		},
		CausedBy: []string{"PG-003"},
		CausesOf: []string{},
		Tags:     []string{"vacuum", "anti-wraparound", "xid"},
		Versions: "9.6+",
	}
}

func rulePG033VacuumDeadTupleThreshold() *Rule {
	return &Rule{
		ID:       "PG-033",
		Name:     "Vacuum Dead Tuple 阈值配置不当",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 8},
			},
			SkipWhen: vacuumSkipWhenNonVacuumAnomaly(),
		},
		Tree: &TreeNode{
			Step:  "检查 autovacuum 触发阈值是否合理",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 8% — 阈值可能过高",
					Match:    MatchGT(8),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "默认 autovacuum_vacuum_scale_factor=0.2 对大表来说阈值过高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看大表的 autovacuum 配置",
							RawSQL: "SELECT n.nspname, c.relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size, s.n_dead_tup, COALESCE((SELECT option_value FROM pg_options_to_table(c.reloptions) WHERE option_name = 'autovacuum_vacuum_scale_factor'), 'default') AS scale_factor FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid JOIN pg_stat_user_tables s ON c.oid = s.relid WHERE c.relkind = 'r' AND pg_total_relation_size(c.oid) > 1073741824 ORDER BY s.n_dead_tup DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对大表设置更低的 scale_factor",
							RawSQL: "ALTER TABLE {table} SET (autovacuum_vacuum_scale_factor = 0.01, autovacuum_vacuum_threshold = 1000)",
							Risk:   "autovacuum 更频繁", Rollback: "ALTER TABLE {table} RESET (autovacuum_vacuum_scale_factor, autovacuum_vacuum_threshold)"},
					},
				},
				{
					Label:    "配置合理",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "autovacuum 触发阈值合理"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-001"},
		Tags:     []string{"vacuum", "threshold", "tuning"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Lock Deep Rules (PG-034 ~ PG-040)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG034AdvisoryLockLeak() *Rule {
	return &Rule{
		ID:       "PG-034",
		Name:     "Advisory Lock 泄漏深度分析",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "advisory"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step: "检查 advisory lock 持有情况",
			Check: func(ctx *EvalContext) interface{} {
				for _, w := range ctx.WaitProfile {
					if w.WaitEvent == "advisory" || w.WaitEventType == "Lock" {
						return w.Percentage
					}
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Label:    "advisory lock 等待占比 > 5%",
					Match:    MatchGT(5),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "advisory lock 等待占比高，可能存在未释放的 advisory lock"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 advisory lock 持有情况",
							RawSQL: "SELECT pid, classid, objid, objsubid, mode, granted FROM pg_locks WHERE locktype = 'advisory' ORDER BY pid",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "释放泄漏的 advisory lock",
							RawSQL: "SELECT pg_advisory_unlock_all(); -- 在泄漏会话中执行",
							Risk:   "可能影响业务逻辑", Rollback: "无"},
					},
				},
				{
					Label:    "advisory lock 正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "advisory lock 使用正常"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-006"},
		Tags:     []string{"lock", "advisory"},
		Versions: "9.6+",
	}
}

func rulePG035RelationLockWait() *Rule {
	return &Rule{
		ID:       "PG-035",
		Name:     "Relation Lock 等待",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 relation lock 等待数",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "lock_waits > 10 — 严重 relation lock 竞争",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "大量 relation lock 等待，表级锁竞争严重"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 relation lock 等待详情",
							RawSQL: "SELECT l.pid, l.locktype, l.relation::regclass, l.mode, l.granted, a.state, LEFT(a.query, 80) AS query FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE NOT l.granted ORDER BY l.pid",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "lock_waits 5-10",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在 relation lock 等待，需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查锁等待",
							RawSQL: "SELECT pid, wait_event_type, wait_event, state, LEFT(query, 60) FROM pg_stat_activity WHERE wait_event_type = 'Lock'",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"lock", "relation", "wait"},
		Versions: "9.6+",
	}
}

func rulePG036TupleLockContention() *Rule {
	return &Rule{
		ID:       "PG-036",
		Name:     "Tuple Lock 争用",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step: "检查 tuple lock 等待比例",
			Check: func(ctx *EvalContext) interface{} {
				for _, w := range ctx.WaitProfile {
					if w.WaitEvent == "tuple" {
						return w.Percentage
					}
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Label:    "tuple lock 等待占比 > 10%",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "tuple lock 争用占比高，多个事务竞争同一行"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 tuple lock 等待",
							RawSQL: "SELECT pid, wait_event, relation::regclass, LEFT(query, 80) FROM pg_stat_activity WHERE wait_event = 'tuple'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "tuple lock 正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "tuple lock 争用在可控范围"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-006"},
		Tags:     []string{"lock", "tuple", "contention"},
		Versions: "9.6+",
	}
}

func rulePG037DDLLockBlocking() *Rule {
	return &Rule{
		ID:       "PG-037",
		Name:     "DDL 锁阻塞查询",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "ddl"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有 DDL 锁阻塞",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "lock_waits > 3 — 可能有 DDL 锁",
					Match:    MatchGT(3),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "可能有 ALTER TABLE / CREATE INDEX 等 DDL 操作持有 AccessExclusiveLock 阻塞查询"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 AccessExclusive 锁持有者",
							RawSQL: "SELECT l.pid, a.usename, l.relation::regclass, l.mode, a.state, LEFT(a.query, 80) FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE l.mode = 'AccessExclusiveLock' AND l.granted",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "使用 CONCURRENTLY 选项避免阻塞",
							RawSQL: "CREATE INDEX CONCURRENTLY idx_name ON table_name (column);",
							Risk:   "CONCURRENTLY 索引构建较慢", Rollback: "DROP INDEX idx_name;"},
					},
				},
				{
					Label:    "DDL 锁影响可控",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "DDL 锁影响在可控范围"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"lock", "ddl", "blocking"},
		Versions: "9.6+",
	}
}

func rulePG038LockTimeoutConfig() *Rule {
	return &Rule{
		ID:       "PG-038",
		Name:     "Lock Timeout 未配置",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 2},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "存在锁等待 — 检查 lock_timeout 配置",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在锁等待，lock_timeout 可能未配置或设置过大"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 lock_timeout 和 deadlock_timeout",
							RawSQL: "SELECT name, setting, unit FROM pg_settings WHERE name IN ('lock_timeout', 'deadlock_timeout', 'statement_timeout')",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "建议配置 lock_timeout",
							RawSQL: "ALTER SYSTEM SET lock_timeout = '30s'; SELECT pg_reload_conf();",
							Risk:   "持锁超时的操作会失败", Rollback: "ALTER SYSTEM RESET lock_timeout; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "锁等待正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "锁等待在正常范围"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "timeout", "config"},
		Versions: "9.6+",
	}
}

func rulePG039LockQueueDepth() *Rule {
	return &Rule{
		ID:       "PG-039",
		Name:     "Lock 队列深度过深",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalMetric, Key: "blocker_count"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "blocker_count", Op: OpGT, Value: 2},
			},
		},
		Tree: &TreeNode{
			Step:  "检查阻塞者数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("blocker_count") },
			Branches: []Branch{
				{
					Label:    "blocker_count > 5 — 阻塞链严重",
					Match:    MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "阻塞者超过 5 个，锁链式阻塞正在扩散"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看阻塞链",
							RawSQL: "SELECT blocked_locks.pid AS blocked_pid, blocked_activity.usename AS blocked_user, LEFT(blocked_activity.query, 60) AS blocked_query, blocking_locks.pid AS blocking_pid, blocking_activity.usename AS blocking_user, LEFT(blocking_activity.query, 60) AS blocking_query FROM pg_catalog.pg_locks blocked_locks JOIN pg_catalog.pg_stat_activity blocked_activity ON blocked_activity.pid = blocked_locks.pid JOIN pg_catalog.pg_locks blocking_locks ON blocking_locks.locktype = blocked_locks.locktype AND blocking_locks.database IS NOT DISTINCT FROM blocked_locks.database AND blocking_locks.relation IS NOT DISTINCT FROM blocked_locks.relation AND blocking_locks.page IS NOT DISTINCT FROM blocked_locks.page AND blocking_locks.tuple IS NOT DISTINCT FROM blocked_locks.tuple AND blocking_locks.transactionid IS NOT DISTINCT FROM blocked_locks.transactionid AND blocking_locks.pid != blocked_locks.pid JOIN pg_catalog.pg_stat_activity blocking_activity ON blocking_activity.pid = blocking_locks.pid WHERE NOT blocked_locks.granted",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "blocker_count 2-5",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在多个阻塞者，锁队列需关注"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看未授予的锁",
							RawSQL: "SELECT pid, locktype, relation::regclass, mode, granted FROM pg_locks WHERE NOT granted",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-006"},
		Tags:     []string{"lock", "queue", "blocking_chain"},
		Versions: "9.6+",
	}
}

func rulePG040PgLocksAnalysis() *Rule {
	return &Rule{
		ID:       "PG-040",
		Name:     "pg_locks 综合分析",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lock_waits"},
			{Type: SignalCategory, Key: "lock"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "lock_waits", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查锁等待总数",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "存在锁等待",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在锁等待，执行 pg_locks 综合分析"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "pg_locks 锁类型分布",
							RawSQL: "SELECT locktype, mode, COUNT(*) AS cnt, SUM(CASE WHEN granted THEN 0 ELSE 1 END) AS waiting FROM pg_locks GROUP BY locktype, mode ORDER BY waiting DESC, cnt DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "最长等待的锁",
							RawSQL: "SELECT a.pid, a.usename, a.wait_event_type, a.wait_event, now() - a.state_change AS wait_duration, LEFT(a.query, 80) FROM pg_stat_activity a WHERE a.wait_event_type = 'Lock' ORDER BY a.state_change LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无锁等待",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无锁等待"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "analysis"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Query Deep Rules (PG-041 ~ PG-050)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG041MissingIndexes() *Rule {
	return &Rule{
		ID:       "PG-041",
		Name:     "缺失索引导致全表扫描",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "seq_scan_heavy_tables"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "索引"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step: "检查是否有高频全表扫描",
			Check: func(ctx *EvalContext) interface{} {
				return ctx.MetricValue("long_queries")
			},
			Branches: []Branch{
				{
					Label:    "存在慢查询 — 可能缺失索引",
					Match:    MatchGT(1),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在慢查询，可能因缺失索引导致全表扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看顺序扫描最多的表",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read, idx_scan, CASE WHEN seq_scan + idx_scan > 0 THEN ROUND(100.0 * seq_scan / (seq_scan + idx_scan), 1) ELSE 0 END AS seq_pct FROM pg_stat_user_tables WHERE seq_scan > 1000 ORDER BY seq_tup_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看 pg_stat_statements 中 full scan 的 SQL",
							RawSQL: "SELECT queryid, calls, ROUND((mean_exec_time/1000)::numeric, 2) AS mean_sec, rows, LEFT(query, 100) FROM pg_stat_statements WHERE shared_blks_read > 10000 ORDER BY shared_blks_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无明显缺失索引迹象",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无明显缺失索引迹象"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-012", "PG-011"},
		Tags:     []string{"index", "missing", "seq_scan"},
		Versions: "9.6+",
	}
}

func rulePG042UnusedIndexes() *Rule {
	return &Rule{
		ID:       "PG-042",
		Name:     "未使用索引浪费资源",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "unused index"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查慢查询情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "缓存命中率低或中 — 可能有无用索引浪费缓存",
					Match:    MatchLT(99),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "未使用的索引浪费磁盘空间和缓存，增加 VACUUM 开销"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出未使用的索引",
							RawSQL: "SELECT s.schemaname, s.relname AS table_name, s.indexrelname AS index_name, pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size, s.idx_scan FROM pg_stat_user_indexes s WHERE s.idx_scan = 0 AND pg_relation_size(s.indexrelid) > 1048576 ORDER BY pg_relation_size(s.indexrelid) DESC LIMIT 20",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "删除未使用索引释放空间",
							RawSQL: "DROP INDEX CONCURRENTLY {index_name};",
							Risk:   "确认索引确实不被应用使用", Rollback: "CREATE INDEX CONCURRENTLY ..."},
					},
				},
				{
					Label:    "缓存充足，影响可控",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "缓存命中率高，无用索引影响有限"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"index", "unused", "bloat"},
		Versions: "9.6+",
	}
}

func rulePG043IndexBloat() *Rule {
	return &Rule{
		ID:       "PG-043",
		Name:     "索引膨胀",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "bloated_tables"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "bloated_tables", Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查索引膨胀情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("bloated_tables") },
			Branches: []Branch{
				{
					Label:    "bloated_tables > 1 — 可能有索引膨胀",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "表膨胀可能伴随索引膨胀，索引膨胀导致查询性能下降"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "估算索引膨胀率",
							RawSQL: "SELECT nspname, relname, indexrelname, pg_size_pretty(pg_relation_size(indexrelid)) AS idx_size, idx_scan FROM pg_stat_user_indexes JOIN pg_class ON pg_stat_user_indexes.indexrelid = pg_class.oid JOIN pg_namespace ON pg_class.relnamespace = pg_namespace.oid WHERE pg_relation_size(indexrelid) > 104857600 ORDER BY pg_relation_size(indexrelid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "重建膨胀索引",
							RawSQL: "REINDEX INDEX CONCURRENTLY {index_name};",
							Risk:   "消耗 CPU/IO，短暂锁定", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "无明显索引膨胀",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "索引膨胀不明显"}},
				},
			},
		},
		CausedBy: []string{"PG-001"},
		CausesOf: []string{"PG-014"},
		Tags:     []string{"index", "bloat", "reindex"},
		Versions: "12+",
	}
}

func rulePG044PartitioningSuggestion() *Rule {
	return &Rule{
		ID:       "PG-044",
		Name:     "大表分区建议",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "分区"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查大表全表扫描情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — 检查是否需要分区",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "大表全表扫描耗时长，考虑按时间或业务键分区"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出最大的非分区表",
							RawSQL: "SELECT n.nspname, c.relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS total_size, s.seq_scan, s.n_live_tup FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid JOIN pg_stat_user_tables s ON c.oid = s.relid WHERE c.relkind = 'r' AND NOT EXISTS (SELECT 1 FROM pg_inherits WHERE inhparent = c.oid) AND pg_total_relation_size(c.oid) > 10737418240 ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "暂无分区需求",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无明显分区需求"}},
				},
			},
		},
		Tags:     []string{"partition", "large_table"},
		Versions: "10+",
	}
}

func rulePG045JITCompilationIssues() *Rule {
	return &Rule{
		ID:       "PG-045",
		Name:     "JIT 编译开销过大",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "jit"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查是否有慢查询可能受 JIT 影响",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — JIT 可能带来额外开销",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "JIT 编译对短查询可能带来额外开销，OLTP 场景建议关闭"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 JIT 配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE 'jit%'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "OLTP 场景关闭 JIT",
							RawSQL: "ALTER SYSTEM SET jit = off; SELECT pg_reload_conf();",
							Risk:   "分析型查询可能变慢", Rollback: "ALTER SYSTEM SET jit = on; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "JIT 影响不明显",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "JIT 影响不明显"}},
				},
			},
		},
		Tags:     []string{"jit", "performance"},
		Versions: "11+",
	}
}

func rulePG046ParallelQueryUnderutilized() *Rule {
	return &Rule{
		ID:       "PG-046",
		Name:     "并行查询未充分利用",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "parallel"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询情况判断并行查询利用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — 可能未启用并行",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "大表扫描慢查询可能因未启用并行查询导致性能不佳"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看并行查询配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name IN ('max_parallel_workers_per_gather', 'max_parallel_workers', 'parallel_tuple_cost', 'parallel_setup_cost', 'min_parallel_table_scan_size')",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "启用并行查询",
							RawSQL: "ALTER SYSTEM SET max_parallel_workers_per_gather = 4; SELECT pg_reload_conf();",
							Risk:   "CPU 消耗增加", Rollback: "ALTER SYSTEM SET max_parallel_workers_per_gather = 2; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "慢查询不多",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前慢查询不多，并行查询需求不迫切"}},
				},
			},
		},
		Tags:     []string{"parallel", "query"},
		Versions: "9.6+",
	}
}

func rulePG047CTEPerformance() *Rule {
	return &Rule{
		ID:       "PG-047",
		Name:     "CTE 性能问题",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "cte"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查是否有慢查询可能涉及 CTE",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "PG 12 之前 CTE 是优化屏障(optimization fence)，12+ 可用 MATERIALIZED/NOT MATERIALIZED 控制"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看包含 WITH 的慢查询",
							RawSQL: "SELECT queryid, calls, mean_exec_time/1000 AS mean_sec, LEFT(query, 120) FROM pg_stat_statements WHERE query ILIKE '%WITH%' ORDER BY mean_exec_time DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无明显 CTE 问题",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无明显 CTE 性能问题"}},
				},
			},
		},
		Tags:     []string{"cte", "query_optimization"},
		Versions: "9.6+",
	}
}

func rulePG048WindowFunctionSpill() *Rule {
	return &Rule{
		ID:       "PG-048",
		Name:     "窗口函数溢出到磁盘",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 10485760}, // 10MB/s
			},
		},
		Tree: &TreeNode{
			Step:  "检查临时空间使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "temp_bytes_rate > 50MB/s — 大量磁盘排序",
					Match:    MatchGT(52428800),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "临时空间写入速率高，窗口函数或排序操作溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看临时文件使用",
							RawSQL: "SELECT datname, temp_files, pg_size_pretty(temp_bytes) AS temp_size FROM pg_stat_database WHERE temp_bytes > 0 ORDER BY temp_bytes DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 work_mem 减少磁盘溢出",
							RawSQL: "ALTER SYSTEM SET work_mem = '256MB'; SELECT pg_reload_conf();",
							Risk:   "内存消耗增加", Rollback: "ALTER SYSTEM RESET work_mem; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "temp_bytes_rate 10-50MB/s",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "临时空间写入偏高，部分查询可能溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 work_mem 配置",
							RawSQL: "SHOW work_mem;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-013"},
		Tags:     []string{"temp", "spill", "window_function", "sort"},
		Versions: "9.6+",
	}
}

func rulePG049PreparedStatementLeak() *Rule {
	return &Rule{
		ID:       "PG-049",
		Name:     "Prepared Statement 泄漏",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "prepared"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查连接情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label:    "连接使用率偏高 — 可能有 prepared statement 泄漏",
					Match:    MatchGT(50),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Prepared statement 未及时 DEALLOCATE 可能导致内存泄漏"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 prepared statement 数量",
							RawSQL: "SELECT count(*) FROM pg_prepared_statements",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "清理过多的 prepared statements",
							RawSQL: "DEALLOCATE ALL;",
							Risk:   "需要应用层重新 prepare", Rollback: "无"},
					},
				},
				{
					Label:    "连接正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "连接使用率正常"}},
				},
			},
		},
		Tags:     []string{"prepared_statement", "leak", "memory"},
		Versions: "9.6+",
	}
}

func rulePG050QueryPlanInstability() *Rule {
	return &Rule{
		ID:       "PG-050",
		Name:     "执行计划不稳定",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "plan"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "long_queries", Op: OpGT, Value: 2},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在多个慢查询 — 可能有执行计划跳变",
					Match:    MatchGT(2),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "慢查询突然增多可能是执行计划跳变导致，通常由统计信息过期引起"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查表统计信息是否过期",
							RawSQL: "SELECT schemaname, relname, last_analyze, last_autoanalyze, n_live_tup, n_mod_since_analyze FROM pg_stat_user_tables WHERE n_mod_since_analyze > n_live_tup * 0.1 ORDER BY n_mod_since_analyze DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对统计信息过期的表执行 ANALYZE",
							RawSQL: "ANALYZE {schema}.{table};",
							Risk:   "短暂增加 IO", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "慢查询不多",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "执行计划稳定性良好"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"plan", "regression", "analyze", "statistics"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// WAL/Checkpoint Deep Rules (PG-051 ~ PG-055)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG051WALSegmentAccumulation() *Rule {
	return &Rule{
		ID:       "PG-051",
		Name:     "WAL 段文件堆积",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 52428800}, // 50MB/s
			},
		},
		Tree: &TreeNode{
			Step:  "检查 WAL 生成速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 速率 > 200MB/s — 段文件可能堆积",
					Match:    MatchGT(209715200),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "WAL 生成速率极高，pg_wal 目录可能快速增长"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 pg_wal 目录大小",
							RawSQL: "SELECT count(*) AS wal_files, pg_size_pretty(sum(size)) AS total_size FROM pg_ls_waldir()",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看归档状态",
							RawSQL: "SELECT archived_count, last_archived_wal, last_archived_time, failed_count, last_failed_wal FROM pg_stat_archiver",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "WAL 速率 50-200MB/s",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 生成速率偏高，建议关注 pg_wal 目录大小"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 WAL 文件数",
							RawSQL: "SELECT count(*) AS wal_file_count FROM pg_ls_waldir()",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-016"},
		CausesOf: []string{},
		Tags:     []string{"wal", "segment", "disk"},
		Versions: "10+",
	}
}

func rulePG052ArchiveCommandFailure() *Rule {
	return &Rule{
		ID:       "PG-052",
		Name:     "WAL 归档命令失败",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalCategory, Key: "wal"},
			{Type: SignalKeyword, Key: "archive"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查 WAL 归档状态",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 生成中 — 检查归档是否正常",
					Match:    MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 归档命令失败会导致 pg_wal 目录膨胀，最终磁盘满"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看归档失败记录",
							RawSQL: "SELECT failed_count, last_failed_wal, last_failed_time FROM pg_stat_archiver WHERE failed_count > 0",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看 archive_command 配置",
							RawSQL: "SHOW archive_command;",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无 WAL 生成",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无 WAL 活动"}},
				},
			},
		},
		Tags:     []string{"wal", "archive", "failure"},
		Versions: "9.6+",
	}
}

func rulePG053BasebackupImpact() *Rule {
	return &Rule{
		ID:       "PG-053",
		Name:     "pg_basebackup 影响性能",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 104857600}, // 100MB/s
			},
		},
		Tree: &TreeNode{
			Step:  "检查是否有 basebackup 运行",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 速率高 — 可能有 basebackup",
					Match:    MatchGT(104857600),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 生成速率高，如果有 pg_basebackup 运行，会造成额外 IO 和 WAL 保留"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否有 basebackup",
							RawSQL: "SELECT pid, usename, application_name, client_addr, backend_start, state FROM pg_stat_activity WHERE backend_type = 'walsender'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看复制槽 WAL 保留",
							RawSQL: "SELECT slot_name, slot_type, active, pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained_wal FROM pg_replication_slots",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "WAL 速率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "WAL 速率正常"}},
				},
			},
		},
		Tags:     []string{"wal", "basebackup", "replication"},
		Versions: "10+",
	}
}

func rulePG054SynchronousStandbyLag() *Rule {
	return &Rule{
		ID:       "PG-054",
		Name:     "同步备库延迟影响主库",
		Category: "replication",
		Signals: []Signal{
			{Type: SignalMetric, Key: "replication_lag_sec"},
			{Type: SignalCategory, Key: "replication"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "replication_lag_sec", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查复制延迟",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag_sec") },
			Branches: []Branch{
				{
					Label:    "replication_lag > 10s — 同步复制可能拖慢主库",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "同步复制延迟 > 10 秒，所有写操作都在等待备库确认"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看复制状态",
							RawSQL: "SELECT pid, usename, application_name, client_addr, state, sync_state, pg_wal_lsn_diff(sent_lsn, replay_lsn) AS replay_lag_bytes FROM pg_stat_replication",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "临时切换到异步复制",
							RawSQL: "ALTER SYSTEM SET synchronous_standby_names = ''; SELECT pg_reload_conf();",
							Risk:   "数据可能在故障时丢失", Rollback: "ALTER SYSTEM SET synchronous_standby_names = '{original}'; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "replication_lag 5-10s",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "复制延迟偏高，如果是同步模式会影响写性能"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看同步模式",
							RawSQL: "SHOW synchronous_standby_names;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-019"},
		Tags:     []string{"replication", "synchronous", "lag"},
		Versions: "9.6+",
	}
}

func rulePG055WALLevelConfig() *Rule {
	return &Rule{
		ID:       "PG-055",
		Name:     "WAL Level 配置检查",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalCategory, Key: "wal"},
			{Type: SignalKeyword, Key: "wal_level"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查 WAL 速率判断 wal_level 是否过高",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 生成中",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "wal_level=logical 比 replica 产生更多 WAL，不需要逻辑复制时建议降级"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 wal_level 配置",
							RawSQL: "SHOW wal_level;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看是否使用逻辑复制",
							RawSQL: "SELECT * FROM pg_replication_slots WHERE slot_type = 'logical'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无 WAL 活动",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无 WAL 活动"}},
				},
			},
		},
		Tags:     []string{"wal", "config", "wal_level"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Connection Deep Rules (PG-056 ~ PG-060)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG056PgbouncerSaturation() *Rule {
	return &Rule{
		ID:       "PG-056",
		Name:     "连接池饱和",
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
					Label:    "connections_pct > 90% — 连接池可能饱和",
					Match:    MatchGT(90),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "连接使用率超过 90%，如果使用 pgbouncer 可能已饱和"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看连接来源分布",
							RawSQL: "SELECT client_addr, usename, datname, count(*) AS conn_count FROM pg_stat_activity GROUP BY client_addr, usename, datname ORDER BY conn_count DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 max_connections 或优化连接池",
							RawSQL: "ALTER SYSTEM SET max_connections = 500; -- 需重启",
							Risk:   "内存消耗增加", Rollback: "ALTER SYSTEM RESET max_connections;"},
					},
				},
				{
					Label:    "connections_pct 80-90%",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "连接使用率偏高，建议使用连接池或增加限制"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看连接数和限制",
							RawSQL: "SELECT count(*) AS current_conn, (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_conn FROM pg_stat_activity",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-020"},
		Tags:     []string{"connection", "pool", "pgbouncer"},
		Versions: "9.6+",
	}
}

func rulePG057ConnectionAge() *Rule {
	return &Rule{
		ID:       "PG-057",
		Name:     "连接存活时间过长",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "connection age"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查连接使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label:    "有连接 — 检查连接年龄",
					Match:    MatchGT(30),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "长时间存活的连接可能导致内存泄漏和连接池效率低下"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看最老的连接",
							RawSQL: "SELECT pid, usename, datname, backend_start, state, now() - backend_start AS connection_age FROM pg_stat_activity WHERE backend_type = 'client backend' ORDER BY backend_start LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "建议配置连接池最大存活时间",
							RawSQL: "-- pgbouncer: server_lifetime = 3600",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无连接",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无活跃连接"}},
				},
			},
		},
		Tags:     []string{"connection", "age", "leak"},
		Versions: "9.6+",
	}
}

func rulePG058PreparedTransactionLeak() *Rule {
	return &Rule{
		ID:       "PG-058",
		Name:     "Prepared Transaction 泄漏",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "prepared transaction"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查 XID 年龄是否偏高(可能因 prepared transaction)",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 20% — 可能有未提交的 prepared transaction",
					Match:    MatchGT(20),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "未提交的 PREPARED TRANSACTION 会阻止 VACUUM 推进 XID"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 prepared transactions",
							RawSQL: "SELECT gid, prepared, owner, database FROM pg_prepared_xacts ORDER BY prepared",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "提交或回滚泄漏的 prepared transaction",
							RawSQL: "ROLLBACK PREPARED '{gid}';",
							Risk:   "业务事务将被回滚", Rollback: "无"},
					},
				},
				{
					Label:    "XID 年龄正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "XID 年龄正常，无 prepared transaction 泄漏迹象"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-003"},
		Tags:     []string{"prepared_transaction", "leak", "xid"},
		Versions: "9.6+",
	}
}

func rulePG059TwoPhaseCommitOrphan() *Rule {
	return &Rule{
		ID:       "PG-059",
		Name:     "两阶段提交孤儿事务",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalCategory, Key: "connection"},
			{Type: SignalKeyword, Key: "two phase"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查 XID 年龄",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "XID 年龄偏高 — 检查孤儿 2PC 事务",
					Match:    MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "两阶段提交(2PC)可能留下孤儿事务，持续阻塞 XID 推进"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看长期未提交的 2PC 事务",
							RawSQL: "SELECT gid, prepared, owner, database, now() - prepared AS age FROM pg_prepared_xacts WHERE prepared < now() - interval '1 hour' ORDER BY prepared",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无孤儿事务迹象",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无孤儿 2PC 事务迹象"}},
				},
			},
		},
		Tags:     []string{"two_phase", "orphan", "transaction"},
		Versions: "9.6+",
	}
}

func rulePG060BackendProcessLeak() *Rule {
	return &Rule{
		ID:       "PG-060",
		Name:     "Backend 进程泄漏",
		Category: "connection",
		Signals: []Signal{
			{Type: SignalMetric, Key: "connections_pct"},
			{Type: SignalCategory, Key: "connection"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step:  "检查连接使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label:    "connections_pct > 70% — 检查是否有进程泄漏",
					Match:    MatchGT(70),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "连接使用率偏高，可能有 backend 进程泄漏(idle 连接未释放)"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "按状态统计连接",
							RawSQL: "SELECT state, count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' GROUP BY state ORDER BY count(*) DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "终止长时间 idle 的连接",
							RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND state_change < now() - interval '30 minutes' AND backend_type = 'client backend'",
							Risk:   "会断开客户端连接", Rollback: "无"},
						{Type: ActionPrevent, Desc: "配置 idle_in_transaction_session_timeout",
							RawSQL: "ALTER SYSTEM SET idle_in_transaction_session_timeout = '5min'; SELECT pg_reload_conf();",
							Risk:   "长事务会被终止", Rollback: "ALTER SYSTEM RESET idle_in_transaction_session_timeout; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "连接正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "连接使用率正常"}},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-020"},
		Tags:     []string{"connection", "leak", "backend"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Configuration Rules (PG-061 ~ PG-070)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG061SharedBuffersSizing() *Rule {
	return &Rule{
		ID:       "PG-061",
		Name:     "shared_buffers 大小配置",
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
		},
		Tree: &TreeNode{
			Step:  "检查缓存命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "cache_hit_pct < 90% — shared_buffers 可能不足",
					Match:    MatchLT(90),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "缓存命中率低于 90%，shared_buffers 可能配置过小"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 shared_buffers 大小",
							RawSQL: "SELECT name, setting, unit, pg_size_pretty(setting::bigint * CASE unit WHEN '8kB' THEN 8192 WHEN 'kB' THEN 1024 ELSE 1 END) AS human_size FROM pg_settings WHERE name = 'shared_buffers'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 shared_buffers (建议物理内存的 25%)",
							RawSQL: "ALTER SYSTEM SET shared_buffers = '4GB'; -- 需重启",
							Risk:   "需要重启", Rollback: "ALTER SYSTEM RESET shared_buffers;"},
					},
				},
				{
					Label:    "cache_hit_pct 90-95%",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "缓存命中率略低，可考虑增加 shared_buffers"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前配置",
							RawSQL: "SHOW shared_buffers;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-014"},
		Tags:     []string{"config", "shared_buffers", "memory"},
		Versions: "9.6+",
	}
}

func rulePG062WorkMemTuning() *Rule {
	return &Rule{
		ID:       "PG-062",
		Name:     "work_mem 调优",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "memory"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 5242880}, // 5MB/s
			},
		},
		Tree: &TreeNode{
			Step:  "检查临时空间使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "temp_bytes_rate > 20MB/s — work_mem 可能不足",
					Match:    MatchGT(20971520),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "临时空间写入速率高，work_mem 可能配置过小导致排序和哈希溢出到磁盘"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 work_mem 配置",
							RawSQL: "SHOW work_mem;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 work_mem",
							RawSQL: "ALTER SYSTEM SET work_mem = '128MB'; SELECT pg_reload_conf();",
							Risk:   "每个排序/哈希操作占用更多内存", Rollback: "ALTER SYSTEM RESET work_mem; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "temp_bytes_rate 5-20MB/s",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "临时空间使用偏高，考虑适当增加 work_mem"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看临时文件统计",
							RawSQL: "SELECT datname, temp_files, pg_size_pretty(temp_bytes) FROM pg_stat_database WHERE temp_bytes > 0",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"PG-013"},
		Tags:     []string{"config", "work_mem", "temp"},
		Versions: "9.6+",
	}
}

func rulePG063EffectiveCacheSize() *Rule {
	return &Rule{
		ID:       "PG-063",
		Name:     "effective_cache_size 配置",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalCategory, Key: "memory"},
			{Type: SignalKeyword, Key: "effective_cache_size"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查缓存命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "缓存命中率可用",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "effective_cache_size 影响优化器是否选择索引扫描，建议设为物理内存的 50-75%"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 effective_cache_size",
							RawSQL: "SHOW effective_cache_size;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "调整 effective_cache_size",
							RawSQL: "ALTER SYSTEM SET effective_cache_size = '12GB'; SELECT pg_reload_conf();",
							Risk:   "仅影响优化器估算", Rollback: "ALTER SYSTEM RESET effective_cache_size; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "无缓存数据",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无缓存数据可供判断"}},
				},
			},
		},
		Tags:     []string{"config", "effective_cache_size"},
		Versions: "9.6+",
	}
}

func rulePG064MaintenanceWorkMem() *Rule {
	return &Rule{
		ID:       "PG-064",
		Name:     "maintenance_work_mem 调优",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "memory"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 5% — maintenance_work_mem 可能不足",
					Match:    MatchGT(5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "maintenance_work_mem 影响 VACUUM 和 CREATE INDEX 速度"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 maintenance_work_mem",
							RawSQL: "SHOW maintenance_work_mem;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 maintenance_work_mem",
							RawSQL: "ALTER SYSTEM SET maintenance_work_mem = '1GB'; SELECT pg_reload_conf();",
							Risk:   "VACUUM 和 CREATE INDEX 消耗更多内存", Rollback: "ALTER SYSTEM RESET maintenance_work_mem; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "配置合理",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "maintenance_work_mem 配置合理"}},
				},
			},
		},
		Tags:     []string{"config", "maintenance_work_mem", "vacuum"},
		Versions: "9.6+",
	}
}

func rulePG065RandomPageCost() *Rule {
	return &Rule{
		ID:       "PG-065",
		Name:     "random_page_cost 配置",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalCategory, Key: "io_storage"},
			{Type: SignalKeyword, Key: "random_page_cost"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查缓存命中率判断是否 SSD",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "可用缓存数据",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "SSD 环境下 random_page_cost 应设为 1.1-1.5，默认 4.0 会导致优化器偏向顺序扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 random_page_cost",
							RawSQL: "SHOW random_page_cost;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "SSD 环境调整 random_page_cost",
							RawSQL: "ALTER SYSTEM SET random_page_cost = 1.1; SELECT pg_reload_conf();",
							Risk:   "优化器偏向索引扫描", Rollback: "ALTER SYSTEM RESET random_page_cost; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "无数据",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无可用数据"}},
				},
			},
		},
		Tags:     []string{"config", "random_page_cost", "ssd"},
		Versions: "9.6+",
	}
}

func rulePG066CheckpointCompletionTarget() *Rule {
	return &Rule{
		ID:       "PG-066",
		Name:     "checkpoint_completion_target 配置",
		Category: "checkpoint",
		Signals: []Signal{
			{Type: SignalMetric, Key: "checkpoints_req"},
			{Type: SignalCategory, Key: "checkpoint"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 checkpoint 频率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoints_req") },
			Branches: []Branch{
				{
					Label:    "频繁 checkpoint — 检查 completion_target",
					Match:    MatchGT(5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "checkpoint_completion_target 控制 checkpoint 写入速率，建议设为 0.9"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 checkpoint 相关配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name IN ('checkpoint_completion_target', 'checkpoint_timeout', 'max_wal_size')",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "设置 checkpoint_completion_target = 0.9",
							RawSQL: "ALTER SYSTEM SET checkpoint_completion_target = 0.9; SELECT pg_reload_conf();",
							Risk:   "无", Rollback: "ALTER SYSTEM RESET checkpoint_completion_target; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "checkpoint 频率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "checkpoint 频率在正常范围"}},
				},
			},
		},
		Tags:     []string{"config", "checkpoint"},
		Versions: "9.6+",
	}
}

func rulePG067WALBuffers() *Rule {
	return &Rule{
		ID:       "PG-067",
		Name:     "wal_buffers 配置",
		Category: "wal",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "wal"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 10485760}, // 10MB/s
			},
		},
		Tree: &TreeNode{
			Step:  "检查 WAL 生成速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 速率高 — 检查 wal_buffers",
					Match:    MatchGT(10485760),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "WAL 生成速率高时 wal_buffers 过小会导致频繁刷写"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 wal_buffers",
							RawSQL: "SHOW wal_buffers;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 wal_buffers (建议 shared_buffers 的 1/32，最少 64MB)",
							RawSQL: "ALTER SYSTEM SET wal_buffers = '64MB'; -- 需重启",
							Risk:   "需要重启", Rollback: "ALTER SYSTEM RESET wal_buffers;"},
					},
				},
				{
					Label:    "WAL 速率正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "WAL 速率正常，wal_buffers 无需调整"}},
				},
			},
		},
		Tags:     []string{"config", "wal_buffers"},
		Versions: "9.6+",
	}
}

func rulePG068BgwriterLruMaxpages() *Rule {
	return &Rule{
		ID:       "PG-068",
		Name:     "bgwriter_lru_maxpages 配置",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalCategory, Key: "io_storage"},
			{Type: SignalKeyword, Key: "bgwriter"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查缓存命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "缓存数据可用",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "bgwriter_lru_maxpages 控制后台写进程每轮刷写的最大页面数"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 bgwriter 统计",
							RawSQL: "SELECT buffers_checkpoint, buffers_clean, buffers_backend, maxwritten_clean FROM pg_stat_bgwriter",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看 bgwriter 配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE 'bgwriter%'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无数据",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无可用数据"}},
				},
			},
		},
		Tags:     []string{"config", "bgwriter"},
		Versions: "9.6+",
	}
}

func rulePG069LogMinDurationStatement() *Rule {
	return &Rule{
		ID:       "PG-069",
		Name:     "log_min_duration_statement 配置",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "monitoring"},
			{Type: SignalKeyword, Key: "slow log"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查是否有慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — 检查慢日志配置",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "log_min_duration_statement 用于记录超时 SQL，建议设为 1000ms-5000ms"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前配置",
							RawSQL: "SHOW log_min_duration_statement;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "配置慢查询日志阈值",
							RawSQL: "ALTER SYSTEM SET log_min_duration_statement = '1s'; SELECT pg_reload_conf();",
							Risk:   "日志量增加", Rollback: "ALTER SYSTEM RESET log_min_duration_statement; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无慢查询"}},
				},
			},
		},
		Tags:     []string{"config", "logging", "slow_query"},
		Versions: "9.6+",
	}
}

func rulePG070AutovacuumNaptime() *Rule {
	return &Rule{
		ID:       "PG-070",
		Name:     "autovacuum_naptime 配置",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 5%",
					Match:    MatchGT(5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "autovacuum_naptime 控制 autovacuum 检查频率，默认 1 分钟，高写入环境可缩短"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 autovacuum_naptime",
							RawSQL: "SHOW autovacuum_naptime;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "缩短 autovacuum_naptime",
							RawSQL: "ALTER SYSTEM SET autovacuum_naptime = '15s'; SELECT pg_reload_conf();",
							Risk:   "autovacuum 检查更频繁", Rollback: "ALTER SYSTEM RESET autovacuum_naptime; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "配置合理",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "autovacuum_naptime 配置合理"}},
				},
			},
		},
		Tags:     []string{"config", "autovacuum", "naptime"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// IO/Storage Rules (PG-071 ~ PG-075)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG071TablespaceFull() *Rule {
	return &Rule{
		ID:       "PG-071",
		Name:     "表空间接近满",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalCategory, Key: "io_storage"},
			{Type: SignalKeyword, Key: "tablespace"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查存储状况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "可能有膨胀导致空间不足",
					Match:    MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "表膨胀和 WAL 堆积可能导致表空间接近满"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看表空间大小",
							RawSQL: "SELECT spcname, pg_size_pretty(pg_tablespace_size(oid)) AS size FROM pg_tablespace ORDER BY pg_tablespace_size(oid) DESC",
							Risk:   "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看最大的表",
							RawSQL: "SELECT n.nspname, c.relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid WHERE c.relkind = 'r' ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无数据",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无可判断的数据"}},
				},
			},
		},
		Tags:     []string{"storage", "tablespace", "disk"},
		Versions: "9.6+",
	}
}

func rulePG072TempTablespace() *Rule {
	return &Rule{
		ID:       "PG-072",
		Name:     "临时表空间配置",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 10485760},
			},
		},
		Tree: &TreeNode{
			Step:  "检查临时空间使用",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "临时空间使用高 — 检查 temp_tablespaces",
					Match:    MatchGT(10485760),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "临时空间写入量大，建议使用独立的 temp_tablespaces 避免影响数据盘 IO"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 temp_tablespaces",
							RawSQL: "SHOW temp_tablespaces;",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "临时空间正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "临时空间使用正常"}},
				},
			},
		},
		Tags:     []string{"storage", "temp_tablespace"},
		Versions: "9.6+",
	}
}

func rulePG073PgStatIOAnalysis() *Rule {
	return &Rule{
		ID:       "PG-073",
		Name:     "pg_stat_io 分析",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalCategory, Key: "io_storage"},
			{Type: SignalKeyword, Key: "io"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查缓存命中率判断 IO 状况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "cache_hit_pct < 99% — 存在物理 IO",
					Match:    MatchLT(99),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "PG 16+ 可使用 pg_stat_io 分析 IO 模式"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 IO 统计 (PG 16+)",
							RawSQL: "SELECT backend_type, object, context, reads, writes, extends, hits, evictions FROM pg_stat_io WHERE reads + writes > 0 ORDER BY reads + writes DESC LIMIT 10",
							Risk:   "无 (PG 16+ 才有此视图)", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看缓存和磁盘读写比",
							RawSQL: "SELECT sum(blks_hit) AS cache_hits, sum(blks_read) AS disk_reads, CASE WHEN sum(blks_hit) + sum(blks_read) > 0 THEN ROUND(100.0 * sum(blks_hit) / (sum(blks_hit) + sum(blks_read)), 2) END AS hit_ratio FROM pg_stat_database",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "缓存命中率高，IO 压力小",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "缓存命中率高，物理 IO 很少"}},
				},
			},
		},
		Tags:     []string{"io", "pg_stat_io", "analysis"},
		Versions: "16+",
	}
}

func rulePG074SeqScanLargeTable() *Rule {
	return &Rule{
		ID:       "PG-074",
		Name:     "大表顺序扫描",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "seq_scan_heavy_tables"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(0),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "大表顺序扫描产生大量 IO，可能是性能瓶颈"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看大表的扫描模式",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read, idx_scan, idx_tup_fetch, pg_size_pretty(pg_total_relation_size(relid)) AS table_size FROM pg_stat_user_tables WHERE pg_total_relation_size(relid) > 1073741824 ORDER BY seq_scan DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无明显大表顺序扫描问题"}},
				},
			},
		},
		CausedBy: []string{"PG-041"},
		CausesOf: []string{"PG-011"},
		Tags:     []string{"io", "seq_scan", "large_table"},
		Versions: "9.6+",
	}
}

func rulePG075WALDirectoryGrowth() *Rule {
	return &Rule{
		ID:       "PG-075",
		Name:     "WAL 目录增长",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "wal_bytes_rate"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 52428800},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 WAL 速率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{
					Label:    "WAL 速率 > 50MB/s — pg_wal 目录可能增长过快",
					Match:    MatchGT(52428800),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "WAL 目录增长过快可能导致磁盘满"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 WAL 目录",
							RawSQL: "SELECT count(*) AS files, pg_size_pretty(sum(size)) AS total FROM pg_ls_waldir()",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 max_wal_size 并检查复制槽",
							RawSQL: "SHOW max_wal_size; SELECT slot_name, active, pg_size_pretty(pg_wal_lsn_diff(pg_current_wal_lsn(), restart_lsn)) AS retained FROM pg_replication_slots",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "WAL 增长正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "WAL 目录增长正常"}},
				},
			},
		},
		CausedBy: []string{"PG-051"},
		CausesOf: []string{},
		Tags:     []string{"wal", "disk", "growth"},
		Versions: "10+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Monitoring Rules (PG-076 ~ PG-080)
// ═══════════════════════════════════════════════════════════════════════════════

func rulePG076PgStatStatementsMissing() *Rule {
	return &Rule{
		ID:       "PG-076",
		Name:     "pg_stat_statements 未安装",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "monitoring"},
			{Type: SignalKeyword, Key: "pg_stat_statements"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查是否有慢查询可供诊断",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — 需要 pg_stat_statements",
					Match:    MatchGT(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "pg_stat_statements 是诊断 SQL 性能的核心扩展，必须安装"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否已安装",
							RawSQL: "SELECT * FROM pg_available_extensions WHERE name = 'pg_stat_statements'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "安装 pg_stat_statements",
							RawSQL: "CREATE EXTENSION IF NOT EXISTS pg_stat_statements; -- 需在 shared_preload_libraries 中配置",
							Risk:   "需重启加载 shared_preload_libraries", Rollback: "DROP EXTENSION pg_stat_statements;"},
					},
				},
				{
					Label:    "当前无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "建议安装 pg_stat_statements 以备不时之需"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "安装 pg_stat_statements",
							RawSQL: "CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
							Risk:   "无", Rollback: "DROP EXTENSION pg_stat_statements;"},
					},
				},
			},
		},
		Tags:     []string{"monitoring", "extension", "pg_stat_statements"},
		Versions: "9.6+",
	}
}

func rulePG077AutoExplainNotConfigured() *Rule {
	return &Rule{
		ID:       "PG-077",
		Name:     "auto_explain 未配置",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "monitoring"},
			{Type: SignalKeyword, Key: "auto_explain"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询 — auto_explain 可自动记录执行计划",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "auto_explain 可自动为慢查询记录执行计划到日志，便于事后分析"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 auto_explain 状态",
							RawSQL: "SELECT * FROM pg_available_extensions WHERE name = 'auto_explain'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "配置 auto_explain",
							RawSQL: "ALTER SYSTEM SET auto_explain.log_min_duration = '1s'; ALTER SYSTEM SET auto_explain.log_analyze = on; SELECT pg_reload_conf();",
							Risk:   "日志量增加", Rollback: "ALTER SYSTEM RESET auto_explain.log_min_duration; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "无慢查询",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无慢查询，auto_explain 优先级较低"}},
				},
			},
		},
		Tags:     []string{"monitoring", "auto_explain", "execution_plan"},
		Versions: "9.6+",
	}
}

func rulePG078LogCheckpointsOff() *Rule {
	return &Rule{
		ID:       "PG-078",
		Name:     "log_checkpoints 未开启",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "checkpoint"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 checkpoint 活动",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoints_req") },
			Branches: []Branch{
				{
					Label:    "有 checkpoint 活动",
					Match:    MatchGT(3),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "log_checkpoints 可记录每次 checkpoint 的耗时和 buffer 写入量，便于调优"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 log_checkpoints 配置",
							RawSQL: "SHOW log_checkpoints;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "开启 log_checkpoints",
							RawSQL: "ALTER SYSTEM SET log_checkpoints = on; SELECT pg_reload_conf();",
							Risk:   "无", Rollback: "ALTER SYSTEM SET log_checkpoints = off; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "无 checkpoint 活动",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无 checkpoint 活动"}},
				},
			},
		},
		Tags:     []string{"monitoring", "log_checkpoints"},
		Versions: "9.6+",
	}
}

func rulePG079TrackActivitiesOff() *Rule {
	return &Rule{
		ID:       "PG-079",
		Name:     "track_activities 检查",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "monitoring"},
			{Type: SignalKeyword, Key: "track_activities"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查活跃会话",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("active_sessions") },
			Branches: []Branch{
				{
					Label:    "有活跃会话",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "track_activities 和 track_activity_query_size 控制 pg_stat_activity 的信息完整度"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 track 相关配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE 'track_%'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 track_activity_query_size",
							RawSQL: "ALTER SYSTEM SET track_activity_query_size = 4096; -- 需重启",
							Risk:   "内存轻微增加", Rollback: "ALTER SYSTEM RESET track_activity_query_size;"},
					},
				},
				{
					Label:    "无活跃会话",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无活跃会话"}},
				},
			},
		},
		Tags:     []string{"monitoring", "track_activities"},
		Versions: "9.6+",
	}
}

func rulePG080PgStatKcacheMissing() *Rule {
	return &Rule{
		ID:       "PG-080",
		Name:     "pg_stat_kcache 未安装",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalCategory, Key: "monitoring"},
			{Type: SignalKeyword, Key: "kcache"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查系统状况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "有缓存数据",
					Match:    MatchGT(0),
					Severity: SeverityLow,
					Findings: []Finding{
						{Desc: "pg_stat_kcache 可记录每条 SQL 的 OS 层 CPU 和 IO 消耗，搭配 pg_stat_statements 使用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 pg_stat_kcache 是否可用",
							RawSQL: "SELECT * FROM pg_available_extensions WHERE name = 'pg_stat_kcache'",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "安装 pg_stat_kcache",
							RawSQL: "CREATE EXTENSION IF NOT EXISTS pg_stat_kcache; -- 需在 shared_preload_libraries 中配置",
							Risk:   "需重启", Rollback: "DROP EXTENSION pg_stat_kcache;"},
					},
				},
				{
					Label:    "无数据",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无可用数据"}},
				},
			},
		},
		Tags:     []string{"monitoring", "extension", "pg_stat_kcache"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Wait Event Rules (PG-081)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-081: LWLock 争用严重 — lwlock_wait_sessions > 3 或 WaitProfile 中 LWLock 占比 > 20%
func rulePG081LWLockContention() *Rule {
	return &Rule{
		ID:       "PG-081",
		Name:     "LWLock 争用严重",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: "lwlock_wait_sessions"},
			{Type: SignalWaitEvent, Key: "LWLock"},
			{Type: SignalCategory, Key: "io_storage"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "lwlock_wait_sessions", Op: OpGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 LWLock 等待数量和类型",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lwlock_wait_sessions") },
			Branches: []Branch{
				{
					Label: "lwlock_wait_sessions > 15 — 严重争用",
					Match: MatchGT(15),
					Then: &TreeNode{
						Step: "检查具体 LWLock 类型",
						Check: func(ctx *EvalContext) interface{} {
							for _, w := range ctx.WaitProfile {
								if w.WaitEventType == "LWLock" {
									return w.WaitEvent
								}
							}
							return "unknown"
						},
						Branches: []Branch{
							{
								Label:    "WALInsert 争用 — WAL 写入瓶颈",
								Match:    func(v interface{}) bool { s, _ := v.(string); return s == "WALInsert" || s == "WALBufMapping" },
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量会话等待 LWLock:WALInsert，WAL 写入成为瓶颈"},
									{Desc: "常见原因：高频小事务提交、synchronous_commit=on、WAL 磁盘 IO 慢"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看 WAL 相关等待会话",
										RawSQL: "SELECT pid, usename, wait_event, LEFT(query, 80), now()-query_start AS runtime FROM pg_stat_activity WHERE wait_event_type='LWLock' AND wait_event LIKE 'WAL%' ORDER BY query_start LIMIT 10",
										Risk:   "无", Rollback: "无"},
									{Type: ActionFix, Desc: "考虑增加 wal_buffers 或使用异步提交",
										RawSQL: "ALTER SYSTEM SET wal_buffers = '64MB'; SELECT pg_reload_conf(); -- 或 SET synchronous_commit = off (仅适用于可容忍少量数据丢失的场景)",
										Risk:   "异步提交可能丢失最近事务", Rollback: "ALTER SYSTEM RESET wal_buffers; SELECT pg_reload_conf();"},
								},
							},
							{
								Label:    "BufferMapping/BufferContent 争用 — 热点页面",
								Match:    func(v interface{}) bool { s, _ := v.(string); return s == "BufferMapping" || s == "BufferContent" || s == "buffer_mapping" || s == "buffer_content" },
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量会话等待 LWLock:BufferMapping/BufferContent，存在热点页面争用"},
									{Desc: "常见原因：多个会话并发更新同一页面、shared_buffers 哈希桶冲突"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看 Buffer 相关等待会话",
										RawSQL: "SELECT pid, usename, wait_event, LEFT(query, 80), now()-query_start AS runtime FROM pg_stat_activity WHERE wait_event_type='LWLock' AND wait_event LIKE 'Buffer%' ORDER BY query_start LIMIT 10",
										Risk:   "无", Rollback: "无"},
									{Type: ActionFix, Desc: "分散热点数据（分区表/反转索引）或增大 shared_buffers",
										RawSQL: "SHOW shared_buffers; -- 建议至少为物理内存的 25%",
										Risk:   "增大 shared_buffers 需重启", Rollback: "无"},
								},
							},
							{
								Label:    "其他 LWLock 争用",
								Match:    MatchDefault(),
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大量会话等待 LWLock，内部轻量锁争用严重"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看所有 LWLock 等待",
										RawSQL: "SELECT wait_event, count(*) FROM pg_stat_activity WHERE wait_event_type='LWLock' AND state='active' GROUP BY wait_event ORDER BY count(*) DESC",
										Risk:   "无", Rollback: "无"},
								},
							},
						},
					},
				},
				{
					Label:    "lwlock_wait_sessions 3-15 — 需关注",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在 LWLock 等待，可能有内部资源争用"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 LWLock 等待分布",
							RawSQL: "SELECT wait_event, count(*) FROM pg_stat_activity WHERE wait_event_type='LWLock' AND state='active' GROUP BY wait_event ORDER BY count(*) DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"PG-023"},
		CausesOf: []string{"PG-011", "PG-025"},
		Tags:     []string{"io_storage", "lwlock", "contention", "wait_event"},
		Versions: "9.6+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Parameter Audit Rules (PG-082 ~ PG-086)
// ═══════════════════════════════════════════════════════════════════════════════

// PG-082: 慢查询日志未开 — log_min_duration_statement = -1
func rulePG082SlowQueryLogDisabled() *Rule {
	return &Rule{
		ID:       "PG-082",
		Name:     "慢查询日志未开",
		Category: "config",
		Signals: []Signal{
			{Type: SignalMetric, Key: "param_log_min_duration_ms"},
			{Type: SignalCategory, Key: "config"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "param_log_min_duration_ms", Op: OpLT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查慢查询日志配置",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("param_log_min_duration_ms") },
			Branches: []Branch{
				{
					Label:    "log_min_duration_statement = -1 — 完全关闭",
					Match:    MatchLT(0),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "log_min_duration_statement = -1，慢查询不会被记录到日志"},
						{Desc: "无法回溯历史慢查询，排查性能问题时缺少关键信息"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "开启慢查询日志（建议 1000ms）",
							RawSQL: "ALTER SYSTEM SET log_min_duration_statement = '1000'; SELECT pg_reload_conf();",
							Risk:   "高频环境可能增加日志量", Rollback: "ALTER SYSTEM SET log_min_duration_statement = '-1'; SELECT pg_reload_conf();"},
						{Type: ActionInvestigate, Desc: "查看当前日志配置",
							RawSQL: "SELECT name, setting, unit FROM pg_settings WHERE name IN ('log_min_duration_statement','log_statement','log_min_messages') ORDER BY name",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"config", "logging", "slow_query"},
		Versions: "9.6+",
	}
}

// PG-083: 并行查询禁用 — max_parallel_workers_per_gather = 0
func rulePG083ParallelQueryDisabled() *Rule {
	return &Rule{
		ID:       "PG-083",
		Name:     "并行查询禁用",
		Category: "config",
		Signals: []Signal{
			{Type: SignalMetric, Key: "param_max_parallel_workers"},
			{Type: SignalCategory, Key: "config"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "param_max_parallel_workers", Op: OpLT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查并行查询配置",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("param_max_parallel_workers") },
			Branches: []Branch{
				{
					Label:    "max_parallel_workers_per_gather = 0 — 并行完全禁用",
					Match:    MatchLT(1),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "max_parallel_workers_per_gather = 0，大表查询无法利用并行扫描"},
						{Desc: "对于 OLAP/报表类查询，启用并行可显著提升性能"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "启用并行查询（建议 2-4）",
							RawSQL: "ALTER SYSTEM SET max_parallel_workers_per_gather = 2; SELECT pg_reload_conf();",
							Risk:   "增加 CPU 使用", Rollback: "ALTER SYSTEM SET max_parallel_workers_per_gather = 0; SELECT pg_reload_conf();"},
					},
				},
			},
		},
		Tags:     []string{"config", "parallel", "performance"},
		Versions: "9.6+",
	}
}

// PG-084: statistics_target 过低 — default_statistics_target < 100
func rulePG084StatisticsTargetLow() *Rule {
	return &Rule{
		ID:       "PG-084",
		Name:     "统计信息精度不足",
		Category: "config",
		Signals: []Signal{
			{Type: SignalMetric, Key: "param_statistics_target"},
			{Type: SignalCategory, Key: "config"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "param_statistics_target", Op: OpLT, Value: 100},
			},
		},
		Tree: &TreeNode{
			Step:  "检查统计信息采样精度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("param_statistics_target") },
			Branches: []Branch{
				{
					Label:    "statistics_target < 100 — 采样精度偏低",
					Match:    MatchLT(100),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "default_statistics_target 低于默认值 100，优化器可能选择次优执行计划"},
						{Desc: "对于数据分布不均的列，建议设置更高的 statistics_target"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "恢复或提高统计采样精度",
							RawSQL: "ALTER SYSTEM SET default_statistics_target = 100; SELECT pg_reload_conf(); ANALYZE;",
							Risk:   "ANALYZE 时间增加", Rollback: "ALTER SYSTEM RESET default_statistics_target; SELECT pg_reload_conf();"},
					},
				},
			},
		},
		Tags:     []string{"config", "statistics", "planner"},
		Versions: "9.6+",
	}
}

// PG-085: random_page_cost 过高 — SSD 环境下 > 1.5 影响索引选择
func rulePG085RandomPageCostHigh() *Rule {
	return &Rule{
		ID:       "PG-085",
		Name:     "random_page_cost 偏高",
		Category: "config",
		Signals: []Signal{
			{Type: SignalMetric, Key: "param_random_page_cost"},
			{Type: SignalCategory, Key: "config"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "param_random_page_cost", Op: OpGT, Value: 1.5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 random_page_cost 配置",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("param_random_page_cost") },
			Branches: []Branch{
				{
					Label:    "random_page_cost > 3 — HDD 默认值，SSD 环境偏高",
					Match:    MatchGT(3),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "random_page_cost = 4（默认值），如果使用 SSD 则该值过高"},
						{Desc: "优化器会偏向全表扫描而非索引扫描，影响查询性能"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "SSD 环境建议设为 1.1",
							RawSQL: "ALTER SYSTEM SET random_page_cost = 1.1; SELECT pg_reload_conf();",
							Risk:   "可能改变现有查询的执行计划", Rollback: "ALTER SYSTEM RESET random_page_cost; SELECT pg_reload_conf();"},
					},
				},
				{
					Label:    "random_page_cost 1.5-3 — 偏高",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "random_page_cost 偏高，SSD 环境建议 1.1-1.5"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认存储类型",
							RawSQL: "SHOW random_page_cost; -- SSD 建议 1.1, HDD 建议 4.0",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"config", "random_page_cost", "ssd", "planner"},
		Versions: "9.6+",
	}
}

// PG-086: pg_stat_statements 未安装 — 缺少关键性能分析扩展
func rulePG086PgStatStatementsNotInstalled() *Rule {
	return &Rule{
		ID:       "PG-086",
		Name:     "pg_stat_statements 未安装",
		Category: "monitoring",
		Signals: []Signal{
			{Type: SignalMetric, Key: "param_pgss_installed"},
			{Type: SignalCategory, Key: "monitoring"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "param_pgss_installed", Op: OpLT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 pg_stat_statements 是否安装",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("param_pgss_installed") },
			Branches: []Branch{
				{
					Label:    "pg_stat_statements 未安装",
					Match:    MatchLT(1),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "pg_stat_statements 未安装，无法追踪 SQL 执行统计信息"},
						{Desc: "这是 PostgreSQL 最重要的性能分析扩展，强烈建议安装"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "安装 pg_stat_statements",
							RawSQL: "ALTER SYSTEM SET shared_preload_libraries = 'pg_stat_statements'; -- 需要重启 PostgreSQL\n-- 重启后执行: CREATE EXTENSION IF NOT EXISTS pg_stat_statements;",
							Risk:   "需要重启 PostgreSQL", Rollback: "DROP EXTENSION pg_stat_statements;"},
						{Type: ActionInvestigate, Desc: "检查已安装的扩展",
							RawSQL: "SELECT name, installed_version, default_version FROM pg_available_extensions WHERE installed_version IS NOT NULL ORDER BY name",
							Risk: "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"monitoring", "extension", "pg_stat_statements"},
		Versions: "9.6+",
	}
}
