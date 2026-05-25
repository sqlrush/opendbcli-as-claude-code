# Rule Engine 诊断管线（当前架构）

## 四阶段管线

```
Stage 1: 信号提取 (extractSignals)
  ├── WaitEvent signals: 等待事件占比 ≥ 1% 的全部提取
  ├── Metric signals: 只提取 anomalous (spike/rising) + 触发指标 + 百分比 >80
  ├── Category signals:
  │   ├── Sentinel 分类 → rootCauseToCategory 映射
  │   ├── 等待事件模式推断 (inferCategoriesFromWaits)
  │   ├── 有阻塞链 → "lock"
  │   ├── 有 Top SQL → "sql_perf"
  │   └── 有空间/undo/temp 异常 → "space"/"undo"
  └── Keyword/ErrorCode signals (用户问题/错误码场景)

Stage 2: 候选规则查找 (index.Match)
  └── 倒排索引: byWaitEvent / byMetric / byCategory / byKeyword / byErrorCode
      任意 signal 命中 → 加入候选列表

Stage 3: 触发过滤 (evaluateTriggers)
  ├── 评估每条候选规则的 trigger.conditions[]
  ├── 检查 trigger.skip_when[]
  └── 通过的规则进入决策树

Stage 4: 决策树 + 冲突解决 (evaluateTrees + Resolve)
  ├── 每条匹配规则走自己的决策树，产出 Diagnosis
  ├── BoostSeverityByImpact: 按 top wait event 占比提升严重程度
  └── Resolve: 因果链分析 → 权重收敛 → 输出 1-2 个根因
      Score = severityWeight × confidence × specificity
```

## 关键代码路径

| 组件 | 文件 |
|------|------|
| Engine.Diagnose() 主入口 | internal/oracle/ruleengine/engine.go:203 |
| extractBurstSignals() 信号提取 | internal/oracle/ruleengine/engine.go:367 |
| index.Match() 倒排索引 | internal/oracle/ruleengine/index.go |
| evaluateTriggers() 触发过滤 | internal/oracle/ruleengine/trigger.go |
| evaluateTrees() 决策树执行 | internal/oracle/ruleengine/engine.go |
| Resolve() 冲突解决 | internal/oracle/ruleengine/resolver.go |
| DiagnoseDebug() 调试版 | internal/oracle/ruleengine/engine.go:250 |

## Resolve 排序公式

```
Score = severityWeight(severity) × confidence × specificity
```

- severityWeight: critical=4, high=3, medium=2, low=1
- confidence: 0.0-1.0 (决策树输出)
- specificity: 规则计算的特异性分数

排序后取 top 1-2，其余标记为 downstream symptom (通过 CausedBy/CausesOf 关系)。
