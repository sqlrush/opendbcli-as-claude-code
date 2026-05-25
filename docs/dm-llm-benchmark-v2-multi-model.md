# DM /llm 多模型回归 Benchmark

**日期**：2026-05-02
**dbaa 版本**：v1.1.23 + DMProfile 强化（含本会话 4 处真机修复 + 等待事件分析路径 + 视图列名陷阱）
**测试机**：47.251.30.180:5237 (DM 8.1.4.200)
**故障场景**：当前实例（含 766 累计死锁 + 770 累计错误 + 1000 长 SQL，无现场负载）
**问题**：诊断当前 DM 实例最近 1 小时的健康状况，给出根因和具体修复 SQL

## 评测结论（4 模型 × 1 故障）

| 模型 | 时长 | 轮数 | 收敛 | 区分累计/实时 | 用 errcode | 用 SP_CLOSE_SESSION | 综合分 |
|---|---:|---:|:---:|:---:|:---:|:---:|---:|
| **glm-5.1** | 158s | 5 | ✅ 最快 | ✅✅ 明确 | ✅ | N/A | **9.5** |
| **deepseek-v4-pro** | 309s | 19 | ✅ | ⚠️ 隐含 | ❌ | N/A | **8.5** |
| **moonshot-v1-128k** | 33s | 1 | ❌ 太浅 | ❌ 累计当现场 | ❌ | ❌ | **3.0** |
| **kimi-k2.6** | 605s+ | 12+ | ❌ 客户端超时 | ❓ 未给最终 | ✅ | ❓ | **0.0** (timeout) |

**有效平均分**：(9.5 + 8.5 + 3.0) / 3 = **7.0/10**（除 timeout）
**全样本**：(9.5 + 8.5 + 3.0 + 0) / 4 = **5.25/10**

vs 之前单模型 deepseek 8.25 — 多模型场景中位数下来，主要是模型能力差异。

## 各模型详细评估

### 1. glm-5.1（最强表现）

**优点**：
- 5 轮即收敛（最快）
- 完整证据汇总表（指标 / 数据 / 来源工具 三列）
- 准确区分"累计 766 死锁"vs"最近 1h 0 死锁"——彻底拒绝把累积值当现场
- 调用 errcode skill 拿到 `-6403 死锁错误码 HIT_COUNT=766`
- 识别 AB-BA 死锁模式 + DBMS_LOCK.SLEEP 拉大持锁窗口
- 提供 3 修复方案（统一锁序 / 移除 SLEEP / 合并语句）
- **结尾自带证据溯源自检表**（每个结论标注来源 + 一致性 ✅）

**唯一瑕疵**：建议查 V$SQLAREA（这个是 Oracle 视图，DM 没有，应是 V$SQL_HISTORY）

### 2. deepseek-v4-pro（深度强但慢）

**优点**：
- 19 轮深挖（覆盖 health/info/alert/anomalies/slowsql/errcode/waits/topsql/deadlock/explain/tableinfo/locks/blocktree/views）
- 完整诊断报告（关键证据汇总 / 紧急措施 / 根因修复 / 总结）
- 识别 BENCH_DM_A / BENCH_DM_B 双表 AB-BA 模式
- 给 3 方案（统一加锁顺序 / SELECT FOR UPDATE WAIT / 加索引）
- 调用 views skill 3 次 + memory_write

**缺点**：
- 没用 errcode skill 拿错误码 hit count
- "累计 vs 实时"区分隐含但不显式
- 19 轮太重，时长 5 分钟

### 3. moonshot-v1-128k（浅且错）

**问题**：
- 1 轮就出最终诊断（仅调 health）
- **完全把累计值当现场**："最近 1 小时出现频繁死锁" — 实际是累计 5 小时数据
- 推荐 SQL 多处错：
  - `v$sql` (DM 没这个视图，应是 V$SQL_HISTORY)
  - `v$deadlock` (应是 V$DEADLOCK_HISTORY)
  - `last_active_time > SYSDATE - INTERVAL '1' HOUR` (DM 不支持 INTERVAL 语法，应是 SYSDATE - 1/24)
- 没具体 sess_id，给的方案泛化

**结论**：moonshot-v1-128k 在 DM 场景下能力不足，不建议生产用。

### 4. kimi-k2.6（客户端超时未完成）

**行为**：
- 12 轮工具调用（health/alert/anomalies/waits/slowsql/topsql/deadlock/activesessions/locks/blocktree/sql×N/tableinfo/explain/views/memory_write）
- 605s 后命中 `MaxDiagnosisTimeout=10min`，引擎主动终止
- **未给最终诊断报告**

**根因**：
- kimi 单轮平均 ~50s（thinking 模式较慢）
- 12 轮 ≈ 10 分钟，挤爆引擎超时预算
- 不是 prompt bug，是模型 + 硬超时配置组合问题

**改进选项**：
- 选项 A: 提高 `MaxDiagnosisTimeout` 到 15min（影响所有模型）
- 选项 B: 检测到 kimi 时单独放宽（按模型配超时）
- 选项 C: 不修，让 kimi 自然命中超时（用户感知"模型不适合复杂诊断"）

## 回归验证结论

✅ **DMProfile 杀会话约束生效** — 4 个模型无一个用 `ALTER SYSTEM KILL SESSION`（Oracle 语法）
✅ **新 skill 被调用** — anomalies (glm/deepseek/kimi) / errcode (glm/kimi) / views (deepseek/kimi)
✅ **真机修复列名生效** — errcode 返回 `CODE/ERRINFO`，views 返回 380+ 视图，glm/kimi 都正确读取
⚠️ **累计/实时区分** — 只有 glm 完美执行，moonshot 完全失败，deepseek 隐含理解
❌ **kimi 在 600+s 长诊断下命中超时** — 已知行为，不是回归

## 总体评估

- **glm-5.1** 在 DM 诊断上达到 9.5 分，是当前 4 模型里最优选择，建议设为 dbaa DM 默认模型
- **deepseek-v4-pro** 深度好但慢，适合复杂 case
- **moonshot-v1-128k** 不适合 DM（视图名错、累积当现场）
- **kimi-k2.6** 在轻量场景可用，复杂场景命中超时

**Tier 1 不对称帮助原则**（summary banner 直接复读）在 glm 表现最佳，证明 DMProfile + skill summary 修复方向正确。

## 测试记录

- 故障状态：当前实例无现场负载（active=1, blocked=0），仅累计 V$SQL_HISTORY 含 766 死锁 / 770 错误
- 测试问题：相同 prompt 跑 4 次（每次切 active_model 重启 dbaa）
- 输出捕获：`/tmp/dm_bench_results/{model}.log` 在测试机
- 本地副本：`/tmp/dm_bench_local/{model}.log`
