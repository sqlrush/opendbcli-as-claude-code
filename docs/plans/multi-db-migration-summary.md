# OpenDB 多数据库迁移总览

> 基于 Oracle 现有实现，梳理 MySQL / PostgreSQL 的差异与改造要点。
> 此文件为汇总索引，各模块明细见独立文件。

## 文件索引

| 文件 | 内容 |
|------|------|
| [01-monitor-skills.md](./01-monitor-skills.md) | 监控类 Skill (23个) 三库对比 |
| [02-query-skills.md](./02-query-skills.md) | 查询类 Skill (10个) 三库对比 |
| [03-admin-skills.md](./03-admin-skills.md) | 管理类 Skill (18个) 三库对比 |
| [04-schema-skills.md](./04-schema-skills.md) | Schema 类 Skill (2个) 三库对比 |
| [05-sentinel-system.md](./05-sentinel-system.md) | 哨兵监控系统三库对比 |
| [06-ruleengine.md](./06-ruleengine.md) | 规则引擎/决策树三库对比 |
| [07-dbtop-collector.md](./07-dbtop-collector.md) | 实时监控面板三库对比 |
| [08-llm-agent.md](./08-llm-agent.md) | LLM 诊断 Agent 三库对比 |
| [09-connection-config.md](./09-connection-config.md) | 连接管理与配置三库对比 |
| [10-db-unique-features.md](./10-db-unique-features.md) | 各库独有功能（Oracle 没有但 MySQL/PG 需要的） |

## 统计概览

| 维度 | Oracle (当前) | MySQL (待建) | PostgreSQL (待建) |
|------|-------------|-------------|------------------|
| Monitor Skills | 23 | ~18 (去掉 ASM/FRA/SGA/PGA/Redo，加 InnoDB/Replication) | ~18 (去掉 ASM/FRA/SGA/PGA/Redo，加 Vacuum/WAL/Replication) |
| Query Skills | 10 | ~7 (去掉 AWR/ASH/PlanHistory/ORA KB，加 perf_schema) | ~8 (去掉 AWR/ORA KB，加 pg_stat_statements) |
| Admin Skills | 18 | ~15 (去掉 Standby/Resize/Gather 改造) | ~15 (同上但 PG 特色不同) |
| Schema Skills | 2 | 2 (SQL 全改) | 2 (SQL 全改) |
| Sentinel 指标 | 48 | ~35 (机制差异大) | ~38 (介于两者之间) |
| 规则引擎规则数 | 266 | ~150 (重写) | ~180 (重写) |
| dbtop SQL | 9 条 | ~8 条 (全部重写) | ~8 条 (全部重写) |
| LLM Prompt | Oracle 专用 | MySQL 专用 (重写) | PG 专用 (重写) |

## 改造难度评估

| 难度 | 说明 | 模块 |
|------|------|------|
| **低** | SQL 语法替换即可，概念直接对应 | sessions, locks, space, params, kill |
| **中** | 概念对应但实现差异大，需重新设计 SQL 和布局 | health, waits, topsql, explain, dbtop |
| **高** | 概念不对应或机制根本不同，需要重新设计 | sentinel, ruleengine, SGA/PGA→Buffer Pool, AWR→perf_schema |
| **新建** | Oracle 没有的概念，MySQL/PG 独有 | InnoDB Monitor, Vacuum, WAL, Binlog, Replication |

## 关键设计差异总结

### 内存模型
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 内存架构 | SGA (共享) + PGA (私有) | InnoDB Buffer Pool + 各类 Cache | shared_buffers + work_mem + OS page cache |
| 关键视图 | v$sga, v$sgastat, v$pgastat | SHOW ENGINE INNODB STATUS, information_schema | pg_stat_bgwriter, pg_buffercache |
| 调优参数 | sga_target, pga_aggregate_target | innodb_buffer_pool_size | shared_buffers, effective_cache_size |

### 等待事件模型
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 等待体系 | 13 个 wait_class，数百个 event | performance_schema.events_waits_* | pg_stat_activity.wait_event_type + wait_event |
| 分类 | User I/O, System I/O, Commit, Concurrency, ... | io, lock, transaction, ... | IO, Lock, LWLock, BufferPin, Activity, ... |
| 粒度 | 微秒级，每个 event 有 total_waits + time_waited | 微秒级，但默认可能未开启 | 无累计时间，只有当前状态 |

### 存储模型
| | Oracle | MySQL (InnoDB) | PostgreSQL |
|---|--------|---------------|-----------|
| 空间管理 | Tablespace → Datafile | Tablespace → .ibd 文件 | Tablespace → pg_default/pg_global |
| 临时空间 | TEMP tablespace | tmpdir / internal_tmp_mem_storage_engine | temp_tablespaces |
| 回滚空间 | UNDO tablespace | InnoDB Undo Logs (undo_001/002) | 内置 MVCC，无独立 Undo |

### 执行计划
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 获取方式 | DBMS_XPLAN.DISPLAY / DISPLAY_CURSOR | EXPLAIN [FORMAT=JSON/TREE/TRADITIONAL] | EXPLAIN (ANALYZE, BUFFERS, FORMAT TEXT/JSON) |
| 历史计划 | dba_hist_sqlstat (AWR) | performance_schema.events_statements_history | pg_stat_statements + auto_explain |
| 计划稳定性 | SQL Plan Management (SPM) | 无原生 (靠 optimizer_switch) | 无原生 (靠参数 + pg_hint_plan 扩展) |

### 高可用
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 复制机制 | Data Guard (物理/逻辑 standby) | GTID Replication (主从/组复制) | Streaming Replication + Logical Replication |
| 状态查看 | v$database, v$archive_dest_status | SHOW REPLICA STATUS | pg_stat_replication, pg_stat_wal_receiver |
| 切换命令 | ALTER DATABASE SWITCHOVER | mysqlfailover / orchestrator | pg_promote() |

### 备份恢复
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 工具 | RMAN | mysqldump / mysqlpump / xtrabackup | pg_dump / pg_basebackup |
| 历史查看 | v$rman_backup_job_details | 无内置视图（靠日志） | pg_stat_archiver + 手动 |
| 增量备份 | RMAN 原生 | xtrabackup 增量 | pg_basebackup + WAL archiving |

### 调度器
| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 内置调度 | DBMS_SCHEDULER (dba_scheduler_jobs) | EVENT SCHEDULER (information_schema.events) | pg_cron 扩展 |
| 状态查看 | dba_scheduler_job_run_details | information_schema.events | cron.job + cron.job_run_details |
