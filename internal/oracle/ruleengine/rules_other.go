/*-------------------------------------------------------------------------
 *
 * rules_other.go
 *	  Oracle rule engine — miscellaneous rules that don't fit the other category buckets.
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_other.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// Metric name constants used across rules. These mirror the sentinel.MetricName
// values so that rules_other.go does not need to import the sentinel package.
const (
	metricPlanChangeCount    = "plan_change_count"
	metricTopSQLDrift        = "top_sql_elapsed_drift"
	metricFullScanRate       = "full_scan_rate"
	metricHardParse          = "hard_parse_rate"
	metricMutexWait          = "mutex_wait_sessions"
	metricBufferCacheHit     = "buffer_cache_hit_pct"
	metricSharedPoolFreePct  = "shared_pool_free_pct"
	metricPGAUsedPct         = "pga_used_pct"
	metricTablespaceUsedPct  = "tablespace_used_pct"
	metricTempUsedPct        = "temp_used_pct"
	metricUndoUsedPct        = "undo_used_pct"
	metricEnqueueDeadlocks   = "enqueue_deadlocks"
	metricLatchFreeRate      = "latch_free_rate"
	metricLogSwitchRate      = "log_switch_rate"
	metricCheckpointNotComplete = "checkpoint_not_complete"
	metricFRAUsedPct         = "fra_used_pct"
	metricAlertLogErrors     = "alert_log_ora_errors"
	metricActive             = "active_sessions"
	metricCommitRate         = "commit_rate"
	metricBlockingChains     = "blocking_chains"
	metricResourceLimitPct   = "resource_limit_pct"
	metricTotalSessions      = "total_sessions"
	metricSessionCreationRate = "session_creation_rate"
	metricLongSQL            = "long_sql"
)

// ─── SQL Performance Rules (5) ──────────────────────────────────────────────

func sqlPerfRules() []*Rule {
	return []*Rule{
		ruleSQLPlanDrift(),
		ruleFullTableScanOLTP(),
		ruleHardParseStorm(),
		ruleStaleStatistics(),
		ruleVersionCountBloat(),
	}
}

// ruleSQLPlanDrift detects SQL plan regression: plan_hash_value changed,
// elapsed time increased >2x. Fix: SQL Plan Management or SQL Profile.
func ruleSQLPlanDrift() *Rule {
	return &Rule{
		ID:       "sql_plan_drift",
		Name:     "SQL执行计划漂移(Plan Regression)",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPlanChangeCount},
			{Type: SignalMetric, Key: metricTopSQLDrift},
			{Type: SignalKeyword, Key: "plan drift"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPlanChangeCount, Op: OpGT, Value: 0},
				{Source: "metrics", Field: metricTopSQLDrift, Op: OpGT, Value: 2.0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Top SQL执行计划变化历史",
			Query: QuerySQLPlanHistory,
			Branches: []Branch{
				{
					Match:    MatchGT(1),
					Label:    "存在多个plan_hash_value,确认计划漂移",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查elapsed_time变化倍数",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Match:    MatchGT(5),
								Label:    "elapsed增长>5x,严重回退",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "SQL执行计划发生严重漂移,elapsed_time增长超过5倍"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "使用SPM基线锁定稳定计划", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))"},
									{Type: ActionFix, Desc: "创建SQL Profile固定优良计划", SkillCommand: "/sql SELECT plan_name FROM DBA_SQL_PLAN_BASELINES WHERE sql_handle = '{handle}'", RawSQL: "EXEC DBMS_SPM.ALTER_SQL_PLAN_BASELINE(sql_handle => '{handle}', plan_name => '{good_plan}', attribute_name => 'FIXED', attribute_value => 'YES')"},
								},
							},
							{
								Match:    MatchGT(2),
								Label:    "elapsed增长2-5x,中等回退",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SQL执行计划漂移,elapsed_time增长2-5倍"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "对比新旧计划差异", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_AWR('{sql_id}', NULL, NULL, 'ALLSTATS'))"},
									{Type: ActionFix, Desc: "加载历史稳定计划到SPM基线", SkillCommand: "/sql @spm_load", RawSQL: "DECLARE l_plans PLS_INTEGER; BEGIN l_plans := DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE(sql_id => '{sql_id}', plan_hash_value => {good_hash}); END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "elapsed增长<2x,轻微偏移",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "执行计划变化但性能影响较小"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控后续变化,建议启用SPM自动捕获", SkillCommand: "/params optimizer_capture_sql_plan_baselines", RawSQL: "ALTER SYSTEM SET optimizer_capture_sql_plan_baselines = TRUE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未检测到计划变化",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SQL_ID未发现多个plan_hash_value"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查是否为统计信息变化导致", SkillCommand: "/sql @stale_stats", RawSQL: "SELECT owner, table_name, last_analyzed, stale_stats FROM DBA_TAB_STATISTICS WHERE stale_stats = 'YES'"},
					},
				},
			},
		},
		CausedBy: []string{"stale_statistics"},
		CausesOf: []string{"buffer_busy_waits", "db_file_scattered_read"},
		Tags:     []string{"sql", "plan", "regression"},
		Versions: "10g+",
	}
}

// ruleFullTableScanOLTP detects excessive full table scans in OLTP workload.
func ruleFullTableScanOLTP() *Rule {
	return &Rule{
		ID:       "full_table_scan_oltp",
		Name:     "OLTP场景全表扫描(Full Table Scan)",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file scattered read"},
			{Type: SignalMetric, Key: metricFullScanRate},
			{Type: SignalKeyword, Key: "full table scan"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "db file scattered read", Op: OpPctGT, Value: 15},
			},
		},
		Tree: &TreeNode{
			Step:  "检查db file scattered read占比与热点对象",
			Query: QueryASHHotObject,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现全扫描热点对象",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有可用索引",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchEquals("no_index"),
								Label:    "缺少适当索引",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "OLTP场景出现大量全表扫描,热点表缺少适当索引"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "根据谓词条件创建B-tree索引", SkillCommand: "/indexadvise {table_name}", RawSQL: "CREATE INDEX idx_{table}_{col} ON {schema}.{table}({columns}) TABLESPACE {ts} ONLINE"},
									{Type: ActionInvestigate, Desc: "确认SQL谓词选择性", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "有索引但未使用",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "表上存在索引但SQL未使用,可能因隐式类型转换或函数"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查SQL谓词是否存在隐式转换", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST OUTLINE'))"},
									{Type: ActionFix, Desc: "考虑添加函数索引或修改SQL", SkillCommand: "/sql @index_usage {table_name}", RawSQL: "SELECT index_name, column_name FROM DBA_IND_COLUMNS WHERE table_name = '{table}'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "scattered read高但无明确热点",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "db file scattered read占比偏高但未找到集中热点对象"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查ASH获取近期全扫描SQL", SkillCommand: "/slowsql", RawSQL: "SELECT sql_id, sql_text FROM V$SQL WHERE disk_reads/NULLIF(executions,0) > 1000 ORDER BY disk_reads DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{"stale_statistics"},
		CausesOf: []string{},
		Tags:     []string{"sql", "index", "full_scan"},
		Versions: "9i+",
	}
}

// ruleHardParseStorm detects excessive hard parsing (>100/s) caused by
// literal SQL. Fix: cursor_sharing or bind variable refactoring.
func ruleHardParseStorm() *Rule {
	return &Rule{
		ID:       "hard_parse_storm",
		Name:     "硬解析风暴(Hard Parse Storm)",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricHardParse},
			{Type: SignalWaitEvent, Key: "latch: shared pool"},
			{Type: SignalWaitEvent, Key: "library cache: mutex X"},
			{Type: SignalKeyword, Key: "hard parse"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				// Use wait_profile so it fires in both burst and live modes.
				{Source: "wait_profile", Field: "latch: shared pool", Op: OpPctGT, Value: 20},
			},
			SkipWhen: []SkipCondition{
				{Desc: "latch:shared pool < 5%", Check: func(ctx *EvalContext) bool {
					return ctx.WaitPct("latch: shared pool") < 5
				}},
			},
		},
		Tree: &TreeNode{
			Step: "检查hard parse rate与cursor统计",
			Check: func(ctx *EvalContext) interface{} {
				result, err := ctx.ExecuteQuery(QueryCursorStats, nil)
				if err == nil && result != nil {
					return result
				}
				if v := ctx.MetricValue(metricHardParse); v > 0 {
					return v
				}
				// Live mode: high latch:shared pool implies hard parse storm.
				if ctx.WaitPct("latch: shared pool") > 30 {
					return float64(200)
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "确认硬解析>100/s — 硬解析风暴",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step: "检查是否大量literal SQL",
						Check: func(ctx *EvalContext) interface{} {
							// Prioritize current-state evidence over cumulative stats.
							if ctx.WaitPct("latch: shared pool") > 30 {
								return float64(80)
							}
							result, err := ctx.ExecuteQuery(QueryParseStats, nil)
							if err == nil && result != nil {
								if pct := ExtractHardParsePct(result); pct >= 0 {
									return pct
								}
							}
							if v := ctx.MetricValue("hard_parse_pct"); v > 0 {
								return v
							}
							return float64(60) // metric triggered >100/s, assume moderate literal SQL
						},
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "literal SQL占比>50%",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量literal SQL导致硬解析风暴,shared pool压力严重"},
									{Desc: "每次硬解析需要获取 shared pool latch 并在 library cache 中创建新 cursor，高并发时造成严重争用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "临时设置cursor_sharing=FORCE缓解", SkillCommand: "/params cursor_sharing", RawSQL: "ALTER SYSTEM SET cursor_sharing = 'FORCE' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "长期方案:修改应用使用绑定变量", SkillCommand: "/slowsql", RawSQL: "SELECT sql_id, force_matching_signature, COUNT(*) cnt FROM V$SQL GROUP BY force_matching_signature, sql_id HAVING COUNT(*) > 10 ORDER BY cnt DESC"},
									{Type: ActionFix, Desc: "增大 session_cached_cursors 减少重复软解析",
										SkillCommand: "/param session_cached_cursors",
										RawSQL:       "ALTER SYSTEM SET session_cached_cursors = 300 SCOPE=SPFILE",
										Risk: "需重启生效", Rollback: "ALTER SYSTEM SET session_cached_cursors = 原值 SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非literal SQL引起",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "硬解析较高,可能因DDL频繁或shared pool不足"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查shared pool free memory", SkillCommand: "/sql @sp_free", RawSQL: "SELECT pool, name, bytes/1024/1024 MB FROM V$SGASTAT WHERE pool = 'shared pool' AND name = 'free memory'"},
									{Type: ActionFix, Desc: "增大shared pool或设置shared_pool_reserved_size", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "硬解析率在正常范围",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "hard parse rate未超过阈值"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期监控硬解析趋势", SkillCommand: "/health", RawSQL: "SELECT value FROM V$SYSSTAT WHERE name = 'parse count (hard)'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"shared_pool_ora4031", "library_cache_mutex", "MI2-005"},
		Tags:     []string{"parse", "cursor", "shared_pool", "session_cached_cursors"},
		Versions: "9i+",
	}
}

// ruleStaleStatistics detects stale optimizer statistics where E-Rows vs
// A-Rows differ by >10x and stats are older than 7 days.
func ruleStaleStatistics() *Rule {
	return &Rule{
		ID:       "stale_statistics",
		Name:     "统计信息过期(Stale Statistics)",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTopSQLDrift},
			{Type: SignalKeyword, Key: "stale statistics"},
			{Type: SignalKeyword, Key: "statistics"},
		},
		Trigger: Trigger{
			Mode: TriggerQuery,
			Conditions: []Condition{
				{Source: "top_sqls", Field: "elapsed_drift", Op: OpGT, Value: 2.0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Top SQL的E-Rows vs A-Rows差异",
			Query: QueryTableStats,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "E-Rows/A-Rows偏差>10x,统计信息严重不准",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查相关表last_analyzed时间",
						Query: QueryTableStats,
						Branches: []Branch{
							{
								Match:    MatchGT(7),
								Label:    "统计信息>7天未更新",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "关键表统计信息超过7天未更新,E-Rows与实际行数偏差>10x"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "立即收集相关表统计信息", SkillCommand: "/sql @gather_stats {schema}.{table}", RawSQL: "BEGIN DBMS_STATS.GATHER_TABLE_STATS(ownname => '{schema}', tabname => '{table}', estimate_percent => DBMS_STATS.AUTO_SAMPLE_SIZE, method_opt => 'FOR ALL COLUMNS SIZE AUTO', cascade => TRUE); END;"},
									{Type: ActionPrevent, Desc: "检查自动统计收集作业是否正常运行", SkillCommand: "/sql @auto_stats_job", RawSQL: "SELECT client_name, status FROM DBA_AUTOTASK_CLIENT WHERE client_name = 'auto optimizer stats collection'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "统计信息近期有更新但仍不准确",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "统计信息存在但不准确,可能需要直方图或更高采样率"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用100%采样重新收集", SkillCommand: "/sql @gather_stats_full", RawSQL: "BEGIN DBMS_STATS.GATHER_TABLE_STATS(ownname => '{schema}', tabname => '{table}', estimate_percent => 100, method_opt => 'FOR ALL COLUMNS SIZE SKEWONLY'); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "E-Rows/A-Rows偏差在合理范围",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "统计信息偏差在正常范围内"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期检查统计信息准确度", SkillCommand: "/health", RawSQL: "SELECT owner, table_name, num_rows, last_analyzed, stale_stats FROM DBA_TAB_STATISTICS WHERE stale_stats = 'YES' AND owner NOT IN ('SYS','SYSTEM')"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"sql_plan_drift", "full_table_scan_oltp"},
		Tags:     []string{"statistics", "optimizer"},
		Versions: "10g+",
	}
}

// ruleVersionCountBloat detects V$SQL version_count > 100, often caused by
// adaptive cursor sharing or bind variable peeking issues.
func ruleVersionCountBloat() *Rule {
	return &Rule{
		ID:       "version_count_bloat",
		Name:     "游标版本膨胀(Version Count Bloat)",
		Category: "sql_perf",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricMutexWait},
			{Type: SignalWaitEvent, Key: "cursor: mutex S"},
			{Type: SignalKeyword, Key: "version count"},
		},
		Trigger: Trigger{
			Mode: TriggerQuery,
			Conditions: []Condition{
				{Source: "metrics", Field: metricMutexWait, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "查询V$SQL中version_count > 100的SQL",
			Query: QueryCursorStats,
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "发现version_count>100",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查V$SQL_SHARED_CURSOR中原因",
						Query: QueryCursorStats,
						Branches: []Branch{
							{
								Match:    MatchEquals("BIND_MISMATCH"),
								Label:    "绑定变量类型不匹配",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大量游标子版本因BIND_MISMATCH,导致mutex争用和shared pool浪费"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "清除问题游标并设置_cursor_obsolete_threshold", SkillCommand: "/sql @purge_cursor {sql_id}", RawSQL: "BEGIN DBMS_SHARED_POOL.PURGE('{address},{hash_value}', 'C'); END;"},
									{Type: ActionPrevent, Desc: "设置_cursor_obsolete_threshold限制版本数", SkillCommand: "/params _cursor_obsolete_threshold", RawSQL: "ALTER SYSTEM SET \"_cursor_obsolete_threshold\" = 100 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致版本膨胀",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "游标版本膨胀,需检查V$SQL_SHARED_CURSOR确定具体原因"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查游标不共享原因", SkillCommand: "/sql @shared_cursor {sql_id}", RawSQL: "SELECT * FROM V$SQL_SHARED_CURSOR WHERE sql_id = '{sql_id}' AND ROWNUM <= 5"},
									{Type: ActionFix, Desc: "清除异常游标释放shared pool", SkillCommand: "/sql @purge_cursor {sql_id}", RawSQL: "EXEC DBMS_SHARED_POOL.PURGE('{address},{hash_value}', 'C')"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未发现版本膨胀问题",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "V$SQL中version_count均在正常范围"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控游标版本趋势", SkillCommand: "/health", RawSQL: "SELECT sql_id, version_count FROM V$SQLAREA WHERE version_count > 50 ORDER BY version_count DESC"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"shared_pool_ora4031"},
		Tags:     []string{"cursor", "mutex", "shared_pool"},
		Versions: "10g+",
	}
}

// ─── Memory Rules (4) ──────────────────────────────────────────────────────

func memoryRules() []*Rule {
	return []*Rule{
		ruleBufferCacheHitLow(),
		ruleSharedPoolORA4031(),
		rulePGAInsufficient(),
		ruleSGAComponentImbalance(),
	}
}

// ruleBufferCacheHitLow detects buffer cache hit ratio below thresholds:
// <95% for OLTP, <85% for DW. Recommends V$DB_CACHE_ADVICE sizing.
func ruleBufferCacheHitLow() *Rule {
	return &Rule{
		ID:       "buffer_cache_hit_low",
		Name:     "Buffer Cache命中率过低",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricBufferCacheHit},
			{Type: SignalKeyword, Key: "buffer cache"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricBufferCacheHit, Op: OpLT, Value: 95},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Buffer Cache命中率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricBufferCacheHit) },
			Branches: []Branch{
				{
					Match:    MatchLT(85),
					Label:    "命中率<85%,严重不足",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "查询V$DB_CACHE_ADVICE获取建议大小",
						Query: QueryDBCacheAdvice,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "DB_CACHE_ADVICE建议增大",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Buffer Cache命中率严重过低(<85%),V$DB_CACHE_ADVICE建议增大缓存"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "根据V$DB_CACHE_ADVICE建议增大db_cache_size", SkillCommand: "/params db_cache_size", RawSQL: "SELECT size_for_estimate, estd_physical_read_factor FROM V$DB_CACHE_ADVICE WHERE name = 'DEFAULT' ORDER BY size_for_estimate"},
									{Type: ActionFix, Desc: "增大Buffer Cache至建议值", SkillCommand: "/sql ALTER SYSTEM SET db_cache_size = '{new_size}'", RawSQL: "ALTER SYSTEM SET db_cache_size = '{advised_size}' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "缓存已足够大,检查SQL问题",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "Buffer Cache已经较大但命中率仍低,可能存在大量全扫描SQL"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查全表扫描SQL", SkillCommand: "/slowsql", RawSQL: "SELECT sql_id, disk_reads, buffer_gets FROM V$SQL WHERE disk_reads/NULLIF(buffer_gets,0) > 0.1 ORDER BY disk_reads DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(95),
					Label:    "命中率85-95%,OLTP场景需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Buffer Cache命中率<95%,OLTP场景偏低"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看DB_CACHE_ADVICE获取建议大小", SkillCommand: "/params db_cache_size", RawSQL: "SELECT size_for_estimate, estd_physical_read_factor FROM V$DB_CACHE_ADVICE WHERE name = 'DEFAULT' ORDER BY size_for_estimate"},
						{Type: ActionFix, Desc: "考虑适度增大db_cache_size", SkillCommand: "/sql ALTER SYSTEM SET db_cache_size = '{size}'", RawSQL: "ALTER SYSTEM SET db_cache_size = '{size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "命中率>=95%,正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Buffer Cache命中率正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"full_table_scan_oltp"},
		CausesOf: []string{},
		Tags:     []string{"memory", "buffer_cache", "sga"},
		Versions: "9i+",
	}
}

// ruleSharedPoolORA4031 detects ORA-4031 (shared pool out of memory),
// checks SP free percentage and hard parse pressure.
func ruleSharedPoolORA4031() *Rule {
	return &Rule{
		ID:       "shared_pool_ora4031",
		Name:     "Shared Pool内存不足(ORA-4031)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-04031"},
			{Type: SignalMetric, Key: metricSharedPoolFreePct},
			{Type: SignalKeyword, Key: "ORA-4031"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSharedPoolFreePct, Op: OpLT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Shared Pool空闲内存比例",
			Query: QuerySPFreeMemory,
			Branches: []Branch{
				{
					Match:    MatchLT(5),
					Label:    "空闲<5%,极度紧张",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查hard parse是否为主因",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricHardParse) },
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "hard parse高,literal SQL耗尽shared pool",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Shared Pool空闲<5%且硬解析率高,大量literal SQL耗尽内存"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "紧急flush shared pool释放碎片", SkillCommand: "/sql ALTER SYSTEM FLUSH SHARED_POOL", RawSQL: "ALTER SYSTEM FLUSH SHARED_POOL"},
									{Type: ActionFix, Desc: "设置cursor_sharing=FORCE并增大shared_pool_size", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "hard parse正常,SP本身不足",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Shared Pool空闲极低但非硬解析导致,可能shared_pool_size配置过小"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大shared_pool_size", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionPrevent, Desc: "设置shared_pool_reserved_size保留空间", SkillCommand: "/params shared_pool_reserved_size", RawSQL: "ALTER SYSTEM SET shared_pool_reserved_size = '{size}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(10),
					Label:    "空闲5-10%,需要关注",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "Shared Pool空闲比例<10%,接近ORA-4031临界点"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查shared pool内存分布", SkillCommand: "/sql @sp_breakdown", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'shared pool' ORDER BY bytes DESC FETCH FIRST 10 ROWS ONLY"},
						{Type: ActionFix, Desc: "考虑增大shared_pool_size", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Shared Pool空闲充足",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Shared Pool空闲内存充足"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"hard_parse_storm", "version_count_bloat"},
		CausesOf: []string{},
		Tags:     []string{"memory", "shared_pool", "ora-4031"},
		Versions: "9i+",
	}
}

// rulePGAInsufficient detects PGA memory pressure: cache hit <90%,
// overalloc >0, multipass sort/hash.
func rulePGAInsufficient() *Rule {
	return &Rule{
		ID:       "pga_insufficient",
		Name:     "PGA内存不足(Multipass Operations)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPGAUsedPct},
			{Type: SignalKeyword, Key: "PGA"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPGAUsedPct, Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step:  "检查PGA cache hit percentage和over-allocation",
			Query: QueryPGAAdvice,
			Branches: []Branch{
				{
					Match:    MatchLT(80),
					Label:    "PGA cache hit<80%,大量磁盘排序",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否存在multipass操作",
						Query: QueryPGAAdvice,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在multipass hash/sort",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "PGA严重不足,出现multipass排序/哈希操作,性能降低数量级"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大pga_aggregate_target", SkillCommand: "/params pga_aggregate_target", RawSQL: "ALTER SYSTEM SET pga_aggregate_target = '{new_size}' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "查找消耗PGA最多的SQL", SkillCommand: "/slowsql", RawSQL: "SELECT sql_id, operation_type, policy, estimated_optimal_size/1024/1024 opt_mb FROM V$SQL_WORKAREA_ACTIVE WHERE policy != 'AUTO' OR multipass_executions > 0"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "大量onepass操作",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "PGA不足导致排序/哈希操作使用onepass模式"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "根据V$PGA_TARGET_ADVICE调整pga_aggregate_target", SkillCommand: "/params pga_aggregate_target", RawSQL: "SELECT pga_target_for_estimate/1024/1024 target_mb, estd_extra_bytes_rw FROM V$PGA_TARGET_ADVICE ORDER BY pga_target_for_estimate"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(90),
					Label:    "PGA cache hit 80-90%,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "PGA cache hit ratio在80-90%,存在onepass操作"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看PGA_TARGET_ADVICE建议值", SkillCommand: "/params pga_aggregate_target", RawSQL: "SELECT pga_target_for_estimate/1024/1024 target_mb, estd_overalloc_count FROM V$PGA_TARGET_ADVICE"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "PGA使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "PGA cache hit ratio正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"temp_space_full"},
		Tags:     []string{"memory", "pga", "sort"},
		Versions: "9i+",
	}
}

// ruleSGAComponentImbalance detects SGA auto-tuning thrashing when
// components resize >10 times per day.
func ruleSGAComponentImbalance() *Rule {
	return &Rule{
		ID:       "sga_component_imbalance",
		Name:     "SGA组件自动调整频繁(Resize Thrashing)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricSharedPoolFreePct},
			{Type: SignalMetric, Key: metricBufferCacheHit},
			{Type: SignalKeyword, Key: "SGA resize"},
		},
		Trigger: Trigger{
			Mode: TriggerQuery,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSharedPoolFreePct, Op: OpLT, Value: 15},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$SGA_RESIZE_OPS中resize次数",
			Query: QuerySegmentStats,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "24h内resize>10次,频繁抖动",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "SGA组件在24小时内resize超过10次,ASMM自动调整频繁抖动"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "设置各组件最小值防止抖动", SkillCommand: "/params db_cache_size", RawSQL: "ALTER SYSTEM SET db_cache_size = '{min_size}' SCOPE=BOTH; ALTER SYSTEM SET shared_pool_size = '{min_size}' SCOPE=BOTH"},
						{Type: ActionPrevent, Desc: "检查sga_target与sga_max_size设置", SkillCommand: "/params sga_target", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('sga_target','sga_max_size','db_cache_size','shared_pool_size')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "SGA resize次数正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SGA组件resize频率在正常范围"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期检查SGA resize历史", SkillCommand: "/sql @sga_resize", RawSQL: "SELECT component, oper_type, final_size/1024/1024 MB, start_time FROM V$SGA_RESIZE_OPS ORDER BY start_time DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"buffer_cache_hit_low", "shared_pool_ora4031"},
		Tags:     []string{"memory", "sga", "asmm"},
		Versions: "10g+",
	}
}

// ─── Space Rules (3) ────────────────────────────────────────────────────────

func spaceRules() []*Rule {
	return []*Rule{
		ruleTablespaceFull(),
		ruleTempSpaceFull(),
		ruleUndoSpaceFull(),
	}
}

// ruleTablespaceFull detects tablespace usage >85% (warn) or >95% (critical).
func ruleTablespaceFull() *Rule {
	return &Rule{
		ID:       "tablespace_full",
		Name:     "表空间使用率过高(Tablespace Full)",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTablespaceUsedPct},
			{Type: SignalErrorCode, Key: "ORA-01653"},
			{Type: SignalKeyword, Key: "tablespace full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "space_details", Field: "tablespace_used_pct", Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step:  "检查表空间使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricTablespaceUsedPct) },
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "使用率>95%,紧急",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否可以autoextend或添加数据文件",
						Query: QueryDatafileStatus,
						Branches: []Branch{
							{
								Match:    MatchBool(true),
								Label:    "可autoextend",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "表空间使用率>95%但数据文件可自动扩展"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "确认autoextend maxsize是否充足", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, file_name, autoextensible, maxbytes/1024/1024 max_mb FROM DBA_DATA_FILES WHERE tablespace_name = '{ts}'"},
									{Type: ActionFix, Desc: "增大maxsize或添加数据文件", SkillCommand: "/sql ALTER TABLESPACE {ts} ADD DATAFILE", RawSQL: "ALTER TABLESPACE {ts} ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无法自动扩展",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "表空间使用率>95%且无法自动扩展,需立即处理"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即添加数据文件", SkillCommand: "/sql ALTER TABLESPACE {ts} ADD DATAFILE", RawSQL: "ALTER TABLESPACE {ts} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
									{Type: ActionInvestigate, Desc: "检查大段对象是否可压缩或归档", SkillCommand: "/sql @large_segments {ts}", RawSQL: "SELECT owner, segment_name, segment_type, bytes/1024/1024 MB FROM DBA_SEGMENTS WHERE tablespace_name = '{ts}' ORDER BY bytes DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(85),
					Label:    "使用率85-95%,预警",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "表空间使用率>85%,需规划扩容"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "规划添加数据文件或启用autoextend", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, ROUND(used_percent,1) pct FROM DBA_TABLESPACE_USAGE_METRICS WHERE used_percent > 85 ORDER BY used_percent DESC"},
						{Type: ActionPrevent, Desc: "设置表空间监控告警阈值", SkillCommand: "/alert", RawSQL: "BEGIN DBMS_SERVER_ALERT.SET_THRESHOLD(DBMS_SERVER_ALERT.TABLESPACE_PCT_FULL, DBMS_SERVER_ALERT.OPERATOR_GE, '85', DBMS_SERVER_ALERT.OPERATOR_GE, '95', 1, 1, NULL, DBMS_SERVER_ALERT.OBJECT_TYPE_TABLESPACE, '{ts}'); END;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "表空间使用率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "表空间使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"space", "tablespace"},
		Versions: "9i+",
	}
}

// ruleTempSpaceFull detects TEMP tablespace usage >90%, often caused by
// disk sorts when PGA is too small.
func ruleTempSpaceFull() *Rule {
	return &Rule{
		ID:       "temp_space_full",
		Name:     "临时表空间使用率过高(Temp Space Full)",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTempUsedPct},
			{Type: SignalErrorCode, Key: "ORA-01652"},
			{Type: SignalKeyword, Key: "temp space"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "space_details", Field: "temp_used_pct", Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查TEMP表空间使用率及消耗者",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricTempUsedPct) },
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "TEMP>95%,临界状态",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "临时表空间使用率>95%,排序/哈希操作面临失败风险"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "查找TEMP空间大户并考虑终止", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.sql_id, u.blocks * 8/1024 MB FROM V$SORT_USAGE u JOIN V$SESSION s ON u.session_addr = s.saddr ORDER BY u.blocks DESC"},
						{Type: ActionFix, Desc: "增大TEMP表空间或增加临时文件", SkillCommand: "/sql ALTER TABLESPACE TEMP ADD TEMPFILE", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
					},
				},
				{
					Match:    MatchGT(90),
					Label:    "TEMP 90-95%,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "临时表空间使用率>90%,可能存在大排序SQL或PGA配置不足"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查PGA大小是否足够", SkillCommand: "/params pga_aggregate_target", RawSQL: "SELECT pga_target_for_estimate/1024/1024 target_mb, estd_overalloc_count FROM V$PGA_TARGET_ADVICE"},
						{Type: ActionFix, Desc: "增大pga_aggregate_target减少磁盘排序", SkillCommand: "/params pga_aggregate_target", RawSQL: "ALTER SYSTEM SET pga_aggregate_target = '{new_size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "TEMP使用率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "临时表空间使用率正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"pga_insufficient"},
		CausesOf: []string{},
		Tags:     []string{"space", "temp", "sort"},
		Versions: "9i+",
	}
}

// ruleUndoSpaceFull detects UNDO tablespace usage >90%, risk of ORA-01555,
// retention insufficient.
func ruleUndoSpaceFull() *Rule {
	return &Rule{
		ID:       "undo_space_full",
		Name:     "Undo表空间使用率过高(ORA-01555风险)",
		Category: "space",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricUndoUsedPct},
			{Type: SignalErrorCode, Key: "ORA-01555"},
			{Type: SignalKeyword, Key: "undo space"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "space_details", Field: "undo_used_pct", Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查UNDO表空间使用率与retention",
			Query: QueryUndoStats,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "UNDO>95%,ORA-01555高风险",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Undo表空间使用率>95%,存在ORA-01555快照过旧风险"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查大事务并评估是否可终止", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.sql_id, t.used_ublk * 8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.addr = s.taddr ORDER BY t.used_ublk DESC"},
						{Type: ActionFix, Desc: "增大UNDO表空间并调整undo_retention", SkillCommand: "/params undo_retention", RawSQL: "ALTER SYSTEM SET undo_retention = 1800 SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchGT(90),
					Label:    "UNDO 90-95%,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "Undo表空间使用率>90%,接近临界点"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查undo segment利用分布", SkillCommand: "/sql @undo_segments", RawSQL: "SELECT tablespace_name, status, COUNT(*), SUM(bytes)/1024/1024 MB FROM DBA_UNDO_EXTENTS GROUP BY tablespace_name, status ORDER BY 4 DESC"},
						{Type: ActionFix, Desc: "增大UNDO表空间", SkillCommand: "/sql ALTER TABLESPACE UNDOTBS1 ADD DATAFILE", RawSQL: "ALTER TABLESPACE UNDOTBS1 ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "UNDO使用率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Undo表空间使用率正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"long_transaction"},
		CausesOf: []string{},
		Tags:     []string{"space", "undo", "ora-01555"},
		Versions: "9i+",
	}
}

// ─── Lock/Concurrency Rules (3) ──────────────────────────────────────────────

func lockRules() []*Rule {
	return []*Rule{
		ruleDeadlock(),
		ruleITLContention(),
		ruleHotBlockCBCLatch(),
	}
}

// ruleDeadlock detects ORA-00060 deadlocks, commonly caused by FK without
// index or inconsistent DML order.
func ruleDeadlock() *Rule {
	return &Rule{
		ID:       "deadlock",
		Name:     "死锁检测(ORA-00060 Deadlock)",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00060"},
			{Type: SignalMetric, Key: metricEnqueueDeadlocks},
			{Type: SignalKeyword, Key: "deadlock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricEnqueueDeadlocks, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "确认ORA-00060死锁发生",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricEnqueueDeadlocks) },
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在死锁事件",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否存在FK无索引(常见死锁原因)",
						Query: QueryFKNoIndex,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "发现FK无索引",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "发现外键无索引,子表DML会对父表加全表锁导致死锁"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即为无索引的FK创建索引", SkillCommand: "/indexadvise {table_name}", RawSQL: "-- 查找无索引FK:\nSELECT c.table_name, cc.column_name FROM DBA_CONSTRAINTS c JOIN DBA_CONS_COLUMNS cc ON c.constraint_name = cc.constraint_name WHERE c.constraint_type = 'R' AND NOT EXISTS (SELECT 1 FROM DBA_IND_COLUMNS ic WHERE ic.table_name = cc.table_name AND ic.column_name = cc.column_name)"},
									{Type: ActionPrevent, Desc: "审查应用DML操作顺序", SkillCommand: "/alert", RawSQL: "SELECT * FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00060%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无FK索引问题,可能DML顺序不一致",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "死锁非FK无索引导致,可能因应用程序DML操作顺序不一致"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查alert log中死锁trace详情", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00060%' AND originating_timestamp > SYSDATE - 1"},
									{Type: ActionFix, Desc: "分析trace文件确定涉及的SQL和对象", SkillCommand: "/sql @deadlock_trace", RawSQL: "SELECT value FROM V$DIAG_INFO WHERE name = 'Default Trace File'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无死锁事件",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到死锁"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "deadlock", "fk_index"},
		Versions: "9i+",
	}
}

// ruleITLContention detects ITL (Interested Transaction List) waits
// when INITRANS is too low (1-2). Fix: increase to 8-16.
func ruleITLContention() *Rule {
	return &Rule{
		ID:       "itl_contention",
		Name:     "ITL争用(Interested Transaction List Contention)",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "enq: TX - allocate ITL entry"},
			{Type: SignalKeyword, Key: "ITL contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "enq: TX - allocate ITL entry", Op: OpPctGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "确认ITL等待事件并定位热点对象",
			Query: QueryASHHotObject,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现ITL争用热点对象",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查对象INITRANS设置",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchLT(4),
								Label:    "INITRANS < 4,过低",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "热点对象INITRANS设置过低(1-2),并发事务无法获取ITL槽位"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大表/索引的INITRANS至8-16", SkillCommand: "/sql ALTER TABLE {table} INITRANS 16", RawSQL: "ALTER TABLE {schema}.{table} INITRANS 16; ALTER INDEX {schema}.{index} INITRANS 16"},
									{Type: ActionPrevent, Desc: "对高并发表在线重组应用新INITRANS", SkillCommand: "/sql @reorg_table {table}", RawSQL: "ALTER TABLE {schema}.{table} MOVE ONLINE; ALTER INDEX {schema}.{index} REBUILD ONLINE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "INITRANS已较大,可能block太热",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "INITRANS配置合理但仍有ITL争用,可能单block过热"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否为热块问题", SkillCommand: "/sql @hot_block", RawSQL: "SELECT obj#, dataobj#, COUNT(*) FROM V$BH WHERE status != 'free' GROUP BY obj#, dataobj# ORDER BY 3 DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未发现明显ITL争用热点",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ITL争用未集中在特定对象"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查ASH中ITL等待详情", SkillCommand: "/waits", RawSQL: "SELECT current_obj#, COUNT(*) FROM V$ACTIVE_SESSION_HISTORY WHERE event = 'enq: TX - allocate ITL entry' AND sample_time > SYSDATE - 1/24 GROUP BY current_obj# ORDER BY 2 DESC"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "itl", "initrans"},
		Versions: "9i+",
	}
}

// ruleHotBlockCBCLatch detects cache buffers chains latch contention
// caused by a hot block, with CBC sleeps/gets >1%.
func ruleHotBlockCBCLatch() *Rule {
	return &Rule{
		ID:       "hot_block_cbc_latch",
		Name:     "热块CBC Latch争用(Cache Buffers Chains)",
		Category: "lock",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "latch: cache buffers chains"},
			{Type: SignalMetric, Key: metricLatchFreeRate},
			{Type: SignalKeyword, Key: "hot block"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "latch: cache buffers chains", Op: OpPctGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查CBC latch miss ratio",
			Query: QueryLatchStats,
			Branches: []Branch{
				{
					Match:    MatchGT(1),
					Label:    "CBC latch sleeps/gets >1%",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "通过ASH定位热块对象",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "定位到热块对象",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "CBC latch争用严重,ASH中集中在特定热块对象"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "对热点表/索引进行hash分区或反转键索引", SkillCommand: "/tableinfo {table_name}", RawSQL: "SELECT object_name, object_type, subobject_name FROM DBA_OBJECTS WHERE object_id = {obj_id}"},
									{Type: ActionFix, Desc: "调整PCTFREE增加每块行数分散度", SkillCommand: "/sql ALTER TABLE {table} PCTFREE 20", RawSQL: "ALTER TABLE {schema}.{table} PCTFREE 20; ALTER TABLE {schema}.{table} MOVE ONLINE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未定位到特定热块",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "CBC latch争用存在但未集中在单一对象"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "使用V$SESSION_WAIT中P1RAW定位", SkillCommand: "/latches", RawSQL: "SELECT latch#, name, gets, misses, sleeps, ROUND(sleeps/NULLIF(gets,0)*100,2) miss_pct FROM V$LATCH WHERE name = 'cache buffers chains'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "CBC latch争用不严重",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "cache buffers chains latch争用在正常范围"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控latch趋势", SkillCommand: "/latches", RawSQL: "SELECT name, gets, misses, sleeps FROM V$LATCH WHERE name LIKE 'cache buffers%'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"lock", "latch", "hot_block", "cbc"},
		Versions: "9i+",
	}
}

// ─── Redo/Archive Rules (2) ──────────────────────────────────────────────────

func redoArchiveRules() []*Rule {
	return []*Rule{
		ruleRedoLogSwitchFrequent(),
		ruleArchiveSpaceFull(),
	}
}

// ruleRedoLogSwitchFrequent detects redo log switches >4 per hour,
// indicating redo log files are too small.
func ruleRedoLogSwitchFrequent() *Rule {
	return &Rule{
		ID:       "redo_log_switch_frequent",
		Name:     "Redo日志切换过频(Log Switch Frequent)",
		Category: "redo",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricLogSwitchRate},
			{Type: SignalMetric, Key: metricCheckpointNotComplete},
			{Type: SignalKeyword, Key: "log switch"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricLogSwitchRate, Op: OpGT, Value: 4},
			},
		},
		Tree: &TreeNode{
			Step:  "检查日志切换频率与redo log大小",
			Query: QueryRedoLogInfo,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "每小时切换>10次,严重过频",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否伴随checkpoint not complete",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue(metricCheckpointNotComplete)
						},
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "出现checkpoint not complete",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Redo切换极频繁且出现checkpoint not complete,I/O写入跟不上"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大redo log文件至2-4GB", SkillCommand: "/sql @resize_redo", RawSQL: "-- 添加新组(大容量)后删除旧组:\nALTER DATABASE ADD LOGFILE GROUP {n} ('{path}') SIZE 4G;\nALTER SYSTEM SWITCH LOGFILE;\nALTER SYSTEM CHECKPOINT;\nALTER DATABASE DROP LOGFILE GROUP {old_n};"},
									{Type: ActionFix, Desc: "增加redo log组数(建议>=4组)", SkillCommand: "/sql @add_loggroup", RawSQL: "SELECT group#, bytes/1024/1024 MB, members, status FROM V$LOG ORDER BY group#"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无checkpoint not complete",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "Redo切换频繁但checkpoint尚可跟上"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大redo log文件大小", SkillCommand: "/sql @resize_redo", RawSQL: "SELECT group#, bytes/1024/1024 size_mb, status FROM V$LOG ORDER BY group#"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(4),
					Label:    "每小时切换4-10次",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "Redo日志每小时切换4-10次,建议增大redo log"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大redo log文件至1-2GB", SkillCommand: "/sql @redo_info", RawSQL: "SELECT group#, bytes/1024/1024 MB, status FROM V$LOG"},
						{Type: ActionPrevent, Desc: "监控redo切换趋势", SkillCommand: "/health", RawSQL: "SELECT TO_CHAR(first_time,'YYYY-MM-DD HH24') hr, COUNT(*) switches FROM V$LOG_HISTORY WHERE first_time > SYSDATE - 1 GROUP BY TO_CHAR(first_time,'YYYY-MM-DD HH24') ORDER BY 1"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "切换频率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Redo日志切换频率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"redo", "log_switch", "checkpoint"},
		Versions: "9i+",
	}
}

// ruleArchiveSpaceFull detects archive destination usage >90%.
// Fix: RMAN delete obsolete or increase archive space.
func ruleArchiveSpaceFull() *Rule {
	return &Rule{
		ID:       "archive_space_full",
		Name:     "归档空间不足(Archive Destination Full)",
		Category: "redo",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFRAUsedPct},
			{Type: SignalErrorCode, Key: "ORA-00257"},
			{Type: SignalKeyword, Key: "archive full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFRAUsedPct, Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查归档目的地/FRA使用率",
			Query: QueryArchiveStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "归档空间>95%,紧急",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "归档目的地使用率>95%,数据库面临挂起风险(无法归档则无法切换redo)"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即使用RMAN删除过期归档", SkillCommand: "/backup", RawSQL: "RMAN> DELETE NOPROMPT ARCHIVELOG ALL COMPLETED BEFORE 'SYSDATE-3';"},
						{Type: ActionFix, Desc: "增大FRA或归档目的地空间", SkillCommand: "/params db_recovery_file_dest_size", RawSQL: "ALTER SYSTEM SET db_recovery_file_dest_size = '{new_size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchGT(90),
					Label:    "归档空间90-95%",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "归档空间使用率>90%,需尽快清理"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "使用RMAN清理过期备份和归档", SkillCommand: "/backup", RawSQL: "RMAN> DELETE NOPROMPT OBSOLETE;"},
						{Type: ActionPrevent, Desc: "配置RMAN自动清理策略", SkillCommand: "/backup", RawSQL: "RMAN> CONFIGURE RETENTION POLICY TO RECOVERY WINDOW OF 7 DAYS;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "归档空间使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "归档目的地空间充足"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"redo_log_switch_frequent"},
		CausesOf: []string{},
		Tags:     []string{"redo", "archive", "fra"},
		Versions: "10g+",
	}
}

// ─── Emergency Rules (3) ────────────────────────────────────────────────────

func emergencyRules() []*Rule {
	return []*Rule{
		ruleORA600(),
		ruleDatabaseHang(),
		ruleConnectionExhaust(),
	}
}

// ruleORA600 detects ORA-00600 (internal error), checks if it repeats,
// and recommends MOS lookup.
func ruleORA600() *Rule {
	return &Rule{
		ID:       "ora_600",
		Name:     "ORA-00600内部错误(Internal Error)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00600"},
			{Type: SignalMetric, Key: metricAlertLogErrors},
			{Type: SignalKeyword, Key: "ORA-600"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricAlertLogErrors, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查alert log中ORA-00600出现频率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricAlertLogErrors) },
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "频繁出现ORA-600(>5次/天)",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否为同一参数组合(重复bug)",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(1),
								Label:    "同一参数重复出现,可能是已知bug",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-00600频繁出现且参数相同,极可能是已知Oracle bug"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "收集trace文件并在MOS(Doc ID 153788.1)查询", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00600%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC"},
									{Type: ActionFix, Desc: "应用对应PSU/补丁", SkillCommand: "/sql @oracle_version", RawSQL: "SELECT banner_full FROM V$VERSION"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "不同参数,多种内部错误",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "多种不同的ORA-00600,系统可能不稳定"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "开SR联系Oracle Support", SkillCommand: "/alert", RawSQL: "SELECT message_text, originating_timestamp FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00600%' ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "偶发ORA-600",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "出现ORA-00600内部错误,需检查是否影响业务"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "收集trace文件并查看error stack", SkillCommand: "/alert", RawSQL: "SELECT value FROM V$DIAG_INFO WHERE name = 'Default Trace File'"},
						{Type: ActionPrevent, Desc: "在MOS中搜索对应的bug和补丁", SkillCommand: "/sql @oracle_version", RawSQL: "SELECT banner_full FROM V$VERSION"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无ORA-600",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到ORA-00600"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-600", "internal_error"},
		Versions: "9i+",
	}
}

// ruleDatabaseHang detects all sessions stuck (database hang), recommends
// hanganalyze and systemstate dump.
func ruleDatabaseHang() *Rule {
	return &Rule{
		ID:       "database_hang",
		Name:     "数据库挂起(Database Hang)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricActive},
			{Type: SignalMetric, Key: metricCommitRate},
			{Type: SignalKeyword, Key: "database hang"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricActive, Op: OpGT, Value: 50},
				{Source: "metrics", Field: metricCommitRate, Op: OpLT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查活跃会话数量与TPS",
			Check: func(ctx *EvalContext) interface{} {
				active := ctx.MetricValue(metricActive)
				tps := ctx.MetricValue(metricCommitRate)
				if active > 50 && tps < 1 {
					return float64(1) // hang confirmed
				}
				return float64(0)
			},
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "大量活跃会话但TPS接近0,确认hang",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查blocking chains深度",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricBlockingChains) },
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在深层blocking chain",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "数据库疑似hang: 大量活跃会话, TPS为0, 存在blocking chain"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "执行hanganalyze获取等待链", SkillCommand: "/sql @hanganalyze", RawSQL: "ORADEBUG SETMYPID\nORADEBUG HANGANALYZE 3"},
									{Type: ActionUrgent, Desc: "执行systemstate dump", SkillCommand: "/sql @systemstate", RawSQL: "ORADEBUG SETMYPID\nORADEBUG DUMP SYSTEMSTATE 266"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无明显blocking但仍hang",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "数据库活跃会话高但TPS为0,无blocking chain,可能为内部资源争用"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "执行hanganalyze并收集systemstate", SkillCommand: "/sql @hanganalyze", RawSQL: "ORADEBUG SETMYPID\nORADEBUG HANGANALYZE 3\nORADEBUG DUMP SYSTEMSTATE 266"},
									{Type: ActionUrgent, Desc: "检查是否需要重启实例", SkillCommand: "/alert", RawSQL: "SELECT inst_id, status, active_state FROM GV$INSTANCE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "数据库运行正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "数据库未检测到hang状态"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "hang", "blocked"},
		Versions: "9i+",
	}
}

// ruleConnectionExhaust detects sessions reaching >85% of the configured
// limit, indicating a connection leak or insufficient pool sizing.
func ruleConnectionExhaust() *Rule {
	return &Rule{
		ID:       "connection_exhaust",
		Name:     "连接数耗尽(Connection Exhaustion)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricResourceLimitPct},
			{Type: SignalMetric, Key: metricTotalSessions},
			{Type: SignalKeyword, Key: "connection exhaust"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricResourceLimitPct, Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step:  "检查sessions/processes相对于limit的使用率",
			Query: QueryResourceLimit,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "sessions使用率>95%,即将耗尽",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "会话数使用率>95%,新连接即将被拒绝(ORA-00018/ORA-00020)"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即清理idle和泄漏连接", SkillCommand: "/sessions", RawSQL: "SELECT sid, serial#, username, machine, status, last_call_et FROM V$SESSION WHERE status = 'INACTIVE' AND last_call_et > 3600 ORDER BY last_call_et DESC"},
						{Type: ActionFix, Desc: "增大sessions/processes参数(需重启)", SkillCommand: "/params sessions", RawSQL: "ALTER SYSTEM SET sessions = {new_val} SCOPE=SPFILE; ALTER SYSTEM SET processes = {new_val} SCOPE=SPFILE"},
					},
				},
				{
					Match:    MatchGT(85),
					Label:    "sessions使用率85-95%",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "会话数使用率>85%,存在连接泄漏风险"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "按machine/program分析连接分布", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, COUNT(*) cnt FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program ORDER BY cnt DESC"},
						{Type: ActionFix, Desc: "通知应用团队排查连接池配置", SkillCommand: "/params sessions", RawSQL: "SELECT resource_name, current_utilization, max_utilization, limit_value FROM V$RESOURCE_LIMIT WHERE resource_name IN ('sessions','processes')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "连接数正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "会话/连接使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"session_leak"},
		CausesOf: []string{},
		Tags:     []string{"emergency", "connection", "sessions"},
		Versions: "9i+",
	}
}

// ─── Session Rules (2) ──────────────────────────────────────────────────────

func sessionRules() []*Rule {
	return []*Rule{
		ruleLongTransaction(),
		ruleSessionLeak(),
	}
}

// ruleLongTransaction detects transactions active >30 minutes,
// checking undo growth and whether they block others.
func ruleLongTransaction() *Rule {
	return &Rule{
		ID:       "long_transaction",
		Name:     "长事务检测(Long Transaction)",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricLongSQL},
			{Type: SignalKeyword, Key: "long transaction"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricLongSQL, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查活跃事务持续时间",
			Query: QueryLongTransactions,
			Branches: []Branch{
				{
					Match:    MatchGT(60),
					Label:    "事务持续>60分钟",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查该事务是否阻塞其他会话",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricBlockingChains) },
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "长事务正在阻塞其他会话",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "长事务(>60min)且正在阻塞其他会话,导致级联等待"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "评估是否可以kill该会话", SkillCommand: "/kill {sid},{serial#}", RawSQL: "ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE"},
									{Type: ActionInvestigate, Desc: "检查事务undo消耗量", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, t.used_ublk*8/1024 undo_mb, t.start_time FROM V$TRANSACTION t JOIN V$SESSION s ON t.addr = s.taddr WHERE t.used_ublk > 1000 ORDER BY t.used_ublk DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "长事务未阻塞他人",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "长事务(>60min)但未阻塞其他会话,仍占用undo资源"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查事务SQL和undo消耗", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.sql_id, t.used_ublk*8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.addr = s.taddr ORDER BY t.start_time"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(30),
					Label:    "事务持续30-60分钟",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在活跃>30分钟的长事务"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看长事务详情", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.sql_id, s.machine, t.start_time FROM V$TRANSACTION t JOIN V$SESSION s ON t.addr = s.taddr WHERE (SYSDATE - t.start_time)*24*60 > 30"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无长事务",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到超长事务"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"undo_space_full"},
		Tags:     []string{"session", "transaction", "undo"},
		Versions: "9i+",
	}
}

// ruleSessionLeak detects idle sessions growing over time,
// indicating a connection pool leak.
func ruleSessionLeak() *Rule {
	return &Rule{
		ID:       "session_leak",
		Name:     "会话泄漏(Session Leak)",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTotalSessions},
			{Type: SignalMetric, Key: metricSessionCreationRate},
			{Type: SignalKeyword, Key: "session leak"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSessionCreationRate, Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查idle session增长趋势",
			Query: QueryResourceLimit,
			Branches: []Branch{
				{
					Match:    MatchGT(70),
					Label:    "idle session占比>70%",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查新建会话速率是否异常",
						Check: func(ctx *EvalContext) interface{} {
							return ctx.MetricValue(metricSessionCreationRate)
						},
						Branches: []Branch{
							{
								Match:    MatchGT(20),
								Label:    "新建会话速率>20/s,泄漏严重",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "idle会话占比>70%且新建速率>20/s,存在严重连接泄漏"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "按machine/program定位泄漏源", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, status, COUNT(*) FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program, status ORDER BY 4 DESC"},
									{Type: ActionFix, Desc: "配置连接池idle超时或Oracle profile限制", SkillCommand: "/sql @create_profile", RawSQL: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME 30; ALTER PROFILE DEFAULT LIMIT CONNECT_TIME 480"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "idle高但新建速率正常",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "idle会话占比偏高,连接池可能配置过大"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析idle会话来源", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, COUNT(*) cnt FROM V$SESSION WHERE status = 'INACTIVE' AND last_call_et > 600 GROUP BY machine, program ORDER BY cnt DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "会话分布正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "idle会话占比在正常范围"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期检查会话趋势", SkillCommand: "/health", RawSQL: "SELECT resource_name, current_utilization, max_utilization FROM V$RESOURCE_LIMIT WHERE resource_name = 'sessions'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"connection_exhaust"},
		Tags:     []string{"session", "leak", "connection_pool"},
		Versions: "9i+",
	}
}

// ─── All Other Rules (aggregate) ────────────────────────────────────────────

// otherRules returns P0 base diagnostic rules only (22 rules).
// P1-P3 rules are registered directly in CommunityProvider.Rules().
func otherRules() []*Rule {
	var rules []*Rule
	rules = append(rules, sqlPerfRules()...)
	rules = append(rules, memoryRules()...)
	rules = append(rules, spaceRules()...)
	rules = append(rules, lockRules()...)
	rules = append(rules, redoArchiveRules()...)
	rules = append(rules, emergencyRules()...)
	rules = append(rules, sessionRules()...)
	return rules
}
