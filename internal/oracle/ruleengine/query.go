/*-------------------------------------------------------------------------
 *
 * query.go
 *	  QueryDef maps a QueryID to the skill command or raw SQL used to
 *	  execute it.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/query.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

// QueryDef maps a QueryID to the skill command or raw SQL used to execute it.
type QueryDef struct {
	ID           QueryID
	Description  string
	SkillCommand string // preferred: /waits, /ash, /sql "..."
	RawSQL       string // fallback raw SQL
}

// queryRegistry holds all known query definitions keyed by QueryID.
var queryRegistry = map[QueryID]*QueryDef{

	// ── ASH queries ──────────────────────────────────────────────────────

	QueryASHBlockClass: {
		ID:           QueryASHBlockClass,
		Description:  "ASH buffer busy waits block class breakdown",
		SkillCommand: `/sql "SELECT DECODE(p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block', 4,'segment_header', 5,'save_undo_header', 6,'free_list', 7,'extent_map', 8,'bitmap_block', 9,'bitmap_index_block', 'other('||p3||')') block_class, COUNT(*) cnt, ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) pct FROM v$active_session_history WHERE event='buffer busy waits' AND sample_time > SYSDATE-1/24 GROUP BY p3 ORDER BY cnt DESC"`,
		RawSQL: `SELECT DECODE(p3, 1,'data_block', 2,'sort_block', 3,'save_undo_block',
       4,'segment_header', 5,'save_undo_header', 6,'free_list',
       7,'extent_map', 8,'bitmap_block', 9,'bitmap_index_block',
       'other('||p3||')') block_class,
       COUNT(*) cnt,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) pct
  FROM v$active_session_history
 WHERE event = 'buffer busy waits'
   AND sample_time > SYSDATE - 1/24
 GROUP BY p3
 ORDER BY cnt DESC`,
	},

	QueryASHHotObject: {
		ID:           QueryASHHotObject,
		Description:  "ASH hot object identification",
		SkillCommand: `/sql "SELECT o.owner, o.object_name, o.object_type, COUNT(*) cnt, ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) pct FROM v$active_session_history ash JOIN dba_objects o ON ash.current_obj# = o.object_id WHERE ash.event IN ('buffer busy waits','gc buffer busy acquire','gc buffer busy release') AND ash.sample_time > SYSDATE-1/24 GROUP BY o.owner, o.object_name, o.object_type ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"`,
		RawSQL: `SELECT o.owner, o.object_name, o.object_type,
       COUNT(*) cnt,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) pct
  FROM v$active_session_history ash
  JOIN dba_objects o ON ash.current_obj# = o.object_id
 WHERE ash.event IN ('buffer busy waits', 'gc buffer busy acquire', 'gc buffer busy release')
   AND ash.sample_time > SYSDATE - 1/24
 GROUP BY o.owner, o.object_name, o.object_type
 ORDER BY cnt DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	QueryASHTopSQL: {
		ID:           QueryASHTopSQL,
		Description:  "ASH top SQL by wait time",
		SkillCommand: `/ash`,
		RawSQL: `SELECT sql_id,
       COUNT(*) cnt,
       ROUND(COUNT(*)*100/SUM(COUNT(*)) OVER(), 1) pct,
       MIN(sql_plan_hash_value) plan_hash
  FROM v$active_session_history
 WHERE sample_time > SYSDATE - 1/24
   AND sql_id IS NOT NULL
 GROUP BY sql_id
 ORDER BY cnt DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	QueryASHBlockConcentrate: {
		ID:           QueryASHBlockConcentrate,
		Description:  "ASH block concentration by file# and block#",
		SkillCommand: `/sql "SELECT current_file#, current_block#, COUNT(*) cnt FROM v$active_session_history WHERE event='buffer busy waits' AND sample_time > SYSDATE-1/24 GROUP BY current_file#, current_block# HAVING COUNT(*) > 5 ORDER BY cnt DESC FETCH FIRST 10 ROWS ONLY"`,
		RawSQL: `SELECT current_file#, current_block#, COUNT(*) cnt
  FROM v$active_session_history
 WHERE event = 'buffer busy waits'
   AND sample_time > SYSDATE - 1/24
 GROUP BY current_file#, current_block#
HAVING COUNT(*) > 5
 ORDER BY cnt DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	// ── Index queries ────────────────────────────────────────────────────

	QueryIndex9010Splits: {
		ID:           QueryIndex9010Splits,
		Description:  "Index 90-10 splits from segment statistics",
		SkillCommand: `/sql "SELECT owner, object_name, statistic_name, value FROM v$segment_statistics WHERE statistic_name LIKE '%split%' AND value > 1000 ORDER BY value DESC FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT owner, object_name, statistic_name, value
  FROM v$segment_statistics
 WHERE statistic_name LIKE '%split%'
   AND value > 1000
 ORDER BY value DESC
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Undo queries ─────────────────────────────────────────────────────

	QueryUndoSegmentRatio: {
		ID:           QueryUndoSegmentRatio,
		Description:  "Undo segment contention ratio",
		SkillCommand: `/sql "SELECT SUM(waits) total_waits, SUM(gets) total_gets, ROUND(SUM(waits)/NULLIF(SUM(gets),0)*100, 2) contention_pct FROM v$rollstat"`,
		RawSQL: `SELECT SUM(waits) total_waits,
       SUM(gets) total_gets,
       ROUND(SUM(waits)/NULLIF(SUM(gets), 0)*100, 2) contention_pct
  FROM v$rollstat`,
	},

	QueryUndoStats: {
		ID:           QueryUndoStats,
		Description:  "Undo tablespace usage and retention stats",
		SkillCommand: `/sql "SELECT u.tablespace_name, t.status, ROUND(u.bytes/1024/1024) used_mb, u.status seg_status, (SELECT value FROM v$parameter WHERE name='undo_retention') undo_retention FROM dba_undo_extents u JOIN dba_tablespaces t ON u.tablespace_name = t.tablespace_name WHERE ROWNUM <= 20"`,
		RawSQL: `SELECT u.tablespace_name, t.status,
       ROUND(u.bytes/1024/1024) used_mb, u.status seg_status,
       (SELECT value FROM v$parameter WHERE name = 'undo_retention') undo_retention
  FROM dba_undo_extents u
  JOIN dba_tablespaces t ON u.tablespace_name = t.tablespace_name
 WHERE ROWNUM <= 20`,
	},

	// ── Latch & mutex queries ────────────────────────────────────────────

	QueryLatchStats: {
		ID:           QueryLatchStats,
		Description:  "Top latch contention statistics",
		SkillCommand: `/sql "SELECT name, gets, misses, sleeps, spin_gets, ROUND(misses/NULLIF(gets,0)*100, 2) miss_pct FROM v$latch WHERE misses > 0 ORDER BY sleeps DESC FETCH FIRST 15 ROWS ONLY"`,
		RawSQL: `SELECT name, gets, misses, sleeps, spin_gets,
       ROUND(misses/NULLIF(gets, 0)*100, 2) miss_pct
  FROM v$latch
 WHERE misses > 0
 ORDER BY sleeps DESC
 FETCH FIRST 15 ROWS ONLY`,
	},

	QueryMutexStats: {
		ID:           QueryMutexStats,
		Description:  "Top mutex sleep statistics",
		SkillCommand: `/sql "SELECT mutex_type, location, sleeps, wait_time FROM v$mutex_sleep ORDER BY sleeps DESC FETCH FIRST 15 ROWS ONLY"`,
		RawSQL: `SELECT mutex_type, location, sleeps, wait_time
  FROM v$mutex_sleep
 ORDER BY sleeps DESC
 FETCH FIRST 15 ROWS ONLY`,
	},

	// ── Wait event queries ───────────────────────────────────────────────

	QueryWaitAvgTime: {
		ID:           QueryWaitAvgTime,
		Description:  "Average wait times by event",
		SkillCommand: `/waits`,
		RawSQL: `SELECT event, total_waits, time_waited_micro,
       ROUND(time_waited_micro/NULLIF(total_waits, 0)/1000, 2) avg_wait_ms,
       wait_class
  FROM v$system_event
 WHERE wait_class <> 'Idle'
 ORDER BY time_waited_micro DESC
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Foreign key / index queries ──────────────────────────────────────

	QueryFKNoIndex: {
		ID:           QueryFKNoIndex,
		Description:  "Foreign keys without supporting indexes",
		SkillCommand: `/sql "SELECT c.owner, c.table_name, c.constraint_name, cc.column_name FROM dba_constraints c JOIN dba_cons_columns cc ON c.constraint_name = cc.constraint_name AND c.owner = cc.owner WHERE c.constraint_type = 'R' AND NOT EXISTS (SELECT 1 FROM dba_ind_columns ic WHERE ic.table_owner = cc.owner AND ic.table_name = cc.table_name AND ic.column_name = cc.column_name AND ic.column_position = cc.position) AND c.owner NOT IN ('SYS','SYSTEM') FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT c.owner, c.table_name, c.constraint_name, cc.column_name
  FROM dba_constraints c
  JOIN dba_cons_columns cc
    ON c.constraint_name = cc.constraint_name AND c.owner = cc.owner
 WHERE c.constraint_type = 'R'
   AND NOT EXISTS (
       SELECT 1 FROM dba_ind_columns ic
        WHERE ic.table_owner = cc.owner
          AND ic.table_name = cc.table_name
          AND ic.column_name = cc.column_name
          AND ic.column_position = cc.position)
   AND c.owner NOT IN ('SYS', 'SYSTEM')
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Memory / SGA / PGA queries ───────────────────────────────────────

	QuerySPFreeMemory: {
		ID:           QuerySPFreeMemory,
		Description:  "Shared pool free memory",
		SkillCommand: `/sql "SELECT pool, name, bytes/1024/1024 mb FROM v$sgastat WHERE pool = 'shared pool' AND name = 'free memory'"`,
		RawSQL: `SELECT pool, name, bytes/1024/1024 mb
  FROM v$sgastat
 WHERE pool = 'shared pool'
   AND name = 'free memory'`,
	},

	QueryPGAAdvice: {
		ID:           QueryPGAAdvice,
		Description:  "PGA target advice",
		SkillCommand: `/sql "SELECT pga_target_for_estimate/1024/1024 target_mb, pga_target_factor, estd_extra_bytes_rw, estd_pga_cache_hit_percentage, estd_overalloc_count FROM v$pga_target_advice ORDER BY pga_target_for_estimate"`,
		RawSQL: `SELECT pga_target_for_estimate/1024/1024 target_mb,
       pga_target_factor,
       estd_extra_bytes_rw,
       estd_pga_cache_hit_percentage,
       estd_overalloc_count
  FROM v$pga_target_advice
 ORDER BY pga_target_for_estimate`,
	},

	QueryDBCacheAdvice: {
		ID:           QueryDBCacheAdvice,
		Description:  "DB buffer cache size advice",
		SkillCommand: `/sql "SELECT size_for_estimate mb, size_factor, estd_physical_read_factor, estd_physical_reads FROM v$db_cache_advice WHERE name = 'DEFAULT' ORDER BY size_for_estimate"`,
		RawSQL: `SELECT size_for_estimate mb,
       size_factor,
       estd_physical_read_factor,
       estd_physical_reads
  FROM v$db_cache_advice
 WHERE name = 'DEFAULT'
 ORDER BY size_for_estimate`,
	},

	// ── Redo / archive queries ───────────────────────────────────────────

	QueryRedoLogInfo: {
		ID:           QueryRedoLogInfo,
		Description:  "Redo log group and member information",
		SkillCommand: `/sql "SELECT l.group#, l.members, l.bytes/1024/1024 size_mb, l.status, l.archived, lf.member FROM v$log l JOIN v$logfile lf ON l.group# = lf.group# ORDER BY l.group#"`,
		RawSQL: `SELECT l.group#, l.members, l.bytes/1024/1024 size_mb,
       l.status, l.archived, lf.member
  FROM v$log l
  JOIN v$logfile lf ON l.group# = lf.group#
 ORDER BY l.group#`,
	},

	QueryArchiveStatus: {
		ID:           QueryArchiveStatus,
		Description:  "Archive log destination status",
		SkillCommand: `/sql "SELECT dest_id, dest_name, status, target, archiver, schedule, process FROM v$archive_dest WHERE status <> 'INACTIVE'"`,
		RawSQL: `SELECT dest_id, dest_name, status, target, archiver, schedule, process
  FROM v$archive_dest
 WHERE status <> 'INACTIVE'`,
	},

	// ── Datafile / storage queries ───────────────────────────────────────

	QueryDatafileStatus: {
		ID:           QueryDatafileStatus,
		Description:  "Datafile status and I/O statistics",
		SkillCommand: `/sql "SELECT f.file#, f.name, f.status, s.phyrds, s.phywrts, s.avgiotim FROM v$datafile f JOIN v$filestat s ON f.file# = s.file# ORDER BY s.avgiotim DESC FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT f.file#, f.name, f.status,
       s.phyrds, s.phywrts, s.avgiotim
  FROM v$datafile f
  JOIN v$filestat s ON f.file# = s.file#
 ORDER BY s.avgiotim DESC
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Resource limit queries ───────────────────────────────────────────

	QueryResourceLimit: {
		ID:           QueryResourceLimit,
		Description:  "Database resource limits and current utilization",
		SkillCommand: `/sql "SELECT resource_name, current_utilization, max_utilization, initial_allocation, limit_value FROM v$resource_limit WHERE max_utilization > 0 ORDER BY current_utilization DESC"`,
		RawSQL: `SELECT resource_name, current_utilization, max_utilization,
       initial_allocation, limit_value
  FROM v$resource_limit
 WHERE max_utilization > 0
 ORDER BY current_utilization DESC`,
	},

	// ── Transaction queries ──────────────────────────────────────────────

	QueryLongTransactions: {
		ID:           QueryLongTransactions,
		Description:  "Long-running active transactions",
		SkillCommand: `/sql "SELECT s.sid, s.serial#, s.username, s.sql_id, t.start_time, ROUND((SYSDATE - TO_DATE(t.start_time, 'MM/DD/YY HH24:MI:SS'))*86400) elapsed_sec, t.used_ublk, t.used_urec FROM v$transaction t JOIN v$session s ON t.ses_addr = s.saddr WHERE (SYSDATE - TO_DATE(t.start_time, 'MM/DD/YY HH24:MI:SS'))*86400 > 300 ORDER BY elapsed_sec DESC"`,
		RawSQL: `SELECT s.sid, s.serial#, s.username, s.sql_id, t.start_time,
       ROUND((SYSDATE - TO_DATE(t.start_time, 'MM/DD/YY HH24:MI:SS'))*86400) elapsed_sec,
       t.used_ublk, t.used_urec
  FROM v$transaction t
  JOIN v$session s ON t.ses_addr = s.saddr
 WHERE (SYSDATE - TO_DATE(t.start_time, 'MM/DD/YY HH24:MI:SS'))*86400 > 300
 ORDER BY elapsed_sec DESC`,
	},

	// ── Cursor / parse queries ───────────────────────────────────────────

	QueryCursorStats: {
		ID:           QueryCursorStats,
		Description:  "Session cursor usage and open cursor counts",
		SkillCommand: `/sql "SELECT s.sid, s.username, s.program, oc.cnt open_cursors FROM v$session s JOIN (SELECT sid, COUNT(*) cnt FROM v$open_cursor GROUP BY sid) oc ON s.sid = oc.sid WHERE oc.cnt > 100 ORDER BY oc.cnt DESC FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT s.sid, s.username, s.program, oc.cnt open_cursors
  FROM v$session s
  JOIN (SELECT sid, COUNT(*) cnt FROM v$open_cursor GROUP BY sid) oc
    ON s.sid = oc.sid
 WHERE oc.cnt > 100
 ORDER BY oc.cnt DESC
 FETCH FIRST 20 ROWS ONLY`,
	},

	QueryParseStats: {
		ID:           QueryParseStats,
		Description:  "Parse statistics (hard parse, soft parse, failures)",
		SkillCommand: `/sql "SELECT name, value FROM v$sysstat WHERE name IN ('parse count (total)', 'parse count (hard)', 'parse count (failures)', 'parse time cpu', 'parse time elapsed')"`,
		RawSQL: `SELECT name, value
  FROM v$sysstat
 WHERE name IN ('parse count (total)', 'parse count (hard)',
                'parse count (failures)', 'parse time cpu',
                'parse time elapsed')`,
	},

	// ── Segment queries ──────────────────────────────────────────────────

	QuerySegmentStats: {
		ID:           QuerySegmentStats,
		Description:  "Top segment-level I/O and space statistics",
		SkillCommand: `/sql "SELECT owner, object_name, object_type, statistic_name, value FROM v$segment_statistics WHERE value > 10000 ORDER BY value DESC FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT owner, object_name, object_type, statistic_name, value
  FROM v$segment_statistics
 WHERE value > 10000
 ORDER BY value DESC
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Table queries ────────────────────────────────────────────────────

	QueryTableStats: {
		ID:           QueryTableStats,
		Description:  "Table statistics (rows, blocks, stale stats)",
		SkillCommand: `/sql "SELECT owner, table_name, num_rows, blocks, avg_row_len, last_analyzed, stale_stats FROM dba_tab_statistics WHERE stale_stats = 'YES' AND owner NOT IN ('SYS','SYSTEM') FETCH FIRST 20 ROWS ONLY"`,
		RawSQL: `SELECT owner, table_name, num_rows, blocks, avg_row_len,
       last_analyzed, stale_stats
  FROM dba_tab_statistics
 WHERE stale_stats = 'YES'
   AND owner NOT IN ('SYS', 'SYSTEM')
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── SQL plan queries ─────────────────────────────────────────────────

	QuerySQLPlanHistory: {
		ID:           QuerySQLPlanHistory,
		Description:  "SQL plan change history for a given SQL_ID",
		SkillCommand: `/sql "SELECT sql_id, plan_hash_value, timestamp, origin, creator, optimizer_cost FROM dba_sql_plan_baselines WHERE sql_handle IN (SELECT sql_handle FROM dba_sql_plan_baselines WHERE plan_name IN (SELECT plan_name FROM v$sql WHERE sql_id = '{sql_id}')) ORDER BY timestamp DESC"`,
		RawSQL: `SELECT sql_id, plan_hash_value, timestamp, origin, creator, optimizer_cost
  FROM dba_sql_plan_baselines
 WHERE sql_handle IN (
       SELECT sql_handle FROM dba_sql_plan_baselines
        WHERE plan_name IN (SELECT plan_name FROM v$sql WHERE sql_id = '{sql_id}'))
 ORDER BY timestamp DESC`,
	},

	// ── Interconnect (RAC) queries ───────────────────────────────────────

	QueryInterconnectStats: {
		ID:           QueryInterconnectStats,
		Description:  "RAC interconnect traffic and latency statistics",
		SkillCommand: `/sql "SELECT instance, name, value FROM gv$sysstat WHERE name IN ('gc cr blocks received', 'gc cr blocks served', 'gc current blocks received', 'gc current blocks served', 'gc cr block receive time', 'gc current block receive time') ORDER BY instance, name"`,
		RawSQL: `SELECT instance, name, value
  FROM gv$sysstat
 WHERE name IN ('gc cr blocks received', 'gc cr blocks served',
                'gc current blocks received', 'gc current blocks served',
                'gc cr block receive time', 'gc current block receive time')
 ORDER BY instance, name`,
	},

	// ── Phase 2: Blocker detail / Row cache ─────────────────────────────

	QueryBlockerDetail: {
		ID:          QueryBlockerDetail,
		Description: "Blocker session detail (command, status, event) for blocking chain analysis",
		RawSQL: `SELECT s.sid, s.serial#, s.username, s.status, s.command,
       NVL(s.event, 'ON CPU') event, NVL(s.sql_id, '-') sql_id,
       NVL(s.blocking_session, 0) blocking_session,
       NVL(ROUND((SYSDATE - s.logon_time)*86400), 0) logon_sec
  FROM v$session s
 WHERE s.sid IN (SELECT blocking_session FROM v$session WHERE blocking_session IS NOT NULL)
 ORDER BY s.sid`,
	},

	QueryRowCacheStats: {
		ID:          QueryRowCacheStats,
		Description: "Row cache statistics by cache type (dc_sequences, dc_objects, etc.)",
		RawSQL: `SELECT parameter, gets, getmisses, modifications,
       ROUND(getmisses/NULLIF(gets,0)*100, 2) miss_pct
  FROM v$rowcache
 WHERE gets > 100
 ORDER BY (getmisses + modifications) DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	QuerySequenceCacheInfo: {
		ID:          QuerySequenceCacheInfo,
		Description: "Sequence CACHE settings for hot sequences",
		RawSQL: `SELECT sequence_owner, sequence_name, cache_size, last_number
  FROM dba_sequences
 WHERE sequence_owner NOT IN ('SYS','SYSTEM','AUDSYS','DBSFWUSER')
   AND cache_size < 100
 ORDER BY cache_size
 FETCH FIRST 20 ROWS ONLY`,
	},

	// ── Phase 4: TEMP/UNDO usage ────────────────────────────────────────

	QueryTempUsage: {
		ID:          QueryTempUsage,
		Description: "TEMP tablespace usage by session/SQL",
		RawSQL: `SELECT s.sid, s.username, s.sql_id, t.blocks * 8/1024 mb_used,
       t.tablespace
  FROM v$tempseg_usage t
  JOIN v$session s ON t.session_num = s.serial# AND t.session_addr = s.saddr
 ORDER BY t.blocks DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	QueryUndoUsage: {
		ID:          QueryUndoUsage,
		Description: "UNDO usage by long transactions",
		RawSQL: `SELECT s.sid, s.username, s.sql_id,
       t.used_ublk * 8/1024 undo_mb,
       ROUND((SYSDATE - t.start_date)*86400) elapsed_sec
  FROM v$transaction t
  JOIN v$session s ON t.ses_addr = s.saddr
 ORDER BY t.used_ublk DESC
 FETCH FIRST 10 ROWS ONLY`,
	},

	// ── Phase 5: Commit rate ────────────────────────────────────────────

	QueryCommitRate: {
		ID:          QueryCommitRate,
		Description: "Commit/rollback rate per second",
		RawSQL: `SELECT name, value FROM v$sysstat
 WHERE name IN ('user commits', 'user rollbacks', 'redo size', 'redo writes')`,
	},

	// ── Phase 3: Top SQL physical reads ─────────────────────────────────

	QueryTopSQLPhysicalReads: {
		ID:          QueryTopSQLPhysicalReads,
		Description: "Top SQL by physical reads (full scan indicator)",
		RawSQL: `SELECT sql_id, plan_hash_value, executions,
       disk_reads, buffer_gets,
       ROUND(disk_reads/NULLIF(executions,0)) reads_per_exec,
       ROUND(elapsed_time/NULLIF(executions,0)/1000) ms_per_exec
  FROM v$sqlarea
 WHERE disk_reads > 1000 AND executions > 0
 ORDER BY disk_reads DESC
 FETCH FIRST 10 ROWS ONLY`,
	},
}

// GetQueryDef returns the query definition for the given ID, or nil if not
// registered.
func GetQueryDef(qid QueryID) *QueryDef {
	return queryRegistry[qid]
}
