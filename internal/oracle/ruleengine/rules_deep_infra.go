/*-------------------------------------------------------------------------
 *
 * rules_deep_infra.go
 *	  Oracle rule engine — deep-infrastructure rules (storage, ASM, redo, archive log paths).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_deep_infra.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Additional metric name constants for deep infrastructure rules ──────────

const (
	metricResultCacheLatch     = "result_cache_latch"
	metricLargePoolFreePct     = "large_pool_free_pct"
	metricJavaPoolUsedPct      = "java_pool_used_pct"
	metricHugePagesConfigured  = "hugepages_configured"
	metricAMMEnabled           = "amm_enabled"
	metricSGAResizeFreq        = "sga_resize_frequency"
	metricASMDiskStatus        = "asm_disk_status"
	metricASMRebalanceActive   = "asm_rebalance_active"
	metricDatafileAutoextend   = "datafile_autoextend_off_count"
	metricDatafileMaxsizeHit   = "datafile_maxsize_hit_count"
	metricLargeTableNoCompress = "large_table_no_compress_count"
	metricHWMWastePct          = "hwm_waste_pct"
	metricFilesystemIOOptions  = "filesystemio_options"
	metricORA1555Count         = "ora_1555_count"
	metricUndoSegContention    = "undo_segment_contention"
	metricUndoAutoTune         = "undo_autotune_retention"
	metricUndoGuarantee        = "undo_guarantee_enabled"
	metricDRCPEnabled          = "drcp_enabled"
	metricIdleInTransaction    = "idle_in_transaction"
	metricProcessLimitPct      = "process_limit_pct"
	metricCPUUsedPct           = "cpu_used_pct"
	metricSGASizeGB            = "sga_size_gb"
	metricRMANChannels         = "rman_channels"
	metricSharedServerEnabled  = "shared_server_enabled"
	metricOJVMInstalled        = "ojvm_installed"
	metricMemoryTarget         = "memory_target"
	metricConnectionCount      = "connection_count"
	metricIdleSessionRatio     = "idle_session_ratio"
)

// ─── Additional QueryID constants for deep infrastructure rules ──────────────

const (
	QueryResultCacheLatch    QueryID = "result_cache_latch"
	QueryLargePoolStats      QueryID = "large_pool_stats"
	QueryJavaPoolStats       QueryID = "java_pool_stats"
	QueryHugePagesInfo       QueryID = "hugepages_info"
	QueryMemoryModelInfo     QueryID = "memory_model_info"
	QuerySGAResizeOps        QueryID = "sga_resize_ops"
	QueryASMDiskStatus       QueryID = "asm_disk_status"
	QueryASMRebalanceInfo    QueryID = "asm_rebalance_info"
	QueryDatafileAutoextend  QueryID = "datafile_autoextend"
	QueryDatafileMaxsize     QueryID = "datafile_maxsize"
	QueryLargeTableCompress  QueryID = "large_table_compress"
	QueryHWMWaste            QueryID = "hwm_waste"
	QueryFilesystemIOOptions QueryID = "filesystemio_options"
	QueryORA1555Stats        QueryID = "ora_1555_stats"
	QueryUndoSegContention   QueryID = "undo_segment_contention"
	QueryUndoAutoTune        QueryID = "undo_autotune"
	QueryUndoRetGuarantee    QueryID = "undo_retention_guarantee"
	QueryDRCPStats           QueryID = "drcp_stats"
	QueryIdleInTransaction   QueryID = "idle_in_transaction"
	QueryProcessLimit        QueryID = "process_limit"
)

// ─── Deep Memory Rules (6) ───────────────────────────────────────────────────

func deepMemoryRules() []*Rule {
	return []*Rule{
		ruleResultCacheContention(),
		ruleLargePoolInsufficient(),
		ruleJavaPoolInsufficient(),
		ruleHugePagesNotConfigured(),
		ruleAMMvsASMM(),
		ruleSGAAutoResize(),
	}
}

// ruleResultCacheContention detects Result Cache latch contention (RC latch)
// caused by FORCE mode on tables with high DML activity. FORCE mode makes
// every query attempt to cache its result, generating massive RC latch waits
// when the underlying data is frequently invalidated by DML.
func ruleResultCacheContention() *Rule {
	return &Rule{
		ID:       "result_cache_contention",
		Name:     "Result Cache Latch争用(RC Latch Contention)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "latch: Result Cache: rc latch"},
			{Type: SignalMetric, Key: metricResultCacheLatch},
			{Type: SignalKeyword, Key: "result cache"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "latch: Result Cache: rc latch", Op: OpPctGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Result Cache Latch争用及RESULT_CACHE_MODE设置",
			Query: QueryResultCacheLatch,
			Branches: []Branch{
				{
					Match:    MatchEquals("FORCE"),
					Label:    "RESULT_CACHE_MODE=FORCE,所有查询尝试缓存",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查高DML表是否导致大量缓存失效",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "高DML表导致缓存频繁失效,latch风暴",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RESULT_CACHE_MODE=FORCE且存在高DML活跃表,导致RC latch严重争用,缓存不断失效重建"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即将RESULT_CACHE_MODE改为MANUAL", SkillCommand: "/params result_cache_mode", RawSQL: "ALTER SYSTEM SET result_cache_mode = 'MANUAL' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "仅对低DML静态查询使用/*+ RESULT_CACHE */提示", SkillCommand: "/sql @result_cache_objects", RawSQL: "SELECT id, name, type, invalidations, scan_count FROM V$RESULT_CACHE_OBJECTS WHERE type = 'Result' ORDER BY invalidations DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionInvestigate, Desc: "检查哪些缓存对象失效最频繁", SkillCommand: "/sql @rc_stats", RawSQL: "SELECT * FROM V$RESULT_CACHE_STATISTICS"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "DML不高但FORCE模式仍有latch开销",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RESULT_CACHE_MODE=FORCE,即使DML不频繁也会产生不必要的RC latch开销"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "改为MANUAL模式,仅对明确受益的查询使用Hint", SkillCommand: "/params result_cache_mode", RawSQL: "ALTER SYSTEM SET result_cache_mode = 'MANUAL' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "RESULT_CACHE_MODE=MANUAL,检查是否有过多Hint使用",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查Result Cache命中率和失效率",
						Query: QueryResultCacheLatch,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "失效率>50%,缓存效果差",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Result Cache失效率过高(>50%),缓存对象被DML频繁失效,应移除对应RESULT_CACHE Hint"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "移除高DML表相关SQL的RESULT_CACHE Hint", SkillCommand: "/sql @result_cache_objects", RawSQL: "SELECT id, name, invalidations, scan_count, ROUND(invalidations/NULLIF(scan_count,0)*100,1) inv_pct FROM V$RESULT_CACHE_OBJECTS WHERE type = 'Result' ORDER BY invalidations DESC"},
									{Type: ActionPrevent, Desc: "减小result_cache_max_size限制资源消耗", SkillCommand: "/params result_cache_max_size", RawSQL: "ALTER SYSTEM SET result_cache_max_size = '64M' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "失效率正常,latch争用可能因并发量大",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Result Cache失效率正常但并发查询量大导致latch争用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "减少result_cache_max_size或调整result_cache_max_result", SkillCommand: "/params result_cache_max_size", RawSQL: "ALTER SYSTEM SET result_cache_max_result = 3 SCOPE=BOTH"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"shared_pool_ora4031"},
		Tags:     []string{"memory", "result_cache", "latch"},
		Versions: "11g+",
	}
}

// ruleLargePoolInsufficient detects Large Pool undersizing. RMAN channels
// each consume ~32MB, shared server UGA lives in large_pool. When undersized,
// ORA-4031 occurs in the large pool rather than shared pool.
func ruleLargePoolInsufficient() *Rule {
	return &Rule{
		ID:       "large_pool_insufficient",
		Name:     "Large Pool不足(RMAN/Shared Server/Parallel)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-04031"},
			{Type: SignalMetric, Key: metricLargePoolFreePct},
			{Type: SignalKeyword, Key: "large pool"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricLargePoolFreePct, Op: OpLT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Large Pool使用率和消费者",
			Query: QueryLargePoolStats,
			Branches: []Branch{
				{
					Match:    MatchLT(5),
					Label:    "Large Pool空闲<5%,极度紧张",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "判断主要消费者:RMAN channels / Shared Server / Parallel Query",
						Query: QueryLargePoolStats,
						Branches: []Branch{
							{
								Match:    MatchEquals("RMAN"),
								Label:    "RMAN备份通道消耗大量Large Pool(每通道~32MB)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Large Pool空闲<5%,主要被RMAN备份通道占用,每个通道约消耗32MB"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大large_pool_size = RMAN通道数 × 32MB + 余量", SkillCommand: "/params large_pool_size", RawSQL: "ALTER SYSTEM SET large_pool_size = '{calculated_size}' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "减少RMAN并行通道数或错峰备份", SkillCommand: "/sql @rman_config", RawSQL: "SELECT conf#, name, value FROM V$RMAN_CONFIGURATION WHERE name LIKE '%PARALLELISM%'"},
									{Type: ActionInvestigate, Desc: "查看Large Pool内存分布", SkillCommand: "/sql @large_pool_breakdown", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'large pool' ORDER BY bytes DESC"},
								},
							},
							{
								Match:    MatchEquals("SHARED_SERVER"),
								Label:    "Shared Server UGA占用大量Large Pool",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Shared Server模式下UGA分配在Large Pool,当前连接数导致Large Pool耗尽"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大large_pool_size或评估是否需要Shared Server", SkillCommand: "/params large_pool_size", RawSQL: "ALTER SYSTEM SET large_pool_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionPrevent, Desc: "评估切换为Dedicated Server模式", SkillCommand: "/params shared_servers", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('shared_servers','dispatchers','large_pool_size')"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Parallel Query或其他组件消耗Large Pool",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Large Pool被Parallel Query Slave或I/O缓冲占用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大large_pool_size适配并行度", SkillCommand: "/params large_pool_size", RawSQL: "ALTER SYSTEM SET large_pool_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查Large Pool各组件占用", SkillCommand: "/sql @large_pool_detail", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'large pool' ORDER BY bytes DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(15),
					Label:    "Large Pool空闲5-15%,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Large Pool空闲比例偏低(5-15%),在RMAN备份或并行操作高峰可能ORA-4031"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查Large Pool使用分布", SkillCommand: "/sql @large_pool_stats", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'large pool' ORDER BY bytes DESC"},
						{Type: ActionPrevent, Desc: "预防性增大large_pool_size", SkillCommand: "/params large_pool_size", RawSQL: "ALTER SYSTEM SET large_pool_size = '{new_size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Large Pool空闲充足",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Large Pool空闲内存充足"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"memory", "large_pool", "rman", "shared_server"},
		Versions: "9i+",
	}
}

// ruleJavaPoolInsufficient detects Java Pool undersizing. OJVM and APEX
// components require Java Pool; the minimum should be 48MB when OJVM is
// installed. Symptoms include ORA-29532 and Java OutOfMemory in alert log.
func ruleJavaPoolInsufficient() *Rule {
	return &Rule{
		ID:       "java_pool_insufficient",
		Name:     "Java Pool不足(OJVM/APEX内存问题)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-29532"},
			{Type: SignalMetric, Key: metricJavaPoolUsedPct},
			{Type: SignalKeyword, Key: "java pool"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricJavaPoolUsedPct, Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Java Pool使用率和OJVM/APEX组件状态",
			Query: QueryJavaPoolStats,
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "Java Pool使用率>90%,接近耗尽",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否安装了OJVM组件",
						Query: QueryJavaPoolStats,
						Branches: []Branch{
							{
								Match:    MatchBool(true),
								Label:    "OJVM已安装,Java Pool需求较大",
								Severity: SeverityHigh,
								Then: &TreeNode{
									Step:  "检查Java Pool当前大小是否低于48MB最低建议值",
									Query: QueryJavaPoolStats,
									Branches: []Branch{
										{
											Match:    MatchLT(48),
											Label:    "Java Pool < 48MB,低于OJVM最低建议值",
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "OJVM已安装但Java Pool低于48MB最低建议值,Java存储过程和APEX可能频繁ORA-29532"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "增大java_pool_size至少到48MB", SkillCommand: "/params java_pool_size", RawSQL: "ALTER SYSTEM SET java_pool_size = '128M' SCOPE=BOTH"},
												{Type: ActionInvestigate, Desc: "检查Java Pool使用分布", SkillCommand: "/sql @java_pool_detail", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'java pool' ORDER BY bytes DESC"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "Java Pool >= 48MB但仍接近耗尽",
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "Java Pool已达最低标准但使用率>90%,APEX或Java应用负载较高,需按实际使用量增大"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "根据当前使用量增大java_pool_size", SkillCommand: "/params java_pool_size", RawSQL: "ALTER SYSTEM SET java_pool_size = '{new_size}' SCOPE=BOTH"},
												{Type: ActionInvestigate, Desc: "查看Java会话数和内存消耗", SkillCommand: "/sql @java_sessions", RawSQL: "SELECT s.sid, s.program, st.value/1024/1024 java_mb FROM V$SESSION s JOIN V$SESSTAT st ON s.sid = st.sid JOIN V$STATNAME sn ON st.statistic# = sn.statistic# WHERE sn.name = 'java call heap used size' AND st.value > 0 ORDER BY st.value DESC"},
											},
										},
									},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "OJVM未安装,Java Pool使用异常",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "OJVM未安装但Java Pool使用率高,可能有隐式Java依赖或配置异常"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查哪些组件在使用Java Pool", SkillCommand: "/sql @java_pool_consumers", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'java pool' ORDER BY bytes DESC"},
									{Type: ActionFix, Desc: "若不需要Java Pool可设为最小值", SkillCommand: "/params java_pool_size", RawSQL: "ALTER SYSTEM SET java_pool_size = '16M' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(70),
					Label:    "Java Pool使用率70-90%,需监控",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Java Pool使用率偏高(70-90%),在APEX或Java批量操作高峰可能不足"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "监控Java Pool使用趋势并预防性增大", SkillCommand: "/params java_pool_size", RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) MB FROM V$SGASTAT WHERE pool = 'java pool' ORDER BY bytes DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Java Pool使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Java Pool使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"memory", "java_pool", "ojvm", "apex"},
		Versions: "10g+",
	}
}

// ruleHugePagesNotConfigured detects environments where SGA > 8GB on Linux
// but HugePages is not configured. When using AMM (memory_target > 0),
// HugePages cannot be used — so this also checks for AMM and recommends ASMM.
func ruleHugePagesNotConfigured() *Rule {
	return &Rule{
		ID:       "hugepages_not_configured",
		Name:     "HugePages未配置(大SGA性能隐患)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricHugePagesConfigured},
			{Type: SignalMetric, Key: metricSGASizeGB},
			{Type: SignalKeyword, Key: "hugepages"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSGASizeGB, Op: OpGT, Value: 8},
			},
		},
		Tree: &TreeNode{
			Step:  "检查SGA大小和HugePages配置",
			Query: QueryHugePagesInfo,
			Branches: []Branch{
				{
					Match:    MatchBool(false),
					Label:    "HugePages未配置",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否使用AMM(memory_target > 0)",
						Query: QueryMemoryModelInfo,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "正在使用AMM(memory_target > 0),AMM与HugePages不兼容",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "SGA > 8GB 且使用AMM模式(memory_target > 0),AMM不支持HugePages,导致大量TLB miss,性能严重下降"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "切换为ASMM模式:设置sga_target,清除memory_target", SkillCommand: "/params memory_target", RawSQL: "ALTER SYSTEM SET memory_target = 0 SCOPE=SPFILE;\nALTER SYSTEM SET sga_target = '{sga_size}' SCOPE=SPFILE;\nALTER SYSTEM SET pga_aggregate_target = '{pga_size}' SCOPE=SPFILE;"},
									{Type: ActionFix, Desc: "配置Linux HugePages后重启实例", SkillCommand: "/sql @hugepages_calc", RawSQL: "-- 计算所需HugePages数:\n-- hugepages = ceil(SGA_SIZE / 2MB) + margin\n-- echo 'vm.nr_hugepages = {number}' >> /etc/sysctl.conf\n-- sysctl -p\nSELECT 'Required HugePages' AS item, CEIL(SUM(value)/1024/1024/2) + 10 AS hugepages FROM V$SGA"},
									{Type: ActionPrevent, Desc: "在/etc/security/limits.conf设置memlock", SkillCommand: "/sql @check_hugepages", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('memory_target','sga_target','sga_max_size','use_large_pages')"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用ASMM但未启用HugePages",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SGA > 8GB 使用ASMM模式但HugePages未配置,大量常规页会导致page table膨胀和TLB miss"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置HugePages并设置use_large_pages=ONLY", SkillCommand: "/params use_large_pages", RawSQL: "ALTER SYSTEM SET use_large_pages = 'ONLY' SCOPE=SPFILE;\n-- Linux: echo 'vm.nr_hugepages = {number}' >> /etc/sysctl.conf && sysctl -p"},
									{Type: ActionInvestigate, Desc: "计算所需HugePages数量", SkillCommand: "/sql @hugepages_calc", RawSQL: "SELECT 'Required HugePages' AS item, CEIL(SUM(value)/1024/1024/2) + 10 AS hugepages FROM V$SGA"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "HugePages已配置",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "验证HugePages分配是否充足",
						Query: QueryHugePagesInfo,
						Branches: []Branch{
							{
								Match:    MatchLT(90),
								Label:    "HugePages使用率<90%,分配充足",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "HugePages已配置且分配充足"}},
								Actions:  nil,
							},
							{
								Match:    MatchDefault(),
								Label:    "HugePages接近耗尽(>90%使用率)",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "HugePages使用率>90%,SGA增长后可能回退到常规页"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "增加HugePages预留数量", SkillCommand: "/sql @hugepages_check", RawSQL: "SELECT 'HugePages Usage' AS item, ROUND(hp_used/NULLIF(hp_total,0)*100,1) AS pct FROM (SELECT value hp_total FROM V$OSSTAT WHERE stat_name = 'NUM_PAGES_TOTAL') t, (SELECT value hp_used FROM V$OSSTAT WHERE stat_name = 'NUM_PAGES_USED') u"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{"amm_vs_asmm"},
		CausesOf: []string{},
		Tags:     []string{"memory", "hugepages", "linux", "tlb"},
		Versions: "11g+",
		Related:  []string{"amm_vs_asmm"},
	}
}

// ruleAMMvsASMM detects Automatic Memory Management (memory_target > 0) on
// production systems. AMM disables HugePages, causes /dev/shm contention,
// and makes memory allocation unpredictable. ASMM (sga_target + pga_aggregate_target)
// is strongly recommended for production.
func ruleAMMvsASMM() *Rule {
	return &Rule{
		ID:       "amm_vs_asmm",
		Name:     "AMM模式不适合生产(推荐ASMM)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricMemoryTarget},
			{Type: SignalMetric, Key: metricAMMEnabled},
			{Type: SignalKeyword, Key: "AMM"},
			{Type: SignalKeyword, Key: "memory_target"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricMemoryTarget, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "确认memory_target > 0(AMM模式)",
			Query: QueryMemoryModelInfo,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "确认AMM模式启用(memory_target > 0)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查SGA大小判断影响程度",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricSGASizeGB) },
						Branches: []Branch{
							{
								Match:    MatchGT(8),
								Label:    "SGA > 8GB,AMM问题严重(HugePages不可用+/dev/shm压力)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "生产环境使用AMM(memory_target > 0),SGA > 8GB,导致:1)无法使用HugePages 2)/dev/shm可能不足 3)SGA/PGA边界不可控"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "切换为ASMM模式:清除memory_target,设置sga_target和pga_aggregate_target", SkillCommand: "/params memory_target", RawSQL: "-- 需要重启实例生效:\nALTER SYSTEM SET memory_target = 0 SCOPE=SPFILE;\nALTER SYSTEM SET memory_max_target = 0 SCOPE=SPFILE;\nALTER SYSTEM SET sga_target = '{current_sga}' SCOPE=SPFILE;\nALTER SYSTEM SET pga_aggregate_target = '{current_pga}' SCOPE=SPFILE;"},
									{Type: ActionFix, Desc: "重启前配置HugePages", SkillCommand: "/sql @hugepages_calc", RawSQL: "SELECT 'Current Memory Config' AS item, name, value FROM V$PARAMETER WHERE name IN ('memory_target','memory_max_target','sga_target','sga_max_size','pga_aggregate_target')"},
								},
							},
							{
								Match:    MatchGT(2),
								Label:    "SGA 2-8GB,AMM仍不推荐用于生产",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "生产环境使用AMM模式,SGA 2-8GB,虽影响比大SGA小但仍建议切换ASMM以获得可预测的内存分配"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "计划切换为ASMM模式", SkillCommand: "/params memory_target", RawSQL: "ALTER SYSTEM SET memory_target = 0 SCOPE=SPFILE;\nALTER SYSTEM SET sga_target = '{sga}' SCOPE=SPFILE;\nALTER SYSTEM SET pga_aggregate_target = '{pga}' SCOPE=SPFILE;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "SGA < 2GB,AMM影响较小(开发/测试可接受)",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "AMM模式启用但SGA较小(<2GB),对开发测试环境影响有限"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "生产上线前务必切换为ASMM", SkillCommand: "/params memory_target", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('memory_target','sga_target','pga_aggregate_target')"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未使用AMM(memory_target = 0),正常ASMM模式",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前使用ASMM模式(memory_target = 0),配置正确"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"hugepages_not_configured", "sga_component_imbalance"},
		Tags:     []string{"memory", "amm", "asmm", "hugepages"},
		Versions: "11g+",
		Related:  []string{"hugepages_not_configured"},
	}
}

// ruleSGAAutoResize detects SGA component resize thrashing via v$sga_resize_ops.
// When ASMM frequently flips components (e.g., shared pool steals from buffer cache
// and vice versa), it indicates the total SGA is undersized or component minimums
// are not set. This causes latency spikes during resize operations.
func ruleSGAAutoResize() *Rule {
	return &Rule{
		ID:       "sga_auto_resize_thrash",
		Name:     "SGA组件Resize抖动(频繁自动调整)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricSGAResizeFreq},
			{Type: SignalKeyword, Key: "SGA resize"},
			{Type: SignalKeyword, Key: "resize thrashing"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSGAResizeFreq, Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "查询V$SGA_RESIZE_OPS最近24小时的resize操作",
			Query: QuerySGAResizeOps,
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    "24h内resize > 20次,严重抖动",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "识别抖动最严重的组件对",
						Query: QuerySGAResizeOps,
						Branches: []Branch{
							{
								Match:    MatchEquals("shared_pool_vs_buffer_cache"),
								Label:    "Shared Pool与Buffer Cache互相抢夺",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Shared Pool和Buffer Cache频繁互相resize(>20次/24h),SGA总量不足以同时满足两者,resize期间造成延迟尖峰"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "设置两个组件的最小值防止互抢", SkillCommand: "/params db_cache_size", RawSQL: "-- 设置组件最小值(当前大小的80%作为底线):\nALTER SYSTEM SET db_cache_size = '{min_cache}' SCOPE=BOTH;\nALTER SYSTEM SET shared_pool_size = '{min_sp}' SCOPE=BOTH;"},
									{Type: ActionFix, Desc: "增大sga_target使两者都有足够空间", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET sga_target = '{new_target}' SCOPE=BOTH;\nALTER SYSTEM SET sga_max_size = '{new_max}' SCOPE=SPFILE;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他组件间抖动",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SGA组件频繁自动调整(>20次/24h),需设置各组件最小值稳定分配"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "为所有主要SGA组件设置最小值", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET db_cache_size = '{min}' SCOPE=BOTH;\nALTER SYSTEM SET shared_pool_size = '{min}' SCOPE=BOTH;\nALTER SYSTEM SET large_pool_size = '{min}' SCOPE=BOTH;\nALTER SYSTEM SET java_pool_size = '{min}' SCOPE=BOTH;"},
									{Type: ActionInvestigate, Desc: "查看resize历史确定抖动模式", SkillCommand: "/sql @sga_resize_history", RawSQL: "SELECT component, oper_type, oper_mode, initial_size/1024/1024 init_mb, final_size/1024/1024 final_mb, start_time, end_time FROM V$SGA_RESIZE_OPS WHERE start_time > SYSDATE - 1 ORDER BY start_time DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(10),
					Label:    "24h内resize 10-20次,轻度抖动",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "SGA组件24h内resize 10-20次,存在轻度自动调整抖动"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "设置关键组件最小值防止抖动", SkillCommand: "/params db_cache_size", RawSQL: "-- 查看resize历史:\nSELECT component, oper_type, COUNT(*) ops FROM V$SGA_RESIZE_OPS WHERE start_time > SYSDATE - 1 GROUP BY component, oper_type ORDER BY ops DESC"},
						{Type: ActionPrevent, Desc: "考虑增大sga_target", SkillCommand: "/params sga_target", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('sga_target','sga_max_size','db_cache_size','shared_pool_size','large_pool_size')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "SGA resize频率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SGA组件resize频率在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"amm_vs_asmm"},
		CausesOf: []string{"buffer_cache_hit_low", "shared_pool_ora4031"},
		Tags:     []string{"memory", "sga", "resize", "asmm"},
		Versions: "10g+",
		Related:  []string{"sga_component_imbalance"},
	}
}

// ─── Deep IO/Storage Rules (7) ───────────────────────────────────────────────

func deepIOStorageRules() []*Rule {
	return []*Rule{
		ruleASMDiskFailure(),
		ruleASMRebalanceImpact(),
		ruleDatafileAutoextendOff(),
		ruleDatafileMaxsizeReached(),
		ruleTableCompressionAdvice(),
		ruleHWMWaste(),
		ruleFilesystemIOOptions(),
	}
}

// ruleASMDiskFailure detects ASM disks in MISSING or DISMOUNTING state.
// A MISSING disk in NORMAL redundancy means data loss risk; in HIGH redundancy
// there is tolerance for one disk but immediate replacement is needed.
func ruleASMDiskFailure() *Rule {
	return &Rule{
		ID:       "asm_disk_failure",
		Name:     "ASM磁盘故障(Disk MISSING/DISMOUNT)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMDiskStatus},
			{Type: SignalKeyword, Key: "ASM disk"},
			{Type: SignalKeyword, Key: "disk failure"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMDiskStatus, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "查询V$ASM_DISK状态,检查MISSING/DISMOUNTING磁盘",
			Query: QueryASMDiskStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现异常状态的ASM磁盘",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查磁盘组冗余级别评估数据丢失风险",
						Query: QueryASMDiskStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("EXTERNAL"),
								Label:    "EXTERNAL冗余(无镜像),磁盘故障=数据丢失",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM磁盘组使用EXTERNAL冗余(无镜像)且存在MISSING/DISMOUNTING磁盘,数据丢失风险极高"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "紧急:确认底层存储状态,可能需要从备份恢复", SkillCommand: "/sql @asm_disk_status", RawSQL: "SELECT dg.name dg_name, dg.type redundancy, d.name disk_name, d.path, d.mode_status, d.state, d.header_status FROM V$ASM_DISKGROUP dg JOIN V$ASM_DISK d ON dg.group_number = d.group_number WHERE d.mode_status != 'ONLINE' OR d.state != 'NORMAL'"},
									{Type: ActionUrgent, Desc: "立即检查RMAN备份可用性", SkillCommand: "/sql @rman_backup_status", RawSQL: "SELECT * FROM V$RMAN_BACKUP_JOB_DETAILS WHERE status = 'COMPLETED' ORDER BY end_time DESC FETCH FIRST 5 ROWS ONLY"},
								},
							},
							{
								Match:    MatchEquals("NORMAL"),
								Label:    "NORMAL冗余(单镜像),可容忍一块盘故障",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM NORMAL冗余磁盘组有磁盘MISSING,单镜像仅容忍一块盘故障,需立即更换"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "更换故障磁盘并触发rebalance", SkillCommand: "/sql @asm_disk_replace", RawSQL: "-- 1. 添加新磁盘:\nALTER DISKGROUP {dg_name} ADD DISK '{new_disk_path}';\n-- 2. 删除故障磁盘(如果仍可见):\nALTER DISKGROUP {dg_name} DROP DISK {failed_disk_name};"},
									{Type: ActionInvestigate, Desc: "检查同一failure group是否还有其他风险磁盘", SkillCommand: "/sql @asm_failure_groups", RawSQL: "SELECT d.group_number, d.failgroup, d.name, d.mode_status, d.state FROM V$ASM_DISK d WHERE d.group_number IN (SELECT group_number FROM V$ASM_DISK WHERE mode_status != 'ONLINE') ORDER BY d.failgroup, d.name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "HIGH冗余(双镜像),仍有容错能力",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ASM HIGH冗余磁盘组有磁盘故障,双镜像仍有容错但应尽快更换"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "更换故障磁盘触发rebalance恢复冗余", SkillCommand: "/sql @asm_disk_replace", RawSQL: "ALTER DISKGROUP {dg_name} ADD DISK '{new_disk_path}';\nALTER DISKGROUP {dg_name} DROP DISK {failed_disk_name};"},
									{Type: ActionInvestigate, Desc: "确认磁盘组rebalance状态", SkillCommand: "/sql @asm_rebalance", RawSQL: "SELECT group_number, operation, state, power, est_minutes FROM V$ASM_OPERATION WHERE operation = 'REBAL'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有ASM磁盘状态正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有ASM磁盘ONLINE/NORMAL状态"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "asm", "disk_failure", "data_loss"},
		Versions: "10g+",
	}
}

// ruleASMRebalanceImpact detects ASM rebalance operations running during
// business hours with high power settings. Rebalance causes significant I/O
// overhead and should be run during maintenance windows or at low power.
func ruleASMRebalanceImpact() *Rule {
	return &Rule{
		ID:       "asm_rebalance_impact",
		Name:     "ASM Rebalance影响业务(高Power/业务高峰)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMRebalanceActive},
			{Type: SignalKeyword, Key: "ASM rebalance"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMRebalanceActive, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前ASM rebalance操作及power设置",
			Query: QueryASMRebalanceInfo,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在活跃的rebalance操作",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查rebalance power级别",
						Query: QueryASMRebalanceInfo,
						Branches: []Branch{
							{
								Match:    MatchGT(5),
								Label:    "Rebalance power > 5,I/O影响大",
								Severity: SeverityHigh,
								Then: &TreeNode{
									Step:  "检查当前是否为业务高峰(active sessions > baseline × 1.5)",
									Check: func(ctx *EvalContext) interface{} {
										active := ctx.MetricValue(metricActive)
										baseline := ctx.BaselineActive
										if baseline == 0 {
											return 0.0
										}
										return active / baseline
									},
									Branches: []Branch{
										{
											Match:    MatchGT(1.5),
											Label:    "业务高峰期,rebalance严重影响性能",
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "ASM rebalance在业务高峰期以高power(>5)运行,大量I/O资源被rebalance占用,导致业务SQL响应时间增加"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "立即降低rebalance power", SkillCommand: "/sql @asm_rebal_power", RawSQL: "ALTER DISKGROUP {dg_name} REBALANCE POWER 1"},
												{Type: ActionFix, Desc: "等待业务低谷再提高power完成rebalance", SkillCommand: "/sql @asm_rebalance_status", RawSQL: "SELECT group_number, operation, state, power, est_minutes FROM V$ASM_OPERATION WHERE operation = 'REBAL'"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "非高峰期但power较高,可接受",
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "ASM rebalance正在运行,power > 5,当前非高峰期影响可控"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "监控rebalance进度和I/O影响", SkillCommand: "/sql @asm_rebalance_status", RawSQL: "SELECT group_number, operation, state, power, est_minutes, actual FROM V$ASM_OPERATION WHERE operation = 'REBAL'"},
											},
										},
									},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Rebalance power <= 5,影响较小",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "ASM rebalance运行中,power <= 5,I/O影响较小"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控rebalance进度", SkillCommand: "/sql @asm_rebalance_progress", RawSQL: "SELECT group_number, operation, state, power, est_minutes FROM V$ASM_OPERATION"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无活跃rebalance操作",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无ASM rebalance操作运行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"asm_disk_failure"},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "asm", "rebalance", "io_contention"},
		Versions: "10g+",
	}
}

// ruleDatafileAutoextendOff detects datafiles with autoextend disabled on
// tablespaces that are growing. When autoextend is OFF, the tablespace will
// hit 100% and cause ORA-01653/ORA-01688 errors.
func ruleDatafileAutoextendOff() *Rule {
	return &Rule{
		ID:       "datafile_autoextend_off",
		Name:     "数据文件自动扩展已关闭(成长表空间风险)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDatafileAutoextend},
			{Type: SignalKeyword, Key: "autoextend"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDatafileAutoextend, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查autoextend关闭的数据文件及所属表空间使用率",
			Query: QueryDatafileAutoextend,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在autoextend关闭的数据文件",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查对应表空间使用率",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricTablespaceUsedPct) },
						Branches: []Branch{
							{
								Match:    MatchGT(85),
								Label:    "表空间使用率>85%且autoextend关闭,即将满",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "表空间使用率>85%但数据文件autoextend已关闭,将很快因空间不足导致ORA-01653/ORA-01688错误"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启用autoextend或添加数据文件", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, file_name, bytes/1024/1024 size_mb, autoextensible FROM DBA_DATA_FILES WHERE autoextensible = 'NO' ORDER BY tablespace_name"},
									{Type: ActionFix, Desc: "启用autoextend并设置合理maxsize", SkillCommand: "/sql @enable_autoextend", RawSQL: "ALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
								},
							},
							{
								Match:    MatchGT(60),
								Label:    "表空间使用率60-85%,预警",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "表空间使用率60-85%且autoextend关闭,按当前增长速度将在可预见时间内满"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "启用autoextend预防空间告急", SkillCommand: "/sql @enable_autoextend", RawSQL: "ALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
									{Type: ActionPrevent, Desc: "检查增长趋势规划容量", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, file_name, bytes/1024/1024 size_mb, maxbytes/1024/1024 max_mb, autoextensible FROM DBA_DATA_FILES WHERE autoextensible = 'NO'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "表空间使用率<60%,暂时安全",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "数据文件autoextend关闭但当前表空间使用率较低,暂无风险但建议启用autoextend"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "建议启用autoextend作为安全措施", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, file_name, autoextensible FROM DBA_DATA_FILES WHERE autoextensible = 'NO'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有数据文件已启用autoextend",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有数据文件均已启用autoextend"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full"},
		Tags:     []string{"io_storage", "datafile", "autoextend", "space"},
		Versions: "9i+",
	}
}

// ruleDatafileMaxsizeReached detects datafiles that have reached their MAXSIZE
// limit and cannot grow further. This is a critical issue that will cause
// immediate space allocation failures.
func ruleDatafileMaxsizeReached() *Rule {
	return &Rule{
		ID:       "datafile_maxsize_reached",
		Name:     "数据文件已达MAXSIZE上限(无法扩展)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDatafileMaxsizeHit},
			{Type: SignalErrorCode, Key: "ORA-01688"},
			{Type: SignalKeyword, Key: "maxsize"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDatafileMaxsizeHit, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "查询已达MAXSIZE的数据文件",
			Query: QueryDatafileMaxsize,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在已达MAXSIZE的数据文件",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查表空间使用率确认紧急程度",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricTablespaceUsedPct) },
						Branches: []Branch{
							{
								Match:    MatchGT(95),
								Label:    "表空间>95%且数据文件已达MAXSIZE,紧急",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "数据文件已达MAXSIZE且表空间使用率>95%,新数据无法写入,将立即报错ORA-01688"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即增大MAXSIZE或添加新数据文件", SkillCommand: "/space", RawSQL: "ALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON MAXSIZE UNLIMITED;\n-- 或添加新数据文件:\nALTER TABLESPACE {ts_name} ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
									{Type: ActionInvestigate, Desc: "检查大段对象是否可清理或归档", SkillCommand: "/sql @large_segments", RawSQL: "SELECT owner, segment_name, segment_type, ROUND(bytes/1024/1024/1024,2) GB FROM DBA_SEGMENTS WHERE tablespace_name = '{ts_name}' ORDER BY bytes DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchGT(80),
								Label:    "表空间80-95%且接近MAXSIZE",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据文件接近或已达MAXSIZE,表空间使用率80-95%,需尽快扩容"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大MAXSIZE或添加新数据文件", SkillCommand: "/sql @resize_datafile", RawSQL: "ALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON MAXSIZE 64G"},
									{Type: ActionPrevent, Desc: "检查32G单文件大小限制(db_block_size相关)", SkillCommand: "/sql @datafile_limits", RawSQL: "SELECT file_name, bytes/1024/1024/1024 size_gb, maxbytes/1024/1024/1024 maxsize_gb, autoextensible FROM DBA_DATA_FILES WHERE tablespace_name = '{ts_name}' ORDER BY bytes DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "表空间使用率<80%但已有文件达MAXSIZE",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "某些数据文件已达MAXSIZE但表空间总体使用率尚可,应预防性扩容"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "增大受限数据文件的MAXSIZE", SkillCommand: "/space", RawSQL: "SELECT file_name, bytes/1024/1024/1024 size_gb, maxbytes/1024/1024/1024 max_gb FROM DBA_DATA_FILES WHERE bytes >= maxbytes * 0.95 AND autoextensible = 'YES'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无数据文件达到MAXSIZE",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有数据文件未达到MAXSIZE上限"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full"},
		Tags:     []string{"io_storage", "datafile", "maxsize", "space"},
		Versions: "9i+",
	}
}

// ruleTableCompressionAdvice detects large tables (>10GB) without compression
// when CPU usage is low (<50%). Table compression can reduce storage by 2-4x
// and improve query performance by reducing I/O.
func ruleTableCompressionAdvice() *Rule {
	return &Rule{
		ID:       "table_compression_advice",
		Name:     "大表未压缩(存储与I/O优化建议)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricLargeTableNoCompress},
			{Type: SignalKeyword, Key: "compression"},
			{Type: SignalKeyword, Key: "large table"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricLargeTableNoCompress, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "查询>10GB未压缩的表并检查CPU使用率",
			Query: QueryLargeTableCompress,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在>10GB未压缩的表",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查CPU使用率判断是否有余量做压缩",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricCPUUsedPct) },
						Branches: []Branch{
							{
								Match:    MatchLT(50),
								Label:    "CPU < 50%,有充足余量启用压缩",
								Severity: SeverityMedium,
								Then: &TreeNode{
									Step:  "检查表的访问模式(DML频率)确定压缩类型",
									Query: QueryLargeTableCompress,
									Branches: []Branch{
										{
											Match:    MatchLT(10),
											Label:    "DML很少,适合HCC(Hybrid Columnar Compression)或OLTP压缩",
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "发现>10GB未压缩表,DML极少,CPU余量充足(<50%),启用压缩可节省2-4x存储并减少I/O"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "对归档型大表启用HCC压缩(Exadata)或BASIC压缩", SkillCommand: "/sql @compress_table", RawSQL: "ALTER TABLE {schema}.{table} MOVE COMPRESS FOR OLTP;\n-- Exadata可用:\n-- ALTER TABLE {schema}.{table} MOVE COMPRESS FOR QUERY HIGH;\n-- 压缩后重建索引:\n-- ALTER INDEX {schema}.{idx} REBUILD ONLINE;"},
												{Type: ActionInvestigate, Desc: "使用压缩顾问评估压缩比", SkillCommand: "/sql @compress_advisor", RawSQL: "DECLARE l_blkcnt PLS_INTEGER; l_rowcnt PLS_INTEGER; l_cmp_ratio NUMBER; l_cmp_type VARCHAR2(200); BEGIN DBMS_COMPRESSION.GET_COMPRESSION_RATIO('{schema}','{table}','',DBMS_COMPRESSION.COMP_FOR_OLTP,l_blkcnt,l_rowcnt,l_cmp_ratio,l_cmp_type); DBMS_OUTPUT.PUT_LINE('Ratio: '||l_cmp_ratio||' Type: '||l_cmp_type); END;"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "有DML活动,使用OLTP压缩(Advanced Compression)",
											Severity: SeverityLow,
											Findings: []Finding{
												{Desc: "发现>10GB未压缩表,存在DML活动,建议使用OLTP压缩(需Advanced Compression License)"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "启用OLTP压缩(DML友好)", SkillCommand: "/sql @compress_oltp", RawSQL: "ALTER TABLE {schema}.{table} MOVE COMPRESS FOR OLTP ONLINE;\n-- 12c+支持ONLINE MOVE,无需停机"},
												{Type: ActionPrevent, Desc: "评估Advanced Compression License需求", SkillCommand: "/sql @check_compression", RawSQL: "SELECT owner, table_name, compression, compress_for, ROUND(bytes/1024/1024/1024,2) GB FROM DBA_SEGMENTS s JOIN DBA_TABLES t ON s.owner = t.owner AND s.segment_name = t.table_name WHERE s.bytes > 10*1024*1024*1024 ORDER BY s.bytes DESC"},
											},
										},
									},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "CPU >= 50%,压缩可能增加CPU负担",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "存在大表未压缩但当前CPU使用率>=50%,压缩会增加CPU开销,建议在低峰期评估"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "在低峰期评估压缩收益", SkillCommand: "/sql @large_uncompressed", RawSQL: "SELECT t.owner, t.table_name, t.compression, ROUND(s.bytes/1024/1024/1024,2) GB FROM DBA_TABLES t JOIN DBA_SEGMENTS s ON t.owner = s.owner AND t.table_name = s.segment_name WHERE s.bytes > 10*1024*1024*1024 AND t.compression = 'DISABLED' ORDER BY s.bytes DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无>10GB未压缩的大表",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有>10GB大表已启用压缩或无此类大表"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full"},
		Tags:     []string{"io_storage", "compression", "space", "performance"},
		Versions: "11g+",
	}
}

// ruleHWMWaste detects tables with High Water Mark (HWM) waste — where
// actual data occupies <30% of allocated blocks after mass DELETE operations.
// The HWM is not reclaimed by DELETE; only MOVE/SHRINK resets it, causing
// full table scans to read mostly empty blocks.
func ruleHWMWaste() *Rule {
	return &Rule{
		ID:       "hwm_waste",
		Name:     "高水位浪费(HWM未回收,大量空块)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricHWMWastePct},
			{Type: SignalKeyword, Key: "high water mark"},
			{Type: SignalKeyword, Key: "HWM"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricHWMWastePct, Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step:  "检查表的实际数据占分配块比例(data_pct)",
			Query: QueryHWMWaste,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现HWM浪费严重的表(data_pct < 30%)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查表大小评估回收收益",
						Query: QueryHWMWaste,
						Branches: []Branch{
							{
								Match:    MatchGT(1024),
								Label:    "浪费空间 > 1GB,收益显著",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "表HWM以下实际数据占比<30%,浪费空间>1GB,大量空块导致全扫描效率极低,DELETE后HWM未回收"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用ALTER TABLE SHRINK SPACE回收(需启用row movement)", SkillCommand: "/sql @shrink_table", RawSQL: "ALTER TABLE {schema}.{table} ENABLE ROW MOVEMENT;\nALTER TABLE {schema}.{table} SHRINK SPACE CASCADE;\nALTER TABLE {schema}.{table} DISABLE ROW MOVEMENT;"},
									{Type: ActionFix, Desc: "或使用MOVE ONLINE重组(12c+)", SkillCommand: "/sql @move_table", RawSQL: "ALTER TABLE {schema}.{table} MOVE ONLINE;\n-- MOVE后重建索引:\n-- ALTER INDEX {schema}.{idx} REBUILD ONLINE;"},
									{Type: ActionInvestigate, Desc: "检查具体表的空间浪费统计", SkillCommand: "/sql @space_advisor", RawSQL: "SELECT table_name, ROUND(blocks * 8 / 1024, 1) alloc_mb, ROUND(num_rows * avg_row_len / 1024 / 1024, 1) data_mb, ROUND(num_rows * avg_row_len / NULLIF(blocks * 8192, 0) * 100, 1) data_pct FROM DBA_TABLES WHERE owner = '{schema}' AND blocks > 1000 AND num_rows * avg_row_len / NULLIF(blocks * 8192, 0) < 0.3 ORDER BY blocks DESC"},
								},
							},
							{
								Match:    MatchGT(100),
								Label:    "浪费空间100MB-1GB",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "表存在HWM浪费(100MB-1GB),批量DELETE后未回收空间"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用SHRINK SPACE回收碎片", SkillCommand: "/sql @shrink_table", RawSQL: "ALTER TABLE {schema}.{table} ENABLE ROW MOVEMENT;\nALTER TABLE {schema}.{table} SHRINK SPACE;\nALTER TABLE {schema}.{table} DISABLE ROW MOVEMENT;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "浪费空间<100MB,影响较小",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "表存在HWM浪费但量级较小(<100MB)"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "后续批量DELETE后记得SHRINK或MOVE", SkillCommand: "/sql @hwm_check", RawSQL: "SELECT owner, table_name, blocks, empty_blocks, avg_space FROM DBA_TABLES WHERE blocks > 100 AND num_rows * avg_row_len / NULLIF(blocks * 8192, 0) < 0.3 ORDER BY blocks DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未检测到严重HWM浪费",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "表的空间利用率正常,无明显HWM浪费"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"full_table_scan_oltp"},
		Tags:     []string{"io_storage", "hwm", "space", "fragmentation"},
		Versions: "10g+",
	}
}

// ruleFilesystemIOOptions detects filesystemio_options not set to SETALL on
// filesystem-based storage. SETALL enables both directIO and asyncIO,
// eliminating OS cache double-buffering and enabling asynchronous I/O for
// Oracle datafiles on filesystems.
func ruleFilesystemIOOptions() *Rule {
	return &Rule{
		ID:       "filesystemio_options",
		Name:     "filesystemio_options未设SETALL(I/O性能隐患)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFilesystemIOOptions},
			{Type: SignalKeyword, Key: "filesystemio"},
			{Type: SignalKeyword, Key: "directio"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFilesystemIOOptions, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查filesystemio_options参数设置",
			Query: QueryFilesystemIOOptions,
			Branches: []Branch{
				{
					Match:    MatchEquals("SETALL"),
					Label:    "filesystemio_options = SETALL,配置正确",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "filesystemio_options已设为SETALL,directIO和asyncIO均已启用"}},
					Actions:  nil,
				},
				{
					Match:    MatchEquals("NONE"),
					Label:    "filesystemio_options = NONE,未启用任何优化",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "确认是否使用文件系统存储(非ASM)",
						Query: QueryFilesystemIOOptions,
						Branches: []Branch{
							{
								Match:    MatchBool(true),
								Label:    "使用文件系统存储,需要设置SETALL",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "使用文件系统存储但filesystemio_options=NONE,未启用directIO导致OS double buffering,未启用asyncIO导致I/O序列化"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "设置filesystemio_options=SETALL(需重启)", SkillCommand: "/params filesystemio_options", RawSQL: "ALTER SYSTEM SET filesystemio_options = 'SETALL' SCOPE=SPFILE;\n-- 需要重启实例生效\n-- 重启前确认文件系统支持Direct I/O(非tmpfs/ramfs)"},
									{Type: ActionInvestigate, Desc: "验证文件系统类型是否支持Direct I/O", SkillCommand: "/sql @datafile_paths", RawSQL: "SELECT name, SUBSTR(name, 1, 30) path_prefix FROM V$DATAFILE UNION ALL SELECT name, SUBSTR(name, 1, 30) FROM V$TEMPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用ASM存储,此参数不关键",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "使用ASM存储,filesystemio_options设置不影响ASM I/O"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "filesystemio_options非SETALL的其他值",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "filesystemio_options未设为推荐值SETALL,可能缺少directIO或asyncIO"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "建议改为SETALL同时启用directIO和asyncIO", SkillCommand: "/params filesystemio_options", RawSQL: "ALTER SYSTEM SET filesystemio_options = 'SETALL' SCOPE=SPFILE;\n-- 需要重启实例生效"},
						{Type: ActionInvestigate, Desc: "检查当前设置值", SkillCommand: "/params filesystemio_options", RawSQL: "SELECT name, value, description FROM V$PARAMETER WHERE name = 'filesystemio_options'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "filesystem", "directio", "asyncio"},
		Versions: "10g+",
	}
}

// ─── Deep Undo Rules (4) ─────────────────────────────────────────────────────

func deepUndoRules() []*Rule {
	return []*Rule{
		ruleORA1555Diagnosis(),
		ruleUndoSegmentContention(),
		ruleUndoRetentionAutoTune(),
		ruleUndoGuaranteeRisk(),
	}
}

// ruleORA1555Diagnosis provides deep diagnosis of ORA-01555 (Snapshot Too Old).
// Root causes include: undo_retention < max query length, EXPIRED undo being
// overwritten too aggressively (EXPIRED < 10% of undo), or fetching across
// commits in cursor loops. This rule checks all three vectors.
func ruleORA1555Diagnosis() *Rule {
	return &Rule{
		ID:       "ora_1555_diagnosis",
		Name:     "ORA-01555深度诊断(Snapshot Too Old)",
		Category: "undo",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01555"},
			{Type: SignalMetric, Key: metricORA1555Count},
			{Type: SignalKeyword, Key: "ORA-1555"},
			{Type: SignalKeyword, Key: "snapshot too old"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA1555Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "确认ORA-01555发生并分析undo retention vs 最长查询时间",
			Query: QueryORA1555Stats,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "确认ORA-01555发生",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查maxquerylen vs undo_retention",
						Query: QueryUndoStats,
						Branches: []Branch{
							{
								Match:    MatchGT(1),
								Label:    "maxquerylen > undo_retention,undo保留时间不足",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "最长查询时间(maxquerylen)超过undo_retention设置,长查询在运行过程中undo被回收导致ORA-01555"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大undo_retention至少覆盖最长查询时间", SkillCommand: "/params undo_retention", RawSQL: "-- undo_retention应 > maxquerylen:\nALTER SYSTEM SET undo_retention = {maxquerylen_plus_margin} SCOPE=BOTH;\n-- 同时确保undo表空间有足够空间"},
									{Type: ActionInvestigate, Desc: "检查当前undo统计", SkillCommand: "/sql @undo_stats", RawSQL: "SELECT maxquerylen, maxqueryid, unxpstealcnt, expstealcnt, ssolderrcnt FROM V$UNDOSTAT WHERE begin_time > SYSDATE - 1 ORDER BY begin_time DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "undo_retention足够,检查EXPIRED undo是否被过度回收",
								Severity: SeverityHigh,
								Then: &TreeNode{
									Step:  "检查EXPIRED undo比例(EXPIRED < 10%表示undo空间紧张)",
									Query: QueryUndoSegmentRatio,
									Branches: []Branch{
										{
											Match:    MatchLT(10),
											Label:    "EXPIRED < 10%,undo空间极度紧张",
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "undo_retention设置足够但EXPIRED undo占比<10%,undo表空间过小导致UNEXPIRED被强制回收引发ORA-01555"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "增大undo表空间", SkillCommand: "/space", RawSQL: "ALTER TABLESPACE UNDOTBS1 ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
												{Type: ActionInvestigate, Desc: "检查undo空间分布", SkillCommand: "/sql @undo_space", RawSQL: "SELECT status, COUNT(*) extents, SUM(bytes)/1024/1024 MB FROM DBA_UNDO_EXTENTS GROUP BY status"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "EXPIRED充足,可能是fetch-across-commit问题",
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "undo_retention和undo空间均充足,ORA-01555可能因cursor loop中fetch跨越了其他session的commit,导致读一致性快照失效"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "检查引发ORA-01555的SQL_ID", SkillCommand: "/sql @ora1555_sql", RawSQL: "SELECT maxqueryid, maxquerylen, ssolderrcnt, nospaceerrcnt FROM V$UNDOSTAT WHERE ssolderrcnt > 0 ORDER BY begin_time DESC FETCH FIRST 5 ROWS ONLY"},
												{Type: ActionFix, Desc: "优化长时间游标:分批提交或使用物化视图", SkillCommand: "/explain {sql_id}", RawSQL: "-- 对于fetch-across-commit:\n-- 1. 减少cursor loop内的COMMIT频率\n-- 2. 或改为BULK COLLECT分批处理\n-- 3. 检查SQL是否可通过索引减少执行时间"},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "近期无ORA-01555",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "近期未发生ORA-01555错误"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"long_transaction", "undo_space_full"},
		CausesOf: []string{},
		Tags:     []string{"undo", "ora-01555", "snapshot", "retention"},
		Versions: "9i+",
	}
}

// ruleUndoSegmentContention detects undo segment header contention when
// too many transactions share too few undo segments. Symptoms include
// "buffer busy waits" on undo headers with V$WAITSTAT showing undo header waits.
func ruleUndoSegmentContention() *Rule {
	return &Rule{
		ID:       "undo_segment_contention",
		Name:     "Undo段头争用(Undo Header Contention)",
		Category: "undo",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "buffer busy waits"},
			{Type: SignalMetric, Key: metricUndoSegContention},
			{Type: SignalKeyword, Key: "undo contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricUndoSegContention, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查每个undo segment的并发事务数(txns_per_seg)",
			Query: QueryUndoSegContention,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "每段并发事务>5,undo segment不足",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查V$WAITSTAT中undo header的等待",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在undo header buffer busy waits",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "每个undo segment平均>5个并发事务,导致undo header buffer busy waits,事务争用undo段头槽位"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大undo表空间使Oracle自动创建更多undo segment", SkillCommand: "/space", RawSQL: "ALTER TABLESPACE UNDOTBS1 ADD DATAFILE SIZE 5G AUTOEXTEND ON MAXSIZE 20G;\n-- Oracle会根据空间自动创建更多undo segment"},
									{Type: ActionInvestigate, Desc: "查看当前undo segment使用情况", SkillCommand: "/sql @undo_segments", RawSQL: "SELECT segment_name, status, tablespace_name FROM DBA_ROLLBACK_SEGS WHERE status = 'ONLINE';\nSELECT class, count, time FROM V$WAITSTAT WHERE class LIKE '%undo%'"},
									{Type: ActionPrevent, Desc: "检查_rollback_segment_count隐含参数(如手动设置过)", SkillCommand: "/sql @undo_params", RawSQL: "SELECT ksppinm name, ksppstvl value FROM x$ksppi a, x$ksppsv b WHERE a.indx = b.indx AND ksppinm LIKE '%rollback%segment%count%'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无明显undo header waits但并发事务/段比率偏高",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "每undo segment并发事务数偏高(>5),虽暂无明显等待但在高峰期可能出现争用"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "适度增大undo表空间以允许更多segment", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, ROUND(SUM(bytes)/1024/1024/1024,2) GB FROM DBA_DATA_FILES WHERE tablespace_name LIKE 'UNDO%' GROUP BY tablespace_name"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "undo segment并发事务数正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "每undo segment并发事务数在合理范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"undo", "contention", "buffer_busy", "segment"},
		Versions: "9i+",
	}
}

// ruleUndoRetentionAutoTune detects when Oracle's _undo_autotune is
// aggressively growing tuned_undoretention, consuming excessive undo space.
// The tuned_undoretention can grow far beyond undo_retention, causing the
// undo tablespace to fill up and preventing new DML.
func ruleUndoRetentionAutoTune() *Rule {
	return &Rule{
		ID:       "undo_retention_autotune",
		Name:     "Undo自动调优过度扩展(AutoTune Retention过大)",
		Category: "undo",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricUndoAutoTune},
			{Type: SignalMetric, Key: metricUndoUsedPct},
			{Type: SignalKeyword, Key: "undo autotune"},
			{Type: SignalKeyword, Key: "tuned_undoretention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricUndoUsedPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查tuned_undoretention vs undo_retention设置",
			Query: QueryUndoAutoTune,
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "tuned_undoretention > 3 × undo_retention,自动调优过度",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查undo表空间使用率确认影响",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricUndoUsedPct) },
						Branches: []Branch{
							{
								Match:    MatchGT(90),
								Label:    "undo使用率>90%且autotune过度扩展",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Oracle _undo_autotune将tuned_undoretention扩展到undo_retention的3倍以上,导致UNEXPIRED undo占满空间,新DML面临ORA-30036风险"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "关闭_undo_autotune并设置合理undo_retention", SkillCommand: "/sql @disable_autotune", RawSQL: "ALTER SYSTEM SET \"_undo_autotune\" = FALSE SCOPE=BOTH;\nALTER SYSTEM SET undo_retention = 900 SCOPE=BOTH;\n-- 900秒(15分钟)适合大多数OLTP场景"},
									{Type: ActionFix, Desc: "增大undo表空间或减小undo_retention", SkillCommand: "/space", RawSQL: "-- 查看当前tuned_undoretention:\nSELECT tuned_undoretention FROM V$UNDOSTAT WHERE begin_time = (SELECT MAX(begin_time) FROM V$UNDOSTAT);\n-- 查看undo空间分布:\nSELECT status, SUM(bytes)/1024/1024 MB FROM DBA_UNDO_EXTENTS GROUP BY status"},
								},
							},
							{
								Match:    MatchGT(80),
								Label:    "undo使用率80-90%,autotune正在消耗空间",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "tuned_undoretention远超undo_retention设置,undo空间80-90%被占用,趋势继续将导致DML失败"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "调整_undo_autotune或增大undo表空间", SkillCommand: "/sql @undo_autotune_check", RawSQL: "SELECT tuned_undoretention, maxquerylen, maxqueryid FROM V$UNDOSTAT ORDER BY begin_time DESC FETCH FIRST 10 ROWS ONLY"},
									{Type: ActionPrevent, Desc: "设置undo_retention上限防止无限增长", SkillCommand: "/params undo_retention", RawSQL: "ALTER SYSTEM SET undo_retention = 1800 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "undo使用率<80%,autotune影响有限",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "tuned_undoretention偏高但undo空间充足,暂无风险"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控tuned_undoretention增长趋势", SkillCommand: "/sql @undo_autotune_trend", RawSQL: "SELECT begin_time, tuned_undoretention, maxquerylen FROM V$UNDOSTAT ORDER BY begin_time DESC FETCH FIRST 24 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "tuned_undoretention在合理范围",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "undo自动调优的retention在合理范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"undo_space_full"},
		Tags:     []string{"undo", "autotune", "retention", "space"},
		Versions: "10g+",
	}
}

// ruleUndoGuaranteeRisk detects UNDO RETENTION GUARANTEE on OLTP systems.
// When GUARANTEE is enabled, Oracle will NEVER overwrite UNEXPIRED undo even
// if the tablespace is full — new DML will fail with ORA-30036 instead.
// This is dangerous for OLTP where DML availability is critical.
func ruleUndoGuaranteeRisk() *Rule {
	return &Rule{
		ID:       "undo_guarantee_risk",
		Name:     "Undo Retention Guarantee风险(OLTP系统DML可能失败)",
		Category: "undo",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricUndoGuarantee},
			{Type: SignalErrorCode, Key: "ORA-30036"},
			{Type: SignalKeyword, Key: "retention guarantee"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricUndoGuarantee, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "确认RETENTION GUARANTEE状态和系统类型",
			Query: QueryUndoRetGuarantee,
			Branches: []Branch{
				{
					Match:    MatchBool(true),
					Label:    "RETENTION GUARANTEE已启用",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查undo表空间使用率评估DML失败风险",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricUndoUsedPct) },
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "undo使用率>80%且GUARANTEE启用,DML失败风险高",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RETENTION GUARANTEE启用且undo使用率>80%,Oracle拒绝覆盖UNEXPIRED undo,新DML将因ORA-30036失败而非覆盖旧undo"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "OLTP系统立即关闭RETENTION GUARANTEE", SkillCommand: "/sql @noguarantee_undo", RawSQL: "ALTER TABLESPACE UNDOTBS1 RETENTION NOGUARANTEE"},
									{Type: ActionFix, Desc: "增大undo表空间以同时满足guarantee需求", SkillCommand: "/space", RawSQL: "ALTER TABLESPACE UNDOTBS1 ADD DATAFILE SIZE 10G AUTOEXTEND ON MAXSIZE 32G"},
									{Type: ActionInvestigate, Desc: "确认是否真的需要guarantee(通常仅数据仓库/报表需要)", SkillCommand: "/sql @undo_ts_info", RawSQL: "SELECT tablespace_name, retention FROM DBA_TABLESPACES WHERE contents = 'UNDO'"},
								},
							},
							{
								Match:    MatchGT(50),
								Label:    "undo使用率50-80%,GUARANTEE存在潜在风险",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RETENTION GUARANTEE启用,undo使用率50-80%,在DML高峰或长查询叠加时可能触发ORA-30036"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "OLTP系统建议关闭RETENTION GUARANTEE", SkillCommand: "/sql @noguarantee_undo", RawSQL: "ALTER TABLESPACE UNDOTBS1 RETENTION NOGUARANTEE"},
									{Type: ActionPrevent, Desc: "若必须保留guarantee,需确保undo表空间足够大", SkillCommand: "/space", RawSQL: "SELECT tablespace_name, retention, ROUND((SELECT SUM(bytes) FROM DBA_DATA_FILES WHERE tablespace_name = t.tablespace_name)/1024/1024/1024,2) size_gb FROM DBA_TABLESPACES t WHERE contents = 'UNDO'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "undo使用率<50%,guarantee当前风险较低",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "RETENTION GUARANTEE启用但undo空间充足,建议OLTP系统仍然关闭guarantee以避免极端情况"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "评估是否需要guarantee,OLTP建议关闭", SkillCommand: "/sql @undo_ts_info", RawSQL: "SELECT tablespace_name, retention FROM DBA_TABLESPACES WHERE contents = 'UNDO'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "RETENTION GUARANTEE未启用(NOGUARANTEE)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Undo表空间使用NOGUARANTEE模式,符合OLTP最佳实践"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"undo_space_full"},
		Tags:     []string{"undo", "guarantee", "ora-30036", "oltp"},
		Versions: "10g+",
	}
}

// ─── Deep Session Rules (3) ──────────────────────────────────────────────────

func deepSessionRules() []*Rule {
	return []*Rule{
		ruleDRCPRecommendation(),
		ruleIdleInTransaction(),
		ruleProcessLimitApproaching(),
	}
}

// ruleDRCPRecommendation detects environments with >500 connections and
// >60% idle ratio where Database Resident Connection Pooling (DRCP) would
// dramatically reduce resource consumption. DRCP shares a pool of dedicated
// server processes among many connections.
func ruleDRCPRecommendation() *Rule {
	return &Rule{
		ID:       "drcp_recommendation",
		Name:     "建议启用DRCP(数据库驻留连接池)",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricConnectionCount},
			{Type: SignalMetric, Key: metricIdleSessionRatio},
			{Type: SignalKeyword, Key: "DRCP"},
			{Type: SignalKeyword, Key: "connection pool"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricConnectionCount, Op: OpGT, Value: 500},
				{Source: "metrics", Field: metricIdleSessionRatio, Op: OpGT, Value: 60},
			},
		},
		Tree: &TreeNode{
			Step:  "检查连接数和idle比率评估DRCP收益",
			Query: QueryDRCPStats,
			Branches: []Branch{
				{
					Match:    MatchGT(500),
					Label:    "连接数>500",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查idle session比例",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricIdleSessionRatio) },
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "idle > 80%,DRCP收益极大",
								Severity: SeverityHigh,
								Then: &TreeNode{
									Step:  "检查DRCP是否已配置",
									Query: QueryDRCPStats,
									Branches: []Branch{
										{
											Match:    MatchBool(false),
											Label:    "DRCP未启用",
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "连接数>500且idle比例>80%,DRCP可将服务器进程数减少80%+,节省PGA内存和OS资源"},
											},
											Actions: []Action{
												{Type: ActionFix, Desc: "配置并启用DRCP连接池", SkillCommand: "/sql @enable_drcp", RawSQL: "-- 启用DRCP:\nBEGIN DBMS_CONNECTION_POOL.CONFIGURE_POOL(\n  pool_name => 'SYS_DEFAULT_CONNECTION_POOL',\n  minsize => 10,\n  maxsize => 100,\n  incrsize => 5,\n  session_cached_cursors => 50,\n  inactivity_timeout => 300,\n  max_think_time => 600);\nEND;\n/\nBEGIN DBMS_CONNECTION_POOL.START_POOL; END;\n/"},
												{Type: ActionPrevent, Desc: "应用端连接串添加:POOLED(对OCI/JDBC-thin)", SkillCommand: "/sql @drcp_config", RawSQL: "-- 连接串示例:\n-- jdbc:oracle:thin:@//host:1521/service:POOLED\n-- 或TNS: (SERVER=POOLED)\nSELECT * FROM DBA_CPOOL_INFO"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "DRCP已启用",
											Severity: SeverityLow,
											Findings: []Finding{{Desc: "DRCP已启用,连接池模式下运行"}},
											Actions: []Action{
												{Type: ActionPrevent, Desc: "检查DRCP池使用效率", SkillCommand: "/sql @drcp_stats", RawSQL: "SELECT * FROM V$CPOOL_STATS"},
											},
										},
									},
								},
							},
							{
								Match:    MatchGT(60),
								Label:    "idle 60-80%,DRCP有一定收益",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "连接数>500且idle比例60-80%,DRCP可显著减少服务器资源占用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "评估并启用DRCP", SkillCommand: "/sql @drcp_assess", RawSQL: "SELECT 'Total Sessions' item, COUNT(*) val FROM V$SESSION WHERE type = 'USER' UNION ALL SELECT 'Idle Sessions', COUNT(*) FROM V$SESSION WHERE type = 'USER' AND status = 'INACTIVE' AND last_call_et > 60 UNION ALL SELECT 'Active Sessions', COUNT(*) FROM V$SESSION WHERE type = 'USER' AND status = 'ACTIVE'"},
									{Type: ActionInvestigate, Desc: "按program/machine分析连接分布", SkillCommand: "/sessions", RawSQL: "SELECT program, machine, status, COUNT(*) cnt FROM V$SESSION WHERE type = 'USER' GROUP BY program, machine, status ORDER BY cnt DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "idle < 60%,DRCP收益有限",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "idle比例<60%,多数连接活跃,DRCP收益有限"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "优化应用连接池配置减少空闲连接", SkillCommand: "/sessions", RawSQL: "SELECT program, status, COUNT(*) FROM V$SESSION WHERE type = 'USER' GROUP BY program, status ORDER BY 3 DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "连接数<=500,DRCP不是优先项",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前连接数<=500,DRCP优先级不高"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"session", "drcp", "connection_pool", "scalability"},
		Versions: "11g+",
	}
}

// ruleIdleInTransaction detects sessions that are idle but still inside an
// open transaction for more than 5 minutes. These sessions hold row-level
// and table-level locks, preventing other sessions from modifying the same
// data and potentially causing cascading lock waits.
func ruleIdleInTransaction() *Rule {
	return &Rule{
		ID:       "idle_in_transaction",
		Name:     "事务中空闲(Idle in Transaction持锁)",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIdleInTransaction},
			{Type: SignalMetric, Key: metricBlockingChains},
			{Type: SignalKeyword, Key: "idle in transaction"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIdleInTransaction, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查空闲但有未提交事务的会话",
			Query: QueryIdleInTransaction,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在idle-in-transaction会话(>5分钟)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否持有锁并阻塞其他会话",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricBlockingChains) },
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "idle事务正在阻塞其他会话",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "存在idle-in-transaction会话(空闲>5分钟但事务未提交),且正在阻塞其他会话,导致锁等待级联"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位并评估是否kill阻塞的idle事务", SkillCommand: "/locks", RawSQL: "SELECT s.sid, s.serial#, s.username, s.machine, s.program, s.status, s.last_call_et idle_sec, t.start_time txn_start, (SELECT COUNT(*) FROM V$SESSION b WHERE b.blocking_session = s.sid) blocked_cnt FROM V$SESSION s JOIN V$TRANSACTION t ON s.taddr = t.addr WHERE s.status = 'INACTIVE' AND s.last_call_et > 300 AND EXISTS (SELECT 1 FROM V$SESSION b WHERE b.blocking_session = s.sid) ORDER BY blocked_cnt DESC"},
									{Type: ActionFix, Desc: "Kill阻塞会话释放锁", SkillCommand: "/kill {sid},{serial#}", RawSQL: "ALTER SYSTEM KILL SESSION '{sid},{serial#}' IMMEDIATE"},
									{Type: ActionPrevent, Desc: "配置Oracle IDLE_TIME profile限制", SkillCommand: "/sql @idle_profile", RawSQL: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME 15;\n-- 或设置_close_cached_open_cursors等"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "idle事务未阻塞他人但仍占用资源",
								Severity: SeverityMedium,
								Then: &TreeNode{
									Step:  "检查idle事务持续时间",
									Query: QueryIdleInTransaction,
									Branches: []Branch{
										{
											Match:    MatchGT(30),
											Label:    "idle-in-transaction > 30分钟",
											Severity: SeverityHigh,
											Findings: []Finding{
												{Desc: "存在idle超过30分钟但事务未提交的会话,虽未阻塞他人但占用undo和锁资源,可能引发ORA-01555"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "查看idle事务详情", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, s.machine, s.program, ROUND(s.last_call_et/60) idle_min, t.used_ublk*8/1024 undo_mb FROM V$SESSION s JOIN V$TRANSACTION t ON s.taddr = t.addr WHERE s.status = 'INACTIVE' AND s.last_call_et > 1800 ORDER BY s.last_call_et DESC"},
												{Type: ActionFix, Desc: "通知应用团队修复未提交事务的代码逻辑", SkillCommand: "/sql @idle_txn_source", RawSQL: "SELECT s.program, s.machine, s.module, s.action, COUNT(*) FROM V$SESSION s JOIN V$TRANSACTION t ON s.taddr = t.addr WHERE s.status = 'INACTIVE' AND s.last_call_et > 300 GROUP BY s.program, s.machine, s.module, s.action ORDER BY 5 DESC"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "idle-in-transaction 5-30分钟",
											Severity: SeverityMedium,
											Findings: []Finding{
												{Desc: "存在idle 5-30分钟的未提交事务,应关注其来源"},
											},
											Actions: []Action{
												{Type: ActionInvestigate, Desc: "监控并定位来源应用", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.username, s.program, s.machine, ROUND(s.last_call_et/60,1) idle_min FROM V$SESSION s WHERE s.taddr IS NOT NULL AND s.status = 'INACTIVE' AND s.last_call_et > 300 ORDER BY s.last_call_et DESC"},
											},
										},
									},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无idle-in-transaction会话",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到idle-in-transaction会话"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"long_transaction"},
		Tags:     []string{"session", "idle", "transaction", "lock"},
		Versions: "9i+",
	}
}

// ruleProcessLimitApproaching detects when the current process count
// exceeds 80% of the configured processes limit. Hitting the limit causes
// ORA-00020 (maximum number of processes exceeded) and blocks all new connections.
func ruleProcessLimitApproaching() *Rule {
	return &Rule{
		ID:       "process_limit_approaching",
		Name:     "进程数接近上限(ORA-00020风险)",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricProcessLimitPct},
			{Type: SignalMetric, Key: metricResourceLimitPct},
			{Type: SignalErrorCode, Key: "ORA-00020"},
			{Type: SignalKeyword, Key: "process limit"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricProcessLimitPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前进程数相对于processes参数的使用率",
			Query: QueryProcessLimit,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "进程使用率>95%,即将ORA-00020",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "分析进程构成:是否有异常来源",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "单一来源占用>50%进程,可能连接泄漏",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "进程使用率>95%即将触发ORA-00020,且单一来源占用超过50%进程配额,疑似连接泄漏"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位异常连接来源并Kill多余进程", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, COUNT(*) cnt, ROUND(COUNT(*)*100/(SELECT value FROM V$PARAMETER WHERE name = 'processes'),1) pct FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program ORDER BY cnt DESC"},
									{Type: ActionUrgent, Desc: "紧急增大processes参数(需重启)", SkillCommand: "/params processes", RawSQL: "ALTER SYSTEM SET processes = {new_limit} SCOPE=SPFILE;\n-- 需要重启实例生效\n-- sessions = 1.5 * processes + 22"},
									{Type: ActionFix, Desc: "Kill泄漏来源的idle会话", SkillCommand: "/sql @kill_idle", RawSQL: "-- 批量Kill某来源的idle会话:\nBEGIN FOR c IN (SELECT sid, serial# FROM V$SESSION WHERE machine = '{leak_source}' AND status = 'INACTIVE' AND last_call_et > 600) LOOP EXECUTE IMMEDIATE 'ALTER SYSTEM KILL SESSION '''||c.sid||','||c.serial#||''' IMMEDIATE'; END LOOP; END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "进程分散在多个来源,业务增长",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "进程使用率>95%,多个来源正常分布,需增大processes参数容纳更多连接"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大processes参数(需要重启实例)", SkillCommand: "/params processes", RawSQL: "ALTER SYSTEM SET processes = {new_limit} SCOPE=SPFILE;\n-- 建议设为当前值的1.5-2倍\n-- 同时调整sessions = 1.5 * processes + 22"},
									{Type: ActionFix, Desc: "考虑启用DRCP减少进程消耗", SkillCommand: "/sql @drcp_assess", RawSQL: "SELECT 'processes limit' item, value FROM V$PARAMETER WHERE name = 'processes' UNION ALL SELECT 'current processes', TO_CHAR(current_utilization) FROM V$RESOURCE_LIMIT WHERE resource_name = 'processes' UNION ALL SELECT 'max ever used', TO_CHAR(max_utilization) FROM V$RESOURCE_LIMIT WHERE resource_name = 'processes'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "进程使用率80-95%,需要关注",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "进程使用率80-95%,接近processes参数上限,业务高峰可能触发ORA-00020"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "计划增大processes参数", SkillCommand: "/params processes", RawSQL: "SELECT resource_name, current_utilization, max_utilization, initial_allocation, limit_value FROM V$RESOURCE_LIMIT WHERE resource_name IN ('processes','sessions')"},
						{Type: ActionInvestigate, Desc: "检查连接来源分布", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, status, COUNT(*) FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program, status ORDER BY 4 DESC FETCH FIRST 15 ROWS ONLY"},
						{Type: ActionPrevent, Desc: "配置连接池和IDLE_TIME限制", SkillCommand: "/sql @resource_profile", RawSQL: "SELECT profile, resource_name, limit FROM DBA_PROFILES WHERE resource_name IN ('IDLE_TIME','CONNECT_TIME','SESSIONS_PER_USER') AND limit != 'UNLIMITED' AND limit != 'DEFAULT'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "进程使用率<80%,正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "进程使用率在安全范围内(<80%)"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"session_leak"},
		CausesOf: []string{"connection_exhaust"},
		Tags:     []string{"session", "processes", "ora-00020", "resource_limit"},
		Versions: "9i+",
	}
}
