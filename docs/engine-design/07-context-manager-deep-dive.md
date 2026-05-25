# 07-附 — 上下文压缩详解：当前 OpenDB vs 新 Engine vs Claude Code

## 一、当前 OpenDB：完全没有上下文管理

### 现状：messages 只增不减

Oracle 的 `loop.go` 里搜 `token`、`compress`、`truncat`、`context window` — **零结果**。代码中完全没有这些概念。

```
当前 loop.go 的消息生命周期:

  turn 0: messages = [system, user]                          → 2 条
  turn 1: messages = [system, user, assistant, tool]         → 4 条
  turn 2: messages = [system, user, asst, tool, hint, asst, tool]  → 7 条
  turn 3: ... → 10 条
  ...
  turn 9: ... → 30+ 条

  每条工具结果可能几百到几万字符
  消息列表只增不减，没有任何控制
```

唯一存在的"截断"在 MySQL/PG 的 loop.go：

```go
// mysql/agent/loop.go:313 — 这不是上下文管理，是粗暴硬截断
func truncateResult(s string) string {
    if len(s) > 3000 {
        return s[:3000] + "\n...(truncated)"
    }
    return s
}
```

Oracle 的 loop.go 连这个都没有 — 工具结果原样放入消息，不限大小。

### 实际会发生什么

```
场景 1: Qwen3.5-9B (32K 上下文) 跑 auto 模式（最多 20 轮）

turn 0:  系统提示 ~2000 token + 用户消息 ~1000 token = ~3K
turn 1:  +assistant ~500 + tool result ~2000 = ~5.5K
turn 2:  +assistant ~500 + tool result ~3000 = ~9K
turn 3:  +hint + assistant ~500 + tool result ~2000 = ~12K
turn 4:  ...  = ~15K
turn 5:  ...  = ~19K
turn 6:  ...  = ~23K
turn 7:  ...  = ~27K        ← 接近 32K 上限
turn 8:  ...  = ~31K        ← 危险！
turn 9:  → 413 Payload Too Large → 直接崩溃，用户看到错误信息

结论: 9B 模型到第 8-9 轮就挂了，20 轮的 auto 模式根本跑不完
```

```
场景 2: DeepSeek V3 (128K 上下文) 跑 auto 模式

如果某轮工具返回大量数据（如 topsql 返回 50 个 SQL 的详情）:
  单个工具结果可能 10K-20K token
  Oracle 的 loop.go 没有截断，原样放入消息
  几轮下来就可能吃掉 50-80K
  → 到第 10 轮左右也可能超窗口

结论: 大模型好一些但问题一样，只是晚几轮崩
```

---

## 二、Claude Code 的 5 层压缩体系

```
Claude Code 完整压缩链 (query.ts):

① Snip Compact (feature-gated)
   → 模型通过 SnipTool 主动标记"这些旧消息可以删了"
   → 按标记删除消息段
   → 给 user message 追加 [id:xxx] 标签供模型引用

② Micro Compact (feature-gated)
   → 利用 API cache_edits 删除 KV Cache 中的旧段
   → 比 autocompact 轻量——不 fork 总结 agent

③ Context Collapse
   → 多轮工具调用折叠为摘要
   → 摘要存在 collapse store 中（不在消息数组里）
   → 比 autocompact 便宜且可逆
   → 折叠后低于阈值 → autocompact 变 no-op

④ Auto Compact（最核心）
   → tokenCount > effectiveContextWindow - 13K 时触发
   → fork 压缩 agent 生成历史摘要
   → 用摘要替换旧消息
   → 模型完全不知道这件事发生了
   → 压缩失败有断路器 (consecutiveFailures)

⑤ Reactive Compact（最后手段）
   → 413 PTL 错误后触发
   → 先尝试 contextCollapse.recoverFromOverflow()（便宜）
   → 再尝试 reactiveCompact.tryReactiveCompact()（贵但彻底）
   → 压缩后重试 API 调用

触发顺序: ①→②→③→④ 每轮检查，⑤ 仅 413 后触发
```

---

## 三、新 Engine 的 3 层压缩体系

对标 Claude Code 5 层，去掉 OpenDB 不需要的 2 层：

```
Claude Code (5层):                  新 Engine (3层):
─────────────────                   ────────────────
① Snip Compact                      (不需要: OpenDB 模型不会主动标记删除)
② Micro Compact                     (不需要: 仅 Anthropic 支持 cache_edits)
③ Context Collapse              →   第1层: Turn Collapse (轮次折叠)
④ Auto Compact                  →   第2层: Auto Summary (自动摘要)
⑤ Reactive Compact              →   第3层: Emergency Truncate (紧急截断)
```

### 第 1 层：Turn Collapse（轮次折叠）

**成本：零（纯字符串操作，不调 API）**
**信息保留度：高（结构化摘要保留关键发现）**
**触发条件：token 使用率 80-90%**

```
折叠前 (10轮，30条消息，~25K token):

  [system]                                         ← 保留
  [user: 诊断请求 + 异常报告]                         ← 保留
  [assistant: 我先查活跃会话和等待事件]                 ← 折叠 ┐
  [tool: activesessions 结果 3000字]                  ← 折叠 │
  [tool: waits 结果 2000字]                           ← 折叠 │
  [assistant: IO等待高，查Top SQL]                     ← 折叠 │ 这些轮次
  [tool: topsql 结果 5000字]                          ← 折叠 │ 压缩为摘要
  [assistant: 找到SQL_ID xxx，查执行计划]               ← 折叠 │
  [tool: explain 结果 2000字]                         ← 折叠 ┘
  [assistant: 全表扫描，查表信息]                       ← 保留（最近3轮）
  [tool: tableinfo 结果 1500字]                       ← 保留
  [assistant: 缺少索引，深入查看]                       ← 保留
  [tool: sql 结果 1000字]                             ← 保留

折叠后 (6条消息，~12K token):

  [system]                                         ← 原样
  [user: 诊断请求 + 异常报告]                         ← 原样
  [user(IsMeta): <system-reminder>                  ← 新插入的摘要
    以下是之前 4 轮诊断的摘要：
    - 调用了 activesessions, waits
      分析: IO等待事件 db file sequential read 占 52%，活跃会话 15 个...
      结果: 等待事件排名前3...
    - 调用了 topsql
      分析: 找到 Top SQL sql_id=8a4kd3xmn1，elapsed 823ms...
      结果: Top 5 SQL 列表...
    - 调用了 explain
      分析: SQL 8a4kd3xmn1 存在全表扫描(TABLE ACCESS FULL)...
      结果: 执行计划 cost=4523...
    请基于此继续分析。
  </system-reminder>]
  [assistant: 全表扫描，查表信息]                       ← 保留
  [tool: tableinfo 结果 1500字]                       ← 保留
  [assistant: 缺少索引，深入查看]                       ← 保留
  [tool: sql 结果 1000字]                             ← 保留

效果: 30条消息 ~25K token → 6条消息 ~12K token（压缩 52%）
```

**摘要生成逻辑（不调 LLM，纯提取）：**

```go
func summarizeTurns(turns []Turn) string {
    for _, turn := range turns {
        // 每轮提取 3 个信息:
        // 1. 调了什么工具
        "- 调用了 activesessions, waits"
        // 2. 模型分析了什么（取前 200 字符）
        "  分析: IO等待事件 db file sequential read 占 52%..."
        // 3. 工具结果要点（取前 100 字符）
        "  结果: 等待事件排名前3..."
    }
}
```

**对标 Claude Code Context Collapse：**
Claude Code 的 Collapse 也是把多轮工具调用折叠为摘要，不调 API。区别是 Claude Code 的摘要存在 collapse store 中（可逆），我们的直接替换消息（不可逆但更简单）。

---

### 第 2 层：Auto Summary（自动摘要）

**成本：一次额外 LLM API 调用（~1000 token 输出）**
**信息保留度：最高（LLM 理解语义后生成摘要）**
**触发条件：Turn Collapse 后仍然 >90%，或需要更深度的压缩**

```
什么时候 Turn Collapse 不够？

  1. 保留的最后 3 轮包含大量工具结果（每个 5000+ 字符）
  2. 系统提示本身就很长（~4700 字）
  3. 初始诊断报告很大（CompressReport ~2000 token）
  → 折叠中间轮次后，首+尾仍然超过阈值
```

```
Auto Summary 过程:

  1. 把当前所有消息格式化为文本
  2. 调用 LLM:
     "请将以下诊断对话浓缩为一段摘要（不超过500字），
      保留所有关键发现、数据和结论"
  3. 用摘要替换所有旧消息

Auto Summary 后:
  [system]                                     ← 保留
  [user: 初始诊断请求]                            ← 保留
  [user(IsMeta): <system-reminder>              ← LLM 生成的摘要
    之前诊断的摘要（系统自动生成）：
    经过 7 轮诊断，已确认以下发现：
    1. 主要瓶颈为 I/O 等待，db file sequential read 占 52%
    2. Top SQL sql_id=8a4kd3xmn1 存在全表扫描，cost=4523
    3. ORDERS 表(SCHEMA: APP_USER)缺少 status 列索引
    4. 当前活跃会话 15 个，无阻塞
    5. PGA 使用率 45%，SGA buffer cache hit 98.5%，内存正常
    待确认：索引创建后是否需要收集统计信息
  </system-reminder>]
  [assistant: 最近一轮分析]                       ← 保留最后 2 轮
  [tool: 最近结果]
  [assistant: ...]
  [tool: ...]
```

**对标 Claude Code Auto Compact：**

```typescript
// Claude Code:
// fork 压缩 agent 异步生成摘要
compactConversation(messages)  // 独立 agent，不阻塞主线程

// 新 Engine:
// 同步调一次 LLM 生成摘要
resp, err := provider.Chat(ctx, summaryRequest)  // 同步，因为 OpenDB 诊断本身是同步的
```

**降级策略：** 如果 LLM 不可用（比如 Ollama 本地模型挂了），Auto Summary 自动降级到 Emergency Truncate。

---

### 第 3 层：Emergency Truncate（紧急截断）

**成本：零**
**信息保留度：最低（中间历史全部丢失）**
**触发条件：**
- API 返回 413 (Payload Too Large)
- Auto Summary 失败
- token 使用率 >95% 且其他压缩都不够

```
紧急截断后:

  [system]                                     ← 保留
  [user(IsMeta): <system-reminder>             ← 截断提示
    由于上下文限制，中间的诊断历史已被截断。
    请基于以下最近的信息继续分析。
  </system-reminder>]
  [assistant: 最新分析]                         ← 保留最后 3 条
  [tool: 最新结果]
  [assistant: ...]
```

**这是最后手段。** 模型知道历史被截断了（有明确提示），可以基于最新信息继续。

**对标 Claude Code Reactive Compact：**

```
Claude Code 的 413 恢复路径:
  第1次尝试: contextCollapse.recoverFromOverflow() — 排出暂存折叠（便宜）
  第2次尝试: reactiveCompact.tryReactiveCompact() — 全量压缩（贵但彻底）
  压缩后重试 API 调用

新 Engine 的 413 恢复路径:
  第1次尝试: Turn Collapse — 折叠中间轮次
  第2次尝试: Emergency Truncate — 保留首尾删中间
  压缩后重试 API 调用
```

---

## 四、Token 追踪：从"完全不知道"到"精确掌控"

### 当前 OpenDB

```go
// llm.Usage 结构体存在，但完全被忽略
type Usage struct {
    InputTokens  int
    OutputTokens int
}

// loop.go 里:
resp, err := a.provider.Chat(ctx, req)
// resp.Usage 有值但没有任何代码读取它
// 不知道用了多少 token，不知道还剩多少
```

### 新 Engine：双计数器

```
两种 Token 计数器配合使用:

SimpleTokenCounter（首轮，估算）:
  → 基于字符数启发式
  → 中文 ~1.5 字符/token，英文 ~4 字符/token
  → 快速、零成本，精度 ±20%
  → 足够判断"是否接近阈值"

UsageTokenCounter（后续轮次，精确）:
  → 首轮用估算值
  → API 响应返回 Usage.InputTokens → 用实际值修正
  → 后续轮次精度 ±5%
  → 越往后越准

追踪流程:
  turn 0: estimated=3200 → API返回actual=3150 → 修正
  turn 1: estimated+=2500=5650 → API返回actual=5820 → 修正
  ...
  turn 7: estimated=26000 → 26000/32000=81% → 触发 Turn Collapse!
```

---

## 五、压缩触发阈值

```
Token 使用率         动作                           成本
──────────────       ─────                          ────
< 80%               无操作                          0
80% - 90%           Turn Collapse（轮次折叠）         0（纯字符串）
90% - 95%           Emergency Truncate（紧急截断）    0（直接删消息）
> 95%               阻止 API 调用，提示用户           0
413 返回             ForceCompress → Turn Collapse    0
                    → 仍不够 → Emergency Truncate    0
                    → 压缩后自动重试 API 调用
```

**注意：** Auto Summary（第2层）不在自动触发链中，因为它需要调 LLM，有成本和延迟。它在 ForceCompress 路径中作为 Turn Collapse 和 Emergency Truncate 之间的可选步骤，或者由 Engine 配置决定是否启用。

---

## 六、与 ResultHandler 的协同

ContextManager 不是单独工作的，它和 ResultHandler（工具结果处理）配合：

```
每轮的完整保护链:

  1. ResultHandler 先控制单个工具结果大小
     → remaining = ContextManager.RemainingTokens()
     → perToolBudget = remaining * 30% / numTools
     → 每个工具结果不超过预算（动态截断）

  2. 工具结果追加到 messages 后
     → ContextManager.MaybeCompress() 检查总量
     → 如果超过 80% → Turn Collapse
     → 如果超过 90% → Emergency Truncate

  → 双重保护：先控制输入大小，再控制累积总量
  → 比 Claude Code 的单一 toolResultBudget 更精细
```

---

## 七、完整对比表

| 维度 | 当前 OpenDB | 新 Engine | Claude Code |
|------|------------|-----------|-------------|
| **Token 追踪** | ❌ 不追踪 | ✅ 估算+实际值修正 | ✅ 精确追踪 |
| **使用率监控** | ❌ 不知道 | ✅ 实时百分比 | ✅ 精确百分比 |
| **阈值检查** | ❌ 无 | ✅ 80%/90%/95% 三级 | ✅ contextWindow - 13K |
| **工具结果截断** | Oracle 无 / MySQL 3000字硬截 | ✅ 动态预算+智能截断 | ✅ 50KB→磁盘+2KB内联 |
| **轮次折叠** | ❌ 无 | ✅ Turn Collapse（零成本） | ✅ Context Collapse |
| **LLM 摘要** | ❌ 无 | ✅ Auto Summary（一次 API） | ✅ fork agent 压缩 |
| **紧急截断** | ❌ 无（413直接崩） | ✅ Emergency Truncate | ✅ Reactive Compact |
| **413 恢复** | ❌ 崩溃 | ✅ ForceCompress→重试 | ✅ Collapse→Compact→重试 |
| **95% 阻断** | ❌ 无 | ✅ 阻止API调用 | ✅ blocking_limit |
| **压缩层数** | 0 | 3 | 5 |
| **模型感知** | — | 不知道（摘要伪装为历史） | 不知道 |

---

## 八、小模型场景：改善最大

```
当前 Qwen3.5-9B (32K) 的诊断能力:
  playbook: 1 轮  → 正常
  assist:   3 轮  → 正常
  auto:     8-9 轮 → 崩溃 (413)
  → 实际最多跑 8 轮，远达不到 20 轮的设计目标

有 ContextManager 后:
  playbook: 1 轮  → 正常
  assist:   10 轮 → 正常（Turn Collapse 在第 6 轮触发）
  auto:     20 轮 → 正常（Turn Collapse + Emergency Truncate 配合）
  → 32K 小模型也能完成完整的深度诊断

具体过程:
  turn 0-5:  正常积累，~19K (59%)
  turn 6:    ~23K (72%) — 接近但未触发
  turn 7:    ~27K (84%) — 触发 Turn Collapse!
             折叠 turn 1-4 为摘要
             → 压缩到 ~15K (47%)
  turn 8-11: 继续积累，~23K (72%)
  turn 12:   ~27K (84%) — 再次触发 Turn Collapse
             折叠 turn 5-9 为摘要
             → 压缩到 ~14K (44%)
  turn 13-17: 继续积累
  turn 18:   触发 Turn Collapse
  turn 19-20: 完成最终诊断
  → 20 轮全部跑完，从未崩溃
```

---

## 九、一句话总结

当前 OpenDB 的上下文管理 = **空白**。消息只增不减，小模型跑不到 10 轮就崩。

新 Engine 的 3 层压缩 = **自动驾驶**。token 追踪 + 阈值触发 + 分级压缩，32K 小模型也能跑完 20 轮深度诊断，模型完全不知道压缩发生了。
