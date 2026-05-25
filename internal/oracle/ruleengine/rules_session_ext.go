/*-------------------------------------------------------------------------
 *
 * rules_session_ext.go
 *	  Oracle rule engine — session-level rule extensions (long-idle, blocking holders, parse failures).
 *	  Loaded by trigger.go and evaluated against the latest
 *	  probe snapshot to produce classified anomaly events.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/rules_session_ext.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// sessionExtRules returns session/connection resource-limit rules.
func sessionExtRules() []*Rule {
	return []*Rule{
		ruleSessionStorm(),
		ruleAbortedConnects(),
	}
}

// ruleSessionStorm detects when sessions approach the SESSIONS parameter limit.
// When usage exceeds 70%, it warns; above 90% it's critical (ORA-00018 imminent).
func ruleSessionStorm() *Rule {
	return &Rule{
		ID:       "session_storm",
		Name:     "连接冲高 — 短时间大量新建连接",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "active_sessions"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "sessions_limit_pct", Op: OpGT, Value: 70},
			},
		},
		Tree: &TreeNode{
			Step: "分析会话使用率和来源",
			Check: func(ctx *EvalContext) interface{} {
				pct := ctx.MetricValue("sessions_limit_pct")
				if pct > 90 {
					return "critical"
				}
				if pct > 70 {
					return "warning"
				}
				return "normal"
			},
			Branches: []Branch{
				{
					Match:    MatchEquals("critical"),
					Label:    "会话使用率>90%,即将耗尽",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "会话使用率超过90%,接近SESSIONS参数上限,新连接将被拒绝(ORA-00018)"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "立即检查会话来源,清理空闲连接",
							RawSQL: "SELECT username, machine, program, status, COUNT(*) cnt FROM v$session WHERE type='USER' GROUP BY username, machine, program, status ORDER BY cnt DESC"},
						{Type: ActionFix, Desc: "临时增大sessions参数",
							RawSQL: "ALTER SYSTEM SET sessions=<新值> SCOPE=SPFILE;\n-- 需要重启生效"},
						{Type: ActionPrevent, Desc: "为应用配置连接池,设置IDLE_TIME profile限制",
							RawSQL: "ALTER PROFILE DEFAULT LIMIT IDLE_TIME 30;"},
					},
				},
				{
					Match:    MatchEquals("warning"),
					Label:    "会话使用率>70%,需关注",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "会话使用率超过70%,建议排查是否有连接泄漏"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看空闲会话分布",
							RawSQL: "SELECT username, machine, status, COUNT(*) cnt, MIN(logon_time) earliest FROM v$session WHERE type='USER' GROUP BY username, machine, status ORDER BY cnt DESC"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "会话使用率正常",
					Severity: SeverityLow,
				},
			},
		},
		Tags:     []string{"session", "connection", "resource_limit"},
		Versions: "9i+",
	}
}

// ruleAbortedConnects detects when PROCESSES usage exceeds 85%, risking ORA-00020.
func ruleAbortedConnects() *Rule {
	return &Rule{
		ID:       "aborted_connects",
		Name:     "连接被拒绝 — sessions/processes达上限",
		Category: "session",
		Signals: []Signal{
			{Type: SignalMetric, Key: "processes_limit_pct"},
		},
		Trigger: Trigger{
			Mode: TriggerAuto,
			Conditions: []Condition{
				{Source: "metrics", Field: "processes_limit_pct", Op: OpGT, Value: 85},
			},
		},
		Tree: &TreeNode{
			Step: "分析processes使用率",
			Check: func(ctx *EvalContext) interface{} {
				pct := ctx.MetricValue("processes_limit_pct")
				if pct > 95 {
					return "critical"
				}
				return "warning"
			},
			Branches: []Branch{
				{
					Match:    MatchEquals("critical"),
					Label:    "Processes使用率>95%,新连接被拒绝",
					Severity: SeverityCritical,
					Findings: []Finding{
						{Desc: "Processes使用率超过95%,新连接将报ORA-00020错误"},
					},
					Actions: []Action{
						{Type: ActionUrgent, Desc: "检查是否有连接泄漏",
							RawSQL: "SELECT username, machine, program, COUNT(*) cnt FROM v$session WHERE type='USER' GROUP BY username, machine, program ORDER BY cnt DESC"},
						{Type: ActionFix, Desc: "增大processes参数",
							RawSQL: "ALTER SYSTEM SET processes=<新值> SCOPE=SPFILE;\n-- 需要重启生效, sessions会自动按 processes*1.5+22 调整"},
					},
				},
				{
					Match:    MatchDefault(),
					Label:    "Processes使用率偏高",
					Severity: SeverityHigh,
					Findings: []Finding{
						{Desc: "Processes使用率超过85%,需关注增长趋势"},
					},
					Actions: []Action{
						{Type: ActionInvestigate, Desc: "查看进程分布",
							RawSQL: "SELECT program, COUNT(*) FROM v$process GROUP BY program ORDER BY COUNT(*) DESC"},
					},
				},
			},
		},
		CausedBy: []string{"session_leak"},
		Tags:     []string{"session", "processes", "ora-00020", "connection"},
		Versions: "9i+",
	}
}
