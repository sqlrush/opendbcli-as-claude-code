# E2E Sentinel + Diagnose 全链路验证日志

**日期**: 2026-03-13 13:31~13:34
**环境**: Mac → Oracle 19.3 (8.160.176.23:1521/orcl) + 本机 Ollama/qwen3.5:9b
**流量**: load_storm.py (heavy=4, io=4, dml=4, lock=4, duration=300s)

---

## Step 1: Oracle 连接

```
[13:31:54] ╔═══════════════════════════════════════════════════════╗
[13:31:54] ║  OpenDB E2E: Sentinel + Diagnose 全链路验证          ║
[13:31:54] ╚═══════════════════════════════════════════════════════╝

▶ Step 1: 连接 Oracle
  ✓ 已连接 — 8.160.176.23:1521/orcl
  版本: 19.3.0.0.0, 实例: orcl
```

## Step 2: Sentinel 基线采集 (6样本, 1s间隔)

```
▶ Step 2: 采集基线 (6样本, 1s间隔)
  [1] active=12 db%=0.0    TPS=0    QPS=0      REDO=0KB/s
  [2] active=11 db%=1324.5 TPS=807  QPS=32338  REDO=20500KB/s
  [3] active=13 db%=629.1  TPS=108  QPS=5805   REDO=2854KB/s
  [4] active=10 db%=998.3  TPS=1122 QPS=43538  REDO=27056KB/s
  [5] active=14 db%=1012.2 TPS=890  QPS=34413  REDO=22311KB/s
  [6] active=14 db%=1130.6 TPS=3    QPS=2077   REDO=22KB/s
```

## Step 3: 当前快照 (collector.Collect)

```
▶ Step 3: 当前快照
  Sessions: total=103 active=11 cpu=5 io=0 idle=92
  Throughput: TPS=1128 QPS=45278 REDO=29037.3KB/s
  Ratios: db%=1054.5 WTR%=44.1

  活跃会话:
    SID=431   OPENDB_TEST dv6nzuasy66vc PGA memory operation        SELECT COUNT(*), MAX(object_name)...
    SID=722   OPENDB_TEST dv6nzuasy66vc PGA memory operation        SELECT COUNT(*), MAX(object_name)...
    SID=291   OPENDB_TEST dv6nzuasy66vc PGA memory operation        SELECT COUNT(*), MAX(object_name)...
    SID=2134  OPENDB_TEST 6adjt0qpkqn1u SQL*Net message to client   SELECT s.SID, s.USERNAME, s.SQL_ID...
    SID=5     OPENDB_TEST dv6nzuasy66vc PGA memory operation        SELECT COUNT(*), MAX(object_name)...

  等待事件:
    DB CPU                           waits=0        time=15330.7s  pct=87.2%
    enq: TX - row lock contention    waits=9034     time=1867.9s   pct=10.6%
    log file parallel write          waits=10314    time=89.1s     pct=0.5%
    control file sequential read     waits=37044    time=68.3s     pct=0.4%
    buffer busy waits                waits=93056    time=62.0s     pct=0.4%
    log file sync                    waits=7664     time=59.7s     pct=0.3%
    db file parallel write           waits=16879    time=32.4s     pct=0.3%
    PGA memory operation             waits=2758722  time=22.7s     pct=0.1%
```

## Step 4: Burst 突发采集 (8帧, 200ms间隔)

```
▶ Step 4: Burst 采集 (8帧, 200ms)
  帧1: active=11 sessions=6
  帧2: active=12 sessions=6
  帧3: active=15 sessions=12
  帧4: active=15 sessions=11
  帧5: active=13 sessions=8
  帧6: active=12 sessions=6
  帧7: active=14 sessions=10
  帧8: active=13 sessions=9
```

## Step 5: sentinel.Analyze() 分析结果

```
▶ Step 5: 分析
  分类: 锁等待阻塞 (置信度 70%)
  峰值: 15, 基线: 12.3, 持续: 11.4s

  证据 (阻塞链):
    SID 859  阻塞 3 个会话, SQL: , 等待:
    SID 1002 阻塞 3 个会话, SQL: c4z057h5sxjyq, 等待: enq: TX - row lock contention
    SID 1142 阻塞 3 个会话, SQL: 577an47jhb0m8, 等待: enq: TX - row lock contention
    SID 1288 阻塞 3 个会话, SQL: , 等待:
    SID 1995 阻塞 1 个会话, SQL: , 等待:

  Top SQL:
    [0] dv6nzuasy66vc 出现=100% 并发=4 wait=Other
        SELECT COUNT(*), MAX(object_name), MIN(object_id)...
    [1] 0crpt431puujr 出现=100% 并发=1 wait=Network
        SELECT s.SID, s.USERNAME, s.SQL_ID, NVL(s.EVENT, 'ON CPU')...
    [2] a8mc93zuf4rct 出现=75%  并发=3 wait=Application
        UPDATE opendb_lock_test SET val = :1 WHERE id = :2
    [3] c4z057h5sxjyq 出现=62%  并发=1 wait=Application
        UPDATE opendb_load_test SET val = :1 WHERE id BETWEEN :2 AND :3
    [4] 577an47jhb0m8 出现=50%  并发=3 wait=Application
        DELETE FROM opendb_load_test WHERE id < :1

  等待分布:
    CPU           74.0%
    Application   24.7%
    System I/O     0.7%
    Commit         0.4%
    Other          0.2%
    Concurrency    0.1%
```

## Step 6: 修复建议模板 (GetRemediation)

```
▶ Step 6: 修复建议模板
  建议: 确认阻塞源头会话正在执行的操作
  建议: 检查是否有未提交的长事务 (idle in transaction)
  建议: 评估是否可以 kill 阻塞源头会话: ALTER SYSTEM KILL SESSION 'sid,serial#'
  建议: 检查应用层是否存在锁升级或锁顺序不一致问题
  建议: 考虑缩小事务粒度, 减少锁持有时间

  排查SQL:
    1. SELECT s1.sid blocker_sid, s1.username blocker_user, s1.sql_id blocker_sql,
       s2.sid waiter_sid, s2.username waiter_user, s2.sql_id waiter_sql,
       s2.seconds_in_wait wait_sec
       FROM v$session s1 JOIN v$session s2 ON s1.sid = s2.blocking_session
       WHERE s2.blocking_session IS NOT NULL
    2. SELECT object_id, session_id, oracle_username, locked_mode
       FROM v$locked_object lo JOIN dba_objects o ON lo.object_id = o.object_id
    3. SELECT sid, type, id1, id2, lmode, request, block
       FROM v$lock WHERE block = 1 OR request > 0
```

## Step 7: agent.CompressReport() 压缩报告 (LLM 输入)

```
▶ Step 7: 压缩报告
  长度: 1770 字符 (约 885 tokens)

  === 性能异常报告 ===
  触发: active_non_idle=11.0 (基线=12.3, 阈值=24.7, 0.0倍)
  持续: 11.4s, 峰值活跃: 15, 基线活跃: 12.3
  采集帧数: 8

  --- 根因判定 ---
  类型: 锁等待阻塞 (lock_contention)
  置信度: 70%
    - SID 859 阻塞 3 个会话
    - SID 1002 阻塞 3 个会话, SQL: c4z057h5sxjyq, 等待: enq: TX - row lock contention
    - SID 1142 阻塞 3 个会话, SQL: 577an47jhb0m8, 等待: enq: TX - row lock contention
    - SID 1288 阻塞 3 个会话
    - SID 1995 阻塞 1 个会话

  --- 指标摘要 ---
    db%:     avg=959.1  max=1394.2 min=499.9  trend=stable
    wtr%:    avg=21.2   max=50.1   min=1.6    trend=spike
    tps:     avg=564.6  max=1111.6 min=2.4    trend=rising
    qps:     avg=22984  max=44417  min=1157   trend=rising
    redo_kb: avg=14057  max=27444  min=6.4    trend=rising
    active:  avg=13.1   max=15.0   min=11.0   trend=stable

  --- 等待分布 ---
    CPU:         74.0% (122124ms)
    Application: 24.7% (40693ms)
    System I/O:   0.7% (1152ms)
    Commit:       0.4% (622ms)
    Other:        0.2% (251ms)

  --- Top SQL ---
    dv6nzuasy66vc: 出现率=100% 并发=4 wait=Other
    0crpt431puujr: 出现率=100% 并发=1 wait=Network
    a8mc93zuf4rct: 出现率=75%  并发=3 wait=Application elapsed=4.0s
      SQL: UPDATE opendb_lock_test SET val = :1 WHERE id = :2

  --- 阻塞链 ---
    SID 859  → 3 个受害者
    SID 1002 → 3 个受害者, SQL=c4z057h5sxjyq, 事件=enq: TX - row lock contention
    SID 1142 → 3 个受害者, SQL=577an47jhb0m8, 事件=enq: TX - row lock contention
```

## Step 8: LLM 提示构建

```
▶ Step 8: 构建 LLM 提示

  System Prompt (547 chars):
  ────────────────────────────────────────
  你是 OpenDB 数据库诊断专家。你的任务是分析 Oracle 数据库性能异常并给出诊断建议。

  规则:
  1. 始终使用中文回复
  2. 先分析数据, 再给出结论
  3. 建议要具体可操作, 包含 SQL 或参数
  4. 区分紧急措施(止血)和根因修复

  模式: playbook (单次分析)
  - 你只有一次回复机会, 不可调用任何工具
  - 基于提供的异常报告直接给出分析和建议
  - 输出格式:
    ## 根因分析
    [分析内容]
    ## 紧急措施
    [止血方案]
    ## 根因修复
    [长期方案]
  ────────────────────────────────────────

  User Prompt (1913 chars):
  ────────────────────────────────────────
  用户诊断请求: 数据库活跃会话突增，请诊断根因并给出修复建议

  当前异常报告:
  [压缩报告全文，即 Step 7 的内容]

  请分析并给出诊断建议。
  ────────────────────────────────────────
```

## Step 9: Ollama 调用日志

```
▶ Step 9: 调用 Ollama (qwen3.5:9b)

  测试连通性... ✓ 响应: "Hi there! How can I help you today?"

  ─── LLM 请求 ───
  Messages: 2
    [0] role=system content(547 chars)
    [1] role=user  content(1913 chars)
  ─── 发送请求到 ollama ───

  ─── LLM 响应 (115.0s) ───
  Tokens: input=1042, output=1405
  StopReason: stop
```

## Step 10: Qwen 3.5:9B 诊断输出 (完整原文)

```
## 根因分析

根据异常报告，本次活跃会话突增的根本原因是 **TX行锁竞争(lock_contention)**：

1. **阻塞特征明确**：`enq: TX - row lock contention` 是典型的行级锁等待事件，
   表明多个会话在尝试修改同一行数据时发生冲突。SID 1002 和 SID 1142 各阻塞 3 个会话，
   形成明显的阻塞链。

2. **瓶颈SQL定位**：Top SQL 中 `UPDATE opendb_lock_test SET val = :1 WHERE id = :2`
   出现率 75%、耗时 4.0s，该表是锁竞争的主要来源，高并发 UPDATE 同一主键范围触发行锁等待。

3. **业务场景判断**：事务量(trend=rising)、TPS 上升(1.6倍)，说明更新频率增加但未适配
   锁策略。CPU 74% 占用较高但不是瓶颈，排除计算密集型问题。

4. **活跃会话突增诱因**：阻塞导致其他 SQL 等待在 `enq: TX` 上无法结束，
   累积后使非空闲会话数暂时升高。

5. **表结构风险**：无索引或单行热点更新未优化，高并发下易形成"锁风暴"。

## 紧急措施（止血方案）

-- 1. 查找阻塞会话并清理
SELECT sid, serial#, sql_id, event, blocking_session,
       seconds_in_wait, wait_time
FROM v$session s
WHERE s.event LIKE '%enq: TX%'
AND s.blocking_session IS NOT NULL;

-- 2. 终止非核心业务会话（谨慎操作）
ALTER SYSTEM KILL SESSION 'sid,serial#';

-- 3. 检查是否有长事务未提交
SELECT sql_id, sid, serial# FROM v$session
WHERE last_load_time < sysdate - 1/60;

如果阻塞是偶发的，在高峰过后阻塞会话会自动释放。
如果是持续阻塞，则需要立即干预以恢复系统。

## 根因修复（长期方案）

### 一、SQL索引优化
CREATE INDEX idx_opendb_lock_test_id ON opendb_lock_test(id);

### 二、更新SQL逻辑优化
- 减少事务持有锁时间：在 UPDATE 语句后尽快提交，避免长事务
- 分批处理机制：使用批处理而不是全表一次性更新
BEGIN
  FOR c IN (SELECT id FROM opendb_lock_test WHERE condition) LOOP
    UPDATE opendb_lock_test SET val = :1 WHERE id = c.id;
    COMMIT;
  END LOOP;
END;

### 三、锁等待监控配置
ALTER SESSION SET events '10235:trace name=lock_trace, level=3';

### 四、架构级改进
- 隔离级别调整：设置为 READ COMMITTED 以减少幻读
- 分区表策略：按主键值进行范围分区
- 并发控制优化：使用乐观锁替代悲观锁
```

## 最终统计

```
▶ Step 10: 诊断结果
  耗时: 115.0s, 轮次: 1 (playbook 模式)
  Tokens: input=1042, output=1405

✓ E2E 验证完成
```

---

## 调用链路总结

```
流量生成 (load_storm.py, 16 workers)
  → Collector.Collect() × 6 (基线采集)
  → Collector.Collect() × 1 (当前快照)
  → Collector.SetMode(ModeBurst)
  → Collector.Collect() × 8 (突发采集, 200ms间隔)
  → sentinel.Analyze() → 分类: lock_contention, 70%
  → sentinel.GetRemediation() → 修复建议模板
  → agent.CompressReport() → 1770 chars, ~885 tokens
  → agent.SystemPromptForDiagnose(ModePlaybook) → 547 chars
  → agent.DiagnoseUserPrompt() → 1913 chars
  → ollama.Chat() → qwen3.5:9b, 115.0s
  → agent.FormatDiagnoseResult() → 格式化输出
```
