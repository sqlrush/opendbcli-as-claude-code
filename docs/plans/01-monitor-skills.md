# 监控类 Skill 三库对比明细

## 1. /health — 数据库健康检查

### Oracle (当前 24 项检查)

| 检查项 | Oracle SQL/视图 | MySQL 等价 | PostgreSQL 等价 |
|--------|---------------|-----------|----------------|
| 实例运行时间 | `v$instance.startup_time` | `SHOW GLOBAL STATUS LIKE 'Uptime'` | `pg_postmaster_start_time()` |
| 数据库角色 | `v$database.database_role` | `SHOW REPLICA STATUS` (判断是否 replica) | `pg_is_in_recovery()` |
| 归档模式 | `v$database.log_mode` | `SHOW VARIABLES LIKE 'log_bin'` | `archive_mode` (pg_settings) |
| 表空间使用率 | `dba_tablespace_usage_metrics` | `information_schema.TABLES` 按库聚合 | `pg_tablespace_size()` + `pg_database_size()` |
| 临时空间 | `dba_tablespace_usage_metrics (TEMPORARY)` | `SHOW GLOBAL STATUS LIKE 'Created_tmp%'` | `pg_stat_database.temp_bytes` |
| Undo 空间 | `dba_tablespace_usage_metrics (UNDO)` | `SHOW ENGINE INNODB STATUS` (History list) | 无等价（MVCC 内置），看 `pg_stat_activity` 长事务 |
| FRA 使用率 | `v$flash_recovery_area_usage` | **无等价**（MySQL 无 FRA 概念） | **无等价** |
| ASM 磁盘组 | `v$asm_diskgroup` | **无等价**（MySQL 无 ASM） | **无等价** |
| 连接数/上限 | `v$session COUNT` + `v$parameter.processes` | `SHOW GLOBAL STATUS LIKE 'Threads_connected'` + `max_connections` | `pg_stat_activity COUNT` + `max_connections` |
| 活跃会话数 | `v$session WHERE ACTIVE` | `SHOW PROCESSLIST` 或 `performance_schema.threads` | `pg_stat_activity WHERE state='active'` |
| 顶级等待事件 | `v$system_event ORDER BY time_waited DESC` | `performance_schema.events_waits_summary_global_by_event_name` | `pg_stat_activity.wait_event` 聚合（无累计时间） |
| 慢 SQL 数量 | `v$sql WHERE elapsed > 5s` | `performance_schema.events_statements_summary_by_digest WHERE avg_timer_wait > 5s` | `pg_stat_statements WHERE mean_exec_time > 5000` |
| Buffer Cache 命中率 | `v$sysstat (physical reads / block gets)` | `SHOW GLOBAL STATUS (Innodb_buffer_pool_read_requests vs reads)` | `pg_stat_bgwriter (buffers_backend / buffers_alloc)` 或 `pg_statio_user_tables` |
| Library Cache 命中率 | `v$librarycache (pins/reloads)` | **无等价**（MySQL 无 Library Cache） | **无等价**（PG 无 Library Cache） |
| PGA 使用率 | `v$pgastat` | **无等价**→ 替换为 Sort Buffer / Join Buffer 使用 | **无等价**→ 替换为 `work_mem` 使用情况 |
| SGA 空闲率 | `v$sgastat shared pool free` | **无等价**→ 替换为 InnoDB Buffer Pool 空闲率 | **无等价**→ 替换为 `shared_buffers` 命中率 |
| 日志切换频率 | `v$log_history (past hour)` | `SHOW BINARY LOG STATUS` + binlog 切换频率 | `pg_stat_wal` (wal_bytes) + WAL 归档速率 |
| Alert Log 错误 | `v$diag_alert_ext` | `performance_schema.error_log` (MySQL 8.0.22+) | `pg_stat_activity` + PostgreSQL log 文件 |
| 备份状态 | `v$rman_backup_job_details` | **无内置视图**（检查 xtrabackup 日志或 cron 状态） | `pg_stat_archiver` + pg_basebackup 日志 |
| 统计信息过时率 | `dba_tab_statistics.stale_stats` | `information_schema.TABLES.UPDATE_TIME` 对比 | `pg_stat_user_tables.last_autoanalyze` |
| 无效对象数 | `dba_objects WHERE INVALID` | **无等价**（MySQL 无 INVALID 对象概念） | **无等价**（PG 无 INVALID 对象概念）→ 可检查 broken functions |
| 资源限制使用率 | `v$resource_limit` | `SHOW GLOBAL STATUS` (max_used_connections) | `pg_stat_activity` COUNT 对比 `max_connections` |
| 密码即将过期 | `dba_users.expiry_date` | `mysql.user.password_lifetime` + `password_last_changed` | `pg_authid.rolvaliduntil` |

### MySQL 新增检查项（Oracle 没有的）
| 检查项 | 数据源 |
|--------|--------|
| InnoDB Redo Log 等待 | `SHOW ENGINE INNODB STATUS` (Log section) |
| Replication 延迟 | `SHOW REPLICA STATUS` (Seconds_Behind_Source) |
| InnoDB History List | `SHOW ENGINE INNODB STATUS` (History list length) |
| Table Open Cache 命中率 | `SHOW GLOBAL STATUS LIKE 'Table_open_cache%'` |
| Thread Cache 命中率 | `Threads_created / Connections` |

### PostgreSQL 新增检查项（Oracle 没有的）
| 检查项 | 数据源 |
|--------|--------|
| Vacuum 滞后 | `pg_stat_user_tables.n_dead_tup` / `n_live_tup` |
| Transaction ID Wraparound | `age(datfrozenxid)` from `pg_database` |
| Replication Lag | `pg_stat_replication.replay_lag` |
| Checkpoint 频率 | `pg_stat_bgwriter.checkpoints_req` vs `checkpoints_timed` |
| Bloat 估算 | `pgstattuple` 扩展 或 估算公式 |

---

## 2. /sessions — 所有会话

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$session` | `information_schema.PROCESSLIST` 或 `performance_schema.threads` | `pg_stat_activity` |
| 会话标识 | SID + Serial# | ID (PROCESSLIST_ID) | pid |
| 用户 | username | USER | usename |
| 状态 | status (ACTIVE/INACTIVE) | COMMAND (Sleep/Query/...) | state (active/idle/...) |
| 当前 SQL | sql_id → v$sql | INFO (当前 SQL 文本) | query |
| 等待事件 | event | 无（MySQL 5.7-）/ events_waits_current (8.0+) | wait_event_type + wait_event |
| 等待时间 | seconds_in_wait | TIME (命令执行秒数) | 无直接等价（需算 query_start 差值） |
| 来源机器 | machine | HOST | client_addr |
| 程序 | program | - | application_name |

### MySQL 独有列
- `DB` — 当前数据库
- `COMMAND` — 命令类型 (Query, Sleep, Connect, Binlog Dump, ...)

### PostgreSQL 独有列
- `datname` — 当前数据库
- `backend_type` — 后端类型 (client backend, autovacuum worker, ...)
- `xact_start` — 事务开始时间
- `query_start` — 查询开始时间

---

## 3. /activesessions — 活跃会话

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 筛选条件 | `status='ACTIVE' AND username IS NOT NULL` | `COMMAND != 'Sleep'` | `state = 'active'` |
| 等待类型 | wait_class | 需 performance_schema | wait_event_type |
| SQL 预览 | sql_id → v$sql.sql_text | INFO 列（完整 SQL） | query 列（完整 SQL） |

---

## 4. /waits — 等待事件统计

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$system_event` | `performance_schema.events_waits_summary_global_by_event_name` | **无累计等待时间视图** |
| 分类 | wait_class (13类) | event_name 前缀分类 (wait/io, wait/lock, ...) | wait_event_type (IO, Lock, LWLock, ...) |
| 累计次数 | total_waits | COUNT_STAR | **仅当前快照**（需自己采样累计） |
| 累计时间 | time_waited_micro | SUM_TIMER_WAIT (皮秒) | **无**（PG 不记录历史等待时间） |
| 平均等待 | 可算 | 可算 | **无法直接计算** |

### PostgreSQL 特殊处理
PG 没有累计等待时间统计，需要：
- 方案 A：采样 `pg_stat_activity.wait_event`，自己累计统计
- 方案 B：使用 `pg_wait_sampling` 扩展（如果安装了）
- 方案 C：只展示当前等待分布快照（与 Oracle 体验不同，但信息仍有价值）

---

## 5. /locks — 锁信息

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$lock` + `v$session` + `dba_objects` | `performance_schema.data_locks` + `data_lock_waits` (8.0+) | `pg_locks` + `pg_stat_activity` |
| 锁类型 | TX (行锁), TM (表锁) | RECORD, TABLE, GAP, NEXT-KEY, AUTO_INC | relation, tuple, transactionid, advisory, ... |
| 锁模式 | 6级 (None→Exclusive) | S, X, IS, IX, AUTO_INC, GAP | AccessShareLock → AccessExclusiveLock (8级) |
| 阻塞信息 | blocking_session | `data_lock_waits.BLOCKING_ENGINE_LOCK_ID` | `pg_locks.granted = false` + pid 关联 |
| 对象名 | dba_objects.object_name | OBJECT_SCHEMA + OBJECT_NAME | pg_class.relname |
| 特有锁类型 | - | Gap Lock, Next-Key Lock (InnoDB 独有) | Advisory Lock (应用级锁) |

---

## 6. /latches — Latch 争用

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | Latch = 轻量级内存锁 | Mutex (InnoDB 内部) | LWLock (轻量级锁) |
| 数据源 | `v$latch` | `performance_schema.mutex_instances` | `pg_stat_activity WHERE wait_event_type='LWLock'` |
| 指标 | gets, misses, sleeps, spin_gets | 无聚合统计（只有 instance 级别） | 无累计统计（只有当前状态） |
| 命令改名 | `/latches` | `/mutexes` (MySQL 叫 mutex) | `/lwlocks` (PG 叫 LWLock) |

### MySQL 替代方案
```sql
SELECT EVENT_NAME, COUNT_STAR, SUM_TIMER_WAIT
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE EVENT_NAME LIKE 'wait/synch/mutex/%'
ORDER BY SUM_TIMER_WAIT DESC LIMIT 30
```

### PostgreSQL 替代方案
```sql
SELECT wait_event, COUNT(*) AS sessions
FROM pg_stat_activity
WHERE wait_event_type = 'LWLock' AND state = 'active'
GROUP BY wait_event
ORDER BY sessions DESC
```

---

## 7. /mutexes — Mutex 争用

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$mutex_sleep` | `performance_schema.events_waits_summary_global_by_event_name WHERE 'wait/synch/mutex%'` | 与 LWLock 合并（PG 不区分 latch/mutex） |
| 指标 | sleeps, wait_time | COUNT_STAR, SUM_TIMER_WAIT | 同上 |

**建议**：MySQL 和 PG 可将 `/latches` 和 `/mutexes` 合并为一个命令 `/syncwaits` 或分别命名。

---

## 8. /dbtop — 实时监控面板

详见 [07-dbtop-collector.md](./07-dbtop-collector.md)

---

## 9. /tempsess — 临时空间占用会话

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$sort_usage` + `v$session` | `information_schema.INNODB_TEMP_TABLE_INFO` + `PROCESSLIST` | `pg_stat_activity` + `pg_stat_database.temp_bytes` |
| 临时空间概念 | TEMP tablespace | internal_tmp_disk_storage_engine (TempTable/MyISAM) | temp_tablespaces |
| 按会话查看 | v$sort_usage.session_addr | **困难**（MySQL 无按会话的临时空间统计） | `pg_stat_statements.temp_blks_read/written` (按 SQL) |

### MySQL 替代方案
MySQL 8.0+ 可通过 `performance_schema.memory_summary_by_thread_by_event_name` 查线程内存：
```sql
SELECT t.PROCESSLIST_ID, t.PROCESSLIST_USER,
       SUM(m.CURRENT_NUMBER_OF_BYTES_USED) / 1048576 AS tmp_mb
FROM performance_schema.memory_summary_by_thread_by_event_name m
JOIN performance_schema.threads t ON m.THREAD_ID = t.THREAD_ID
WHERE m.EVENT_NAME LIKE '%tmp%'
GROUP BY t.PROCESSLIST_ID, t.PROCESSLIST_USER
ORDER BY tmp_mb DESC LIMIT 20
```

### PostgreSQL 替代方案
PG 无直接按会话的临时空间视图，可：
- 查询 `pg_stat_statements` 的 `temp_blks_read` + `temp_blks_written` (按 SQL 维度)
- 查看 `pg_stat_database.temp_files` + `temp_bytes` (库级别)

---

## 10. /undosess — Undo 占用会话

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | UNDO tablespace，独立的回滚段 | InnoDB Undo Logs（undo_001, undo_002） | 无独立 Undo（MVCC 版本链在 heap 中） |
| 数据源 | `v$transaction` + `v$session` | `information_schema.INNODB_TRX` | `pg_stat_activity` (长事务 = 版本膨胀) |
| 按会话 | v$transaction.ses_addr | INNODB_TRX.trx_mysql_thread_id | pg_stat_activity.xact_start |

### MySQL 等价
```sql
SELECT t.trx_id, t.trx_mysql_thread_id AS pid,
       p.USER, p.HOST,
       TIMESTAMPDIFF(SECOND, t.trx_started, NOW()) AS duration_sec,
       t.trx_rows_modified, t.trx_state
FROM information_schema.INNODB_TRX t
JOIN information_schema.PROCESSLIST p ON t.trx_mysql_thread_id = p.ID
ORDER BY duration_sec DESC
```

### PostgreSQL 等价
PG 无 Undo 概念，但长事务会导致表膨胀（dead tuples 无法回收）：
```sql
SELECT pid, usename, state,
       age(clock_timestamp(), xact_start) AS xact_duration,
       query
FROM pg_stat_activity
WHERE xact_start IS NOT NULL
ORDER BY xact_start
LIMIT 20
```
**命令改名建议**：PG 版改为 `/longtx` (长事务) 更贴切。

---

## 11. /pga — PGA 内存详情

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | PGA = 每个进程的私有内存区 | 每连接内存 (sort_buffer, join_buffer, ...) | work_mem, maintenance_work_mem (每操作) |
| 数据源 | `v$pgastat` + `v$process` | `performance_schema.memory_summary_by_thread_by_event_name` | 无直接视图 |
| 调优参数 | pga_aggregate_target | sort_buffer_size, join_buffer_size, read_buffer_size | work_mem, maintenance_work_mem |

### MySQL 等价：/connmem (连接内存)
```sql
SELECT t.PROCESSLIST_ID, t.PROCESSLIST_USER,
       ROUND(SUM(m.CURRENT_NUMBER_OF_BYTES_USED)/1048576, 1) AS mem_mb
FROM performance_schema.memory_summary_by_thread_by_event_name m
JOIN performance_schema.threads t ON m.THREAD_ID = t.THREAD_ID
WHERE t.TYPE = 'FOREGROUND'
GROUP BY t.THREAD_ID
ORDER BY mem_mb DESC LIMIT 10
```

### PostgreSQL 等价
PG 无内置按连接的内存统计。可：
- 查 OS 级别 `/proc/<pid>/status` 的 VmRSS
- 使用 `pg_backend_memory_contexts` (PG 14+)

**命令改名建议**：MySQL → `/connmem`，PG → `/workmem` 或 `/memory`

---

## 12. /sga — SGA 内存详情

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | SGA = 共享全局区 (Buffer Cache + Shared Pool + ...) | InnoDB Buffer Pool + Query Cache (已移除) + Key Buffer | shared_buffers + wal_buffers |
| 数据源 | `v$sga_dynamic_components` | `SHOW ENGINE INNODB STATUS` + `SHOW GLOBAL STATUS` | `pg_stat_bgwriter` + `pg_buffercache` 扩展 |
| 组件 | Buffer Cache, Shared Pool, Large Pool, Java Pool, ... | Buffer Pool, Log Buffer, Adaptive Hash Index, ... | shared_buffers (唯一主要共享内存) |

### MySQL 等价：/bufferpool
```sql
-- InnoDB Buffer Pool 状态
SELECT
  @@innodb_buffer_pool_size / 1048576 AS pool_size_mb,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_total') AS total_pages,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_free') AS free_pages,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_pages_dirty') AS dirty_pages,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_read_requests') AS read_requests,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Innodb_buffer_pool_reads') AS disk_reads
```

### PostgreSQL 等价：/sharedbufs
需要 `pg_buffercache` 扩展：
```sql
SELECT c.relname, COUNT(*) AS buffers,
       ROUND(COUNT(*) * 8192.0 / 1048576, 1) AS mb
FROM pg_buffercache b
JOIN pg_class c ON b.relfilenode = c.relfilenode
WHERE b.reldatabase = (SELECT oid FROM pg_database WHERE datname = current_database())
GROUP BY c.relname
ORDER BY buffers DESC LIMIT 20
```

**命令改名建议**：MySQL → `/bufferpool`，PG → `/sharedbufs`

---

## 13. /redo — Redo 日志状态

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | Redo Log Groups (循环写) | Binary Log (顺序写) | WAL (Write-Ahead Log) |
| 日志状态 | `v$log` (CURRENT/INACTIVE/ACTIVE) | `SHOW BINARY LOGS` | `pg_ls_waldir()` + `pg_stat_wal` |
| 切换频率 | `v$log_history` | binlog 文件创建时间 | `pg_stat_wal.wal_bytes` delta |
| 归档 | `v$archive_dest` | binlog 自动清理 (expire_logs_days) | `archive_command` + `pg_stat_archiver` |

### MySQL 等价：/binlog
```sql
SHOW BINARY LOGS;
SHOW BINARY LOG STATUS;
SHOW VARIABLES LIKE 'expire_logs_days';
SHOW VARIABLES LIKE 'binlog_expire_logs_seconds';
```

### PostgreSQL 等价：/wal
```sql
SELECT * FROM pg_stat_wal;  -- PG 14+
SELECT * FROM pg_stat_archiver;
SELECT pg_current_wal_lsn(), pg_walfile_name(pg_current_wal_lsn());
```

---

## 14. /fra — Flash Recovery Area

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | FRA = 统一的恢复文件存储区 | **无等价** | **无等价** |
| 替代 | - | binlog 空间管理 | WAL 归档空间管理 |

**Oracle 独有**，MySQL/PG 不需要此 Skill。但可改造为：
- MySQL：`/binlogspace` — 显示 binlog 磁盘占用
- PG：`/walspace` — 显示 WAL 和归档占用

---

## 15. /asm — ASM 磁盘组

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | ASM = Oracle 专用卷管理 | **无等价** | **无等价** |

**Oracle 独有**。MySQL/PG 不需要此 Skill。无替代。

---

## 16. /sortusage — 排序段使用

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$sort_segment` + `v$tempseg_usage` | `performance_schema.memory_summary_*` + `SHOW STATUS LIKE 'Sort_%'` | `pg_stat_database.temp_files/temp_bytes` |

### MySQL 等价
```sql
SHOW GLOBAL STATUS LIKE 'Sort_merge_passes';
SHOW GLOBAL STATUS LIKE 'Sort_range';
SHOW GLOBAL STATUS LIKE 'Sort_rows';
SHOW GLOBAL STATUS LIKE 'Sort_scan';
SHOW GLOBAL STATUS LIKE 'Created_tmp_disk_tables';
SHOW GLOBAL STATUS LIKE 'Created_tmp_tables';
```

---

## 17. /resource — 资源限制

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$resource_limit` | `SHOW GLOBAL STATUS` + `SHOW VARIABLES` | `pg_settings` + `pg_stat_activity` |
| 关键资源 | processes, sessions, open_cursors | max_connections, table_open_cache, open_files_limit | max_connections, max_wal_senders, max_worker_processes |

---

## 18. /blocktree — 阻塞链

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$session.blocking_session` | `performance_schema.data_lock_waits` + `threads` | `pg_locks` (granted=false) + `pg_stat_activity` |
| 树构建 | SID → blocking_session 递归 | BLOCKING_THREAD_ID → REQUESTING_THREAD_ID | 通过 `pg_blocking_pids()` (PG 9.6+) |

### MySQL 8.0+
```sql
SELECT
  r.PROCESSLIST_ID AS blocked_pid,
  r.PROCESSLIST_USER AS blocked_user,
  b.PROCESSLIST_ID AS blocker_pid,
  b.PROCESSLIST_USER AS blocker_user,
  r.PROCESSLIST_INFO AS blocked_query,
  b.PROCESSLIST_INFO AS blocker_query
FROM performance_schema.data_lock_waits w
JOIN performance_schema.threads r ON r.THREAD_ID = w.REQUESTING_THREAD_ID
JOIN performance_schema.threads b ON b.THREAD_ID = w.BLOCKING_THREAD_ID
```

### PostgreSQL
```sql
SELECT
  blocked.pid AS blocked_pid,
  blocked.usename AS blocked_user,
  blocker.pid AS blocker_pid,
  blocker.usename AS blocker_user,
  blocked.query AS blocked_query,
  blocker.query AS blocker_query
FROM pg_stat_activity blocked
JOIN LATERAL unnest(pg_blocking_pids(blocked.pid)) AS blocker_pid ON true
JOIN pg_stat_activity blocker ON blocker.pid = blocker_pid
WHERE blocked.wait_event_type = 'Lock'
```

---

## 19. /segments — Top 段/表大小

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_segments` | `information_schema.TABLES` | `pg_total_relation_size()` + `pg_class` |
| 增长历史 | `dba_hist_seg_stat` (AWR) | 无内置历史 | 无内置历史（需自建快照） |

### MySQL 等价
```sql
SELECT TABLE_SCHEMA, TABLE_NAME, ENGINE,
       ROUND((DATA_LENGTH + INDEX_LENGTH) / 1048576, 2) AS size_mb,
       TABLE_ROWS
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
ORDER BY (DATA_LENGTH + INDEX_LENGTH) DESC LIMIT 20
```

### PostgreSQL 等价
```sql
SELECT schemaname, relname,
       pg_total_relation_size(schemaname || '.' || relname) / 1048576 AS total_mb,
       pg_table_size(schemaname || '.' || relname) / 1048576 AS table_mb,
       pg_indexes_size(schemaname || '.' || relname) / 1048576 AS index_mb
FROM pg_stat_user_tables
ORDER BY pg_total_relation_size(schemaname || '.' || relname) DESC LIMIT 20
```

---

## 20. /os — 操作系统指标

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$osstat` | 需读 `/proc/stat`, `/proc/meminfo` 或 OS 命令 | 需读 OS 文件或用 `pg_sys_info` 扩展 |
| CPU | v$osstat NUM_CPUS, BUSY_TIME | 无内置（MySQL 不暴露 OS CPU） | 无内置 |
| 内存 | v$osstat PHYSICAL_MEMORY_BYTES | 无内置 | 无内置 |

**三库共同问题**：MySQL 和 PG 不像 Oracle 那样通过 SQL 暴露 OS 指标。
- 方案：Go 直接读 `/proc/stat`, `/proc/meminfo`, `/proc/loadavg`（Linux），统一实现
- 此 Skill 可考虑放入壳层公用（OS 读取逻辑无关数据库类型），只是进程列表部分各库不同

---

## 21. /perfsnap — 性能快照

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| ASH 数据源 | `v$active_session_history` | `performance_schema.events_statements_history` (有限) | `pg_stat_activity` 快照（需自采样） |
| 系统统计 | `v$sysstat` | `SHOW GLOBAL STATUS` | `pg_stat_bgwriter` + `pg_stat_database` |
| 等待统计 | `v$system_event` | `performance_schema.events_waits_summary_*` | 需采样 `pg_stat_activity.wait_event` |

**差异很大**：Oracle 有 ASH（1 秒采样），MySQL 的 performance_schema 记录有限，PG 完全没有内置采样。需要 opendb 自己实现采样逻辑。

---

## 22. /indexhealth — 索引健康

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 无效索引 | `dba_indexes WHERE status='UNUSABLE'` | 无等价（InnoDB 索引不会标记 UNUSABLE） | `pg_index WHERE NOT indisvalid` |
| 碎片化 | blevel > 4 | `SHOW TABLE STATUS` (Data_free) | `pgstattuple` 扩展 |
| 未使用索引 | `v$object_usage` | `sys.schema_unused_indexes` (sys schema) | `pg_stat_user_indexes.idx_scan = 0` |

### MySQL 等价
```sql
-- 未使用索引
SELECT * FROM sys.schema_unused_indexes
WHERE object_schema NOT IN ('mysql', 'performance_schema', 'sys')
```

### PostgreSQL 等价
```sql
-- 未使用索引
SELECT schemaname, relname, indexrelname, idx_scan
FROM pg_stat_user_indexes
WHERE idx_scan = 0
ORDER BY pg_relation_size(indexrelid) DESC LIMIT 20
```

---

## 23. /users — 用户账户

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `dba_users` | `mysql.user` | `pg_authid` + `pg_roles` |
| 过期信息 | expiry_date | password_lifetime + password_last_changed | rolvaliduntil |
| 状态 | account_status (OPEN/LOCKED/EXPIRED) | account_locked (Y/N) | rolcanlogin |
| Profile | profile (DEFAULT, ...) | 无 Profile 概念 | 无 Profile 概念 |

### MySQL 等价
```sql
SELECT User, Host, account_locked,
       password_expired, password_lifetime,
       password_last_changed
FROM mysql.user
WHERE User NOT IN ('mysql.sys', 'mysql.session', 'mysql.infoschema')
ORDER BY password_expired DESC, password_last_changed
```

### PostgreSQL 等价
```sql
SELECT rolname, rolcanlogin, rolvaliduntil,
       CASE WHEN rolvaliduntil IS NOT NULL
            THEN EXTRACT(DAY FROM rolvaliduntil - now())::int
            ELSE NULL END AS days_left
FROM pg_authid
WHERE rolname NOT LIKE 'pg_%'
ORDER BY rolvaliduntil NULLS LAST
```
