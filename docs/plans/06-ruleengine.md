# 规则引擎/决策树三库对比（基于 ailinkdb 训练数据）

> 本文基于 ailinkdb/data/ 下 Oracle 1,691 条、MySQL 548 条、PostgreSQL 561 条训练数据的全量扫描，
> 提取诊断模式和决策树，映射到 opendb 规则引擎的规则设计。

## 数据源概览

| | Oracle | MySQL | PostgreSQL |
|---|--------|-------|-----------|
| 训练文件数 | 222 | 104 (71+33xdb) | 97 (64+33xdb) |
| Q&A 总数 | 1,691 | 548 | 561 |
| 知识主题数 | ~1,285 | ~387+161xdb | ~400+161xdb |
| 已有决策树 | 2 个完整 JSON | 0 | 0 |
| 覆盖类别 | 9 大类 | 8 大类 | 8 大类 |

## 规则引擎框架（三库通用，可复用）

以下部分是数据库无关的，放在壳层：
- Decision Tree 结构（TreeNode 递归、Condition 运算符 gt/lt/eq/pct_gt/exists/not_empty）
- Signal / Trigger / EvalContext 抽象
- Rule 注册与执行引擎
- 结果格式化（Finding + Action + Severity）

**全部规则定义**（等待事件名、SQL 查询、阈值、推理路径、修复建议）必须各库独立重写。

---

## 1. 等待事件规则

### Oracle（当前 31 条 + ailinkdb 覆盖 177 个等待事件主题）

ailinkdb 训练数据中出现频率最高的等待事件：

| 等待事件 | 训练出现次数 | opendb 已有规则 | 决策树复杂度 |
|---------|------------|---------------|-------------|
| log file sync | 49 | ✓ WE003 | 高（LGWR/redo sizing/commit freq 三分支） |
| db file sequential read | 42 | ✓ WE001 | 中 |
| buffer busy waits | 40 | ✓ WE004 | 高（hot block/ASSM/ITL 多分支） |
| direct path read | 39 | ✓ WE009 | 中 |
| enq: TX - row lock | 35 | ✓ WE005 | 中 |
| library cache lock | 26 | ✓ WE007 | 高 |
| db file scattered read | 25 | ✓ WE002 | 中 |
| free buffer waits | 23 | ✓ WE010 | 中 |
| log file parallel write | 23 | ✓ | 中 |
| enq: SQ - contention | 15 | ✓ WE-ext | 中（CACHE/ORDER/SCALE 分支） |
| read by other session | 15 | ✓ | 低 |
| library cache pin | 12 | ✓ | 中 |
| gc buffer busy (RAC) | 10 | ✓ | 高（RAC 专用） |
| cursor: pin S wait on X | 6 | ✓ WE006 | **有完整 JSON 决策树** |
| cursor: pin S | 4 | ✓ | **有完整 JSON 决策树** |

### MySQL（需新建 ~20 条，基于 ailinkdb 31 个锁并发 + 55 个监控主题）

| 规则 ID | 等待事件/场景 | ailinkdb 数据源 | 决策树要点 |
|---------|-------------|---------------|-----------|
| MY-WE001 | InnoDB row lock wait | batch3 (6题) + b09 (15题) | data_locks → INNODB_TRX → 阻塞链 → KILL |
| MY-WE002 | MDL lock contention | batch3 Q23 + b09 | metadata_locks → 长事务检测 → lock_wait_timeout |
| MY-WE003 | Deadlock storm | batch3 Q20 + emg_mysql Q3 | INNODB STATUS → 死锁图 → 访问顺序修复 |
| MY-WE004 | Gap Lock / Next-Key Lock | batch3 Q19 + b09 | isolation level 检查 → RC 建议 → 索引优化 |
| MY-WE005 | buf_pool_mutex contention | batch2 Q11 + b01 | Buffer Pool instances 检查 → 热表检测 |
| MY-WE006 | btr_search_latch (AHI) | batch2 Q17(AHI) | AHI hit ratio → disable AHI 建议 |
| MY-WE007 | log_sys_mutex | batch2 Q12 | redo log sizing → innodb_flush_log_at_trx_commit |
| MY-WE008 | dict_sys_mutex | batch7 Q44(DDL) | DDL 并发检测 → gh-ost 建议 |
| MY-WE009 | innodb_data_file IO | batch2 Q14 | innodb_io_capacity → SSD/HDD 检测 → flush method |
| MY-WE010 | innodb_log_file IO | batch2 Q12 | log_file_size → group commit → sync_binlog |
| MY-WE011 | Auto-increment lock | batch3 Q24(热行) | innodb_autoinc_lock_mode 检查 |
| MY-WE012 | Hot row contention | batch3 Q24 + b09 Q10(行业场景) | 行拆分/桶化 → 乐观锁 → 队列化 |
| MY-WE013 | Semi-sync timeout | batch4 Q27 + a03-a04 | Rpl_semi_sync_master_no_tx → 网络/从库检查 |
| MY-WE014 | Replication SQL delay | batch4 Q25 + a01 | IO vs SQL thread → parallel replication 调优 |
| MY-WE015 | Connection storm | emg_mysql Q4+Q9 | Threads_connected vs max_connections → KILL idle → 连接池 |
| MY-WE016 | Binlog sync overhead | batch6 Q40 + b08 | sync_binlog → group commit → 性能权衡 |
| MY-WE017 | Table fragmentation | batch7 Q44(DDL) | DATA_FREE/DATA_LENGTH → OPTIMIZE TABLE |
| MY-WE018 | InnoDB history list | batch2 Q13 + emg_mysql | History list length → 长事务 → KILL |
| MY-WE019 | Sort/tmp disk spill | batch6 Q39 + b07 | sort_buffer_size → tmp_table_size → SQL 优化 |
| MY-WE020 | OOM risk | emg_mysql Q7 | 内存预算公式 → max_connections × per_session |

### PostgreSQL（需新建 ~25 条，基于 ailinkdb 35 个锁并发 + 65 个监控 + 55 个 VACUUM 主题）

| 规则 ID | 等待事件/场景 | ailinkdb 数据源 | 决策树要点 |
|---------|-------------|---------------|-----------|
| PG-WE001 | Lock:transactionid (行锁) | batch4 Q25-26 + b03 | pg_locks → pg_blocking_pids() → 阻塞链 |
| PG-WE002 | Lock:relation (表锁/DDL) | batch4 Q29 + emg_pgsql Q8 | AccessExclusiveLock → 队列级联 → lock_timeout |
| PG-WE003 | Deadlock | batch4 Q26 + emg_pgsql | deadlock_timeout → pg_stat_database.deadlocks → 访问顺序 |
| PG-WE004 | IO:DataFileRead | batch1 Q1-2 + e01 | Seq Scan 检测 → 缺索引 → shared_buffers |
| PG-WE005 | IO:WALSync | batch5 Q31-32 + a01-a02 | max_wal_size → checkpoint 频率 → wal_compression |
| PG-WE006 | IO:BufFileRead/Write | batch3 Q20 + e01 | work_mem 不足 → temp_blks_written → 调大 |
| PG-WE007 | LWLock:buffer_content | batch3 Q19 + e01_pgstat | 热页争用 → 表设计 → 分区 |
| PG-WE008 | LWLock:WALInsertLock | batch5 Q31 | WAL 写入争用 → wal_buffers → wal_compression |
| PG-WE009 | LWLock:buffer_mapping | e01_pgstat | 哈希表争用 → shared_buffers 大小 |
| PG-WE010 | LWLock:lock_manager | batch4 + e01_pgstat | 锁管理器争用 → 减少锁持有时间 |
| PG-WE011 | LWLock:ProcArrayLock | e01_pgstat | 快照争用 → 长事务检测 → idle_in_transaction_timeout |
| PG-WE012 | BufferPin:BufferPin | e01_pgstat | 并发热数据 → 分区/索引优化 |
| PG-WE013 | Vacuum lag (dead tuples) | batch2 Q11-12 + b05-b09 | n_dead_tup/n_live_tup → autovacuum 参数 → pg_repack |
| PG-WE014 | XID wraparound | batch4 Q27 + b04 + emg_pgsql Q5 | age(datfrozenxid) → VACUUM FREEZE → 紧急处理 |
| PG-WE015 | Table bloat | batch2 Q12 + b05-b06 | pgstattuple → pg_repack → VACUUM FULL |
| PG-WE016 | Index bloat | batch2 + e01 | pg_stat_user_indexes → REINDEX CONCURRENTLY |
| PG-WE017 | Replication lag | batch5 Q33 + a03-a06 | pg_stat_replication.replay_lag → 从库 IO → 查询冲突 |
| PG-WE018 | Replication slot bloat | batch5 + a09 + emg_pgsql Q3 | pg_replication_slots → max_slot_wal_keep_size → 删除不活跃 |
| PG-WE019 | Checkpoint storm | batch5 Q32 + c12 | checkpoints_req vs timed → max_wal_size → completion_target |
| PG-WE020 | Connection exhaustion | emg_pgsql Q1 + c12 | pg_stat_activity state 分布 → idle_in_transaction → PgBouncer |
| PG-WE021 | Long transaction | batch4 Q28 + emg_pgsql Q4 | xact_start 检测 → 阻塞 VACUUM → KILL |
| PG-WE022 | WAL archive failure | batch5 + a07 + emg_pgsql Q3 | pg_stat_archiver → archive_command → 空间 |
| PG-WE023 | Recovery conflict | batch5 Q34(流复制冲突) + a01 | pg_stat_database_conflicts → hot_standby_feedback → delay |
| PG-WE024 | Data corruption | emg_pgsql Q9 | pg_amcheck → REINDEX → 备份恢复 |
| PG-WE025 | Performance degradation | emg_pgsql Q10 | 分层排查: wait_event → Top SQL → bloat → stats → OS |

---

## 2. SQL 调优规则

### Oracle（当前 25 条 + ailinkdb 覆盖 329 个 SQL 调优主题）

ailinkdb 数据中 SQL 调优覆盖极其深入：执行计划分析(33)、SPM(10)、Adaptive(15)、Hints(10)、查询变换(24)、分区裁剪(24)、索引设计(18)、排序/Hash(27)、并行查询(24)、统计信息(32)、子查询(12)、窗口函数(10)、计划回退(8)。

### MySQL SQL 调优规则（~15 条，基于 ailinkdb 75 个 SQL 调优主题）

| 规则 ID | 名称 | ailinkdb 来源 | 诊断逻辑 |
|---------|------|-------------|---------|
| MY-ST001 | EXPLAIN type=ALL 全表扫描 | batch1 Q1-2 + b03 | rows > 10K + type=ALL → 加索引建议 |
| MY-ST002 | 隐式类型转换致索引失效 | batch1 Q3 + b03 | possible_keys 有但 key=NULL → 检测类型不匹配 |
| MY-ST003 | filesort + Using temporary | batch1 Q6 + b04 | ORDER BY/GROUP BY 未利用索引 → 索引覆盖建议 |
| MY-ST004 | JOIN 缺索引 (BNL/Hash) | batch1 Q4 + sql_join | Using join buffer → driven 表缺索引 |
| MY-ST005 | 深分页 OFFSET > 10K | batch1 Q7 + b05 | LIMIT offset 大 → 延迟 JOIN / 游标分页 |
| MY-ST006 | 子查询物化/未优化 | batch1 Q5 + sql_subquery | DERIVED/MATERIALIZED → 改写为 JOIN |
| MY-ST007 | 统计信息过时 | batch2 Q18(直方图) | UPDATE_TIME 旧 → ANALYZE TABLE |
| MY-ST008 | 直方图缺失(数据倾斜) | batch6 Q42(8.0特性) | 分布不均匀 → UPDATE HISTOGRAM |
| MY-ST009 | ICP/MRR 未启用 | batch1 + b07 | optimizer_switch 检查 |
| MY-ST010 | Invisible Index 可用 | batch6 Q42 | 8.0+ 隐藏索引测试 |
| MY-ST011 | 窗口函数优化 | d01(窗口函数) | filesort in window → 索引优化 |
| MY-ST012 | CTE 递归优化 | d02(CTE) | WITH RECURSIVE 性能检查 |
| MY-ST013 | Generated Column 索引 | b07 + e03 | 函数索引替代方案 |
| MY-ST014 | Optimizer Trace 分析 | b06 + e01 | 计划选择原因追踪 |
| MY-ST015 | 行业场景瓶颈分析 | sql_bottleneck(10个行业) | 电商/金融/电信等场景诊断模板 |

### PostgreSQL SQL 调优规则（~18 条，基于 ailinkdb 85 个查询优化主题）

| 规则 ID | 名称 | ailinkdb 来源 | 诊断逻辑 |
|---------|------|-------------|---------|
| PG-ST001 | Seq Scan on large table | batch1 Q1-2 + b01 | Seq Scan + rows > 10K → 加索引 |
| PG-ST002 | 估算行数偏差 > 10x | batch1 Q1 + b01 | actual_rows/estimated_rows → ANALYZE 或 extended stats |
| PG-ST003 | Hash Join disk spill | batch1 Q4 + b02 | Batches > 1 → 调大 work_mem |
| PG-ST004 | Index Only Scan heap fetches | batch1 + b01 | Heap Fetches 高 → VACUUM 更新 visibility map |
| PG-ST005 | CTE 物化屏障 (PG<12) | batch1 Q5 + sql_subquery | CTE Scan → NOT MATERIALIZED 或改写 |
| PG-ST006 | OFFSET 深分页 | batch1 Q6 + b10 | OFFSET > 10K → keyset 分页 |
| PG-ST007 | random_page_cost 不匹配 SSD | batch7 Q46 + c12 | SSD + random_page_cost=4.0 → 建议 1.1 |
| PG-ST008 | 并行查询未启用 | batch7 Q45 + b13 | 大表扫描 + workers=0 → 调 parallel 参数 |
| PG-ST009 | JIT 编译开销 > 执行 | batch1 + b13 | JIT time > exec time → 禁用 JIT |
| PG-ST010 | 分区裁剪失败 | batch2 Q14 + c01 | EXPLAIN 扫描所有分区 → 检查 WHERE 条件 |
| PG-ST011 | BRIN 索引机会 | batch1 Q2 + b01 | 时序数据 + B-tree 大 → BRIN 建议 |
| PG-ST012 | Partial Index 机会 | batch1 Q3 + b01 | 高选择性 WHERE → 部分索引 |
| PG-ST013 | GIN 索引(JSONB) | batch1 Q8 + b02 | JSONB 操作符 → GIN 索引建议 |
| PG-ST014 | 统计信息过时 | batch7 Q47 + e01 | n_mod_since_analyze > 10% → ANALYZE |
| PG-ST015 | Extended Statistics 缺失 | b01 + e01 | 多列相关性 → CREATE STATISTICS |
| PG-ST016 | TOAST 解压开销 | batch2 Q15 + b06 | toast_blks_read 高 → EXTERNAL 存储策略 |
| PG-ST017 | 全文搜索优化 | batch1 Q7 + b02 | tsvector + GIN 索引 |
| PG-ST018 | 行业场景瓶颈分析 | sql_bottleneck(10行业) | 场景化诊断模板 |

---

## 3. 内存/IO 规则

### Oracle（当前 15 条 + ailinkdb 45 个内存主题）

ailinkdb 覆盖：Buffer Cache advice(18)、Shared Pool ORA-04031(35)、PGA overflow(11)、AMM vs ASMM(5)、Result Cache(3)、memory advisors(7)。

### MySQL 内存/IO 规则（~10 条，基于 ailinkdb 23 个 InnoDB + 28 个参数主题）

| 规则 ID | 名称 | ailinkdb 来源 | 阈值/逻辑 |
|---------|------|-------------|---------|
| MY-MI001 | Buffer Pool hit < 99% | batch2 Q11 + b01 | 1-(reads/read_requests) < 99% → 调大 BP |
| MY-MI002 | Buffer Pool < 50% RAM | batch6 Q36 + b07 | 专用服务器 BP < 50% RAM → 调大 |
| MY-MI003 | Redo Log 太小 | batch2 Q12 + b08 | Checkpoint Age 接近上限 → 调大 log_file_size |
| MY-MI004 | History list > 100K | batch2 Q13 | Undo purge 滞后 → 长事务检查 |
| MY-MI005 | tmp 磁盘溢出 > 10% | batch6 Q39 + b07 | Created_tmp_disk/Created_tmp > 10% → 调大 tmp_table_size |
| MY-MI006 | sort_merge_passes 高 | batch6 Q39 | 排序溢出 → sort_buffer_size (注意 per-session) |
| MY-MI007 | IO capacity 不匹配存储 | batch2 Q14 + b08 | HDD:200 / SSD:2000-5000 / NVMe:10000-20000 |
| MY-MI008 | AHI 争用 | batch2 Q17 | btr_search_latch + AHI hit ratio → disable |
| MY-MI009 | OOM 风险 | emg_mysql Q7 + b07 | BP + max_conn × per_session > 90% RAM |
| MY-MI010 | Dirty page ratio 高 | batch2 Q14 | innodb_max_dirty_pages_pct → flush 调优 |

### PostgreSQL 内存/IO 规则（~8 条，基于 ailinkdb 30 个内存主题）

| 规则 ID | 名称 | ailinkdb 来源 | 阈值/逻辑 |
|---------|------|-------------|---------|
| PG-MI001 | Cache hit < 99% | batch3 Q19 + e01 | blks_hit/(blks_hit+blks_read) < 99% |
| PG-MI002 | shared_buffers 默认未调 | batch3 Q19 + c12 | 128MB on > 8GB RAM → 调到 25% RAM |
| PG-MI003 | work_mem 溢出 | batch3 Q20 + e01 | temp_blks_written > 0 → 调大 work_mem |
| PG-MI004 | effective_cache_size < shared_buffers | batch3 Q21 + e02 | 明显配置错误 |
| PG-MI005 | buffers_backend 比率高 | batch6 Q40 + e01_pgstat | buffers_backend / total > 30% → bgwriter 调优 |
| PG-MI006 | Checkpoint 导致 IO 尖峰 | batch5 Q32 + c12 | checkpoints_req > timed → max_wal_size |
| PG-MI007 | maintenance_work_mem 太小 | batch3 Q22 | VACUUM/INDEX 慢 → 调大 |
| PG-MI008 | random_page_cost SSD 不匹配 | batch7 Q46 | SSD + rpc=4.0 → 建议 1.1 |

---

## 4. 运维/HA/紧急规则

### Oracle（当前 HA 38 条 + OP 20 条 + EM 97 条 = 155 条）

ailinkdb HA 数据极其丰富（195 个主题）：Data Guard Broker(25+)、Switchover/Failover(20+)、RAC(65+)、GoldenGate(40)、ASM/RMAN(30)。

紧急场景覆盖 175 个主题，ORA 错误 TOP 20：ORA-01555(45)、ORA-00600(36)、ORA-04031(35)、ORA-01652(15)、ORA-07445(15)、ORA-00060(12)。

### MySQL 运维/HA/紧急规则（~25 条，基于 ailinkdb 63 个复制 + 91 个架构 + 10 个紧急主题）

| 规则 ID | 名称 | ailinkdb 来源 | 诊断逻辑 |
|---------|------|-------------|---------|
| MY-HA001 | Replication broken (IO Thread) | a01 + emg Q1-2 | Replica_IO_Running=No → 检查网络/权限/binlog |
| MY-HA002 | Replication broken (SQL Thread) | a01 + emg Q1-2 | 1062/1032 → 空事务跳过 → pt-table-sync |
| MY-HA003 | Replication delay | a01 + batch4 Q25 | IO vs SQL 瓶颈分流 → parallel replication |
| MY-HA004 | Semi-sync degradation | a03-a04 | Rpl_semi_sync_master_no_tx → 超时/网络/从库 |
| MY-HA005 | GTID errant transaction | a01 (errant) | GTID_SUBTRACT → 主库注入空事务 |
| MY-HA006 | Failover 决策 (GTID) | a01-a02 + emg Q8 | 比较 GTID set → promote 最新从库 |
| MY-HA007 | MGR member down | a05-a06 | group_replication_members → auto-rejoin |
| MY-HA008 | Binlog 空间爆满 | emg Q5 + batch6 Q40 | PURGE BINARY LOGS → expire_logs_seconds |
| MY-HA009 | Disk full | emg Q5 | 紧急清理 → 扩容 |
| MY-HA010 | Too many connections | emg Q4 + c11 | KILL idle → 连接池 → max_connections |
| MY-OP001 | DDL 安全评估 | batch7 Q44 + c01-c03 | INSTANT > INPLACE > gh-ost 决策树 |
| MY-OP002 | 统计信息过时 | batch2 Q18 | UPDATE_TIME → ANALYZE TABLE |
| MY-OP003 | Table fragmentation | batch7 + c04 | DATA_FREE/DATA_LENGTH > 20% → OPTIMIZE |
| MY-OP004 | 密码过期 | c14-c15 | mysql.user password_lifetime 检查 |
| MY-OP005 | 备份策略检查 | batch7 Q49 + c07 | RPO/RTO → xtrabackup/Clone 决策 |
| MY-EM001 | Deadlock storm | emg Q3 + batch3 Q20 | 1min > 10 次 → 分析死锁图 → 修改应用 |
| MY-EM002 | SQL avalanche | emg Q9 | 识别雪崩 SQL → KILL → 加索引 → 熔断 |
| MY-EM003 | OOM Kill | emg Q7 | 内存预算 → 减 max_conn 或 buffer |
| MY-EM004 | 误删数据 | emg Q10 | binlog2sql flashback → 延迟从库恢复 |
| MY-EM005 | Buffer Pool hit drop | emg Q6 | 大表扫描污染 → innodb_old_blocks_time |
| MY-EM006 | InnoDB corruption | emg(隐含) | innodb_force_recovery → 备份 → 修复 |

### PostgreSQL 运维/HA/紧急规则（~25 条，基于 ailinkdb 70 个 WAL/复制 + 55 个存储 + 10 个紧急主题）

| 规则 ID | 名称 | ailinkdb 来源 | 诊断逻辑 |
|---------|------|-------------|---------|
| PG-HA001 | Replication lag | batch5 Q33 + a03-a06 | replay_lag → 从库 IO / 查询冲突 |
| PG-HA002 | Replication broken | emg Q6 + a01 | WAL segment removed → 重建从库 → slot |
| PG-HA003 | Sync replication stall | a01-a02 (同步复制) | 所有同步备库挂 → 主库 hang → 降级异步 |
| PG-HA004 | Replication slot bloat | a09 + emg Q3 | active=false → 删除不活跃 slot |
| PG-HA005 | Recovery conflict | batch5 Q34(冲突) | confl_snapshot → hot_standby_feedback → delay |
| PG-HA006 | WAL archive failure | a07 + emg Q3 | pg_stat_archiver → archive_command 修复 |
| PG-HA007 | Failover 决策 | a06 (Patroni) | Patroni/repmgr → pg_promote() |
| PG-HA008 | pg_wal disk full | emg Q3 | 检查 slot + archiver → 清理 |
| PG-HA009 | Disk full | emg Q3 | WAL + temp + bloat → 清理 |
| PG-HA010 | Connection exhaustion | emg Q1 + c12 | idle in transaction → timeout → PgBouncer |
| PG-OP001 | Vacuum lag (dead tuples) | batch2 Q11 + b05-b09 | n_dead_tup ratio → autovacuum 参数调优 |
| PG-OP002 | XID wraparound | batch4 Q27 + emg Q5 | age(datfrozenxid) > 1B → VACUUM FREEZE |
| PG-OP003 | Table bloat | batch2 Q12 + b05-b06 | pgstattuple → pg_repack |
| PG-OP004 | Index bloat | b01 + e01 | idx_scan=0 + 大索引 → REINDEX CONCURRENTLY |
| PG-OP005 | 统计信息过时 | batch7 Q47 + e01 | n_mod_since_analyze → ANALYZE |
| PG-OP006 | Long transaction | batch4 Q28 + emg Q4 | xact_start → 阻塞 VACUUM → KILL |
| PG-OP007 | DDL 锁级联 | emg Q8 | lock_timeout=3s → 检查阻塞 → 安全 DDL |
| PG-OP008 | 密码过期 | c14 | pg_authid.rolvaliduntil 检查 |
| PG-OP009 | 备份检查 | a07-a08 | pg_stat_archiver → pgBackRest 状态 |
| PG-EM001 | XID wraparound emergency | emg Q5 | age > 2B → 数据库即将只读 → 紧急 FREEZE |
| PG-EM002 | OOM Kill | emg Q7 | 内存检查 → 减 max_conn → vm.overcommit |
| PG-EM003 | Data corruption | emg Q9 | pg_amcheck → REINDEX → 备份恢复 |
| PG-EM004 | Performance degradation | emg Q10 | 分层: wait → Top SQL → bloat → stats → OS |
| PG-EM005 | DDL blocking cascade | emg Q8 | 杀阻塞 SELECT → DDL 继续 |

---

## 5. 规则规模总结（基于数据驱动的估算）

| 类别 | Oracle (当前) | MySQL (预估) | PostgreSQL (预估) |
|------|-------------|-------------|------------------|
| 等待事件 | 31 | 20 | 25 |
| SQL 调优 | 25 | 15 | 18 |
| 内存/IO | 15 | 10 | 8 |
| 运维 | 20 | 10 | 10 |
| HA | 38 | 10 | 10 |
| 紧急 | 97 | 6 | 5 |
| 深度诊断 | 40 | 10 | 10 |
| **总计** | **266** | **~81** | **~86** |

### 为什么 MySQL/PG 规则数比 Oracle 少很多？

1. **Oracle 架构更复杂**: RAC(65 个主题)、ASM(21)、Data Guard Broker(25+) — MySQL/PG 无对等
2. **Oracle 等待事件体系更庞大**: 数百个事件 vs MySQL ~50 个 vs PG ~40 个
3. **Oracle 紧急场景更多**: ORA 错误体系(175 个主题)比 MySQL/PG 的错误体系复杂得多
4. **数据量差距**: Oracle 1,691 条训练数据 vs MySQL 548 vs PG 561

### 后续可补充方向

基于 ailinkdb 跨库数据(161 条 xdb)中识别的差异点，以下方面可以额外补充规则：

**MySQL 可补充**：
- MGR flow control(a05-a06)、InnoDB Cluster(a07-a08)、ProxySQL 路由(a07) → +10 条 HA 规则
- 行业场景(sql_bottleneck 10 个行业) → +10 条场景化 SQL 规则
- 8.0 新特性(d04-d10) → +5 条特性检测规则

**PostgreSQL 可补充**：
- Patroni/repmgr 深度(a06+a08) → +10 条 HA 规则
- 行业场景(sql_bottleneck) → +10 条场景化 SQL 规则
- PG 14-17 新特性(batch7 Q48) → +5 条特性检测规则

补充后：MySQL ~106 条、PostgreSQL ~111 条，接近 Oracle 的密度（考虑到架构复杂度差异，这是合理的）。

---

## 6. 决策树数据复用

ailinkdb/data/rules/ 目录下已有 2 个完整的 Oracle 决策树 JSON：

### WE_cursor_pin_s.json (可作为决策树格式模板)
- 8 个诊断查询
- 5 个根因分支
- 阈值体系：wait_pct_trigger=3%, avg_wait_ms_normal=1ms, users_executing_hot=100
- 5 步紧急 SOP

### WE006_cursor_pin_s_wait_on_x.json
- 8 个诊断查询
- 5 个根因分支（Literal SQL → Version Count → Shared Pool → Complex Parse → Hot SQL）
- 阈值体系：hard_parse_pct=30%, literal_sql_pct=30%, version_count=200, shared_pool_free=10%
- 5 步紧急 SOP

**MySQL/PG 的决策树应采用相同的 JSON 格式**，便于规则引擎统一解析。每个规则包含：
1. 诊断查询列表（各库各自的 SQL）
2. 决策分支（阈值条件 + 下一步检查）
3. 根因列表（原因 + 修复建议）
4. 紧急 SOP（分步操作）

### 生成脚本参考
- `raw/add_decision_trees.py` — Oracle 决策树生成脚本
- `raw/add_xdb_b_trees.py` / `add_xdb_b_trees_2.py` — 跨库决策树脚本

可参考这些脚本的格式，基于 ailinkdb 训练数据批量生成 MySQL/PG 的决策树 JSON。
