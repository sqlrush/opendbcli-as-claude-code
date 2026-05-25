# 厂商能力定义参考 (ProviderCapability)

> 源码位置: `internal/engine/provider/openaicompat.go`

## ProviderCapability 字段说明

| 字段 | 作用 | 影响什么 |
|------|------|---------|
| `MaxContextWindow` | 上下文窗口大小 | 决定什么时候触发消息压缩 |
| `MaxOutputTokens` | 最大输出长度 | 设置 `max_tokens` 参数 |
| `Thinking.Mode` | 推理启用方式 | 发请求时怎么告诉模型"请推理" |
| `Thinking.ExtractField` | 推理内容在哪个字段 | 解析响应时从哪提取推理过程 |
| `Thinking.MultiTurnPolicy` | 多轮推理处理 | 下一轮发消息时是否保留上轮推理 |
| `Thinking.EnableParams` | 注入的请求参数 | 拼接到 HTTP body 中 |
| `ToolCalling` | 工具调用能力 | 是否发送 tools 参数 |
| `Caching` | 缓存能力 | 读取缓存命中统计 |
| `RateLimit` | 限流头 | 解析限流响应头做重试 |
| `Output.FixedTemperature` | 是否锁定 temperature | 有些模型不接受 temperature 参数 |

## 各厂商能力对比

| 厂商 | 上下文 | 最大输出 | 推理启用方式 | 推理提取字段 | 多轮推理策略 | 缓存 | 工具调用 | 特殊点 |
|------|--------|---------|------------|------------|------------|------|---------|--------|
| Qwen | 1M | 8K | `enable_thinking: true` | `reasoning_content` | strip | 无 | 标准 | |
| DeepSeek | 128K | 8K | `thinking.type: enabled` | `<think>` 标签 + `reasoning_content` | strip | 自动(命中/未命中) | 标准 | 锁 temperature |
| Kimi | 256K | 32K | `thinking.type: enabled` | `reasoning_content` | strip | 无 | 标准 | tool_choice 受限 |
| GLM | 200K | 128K | `thinking.type: enabled` | `reasoning_content` | strip | 自动 | 标准 | |
| MiniMax | 205K | 16K | `reasoning_split: true` | `reasoning_details` | preserve | 无 | 标准 | 锁 temperature |
| MiMo | 1M | 16K | `enable_thinking: true` | `reasoning_content` | strip | 无 | 标准 | |
| OpenAI | 272K | 16K | `reasoning.effort: level` | 专用 blocks | preserve | 无 | 并行+严格模式 | 结构化输出、限流头 |
| Generic | 32K | 4K | 不支持 | 无 | 无 | 无 | 标准 | 兜底 |

## 为什么各厂商字段不一样

虽然都号称"OpenAI 兼容"，但各厂商在以下 3 个维度存在分歧：

### 1. 推理/思考链（Thinking）— 分歧最大

没有统一标准，各家自行设计：

| 实现方式 | 厂商 | 请求参数 | 响应字段 |
|---------|------|---------|---------|
| 布尔开关 | Qwen, MiMo | `enable_thinking: true` | `reasoning_content` |
| 嵌套对象 | Kimi, DeepSeek, GLM | `thinking: {type: enabled}` | `reasoning_content` |
| 分离模式 | MiniMax | `reasoning_split: true` | `reasoning_details` |
| 努力等级 | OpenAI | `reasoning: {effort: "high"}` | 专用 content block |
| 内容内嵌 | DeepSeek (fallback) | 无 | `<think>...</think>` 包裹在 content 中 |

根因: OpenAI 最初的 Chat Completions API 没有定义推理字段。各家在 2025 年推理模型爆发后各自扩展，导致参数名、字段名、嵌套结构全不一样。

### 2. 缓存统计（Caching）— 部分厂商有

| 厂商 | 缓存模式 | 命中字段 | 未命中字段 |
|------|---------|---------|----------|
| DeepSeek | 自动 | `prompt_cache_hit_tokens` | `prompt_cache_miss_tokens` |
| GLM | 自动 | (有但字段不同) | |
| 其他 | 不支持 | — | — |

根因: Anthropic 率先推出 prompt caching，DeepSeek/GLM 跟进但用自己的字段名。OpenAI 和其他厂商尚未在兼容 API 中暴露缓存统计。

### 3. 输出约束（Output）— 各有限制

| 约束 | 厂商 | 说明 |
|------|------|------|
| 锁定 temperature | DeepSeek, MiniMax, Kimi | 推理模型不接受 temperature 参数，传了会报错 |
| 并行工具调用 | 仅 OpenAI | 其他厂商一次只返回一个 tool_call |
| 严格模式 | 仅 OpenAI | `strict: true` 保证工具参数 100% 符合 schema |
| 结构化输出 | 仅 OpenAI | `response_format: {type: json_schema}` |

根因: 各家对 OpenAI API 的兼容程度不同。大多数只兼容核心的 chat/completions 接口，高级功能（并行工具、结构化输出）是 OpenAI 独有的。

### 总结

"OpenAI 兼容"只意味着**基础的消息格式一样**（messages + role + content）。一旦涉及推理、缓存、高级工具调用这些进阶功能，各厂商就各走各的路了。`ProviderCapability` 的设计就是为了把这些差异封装在一处，让引擎上层代码完全不感知厂商差异。
