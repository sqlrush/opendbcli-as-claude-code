# 哨兵监控系统三库对比

## 1. 指标体系对照

### Fast 层 (1s 采样)

| Oracle 指标 | Oracle 数据源 | MySQL 等价指标 | MySQL 数据源 | PG 等价指标 | PG 数据源 |
|-------------|-------------|---------------|-------------|------------|----------|
| active_sessions | v$session WHERE ACTIVE | threads_running | `SHOW GLOBAL STATUS` | active_connections | pg_stat_activity WHERE state='active' |
| on_cpu | v$session WHERE wait_class NOT IN (Idle, I/O) | threads_running - threads_waiting | performance_schema | active - waiting | pg_stat_activity |
| io_wait | v$session WHERE wait_class IN (User I/O, System I/O) | Innodb_data_pending_reads/writes | `SHOW GLOBAL STATUS` | waiting on IO | pg_stat_activity WHERE wait_event_type='IO' |
| lock_wait | v$session WHERE event LIKE 'enq%' | Innodb_row_lock_current_waits | `SHOW GLOBAL STATUS` | waiting on Lock | pg_stat_activity WHERE wait_event_type='Lock' |
| long_sql | v$session WHERE elapsed > threshold | threads with TIME > threshold | PROCESSLIST | queries with duration > threshold | pg_stat_activity |
| redo_kb_per_sec | v$sysstat (redo size delta) | Innodb_os_log_written delta | `SHOW GLOBAL STATUS` | wal_bytes delta | pg_stat_wal (PG14+) |
| hard_parse_per_sec | v$sysstat (parse count hard delta) | Com_stmt_prepare delta (近似) | `SHOW GLOBAL STATUS` | **无等价** | - |

### Medium 层 (10s 采样)

| Oracle 指标 | MySQL 等价 | PG 等价 |
|-------------|-----------|---------|
| tps (commits+rollbacks/s) | Com_commit + Com_rollback delta | xact_commit + xact_rollback delta (pg_stat_database) |
| qps (execute count/s) | Queries delta | 无直接等价，可用 pg_stat_statements 总 calls delta |
| logical_reads/s | buffer_gets delta | shared_blks_hit delta (pg_stat_database) |
| physical_reads/s | disk_reads delta → Innodb_data_reads delta | shared_blks_read delta |
| db_cpu_pct | v$sys_time_model → 无直接等价 | 无直接等价（需 OS 级采集或 pg_stat_kcache 扩展） |
| wait_time_ratio | (db_time - cpu_time) / db_time | 无直接等价 | 无直接等价 |
| avg_wait_ms | v$system_event delta | events_waits_summary delta | **无**（PG 不记录累计等待时间） |
| enqueue_waits | v$sysstat enqueue waits | Innodb_row_lock_waits delta | pg_stat_database.deadlocks delta (粗粒度) |
| latch_sleeps | v$latch sleeps delta | mutex sleeps (perf_schema) | **无** |
| blocking_chains | v$session blocking_session | data_lock_waits COUNT | pg_blocking_pids() 聚合 |
| top_sql_elapsed | v$sql | events_statements_summary_by_digest | pg_stat_statements |
| sort_disk | v$sysstat sorts (disk) | Sort_merge_passes | temp_blks_written (pg_stat_statements) |
| full_scans | v$sysstat table scans (long) | Select_scan | **无直接等价** |
| parse_failures | v$sysstat parse failures | Com_stmt_prepare errors | **无** |

### Slow 层 (30s 采样)

| Oracle 指标 | MySQL 等价 | PG 等价 |
|-------------|-----------|---------|
| buffer_cache_hit_pct | v$sysstat → Innodb_buffer_pool_read_requests / reads | blks_hit / (blks_hit + blks_read) from pg_stat_database |
| library_cache_hit_pct | v$librarycache → **无等价** | **无等价** |
| shared_pool_free_pct | v$sgastat → **无等价** | **无等价** |
| pga_used_pct | v$pgastat → 无直接等价，可算连接总内存/限制 | **无** |
| tablespace_used_pct | dba_tablespace_usage_metrics | `SUM(DATA_LENGTH)/disk_size` (近似) | `pg_database_size()/disk_size` (近似) |
| temp_used_pct | 同上 TEMPORARY | Created_tmp_disk_tables rate | temp_bytes (pg_stat_database) |
| undo_used_pct | 同上 UNDO | InnoDB History list length | age(datfrozenxid) (事务 ID wraparound) |
| fra_used_pct | v$flash_recovery_area_usage | **无** | **无** |
| asm_used_pct | v$asm_diskgroup | **无** | **无** |
| archive_lag_sec | v$archive_dest_status | Seconds_Behind_Source | replay_lag (pg_stat_replication) |
| log_switch_rate | v$log_history | binlog file creation rate | WAL bytes/s (pg_stat_wal) |
| invalid_objects | dba_objects INVALID | **无** | **无** (可检查失效函数) |
| stale_stats_pct | dba_tab_statistics | 估算 (UPDATE_TIME 对比) | n_mod_since_analyze / n_live_tup |
| resource_limit_pct | v$resource_limit | max_used_connections / max_connections | numbackends / max_connections |
| password_expiry_days | dba_users | mysql.user password_lifetime | pg_authid.rolvaliduntil |
| open_cursor_pct | v$resource_limit open_cursors | **无** (MySQL 无 cursor 限制概念) | **无** |

## 2. 检测算法适用性

| 算法 | 说明 | Oracle | MySQL | PG |
|------|------|--------|-------|----|
| T1 3σ 自适应 | 均值 ± 3σ | ✓ 全部指标 | ✓ 全部指标 | ✓ 全部指标 |
| T2 硬顶 | 绝对危险阈值 | ✓ | ✓ | ✓ |
| T3 趋势 | 线性回归斜率 | ✓ | ✓ | ✓ |
| T4 加速度 | 二阶导数 | ✓ | ✓ | ✓ |
| T5 复合 | 多指标 AND | ✓ | ✓（指标组合不同） | ✓（指标组合不同） |
| T6 容量 | 黄线/红线 | ✓ | ✓ | ✓ |
| T7 偏移 | 窗口均值对比 | ✓ | ✓ | ✓ |
| T8 回归 | 值低于下限 | ✓ (cache hit) | ✓ (buffer pool hit) | ✓ (cache hit) |
| T9 缺失 | 速率降为零 | ✓ | ✓ | ✓ |

**算法框架通用**，但：
- T5 复合检测的指标组合因库而异（如 Oracle 的 redo + log file sync，MySQL 的 binlog + sync）
- T6 容量阈值不同（如 Oracle tablespace 有 95% 红线，MySQL 无 tablespace 概念）
- T8 的 cache hit 计算方式不同

## 3. 根因分类对照

| Oracle 根因 | MySQL 等价根因 | PG 等价根因 |
|-------------|--------------|------------|
| bad_sql (慢 SQL 争用) | slow_query (慢查询) | slow_query (慢查询) |
| io_subsystem (存储 IO) | io_bottleneck (磁盘 IO) | io_bottleneck (磁盘 IO) |
| latch_storm (Latch 争用) | mutex_contention (InnoDB Mutex) | lwlock_contention (LWLock 争用) |
| redo_bottleneck (Redo 写入) | binlog_bottleneck (Binlog 写入) | wal_bottleneck (WAL 写入) |
| lock_contention (行锁/表锁) | row_lock_contention (InnoDB 行锁) | lock_contention (行锁) |
| traffic_storm (连接风暴) | connection_storm (连接风暴) | connection_storm (连接风暴) |
| - | **replication_lag** (主从延迟) | **replication_lag** (流复制延迟) |
| - | **innodb_history** (Undo 积压) | **vacuum_lag** (Vacuum 滞后/XID wraparound) |
| - | - | **bloat** (表/索引膨胀) |

### MySQL 新增根因
- **replication_lag**：主从延迟持续增大，可能导致读写分离场景数据不一致
- **innodb_history**：History list length 持续增长，说明有长事务阻止 purge

### PostgreSQL 新增根因
- **replication_lag**：流复制延迟
- **vacuum_lag**：autovacuum 跟不上 DML 速率，dead tuples 堆积
- **bloat**：表或索引膨胀严重（dead tuples / live tuples > 阈值）
- **xid_wraparound**：事务 ID 即将耗尽，需要紧急 VACUUM FREEZE

## 4. Probe SQL 对照

### Fast Probe (1s)

**Oracle:**
```sql
SELECT COUNT(*), SUM(CASE WHEN status='ACTIVE' AND type='USER' THEN 1 ELSE 0 END), ...
FROM v$session
```

**MySQL:**
```sql
SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_connected') AS total,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME='Threads_running') AS active,
  (SELECT COUNT(*) FROM performance_schema.data_lock_waits) AS lock_waits
```

**PostgreSQL:**
```sql
SELECT
  COUNT(*) AS total,
  COUNT(*) FILTER (WHERE state = 'active') AS active,
  COUNT(*) FILTER (WHERE wait_event_type = 'Lock') AS lock_wait,
  COUNT(*) FILTER (WHERE wait_event_type = 'IO') AS io_wait
FROM pg_stat_activity
WHERE backend_type = 'client backend'
```

### Medium Probe (10s)

**Oracle:** `v$sysstat` delta + `v$system_event` delta + `v$latch` delta

**MySQL:** `SHOW GLOBAL STATUS` delta (多个 Innodb_* 变量)

**PostgreSQL:** `pg_stat_database` delta + `pg_stat_bgwriter` delta

### Slow Probe (30s)

**Oracle:** `dba_tablespace_usage_metrics` + `v$resource_limit` + `dba_tab_statistics`

**MySQL:** `information_schema.TABLES` 聚合 + `SHOW GLOBAL STATUS` (connections 相关)

**PostgreSQL:** `pg_database_size()` + `pg_stat_user_tables` (dead tuples) + `age(datfrozenxid)`

## 5. Burst 采集差异

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 采集间隔 | 200ms | 200ms (可行) | 200ms (可行) |
| 会话快照 | v$session + v$sql | PROCESSLIST + events_statements_current | pg_stat_activity |
| 阻塞链 | v$session.blocking_session | data_lock_waits | pg_blocking_pids() |
| SQL 文本 | v$sql.sql_text (共享池缓存) | INFO 列 / events_statements_current.SQL_TEXT | pg_stat_activity.query |
| 等待事件 | event + wait_class | events_waits_current (如果开启) | wait_event_type + wait_event |

### MySQL Burst 特殊点
- `performance_schema.events_waits_current` 需要 `setup_instruments` 和 `setup_consumers` 开启
- 默认可能未开启 → 需检测并提示用户

### PostgreSQL Burst 特殊点
- `pg_stat_activity` 查询本身有性能开销（比 v$session 重）
- 200ms 频率可能对高并发 PG 有影响 → 可调整为 500ms
