# Prompt 加载顺序：memory / drift history / 当前问题在哪

日期：2026-05-01
触发：用户追问 "memory 和 drift 之前对话这两个内容加载到上下文中的顺序是怎样的"，进一步澄清"远端 system prompt"概念引发的误解。

## 一、完整 prompt 的线性顺序（最权威版本）

`internal/engine/context/builder.go:76-77` 入口返回 `SystemPrompt + Messages` 两部分，发送给 LLM 时是一段连续序列：

```
索引    位置                                  代码引用                                 归属
[1]     Universal base prompt                 builder.go:88, 366行 (strict/templated)  ┐
[2]     DB-specific rules                     builder.go:95-102                        │
[3]     Management awareness prompts          builder.go:104-116                       │ System Prompt
[4]     User policies / rules                 builder.go:119-137                       │ (6 块按
[5a]    PROFILE.md (实例画像)                  builder.go:140-154                       │  代码顺序)
[5b]    MEMORY.md (实例记忆索引)               builder.go:158-162                       │
[6]     Mode modifier                         builder.go:165-169                       ┘
[7]     history-1: 上次 user 问题             builder.go:181-183 (HistoryMessages)     ┐
[8]     history-2: 上次 assistant 回答                                                 │
...                                                                                    │ Messages
[N-2]   Environment context (当前时间等)       builder.go:185-187 (IsMeta=true)         │
[N-1]   Current user question                 builder.go:189-191                       ┘
```

每一块在 prompt 字节流里只出现一次、按上面顺序拼接。这是线性序列。

## 二、memory 和 drift 历史的关系

| 项 | 在 prompt 里的位置 |
|---|---|
| **memory（PROFILE.md + MEMORY.md）** | system prompt 第 5 块（靠后但仍在 system prompt 内） |
| **drift 保留的 history** | messages 头部（紧跟 system prompt 之后） |
| **当前用户问题** | messages 末尾（最后一条） |

加载顺序上：memory 永远先于 drift history。
LLM 注意力上："越靠近末尾权重越高"，所以当前问题 > history > memory > 基础规则。

## 三、修正：之前用"远端 system prompt"是含糊错误的说法

上一轮回答里画了这样的图（错误）：

```
当前问题（最强）
   ↑
环境信息
   ↑
drift 保留的 history
   ↑
... 远端 system prompt ...   ← 这里有问题
   ↑
MEMORY.md 索引
PROFILE.md
基础规则（最远）
```

错误：
1. 把"远端 system prompt"作为额外的独立区域放在 MEMORY.md 上面 — 实际上 MEMORY.md 本身就在 system prompt 内，不存在两个"system prompt"
2. MEMORY.md 在 system prompt 里属"靠后"（第 5b / 共 6 块），但图里画成接近"最远"，暗示注意力权重很低 — 不准确

## 四、修正后的正确图

按 prompt 物理位置（从 prompt 开始 0.0 → prompt 结束 1.0）：

```
  0.0 ─┬─ [1] Universal base prompt          ← 最远（注意力最弱）
       │
       │  [2] DB rules
       │
       │  [3] Management
       │
       │  [4] Policies
       │
       │  [5a] PROFILE.md
       │
       │  [5b] MEMORY.md                      ← 在 system prompt 内属"靠后"
       │
       │  [6] Mode modifier                    ← system prompt 末尾
       │
       │  [7..N-3] drift history              ← messages 里
       │
       │  [N-2] Env context
       │
  1.0 ─┴─ [N-1] Current user question        ← 最近、最强
```

## 五、对实际使用的影响

LLM 的"近因效应" / "lost-in-the-middle"：

- 当前问题 + 环境信息（prompt 最末尾）→ 注意力最高
- drift 保留的 history（messages 头部）→ 仅次于当前问题
- MEMORY.md（system prompt 第 5b）→ 比 history 远，但还在 system prompt 末尾段
- 基础规则（system prompt 第 1）→ 最远

实际效果：
- drift 触发清空 history 后，模型对"近期具体对话"完全没记忆
- MEMORY.md 仍在但权重不如近期 history — 模型能"想起 bench_og_hot 缺索引"，但难"想起刚才那条具体的 SQL"
- PROFILE.md 在 5a，比 MEMORY.md 还靠前一点，权重更弱

## 六、相关代码与文档

- `internal/engine/context/builder.go:76-194` — buildSystemPrompt + buildMessages 的完整实现
- `internal/engine/context/drift.go` — DropHistoryOnDrift 决定 history 是否注入
- `internal/engine/memory/store.go` + `profile.go` + `index.go` — memory 文件读写
- `docs/discussion-2026-05-01-drift-session-design-tradeoff.md` — drift × session 权衡（含本话题前置讨论）

## 七、教训

CLAUDE.md "架构与功能问答规范" 强调"基于扫描结果回答 + 引用具体文件路径/行号/函数名作为证据"。我用"远端 system prompt"这种含糊术语，没对应到具体 block 编号，导致用户合理地怀疑"是不是漏了一个东西"。

教训：

1. 提到 prompt 内部位置时，必须用 **block 编号 + 代码行号** 锚定，避免"远端/近端/前/后"这种相对词
2. 画图时，每个位置的标签必须与代码里的 SystemPromptBlock 索引一一对应
3. 注意力分布（近因效应）和物理位置（block 顺序）是两回事，不能混着画
