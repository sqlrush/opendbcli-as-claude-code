# 实施方案：database_hang 决策树重构 + 7 类新增根因场景

## 概述

两个相关增强，共 8 个 Phase，预估新增 ~1,635 行代码。

## 最终分类优先级链（14 条规则）

```
Rule  1: 锁等待阻塞      (最高 — 阻塞链是确定性证据)
Rule  2: SQL并发冲高      (单 SQL 高并发主导)
Rule  3: 执行计划漂移     (单 SQL 低并发高耗时，plan 变化) [新]
Rule  4: Redo冲高         (Commit 类等待主导)
Rule  5: 归档冲高         (log file switch archiving 等待) [新]
Rule  6: DG同步冲高       (log file sync + 备库 lag) [新]
Rule  7: Latch争用冲高    (Concurrency 类等待主导)
Rule  8: 存储I/O冲高      (IO 类等待主导)
Rule  9: 内存冲高         (内存相关等待 + cache miss) [新]
Rule 10: 连接数冲高       (sessions 逼近上限) [新]
Rule 11: Undo冲高         (undo 空间 + US contention) [新]
Rule 12: TEMP冲高         (temp 空间 + direct path 等待) [新]
Rule 13: 流量冲高         (active spike, SQL 分散, 无阻塞)
Rule 14: 数据库Hang       (排除法，以上全不匹配) [新]
Fallback: classifyFromTrigger
Default:  CauseUnknown
```

## Phase 1: 共享基础设施

### 1.1 types.go — 新增 8 个 RootCauseType 常量
```go
CausePlanDrift         = "plan_drift"        // 执行计划漂移
CauseMemoryPressure    = "memory_pressure"   // 内存冲高
CauseConnectionExhaust = "connection_exhaust" // 连接数冲高
CauseArchiveDelay      = "archive_delay"     // 归档冲高
CauseDGSyncDelay       = "dg_sync_delay"     // DG同步冲高
CauseUndoPressure      = "undo_pressure"     // Undo冲高
CauseTempPressure      = "temp_pressure"     // TEMP冲高
CauseDatabaseHang      = "database_hang"     // 数据库Hang
```

### 1.2 types.go — BurstReport 新增字段
- `PlanHistory map[string][]int64` — sql_id → plan_hash_value 列表
- `ResourceLimits []ResourceLimitEntry` — sessions/processes 资源限制

### 1.3 analyze.go — 聚合 plan_hash_value 历史

## Phase 2: database_hang 决策树（新文件 classify_hang.go）

### 四步排除法：
1. **预筛选**: active_sessions 高 + TPS 低 → 候选
2. **等待事件排除**:
   - cursor: pin S >60% → 不是 hang（是 Latch）
   - enq: TX/TM >60% → 不是 hang（是锁）
   - db file read >60% → 不是 hang（是 IO）
   - log file sync >60% → 不是 hang（是 Redo）
3. **正向指标评分**:
   - 等待事件分散（无单类 >40%）→ +0.2
   - 实例状态非 OPEN → +0.3
   - sessions >95% → +0.15
   - 归档等待出现 → +0.15
   - active/baseline >10x → +0.2
4. **判定**: score ≥ 0.5 → 分类为 DatabaseHang

### 新增 remediation: 检查 v$instance、后台进程、资源限制、归档目标

## Phase 3: CausePlanDrift（P0）

### 区分于 CauseBadSQL 的关键：
- CauseBadSQL: OccurrenceRate ≥ 0.5 AND MaxConcurrent ≥ 3（高并发）
- CausePlanDrift: OccurrenceRate ≥ 0.3 AND MaxConcurrent < 5（低并发高耗时）

### 证据：
- PlanHistory 中同一 sql_id 有多个 plan_hash_value → 直接证据
- 单 plan 但 elapsed >30s + 并发 ≤ 2 → 间接证据

### 置信度: 0.8（多 plan hash）/ 0.6（单 plan 但模式匹配）

## Phase 4: CauseMemoryPressure + CauseConnectionExhaust（P1）

### MemoryPressure:
- 触发: PGA/SGA 使用率突增 + buffer busy waits / free buffer waits
- 区分 IO: IO 是磁盘级，Memory 是缓存级

### ConnectionExhaust:
- 触发: ResourceLimits sessions >85%
- 区分 TrafficStorm: TrafficStorm 是活跃会话多，ConnectionExhaust 是总会话逼近硬限

## Phase 5: 四个 P2 场景

| 场景 | 触发 | 关键等待事件 | 关键指标 |
|------|------|-------------|---------|
| 归档冲高 | archive_sessions | log file switch (archiving needed) | FRA >90% |
| DG同步冲高 | log file sync 延迟 | log file sync | standby_apply_lag >120s |
| Undo冲高 | undo 空间 | enq: US - contention | undo_used_pct >85% |
| TEMP冲高 | temp 使用率 | direct path read/write temp | temp_used_pct >85% |

注：standby_apply_lag 和 undo_used_pct 探针当前 stub 为 0，分类函数就绪但暂时休眠。

## Phase 6: 优先级链终态 + classifyFromTrigger 映射更新

## Phase 7: 测试
- classify_hang_test.go: 9 个测试用例
- classify_new_test.go: 14+ 个测试 + 4 个优先级链集成测试
- remediation_test.go: 覆盖全部 14 种根因

## Phase 8（可延后）: 实现 undo/standby 真实探针

## 工作量预估

| Phase | 内容 | 复杂度 | 预估行数 |
|-------|------|--------|---------|
| 1 | 共享基础设施 | 低 | ~70 |
| 2 | hang 决策树 | 中 | ~570 |
| 3 | PlanDrift | 中 | ~95 |
| 4 | Memory + Connection | 中 | ~160 |
| 5 | 四个 P2 场景 | 低 | ~320 |
| 6 | 优先级链 + 映射 | 低 | ~30 |
| 7 | 测试 | 低 | ~610 |
| 8 | 探针补齐 | 低 | ~30 |
| **合计** | | | **~1,635 行** |

## 依赖关系

```
Phase 1 → Phase 2/3/4/5 (并行)
Phase 2-5 → Phase 6 (汇总)
Phase 6 → Phase 7 (测试)
Phase 8 独立
```

## 文件清单

### 新增文件（4 个）
- `internal/oracle/sentinel/classify_hang.go` — hang 决策树
- `internal/oracle/sentinel/classify_new.go` — 7 个新分类函数
- `internal/oracle/sentinel/classify_hang_test.go`
- `internal/oracle/sentinel/classify_new_test.go`

### 修改文件（6 个）
- `internal/oracle/sentinel/types.go` — 新常量 + 新结构体
- `internal/oracle/sentinel/classify.go` — 优先级链重排
- `internal/oracle/sentinel/remediation.go` — 8 个新模板
- `internal/oracle/sentinel/post_burst.go` — ResourceLimits 采集
- `internal/oracle/sentinel/analyze.go` — PlanHistory 聚合
- `internal/oracle/sentinel/format_monitor.go` — 验证无需改
