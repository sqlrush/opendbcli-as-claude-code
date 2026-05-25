# /llm → /sqltune 路由失败修复方案

> 状态：待评审
> 范围：opendb (main) + 同步 dbaa / linkdb
> 关联：`internal/opengauss/sqltuner/`、`internal/engine/context/builder.go`、`internal/opengauss/skill/query/sqltune_skill.go`

---

## 1. 问题陈述

用户问 "SQL_ID xxx 如何优化"，期望拿到 sqltune 的 5 维度结构化报告。
实测三种 LLM 都不给：

| 模型 | 失败模式 |
|---|---|
| 35B (qwen36) | 第 2 轮乐观调 sqltune (8s) → phase A failed silently → 切通用诊断 |
| Opus | 18 轮 raw `sql` 硬解 → 拿不到完整 SQL → 让用户自己粘 SQL |
| GLM-5.1 | 18 轮 raw `sql` 硬解（与 Opus 完全同模式）→ og 列名差异挂壁 → 让用户粘 SQL |

**Opus 和 GLM-5.1 失败模式 100% 一致**——卡在 og-lite 元数据列名（`unique_sql_id` vs `query_id`，`statement_history` 没 `schema_name`）。三个独立模型同样卡点，**不是模型智能问题，是 prompt 设计 + og 元数据缺设计的共同结果**。

**已确认 max_turns = 20**（`internal/engine/config.go:39 DefaultMaxTurns: 20`），18 轮停止是 Opus / GLM 自己的"努力上限"，不是引擎硬约束 — engine 给的预算够用。

**根因双重**：

1. **og 在 `dbe_perf.statement` 里存的 SQL 不可 EXPLAIN**
   - 字面量被归一化成 `?` / `$N` (unique_sql 聚合机制)
   - 表名无 schema 前缀 (`SET search_path` 不持久化)
   - sqltune 的 `runExplain` 直接喂给 EXPLAIN → 必挂

2. **engine 没有从 SQL_ID 到「ready-for-EXPLAIN SQL」的转换层**
   - 35B 不知道这层缺失，把 `?` 版本喂给 sqltune
   - Opus 严格守 prompt "完整 SQL"，不调 sqltune；自己 raw query 又被 og-lite 列名差异 + result cache 卡住

---

## 2. 修复目标（验收标准）

成功的标志（同一 demo 场景，下次实测）：

- [ ] **35B / Opus / GLM-5.1 三组**在用户问 "SQL_ID xxx 如何优化" 时**都能拿到 sqltune 的 5 维度报告**（同一 demo 场景重跑，三模型必须全过）
- [ ] sqltune 失败时 **/llm 必须明确告知用户失败原因**，不能静默退化
- [ ] 用户**不需要手动粘贴 SQL**，引擎自己从 og 元数据补全

---

## 3. 四个候选方案

### 方案 A · 改 system prompt（最便宜，最先做）

**文件**：`internal/engine/context/builder.go:431-444`

**当前 prompt**：
```
**用户意图: 单条 SQL 调优**（"这条 SQL 怎么优化 / 有没有优化空间 / SQL 慢 / 帮我看下这个 SQL"）
  → **第一调用 sqltune** 工具，把用户给的完整 SQL 传给它
  → sqltune 内部跑 5 维度专项调优分析（SQL 重写 / 索引 / HINT / 表结构 / 统计），含真实 EXPLAIN 验证 + 等价性检查
  → **不要走 health/alert/activesessions 那一套**（那是聚类层诊断，跟单 SQL 调优无关）
  → sqltune 输出已经是完整 markdown 报告，你直接转给用户即可，不用再加自己的分析
```

**新 prompt**（在原文末追加）：
```
**取 SQL 文本的策略（重要）**：
  - 用户直接粘 SQL → 用粘的版本
  - 用户只给 SQL_ID（如 "SQL_ID 33402943 怎么优化"）：
    1. 先调 sql 工具查带字面量的版本：
       SELECT query FROM dbe_perf.statement_history
        WHERE unique_query = (SELECT query FROM dbe_perf.statement WHERE unique_sql_id = <ID>)
        ORDER BY start_time DESC LIMIT 1;
    2. 若上面拿不到（statement_history 已淘汰），用 statement.query 但**先用样例字面量替换 `?`**
       例如 LIKE ? → LIKE '%test%'，TO_CHAR(d,?) → TO_CHAR(d,'YYYY')
    3. 表名缺 schema 前缀（裸 `customers` 而非 `sqltune_demo.customers`）：
       调 sql 查 information_schema.tables 找归属 schema，重写后再传 sqltune

**og-lite 元数据列名速查（实测三个模型都用错过 PG 命名约定，照下面来）**：
  - dbe_perf.statement: 字段 `unique_sql_id` (不是 query_id) / query / n_calls / total_elapse_time
  - dbe_perf.statement_history: 字段 `unique_query` / query / start_time / finish_time / db_time
    （**没有 schema_name / queryid / query_id 这些列**）
  - dbe_perf.gs_slow_query_info / gs_slow_query_history: 字段 query_plan / start_time / finish_time / duration
  不要试 PG 命名约定（query_id / queryid / schema_name），og-lite 没有这些列。
  失败一次就不要在同一字段名上重试。

**sqltune 失败处理（重要）**：
  - sqltune 返回错误时**不要静默切通用诊断**，必须在最终输出明确说明：
    "/sqltune 调用失败，原因：<error message>。已降级到通用诊断"
  - 错误含 "phase A" / "plan collection failed" / "relation does not exist" / "unbound parameter"：
    优先尝试 schema 补全或字面量替换后重试 sqltune；3 次重试都失败再降级
```

**变更行数**：~30 行 markdown / Go string literal
**风险**：低（仅 prompt，不改逻辑）
**验证**：同 demo 场景重跑 /llm，看 35B / Opus 是否能拿到 sqltune 完整报告

---

### 方案 B · sqltune 容错占位符 SQL（必做）

**文件**：`internal/opengauss/sqltuner/plan_collector.go`

**改动**：`Collect` 入口前加占位符检测；`runExplain` 失败后增强错误信息

**伪代码**：
```go
func (c *PlanCollector) Collect(ctx context.Context, sql string, opts TuneOptions) (*PlanInfo, error) {
    // (新增) 不要把含 ? / $N 的归一化 SQL 喂给 EXPLAIN
    if n := unboundParamCount(sql); n > 0 {
        return nil, &PlaceholderSQLError{
            Count:   n,
            Message: fmt.Sprintf("SQL contains %d unbound placeholder(s). 这通常说明 SQL 是从 dbe_perf.statement 取的归一化版本，无法直接 EXPLAIN。请提供带字面量的原始 SQL，或先从 dbe_perf.statement_history 取带值的版本。", n),
        }
    }
    // ... 原逻辑
}

func unboundParamCount(sql string) int {
    // 简单解析：跳过单引号字符串和注释，数 ? / $N
    // (实现 ~30 行)
}

type PlaceholderSQLError struct {
    Count   int
    Message string
}
func (e *PlaceholderSQLError) Error() string { return e.Message }
```

**对应 skill 改动**（`sqltune_skill.go:113`）：
```go
if err != nil {
    var pe *PlaceholderSQLError
    if errors.As(err, &pe) {
        return &skill.Result{
            Type:     skill.ResultError,
            Rendered: "  ⚠️ SQL 含未绑定占位符（" + strconv.Itoa(pe.Count) + " 个 ?/$N）\n     " + pe.Message,
            Summary:  pe.Message,
        }, nil
    }
    // ... 原逻辑
}
```

**变更行数**：~60 行 Go (含单元测试)
**风险**：低，纯前置检查
**验证**：单测覆盖 `?`、`$1`、字符串里的 `?` (不算)、注释里的 `?` (不算)

---

### 方案 C · sqltune 自动 schema 补全

**文件**：`internal/opengauss/sqltuner/plan_collector.go::runExplain`

**逻辑**：EXPLAIN 第一次失败若 error 含 `relation ".*" does not exist`：

```go
func (c *PlanCollector) runExplain(...) (*PlanInfo, error) {
    res, err := c.driver.Query(ctx, "EXPLAIN ... " + sql)
    if err == nil { return parsePlanInfo(res) }

    // (新增) 尝试 schema 补全
    missing := parseRelationNotExist(err.Error())
    if missing != "" {
        schema, err2 := c.findSchema(ctx, missing)
        if err2 == nil && schema != "" {
            qualifiedSQL := qualifyTableNames(sql, missing, schema)
            res, err = c.driver.Query(ctx, "EXPLAIN ... " + qualifiedSQL)
            if err == nil { return parsePlanInfo(res) }
        }
    }
    return nil, fmt.Errorf("EXPLAIN failed: %w", err)
}

// 查 pg_class + pg_namespace 找表归属（处理多 schema 候选时取最近一次有数据的）
func (c *PlanCollector) findSchema(ctx context.Context, table string) (string, error) {
    rows := c.driver.Query(ctx,
        "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid "+
        "WHERE c.relname=$1 AND c.relkind IN ('r','v','m','p') ORDER BY n.nspname LIMIT 1",
        table)
    // ...
}
```

**变更行数**：~120 行 Go (含表名解析 + 单测)
**风险**：中
- 多 schema 候选时选错 schema 可能给出错误的 EXPLAIN（表结构同名但内容不同）
- SQL 重写的 token 解析需谨慎（不能改字符串字面量里的表名）
**验证**：覆盖
- 单一 schema 候选 → 自动补全成功
- 多 schema 候选 → 取第一个，**banner 标注 "假定 schema=X"**
- 字符串里有同名标识符 → 不替换

---

### 方案 D · engine SQL 文本解析层（理想终态）

**新文件**：`internal/engine/sqlfetch/fetcher.go`

新增工具 `sql_fetch` 让 LLM 直接用：

```
用户: SQL_ID 33402943 怎么优化
LLM round 1: sql_fetch(33402943)
  → engine 内部串联：
     ① dbe_perf.statement_history WHERE unique_sql_id (拿带字面量的最近执行)
     ② 失败 → dbe_perf.statement.query + 替换 ? 为 statement_history 里的样例值
     ③ 失败 → dbe_perf.statement.query 原样返回 + 标注 "归一化 SQL，可能 EXPLAIN 失败"
     还自动加 schema 前缀（基于查到 SQL 涉及的所有表）
  → 返回 ready-for-EXPLAIN SQL + 元数据（schema、字面量来源）
LLM round 2: sqltune(返回的 SQL)
  → 正常给 5 维度报告
```

**变更行数**：~250 行 Go (新模块 + 工具注册 + 单测 + 集成测)
**风险**：中
- 需要四库分别实现（og / pg / mysql / oracle 的 SQL 来源不同）
- 字面量替换可能选到不代表性的样例值（影响 EXPLAIN 估算）
**验证**：四库各跑 demo 慢 SQL → /llm 优化 → 都能拿到 sqltune 完整报告

---

## 4. 建议执行顺序（GLM 数据点后已升级）

| 阶段 | 方案 | 工作量 | 立即收益 |
|---|---|---|---|
| **P0 (今天/明天)** | A + B | 1 小时 | 35B 不再吞错误；大模型知道怎么取能用的 SQL；用户能看到 sqltune 失败原因 |
| **P1 (本周，C + D 并行)** | C 自动 schema 补全 + **D engine sqlfetch** | 半天 + 2-3 天 | C: sqltune 自救覆盖 90% 场景；**D: 根除 LLM 对 og 元数据的知识盲区**（三个模型都卡同一处证明这块 LLM 永远学不会，不该指望 prompt） |

**升级理由（基于 GLM 实测数据）**：原排期把 D 放 P2，理由是工作量大。但实测发现 Opus / GLM 失败模式 100% 一致，都卡在 og-lite 列名差异上 — 这意味着 **A/B/C 都是"教 LLM 解决"，仍依赖 LLM 正确执行；D 是"engine 自己解决"，根除依赖**。所以 D 不是奢侈品，是必需品。

---

## 5. 开放问题（先讨论再动手）

1. **方案 A 的 prompt 长度**：原 prompt 已经很长（builder.go 几百行），再加 30 行会不会让小模型注意力分散？
   - 备选：把"取 SQL 文本策略"放进 sqltune 的 ToolDef.Description（只在 sqltune 上下文激活）

2. **方案 B 的占位符识别**：`?` 在字符串里 (`LIKE '?'`) 不算占位符，但简单 grep 会误识别。是否引入轻量 SQL parser？
   - 备选：用现有 `internal/sqltune/sqlsplit.go`（如果存在）的 token 化能力，否则手写最小可用 tokenizer

3. **方案 C 的多 schema 歧义**：`customers` 在 `public` 和 `sqltune_demo` 都有时怎么选？
   - 选项 1：调用方传 hint（用户当前 search_path）
   - 选项 2：取 row count 最大的（最可能是真业务表）
   - 选项 3：返回多候选让 LLM 决定

4. **方案 D 的字面量样例值**：从 statement_history 拿样例值，但若该 SQL 历史只跑过一次且参数极端（比如 `WHERE id = 99999999999`）会让 EXPLAIN 估算失真。
   - 备选：取多次执行的中位数；或在 EXPLAIN 时主动 RANDOMIZE 用边界值

5. **PG / MySQL / Oracle 各库等价方案**：不仅 og 有这问题。pg 的 `pg_stat_statements.query` 同样归一化；mysql 的 `performance_schema.events_statements_summary_by_digest.DIGEST_TEXT` 也是。是否在 P2 阶段统一一套接口？

6. **engine max_turns 是否够用**：实测 `DefaultMaxTurns = 20`（已确认 `internal/engine/config.go:39`）。Opus / GLM 各跑 18 轮停止是模型自己的"努力上限"，不是引擎硬约束。**结论：max_turns 够用，不需要调整**。但 P0 改完后若仍 20 轮跑不完，再考虑放宽到 30。

---

## 6. 测试用例（修复后必须能通过）

**Test 1**：og 上跑 `sqltune_demo` 的 17.7s 多表慢 SQL，让它进 dbe_perf.statement (`?` + 无 schema)，然后：

```
opendb -c og /llm "SQL_ID 33402943 如何优化"
```

期望：5 分钟内拿到 sqltune 的 5 维度结构化报告（cost 数字 + 5 candidates + EXPLAIN 验证）。

**Test 2**：构造一个 dbe_perf.statement_history 已淘汰的 SQL（只剩 statement 归一化版本）：

```
opendb -c og /llm "SQL_ID xxxxxx 如何优化"
```

期望：拿到 sqltune 报告 + banner 标注 "字面量样例值由引擎自动填充"。

**Test 3**：故意删 sqltune_demo schema，让 sqltune EXPLAIN 必失败：

```
opendb -c og /sqltune "<broken sql>"
```

期望：明确错误信息 "EXPLAIN 失败：relation 'customers' does not exist。已尝试 schema 补全失败"，**不再是模糊的 "phase A failed"**。

**Test 4**：用 35B 跑 Test 1，确认不再 silently swallow 错误。
**Test 5**：用 Opus 跑 Test 1，确认不再 18 轮 raw query。
**Test 6（三模型一致性）**：35B / Opus / GLM-5.1 同跑 Test 1，**三组都必须拿到 sqltune 完整报告**（含 5 candidates + EXPLAIN 验证 + cost diff）。任一模型还在让用户粘 SQL 即视为修复未达标。

---

## 7. 关联文件 / 依赖

- `internal/opengauss/sqltuner/tuner.go` — phase A 编排
- `internal/opengauss/sqltuner/plan_collector.go` — EXPLAIN 入口
- `internal/opengauss/skill/query/sqltune_skill.go` — skill 适配
- `internal/engine/context/builder.go:431+` — system prompt
- `internal/engine/tool/orchestrator.go` — tool 错误传播路径（修方案 A 时需确认错误能进 LLM 上下文）
- `docs/sqltune/design-og-sqltuner.md` — 总设计文档（最终修复后需补一节"输入容错"）
- `~/.claude/projects/-Users-sqlrush-opendb/memory/todo-llm-sqltune-routing-failure.md` — 本次诊断分析（已写入）

---

## 8. 待用户确认的问题

1. P0 + P1 + P2 全做，还是先只做 P0 看效果？
2. 方案 A 改 prompt 时，是把"取 SQL 文本策略"放在通用 prompt 里（所有场景都看到），还是放进 sqltune ToolDef（只在 LLM 准备调 sqltune 时激活）？
3. 方案 D 是 og 单库先做，还是从一开始就抽四库统一接口？
