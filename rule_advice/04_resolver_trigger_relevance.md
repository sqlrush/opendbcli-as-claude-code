# Resolver 触发指标相关性优化

## 当前问题

Resolve() 排序公式: `Score = severityWeight × confidence × specificity`

**缺失维度**: 诊断结论是否回应了触发指标。

当 redo_rate 触发时，一个 SQ contention 的诊断（severity=high, confidence=0.78）
可以轻易压过一个 redo 生成量的诊断（severity=medium, confidence=0.70），
即使后者才是对触发指标的直接回应。

## 优化方案

### 方案 A: 触发相关性加权（推荐）

```
Score = severityWeight × confidence × specificity × triggerRelevance

triggerRelevance 计算:
  - 规则的 signals 包含触发指标 → 1.5
  - 规则的 category 与触发指标分类一致 → 1.3
  - 规则的 causes_of 包含触发指标相关规则 → 1.2
  - 无关联 → 1.0
```

**优点**: 改动最小，只需在 Resolve 里加一个乘数
**缺点**: 可能在某些场景下错误压低正确的非触发相关诊断

### 方案 B: 双轨诊断

```
Primary 必须与触发指标相关（如果有相关规则命中的话）
Secondary 从所有诊断中选最高分的非 Primary 规则
```

**优点**: 保证 Primary 始终回应触发指标
**缺点**: 如果触发指标相关规则置信度很低（如 30%），强制作为 Primary 可能误导

### 方案 C: 触发指标作为 tie-breaker

```
Score 计算不变，但在排序时:
  - 同分段内（分差 <10%），优先选择与触发指标相关的
  - 大分差时，仍然按原公式
```

**优点**: 最保守，不影响现有诊断质量
**缺点**: 在本案例中分差可能较大，不一定能纠正

## 建议

优先做方案 A，triggerRelevance 默认 1.0（不影响现有逻辑），
只对明确与触发指标相关的规则给 1.3-1.5 的加成。
这样现有的 25 个测试场景结果不受影响（它们的触发指标和诊断通常一致）。
