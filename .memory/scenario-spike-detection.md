---
name: scenario-spike-detection
description: 场景方案 — 哨兵探测(3σ) → 爆发采集(200ms/帧) → 聚合分析 → Qwen 深挖 → 可执行建议。含已实现6类+待实现7类根因场景
type: project
---

# 场景：性能异常自动诊断

## 已实现的 6 类根因场景

| # | RootCauseType | 显示名 | 触发信号 | 诊断依据 |
|---|------|--------|----------|----------|
| 1 | CauseLockContention | 锁等待阻塞 | lock_sessions 冲高 | BlockingChain 受害者 ≥ 2 |
| 2 | CauseBadSQL | SQL并发冲高 | active/cpu/long_sql 冲高 | 单 SQL 出现率 >50%，并发 ≥ 3 |
| 3 | CauseRedoBottleneck | Redo冲高 | redo_rate 冲高 | Commit 类等待 >20% |
| 4 | CauseLatchStorm | Latch争用冲高 | hard_parse_rate 冲高 | Concurrency 类等待 >25% |
| 5 | CauseIOSubsystem | 存储I/O冲高 | io_sessions 冲高 | I/O 类等待 >40% |
| 6 | CauseTrafficStorm | 流量冲高 | active_sessions 冲高 | active spike + SQL 分散 + 无阻塞 |

## 待实现的 7 类根因场景

| # | 根因 | 显示名 | 触发信号 | 诊断依据 | 优先级 |
|---|------|--------|----------|----------|--------|
| 7 | CauseMemoryPressure | 内存冲高 | PGA/SGA 使用率突增 | PGA memory operation 等待占比高；db_cache 命中率骤降 | P1 |
| 8 | CausePlanDrift | 执行计划漂移 | 单 SQL elapsed 突增但并发不高 | 同 sql_id 的 plan_hash_value 在 burst 期间与历史不同 | P0 |
| 9 | CauseConnectionExhaust | 连接数冲高 | sessions 逼近 processes 上限 | v$resource_limit sessions 使用率 >85% | P1 |
| 10 | CauseArchiveDelay | 归档冲高 | archive_sessions 出现 | log file switch (archiving needed) 等待 | P2 |
| 11 | CauseDGSyncDelay | DG同步冲高 | log file sync 冲高 | 主库 log file sync 异常 + 备库 apply lag 增大 | P2 |
| 12 | CauseUndoPressure | Undo冲高 | 长事务/undo 空间 | enq: US - contention 等待；undo 使用率 >85% | P2 |
| 13 | CauseTempPressure | TEMP冲高 | temp 使用率突增 | direct path read/write temp 等待冲高 | P2 |

### 实现思路（不改架构）
1. 探针扩展（probe.go）— 新增需要的轻量指标
2. 分类规则（classify.go）— 在优先级链中插入新规则
3. Remediation 模板（remediation.go）— 对应的排查 SQL（LLM 模式用）

## 整体架构

```
┌─────────────────────────────────────────────────────────────────┐
│                    OpenDB Sentinel Mode                         │
│                                                                 │
│  ┌──────────┐     ┌──────────────┐     ┌───────────────────┐   │
│  │ Phase 1  │     │   Phase 2    │     │     Phase 3       │   │
│  │ 哨兵探测  │────▶│  爆发采集     │────▶│   分析 + 建议      │   │
│  │ (低成本)  │触发  │ (高频细粒度)  │结束  │  (Qwen 介入)      │   │
│  └──────────┘     └──────────────┘     └───────────────────┘   │
│                                                                 │
│  1次轻量SQL/秒    200ms/帧 × 30秒     聚合 → 压缩 → AI 分析    │
│  成本≈0           ≈150帧详细数据       → 可执行的命令行输出      │
└─────────────────────────────────────────────────────────────────┘
```
