/*-------------------------------------------------------------------------
 *
 * rules_memio_ext.go
 *	  Oracle rule engine — memory + IO extension rules (PGA bloat, redo write spikes, async IO).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_memio_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Extended Memory/IO metric name constants ───────────────────────────────

const (
	metricIMColumnStoreEnabled = "im_column_store_enabled"
	metricIMColumnStoreUsedPct = "im_column_store_used_pct"
	metricSmartFlashCacheHit   = "smart_flash_cache_hit_pct"
	metricSmartFlashCacheConf  = "smart_flash_cache_configured"
	metricKeepPoolSizeMB       = "keep_pool_size_mb"
	metricRecyclePoolSizeMB    = "recycle_pool_size_mb"
	metricPGAWorkareaPolicy    = "pga_workarea_policy"
	metricPGAMultipassCount    = "pga_multipass_count"
	metricSubpoolLatchWait     = "shared_pool_subpool_latch_wait"
	metricLibCacheReloadRatio  = "library_cache_reload_ratio"
	metricSGATargetMB          = "sga_target_mb"
	metricSGAMinimumsSumMB     = "sga_minimums_sum_mb"
	metricPGAMultipassSQLCount = "pga_multipass_sql_count"

	metricRedoLogMultiplexed    = "redo_log_multiplexed"
	metricControlfileMultiplexed = "controlfile_multiplexed"
	metricAsyncIOFallbackSync   = "async_io_fallback_sync"
	metricIOCalibrated          = "io_calibrated"
	metricFlashCacheHitRatio    = "flash_cache_hit_ratio"
	metricIOSchedulerType       = "io_scheduler_type"
	metricMultipathIOConfigured = "multipath_io_configured"
)

// ─── Extended Memory/IO QueryID constants ────────────────────────────────────

const (
	QueryIMColumnStoreStatus QueryID = "im_column_store_status"
	QuerySmartFlashCacheInfo QueryID = "smart_flash_cache_info"
	QueryKeepRecyclePool     QueryID = "keep_recycle_pool"
	QueryPGAWorkareaPolicy   QueryID = "pga_workarea_policy"
	QuerySubpoolLatchStats   QueryID = "subpool_latch_stats"
	QueryLibCacheReloads     QueryID = "lib_cache_reloads"
	QuerySGATargetComponents QueryID = "sga_target_components"
	QueryPGAMultipassSQL     QueryID = "pga_multipass_sql"
	QueryRedoLogMembers      QueryID = "redo_log_members"
	QueryControlfileLocations QueryID = "controlfile_locations"
	QueryAsyncIOStatus       QueryID = "async_io_status"
	QueryIOCalibration       QueryID = "io_calibration"
	QueryFlashCacheStats     QueryID = "flash_cache_stats"
	QueryIOScheduler         QueryID = "io_scheduler"
	QueryMultipathConfig     QueryID = "multipath_config"
)

// ═══════════════════════════════════════════════════════════════════════════
// Extended Memory/IO Rules (15)
// MI2-001 ~ MI2-015
// ═══════════════════════════════════════════════════════════════════════════

func extMemoryIORules() []*Rule {
	return []*Rule{
		// Memory (8)
		ruleInMemoryColumnStore(),       // MI2-001
		ruleSmartFlashCache(),           // MI2-002
		ruleBufferPoolKeepRecycle(),     // MI2-003
		rulePGAWorkareaPolicyCaching(),  // MI2-004
		ruleSharedPoolSubpoolContention(), // MI2-005
		ruleLibraryCacheFragmentation(), // MI2-006
		ruleSGATargetTooLow(),           // MI2-007
		rulePGAMultipassSQL(),           // MI2-008

		// IO (7)
		ruleRedoLogMultiplexing(),  // MI2-009
		ruleControlfileMultiplex(), // MI2-010
		ruleASyncIOVerification(),  // MI2-011
		ruleIOCalibrationBaseline(), // MI2-012
		ruleFlashCacheHitRatio(),   // MI2-013
		ruleIOSchedulerChoice(),    // MI2-014
		ruleMultipathIOConfig(),    // MI2-015
	}
}

// ─── Memory Rules (8) ────────────────────────────────────────────────────────

// ruleInMemoryColumnStore detects Database In-Memory column store configuration
// issues: inmemory_size=0 on 12.1.0.2+, or IM column store full (>90%), or
// objects with INMEMORY attribute but not populated.
func ruleInMemoryColumnStore() *Rule {
	return &Rule{
		ID:       "MI2-001",
		Name:     "In-Memory Column Store配置问题(IM Column Store)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIMColumnStoreEnabled},
			{Type: SignalMetric, Key: metricIMColumnStoreUsedPct},
			{Type: SignalKeyword, Key: "in-memory"},
			{Type: SignalKeyword, Key: "column store"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIMColumnStoreEnabled, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查inmemory_size参数和IM Column Store状态",
			Query: QueryIMColumnStoreStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("DISABLED"),
					Label:    "inmemory_size=0,In-Memory未启用",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查数据库版本是否支持In-Memory(12.1.0.2+)",
						Query: QueryIMColumnStoreStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("SUPPORTED"),
								Label:    "版本支持但未启用,可能是有意未用",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "数据库版本支持In-Memory Column Store但未启用(inmemory_size=0),如有OLAP分析负载可考虑启用"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "评估是否适合启用In-Memory", SkillCommand: "/params inmemory_size", RawSQL: "SELECT banner_full FROM V$VERSION"},
									{Type: ActionFix, Desc: "设置inmemory_size启用IM(需重启)", SkillCommand: "/params inmemory_size", RawSQL: "ALTER SYSTEM SET inmemory_size = '4G' SCOPE=SPFILE", Risk: "需要重启实例,且占用SGA内存", Rollback: "ALTER SYSTEM SET inmemory_size = 0 SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "版本不支持In-Memory",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "当前Oracle版本不支持In-Memory Column Store"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchGT(90),
					Label:    "IM Column Store使用率>90%,空间不足",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查IM对象填充优先级和未填充对象",
						Query: QueryIMColumnStoreStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在INMEMORY对象未能填充(空间不够)",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "IM Column Store使用>90%,部分INMEMORY对象未能填充,需增大inmemory_size或调整对象优先级"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大inmemory_size", SkillCommand: "/params inmemory_size", RawSQL: "ALTER SYSTEM SET inmemory_size = '{new_size}' SCOPE=SPFILE", Risk: "需重启实例"},
									{Type: ActionFix, Desc: "降低低优先级对象的IM级别", SkillCommand: "/sql @im_objects", RawSQL: "SELECT owner, segment_name, inmemory_size/1024/1024 im_mb, inmemory_priority, populate_status FROM V$IM_SEGMENTS ORDER BY inmemory_size DESC"},
									{Type: ActionInvestigate, Desc: "检查哪些对象可移除INMEMORY属性", SkillCommand: "/sql @im_candidates", RawSQL: "SELECT owner, table_name, inmemory, inmemory_priority, inmemory_compression FROM DBA_TABLES WHERE inmemory = 'ENABLED' ORDER BY num_rows DESC NULLS LAST"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "所有对象已填充但空间紧张",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "IM Column Store接近满载,应预留增长空间"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "监控IM使用率增长趋势", SkillCommand: "/sql @im_stats", RawSQL: "SELECT pool, alloc_bytes/1024/1024 alloc_mb, used_bytes/1024/1024 used_mb, populate_status FROM V$INMEMORY_AREA"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "IM Column Store配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "In-Memory Column Store已启用且使用率正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"memory", "in-memory", "column_store", "olap"},
		Versions: "12.1.0.2+",
	}
}

// ruleSmartFlashCache detects Smart Flash Cache (Database Smart Flash Cache / SFC)
// configuration and monitoring issues. SFC uses SSD as L2 cache for buffer cache.
func ruleSmartFlashCache() *Rule {
	return &Rule{
		ID:       "MI2-002",
		Name:     "Smart Flash Cache配置/监控(SFC)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricSmartFlashCacheHit},
			{Type: SignalMetric, Key: metricSmartFlashCacheConf},
			{Type: SignalKeyword, Key: "flash cache"},
			{Type: SignalKeyword, Key: "smart flash"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSmartFlashCacheConf, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查db_flash_cache_file和db_flash_cache_size配置",
			Query: QuerySmartFlashCacheInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("NOT_CONFIGURED"),
					Label:    "Smart Flash Cache未配置",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查是否有SSD设备可用于Flash Cache",
						Query: QuerySmartFlashCacheInfo,
						Branches: []Branch{
							{
								Match:    MatchEquals("SSD_AVAILABLE"),
								Label:    "有SSD可用但未配置SFC",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "检测到SSD设备但未配置Database Smart Flash Cache,对于buffer cache命中率偏低的OLTP库可显著提升性能"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置Smart Flash Cache", SkillCommand: "/params db_flash_cache_file", RawSQL: "ALTER SYSTEM SET db_flash_cache_file = '/dev/ssd_device' SCOPE=SPFILE;\nALTER SYSTEM SET db_flash_cache_size = '64G' SCOPE=SPFILE;", Risk: "需重启实例", Rollback: "ALTER SYSTEM SET db_flash_cache_size = 0 SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无SSD设备,Flash Cache不适用",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "无SSD设备可用,Smart Flash Cache不适用"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchLT(50),
					Label:    "Flash Cache命中率 < 50%,效果差",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查Flash Cache中热块分布",
						Query: QuerySmartFlashCacheInfo,
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "大量全表扫描块进入Flash Cache,污染缓存",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Smart Flash Cache命中率<50%,大量全表扫描block污染Flash Cache,应使用db_flash_cache_keep排除大表"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将全表扫描大表设为NOCACHE避免污染Flash Cache", SkillCommand: "/sql @flash_cache_objects", RawSQL: "ALTER TABLE {schema}.{table_name} STORAGE (FLASH_CACHE NONE)"},
									{Type: ActionInvestigate, Desc: "查看Flash Cache对象统计", SkillCommand: "/sql @flash_cache_stats", RawSQL: "SELECT * FROM V$FLASH_CACHE_STAT"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Flash Cache大小可能不足",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Smart Flash Cache命中率低,可能需要增大db_flash_cache_size或调优缓存策略"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大db_flash_cache_size", SkillCommand: "/params db_flash_cache_size", RawSQL: "ALTER SYSTEM SET db_flash_cache_size = '{new_size}' SCOPE=SPFILE"},
									{Type: ActionInvestigate, Desc: "检查Flash Cache使用统计", SkillCommand: "/sql @flash_cache_stats", RawSQL: "SELECT stat_name, value FROM V$FLASH_CACHE_STAT"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Smart Flash Cache正常运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Smart Flash Cache配置正常,命中率良好"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"buffer_busy_waits"},
		CausesOf: []string{},
		Tags:     []string{"memory", "flash_cache", "ssd", "l2_cache"},
		Versions: "11.2+",
	}
}

// ruleBufferPoolKeepRecycle detects KEEP and RECYCLE buffer pool configuration
// issues: frequently accessed small tables not in KEEP pool, or RECYCLE pool
// not used for large table scans.
func ruleBufferPoolKeepRecycle() *Rule {
	return &Rule{
		ID:       "MI2-003",
		Name:     "KEEP/RECYCLE缓冲池配置(Buffer Pool Keep/Recycle)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricKeepPoolSizeMB},
			{Type: SignalMetric, Key: metricRecyclePoolSizeMB},
			{Type: SignalKeyword, Key: "keep pool"},
			{Type: SignalKeyword, Key: "recycle pool"},
			{Type: SignalKeyword, Key: "buffer pool"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricKeepPoolSizeMB, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查db_keep_cache_size和db_recycle_cache_size配置",
			Query: QueryKeepRecyclePool,
			Branches: []Branch{
				{
					Match:    MatchEquals("BOTH_ZERO"),
					Label:    "KEEP和RECYCLE池均未配置",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查是否存在频繁访问的小表和大表全扫描",
						Query: QueryKeepRecyclePool,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在适合放入KEEP池的高频小表",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "未配置KEEP/RECYCLE缓冲池,检测到高频访问小表可受益于KEEP pool(避免被LRU淘汰)"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置db_keep_cache_size并将小热表分配到KEEP池", SkillCommand: "/params db_keep_cache_size", RawSQL: "ALTER SYSTEM SET db_keep_cache_size = '512M' SCOPE=BOTH", Risk: "会减少DEFAULT pool大小"},
									{Type: ActionFix, Desc: "将频繁访问的小表设为KEEP", SkillCommand: "/sql @keep_candidates", RawSQL: "ALTER TABLE {schema}.{table} STORAGE (BUFFER_POOL KEEP)"},
									{Type: ActionInvestigate, Desc: "找出KEEP池候选表(小表+高访问频率)", SkillCommand: "/sql @keep_candidates", RawSQL: "SELECT owner, object_name, object_type, blocks, ROUND(blocks*8/1024,1) size_mb FROM DBA_SEGMENTS s JOIN (SELECT obj# FROM X$BH GROUP BY obj# HAVING COUNT(*) > 1000) h ON s.header_block = h.obj# WHERE blocks < 1000 ORDER BY blocks DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无明显的KEEP/RECYCLE候选对象",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "KEEP/RECYCLE缓冲池未配置,当前工作负载无明显受益场景"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchEquals("KEEP_OVERFLOW"),
					Label:    "KEEP池使用率过高,频繁淘汰",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "KEEP缓冲池使用率过高,分配到KEEP池的对象超出池容量,频繁发生LRU淘汰,违背KEEP池设计初衷"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大db_keep_cache_size", SkillCommand: "/params db_keep_cache_size", RawSQL: "ALTER SYSTEM SET db_keep_cache_size = '{new_size}' SCOPE=BOTH"},
						{Type: ActionInvestigate, Desc: "检查KEEP池中对象总大小", SkillCommand: "/sql @keep_pool_objects", RawSQL: "SELECT owner, segment_name, segment_type, bytes/1024/1024 mb FROM DBA_SEGMENTS WHERE buffer_pool = 'KEEP' ORDER BY bytes DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "KEEP/RECYCLE池配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "KEEP/RECYCLE缓冲池配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"buffer_busy_waits"},
		Tags:     []string{"memory", "buffer_pool", "keep", "recycle", "cache"},
		Versions: "9i+",
	}
}

// rulePGAWorkareaPolicyCaching detects PGA workarea policy set to MANUAL
// (instead of AUTO), which prevents automatic PGA memory management and
// may lead to excessive sort/hash memory usage or PGA target violations.
func rulePGAWorkareaPolicyCaching() *Rule {
	return &Rule{
		ID:       "MI2-004",
		Name:     "PGA工作区策略问题(Workarea Policy AUTO vs MANUAL)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPGAWorkareaPolicy},
			{Type: SignalMetric, Key: metricPGAUsedPct},
			{Type: SignalKeyword, Key: "pga workarea"},
			{Type: SignalKeyword, Key: "workarea policy"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPGAWorkareaPolicy, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查workarea_size_policy参数",
			Query: QueryPGAWorkareaPolicy,
			Branches: []Branch{
				{
					Match:    MatchEquals("MANUAL"),
					Label:    "workarea_size_policy=MANUAL,手动管理",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查sort_area_size和hash_area_size设置",
						Query: QueryPGAWorkareaPolicy,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "sort_area_size > 100MB,单会话可用过大PGA",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "workarea_size_policy=MANUAL且sort_area_size > 100MB,每个执行排序的会话可消耗大量PGA,高并发下易导致系统内存耗尽"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "切换为AUTO策略并设置合理pga_aggregate_target", SkillCommand: "/params workarea_size_policy", RawSQL: "ALTER SYSTEM SET workarea_size_policy = 'AUTO' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "设置pga_aggregate_target", SkillCommand: "/params pga_aggregate_target", RawSQL: "ALTER SYSTEM SET pga_aggregate_target = '{target_size}' SCOPE=BOTH"},
									{Type: ActionPrevent, Desc: "监控PGA使用情况", SkillCommand: "/sql @pga_stats", RawSQL: "SELECT name, value FROM V$PGASTAT WHERE name IN ('aggregate PGA target parameter', 'total PGA allocated', 'maximum PGA allocated', 'over allocation count')"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "MANUAL但sort_area_size合理",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "workarea_size_policy=MANUAL,Oracle推荐使用AUTO让数据库自动管理PGA工作区分配"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "切换为AUTO策略", SkillCommand: "/params workarea_size_policy", RawSQL: "ALTER SYSTEM SET workarea_size_policy = 'AUTO' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("AUTO"),
					Label:    "workarea_size_policy=AUTO(推荐配置)",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查PGA over allocation和multipass情况",
						Query: QueryPGAAdvice,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在PGA over allocation",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "workarea_size_policy=AUTO但PGA发生over allocation,pga_aggregate_target设置过小"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大pga_aggregate_target", SkillCommand: "/params pga_aggregate_target", RawSQL: "ALTER SYSTEM SET pga_aggregate_target = '{new_size}' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "查看PGA Advice确定最优target", SkillCommand: "/sql @pga_advice", RawSQL: "SELECT pga_target_for_estimate/1024/1024 target_mb, pga_target_factor, estd_extra_bytes_rw, estd_pga_cache_hit_percentage, estd_overalloc_count FROM V$PGA_TARGET_ADVICE ORDER BY pga_target_for_estimate"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "PGA管理正常",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "PGA工作区策略AUTO且无over allocation"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "工作区策略状态未知",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "无法确定workarea_size_policy配置"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查当前PGA参数配置", SkillCommand: "/params workarea_size_policy", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('workarea_size_policy','pga_aggregate_target','sort_area_size','hash_area_size')"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"temp_full_ora1652"},
		Tags:     []string{"memory", "pga", "workarea", "sort", "hash"},
		Versions: "9i+",
	}
}

// ruleSharedPoolSubpoolContention detects shared pool subpool latch contention.
// When shared_pool_size is large (>4GB), Oracle splits it into subpools.
// Uneven distribution of objects across subpools can cause concentrated latch waits.
func ruleSharedPoolSubpoolContention() *Rule {
	return &Rule{
		ID:       "MI2-005",
		Name:     "共享池子池Latch争用(Subpool Latch Contention)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "latch: shared pool"},
			{Type: SignalMetric, Key: metricSubpoolLatchWait},
			{Type: SignalKeyword, Key: "subpool"},
			{Type: SignalKeyword, Key: "shared pool latch"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "wait_profile", Field: "latch: shared pool", Op: OpPctGT, Value: 3},
			},
		},
		Tree: &TreeNode{
			Step: "检查shared pool subpool数量和各subpool free memory分布",
			Check: func(ctx *EvalContext) interface{} {
				// Try query first; if unavailable (live mode), use wait pct as proxy.
				result, err := ctx.ExecuteQuery(QuerySubpoolLatchStats, nil)
				if err == nil && result != nil {
					return result
				}
				// Fallback: use latch: shared pool percentage as severity proxy.
				pct := ctx.WaitPct("latch: shared pool")
				if pct > 30 {
					return float64(5) // Treat as severe (>3x ratio)
				}
				if pct > 10 {
					return float64(2) // Treat as moderate (>1x ratio)
				}
				return float64(0) // Low → will hit MatchDefault "subpool正常" branch
			},
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "subpool间free memory差异>3倍,分布不均",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step: "检查是否有大量literal SQL导致特定subpool耗尽",
						Check: func(ctx *EvalContext) interface{} {
							// High latch:shared pool is strong current-state evidence of
							// hard parse storm; cumulative v$sysstat pct can be misleading.
							spPct := ctx.WaitPct("latch: shared pool")
							if spPct >= 50 {
								return float64(90)
							}
							// For moderate cases, try the actual query.
							result, err := ctx.ExecuteQuery(QueryParseStats, nil)
							if err == nil && result != nil {
								if pct := ExtractHardParsePct(result); pct >= 0 {
									return pct
								}
							}
							return ctx.MetricValue("hard_parse_pct")
						},
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "hard parse rate高,literal SQL集中在某些subpool",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "shared pool subpool间内存分布严重不均,hard parse率高导致literal SQL集中在特定subpool,造成latch争用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "设置cursor_sharing=FORCE减少literal SQL", SkillCommand: "/params cursor_sharing", RawSQL: "ALTER SYSTEM SET cursor_sharing = 'FORCE' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "长期:修改应用使用绑定变量", SkillCommand: "/slowsql", RawSQL: "SELECT force_matching_signature, COUNT(*) cnt FROM V$SQL WHERE force_matching_signature > 0 GROUP BY force_matching_signature HAVING COUNT(*) > 50 ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"},
									{Type: ActionFix, Desc: "增大 session_cached_cursors 减少重复软解析开销",
										RawSQL: "ALTER SYSTEM SET session_cached_cursors = 300 SCOPE=SPFILE",
										Risk: "需重启生效", Rollback: "ALTER SYSTEM SET session_cached_cursors = 原值 SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非literal SQL引起,subpool自身分布问题",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "shared pool subpool内存分布不均导致latch争用,非literal SQL引起"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "flush shared pool重新均匀分配", SkillCommand: "/sql @flush_sp", RawSQL: "ALTER SYSTEM FLUSH SHARED_POOL", Risk: "会清除所有缓存执行计划"},
									{Type: ActionInvestigate, Desc: "检查各subpool内存使用详情", SkillCommand: "/sql @subpool_detail", RawSQL: "SELECT ksmssnam name, ksmdsidx subpool#, SUM(ksmsslen)/1024/1024 mb FROM X$KSMSS GROUP BY ksmssnam, ksmdsidx HAVING SUM(ksmsslen) > 10485760 ORDER BY ksmdsidx, mb DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "轻微subpool分布不均",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "shared pool subpool free memory轻微不均,但latch争用已有表现"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控subpool内存变化趋势", SkillCommand: "/sql @subpool_stats", RawSQL: "SELECT ksmssnam, ksmdsidx, SUM(ksmsslen)/1024/1024 mb FROM X$KSMSS WHERE ksmssnam = 'free memory' GROUP BY ksmssnam, ksmdsidx ORDER BY ksmdsidx"},
					},
				},
				{
					Label: "subpool 分布正常 — 检查硬解析和 SP 空闲",
					Match: MatchDefault(),
					Then: &TreeNode{
							Step: "subpool 分布正常时检查硬解析率定位真正瓶颈",
							Check: func(ctx *EvalContext) interface{} {
								result, err := ctx.ExecuteQuery(QueryParseStats, nil)
								if err == nil && result != nil {
									if pct := ExtractHardParsePct(result); pct >= 0 {
										return pct
									}
								}
								if v := ctx.MetricValue("hard_parse_pct"); v > 0 {
									return v
								}
								if ctx.WaitPct("latch: shared pool") > 30 {
									return float64(10) // assume elevated hard parse
								}
								return float64(0)
							},
							Branches: []Branch{
								{
									Label:    "硬解析率 > 5% — 硬解析导致 shared pool latch 争用",
									Match:    MatchGT(5),
									Severity: SeverityHigh,
									Findings: []Finding{
										{Desc: "shared pool subpool 分布正常，但硬解析率偏高（> 5%）"},
										{Desc: "硬解析产生的 shared pool 分配/释放操作是 latch 争用的根因"},
									},
									Actions: []Action{
										{Type: ActionFix, Desc: "使用绑定变量减少硬解析",
											SkillCommand: "/ash top_sql event='latch: shared pool'",
											RawSQL:       "SELECT sql_id, executions, parse_calls, version_count FROM v$sqlarea WHERE parse_calls > 100 AND executions <= parse_calls ORDER BY parse_calls DESC FETCH FIRST 20 ROWS ONLY",
											Risk: "需要修改应用代码", Rollback: "无"},
										{Type: ActionFix, Desc: "临时启用 CURSOR_SHARING=FORCE 缓解",
											RawSQL: "ALTER SYSTEM SET CURSOR_SHARING='FORCE' SCOPE=BOTH",
											Risk: "可能导致部分执行计划次优", Rollback: "ALTER SYSTEM SET CURSOR_SHARING='EXACT' SCOPE=BOTH"},
									},
								},
								{
									Label:    "硬解析正常 — latch: shared pool 占比检查",
									Match:    MatchDefault(),
									Then: &TreeNode{
										Step:  "检查 latch: shared pool 是否为主要等待",
										Check: func(ctx *EvalContext) interface{} { return ctx.WaitPct("latch: shared pool") },
										Branches: []Branch{
											{
												Label:    "占比 > 10% — shared pool latch 争用",
												Match:    MatchGT(10),
												Severity: SeverityMedium,
												Findings: []Finding{{Desc: "shared pool subpool 分布正常且硬解析正常，但 latch: shared pool 争用明显，可能是 shared pool 碎片化或大量 PL/SQL 对象加载"}},
												Actions: []Action{
													{Type: ActionInvestigate, Desc: "检查 shared pool 碎片",
														SkillCommand: "/sga check",
														RawSQL: "SELECT pool, name, ROUND(bytes/1024/1024,1) mb FROM v$sgastat WHERE pool='shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY",
														Risk: "无", Rollback: "无"},
												},
											},
											{
												Label:    "占比低 — 非主要瓶颈",
												Match:    MatchDefault(),
												Severity: SeverityLow,
												Findings: []Finding{{Desc: "latch: shared pool 占比低，subpool 分布正常，非当前主要瓶颈"}},
											},
										},
									},
								},
							},
					},
				},
			},
		},
		CausedBy: []string{"hard_parse_storm"},
		CausesOf: []string{"ora_4031_emergency"},
		Tags:     []string{"memory", "shared_pool", "subpool", "latch"},
		Versions: "10g+",
	}
}

// ruleLibraryCacheFragmentation detects library cache fragmentation and excessive
// reloads. High reload ratio means cursors are being aged out and re-parsed,
// wasting CPU and causing latch contention.
func ruleLibraryCacheFragmentation() *Rule {
	return &Rule{
		ID:       "MI2-006",
		Name:     "库缓存碎片化/重加载(Library Cache Fragmentation)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricLibCacheReloadRatio},
			{Type: SignalWaitEvent, Key: "library cache lock"},
			{Type: SignalKeyword, Key: "library cache"},
			{Type: SignalKeyword, Key: "reload"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricLibCacheReloadRatio, Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查library cache命中率和reload/invalidation比率",
			Query: QueryLibCacheReloads,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "reload ratio > 5%,严重碎片化",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查shared pool free memory确认是否空间不足导致淘汰",
						Query: QuerySPFreeMemory,
						Branches: []Branch{
							{
								Match:    MatchLT(10),
								Label:    "shared pool free < 10%,空间不足导致频繁淘汰",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Library cache reload ratio > 5%,shared pool free < 10%,cursor被频繁淘汰再重加载,大量硬解析消耗CPU"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大shared_pool_size减少cursor淘汰", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "使用DBMS_SHARED_POOL.KEEP固定重要cursor", SkillCommand: "/sql @pin_cursors", RawSQL: "BEGIN DBMS_SHARED_POOL.KEEP('{sql_address}', 'C'); END;"},
									{Type: ActionInvestigate, Desc: "查看library cache统计", SkillCommand: "/sql @lib_cache_stats", RawSQL: "SELECT namespace, gets, gethits, gethitratio, pins, pinhits, pinhitratio, reloads, invalidations FROM V$LIBRARYCACHE ORDER BY reloads DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "shared pool有空间但仍有大量reload",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Library cache reload高但shared pool有空余,可能为大量invalidation(DDL/统计信息收集)导致cursor失效"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查invalidation原因(DDL/gather stats)", SkillCommand: "/sql @lib_cache_invalidation", RawSQL: "SELECT namespace, invalidations, reloads FROM V$LIBRARYCACHE WHERE invalidations > 0 ORDER BY invalidations DESC"},
									{Type: ActionFix, Desc: "调整统计信息收集时间避开业务高峰", SkillCommand: "/sql @stats_job", RawSQL: "SELECT owner, job_name, enabled, last_start_date FROM DBA_SCHEDULER_JOBS WHERE job_name LIKE '%STATS%'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "reload ratio 1-5%,轻微碎片",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Library cache reload ratio 1-5%,存在轻微碎片化,部分cursor被淘汰后重加载"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查library cache各namespace的reload情况", SkillCommand: "/sql @lib_cache_stats", RawSQL: "SELECT namespace, gets, gethitratio, reloads, invalidations FROM V$LIBRARYCACHE WHERE reloads > 0 ORDER BY reloads DESC"},
						{Type: ActionPrevent, Desc: "监控shared pool使用趋势", SkillCommand: "/sql @sp_trend", RawSQL: "SELECT name, bytes/1024/1024 mb FROM V$SGASTAT WHERE pool = 'shared pool' AND name = 'free memory'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Library cache健康",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Library cache reload ratio正常,cursor缓存命中率良好"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"ora_4031_emergency", "hard_parse_storm"},
		CausesOf: []string{},
		Tags:     []string{"memory", "library_cache", "reload", "fragmentation", "cursor"},
		Versions: "9i+",
	}
}

// ruleSGATargetTooLow detects sga_target set lower than the sum of all
// minimum SGA component sizes (db_cache_size + shared_pool_size + large_pool_size
// + java_pool_size + streams_pool_size). This prevents ASMM from functioning
// properly and can cause ORA-04031.
func ruleSGATargetTooLow() *Rule {
	return &Rule{
		ID:       "MI2-007",
		Name:     "SGA_TARGET低于组件最小值之和(SGA Target Too Low)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricSGATargetMB},
			{Type: SignalMetric, Key: metricSGAMinimumsSumMB},
			{Type: SignalKeyword, Key: "sga_target"},
			{Type: SignalKeyword, Key: "sga too small"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSGATargetMB, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查sga_target与各SGA组件minimum_size之和对比",
			Query: QuerySGATargetComponents,
			Branches: []Branch{
				{
					Match:    MatchLT(0),
					Label:    "sga_target < 组件最小值之和,ASMM无弹性",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查差额有多大,评估影响",
						Query: QuerySGATargetComponents,
						Branches: []Branch{
							{
								Match:    MatchLT(-500),
								Label:    "差额超过500MB,严重不足",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "sga_target比各SGA组件min_size之和小500MB以上,ASMM完全无法动态调整内存,各组件被锁定在最小值,极易发生ORA-04031"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大sga_target至少超过minimums之和20%", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET sga_target = '{new_size}' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "或降低各组件的最小值释放弹性", SkillCommand: "/sql @sga_components", RawSQL: "SELECT component, current_size/1024/1024 current_mb, min_size/1024/1024 min_mb, user_specified_size/1024/1024 user_mb FROM V$SGA_DYNAMIC_COMPONENTS ORDER BY current_size DESC"},
									{Type: ActionInvestigate, Desc: "查看当前SGA各组件分配", SkillCommand: "/sql @sga_detail", RawSQL: "SELECT component, current_size/1024/1024 mb, min_size/1024/1024 min_mb, granule_size/1024/1024 gran_mb FROM V$SGA_DYNAMIC_COMPONENTS WHERE current_size > 0 ORDER BY current_size DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "差额较小,ASMM弹性受限但可工作",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "sga_target略低于组件最小值之和,ASMM弹性受限,负载变化时可能出现内存不足"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大sga_target留出20%弹性空间", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET sga_target = '{new_size}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchBetween(0, 500),
					Label:    "sga_target略高于组件最小值,弹性小",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "sga_target仅略高于组件最小值之和,ASMM可调整空间不足500MB,建议留出更多弹性"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "建议sga_target至少超过min之和20-30%", SkillCommand: "/params sga_target", RawSQL: "SELECT component, current_size/1024/1024 current_mb, min_size/1024/1024 min_mb FROM V$SGA_DYNAMIC_COMPONENTS WHERE current_size > 0 ORDER BY current_size DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "sga_target配置合理,ASMM有足够弹性",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "sga_target高于SGA组件最小值之和,ASMM有充足弹性空间"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"ora_4031_emergency", "MI2-006"},
		Tags:     []string{"memory", "sga", "asmm", "sga_target", "sizing"},
		Versions: "10g+",
	}
}

// rulePGAMultipassSQL detects individual SQL statements causing multipass
// workarea execution (sort/hash join/bitmap). Multipass is extremely slow
// and means the operation spills to temp twice.
func rulePGAMultipassSQL() *Rule {
	return &Rule{
		ID:       "MI2-008",
		Name:     "SQL引起PGA Multipass(Individual SQL Multipass)",
		Category: "memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPGAMultipassCount},
			{Type: SignalMetric, Key: metricPGAMultipassSQLCount},
			{Type: SignalKeyword, Key: "multipass"},
			{Type: SignalKeyword, Key: "pga multipass"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPGAMultipassCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$SQL_WORKAREA_ACTIVE中multipass执行",
			Query: QueryPGAMultipassSQL,
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "多个SQL发生multipass,系统性PGA不足",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查pga_aggregate_target设置",
						Query: QueryPGAAdvice,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "PGA Advice建议增大target可消除multipass",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "多个SQL发生multipass工作区执行(排序/哈希连接溢出到temp两次),PGA Advice建议增大pga_aggregate_target"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大pga_aggregate_target消除multipass", SkillCommand: "/params pga_aggregate_target", RawSQL: "ALTER SYSTEM SET pga_aggregate_target = '{advised_size}' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "查看当前multipass SQL详情", SkillCommand: "/sql @multipass_sql", RawSQL: "SELECT sql_id, operation_type, policy, estimated_optimal_size/1024/1024 optimal_mb, estimated_onepass_size/1024/1024 onepass_mb, actual_mem_used/1024/1024 actual_mb, number_passes, tempseg_size/1024/1024 temp_mb FROM V$SQL_WORKAREA_ACTIVE WHERE number_passes > 1"},
									{Type: ActionFix, Desc: "优化multipass SQL减少排序/哈希数据量", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT sql_id, child_number, operation_type, estimated_optimal_size/1024/1024 opt_mb, number_passes FROM V$SQL_WORKAREA WHERE number_passes > 1 ORDER BY number_passes DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "PGA target已较大,SQL本身需优化",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "多个SQL发生multipass但pga_aggregate_target已较大,需优化SQL减少排序/哈希连接数据量"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "优化multipass SQL的执行计划", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT sql_id, operation_type, number_passes, tempseg_size/1024/1024 temp_mb FROM V$SQL_WORKAREA_ACTIVE WHERE number_passes > 1 ORDER BY tempseg_size DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量SQL发生multipass",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "个别SQL发生multipass工作区执行,排序或哈希操作数据量超出onepass内存分配"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看multipass SQL详情并评估优化", SkillCommand: "/sql @multipass_detail", RawSQL: "SELECT wa.sql_id, wa.operation_type, wa.policy, wa.estimated_optimal_size/1024/1024 optimal_mb, wa.actual_mem_used/1024/1024 actual_mb, wa.number_passes, wa.tempseg_size/1024/1024 temp_mb FROM V$SQL_WORKAREA_ACTIVE wa WHERE wa.number_passes > 1 ORDER BY wa.tempseg_size DESC"},
						{Type: ActionFix, Desc: "对问题SQL考虑添加index或改写避免大排序", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT sql_id, sql_text FROM V$SQL WHERE sql_id IN (SELECT sql_id FROM V$SQL_WORKAREA_ACTIVE WHERE number_passes > 1)"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无multipass工作区执行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无SQL发生multipass工作区执行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"MI2-004"},
		CausesOf: []string{"temp_full_ora1652"},
		Tags:     []string{"memory", "pga", "multipass", "sort", "hash_join", "temp"},
		Versions: "9i+",
	}
}

// ─── IO Rules (7) ────────────────────────────────────────────────────────────

// ruleRedoLogMultiplexing detects redo log files that are not multiplexed.
// Without multiplexing, a single disk failure can lose redo data and
// make crash recovery impossible.
func ruleRedoLogMultiplexing() *Rule {
	return &Rule{
		ID:       "MI2-009",
		Name:     "Redo Log未多路复用(Redo Log Not Multiplexed)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRedoLogMultiplexed},
			{Type: SignalKeyword, Key: "redo multiplex"},
			{Type: SignalKeyword, Key: "redo log member"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRedoLogMultiplexed, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$LOGFILE中每个group的member数量",
			Query: QueryRedoLogMembers,
			Branches: []Branch{
				{
					Match:    MatchEquals("SINGLE"),
					Label:    "所有redo log group均为单member",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否使用ASM(ASM自带冗余)",
						Query: QueryRedoLogMembers,
						Branches: []Branch{
							{
								Match:    MatchEquals("ASM"),
								Label:    "使用ASM存储,ASM冗余提供保护",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Redo log单member但存储在ASM上,ASM NORMAL/HIGH冗余提供磁盘级保护,但Oracle仍建议OS级多路复用"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "建议添加第二个member到不同disk group", SkillCommand: "/sql @add_logfile_member", RawSQL: "ALTER DATABASE ADD LOGFILE MEMBER '{+OTHER_DG/redo_g{n}_b.log}' TO GROUP {n}"},
									{Type: ActionInvestigate, Desc: "确认ASM冗余级别", SkillCommand: "/sql @asm_dg_info", RawSQL: "SELECT name, type, total_mb, free_mb FROM V$ASM_DISKGROUP"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "文件系统存储且无多路复用,高风险",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Redo log文件未多路复用且非ASM存储,单点磁盘故障将导致redo丢失,crash recovery失败,数据库可能无法打开"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即为每个group添加第二个member到不同磁盘", SkillCommand: "/sql @add_logfile_member", RawSQL: "ALTER DATABASE ADD LOGFILE MEMBER '/u02/oradata/{db}/redo{n}b.log' TO GROUP {n}"},
									{Type: ActionInvestigate, Desc: "查看当前redo log配置", SkillCommand: "/sql @redo_config", RawSQL: "SELECT l.group#, l.members, l.bytes/1024/1024 size_mb, l.status, f.member FROM V$LOG l JOIN V$LOGFILE f ON l.group# = f.group# ORDER BY l.group#, f.member"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("PARTIAL"),
					Label:    "部分group未多路复用",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "部分redo log group仅有单member,存在数据保护缺口"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "为单member的group添加额外member", SkillCommand: "/sql @add_logfile_member", RawSQL: "SELECT group#, members FROM V$LOG WHERE members = 1"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有redo log已多路复用",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有redo log group均有多个member,数据保护良好"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "redo", "multiplex", "data_protection"},
		Versions: "9i+",
	}
}

// ruleControlfileMultiplex detects control files that are not multiplexed.
// Loss of all control file copies requires full database restore or manual
// controlfile recreation.
func ruleControlfileMultiplex() *Rule {
	return &Rule{
		ID:       "MI2-010",
		Name:     "控制文件未多路复用(Controlfile Not Multiplexed)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricControlfileMultiplexed},
			{Type: SignalKeyword, Key: "controlfile multiplex"},
			{Type: SignalKeyword, Key: "control file"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricControlfileMultiplexed, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$CONTROLFILE中控制文件副本数",
			Query: QueryControlfileLocations,
			Branches: []Branch{
				{
					Match:    MatchEquals("SINGLE"),
					Label:    "仅有一个控制文件副本",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查控制文件是否在ASM上",
						Query: QueryControlfileLocations,
						Branches: []Branch{
							{
								Match:    MatchEquals("ASM"),
								Label:    "ASM存储,有磁盘级冗余",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "仅有一个控制文件,虽在ASM上有磁盘冗余,但Oracle最佳实践建议至少3份控制文件副本在不同位置"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "在不同ASM disk group添加控制文件副本", SkillCommand: "/params control_files", RawSQL: "-- 1. ALTER SYSTEM SET control_files='+DATA/ctrl01.ctl','+FRA/ctrl02.ctl' SCOPE=SPFILE;\n-- 2. SHUTDOWN IMMEDIATE;\n-- 3. RMAN> RESTORE CONTROLFILE FROM '+DATA/ctrl01.ctl';\n-- 4. STARTUP;", Risk: "需停机修改"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "文件系统单副本,极高风险",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "仅有一个控制文件副本且存储在文件系统上,磁盘故障将导致无法启动数据库,需要从备份恢复或手动重建控制文件"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即添加控制文件副本到不同磁盘", SkillCommand: "/params control_files", RawSQL: "-- 1. ALTER SYSTEM SET control_files='/u01/oradata/{db}/control01.ctl','/u02/oradata/{db}/control02.ctl','/u03/oradata/{db}/control03.ctl' SCOPE=SPFILE;\n-- 2. SHUTDOWN IMMEDIATE;\n-- 3. OS copy control file to new locations;\n-- 4. STARTUP;", Risk: "需停机操作"},
									{Type: ActionInvestigate, Desc: "查看当前控制文件位置", SkillCommand: "/sql @controlfile_info", RawSQL: "SELECT name, status, block_size, file_size_blks FROM V$CONTROLFILE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("TWO"),
					Label:    "有两个副本,建议增至三个",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "控制文件有2个副本,Oracle最佳实践建议至少3个副本分布在不同存储位置"},
					},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "添加第三个控制文件副本", SkillCommand: "/params control_files", RawSQL: "SELECT name FROM V$CONTROLFILE"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "控制文件多路复用正常(>=3副本)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "控制文件已正确多路复用,有3个或以上副本"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "controlfile", "multiplex", "data_protection"},
		Versions: "9i+",
	}
}

// ruleASyncIOVerification detects async IO configuration that falls back to
// synchronous IO. Even when filesystemio_options=ASYNCH is set, OS-level issues
// (kernel params, filesystem type) can cause Oracle to silently fall back to sync.
func ruleASyncIOVerification() *Rule {
	return &Rule{
		ID:       "MI2-011",
		Name:     "异步IO回退为同步IO(Async IO Fallback to Sync)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricAsyncIOFallbackSync},
			{Type: SignalMetric, Key: metricFilesystemIOOptions},
			{Type: SignalKeyword, Key: "async io"},
			{Type: SignalKeyword, Key: "filesystemio"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricAsyncIOFallbackSync, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查filesystemio_options参数和实际IO模式",
			Query: QueryAsyncIOStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("SYNC_FALLBACK"),
					Label:    "配置为ASYNCH但实际使用同步IO",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查数据文件的asynch_io列确认哪些文件回退",
						Query: QueryAsyncIOStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    ">50%的数据文件使用同步IO",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "filesystemio_options=ASYNCH但超过50%的数据文件实际使用同步IO,可能为OS内核参数(aio-max-nr)不足或文件系统不支持AIO"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查OS AIO配置", SkillCommand: "/sql @os_aio_check", RawSQL: "-- Linux: cat /proc/sys/fs/aio-max-nr; cat /proc/sys/fs/aio-nr\n-- 如果aio-nr接近aio-max-nr,需要增大: sysctl -w fs.aio-max-nr=1048576"},
									{Type: ActionInvestigate, Desc: "检查哪些数据文件使用同步IO", SkillCommand: "/sql @asynch_check", RawSQL: "SELECT file#, name, asynch_io FROM V$DATAFILE"},
									{Type: ActionFix, Desc: "确认文件系统类型支持AIO(ext4/xfs支持, NFS需特殊配置)", SkillCommand: "/sql @filesystemio", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name = 'filesystemio_options'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "少数文件回退到同步IO",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "少数数据文件回退为同步IO,可能为特定文件系统挂载选项问题"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "确认回退同步IO的具体文件和所在文件系统", SkillCommand: "/sql @asynch_check", RawSQL: "SELECT file#, name, asynch_io FROM V$DATAFILE WHERE asynch_io = 'OFF'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("NONE"),
					Label:    "filesystemio_options=NONE,未启用异步IO",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "filesystemio_options=NONE(或DIRECTIO),未启用异步IO,所有数据文件IO使用同步模式,影响并发IO性能"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "设置filesystemio_options=SETALL启用AIO+DIO", SkillCommand: "/params filesystemio_options", RawSQL: "ALTER SYSTEM SET filesystemio_options = 'SETALL' SCOPE=SPFILE", Risk: "需重启实例,确认OS支持AIO", Rollback: "ALTER SYSTEM SET filesystemio_options = 'NONE' SCOPE=SPFILE"},
						{Type: ActionInvestigate, Desc: "确认OS AIO支持情况", SkillCommand: "/sql @os_info", RawSQL: "SELECT stat_name, value FROM V$OSSTAT WHERE stat_name LIKE '%CPU%' OR stat_name LIKE '%PHYSICAL%'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "异步IO配置正常且实际生效",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "异步IO正确配置且实际生效"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"db_file_sequential_read", "db_file_scattered_read"},
		Tags:     []string{"io_storage", "async_io", "filesystem", "performance"},
		Versions: "10g+",
	}
}

// ruleIOCalibrationBaseline detects missing DBMS_RESOURCE_MANAGER.CALIBRATE_IO
// baseline. Without IO calibration, Oracle cannot properly estimate IO costs
// for optimizer decisions and resource management.
func ruleIOCalibrationBaseline() *Rule {
	return &Rule{
		ID:       "MI2-012",
		Name:     "IO校准基线缺失(IO Calibration Missing)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIOCalibrated},
			{Type: SignalKeyword, Key: "io calibration"},
			{Type: SignalKeyword, Key: "calibrate_io"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIOCalibrated, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DBA_RSRC_IO_CALIBRATE中是否有校准数据",
			Query: QueryIOCalibration,
			Branches: []Branch{
				{
					Match:    MatchEquals("NO_DATA"),
					Label:    "从未执行过IO校准",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查是否使用了Resource Manager IO限制功能",
						Query: QueryIOCalibration,
						Branches: []Branch{
							{
								Match:    MatchEquals("RM_IO_USED"),
								Label:    "使用了RM IO限制但无校准基线",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "配置了Resource Manager IO限制但从未执行IO校准,RM无法准确限制IO资源,可能过度限制或限制不足"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "执行IO校准建立基线", SkillCommand: "/sql @io_calibrate", RawSQL: "SET SERVEROUTPUT ON\nDECLARE\n  v_lat INTEGER; v_iops INTEGER; v_mbps INTEGER;\nBEGIN\n  DBMS_RESOURCE_MANAGER.CALIBRATE_IO(num_physical_disks => {disk_count}, max_latency => 10, max_iops => v_iops, max_mbps => v_mbps, actual_latency => v_lat);\n  DBMS_OUTPUT.PUT_LINE('IOPS=' || v_iops || ' MBPS=' || v_mbps || ' LAT=' || v_lat);\nEND;", Risk: "校准过程会产生大量IO,建议在维护窗口执行"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未使用RM IO限制,校准为可选",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "未执行IO校准,如需使用Resource Manager IO限制或希望优化器有IO成本参考,建议执行校准"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "在维护窗口执行IO校准作为基线", SkillCommand: "/sql @io_calibrate", RawSQL: "-- 在维护窗口执行:\nBEGIN DBMS_RESOURCE_MANAGER.CALIBRATE_IO({disk_count}, 10, :iops, :mbps, :lat); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("STALE"),
					Label:    "IO校准数据过旧(>6个月)",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "IO校准数据超过6个月未更新,存储环境可能已变更(扩容/迁移),校准结果不再准确"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "重新执行IO校准更新基线", SkillCommand: "/sql @io_calibrate", RawSQL: "SELECT max_iops, max_mbps, max_pmbps, latency, num_physical_disks, calibration_time FROM DBA_RSRC_IO_CALIBRATE"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "IO校准数据有效",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "IO校准数据存在且较新"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "calibration", "resource_manager", "baseline"},
		Versions: "11g+",
	}
}

// ruleFlashCacheHitRatio detects low Flash Cache (Database Smart Flash Cache)
// hit ratio (<80%), indicating the flash cache may not be effective for the
// current workload.
func ruleFlashCacheHitRatio() *Rule {
	return &Rule{
		ID:       "MI2-013",
		Name:     "Flash Cache命中率低(Flash Cache Hit Ratio < 80%)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFlashCacheHitRatio},
			{Type: SignalKeyword, Key: "flash cache hit"},
			{Type: SignalKeyword, Key: "flash cache ratio"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFlashCacheHitRatio, Op: OpLT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Flash Cache命中统计",
			Query: QueryFlashCacheStats,
			Branches: []Branch{
				{
					Match:    MatchLT(50),
					Label:    "Flash Cache命中率 < 50%,几乎无效",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查Flash Cache大小是否合理",
						Query: QueryFlashCacheStats,
						Branches: []Branch{
							{
								Match:    MatchLT(10),
								Label:    "Flash Cache远小于活跃数据集",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Flash Cache命中率 < 50%,Flash Cache大小远小于活跃数据集,大部分读取仍从磁盘完成"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大db_flash_cache_size或将不适合的对象排除", SkillCommand: "/params db_flash_cache_size", RawSQL: "ALTER SYSTEM SET db_flash_cache_size = '{new_size}' SCOPE=SPFILE"},
									{Type: ActionInvestigate, Desc: "检查Flash Cache空间使用分布", SkillCommand: "/sql @flash_objects", RawSQL: "SELECT * FROM V$FLASH_CACHE_STAT"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Flash Cache大小合理但命中率低",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Flash Cache大小合理但命中率低,可能工作负载随机IO多或大表扫描污染缓存"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将大表扫描排除出Flash Cache", SkillCommand: "/sql @exclude_flash", RawSQL: "ALTER TABLE {schema}.{table} STORAGE (FLASH_CACHE NONE)"},
									{Type: ActionInvestigate, Desc: "分析Flash Cache中的热块分布", SkillCommand: "/sql @flash_cache_detail", RawSQL: "SELECT * FROM V$FLASH_CACHE_STAT"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(80),
					Label:    "Flash Cache命中率 50-80%,可优化",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Flash Cache命中率在50-80%,有优化空间,可通过排除不适合缓存的对象提高命中率"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析哪些对象占用Flash Cache但命中率低", SkillCommand: "/sql @flash_cache_objects", RawSQL: "SELECT * FROM V$FLASH_CACHE_STAT"},
						{Type: ActionFix, Desc: "将全表扫描大表设为FLASH_CACHE NONE", SkillCommand: "/sql @flash_cache_tune", RawSQL: "-- 查看大表: SELECT owner, segment_name, bytes/1024/1024/1024 gb FROM DBA_SEGMENTS WHERE segment_type = 'TABLE' AND bytes > 10*1024*1024*1024 ORDER BY bytes DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Flash Cache命中率正常(>=80%)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flash Cache命中率 >= 80%,缓存效果良好"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "flash_cache", "hit_ratio", "ssd"},
		Versions: "11.2+",
	}
}

// ruleIOSchedulerChoice detects suboptimal Linux IO scheduler configuration for
// Oracle databases. SSDs should use 'noop' or 'none'; spinning disks should use
// 'deadline' or 'mq-deadline'. CFQ (default in some distros) is suboptimal for DB.
func ruleIOSchedulerChoice() *Rule {
	return &Rule{
		ID:       "MI2-014",
		Name:     "IO调度器选择不当(IO Scheduler Mismatch)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIOSchedulerType},
			{Type: SignalKeyword, Key: "io scheduler"},
			{Type: SignalKeyword, Key: "cfq"},
			{Type: SignalKeyword, Key: "noop"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIOSchedulerType, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Linux IO调度器类型",
			Query: QueryIOScheduler,
			Branches: []Branch{
				{
					Match:    MatchEquals("CFQ"),
					Label:    "使用CFQ调度器(不推荐用于Oracle)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查底层存储类型(SSD vs HDD)",
						Query: QueryIOScheduler,
						Branches: []Branch{
							{
								Match:    MatchEquals("SSD"),
								Label:    "SSD + CFQ,严重不匹配",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SSD存储使用CFQ IO调度器,CFQ的队列和时间片机制对SSD完全无意义,增加延迟,应使用noop/none"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将IO调度器改为noop(SSD最优)", SkillCommand: "/sql @os_io_scheduler", RawSQL: "-- echo noop > /sys/block/{device}/queue/scheduler\n-- 永久生效: /etc/udev/rules.d/60-scheduler.rules 或 grub中elevator=noop"},
									{Type: ActionInvestigate, Desc: "确认Oracle数据文件所在块设备", SkillCommand: "/sql @datafile_locations", RawSQL: "SELECT file#, name FROM V$DATAFILE ORDER BY file#"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "HDD + CFQ,建议改为deadline",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "HDD存储使用CFQ调度器,Oracle建议使用deadline调度器,对数据库随机IO更友好"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将IO调度器改为deadline", SkillCommand: "/sql @os_io_scheduler", RawSQL: "-- echo deadline > /sys/block/{device}/queue/scheduler\n-- 永久生效: grub中elevator=deadline"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("NOOP_ON_HDD"),
					Label:    "HDD使用noop,缺少IO优化",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "HDD存储使用noop调度器,noop不做任何IO重排序,HDD随机IO性能会下降,建议用deadline"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "将IO调度器改为deadline以优化HDD随机IO", SkillCommand: "/sql @os_io_scheduler", RawSQL: "-- echo deadline > /sys/block/{device}/queue/scheduler"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "IO调度器配置合理",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "IO调度器与存储类型匹配,配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"io_storage", "scheduler", "linux", "ssd", "performance"},
		Versions: "10g+",
	}
}

// ruleMultipathIOConfig detects multipath IO not configured for ASM disks.
// Without multipath, a single HBA/path failure makes ASM disks unavailable,
// and load balancing across paths is lost.
func ruleMultipathIOConfig() *Rule {
	return &Rule{
		ID:       "MI2-015",
		Name:     "多路径IO未配置(Multipath IO for ASM)",
		Category: "io_storage",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricMultipathIOConfigured},
			{Type: SignalKeyword, Key: "multipath"},
			{Type: SignalKeyword, Key: "dm-multipath"},
			{Type: SignalKeyword, Key: "asm multipath"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricMultipathIOConfigured, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ASM磁盘是否通过multipath设备访问",
			Query: QueryMultipathConfig,
			Branches: []Branch{
				{
					Match:    MatchEquals("NO_MULTIPATH"),
					Label:    "ASM磁盘未使用multipath",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否为SAN存储(SAN应该配multipath)",
						Query: QueryMultipathConfig,
						Branches: []Branch{
							{
								Match:    MatchEquals("SAN"),
								Label:    "SAN存储但无multipath,高风险",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM使用SAN存储但未配置多路径IO(dm-multipath/powerpath),单一HBA/FC路径故障将导致所有ASM磁盘不可用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "安装和配置dm-multipath", SkillCommand: "/sql @asm_disk_paths", RawSQL: "-- 1. yum install device-mapper-multipath\n-- 2. mpathconf --enable\n-- 3. 配置/etc/multipath.conf\n-- 4. systemctl restart multipathd", Risk: "需要存储管理员配合配置,可能需停机维护"},
									{Type: ActionInvestigate, Desc: "查看当前ASM磁盘路径", SkillCommand: "/sql @asm_disk_paths", RawSQL: "SELECT name, path, mount_status, mode_status FROM V$ASM_DISK ORDER BY group_number, disk_number"},
									{Type: ActionPrevent, Desc: "配置后需使用udev rules稳定设备名称", SkillCommand: "/sql @asm_discovery", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name = 'asm_diskstring'"},
								},
							},
							{
								Match:    MatchEquals("LOCAL"),
								Label:    "本地存储,multipath不适用",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "ASM使用本地存储,多路径IO不适用于本地磁盘"},
								},
								Actions: nil,
							},
							{
								Match:    MatchDefault(),
								Label:    "存储类型未知,建议确认",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "无法确认ASM存储类型,如使用SAN/NAS存储强烈建议配置多路径IO"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "确认ASM磁盘底层存储类型", SkillCommand: "/sql @asm_disk_paths", RawSQL: "SELECT name, path, header_status, mount_status FROM V$ASM_DISK ORDER BY group_number, disk_number"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("PARTIAL"),
					Label:    "部分ASM磁盘未通过multipath",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "部分ASM磁盘未通过multipath设备访问,存在路径不一致风险,可能导致部分磁盘在路径故障时不可用"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "统一所有ASM磁盘使用multipath设备", SkillCommand: "/sql @asm_disk_paths", RawSQL: "SELECT name, path, mount_status FROM V$ASM_DISK WHERE path NOT LIKE '/dev/mapper/%' AND path NOT LIKE '/dev/dm-%' ORDER BY group_number"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有ASM磁盘已配置multipath",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有ASM磁盘通过multipath设备访问,路径冗余和负载均衡已配置"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"asm_disk_failure"},
		Tags:     []string{"io_storage", "multipath", "asm", "san", "ha"},
		Versions: "10g+",
	}
}
