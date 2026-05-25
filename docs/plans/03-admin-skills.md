# 管理类 Skill 三库对比明细

## 1. /kill — 终止会话

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 命令 | `ALTER SYSTEM KILL SESSION 'sid,serial#'` | `KILL <processlist_id>` 或 `KILL QUERY <id>` | `SELECT pg_terminate_backend(pid)` 或 `pg_cancel_backend(pid)` |
| 标识符 | SID + Serial# (需查 v$session) | PROCESSLIST_ID (直接用) | PID (直接用) |
| IMMEDIATE | `ALTER SYSTEM KILL SESSION 'sid,serial#' IMMEDIATE` | `KILL` 本身就是立即的 | `pg_terminate_backend` 就是立即的 |
| 只杀查询 | 不支持（只能杀整个会话） | `KILL QUERY <id>` (只取消当前查询) | `pg_cancel_backend(pid)` (只取消当前查询) |

### MySQL 等价
```sql
-- 查进程
SELECT ID, USER, HOST, DB, COMMAND, TIME, STATE, INFO
FROM information_schema.PROCESSLIST WHERE ID = :1;
-- 杀会话
KILL :1;
-- 只杀查询
KILL QUERY :1;
```

### PostgreSQL 等价
```sql
-- 查进程
SELECT pid, usename, client_addr, state, query
FROM pg_stat_activity WHERE pid = :1;
-- 终止会话
SELECT pg_terminate_backend(:1);
-- 只取消查询
SELECT pg_cancel_backend(:1);
```

### 差异点
- MySQL/PG 多一个选项：只杀查询 vs 杀整个会话
- Oracle 需要额外查 serial#，MySQL/PG 直接用 PID
- 命令建议：`/kill 142` (杀会话), `/kill 142 query` (只杀查询)

---

## 2. /space — 空间使用

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | Tablespace → Datafile | Database → Table (.ibd) | Database → Table (base/) |
| 数据源 | `dba_tablespace_usage_metrics` | `information_schema.TABLES` | `pg_database_size()` + `pg_tablespace_size()` |
| 粒度 | 表空间级别（多个表共享） | 表级别（每个表独立 .ibd） | 数据库 / 表空间 / 表 多级 |

### MySQL 等价
```sql
-- 按数据库统计
SELECT TABLE_SCHEMA AS db_name,
       ROUND(SUM(DATA_LENGTH + INDEX_LENGTH) / 1048576, 2) AS used_mb,
       ROUND(SUM(DATA_FREE) / 1048576, 2) AS free_mb,
       COUNT(*) AS table_count
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
GROUP BY TABLE_SCHEMA
ORDER BY used_mb DESC;

-- 磁盘级别
SELECT @@datadir AS data_dir,
       @@innodb_data_file_path AS ibdata_config;
```

### PostgreSQL 等价
```sql
-- 按数据库
SELECT datname,
       pg_database_size(datname) / 1048576 AS size_mb
FROM pg_database
WHERE datname NOT IN ('template0', 'template1')
ORDER BY pg_database_size(datname) DESC;

-- 按表空间
SELECT spcname,
       pg_tablespace_size(spcname) / 1048576 AS size_mb
FROM pg_tablespace
ORDER BY pg_tablespace_size(spcname) DESC;
```

### 布局差异
- Oracle：表空间名 + 使用率进度条 + used/total MB
- MySQL：数据库名 + 大小 + 表数量（MySQL 无"使用率"概念，磁盘空间由 OS 管理）
- PG：数据库 + 表空间 双维度

---

## 3. /backup — 备份历史

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 工具 | RMAN | mysqldump/xtrabackup/mysqlpump | pg_dump/pg_basebackup |
| 历史查看 | `v$rman_backup_job_details` | **无内置视图** | `pg_stat_archiver` (WAL归档) |
| 状态 | COMPLETED/FAILED/RUNNING | - | archived_count/failed_count |

### MySQL 替代
MySQL 无内置备份历史视图。替代方案：
- 检查 binlog 最后写入时间（`SHOW BINARY LOGS`）
- 检查 xtrabackup 的 checkpoint 信息（`SHOW ENGINE INNODB STATUS` 的 `Last checkpoint at`）
- 或 opendb 自建备份执行日志

### PostgreSQL 替代
```sql
-- WAL 归档状态
SELECT archived_count, failed_count,
       last_archived_wal, last_archived_time,
       last_failed_wal, last_failed_time
FROM pg_stat_archiver;
```
PG 也没有 pg_dump/pg_basebackup 的历史视图，需 opendb 自建或查日志。

---

## 4. /standby — 高可用状态

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 复制类型 | Data Guard (物理/逻辑 standby) | GTID Replication (主从/组复制) | Streaming Replication |
| 角色查看 | `v$database.database_role` | `SHOW REPLICA STATUS` | `pg_is_in_recovery()` |
| 延迟 | `v$archive_dest_status.gap_status` | `Seconds_Behind_Source` | `pg_stat_replication.replay_lag` |
| 切换 | ALTER DATABASE SWITCHOVER | mysqlfailover / 手动 | `pg_promote()` |

### MySQL 等价：/replication
```sql
-- 主库
SHOW BINARY LOG STATUS;
SHOW REPLICAS;

-- 从库
SHOW REPLICA STATUS\G
-- 关键字段：Replica_IO_Running, Replica_SQL_Running, Seconds_Behind_Source,
--          Retrieved_Gtid_Set, Executed_Gtid_Set
```

### PostgreSQL 等价：/replication
```sql
-- 主库
SELECT client_addr, state, sent_lsn, write_lsn, flush_lsn, replay_lsn,
       write_lag, flush_lag, replay_lag
FROM pg_stat_replication;

-- 从库
SELECT * FROM pg_stat_wal_receiver;
SELECT pg_is_in_recovery(), pg_last_wal_receive_lsn(), pg_last_wal_replay_lsn();
```

### 命令改名
Oracle: `/standby` → MySQL: `/replication` → PG: `/replication`

---

## 5. /params — 参数查看

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$parameter` | `SHOW VARIABLES` 或 `performance_schema.global_variables` | `pg_settings` |
| 模式匹配 | `LIKE UPPER(:pattern)` | `LIKE :pattern` | `WHERE name LIKE :pattern` |
| 字段 | name, value, description | Variable_name, Value | name, setting, unit, short_desc, context |

### MySQL 等价
```sql
SELECT VARIABLE_NAME, VARIABLE_VALUE
FROM performance_schema.global_variables
WHERE VARIABLE_NAME LIKE :pattern
ORDER BY VARIABLE_NAME
```

### PostgreSQL 等价
```sql
SELECT name, setting, unit, short_desc, context
FROM pg_settings
WHERE name LIKE :pattern
ORDER BY name
```

### PG 额外信息
PG 的 `pg_settings` 比 Oracle/MySQL 丰富：有 `context` (何时生效: postmaster/sighup/user/superuser)、`unit`、`boot_val` (默认值)、`reset_val`、`source` (配置来源)。

---

## 6. /alert — 告警日志

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$diag_alert_ext` | `performance_schema.error_log` (MySQL 8.0.22+) | PostgreSQL 日志文件 |
| SQL 查看 | 可以 SQL 查询 | 可以 SQL 查询 (8.0.22+) | **需读文件** (`log_destination` 配置) |
| 级别 | message_level (1-32) | prio (System/Error/Warning/Note) | LOG/WARNING/ERROR/FATAL/PANIC |

### MySQL 等价 (8.0.22+)
```sql
SELECT logged AS time, prio AS level,
       subsystem, data AS message
FROM performance_schema.error_log
WHERE logged > NOW() - INTERVAL :hours HOUR
  AND prio IN ('System', 'Error', 'Warning')
ORDER BY logged DESC LIMIT 50
```

### PostgreSQL 替代
PG 的日志无法通过 SQL 查询（除非 `log_destination = 'csvlog'` + 外部表）。

替代方案：
- 如果 `log_destination = 'csvlog'`：用外部表 `pg_catalog.pg_log` 或 `file_fdw` 扩展
- 如果 `log_destination = 'stderr'`：opendb 读取日志文件
- 使用 `pg_current_logfile()` (PG 10+) 获取当前日志路径

---

## 7. /alter — 修改参数

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 命令 | `ALTER SYSTEM SET param = value SCOPE=BOTH` | `SET GLOBAL param = value` | `ALTER SYSTEM SET param = value` |
| 生效范围 | SCOPE: MEMORY/SPFILE/BOTH | GLOBAL/SESSION | 取决于 context: postmaster 需重启, sighup 需 reload |
| 持久化 | SPFILE 自动持久化 | 需手动写 my.cnf 或 SET PERSIST (8.0+) | ALTER SYSTEM 写入 postgresql.auto.conf |
| 可修改性 | issys_modifiable (IMMEDIATE/DEFERRED/FALSE) | @@global 是否可设置 | context (postmaster/sighup/superuser/user) |

### MySQL 等价
```sql
-- 查看参数
SELECT VARIABLE_NAME, VARIABLE_VALUE
FROM performance_schema.global_variables WHERE VARIABLE_NAME = :param;

-- 修改（内存）
SET GLOBAL :param = :value;

-- 修改（持久化，MySQL 8.0+）
SET PERSIST :param = :value;
```

### PostgreSQL 等价
```sql
-- 查看参数
SELECT name, setting, unit, context, short_desc
FROM pg_settings WHERE name = :param;

-- 修改
ALTER SYSTEM SET :param = :value;
SELECT pg_reload_conf();  -- 对 sighup 类型参数

-- 对 postmaster 类型参数，需要提示用户重启
```

### 差异处理
- MySQL 8.0+ 的 `SET PERSIST` 等价于 Oracle 的 `SCOPE=BOTH`
- PG 需要根据 `context` 判断：`sighup` → ALTER SYSTEM + reload，`postmaster` → 需重启
- 三库都需要显示参数是否可动态修改

---

## 8. /resize — 表空间扩容

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | Tablespace → Datafile resize/add | **无等价**（InnoDB 自动扩展） | **无等价**（PG 表空间 = 目录） |
| 操作 | ALTER DATABASE DATAFILE RESIZE / ADD | - | - |

### Oracle 独有的原因
Oracle 的 tablespace 是预分配固定大小的，需要手动扩容。MySQL InnoDB 和 PG 都是自动增长的。

### MySQL 替代：/innodb
```sql
-- InnoDB 系统表空间
SHOW VARIABLES LIKE 'innodb_data_file_path';
SHOW VARIABLES LIKE 'innodb_autoextend_increment';

-- InnoDB Undo 表空间
SELECT TABLESPACE_NAME, FILE_NAME, INITIAL_SIZE, AUTOEXTEND_SIZE
FROM information_schema.INNODB_TABLESPACES WHERE SPACE_TYPE = 'Undo';
```

### PostgreSQL 替代
PG 的表空间就是目录，空间管理由文件系统负责。无需 opendb 管理。

**建议**：MySQL/PG 不提供 `/resize` 命令。

---

## 9. /jobs — 调度作业

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 调度器 | DBMS_SCHEDULER | EVENT SCHEDULER | pg_cron 扩展 |
| 作业列表 | `dba_scheduler_jobs` | `information_schema.EVENTS` | `cron.job` |
| 执行历史 | `dba_scheduler_job_run_details` | `information_schema.EVENTS.LAST_EXECUTED` | `cron.job_run_details` |
| 失败详情 | error#, additional_info | LAST_ALTERED (无详情) | status, return_message |

### MySQL 等价
```sql
-- 作业列表
SELECT EVENT_SCHEMA, EVENT_NAME, STATUS, EVENT_TYPE,
       INTERVAL_VALUE, INTERVAL_FIELD,
       LAST_EXECUTED, STARTS, ENDS
FROM information_schema.EVENTS
WHERE EVENT_SCHEMA NOT IN ('mysql', 'sys')
ORDER BY LAST_EXECUTED DESC;

-- 检查调度器是否启用
SHOW VARIABLES LIKE 'event_scheduler';
```

### PostgreSQL 等价 (需要 pg_cron 扩展)
```sql
-- 作业列表
SELECT jobid, schedule, command, nodename, nodeport, database, username, active
FROM cron.job
ORDER BY jobid;

-- 执行历史
SELECT jobid, command, status, return_message, start_time, end_time
FROM cron.job_run_details
ORDER BY start_time DESC LIMIT 30;
```

---

## 10. /gather — 统计信息收集

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 收集命令 | `DBMS_STATS.GATHER_TABLE_STATS` | `ANALYZE TABLE` | `ANALYZE` |
| 过时检测 | `dba_tab_statistics.stale_stats` | `information_schema.TABLES.UPDATE_TIME` | `pg_stat_user_tables.last_autoanalyze` + `n_mod_since_analyze` |
| 自动收集 | 内置 auto task | `innodb_stats_auto_recalc` | `autovacuum` (ANALYZE 是 autovacuum 的一部分) |

### MySQL 等价
```sql
-- 检查需要 ANALYZE 的表
SELECT TABLE_SCHEMA, TABLE_NAME, UPDATE_TIME, TABLE_ROWS
FROM information_schema.TABLES
WHERE TABLE_SCHEMA NOT IN ('mysql', 'information_schema', 'performance_schema', 'sys')
  AND ENGINE = 'InnoDB'
  AND (UPDATE_TIME IS NULL OR UPDATE_TIME < NOW() - INTERVAL 7 DAY)
ORDER BY TABLE_ROWS DESC LIMIT 50;

-- 收集
ANALYZE TABLE schema.table;
```

### PostgreSQL 等价
```sql
-- 检查需要 ANALYZE 的表
SELECT schemaname, relname, last_analyze, last_autoanalyze,
       n_mod_since_analyze, n_live_tup
FROM pg_stat_user_tables
WHERE n_mod_since_analyze > n_live_tup * 0.1
   OR last_analyze IS NULL
ORDER BY n_mod_since_analyze DESC LIMIT 50;

-- 收集
ANALYZE schema.table;
```

---

## 11. /indexhealth — 索引健康

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 无效索引 | `dba_indexes WHERE UNUSABLE` | 无等价 | `pg_index WHERE NOT indisvalid` |
| 碎片化 | blevel > 4 | `SHOW TABLE STATUS` (Data_free) | `pgstattuple` 扩展 |
| 未使用 | `v$object_usage` | `sys.schema_unused_indexes` | `pg_stat_user_indexes.idx_scan = 0` |
| 重建 | `ALTER INDEX REBUILD` | `ALTER TABLE ... FORCE` 或 `OPTIMIZE TABLE` | `REINDEX INDEX` |

---

## 公用 Skill（不需要改造）

| Skill | 说明 | 是否公用 |
|-------|------|---------|
| /help | 读 registry，自动适配 | 公用（但显示内容因库而异） |
| /clear | 清屏 | 完全公用 |
| /config | 显示配置 | 完全公用 |
| /history | 命令历史 | 完全公用 |
| /login | 连接管理 | 公用（但连接参数因库而异） |
| /logout | 断开连接 | 完全公用 |
| /conn | 快速连接 | 需改造：连接串格式不同 |
| /scheduler | 定时任务 | 公用（opendb 内置调度器） |

### /conn 连接串差异

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 格式 | `user/pass@host:1521/service` | `user:pass@host:3306/database` | `user:pass@host:5432/database` |
| 默认端口 | 1521 | 3306 | 5432 |
| 特有参数 | `as sysdba`, service vs SID | charset, ssl-mode | sslmode, search_path |
| OS 认证 | `/ as sysdba` | `--login-path` | `peer` auth |
