/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  mysqlPromptBuilder provides MySQL-specific knowledge sections for
 *	  the neutral GenericTuner's system prompt assembly.
 *
 *	  Knowledge sourced from MySQL 8.0 documentation: cost model paper
 *	  (Marcin Konecki et al.), optimizer_switch reference, hint syntax
 *	  (MySQL Reference Manual chapter 8.9), optimizer_trace JSON
 *	  structure.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/sqltuner/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import "github.com/sqlrush/opendb/internal/sqltune"

// mysqlPromptBuilder implements sqltune.PromptBuilder.
type mysqlPromptBuilder struct{}

// NewPromptBuilder returns a MySQL prompt builder.
func NewPromptBuilder() sqltune.PromptBuilder { return &mysqlPromptBuilder{} }

func (mysqlPromptBuilder) RoleTag() string { return "MySQL 8.0 SQL 调优专家" }

func (mysqlPromptBuilder) CBOKnowledge() string {
	return `## Cost 模型
MySQL 8.0 cost 计算公式：
  total_cost = read_cost + eval_cost
  read_cost  = io_block_read_cost × pages_to_read
  eval_cost  = row_evaluate_cost × rows_examined_per_scan
默认参数: io_block_read_cost=1.0, row_evaluate_cost=0.2

## Join 算子选择
| 算子 | 适用条件 | 备注 |
|------|----------|------|
| Nested Loop | 内表小 OR 内表有索引 | MySQL 默认；BNL=Block Nested Loop 用 join_buffer_size |
| Hash Join | 8.0.18+ EQUI JOIN 无索引可用时 | optimizer_switch='hash_join=on' 才启用 |
| Sort Merge | MySQL 不支持 — 无 Merge Join |

## 索引选择决策
CBO 走 Index Scan 的前提:
  1. WHERE 命中索引前导列（最左前缀）
  2. 估算行数 < 表总行数的 30%（rough rule）
  3. 谓词 sargable — 无函数包裹列

CBO 不走索引常见原因:
  - 列被函数包裹 (DATE_FORMAT(col)) → 改写或建函数索引 (8.0.13+)
  - 隐式类型转换 (col 是 INT，谓词是 '10') → 字面量类型对齐
  - 隐式字符集转换（latin1 col vs utf8mb4 谓词）
  - 估算行数过大 → ANALYZE TABLE 或 histogram_set

## 关键 optimizer_switch 标志
- block_nested_loop: BNL 是否启用
- batched_key_access: BKA 是否启用（需 hint 显式触发）
- index_merge: 多索引合并扫描
- index_condition_pushdown: ICP 把 WHERE 推到存储引擎
- semijoin / loosescan / firstmatch / materialization: 子查询展开策略
- hash_join: 8.0.18+ Hash Join

## optimizer_trace（CBO 决策完整 dump）
SET SESSION optimizer_trace="enabled=on" 后跑 SQL，
SELECT TRACE FROM information_schema.OPTIMIZER_TRACE
拿到 JSON：含 join_preparation / join_optimization / join_execution 三段，
每段含 rows_estimation / considered_execution_plans / cost_for_plan。
**这是 MySQL 唯一的 CBO 决策完整 dump**，opendb 已自动采集到 cc.Trace。`
}

func (mysqlPromptBuilder) PlanReading() string {
	return `1. 找 cost 最高的 table 节点（瓶颈算子）
2. 看 rows_examined_per_scan vs rows_produced_per_join：
   - 偏差 > 10× → 统计失真（推 ANALYZE TABLE 或 histogram_set）
   - 偏差 < 2× → 统计 OK，看 access_type 选错
3. 看 access_type：
   - ALL（Full Table Scan）on 大表 → 缺索引 OR 谓词不可 sargable
   - index → 全索引扫描，可能缺更窄的索引
   - range → 范围扫，看 used_key_parts 是否充分
   - ref/eq_ref → 等值索引查（最佳）
4. 看 Extra 字段：
   - "Using filesort" → ORDER BY 无索引支持，考虑 covering index
   - "Using temporary" → GROUP BY/DISTINCT 触发临时表，可能 work_mem 不足
   - "Using where" + "Using index condition" → ICP 启用（好）
   - "Using join buffer (Block Nested Loop)" → 缺连接索引
5. optimizer_trace 中比对 considered_execution_plans：
   - rows_estimation 错 → 统计问题
   - cost_for_plan 大但被选 → 其他候选 cost 更大，看是否能开启 hash_join`
}

func (mysqlPromptBuilder) HintSyntax() string {
	return `MySQL 8.0 hint 语法（注释式）：
  /*+ HASH_JOIN(t1, t2) */            -- 强制 hash join
  /*+ BNL(t1, t2) */                  -- 强制 block nested loop
  /*+ NO_BNL(t1) */
  /*+ INDEX(t idx_name) */            -- 强制走指定索引
  /*+ NO_INDEX(t idx_name) */
  /*+ JOIN_ORDER(t1, t2, t3) */       -- 固定 join 顺序
  /*+ SET_VAR(optimizer_switch='hash_join=on') */  -- 临时改 GUC

旧式 SELECT 后 hint（推荐改用新式注释）：
  SELECT /*+ USE_INDEX(t idx_name) */ ... FROM t
  SELECT STRAIGHT_JOIN ... FROM t1, t2  -- 老 hint 强制 t1 在前

注意：MySQL hint 仅在该 session 该 query 生效，不全局，不持久。`
}
