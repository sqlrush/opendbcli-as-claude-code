# 02 — 核心类型设计

## 现有类型 vs 新类型对比

```
现有 llm.Message (5字段)              新 engine.Message (8字段)
─────────────────────                ──────────────────────
Role                                 Role
Content                              Content
ReasoningContent                     → 统一到 Thinking
ToolCalls                            ToolCalls
ToolCallID                           ToolCallID
                                     + IsMeta        (隐藏消息标记)
                                     + ThinkingBlocks(结构化思维)
                                     + CacheControl  (缓存标记)

现有 llm.ChatRequest (4字段)          新 engine.Request (8字段+扩展)
──────────────────────────           ─────────────────────────────
Messages                             Messages
Tools                                Tools
MaxTokens                            MaxTokens
Temperature                          Temperature
                                     + SystemPrompt  (独立系统提示)
                                     + Capability     (厂商能力引用)
                                     + Extra          (厂商专属参数)
                                     + Stream         (是否流式)

现有 llm.Response (6字段)             新 engine.Response (10字段)
───────────────────────              ──────────────────────────
Content                              Content
ReasoningContent                     → 统一到 Thinking
Thinking                             Thinking
ToolCalls                            ToolCalls
Usage                                Usage (扩展)
StopReason                           StopReason
                                     + CacheStats    (缓存命中统计)
                                     + RawHeaders    (原始响应头)
                                     + Truncated     (输出是否被截断)
```

## 详细类型定义

```go
package engine

// ═══════════════════════════════════════════════════════
// Message — 对话消息
// ═══════════════════════════════════════════════════════

type Message struct {
    Role      string      `json:"role"`       // system / user / assistant / tool
    Content   string      `json:"content,omitempty"`
    ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
    ToolCallID string     `json:"tool_call_id,omitempty"`

    // 思维/推理内容（统一字段，不再区分 ReasoningContent/Thinking）
    // 由 ProviderAdapter 从各厂商格式统一提取
    Thinking  string      `json:"thinking,omitempty"`

    // 结构化思维块（Anthropic 专用：thinking blocks + signature）
    // 其他厂商不使用此字段
    ThinkingBlocks []ThinkingBlock `json:"thinking_blocks,omitempty"`

    // 隐藏消息标记 — 类似 Claude Code 的 isMeta
    // 用于注入上下文：模型看到，用户看不到
    IsMeta    bool        `json:"is_meta,omitempty"`

    // 缓存控制（Anthropic 显式缓存）
    CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type ThinkingBlock struct {
    Type      string `json:"type"`       // "thinking"
    Thinking  string `json:"thinking"`   // 思维内容
    Signature string `json:"signature"`  // Anthropic 签名
}

type CacheControl struct {
    Type string `json:"type"` // "ephemeral"
    TTL  string `json:"ttl,omitempty"`  // "1h" 或空(默认5min)
}

type ToolCall struct {
    ID        string `json:"id"`
    Name      string `json:"name"`
    Arguments string `json:"arguments"`
}

// ═══════════════════════════════════════════════════════
// Request — API 请求
// ═══════════════════════════════════════════════════════

type Request struct {
    // 基础字段（所有厂商通用）
    Messages    []Message
    Tools       []ToolSchema
    MaxTokens   int
    Temperature *float64
    Stream      bool

    // 系统提示（独立于 messages，某些厂商如 Gemini 要求分开传）
    SystemPrompt []SystemPromptBlock

    // 厂商能力引用（Engine 根据此字段决定加什么参数）
    Capability   *ProviderCapability

    // 厂商专属参数（由 ProviderAdapter 填充）
    // 例: {"thinking": {"type": "adaptive"}, "effort": "high"}
    Extra        map[string]any
}

type SystemPromptBlock struct {
    Text         string        `json:"text"`
    CacheControl *CacheControl `json:"cache_control,omitempty"`
}

type ToolSchema struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    InputSchema any    `json:"input_schema"` // JSON Schema
}

// ═══════════════════════════════════════════════════════
// Response — API 响应
// ═══════════════════════════════════════════════════════

type Response struct {
    Content    string
    Thinking   string           // 统一的思维内容（纯文本）
    ToolCalls  []ToolCall
    StopReason string           // stop / tool_use / length / max_tokens / ...
    Usage      Usage
    CacheStats CacheStats       // 缓存命中统计
    Truncated  bool             // 输出是否因 max_tokens 被截断
    RawHeaders map[string]string // 原始响应头（用于限流解析）
}

// ═══════════════════════════════════════════════════════
// Usage — 扩展的 token 用量统计
// ═══════════════════════════════════════════════════════

type Usage struct {
    InputTokens        int  // 输入 token
    OutputTokens       int  // 输出 token
    ThinkingTokens     int  // 推理 token（OpenAI reasoning_tokens 等）
    CacheCreationTokens int // 缓存创建 token（Anthropic）
    CacheReadTokens    int  // 缓存命中 token（Anthropic/DeepSeek）
    CacheMissTokens    int  // 缓存未命中 token（DeepSeek）
}

// TotalInputCost 返回实际计费的输入 token 数
// 缓存命中通常按 0.1x 计费
func (u Usage) TotalInputCost() int {
    return u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens/10
}

type CacheStats struct {
    Hit      bool // 是否有缓存命中
    HitTokens  int
    MissTokens int
}

// ═══════════════════════════════════════════════════════
// Engine 输入/输出
// ═══════════════════════════════════════════════════════

type EngineInput struct {
    // 用户输入
    UserMessage  string

    // 上下文数据（由调用方提供）
    CompressedReport string            // Sentinel 压缩报告
    DatabaseInfo     DatabaseInfo      // 数据库连接信息
    Metadata         map[string]string // 附加元数据

    // 配置
    Mode        DiagnoseMode  // playbook / assist / auto
    MaxTurns    int           // 0 = 使用默认值
    OnRound     func(turn int, toolNames []string) // 进度回调
    OnStream    func(delta string)                  // 流式输出回调
}

type DatabaseInfo struct {
    Product  string // oracle / mysql / postgres / opengauss
    Version  string // 19c, 8.0, 16, etc.
    Instance string // 实例名
    Host     string
}

type DiagnoseMode string

const (
    ModePlaybook DiagnoseMode = "playbook"
    ModeAssist   DiagnoseMode = "assist"
    ModeAuto     DiagnoseMode = "auto"
)

type EngineResult struct {
    Content       string        // 最终诊断文本
    Thinking      string        // 思维链（如果有）
    TotalUsage    Usage         // 累计 token 用量
    TurnsUsed     int           // 使用的轮次数
    MaxTurnsHit   bool          // 是否达到最大轮次
    ToolsInvoked  []string      // 调用过的工具列表
    Errors        []TurnError   // 各轮次的错误（如果有）
}

type TurnError struct {
    Turn    int
    Tool    string
    Error   string
}

// ═══════════════════════════════════════════════════════
// Stream 事件（增强版）
// ═══════════════════════════════════════════════════════

type StreamEventType uint8

const (
    StreamTextDelta     StreamEventType = iota
    StreamThinkingDelta                         // 思维内容增量
    StreamToolCallDelta
    StreamToolResult                            // 工具执行结果
    StreamDone
    StreamError                                 // 流式错误（不中断）
)

type StreamEvent struct {
    Type     StreamEventType
    Content  string
    ToolCall *ToolCall
    Error    error
}
```

## 设计要点

### 1. 思维内容统一

现有代码有三个不同的字段处理思维内容：
- `ReasoningContent` — Kimi 的 reasoning_content
- `Thinking` — Qwen/DeepSeek 的 `<think>` 标签提取
- `stripThink` — 配置项，决定是否剥离

新设计统一为：
- `Message.Thinking` — 纯文本思维内容（所有厂商统一）
- `Message.ThinkingBlocks` — 结构化思维（仅 Anthropic，需要 signature）
- 提取逻辑在 ProviderAdapter 中完成，Engine 只看统一字段

### 2. Extra 扩展机制

`Request.Extra` 是厂商专属参数的通道：
```go
// Anthropic
extra["thinking"] = map[string]any{"type": "adaptive"}
extra["effort"] = "high"
extra["speed"] = "fast"

// OpenAI
extra["reasoning"] = map[string]any{"effort": "high"}
extra["parallel_tool_calls"] = true

// Qwen
extra["enable_thinking"] = true
extra["thinking_budget"] = 4096

// Gemini
extra["thinkingConfig"] = map[string]any{"thinkingBudget": -1}
extra["safetySettings"] = [...]
```

Engine 填充 Extra，ProviderAdapter 在序列化时合并到请求体。

### 3. IsMeta 隐藏消息

借鉴 Claude Code 的 `isMeta: true`：
```go
// 注入数据库环境信息，用户不可见
msg := Message{
    Role:    "user",
    Content: "<system-reminder>当前数据库: Oracle 19c, 实例: orcl...</system-reminder>",
    IsMeta:  true,
}
```

渲染层检查 `IsMeta`，跳过不展示给用户。
