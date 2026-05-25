---
name: dual-model-strategy
description: 核心需求 — 双模型诊断策略：9B 用 OpenDB 辅助推理，27B+ 用纯 LLM 推理链，失败后降级到 OpenDB 辅助
type: project
---

# 双模型诊断策略（核心需求）

## 需求描述

OpenDB 需要支持根据接入模型参数量自动选择诊断策略：

### 小参数模型（≤9B）— GuidedStrategy
- **策略**: LLM 推理链 + OpenDB 辅助判断
- **原因**: 9B 上下文窗口小（32K），工具调用能力有限，需要 OpenDB 编排引导
- **实现**: 当前的 playbook/assist 模式，OpenDB 压缩报告 + 规则兜底 + 限制轮数

### 大参数模型（≥27B）— AutonomousStrategy
- **策略**: 全部使用 LLM 推理链（最多 10 轮自主推理）
- **原因**: 27B+ 上下文窗口大（128K-262K），工具调用能力强，可自主规划诊断路径
- **实现**: auto 模式，LLM 自主选择工具、规划步骤、收集证据
- **降级**: 如果 10 轮未解决，自动降级到 OpenDB 辅助判断模式（GuidedStrategy）

### 降级逻辑
```
大模型 → AutonomousStrategy(10轮)
  ├─ 成功 → 返回诊断结果
  └─ 失败（10轮未收敛） → GuidedStrategy(规则分类 + 补充查询)
```

## 当前架构评估（2026-03-15）

### 已具备的基础
- `DiagnoseMode` 枚举（playbook/assist/auto）对应不同轮数和工具权限
- `AgentLoop` 模型无关，接受任意 `llm.Provider`
- `readOnlyFilter` 工具过滤机制
- `CompressReport()` 报告压缩
- 规则兜底分类（7 类根因）

### 需要新增的能力
1. **ModelCapability 配置**: 在 config.yaml 中声明模型能力等级（small/large）
2. **DiagnoseStrategy 接口**: 抽象策略选择，替代硬编码的 mode switch
3. **降级检测**: 判断 10 轮是否收敛（LLM 是否给出了明确结论）
4. **降级执行**: AutonomousStrategy 失败后自动切换到 GuidedStrategy
5. **多 Provider 支持**: 可选，允许降级时切换到不同模型

## 设计方向

```go
type DiagnoseStrategy interface {
    Name() string
    Diagnose(ctx, report, question) (*DiagnoseResult, error)
}

type GuidedStrategy struct { ... }      // 小模型：OpenDB 编排
type AutonomousStrategy struct { ... }  // 大模型：LLM 自主 + 降级

// 策略选择
func SelectStrategy(cfg ModelCapability) DiagnoseStrategy {
    if cfg.Level == "large" {
        return &AutonomousStrategy{fallback: &GuidedStrategy{...}}
    }
    return &GuidedStrategy{...}
}
```
