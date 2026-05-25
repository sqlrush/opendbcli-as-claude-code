# 各库独有功能 — Oracle 没有但 MySQL/PG 需要的

## MySQL 独有 Skill（Oracle 中不存在）

| 新 Skill | 命令 | 说明 | 数据源 |
|---------|------|------|--------|
| InnoDB Status | `/innodb` | InnoDB 引擎状态详情（Buffer Pool, Log, Row Operations, Semaphores） | `SHOW ENGINE INNODB STATUS` |
| Replication | `/replication` | 主从复制状态（IO Thread, SQL Thread, Lag, GTID） | `SHOW REPLICA STATUS` |
| Binlog | `/binlog` | Binary Log 列表、当前位置、过期策略 | `SHOW BINARY LOGS`, `SHOW BINARY LOG STATUS` |
| Buffer Pool | `/bufferpool` | InnoDB Buffer Pool 详细统计 | `SHOW STATUS LIKE 'Innodb_buffer_pool%'` |
| Connection Memory | `/connmem` | 按连接的内存使用 | `performance_schema.memory_summary_by_thread_by_event_name` |
| Deadlock Info | `/deadlock` | 最近的 InnoDB 死锁详情 | `SHOW ENGINE INNODB STATUS` (LATEST DEADLOCK section) |
| MDL Locks | `/mdl` | Metadata Lock 等待情况 | `performance_schema.metadata_locks` |
| Table Fragmentation | `/fragmentation` | 表碎片率统计 | `information_schema.TABLES` (DATA_FREE / DATA_LENGTH) |
| InnoDB Metrics | `/innodbmetrics` | InnoDB 内部指标监控 | `information_schema.INNODB_METRICS` |
| MySQL Error KB | `/mysqlerr [code]` | MySQL 错误知识库 | 内置知识库 50+ 条目 |
| Processlist | `/processlist` | 增强版进程列表（比 /sessions 更 MySQL 风格） | `SHOW FULL PROCESSLIST` |
| Engine Status | `/engines` | 存储引擎状态概览 | `SHOW ENGINES` |
| Slave Hosts | `/replicas` | 查看所有从库连接 | `SHOW REPLICAS` |
| GTID | `/gtid` | GTID 执行状态和一致性检查 | `SHOW MASTER STATUS` + `SHOW REPLICA STATUS` |

### MySQL 特有的 Sentinel 告警场景
- **Replication Lag > N 秒**
- **Replication Broken** (IO/SQL Thread 停止)
- **InnoDB History List > 阈值** (长事务阻止 purge)
- **InnoDB Deadlock Storm** (短时间内频繁死锁)
- **Metadata Lock Wait > N 秒** (DDL 阻塞所有查询)
- **Buffer Pool Hit Rate 下降**
- **Binlog 空间占用过大**
- **Table Auto-increment 接近上限**

### MySQL 特有的规则引擎场景

| 场景 | 根因 | 诊断路径 |
|------|------|---------|
| DDL 阻塞 | MDL 长时间持有 | → 找到持有 MDL 的事务 → 判断是否可 KILL |
| Gap Lock 争用 | Next-Key Lock 范围过大 | → 分析 isolation level → 建议改 RC |
| Undo 膨胀 | 长事务阻止 purge | → 找到长事务 → 分析原因 → 建议 KILL 或优化 |
| 主从延迟 | SQL Thread 回放慢 | → 检查并行复制配置 → 分析大事务 |
| Semi-sync 超时 | 半同步复制等待 | → 检查网络延迟 → 调整 rpl_semi_sync_master_timeout |

---

## PostgreSQL 独有 Skill（Oracle 中不存在）

| 新 Skill | 命令 | 说明 | 数据源 |
|---------|------|------|--------|
| Vacuum Status | `/vacuum` | Vacuum 状态（dead tuples, last vacuum, autovacuum lag） | `pg_stat_user_tables`, `pg_stat_progress_vacuum` |
| Bloat Estimate | `/bloat` | 表/索引膨胀估算 | `pgstattuple` 扩展 或估算公式 |
| WAL Status | `/wal` | WAL 状态、归档进度、WAL 生成速率 | `pg_stat_wal`, `pg_stat_archiver`, `pg_ls_waldir()` |
| Replication | `/replication` | 流复制状态（lag, 复制槽, WAL sender） | `pg_stat_replication`, `pg_stat_wal_receiver` |
| Long Transactions | `/longtx` | 长事务检查（阻止 vacuum） | `pg_stat_activity WHERE xact_start < threshold` |
| XID Wraparound | `/xid` | 事务 ID 年龄和 wraparound 风险 | `pg_database.datfrozenxid`, `pg_class.relfrozenxid` |
| Shared Buffers | `/sharedbufs` | shared_buffers 中各表占用情况 | `pg_buffercache` 扩展 |
| Extensions | `/extensions` | 已安装扩展列表 | `pg_available_extensions`, `pg_extension` |
| Replication Slots | `/slots` | 复制槽状态和 WAL 积压 | `pg_replication_slots` |
| PG Error KB | `/pgerr [code]` | PostgreSQL 错误知识库 | 内置知识库 50+ 条目 |
| Sequences | `/sequences` | 序列使用情况和耗尽风险 | `pg_sequences` |
| Cancel Backend | `/cancel <pid>` | 只取消查询（不杀会话） | `pg_cancel_backend()` |
| Settings Diff | `/settingsdiff` | 当前值 vs 默认值差异 | `pg_settings WHERE boot_val != setting` |
| Toast | `/toast` | TOAST 表大小统计 | `pg_class WHERE reltoastrelid` |

### PostgreSQL 特有的 Sentinel 告警场景
- **Vacuum Lag** — dead tuples / live tuples > 20%
- **XID Wraparound Risk** — age(datfrozenxid) > 1 billion
- **Replication Lag > N 秒**
- **Replication Slot WAL 积压** — 复制槽保留过多 WAL
- **Table Bloat > 50%** — 表膨胀率过高
- **Checkpoint Storm** — 频繁 checkpoint 导致 IO 峰值
- **Long Transaction > N 分钟** — 阻止 VACUUM 回收
- **WAL Archive Failure** — 连续归档失败

### PostgreSQL 特有的规则引擎场景

| 场景 | 根因 | 诊断路径 |
|------|------|---------|
| Vacuum 滞后 | autovacuum 跟不上 DML | → 检查 autovacuum_naptime → 检查 vacuum_cost_delay → 增大 worker |
| XID Wraparound | 未及时 VACUUM FREEZE | → 找出最老的 datfrozenxid → 对最老的库执行 VACUUM FREEZE |
| 表膨胀 | 长事务阻止回收 | → 找到长事务 → KILL → VACUUM FULL (或 pg_repack) |
| WAL 积压 | 复制槽未消费 | → 检查 subscriber 状态 → 删除不活跃的 slot |
| Checkpoint 风暴 | checkpoint_completion_target 太低 | → 调大 max_wal_size → 调大 checkpoint_completion_target |
| Recovery Conflict | Hot Standby 查询冲突 | → 检查 max_standby_streaming_delay → 调大或取消冲突查询 |
| Lock Escalation | 大量行锁合并 | → 分析查询模式 → 拆分大事务 |

---

## 三库 Skill 数量对比

| 类别 | Oracle | MySQL | PostgreSQL |
|------|--------|-------|-----------|
| 通用可平移 | 参考基线 | ~25 (概念对应，SQL 全改) | ~25 (概念对应，SQL 全改) |
| 独有功能 | ASM, FRA, SGA, PGA, Redo, Standby, AWR, ASH, ORA KB, Gather, Resize, Standby | InnoDB Status, Replication, Binlog, Buffer Pool, Deadlock, MDL, GTID, ... | Vacuum, Bloat, WAL, XID, Shared Buffers, Replication Slots, Long TX, ... |
| 完全公用 | help, clear, config, history, login, logout, scheduler | 同 | 同 |
| **Skill 总数** | **~55** | **~42** | **~45** |

---

## 工作量预估

| 工作项 | MySQL | PostgreSQL |
|--------|-------|-----------|
| Driver 实现 | 1 周 | 1 周 |
| 平移 Skill (~25 个，SQL 重写) | 3-4 周 | 3-4 周 |
| 独有 Skill (~14/~16 个) | 3-4 周 | 3-4 周 |
| Sentinel 改造 (指标 + 探针) | 2 周 | 2 周 |
| Rule Engine 重写 (~150/~185 条) | 4-6 周 | 5-7 周 |
| dbtop 改造 | 1 周 | 1 周 |
| LLM Agent 改造 (prompt + 知识库) | 2 周 | 2 周 |
| 连接管理 / 配置 | 1 周 | 1 周 |
| 测试 + 调试 | 3-4 周 | 3-4 周 |
| **总计** | **~20-24 周** | **~22-26 周** |

注意：以上为纯开发时间估算，不含需求确认和架构评审。两个库可并行开发，但建议先完成一个再做另一个（第二个可复用经验，速度更快）。
