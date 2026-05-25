---
name: model-selection
description: 模型选型 — 9B(编排式) → 27B-Opus-Distilled(待验证自主推理) → 35B-A3B(备选)，含部署要求和架构影响
type: project
---

# 模型选型（2026-03）

## 当前在用：Qwen3.5-9B

<10B 级别综合最强，8GB 显存可跑。
但 **9B 做不好 5 步以上的链式推理**，超过 3 步容易丢上下文或一条路走到黑。
当前采用 **编排式**（OpenDB 决定查什么，Qwen 负责解读）。

## 候选升级：Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled

来源: kwangsuklee/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled-GGUF (Ollama)

| 维度 | 表现 |
|------|------|
| 底座 | Qwen3.5-27B |
| 蒸馏源 | Claude Opus 4.6 推理链 (CoT) |
| 工具调用 | Qwen3.5 系列 BFCL-V4 得分 72.2（超 GPT-5 mini 30%） |
| 自主性 | 实测连续自主运行 9 分钟，自动等工具响应→读结果→自纠错 |
| 上下文 | 262K |
| 部署 | Q4_K_M ~17GB VRAM，单张 RTX 3090，29-35 tok/s |

**如果验证通过，可切到 AutonomousStrategy（LLM 自主决定调什么工具）。**

### 风险
- 蒸馏 ≠ 原模型，推理深度有折损
- 量化可能影响推理质量
- DBA 领域知识不一定蒸馏到了（Opus 蒸馏的是通用推理链）
- 需要实测验证

### 验证计划
1. 测试服务器部署：`ollama pull kwangsuklee/Qwen3.5-27B-Claude-4.6-Opus-Reasoning-Distilled-GGUF`
2. 用已采集的 burst 数据手动喂模型，看能否自主诊断
3. 重点验证：function calling 稳定性、多步推理、Oracle 领域知识
4. 通过后切到 AutonomousStrategy

## 备选升级：Qwen3.5-35B-A3B

MoE 架构，激活 3B，文件 ~19GB，需 24GB VRAM。
同代内的官方 MoE 方案，推理能力介于 9B 和 27B-Distilled 之间。

## 模型与架构策略对应关系

| 模型 | 策略 | LLM 做什么 | OpenDB 做什么 |
|------|------|-----------|--------------|
| 9B | GuidedStrategy（编排式） | 每步单轮解读 | 决定查什么、编排流程 |
| 27B-Opus-Distilled | AutonomousStrategy（自主式） | 自主调工具、链式推理 | 采全量数据、规则兜底 |
| 35B-A3B | 半自主（3-5 步） | 自己决定调 2-3 个工具 | 采数据 + 兜底 |
| 70B+ | 全自主 | 完全自主链式推理 | 纯探针 + 兜底 |

## 全量数据包方案（不论哪个模型都适用）

不编排推理路径，编排数据采集。固定采 ~20 个查询：
- burst 数据（已有）
- Top SQL 执行计划（自动 DISPLAY_CURSOR）
- Top SQL 历史 plan_hash 对比
- resource_limit / PGA / Undo / Temp
- OS: iostat + free（如 /os 可用）
- alert log 最近 ORA-

一次性打包喂给 LLM，~3000 token 压缩后。
9B 做单轮解读，27B+ 做自主深挖。

## <10B 级别竞争格局（参考）

| 模型 | 参数 | 强项 | 弱项 |
|------|------|------|------|
| Qwen3.5-9B | 9B | 综合最均衡，中文强 | 链式推理 >3 步不稳定 |
| Falcon-H1R-7B | 7B | 数学推理最强 | 中文弱 |
| Phi-4-mini | 3.8B | 极小体积 | 覆盖面窄 |

**升级应在同代（3.5）内选择，不要退回 Qwen3 系列。**
