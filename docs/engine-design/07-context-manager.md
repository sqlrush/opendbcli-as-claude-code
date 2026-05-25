# 07 — ContextManager 设计

## 当前问题

OpenDB 当前完全没有上下文窗口管理：
- 不追踪 token 使用量
- 不检查是否即将超窗口
- 不做任何压缩或截断
- 长诊断（10+ 轮）可能超窗口导致 413 错误

## 设计目标

对标 Claude Code 的多层压缩体系，但简化为 OpenDB 场景需要的 3 层：

```
Claude Code (5层):        OpenDB Engine (3层):
─────────────────         ────────────────────
Snip Compact              (不需要: 无用户标记删除)
Micro Compact             (不需要: 无 cache_edits)
Context Collapse    →     Turn Collapse (轮次折叠)
Auto Compact        →     Auto Summary (自动摘要)
Reactive Compact    →     Emergency Truncate (紧急截断)
```

## 接口定义

```go
package context

type Manager struct {
    maxContextTokens int       // 上下文窗口上限（从 Capability 获取）
    safetyBuffer     int       // 安全缓冲（默认 2000 token）
    tokenCounter     TokenCounter
    compressor       *Compressor
}

type TokenCounter interface {
    // Count 估算消息列表的总 token 数
    Count(messages []engine.Message) int
    // CountText 估算纯文本的 token 数
    CountText(text string) int
}

func NewManager(
    maxTokens int,
    counter TokenCounter,
    compressor *Compressor,
) *Manager

// MaybeCompress 检查是否需要压缩，如需要则执行
// 返回新的消息列表（不可变，不修改原 messages）
func (m *Manager) MaybeCompress(messages []engine.Message) ([]engine.Message, bool)

// ForceCompress 强制压缩（413 后触发）
func (m *Manager) ForceCompress(messages []engine.Message) []engine.Message

// RemainingTokens 返回剩余可用 token
func (m *Manager) RemainingTokens(messages []engine.Message) int

// ShouldBlock 是否应阻止 API 调用（>95% 容量）
func (m *Manager) ShouldBlock(messages []engine.Message) bool

// TokenUsage 返回当前 token 使用统计
func (m *Manager) TokenUsage(messages []engine.Message) TokenUsageInfo

type TokenUsageInfo struct {
    Used       int
    Limit      int
    Remaining  int
    Percentage float64
}
```

## Token 计数

```go
// SimpleTokenCounter — 基于字符数的快速估算
// 中文约 1.5 字符/token，英文约 4 字符/token
// 这是粗估，但足够用于阈值判断
type SimpleTokenCounter struct{}

func (c *SimpleTokenCounter) Count(messages []engine.Message) int {
    total := 0
    for _, msg := range messages {
        total += c.CountText(msg.Content)
        total += c.CountText(msg.Thinking)
        // 每条消息有固定开销 (role, formatting)
        total += 4
    }
    return total
}

func (c *SimpleTokenCounter) CountText(text string) int {
    if text == "" {
        return 0
    }

    // 简单启发式：统计中文字符和非中文字符分别估算
    chinese := 0
    other := 0
    for _, r := range text {
        if r >= 0x4E00 && r <= 0x9FFF {
            chinese++
        } else {
            other++
        }
    }

    // 中文: ~1.5 字符/token, 英文: ~4 字符/token
    return chinese*2/3 + other/4 + 1
}

// UsageTokenCounter — 基于 API 返回的实际 usage 精确追踪
// 首轮用 SimpleTokenCounter 估算，后续轮次用累计的实际 usage 修正
type UsageTokenCounter struct {
    actualUsed int // 从 API response.Usage 累计
    estimated  *SimpleTokenCounter
}

func (c *UsageTokenCounter) UpdateActual(usage engine.Usage) {
    c.actualUsed = usage.InputTokens
}
```

## 压缩器

```go
type Compressor struct {
    provider engine.ProviderAdapter // 用于生成摘要（可选）
}

// ── 第1层: Turn Collapse（轮次折叠）──
// 将早期的工具调用轮次折叠为摘要
// 对标 Claude Code 的 Context Collapse

func (c *Compressor) CollapseTurns(
    messages []engine.Message,
    targetTokens int,
) []engine.Message {
    result := make([]engine.Message, 0)

    // 保留：系统提示 + 最近 3 轮 + 第一条用户消息
    // 折叠：中间的工具调用轮次

    // 找到所有"轮次"边界（assistant 消息后跟 tool 结果）
    turns := splitIntoTurns(messages)

    if len(turns) <= 4 {
        return messages // 不够折叠
    }

    // 保留第一轮（系统+用户初始消息）和最后 3 轮
    keepFirst := turns[0]
    keepLast := turns[len(turns)-3:]
    collapsible := turns[1 : len(turns)-3]

    // 为可折叠轮次生成摘要
    summary := c.summarizeTurns(collapsible)

    result = append(result, keepFirst.Messages...)
    result = append(result, engine.Message{
        Role: "user",
        Content: fmt.Sprintf(
            "<system-reminder>以下是之前 %d 轮诊断的摘要：\n%s\n请基于此继续分析。</system-reminder>",
            len(collapsible), summary,
        ),
        IsMeta: true,
    })
    for _, turn := range keepLast {
        result = append(result, turn.Messages...)
    }

    return result
}

// summarizeTurns — 从工具调用轮次中提取关键信息
func (c *Compressor) summarizeTurns(turns []Turn) string {
    var buf strings.Builder

    for _, turn := range turns {
        // 提取这一轮调用了什么工具、关键发现
        for _, msg := range turn.Messages {
            if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
                names := make([]string, len(msg.ToolCalls))
                for i, tc := range msg.ToolCalls {
                    names[i] = tc.Name
                }
                buf.WriteString(fmt.Sprintf("- 调用了 %s\n", strings.Join(names, ", ")))
            }

            if msg.Role == "assistant" && msg.Content != "" {
                // 提取 assistant 的分析要点（取前 200 字符）
                content := msg.Content
                if len(content) > 200 {
                    content = content[:200] + "..."
                }
                buf.WriteString(fmt.Sprintf("  分析: %s\n", content))
            }

            if msg.Role == "tool" {
                // 提取工具结果摘要（取前 100 字符）
                content := msg.Content
                if len(content) > 100 {
                    content = content[:100] + "..."
                }
                buf.WriteString(fmt.Sprintf("  结果: %s\n", content))
            }
        }
    }

    return buf.String()
}

// ── 第2层: Auto Summary（自动摘要）──
// 当折叠不够时，用 LLM 生成对话摘要替换旧消息
// 对标 Claude Code 的 Auto Compact

func (c *Compressor) AutoSummary(
    ctx context.Context,
    messages []engine.Message,
) ([]engine.Message, error) {
    if c.provider == nil {
        // 无 LLM 可用，降级到紧急截断
        return c.EmergencyTruncate(messages), nil
    }

    // 构建摘要请求
    summaryReq := &engine.Request{
        Messages: []engine.Message{
            {
                Role: "user",
                Content: fmt.Sprintf(
                    "请将以下诊断对话浓缩为一段摘要（不超过500字），保留所有关键发现、数据和结论：\n\n%s",
                    formatMessagesForSummary(messages),
                ),
            },
        },
        MaxTokens: 1000,
    }

    resp, err := c.provider.Chat(ctx, summaryReq)
    if err != nil {
        // 摘要失败，降级到紧急截断
        return c.EmergencyTruncate(messages), nil
    }

    // 用摘要替换旧消息，保留最新轮次
    return []engine.Message{
        messages[0], // 保留系统提示/初始上下文
        {
            Role: "user",
            Content: fmt.Sprintf(
                "<system-reminder>以下是之前诊断对话的摘要（由系统自动生成）：\n%s\n请基于此继续分析。</system-reminder>",
                resp.Content,
            ),
            IsMeta: true,
        },
        // 保留最后 2 轮
        messages[len(messages)-4], // assistant
        messages[len(messages)-3], // tool result
        messages[len(messages)-2], // assistant
        messages[len(messages)-1], // tool result or user
    }, nil
}

// ── 第3层: Emergency Truncate（紧急截断）──
// 最后手段：直接删除中间消息
// 对标 Claude Code 的 Reactive Compact

func (c *Compressor) EmergencyTruncate(messages []engine.Message) []engine.Message {
    if len(messages) <= 4 {
        return messages
    }

    // 保留第一条和最后 3 条
    result := make([]engine.Message, 0, 5)
    result = append(result, messages[0])
    result = append(result, engine.Message{
        Role:    "user",
        Content: "<system-reminder>由于上下文限制，中间的诊断历史已被截断。请基于以下最近的信息继续分析。</system-reminder>",
        IsMeta:  true,
    })
    result = append(result, messages[len(messages)-3:]...)
    return result
}
```

## Manager 主逻辑

```go
func (m *Manager) MaybeCompress(messages []engine.Message) ([]engine.Message, bool) {
    used := m.tokenCounter.Count(messages)
    threshold := m.maxContextTokens - m.safetyBuffer

    if used < int(float64(threshold)*0.8) {
        return messages, false // 80% 以下不压缩
    }

    // 80%-90%: 尝试轮次折叠
    if used < int(float64(threshold)*0.9) {
        collapsed := m.compressor.CollapseTurns(messages, threshold)
        if m.tokenCounter.Count(collapsed) < int(float64(threshold)*0.8) {
            return collapsed, true
        }
    }

    // 90%+: 紧急截断（Auto Summary 需要上下文，可能来不及）
    truncated := m.compressor.EmergencyTruncate(messages)
    return truncated, true
}

func (m *Manager) ForceCompress(messages []engine.Message) []engine.Message {
    // 413 后强制调用，依次尝试
    collapsed := m.compressor.CollapseTurns(messages, m.maxContextTokens/2)
    if m.tokenCounter.Count(collapsed) < m.maxContextTokens-m.safetyBuffer {
        return collapsed
    }

    return m.compressor.EmergencyTruncate(messages)
}

func (m *Manager) RemainingTokens(messages []engine.Message) int {
    used := m.tokenCounter.Count(messages)
    remaining := m.maxContextTokens - used - m.safetyBuffer
    if remaining < 0 {
        return 0
    }
    return remaining
}

func (m *Manager) ShouldBlock(messages []engine.Message) bool {
    used := m.tokenCounter.Count(messages)
    return float64(used) > float64(m.maxContextTokens)*0.95
}
```

## 压缩触发阈值

```
Token 使用率         动作
──────────────       ─────
< 80%               无操作
80% - 90%           Turn Collapse（轮次折叠）
90% - 95%           Emergency Truncate（紧急截断）
> 95%               阻止 API 调用，提示用户
413 返回             ForceCompress → 重试
```
