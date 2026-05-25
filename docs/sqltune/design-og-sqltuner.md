# OG SQL Tuner — Design Doc

**项目**: opendb / dbaa 复杂 SQL 性能调优专项（OG / GaussDB 优先）
**版本**: v0.1（design 阶段，未实现）
**日期**: 2026-04-28
**作者**: discussion 沉淀
**状态**: 待 review → 接入 P1 实现

---

## 1. 目标与非目标

### 1.1 总目标

在 OpenGauss / GaussDB 上，对**复杂 SQL**（多层嵌套子查询 / 多表 JOIN / 视图封装 / CTE / 窗口函数）：

1. **快速判断**执行计划的真实问题（成本失真 / 算子选错 / 索引失效 / 统计偏差）
2. **给出最优方案**，覆盖五个维度：
   - SQL 重写（子查询 ↔ JOIN，EXISTS ↔ IN，谓词下推等）
   - HINT 注入（OG 5.0 原生 `/*+ */` 语法）
   - 索引调整（新建 / 改 covering / expression index）
   - 表结构改造（分区 / 反规范化 / 数据类型）
   - 统计信息修复（ANALYZE / 扩展统计）
3. **每个方案都有 EXPLAIN 验证证据**，不是 LLM 凭直觉给

### 1.2 七项核心能力目标（用户明确需求）

本期 tuner 必须能交付以下 7 项**专项能力**，这是衡量"超过 raw LLM"的硬指标：

| # | 能力 | 详细要求 | 涉及机制 |
|---|------|---------|---------|
| **G1** | **索引策略** | 不止"加什么索引"，要给：单列 / 复合 / covering / expression / 部分索引 / 倒序 / GIN/GiST 各种类型的精准选择，含选择性分析 + 重建排序 | M2 |
| **G2** | **执行计划详细分析 + 精准解读 CBO 算法** | 解读 CBO 为什么选 NL 不选 Hash、为什么选 Seq Scan 不走索引、cost 公式拆解（IO + CPU + Network 各项贡献）、行宽估算合理性、并行度选择逻辑 | M1 + 专属 prompt |
| **G3** | **统计信息准确性分析** | 检测 actual vs estimated 偏差，定位偏差来源（基数估算错？相关性丢失？数据漂移？）、给精准 ANALYZE / 扩展统计 / 重建直方图方案 | M1 + M2 |
| **G4** | **复杂 SQL 重写优化器友好版** | 把"优化器懒得优化"的形态改成"它擅长优化"的形态：subquery flatten / view inline / predicate pushdown / OR → UNION ALL / NOT IN → NOT EXISTS / 半联接转化 等 | M3 + M4 |
| **G5** | **PlanTrace 解读 — CBO 决策溯源** | 不只是"这个计划有问题"，要回答 **"为什么 CBO 选了这个计划而不是别的"**。读 CBO trace（OG 的 `set_explain_pretty_print` / pg_hint_plan trace），分析候选路径剪枝过程 | M1 + 专属 prompt + 启用 plan_trace |
| **G6** | **各种 HINT 的精准使用** | OG 5.0 全集 HINT 精通：`tablescan` / `indexscan(t, idx)` / `nestloop(t1 t2)` / `hashjoin` / `mergejoin` / `leading((a b) c)` / `set(work_mem '64MB')` 等，给 HINT 时附带"为什么用这个 HINT 而不用那个"+ 副作用警告 | M7 + 专属 prompt |
| **G7** | **超大复杂 SQL（千行级）精准优化** | 几千行 SQL（如报表 / ETL）的端到端分析：分段拆解 → 视图展开 → 找瓶颈段（80/20）→ 局部优化 → 验证整体收益。要能扛过 100KB+ 的 SQL 文本 | M1 + view_expander + 分段 strategy |

#### G2 + G5 是 tuner 的"灵魂能力"

raw LLM 给方案是"猜"——它不知道 OG CBO 的 cost 公式、连接顺序枚举算法、统计直方图边界判定。**G2 + G5 要求 tuner 把 CBO 内部决策过程"白盒"展示给用户**，这是其他工具（包括商业工具）都做不好的差异化。

实现关键：
- 启用 OG 的 trace 能力：`SET log_statement_stats = on`、`SET enable_explain_pretty_print = on`、`SET pgxc_node_strategy = 'fdw'` 等开关
- 配合 `EXPLAIN (ANALYZE, BUFFERS, COSTS, TIMING, VERBOSE, FORMAT JSON, PLAN_TRACE TRUE)` 拿到完整决策链
- system prompt 注入 OG CBO 关键算法说明（基于 OG 源码 `optimizer/path/`）

#### G7 实现策略（千行 SQL）

千行 SQL 不能一次性塞 LLM 上下文，需要分段：

1. **解析 SQL AST** — 拆成 CTE / 主查询 / 子查询树
2. **EXPLAIN 整体一次** — 拿到完整 plan，标识 cost 最高的几个子树
3. **按 plan 节点 cost 80/20** — 选 cost 占比 > 10% 的节点对应的 SQL 段
4. **逐段 tuner** — 对每段 SQL 单独跑完整 7 机制
5. **整合回报告** — 总 SQL 角度看每段优化叠加后的总收益预估

这是 G7 的实现路径，**P5/P6 阶段重点攻坚**。

### 1.3 非目标（本期不做）

- 跨库支持（Oracle / MySQL / PG）— 未来填空，每库独立目录
- Anti-Pattern 规则引擎层（Mechanism 5）— v0.2 之后再加
- SQL Profile / Outline 持久化（OG 当前不支持）
- 自动应用方案（永远是给建议，用户决定执行）
- 千行 SQL 的"自动重写整段"（G7 给建议但不动整段）

---

## 2. 核心差异化：opendb tuner vs raw LLM

raw LLM = 文本推理；opendb tuner = 文本推理 + 真实数据 + 验证闭环 + 领域知识。

7 个落地机制（砍 Mechanism 5 anti-pattern）：

| # | 机制 | 解决的痛点 |
|---|------|----------|
| 1 | 真实 EXPLAIN ANALYZE 数据 | LLM 看猜的计划 → 看真实的计划 |
| 2 | Schema / Stats / Distribution 三件套 | LLM 不知道列基数和分布 |
| 3 | 迭代验证闭环 | LLM 一次性输出 → 多轮 explain 反馈 |
| 4 | 语义等价性验证 | LLM 重写错了不知道 |
| 6 | 历史经验注入（memory） | LLM 缺项目级背景 |
| 7 | 方言/版本知识 | LLM 给 PG 通用方案，OG 跑不通 |
| 8 | 运行时上下文（waits / locks） | LLM 把环境问题误判为 SQL 问题 |

### Mechanism 7 详解：方言/版本注入

启动 tuner 时一次性查询并注入 system prompt：

| 项 | 查询 | LLM 用来判断 |
|----|-----|------------|
| 版本 | `SELECT version()` | OG 5.0/5.1/6.0 算子集差异 |
| 扩展 | `SELECT extname FROM pg_extension` | pg_hint_plan / pg_stat_statements / pg_repack 是否可用 |
| 关键参数 | work_mem / shared_buffers / max_parallel_workers | hash join 大表是否会 spill |
| 存储类型 | `pg_class.relstorage` | 行存 / 列存差异 |
| 高可用 | `pg_stat_replication` | schema 变更是否要同步备库 |
| 分区 | `pg_partition` | 已分区表的分区策略 |
| OG 特有限制 | （硬编码）不支持 GIN / INCLUDE / BRIN，支持列存 + IUD RETURNING + 原生 HINT | LLM 给方案不会用不存在的特性 |

注入示例：

```
当前数据库环境:
- 产品: OpenGauss 5.0.0
- 已装扩展: dbe_perf, security_plugin
- 关键参数: work_mem=4MB, shared_buffers=1GB
- 高可用: 主备同步模式
- 不支持: GIN, INCLUDE 索引, BRIN
- 支持: 列存表, RETURNING, /*+ */ 原生 HINT

请根据以上边界给方案。
```

---

## 3. 架构（每库独立）

### 目录结构

```
internal/opengauss/sqltuner/
├── tuner.go                  # 主入口 + agent loop
├── plan_collector.go         # M1: EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS, VERBOSE)
├── schema_collector.go       # M2: pg_class + pg_stats + pg_index + pg_attribute
├── view_expander.go          # 视图展开（递归到底层表）
├── dialect_context.go        # M7: 版本 / 扩展 / 参数注入
├── runtime_context.go        # M8: pg_stat_activity + pg_locks 快照
├── memory_query.go           # M6: 查相关历史 memory
├── equiv_verifier.go         # M4: 重写后等价性抽样验证
├── prompt.go                 # system prompt 模板
└── tuner_test.go
```

未来 oracle/sqltuner/ mysql/sqltuner/ postgres/sqltuner/ 平行存在，**互不依赖**。

### Skill 接口

```go
// /sqltune <SQL> [--verify] [--max-rounds N]
type SQLTuneSkill struct {
    driver        db.Driver
    modelMgr      *model.Manager
    memoryStore   *memory.Store
    ...
}

func (s *SQLTuneSkill) Execute(ctx, params) (*Result, error)
```

输入：原始 SQL 文本
输出：markdown 报告（多方案 + 三件套 + EXPLAIN diff）

### Agent Loop（基于现有 engine）

复用 `internal/engine/`，跟 `/llm` 共享 turn 管理 + streaming + session save。tuner 是 engine 的另一个 caller，**不重写 engine**。

特殊化：
- 自定义 system prompt（`prompt.go`）
- 自定义 tool set（限制 LLM 只能调和调优相关的工具）
- 自定义初始 user message（含全部上下文：plan + schema + dialect + runtime）

---

## 4. Skill 接口详细定义

### Input

```yaml
command: /sqltune
args:
  sql: |
    SELECT count(*)
    FROM orders o
    JOIN users u ON o.user_id = u.id
    WHERE u.created_at > '2024-01-01'
      AND o.status IN ('paid', 'shipped')
  flags:
    --verify: 是否运行等价性验证（默认 true）
    --max-rounds: agent loop 最大轮数（默认 10）
    --dry-run: 只给方案不执行 EXPLAIN（默认 false）
    --simple: 跳过 M6/M8（默认 false，用于快速测试）
```

### Output 报告结构

```markdown
# SQL Tuning Report

## 1. 输入 SQL
[原 SQL]

## 2. 执行计划分析
[EXPLAIN ANALYZE tree, 标注问题节点]

## 3. 关键证据
| 证据 | 数据 | 来源 |
|------|------|------|
| 统计偏差 | est=100, actual=50000 | EXPLAIN |
| Hash 溢出 | disk batches=4, work_mem=4MB | EXPLAIN BUFFERS |
| 列基数 | u.email n_distinct=10万 | pg_stats |
| ... | ... | ... |

## 4. 优化方案（按预期收益排序）

### 方案 1: 重写为 EXISTS 子查询
**操作**:
```sql
-- 原 SQL
SELECT count(*) FROM orders o JOIN users u ...

-- 重写后
SELECT count(*) FROM orders o
WHERE EXISTS (SELECT 1 FROM users u WHERE u.id = o.user_id AND u.created_at > '2024-01-01')
  AND o.status IN ('paid', 'shipped');
```

**预期成本**: 12345 → 234（**52× 提升**）
**EXPLAIN 验证**: ✅ 已通过新计划成本验证
**等价性验证**: ✅ 抽样 1000 行结果一致
**⚠️ 风险**: 无（纯重写，不动 schema）
**📋 前置检查**: 无
**🔄 回滚**: 改回原 SQL

### 方案 2: 加 covering 索引
...

### 方案 3: 表分区（按 created_at 月份）
...

## 5. 已尝试但未采纳的方案
- 改成 LEFT JOIN：成本反而上升 → 丢弃
- 加索引 idx_orders_status_user：选择性不足 → 丢弃

## 6. 综合建议
**生产环境推荐顺序**: 方案 1（立即可执行）→ 方案 2（低峰期建索引）
```

---

## 5. Agent Loop 流程 — 自适应 2 轮 + 自动升级

### 设计原则

> 一切以最精确优化 SQL 为目标。简单 SQL 求快，复杂 SQL 求准。

默认走 **2 轮架构**追速度；若 Round 1 输出质量不达标自动升级到多轮深挖。**用户感知**：简单的几十秒，难的几分钟，质量都 ≥ 95%。

### 标准流程：Phase A + 2 轮 LLM

```
Phase A: 确定性收集 (0 轮 LLM, 5-30 秒)
  ├─ plan_collector.Collect(SQL)      → PlanTree (含 cost / est / actual / buffers)
  ├─ schema_collector.Collect(SQL)     → SchemaInfo (涉及表全部 schema + stats + 索引)
  ├─ view_expander.Expand(SQL)         → ExpandedSQL (视图替换底层 SQL)
  ├─ dialect_context.Snapshot()        → DialectInfo (M7)
  ├─ runtime_context.Snapshot()        → RuntimeInfo (M8)
  └─ memory_query.Find(tables)         → 历史 memory (M6)
  ⚠️ 全部并行执行, 总耗时由最慢的 EXPLAIN 决定

Round 1: Mega-analysis (1 次 LLM, 30-90 秒)
  System Prompt: tuner 专属 (M7 注入 + CBO 算法说明 + 强制多样化要求)
  User Message: Phase A 收集到的全部资料一次性发出
  LLM 输出: JSON 格式的多 candidate 方案集
    {
      "confidence": 0.85,
      "candidates": [
        {"id": 1, "type": "rewrite", "sql": "...", "rationale": "..."},
        {"id": 2, "type": "index",   "ddl": "...", "rationale": "..."},
        {"id": 3, "type": "hint",    "sql": "...", "rationale": "..."},
        {"id": 4, "type": "schema",  "ddl": "...", "rationale": "..."},
        {"id": 5, "type": "stats",   "sql": "...", "rationale": "..."}
      ],
      "explored_dimensions": ["rewrite", "index", "hint", "schema", "stats"],
      "uncertainty_notes": ["bench_mix_a 死锁机制需要更多 OG 内核知识..."]
    }
  约束: 必须给至少 4 个正交方案 (5 维度尽量覆盖)

Round 2: 并行验证 + 精修报告 (1 次 LLM, 30-60 秒)
  Tuner 并发执行 (goroutine, 全程 10-30 秒):
    - 对每个 candidate 跑 EXPLAIN (无 ANALYZE) 拿新 cost
    - 对 SQL 重写方案抽样跑等价性验证 (M4)
    - 收集结果矩阵 [cand_id, new_cost, equiv_check, error]
  发回 LLM:
    - 验证结果矩阵
    - "candidate 1 cost 12345→234 (52×) ✅, candidate 2 cost 12345→8000 ⚠️ 仅 1.5×, ..."
  LLM 输出: markdown 最终报告 (按收益排序 + 三件套 + 综合建议 + 弃案理由)
```

总耗时（标准 2 轮）：

| 阶段 | 耗时 |
|------|------|
| Phase A | 5-30s |
| Round 1 | 30-90s |
| 并行验证 | 10-30s |
| Round 2 | 30-60s |
| **合计** | **75s - 3.5 min** |

### 自动升级机制（关键能力）

Round 1 完成后，tuner 自检 **4 个信号**，命中任一 → 自动升级到多轮深挖：

| # | 升级信号 | 判定条件 | 含义 |
|---|---------|---------|------|
| ❶ | LLM 自报低置信 | `confidence < 0.7` | LLM 自己觉得没答好 |
| ❷ | 改善幅度不足 | 所有 candidate 经验证后 `min(new_cost/old_cost) > 0.5` | 没找到 ≥2× 改善方案 |
| ❸ | 探索方向单一 | `len(explored_dimensions) < 3` | LLM 只想到 1-2 类方案，没发散 |
| ❹ | SQL 复杂度超阈 | SQL > 500 行 OR 涉及表 > 20 OR plan 节点 > 100 | 一开始就直接走深度模式 |

升级触发后，进入 **深度模式**：

```
Round 3-N: 深度迭代 (3-15 次 LLM, 取决于复杂度)
  每轮:
    ① LLM 看 Round 1+前序轮次的所有 candidate + 验证结果, 反思 + 出新方案
    ② tuner 验证新方案 (EXPLAIN + 等价性)
    ③ 若 ≥1 候选改善 ≥ 5×, 收敛
    ④ 若 N >= max_rounds (默认 15), 强制收敛
  收敛标准 (任一满足即停):
    - 找到 ≥1 候选, 验证后 cost 降至原来 < 20%
    - 连续 3 轮无改善
    - 用户 max-rounds 上限到

Round Final: 精修报告 (1 次 LLM)
  同标准流程的 Round 2 输出格式
```

升级后总耗时矩阵：

| SQL 类型 | 自动模式行为 | 总耗时 | 质量 |
|---------|------------|-------|------|
| 简单 (< 50 行) | 永远 2 轮 | **30-90s** | 100% |
| 中等 (50-200) | 大概率 2 轮 | **1-2 min** | 95-97% |
| Q64 级 (100-500) | 2 轮 + 偶尔升级到 4 轮 | **2-4 min** | 92-97% |
| 千行 ETL (1000-3000) | 自动升级到 4-6 轮 | **5-8 min** | 95%+ |
| 极端 (3000+) | 自动升级到 8-12 轮 | **10-15 min** | 95%+ |

简单 SQL 比纯多轮快 **5×**，千行 SQL 也比纯多轮快 **30-50%**（Phase A 提前收齐 schema），质量保在 95% 以上。

### CLI flag

```bash
/sqltune <SQL>                  # 默认 自适应 2 轮 + 自动升级
/sqltune <SQL> --fast           # 强制 2 轮, 不升级 (最快, 风险自负)
/sqltune <SQL> --deep           # 强制深度模式 (生产关键 SQL)
/sqltune <SQL> --rounds 10      # 手动设上限
/sqltune <SQL> --no-verify      # 跳过等价性验证 (DML SQL 用)
```

### 强制多样化（让 2 轮的探索不输多轮）

Round 1 prompt 加硬约束：

```
你必须给出**互相正交**的至少 4 个候选方案, 覆盖以下维度 (至少命中 4 个):
  ❶ 纯 SQL 重写 (不动 schema, 不加 HINT)
  ❷ 索引调整 (新建 / 改 covering / 部分索引 / expression 索引)
  ❸ HINT 注入 (leading / scan / join 类型)
  ❹ 表结构改造 (分区 / 反规范化 / 类型修改)
  ❺ 统计信息修复 (扩展统计 / 直方图)

不要给同一思路的多个变体 (例如三个不同的 EXISTS 写法).
明确说明每个 candidate 处理哪个根因.
```

强制 LLM 在一轮内把"探索空间"打开，弥补 self-correction 缺失。

### Round 1 LLM 输出 Schema (JSON)

```go
type Round1Output struct {
    Confidence       float64       // 0-1, LLM 自评
    Candidates       []Candidate
    ExploredDims     []string      // ["rewrite","index","hint","schema","stats"]
    UncertaintyNotes []string      // LLM 觉得不确定的点
}

type Candidate struct {
    ID            int
    Type          string         // "rewrite" | "index" | "hint" | "schema" | "stats"
    SQL           string         // 完整可执行 SQL (rewrite/hint) 或 DDL (index/schema/stats)
    Rationale     string         // 为什么这个方案 (G2/G5 CBO 解读放这里)
    ExpectedGain  string         // LLM 估的预期收益
    RiskNotes     string         // 风险提示
    AppliesTo     []string       // ["bench_og_hot", "bench_mix_b"]
}
```

强制结构化输出避免 LLM 自由文本里漏字段。

### 等价性验证细则（M4）

仅对 `Type == "rewrite"` 的 candidate 跑：

```sql
-- 加 ORDER BY + LIMIT 让结果稳定
SELECT md5(string_agg(t::text, ',' ORDER BY ...))
FROM (<原 SQL ORDER BY ... LIMIT 1000>) t;

SELECT md5(string_agg(t::text, ',' ORDER BY ...))
FROM (<新 SQL ORDER BY ... LIMIT 1000>) t;

-- md5 一致 → 等价
```

⚠️ **限制**：
- 只对**只读 SQL**做（DML 不行 — 直接标 unverified）
- 必须能加稳定 ORDER BY（无序结果跳过）
- 抽样不代表全数据，标"抽样验证通过 ≠ 全量等价"
- 抽样 ≤ 1000 行避免大查询拖慢 tuner

---

## 6. 各 Mechanism 的具体实现要点

### M1: Plan Collector

#### 标准模式（< 500 行 SQL）

```sql
EXPLAIN (FORMAT JSON, ANALYZE TRUE, BUFFERS TRUE, VERBOSE TRUE, COSTS TRUE)
<user SQL>
```

⚠️ ANALYZE 会**实际运行 SQL**。对长 SQL 要加保护：
- 默认 timeout 30s（用 `SET statement_timeout`）
- DML SQL 强制走 `ROLLBACK` 包裹
- 用户 `--no-analyze` flag → 回退到无 ANALYZE 的 EXPLAIN

#### 千行 SQL 必须 `--no-analyze` 的根本原因

**EXPLAIN ANALYZE 真的会把 SQL 跑一遍**，对千行 SQL 来说意味着：

| 场景 | 真实耗时 |
|------|---------|
| TPC-DS Q64（10TB scale）| **30+ 分钟** |
| 真实 ETL（10 亿 × 5000 万 hash join）| **1-5 小时** |
| 多 CTE 报表（每段扫上亿行）| **几十分钟** |
| JOIN 顺序错的笛卡尔积 SQL | **几小时到几天**（就是要诊断的问题）|

**tuner 等不起这种耗时**。而且 `statement_timeout` 触发后 OG 直接报错，**不返回部分 plan**——要么跑完，要么啥都没有。

副作用：跑一次千行 SQL 可能 锁表 / 撑爆 work_mem / 撑爆 temp_tablespace / 占满 parallel workers / WAL 暴增。**线上 DBA 看到 tuner 把库搞挂，下次绝不再用**。

#### 不 ANALYZE 损失什么

| 数据 | 用途 | 没了影响 |
|------|-----|---------|
| `actual_rows` | G3 统计准确性最重要信号 | **估算 vs 实际偏差看不到** |
| `actual_time per node` | 算子级耗时 | 慢点定位粗化 |
| `buffers shared hit/read` | 缓存命中率 | 不知道 IO 比例 |
| `sort method` | memory / disk | 不知道是否溢出磁盘 |

**最大损失是 actual_rows** — G3 受冲击最大。

#### G3 三层兜底（补回 actual_rows 信号）

千行 SQL 不 ANALYZE，但 G3 仍能做。三层兜底：

**① pg_stat_statements 历史数据（首选）**

```sql
SELECT query, calls, mean_exec_time, total_exec_time, rows
FROM pg_stat_statements
WHERE query LIKE '%<table_name>%'
ORDER BY total_exec_time DESC
LIMIT 20;
```

历史上每条 SQL 跑过的真实行数 + 耗时。**不需要重跑 SQL**。前置条件：OG 装了 `pg_stat_statements`（多数生产环境装了）。

**② 子查询级局部 ANALYZE**

把千行 SQL 按 CTE 拆段，对**单个 CTE 单独 ANALYZE**：

```sql
-- 不 ANALYZE 整体 SQL
-- 但 ANALYZE 单个 CTE 体（这条快, 通常 5-30s）
EXPLAIN ANALYZE
SELECT ... FROM bench_mix_b WHERE uid BETWEEN ...;
```

整体 SQL 不跑。这是 G7 分段策略的副产品 — 单 CTE 通常可控。

**③ pg_stats 估算 + 数据漂移检测**

```
SQL 谓词命中 pg_stats.most_common_vals 的 top → 选择性高（行多）
SQL 谓词命中 histogram bound 之外      → 选择性极低（CBO 可能估错）
n_distinct vs reltuples 比例反推基数
```

纯估算，但比 raw LLM 的零信息强一万倍。

#### 默认策略（落地规则）

```
SQL 行数判断（plan_collector.go::Collect 入口）:
  < 100 行     → EXPLAIN ANALYZE 默认开, timeout 30s
  100-500 行   → EXPLAIN ANALYZE 默认开, timeout 60s, 失败 fallback EXPLAIN
  500-1000 行  → EXPLAIN 默认（无 ANALYZE）, G3 走 pg_stat_statements 兜底
  > 1000 行    → EXPLAIN 默认 + G3 三层兜底全开（pg_stat_statements + 拆 CTE + pg_stats）

用户 flag 覆盖:
  /sqltune --analyze        → 强制 ANALYZE（即使千行，DBA 自负责）
  /sqltune --no-analyze     → 强制不 ANALYZE
  /sqltune --analyze-cte    → 拆 CTE 单独 ANALYZE（G7 默认行为）
```

#### 输出 PlanNode tree 结构

输出结构化 PlanNode tree：

```go
type PlanNode struct {
    Operator     string  // "Seq Scan", "Hash Join", ...
    RelationName string
    Alias        string
    StartupCost  float64
    TotalCost    float64
    PlanRows     int64   // estimate
    PlanWidth    int
    ActualStartupTime float64  // ms
    ActualTotalTime   float64
    ActualRows        int64
    ActualLoops       int64
    SharedHitBlocks   int64
    SharedReadBlocks  int64
    Children     []*PlanNode
    Filter       string
    JoinFilter   string
    HashCondition string
    IndexCondition string
    SortKey      []string
    SortMethod   string  // "external merge" 表示溢出磁盘
    SortSpaceType string // "Memory" / "Disk"
    SortSpaceUsed int64
    Raw          map[string]any  // 原始 JSON 备查
}
```

### M2: Schema Collector

针对 SQL 涉及的所有表（通过解析 SQL FROM/JOIN 提取），**4 个查询并发执行**（兑现 §5 Phase A 的并行承诺）：

```go
func (s *SchemaCollector) Collect(ctx context.Context, tables []string) (*SchemaInfo, error) {
    info := &SchemaInfo{}
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return s.collectTableInfo(gctx, tables, &info.Tables) })
    g.Go(func() error { return s.collectIndexes(gctx, tables, &info.Indexes) })
    g.Go(func() error { return s.collectStats(gctx, tables, &info.Stats) })
    g.Go(func() error { return s.collectFKs(gctx, tables, &info.FKs) })
    if err := g.Wait(); err != nil {
        return nil, fmt.Errorf("schema collect: %w", err)
    }
    return info, nil
}
```

每个查询：

```sql
-- 表基本信息
SELECT relname, relpages, reltuples, relkind
FROM pg_class WHERE relname IN (...);

-- 索引
SELECT i.relname AS idx_name, ix.indisunique, ix.indisprimary,
       array_agg(att.attname ORDER BY ord) AS columns,
       pg_get_indexdef(ix.indexrelid) AS def
FROM pg_class t JOIN pg_index ix ON t.oid = ix.indrelid
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN unnest(ix.indkey) WITH ORDINALITY k(attnum, ord) ON true
JOIN pg_attribute att ON att.attrelid = t.oid AND att.attnum = k.attnum
WHERE t.relname IN (...)
GROUP BY 1,2,3,ix.indexrelid;

-- 列统计
SELECT tablename, attname, n_distinct, null_frac, avg_width,
       most_common_vals, most_common_freqs, correlation
FROM pg_stats WHERE tablename IN (...);

-- FK 约束（提示常用 JOIN 路径）
SELECT conrelid::regclass, conname, pg_get_constraintdef(oid)
FROM pg_constraint
WHERE contype = 'f' AND conrelid::regclass::text IN (...);
```

并行后总耗时 = max(4 个查询)，约 0.5-2 秒。串行的话 4× 慢。

### M3: 迭代验证闭环 — 批量并行版

§5 新架构的 Round 2 是**一次性并行验证所有 candidate**（不再每轮单跑），所以接口是批量的：

```go
type VerifyResult struct {
    CandID    int
    NewCost   float64
    OldCost   float64
    Speedup   float64       // OldCost / NewCost
    Error     error
    EquivOK   *bool         // nil = 未验证, true/false = 验证结果
}

func (t *Tuner) verifyCandidates(
    ctx context.Context,
    origSQL string,
    cands []Candidate,
) []VerifyResult {
    results := make([]VerifyResult, len(cands))
    sem := make(chan struct{}, 5)  // 并发上限 5
    var wg sync.WaitGroup
    for i, cand := range cands {
        wg.Add(1)
        sem <- struct{}{}
        go func(i int, c Candidate) {
            defer wg.Done()
            defer func() { <-sem }()
            cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
            defer cancel()
            results[i] = t.verifyOne(cctx, origSQL, c)
        }(i, cand)
    }
    wg.Wait()
    return results
}

func (t *Tuner) verifyOne(ctx context.Context, origSQL string, c Candidate) VerifyResult {
    // 对 rewrite/hint 类: EXPLAIN candidate.SQL
    // 对 index/schema/stats 类: 不能直接 EXPLAIN（DDL 没改），
    //   只能跟 LLM 一起 reason "如果加了这个索引 CBO 会用吗？"
    //   实施细节: P3 阶段 prototype 后定
    ...
}
```

反馈给 LLM（Round 2 输入）：

```
[Verifier 矩阵]
原 SQL 成本: 12345.67

| Cand | Type     | NewCost | Speedup | Equiv | Note          |
|------|----------|---------|---------|-------|---------------|
| 1    | rewrite  | 234.56  | 52×     | ✅    | 通过抽样验证  |
| 2    | index    | 567.89  | 21×     | n/a   | DDL 类未实跑  |
| 3    | hint     | 12300   | 1.0×    | ✅    | 几乎无改善    |
| 4    | schema   | n/a     | n/a     | n/a   | 分区改造需停表|
| 5    | stats    | 5670    | 2.2×    | n/a   | 修复 ANALYZE  |
```

**注意**：index / schema / stats 类方案**不能直接通过 EXPLAIN 验证收益**（DDL 没真改）。这类只能让 LLM 基于 schema 推理"如果改了 CBO 会怎么走"，准确度低于 rewrite/hint 类。P3 阶段需明确这个边界并标注给用户。

### M4: 等价性验证 — 稳定 md5 版

对 SQL 重写方案抽样验证（仅 `Type == "rewrite"` 的 candidate）：

```sql
-- 加 LIMIT + 强制稳定排序，对结果集做 md5 hash
WITH stable_orig AS (
  SELECT * FROM (<原 SQL>) t
  ORDER BY <主键列 / 全列> LIMIT 1000
),
stable_new AS (
  SELECT * FROM (<新 SQL>) t
  ORDER BY <同主键列 / 全列> LIMIT 1000
)
SELECT
  (SELECT md5(string_agg(row(t.*)::text, ',' ORDER BY <主键列>)) FROM stable_orig t) AS h_orig,
  (SELECT md5(string_agg(row(t.*)::text, ',' ORDER BY <主键列>)) FROM stable_new t)  AS h_new;

-- h_orig == h_new → 等价
```

**为什么用 md5 不用 EXCEPT**：
- EXCEPT 需要双向跑两次，慢且容易因 NULL 语义偏差误判
- md5 一次性出 hash，O(1) 比对
- string_agg 内 ORDER BY 强制顺序稳定（避免随机顺序导致误判不等价）

⚠️ 限制：
- 只对**只读 SQL**做（DML 强制标 unverified，不跑）
- 必须能加稳定 ORDER BY（首选主键，否则全列）
- 抽样 ≤ 1000 行（避免大查询拖慢 tuner）
- 浮点数精度差异：把浮点列 cast 成 numeric(10,2) 后再 hash
- NULL 语义：MD5(NULL) = NULL 会让整体 hash 变 NULL → string_agg 配 COALESCE 兜底

不通过 → 方案标"⚠️ 等价性未通过，仅供参考，需人工审核"。

### M6: Memory 注入

启动时调 memory store 拿历史调优经验：

```go
relevant := memoryStore.Find(memory.Query{
    Tables:  involvedTables,
    Tags:    []string{"sql_tune", "index", "schema_change"},
    MaxAge:  90 * 24 * time.Hour,
})
```

把 relevant memory 的 title + content 摘要拼进 user message。

⚠️ **API 依赖待确认**: 当前 `internal/engine/memory.Store` 的接口可能不支持 `Find(Query)` 这种结构化查询（需查代码）。P5 阶段开始前要：
1. **优先**：扩展 `memory.Store` 加 `Find(Query) []Entry` API（按 Tables / Tags / Time range 过滤）
2. **fallback**：如果 memory.Store 只支持全文检索，tuner 拼"涉及表名 OR 关键 tag"作为 query string 检索
3. **降级**：如果 memory store nil（用户没启用 memory），跳过 M6，不阻塞

memory entry 结构假设：
```yaml
type: solution|incident|preference|workload|pattern
title: "上次给 bench_og_hot 加 idx_uid 后查询从 500ms → 5ms"
content: "<完整 markdown>"
tables: [bench_og_hot]
tags: [sql_tune, index]
created_at: 2026-04-15
```

不一致就先扩展 memory.Store API。

### M7: 方言/版本（已详述）

启动时一次性查询，结果缓存到 `t.dialectInfo`，注入 system prompt 前缀。

### M8: 运行时上下文 — 带权限优雅降级

```sql
-- 当前等待事件分布
SELECT wait_event_type, wait_event, count(*)
FROM pg_stat_activity
WHERE state = 'active' AND pid != pg_backend_pid()
GROUP BY 1,2 ORDER BY 3 DESC;

-- 当前是否有针对涉及表的锁
SELECT * FROM pg_locks
WHERE relation IN (
  SELECT oid FROM pg_class WHERE relname IN (...)
);
```

让 LLM 在最终建议中分开判断"SQL 自身的问题" vs "环境的问题"。

⚠️ **权限优雅降级**：

OG 5.0 默认非超级用户**看不到其他会话的 query 文本** — `pg_stat_activity.query` 显示 `<insufficient privilege>`。我们的 OG 测试连接（`opendb` 用户）就是只读账号，会触发这个限制。

```go
func (r *RuntimeCollector) Snapshot(ctx context.Context) (*RuntimeInfo, error) {
    info := &RuntimeInfo{}

    // 尝试全字段查询
    err := r.driver.Query(ctx, fullStatActivitySQL, &info.Sessions)
    if err != nil || isInsufficientPrivilege(info.Sessions) {
        // 降级：只查 wait_event_type 聚合（不需要 query 文本权限）
        err = r.driver.Query(ctx, limitedStatActivitySQL, &info.Sessions)
        info.Degraded = true
    }

    // 锁信息：pg_locks 通常所有用户都能查
    _ = r.driver.Query(ctx, locksSQL, &info.Locks)

    return info, nil
}

func isInsufficientPrivilege(sessions []Session) bool {
    for _, s := range sessions {
        if strings.Contains(s.Query, "insufficient privilege") {
            return true
        }
    }
    return false
}
```

**降级后给 LLM 的提示**：

```
Runtime 上下文（部分降级 — 用户权限不足）:
- 等待事件分布: <可看到, 聚合数据>
- 当前 SQL 文本: 不可见（需要 monadmin 权限）
- 锁信息: <可看到>
```

LLM 看到 "Degraded" 标记会知道**不要假设有完整运行时信息**，避免基于不全数据下结论。

---

## 7. System Prompt 设计（关键）

prompt 是让 G2/G5 灵魂能力（CBO 解读 + PlanTrace 解读）真正落地的钥匙。**仅靠"你是 OG 调优专家"一句话，LLM 还是凭通用 PG 知识猜，差异化失效**。

完整 prompt 由 9 段构成（约 2500 字 / 3000 tokens）：

### 7.1 整体结构

```
[Section 1: 角色 + 能力边界]                  (~100 字)
[Section 2: 当前环境 M7 注入]                 (~200 字, 模板填充)
[Section 3: OG CBO 算法核心知识]              (~800 字, 硬编码)
[Section 4: PlanTrace 解读模板]               (~400 字)
[Section 5: 调优原则 5 条]                    (~150 字)
[Section 6: 强制多样化要求]                   (~200 字)
[Section 7: Round 1 输出 JSON schema]         (~300 字)
[Section 8: 禁用措辞 + 正反例]                (~200 字)
[Section 9: 最终报告 markdown 格式]            (~150 字)
```

### 7.2 Section 1: 角色 + 能力边界

```
你是 OpenGauss 5.0.0 SQL 调优专家。用户是专业 DBA。

你的输出会被直接用于生产环境，必须满足：
- 每个建议都引用具体的 EXPLAIN / pg_stats / 工具数据
- 每个 SQL / DDL 必须语法完整、可直接执行（OG 5.0 方言）
- 风险评估必须含三件套：⚠️ 风险 / 📋 前置检查 / 🔄 回滚方案
```

### 7.3 Section 2: 当前环境（M7 注入，运行时模板填充）

```
{dialectInfo block}

例:
当前数据库环境:
- 产品: OpenGauss 5.0.0
- 已装扩展: dbe_perf, security_plugin, pg_stat_statements
- 关键参数: work_mem=4MB, shared_buffers=1GB, max_parallel_workers_per_gather=2
- 高可用: 主备同步模式（schema 变更同步备库）
- 不支持: GIN, INCLUDE 索引, BRIN
- 支持: 列存表, RETURNING, /*+ */ 原生 HINT, pg_hint_plan 风格语法

请根据以上能力边界给方案。给出的特性必须确认 OG 5.0 支持。
```

### 7.4 Section 3: OG CBO 算法核心知识（G2/G5 灵魂）

```
# OG 5.0 CBO Cost 公式（用于 G2 解读）

total_cost = startup_cost + run_cost
  startup_cost = 启动时间（如 sort 必须收完所有行才出第一行）
  run_cost = (cpu_tuple_cost × rows)                       // 默认 0.01
           + (cpu_operator_cost × rows × N_predicates)     // 默认 0.0025
           + (seq_page_cost × pages)                       // Seq Scan, 默认 1.0
           + (random_page_cost × index_pages)              // Index Scan, 默认 4.0

# Join 算子选择决策

| 算子 | 适用条件 | cost 公式简化 |
|------|---------|--------------|
| Nested Loop | 内层 rows < 100 OR 内层有索引可走 | outer_rows × (inner_lookup_cost) |
| Hash Join | 两边都不太大, hash table 能装进 work_mem | build_outer + probe_inner |
| Merge Join | 两边已排序 OR sort 成本 < hash | sort_outer + sort_inner + merge |

# Join 顺序枚举

| from_collapse_limit | 算法 | 后果 |
|---------------------|------|------|
| ≤ 8 表 | DP 全排列 (2^N 复杂度) | 全局最优 |
| > 8 表 | GEQO 遗传算法 | 局部最优, 不稳定 |

调优要点: 复杂 SQL > 8 表 join 时, SET from_collapse_limit=20 让 DP 完整跑.

# 统计直方图选择性计算

WHERE col = X 的 selectivity:
  - 命中 most_common_vals: 用 most_common_freqs[X]
  - 否则: 1 / n_distinct

WHERE col > X 的 selectivity:
  - 用 histogram_bounds 二分查找定位 bucket

AND 多谓词:
  - 默认: selectivity_a × selectivity_b （独立性假设）
  - 假设错误时（关联列）→ 用 CREATE STATISTICS dependencies 修复

OR 多谓词:
  - 默认: 1 - (1-sel_a) × (1-sel_b)
  - 容易低估（认为大量重叠）

# 索引选择决策

CBO 走 Index Scan 的前提:
  1. 谓词命中索引前导列（或 expression 索引匹配函数）
  2. 估算行数 < cost_threshold（通常表的 10-30%）
  3. 谓词 sargable（无函数包裹、无类型转换）

CBO 不走索引的常见原因:
  - 谓词被 COALESCE / substr / col + 1 包裹 → 改写或建表达式索引
  - 估算行数过大 → 修统计 / 加 partial index
  - 索引列类型 vs 谓词字面量类型不匹配 → 隐式转换阻断
```

### 7.5 Section 4: PlanTrace 解读模板（G5 灵魂）

```
# 读 EXPLAIN 的标准流程

1. 找 cost 最高的节点（瓶颈算子）
2. 看 estimated_rows vs actual_rows:
   - 偏差 > 10× → 统计失真（G3 主因）
   - 偏差 < 2× → 统计 OK，看算子选错
3. 看算子类型:
   - Seq Scan on 大表 → 缺索引 OR 谓词不可 sargable
   - NL on 大表内层 → join 顺序错 OR 内层缺索引
   - Hash sort_method=external → work_mem 不足
   - Sort sort_method=external → work_mem 不足
4. 看 Filter 条件 vs Index Cond:
   - 应该走索引的谓词进了 Filter → sargable 问题
5. 反推 CBO 决策:
   - "如果选择性估算正确, CBO 还会选这个吗?"
   - "如果统计修复了, CBO 会切换到什么算子?"
   - "如果加了 hint X, plan 会变成什么样?"

# G5 输出标准 (cbo_analysis 字段)

不要只说"这个 plan 不好"。要说:
"CBO 在节点 #N 选了 Seq Scan 因为 estimated_rows=100 落在 cost 区间内,
 但 actual_rows=50000 显示统计严重失真。修复: CREATE STATISTICS 后,
 CBO 会重估 selectivity ≈ 5%, 切换到 Bitmap Index Scan."
```

### 7.6 Section 5: 调优原则

```
1. 用证据说话 — 每个结论引用具体的 EXPLAIN / pg_stats / 工具数据
2. 五维度方案 — SQL 重写 / HINT / 索引 / 表结构 / 统计修复
3. 按预期收益排序 — 收益不低于 30% 才提
4. 三件套强制 — 操作 / 风险 / 前置 / 回滚
5. 等价性硬约束 — SQL 重写未通过验证必须标注 unverified
```

### 7.7 Section 6: 强制多样化（Round 1 关键约束）

```
你必须给出**互相正交**的至少 4 个候选方案，覆盖以下维度（至少命中 4 个）:

  ❶ 纯 SQL 重写（不动 schema, 不加 HINT）
  ❷ 索引调整（新建 / 改 covering / 部分索引 / expression 索引）
  ❸ HINT 注入（leading / scan / join 类型 / set 参数）
  ❹ 表结构改造（分区 / 反规范化 / 类型修改 / 列存）
  ❺ 统计信息修复（扩展统计 / 直方图 / ANALYZE）

不要给同一思路的多个变体（例如三个不同的 EXISTS 写法）。
明确说明每个 candidate 处理哪个根因。
```

### 7.8 Section 7: Round 1 输出 JSON schema（严格）

````
你的输出必须是严格 JSON，不要在 JSON 外加任何文字、不要包 markdown 代码块。

```json
{
  "confidence": <0.0-1.0>,
  "cbo_analysis": "<G2/G5: 一段话解释为什么 CBO 选了当前 plan, 引用 cost 数字 + estimated/actual rows>",
  "candidates": [
    {
      "id": 1,
      "type": "rewrite|index|hint|schema|stats",
      "sql": "<完整可执行 SQL 或 DDL>",
      "rationale": "<为什么这个方案 — 必须引用 plan 数据>",
      "expected_gain": "<预估收益 e.g. 20×, 50%>",
      "applies_to": ["<table_a>", "<table_b>"],
      "risk_level": "low|medium|high"
    }
  ],
  "explored_dimensions": ["rewrite","index","hint","schema","stats"],
  "uncertainty_notes": ["<不确定的点>"]
}
```

至少 4 个 candidate, 5 维度尽量覆盖。
````

### 7.9 Section 8: 禁用措辞 + 正反例

```
# 禁用措辞（出现这些直接判失败 — borrowed from /llm prompt）

❌ "本次分析仅基于 X 工具，如需更精准请..."
❌ "建议补充查询 Y"
❌ "可能需要更多上下文"
❌ "这取决于业务场景"
❌ "理论上 / 一般来说 / 通常"

# 反例 vs 正例

❌ 反例（含糊）:
   "Hash join 内存不够，可能溢出磁盘，建议增加 work_mem"

✅ 正例（带证据 + 具体数字）:
   "Hash 节点 #5 显示 sort_method=external batches=4，
    当前 work_mem=4MB，hash table 估算需要 16MB.
    SET work_mem='64MB'; 后该节点会切换到内存 hash, cost 从 8500 → 1200 (7×)."

❌ 反例（泛泛建议）:
   "建议加索引优化查询"

✅ 正例（具体 DDL + 选择性论证）:
   "bench_mix_b.uid 列 n_distinct=100万, 谓词命中 ~1000 行, selectivity=0.001,
    建议: CREATE INDEX CONCURRENTLY idx_bench_mix_b_uid ON bench_mix_b(uid);
    EXPLAIN 验证: cost 12345 → 234 (52×)."
```

### 7.10 Section 9: 最终报告 markdown 格式

```
Round 2 输出回归 markdown，结构见 §4 报告输出格式。
```

### 7.11 Prompt 总长度预算

| Section | 字数 | tokens 估 |
|---------|------|-----------|
| 1-2 角色 + 环境 | 300 | ~400 |
| 3 CBO 知识 | 800 | ~1100 |
| 4 PlanTrace 模板 | 400 | ~550 |
| 5-6 原则 + 多样化 | 350 | ~480 |
| 7 JSON schema | 300 | ~410 |
| 8 禁用措辞 + 例 | 200 | ~280 |
| 9 报告格式 | 150 | ~200 |
| **总计** | **2500** | **~3400** |

加上 M7 dynamic 部分约 200 tokens，**总 prompt ~3600 tokens**。在 Phase A 收集到的 user message（含 plan + schema + dialect + runtime + memory，约 30K-100K tokens）旁边占比 < 10%，合理。

### 7.12 P1 阶段交付的最小 prompt

P1 时间紧，先实现 Sections 1/2/5/9 + 简化版 Section 3（只保留 Cost 公式 + Join 决策表），约 1000 字。**P3 阶段补全 Sections 4/6/7/8**，达到完整版。

---

## 8. 6 个金标准 SQL 案例（P1-P5 测试集）

完整可重现案例（含 DDL + 数据生成 + 期望产出）见 [`golden-standard-cases.md`](./golden-standard-cases.md)。

7 项能力目标覆盖矩阵：

| 案例 | G1 索引 | G2 CBO 解读 | G3 统计 | G4 重写 | G5 PlanTrace | G6 HINT | G7 千行 |
|------|--------|------------|--------|--------|--------------|--------|---------|
| 1. 多表 JOIN 错误 join order | ✓ | ✓ | ✓ |  | ✓ | ✓ |  |
| 2. 三层嵌套子查询 → semi join |  | ✓ |  | ✓ |  |  |  |
| 3. 关联列统计偏差 | ✓ |  | ✓ |  | ✓ |  |  |
| 4. 函数包裹非 sargable 谓词 | ✓ |  |  | ✓ |  |  |  |
| 5. TPC-DS Q64 改造（百行级）|  | ✓ | ✓ | ✓ |  | ✓ | （部分） |
| **6. 真千行级财务报表 ETL** | ✓ | ✓ | ✓ | ✓ | ✓ | ✓ | **✓** |

每个目标至少 2 个案例覆盖。**G7（千行 SQL）专属案例 6 覆盖**，案例 5 作为"百行级"过渡测试。

### 案例分期使用

| 阶段 | 用哪些案例 | 目的 |
|------|----------|------|
| P1（skill 骨架） | 案例 1, 4 | 简单流程跑通 |
| P2（Round 1+2） | 案例 1-4 全部 | 验证 2 轮架构端到端 |
| P3（自动升级） | 案例 5 | 验证升级触发 + 百行级处理 |
| P4（千行专项） | **案例 6** | 验证 G7 分段 + token 压缩 + 多轮深度模式 |
| P5（综合 benchmark） | 全部 6 个 | 发版前的回归 |

### 案例 6 概要（详细见 golden-standard-cases.md §6）

**业务场景**: 零售企业年终财务报表 ETL — 跨年同店销售对比 + 客户分群 + 区域汇总 + 商品类目排名。

**SQL 复杂度**:
- ~1500 行
- 12 个 CTE (含 5 个引用其他 CTE 的嵌套 CTE)
- 联合 5 个地区 UNION ALL (每段 ~250 行)
- 30+ 表参与
- 6 个窗口函数 (RANK / ROW_NUMBER / LAG / SUM OVER)
- 4 层视图嵌套 (业务封装的 v_dim_customer / v_fact_sales_enriched 等)
- COALESCE / CASE WHEN 大量

**来源**: 综合自 TPC-DS Q11 + Q14 + Q23 + Q72 + Q78（这 5 个都是 TPC-DS 里相对长的查询），改造为单一 ETL 报表形态。完整 SQL 由 design doc 实施时手工拼接（约 1500 行），保留每段子查询的真实复杂度。

**期望 tuner 行为**（验证 G7 分段策略）:
1. 自动检测 SQL > 1000 行 → 触发 G7 千行模式
2. AST 拆段识别 12 个 CTE 边界
3. EXPLAIN（不 ANALYZE）拿全局 plan
4. 80/20 找出 cost 占比 > 10% 的 4-5 个热点 CTE
5. 对每个热点 CTE 单独深挖（局部 ANALYZE 可选）
6. 整合方案，给出每段优化的预估收益 + 整体改造收益
7. token 压缩验证：最终塞 LLM 的 prompt < 100K tokens（即使 SQL 1500 行）

**关键测试指标**:
- 总耗时 ≤ 8 min（千行档目标）
- 找出真问题数 ≥ 4 类（缺索引 / 统计偏差 / CTE 重复扫描 / join 顺序）
- 至少 1 个方案让整体 cost 降至 < 30%
- LLM prompt token < 100K（验证 token 压缩生效）

### 来源

- 案例 1/2: 综合自 PostgreSQL 文档 + 电商常见模式
- 案例 3/4: 直接复刻 dev.to 真实生产 bug ([When ANALYZE Isn't Enough](https://dev.to/michal_cyncynatus_3a792c2/when-analyze-isnt-enough-debugging-bad-row-estimation-in-postgresql-47n6))
- 案例 5: TPC-DS Query 64
- 案例 6: TPC-DS Q11/14/23/72/78 拼合，模拟真实零售 ETL 形态

---

## 9. 分期实施计划

按 "**自适应 2 轮 + 自动升级**" 架构，分期更紧凑：

### MVP (P1-P3): ~3 周

| 期 | 周 | 内容 | DoD |
|----|----|------|------|
| P1 | 1 | Phase A 全部模块 (plan/schema/dialect/runtime/memory 收集) + skill 骨架 + 强制多样化 prompt | `/sqltune <简单 SQL>` Phase A 跑通, dump 收集到的全部资料 |
| P2 | 1 | Round 1 (Mega-analysis) + Round 2 (并行验证) + JSON 输出 schema + markdown 报告渲染 | `/sqltune <SQL>` 完整 2 轮跑通, 案例 1/3/4 (简单+中等) 验证通过 |
| P3 | 1 | M4 等价性验证 + view_expander + 自动升级机制（4 个升级信号） | 案例 2 (嵌套子查询) + 案例 5 (TPC-DS Q64) 验证通过, 升级机制自动触发 |

**MVP 交付**: `/sqltune <SQL>` 自适应 2 轮 + 升级到多轮的完整闭环，5 个金标准案例全部跑通，简单 SQL 30-90 秒，复杂 100+ 行 2-4 分钟。

### 增强 (P4-P5): ~2-3 周

| 期 | 周 | 内容 |
|----|----|------|
| P4 | 1-2 | G7 千行 SQL 专项：token 智能压缩（plan tree 折叠 + schema 分级 + CTE 去重）+ 强制多轮深度模式 + benchmark 千行 ETL | 千行 SQL 5-8 min 跑通，质量 ≥ 95% |
| P5 | 1 | UX 打磨 + benchmark 报告 + 文档 + 集成 `/llm` cross-link | 发版 v1.0 |

### 总工程量: ~5-6 周（OG 单库）

比原 6-8 周缩短 25%，关键在于 2 轮架构让 Round 1+2 是同一个流程的两端，**P2 一周内就能交付端到端的可用版本**，剩下时间打磨深度模式和千行 SQL 优化。

---

## 10. 风险与缓解

| 风险 | 缓解 |
|------|------|
| EXPLAIN ANALYZE 跑长 SQL 阻塞业务 | timeout 30s + DML 强制 ROLLBACK + `--no-analyze` flag + 千行 SQL 默认只用 EXPLAIN（无 ANALYZE） |
| LLM 重写出语义错误 SQL | M4 等价性验证 + 标注 unverified |
| LLM 给出 OG 不支持的特性 | M7 dialect 注入硬约束 |
| 方案太多用户 overwhelm | 收益 <30% 不提；最多输出 5 个方案 |
| Schema 信息泄漏（API key 在 SQL 里）| collector 不传 SQL 原文给 LLM 时，先 mask 字面量 |
| 错误建议被生产采纳 | 永远是建议，不自动执行；三件套 + 风险等级标记 |
| **2 轮模式对最复杂 SQL 质量打折** | 4 信号自动升级 + `--deep` 强制多轮 + 强制多样化 prompt |
| **2 轮模式 token 成本高** （1M context 模型贵）| Phase A 智能压缩 + plan tree 折叠 + schema 分级；可用 Kimi K2.6 (256K) / Opus 1M / Gemini 2M 按预算选 |
| **2 轮模式对低 confidence 模型不适用** | 配置时检测 active model context window；< 200K 自动 fallback 到分段多轮模式 |
| **Round 1 LLM JSON 输出格式错** | JSON schema 校验 + 失败重试 + fallback 到自由文本解析 |
| **并行 EXPLAIN 拖慢 OG 实例** | 限制并发数 ≤ 5，每个 EXPLAIN 单独 30s timeout |

---

## 11. 与现有功能的关系

- **/llm 诊断流程**: 当 /llm 识别出"主要问题是慢 SQL X"时，输出末尾建议"用 /sqltune <X> 深挖"。**不自动跳转**。
- **/explain 现有 skill**: tuner 内部用，对外仍保留 /explain 给用户手敲。
- **SQL Advisor v0.9.18**: 现有的 25 条规则保留作 Anti-Pattern 起点（v0.2 加 Mechanism 5 时复用），**本期不动**。
- **Memory 系统**: tuner 启动时只读 query，不写。等用户确认采纳方案后，可手动 `/memory write` 记录"调过这个 SQL"。

---

## 12. 待 review 清单（交付前需要确认）

- [x] 5 个金标准 SQL 的具体内容 → 已落地 `golden-standard-cases.md`
- [ ] **Round 1 system prompt 的强制多样化措辞**（5 维度正交约束怎么写最有效）
- [ ] **4 个自动升级信号的具体阈值**：
  - confidence 阈值 0.7 是否合理？
  - cost ratio 0.5（即至少 2× 改善）是否过严？
  - dimension 数量 < 3 算"探索单一"是否合理？
  - SQL 行数 / 表数 / plan 节点数的"复杂度阈值"具体设多少？
- [ ] PlanNode 数据结构是否需要扩字段（OG 5.0 特有的算子如 `Vector ...` 列存算子）
- [ ] 等价性验证的"稳定性"判定（哪些 SQL 抽样可信，ORDER BY 加在哪里）
- [ ] **千行 SQL token 压缩策略具体规则**（plan tree 折叠阈值 5%？schema 分级哪些是 hot）
- [ ] **G3 三层兜底优先级与组合策略**（pg_stat_statements 不可用时是否强制走拆 CTE 局部 ANALYZE，会否再次拖累 OG）
- [ ] **行数判断阈值的具体数字**（100/500/1000 是经验值，需在金标准案例上验证）
- [ ] 报告默认是否 markdown，是否同时支持 JSON 输出（给 CI）
- [ ] **Round 1 LLM JSON 输出 schema 校验失败时的 fallback 策略**（重试？降级？）

---

## 13. 后续路线（v0.2+，不在本期）

- [ ] Mechanism 5 anti-pattern rule layer（复用现有 ruleengine 框架）
- [ ] 跨库扩展（Oracle / MySQL / PG sqltuner，每库独立目录）
- [ ] 持久化 SQL profile / SQL outline（OG 版本支持后）
- [ ] 自动周期 tuning（scheduler 定期扫慢 SQL 自动给方案）
- [ ] 集成 Sentinel：检测到 SQL 异常 → 自动触发 tuner 给方案
