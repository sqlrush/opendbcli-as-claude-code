# 实时监控面板 (dbtop) 三库对比

## 1. 当前 Oracle 实现概览

### 采集 SQL（9 条，4 组并行执行）

| # | SQL 常量 | 查询目标 | 数据源 |
|---|---------|---------|--------|
| 1 | `dbRoleSQL` | 数据库角色 (PRIMARY/STANDBY) | `v$database` |
| 2 | `sgaUsedSQL` | SGA 已用 (MB) | `v$sgastat` |
| 3 | `sgaMaxSQL` | SGA 总量 (MB) | `v$sga` |
| 4 | `pgaUsedSQL` | PGA 已用 (MB) | `v$pgastat` |
| 5 | `pgaMaxSQL` | PGA 上限 (MB) | `v$parameter` (pga_aggregate_limit) |
| 6 | `timeModelSQL` | DB CPU / DB time (微秒, delta) | `v$sys_time_model` |
| 7 | `sysstatSQL2` | commits / rollbacks / executes / redo size (delta) | `v$sysstat` |
| 8 | `sessionCountSQL` | 会话计数 (SN/AN/ACPU/AIO/IDL) | `v$session` |
| 9 | `eventSQL` | Top 10 等待事件 (含 DB CPU, delta) | `v$system_event` + `v$sys_time_model` |
| 10 | `activeSessionSQL` | 活跃会话列表 (SID/User/SQL/Event/Elapsed) | `v$session` + `v$sql` |
| 11 | `burstSessionSQL` | 爆发模式扩展 (blocking/plan_hash/machine) | `v$session` + `v$sql` |

### Snapshot 结构

| 区域 | 字段 | 计算方式 |
|------|------|---------|
| DB Info | Version, InstanceName, DBRole | 缓存（只查一次） |
| Memory | SGAUsedMB, SGAMaxMB, PGAUsedMB, PGAMaxMB | 每帧查询 |
| CPU/Wait | DBPercent, WTRPercent | delta: (DB time - CPU time) / elapsed |
| Sessions | TotalSessions, ActiveCount, ActiveCPU, ActiveIO, IdleCount | 每帧 COUNT |
| Throughput | TPS, QPS, RedoKBs | delta: commits/executes/redo_size per second |
| Wait Events | []WaitEvent (event, waits, time, pct, wait_class, delta) | 累计值 + 帧间 delta |
| Active Sessions | []SessionRow (SID, User, SQL_ID, Event, Elapsed, SQL Text) | 每帧快照 |
| Health | HealthLevel, []Alerts | EvaluateHealth() 规则判断 |

### 渲染布局（28 行固定）

| 面板 | 行数 | 内容 |
|------|------|------|
| Header Box | 5 | 版本/实例/角色/健康 + SGA/PGA 进度条 + SN/AN/TPS/QPS/Redo |
| Events Box | 8 | Top 5 等待事件 + 柱状图 + delta 占比 |
| Sessions Box | 14 | Top 10 活跃会话 (SID/User/SQL_ID/Event/Elapsed/SQL) |
| Status Bar | 1 | 刷新间隔 + 模式 + 快捷键提示 |

### 并行采集分组

```
Group 1 (goroutine): dbRole + SGA + PGA + timeModel + sysstat → 5 SQL
Group 2 (goroutine): sessionCounts                             → 1 SQL
Group 3 (goroutine): waitEvents + delta 计算                    → 1 SQL
Group 4 (goroutine): activeSessions                            → 1 SQL
```

4 组并行 → sync.WaitGroup → 合并 Snapshot → EvaluateHealth → Render

---

## 2. MySQL 等价 SQL

### SQL 1: 数据库角色
```sql
-- Oracle: SELECT database_role FROM v$database
-- MySQL:
SELECT
  CASE WHEN @@read_only = 1 THEN 'STANDBY' ELSE 'PRIMARY' END AS database_role
```
备注：MySQL 判断主从靠 `read_only` + `SHOW REPLICA STATUS`，不像 Oracle 有明确 role。

### SQL 2-3: 内存（SGA → InnoDB Buffer Pool）
```sql
-- Oracle: SGA used/max
-- MySQL: InnoDB Buffer Pool
SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status
   WHERE VARIABLE_NAME = 'Innodb_buffer_pool_bytes_data') / 1048576 AS bp_used_mb,
  @@innodb_buffer_pool_size / 1048576 AS bp_max_mb
```

### SQL 4-5: 内存（PGA → 连接内存）
```sql
-- Oracle: PGA used / pga_aggregate_limit
-- MySQL: 无直接等价，近似用总连接内存
SELECT
  ROUND(SUM(CURRENT_NUMBER_OF_BYTES_USED) / 1048576, 0) AS conn_mem_mb
FROM performance_schema.memory_summary_global_by_event_name
WHERE EVENT_NAME LIKE 'memory/sql/%'
```
备注：MySQL 无 PGA 概念。用 `performance_schema.memory_summary_global_by_event_name` 近似。

### SQL 6: CPU/Wait 比率
```sql
-- Oracle: v$sys_time_model (DB CPU, DB time)
-- MySQL: 无直接等价
-- 近似方案：用 Threads_running / CPU 核数 估算 CPU 使用率
SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status
   WHERE VARIABLE_NAME = 'Threads_running') AS threads_running
```
备注：MySQL 无 DB CPU / DB time 概念。`db%` 和 `WTR%` 需要用不同指标近似：
- `db%` ≈ Threads_running / max_connections × 100
- `WTR%` ≈ (Innodb_row_lock_current_waits + 其他等待) / Threads_running

### SQL 7: 吞吐量 (TPS/QPS/Redo)
```sql
-- Oracle: v$sysstat (user commits, user rollbacks, execute count, redo size)
-- MySQL:
SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Com_commit') AS commits,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Com_rollback') AS rollbacks,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Queries') AS queries,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Innodb_os_log_written') AS redo_bytes
```

### SQL 8: 会话计数
```sql
-- Oracle: v$session COUNT by status/wait_class
-- MySQL:
SELECT
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Threads_connected') AS total,
  (SELECT VARIABLE_VALUE FROM performance_schema.global_status WHERE VARIABLE_NAME = 'Threads_running') AS active,
  (SELECT COUNT(*) FROM performance_schema.threads WHERE TYPE = 'FOREGROUND' AND PROCESSLIST_STATE IS NOT NULL
   AND PROCESSLIST_STATE NOT IN ('', 'Sleep')) AS non_idle
```
或使用 `information_schema.PROCESSLIST`:
```sql
SELECT
  COUNT(*) AS SN,
  SUM(CASE WHEN COMMAND != 'Sleep' THEN 1 ELSE 0 END) AS AN,
  SUM(CASE WHEN COMMAND = 'Sleep' THEN 1 ELSE 0 END) AS IDL
FROM information_schema.PROCESSLIST
```

### SQL 9: 等待事件 Top 10
```sql
-- Oracle: v$system_event + DB CPU (累计, delta)
-- MySQL:
SELECT EVENT_NAME AS event,
       COUNT_STAR AS waits,
       ROUND(SUM_TIMER_WAIT / 1e12, 3) AS time_sec,
       SUBSTRING_INDEX(EVENT_NAME, '/', 2) AS wait_class
FROM performance_schema.events_waits_summary_global_by_event_name
WHERE EVENT_NAME NOT LIKE 'wait/idle/%'
  AND COUNT_STAR > 0
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 10
```
备注：MySQL 的 `performance_schema.events_waits_summary_*` 提供累计值，支持 delta 计算，与 Oracle 类似。

### SQL 10: 活跃会话列表
```sql
-- Oracle: v$session + v$sql WHERE ACTIVE
-- MySQL:
SELECT
  p.ID AS sid,
  p.USER AS username,
  LEFT(MD5(p.INFO), 13) AS sql_id,  -- MySQL 无 sql_id，用 digest 近似
  COALESCE(w.EVENT_NAME, 'ON CPU') AS event,
  COALESCE(SUBSTRING_INDEX(w.EVENT_NAME, '/', 2), 'CPU') AS wait_class,
  p.TIME AS elapsed_sec,
  LEFT(p.INFO, 80) AS sql_text,
  SUBSTRING_INDEX(p.HOST, ':', 1) AS program,
  p.COMMAND AS status
FROM information_schema.PROCESSLIST p
LEFT JOIN performance_schema.events_waits_current w
  ON w.THREAD_ID = (SELECT THREAD_ID FROM performance_schema.threads WHERE PROCESSLIST_ID = p.ID)
WHERE p.COMMAND != 'Sleep'
ORDER BY p.TIME DESC
LIMIT 20
```

### SQL 11: Burst 模式扩展
```sql
-- 在 SQL 10 基础上加入阻塞信息
SELECT
  p.ID AS sid, p.USER, LEFT(p.INFO, 200) AS sql_text,
  COALESCE(w.EVENT_NAME, 'ON CPU') AS event,
  p.TIME AS elapsed_sec,
  COALESCE(
    (SELECT BLOCKING_THREAD_ID FROM performance_schema.data_lock_waits
     WHERE REQUESTING_THREAD_ID = t.THREAD_ID LIMIT 1), 0
  ) AS blocking_sid,
  SUBSTRING_INDEX(p.HOST, ':', 1) AS machine
FROM information_schema.PROCESSLIST p
JOIN performance_schema.threads t ON t.PROCESSLIST_ID = p.ID
LEFT JOIN performance_schema.events_waits_current w ON w.THREAD_ID = t.THREAD_ID
WHERE p.COMMAND != 'Sleep'
ORDER BY p.TIME DESC
```

---

## 3. PostgreSQL 等价 SQL

### SQL 1: 数据库角色
```sql
-- Oracle: SELECT database_role FROM v$database
-- PG:
SELECT CASE WHEN pg_is_in_recovery() THEN 'STANDBY' ELSE 'PRIMARY' END AS database_role
```

### SQL 2-3: 内存（SGA → shared_buffers）
```sql
-- Oracle: SGA used/max
-- PG: shared_buffers 使用情况（需要 pg_buffercache 扩展）
SELECT
  COUNT(*) * 8192 / 1048576 AS used_mb,
  (SELECT setting::bigint * 8192 / 1048576 FROM pg_settings WHERE name = 'shared_buffers') AS max_mb
FROM pg_buffercache
WHERE reldatabase IS NOT NULL
```
备注：无 `pg_buffercache` 扩展时，退化为只显示 `shared_buffers` 配置值（无法获取实际使用量）。

### SQL 4-5: 内存（PGA → work_mem）
```sql
-- Oracle: PGA used / limit
-- PG: 无直接等价。显示 work_mem 配置 + 当前 temp 使用
SELECT
  (SELECT setting FROM pg_settings WHERE name = 'work_mem') AS work_mem,
  (SELECT setting FROM pg_settings WHERE name = 'maintenance_work_mem') AS maint_work_mem,
  (SELECT SUM(temp_bytes) FROM pg_stat_database) AS temp_bytes_total
```

### SQL 6: CPU/Wait 比率
```sql
-- Oracle: v$sys_time_model
-- PG: 无直接等价。近似用活跃会话比例
SELECT
  COUNT(*) FILTER (WHERE state = 'active') AS active,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type IS NULL) AS on_cpu,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type IS NOT NULL) AS waiting,
  (SELECT setting::int FROM pg_settings WHERE name = 'max_connections') AS max_conn
FROM pg_stat_activity
WHERE backend_type = 'client backend'
```
- `db%` ≈ active / max_connections × 100
- `WTR%` ≈ waiting / active × 100

### SQL 7: 吞吐量
```sql
-- Oracle: v$sysstat delta
-- PG:
SELECT
  xact_commit AS commits,
  xact_rollback AS rollbacks,
  tup_returned + tup_fetched + tup_inserted + tup_updated + tup_deleted AS total_ops,
  COALESCE((SELECT wal_bytes FROM pg_stat_wal), 0) AS wal_bytes  -- PG 14+
FROM pg_stat_database
WHERE datname = current_database()
```
- TPS = delta(commits + rollbacks) / interval
- QPS ≈ delta(total_ops) / interval
- Redo KB/s = delta(wal_bytes) / 1024 / interval

### SQL 8: 会话计数
```sql
-- Oracle: v$session COUNT
-- PG:
SELECT
  COUNT(*) AS total,
  COUNT(*) FILTER (WHERE state = 'active') AS active,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type IS NULL) AS on_cpu,
  COUNT(*) FILTER (WHERE state = 'active' AND wait_event_type = 'IO') AS io_wait,
  COUNT(*) FILTER (WHERE state = 'idle') AS idle
FROM pg_stat_activity
WHERE backend_type = 'client backend'
```

### SQL 9: 等待事件分布
```sql
-- Oracle: v$system_event (累计 delta)
-- PG: 只有当前快照，无累计值
SELECT
  COALESCE(wait_event, 'ON CPU') AS event,
  COUNT(*) AS sessions,
  COALESCE(wait_event_type, 'CPU') AS wait_class
FROM pg_stat_activity
WHERE state = 'active' AND backend_type = 'client backend'
GROUP BY wait_event, wait_event_type
ORDER BY sessions DESC
LIMIT 10
```

**重大差异**：PG 没有累计等待时间，只能统计**当前快照的会话数分布**。
- Oracle dbtop 显示等待时间占比（百分比柱状图）
- PG dbtop 改为显示等待会话数占比（或 opendb 自建采样累计）

### SQL 10: 活跃会话列表
```sql
-- Oracle: v$session + v$sql
-- PG:
SELECT
  pid AS sid,
  usename AS username,
  COALESCE(LEFT(query, 13), '') AS sql_id,  -- PG 无 sql_id
  COALESCE(wait_event, 'ON CPU') AS event,
  COALESCE(wait_event_type, 'CPU') AS wait_class,
  EXTRACT(EPOCH FROM clock_timestamp() - query_start)::numeric(10,1) AS elapsed_sec,
  LEFT(query, 80) AS sql_text,
  COALESCE(application_name, '') AS program,
  state AS status
FROM pg_stat_activity
WHERE state = 'active' AND backend_type = 'client backend'
  AND pid != pg_backend_pid()
ORDER BY query_start
LIMIT 20
```

### SQL 11: Burst 模式扩展
```sql
-- 加入阻塞信息
SELECT
  a.pid AS sid, a.usename, LEFT(a.query, 200) AS sql_text,
  COALESCE(a.wait_event, 'ON CPU') AS event,
  COALESCE(a.wait_event_type, 'CPU') AS wait_class,
  EXTRACT(EPOCH FROM clock_timestamp() - a.query_start)::numeric(10,1) AS elapsed_sec,
  COALESCE(client_addr::text, 'local') AS machine,
  COALESCE((pg_blocking_pids(a.pid))[1], 0) AS blocking_pid
FROM pg_stat_activity a
WHERE a.state = 'active' AND a.backend_type = 'client backend'
  AND a.pid != pg_backend_pid()
ORDER BY a.query_start
```

---

## 4. Snapshot 结构三库差异

| 字段 | Oracle | MySQL | PostgreSQL |
|------|--------|-------|-----------|
| DBRole | `v$database.database_role` | `@@read_only` + REPLICA STATUS | `pg_is_in_recovery()` |
| SGAUsedMB / SGAMaxMB | `v$sgastat` / `v$sga` | **替换为** BPUsedMB / BPMaxMB (Buffer Pool) | **替换为** SBufUsedMB / SBufMaxMB (shared_buffers) |
| PGAUsedMB / PGAMaxMB | `v$pgastat` / `v$parameter` | **替换为** ConnMemMB (总连接内存) | **替换为** WorkMemMB (配置值) |
| DBPercent | DB time delta / elapsed | Threads_running 近似 | active / max_connections 近似 |
| WTRPercent | (DB time - CPU time) / DB time | 等待会话 / running 近似 | waiting / active 近似 |
| TPS | user commits + rollbacks delta | Com_commit + Com_rollback delta | xact_commit + xact_rollback delta |
| QPS | execute count delta | Queries delta | total_ops delta (近似) |
| RedoKBs | redo size delta | Innodb_os_log_written delta | wal_bytes delta (PG 14+) |
| Events[].TimeSec | 累计值，支持精确 delta | 累计值 (皮秒)，支持精确 delta | **无累计值**，只有当前快照会话数 |
| Sessions[].SQLID | `v$sql.sql_id` (13 chars) | 无原生 SQL ID，用 DIGEST 近似 | 无 SQL ID，用 queryid (pg_stat_statements) 或截取 |

---

## 5. 渲染面板改造

### Header Box 差异

| 行 | Oracle | MySQL | PostgreSQL |
|----|--------|-------|-----------|
| 标题 | `oracle 19c` | `mysql 8.0` | `postgresql 16` |
| 内存行 | `SGA [████░░░░] 2.1G/4.0G  PGA [██░░░░░░] 512M/2.0G` | `BP [████░░░░] 6.1G/8.0G  ConnMem 128M` | `SBuf [████░░░░] 1.2G/2.0G  WorkMem 256MB` |
| 指标行 | `db% 45  WTR% 22  SN 120  AN 8 ...` | `db% 45  WTR% 22  SN 120  AN 8 ...` (计算方式不同) | `db% 45  WTR% 22  SN 120  AN 8 ...` (计算方式不同) |

### Events Box 差异

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | 累计等待时间 delta | 累计等待时间 delta (皮秒→秒) | **当前快照会话数分布** |
| 柱状图含义 | 时间占比 (%) | 时间占比 (%) | 会话数占比 (%) |
| 排序 | by DTimeMs DESC | by delta SUM_TIMER_WAIT DESC | by session count DESC |
| CPU 行 | `DB CPU` from `v$sys_time_model` | `CPU` = Threads_running - waiting | `ON CPU` = active - waiting |

### Sessions Box

三库格式基本一致（SID/User/Event/Elapsed/SQL），差异：
- **会话标识**：Oracle SID / MySQL ID / PG pid
- **SQL 标识**：Oracle sql_id / MySQL DIGEST / PG queryid
- **等待事件名**：三库命名体系完全不同

---

## 6. 性能与刷新频率

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 正常模式 | 1s | 1s | 1s |
| Burst 模式 | 200ms | 200ms | **500ms**（pg_stat_activity 查询开销较大） |
| 采集 SQL 数 | 9 条（4 组并行） | ~8 条（4 组并行） | ~8 条（4 组并行） |
| 主要性能瓶颈 | 无（v$session 轻量） | `PROCESSLIST` 在高并发时较重 | `pg_stat_activity` 有 LWLock 开销 |
| 优化建议 | — | 优先用 `performance_schema.threads` 替代 `PROCESSLIST` | 缓存 `pg_buffercache` 结果（30s 更新一次） |

### PG 等待事件无累计值的处理方案

PG 的 `pg_stat_activity.wait_event` 只是当前快照，无历史累计。三种方案：

| 方案 | 说明 | 推荐 |
|------|------|------|
| A. 快照会话数 | 每帧统计各 wait_event 的会话数占比 | **推荐**（简单，信息足够） |
| B. opendb 自建累计 | 每帧采样 wait_event，内存中累计 count | 可选（更接近 Oracle 体验） |
| C. pg_wait_sampling 扩展 | 使用第三方扩展采样 | 不推荐（依赖扩展安装） |

---

## 7. Collector 代码结构（三库统一接口）

```go
// 壳层接口（公用）
type DbtopCollector interface {
    Collect(ctx context.Context) Snapshot
    SetMode(m CollectMode)
    Mode() CollectMode
}

// Oracle 实现 → internal/oracle/monitor/dbtop/collector.go
// MySQL 实现  → internal/mysql/monitor/dbtop/collector.go
// PG 实现     → internal/postgres/monitor/dbtop/collector.go
```

### Snapshot 结构保持统一

Snapshot 结构体放在壳层（`internal/monitor/dbtop/types.go`），三库共用。字段名统一（如 `MemPrimaryUsedMB` / `MemPrimaryMaxMB` 替代 Oracle 特有的 SGA/PGA），渲染时根据 DBType 显示不同标签。

或者更简单：每个产品有自己的 Snapshot 和 Renderer（遵循"独立优于复用"原则），只共享 `CollectMode`、`HealthLevel` 等极少量类型。

### 推荐方案

遵循多数据库架构策略"有任何差别的全部独立"原则：
- **types.go** 中的 `CollectMode`、`HealthLevel` 放壳层
- **Snapshot**、**Collector**、**Renderer** 各产品独立
- 渲染布局统一 28 行，但内存面板标签、Events 面板含义各自定义

---

## 8. 搬家清单

| 来源 | 目标 | 文件数 |
|------|------|--------|
| `internal/monitor/dbtop/collector.go` | `internal/oracle/monitor/dbtop/collector.go` | |
| `internal/monitor/dbtop/types.go` | 壳层保留 `CollectMode` + `HealthLevel`；Snapshot/SessionRow/WaitEvent 搬到 oracle | |
| `internal/monitor/dbtop/renderer.go` | `internal/oracle/monitor/dbtop/renderer.go` | |
| `internal/monitor/dbtop/delta.go` | `internal/oracle/monitor/dbtop/delta.go` | |
| `internal/monitor/dbtop/health.go` | `internal/oracle/monitor/dbtop/health.go` | |
| 测试文件 (5 个 _test.go) | 随代码一起搬 | |

壳层保留：
```go
// internal/monitor/dbtop/mode.go (壳层公用)
type CollectMode uint8
const (ModeNormal CollectMode = iota; ModeBurst)

type HealthLevel uint8
const (Healthy HealthLevel = iota; Warning; Critical)
```
