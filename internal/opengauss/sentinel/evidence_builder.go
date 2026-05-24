/*-------------------------------------------------------------------------
 *
 * evidence_builder.go
 *    Deterministic evidence package for Sentinel burst diagnosis.
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *    internal/opengauss/sentinel/evidence_builder.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"fmt"
	"sort"
	"strings"
)

type IncidentEvidence struct {
	AlertMetric     []EvidencePair
	BaselineCurrent []EvidencePair
	Burst           []EvidencePair
	CurrentSnapshot []EvidencePair
	TopSQLs         []EvidencePair
	Waits           []EvidencePair
	Blockers        []EvidencePair
	RootCauses      []EvidencePair
	Urgent          []string
	Fixes           []string
	VerifySQL       []string
	Rollback        []string
}

type EvidencePair struct {
	Name  string
	Value string
	Note  string
}

func BuildIncidentEvidence(report BurstReport) IncidentEvidence {
	var e IncidentEvidence
	e.AlertMetric = buildAlertMetricEvidence(report)
	e.BaselineCurrent = buildBaselineCurrentEvidence(report)
	e.Burst = buildBurstMetricEvidence(report)
	e.CurrentSnapshot = buildCurrentSnapshotEvidence(report)
	e.TopSQLs = buildSentinelTopSQLEvidence(report)
	e.Waits = buildSentinelWaitEvidence(report)
	e.Blockers = buildBlockerEvidence(report)
	e.RootCauses = buildRootCauseEvidence(report)
	e.Urgent, e.Fixes = actionsForCause(report.Classification.Cause)
	e.VerifySQL, e.Rollback = validationForReport(report)
	return e
}

func FormatEvidenceDiagnosis(report BurstReport) string {
	e := BuildIncidentEvidence(report)
	var b strings.Builder
	b.WriteString("  OG Sentinel 异常证据包\n\n")
	renderPairs(&b, "告警指标", e.AlertMetric)
	renderPairs(&b, "Baseline vs Current", e.BaselineCurrent)
	renderPairs(&b, "Burst 时刻证据", e.Burst)
	renderPairs(&b, "当前快照对比", e.CurrentSnapshot)
	renderPairs(&b, "当前 Top SQL 快照", e.TopSQLs)
	renderPairs(&b, "等待事件快照", e.Waits)
	renderPairs(&b, "阻塞链快照", e.Blockers)
	renderPairs(&b, "主因 / 次因", e.RootCauses)
	renderList(&b, "紧急措施", e.Urgent)
	renderList(&b, "根因修复", e.Fixes)
	renderSQLList(&b, "验证 SQL", e.VerifySQL)
	renderList(&b, "回滚方案", e.Rollback)
	return b.String()
}

func buildAlertMetricEvidence(report BurstReport) []EvidencePair {
	tr := report.TriggerEvent
	if tr.Metric == "" {
		return nil
	}
	metric := MetricName(tr.Metric)
	unit := MetricUnit(metric)
	return []EvidencePair{
		{Name: "触发指标", Value: MetricLabel(metric), Note: tr.Timestamp.Format("2006-01-02 15:04:05")},
		{Name: "baseline -> current", Value: formatAlertValues(tr.Baseline, tr.Current, unit), Note: fmt.Sprintf("threshold=%s%s multiplier=%.1fx", formatNum(tr.Threshold), unit, tr.Multiplier)},
		{Name: "采集窗口", Value: fmt.Sprintf("%s ~ %s", report.StartTime.Format("15:04:05"), report.EndTime.Format("15:04:05")), Note: fmt.Sprintf("%.1fs", report.DurationSec)},
	}
}

func buildBaselineCurrentEvidence(report BurstReport) []EvidencePair {
	var rows []EvidencePair
	tr := report.TriggerEvent
	if tr.Metric != "" {
		metric := MetricName(tr.Metric)
		unit := MetricUnit(metric)
		rows = append(rows, EvidencePair{
			Name:  MetricLabel(metric),
			Value: formatAlertValues(tr.Baseline, tr.Current, unit),
			Note:  fmt.Sprintf("threshold=%s%s multiplier=%.1fx", formatNum(tr.Threshold), unit, tr.Multiplier),
		})
	}
	if report.BaselineActive > 0 || report.PeakActive > 0 {
		rows = append(rows, EvidencePair{
			Name:  "活跃会话基线/峰值",
			Value: fmt.Sprintf("%s->%d个", formatNum(report.BaselineActive), report.PeakActive),
			Note:  "burst window",
		})
	}
	for _, m := range []MetricName{MetricConnectionsPct, MetricCacheHitPct, MetricDeadTupleRatio, MetricXIDAgeRatio, MetricWALBytesRate, MetricTempBytesRate, MetricReplicationLag} {
		ms, ok := report.Metrics[string(m)]
		if !ok {
			continue
		}
		unit := MetricUnit(m)
		rows = append(rows, EvidencePair{
			Name:  MetricLabel(m),
			Value: fmt.Sprintf("min=%s%s avg=%s%s max=%s%s", formatNum(ms.Min), unit, formatNum(ms.Avg), unit, formatNum(ms.Max), unit),
			Note:  nonEmpty(ms.Trend, "baseline compare"),
		})
	}
	return rows
}

func buildBurstMetricEvidence(report BurstReport) []EvidencePair {
	order := []MetricName{MetricActiveSessions, MetricLockWaits, MetricLongQueries, MetricIdleInTransaction, MetricConnectionsPct, MetricBlockerCount, MetricDeadlocks, MetricWALBytesRate, MetricCheckpointsReq, MetricTempBytesRate, MetricCacheHitPct, MetricDeadTupleRatio, MetricXIDAgeRatio, MetricReplicationLag}
	var rows []EvidencePair
	for _, m := range order {
		ms, ok := report.Metrics[string(m)]
		if !ok {
			continue
		}
		unit := MetricUnit(m)
		rows = append(rows, EvidencePair{Name: MetricLabel(m), Value: fmt.Sprintf("avg=%s%s max=%s%s", formatNum(ms.Avg), unit, formatNum(ms.Max), unit), Note: nonEmpty(ms.Trend, "snapshot")})
	}
	return rows
}

func buildCurrentSnapshotEvidence(report BurstReport) []EvidencePair {
	var rows []EvidencePair
	if report.PeakActive > 0 || report.BaselineActive > 0 {
		rows = append(rows, EvidencePair{
			Name:  "activesessions",
			Value: fmt.Sprintf("baseline=%s peak=%d", formatNum(report.BaselineActive), report.PeakActive),
			Note:  "当前/突发活跃会话压力",
		})
	}
	if len(report.WaitProfile) > 0 {
		w := report.WaitProfile[0]
		rows = append(rows, EvidencePair{
			Name:  "waits",
			Value: fmt.Sprintf("%s/%s %d sessions", nonEmpty(w.WaitEventType, "-"), nonEmpty(w.WaitEvent, "-"), w.Count),
			Note:  fmt.Sprintf("%.1f%% of sampled waits", w.Percentage),
		})
	}
	if len(report.BlockingChains) > 0 {
		victims := 0
		for _, c := range report.BlockingChains {
			victims += c.VictimCount
		}
		rows = append(rows, EvidencePair{
			Name:  "blocktree",
			Value: fmt.Sprintf("%d chains / %d victims", len(report.BlockingChains), victims),
			Note:  "当前阻塞链快照",
		})
	}
	if len(report.TopSQLs) > 0 {
		top := report.TopSQLs[0]
		rows = append(rows, EvidencePair{
			Name:  "topsql/slowsql",
			Value: fmt.Sprintf("%d captured, top=%s", len(report.TopSQLs), nonEmpty(top.QueryID, "#1")),
			Note:  fmt.Sprintf("active=%d max=%.1fs", top.ActiveCount, top.MaxTimeSec),
		})
	}
	for _, m := range []MetricName{MetricCacheHitPct, MetricConnectionsPct, MetricDeadTupleRatio, MetricXIDAgeRatio, MetricWALBytesRate, MetricCheckpointsReq, MetricTempBytesRate} {
		ms, ok := report.Metrics[string(m)]
		if !ok {
			continue
		}
		rows = append(rows, EvidencePair{
			Name:  "health: " + MetricLabel(m),
			Value: fmt.Sprintf("current/max=%s%s", formatNum(ms.Max), MetricUnit(m)),
			Note:  currentSnapshotNote(m, ms),
		})
	}
	if c := report.Classification; c.Cause != "" && c.Cause != CauseUnknown {
		rows = append(rows, EvidencePair{
			Name:  "当前快照分类",
			Value: c.Cause.String(),
			Note:  fmt.Sprintf("confidence=%.0f%%", c.Confidence*100),
		})
	}
	return rows
}

func buildSentinelTopSQLEvidence(report BurstReport) []EvidencePair {
	if len(report.TopSQLs) == 0 {
		return nil
	}
	tops := append([]SQLProfile(nil), report.TopSQLs...)
	sort.SliceStable(tops, func(i, j int) bool {
		if tops[i].ActiveCount != tops[j].ActiveCount {
			return tops[i].ActiveCount > tops[j].ActiveCount
		}
		return tops[i].MaxTimeSec > tops[j].MaxTimeSec
	})
	var rows []EvidencePair
	for i, sql := range tops {
		if i >= 5 {
			break
		}
		q := strings.Join(strings.Fields(sql.Query), " ")
		if len([]rune(q)) > 120 {
			q = string([]rune(q)[:120]) + "..."
		}
		rows = append(rows, EvidencePair{Name: nonEmpty(sql.QueryID, fmt.Sprintf("#%d", i+1)), Value: fmt.Sprintf("active=%d max=%.1fs mean=%.1fs", sql.ActiveCount, sql.MaxTimeSec, sql.MeanTimeSec), Note: q})
	}
	return rows
}

func buildSentinelWaitEvidence(report BurstReport) []EvidencePair {
	if len(report.WaitProfile) == 0 {
		return nil
	}
	var rows []EvidencePair
	for i, w := range report.WaitProfile {
		if i >= 6 {
			break
		}
		rows = append(rows, EvidencePair{Name: nonEmpty(w.WaitEvent, w.WaitEventType), Value: fmt.Sprintf("%d sessions / %.1f%%", w.Count, w.Percentage), Note: w.WaitEventType})
	}
	return rows
}

func buildBlockerEvidence(report BurstReport) []EvidencePair {
	if len(report.BlockingChains) == 0 {
		return nil
	}
	var rows []EvidencePair
	for i, c := range report.BlockingChains {
		if i >= 5 {
			break
		}
		q := strings.Join(strings.Fields(c.BlockerQuery), " ")
		if len([]rune(q)) > 100 {
			q = string([]rune(q)[:100]) + "..."
		}
		rows = append(rows, EvidencePair{Name: fmt.Sprintf("PID %d", c.BlockerPID), Value: fmt.Sprintf("victims=%d wait=%s", c.VictimCount, c.WaitEvent), Note: q})
	}
	return rows
}

func buildRootCauseEvidence(report BurstReport) []EvidencePair {
	c := report.Classification
	if c.Cause == "" || c.Cause == CauseUnknown {
		return []EvidencePair{{Name: "主因", Value: CauseUnknown.String(), Note: "规则证据不足，需要结合当前快照继续查证"}}
	}
	rows := []EvidencePair{{Name: "主因", Value: c.Cause.String(), Note: fmt.Sprintf("confidence=%.0f%%", c.Confidence*100)}}
	for i, ev := range c.Evidence {
		rows = append(rows, EvidencePair{Name: fmt.Sprintf("证据%d", i+1), Value: ev, Note: "burst classification"})
	}
	if len(report.BlockingChains) > 0 && c.Cause != CauseLockContention {
		rows = append(rows, EvidencePair{Name: "次因", Value: CauseLockContention.String(), Note: "存在阻塞链快照"})
	}
	if len(report.TopSQLs) > 0 && c.Cause != CauseSlowQuery {
		rows = append(rows, EvidencePair{Name: "次因", Value: CauseSlowQuery.String(), Note: "存在 Top SQL 快照"})
	}
	return rows
}

func actionsForCause(cause RootCauseType) (urgent []string, fixes []string) {
	switch cause {
	case CauseLockContention:
		urgent = []string{"定位根阻塞 PID，确认业务影响后终止或让其提交/回滚", "保留阻塞链和 blocker SQL 作为复盘证据"}
		fixes = []string{"统一事务加锁顺序，缩短事务持有锁时间", "为高频更新/查询路径补齐合适索引，减少锁持有范围"}
	case CauseSlowQuery:
		urgent = []string{"从 Top SQL 中确认当前仍在执行的 SQL，必要时限流或终止异常会话", "对 SQL_ID 进入 /sqltune 做计划级分析"}
		fixes = []string{"补索引/改写 SQL/修复统计信息后复测", "把慢 SQL 纳入发布前基准和回归测试"}
	case CauseVacuumLag:
		urgent = []string{"确认是否有长事务阻止 vacuum，必要时结束长事务", "对高 dead tuple 表执行 vacuum/analyze"}
		fixes = []string{"调整 autovacuum 阈值和频率", "治理批量更新/删除模式，降低膨胀"}
	case CauseXIDWraparound:
		urgent = []string{"立即确认 XID age 最高库/表，禁止继续拖延", "必要时安排紧急 VACUUM FREEZE"}
		fixes = []string{"修复 autovacuum/freeze 参数和长事务治理", "建立 XID age 告警阈值"}
	case CauseWALBottleneck:
		urgent = []string{"确认 WAL 写入峰值来源，暂停非业务批量写入", "检查磁盘延迟和 checkpoint 状态"}
		fixes = []string{"优化批量写入节奏，调整 checkpoint/wal_buffers", "评估 WAL 盘性能和归档链路"}
	case CauseIOBottleneck:
		urgent = []string{"确认 IO wait 或临时文件写入来源，优先定位正在溢写磁盘的 SQL", "必要时限流排序/hash/全表扫描类大查询，避免拖垮共享存储"}
		fixes = []string{"为高 IO SQL 补索引或改写，减少全表扫描和大排序", "按业务 SQL 验证 work_mem/temp_file_limit/存储吞吐配置，避免一刀切放大内存"}
	case CauseConnectionStorm:
		urgent = []string{"确认连接来源，限流异常客户端", "清理 idle in transaction / 长时间空闲连接"}
		fixes = []string{"启用连接池并设置连接上限", "修复应用连接泄漏"}
	case CauseReplicationLag:
		urgent = []string{"确认备库 replay/receive 延迟和网络状态", "必要时暂停读流量切到延迟可接受节点"}
		fixes = []string{"优化主备网络和备库 IO", "降低主库 WAL 峰值或调整同步策略"}
	case CauseCheckpointStorm:
		urgent = []string{"确认 checkpoint 请求来源和磁盘写延迟", "降低短时间批量写入"}
		fixes = []string{"调整 checkpoint_segments/checkpoint_timeout 等参数", "优化写入批次和 WAL/checkpoint 配置"}
	default:
		urgent = []string{"结合告警指标、Top SQL、等待事件和阻塞链继续查证，不直接下结论"}
		fixes = []string{"补齐该类告警的规则和指标采集，避免下次只能归类为 unknown"}
	}
	return urgent, fixes
}

func validationForReport(report BurstReport) (verifySQL []string, rollback []string) {
	cause := report.Classification.Cause
	switch cause {
	case CauseLockContention:
		verifySQL = []string{
			`SELECT a.pid, a.usename, a.state, a.wait_event_type, a.wait_event, a.query
FROM pg_stat_activity a
WHERE a.wait_event_type = 'Lock'
ORDER BY a.query_start NULLS LAST;`,
			`SELECT bl.pid AS blocked_pid, ka.pid AS blocker_pid, ka.query AS blocker_query
FROM pg_locks bl
JOIN pg_locks kl ON kl.locktype = bl.locktype
 AND kl.database IS NOT DISTINCT FROM bl.database
 AND kl.relation IS NOT DISTINCT FROM bl.relation
 AND kl.page IS NOT DISTINCT FROM bl.page
 AND kl.tuple IS NOT DISTINCT FROM bl.tuple
 AND kl.transactionid IS NOT DISTINCT FROM bl.transactionid
 AND kl.classid IS NOT DISTINCT FROM bl.classid
 AND kl.objid IS NOT DISTINCT FROM bl.objid
 AND kl.objsubid IS NOT DISTINCT FROM bl.objsubid
JOIN pg_stat_activity ka ON ka.pid = kl.pid
WHERE NOT bl.granted AND kl.granted;`,
		}
		if len(report.BlockingChains) > 0 && report.BlockingChains[0].BlockerPID > 0 {
			pid := report.BlockingChains[0].BlockerPID
			rollback = []string{
				fmt.Sprintf("优先执行 `SELECT pg_cancel_backend(%d);`，确认业务允许后才 `SELECT pg_terminate_backend(%d);`。", pid, pid),
				"回滚应用侧长事务或批处理，保留 blocker SQL 和事务日志用于复盘。",
			}
		} else {
			rollback = []string{"优先 cancel 根阻塞会话，确认业务允许后才 terminate。", "回滚应用侧长事务或批处理，保留 blocker SQL 和事务日志用于复盘。"}
		}
	case CauseSlowQuery:
		tune := "-- Sentinel 未采集到 SQL_ID，先执行 /topsql 或 /slowsql 定位 SQL_ID，再进入 /sqltune"
		explain := "-- Sentinel 未采集到完整 SQL，优先通过 SQL_ID 解析历史 SQL 后复测执行计划"
		if len(report.TopSQLs) > 0 {
			top := report.TopSQLs[0]
			if strings.TrimSpace(top.QueryID) != "" {
				tune = "/sqltune " + strings.TrimSpace(top.QueryID)
			}
			if strings.TrimSpace(top.Query) != "" {
				explain = "EXPLAIN PERFORMANCE " + ensureSemicolon(strings.TrimSpace(top.Query))
			}
		}
		verifySQL = []string{tune, explain}
		rollback = []string{"索引/统计/SQL 改写先在影子库验证；上线后保留 DROP INDEX / 回退 SQL 的脚本。", "若限流或 kill 会话造成业务影响，恢复调用方并按 SQL_ID 复测。"}
	case CauseVacuumLag:
		verifySQL = []string{
			`SELECT relname, n_live_tup, n_dead_tup,
       ROUND(n_dead_tup * 100.0 / NULLIF(n_live_tup + n_dead_tup, 0), 2) AS dead_pct,
       last_vacuum, last_autovacuum, last_analyze, last_autoanalyze
FROM pg_stat_user_tables
ORDER BY n_dead_tup DESC
LIMIT 20;`,
		}
		rollback = []string{"VACUUM/ANALYZE 本身不可逆；若改过 autovacuum 参数，按变更单恢复原值。", "对 VACUUM FULL/重建表类操作必须先准备备份和维护窗口。"}
	case CauseXIDWraparound:
		verifySQL = []string{
			`SELECT datname, age(datfrozenxid) AS xid_age,
       ROUND(age(datfrozenxid) * 100.0 / 2000000000, 2) AS xid_age_pct
FROM pg_database
ORDER BY age(datfrozenxid) DESC;`,
			`SELECT relname, age(relfrozenxid) AS xid_age
FROM pg_class
WHERE relkind IN ('r','t','m')
ORDER BY age(relfrozenxid) DESC
LIMIT 20;`,
		}
		rollback = []string{"VACUUM FREEZE 不做数据回滚；如调整 freeze/autovacuum 参数，保留原配置用于恢复。", "紧急处置期间暂停高风险 DDL/大事务，处理完成后恢复业务节奏。"}
	case CauseWALBottleneck:
		verifySQL = []string{
			`SELECT name, setting, unit
FROM pg_settings
WHERE name IN ('checkpoint_timeout','checkpoint_completion_target','wal_buffers','max_wal_size','archive_mode','synchronous_commit')
ORDER BY name;`,
			`SELECT checkpoints_timed, checkpoints_req, checkpoint_write_time, checkpoint_sync_time
FROM pg_stat_bgwriter;`,
		}
		rollback = []string{"参数调整用 ALTER SYSTEM 前记录旧值；异常时恢复旧值并 reload/restart。", "暂停的批量写入按批次恢复，避免再次形成 WAL 峰值。"}
	case CauseIOBottleneck:
		verifySQL = []string{
			`SELECT datname, temp_files, pg_size_pretty(temp_bytes) AS temp_bytes
FROM pg_stat_database
ORDER BY temp_bytes DESC;`,
			`SELECT pid, usename, application_name, wait_event_type, wait_event, state, query
FROM pg_stat_activity
WHERE wait_event_type = 'IO' OR wait_event ILIKE '%IO%'
ORDER BY query_start NULLS LAST;`,
			`SELECT name, setting, unit
FROM pg_settings
WHERE name IN ('work_mem','temp_file_limit','shared_buffers','effective_cache_size')
ORDER BY name;`,
		}
		rollback = []string{"work_mem/temp_file_limit 等参数调整必须记录旧值；异常时恢复原值并 reload。", "限流或取消大查询后，按 SQL_ID 复测计划，确认不会再次触发 IO wait。"}
	case CauseConnectionStorm:
		verifySQL = []string{
			`SELECT usename, application_name, client_addr, state, COUNT(*) AS sessions
FROM pg_stat_activity
GROUP BY usename, application_name, client_addr, state
ORDER BY sessions DESC;`,
			`SHOW max_connections;`,
		}
		rollback = []string{"连接池限流先按应用维度灰度；如误伤业务，恢复原连接池上限。", "终止连接前确认来源，优先 cancel 空闲事务，再 terminate 异常连接。"}
	case CauseReplicationLag:
		verifySQL = []string{
			`SELECT application_name, client_addr, state, sync_state,
       sent_location, write_location, flush_location, replay_location
FROM pg_stat_replication;`,
		}
		rollback = []string{"读流量切换后保留原路由；延迟恢复后按灰度切回。", "同步策略参数变更需记录旧值，异常时恢复。"}
	case CauseCheckpointStorm:
		verifySQL = []string{
			`SELECT checkpoints_timed, checkpoints_req, buffers_checkpoint, checkpoint_write_time, checkpoint_sync_time
FROM pg_stat_bgwriter;`,
			`SELECT name, setting, unit
FROM pg_settings
WHERE name IN ('checkpoint_timeout','checkpoint_completion_target','max_wal_size','bgwriter_delay')
ORDER BY name;`,
		}
		rollback = []string{"checkpoint/WAL 参数调整需保留旧值；写放大或恢复变慢时回退。", "批量写入节奏调整按任务批次恢复。"}
	default:
		verifySQL = []string{
			`/health`,
			`/activesessions`,
			`/waits`,
			`/blocktree`,
			`/topsql`,
			`/slowsql`,
		}
		rollback = []string{"未形成确定根因前不做破坏性操作；只允许采集证据、限流或 cancel 可确认的异常会话。"}
	}
	return verifySQL, rollback
}

func renderPairs(b *strings.Builder, title string, rows []EvidencePair) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("  " + title + ":\n")
	for _, r := range rows {
		line := fmt.Sprintf("    - %s: %s", r.Name, r.Value)
		if strings.TrimSpace(r.Note) != "" {
			line += " (" + r.Note + ")"
		}
		b.WriteString(line + "\n")
	}
	b.WriteByte('\n')
}

func renderList(b *strings.Builder, title string, rows []string) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("  " + title + ":\n")
	for _, r := range rows {
		if strings.TrimSpace(r) == "" {
			continue
		}
		b.WriteString("    - " + strings.TrimSpace(r) + "\n")
	}
	b.WriteByte('\n')
}

func renderSQLList(b *strings.Builder, title string, rows []string) {
	if len(rows) == 0 {
		return
	}
	b.WriteString("  " + title + ":\n")
	for _, r := range rows {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		for _, line := range strings.Split(r, "\n") {
			b.WriteString("    " + line + "\n")
		}
		b.WriteByte('\n')
	}
}

func currentSnapshotNote(metric MetricName, ms MetricSummary) string {
	switch metric {
	case MetricCacheHitPct:
		if ms.Max > 0 && ms.Max < 80 {
			return "缓存命中偏低，需结合物理读/Top SQL 判断"
		}
	case MetricConnectionsPct:
		if ms.Max >= 80 {
			return "连接使用率接近上限"
		}
	case MetricDeadTupleRatio:
		if ms.Max >= 20 {
			return "dead tuples 风险偏高"
		}
	case MetricXIDAgeRatio:
		if ms.Max >= 70 {
			return "XID 年龄进入高风险区"
		}
	case MetricWALBytesRate:
		if ms.Max > 0 {
			return "WAL 写入峰值，需结合 checkpoint/归档"
		}
	case MetricCheckpointsReq:
		if ms.Max > 0 {
			return "请求 checkpoint 增多"
		}
	case MetricTempBytesRate:
		if ms.Max > 0 {
			return "临时空间写入，需检查排序/hash/大查询"
		}
	}
	return nonEmpty(ms.Trend, "current snapshot")
}

func ensureSemicolon(sql string) string {
	if strings.HasSuffix(strings.TrimSpace(sql), ";") {
		return strings.TrimSpace(sql)
	}
	return strings.TrimSpace(sql) + ";"
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
