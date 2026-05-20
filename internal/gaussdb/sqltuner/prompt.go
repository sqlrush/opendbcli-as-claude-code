/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  gaussdbPromptBuilder = PG-family content (binary-compatible with
 *	  openGauss) + the GS_PLAN_TRACE differentiator.
 *
 *	  Most CBO knowledge is identical to PG/og; we only add the
 *	  GaussDB-specific note about GS_PLAN_TRACE availability so the
 *	  LLM knows it can rely on real CBO decision dumps (unlike open
 *	  source PG/og which lack this).
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/gaussdb/sqltuner/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import "github.com/sqlrush/opendb/internal/sqltune"

type gaussdbPromptBuilder struct{}

// NewPromptBuilder returns a GaussDB prompt builder.
func NewPromptBuilder() sqltune.PromptBuilder { return &gaussdbPromptBuilder{} }

func (gaussdbPromptBuilder) RoleTag() string { return "GaussDB(for openGauss) 2.0+ SQL 调优专家" }

func (gaussdbPromptBuilder) CBOKnowledge() string {
	return `## Cost 公式（PG-family compatible）
total_cost = startup_cost + run_cost
  startup_cost = 启动时间
  run_cost     = (cpu_tuple_cost × rows) + (cpu_operator_cost × rows × N_predicates)
               + (seq_page_cost × pages) + (random_page_cost × index_pages)

## Join 算子选择
| 算子 | 适用条件 | cost 公式 |
|------|----------|----------|
| Nested Loop | 内层 rows < 100 OR 内层有索引 | outer × inner_lookup |
| Hash Join | 两边都不太大，hash 装得进 work_mem | build + probe |
| Merge Join | 两边已排序 | sort_o + sort_i + merge |

## 选择性计算
WHERE col = X:
  - 命中 most_common_vals → most_common_freqs[X]
  - 否则 → 1 / n_distinct
WHERE col > X:
  - 用 histogram_bounds 二分定位 bucket

## ⭐ GS_PLAN_TRACE（GaussDB 独有）
GaussDB Centralized 有 **GS_PLAN_TRACE** 系统表（其他 PG 家族无此能力）：
plan_trace 列上限 300 MB，dump CBO 完整决策过程 —— 类似 Oracle 10053 但走 SQL 直读。

opendb 已自动采集到 cc.Trace（如 plan_trace 已被 DBA 启用且当前会话有 sysadmin 权限）。
**这是 PG 家族里唯一能告诉你"为什么没选别的 plan"的数据**。
读取方法：
  SELECT plan, plan_trace FROM gs_plan_trace ORDER BY modifydate DESC LIMIT 1;

## EXPLAIN PERFORMANCE（执行画像）
GaussDB 同时支持 EXPLAIN PERFORMANCE，11 列 per-operator detail：
A-time / A-rows / E-rows / E-distinct / Peak Memory / E-memory / E-costs。
opendb 也已采集。比对 A-rows vs E-rows 推断 stats 失真。`
}

func (gaussdbPromptBuilder) PlanReading() string {
	return `1. 找 cost 最高的算子（瓶颈）
2. 看 plan_rows vs actual_rows（来自 EXPLAIN PERFORMANCE 的 A-rows / E-rows）:
   - 偏差 > 10× → 统计失真，DBE_PERF.GATHER_TABLE_STATS / VACUUM ANALYZE
   - 偏差 < 2× → 看算子选错
3. 看算子类型：
   - Seq Scan on 大表 → 缺索引
   - Hash 节点 Peak Memory > E-memory → 溢出磁盘，调 work_mem
4. ⭐ 读 GS_PLAN_TRACE（如可用，opendb 自动提供）:
   - 找 "Path candidate" 列出 CBO 考虑过的 plan
   - 找 "Selected path" 看最终选择
   - 找 "Rejected: reason..." 看为什么没选 X
5. EXPLAIN PERFORMANCE 的算子级 detail:
   - Stream operator → 分布式场景才有（centralized 无）
   - Peak Memory 列：节点真实内存峰值，对比 E-memory 看估算偏差`
}

func (gaussdbPromptBuilder) HintSyntax() string {
	return `GaussDB 兼容 og hint 语法，类 PG 风格：

  /*+ HashJoin(t1 t2) */
  /*+ NestLoop(t1 t2) */
  /*+ Leading((t1 t2 t3)) */          -- 固定 join 顺序
  /*+ IndexScan(t idx_name) */
  /*+ SeqScan(t) */
  /*+ Rows(t #100) */                 -- 强制估算行数
  /*+ Set(work_mem '256MB') */        -- 临时改 GUC

会话级 GUC 调整：
  SET enable_seqscan = off;
  SET work_mem = '256MB';
  SET random_page_cost = 1.1;

部分功能仅集中式可用，分布式（DWS）需额外考虑 distribute 与 redistribute 算子。`
}
