# LLM 厂商 API 能力调研报告

> 日期：2026-04-03
> 用途：为 ProviderCapability 抽象设计提供数据支撑

---

## 各厂商 API 特性详表

### 1. Anthropic Claude API

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.anthropic.com/v1/messages` |
| **认证** | `x-api-key` header + `anthropic-version: 2023-06-01` |
| **上下文** | Opus 4.6: 1M, Sonnet 4.6: 1M, Haiku 4.5: 200K |
| **最大输出** | Opus 128K, Sonnet 64K, Haiku 64K |
| **思维模式** | Adaptive: `thinking: {"type": "adaptive"}`；Extended: `thinking: {"type": "enabled", "budget_tokens": N}`（min 1024）；响应含 `thinking` blocks + `signature`；display: `"summarized"` / `"omitted"` |
| **Effort** | `effort`: `"low"` / `"medium"` / `"high"` / `"max"`（Opus only）|
| **Speed** | `speed: "fast"`（beta, Opus only, 6x pricing）|
| **Prompt Cache** | 显式: `cache_control: {"type": "ephemeral"}`（最多4个断点）；TTL: 5min（1.25x写入）或 1h（2x写入）；读取: 0.1x成本；最小token: 1024-4096 |
| **Cache 响应字段** | `cache_creation_input_tokens`, `cache_read_input_tokens` |
| **工具调用** | Anthropic 原生格式 `input_schema`；`tool_choice`: auto/any/none/named；thinking时只能 auto/none |
| **限流头** | `anthropic-ratelimit-*`（12+ headers）, `retry-after` |
| **特殊错误码** | 529 (overloaded) |
| **Beta Headers** | `anthropic-beta`: output-300k, interleaved-thinking, skills, files-api 等 |
| **独有** | Compaction API（服务端上下文压缩）；task_budget；cache_edits |

### 2. OpenAI API

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.openai.com/v1/chat/completions` |
| **认证** | `Authorization: Bearer $KEY` |
| **上下文** | GPT-5.4: 272K（标准）/ 1.05M（扩展，>272K 2x input pricing）|
| **思维模式** | `reasoning.effort`: `"none"` / `"minimal"` / `"low"` / `"medium"` / `"high"` / `"xhigh"`；reasoning_tokens 仅计数不可见；可选 reasoning summary |
| **缓存** | 全自动，前缀匹配折扣，无需客户端操作 |
| **结构化输出** | `response_format: {"type": "json_schema", "json_schema": {..., "strict": true}}` |
| **预测输出** | `prediction` 参数（加速已知大部分内容的场景）|
| **并行工具调用** | `parallel_tool_calls: true/false` |
| **Seed** | `seed` 参数（可重现）|
| **限流头** | `x-ratelimit-*`（6 headers）|

### 3. Google Gemini API

| 特性 | 详情 |
|------|------|
| **端点** | `generativelanguage.googleapis.com/v1beta/models/{model}:generateContent` |
| **认证** | API key 或 OAuth Bearer |
| **上下文** | Gemini 2.5 Pro/Flash: 1M, 3.x: 1-2M |
| **思维模式** | 2.5: `thinkingConfig.thinkingBudget`: 0(关) / -1(动态) / 1-24576；3.x: `thinkingLevel`: minimal/low/medium/high；响应 `parts[].thought: true` |
| **缓存** | 显式: `caches.create()` 独立API + TTL 配置；隐式: 2.5+ 自动90%折扣 |
| **Grounding** | `tools: [{google_search: {}}]`；Code Execution 内置工具 |
| **Safety** | `safetySettings` 数组，per-request 配置 |
| **系统指令** | 独立 `system_instruction` 字段（不在 messages 中）|
| **工具调用** | Gemini 原生格式 `functionDeclarations`；`functionCallingConfig.mode`: AUTO/ANY/NONE |

### 4. DeepSeek API

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.deepseek.com/chat/completions` |
| **认证** | `Authorization: Bearer $KEY`（OpenAI兼容）|
| **上下文** | V3.2: 128K, reasoner: 128K |
| **思维模式** | `model: "deepseek-reasoner"` 或 `thinking: {"type": "enabled"}`；响应 `reasoning_content` 字段；多轮: 工具链内保留，新用户轮次剥离 |
| **缓存** | 全自动磁盘缓存，64token粒度，0.1x命中成本；响应: `prompt_cache_hit_tokens`, `prompt_cache_miss_tokens` |
| **FIM** | Beta端点 `/beta`，提供prefix+suffix，模型填中间 |
| **限制** | reasoner 不支持 temperature/top_p/penalties |
| **工具调用** | OpenAI兼容 |

### 5. Kimi/Moonshot API

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.moonshot.ai/v1/chat/completions` |
| **上下文** | K2.5: 256K |
| **思维模式** | `thinking: {"type": "enabled"}`；响应 `reasoning_content` 字段；多轮: 工具链内保留，新用户轮次剥离；thinking时 tool_choice 只能 auto/none |
| **缓存** | `prompt_cache_key` 参数 |
| **限制** | K2.5 temperature/top_p 固定不可改 |

### 6. Qwen/DashScope API

| 特性 | 详情 |
|------|------|
| **端点** | OpenAI兼容: `dashscope-intl.aliyuncs.com/compatible-mode/v1/chat/completions` |
| **上下文** | Qwen3.5: 最高1M |
| **思维模式** | `enable_thinking: true`（extra_body）；`thinking_budget` 限制推理token；响应 `reasoning_content` 字段 |
| **增量输出** | `incremental_output: true`（流式每chunk只含新内容）|
| **Web搜索** | `X-DashScope-Plugin: {"web_search_pro": {}}` |
| **工具调用** | OpenAI兼容 |

### 7. GLM/Zhipu API

| 特性 | 详情 |
|------|------|
| **端点** | `https://open.bigmodel.cn/api/paas/v4/chat/completions` |
| **上下文** | GLM-5: 200K |
| **最大输出** | GLM-5: 128K |
| **思维模式** | `extra_body: {"thinking": {"type": "enabled"}}`；响应 `reasoning_content` |
| **缓存** | 自动，折扣定价（$0.20 vs $1.00/M）|
| **Web搜索** | 内置 `web_search` 工具 |
| **工具调用** | OpenAI兼容 |

### 8. MiniMax API

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.minimax.io/v1/chat/completions` |
| **上下文** | 204,800 tokens |
| **思维模式** | `extra_body: {"reasoning_split": true}`；响应 `reasoning_details` 字段（注意：字段名不同）|
| **高速模式** | `-highspeed` 变体（100 tps）|
| **限制** | temperature (0,1] only, n=1 only, 不支持 presence/frequency_penalty |
| **工具调用** | OpenAI兼容 |

### 9. MiMo (小米)

| 特性 | 详情 |
|------|------|
| **端点** | `https://api.xiaomimimo.com/v1/chat/completions` |
| **上下文** | Pro: 1M |
| **思维模式** | `enable_thinking: true`；响应 `reasoning_content`；`<think>` 标签 |
| **工具调用** | OpenAI兼容 |
| **独有** | MTP多token预测加速 |

### 10. 本地部署 (Ollama / vLLM)

| 特性 | Ollama | vLLM |
|------|--------|------|
| **端点** | `localhost:11434` | `localhost:8000` |
| **认证** | 无 | 无 |
| **缓存** | 无 | `enable_prefix_caching=True`（自动KV缓存复用）|
| **工具调用** | 兼容模型支持（llama3.1+, qwen2.5+）| 完整OpenAI兼容 |
| **结构化输出** | `format` 参数（JSON schema）| `guided_json`, `guided_regex` |
| **上下文** | 模型依赖，`num_ctx` 配置 | `--max-model-len` 配置 |

---

## 跨厂商能力对比矩阵

```
                  思维模式        缓存          工具调用      特殊能力
                  ────────        ────          ────────      ────────
Anthropic         adaptive        显式(4断点)    原生          effort/speed/budget/beta
OpenAI            effort_level    自动          兼容          structured/parallel/predict/seed
Gemini            budget          独立API+自动   原生          grounding/code_exec/safety
DeepSeek          auto_tags       自动(磁盘)     兼容          FIM/prefix_cache
Kimi              enable_flag     cache_key     兼容          reasoning_content
Qwen              enable_flag     —             兼容          web_search/incremental
GLM               enable_flag     自动          兼容          web_search
MiniMax           split           —             兼容          reasoning_details/highspeed
MiMo              enable_flag     —             兼容          MTP加速
Ollama            auto_tags       —             兼容*         本地/无认证
vLLM              auto_tags       prefix_cache  兼容          PagedAttention/LoRA
```

### 思维内容提取字段差异

| 厂商 | 字段名 | 格式 |
|------|--------|------|
| Anthropic | `content[].type=="thinking"` | 结构化blocks + signature |
| OpenAI | `reasoning_tokens`(计数) + optional summary | 仅统计/摘要 |
| Gemini | `parts[].thought==true` | parts数组中标记 |
| DeepSeek/Kimi/Qwen/GLM/MiMo | `reasoning_content` | 与content平级的字符串字段 |
| MiniMax | `reasoning_details` | 字段名不同 |
| Ollama本地 | `<think>...</think>` 标签 | 嵌在content中需提取 |

### 思维内容多轮处理策略

| 厂商 | 策略 |
|------|------|
| Anthropic | **必须保留** thinking blocks + signature（丢失会报错）|
| OpenAI | **保留** reasoning items |
| DeepSeek/Kimi/Qwen/GLM/MiMo | 工具链内保留，**新用户轮次剥离** |
| MiniMax | 保留完整assistant响应 |

---

## ProviderCapability 设计（基于调研）

详见 engine 架构设计文档。核心结论：

1. **思维模式**差异最大：6种开启方式 × 5种提取方式 × 3种多轮策略
2. **缓存**分三大阵营：显式标记(Anthropic) / 全自动(DeepSeek/OpenAI/GLM) / 独立API(Gemini)
3. **工具调用**格式分三族：Anthropic原生 / OpenAI兼容(国产大部分) / Gemini原生
4. **限流**只有 Anthropic/OpenAI 有丰富头信息，其他都是标准429
5. **国产模型**高度统一：大部分走 OpenAI 兼容格式 + `reasoning_content` 字段
