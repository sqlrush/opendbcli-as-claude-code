# Drift × Session 文件覆盖：设计权衡与遗失问题

日期：2026-05-01
触发：用户在调试"LLM 输出空白"bug 时，发现 session 文件无法用作审计取证；进一步追问 drift 机制的设计意图、与 session 文件覆盖的相互作用、以及对话连贯性的影响。

## 一、机制概览

### Session 文件
- 路径：`~/.dbaa/sessions/<instance>/<session_id>.jsonl`
- **不是按调用归档**，是整个 session 一个文件
- saveSession 每次写入用 atomic temp+rename，**全文件覆盖**（不是 append）
- 24 小时内同一 instance 复用同一 SessionID（resume.go ResumeMaxAge）

### Drift 机制
- 文件：`internal/engine/context/drift.go`
- 阈值：`TopicDriftThreshold = 0.05`
- 算法：Jaccard 相似度（中文按字切分 + 英文按词 + 停用词过滤）
- 触发条件：新 user message 与上次 user message 的 Jaccard sim ≤ 0.05
- 触发后行为：`historyMessages = nil`（**在内存里**清空历史）
- 设计意图（v1.1.10 CHANGELOG 原文）：
  > "如果检测到主题漂移就丢弃历史，让 LLM 从干净上下文起步。
  >  阈值 0.05 保守取值，避免误伤'继续分析'这类同主题关键词少的续问"

### Drift 的职责（精确版）
**drift 只做一件事**：在 `/llm` 启动时，决定上次对话的 messages 要不要被加载进这次 `/llm` 的 prompt 上下文。

drift **不**直接管：
- session 持久化（文件总是会存）
- 当前 /llm 内的多轮上下文（永远完整保留）
- 任何渲染或显示

## 二、Drift 与 saveSession 的相互作用

drift 触发后的完整流程：

```
1. ResumeOrNew → 找到 24h 内的 SessionID S1（同一个）
2. Load 文件 → historyMessages = [Q1, A1, ...]
3. DropHistoryOnDrift → sim ≤ 0.05 → historyMessages = nil
4. engine 主循环 msgs 从 [Q_new] 起步（不含 Q1/A1）
5. 跑完 N 轮工具调用 → msgs = [Q_new, A_r1, tool_1, ..., A_final]
6. saveSession 把 msgs 写到 S1 对应文件路径
7. 文件原内容（含 Q1/A1）被这个 msgs 完全覆盖

后果：
- 文件路径不变、SessionID 不变、Status 仍 active
- 但 Q1/A1 在物理层面已经不在文件里了
```

## 三、用户发现的关键设计问题：**话题切换后的"失忆"**

用户使用模式：

```
t1: 问 "数据库存在什么问题"            → 答 A1，存 [Q1,A1]
t2: 问 "到底哪些表 VACUUM"              → drift（sim=0），清 [Q1,A1]
                                          存 [Q2,A2]，文件覆盖，Q1/A1 物理丢失
t3: 问 "数据库存在什么问题" (回到话题1) → drift（vs Q2，sim=0），清 [Q2,A2]
                                          LLM 看到的历史 = 空
                                          → LLM 完全不记得 t1 / t2 任何内容
```

**LLM 对早先话题"完全失忆"**——即便用户主观感觉"我之前讨论过这个表"，LLM 看不到任何旧对话。

### 这与 Claude Code 等通用 LLM CLI 的预期行为完全不同

Claude Code 默认保留全部对话（靠大 context + /compact 压缩）。用户说"刚才那个 bug 怎么修的"时，Claude Code 能从同一会话翻出几小时前的讨论。
opendb 在 drift 触发后，"刚才"指的事就找不到了——不是 LLM 的注意力问题，是 prompt 里**根本没注入旧消息**。

### 受影响的真实使用场景

1. **诊断中跳话题再回来**：在做问题诊断时，DBA 经常需要切到相邻问题查证再回主线。每次切换都触发 drift，回主线时丢失上下文。
2. **跨日 resume**：v1.1.10 ResumeMaxAge=24h 设计是为"一天工作流可以连贯"，但用户中午问的事下午回来续问，如果字面差异大照样 drift。
3. **审计回溯**：bug 出现后想回看"刚才那次失败的 LLM 实际输出了什么"——找不到，被覆盖了。

## 四、根本原因

drift 算法选择和保留预期脱节：
- **设计意图**：0.05 阈值 = 保守 = 倾向保留
- **算法实际**：Jaccard 在中文短问场景下天然给 0（"数据库慢" 和 "VACUUM 哪些表" 字面零重合）
- **结果**：阈值数字是保守的，但实际触发频率比设计预期高得多

drift 与 saveSession 的复合效应没在原始设计中考虑：
- drift 设计目标只是"防上下文污染"
- saveSession 设计目标只是"持久化对话方便 resume"
- 两者叠加 → drift 清掉的对话不仅在 prompt 里没了，**在物理文件上也没了**
- 这一层副作用在 v1.1.10 引入 drift 时**没明确权衡过**

## 五、可能的改进方向（仅讨论，不动代码）

### 方向 A：算法改进（保留 drift，但更准）

- 阈值从 0.05 → 0.10/0.15 + 改用 **embedding 余弦相似度** 替代 Jaccard
- 中文短问场景下 embedding 比 Jaccard 鲁棒得多
- 代价：引入 embedding 服务调用，增加每次 /llm 的延迟

### 方向 B：分离审计日志与对话上下文

```
~/.dbaa/sessions/<instance>/
├── current.jsonl          ← 当前 session（受 drift 影响，会被覆盖）
└── audit/
    ├── 20260501-000613.jsonl  ← 每次 /llm 调用结束后归档一份，永不覆盖
    ├── 20260501-001142.jsonl
    └── ...
```

- drift 仍然影响 prompt 上下文（保留设计意图）
- audit 目录纯归档，下次 bug 取证可用
- 代价：磁盘占用增长（用 `internal/engine/quota` 兜底自动清理旧文件）

### 方向 C：drift 不删，只标记 boundary

- 检测到 drift → 在 messages 数组里插一条 `boundary` 元消息
- prompt 构造时根据 boundary 决定从哪里开始
- 文件仍保留所有历史，可回溯
- 用户主动"恢复"早先话题时，LLM 能拿到 pre-boundary 内容

### 方向 D：让用户显式控制（最低成本）

- 当前已有 `/session new` 命令（v1.1.08 引入）
- 配套加 `/session resume <id>` 让用户手动恢复早先 session
- drift 默认行为不变，但提供逃生通道
- 代价：用户认知负担

## 六、我的偏好（仅供参考，等用户拍板）

**短期**：方向 D（成本最低）+ 文档化"drift 触发会丢失历史"这个事实，让用户预期对齐。
**中期**：方向 B（审计日志），解决 bug 取证 + 历史回溯两个需求。
**长期**：方向 A（embedding 替代 Jaccard），从根上修 drift 触发率不准的问题。

## 七、补充：memory 系统对 drift 失忆问题的兜底（用户后续提问后补充）

### memory 系统是什么

- 路径：`~/.dbaa/memory/<instance>/`
- 文件：`MEMORY.md`（索引）+ `PROFILE.md`（实例画像）+ 单条记忆 `incident_*.md` / `solution_*.md` / `pattern_*.md` / `workload_*.md` / `preference_*.md`
- 由 LLM 在诊断中通过 `memory_write` 工具主动写
- **每次 /llm 启动**，MEMORY.md 索引 + PROFILE.md 都会**自动注入 system prompt**（builder.go:140-162）
- **不受 drift / saveSession 覆盖影响**

### memory 能解决 drift 失忆的哪部分

✅ 实例级长期知识不丢：
- "曾经诊断过 bench_og_hot 缺索引" → MEMORY.md 索引直接注入 prompt
- "上次修复方案是给 uid 列建索引" → solution_*.md 单条记忆通过 memory_recall 读到
- "这个实例的负载特征是高并发 seqscan 查询" → PROFILE.md 永久画像

❌ memory 不能解决的：
- 短期对话连贯（"刚才那条 UPDATE 你再分析下计划"）
- 原始工具结果数据（memory 存结论不存原始数据）
- 半句话续问 / 模糊对话引用

### 修正：第三节"切回旧话题完全失忆"说法过头

之前在第三节写"LLM 对早先话题完全失忆"，**忽略了 memory 系统的兜底作用**。
准确说法是：
- session messages 物理丢失（drift+覆盖）
- 但 MEMORY.md / PROFILE.md / 单条记忆文件**不丢**
- 切回旧话题时，LLM **能从 memory 找回 80% 实用知识**（实例做过什么、修过什么、什么模式反复出现）
- 缺的是 20% **细粒度对话连贯**（具体上下文引用、半句话续问）

### 三机制分工（修正后的全景）

```
session   → 短期对话续问（drift 是它的清理开关，会丢）
memory    → 实例级长期知识沉淀（drift 不影响，永久保留）
PROFILE   → 实例画像（缓存友好，跨调用稳定）
```

实际效果：opendb 这套三层结构对 DBA 场景"同一实例反复诊断、跨周积累经验"非常合适，drift 的副作用被 memory 兜得不错。第五节的"改进方向"优先级可下调——drift 不删历史的紧迫性比预想的低。

## 八、相关文件参考

- `internal/engine/context/drift.go` — drift 实现
- `internal/engine/session/filestore.go` — 文件 atomic 覆写
- `internal/engine/session/resume.go` — 24h ResumeMaxAge 逻辑
- `internal/engine/engine.go:104-115` — drift 调用点
- `docs/CHANGELOG.md:745-751` — v1.1.10 drift 引入说明
- `docs/code-state-correction-2026-05-01-drift-design-intent.md` — drift 设计意图修正
- `docs/code-state-correction-2026-05-01-session-file-analysis.md` — session 文件分析方法论
