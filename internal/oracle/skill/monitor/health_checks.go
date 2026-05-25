/*-------------------------------------------------------------------------
 *
 * health_checks.go
 *	  Individual health-check probes used by the Oracle /health
 *	  skill — instance up, archiver state, alert-log scrape, top
 *	  wait events, dead-tuple bloat, etc. Each returns a struct
 *	  the renderer maps to a section row.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/health_checks.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
)

// ── SQL constants ─────────────────────────────────────────────

const (
	sqlInstanceUptime = `SELECT ROUND((SYSDATE - startup_time) * 1440) FROM v$instance`

	sqlDatabaseRole = `SELECT database_role FROM v$database`

	sqlArchiveMode = `SELECT log_mode FROM v$database`

	sqlTablespace = `SELECT m.tablespace_name, ROUND(m.used_percent,1)
FROM dba_tablespace_usage_metrics m
JOIN dba_tablespaces t ON m.tablespace_name = t.tablespace_name
WHERE t.contents NOT IN ('TEMPORARY','UNDO')
ORDER BY m.used_percent DESC`

	sqlTempSpace = `SELECT m.tablespace_name, ROUND(m.used_percent,1)
FROM dba_tablespace_usage_metrics m
JOIN dba_tablespaces t ON m.tablespace_name = t.tablespace_name
WHERE t.contents = 'TEMPORARY'
ORDER BY m.used_percent DESC`

	sqlUndoSpace = `SELECT m.tablespace_name, ROUND(m.used_percent,1)
FROM dba_tablespace_usage_metrics m
JOIN dba_tablespaces t ON m.tablespace_name = t.tablespace_name
WHERE t.contents = 'UNDO'
ORDER BY m.used_percent DESC`

	sqlFRA = `SELECT SUM(percent_space_used) FROM v$flash_recovery_area_usage`

	sqlASM = `SELECT name, ROUND((total_mb-free_mb)*100/NULLIF(total_mb,0),1) FROM v$asm_diskgroup`

	sqlConnections = `SELECT
    (SELECT COUNT(*) FROM v$session),
    (SELECT value FROM v$parameter WHERE name='processes')
FROM dual`

	sqlActiveSessions = `SELECT COUNT(*) FROM v$session WHERE status='ACTIVE' AND username IS NOT NULL`

	sqlWaitEvents = `SELECT event, ROUND(time_waited_micro/1e6,1)
FROM v$system_event
WHERE wait_class != 'Idle'
ORDER BY time_waited_micro DESC
FETCH FIRST 1 ROWS ONLY`

	sqlSlowSQL = `SELECT COUNT(*) FROM v$sql WHERE elapsed_time/NULLIF(executions,0)/1e6 > 5`

	sqlBufferHit = `SELECT ROUND((1 - (SUM(DECODE(name,'physical reads',value,0)) /
NULLIF(SUM(DECODE(name,'db block gets',value,0)) + SUM(DECODE(name,'consistent gets',value,0)),0))) * 100, 1)
FROM v$sysstat
WHERE name IN ('db block gets','consistent gets','physical reads')`

	sqlLibraryHit = `SELECT ROUND(SUM(pins-reloads)*100/NULLIF(SUM(pins),0),1) FROM v$librarycache`

	sqlPGAAllocated = `SELECT ROUND(value/1048576) FROM v$pgastat WHERE name='total PGA allocated'`
	sqlPGATarget    = `SELECT ROUND(value/1048576) FROM v$pgastat WHERE name='aggregate PGA target parameter'`

	sqlSGA = `SELECT ROUND(SUM(value)/1048576) FROM v$sga`

	sqlSharedPoolFree = `SELECT ROUND(SUM(DECODE(name,'free memory',bytes,0))*100/NULLIF(SUM(bytes),0),1)
FROM v$sgastat
WHERE pool='shared pool'`

	sqlRedoSwitch = `SELECT COUNT(*) FROM v$log_history WHERE first_time > SYSDATE - 1/24`

	sqlAlertLog = `SELECT COUNT(*) FROM v$diag_alert_ext
WHERE message_level <= 8
AND originating_timestamp > SYSDATE - 1`

	sqlBackup = `SELECT status FROM v$rman_backup_job_details
ORDER BY start_time DESC
FETCH FIRST 1 ROWS ONLY`

	sqlStaleStats = `SELECT ROUND(COUNT(CASE WHEN stale_stats='YES' THEN 1 END)*100/NULLIF(COUNT(*),0),1)
FROM dba_tab_statistics
WHERE owner NOT IN ('SYS','SYSTEM','MDSYS','CTXSYS','XDB','WMSYS','ORDSYS','OLAPSYS','DBSNMP','GSMADMIN_INTERNAL','APPQOSSYS','LBACSYS','OUTLN')`

	sqlInvalidObjects = `SELECT COUNT(*) FROM dba_objects
WHERE status='INVALID' AND owner NOT IN ('SYS','SYSTEM','PUBLIC')`

	sqlResourceLimit = `SELECT resource_name,
       CASE WHEN REGEXP_LIKE(limit_value, '^\d+$') AND TO_NUMBER(limit_value) > 0
            THEN ROUND(max_utilization*100/TO_NUMBER(limit_value),1)
            ELSE 0 END AS pct
FROM v$resource_limit
WHERE limit_value != 'UNLIMITED' AND limit_value != ' UNLIMITED' AND max_utilization > 0
ORDER BY 2 DESC
FETCH FIRST 1 ROWS ONLY`

	sqlPasswordExpiry = `SELECT username, ROUND(expiry_date - SYSDATE)
FROM dba_users
WHERE account_status='OPEN' AND expiry_date IS NOT NULL
  AND expiry_date > SYSDATE AND expiry_date - SYSDATE < 30
ORDER BY expiry_date`
)

// ── Group 1: 基础 (3) ────────────────────────────────────────

// checkInstance verifies the database instance is running and reports uptime.
func (h *HealthSkill) checkInstance(ctx context.Context, info db.ServerInfo) checkResult {
	result, err := h.driver.Query(ctx, sqlInstanceUptime)
	if err != nil || len(result.Rows) == 0 {
		return checkResult{name: "实例状态", status: statusOK, message: "UP"}
	}

	minutes := toFloat64(result.Rows[0][0])
	msg := formatUptime(minutes)
	return checkResult{name: "实例状态", status: statusOK, message: msg}
}

// formatUptime converts minutes to a human-readable uptime string.
func formatUptime(minutes float64) string {
	if minutes < 60 {
		return fmt.Sprintf("UP %.0f 分钟", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("UP %.1f 小时", hours)
	}
	days := hours / 24
	return fmt.Sprintf("UP %.1f 天", days)
}

// checkDatabaseRole reports the database role (PRIMARY/STANDBY).
func (h *HealthSkill) checkDatabaseRole(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlDatabaseRole)
	if err != nil {
		return checkResult{name: "数据库角色", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "数据库角色", status: statusFail, message: "无法获取"}
	}

	role := fmt.Sprintf("%v", result.Rows[0][0])
	return checkResult{name: "数据库角色", status: statusOK, message: role}
}

// checkArchiveMode checks whether the database is in ARCHIVELOG mode.
func (h *HealthSkill) checkArchiveMode(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlArchiveMode)
	if err != nil {
		return checkResult{name: "归档模式", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "归档模式", status: statusFail, message: "无法获取"}
	}

	mode := strings.TrimSpace(fmt.Sprintf("%v", result.Rows[0][0]))
	if strings.EqualFold(mode, "ARCHIVELOG") {
		return checkResult{name: "归档模式", status: statusOK, message: mode}
	}
	return checkResult{name: "归档模式", status: statusWarn, message: mode}
}

// ── Group 2: 空间 (5) ────────────────────────────────────────

// checkTablespace checks non-temporary, non-undo tablespace usage.
func (h *HealthSkill) checkTablespace(ctx context.Context) checkResult {
	return h.checkSpaceGeneric(ctx, "表空间", sqlTablespace, 85, 95)
}

// checkTempSpace checks temporary tablespace usage.
func (h *HealthSkill) checkTempSpace(ctx context.Context) checkResult {
	return h.checkSpaceGeneric(ctx, "临时表空间", sqlTempSpace, 80, 95)
}

// checkUndoSpace checks undo tablespace usage.
func (h *HealthSkill) checkUndoSpace(ctx context.Context) checkResult {
	return h.checkSpaceGeneric(ctx, "Undo表空间", sqlUndoSpace, 80, 95)
}

// checkSpaceGeneric is a shared implementation for tablespace-type checks.
func (h *HealthSkill) checkSpaceGeneric(ctx context.Context, name, sql string, warnPct, failPct float64) checkResult {
	result, err := h.driver.Query(ctx, sql)
	if err != nil {
		return checkResult{name: name, status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 {
		return checkResult{name: name, status: statusOK, message: "无数据"}
	}

	worst := statusOK
	var highCount int
	var highestName string
	var highestPct float64

	for _, row := range result.Rows {
		if len(row) < 2 {
			continue
		}
		tsName := fmt.Sprintf("%v", row[0])
		pct := toFloat64(row[1])

		// Track highest for message.
		if pct > highestPct {
			highestPct = pct
			highestName = tsName
		}

		switch {
		case pct > failPct:
			worst = statusFail
			highCount++
		case pct > warnPct:
			if worst < statusWarn {
				worst = statusWarn
			}
			highCount++
		}
	}

	if worst == statusOK {
		return checkResult{name: name, status: statusOK, message: fmt.Sprintf("最高 %s %.1f%%", highestName, highestPct)}
	}
	return checkResult{
		name:    name,
		status:  worst,
		message: fmt.Sprintf("最高 %s %.1f%%, %d个超阈值", highestName, highestPct, highCount),
	}
}

// checkFRA checks Flash Recovery Area usage.
func (h *HealthSkill) checkFRA(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlFRA)
	if err != nil {
		// FRA not configured is common; treat as OK.
		return checkResult{name: "FRA使用率", status: statusOK, message: "未配置"}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == nil {
		return checkResult{name: "FRA使用率", status: statusOK, message: "未配置"}
	}

	pct := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.1f%%", pct)

	switch {
	case pct > 95:
		return checkResult{name: "FRA使用率", status: statusFail, message: msg}
	case pct > 80:
		return checkResult{name: "FRA使用率", status: statusWarn, message: msg}
	default:
		return checkResult{name: "FRA使用率", status: statusOK, message: msg}
	}
}

// checkASM checks ASM disk group usage.
func (h *HealthSkill) checkASM(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlASM)
	if err != nil {
		return checkResult{name: "ASM磁盘组", status: statusOK, message: "未使用ASM"}
	}
	if len(result.Rows) == 0 {
		return checkResult{name: "ASM磁盘组", status: statusOK, message: "未使用ASM"}
	}

	worst := statusOK
	var highestName string
	var highestPct float64

	for _, row := range result.Rows {
		if len(row) < 2 {
			continue
		}
		dgName := fmt.Sprintf("%v", row[0])
		pct := toFloat64(row[1])

		if pct > highestPct {
			highestPct = pct
			highestName = dgName
		}
		switch {
		case pct > 95:
			worst = statusFail
		case pct > 80:
			if worst < statusWarn {
				worst = statusWarn
			}
		}
	}

	msg := fmt.Sprintf("最高 %s %.1f%%", highestName, highestPct)
	return checkResult{name: "ASM磁盘组", status: worst, message: msg}
}

// ── Group 3: 会话/性能 (6) ───────────────────────────────────

// checkConnections compares session count against the processes limit.
func (h *HealthSkill) checkConnections(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlConnections)
	if err != nil {
		return checkResult{name: "连接数", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) < 2 {
		return checkResult{name: "连接数", status: statusFail, message: "无法获取"}
	}

	current := toFloat64(result.Rows[0][0])
	maxProc := toFloat64(result.Rows[0][1])
	if maxProc == 0 {
		return checkResult{name: "连接数", status: statusWarn, message: "max processes = 0"}
	}

	pct := (current / maxProc) * 100
	msg := fmt.Sprintf("%.0f / %.0f (%.0f%%)", current, maxProc, pct)

	switch {
	case pct > 95:
		return checkResult{name: "连接数", status: statusFail, message: msg}
	case pct > 80:
		return checkResult{name: "连接数", status: statusWarn, message: msg}
	default:
		return checkResult{name: "连接数", status: statusOK, message: msg}
	}
}

// checkActiveSessions counts active user sessions.
func (h *HealthSkill) checkActiveSessions(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlActiveSessions)
	if err != nil {
		return checkResult{name: "活跃会话", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "活跃会话", status: statusOK, message: "0"}
	}

	cnt := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.0f", cnt)

	switch {
	case cnt > 200:
		return checkResult{name: "活跃会话", status: statusFail, message: msg}
	case cnt > 50:
		return checkResult{name: "活跃会话", status: statusWarn, message: msg}
	default:
		return checkResult{name: "活跃会话", status: statusOK, message: msg}
	}
}

// checkWaitEvents reports the top non-idle wait event.
func (h *HealthSkill) checkWaitEvents(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlWaitEvents)
	if err != nil {
		return checkResult{name: "等待事件", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) < 2 {
		return checkResult{name: "等待事件", status: statusOK, message: "无异常"}
	}

	event := fmt.Sprintf("%v", result.Rows[0][0])
	timeSec := toFloat64(result.Rows[0][1])
	msg := fmt.Sprintf("Top1: %s %.1fs", event, timeSec)

	switch {
	case timeSec > 60:
		return checkResult{name: "等待事件", status: statusFail, message: msg}
	case timeSec > 10:
		return checkResult{name: "等待事件", status: statusWarn, message: msg}
	default:
		return checkResult{name: "等待事件", status: statusOK, message: msg}
	}
}

// checkSlowSQL counts SQL statements with average elapsed time > 5s.
func (h *HealthSkill) checkSlowSQL(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlSlowSQL)
	if err != nil {
		return checkResult{name: "慢SQL", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "慢SQL", status: statusOK, message: "0 条平均>5s"}
	}

	cnt := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.0f 条平均>5s", cnt)

	switch {
	case cnt > 10:
		return checkResult{name: "慢SQL", status: statusFail, message: msg}
	case cnt >= 1:
		return checkResult{name: "慢SQL", status: statusWarn, message: msg}
	default:
		return checkResult{name: "慢SQL", status: statusOK, message: msg}
	}
}

// checkBufferHit reports the buffer cache hit ratio.
func (h *HealthSkill) checkBufferHit(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlBufferHit)
	if err != nil {
		return checkResult{name: "Buffer Hit", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == nil {
		return checkResult{name: "Buffer Hit", status: statusWarn, message: "无数据"}
	}

	pct := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.1f%%", pct)

	switch {
	case pct < 90:
		return checkResult{name: "Buffer Hit", status: statusFail, message: msg}
	case pct < 95:
		return checkResult{name: "Buffer Hit", status: statusWarn, message: msg}
	default:
		return checkResult{name: "Buffer Hit", status: statusOK, message: msg}
	}
}

// checkLibraryHit reports the library cache hit ratio.
func (h *HealthSkill) checkLibraryHit(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlLibraryHit)
	if err != nil {
		return checkResult{name: "Library Hit", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == nil {
		return checkResult{name: "Library Hit", status: statusWarn, message: "无数据"}
	}

	pct := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.1f%%", pct)

	switch {
	case pct < 90:
		return checkResult{name: "Library Hit", status: statusFail, message: msg}
	case pct < 95:
		return checkResult{name: "Library Hit", status: statusWarn, message: msg}
	default:
		return checkResult{name: "Library Hit", status: statusOK, message: msg}
	}
}

// ── Group 4: 内存 (3) ────────────────────────────────────────

// checkPGA reports PGA usage ratio.
func (h *HealthSkill) checkPGA(ctx context.Context) checkResult {
	allocResult, err := h.driver.Query(ctx, sqlPGAAllocated)
	if err != nil {
		return checkResult{name: "PGA使用率", status: statusFail, message: err.Error()}
	}
	targetResult, err := h.driver.Query(ctx, sqlPGATarget)
	if err != nil {
		return checkResult{name: "PGA使用率", status: statusFail, message: err.Error()}
	}

	if len(allocResult.Rows) == 0 || len(allocResult.Rows[0]) == 0 ||
		len(targetResult.Rows) == 0 || len(targetResult.Rows[0]) == 0 {
		return checkResult{name: "PGA使用率", status: statusWarn, message: "无数据"}
	}

	allocMB := toFloat64(allocResult.Rows[0][0])
	targetMB := toFloat64(targetResult.Rows[0][0])

	if targetMB == 0 {
		return checkResult{name: "PGA使用率", status: statusOK, message: fmt.Sprintf("%.0f MB (auto)", allocMB)}
	}

	pct := (allocMB / targetMB) * 100
	msg := fmt.Sprintf("%.0f / %.0f MB (%.0f%%)", allocMB, targetMB, pct)

	switch {
	case pct > 95:
		return checkResult{name: "PGA使用率", status: statusFail, message: msg}
	case pct > 80:
		return checkResult{name: "PGA使用率", status: statusWarn, message: msg}
	default:
		return checkResult{name: "PGA使用率", status: statusOK, message: msg}
	}
}

// checkSGA reports total SGA size (informational).
func (h *HealthSkill) checkSGA(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlSGA)
	if err != nil {
		return checkResult{name: "SGA概览", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "SGA概览", status: statusWarn, message: "无数据"}
	}

	mb := toFloat64(result.Rows[0][0])
	return checkResult{name: "SGA概览", status: statusOK, message: fmt.Sprintf("%.0f MB", mb)}
}

// checkSharedPoolFree reports the shared pool free memory percentage.
func (h *HealthSkill) checkSharedPoolFree(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlSharedPoolFree)
	if err != nil {
		return checkResult{name: "Shared Pool", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == nil {
		return checkResult{name: "Shared Pool", status: statusWarn, message: "无数据"}
	}

	freePct := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("空闲 %.1f%%", freePct)

	switch {
	case freePct < 5:
		return checkResult{name: "Shared Pool", status: statusFail, message: msg}
	case freePct < 15:
		return checkResult{name: "Shared Pool", status: statusWarn, message: msg}
	default:
		return checkResult{name: "Shared Pool", status: statusOK, message: msg}
	}
}

// ── Group 5: 日志 (2) ────────────────────────────────────────

// checkRedoSwitch counts redo log switches in the last hour.
func (h *HealthSkill) checkRedoSwitch(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlRedoSwitch)
	if err != nil {
		return checkResult{name: "Redo切换", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "Redo切换", status: statusOK, message: "最近1h 0次"}
	}

	cnt := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("最近1h %.0f次", cnt)

	switch {
	case cnt > 30:
		return checkResult{name: "Redo切换", status: statusFail, message: msg}
	case cnt > 10:
		return checkResult{name: "Redo切换", status: statusWarn, message: msg}
	default:
		return checkResult{name: "Redo切换", status: statusOK, message: msg}
	}
}

// checkAlertLog counts error-level entries in the alert log over the last 24 hours.
func (h *HealthSkill) checkAlertLog(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlAlertLog)
	if err != nil {
		return checkResult{name: "告警日志", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "告警日志", status: statusOK, message: "最近24h无告警"}
	}

	cnt := toFloat64(result.Rows[0][0])
	switch {
	case cnt > 10:
		return checkResult{name: "告警日志", status: statusFail, message: fmt.Sprintf("%.0f 条 (最近24h)", cnt)}
	case cnt > 0:
		return checkResult{name: "告警日志", status: statusWarn, message: fmt.Sprintf("%.0f 条 (最近24h)", cnt)}
	default:
		return checkResult{name: "告警日志", status: statusOK, message: "最近24h无告警"}
	}
}

// ── Group 6: 维护/安全 (5) ───────────────────────────────────

// checkBackup evaluates the most recent RMAN backup status.
func (h *HealthSkill) checkBackup(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlBackup)
	if err != nil {
		return checkResult{name: "备份", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 {
		return checkResult{name: "备份", status: statusWarn, message: "无备份记录"}
	}
	if len(result.Rows[0]) == 0 {
		return checkResult{name: "备份", status: statusFail, message: "数据格式异常"}
	}

	status := strings.TrimSpace(fmt.Sprintf("%v", result.Rows[0][0]))
	switch strings.ToUpper(status) {
	case "COMPLETED":
		return checkResult{name: "备份", status: statusOK, message: "COMPLETED"}
	case "FAILED":
		return checkResult{name: "备份", status: statusFail, message: "FAILED"}
	default:
		return checkResult{name: "备份", status: statusWarn, message: status}
	}
}

// checkStaleStats reports the percentage of tables with stale statistics.
func (h *HealthSkill) checkStaleStats(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlStaleStats)
	if err != nil {
		return checkResult{name: "统计信息", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 || result.Rows[0][0] == nil {
		return checkResult{name: "统计信息", status: statusOK, message: "0% 过期"}
	}

	pct := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.1f%% 过期", pct)

	switch {
	case pct > 30:
		return checkResult{name: "统计信息", status: statusFail, message: msg}
	case pct > 10:
		return checkResult{name: "统计信息", status: statusWarn, message: msg}
	default:
		return checkResult{name: "统计信息", status: statusOK, message: msg}
	}
}

// checkInvalidObjects counts invalid objects outside system schemas.
func (h *HealthSkill) checkInvalidObjects(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlInvalidObjects)
	if err != nil {
		return checkResult{name: "无效对象", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) == 0 {
		return checkResult{name: "无效对象", status: statusOK, message: "0"}
	}

	cnt := toFloat64(result.Rows[0][0])
	msg := fmt.Sprintf("%.0f 个", cnt)

	switch {
	case cnt > 10:
		return checkResult{name: "无效对象", status: statusFail, message: msg}
	case cnt >= 1:
		return checkResult{name: "无效对象", status: statusWarn, message: msg}
	default:
		return checkResult{name: "无效对象", status: statusOK, message: "0"}
	}
}

// checkResourceLimit reports the highest resource utilization percentage.
func (h *HealthSkill) checkResourceLimit(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlResourceLimit)
	if err != nil {
		return checkResult{name: "资源限制", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 || len(result.Rows[0]) < 2 {
		return checkResult{name: "资源限制", status: statusOK, message: "无超限"}
	}

	resName := fmt.Sprintf("%v", result.Rows[0][0])
	pct := toFloat64(result.Rows[0][1])
	msg := fmt.Sprintf("%s %.1f%%", resName, pct)

	switch {
	case pct > 95:
		return checkResult{name: "资源限制", status: statusFail, message: msg}
	case pct > 80:
		return checkResult{name: "资源限制", status: statusWarn, message: msg}
	default:
		return checkResult{name: "资源限制", status: statusOK, message: msg}
	}
}

// checkPasswordExpiry reports users with passwords expiring within 30 days.
func (h *HealthSkill) checkPasswordExpiry(ctx context.Context) checkResult {
	result, err := h.driver.Query(ctx, sqlPasswordExpiry)
	if err != nil {
		return checkResult{name: "密码过期", status: statusFail, message: err.Error()}
	}
	if len(result.Rows) == 0 {
		return checkResult{name: "密码过期", status: statusOK, message: "无即将过期"}
	}

	// Find the minimum days remaining.
	minDays := float64(999)
	var userCount int
	var soonestUser string

	for _, row := range result.Rows {
		if len(row) < 2 {
			continue
		}
		userCount++
		days := toFloat64(row[1])
		if days < minDays {
			minDays = days
			soonestUser = fmt.Sprintf("%v", row[0])
		}
	}

	if userCount == 0 {
		return checkResult{name: "密码过期", status: statusOK, message: "无即将过期"}
	}

	msg := fmt.Sprintf("%d 用户, 最近 %s %.0f天", userCount, soonestUser, minDays)

	if minDays < 7 {
		return checkResult{name: "密码过期", status: statusFail, message: msg}
	}
	return checkResult{name: "密码过期", status: statusWarn, message: msg}
}
