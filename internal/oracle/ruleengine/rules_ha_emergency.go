/*-------------------------------------------------------------------------
 *
 * rules_ha_emergency.go
 *	  Oracle rule engine — HA-emergency rules (Data Guard fail-over, RAC node eviction).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_ha_emergency.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── HA & Emergency Query IDs ───────────────────────────────────────────────

const (
	QueryDGStatus          QueryID = "dg_status"
	QueryDGGap             QueryID = "dg_gap"
	QueryDGMRPStatus       QueryID = "dg_mrp_status"
	QueryDGSwitchoverReady QueryID = "dg_switchover_ready"
	QueryDGStandbyRedoLog  QueryID = "dg_standby_redo_log"
	QueryDGBrokerConfig    QueryID = "dg_broker_config"
	QueryRACInterconnect   QueryID = "rac_interconnect"
	QueryRACGCStats        QueryID = "rac_gc_stats"
	QueryRACInstances      QueryID = "rac_instances"
	QueryRACServiceStats   QueryID = "rac_service_stats"
	QueryRACDRMStats       QueryID = "rac_drm_stats"
	QueryControlfileInfo   QueryID = "controlfile_info"
	QueryCorruptBlocks     QueryID = "corrupt_blocks"
	QueryTempUsage         QueryID = "temp_usage"
	QueryArchiveDest       QueryID = "archive_dest"
)

// ─── Metric name constants for HA/emergency rules ──────────────────────────

const (
	metricDGTransportLag = "dg_transport_lag_sec"
	metricDGApplyLag     = "dg_apply_lag_sec"
	metricDGGapCount     = "dg_archive_gap_count"
	metricDGMRPRunning   = "dg_mrp_running"
	metricDGSyncRTT      = "dg_sync_rtt_ms"
	metricDGSwitchover   = "dg_switchover_status"
	metricDGSRLCount     = "dg_srl_count"
	metricDGBrokerWarn   = "dg_broker_warning_count"

	metricRACMisscount       = "rac_css_misscount"
	metricRACGCAvgMs         = "rac_gc_block_recv_avg_ms"
	metricRACDRMRemaster     = "rac_drm_remaster_events"
	metricRACSessionImbalance = "rac_session_imbalance_pct"
	metricRACGCWaitPct       = "rac_gc_wait_pct_dbtime"
	metricRACNetPartition    = "rac_network_partition"
	metricRACInstCrash       = "rac_instance_crash"

	metricORA7445Count      = "ora_7445_count"
	metricORA4031Count      = "ora_4031_count"
	// metricORA1555Count defined in rules_deep_infra.go
	metricORA30036Count     = "ora_30036_count"
	metricTSFullCount       = "tablespace_full_count"
	metricArchiveDestFull   = "archive_dest_full"
	metricORA1652Count      = "ora_1652_count"
	metricCorruptBlockCount = "corrupt_block_count"
	metricControlfileIssue  = "controlfile_issue"
	metricListenerDown      = "listener_down"
)

// ═══════════════════════════════════════════════════════════════════════════
// Data Guard Rules (8)
// ═══════════════════════════════════════════════════════════════════════════

func haDataGuardRules() []*Rule {
	return []*Rule{
		ruleDGTransportLag(),
		ruleDGApplyLag(),
		ruleDGGapDetected(),
		ruleDGMRPStopped(),
		ruleDGSyncModeLatency(),
		ruleDGSwitchoverPrecheck(),
		ruleDGStandbyRedoLog(),
		ruleDGBrokerWarning(),
	}
}

// ruleDGTransportLag detects Data Guard transport lag > 1 minute, indicating
// network issues, archive dest problems, or log shipping bottlenecks.
func ruleDGTransportLag() *Rule {
	return &Rule{
		ID:       "dg_transport_lag",
		Name:     "Data Guard传输延迟(Transport Lag)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGTransportLag},
			{Type: SignalKeyword, Key: "transport lag"},
			{Type: SignalKeyword, Key: "data guard lag"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGTransportLag, Op: OpGT, Value: 60},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$DATAGUARD_STATS中transport_lag值",
			Query: QueryDGStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(600),
					Label:    "transport_lag > 10min,严重网络/归档问题",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查archive dest状态和网络连通性",
						Query: QueryArchiveDest,
						Branches: []Branch{
							{
								Match:    MatchEquals("ERROR"),
								Label:    "archive dest处于ERROR状态",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Data Guard transport lag严重(>10min),archive destination处于ERROR状态"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查archive dest错误详情", SkillCommand: "/sql @dg_status", RawSQL: "SELECT dest_id, status, error, target, process FROM V$ARCHIVE_DEST WHERE status = 'ERROR'"},
									{Type: ActionFix, Desc: "检查主备之间网络连通性", SkillCommand: "/sql @dg_network", RawSQL: "SELECT * FROM V$DATAGUARD_STATS WHERE name IN ('transport lag', 'apply lag')"},
									{Type: ActionFix, Desc: "重新启用archive dest", SkillCommand: "/sql @dg_enable_dest", RawSQL: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_STATE_2 = 'ENABLE' SCOPE=BOTH", Risk: "确认备库可达后再启用", Rollback: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_STATE_2 = 'DEFER' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "dest状态正常但传输慢",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Archive dest状态正常但transport lag>10min,可能为网络带宽不足或主库产生redo过快"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查redo生成速率与网络带宽", SkillCommand: "/sql @redo_rate", RawSQL: "SELECT TRUNC(completion_time, 'HH') hr, COUNT(*) log_switches, SUM(blocks * block_size)/1024/1024 redo_mb FROM V$ARCHIVED_LOG WHERE completion_time > SYSDATE - 1 GROUP BY TRUNC(completion_time, 'HH') ORDER BY hr DESC"},
									{Type: ActionFix, Desc: "考虑启用redo transport压缩(12c+)", SkillCommand: "/params log_archive_dest_2", RawSQL: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2 = 'SERVICE=standby ASYNC COMPRESSION=ENABLE' SCOPE=BOTH", Risk: "压缩会增加CPU开销", Rollback: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2 = 'SERVICE=standby ASYNC NOCOMPRESSION' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(60),
					Label:    "transport_lag 1-10min,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Data Guard transport lag > 1分钟,需检查网络或archive进程"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查archive进程状态", SkillCommand: "/sql @dg_process", RawSQL: "SELECT process, status, thread#, sequence#, block#, blocks FROM V$MANAGED_STANDBY WHERE process LIKE 'ARCH%' OR process = 'RFS'"},
						{Type: ActionInvestigate, Desc: "检查网络延迟", SkillCommand: "/sql @dg_stats", RawSQL: "SELECT name, value, datum_time, time_computed FROM V$DATAGUARD_STATS"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "transport_lag正常(< 1min)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Data Guard传输延迟在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"redo_log_switch_frequent"},
		CausesOf: []string{"dg_apply_lag", "dg_gap_detected"},
		Tags:     []string{"dataguard", "ha", "transport", "network"},
		Versions: "9i+",
	}
}

// ruleDGApplyLag detects Data Guard apply lag > 10 minutes, indicating
// MRP issues, large transactions, or standby performance problems.
func ruleDGApplyLag() *Rule {
	return &Rule{
		ID:       "dg_apply_lag",
		Name:     "Data Guard应用延迟(Apply Lag)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGApplyLag},
			{Type: SignalKeyword, Key: "apply lag"},
			{Type: SignalKeyword, Key: "mrp delay"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGApplyLag, Op: OpGT, Value: 600},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$DATAGUARD_STATS中apply_lag值",
			Query: QueryDGStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(3600),
					Label:    "apply_lag > 1小时,严重滞后",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查MRP进程状态和等待事件",
						Query: QueryDGMRPStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("NOT_RUNNING"),
								Label:    "MRP进程未运行",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Apply lag>1小时且MRP进程未运行,日志应用已完全停止"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即启动MRP进程", SkillCommand: "/sql @start_mrp", RawSQL: "ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION"},
									{Type: ActionInvestigate, Desc: "检查alert.log中MRP停止原因", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%MRP%' OR message_text LIKE '%recovery%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "MRP运行中但应用慢",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "MRP进程运行但apply lag>1小时,可能存在大事务或备库I/O瓶颈"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查MRP等待事件", SkillCommand: "/sql @mrp_waits", RawSQL: "SELECT event, state, client_info FROM V$SESSION WHERE program LIKE '%(MRP0)%'"},
									{Type: ActionFix, Desc: "考虑启用并行恢复加速应用", SkillCommand: "/params recovery_parallelism", RawSQL: "ALTER SYSTEM SET recovery_parallelism = 4 SCOPE=BOTH", Risk: "增加CPU和I/O开销", Rollback: "ALTER SYSTEM SET recovery_parallelism = 0 SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(600),
					Label:    "apply_lag 10min-1hr,需关注",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否存在大事务导致应用延迟",
						Query: QueryLongTransactions,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在长事务可能影响应用速度",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Apply lag>10min,主库存在长事务,备库需等待事务完整才能应用"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查MRP当前应用的redo sequence", SkillCommand: "/sql @mrp_progress", RawSQL: "SELECT process, status, thread#, sequence#, block#, blocks FROM V$MANAGED_STANDBY WHERE process = 'MRP0'"},
									{Type: ActionPrevent, Desc: "监控主库大事务,避免超长事务", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, t.used_ublk*8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr ORDER BY t.used_ublk DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无长事务但apply慢",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Apply lag>10min,无明显长事务,可能为备库I/O性能不足"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查备库I/O性能", SkillCommand: "/sql @standby_io", RawSQL: "SELECT file#, name, phyrds, phywrts, avgiotim FROM V$DATAFILE f JOIN V$FILESTAT s ON f.file# = s.file# ORDER BY avgiotim DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "apply_lag正常(< 10min)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Data Guard应用延迟在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"dg_transport_lag", "dg_mrp_stopped"},
		CausesOf: []string{},
		Tags:     []string{"dataguard", "ha", "apply", "mrp"},
		Versions: "9i+",
	}
}

// ruleDGGapDetected detects archive log gaps between primary and standby
// via V$ARCHIVE_GAP, requiring FAL (Fetch Archive Log) resolution.
func ruleDGGapDetected() *Rule {
	return &Rule{
		ID:       "dg_gap_detected",
		Name:     "Data Guard归档间隙(Archive Gap)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGGapCount},
			{Type: SignalErrorCode, Key: "ORA-16196"},
			{Type: SignalKeyword, Key: "archive gap"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGGapCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "查询V$ARCHIVE_GAP确认间隙范围",
			Query: QueryDGGap,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "间隙超过10个sequence,严重落后",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查FAL进程和archive dest状态",
						Query: QueryArchiveDest,
						Branches: []Branch{
							{
								Match:    MatchEquals("ERROR"),
								Label:    "FAL/archive dest异常",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Data Guard存在严重归档间隙(>10 sequences),FAL进程可能失败"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并修复FAL配置", SkillCommand: "/sql @fal_status", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('fal_server', 'fal_client')"},
									{Type: ActionUrgent, Desc: "手动注册缺失的归档日志", SkillCommand: "/sql @register_arch", RawSQL: "ALTER DATABASE REGISTER LOGFILE '{archive_path}'"},
									{Type: ActionFix, Desc: "如间隙无法恢复考虑增量备份恢复备库", SkillCommand: "/sql @incremental_restore", RawSQL: "-- RMAN: BACKUP INCREMENTAL FROM SCN {scn} DATABASE; RESTORE DATABASE FROM ... CATALOG ..."},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "dest正常但仍有间隙",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Archive dest正常但存在间隙,可能因主库归档已被清除或磁盘空间不足"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查主库归档是否可用", SkillCommand: "/sql @arch_available", RawSQL: "SELECT thread#, sequence#, name, applied FROM V$ARCHIVED_LOG WHERE thread# = {thread} AND sequence# BETWEEN {low} AND {high} ORDER BY sequence#"},
									{Type: ActionFix, Desc: "从RMAN备份恢复缺失归档", SkillCommand: "/sql @rman_restore_arch", RawSQL: "-- RMAN: RESTORE ARCHIVELOG FROM SEQUENCE {low_seq} UNTIL SEQUENCE {high_seq} THREAD {thread};"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量间隙(1-10 sequences)",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Data Guard存在归档间隙,FAL进程应自动解决"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认FAL是否正在自动恢复", SkillCommand: "/sql @fal_progress", RawSQL: "SELECT thread#, low_sequence#, high_sequence# FROM V$ARCHIVE_GAP"},
						{Type: ActionInvestigate, Desc: "检查alert.log中gap相关信息", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%gap%' OR message_text LIKE '%FAL%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无归档间隙",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Data Guard无归档间隙,日志传输连续"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"dg_transport_lag", "archive_full_hang"},
		CausesOf: []string{"dg_apply_lag"},
		Tags:     []string{"dataguard", "ha", "gap", "fal"},
		Versions: "9i+",
	}
}

// ruleDGMRPStopped detects MRP (Managed Recovery Process) not running on
// standby, meaning redo apply is stopped and RPO is growing.
func ruleDGMRPStopped() *Rule {
	return &Rule{
		ID:       "dg_mrp_stopped",
		Name:     "Data Guard MRP进程停止(MRP Not Running)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGMRPRunning},
			{Type: SignalKeyword, Key: "MRP stopped"},
			{Type: SignalKeyword, Key: "recovery stopped"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGMRPRunning, Op: OpEQ, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$MANAGED_STANDBY中MRP进程状态",
			Query: QueryDGMRPStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("NOT_RUNNING"),
					Label:    "确认MRP进程不存在",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查alert.log中MRP停止原因",
						Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricAlertLogErrors) },
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "alert.log中存在错误",
								Severity: SeverityCritical,
								Then: &TreeNode{
									Step:  "检查是否有ORA错误导致MRP停止",
									Query: QueryDGStatus,
									Branches: []Branch{
										{
											Match:    MatchEquals("GAP"),
											Label:    "因归档间隙MRP无法继续",
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "MRP因归档间隙停止,需解决GAP后重启MRP"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "检查并解决归档间隙", SkillCommand: "/sql @dg_gap", RawSQL: "SELECT thread#, low_sequence#, high_sequence# FROM V$ARCHIVE_GAP"},
												{Type: ActionUrgent, Desc: "解决间隙后重启MRP", SkillCommand: "/sql @start_mrp", RawSQL: "ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION"},
											},
										},
										{
											Match:    MatchDefault(),
											Label:    "其他ORA错误导致MRP停止",
											Severity: SeverityCritical,
											Findings: []Finding{
												{Desc: "MRP因ORA错误停止,需查看alert.log确定根因"},
											},
											Actions: []Action{
												{Type: ActionUrgent, Desc: "查看alert.log最近的MRP错误", SkillCommand: "/alert", RawSQL: "SELECT message_text, originating_timestamp FROM V$DIAG_ALERT_EXT WHERE (message_text LIKE '%MRP%' OR message_text LIKE '%ORA-%' OR message_text LIKE '%recovery%') AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC FETCH FIRST 30 ROWS ONLY"},
												{Type: ActionFix, Desc: "修复错误后重启MRP", SkillCommand: "/sql @start_mrp", RawSQL: "ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION"},
											},
										},
									},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无明显错误但MRP未运行",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "MRP进程未运行且alert.log无明显错误,可能因人工操作或维护停止"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "重新启动MRP进程", SkillCommand: "/sql @start_mrp", RawSQL: "ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION"},
									{Type: ActionPrevent, Desc: "设置Data Guard自动管理(broker)", SkillCommand: "/sql @dg_broker", RawSQL: "-- DGMGRL: EDIT DATABASE '{db_unique_name}' SET STATE='APPLY-ON';"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "MRP进程正在运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "MRP进程正常运行中"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"dg_gap_detected", "datafile_corruption"},
		CausesOf: []string{"dg_apply_lag"},
		Tags:     []string{"dataguard", "ha", "mrp", "recovery"},
		Versions: "9i+",
	}
}

// ruleDGSyncModeLatency detects SYNC mode Data Guard with RTT > 5ms,
// recommending transition to ASYNC or Far Sync to reduce commit latency.
func ruleDGSyncModeLatency() *Rule {
	return &Rule{
		ID:       "dg_sync_mode_latency",
		Name:     "Data Guard SYNC模式延迟(SYNC RTT High)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGSyncRTT},
			{Type: SignalWaitEvent, Key: "log file sync"},
			{Type: SignalKeyword, Key: "sync mode latency"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGSyncRTT, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查SYNC模式RTT和log file sync等待",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricDGSyncRTT) },
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    "RTT > 20ms,SYNC模式严重影响性能",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查log file sync等待时间",
						Check: func(ctx *EvalContext) interface{} { return ctx.WaitAvgMs("log file sync") },
						Branches: []Branch{
							{
								Match:    MatchGT(20),
								Label:    "log file sync avg > 20ms,确认SYNC造成瓶颈",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Data Guard SYNC模式RTT>20ms,log file sync严重偏高,事务提交延迟显著"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "切换为ASYNC模式缓解性能影响", SkillCommand: "/sql @dg_async", RawSQL: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2 = 'SERVICE=standby ASYNC VALID_FOR=(ONLINE_LOGFILES,PRIMARY_ROLE) DB_UNIQUE_NAME=standby' SCOPE=BOTH", Risk: "切换ASYNC会降低RPO保证(可能丢失少量redo)", Rollback: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2 = 'SERVICE=standby SYNC AFFIRM ...' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "长期方案:部署Far Sync实例减少RTT", SkillCommand: "/sql @dg_farsync", RawSQL: "-- 需要额外Far Sync实例部署在主库网络附近,SYNC到Far Sync,Far Sync ASYNC到备库"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "log file sync尚可接受",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RTT>20ms但log file sync尚在可接受范围,可能有其他LGWR优化"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查网络延迟原因", SkillCommand: "/sql @dg_rtt", RawSQL: "SELECT dest_id, transmit_mode, affirm, net_timeout FROM V$ARCHIVE_DEST WHERE dest_id = 2"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "RTT 5-20ms,SYNC模式有一定性能开销",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Data Guard SYNC模式RTT在5-20ms,对事务延迟有一定影响"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "评估业务对commit延迟的容忍度", SkillCommand: "/waits", RawSQL: "SELECT event, average_wait*10 avg_wait_ms FROM V$SYSTEM_EVENT WHERE event = 'log file sync'"},
						{Type: ActionPrevent, Desc: "考虑ASYNC+AFFIRM或Far Sync方案", SkillCommand: "/sql @dg_config", RawSQL: "SELECT dest_id, destination, transmit_mode, affirm, async_blocks FROM V$ARCHIVE_DEST WHERE status = 'VALID' AND target = 'STANDBY'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "RTT正常(< 5ms)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SYNC模式RTT在合理范围(<5ms),对性能影响较小"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"dataguard", "ha", "sync", "latency", "commit"},
		Versions: "9i+",
	}
}

// ruleDGSwitchoverPrecheck validates switchover_status in V$DATABASE before
// performing a Data Guard switchover operation.
func ruleDGSwitchoverPrecheck() *Rule {
	return &Rule{
		ID:       "dg_switchover_precheck",
		Name:     "Data Guard切换前检查(Switchover Precheck)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGSwitchover},
			{Type: SignalKeyword, Key: "switchover"},
			{Type: SignalKeyword, Key: "switchover precheck"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGSwitchover, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$DATABASE.SWITCHOVER_STATUS",
			Query: QueryDGSwitchoverReady,
			Branches: []Branch{
				{
					Match:    MatchEquals("TO STANDBY"),
					Label:    "主库可切换为备库(TO STANDBY)",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "验证备库switchover_status",
						Query: QueryDGStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("TO PRIMARY"),
								Label:    "备库可切换为主库,switchover就绪",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "主备库switchover_status均正常,可安全执行切换"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "执行主库switchover", SkillCommand: "/sql @dg_switchover", RawSQL: "ALTER DATABASE COMMIT TO SWITCHOVER TO STANDBY WITH SESSION SHUTDOWN", Risk: "所有连接将断开,应用需要重连", Rollback: "如果切换中断,参考MOS Doc ID 1304939.1"},
									{Type: ActionPrevent, Desc: "切换后验证新主库状态", SkillCommand: "/sql @dg_verify", RawSQL: "SELECT database_role, open_mode, switchover_status FROM V$DATABASE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "备库switchover_status异常",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "主库ready但备库switchover_status不是'TO PRIMARY',需先解决备库问题"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查备库switchover_status详情", SkillCommand: "/sql @standby_status", RawSQL: "SELECT database_role, switchover_status, supplemental_log_data_min FROM V$DATABASE"},
									{Type: ActionFix, Desc: "确保备库redo已完全应用", SkillCommand: "/sql @mrp_progress", RawSQL: "SELECT process, status, thread#, sequence# FROM V$MANAGED_STANDBY WHERE process = 'MRP0'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("SESSIONS ACTIVE"),
					Label:    "有活跃会话阻止切换",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "Switchover_status='SESSIONS ACTIVE',需WITH SESSION SHUTDOWN或先断开会话"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "使用WITH SESSION SHUTDOWN强制切换", SkillCommand: "/sql @dg_switchover_force", RawSQL: "ALTER DATABASE COMMIT TO SWITCHOVER TO STANDBY WITH SESSION SHUTDOWN"},
						{Type: ActionInvestigate, Desc: "查看活跃会话", SkillCommand: "/sessions", RawSQL: "SELECT sid, serial#, username, status, program FROM V$SESSION WHERE type = 'USER' AND status = 'ACTIVE'"},
					},
				},
				{
					Match:    MatchEquals("NOT ALLOWED"),
					Label:    "switchover不允许",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "SWITCHOVER_STATUS='NOT ALLOWED',存在严重问题阻止切换"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查DG配置和gap状态", SkillCommand: "/sql @dg_full_status", RawSQL: "SELECT dest_id, status, error, gap_status FROM V$ARCHIVE_DEST_STATUS WHERE type = 'PHYSICAL'"},
						{Type: ActionFix, Desc: "解决问题后重新验证switchover就绪", SkillCommand: "/sql @dg_validate", RawSQL: "SELECT database_role, protection_mode, protection_level, switchover_status FROM V$DATABASE"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "其他switchover状态",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "Switchover_status需进一步确认"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前switchover完整状态", SkillCommand: "/sql @dg_status", RawSQL: "SELECT database_role, open_mode, protection_mode, switchover_status, dataguard_broker FROM V$DATABASE"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"dataguard", "ha", "switchover", "precheck"},
		Versions: "10g+",
	}
}

// ruleDGStandbyRedoLog validates standby redo log (SRL) count and size
// configuration to ensure proper redo receive on standby.
func ruleDGStandbyRedoLog() *Rule {
	return &Rule{
		ID:       "dg_standby_redo_log",
		Name:     "Data Guard Standby Redo Log配置检查(SRL Validation)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGSRLCount},
			{Type: SignalKeyword, Key: "standby redo log"},
			{Type: SignalKeyword, Key: "SRL"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGSRLCount, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Standby Redo Log组数和大小",
			Query: QueryDGStandbyRedoLog,
			Branches: []Branch{
				{
					Match:    MatchEquals("NO_SRL"),
					Label:    "未配置Standby Redo Log",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "备库未配置Standby Redo Log (SRL),SYNC模式下性能受损,RFS将使用archived log直接写入"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "创建SRL(每个thread的ORL组数+1)", SkillCommand: "/sql @create_srl", RawSQL: "ALTER DATABASE ADD STANDBY LOGFILE THREAD 1 GROUP {n} ('{path}') SIZE {size}M", Risk: "需要足够磁盘空间", Rollback: "ALTER DATABASE DROP STANDBY LOGFILE GROUP {n}"},
						{Type: ActionInvestigate, Desc: "检查当前ORL配置确定SRL数量", SkillCommand: "/sql @orl_info", RawSQL: "SELECT thread#, group#, bytes/1024/1024 size_mb, members FROM V$LOG ORDER BY thread#, group#"},
					},
				},
				{
					Match:    MatchEquals("SIZE_MISMATCH"),
					Label:    "SRL大小与ORL不匹配",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Standby Redo Log大小与Online Redo Log不一致,Oracle建议两者大小相同"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "重建SRL使大小匹配ORL", SkillCommand: "/sql @recreate_srl", RawSQL: "-- 先DROP再ADD: ALTER DATABASE DROP STANDBY LOGFILE GROUP {n}; ALTER DATABASE ADD STANDBY LOGFILE GROUP {n} ('{path}') SIZE {orl_size}M"},
						{Type: ActionInvestigate, Desc: "对比ORL和SRL大小", SkillCommand: "/sql @compare_logs", RawSQL: "SELECT 'ORL' log_type, group#, bytes/1024/1024 size_mb FROM V$LOG UNION ALL SELECT 'SRL', group#, bytes/1024/1024 FROM V$STANDBY_LOG ORDER BY 1, 2"},
					},
				},
				{
					Match:    MatchEquals("COUNT_LOW"),
					Label:    "SRL组数不足",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "SRL组数少于推荐值(应为每个thread的ORL组数+1),可能导致RFS等待"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "添加额外SRL组", SkillCommand: "/sql @add_srl", RawSQL: "ALTER DATABASE ADD STANDBY LOGFILE THREAD {thread} GROUP {n} ('{path}') SIZE {size}M"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "SRL配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Standby Redo Log配置正确,数量和大小符合最佳实践"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"dg_transport_lag"},
		Tags:     []string{"dataguard", "ha", "srl", "redo"},
		Versions: "9i+",
	}
}

// ruleDGBrokerWarning detects Data Guard broker (DGMGRL) configuration
// warnings such as status != SUCCESS on database members.
func ruleDGBrokerWarning() *Rule {
	return &Rule{
		ID:       "dg_broker_warning",
		Name:     "Data Guard Broker告警(Broker Warning)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGBrokerWarn},
			{Type: SignalKeyword, Key: "broker warning"},
			{Type: SignalKeyword, Key: "DGMGRL"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGBrokerWarn, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DG Broker配置中数据库状态",
			Query: QueryDGBrokerConfig,
			Branches: []Branch{
				{
					Match:    MatchEquals("ERROR"),
					Label:    "Broker成员处于ERROR状态",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查具体ERROR详情",
						Query: QueryDGStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("ORA-16766"),
								Label:    "Redo Apply关闭(ORA-16766)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "DG Broker报告ORA-16766,备库redo apply已关闭"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "通过Broker重新启用apply", SkillCommand: "/sql @broker_enable_apply", RawSQL: "-- DGMGRL: EDIT DATABASE '{standby_name}' SET STATE='APPLY-ON';"},
									{Type: ActionInvestigate, Desc: "检查Broker配置详情", SkillCommand: "/sql @broker_show", RawSQL: "SELECT fs_failover_status, fs_failover_observer_present, fs_failover_observer_host FROM V$FS_FAILOVER_STATS"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他Broker错误",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "DG Broker配置存在错误,需通过DGMGRL show database排查"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "使用DGMGRL查看完整状态", SkillCommand: "/sql @broker_full_status", RawSQL: "-- DGMGRL: SHOW CONFIGURATION VERBOSE; SHOW DATABASE VERBOSE '{db_name}';"},
									{Type: ActionFix, Desc: "尝试disable/enable修复", SkillCommand: "/sql @broker_reset", RawSQL: "-- DGMGRL: DISABLE DATABASE '{db_name}'; ENABLE DATABASE '{db_name}';", Risk: "disable会暂停log shipping"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("WARNING"),
					Label:    "Broker成员有WARNING",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "DG Broker配置存在警告,可能影响自动failover功能"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看Broker完整配置和警告详情", SkillCommand: "/sql @broker_config", RawSQL: "-- DGMGRL: SHOW CONFIGURATION; SHOW DATABASE '{db_name}';"},
						{Type: ActionPrevent, Desc: "检查Fast-Start Failover的observer状态", SkillCommand: "/sql @fsfo_status", RawSQL: "SELECT fs_failover_status, fs_failover_observer_present, fs_failover_observer_host FROM V$FS_FAILOVER_STATS"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Broker配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Data Guard Broker配置正常,所有成员状态为SUCCESS"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"dg_mrp_stopped", "dg_transport_lag"},
		CausesOf: []string{},
		Tags:     []string{"dataguard", "ha", "broker", "dgmgrl"},
		Versions: "10g+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RAC Rules (7)
// ═══════════════════════════════════════════════════════════════════════════

func haRACRules() []*Rule {
	return []*Rule{
		ruleRACNodeEviction(),
		ruleRACInterconnectSlow(),
		ruleRACDRMThrashing(),
		ruleRACServiceImbalance(),
		ruleRACGCContentionHigh(),
		ruleRACSplitBrain(),
		ruleRACInstanceRecovery(),
	}
}

// ruleRACNodeEviction detects CSS/CSSD node eviction events where misscount
// exceeds 30 seconds threshold, caused by network, OS, storage, or voting disk.
func ruleRACNodeEviction() *Rule {
	return &Rule{
		ID:       "rac_node_eviction",
		Name:     "RAC节点驱逐(Node Eviction)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACMisscount},
			{Type: SignalErrorCode, Key: "ORA-29740"},
			{Type: SignalKeyword, Key: "node eviction"},
			{Type: SignalKeyword, Key: "CSS misscount"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACMisscount, Op: OpGTE, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查CSS/CSSD心跳和misscount",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricRACMisscount) },
			Branches: []Branch{
				{
					Match:    MatchGTE(30),
					Label:    "misscount >= 30s, 节点已被/将被驱逐",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查导致eviction的根因类别",
						Query: QueryRACInterconnect,
						Branches: []Branch{
							{
								Match:    MatchEquals("NETWORK"),
								Label:    "私有网络故障导致eviction",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC节点因私有互联网络超时被驱逐,CSS misscount超过30秒"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查互联网卡和交换机状态", SkillCommand: "/sql @rac_interconnect", RawSQL: "SELECT inst_id, name, ip_address, is_public FROM GV$CLUSTER_INTERCONNECTS"},
									{Type: ActionFix, Desc: "确认interconnect冗余配置(bonding/HAIP)", SkillCommand: "/sql @rac_haip", RawSQL: "SELECT name, value FROM GV$PARAMETER WHERE name = 'cluster_interconnects'"},
									{Type: ActionPrevent, Desc: "检查CSS misscount和diagwait参数", SkillCommand: "/sql @css_params", RawSQL: "-- crsctl get css misscount; crsctl get css diagwait"},
								},
							},
							{
								Match:    MatchEquals("STORAGE"),
								Label:    "存储/voting disk故障导致eviction",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC节点因voting disk访问超时被驱逐,存储I/O可能存在问题"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查voting disk状态和I/O延迟", SkillCommand: "/sql @voting_disk", RawSQL: "-- crsctl query css votedisk; dd检查I/O延迟"},
									{Type: ActionFix, Desc: "检查ASM磁盘组和存储链路", SkillCommand: "/sql @asm_diskgroup", RawSQL: "SELECT name, state, type, total_mb, free_mb FROM V$ASM_DISKGROUP"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "OS层面(CPU/内存/调度)导致eviction",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC节点eviction可能因OS资源耗尽(CPU 100%/内存swap/进程调度延迟)"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查OS层面资源使用", SkillCommand: "/sql @os_stats", RawSQL: "SELECT inst_id, stat_name, value FROM GV$OSSTAT WHERE stat_name IN ('NUM_CPUS','IDLE_TIME','BUSY_TIME','AVG_IDLE_TIME')"},
									{Type: ActionPrevent, Desc: "确保Oracle进程使用RT调度优先级", SkillCommand: "/sql @rac_config", RawSQL: "-- 检查/etc/security/limits.conf和hugepages配置"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "misscount正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "CSS心跳正常,无eviction风险"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rac_instance_recovery"},
		Tags:     []string{"rac", "ha", "eviction", "css", "clusterware"},
		Versions: "10g+",
	}
}

// ruleRACInterconnectSlow detects gc block receive time > 1ms, indicating
// interconnect bandwidth or MTU configuration issues.
func ruleRACInterconnectSlow() *Rule {
	return &Rule{
		ID:       "rac_interconnect_slow",
		Name:     "RAC互联网络慢(Interconnect Slow)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACGCAvgMs},
			{Type: SignalWaitEvent, Key: "gc cr block busy"},
			{Type: SignalWaitEvent, Key: "gc current block busy"},
			{Type: SignalKeyword, Key: "interconnect slow"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACGCAvgMs, Op: OpGT, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查gc block receive平均时间",
			Query: QueryInterconnectStats,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "gc block receive > 5ms,互联网络严重瓶颈",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查interconnect MTU和带宽配置",
						Query: QueryRACInterconnect,
						Branches: []Branch{
							{
								Match:    MatchLT(9000),
								Label:    "MTU < 9000(未启用Jumbo Frame)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC interconnect gc block receive > 5ms,且未启用Jumbo Frame(MTU < 9000),网络传输效率低"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "启用Jumbo Frame(MTU=9000)", SkillCommand: "/sql @rac_mtu", RawSQL: "-- ifconfig {iface} mtu 9000; 交换机端也需配置Jumbo Frame", Risk: "需要网络短暂中断,建议维护窗口操作", Rollback: "ifconfig {iface} mtu 1500"},
									{Type: ActionInvestigate, Desc: "检查interconnect带宽利用率", SkillCommand: "/sql @rac_traffic", RawSQL: "SELECT inst_id, name, value FROM GV$SYSSTAT WHERE name LIKE 'gc%' AND name LIKE '%received%' ORDER BY inst_id, name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "MTU正常,可能带宽不足",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Jumbo Frame已启用但gc延迟仍高,可能为interconnect带宽不足或交换机瓶颈"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查interconnect流量是否接近带宽上限", SkillCommand: "/sql @rac_bandwidth", RawSQL: "SELECT inst_id, name, value FROM GV$SYSSTAT WHERE name IN ('gc cr blocks received', 'gc current blocks received', 'gc cr block receive time', 'gc current block receive time') ORDER BY inst_id, name"},
									{Type: ActionFix, Desc: "考虑升级到10Gb/25Gb interconnect", SkillCommand: "/sql @rac_interconnect", RawSQL: "SELECT inst_id, name, ip_address, is_public FROM GV$CLUSTER_INTERCONNECTS"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(1),
					Label:    "gc block receive 1-5ms,有优化空间",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "RAC interconnect gc block receive在1-5ms,略高于最佳实践(<1ms)"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查gc等待事件细分", SkillCommand: "/waits", RawSQL: "SELECT event, total_waits, time_waited_micro/NULLIF(total_waits,0)/1000 avg_ms FROM V$SYSTEM_EVENT WHERE event LIKE 'gc%' AND wait_class <> 'Idle' ORDER BY time_waited_micro DESC FETCH FIRST 10 ROWS ONLY"},
						{Type: ActionPrevent, Desc: "检查是否有hot block需要redistribute", SkillCommand: "/sql @gc_hot_blocks", RawSQL: "SELECT current_obj#, o.object_name, COUNT(*) cnt FROM V$ACTIVE_SESSION_HISTORY ash LEFT JOIN DBA_OBJECTS o ON ash.current_obj# = o.object_id WHERE ash.event LIKE 'gc%' AND ash.sample_time > SYSDATE - 1/24 GROUP BY current_obj#, o.object_name ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "interconnect延迟正常(< 1ms)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RAC互联网络延迟正常,gc block receive < 1ms"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rac_gc_contention_high"},
		Tags:     []string{"rac", "ha", "interconnect", "gc", "network"},
		Versions: "10g+",
	}
}

// ruleRACDRMThrashing detects frequent DRM (Dynamic Resource Management)
// remastering events causing global cache contention spikes.
func ruleRACDRMThrashing() *Rule {
	return &Rule{
		ID:       "rac_drm_thrashing",
		Name:     "RAC DRM抖动(DRM Remaster Thrashing)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACDRMRemaster},
			{Type: SignalWaitEvent, Key: "gc remaster"},
			{Type: SignalKeyword, Key: "DRM thrashing"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACDRMRemaster, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DRM remaster事件频率",
			Query: QueryRACDRMStats,
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    "DRM remaster > 20次/小时,严重抖动",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查remaster涉及的对象和实例",
						Query: QueryRACGCStats,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "热点对象频繁remaster",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "DRM频繁对热点对象进行remaster导致gc等待飙升,建议禁用DRM或增加_gc_policy_time"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "临时禁用DRM停止抖动", SkillCommand: "/sql @disable_drm", RawSQL: "ALTER SYSTEM SET \"_gc_policy_time\" = 0 SCOPE=BOTH", Risk: "禁用DRM后资源分配不再自动优化", Rollback: "ALTER SYSTEM SET \"_gc_policy_time\" = 10 SCOPE=BOTH"},
									{Type: ActionFix, Desc: "增大_gc_policy_time减少remaster频率", SkillCommand: "/params _gc_policy_time", RawSQL: "ALTER SYSTEM SET \"_gc_policy_time\" = 20 SCOPE=BOTH"},
									{Type: ActionPrevent, Desc: "对频繁remaster的对象使用affinity", SkillCommand: "/sql @drm_affinity", RawSQL: "SELECT object_id, current_master, previous_master, remaster_cnt FROM GV$GCSPFMASTER_INFO WHERE remaster_cnt > 0 ORDER BY remaster_cnt DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "DRM抖动但无明确热点",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "DRM remaster频繁但未找到集中热点对象,可能为整体workload imbalance"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "设置_gc_policy_time增大remaster间隔", SkillCommand: "/params _gc_policy_time", RawSQL: "ALTER SYSTEM SET \"_gc_policy_time\" = 20 SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "DRM remaster 5-20次/小时,需观察",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "DRM remaster频率偏高,可能引起间歇性gc性能波动"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查DRM remaster历史", SkillCommand: "/sql @drm_history", RawSQL: "SELECT inst_id, event, total_waits, time_waited_micro/1000 time_ms FROM GV$SYSTEM_EVENT WHERE event LIKE '%remaster%' ORDER BY time_waited_micro DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "DRM remaster正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "DRM remaster频率在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"rac_service_imbalance"},
		CausesOf: []string{"rac_gc_contention_high"},
		Tags:     []string{"rac", "ha", "drm", "remaster", "gc"},
		Versions: "10g+",
	}
}

// ruleRACServiceImbalance detects session count imbalance > 30% across
// RAC instances, indicating service or load balancing misconfiguration.
func ruleRACServiceImbalance() *Rule {
	return &Rule{
		ID:       "rac_service_imbalance",
		Name:     "RAC实例负载不均衡(Service Imbalance)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACSessionImbalance},
			{Type: SignalKeyword, Key: "service imbalance"},
			{Type: SignalKeyword, Key: "load balance"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACSessionImbalance, Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查各实例会话数分布",
			Query: QueryRACServiceStats,
			Branches: []Branch{
				{
					Match:    MatchGT(60),
					Label:    "imbalance > 60%, 严重不均衡",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查service配置和connection load balancing",
						Query: QueryRACInstances,
						Branches: []Branch{
							{
								Match:    MatchEquals("SINGLE_SERVICE"),
								Label:    "所有连接使用同一service/实例",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RAC实例负载严重不均衡(>60%),所有连接集中在一个实例上,未配置多实例service"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "创建多实例service并配置CLB", SkillCommand: "/sql @rac_service", RawSQL: "-- srvctl add service -db {db} -service {svc} -preferred {inst1},{inst2} -clbgoal LONG -rlbgoal SERVICE_TIME"},
									{Type: ActionFix, Desc: "配置客户端SCAN负载均衡", SkillCommand: "/sql @scan_config", RawSQL: "-- TNS连接串使用SCAN地址并设置LOAD_BALANCE=YES"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "有多实例service但分布不均",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "RAC有多实例service但负载分布不均,需检查CLB/RLB配置"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查service负载均衡配置", SkillCommand: "/sql @rac_service_config", RawSQL: "SELECT inst_id, name, goal, clb_goal, aq_ha_notifications FROM GV$SERVICES WHERE name NOT IN ('SYS$BACKGROUND','SYS$USERS')"},
									{Type: ActionFix, Desc: "调整CLB GOAL为SERVICE_TIME", SkillCommand: "/sql @update_service", RawSQL: "-- srvctl modify service -db {db} -service {svc} -clbgoal LONG -rlbgoal SERVICE_TIME"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(30),
					Label:    "imbalance 30-60%,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "RAC实例间会话数差异>30%,存在一定程度的负载不均衡"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查各实例会话和负载分布", SkillCommand: "/sql @rac_load", RawSQL: "SELECT inst_id, COUNT(*) session_cnt, SUM(CASE WHEN status='ACTIVE' THEN 1 ELSE 0 END) active_cnt FROM GV$SESSION WHERE type = 'USER' GROUP BY inst_id ORDER BY inst_id"},
						{Type: ActionPrevent, Desc: "确认RLB advisory正常工作", SkillCommand: "/sql @rlb_check", RawSQL: "SELECT inst_id, name, goal, clb_goal FROM GV$SERVICES WHERE name NOT LIKE 'SYS$%'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "实例负载均衡正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RAC各实例会话分布均衡"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rac_drm_thrashing"},
		Tags:     []string{"rac", "ha", "service", "loadbalance"},
		Versions: "10g+",
	}
}

// ruleRACGCContentionHigh detects gc waits exceeding 15% of total DB Time
// across the cluster, indicating excessive cross-instance block shipping.
func ruleRACGCContentionHigh() *Rule {
	return &Rule{
		ID:       "rac_gc_contention_high",
		Name:     "RAC GC争用过高(GC Contention High)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACGCWaitPct},
			{Type: SignalWaitEvent, Key: "gc buffer busy acquire"},
			{Type: SignalWaitEvent, Key: "gc buffer busy release"},
			{Type: SignalKeyword, Key: "gc contention"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACGCWaitPct, Op: OpGT, Value: 15},
			},
		},
		Tree: &TreeNode{
			Step:  "检查gc等待事件占DB Time比例",
			Query: QueryRACGCStats,
			Branches: []Branch{
				{
					Match:    MatchGT(40),
					Label:    "gc waits > 40% DB Time,极度争用",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "定位gc争用的热点对象",
						Query: QueryASHHotObject,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在gc争用热点对象",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC gc等待占DB Time > 40%,存在热点对象导致大量跨实例block shipping"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "对热点表进行Sequence Cache或反转键索引优化", SkillCommand: "/sql @gc_hot_fix", RawSQL: "-- 热点为index right-growing: ALTER INDEX {idx} REBUILD REVERSE; 热点为sequence: ALTER SEQUENCE {seq} CACHE 1000"},
									{Type: ActionFix, Desc: "考虑将热点对象的访问集中到单一实例(Service Partition)", SkillCommand: "/sql @partition_service", RawSQL: "-- srvctl modify service -db {db} -service {hot_svc} -preferred {inst1} -available {inst2}"},
									{Type: ActionInvestigate, Desc: "检查ASH中gc等待的具体block类型", SkillCommand: "/sql @gc_block_class", RawSQL: "SELECT DECODE(ash.p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block', 4,'segment_header', 'other') block_class, COUNT(*) cnt FROM V$ACTIVE_SESSION_HISTORY ash WHERE ash.event LIKE 'gc%' AND ash.sample_time > SYSDATE - 1/24 GROUP BY ash.p3 ORDER BY cnt DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "gc高但无明确热点",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "gc等待极高但无集中热点,可能为整体workload cross-instance交互过多"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "重新设计service partition减少跨实例访问", SkillCommand: "/sql @rac_design", RawSQL: "SELECT inst_id, service_name, COUNT(*) cnt FROM GV$SESSION WHERE type = 'USER' GROUP BY inst_id, service_name ORDER BY inst_id"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(15),
					Label:    "gc waits 15-40% DB Time,明显争用",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "RAC gc等待占DB Time 15-40%,跨实例block shipping偏多"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "定位gc等待热点对象", SkillCommand: "/sql @gc_hot_objects", RawSQL: "SELECT current_obj#, o.object_name, o.object_type, COUNT(*) cnt FROM V$ACTIVE_SESSION_HISTORY ash LEFT JOIN DBA_OBJECTS o ON ash.current_obj# = o.object_id WHERE ash.event LIKE 'gc%' AND ash.sample_time > SYSDATE - 1/24 GROUP BY current_obj#, o.object_name, o.object_type ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"},
						{Type: ActionFix, Desc: "检查并优化interconnect配置", SkillCommand: "/sql @rac_interconnect", RawSQL: "SELECT inst_id, name, ip_address, is_public FROM GV$CLUSTER_INTERCONNECTS"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "gc争用正常(< 15% DB Time)",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RAC gc等待占比正常,跨实例交互在合理范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"rac_interconnect_slow", "rac_drm_thrashing"},
		CausesOf: []string{},
		Tags:     []string{"rac", "ha", "gc", "contention", "cache_fusion"},
		Versions: "10g+",
	}
}

// ruleRACSplitBrain detects RAC network partition (split brain) scenarios
// where cluster nodes lose interconnect communication.
func ruleRACSplitBrain() *Rule {
	return &Rule{
		ID:       "rac_split_brain",
		Name:     "RAC脑裂检测(Split Brain)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACNetPartition},
			{Type: SignalErrorCode, Key: "ORA-29740"},
			{Type: SignalKeyword, Key: "split brain"},
			{Type: SignalKeyword, Key: "network partition"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACNetPartition, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查集群节点通信状态",
			Query: QueryRACInstances,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "检测到节点间通信异常",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查voting disk仲裁结果",
						Query: QueryRACInterconnect,
						Branches: []Branch{
							{
								Match:    MatchEquals("PARTITION"),
								Label:    "确认网络分区,少数派节点将被驱逐",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC发生网络分区(split brain),Oracle CSS通过voting disk进行仲裁,少数派节点将被驱逐"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查被驱逐节点状态和原因", SkillCommand: "/sql @cluster_status", RawSQL: "SELECT inst_id, instance_name, host_name, status, startup_time FROM GV$INSTANCE ORDER BY inst_id"},
									{Type: ActionUrgent, Desc: "检查互联网络物理连接", SkillCommand: "/sql @interconnect_check", RawSQL: "SELECT inst_id, name, ip_address, is_public FROM GV$CLUSTER_INTERCONNECTS"},
									{Type: ActionFix, Desc: "恢复网络后验证集群完整性", SkillCommand: "/sql @cluster_verify", RawSQL: "-- cluvfy comp nodecon -n all; crsctl check cluster -all"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "通信抖动但未完全分区",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RAC互联网络存在间歇性通信中断,有split brain风险"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查interconnect网络丢包和延迟", SkillCommand: "/sql @net_check", RawSQL: "-- ping -c 100 {peer_ip}; 检查丢包率和延迟"},
									{Type: ActionPrevent, Desc: "配置interconnect冗余(bonding或HAIP)", SkillCommand: "/sql @haip_config", RawSQL: "-- oifcfg getif; oifcfg setif -global {iface}:169.254.x.x:cluster_interconnect"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无网络分区",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RAC集群节点间通信正常,无split brain风险"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rac_node_eviction"},
		Tags:     []string{"rac", "ha", "split_brain", "network_partition", "voting_disk"},
		Versions: "10g+",
	}
}

// ruleRACInstanceRecovery detects instance crash events and estimates
// SMON recovery time based on redo to apply.
func ruleRACInstanceRecovery() *Rule {
	return &Rule{
		ID:       "rac_instance_recovery",
		Name:     "RAC实例恢复(Instance Recovery)",
		Category: "ha_rac",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACInstCrash},
			{Type: SignalErrorCode, Key: "ORA-01092"},
			{Type: SignalKeyword, Key: "instance recovery"},
			{Type: SignalKeyword, Key: "SMON recovery"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACInstCrash, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查实例状态和SMON恢复进度",
			Query: QueryRACInstances,
			Branches: []Branch{
				{
					Match:    MatchEquals("RECOVERY"),
					Label:    "SMON正在执行instance recovery",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查恢复进度和预计时间",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(300),
								Label:    "预计恢复时间 > 5分钟",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "RAC实例崩溃,SMON正在执行instance recovery,预计恢复时间较长(>5min)"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "监控SMON恢复进度", SkillCommand: "/sql @smon_progress", RawSQL: "SELECT sid, opname, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct, time_remaining FROM V$SESSION_LONGOPS WHERE opname LIKE '%Recovery%' AND sofar <> totalwork"},
									{Type: ActionInvestigate, Desc: "检查需要回滚的事务数", SkillCommand: "/sql @recovery_txn", RawSQL: "SELECT usn, state, undoblockstotal, undoblocksdone, undoblockstotal-undoblocksdone blocks_remaining, DECODE(cputime,0,0,ROUND(undoblockstotal/cputime*(undoblockstotal-undoblocksdone))) est_sec FROM V$FAST_START_TRANSACTIONS WHERE state <> 'RECOVERED'"},
									{Type: ActionPrevent, Desc: "优化fast_start_mttr_target减少未来恢复时间", SkillCommand: "/params fast_start_mttr_target", RawSQL: "ALTER SYSTEM SET fast_start_mttr_target = 300 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "恢复中,时间较短",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "RAC实例崩溃,SMON正在执行instance recovery,预计很快完成"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "监控恢复进度", SkillCommand: "/sql @smon_progress", RawSQL: "SELECT sid, opname, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct FROM V$SESSION_LONGOPS WHERE opname LIKE '%Recovery%'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("DOWN"),
					Label:    "实例已DOWN,尚未开始恢复",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "RAC实例崩溃且未自动重启,需人工干预"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查CRS并尝试重启实例", SkillCommand: "/sql @restart_instance", RawSQL: "-- srvctl start instance -db {db} -instance {inst}; srvctl status database -db {db}"},
						{Type: ActionInvestigate, Desc: "检查alert.log中崩溃原因", SkillCommand: "/alert", RawSQL: "SELECT message_text, originating_timestamp FROM V$DIAG_ALERT_EXT WHERE originating_timestamp > SYSDATE - 1/24 ORDER BY originating_timestamp DESC FETCH FIRST 50 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有实例正常运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "RAC所有实例正常运行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"rac_node_eviction", "rac_split_brain"},
		CausesOf: []string{},
		Tags:     []string{"rac", "ha", "recovery", "smon", "crash"},
		Versions: "10g+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Deep Emergency Rules (10)
// ═══════════════════════════════════════════════════════════════════════════

func deepEmergencyRules() []*Rule {
	return []*Rule{
		ruleORA7445(),
		ruleORA4031Emergency(),
		ruleORA1555Emergency(),
		ruleORA30036(),
		ruleTablespaceFullEmergency(),
		ruleArchiveFullHang(),
		ruleTempFullORA1652(),
		ruleDatafileCorruption(),
		ruleControlFileIssue(),
		ruleListenerDown(),
	}
}

// ruleORA7445 detects ORA-07445 (signal/access violation) internal errors,
// requiring MOS lookup and patch investigation.
func ruleORA7445() *Rule {
	return &Rule{
		ID:       "ora_7445",
		Name:     "ORA-07445内核异常(Signal/Access Violation)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-07445"},
			{Type: SignalMetric, Key: metricORA7445Count},
			{Type: SignalKeyword, Key: "ORA-7445"},
			{Type: SignalKeyword, Key: "access violation"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA7445Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ORA-07445出现频率和函数签名",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricORA7445Count) },
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "频繁ORA-7445(>3次),可能导致实例不稳定",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否同一函数签名(同一bug重复触发)",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(1),
								Label:    "同一函数签名重复出现",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-07445频繁出现(>3次),同一函数签名重复,几乎确定为已知Oracle bug"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "收集trace文件并在MOS搜索函数签名(Doc ID 153788.1)", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-07445%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC"},
									{Type: ActionUrgent, Desc: "使用ORA-7445 lookup tool确定补丁", SkillCommand: "/sql @ora7445_trace", RawSQL: "SELECT value FROM V$DIAG_INFO WHERE name = 'Default Trace File'"},
									{Type: ActionFix, Desc: "应用对应one-off patch或PSU", SkillCommand: "/sql @oracle_version", RawSQL: "SELECT banner_full FROM V$VERSION"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "不同函数签名,多种bug",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "多种不同的ORA-07445,系统严重不稳定,可能需要紧急打补丁或升级"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "开SR联系Oracle Support紧急处理", SkillCommand: "/alert", RawSQL: "SELECT message_text, originating_timestamp FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-07445%' ORDER BY originating_timestamp DESC FETCH FIRST 30 ROWS ONLY"},
									{Type: ActionFix, Desc: "尽快升级到最新PSU/RU", SkillCommand: "/sql @oracle_patches", RawSQL: "SELECT action_time, action, namespace, version, comments FROM DBA_REGISTRY_SQLPATCH ORDER BY action_time DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "偶发ORA-7445(1-3次)",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "出现ORA-07445内核异常,需收集trace文件定位具体函数和补丁"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "收集incident trace和core dump", SkillCommand: "/alert", RawSQL: "SELECT value FROM V$DIAG_INFO WHERE name IN ('Default Trace File', 'Diag Trace')"},
						{Type: ActionFix, Desc: "在MOS Doc ID 153788.1查询已知bug和补丁", SkillCommand: "/sql @oracle_version", RawSQL: "SELECT banner_full FROM V$VERSION"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无ORA-7445",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到ORA-07445"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-7445", "signal", "kernel", "patch"},
		Versions: "9i+",
	}
}

// ruleORA4031Emergency detects ORA-04031 (shared pool OOM) requiring
// immediate shared pool flush and root cause investigation.
func ruleORA4031Emergency() *Rule {
	return &Rule{
		ID:       "ora_4031_emergency",
		Name:     "ORA-04031共享池内存耗尽(Shared Pool OOM)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-04031"},
			{Type: SignalMetric, Key: metricORA4031Count},
			{Type: SignalMetric, Key: metricSharedPoolFreePct},
			{Type: SignalKeyword, Key: "ORA-4031"},
			{Type: SignalKeyword, Key: "shared pool OOM"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA4031Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查shared pool free memory百分比",
			Query: QuerySPFreeMemory,
			Branches: []Branch{
				{
					Match:    MatchLT(5),
					Label:    "shared pool free < 5%,严重不足",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有literal SQL导致碎片化",
						Query: QueryParseStats,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "hard parse rate > 100/s,literal SQL引起",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-04031:shared pool free < 5%,hard parse > 100/s,大量literal SQL导致内存碎片化"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即flush shared_pool缓解", SkillCommand: "/sql @flush_sp", RawSQL: "ALTER SYSTEM FLUSH SHARED_POOL", Risk: "会清除所有缓存的执行计划,短暂性能下降", Rollback: "无法rollback,执行计划会自动重新缓存"},
									{Type: ActionUrgent, Desc: "设置cursor_sharing=FORCE", SkillCommand: "/params cursor_sharing", RawSQL: "ALTER SYSTEM SET cursor_sharing = 'FORCE' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "长期:修改应用使用绑定变量", SkillCommand: "/slowsql", RawSQL: "SELECT force_matching_signature, COUNT(*) cnt, MIN(sql_id) sample_sql_id FROM V$SQL WHERE force_matching_signature > 0 GROUP BY force_matching_signature HAVING COUNT(*) > 50 ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非literal SQL引起",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-04031:shared pool耗尽但非literal SQL引起,可能为shared pool配置过小或内存泄漏"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "flush shared pool紧急缓解", SkillCommand: "/sql @flush_sp", RawSQL: "ALTER SYSTEM FLUSH SHARED_POOL"},
									{Type: ActionFix, Desc: "增大shared_pool_size", SkillCommand: "/params shared_pool_size", RawSQL: "ALTER SYSTEM SET shared_pool_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查shared pool内存分配详情", SkillCommand: "/sql @sp_detail", RawSQL: "SELECT pool, name, bytes/1024/1024 mb FROM V$SGASTAT WHERE pool = 'shared pool' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(15),
					Label:    "shared pool free 5-15%,偏低",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ORA-04031发生,shared pool free在5-15%,有持续OOM风险"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "分析shared pool内存消耗分布", SkillCommand: "/sql @sp_analysis", RawSQL: "SELECT pool, name, bytes/1024/1024 mb FROM V$SGASTAT WHERE pool = 'shared pool' AND bytes > 1048576 ORDER BY bytes DESC"},
						{Type: ActionFix, Desc: "增大shared_pool_size或启用ASMM", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET sga_target = '{new_size}' SCOPE=BOTH"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "shared pool free > 15%",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "ORA-04031发生但shared pool free尚有空间,可能为特定大内存分配请求失败"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查ORA-04031中请求的内存大小", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-04031%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{"hard_parse_storm"},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-4031", "shared_pool", "oom", "memory"},
		Versions: "9i+",
	}
}

// ruleORA1555Emergency detects ORA-01555 (snapshot too old) caused by
// undo retention/space issues or long-running queries.
func ruleORA1555Emergency() *Rule {
	return &Rule{
		ID:       "ora_1555_emergency",
		Name:     "ORA-01555快照过旧(Snapshot Too Old)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01555"},
			{Type: SignalMetric, Key: metricORA1555Count},
			{Type: SignalMetric, Key: metricUndoUsedPct},
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
			Step:  "检查undo表空间使用率和undo_retention设置",
			Query: QueryUndoStats,
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "undo使用率 > 90%,空间不足",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有大事务占用undo",
						Query: QueryLongTransactions,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在长事务消耗大量undo",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01555:undo使用>90%,存在长事务大量消耗undo空间,导致其他查询快照过旧"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "评估是否可以终止长事务释放undo", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, s.sql_id, t.used_ublk*8/1024 undo_mb, ROUND((SYSDATE-TO_DATE(t.start_time,'MM/DD/YY HH24:MI:SS'))*1440) elapsed_min FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr ORDER BY t.used_ublk DESC"},
									{Type: ActionFix, Desc: "扩展undo表空间", SkillCommand: "/sql @extend_undo", RawSQL: "ALTER TABLESPACE {undo_ts} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
									{Type: ActionFix, Desc: "增大undo_retention", SkillCommand: "/params undo_retention", RawSQL: "ALTER SYSTEM SET undo_retention = 1800 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无长事务但undo空间不足",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01555:undo空间不足但无异常长事务,undo表空间本身可能过小"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "扩展undo表空间", SkillCommand: "/sql @extend_undo", RawSQL: "ALTER TABLESPACE {undo_ts} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
									{Type: ActionFix, Desc: "增大undo_retention", SkillCommand: "/params undo_retention", RawSQL: "ALTER SYSTEM SET undo_retention = 3600 SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "undo空间充足但仍出现ORA-1555",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有查询执行时间过长",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在长时间运行的查询",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01555:undo空间充足,但有查询运行时间超过undo_retention,需优化SQL或增大retention"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "优化长时间查询减少执行时间", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT sql_id, elapsed_time/1000000 elapsed_sec, executions FROM V$SQL WHERE elapsed_time/NULLIF(executions,0) > 1800000000 ORDER BY elapsed_time DESC FETCH FIRST 10 ROWS ONLY"},
									{Type: ActionFix, Desc: "增大undo_retention匹配最长查询", SkillCommand: "/params undo_retention", RawSQL: "ALTER SYSTEM SET undo_retention = 7200 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "需进一步分析ORA-1555原因",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "ORA-01555发生但常见原因均未匹配,需查看alert.log详细分析"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查alert.log中ORA-01555详情", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01555%' AND originating_timestamp > SYSDATE - 7 ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-1555", "undo", "snapshot", "retention"},
		Versions: "9i+",
	}
}

// ruleORA30036 detects ORA-30036 (unable to extend undo segment) when
// undo tablespace has RETENTION GUARANTEE and is completely full.
func ruleORA30036() *Rule {
	return &Rule{
		ID:       "ora_30036",
		Name:     "ORA-30036 Undo空间耗尽(Undo Segment Full)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-30036"},
			{Type: SignalMetric, Key: metricORA30036Count},
			{Type: SignalMetric, Key: metricUndoUsedPct},
			{Type: SignalKeyword, Key: "ORA-30036"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA30036Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查undo表空间使用率和RETENTION GUARANTEE",
			Query: QueryUndoStats,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "undo使用率 > 95%,接近100%满",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否启用了RETENTION GUARANTEE",
						Query: QueryUndoSegmentRatio,
						Branches: []Branch{
							{
								Match:    MatchEquals("GUARANTEE"),
								Label:    "RETENTION GUARANTEE已启用,DML被阻塞",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-30036: undo表空间已满且启用RETENTION GUARANTEE,所有DML操作失败"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "临时取消RETENTION GUARANTEE", SkillCommand: "/sql @undo_noguarantee", RawSQL: "ALTER TABLESPACE {undo_ts} RETENTION NOGUARANTEE", Risk: "长查询可能遇到ORA-01555", Rollback: "ALTER TABLESPACE {undo_ts} RETENTION GUARANTEE"},
									{Type: ActionUrgent, Desc: "扩展undo表空间", SkillCommand: "/sql @extend_undo", RawSQL: "ALTER TABLESPACE {undo_ts} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
									{Type: ActionInvestigate, Desc: "查找占用最多undo的事务", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, t.used_ublk*8/1024 undo_mb FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr ORDER BY t.used_ublk DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无GUARANTEE但undo仍满",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-30036: undo表空间满,大量活跃事务消耗所有undo空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "扩展undo表空间", SkillCommand: "/sql @extend_undo", RawSQL: "ALTER TABLESPACE {undo_ts} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
									{Type: ActionFix, Desc: "检查并终止异常长事务", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, t.used_ublk*8/1024 undo_mb, ROUND((SYSDATE-TO_DATE(t.start_time,'MM/DD/YY HH24:MI:SS'))*1440) elapsed_min FROM V$TRANSACTION t JOIN V$SESSION s ON t.ses_addr = s.saddr ORDER BY t.used_ublk DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "undo使用率不高但报ORA-30036",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ORA-30036但undo使用率不高,可能为单段扩展限制或数据文件无法自动扩展"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查undo数据文件自动扩展配置", SkillCommand: "/sql @undo_datafile", RawSQL: "SELECT file_name, bytes/1024/1024 size_mb, maxbytes/1024/1024 max_mb, autoextensible FROM DBA_DATA_FILES WHERE tablespace_name = (SELECT value FROM V$PARAMETER WHERE name = 'undo_tablespace')"},
						{Type: ActionFix, Desc: "启用自动扩展或增大maxsize", SkillCommand: "/sql @enable_autoext", RawSQL: "ALTER DATABASE DATAFILE '{file}' AUTOEXTEND ON NEXT 512M MAXSIZE 30G"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"ora_1555_emergency"},
		Tags:     []string{"emergency", "ora-30036", "undo", "guarantee", "space"},
		Versions: "9i+",
	}
}

// ruleTablespaceFullEmergency detects tablespace at 100% usage where DML
// operations are failing and immediate space extension is needed.
func ruleTablespaceFullEmergency() *Rule {
	return &Rule{
		ID:       "tablespace_full_emergency",
		Name:     "表空间100%满(Tablespace Full Emergency)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01653"},
			{Type: SignalErrorCode, Key: "ORA-01654"},
			{Type: SignalMetric, Key: metricTSFullCount},
			{Type: SignalMetric, Key: metricTablespaceUsedPct},
			{Type: SignalKeyword, Key: "tablespace full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricTablespaceUsedPct, Op: OpGTE, Value: 99},
			},
		},
		Tree: &TreeNode{
			Step:  "定位100%满的表空间",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricTablespaceUsedPct) },
			Branches: []Branch{
				{
					Match:    MatchGTE(100),
					Label:    "表空间100%满,DML失败",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否可自动扩展或有空间可扩",
						Query: QueryDatafileStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("NOAUTOEXT"),
								Label:    "数据文件未启用自动扩展",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "表空间100%满且数据文件未启用自动扩展,所有相关DML将失败"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启用自动扩展", SkillCommand: "/sql @enable_autoext", RawSQL: "ALTER DATABASE DATAFILE '{file}' AUTOEXTEND ON NEXT 512M MAXSIZE 32G", Risk: "确保磁盘有足够可用空间"},
									{Type: ActionUrgent, Desc: "或手动添加数据文件", SkillCommand: "/sql @add_datafile", RawSQL: "ALTER TABLESPACE {ts_name} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
								},
							},
							{
								Match:    MatchEquals("MAXED"),
								Label:    "数据文件已达maxsize上限",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "表空间100%满,数据文件已达autoextend maxsize限制"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大数据文件maxsize或添加新数据文件", SkillCommand: "/sql @resize_datafile", RawSQL: "ALTER DATABASE DATAFILE '{file}' AUTOEXTEND ON MAXSIZE 64G"},
									{Type: ActionUrgent, Desc: "添加新数据文件到表空间", SkillCommand: "/sql @add_datafile", RawSQL: "ALTER TABLESPACE {ts_name} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致无法扩展",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "表空间满且无法自动扩展,需检查磁盘空间和ASM"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查OS/ASM可用磁盘空间", SkillCommand: "/sql @disk_space", RawSQL: "SELECT name, total_mb, free_mb, ROUND(free_mb/total_mb*100,1) free_pct FROM V$ASM_DISKGROUP"},
									{Type: ActionFix, Desc: "清理不需要的数据释放空间", SkillCommand: "/sql @large_segments", RawSQL: "SELECT owner, segment_name, segment_type, bytes/1024/1024/1024 gb FROM DBA_SEGMENTS WHERE tablespace_name = '{ts_name}' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGTE(99),
					Label:    "表空间99%+,即将满",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "表空间使用率>99%,即将达到100%,需立即扩容"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即扩展表空间避免业务中断", SkillCommand: "/sql @extend_ts", RawSQL: "ALTER TABLESPACE {ts_name} ADD DATAFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
						{Type: ActionPrevent, Desc: "设置表空间告警阈值", SkillCommand: "/sql @ts_alert", RawSQL: "BEGIN DBMS_SERVER_ALERT.SET_THRESHOLD(DBMS_SERVER_ALERT.TABLESPACE_PCT_FULL, DBMS_SERVER_ALERT.OPERATOR_GE, '85', DBMS_SERVER_ALERT.OPERATOR_GE, '95', 1, 1, NULL, DBMS_SERVER_ALERT.OBJECT_TYPE_TABLESPACE, '{ts_name}'); END;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "表空间使用率正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有表空间使用率在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "tablespace", "space", "full", "ora-01653"},
		Versions: "9i+",
	}
}

// ruleArchiveFullHang detects archive destination full causing database
// to suspend (ARCH stuck), requiring immediate space reclamation.
func ruleArchiveFullHang() *Rule {
	return &Rule{
		ID:       "archive_full_hang",
		Name:     "归档空间满数据库挂起(Archive Dest Full)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00257"},
			{Type: SignalMetric, Key: metricArchiveDestFull},
			{Type: SignalMetric, Key: metricFRAUsedPct},
			{Type: SignalKeyword, Key: "archiver hung"},
			{Type: SignalKeyword, Key: "archive full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFRAUsedPct, Op: OpGTE, Value: 98},
			},
		},
		Tree: &TreeNode{
			Step:  "检查归档目的地空间使用率",
			Query: QueryArchiveStatus,
			Branches: []Branch{
				{
					Match:    MatchGTE(100),
					Label:    "归档目的地100%满,数据库已挂起",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查FRA或归档磁盘路径",
						Query: QueryArchiveDest,
						Branches: []Branch{
							{
								Match:    MatchEquals("FRA"),
								Label:    "使用FRA(Flash Recovery Area)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-00257: FRA 100%满,ARCH进程无法归档,数据库将挂起所有DML"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "删除过期的RMAN备份释放FRA空间", SkillCommand: "/sql @rman_clean", RawSQL: "-- RMAN: DELETE NOPROMPT OBSOLETE; CROSSCHECK ARCHIVELOG ALL; DELETE NOPROMPT EXPIRED ARCHIVELOG ALL;", Risk: "确认备份策略后再删除"},
									{Type: ActionUrgent, Desc: "增大db_recovery_file_dest_size", SkillCommand: "/params db_recovery_file_dest_size", RawSQL: "ALTER SYSTEM SET db_recovery_file_dest_size = '{new_size}' SCOPE=BOTH"},
									{Type: ActionFix, Desc: "配置RMAN自动删除策略", SkillCommand: "/sql @rman_retention", RawSQL: "-- RMAN: CONFIGURE RETENTION POLICY TO RECOVERY WINDOW OF 7 DAYS; CONFIGURE ARCHIVELOG DELETION POLICY TO APPLIED ON ALL STANDBY;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用自定义归档路径",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-00257: 归档目的地磁盘空间满,数据库挂起"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即清理归档目录释放空间", SkillCommand: "/sql @arch_cleanup", RawSQL: "-- 1. 确认备份后: rm /archivelog/older_than_7days; 2. RMAN: CROSSCHECK ARCHIVELOG ALL;"},
									{Type: ActionUrgent, Desc: "临时切换归档目的地到有空间的路径", SkillCommand: "/sql @switch_arch_dest", RawSQL: "ALTER SYSTEM SET log_archive_dest_1 = 'LOCATION={new_path}' SCOPE=BOTH", Risk: "确保新路径有足够空间且Oracle有写权限", Rollback: "ALTER SYSTEM SET log_archive_dest_1 = 'LOCATION={old_path}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGTE(98),
					Label:    "归档空间98-100%,即将满",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "归档目的地使用率>98%,即将满,如不处理将导致数据库挂起"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即释放归档空间", SkillCommand: "/sql @fra_usage", RawSQL: "SELECT * FROM V$RECOVERY_FILE_DEST"},
						{Type: ActionFix, Desc: "清理过期归档和备份", SkillCommand: "/sql @rman_cleanup", RawSQL: "-- RMAN: DELETE NOPROMPT OBSOLETE; DELETE NOPROMPT ARCHIVELOG UNTIL TIME 'SYSDATE-3';"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "归档空间正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "归档目的地空间充足"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"redo_log_switch_frequent"},
		CausesOf: []string{"database_hang", "dg_gap_detected"},
		Tags:     []string{"emergency", "archive", "fra", "ora-00257", "hang"},
		Versions: "10g+",
	}
}

// ruleTempFullORA1652 detects ORA-01652 (unable to extend temp segment)
// when TEMP tablespace is exhausted by large sorts, hash joins, or PGA spill.
func ruleTempFullORA1652() *Rule {
	return &Rule{
		ID:       "temp_full_ora1652",
		Name:     "ORA-01652临时空间耗尽(Temp Full)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01652"},
			{Type: SignalMetric, Key: metricORA1652Count},
			{Type: SignalMetric, Key: metricTempUsedPct},
			{Type: SignalKeyword, Key: "ORA-1652"},
			{Type: SignalKeyword, Key: "temp full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricTempUsedPct, Op: OpGTE, Value: 95},
			},
		},
		Tree: &TreeNode{
			Step:  "检查TEMP表空间使用率和消耗会话",
			Query: QueryTempUsage,
			Branches: []Branch{
				{
					Match:    MatchGTE(99),
					Label:    "TEMP 99%+满,操作失败",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "定位消耗TEMP最多的会话/SQL",
						Query: QueryASHTopSQL,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "找到TEMP高消耗的SQL",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01652: TEMP表空间耗尽,存在大量sort/hash join操作溢出到TEMP"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位并考虑终止消耗TEMP最多的会话", SkillCommand: "/sessions", RawSQL: "SELECT s.sid, s.serial#, s.username, s.sql_id, su.blocks*8/1024 temp_mb FROM V$SORT_USAGE su JOIN V$SESSION s ON su.session_addr = s.saddr ORDER BY su.blocks DESC FETCH FIRST 10 ROWS ONLY"},
									{Type: ActionUrgent, Desc: "扩展TEMP表空间", SkillCommand: "/sql @extend_temp", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
									{Type: ActionFix, Desc: "优化SQL减少TEMP使用", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST'))"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未定位到具体SQL",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "TEMP表空间耗尽,需扩展空间并排查高消耗会话"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "扩展TEMP表空间", SkillCommand: "/sql @extend_temp", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
									{Type: ActionInvestigate, Desc: "查看所有TEMP使用分布", SkillCommand: "/sql @temp_detail", RawSQL: "SELECT tablespace, segtype, SUM(blocks)*8/1024 mb, COUNT(DISTINCT session_addr) sessions FROM V$SORT_USAGE GROUP BY tablespace, segtype ORDER BY mb DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGTE(95),
					Label:    "TEMP 95-99%,即将满",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "TEMP表空间使用率>95%,有耗尽风险"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看TEMP消耗top会话", SkillCommand: "/sql @temp_top", RawSQL: "SELECT s.sid, s.username, s.sql_id, su.blocks*8/1024 temp_mb FROM V$SORT_USAGE su JOIN V$SESSION s ON su.session_addr = s.saddr ORDER BY su.blocks DESC FETCH FIRST 10 ROWS ONLY"},
						{Type: ActionFix, Desc: "扩展TEMP表空间作为预防", SkillCommand: "/sql @extend_temp", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE '{path}' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "TEMP空间正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "TEMP表空间使用率正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-1652", "temp", "sort", "hash_join"},
		Versions: "9i+",
	}
}

// ruleDatafileCorruption detects ORA-01578 (data block corruption) requiring
// block or media recovery.
func ruleDatafileCorruption() *Rule {
	return &Rule{
		ID:       "datafile_corruption",
		Name:     "ORA-01578数据文件损坏(Data Block Corruption)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01578"},
			{Type: SignalErrorCode, Key: "ORA-01110"},
			{Type: SignalMetric, Key: metricCorruptBlockCount},
			{Type: SignalKeyword, Key: "block corruption"},
			{Type: SignalKeyword, Key: "ORA-1578"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricCorruptBlockCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$DATABASE_BLOCK_CORRUPTION中的损坏块",
			Query: QueryCorruptBlocks,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "损坏块 > 10个,可能为介质损坏",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查损坏块是否集中在同一数据文件",
						Query: QueryDatafileStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("SINGLE_FILE"),
								Label:    "损坏集中在单一数据文件",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01578: 多个损坏块集中在同一数据文件,可能为底层存储介质故障"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "使用RMAN进行数据文件级别恢复", SkillCommand: "/sql @rman_restore_df", RawSQL: "-- RMAN: RESTORE DATAFILE {file#}; RECOVER DATAFILE {file#}; ALTER DATABASE DATAFILE {file#} ONLINE;"},
									{Type: ActionInvestigate, Desc: "检查底层存储健康状态", SkillCommand: "/sql @corrupt_detail", RawSQL: "SELECT file#, block#, blocks, corruption_change#, corruption_type FROM V$DATABASE_BLOCK_CORRUPTION ORDER BY file#, block#"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "损坏分散在多个文件",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "多个数据文件出现block corruption,可能为存储系统整体故障"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "使用RMAN进行block级别恢复", SkillCommand: "/sql @rman_block_recover", RawSQL: "-- RMAN: BLOCKRECOVER CORRUPTION LIST;"},
									{Type: ActionUrgent, Desc: "验证完整数据库(RMAN VALIDATE)", SkillCommand: "/sql @rman_validate", RawSQL: "-- RMAN: BACKUP VALIDATE CHECK LOGICAL DATABASE;"},
									{Type: ActionFix, Desc: "检查存储链路和控制器日志", SkillCommand: "/sql @io_errors", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01578%' OR message_text LIKE '%ORA-01110%' ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量损坏块(1-10个)",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ORA-01578: 检测到少量数据块损坏,需使用RMAN block recovery修复"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "使用RMAN block media recovery修复", SkillCommand: "/sql @rman_bmr", RawSQL: "-- RMAN: BLOCKRECOVER DATAFILE {file#} BLOCK {block#};"},
						{Type: ActionInvestigate, Desc: "确认损坏类型(物理/逻辑)", SkillCommand: "/sql @corrupt_type", RawSQL: "SELECT file#, block#, corruption_type, DECODE(corruption_type, 'PHYSICAL','物理损坏-需BMR', 'LOGICAL','逻辑损坏-DBMS_REPAIR', 'NOLOGGING','NOLOGGING-需从备库恢复', corruption_type) advice FROM V$DATABASE_BLOCK_CORRUPTION"},
						{Type: ActionPrevent, Desc: "启用DB_BLOCK_CHECKING和DB_BLOCK_CHECKSUM", SkillCommand: "/params db_block_checking", RawSQL: "ALTER SYSTEM SET db_block_checking = MEDIUM SCOPE=BOTH; ALTER SYSTEM SET db_block_checksum = TYPICAL SCOPE=BOTH", Risk: "db_block_checking会增加1-10% CPU开销"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无数据块损坏",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到数据块损坏"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"dg_mrp_stopped"},
		Tags:     []string{"emergency", "corruption", "ora-1578", "block_recovery", "rman"},
		Versions: "9i+",
	}
}

// ruleControlFileIssue detects controlfile backup validation failures and
// multiplex configuration issues.
func ruleControlFileIssue() *Rule {
	return &Rule{
		ID:       "controlfile_issue",
		Name:     "控制文件问题(Controlfile Issue)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricControlfileIssue},
			{Type: SignalErrorCode, Key: "ORA-00205"},
			{Type: SignalKeyword, Key: "controlfile"},
			{Type: SignalKeyword, Key: "control file"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricControlfileIssue, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查控制文件多路复用和备份状态",
			Query: QueryControlfileInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("SINGLE_COPY"),
					Label:    "控制文件仅单份,无冗余",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查控制文件备份时间",
						Query: QueryDatafileStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(24),
								Label:    "控制文件备份超过24小时",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "控制文件仅单份且备份超过24小时,如丢失将无法恢复数据库"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即备份控制文件", SkillCommand: "/sql @backup_cf", RawSQL: "ALTER DATABASE BACKUP CONTROLFILE TO TRACE AS '{path}/cf_backup.trc'"},
									{Type: ActionUrgent, Desc: "配置控制文件多路复用", SkillCommand: "/sql @multiplex_cf", RawSQL: "ALTER SYSTEM SET control_files = '{path1}/control01.ctl', '{path2}/control02.ctl', '{path3}/control03.ctl' SCOPE=SPFILE", Risk: "需要重启数据库并复制控制文件", Rollback: "恢复原control_files参数"},
									{Type: ActionFix, Desc: "配置RMAN自动备份控制文件", SkillCommand: "/sql @rman_autobackup", RawSQL: "-- RMAN: CONFIGURE CONTROLFILE AUTOBACKUP ON; CONFIGURE CONTROLFILE AUTOBACKUP FORMAT FOR DEVICE TYPE DISK TO '{path}/cf_%F';"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "有近期备份但仍为单份",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "控制文件仅单份,虽有近期备份,仍建议多路复用到不同磁盘"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "配置控制文件多路复用(至少3份)", SkillCommand: "/sql @multiplex_cf", RawSQL: "ALTER SYSTEM SET control_files = '{path1}/control01.ctl', '{path2}/control02.ctl', '{path3}/control03.ctl' SCOPE=SPFILE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("NO_BACKUP"),
					Label:    "控制文件无备份",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "控制文件无RMAN备份记录,数据库恢复能力受损"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即进行控制文件备份", SkillCommand: "/sql @backup_cf", RawSQL: "ALTER DATABASE BACKUP CONTROLFILE TO '{path}/cf_backup_$(date +%Y%m%d).bak'"},
						{Type: ActionFix, Desc: "启用RMAN控制文件自动备份", SkillCommand: "/sql @rman_autobackup", RawSQL: "-- RMAN: CONFIGURE CONTROLFILE AUTOBACKUP ON;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "控制文件配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "控制文件已多路复用且有近期备份"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "controlfile", "backup", "multiplex", "recovery"},
		Versions: "9i+",
	}
}

// ruleListenerDown detects TNS listener not running or refusing connections,
// indicating listener crash or misconfiguration.
func ruleListenerDown() *Rule {
	return &Rule{
		ID:       "listener_down",
		Name:     "监听器宕机(Listener Down)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-12541"},
			{Type: SignalErrorCode, Key: "TNS-12541"},
			{Type: SignalMetric, Key: metricListenerDown},
			{Type: SignalKeyword, Key: "listener down"},
			{Type: SignalKeyword, Key: "TNS connection refused"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricListenerDown, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查监听器进程和端口状态",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricListenerDown) },
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "监听器不可用",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "区分监听器是否崩溃或配置错误",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchEquals("CRASHED"),
								Label:    "监听器进程已崩溃",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "TNS Listener进程已崩溃,所有新连接将被拒绝(ORA-12541)"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即重启监听器", SkillCommand: "/sql @start_listener", RawSQL: "-- lsnrctl start {listener_name}"},
									{Type: ActionInvestigate, Desc: "检查listener.log确定崩溃原因", SkillCommand: "/sql @listener_log", RawSQL: "-- tail -100 $ORACLE_BASE/diag/tnslsnr/{hostname}/{listener}/trace/{listener}.log"},
									{Type: ActionPrevent, Desc: "确认listener已注册在CRS(RAC)或自动启动", SkillCommand: "/sql @listener_config", RawSQL: "-- srvctl status listener; lsnrctl status"},
								},
							},
							{
								Match:    MatchEquals("BLOCKED"),
								Label:    "监听器端口被阻塞或配置错误",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "监听器运行但端口不可达,可能为防火墙阻塞或listener.ora配置错误"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查防火墙和网络ACL", SkillCommand: "/sql @check_port", RawSQL: "-- netstat -tlnp | grep 1521; iptables -L -n | grep 1521"},
									{Type: ActionFix, Desc: "检查listener.ora配置", SkillCommand: "/sql @listener_ora", RawSQL: "-- cat $ORACLE_HOME/network/admin/listener.ora"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "监听器状态异常",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "监听器状态异常,需排查具体原因"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查监听器状态", SkillCommand: "/sql @lsnrctl_status", RawSQL: "-- lsnrctl status; lsnrctl services"},
									{Type: ActionFix, Desc: "重启监听器", SkillCommand: "/sql @restart_listener", RawSQL: "-- lsnrctl stop; lsnrctl start"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "监听器正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "TNS Listener正常运行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"connection_exhaust"},
		Tags:     []string{"emergency", "listener", "tns", "ora-12541", "connection"},
		Versions: "9i+",
	}
}
