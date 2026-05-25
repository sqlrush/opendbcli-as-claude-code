/*-------------------------------------------------------------------------
 *
 * remediation.go
 *	  GetRemediation returns pre-built investigation and fix templates
 *	  for the given root cause type. These are deterministic — no AI
 *	  needed.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/remediation.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

// GetRemediation returns pre-built investigation and fix templates
// for the given root cause type. These are deterministic — no AI needed.
func GetRemediation(cause RootCauseType) Remediation {
	switch cause {
	case CauseBadSQL:
		return remediationBadSQL()
	case CauseIOSubsystem:
		return remediationIOSubsystem()
	case CauseLatchStorm:
		return remediationLatchStorm()
	case CauseRedoBottleneck:
		return remediationRedoBottleneck()
	case CauseLockContention:
		return remediationLockContention()
	case CauseTrafficStorm:
		return remediationTrafficStorm()
	case CausePlanDrift:
		return remediationPlanDrift()
	case CauseMemoryPressure:
		return remediationMemoryPressure()
	case CauseConnectionExhaust:
		return remediationConnectionExhaust()
	case CauseArchiveDelay:
		return remediationArchiveDelay()
	case CauseDGSyncDelay:
		return remediationDGSyncDelay()
	case CauseUndoPressure:
		return remediationUndoPressure()
	case CauseTempPressure:
		return remediationTempPressure()
	case CauseDatabaseHang:
		return remediationDatabaseHang()
	default:
		return Remediation{
			Suggestions: []string{"无法确定根因, 建议人工分析 AWR 报告"},
		}
	}
}

func remediationBadSQL() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT sql_id, plan_hash_value, executions, elapsed_time/1e6 elapsed_sec, " +
				"buffer_gets, disk_reads FROM v$sql WHERE sql_id = :sql_id ORDER BY elapsed_time DESC",
			"SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(:sql_id, :child_number, 'ALLSTATS LAST'))",
			"SELECT snap_id, plan_hash_value, executions_delta, elapsed_time_delta/1e6 " +
				"FROM dba_hist_sqlstat WHERE sql_id = :sql_id ORDER BY snap_id DESC FETCH FIRST 20 ROWS ONLY",
		},
		Suggestions: []string{
			"检查执行计划是否发生变化 (plan_hash_value)",
			"确认统计信息是否过期: EXEC DBMS_STATS.GATHER_TABLE_STATS",
			"考虑使用 SQL Plan Baseline 锁定好的执行计划",
			"检查是否缺少索引或索引失效",
			"如果是临时紧急情况, 可用 DBMS_RESOURCE_MANAGER 限制该SQL并发",
		},
		Parameters: []string{
			"optimizer_index_cost_adj",
			"optimizer_dynamic_sampling",
			"cursor_sharing",
		},
	}
}

func remediationIOSubsystem() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT event, total_waits, time_waited_micro/1e6 time_sec " +
				"FROM v$system_event WHERE wait_class IN ('User I/O','System I/O') " +
				"ORDER BY time_waited_micro DESC FETCH FIRST 10 ROWS ONLY",
			"SELECT name, phyrds, phywrts, readtim, writetim " +
				"FROM v$filestat f JOIN v$datafile d ON f.file#=d.file# ORDER BY readtim DESC",
			"SELECT metric_name, value FROM v$sysmetric " +
				"WHERE metric_name LIKE '%I/O%' AND group_id = 2",
		},
		Suggestions: []string{
			"检查存储子系统延迟 (OS 层 iostat/sar)",
			"确认是否有大量全表扫描导致物理读飙升",
			"检查 ASM 或文件系统是否存在热点磁盘",
			"评估是否需要增大 db_cache_size 减少物理读",
			"检查是否有备份/RMAN任务正在消耗I/O带宽",
		},
		Parameters: []string{
			"db_cache_size",
			"db_file_multiblock_read_count",
			"filesystemio_options",
			"disk_asynch_io",
		},
	}
}

func remediationLatchStorm() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT addr, name, gets, misses, sleeps, immediate_gets, immediate_misses " +
				"FROM v$latch ORDER BY sleeps DESC FETCH FIRST 10 ROWS ONLY",
			"SELECT namespace, gets, gethits, pins, pinhits, reloads, invalidations " +
				"FROM v$librarycache",
			"SELECT sql_id, parse_calls, executions, version_count " +
				"FROM v$sql WHERE version_count > 10 ORDER BY version_count DESC FETCH FIRST 10 ROWS ONLY",
		},
		Suggestions: []string{
			"检查是否存在硬解析冲高 (大量 literal SQL, 未使用绑定变量)",
			"如果 version_count 过高, 考虑设置 cursor_sharing=FORCE (临时措施)",
			"检查 shared_pool_size 是否过小导致频繁换出",
			"确认是否有对象频繁失效导致 library cache 争用",
			"检查应用连接池配置, 避免过多并发解析",
		},
		Parameters: []string{
			"shared_pool_size",
			"cursor_sharing",
			"open_cursors",
			"session_cached_cursors",
		},
	}
}

func remediationRedoBottleneck() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT event, total_waits, time_waited_micro/1e6 time_sec " +
				"FROM v$system_event WHERE event LIKE '%log file%' ORDER BY time_waited_micro DESC",
			"SELECT group#, thread#, bytes/1024/1024 size_mb, members, status " +
				"FROM v$log ORDER BY group#",
			"SELECT name, value FROM v$sysstat WHERE name LIKE '%redo%'",
		},
		Suggestions: []string{
			"检查 redo log 文件是否在高速存储上 (SSD/NVMe)",
			"增大 redo log 文件大小, 减少 log switch 频率",
			"增加 redo log group 数量",
			"检查归档进程是否阻塞 (归档目标空间满)",
			"评估 commit 频率是否过高, 应用层是否可以批量提交",
		},
		Parameters: []string{
			"log_buffer",
			"log_checkpoint_interval",
			"archive_lag_target",
			"commit_logging",
			"commit_wait",
		},
	}
}

func remediationLockContention() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT s1.sid blocker_sid, s1.username blocker_user, s1.sql_id blocker_sql, " +
				"s2.sid waiter_sid, s2.username waiter_user, s2.sql_id waiter_sql, " +
				"s2.seconds_in_wait wait_sec " +
				"FROM v$session s1 JOIN v$session s2 ON s1.sid = s2.blocking_session " +
				"WHERE s2.blocking_session IS NOT NULL",
			"SELECT object_id, session_id, oracle_username, locked_mode " +
				"FROM v$locked_object lo JOIN dba_objects o ON lo.object_id = o.object_id",
			"SELECT sid, type, id1, id2, lmode, request, block " +
				"FROM v$lock WHERE block = 1 OR request > 0",
		},
		Suggestions: []string{
			"确认阻塞源头会话正在执行的操作",
			"检查是否有未提交的长事务 (idle in transaction)",
			"评估是否可以 kill 阻塞源头会话: ALTER SYSTEM KILL SESSION 'sid,serial#'",
			"检查应用层是否存在锁升级或锁顺序不一致问题",
			"考虑缩小事务粒度, 减少锁持有时间",
		},
		Parameters: []string{
			"ddl_lock_timeout",
			"distributed_lock_timeout",
		},
	}
}

func remediationTrafficStorm() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT program, machine, COUNT(*) cnt " +
				"FROM v$session WHERE status = 'ACTIVE' AND type = 'USER' " +
				"GROUP BY program, machine ORDER BY cnt DESC",
			"SELECT resource_name, current_utilization, max_utilization, limit_value " +
				"FROM v$resource_limit WHERE resource_name IN ('sessions','processes')",
			"SELECT sample_time, session_count " +
				"FROM v$active_session_history " +
				"WHERE sample_time > SYSDATE - INTERVAL '10' MINUTE " +
				"GROUP BY sample_time ORDER BY sample_time",
		},
		Suggestions: []string{
			"按 program/machine 定位流量来源",
			"检查应用连接池是否配置合理 (最大连接数)",
			"确认是否有批量任务 / 定时任务集中触发",
			"评估是否需要使用 Oracle Resource Manager 限流",
			"检查 processes/sessions 参数是否需要调整",
		},
		Parameters: []string{
			"processes",
			"sessions",
			"resource_manager_plan",
		},
	}
}

func remediationPlanDrift() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT sql_id, plan_hash_value, timestamp, origin " +
				"FROM dba_sql_plan_baselines WHERE sql_id = :sql_id ORDER BY timestamp DESC",
			"SELECT plan_hash_value, executions, elapsed_time/GREATEST(executions,1)/1e6 avg_elapsed " +
				"FROM v$sql WHERE sql_id = :sql_id ORDER BY avg_elapsed DESC",
			"SELECT * FROM TABLE(DBMS_XPLAN.DISPLAY_CURSOR(:sql_id, NULL, 'ALLSTATS LAST'))",
		},
		Suggestions: []string{
			"对比历史执行计划, 确认是否发生 plan regression",
			"检查统计信息是否过期: EXEC DBMS_STATS.GATHER_TABLE_STATS",
			"使用 SQL Plan Baseline 锁定好的执行计划: DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE",
			"检查是否有自适应计划 (Adaptive Plans) 导致切换",
			"考虑使用 SQL Profile 或 SQL Patch 修正计划",
		},
		Parameters: []string{
			"optimizer_adaptive_plans",
			"optimizer_adaptive_statistics",
			"optimizer_index_cost_adj",
		},
	}
}

func remediationMemoryPressure() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT component, current_size/1024/1024 current_mb, " +
				"min_size/1024/1024 min_mb, max_size/1024/1024 max_mb " +
				"FROM v$sga_dynamic_components ORDER BY current_size DESC",
			"SELECT name, value FROM v$pgastat WHERE name IN " +
				"('total PGA allocated','total PGA inuse','maximum PGA allocated')",
			"SELECT event, total_waits, time_waited_micro/1e6 time_sec " +
				"FROM v$system_event WHERE event IN ('free buffer waits','buffer busy waits') " +
				"ORDER BY time_waited_micro DESC",
		},
		Suggestions: []string{
			"检查 SGA/PGA 自动管理目标是否合理",
			"确认是否有大量排序/Hash Join 溢出到磁盘 (PGA 不足)",
			"检查 buffer cache hit ratio, 低于 95% 需扩容",
			"评估 MEMORY_TARGET / SGA_TARGET 是否需要增大",
			"检查是否有内存泄漏 (PGA 持续增长不释放)",
		},
		Parameters: []string{
			"memory_target",
			"sga_target",
			"pga_aggregate_target",
			"db_cache_size",
		},
	}
}

func remediationConnectionExhaust() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT resource_name, current_utilization, max_utilization, " +
				"DECODE(limit_value,'UNLIMITED',-1,TO_NUMBER(limit_value)) limit_value " +
				"FROM v$resource_limit WHERE resource_name IN ('sessions','processes')",
			"SELECT program, machine, status, COUNT(*) cnt " +
				"FROM v$session WHERE type = 'USER' GROUP BY program, machine, status ORDER BY cnt DESC",
			"SELECT username, COUNT(*) cnt FROM v$session " +
				"WHERE type = 'USER' GROUP BY username ORDER BY cnt DESC",
		},
		Suggestions: []string{
			"确认 sessions/processes 参数是否需要调大",
			"检查应用连接池配置, 是否有连接泄漏",
			"清理 INACTIVE 会话: ALTER SYSTEM KILL SESSION 'sid,serial#'",
			"启用 Oracle Connection Broker / DRCP 减少进程消耗",
			"按 program/machine 定位连接数异常来源",
		},
		Parameters: []string{
			"processes",
			"sessions",
			"resource_limit",
		},
	}
}

func remediationArchiveDelay() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT dest_id, status, error, archived_seq#, applied_seq# " +
				"FROM v$archive_dest WHERE status != 'INACTIVE'",
			"SELECT name, space_limit/1024/1024/1024 limit_gb, " +
				"space_used/1024/1024/1024 used_gb, space_reclaimable/1024/1024/1024 reclaimable_gb " +
				"FROM v$recovery_file_dest",
			"SELECT event, total_waits, time_waited_micro/1e6 time_sec " +
				"FROM v$system_event WHERE event LIKE '%archiv%' OR event LIKE '%log file switch%' " +
				"ORDER BY time_waited_micro DESC",
		},
		Suggestions: []string{
			"检查 FRA 空间使用率, 清理过期归档日志",
			"确认归档目标磁盘是否有足够空间和I/O带宽",
			"检查归档进程 (ARCn) 是否正常运行",
			"增大 FRA 空间: ALTER SYSTEM SET db_recovery_file_dest_size",
			"清理过期备份: RMAN> DELETE OBSOLETE",
		},
		Parameters: []string{
			"db_recovery_file_dest_size",
			"log_archive_dest_1",
			"archive_lag_target",
		},
	}
}

func remediationDGSyncDelay() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT dest_id, status, error, affirm, synchronization_status, " +
				"applied_seq#, archived_seq# FROM v$archive_dest WHERE target = 'STANDBY'",
			"SELECT name, value, datum_time FROM v$dataguard_stats " +
				"WHERE name IN ('transport lag','apply lag','apply finish time')",
			"SELECT event, total_waits, time_waited_micro/1e6 time_sec " +
				"FROM v$system_event WHERE event = 'log file sync' ORDER BY time_waited_micro DESC",
		},
		Suggestions: []string{
			"检查备库 apply lag, 确认 MRP 进程是否正常",
			"检查主备之间网络延迟和带宽",
			"如果是 SYNC 模式导致主库延迟, 考虑临时切为 ASYNC",
			"确认备库存储I/O是否成为瓶颈",
			"检查 log_archive_dest_n 的 NET_TIMEOUT/REOPEN 配置",
		},
		Parameters: []string{
			"log_archive_dest_2",
			"log_archive_dest_state_2",
			"standby_file_management",
		},
	}
}

func remediationUndoPressure() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT tablespace_name, status, SUM(bytes)/1024/1024 size_mb " +
				"FROM dba_undo_extents GROUP BY tablespace_name, status",
			"SELECT TO_CHAR(begin_time,'HH24:MI') begin_time, undoblks, txncount, " +
				"maxquerylen, ssolderrcnt FROM v$undostat " +
				"WHERE begin_time > SYSDATE - INTERVAL '1' HOUR ORDER BY begin_time",
			"SELECT s.sid, s.username, s.sql_id, t.used_ublk * 8/1024 undo_mb " +
				"FROM v$transaction t JOIN v$session s ON t.ses_addr = s.saddr " +
				"ORDER BY t.used_ublk DESC FETCH FIRST 10 ROWS ONLY",
		},
		Suggestions: []string{
			"检查是否有长事务占用大量 Undo 空间",
			"确认 UNDO_RETENTION 设置是否合理",
			"增大 Undo 表空间: ALTER TABLESPACE undotbs1 ADD DATAFILE",
			"检查 ORA-01555 (snapshot too old) 是否频繁发生",
			"评估是否有未提交的长事务需要回滚",
		},
		Parameters: []string{
			"undo_tablespace",
			"undo_retention",
			"undo_management",
		},
	}
}

func remediationTempPressure() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT tablespace_name, tablespace_size/1024/1024 total_mb, " +
				"allocated_space/1024/1024 alloc_mb, free_space/1024/1024 free_mb " +
				"FROM dba_temp_free_space",
			"SELECT s.sid, s.username, s.sql_id, t.blocks * 8/1024 temp_mb " +
				"FROM v$tempseg_usage t JOIN v$session s ON t.session_addr = s.saddr " +
				"ORDER BY t.blocks DESC FETCH FIRST 10 ROWS ONLY",
			"SELECT sql_id, operation_type, actual_mem_used/1024/1024 mem_mb, " +
				"tempseg_size/1024/1024 temp_mb FROM v$sql_workarea_active " +
				"ORDER BY tempseg_size DESC NULLS LAST FETCH FIRST 10 ROWS ONLY",
		},
		Suggestions: []string{
			"按会话定位 TEMP 使用大户, 检查相关 SQL",
			"检查是否有大量排序/Hash Join 溢出到 TEMP",
			"增大 TEMP 表空间: ALTER TABLESPACE temp ADD TEMPFILE",
			"优化 SQL 减少排序和 Hash Join 的内存需求",
			"评估 PGA_AGGREGATE_TARGET 是否过小",
		},
		Parameters: []string{
			"pga_aggregate_target",
			"workarea_size_policy",
			"sort_area_size",
		},
	}
}

func remediationDatabaseHang() Remediation {
	return Remediation{
		InvestigateSQL: []string{
			"SELECT instance_name, status, database_status, active_state, host_name " +
				"FROM v$instance",
			"SELECT name, description, error_count FROM v$bgprocess " +
				"WHERE paddr != '00' ORDER BY name",
			"SELECT resource_name, current_utilization, max_utilization, limit_value " +
				"FROM v$resource_limit WHERE resource_name IN ('sessions','processes','enqueue_resources')",
			"SELECT dest_id, status, error FROM v$archive_dest WHERE status = 'ERROR'",
		},
		Suggestions: []string{
			"检查 v$instance 状态, 确认实例是否正常 OPEN",
			"检查后台进程 (PMON/SMON/DBWn/LGWR) 是否存活",
			"检查 sessions/processes 是否达到上限",
			"检查归档目标是否有空间 (FRA 满可导致 hang)",
			"收集 hanganalyze: ALTER SESSION SET events 'immediate trace name hanganalyze level 3'",
			"收集 systemstate dump: ALTER SESSION SET events 'immediate trace name systemstate level 266'",
		},
		Parameters: []string{
			"processes",
			"sessions",
			"db_recovery_file_dest_size",
		},
	}
}
