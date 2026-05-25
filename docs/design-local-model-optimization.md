# 本地小模型优化方案 — 不对称帮助框架

**日期**：2026-04-30
**触发场景**：v1.1.22 修完 session 污染 + blocktree OOM 后，对比 DeepSeek-V4-Pro
和 Qwen3.5-27B-Opus-Local 在同一故障场景下的诊断输出，发现 Qwen 给出的报告
"指导性强但缺具体 PID/sql_id"，DeepSeek 则给出可直接执行的具体值。

## 问题定义

### 现象

同一 OG anti-pattern F3 故障（30 并发 `UPDATE bench_app_counter WHERE id=1`），
两模型都被同一份 `system_prompt`（capability=large → strict 变体）驱动：

| 模型 | 调用工具 | 轮数 | 结论中的 PID 体现 |
|---|---|---:|---|
| DeepSeek-V4-Pro | 30+ 个 | 11+ | `pg_terminate_backend(140477135124224)` 具体值 |
| Qwen3.5-27B-Opus | 5 个 | 2 | "请用 `select pid where ...` 自己查"占位符 |

### 根因（单一定论）

**与 prompt 无关 / 与协议适配无关**，纯粹是模型能力差距：

1. **参数量差距 ~20×**（DeepSeek 600B+ MoE vs Qwen 27B）
2. **思考模式缺失**（DeepSeek 内置 thinking，Qwen-Opus 没有）
3. **注意力预算不够**：18000 字符 system prompt + 多轮工具结果，27B 模型在
   长 context 下细节丢失率高
4. **生成偏好**：语言模型默认走"低成本占位符"路线，复述 12 位长串数字成本高
5. **轮数偏好**：训练数据里 27B 模型倾向"快速结论"，11 轮深挖是大模型行为

## 设计原则

> **单 prompt + 不对称帮助**

让"工具层、管道层、后处理层"为小模型多做事，**这些多做的事对大模型是 no-op
或冗余但无害**。系统 prompt 始终只有一份。

类比：自行车和 F1 走同一条路。不能给自行车单独修一条路，**但可以在路边加路标**
（自行车依赖路标，F1 视而不见）。

### 必须避免的反模式

| 反模式 | 为什么不行 |
|---|---|
| ❌ 给每个模型写一套独立 system prompt | 维护爆炸 + 行为漂移；本会话已经吃过 strict / templated 双变体的同步亏（sqltune 路由规则只改了 strict 漏 templated） |
| ❌ 在 prompt 里加 `if model_size < 30B` | system prompt 不能引用模型自身属性，模型不知道自己多大 |
| ❌ 降低小模型输出标准 | 用户不知道自己在用小模型 → 体验不一致 |
| ❌ 后处理时把大模型也强制 refine | 大模型本来就对，refine 可能把对的改错 |
| ❌ 在主 prompt 里加"如果你是小模型，请..." | 反递归，模型读不懂 |

业界主流（Anthropic / OpenAI / Cursor）都是 **prompt 是协议，应稳定；模型才该追赶 prompt**。

## 三层不对称帮助

### Tier 1: 工具输出层加 `[summary]` banner ⭐⭐⭐ 最划算

工具结果末尾加结构化摘要行，**放显眼位置（最后几行）**。小模型容易复读
"key: 具体值"格式（语言模型对"复读已存在字符串"几乎无成本），大模型自己
也提取过这些值，多几行无副作用。

**示例 — `activesessions` 工具改造后输出：**

```
PID         User    State   Wait              Elapsed   Query
14047...    opendb  active  Lock:lock_wait    83.2s     UPDATE bench_app_counter ...
14047...    opendb  active  Lock:lock_wait    81.5s     UPDATE bench_app_counter ...
... (28 行)

[summary]
top_blocker_pid: 140477135124224 (oldest waiter 83s)
total_waiting_pids: 28
all_blocked_on_sql: UPDATE bench_app_counter SET counter = counter + 1 WHERE id = 1
hottest_table: public.bench_app_counter
```

**改造范围**（4 个高频工具覆盖 ~80% 诊断场景）：

| 工具 | 加的 summary 字段 |
|---|---|
| `activesessions` | top_blocker_pid + waiting_count + dominant_sql + hottest_table |
| `topsql` / `slowsql` | hottest_sql_id + n_calls + avg_ms + sql_text_head |
| `blocktree` | root_blocker_pid + chain_depth + victim_count |
| `health` | critical_metric + value + threshold |

**影响**：
- 大模型：0（信息冗余）
- 小模型：具体 PID/sql_id 出现率 30% → 90%（预估）

**实现位置**：在每个 skill 的 `formatXxxPanel()` 函数末尾追加 banner 段落
（参考 `blocktree.go::renderOGBlockTree` 已经有"提示"行的模式）。

### Tier 2: 管道层"最小轮数门槛 + 占位符检查" ⭐⭐ 次划算

小模型有"草草收尾"的强偏好。在 `engine.go` 主循环中加软约束：

```go
// 在判断"无 tool_calls = final round"前增加一个 check
if turn < e.config.MinDiagnoseRounds && hasUnverifiedPlaceholder(resp.Content) {
    // 注入一条 user 消息: "请继续验证 PID/sql_id 等具体数值"
    messages = append(messages, econtext.Message{
        Role: "user", IsMeta: true,
        Content: "<system-reminder>结论中检测到占位符（如 <PID>、'select pid where'）。请继续调用工具拿到具体数值后再给最终结论。</system-reminder>",
    })
    continue
}
```

`hasUnverifiedPlaceholder` 用正则识别"懒"句式：
- `<PID>` / `<pid>` / `<sql_id>` / `<具体值>`
- `select pid where` / `select.*from pg_stat_activity where`
- `自己查` / `请运行` / `如果发现` / `根据返回值`

**配置项**（在 `engine/config.go`）：

```go
MinDiagnoseRounds int  // 默认 0（关闭），可调到 3-5 启用
```

**影响**：
- 大模型：DeepSeek 通常 8-11 轮自然完成，到 final 轮时 content 很少含占位符 → 判定为 false → no-op
- 小模型：Qwen 2 轮收尾被拒回 → 强制深挖 → 多调 topsql/sql 工具 → 拿到具体值

### Tier 3: Refiner 后处理 ⭐ 兜底

主诊断完成后跑一个**极轻量 refine 轮**（~500 tokens）：

```text
[refine prompt]
以下是你刚才的诊断报告：
{report}

以下是工具调用过的全部数据：
{tool_results}

任务：检查报告中是否有 PID/sql_id/表名 的占位符或泛指（如 "<PID>"、
"对应进程"、"该 SQL"），从工具数据中找出具体值替换。
只输出修改后的报告，不解释。
```

**关键设计**：
- Refine 用**同一个本地小模型**跑（不调云端 API，成本几乎为 0）
- Refine 任务比诊断简单 10 倍，27B 模型完全胜任
- Refine 总会跑，**对大模型也跑**：DeepSeek 报告原本含具体值，refine 找不到东西改 → 输出原文 → 几乎 no-op

**实现位置**：`engine.go` 主循环结束后，`saveSession` 之前。新增方法
`refineDeliverable(ctx, finalReport, toolResults)`。

## 实施顺序与决策矩阵

### 决策矩阵

| 场景 | Tier 1 | Tier 2 | Tier 3 |
|---|---|---|---|
| 工具结果里有具体值，模型懒不用 | ✅ 直接修 | — | 兜底 |
| 模型轮数不够、没看完证据 | — | ✅ | — |
| 模型完全没意识到要给具体值 | — | — | ✅ |

### 推荐顺序

```
v1.1.23 (1-2 天):
  Tier 1 全做完 (4 个工具加 summary banner)
  跑 5 模型 benchmark 测"具体 ID 出现率"提升

v1.1.24 (按效果定):
  如果 Tier 1 已让小模型够用 → 不做 Tier 2/3
  如果还差，加 Tier 2 (engine 加 MinDiagnoseRounds + 占位符检查)

v1.1.25+ (兜底):
  如果 Tier 1+2 还有 case 漏掉 → 加 Tier 3 Refiner
```

**最小可行方案**：只做 Tier 1。一周观察看是否够用，再决定要不要 Tier 2 / 3。

## 量化基线（修复前数据，做对比用）

同一 OG anti-pattern F3 故障，5 模型同 prompt 测试结果（v1.1.22 实测）：

| 模型 | 轮数 | 工具数 | 报告 size | 含具体 PID | 含具体 sql_id |
|---|---:|---:|---:|---:|---:|
| deepseek-v4-pro | 11+ | 30+ | 5337B | ✅ | ✅ |
| glm-5.1 | 4-6 | 15+ | 6343B | ✅ | 部分 |
| kimi-k2.6 | 5-8 | 20+ | 4819B | ✅ | ✅ |
| moonshot-v1-128k | 1-2 | 3-5 | 615B | ❌ | ❌ |
| qwen-opus-local | 2 | 5 | ~3000B | ❌ | ❌ |

Tier 1 实施后预期（按 banner 复读率 90% 估算）：

| 模型 | 含具体 PID（预期）| 含具体 sql_id（预期）|
|---|---|---|
| deepseek-v4-pro | ✅（不变）| ✅（不变）|
| glm-5.1 | ✅ | ✅（提升）|
| kimi-k2.6 | ✅（不变）| ✅（不变）|
| moonshot-v1-128k | ✅（提升）| ✅（提升）|
| qwen-opus-local | ✅（提升）| ✅（提升）|

## 与已有架构的关系

### 不破坏的现有设计

- ✅ 当前 `universalSystemPrompt(capability)` 双变体（strict / templated）保持不变
- ✅ 协议层（adapter）和业务 prompt 层的分离保持不变
- ✅ Capability 分层（按"模型能扛多复杂指令"分两档）是对的设计

### 触及的现有文件

```
Tier 1:
  internal/opengauss/skill/monitor/activesessions.go  (formatOGActiveSessionsPanel 末尾加 summary)
  internal/opengauss/skill/monitor/blocktree.go       (renderOGBlockTree 末尾扩展 summary)
  internal/opengauss/skill/monitor/health.go          (类似)
  internal/opengauss/skill/query/topsql.go            (类似)
  internal/opengauss/skill/query/slowsql.go           (类似)
  + 同步 oracle/postgres/mysql 4 库（一致性原则）

Tier 2:
  internal/engine/config.go     (新增 MinDiagnoseRounds)
  internal/engine/engine.go     (主循环加 placeholder check + 注入 user 消息)
  internal/engine/placeholder.go (新文件，hasUnverifiedPlaceholder)

Tier 3:
  internal/engine/engine.go     (主循环结束后调 refineDeliverable)
  internal/engine/refiner.go    (新文件，refineDeliverable 实现)
```

### 单元测试要求

每层都要有：
- 已有具体值的报告 → 不修改（保护大模型）
- 占位符报告 → 替换成具体值（小模型受益）
- 工具数据缺失时 → 优雅降级，不引入幻觉

## 风险与权衡

| 风险 | 概率 | 缓解 |
|---|---|---|
| Banner 占用 input token，长期累计成本 | 低 | 4 工具 × 5 行 banner ≈ +500 tokens/round，对 200K context 几乎 0 |
| Refiner 把对的改错 | 中 | Refiner prompt 严格限定"只替换占位符"，加 diff 检测，差异 >30% 拒绝 |
| MinDiagnoseRounds 让大模型多跑 | 低 | 大模型本来就跑很多轮，门槛 3-5 不会触发 |
| Banner 字段命名漂移 | 中 | 在 `internal/opengauss/skill/monitor/banner.go` 集中定义常量 + 单元测试 |

## 不做的事（以及为什么）

- ❌ 给 Qwen 写专属 prompt — 维护爆炸 + 协议漂移
- ❌ 在 system prompt 里检测模型大小 — 反递归
- ❌ 引入"小模型 fallback 路径" — 路径分裂会发散
- ❌ 上更大本地模型（72B）— 这是基础设施事，不在 prompt 框架优化范围内
- ❌ 用云端模型做 refine — 成本累计 + 引入云端依赖

## 参考

- 本会话上下游：v1.1.22 修复 session 污染 + blocktree OOM
- 模型能力差距实测：远端 Linux 4 模型回归（每个模型 RSS 35 MB / exit=0）
- 业界 prompt 设计共识：Anthropic / OpenAI / Cursor 均采用单一 prompt 框架
