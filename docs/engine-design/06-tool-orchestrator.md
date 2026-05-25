# 06 — ToolOrchestrator + ResultHandler 设计

## 当前问题

1. 所有工具串行执行（多个只读查询可以并发）
2. 等模型完整输出后才开始执行工具（可以流式预执行）
3. MySQL/PG 硬截断 3000 字符，Oracle 无截断（应动态预算控制）
4. 工具描述写死在 system prompt 里（应动态生成）

## ToolOrchestrator

### 接口定义

```go
package tool

type Orchestrator struct {
    executor       SkillExecutor    // 现有 skill.Executor 接口
    maxConcurrency int              // 只读工具最大并发数，默认 5
}

// SkillExecutor — 桥接现有 skill 系统
type SkillExecutor interface {
    Execute(ctx context.Context, name string, params map[string]any) (*SkillResult, error)
    SecurityLevel(name string) int
}

type SkillResult struct {
    Data     any
    Text     string
    Rendered string
    Summary  string
    Error    string
}

func NewOrchestrator(executor SkillExecutor, maxConcurrency int) *Orchestrator

// Execute 执行一组工具调用
// 只读工具并发执行，写入工具串行执行
func (o *Orchestrator) Execute(
    ctx context.Context,
    toolCalls []engine.ToolCall,
) []ToolResult

// ExecuteStreaming 在模型流式输出过程中预执行工具
func (o *Orchestrator) ExecuteStreaming(
    ctx context.Context,
    toolCallCh <-chan engine.ToolCall,  // 流式接收工具调用
) <-chan ToolResult                     // 流式返回结果

type ToolResult struct {
    ToolCallID string
    Name       string
    Content    string  // 格式化后的结果文本
    Error      string
    Duration   time.Duration
}
```

### 并发执行逻辑

```go
func (o *Orchestrator) Execute(
    ctx context.Context,
    toolCalls []engine.ToolCall,
) []ToolResult {
    // 分区：只读 vs 写入
    readOnly, writeOps := o.partition(toolCalls)

    results := make([]ToolResult, 0, len(toolCalls))

    // 只读工具并发执行
    if len(readOnly) > 0 {
        readResults := o.executeConcurrent(ctx, readOnly)
        results = append(results, readResults...)
    }

    // 写入工具串行执行
    for _, tc := range writeOps {
        result := o.executeSingle(ctx, tc)
        results = append(results, result)
    }

    return results
}

func (o *Orchestrator) partition(toolCalls []engine.ToolCall) (readOnly, writeOps []engine.ToolCall) {
    for _, tc := range toolCalls {
        level := o.executor.SecurityLevel(tc.Name)
        if level <= 0 { // Level 0 = 只读
            readOnly = append(readOnly, tc)
        } else {
            writeOps = append(writeOps, tc)
        }
    }
    return
}

func (o *Orchestrator) executeConcurrent(
    ctx context.Context,
    toolCalls []engine.ToolCall,
) []ToolResult {
    sem := make(chan struct{}, o.maxConcurrency)
    resultCh := make(chan ToolResult, len(toolCalls))

    var wg sync.WaitGroup
    for _, tc := range toolCalls {
        wg.Add(1)
        go func(tc engine.ToolCall) {
            defer wg.Done()
            sem <- struct{}{}        // 获取信号量
            defer func() { <-sem }() // 释放信号量

            resultCh <- o.executeSingle(ctx, tc)
        }(tc)
    }

    wg.Wait()
    close(resultCh)

    results := make([]ToolResult, 0, len(toolCalls))
    for r := range resultCh {
        results = append(results, r)
    }

    // 按原始顺序排序（保持确定性）
    sort.Slice(results, func(i, j int) bool {
        return indexOf(toolCalls, results[i].ToolCallID) <
               indexOf(toolCalls, results[j].ToolCallID)
    })

    return results
}
```

### 流式预执行

```go
// ExecuteStreaming — 模型输出过程中预执行工具
// 对标 Claude Code 的 StreamingToolExecutor
func (o *Orchestrator) ExecuteStreaming(
    ctx context.Context,
    toolCallCh <-chan engine.ToolCall,
) <-chan ToolResult {
    resultCh := make(chan ToolResult, 10)
    sem := make(chan struct{}, o.maxConcurrency)

    go func() {
        defer close(resultCh)
        var wg sync.WaitGroup

        for tc := range toolCallCh {
            level := o.executor.SecurityLevel(tc.Name)
            if level > 0 {
                // 写入工具不预执行，等模型完整输出后串行
                // 直接放入结果队列等待后续处理
                resultCh <- ToolResult{
                    ToolCallID: tc.ID,
                    Name:       tc.Name,
                    Content:    "", // 标记为待执行
                }
                continue
            }

            // 只读工具立即并发启动
            wg.Add(1)
            go func(tc engine.ToolCall) {
                defer wg.Done()
                sem <- struct{}{}
                defer func() { <-sem }()

                resultCh <- o.executeSingle(ctx, tc)
            }(tc)
        }

        wg.Wait()
    }()

    return resultCh
}
```

## ResultHandler

### 职责

1. 控制工具结果大小（动态预算，不硬截断）
2. 大结果写磁盘 + 内联摘要
3. 格式化结果为模型友好的文本

### 接口定义

```go
type ResultHandler struct {
    maxResultSize    int    // 单个工具结果最大字符数，默认 4000
    maxTotalBudget   int    // 所有结果总预算，基于剩余上下文窗口动态计算
    persistDir       string // 大结果持久化目录
}

func NewResultHandler(maxResultSize int, persistDir string) *ResultHandler

// Process 处理工具结果列表
func (h *ResultHandler) Process(results []ToolResult, remainingTokens int) []ToolResult

// SetBudget 根据剩余上下文窗口调整预算
func (h *ResultHandler) SetBudget(remainingTokens int)
```

### 结果处理逻辑

```go
func (h *ResultHandler) Process(results []ToolResult, remainingTokens int) []ToolResult {
    // 动态预算：剩余上下文的 30%（留 70% 给模型思考和输出）
    totalBudget := remainingTokens * 30 / 100
    if totalBudget < 2000 {
        totalBudget = 2000 // 最低保障
    }

    // 每个工具均分预算
    perToolBudget := totalBudget / len(results)
    if perToolBudget > h.maxResultSize {
        perToolBudget = h.maxResultSize
    }
    if perToolBudget < 500 {
        perToolBudget = 500 // 单个最低保障
    }

    processed := make([]ToolResult, len(results))
    for i, r := range results {
        processed[i] = h.processOne(r, perToolBudget)
    }
    return processed
}

func (h *ResultHandler) processOne(r ToolResult, budget int) ToolResult {
    if r.Error != "" {
        return r // 错误消息不截断
    }

    content := r.Content
    if len(content) <= budget {
        return r // 不超预算，原样返回
    }

    // 超预算：智能截断
    result := r // 创建副本（不可变）

    if h.persistDir != "" {
        // 写磁盘 + 内联摘要（对标 Claude Code 的大结果处理）
        filePath := h.persistToDisk(r)
        result.Content = h.smartTruncate(content, budget-200) +
            fmt.Sprintf("\n\n...(结果已截断，完整内容 %d 字符，保存在 %s)", len(content), filePath)
    } else {
        // 无持久化：直接智能截断
        result.Content = h.smartTruncate(content, budget)
    }

    return result
}

// smartTruncate — 智能截断，保留头尾信息
func (h *ResultHandler) smartTruncate(content string, budget int) string {
    if len(content) <= budget {
        return content
    }

    // 保留前 70% + 后 20% + 中间省略提示
    headSize := budget * 70 / 100
    tailSize := budget * 20 / 100

    head := content[:headSize]
    tail := content[len(content)-tailSize:]
    omitted := len(content) - headSize - tailSize

    return head +
        fmt.Sprintf("\n\n... (%d 字符已省略) ...\n\n", omitted) +
        tail
}
```

### 结果格式化

```go
// FormatToolResult — 将 SkillResult 格式化为模型友好的文本
func FormatToolResult(name string, sr *SkillResult) string {
    if sr.Error != "" {
        return fmt.Sprintf("Error executing %s: %s", name, sr.Error)
    }

    // 优先使用 Rendered（格式化好的文本）
    if sr.Rendered != "" {
        return sr.Rendered
    }

    // 表格数据格式化为对齐文本
    if data, ok := sr.Data.([]map[string]any); ok && len(data) > 0 {
        return formatTable(data)
    }

    // 纯文本
    if sr.Text != "" {
        return sr.Text
    }

    // Fallback
    return sr.Summary
}

// formatTable — 格式化表格数据
func formatTable(rows []map[string]any) string {
    if len(rows) == 0 {
        return "(no data)"
    }

    // 收集列名（保持顺序）
    var cols []string
    for k := range rows[0] {
        cols = append(cols, k)
    }
    sort.Strings(cols)

    // 计算列宽
    widths := make(map[string]int)
    for _, col := range cols {
        widths[col] = len(col)
    }
    for _, row := range rows {
        for _, col := range cols {
            v := fmt.Sprintf("%v", row[col])
            if len(v) > widths[col] {
                widths[col] = len(v)
            }
        }
    }

    // 限制单列最大宽度
    for col := range widths {
        if widths[col] > 60 {
            widths[col] = 60
        }
    }

    var buf strings.Builder

    // 表头
    for _, col := range cols {
        buf.WriteString(fmt.Sprintf("%-*s  ", widths[col], col))
    }
    buf.WriteString("\n")

    // 分隔线
    for _, col := range cols {
        buf.WriteString(strings.Repeat("─", widths[col]))
        buf.WriteString("  ")
    }
    buf.WriteString("\n")

    // 数据行（限制最多 50 行）
    maxRows := 50
    for i, row := range rows {
        if i >= maxRows {
            buf.WriteString(fmt.Sprintf("... (%d more rows)\n", len(rows)-maxRows))
            break
        }
        for _, col := range cols {
            v := fmt.Sprintf("%v", row[col])
            if len(v) > widths[col] {
                v = v[:widths[col]-1] + "…"
            }
            buf.WriteString(fmt.Sprintf("%-*s  ", widths[col], v))
        }
        buf.WriteString("\n")
    }

    return buf.String()
}
```
