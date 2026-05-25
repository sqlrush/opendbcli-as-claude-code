/*-------------------------------------------------------------------------
 *
 * rules_extended.go
 *	  openGauss rule engine — extended classification rules (newer than the core set, kept separate to ease review).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/ruleengine/rules_extended.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// extendedRules returns 55 additional OpenGauss diagnostic rules (OG-026 ~ OG-080)
// covering deep vacuum, lock, query, WAL/checkpoint, connection, configuration,
// IO/storage, and monitoring scenarios.
// Adapted from PostgreSQL rules with OpenGauss-specific SQL and system views.
func extendedRules() []*Rule {
	return []*Rule{
		// ── Vacuum Deep (8): OG-026 ~ OG-033 ──
		ruleOG026AutovacuumMaxWorkers(),
		ruleOG027VacuumCostDelay(),
		ruleOG028FreezeAgePerTable(),
		ruleOG029VacuumProgress(),
		ruleOG030WraparoundEmergency(),
		ruleOG031TOASTVacuum(),
		ruleOG032AntiWraparoundVacuum(),
		ruleOG033VacuumDeadTupleThreshold(),

		// ── Lock Deep (7): OG-034 ~ OG-040 ──
		ruleOG034AdvisoryLockLeak(),
		ruleOG035RelationLockWait(),
		ruleOG036TupleLockContention(),
		ruleOG037DDLLockBlocking(),
		ruleOG038LockTimeoutConfig(),
		ruleOG039LockQueueDepth(),
		ruleOG040PgLocksAnalysis(),

		// ── Query Deep (10): OG-041 ~ OG-050 ──
		ruleOG041MissingIndexes(),
		ruleOG042UnusedIndexes(),
		ruleOG043IndexBloat(),
		ruleOG044PartitioningSuggestion(),
		ruleOG045QueryPerformanceTuning(),
		ruleOG046ParallelQueryConfig(),
		ruleOG047CTEPerformance(),
		ruleOG048SortSpill(),
		ruleOG049PreparedStatementLeak(),
		ruleOG050QueryPlanInstability(),

		// ── WAL/Checkpoint Deep (5): OG-051 ~ OG-055 ──
		ruleOG051WALSegmentAccumulation(),
		ruleOG052ArchiveCommandFailure(),
		ruleOG053BasebackupImpact(),
		ruleOG054SynchronousStandbyLag(),
		ruleOG055WALLevelConfig(),

		// ── Connection Deep (5): OG-056 ~ OG-060 ──
		ruleOG056ConnectionPoolSaturation(),
		ruleOG057ConnectionAge(),
		ruleOG058PreparedTransactionLeak(),
		ruleOG059TwoPhaseCommitOrphan(),
		ruleOG060BackendThreadLeak(),

		// ── Configuration (10): OG-061 ~ OG-070 ──
		ruleOG061SharedBuffersSizing(),
		ruleOG062WorkMemTuning(),
		ruleOG063EffectiveCacheSize(),
		ruleOG064MaintenanceWorkMem(),
		ruleOG065RandomPageCost(),
		ruleOG066CheckpointCompletionTarget(),
		ruleOG067WALBuffers(),
		ruleOG068BgwriterConfig(),
		ruleOG069LogMinDurationStatement(),
		ruleOG070AutovacuumNaptime(),

		// ── IO/Storage (5): OG-071 ~ OG-075 ──
		ruleOG071TablespaceFull(),
		ruleOG072TempTablespace(),
		ruleOG073IOAnalysis(),
		ruleOG074SeqScanLargeTable(),
		ruleOG075WALDirectoryGrowth(),

		// ── Monitoring (5): OG-076 ~ OG-080 ──
		ruleOG076StatementTrackMissing(),
		ruleOG077AutoExplainNotConfigured(),
		ruleOG078LogCheckpointsOff(),
		ruleOG079TrackActivitiesOff(),
		ruleOG080PerformanceViewCheck(),
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Vacuum Deep Rules (OG-026 ~ OG-033)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG026AutovacuumMaxWorkers() *Rule {
	return &Rule{
		ID:       "OG-026",
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
							RawSQL: "gs_guc reload -D $GAUSSDATA -c \"autovacuum_max_workers = 6\"",
							Risk:   "CPU/IO 消耗增加", Rollback: "gs_guc reload -D $GAUSSDATA -c \"autovacuum_max_workers = 3\""},
					},
				},
				{
					Label:    "dead_tuple_ratio 15-30%",
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
		CausedBy: []string{"OG-001"},
		CausesOf: []string{"OG-002"},
		Tags:     []string{"vacuum", "autovacuum", "worker"},
		Versions: "1.0+",
	}
}

func ruleOG027VacuumCostDelay() *Rule {
	return &Rule{
		ID:       "OG-027",
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
		},
		Tree: &TreeNode{
			Step:  "检查死元组比例判断 vacuum cost delay 是否过大",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 20%",
					Match:    MatchGT(20),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "死元组堆积严重，autovacuum_vacuum_cost_delay 可能设置过高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前 cost delay 配置",
							RawSQL: "SHOW autovacuum_vacuum_cost_delay;",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "降低 autovacuum_vacuum_cost_delay",
							RawSQL: "gs_guc reload -D $GAUSSDATA -c \"autovacuum_vacuum_cost_delay = 2\"",
							Risk:   "IO 压力增加", Rollback: "gs_guc reload -D $GAUSSDATA -c \"autovacuum_vacuum_cost_delay = 20\""},
					},
				},
				{
					Label:    "dead_tuple_ratio 10-20%",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "死元组比例偏高，考虑降低 vacuum cost delay"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 vacuum cost 参数",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE '%vacuum_cost%'",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"vacuum", "cost_delay", "tuning"},
		Versions: "1.0+",
	}
}

func ruleOG028FreezeAgePerTable() *Rule {
	return &Rule{
		ID:       "OG-028",
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
		},
		Tree: &TreeNode{
			Step:  "检查 XID 年龄占比",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 60%",
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
							RawSQL: "gs_guc reload -D $GAUSSDATA -c \"vacuum_freeze_min_age = 50000000\"",
							Risk:   "无", Rollback: "gs_guc reload -D $GAUSSDATA -c \"vacuum_freeze_min_age = 50000000\""},
					},
				},
			},
		},
		CausedBy: []string{"OG-001"},
		CausesOf: []string{"OG-002"},
		Tags:     []string{"vacuum", "freeze", "xid"},
		Versions: "1.0+",
	}
}

func ruleOG029VacuumProgress() *Rule {
	return &Rule{
		ID:       "OG-029",
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
		},
		Tree: &TreeNode{
			Step:  "检查 VACUUM 进度",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 5%",
					Match:    MatchGT(5),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "VACUUM 运行中但死元组仍偏高，可能进度缓慢"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 VACUUM 进度",
							RawSQL: "SELECT pid, datname, relid::regclass AS table_name, phase, heap_blks_total, heap_blks_scanned, heap_blks_vacuumed FROM pg_stat_progress_vacuum",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "VACUUM 运行正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "VACUUM 进度正常"}},
				},
			},
		},
		Tags:     []string{"vacuum", "progress"},
		Versions: "2.0+",
	}
}

func ruleOG030WraparoundEmergency() *Rule {
	return &Rule{
		ID:       "OG-030",
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
			Step:  "检查 XID 紧急回卷",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 95% — 数据库即将拒绝写入",
					Match:    MatchGT(95),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "XID 年龄已达 95% 以上，数据库即将拒绝所有写操作"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "终止长事务并执行 VACUUM FREEZE",
							RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE xact_start IS NOT NULL AND pid <> pg_backend_pid() AND state = 'idle in transaction' AND query_start < now() - interval '10 minutes'",
							Risk:   "终止长事务可能影响业务", Rollback: "无需回滚"},
						{Type: ActionUrgent, Desc: "对最老数据库执行 VACUUM FREEZE",
							RawSQL: "VACUUM FREEZE;",
							Risk:   "运行时间长", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "xid_age_pct 90-95%",
					Match:    MatchDefault(),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "XID 年龄已达 90% 以上，处于紧急状态"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查 XID 年龄分布",
							RawSQL: "SELECT datname, age(datfrozenxid) AS xid_age FROM pg_database ORDER BY xid_age DESC",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		CausedBy: []string{"OG-002", "OG-028"},
		Tags:     []string{"vacuum", "xid", "wraparound", "emergency"},
		Versions: "1.0+",
	}
}

func ruleOG031TOASTVacuum() *Rule {
	return &Rule{
		ID:       "OG-031",
		Name:     "TOAST 表 Vacuum 滞后",
		Category: "vacuum",
		Signals: []Signal{
			{Type: SignalMetric, Key: "dead_tuple_ratio"},
			{Type: SignalCategory, Key: "vacuum"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查 TOAST 表膨胀",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 10%",
					Match:    MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "TOAST 表可能存在膨胀"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 TOAST 表大小",
							RawSQL: "SELECT c.relname AS main_table, t.relname AS toast_table, pg_size_pretty(pg_relation_size(t.oid)) AS toast_size FROM pg_class c JOIN pg_class t ON c.reltoastrelid = t.oid WHERE pg_relation_size(t.oid) > 100*1024*1024 ORDER BY pg_relation_size(t.oid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对含大 TOAST 的表执行 VACUUM FULL",
							RawSQL: "VACUUM FULL {schema}.{table};",
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
		Tags:     []string{"vacuum", "toast", "bloat"},
		Versions: "1.0+",
	}
}

func ruleOG032AntiWraparoundVacuum() *Rule {
	return &Rule{
		ID:       "OG-032",
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
			Step:  "检查 anti-wraparound vacuum",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{
					Label:    "xid_age_pct > 70%",
					Match:    MatchGT(70),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "XID 年龄偏高，anti-wraparound vacuum 可能已被触发，会导致 IO 冲高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看正在运行的 autovacuum",
							RawSQL: "SELECT pid, datname, query FROM pg_stat_activity WHERE backend_type = 'autovacuum worker'",
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
		CausedBy: []string{"OG-002"},
		Tags:     []string{"vacuum", "anti-wraparound", "xid"},
		Versions: "1.0+",
	}
}

func ruleOG033VacuumDeadTupleThreshold() *Rule {
	return &Rule{
		ID:       "OG-033",
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
		},
		Tree: &TreeNode{
			Step:  "检查 autovacuum 触发阈值",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 8%",
					Match:    MatchGT(8),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "默认 autovacuum_vacuum_scale_factor=0.2 对大表来说阈值过高"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看大表 autovacuum 配置",
							RawSQL: "SELECT n.nspname, c.relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size, s.n_dead_tup FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid JOIN pg_stat_user_tables s ON c.oid = s.relid WHERE c.relkind = 'r' AND pg_total_relation_size(c.oid) > 1073741824 ORDER BY s.n_dead_tup DESC LIMIT 10",
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
		Tags:     []string{"vacuum", "threshold", "tuning"},
		Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Lock Deep Rules (OG-034 ~ OG-040)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG034AdvisoryLockLeak() *Rule {
	return &Rule{
		ID:       "OG-034",
		Name:     "Advisory Lock 泄漏",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalCategory, Key: "lock"},
			{Type: SignalKeyword, Key: "advisory"},
		},
		Trigger: Trigger{Mode: TriggerManual},
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
						{Type: ActionInvestigate, Desc: "查看 advisory lock",
							RawSQL: "SELECT pid, classid, objid, mode, granted FROM pg_locks WHERE locktype = 'advisory' ORDER BY pid",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "释放 advisory lock",
							RawSQL: "SELECT pg_advisory_unlock_all();",
							Risk:   "可能影响业务", Rollback: "无"},
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
		Tags:     []string{"lock", "advisory"},
		Versions: "1.0+",
	}
}

func ruleOG035RelationLockWait() *Rule {
	return &Rule{
		ID:       "OG-035",
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
			Step:  "检查 relation lock 等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "lock_waits > 10",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "大量 relation lock 等待，表级锁竞争严重"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看锁等待详情",
							RawSQL: "SELECT l.pid, l.locktype, l.relation::regclass, l.mode, l.granted, a.state, LEFT(a.query, 80) AS query FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE NOT l.granted ORDER BY l.pid",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "lock_waits 5-10",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在 relation lock 等待"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查等待中的会话",
							RawSQL: "SELECT pid, waiting, state, LEFT(query, 60) FROM pg_stat_activity WHERE waiting = true",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"lock", "relation", "wait"},
		Versions: "1.0+",
	}
}

func ruleOG036TupleLockContention() *Rule {
	return &Rule{
		ID:       "OG-036",
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
			Step: "检查 tuple lock 等待",
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
					Label:    "tuple lock 等待 > 10%",
					Match:    MatchGT(10),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "tuple lock 争用占比高，多个事务竞争同一行"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看等待中的会话",
							RawSQL: "SELECT pid, waiting, state, LEFT(query, 80) FROM pg_stat_activity WHERE waiting = true",
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
		Tags:     []string{"lock", "tuple", "contention"},
		Versions: "1.0+",
	}
}

func ruleOG037DDLLockBlocking() *Rule {
	return &Rule{
		ID:       "OG-037",
		Name:     "DDL 锁阻塞查询",
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
			Step:  "检查 DDL 锁阻塞",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "lock_waits > 3",
					Match:    MatchGT(3),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "可能有 ALTER TABLE / CREATE INDEX 等 DDL 操作持有 AccessExclusiveLock"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 AccessExclusive 锁",
							RawSQL: "SELECT l.pid, a.usename, l.relation::regclass, l.mode, a.state, LEFT(a.query, 80) FROM pg_locks l JOIN pg_stat_activity a ON l.pid = a.pid WHERE l.mode = 'AccessExclusiveLock' AND l.granted",
							Risk:   "无", Rollback: "无"},
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
		Tags:     []string{"lock", "ddl", "blocking"},
		Versions: "1.0+",
	}
}

func ruleOG038LockTimeoutConfig() *Rule {
	return &Rule{
		ID:       "OG-038",
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
			Step:  "检查锁超时配置",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("lock_waits") },
			Branches: []Branch{
				{
					Label:    "存在锁等待",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在锁等待，lockwait_timeout 可能未配置"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看锁超时配置",
							RawSQL: "SELECT name, setting, unit FROM pg_settings WHERE name IN ('lockwait_timeout', 'deadlock_timeout', 'statement_timeout')",
							Risk:   "无", Rollback: "无"},
						{Type: ActionPrevent, Desc: "配置 lockwait_timeout",
							RawSQL: "gs_guc reload -D $GAUSSDATA -c \"lockwait_timeout = 30000\"",
							Risk:   "超时操作会失败", Rollback: "gs_guc reload -D $GAUSSDATA -c \"lockwait_timeout = 0\""},
					},
				},
				{
					Label:    "锁等待正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "锁等待正常"}},
				},
			},
		},
		Tags:     []string{"lock", "timeout", "config"},
		Versions: "1.0+",
	}
}

func ruleOG039LockQueueDepth() *Rule {
	return &Rule{
		ID:       "OG-039",
		Name:     "Lock 队列深度过深",
		Category: "lock",
		Signals: []Signal{
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
					Label:    "blocker_count > 5",
					Match:    MatchGT(5),
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "阻塞者超过 5 个，锁链式阻塞正在扩散"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查看阻塞链",
							RawSQL: "SELECT blocked.pid AS blocked_pid, LEFT(blocked.query, 60) AS blocked_query, blocker.pid AS blocker_pid, LEFT(blocker.query, 60) AS blocker_query FROM pg_locks bl JOIN pg_stat_activity blocked ON bl.pid = blocked.pid JOIN pg_locks bll ON bll.locktype = bl.locktype AND bll.relation = bl.relation AND bll.pid != bl.pid JOIN pg_stat_activity blocker ON bll.pid = blocker.pid WHERE NOT bl.granted AND bll.granted",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "blocker_count 2-5",
					Match:    MatchDefault(),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在多个阻塞者"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看未授予的锁",
							RawSQL: "SELECT pid, locktype, relation::regclass, mode, granted FROM pg_locks WHERE NOT granted",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"lock", "queue", "blocking_chain"},
		Versions: "1.0+",
	}
}

func ruleOG040PgLocksAnalysis() *Rule {
	return &Rule{
		ID:       "OG-040",
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
						{Desc: "pg_locks 综合分析"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "锁类型分布",
							RawSQL: "SELECT locktype, mode, COUNT(*) AS cnt, SUM(CASE WHEN granted THEN 0 ELSE 1 END) AS waiting FROM pg_locks GROUP BY locktype, mode ORDER BY waiting DESC, cnt DESC",
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
		Tags:     []string{"lock", "analysis"},
		Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Query Deep Rules (OG-041 ~ OG-050)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG041MissingIndexes() *Rule {
	return &Rule{
		ID:       "OG-041",
		Name:     "缺失索引导致全表扫描",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
			{Type: SignalKeyword, Key: "索引"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查全表扫描情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(1),
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "存在慢查询，可能因缺失索引导致全表扫描"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看顺序扫描最多的表",
							RawSQL: "SELECT schemaname, relname, seq_scan, seq_tup_read, idx_scan FROM pg_stat_user_tables WHERE seq_scan > 1000 ORDER BY seq_tup_read DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无明显缺失索引",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无明显缺失索引迹象"}},
				},
			},
		},
		Tags:     []string{"index", "missing", "seq_scan"},
		Versions: "1.0+",
	}
}

func ruleOG042UnusedIndexes() *Rule {
	return &Rule{
		ID:       "OG-042",
		Name:     "未使用索引浪费资源",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查缓存情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{
					Label:    "缓存命中率可用",
					Match:    MatchLT(99),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "未使用的索引浪费磁盘空间和缓存，增加 VACUUM 开销"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出未使用的索引",
							RawSQL: "SELECT s.schemaname, s.relname AS table_name, s.indexrelname AS index_name, pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size, s.idx_scan FROM pg_stat_user_indexes s WHERE s.idx_scan = 0 AND pg_relation_size(s.indexrelid) > 1048576 ORDER BY pg_relation_size(s.indexrelid) DESC LIMIT 20",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "缓存充足",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "缓存命中率高"}},
				},
			},
		},
		Tags:     []string{"index", "unused"},
		Versions: "1.0+",
	}
}

func ruleOG043IndexBloat() *Rule {
	return &Rule{
		ID:       "OG-043",
		Name:     "索引膨胀",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalCategory, Key: "sql_perf"},
		},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查死元组比例推断索引膨胀",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{
					Label:    "dead_tuple_ratio > 10%",
					Match:    MatchGT(10),
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "表膨胀可能伴随索引膨胀"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看大索引",
							RawSQL: "SELECT nspname, relname, indexrelname, pg_size_pretty(pg_relation_size(indexrelid)) AS idx_size FROM pg_stat_user_indexes JOIN pg_class ON pg_stat_user_indexes.indexrelid = pg_class.oid JOIN pg_namespace ON pg_class.relnamespace = pg_namespace.oid WHERE pg_relation_size(indexrelid) > 104857600 ORDER BY pg_relation_size(indexrelid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "重建膨胀索引",
							RawSQL: "REINDEX INDEX {index_name};",
							Risk:   "锁表", Rollback: "无需回滚"},
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
		Tags:     []string{"index", "bloat"},
		Versions: "1.0+",
	}
}

func ruleOG044PartitioningSuggestion() *Rule {
	return &Rule{
		ID:       "OG-044",
		Name:     "大表分区建议",
		Category: "sql_perf",
		Signals:  []Signal{{Type: SignalCategory, Key: "sql_perf"}},
		Trigger:  Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "大表全表扫描耗时长，考虑分区"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "列出最大的非分区表",
							RawSQL: "SELECT n.nspname, c.relname, pg_size_pretty(pg_total_relation_size(c.oid)) AS size, s.n_live_tup FROM pg_class c JOIN pg_namespace n ON c.relnamespace = n.oid JOIN pg_stat_user_tables s ON c.oid = s.relid WHERE c.relkind = 'r' AND pg_total_relation_size(c.oid) > 10737418240 ORDER BY pg_total_relation_size(c.oid) DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "暂无分区需求",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无分区需求"}},
				},
			},
		},
		Tags:     []string{"partition", "large_table"},
		Versions: "1.0+",
	}
}

func ruleOG045QueryPerformanceTuning() *Rule {
	return &Rule{
		ID:       "OG-045",
		Name:     "查询性能调优建议",
		Category: "sql_perf",
		Signals:  []Signal{{Type: SignalCategory, Key: "sql_perf"}},
		Trigger:  Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "OpenGauss 提供 AI 查询调优功能，可检查是否启用"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 AI 调优开关",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE '%ai%' OR name LIKE '%tune%'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "慢查询不多",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "慢查询不多"}},
				},
			},
		},
		Tags:     []string{"query", "tuning", "ai"},
		Versions: "2.0+",
	}
}

func ruleOG046ParallelQueryConfig() *Rule {
	return &Rule{
		ID:       "OG-046",
		Name:     "并行查询配置",
		Category: "sql_perf",
		Signals:  []Signal{{Type: SignalCategory, Key: "sql_perf"}},
		Trigger:  Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(2),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "大表扫描慢查询可能因未启用并行导致性能不佳"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看并行查询配置",
							RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE '%parallel%' OR name = 'query_dop'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "慢查询不多",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "慢查询不多"}},
				},
			},
		},
		Tags:     []string{"parallel", "query"},
		Versions: "1.0+",
	}
}

func ruleOG047CTEPerformance() *Rule {
	return &Rule{
		ID:       "OG-047",
		Name:     "CTE 性能问题",
		Category: "sql_perf",
		Signals:  []Signal{{Type: SignalCategory, Key: "sql_perf"}},
		Trigger:  Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查慢查询",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{
					Label:    "存在慢查询",
					Match:    MatchGT(1),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "CTE 在某些版本是优化屏障，可能导致性能问题"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看包含 WITH 的活跃查询",
							RawSQL: "SELECT pid, state, LEFT(query, 120) FROM pg_stat_activity WHERE query ILIKE '%WITH%' AND state = 'active'",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "无 CTE 问题",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无明显 CTE 性能问题"}},
				},
			},
		},
		Tags:     []string{"cte", "query"},
		Versions: "1.0+",
	}
}

func ruleOG048SortSpill() *Rule {
	return &Rule{
		ID:       "OG-048",
		Name:     "排序溢出到磁盘",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "temp_bytes_rate"},
			{Type: SignalCategory, Key: "sql_perf"},
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
					Label:    "temp_bytes_rate > 50MB/s",
					Match:    MatchGT(52428800),
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "排序操作溢出到磁盘，work_mem 不足"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看临时文件",
							RawSQL: "SELECT datname, temp_files, pg_size_pretty(temp_bytes) FROM pg_stat_database WHERE temp_bytes > 0",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 work_mem",
							RawSQL: "gs_guc reload -D $GAUSSDATA -c \"work_mem = '256MB'\"",
							Risk:   "内存增加", Rollback: "gs_guc reload -D $GAUSSDATA -c \"work_mem = '64MB'\""},
					},
				},
				{
					Label:    "temp_bytes_rate 10-50MB/s",
					Match:    MatchDefault(),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "临时空间写入偏高"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 work_mem",
							RawSQL: "SHOW work_mem;",
							Risk:   "无", Rollback: "无"},
					},
				},
			},
		},
		Tags:     []string{"temp", "spill", "sort"},
		Versions: "1.0+",
	}
}

func ruleOG049PreparedStatementLeak() *Rule {
	return &Rule{
		ID:       "OG-049",
		Name:     "Prepared Statement 泄漏",
		Category: "sql_perf",
		Signals:  []Signal{{Type: SignalCategory, Key: "sql_perf"}},
		Trigger:  Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step:  "检查连接情况",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{
					Label:    "连接偏高",
					Match:    MatchGT(50),
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "Prepared statement 未及时释放可能导致内存泄漏"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 prepared statement",
							RawSQL: "SELECT count(*) FROM pg_prepared_statements",
							Risk:   "无", Rollback: "无"},
					},
				},
				{
					Label:    "连接正常",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "连接正常"}},
				},
			},
		},
		Tags:     []string{"prepared_statement", "leak"},
		Versions: "1.0+",
	}
}

func ruleOG050QueryPlanInstability() *Rule {
	return &Rule{
		ID:       "OG-050",
		Name:     "执行计划不稳定",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: "long_queries"},
			{Type: SignalCategory, Key: "sql_perf"},
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
					Label:    "存在多个慢查询",
					Match:    MatchGT(2),
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "慢查询突增可能由统计信息过期导致执行计划跳变"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查统计信息",
							RawSQL: "SELECT schemaname, relname, last_analyze, last_autoanalyze, n_mod_since_analyze FROM pg_stat_user_tables WHERE n_mod_since_analyze > 10000 ORDER BY n_mod_since_analyze DESC LIMIT 10",
							Risk:   "无", Rollback: "无"},
						{Type: ActionFix, Desc: "对过期表执行 ANALYZE",
							RawSQL: "ANALYZE {schema}.{table};",
							Risk:   "短暂 IO 增加", Rollback: "无需回滚"},
					},
				},
				{
					Label:    "慢查询不多",
					Match:    MatchDefault(),
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "执行计划稳定"}},
				},
			},
		},
		Tags:     []string{"plan", "regression", "analyze"},
		Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// WAL/Checkpoint Deep Rules (OG-051 ~ OG-055)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG051WALSegmentAccumulation() *Rule {
	return &Rule{
		ID: "OG-051", Name: "WAL 段文件堆积", Category: "wal",
		Signals: []Signal{{Type: SignalMetric, Key: "wal_bytes_rate"}, {Type: SignalCategory, Key: "wal"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 52428800}}},
		Tree: &TreeNode{
			Step: "检查 WAL 生成速率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL 速率 > 200MB/s", Match: MatchGT(209715200), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "WAL 生成速率极高，pg_xlog 目录可能快速增长"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看归档状态", RawSQL: "SELECT archived_count, last_archived_wal, failed_count FROM pg_stat_archiver", Risk: "无", Rollback: "无"},
					}},
				{Label: "WAL 速率 50-200MB/s", Match: MatchDefault(), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "WAL 生成速率偏高"}}},
			},
		},
		Tags: []string{"wal", "segment"}, Versions: "1.0+",
	}
}

func ruleOG052ArchiveCommandFailure() *Rule {
	return &Rule{
		ID: "OG-052", Name: "WAL 归档命令失败", Category: "wal",
		Signals: []Signal{{Type: SignalCategory, Key: "wal"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查归档状态", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL 生成中", Match: MatchGT(0), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "WAL 归档失败会导致 xlog 目录膨胀"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看归档失败", RawSQL: "SELECT failed_count, last_failed_wal FROM pg_stat_archiver WHERE failed_count > 0", Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看 archive_command", RawSQL: "SHOW archive_command;", Risk: "无", Rollback: "无"},
					}},
				{Label: "无 WAL 活动", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无 WAL 活动"}}},
			},
		},
		Tags: []string{"wal", "archive"}, Versions: "1.0+",
	}
}

func ruleOG053BasebackupImpact() *Rule {
	return &Rule{
		ID: "OG-053", Name: "gs_basebackup 影响性能", Category: "wal",
		Signals: []Signal{{Type: SignalMetric, Key: "wal_bytes_rate"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 104857600}}},
		Tree: &TreeNode{
			Step: "检查备份影响", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL 速率高", Match: MatchGT(104857600), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "WAL 生成速率高，如果有 gs_basebackup 运行会造成额外 IO"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 walsender", RawSQL: "SELECT pid, usename, application_name, client_addr, state FROM pg_stat_activity WHERE backend_type = 'walsender'", Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看复制槽", RawSQL: "SELECT slot_name, slot_type, active FROM pg_replication_slots", Risk: "无", Rollback: "无"},
					}},
				{Label: "WAL 正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "WAL 速率正常"}}},
			},
		},
		Tags: []string{"wal", "backup"}, Versions: "1.0+",
	}
}

func ruleOG054SynchronousStandbyLag() *Rule {
	return &Rule{
		ID: "OG-054", Name: "同步备库延迟影响主库", Category: "replication",
		Signals: []Signal{{Type: SignalMetric, Key: "replication_lag_sec"}, {Type: SignalCategory, Key: "replication"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "replication_lag_sec", Op: OpGT, Value: 5}}},
		Tree: &TreeNode{
			Step: "检查复制延迟", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("replication_lag_sec") },
			Branches: []Branch{
				{Label: "replication_lag > 10s", Match: MatchGT(10), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "同步复制延迟 > 10 秒，写操作被阻塞"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看复制状态", RawSQL: "SELECT pid, usename, application_name, client_addr, state, sync_state FROM pg_stat_replication", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "临时切异步", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"synchronous_standby_names = ''\"", Risk: "数据可能丢失", Rollback: "恢复同步配置"},
					}},
				{Label: "replication_lag 5-10s", Match: MatchDefault(), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "复制延迟偏高"}}},
			},
		},
		Tags: []string{"replication", "synchronous"}, Versions: "1.0+",
	}
}

func ruleOG055WALLevelConfig() *Rule {
	return &Rule{
		ID: "OG-055", Name: "WAL Level 配置检查", Category: "wal",
		Signals: []Signal{{Type: SignalCategory, Key: "wal"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查 WAL Level", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL 生成中", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "wal_level=logical 产生更多 WAL"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 wal_level", RawSQL: "SHOW wal_level;", Risk: "无", Rollback: "无"},
					}},
				{Label: "无活动", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无 WAL 活动"}}},
			},
		},
		Tags: []string{"wal", "config"}, Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Connection Deep Rules (OG-056 ~ OG-060)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG056ConnectionPoolSaturation() *Rule {
	return &Rule{
		ID: "OG-056", Name: "连接池饱和", Category: "connection",
		Signals: []Signal{{Type: SignalMetric, Key: "connections_pct"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 80}}},
		Tree: &TreeNode{
			Step: "检查连接使用率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{Label: "connections_pct > 90%", Match: MatchGT(90), Severity: SeverityCritical,
					Findings: []Finding{{Desc: "连接使用率超过 90%"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看连接来源", RawSQL: "SELECT client_addr, usename, datname, count(*) AS cnt FROM pg_stat_activity GROUP BY client_addr, usename, datname ORDER BY cnt DESC LIMIT 10", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 max_connections", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"max_connections = 500\"", Risk: "需重启", Rollback: "恢复原值"},
					}},
				{Label: "connections_pct 80-90%", Match: MatchDefault(), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "连接使用率偏高"}}},
			},
		},
		Tags: []string{"connection", "pool"}, Versions: "1.0+",
	}
}

func ruleOG057ConnectionAge() *Rule {
	return &Rule{
		ID: "OG-057", Name: "连接存活时间过长", Category: "connection",
		Signals: []Signal{{Type: SignalCategory, Key: "connection"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查连接年龄", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{Label: "有连接", Match: MatchGT(0), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "长时间存活的连接可能导致内存泄漏"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看最老连接", RawSQL: "SELECT pid, usename, datname, backend_start, state, now() - backend_start AS age FROM pg_stat_activity WHERE backend_type = 'client backend' ORDER BY backend_start LIMIT 10", Risk: "无", Rollback: "无"},
					}},
				{Label: "无连接", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无活跃连接"}}},
			},
		},
		Tags: []string{"connection", "age"}, Versions: "1.0+",
	}
}

func ruleOG058PreparedTransactionLeak() *Rule {
	return &Rule{
		ID: "OG-058", Name: "Prepared Transaction 泄漏", Category: "connection",
		Signals: []Signal{{Type: SignalCategory, Key: "connection"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查 XID 年龄", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{Label: "xid_age 偏高", Match: MatchGT(20), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "未提交的 PREPARED TRANSACTION 会阻止 VACUUM 推进 XID"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 prepared transactions", RawSQL: "SELECT gid, prepared, owner, database FROM pg_prepared_xacts ORDER BY prepared", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "回滚泄漏事务", RawSQL: "ROLLBACK PREPARED '{gid}';", Risk: "业务事务回滚", Rollback: "无"},
					}},
				{Label: "XID 正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无泄漏"}}},
			},
		},
		Tags: []string{"prepared_transaction", "leak"}, Versions: "1.0+",
	}
}

func ruleOG059TwoPhaseCommitOrphan() *Rule {
	return &Rule{
		ID: "OG-059", Name: "两阶段提交孤儿事务", Category: "connection",
		Signals: []Signal{{Type: SignalCategory, Key: "connection"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查孤儿 2PC", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("xid_age_pct") },
			Branches: []Branch{
				{Label: "XID 偏高", Match: MatchGT(10), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "2PC 可能留下孤儿事务"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看长期未提交的 2PC", RawSQL: "SELECT gid, prepared, owner, database, now() - prepared AS age FROM pg_prepared_xacts WHERE prepared < now() - interval '1 hour'", Risk: "无", Rollback: "无"},
					}},
				{Label: "无孤儿事务", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无孤儿事务"}}},
			},
		},
		Tags: []string{"two_phase", "orphan"}, Versions: "1.0+",
	}
}

func ruleOG060BackendThreadLeak() *Rule {
	return &Rule{
		ID: "OG-060", Name: "Backend 线程泄漏", Category: "connection",
		Signals: []Signal{{Type: SignalMetric, Key: "connections_pct"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "connections_pct", Op: OpGT, Value: 70}}},
		Tree: &TreeNode{
			Step: "检查连接使用率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("connections_pct") },
			Branches: []Branch{
				{Label: "connections_pct > 70%", Match: MatchGT(70), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "连接使用率偏高，可能有 idle 线程未释放"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "按状态统计连接", RawSQL: "SELECT state, count(*) FROM pg_stat_activity GROUP BY state ORDER BY count(*) DESC", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "终止长时间 idle 连接", RawSQL: "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE state = 'idle' AND state_change < now() - interval '30 minutes'", Risk: "断开客户端", Rollback: "无"},
					}},
				{Label: "连接正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "连接正常"}}},
			},
		},
		Tags: []string{"connection", "leak", "thread"}, Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Configuration Rules (OG-061 ~ OG-070)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG061SharedBuffersSizing() *Rule {
	return &Rule{
		ID: "OG-061", Name: "shared_buffers 配置", Category: "memory",
		Signals: []Signal{{Type: SignalMetric, Key: "cache_hit_pct"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "cache_hit_pct", Op: OpLT, Value: 95}}},
		Tree: &TreeNode{
			Step: "检查缓存命中率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "cache_hit < 90%", Match: MatchLT(90), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "缓存命中率低，shared_buffers 可能不足"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 shared_buffers", RawSQL: "SHOW shared_buffers;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 shared_buffers", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"shared_buffers = '4GB'\"", Risk: "需重启", Rollback: "恢复原值"},
					}},
				{Label: "cache_hit 90-95%", Match: MatchDefault(), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "缓存命中率略低"}}},
			},
		},
		Tags: []string{"config", "shared_buffers"}, Versions: "1.0+",
	}
}

func ruleOG062WorkMemTuning() *Rule {
	return &Rule{
		ID: "OG-062", Name: "work_mem 调优", Category: "memory",
		Signals: []Signal{{Type: SignalMetric, Key: "temp_bytes_rate"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 5242880}}},
		Tree: &TreeNode{
			Step: "检查临时空间", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes_rate") },
			Branches: []Branch{
				{Label: "temp > 20MB/s", Match: MatchGT(20971520), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "work_mem 不足导致排序溢出"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 work_mem", RawSQL: "SHOW work_mem;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 work_mem", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"work_mem = '128MB'\"", Risk: "内存增加", Rollback: "恢复原值"},
					}},
				{Label: "temp 5-20MB/s", Match: MatchDefault(), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "临时空间偏高"}}},
			},
		},
		Tags: []string{"config", "work_mem"}, Versions: "1.0+",
	}
}

func ruleOG063EffectiveCacheSize() *Rule {
	return &Rule{
		ID: "OG-063", Name: "effective_cache_size 配置", Category: "memory",
		Signals: []Signal{{Type: SignalCategory, Key: "memory"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查配置", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "有缓存数据", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "effective_cache_size 影响优化器选择，建议设为物理内存 50-75%"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW effective_cache_size;", Risk: "无", Rollback: "无"},
					}},
				{Label: "无数据", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无数据"}}},
			},
		},
		Tags: []string{"config", "effective_cache_size"}, Versions: "1.0+",
	}
}

func ruleOG064MaintenanceWorkMem() *Rule {
	return &Rule{
		ID: "OG-064", Name: "maintenance_work_mem 调优", Category: "memory",
		Signals: []Signal{{Type: SignalMetric, Key: "dead_tuple_ratio"}},
		Trigger: Trigger{Mode: TriggerManual, Conditions: []Condition{{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5}}},
		Tree: &TreeNode{
			Step: "检查死元组", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{Label: "dead_tuple > 5%", Match: MatchGT(5), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "maintenance_work_mem 影响 VACUUM 速度"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW maintenance_work_mem;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "增加 maintenance_work_mem", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"maintenance_work_mem = '1GB'\"", Risk: "内存增加", Rollback: "恢复原值"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "配置合理"}}},
			},
		},
		Tags: []string{"config", "maintenance_work_mem"}, Versions: "1.0+",
	}
}

func ruleOG065RandomPageCost() *Rule {
	return &Rule{
		ID: "OG-065", Name: "random_page_cost 配置", Category: "io_storage",
		Signals: []Signal{{Type: SignalCategory, Key: "io_storage"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查存储配置", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "有数据", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "SSD 环境下 random_page_cost 应为 1.1-1.5"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW random_page_cost;", Risk: "无", Rollback: "无"},
					}},
				{Label: "无数据", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无数据"}}},
			},
		},
		Tags: []string{"config", "random_page_cost"}, Versions: "1.0+",
	}
}

func ruleOG066CheckpointCompletionTarget() *Rule {
	return &Rule{
		ID: "OG-066", Name: "checkpoint_completion_target 配置", Category: "checkpoint",
		Signals: []Signal{{Type: SignalMetric, Key: "checkpoints_req"}},
		Trigger: Trigger{Mode: TriggerManual, Conditions: []Condition{{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 5}}},
		Tree: &TreeNode{
			Step: "检查 checkpoint", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoints_req") },
			Branches: []Branch{
				{Label: "频繁 checkpoint", Match: MatchGT(5), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "checkpoint_completion_target 建议设为 0.9"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SELECT name, setting FROM pg_settings WHERE name IN ('checkpoint_completion_target', 'checkpoint_timeout')", Risk: "无", Rollback: "无"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "正常"}}},
			},
		},
		Tags: []string{"config", "checkpoint"}, Versions: "1.0+",
	}
}

func ruleOG067WALBuffers() *Rule {
	return &Rule{
		ID: "OG-067", Name: "wal_buffers 配置", Category: "wal",
		Signals: []Signal{{Type: SignalMetric, Key: "wal_bytes_rate"}},
		Trigger: Trigger{Mode: TriggerManual, Conditions: []Condition{{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 10485760}}},
		Tree: &TreeNode{
			Step: "检查 WAL 速率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL 速率高", Match: MatchGT(10485760), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "wal_buffers 过小会导致频繁刷写"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW wal_buffers;", Risk: "无", Rollback: "无"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "正常"}}},
			},
		},
		Tags: []string{"config", "wal_buffers"}, Versions: "1.0+",
	}
}

func ruleOG068BgwriterConfig() *Rule {
	return &Rule{
		ID: "OG-068", Name: "bgwriter 配置", Category: "io_storage",
		Signals: []Signal{{Type: SignalCategory, Key: "io_storage"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查 bgwriter", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "有数据", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "bgwriter 控制后台写进程效率"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 bgwriter 统计", RawSQL: "SELECT buffers_checkpoint, buffers_clean, buffers_backend FROM pg_stat_bgwriter", Risk: "无", Rollback: "无"},
					}},
				{Label: "无数据", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无数据"}}},
			},
		},
		Tags: []string{"config", "bgwriter"}, Versions: "1.0+",
	}
}

func ruleOG069LogMinDurationStatement() *Rule {
	return &Rule{
		ID: "OG-069", Name: "log_min_duration_statement 配置", Category: "monitoring",
		Signals: []Signal{{Type: SignalCategory, Key: "monitoring"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查慢查询日志", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{Label: "存在慢查询", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "log_min_duration_statement 用于记录超时 SQL"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW log_min_duration_statement;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "配置慢查询日志", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"log_min_duration_statement = 1000\"", Risk: "日志量增加", Rollback: "恢复原值"},
					}},
				{Label: "无慢查询", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无慢查询"}}},
			},
		},
		Tags: []string{"config", "logging"}, Versions: "1.0+",
	}
}

func ruleOG070AutovacuumNaptime() *Rule {
	return &Rule{
		ID: "OG-070", Name: "autovacuum_naptime 配置", Category: "vacuum",
		Signals: []Signal{{Type: SignalMetric, Key: "dead_tuple_ratio"}},
		Trigger: Trigger{Mode: TriggerManual, Conditions: []Condition{{Source: "metrics", Field: "dead_tuple_ratio", Op: OpGT, Value: 5}}},
		Tree: &TreeNode{
			Step: "检查 autovacuum 频率", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{Label: "dead_tuple > 5%", Match: MatchGT(5), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "autovacuum_naptime 默认 1 分钟，高写入环境可缩短"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW autovacuum_naptime;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "缩短 naptime", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"autovacuum_naptime = '15s'\"", Risk: "更频繁", Rollback: "恢复原值"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "配置合理"}}},
			},
		},
		Tags: []string{"config", "autovacuum"}, Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// IO/Storage Rules (OG-071 ~ OG-075)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG071TablespaceFull() *Rule {
	return &Rule{
		ID: "OG-071", Name: "表空间接近满", Category: "io_storage",
		Signals: []Signal{{Type: SignalCategory, Key: "io_storage"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查存储", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("dead_tuple_ratio") },
			Branches: []Branch{
				{Label: "有膨胀", Match: MatchGT(0), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "表膨胀和 WAL 堆积可能导致空间不足"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看表空间", RawSQL: "SELECT spcname, pg_size_pretty(pg_tablespace_size(oid)) AS size FROM pg_tablespace ORDER BY pg_tablespace_size(oid) DESC", Risk: "无", Rollback: "无"},
					}},
				{Label: "无数据", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无可判断数据"}}},
			},
		},
		Tags: []string{"storage", "tablespace"}, Versions: "1.0+",
	}
}

func ruleOG072TempTablespace() *Rule {
	return &Rule{
		ID: "OG-072", Name: "临时表空间配置", Category: "io_storage",
		Signals: []Signal{{Type: SignalMetric, Key: "temp_bytes_rate"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "temp_bytes_rate", Op: OpGT, Value: 10485760}}},
		Tree: &TreeNode{
			Step: "检查临时空间", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("temp_bytes_rate") },
			Branches: []Branch{
				{Label: "临时空间高", Match: MatchGT(10485760), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "建议使用独立 temp_tablespaces"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW temp_tablespaces;", Risk: "无", Rollback: "无"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "正常"}}},
			},
		},
		Tags: []string{"storage", "temp"}, Versions: "1.0+",
	}
}

func ruleOG073IOAnalysis() *Rule {
	return &Rule{
		ID: "OG-073", Name: "IO 分析", Category: "io_storage",
		Signals: []Signal{{Type: SignalCategory, Key: "io_storage"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查 IO 状况", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "cache_hit < 99%", Match: MatchLT(99), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在物理 IO"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看缓存命中率", RawSQL: "SELECT sum(blks_hit) AS hits, sum(blks_read) AS reads, CASE WHEN sum(blks_hit) + sum(blks_read) > 0 THEN ROUND(100.0 * sum(blks_hit) / (sum(blks_hit) + sum(blks_read)), 2) END AS ratio FROM pg_stat_database", Risk: "无", Rollback: "无"},
					}},
				{Label: "缓存命中高", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "IO 压力小"}}},
			},
		},
		Tags: []string{"io", "analysis"}, Versions: "1.0+",
	}
}

func ruleOG074SeqScanLargeTable() *Rule {
	return &Rule{
		ID: "OG-074", Name: "大表顺序扫描", Category: "io_storage",
		Signals: []Signal{{Type: SignalCategory, Key: "io_storage"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查大表扫描", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{Label: "有慢查询", Match: MatchGT(0), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "大表顺序扫描产生大量 IO"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看大表扫描模式", RawSQL: "SELECT schemaname, relname, seq_scan, idx_scan, pg_size_pretty(pg_total_relation_size(relid)) FROM pg_stat_user_tables WHERE pg_total_relation_size(relid) > 1073741824 ORDER BY seq_scan DESC LIMIT 10", Risk: "无", Rollback: "无"},
					}},
				{Label: "无慢查询", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无问题"}}},
			},
		},
		Tags: []string{"io", "seq_scan"}, Versions: "1.0+",
	}
}

func ruleOG075WALDirectoryGrowth() *Rule {
	return &Rule{
		ID: "OG-075", Name: "WAL 目录增长", Category: "io_storage",
		Signals: []Signal{{Type: SignalMetric, Key: "wal_bytes_rate"}},
		Trigger: Trigger{Mode: TriggerAuto, Conditions: []Condition{{Source: "metrics", Field: "wal_bytes_rate", Op: OpGT, Value: 52428800}}},
		Tree: &TreeNode{
			Step: "检查 WAL 增长", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("wal_bytes_rate") },
			Branches: []Branch{
				{Label: "WAL > 50MB/s", Match: MatchGT(52428800), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "WAL 目录增长过快"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看复制槽", RawSQL: "SELECT slot_name, active FROM pg_replication_slots", Risk: "无", Rollback: "无"},
					}},
				{Label: "正常", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "正常"}}},
			},
		},
		Tags: []string{"wal", "disk"}, Versions: "1.0+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════════
// Monitoring Rules (OG-076 ~ OG-080)
// ═══════════════════════════════════════════════════════════════════════════════

func ruleOG076StatementTrackMissing() *Rule {
	return &Rule{
		ID: "OG-076", Name: "statement_track 未启用", Category: "monitoring",
		Signals: []Signal{{Type: SignalCategory, Key: "monitoring"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查语句追踪", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{Label: "有慢查询", Match: MatchGT(0), Severity: SeverityHigh,
					Findings: []Finding{{Desc: "OpenGauss 内置 statement_track 功能，建议启用以追踪 SQL 性能"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 statement_track", RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE '%track_stmt%' OR name LIKE '%statement%'", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "启用语句追踪", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"track_stmt_stat_level = 'OFF,L1'\"", Risk: "轻微性能影响", Rollback: "恢复原值"},
					}},
				{Label: "无慢查询", Match: MatchDefault(), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "建议启用 statement_track"}}},
			},
		},
		Tags: []string{"monitoring", "statement_track"}, Versions: "2.0+",
	}
}

func ruleOG077AutoExplainNotConfigured() *Rule {
	return &Rule{
		ID: "OG-077", Name: "auto_explain 未配置", Category: "monitoring",
		Signals: []Signal{{Type: SignalCategory, Key: "monitoring"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查慢查询", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("long_queries") },
			Branches: []Branch{
				{Label: "有慢查询", Match: MatchGT(1), Severity: SeverityMedium,
					Findings: []Finding{{Desc: "auto_explain 可自动记录慢查询执行计划"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查 auto_explain", RawSQL: "SELECT * FROM pg_available_extensions WHERE name = 'auto_explain'", Risk: "无", Rollback: "无"},
					}},
				{Label: "无慢查询", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "优先级低"}}},
			},
		},
		Tags: []string{"monitoring", "auto_explain"}, Versions: "1.0+",
	}
}

func ruleOG078LogCheckpointsOff() *Rule {
	return &Rule{
		ID: "OG-078", Name: "log_checkpoints 未开启", Category: "monitoring",
		Signals: []Signal{{Type: SignalMetric, Key: "checkpoints_req"}},
		Trigger: Trigger{Mode: TriggerManual, Conditions: []Condition{{Source: "metrics", Field: "checkpoints_req", Op: OpGT, Value: 1}}},
		Tree: &TreeNode{
			Step: "检查 checkpoint 日志", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("checkpoints_req") },
			Branches: []Branch{
				{Label: "有 checkpoint", Match: MatchGT(1), Severity: SeverityLow,
					Findings: []Finding{{Desc: "log_checkpoints 记录 checkpoint 耗时"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看配置", RawSQL: "SHOW log_checkpoints;", Risk: "无", Rollback: "无"},
						{Type: ActionFix, Desc: "开启", RawSQL: "gs_guc reload -D $GAUSSDATA -c \"log_checkpoints = on\"", Risk: "无", Rollback: "恢复"},
					}},
				{Label: "无 checkpoint", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无活动"}}},
			},
		},
		Tags: []string{"monitoring", "checkpoint"}, Versions: "1.0+",
	}
}

func ruleOG079TrackActivitiesOff() *Rule {
	return &Rule{
		ID: "OG-079", Name: "track_activities 检查", Category: "monitoring",
		Signals: []Signal{{Type: SignalCategory, Key: "monitoring"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查会话追踪", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("active_sessions") },
			Branches: []Branch{
				{Label: "有活跃会话", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "track_activities 和 track_activity_query_size 控制 pg_stat_activity 信息完整度"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看 track 配置", RawSQL: "SELECT name, setting FROM pg_settings WHERE name LIKE 'track_%'", Risk: "无", Rollback: "无"},
					}},
				{Label: "无会话", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无活跃会话"}}},
			},
		},
		Tags: []string{"monitoring", "track_activities"}, Versions: "1.0+",
	}
}

func ruleOG080PerformanceViewCheck() *Rule {
	return &Rule{
		ID: "OG-080", Name: "性能视图检查", Category: "monitoring",
		Signals: []Signal{{Type: SignalCategory, Key: "monitoring"}},
		Trigger: Trigger{Mode: TriggerManual},
		Tree: &TreeNode{
			Step: "检查 OpenGauss 性能视图", Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue("cache_hit_pct") },
			Branches: []Branch{
				{Label: "有数据", Match: MatchGT(0), Severity: SeverityLow,
					Findings: []Finding{{Desc: "OpenGauss 提供 dbe_perf schema 下的性能视图，建议利用"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看可用性能视图", RawSQL: "SELECT schemaname, viewname FROM pg_views WHERE schemaname = 'dbe_perf' ORDER BY viewname LIMIT 20", Risk: "无", Rollback: "无"},
						{Type: ActionInvestigate, Desc: "查看全局等待事件", RawSQL: "SELECT * FROM dbe_perf.global_wait_events ORDER BY total_wait_time DESC LIMIT 10", Risk: "无 (如果视图不存在会报错)", Rollback: "无"},
					}},
				{Label: "无数据", Match: MatchDefault(), Severity: SeverityLow, Findings: []Finding{{Desc: "无数据"}}},
			},
		},
		Tags: []string{"monitoring", "dbe_perf", "performance_view"}, Versions: "2.0+",
	}
}
