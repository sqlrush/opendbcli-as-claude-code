# 查询类 Skill 三库对比明细

## 1. /sql — SQL 执行器

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 读写检测 | SELECT/SHOW/DESCRIBE/DESC/EXPLAIN/WITH → Query | SELECT/SHOW/DESCRIBE/EXPLAIN/WITH → Query | SELECT/SHOW/EXPLAIN/WITH → Query |
| 多语句 | 分号分割，逐条执行 | 同 | 同 |
| 事务 | 自动 COMMIT（除非在 TX 中） | 自动 COMMIT (autocommit=1) | 自动 COMMIT |

**此 Skill 理论上通用**，但需注意：
- Oracle 用 `FETCH FIRST N ROWS ONLY`，MySQL 用 `LIMIT N`，PG 用 `LIMIT N`
- Oracle 的 `DUAL` 表，MySQL 可选，PG 不需要
- Oracle 特有语法如 `CONNECT BY`, `ROWNUM`，MySQL/PG 不支持

---

## 2. /topsql — Top SQL

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$sql` | `performance_schema.events_statements_summary_by_digest` | `pg_stat_statements` (扩展) |
| SQL 标识 | sql_id (13 chars) | DIGEST (32 chars MD5) | queryid (int64) |
| 时间窗口 | last_active_time 过滤 | LAST_SEEN 过滤 | 无时间过滤（全部累计，需 reset） |
| 指标 | elapsed_time, executions, buffer_gets | SUM_TIMER_WAIT, COUNT_STAR, SUM_ROWS_EXAMINED | total_exec_time, calls, shared_blks_hit+read |
| SQL 文本 | sql_text (v$sql) | DIGEST_TEXT (模板化) | query (模板化) |

### MySQL 等价
```sql
SELECT DIGEST_TEXT AS sql_text,
       SCHEMA_NAME,
       COUNT_STAR AS exec_count,
       ROUND(SUM_TIMER_WAIT / 1e12, 2) AS total_sec,
       ROUND(SUM_TIMER_WAIT / COUNT_STAR / 1e12, 4) AS avg_sec,
       SUM_ROWS_EXAMINED AS rows_examined,
       SUM_ROWS_SENT AS rows_sent
FROM performance_schema.events_statements_summary_by_digest
WHERE LAST_SEEN > NOW() - INTERVAL :1 MINUTE
AND COUNT_STAR > 0
ORDER BY SUM_TIMER_WAIT DESC
LIMIT 20
```

### PostgreSQL 等价
```sql
SELECT queryid, query,
       calls,
       ROUND(total_exec_time::numeric / 1000, 2) AS total_sec,
       ROUND((total_exec_time / calls)::numeric / 1000, 4) AS avg_sec,
       shared_blks_hit + shared_blks_read AS total_blks,
       ROUND((shared_blks_hit + shared_blks_read)::numeric / NULLIF(calls, 0)) AS avg_blks
FROM pg_stat_statements
WHERE calls > 0
ORDER BY total_exec_time DESC
LIMIT 20
```

**注意**：PG 的 `pg_stat_statements` 是累计值，没有时间窗口过滤。需要 `pg_stat_statements_reset()` 或 opendb 自建差值快照。

### 排序键差异

| 排序键 | Oracle (v$sql) | MySQL (perf_schema) | PG (pg_stat_statements) |
|--------|---------------|--------------------|-----------------------|
| el (elapsed) | elapsed_time | SUM_TIMER_WAIT | total_exec_time |
| ae (avg elapsed) | elapsed_time/executions | AVG_TIMER_WAIT | mean_exec_time |
| lr (logical reads) | buffer_gets | SUM_ROWS_EXAMINED (近似) | shared_blks_hit + shared_blks_read |
| pr (physical reads) | disk_reads | SUM_NO_INDEX_USED (近似) | shared_blks_read |
| al (avg logical) | buffer_gets/executions | SUM_ROWS_EXAMINED/COUNT_STAR | (blks_hit+blks_read)/calls |
| ap (avg physical) | disk_reads/executions | 无直接等价 | shared_blks_read/calls |
| ex (executions) | executions | COUNT_STAR | calls |

---

## 3. /ash — ASH 分析

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | Active Session History = 每秒采样 | **无等价** | **无等价** |
| 数据源 | `v$active_session_history` | 需 opendb 自建采样 | 需 opendb 自建采样 |
| 采样粒度 | 1 秒/session | - | - |

### 重大差异
ASH 是 Oracle 独有功能，MySQL 和 PG 都没有内置等价物。

**MySQL 替代方案**：
- `performance_schema.events_statements_history` — 只保留最近 10 条/线程
- `performance_schema.events_statements_history_long` — 最近 10000 条全局
- 不按秒采样，而是按语句完成事件记录
- opendb 可自建采样 goroutine，每秒查 `SHOW PROCESSLIST` 保存快照

**PostgreSQL 替代方案**：
- `pg_stat_activity` 只是当前快照
- `pg_stat_statements` 是累计统计
- opendb 可自建采样 goroutine，每秒查 `pg_stat_activity` 保存快照
- 也可使用 `pg_wait_sampling` 扩展（如果安装了）

### 命令改名建议
- MySQL/PG：仍叫 `/ash`，但底层用 opendb 自建采样（类似 Oracle 的行为）
- 或者改名 `/activity` 更准确

---

## 4. /slowsql — 慢 SQL

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 数据源 | `v$sql WHERE elapsed_time > threshold` | `performance_schema.events_statements_summary_by_digest` 或 `slow_query_log` | `pg_stat_statements WHERE mean_exec_time > threshold` |
| 阈值单位 | 毫秒 (自定义) | 毫秒 (自定义) / `long_query_time` (秒) | 毫秒 (自定义) |

### MySQL 等价
```sql
SELECT DIGEST_TEXT, SCHEMA_NAME,
       COUNT_STAR AS exec_count,
       ROUND(MAX_TIMER_WAIT / 1e12, 2) AS max_sec,
       ROUND(AVG_TIMER_WAIT / 1e12, 2) AS avg_sec,
       SUM_ROWS_SENT, SUM_ROWS_EXAMINED
FROM performance_schema.events_statements_summary_by_digest
WHERE AVG_TIMER_WAIT / 1e9 > :threshold_ms
ORDER BY MAX_TIMER_WAIT DESC LIMIT 20
```

### PostgreSQL 等价
```sql
SELECT queryid, query, calls,
       ROUND(mean_exec_time::numeric, 2) AS avg_ms,
       ROUND(max_exec_time::numeric, 2) AS max_ms,
       rows
FROM pg_stat_statements
WHERE mean_exec_time > :threshold_ms
ORDER BY mean_exec_time DESC LIMIT 20
```

---

## 5. /explain — 执行计划

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| SQL 文本 | `EXPLAIN PLAN FOR` + `DBMS_XPLAN.DISPLAY()` | `EXPLAIN [FORMAT=TREE/JSON] <sql>` | `EXPLAIN (ANALYZE, BUFFERS) <sql>` |
| SQL ID | `DBMS_XPLAN.DISPLAY_CURSOR(sql_id)` | 无（MySQL 不支持按历史 SQL ID 查计划） | `auto_explain` 模块 + 日志查看 |
| 输出格式 | 表格化（管道分隔） | TREE (8.0.16+) / TRADITIONAL / JSON | TEXT / JSON / YAML / XML |
| 实际执行 | 只估算（DISPLAY），可看实际（DISPLAY_CURSOR） | `EXPLAIN ANALYZE` (实际执行!) | `EXPLAIN ANALYZE` (实际执行!) |

### MySQL 等价
```sql
EXPLAIN FORMAT=TREE SELECT ...;
-- 或
EXPLAIN ANALYZE SELECT ...;  -- MySQL 8.0.18+ (会实际执行!)
```

### PostgreSQL 等价
```sql
EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT) SELECT ...;
-- 注意：ANALYZE 会实际执行 SQL!
```

### 关键差异
- **Oracle** 可以通过 `DBMS_XPLAN.DISPLAY_CURSOR(sql_id)` 查看已执行 SQL 的计划，MySQL/PG 无此能力
- **MySQL/PG** 的 `EXPLAIN ANALYZE` 会**实际执行 SQL**，对写操作危险 → 需要安全保护（自动加 BEGIN/ROLLBACK 包裹，或只允许 SELECT）
- **输出解析**：三库格式完全不同，需各自独立的 plan parser

---

## 6. /awr — AWR 分析

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | AWR = 自动工作负载仓库 | **无等价** | **无等价** |
| 数据源 | `dba_hist_snapshot`, `dba_hist_sqlstat`, `dba_hist_system_event` | - | - |
| 快照间隔 | 默认 60 分钟 | - | - |

### 重大差异
AWR 是 Oracle 独有功能（且需要 Diagnostic Pack License）。

**MySQL 替代方案**：
- `performance_schema.events_statements_summary_by_digest` 有 FIRST_SEEN/LAST_SEEN，但不按快照
- `sys.statement_analysis` 可做类似分析
- opendb 的 `/perfsnap` 可替代部分 AWR 功能（自建快照对比）

**PostgreSQL 替代方案**：
- `pg_stat_statements` 累计统计
- `pg_stat_bgwriter` 系统级统计
- `pg_stat_database` 数据库级统计
- opendb 的 `/perfsnap` 自建快照对比

### 命令改造
MySQL/PG 版**不提供 /awr**，改为增强的 `/perfsnap`（opendb 自建快照机制）。

---

## 7. /planhistory — 计划历史

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 概念 | 同一 SQL 的历史执行计划变更 | **无等价** | **无直接等价** |
| 数据源 | `dba_hist_sqlstat` (plan_hash_value) | - | `pg_stat_statements` (只有一个 plan 统计) |

### 重大差异
- Oracle 通过 AWR 保存每个快照期间每个 SQL 的 plan_hash_value，可以检测计划回退
- MySQL 完全没有计划历史
- PG 的 `pg_stat_statements` 不区分 plan_hash

**建议**：MySQL/PG 不提供此 Skill。如需要，opendb 自建计划快照。

---

## 8. /ora — ORA 错误知识库

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 错误格式 | ORA-XXXXX | MySQL Error XXXX (ER_xxx) | SQLSTATE XXXXX / PG error code |
| 错误扫描 | `v$diag_alert_ext` | `performance_schema.error_log` (8.0.22+) | PostgreSQL 日志文件 |
| 知识库 | 60+ ORA 错误条目 | 需要新建 MySQL 错误知识库 | 需要新建 PG 错误知识库 |

### MySQL 等价：/mysqlerr
需要新建 MySQL 错误知识库，常见错误如：
- 1040 (Too many connections)
- 1205 (Lock wait timeout exceeded)
- 1213 (Deadlock found)
- 1062 (Duplicate entry)
- 2002/2003 (Can't connect)
- 1045 (Access denied)
- 1146 (Table doesn't exist)
- 1366 (Incorrect value)
- 1114 (Table is full)
- 1594 (Relay log read failure)
- 等等...

### PostgreSQL 等价：/pgerr
需要新建 PG 错误知识库，常见错误如：
- 53300 (too_many_connections)
- 40P01 (deadlock_detected)
- 23505 (unique_violation)
- 57014 (query_canceled)
- 53100 (disk_full)
- 55P03 (lock_not_available)
- 42P01 (undefined_table)
- 08006 (connection_failure)
- 25P02 (in_failed_sql_transaction)
- 等等...

**工作量**：每个库需要 50-100 个错误条目，每个含原因、诊断命令、修复建议。

---

## 9. /topsql 排序键对照

| 键 | 含义 | Oracle | MySQL | PostgreSQL |
|----|------|--------|-------|-----------|
| el | 总耗时 | elapsed_time | SUM_TIMER_WAIT | total_exec_time |
| ae | 平均耗时 | elapsed_time/executions | AVG_TIMER_WAIT | mean_exec_time |
| lr | 逻辑读 | buffer_gets | SUM_ROWS_EXAMINED (近似) | shared_blks_hit + shared_blks_read |
| pr | 物理读 | disk_reads | SUM_NO_INDEX_USED (近似) | shared_blks_read |
| al | 平均逻辑读 | buffer_gets/executions | 计算 | 计算 |
| ap | 平均物理读 | disk_reads/executions | 无直接等价 | shared_blks_read/calls |
| ex | 执行次数 | executions | COUNT_STAR | calls |

### MySQL 额外排序键
- `re` — rows_examined (扫描行数，MySQL 特色)
- `rs` — rows_sent (返回行数)
- `tmp` — SUM_CREATED_TMP_TABLES (创建临时表数)

### PostgreSQL 额外排序键
- `blk` — shared_blks_dirtied (脏块数)
- `tmp` — temp_blks_written (临时文件写入)
- `wal` — wal_bytes (WAL 生成量，PG 13+)
