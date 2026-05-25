# DM /llm 4 故障场景诊断质量评测

**日期**：2026-05-01
**模型**：deepseek-v4-pro (thinking, large capability)
**dbaa 版本**：v1.1.23 (含 task 73 summary banner 修复)
**测试机**：47.251.30.180:5237 (DM 8.1.4.200)

## 评测结论

| 故障 | 轮数 | 时长 | 根因识别 | 量化数据 | 具体 PID/SQL_ID | DM 语法正确 | 综合分 |
|---|---:|---:|:---:|:---:|:---:|:---:|---:|
| **F1 hot row UPDATE** | 14+ | (timeout) | ✅ | ✅ | 部分 | ⚠️ 泛指 | 7.5 |
| **F2 missing index** | 17 | 7.5min | ✅ | ✅ | ✅ | ⚠️ Oracle KILL | **9.5** |
| **F3 long tx + lock** | 19 | 4.4min | ⚠️ 部分 | ✅ | ❌ | ⚠️ | 7.0 |
| **F4 deadlock 反向 UPDATE** | 9 | 2.6min | ✅✅ | ✅ | ✅✅ 含 PID | ✅ `SP_CLOSE_SESSION` | **9.0** |

**平均分 8.25 / 10** — DM /llm 已达可用标准（>7 分）。

## 4 故障详细评估

### F1 hot row UPDATE（30 worker 抢同一行 UPDATE）

**LLM 抓到**：91 锁 / 50 阻塞链，"hot row 锁争用"
**优点**：识别 P0/P1/P2 优先级表 (5 项)
**缺点**：未给出具体 `SP_CLOSE_SESSION(<sess_id>)` PID，停在"建议 kill 持有锁的长事务会话"
**原因**：F1 测试时 task 73 summary banner 还没加；改进后预期能给出具体 PID

### F2 missing index（缺索引全表扫 600 万行）

**LLM 抓到**：
- 根因：`STATUS 列缺索引` → `SELECT COUNT(*) WHERE status=3` 全扫
- 量化：4977 次执行 / 总 124611 秒 / 平均 25 秒
- 修复：`CREATE INDEX idx_bench_users_status ON opendb.bench_dm_users(status)`
- 风险评估 + 回滚 + 方案二（计数器表）
- 完整 5 维证据表 + 因果链

**唯一瑕疵**：紧急措施段写了 `ALTER SYSTEM KILL SESSION '<sess_id>'` (Oracle 语法)
- DM 应是 `CALL SP_CLOSE_SESSION(<sess_id>)`
- 原因：activesessions skill 没加 `kill_cmd:` summary 条目，模型 fallback Oracle 知识

### F3 long tx + lock（5 worker 持锁 30s）

**LLM 抓到**：5 并发 UPDATE 现象 + CREATE INDEX 建议
**问题**：把 F2 残留的 `COUNT(*) WHERE status=3` 历史数据（V$SQL_HISTORY 累积值）当成当前问题
- 实际 F3 不跑 COUNT 查询，是 UPDATE 长事务
- 模型分不清"累积统计"和"当前现场"

**根本原因**：DM 工具结果（如 topsql）返回 V$SQL_HISTORY 累积值，没有"最近 N 秒"过滤选项。模型只能看到全量历史。
- 这跟之前 OG 测试时观察到的同类问题（design-local-model-optimization Tier 1 待解）一致
- 解决：给 topsql/slowsql 加 `since_minutes` 参数 + summary 标注 "data is cumulative since DB reset"

**未给** `SP_CLOSE_SESSION` 长事务 PID

### F4 deadlock（4×A + 4×B 反向 UPDATE）

**LLM 抓到**（最佳表现）：
- 根因：hot row + 事务内 `DBMS_LOCK.SLEEP` 持锁 + 反向 UPDATE 死锁
- 量化：14 阻塞链 / 累计 139 死锁
- 涉及表：BENCH_DM_A / BENCH_DM_B 准确
- **`CALL SP_CLOSE_SESSION(140304996625128);` 给出具体 PID + DM 正确语法**
- 修复方案：去掉事务内 SLEEP，附 ❌/✅ 对比代码

**唯一瑕疵**："Oracle 自动检测并回滚" — 应该是"DM 自动检测"

**为何 F4 表现最佳**：
1. 死锁现象明显（V$DEADLOCK_HISTORY 直接给出，summary 显示 deadlock_total 数据）
2. 阻塞链直接 join 出 sess_id（blocktree skill summary 含 `kill_blocker_cmd: CALL SP_CLOSE_SESSION(<sess_id>)`）
3. 9 轮就收敛（vs F2 17 轮 / F3 19 轮）

## summary banner 真实价值证据

| skill summary 字段 | LLM 复读情况 |
|---|---|
| `blocktree.kill_blocker_cmd: CALL SP_CLOSE_SESSION(<id>)` | ✅ F4 直接复读 |
| `topsql.hottest_sql_id` | ✅ F2 复读 SQL_ID 2353 |
| `topsql.hottest_avg_time_ms` | ✅ F2 复读 25 秒 |
| `locks.blocked_count` | ✅ F4 复读 7/8 阻塞 |
| `info.role: PRIMARY` | ✅ F1 复读 |

**结论**：summary banner 的 "key: 具体值" 模式让中型模型也能直接复读具体值，证明 docs/design-local-model-optimization.md Tier 1 不对称帮助原则有效。

## 待优化项（按 ROI 排序）

### P0（立即修复）

1. **activesessions / sessions skill summary 加 `kill_cmd: CALL SP_CLOSE_SESSION(<sess_id>)`**
   - 让 LLM 在所有"会话级"场景都能给出 DM 正确语法
   - F1/F3 的"未给具体 PID" 痛点
2. **DMProfile.SystemPromptRules 加硬约束**：
   - "杀会话**只能**用 `CALL SP_CLOSE_SESSION(<sess_id>)`，**禁止** `ALTER SYSTEM KILL SESSION` (Oracle)"
   - 避免 F2 反例

### P1（中期改进）

3. **topsql / slowsql / waits 标注"累积值/实时值"**
   - summary 加 `data_window: cumulative since reset`
   - 解决 F3 把历史数据当现场的问题
4. **加 `/ash` 或 `/recent_sql` skill** 拿"最近 N 分钟"实时 SQL 数据
   - 区分实时 vs 累积

### P2（远期）

5. **Sentinel skill 接入** — 启动诊断时拿"当前异常上下文"
6. **错误码字典 skill** (V$ERR_INFO) — 让 LLM 解读 ERR-9042 等具体错误

## 已修复 (2026-05-01 commit 1ee2d8a5)

针对 F2 + F3 痛点的修复已 push 到 dbaa 分支:

1. **DMProfile.SystemPromptRules** 加硬约束:
   - 杀会话 **必须** 用 `CALL SP_CLOSE_SESSION(<sess_id>)`，**禁止** Oracle `ALTER SYSTEM KILL SESSION`
   - 累积值 vs 实时值警告：`topsql/slowsql/waits` 多为累积自上次 reset，不要当成"现在正在发生"
   - 占位符违规检查：sess_id / sql_id / 表名必须给具体值

2. **sessions skill summary** 新增:
   - `data_window: real-time snapshot (V$SESSIONS)`
   - `kill_session_syntax: CALL SP_CLOSE_SESSION(<sess_id>)`

3. **activesessions skill summary** 新增 (前提是有活跃会话):
   - `oldest_active_sess_id` / `oldest_active_user` / `oldest_active_elapsed_sec`
   - `oldest_active_sql_head`
   - `kill_oldest_cmd: CALL SP_CLOSE_SESSION(<具体sess_id>)`

**待回归验证** (SSH 临时受限，暂不能立即 retest):
- F2 复跑：确认不再 fallback Oracle `ALTER SYSTEM KILL SESSION`
- F3 复跑：确认能区分 V$SQL_HISTORY 累积值与当前 V$SESSIONS 实时

## 编辑历史

- 2026-05-01: 初版 — F1-F4 实测 + 评分
- 2026-05-01: 增补 — 已修复段，记录 DMProfile + sessions/activesessions summary 三处改动
