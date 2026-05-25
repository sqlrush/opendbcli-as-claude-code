---
name: 待修复Bug和待办清单
description: 压测中发现的bug和待优化项，按优先级排列
type: project
---

## 明天要做（2026-03-19）

### 规则引擎
1. **database_hang 决策树重构** — 当前太浅，活跃高+TPS低直接判hang。需要先检查Top Wait Event分类（cursor争用/锁等待/IO/真hang），避免误判。需要先在 ailinkdb/data 生成决策树JSON。
2. **规则诊断输出美化** — 双线框标题、根因/次因视觉层级、处置建议方框包围、风险dim颜色。改 `ruleengine/format.go`。

### Bug
3. **LLM 流式输出重复行** — /diag 的 LLM 输出中长行出现重复。
4. **LLM 报错美化** — ✅ 已改（connection refused → 友好提示），需部署验证。

### 交互
5. **sentinel 指标基线全 0** — 空闲时 std=0.0，确认是否正常。
6. **退出空行修复** — 代码已改，需 REPL 验证。
7. **⏺ 执行动画** — 代码已实现，需 REPL 验证。
8. **`/rule` debug 输出** — 已改为正式模式（去掉debug），已部署。
