# 04 — ContextBuilder 设计

## 职责

ContextBuilder 负责"给模型提供足够多的、正确的信息"。这是对标 Claude Code 最关键的模块。

Claude Code 有 8 层上下文注入，OpenDB 当前只有 2 层。ContextBuilder 将扩展到 5 层（去掉 OpenDB 不需要的 IDE/图片/@附件等）。

## 六层上下文体系

```
┌─ Layer 1: 系统提示（每次发送）──────────────────────────────┐
│  身份定义 + 行为规范 + 工具使用策略 + 输出格式 + 安全约束    │
│  + DB特定规则（通过 PromptProfile 注入）                     │
│  → 对标 Claude Code 的 constants/prompts.ts                  │
└──────────────────────────────────────────────────────────────┘
┌─ Layer 1.5: 用户自定义规则（类似 CLAUDE.md）───────────────┐
│  ~/.opendb/rules/*.md (全局) + ~/.opendb/rules/{实例}/*.md  │
│  DBA 自定义的诊断规范、公司特定规则                          │
│  → 对标 Claude Code 的 CLAUDE.md 四级加载体系                │
└──────────────────────────────────────────────────────────────┘
┌─ Layer 2: 环境上下文（隐藏消息，IsMeta=true）──────────────┐
│  数据库连接信息 + 版本 + 实例名 + 当前时间                   │
│  → 对标 Claude Code 的 prependUserContext() + gitStatus      │
└──────────────────────────────────────────────────────────────┘
┌─ Layer 3: 诊断上下文（附加到用户消息）─────────────────────┐
│  Sentinel 压缩报告 + 实时指标快照                            │
│  → 对标 Claude Code 的 自动附件收集                          │
└──────────────────────────────────────────────────────────────┘
┌─ Layer 4: 工具描述（动态生成）─────────────────────────────┐
│  每个 skill 的名称+描述+参数schema，根据模式过滤             │
│  → 对标 Claude Code 的 tool.prompt() 动态生成                │
└──────────────────────────────────────────────────────────────┘
┌─ Layer 5: 轮次上下文（多轮过程中注入）─────────────────────┐
│  收敛引导 + token预算提示 + 工具结果格式化                   │
│  → 对标 Claude Code 的 system-reminder 标签                  │
└──────────────────────────────────────────────────────────────┘
```

## 接口定义

```go
package context

type Builder struct {
    profile    profile.PromptProfile    // DB特定配置
    capability *provider.ProviderCapability
}

func NewBuilder(p profile.PromptProfile, cap *provider.ProviderCapability) *Builder

// Build 构建完整的消息列表（首轮）
func (b *Builder) Build(input engine.EngineInput) BuildResult

// InjectTurnContext 在每轮开始前注入轮次上下文
func (b *Builder) InjectTurnContext(messages []engine.Message, turn, maxTurns int) []engine.Message

type BuildResult struct {
    SystemPrompt []engine.SystemPromptBlock  // 系统提示（独立传给 API）
    Messages     []engine.Message            // 消息列表
    Tools        []engine.ToolSchema         // 工具描述
}
```

## Layer 1: 系统提示（重构核心）

### 当前 vs 目标

```
当前 Oracle prompt (~500字):
  "你是 OpenDB 数据库诊断专家..."
  + 可用 skill 列表
  + 11 条规则
  + 模式说明

目标 (~5000字，结构化):
  ├─ 身份与角色
  ├─ 核心行为规范（15条）
  ├─ 工具使用策略（每个工具什么场景用、怎么组合）
  ├─ 推理策略（链式推理指南）
  ├─ 输出格式规范
  ├─ 安全约束
  ├─ 环境信息
  └─ DB特定规则（PromptProfile 注入）
```

### 系统提示模板

```go
func (b *Builder) buildSystemPrompt(input engine.EngineInput) []engine.SystemPromptBlock {
    blocks := []engine.SystemPromptBlock{}

    // ── Block 1: 身份与核心规范（可缓存）──
    blocks = append(blocks, engine.SystemPromptBlock{
        Text: b.identityAndRules(),
        CacheControl: &engine.CacheControl{Type: "ephemeral"}, // Anthropic cache
    })

    // ── Block 2: DB特定规则（可缓存）──
    blocks = append(blocks, engine.SystemPromptBlock{
        Text: b.profile.SystemPromptRules(),
        CacheControl: &engine.CacheControl{Type: "ephemeral"},
    })

    // ── Block 3: 环境信息（动态，不缓存）──
    blocks = append(blocks, engine.SystemPromptBlock{
        Text: b.environmentInfo(input),
    })

    return blocks
}
```

### 系统提示内容设计

```go
func (b *Builder) identityAndRules() string {
    return `# 身份
你是 OpenDB 数据库诊断专家。你通过调用 OpenDB 提供的工具（skill）来采集数据库信息，
基于证据进行链式推理，给出精准的诊断结论和可执行的修复方案。

# 系统规则
- 所有非工具调用的输出都会展示给用户（专业 DBA）
- 工具调用的结果包含真实的数据库指标数据
- 始终使用中文回复

# 核心行为规范
1. 先观察再诊断 — 至少收集 2-3 个维度的数据再给结论
2. 用证据说话 — 每个结论必须引用具体的工具查询结果（数值、SQL ID、等待事件名等）
3. 禁止编造数据 — 如果工具没返回某个信息，不要假设它的值
4. 不要重复查询 — 如果前面的轮次已经获取了某个数据，直接引用，不要重复调用同一工具
5. 高效使用轮次 — 每轮查询 1-2 个最关键的信息，不要一次查太多也不要查无关的
6. 主动收敛 — 当你有足够信息给出结论时，立即给出，不要继续"探索"
7. 先诊断再换策略 — 如果第一轮查询结果不符合预期，先分析原因，再决定查什么
8. 区分紧急措施和根因修复 — 止血方案要能立即执行，根因方案要彻底解决
9. SQL必须可执行 — 修复建议中的 SQL 必须完整、语法正确、可直接粘贴执行
10. 引用对象先验证 — 引用表名/索引名/序列名前，必须用 sql skill 查询确认其存在
11. ISEQ$$_ 序列 — 这是 identity column 的自动序列，需 ALTER TABLE ... MODIFY ... GENERATED AS IDENTITY

# 工具使用策略
## 诊断入口
- 性能问题 → 先 activesessions + waits，看活跃会话和等待事件分布
- SQL问题 → 先 topsql 或 slowsql，找到问题 SQL 后 explain 看执行计划
- 锁问题 → 先 locks，如果有阻塞 blocktree 看完整阻塞链
- 空间问题 → 先 space，再 segments 看具体对象

## 深度分析组合
- 全表扫描 → explain → tableinfo（看索引）→ 给出建索引建议
- 等待事件高 → waits → ash（采样分析）→ 找到 SQL → explain
- 历史对比 → awr（看趋势变化）→ planhistory（看执行计划是否变了）
- I/O 高 → os（看磁盘IO）→ topsql（找大IO的SQL）→ explain

## 注意事项
- sql skill 是最后手段 — 优先用专用 skill，只有专用 skill 覆盖不到时才用 sql 直查
- kill 要谨慎 — 只在明确阻塞且用户允许时才 kill
- alter 要确认 — 参数修改会影响全局，确认用户知晓影响

# 输出格式
最终诊断必须包含以下三部分：

## 根因分析
[基于证据的明确结论。引用具体数据，如"db file sequential read 占等待事件 65%（来自 waits 查询）"]

## 紧急措施
[可立即执行的止血方案。给出完整的原生 SQL，放在代码块中]

## 根因修复
[长期修复方案。给出完整的原生 SQL 和参数调整建议]

# 安全约束
- Level 0（只读）: 所有查询类 skill 无需确认
- Level 1（操作）: kill 需二次确认
- Level 2（管理）: alter, resize 需二次确认
- Level 3（危险）: DROP 等操作需强制确认（不可关闭）`
}
```

## Layer 2: 环境上下文

```go
func (b *Builder) buildEnvironmentContext(input engine.EngineInput) engine.Message {
    info := input.DatabaseInfo
    content := fmt.Sprintf(`<system-reminder>
# 数据库环境
产品: %s %s
实例: %s
地址: %s
当前时间: %s

# 诊断模式
模式: %s
最大轮次: %d

IMPORTANT: 此上下文仅供参考，基于你的专业判断决定是否相关。
</system-reminder>`,
        info.Product, info.Version,
        info.Instance,
        info.Host,
        time.Now().Format("2006-01-02 15:04:05"),
        input.Mode,
        input.MaxTurns,
    )

    return engine.Message{
        Role:    "user",
        Content: content,
        IsMeta:  true, // 用户不可见
    }
}
```

## Layer 3: 诊断上下文

```go
func (b *Builder) buildDiagnoseContext(input engine.EngineInput) engine.Message {
    if input.CompressedReport == "" {
        return engine.Message{
            Role:    "user",
            Content: input.UserMessage,
        }
    }

    // 用户消息 + 压缩报告组合
    content := fmt.Sprintf(`%s

以下是当前数据库异常报告：
%s

请基于以上数据和你的工具进行诊断。`, input.UserMessage, input.CompressedReport)

    return engine.Message{
        Role:    "user",
        Content: content,
    }
}
```

## Layer 4: 动态工具描述

```go
func (b *Builder) buildTools(input engine.EngineInput) []engine.ToolSchema {
    registry := b.profile.ToolRegistry()
    filter := b.profile.ToolFilter(input.Mode)

    var tools []engine.ToolSchema
    for _, skill := range registry.All() {
        if !filter(skill) {
            continue
        }

        tools = append(tools, engine.ToolSchema{
            Name:        skill.Name(),
            Description: b.buildToolDescription(skill), // 动态描述
            InputSchema: skill.ParamsSchema(),           // JSON Schema
        })
    }
    return tools
}

// buildToolDescription — 动态生成工具描述
// 对标 Claude Code 的 tool.prompt()，不写死在系统提示里
func (b *Builder) buildToolDescription(s skill.Skill) string {
    desc := s.Description()

    // 添加使用场景提示
    if hint := b.profile.ToolUsageHint(s.Name()); hint != "" {
        desc += "\n使用场景: " + hint
    }

    // 添加输出格式提示
    if format := s.OutputFormat(); format != "" {
        desc += "\n输出格式: " + format
    }

    return desc
}
```

## Layer 5: 轮次上下文

```go
// InjectTurnContext — 每轮开始前注入引导信息
func (b *Builder) InjectTurnContext(
    messages []engine.Message, turn, maxTurns int,
) []engine.Message {
    result := make([]engine.Message, len(messages))
    copy(result, messages)

    // 收敛引导（倒数第2轮开始）
    if maxTurns > 0 && turn >= maxTurns-2 {
        hint := fmt.Sprintf(
            `<system-reminder>你已使用 %d/%d 轮。请在本轮直接给出最终诊断总结，不要再调用任何工具。如果信息不足，基于已有数据给出最佳判断。</system-reminder>`,
            turn+1, maxTurns,
        )
        result = append(result, engine.Message{
            Role:    "user",
            Content: hint,
            IsMeta:  true,
        })
    }

    // Token 预算提示（当上下文使用超过 70%）
    if b.capability.Output.SupportsTaskBudget {
        // Engine 在外层计算并传入
    }

    return result
}
```

## 思维内容多轮处理

```go
// PrepareMessagesForNextTurn — 根据厂商策略处理思维内容
func (b *Builder) PrepareMessagesForNextTurn(
    messages []engine.Message, isNewUserTurn bool,
) []engine.Message {
    policy := b.capability.Thinking.MultiTurnPolicy
    result := make([]engine.Message, 0, len(messages))

    for _, msg := range messages {
        newMsg := msg // 不可变：创建副本

        switch policy {
        case provider.ThinkingPreserveAll:
            // Anthropic/OpenAI: 保留所有思维内容
            // 不做任何修改

        case provider.ThinkingStripBetweenTurns:
            // DeepSeek/Kimi/Qwen/GLM/MiMo:
            // 如果是新用户轮次，剥离之前的思维内容
            // 工具链内保留
            if isNewUserTurn && msg.Role == "assistant" {
                newMsg.Thinking = ""
                newMsg.ThinkingBlocks = nil
            }

        case provider.ThinkingStripAll:
            // 本地模型: 不保留思维
            newMsg.Thinking = ""
            newMsg.ThinkingBlocks = nil
        }

        result = append(result, newMsg)
    }

    return result
}
```
