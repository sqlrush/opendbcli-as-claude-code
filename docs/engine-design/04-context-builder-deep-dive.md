# 04-附 — 5 层上下文注入详解：当前实现 vs 新 Engine

## 一、当前 OpenDB 实际做了什么

当前诊断流程中，模型收到的信息只有两块：

```
当前 OpenDB 发给 LLM 的全部内容:
┌──────────────────────────────────────────────────┐
│ messages[0] = {                                   │
│   role: "system",                                 │
│   content: SystemPromptForDiagnose(mode)          │
│       → ~1200字: 角色 + 12条规则 + 工具名列表      │
│       → 无推理策略、无工具使用场景、无环境信息       │
│ }                                                 │
│                                                   │
│ messages[1] = {                                   │
│   role: "user",                                   │
│   content: DiagnoseUserPrompt(input, compressed)  │
│       → "用户诊断请求: xxx\n当前异常报告:\nxxx"     │
│       → 用户输入和压缩报告简单拼接                  │
│ }                                                 │
└──────────────────────────────────────────────────┘
  就这两条消息，没有了。
```

模型不知道：
- 连的是什么数据库（Oracle 19c? 11g? MySQL 8.0?）
- 实例名是什么
- 当前时间是几点
- 每个工具具体做什么、什么场景该用
- 遇到不同类型的异常该从哪里入手
- 等待事件是什么含义
- 诊断该怎么推理（先观察再假设再验证）

---

## 二、新 Engine 的 5 层上下文注入

```
Layer 1  系统提示      "教模型怎么做" — 行为规范+推理策略+工具策略+格式+安全
Layer 2  环境上下文    "告诉模型现在面对什么" — DB类型/版本/实例/时间/模式
Layer 3  诊断上下文    "给模型看数据" — 用户问题+压缩报告+异常指标
Layer 4  工具描述      "告诉模型能做什么" — 每个工具的名称+描述+使用场景+参数
Layer 5  轮次上下文    "每轮动态引导" — 收敛提示+预算提示+中间发现注入
```

---

## 三、逐层详解

### Layer 1：系统提示（从 1200 字 → 4700 字）

```
当前 ~1200字:                         新版 ~4700字:
─────────────                         ──────────────
角色描述(1句)                          角色描述(更精确)
12条规则(混杂)                         ┌─ 核心原则(4条，最重要的)
工具名列表(无描述)                      ├─ 推理策略
模式说明(3行)                          │   ├─ 6步诊断推理流程
                                      │   └─ 5个常见推理错误避免
                                      ├─ 工具使用策略
                                      │   ├─ 7种异常类型→首选工具映射表
                                      │   ├─ 12种深度分析路径(发现X→下一步Y)
                                      │   └─ 4条工具组合原则
                                      ├─ 输出格式(中间轮+最终诊断)
                                      ├─ 安全约束(分级)
                                      ├─ Oracle特定知识
                                      │   ├─ 对象引用规则(6条，强化)
                                      │   ├─ 16个等待事件含义+根因速查表
                                      │   ├─ 关键视图参考(10个)
                                      │   ├─ 参数修改注意事项(5条)
                                      │   └─ 6个高频ORA错误快速检查
                                      └─ 模式修饰(playbook/assist/auto)
```

#### 关键新增 — 推理策略（当前完全没有）

```
当前: 模型自己摸索怎么诊断，经常出现：
  - 一上来就下结论（没有收集证据）
  - 查了一堆工具但不分析（信息过多干扰判断）
  - 反复查同一个工具（浪费轮次）
  - "建议检查XX"而不是"根因是XX"（不给结论）

新版: 明确教模型6步推理流程：
  1. 观察现象 → 2. 建立假设(不超过2-3个)
  → 3. 收集证据(每轮1-2工具) → 4. 排除或确认
  → 5. 深入验证 → 6. 给出结论
```

#### 关键新增 — 工具入口选择（当前完全没有）

```
当前: 模型看到 27 个工具名，不知道从哪开始
  经常出现：性能问题先查 health（太泛），空间问题先查 waits（方向错）

新版: 异常类型→首选工具的直接映射
  性能下降 → activesessions + waits
  SQL慢查  → topsql 或 slowsql
  锁等待   → locks 或 blocktree
  空间告警 → space
  CPU/IO高 → os + waits
```

#### 关键新增 — 深度分析路径（当前完全没有）

```
当前: 模型查完第一个工具后不知道下一步该查什么
  经常出现：查了 waits 发现 IO 高，然后查 health（无关），而不是查 topsql→explain

新版: 12条"发现X→下一步Y"路径：
  全表扫描   → explain → tableinfo → 给建索引建议
  等待事件高 → ash → 找SQL → explain
  行锁争用   → blocktree → 找阻塞源SID
  I/O高      → os → topsql → explain
```

完整提示词内容见 [10-system-prompts.md](10-system-prompts.md)

---

### Layer 2：环境上下文（当前完全没有）

```
当前:
  模型不知道连的是什么数据库
  系统提示里写死"Oracle"，但如果用的是 MySQL 的 loop.go 就是"MySQL"
  不知道版本（Oracle 19c 和 11g 的诊断方式不同）
  不知道实例名（无法在建议中引用）
  不知道当前时间（无法判断是否在业务高峰）

新 Engine:
  在消息列表最前面插入一条隐藏消息（IsMeta=true，用户看不到）：

  <system-reminder>
  # 当前环境
  数据库: Oracle 19c
  实例: orcl
  地址: 192.168.1.100:1521
  当前时间: 2026-04-04 10:30:00
  诊断模式: auto
  最大轮次: 20
  </system-reminder>

  效果:
  - 模型知道是 19c，可以用 19c 特有的视图和功能
  - 模型知道实例名，建议里可以写 "在 orcl 实例上执行..."
  - 模型知道当前时间，可以判断"这个问题出现在工作时间还是批处理窗口"
  - 模型知道剩余轮次，可以自主规划诊断深度
```

#### IsMeta 机制（借鉴 Claude Code）

```
Claude Code 做法:
  prependUserContext() 插入 {isMeta: true} 的消息
  → 用户在 REPL 中看不到这条消息
  → 模型看得到，用于决策

OpenDB 新 Engine 同理:
  Message{IsMeta: true} → 渲染层跳过不展示
  → 模型看到完整的环境信息
  → 用户不需要每次看到"你连的是 Oracle 19c..."
```

---

### Layer 3：诊断上下文（当前有但简陋）

```
当前:
  DiagnoseUserPrompt(userInput, compressedReport)
  → "用户诊断请求: xxx\n当前异常报告:\nxxx\n请分析并给出诊断建议。"
  就是简单字符串拼接

新 Engine:
  诊断上下文可以更丰富：

  用户消息 + 压缩报告
  ├─ CompressReport 输出（已有，~2000 token，保留）
  ├─ 实时指标快照（新增，可选）
  │   → 当前活跃会话数、Top 等待事件、CPU使用率
  │   → 让模型在第一轮就有基本的状态感知
  └─ 历史对比提示（新增，可选）
      → 如果 Sentinel 有基线数据，附上"基线活跃会话=10，当前=87"
      → 帮助模型判断异常程度
```

这一层当前已经做得不错（CompressReport 是 OpenDB 的独有优势），新 Engine 主要是让它可以扩展更多数据源。

---

### Layer 4：动态工具描述（从静态文本 → 结构化 JSON Schema）

```
当前:
  工具描述写死在系统提示里：
  "可用的 skill (查询类):
    activesessions, sessions, waits, locks, blocktree..."
  → 就是工具名列表，没有说每个工具做什么、参数怎么传

  在 AgentLoop 中传给 API 的 tools:
  [{"type":"function","function":{"name":"waits","description":"等待事件统计","parameters":{...}}}]
  → 有 description 但很简短，没有使用场景提示

新 Engine:
  工具描述动态生成，每个工具包含：
  1. 名称 + 基础描述（来自 skill.ToolDef()）
  2. 使用场景提示（来自 PromptProfile.ToolUsageHint()）
  3. 参数 JSON Schema（来自 skill.ParamsSchema()）

  例如 waits 工具的完整描述：
  {
    "name": "waits",
    "description": "查看非 idle 等待事件排名。\n使用场景: 诊断入口——查看等待事件分布，定位性能瓶颈方向",
    "input_schema": {"type": "object", "properties": {...}}
  }

  而且工具列表从系统提示中移除，改为通过 API 的 tools 参数传递：
  → 系统提示更短更聚焦
  → 工具描述可以根据模式动态过滤（assist 只看查询工具）
  → 支持 Anthropic prompt cache（tools 参数独立缓存）
```

---

### Layer 5：轮次上下文（当前只有收敛引导，新增更多）

```
当前:
  仅在 maxTurns-2 时注入一条收敛提示：
  "你已使用 X/Y 轮。请在本轮直接给出最终诊断总结..."

新 Engine:
  每轮开始前可注入多种动态信息：

  1. 收敛引导（保留现有，已证明有效）
     turn >= maxTurns-2 → "请给出最终诊断"

  2. Token 预算提示（新增）
     当上下文使用 >70% →
     <system-reminder>上下文空间有限，请在接下来 1-2 轮内收敛。</system-reminder>

  3. 中间发现注入（新增，可选）
     如果工具结果中检测到关键模式（如 ORA- 错误、FTS、阻塞链）→
     <system-reminder>注意：上一轮工具结果中发现 ORA-01555，这通常指向 undo 问题。</system-reminder>
     → 帮助弱模型（9B）注意到关键信号

  所有轮次注入都用 IsMeta=true，用户看不到。
```

---

## 四、5 层上下文的发送时序

```
首轮发送给 API 的完整消息结构:

{
  "system": [                              ← Layer 1: 系统提示
    {"text": "通用基座(3000字)", "cache_control": {"type":"ephemeral"}},
    {"text": "Oracle特定(1500字)", "cache_control": {"type":"ephemeral"}},
    {"text": "模式: auto..."}
  ],

  "messages": [
    {                                      ← Layer 2: 环境上下文
      "role": "user",
      "content": "<system-reminder>数据库:Oracle 19c, 实例:orcl...</system-reminder>"
      // IsMeta=true, 用户不可见
    },
    {                                      ← Layer 3: 诊断上下文
      "role": "user",
      "content": "用户请求: 数据库响应变慢\n\n异常报告:\n触发:db%=85.3..."
    }
  ],

  "tools": [                               ← Layer 4: 动态工具描述
    {"name":"activesessions","description":"活跃会话列表\n使用场景:诊断入口..."},
    {"name":"waits","description":"等待事件统计\n使用场景:诊断入口..."},
    ...
  ],

  "thinking": {"type":"adaptive"},          ← 厂商专属参数
  "effort": "high"
}

后续轮次追加:                               ← Layer 5: 轮次上下文
  - assistant 消息（含 tool_calls）
  - tool 结果消息（含动态截断后的内容）
  - 收敛引导 / 预算提示（IsMeta=true）
```

---

## 五、对比 Claude Code 的 8 层上下文

```
Claude Code 8层:                   OpenDB 新 Engine 5层:
──────────────                     ─────────────────────
1. 系统提示(30KB)          →       1. 系统提示(4.7KB)        ✅ 对标
2. Git 状态               →       2. 环境上下文(DB信息)      ✅ 对标(DB版本替代Git)
3. CLAUDE.md(四级)         →       (不需要: OpenDB 无项目规则文件体系)
4. @文件附件              →       (不需要: DB诊断无文件引用)
5. 30+自动附件            →       3. 诊断上下文(报告+指标)   ✅ 对标(DB数据替代IDE状态)
6. 嵌套记忆               →       (不需要: DB诊断无子包规则)
7. Hooks                  →       (未来可考虑: 用户自定义注入)
8. <system-reminder>      →       4-5. 工具描述+轮次注入     ✅ 对标

保留精华，去掉 OpenDB 不需要的层。
```

---

## 六、量化对比

| 维度 | 当前 OpenDB | 新 Engine | Claude Code |
|------|------------|-----------|-------------|
| **上下文层数** | 2 | 5 | 8 |
| **系统提示** | ~1200字 | ~4700字 | ~30KB |
| **环境信息** | 无 | DB类型/版本/实例/时间 | Git/OS/Shell/Model |
| **推理策略** | 无 | 6步流程+5个避免 | 15+条任务执行规则 |
| **工具策略** | 工具名列表 | 7入口+12路径+4原则 | "用Read不用cat"级别 |
| **动态注入** | 仅收敛提示(1条) | 收敛+预算+发现(3类) | 30+种自动附件 |
| **隐藏消息** | 无 | IsMeta=true | isMeta=true |
| **工具描述** | 静态写在prompt | 动态生成+场景提示 | tool.prompt()动态 |
