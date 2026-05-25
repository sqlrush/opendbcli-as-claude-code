# dbtop 健康指标三库对比审计

> 专项: dbtop 指标采集策略扫描
> 日期: 2026-03-30
> 范围: MySQL / Oracle / PostgreSQL

---

## 一、吞吐量指标

### 1. TPS (Transactions Per Second)

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **数据源** | `performance_schema.global_status` | `v$sysstat` | `pg_stat_database` |
| **计数器** | `Handler_commit` + `Handler_rollback` | `user commits` + `user rollbacks` | `xact_commit` + `xact_rollback` |
| **公式** | `(commits_delta + rollbacks_delta) / elapsed` | 同左 | 同左 |
| **autocommit 隐式提交** | ✅ 计入 | ✅ 计入 | ✅ 计入 |
| **计数层** | 存储引擎层 | 事务管理层 | 事务管理层 |

**状态: ✅ 一致** (MySQL 已从 `Com_commit` 修正为 `Handler_commit`)

---

### 2. QPS (Queries Per Second)

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **数据源** | `performance_schema.global_status` | `v$sysstat` | `pg_stat_database` |
| **计数器** | `Queries` | `execute count` | `tup_returned + tup_fetched + tup_inserted + tup_updated + tup_deleted` |
| **含义** | 服务端接收的所有语句数 (含 COM_QUERY, COM_STMT_EXECUTE 等) | SQL 解析+执行次数 (含递归SQL) | **元组操作数** (非语句数) |
| **含管理语句** | ✅ 含 SHOW/SET 等 | ✅ 含 | ❌ 不含 |
| **含递归/内部SQL** | ❌ 不含 | ✅ 含 | ❌ 不含 |

**状态: ⚠️ 差异显著**

| 问题 | 说明 |
|------|------|
| PG 语义不同 | PG 计的是元组操作量，不是语句数。一条 `SELECT * FROM t` 返回 1000 行则 QPS +1000，而 MySQL/Oracle 只 +1 |
| Oracle 含递归 | `execute count` 包含 PL/SQL 内部递归执行，比用户可见语句数偏高 |
| 显示标签 | 三库都标为 "QPS"，但实际含义不同 |

---

### 3. Redo / WAL 写入速率

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **数据源** | `performance_schema.global_status` | `v$sysstat` | `pg_stat_wal` (PG14+) |
| **计数器** | `Innodb_os_log_written` | `redo size` | `wal_bytes` |
| **单位** | bytes → KB/s | bytes → KB/s | bytes → KB/s |
| **显示标签** | `Redo` | `REDO` | `WAL` |
| **降级处理** | 无 | 无 | PG < 14 返回 0 |

**状态: ✅ 一致** (语义等价，标签符合各库习惯)

---

## 二、内存指标

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **指标名** | BP (Buffer Pool) | SGA + PGA | SBuf + CacheHit |
| **已用** | `Innodb_buffer_pool_bytes_data + bytes_dirty` | `v$sgastat` SUM(bytes) | `shared_buffers` setting (静态) |
| **上限** | `@@innodb_buffer_pool_size` | `v$sga` SUM(value) / `pga_aggregate_limit` | 硬编码 32GB 做 bar 比例 |
| **命中率** | ❌ 不显示 | ❌ 不显示 | ✅ `blks_hit / (blks_hit + blks_read)` |
| **显示格式** | `BP [bar] used/max` | `SGA [bar] used  PGA [bar] used` | `SBuf [bar] size  CacheHit [bar] pct` |

**状态: ⚠️ 设计差异 (各库内存模型不同，合理)**

| 问题 | 说明 |
|------|------|
| PG SBuf 是静态值 | `shared_buffers` 是配置参数不是实时使用量，bar 永远不变 |
| PG bar 上限硬编码 | 用 32GB 做基准，如果配 64GB shared_buffers 则 bar 溢出到 100% |
| MySQL 无命中率 | 可考虑补充 `Innodb_buffer_pool_read_requests` vs `Innodb_buffer_pool_reads` |
| Oracle 无命中率 | 可考虑补充 `v$sysstat` buffer cache hit ratio |

---

## 三、负载指标

### 5. db% (数据库繁忙度)

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **公式** | `Threads_running / max_connections * 100` | `DB_CPU_delta / wall_time_delta * 100` | `ActiveCount` (原始活跃会话数) |
| **含义** | 运行线程占最大连接比 | CPU 时间占比 (类似 OS CPU%) | 活跃会话绝对数 |
| **单位** | 百分比 (0-100) | 百分比 (0-100+) | 会话数 (0-N) |
| **bar 缩放** | 直接用百分比 | 直接用百分比 | `min(ActiveCount * 10, 100)` |

**状态: ⚠️ 差异显著**

| 问题 | 说明 |
|------|------|
| 三库语义不同 | MySQL = 连接使用率, Oracle = CPU 使用率, PG = 活跃会话数 |
| MySQL 含义偏弱 | `Threads_running / max_connections` 反映的是连接池压力，不是 CPU 繁忙度 |
| PG 非百分比 | db% 值可能是 0, 1, 5, 50，跟 MySQL/Oracle 的百分比不可比 |

---

### 6. WTR% (等待时间比)

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **是否实现** | ❌ 缺失 | ✅ | ✅ |
| **公式** | — | `(DB_time - DB_CPU) / DB_time * 100` | `waiting / active * 100` |
| **含义** | — | 等待时间占 DB time 比 | 等待会话占活跃会话比 |
| **数据源** | — | `v$sys_time_model` | `pg_stat_activity` |

**状态: ⚠️ MySQL 缺失**

| 问题 | 说明 |
|------|------|
| MySQL 不显示 WTR% | Header 行只有 `db%`，无 `WTR%` |
| PG 语义是会话比 | Oracle 是时间维度，PG 是计数维度 |

---

## 四、会话指标

### 7. 会话计数

| 字段 | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **Total** | `Threads_connected` | `COUNT(v$session)` | `COUNT(pg_stat_activity)` |
| **Active** | `Threads_running` | `status='ACTIVE' AND type='USER'` | `state='active'` |
| **ActiveCPU** | ❌ 缺失 | `wait_class NOT IN ('Idle','User I/O','System I/O')` | `state='active' AND wait_event_type IS NULL` |
| **ActiveIO** | ❌ 缺失 | `wait_class IN ('User I/O','System I/O')` | `state='active' AND wait_event_type='IO'` |
| **Idle** | `connected - running` | `status<>'ACTIVE' OR type<>'USER'` | `state='idle'` |
| **Waiting** | ❌ 缺失 | ❌ 缺失 | `wait_event_type IS NOT NULL AND state='active'` |
| **显示标签** | `SN AN Idle` | `Session Active ActiveCPU ActiveIO Idle` | `Session Active ActiveCPU ActiveIO Idle` |

**状态: ⚠️ MySQL 缺 ActiveCPU / ActiveIO / Waiting**

---

## 五、等待事件

### 8. Top Wait Events

| | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **数据源** | `performance_schema.events_waits_summary_global_by_event_name` | `v$system_event` + `v$sys_time_model (DB CPU)` | `pg_stat_activity` |
| **采集方式** | 累计计数器，帧间求 delta | 累计计数器，帧间求 delta | **实时快照会话数**，跨帧累加 (ASH-style) |
| **Delta 字段** | DCount / DTimeMs / DPCT | DWaits / DTimeMs / DPCT | Sessions / Percentage |
| **累计字段** | Count / TimeSec / PCT | Waits / TimeSec / PCT | CumulSessions / CumulPct |
| **时间精度** | 皮秒 (int64 无损) | 微秒 (float64 round-trip) | 采样估算 `sessions × interval` |
| **含 DB CPU** | ❌ | ✅ (从 v$sys_time_model 注入) | ✅ (wait_event IS NULL → "On CPU") |
| **WaitClass** | ❌ | ✅ | ✅ (wait_event_type) |

**状态: ⚠️ 差异显著**

| 问题 | 说明 |
|------|------|
| MySQL 无 DB CPU | 等待事件中不含 CPU 时间项，Oracle/PG 都有 |
| Oracle 精度损失 | delta 通过 float64 round-trip (micro→sec→micro)，大累计值可能丢精度 |
| PG 时间是估算 | `sessions × interval` 只是近似值，不如 MySQL/Oracle 的精确计数器 |

---

## 六、活跃会话列表

### 9. Active Sessions

| 字段 | MySQL | Oracle | PostgreSQL |
|---|---|---|---|
| **会话标识** | `ID` (PROCESSLIST.ID) | `SID` (v$session) | `PID` (pg_stat_activity) |
| **用户** | `USER` | `USERNAME` | `usename` |
| **SQL标识** | ❌ 缺失 | `SQL_ID` | `query_id` (PG14+) |
| **等待事件** | ❌ 缺失 | `EVENT` | `wait_event` |
| **等待类** | ❌ 缺失 | `WAIT_CLASS` | `wait_event_type` |
| **执行时间** | `TIME` (秒, 整数) | `SYSDATE - SQL_EXEC_START` (秒, 小数) | `clock_timestamp() - query_start` (秒, 小数) |
| **SQL文本** | `LEFT(INFO, 80)` | `SUBSTR(SQL_TEXT, 1, 80)` | `LEFT(query, 80)` |
| **命令类型** | `COMMAND` | ❌ | ❌ |
| **状态** | `STATE` | `STATUS` | ❌ |
| **程序名** | ❌ 缺失 | `PROGRAM` | ❌ |
| **Burst 模式** | ❌ | ✅ (blocking_sid, plan_hash, machine, wait_time, child_number) | ❌ |
| **数据来源** | `information_schema.PROCESSLIST` | `v$session + v$sql` | `pg_stat_activity` |

**状态: ⚠️ MySQL 字段最少，Oracle 最丰富**

| 问题 | 说明 |
|------|------|
| MySQL 无 SQL_ID / 等待事件 | `PROCESSLIST` 不含等待详情，需改用 `performance_schema.threads` + `events_waits_current` |
| MySQL 无 Burst 模式 | Oracle 有扩展字段 (blocking, plan hash)，MySQL/PG 无 |
| PG 无 query_id 降级 | PG < 14 无 query_id，有 fallback SQL |

---

## 七、健康评估阈值

### 10. Health Thresholds

| 阈值项 | MySQL | Oracle | PostgreSQL | 是否一致 |
|--------|-------|--------|------------|----------|
| **AN Warning** | 20 | 30 | 20 | ⚠️ Oracle 偏高 |
| **AN Critical** | 50 | 80 | 50 | ⚠️ Oracle 偏高 |
| **db% Warning** | 30 | 50 | 5 | ⚠️ 三库不同 |
| **db% Critical** | 60 | 80 | 10 | ⚠️ 三库不同 |
| **WTR% Warning** | — | 30 | 30 | ✅ (MySQL 缺失) |
| **WTR% Critical** | — | 60 | 60 | ✅ (MySQL 缺失) |
| **TPS Drop Warning** | 50% | 50% | 50% | ✅ 一致 |
| **TPS Drop Critical** | 80% | 80% | 80% | ✅ 一致 |
| **Elapsed Warning** | 300s | 300s | 300s | ✅ 一致 |
| **Elapsed Critical** | 600s | 600s | 600s | ✅ 一致 |
| **Event PCT Warning** | ❌ 不检查 | 30% | 30% | ⚠️ MySQL 缺失 |
| **Event PCT Critical** | ❌ 不检查 | 50% | 50% | ⚠️ MySQL 缺失 |
| **CacheHit Warning** | ❌ (BP% > 95 报警) | ❌ | < 95% | ⚠️ 各不同 |

**状态: ⚠️ db% 阈值差异合理 (因 db% 定义不同)，AN 阈值差异需评估**

---

## 八、问题汇总

### 需修复 (Bug 级别)

| # | 问题 | 涉及库 | 状态 |
|---|------|--------|------|
| 1 | ~~MySQL TPS 不含 autocommit~~ | MySQL | ✅ 已修 — `Com_commit` → `Handler_commit` |
| 2 | ~~Oracle 等待事件 delta float64 丢精度~~ | Oracle | ✅ 已修 — 新增 `RawTimeMicro int64` 字段 |
| 3 | ~~PG SBuf bar 硬编码 32GB~~ | PG | ✅ 已修 — 移除 bar，仅显示配置值 |

### 需对齐 (一致性)

| # | 问题 | 状态 |
|---|------|------|
| 4 | ~~QPS 三库定义不同~~ | ✅ 已修 — PG 优先用 `pg_stat_statements.calls`，不可用时 fallback 元组操作 |
| 5 | ~~MySQL 缺 WTR%~~ | ✅ 已修 — `(Active - ActiveCPU) / Active * 100`，`performance_schema.threads` 提供 |
| 6 | ~~MySQL 缺 ActiveCPU / ActiveIO~~ | ✅ 已修 — 新增 `sessionCountSQL` 从 `performance_schema.threads` 采集 |
| 7 | ~~MySQL 无 SQL_ID / 等待事件~~ | ✅ 已修 — JOIN `events_statements_current` 获取 DIGEST，STATE 映射为 EVENT/CLASS |
| 8 | ~~MySQL 无 Event PCT 健康检查~~ | ✅ 已修 — health.go 新增 evtPctWarn/evtPctCrit |
| 9 | AN 阈值 Oracle 偏高 | 保留不改 — Oracle 场景合理 (大库常 30+ active) |

### 布局对齐

| # | 项目 | 状态 |
|---|------|------|
| 10 | ~~MySQL 会话列表列宽/命名~~ | ✅ 已修 — SID(5) USR(10) SQLID(13) EVENT(20) CLASS(10) E/T(5) SQL，与 Oracle 一致 |
| 11 | ~~PG 会话列表列宽/命名~~ | ✅ 已修 — PID(5) USR(10) QUERYID(13) EVENT(20) TYPE(10) E/T(5) SQL，与 Oracle 一致 |
| 12 | ~~MySQL Counts 行命名~~ | ✅ 已修 — `Session Active ActiveCPU ActiveIO Idle │ TPS QPS Redo`，与 Oracle 一致 |
| 13 | ~~MySQL Metrics 行~~ | ✅ 已修 — 新增 `WTR% [bar] val`，与 Oracle 一致 |

### 可增强 (Nice to have)

| # | 问题 | 建议 |
|---|------|------|
| 14 | MySQL/Oracle 无 CacheHit 指标 | 分别用 `Innodb_buffer_pool_read_requests/reads` 和 `v$sysstat buffer cache hit` |
| 15 | MySQL 无等待事件 DB CPU 项 | 需 `performance_schema.events_stages` (MySQL 无 `v$sys_time_model`) |
| 16 | MySQL/PG 无 Burst 模式 | MySQL: `data_locks` 获取 blocking，PG: `pg_blocking_pids()` |
