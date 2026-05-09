/*-------------------------------------------------------------------------
 *
 * rules_ops_ext.go
 *	  Oracle rule engine — operations rule extensions (advanced compaction, segment growth).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_ops_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Query IDs for extended operations rules ─────────────────────────────────

const (
	QueryTNSListenerStatus   QueryID = "tns_listener_status"
	QueryDRCPPoolStats       QueryID = "drcp_pool_stats"
	QuerySCANListenerBalance QueryID = "scan_listener_balance"
	QueryServiceRegistration QueryID = "service_registration"
	QuerySchedulerJobStatus  QueryID = "scheduler_job_status"
	QuerySchedulerJobRunning QueryID = "scheduler_job_running"
	QuerySchedulerJobChains  QueryID = "scheduler_job_chains"
	QueryDataPumpSessions    QueryID = "data_pump_sessions"
	QueryDataPumpHistory     QueryID = "data_pump_history"
	QueryDefaultTSUsage      QueryID = "default_ts_usage"
	QueryTempTSFiles         QueryID = "temp_ts_files"
	QueryUndoTSAutoextend    QueryID = "undo_ts_autoextend"
	QueryBigfileTSInfo       QueryID = "bigfile_ts_info"
	QueryCompressionStats    QueryID = "compression_stats"
	QueryHCCEligible         QueryID = "hcc_eligible"
	QueryOLTPCompressionCPU  QueryID = "oltp_compression_cpu"
	QueryInMemoryPopulate    QueryID = "inmemory_populate"
	QueryInMemoryObjects     QueryID = "inmemory_objects"
	QueryInMemoryAreaSize    QueryID = "inmemory_area_size"
)

// ─── Metric constants for extended operations rules ──────────────────────────

const (
	metricTNSTimeoutCount        = "tns_timeout_count"
	metricDRCPPoolBusyPct        = "drcp_pool_busy_pct"
	metricSCANImbalancePct       = "scan_imbalance_pct"
	metricServiceNotRegistered   = "service_not_registered"
	metricJobFailureCount        = "scheduler_job_failure_count"
	metricJobOverlapCount        = "scheduler_job_overlap_count"
	metricJobChainStuckCount     = "scheduler_job_chain_stuck_count"
	metricDataPumpElapsedMin     = "data_pump_elapsed_min"
	metricDefaultTSUsedPct       = "default_ts_used_pct"
	metricTempTSFileCount        = "temp_ts_file_count"
	metricUndoAutoextendOff      = "undo_autoextend_off"
	metricCompressionDMLPct      = "compression_dml_pct"
	metricOLTPCompressionCPUPct  = "oltp_compression_cpu_pct"
	metricIMPopulateStallCount   = "im_populate_stall_count"
	metricIMSizePct              = "inmemory_size_pct"
)

// ─── Network / TNS Rules (4) ─────────────────────────────────────────────────

func networkRules() []*Rule {
	return []*Rule{
		ruleTNSTimeout(),
		ruleDRCPPoolExhaust(),
		ruleSCANListenerImbalance(),
		ruleServiceRegistration(),
	}
}

// ruleTNSTimeout detects TNS connection timeouts caused by listener
// misconfiguration, network latency, or overloaded listeners.
func ruleTNSTimeout() *Rule {
	return &Rule{
		ID:       "OP2-001",
		Name:     "TNS连接超时(TNS Connection Timeout)",
		Category: "network",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-12170"},
			{Type: SignalErrorCode, Key: "ORA-12535"},
			{Type: SignalErrorCode, Key: "TNS-12535"},
			{Type: SignalMetric, Key: metricTNSTimeoutCount},
			{Type: SignalKeyword, Key: "tns timeout"},
			{Type: SignalKeyword, Key: "connection timeout"},
			{Type: SignalKeyword, Key: "listener timeout"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricTNSTimeoutCount, Op: OpGT, Value: 5},
			},
		},
		Tree: &TreeNode{
			Step:  "检查TNS listener状态与连接超时",
			Query: QueryTNSListenerStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    "大量TNS超时(>50/小时),listener可能过载",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查listener活跃连接数与processes参数",
						Query: QueryProcessesSessions,
						Branches: []Branch{
							{
								Match:    MatchGT(90),
								Label:    "processes使用>90%,连接数接近上限",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "TNS大量超时且数据库进程数接近processes上限,新连接无法建立"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并增大processes参数", SkillCommand: "/params processes", RawSQL: "SELECT resource_name, current_utilization, max_utilization, limit_value FROM V$RESOURCE_LIMIT WHERE resource_name IN ('processes','sessions')"},
									{Type: ActionFix, Desc: "增大processes参数(需重启)", SkillCommand: "/sql ALTER SYSTEM SET processes", RawSQL: "ALTER SYSTEM SET processes = {new_value} SCOPE=SPFILE"},
									{Type: ActionInvestigate, Desc: "查找连接泄漏的应用程序", SkillCommand: "/sql @session_by_program", RawSQL: "SELECT program, machine, COUNT(*) cnt FROM V$SESSION WHERE type = 'USER' GROUP BY program, machine ORDER BY cnt DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "processes有余量,检查网络/listener配置",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "TNS超时频繁但数据库进程有余量,可能为网络层或listener配置问题"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查listener日志中的错误", SkillCommand: "/sql @listener_log", RawSQL: "-- 检查listener.log:\n-- adrci> SHOW ALERT -term\n-- lsnrctl status\n-- lsnrctl services"},
									{Type: ActionFix, Desc: "配置SQLNET.INBOUND_CONNECT_TIMEOUT防止慢连接", SkillCommand: "/sql @sqlnet_params", RawSQL: "-- sqlnet.ora参数:\n-- SQLNET.INBOUND_CONNECT_TIMEOUT=60\n-- SQLNET.RECV_TIMEOUT=30\n-- SQLNET.SEND_TIMEOUT=30"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(5),
					Label:    "少量TNS超时(5-50/小时)",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在间歇性TNS连接超时,建议检查网络稳定性和listener负载"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查listener连接统计", SkillCommand: "/sql @listener_stats", RawSQL: "-- lsnrctl status\n-- lsnrctl services\nSELECT inst_id, name, value FROM GV$SYSSTAT WHERE name LIKE '%logon%' ORDER BY inst_id, name"},
						{Type: ActionPrevent, Desc: "配置连接池减少新建连接数", SkillCommand: "/sql @connection_pool", RawSQL: "-- 应用端建议启用连接池\n-- Oracle DRCP: EXEC DBMS_CONNECTION_POOL.START_POOL;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "TNS连接正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "TNS连接无超时,网络和listener状态正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-002"},
		Tags:     []string{"network", "tns", "listener", "connection"},
		Versions: "9i+",
	}
}

// ruleDRCPPoolExhaust detects Database Resident Connection Pooling (DRCP) pool
// exhaustion where all pooled servers are busy and new requests are queued.
func ruleDRCPPoolExhaust() *Rule {
	return &Rule{
		ID:       "OP2-002",
		Name:     "DRCP连接池耗尽(DRCP Pool Exhaustion)",
		Category: "network",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDRCPPoolBusyPct},
			{Type: SignalKeyword, Key: "drcp pool"},
			{Type: SignalKeyword, Key: "connection pool exhaust"},
			{Type: SignalKeyword, Key: "pooled server"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDRCPPoolBusyPct, Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DRCP连接池统计",
			Query: QueryDRCPPoolStats,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "DRCP池>95%繁忙,请求排队",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查DRCP等待队列深度",
						Query: QueryDRCPPoolStats,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在等待队列,请求被阻塞",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "DRCP连接池全部繁忙且请求排队,应用端连接获取延迟增大"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大DRCP池MAXSIZE", SkillCommand: "/sql @drcp_config", RawSQL: "BEGIN DBMS_CONNECTION_POOL.ALTER_PARAM('','MAXSIZE','{new_max}'); END;"},
									{Type: ActionInvestigate, Desc: "检查DRCP连接池使用详情", SkillCommand: "/sql @drcp_stats", RawSQL: "SELECT num_requests, num_hits, num_misses, num_waits, wait_time, client_req_timeouts, num_authentications FROM V$CPOOL_STATS"},
									{Type: ActionFix, Desc: "检查应用端连接释放是否及时", SkillCommand: "/sql @drcp_sessions", RawSQL: "SELECT cclass_name, num_servers, num_busy, num_idle, num_waits FROM V$CPOOL_CC_STATS ORDER BY num_busy DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "池繁忙但无排队",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "DRCP连接池接近饱和,建议提前扩容"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "预防性增大DRCP池", SkillCommand: "/sql @drcp_config", RawSQL: "BEGIN DBMS_CONNECTION_POOL.ALTER_PARAM('','MAXSIZE','{new_max}'); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "DRCP池80-95%繁忙,接近饱和",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "DRCP连接池使用率>80%,建议关注增长趋势"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看DRCP池统计", SkillCommand: "/sql @drcp_stats", RawSQL: "SELECT num_requests, num_hits, num_misses, num_waits FROM V$CPOOL_STATS"},
						{Type: ActionPrevent, Desc: "配置DRCP池INACTIVITY_TIMEOUT回收空闲连接", SkillCommand: "/sql @drcp_config", RawSQL: "BEGIN DBMS_CONNECTION_POOL.ALTER_PARAM('','INACTIVITY_TIMEOUT','300'); END;"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "DRCP连接池使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "DRCP连接池使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"OP2-001"},
		CausesOf: []string{},
		Tags:     []string{"network", "drcp", "connection_pool"},
		Versions: "11g+",
	}
}

// ruleSCANListenerImbalance detects uneven connection distribution across
// SCAN listeners in RAC environments, causing hotspot nodes.
func ruleSCANListenerImbalance() *Rule {
	return &Rule{
		ID:       "OP2-003",
		Name:     "SCAN Listener负载不均(SCAN Listener Imbalance)",
		Category: "network",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricSCANImbalancePct},
			{Type: SignalKeyword, Key: "scan listener"},
			{Type: SignalKeyword, Key: "listener imbalance"},
			{Type: SignalKeyword, Key: "connection distribution"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricSCANImbalancePct, Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查SCAN listener各节点连接分布",
			Query: QuerySCANListenerBalance,
			Branches: []Branch{
				{
					Match:    MatchGT(50),
					Label:    "节点间连接差异>50%,严重不均",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查服务配置与负载均衡目标",
						Query: QueryServiceRegistration,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "服务仅注册在部分节点",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SCAN listener连接分配严重不均,部分服务仅注册在少数节点,导致热点节点"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查各节点服务注册状态", SkillCommand: "/sql @rac_services", RawSQL: "SELECT inst_id, name, network_name, enabled, failover_method, clb_goal, aq_ha_notifications FROM GV$ACTIVE_SERVICES WHERE name NOT LIKE 'SYS%' ORDER BY name, inst_id"},
									{Type: ActionFix, Desc: "设置CLB_GOAL为SHORT实现连接级负载均衡", SkillCommand: "/sql @modify_service", RawSQL: "BEGIN DBMS_SERVICE.MODIFY_SERVICE(service_name => '{svc}', clb_goal => DBMS_SERVICE.CLB_GOAL_SHORT); END;"},
									{Type: ActionFix, Desc: "在所有期望节点上启动服务", SkillCommand: "/sql @start_service", RawSQL: "-- srvctl modify service -d {db} -s {svc} -preferred {inst1},{inst2}\n-- srvctl start service -d {db} -s {svc}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "服务注册正常,检查负载均衡策略",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "服务在各节点均有注册但连接分布不均,可能是负载均衡策略或应用端缓存导致"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "检查LISTENER负载均衡配置", SkillCommand: "/sql @listener_config", RawSQL: "-- srvctl config scan_listener\n-- srvctl config service -d {db} -s {svc}\nSELECT inst_id, name, clb_goal, aq_ha_notifications FROM GV$ACTIVE_SERVICES WHERE name NOT LIKE 'SYS%'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(30),
					Label:    "节点间连接差异30-50%,轻度不均",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "SCAN listener连接分布存在轻度不均衡,建议检查服务配置"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看各实例会话数分布", SkillCommand: "/sql @rac_session_dist", RawSQL: "SELECT inst_id, COUNT(*) session_cnt FROM GV$SESSION WHERE type = 'USER' GROUP BY inst_id ORDER BY inst_id"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "SCAN listener连接分布均匀",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SCAN listener各节点连接分布均匀"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"network", "rac", "scan", "listener", "load_balance"},
		Versions: "11.2+",
	}
}

// ruleServiceRegistration detects database services not properly registered
// with the listener, causing connection failures for specific service names.
func ruleServiceRegistration() *Rule {
	return &Rule{
		ID:       "OP2-004",
		Name:     "服务未注册到Listener(Service Not Registered)",
		Category: "network",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-12514"},
			{Type: SignalErrorCode, Key: "ORA-12541"},
			{Type: SignalErrorCode, Key: "TNS-12514"},
			{Type: SignalMetric, Key: metricServiceNotRegistered},
			{Type: SignalKeyword, Key: "service not registered"},
			{Type: SignalKeyword, Key: "ora-12514"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查数据库服务注册状态",
			Query: QueryServiceRegistration,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现未注册的服务",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查LOCAL_LISTENER和REMOTE_LISTENER参数",
						Query: QueryTNSListenerStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "listener参数配置问题",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库服务未注册到listener,LOCAL_LISTENER或REMOTE_LISTENER参数配置不正确"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并修正LOCAL_LISTENER参数", SkillCommand: "/params local_listener", RawSQL: "SHOW PARAMETER local_listener;\nSHOW PARAMETER remote_listener;\n-- 修正: ALTER SYSTEM SET local_listener='(ADDRESS=(PROTOCOL=TCP)(HOST={host})(PORT={port}))' SCOPE=BOTH;"},
									{Type: ActionFix, Desc: "强制服务重新注册到listener", SkillCommand: "/sql ALTER SYSTEM REGISTER", RawSQL: "ALTER SYSTEM REGISTER"},
									{Type: ActionInvestigate, Desc: "验证listener上的服务列表", SkillCommand: "/sql @listener_services", RawSQL: "-- lsnrctl services\nSELECT name, network_name, enabled, blocked FROM V$ACTIVE_SERVICES ORDER BY name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "listener参数正常,服务可能未启动",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "listener配置正常但服务未注册,服务可能未启动或被禁用"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "启动数据库服务", SkillCommand: "/sql @start_service", RawSQL: "-- srvctl start service -d {db} -s {svc}\n-- 或手动: EXEC DBMS_SERVICE.START_SERVICE('{svc}');"},
									{Type: ActionFix, Desc: "手动触发服务注册", SkillCommand: "/sql ALTER SYSTEM REGISTER", RawSQL: "ALTER SYSTEM REGISTER"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有服务已正确注册",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "数据库服务均已正确注册到listener"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"OP2-001"},
		CausesOf: []string{},
		Tags:     []string{"network", "listener", "service", "registration"},
		Versions: "9i+",
	}
}

// ─── Scheduler Rules (3) ─────────────────────────────────────────────────────

func schedulerRules() []*Rule {
	return []*Rule{
		ruleJobFailure(),
		ruleJobOverlap(),
		ruleJobChainStuck(),
	}
}

// ruleJobFailure detects DBMS_SCHEDULER jobs that have failed, providing
// error details and investigation steps.
func ruleJobFailure() *Rule {
	return &Rule{
		ID:       "OP2-005",
		Name:     "调度任务失败(Scheduler Job Failure)",
		Category: "scheduler",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricJobFailureCount},
			{Type: SignalKeyword, Key: "job failed"},
			{Type: SignalKeyword, Key: "scheduler failure"},
			{Type: SignalKeyword, Key: "dbms_scheduler"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricJobFailureCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查最近失败的DBMS_SCHEDULER作业",
			Query: QuerySchedulerJobStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "大量作业失败(>10),系统性问题",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查失败作业的错误号分布",
						Query: QuerySchedulerJobStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("ORA-12012"),
								Label:    "作业内部错误(ORA-12012)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量调度作业因内部错误失败(ORA-12012),需检查作业存储过程逻辑"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看失败作业详细错误", SkillCommand: "/sql @job_run_details", RawSQL: "SELECT job_name, status, error#, SUBSTR(additional_info,1,200) info, actual_start_date FROM DBA_SCHEDULER_JOB_RUN_DETAILS WHERE status = 'FAILED' AND actual_start_date > SYSDATE - 1 ORDER BY actual_start_date DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "检查并修复失败作业的PL/SQL过程", SkillCommand: "/sql @job_actions", RawSQL: "SELECT owner, job_name, job_type, job_action, enabled, state, failure_count FROM DBA_SCHEDULER_JOBS WHERE failure_count > 0 ORDER BY failure_count DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他错误导致作业失败",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "多个调度作业失败,需逐个排查失败原因"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看所有失败作业及错误号", SkillCommand: "/sql @job_failures", RawSQL: "SELECT job_name, error#, run_count, failure_count, state FROM DBA_SCHEDULER_JOBS WHERE failure_count > 0 ORDER BY failure_count DESC"},
									{Type: ActionFix, Desc: "重新启用被禁用的失败作业", SkillCommand: "/sql @enable_jobs", RawSQL: "-- 排查后重新启用:\nBEGIN DBMS_SCHEDULER.ENABLE('{owner}.{job_name}'); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量作业失败(1-10)",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在少量调度作业失败,建议检查失败原因"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看最近失败的作业", SkillCommand: "/sql @job_run_details", RawSQL: "SELECT job_name, status, error#, SUBSTR(additional_info,1,200) info FROM DBA_SCHEDULER_JOB_RUN_DETAILS WHERE status = 'FAILED' AND actual_start_date > SYSDATE - 7 ORDER BY actual_start_date DESC FETCH FIRST 10 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有调度作业运行正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "DBMS_SCHEDULER作业均正常运行,无失败记录"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-006"},
		Tags:     []string{"scheduler", "job", "failure"},
		Versions: "10g+",
	}
}

// ruleJobOverlap detects concurrent scheduler job execution overlap, where
// multiple instances of the same job run simultaneously causing contention.
func ruleJobOverlap() *Rule {
	return &Rule{
		ID:       "OP2-006",
		Name:     "调度任务并发重叠(Scheduler Job Overlap)",
		Category: "scheduler",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricJobOverlapCount},
			{Type: SignalKeyword, Key: "job overlap"},
			{Type: SignalKeyword, Key: "concurrent job"},
			{Type: SignalKeyword, Key: "job conflict"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricJobOverlapCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前并发运行的调度作业",
			Query: QuerySchedulerJobRunning,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "大量并发作业(>5),可能资源争用",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查并发作业是否有重叠的同名作业",
						Query: QuerySchedulerJobRunning,
						Branches: []Branch{
							{
								Match:    MatchGT(1),
								Label:    "同一作业多实例并发运行",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "同一调度作业存在多个并发实例运行,上次执行未完成新的已启动,可能导致数据不一致或锁争用"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看重叠运行的作业", SkillCommand: "/sql @running_jobs", RawSQL: "SELECT r.owner, r.job_name, r.session_id, r.elapsed_time, r.cpu_used FROM DBA_SCHEDULER_RUNNING_JOBS r ORDER BY r.elapsed_time DESC"},
									{Type: ActionFix, Desc: "设置RESTARTABLE=FALSE防止重叠执行", SkillCommand: "/sql @job_attr", RawSQL: "BEGIN DBMS_SCHEDULER.SET_ATTRIBUTE(name => '{owner}.{job_name}', attribute => 'RESTARTABLE', value => FALSE); END;"},
									{Type: ActionFix, Desc: "增加作业运行窗口或减小频率", SkillCommand: "/sql @job_schedule", RawSQL: "BEGIN DBMS_SCHEDULER.SET_ATTRIBUTE(name => '{owner}.{job_name}', attribute => 'repeat_interval', value => '{new_interval}'); END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "不同作业并发运行,资源竞争",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "多个不同作业同时运行,可能导致资源争用"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用DBMS_SCHEDULER窗口组错开作业调度", SkillCommand: "/sql @job_windows", RawSQL: "SELECT window_name, enabled, active, duration, repeat_interval FROM DBA_SCHEDULER_WINDOWS ORDER BY window_name"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量并发作业,监控中",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "存在少量并发调度作业,当前未发现严重问题"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看运行中的作业", SkillCommand: "/sql @running_jobs", RawSQL: "SELECT owner, job_name, session_id, elapsed_time FROM DBA_SCHEDULER_RUNNING_JOBS ORDER BY elapsed_time DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无并发作业运行",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无并发调度作业运行"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"OP2-005"},
		CausesOf: []string{},
		Tags:     []string{"scheduler", "job", "overlap", "concurrency"},
		Versions: "10g+",
	}
}

// ruleJobChainStuck detects DBMS_SCHEDULER job chains stuck in an intermediate
// step, not progressing to the next step or completion.
func ruleJobChainStuck() *Rule {
	return &Rule{
		ID:       "OP2-007",
		Name:     "作业链卡在中间步骤(Job Chain Stuck)",
		Category: "scheduler",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricJobChainStuckCount},
			{Type: SignalKeyword, Key: "job chain stuck"},
			{Type: SignalKeyword, Key: "chain step"},
			{Type: SignalKeyword, Key: "scheduler chain"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricJobChainStuckCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查DBMS_SCHEDULER作业链状态",
			Query: QuerySchedulerJobChains,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现卡住的作业链步骤",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查卡住步骤的依赖条件",
						Query: QuerySchedulerJobChains,
						Branches: []Branch{
							{
								Match:    MatchEquals("STALLED"),
								Label:    "步骤显式STALLED状态",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "作业链步骤处于STALLED状态,依赖条件无法满足或前序步骤失败未处理"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查作业链步骤状态和依赖", SkillCommand: "/sql @chain_steps", RawSQL: "SELECT owner, chain_name, step_name, state, error_code, start_date, end_date, skip, pause FROM DBA_SCHEDULER_CHAIN_STEPS WHERE state NOT IN ('SUCCEEDED','NOT_STARTED') ORDER BY chain_name, step_name"},
									{Type: ActionFix, Desc: "手动跳过或重置卡住的步骤", SkillCommand: "/sql @chain_alter", RawSQL: "BEGIN DBMS_SCHEDULER.ALTER_CHAIN(chain_name => '{owner}.{chain}', step_name => '{step}', attribute => 'SKIP', value => TRUE); END;"},
									{Type: ActionInvestigate, Desc: "检查链规则和条件", SkillCommand: "/sql @chain_rules", RawSQL: "SELECT owner, chain_name, rule_name, condition, action, comments FROM DBA_SCHEDULER_CHAIN_RULES WHERE chain_name = '{chain}' ORDER BY rule_name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "步骤长时间运行中",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "作业链步骤运行时间异常长,可能卡住"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查步骤关联的作业运行状态", SkillCommand: "/sql @chain_running", RawSQL: "SELECT r.owner, r.job_name, r.session_id, r.elapsed_time, s.state FROM DBA_SCHEDULER_RUNNING_JOBS r JOIN DBA_SCHEDULER_CHAIN_STEPS s ON r.job_name LIKE '%' || s.step_name || '%' ORDER BY r.elapsed_time DESC"},
									{Type: ActionFix, Desc: "停止卡住的作业链并重新调度", SkillCommand: "/sql @stop_chain", RawSQL: "BEGIN DBMS_SCHEDULER.STOP_JOB(job_name => '{owner}.{chain_job}', force => TRUE); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "作业链运行正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "DBMS_SCHEDULER作业链均正常运行或完成"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"OP2-005"},
		CausesOf: []string{},
		Tags:     []string{"scheduler", "job_chain", "stuck"},
		Versions: "11g+",
	}
}

// ─── Data Pump Rules (3) ─────────────────────────────────────────────────────

func dataPumpRules() []*Rule {
	return []*Rule{
		ruleDataPumpSlow(),
		ruleDataPumpParallel(),
		ruleDataPumpEstimate(),
	}
}

// ruleDataPumpSlow detects slow Data Pump export/import operations and provides
// investigation and optimization guidance.
func ruleDataPumpSlow() *Rule {
	return &Rule{
		ID:       "OP2-008",
		Name:     "Data Pump导出导入缓慢(Data Pump Slow)",
		Category: "data_pump",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDataPumpElapsedMin},
			{Type: SignalKeyword, Key: "expdp slow"},
			{Type: SignalKeyword, Key: "impdp slow"},
			{Type: SignalKeyword, Key: "data pump slow"},
			{Type: SignalKeyword, Key: "data pump performance"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDataPumpElapsedMin, Op: OpGT, Value: 60},
			},
		},
		Tree: &TreeNode{
			Step:  "检查当前Data Pump会话与进度",
			Query: QueryDataPumpSessions,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现活跃Data Pump会话",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查Data Pump工作进程等待事件",
						Query: QueryDataPumpSessions,
						Branches: []Branch{
							{
								Match:    MatchEquals("Streams AQ: enqueue blocked on low memory"),
								Label:    "Data Pump因内存不足阻塞",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Data Pump操作因Streams AQ内存不足阻塞,导出/导入速度严重降低"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "增大STREAMS_POOL_SIZE", SkillCommand: "/params streams_pool_size", RawSQL: "ALTER SYSTEM SET streams_pool_size = 256M SCOPE=BOTH"},
									{Type: ActionInvestigate, Desc: "查看Data Pump进度", SkillCommand: "/sql @dp_progress", RawSQL: "SELECT opname, target_desc, sofar, totalwork, ROUND(sofar/NULLIF(totalwork,0)*100,1) pct_done, time_remaining FROM V$SESSION_LONGOPS WHERE opname LIKE 'EXPORT%' OR opname LIKE 'IMPORT%' ORDER BY start_time DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他等待影响Data Pump性能",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "Data Pump操作运行缓慢,需检查I/O和并行配置"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查Data Pump工作进程等待", SkillCommand: "/sql @dp_waits", RawSQL: "SELECT s.sid, s.serial#, s.program, sw.event, sw.seconds_in_wait FROM V$SESSION s JOIN V$SESSION_WAIT sw ON s.sid = sw.sid WHERE s.program LIKE '%DM%' OR s.program LIKE '%DW%' ORDER BY sw.seconds_in_wait DESC"},
									{Type: ActionFix, Desc: "使用PARALLEL参数加速", SkillCommand: "/sql @dp_parallel", RawSQL: "-- expdp user/pass DIRECTORY=dp_dir DUMPFILE=exp_%U.dmp PARALLEL=4\n-- 确保DUMPFILE使用%U通配符以支持多文件并行写入"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无活跃Data Pump会话",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "当前无Data Pump操作在运行"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看最近Data Pump执行历史", SkillCommand: "/sql @dp_history", RawSQL: "SELECT job_name, operation, job_mode, state, degree, attached_sessions FROM DBA_DATAPUMP_JOBS ORDER BY job_name"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"data_pump", "expdp", "impdp", "performance"},
		Versions: "10g+",
	}
}

// ruleDataPumpParallel detects large schema Data Pump operations without
// PARALLEL configured, resulting in suboptimal throughput.
func ruleDataPumpParallel() *Rule {
	return &Rule{
		ID:       "OP2-009",
		Name:     "Data Pump未配置并行(Data Pump No Parallel)",
		Category: "data_pump",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "data pump parallel"},
			{Type: SignalKeyword, Key: "expdp parallel"},
			{Type: SignalKeyword, Key: "impdp parallel"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查Data Pump作业的并行度配置",
			Query: QueryDataPumpHistory,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现Data Pump作业记录",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查大schema导出是否使用PARALLEL",
						Query: QueryDataPumpHistory,
						Branches: []Branch{
							{
								Match:    MatchLTE(1),
								Label:    "大schema导出未使用PARALLEL",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "大型schema(>10GB)的Data Pump操作未配置PARALLEL,导出/导入效率低下"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用PARALLEL参数重新执行(度数建议=CPU核数/2)", SkillCommand: "/sql @dp_parallel_guide", RawSQL: "-- 最佳实践:\n-- expdp DIRECTORY=dp_dir DUMPFILE=exp_%U.dmp PARALLEL=4 FILESIZE=5G\n-- 注意: DUMPFILE必须用%U, FILESIZE控制单文件大小\n-- PARALLEL度数建议: CPU核数/2, 且不超过可用dumpfile数"},
									{Type: ActionPrevent, Desc: "建立Data Pump标准操作模板", SkillCommand: "/sql @dp_parfile", RawSQL: "-- 创建参数文件 dp_exp.par:\n-- DIRECTORY=dp_dir\n-- DUMPFILE=exp_%U.dmp\n-- LOGFILE=exp.log\n-- PARALLEL=4\n-- FILESIZE=5G\n-- COMPRESSION=ALL\n-- expdp user/pass PARFILE=dp_exp.par"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "已配置PARALLEL",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "Data Pump已配置并行度,配置合理"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "验证PARALLEL度数是否最优", SkillCommand: "/sql @cpu_count", RawSQL: "SELECT value FROM V$PARAMETER WHERE name = 'cpu_count'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无Data Pump历史记录",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未发现Data Pump作业记录"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-008"},
		Tags:     []string{"data_pump", "parallel", "best_practice"},
		Versions: "10g+",
	}
}

// ruleDataPumpEstimate detects opportunities to use ESTIMATE_ONLY for Data Pump
// planning before executing full exports.
func ruleDataPumpEstimate() *Rule {
	return &Rule{
		ID:       "OP2-010",
		Name:     "Data Pump未做容量预估(Data Pump No Estimate)",
		Category: "data_pump",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "data pump estimate"},
			{Type: SignalKeyword, Key: "expdp estimate"},
			{Type: SignalKeyword, Key: "dump file size"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查schema大小与Data Pump空间预估",
			Query: QueryDataPumpHistory,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "有Data Pump作业历史",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查目标目录剩余空间是否充足",
						Query: QueryDataPumpHistory,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "曾因空间不足失败",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Data Pump曾因目标目录空间不足失败,建议先使用ESTIMATE_ONLY预估所需空间"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "使用ESTIMATE_ONLY预估导出大小", SkillCommand: "/sql @dp_estimate", RawSQL: "-- expdp user/pass SCHEMAS={schema} ESTIMATE_ONLY=Y LOGFILE=estimate.log\n-- 或使用BLOCKS方法(快速但不精确):\n-- expdp user/pass SCHEMAS={schema} ESTIMATE=BLOCKS ESTIMATE_ONLY=Y"},
									{Type: ActionPrevent, Desc: "检查当前schema总大小", SkillCommand: "/sql @schema_size", RawSQL: "SELECT owner, ROUND(SUM(bytes)/1024/1024/1024,2) gb FROM DBA_SEGMENTS GROUP BY owner ORDER BY gb DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未发生空间不足,但建议预估",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "建议大型导出前使用ESTIMATE_ONLY预估所需空间,避免中途失败"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "养成导出前预估的习惯", SkillCommand: "/sql @dp_estimate", RawSQL: "-- 最佳实践: 导出前先估算\n-- expdp user/pass SCHEMAS={schema} ESTIMATE_ONLY=Y\n-- 确认目标目录空间: SELECT * FROM DBA_DIRECTORIES WHERE directory_name = '{dir}'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无Data Pump历史",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未发现Data Pump作业记录,首次导出建议先做预估"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "首次导出使用ESTIMATE_ONLY", SkillCommand: "/sql @dp_estimate", RawSQL: "-- expdp user/pass SCHEMAS={schema} ESTIMATE_ONLY=Y LOGFILE=estimate.log"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-008"},
		Tags:     []string{"data_pump", "estimate", "planning"},
		Versions: "10g+",
	}
}

// ─── Tablespace Rules (4) ────────────────────────────────────────────────────

func tablespaceRules() []*Rule {
	return []*Rule{
		ruleDefaultTSOveruse(),
		ruleTempTSMultiFile(),
		ruleUndoTSAutoextend(),
		ruleBigfileTSRecommendation(),
	}
}

// ruleDefaultTSOveruse detects the default tablespace (USERS) growing too
// large because application objects are created without specifying a tablespace.
func ruleDefaultTSOveruse() *Rule {
	return &Rule{
		ID:       "OP2-011",
		Name:     "默认表空间过大(Default Tablespace Overuse)",
		Category: "tablespace",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricDefaultTSUsedPct},
			{Type: SignalKeyword, Key: "default tablespace"},
			{Type: SignalKeyword, Key: "users tablespace"},
			{Type: SignalKeyword, Key: "tablespace full"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricDefaultTSUsedPct, Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step:  "检查默认表空间使用情况",
			Query: QueryDefaultTSUsage,
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "默认表空间使用>90%,严重",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查默认表空间中的对象归属",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchGT(10),
								Label:    "默认表空间中存在>10个应用schema对象",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "默认表空间(USERS)使用>90%,大量应用对象未指定专用表空间而落入默认表空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查找默认表空间中的大对象", SkillCommand: "/sql @default_ts_objects", RawSQL: "SELECT owner, segment_name, segment_type, ROUND(bytes/1024/1024) mb FROM DBA_SEGMENTS WHERE tablespace_name = (SELECT property_value FROM DATABASE_PROPERTIES WHERE property_name = 'DEFAULT_PERMANENT_TABLESPACE') ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "创建专用表空间并迁移对象", SkillCommand: "/sql @create_ts", RawSQL: "CREATE TABLESPACE {app_ts} DATAFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 50G;\nALTER TABLE {owner}.{table} MOVE TABLESPACE {app_ts};\nALTER USER {user} DEFAULT TABLESPACE {app_ts};"},
									{Type: ActionPrevent, Desc: "修改用户默认表空间防止后续误用", SkillCommand: "/sql ALTER USER DEFAULT TABLESPACE", RawSQL: "ALTER USER {user} DEFAULT TABLESPACE {app_ts}"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "默认表空间中对象较少,纯粹空间不足",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "默认表空间使用率高,需扩容或清理"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "添加数据文件扩容默认表空间", SkillCommand: "/sql @add_datafile", RawSQL: "ALTER TABLESPACE USERS ADD DATAFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(70),
					Label:    "默认表空间使用70-90%,预警",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "默认表空间使用率>70%,建议检查是否有应用对象误放入默认表空间"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "检查默认表空间中的对象", SkillCommand: "/sql @default_ts_objects", RawSQL: "SELECT owner, segment_type, COUNT(*) obj_cnt, ROUND(SUM(bytes)/1024/1024) total_mb FROM DBA_SEGMENTS WHERE tablespace_name = (SELECT property_value FROM DATABASE_PROPERTIES WHERE property_name = 'DEFAULT_PERMANENT_TABLESPACE') GROUP BY owner, segment_type ORDER BY total_mb DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "默认表空间使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "默认表空间使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full"},
		Tags:     []string{"tablespace", "default", "users", "space"},
		Versions: "9i+",
	}
}

// ruleTempTSMultiFile detects temp tablespace with only a single temp file,
// which becomes a bottleneck for large sorts and hash joins.
func ruleTempTSMultiFile() *Rule {
	return &Rule{
		ID:       "OP2-012",
		Name:     "临时表空间单文件瓶颈(Temp TS Single File)",
		Category: "tablespace",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTempTSFileCount},
			{Type: SignalKeyword, Key: "temp tablespace"},
			{Type: SignalKeyword, Key: "temp file bottleneck"},
			{Type: SignalKeyword, Key: "sort disk"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricTempTSFileCount, Op: OpLTE, Value: 1},
			},
		},
		Tree: &TreeNode{
			Step:  "检查临时表空间文件数与大小",
			Query: QueryTempTSFiles,
			Branches: []Branch{
				{
					Match:    MatchLTE(1),
					Label:    "临时表空间仅1个文件",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查临时表空间使用率和排序活动",
						Query: QueryTempTSFiles,
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "临时表空间使用率>80%",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "临时表空间仅有单个临时文件且使用率>80%,大量排序和哈希连接操作将受I/O瓶颈影响"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即添加多个临时文件分散I/O", SkillCommand: "/sql @add_tempfile", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G;\nALTER TABLESPACE TEMP ADD TEMPFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G;\nALTER TABLESPACE TEMP ADD TEMPFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G;"},
									{Type: ActionInvestigate, Desc: "查看临时空间消耗大户", SkillCommand: "/sql @temp_usage", RawSQL: "SELECT s.sid, s.serial#, s.username, s.program, t.blocks * 8/1024 mb_used, t.tablespace FROM V$SESSION s JOIN V$TEMPSEG_USAGE t ON s.saddr = t.session_addr ORDER BY t.blocks DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用率正常但仍建议多文件",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "临时表空间仅有单文件,建议添加多个文件以分散I/O并提高并行排序性能"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "添加额外临时文件", SkillCommand: "/sql @add_tempfile", RawSQL: "ALTER TABLESPACE TEMP ADD TEMPFILE '+DATA' SIZE 10G AUTOEXTEND ON NEXT 1G MAXSIZE 30G"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "临时表空间已有多个文件",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "临时表空间配置了多个文件,I/O可分散"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"tablespace", "temp", "sort", "io"},
		Versions: "9i+",
	}
}

// ruleUndoTSAutoextend detects undo tablespace without autoextend enabled,
// risking ORA-30036 (unable to extend undo segment) during long transactions.
func ruleUndoTSAutoextend() *Rule {
	return &Rule{
		ID:       "OP2-013",
		Name:     "UNDO表空间未自动扩展(Undo TS No Autoextend)",
		Category: "tablespace",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-30036"},
			{Type: SignalMetric, Key: metricUndoAutoextendOff},
			{Type: SignalKeyword, Key: "undo autoextend"},
			{Type: SignalKeyword, Key: "undo tablespace full"},
			{Type: SignalKeyword, Key: "ora-30036"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricUndoAutoextendOff, Op: OpExists},
			},
		},
		Tree: &TreeNode{
			Step:  "检查UNDO表空间自动扩展配置",
			Query: QueryUndoTSAutoextend,
			Branches: []Branch{
				{
					Match:    MatchBool(false),
					Label:    "UNDO表空间未开启自动扩展",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查UNDO表空间当前使用率",
						Query: QueryUndoSegmentRatio,
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "UNDO使用率>80%且未自动扩展,高风险",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "UNDO表空间使用率>80%且未启用AUTOEXTEND,长事务或大批量DML可能导致ORA-30036错误"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启用UNDO表空间自动扩展", SkillCommand: "/sql @undo_autoextend", RawSQL: "SELECT file_name, tablespace_name, bytes/1024/1024 mb, autoextensible FROM DBA_DATA_FILES WHERE tablespace_name = (SELECT value FROM V$PARAMETER WHERE name = 'undo_tablespace');\n-- 启用自动扩展:\nALTER DATABASE DATAFILE '{file_name}' AUTOEXTEND ON NEXT 512M MAXSIZE 30G;"},
									{Type: ActionInvestigate, Desc: "查看当前UNDO使用情况", SkillCommand: "/sql @undo_usage", RawSQL: "SELECT tablespace_name, status, COUNT(*) extent_cnt, SUM(bytes)/1024/1024 mb FROM DBA_UNDO_EXTENTS GROUP BY tablespace_name, status ORDER BY tablespace_name, status"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "UNDO使用率正常但建议启用自动扩展",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "UNDO表空间未启用AUTOEXTEND,建议开启以防止突发长事务导致ORA-30036"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "启用UNDO数据文件自动扩展", SkillCommand: "/sql @undo_autoextend", RawSQL: "ALTER DATABASE DATAFILE '{undo_file}' AUTOEXTEND ON NEXT 512M MAXSIZE 30G"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "UNDO表空间已启用自动扩展",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "UNDO表空间已启用AUTOEXTEND,配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"undo_contention"},
		Tags:     []string{"tablespace", "undo", "autoextend", "space"},
		Versions: "9i+",
	}
}

// ruleBigfileTSRecommendation detects when bigfile tablespace should be used
// instead of traditional smallfile tablespace for very large databases.
func ruleBigfileTSRecommendation() *Rule {
	return &Rule{
		ID:       "OP2-014",
		Name:     "建议使用大文件表空间(Bigfile TS Recommendation)",
		Category: "tablespace",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "bigfile tablespace"},
			{Type: SignalKeyword, Key: "too many datafiles"},
			{Type: SignalKeyword, Key: "datafile limit"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查数据文件数量与表空间类型",
			Query: QueryBigfileTSInfo,
			Branches: []Branch{
				{
					Match:    MatchGT(500),
					Label:    "数据文件>500个,管理复杂度高",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否使用ASM和是否有大表空间",
						Query: QueryBigfileTSInfo,
						Branches: []Branch{
							{
								Match:    MatchBool(true),
								Label:    "使用ASM,适合迁移到大文件表空间",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "数据库有>500个数据文件且使用ASM存储,建议将大型表空间迁移为Bigfile表空间以减少管理复杂度"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看各表空间数据文件数", SkillCommand: "/sql @ts_datafiles", RawSQL: "SELECT tablespace_name, bigfile, COUNT(*) file_cnt, ROUND(SUM(bytes)/1024/1024/1024,2) total_gb FROM DBA_DATA_FILES GROUP BY tablespace_name, bigfile ORDER BY file_cnt DESC"},
									{Type: ActionFix, Desc: "新建大文件表空间并迁移数据", SkillCommand: "/sql @create_bigfile_ts", RawSQL: "CREATE BIGFILE TABLESPACE {ts_name} DATAFILE '+DATA' SIZE 100G AUTOEXTEND ON NEXT 10G MAXSIZE 8T;\n-- 迁移: ALTER TABLE {table} MOVE TABLESPACE {ts_name} ONLINE;"},
									{Type: ActionPrevent, Desc: "设置默认表空间类型为BIGFILE", SkillCommand: "/sql SET DEFAULT BIGFILE", RawSQL: "ALTER DATABASE SET DEFAULT BIGFILE TABLESPACE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "非ASM环境,评估后决定",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "数据文件数量多,非ASM环境下Bigfile表空间需评估文件系统支持"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "评估文件系统对大文件的支持", SkillCommand: "/sql @fs_check", RawSQL: "SELECT tablespace_name, COUNT(*) file_cnt, ROUND(SUM(bytes)/1024/1024/1024,2) total_gb FROM DBA_DATA_FILES GROUP BY tablespace_name HAVING COUNT(*) > 10 ORDER BY file_cnt DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(100),
					Label:    "数据文件100-500个,可考虑优化",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "数据文件数量适中,可考虑对大型表空间使用Bigfile以简化管理"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "了解Bigfile表空间适用场景", SkillCommand: "/sql @bigfile_info", RawSQL: "SELECT tablespace_name, bigfile, block_size, status FROM DBA_TABLESPACES ORDER BY tablespace_name"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "数据文件数量正常,无需Bigfile",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "数据文件数量不多,当前Smallfile表空间配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"tablespace", "bigfile", "datafile", "management"},
		Versions: "10g+",
	}
}

// ─── Compression Rules (3) ───────────────────────────────────────────────────

func compressionRules() []*Rule {
	return []*Rule{
		ruleBasicCompressionWaste(),
		ruleHCCNotUtilized(),
		ruleOLTPCompressionOverhead(),
	}
}

// ruleBasicCompressionWaste detects basic compression applied on DML-heavy
// tables, which causes excessive compression/decompression overhead and redo.
func ruleBasicCompressionWaste() *Rule {
	return &Rule{
		ID:       "OP2-015",
		Name:     "Basic压缩用于高DML表(Basic Compression on DML Table)",
		Category: "compression",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricCompressionDMLPct},
			{Type: SignalKeyword, Key: "basic compression"},
			{Type: SignalKeyword, Key: "compression overhead"},
			{Type: SignalKeyword, Key: "compress for oltp"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricCompressionDMLPct, Op: OpGT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Basic压缩表的DML活动情况",
			Query: QueryCompressionStats,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现Basic压缩的高DML表",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查这些表的redo生成量",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchGT(1000),
								Label:    "Basic压缩表redo生成>1000MB/天",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Basic压缩(COMPRESS FOR DIRECT_LOAD)用于高DML表导致大量redo生成,因为常规DML会触发解压-修改-重压操作"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "将高DML表从Basic压缩改为OLTP压缩或不压缩", SkillCommand: "/sql @alter_compression", RawSQL: "ALTER TABLE {owner}.{table} MOVE COMPRESS FOR OLTP;\n-- 或取消压缩: ALTER TABLE {owner}.{table} MOVE NOCOMPRESS;\n-- 注意: MOVE后需重建索引"},
									{Type: ActionInvestigate, Desc: "查看Basic压缩表列表及DML统计", SkillCommand: "/sql @compression_tables", RawSQL: "SELECT t.owner, t.table_name, t.compression, t.compress_for, s.inserts, s.updates, s.deletes FROM DBA_TABLES t LEFT JOIN DBA_TAB_MODIFICATIONS s ON t.owner = s.table_owner AND t.table_name = s.table_name WHERE t.compression = 'ENABLED' AND t.compress_for = 'BASIC' AND NVL(s.inserts,0) + NVL(s.updates,0) + NVL(s.deletes,0) > 10000 ORDER BY NVL(s.inserts,0) + NVL(s.updates,0) + NVL(s.deletes,0) DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "redo生成量可接受",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "Basic压缩表存在DML活动,建议监控redo生成趋势"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "持续监控压缩表DML和redo", SkillCommand: "/sql @compression_monitor", RawSQL: "SELECT t.owner, t.table_name, t.compress_for, s.inserts, s.updates, s.deletes FROM DBA_TABLES t JOIN DBA_TAB_MODIFICATIONS s ON t.owner = s.table_owner AND t.table_name = s.table_name WHERE t.compression = 'ENABLED' ORDER BY NVL(s.inserts,0) + NVL(s.updates,0) + NVL(s.deletes,0) DESC FETCH FIRST 20 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未发现Basic压缩的高DML表",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "不存在Basic压缩的高DML表,压缩策略合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"compression", "basic", "dml", "redo"},
		Versions: "11g+",
	}
}

// ruleHCCNotUtilized detects Exadata environments where Hybrid Columnar
// Compression (HCC) is not used on cold/archive data, missing storage savings.
func ruleHCCNotUtilized() *Rule {
	return &Rule{
		ID:       "OP2-016",
		Name:     "Exadata HCC未用于冷数据(HCC Not Utilized)",
		Category: "compression",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "hcc compression"},
			{Type: SignalKeyword, Key: "exadata compression"},
			{Type: SignalKeyword, Key: "cold data compression"},
			{Type: SignalKeyword, Key: "warehouse compress"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查Exadata环境和冷数据HCC使用情况",
			Query: QueryHCCEligible,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "运行在Exadata上,可使用HCC",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "检查大表中未压缩的冷数据",
						Query: QueryHCCEligible,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "存在>100GB未压缩冷数据",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Exadata环境下存在大量未压缩的冷数据(>100GB),启用HCC可节省60-90%存储空间"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "对冷数据表启用HCC QUERY HIGH压缩", SkillCommand: "/sql @hcc_compress", RawSQL: "ALTER TABLE {owner}.{table} MOVE COMPRESS FOR QUERY HIGH;\n-- 对分区表的旧分区: ALTER TABLE {owner}.{table} MOVE PARTITION {part} COMPRESS FOR QUERY HIGH;\n-- 注意: MOVE后需重建索引"},
									{Type: ActionInvestigate, Desc: "识别HCC适用候选表", SkillCommand: "/sql @hcc_candidates", RawSQL: "SELECT owner, table_name, ROUND(blocks*8/1024) mb, compression, compress_for, last_analyzed, NVL(num_rows,0) rows_cnt FROM DBA_TABLES WHERE blocks * 8 / 1024 > 1024 AND (compression = 'DISABLED' OR compress_for IN ('BASIC','OLTP')) ORDER BY blocks DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionPrevent, Desc: "建立ILM策略自动压缩老化数据", SkillCommand: "/sql @ilm_policy", RawSQL: "ALTER TABLE {owner}.{table} ILM ADD POLICY ROW STORE COMPRESS ADVANCED ROW AFTER 90 DAYS OF NO MODIFICATION"},
								},
							},
							{
								Match:    MatchGT(0),
								Label:    "少量冷数据未压缩",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "Exadata环境下存在未压缩冷数据,建议评估HCC压缩收益"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "评估HCC压缩收益", SkillCommand: "/sql @compression_advisor", RawSQL: "-- 使用压缩顾问评估:\nDECLARE l_blkcnt PLS_INTEGER; l_rowcnt PLS_INTEGER; l_cmptype PLS_INTEGER; l_ratio NUMBER;\nBEGIN DBMS_COMPRESSION.GET_COMPRESSION_RATIO('{ts}','{owner}','{table}',NULL,DBMS_COMPRESSION.COMP_FOR_QUERY_HIGH,l_blkcnt,l_rowcnt,l_cmptype,l_ratio); DBMS_OUTPUT.PUT_LINE('Ratio: ' || l_ratio); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "非Exadata环境,HCC不可用",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "非Exadata环境,HCC不可用。可考虑OLTP压缩或Basic压缩"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "了解非Exadata环境可用的压缩选项", SkillCommand: "/sql @compression_options", RawSQL: "SELECT table_name, compression, compress_for FROM DBA_TABLES WHERE compression = 'ENABLED' ORDER BY table_name"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"compression", "hcc", "exadata", "cold_data"},
		Versions: "11.2+",
	}
}

// ruleOLTPCompressionOverhead detects OLTP compression causing excessive CPU
// overhead (>10%) during DML operations.
func ruleOLTPCompressionOverhead() *Rule {
	return &Rule{
		ID:       "OP2-017",
		Name:     "OLTP压缩CPU开销过高(OLTP Compression Overhead)",
		Category: "compression",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricOLTPCompressionCPUPct},
			{Type: SignalKeyword, Key: "oltp compression cpu"},
			{Type: SignalKeyword, Key: "compression overhead"},
			{Type: SignalKeyword, Key: "advanced compression"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricOLTPCompressionCPUPct, Op: OpGT, Value: 10},
			},
		},
		Tree: &TreeNode{
			Step:  "检查OLTP压缩表的CPU开销",
			Query: QueryOLTPCompressionCPU,
			Branches: []Branch{
				{
					Match:    MatchGT(20),
					Label:    "OLTP压缩CPU开销>20%,严重影响性能",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "识别CPU开销最高的压缩表",
						Query: QueryCompressionStats,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "定位到高开销压缩表",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "OLTP压缩导致CPU开销>20%,热点DML表的压缩/解压消耗大量CPU资源"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "对CPU开销最高的表取消压缩", SkillCommand: "/sql @disable_compress", RawSQL: "ALTER TABLE {owner}.{table} MOVE NOCOMPRESS;\n-- 如果表过大,使用ONLINE MOVE(12c+):\nALTER TABLE {owner}.{table} MOVE NOCOMPRESS ONLINE;"},
									{Type: ActionInvestigate, Desc: "查看压缩表CPU消耗排名", SkillCommand: "/sql @compress_cpu", RawSQL: "SELECT t.owner, t.table_name, t.compress_for, s.physical_reads, s.physical_writes, s.db_block_changes FROM DBA_TABLES t JOIN (SELECT owner, object_name, SUM(CASE WHEN statistic_name = 'physical reads' THEN value END) physical_reads, SUM(CASE WHEN statistic_name = 'physical writes' THEN value END) physical_writes, SUM(CASE WHEN statistic_name = 'db block changes' THEN value END) db_block_changes FROM V$SEGMENT_STATISTICS GROUP BY owner, object_name) s ON t.owner = s.owner AND t.table_name = s.object_name WHERE t.compression = 'ENABLED' AND t.compress_for = 'ADVANCED' ORDER BY s.db_block_changes DESC NULLS LAST FETCH FIRST 20 ROWS ONLY"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "未能定位具体表,需进一步排查",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "OLTP压缩CPU开销高但无法定位具体表,建议使用ASH分析"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "使用ASH分析压缩相关CPU等待", SkillCommand: "/sql @ash_compress", RawSQL: "SELECT sql_id, COUNT(*) sample_cnt, ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(),1) pct FROM V$ACTIVE_SESSION_HISTORY WHERE sample_time > SYSDATE - 1/24 AND event IS NULL AND session_state = 'ON CPU' GROUP BY sql_id ORDER BY sample_cnt DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(10),
					Label:    "OLTP压缩CPU开销10-20%,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "OLTP压缩CPU开销>10%,建议评估压缩收益与CPU成本的平衡"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "对比压缩空间节省与CPU成本", SkillCommand: "/sql @compress_benefit", RawSQL: "SELECT t.owner, t.table_name, t.compress_for, ROUND(t.blocks*8/1024) mb, m.inserts, m.updates, m.deletes FROM DBA_TABLES t LEFT JOIN DBA_TAB_MODIFICATIONS m ON t.owner = m.table_owner AND t.table_name = m.table_name WHERE t.compression = 'ENABLED' AND t.compress_for = 'ADVANCED' ORDER BY NVL(m.inserts,0)+NVL(m.updates,0)+NVL(m.deletes,0) DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "OLTP压缩CPU开销正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "OLTP压缩CPU开销在可接受范围(<10%)"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"compression", "oltp", "cpu", "advanced"},
		Versions: "11g+",
	}
}

// ─── In-Memory Rules (3) ─────────────────────────────────────────────────────

func inMemoryRules() []*Rule {
	return []*Rule{
		ruleInMemoryPopulateStalled(),
		ruleInMemoryPriorityMismatch(),
		ruleInMemorySizeInsufficient(),
	}
}

// ruleInMemoryPopulateStalled detects In-Memory column store population that
// is stuck or progressing very slowly after restart or object enable.
func ruleInMemoryPopulateStalled() *Rule {
	return &Rule{
		ID:       "OP2-018",
		Name:     "In-Memory加载停滞(IM Populate Stalled)",
		Category: "in_memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIMPopulateStallCount},
			{Type: SignalKeyword, Key: "inmemory populate"},
			{Type: SignalKeyword, Key: "im population stalled"},
			{Type: SignalKeyword, Key: "inmemory slow"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIMPopulateStallCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查In-Memory对象加载状态",
			Query: QueryInMemoryPopulate,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在未完成IM加载的对象",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查IM加载进度与等待原因",
						Query: QueryInMemoryPopulate,
						Branches: []Branch{
							{
								Match:    MatchLT(10),
								Label:    "加载进度<10%,可能停滞",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "In-Memory对象加载进度极低(<10%),可能因IMCO后台进程阻塞或inmemory_size不足"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查IMCO进程和IM加载状态", SkillCommand: "/sql @im_populate_status", RawSQL: "SELECT owner, segment_name, partition_name, populate_status, bytes, inmemory_size, bytes_not_populated, ROUND(inmemory_size/NULLIF(bytes,0)*100,1) pct_populated FROM V$IM_SEGMENTS ORDER BY bytes_not_populated DESC"},
									{Type: ActionFix, Desc: "手动触发IM加载", SkillCommand: "/sql @im_force_populate", RawSQL: "-- 手动触发加载:\nBEGIN DBMS_INMEMORY.POPULATE(schema_name => '{owner}', table_name => '{table}'); END;\n-- 或使用优先级: ALTER TABLE {owner}.{table} INMEMORY PRIORITY CRITICAL;"},
									{Type: ActionInvestigate, Desc: "检查inmemory_size是否足够", SkillCommand: "/params inmemory_size", RawSQL: "SELECT pool, alloc_bytes/1024/1024 alloc_mb, used_bytes/1024/1024 used_mb, populate_status FROM V$INMEMORY_AREA"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "加载进行中,速度较慢",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "In-Memory加载进行中但速度较慢,可能因I/O负载高或对象过大"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看IM加载进度", SkillCommand: "/sql @im_progress", RawSQL: "SELECT owner, segment_name, populate_status, ROUND(inmemory_size/NULLIF(bytes,0)*100,1) pct_populated FROM V$IM_SEGMENTS WHERE populate_status != 'COMPLETED' ORDER BY bytes DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "所有In-Memory对象加载完成",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有配置的In-Memory对象已完成加载"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"OP2-020"},
		CausesOf: []string{},
		Tags:     []string{"inmemory", "populate", "performance"},
		Versions: "12.1.0.2+",
	}
}

// ruleInMemoryPriorityMismatch detects hot/frequently-accessed tables configured
// with low In-Memory priority while cold tables have high priority.
func ruleInMemoryPriorityMismatch() *Rule {
	return &Rule{
		ID:       "OP2-019",
		Name:     "In-Memory优先级不匹配(IM Priority Mismatch)",
		Category: "in_memory",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "inmemory priority"},
			{Type: SignalKeyword, Key: "im priority mismatch"},
			{Type: SignalKeyword, Key: "inmemory hot table"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查In-Memory对象优先级与访问频率",
			Query: QueryInMemoryObjects,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "存在配置了In-Memory的对象",
					Severity: SeverityLow,
					Then: &TreeNode{
						Step:  "对比对象访问频率与IM优先级设置",
						Query: QueryInMemoryObjects,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "发现优先级与访问频率不匹配",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "热点高频访问表设置了低IM优先级(NONE/LOW),而低频表设置了高优先级,导致热点表在重启后延迟加载"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看IM对象及其访问统计", SkillCommand: "/sql @im_priority_check", RawSQL: "SELECT o.owner, o.object_name, i.inmemory_priority, i.inmemory_compression, ROUND(i.bytes/1024/1024) orig_mb, ROUND(i.inmemory_size/1024/1024) im_mb, s.physical_reads, s.logical_reads FROM V$IM_SEGMENTS i JOIN DBA_OBJECTS o ON i.objd = o.data_object_id LEFT JOIN (SELECT obj#, SUM(CASE WHEN statistic_name='physical reads' THEN value END) physical_reads, SUM(CASE WHEN statistic_name='logical reads' THEN value END) logical_reads FROM V$SEGMENT_STATISTICS GROUP BY obj#) s ON o.object_id = s.obj# ORDER BY s.logical_reads DESC NULLS LAST"},
									{Type: ActionFix, Desc: "将热点表优先级提升为CRITICAL/HIGH", SkillCommand: "/sql @im_alter_priority", RawSQL: "ALTER TABLE {owner}.{hot_table} INMEMORY PRIORITY CRITICAL;\nALTER TABLE {owner}.{cold_table} INMEMORY PRIORITY LOW;"},
									{Type: ActionPrevent, Desc: "建立IM优先级定期审查机制", SkillCommand: "/sql @im_review", RawSQL: "-- 定期执行以下查询审查IM优先级是否合理:\nSELECT segment_name, inmemory_priority, populate_status, ROUND(bytes/1024/1024) mb FROM V$IM_SEGMENTS ORDER BY inmemory_priority, bytes DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "优先级配置合理",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "In-Memory对象优先级配置与访问频率基本匹配"}},
								Actions:  nil,
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未配置In-Memory对象",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未配置In-Memory对象,如需启用建议先评估热点表"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "评估哪些表适合In-Memory", SkillCommand: "/sql @im_candidates", RawSQL: "SELECT owner, object_name, statistic_name, value FROM V$SEGMENT_STATISTICS WHERE statistic_name IN ('physical reads','logical reads') AND value > 100000 ORDER BY value DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-018"},
		Tags:     []string{"inmemory", "priority", "hot_table"},
		Versions: "12.1.0.2+",
	}
}

// ruleInMemorySizeInsufficient detects inmemory_size too small to hold the
// designated working set, causing IM eviction and populate churn.
func ruleInMemorySizeInsufficient() *Rule {
	return &Rule{
		ID:       "OP2-020",
		Name:     "In-Memory区域不足(IM Size Insufficient)",
		Category: "in_memory",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricIMSizePct},
			{Type: SignalKeyword, Key: "inmemory_size"},
			{Type: SignalKeyword, Key: "im area full"},
			{Type: SignalKeyword, Key: "inmemory eviction"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricIMSizePct, Op: OpGT, Value: 90},
			},
		},
		Tree: &TreeNode{
			Step:  "检查In-Memory区域使用率",
			Query: QueryInMemoryAreaSize,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "In-Memory区域使用>95%,即将耗尽",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查IM驱逐和未能加载的对象",
						Query: QueryInMemoryPopulate,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在因空间不足未加载的对象",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "In-Memory区域使用>95%,部分配置的对象因空间不足无法加载,导致查询回退到缓冲区缓存"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "增大inmemory_size参数", SkillCommand: "/params inmemory_size", RawSQL: "ALTER SYSTEM SET inmemory_size = {new_size} SCOPE=BOTH;\n-- 注意: 12.1需要重启, 12.2+支持在线增大"},
									{Type: ActionInvestigate, Desc: "查看IM区域使用详情", SkillCommand: "/sql @im_area", RawSQL: "SELECT pool, alloc_bytes/1024/1024 alloc_mb, used_bytes/1024/1024 used_mb, populate_status, ROUND(used_bytes/NULLIF(alloc_bytes,0)*100,1) pct_used FROM V$INMEMORY_AREA"},
									{Type: ActionFix, Desc: "移除低优先级对象释放IM空间", SkillCommand: "/sql @im_remove", RawSQL: "-- 移除低频对象:\nALTER TABLE {owner}.{cold_table} NO INMEMORY;\n-- 查看当前IM对象:\nSELECT owner, segment_name, ROUND(inmemory_size/1024/1024) im_mb, inmemory_priority, populate_status FROM V$IM_SEGMENTS ORDER BY inmemory_size DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "对象均已加载但空间紧张",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "In-Memory区域接近满载,新增对象将无法加载"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "计算所需IM空间并扩容", SkillCommand: "/sql @im_sizing", RawSQL: "SELECT 'Current IM Used' metric, ROUND(SUM(inmemory_size)/1024/1024) mb FROM V$IM_SEGMENTS UNION ALL SELECT 'Configured', value/1024/1024 FROM V$PARAMETER WHERE name = 'inmemory_size' UNION ALL SELECT 'Candidate Tables', ROUND(SUM(bytes)/1024/1024) FROM DBA_TABLES WHERE inmemory = 'ENABLED' AND table_name NOT IN (SELECT segment_name FROM V$IM_SEGMENTS)"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "In-Memory区域使用80-95%",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "In-Memory区域使用>80%,建议规划扩容以容纳未来增长"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "评估IM空间增长趋势", SkillCommand: "/sql @im_usage_trend", RawSQL: "SELECT pool, ROUND(alloc_bytes/1024/1024) alloc_mb, ROUND(used_bytes/1024/1024) used_mb, ROUND(used_bytes/NULLIF(alloc_bytes,0)*100,1) pct_used FROM V$INMEMORY_AREA"},
						{Type: ActionPrevent, Desc: "设置IM空间使用告警", SkillCommand: "/alert", RawSQL: "-- 监控查询:\nSELECT ROUND(SUM(used_bytes)/NULLIF(SUM(alloc_bytes),0)*100,1) im_pct_used FROM V$INMEMORY_AREA"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "In-Memory区域空间充足",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "In-Memory区域空间使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"OP2-018"},
		Tags:     []string{"inmemory", "sizing", "memory"},
		Versions: "12.1.0.2+",
	}
}
