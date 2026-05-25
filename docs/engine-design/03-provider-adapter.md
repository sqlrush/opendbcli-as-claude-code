# 03 — ProviderAdapter 设计

## 接口定义

```go
package provider

// ProviderAdapter — 统一的厂商适配接口
// 替代现有的 llm.Provider，增加能力声明和请求/响应转换
type ProviderAdapter interface {
    // 基础通信
    Chat(ctx context.Context, req *engine.Request) (*engine.Response, error)
    ChatStream(ctx context.Context, req *engine.Request) (engine.Stream, error)

    // 能力声明
    Capability() *ProviderCapability

    // 请求增强 — 根据厂商能力填充 Extra 参数
    // Engine 调用此方法让 adapter 注入厂商专属参数
    EnhanceRequest(req *engine.Request)

    // 响应归一化 — 从厂商特定格式提取统一字段
    // 思维内容提取、缓存统计、限流头解析等
    NormalizeResponse(raw *RawHTTPResponse) (*engine.Response, error)

    // 限流信息解析
    ParseRateLimitHeaders(headers http.Header) *RateLimitInfo

    // 名称标识
    Name() string
}

// RawHTTPResponse — 原始 HTTP 响应（adapter 内部使用）
type RawHTTPResponse struct {
    StatusCode int
    Headers    http.Header
    Body       []byte
}

// RateLimitInfo — 解析后的限流信息
type RateLimitInfo struct {
    RetryAfterSeconds float64
    RemainingRequests int
    RemainingTokens   int
    ResetTime         time.Time
}
```

## Capability 完整定义

```go
// ProviderCapability — 厂商能力声明（完整版）
type ProviderCapability struct {
    Name             string
    MaxContextWindow int
    MaxOutputTokens  int

    Thinking    ThinkingCapability
    Caching     CachingCapability
    ToolCalling ToolCallingCapability
    RateLimit   RateLimitCapability
    Output      OutputCapability
}

// ── 思维模式 ──

type ThinkingMode int

const (
    ThinkingNone         ThinkingMode = iota
    ThinkingAdaptive                          // Anthropic
    ThinkingEffortLevel                       // OpenAI
    ThinkingBudget                            // Gemini
    ThinkingEnableFlag                        // Qwen/MiMo/Kimi/GLM
    ThinkingAutoTags                          // DeepSeek/Ollama本地
    ThinkingSplit                             // MiniMax
)

type ThinkingMultiTurnPolicy int

const (
    ThinkingPreserveAll      ThinkingMultiTurnPolicy = iota // Anthropic/OpenAI
    ThinkingStripBetweenTurns                               // DeepSeek/Kimi/Qwen/GLM/MiMo
    ThinkingStripAll                                        // 不保留
)

type ThinkingCapability struct {
    Supported       bool
    Mode            ThinkingMode
    MultiTurnPolicy ThinkingMultiTurnPolicy

    // 思维内容在响应 JSON 中的字段名
    // "thinking_blocks" / "reasoning_content" / "reasoning_details" / "thought_parts" / ""
    ExtractField    string

    // 生成开启思维模式的请求参数
    // level: "low" / "medium" / "high" / "max"
    EnableParams    func(level string) map[string]any
}

// ── 缓存 ──

type CachingMode int

const (
    CachingNone        CachingMode = iota
    CachingExplicit                         // Anthropic: 客户端标记断点
    CachingAutomatic                        // DeepSeek/OpenAI/GLM: 服务端自动
    CachingSeparateAPI                      // Gemini: 独立 API
)

type CachingCapability struct {
    Mode           CachingMode
    MaxBreakpoints int    // Anthropic: 4
    MinCacheTokens int    // 最小可缓存 token 数
    CacheReadField  string // 响应中缓存命中字段名
    CacheMissField  string // 响应中缓存未命中字段名
    CacheCreateField string // 响应中缓存创建字段名
}

// ── 工具调用 ──

type ToolCallFormat int

const (
    ToolFormatOpenAICompatible ToolCallFormat = iota // 大部分厂商
    ToolFormatAnthropicNative                         // Anthropic
    ToolFormatGeminiNative                            // Gemini
    ToolFormatTextSimulation                          // 弱模型文本模拟
)

type ToolCallingCapability struct {
    Supported          bool
    Format             ToolCallFormat
    SupportsParallel   bool   // OpenAI parallel_tool_calls
    SupportsStrict     bool   // 严格 schema 验证
    TextFallback       bool   // 不支持 native 时可降级到文本模拟
    ThinkingConstraints []string // thinking 模式下的工具调用限制
}

// ── 限流 ──

type RateLimitCapability struct {
    HeaderPrefix  string  // "anthropic-ratelimit" / "x-ratelimit" / ""
    HasRetryAfter bool
    OverloadCode  int     // 529(Anthropic) / 0
    IsLocal       bool    // 本地部署不重试网络错误
}

// ── 输出控制 ──

type OutputCapability struct {
    SupportsEffort          bool
    EffortLevels            []string  // ["low","medium","high","max"]
    SupportsSpeed           bool
    SupportsTaskBudget      bool
    SupportsStructuredOutput bool
    SupportsPredictedOutput  bool
    SupportsSeed            bool
    FixedTemperature        bool     // 某些模型不允许设 temperature
}
```

## 各厂商 Adapter 实现

### Anthropic Adapter

```go
type AnthropicAdapter struct {
    baseURL    string
    apiKey     string
    model      string
    version    string // anthropic-version header
    betas      []string
    httpClient *http.Client
}

func (a *AnthropicAdapter) Capability() *ProviderCapability {
    return &ProviderCapability{
        Name:             "anthropic",
        MaxContextWindow: 1_000_000,  // Opus 4.6
        MaxOutputTokens:  128_000,
        Thinking: ThinkingCapability{
            Supported:       true,
            Mode:            ThinkingAdaptive,
            MultiTurnPolicy: ThinkingPreserveAll,
            ExtractField:    "thinking_blocks",
            EnableParams: func(level string) map[string]any {
                return map[string]any{
                    "thinking": map[string]any{"type": "adaptive"},
                }
            },
        },
        Caching: CachingCapability{
            Mode:             CachingExplicit,
            MaxBreakpoints:   4,
            MinCacheTokens:   1024,
            CacheReadField:   "cache_read_input_tokens",
            CacheCreateField: "cache_creation_input_tokens",
        },
        ToolCalling: ToolCallingCapability{
            Supported:          true,
            Format:             ToolFormatAnthropicNative,
            SupportsStrict:     true,
            ThinkingConstraints: []string{"tool_choice must be auto or none"},
        },
        RateLimit: RateLimitCapability{
            HeaderPrefix:  "anthropic-ratelimit",
            HasRetryAfter: true,
            OverloadCode:  529,
        },
        Output: OutputCapability{
            SupportsEffort:     true,
            EffortLevels:       []string{"low", "medium", "high", "max"},
            SupportsSpeed:      true,
            SupportsTaskBudget: true,
        },
    }
}

func (a *AnthropicAdapter) EnhanceRequest(req *engine.Request) {
    caps := a.Capability()

    // 思维模式
    if caps.Thinking.Supported {
        req.Extra["thinking"] = map[string]any{"type": "adaptive"}
    }

    // Effort
    if caps.Output.SupportsEffort {
        req.Extra["effort"] = "high" // 可从 EngineConfig 配置
    }

    // Task Budget
    if caps.Output.SupportsTaskBudget {
        // Engine 计算剩余预算后传入
        if budget, ok := req.Extra["_remaining_budget"]; ok {
            req.Extra["output_config"] = map[string]any{
                "task_budget": budget,
            }
        }
    }

    // Prompt Cache 标记
    if caps.Caching.Mode == CachingExplicit {
        markCacheBreakpoints(req)
    }

    // Beta headers
    req.Extra["_anthropic_betas"] = a.betas
    req.Extra["_anthropic_version"] = a.version
}

// Chat — Anthropic 原生 API 格式（非 OpenAI 兼容）
func (a *AnthropicAdapter) Chat(ctx context.Context, req *engine.Request) (*engine.Response, error) {
    a.EnhanceRequest(req)
    body := a.buildAnthropicRequestBody(req) // 转换为 Anthropic messages API 格式
    // ... HTTP 调用 ...
    return a.NormalizeResponse(raw)
}
```

### OpenAI Compatible Adapter（通用，覆盖大部分国产模型）

```go
type OpenAICompatAdapter struct {
    baseURL     string
    apiKey      string
    model       string
    vendor      string  // "openai" / "deepseek" / "kimi" / "qwen" / "glm" / "minimax" / "mimo"
    httpClient  *http.Client
    capOverride *ProviderCapability // 厂商特定能力覆盖
}

// 根据 vendor 返回不同能力
func (a *OpenAICompatAdapter) Capability() *ProviderCapability {
    switch a.vendor {
    case "deepseek":
        return a.deepseekCapability()
    case "kimi":
        return a.kimiCapability()
    case "qwen":
        return a.qwenCapability()
    case "glm":
        return a.glmCapability()
    case "minimax":
        return a.minimaxCapability()
    case "mimo":
        return a.mimoCapability()
    case "openai":
        return a.openaiCapability()
    default:
        return a.genericCapability() // 最小公约数
    }
}

func (a *OpenAICompatAdapter) deepseekCapability() *ProviderCapability {
    return &ProviderCapability{
        Name:             "deepseek",
        MaxContextWindow: 128_000,
        Thinking: ThinkingCapability{
            Supported:       true,
            Mode:            ThinkingAutoTags,
            MultiTurnPolicy: ThinkingStripBetweenTurns,
            ExtractField:    "reasoning_content",
            EnableParams: func(level string) map[string]any {
                return map[string]any{
                    "thinking": map[string]any{"type": "enabled"},
                }
            },
        },
        Caching: CachingCapability{
            Mode:           CachingAutomatic,
            CacheReadField: "prompt_cache_hit_tokens",
            CacheMissField: "prompt_cache_miss_tokens",
        },
        ToolCalling: ToolCallingCapability{
            Supported: true,
            Format:    ToolFormatOpenAICompatible,
        },
        Output: OutputCapability{
            FixedTemperature: true, // reasoner 不支持 temperature
        },
        // ...
    }
}

func (a *OpenAICompatAdapter) qwenCapability() *ProviderCapability {
    return &ProviderCapability{
        Name:             "qwen",
        MaxContextWindow: 1_000_000,
        Thinking: ThinkingCapability{
            Supported:       true,
            Mode:            ThinkingEnableFlag,
            MultiTurnPolicy: ThinkingStripBetweenTurns,
            ExtractField:    "reasoning_content",
            EnableParams: func(level string) map[string]any {
                return map[string]any{"enable_thinking": true}
            },
        },
        ToolCalling: ToolCallingCapability{
            Supported: true,
            Format:    ToolFormatOpenAICompatible,
        },
        // ...
    }
}

// EnhanceRequest — 根据 vendor 注入专属参数
func (a *OpenAICompatAdapter) EnhanceRequest(req *engine.Request) {
    caps := a.Capability()

    if caps.Thinking.Supported && caps.Thinking.EnableParams != nil {
        for k, v := range caps.Thinking.EnableParams("high") {
            req.Extra[k] = v
        }
    }

    // Qwen 专属: 增量输出
    if a.vendor == "qwen" {
        req.Extra["incremental_output"] = true
    }
}

// NormalizeResponse — 统一提取思维内容
func (a *OpenAICompatAdapter) NormalizeResponse(raw *RawHTTPResponse) (*engine.Response, error) {
    var oaiResp oaiResponseFull // 扩展的 OpenAI 响应结构
    json.Unmarshal(raw.Body, &oaiResp)

    resp := &engine.Response{
        Content:    oaiResp.Choices[0].Message.Content,
        StopReason: oaiResp.Choices[0].FinishReason,
        Usage: engine.Usage{
            InputTokens:  oaiResp.Usage.PromptTokens,
            OutputTokens: oaiResp.Usage.CompletionTokens,
        },
        RawHeaders: headerToMap(raw.Headers),
    }

    caps := a.Capability()

    // 思维内容提取（统一入口）
    switch caps.Thinking.ExtractField {
    case "reasoning_content":
        resp.Thinking = oaiResp.Choices[0].Message.ReasoningContent
    case "reasoning_details":
        resp.Thinking = oaiResp.Choices[0].Message.ReasoningDetails
    }

    // 思维内容可能在 content 中以 <think> 标签存在
    if caps.Thinking.Mode == ThinkingAutoTags && resp.Thinking == "" {
        resp.Thinking, resp.Content = extractThinkTags(resp.Content)
    }

    // 缓存统计
    if caps.Caching.CacheReadField != "" {
        resp.CacheStats.HitTokens = oaiResp.Usage.Extra[caps.Caching.CacheReadField]
        resp.CacheStats.Hit = resp.CacheStats.HitTokens > 0
    }

    // 输出截断检测
    resp.Truncated = (resp.StopReason == "length" || resp.StopReason == "max_tokens")

    return resp, nil
}
```

### Ollama Adapter

```go
type OllamaAdapter struct {
    baseURL    string
    model      string
    httpClient *http.Client
}

func (a *OllamaAdapter) Capability() *ProviderCapability {
    return &ProviderCapability{
        Name:             "ollama",
        MaxContextWindow: 32_768,  // 模型依赖，可配置
        Thinking: ThinkingCapability{
            Supported:       true,
            Mode:            ThinkingAutoTags,
            MultiTurnPolicy: ThinkingStripAll,
            ExtractField:    "", // 从 content 中提取 <think> 标签
        },
        Caching: CachingCapability{
            Mode: CachingNone,
        },
        ToolCalling: ToolCallingCapability{
            Supported:    true,  // 兼容模型支持
            Format:       ToolFormatOpenAICompatible,
            TextFallback: true,  // 不支持时降级到文本模拟
        },
        RateLimit: RateLimitCapability{
            IsLocal: true, // 本地不需要重试网络错误
        },
    }
}
```

## Adapter 工厂

```go
// NewAdapter 根据配置创建对应的 ProviderAdapter
func NewAdapter(profile model.ModelProfile) (ProviderAdapter, error) {
    switch {
    case profile.Provider == "anthropic":
        return NewAnthropicAdapter(profile), nil

    case profile.Provider == "gemini":
        return NewGeminiAdapter(profile), nil

    case profile.Provider == "ollama":
        return NewOllamaAdapter(profile), nil

    case profile.Provider == "vllm":
        return NewVLLMAdapter(profile), nil

    case profile.Provider == "openai":
        // OpenAI 和国产模型统一用 OpenAICompatAdapter
        // 通过 vendor 字段区分能力
        vendor := profile.Vendor
        if vendor == "" {
            vendor = detectVendor(profile.BaseURL)
        }
        return NewOpenAICompatAdapter(profile, vendor), nil

    default:
        // 未知 provider 走通用 OpenAI 兼容
        return NewOpenAICompatAdapter(profile, "generic"), nil
    }
}

// detectVendor 根据 baseURL 自动检测厂商
func detectVendor(baseURL string) string {
    switch {
    case strings.Contains(baseURL, "deepseek.com"):
        return "deepseek"
    case strings.Contains(baseURL, "moonshot.ai"):
        return "kimi"
    case strings.Contains(baseURL, "dashscope"):
        return "qwen"
    case strings.Contains(baseURL, "bigmodel.cn"):
        return "glm"
    case strings.Contains(baseURL, "minimax.io"):
        return "minimax"
    case strings.Contains(baseURL, "xiaomimimo.com"):
        return "mimo"
    case strings.Contains(baseURL, "openai.com"):
        return "openai"
    default:
        return "generic"
    }
}
```

## 配置示例 (models.yaml)

```yaml
models:
  - name: claude-opus
    provider: anthropic
    vendor: Anthropic
    base_url: https://api.anthropic.com
    model: claude-opus-4-6-20250303
    api_key: ${ANTHROPIC_API_KEY}
    capability: large

  - name: deepseek-r1
    provider: openai       # OpenAI 兼容协议
    vendor: deepseek       # 但厂商是 DeepSeek → 启用专属优化
    base_url: https://api.deepseek.com
    model: deepseek-reasoner
    api_key: ${DEEPSEEK_API_KEY}
    capability: large

  - name: qwen-local
    provider: ollama
    vendor: Qwen
    model: qwen3.5:9b
    capability: small

  - name: gemini-pro
    provider: gemini
    vendor: Google
    base_url: https://generativelanguage.googleapis.com
    model: gemini-2.5-pro
    api_key: ${GEMINI_API_KEY}
    capability: large
```
