# 06-附 — 工具执行+截断详解：当前 OpenDB vs 新 Engine vs Claude Code

## 一、当前 OpenDB 工具执行：3 个问题

### 问题 1：全部串行，一个接一个

```go
// oracle/agent/loop.go:150-159 — 当前实现

for _, tc := range resp.ToolCalls {
    resultContent := a.executeTool(ctx, tc)      // 阻塞等完才下一个
    messages = append(messages, llm.Message{
        Role:       "tool",
        Content:    resultContent,
        ToolCallID: tc.ID,
    })
}
```

**实际场景：** 模型在一轮中同时请求 3 个工具

```
模型输出: "我需要查活跃会话、等待事件和 Top SQL"
  tool_calls: [activesessions, waits, topsql]

当前执行（串行）:
  t=0s    执行 activesessions → 查数据库 → 1.5s 返回
  t=1.5s  执行 waits         → 查数据库 → 1.2s 返回
  t=2.7s  执行 topsql        → 查数据库 → 2.0s 返回
  t=4.7s  全部完成
  总耗时: 4.7 秒

  这3个查询互相独立（都是只读 SELECT），完全可以并行
```

### 问题 2：Oracle 无截断，大结果撑爆上下文

```go
// oracle/agent/loop.go — formatResult 无截断
func formatResult(r *skill.Result) string {
    switch r.Type {
    case skill.ResultText:
        if r.Rendered != "" { return r.Rendered }    // ← 多大都原样返回
    case skill.ResultTable:
        return formatTable(r)                         // ← 多少行都全量格式化
    }
}

// mysql/agent/loop.go — 有截断但是硬截 3000 字符
func truncateResult(s string) string {
    if len(s) > 3000 {
        return s[:3000] + "\n...(truncated)"          // ← 一刀切，不管内容结构
    }
    return s
}
```

**实际场景：**

```
/topsql 返回 Top 50 SQL 的详细信息:
  每个 SQL: sql_id + text(可能几百字符) + 统计指标
  50 个 SQL → 格式化后可能 8000-15000 字符

Oracle loop.go: 原样放入消息 → 吃掉大量上下文空间
MySQL loop.go: 硬截 3000 字符 → SQL 列表在第 12 个就断了
               后面 38 个 SQL 的信息全部丢失
               模型只看到部分数据就要做诊断
```

### 问题 3：等模型完整输出后才开始执行

```
当前时序:

  t=0s     模型开始流式输出
  t=1s     "分析等待事件分布..."（文本）
  t=2s     tool_call: activesessions    ← 模型已经决定了
  t=2.5s   tool_call: waits             ← 模型已经决定了
  t=3s     tool_call: topsql + 输出结束  ← 但我们要等到这里
  ──────── 模型输出完毕，才开始执行 ────────
  t=3s     开始执行 activesessions
  t=4.5s   开始执行 waits
  t=5.7s   开始执行 topsql
  t=7.7s   全部完成
  总耗时: 7.7 秒

  实际上 activesessions 在 t=2s 模型就决定要调了，到 t=3s 才开始执行，白等 1 秒
```

---

## 二、新 Engine 的 3 项提升

### 提升 1：只读并发，写入串行

```
新 Engine 执行（并发）:
  t=0s    同时启动 activesessions + waits + topsql
  t=0s    ┌ activesessions → 查数据库...
  t=0s    ├ waits          → 查数据库...
  t=0s    └ topsql         → 查数据库...
  t=1.2s  waits 完成
  t=1.5s  activesessions 完成
  t=2.0s  topsql 完成
  t=2.0s  全部完成
  总耗时: 2.0 秒（最慢的那个决定总时间）

  对比:
  当前串行: 4.7 秒
  新并发:   2.0 秒
  节省:     2.7 秒（-57%）
```

**安全保证：**

```
为什么写入操作不能并发？

场景: 模型同时调 kill 456 和 kill 789
  并发: 两个 kill 同时执行，可能影响正在处理阻塞链的中间状态
  串行: kill 456 → 确认成功 → kill 789 → 确认成功
  → 串行保证操作的因果正确性

分区判断:
  SecurityLevel <= 0 (LevelReadOnly) → 并发安全
  SecurityLevel > 0 (操作/管理/危险) → 必须串行
  → 和现有 OpenDB 安全分级完全一致
```

### 提升 2：动态预算截断（替代硬截断）

**动态预算计算：**

```
场景 A: 128K 模型，已用 40K，剩余 88K，3 个工具
  总预算 = 88K × 30% = 26.4K
  每工具 = 26.4K / 3 = 8.8K
  → activesessions (2000字符) → 不截断
  → waits (1500字符)          → 不截断
  → topsql (15000字符)        → 截断到 8800 字符

场景 B: 32K 小模型，已用 22K，剩余 10K，3 个工具
  总预算 = 10K × 30% = 3K
  每工具 = 3K / 3 = 1K
  → activesessions (2000字符) → 截断到 1000 字符
  → waits (1500字符)          → 截断到 1000 字符
  → topsql (15000字符)        → 截断到 1000 字符

→ 预算随剩余空间动态调整
→ 大模型看更多数据，小模型看精简版
→ 不会因为一个大结果吃掉全部预算
```

**智能截断（头70%+尾20%，不是一刀切）：**

```
硬截断（当前 MySQL 做法）:
  s[:3000] + "...(truncated)"
  → 可能切在 SQL 文本中间
  → 后面 12000 字符全部丢失

智能截断（新 Engine）:
  头 70% + "...(%d 字符已省略)..." + 尾 20%

  topsql 返回的典型结构:
    SQL_ID        EXECUTIONS  ELAPSED_MS  PLAN
    ────────────  ──────────  ──────────  ────
    8a4kd3xmn1   1234        823         FTS    ← 头部: Top 1（最重要）
    fz9qw2bvt8   456         1204        IDX    ← Top 2
    g7mn4pqas2   89          4521        FTS    ← Top 3
    ...
    (中间 40 个 SQL 省略)
    ...
    zz1abc999    2           12          IDX    ← 尾部: 最后几个
    === 总计: 50 SQL, 累计 elapsed 12345s ===  ← 尾部可能有汇总行

  → 保留最重要的头部 + 可能有汇总的尾部
  → 比一刀切丢失的信息少得多
```

**大结果可选写磁盘：**

```
超大结果（>预算）:
  完整内容写入 /tmp/opendb-tool-results/xxx.txt
  消息中放截断版 + 路径引用:
  "...(完整内容 15000 字符，保存在 /tmp/opendb-tool-results/abc123.txt)"

  → 完整数据不丢失，模型如需要可以告知用户查看
```

### 提升 3：流式工具预执行

```
新 Engine 时序（流式预执行）:

  t=0s     模型开始流式输出
  t=1s     "分析等待事件分布..."（文本）
  t=2s     tool_call: activesessions → 立即启动执行!
  t=2.5s   tool_call: waits → 立即启动执行!
  t=3s     tool_call: topsql → 立即启动执行!
  t=3.2s   waits 完成
  t=3.5s   activesessions 完成
  t=4.0s   模型输出结束 + topsql 完成(几乎同时)
  总耗时: 4.0 秒

  对比:
  当前:    7.7 秒（模型输出 3s + 工具串行 4.7s）
  新并发:  5.1 秒（模型输出 3s + 工具并发 2.0s + 处理 0.1s）
  新+预执行: 4.0 秒（模型输出和工具执行重叠）
  节省: 3.7 秒（-48%）
```

**安全保证：** 只有 SecurityLevel<=0（只读）的工具才预执行。kill/alter 等操作类工具必须等模型完整输出后确认。

---

## 三、对标 Claude Code 源码

### 每一项都能对应到 Claude Code 的具体文件

```
                        Claude Code 源码              新 Engine 设计
                        ──────────────               ────────────────
只读并发+写入串行        toolOrchestration.ts          ToolOrchestrator.partition()
                        partitionToolCalls()          → 相同思路
                        max concurrency 10            → 我们用 5（DB查询比文件操作重）

流式预执行              StreamingToolExecutor.ts       ExecuteStreaming()
                        addTool() 一解析完就提交       → toolCallCh 流式接收
                        getCompletedResults()          → resultCh 流式返回
                        siblingAbortController         → 我们简化掉（DB查询不会级联失败）

动态结果截断            toolResultStorage.ts           ResultHandler.Process()
                        applyToolResultBudget()        → 相同思路：按预算控制
                        maxResultSizeChars per tool    → 我们按剩余上下文动态算
                        50KB→写磁盘+内联2KB            → 我们4K预算+可选写磁盘

表格格式化              （CC不需要，工具返回JSON）       formatTable()
                                                      → OpenDB 独有（DB查询返回表格）
```

### 具体对应关系

| 新 Engine 设计 | Claude Code 源文件 | 我们改了什么 |
|---------------|-------------------|-------------|
| 只读/写入分区 | `toolOrchestration.ts:partitionToolCalls()` | 判断条件从 `isReadOnly()` 改为 `SecurityLevel<=0` |
| 信号量控制并发 | `toolOrchestration.ts` max concurrency | 从 10 降到 5（DB 连接池比文件系统更受限） |
| 流式预执行 | `StreamingToolExecutor.ts` | 去掉 siblingAbortController（DB 查询无级联） |
| 结果预算控制 | `toolResultStorage.ts:applyToolResultBudget()` | 从固定 maxResultSizeChars 改为动态计算 |
| 大结果写磁盘 | `toolResultStorage.ts` 50KB→磁盘 | 阈值更小（4K），DB 结果小于代码文件 |
| 智能截断(头尾) | CC 只保留头部 2KB | 我们保留头70%+尾20%，DB 表格尾部可能有汇总 |
| 表格格式化 | CC 不需要 | OpenDB 独有，保留现有 formatTable 加列宽和行数限制 |
| 结果按原始顺序 | StreamingToolExecutor 按接收顺序 | 相同：并发执行但结果排序保持确定性 |

---

## 四、整个项目的 Claude Code 对标关系

不只是工具执行，**所有 12 项优化都来自 Claude Code 的具体实现**：

| 优化项 | Claude Code 源码 | 新 Engine 对标 |
|--------|-----------------|---------------|
| 系统提示重构 | `prompts.ts:getSystemPrompt()` ~30KB | 04-ContextBuilder ~4.7KB |
| 工具使用策略 | `prompts.ts` "用Read不用cat" 级别指导 | 10-SystemPrompts 工具入口+路径映射 |
| 结果截断 | `toolResultStorage.ts` 50KB→磁盘 | 06-ResultHandler 动态预算 |
| Adaptive thinking | `claude.ts` `thinking:{type:'adaptive'}` | 03-ProviderAdapter Capability |
| HTTP 重试 | `withRetry.ts` 500ms×2^n, max 10 | 05-RetryPolicy 500ms×2^n, max 5 |
| 上下文压缩 | `autoCompact.ts` + `contextCollapse` | 07-ContextManager 3层压缩 |
| max_tokens 恢复 | `withRetry.ts` parseMaxTokensContextOverflow | 08-Engine recoverTruncatedOutput |
| 工具并发 | `toolOrchestration.ts` partition | 06-ToolOrchestrator partition |
| Prompt Cache | `claude.ts` cache_control ephemeral | 03-ProviderAdapter CachingExplicit |
| 流式预执行 | `StreamingToolExecutor.ts` | 06-ToolOrchestrator ExecuteStreaming |
| 动态工具描述 | `tool.prompt()` 每次 API 调用时计算 | 04-ContextBuilder Layer 4 |
| task_budget | `claude.ts` output_config.task_budget | 03-ProviderAdapter OutputCapability |

**Claude Code 是教科书，新 Engine 是根据 DBA 数据库诊断场景写的读书笔记 — 取其精华，适配自己的场景。**

---

## 五、完整对比表

| 维度 | 当前 OpenDB | 新 Engine | Claude Code |
|------|------------|-----------|-------------|
| **执行模型** | 全部串行 | 只读并发(max 5) + 写入串行 | 只读并发(max 10) + 写入串行 |
| **预执行** | ❌ 等完整输出 | ✅ 流式预执行只读工具 | ✅ StreamingToolExecutor |
| **截断-Oracle** | ❌ 无截断 | ✅ 动态预算 | ✅ 50KB→磁盘 |
| **截断-MySQL/PG** | 硬截 3000 字符 | ✅ 动态预算+智能截断 | ✅ 预算控制 |
| **截断方式** | 一刀切前3000 | 头70%+尾20%+中间省略 | 前2KB+磁盘持久化 |
| **预算计算** | 固定 3000 | 剩余上下文×30%÷工具数 | maxResultSizeChars per tool |
| **大结果处理** | 无 | 可选写磁盘+路径引用 | 写磁盘+内联2KB |
| **分区依据** | — | SecurityLevel(0=只读) | isReadOnly()+isConcurrencySafe() |
| **3工具延迟** | ~4.7s (串行) | ~2.0s (并发) | ~2.0s (并发+预执行) |
| **带预执行** | ~7.7s | ~4.0s | ~3.5s |

---

## 六、组合效果：一轮诊断的时间对比

```
场景: 模型请求 3 个只读工具（activesessions + waits + topsql）

当前 OpenDB:
  模型输出: 3.0s (等完才开始)
  工具执行: 4.7s (串行)
  结果处理: 0s   (无截断)
  总计: 7.7s

新 Engine (并发，无预执行):
  模型输出: 3.0s (等完才开始)
  工具执行: 2.0s (并发)
  结果处理: 0.1s (动态截断)
  总计: 5.1s  (-34%)

新 Engine (并发+预执行):
  模型输出: 3.0s ──┐
  工具执行: 2.0s ──┘ (重叠执行)
  结果处理: 0.1s
  总计: 4.0s  (-48%)

10 轮诊断累计:
  当前: 7.7s × 10 = 77s (仅工具执行部分)
  新:   4.0s × 10 = 40s
  节省: 37 秒
```

---

## 七、与 ContextManager 的协同

ResultHandler 不是单独工作的，它和 ContextManager（上下文压缩）配合形成双重保护：

```
每轮的完整保护链:

  1. ResultHandler 先控制单个工具结果大小
     → remaining = ContextManager.RemainingTokens()
     → perToolBudget = remaining × 30% / numTools
     → 每个工具结果不超过预算（动态截断）

  2. 工具结果追加到 messages 后
     → ContextManager.MaybeCompress() 检查总量
     → 如果超过 80% → Turn Collapse
     → 如果超过 90% → Emergency Truncate

  → 双重保护：先控制增量大小，再控制累积总量
  → 比 Claude Code 的单一 toolResultBudget 更精细
```
