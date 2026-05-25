# 08-附 — 统一 Agent Loop 详细对比：当前实现 vs 新 Engine

## 一、当前 OpenDB 的 Agent Loop 是什么样的

### 核心：`oracle/agent/loop.go` — 60 行主循环

```go
// 当前实现（精简版）
func (a *AgentLoop) Run(ctx, userMessage) (string, error) {
    messages := [{system, prompt}, {user, userMessage}]
    tools := buildFilteredTools()

    for turn := 0; turn < maxTurns; turn++ {
        // 收敛引导（最后2轮）
        if turn >= maxTurns-2 { inject hint }

        resp, err := provider.Chat(ctx, {messages, tools})  // ← 无重试
        if err != nil { return err }                          // ← 直接失败

        if len(resp.ToolCalls) == 0 { return resp.Content }  // ← 完成

        messages = append(messages, assistantMsg)
        for _, tc := range resp.ToolCalls {
            result := executeTool(tc)                         // ← 逐个串行
            messages = append(messages, toolMsg)              // ← 无截断控制
        }
    }
    return lastContent + MaxTurnsNote                         // ← 超轮次
}
```

### MySQL/PG/OpenGauss 的 loop.go — 几乎完全复制，唯一差异

- MySQL/PG 有 `truncateResult(s string)` 硬截断 3000 字符
- Oracle 没有截断
- systemPrompt 不同

### 额外 `prompt_loop.go` — 文本模拟路径（~300行），完全独立于 AgentLoop

---

## 二、新 Engine 主循环每轮 8 个步骤对比

```
                当前 AgentLoop              新 Engine
每轮步骤        ─────────────               ────────
                                        2a. 上下文压缩检查
                收敛引导(最后2轮)          2b. 轮次上下文注入(含收敛引导)
                                        2c. 构建请求(含厂商专属参数)
                provider.Chat()           2d. callWithRetry(含重试+413恢复)
                                        2e. 累计 usage
                                        2f. 输出截断恢复
                if no tools → return      2g. if no tools → return
                串行执行工具               2h. 并发执行工具 + 结果预算控制
```

---

## 三、8 项提升逐项详解

### 提升 1：每轮开始前的上下文压缩（当前完全没有）

```
当前:
  messages 只进不出，随轮次无限增长
  10轮对话后 messages 可能有 30-50 条消息
  小模型(32K窗口) 到第5-6轮就可能超窗口 → 413错误 → 直接崩溃

新 Engine:
  每轮开始检查 token 使用率
  < 80%  → 不压缩
  80-90% → Turn Collapse（折叠早期工具调用轮次为摘要）
  > 90%  → Emergency Truncate（保留首尾，删除中间）
  > 95%  → 阻止 API 调用

  效果: 10轮诊断不再受上下文窗口限制
        32K 小模型也能跑完 10 轮
        128K+ 大模型可以跑 20 轮深度诊断
```

### 提升 2：API 调用含重试（当前出错直接终止）

```
当前:
  resp, err := provider.Chat(ctx, req)
  if err != nil {
      return "", fmt.Errorf("llm chat failed on turn %d: %w", turn, err)
  }
  // 429限流? 网络抖动? 服务过载? → 全部直接失败

新 Engine:
  resp, err := e.callWithRetry(ctx, req)
  // 内部执行:
  //   attempt 1: 失败(429) → 等 500ms
  //   attempt 2: 失败(429) → 等 1s
  //   attempt 3: 成功 → 返回
  //
  // 如果是 413(上下文过大):
  //   → 触发 ForceCompress → 压缩消息 → 重新发送
  //
  // 如果是 529(Anthropic过载):
  //   → 最多重试3次，可触发模型降级

  效果: 间歇性网络问题、API限流不再中断诊断
        413 自动压缩恢复，不需要用户干预
```

### 提升 3：输出截断自动恢复（当前模型输出被截断就丢失）

```
当前:
  模型输出被 max_tokens 截断 → StopReason="length"
  但 loop.go 不检查 StopReason
  诊断结论可能写到一半就断了，用户看到不完整的结果

新 Engine:
  if resp.Truncated && recoveries < 2 {
      // 1. 升级 max_tokens: 8000 → 32000
      // 2. 注入续写提示: "从截断处继续，不要重复"
      // 3. 重新调用 API
      // 4. 合并: 前半段 + 后半段
  }

  效果: 复杂诊断的长输出不会被截断
        最多自动续写2次，覆盖绝大部分场景
```

### 提升 4：工具并发执行（当前全部串行）

```
当前:
  for _, tc := range resp.ToolCalls {
      result := executeTool(tc)          // 一个接一个
      messages = append(messages, ...)
  }
  // 如果模型同时调用 activesessions + waits + topsql
  // 每个查询 1-2 秒，串行 = 3-6 秒

新 Engine:
  // 分区: 只读(activesessions/waits/topsql) vs 写入(kill/alter)
  readOnly, writeOps := partition(toolCalls)

  // 只读并发(最多5个同时)
  readResults := executeConcurrent(readOnly)
  // 写入串行
  writeResults := executeSerial(writeOps)

  效果: 3个只读查询并发 = 1-2秒（vs 当前3-6秒）
        每轮节省 50-70% 的工具执行时间
        安全保证: 写入操作仍然串行，不会并发 kill
```

### 提升 5：工具结果智能截断（当前要么不截断要么硬截断）

```
当前:
  Oracle: formatResult(r) → 无截断，可能返回几万字符
  MySQL:  truncateResult(s) → if len(s) > 3000 { s[:3000] + "...(truncated)" }
  问题: Oracle 大结果撑爆上下文; MySQL 3000字符可能丢失关键信息

新 Engine:
  // 根据剩余上下文窗口动态计算每个工具的预算
  remaining := contextManager.RemainingTokens(messages)
  perToolBudget := remaining * 30% / len(toolCalls)

  // 智能截断: 保留前70% + 后20% + 中间省略
  smartTruncate(content, budget)

  // 超大结果可写磁盘 + 内联摘要
  "...(完整内容 15000 字符，保存在 /tmp/tool-result-xxx.txt)"

  效果: 不会因为一个大查询结果撑爆上下文
        保留头尾信息，比硬截断更智能
        预算随剩余窗口动态调整
```

### 提升 6：厂商专属参数注入（当前只传 messages+tools）

```
当前:
  provider.Chat(ctx, llm.ChatRequest{
      Messages: messages,
      Tools:    tools,
      // 就这两个字段，所有厂商一视同仁
  })

新 Engine:
  req := buildRequest(systemPrompt, messages, tools, turn)
  provider.EnhanceRequest(req)  // adapter 注入专属参数
  // Anthropic → thinking:adaptive, effort:high, cache_control, task_budget
  // OpenAI   → reasoning.effort:high, parallel_tool_calls:true
  // Qwen     → enable_thinking:true, thinking_budget:4096
  // DeepSeek → thinking.type:enabled
  // Ollama   → 无额外参数（但<think>标签自动提取）

  效果: 每个厂商的模型都在最优配置下运行
        Anthropic 模型自动开启深度推理
        缓存命中自动标记，减90%成本
```

### 提升 7：统一代码消除重复（当前 4 份几乎相同的代码）

```
当前:
  oracle/agent/loop.go       ~400 行
  mysql/agent/loop.go        ~350 行  (90% 复制自 oracle)
  postgres/agent/loop.go     ~350 行  (90% 复制自 oracle)
  opengauss/agent/loop.go    ~350 行  (90% 复制自 oracle)
  oracle/agent/prompt_loop.go ~300 行  (文本模拟路径)
  ─────────────────────────────────
  合计: ~1750 行，其中 ~1200 行是重复代码

新 Engine:
  engine/engine.go           ~300 行  (统一主循环)
  engine/provider/adapter.go ~100 行  (接口)
  engine/context/builder.go  ~200 行  (上下文构建)
  engine/context/manager.go  ~200 行  (压缩管理)
  engine/tool/orchestrator.go ~150 行  (并发执行)
  engine/tool/result.go      ~100 行  (结果处理)
  engine/retry/policy.go     ~150 行  (重试)
  engine/profile/oracle.go   ~100 行  (Oracle特定)
  ─────────────────────────────────
  合计: ~1300 行，零重复，且功能多出 6 项

  效果: 修一个 bug，4 个 DB 同时修复
        加一个特性，4 个 DB 同时受益
        新增数据库支持只需写一个 PromptProfile
```

### 提升 8：思维内容统一处理（当前三个字段各管各的）

```
当前:
  ReasoningContent string  // Kimi 用
  Thinking         string  // Qwen/DeepSeek <think>标签提取
  stripThink bool          // 配置项，决定是否剥离
  // 三个字段三种逻辑，加新模型就加新字段

新 Engine:
  // 统一字段
  Message.Thinking string           // 所有厂商统一
  Message.ThinkingBlocks []Block    // Anthropic 结构化思维

  // ProviderAdapter.NormalizeResponse() 统一提取:
  //   Anthropic  → thinking blocks
  //   Kimi/Qwen/GLM/DeepSeek/MiMo → reasoning_content 字段
  //   MiniMax    → reasoning_details 字段
  //   Ollama     → <think> 标签提取

  // 多轮处理也统一:
  //   Anthropic → 保留 blocks + signature
  //   DeepSeek/Kimi/Qwen → 工具链内保留，新轮次剥离

  效果: 加新厂商不需要改 Engine 代码
        思维链可选展示给用户（高级调试模式）
```

---

## 四、流程图对比

```
当前 AgentLoop:
┌─────────────────────────────────────────┐
│ system+user → Chat → tools? → execute → │ ← 就这一层循环
│              ↑__________________________|    无保护、无优化
└─────────────────────────────────────────┘

新 Engine:
┌──────────────────────────────────────────────────────────┐
│                                                           │
│  ┌─ 压缩检查 ─┐                                          │
│  │ >80%折叠   │                                          │
│  │ >90%截断   │                                          │
│  │ >95%阻止   │                                          │
│  └─────┬──────┘                                          │
│        ↓                                                  │
│  ┌─ 轮次注入 ─┐     ┌─ 构建请求 ─┐                       │
│  │ 收敛引导   │ →  │ 厂商参数   │                       │
│  │ 预算提示   │     │ cache标记  │                       │
│  └────────────┘     │ thinking   │                       │
│                      └─────┬──────┘                       │
│                            ↓                              │
│                 ┌─ callWithRetry ──┐                     │
│                 │ 429 → 退避重试   │                     │
│                 │ 413 → 压缩重试   │                     │
│                 │ 529 → 降级重试   │                     │
│                 └─────────┬────────┘                     │
│                           ↓                               │
│              ┌─ 截断恢复 ──┐                              │
│              │ length→升级  │                              │
│              │ 续写→合并    │                              │
│              └──────┬──────┘                              │
│                     ↓                                     │
│           no tools → return                               │
│                     ↓                                     │
│        ┌─ 工具执行 ────────┐                              │
│        │ 只读→并发(max 5)  │                              │
│        │ 写入→串行         │                              │
│        └────────┬──────────┘                              │
│                 ↓                                         │
│        ┌─ 结果处理 ────────┐                              │
│        │ 动态预算截断      │                              │
│        │ 大结果→磁盘       │                              │
│        └────────┬──────────┘                              │
│                 ↓                                         │
│           追加到历史 → 回到顶部                            │
└──────────────────────────────────────────────────────────┘
```

---

## 五、量化对比总结

| 维度 | 当前 AgentLoop | 新 Engine | 提升 |
|------|---------------|-----------|------|
| **代码行数** | ~1750行（4份重复） | ~1300行（零重复） | -26% 代码，+6项功能 |
| **API 调用失败** | 直接终止 | 自动重试5次+指数退避 | 可用性大幅提升 |
| **上下文溢出** | 413崩溃 | 自动压缩恢复 | 从崩溃→自愈 |
| **输出截断** | 丢失 | 自动续写2次 | 不再丢失诊断结论 |
| **工具执行** | 全串行 | 只读并发(max 5) | 每轮省50-70%时间 |
| **结果截断** | 无/硬3000字 | 动态预算+智能截断 | 不撑爆也不丢信息 |
| **厂商优化** | 零 | Capability驱动全参数 | 每个模型最优运行 |
| **思维处理** | 3字段各管各 | 统一提取+多轮策略 | 加厂商零改动 |
| **新DB支持** | 复制~350行loop.go | 写~100行Profile | 工作量降低70% |

---

## 六、一句话总结

当前 AgentLoop 是一个**裸循环** — 发消息、收回复、执行工具、拼回去。

新 Engine 在这个裸循环的**每一步**都加了保护和优化，让同一个模型在同一个诊断任务上表现更好、更稳定、更高效。
