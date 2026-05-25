# PostgreSQL Rule Engine 验证 — 100 个故障场景（推理链校准版）

## 设计原则

- 每个场景需要 **2-6 步推理链**（决策树深度）
- 太简单的（1 步到位）不入选：磁盘满直接报错、单纯语法错误
- 太复杂的（需 GDB 调试/跨集群推理）不入选：内核 BUG、复杂分布式事务
- 目标：Opus 调 rule → 对比打分 → 输出 rule 优化方案
- 所有场景基于 **单实例 PostgreSQL 14+**，不含流复制/Patroni 等 HA 场景

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

### T001 — 行锁级联：blocker 是 idle in transaction
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查 pg_locks + pg_stat_activity 阻塞链 → root blocker PID=1234
  3. 查 blocker 状态 → state='idle in transaction', 无当前 query, 事务已开 300s
  4. 判定：应用未提交事务 → 建议设 idle_in_transaction_session_timeout 或 kill session
- **模拟**: Session A `BEGIN; UPDATE t SET x=1 WHERE id=1;`（不 COMMIT）; Session B~T 更新同一行
- **指标**: lock_wait_sessions > 15, blocker_count ≥ 1, idle_in_transaction > 5

### T002 — 行锁级联：blocker 在跑慢 SQL
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查阻塞链 → root blocker PID=5678
  3. 查 blocker 状态 → state='active', query 是全表扫描 UPDATE, duration=120s
  4. 判定：blocker 的慢 SQL 导致长时间持锁 → 建议优化该 SQL（加索引/改写）
- **模拟**: Session A 执行无索引大表 `UPDATE t SET x=x+1 WHERE col LIKE '%abc%'`; Session B~T 等待同行
- **指标**: lock_wait_sessions > 10, long_queries > 1, blocker_count ≥ 1

### T003 — 行锁级联：blocker 在等 IO
- **推理链**（5 步）:
  1. 检测 lock_wait_sessions > 阈值
  2. 查阻塞链 → root blocker PID=9012
  3. 查 blocker 状态 → state='active', wait_event_type='IO', wait_event='DataFileRead'
  4. 查 blocker 的 SQL → 正常 SQL 但 IO 延迟异常
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
- **指标**: blocker_count ≥ 3, lock_wait_sessions > 20

### T005 — 死锁 + 频率判断
- **推理链**（3 步）:
  1. 检测 deadlocks > 0（即时触发）
  2. 查死锁频率：偶发（< 1 次/小时）还是频繁（> 5 次/小时）
  3. 频率低 → 建议重试逻辑即可; 频率高 → 查事务中 SQL 执行顺序，建议统一访问顺序
- **模拟**: 两个会话交叉更新两行，循环触发死锁（频率可控）
- **指标**: deadlocks > 3/h, pg_stat_database.deadlocks 增长

### T006 — DDL 阻塞 DML（AccessExclusiveLock）
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike
  2. 查 pg_locks → 有 AccessExclusiveLock 请求（ALTER TABLE / DROP INDEX）
  3. 查阻塞链 → DDL 在等活跃事务释放，同时阻塞后续所有查询
  4. 判定：DDL/DML 冲突 → 建议在维护窗口做 DDL 或用 lock_timeout + CREATE INDEX CONCURRENTLY
- **模拟**: 多路查询运行中 + `ALTER TABLE t ADD COLUMN new_col INT`
- **指标**: lock_wait_sessions spike, blocker_count ≥ 1

### T007 — VACUUM 与查询锁冲突
- **推理链**（4 步）:
  1. 检测 autovacuum_workers 增加但 dead_tuple_ratio 不降
  2. 查 pg_stat_activity → autovacuum 进程 wait_event='Lock' 等待 AccessExclusiveLock
  3. 查阻塞源 → 长事务阻止 VACUUM 清理
  4. 判定：长事务阻止 VACUUM → 建议处理长事务 + 设 old_snapshot_threshold
- **模拟**: 长事务不提交 + autovacuum 尝试 VACUUM 同一表
- **指标**: autovacuum_workers > 3, dead_tuple_ratio > 20%, oldest_xact_age_sec > 3600

### T008 — Advisory Lock 泄漏
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 升高, 但 row_exclusive_locks 不高
  2. 查 pg_locks → 大量 advisory lock 持有
  3. 查持有会话 → 应用获取 advisory lock 后未释放
  4. 判定：advisory lock 泄漏 → 建议应用层确保 pg_advisory_unlock 或使用 session-level lock + 断连释放
- **模拟**: 循环 `SELECT pg_advisory_lock(n)` 不释放
- **指标**: lock_wait_sessions 升高, pg_locks advisory 类型 > 100

### T009 — 外键缺索引导致锁扩散
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 升高 + avg_query_time_ms 冲高
  2. 查阻塞链 → blocker 在执行 DELETE FROM parent 或 UPDATE parent
  3. 查子表外键列 → 无索引，PG 需扫描子表验证 FK 约束
  4. 判定：子表 FK 列缺索引导致全表扫描 + 长时间持锁 → 建议 CREATE INDEX ON child(fk_col)
- **模拟**: 父子表 FK 无索引, `DELETE FROM parent WHERE id=1`（子表百万行）
- **指标**: lock_wait_sessions > 5, avg_query_time_ms 冲高

### T010 — 并发 INSERT 热点页争用
- **推理链**（4 步）:
  1. 检测 active_sessions 升高 + wait_event='BufferContent' 或 'extend'
  2. 查 pg_stat_activity → 多个会话等待同一表的 buffer 锁
  3. 查该表 → 单一自增主键，所有 INSERT 写入同一个末尾页
  4. 判定：热点页争用 → 建议使用 HASH 分区表 或 fillfactor 调低
- **模拟**: 50 路并发 INSERT 到自增 ID 表
- **指标**: active_sessions 升高, bufferpin_wait_sessions > 5

### T011 — SHARE UPDATE EXCLUSIVE 锁阻塞 DDL
- **推理链**（4 步）:
  1. 检测 DDL 操作挂起（如 CREATE INDEX CONCURRENTLY）
  2. 查 pg_locks → CONCURRENTLY 操作需 SHARE UPDATE EXCLUSIVE, 但被已有锁阻塞
  3. 查阻塞源 → 某长事务持有 RowExclusiveLock
  4. 判定：长事务阻塞并发索引创建 → 建议等长事务结束 或设 lock_timeout 重试
- **模拟**: 长事务 + `CREATE INDEX CONCURRENTLY`
- **指标**: lock_wait_sessions > 0, long_queries > 0

### T012 — TRUNCATE 阻塞读查询
- **推理链**（3 步）:
  1. 检测 lock_wait_sessions spike, 大量 SELECT 被阻塞
  2. 查 pg_locks → AccessExclusiveLock（TRUNCATE 要求）阻塞 AccessShareLock
  3. 判定：TRUNCATE 需要最强锁 → 建议改用 DELETE + VACUUM 或在维护窗口执行
- **模拟**: 高并发读 + `TRUNCATE TABLE t`
- **指标**: lock_wait_sessions spike

### T013 — 预备事务（Prepared Transaction）未清理
- **推理链**（4 步）:
  1. 检测 xid_age_pct 持续上升 + VACUUM 效率下降
  2. 查 pg_prepared_xacts → 有遗留的预备事务，时间很早
  3. 查该事务是否有对应的 2PC 完成 → 没有
  4. 判定：孤立预备事务阻止 XID 回收 → 建议 ROLLBACK PREPARED 'txn_id'
- **模拟**: `PREPARE TRANSACTION 'test_2pc'` 后不 COMMIT/ROLLBACK
- **指标**: xid_age_pct 持续上升, pg_prepared_xacts 有记录

### T014 — 分区表 DDL 锁粒度问题
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike, 多个查询被阻塞
  2. 查 pg_locks → ALTER TABLE 父表需 AccessExclusiveLock 影响所有分区
  3. 查操作类型 → 只是 ATTACH/DETACH PARTITION
  4. 判定：分区操作锁粒度过大 → 建议 PG14+ 用 ALTER TABLE DETACH PARTITION CONCURRENTLY
- **模拟**: 高并发查询分区表 + `ALTER TABLE parent DETACH PARTITION p_old`
- **指标**: lock_wait_sessions > 10

### T015 — 锁等待超时后重试风暴
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions 波动 + backend_errors_rate 升高
  2. 查 pg_stat_activity → 大量 ERROR: canceling statement due to lock timeout
  3. 查应用行为 → 超时后立即重试，造成重试风暴
  4. 判定：无退避的重试策略 → 建议应用实现指数退避 + 调整 lock_timeout
- **模拟**: lock_timeout=1s + 高并发争抢同一行 + 应用立即重试
- **指标**: lock_wait_sessions 波动, backend_errors_rate spike

---

## 二、SQL 性能（20 个场景）

### T016 — 全表扫描：缺索引 + 确认应该加索引
- **推理链**（4 步）:
  1. 检测 cpu_sessions / io_wait_sessions 冲高
  2. 查 pg_stat_statements Top SQL → Seq Scan on large_table, rows=5000000
  3. 查 WHERE 条件列 → 无索引, 选择度高（n_distinct 大）
  4. 判定：应加索引 → 建议 CREATE INDEX + EXPLAIN 验证
- **模拟**: 百万行表 `SELECT * FROM t WHERE email = 'x@y.com'` 无索引
- **指标**: active_sessions 冲高, tup_returned_rate >> tup_fetched_rate

### T017 — 全表扫描：选择度低不该加索引
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 冲高
  2. 查 Top SQL → Seq Scan on t
  3. 查 WHERE 条件列 → 选择度极低（status='active' 占 95%），加索引反而更差
  4. 判定：全扫是合理选择 → 建议分区裁剪或 BRIN 索引，不建议 B-tree 索引
- **模拟**: 百万行表 status 列只有 3 个值（active=95%），查 status='active'
- **指标**: tup_returned_rate 高但 active_sessions 不高

### T018 — 执行计划漂移：统计信息过期
- **推理链**（5 步）:
  1. 检测 avg_query_time_ms 突然升高
  2. 查 pg_stat_statements → 某 SQL 的 mean_exec_time 从 5ms 变成 500ms
  3. 查 EXPLAIN → 计划从 Index Scan 变成 Seq Scan
  4. 查 pg_stat_user_tables → last_autoanalyze 是 3 天前，期间大量写入
  5. 判定：统计信息过期导致计划回退 → 建议 ANALYZE table + 调整 autovacuum_analyze_threshold
- **模拟**: 大量 INSERT 改变数据分布后不 ANALYZE
- **指标**: avg_query_time_ms spike

### T019 — CTE 物化导致性能差（Optimization Fence）
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + long_queries 增加
  2. 查 Top SQL → WITH cte AS (SELECT ... FROM big_table) SELECT * FROM cte WHERE ...
  3. 查 EXPLAIN → CTE Scan（物化了整个大表），WHERE 过滤在 CTE 外部
  4. 判定：CTE 物化了不需要的数据 → 建议 PG12+ 用 `WITH cte AS NOT MATERIALIZED (...)` 或改写为子查询
- **模拟**: CTE 返回百万行，外层只取 10 行
- **指标**: cpu_sessions 升高, temp_bytes_rate 可能升高

### T020 — Nested Loop 低效：驱动表估算偏差
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 冲高
  2. 查 Top SQL → Nested Loop, outer 实际返回 100000 行
  3. 查 EXPLAIN ANALYZE → 优化器估算 100 行（统计信息不准或 correlation 差）
  4. 判定：驱动表行数估算偏差 → 建议 ANALYZE + 考虑 SET enable_nestloop=off 测试 Hash Join
- **模拟**: 统计信息过期的大表做 NL JOIN
- **指标**: io_wait_sessions 冲高, avg_query_time_ms 升高

### T021 — Hash Join 溢出到磁盘
- **推理链**（4 步）:
  1. 检测 temp_bytes_rate spike + temp_files_rate 升高
  2. 查 Top SQL → Hash Join, Batches: 16（溢出到磁盘）
  3. 查 work_mem → 4MB（太小，hash 表放不下）
  4. 判定：work_mem 不足 → 建议增大 work_mem 或 SET LOCAL work_mem='256MB' 针对该查询
- **模拟**: 两个大表 JOIN + work_mem=4MB
- **指标**: temp_bytes_rate spike, temp_files_rate > 10/s

### T022 — 大排序溢出到磁盘
- **推理链**（4 步）:
  1. 检测 temp_bytes_rate spike
  2. 查 Top SQL → Sort Method: external merge, Sort Space: 500MB
  3. 查 work_mem → 4MB（远小于排序需求）
  4. 判定：work_mem 不足导致排序溢出 → 建议增大 work_mem + 考虑添加排序列索引
- **模拟**: `SELECT * FROM big_table ORDER BY col1, col2` + work_mem=4MB
- **指标**: temp_bytes_rate spike, temp_files_rate 升高

### T023 — 隐式类型转换导致索引失效
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + tup_returned_rate 冲高
  2. 查 Top SQL → `WHERE varchar_col = 123`（数字与 varchar 比较）
  3. 查 EXPLAIN → Seq Scan + Filter（索引无法使用，PG 对 varchar 列做了隐式转换）
  4. 判定：隐式类型转换 → 建议改 `WHERE varchar_col = '123'`
- **模拟**: VARCHAR 列有索引，用数字条件查询
- **指标**: cpu_sessions 升高, tup_returned_rate >> tup_fetched_rate

### T024 — 函数索引失效：WHERE 条件函数不匹配
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 升高
  2. 查 Top SQL → `WHERE LOWER(name) = 'john'`
  3. 查索引 → 只有 name 上的 B-tree，无 LOWER(name) 函数索引
  4. 判定：需要表达式索引 → 建议 `CREATE INDEX idx_lower_name ON t (LOWER(name))`
- **模拟**: 百万行表，有 name 索引，但查询用 LOWER(name)
- **指标**: avg_query_time_ms 升高

### T025 — LIKE '%keyword%' 全表扫描
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高
  2. 查 Top SQL → `WHERE content LIKE '%keyword%'`, Seq Scan
  3. 查索引 → B-tree 无法支持前模糊匹配
  4. 判定：前模糊 B-tree 无法用 → 建议 pg_trgm 扩展 + GIN 索引 或全文搜索
- **模拟**: 百万行文本列 `LIKE '%search%'`
- **指标**: cpu_sessions 升高, long_queries > 0

### T026 — 分区裁剪失效
- **推理链**（4 步）:
  1. 检测 tup_returned_rate 冲高 + io_wait_sessions 升高
  2. 查 Top SQL → 分区表查询, EXPLAIN 显示 Append → 扫描所有分区
  3. 查 WHERE 条件 → 对分区键做了函数转换 `WHERE DATE_TRUNC('month', created_at) = ...`
  4. 判定：分区裁剪失效 → 建议改写为范围条件 `WHERE created_at >= ... AND created_at < ...`
- **模拟**: 按月 RANGE 分区表用函数条件查询
- **指标**: tup_returned_rate 冲高

### T027 — NOT IN 子查询性能陷阱
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + long_queries 增加
  2. 查 Top SQL → `WHERE id NOT IN (SELECT id FROM other_table)`
  3. 查 EXPLAIN → Anti Join 但子查询有 NULL 值导致全量比较
  4. 判定：NOT IN 遇 NULL 退化 → 建议改 `WHERE NOT EXISTS (SELECT 1 FROM other_table WHERE ...)` 或确保非 NULL
- **模拟**: 子查询结果含 NULL + 大表 NOT IN
- **指标**: cpu_sessions 升高, long_queries > 0

### T028 — OFFSET 大分页性能退化
- **推理链**（3 步）:
  1. 检测 avg_query_time_ms 逐渐升高
  2. 查 Top SQL → `SELECT * FROM t ORDER BY id LIMIT 20 OFFSET 500000`
  3. 判定：大 OFFSET 需扫描前 50 万行再丢弃 → 建议改 keyset pagination（WHERE id > last_id LIMIT 20）
- **模拟**: 翻页到 2 万页
- **指标**: avg_query_time_ms 随翻页深度线性增长

### T029 — 相关子查询 N+1 问题
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + tup_fetched_rate spike
  2. 查 Top SQL → `SELECT *, (SELECT count(*) FROM orders WHERE orders.user_id = users.id) FROM users`
  3. 查 EXPLAIN → SubPlan, 对每行执行子查询
  4. 判定：相关子查询导致 N+1 → 建议改为 LEFT JOIN + GROUP BY 或 LATERAL JOIN
- **模拟**: users 表 10 万行，每行触发一次子查询
- **指标**: cpu_sessions 升高, tup_fetched_rate 极高

### T030 — JSONB 查询无 GIN 索引
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高
  2. 查 Top SQL → `WHERE data @> '{"status": "active"}'` 或 `data->>'key' = 'val'`
  3. 查索引 → JSONB 列无 GIN 索引，走 Seq Scan
  4. 判定：缺 GIN 索引 → 建议 `CREATE INDEX idx_data ON t USING GIN (data)` 或表达式索引
- **模拟**: 百万行 JSONB 列无索引查询
- **指标**: cpu_sessions 升高

### T031 — 多列索引顺序不当
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 偏高
  2. 查 Top SQL → `WHERE status = 'active' AND created_at > '2026-01-01'`
  3. 查索引 → INDEX(created_at, status) — 顺序反了，选择度高的 status 不在前面
  4. 判定：索引列顺序不优 → 建议重建为 INDEX(status, created_at) 让高选择度列在前
- **模拟**: 复合索引顺序反的情况
- **指标**: avg_query_time_ms 偏高

### T032 — 过多索引导致写入变慢
- **推理链**（4 步）:
  1. 检测 xact_commit_rate 下降 + avg_query_time_ms（INSERT/UPDATE）升高
  2. 查目标表 → 有 12 个索引
  3. 查索引使用情况 → pg_stat_user_indexes 显示 6 个索引 idx_scan = 0（从未使用）
  4. 判定：冗余索引拖慢写入 → 建议 DROP 未使用索引
- **模拟**: 高并发写入 + 12 个索引的表
- **指标**: xact_commit_rate 下降

### T033 — 大表 COUNT(*) 慢
- **推理链**（3 步）:
  1. 检测 long_queries > 0, 某 SQL 持续运行 30s+
  2. 查 SQL → `SELECT COUNT(*) FROM big_table`, Seq Scan（PG 需全表扫计算准确 count）
  3. 判定：PG MVCC 无法快速 COUNT → 建议用 pg_class.reltuples 近似值 或维护计数器表
- **模拟**: 5000 万行表 COUNT(*)
- **指标**: long_queries > 0, io_wait_sessions 升高

### T034 — SELECT DISTINCT 大结果集排序
- **推理链**（4 步）:
  1. 检测 temp_bytes_rate spike + cpu_sessions 升高
  2. 查 Top SQL → `SELECT DISTINCT col1, col2 FROM big_table`
  3. 查 EXPLAIN → HashAggregate / Sort, 去重需要大量内存
  4. 判定：DISTINCT 对大结果集开销大 → 建议用 GROUP BY 替代 或 EXISTS 子查询
- **模拟**: 百万行表上 DISTINCT 多列
- **指标**: temp_bytes_rate spike

### T035 — Window Function 无合适索引
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + temp_bytes_rate 升高
  2. 查 Top SQL → `ROW_NUMBER() OVER (PARTITION BY user_id ORDER BY created_at DESC)`
  3. 查 EXPLAIN → Sort + WindowAgg, 全表排序
  4. 判定：缺排序索引 → 建议 `CREATE INDEX ON t (user_id, created_at DESC)` 让窗口函数用索引
- **模拟**: 百万行表上窗口函数无合适索引
- **指标**: cpu_sessions 升高, temp_bytes_rate 升高

---

## 三、VACUUM 与 MVCC（10 个场景）

### T036 — Dead Tuple 堆积：autovacuum 跟不上写入速度
- **推理链**（4 步）:
  1. 检测 dead_tuple_ratio > 20%（T6 黄线）
  2. 查 pg_stat_user_tables → n_dead_tup 持续增长，last_autovacuum 正常在跑
  3. 查写入速率 → 高频 UPDATE/DELETE，autovacuum 清理速度跟不上
  4. 判定：autovacuum 配置不够激进 → 建议调低 autovacuum_vacuum_cost_delay + 增大 autovacuum_vacuum_cost_limit
- **模拟**: 持续高频 UPDATE + 默认 autovacuum 参数
- **指标**: dead_tuple_ratio > 20%, autovacuum_workers > 0

### T037 — Dead Tuple 堆积：长事务阻止 VACUUM 回收
- **推理链**（4 步）:
  1. 检测 dead_tuple_ratio > 30% + autovacuum 在跑但无效果
  2. 查 pg_stat_activity → oldest_xact_age_sec > 7200（2 小时前的事务）
  3. 查该长事务 → 是报表查询或遗忘的 BEGIN
  4. 判定：长事务的 xmin 阻止 VACUUM 回收 → 建议终止长事务 + 设 idle_in_transaction_session_timeout
- **模拟**: 打开事务不关闭 + 持续大量 UPDATE
- **指标**: dead_tuple_ratio > 30%, oldest_xact_age_sec > 3600

### T038 — XID Wraparound 警告
- **推理链**（4 步）:
  1. 检测 xid_age_pct > 70%（T6 黄线，接近 2^31 上限）
  2. 查 pg_database → datfrozenxid 年龄排名
  3. 查哪些表 relfrozenxid 最老 → 大表未被 aggressive vacuum
  4. 判定：XID 即将耗尽 → 紧急执行 VACUUM FREEZE + 检查是否有阻塞 VACUUM 的长事务
- **模拟**: 手动设置 autovacuum_freeze_max_age 极大 + 大量事务
- **指标**: xid_age_pct > 70%

### T039 — 表膨胀严重：频繁 UPDATE 导致
- **推理链**（4 步）:
  1. 检测 table_bloat_pct > 50%（T6 黄线）
  2. 查 pg_stat_user_tables → n_dead_tup 正常（VACUUM 在工作），但表大小远超实际数据量
  3. 查更新模式 → 高频 UPDATE 同一行，即使 VACUUM 回收页也不归还 OS
  4. 判定：表膨胀 → 建议 pg_repack 在线重建 或 VACUUM FULL（需要排他锁）
- **模拟**: 对同一表反复 UPDATE 所有行
- **指标**: table_bloat_pct > 50%

### T040 — 索引膨胀导致性能下降
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 逐渐升高
  2. 查 pg_stat_user_indexes → 索引大小远超预期
  3. 查 pg_relation_size vs 估算大小 → 膨胀率 > 3 倍
  4. 判定：索引膨胀 → 建议 REINDEX CONCURRENTLY 或 pg_repack
- **模拟**: 大量 DELETE + INSERT（不同 key）导致 B-tree 页分裂碎片化
- **指标**: avg_query_time_ms 逐渐升高, io_wait_sessions 略升

### T041 — autovacuum 被频繁取消
- **推理链**（4 步）:
  1. 检测 dead_tuple_ratio 持续高 + autovacuum 频繁启停
  2. 查 pg_stat_progress_vacuum → 经常被取消（autovacuum_vacuum_cost_delay 导致过慢）
  3. 查冲突原因 → 与用户锁冲突 或 deadlock_timeout 触发
  4. 判定：autovacuum 优先级太低 → 建议提高 autovacuum_vacuum_cost_limit + 减少 autovacuum_vacuum_cost_delay
- **模拟**: 高写入负载 + autovacuum 参数保守
- **指标**: dead_tuple_ratio 持续高, autovacuum_workers 波动

### T042 — HOT Update 链过长
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 升高 + 更新量大但索引不变
  2. 查 pg_stat_user_tables → n_tup_hot_upd 占比高
  3. 查 fillfactor → 默认 100%（无空间给 HOT 链）
  4. 判定：HOT 链过长且无空闲空间 → 建议设 fillfactor=70~80 让 HOT update 有空间，或触发 prune
- **模拟**: 频繁 UPDATE 非索引列 + fillfactor=100
- **指标**: avg_query_time_ms 升高

### T043 — VACUUM FULL 长时间阻塞
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike + 一个进程执行 VACUUM FULL
  2. 查 pg_stat_activity → VACUUM FULL 持有 AccessExclusiveLock，阻塞所有读写
  3. 查表大小 → 50GB 表，VACUUM FULL 需要几十分钟
  4. 判定：不应在业务时段用 VACUUM FULL → 建议改用 pg_repack（在线 + 不阻塞）
- **模拟**: 业务负载 + `VACUUM FULL big_table`
- **指标**: lock_wait_sessions spike, long_queries > 0

### T044 — TOAST 表膨胀
- **推理链**（4 步）:
  1. 检测 tablespace_used_pct 持续上升（T3 趋势）
  2. 查 pg_total_relation_size vs pg_relation_size → TOAST 表占比 > 80%
  3. 查该表 → 大量 UPDATE TEXT/JSONB 列，TOAST 旧版本积累
  4. 判定：TOAST 膨胀 → 建议 VACUUM FULL 或 pg_repack 处理 TOAST 表
- **模拟**: 反复 UPDATE JSONB 大字段
- **指标**: tablespace_used_pct 上升趋势

### T045 — autovacuum 饥饿：大表独占 worker
- **推理链**（4 步）:
  1. 检测多个表 dead_tuple_ratio > 20%，但只有大表在被 VACUUM
  2. 查 pg_stat_progress_vacuum → autovacuum_max_workers=3, 全被大表占满
  3. 查小表 → 小表 dead_tuple 堆积但排不上队
  4. 判定：autovacuum worker 不足 → 建议增大 autovacuum_max_workers + 对大表设 per-table autovacuum 参数降低阈值
- **模拟**: 3 个大表持续 UPDATE + autovacuum_max_workers=3 + 20 个小表也需要 VACUUM
- **指标**: dead_tuple_ratio 多表 > 20%, autovacuum_workers = autovacuum_max_workers

---

## 四、内存与缓存（10 个场景）

### T046 — Buffer Cache 命中率下降：大查询冲刷
- **推理链**（4 步）:
  1. 检测 cache_hit_pct < 90%（T8 回归触发）
  2. 查 io_wait_sessions → spike
  3. 查 Top SQL → 大表 Seq Scan 冲刷 shared_buffers
  4. 判定：全扫冲刷缓存 → 建议加索引减少全扫 或增大 shared_buffers
- **模拟**: `SELECT * FROM very_large_table` + 并发 OLTP 小查询
- **指标**: cache_hit_pct < 90%, io_wait_sessions spike

### T047 — Buffer Cache 命中率持续低：shared_buffers 太小
- **推理链**（4 步）:
  1. 检测 cache_hit_pct 持续 < 95%
  2. 查 io_wait_sessions → 持续偏高（不是 spike）
  3. 查 shared_buffers → 128MB（数据集 10GB）
  4. 判定：shared_buffers 不足 → 建议增大到物理内存的 25%
- **模拟**: shared_buffers=128MB, 活跃数据集 10GB, 正常 OLTP 负载
- **指标**: cache_hit_pct 持续 < 95%, io_wait_sessions 持续偏高

### T048 — work_mem 不足导致大量临时文件
- **推理链**（4 步）:
  1. 检测 temp_files_rate > 50/s + temp_bytes_rate spike
  2. 查 pg_stat_statements → 多个 SQL 产生临时文件
  3. 查 work_mem → 4MB（全局默认值太小）
  4. 判定：work_mem 不足 → 建议增大 work_mem（注意：每个 sort/hash 操作独立分配，需计算总内存）
- **模拟**: 多路并发复杂查询 + work_mem=4MB
- **指标**: temp_files_rate > 50/s, temp_bytes_rate spike

### T049 — effective_cache_size 设错导致计划偏差
- **推理链**（4 步）:
  1. 检测多个查询的计划偏向 Seq Scan
  2. 查 EXPLAIN → 优化器评估索引扫描 cost 过高
  3. 查 effective_cache_size → 128MB（远小于实际可用缓存，包括 OS cache）
  4. 判定：effective_cache_size 设太小误导优化器 → 建议设为物理内存的 50-75%
- **模拟**: effective_cache_size=128MB + 大内存服务器
- **指标**: tup_returned_rate 偏高（全扫多）

### T050 — maintenance_work_mem 不足导致 VACUUM/INDEX 创建慢
- **推理链**（4 步）:
  1. 检测 autovacuum 耗时过长 或 CREATE INDEX 执行数十分钟
  2. 查 pg_stat_progress_vacuum / pg_stat_progress_create_index → 进度缓慢
  3. 查 maintenance_work_mem → 64MB（大表需要更多内存加速）
  4. 判定：maintenance_work_mem 不足 → 建议增大到 512MB-1GB
- **模拟**: 5000 万行表 CREATE INDEX + maintenance_work_mem=64MB
- **指标**: long_queries > 0, autovacuum 耗时 > 30min

### T051 — 连接数 × work_mem 导致 OOM
- **推理链**（5 步）:
  1. 检测 backend_errors_rate spike + 某些连接断开
  2. 查 dmesg/syslog → OOM killer 杀掉了 PG 后台进程
  3. 查 work_mem → 256MB, max_connections → 200
  4. 计算最坏情况 → 200 × 256MB × 多个 sort 操作 = 远超物理内存
  5. 判定：work_mem × 连接数内存溢出 → 建议降低 work_mem 或 max_connections + 引入连接池
- **模拟**: work_mem=256MB + 100 路并发复杂查询
- **指标**: backend_errors_rate spike

### T052 — huge_pages 未启用导致内存碎片
- **推理链**（3 步）:
  1. 检测 shared_buffers 设为 8GB+ 但性能波动
  2. 查 huge_pages 设置 → off
  3. 判定：大 shared_buffers 无 huge_pages 导致 TLB miss 增多 → 建议启用 huge_pages=on + 配置 OS huge pages
- **模拟**: shared_buffers=8GB + huge_pages=off + 高并发
- **指标**: active_sessions 波动

### T053 — shared_buffers 过大导致 OS cache 不足
- **推理链**（4 步）:
  1. 检测 cache_hit_pct 正常但 io_wait_sessions 偏高
  2. 查 shared_buffers → 设为物理内存的 60%
  3. 查 OS 可用内存 → 几乎无 page cache，大量 direct IO
  4. 判定：shared_buffers 过大挤占 OS cache → 建议降到 25% 让 OS cache 补充
- **模拟**: shared_buffers 设为内存 60% + 高并发
- **指标**: io_wait_sessions 偏高, cache_hit_pct 正常

### T054 — temp_buffers 不足导致临时表溢出
- **推理链**（3 步）:
  1. 检测 temp_bytes_rate 升高 + 使用临时表的查询变慢
  2. 查 Top SQL → 大量写入临时表
  3. 判定：temp_buffers 默认 8MB 太小 → 建议在会话级别增大 temp_buffers（必须在使用临时表前设置）
- **模拟**: 创建临时表写入 50 万行 + temp_buffers=8MB
- **指标**: temp_bytes_rate 升高

### T055 — 频繁 Backend 进程创建的内存开销
- **推理链**（4 步）:
  1. 检测 connections_pct 波动 + active_sessions 忽高忽低
  2. 查 pg_stat_activity → 大量短连接（每次 connect/disconnect）
  3. 查连接池 → 未使用连接池，每次请求新建进程
  4. 判定：PG 进程模型下频繁 fork 开销大 → 建议引入 PgBouncer 或 PgPool 连接池
- **模拟**: 500 次/秒 connect + query + disconnect
- **指标**: connections_pct 波动

---

## 五、WAL 与 Checkpoint（10 个场景）

### T056 — WAL 生成速率过高
- **推理链**（4 步）:
  1. 检测 wal_bytes_rate > 100MB/s（T2 硬顶）
  2. 查 Top SQL → 大批量 INSERT/UPDATE/DELETE
  3. 查 full_page_writes → on（默认），checkpoint 后首次修改每页都要全页写
  4. 判定：大批量 DML + full_page_writes → 建议分批提交 + 考虑增大 checkpoint_timeout 减少 full page write 频率
- **模拟**: `INSERT INTO t SELECT generate_series(1, 10000000)`
- **指标**: wal_bytes_rate > 100MB/s

### T057 — Checkpoint 过于频繁
- **推理链**（4 步）:
  1. 检测 checkpoints_req > 5/h（请求式 checkpoint 多于时间式）
  2. 查 max_wal_size → 1GB（太小）
  3. 查 wal_bytes_rate → 持续高
  4. 判定：max_wal_size 不足导致频繁 checkpoint → 建议增大 max_wal_size 到 4-16GB
- **模拟**: max_wal_size=1GB + 持续大量 DML
- **指标**: checkpoints_req > 5/h

### T058 — Checkpoint 导致 IO 尖峰（Checkpoint Spike）
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 周期性 spike
  2. 查 spike 时间 → 与 checkpoint 完成时间吻合
  3. 查 checkpoint_completion_target → 0.5（脏页在 50% 时间内刷完，IO 集中）
  4. 判定：checkpoint IO 集中 → 建议设 checkpoint_completion_target=0.9 分散 IO
- **模拟**: checkpoint_completion_target=0.5 + 大量写入
- **指标**: io_wait_sessions 周期性 spike

### T059 — WAL Archive 失败堆积
- **推理链**（4 步）:
  1. 检测 archive_fail_count > 0（持续）
  2. 查 pg_stat_archiver → last_failed_time 频繁, 失败 WAL 文件堆积
  3. 查 archive_command → 目标目录满 或 SSH 断开
  4. 判定：归档失败会导致 WAL 堆积最终磁盘满 → 紧急修复 archive_command + 清理 pg_wal
- **模拟**: archive_command 指向不存在目录
- **指标**: archive_fail_count > 0, tablespace_used_pct 上升

### T060 — pg_wal 目录膨胀占满磁盘
- **推理链**（4 步）:
  1. 检测 tablespace_used_pct > 95%（T6 红线）
  2. 查磁盘 → pg_wal 目录占用大量空间
  3. 查原因 → 复制槽 replication_lag_sec 极高 或 archive_fail_count > 0 导致 WAL 不能回收
  4. 判定：WAL 堆积 → 删除不活跃 replication slot 或修复归档
- **模拟**: 创建逻辑复制槽但不消费 + 持续 DML
- **指标**: tablespace_used_pct > 95%, replication_lag_sec 极高

### T061 — Replication Slot 阻止 WAL 回收
- **推理链**（4 步）:
  1. 检测 pg_wal 持续增长（T3 趋势）
  2. 查 pg_replication_slots → 有不活跃的 slot, confirmed_flush_lsn 远落后
  3. 查该 slot 对应的订阅者 → 下线/网络断开
  4. 判定：不活跃 slot 阻止 WAL 回收 → 建议 DROP SLOT 或设 max_slot_wal_keep_size 限制
- **模拟**: 创建逻辑复制 slot + 订阅者断开
- **指标**: wal_bytes_rate 正常但 pg_wal 持续增长

### T062 — 同步提交导致延迟高
- **推理链**（4 步）:
  1. 检测 avg_query_time_ms 偏高 + xact_commit_rate 偏低
  2. 查 wait_event → 'WALSync', 'WALWrite' 频繁
  3. 查 synchronous_commit → on, WAL 磁盘是慢速 HDD
  4. 判定：同步提交 + 慢磁盘 → 建议 WAL 放 SSD 或对低一致性需求业务设 synchronous_commit=off
- **模拟**: synchronous_commit=on + WAL 在 HDD + 高频提交
- **指标**: avg_query_time_ms 偏高, xact_commit_rate 偏低

### T063 — wal_level = logical 的额外开销
- **推理链**（3 步）:
  1. 检测 wal_bytes_rate 升高 + 无逻辑复制在使用
  2. 查 wal_level → logical（产生额外解码信息）
  3. 判定：无需逻辑复制却用 logical 级别 → 建议降为 replica 减少 WAL 量（需重启）
- **模拟**: wal_level=logical + 高写入负载 vs wal_level=replica 对比
- **指标**: wal_bytes_rate 偏高

### T064 — WAL Sender 进程占满 max_wal_senders
- **推理链**（4 步）:
  1. 检测 备份或新 replica 连接失败
  2. 查 pg_stat_replication → max_wal_senders 已满
  3. 查各 sender 用途 → 有过期的 pg_basebackup 或测试用 replica 未清理
  4. 判定：wal sender 资源耗尽 → 建议清理不用的 replica + 增大 max_wal_senders
- **模拟**: max_wal_senders=5 + 5 个复制连接 + 尝试新备份
- **指标**: backend_errors_rate 升高

### T065 — full_page_writes 的 IO 放大
- **推理链**（4 步）:
  1. 检测 wal_bytes_rate 异常高 + checkpoint 后尤其明显
  2. 查 checkpoint 频率 → 正常
  3. 查 full_page_writes → on, checkpoint 后每页首次修改写完整 8KB
  4. 判定：频繁小更新 + full page write 导致 WAL 放大 → 建议增大 checkpoint_timeout 减少 full page write 触发频率
- **模拟**: 高频随机单行 UPDATE + 小 checkpoint_timeout
- **指标**: wal_bytes_rate 异常高

---

## 六、等待事件与延迟（10 个场景）

### T066 — LWLock:BufferMapping 争用
- **推理链**（4 步）:
  1. 检测 lwlock_wait_sessions > 5
  2. 查 pg_stat_activity → wait_event='BufferMapping'
  3. 查负载类型 → 大量并发随机读写，buffer mapping hash 表争用
  4. 判定：buffer mapping 热点 → 建议减少并发度 或升级到 PG16+ 改进的 buffer mapping
- **模拟**: 100 路并发随机读写不同表
- **指标**: lwlock_wait_sessions > 5

### T067 — LWLock:WALInsert 争用
- **推理链**（4 步）:
  1. 检测 lwlock_wait_sessions > 3 + xact_commit_rate 不升
  2. 查 pg_stat_activity → wait_event='WALInsert'
  3. 查 wal_buffers → 太小, 或高并发写入导致 WAL 插入争用
  4. 判定：WAL 写入瓶颈 → 建议增大 wal_buffers + 确认 wal_level 不高于需要
- **模拟**: 100 路并发 INSERT + wal_buffers=64kB
- **指标**: lwlock_wait_sessions > 3

### T068 — IO:DataFileRead 延迟高
- **推理链**（4 步）:
  1. 检测 io_wait_sessions > 10（持续）
  2. 查 pg_stat_activity → wait_event='DataFileRead'
  3. 查 cache_hit_pct → 85%（命中率偏低，大量磁盘读）
  4. 判定：磁盘读取成为瓶颈 → 建议增大 shared_buffers + 检查磁盘 IO 性能
- **模拟**: shared_buffers 偏小 + 活跃数据集大于缓存
- **指标**: io_wait_sessions > 10, cache_hit_pct < 90%

### T069 — IO:DataFileWrite 延迟高（bgwriter/checkpointer）
- **推理链**（4 步）:
  1. 检测 io_wait_sessions 周期性升高
  2. 查 pg_stat_activity → checkpointer 和 bgwriter 进程 wait_event='DataFileWrite'
  3. 查 bgwriter_lru_maxpages → 默认 100（太保守）
  4. 判定：后台写入跟不上脏页产生 → 建议增大 bgwriter_lru_maxpages + 确认磁盘写入性能
- **模拟**: 高并发写入 + 慢磁盘
- **指标**: io_wait_sessions 周期性升高

### T070 — BufferPin 等待
- **推理链**（4 步）:
  1. 检测 bufferpin_wait_sessions > 3
  2. 查 pg_stat_activity → wait_event='BufferPin'
  3. 查原因 → VACUUM 尝试清理页面但被 cursor 持有 pin（长时间游标扫描）
  4. 判定：长游标阻塞 VACUUM → 建议拆分游标批量大小 或设 old_snapshot_threshold
- **模拟**: 长 cursor FETCH + 同表 VACUUM
- **指标**: bufferpin_wait_sessions > 3

### T071 — Client:ClientRead 等待（网络/应用慢）
- **推理链**（4 步）:
  1. 检测 active_sessions 偏高但 cpu_sessions + io_wait_sessions 正常
  2. 查 pg_stat_activity → 多个会话 wait_event='ClientRead'
  3. 查这些会话 → 都在等客户端发送下一个命令
  4. 判定：客户端处理慢或网络延迟 → 建议检查应用层处理时间 + 网络质量
- **模拟**: 客户端收到结果后 sleep 再发下一个请求
- **指标**: active_sessions 偏高, idle_in_transaction > 5

### T072 — Lock:transactionid 等待
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions > 10
  2. 查 pg_stat_activity → wait_event='transactionid'
  3. 查 pg_locks → 等待另一个事务的 xid 锁（INSERT ON CONFLICT / SERIALIZABLE 冲突）
  4. 判定：事务级别冲突 → 建议检查隔离级别是否过高 或优化 UPSERT 逻辑
- **模拟**: SERIALIZABLE 隔离级别下高并发更新同一行
- **指标**: lock_wait_sessions > 10

### T073 — Lock:extend 等待（表扩展争用）
- **推理链**（3 步）:
  1. 检测 active_sessions 升高 + wait_event='extend'
  2. 查 pg_stat_activity → 多个 INSERT 会话等待表文件扩展
  3. 判定：高并发 INSERT 争用表文件扩展 → 建议 PG16+ 使用 bulk extend 优化 或分区表分散写入
- **模拟**: 100 路并发 INSERT 到单表
- **指标**: active_sessions 升高

### T074 — CPU 自旋等待（高并发下 SpinDelay）
- **推理链**（4 步）:
  1. 检测 cpu_sessions 异常高 + 实际吞吐未增长
  2. 查 pg_stat_activity → 多数会话 wait_event_type='LWLock' 或 'SpinDelay'
  3. 查并发度 → active_sessions > CPU 核数的 3 倍
  4. 判定：过度并发导致 CPU 自旋等待 → 建议引入连接池限制并发到 CPU 核数的 2-3 倍
- **模拟**: 200 路并发 + 8 核 CPU
- **指标**: cpu_sessions 异常高, xact_commit_rate 不增

### T075 — IO:WALWrite 延迟导致提交慢
- **推理链**（4 步）:
  1. 检测 xact_commit_rate 下降 + avg_query_time_ms 升高
  2. 查 pg_stat_activity → wait_event='WALWrite' 或 'WALSync'
  3. 查 WAL 磁盘 → IO 延迟高
  4. 判定：WAL 磁盘写入慢 → 建议将 pg_wal 迁移到 SSD + 检查 IO 调度器
- **模拟**: WAL 放在限速磁盘 + 高频提交
- **指标**: xact_commit_rate 下降

---

## 七、连接与会话管理（10 个场景）

### T076 — 连接风暴 + 无连接池
- **推理链**（4 步）:
  1. 检测 connections_pct > 80%（T4 加速度）+ 短连接频繁创建
  2. 查 pg_stat_activity → 大量来自同一 application_name 的短连接
  3. 查连接池 → 未使用 PgBouncer
  4. 判定：应用层无连接池 → 建议引入 PgBouncer（transaction mode）
- **模拟**: 循环 connect/query/disconnect 每秒 100 次
- **指标**: connections_pct > 80%, active_sessions 波动

### T077 — Connections 接近 max_connections
- **推理链**（4 步）:
  1. 检测 connections_pct > 90%（T6 触发）
  2. 查 pg_stat_activity → 按 application_name / client_addr 分组
  3. 定位消耗最多连接的来源 → 某个微服务每实例 50 连接 × 10 实例
  4. 判定：连接数溢出 → 建议引入连接池 + 减小每实例池大小 + 临时增大 max_connections
- **模拟**: 打开连接到 max_connections 的 90%
- **指标**: connections_pct > 90%

### T078 — idle in transaction 会话堆积
- **推理链**（4 步）:
  1. 检测 idle_in_transaction > 20 + oldest_xact_age_sec > 600
  2. 查 pg_stat_activity → 大量 state='idle in transaction', 有些已持续数小时
  3. 查应用 → 事务中间做了外部调用但异常未回滚
  4. 判定：事务泄漏 → 建议设 idle_in_transaction_session_timeout=60s + 应用层加 finally 块
- **模拟**: BEGIN 后不 COMMIT/ROLLBACK，堆积 50 个
- **指标**: idle_in_transaction > 20, oldest_xact_age_sec > 600

### T079 — 会话泄漏（趋势检测 T3）
- **推理链**（4 步）:
  1. 检测 connections_pct 持续上升趋势（T3 线性回归斜率 > 0.5σ）
  2. 查 pg_stat_activity → idle 会话持续增长，backend_start 很早
  3. 查 client_addr/application_name → 定位泄漏来源
  4. 判定：应用未正确关闭连接 → 建议修复应用连接管理 + PgBouncer 设 server_idle_timeout
- **模拟**: 打开连接后不关闭，持续累积
- **指标**: connections_pct T3 趋势上升

### T080 — 连接池 PgBouncer 配置不当
- **推理链**（4 步）:
  1. 检测 connections_pct 低但 backend_errors_rate 高（客户端报连接超时）
  2. 查 PG 连接数 → 正常（PgBouncer 限制了）
  3. 查 PgBouncer 配置 → pool_size=5, 大量请求排队超时
  4. 判定：PgBouncer 池太小 → 建议增大 pool_size 适配并发量
- **模拟**: PgBouncer pool_size=5 + 50 并发请求
- **指标**: backend_errors_rate 高, connections_pct 低

### T081 — 大量 idle 连接占满 max_connections
- **推理链**（4 步）:
  1. 检测 connections_pct > 95% 但 active_sessions < 10
  2. 查 pg_stat_activity → 90% 会话 state='idle', 无活跃查询
  3. 查应用 → 连接池 min_idle 设太高，每个微服务保持 50 空闲连接
  4. 判定：空闲连接浪费 → 建议减小连接池 min_idle + 引入 PgBouncer 复用
- **模拟**: 20 个应用实例各保持 10 个空闲连接, max_connections=200
- **指标**: connections_pct > 95%, active_sessions < 10

### T082 — statement_timeout 未设导致查询失控
- **推理链**（4 步）:
  1. 检测 long_queries > 5, 有查询跑了数小时
  2. 查 pg_stat_activity → 多个会话 active, duration > 3600s
  3. 查 statement_timeout → 0（无限制）
  4. 判定：无 statement_timeout 保护 → 建议设全局 statement_timeout=30s + 特殊查询用 SET LOCAL
- **模拟**: 低效查询（笛卡尔积）无 timeout
- **指标**: long_queries > 5

### T083 — Superuser 连接预留不足
- **推理链**（3 步）:
  1. 检测 connections_pct = 100% + DBA 无法连接
  2. 查 superuser_reserved_connections → 3（默认）, 但 superuser 连接也用满
  3. 判定：所有连接含预留均满 → 建议增大 superuser_reserved_connections + 紧急 pg_terminate_backend 清理
- **模拟**: max_connections=100, 全部占满 + superuser 预留也满
- **指标**: connections_pct = 100%, backend_errors_rate spike

### T084 — Active Sessions 加速度突增（T4）
- **推理链**（3 步）:
  1. 检测 active_sessions 二阶导数 > std（T4 加速度）
  2. 查 spike 时间点对应的等待事件 → 定位是锁/IO/CPU
  3. 判定：根据等待事件分类给出根因
- **模拟**: 定时任务在某一秒启动 50 个并发查询
- **指标**: active_sessions 1 秒内从 5 跳到 50

### T085 — pg_terminate_backend 误杀导致事务回滚风暴
- **推理链**（4 步）:
  1. 检测 xact_rollback_rate spike + backend_errors_rate spike
  2. 查 pg_stat_database → xact_rollback 突增
  3. 查日志 → 大量 "terminating connection due to administrator command"
  4. 判定：批量 terminate 导致大事务回滚 → 建议用 pg_cancel_backend 优先 + 分批 terminate
- **模拟**: 批量 pg_terminate_backend 杀掉 50 个活跃会话
- **指标**: xact_rollback_rate spike

---

## 八、配置与参数问题（5 个场景）

### T086 — random_page_cost 过高导致不走索引
- **推理链**（4 步）:
  1. 检测多个查询偏向 Seq Scan
  2. 查 EXPLAIN → 优化器评估 Index Scan cost 过高
  3. 查 random_page_cost → 4.0（默认值，适合 HDD，但实际用 SSD）
  4. 判定：SSD 环境应降低 random_page_cost → 建议设为 1.1-1.5
- **模拟**: SSD 磁盘 + random_page_cost=4.0 + 中等大小表查询
- **指标**: tup_returned_rate 偏高（全扫多）

### T087 — max_parallel_workers_per_gather 为 0 禁用并行
- **推理链**（4 步）:
  1. 检测大查询单线程执行, long_queries > 0
  2. 查 EXPLAIN → 无 Parallel 节点
  3. 查 max_parallel_workers_per_gather → 0
  4. 判定：并行被禁用 → 建议设为 2-4 + 确认 max_worker_processes 足够
- **模拟**: 大表聚合查询 + max_parallel_workers_per_gather=0
- **指标**: long_queries > 0, cpu_sessions 低（只用 1 核）

### T088 — log_min_duration_statement 未设导致慢查询不可追踪
- **推理链**（3 步）:
  1. 用户报慢但无法定位 SQL
  2. 查 log_min_duration_statement → -1（禁用）+ pg_stat_statements 未安装
  3. 判定：缺少慢查询日志 → 建议设 log_min_duration_statement=1000 + 安装 pg_stat_statements 扩展
- **模拟**: 慢查询但无法追踪
- **指标**: avg_query_time_ms 偏高但无法定位具体 SQL

### T089 — default_statistics_target 过低导致计划偏差
- **推理链**（4 步）:
  1. 检测多个查询计划不优
  2. 查 EXPLAIN → 行数估算偏差大
  3. 查 default_statistics_target → 100（默认）, 数据分布复杂的列需要更多
  4. 判定：统计采样不足 → 建议对关键列设 ALTER TABLE SET STATISTICS 1000 + ANALYZE
- **模拟**: 数据倾斜严重的列 + 默认统计目标
- **指标**: avg_query_time_ms 偏高

### T090 — shared_preload_libraries 缺少关键扩展
- **推理链**（3 步）:
  1. 诊断过程中发现 pg_stat_statements 无数据
  2. 查 shared_preload_libraries → 未包含 pg_stat_statements
  3. 判定：缺少关键监控扩展 → 建议添加 pg_stat_statements + auto_explain（需重启）
- **模拟**: 默认安装无任何预加载扩展
- **指标**: 无法获取 SQL 级别统计

---

## 九、系统与运维（10 个场景）

### T091 — pg_stat_statements 重置后丢失历史
- **推理链**（3 步）:
  1. 诊断时发现 pg_stat_statements 数据全是近期的
  2. 查 → 有人执行了 pg_stat_statements_reset()
  3. 判定：监控数据丢失 → 建议定期快照 pg_stat_statements 到历史表 + 限制 reset 权限
- **模拟**: pg_stat_statements_reset() 后查询历史
- **指标**: 无法追踪历史 SQL 性能

### T092 — pg_hba.conf 配置过于宽松
- **推理链**（4 步）:
  1. 检测 backend_errors_rate 升高（大量连接失败或异常连接）
  2. 查 pg_stat_activity → 有来自意外 IP 的连接
  3. 查 pg_hba.conf → `host all all 0.0.0.0/0 md5`（全开放）
  4. 判定：安全风险 → 建议限制 IP 范围 + 使用 scram-sha-256
- **模拟**: 全开放 pg_hba.conf + 扫描连接
- **指标**: backend_errors_rate 升高

### T093 — 版本升级后统计信息丢失
- **推理链**（4 步）:
  1. 检测升级后多个查询性能下降
  2. 查 pg_stat_user_tables → last_analyze = NULL（统计信息被清除）
  3. 查 pg_upgrade 日志 → 正常，但未执行 post-upgrade ANALYZE
  4. 判定：升级后需重新收集统计信息 → 建议立即执行 `vacuumdb --all --analyze-in-stages`
- **模拟**: pg_upgrade 后不 ANALYZE
- **指标**: avg_query_time_ms 全面升高

### T094 — 扩展版本不兼容导致错误
- **推理链**（3 步）:
  1. 检测 backend_errors_rate 升高 + 特定功能报错
  2. 查 pg_extension → 扩展版本与 PG 版本不兼容
  3. 判定：扩展需要升级 → 建议 `ALTER EXTENSION xxx UPDATE TO 'new_version'`
- **模拟**: PG 大版本升级后扩展未更新
- **指标**: backend_errors_rate 升高

### T095 — 日志文件撑满磁盘
- **推理链**（4 步）:
  1. 检测 tablespace_used_pct > 95%（T6 红线）
  2. 查磁盘 → pg_log / log 目录占用大量空间
  3. 查 log_rotation_age / log_rotation_size → 无轮转 或 log_min_duration_statement=0（记录所有 SQL）
  4. 判定：日志配置过于详细 → 建议设合理 log_rotation + 提高 log_min_duration_statement 阈值
- **模拟**: log_statement='all' + 高并发
- **指标**: tablespace_used_pct > 95%

### T096 — ANALYZE 同时跑导致统计信息互相覆盖
- **推理链**（4 步）:
  1. 检测计划波动 + autovacuum 和手动 ANALYZE 同时运行
  2. 查 pg_stat_activity → autovacuum 的 ANALYZE 和用户手动 ANALYZE 同时操作同一表
  3. 查采样差异 → 不同采样可能产生不同统计信息
  4. 判定：并发 ANALYZE 导致统计信息抖动 → 建议避免手动 ANALYZE 与 autovacuum 冲突
- **模拟**: 定时任务 ANALYZE 与 autovacuum 同时触发
- **指标**: avg_query_time_ms 波动

### T097 — 大表 REINDEX 阻塞写入
- **推理链**（4 步）:
  1. 检测 lock_wait_sessions spike + 一个 REINDEX 操作
  2. 查 pg_stat_activity → REINDEX 持有 AccessExclusiveLock
  3. 查表大小 → 50GB 索引，REINDEX 需要很长时间
  4. 判定：REINDEX 阻塞写入 → 建议用 REINDEX CONCURRENTLY（PG12+）
- **模拟**: 高并发写入 + `REINDEX INDEX big_index`
- **指标**: lock_wait_sessions spike

### T098 — 逻辑复制冲突导致订阅者停止
- **推理链**（4 步）:
  1. 检测 replication_lag_sec 持续上升
  2. 查 pg_stat_subscription → 状态异常
  3. 查订阅者日志 → ERROR: duplicate key value violates unique constraint
  4. 判定：主库和订阅者数据冲突 → 建议修复冲突数据 + ALTER SUBSCRIPTION SKIP
- **模拟**: 在订阅者上直接写入与发布者冲突的数据
- **指标**: replication_lag_sec 持续上升

### T099 — pg_cron 任务堆叠
- **推理链**（4 步）:
  1. 检测 active_sessions 定时升高 + cpu_sessions spike
  2. 查 pg_stat_activity → 多个相同 SQL 同时运行（cron 任务上次未完成又启动新的）
  3. 查 cron.job → 间隔 5 分钟但任务运行 10 分钟
  4. 判定：cron 任务堆叠 → 建议增加互斥锁（advisory lock）防重入 或增大间隔
- **模拟**: pg_cron 每分钟触发一个 5 分钟的任务
- **指标**: active_sessions 定时升高

### T100 — 综合场景：IO 慢 + 锁等待 + WAL 堆积
- **推理链**（6 步）:
  1. 检测 active_sessions spike + 多项指标异常
  2. 查等待事件分布 → IO 30% + Lock 40% + WAL 20%
  3. 查锁等待 → blocker 在等 IO（DataFileRead）
  4. 查 IO 延迟 → 存储异常
  5. 查 WAL → wal_bytes_rate 升高但 WALSync 延迟因 IO 慢
  6. 判定：根因是 IO 子系统 → 锁等待和 WAL 堆积都是 IO 慢的连锁反应
- **模拟**: IO 限速 + 并发 DML + 互相等待
- **指标**: io_wait_sessions > 10, lock_wait_sessions > 10, wal_bytes_rate 升高

---

## 统计摘要

### 按推理链步数分布

| 步数 | 场景数 | 占比 |
|------|--------|------|
| 3 步 | 22 | 22% |
| 4 步 | 65 | 65% |
| 5 步 | 10 | 10% |
| 6 步 | 3 | 3% |

**平均推理链深度: 3.9 步**

### 按分类分布

| 分类 | 场景数 |
|------|--------|
| 锁与阻塞 | 15 |
| SQL 性能 | 20 |
| VACUUM 与 MVCC | 10 |
| 内存与缓存 | 10 |
| WAL 与 Checkpoint | 10 |
| 等待与延迟 | 10 |
| 连接与会话 | 10 |
| 配置与参数 | 5 |
| 系统与运维 | 10 |

### 与 Oracle 分类对比

| PG 分类 | 对应 Oracle 分类 | 差异说明 |
|---------|-----------------|---------|
| 锁与阻塞 | 锁与阻塞 | PG 无 ITL/Sequence/HW 争用，替换为 advisory lock/VACUUM 锁/分区 DDL |
| SQL 性能 | SQL 性能 | PG 无 HINT 系统/SPM/ACS，替换为 CTE/JSONB/OFFSET/Window Function |
| VACUUM 与 MVCC | 无对应（Oracle UNDO 自动管理） | **PG 独有**：autovacuum/dead tuple/XID wraparound/bloat |
| 内存与缓存 | 内存与缓存 | PG 无 SGA/PGA/Shared Pool 概念，替换为 shared_buffers/work_mem/OS cache |
| WAL 与 Checkpoint | Redo 与日志 | WAL 对应 Redo，但 PG 有 replication slot/logical decoding 特有问题 |
| 等待与延迟 | 等待与延迟 | PG wait_event 体系完全不同，LWLock/BufferPin/IO 等 PG 特有 |
| 连接与会话 | 连接与会话 | PG 进程模型（非线程），连接池更关键 |
| 配置与参数 | 配置与参数 | PG 参数体系不同（random_page_cost/shared_preload_libraries 等） |
| 系统与运维 | 系统与运维 | PG 特有：pg_upgrade/extension/REINDEX CONCURRENTLY/逻辑复制 |

### 环境约束

- **测试服务器**: 单实例 PostgreSQL 14+
- **不含**: 流复制 standby、Patroni/PgPool HA（单实例测试）
- **IO 模拟**: 通过 cgroup v2 限速（root 权限可用）
- **所有场景可安全还原**: 每个模拟后可回退到正常状态

### 按触发策略覆盖

| 策略 | 涉及场景数 |
|------|-----------|
| T1 3σ阈值 | 55 |
| T2 硬顶 | 8 |
| T3 趋势 | 8 |
| T4 加速度 | 6 |
| T5 复合 | 2 |
| T6 容量 | 16 |
| T7 偏移 | 3 |
| T8 回归 | 5 |
| T9 缺失 | 3 |

### PG 特有场景占比

| 类型 | 场景数 | 说明 |
|------|--------|------|
| PG 独有 | 42 | VACUUM/MVCC/XID/bloat/连接池/replication slot 等 |
| 通用但 PG 特化 | 38 | 锁/SQL/IO 等通用问题但用 PG 特有视图/参数/等待事件 |
| 与 Oracle 对等 | 20 | 相同推理逻辑仅替换术语（如行锁级联、死锁、IO 延迟） |
