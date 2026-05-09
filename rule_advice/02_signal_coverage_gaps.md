# 信号覆盖缺口分析

## 问题：触发指标与诊断结论脱节

当前架构的核心缺陷：**信号提取阶段提取了触发指标 signal，但没有规则注册这些 signal**，
导致最终诊断由不相关的等待事件规则主导。

## 缺口清单

### 1. redo_rate metric — 无规则覆盖 ❌

**现象**: redo_rate spike 触发，但诊断结论是 SQ contention（等待事件主导）

**信号链路**:
```
Signal{Metric, "redo_rate"} → index.byMetric["redo_rate"] → 空
Signal{Category, "redo"}    → index.byCategory["redo"]    → 空
  (ORA_162 category 是 "storage"，不是 "redo"；signals 只有 log file switch/sync)
Signal{WaitEvent, "enq: sq - contention"} → WE2-007b → 命中
```

**结果**: 只有 WE2-007b 产出诊断 → 自动成为 Primary → 诊断偏离触发指标

**实际案例** (2026-03-25):
- 触发: redo_rate 20044.8→18643.4 KB/s
- 等待事件: enq: SQ - contention 68.3%, buffer busy waits 13.5%
- 诊断输出: "序列 CACHE 过小或 NOCACHE 导致 SQ 争用"
- 问题: SQ 争用不产生 redo，redo 来源是大量 DML，SQ 争用只是 DML 的下游瓶颈

### 2. Resolver 不考虑触发指标相关性 ❌

**现象**: 即使有多条规则产出诊断，Resolver 也不会优先选择与触发指标相关的那条

**当前排序公式**: `Score = severityWeight × confidence × specificity`

**缺失**: 没有 "trigger relevance" 维度。一个和 redo_rate 完全无关的诊断，
只要 severity/confidence/specificity 够高，就能成为 Primary。

### 3. ORA_162 category 标注错误

**现状**: `"category": "storage"`
**问题**: redo 相关的 category signal 是 `"redo"`（来自 Sentinel CauseRedoBottleneck），
但 ORA_162 注册在 `"storage"` 下，不会被 `"redo"` category signal 命中。

### 4. 等待事件规则的"喧宾夺主"模式

**模式**: 当触发指标是非等待事件类（如 redo_rate、active_sessions、temp_used_pct），
但报告中包含显著等待事件时，等待事件规则必然命中且分高（因为占比大 → severity boost 大），
而指标类规则缺失 → 诊断被等待事件主导。

**受影响的触发指标**:
- redo_rate → 被等待事件规则抢占
- active_sessions → 可能被等待事件规则抢占
- (其他 metric 类触发器同理)
