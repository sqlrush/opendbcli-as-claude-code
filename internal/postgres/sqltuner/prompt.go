/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  pgPromptBuilder provides PostgreSQL-specific knowledge sections
 *	  for the neutral GenericTuner.
 *
 *	  Heavily annotated with PG-specific caveats: no rejected-paths
 *	  dump (the structural CBO short-coming), pg_stats reading patterns,
 *	  GUC names + defaults, hint plan via pg_hint_plan extension.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/sqltuner/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import "github.com/sqlrush/opendb/internal/sqltune"

type pgPromptBuilder struct{}

// NewPromptBuilder returns a PostgreSQL prompt builder.
func NewPromptBuilder() sqltune.PromptBuilder { return &pgPromptBuilder{} }

func (pgPromptBuilder) RoleTag() string { return "PostgreSQL 14+ SQL 调优专家" }

func (pgPromptBuilder) CBOKnowledge() string {
	return `## Cost 公式
total_cost = startup_cost + run_cost
  startup_cost = 启动时间（如 sort 必须收完所有行才出第一行）
  run_cost     = (cpu_tuple_cost × rows)                       // 默认 0.01
               + (cpu_operator_cost × rows × N_predicates)     // 默认 0.0025
               + (seq_page_cost × pages)                       // Seq Scan, 默认 1.0
               + (random_page_cost × index_pages)              // Index Scan, 默认 4.0

random_page_cost 关键：SSD 推荐 1.1（vs 默认 4.0 是机械盘假设），降低后 CBO 会更愿意走索引。

## Join 算子选择
| 算子 | 适用条件 | cost 公式简化 |
|------|----------|----------------|
| Nested Loop | 内层 rows < 100 OR 内层有索引 | outer × inner_lookup |
| Hash Join | 两边都不太大，hash 装得进 work_mem | build + probe |
| Merge Join | 两边已排序 OR sort < hash | sort_o + sort_i + merge |

## Join 顺序枚举
| from_collapse_limit | 算法 | 后果 |
|---------------------|------|------|
| ≤ 8 表 | DP 全排列 (2^N) | 全局最优 |
| > 8 表 | GEQO 遗传算法 | 局部最优，不稳定 |
复杂 SQL > 8 表时 SET from_collapse_limit=20 让 DP 完整跑。

## 选择性计算
WHERE col = X:
  - 命中 most_common_vals → most_common_freqs[X]
  - 否则 → 1 / n_distinct
WHERE col > X:
  - 用 histogram_bounds 二分定位 bucket
AND 多谓词:
  - 默认 sel_a × sel_b （独立性假设）
  - 关联列假设错误 → CREATE STATISTICS dependencies 修复

## ⚠️ PG 结构性短板
**PG 不 dump rejected paths** — 你看不到 planner 考虑过哪些其他 plan、为什么 reject 了。
opendb 已采集 pg_stats 旁路（n_distinct / null_frac / correlation / most_common_vals）
让你能推断 CBO 决策依据。**如发现 actual_rows vs plan_rows 严重偏差，必看 pg_stats**。`
}

func (pgPromptBuilder) PlanReading() string {
	return `1. 找 cost 最高的节点（瓶颈算子）
2. 看 plan_rows vs actual_rows:
   - 偏差 > 10× → 统计失真，ANALYZE 或 CREATE STATISTICS
   - 偏差 < 2× → 统计 OK，看算子选错
3. 看算子类型：
   - Seq Scan on 大表 → 缺索引 OR 谓词不可 sargable
   - Nested Loop 内层是 Seq Scan 大表 → join 顺序错 OR 内层缺索引
   - Hash 节点 sort_method=external → work_mem 不足
   - Sort 节点 sort_method=external → work_mem 不足
4. 看 Filter 条件 vs Index Cond:
   - 应走索引的谓词进了 Filter → sargable 问题（函数包裹/类型不匹配）
5. 看 BUFFERS（shared_hit / shared_read）：
   - shared_read 高 → 缺索引或 effective_cache_size 设小了
6. 反推 CBO 决策（PG 无 trace, 只能逻辑推理）：
   - "如果 selectivity 估算正确, CBO 还会选这个吗？"
   - "如果 work_mem 够大, Hash 节点不溢出, plan 还会变吗？"`
}

func (pgPromptBuilder) HintSyntax() string {
	return `PostgreSQL **原生不支持 hint**。两个路径：

1. **pg_hint_plan 扩展**（最常用）— 安装后用注释式：
   /*+ HashJoin(t1 t2) */
   /*+ Leading(t1 t2 t3) */            -- 固定 join 顺序
   /*+ IndexScan(t idx_name) */
   /*+ SeqScan(t) */
   /*+ Set(work_mem '256MB') */        -- 临时改 GUC

2. **会话级 GUC 调整**（无扩展时唯一手段）：
   SET enable_seqscan = off;           -- 强制不走 seq scan
   SET enable_nestloop = off;          -- 强制不走 nested loop
   SET work_mem = '256MB';
   SET random_page_cost = 1.1;         -- SSD 调优

不要 propose Oracle/MySQL 风格的 /*+ INDEX(...) */ — PG 不识别会被当 comment 忽略。`
}
