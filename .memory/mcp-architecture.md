# OpenDB MCP Architecture 探索结果

## 目标
开发 opendb 成为 Qwen 3.5:9B 的 MCP 工具，实现端到端数据库问题解决。

## 现有架构 MCP 就绪度

### Skill 系统天然适配 MCP
每个 skill 已具备：
- `Name()` / `Description()` → MCP tool 标识
- `ToolDef()` → JSON Schema 参数定义
- `Execute(ctx, params) → *Result` → 工具执行
- `SecurityLevel()` → 安全级别控制

### 核心接口
```go
// db.Driver — 数据库操作
Query(ctx, sql, args) → (*QueryResult, error)
Exec(ctx, sql, args) → (*ExecResult, error)
Ping(ctx) → error
ServerInfo() → ServerInfo

// skill.Skill — 工具定义
ToolDef() → ToolDef{Name, Description, Parameters(JSON schema)}
Execute(ctx, Params) → (*Result, error)

// skill.Result — 统一结果
Type: ResultTable | ResultText | ResultRefresh | ResultError
Data: any (QueryResult, string, etc.)
Summary: string
```

### 已有 20+ 技能可直接暴露为 MCP 工具
- **Admin**: help, login, logout, kill, space, params, alert, backup, standby, config, clear, history
- **Query**: slowsql, explain, sql
- **Monitor**: sessions, activesessions, waits, locks, latches, mutexes, health, dbtop
- **Schema**: tableinfo, indexadvise

---

## 核心设计决策：分层架构，降低 AI 依赖

### 问题
多轮 Agent Loop 完全依赖 Qwen 9B 的判断准确性，风险点：
- 选错工具（该查 waits 却去查 locks）
- 误读结果（忽略关键指标）
- 推理断链（看到全表扫描却建议 kill session）
- 兜圈子（反复调同一个工具）

### 解决方案：分层处理，AI 只做最后一步

| 层级 | 谁做 | 依赖 AI？ |
|------|------|-----------|
| **数据采集** | OpenDB 按 Playbook 自动执行 | 否 |
| **异常检测** | OpenDB 规则引擎（阈值） | 否 |
| **根因关联** | OpenDB 规则（if 等待事件 top1 是 I/O → 查慢 SQL） | 否 |
| **结论生成** | Qwen 把数据+异常翻译成人话 | 是，但容错高 |
| **方案建议** | Qwen 给出建议（加索引/调参数/kill） | 是，风险点 |
| **执行操作** | OpenDB 执行，**必须人工确认** | 否 |

### 三种降低 AI 依赖的机制

#### 1. 预设诊断剧本（Playbook）
不让 Qwen 自由选工具，OpenDB 定义确定性的诊断流程：
```
用户: "数据库慢"
OpenDB 自动执行（不需要 AI）:
  Step 1: health()          → 确定大方向
  Step 2: waits()           → 等待事件排名
  Step 3: activesessions()  → 活跃会话详情
  Step 4: slowsql()         → 慢 SQL 列表
全部结果打包 → 一次性发给 Qwen → 输出诊断报告
```
AI 角色从"决策者"降级为"分析师"。

#### 2. 规则引擎 + AI 兜底
```go
if snap.DBPercent > 80 {
    // 自动采集 CPU 相关数据
    results = append(results, execute("activesessions"), execute("waits"))
}
if snap.WTRPercent > 60 {
    // 自动采集 I/O 相关数据
    results = append(results, execute("waits"), execute("locks"))
}
```
health.go 里的阈值判断已有基础，可直接延伸。

#### 3. 结果压缩（context window 管理）
Qwen 3.5:9B context ~32K，需要压缩 tool 结果：
- 表格只保留 top N 行
- SQL 文本截断到前 200 字符
- 超过 maxTokens 时做摘要

---

## 三模式兼容设计（`--mode` 参数）

### 模式定义

| 模式 | AI 自主度 | 工具选择 | 最大轮次 | 适用场景 |
|------|-----------|----------|----------|----------|
| `playbook` | 最低（默认） | OpenDB 按预设流程，AI 只写报告 | 1（单次） | 9B 小模型、生产环境、要求可控 |
| `assist` | 中等 | AI 从受限工具集中选择，OpenDB 兜底 | 3 | 中等模型、需要一定灵活性 |
| `auto` | 最高 | AI 完全自主决策，OpenDB 仅做安全拦截 | 10 | 大模型（70B+）、测试环境、高级用户 |

### 使用方式

```bash
# 命令行
/diagnose                        # 默认 playbook 模式
/diagnose --mode=assist          # 中等自主
/diagnose --mode=auto            # 完全自主
/diagnose --mode=assist --rounds=5  # 覆盖轮次

# 自然语言输入（已连 LLM 时）
数据库很慢                        # 用 config 里的默认模式
```

### 配置文件

```yaml
# ~/.opendb/config.yaml
llm:
  provider: ollama               # ollama | openai | none
  model: qwen3.5:9b
  base_url: http://localhost:11434
  diagnose_mode: playbook        # playbook | assist | auto
  max_rounds: 3                  # assist/auto 模式最大轮次
  max_result_tokens: 2000        # 单次 tool 结果最大 token 数
```

### 模式一：playbook（默认）

```
用户: "数据库慢" 或 /diagnose
        │
        ▼
┌─ OpenDB Playbook 引擎（确定性，不依赖 AI）────────┐
│                                                   │
│  Step 1: health()          → 整体健康状态          │
│  Step 2: waits()           → 等待事件 Top 10       │
│  Step 3: activesessions()  → 活跃会话列表          │
│  Step 4: slowsql()         → 慢 SQL Top 10         │
│  Step 5: 规则引擎           → 标注异常项 + 关联分析  │
│                                                   │
│  输出: 结构化数据包 {                               │
│    health: {..., alerts: [...]},                   │
│    waits: [{event, pct, flag: "CRITICAL"}, ...],  │
│    sessions: [...],                                │
│    slowsql: [...],                                 │
│    rule_findings: [                                │
│      "db% 87% 超过阈值 80%",                       │
│      "等待事件 TOP1 为 I/O 类，关联慢 SQL abc123",   │
│    ]                                               │
│  }                                                 │
└──────────────────────┬────────────────────────────┘
                       │
                       ▼ 单次调用
┌─ Qwen（分析师角色）──────────────────────────────┐
│  System: "你是 Oracle DBA，根据以下诊断数据写报告" │
│  User: {上面的结构化数据包}                       │
│  → 输出: 诊断报告（结论 + 建议）                  │
└──────────────────────────────────────────────────┘
```

**Playbook 路由规则**（基于 health 结果自动选择采集路径）：
```go
type Playbook struct {
    Name     string
    Trigger  func(health *Snapshot) bool
    Steps    []string  // skill names to execute
}

var defaultPlaybooks = []Playbook{
    {
        Name:    "performance",
        Trigger: func(h) { return h.DBPercent > 50 || h.WTRPercent > 30 },
        Steps:   []string{"health", "waits", "activesessions", "slowsql"},
    },
    {
        Name:    "blocking",
        Trigger: func(h) { return h.ActiveCount > 30 },
        Steps:   []string{"health", "locks", "activesessions", "sessions"},
    },
    {
        Name:    "space",
        Trigger: func(h) { return hasSpaceAlert(h) },
        Steps:   []string{"health", "space", "alert"},
    },
    {
        Name:    "general",  // 兜底
        Trigger: func(h) { return true },
        Steps:   []string{"health", "waits", "activesessions", "slowsql", "locks"},
    },
}
```

### 模式二：assist（受限 Agent Loop）

```
用户: /diagnose --mode=assist
        │
        ▼
┌─ Round 0: OpenDB 先跑 health（强制）──────────┐
│  health 结果 + 规则引擎标注异常                 │
└──────────────────────┬────────────────────────┘
                       │
                       ▼
┌─ Round 1~N: Qwen 选择工具（受限）─────────────┐
│                                               │
│  可选工具集（只读，约 10 个）:                   │
│    waits, activesessions, slowsql, explain,   │
│    locks, latches, sessions, space, params,   │
│    alert                                      │
│                                               │
│  不可选（被过滤掉）:                            │
│    kill, sql(写), login, logout, config,      │
│    dbtop, clear, history, help                │
│                                               │
│  每轮: Qwen → tool_call → OpenDB 执行         │
│         → 结果压缩 → 追加到 messages            │
│                                               │
│  终止条件:                                     │
│    - Qwen 返回纯文本（给出结论）                 │
│    - 达到 max_rounds                           │
│    - Qwen 连续 2 次调同一个工具（防兜圈）        │
└───────────────────────────────────────────────┘
```

**防护机制**：
- 首轮 health 强制执行，不由 AI 决定
- 工具白名单，排除所有写操作和无关工具
- 最大轮次限制（默认 3）
- 重复调用检测（连续 2 次同工具 → 强制结束）
- 单次结果 token 压缩（防 context 爆炸）

### 模式三：auto（完全自主 Agent Loop）

```
用户: /diagnose --mode=auto
        │
        ▼
┌─ Agent Loop ─────────────────────────────────┐
│                                               │
│  全部工具可用（含 kill, sql）                    │
│  但写操作触发安全确认:                           │
│    Qwen: tool_call: kill(sid=123)             │
│    OpenDB: "⚠ 确认终止会话 SID=123？[y/N]"    │
│    用户: y                                    │
│    OpenDB: 执行 kill → 结果返回 Qwen           │
│                                               │
│  最大轮次: 10（可配置）                          │
│  安全拦截: SecurityLevel >= Operator 需确认     │
│                                               │
└───────────────────────────────────────────────┘
```

### 模式切换的代码架构

```go
// internal/agent/agent.go
type Agent struct {
    llm      llm.Client
    executor *skill.Executor
    registry *skill.Registry
    config   AgentConfig
}

type AgentConfig struct {
    Mode          string  // "playbook" | "assist" | "auto"
    MaxRounds     int
    MaxResultToks int
}

func (a *Agent) Diagnose(ctx context.Context, question string) (string, error) {
    switch a.config.Mode {
    case "playbook":
        return a.runPlaybook(ctx, question)
    case "assist":
        return a.runAssist(ctx, question)
    case "auto":
        return a.runAuto(ctx, question)
    default:
        return a.runPlaybook(ctx, question)
    }
}

// runPlaybook: 确定性采集 → 单次 AI
func (a *Agent) runPlaybook(ctx, question) (string, error) {
    snap := a.collectHealth(ctx)
    playbook := a.selectPlaybook(snap)
    results := a.executeSteps(ctx, playbook.Steps)
    findings := a.applyRules(snap, results)
    return a.llm.Chat(ctx, buildPlaybookPrompt(question, results, findings), nil)
}

// runAssist: 强制 health → 受限 agent loop
func (a *Agent) runAssist(ctx, question) (string, error) {
    healthResult := a.executor.Execute(ctx, "health", nil)
    messages := buildAssistPrompt(question, healthResult)
    tools := a.registry.ExportTools(readOnlyFilter)  // 只导出只读工具
    return a.agentLoop(ctx, messages, tools, a.config.MaxRounds)
}

// runAuto: 完整 agent loop，写操作需确认
func (a *Agent) runAuto(ctx, question) (string, error) {
    messages := buildAutoPrompt(question)
    tools := a.registry.ExportTools(nil)  // 全部工具
    return a.agentLoop(ctx, messages, tools, a.config.MaxRounds)
}

// agentLoop: 通用多轮循环（assist 和 auto 共用）
func (a *Agent) agentLoop(ctx, messages, tools, maxRounds) (string, error) {
    lastTool := ""
    repeatCount := 0

    for round := 0; round < maxRounds; round++ {
        resp := a.llm.Chat(ctx, messages, tools)

        if resp.ToolCalls == nil {
            return resp.Content, nil  // 最终结论
        }

        for _, call := range resp.ToolCalls {
            // 重复调用检测
            if call.Name == lastTool {
                repeatCount++
                if repeatCount >= 2 {
                    return a.forceConclusion(ctx, messages)
                }
            } else {
                lastTool = call.Name
                repeatCount = 0
            }

            // 执行工具
            result := a.executor.Execute(ctx, call.Name, call.Args)
            compressed := CompressResult(result, a.config.MaxResultToks)
            messages = append(messages, toolResultMsg(call.ID, compressed))
        }
    }
    return a.forceConclusion(ctx, messages)  // 超过轮次，强制总结
}
```

### System Prompt（分模式）

```go
var systemPrompts = map[string]string{
    "playbook": `你是 Oracle DBA 专家。根据以下自动采集的诊断数据，输出诊断报告。
要求：
1. 先总结数据库当前状态（一句话）
2. 列出发现的问题（按严重程度排序）
3. 每个问题给出具体建议（引用数据中的具体数值）
4. 如果数据显示正常，直接说"未发现异常"`,

    "assist": `你是 Oracle DBA 专家。用户描述了数据库问题，health 检查结果已提供。
你可以调用工具进一步诊断。
规则：
- 每次只调 1 个最相关的工具
- 如果已经有足够信息，直接给出结论，不要多余调用
- 结论必须引用具体数据`,

    "auto": `你是 Oracle DBA 专家。用户描述了数据库问题，你可以自由调用工具诊断和解决。
规则：
- 先 health 了解全局，再针对性深入
- kill session 等操作前说明原因
- 操作后再次检查确认效果
- 结论必须引用具体数据`,
}
```

### 交互示例

**playbook 模式**（9B 模型推荐）：
```
opendb> 数据库很慢

[Playbook: performance] 采集中...
  ✓ health      db% 87% ⚠  WTR% 62% ⚠
  ✓ waits       Top1: db file sequential read (45%)
  ✓ sessions    8 active (5 I/O wait)
  ✓ slowsql     Top1: SQL abc123 (12.3s)

正在分析...

━━━ 诊断报告 ━━━
数据库当前处于高负载状态。

问题 1（严重）: I/O 瓶颈
  - db% 87%，WTR% 62%，大部分时间消耗在 I/O 等待
  - 等待事件 TOP1: db file sequential read 占 45%
  - 8 个活跃会话中 5 个在等待 I/O

问题 2（根因）: 全表扫描
  - SQL abc123 执行 12.3 秒，对 ORDERS 表全表扫描
  - 建议: CREATE INDEX idx_orders_date ON orders(order_date);

问题 3（次要）: 活跃会话偏多
  - 8 个活跃会话，建议关注是否有会话积压
```

**assist 模式**：
```
opendb> /diagnose --mode=assist 数据库很慢

[health] db% 87% ⚠  WTR% 62% ⚠  AN=8

[Round 1/3] AI 选择: waits
  → Top1: db file sequential read (45%)

[Round 2/3] AI 选择: slowsql
  → Top1: SQL abc123 (12.3s, FULL TABLE SCAN)

[Round 3/3] AI 给出结论:
  SQL abc123 全表扫描导致 I/O 瓶颈...
```

**auto 模式**：
```
opendb> /diagnose --mode=auto 数据库慢，帮我处理

[Round 1] AI: health()         → db% 87%, AN=8
[Round 2] AI: waits()          → I/O 瓶颈
[Round 3] AI: slowsql()        → SQL abc123
[Round 4] AI: explain(abc123)  → FULL TABLE SCAN
[Round 5] AI: 结论 + 建议 CREATE INDEX ...
⚠ AI 建议执行: CREATE INDEX idx_orders_date ON orders(order_date);
  确认执行？[y/N] _
```

---

## 流式数据处理策略（dbtop → AI）

### 问题
dbtop 每秒产生一帧数据，如果持续采集 30 秒直接喂给 Qwen：
- 30 帧原始数据轻松超 10 万字符，context 爆掉
- LLM 不擅长从原始时序数值中发现趋势和突变
- 处理速度跟不上数据产生速度

### 解决方案：OpenDB 聚合 → Qwen 看摘要

```
dbtop 30 帧原始数据
    │
    ▼ OpenDB aggregator（确定性，不需要 AI）
    │
    ├─ 指标统计: avg/max/min/趋势(rising/falling/stable/spike/drop)
    ├─ 异常点检测: 相邻帧变化超 50% 的时间点
    ├─ 等待事件切换: 前半段 vs 后半段 TOP1 是否变化
    ├─ SQL 关联: 新出现的 SQL 是否与异常点时间吻合
    │
    ▼ 压缩到 ~2000 token 摘要
    │
    Qwen 单次调用 → 输出分析结论
```

### 聚合引擎核心结构

```go
// internal/agent/aggregator.go

type AggregatedReport struct {
    Duration   time.Duration
    Metrics    map[string]MetricSummary  // db%, WTR%, TPS, QPS, AN
    Anomalies  []Anomaly                // 突变点
    WaitShift  *WaitShift               // 等待事件是否切换
    NewSQLs    []SQLAppearance          // 新出现的 SQL
}

type MetricSummary struct {
    Avg, Max, Min float64
    Trend         string  // "rising"|"falling"|"stable"|"spike"|"drop"
}

type Anomaly struct {
    Timestamp time.Time
    Metric    string
    Before, After float64
    ChangePct float64
}
```

### 算法（不需要 AI）

- **趋势判断**: 前半段均值 vs 后半段均值，变化 >50% spike，>15% rising，<-50% drop，<-15% falling
- **异常点检测**: 相邻帧变化超 50% 标记为异常
- **等待事件切换**: 前半段和后半段分别统计 TOP1，不同则标记 shifted
- **SQL 关联**: 新 SQL 首次出现时间与异常点时间差 <2s 视为关联

### 喂给 Qwen 的摘要示例

```
采样窗口: 30 秒 (12:05:00 ~ 12:05:30)

指标趋势:
  db%: avg=72 max=91 min=45 趋势=spike(↑60%)
  TPS: avg=1200 max=1800 min=300 趋势=drop(↓83%)
  AN:  avg=12 max=23 min=5 趋势=spike(↑360%)

异常点:
  [12:05:12] db% 45→91 (+102%), AN 5→23 (+360%)
  [12:05:18] TPS 1800→300 (-83%)

等待事件切换:
  前半段 TOP1: DB CPU (62%) → 后半段 TOP1: db file sequential read (71%)

关联 SQL:
  SQL cde456 首次出现于 12:05:12，与 db% 飙升同步，持续 18s
```

### 使用方式

```bash
/diagnose --observe=30          # 采集 30 秒后自动分析
/diagnose --observe=60 --mode=assist  # 采集 60 秒，assist 模式
```

### 分层原则

| 层级 | 谁做 | 为什么 |
|------|------|--------|
| 数据采集（每秒） | OpenDB dbtop | 确定性，高频 |
| 聚合统计 | OpenDB aggregator | 数学运算，AI 不擅长 |
| 异常检测 | OpenDB 规则（阈值/突变） | 确定性，零误判 |
| 关联分析 | OpenDB（时间吻合检测） | 简单规则 |
| **解读+建议** | **Qwen（单次调用）** | **AI 唯一介入点** |

**30 帧原始数据 → 聚合成 2000 token 摘要 → 单次喂给 Qwen。流式数据问题完全消除。**

---

## 技术实现清单

### 需要新增的组件
1. **Ollama 客户端** (`internal/llm/ollama.go`) — 调 Qwen API
2. **Tool Schema 转换** — skill.ToolDef → OpenAI function 格式
3. **诊断 Playbook** (`internal/agent/playbook.go`) — 预设诊断流程
4. **规则引擎扩展** — 从 health.go 阈值延伸到自动关联
5. **结果压缩** (`internal/agent/compress.go`) — context window 管理
6. **System Prompt** — DBA 角色 + 行为规范

### 开发顺序
```
① Ollama Client        ← 独立，可先做
② Tool Schema 转换     ← 依赖 skill.Registry，简单
③ 诊断 Playbook        ← 核心，确定性逻辑
④ 结果压缩             ← 独立
⑤ /diagnose 命令       ← 依赖 ①②③④，串起来
⑥ System Prompt 调优   ← 依赖 ⑤，反复测试
```

### 需要新增的目录
```
internal/llm/               — LLM 客户端（Ollama/OpenAI 兼容）
internal/agent/              — 诊断 Playbook + 规则引擎 + 结果压缩
internal/server/             — HTTP API 服务器（后续）
internal/mcp/                — MCP 协议适配层（后续）
```

## 项目结构（已有）
```
cmd/opendb/main.go          — CLI 入口
internal/db/driver.go        — 数据库接口
internal/db/oracle/          — Oracle 实现
internal/connection/         — 连接管理
internal/skill/              — 技能系统（核心）
internal/dispatch/           — 输入路由
internal/security/           — 安全控制
internal/format/             — 输出格式化
internal/monitor/dbtop/      — 实时监控
internal/ui/                 — TUI/REPL
internal/config/             — 配置系统
internal/errors/             — 错误类型
internal/session/            — 会话历史
```
