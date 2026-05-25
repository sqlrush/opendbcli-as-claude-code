# PromptToolAdapter 设计文档 (v1.2.0)

> 让 opendb 在 LLM 关闭 Function Calling 时仍能完整调用 60+ skill，质量逼近 FC 模式

**作者**: opendb team
**版本**: Design v1 (待评审)
**目标版本**: opendb v1.2.0
**预计工作量**: 1.5-2 周（含 benchmark 迭代）
**评审状态**: 🟡 待客户/Tech Lead 评审

---

## 1. 背景

### 1.1 问题

opendb 当前的 Engine 主循环 (`internal/engine/engine.go`) 强依赖 LLM 的原生 Function Calling 能力：

```go
resp, _ := provider.Chat(ctx, req)
if len(resp.ToolCalls) == 0 {
    // 终态：LLM 没要求调工具，用 resp.Content 作答
    return resp.Content
}
// ReAct: 执行 tool_calls，把结果拼回 messages，继续下一轮
for _, tc := range resp.ToolCalls { ... }
```

**强依赖点**: `resp.ToolCalls` 字段必须由 LLM provider 返回结构化数据。

### 1.2 客户场景

某客户使用 Qwen3.6 / Qwen3.2，部署环境不开启 FC：

| 不开 FC 的常见原因 | 占比 |
|---|---|
| vLLM 未加 `--enable-auto-tool-choice --tool-call-parser hermes` | >50% |
| vLLM 版本 < 0.6（tools 接口未 GA） | ~20% |
| Chat template 错配（用 ChatML 而非 qwen-tool-use） | ~15% |
| API gateway 剥 tools 参数 | ~10% |
| 国产推理框架（华为 MindIE / 百度 PaddleNLP）不支持 | ~5% |

**优先让客户开 FC**；如果合规/运维流程改不动，需要 opendb 在 Prompt 层兜底实现 tool calling。

### 1.3 现状影响

当前不开 FC 直接接入 opendb 的结果：

```
Engine 第一轮调 LLM → resp.ToolCalls == [] → 直接退出循环 
                  → 把 LLM 的"我建议跑 health 看一下"文本作为最终答案
                  → opendb 60+ skill 一个都没执行
                  → 用户拿到的就是一段空话
```

---

## 2. 目标 / 非目标

### 2.1 目标

| 编号 | 目标 | 验收标准 |
|---|---|---|
| G1 | 不开 FC 的 LLM 能调用 opendb 全部 skill | 60+ skill 均可通过 prompt 模式正确触发 |
| G2 | Prompt 模式输出质量逼近 FC 模式 | benchmark 50 case 上 ≥ 95% (相对 FC 基线) |
| G3 | 对当前所有 FC 模型零侵入 | 全部现有 e2e 测试通过；输出 byte-level 一致 |
| G4 | 配置层简单可控 | 加一个 `tool_mode` 字段切换；不设默认 native |
| G5 | 失败可回退 | 一行 yaml 配置或环境变量瞬时切回 native |

### 2.2 非目标

- **不**取代原生 FC：FC 始终是首选路径
- **不**支持 LLM 完全无 Chat 能力的场景（如纯 completions API）
- **不**为模型微调投入资源（v1.2.x 只做 prompt + 解析层；如需微调走另立项目）
- **不**做跨 turn 的工具调用计划（仍是单 turn 单决策的 ReAct，不做 Plan-and-Execute）

---

## 3. 架构设计

### 3.1 整体原则：共享内核 + 双适配壳

```
                 ┌─────────────────────────────────────────┐
                 │  共享 Prompt 内核 (~70% 内容)            │
                 │  -─────────────────────────────────────-  │
                 │  - 角色定义 (你是 OpenDB 诊断专家...)    │
                 │  - 推理原则 (用证据说话 / 禁编造 / ...)   │
                 │  - 工具选择策略                          │
                 │    (单 SQL → sqltune;                    │
                 │     聚类诊断 → health→alert→...;          │
                 │     WDR → wdranalyze)                    │
                 │  - 输出质量规范                          │
                 │    (passthrough 例外 / 无"建议补充" / ...) │
                 │  - 三层诊断输出模板 (Layer 1/2/3)        │
                 └──────────┬──────────────────────────────┘
                            │
                ┌───────────┴───────────┐
                │                       │
        ┌───────▼────────┐      ┌───────▼────────┐
        │  NativeFCBuilder │      │ PromptModeBuilder │
        │  (~15% 独有)    │      │  (~15% 独有)    │
        │                │      │                │
        │  - tools 数组   │      │  - 工具描述塞   │
        │    走 API 字段  │      │    进 system    │
        │  - 信任原生     │      │  - Format A/B   │
        │    tool_calls   │      │    规则        │
        │                │      │  - few-shot     │
        │                │      │    示例 5-10 个 │
        │                │      │  - JSON 输出    │
        │                │      │    强约束      │
        └───────┬────────┘      └───────┬────────┘
                │                       │
                └──────────┬────────────┘
                           │
                ┌──────────▼──────────┐
                │  Provider.Chat()    │
                │  (Engine 不感知模式) │
                └─────────────────────┘
```

### 3.2 数据流

**Native FC 模式**（现有路径）：

```
Engine.Run
  ↓ req: {messages, tools: [JSON Schema array]}
NativeFCBuilder.BuildSystemPrompt → 返回原 prompt（不动 tools 字段）
  ↓
Provider.Chat → vLLM/Claude/GPT 原生 tool API
  ↓ resp: {content: "", tool_calls: [{name, args}, ...]}
NativeFCBuilder.PostProcessResponse → 直接返回（无处理）
  ↓
Engine 解析 resp.ToolCalls，并行执行
```

**Prompt 模式**（新增）：

```
Engine.Run
  ↓ req: {messages, tools: [JSON Schema array]}
PromptModeBuilder.BuildSystemPrompt:
  1. 把 tools 序列化成 compact 文本
  2. 拼到 base system prompt 末尾
  3. 加 Format A/B 规则 + few-shot
  4. 清空 req.tools 字段（防 vLLM 报错）
  ↓
Provider.Chat → vLLM 普通 Chat API（不带 tools 参数）
  ↓ resp: {content: "```json\n{...}\n```", tool_calls: nil}
PromptModeBuilder.PostProcessResponse:
  1. 检测 resp.content 是 Format A 还是 B
  2. Format A: 解析 JSON → resp.tool_calls; resp.content = ""
  3. Format B: 保持原样
  4. 解析失败 → 重试机制（最多 2 次）
  ↓
Engine 看到结构化 resp.ToolCalls（跟 FC 模式无差别），继续 ReAct
```

### 3.3 类型设计

```go
// 新增: internal/engine/provider/prompt_builder.go

// PromptBuilder 是 provider 层的 prompt 注入 + 响应后处理插件
type PromptBuilder interface {
    // BuildSystemPrompt 在 base prompt 基础上增删工具相关内容
    BuildSystemPrompt(basePrompt string, tools []ToolSchema) string

    // PrepareRequest 在调 LLM 前对 ChatRequest 做最终处理
    // (Prompt 模式需要清空 req.Tools 字段)
    PrepareRequest(req *ChatRequest)

    // PostProcessResponse 在 LLM 响应后处理
    // (Prompt 模式需要从 resp.Content 解析 JSON 到 resp.ToolCalls)
    PostProcessResponse(resp *Response) *Response

    // Mode 返回 builder 的标识字符串 (日志用)
    Mode() string
}

// NativeFCBuilder: 默认实现，零侵入现有路径
type NativeFCBuilder struct{}

func (NativeFCBuilder) BuildSystemPrompt(base string, _ []ToolSchema) string {
    return base // 工具走 API tools 参数，prompt 不动
}
func (NativeFCBuilder) PrepareRequest(_ *ChatRequest) {} // no-op
func (NativeFCBuilder) PostProcessResponse(r *Response) *Response { return r }
func (NativeFCBuilder) Mode() string { return "native" }

// PromptModeBuilder: 新增实现
type PromptModeBuilder struct {
    fewShots       []FewShotExample
    toolFilter     ToolFilter           // 工具子集筛选
    parser         *JSONToolCallParser  // 解析器
    maxParseRetry  int                  // 默认 2
}

func (b *PromptModeBuilder) BuildSystemPrompt(base string, tools []ToolSchema) string {
    filtered := b.toolFilter.Filter(tools, ...)  // 按场景精简
    return base +
        "\n\n# 可用工具\n" + serializeToolsCompact(filtered) +
        "\n\n# 输出格式\n" + formatRulesPrompt +
        "\n\n# 示例\n" + b.renderFewShots()
}
func (b *PromptModeBuilder) PrepareRequest(req *ChatRequest) {
    req.Tools = nil  // 关键：清空 tools 字段防 vLLM 拒绝
}
func (b *PromptModeBuilder) PostProcessResponse(r *Response) *Response {
    return b.parser.Parse(r)
}
func (b *PromptModeBuilder) Mode() string { return "prompt" }
```

### 3.4 Provider 接入

```go
// 修改: internal/engine/provider/openaicompat.go

type OpenAICompatProvider struct {
    // ... 现有字段
    builder PromptBuilder // 新增
}

func NewOpenAICompatProvider(cfg Config) *OpenAICompatProvider {
    builder := selectPromptBuilder(cfg.ToolMode, cfg.Model)
    return &OpenAICompatProvider{builder: builder, ...}
}

func selectPromptBuilder(mode string, modelID string) PromptBuilder {
    switch mode {
    case "prompt":
        return NewPromptModeBuilder(...)
    case "auto":
        return NewAutoProbingBuilder(...) // v1.2.x 选做
    default:
        return NativeFCBuilder{}
    }
}

func (p *OpenAICompatProvider) Chat(ctx context.Context, req ChatRequest) (*Response, error) {
    // 1. 系统 prompt 注入
    if req.SystemPrompt != nil {
        req.SystemPrompt[0].Text = p.builder.BuildSystemPrompt(
            req.SystemPrompt[0].Text, req.Tools)
    }
    // 2. 请求最终处理
    p.builder.PrepareRequest(&req)
    // 3. 调底层 API
    resp, err := p.callAPI(ctx, req)
    if err != nil {
        return nil, err
    }
    // 4. 响应后处理
    return p.builder.PostProcessResponse(resp), nil
}
```

---

## 4. 详细设计

### 4.1 配置层

#### 4.1.1 yaml schema 扩展

```yaml
models:
  - name: qwen36-customer
    provider: openai
    vendor: Qwen
    base_url: http://customer-vllm:8000/v1
    model: qwen3.6
    capability: large
    tool_mode: prompt        # 新增字段
    # tool_mode 可选值:
    #   native (默认): 用原生 tool_calls API
    #   prompt: 用 PromptToolAdapter
    #   auto:   首次调用探测，缓存结果（v1.2.x 选做）

  # 现有模型不写 tool_mode，保持 native 行为
  - name: opus
    provider: openai
    vendor: Anthropic
    model: opus-4.6
    # 没写 tool_mode → 等效 tool_mode: native
```

#### 4.1.2 模型配置代码

```go
// 修改: internal/model/config.go

type Config struct {
    Name        string `yaml:"name"`
    Provider    string `yaml:"provider"`
    Model       string `yaml:"model"`
    Capability  string `yaml:"capability"`
    ToolMode    string `yaml:"tool_mode,omitempty"` // 新增
    // ... 现有字段
}

// 修改: internal/model/capability.go (复用现有 InferCapability 风格)

// InferToolMode 按模型 ID 推断默认 tool_mode（仅当 yaml 没显式设时使用）
func InferToolMode(provider, modelID string) string {
    // 默认 native，所有现有逻辑零影响
    return "native"
}
```

### 4.2 工具描述精简（ToolFilter）

#### 4.2.1 为什么需要

| 不精简 | 精简 |
|---|---|
| 60+ skill × 平均 150 字描述 = ~8K token | 按场景挑 5-15 个 skill = ~1-2K token |
| 每轮重复 8K 入参 | 每轮 1-2K |
| LLM 注意力被稀释 | 选择空间小，准确率高 |

注意：FC 模式也可受益于精简（省 schema 序列化开销 + 提高准确率），所以 ToolFilter **两个模式共用**。

#### 4.2.2 接口与实现

```go
// 新增: internal/engine/tool/filter.go

type ToolFilter interface {
    // Filter 根据 context 从全量 tools 里选出本轮要传给 LLM 的子集
    Filter(allTools []ToolSchema, ctx FilterContext) []ToolSchema
}

type FilterContext struct {
    UserMessage    string      // 用户原始问题
    PreviousRounds int         // 已经过去几轮
    LastToolCalls  []ToolCall  // 上一轮调了哪些工具
    Database       string      // oracle / mysql / postgres / opengauss
}

type SceneBasedFilter struct {
    scenes []Scene
}

type Scene struct {
    Name       string
    Triggers   []string    // 匹配用户消息的关键词
    Tools      []string    // 这个场景下要注入的 skill name 列表
}

var defaultScenes = []Scene{
    {
        Name:     "single_sql_tune",
        Triggers: []string{"SQL_ID", "怎么优化", "调优", "SQL 慢"},
        Tools:    []string{"sqltune", "sqlfetch", "explain"},
    },
    {
        Name:     "cluster_diag",
        Triggers: []string{"数据库慢", "出问题", "卡", "死锁", "性能"},
        Tools:    []string{"health", "alert", "activesessions", "waits",
                          "blocktree", "topsql", "slowsql"},
    },
    {
        Name:     "wdr_analysis",
        Triggers: []string{"wdr", "WDR", "awr", "AWR", "报告"},
        Tools:    []string{"wdranalyze", "wdr_snapshot"},
    },
    {
        Name:     "memory_io",
        Triggers: []string{"内存", "缓存", "命中率", "IO", "buffer"},
        Tools:    []string{"health", "params", "objstats"},
    },
    {
        Name:     "fallback",
        Triggers: []string{}, // 默认场景
        Tools:    []string{"sql", "health"}, // 万能兜底
    },
}

func (f SceneBasedFilter) Filter(all []ToolSchema, ctx FilterContext) []ToolSchema {
    // 1. 第一轮按用户消息匹配场景
    // 2. 后续轮次根据 LastToolCalls 扩展（如调了 alert 后注入 alert detail 相关 tool）
    // 3. 始终包含 "fallback" 场景的工具兜底
    // 4. 去重 + 保留原始 tool order
    ...
}
```

#### 4.2.3 工具描述压缩

**FC 模式**保留完整 JSON Schema（vLLM/OpenAI API 需要）。

**Prompt 模式**用紧凑 Markdown：

```markdown
# 可用工具

## health
查询数据库整体健康状态 (实例/会话/锁/缓存)
参数: 无

## sqltune
对单条 SQL 做 5 维度调优分析 (重写/索引/HINT/统计/表结构)
参数:
  args (string, 必填): 完整可 EXPLAIN 的 SQL 文本

## wdranalyze
解析 WDR 报告并生成三层分析 (总览/风险详解/优化方案)
参数:
  args (string, 必填): "file <path>" 或 "latest" 或 "<snapA> <snapB>"

## ... (其他工具)
```

每个工具固定 4-5 行。60 工具全列 → 240-300 行 ≈ 2K token。filter 后 5-10 工具 → 30-50 行 ≈ 300-500 token。

### 4.3 Prompt 模式系统提示

#### 4.3.1 完整模板结构

```
{base system prompt 共享内核}
─────────────────────────────────────
# 可用工具
{filtered tool descriptions}

# 输出格式规则

你必须在下面两种格式中**严格二选一**，不要混用：

## 格式 A — 调用工具

当你需要采集数据时，**仅**输出一个 JSON 代码块，不要任何前缀或解释：

```json
{"tool_calls": [{"name": "<工具名>", "args": {"<key>": "<value>"}}]}
```

允许一次声明多个 tool_calls（数组），但 vLLM 后台会串行执行。

## 格式 B — 给最终答案

当你已经有足够数据回答时，**直接输出 markdown 答案**，不要 JSON。
答案需符合"OpenDB 三层诊断输出模板"（见上文）。

## 关键规则

1. **第一个字符决定格式**: ``` 开头表示格式 A，其他字符表示格式 B
2. **不要混用**: 不要输出 "我先调 health 工具：```json{...}```" 这种前缀
3. **格式 A 内不要加解释**: 调工具就只输出 JSON，不要"我准备调..." 这种话
4. **工具名必须严格匹配**: 不要简写或意译（health 不能写成 heath/health_check）
5. **不调工具不能输出 JSON**: 格式 B 是纯文本，不允许包含 ```json 块

# 示例

## 示例 1: 单 SQL 调优
User: SQL_ID 4175761868 怎么优化
Assistant:
```json
{"tool_calls": [{"name": "sqltune", "args": {"args": "SELECT ..."}}]}
```

## 示例 2: 聚类诊断起步
User: 数据库怎么有点慢
Assistant:
```json
{"tool_calls": [{"name": "health", "args": {}}]}
```

## 示例 3: 工具结果已足够，给答案
User: (前面调了 health + alert + activesessions，已经看到死锁信息)
Assistant:
## 根因分析
死锁源于 customer 表的 ID 14523 行...

## 紧急措施
SELECT pg_terminate_backend(12345);

## 根因修复
...
```

#### 4.3.2 Few-shot 选型原则

| 类别 | 数量 | 覆盖场景 |
|---|---|---|
| 单工具调用 | 2 | sqltune / health |
| 多工具并行 | 1 | health + alert 同时声明 |
| 无参数工具 | 1 | health |
| 复杂参数（含 SQL） | 1 | sqltune 完整 SQL |
| 文件路径参数 | 1 | wdranalyze file /tmp/x.html |
| 最终答案（Format B） | 2 | 含三层模板 / 简短答案 |
| 错误恢复 | 1 | 上一轮 JSON 报错后的修正 |

合计 9 个 few-shot，可裁剪到 5-6 个高 ROI 的（按 benchmark 命中率排序）。

### 4.4 JSON 响应解析器

#### 4.4.1 解析器状态机

```
LLM Response (string)
  │
  ├─ 步骤 1: 提取 JSON 块 (容错优先级)
  │   1. 找 ```json ... ``` 代码块（首选）
  │   2. 找 ``` ... ```（无 lang 标签）
  │   3. 找首个 { 到匹配 } 的子串（balance 计数）
  │   4. 整个 content 当 JSON
  │
  │   失败 → 跳到步骤 5
  │
  ├─ 步骤 2: JSON 容错修复
  │   1. 单引号 → 双引号
  │   2. 尾随逗号去除
  │   3. unquoted key 加引号 (基于正则)
  │   4. 注释行去除 (// 和 /* */)
  │
  ├─ 步骤 3: 字段校验
  │   1. 必须有 tool_calls 数组
  │   2. 每个 tool_call 必须有 name 字段
  │   3. args 缺失 → 兜底空 map (允许无参数工具)
  │
  ├─ 步骤 4: 工具名纠错
  │   1. 严格匹配 (case-sensitive) → 成功返回
  │   2. 小写匹配 → 矫正后返回
  │   3. Levenshtein 距离 ≤ 1 → 矫正后返回 (heath → health)
  │   4. 失败 → 返回解析错误
  │
  └─ 步骤 5: Format B fallback
      内容当作最终答案返回 (resp.Content 不变, ToolCalls = nil)
```

#### 4.4.2 错误反馈循环

第一次解析失败时，不直接返回给用户，而是把错误反馈给 LLM 重试：

```go
func (b *PromptModeBuilder) PostProcessResponse(r *Response) *Response {
    parsed, err := b.parser.Parse(r.Content)
    if err == nil {
        return parsed
    }
    // 解析失败 → 触发重试机制
    if r.RetryCount < b.maxParseRetry {
        return b.requestRetry(r, err)
    }
    // 重试用完 → 当 Format B 答案返回
    return r
}

func (b *PromptModeBuilder) requestRetry(r *Response, parseErr error) *Response {
    // 在 messages 末尾追加 system message 反馈错误，触发下一轮 LLM 调用
    // (实际重试在 Engine 的下一个 turn 完成，这里只是构造反馈消息)
    feedbackMsg := fmt.Sprintf(
        "你上一轮输出格式错误: %s\n"+
        "请严格按 Format A 或 Format B 重新回答, 不要混用.\n"+
        "示例 Format A: ```json\n{\"tool_calls\":[{\"name\":\"<工具>\",\"args\":{...}}]}\n```",
        parseErr.Error())
    r.RetryFeedback = feedbackMsg // 新增字段
    r.NeedRetry = true
    return r
}
```

Engine 检测 `r.NeedRetry == true` 时，把 `r.RetryFeedback` 作为 system message 追加到 messages，继续 ReAct loop（不当成正常 round）。

### 4.5 流式处理

#### 4.5.1 模式判定

第一个 chunk 到达时判定：

```
First chunk 前 50 字符
  │
  ├─ 包含 "```json" / "```" → JSON Mode → 缓冲直到 ``` 闭合 → 解析
  ├─ 以 "{" 开头 → JSON Mode → 缓冲直到 { } 配对 → 解析  
  └─ 其他文本 → Text Mode → 直接 stream 给 OnStream
```

#### 4.5.2 实现

```go
type StreamingParser struct {
    buf         strings.Builder
    mode        StreamMode // unknown / json / text
    onText      func(string)
    onToolCall  func([]ToolCall)
    onError     func(error)
}

func (p *StreamingParser) Feed(chunk string) {
    p.buf.WriteString(chunk)
    if p.mode == StreamModeUnknown {
        p.mode = detectMode(p.buf.String())
    }
    switch p.mode {
    case StreamModeJSON:
        if isJSONComplete(p.buf.String()) {
            calls, err := parseToolCalls(p.buf.String())
            if err != nil {
                p.onError(err)
            } else {
                p.onToolCall(calls)
            }
        }
    case StreamModeText:
        p.onText(chunk) // 实时流给 REPL
    }
}
```

#### 4.5.3 性能影响

| 场景 | 影响 |
|---|---|
| Format A (调工具)，平均 100-200 字 JSON | 用户多等 0.5-2 秒（不流式） |
| Format B (最终答案)，长 2-5K 字 | 流式，跟原生 FC 无差别 |

整体用户体感差异不显著。

### 4.6 Passthrough 短路兼容

v1.1.51 加的 `<!-- WDR_REPORT_BEGIN` marker 短路逻辑**完全无需改动**：

```go
// engine.go (现有代码)
if containsPassthroughMarker(tr.Content) {
    passthrough = stripPassthroughMarker(tr.Content)
    break
}
```

这是在 tool result 上检测，跟 LLM provider 用哪种模式无关。两个模式都自动受益。

---

## 5. 实施阶段

### 5.1 Phase 1: 框架与基础解析 (3 天)

| 任务 | 验收 |
|---|---|
| `internal/engine/provider/prompt_builder.go` 接口 + NativeFCBuilder | 单元测试覆盖；现有所有 e2e 通过 |
| `internal/engine/provider/prompt_mode_builder.go` 骨架 | 能加载、能传 prompt |
| `internal/engine/provider/json_parser.go` 解析器 | 单元测试 30+ case |
| 配置层 `tool_mode` 字段 | yaml 解析正确，默认 native |
| 注入到 openaicompat | 选 prompt 模式时 tools=nil |

### 5.2 Phase 2: Prompt 模板与 few-shot (3 天)

| 任务 | 验收 |
|---|---|
| 起草 Format A/B 规则模板 | 人工 review |
| 编写 5-9 个 few-shot 示例 | 覆盖 9 个典型场景 |
| ToolFilter 场景化精简 | 5 个场景定义，单元测试 |
| 工具描述紧凑序列化 | 60 工具序列化 < 2K token |

### 5.3 Phase 3: 容错与错误反馈 (2 天)

| 任务 | 验收 |
|---|---|
| JSON 自动修复 (引号/逗号/注释) | 单元测试 15+ case |
| 工具名 Levenshtein 纠错 | 测试 heath→health 等 |
| 错误反馈循环机制 | 集成测试模拟 LLM 输出错误 |

### 5.4 Phase 4: 流式适配 (2 天)

| 任务 | 验收 |
|---|---|
| StreamingParser 状态机 | 单元测试 chunk-by-chunk |
| OnStream 回调路径打通 | REPL 实测体验无明显延迟 |
| JSON 模式缓冲 + 解析 | 不破坏 passthrough 短路 |

### 5.5 Phase 5: Benchmark 与迭代 (3-5 天)

| 任务 | 验收 |
|---|---|
| Benchmark 框架 (50 case × 2 模式 × 多模型) | 自动跑全套对比 |
| 收集 v0 命中率 | 报告生成 |
| 按 benchmark 数据定向优化 prompt/parser | 命中率达到 ≥ 95% (相对 FC) |
| 文档 + CHANGELOG | 用户可读 |

### 5.6 风险缓冲 (2 天)

- 应对意外的 Qwen3 行为
- 客户接入兼容性问题

**总计**: 1.5-2 周

---

## 6. Benchmark 设计

### 6.1 评测集 (50 cases)

按 skill 类型与复杂度分层：

| 类别 | 数量 | 示例 |
|---|---|---|
| 单工具简单调用 | 12 | "查数据库健康" → health |
| 单工具带参数 | 10 | "看 60s 内 Top SQL" → topsql 60s |
| 工具链 (3+ 步) | 8 | "数据库慢" → health → alert → activesessions → 综合 |
| 复杂参数 (SQL) | 7 | "优化这条 SQL: ..." → sqltune |
| 复杂参数 (文件路径) | 5 | "分析 /tmp/wdr.html" → wdranalyze file ... |
| 三层模板输出 | 5 | wdranalyze 后输出 Layer 1/2/3 |
| 边界 (无工具直接答) | 3 | "openGauss 是什么" → 直接 Format B |

### 6.2 评分维度

每个 case 独立打分（FC 模式 vs Prompt 模式）：

| 维度 | 权重 | 评分方式 |
|---|---|---|
| 工具调用准确率（调对了工具吗） | 30% | 1/0 |
| 参数正确率 | 25% | 1/0.5/0 (完全对/部分对/错) |
| 工具链完整度 | 20% | 期望步数 / 实际步数 |
| 最终输出质量 | 15% | 人工 1-5 分 |
| 延迟 | 10% | 相对 FC 的倍数 |

### 6.3 目标

| 指标 | v1.2.0 GA | v1.2.x 终态 |
|---|---|---|
| 工具调用准确率 | ≥ 92% | ≥ 97% |
| 参数正确率 | ≥ 88% | ≥ 95% |
| 综合分 (Prompt / FC) | ≥ 90% | ≥ 95% |
| 平均延迟倍数 | ≤ 1.3× | ≤ 1.15× |

### 6.4 模型矩阵

| 模型 | tool_mode | 优先级 |
|---|---|---|
| Qwen3.6 | prompt | P0 (客户必测) |
| Qwen3.2 | prompt | P0 (客户必测) |
| Qwen2.5-72B | prompt | P1 (兼容老版本) |
| GLM-5.1 | prompt | P2 (验证泛化) |
| DeepSeek-V4 | native | P0 (回归测试) |
| Opus 4.6 | native | P0 (回归测试) |

---

## 7. 兼容性保证

### 7.1 对现有 FC 模型零侵入

| 保证 | 实现方式 |
|---|---|
| 默认行为不变 | 不设 `tool_mode` → NativeFCBuilder → byte-level 一致 |
| 现有测试通过 | 全部 e2e (700+ 测试) 全跑过 |
| 性能不退化 | NativeFCBuilder 全部 method 都是 zero-op 或 return-as-is |
| 可瞬时回退 | 改 yaml `tool_mode` 字段即生效，无需重启 |

### 7.2 灰度发布

1. **内部测试**: 用 qwen36-customer 配置在 og 实例验证 (1-2 天)
2. **客户 dry-run**: 客户接入测试环境，跑 benchmark 50 case
3. **生产灰度**: 客户某个非关键 db 实例先用 prompt 模式
4. **全量切换**: 客户主力环境切 prompt 模式

任何阶段出问题 → 一行 yaml 改回 native 立即回退。

---

## 8. 风险与缓解

| 风险 | 等级 | 缓解 |
|---|---|---|
| Qwen3 长上下文 (>50K) JSON 输出衰减 | 🟡 | benchmark 必须含长 context case；如确认衰减则限制 prompt 模式下的 context 长度 |
| 工具名 Levenshtein 误纠（如 health → wealth） | 🟢 | 限制 distance ≤ 1 + 候选必须在工具表里 |
| 串行调用拖慢 wdranalyze (60s+) 链路 | 🟡 | 文档明确告知；引导客户优化 vLLM 配置开 FC |
| Few-shot 示例污染答案风格 | 🟢 | 多样化 few-shot；benchmark 监控输出质量 |
| JSON 解析重试死循环 | 🟢 | 硬限制 maxParseRetry=2，超过转 Format B |
| Format A/B 混用（LLM 输出 "我先调..." 前缀加 JSON） | 🟡 | parser 容错: 找第一个 ```json 或 { 开始解析；同时 prompt 强约束 |
| 配置错误（tool_mode 拼错）回退到 native 没察觉 | 🟢 | yaml schema 校验 + 启动时日志 "tool_mode=prompt active" |

---

## 9. 文件清单

### 9.1 新增

```
internal/engine/provider/prompt_builder.go         (~100 行) - 接口 + NativeFCBuilder
internal/engine/provider/prompt_mode_builder.go    (~250 行) - PromptModeBuilder 实现
internal/engine/provider/json_parser.go            (~200 行) - JSON 解析 + 容错 + 纠错
internal/engine/provider/streaming_parser.go       (~150 行) - 流式适配状态机
internal/engine/provider/prompt_mode_builder_test.go  (~300 行)
internal/engine/provider/json_parser_test.go       (~200 行)
internal/engine/tool/filter.go                     (~150 行) - SceneBasedFilter
internal/engine/tool/filter_test.go                (~100 行)
internal/engine/tool/tool_serializer.go            (~100 行) - 紧凑序列化
benchmark/prompt_mode/                              (~500 行) - 50 case + 评测脚本
docs/design-prompt-tool-adapter-v1.2.0.md          (本文档)
```

### 9.2 修改

```
internal/model/config.go                           (+10 行) - 加 ToolMode 字段
internal/model/capability.go                       (+15 行) - InferToolMode
internal/engine/provider/openaicompat.go           (+30 行) - 接入 PromptBuilder
internal/engine/provider/types.go                  (+20 行) - Response 加 RetryFeedback / NeedRetry
internal/engine/engine.go                          (+15 行) - 处理 NeedRetry
docs/CHANGELOG.md                                  (+30 行) - v1.2.0 发版说明
```

**总计**: 约 **2200 行新增** + 120 行修改

---

## 10. 后续 v1.2.x 改进方向

| 版本 | 内容 |
|---|---|
| v1.2.1 | auto 模式: 首次探测 → 缓存; 不需要客户手动配 tool_mode |
| v1.2.2 | 工具 schema 校验层 (执行前 args 类型/必填校验) |
| v1.2.3 | 模型特化 prompt 模板 (Qwen / GLM / DeepSeek 各一套微调) |
| v1.2.4 | DM/MySQL/PG 库特化 few-shot |
| v1.3.0 | (可选) 用一个小模型做 router（PromptToolAdapter 之外的另一个方案）|

---

## 11. 评审 checklist

请评审者确认以下问题：

- [ ] **架构原则**: "共享内核 + 双适配壳" 是否合理？
- [ ] **质量目标**: ≥ 95% (相对 FC) 是否合适？太高 / 太低？
- [ ] **工作量预算**: 1.5-2 周是否合理？
- [ ] **客户优先级**: Qwen3.6 / Qwen3.2 是否唯一目标，还是要照顾更多模型？
- [ ] **配置层**: `tool_mode` 字段命名 OK 吗？要不要叫 `function_calling_mode`？
- [ ] **Benchmark 集**: 50 case 是否够？需要客户提供真实业务场景吗？
- [ ] **灰度发布策略**: 4 阶段是否合适？
- [ ] **后续 v1.2.x 优先级**: auto 模式提到 v1.2.1 还是 v1.2.0 内置？

---

**评审通过后开始实施。任何反馈请直接评论或在 docs/design-prompt-tool-adapter-v1.2.0.md 里改。**
