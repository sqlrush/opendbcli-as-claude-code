# MySQL Rule Engine 验证 — 100 个故障场景（推理链校准版）

## 设计原则

- 每个场景需要 **2-6 步推理链**（决策树深度）
- 太简单的（1 步到位）不入选：磁盘满直接报错、单纯语法错误
- 太复杂的（需 GDB 调试/跨集群推理）不入选：MySQL 内核 BUG、MGR 脑裂
- 目标：Opus 调 rule → 对比打分 → 输出 rule 优化方案
- 所有场景基于 **单实例 MySQL 8.0+**，不含 Group Replication / InnoDB Cluster 等 HA 场景

## 评分维度（Opus 打分标准）

| 维度 | 权重 | 说明 |
|------|------|------|
| 根因识别 | 40 分 | 是否找对根本原因 |
| 修复建议 | 30 分 | SQL/操作是否正确可执行 |
| 严重程度 | 10 分 | 是否正确评估紧急性 |
| 排查路径 | 10 分 | 诊断步骤是否合理高效 |
| 完整性 | 10 分 | 是否遗漏关键信息 |

---

## 一、锁与阻塞（15 个场景）

### T001 — InnoDB 行锁级联：blocker 是空闲会话
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查 performance_schema.data_locks + data_lock_waits → root blocker thread_id=123
  3. 查 blocker 状态 → COMMAND='Sleep', 持有行锁但未提交事务
  4. 判定：应用未提交事务 → 建议 KILL 会话或联系应用方设 wait_timeout / innodb_lock_wait_timeout
- **模拟**: Session A `BEGIN; UPDATE t SET x=1 WHERE id=1;`（不 COMMIT）; Session B~T 更新同一行
- **指标**: lock_wait_sessions > 15, blocking_chains ≥ 1, innodb_row_lock_time_avg > 5000ms

### T002 — InnoDB 行锁级联：blocker 在跑慢 SQL
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查阻塞链 → root blocker ID=456
  3. 查 blocker 状态 → COMMAND='Query', INFO 是全表扫描 UPDATE, TIME=120s
  4. 判定：blocker 的慢 SQL 导致长时间持锁 → 建议优化该 SQL（加索引/改写）
- **模拟**: Session A 执行无索引大表 `UPDATE t SET x=x+1 WHERE col LIKE '%abc%'`; Session B~T 等待同一行
- **指标**: lock_wait_sessions > 10, long_queries > 1, blocking_chains ≥ 1

### T003 — InnoDB 行锁级联：blocker 在等 IO
- **推理链**（5 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查阻塞链 → root blocker ID=789
  3. 查 blocker 状态 → COMMAND='Query', 正在执行但等待 IO
  4. 查 performance_schema.events_waits_current → wait/io/file/innodb/innodb_data_file
  5. 判定：根因是存储 IO 问题而非锁问题 → 建议先解决 IO 延迟
- **模拟**: IO 限速 + Session A 执行正常 UPDATE（因 IO 慢变长事务）; Session B~T 等待
- **指标**: lock_wait_sessions > 10, io_wait_sessions > 3

### T004 — 多层阻塞链分析
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 阈值, 阻塞链深度 ≥ 3
  2. 查阻塞树 → A 阻塞 B, B 阻塞 C, C 阻塞 D~Z
  3. 定位最上层 root blocker A → 查其状态和 SQL
  4. 判定：A 是根因 → 按 A 的状态给出建议（不是处理 B 或 C）
- **模拟**: 4 个会话串联锁依赖 + 20 个会话排队
- **指标**: blocking_chains 层数 ≥ 3, lock_wait_sessions > 20

### T005 — 死锁 + 频率判断
- **推理链**（3 步）:
  1. 检测 innodb_deadlocks > 0（即时触发）
  2. 查死锁频率：偶发（< 1 次/小时）还是频繁（> 5 次/小时）
  3. 频率低 → 建议重试逻辑即可; 频率高 → 查 SHOW ENGINE INNODB STATUS 的 LATEST DEADLOCK，建议统一访问顺序
- **模拟**: 两个会话交叉更新两行，循环触发死锁（频率可控）
- **指标**: innodb_deadlocks > 3/h

### T006 — Metadata Lock 阻塞 DML
- **推理链**（4 步）:
  1. 检测大量会话状态 "Waiting for table metadata lock"
  2. 查 performance_schema.metadata_locks → 有 EXCLUSIVE MDL 请求（ALTER TABLE / DROP TABLE）
  3. 查阻塞链 → DDL 在等活跃事务释放 SHARED_READ MDL，同时阻塞后续所有查询
  4. 判定：DDL/DML 冲突 → 建议设 lock_wait_timeout + 在维护窗口做 DDL 或用 pt-online-schema-change / gh-ost
- **模拟**: 长事务运行中 + `ALTER TABLE t ADD COLUMN new_col INT`
- **指标**: metadata_lock_wait_sessions > 5, lock_wait_sessions spike

### T007 — Gap Lock 导致 INSERT 阻塞
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 升高 + 锁类型为 RECORD 类型
  2. 查 data_locks → LOCK_MODE='X,GAP', 多个 INSERT 被 gap lock 阻塞
  3. 查触发 gap lock 的事务 → REPEATABLE READ 隔离级别下的范围查询
  4. 判定：gap lock 范围过大阻塞 INSERT → 建议改用 READ COMMITTED 隔离级别（如业务允许）或缩小查询范围
- **模拟**: REPEATABLE READ + `SELECT * FROM t WHERE id BETWEEN 10 AND 20 FOR UPDATE` + 另一会话 INSERT id=15
- **指标**: lock_wait_sessions > 5, innodb_row_lock_waits spike

### T008 — Next-Key Lock 导致并发 INSERT 死锁
- **推理链**（4 步）:
  1. 检测 innodb_deadlocks 增加
  2. 查 SHOW ENGINE INNODB STATUS → 死锁涉及 INSERT + next-key lock
  3. 查隔离级别 → REPEATABLE READ, INSERT 需要检查唯一键导致 gap lock 交叉
  4. 判定：唯一索引 + RR 隔离级别下的 INSERT 死锁 → 建议改 READ COMMITTED 或应用层串行化
- **模拟**: 两个会话同时 INSERT 相邻值到唯一索引列
- **指标**: innodb_deadlocks > 0

### T009 — FLUSH TABLES WITH READ LOCK 阻塞全库
- **推理链**（4 步）:
  1. 检测所有写操作挂起 + lock_wait_sessions spike
  2. 查 processlist → 有会话执行 FTWRL, 状态 "Waiting for table flush"
  3. 查 FTWRL 来源 → mysqldump --single-transaction 未使用 或备份工具配置错误
  4. 判定：FTWRL 导致全库阻塞 → 建议使用 mysqldump --single-transaction + 检查备份工具配置
- **模拟**: `FLUSH TABLES WITH READ LOCK` + 并发 DML
- **指标**: lock_wait_sessions spike, 所有写入暂停

### T010 — 外键缺索引导致锁扩散
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 升高 + 查询变慢
  2. 查阻塞链 → blocker 在执行 DELETE FROM parent 或 UPDATE parent
  3. 查子表外键列 → 无索引（MySQL 8.0 前不强制），导致子表全表扫描加锁验证
  4. 判定：子表 FK 列缺索引导致全表扫描 + 大量锁持有 → 建议 CREATE INDEX ON child(fk_col)
- **模拟**: 父子表 FK 无索引, `DELETE FROM parent WHERE id=1`（子表百万行）
- **指标**: lock_wait_sessions > 5, innodb_row_lock_time_avg 冲高

### T011 — Online DDL 阻塞分析
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike + 一个 DDL 操作长时间运行
  2. 查 processlist → ALTER TABLE ... ALGORITHM=INPLACE 在最后阶段等 MDL
  3. 查等待原因 → DDL 完成阶段需 EXCLUSIVE MDL，被长事务阻塞
  4. 判定：Online DDL 最后阶段需要排他锁 → 建议确保无长事务 + 设 lock_wait_timeout
- **模拟**: 长事务 + `ALTER TABLE t ADD INDEX idx_col (col), ALGORITHM=INPLACE`
- **指标**: metadata_lock_wait_sessions > 0, long_queries > 0

### T012 — 自增锁争用（innodb_autoinc_lock_mode）
- **推理链**（4 步）:
  1. 检测 active_sessions 升高 + INSERT 性能下降
  2. 查 performance_schema → wait/synch/mutex/innodb/autoinc_mutex 争用
  3. 查 innodb_autoinc_lock_mode → 0 或 1（传统模式）
  4. 判定：自增锁争用 → 建议设 innodb_autoinc_lock_mode=2（交错模式，需确认复制安全性）
- **模拟**: 100 路并发 INSERT + innodb_autoinc_lock_mode=1
- **指标**: active_sessions 升高, INSERT 吞吐下降

### T013 — 表级锁与 InnoDB 行锁混用
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike + 部分会话等待时间异常长
  2. 查 processlist → 有会话执行了 LOCK TABLES ... WRITE
  3. 查 LOCK TABLES 来源 → 遗留应用或 MyISAM 迁移代码
  4. 判定：表级锁阻塞所有并发 → 建议改用 InnoDB 行级事务替代 LOCK TABLES
- **模拟**: `LOCK TABLES t WRITE` + 并发读写该表
- **指标**: lock_wait_sessions spike

### T014 — INSERT ... ON DUPLICATE KEY UPDATE 热点争用
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 升高 + innodb_row_lock_waits spike
  2. 查 Top SQL → `INSERT INTO t ... ON DUPLICATE KEY UPDATE`，高并发写入同一批 key
  3. 查 data_locks → 大量 next-key lock 争用
  4. 判定：UPSERT 导致热点行锁 → 建议分散写入（hash 分区）或降低并发度 + 考虑 REPLACE INTO 的差异
- **模拟**: 100 路并发 INSERT ON DUPLICATE KEY UPDATE 到相同 key 范围
- **指标**: innodb_row_lock_waits spike, lock_wait_sessions > 10

### T015 — 锁等待超时后重试风暴
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 波动 + innodb_lock_wait_timeout 触发频繁
  2. 查 performance_schema → 大量 ERROR 1205 (Lock wait timeout exceeded)
  3. 查应用行为 → 超时后立即重试，造成重试风暴
  4. 判定：无退避的重试策略 → 建议应用实现指数退避 + 适当调整 innodb_lock_wait_timeout
- **模拟**: innodb_lock_wait_timeout=5 + 高并发争抢同一行 + 应用立即重试
- **指标**: lock_wait_sessions 波动, innodb_lock_wait_timeouts spike

---

## 二、SQL 性能（20 个场景）

### T016 — 全表扫描：缺索引 + 确认应该加索引
- **推理链**（4 步）:
  1. 检测 cpu_sessions / io_wait_sessions 冲高
  2. 查 Top SQL（performance_schema.events_statements_summary_by_digest）→ 全表扫描, rows_examined >> rows_sent
  3. 查 WHERE 条件列 → 无索引, 选择度高（cardinality 大）
  4. 判定：应加索引 → 建议 CREATE INDEX + EXPLAIN 验证
- **模拟**: 百万行表 `SELECT * FROM t WHERE email = 'x@y.com'` 无索引
- **指标**: active_sessions 冲高, innodb_buffer_pool_reads spike

### T017 — 全表扫描：选择度低不该加索引
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 冲高
  2. 查 Top SQL → type=ALL（全表扫描）
  3. 查 WHERE 条件列 → 选择度极低（status='active' 占 95%），加索引反而更差
  4. 判定：全扫是合理选择 → 建议分区裁剪或覆盖索引，不建议加 B-tree 索引
- **模拟**: 百万行表 status 列只有 3 个值（active=95%），查 status='active'
- **指标**: innodb_buffer_pool_reads 高但 active_sessions 不高

### T018 — 执行计划漂移：统计信息过期
- **推理链**（5 步）:
  1. 检测 avg_query_time_ms 突然升高
  2. 查 Top SQL → 某 SQL 的平均执行时间从 5ms 变成 500ms
  3. 查 EXPLAIN → 计划从 ref 变成 ALL（全扫）
  4. 查 information_schema.TABLES → 表统计信息过期（UPDATE_TIME 很早），innodb_stats_persistent 采样不足
  5. 判定：统计信息过期导致计划回退 → 建议 ANALYZE TABLE + 调整 innodb_stats_persistent_sample_pages
- **模拟**: 大量 INSERT 改变数据分布后不 ANALYZE TABLE
- **指标**: avg_query_time_ms spike

### T019 — 隐式类型转换导致索引失效
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + rows_examined >> rows_sent
  2. 查 Top SQL → `WHERE varchar_col = 123`（数字与 VARCHAR 比较）
  3. 查 EXPLAIN → type=ALL（MySQL 对 varchar 列做隐式转换，无法使用索引）
  4. 判定：隐式类型转换 → 建议改 `WHERE varchar_col = '123'`
- **模拟**: VARCHAR 列有索引，用数字条件查询
- **指标**: cpu_sessions 升高, rows_examined >> rows_sent

### T020 — 函数索引失效：WHERE 条件函数不匹配
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 升高
  2. 查 Top SQL → `WHERE DATE(created_at) = '2026-01-01'`
  3. 查索引 → 只有 created_at 上的 B-tree，DATE() 函数导致索引无法使用
  4. 判定：函数破坏索引 → 建议改写为范围查询 `WHERE created_at >= '2026-01-01' AND created_at < '2026-01-02'` 或创建函数索引（MySQL 8.0+）
- **模拟**: 百万行表，有 created_at 索引，但查询用 DATE(created_at)
- **指标**: avg_query_time_ms 升高

### T021 — LIKE '%keyword%' 全表扫描
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高
  2. 查 Top SQL → `WHERE content LIKE '%keyword%'`, type=ALL
  3. 查索引 → B-tree 无法支持前模糊匹配
  4. 判定：前模糊 B-tree 无法用 → 建议 FULLTEXT INDEX 或 外部搜索引擎（Elasticsearch）
- **模拟**: 百万行文本列 `LIKE '%search%'`
- **指标**: cpu_sessions 升高, long_queries > 0

### T022 — 分区裁剪失效
- **推理链**（4 步）:
  1. 检测 rows_examined spike + io_wait_sessions 升高
  2. 查 Top SQL → 分区表查询, EXPLAIN 显示 partitions=ALL
  3. 查 WHERE 条件 → 对分区键做了函数转换 `WHERE YEAR(created_at) = 2026`
  4. 判定：分区裁剪失效 → 建议改写为范围条件 `WHERE created_at >= '2026-01-01' AND created_at < '2027-01-01'`
- **模拟**: 按月 RANGE 分区表用函数条件查询
- **指标**: rows_examined spike

### T023 — NOT IN 子查询性能陷阱
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + long_queries 增加
  2. 查 Top SQL → `WHERE id NOT IN (SELECT id FROM other_table)`
  3. 查 EXPLAIN → DEPENDENT SUBQUERY（MySQL 可能无法优化为 Anti Join）
  4. 判定：NOT IN 退化为相关子查询 → 建议改 `WHERE NOT EXISTS (SELECT 1 FROM other_table WHERE ...)` 或 LEFT JOIN ... IS NULL
- **模拟**: 子查询结果含 NULL + 大表 NOT IN
- **指标**: cpu_sessions 升高, long_queries > 0

### T024 — OFFSET 大分页性能退化
- **推理链**（3 步）:
  1. 检测 avg_query_time_ms 逐渐升高
  2. 查 Top SQL → `SELECT * FROM t ORDER BY id LIMIT 20 OFFSET 500000`
  3. 判定：大 OFFSET 需扫描前 50 万行再丢弃 → 建议改 keyset pagination（WHERE id > last_id LIMIT 20）
- **模拟**: 翻页到 2 万页
- **指标**: avg_query_time_ms 随翻页深度线性增长

### T025 — 多列索引顺序不当
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 偏高
  2. 查 Top SQL → `WHERE status = 'active' AND created_at > '2026-01-01'`
  3. 查索引 → INDEX(created_at, status) — 顺序反了，等值条件列 status 不在前面
  4. 判定：索引列顺序不优 → 建议重建为 INDEX(status, created_at) 让等值条件列在前
- **模拟**: 复合索引顺序反的情况
- **指标**: avg_query_time_ms 偏高

### T026 — 过多索引导致写入变慢
- **推理链**（4 步）:
  1. 检测 INSERT/UPDATE TPS 下降 + avg_query_time_ms（写入）升高
  2. 查目标表 → 有 12 个索引
  3. 查索引使用情况 → performance_schema.table_io_waits_summary_by_index_usage 显示 6 个索引从未被查询使用
  4. 判定：冗余索引拖慢写入 → 建议 DROP 未使用索引
- **模拟**: 高并发写入 + 12 个索引的表
- **指标**: INSERT/UPDATE TPS 下降

### T027 — 相关子查询 N+1 问题
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + rows_examined 极高
  2. 查 Top SQL → `SELECT *, (SELECT COUNT(*) FROM orders WHERE orders.user_id = users.id) FROM users`
  3. 查 EXPLAIN → DEPENDENT SUBQUERY，对每行执行子查询
  4. 判定：相关子查询导致 N+1 → 建议改为 LEFT JOIN + GROUP BY
- **模拟**: users 表 10 万行，每行触发一次子查询
- **指标**: cpu_sessions 升高, rows_examined 极高

### T028 — JOIN 缺索引导致全表扫描
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 冲高 + long_queries 增加
  2. 查 Top SQL → `SELECT * FROM a JOIN b ON a.col = b.col`, EXPLAIN 显示 b 表 type=ALL
  3. 查 b.col → 无索引, join_buffer 溢出到磁盘
  4. 判定：JOIN 列缺索引 → 建议 CREATE INDEX ON b(col) + 检查 join_buffer_size
- **模拟**: 两个大表 JOIN, 被驱动表 JOIN 列无索引
- **指标**: io_wait_sessions 冲高, created_tmp_disk_tables spike

### T029 — 大排序溢出到磁盘
- **推理链**（4 步）:
  1. 检测 created_tmp_disk_tables spike + sort_merge_passes 升高
  2. 查 Top SQL → ORDER BY 大结果集
  3. 查 sort_buffer_size → 256KB（太小）
  4. 判定：sort_buffer_size 不足导致磁盘排序 → 建议适当增大 sort_buffer_size 或 优化 SQL 减少排序量 + 加排序索引
- **模拟**: `SELECT * FROM big_table ORDER BY col1, col2, col3` + sort_buffer_size=256KB
- **指标**: sort_merge_passes spike, created_tmp_disk_tables 升高

### T030 — Hash Join 溢出（MySQL 8.0.18+）
- **推理链**（4 步）:
  1. 检测 created_tmp_disk_tables spike + io_wait_sessions 增加
  2. 查 Top SQL → 无索引的 JOIN, EXPLAIN 显示 hash join
  3. 查 join_buffer_size → 256KB（太小，hash 表放不下）
  4. 判定：join_buffer_size 不足 → 建议增大 join_buffer_size 或为 JOIN 列加索引
- **模拟**: 两个大表 JOIN 无索引 + join_buffer_size=256KB
- **指标**: created_tmp_disk_tables spike

### T031 — SELECT * 导致回表开销大
- **推理链**（4 步）:
  1. 检测 innodb_buffer_pool_reads 冲高 + 查询慢
  2. 查 Top SQL → `SELECT * FROM t WHERE idx_col = 'x'`, 虽然走索引但回表严重
  3. 查表结构 → 宽表 50+ 列，SELECT * 导致大量随机 IO 回表读
  4. 判定：SELECT * 回表开销大 → 建议只 SELECT 需要的列 + 考虑覆盖索引
- **模拟**: 宽表 + 二级索引查询 + SELECT *
- **指标**: innodb_buffer_pool_reads 冲高

### T032 — Filesort + Temporary 双重开销
- **推理链**（4 步）:
  1. 检测 created_tmp_disk_tables spike + sort_merge_passes 升高 + long_queries 增加
  2. 查 Top SQL → GROUP BY + ORDER BY 不同列，EXPLAIN Extra: Using temporary; Using filesort
  3. 查数据量 → 临时表超过 tmp_table_size 溢出到磁盘
  4. 判定：临时表 + 排序双重磁盘 IO → 建议优化 SQL（让 GROUP BY 和 ORDER BY 使用同一索引）+ 适当增大 tmp_table_size
- **模拟**: `SELECT col1, COUNT(*) FROM big_table GROUP BY col1 ORDER BY col2`
- **指标**: created_tmp_disk_tables spike, sort_merge_passes 升高

### T033 — 大表 COUNT(*) 无 WHERE 条件
- **推理链**（3 步）:
  1. 检测 long_queries > 0, 某 SQL 持续运行 30s+
  2. 查 SQL → `SELECT COUNT(*) FROM big_table`（InnoDB 需要遍历索引计数）
  3. 判定：InnoDB 无法快速 COUNT → 建议使用 information_schema.TABLES.TABLE_ROWS 近似值 或维护计数器表
- **模拟**: 5000 万行表 COUNT(*)
- **指标**: long_queries > 0, innodb_buffer_pool_reads 升高

### T034 — 大 IN 列表导致优化器估算偏差
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高
  2. 查 Top SQL → `WHERE id IN (1,2,3,...,10000)` 包含上万个值
  3. 查 EXPLAIN → range 扫描但优化器估算行数不准，可能退化
  4. 判定：大 IN 列表效率低 → 建议改用临时表 JOIN 或分批查询
- **模拟**: IN 列表包含 10000 个值
- **指标**: cpu_sessions 升高

### T035 — 窗口函数无合适索引（MySQL 8.0+）
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + created_tmp_disk_tables 升高
  2. 查 Top SQL → `ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC)`
  3. 查 EXPLAIN → 全表排序 + 临时表
  4. 判定：缺排序索引 → 建议 `CREATE INDEX ON t (user_id, created_at DESC)` 让窗口函数用索引
- **模拟**: 百万行表上窗口函数无合适索引
- **指标**: cpu_sessions 升高, created_tmp_disk_tables 升高

---

## 三、InnoDB 引擎（10 个场景）

### T036 — Buffer Pool 命中率下降：大查询冲刷
- **推理链**（4 步）:
  1. 检测 innodb_buffer_pool_hit_rate < 95%（回归触发）
  2. 查 innodb_buffer_pool_reads → spike
  3. 查 Top SQL → 大表全表扫描冲刷 buffer pool 的 LRU
  4. 判定：全扫冲刷 buffer pool → 建议加索引减少全扫 + 确认 innodb_old_blocks_time 设置（默认 1000ms 已保护，但极大扫描仍会影响）
- **模拟**: `SELECT * FROM very_large_table` + 并发 OLTP 小查询
- **指标**: innodb_buffer_pool_hit_rate < 95%, innodb_buffer_pool_reads spike

### T037 — Buffer Pool 命中率持续低：buffer pool 太小
- **推理链**（4 步）:
  1. 检测 innodb_buffer_pool_hit_rate 持续 < 95%
  2. 查 innodb_buffer_pool_reads → 持续高（非 spike）
  3. 查 innodb_buffer_pool_size → 512MB（活跃数据集 10GB）
  4. 判定：buffer pool 不足 → 建议增大到物理内存的 50-70%
- **模拟**: innodb_buffer_pool_size=512MB, 活跃数据集 10GB, 正常 OLTP 负载
- **指标**: innodb_buffer_pool_hit_rate 持续 < 95%, innodb_buffer_pool_reads 持续高

### T038 — Redo Log 太小导致频繁 Checkpoint
- **推理链**（4 步）:
  1. 检测 innodb_checkpoint_age 持续接近 innodb_log_file_size 上限
  2. 查 Innodb_log_waits → > 0（log buffer 等待冲刷）
  3. 查 redo log 大小 → innodb_log_file_size=48MB（太小）
  4. 判定：redo log 太小导致频繁 checkpoint → 建议增大 innodb_redo_log_capacity（8.0.30+）或 innodb_log_file_size 到 1-4GB
- **模拟**: innodb_log_file_size=48MB + 持续大量 DML
- **指标**: Innodb_log_waits > 0, innodb_checkpoint_age 接近上限

### T039 — Undo Log 膨胀：长事务阻止 purge
- **推理链**（4 步）:
  1. 检测 undo 表空间持续增长
  2. 查 information_schema.INNODB_TRX → 有事务运行数小时未提交
  3. 查 Innodb_purge_trx_no 和 history_list_length → purge 线程滞后
  4. 判定：长事务阻止 undo purge → 建议终止长事务 + 设 innodb_undo_log_truncate=ON + 监控 history_list_length
- **模拟**: 打开事务不关闭 + 持续大量 UPDATE
- **指标**: history_list_length 持续增长, undo 表空间增大

### T040 — Change Buffer 导致随机读变慢
- **推理链**（4 步）:
  1. 检测 SELECT 查询偶尔变慢 + innodb_buffer_pool_reads spike
  2. 查 Innodb_ibuf_merges → 合并操作频繁
  3. 查写入模式 → 大量随机 INSERT 到二级索引, change buffer 积累后 SELECT 触发合并
  4. 判定：change buffer 合并拖慢读取 → 如果读多写少建议设 innodb_change_buffering=none
- **模拟**: 大量随机 INSERT + 随后的 SELECT 触发合并
- **指标**: Innodb_ibuf_merges spike, 查询延迟波动

### T041 — Adaptive Hash Index 争用
- **推理链**（4 步）:
  1. 检测 active_sessions 升高 + performance_schema 显示 btr_search 相关等待
  2. 查 SHOW ENGINE INNODB STATUS → AHI hit ratio 极低或 AHI 分区争用
  3. 查负载类型 → 大量随机访问不同的索引页，AHI 命中率低但维护开销高
  4. 判定：AHI 收益低但开销大 → 建议 SET GLOBAL innodb_adaptive_hash_index=OFF 测试
- **模拟**: 大量不同模式的查询 + 高并发
- **指标**: active_sessions 升高, AHI hit ratio < 50%

### T042 — Doublewrite Buffer 写入延迟
- **推理链**（4 步）:
  1. 检测写入 TPS 下降 + io_wait_sessions 升高
  2. 查 performance_schema → wait/io/file/innodb/innodb_dblwr_file 占比高
  3. 查存储类型 → HDD，doublewrite 增加了额外写入
  4. 判定：doublewrite 在慢速存储上开销大 → 如使用支持原子写的 SSD/NVMe 可考虑 innodb_doublewrite=OFF（需确认存储支持原子写）
- **模拟**: HDD 存储 + 高并发写入
- **指标**: io_wait_sessions 升高, 写入延迟增加

### T043 — 临时表空间膨胀
- **推理链**（4 步）:
  1. 检测磁盘使用持续上升
  2. 查 ibtmp1 文件 → 持续增长（不会自动收缩）
  3. 查 Top SQL → 大量使用内部临时表的查询（GROUP BY / DISTINCT / UNION）
  4. 判定：临时表空间膨胀 → 建议重启 MySQL 释放临时表空间 + 优化产生临时表的 SQL + 设 innodb_temp_tablespaces_dir（8.0.28+）
- **模拟**: 大量产生磁盘临时表的查询
- **指标**: ibtmp1 文件持续增长

### T044 — InnoDB I/O 线程不足
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 持续高 + 吞吐不增长
  2. 查 SHOW ENGINE INNODB STATUS → pending reads/writes 堆积
  3. 查 innodb_read_io_threads / innodb_write_io_threads → 4（默认值，IOPS 高的 SSD 可能不足）
  4. 判定：I/O 线程数不匹配存储能力 → 建议增大到 8-16（需重启）
- **模拟**: 高 IOPS SSD + innodb_read_io_threads=4 + 高并发 OLTP
- **指标**: io_wait_sessions 持续高, pending I/O 堆积

### T045 — Page Cleaner 跟不上脏页产生速度
- **推理链**（4 步）:
  1. 检测写入 TPS 波动 + checkpoint_age 接近上限
  2. 查 Innodb_buffer_pool_pages_dirty → 持续高占比
  3. 查 innodb_page_cleaners → 1（默认值少于 buffer pool instances）
  4. 判定：page cleaner 线程不足 → 建议 innodb_page_cleaners = innodb_buffer_pool_instances + 调整 innodb_io_capacity
- **模拟**: 高写入负载 + innodb_page_cleaners=1 + innodb_buffer_pool_instances=8
- **指标**: Innodb_buffer_pool_pages_dirty 占比高, 写入 TPS 波动

---

## 四、内存与缓存（10 个场景）

### T046 — sort_buffer_size 全局设过大导致 OOM
- **推理链**（5 步）:
  1. 检测 MySQL 进程被 OOM Killer 杀掉 或连接断开
  2. 查 dmesg/syslog → OOM killer 杀掉了 mysqld
  3. 查 sort_buffer_size → 256MB（全局生效，每个排序操作独立分配）
  4. 计算最坏情况 → 200 连接 × 256MB = 50GB（远超物理内存）
  5. 判定：sort_buffer_size × 连接数 内存溢出 → 建议降到 256KB-2MB + 只对需要的查询 SET SESSION
- **模拟**: sort_buffer_size=256MB + 100 路并发排序查询
- **指标**: 进程被杀或连接异常断开

### T047 — join_buffer_size 不足导致 Block Nested Loop
- **推理链**（4 步）:
  1. 检测 long_queries 增加 + created_tmp_disk_tables spike
  2. 查 Top SQL → EXPLAIN Extra: Using join buffer (Block Nested Loop)
  3. 查 join_buffer_size → 256KB（默认，大 JOIN 不够用）
  4. 判定：join_buffer_size 不足 → 建议首选为 JOIN 列加索引，若无法加索引则适当增大 join_buffer_size
- **模拟**: 两大表 JOIN + 被驱动表无索引 + join_buffer_size=256KB
- **指标**: long_queries 增加

### T048 — tmp_table_size 不足导致大量磁盘临时表
- **推理链**（4 步）:
  1. 检测 created_tmp_disk_tables / created_tmp_tables > 25%（大量临时表落盘）
  2. 查 tmp_table_size / max_heap_table_size → 16MB（默认）
  3. 查产生临时表的 Top SQL → GROUP BY / DISTINCT 结果集大
  4. 判定：临时表内存不足 → 建议适当增大 tmp_table_size + max_heap_table_size（需同步）+ 优化 SQL
- **模拟**: 大量 GROUP BY 查询 + tmp_table_size=16MB
- **指标**: created_tmp_disk_tables / created_tmp_tables > 25%

### T049 — Table Open Cache 不足导致频繁打开关闭表
- **推理链**（4 步）:
  1. 检测 opened_tables 持续增长（非首次启动）
  2. 查 table_open_cache → 2000, 但数据库有 5000+ 表
  3. 查 table_open_cache_misses → 持续增加
  4. 判定：table_open_cache 不足 → 建议增大到表数量的 1-2 倍
- **模拟**: 5000 个表 + table_open_cache=2000 + 交替访问不同表
- **指标**: opened_tables 持续增长, table_open_cache_misses 升高

### T050 — Thread Cache 不足导致频繁创建线程
- **推理链**（4 步）:
  1. 检测 threads_created 持续增长 + 短连接频繁
  2. 查 thread_cache_size → 0 或太小
  3. 查连接模式 → 大量短连接（每次 connect/query/disconnect）
  4. 判定：无连接复用 → 建议增大 thread_cache_size 到 50-100 + 引入连接池（ProxySQL / 应用层连接池）
- **模拟**: 循环 connect/disconnect + thread_cache_size=0
- **指标**: threads_created 持续增长

### T051 — Query Cache 导致争用（MySQL 5.7）
- **推理链**（4 步）:
  1. 检测 active_sessions 升高但吞吐不增长
  2. 查 performance_schema → wait/synch/mutex/sql/LOCK_query_cache 争用
  3. 查 query_cache_type → ON, query_cache_size > 0
  4. 判定：Query Cache 在高并发写场景反而成为瓶颈 → 建议关闭 Query Cache（MySQL 8.0 已移除）
- **模拟**: 高并发读写 + query_cache_type=ON + query_cache_size=128MB
- **指标**: active_sessions 升高, Qcache_lowmem_prunes 升高

### T052 — Prepared Statement 泄漏
- **推理链**（4 步）:
  1. 检测 com_stmt_prepare 远大于 com_stmt_close
  2. 查 prepared_stmt_count → 接近 max_prepared_stmt_count
  3. 查应用 → 频繁 prepare 但不 close
  4. 判定：prepared statement 泄漏 → 建议修复应用层 close 逻辑 + 监控 prepared_stmt_count
- **模拟**: 循环 PREPARE 不 DEALLOCATE
- **指标**: prepared_stmt_count 持续增长

### T053 — InnoDB Buffer Pool Chunk Size 导致内存浪费
- **推理链**（3 步）:
  1. 检测 innodb_buffer_pool_size 实际分配远大于配置值
  2. 查 innodb_buffer_pool_chunk_size → 128MB, instances=8, 最终对齐到 chunk × instances 的倍数
  3. 判定：chunk_size × instances 对齐导致浪费 → 建议调整 chunk_size 让 buffer_pool_size 精确对齐
- **模拟**: innodb_buffer_pool_size=1.5GB, chunk_size=128MB, instances=8
- **指标**: 实际分配 > 配置值

### T054 — Binlog Cache 溢出到磁盘
- **推理链**（4 步）:
  1. 检测 binlog_cache_disk_use / binlog_cache_use > 5%（大量事务写临时文件）
  2. 查 binlog_cache_size → 32KB（默认, 大事务不够用）
  3. 查产生大事务的 SQL → 批量 INSERT / UPDATE 大量行
  4. 判定：binlog cache 不足 → 建议适当增大 binlog_cache_size（如 1-4MB）+ 分批提交大事务
- **模拟**: 大事务 UPDATE 100 万行 + binlog_cache_size=32KB
- **指标**: binlog_cache_disk_use > 5%

### T055 — 内存碎片化导致 MySQL 进程 RSS 持续增长
- **推理链**（4 步）:
  1. 检测 mysqld 进程 RSS 远大于 buffer_pool_size + 其他配置的内存总和
  2. 查 performance_schema.memory_summary_global_by_event_name → 定位高内存消耗模块
  3. 查 malloc 库 → 默认 glibc malloc 碎片化
  4. 判定：内存碎片化 → 建议使用 jemalloc 或 tcmalloc + 监控 RSS 增长趋势
- **模拟**: 长时间运行 + 高并发不同大小的内存分配
- **指标**: RSS 持续增长

---

## 五、Binlog 与复制（10 个场景）

### T056 — Binlog 写入速率过高
- **推理链**（4 步）:
  1. 检测 binlog 文件生成速度 > 100MB/min
  2. 查 Top SQL → 大批量 INSERT/UPDATE/DELETE
  3. 查 binlog_format → ROW（行模式对大批量操作每行记录一条）
  4. 判定：大批量 DML + ROW 模式产生过多 binlog → 建议分批提交 + 对不需要精确复制的操作考虑 binlog_row_image=MINIMAL
- **模拟**: `UPDATE big_table SET col=col+1`（全表更新百万行）
- **指标**: binlog 文件快速增长, Binlog_bytes_written spike

### T057 — sync_binlog 导致提交延迟
- **推理链**（4 步）:
  1. 检测 TPS 偏低 + 写入延迟高
  2. 查 performance_schema → wait/io/file/sql/binlog 等待占比高
  3. 查 sync_binlog → 1（每次提交都 fsync）+ 存储为 HDD
  4. 判定：sync_binlog=1 在慢存储上开销大 → 如可接受少量数据丢失建议 sync_binlog=100 或 0 + 迁移到 SSD
- **模拟**: sync_binlog=1 + HDD + 高频提交
- **指标**: TPS 偏低, 提交延迟高

### T058 — 主从复制延迟：大事务
- **推理链**（4 步）:
  1. 检测 Seconds_Behind_Master > 60（或 replication_lag_sec 升高）
  2. 查 relay log → 有单条大事务在回放（DELETE/UPDATE 百万行）
  3. 查 slave_parallel_workers → 单线程回放或 DATABASE 模式无法并行
  4. 判定：大事务导致单线程回放慢 → 建议拆分大事务 + 设 slave_parallel_type=LOGICAL_CLOCK
- **模拟**: 主库执行大事务 + 从库单线程复制
- **指标**: Seconds_Behind_Master > 60

### T059 — 主从复制延迟：从库无索引
- **推理链**（4 步）:
  1. 检测 Seconds_Behind_Master 持续增加
  2. 查 relay log 回放状态 → 每条 UPDATE 在从库全表扫描
  3. 查 binlog_format → ROW, 但从库上该表缺少主键或唯一索引
  4. 判定：从库无主键/索引导致 ROW 复制每次全表扫描 → 建议从库加主键 + 设 slave_rows_search_algorithms
- **模拟**: 无主键表 + ROW 复制 + 大量 UPDATE
- **指标**: Seconds_Behind_Master 持续增加

### T060 — Binlog 文件堆积占满磁盘
- **推理链**（4 步）:
  1. 检测磁盘使用 > 95%
  2. 查磁盘 → binlog 文件占用大量空间
  3. 查 expire_logs_days / binlog_expire_logs_seconds → 未设置或过长
  4. 判定：binlog 未自动清理 → 建议设 binlog_expire_logs_seconds=604800（7 天）+ PURGE BINARY LOGS 临时清理
- **模拟**: 不设过期 + 持续 DML
- **指标**: 磁盘使用 > 95%

### T061 — GTID 复制中断：errant transaction
- **推理链**（4 步）:
  1. 检测复制中断 + SHOW SLAVE STATUS 显示 GTID 不一致
  2. 查从库 → 有人在从库直接执行了写入操作，产生 errant GTID
  3. 查 GTID_EXECUTED vs GTID_PURGED → 从库 GTID 集合包含主库不存在的事务
  4. 判定：errant transaction → 建议在主库注入空事务对齐 GTID 或重建从库 + 从库设 super_read_only=ON
- **模拟**: 在从库执行 INSERT
- **指标**: 复制中断

### T062 — 半同步复制超时降级
- **推理链**（4 步）:
  1. 检测 Rpl_semi_sync_master_status → OFF（从 ON 变为 OFF）
  2. 查 Rpl_semi_sync_master_no_tx → 增加
  3. 查从库网络 → 网络延迟或从库 IO 慢导致 ACK 超时
  4. 判定：半同步降级为异步 → 建议检查网络 + 调整 rpl_semi_sync_master_timeout + 确认数据一致性
- **模拟**: 限制从库网络 + rpl_semi_sync_master_timeout=1000
- **指标**: Rpl_semi_sync_master_status=OFF

### T063 — Binlog 格式 STATEMENT 导致数据不一致
- **推理链**（4 步）:
  1. 检测主从数据不一致
  2. 查 binlog_format → STATEMENT
  3. 查问题 SQL → 包含不确定函数（UUID(), SYSDATE(), RAND()）
  4. 判定：STATEMENT 模式对不确定函数复制不安全 → 建议改为 ROW 或 MIXED 格式
- **模拟**: binlog_format=STATEMENT + `INSERT INTO t VALUES (UUID())`
- **指标**: 主从数据不一致

### T064 — relay_log_space_limit 导致 IO 线程暂停
- **推理链**（4 步）:
  1. 检测 Seconds_Behind_Master 突然增加 + IO Thread 状态变为 waiting
  2. 查 relay_log_space_limit → 设置了上限
  3. 查 SQL Thread → 回放速度慢，relay log 来不及清理
  4. 判定：relay log 空间限制导致 IO 线程暂停 → 建议增大 relay_log_space_limit 或提升 SQL Thread 速度
- **模拟**: relay_log_space_limit=512MB + 大量 binlog
- **指标**: Seconds_Behind_Master 增加, IO Thread waiting

### T065 — 多源复制通道冲突
- **推理链**（4 步）:
  1. 检测某个复制通道中断 + SQL Thread 报错
  2. 查 SHOW SLAVE STATUS FOR CHANNEL → ERROR: Duplicate entry
  3. 查多源配置 → 多个主库复制到同一从库，数据冲突
  4. 判定：多源复制数据冲突 → 建议配置 replicate_do_db 隔离 + 或使用不同 auto_increment_offset
- **模拟**: 两个主库写同一个表到同一个从库
- **指标**: 复制通道中断

---

## 六、等待事件与延迟（10 个场景）

### T066 — wait/io/file/innodb/innodb_data_file 延迟高
- **推理链**（4 步）:
  1. 检测 io_wait_sessions > 10（持续）
  2. 查 performance_schema.events_waits_summary_global_by_event_name → innodb_data_file 等待时间占比高
  3. 查存储 IO → 读写延迟异常
  4. 判定：底层存储性能下降 → 建议检查磁盘健康 + 迁移到 SSD + 增大 buffer pool 减少 IO
- **模拟**: IO 限速 + 正常 OLTP 负载
- **指标**: io_wait_sessions > 10

### T067 — wait/io/file/innodb/innodb_log_file 延迟（Redo Log 写入慢）
- **推理链**（4 步）:
  1. 检测 TPS 下降 + 提交延迟升高
  2. 查 performance_schema → innodb_log_file 等待时间高
  3. 查 redo log 所在磁盘 → IO 延迟异常
  4. 判定：redo log 写入瓶颈 → 建议将 redo log 迁移到快速 SSD + 增大 innodb_log_buffer_size
- **模拟**: Redo log 在慢磁盘 + 高频提交
- **指标**: TPS 下降, innodb_log_file 等待高

### T068 — wait/synch/mutex/innodb/buf_pool_mutex 争用
- **推理链**（4 步）:
  1. 检测 active_sessions 升高但吞吐不增长
  2. 查 performance_schema → buf_pool_mutex 等待占比高
  3. 查 innodb_buffer_pool_instances → 1（默认, 单个 buffer pool 实例）
  4. 判定：单 buffer pool 实例在高并发下争用 → 建议增大 innodb_buffer_pool_instances 到 8-16
- **模拟**: innodb_buffer_pool_instances=1 + 高并发 OLTP
- **指标**: active_sessions 升高, buf_pool_mutex 等待高

### T069 — wait/synch/mutex/innodb/trx_mutex 争用
- **推理链**（4 步）:
  1. 检测 TPS 下降 + active_sessions 升高
  2. 查 performance_schema → trx_mutex 等待占比高
  3. 查事务模式 → 大量短事务频繁 BEGIN/COMMIT
  4. 判定：事务管理器争用 → 建议减少事务频率（批量提交）+ 检查 innodb_thread_concurrency
- **模拟**: 1000 路并发单行事务
- **指标**: TPS 下降, trx_mutex 等待高

### T070 — wait/synch/rwlock/innodb/btr_search_latch 争用（AHI Latch）
- **推理链**（4 步）:
  1. 检测 active_sessions 升高 + performance_schema 显示 btr_search_latch 争用
  2. 查 innodb_adaptive_hash_index → ON
  3. 查 SHOW ENGINE INNODB STATUS → AHI 分区间负载不均
  4. 判定：AHI latch 争用 → 建议关闭 AHI: SET GLOBAL innodb_adaptive_hash_index=OFF
- **模拟**: 高并发随机查询 + AHI ON
- **指标**: active_sessions 升高, btr_search_latch 等待高

### T071 — wait/io/table/sql/handler 高延迟（表 IO 等待）
- **推理链**（4 步）:
  1. 检测查询延迟整体偏高
  2. 查 performance_schema.table_io_waits_summary_by_table → 某表等待时间异常
  3. 查该表 → 频繁全扫 + 数据量大
  4. 判定：表级 IO 等待集中在某几个热表 → 建议优化热表查询 + 加索引 + 考虑分区
- **模拟**: 高并发访问无索引大表
- **指标**: 特定表 io_wait 异常高

### T072 — wait/synch/mutex/sql/LOCK_open 争用（表定义缓存）
- **推理链**（4 步）:
  1. 检测 active_sessions 升高 + opened_tables 持续增长
  2. 查 performance_schema → LOCK_open mutex 争用
  3. 查 table_definition_cache → 太小（默认 2000，数据库有 10000+ 表）
  4. 判定：表定义缓存不足 → 建议增大 table_definition_cache + table_open_cache
- **模拟**: 10000 个表 + table_definition_cache=2000 + 高并发查询不同表
- **指标**: LOCK_open 争用高, opened_tables 增长

### T073 — wait/synch/cond/sql/MYSQL_BIN_LOG::COND_done（Binlog Group Commit 等待）
- **推理链**（4 步）:
  1. 检测 TPS 不增长 + 提交延迟偏高
  2. 查 performance_schema → binlog group commit 相关等待
  3. 查 binlog_group_commit_sync_delay → 0（无延迟聚合，每次单独 fsync）
  4. 判定：group commit 未充分聚合 → 建议设 binlog_group_commit_sync_delay=1000-10000μs 提升吞吐
- **模拟**: 高频小事务 + binlog_group_commit_sync_delay=0
- **指标**: TPS 不增长, fsync 频率高

### T074 — 提交速率骤降 + 根因定位
- **推理链**（4 步）:
  1. 检测 TPS 从 500/s 降到 < 100/s
  2. 查 active_sessions → 不降反升
  3. 查 Top Wait Event → 锁等待或 IO 等待
  4. 判定：吞吐下降但会话不降 → 存在阻塞（非正常业务低峰）→ 查锁或 IO
- **模拟**: 锁住关键表后所有事务等待
- **指标**: TPS drop > 80%, active_sessions 不变

### T075 — CPU 自旋等待（高并发下 spin_rounds 冲高）
- **推理链**（4 步）:
  1. 检测 cpu_sessions 异常高 + 实际吞吐未增长
  2. 查 SHOW ENGINE INNODB STATUS → spin_rounds / spin_waits 比值高
  3. 查并发度 → active_sessions > CPU 核数的 3 倍
  4. 判定：过度并发导致 spin 等待 → 建议调整 innodb_spin_wait_delay + 引入连接池限制并发
- **模拟**: 200 路并发 + 8 核 CPU
- **指标**: cpu_sessions 异常高, TPS 不增

---

## 七、连接与会话管理（10 个场景）

### T076 — 连接风暴 + 无连接池
- **推理链**（4 步）:
  1. 检测 connections_pct > 80% + threads_created spike
  2. 查 processlist → 大量来自同一 host 的短连接
  3. 查是否使用连接池 → 未使用
  4. 判定：应用层无连接池 → 建议引入 ProxySQL / HikariCP + 增大 thread_cache_size
- **模拟**: 循环 connect/query/disconnect 每秒 500 次
- **指标**: connections_pct > 80%, threads_created spike

### T077 — Connections 接近 max_connections
- **推理链**（4 步）:
  1. 检测 connections_pct > 90%（或 Threads_connected 接近 max_connections）
  2. 查 processlist → 按 user/host 分组
  3. 定位消耗最多连接的来源 → 某微服务每实例 50 连接 × 10 实例
  4. 判定：连接数溢出 → 建议引入连接池 + 减小每实例池大小 + 临时增大 max_connections
- **模拟**: 打开连接到 max_connections 的 90%
- **指标**: connections_pct > 90%

### T078 — Sleep 会话堆积
- **推理链**（4 步）:
  1. 检测 Threads_connected 持续升高但 active_sessions 不升
  2. 查 processlist → 90% 会话 Command='Sleep', Time > 3600s
  3. 查应用 → 连接池 min_idle 设太高或连接泄漏
  4. 判定：空闲连接浪费 → 建议设 wait_timeout=300 + interactive_timeout=300 + 修复应用连接管理
- **模拟**: 打开连接后不关闭，持续累积
- **指标**: Threads_connected 持续升高, 大量 Sleep 会话

### T079 — 会话泄漏（趋势检测 T3）
- **推理链**（4 步）:
  1. 检测 Threads_connected 持续上升趋势（T3 线性回归斜率 > 0.5σ）
  2. 查 processlist → Sleep 会话持续增长, Time 值很大
  3. 查 user/host → 定位泄漏来源
  4. 判定：应用未正确关闭连接 → 建议修复应用连接管理 + 设 wait_timeout
- **模拟**: 打开连接后不关闭，持续累积
- **指标**: Threads_connected T3 趋势上升

### T080 — Aborted Connects 冲高
- **推理链**（3 步）:
  1. 检测 Aborted_connects spike
  2. 查原因 → 密码错误？max_connections 已满？SSL 握手失败？
  3. 判定：如果 max_connections 满 → 增大参数; 如果密码错误 → 排查应用配置; 如果 SSL → 检查证书
- **模拟**: 错误密码批量连接尝试 或 max_connections 满后继续连接
- **指标**: Aborted_connects spike

### T081 — Aborted Clients 冲高（客户端异常断开）
- **推理链**（4 步）:
  1. 检测 Aborted_clients 持续增加
  2. 查网络 → 网络不稳定 或客户端超时设置过短
  3. 查 wait_timeout → 28800（8 小时，客户端超时远小于此）
  4. 判定：客户端连接被意外关闭 → 建议检查网络 + 调整客户端连接超时 + 应用层重连逻辑
- **模拟**: 客户端连接后网络中断
- **指标**: Aborted_clients 持续增加

### T082 — max_connections 对比 open_files_limit 不足
- **推理链**（4 步）:
  1. 检测 "Too many open files" 错误
  2. 查 open_files_limit → 1024
  3. 计算需求 → max_connections(500) + table_open_cache(4000) + ... > 1024
  4. 判定：文件描述符不足 → 建议在 systemd/limits.conf 增大 open_files_limit
- **模拟**: open_files_limit=1024 + 高并发 + 多表访问
- **指标**: 错误日志 "Too many open files"

### T083 — Active Sessions 加速度突增（T4）
- **推理链**（3 步）:
  1. 检测 active_sessions 二阶导数 > std（T4 加速度）
  2. 查 spike 时间点对应的等待事件 → 定位是锁/IO/CPU
  3. 判定：根据等待事件分类给出根因
- **模拟**: 定时任务在某一秒启动 50 个并发
- **指标**: active_sessions 1 秒内从 5 跳到 50

### T084 — 全库 Hang 分析
- **推理链**（5 步）:
  1. 检测 active_sessions > 30 但 TPS = 0
  2. 查 cpu_sessions → 0（无人在跑）
  3. 查 Top Wait → 全部在等某个事件
  4. 查等待链 → 是否有 MDL 全局锁 或 FTWRL
  5. 判定：数据库 Hang → 建议定位 root blocker + KILL 或等待锁释放
- **模拟**: FTWRL + 所有操作等待
- **指标**: active_sessions > 30, TPS = 0, cpu_sessions = 0

### T085 — ProxySQL 连接池配置不当
- **推理链**（4 步）:
  1. 检测 connections_pct 低但应用报连接超时
  2. 查 MySQL 连接数 → 正常（ProxySQL 限制了）
  3. 查 ProxySQL 配置 → max_connections=10, 大量请求排队超时
  4. 判定：ProxySQL 池太小 → 建议增大 max_connections 适配并发量 + 调整连接复用策略
- **模拟**: ProxySQL max_connections=10 + 100 并发请求
- **指标**: 应用超时, connections_pct 低

---

## 八、配置与参数问题（5 个场景）

### T086 — innodb_flush_log_at_trx_commit 设错导致数据丢失风险
- **推理链**（4 步）:
  1. 检测 TPS 异常高（比同配置的其他实例快很多）
  2. 查 innodb_flush_log_at_trx_commit → 0（每秒刷盘而非每次提交）
  3. 评估风险 → 崩溃可能丢失 1 秒数据
  4. 判定：配置不安全 → 建议改为 1（每次提交刷盘）确保 ACID + 如需性能用 2
- **模拟**: innodb_flush_log_at_trx_commit=0 vs 1 的性能差异
- **指标**: TPS 异常高

### T087 — innodb_flush_method 不当导致双重缓冲
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 偏高 + 内存使用高于预期
  2. 查 innodb_flush_method → fsync（默认）
  3. 查 OS → Linux, 数据文件经过 OS page cache 再写磁盘（双重缓冲）
  4. 判定：双重缓冲浪费内存 + 增加 IO → 建议改为 O_DIRECT 避免 OS cache 浪费
- **模拟**: innodb_flush_method=fsync + 大 buffer pool
- **指标**: 内存使用高, OS cache 占用大

### T088 — innodb_file_per_table=OFF 导致共享表空间膨胀
- **推理链**（4 步）:
  1. 检测 ibdata1 文件持续增长（不会自动收缩）
  2. 查 innodb_file_per_table → OFF（所有表数据在 ibdata1）
  3. 查 DROP TABLE 后 → ibdata1 大小不变（共享表空间不释放空间）
  4. 判定：共享表空间无法释放 → 建议开启 innodb_file_per_table=ON + 逐步迁移表到独立表空间
- **模拟**: innodb_file_per_table=OFF + 创建大表后 DROP
- **指标**: ibdata1 持续增长

### T089 — lower_case_table_names 迁移后表找不到
- **推理链**（3 步）:
  1. 检测应用报 "Table not found" 错误
  2. 查 lower_case_table_names → Linux 上为 0（区分大小写），但应用用大写表名创建
  3. 判定：大小写敏感问题 → 建议统一表名为小写 + 注意 lower_case_table_names 只能在初始化时设置
- **模拟**: Linux 上 lower_case_table_names=0, 混合大小写表名
- **指标**: 表访问报错

### T090 — sql_mode 不严格导致数据截断
- **推理链**（4 步）:
  1. 检测数据质量问题（字段值被截断、日期为 0000-00-00）
  2. 查 sql_mode → 未包含 STRICT_TRANS_TABLES
  3. 查 warnings → 大量 Data truncated, Incorrect date value
  4. 判定：sql_mode 不够严格 → 建议启用 STRICT_TRANS_TABLES + NO_ZERO_DATE + NO_ZERO_IN_DATE
- **模拟**: 非严格模式 + INSERT 超长字符串 / 无效日期
- **指标**: 数据质量异常

---

## 九、系统与运维（10 个场景）

### T091 — 慢查询日志未启用导致无法追踪
- **推理链**（3 步）:
  1. 用户报慢但无法定位 SQL
  2. 查 slow_query_log → OFF + performance_schema.events_statements 未启用 digest
  3. 判定：缺少慢查询追踪 → 建议启用 slow_query_log=ON + long_query_time=1 + log_queries_not_using_indexes=ON
- **模拟**: 慢查询但无法追踪
- **指标**: 无法定位慢 SQL

### T092 — ERROR 日志刷屏影响性能
- **推理链**（4 步）:
  1. 检测磁盘 IO 偏高 + 错误日志文件快速增长
  2. 查 error log → 大量重复告警（如 InnoDB: page_cleaner, Aborted connection）
  3. 查日志级别 → log_error_verbosity=3（所有信息）
  4. 判定：日志过详细 → 建议 log_error_verbosity=2 + 修复根因减少告警
- **模拟**: 大量连接异常断开导致日志刷屏
- **指标**: error log 快速增长

### T093 — pt-online-schema-change 导致负载升高
- **推理链**（4 步）:
  1. 检测 active_sessions spike + IO 升高 + 复制延迟
  2. 查 processlist → pt-osc 在执行批量数据复制
  3. 查 chunk-size → 1000（太大），每次复制造成大量写入
  4. 判定：pt-osc 参数不当 → 建议减小 chunk-size + 增大 chunk-time + 添加 --max-lag 限制
- **模拟**: pt-osc 操作大表 + chunk-size=5000
- **指标**: active_sessions spike, 复制延迟增加

### T094 — mysqldump 导致长事务阻塞
- **推理链**（4 步）:
  1. 检测 dead_tuple 堆积（InnoDB purge 滞后）+ history_list_length 增长
  2. 查 processlist → mysqldump --single-transaction 在执行，持有一致性读快照数小时
  3. 查 INNODB_TRX → 该事务 TRX_STARTED 很早
  4. 判定：mysqldump 长事务阻止 undo purge → 建议改用 Percona XtraBackup 物理备份 或分表导出
- **模拟**: mysqldump --single-transaction 导出大库
- **指标**: history_list_length 持续增长

### T095 — OPTIMIZE TABLE 阻塞读写
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike + 一个 DDL 操作长时间运行
  2. 查 processlist → OPTIMIZE TABLE big_table 需要重建表
  3. 查表大小 → 50GB，OPTIMIZE 需要几十分钟
  4. 判定：OPTIMIZE TABLE 在 InnoDB 上等同于 ALTER TABLE FORCE → 建议改用 pt-online-schema-change 或 ALTER TABLE ... ALGORITHM=INPLACE, LOCK=NONE
- **模拟**: 业务负载 + `OPTIMIZE TABLE big_table`
- **指标**: lock_wait_sessions spike

### T096 — 版本升级后 SQL 兼容性问题
- **推理链**（4 步）:
  1. 检测升级到 MySQL 8.0 后大量 SQL 报错
  2. 查错误类型 → 保留字冲突（如 rank, groups）+ 默认行为变化
  3. 查 sql_mode 差异 → 8.0 默认更严格
  4. 判定：版本兼容性问题 → 建议使用 mysql_upgrade 检查 + 用反引号包裹保留字 + 适配 sql_mode
- **模拟**: MySQL 5.7 SQL 在 8.0 执行
- **指标**: 大量 SQL 报错

### T097 — Event Scheduler 任务堆叠
- **推理链**（4 步）:
  1. 检测 active_sessions 定时升高 + cpu_sessions spike
  2. 查 information_schema.EVENTS + processlist → 多个相同 Event 实例同时运行
  3. 查 Event 定义 → 间隔 5 分钟但执行需 10 分钟
  4. 判定：Event 任务堆叠 → 建议在 Event 中加互斥锁（GET_LOCK）防重入 或增大间隔
- **模拟**: Event 每分钟触发一个 5 分钟的任务
- **指标**: active_sessions 定时升高

### T098 — 外键级联删除导致慢操作
- **推理链**（4 步）:
  1. 检测某个 DELETE 操作耗时异常长 + lock_wait_sessions 升高
  2. 查 SQL → `DELETE FROM parent WHERE id=1`, 该表有多级 CASCADE 外键
  3. 查外键树 → parent → child → grandchild, 级联删除数万行
  4. 判定：级联外键导致连锁删除 → 建议应用层控制删除顺序 或 改 ON DELETE SET NULL + 后台清理
- **模拟**: 3 层级联外键 + DELETE 父表一行
- **指标**: long_queries > 0, lock_wait_sessions 升高

### T099 — InnoDB 数据字典锁争用（大量表操作）
- **推理链**（4 步）:
  1. 检测 DDL 操作变慢 + SHOW TABLES/INFORMATION_SCHEMA 查询延迟高
  2. 查 performance_schema → dict_sys_mutex 争用
  3. 查表数量 → 数据库有 50000+ 表
  4. 判定：大量表导致数据字典争用 → 建议分库 + 减少表数量 + 避免高频访问 information_schema
- **模拟**: 50000 个表 + 并发 DDL + 并发 SHOW TABLES
- **指标**: DDL 延迟高, dict_sys_mutex 争用

### T100 — 综合场景：IO 慢 + 锁等待 + Binlog 堆积
- **推理链**（6 步）:
  1. 检测 active_sessions spike + 多项指标异常
  2. 查等待事件分布 → IO 30% + Lock 40% + Binlog 20%
  3. 查锁等待 → blocker 在等 IO（innodb_data_file read）
  4. 查 IO 延迟 → 存储异常
  5. 查 Binlog → sync_binlog=1 也因 IO 慢导致提交堆积
  6. 判定：根因是 IO 子系统 → 锁等待和 binlog 写入慢都是 IO 慢的连锁反应
- **模拟**: IO 限速 + 并发 DML + 互相等待
- **指标**: io_wait_sessions > 10, lock_wait_sessions > 10, TPS 骤降

---

## 统计摘要

### 按推理链步数分布

| 步数 | 场景数 | 占比 |
|------|--------|------|
| 3 步 | 20 | 20% |
| 4 步 | 67 | 67% |
| 5 步 | 10 | 10% |
| 6 步 | 3 | 3% |

**平均推理链深度: 3.96 步**

### 按分类分布

| 分类 | 场景数 |
|------|--------|
| 锁与阻塞 | 15 |
| SQL 性能 | 20 |
| InnoDB 引擎 | 10 |
| 内存与缓存 | 10 |
| Binlog 与复制 | 10 |
| 等待与延迟 | 10 |
| 连接与会话 | 10 |
| 配置与参数 | 5 |
| 系统与运维 | 10 |

### 与 Oracle/PG 分类对比

| MySQL 分类 | Oracle 对应 | PG 对应 | 差异说明 |
|-----------|------------|---------|---------|
| 锁与阻塞 | 锁与阻塞 | 锁与阻塞 | MySQL 独有：MDL、Gap Lock、Next-Key Lock、FTWRL；无 ITL/Sequence/HW（Oracle）、无 Advisory Lock（PG）|
| SQL 性能 | SQL 性能 | SQL 性能 | MySQL 独有：SELECT * 回表、Block Nested Loop；无 HINT 系统/SPM/ACS（Oracle）、无 CTE Fence（PG） |
| InnoDB 引擎 | 存储与容量 | VACUUM 与 MVCC | **MySQL 独有**：Change Buffer、AHI、Doublewrite、Page Cleaner；Oracle UNDO 自动管理；PG 需要 VACUUM |
| 内存与缓存 | 内存与缓存 | 内存与缓存 | MySQL 独有：Buffer Pool Instances、Thread Cache、Query Cache(5.7)；无 SGA/PGA（Oracle）、无 shared_buffers/work_mem（PG） |
| Binlog 与复制 | Redo 与日志 | WAL 与 Checkpoint | MySQL 独有：GTID、semi-sync、binlog_format、多源复制；Oracle 用 Redo Log + 归档；PG 用 WAL + Replication Slot |
| 等待与延迟 | 等待与延迟 | 等待与延迟 | MySQL 用 performance_schema wait 体系；Oracle 用 V$EVENT；PG 用 pg_stat_activity.wait_event |
| 连接与会话 | 连接与会话 | 连接与会话 | MySQL 线程模型（非 Oracle 进程/PG 进程）；ProxySQL 对应 PgBouncer |
| 配置与参数 | 配置与参数 | 配置与参数 | MySQL 独有：innodb_flush_log_at_trx_commit、lower_case_table_names、sql_mode |
| 系统与运维 | 系统与运维 | 系统与运维 | MySQL 独有：pt-osc/gh-ost、Event Scheduler、级联外键、数据字典锁 |

### 环境约束

- **测试服务器**: 单实例 MySQL 8.0+
- **不含**: Group Replication、InnoDB Cluster、MySQL Router 等 HA 场景
- **IO 模拟**: 通过 cgroup v2 限速（root 权限可用）
- **所有场景可安全还原**: 每个模拟后可回退到正常状态

### 按触发策略覆盖

| 策略 | 涉及场景数 |
|------|-----------|
| T1 3σ阈值 | 60 |
| T2 硬顶 | 8 |
| T3 趋势 | 6 |
| T4 加速度 | 8 |
| T5 复合 | 2 |
| T6 容量 | 12 |
| T7 偏移 | 4 |
| T8 回归 | 6 |
| T9 缺失 | 4 |
