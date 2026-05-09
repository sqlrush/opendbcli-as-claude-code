/*-------------------------------------------------------------------------
 *
 * rules_ha_ext.go
 *	  Oracle rule engine — HA-extension rules (replication lag, standby gap, broker config drift).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_ha_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── HA Extended Query IDs ──────────────────────────────────────────────────

const (
	QueryOGGStatus        QueryID = "ogg_status"
	QueryOGGTrailSpace    QueryID = "ogg_trail_space"
	QueryOGGConflict      QueryID = "ogg_conflict"
	QueryOGGCheckpoint    QueryID = "ogg_checkpoint"
	QueryASMDiskgroup     QueryID = "asm_diskgroup"
	QueryASMRebalance     QueryID = "asm_rebalance"
	QueryASMAUSize        QueryID = "asm_au_size"
	QueryASMDiskPerf      QueryID = "asm_disk_perf"
	QueryRMANConfig       QueryID = "rman_config"
	// QueryRMANBackupStatus defined in rules_operations.go
	QueryRMANCrosscheck   QueryID = "rman_crosscheck"
	QueryFlashbackStatus  QueryID = "flashback_status"
	QueryFlashbackLogs    QueryID = "flashback_logs"
	// QueryFRAUsage defined in rules_operations.go
	QueryRecyclebinUsage  QueryID = "recyclebin_usage"
	QueryFDAStatus        QueryID = "fda_status"
)

// ─── Metric name constants for HA extended rules ────────────────────────────

const (
	metricOGGExtractStatus   = "ogg_extract_status"
	metricOGGReplicatLagSec  = "ogg_replicat_lag_sec"
	metricOGGTrailDiskPct    = "ogg_trail_disk_usage_pct"
	metricOGGCDRConflicts    = "ogg_cdr_conflict_count"
	metricOGGCheckpointLag   = "ogg_checkpoint_lag_sec"

	metricASMDiskgroupUsePct    = "asm_diskgroup_usage_pct"
	metricASMRebalanceProgress  = "asm_rebalance_progress"
	metricASMAUSizeMB           = "asm_au_size_mb"
	metricASMRedundancyType     = "asm_redundancy_type"
	metricASMDiskLatencyMs      = "asm_disk_latency_ms"

	metricRMANBCTEnabled       = "rman_bct_enabled"
	metricRMANIncrMerge        = "rman_incremental_merge"
	metricRMANParallelism      = "rman_parallelism"
	metricRMANObsoleteCount    = "rman_obsolete_backup_count"
	metricRMANEncryption       = "rman_backup_encrypted"

	metricFlashbackEnabled      = "flashback_database_enabled"
	metricFlashbackRetention    = "flashback_retention_target_min"
	metricFlashbackLogSpacePct  = "flashback_log_fra_pct"
	metricRecyclebinSpaceMB     = "recyclebin_space_mb"
	metricFDAEnabled            = "fda_enabled"
)

// ═══════════════════════════════════════════════════════════════════════════
// GoldenGate Rules (5) — HA2-001 .. HA2-005
// ═══════════════════════════════════════════════════════════════════════════

func haGoldenGateRules() []*Rule {
	return []*Rule{
		ruleOGGExtractAbend(),
		ruleOGGReplicatLag(),
		ruleOGGTrailFileGrowth(),
		ruleOGGCDRConflict(),
		ruleOGGCheckpointLag(),
	}
}

// ruleOGGExtractAbend detects OGG Extract process ABENDED state, commonly
// caused by missing archive logs, supplemental logging not enabled, or
// LogMiner registration failures.
func ruleOGGExtractAbend() *Rule {
	return &Rule{
		ID:       "HA2-001",
		Name:     "GoldenGate Extract进程ABEND",
		Category: "ha_ogg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOGGExtractStatus},
			{Type: SignalKeyword, Key: "extract abend"},
			{Type: SignalKeyword, Key: "goldengate extract"},
			{Type: SignalKeyword, Key: "OGG abend"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOGGExtractStatus, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Extract进程状态和ggserr.log错误信息",
			Query: QueryOGGStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("ABENDED"),
					Label:    "Extract进程ABENDED",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查ABEND原因类别(归档缺失/supplemental log/注册)",
						Query: QueryArchiveStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("MISSING"),
								Label:    "所需归档日志已被删除",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "GoldenGate Extract ABEND:所需归档日志已被删除,Extract无法继续挖掘redo"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "确认Extract所需的起始SCN和对应归档", SkillCommand: "/sql @ogg_extract_scn", RawSQL: "SELECT * FROM DBA_REGISTERED_ARCHIVED_LOG WHERE consumer_name LIKE '%OGG%' ORDER BY first_scn DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "从RMAN恢复缺失的归档日志", SkillCommand: "/sql @rman_restore_arch", RawSQL: "-- RMAN: RESTORE ARCHIVELOG FROM SEQUENCE {low} UNTIL SEQUENCE {high} THREAD 1;"},
									{Type: ActionFix, Desc: "若归档不可恢复,使用ADD TRANDATA重新初始化", SkillCommand: "/sql @ogg_reinit", RawSQL: "-- GGSCI: ALTER EXTRACT {name}, BEGIN NOW; START EXTRACT {name}", Risk: "重新初始化可能丢失部分增量数据,需验证目标端数据一致性", Rollback: "STOP EXTRACT {name}"},
									{Type: ActionPrevent, Desc: "设置归档日志保留策略,确保OGG消费完成前不删除", SkillCommand: "/params archive", RawSQL: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = 1440 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchEquals("NO_SUPPLOG"),
								Label:    "缺少supplemental logging",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "GoldenGate Extract ABEND:数据库或表级supplemental logging未启用"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查数据库级supplemental log状态", SkillCommand: "/sql @supplog", RawSQL: "SELECT supplemental_log_data_min, supplemental_log_data_pk, supplemental_log_data_ui, supplemental_log_data_all FROM V$DATABASE"},
									{Type: ActionFix, Desc: "启用数据库级minimum supplemental logging", SkillCommand: "/sql @enable_supplog", RawSQL: "ALTER DATABASE ADD SUPPLEMENTAL LOG DATA", Risk: "会略微增加redo生成量", Rollback: "ALTER DATABASE DROP SUPPLEMENTAL LOG DATA"},
									{Type: ActionFix, Desc: "重启Extract进程", SkillCommand: "/sql @ogg_restart", RawSQL: "-- GGSCI: START EXTRACT {name}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他ABEND原因(LogMiner注册/DDL/权限)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "GoldenGate Extract ABEND:可能因LogMiner注册失败、DDL不支持或权限不足"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看ggserr.log和Extract report文件获取详细错误", SkillCommand: "/sql @ogg_report", RawSQL: "-- GGSCI: VIEW REPORT {extract_name}"},
									{Type: ActionInvestigate, Desc: "检查OGG进程注册状态", SkillCommand: "/sql @ogg_register", RawSQL: "SELECT capture_name, status, status_change_time, error_message FROM DBA_CAPTURE WHERE purpose = 'GoldenGate Capture'"},
									{Type: ActionFix, Desc: "清理并重新注册Extract", SkillCommand: "/sql @ogg_reregister", RawSQL: "-- GGSCI: UNREGISTER EXTRACT {name} DATABASE; REGISTER EXTRACT {name} DATABASE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("STOPPED"),
					Label:    "Extract进程处于STOPPED状态",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "GoldenGate Extract进程处于STOPPED状态,数据复制已中断"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查Extract为何停止", SkillCommand: "/sql @ogg_info", RawSQL: "-- GGSCI: INFO EXTRACT {name}, DETAIL"},
						{Type: ActionFix, Desc: "确认无异常后重启Extract", SkillCommand: "/sql @ogg_start", RawSQL: "-- GGSCI: START EXTRACT {name}"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Extract运行正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "GoldenGate Extract进程运行正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"archive_full_hang"},
		CausesOf: []string{"HA2-002", "HA2-005"},
		Tags:     []string{"goldengate", "ogg", "extract", "ha", "replication"},
		Versions: "11g+",
	}
}

// ruleOGGReplicatLag detects OGG Replicat lag increasing beyond threshold,
// commonly caused by large transactions, missing parallel apply configuration,
// or target database bottlenecks.
func ruleOGGReplicatLag() *Rule {
	return &Rule{
		ID:       "HA2-002",
		Name:     "GoldenGate Replicat应用延迟",
		Category: "ha_ogg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOGGReplicatLagSec},
			{Type: SignalKeyword, Key: "replicat lag"},
			{Type: SignalKeyword, Key: "OGG lag"},
			{Type: SignalKeyword, Key: "goldengate delay"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOGGReplicatLagSec, Op: OpGT, Value: 300},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Replicat延迟值和趋势",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricOGGReplicatLagSec) },
			Branches: []Branch{
				{
					Match:    MatchGT(3600),
					Label:    "Replicat延迟>1小时,严重滞后",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查Replicat模式(经典 vs 并行/集成)",
						Query: QueryOGGStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("CLASSIC"),
								Label:    "使用经典单线程Replicat",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Replicat延迟>1小时,使用经典单线程模式,无法充分利用目标库资源"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "考虑切换为Parallel Replicat(并行应用)", SkillCommand: "/sql @ogg_replicat_mode", RawSQL: "-- GGSCI: INFO REPLICAT {name}, DETAIL; 查看 APPLY_PARALLELISM 参数"},
									{Type: ActionFix, Desc: "配置Integrated/Parallel Replicat提升吞吐", SkillCommand: "/sql @ogg_parallel", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; 添加 SPLIT_TRANS_RECS 和 PARALLELISM参数", Risk: "切换Replicat模式需要短暂停止复制", Rollback: "恢复经典模式配置并START REPLICAT"},
									{Type: ActionInvestigate, Desc: "检查目标库是否存在性能瓶颈", SkillCommand: "/sql @target_perf", RawSQL: "SELECT event, total_waits, time_waited_micro/1000 time_ms FROM V$SYSTEM_EVENT WHERE wait_class != 'Idle' ORDER BY time_waited_micro DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchEquals("LARGE_TXN"),
								Label:    "大事务导致Replicat处理缓慢",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Replicat延迟>1小时,检测到大事务阻塞应用进度"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看Replicat当前正在处理的事务大小", SkillCommand: "/sql @ogg_stats", RawSQL: "-- GGSCI: SEND REPLICAT {name}, STATUS"},
									{Type: ActionFix, Desc: "配置GROUPTRANSOPS和MAXTRANSOPS优化大事务处理", SkillCommand: "/sql @ogg_trans_opts", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; GROUPTRANSOPS 5000; MAXTRANSOPS 10000"},
									{Type: ActionPrevent, Desc: "在源端限制超大事务或分批处理", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, t.used_ublk*8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr WHERE t.used_ublk*8/1024 > 500 ORDER BY t.used_ublk DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致Replicat延迟",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Replicat延迟>1小时,需检查trail文件读取速度和目标库约束/索引影响"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查Replicat统计和trail读取速率", SkillCommand: "/sql @ogg_lag_detail", RawSQL: "-- GGSCI: STATS REPLICAT {name}, TOTALSONLY *, REPORTRATE HR"},
									{Type: ActionFix, Desc: "检查目标表是否有过多索引或触发器影响DML性能", SkillCommand: "/sql @target_indexes", RawSQL: "SELECT table_name, COUNT(*) idx_count FROM DBA_INDEXES WHERE owner = '{ogg_target_schema}' GROUP BY table_name HAVING COUNT(*) > 5 ORDER BY COUNT(*) DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(300),
					Label:    "Replicat延迟5min-1hr,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "GoldenGate Replicat延迟5~60分钟,建议关注并优化"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看Replicat延迟趋势和处理速率", SkillCommand: "/sql @ogg_lag_stats", RawSQL: "-- GGSCI: STATS REPLICAT {name}, LATEST"},
						{Type: ActionPrevent, Desc: "确认trail文件所在磁盘I/O正常", SkillCommand: "/sql @trail_io", RawSQL: "-- ls -la {trail_dir}; iostat -x 3 3"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Replicat延迟正常(<5min)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "GoldenGate Replicat延迟在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"HA2-001", "HA2-003"},
		CausesOf: []string{},
		Tags:     []string{"goldengate", "ogg", "replicat", "lag", "ha"},
		Versions: "11g+",
	}
}

// ruleOGGTrailFileGrowth detects OGG trail file disk space consumption growing
// excessively, which can fill up the filesystem and halt the entire pipeline.
func ruleOGGTrailFileGrowth() *Rule {
	return &Rule{
		ID:       "HA2-003",
		Name:     "GoldenGate Trail文件磁盘空间告警",
		Category: "ha_ogg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOGGTrailDiskPct},
			{Type: SignalKeyword, Key: "trail file space"},
			{Type: SignalKeyword, Key: "OGG trail disk"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOGGTrailDiskPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Trail文件目录磁盘使用率",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricOGGTrailDiskPct) },
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "Trail目录使用率>95%,即将写满",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有Replicat停止导致trail堆积",
						Query: QueryOGGTrailSpace,
						Branches: []Branch{
							{
								Match:    MatchEquals("REPLICAT_STOPPED"),
								Label:    "Replicat停止导致trail无法清理",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Trail目录使用>95%且Replicat已停止,trail文件持续堆积,Extract即将因磁盘满ABEND"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即修复并启动Replicat消费trail", SkillCommand: "/sql @ogg_rep_status", RawSQL: "-- GGSCI: INFO REPLICAT *, DETAIL"},
									{Type: ActionUrgent, Desc: "清理已被Replicat消费完成的旧trail文件", SkillCommand: "/sql @ogg_purge_trail", RawSQL: "-- GGSCI: PURGE EXTTRAIL {trail_path}, BEFORE {timestamp}"},
									{Type: ActionPrevent, Desc: "配置PURGEOLDEXTRACTS自动清理", SkillCommand: "/sql @ogg_purge_config", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; PURGEOLDEXTRACTS {trail_dir}, USECHECKPOINTS, MINKEEPDAYS 3"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Trail生成速度超过消费速度",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Trail目录使用>95%,Replicat运行中但消费速度跟不上生成速度"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "临时增加Trail目录空间或迁移trail路径", SkillCommand: "/sql @trail_space", RawSQL: "-- df -h {trail_dir}; GGSCI: ALTER EXTRACT {name}, EXTTRAIL {new_path}"},
									{Type: ActionFix, Desc: "优化Replicat吞吐(增加并行/BATCHSQL)", SkillCommand: "/sql @ogg_batch", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; BATCHSQL BATCHTRANSOPS 5000"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "Trail目录使用率80-95%,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "GoldenGate Trail文件目录使用率80~95%,需提前规划空间"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查trail文件生成速率和保留天数", SkillCommand: "/sql @ogg_trail_info", RawSQL: "-- ls -lhrt {trail_dir}; GGSCI: INFO EXTRACT {name}, SHOWCH"},
						{Type: ActionPrevent, Desc: "配置trail文件自动清理策略", SkillCommand: "/sql @ogg_purge", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; PURGEOLDEXTRACTS {trail_dir}, USECHECKPOINTS, MINKEEPDAYS 2"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Trail磁盘空间正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "GoldenGate Trail文件磁盘使用正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"HA2-002"},
		CausesOf: []string{"HA2-001"},
		Tags:     []string{"goldengate", "ogg", "trail", "disk", "space"},
		Versions: "11g+",
	}
}

// ruleOGGCDRConflict detects bi-directional replication conflict detection and
// resolution (CDR) issues in active-active GoldenGate configurations.
func ruleOGGCDRConflict() *Rule {
	return &Rule{
		ID:       "HA2-004",
		Name:     "GoldenGate双向复制冲突(CDR Conflict)",
		Category: "ha_ogg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOGGCDRConflicts},
			{Type: SignalKeyword, Key: "CDR conflict"},
			{Type: SignalKeyword, Key: "OGG conflict"},
			{Type: SignalKeyword, Key: "bi-directional conflict"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOGGCDRConflicts, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查CDR冲突数量和类型",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricOGGCDRConflicts) },
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "大量CDR冲突(>100),数据一致性受威胁",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "分析冲突类型(UNIQUENESS/UPDATE/DELETE)",
						Query: QueryOGGConflict,
						Branches: []Branch{
							{
								Match:    MatchEquals("UNIQUENESS"),
								Label:    "主键/唯一键冲突,两端同时INSERT",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "GoldenGate双向复制出现大量UNIQUENESS冲突,两端同时向同一表INSERT导致主键冲突"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查冲突表和行级详情", SkillCommand: "/sql @ogg_conflicts", RawSQL: "-- GGSCI: VIEW REPORT {rep_name}; 搜索 'CDR' 或 'CONFLICT'"},
									{Type: ActionFix, Desc: "配置CDR自动冲突解决策略(USELATESTVERSION)", SkillCommand: "/sql @ogg_cdr_config", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; MAP *.*, TARGET *.*, COMPARECOLS (ON UPDATE ALL, ON DELETE ALL), RESOLVECONFLICT (INSERTROWEXISTS, (DEFAULT, USEMAX (last_update_ts)))"},
									{Type: ActionPrevent, Desc: "检查序列分配策略,确保不同站点使用不同范围", SkillCommand: "/sql @seq_ranges", RawSQL: "SELECT sequence_owner, sequence_name, min_value, max_value, increment_by, last_number FROM DBA_SEQUENCES WHERE sequence_owner NOT IN ('SYS','SYSTEM') ORDER BY sequence_owner, sequence_name"},
								},
							},
							{
								Match:    MatchEquals("UPDATE_MISSING"),
								Label:    "UPDATE目标行不存在",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "GoldenGate CDR冲突:UPDATE操作找不到目标行,数据不同步"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否因DELETE/UPDATE顺序错乱导致", SkillCommand: "/sql @ogg_discard", RawSQL: "-- cat {discard_file}; 检查丢弃的DML操作"},
									{Type: ActionFix, Desc: "配置HANDLECOLLISIONS临时处理冲突", SkillCommand: "/sql @ogg_handle_coll", RawSQL: "-- GGSCI: SEND REPLICAT {name}, HANDLECOLLISIONS"},
									{Type: ActionFix, Desc: "对不同步的表进行数据比对和修复", SkillCommand: "/sql @ogg_veridata", RawSQL: "-- 使用Oracle GoldenGate Veridata进行全表数据比对"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "混合类型CDR冲突",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "GoldenGate双向复制出现多种类型冲突,需全面检查CDR配置"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "全面审查CDR配置和冲突日志", SkillCommand: "/sql @ogg_cdr_review", RawSQL: "-- GGSCI: VIEW REPORT {rep_name}; STATS REPLICAT {name}, REPORTRATE HR"},
									{Type: ActionFix, Desc: "配置完整的CDR冲突解决策略", SkillCommand: "/sql @ogg_cdr_full", RawSQL: "-- 参考Oracle GoldenGate文档配置RESOLVECONFLICT规则"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量CDR冲突(1-100),需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "GoldenGate存在少量CDR冲突,需确认冲突解决策略是否正确"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查冲突频率和涉及的表", SkillCommand: "/sql @ogg_conflict_summary", RawSQL: "-- GGSCI: STATS REPLICAT {name}, TABLE *.* REPORTRATE MIN"},
						{Type: ActionPrevent, Desc: "确认所有双向复制表已配置冲突检测和解决策略", SkillCommand: "/sql @ogg_cdr_tables", RawSQL: "-- GGSCI: VIEW PARAMS {rep_name}; 检查RESOLVECONFLICT配置"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无CDR冲突",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "GoldenGate双向复制无冲突"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"HA2-002"},
		Tags:     []string{"goldengate", "ogg", "cdr", "conflict", "bidirectional", "ha"},
		Versions: "11g+",
	}
}

// ruleOGGCheckpointLag detects OGG checkpoint lag exceeding recovery
// threshold, meaning recovery after restart will take excessively long.
func ruleOGGCheckpointLag() *Rule {
	return &Rule{
		ID:       "HA2-005",
		Name:     "GoldenGate Checkpoint延迟",
		Category: "ha_ogg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOGGCheckpointLag},
			{Type: SignalKeyword, Key: "OGG checkpoint lag"},
			{Type: SignalKeyword, Key: "goldengate checkpoint"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOGGCheckpointLag, Op: OpGT, Value: 1800},
			},
		},
		Tree: &TreeNode{
			Step:  "检查OGG各进程checkpoint延迟(秒)",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricOGGCheckpointLag) },
			Branches: []Branch{
				{
					Match:    MatchGT(7200),
					Label:    "Checkpoint延迟>2小时,恢复时间过长",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查checkpoint位置与当前trail/redo位置差距",
						Query: QueryOGGCheckpoint,
						Branches: []Branch{
							{
								Match:    MatchEquals("EXTRACT"),
								Label:    "Extract进程checkpoint严重滞后",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "GoldenGate Extract checkpoint延迟>2小时,若进程异常重启,恢复期间数据延迟将超过2小时"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查Extract checkpoint详情", SkillCommand: "/sql @ogg_ext_chk", RawSQL: "-- GGSCI: INFO EXTRACT {name}, SHOWCH"},
									{Type: ActionFix, Desc: "调整CHECKPOINTSECS参数增加checkpoint频率", SkillCommand: "/sql @ogg_chk_config", RawSQL: "-- GGSCI: EDIT PARAMS {ext_name}; CHECKPOINTSECS 30", Risk: "频繁checkpoint可能略微影响吞吐量", Rollback: "恢复CHECKPOINTSECS为默认值"},
									{Type: ActionInvestigate, Desc: "检查是否有长事务阻止checkpoint推进", SkillCommand: "/sql @long_txn_ogg", RawSQL: "SELECT s.sid, s.serial#, s.username, t.start_time, t.used_ublk*8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr WHERE t.used_ublk > 0 ORDER BY t.start_time"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Replicat checkpoint滞后",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "GoldenGate Replicat checkpoint延迟>2小时,重启后需从较早位置重新应用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "调整Replicat CHECKPOINTSECS", SkillCommand: "/sql @ogg_rep_chk", RawSQL: "-- GGSCI: EDIT PARAMS {rep_name}; CHECKPOINTSECS 30"},
									{Type: ActionInvestigate, Desc: "检查Replicat是否被大事务阻塞", SkillCommand: "/sql @ogg_rep_detail", RawSQL: "-- GGSCI: SEND REPLICAT {name}, STATUS"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(1800),
					Label:    "Checkpoint延迟30min-2hr,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "GoldenGate checkpoint延迟在30分钟~2小时之间,建议优化"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看各进程checkpoint位置", SkillCommand: "/sql @ogg_all_chk", RawSQL: "-- GGSCI: INFO EXTRACT *, SHOWCH; INFO REPLICAT *, SHOWCH"},
						{Type: ActionPrevent, Desc: "考虑减小CHECKPOINTSECS至30-60秒", SkillCommand: "/sql @ogg_param", RawSQL: "-- GGSCI: EDIT PARAMS {name}; CHECKPOINTSECS 30"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Checkpoint延迟正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "GoldenGate checkpoint延迟在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"HA2-001"},
		CausesOf: []string{},
		Tags:     []string{"goldengate", "ogg", "checkpoint", "recovery", "ha"},
		Versions: "11g+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// ASM Rules (5) — HA2-006 .. HA2-010
// ═══════════════════════════════════════════════════════════════════════════

func haASMRules() []*Rule {
	return []*Rule{
		ruleASMDiskgroupFull(),
		ruleASMRebalanceStuck(),
		ruleASMAUSize(),
		ruleASMRedundancyCheck(),
		ruleASMDiskPerformance(),
	}
}

// ruleASMDiskgroupFull detects ASM diskgroup usage exceeding 85%, risking
// database write failures when the diskgroup fills completely.
func ruleASMDiskgroupFull() *Rule {
	return &Rule{
		ID:       "HA2-006",
		Name:     "ASM磁盘组空间不足(Diskgroup Full)",
		Category: "ha_asm",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMDiskgroupUsePct},
			{Type: SignalKeyword, Key: "ASM diskgroup full"},
			{Type: SignalKeyword, Key: "ASM space"},
			{Type: SignalErrorCode, Key: "ORA-15041"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMDiskgroupUsePct, Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$ASM_DISKGROUP中各磁盘组使用率",
			Query: QueryASMDiskgroup,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "磁盘组使用率>95%,严重紧急",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查磁盘组冗余类型和可回收空间",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", metricASMRedundancyType) },
						Branches: []Branch{
							{
								Match:    MatchEquals("HIGH"),
								Label:    "HIGH冗余(3倍镜像),实际可用更少",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM磁盘组使用率>95%且为HIGH冗余,实际每写1GB消耗3GB,空间极其紧张"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即检查可释放的空间(audit/trace/过期数据)", SkillCommand: "/sql @asm_space_detail", RawSQL: "SELECT dg.name, dg.total_mb, dg.free_mb, ROUND((1-dg.free_mb/dg.total_mb)*100,1) pct_used, dg.type FROM V$ASM_DISKGROUP dg ORDER BY pct_used DESC"},
									{Type: ActionUrgent, Desc: "检查回收站和过期归档日志", SkillCommand: "/sql @reclaimable", RawSQL: "SELECT * FROM V$ASM_DISKGROUP_STAT WHERE group_number IN (SELECT group_number FROM V$ASM_DISKGROUP WHERE free_mb/total_mb < 0.05)"},
									{Type: ActionFix, Desc: "添加新磁盘到磁盘组", SkillCommand: "/sql @asm_add_disk", RawSQL: "ALTER DISKGROUP {dg_name} ADD DISK '{disk_path}'", Risk: "添加磁盘将触发rebalance,短期I/O增加", Rollback: "ALTER DISKGROUP {dg_name} DROP DISK {disk_name}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "NORMAL/EXTERNAL冗余",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM磁盘组使用率>95%,需立即扩容或清理空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查磁盘组使用详情", SkillCommand: "/sql @asm_usage", RawSQL: "SELECT dg.name, dg.total_mb, dg.free_mb, ROUND((1-dg.free_mb/dg.total_mb)*100,1) pct_used FROM V$ASM_DISKGROUP dg ORDER BY pct_used DESC"},
									{Type: ActionFix, Desc: "清理过期的归档日志释放FRA空间", SkillCommand: "/sql @rman_delete_obs", RawSQL: "-- RMAN: DELETE NOPROMPT OBSOLETE; CROSSCHECK ARCHIVELOG ALL; DELETE NOPROMPT EXPIRED ARCHIVELOG ALL;"},
									{Type: ActionFix, Desc: "添加磁盘扩容", SkillCommand: "/sql @asm_add_disk", RawSQL: "ALTER DISKGROUP {dg_name} ADD DISK '{disk_path}'", Risk: "rebalance期间I/O增加", Rollback: "ALTER DISKGROUP {dg_name} DROP DISK {disk_name}"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(85),
					Label:    "使用率85-95%,需规划扩容",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ASM磁盘组使用率85~95%,建议尽快规划扩容"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "预估空间增长趋势", SkillCommand: "/sql @asm_growth", RawSQL: "SELECT dg.name, dg.total_mb, dg.free_mb, ROUND((1-dg.free_mb/dg.total_mb)*100,1) pct_used, dg.type FROM V$ASM_DISKGROUP dg"},
						{Type: ActionPrevent, Desc: "检查是否有可清理的空间(audit/trace/temp)", SkillCommand: "/sql @asm_reclaimable", RawSQL: "SELECT occupant_name, space_used/1024/1024 used_mb, space_reclaimable/1024/1024 reclaimable_mb FROM V$RECOVERY_AREA_USAGE WHERE space_reclaimable > 0"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "磁盘组使用率正常(<85%)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM磁盘组空间使用正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full_emergency", "archive_full_hang"},
		Tags:     []string{"asm", "diskgroup", "space", "ha", "storage"},
		Versions: "10g+",
	}
}

// ruleASMRebalanceStuck detects ASM rebalance operations that are not making
// progress, which can cause prolonged I/O overhead and performance impact.
func ruleASMRebalanceStuck() *Rule {
	return &Rule{
		ID:       "HA2-007",
		Name:     "ASM Rebalance停滞",
		Category: "ha_asm",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMRebalanceProgress},
			{Type: SignalKeyword, Key: "ASM rebalance stuck"},
			{Type: SignalKeyword, Key: "ASM rebalance slow"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMRebalanceProgress, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$ASM_OPERATION中rebalance进度",
			Query: QueryASMRebalance,
			Branches: []Branch{
				{
					Match:    MatchLT(1),
					Label:    "Rebalance进度接近0%,完全停滞",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查rebalance停滞原因(I/O瓶颈/磁盘故障/power设置)",
						Query: QueryASMDiskPerf,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "磁盘I/O延迟>50ms,存储瓶颈导致rebalance停滞",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM rebalance停滞,底层磁盘I/O延迟>50ms,存储瓶颈阻止rebalance推进"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查存储层面I/O状态", SkillCommand: "/sql @asm_io_stats", RawSQL: "SELECT dg.name dg_name, d.name disk_name, d.read_time, d.write_time, d.reads, d.writes, ROUND(d.read_time/GREATEST(d.reads,1)*1000,2) avg_read_ms, ROUND(d.write_time/GREATEST(d.writes,1)*1000,2) avg_write_ms FROM V$ASM_DISK_STAT d JOIN V$ASM_DISKGROUP dg ON d.group_number = dg.group_number ORDER BY avg_write_ms DESC"},
									{Type: ActionFix, Desc: "降低rebalance power减少I/O影响", SkillCommand: "/sql @asm_power", RawSQL: "ALTER DISKGROUP {dg_name} REBALANCE POWER 2", Risk: "降低power会延长rebalance时间", Rollback: "ALTER DISKGROUP {dg_name} REBALANCE POWER 8"},
									{Type: ActionInvestigate, Desc: "检查是否有磁盘处于OFFLINE/DROPPING状态", SkillCommand: "/sql @asm_disk_status", RawSQL: "SELECT group_number, name, path, state, mode_status, total_mb, free_mb FROM V$ASM_DISK WHERE state != 'NORMAL' OR mode_status != 'ONLINE'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "I/O正常但rebalance仍停滞",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ASM rebalance停滞但磁盘I/O正常,可能因power设置过低或内部错误"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查当前rebalance操作详情", SkillCommand: "/sql @asm_rebal_detail", RawSQL: "SELECT group_number, operation, state, power, actual, sofar, est_work, est_rate, est_minutes FROM V$ASM_OPERATION WHERE operation = 'REBAL'"},
									{Type: ActionFix, Desc: "适当提高rebalance power加速完成", SkillCommand: "/sql @asm_power_up", RawSQL: "ALTER DISKGROUP {dg_name} REBALANCE POWER 8", Risk: "会增加I/O负载影响前台业务", Rollback: "ALTER DISKGROUP {dg_name} REBALANCE POWER 2"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "Rebalance正在进行中",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "ASM rebalance操作正在进行,需监控进度和对业务的I/O影响"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控rebalance预估完成时间", SkillCommand: "/sql @asm_rebal_eta", RawSQL: "SELECT group_number, operation, state, power, actual, sofar, est_work, est_rate, est_minutes FROM V$ASM_OPERATION"},
						{Type: ActionPrevent, Desc: "在业务低峰期执行rebalance", SkillCommand: "/sql @asm_power_low", RawSQL: "ALTER DISKGROUP {dg_name} REBALANCE POWER 1"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无rebalance操作",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM当前无rebalance操作"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"HA2-010"},
		CausesOf: []string{},
		Tags:     []string{"asm", "rebalance", "ha", "storage", "performance"},
		Versions: "10g+",
	}
}

// ruleASMAUSize detects ASM Allocation Unit (AU) size mismatch for workload
// type: 1MB is default but suboptimal for large databases; 4MB or 64MB
// recommended for data warehousing or large VLDB.
func ruleASMAUSize() *Rule {
	return &Rule{
		ID:       "HA2-008",
		Name:     "ASM AU大小不匹配工作负载",
		Category: "ha_asm",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMAUSizeMB},
			{Type: SignalKeyword, Key: "ASM AU size"},
			{Type: SignalKeyword, Key: "allocation unit"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMAUSizeMB, Op: OpExists, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$ASM_DISKGROUP中AU_SIZE设置",
			Query: QueryASMAUSize,
			Branches: []Branch{
				{
					Match:    MatchLTE(1),
					Label:    "AU=1MB(默认值),可能不适合大库",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查数据库大小判断AU是否合适",
						Query: QueryASMDiskgroup,
						Branches: []Branch{
							{
								Match:    MatchGT(5000000),
								Label:    "数据库>5TB但AU仅1MB",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库>5TB但ASM AU=1MB,大文件会产生过多extent,元数据管理开销大,建议64MB"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查当前磁盘组AU配置", SkillCommand: "/sql @asm_au", RawSQL: "SELECT name, allocation_unit_size/1024/1024 au_mb, total_mb, free_mb, type FROM V$ASM_DISKGROUP"},
									{Type: ActionFix, Desc: "新建磁盘组使用64MB AU后迁移数据", SkillCommand: "/sql @asm_create_dg", RawSQL: "CREATE DISKGROUP dg_data_new NORMAL REDUNDANCY DISK '/dev/oracleasm/disk*' ATTRIBUTE 'au_size' = '64M', 'compatible.asm' = '11.2'", Risk: "需要创建新磁盘组并在线迁移数据文件,操作复杂且耗时", Rollback: "保留原磁盘组不删除直到迁移确认完成"},
									{Type: ActionPrevent, Desc: "新建磁盘组时务必评估AU大小", SkillCommand: "/sql @asm_best_practice", RawSQL: "-- 最佳实践: <1TB用1MB, 1-5TB用4MB, >5TB用64MB AU"},
								},
							},
							{
								Match:    MatchGT(1000000),
								Label:    "数据库1-5TB,建议AU=4MB",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "数据库1~5TB但ASM AU=1MB,建议使用4MB AU减少extent数量"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "评估AU调整的收益", SkillCommand: "/sql @asm_extent_count", RawSQL: "SELECT dg.name, COUNT(*) extent_count FROM V$ASM_DISKGROUP dg JOIN V$ASM_FILE f ON dg.group_number = f.group_number GROUP BY dg.name ORDER BY extent_count DESC"},
									{Type: ActionPrevent, Desc: "下次扩容或迁移时使用4MB AU创建磁盘组", SkillCommand: "/sql @asm_plan", RawSQL: "-- 规划下次维护窗口使用4MB AU创建新磁盘组"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "数据库<1TB,1MB AU合适",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "数据库小于1TB,默认1MB AU大小合适"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchBetween(2, 4),
					Label:    "AU=4MB,适合中型数据库",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM AU=4MB,适合中等规模数据库"}},
					Actions:  nil,
				},
				{
					Match:    MatchDefault(),
					Label:    "AU=64MB或其他",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM AU大小配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"asm", "au", "allocation_unit", "sizing", "best_practice"},
		Versions: "11g+",
	}
}

// ruleASMRedundancyCheck detects EXTERNAL redundancy diskgroups without
// confirmed hardware RAID protection, risking data loss on disk failure.
func ruleASMRedundancyCheck() *Rule {
	return &Rule{
		ID:       "HA2-009",
		Name:     "ASM EXTERNAL冗余无硬件RAID保护",
		Category: "ha_asm",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMRedundancyType},
			{Type: SignalKeyword, Key: "ASM redundancy"},
			{Type: SignalKeyword, Key: "EXTERNAL redundancy"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMRedundancyType, Op: OpExists, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$ASM_DISKGROUP冗余类型",
			Query: QueryASMDiskgroup,
			Branches: []Branch{
				{
					Match:    MatchEquals("EXTERN"),
					Label:    "使用EXTERNAL冗余",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查磁盘组用途(DATA/FRA/OCR),评估风险",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "asm_dg_purpose") },
						Branches: []Branch{
							{
								Match:    MatchEquals("DATA"),
								Label:    "DATA磁盘组使用EXTERNAL冗余",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "DATA磁盘组使用EXTERNAL冗余,若底层无硬件RAID保护,单盘故障将导致数据丢失"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "确认底层存储是否有RAID保护", SkillCommand: "/sql @asm_disk_path", RawSQL: "SELECT group_number, name, path, total_mb, free_mb, os_mb FROM V$ASM_DISK WHERE group_number IN (SELECT group_number FROM V$ASM_DISKGROUP WHERE type = 'EXTERN')"},
									{Type: ActionFix, Desc: "如无硬件RAID,迁移到NORMAL冗余磁盘组", SkillCommand: "/sql @asm_migrate", RawSQL: "-- 1) CREATE DISKGROUP dg_data_new NORMAL REDUNDANCY ...; 2) ALTER DATABASE MOVE DATAFILE ... TO '+dg_data_new';", Risk: "迁移数据文件期间I/O增加但在线不停机", Rollback: "ALTER DATABASE MOVE DATAFILE ... TO '+原磁盘组'"},
									{Type: ActionPrevent, Desc: "OCR/Voting Disk磁盘组应始终使用NORMAL或HIGH冗余", SkillCommand: "/sql @asm_ocr_dg", RawSQL: "-- crsctl query css votedisk; ocrcheck"},
								},
							},
							{
								Match:    MatchEquals("FRA"),
								Label:    "FRA磁盘组使用EXTERNAL冗余",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "FRA磁盘组使用EXTERNAL冗余,备份和归档数据无ASM镜像保护"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "确认FRA磁盘组底层存储冗余级别", SkillCommand: "/sql @asm_fra_detail", RawSQL: "SELECT name, type, total_mb, free_mb FROM V$ASM_DISKGROUP WHERE name LIKE '%FRA%' OR name LIKE '%RECO%'"},
									{Type: ActionPrevent, Desc: "确保FRA内容有额外备份副本(如tape)", SkillCommand: "/sql @rman_config", RawSQL: "SHOW ALL -- 在RMAN中执行,确认备份冗余策略"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他磁盘组使用EXTERNAL冗余",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "ASM磁盘组使用EXTERNAL冗余,需确认底层存储有冗余保护"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "审查所有EXTERNAL冗余磁盘组", SkillCommand: "/sql @asm_all_dg", RawSQL: "SELECT name, type, total_mb, free_mb, state FROM V$ASM_DISKGROUP"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "使用NORMAL或HIGH冗余,ASM层有镜像保护",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM磁盘组使用NORMAL/HIGH冗余,具有镜像保护"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"datafile_corruption"},
		Tags:     []string{"asm", "redundancy", "raid", "ha", "data_protection"},
		Versions: "10g+",
	}
}

// ruleASMDiskPerformance detects individual ASM disk I/O latency exceeding
// thresholds (>20ms for HDD, >5ms for SSD), indicating failing or degraded disks.
func ruleASMDiskPerformance() *Rule {
	return &Rule{
		ID:       "HA2-010",
		Name:     "ASM磁盘I/O延迟异常",
		Category: "ha_asm",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricASMDiskLatencyMs},
			{Type: SignalKeyword, Key: "ASM disk latency"},
			{Type: SignalKeyword, Key: "ASM disk slow"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricASMDiskLatencyMs, Op: OpGT, Value: 20},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$ASM_DISK_STAT中各磁盘I/O延迟",
			Query: QueryASMDiskPerf,
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "磁盘延迟>100ms,严重性能问题或磁盘故障",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有磁盘处于异常状态(OFFLINE/HUNG)",
						Query: QueryASMDiskgroup,
						Branches: []Branch{
							{
								Match:    MatchEquals("OFFLINE"),
								Label:    "存在OFFLINE磁盘",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM磁盘I/O延迟>100ms且存在OFFLINE磁盘,磁盘硬件故障可能性大"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "识别故障磁盘并检查硬件状态", SkillCommand: "/sql @asm_disk_detail", RawSQL: "SELECT group_number, disk_number, name, path, state, mode_status, mount_status, header_status, total_mb, free_mb, read_time, write_time, reads, writes FROM V$ASM_DISK WHERE state != 'NORMAL' OR mode_status != 'ONLINE' ORDER BY group_number, disk_number"},
									{Type: ActionUrgent, Desc: "如磁盘故障,准备替换并触发rebalance", SkillCommand: "/sql @asm_replace_disk", RawSQL: "ALTER DISKGROUP {dg_name} DROP DISK {bad_disk}; ALTER DISKGROUP {dg_name} ADD DISK '{new_disk_path}'", Risk: "DROP DISK触发rebalance,确保冗余级别足够", Rollback: "若新磁盘有问题: ALTER DISKGROUP {dg_name} UNDROP DISKS"},
									{Type: ActionInvestigate, Desc: "检查OS级别磁盘错误", SkillCommand: "/sql @os_disk_err", RawSQL: "-- dmesg | grep -i error; cat /var/log/messages | grep -i scsi"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "磁盘在线但严重慢",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM磁盘I/O延迟>100ms但无OFFLINE磁盘,可能因存储控制器/SAN链路/阵列性能问题"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查各磁盘I/O延迟分布", SkillCommand: "/sql @asm_disk_latency", RawSQL: "SELECT d.group_number, dg.name dg_name, d.name disk_name, d.path, ROUND(d.read_time/GREATEST(d.reads,1)*1000,2) avg_read_ms, ROUND(d.write_time/GREATEST(d.writes,1)*1000,2) avg_write_ms FROM V$ASM_DISK_STAT d JOIN V$ASM_DISKGROUP dg ON d.group_number = dg.group_number ORDER BY avg_write_ms DESC"},
									{Type: ActionInvestigate, Desc: "联系存储团队检查SAN/NAS层面性能", SkillCommand: "/sql @storage_check", RawSQL: "-- 检查multipath, HBA queue depth, 存储阵列cache hit率"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(20),
					Label:    "磁盘延迟20-100ms(HDD偏高/SSD异常)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "判断磁盘类型(SSD vs HDD)",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricASMDiskLatencyMs) },
						Branches: []Branch{
							{
								Match:    MatchGT(5),
								Label:    "如果是SSD,>5ms已偏高",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ASM磁盘I/O延迟>20ms,如为SSD则>5ms已不正常,需检查SSD健康状态(磨损/温度)"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查各磁盘延迟和类型", SkillCommand: "/sql @asm_io_detail", RawSQL: "SELECT d.name, d.path, d.total_mb, ROUND(d.read_time/GREATEST(d.reads,1)*1000,2) read_ms, ROUND(d.write_time/GREATEST(d.writes,1)*1000,2) write_ms FROM V$ASM_DISK_STAT d ORDER BY write_ms DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionPrevent, Desc: "设置ASM磁盘I/O监控告警", SkillCommand: "/sql @asm_monitor", RawSQL: "-- 建议配置自动监控: ASM disk latency > 5ms(SSD) / 20ms(HDD) 告警"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "延迟在HDD可接受范围边界",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "ASM磁盘I/O延迟处于HDD可接受范围上限,建议持续监控"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查是否特定磁盘延迟偏高", SkillCommand: "/sql @asm_outlier", RawSQL: "SELECT d.name, d.path, ROUND(d.read_time/GREATEST(d.reads,1)*1000,2) read_ms, ROUND(d.write_time/GREATEST(d.writes,1)*1000,2) write_ms FROM V$ASM_DISK_STAT d ORDER BY write_ms DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "磁盘I/O延迟正常(<20ms)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM磁盘I/O延迟正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"HA2-007"},
		Tags:     []string{"asm", "disk", "latency", "io", "performance", "ha"},
		Versions: "10g+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RMAN Rules (5) — HA2-011 .. HA2-015
// ═══════════════════════════════════════════════════════════════════════════

func haRMANRules() []*Rule {
	return []*Rule{
		ruleRMANBlockChangeTracking(),
		ruleRMANIncrementalMerge(),
		ruleRMANParallelism(),
		ruleRMANCrosscheck(),
		ruleRMANEncryption(),
	}
}

// ruleRMANBlockChangeTracking detects BCT not enabled, leading to slow
// incremental backups that must scan all blocks rather than only changed ones.
func ruleRMANBlockChangeTracking() *Rule {
	return &Rule{
		ID:       "HA2-011",
		Name:     "RMAN Block Change Tracking未启用",
		Category: "ha_rman",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANBCTEnabled},
			{Type: SignalKeyword, Key: "block change tracking"},
			{Type: SignalKeyword, Key: "BCT"},
			{Type: SignalKeyword, Key: "incremental backup slow"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANBCTEnabled, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$BLOCK_CHANGE_TRACKING是否启用",
			Query: QueryRMANConfig,
			Branches: []Branch{
				{
					Match:    MatchEquals("DISABLED"),
					Label:    "BCT未启用",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查数据库大小和增量备份使用情况",
						Query: QueryRMANBackupStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("INCREMENTAL_USED"),
								Label:    "正在使用增量备份但无BCT",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库使用增量备份策略但未启用BCT,每次增量备份必须扫描所有数据块,耗时远超必要"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "启用Block Change Tracking(企业版功能)", SkillCommand: "/sql @enable_bct", RawSQL: "ALTER DATABASE ENABLE BLOCK CHANGE TRACKING USING FILE '+DATA/bct/block_change_tracking.ctf'", Risk: "启用BCT会增加约1/30000数据库大小的空间占用,以及极小的DML开销", Rollback: "ALTER DATABASE DISABLE BLOCK CHANGE TRACKING"},
									{Type: ActionInvestigate, Desc: "确认是否为企业版(BCT为EE功能)", SkillCommand: "/sql @edition", RawSQL: "SELECT banner FROM V$VERSION WHERE banner LIKE '%Enterprise%'"},
									{Type: ActionInvestigate, Desc: "查看当前增量备份耗时", SkillCommand: "/sql @rman_backup_time", RawSQL: "SELECT input_type, status, start_time, end_time, ROUND((end_time-start_time)*24*60,1) duration_min, input_bytes/1024/1024/1024 input_gb FROM V$RMAN_BACKUP_JOB_DETAILS WHERE input_type LIKE '%INCR%' ORDER BY start_time DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未使用增量备份,BCT影响较小",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "BCT未启用但当前使用全量备份策略,BCT对全量备份无影响"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "考虑切换到增量备份策略时同步启用BCT", SkillCommand: "/sql @backup_strategy", RawSQL: "SELECT input_type, COUNT(*), SUM(input_bytes)/1024/1024/1024 total_gb FROM V$RMAN_BACKUP_JOB_DETAILS WHERE start_time > SYSDATE - 30 GROUP BY input_type"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "BCT已启用",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Block Change Tracking已启用,增量备份性能优化"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"rman", "bct", "backup", "incremental", "performance"},
		Versions: "10g+",
	}
}

// ruleRMANIncrementalMerge detects databases not using incrementally updated
// backups (incremental merge), missing the fastest recovery strategy.
func ruleRMANIncrementalMerge() *Rule {
	return &Rule{
		ID:       "HA2-012",
		Name:     "RMAN未使用增量合并备份策略",
		Category: "ha_rman",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANIncrMerge},
			{Type: SignalKeyword, Key: "incremental merge"},
			{Type: SignalKeyword, Key: "incrementally updated backup"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANIncrMerge, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查RMAN备份策略是否使用增量合并",
			Query: QueryRMANBackupStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("NO_INCR_MERGE"),
					Label:    "未使用增量合并备份",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "评估数据库大小和RTO要求",
						Query: QueryASMDiskgroup,
						Branches: []Branch{
							{
								Match:    MatchGT(500000),
								Label:    "数据库>500GB,增量合并收益显著",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库>500GB但未使用增量合并备份策略(RECOVER COPY + INCREMENTAL),恢复时需要先还原全备再逐一应用增量,RTO过长"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置增量合并备份策略", SkillCommand: "/sql @rman_incr_merge", RawSQL: "-- RMAN脚本:\n-- RUN {\n--   RECOVER COPY OF DATABASE WITH TAG 'incr_merge';\n--   BACKUP INCREMENTAL LEVEL 1 FOR RECOVER OF COPY WITH TAG 'incr_merge' DATABASE;\n-- }", Risk: "首次执行需要完整的IMAGE COPY耗时较长", Rollback: "切换回原有备份策略"},
									{Type: ActionInvestigate, Desc: "评估当前RTO和恢复时间", SkillCommand: "/sql @rman_rto", RawSQL: "SELECT input_type, status, start_time, end_time, ROUND((end_time-start_time)*24*60,1) duration_min FROM V$RMAN_BACKUP_JOB_DETAILS WHERE status = 'COMPLETED' ORDER BY start_time DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "数据库<500GB,增量合并可选",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "数据库<500GB,增量合并备份为可选优化,当前备份策略可接受"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "了解增量合并备份的优势", SkillCommand: "/sql @rman_strategy", RawSQL: "-- 增量合并备份: 每天只需应用一次增量即可获得最新的全备份副本,恢复时无需逐一应用增量日志"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "已使用增量合并策略",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RMAN已配置增量合并备份策略,恢复效率最优"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"rman", "incremental", "merge", "backup", "rto", "recovery"},
		Versions: "10g+",
	}
}

// ruleRMANParallelism detects RMAN PARALLELISM configured too low for large
// databases, causing unnecessarily long backup windows.
func ruleRMANParallelism() *Rule {
	return &Rule{
		ID:       "HA2-013",
		Name:     "RMAN并行度过低",
		Category: "ha_rman",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANParallelism},
			{Type: SignalKeyword, Key: "RMAN parallelism"},
			{Type: SignalKeyword, Key: "backup slow"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANParallelism, Op: OpLTE, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查RMAN PARALLELISM配置",
			Query: QueryRMANConfig,
			Branches: []Branch{
				{
					Match:    MatchLTE(1),
					Label:    "PARALLELISM=1(默认),单通道备份",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "评估数据库大小和CPU资源",
						Query: QueryASMDiskgroup,
						Branches: []Branch{
							{
								Match:    MatchGT(1000000),
								Label:    "数据库>1TB,单通道备份效率低",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库>1TB但RMAN PARALLELISM=1,备份仅使用单通道,未充分利用I/O带宽和CPU资源"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置RMAN并行度(建议CPU核数/2或通道数)", SkillCommand: "/sql @rman_parallelism", RawSQL: "-- RMAN: CONFIGURE DEVICE TYPE DISK PARALLELISM 4 BACKUP TYPE TO BACKUPSET;", Risk: "增加并行度会增加CPU和I/O负载,建议在低峰期验证", Rollback: "-- RMAN: CONFIGURE DEVICE TYPE DISK PARALLELISM 1;"},
									{Type: ActionInvestigate, Desc: "查看当前备份耗时和通道利用", SkillCommand: "/sql @rman_channel_perf", RawSQL: "SELECT start_time, end_time, input_type, status, output_bytes/1024/1024/1024 out_gb, ROUND((end_time-start_time)*24*60,1) min, ROUND(output_bytes/1024/1024/((end_time-start_time)*24*3600),1) mb_per_sec FROM V$RMAN_BACKUP_JOB_DETAILS WHERE start_time > SYSDATE - 7 ORDER BY start_time DESC"},
									{Type: ActionInvestigate, Desc: "检查可用CPU核数", SkillCommand: "/sql @cpu_count", RawSQL: "SELECT value FROM V$PARAMETER WHERE name = 'cpu_count'"},
								},
							},
							{
								Match:    MatchGT(200000),
								Label:    "数据库200GB-1TB,建议2-4并行度",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "数据库200GB~1TB,RMAN单通道可能导致备份窗口偏长,建议2-4并行度"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "设置适当并行度", SkillCommand: "/sql @rman_parallel_2", RawSQL: "-- RMAN: CONFIGURE DEVICE TYPE DISK PARALLELISM 2;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "数据库<200GB,单通道可接受",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "数据库较小,RMAN单通道备份效率可接受"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "PARALLELISM已配置",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RMAN并行度已配置,备份效率良好"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"rman", "parallelism", "backup", "performance"},
		Versions: "9i+",
	}
}

// ruleRMANCrosscheck detects obsolete backups not crosschecked or deleted,
// wasting disk space and risking confusion during recovery.
func ruleRMANCrosscheck() *Rule {
	return &Rule{
		ID:       "HA2-014",
		Name:     "RMAN过期备份未清理(Crosscheck/Delete)",
		Category: "ha_rman",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANObsoleteCount},
			{Type: SignalKeyword, Key: "RMAN crosscheck"},
			{Type: SignalKeyword, Key: "obsolete backup"},
			{Type: SignalKeyword, Key: "expired backup"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANObsoleteCount, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查RMAN obsolete/expired备份数量",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricRMANObsoleteCount) },
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    "大量过期备份(>50),严重浪费空间",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查过期备份占用空间和FRA使用率",
						Query: QueryFRAUsage,
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "FRA使用率>80%且有大量过期备份",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "FRA使用率>80%且存在50+个过期备份未清理,可能导致备份失败或归档空间不足"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即执行crosscheck并删除过期备份", SkillCommand: "/sql @rman_cleanup", RawSQL: "-- RMAN:\n-- CROSSCHECK BACKUP;\n-- CROSSCHECK ARCHIVELOG ALL;\n-- DELETE NOPROMPT OBSOLETE;\n-- DELETE NOPROMPT EXPIRED BACKUP;\n-- DELETE NOPROMPT EXPIRED ARCHIVELOG ALL;"},
									{Type: ActionInvestigate, Desc: "检查FRA空间使用详情", SkillCommand: "/sql @fra_usage", RawSQL: "SELECT * FROM V$RECOVERY_AREA_USAGE"},
									{Type: ActionPrevent, Desc: "配置定期自动crosscheck和cleanup任务", SkillCommand: "/sql @rman_auto_clean", RawSQL: "-- 在RMAN备份脚本末尾添加: CROSSCHECK BACKUP; DELETE NOPROMPT OBSOLETE;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "FRA空间尚可但应清理",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "存在大量过期RMAN备份,虽FRA未满但应及时清理避免空间浪费"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "执行crosscheck和删除过期备份", SkillCommand: "/sql @rman_crosscheck", RawSQL: "-- RMAN: CROSSCHECK BACKUP; DELETE NOPROMPT OBSOLETE;"},
									{Type: ActionPrevent, Desc: "验证备份保留策略配置", SkillCommand: "/sql @rman_retention", RawSQL: "-- RMAN: SHOW RETENTION POLICY; SHOW ARCHIVELOG DELETION POLICY;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "少量过期备份(5-50),需定期清理",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "存在5~50个过期RMAN备份,建议定期执行crosscheck和delete obsolete"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "清理过期备份", SkillCommand: "/sql @rman_delete_obs", RawSQL: "-- RMAN: CROSSCHECK BACKUP; DELETE NOPROMPT OBSOLETE;"},
						{Type: ActionPrevent, Desc: "将crosscheck加入日常备份脚本", SkillCommand: "/sql @rman_script", RawSQL: "-- 在RMAN备份脚本末尾自动执行清理"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无过期备份,管理良好",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RMAN备份管理良好,无过期备份积压"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"HA2-006", "archive_full_hang"},
		Tags:     []string{"rman", "crosscheck", "obsolete", "backup", "space", "maintenance"},
		Versions: "9i+",
	}
}

// ruleRMANEncryption detects RMAN backups not encrypted, posing compliance
// and security risks especially for off-site or cloud storage.
func ruleRMANEncryption() *Rule {
	return &Rule{
		ID:       "HA2-015",
		Name:     "RMAN备份未加密",
		Category: "ha_rman",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANEncryption},
			{Type: SignalKeyword, Key: "RMAN encryption"},
			{Type: SignalKeyword, Key: "backup encryption"},
			{Type: SignalKeyword, Key: "backup security"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANEncryption, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查RMAN加密配置状态",
			Query: QueryRMANConfig,
			Branches: []Branch{
				{
					Match:    MatchEquals("NOT_ENCRYPTED"),
					Label:    "备份未加密",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "评估备份存储位置和合规要求",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "backup_dest_type") },
						Branches: []Branch{
							{
								Match:    MatchEquals("CLOUD"),
								Label:    "备份存储在云端/异地但未加密",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RMAN备份存储在云端或异地但未加密,数据泄露风险极高,违反多数合规要求(GDPR/SOX/等保)"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即配置RMAN备份加密", SkillCommand: "/sql @rman_encrypt", RawSQL: "-- RMAN:\n-- SET ENCRYPTION ON IDENTIFIED BY '{password}' ONLY;\n-- 或配置TDE钱包后:\n-- CONFIGURE ENCRYPTION FOR DATABASE ON;", Risk: "加密会增加约5-15%的CPU开销和备份时间", Rollback: "-- RMAN: CONFIGURE ENCRYPTION FOR DATABASE OFF;"},
									{Type: ActionFix, Desc: "配置TDE钱包作为加密基础", SkillCommand: "/sql @tde_wallet", RawSQL: "ADMINISTER KEY MANAGEMENT CREATE KEYSTORE '{wallet_path}' IDENTIFIED BY '{password}';\nADMINISTER KEY MANAGEMENT SET KEYSTORE OPEN IDENTIFIED BY '{password}';\nADMINISTER KEY MANAGEMENT SET KEY IDENTIFIED BY '{password}' WITH BACKUP;"},
									{Type: ActionPrevent, Desc: "审查当前未加密备份是否需要重新备份", SkillCommand: "/sql @backup_audit", RawSQL: "SELECT handle, encrypted, start_time, completion_time FROM V$BACKUP_PIECE WHERE start_time > SYSDATE - 30 ORDER BY start_time DESC"},
								},
							},
							{
								Match:    MatchEquals("TAPE"),
								Label:    "备份到磁带但未加密",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RMAN备份到磁带但未加密,磁带物理丢失或失窃将导致数据泄露"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置RMAN备份加密(密码或TDE)", SkillCommand: "/sql @rman_tape_encrypt", RawSQL: "-- RMAN: CONFIGURE ENCRYPTION FOR DATABASE ON;\n-- 确保使用ENCRYPTION ALGORITHM = 'AES256'"},
									{Type: ActionPrevent, Desc: "审查磁带管理流程和物理安全", SkillCommand: "/sql @tape_policy", RawSQL: "-- 检查磁带保管、运输、销毁流程是否符合安全规范"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "本地磁盘备份未加密",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "RMAN本地备份未加密,如有合规要求建议启用加密"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "评估是否有加密合规要求", SkillCommand: "/sql @compliance_check", RawSQL: "-- 评估: GDPR、SOX、PCI-DSS、等保三级是否要求备份加密"},
									{Type: ActionPrevent, Desc: "配置透明加密(推荐)", SkillCommand: "/sql @rman_tde_encrypt", RawSQL: "-- RMAN: CONFIGURE ENCRYPTION FOR DATABASE ON; -- 需先配置TDE钱包"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "备份已加密",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RMAN备份已配置加密,符合安全合规要求"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"rman", "encryption", "security", "compliance", "backup"},
		Versions: "10gR2+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Flashback Rules (5) — HA2-016 .. HA2-020
// ═══════════════════════════════════════════════════════════════════════════

func haFlashbackRules() []*Rule {
	return []*Rule{
		ruleFlashbackDatabaseNotEnabled(),
		ruleFlashbackRetentionTarget(),
		ruleFlashbackLogSpace(),
		ruleFlashbackDropRecyclebin(),
		ruleFlashbackDataArchive(),
	}
}

// ruleFlashbackDatabaseNotEnabled detects Flashback Database not enabled,
// losing the ability to quickly rewind the database to a prior point in time.
func ruleFlashbackDatabaseNotEnabled() *Rule {
	return &Rule{
		ID:       "HA2-016",
		Name:     "Flashback Database未启用",
		Category: "ha_flashback",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFlashbackEnabled},
			{Type: SignalKeyword, Key: "flashback database"},
			{Type: SignalKeyword, Key: "flashback not enabled"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFlashbackEnabled, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$DATABASE中FLASHBACK_ON状态",
			Query: QueryFlashbackStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("NO"),
					Label:    "Flashback Database未启用",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查数据库角色和是否配置了Data Guard",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", metricDGSwitchover) },
						Branches: []Branch{
							{
								Match:    MatchEquals("PRIMARY"),
								Label:    "主库+DG配置但Flashback未启用",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Data Guard主库未启用Flashback Database,Failover后无法快速恢复旧主库为新备库(需重建)"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "在主库启用Flashback Database", SkillCommand: "/sql @enable_flashback", RawSQL: "ALTER DATABASE FLASHBACK ON", Risk: "需要数据库处于ARCHIVELOG模式且有足够FRA空间;会生成flashback log增加I/O", Rollback: "ALTER DATABASE FLASHBACK OFF"},
									{Type: ActionInvestigate, Desc: "确认FRA空间是否充足", SkillCommand: "/sql @fra_space", RawSQL: "SELECT name, space_limit/1024/1024/1024 limit_gb, space_used/1024/1024/1024 used_gb, ROUND(space_used/space_limit*100,1) pct_used FROM V$RECOVERY_FILE_DEST"},
									{Type: ActionPrevent, Desc: "DG环境强烈建议主备都启用Flashback", SkillCommand: "/sql @dg_flashback", RawSQL: "SELECT db_unique_name, database_role, flashback_on FROM V$DATABASE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非DG环境但Flashback未启用",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "Flashback Database未启用,错误操作(如DROP/TRUNCATE)后无法快速回退数据库到之前状态"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "评估是否需要启用Flashback Database", SkillCommand: "/sql @flashback_eval", RawSQL: "SELECT log_mode, flashback_on, force_logging FROM V$DATABASE"},
									{Type: ActionInvestigate, Desc: "检查是否为ARCHIVELOG模式(Flashback前提)", SkillCommand: "/sql @archivelog", RawSQL: "SELECT log_mode FROM V$DATABASE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Flashback Database已启用",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flashback Database已启用,支持快速时间点回退"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"flashback", "database", "recovery", "ha", "dataguard"},
		Versions: "10g+",
	}
}

// ruleFlashbackRetentionTarget detects flashback retention target set too
// short, limiting the time window available for database flashback recovery.
func ruleFlashbackRetentionTarget() *Rule {
	return &Rule{
		ID:       "HA2-017",
		Name:     "Flashback保留时间过短(Retention Target)",
		Category: "ha_flashback",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFlashbackRetention},
			{Type: SignalKeyword, Key: "flashback retention"},
			{Type: SignalKeyword, Key: "db_flashback_retention_target"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFlashbackRetention, Op: OpLT, Value: 60},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DB_FLASHBACK_RETENTION_TARGET参数(分钟)",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricFlashbackRetention) },
			Branches: []Branch{
				{
					Match:    MatchLT(30),
					Label:    "保留时间<30分钟,极短",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查Flashback是否实际启用",
						Query: QueryFlashbackStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("YES"),
								Label:    "Flashback启用但保留时间极短",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Flashback Database已启用但保留时间<30分钟,发现问题后几乎没有时间执行flashback操作"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增加flashback保留时间(建议至少1440分钟=24小时)", SkillCommand: "/params db_flashback_retention_target", RawSQL: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = 1440 SCOPE=BOTH", Risk: "增加保留时间会占用更多FRA空间", Rollback: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = {原始值} SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查FRA空间是否支持更长保留", SkillCommand: "/sql @fra_capacity", RawSQL: "SELECT name, space_limit/1024/1024/1024 limit_gb, space_used/1024/1024/1024 used_gb, space_reclaimable/1024/1024/1024 reclaimable_gb FROM V$RECOVERY_FILE_DEST"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Flashback未启用,保留时间无意义",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "Flashback Database未启用,DB_FLASHBACK_RETENTION_TARGET参数不生效"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "如需flashback能力,先启用Flashback Database", SkillCommand: "/sql @enable_fb", RawSQL: "ALTER DATABASE FLASHBACK ON"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(60),
					Label:    "保留时间30-60分钟,偏短",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Flashback保留时间30~60分钟,对于大多数场景偏短,建议至少1440分钟(24小时)"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增加保留时间到24小时", SkillCommand: "/params db_flashback_retention_target", RawSQL: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = 1440 SCOPE=BOTH"},
						{Type: ActionInvestigate, Desc: "确认当前实际可flashback时间窗口", SkillCommand: "/sql @flashback_window", RawSQL: "SELECT oldest_flashback_scn, oldest_flashback_time, ROUND((SYSDATE - oldest_flashback_time)*24*60,1) actual_retention_min FROM V$FLASHBACK_DATABASE_LOG"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "保留时间>=60分钟",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flashback保留时间配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"flashback", "retention", "recovery", "ha"},
		Versions: "10g+",
	}
}

// ruleFlashbackLogSpace detects flashback logs consuming too much FRA space,
// potentially starving other FRA consumers (backups, archived logs).
func ruleFlashbackLogSpace() *Rule {
	return &Rule{
		ID:       "HA2-018",
		Name:     "Flashback日志占用过多FRA空间",
		Category: "ha_flashback",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFlashbackLogSpacePct},
			{Type: SignalKeyword, Key: "flashback log space"},
			{Type: SignalKeyword, Key: "flashback FRA"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFlashbackLogSpacePct, Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step:  "检查FRA中flashback log占用比例",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricFlashbackLogSpacePct) },
			Branches: []Branch{
				{
					Match:    MatchGT(80),
					Label:    "Flashback日志占FRA>80%,挤压备份空间",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查FRA整体使用率和备份空间",
						Query: QueryFRAUsage,
						Branches: []Branch{
							{
								Match:    MatchGT(90),
								Label:    "FRA总使用率>90%",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "FRA使用率>90%且flashback日志占80%以上,备份和归档空间严重不足,可能导致RMAN备份失败或归档挂起"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "减小flashback保留时间释放FRA空间", SkillCommand: "/params db_flashback_retention_target", RawSQL: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = 720 SCOPE=BOTH", Risk: "缩短flashback窗口", Rollback: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = {原始值} SCOPE=BOTH"},
									{Type: ActionUrgent, Desc: "扩大FRA空间限制", SkillCommand: "/params db_recovery_file_dest_size", RawSQL: "ALTER SYSTEM SET DB_RECOVERY_FILE_DEST_SIZE = {new_size}G SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查FRA各组件使用详情", SkillCommand: "/sql @fra_detail", RawSQL: "SELECT file_type, percent_space_used pct, percent_space_reclaimable reclaimable_pct, number_of_files FROM V$RECOVERY_AREA_USAGE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "FRA尚有空间但flashback占比过高",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Flashback日志占FRA>80%,虽FRA未满但需预防性调整,避免备份空间不足"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "适当降低flashback保留或扩大FRA", SkillCommand: "/sql @fra_adjust", RawSQL: "ALTER SYSTEM SET DB_FLASHBACK_RETENTION_TARGET = 1440 SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "评估flashback实际使用需求", SkillCommand: "/sql @flashback_usage", RawSQL: "SELECT estimated_flashback_size/1024/1024/1024 fb_gb, oldest_flashback_time, oldest_flashback_scn, retention_target FROM V$FLASHBACK_DATABASE_LOG"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(50),
					Label:    "Flashback日志占FRA 50-80%,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Flashback日志占FRA空间50~80%,建议监控趋势并确保备份空间充足"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看FRA使用分布", SkillCommand: "/sql @fra_breakdown", RawSQL: "SELECT file_type, percent_space_used pct, number_of_files FROM V$RECOVERY_AREA_USAGE ORDER BY percent_space_used DESC"},
						{Type: ActionPrevent, Desc: "确保FRA空间规划考虑flashback增长", SkillCommand: "/sql @fra_size_plan", RawSQL: "SELECT name, space_limit/1024/1024/1024 limit_gb FROM V$RECOVERY_FILE_DEST"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Flashback日志空间占比正常(<50%)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flashback日志FRA空间占用正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"HA2-017"},
		CausesOf: []string{"archive_full_hang", "HA2-014"},
		Tags:     []string{"flashback", "fra", "space", "backup", "ha"},
		Versions: "10g+",
	}
}

// ruleFlashbackDropRecyclebin detects RECYCLEBIN accumulating excessive space,
// impacting tablespace utilization and requiring purge management.
func ruleFlashbackDropRecyclebin() *Rule {
	return &Rule{
		ID:       "HA2-019",
		Name:     "回收站空间占用过大(RECYCLEBIN)",
		Category: "ha_flashback",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRecyclebinSpaceMB},
			{Type: SignalKeyword, Key: "recyclebin"},
			{Type: SignalKeyword, Key: "BIN$"},
			{Type: SignalKeyword, Key: "flashback drop"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRecyclebinSpaceMB, Op: OpGT, Value: 10240},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DBA_RECYCLEBIN中对象数量和空间占用",
			Query: QueryRecyclebinUsage,
			Branches: []Branch{
				{
					Match:    MatchGT(102400),
					Label:    "回收站>100GB,严重占用空间",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查表空间剩余空间和回收站占比",
						Query: QueryASMDiskgroup,
						Branches: []Branch{
							{
								Match:    MatchGT(85),
								Label:    "表空间使用率>85%且回收站占用大",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "回收站占用>100GB且表空间使用率>85%,回收站内的BIN$对象占据大量可用空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "清理回收站释放空间", SkillCommand: "/sql @purge_recyclebin", RawSQL: "PURGE DBA_RECYCLEBIN", Risk: "清理后无法通过FLASHBACK TABLE恢复已删除的表", Rollback: "无法回退,清理前确认不需要恢复"},
									{Type: ActionInvestigate, Desc: "查看回收站中最大的对象", SkillCommand: "/sql @recyclebin_top", RawSQL: "SELECT owner, original_name, type, ROUND(space*8/1024,1) size_mb, droptime FROM DBA_RECYCLEBIN ORDER BY space DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionPrevent, Desc: "对频繁DROP/RECREATE的表使用PURGE关键字", SkillCommand: "/sql @drop_purge", RawSQL: "-- DROP TABLE {table_name} PURGE; -- 直接跳过回收站"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "表空间尚可但回收站过大",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "回收站>100GB,建议清理节省空间,尽管表空间尚未紧张"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "清理指定时间前的回收站对象", SkillCommand: "/sql @purge_old", RawSQL: "-- 清理30天前的回收站对象:\n-- BEGIN FOR r IN (SELECT owner, object_name FROM DBA_RECYCLEBIN WHERE droptime < SYSDATE - 30) LOOP EXECUTE IMMEDIATE 'PURGE TABLE \"'||r.owner||'\".\"'||r.object_name||'\"'; END LOOP; END;"},
									{Type: ActionPrevent, Desc: "评估是否需要关闭RECYCLEBIN", SkillCommand: "/params recyclebin", RawSQL: "ALTER SYSTEM SET RECYCLEBIN = OFF SCOPE=BOTH", Risk: "关闭后DROP TABLE将无法通过FLASHBACK TABLE恢复", Rollback: "ALTER SYSTEM SET RECYCLEBIN = ON SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(10240),
					Label:    "回收站10-100GB,需定期清理",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "回收站空间10~100GB,建议定期清理不再需要的回收站对象"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看回收站内容概况", SkillCommand: "/sql @recyclebin_summary", RawSQL: "SELECT owner, type, COUNT(*) obj_count, ROUND(SUM(space*8/1024),1) total_mb FROM DBA_RECYCLEBIN GROUP BY owner, type ORDER BY total_mb DESC"},
						{Type: ActionPrevent, Desc: "建立回收站定期清理机制", SkillCommand: "/sql @recyclebin_policy", RawSQL: "-- 建议每周清理超过7天的回收站对象"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "回收站空间正常(<10GB)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "回收站空间占用正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full_emergency"},
		Tags:     []string{"flashback", "recyclebin", "space", "drop", "purge"},
		Versions: "10g+",
	}
}

// ruleFlashbackDataArchive detects Flashback Data Archive (FDA/Total Recall)
// configuration issues, including not being enabled for audit-critical tables
// or having insufficient retention.
func ruleFlashbackDataArchive() *Rule {
	return &Rule{
		ID:       "HA2-020",
		Name:     "Flashback Data Archive配置检查(FDA/Total Recall)",
		Category: "ha_flashback",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFDAEnabled},
			{Type: SignalKeyword, Key: "flashback data archive"},
			{Type: SignalKeyword, Key: "FDA"},
			{Type: SignalKeyword, Key: "total recall"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFDAEnabled, Op: OpExists, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Flashback Data Archive配置状态",
			Query: QueryFDAStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("NOT_CONFIGURED"),
					Label:    "FDA未配置",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "评估是否有审计/合规需求需要历史数据追溯",
						Check: func(ctx *EvalContext) interface{} { return ctx.GetStr("metrics", "compliance_required") },
						Branches: []Branch{
							{
								Match:    MatchEquals("YES"),
								Label:    "有合规需求但未配置FDA",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "系统有合规审计需求但未配置Flashback Data Archive(Total Recall),无法追溯历史数据变更"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "创建Flashback Data Archive并关联关键表", SkillCommand: "/sql @create_fda", RawSQL: "CREATE FLASHBACK ARCHIVE fda_1year TABLESPACE {ts_name} RETENTION 1 YEAR;\nALTER TABLE {schema}.{table} FLASHBACK ARCHIVE fda_1year;", Risk: "启用FDA会增加DML开销(约10-15%)和存储空间", Rollback: "ALTER TABLE {schema}.{table} NO FLASHBACK ARCHIVE;\nDROP FLASHBACK ARCHIVE fda_1year;"},
									{Type: ActionInvestigate, Desc: "确认需要FDA的表列表", SkillCommand: "/sql @fda_candidate", RawSQL: "SELECT owner, table_name, num_rows FROM DBA_TABLES WHERE owner NOT IN ('SYS','SYSTEM','DBSNMP') AND num_rows > 0 ORDER BY num_rows DESC FETCH FIRST 50 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无特殊合规需求,FDA为可选",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "Flashback Data Archive未配置,如无合规要求则可选功能"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "了解FDA/Total Recall能力以备未来需要", SkillCommand: "/sql @fda_info", RawSQL: "-- Flashback Data Archive允许查询任意历史时间点的行数据: SELECT * FROM {table} AS OF TIMESTAMP {time}"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("CONFIGURED"),
					Label:    "FDA已配置,检查运行状态",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查FDA归档是否正常运行",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricFDAEnabled) },
						Branches: []Branch{
							{
								Match:    MatchLT(1),
								Label:    "FDA已配置但有异常",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Flashback Data Archive已配置但运行异常,历史数据归档可能中断"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查FDA状态和错误", SkillCommand: "/sql @fda_errors", RawSQL: "SELECT flashback_archive_name, status, flashback_archive# FROM DBA_FLASHBACK_ARCHIVE"},
									{Type: ActionInvestigate, Desc: "检查FDA关联的表和表空间", SkillCommand: "/sql @fda_tables", RawSQL: "SELECT table_name, owner_name, flashback_archive_name, archive_table_name, status FROM DBA_FLASHBACK_ARCHIVE_TABLES"},
									{Type: ActionFix, Desc: "检查FDA表空间是否充足", SkillCommand: "/sql @fda_ts", RawSQL: "SELECT t.tablespace_name, ROUND(SUM(bytes)/1024/1024/1024,2) used_gb, ROUND(SUM(maxbytes)/1024/1024/1024,2) max_gb FROM DBA_DATA_FILES t WHERE tablespace_name IN (SELECT tablespace_name FROM DBA_FLASHBACK_ARCHIVE_TS) GROUP BY t.tablespace_name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "FDA运行正常",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "Flashback Data Archive已配置且运行正常"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "定期检查FDA表空间和保留策略", SkillCommand: "/sql @fda_health", RawSQL: "SELECT flashback_archive_name, retention_in_days, create_time, status FROM DBA_FLASHBACK_ARCHIVE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "FDA状态未知",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flashback Data Archive状态检查完成"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"flashback", "fda", "total_recall", "audit", "compliance", "history"},
		Versions: "11g+",
	}
}
