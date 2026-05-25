# Rule Engine 全量重测方案

## 原则

- **SSD 不是借口** — 除 RAC/DG/RM/后台进程故障(DBWR/LGWR)外，所有场景都能模拟
- 排除场景仅 8 个: T041, T056, T060, T062, T069, T072, T080, T083
- 其余所有 0 分 + 未测 + ~15 估算 + N/A 场景全部重新测试
- 测试前必须确认 RM 已关闭、LOADTEST 已停止

## 排除清单（8 个不测）

| # | 场景 | 排除原因 |
|---|------|---------|
| T041 | Free Buffer Waits+DBWR慢 | 后台进程(DBWR写慢) |
| T056 | Log File Sync存储IO | 后台进程(LGWR写慢) |
| T060 | 归档延迟+备库 | Data Guard |
| T062 | LGWR写延迟+提交堆积 | 后台进程(LGWR写慢) |
| T069 | 网络延迟偏移 | RAC/多节点 |
| T072 | DBWR写入慢 | 后台进程(DBWR写慢) |
| T080 | Resource Manager限流 | RM |
| T083 | 后台进程等待异常 | 后台进程 |

---

## Batch A — SQL 执行计划类（13 个）

无特殊环境要求，构造 SQL 即可。

### A1: 全表扫描/索引/计划

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T018 | 执行计划漂移(统计信息) | 1. 创建表+索引，收集统计信息让优化器走索引<br>2. `DBMS_STATS.SET_TABLE_STATS` 篡改 NUM_ROWS 使计划翻转为全扫<br>3. 30 并发跑该 SQL | plan_change_count>0, full_scan_rate升高 |
| T019 | 执行计划漂移(ACS) | 1. 高倾斜表(type='A' 99%, 'B' 1%)+直方图<br>2. `CURSOR_SHARING=FORCE`<br>3. 先跑 type='A'(全扫计划), 再跑 type='B'(应走索引但被锁定) | 同 SQL_ID 多 child cursor |
| T027 | 分区裁剪失效 | 1. 创建 RANGE 分区表(按日期)<br>2. `SELECT * FROM t WHERE TO_CHAR(dt,'YYYYMMDD')='20260101'`<br>3. 30 并发 | 执行计划显示 PARTITION RANGE ALL |
| T031 | 不必要排序 | 1. `SELECT * FROM io_test ORDER BY col1,col2,col3,col4 FETCH FIRST 10 ROWS ONLY`<br>2. 30 并发（排序百万行只取10行） | temp_used_pct 升高, cpu_sessions 升高 |
| T032 | 嵌套视图放弃合并 | 1. 创建 3 层嵌套视图: V3→V2→V1→表<br>2. 每层 DISTINCT+GROUP BY<br>3. 查询 V3，30 并发 | 执行计划含 VIEW 操作(未合并), cpu_sessions高 |
| T033 | 并行查询降级 | 1. `ALTER SYSTEM SET PARALLEL_MAX_SERVERS=8`<br>2. 同时启动 5 个 `SELECT /*+ PARALLEL(8) */ * FROM io_test` | 实际 DOP<请求 DOP |
| T034 | SQL回退(新索引致差计划) | 1. 小表(1万行), 全扫本来很快<br>2. 添加选择性差的索引(status 只有2值)<br>3. 查询 `WHERE status='A'`，优化器选了索引但更慢 | plan_change_count>0, elapsed_time增大 |
| T081 | 隐式类型转换 | 1. VARCHAR2 列有索引<br>2. `SELECT * FROM t WHERE varchar_col=123`(数字)<br>3. 30 并发 — 索引无法使用 | full_scan_rate高, cpu_sessions高 |
| T054 | 段空间浪费(HWM) | 1. io_test 表(5M行1.2GB)<br>2. `DELETE FROM io_test WHERE ROWNUM<=4500000`(删90%)<br>3. `SELECT COUNT(*) FROM io_test` 仍然全扫 1.2GB 块 | 物理读与行数不匹配 |
| T028 | SPM锁定次优计划 | 1. 表无索引, 执行 SQL 走全扫<br>2. `DBMS_SPM.LOAD_PLANS_FROM_CURSOR_CACHE` 锁定全扫计划<br>3. 添加索引, 再跑 SQL — 计划不变(SPM锁定) | plan不变，baseline ACCEPTED |

### A2: PGA/排序/溢出

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T022 | 大排序溢出TEMP | 1. `ALTER SESSION SET WORKAREA_SIZE_POLICY=MANUAL`<br>2. `ALTER SESSION SET SORT_AREA_SIZE=1048576`(1MB)<br>3. `SELECT * FROM io_test ORDER BY col1,col2` — 1.2GB 排序溢出 | temp_used_pct 升高, "direct path read/write temp" |
| T023 | Hash Join溢出 | 1. 同上缩 PGA<br>2. `SELECT * FROM io_test a JOIN io_test b ON a.id=b.id` | one-pass/multi-pass 操作 |
| T039 | PGA溢出TEMP | 同 T022，多路并发 | pga_used_pct>95%, temp_used_pct升高 |
| T025 | PL/SQL逐行处理 | ```sql<br>BEGIN FOR r IN (SELECT * FROM io_test) LOOP<br>  INSERT INTO target VALUES(r.id,...);<br>  COMMIT;<br>END LOOP; END;``` | long_sql, cpu_sessions, commit_rate极低 |

### A3: 硬解析/SP

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T038 | ORA-04031 | 1. 缩小 SHARED_POOL_SIZE (需 PDB 级别调整或 session 级别)<br>2. 大量不同 literal SQL<br>3. 触发 ORA-04031 | shared_pool_free_pct<5%, alert_log ORA-04031 |

---

## Batch B — 容量/存储类（8 个）

通过缩小表空间或填充数据模拟，不依赖 IO 速度。

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T047 | TEMP满 | 1. 创建小 TEMP 表空间(100MB)<br>2. 切换 testuser 使用该 TEMP<br>3. 多路大排序填满 | temp_used_pct≥95% |
| T048 | UNDO不足 | 1. 创建小 UNDO 表空间(50MB), 切换为当前 UNDO<br>2. 开长事务(大 UPDATE 不提交)<br>3. 其他会话并发 DML | undo_used_pct≥95%, ORA-30036 |
| T051 | ORA-01555 | 1. `ALTER SYSTEM SET UNDO_RETENTION=30`<br>2. Session A: `SELECT DBMS_SESSION.SLEEP(120) FROM io_test`(长查询2分钟)<br>3. Session B~T: 高频 UPDATE io_test(覆盖 UNDO) | ORA-01555 in alert log |
| T053 | Datafile到上限 | 1. 创建表空间 datafile MAXSIZE 200M<br>2. 持续 INSERT 直到满 | ORA-01654, tablespace_used_pct≥95% |
| T063 | LOB膨胀 | 1. 创建含 CLOB 列的表<br>2. 循环 `UPDATE SET clob_col=DBMS_RANDOM.STRING('x',32000)`<br>3. 旧版本不回收 → 表空间增长 | tablespace_used_pct上升趋势 |
| T049 | FRA满 | **需先开归档模式**<br>1. 设小 FRA (500MB)<br>2. 大量 DML 产生归档<br>3. 不删归档 | fra_used_pct≥95% |
| T055 | 归档目录空间 | **需归档模式**, 同 T049 | archive_lag_sec>300 |
| T061 | 归档模式大批量 | **需归档模式**<br>`INSERT INTO t SELECT * FROM io_test`(百万行) | redo_rate>80000KB/s |

---

## Batch C — Redo/日志类（4 个）

通过缩小 Redo Log 模拟，不需要慢 IO。

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T050 | Redo频繁切换 | 1. 添加新 redo log group (50MB×3)<br>2. 切换到新 group, 删除旧的大 group<br>3. 持续大量 DML | log_switch_rate>30/h |
| T057 | Log File Sync高频提交 | 1. `BEGIN FOR i IN 1..1000000 LOOP INSERT INTO t VALUES(i); COMMIT; END LOOP; END;`<br>2. 逐行提交 — LGWR 排队 | log_file_sync>5000us, commit_rate>5000/s |
| T059 | Redo Log Space Wait | 1. 3 组 50MB redo + 持续大量 DML<br>2. 写速度超过 checkpoint → 全部 ACTIVE | redo_log_space_wait>5 |
| T065 | Log Switch抖动 | 1. 50MB redo log<br>2. 持续中等 DML<br>3. 每 3 分钟 log switch + checkpoint IO 抖动 | active_sessions 周期性 spike |
| T088 | LOG_BUFFER过小 | 1. 缩小 LOG_BUFFER (需重启)<br>2. 高频大量 DML | 等待 "log buffer space" |

---

## Batch D — 内存/SGA 类（3 个）

需要调整 SGA 组件大小，影响全局但可恢复。

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T037 | Buffer Cache太小 | 1. 缩小 DB_CACHE_SIZE (ASMM 下设 MIN)<br>2. 多路 OLTP 查询不同数据 | buffer_cache_hit_pct持续<90% |
| T043 | Result Cache失效 | 1. `ALTER SESSION SET RESULT_CACHE_MODE=FORCE`<br>2. 表同时高频 SELECT(用缓存) + UPDATE(失效缓存) | 等待 "Result Cache: RC Latch" |
| T044 | SGA Auto-resize | 1. 缩小 SGA_TARGET, 不固定组件<br>2. 交替跑大查询(需 buffer cache) 和大量解析(需 shared pool) | V$SGA_RESIZE_OPS 频繁 |

---

## Batch E — 锁场景补充（5 个）

利用大表或 flush cache 延长操作时间。

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T003 | blocker等IO | 1. `ALTER SYSTEM FLUSH BUFFER_CACHE`<br>2. Session A: `UPDATE io_test SET col=1 WHERE id=1`(flush后需读磁盘,产生IO等待)<br>3. Session B~T: 更新同一行等待 | blocker wait_event='db file sequential read' |
| T006 | FK缺索引TM锁 | 1. 父表(10万行)+子表(500万行) FK 无索引<br>2. `DELETE FROM parent WHERE id=1`(需全扫子表检查FK) | 等待 "enq: TM" |
| T007 | DDL阻塞DML | 1. Session A: `BEGIN LOOP UPDATE t SET x=x+1; COMMIT; END LOOP;`<br>2. Session B: `ALTER TABLE t ADD (newcol NUMBER)` → 等 DML 释放 | library cache lock, enq: TM |
| T008 | ITL争用 | 1. `CREATE TABLE itl_test(...) INITRANS 1 PCTFREE 0` (只有1个ITL槽)<br>2. 50 并发更新不同行 → ITL 槽不够 | 等待 "enq: TX - allocate ITL entry" |
| T015 | DDL Lock超时 | 1. `ALTER SESSION SET DDL_LOCK_TIMEOUT=5`<br>2. 长事务运行中循环尝试 DDL → 反复超时重试 | ORA-30006, enqueue_wait_time spike |

---

## Batch F — IO 等待事件（2 个）

使用 flush buffer cache + 大表(>1GB) 产生 IO 等待。

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T068 | db file scattered read | 1. `ALTER SYSTEM FLUSH BUFFER_CACHE`<br>2. `SELECT * FROM io_test`(5M行 1.2GB全扫)<br>3. 并发多路 — 产生 scattered read | db_file_scat_read 等待 |
| T075 | IO Calibration差异 | 1. `DBMS_RESOURCE_MANAGER.CALIBRATE_IO(num_physical_disks=>1, max_latency=>20, max_iops=>out, max_mbps=>out, actual_latency=>out)`<br>2. 对比校准结果与实际 IO 延迟 | 校准数据可供 rule 分析 |

---

## Batch G — 连接/会话类（5 个）

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T076 | 连接冲高 | 脚本循环 `sqlplus connect/disconnect` 500次/分钟 | session_creation_rate>50/s |
| T077 | 会话接近上限 | 1. 查 `SHOW PARAMETER sessions` → 假设 400<br>2. 打开 350+ 并发连接保持 | resource_limit_pct>90% |
| T078 | Aborted Connects | 错误密码批量连接: `for i in {1..200}; do sqlplus wrong/wrong@... &; done` | alert_log 连接拒绝错误 |
| T079 | 会话泄漏(T3) | Python 脚本每 5 秒打开 1 个连接不关闭，持续 5 分钟 | total_sessions T3 趋势上升 |
| T095 | Resource Limit上限 | 同 T077，推到 95%+ | resource_limit_pct>95% |

---

## Batch H — Alert/系统类（6 个）

| # | 场景 | 模拟方法 | 验证点 |
|---|------|---------|-------|
| T092 | Alert Log ORA错误 | 触发多种 ORA 错误(ORA-01555+ORA-04031) 让 alert log 堆积 | alert_log_ora_errors>10/min |
| T093 | Job调度失败 | 1. `DBMS_SCHEDULER.CREATE_JOB` 引用不存在的存储过程<br>2. 设 5 秒执行一次 → 持续失败 | job_failure_rate>50% |
| T096 | 审计开销 | 1. `AUDIT ALL STATEMENTS BY testuser`<br>2. 高频 DML → redo 因审计增大 | redo_rate偏高, cpu略升 |
| T070 | 提交速率骤降(T9) | 1. 正常负载(commit_rate 500/s)运行 2 分钟<br>2. 锁住关键表 → commit_rate 骤降 | commit_rate drop>80% |
| T071 | Enqueue Wait偏移(T7) | 1. 正常负载2分钟(baseline enqueue wait)<br>2. 逐步增加锁持有时间 → 均值漂移 | enqueue_wait_time T7 偏移 |
| T011 | Buffer Busy Wait | 1. 右增长序列主键表<br>2. 100 并发 INSERT → 索引右侧叶块争用 | 等待 "buffer busy waits" |

---

## Batch I — 特殊操作（5 个）

| # | 场景 | 模拟方法 | 风险 |
|---|------|---------|------|
| T085 | 全库Hang | 1. 锁 SYS 对象 + 所有操作等待<br>2. commit_rate=0, active_sessions>30 | **高风险** — 需手动恢复 |
| T094 | Crash Recovery | 1. `shutdown abort` 后 `startup`<br>2. 立即跑负载 — buffer cache 全空 | 比 kill -9 安全 |
| T082 | PX资源争用 | 1. `PARALLEL_MAX_SERVERS=8`<br>2. 5 个 `PARALLEL(8)` 大查询同时跑 | 影响全局并行 |
| T097 | Data Pump Hang | 1. expdp 导出大表<br>2. 同时对该表做 DDL → Data Pump 等待 | 需 Data Pump 权限 |
| T098 | 升级后SQL回退 | 1. `ALTER SESSION SET OPTIMIZER_FEATURES_ENABLE='11.2.0.4'`<br>2. 跑 SQL 对比计划变化 | 仅影响会话 |
| T045 | Large Pool+RMAN | 1. `LARGE_POOL_SIZE=0`<br>2. RMAN 备份 + OLTP 负载 | 需 RMAN |
| T100 | 综合场景 | 同时注入: 锁(T001) + 大 DML(T052) + flush cache(T068) | 多重故障叠加 |

---

## 执行策略

### 前置条件（每次测试前）
```sql
-- 确认 RM 关闭
SELECT VALUE FROM V$PARAMETER WHERE NAME='resource_manager_plan';
-- 确认无干扰进程
SELECT USERNAME,PROGRAM FROM V$SESSION WHERE USERNAME NOT IN ('SYS','SYSTEM','TESTUSER');
-- 停 LOADTEST
-- pkill -f LOADTEST
```

### 优先级排序

| 优先级 | Batch | 场景数 | 理由 |
|--------|-------|--------|------|
| **P0** | E(锁补充) + F(IO) | 7 | 已有规则覆盖，最可能直接拿高分 |
| **P1** | B(容量) + C(Redo) | 12 | 容量规则(T046=80)和Redo规则(T091=70)已证明有效 |
| **P2** | G(连接) + H(Alert) | 11 | 需新增规则但模拟简单 |
| **P3** | A(SQL计划) | 13 | 需 Phase 3 SQL 诊断能力，可能需开发新规则 |
| **P4** | D(SGA) + I(特殊) | 10 | 需全局参数变更或特殊操作 |

### 归档模式预备

T049/T055/T061 需要归档模式，开启步骤：
```sql
SHUTDOWN IMMEDIATE;
STARTUP MOUNT;
ALTER DATABASE ARCHIVELOG;
ALTER DATABASE OPEN;
-- 设小 FRA
ALTER SYSTEM SET DB_RECOVERY_FILE_DEST_SIZE=500M;
```

### 闭环流程（每个场景）

```
1. 注入故障（按模拟方法）
2. 确认故障达成（检查等待事件/指标）
3. opendb -c oracle '/rule live'
4. 记录 rule 输出
5. 清理故障
6. 如得分<70 → 分析差距 → 开发/优化规则 → 重测
```

### 预期产出

- P0+P1: 19 个场景, 预期 12+ 个拿到 ≥50 分（已有规则基础）
- P2+P3: 24 个场景, 大部分需新增规则, 首轮预期 0-30 分 → 迭代后 50+
- P4: 10 个场景, 逐步扩展

**总目标**: 将有诊断均分从 65.7 提升到 70+, ≥50 分场景从 33 个增加到 55+
