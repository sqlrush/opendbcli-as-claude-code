/*-------------------------------------------------------------------------
 *
 * rules_emergency_ext.go
 *	  Oracle rule engine — emergency-response rule extensions (DB hung, instance down, listener loss).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_emergency_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Extended Emergency metric name constants ────────────────────────────────

const (
	metricORA00020Count = "ora_00020_count"
	metricORA00054Count = "ora_00054_count"
	metricORA00060Count = "ora_00060_count"
	metricORA01000Count = "ora_01000_count"
	metricORA01031Count = "ora_01031_count"
	metricORA01536Count = "ora_01536_count"
	metricORA01628Count = "ora_01628_count"
	metricORA04068Count = "ora_04068_count"

	metricRACOCSSDReboot     = "rac_ocssd_reboot"
	metricRACVotingDiskIO    = "rac_voting_disk_io_timeout"
	metricRACONSFailure      = "rac_ons_failure"
	metricRACCRSResourceFail = "rac_crs_resource_fail"
	metricRACASMInstDown     = "rac_asm_instance_down"

	metricDGFlashbackStatus    = "dg_flashback_status"
	metricDGSnapshotStandby    = "dg_snapshot_standby"
	metricDGProtectionMode     = "dg_protection_mode_current"
	metricDGFarSyncStatus      = "dg_far_sync_status"
	metricDGPasswordFileSync   = "dg_password_file_sync"
)

// ─── Extended Emergency QueryID constants ────────────────────────────────────

const (
	QueryORA00020Detail     QueryID = "ora_00020_detail"
	QueryORA00054Detail     QueryID = "ora_00054_detail"
	QueryORA00060Detail     QueryID = "ora_00060_detail"
	QueryORA01000Detail     QueryID = "ora_01000_detail"
	QueryORA01031Detail     QueryID = "ora_01031_detail"
	QueryORA01536Detail     QueryID = "ora_01536_detail"
	QueryORA01628Detail     QueryID = "ora_01628_detail"
	QueryORA04068Detail     QueryID = "ora_04068_detail"
	QueryOCSSDLog           QueryID = "ocssd_log"
	QueryVotingDiskStatus   QueryID = "voting_disk_status"
	QueryONSConfig          QueryID = "ons_config"
	QueryCRSResourceStatus  QueryID = "crs_resource_status"
	QueryASMInstanceStatus  QueryID = "asm_instance_status"
	QueryDGFlashbackInfo    QueryID = "dg_flashback_info"
	QueryDGSnapshotInfo     QueryID = "dg_snapshot_info"
	QueryDGProtectionInfo   QueryID = "dg_protection_info"
	QueryDGFarSyncInfo      QueryID = "dg_far_sync_info"
	QueryDGPasswordFileInfo QueryID = "dg_password_file_info"
)

// ═══════════════════════════════════════════════════════════════════════════
// Extended Emergency Rules (18)
// EM2-001 ~ EM2-018
// ═══════════════════════════════════════════════════════════════════════════

func extEmergencyRules() []*Rule {
	return []*Rule{
		// ORA errors (8)
		ruleORA00020(), // EM2-001
		ruleORA00054(), // EM2-002
		ruleORA00060(), // EM2-003
		ruleORA01000(), // EM2-004
		ruleORA01031(), // EM2-005
		ruleORA01536(), // EM2-006
		ruleORA01628(), // EM2-007
		ruleORA04068(), // EM2-008

		// RAC emergency (5)
		ruleRACOCSSDReboot(),    // EM2-009
		ruleRACVotingDiskIssue(), // EM2-010
		ruleRACONSNotification(), // EM2-011
		ruleRACCRSResourceFail(), // EM2-012
		ruleRACASMInstanceDown(), // EM2-013

		// DG emergency (5)
		ruleDGFlashbackRebuild(),     // EM2-014
		ruleDGSnapshotStandby(),      // EM2-015
		ruleDGProtectionModeChange(), // EM2-016
		ruleDGFarSyncConfig(),        // EM2-017
		ruleDGPasswordSync(),         // EM2-018
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// ORA Error Rules (8)
// ═══════════════════════════════════════════════════════════════════════════

// ruleORA00020 detects ORA-00020: maximum number of processes exceeded.
// All process slots are used, new connections are rejected.
func ruleORA00020() *Rule {
	return &Rule{
		ID:       "EM2-001",
		Name:     "ORA-00020进程数耗尽(Max Processes Exceeded)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00020"},
			{Type: SignalMetric, Key: metricORA00020Count},
			{Type: SignalKeyword, Key: "ORA-00020"},
			{Type: SignalKeyword, Key: "maximum number of processes"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA00020Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$RESOURCE_LIMIT中processes当前使用率",
			Query: QueryProcessLimit,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "processes使用率>95%,已耗尽",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否存在连接泄漏(大量INACTIVE会话)",
						Query: QueryORA00020Detail,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    ">50%会话为INACTIVE且idle >1小时,连接泄漏",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-00020:processes耗尽,超过50%为长时间INACTIVE会话,存在严重连接泄漏"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即kill长时间idle的泄漏会话", SkillCommand: "/sessions", RawSQL: "BEGIN\n  FOR r IN (SELECT sid, serial# FROM V$SESSION WHERE status = 'INACTIVE' AND last_call_et > 3600 AND username IS NOT NULL) LOOP\n    EXECUTE IMMEDIATE 'ALTER SYSTEM KILL SESSION ''' || r.sid || ',' || r.serial# || ''' IMMEDIATE';\n  END LOOP;\nEND;", Risk: "会终止idle会话,确认不影响应用"},
									{Type: ActionFix, Desc: "增大processes参数(需重启)", SkillCommand: "/params processes", RawSQL: "ALTER SYSTEM SET processes = {new_val} SCOPE=SPFILE", Risk: "需重启实例", Rollback: "ALTER SYSTEM SET processes = {old_val} SCOPE=SPFILE"},
									{Type: ActionPrevent, Desc: "配置IDLE会话超时(Oracle 19c+ profile)", SkillCommand: "/sql @idle_timeout", RawSQL: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME 60"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "大部分会话活跃,确实需求超出限制",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-00020:processes耗尽且大部分会话活跃,并发需求确实超出processes设置"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大processes参数(需重启)", SkillCommand: "/params processes", RawSQL: "ALTER SYSTEM SET processes = {new_val} SCOPE=SPFILE"},
									{Type: ActionFix, Desc: "考虑启用连接池(DRCP)减少进程消耗", SkillCommand: "/params connection_brokers", RawSQL: "ALTER SYSTEM SET connection_brokers = '((TYPE=POOLED)(BROKERS=1)(CONNECTIONS=40))' SCOPE=SPFILE"},
									{Type: ActionInvestigate, Desc: "按machine分析连接分布", SkillCommand: "/sessions", RawSQL: "SELECT machine, program, COUNT(*) cnt FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program ORDER BY cnt DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "processes使用率80-95%,即将耗尽",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ORA-00020已发生,processes使用80-95%,需尽快扩容或清理泄漏"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "增大processes并排查泄漏", SkillCommand: "/params processes", RawSQL: "SELECT resource_name, current_utilization, max_utilization, limit_value FROM V$RESOURCE_LIMIT WHERE resource_name IN ('processes','sessions')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "当前processes使用正常",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "ORA-00020曾发生但当前进程使用率已恢复,需排查历史峰值原因"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查processes历史最大使用量", SkillCommand: "/sql @process_history", RawSQL: "SELECT resource_name, current_utilization, max_utilization, limit_value FROM V$RESOURCE_LIMIT WHERE resource_name = 'processes'"},
					},
				},
			},
		},
		CausedBy: []string{"connection_exhaust"},
		CausesOf: []string{"database_hang"},
		Tags:     []string{"emergency", "ora-00020", "processes", "connection"},
		Versions: "9i+",
	}
}

// ruleORA00054 detects ORA-00054: resource busy and acquire with NOWAIT specified
// or timeout expired. Occurs when DDL or lock operations use NOWAIT but the
// resource is held by another session.
func ruleORA00054() *Rule {
	return &Rule{
		ID:       "EM2-002",
		Name:     "ORA-00054资源忙/NOWAIT超时(Resource Busy NOWAIT)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00054"},
			{Type: SignalMetric, Key: metricORA00054Count},
			{Type: SignalKeyword, Key: "ORA-00054"},
			{Type: SignalKeyword, Key: "resource busy"},
			{Type: SignalKeyword, Key: "NOWAIT"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA00054Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前锁持有和等待情况",
			Query: QueryORA00054Detail,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    ">10次/分钟ORA-00054,频繁锁冲突",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否为DDL操作被DML阻塞",
						Query: QueryORA00054Detail,
						Branches: []Branch{
							{
								Match:    MatchEquals("DDL_BLOCKED"),
								Label:    "DDL(如ALTER TABLE)被长事务DML阻塞",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "频繁ORA-00054:DDL操作(ALTER TABLE/CREATE INDEX)被长事务持有的TM锁阻塞,DDL使用NOWAIT或ddl_lock_timeout过短"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "设置ddl_lock_timeout允许DDL等待", SkillCommand: "/params ddl_lock_timeout", RawSQL: "ALTER SESSION SET ddl_lock_timeout = 60"},
									{Type: ActionInvestigate, Desc: "查看当前TM锁持有者", SkillCommand: "/sql @tm_locks", RawSQL: "SELECT l.session_id, l.lock_type, l.mode_held, l.mode_requested, l.blocking_others, s.sql_id, s.username, s.machine FROM DBA_LOCK l JOIN V$SESSION s ON l.session_id = s.sid WHERE l.lock_type = 'DML'"},
									{Type: ActionFix, Desc: "在维护窗口执行DDL或使用ONLINE选项", SkillCommand: "/sql @online_ddl", RawSQL: "-- ALTER INDEX ... REBUILD ONLINE; ALTER TABLE ... ADD COLUMN ... ONLINE;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "应用层NOWAIT锁获取失败",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "ORA-00054频繁发生,应用代码使用SELECT...FOR UPDATE NOWAIT或WAIT短超时,在高并发下频繁失败"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "修改应用使用FOR UPDATE WAIT {n}替代NOWAIT", SkillCommand: "/sql @nowait_sql", RawSQL: "SELECT sql_id, sql_text FROM V$SQL WHERE sql_text LIKE '%FOR UPDATE NOWAIT%' OR sql_text LIKE '%FOR UPDATE WAIT%'"},
									{Type: ActionInvestigate, Desc: "分析ORA-00054发生时的blocking session", SkillCommand: "/sql @blocking_analysis", RawSQL: "SELECT blocking_session, sid, event, seconds_in_wait, sql_id FROM V$SESSION WHERE blocking_session IS NOT NULL AND event LIKE '%enq%'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "偶发ORA-00054",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "偶发ORA-00054,资源临时被锁定,通常可通过重试解决"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看当前锁等待情况", SkillCommand: "/sql @lock_wait", RawSQL: "SELECT sid, type, id1, id2, lmode, request, block FROM V$LOCK WHERE request > 0 OR block > 0"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无ORA-00054",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到ORA-00054"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"long_transaction"},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-00054", "lock", "nowait", "ddl"},
		Versions: "9i+",
	}
}

// ruleORA00060 detects ORA-00060: deadlock detected while waiting for resource.
// Enhanced version that checks deadlock frequency, patterns, and root cause
// (missing index on FK, application design issues).
func ruleORA00060() *Rule {
	return &Rule{
		ID:       "EM2-003",
		Name:     "ORA-00060死锁检测增强(Deadlock Enhanced)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-00060"},
			{Type: SignalMetric, Key: metricORA00060Count},
			{Type: SignalMetric, Key: metricEnqueueDeadlocks},
			{Type: SignalKeyword, Key: "ORA-00060"},
			{Type: SignalKeyword, Key: "deadlock"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA00060Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查死锁频率和trace文件中deadlock graph",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricORA00060Count) },
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    ">10次/小时死锁,系统性问题",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查死锁类型:TX-TX(行锁) vs TM-TM(表锁)",
						Query: QueryORA00060Detail,
						Branches: []Branch{
							{
								Match:    MatchEquals("TM_DEADLOCK"),
								Label:    "TM锁死锁(表级锁),可能FK无索引",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "频繁TM锁死锁(>10/小时),最常见原因为外键列无索引:子表DML需对父表加share lock,当多会话操作同一父子关系时产生死锁"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找无索引的外键列并创建索引", SkillCommand: "/sql @fk_no_index", RawSQL: "SELECT c.owner, c.table_name, cc.column_name, c.constraint_name FROM DBA_CONSTRAINTS c JOIN DBA_CONS_COLUMNS cc ON c.constraint_name = cc.constraint_name AND c.owner = cc.owner WHERE c.constraint_type = 'R' AND NOT EXISTS (SELECT 1 FROM DBA_IND_COLUMNS ic WHERE ic.table_owner = cc.owner AND ic.table_name = cc.table_name AND ic.column_name = cc.column_name)"},
									{Type: ActionInvestigate, Desc: "查看deadlock trace文件", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00060%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC"},
								},
							},
							{
								Match:    MatchEquals("TX_DEADLOCK"),
								Label:    "TX锁死锁(行级锁),应用锁序不一致",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "频繁TX锁死锁,多会话以不同顺序更新相同行集,应用层需统一加锁顺序"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "分析死锁SQL确定锁顺序冲突", SkillCommand: "/sql @deadlock_sql", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00060%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "修改应用统一锁获取顺序(按PK排序)", SkillCommand: "/explain {sql_id}", RawSQL: "-- 应用层修改: 所有更新按主键升序加锁"},
									{Type: ActionPrevent, Desc: "设置锁超时避免无限等待", SkillCommand: "/sql @lock_timeout", RawSQL: "ALTER SYSTEM SET distributed_lock_timeout = 30"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "混合类型死锁",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "频繁死锁且类型混合,需逐一分析trace文件确定模式"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "收集所有deadlock trace文件分析", SkillCommand: "/alert", RawSQL: "SELECT value FROM V$DIAG_INFO WHERE name = 'Diag Trace'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "偶发死锁",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "检测到ORA-00060死锁,虽然偶发但仍需分析trace确保不是系统性问题"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看deadlock trace确定涉及的SQL和对象", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-00060%' ORDER BY originating_timestamp DESC FETCH FIRST 5 ROWS ONLY"},
						{Type: ActionPrevent, Desc: "检查FK列是否有索引", SkillCommand: "/sql @fk_no_index", RawSQL: "SELECT c.owner, c.table_name, cc.column_name FROM DBA_CONSTRAINTS c JOIN DBA_CONS_COLUMNS cc ON c.constraint_name = cc.constraint_name AND c.owner = cc.owner WHERE c.constraint_type = 'R' AND NOT EXISTS (SELECT 1 FROM DBA_IND_COLUMNS ic WHERE ic.table_owner = cc.owner AND ic.table_name = cc.table_name AND ic.column_name = cc.column_name)"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无死锁",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到ORA-00060死锁"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-00060", "deadlock", "lock", "fk_index"},
		Versions: "9i+",
	}
}

// ruleORA01000 detects ORA-01000: maximum open cursors exceeded. Each session
// has a limit set by open_cursors parameter. Cursor leak in application code
// is the most common cause.
func ruleORA01000() *Rule {
	return &Rule{
		ID:       "EM2-004",
		Name:     "ORA-01000游标数耗尽(Max Open Cursors Exceeded)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01000"},
			{Type: SignalMetric, Key: metricORA01000Count},
			{Type: SignalKeyword, Key: "ORA-01000"},
			{Type: SignalKeyword, Key: "open cursors"},
			{Type: SignalKeyword, Key: "cursor leak"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA01000Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查open_cursors参数设置和会话游标使用",
			Query: QueryCursorStats,
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "多个会话接近open_cursors上限",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否为游标泄漏(同一SQL_ID大量重复打开)",
						Query: QueryORA01000Detail,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "同一SQL_ID被单会话打开100+次,确认泄漏",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01000:单会话对同一SQL_ID打开100+个游标,典型的应用层游标泄漏(ResultSet/Statement未close)"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "定位泄漏的SQL和应用代码", SkillCommand: "/sql @cursor_leak", RawSQL: "SELECT s.sid, s.username, s.machine, s.program, oc.sql_id, COUNT(*) cursor_count FROM V$OPEN_CURSOR oc JOIN V$SESSION s ON oc.sid = s.sid GROUP BY s.sid, s.username, s.machine, s.program, oc.sql_id HAVING COUNT(*) > 50 ORDER BY cursor_count DESC"},
									{Type: ActionFix, Desc: "通知应用开发修复游标泄漏(关闭ResultSet/Statement)", SkillCommand: "/sql @cursor_leak_sql", RawSQL: "SELECT sql_id, sql_text FROM V$SQL WHERE sql_id IN (SELECT sql_id FROM V$OPEN_CURSOR GROUP BY sid, sql_id HAVING COUNT(*) > 50 FETCH FIRST 1 ROWS ONLY)"},
									{Type: ActionPrevent, Desc: "临时增大open_cursors缓解(非根因解决)", SkillCommand: "/params open_cursors", RawSQL: "ALTER SYSTEM SET open_cursors = {new_val} SCOPE=BOTH", Risk: "增大仅延缓问题,内存消耗增加"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非泄漏,业务确实需要大量游标",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01000:会话游标使用接近上限但未检测到泄漏模式,可能业务逻辑确实需要大量游标"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大open_cursors参数", SkillCommand: "/params open_cursors", RawSQL: "ALTER SYSTEM SET open_cursors = {new_val} SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "检查会话游标使用分布", SkillCommand: "/sql @cursor_usage", RawSQL: "SELECT sid, user_name, COUNT(*) cursor_count FROM V$OPEN_CURSOR GROUP BY sid, user_name ORDER BY cursor_count DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "当前游标使用率不高",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "ORA-01000曾发生但当前游标使用率不高,可能为特定操作触发"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查alert.log中ORA-01000发生时间和会话", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01000%' ORDER BY originating_timestamp DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"ora_4031_emergency"},
		Tags:     []string{"emergency", "ora-01000", "cursor", "leak", "open_cursors"},
		Versions: "9i+",
	}
}

// ruleORA01031 detects ORA-01031: insufficient privileges. This can indicate
// security misconfigurations, missing grants, or privilege escalation attempts.
func ruleORA01031() *Rule {
	return &Rule{
		ID:       "EM2-005",
		Name:     "ORA-01031权限不足(Insufficient Privileges)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01031"},
			{Type: SignalMetric, Key: metricORA01031Count},
			{Type: SignalKeyword, Key: "ORA-01031"},
			{Type: SignalKeyword, Key: "insufficient privileges"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA01031Count, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ORA-01031出现频率和来源会话",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricORA01031Count) },
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    ">50次/小时,可能为权限配置错误或攻击",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否来自同一用户/机器(定位问题源)",
						Query: QueryORA01031Detail,
						Branches: []Branch{
							{
								Match:    MatchEquals("SINGLE_SOURCE"),
								Label:    "单一来源,应用权限配置问题",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01031频繁发生(>50次/小时),来自单一用户/应用,大概率为权限配置遗漏或用户尝试未授权操作"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "确认需要的权限并授予", SkillCommand: "/sql @check_privs", RawSQL: "SELECT grantee, privilege, admin_option FROM DBA_SYS_PRIVS WHERE grantee = '{username}' ORDER BY privilege"},
									{Type: ActionInvestigate, Desc: "查看失败的SQL确定需要什么权限", SkillCommand: "/sql @audit_trail", RawSQL: "SELECT username, userhost, action_name, obj_name, returncode, timestamp# FROM SYS.AUD$ WHERE returncode = 1031 AND timestamp# > SYSDATE - 1 ORDER BY timestamp# DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "多来源,可能为权限探测/安全事件",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01031来自多个不同来源(>50次/小时),可能为安全扫描或权限探测攻击"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查审计日志确认是否为安全事件", SkillCommand: "/sql @audit_privfail", RawSQL: "SELECT username, userhost, action_name, obj_name, COUNT(*) cnt FROM SYS.AUD$ WHERE returncode = 1031 AND timestamp# > SYSDATE - 1 GROUP BY username, userhost, action_name, obj_name ORDER BY cnt DESC"},
									{Type: ActionPrevent, Desc: "启用统一审计加强监控", SkillCommand: "/sql @enable_audit", RawSQL: "CREATE AUDIT POLICY priv_fail_audit ACTIONS ALL WHEN 'SYS_CONTEXT(''USERENV'',''AUTHENTICATION_METHOD'') != ''PASSWORD''' EVALUATE PER SESSION"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "少量ORA-01031",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "检测到ORA-01031权限不足错误,可能为新部署应用缺少权限或用户误操作"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查哪些操作触发权限错误", SkillCommand: "/sql @priv_errors", RawSQL: "SELECT username, action_name, obj_name, returncode FROM SYS.AUD$ WHERE returncode = 1031 ORDER BY timestamp# DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "权限错误在正常范围",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ORA-01031偶发,属正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-01031", "privilege", "security"},
		Versions: "9i+",
	}
}

// ruleORA01536 detects ORA-01536: space quota exceeded for tablespace.
// User has exhausted their tablespace quota and cannot allocate more extents.
func ruleORA01536() *Rule {
	return &Rule{
		ID:       "EM2-006",
		Name:     "ORA-01536表空间配额耗尽(Space Quota Exceeded)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01536"},
			{Type: SignalMetric, Key: metricORA01536Count},
			{Type: SignalKeyword, Key: "ORA-01536"},
			{Type: SignalKeyword, Key: "space quota"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA01536Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查用户表空间配额使用情况",
			Query: QueryORA01536Detail,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "配额使用>95%,即将或已耗尽",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否为数据正常增长还是异常",
						Query: QueryORA01536Detail,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在大段增长(可能为批量导入或异常)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01536:用户表空间配额耗尽,检测到近期有大量段增长,可能为批量数据导入超出预期"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大用户表空间配额", SkillCommand: "/sql @alter_quota", RawSQL: "ALTER USER {username} QUOTA UNLIMITED ON {tablespace_name}", Risk: "UNLIMITED配额无上限,建议设具体值"},
									{Type: ActionInvestigate, Desc: "检查当前配额使用详情", SkillCommand: "/sql @quota_usage", RawSQL: "SELECT username, tablespace_name, bytes/1024/1024 used_mb, max_bytes/1024/1024 quota_mb, ROUND(bytes/NULLIF(max_bytes,0)*100,1) pct FROM DBA_TS_QUOTAS WHERE max_bytes > 0 ORDER BY bytes/NULLIF(max_bytes,0) DESC"},
									{Type: ActionPrevent, Desc: "设置合理配额上限", SkillCommand: "/sql @set_quota", RawSQL: "ALTER USER {username} QUOTA {size}M ON {tablespace_name}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "正常增长达到配额上限",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01536:用户配额已满,数据正常增长到达配额限制,需扩充配额"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大配额或设为UNLIMITED", SkillCommand: "/sql @alter_quota", RawSQL: "ALTER USER {username} QUOTA {new_size}M ON {tablespace_name}"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "当前配额使用率不高",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "ORA-01536曾发生,当前配额使用恢复正常(可能已扩容)"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认配额是否已调整", SkillCommand: "/sql @quota_check", RawSQL: "SELECT username, tablespace_name, bytes/1024/1024 used_mb, DECODE(max_bytes,-1,'UNLIMITED',max_bytes/1024/1024||'MB') quota FROM DBA_TS_QUOTAS ORDER BY username, tablespace_name"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-01536", "quota", "space", "tablespace"},
		Versions: "9i+",
	}
}

// ruleORA01628 detects ORA-01628: max # extents reached for rollback segment
// or table/index segment. In ASSM with autoextend, this usually means the
// datafile reached MAXSIZE or the tablespace is full.
func ruleORA01628() *Rule {
	return &Rule{
		ID:       "EM2-007",
		Name:     "ORA-01628最大扩展数达到(Max Extents Reached)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01628"},
			{Type: SignalMetric, Key: metricORA01628Count},
			{Type: SignalKeyword, Key: "ORA-01628"},
			{Type: SignalKeyword, Key: "max extents"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA01628Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查达到最大扩展数的段类型和原因",
			Query: QueryORA01628Detail,
			Branches: []Branch{
				{
					Match:    MatchEquals("DICTIONARY_MANAGED"),
					Label:    "字典管理表空间的MAX_EXTENTS限制",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查段的当前extents数和max_extents设置",
						Query: QueryORA01628Detail,
						Branches: []Branch{
							{
								Match:    MatchEquals("SMALL_MAX"),
								Label:    "max_extents设置过小(如默认121或505)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ORA-01628:段达到max_extents上限(字典管理表空间默认值过小),需增大或迁移到本地管理表空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大段的max_extents", SkillCommand: "/sql @alter_maxextents", RawSQL: "ALTER TABLE {owner}.{table} STORAGE (MAXEXTENTS UNLIMITED)"},
									{Type: ActionFix, Desc: "迁移到本地管理表空间(无extent数限制)", SkillCommand: "/sql @migrate_ts", RawSQL: "-- DBMS_SPACE_ADMIN.TABLESPACE_MIGRATE_TO_LOCAL('{tablespace_name}')", Risk: "需要足够空间和维护窗口"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "max_extents已设大但数据量确实超出",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-01628:段扩展数已大但仍达到上限,数据量增长超出预期"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "设置MAXEXTENTS UNLIMITED或增大", SkillCommand: "/sql @alter_maxextents", RawSQL: "ALTER TABLE {owner}.{table} STORAGE (MAXEXTENTS UNLIMITED)"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("DATAFILE_MAXSIZE"),
					Label:    "本地管理表空间datafile达到MAXSIZE",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "ORA-01628:datafile已达到MAXSIZE上限,表空间无法再自动扩展,需增大MAXSIZE或添加新datafile"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "增大datafile MAXSIZE或添加新datafile", SkillCommand: "/sql @extend_ts", RawSQL: "ALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON NEXT 1G MAXSIZE 32G"},
						{Type: ActionInvestigate, Desc: "检查datafile当前大小和MAXSIZE", SkillCommand: "/sql @datafile_sizes", RawSQL: "SELECT file_name, bytes/1024/1024/1024 size_gb, maxbytes/1024/1024/1024 maxsize_gb, autoextensible FROM DBA_DATA_FILES WHERE tablespace_name = '{ts_name}' ORDER BY file_name"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "需进一步确认原因",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "ORA-01628发生,需查看alert.log确定具体段和表空间信息"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看alert.log中ORA-01628详情", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01628%' ORDER BY originating_timestamp DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-01628", "extents", "space", "tablespace"},
		Versions: "9i+",
	}
}

// ruleORA04068 detects ORA-04068: existing state of packages has been discarded.
// Occurs when a package body is recompiled/invalidated while sessions have
// package state. Common during deployments and can cause application errors.
func ruleORA04068() *Rule {
	return &Rule{
		ID:       "EM2-008",
		Name:     "ORA-04068包状态丢失(Package State Discarded)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-04068"},
			{Type: SignalMetric, Key: metricORA04068Count},
			{Type: SignalKeyword, Key: "ORA-04068"},
			{Type: SignalKeyword, Key: "package state"},
			{Type: SignalKeyword, Key: "package discarded"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricORA04068Count, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ORA-04068频率和触发源",
			Check: func(ctx *EvalContext) interface{} { return ctx.MetricValue(metricORA04068Count) },
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    ">20次,大量会话受影响",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有DDL部署正在进行(package recompile)",
						Query: QueryORA04068Detail,
						Branches: []Branch{
							{
								Match:    MatchEquals("ACTIVE_DDL"),
								Label:    "检测到近期package DDL操作",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ORA-04068大量发生,检测到近期有package body重编译,所有持有该package state的会话下次调用时会报错(需重试)"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "确认部署是否完成,等待所有会话自动恢复(下次调用自动重建state)", SkillCommand: "/sql @recent_ddl", RawSQL: "SELECT owner, object_name, object_type, last_ddl_time, status FROM DBA_OBJECTS WHERE object_type IN ('PACKAGE','PACKAGE BODY') AND last_ddl_time > SYSDATE - 1/24 ORDER BY last_ddl_time DESC"},
									{Type: ActionPrevent, Desc: "使用Edition-Based Redefinition(12c+)实现在线部署", SkillCommand: "/sql @ebr_check", RawSQL: "SELECT edition_name, usable FROM DBA_EDITIONS"},
									{Type: ActionPrevent, Desc: "应用层添加ORA-04068重试逻辑", SkillCommand: "/sql @pkg_state", RawSQL: "-- 应用层: catch ORA-04068, reconnect/retry once"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无近期DDL,可能为依赖对象级联失效",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "ORA-04068发生但无直接DDL,可能为统计信息收集或同义词变更导致package级联失效"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查INVALID对象", SkillCommand: "/sql @invalid_objects", RawSQL: "SELECT owner, object_name, object_type, status, last_ddl_time FROM DBA_OBJECTS WHERE status = 'INVALID' AND object_type IN ('PACKAGE','PACKAGE BODY','PROCEDURE','FUNCTION') ORDER BY last_ddl_time DESC"},
									{Type: ActionFix, Desc: "重新编译INVALID对象", SkillCommand: "/sql @recompile", RawSQL: "BEGIN UTL_RECOMP.RECOMP_SERIAL('{schema}'); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量ORA-04068",
					Severity: SeverityMedium,
					Findings: []Finding{
						{Desc: "少量ORA-04068,可能为单次package重编译影响少数会话"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查最近被重编译的package", SkillCommand: "/sql @recent_pkg_change", RawSQL: "SELECT owner, object_name, object_type, last_ddl_time, status FROM DBA_OBJECTS WHERE object_type IN ('PACKAGE','PACKAGE BODY') AND last_ddl_time > SYSDATE - 1 ORDER BY last_ddl_time DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无ORA-04068",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到ORA-04068"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "ora-04068", "package", "state", "deployment"},
		Versions: "9i+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// RAC Emergency Rules (5)
// ═══════════════════════════════════════════════════════════════════════════

// ruleRACOCSSDReboot detects OCSSD-initiated node reboot due to CSS timeout.
// When ocssd cannot communicate with other nodes within misscount timeout,
// it triggers a node reboot to prevent split-brain.
func ruleRACOCSSDReboot() *Rule {
	return &Rule{
		ID:       "EM2-009",
		Name:     "OCSSD触发节点重启(CSS Timeout Reboot)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACOCSSDReboot},
			{Type: SignalKeyword, Key: "ocssd"},
			{Type: SignalKeyword, Key: "css reboot"},
			{Type: SignalKeyword, Key: "node eviction"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACOCSSDReboot, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ocssd.log中重启原因和时间线",
			Query: QueryOCSSDLog,
			Branches: []Branch{
				{
					Match:    MatchGT(1),
					Label:    "多次OCSSD重启,反复发生",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查misscount设置和网络心跳延迟",
						Query: QueryOCSSDLog,
						Branches: []Branch{
							{
								Match:    MatchEquals("NETWORK"),
								Label:    "互联网络延迟导致心跳超时",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "OCSSD反复触发节点重启,原因为互联网络延迟超过misscount阈值,心跳包丢失或延迟过高"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查互联网络(private interconnect)质量", SkillCommand: "/sql @rac_interconnect", RawSQL: "SELECT inst_id, name, ip_address FROM GV$CLUSTER_INTERCONNECTS"},
									{Type: ActionFix, Desc: "适当增大misscount(仅在网络延迟可控前提下)", SkillCommand: "/sql @css_misscount", RawSQL: "-- crsctl get css misscount\n-- crsctl set css misscount {new_val}", Risk: "增大misscount会延长split-brain检测时间"},
									{Type: ActionPrevent, Desc: "配置冗余互联网络(bonding)", SkillCommand: "/sql @haip_status", RawSQL: "SELECT inst_id, name, ip_address FROM GV$CLUSTER_INTERCONNECTS ORDER BY inst_id"},
								},
							},
							{
								Match:    MatchEquals("IO_DELAY"),
								Label:    "voting disk IO延迟导致心跳超时",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "OCSSD重启由voting disk IO延迟引起,存储子系统性能问题导致CSS无法及时完成voting disk心跳"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查voting disk所在存储性能", SkillCommand: "/sql @voting_disk", RawSQL: "-- crsctl query css votedisk"},
									{Type: ActionFix, Desc: "将voting disk迁移到高性能存储(SSD)", SkillCommand: "/sql @voting_disk_move", RawSQL: "-- crsctl replace votedisk +FAST_DG", Risk: "需要集群维护窗口"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因触发重启",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "OCSSD反复触发节点重启,具体原因需分析ocssd.log和cssd trace"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "收集Grid Infra诊断数据", SkillCommand: "/sql @gi_diag", RawSQL: "-- $GRID_HOME/bin/diagcollection.sh collect"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "单次OCSSD重启事件",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "检测到OCSSD触发的节点重启事件,需分析根因防止再次发生"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查ocssd.log中重启时间线", SkillCommand: "/sql @ocssd_events", RawSQL: "-- grep 'reboot' $GRID_HOME/log/{hostname}/cssd/ocssd.log | tail -20"},
						{Type: ActionInvestigate, Desc: "检查对应时间点的系统负载和IO延迟", SkillCommand: "/sql @os_stats", RawSQL: "SELECT stat_name, value FROM V$OSSTAT WHERE stat_name LIKE 'BUSY%' OR stat_name LIKE '%IDLE%'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无OCSSD重启事件",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未检测到OCSSD触发的节点重启"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rac_node_eviction"},
		Tags:     []string{"emergency", "rac", "ocssd", "css", "node_eviction", "reboot"},
		Versions: "10g+",
	}
}

// ruleRACVotingDiskIssue detects voting disk IO timeout issues in RAC.
// Voting disk is critical for cluster membership; IO delays can cause
// node eviction.
func ruleRACVotingDiskIssue() *Rule {
	return &Rule{
		ID:       "EM2-010",
		Name:     "Voting Disk IO超时(Voting Disk IO Timeout)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACVotingDiskIO},
			{Type: SignalKeyword, Key: "voting disk"},
			{Type: SignalKeyword, Key: "vote timeout"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACVotingDiskIO, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查voting disk状态和IO延迟",
			Query: QueryVotingDiskStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(200),
					Label:    "voting disk IO延迟 > 200ms,严重",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查voting disk数量和存储位置",
						Query: QueryVotingDiskStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("SINGLE_VD"),
								Label:    "仅有一个voting disk,无冗余",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Voting disk IO延迟 > 200ms且仅有一个voting disk,任何IO卡顿将导致节点被踢出集群"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查voting disk底层存储健康状态", SkillCommand: "/sql @voting_disk_check", RawSQL: "-- crsctl query css votedisk\n-- 检查ASM磁盘健康: SELECT name, path, mode_status, state FROM V$ASM_DISK WHERE group_number = (SELECT group_number FROM V$ASM_DISKGROUP WHERE name = '{vd_dg}')"},
									{Type: ActionFix, Desc: "增加voting disk到高性能存储", SkillCommand: "/sql @add_voting_disk", RawSQL: "-- crsctl add css votedisk +FAST_DG", Risk: "需在维护窗口操作"},
									{Type: ActionPrevent, Desc: "确保voting disk所在存储有IO QoS保证", SkillCommand: "/sql @storage_perf", RawSQL: "-- 配置存储层面IO优先级保证voting disk延迟 < 10ms"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "多个voting disk但IO延迟高",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Voting disk IO延迟 > 200ms,虽有多个voting disk但如果存储共享同一路径则无法提供真正冗余"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将voting disk分布到不同存储路径", SkillCommand: "/sql @voting_disk_distribute", RawSQL: "-- 确保voting disk分布在不同ASM disk group/存储"},
									{Type: ActionInvestigate, Desc: "检查底层存储IO性能", SkillCommand: "/sql @io_stats", RawSQL: "SELECT filetype_name, small_read_megabytes, large_read_megabytes, small_read_servicetime, large_read_servicetime FROM V$IOSTAT_FILE_BY_FILETYPE ORDER BY small_read_servicetime DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "voting disk IO有延迟但未超临界",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "检测到voting disk IO延迟,虽未达到致命阈值但需关注,存储性能下降可能逐渐恶化"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "监控voting disk IO延迟趋势", SkillCommand: "/sql @vd_io_trend", RawSQL: "-- crsctl query css votedisk\n-- 检查cssd trace中vote IO timing"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "voting disk IO正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Voting disk IO正常,无超时风险"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"EM2-009"},
		Tags:     []string{"emergency", "rac", "voting_disk", "io", "cluster"},
		Versions: "10g+",
	}
}

// ruleRACONSNotification detects Oracle Notification Service (ONS) and
// Fast Application Notification (FAN) failures in RAC, which prevent
// connection failover and load balancing from working correctly.
func ruleRACONSNotification() *Rule {
	return &Rule{
		ID:       "EM2-011",
		Name:     "ONS/FAN通知失败(ONS Notification Failure)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACONSFailure},
			{Type: SignalKeyword, Key: "ONS"},
			{Type: SignalKeyword, Key: "FAN"},
			{Type: SignalKeyword, Key: "notification failure"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACONSFailure, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ONS daemon状态和FAN事件传播",
			Query: QueryONSConfig,
			Branches: []Branch{
				{
					Match:    MatchEquals("ONS_DOWN"),
					Label:    "ONS daemon未运行",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查ONS是否配置且是否为CRS管理",
						Query: QueryONSConfig,
						Branches: []Branch{
							{
								Match:    MatchEquals("NOT_CONFIGURED"),
								Label:    "ONS未配置",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ONS未配置或未运行,FAN事件(UP/DOWN/PLANNEDDOWN)无法传播到客户端,Fast Connection Failover不工作"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启动ONS daemon", SkillCommand: "/sql @start_ons", RawSQL: "-- $GRID_HOME/bin/onsctl start\n-- 或: srvctl start ons"},
									{Type: ActionFix, Desc: "配置ONS监听地址和端口", SkillCommand: "/sql @config_ons", RawSQL: "-- srvctl config ons\n-- $GRID_HOME/opmn/conf/ons.config: nodes={node1}:{port},{node2}:{port}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "ONS已配置但daemon意外停止",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ONS daemon意外停止,FAN事件传播中断,应用无法感知实例故障进行自动failover"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即重启ONS", SkillCommand: "/sql @restart_ons", RawSQL: "-- srvctl stop ons; srvctl start ons"},
									{Type: ActionInvestigate, Desc: "检查ONS日志确认停止原因", SkillCommand: "/sql @ons_log", RawSQL: "-- cat $GRID_HOME/log/{hostname}/ons/ons.log | tail -50"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("FAN_DELAY"),
					Label:    "FAN事件传播延迟",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "FAN事件传播存在延迟,客户端可能无法及时感知节点/服务状态变更,failover响应慢"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查ONS网络连通性", SkillCommand: "/sql @ons_ping", RawSQL: "-- onsctl ping"},
						{Type: ActionFix, Desc: "检查ONS端口防火墙规则", SkillCommand: "/sql @ons_config_detail", RawSQL: "-- srvctl config ons -detail"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "ONS/FAN正常运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ONS/FAN通知服务正常运行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"emergency", "rac", "ons", "fan", "failover", "notification"},
		Versions: "10g+",
	}
}

// ruleRACCRSResourceFail detects CRS resources (database, listener, service,
// VIP) that fail to start or are in UNKNOWN/OFFLINE state when they should be online.
func ruleRACCRSResourceFail() *Rule {
	return &Rule{
		ID:       "EM2-012",
		Name:     "CRS资源启动失败(CRS Resource Not Starting)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACCRSResourceFail},
			{Type: SignalKeyword, Key: "CRS resource"},
			{Type: SignalKeyword, Key: "crs_stat"},
			{Type: SignalKeyword, Key: "resource fail"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACCRSResourceFail, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查CRS资源状态(crsctl stat res -t)",
			Query: QueryCRSResourceStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("DB_OFFLINE"),
					Label:    "数据库资源OFFLINE",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查数据库实例alert.log中启动失败原因",
						Query: QueryCRSResourceStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("ORA_ERROR"),
								Label:    "实例启动时遇到ORA错误",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "CRS数据库资源OFFLINE,实例启动遇到ORA错误,无法正常open database"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看实例alert.log最近错误", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE originating_timestamp > SYSDATE - 1/24 ORDER BY originating_timestamp DESC FETCH FIRST 30 ROWS ONLY"},
									{Type: ActionFix, Desc: "手动启动实例排查", SkillCommand: "/sql @manual_start", RawSQL: "-- srvctl start database -d {db_name} -o mount\n-- 查看alert log确认mount成功后: ALTER DATABASE OPEN;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非ORA错误导致资源不可用",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "CRS数据库资源OFFLINE,可能为CRS agent问题或依赖资源(ASM/listener)未就绪"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查CRS资源依赖链", SkillCommand: "/sql @crs_deps", RawSQL: "-- crsctl stat res ora.{db}.db -p | grep START_DEPENDENCIES\n-- crsctl stat res -t | grep -E 'OFFLINE|UNKNOWN'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("LISTENER_OFFLINE"),
					Label:    "Listener资源OFFLINE",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "CRS Listener资源OFFLINE,新连接无法建立,影响所有应用"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "启动Listener资源", SkillCommand: "/sql @start_listener", RawSQL: "-- srvctl start listener -l {listener_name}"},
						{Type: ActionInvestigate, Desc: "检查listener.log中错误", SkillCommand: "/sql @listener_log", RawSQL: "-- lsnrctl status {listener_name}\n-- tail -50 $GRID_HOME/log/{hostname}/listener_{name}/listener_{name}.log"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "CRS资源状态正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有CRS资源处于正常状态"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"EM2-013"},
		CausesOf: []string{},
		Tags:     []string{"emergency", "rac", "crs", "resource", "cluster"},
		Versions: "10g+",
	}
}

// ruleRACASMInstanceDown detects ASM instance down or not running in RAC.
// ASM provides storage for database files; if ASM instance is down, the
// database instance cannot access its data files and will crash or refuse to start.
func ruleRACASMInstanceDown() *Rule {
	return &Rule{
		ID:       "EM2-013",
		Name:     "ASM实例宕机(ASM Instance Down)",
		Category: "emergency",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRACASMInstDown},
			{Type: SignalKeyword, Key: "ASM instance"},
			{Type: SignalKeyword, Key: "ASM down"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRACASMInstDown, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ASM实例状态",
			Query: QueryASMInstanceStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("DOWN"),
					Label:    "ASM实例完全宕机",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查ASM实例alert.log中宕机原因",
						Query: QueryASMInstanceStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("DISK_FAILURE"),
								Label:    "ASM磁盘故障导致实例崩溃",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM实例因底层磁盘故障宕机,所有依赖该ASM实例的数据库实例将无法访问数据文件"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并修复底层存储问题", SkillCommand: "/sql @asm_alert", RawSQL: "-- 查看ASM alert.log:\n-- adrci> show alert -p 'message_text like \"%ORA-%\"'"},
									{Type: ActionUrgent, Desc: "尝试重启ASM实例", SkillCommand: "/sql @start_asm", RawSQL: "-- srvctl start asm", Risk: "如果磁盘仍有故障可能无法启动"},
									{Type: ActionFix, Desc: "替换故障磁盘后重启", SkillCommand: "/sql @asm_disk_repair", RawSQL: "-- srvctl start asm\n-- ALTER DISKGROUP {dg} ADD DISK '{new_path}'"},
								},
							},
							{
								Match:    MatchEquals("ORA_600_7445"),
								Label:    "ASM实例遇到内部错误崩溃",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ASM实例因ORA-600/ORA-7445内部错误崩溃,需收集诊断信息并尝试重启"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "重启ASM实例", SkillCommand: "/sql @restart_asm", RawSQL: "-- srvctl stop asm -f; srvctl start asm"},
									{Type: ActionInvestigate, Desc: "收集ASM诊断信息", SkillCommand: "/sql @asm_diag", RawSQL: "-- adrci> show incident -mode detail"},
									{Type: ActionFix, Desc: "应用ASM补丁修复已知bug", SkillCommand: "/sql @asm_version", RawSQL: "-- $GRID_HOME/OPatch/opatch lspatches"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致ASM宕机",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "ASM实例宕机,具体原因需查看ASM alert.log分析"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "尝试重启ASM实例", SkillCommand: "/sql @start_asm", RawSQL: "-- srvctl start asm"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("RESTRICTED"),
					Label:    "ASM实例处于RESTRICTED模式",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "ASM实例处于RESTRICTED模式,新的数据库实例无法挂载ASM磁盘组"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "取消ASM RESTRICTED模式", SkillCommand: "/sql @asm_unrestrict", RawSQL: "ALTER SYSTEM DISABLE RESTRICTED SESSION"},
						{Type: ActionInvestigate, Desc: "检查为何进入RESTRICTED模式", SkillCommand: "/sql @asm_alert_check", RawSQL: "-- 查看ASM alert.log确认原因"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "ASM实例正常运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ASM实例正常运行中"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"asm_disk_failure"},
		CausesOf: []string{"EM2-012", "database_hang"},
		Tags:     []string{"emergency", "rac", "asm", "instance", "storage"},
		Versions: "10g+",
	}
}

// ═══════════════════════════════════════════════════════════════════════════
// Data Guard Emergency Rules (5)
// ═══════════════════════════════════════════════════════════════════════════

// ruleDGFlashbackRebuild detects scenarios where flashback database can be used
// to rebuild a standby after failover, instead of a full restore. This is critical
// during DR scenarios to minimize RTO.
func ruleDGFlashbackRebuild() *Rule {
	return &Rule{
		ID:       "EM2-014",
		Name:     "Flashback Database重建备库(Flashback Rebuild Standby)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGFlashbackStatus},
			{Type: SignalKeyword, Key: "flashback rebuild"},
			{Type: SignalKeyword, Key: "reinstate"},
			{Type: SignalKeyword, Key: "flashback standby"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGFlashbackStatus, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查flashback database是否在主备库都已启用",
			Query: QueryDGFlashbackInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("BOTH_DISABLED"),
					Label:    "主备库均未启用flashback",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查FRA空间是否足够启用flashback",
						Query: QueryDGFlashbackInfo,
						Branches: []Branch{
							{
								Match:    MatchGT(20),
								Label:    "FRA有足够空间",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "主备库均未启用Flashback Database,failover后旧主库只能通过全量恢复重建为备库,RTO长达数小时。FRA空间充足可立即启用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "在主库启用flashback database", SkillCommand: "/sql @enable_flashback", RawSQL: "ALTER DATABASE FLASHBACK ON", Risk: "会增加FRA空间使用和少量IO开销", Rollback: "ALTER DATABASE FLASHBACK OFF"},
									{Type: ActionFix, Desc: "在备库启用flashback database", SkillCommand: "/sql @enable_flashback_standby", RawSQL: "-- 在备库MRP停止后: ALTER DATABASE FLASHBACK ON;\n-- 重启MRP: ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION"},
									{Type: ActionPrevent, Desc: "设置合理的db_flashback_retention_target", SkillCommand: "/params db_flashback_retention_target", RawSQL: "ALTER SYSTEM SET db_flashback_retention_target = 1440 SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "FRA空间不足,需先扩容",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "主备库未启用flashback且FRA空间不足,需先扩容FRA再启用flashback"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大db_recovery_file_dest_size", SkillCommand: "/params db_recovery_file_dest_size", RawSQL: "ALTER SYSTEM SET db_recovery_file_dest_size = '{new_size}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("PRIMARY_ONLY"),
					Label:    "仅主库启用flashback,备库未启用",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "备库未启用Flashback Database,如果主库failover,旧备库(新主库)无法使用flashback reinstate旧主库为新备库"},
					},
					Actions: []Action{
						{Type: ActionFix, Desc: "在备库启用flashback", SkillCommand: "/sql @enable_flashback_standby", RawSQL: "-- 备库上执行:\nALTER DATABASE RECOVER MANAGED STANDBY DATABASE CANCEL;\nALTER DATABASE FLASHBACK ON;\nALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "主备库均已启用flashback",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Flashback Database在主备库均已启用,failover后可快速reinstate旧主库"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"ha_dg", "flashback", "rebuild", "failover", "rto"},
		Versions: "10g+",
	}
}

// ruleDGSnapshotStandby detects snapshot standby conversion issues. Snapshot
// standby allows the standby to be opened read-write temporarily for testing,
// but must be converted back before redo gap becomes too large.
func ruleDGSnapshotStandby() *Rule {
	return &Rule{
		ID:       "EM2-015",
		Name:     "Snapshot Standby转换问题(Snapshot Standby Issues)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGSnapshotStandby},
			{Type: SignalKeyword, Key: "snapshot standby"},
			{Type: SignalKeyword, Key: "convert standby"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGSnapshotStandby, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查备库是否处于snapshot standby模式",
			Query: QueryDGSnapshotInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("SNAPSHOT"),
					Label:    "备库处于snapshot standby模式",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查snapshot standby持续时间和累积redo gap",
						Query: QueryDGSnapshotInfo,
						Branches: []Branch{
							{
								Match:    MatchGT(24),
								Label:    "snapshot模式超过24小时,redo gap大",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "备库处于snapshot standby模式超过24小时,累积大量redo gap,转换回physical standby时需要长时间应用redo,期间DR保护中断"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "尽快将snapshot standby转换回physical standby", SkillCommand: "/sql @convert_physical", RawSQL: "-- 1. SHUTDOWN IMMEDIATE;\n-- 2. STARTUP MOUNT;\n-- 3. ALTER DATABASE CONVERT TO PHYSICAL STANDBY;\n-- 4. SHUTDOWN IMMEDIATE;\n-- 5. STARTUP;\n-- 6. ALTER DATABASE RECOVER MANAGED STANDBY DATABASE USING CURRENT LOGFILE DISCONNECT FROM SESSION;", Risk: "转换过程需要停机,flashback会自动应用到snapshot之前的SCN"},
									{Type: ActionInvestigate, Desc: "检查累积的redo gap", SkillCommand: "/sql @dg_gap_size", RawSQL: "SELECT thread#, MAX(sequence#) - MIN(sequence#) AS gap_sequences FROM V$ARCHIVED_LOG WHERE first_time > (SELECT TO_DATE(flashback_on,'YYYY-MM-DD HH24:MI:SS') FROM V$DATABASE) AND applied = 'NO' GROUP BY thread#"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "snapshot模式时间较短,可控",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "备库处于snapshot standby模式,DR保护暂时中断,需在测试完成后及时转回"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "完成测试后转换回physical standby", SkillCommand: "/sql @convert_physical", RawSQL: "ALTER DATABASE CONVERT TO PHYSICAL STANDBY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("CONVERT_FAILED"),
					Label:    "snapshot转换回physical失败",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Snapshot standby转换回physical standby失败,可能为flashback日志不足或FRA空间不够"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查转换失败原因", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%convert%' OR message_text LIKE '%flashback%' AND originating_timestamp > SYSDATE - 1 ORDER BY originating_timestamp DESC FETCH FIRST 20 ROWS ONLY"},
						{Type: ActionFix, Desc: "如flashback日志不足,可能需要重建standby", SkillCommand: "/sql @dg_rebuild", RawSQL: "-- RMAN> DUPLICATE TARGET DATABASE FOR STANDBY FROM ACTIVE DATABASE;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "备库为正常physical standby",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "备库处于正常的physical standby模式,DR保护正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"dg_apply_lag"},
		Tags:     []string{"ha_dg", "snapshot_standby", "convert", "testing"},
		Versions: "11g+",
	}
}

// ruleDGProtectionModeChange detects risks associated with Data Guard protection
// mode changes (Maximum Performance / Maximum Availability / Maximum Protection).
func ruleDGProtectionModeChange() *Rule {
	return &Rule{
		ID:       "EM2-016",
		Name:     "Data Guard保护模式变更风险(Protection Mode Change)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGProtectionMode},
			{Type: SignalKeyword, Key: "protection mode"},
			{Type: SignalKeyword, Key: "maximum protection"},
			{Type: SignalKeyword, Key: "maximum availability"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGProtectionMode, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前保护模式和实际保护级别",
			Query: QueryDGProtectionInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("MAX_PROTECTION"),
					Label:    "Maximum Protection模式",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查standby是否可达(不可达时主库将停止)",
						Query: QueryDGStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("STANDBY_UNREACHABLE"),
								Label:    "备库不可达,主库面临停机风险!",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Maximum Protection模式下备库不可达,如果所有standby destination失效,主库将自动shutdown以保护数据零丢失承诺"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即恢复备库连通性或降级到Maximum Availability", SkillCommand: "/sql @dg_downgrade", RawSQL: "ALTER DATABASE SET STANDBY DATABASE TO MAXIMIZE AVAILABILITY", Risk: "降级后允许数据丢失,但主库不会因备库不可达停机", Rollback: "ALTER DATABASE SET STANDBY DATABASE TO MAXIMIZE PROTECTION"},
									{Type: ActionInvestigate, Desc: "检查备库和网络状态", SkillCommand: "/sql @dg_dest_status", RawSQL: "SELECT dest_id, status, error, type FROM V$ARCHIVE_DEST_STATUS WHERE type = 'PHYSICAL'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "Maximum Protection模式且备库可达",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "当前为Maximum Protection模式,提供零数据丢失保护,但备库不可达时主库将自动停机,需确保网络高度可靠"},
								},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "确认有足够的standby destination和网络冗余", SkillCommand: "/sql @dg_config", RawSQL: "SELECT protection_mode, protection_level, database_role, switchover_status FROM V$DATABASE"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("MODE_MISMATCH"),
					Label:    "protection_mode与protection_level不一致",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Data Guard protection_mode与protection_level不一致(如配置Maximum Availability但实际降级为Maximum Performance),表明同步传输有问题"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查protection mode/level差异原因", SkillCommand: "/sql @dg_protection", RawSQL: "SELECT protection_mode, protection_level, database_role FROM V$DATABASE"},
						{Type: ActionFix, Desc: "解决redo transport问题恢复保护级别", SkillCommand: "/sql @dg_dest_error", RawSQL: "SELECT dest_id, status, error, transmit_mode FROM V$ARCHIVE_DEST_STATUS WHERE type = 'PHYSICAL'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "保护模式配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Data Guard保护模式配置正常,mode与level一致"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"dg_transport_lag"},
		CausesOf: []string{},
		Tags:     []string{"ha_dg", "protection_mode", "sync", "availability"},
		Versions: "9i+",
	}
}

// ruleDGFarSyncConfig detects Data Guard Far Sync configuration issues.
// Far Sync instances act as redo transport relays for zero-data-loss protection
// across long distances.
func ruleDGFarSyncConfig() *Rule {
	return &Rule{
		ID:       "EM2-017",
		Name:     "Far Sync配置问题(Far Sync Configuration)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGFarSyncStatus},
			{Type: SignalKeyword, Key: "far sync"},
			{Type: SignalKeyword, Key: "farsync"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGFarSyncStatus, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Far Sync实例状态和redo传输链路",
			Query: QueryDGFarSyncInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("FARSYNC_DOWN"),
					Label:    "Far Sync实例不可用",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有备用传输路径(alternate dest)",
						Query: QueryDGFarSyncInfo,
						Branches: []Branch{
							{
								Match:    MatchEquals("NO_ALTERNATE"),
								Label:    "无备用路径,redo传输中断",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Far Sync实例不可用且无备用redo transport路径配置,远端standby无法接收redo,零数据丢失保护中断"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并恢复Far Sync实例", SkillCommand: "/sql @farsync_status", RawSQL: "SELECT dest_id, status, error, type, target FROM V$ARCHIVE_DEST_STATUS WHERE type = 'FAR SYNC'"},
									{Type: ActionFix, Desc: "配置alternate destination作为fallback", SkillCommand: "/sql @alt_dest", RawSQL: "ALTER SYSTEM SET LOG_ARCHIVE_DEST_2 = 'SERVICE=remote_standby ASYNC ALTERNATE=LOG_ARCHIVE_DEST_3' SCOPE=BOTH"},
									{Type: ActionPrevent, Desc: "部署第二个Far Sync实例作为冗余", SkillCommand: "/sql @farsync_config", RawSQL: "-- DGMGRL: ADD FAR_SYNC farsync2 AS CONNECT IDENTIFIER IS farsync2_tns;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "有备用路径,已自动failover",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Far Sync实例不可用,redo transport已failover到备用路径,但需尽快恢复Far Sync保持零丢失保护"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "恢复Far Sync实例", SkillCommand: "/sql @restart_farsync", RawSQL: "-- srvctl start instance -d {farsync_db} -i {farsync_inst}"},
									{Type: ActionInvestigate, Desc: "检查Far Sync实例alert.log", SkillCommand: "/sql @farsync_alert", RawSQL: "-- 在Far Sync节点查看alert.log"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("LAG_HIGH"),
					Label:    "Far Sync redo接收延迟高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Far Sync实例接收redo延迟高,可能为主库到Far Sync之间网络问题,影响下游standby的数据保护"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查主库到Far Sync的网络延迟", SkillCommand: "/sql @farsync_lag", RawSQL: "SELECT name, value, datum_time FROM V$DATAGUARD_STATS WHERE name IN ('transport lag','apply lag')"},
						{Type: ActionFix, Desc: "检查Far Sync SRL配置和IO性能", SkillCommand: "/sql @farsync_srl", RawSQL: "SELECT group#, bytes/1024/1024 size_mb, status FROM V$STANDBY_LOG"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Far Sync配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Far Sync实例正常运行,redo传输链路健康"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"dg_transport_lag"},
		Tags:     []string{"ha_dg", "far_sync", "zero_data_loss", "relay"},
		Versions: "12c+",
	}
}

// ruleDGPasswordSync detects password file synchronization issues between
// primary and standby databases. When SYS/SYSDBA password changes on primary,
// the password file must be synced to standby, otherwise redo transport
// and broker connections will fail.
func ruleDGPasswordSync() *Rule {
	return &Rule{
		ID:       "EM2-018",
		Name:     "密码文件同步问题(Password File Sync)",
		Category: "ha_dg",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDGPasswordFileSync},
			{Type: SignalKeyword, Key: "password file"},
			{Type: SignalKeyword, Key: "password sync"},
			{Type: SignalKeyword, Key: "orapwd"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDGPasswordFileSync, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查密码文件同步状态(12c+ remote_login_passwordfile)",
			Query: QueryDGPasswordFileInfo,
			Branches: []Branch{
				{
					Match:    MatchEquals("OUT_OF_SYNC"),
					Label:    "主备密码文件不同步",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查Oracle版本是否支持自动密码文件同步(12.2+)",
						Query: QueryDGPasswordFileInfo,
						Branches: []Branch{
							{
								Match:    MatchEquals("AUTO_SYNC_AVAILABLE"),
								Label:    "12.2+支持自动密码文件传输",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "主备库密码文件不同步,Oracle 12.2+支持通过redo自动传输密码文件变更,但需确认此功能已启用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "确认redo transport正常(密码文件会自动随redo传输)", SkillCommand: "/sql @dg_pwd_check", RawSQL: "SELECT dest_id, status, error FROM V$ARCHIVE_DEST_STATUS WHERE type = 'PHYSICAL'"},
									{Type: ActionInvestigate, Desc: "检查是否有ORA-01017相关错误", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01017%' OR message_text LIKE '%password%' AND originating_timestamp > SYSDATE - 7 ORDER BY originating_timestamp DESC FETCH FIRST 10 ROWS ONLY"},
									{Type: ActionFix, Desc: "如自动同步不工作,手动复制密码文件", SkillCommand: "/sql @pwd_file_info", RawSQL: "SELECT file_name FROM V$PASSWORDFILE_INFO", Risk: "需停止备库MRP后操作"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "12.1或更早,需手动同步",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "主备库密码文件不同步且版本不支持自动同步(12.1及更早),redo transport认证可能失败导致DG断开"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "手动将主库密码文件复制到备库", SkillCommand: "/sql @pwd_file_copy", RawSQL: "-- 1. 在主库: orapwd file=$ORACLE_HOME/dbs/orapw{SID} entries=10 force=y\n-- 2. scp到备库: scp orapw{SID} standby:$ORACLE_HOME/dbs/\n-- 3. 在ASM: srvctl modify database -d {db} -pwfile +DATA/{db}/PASSWORD/pwdfile", Risk: "复制过程中如密码再次变更需重复操作"},
									{Type: ActionInvestigate, Desc: "确认ORA-01017是否已发生", SkillCommand: "/alert", RawSQL: "SELECT message_text FROM V$DIAG_ALERT_EXT WHERE message_text LIKE '%ORA-01017%' ORDER BY originating_timestamp DESC FETCH FIRST 5 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchEquals("SYS_PASSWORD_CHANGED"),
					Label:    "近期SYS密码变更,需确认同步",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查密码变更后redo transport是否正常",
						Query: QueryDGStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("ERROR"),
								Label:    "redo transport出错,可能密码不同步",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SYS密码近期变更后redo transport出错,极可能为密码文件未同步到备库,导致认证失败"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "同步密码文件到备库", SkillCommand: "/sql @sync_pwd", RawSQL: "-- scp $ORACLE_HOME/dbs/orapw{SID} standby:$ORACLE_HOME/dbs/"},
									{Type: ActionFix, Desc: "验证同步后redo transport恢复", SkillCommand: "/sql @dg_verify", RawSQL: "SELECT dest_id, status, error FROM V$ARCHIVE_DEST_STATUS WHERE type = 'PHYSICAL'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "redo transport正常,密码同步OK",
								Severity: SeverityLow,
								Findings: []Finding{
									{Desc: "SYS密码近期变更但redo transport正常,密码文件已正确同步或版本>=12.2自动同步"},
								},
								Actions: nil,
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "密码文件同步正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "密码文件主备同步正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"dg_transport_lag"},
		Tags:     []string{"ha_dg", "password", "sync", "security", "authentication"},
		Versions: "9i+",
	}
}
