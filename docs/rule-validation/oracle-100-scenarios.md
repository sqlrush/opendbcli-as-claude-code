# Oracle Rule Engine 验证 — 100 个故障场景（推理链校准版）

## 设计原则

- 每个场景需要 **2-6 步推理链**（决策树深度）
- 太简单的（1 步到位）不入选：表空间满直接加文件、单纯 ORA-00060 死锁
- 太复杂的（需 trace 分析/跨实例推理）不入选：ORA-00600 内部错误、RAC split brain
- 目标：Opus 调 rule → 对比打分 → 输出 rule 优化方案

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

### T001 — 行锁级联：blocker 是空闲会话
- **推理链**（4 步）:
  1. 检测 lock_sessions > 阈值
  2. 查阻塞链 → root blocker SID=123
  3. 查 blocker 状态 → state='INACTIVE', 无当前 SQL, 持有 TX 锁
  4. 判定：应用未提交事务 → 建议 kill session 或联系应用方设 idle_timeout
- **模拟**: Session A `UPDATE t SET x=1 WHERE id=1` 不提交; Session B~T 更新同一行
- **指标**: lock_sessions > 15, blocking_chains ≥ 1, enqueue_wait_time_ms > 5000

### T002 — 行锁级联：blocker 在跑慢 SQL
- **推理链**（4 步）:
  1. 检测 lock_sessions > 阈值
  2. 查阻塞链 → root blocker SID=456
  3. 查 blocker 状态 → state='ACTIVE', SQL_ID=xxx, elapsed=120s, 全表扫描
  4. 判定：blocker 的慢 SQL 导致长时间持锁 → 建议优化该 SQL（加索引/改写）
- **模拟**: Session A 执行无索引大表 UPDATE（耗时 2 分钟）; Session B~T 等待同一行
- **指标**: lock_sessions > 10, long_sql > 1, blocking_chains ≥ 1

### T003 — 行锁级联：blocker 也在等 IO
- **推理链**（5 步）:
  1. 检测 lock_sessions > 阈值
  2. 查阻塞链 → root blocker SID=789
  3. 查 blocker 状态 → state='ACTIVE', wait_event='db file sequential read'
  4. 查 blocker 的 SQL → 正常 SQL 但 IO 延迟异常（> 50ms）
  5. 判定：根因是存储 IO 问题而非锁问题 → 建议先解决 IO 延迟
- **模拟**: IO 限速 + Session A 执行正常 UPDATE（因 IO 慢变长事务）; Session B~T 等待
- **指标**: lock_sessions > 10, io_sessions > 3, db_file_seq_read_avg_us > 50000

### T004 — 多层阻塞链分析
- **推理链**（4 步）:
  1. 检测 lock_sessions > 阈值, blocking_chains 深度 ≥ 3
  2. 查阻塞树 → A 阻塞 B, B 阻塞 C, C 阻塞 D~Z
  3. 定位最上层 root blocker A → 查其状态和 SQL
  4. 判定：A 是根因 → 按 A 的状态给出建议（不是处理 B 或 C）
- **模拟**: 4 个会话串联锁依赖 + 20 个会话排队
- **指标**: blocking_chains 层数 ≥ 3, lock_sessions > 20

### T005 — 死锁 + 频率判断
- **推理链**（3 步）:
  1. 检测 enqueue_deadlocks > 0（即时触发）
  2. 查死锁频率：偶发（< 1 次/小时）还是频繁（> 5 次/小时）
  3. 频率低 → 建议重试逻辑即可; 频率高 → 查事务中 SQL 执行顺序，建议统一访问顺序
- **模拟**: 两个会话交叉更新两行，循环触发死锁（频率可控）
- **指标**: enqueue_deadlocks > 3/h, alert_log_ora_errors > 0

### T006 — 外键缺索引导致 TM 锁扩散
- **推理链**（4 步）:
  1. 检测 lock_sessions 升高, 等待事件 "enq: TM - contention"
  2. 查阻塞链 → blocker 在执行 DELETE FROM parent
  3. 查子表外键列 → 无索引
  4. 判定：子表 FK 列缺索引导致全表锁 → 建议 CREATE INDEX on child(fk_col)
- **模拟**: 父子表 FK 无索引, DELETE FROM parent WHERE id=1
- **指标**: lock_sessions > 5, 等待 "enq: TM", enqueue_wait_time_ms > 5000

### T007 — DDL 阻塞 DML
- **推理链**（4 步）:
  1. 检测 lock_sessions spike + mutex_wait_sessions 升高
  2. 查等待事件 → "library cache lock" 或 "enq: TM"
  3. 查阻塞源 → 一个 ALTER TABLE 操作在等活跃 DML 释放
  4. 判定：DDL/DML 冲突 → 建议在维护窗口做 DDL 或使用 ONLINE DDL
- **模拟**: 多路查询运行中 + 一个 ALTER TABLE ADD COLUMN
- **指标**: mutex_wait_sessions > 5, enqueue_wait_time_ms > 10000

### T008 — ITL 争用
- **推理链**（4 步）:
  1. 检测 lock_sessions 升高
  2. 查等待事件 → "enq: TX - allocate ITL entry"
  3. 查目标表的 INITRANS → 值为 1 或 2（太小）
  4. 判定：ITL 槽位不足 → 建议 ALTER TABLE MOVE INITRANS 10
- **模拟**: INITRANS=1 的小表 + 50 路并发更新不同行
- **指标**: lock_sessions > 10, 等待 "enq: TX - allocate ITL entry"

### T009 — Sequence 争用
- **推理链**（3 步）:
  1. 检测活跃会话增加 + 等待 "enq: SQ - contention"
  2. 查当前使用的 sequence → CACHE_SIZE = 1 或 20
  3. 判定：CACHE 太小 → 建议 ALTER SEQUENCE xxx CACHE 1000
- **模拟**: CACHE=1 的 Sequence + 50 路并发 NEXTVAL
- **指标**: 等待 "enq: SQ" 占比 > 20%, active_sessions 升高

### T010 — HW 争用（高水位标记）
- **推理链**（3 步）:
  1. 检测活跃会话增加 + 等待 "enq: HW - contention"
  2. 查目标表 → 非 ASSM 管理或段空间管理问题
  3. 判定：高并发 INSERT 导致高水位推进争用 → 建议使用 ASSM 或分区表
- **模拟**: 非 ASSM 表空间 + 100 路并发 INSERT
- **指标**: 等待 "enq: HW" 占比 > 15%

### T011 — Buffer Busy Wait + 热块定位
- **推理链**（4 步）:
  1. 检测活跃会话增加 + 等待 "buffer busy waits" 占比 > 30%
  2. 查 V$SEGMENT_STATISTICS 找热块 → 定位到具体表/索引
  3. 查热块原因 → 右增长索引 or 小表高并发
  4. 判定：索引热块 → 建议 REVERSE KEY INDEX 或分区; 表热块 → HASH 分区
- **模拟**: 高并发 INSERT 到有右增长序列主键的表
- **指标**: 等待 "buffer busy waits" > 30%, active_sessions 升高

### T012 — Row Cache Lock
- **推理链**（3 步）:
  1. 检测等待 "row cache lock" 占比 > 20%
  2. 查 V$ROWCACHE → 哪个 cache（dc_sequences / dc_objects）争用
  3. 判定：如果 dc_sequences → sequence cache 太小; 如果 dc_objects → 频繁 DDL
- **模拟**: 高并发 CREATE/DROP SEQUENCE 或 GRANT 操作
- **指标**: 等待 "row cache lock" > 20%

### T013 — Cursor Pin S Wait on X
- **推理链**（4 步）:
  1. 检测 mutex_wait_sessions > 5
  2. 查等待事件 → "cursor: pin S wait on X"
  3. 查相关 SQL → 多个版本（V$SQL.CHILD_NUMBER 过多）
  4. 判定：cursor sharing 问题 → 检查绑定变量类型一致性，或设置 CURSOR_SHARING=FORCE
- **模拟**: 高并发相同 SQL 但绑定变量类型不一致
- **指标**: mutex_wait_sessions > 5, 多 child cursor

### T014 — Latch: Cache Buffers Chains + 热块
- **推理链**（4 步）:
  1. 检测 latch_free_rate > 3%
  2. 查 V$LATCH → "cache buffers chains" 争用
  3. 查热块地址 → 定位到具体 segment
  4. 判定：热块导致 latch 争用 → 建议 HASH 分区消除热块
- **模拟**: 100 路并发读同一个 1 行的小索引表
- **指标**: latch_free_rate > 3%, 等待 "latch: cache buffers chains"

### T015 — 锁等待超时后的级联影响
- **推理链**（4 步）:
  1. 检测 alert_log_ora_errors 升高 + lock_sessions 波动
  2. 查 alert log → ORA-30006 (DDL_LOCK_TIMEOUT) 或 ORA-02049
  3. 查是否有 DDL 操作在争抢锁
  4. 判定：DDL 超时后重试导致反复争抢 → 建议调整 DDL_LOCK_TIMEOUT 或改维护窗口
- **模拟**: DDL_LOCK_TIMEOUT=5 + 大事务执行期间循环尝试 DDL
- **指标**: enqueue_wait_time_ms spike, alert_log_ora_errors > 0

---

## 二、SQL 性能（20 个场景）

### T016 — 全表扫描：缺索引 + 确认是否该加索引
- **推理链**（4 步）:
  1. 检测 cpu_sessions / io_sessions 冲高
  2. 查 Top SQL → SQL_ID=xxx, 执行计划 TABLE ACCESS FULL
  3. 查 WHERE 条件列 → 无索引, 选择度高（distinct values/rows > 10%）
  4. 判定：应加索引 → 建议 CREATE INDEX + 验证计划改变
- **模拟**: 百万行表 `SELECT * FROM t WHERE indexed_col = 'x'` 但该列无索引
- **指标**: full_scan_rate > 50/s, physical_read_rate > 5000/s

### T017 — 全表扫描：选择度低不该加索引
- **推理链**（4 步）:
  1. 检测 io_sessions 冲高
  2. 查 Top SQL → TABLE ACCESS FULL
  3. 查 WHERE 条件列 → 选择度极低（status='A' 占 99%），加索引反而更差
  4. 判定：全扫是合理选择 → 建议分区裁剪或业务层过滤，不建议加索引
- **模拟**: 百万行表 status 列只有 2 个值（A=99%, B=1%），查 status='A'
- **指标**: full_scan_rate > 30/s, 但 cpu_sessions 不高

### T018 — 执行计划漂移：统计信息变化
- **推理链**（5 步）:
  1. 检测 top_sql_elapsed_drift > 5 倍
  2. 查 Top SQL → SQL_ID=xxx, 当前计划 HASH 值与历史不同
  3. 查 V$SQL_PLAN 新旧计划对比 → 旧计划走索引, 新计划走全扫
  4. 查统计信息 → 刚被重新收集，NDV 估算变化
  5. 判定：统计信息导致计划回退 → 建议 SPM 固定好计划 或 SQL Profile
- **模拟**: 收集统计信息后手动修改 NUM_ROWS 让优化器选错
- **指标**: plan_change_count > 1, top_sql_elapsed_drift > 5

### T019 — 执行计划漂移：ACS（自适应游标）
- **推理链**（4 步）:
  1. 检测某 SQL 性能波动 → 同一 SQL_ID 多个 child cursor
  2. 查 V$SQL → IS_BIND_SENSITIVE='Y', IS_BIND_AWARE='Y'
  3. 查不同 child 的执行计划 → 绑定值不同导致不同计划
  4. 判定：ACS 在数据倾斜场景抖动 → 建议用 SQL Plan Baseline 固定或检查直方图
- **模拟**: 高倾斜列 + 绑定变量 + cursor_sharing
- **指标**: plan_change_count > 3, 同一 SQL_ID 多个 plan_hash

### T020 — 硬解析冲高 + shared pool 影响
- **推理链**（4 步）:
  1. 检测 hard_parse_rate > 200/s
  2. 查 shared_pool_free_pct → 下降到 < 15%
  3. 查 Top SQL → 大量 literal SQL（无绑定变量）
  4. 判定：应用层未使用绑定变量 → 建议改应用 或临时 CURSOR_SHARING=FORCE
- **模拟**: 循环执行 `SELECT * FROM t WHERE id = <literal>`
- **指标**: hard_parse_rate > 200/s, shared_pool_free_pct < 15%, library_cache_hit_pct < 90%

### T021 — 硬解析冲高但 Shared Pool 充足
- **推理链**（3 步）:
  1. 检测 hard_parse_rate > 100/s
  2. 查 shared_pool_free_pct → 50%（充足）
  3. 判定：硬解析本身消耗 CPU 但 shared pool 未成瓶颈 → 建议改绑定变量但优先级中等
- **模拟**: 少量不同 SQL 文本但频率高
- **指标**: hard_parse_rate > 100/s, shared_pool_free_pct > 40%, cpu_sessions 略升

### T022 — 大排序溢出到 TEMP
- **推理链**（4 步）:
  1. 检测 temp_used_pct > 80%
  2. 查 Top SQL → ORDER BY / GROUP BY 大结果集
  3. 查 PGA → pga_used_pct 高，排序内存不足溢出到磁盘
  4. 判定：PGA 不足 → 建议增大 PGA_AGGREGATE_TARGET 或优化 SQL 减少排序量
- **模拟**: `SELECT * FROM big_table ORDER BY col1, col2, col3` + PGA_TARGET=100M
- **指标**: temp_used_pct > 80%, pga_used_pct > 90%, 等待 "direct path read temp"

### T023 — Hash Join 溢出
- **推理链**（4 步）:
  1. 检测 temp_used_pct 上升 + io_sessions 增加
  2. 查 Top SQL → 执行计划含 HASH JOIN, 实际行数远超预估
  3. 查 PGA → one-pass 或 multi-pass 操作
  4. 判定：build 端太大 → 建议增 PGA 或在 SQL 层减少 join 数据量（加 WHERE 过滤）
- **模拟**: 两个大表 JOIN + PGA 设小
- **指标**: temp_used_pct > 50%, pga_used_pct > 85%

### T024 — Nested Loop 低效：驱动表行数太多
- **推理链**（4 步）:
  1. 检测 physical_read_rate > 20000/s, io_sessions 冲高
  2. 查 Top SQL → 执行计划 NESTED LOOP, 驱动表实际返回 10 万行
  3. 查优化器估算 → 预估 100 行（统计信息不准）
  4. 判定：驱动表行数估算偏差 → 建议收集统计信息 或 HINT 改 HASH JOIN
- **模拟**: 统计信息过期的大表做 NL JOIN
- **指标**: physical_read_rate > 20000/s, db_file_seq_read_avg_us > 10000

### T025 — PL/SQL 逐行处理 + 批量优化建议
- **推理链**（3 步）:
  1. 检测 long_sql > 3, cpu_sessions 持续占用
  2. 查 Top SQL → PL/SQL 块, cursor loop + 逐行 INSERT
  3. 判定：逐行处理效率低 → 建议改 FORALL/BULK COLLECT 或纯 SQL INSERT AS SELECT
- **模拟**: CURSOR LOOP 逐行 INSERT 处理 50 万行
- **指标**: long_sql > 3, commit_rate 极低, cpu_sessions > 3

### T026 — 数据倾斜 + 绑定变量窥探
- **推理链**（5 步）:
  1. 检测某 SQL 执行时间波动大
  2. 查 V$SQL → 同一 SQL_ID 不同执行时间（100ms vs 30s）
  3. 查绑定变量值 → 高频值走索引快，低频值走全扫慢
  4. 查直方图 → 无直方图或过期
  5. 判定：数据倾斜 + bind peeking → 建议收集直方图 + 考虑 SQL Profile
- **模拟**: 表 99% 行 type='A', 1% type='B', 绑定变量查询
- **指标**: top_sql_elapsed_drift > 10 倍

### T027 — 分区裁剪失效
- **推理链**（4 步）:
  1. 检测 full_scan_rate 升高 + physical_read_rate 冲高
  2. 查 Top SQL → 分区表查询, 计划显示 PARTITION RANGE ALL
  3. 查 WHERE 条件 → 使用非分区键或对分区键做函数转换
  4. 判定：分区裁剪失效 → 建议改写 WHERE 条件用分区键 或 改用函数索引
- **模拟**: 分区表用 TO_CHAR(partition_date) = '20260101' 查询
- **指标**: full_scan_rate 升高, physical_read_rate 升高

### T028 — SQL 执行计划被 SPM 锁定为次优
- **推理链**（4 步）:
  1. 检测某 SQL 长时间性能差但 plan_change_count = 0
  2. 查 DBA_SQL_PLAN_BASELINES → 有 ACCEPTED 计划
  3. 对比 ACCEPTED 计划和当前最优计划 → ACCEPTED 是旧的低效计划
  4. 判定：SPM 锁定了次优计划 → 建议 evolve baseline 或手动 accept 新计划
- **模拟**: 用 SPM 固定一个全扫计划, 然后加了索引但计划不变
- **指标**: top_sql_elapsed_drift > 5 但 plan_change_count = 0

### T029 — SQL 使用错误 HINT
- **推理链**（3 步）:
  1. 检测 Top SQL 性能差
  2. 查执行计划 → HINT 指定了错误索引（INDEX(t idx_col2)）但查 col1
  3. 判定：HINT 误导优化器 → 建议删除 HINT 让优化器自选
- **模拟**: `SELECT /*+ INDEX(t idx_col2) */ * FROM t WHERE col1 = 'x'`
- **指标**: physical_read_rate 异常升高, io_sessions 冲高

### T030 — 大量 SELECT FOR UPDATE 争用
- **推理链**（4 步）:
  1. 检测 lock_sessions 升高 + row_lock_wait_time_ms 升高
  2. 查 Top SQL → SELECT ... FOR UPDATE
  3. 查持锁时间 → 事务持锁后做了大量处理（应用层慢）
  4. 判定：SELECT FOR UPDATE 范围太大或持锁太久 → 建议缩小锁粒度 + SKIP LOCKED
- **模拟**: `SELECT * FROM t WHERE status='pending' FOR UPDATE` + 慢处理逻辑
- **指标**: lock_sessions > 10, row_lock_wait_time_ms > 5000

### T031 — 大量排序但不需要排序
- **推理链**（3 步）:
  1. 检测 temp_used_pct 升高 + cpu_sessions 升高
  2. 查 Top SQL → ORDER BY 但业务不需要排序（多余的 ORDER BY）
  3. 判定：无用排序消耗资源 → 建议去掉不必要的 ORDER BY
- **模拟**: `SELECT * FROM big_table ORDER BY all_columns FETCH FIRST 10 ROWS ONLY`（排序了但只取 10 行）
- **指标**: temp_used_pct 升高, cpu_sessions 升高

### T032 — 视图嵌套导致优化器放弃合并
- **推理链**（4 步）:
  1. 检测 cpu_sessions 持续高 + long_sql 增加
  2. 查 Top SQL → 多层嵌套视图
  3. 查执行计划 → VIEW 操作（未合并）, SORT GROUP BY 在子查询
  4. 判定：视图合并失败 → 建议改写为单层 SQL 或使用 MERGE hint
- **模拟**: 多层嵌套视图 + DISTINCT + GROUP BY
- **指标**: cpu_sessions > 5, long_sql > 3

### T033 — Parallel Query 降级执行
- **推理链**（4 步）:
  1. 检测 pq_sessions 低于预期, long_sql > 3
  2. 查 Top SQL → HINT PARALLEL(8) 但实际 DOP=1
  3. 查 V$PQ_SYSSTAT → parallel 资源耗尽 或 PARALLEL_MAX_SERVERS 满
  4. 判定：并行资源不足降级串行 → 建议增大 PARALLEL_MAX_SERVERS 或错开大查询
- **模拟**: PARALLEL_MAX_SERVERS=8 + 5 个 PARALLEL(8) 查询同时跑
- **指标**: pq_sessions > 8, long_sql > 3, active_sessions > 15

### T034 — SQL 回退（新加索引导致计划变差）
- **推理链**（5 步）:
  1. 检测 top_sql_elapsed_drift > 5
  2. 查 Top SQL → 计划变了, 用了新加的索引
  3. 查新索引 → 选择性不好, 优化器错误估算
  4. 查之前计划 → 全扫反而更快（小表）
  5. 判定：新索引误导优化器 → 建议 DROP INDEX 或使用 SQL Profile 固定旧计划
- **模拟**: 小表加了选择性差的索引, 优化器选了索引但更慢
- **指标**: plan_change_count > 0, top_sql_elapsed_drift > 5

### T035 — 递归 SQL 导致 CPU 冲高
- **推理链**（4 步）:
  1. 检测 cpu_sessions 冲高 + hard_parse_rate 升高
  2. 查 Top SQL → 大量递归 SQL（SYS 用户, 数据字典查询）
  3. 查根因 → 用户 SQL 频繁触发 dictionary lookup（权限检查、动态 SQL）
  4. 判定：递归 SQL 开销过大 → 建议减少动态 SQL, 预编译存储过程
- **模拟**: PL/SQL 中循环 EXECUTE IMMEDIATE 不同 SQL
- **指标**: hard_parse_rate > 100/s, cpu_sessions > 5, recursive SQL 比例高

---

## 三、内存与缓存（10 个场景）

### T036 — Buffer Cache 命中率下降：大表全扫冲刷
- **推理链**（4 步）:
  1. 检测 buffer_cache_hit_pct < 90%（T8 回归触发）
  2. 查 physical_read_rate → spike
  3. 查 Top SQL → 大表全表扫描
  4. 判定：全扫冲刷 buffer cache → 建议限制大查询用 direct path read 或加索引
- **模拟**: `SELECT * FROM very_large_table` 全扫 + 并发 OLTP 小查询
- **指标**: buffer_cache_hit_pct < 85%, physical_read_rate spike

### T037 — Buffer Cache 命中率下降：buffer pool 太小
- **推理链**（4 步）:
  1. 检测 buffer_cache_hit_pct < 90%
  2. 查 physical_read_rate → 持续高（非 spike）
  3. 查 DB_CACHE_SIZE → 相对数据集太小
  4. 判定：buffer pool 不足 → 建议增大 DB_CACHE_SIZE
- **模拟**: DB_CACHE_SIZE 设为 200M, 数据集 2GB, 正常 OLTP 负载
- **指标**: buffer_cache_hit_pct 持续 < 90%, physical_read_rate 持续高

### T038 — Shared Pool 不足：ORA-04031
- **推理链**（4 步）:
  1. 检测 shared_pool_free_pct < 5%（T6 红线）+ alert_log 有 ORA-04031
  2. 查硬解析率 → 高（大量 literal SQL）
  3. 查 V$SGASTAT → shared pool 碎片化
  4. 判定：硬解析 + 碎片化 → 建议增 SHARED_POOL_SIZE + 改绑定变量 + FLUSH SHARED_POOL 临时缓解
- **模拟**: SHARED_POOL_SIZE 设小 + 大量不同 SQL 文本
- **指标**: shared_pool_free_pct < 5%, hard_parse_rate > 100/s, alert_log_ora_errors > 0

### T039 — PGA 溢出到 TEMP
- **推理链**（4 步）:
  1. 检测 pga_used_pct > 95%（T6 红线）+ temp_used_pct 升高
  2. 查 V$SQL_WORKAREA → 哪些 SQL 在做 one-pass/multi-pass
  3. 查这些 SQL → 大排序/HASH JOIN
  4. 判定：PGA 不足 → 建议增大 PGA_AGGREGATE_TARGET + 优化大排序 SQL
- **模拟**: PGA_AGGREGATE_TARGET=100M + 多路大排序
- **指标**: pga_used_pct > 95%, temp_used_pct > 50%

### T040 — Library Cache 命中率下降
- **推理链**（4 步）:
  1. 检测 library_cache_hit_pct < 95%（T8 回归, floor=95%）
  2. 查 hard_parse_rate → 升高
  3. 查 shared_pool_free_pct → 偏低但未触发 ORA-04031
  4. 判定：SQL 被频繁 age out → 建议增 SHARED_POOL_SIZE 或减少硬解析
- **模拟**: SHARED_POOL_SIZE 偏小 + 高并发不同 SQL
- **指标**: library_cache_hit_pct < 95%, hard_parse_rate > 50/s

### T041 — Free Buffer Waits + DBWR 写慢
- **推理链**（4 步）:
  1. 检测等待 "free buffer waits" 出现
  2. 查 buffer_cache_hit_pct → 可能正常（问题是写不是读）
  3. 查 DBWR 等待 → "db file parallel write" 延迟高
  4. 判定：存储写入慢导致脏块积压 → 建议检查存储性能 或增加 DBWR_IO_SLAVES
- **模拟**: IO 限速 + 大量 DML
- **指标**: 等待 "free buffer waits" > 10%, db file parallel write 延迟高

### T042 — Shared Pool 碎片化但不缺空间
- **推理链**（4 步）:
  1. 检测 hard_parse_rate 略升 + 偶发 ORA-04031
  2. 查 shared_pool_free_pct → 20%（看似充足）
  3. 查 V$SGASTAT → free memory 碎片化，大块分配失败
  4. 判定：碎片化问题 → 建议 ALTER SYSTEM FLUSH SHARED_POOL（临时）+ 增大 SHARED_POOL_RESERVED_SIZE
- **模拟**: 交替加载/卸载大 PL/SQL 包造成碎片
- **指标**: shared_pool_free_pct > 15% 但 ORA-04031 出现

### T043 — Result Cache 失效风暴
- **推理链**（3 步）:
  1. 检测 CPU 使用略升 + 等待 "Result Cache: RC Latch"
  2. 查依赖表 → 高频 DML 表启用了 Result Cache
  3. 判定：频繁 DML 使 Result Cache 持续失效，latch 争用反而降低性能 → 建议对高频 DML 表禁用 Result Cache
- **模拟**: 对高频 UPDATE 表使用 RESULT_CACHE hint
- **指标**: 等待 "Result Cache: RC Latch" 增加

### T044 — SGA Auto-resize 抖动
- **推理链**（4 步）:
  1. 检测 shared_pool_free_pct 或 buffer_cache_hit_pct 波动（T7 偏移）
  2. 查 V$SGA_RESIZE_OPS → 频繁 resize 操作
  3. 查 SGA_TARGET → 设置偏小，组件之间抢资源
  4. 判定：SGA 自动调整频繁 → 建议增大 SGA_TARGET 或固定组件大小
- **模拟**: SGA_TARGET 设偏小 + 变化负载
- **指标**: shared_pool_free_pct 波动, V$SGA_RESIZE_OPS 记录频繁

### T045 — Large Pool 不足影响 RMAN
- **推理链**（3 步）:
  1. 检测 RMAN 备份变慢 + 等待 "resmgr" 相关
  2. 查 LARGE_POOL_SIZE → 太小, RMAN 使用 shared pool 导致争用
  3. 判定：LARGE_POOL 不足 → 建议增大 LARGE_POOL_SIZE
- **模拟**: LARGE_POOL_SIZE=0 + RMAN 备份 + 高并发 OLTP
- **指标**: 等待 "RMAN backup" 相关, shared_pool_free_pct 下降

---

## 四、存储与容量（10 个场景）

### T046 — 表空间满 + 定位大表 + 扩展建议
- **推理链**（4 步）:
  1. 检测 tablespace_used_pct ≥ 95%（T6 红线）
  2. 查 DBA_SEGMENTS → 定位 Top 5 大 segment
  3. 查 autoextend 状态 → 是否已到 MAXSIZE
  4. 判定：按大 segment 给出建议 → 清理历史数据 / 加 datafile / 增 MAXSIZE
- **模拟**: 插入数据到表空间 95%
- **指标**: tablespace_used_pct ≥ 95%

### T047 — TEMP 表空间满 + 定位消耗者
- **推理链**（4 步）:
  1. 检测 temp_used_pct ≥ 95%（T6 红线）
  2. 查 V$SORT_USAGE / V$TEMPSEG_USAGE → 哪个 session 占用最多
  3. 查该 session 的 SQL → 大排序/HASH JOIN
  4. 判定：特定 SQL 占满 TEMP → 建议优化 SQL + 临时增大 TEMP
- **模拟**: 多路大排序 + 小 TEMP 表空间
- **指标**: temp_used_pct ≥ 95%

### T048 — UNDO 表空间满 + 长事务分析
- **推理链**（4 步）:
  1. 检测 undo_used_pct ≥ 95%（T6 红线）
  2. 查 V$TRANSACTION → 长事务占大量 UNDO
  3. 查长事务对应的 SQL → 是正常业务还是遗忘的大事务
  4. 判定：长事务占 UNDO → 建议拆分大事务 + 调整 UNDO_RETENTION
- **模拟**: 长事务不提交 + 高并发 DML
- **指标**: undo_used_pct ≥ 95%, alert_log ORA-30036

### T049 — FRA 满 + 归档策略分析
- **推理链**（4 步）:
  1. 检测 fra_used_pct ≥ 95%（T6 红线）
  2. 查 V$RECOVERY_FILE_DEST → 哪类文件占用最多（archived log / flashback log）
  3. 查 RMAN retention policy → 是否有删除策略
  4. 判定：归档堆积 → 建议 RMAN DELETE ARCHIVELOG + 调整 FRA 大小
- **模拟**: 停止 RMAN 删除策略 + 持续生成归档
- **指标**: fra_used_pct ≥ 95%, archive_lag_sec 可能上升

### T050 — Redo Log 太小导致频繁切换
- **推理链**（4 步）:
  1. 检测 log_switch_rate > 30 次/h
  2. 查 redo log 大小 → 50MB（太小）
  3. 查 checkpoint_not_complete → 是否 > 0（写入跟不上切换）
  4. 判定：redo log 太小 → 建议增大到 500MB-1GB + 增加组数
- **模拟**: 50M redo log + 持续大量 DML
- **指标**: log_switch_rate > 30/h, checkpoint_not_complete > 0

### T051 — ORA-01555 快照过旧分析
- **推理链**（4 步）:
  1. 检测 alert_log_ora_errors > 0 (ORA-01555)
  2. 查当前 UNDO_RETENTION → 值偏小
  3. 查是否有长查询 → 长查询需要的一致性读版本被覆盖
  4. 判定：UNDO_RETENTION 不足 → 建议增大 UNDO_RETENTION + 增大 UNDO 表空间
- **模拟**: UNDO_RETENTION=60 + 长查询(5分钟) + 高并发 DML
- **指标**: alert_log_ora_errors > 0, undo_used_pct 高

### T052 — 大批量 DML 产生巨量 Redo
- **推理链**（4 步）:
  1. 检测 redo_rate > 100000 KB/s（T2 硬顶）
  2. 查 Top SQL → 大批量 INSERT/UPDATE
  3. 查是否可用 NOLOGGING → 业务允许？有备库？
  4. 判定：批量操作 redo 过大 → 建议分批提交 + 考虑 APPEND + NOLOGGING（如无备库）
- **模拟**: `INSERT INTO big SELECT * FROM source`（百万行）
- **指标**: redo_rate > 100000 KB/s, log_switch_rate > 20/h

### T053 — Datafile Autoextend 达上限
- **推理链**（3 步）:
  1. 检测 tablespace_used_pct ≥ 95% + ORA-01654 错误
  2. 查 DBA_DATA_FILES → MAXBYTES 已达上限
  3. 判定：需要增大 MAXSIZE 或添加新 datafile
- **模拟**: MAXSIZE=500M 的 datafile, 插入数据到满
- **指标**: tablespace_used_pct ≥ 95%, alert_log ORA-01654

### T054 — 段空间浪费（高水位标记问题）
- **推理链**（4 步）:
  1. 检测 full_scan_rate 对某表异常高
  2. 查该表 → 大量 DELETE 后表仍然很大（HWM 未回退）
  3. 查 DBA_SEGMENTS.BYTES vs 实际行数 → 差异巨大
  4. 判定：高水位标记过高 → 建议 ALTER TABLE SHRINK SPACE 或 MOVE + REBUILD INDEX
- **模拟**: 百万行表 DELETE 90% 后查询仍然全扫大量块
- **指标**: full_scan_rate 高, physical_read_rate 与行数不匹配

### T055 — 归档目录空间分析
- **推理链**（4 步）:
  1. 检测 archive_lag_sec > 300（T6 黄线）
  2. 查 V$ARCHIVE_DEST → 目标目录状态
  3. 查磁盘空间 → 归档目录满
  4. 判定：归档目录空间不足 → 建议清理旧归档 + 增大磁盘 + 配置备用目录
- **模拟**: 限制归档目录空间 + 大量 DML
- **指标**: archive_lag_sec > 300, redo_log_space_wait > 0

---

## 五、Redo 与日志（10 个场景）

### T056 — Log File Sync 慢：存储 IO 问题
- **推理链**（4 步）:
  1. 检测 log_file_sync_avg_us > 10000（T1+T2 触发）
  2. 查 "log file parallel write" 延迟 → 也高
  3. 查 redo log 所在磁盘 IO 延迟 → 异常
  4. 判定：底层存储慢导致 redo 写入延迟 → 建议迁移 redo log 到快速存储
- **模拟**: IO 限速 redo log 磁盘 + 高频提交
- **指标**: log_file_sync_avg_us > 10000, "log file parallel write" 也高

### T057 — Log File Sync 慢：提交频率过高
- **推理链**（4 步）:
  1. 检测 log_file_sync_avg_us > 5000
  2. 查 "log file parallel write" → 正常（< 2ms）
  3. 查 commit_rate → > 5000/s（极高频提交）
  4. 判定：提交频率太高导致 LGWR 排队 → 建议 batch commit 或 COMMIT_WRITE=NOWAIT
- **模拟**: 逐行提交 100 万条 INSERT（每行一个 COMMIT）
- **指标**: log_file_sync > 5000us, "log file parallel write" < 2ms, commit_rate > 5000/s

### T058 — Checkpoint Not Complete
- **推理链**（4 步）:
  1. 检测 checkpoint_not_complete > 3/h
  2. 查 log_switch_rate → 是否频繁切换
  3. 查 redo log 大小 + IO 性能
  4. 判定：redo log 太小 + IO 慢 → 建议增大 redo log + 提升 IO
- **模拟**: 小 redo log + IO 限速 + 持续 DML
- **指标**: checkpoint_not_complete > 3/h, log_switch_rate > 20/h

### T059 — Redo Log Space Wait
- **推理链**（3 步）:
  1. 检测 redo_log_space_wait > 5（T1+T5 复合）
  2. 查所有 redo log group 状态 → 全部 ACTIVE（等 checkpoint）
  3. 判定：redo log 组数不够或太小 → 建议增加 redo log 组 + 增大 size
- **模拟**: 3 组 50M redo log + 持续大量 DML
- **指标**: redo_log_space_wait > 5, checkpoint_not_complete > 0

### T060 — 归档延迟 + 备库影响
- **推理链**（4 步）:
  1. 检测 archive_lag_sec > 60（T1 触发）
  2. 查归档进程状态 → 正常但落后
  3. 查 redo 生成速率 → 远超归档传输速率
  4. 判定：redo 生成太快 → 建议减少 redo 生成 + 增大归档带宽
- **模拟**: 大批量 DML + 限速归档网络
- **指标**: archive_lag_sec > 60, redo_rate > 50000 KB/s

### T061 — 归档模式下大批量操作的 Redo 管理
- **推理链**（4 步）:
  1. 检测 redo_rate > 80000 KB/s + log_switch_rate > 15/h
  2. 查 Top SQL → 大批量 INSERT/UPDATE/DELETE
  3. 查是否可分批提交 → 当前一次性处理百万行
  4. 判定：单次大事务产生过多 redo → 建议分批 1 万行提交 + 考虑 NOLOGGING/APPEND
- **模拟**: `INSERT INTO target SELECT * FROM source` 百万行不分批
- **指标**: redo_rate > 80000 KB/s, log_switch_rate > 15/h

### T062 — LGWR 写延迟 + 提交堆积分析
- **推理链**（4 步）:
  1. 检测 log_file_sync_avg_us > 15000 + commit_rate 下降
  2. 查 "log file parallel write" → 延迟也高（> 10ms）
  3. 查 redo log 组数和大小 → 只有 2 组 50M
  4. 判定：redo log 组不够 + IO 慢双重因素 → 建议增到 4+ 组 + 迁移到快速存储
- **模拟**: 2 组 50M redo log + IO 限速 + 高频提交
- **指标**: log_file_sync_avg_us > 15000, commit_rate 下降

### T063 — LOB 段膨胀导致表空间增长
- **推理链**（4 步）:
  1. 检测 tablespace_used_pct 持续上升（T3 趋势）
  2. 查 DBA_SEGMENTS → LOB 段占比最大
  3. 查 LOB 表 → 大量 UPDATE LOB 列但旧版本未回收
  4. 判定：LOB 段膨胀 → 建议 ALTER TABLE MOVE LOB 或设置 PCTVERSION/RETENTION
- **模拟**: 循环 UPDATE CLOB 列产生大量旧版本
- **指标**: tablespace_used_pct 上升趋势, LOB segment 占比 > 60%

### T064 — 索引失效后查询退化
- **推理链**（4 步）:
  1. 检测 full_scan_rate spike + physical_read_rate 冲高
  2. 查 Top SQL → 之前走索引的 SQL 现在走全扫
  3. 查索引状态 → UNUSABLE（DDL 操作导致）
  4. 判定：索引失效 → 建议 ALTER INDEX REBUILD
- **模拟**: ALTER TABLE MOVE 后不 REBUILD INDEX
- **指标**: full_scan_rate spike, physical_read_rate 冲高, plan_change_count > 0

### T065 — Log Switch 时性能抖动
- **推理链**（4 步）:
  1. 检测周期性 active_sessions spike（每隔几分钟）
  2. 查 log switch 频率 → 每 3 分钟一次
  3. 查 spike 与 log switch 时间相关性 → 吻合
  4. 判定：log switch 触发 checkpoint 导致 IO 抖动 → 建议增大 redo log
- **模拟**: 50M redo log + 持续中等 DML
- **指标**: log_switch_rate > 20/h, active_sessions 周期性 spike

---

## 六、等待事件与延迟（10 个场景）

### T066 — db file sequential read 延迟：存储问题
- **推理链**（4 步）:
  1. 检测 db_file_seq_read_avg_us > 20000（T1+T2 触发）
  2. 查 Top SQL → 正常索引扫描（非全扫）
  3. 查存储层 IO 延迟 → 异常高
  4. 判定：底层存储性能下降 → 建议检查存储告警 + 考虑缓存预热
- **模拟**: IO 限速 + 索引范围扫描
- **指标**: db_file_seq_read_avg_us > 20000

### T067 — db file sequential read 延迟：索引低效
- **推理链**（4 步）:
  1. 检测 db_file_seq_read_avg_us > 10000 + physical_read_rate 高
  2. 查 Top SQL → 索引扫描但返回大量行
  3. 查索引 clustering factor → 很高（数据物理顺序与索引顺序不一致）
  4. 判定：索引 clustering factor 差 → 建议重建表或改用覆盖索引
- **模拟**: 按非主键列建索引，查询大量行
- **指标**: db_file_seq_read 延迟高, physical_read_rate 高

### T068 — db file scattered read 延迟
- **推理链**（3 步）:
  1. 检测 db_file_scat_read_avg_us > 15000
  2. 查 Top SQL → 全表扫描
  3. 判定：全扫导致多块读延迟 → 建议加索引减少全扫 或检查存储性能
- **模拟**: IO 限速 + 全表扫描
- **指标**: db_file_scat_read_avg_us > 15000

### T069 — 网络延迟偏移（T7 检测）
- **推理链**（3 步）:
  1. 检测 network_round_trip_us 均值偏移（T7 新旧窗口差 > 2σ）
  2. 查网络层 → 延迟从 1ms 基线漂移到 5ms
  3. 判定：网络质量下降 → 建议检查网络设备/链路
- **模拟**: TC 限速模拟网络延迟
- **指标**: network_round_trip_us 偏移

### T070 — 提交速率骤降（T9 缺失）
- **推理链**（4 步）:
  1. 检测 commit_rate 从 500/s 降到 < 100/s（T9 drop > 80%）
  2. 查 active_sessions → 不降反升
  3. 查 Top Wait Event → 锁等待或 IO 等待
  4. 判定：吞吐下降但会话不降 → 存在阻塞（非正常业务低峰）→ 查锁或 IO
- **模拟**: 锁住关键表后所有事务等待
- **指标**: commit_rate drop > 80%, active_sessions 不变

### T071 — Enqueue Wait 均值偏移（T7）
- **推理链**（3 步）:
  1. 检测 enqueue_wait_time_ms 新旧窗口均值差 > 2σ
  2. 查锁等待类型 → 是 TX 锁还是 TM 锁
  3. 判定：根据锁类型给出不同建议（TX → 应用优化, TM → DDL 冲突）
- **模拟**: 逐步增加锁持有时间，使均值漂移
- **指标**: enqueue_wait_time_ms T7 偏移

### T072 — DBWR 写入慢导致 free buffer waits
- **推理链**（4 步）:
  1. 检测等待 "free buffer waits" + io_sessions 升高
  2. 查 "db file parallel write" 延迟 → 高
  3. 查磁盘 IO → 写延迟异常
  4. 判定：写性能瓶颈 → 建议检查存储 + 增加 DBWR_IO_SLAVES
- **模拟**: IO 限速 + 大量 DML
- **指标**: 等待 "free buffer waits", "db file parallel write" 延迟高

### T073 — Read by Other Session
- **推理链**（3 步）:
  1. 检测等待 "read by other session" 出现
  2. 查对应数据块 → 多个会话竞争同一冷块
  3. 判定：正常并发冲突（短暂等待）→ 如频繁则建议预热缓存或分区
- **模拟**: FLUSH BUFFER_CACHE 后并发读同一表
- **指标**: 等待 "read by other session"

### T074 — Direct Path Read 导致全扫绕过缓存
- **推理链**（4 步）:
  1. 检测 physical_read_rate spike + buffer_cache_hit_pct 不变（未经过 cache）
  2. 查 Top SQL → 大表查询走 direct path read（Serial Direct Read）
  3. 查表大小 vs _small_table_threshold → 表太大触发 direct path
  4. 判定：大表 direct path 正常但影响性能 → 建议并行或缓存控制
- **模拟**: 大表全扫触发 serial direct read
- **指标**: physical_read_rate spike, 等待 "direct path read"

### T075 — IO Calibration 差异分析
- **推理链**（3 步）:
  1. 检测多项 IO 延迟指标均偏高（T1 触发）
  2. 查 DBMS_RESOURCE_MANAGER.CALIBRATE_IO 结果
  3. 判定：存储 IOPS/吞吐低于预期 → 建议联系存储管理员
- **模拟**: IO 限速模拟存储退化
- **指标**: db_file_seq_read, db_file_scat_read, log_file_sync 均偏高

---

## 七、连接与会话管理（10 个场景）

### T076 — 连接风暴 + 连接池分析
- **推理链**（4 步）:
  1. 检测 session_creation_rate > 50/s（T1+T4 加速度）
  2. 查连接来源 → 同一 MACHINE/PROGRAM 大量短连接
  3. 查是否使用连接池 → 未使用或池大小不足
  4. 判定：应用层连接池问题 → 建议配置连接池 + 设置 SHARED_SERVERS
- **模拟**: 循环 connect/disconnect 500 次/分钟
- **指标**: session_creation_rate > 50/s, total_sessions 波动

### T077 — Sessions 接近上限 + 来源定位
- **推理链**（4 步）:
  1. 检测 resource_limit_pct > 90%（T6 触发）
  2. 查 V$SESSION → 按 USERNAME/MACHINE 分组
  3. 定位消耗最多连接的来源
  4. 判定：某应用连接泄漏 → 建议联系应用方 + 临时增大 sessions
- **模拟**: 打开 sessions 到 90% 的参数值
- **指标**: resource_limit_pct > 90%

### T078 — Aborted Connects 冲高
- **推理链**（3 步）:
  1. 检测 alert log 中连接拒绝错误
  2. 查 sessions/processes 使用率 → 已满或密码错误
  3. 判定：如果使用率满 → 增大参数; 如果密码错误 → 排查应用配置
- **模拟**: 错误密码批量连接尝试
- **指标**: alert_log_ora_errors > 0, session_creation_rate 异常

### T079 — 会话泄漏（趋势检测 T3）
- **推理链**（4 步）:
  1. 检测 total_sessions 持续上升趋势（T3 线性回归斜率 > 0.5σ）
  2. 查 V$SESSION → INACTIVE 会话持续增长, LOGON_TIME 很早
  3. 查 MACHINE/PROGRAM → 定位泄漏来源
  4. 判定：应用未正确关闭连接 → 建议修复应用 + 设置 IDLE_TIME profile
- **模拟**: 打开连接后不关闭，持续累积
- **指标**: total_sessions T3 趋势上升

### T080 — Resource Manager 限流导致响应慢
- **推理链**（4 步）:
  1. 检测 sql_throttle_count > 5 + active_sessions 不高但用户报慢
  2. 查 DBA_RSRC_PLAN_DIRECTIVES → 当前 plan 限制
  3. 查被限流的消费者组 → 用户在该组
  4. 判定：Resource Manager CPU 限制 → 建议调整 plan 或切换 plan
- **模拟**: 配置 Resource Plan 限制 CPU 为 10%
- **指标**: sql_throttle_count > 5

### T081 — 隐式类型转换导致索引失效
- **推理链**（4 步）:
  1. 检测 cpu_sessions 升高 + full_scan_rate 升高
  2. 查 Top SQL → WHERE varchar_col = 123（数字与字符串比较）
  3. 查执行计划 → TO_NUMBER(varchar_col) 导致索引无法使用
  4. 判定：隐式类型转换 → 建议改 WHERE varchar_col = '123'
- **模拟**: VARCHAR2 列有索引，用数字条件查询
- **指标**: cpu_sessions 升高, full_scan_rate 升高

### T082 — 并行查询资源争用
- **推理链**（4 步）:
  1. 检测 pq_sessions > PARALLEL_MAX_SERVERS × 80%
  2. 查当前并行查询 → 多个大查询同时执行
  3. 查各查询的请求 DOP vs 实际 DOP → 部分降级
  4. 判定：并行资源不足 → 建议错开执行时间 或增大 PARALLEL_MAX_SERVERS
- **模拟**: 5 个 PARALLEL(8) 查询同时执行
- **指标**: pq_sessions > 30, active_sessions > 40

### T083 — 后台进程等待异常
- **推理链**（3 步）:
  1. 检测 background_wait > 5
  2. 查具体后台进程 → DBWR/LGWR/ARCH 哪个在等
  3. 判定：根据等待的进程给出不同建议（DBWR→存储, LGWR→redo, ARCH→归档）
- **模拟**: IO 限速特定磁盘
- **指标**: background_wait > 5

### T084 — Active Sessions 加速度突增（T4）
- **推理链**（3 步）:
  1. 检测 active_sessions 二阶导数 > std（T4 加速度）
  2. 查 spike 时间点对应的等待事件 → 定位是锁/IO/CPU
  3. 判定：根据等待事件分类给出根因
- **模拟**: 定时任务在某一秒启动 50 个并发
- **指标**: active_sessions 1 秒内从 5 跳到 50

### T085 — 全库 Hang 分析
- **推理链**（5 步）:
  1. 检测 active_sessions > 30 但 commit_rate = 0（T9）
  2. 查 cpu_sessions → 0（无人在跑）
  3. 查 Top Wait → 全部在等某个事件
  4. 查等待链 → 是否有循环依赖或系统级锁
  5. 判定：数据库 Hang → 建议 oradebug hanganalyze 或 kill root blocker
- **模拟**: 持有 SYS 对象锁 + 所有操作等待
- **指标**: active_sessions > 30, commit_rate = 0, cpu_sessions = 0

---

## 八、配置与参数问题（5 个场景）

### T086 — UNDO_RETENTION 过小导致 ORA-01555
- **推理链**（4 步）:
  1. 检测 alert_log_ora_errors > 0（ORA-01555）
  2. 查当前 UNDO_RETENTION → 60s（太小）
  3. 查长查询 → 有查询运行 5 分钟，需要一致性读版本
  4. 判定：UNDO_RETENTION 不匹配业务查询时长 → 建议增大到 900+ 秒
- **模拟**: UNDO_RETENTION=60 + 长查询 + 高并发 DML
- **指标**: alert_log_ora_errors > 0, undo_used_pct 高

### T087 — OPEN_CURSORS 不足导致 ORA-01000
- **推理链**（4 步）:
  1. 检测 alert_log_ora_errors > 0（ORA-01000 maximum open cursors exceeded）
  2. 查 OPEN_CURSORS 参数 → 300（默认值）
  3. 查 V$SESSTAT → 某些 session 已开 cursor 接近 300
  4. 判定：cursor 泄漏或 OPEN_CURSORS 太小 → 查应用是否关闭 cursor + 临时增大参数
- **模拟**: PL/SQL 循环打开 cursor 不关闭
- **指标**: alert_log_ora_errors > 0

### T088 — LOG_BUFFER 太小导致 redo 等待
- **推理链**（4 步）:
  1. 检测 log_file_sync_avg_us > 5000 + redo_log_space_wait > 0
  2. 查 "log buffer space" 等待 → 出现
  3. 查 LOG_BUFFER → 偏小（如 2M）
  4. 判定：LOG_BUFFER 不足 → 建议增大到 16M+
- **模拟**: LOG_BUFFER=2M + 高频大量 DML
- **指标**: 等待 "log buffer space", redo_log_space_wait > 0

### T089 — 优化器参数配错导致全局计划退化
- **推理链**（4 步）:
  1. 检测多条 SQL 的 top_sql_elapsed_drift > 5
  2. 查 OPTIMIZER_MODE → RULE（被误设为 RBO 模式）
  3. 查 plan_change_count → 多个 SQL 计划变了
  4. 判定：优化器模式配错 → 建议改回 ALL_ROWS
- **模拟**: `ALTER SYSTEM SET OPTIMIZER_MODE=RULE` 后跑负载
- **指标**: plan_change_count > 5, top_sql_elapsed_drift 多个 > 5

### T090 — DB_FILE_MULTIBLOCK_READ_COUNT 过大导致全扫偏好
- **推理链**（4 步）:
  1. 检测 full_scan_rate 偏高, 本应走索引的 SQL 走了全扫
  2. 查执行计划 → 优化器选全扫（cost 更低）
  3. 查 DB_FILE_MULTIBLOCK_READ_COUNT → 128（过大，导致全扫 cost 被低估）
  4. 判定：MBRC 过大误导优化器 → 建议减小或让 Oracle 自动设置
- **模拟**: `ALTER SYSTEM SET DB_FILE_MULTIBLOCK_READ_COUNT=128` + 中等表查询
- **指标**: full_scan_rate 升高

---

## 九、系统与运维（10 个场景）

### T091 — Checkpoint Not Complete + 定位原因
- **推理链**（4 步）:
  1. 检测 checkpoint_not_complete > 5/h
  2. 查 log_switch_rate → 频繁切换？
  3. 查 DBWR IO 延迟 → 写入慢？
  4. 判定：redo log 太小 and/or IO 慢 → 对应建议
- **模拟**: 小 redo log + IO 限速 + 持续 DML
- **指标**: checkpoint_not_complete > 5/h

### T092 — Alert Log ORA 错误分析
- **推理链**（4 步）:
  1. 检测 alert_log_ora_errors > 10/min（T1+T4 加速度）
  2. 查错误类型 → ORA-04031 / ORA-01555 / ORA-00060 等
  3. 根据错误类型定位对应子系统
  4. 判定：按错误类型给出对应修复建议
- **模拟**: 触发特定可重现 ORA 错误
- **指标**: alert_log_ora_errors > 10/min

### T093 — Job 调度连续失败
- **推理链**（4 步）:
  1. 检测 job_failure_rate > 50%（T1 触发）
  2. 查 DBA_SCHEDULER_JOB_RUN_DETAILS → 失败原因
  3. 查是否是依赖资源不可用 or SQL 错误
  4. 判定：根据失败原因给出修复建议
- **模拟**: 创建依赖外部资源的 Job + 断开资源
- **指标**: job_failure_rate > 50%

### T094 — 实例恢复后的性能问题
- **推理链**（4 步）:
  1. 检测 instance_status 刚变为 OPEN + buffer_cache_hit_pct 极低
  2. 查 uptime → 很短（刚启动）
  3. 查 physical_read_rate → spike（缓存冷启动）
  4. 判定：实例重启后缓存为空 → 建议缓存预热
- **模拟**: 重启 Oracle 后立即跑负载
- **指标**: buffer_cache_hit_pct < 70%, physical_read_rate spike

### T095 — Resource Limit 达上限
- **推理链**（3 步）:
  1. 检测 resource_limit_pct > 95%（T6 红线）
  2. 查 V$RESOURCE_LIMIT → sessions 还是 processes 先到上限
  3. 判定：建议增大对应参数 + 排查连接泄漏
- **模拟**: 打开连接到 processes 的 95%
- **指标**: resource_limit_pct > 95%

### T096 — 审计开销分析
- **推理链**（3 步）:
  1. 检测 redo_rate 偏高 + cpu_sessions 略升
  2. 查审计配置 → 精细审计（FGA）对高频表
  3. 判定：审计开销过大 → 建议精简审计策略
- **模拟**: 对高频表启用 FGA
- **指标**: redo_rate 升高

### T097 — Data Pump 挂起分析
- **推理链**（4 步）:
  1. 检测 long_sql > 0, 某会话执行 expdp/impdp 相关 SQL
  2. 查该会话等待事件 → 是 lock 等待还是 IO 等待
  3. 如果 lock → 查阻塞源; 如果 IO → 查目标目录性能
  4. 判定：根据等待类型给出建议
- **模拟**: 导出大表时锁住表
- **指标**: long_sql > 0, lock_sessions 可能升高

### T098 — 数据库升级后 SQL 回退
- **推理链**（5 步）:
  1. 检测多条 SQL 性能回退（top_sql_elapsed_drift 多个 > 5）
  2. 查 optimizer 版本 → 刚升级
  3. 查 plan_change_count → 多个 SQL 计划变了
  4. 查新旧计划差异 → 新版本优化器选了不同策略
  5. 判定：升级导致计划回退 → 建议 STS+SPM 批量固定 或设置 OPTIMIZER_FEATURES_ENABLE
- **模拟**: 设置 OPTIMIZER_FEATURES_ENABLE 到旧版本模拟
- **指标**: plan_change_count > 10, top_sql_elapsed_drift 多个 > 5

### T099 — Latch: Shared Pool 争用 + 定位
- **推理链**（4 步）:
  1. 检测 latch_free_rate > 5%（T8 回归触发）
  2. 查 V$LATCH → "shared pool" latch 争用
  3. 查 hard_parse_rate → 高（根因）
  4. 判定：硬解析导致 shared pool latch 争用 → 建议绑定变量
- **模拟**: 100 路并发不同 SQL 文本
- **指标**: latch_free_rate > 5%, hard_parse_rate > 200/s

### T100 — 综合场景：IO 慢 + 锁等待 + Redo 堆积
- **推理链**（6 步）:
  1. 检测 active_sessions spike + 多项指标异常
  2. 查等待事件分布 → IO 30% + Lock 40% + Commit 20%
  3. 查锁等待 → blocker 在等 IO
  4. 查 IO 延迟 → 存储异常
  5. 查 redo → log_file_sync 也因 IO 升高
  6. 判定：根因是 IO 子系统 → 锁等待和 redo 堆积都是 IO 慢的连锁反应
- **模拟**: IO 限速 + 并发 DML + 互相等待
- **指标**: io_sessions > 10, lock_sessions > 10, log_file_sync > 10000

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
| 内存与缓存 | 10 |
| 存储与容量 | 10 |
| Redo 与日志 | 10 |
| 等待与延迟 | 10 |
| 连接与会话 | 10 |
| 配置与参数 | 5 |
| 系统与运维 | 10 |

### 环境约束

- **测试服务器**: root@47.88.57.91 单机 Oracle
- **不含**: Data Guard（无备库）、RAC（单实例）
- **IO 模拟**: 通过 cgroup v2 限速（root 权限可用）
- **所有场景可安全还原**: 每个模拟后可回退到正常状态

### 按触发策略覆盖

| 策略 | 涉及场景数 |
|------|-----------|
| T1 3σ阈值 | 62 |
| T2 硬顶 | 10 |
| T3 趋势 | 6 |
| T4 加速度 | 8 |
| T5 复合 | 2 |
| T6 容量 | 14 |
| T7 偏移 | 5 |
| T8 回归 | 8 |
| T9 缺失 | 4 |
