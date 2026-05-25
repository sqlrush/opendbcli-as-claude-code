# OpenDB v1.1.30 修复方案

> 状态：待评审
> 范围：opendb (main) + 同步 dbaa / linkdb
> 关联：
>   - v1.1.28 / v1.1.29 已完成的 P0+P1 — `docs/sqltune/plan-llm-sqltune-routing-fix.md`
>   - 实测复杂 SQL 暴露的新墙 — 本次 10 表 SQL 实测纪录
>   - 关联 memory：[`todo-llm-sqltune-routing-failure.md`](../../../.claude/projects/-Users-sqlrush-opendb/memory/todo-llm-sqltune-routing-failure.md)

---

## 1. 背景与动机

### 1.1 v1.1.28 / v1.1.29 已完成的成果

实测 4 模型（35B / Opus / GLM-5.1 / DeepSeek-V4-Pro）在 **5 表场景**全部通过：从 SQL_ID 33402943（5 表，2 个 ?）出发都能拿到 sqltune 的 5 维度结构化报告。这一战已胜。

### 1.2 v1.1.30 要攻的新战线

实测 **10 表 SQL_ID 2278588878**（CTE + 3 层嵌套 IN + 相关子查询 + 4 个 ?）暴露了三道新墙：

| 墙 | 现象 | 严重度 |
|---|---|---|
| **墙 7 · Memory 跨 SQL 污染** | Opus 在 2278588878 上输出**针对 33402943 的诊断**（4 表 + 幻觉日期 `'2026-01-15'`），自信地给错答案 | **🚨 P0 — 比"无答案"更危险，需立刻修** |
| **墙 1 · 占位符密度** | 35B 在 4 个 `?` 的 SQL 上 16 轮空转，最终输出空白 | P0 — 阻塞 35B 在复杂场景使用 |
| **修复 1 · engine 兜底** | LLM 输出空 + 调用过工具时，引擎返回空 string 给用户 | P1 — 安全网，不是治本 |

### 1.3 为什么 v1.1.30 必须做（产品视角）

走"策略 B 爬复杂度阶梯"——简单 SQL（用户自己也能优化）不是产品价值，**复杂 SQL 才是客户付费理由**。dbaa（农行）/ linkdb（仁合时创）业务系统的真实慢 SQL 普遍 8-15 表 + 多层嵌套，v1.1.30 不过这关产品无法商用。

---

## 2. 目标与验收

### 2.1 v1.1.30 必达目标

- [ ] 35B / Opus 问 "SQL_ID 2278588878 如何优化"，**给出真正针对 10 表 SQL 的诊断**（不是张冠李戴 33402943 的方案）
- [ ] 35B 在 10 表 SQL 上 ≤ 8 轮拿到包含 5 维度的 sqltune 报告（即使 Phase A 用 auto-substitute 后的 SQL）
- [ ] 任意 LLM 在任意失败场景下，**用户至少看到"调了哪些工具 + 卡在哪"** 的可读输出，不再有空白返回
- [ ] memory 不再跨 SQL 污染（清新 SQL_ID 的 memory 后，旧 SQL_ID 的诊断不会被错误召回）

### 2.2 不在 v1.1.30 范围内（推迟到下一版）

- 墙 2 sqltune 自身性能优化（Opus 在 sqltune 5m45s 那次实测过墙了，先不动）— v1.1.31
- 墙 3 5 维度方案在 10 表 SQL 上的真实价值与可执行性 — v1.2.0
- 墙 4 PG/MySQL/Oracle 等价问题 — v1.2.x
- 墙 5 跨实例知识迁移 — v1.2.x

### 2.3 验收测试矩阵

跑 4 模型 × 2 场景，全部通过才发版：

| 测试 | 期望 |
|---|---|
| **T1**：清空 memory + 35B 问 33402943（5 表）| 5 维度方案 + cost 证据，**与 v1.1.28 实测对齐** |
| **T2**：清空 memory + 35B 问 2278588878（10 表）| 真针对 10 表的诊断（提到 suppliers/categories/CTE/嵌套），**Phase A 不再因占位符 fail** |
| **T3**：清空 memory + Opus 问 2278588878 | 真针对 10 表的诊断 |
| **T4**：跑 T1 后**不清 memory** + 35B 问 2278588878 | 必须回 10 表的诊断，**不允许复用 T1 的诊断** |
| **T5**：故意构造 sqltune 必失败的 SQL（含未知函数）+ 任意模型 | 引擎合成"调用了 X 工具，错误是 Y" 的可读输出，不是空白 |
| **T6**：sqlfetch 对一个完全没存进 dbe_perf 的 SQL_ID | 友好错误 + 建议，不崩溃 |

---

## 3. 三个修复方案

### 3.1 方案 A · Memory 上下文隔离（墙 7）

#### 3.1.1 现状分析

`internal/engine/memory/store.go` 里的 memory 召回逻辑（推测，待 grep 确认）：
- memory 文件存在 `~/.opendb/memory/*.md`，frontmatter 有 `name` `description` `type` 字段
- 召回时大概率走"按 description 关键词模糊匹配"或"按表名出现匹配"
- **没有 SQL fingerprint 概念**，所以 33402943 的 memory 在问 2278588878 时被召回（因为表名重叠）

#### 3.1.2 设计

**核心改动**：memory 写入时计算 SQL **fingerprint**，召回时严格匹配。

##### Fingerprint 定义

```go
// SQLFingerprint 把 SQL 文本归约为可比较的 hash。设计意图：
//   - 同一 SQL 的不同字面量值产生相同 fingerprint
//   - 不同表集合 / 不同 JOIN 拓扑产生不同 fingerprint
//   - 不依赖数据库的 unique_sql_id（跨 DB 一致）
type SQLFingerprint struct {
    Hash    string   // SHA256 of normalized SQL
    Tables  []string // sorted table names (for fuzzy match)
    HasCTE  bool     // structural marker
    Depth   int      // max subquery depth
}

func ComputeFingerprint(sql string) SQLFingerprint
```

归约规则：
1. 字符串字面量替换为 `'?'`
2. 数字字面量替换为 `?`
3. 表名前 schema 去掉（`sqltune_demo.customers` → `customers`）
4. 多余空白合并
5. 关键字大小写归一化（`SELECT` / `select` → `select`）

##### Memory entry schema 扩展

新 frontmatter 字段：

```yaml
---
name: SQL_ID 33402943 调优结果
description: ...
type: project
sql_fingerprint: a3b4c5d6e7f8g9h0...    # NEW
sql_tables: [customers, orders, order_items, products]   # NEW
sql_depth: 1                              # NEW
sql_has_cte: false                        # NEW
---
```

##### 召回策略（严格 + 模糊）

```go
type RecallMode int

const (
    RecallStrict RecallMode = iota  // 必须 fingerprint 完全相等
    RecallFuzzy                     // tables Jaccard ≥ threshold + 标注相似度
    RecallNone                      // 不召回 SQL 类 memory
)

// FindRelevant 给 sqltune 用的召回入口
func (s *Store) FindRelevant(input QueryContext) []MemoryEntry {
    if input.SQL != "" {
        return s.findByFingerprint(input.SQL, RecallFuzzy)
    }
    return s.findByTables(input.Tables, RecallNone)
}
```

##### 召回结果在 prompt 中的标注

```
之前：直接把 memory 内容塞进 prompt（LLM 当作权威）
之后：标注 "[匹配类型: 严格 / 模糊 X%]，仅供参考"
```

具体 prompt 文案：

```
## 历史诊断（参考）

⚠️ 以下是历史相似 SQL 的诊断结果，**不是当前 SQL 的真实诊断**：
- 当前 SQL fingerprint: <hash A>
- 命中 memory fingerprint: <hash B>，相似度 65%（基于表集合 Jaccard）
- 命中表集合: [customers, orders, order_items, products]
- 当前表集合: [customers, orders, order_items, products, regions, suppliers, categories, payments, reviews, shipments]

memory 内容：
<...>

⚠️ 当前 SQL 多了 6 张表（regions / suppliers / categories / payments / reviews / shipments），
请基于当前 SQL 的实际结构出诊断，**不要直接照搬上面 memory**。
```

#### 3.1.3 文件改动清单

```
内部模块:
  internal/engine/memory/fingerprint.go    新建（~200 行）
  internal/engine/memory/store.go          改 FindRelevant
  internal/engine/memory/store_test.go     加 fingerprint 测试
  
SQL parser:
  internal/sqlparse/normalizer.go          新建（~150 行）
  internal/sqlparse/normalizer_test.go     新建

集成:
  internal/opengauss/sqltuner/tuner.go     调 ComputeFingerprint 传给 memory query
  internal/engine/context/builder.go       prompt 模板加"相似度标注"块
```

#### 3.1.4 兼容性

旧 memory 文件（无 fingerprint）→ 启动时 lazy 计算 fingerprint 并写回。或干脆按"低优先级 fuzzy"处理，3 次召回不到就降级为 RecallNone。

#### 3.1.5 风险

- **Jaccard 阈值取多少**：太严格（比如 0.95）几乎没命中；太宽松（0.5）又会召回不相关 SQL。建议初始 **0.85** 后续看实测调
- **CTE / 子查询的表名提取**：需要 SQL parser，不能简单 regex。建议复用现有 `internal/opengauss/sqltuner/schema_collector.go` 的 InvolvedTables 提取逻辑
- **fingerprint 不稳定**：同一 SQL 因为加 / 删空白 / 注释产生不同 hash。归约规则要充分覆盖

---

### 3.2 方案 B · Auto-substitute Placeholders（墙 1）

#### 3.2.1 现状分析

og 即使 L2,L2 模式仍然把部分常量归一化为 `?`：

```sql
WHERE region_id <= ?         -- INT，原值是 50
AND   UPPER(c.email) LIKE ?  -- VARCHAR，原值 '%@GMAIL.COM'
AND   TO_CHAR(o.order_date, ?) = ?   -- 函数模板 + 字面量
```

35B 在 4 个 ? 上推不出合理的替换值，sqltune phase A 必失败。

#### 3.2.2 设计

**核心思路**：sqlfetch 内部用**确定性规则**自动替换 `?` 为合理样例值，让 LLM 拿到的 SQL 直接可 EXPLAIN。**不依赖 LLM 智能**——纯符号推理 + 列元数据。

##### 替换规则表

| 上下文模式 | 列类型 | 替换值 |
|---|---|---|
| `col LIKE ?` | VARCHAR / TEXT | `'%test%'` |
| `col = ?` / `col <> ?` | INT / BIGINT | `1` |
| `col = ?` / `col <> ?` | VARCHAR | `'test'` |
| `col = ?` / `col <> ?` | DATE / TIMESTAMP | `CURRENT_DATE` |
| `col <= ? / >= ? / < ? / > ?` | INT | `50`（中等值）或查 `pg_stats.histogram_bounds` 取中位数 |
| `col <= ? / >= ?` | DATE | `CURRENT_DATE - 30`（30 天前）|
| `col IN (?, ?, ?)` | 任意 | 展开为对应类型字面量 |
| `TO_CHAR(date_col, ?)` | (函数模板) | `'YYYY-MM-DD'` |
| `LIMIT ?` | (limit) | `100` |
| 未识别 | (fallback) | `1` 或 `'placeholder'` |

##### 实现架构

```
sqlfetch_skill.go (修改):
  Execute() — 增加 auto_substitute 参数 (bool, 默认 true)
              拉到 SQL 后调 PlaceholderSubstituter.Substitute(sql, schema)
              返回带样例值的 SQL + 标注哪些是合成的

placeholder_substituter.go (新建):
  type PlaceholderSubstituter struct {
      driver db.Driver  // 用于查 pg_stats / information_schema
  }
  
  func (s *PlaceholderSubstituter) Substitute(
      sql string,
      schema string,
  ) (substitutedSQL string, substitutions []Substitution, err error)
  
  type Substitution struct {
      Position int    // ? 在原 SQL 中的位置（offset）
      Context  string // "col LIKE ?", "col = ?", etc.
      Value    string // 替换值
      Source   string // "rule" / "stats_median" / "default"
  }
```

##### 替换流程

```
1. 用 detectPlaceholders（已有）找出所有 ? 位置
2. 对每个 ? 向左 token 化扫描，识别：
   - 列名（如 c.email, o.order_date）
   - 操作符（=, <=, LIKE, IN）
   - 函数包裹（TO_CHAR(date, ?)）
3. 查列类型：
   - SELECT data_type FROM information_schema.columns
     WHERE table_schema = ? AND table_name = ? AND column_name = ?
4. 应用规则表 → 生成替换值
5. 重写 SQL（按位置从后往前替换，避免 offset 错位）
6. 返回原 SQL + 替换后 SQL + Substitution 列表
```

##### 输出示例

```
✓ /sqlfetch 2278588878 命中（来源：dbe_perf.statement_history）

  schema: sqltune_demo
  状态: ✅ 已自动替换 4 个占位符（auto-substitute），可直接喂给 /sqltune

  替换详情：
    Position 1: region_id <= ?     → region_id <= 50    (规则: INT 范围比较)
    Position 2: UPPER(email) LIKE ? → LIKE '%test%'      (规则: VARCHAR LIKE)
    Position 3: TO_CHAR(date, ?)    → TO_CHAR(d,'YYYY')  (规则: TO_CHAR 函数模板)
    Position 4: ... = ?             → '2024'             (规则: TO_CHAR 结果比较)

  --- SQL ---
  <substituted SQL ready for sqltune>

  --- 下一步 ---
  /sqltune <substituted SQL>

  ⚠️ 注意：替换值是 sqltune 跑 EXPLAIN 用的"合理样例"，不是原 SQL 的真实业务值。
     CBO 估算基于统计信息，不依赖具体字面量，所以执行计划结构与真实 SQL 一致。
```

##### LLM 可见的 ToolDef 升级

```go
ToolDef{
    Name: "sqlfetch",
    Description: "...原描述... " +
        "默认开启 auto_substitute=true，自动用合理样例值替换 ? 占位符，" +
        "返回的 SQL 可直接喂给 /sqltune 而无需手动替换。" +
        "如果你需要原始归一化版本（含 ?），传 auto_substitute=false。",
    Parameters: ...增加 auto_substitute bool ...
}
```

#### 3.2.3 文件改动清单

```
新建:
  internal/opengauss/sqltuner/placeholder_substituter.go   (~300 行)
  internal/opengauss/sqltuner/placeholder_substituter_test.go  (~200 行)

修改:
  internal/opengauss/skill/query/sqlfetch_skill.go         (~50 行改动)
  internal/engine/context/builder.go                       (prompt 内提到默认 substitute)
```

#### 3.2.4 风险

- **复杂 WHERE 子句的 token 化**：CTE / window function / lateral join 里的 ? 上下文识别难度大。MVP 先做简单 WHERE，复杂场景 fall back to "1" 默认值
- **substitute 后 EXPLAIN 估算偏差**：替换值不真实，CBO 估算可能跟生产差很多。但**plan 结构（join order / scan method）通常一致**，sqltune 主要看结构问题，不看 ms 精度
- **类型查询性能**：每个 ? 查一次 information_schema 太慢。改成**批量查所有列类型一次**，cache 在内存

#### 3.2.5 测试矩阵

```go
TestPlaceholderSubstituter_BasicRules:
  - LIKE ? → '%test%'
  - = ? on int → 1
  - = ? on varchar → 'test'
  - <= ? on int → 50
  - IN (?,?,?) → ('a','b','c')
  
TestPlaceholderSubstituter_FunctionContext:
  - TO_CHAR(date, ?) → 'YYYY-MM-DD'
  - SUBSTRING(col, ?, ?) → 1, 10
  - DATE_TRUNC(?, col) → 'day'

TestPlaceholderSubstituter_RealWorld:
  - 跑 SQL_ID 33402943 (5 表) → 替换 2 个 ?
  - 跑 SQL_ID 2278588878 (10 表) → 替换 4 个 ?
  - 替换后 SQL 必须能在 og 上 EXPLAIN 成功
```

---

### 3.3 方案 C · Engine Partial-Result Synthesizer（修复 1 兜底）

#### 3.3.1 现状分析

`internal/engine/engine.go` 的退出逻辑：

```go
// 当前代码（line 274-298）:
if len(resp.ToolCalls) == 0 {
    captureDeliverable(resp.Content)
    result.Content = deliverableContent.String()  // ← 可能是 ""
    // ... 其他 metadata 设置 ...
    return result, nil
}
```

35B 在墙 1 / 墙 7 任意一个失败场景下，最终轮 Content 是 ""，引擎返回空白给用户。

#### 3.3.2 设计

退出前增加 fallback 检查：如果 Content 空 + 调用了多个工具 → 调 `synthesizePartialResult` 合成简洁摘要。

```go
if len(resp.ToolCalls) == 0 {
    captureDeliverable(resp.Content)
    result.Content = deliverableContent.String()
    
    // NEW: 模型放弃了但跑过工具 → 别返回空白
    if result.Content == "" && len(result.ToolsInvoked) > 0 {
        result.Content = synthesizePartialResult(
            result.ToolsInvoked,
            messages,
            turn+1,
        )
    }
    // ... 其他 metadata ...
}
```

`synthesizePartialResult` 已存在（line 408 用于 MaxTurnsHit 路径），只是没在 no-tool-call exit 路径调用。这次复用即可。

#### 3.3.3 输出示例

```
⚠️ 模型在 16 轮内未给出最终结论。已调用以下工具：

| 工具 | 调用次数 | 最近一次输出 / 错误 |
|---|---|---|
| sqlfetch | 2 | ✓ 拿到 SQL（4 占位符）|
| sqltune  | 1 | ✗ PlaceholderSQLError: 4 unbound placeholders |
| sql      | 10 | 各种查表，未找到带字面量的 SQL 版本 |
| tableinfo | 4 | ✓ 拿到 4 张表的列结构 |

最后一轮模型输出为空。可能原因：
- 模型遇到不可解的占位符问题（v1.1.30 的 auto-substitute 应能解决）
- 上下文已达模型注意力阈值

建议：
1. 升级到 v1.1.30 后重试（含 auto-substitute）
2. 换用更强模型（/model opus / glm-5.1）
3. 直接粘贴带字面量的 SQL：/sqltune <SQL 文本>
```

#### 3.3.4 文件改动清单

```
修改:
  internal/engine/engine.go        ~10 行（exit 逻辑加 fallback 调用）
  internal/engine/engine_test.go   加 1-2 个测试
```

工作量：30 分钟。

#### 3.3.5 风险

无。纯增量修改，不影响现有成功路径。

---

## 4. 执行顺序与里程碑

### 4.1 推荐顺序

```
Day 1: 方案 C (修复 1) — 30 min
       立刻给所有失败场景兜底，不再有空白返回
       优先做这个：即使后面 A/B 没做完，至少用户看到东西

Day 1-2: 方案 A (memory 隔离)
       核心修复：fingerprint + 召回标注
       完成后 Opus 不再"自信地给错答案"

Day 2-3: 方案 B (auto-substitute)
       让 35B 也能搞定复杂 SQL
       完成后 4 模型一致通过 T1-T6

Day 4: 联合验收 + 发版
       T1-T6 全过 → 发 v1.1.30
       博客：续写 v1.1.28 那篇，加新 chapter "复杂 SQL 的下一战"
```

### 4.2 里程碑（每个独立可交付）

| 里程碑 | 交付物 | 验收 |
|---|---|---|
| **M1** | 方案 C 完成 | T5 通过 |
| **M2** | 方案 A 完成 | T1 + T4 通过（memory 不污染）|
| **M3** | 方案 B 完成 | T2 + T3 通过（35B + Opus 攻克 10 表）|
| **M4** | 全 6 测试 + 发版 | v1.1.30 GitHub Release |

每个里程碑独立可发版（如果时间紧 v1.1.30 可以只发 M1+M2，M3 留 v1.1.31）。

---

## 5. 开放问题（先讨论再动手）

1. **fingerprint 算法选择**：用我提议的"归约后 SHA256"还是引入现有库（如 `pg_query_go` 的 normalize 函数）？前者简单，后者更标准但加依赖。
   - 建议：MVP 用归约 SHA256，后续如有问题再换 `pg_query_go`

2. **Jaccard 阈值**：0.85 严格还是 0.7 宽松？
   - 建议：MVP 走 0.85，发版后看实测调

3. **memory fingerprint 是否要存到 frontmatter 还是单独 index**：
   - frontmatter：每次召回都要扫所有 .md 解析 frontmatter，O(N)
   - 单独 index 文件：~/.opendb/memory/.fingerprint_index.json，O(log N)
   - 建议：MVP 用 frontmatter，N 大于 1000 再加 index

4. **auto-substitute 的默认值**：默认 true 还是 false？
   - true：LLM 调 sqlfetch 直接拿到能跑的 SQL，UX 好；但替换错时不易察觉
   - false：用户/LLM 必须显式开启，更安全
   - 建议：默认 true，输出里**清晰标注哪些被替换了**

5. **PG / MySQL / Oracle 的 placeholder substituter 是否同步做**：
   - og 先做（v1.1.30），三库等 og 验证后再扩展（v1.1.31 或 v1.2.0）
   - 建议：先 og

6. **sqltune 调 sqlfetch 的内部链路**：
   - 当前 sqltune 接受 SQL 文本，不知道 SQL_ID
   - 是否要让 sqltune 也能接受 SQL_ID 参数（内部调 sqlfetch）？
   - 建议：先不做，让 LLM 显式调 sqlfetch + sqltune。后续看是否值得"sqltune 一键模式"

---

## 6. 与 v1.1.28 / v1.1.29 的兼容性

- 旧 memory 文件无 fingerprint → lazy 补算或归类为 RecallNone
- 旧 sqlfetch 调用（不传 auto_substitute）→ 默认开启 auto_substitute（new behavior，**break change**！需在 release notes 写清楚）
- engine 兜底是纯增量，不破坏

**break change 警告**：方案 B 默认 auto_substitute=true 改变了 v1.1.28/29 sqlfetch 的输出格式。如果有用户脚本依赖原始 ? 输出，会受影响。
- 缓解：保留 `auto_substitute=false` 选项，文档明示

---

## 7. 测试用例细节

### 7.1 T1：v1.1.28 回归（5 表）

```bash
rm -rf ~/.opendb/memory/*
dbaa
/model qwen36-35b-a3b
SQL_ID 33402943 如何优化
```

期望：5 维度方案 + Cost 21,936 → 1,000-2,000 类似 v1.1.28 实测。

### 7.2 T2：35B 攻克 10 表

```bash
rm -rf ~/.opendb/memory/*
dbaa
/model qwen36-35b-a3b
SQL_ID 2278588878 如何优化
```

期望：
- ≤ 8 轮
- 输出**明确提到** suppliers / categories / regions / payments / reviews / shipments 中至少 4 张表
- 输出**明确处理** CTE 或 3 层嵌套 IN
- 5 维度方案完整

### 7.3 T3：Opus 攻克 10 表

同 T2 但用 `/model opus`。

期望：
- ≤ 6 轮（Opus 应该比 35B 快）
- 内容质量更高（含组合索引等高级建议）

### 7.4 T4：memory 不污染

```bash
# 先跑 T1 让 memory 写入 33402943 的诊断
rm -rf ~/.opendb/memory/*
dbaa /llm "SQL_ID 33402943 如何优化"  # batch 模式让 memory_write 触发

# 不清 memory，直接问 2278588878
dbaa /llm "SQL_ID 2278588878 如何优化"
```

期望：
- 输出**真针对 10 表**（不能复用 33402943 的方案）
- prompt 注入的 memory 块**清晰标注** "相似度 X%"
- LLM 输出里**不出现** 33402943 的 SQL_ID（除非作为对比说"跟历史 SQL_ID 33402943 类似"）

### 7.5 T5：失败兜底

构造一个 sqltune 必失败的 SQL：

```bash
dbaa
/llm "请优化这条 SQL: SELECT undefined_function(x) FROM nonexistent_table"
```

期望：
- 引擎返回**调了什么工具 + 失败原因** 的可读输出
- 不是空白
- 至少包含一句"建议下一步"

### 7.6 T6：sqlfetch 找不到 SQL

```bash
dbaa /sqlfetch 99999999
```

期望：友好错误，建议用户用 /slowsql 重新核对。

---

## 8. 风险与降级方案

| 风险 | 降级 |
|---|---|
| auto-substitute 改写后的 SQL 在 og 上反而 EXPLAIN 失败 | 自动 fall back 到原 SQL + 警告"已 substitute 但 EXPLAIN 失败，可能需要手工调整" |
| Memory fingerprint 阈值调不准 | 提供 GUC 风格配置 `~/.opendb/config.yaml memory.recall_threshold` |
| sqltune 在 substituted SQL 上耗时仍超 10min | engine 给 sqltune 增大超时（v1.1.31 解决）|
| 旧 memory 大量 lazy 补算导致启动慢 | 启动时只 lazy 补算"今天写的"，旧 memory 后台异步补算 |

---

## 9. 关联文档与代码

- 上一版方案 ([`plan-llm-sqltune-routing-fix.md`](plan-llm-sqltune-routing-fix.md))
- v1.1.28 博客 ([`docs/blog/v1.1.28-sqltune-routing-fix.md`](../blog/v1.1.28-sqltune-routing-fix.md))
- v1.1.28/29 CHANGELOG 节
- 关联 memory：todo-llm-sqltune-routing-failure.md

实测数据来源：本次 10 表 SQL 实测对话（不会写到博客但作为内部参考）。

---

## 10. 等用户决断的几个抉择

1. **方案 A 的 Jaccard 阈值默认 0.85 是否接受？**
2. **方案 B 的 auto_substitute 默认 true 是否接受**（这是 break change）？
3. **里程碑顺序 C → A → B 是否合理**？还是先 A 因为它最严重？
4. **v1.1.30 是否在 M1+M2 完成时就发版**（M3 留 v1.1.31），还是必须 M1+M2+M3 一起发？
5. **要不要把 og 之外的三库（PG/MySQL/Oracle）的 auto-substitute 也并到 v1.1.30**？还是 v1.1.30 仅 og，三库等 v1.1.31？

待你决断后开工。
