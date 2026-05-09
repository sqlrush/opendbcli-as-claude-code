# 现有 Redo 相关规则审计

## 规则清单

| Rule ID | 名称 | category | 触发条件 | 问题 |
|---------|------|----------|---------|------|
| ORA_162 | Redo Log 管理诊断 | storage | log_switch_per_hour > 6 | category 应加 "redo"；不覆盖 redo_rate 指标 |
| ORA_163 | Redo Log 切换优化 | storage | log file switch 等待 | 同上 |
| ORA_189 | log file switch 等待诊断 | wait_event | log file switch 等待 | 只诊断日志切换等待，不诊断 redo 生成量 |
| WE2-007 | enq: SQ (RAC) | wait_event | SQ contention > 2% + RAC | 正确覆盖 RAC 场景 |
| WE2-007b | enq: SQ (单实例) | wait_event | SQ contention > 2% + 非 RAC | 正确覆盖单实例；但没有 causes_of 定义连接到 redo |

## 关键发现

### 1. "redo" category 无人注册

Sentinel 分类 CauseRedoBottleneck → 生成 Signal{Category, "redo"}
但没有任何规则的 index 包含 category="redo"

- ORA_162/163: category="storage"
- ORA_189: category="wait_event"

**修复**: ORA_162 的 signals 应加 `{type: "category", key: "redo"}`

### 2. "redo_rate" metric 无人注册

extractBurstSignals 会提取 Signal{Metric, "redo_rate"}（因为是 triggerMetric）
但没有规则注册 redo_rate 作为 metric signal。

**修复**: 新增 redo 生成量异常规则，signals 包含 `{type: "metric", key: "redo_rate"}`

### 3. 现有 redo 规则只覆盖"日志管理"，不覆盖"生成量"

```
redo 问题域:
  ├── Redo 生成量过高 (redo_rate spike)     ← 无规则覆盖 ❌
  │   └── 原因: 大量 DML、批量导入、频繁 commit
  ├── Redo 日志管理问题 (log switch 频繁)   ← ORA_162/163 覆盖 ✅
  │   └── 原因: 日志文件过小、日志组不足
  └── Redo 写入性能 (log file sync 慢)      ← ORA_189 覆盖 ✅
      └── 原因: 存储 IO 差、LGWR 争用
```

### 4. WE2-007b 的 MatchDefault 问题

WE2-007b 的决策树只有一个 branch，用 MatchDefault()：
```go
Branches: []Branch{
    {
        Label:    "序列 CACHE 过小或 NOCACHE 导致 SQ 争用",
        Match:    MatchDefault(),   // 永远命中
        Severity: SeverityHigh,
        ...
    },
},
```

**问题**: 没有实际查询验证序列 CACHE 是否真的过小，直接输出结论。
应该加决策树步骤查 dba_sequences 确认 CACHE 配置。

### 5. causes_of 关系缺失

WE2-007b 定义了 `CausesOf: []string{"WD015"}`（row cache lock），
但没有 CausedBy 定义。如果新增 redo 生成量规则，需要在该规则的
causes_of 里加 WE2-007b，或者在 WE2-007b 的 caused_by 里加 redo 规则。
