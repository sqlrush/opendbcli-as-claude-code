/*-------------------------------------------------------------------------
 *
 * rules_operations.go
 *	  Oracle rule engine — operations rules (backup overdue, statistics stale, undo retention).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_operations.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// ─── Query IDs for operations rules ─────────────────────────────────────────

const (
	QueryPDBResourceUsage     QueryID = "pdb_resource_usage"
	QueryPDBStorageUsage      QueryID = "pdb_storage_usage"
	QueryCDBCommonUsers       QueryID = "cdb_common_users"
	QueryPDBLockdownProfile   QueryID = "pdb_lockdown_profile"
	QueryFailedLogins         QueryID = "failed_logins"
	QueryDBAPrivilegeGrants   QueryID = "dba_privilege_grants"
	QueryUnifiedAuditStatus   QueryID = "unified_audit_status"
	QueryIntervalPartitions   QueryID = "interval_partitions"
	QuerySubpartitionBalance  QueryID = "subpartition_balance"
	QueryRMANBackupStatus     QueryID = "rman_backup_status"
	QueryRMANFailures         QueryID = "rman_failures"
	QueryFRAUsage             QueryID = "fra_usage"
	QueryArchiveLogBackup     QueryID = "archive_log_backup"
	QueryInvalidObjects       QueryID = "invalid_objects"
	QueryDeprecatedParams     QueryID = "deprecated_params"
	QueryCompatibleParam      QueryID = "compatible_param"
	QueryOptimizerParams      QueryID = "optimizer_params"
	QueryUnderscoredParams    QueryID = "underscored_params"
	QuerySGAPGAMemory         QueryID = "sga_pga_memory"
	QueryProcessesSessions    QueryID = "processes_sessions"
	QueryAWRRetention         QueryID = "awr_retention"
	QueryAWRSnapshots         QueryID = "awr_snapshots"
	QueryADDMFindings         QueryID = "addm_findings"
	QueryAWRBaselines         QueryID = "awr_baselines"
)

// ─── Metric constants for operations rules ──────────────────────────────────

const (
	metricPDBCPUUsedPct       = "pdb_cpu_used_pct"
	metricPDBStorageUsedPct   = "pdb_storage_used_pct"
	metricFailedLoginCount    = "failed_login_count"
	metricRMANBackupAge       = "rman_backup_age_hours"
	metricRMANFailureCount    = "rman_failure_count"
	metricInvalidObjectCount  = "invalid_object_count"
	metricAWRRetentionDays    = "awr_retention_days"
	metricADDMHighFindings    = "addm_high_findings"
)

// ─── CDB/PDB Rules (4) ─────────────────────────────────────────────────────

func cdbPdbRules() []*Rule {
	return []*Rule{
		rulePDBResourceLimit(),
		rulePDBStorageQuota(),
		ruleCDBCommonUserIssue(),
		rulePDBViolation(),
	}
}

// rulePDBResourceLimit detects PDB hitting CPU/memory/IO resource limits
// configured via resource manager plans.
func rulePDBResourceLimit() *Rule {
	return &Rule{
		ID:       "pdb_resource_limit",
		Name:     "PDB资源限制命中(PDB Resource Limit)",
		Category: "cdb_pdb",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPDBCPUUsedPct},
			{Type: SignalKeyword, Key: "pdb resource"},
			{Type: SignalKeyword, Key: "resource limit"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPDBCPUUsedPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查PDB资源使用与限制",
			Query: QueryPDBResourceUsage,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "PDB资源使用>95%限制,严重受限",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查资源管理器计划配置",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "Resource Manager限制过严格",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "PDB资源使用已达到Resource Manager配置的CPU/IO上限,业务受影响"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并调整PDB资源计划份额", SkillCommand: "/sql @pdb_resource_plan", RawSQL: "SELECT plan, pluggable_database, shares, utilization_limit, parallel_server_limit FROM DBA_CDB_RSRC_PLAN_DIRECTIVES WHERE plan = (SELECT name FROM V$RSRC_PLAN WHERE is_top_plan = 'TRUE')"},
									{Type: ActionFix, Desc: "提高PDB CPU/IO utilization_limit", SkillCommand: "/sql @alter_pdb_directive", RawSQL: "BEGIN DBMS_RESOURCE_MANAGER.UPDATE_CDB_PLAN_DIRECTIVE(plan => '{plan}', pluggable_database => '{pdb}', new_utilization_limit => 90, new_shares => 3); END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "PDB本身负载过高",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "PDB资源消耗接近上限,需优化PDB内工作负载或提升限额"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查PDB内Top SQL消耗", SkillCommand: "/slowsql", RawSQL: "SELECT sql_id, cpu_time/1e6 cpu_sec, elapsed_time/1e6 ela_sec, executions FROM V$SQL WHERE con_id = (SELECT con_id FROM V$PDBS WHERE name = '{pdb}') ORDER BY cpu_time DESC FETCH FIRST 10 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "PDB资源使用80-95%,接近限制",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "PDB资源使用接近配置上限,建议监控趋势"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看PDB资源使用趋势", SkillCommand: "/sql @pdb_resource_history", RawSQL: "SELECT con_id, pdb_name, cpu_consumed_time, cpu_wait_time, io_service_time FROM V$RSRC_CONSUMER_GROUP WHERE con_id > 2 ORDER BY cpu_consumed_time DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "PDB资源使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "PDB资源使用在配置限制范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"cdb", "pdb", "resource_manager"},
		Versions: "12c+",
	}
}

// rulePDBStorageQuota detects PDB storage approaching its configured quota limit.
func rulePDBStorageQuota() *Rule {
	return &Rule{
		ID:       "pdb_storage_quota",
		Name:     "PDB存储接近配额(PDB Storage Quota)",
		Category: "cdb_pdb",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricPDBStorageUsedPct},
			{Type: SignalErrorCode, Key: "ORA-65114"},
			{Type: SignalKeyword, Key: "pdb storage"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricPDBStorageUsedPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查PDB存储使用与配额",
			Query: QueryPDBStorageUsage,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "PDB存储>95%配额,即将用尽",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查PDB内大段对象",
						Query: QuerySegmentStats,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在大段对象可清理",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "PDB存储使用已超过95%配额,存在大段对象占用空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查PDB内大段对象并清理", SkillCommand: "/sql @pdb_large_segments", RawSQL: "SELECT segment_name, segment_type, ROUND(bytes/1024/1024) mb FROM DBA_SEGMENTS ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "提高PDB存储配额", SkillCommand: "/sql ALTER PLUGGABLE DATABASE {pdb} STORAGE", RawSQL: "ALTER PLUGGABLE DATABASE {pdb} STORAGE (MAXSIZE {new_size})"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "需提高配额",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "PDB存储接近配额上限,需扩大配额或迁移数据"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "扩大PDB存储配额", SkillCommand: "/sql ALTER PLUGGABLE DATABASE STORAGE", RawSQL: "ALTER PLUGGABLE DATABASE {pdb} STORAGE (MAXSIZE UNLIMITED)"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "PDB存储80-95%配额,预警",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "PDB存储使用超过80%配额,建议提前规划扩容"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "规划PDB配额扩容", SkillCommand: "/sql @pdb_storage_info", RawSQL: "SELECT con_id, pdb_name, total_size/1024/1024 total_mb, max_size/1024/1024 quota_mb FROM CDB_PDBS p JOIN CDB_PDB_STORAGE s ON p.con_id = s.con_id"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "PDB存储使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "PDB存储使用在配额范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"tablespace_full"},
		Tags:     []string{"cdb", "pdb", "storage", "quota"},
		Versions: "12c+",
	}
}

// ruleCDBCommonUserIssue detects privilege conflicts between common users
// and local PDB users in multitenant environments.
func ruleCDBCommonUserIssue() *Rule {
	return &Rule{
		ID:       "cdb_common_user_issue",
		Name:     "Common User权限冲突(CDB Common User Issue)",
		Category: "cdb_pdb",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-65094"},
			{Type: SignalKeyword, Key: "common user"},
			{Type: SignalKeyword, Key: "c## user"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查CDB common user权限配置",
			Query: QueryCDBCommonUsers,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现common user权限冲突",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查common user在各PDB中的授权差异",
						Query: QueryDBAPrivilegeGrants,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "common user权限在PDB间不一致",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "Common User在不同PDB中权限不一致,可能导致安全隐患或功能异常"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "审计common user在各PDB中的权限", SkillCommand: "/sql @common_user_privs", RawSQL: "SELECT c.username, p.con_id, p.granted_role FROM CDB_USERS c JOIN CDB_ROLE_PRIVS p ON c.username = p.grantee WHERE c.common = 'YES' AND c.oracle_maintained = 'N' ORDER BY c.username, p.con_id"},
									{Type: ActionFix, Desc: "统一common user权限(使用CONTAINER=ALL)", SkillCommand: "/sql GRANT {role} TO {user} CONTAINER=ALL", RawSQL: "GRANT {role} TO {user} CONTAINER=ALL"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "权限配置正常",
								Severity: SeverityLow,
								Findings: []Finding{{Desc: "Common user权限配置在各PDB中一致"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "定期审计common user权限", SkillCommand: "/sql @common_user_audit", RawSQL: "SELECT username, common, oracle_maintained FROM CDB_USERS WHERE common = 'YES' ORDER BY username"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未发现common user问题",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "CDB common user配置正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"cdb", "pdb", "common_user", "security"},
		Versions: "12c+",
	}
}

// rulePDBViolation detects lockdown profile violations in PDB environments,
// where PDB operations are blocked by CDB-level lockdown profiles.
func rulePDBViolation() *Rule {
	return &Rule{
		ID:       "pdb_lockdown_violation",
		Name:     "PDB Lockdown Profile违规(PDB Violation)",
		Category: "cdb_pdb",
		Signals: []Signal{
			{Type: SignalErrorCode, Key: "ORA-01031"},
			{Type: SignalErrorCode, Key: "ORA-01027"},
			{Type: SignalKeyword, Key: "lockdown profile"},
			{Type: SignalKeyword, Key: "pdb violation"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查PDB lockdown profile配置",
			Query: QueryPDBLockdownProfile,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "PDB设置了lockdown profile",
					Severity: SeverityMedium,
					Then: &TreeNode{
						Step:  "检查lockdown profile限制的操作",
						Query: QueryPDBLockdownProfile,
						Branches: []Branch{
							{
								Match:    MatchGT(5),
								Label:    "大量操作被限制",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "PDB lockdown profile限制了大量操作,可能影响正常DBA运维"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看lockdown profile限制详情", SkillCommand: "/sql @lockdown_rules", RawSQL: "SELECT profile_name, rule_type, rule, clause, clause_option, status FROM DBA_LOCKDOWN_PROFILES WHERE profile_name = (SELECT lockdown_profile FROM V$PDBS WHERE con_id = SYS_CONTEXT('USERENV','CON_ID')) ORDER BY rule_type, rule"},
									{Type: ActionFix, Desc: "在CDB root中调整lockdown profile", SkillCommand: "/sql @alter_lockdown", RawSQL: "-- 在CDB$ROOT中执行:\nALTER LOCKDOWN PROFILE {profile_name} DISABLE STATEMENT = ('{statement}')"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "少量操作被限制",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "PDB存在lockdown profile,部分操作受限"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "确认受限操作是否影响业务", SkillCommand: "/sql @lockdown_rules", RawSQL: "SELECT rule_type, rule, clause, status FROM DBA_LOCKDOWN_PROFILES WHERE profile_name = (SELECT lockdown_profile FROM V$PDBS WHERE con_id = SYS_CONTEXT('USERENV','CON_ID'))"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未配置lockdown profile",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "PDB未配置lockdown profile,无操作限制"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "生产环境建议配置lockdown profile加固PDB安全", SkillCommand: "/sql @create_lockdown", RawSQL: "-- 在CDB$ROOT中执行:\nCREATE LOCKDOWN PROFILE {profile_name};\nALTER LOCKDOWN PROFILE {profile_name} DISABLE STATEMENT = ('ALTER SYSTEM') CLAUSE = ('SET') OPTION ALL EXCEPT = ('nls_language','nls_territory');\nALTER PLUGGABLE DATABASE {pdb} LOCKDOWN PROFILE = '{profile_name}'"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"cdb", "pdb", "lockdown", "security"},
		Versions: "12.2+",
	}
}

// ─── Security Rules (3) ─────────────────────────────────────────────────────

func securityRules() []*Rule {
	return []*Rule{
		ruleFailedLoginAttempts(),
		rulePrivilegeEscalation(),
		ruleAuditNotEnabled(),
	}
}

// ruleFailedLoginAttempts detects failed login attempts exceeding 50/hour,
// indicating possible brute force attack.
func ruleFailedLoginAttempts() *Rule {
	return &Rule{
		ID:       "failed_login_attempts",
		Name:     "异常登录失败(Failed Login Brute Force)",
		Category: "security",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFailedLoginCount},
			{Type: SignalErrorCode, Key: "ORA-01017"},
			{Type: SignalKeyword, Key: "failed login"},
			{Type: SignalKeyword, Key: "brute force"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFailedLoginCount, Op: OpGT, Value: 50},
			},
		},
		Tree: &TreeNode{
			Step:  "检查最近1小时内登录失败次数与来源",
			Query: QueryFailedLogins,
			Branches: []Branch{
				{
					Match:    MatchGT(200),
					Label:    "登录失败>200/小时,疑似暴力破解",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查失败登录来源IP分布",
						Query: QueryFailedLogins,
						Branches: []Branch{
							{
								Match:    MatchGT(100),
								Label:    "单一来源大量失败,确认暴力攻击",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "检测到来自单一来源的大量登录失败尝试(>100/小时),疑似暴力破解攻击"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即封锁攻击来源IP", SkillCommand: "/sql @block_ip", RawSQL: "-- 通过sqlnet.ora VALID_NODE_CHECKING或防火墙封锁:\n-- TCP.VALIDNODE_CHECKING = YES\n-- TCP.EXCLUDED_NODES = ({attacker_ip})"},
									{Type: ActionFix, Desc: "设置账户锁定策略", SkillCommand: "/sql @login_profile", RawSQL: "ALTER PROFILE DEFAULT LIMIT FAILED_LOGIN_ATTEMPTS 5 PASSWORD_LOCK_TIME 1/24"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "多来源分散失败",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "多个来源频繁登录失败,可能为分布式攻击或应用配置错误"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查失败登录的用户名和来源", SkillCommand: "/sql @failed_login_detail", RawSQL: "SELECT username, os_username, userhost, terminal, TO_CHAR(timestamp,'YYYY-MM-DD HH24:MI:SS') ts FROM DBA_AUDIT_TRAIL WHERE action_name = 'LOGON' AND returncode != 0 AND timestamp > SYSDATE - 1/24 ORDER BY timestamp DESC"},
									{Type: ActionFix, Desc: "启用登录失败告警", SkillCommand: "/sql @audit_failed_login", RawSQL: "AUDIT SESSION WHENEVER NOT SUCCESSFUL"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(50),
					Label:    "登录失败50-200/小时,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "登录失败次数偏高(>50/小时),需排查是否为应用配置错误或恶意行为"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看失败登录详情", SkillCommand: "/sql @failed_login_summary", RawSQL: "SELECT username, userhost, COUNT(*) cnt FROM DBA_AUDIT_TRAIL WHERE action_name = 'LOGON' AND returncode != 0 AND timestamp > SYSDATE - 1/24 GROUP BY username, userhost ORDER BY cnt DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "登录失败次数正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "登录失败次数在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "login", "audit"},
		Versions: "9i+",
	}
}

// rulePrivilegeEscalation detects DBA role granted to non-admin users,
// a common security anti-pattern.
func rulePrivilegeEscalation() *Rule {
	return &Rule{
		ID:       "privilege_escalation",
		Name:     "权限提升风险(DBA Role to Non-Admin)",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "privilege escalation"},
			{Type: SignalKeyword, Key: "DBA role"},
			{Type: SignalKeyword, Key: "security audit"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查DBA角色授予情况",
			Query: QueryDBAPrivilegeGrants,
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "DBA角色授予>3个非系统用户",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查WITH ADMIN OPTION传播链",
						Query: QueryDBAPrivilegeGrants,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在WITH ADMIN OPTION级联授权",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "多个非系统用户持有DBA角色,且存在WITH ADMIN OPTION级联风险"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即审计DBA角色持有者", SkillCommand: "/sql @dba_grantees", RawSQL: "SELECT grantee, granted_role, admin_option, default_role FROM DBA_ROLE_PRIVS WHERE granted_role IN ('DBA','SYSDBA') AND grantee NOT IN ('SYS','SYSTEM') ORDER BY grantee"},
									{Type: ActionFix, Desc: "回收非必要的DBA角色授权", SkillCommand: "/sql REVOKE DBA FROM {user}", RawSQL: "REVOKE DBA FROM {user}"},
									{Type: ActionPrevent, Desc: "创建最小权限自定义角色替代DBA", SkillCommand: "/sql @create_custom_role", RawSQL: "CREATE ROLE app_admin_role;\nGRANT CREATE SESSION, SELECT ANY TABLE TO app_admin_role;\n-- 按实际需求授予最小权限"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无级联授权但DBA角色过度使用",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "多个非系统用户持有DBA角色,违反最小权限原则"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "回收DBA角色,授予最小权限", SkillCommand: "/sql @dba_grantees", RawSQL: "SELECT grantee, granted_role FROM DBA_ROLE_PRIVS WHERE granted_role = 'DBA' AND grantee NOT IN ('SYS','SYSTEM')"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "DBA角色授予1-3个用户,需确认",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "少量非系统用户持有DBA角色,建议确认是否为合规授权"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认DBA角色授权合理性", SkillCommand: "/sql @dba_grantees", RawSQL: "SELECT grantee, granted_role, admin_option FROM DBA_ROLE_PRIVS WHERE granted_role = 'DBA' AND grantee NOT IN ('SYS','SYSTEM')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "DBA角色授权正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "仅系统用户持有DBA角色,权限配置合理"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "privilege", "audit"},
		Versions: "9i+",
	}
}

// ruleAuditNotEnabled detects unified audit not configured on 12c+,
// which is required for compliance and security monitoring.
func ruleAuditNotEnabled() *Rule {
	return &Rule{
		ID:       "audit_not_enabled",
		Name:     "统一审计未启用(Unified Audit Not Enabled)",
		Category: "security",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "audit"},
			{Type: SignalKeyword, Key: "unified audit"},
			{Type: SignalKeyword, Key: "compliance"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查统一审计配置状态",
			Query: QueryUnifiedAuditStatus,
			Branches: []Branch{
				{
					Match:    MatchEquals("FALSE"),
					Label:    "统一审计未启用",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查传统审计是否有配置",
						Query: QueryUnifiedAuditStatus,
						Branches: []Branch{
							{
								Match:    MatchEquals("NONE"),
								Label:    "任何审计均未配置",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "数据库12c+未启用统一审计,且无传统审计配置,存在严重合规和安全风险"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "启用统一审计(需重启)", SkillCommand: "/sql @enable_unified_audit", RawSQL: "-- 1. 关闭数据库\n-- 2. 重编译: $ORACLE_HOME/rdbms/lib> make -f ins_rdbms.mk uniaud_on ioracle\n-- 3. 启动数据库\n-- 4. 验证: SELECT VALUE FROM V$OPTION WHERE PARAMETER = 'Unified Auditing'"},
									{Type: ActionFix, Desc: "配置基本审计策略", SkillCommand: "/sql @create_audit_policy", RawSQL: "CREATE AUDIT POLICY ora_logon_failures ACTIONS LOGON WHEN 'SYS_CONTEXT(''USERENV'',''AUTHENTICATION_TYPE'') != ''DATABASE''' EVALUATE PER SESSION;\nAUDIT POLICY ora_logon_failures"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用传统审计,建议迁移",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "使用传统审计(非统一审计),建议迁移至12c+统一审计以获得更完整的审计功能"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "规划迁移到统一审计", SkillCommand: "/sql @audit_migration", RawSQL: "SELECT audit_option, success, failure FROM DBA_STMT_AUDIT_OPTS UNION ALL SELECT privilege, success, failure FROM DBA_PRIV_AUDIT_OPTS"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "统一审计已启用",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "统一审计已启用,配置正常"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期审查审计策略完整性", SkillCommand: "/sql @audit_policies", RawSQL: "SELECT policy_name, enabled_opt, entity_name, entity_type, success, failure FROM AUDIT_UNIFIED_ENABLED_POLICIES ORDER BY policy_name"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"security", "audit", "compliance"},
		Versions: "12c+",
	}
}

// ─── Partition Rules (3) ────────────────────────────────────────────────────

func partitionRules() []*Rule {
	return []*Rule{
		rulePartitionPruneFailure(),
		ruleIntervalPartitionOverflow(),
		ruleSubpartitionImbalance(),
	}
}

// rulePartitionPruneFailure detects queries not pruning partitions due to
// functions applied on partition key columns.
func rulePartitionPruneFailure() *Rule {
	return &Rule{
		ID:       "partition_prune_failure",
		Name:     "分区裁剪失效(Partition Prune Failure)",
		Category: "partition",
		Signals: []Signal{
			{Type: SignalWaitEvent, Key: "db file scattered read"},
			{Type: SignalKeyword, Key: "partition prune"},
			{Type: SignalKeyword, Key: "partition scan"},
		},
		Trigger: Trigger{
			Mode: TriggerQuery,
			Conditions: []Condition{
				{Source: "top_sqls", Field: "elapsed_drift", Op: OpGT, Value: 3.0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查Top SQL的分区裁剪情况",
			Query: QueryPartitionPruning,
			Branches: []Branch{
				{
					Match:    MatchGT(0),
					Label:    "发现分区裁剪失效的SQL",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查失效原因(谓词上是否有函数)",
						Query: QueryPartitionPruning,
						Branches: []Branch{
							{
								Match:    MatchEquals("FUNCTION_ON_KEY"),
								Label:    "分区键上使用了函数",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SQL在分区键列上使用了函数(如TO_CHAR, TRUNC),导致分区裁剪完全失效,扫描所有分区"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "改写SQL避免在分区键上使用函数", SkillCommand: "/explain {sql_id}", RawSQL: "-- 错误: WHERE TO_CHAR(created_date,'YYYYMM') = '202301'\n-- 正确: WHERE created_date >= DATE '2023-01-01' AND created_date < DATE '2023-02-01'"},
									{Type: ActionInvestigate, Desc: "查看执行计划中的分区访问步骤", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR('{sql_id}', NULL, 'ALLSTATS LAST PARTITION'))"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致裁剪失效",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "分区裁剪失效,可能因隐式类型转换或绑定变量类型不匹配"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查SQL谓词与分区键类型", SkillCommand: "/explain {sql_id}", RawSQL: "SELECT partition_name, partition_position, high_value FROM DBA_TAB_PARTITIONS WHERE table_name = '{table}' AND table_owner = '{owner}' ORDER BY partition_position"},
								},
							},
						},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "分区裁剪正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Top SQL的分区裁剪工作正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"full_table_scan_oltp"},
		Tags:     []string{"partition", "prune", "sql"},
		Versions: "9i+",
	}
}

// ruleIntervalPartitionOverflow detects too many auto-created interval
// partitions, which can cause performance and management issues.
func ruleIntervalPartitionOverflow() *Rule {
	return &Rule{
		ID:       "interval_partition_overflow",
		Name:     "Interval分区过多(Interval Partition Overflow)",
		Category: "partition",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "interval partition"},
			{Type: SignalKeyword, Key: "too many partitions"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查Interval分区表的分区数量",
			Query: QueryIntervalPartitions,
			Branches: []Branch{
				{
					Match:    MatchGT(10000),
					Label:    "分区数>10000,严重过多",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否有异常数据导致分区爆炸",
						Query: QueryIntervalPartitions,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在异常high_value(如未来日期)",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "Interval分区表自动创建了超过10000个分区,可能因异常数据(如未来日期)导致分区爆炸"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查并清除异常远期分区", SkillCommand: "/sql @interval_part_cleanup", RawSQL: "SELECT table_name, partition_name, high_value, num_rows FROM DBA_TAB_PARTITIONS WHERE table_owner = '{owner}' AND table_name = '{table}' ORDER BY partition_position DESC FETCH FIRST 20 ROWS ONLY"},
									{Type: ActionFix, Desc: "设置分区上界,转为Range分区+Maxvalue", SkillCommand: "/sql @set_interval_off", RawSQL: "ALTER TABLE {owner}.{table} SET INTERVAL ()"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "正常数据增长导致分区过多",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "Interval分区数过多,建议合并历史分区或调整interval粒度"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "合并历史小分区", SkillCommand: "/sql @merge_partitions", RawSQL: "ALTER TABLE {owner}.{table} MERGE PARTITIONS {p1}, {p2} INTO PARTITION {p_merged}"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(1000),
					Label:    "分区数1000-10000,需关注",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "Interval分区数较多(>1000),建议评估分区管理策略"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看分区分布情况", SkillCommand: "/sql @partition_stats", RawSQL: "SELECT table_name, COUNT(*) part_cnt, MIN(partition_name) min_part, MAX(partition_name) max_part FROM DBA_TAB_PARTITIONS WHERE table_owner = '{owner}' GROUP BY table_name HAVING COUNT(*) > 1000 ORDER BY part_cnt DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "分区数量正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "Interval分区数在正常范围内"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"partition", "interval", "management"},
		Versions: "11g+",
	}
}

// ruleSubpartitionImbalance detects highly skewed subpartition sizes,
// where some subpartitions are significantly larger than others.
func ruleSubpartitionImbalance() *Rule {
	return &Rule{
		ID:       "subpartition_imbalance",
		Name:     "子分区不均衡(Subpartition Imbalance)",
		Category: "partition",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "subpartition imbalance"},
			{Type: SignalKeyword, Key: "partition skew"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查子分区大小分布",
			Query: QuerySubpartitionBalance,
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "子分区大小比>100:1,严重倾斜",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查子分区键选择是否合理",
						Query: QuerySubpartitionBalance,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "子分区键数据分布不均匀",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "组合分区的子分区大小严重不均衡(最大/最小>100:1),子分区键选择不当"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "分析子分区键值分布", SkillCommand: "/sql @subpart_analysis", RawSQL: "SELECT partition_name, subpartition_name, num_rows, ROUND(bytes/1024/1024) mb FROM DBA_TAB_SUBPARTITIONS WHERE table_name = '{table}' AND table_owner = '{owner}' ORDER BY bytes DESC"},
									{Type: ActionFix, Desc: "重新设计子分区策略", SkillCommand: "/sql @redefine_subpart", RawSQL: "-- 评估使用HASH子分区替代LIST/RANGE以均匀分布:\n-- ALTER TABLE {table} MODIFY DEFAULT ATTRIBUTES FOR PARTITION {part}\n--   STORE IN ({ts1},{ts2},{ts3},{ts4})"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "需评估子分区策略",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "子分区不均衡,需评估是否影响查询性能"}},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查子分区上的查询模式", SkillCommand: "/sql @subpart_access", RawSQL: "SELECT table_name, partition_name, subpartition_name, num_rows FROM DBA_TAB_SUBPARTITIONS WHERE table_name = '{table}' ORDER BY num_rows DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(10),
					Label:    "子分区大小比10-100:1,中等倾斜",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "子分区大小存在中等不均衡(比值>10:1),建议关注"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看子分区大小分布", SkillCommand: "/sql @subpart_stats", RawSQL: "SELECT subpartition_name, num_rows, ROUND(bytes/1024/1024) mb FROM DBA_TAB_SUBPARTITIONS WHERE table_name = '{table}' ORDER BY bytes DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "子分区分布均匀",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "子分区大小分布较均匀"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"partition", "subpartition", "balance"},
		Versions: "11g+",
	}
}

// ─── Backup/Recovery Rules (4) ──────────────────────────────────────────────

func backupRecoveryRules() []*Rule {
	return []*Rule{
		ruleRMANBackupStale(),
		ruleRMANBackupFailing(),
		ruleFRASpacePressure(),
		ruleArchiveLogRetention(),
	}
}

// ruleRMANBackupStale detects last successful backup older than 24 hours.
func ruleRMANBackupStale() *Rule {
	return &Rule{
		ID:       "rman_backup_stale",
		Name:     "备份过期(RMAN Backup Stale >24h)",
		Category: "backup",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANBackupAge},
			{Type: SignalKeyword, Key: "backup stale"},
			{Type: SignalKeyword, Key: "last backup"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANBackupAge, Op: OpGT, Value: 24},
			},
		},
		Tree: &TreeNode{
			Step:  "检查最近RMAN备份状态",
			Query: QueryRMANBackupStatus,
			Branches: []Branch{
				{
					Match:    MatchGT(72),
					Label:    "最后成功备份>72小时,高风险",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查备份失败原因",
						Query: QueryRMANFailures,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在备份失败记录",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "最后成功备份已超过72小时,且存在备份失败记录,数据丢失风险极高"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即排查备份失败原因并手动触发备份", SkillCommand: "/sql @rman_failures", RawSQL: "SELECT operation, status, start_time, end_time, output_device_type FROM V$RMAN_STATUS WHERE status = 'FAILED' AND start_time > SYSDATE - 3 ORDER BY start_time DESC"},
									{Type: ActionFix, Desc: "修复后立即执行全库备份", SkillCommand: "/sql @rman_backup_now", RawSQL: "-- RMAN命令:\n-- BACKUP DATABASE PLUS ARCHIVELOG;\n-- BACKUP CURRENT CONTROLFILE;\n-- DELETE OBSOLETE;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "无失败记录,备份作业可能未调度",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "超过72小时无成功备份,且无失败记录,备份作业可能未被调度"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查RMAN调度作业配置", SkillCommand: "/sql @scheduler_jobs", RawSQL: "SELECT job_name, enabled, state, last_start_date, next_run_date FROM DBA_SCHEDULER_JOBS WHERE job_name LIKE '%BACKUP%' OR job_name LIKE '%RMAN%' ORDER BY last_start_date DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(24),
					Label:    "最后成功备份>24小时,超出窗口",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "最后成功备份已超过24小时,超出生产系统RTO/RPO要求"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "检查备份调度配置", SkillCommand: "/sql @rman_config", RawSQL: "SELECT operation, status, TO_CHAR(start_time,'YYYY-MM-DD HH24:MI') start_t, TO_CHAR(end_time,'YYYY-MM-DD HH24:MI') end_t FROM V$RMAN_STATUS WHERE operation = 'BACKUP' AND start_time > SYSDATE - 7 ORDER BY start_time DESC"},
						{Type: ActionPrevent, Desc: "配置备份监控告警", SkillCommand: "/alert", RawSQL: "-- 建议配置监控脚本检查V$RMAN_STATUS中最后成功备份时间"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "备份时间正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "最后成功备份在24小时内,备份策略正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"backup", "rman", "recovery"},
		Versions: "10g+",
	}
}

// ruleRMANBackupFailing detects recent backup failures in V$RMAN_STATUS.
func ruleRMANBackupFailing() *Rule {
	return &Rule{
		ID:       "rman_backup_failing",
		Name:     "备份持续失败(RMAN Backup Failing)",
		Category: "backup",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricRMANFailureCount},
			{Type: SignalKeyword, Key: "backup fail"},
			{Type: SignalKeyword, Key: "rman error"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricRMANFailureCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查V$RMAN_STATUS中的备份失败记录",
			Query: QueryRMANFailures,
			Branches: []Branch{
				{
					Match:    MatchGT(3),
					Label:    "连续>3次备份失败",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查备份失败的具体错误",
						Query: QueryRMANFailures,
						Branches: []Branch{
							{
								Match:    MatchEquals("ORA-19502"),
								Label:    "磁盘空间不足导致备份失败",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "备份连续失败因磁盘空间不足(ORA-19502),需立即清理空间"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "清理过期备份释放空间", SkillCommand: "/sql @rman_cleanup", RawSQL: "-- RMAN命令:\n-- DELETE NOPROMPT OBSOLETE;\n-- DELETE NOPROMPT EXPIRED BACKUP;\n-- CROSSCHECK BACKUP;"},
									{Type: ActionFix, Desc: "增加备份目标磁盘空间", SkillCommand: "/sql @backup_dest", RawSQL: "SHOW PARAMETER db_recovery_file_dest_size"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他错误导致备份失败",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "备份连续失败,需立即排查失败根因并修复"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看备份失败详细错误", SkillCommand: "/sql @rman_output", RawSQL: "SELECT output FROM V$RMAN_OUTPUT WHERE session_recid = (SELECT MAX(session_recid) FROM V$RMAN_STATUS WHERE status = 'FAILED') ORDER BY recid"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "存在1-3次备份失败",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "近期存在RMAN备份失败记录,需排查原因"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看失败备份详情", SkillCommand: "/sql @rman_status", RawSQL: "SELECT operation, status, TO_CHAR(start_time,'YYYY-MM-DD HH24:MI') start_t, object_type, output_device_type FROM V$RMAN_STATUS WHERE status NOT IN ('COMPLETED','RUNNING') AND start_time > SYSDATE - 7 ORDER BY start_time DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无备份失败记录",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "近期RMAN备份均成功完成"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"rman_backup_stale"},
		Tags:     []string{"backup", "rman", "failure"},
		Versions: "10g+",
	}
}

// ruleFRASpacePressure detects FRA (Fast Recovery Area) usage >80% where
// auto cleanup cannot keep up with space demand.
func ruleFRASpacePressure() *Rule {
	return &Rule{
		ID:       "fra_space_pressure",
		Name:     "FRA空间不足(FRA Space Pressure)",
		Category: "backup",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFRAUsedPct},
			{Type: SignalKeyword, Key: "fra space"},
			{Type: SignalKeyword, Key: "recovery area full"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: metricFRAUsedPct, Op: OpGT, Value: 80},
			},
		},
		Tree: &TreeNode{
			Step:  "检查FRA使用率与组成",
			Query: QueryFRAUsage,
			Branches: []Branch{
				{
					Match:    MatchGT(95),
					Label:    "FRA使用率>95%,紧急",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查FRA中最大占用类型",
						Query: QueryFRAUsage,
						Branches: []Branch{
							{
								Match:    MatchEquals("ARCHIVED LOG"),
								Label:    "归档日志占用FRA主要空间",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "FRA使用率>95%,归档日志占用大量空间,auto cleanup无法跟上"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "手动清理已备份的归档日志", SkillCommand: "/sql @rman_del_arch", RawSQL: "-- RMAN命令:\n-- DELETE NOPROMPT ARCHIVELOG ALL BACKED UP 1 TIMES TO DEVICE TYPE DISK;\n-- 或按时间: DELETE NOPROMPT ARCHIVELOG UNTIL TIME 'SYSDATE-3';"},
									{Type: ActionFix, Desc: "增大FRA大小", SkillCommand: "/params db_recovery_file_dest_size", RawSQL: "ALTER SYSTEM SET db_recovery_file_dest_size = '{new_size}' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他类型占用FRA空间",
								Severity: SeverityCritical,
								Findings: []Finding{{Desc: "FRA使用率>95%,需立即清理空间"}},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查FRA空间组成并清理", SkillCommand: "/sql @fra_usage", RawSQL: "SELECT file_type, percent_space_used, percent_space_reclaimable, number_of_files FROM V$RECOVERY_AREA_USAGE ORDER BY percent_space_used DESC"},
									{Type: ActionFix, Desc: "删除过期备份释放FRA空间", SkillCommand: "/sql @rman_cleanup", RawSQL: "-- RMAN: DELETE NOPROMPT OBSOLETE;\n-- RMAN: CROSSCHECK BACKUP; DELETE NOPROMPT EXPIRED BACKUP;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "FRA使用率80-95%,预警",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "FRA使用率>80%,建议扩容或优化备份保留策略"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "检查并优化RMAN保留策略", SkillCommand: "/sql @rman_retention", RawSQL: "-- RMAN: SHOW RETENTION POLICY;\n-- 查看: SELECT file_type, percent_space_used FROM V$RECOVERY_AREA_USAGE"},
						{Type: ActionPrevent, Desc: "设置FRA空间监控告警", SkillCommand: "/alert", RawSQL: "SELECT ROUND(space_used/space_limit*100,1) pct_used, ROUND(space_reclaimable/1024/1024) reclaimable_mb FROM V$RECOVERY_FILE_DEST"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "FRA空间使用正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "FRA空间使用率在正常范围"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"rman_backup_failing"},
		CausesOf: []string{},
		Tags:     []string{"backup", "fra", "space"},
		Versions: "10g+",
	}
}

// ruleArchiveLogRetention detects archive logs not backed up and filling
// storage, a common cause of database hangs.
func ruleArchiveLogRetention() *Rule {
	return &Rule{
		ID:       "archive_log_retention",
		Name:     "归档日志未备份(Archive Log Retention)",
		Category: "backup",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricFRAUsedPct},
			{Type: SignalKeyword, Key: "archive log"},
			{Type: SignalKeyword, Key: "archivelog backup"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查归档日志备份状态",
			Query: QueryArchiveLogBackup,
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "大量归档日志未备份(>100个)",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查归档目标空间",
						Query: QueryArchiveStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(90),
								Label:    "归档目标空间>90%,即将满",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "大量归档日志未备份(>100个),归档目标空间即将满,可能导致数据库挂起"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即备份归档日志释放空间", SkillCommand: "/sql @backup_archivelog", RawSQL: "-- RMAN命令:\n-- BACKUP ARCHIVELOG ALL DELETE INPUT;\n-- 或: BACKUP ARCHIVELOG ALL NOT BACKED UP;"},
									{Type: ActionFix, Desc: "配置归档日志自动备份和清理", SkillCommand: "/sql @rman_arch_policy", RawSQL: "-- RMAN配置:\n-- CONFIGURE ARCHIVELOG DELETION POLICY TO BACKED UP 1 TIMES TO DISK;\n-- CONFIGURE BACKUP OPTIMIZATION ON;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "归档空间尚有余量",
								Severity: SeverityHigh,
								Findings: []Finding{{Desc: "大量归档日志未备份,虽空间暂时充足但存在风险"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "备份归档日志并配置自动清理", SkillCommand: "/sql @backup_archivelog", RawSQL: "-- RMAN: BACKUP ARCHIVELOG ALL;\n-- 然后配置自动删除策略"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量归档日志未备份",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在未备份的归档日志,建议检查备份策略"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看未备份归档日志", SkillCommand: "/sql @unbackup_archlog", RawSQL: "SELECT sequence#, TO_CHAR(first_time,'YYYY-MM-DD HH24:MI') first_t, TO_CHAR(completion_time,'YYYY-MM-DD HH24:MI') comp_t, backed_up FROM V$ARCHIVED_LOG WHERE backed_up = 'NO' AND deleted = 'NO' ORDER BY sequence# DESC FETCH FIRST 20 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "归档日志均已备份",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有归档日志均已备份,备份策略正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{"rman_backup_failing"},
		CausesOf: []string{"fra_space_pressure"},
		Tags:     []string{"backup", "archivelog", "retention"},
		Versions: "10g+",
	}
}

// ─── Upgrade Rules (3) ──────────────────────────────────────────────────────

func upgradeRules() []*Rule {
	return []*Rule{
		ruleInvalidObjects(),
		ruleDeprecatedParameters(),
		ruleCompatibilityCheck(),
	}
}

// ruleInvalidObjects detects invalid objects after upgrade or patch application.
func ruleInvalidObjects() *Rule {
	return &Rule{
		ID:       "invalid_objects",
		Name:     "无效对象(Invalid Objects Post-Upgrade)",
		Category: "upgrade",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricInvalidObjectCount},
			{Type: SignalErrorCode, Key: "ORA-04063"},
			{Type: SignalKeyword, Key: "invalid objects"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricInvalidObjectCount, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查无效对象数量与类型",
			Query: QueryInvalidObjects,
			Branches: []Branch{
				{
					Match:    MatchGT(100),
					Label:    "无效对象>100个,可能升级后未重编译",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查无效对象所属Schema",
						Query: QueryInvalidObjects,
						Branches: []Branch{
							{
								Match:    MatchEquals("SYS"),
								Label:    "SYS Schema有无效对象",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "升级/补丁后SYS Schema存在大量无效对象,需立即运行utlrp.sql修复"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "运行utlrp.sql重编译所有无效对象", SkillCommand: "/sql @recompile_all", RawSQL: "@$ORACLE_HOME/rdbms/admin/utlrp.sql"},
									{Type: ActionInvestigate, Desc: "查看无效对象详情", SkillCommand: "/sql @invalid_objects", RawSQL: "SELECT owner, object_type, object_name, status FROM DBA_OBJECTS WHERE status = 'INVALID' ORDER BY owner, object_type, object_name"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "应用Schema有无效对象",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "应用Schema存在大量无效对象,可能影响业务功能"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "重编译应用Schema无效对象", SkillCommand: "/sql @recompile_schema", RawSQL: "BEGIN DBMS_UTILITY.COMPILE_SCHEMA(schema => '{schema}', compile_all => FALSE); END;"},
									{Type: ActionInvestigate, Desc: "按类型统计无效对象", SkillCommand: "/sql @invalid_summary", RawSQL: "SELECT owner, object_type, COUNT(*) cnt FROM DBA_OBJECTS WHERE status = 'INVALID' GROUP BY owner, object_type ORDER BY cnt DESC"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量无效对象(<100)",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在少量无效对象,建议逐一重编译确认"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "逐一重编译无效对象", SkillCommand: "/sql @recompile", RawSQL: "SELECT 'ALTER ' || DECODE(object_type,'PACKAGE BODY','PACKAGE','TYPE BODY','TYPE',object_type) || ' ' || owner || '.' || object_name || ' COMPILE' || DECODE(object_type,'PACKAGE BODY',' BODY','TYPE BODY',' BODY') || ';' stmt FROM DBA_OBJECTS WHERE status = 'INVALID' ORDER BY object_type, owner, object_name"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无无效对象",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "数据库中无无效对象,状态正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"upgrade", "patch", "invalid", "compile"},
		Versions: "9i+",
	}
}

// ruleDeprecatedParameters detects usage of deprecated or removed parameters
// that may cause issues after database upgrade.
func ruleDeprecatedParameters() *Rule {
	return &Rule{
		ID:       "deprecated_parameters",
		Name:     "废弃参数使用(Deprecated Parameters)",
		Category: "upgrade",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "deprecated parameter"},
			{Type: SignalKeyword, Key: "removed parameter"},
			{Type: SignalKeyword, Key: "upgrade parameter"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查正在使用的废弃/移除参数",
			Query: QueryDeprecatedParams,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "使用>5个废弃参数",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有已移除(removed)参数",
						Query: QueryDeprecatedParams,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在已移除参数,升级后可能启动失败",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "使用了已被移除的参数,目标版本升级后数据库可能无法启动"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "升级前必须移除已废弃参数", SkillCommand: "/sql @deprecated_params", RawSQL: "SELECT name, value, isdefault, ismodified, description FROM V$PARAMETER WHERE name IN (SELECT name FROM V$OBSOLETE_PARAMETER WHERE isspecified = 'TRUE')"},
									{Type: ActionFix, Desc: "使用RESET清除spfile中的废弃参数", SkillCommand: "/sql @reset_params", RawSQL: "ALTER SYSTEM RESET \"{param_name}\" SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "仅废弃但未移除",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "多个废弃参数仍在使用,建议升级前清理以避免潜在问题"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "逐步替换废弃参数", SkillCommand: "/sql @deprecated_params", RawSQL: "SELECT name, value, isdefault FROM V$PARAMETER WHERE name IN (SELECT name FROM V$OBSOLETE_PARAMETER) AND isdefault = 'FALSE'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量废弃参数(<5)",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在少量废弃参数,建议在下次维护窗口清理"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看废弃参数列表", SkillCommand: "/sql @obsolete_params", RawSQL: "SELECT p.name, p.value, o.isspecified FROM V$PARAMETER p, V$OBSOLETE_PARAMETER o WHERE p.name = o.name AND p.isdefault = 'FALSE'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "未使用废弃参数",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未使用任何废弃或移除参数"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"upgrade", "parameter", "deprecated"},
		Versions: "10g+",
	}
}

// ruleCompatibilityCheck detects the compatible parameter blocking new features
// that require a higher compatibility level.
func ruleCompatibilityCheck() *Rule {
	return &Rule{
		ID:       "compatibility_check",
		Name:     "Compatible参数限制(Compatibility Check)",
		Category: "upgrade",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "compatible"},
			{Type: SignalKeyword, Key: "compatibility"},
			{Type: SignalKeyword, Key: "upgrade compatible"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查compatible参数与实际版本差异",
			Query: QueryCompatibleParam,
			Branches: []Branch{
				{
					Match:    MatchGT(2),
					Label:    "compatible落后实际版本>2个大版本",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查被阻塞的功能特性",
						Query: QueryCompatibleParam,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "多个重要功能被阻塞",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "compatible参数远低于实际数据库版本,多个新功能(如在线操作、JSON支持等)无法使用"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "评估提高compatible参数(不可回退!)", SkillCommand: "/params compatible", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name = 'compatible'"},
									{Type: ActionInvestigate, Desc: "查看当前版本与compatible差异", SkillCommand: "/sql @version_info", RawSQL: "SELECT banner, banner_full FROM V$VERSION UNION ALL SELECT 'compatible=' || value, NULL FROM V$PARAMETER WHERE name = 'compatible'"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "功能影响较小",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "compatible参数低于实际版本,部分新功能受限"}},
								Actions: []Action{
									{Type: ActionPrevent, Desc: "下次维护窗口评估compatible升级", SkillCommand: "/params compatible", RawSQL: "-- 注意: 提高compatible参数不可回退!\n-- ALTER SYSTEM SET compatible = '{target_version}' SCOPE=SPFILE;\n-- 需要重启数据库"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "compatible略低于实际版本",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "compatible参数略低于数据库实际版本,建议在维护窗口提升"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "确认compatible提升影响", SkillCommand: "/params compatible", RawSQL: "SELECT name, value, description FROM V$PARAMETER WHERE name = 'compatible'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "compatible与版本匹配",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "compatible参数与数据库版本匹配,所有功能可用"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"upgrade", "compatible", "feature"},
		Versions: "10g+",
	}
}

// ─── Parameter Rules (4) ────────────────────────────────────────────────────

func parameterRules() []*Rule {
	return []*Rule{
		ruleOptimizerParamMismatch(),
		ruleUnderscoredParams(),
		ruleSGAPGARatio(),
		ruleProcessesSessionsRatio(),
	}
}

// ruleOptimizerParamMismatch detects non-default optimizer parameters that
// may cause unexpected query plan behavior.
func ruleOptimizerParamMismatch() *Rule {
	return &Rule{
		ID:       "optimizer_param_mismatch",
		Name:     "优化器参数异常(Optimizer Param Mismatch)",
		Category: "parameter",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "optimizer parameter"},
			{Type: SignalKeyword, Key: "optimizer_features_enable"},
			{Type: SignalKeyword, Key: "non-default parameter"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查非默认优化器参数",
			Query: QueryOptimizerParams,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "非默认优化器参数>5个",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查optimizer_features_enable版本",
						Query: QueryOptimizerParams,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "optimizer_features_enable低于实际版本",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "optimizer_features_enable设置低于数据库实际版本,且多个优化器参数被手动修改,可能导致次优执行计划"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "列出所有非默认优化器参数", SkillCommand: "/params optimizer%", RawSQL: "SELECT name, value, isdefault, ismodified FROM V$PARAMETER WHERE name LIKE 'optimizer%' AND isdefault = 'FALSE' ORDER BY name"},
									{Type: ActionFix, Desc: "评估恢复优化器参数为默认值", SkillCommand: "/sql @reset_optimizer", RawSQL: "-- 逐一评估后重置:\nALTER SYSTEM RESET \"{param}\" SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "optimizer_features_enable版本正确",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "多个优化器参数被手动修改,建议逐一评估必要性"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查手动修改的优化器参数", SkillCommand: "/params optimizer%", RawSQL: "SELECT name, value, isdefault FROM V$PARAMETER WHERE name LIKE 'optimizer%' AND isdefault = 'FALSE'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量非默认优化器参数",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在非默认优化器参数,建议确认修改原因"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看修改的优化器参数", SkillCommand: "/params optimizer%", RawSQL: "SELECT name, value, isdefault, ismodified, description FROM V$PARAMETER WHERE name LIKE 'optimizer%' AND isdefault = 'FALSE'"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "优化器参数均为默认值",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "所有优化器参数均为默认值,配置正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"sql_plan_drift"},
		Tags:     []string{"parameter", "optimizer", "tuning"},
		Versions: "10g+",
	}
}

// ruleUnderscoredParams detects hidden (underscore) parameters set without
// Oracle Service Request, which can cause unexpected behavior.
func ruleUnderscoredParams() *Rule {
	return &Rule{
		ID:       "underscored_params",
		Name:     "隐藏参数使用(Hidden Underscore Parameters)",
		Category: "parameter",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "hidden parameter"},
			{Type: SignalKeyword, Key: "underscore parameter"},
			{Type: SignalKeyword, Key: "_parameter"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查已设置的隐藏参数(下划线开头)",
			Query: QueryUnderscoredParams,
			Branches: []Branch{
				{
					Match:    MatchGT(10),
					Label:    "隐藏参数>10个,过多",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有高风险隐藏参数",
						Query: QueryUnderscoredParams,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "存在高风险隐藏参数",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "设置了大量隐藏参数(>10个),且包含高风险参数,可能导致数据库不稳定或数据损坏风险"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "审计所有隐藏参数并确认SR编号", SkillCommand: "/sql @hidden_params", RawSQL: "SELECT ksppinm name, ksppstvl value, ksppdesc description FROM x$ksppi a, x$ksppsv b WHERE a.indx = b.indx AND ksppinm LIKE '\\_%' ESCAPE '\\' AND ksppstdf = 'FALSE' ORDER BY ksppinm"},
									{Type: ActionFix, Desc: "移除无SR支持的隐藏参数", SkillCommand: "/sql @reset_hidden", RawSQL: "ALTER SYSTEM RESET \"{_param}\" SCOPE=SPFILE"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "隐藏参数风险可控",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "设置了较多隐藏参数,建议逐一核实是否有Oracle SR支持"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "列出所有非默认隐藏参数", SkillCommand: "/sql @hidden_params", RawSQL: "SELECT ksppinm name, ksppstvl value FROM x$ksppi a, x$ksppsv b WHERE a.indx = b.indx AND ksppinm LIKE '\\_%' ESCAPE '\\' AND ksppstdf = 'FALSE'"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量隐藏参数(1-10)",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在少量隐藏参数,建议确认每个参数均有Oracle SR支持"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看非默认隐藏参数", SkillCommand: "/sql @hidden_params", RawSQL: "SELECT ksppinm name, ksppstvl value, ksppdesc description FROM x$ksppi a, x$ksppsv b WHERE a.indx = b.indx AND ksppinm LIKE '\\_%' ESCAPE '\\' AND ksppstdf = 'FALSE' ORDER BY ksppinm"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无自定义隐藏参数",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "未设置任何隐藏参数,配置正常"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"parameter", "hidden", "underscore"},
		Versions: "9i+",
	}
}

// ruleSGAPGARatio detects SGA+PGA exceeding 80% of physical memory,
// which can cause OS-level memory pressure and swapping.
func ruleSGAPGARatio() *Rule {
	return &Rule{
		ID:       "sga_pga_ratio",
		Name:     "SGA+PGA内存占比过高(SGA+PGA Memory Ratio)",
		Category: "parameter",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "sga pga memory"},
			{Type: SignalKeyword, Key: "memory ratio"},
			{Type: SignalKeyword, Key: "memory overcommit"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查SGA+PGA与物理内存的比值",
			Query: QuerySGAPGAMemory,
			Branches: []Branch{
				{
					Match:    MatchGT(90),
					Label:    "SGA+PGA >90%物理内存,严重过量",
					Severity: SeverityCritical,
					Then: &TreeNode{
						Step:  "检查是否已出现swap使用",
						Query: QuerySGAPGAMemory,
						Branches: []Branch{
							{
								Match:    MatchGT(0),
								Label:    "OS已使用swap,内存严重不足",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "SGA+PGA占物理内存>90%,OS已开始使用swap,数据库性能严重受影响"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即缩减SGA或PGA", SkillCommand: "/params sga_target", RawSQL: "SELECT 'SGA' comp, ROUND(SUM(value)/1024/1024) mb FROM V$SGA UNION ALL SELECT 'PGA', ROUND(value/1024/1024) FROM V$PGASTAT WHERE name = 'total PGA allocated'"},
									{Type: ActionFix, Desc: "调整memory_target使SGA+PGA<=80%物理内存", SkillCommand: "/params memory_target", RawSQL: "ALTER SYSTEM SET memory_target = '{target}' SCOPE=BOTH"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "暂未swap但风险高",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "SGA+PGA占物理内存>90%,虽暂无swap但连接数增加时极易引发OOM"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "降低SGA/PGA使总量不超过80%物理内存", SkillCommand: "/params sga_target", RawSQL: "ALTER SYSTEM SET sga_target = '{new_sga}' SCOPE=BOTH;\nALTER SYSTEM SET pga_aggregate_target = '{new_pga}' SCOPE=BOTH"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(80),
					Label:    "SGA+PGA 80-90%物理内存,偏高",
					Severity: SeverityHigh,
					Findings: []Finding{{Desc: "SGA+PGA占物理内存>80%,建议降低以留出OS和其他进程空间"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "调整SGA+PGA使总量<=80%物理内存", SkillCommand: "/params memory_target", RawSQL: "SELECT name, ROUND(value/1024/1024) mb FROM V$PARAMETER WHERE name IN ('sga_target','pga_aggregate_target','memory_target')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "内存分配合理",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "SGA+PGA占物理内存比例合理(<80%)"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"pga_insufficient"},
		Tags:     []string{"parameter", "memory", "sga", "pga"},
		Versions: "10g+",
	}
}

// ruleProcessesSessionsRatio detects misconfigured processes and sessions
// parameters where sessions != processes*1.5+22.
func ruleProcessesSessionsRatio() *Rule {
	return &Rule{
		ID:       "processes_sessions_ratio",
		Name:     "Processes/Sessions配置异常(Processes Sessions Ratio)",
		Category: "parameter",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricTotalSessions},
			{Type: SignalKeyword, Key: "processes sessions"},
			{Type: SignalKeyword, Key: "max sessions"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查processes和sessions参数配置",
			Query: QueryProcessesSessions,
			Branches: []Branch{
				{
					Match:    MatchGT(2),
					Label:    "sessions/processes比值异常(>2倍偏差)",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查当前实际使用情况",
						Query: QueryResourceLimit,
						Branches: []Branch{
							{
								Match:    MatchGT(90),
								Label:    "当前sessions使用>90%限制",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "sessions参数配置不合理且当前使用率>90%,即将耗尽连接资源"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "立即调整processes和sessions参数", SkillCommand: "/params processes", RawSQL: "ALTER SYSTEM SET processes = {new_processes} SCOPE=SPFILE;\nALTER SYSTEM SET sessions = {new_sessions} SCOPE=SPFILE;\n-- 需要重启数据库生效"},
									{Type: ActionInvestigate, Desc: "检查当前连接数与来源", SkillCommand: "/sql @session_count", RawSQL: "SELECT machine, program, COUNT(*) cnt FROM V$SESSION WHERE type = 'USER' GROUP BY machine, program ORDER BY cnt DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "使用率尚可但配置需优化",
								Severity: SeverityMedium,
								Findings: []Finding{
									{Desc: "processes和sessions参数配置不符合Oracle推荐比例(sessions=processes*1.5+22)"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "按推荐公式调整参数", SkillCommand: "/params processes", RawSQL: "-- Oracle推荐: sessions = processes * 1.5 + 22\n-- 当前值:\nSELECT name, value FROM V$PARAMETER WHERE name IN ('processes','sessions') ORDER BY name"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "sessions/processes比值略有偏差",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "processes/sessions参数比值与推荐略有偏差,非紧急问题"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "下次维护窗口调整为推荐值", SkillCommand: "/params processes", RawSQL: "SELECT name, value FROM V$PARAMETER WHERE name IN ('processes','sessions','transactions')"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "processes/sessions配置正常",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "processes和sessions参数配置符合推荐比例"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"parameter", "processes", "sessions"},
		Versions: "9i+",
	}
}

// ─── Performance Baseline Rules (4) ─────────────────────────────────────────

func performanceBaselineRules() []*Rule {
	return []*Rule{
		ruleAWRRetentionShort(),
		ruleAWRSnapshotGap(),
		ruleADDMFindingsIgnored(),
		ruleBaselineNotEstablished(),
	}
}

// ruleAWRRetentionShort detects AWR retention period less than 30 days,
// which is insufficient for production performance trend analysis.
func ruleAWRRetentionShort() *Rule {
	return &Rule{
		ID:       "awr_retention_short",
		Name:     "AWR保留期过短(AWR Retention <30 Days)",
		Category: "performance_baseline",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricAWRRetentionDays},
			{Type: SignalKeyword, Key: "awr retention"},
			{Type: SignalKeyword, Key: "awr snapshot"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricAWRRetentionDays, Op: OpLT, Value: 30},
			},
		},
		Tree: &TreeNode{
			Step:  "检查AWR保留策略配置",
			Query: QueryAWRRetention,
			Branches: []Branch{
				{
					Match:    MatchLT(8),
					Label:    "AWR保留<8天,严重不足",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查SYSAUX表空间剩余空间",
						Query: QueryDatafileStatus,
						Branches: []Branch{
							{
								Match:    MatchGT(80),
								Label:    "SYSAUX空间>80%,可能是AWR被缩短的原因",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "AWR保留期<8天且SYSAUX表空间空间不足,导致历史性能数据缺失,无法进行趋势分析"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "扩大SYSAUX表空间后增加AWR保留期", SkillCommand: "/sql ALTER TABLESPACE SYSAUX ADD DATAFILE", RawSQL: "ALTER TABLESPACE SYSAUX ADD DATAFILE SIZE 5G AUTOEXTEND ON MAXSIZE 20G;\nBEGIN DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(retention => 43200, interval => 60); END;"},
									{Type: ActionInvestigate, Desc: "检查SYSAUX空间占用", SkillCommand: "/sql @sysaux_usage", RawSQL: "SELECT occupant_name, space_usage_kbytes/1024 mb FROM V$SYSAUX_OCCUPANTS ORDER BY space_usage_kbytes DESC"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "SYSAUX空间充足,手动增加保留期",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "AWR保留期过短,SYSAUX空间充足,建议增加保留期"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "增加AWR保留期至30天", SkillCommand: "/sql @awr_modify", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(retention => 43200, interval => 60); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchLT(30),
					Label:    "AWR保留8-30天,生产环境偏短",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "AWR保留期<30天,生产环境建议至少保留30天以支持月度性能对比"}},
					Actions: []Action{
						{Type: ActionFix, Desc: "增加AWR保留期至30-90天", SkillCommand: "/sql @awr_modify", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(retention => 43200); END;\n-- 43200分钟 = 30天"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "AWR保留期>=30天",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "AWR保留期配置合理(>=30天)"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"awr", "retention", "baseline"},
		Versions: "10g+",
	}
}

// ruleAWRSnapshotGap detects missing AWR snapshots indicating snapshot
// collection issues.
func ruleAWRSnapshotGap() *Rule {
	return &Rule{
		ID:       "awr_snapshot_gap",
		Name:     "AWR快照缺失(AWR Snapshot Gap)",
		Category: "performance_baseline",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "awr snapshot gap"},
			{Type: SignalKeyword, Key: "missing snapshot"},
			{Type: SignalKeyword, Key: "awr gap"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查AWR快照连续性",
			Query: QueryAWRSnapshots,
			Branches: []Branch{
				{
					Match:    MatchGT(6),
					Label:    "快照缺失>6小时,长时间中断",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查MMON进程状态与告警日志",
						Query: QueryAWRSnapshots,
						Branches: []Branch{
							{
								Match:    MatchEquals("MMON_DOWN"),
								Label:    "MMON进程异常",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "AWR快照缺失超过6小时,MMON后台进程可能异常,导致无法收集性能数据"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "检查MMON进程状态", SkillCommand: "/sql @mmon_status", RawSQL: "SELECT pname, description, paddr FROM V$BGPROCESS WHERE pname = 'MMON';\nSELECT sid, serial#, program, status FROM V$SESSION WHERE program LIKE '%MMON%'"},
									{Type: ActionFix, Desc: "手动创建快照验证功能", SkillCommand: "/sql @manual_snap", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT(); END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "其他原因导致快照中断",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "AWR快照存在较长时间缺失,可能因数据库重启或SYSAUX空间不足"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "检查缺失时段的告警日志", SkillCommand: "/sql @snap_gaps", RawSQL: "SELECT snap_id, TO_CHAR(begin_interval_time,'YYYY-MM-DD HH24:MI') begin_t, TO_CHAR(end_interval_time,'YYYY-MM-DD HH24:MI') end_t FROM DBA_HIST_SNAPSHOT ORDER BY snap_id DESC FETCH FIRST 48 ROWS ONLY"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "存在短期快照缺失",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "AWR快照存在短期缺失,建议检查快照采集间隔配置"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看快照采集配置", SkillCommand: "/sql @awr_settings", RawSQL: "SELECT snap_interval, retention FROM DBA_HIST_WR_CONTROL"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "AWR快照连续完整",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "AWR快照采集连续,无缺失"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{"awr_retention_short"},
		Tags:     []string{"awr", "snapshot", "mmon"},
		Versions: "10g+",
	}
}

// ruleADDMFindingsIgnored detects ADDM high-impact findings that have not
// been addressed.
func ruleADDMFindingsIgnored() *Rule {
	return &Rule{
		ID:       "addm_findings_ignored",
		Name:     "ADDM高影响发现未处理(ADDM Findings Ignored)",
		Category: "performance_baseline",
		Signals: []Signal{
			{Type: SignalMetric, Key: metricADDMHighFindings},
			{Type: SignalKeyword, Key: "addm findings"},
			{Type: SignalKeyword, Key: "addm recommendation"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
			Conditions: []Condition{
				{Source: "metrics", Field: metricADDMHighFindings, Op: OpGT, Value: 0},
			},
		},
		Tree: &TreeNode{
			Step:  "检查ADDM最近高影响发现",
			Query: QueryADDMFindings,
			Branches: []Branch{
				{
					Match:    MatchGT(5),
					Label:    "高影响ADDM发现>5个",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查ADDM发现类型分布",
						Query: QueryADDMFindings,
						Branches: []Branch{
							{
								Match:    MatchGT(50),
								Label:    "ADDM估算影响>50% DB Time",
								Severity: SeverityCritical,
								Findings: []Finding{
									{Desc: "ADDM发现多个高影响问题(估算影响>50% DB Time)未被处理,数据库性能可显著提升"},
								},
								Actions: []Action{
									{Type: ActionUrgent, Desc: "查看并处理高影响ADDM建议", SkillCommand: "/sql @addm_findings", RawSQL: "SELECT f.finding_name, f.impact_type, ROUND(f.impact,1) impact_pct, r.type rec_type, r.benefit_type FROM DBA_ADVISOR_FINDINGS f JOIN DBA_ADVISOR_RECOMMENDATIONS r ON f.task_id = r.task_id AND f.finding_id = r.finding_id WHERE f.task_id = (SELECT MAX(task_id) FROM DBA_ADVISOR_TASKS WHERE advisor_name = 'ADDM') ORDER BY f.impact DESC"},
									{Type: ActionFix, Desc: "按ADDM建议执行优化", SkillCommand: "/sql @addm_actions", RawSQL: "SELECT a.command, a.message FROM DBA_ADVISOR_ACTIONS a WHERE a.task_id = (SELECT MAX(task_id) FROM DBA_ADVISOR_TASKS WHERE advisor_name = 'ADDM') ORDER BY a.rec_id, a.action_id"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "影响较低但仍需关注",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "ADDM存在多个高影响发现,建议逐一评估处理"},
								},
								Actions: []Action{
									{Type: ActionInvestigate, Desc: "查看ADDM发现详情", SkillCommand: "/sql @addm_report", RawSQL: "SELECT DBMS_ADVISOR.GET_TASK_REPORT(task_name => (SELECT task_name FROM DBA_ADVISOR_TASKS WHERE advisor_name = 'ADDM' AND task_id = (SELECT MAX(task_id) FROM DBA_ADVISOR_TASKS WHERE advisor_name = 'ADDM'))) FROM DUAL"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "少量ADDM高影响发现",
					Severity: SeverityMedium,
					Findings: []Finding{{Desc: "存在少量ADDM高影响发现,建议定期review"}},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看最新ADDM报告", SkillCommand: "/sql @addm_latest", RawSQL: "SELECT task_name, status, TO_CHAR(created,'YYYY-MM-DD HH24:MI') created FROM DBA_ADVISOR_TASKS WHERE advisor_name = 'ADDM' ORDER BY task_id DESC FETCH FIRST 5 ROWS ONLY"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "无高影响ADDM发现",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "ADDM最近未发现高影响性能问题"}},
					Actions:  nil,
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"addm", "advisor", "performance"},
		Versions: "10g+",
	}
}

// ruleBaselineNotEstablished detects no AWR baseline established for
// performance comparison in production environments.
func ruleBaselineNotEstablished() *Rule {
	return &Rule{
		ID:       "baseline_not_established",
		Name:     "AWR基线未建立(No AWR Baseline)",
		Category: "performance_baseline",
		Signals: []Signal{
			{Type: SignalKeyword, Key: "awr baseline"},
			{Type: SignalKeyword, Key: "performance baseline"},
			{Type: SignalKeyword, Key: "no baseline"},
		},
		Trigger: Trigger{
			Mode: TriggerManual,
		},
		Tree: &TreeNode{
			Step:  "检查AWR基线配置",
			Query: QueryAWRBaselines,
			Branches: []Branch{
				{
					Match:    MatchEquals("NONE"),
					Label:    "未建立任何AWR基线",
					Severity: SeverityHigh,
					Then: &TreeNode{
						Step:  "检查是否有足够历史快照可用于建立基线",
						Query: QueryAWRSnapshots,
						Branches: []Branch{
							{
								Match:    MatchGT(168),
								Label:    "有>1周快照,可建立基线",
								Severity: SeverityHigh,
								Findings: []Finding{
									{Desc: "生产数据库未建立AWR基线,无法对比性能变化趋势,有足够历史数据可立即建立"},
								},
								Actions: []Action{
									{Type: ActionFix, Desc: "建立业务高峰期AWR基线", SkillCommand: "/sql @create_baseline", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.CREATE_BASELINE(start_snap_id => {start_snap}, end_snap_id => {end_snap}, baseline_name => 'PEAK_HOURS_BASELINE', expiration => NULL); END;"},
									{Type: ActionPrevent, Desc: "配置移动窗口基线(自动维护)", SkillCommand: "/sql @moving_window_baseline", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.MODIFY_BASELINE_WINDOW_SIZE(window_size => 30); END;"},
								},
							},
							{
								Match:    MatchDefault(),
								Label:    "历史快照不足,需等待积累",
								Severity: SeverityMedium,
								Findings: []Finding{{Desc: "AWR历史快照不足,建议先增加AWR保留期积累数据后再建立基线"}},
								Actions: []Action{
									{Type: ActionFix, Desc: "增加AWR保留期并等待数据积累", SkillCommand: "/sql @awr_modify", RawSQL: "BEGIN DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(retention => 43200, interval => 60); END;"},
								},
							},
						},
					},
				},
				{
					Match:    MatchGT(0),
					Label:    "已有AWR基线",
					Severity: SeverityLow,
					Findings: []Finding{{Desc: "已建立AWR基线,可用于性能对比分析"}},
					Actions: []Action{
						{Type: ActionPrevent, Desc: "定期更新基线以反映最新正常负载", SkillCommand: "/sql @list_baselines", RawSQL: "SELECT baseline_name, baseline_type, start_snap_id, end_snap_id, TO_CHAR(creation_time,'YYYY-MM-DD') created FROM DBA_HIST_BASELINE ORDER BY creation_time DESC"},
					},
				},
			},
		},
		CausedBy: []string{},
		CausesOf: []string{},
		Tags:     []string{"awr", "baseline", "performance"},
		Versions: "10g+",
	}
}
