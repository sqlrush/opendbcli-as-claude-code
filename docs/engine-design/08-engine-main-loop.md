# 08 — Engine 主循环设计

## 这是整个项目的核心：统一 Agent Loop

替代现有 4 套分散的 loop.go，成为所有 DB 类型共用的链式推理引擎。

## Engine 结构

```go
package engine

type Engine struct {
    provider       provider.ProviderAdapter
    contextBuilder *context.Builder
    contextManager *context.Manager
    toolOrch       *tool.Orchestrator
    resultHandler  *tool.ResultHandler
    retryPolicy    *retry.Policy
    config         EngineConfig
}

type EngineConfig struct {
    DefaultMaxTurns     int           // 默认最大轮次
    DefaultMaxTokens    int           // 默认最大输出 token
    ThinkingLevel       string        // 思维模式级别: low/medium/high
    StreamFinalOnly     bool          // 仅最后一轮流式（兼容模式）
    EnablePreExecution  bool          // 启用流式工具预执行
    EnableCompression   bool          // 启用上下文压缩
    MaxOutputRecoveries int           // max_output_tokens 恢复次数，默认 2
}

func DefaultConfig() EngineConfig {
    return EngineConfig{
        DefaultMaxTurns:     20,
        DefaultMaxTokens:    8000,
        ThinkingLevel:       "high",
        StreamFinalOnly:     false,
        EnablePreExecution:  true,
        EnableCompression:   true,
        MaxOutputRecoveries: 2,
    }
}

func New(
    adapter provider.ProviderAdapter,
    profile profile.PromptProfile,
    executor tool.SkillExecutor,
    opts ...Option,
) *Engine
```

## 主循环

```go
func (e *Engine) Run(ctx context.Context, input EngineInput) (*EngineResult, error) {
    caps := e.provider.Capability()

    // ── 阶段 1: 构建初始上下文 ──
    built := e.contextBuilder.Build(input)

    maxTurns := input.MaxTurns
    if maxTurns == 0 {
        maxTurns = e.config.DefaultMaxTurns
    }

    // 根据模式调整
    switch input.Mode {
    case ModePlaybook:
        maxTurns = 1
    case ModeAssist:
        if maxTurns > 20 {
            maxTurns = 20
        }
    }

    result := &EngineResult{}
    messages := built.Messages
    tools := built.Tools
    var totalUsage Usage
    var outputRecoveries int

    // ── 阶段 2: 主循环 ──
    for turn := 0; turn < maxTurns; turn++ {
        // 2a. 上下文管理：检查是否需要压缩
        if e.config.EnableCompression && turn > 0 {
            if e.contextManager.ShouldBlock(messages) {
                // 超过 95% 容量，强制压缩
                messages = e.contextManager.ForceCompress(messages)
            } else {
                compressed, did := e.contextManager.MaybeCompress(messages)
                if did {
                    messages = compressed
                }
            }
        }

        // 2b. 注入轮次上下文（收敛引导等）
        messagesWithContext := e.contextBuilder.InjectTurnContext(messages, turn, maxTurns)

        // 2c. 构建请求
        req := e.buildRequest(built.SystemPrompt, messagesWithContext, tools, turn)

        // 2d. 调用 API（含重试）
        resp, err := e.callWithRetry(ctx, req)
        if err != nil {
            // 413 上下文过大 → 压缩后重试一次
            if isContextTooLong(err) {
                messages = e.contextManager.ForceCompress(messages)
                messagesWithContext = e.contextBuilder.InjectTurnContext(messages, turn, maxTurns)
                req = e.buildRequest(built.SystemPrompt, messagesWithContext, tools, turn)
                resp, err = e.callWithRetry(ctx, req)
            }
            if err != nil {
                result.Errors = append(result.Errors, TurnError{Turn: turn, Error: err.Error()})
                return result, fmt.Errorf("turn %d: %w", turn, err)
            }
        }

        // 2e. 累计 usage
        totalUsage = totalUsage.Add(resp.Usage)

        // 2f. 输出截断恢复
        if resp.Truncated && outputRecoveries < e.config.MaxOutputRecoveries {
            outputRecoveries++
            // 升级 max_tokens 并注入续写提示
            upgraded := e.recoverTruncatedOutput(ctx, req, resp, messages)
            if upgraded != nil {
                resp = upgraded
                totalUsage = totalUsage.Add(upgraded.Usage)
            }
        }

        // 2g. 没有工具调用 → 完成
        if len(resp.ToolCalls) == 0 {
            result.Content = resp.Content
            result.Thinking = resp.Thinking
            result.TotalUsage = totalUsage
            result.TurnsUsed = turn + 1
            return result, nil
        }

        // 2h. 有工具调用 → 执行工具
        // 追加 assistant 消息到历史
        assistantMsg := engine.Message{
            Role:           "assistant",
            Content:        resp.Content,
            Thinking:       resp.Thinking,
            ThinkingBlocks: resp.ThinkingBlocks,
            ToolCalls:      resp.ToolCalls,
        }

        // 根据厂商策略处理思维内容
        messages = e.contextBuilder.PrepareMessagesForNextTurn(
            append(messages, assistantMsg), false,
        )

        // 执行工具
        remaining := e.contextManager.RemainingTokens(messages)
        toolResults := e.toolOrch.Execute(ctx, resp.ToolCalls)
        toolResults = e.resultHandler.Process(toolResults, remaining)

        // 工具名记录
        for _, tr := range toolResults {
            result.ToolsInvoked = append(result.ToolsInvoked, tr.Name)
            if tr.Error != "" {
                result.Errors = append(result.Errors, TurnError{
                    Turn: turn, Tool: tr.Name, Error: tr.Error,
                })
            }
        }

        // 追加工具结果到历史
        for _, tr := range toolResults {
            messages = append(messages, engine.Message{
                Role:       "tool",
                Content:    tr.Content,
                ToolCallID: tr.ToolCallID,
            })
        }

        // 进度回调
        if input.OnRound != nil {
            names := make([]string, len(resp.ToolCalls))
            for i, tc := range resp.ToolCalls {
                names[i] = tc.Name
            }
            input.OnRound(turn+1, names)
        }
    }

    // ── 阶段 3: 最大轮次已达到 ──
    result.MaxTurnsHit = true
    result.TurnsUsed = maxTurns
    result.TotalUsage = totalUsage

    // 最后一轮 assistant 的 content 作为最终结果
    for i := len(messages) - 1; i >= 0; i-- {
        if messages[i].Role == "assistant" && messages[i].Content != "" {
            result.Content = messages[i].Content
            break
        }
    }

    return result, nil
}
```

## 请求构建

```go
func (e *Engine) buildRequest(
    systemPrompt []SystemPromptBlock,
    messages []Message,
    tools []ToolSchema,
    turn int,
) *Request {
    caps := e.provider.Capability()

    req := &Request{
        Messages:     messages,
        Tools:        tools,
        SystemPrompt: systemPrompt,
        MaxTokens:    e.config.DefaultMaxTokens,
        Stream:       !e.config.StreamFinalOnly || turn > 0,
        Capability:   caps,
        Extra:        make(map[string]any),
    }

    // 剩余 token 预算（供 task_budget 使用）
    remaining := e.contextManager.RemainingTokens(messages)
    req.Extra["_remaining_budget"] = remaining

    // 让 adapter 注入厂商专属参数
    e.provider.EnhanceRequest(req)

    return req
}
```

## max_output_tokens 截断恢复

```go
func (e *Engine) recoverTruncatedOutput(
    ctx context.Context,
    originalReq *Request,
    truncatedResp *Response,
    messages []Message,
) *Response {
    // 升级 max_tokens
    newMaxTokens := e.config.DefaultMaxTokens * 4
    caps := e.provider.Capability()
    if newMaxTokens > caps.MaxOutputTokens {
        newMaxTokens = caps.MaxOutputTokens
    }

    // 注入续写提示
    continueMsg := Message{
        Role:    "user",
        Content: "<system-reminder>你的上一条回复因长度限制被截断。请从截断处继续，直接续写内容，不要重复已输出的部分。</system-reminder>",
        IsMeta:  true,
    }

    resumeMessages := append(messages,
        Message{Role: "assistant", Content: truncatedResp.Content},
        continueMsg,
    )

    req := e.buildRequest(originalReq.SystemPrompt, resumeMessages, originalReq.Tools, 0)
    req.MaxTokens = newMaxTokens

    resp, err := e.callWithRetry(ctx, req)
    if err != nil {
        return nil
    }

    // 合并内容
    resp.Content = truncatedResp.Content + resp.Content
    return resp
}
```

## Playbook 模式（单轮，可流式）

```go
// playbook 模式走主循环（maxTurns=1），但支持流式输出
// 不需要特殊处理，主循环 turn=0 时 len(toolCalls)==0 直接返回
```

## 双路径保留（text模拟 fallback）

```go
// 当 ToolCallingCapability.TextFallback == true 时
// Engine 检测到 native function calling 失败后
// 切换到 PromptLoop 兼容模式

func (e *Engine) Run(ctx context.Context, input EngineInput) (*EngineResult, error) {
    caps := e.provider.Capability()

    if caps.ToolCalling.Supported {
        result, err := e.runNativeLoop(ctx, input)
        if err == nil {
            return result, nil
        }
        // native 失败且支持 text fallback → 降级
        if caps.ToolCalling.TextFallback {
            return e.runTextLoop(ctx, input)
        }
        return nil, err
    }

    // 不支持 native → 直接用 text 模拟
    if caps.ToolCalling.TextFallback {
        return e.runTextLoop(ctx, input)
    }

    return nil, fmt.Errorf("provider %s does not support tool calling", caps.Name)
}

// runTextLoop 保留现有 PromptLoop 的文本模拟逻辑
// 从现有 oracle/agent/prompt_loop.go 迁移
func (e *Engine) runTextLoop(ctx context.Context, input EngineInput) (*EngineResult, error) {
    // 使用 ```action {"skill":"xxx","args":"yyy"} ``` 文本块
    // 正则解析 + 执行
    // 复用现有逻辑，但接入 Engine 的重试/压缩/结果处理
    // ...
}
```

## 与现有代码的桥接

```go
// 现有 DiagnoseSkill 调用方式:
//   agentLoop := agent.NewAgentLoop(provider, executor, registry)
//   result := agentLoop.Run(ctx, userMessage)
//
// 新的调用方式:
//   eng := engine.New(adapter, oracleProfile, executor)
//   result := eng.Run(ctx, engine.EngineInput{
//       UserMessage: userMessage,
//       CompressedReport: report,
//       DatabaseInfo: dbInfo,
//       Mode: engine.ModeAuto,
//       OnRound: progressCallback,
//       OnStream: streamCallback,
//   })
```
