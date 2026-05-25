---
name: architecture-model-agnostic
description: 架构设计原则 — 模型无关性，换模型只改配置不改代码，分层从弱到强渐进放开
type: project
---

# 架构模型无关性设计

## 核心原则

分层架构天然支持模型升级，从弱到强渐进放开，不需要重构。

## 模型越强，OpenDB 做得越少

```
模型能力弱 (9B)                    模型能力强 (100B+)
│                                  │
│  OpenDB 做 90%                   │  OpenDB 做 30%
│  ├─ 采集 ✓                       │  ├─ 采集 ✓（永远需要）
│  ├─ 异常检测 ✓                    │  ├─ 异常检测 ✓（永远需要）
│  ├─ 根因分类 ✓                    │  ├─ 根因分类 → 可跳过，AI 自己判断
│  ├─ 建议模板 ✓                    │  ├─ 建议模板 → 可跳过，AI 自己写
│  └─ Qwen 写报告                  │  └─ AI 全程主导分析和建议
│                                  │
│  playbook 模式                   │  auto 模式
```

`--mode` 参数本质上是一个渐变旋钮：
```
playbook ──────── assist ──────── auto
OpenDB 主导                      AI 主导
9B 够用                         需要强模型
确定性高                         灵活性高
```

## 换模型时各层的影响

| 层级 | 换强模型后 | 需要改代码吗 |
|------|-----------|-------------|
| **数据采集**（Collector/Sentinel） | 不变，永远需要 | 不改 |
| **爆发采集**（Burst） | 不变，永远需要 | 不改 |
| **聚合引擎**（Aggregator） | 不变，压缩结果仍有用 | 不改 |
| **根因预分类**（Classify） | 可跳过，但保留作辅助验证 | 不改，保留 |
| **建议模板**（Remediation） | 可跳过，AI 自己生成更好的建议 | 不改，保留 |
| **Agent Loop** | 不变，只是 max_rounds 可放大 | 改配置 |
| **System Prompt** | 可简化，不再需要把规则写死 | 改 prompt 文本 |
| **工具集过滤** | assist 模式可放开更多工具 | 改配置 |
| **Ollama Client** | 换个 model 名字 | 改配置 |

**改的全是配置和 prompt，不是代码。**

## 接口抽象保证可替换

```go
type LLMClient interface {
    Chat(ctx, messages, tools) (*Message, error)
}

// 现有
type OllamaClient struct { ... }   // Ollama（OpenAI 兼容）

// 未来可扩展
type VLLMClient struct { ... }     // vLLM 高吞吐部署
type LocalClient struct { ... }    // 本地推理引擎
type OpenAIClient struct { ... }   // 闭源 API（如需要）
```

上层 Agent 只依赖 LLMClient 接口，不关心底层是什么模型。

## 换模型的操作清单

```
1. ~/.opendb/config.yaml 改 model 名字         ← 10 秒
2. 调整 diagnose_mode 默认值 (playbook → auto)  ← 10 秒
3. 调整 max_rounds (3 → 10)                     ← 10 秒
4. 可选: 简化 system prompt                      ← 30 分钟
5. 重构代码                                      ← 不需要
```

## 为什么不需要重构

1. **数据采集层**永远需要 — 再聪明的 AI 也不能凭空生成数据库指标
2. **规则引擎**保留价值 — 作为 AI 判断的验证和兜底，不会浪费
3. **建议模板**保留价值 — 即使 AI 不用，也可以作为 baseline 对比 AI 输出质量
4. **Agent Loop**是通用的 — messages + tools + 循环，与模型无关
5. **LLMClient 接口**隔离了模型差异 — 换模型 = 换实现类
