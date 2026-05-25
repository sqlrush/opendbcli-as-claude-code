# 03-附 — ProviderAdapter 详解：为什么 5 个 Adapter，不多不少

## 一、全球 LLM API 格式版图

```
API 协议真正独立的只有三家，其他所有厂商都跟着 OpenAI 走:

Anthropic ──── 独立协议 ──── 只有 Anthropic 自己用
Google    ──── 独立协议 ──── 只有 Gemini 自己用
OpenAI    ──── 事实标准 ──── 全世界其他所有厂商都兼容
```

---

## 二、三族 API 格式详细对比

### 端点

```
OpenAI 兼容:  POST /v1/chat/completions
Anthropic:    POST /v1/messages
Gemini:       POST /v1beta/models/{model}:generateContent    ← 模型名在 URL 里
```

### 认证

```
OpenAI 兼容:  Authorization: Bearer sk-xxx
Anthropic:    x-api-key: sk-ant-xxx + anthropic-version: 2023-06-01
Gemini:       ?key=AIza-xxx（URL query 参数）或 OAuth Bearer token
```

### 消息格式

```
OpenAI:    {"role": "user", "content": "你好"}                           ← content 是字符串
Anthropic: {"role": "user", "content": [{"type": "text", "text": "你好"}]}  ← content 是数组
Gemini:    {"role": "user", "parts": [{"text": "你好"}]}                 ← 不叫 content，叫 parts
```

### System 指令

```
OpenAI:    messages[0] = {"role": "system", "content": "..."}     ← 在 messages 数组里
Anthropic: "system": [{"type":"text","text":"..."}]               ← 顶层独立参数
Gemini:    "system_instruction": {"parts":[{"text":"..."}]}       ← 又一个不同的名字
```

### 工具格式

```
OpenAI:    tools: [{"type":"function","function":{"name":"x","parameters":{...}}}]
Anthropic: tools: [{"name":"x","input_schema":{...}}]                ← 没有 function 包装
Gemini:    tools: [{"function_declarations":[{"name":"x","parameters":{...}}]}]
           tool_config: {"function_calling_config":{"mode":"AUTO"}}  ← 控制方式也不同
```

### 工具调用响应

```
OpenAI:    choices[0].message.tool_calls[].function.name / .arguments
Anthropic: content[].type=="tool_use" → .name / .input              ← 在 content 数组里混排
Gemini:    candidates[0].content.parts[].functionCall.name / .args
```

### 思维模式

```
OpenAI o系列:  reasoning.effort = "high"
Anthropic:     thinking: {type: "adaptive"}
Gemini 2.5:    generation_config.thinking_config.thinking_budget = -1  ← token 预算
Gemini 3.x:    thinking_config.thinking_level = "high"                 ← 换成 level
```

### 思维内容提取

```
OpenAI:    仅 reasoning_tokens 计数 + optional summary（看不到内容）
Anthropic: content[].type=="thinking" → .thinking + .signature         ← 结构化 blocks
Gemini:    parts[].thought==true                                       ← parts 中标记
```

### 缓存机制

```
OpenAI/DeepSeek:  全自动，客户端不操作
Anthropic:        客户端标记 cache_control 断点（最多4个），TTL 5min/1h
Gemini:           独立 API 调用! caches.create() 创建缓存对象 → 返回 cache name
                  → 后续请求通过 cached_content 参数引用 → 三家里最复杂
```

### 独有参数

```
Anthropic 独有:
  effort: "low"/"medium"/"high"/"max"    ← 推理力度
  speed: "fast"                           ← 快速模式（6x价格）
  output_config.task_budget               ← 剩余预算
  cache_control + cache_edits             ← 缓存精细控制
  anthropic-beta: [...]                   ← Beta 功能声明

Gemini 独有:
  safety_settings: [...]                  ← 安全过滤级别
  tools: [{google_search: {}}]            ← 内置 Google 搜索
  tools: [{code_execution: {}}]           ← 内置代码执行
  cached_content: "cache-name"            ← 引用预创建的缓存
```

### 总表

| 维度 | OpenAI (+ 国产) | Anthropic | Gemini |
|------|----------------|-----------|--------|
| **端点** | `/v1/chat/completions` | `/v1/messages` | `/v1beta/models/{model}:generateContent` |
| **认证** | `Authorization: Bearer` | `x-api-key` + version | URL `?key=` 或 OAuth |
| **消息字段** | `content` (字符串) | `content` (数组) | `parts` (数组) |
| **System** | messages[0].role=system | 顶层 `system` | 顶层 `system_instruction` |
| **工具定义** | `tools[].function` | `tools[].input_schema` | `tools[].function_declarations` |
| **工具响应** | `tool_calls[].function` | `content[].type=tool_use` | `parts[].functionCall` |
| **思维控制** | `reasoning.effort` | `thinking.type` | `thinking_config.thinking_budget` |
| **思维提取** | 仅 token 计数 | `content[].type=thinking` | `parts[].thought=true` |
| **缓存** | 自动 | 客户端标记断点 | 独立 API 创建 |

---

## 三、国产模型 OpenAI 兼容情况

所有主流国产模型厂商都提供了 OpenAI 兼容接口。大部分只有兼容接口，没有独立格式。

| 厂商 | 有独立原生 API 吗 | 情况 | 推荐 |
|------|------------------|------|------|
| **DeepSeek** | ❌ 无 | 就是 OpenAI 格式 | OpenAI 兼容 |
| **Kimi** | ❌ 无 | 就是 OpenAI 格式 | OpenAI 兼容 |
| **Qwen** | ✅ 有 | DashScope 原生格式，但官方推荐兼容模式 | OpenAI 兼容 |
| **GLM** | ❌ 无 | v4 直接就是 OpenAI 兼容（v3 独立格式已废弃） | OpenAI 兼容 |
| **MiniMax** | ✅ 有 | 早期有独立格式，仍可用，但推荐兼容模式 | OpenAI 兼容 |
| **MiMo** | ❌ 无 | 就是 OpenAI 格式 | OpenAI 兼容 |
| **百川** | ✅ 有 | 已全面转向 OpenAI 兼容 | OpenAI 兼容 |
| **零一万物** | ❌ 无 | 直接 OpenAI 兼容 | OpenAI 兼容 |

**OpenAI 的 `/v1/chat/completions` 是国内大模型 API 的事实标准。**

但"兼容"不等于"完全一样"，各家在基础协议上加了厂商专属扩展：

```
共同基础（OpenAI 标准）:
  model, messages, tools, stream, max_tokens, temperature
  → response: choices[0].message.content / tool_calls / finish_reason
  → usage: prompt_tokens, completion_tokens

各家私有扩展:
  DeepSeek:  thinking.type, reasoning_content, prompt_cache_hit_tokens
  Kimi:      thinking.type, reasoning_content, K2.5 固定 temperature
  Qwen:      enable_thinking, thinking_budget, incremental_output, X-DashScope-Plugin
  GLM:       extra_body.thinking.type, reasoning_content
  MiniMax:   reasoning_split, reasoning_details（字段名不同!）, temperature (0,1] only
  MiMo:      enable_thinking, reasoning_content
```

---

## 四、5 个 Adapter 的分工与理由

### AnthropicAdapter（独立） — 协议完全不同

```
必须独立的原因:
  ❌ system 放 messages[0] → Anthropic 不认（要求顶层 system 参数）
  ❌ content 发字符串 → Anthropic 期望数组
  ❌ tools 用 function 包装 → Anthropic 不认
  ❌ 不发 x-api-key 和 anthropic-version → 401
  ❌ 无法传 thinking/effort/speed/cache_control
  ❌ 响应中 thinking blocks + signature 无法用 OpenAI 格式解析
  → 和 OpenAI 兼容格式 0% 共用，必须完全独立实现
```

### GeminiAdapter（独立） — 协议完全不同

```
必须独立的原因:
  ❌ 端点格式不同（模型名在 URL 路径里）
  ❌ 认证方式不同（key 在 URL query 里）
  ❌ 消息用 parts 不是 content
  ❌ system 用 system_instruction
  ❌ 工具用 function_declarations
  ❌ 缓存需要先调独立 API 创建缓存对象
  ❌ 思维内容在 parts[].thought 里
  → 和 OpenAI 兼容格式 0% 共用，必须完全独立实现
```

### OpenAICompatAdapter（通用） — 覆盖 7 厂商

```
一个 Adapter 覆盖: OpenAI / DeepSeek / Kimi / Qwen / GLM / MiniMax / MiMo

共用部分 (90%):
  ✅ 端点: 都是 POST /v1/chat/completions
  ✅ 认证: 都是 Authorization: Bearer
  ✅ 消息格式: 都是 {role, content, tool_calls}
  ✅ 工具格式: 都是 {type:"function", function:{name, parameters}}
  ✅ 流式: 都是 SSE data: {...}
  ✅ 响应: 都是 choices[0].message.content / tool_calls

差异部分 (10%, 通过 Capability 处理):
  思维开启参数名不同 → EnableParams() 返回不同的 map
  思维提取字段名不同 → ExtractField 指定 "reasoning_content" 或 "reasoning_details"
  缓存统计字段不同 → CacheReadField 指定不同字段名
  参数限制不同 → FixedTemperature 等标记
```

### OllamaAdapter（独立） — 协议兼容但运行特性不同

```
虽然 Ollama 也走 /v1/chat/completions 兼容格式，但独立 Adapter 的原因:

1. 无认证
   云 API: Authorization: Bearer sk-xxx
   Ollama: 无任何认证头
   → 如果共用 OpenAICompatAdapter，需要处理 "apiKey 为空时不发 Authorization 头"
   → 可以做但不够干净

2. 本地特性影响重试策略
   云 API: 网络错误 → 重试（可能是临时网络抖动）
   Ollama: 网络错误 → 不重试（可能是模型还在推理，5min timeout 是正常的）
   → RateLimit.IsLocal = true，RetryPolicy 需要区别对待

3. 上下文窗口可变
   云 API: 上下文窗口固定（厂商决定）
   Ollama: 上下文窗口由 num_ctx 参数配置，模型之间差异大
   → 需要从 Ollama API 查询实际 num_ctx，不能硬编码

4. 工具调用不保证可用
   云 API: 大部分都支持 function calling
   Ollama: 取决于模型（llama3.1+ 支持，qwen2.5+ 支持，其他可能不支持）
   → ToolCalling.TextFallback = true，不支持时自动降级到文本模拟

5. 思维提取方式固定
   云 API: 各厂商通过不同字段返回思维内容
   Ollama: 所有模型统一用 <think>...</think> 标签嵌在 content 里
   → 提取逻辑固定，不需要 Capability 动态选择

如果硬塞进 OpenAICompatAdapter:
  → 需要加 if apiKey == "" / if isLocal / if textFallback 等大量分支
  → 违背 "Capability 驱动，不硬编码身份" 的原则
  → 不如独立一个 ~100 行的简单 Adapter 更清晰
```

### VLLMAdapter（独立） — 协议兼容但有独特能力

```
虽然 vLLM 也走 /v1/chat/completions 兼容格式，但独立 Adapter 的原因:

1. Prefix Caching
   Ollama: 无缓存
   vLLM:   enable_prefix_caching=True，自动 KV 缓存复用
   → Caching.Mode = CachingAutomatic
   → 可以从响应中读取缓存命中统计

2. 结构化输出
   Ollama: format 参数（基础 JSON schema）
   vLLM:   guided_json / guided_regex / guided_choice / guided_grammar
   → 比 Ollama 强得多的结构化输出控制
   → Output.SupportsStructuredOutput = true

3. 部署配置
   Ollama: 简单，ollama pull 就能用
   vLLM:   生产级，支持 tensor 并行、LoRA 热加载、连续批处理
   → 上下文窗口通过 --max-model-len 配置
   → 需要查询 /v1/models 获取实际能力

4. 无认证但可配置
   Ollama: 永远无认证
   vLLM:   默认无认证，但支持通过反向代理加认证
   → 认证行为需要可配置

如果和 Ollama 合并成一个 "LocalAdapter":
  → 缓存能力完全不同（一个有一个没有）
  → 结构化输出能力不同
  → 配置方式不同
  → 合并反而增加复杂度

如果塞进 OpenAICompatAdapter:
  → 同 Ollama 的问题：IsLocal + 无认证 + 可变上下文 等分支太多
```

---

## 五、为什么不能更少？也不需要更多？

### 能不能只用 3 个？（合并 Ollama + vLLM 到 OpenAICompatAdapter）

```
可以，但代价是:
  OpenAICompatAdapter 里会出现大量分支:
    if isLocal { 不发 Authorization }
    if isLocal { 不重试网络错误 }
    if vendor == "ollama" { TextFallback = true }
    if vendor == "vllm" { 读 prefix cache 统计 }
    if vendor == "vllm" { 支持 guided_json }

  这违背了设计原则——Capability 驱动而不是 if-else 驱动
  3 个变成了一个"上帝 Adapter"，维护成本反而更高
```

### 需不需要更多？（每个国产厂商一个 Adapter？）

```
不需要。7 个国产厂商的差异:
  - HTTP 调用: 100% 相同
  - 请求格式: 95% 相同（差异在 Extra 参数）
  - 响应格式: 90% 相同（差异在 reasoning 字段名）
  - 流式格式: 100% 相同

  这些差异全部通过 Capability + EnhanceRequest + NormalizeResponse 处理
  不需要 7 个独立 Adapter
```

### 最终结论

```
6 个 Adapter 是最优平衡:

  AnthropicAdapter      ← 协议独立，无法共用            → 必须独立
  GeminiAdapter         ← 协议独立，无法共用            → 必须独立
  OpenAICompatAdapter   ← 7 厂商共用，Capability 差异化 → 合并最优
  OllamaAdapter         ← 本地 + 无认证 + 弱模型兜底     → 独立更清晰
  VLLMAdapter           ← 本地 + prefix cache + guided   → 独立更清晰
  MLXAdapter            ← Apple Silicon 原生 + 统一内存   → 独立更清晰

  少于 6 个: 把本地部署差异塞进 OpenAICompatAdapter 变上帝类
  多于 6 个: 国产厂商 Adapter 间 90% 代码重复
```

---

## 六、6 个 Adapter 覆盖关系

```
AnthropicAdapter ──→ Claude Opus / Sonnet / Haiku
GeminiAdapter ─────→ Gemini Pro / Flash
OpenAICompatAdapter
  ├─ vendor=openai ──→ GPT-5 / GPT-4 / o系列
  ├─ vendor=deepseek → DeepSeek V3 / R1
  ├─ vendor=kimi ────→ Kimi K2.5 / moonshot-v1
  ├─ vendor=qwen ────→ Qwen3.5 / QwQ
  ├─ vendor=glm ─────→ GLM-5 / GLM-4.5
  ├─ vendor=minimax ─→ MiniMax M2.7 / M2.5
  ├─ vendor=mimo ────→ MiMo V2 Pro / Flash
  └─ vendor=generic ─→ 其他任何 OpenAI 兼容服务（含 Exo 等分布式框架）
OllamaAdapter ─────→ 本地 Ollama 部署的任何模型
VLLMAdapter ───────→ 本地 vLLM 部署的任何模型 (NVIDIA GPU)
MLXAdapter ────────→ 本地 MLX 部署的任何模型 (Apple Silicon)

6 个 Adapter → 覆盖 12+ 厂商 → 未来加新厂商零 Engine 改动
```

---

## 七、MLXAdapter — Apple Silicon 原生推理

### 为什么独立

MLX 是 Apple 官方的机器学习框架，专为 M 系列芯片的**统一内存架构**设计。
虽然 `mlx_lm.server` 暴露 OpenAI 兼容接口，但 MLX 有独特的本地优势值得利用。

```
MLX vs Ollama 在 Apple Silicon 上的差异:

                     Ollama                    MLX
底层引擎              llama.cpp (C++)           MLX (Apple Swift/C++)
GPU 调度              Metal (通用)              Metal (Apple 深度优化)
内存管理              标准分配                   统一内存零拷贝
                                               → CPU/GPU 共享内存，无数据搬移
量化格式              GGUF                      MLX 原生格式 + safetensors
模型生态              Ollama Hub (最大)          HuggingFace MLX 社区 (在增长)
推理性能              1x (基准)                  1.2-1.5x (Apple 优化加成)
内存效率              标准                       更优（统一内存，碎片更少）
API                  OpenAI 兼容                OpenAI 兼容 (mlx_lm.server)
多机分布式            ❌                         ❌ (需要 Exo 等框架)
LoRA 微调             ❌                         ✅ mlx_lm.lora 支持本地微调
```

### MLXAdapter 独立的理由

```
1. 统一内存优化
   Ollama: 通过 Metal 使用 GPU，但内存管理是通用的
   MLX:    直接操作统一内存，CPU/GPU 零拷贝
   → 同一台 Mac，同一个模型，MLX 通常比 Ollama 快 20-50%
   → 值得在 Capability 中体现差异

2. 本地 LoRA 微调
   Ollama: 不支持微调
   MLX:    支持 LoRA 微调 → 可以微调 DBA 领域模型
   → 未来 OpenDB 可以提供微调后的诊断模型
   → Capability 需要声明微调能力

3. 量化格式不同
   Ollama: GGUF 格式（llama.cpp 生态）
   MLX:    MLX 原生格式 / safetensors
   → 模型加载路径不同，不能混用

4. 无认证 + 本地特性
   和 Ollama 一样: IsLocal=true, 无认证, 可变上下文窗口
   → 但如果塞进 OllamaAdapter，需要区分 "底层是 llama.cpp 还是 MLX"
   → 独立更干净

5. Apple 生态持续投入
   Apple 在持续加强 MLX 能力（每个 macOS 版本都有更新）
   → 未来可能有更多 Apple Silicon 专属优化值得利用
   → 独立 Adapter 预留了扩展空间
```

### MLXAdapter Capability

```go
func (a *MLXAdapter) Capability() *ProviderCapability {
    return &ProviderCapability{
        Name:             "mlx",
        MaxContextWindow: 32_768,   // 模型依赖，可配置
        Thinking: ThinkingCapability{
            Supported:       true,
            Mode:            ThinkingAutoTags,   // <think> 标签
            MultiTurnPolicy: ThinkingStripAll,
            ExtractField:    "",                 // 从 content 提取
        },
        Caching: CachingCapability{
            Mode: CachingNone,   // MLX 当前无 prefix caching
        },
        ToolCalling: ToolCallingCapability{
            Supported:    true,
            Format:       ToolFormatOpenAICompatible,
            TextFallback: true,  // 不支持时降级文本模拟
        },
        RateLimit: RateLimitCapability{
            IsLocal: true,       // 本地不重试网络错误
        },
        Output: OutputCapability{
            SupportsStructuredOutput: false, // mlx_lm.server 暂不支持 guided
        },
    }
}
```

### 配置示例

```yaml
models:
  - name: qwen-mlx
    provider: mlx
    vendor: Apple
    model: mlx-community/Qwen2.5-7B-Instruct-4bit
    capability: small
    description: "Qwen 7B on MLX (Apple Silicon optimized)"

  - name: qwen-ollama
    provider: ollama
    vendor: Qwen
    model: qwen3.5:9b
    capability: small
    description: "Qwen 9B on Ollama"
```

### 3 个本地 Adapter 的定位总结

```
OllamaAdapter   → "开箱即用"      Mac/Linux 都支持，最大模型生态，开发测试首选
MLXAdapter      → "Apple 原生"    Mac 专属，性能更优，支持微调，Apple 生态
VLLMAdapter     → "生产级部署"    Linux + NVIDIA 专属，高吞吐，多用户

选型指南:
  Mac 开发测试，追求简单     → Ollama
  Mac 开发测试，追求性能     → MLX
  Mac 本地微调 DBA 模型     → MLX (唯一选择)
  Linux 生产，单用户        → Ollama
  Linux 生产，多用户高吞吐   → vLLM
```
