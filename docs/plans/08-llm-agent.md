# LLM 诊断 Agent 三库对比

## 1. System Prompt 改造

### Oracle (当前)
```
你是 OpenDB 数据库诊断专家。你的任务是分析 Oracle 数据库性能异常并给出诊断建议。
```

### MySQL (新建)
```
你是 OpenDB 数据库诊断专家。你的任务是分析 MySQL (InnoDB) 数据库性能异常并给出诊断建议。
```

### PostgreSQL (新建)
```
你是 OpenDB 数据库诊断专家。你的任务是分析 PostgreSQL 数据库性能异常并给出诊断建议。
```

## 2. Skill Reference 列表改造

### Oracle (当前 26 个查询命令 + 3 个操作命令)
详见当前 `opendbSkillReference` 常量。

### MySQL Skill Reference (预计)

```
查询类:
  /activesessions  — 活跃线程列表
  /sessions        — 所有连接概览
  /waits           — performance_schema 等待事件统计
  /locks           — InnoDB 行锁/MDL 锁
  /blocktree       — 层级阻塞链
  /syncwaits       — Mutex/Rwlock 争用
  /health          — 数据库健康检查
  /slowsql [ms]    — 慢查询 (默认 1000ms)
  /topsql [分钟] [排序] — Top SQL (排序: el/ae/lr/re/ex)
  /explain <sql>   — SQL 执行计划
  /params <名称>   — 搜索 MySQL 变量
  /space           — 数据库/表空间使用
  /bufferpool      — InnoDB Buffer Pool 详情
  /connmem         — 连接内存使用
  /binlog          — Binary Log 状态
  /replication     — 主从复制状态
  /jobs            — Event Scheduler 作业
  /resource        — 资源限制使用
  /alert           — 近期错误日志
  /backup          — 备份状态
  /segments        — 大表排名
  /indexhealth     — 索引健康检查
  /users           — 用户账户状态
  /os              — 操作系统指标

操作类 (需确认):
  /kill <id>               — 终止连接
  /kill <id> query         — 只取消当前查询
  /alter <变量> [值]       — 修改系统变量
  /gather [check|run]      — 收集表统计信息
```

### PostgreSQL Skill Reference (预计)

```
查询类:
  /activesessions  — 活跃会话列表
  /sessions        — 所有连接概览
  /waits           — 等待事件分布快照
  /locks           — 行锁/表锁/Advisory Lock
  /blocktree       — 层级阻塞链
  /lwlocks         — LWLock 争用分布
  /health          — 数据库健康检查
  /slowsql [ms]    — 慢查询 (默认 1000ms, 需 pg_stat_statements)
  /topsql [排序]   — Top SQL (排序: el/ae/lr/pr/ex/tmp/wal)
  /explain <sql>   — SQL 执行计划 (EXPLAIN ANALYZE)
  /params <名称>   — 搜索 PostgreSQL 参数
  /space           — 数据库/表空间使用
  /sharedbufs      — shared_buffers 使用详情
  /wal             — WAL 状态和归档
  /vacuum          — Vacuum 状态和 Dead Tuples
  /replication     — 流复制状态
  /jobs            — pg_cron 调度作业
  /resource        — 资源限制使用
  /alert           — 近期日志 (需 csvlog)
  /backup          — WAL 归档状态
  /segments        — 大表排名
  /bloat           — 表/索引膨胀估算
  /indexhealth     — 索引健康检查
  /users           — 用户账户状态
  /longtx          — 长事务检查
  /os              — 操作系统指标

操作类 (需确认):
  /kill <pid>              — 终止会话 (pg_terminate_backend)
  /cancel <pid>            — 只取消查询 (pg_cancel_backend)
  /alter <参数> [值]       — 修改系统参数 (ALTER SYSTEM)
  /gather [check|run]      — 收集表统计信息 (ANALYZE)
  /vacuum [table]          — 手动 VACUUM
```

## 3. Compress Report 改造

### Oracle BurstReport 字段 → MySQL/PG

| Oracle 字段 | MySQL 等价 | PG 等价 |
|------------|-----------|---------|
| WaitProfile (event, wait_class) | WaitProfile (event_name, event_category) | WaitProfile (wait_event, wait_event_type) |
| TopSQLs (sql_id, plan_hash) | TopSQLs (digest, schema_name) | TopSQLs (queryid) |
| BlockingChains (SID, serial#) | BlockingChains (processlist_id) | BlockingChains (pid) |
| SpaceDetails (tablespace) | SpaceDetails (database, table) | SpaceDetails (database, tablespace) |
| ParamDetails (v$parameter) | ParamDetails (global_variables) | ParamDetails (pg_settings) |
| Metrics (redo_kb_per_sec) | Metrics (binlog_kb_per_sec) | Metrics (wal_kb_per_sec) |
| RootCause (latch_storm) | RootCause (mutex_contention) | RootCause (lwlock_contention) |

### 压缩报告模板调整

Oracle 的 `根因判定` 输出类似：
```
类型: bad_sql (慢SQL争用)
```

MySQL 改为：
```
类型: slow_query (慢查询/锁争用)
```

PostgreSQL 改为：
```
类型: slow_query (慢查询/Seq Scan)
```

## 4. 诊断模式适用性

| 模式 | Oracle | MySQL | PostgreSQL |
|------|--------|-------|-----------|
| Playbook (1轮) | ✓ | ✓ | ✓ |
| Assist (3轮) | ✓ | ✓ | ✓ |
| Auto (10轮) | ✓ | ✓ | ✓ |

三库的诊断模式框架**完全通用**，只是：
1. System Prompt 不同（数据库类型、可用命令列表）
2. Compress 的字段名和根因类型不同
3. LLM 的知识背景需要针对各库调整

## 5. PromptLoop Action Block

### Oracle (当前)
```json
{"skill": "activesessions", "args": ""}
{"skill": "explain", "args": "4t8r7kz2xv2p1"}
{"skill": "kill", "args": "142"}
```

### MySQL (同样格式，不同 Skill 名)
```json
{"skill": "activesessions", "args": ""}
{"skill": "explain", "args": "SELECT * FROM orders WHERE ..."}
{"skill": "kill", "args": "142"}
```

### PostgreSQL (同样格式)
```json
{"skill": "activesessions", "args": ""}
{"skill": "explain", "args": "SELECT * FROM orders WHERE ..."}
{"skill": "kill", "args": "12345"}
```

**Action Block 格式通用**，只是 skill 名和参数含义因库而异。

## 6. Format/Render 逻辑

### 通用部分（可复用）
- Markdown 转终端格式 (##, ###, 代码块, 列表)
- SQL_ID / queryid 高亮
- 问题术语 / 解决术语着色
- Token 使用量显示
- Thinking Chain 渲染

### 需要调整的部分
| 功能 | Oracle | MySQL | PG |
|------|--------|-------|----|
| SQL 标识高亮 | 13字符 sql_id | 32字符 DIGEST | int64 queryid |
| 术语匹配 | "表空间","Redo","SGA","PGA" | "Buffer Pool","Binlog","InnoDB" | "WAL","Vacuum","shared_buffers","Bloat" |

### 建议
Format/Render 逻辑放在壳层（通用），但各产品可注入自己的术语高亮配置。或者每个产品独立一套 format，虽然有重复，但保持独立。

## 7. Tool Definitions 生成

当前通过 `registry.All()` 自动生成 LLM 可用的 tool list。

多数据库后，每个产品注册自己的 Skill → `registry.All()` 自动只返回当前产品的 Skill → tool list 自动适配。

**无需额外改造**，Registry 机制天然支持。

## 8. 错误知识库改造

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 错误格式 | ORA-XXXXX | ER_XXXX / MySQL Error XXXX | SQLSTATE XXXXX |
| 数据源 | v$diag_alert_ext | performance_schema.error_log | PostgreSQL 日志 |
| 知识库规模 | 60+ 条目 | 预计 50+ 条目 | 预计 50+ 条目 |
| 诊断命令引用 | /sga, /pga, /health, ... | /bufferpool, /connmem, /health, ... | /sharedbufs, /vacuum, /health, ... |

每个库需要**完全独立的错误知识库**，包括：
- 错误码
- 中文含义
- 严重级别
- 分类
- 原因列表
- 诊断命令（引用各自产品的 /skill）
- 修复建议
