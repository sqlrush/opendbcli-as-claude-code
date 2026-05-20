/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  oraclePromptBuilder provides Oracle-specific knowledge sections
 *	  for the neutral GenericTuner.
 *
 *	  Oracle CBO algorithm differs significantly from PG-family:
 *	  bind peeking, adaptive cursor sharing, SQL Plan Management,
 *	  hash join with broadcast/replicated, RAC parallelism. We focus
 *	  on the single-instance non-PARALLEL knowledge for most opendb
 *	  use cases.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sqltuner/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import "github.com/sqlrush/opendb/internal/sqltune"

type oraclePromptBuilder struct{}

// NewPromptBuilder returns an Oracle prompt builder.
func NewPromptBuilder() sqltune.PromptBuilder { return &oraclePromptBuilder{} }

func (oraclePromptBuilder) RoleTag() string { return "Oracle 19c SQL 调优专家" }

func (oraclePromptBuilder) CBOKnowledge() string {
	return `## Cost 公式（IO + CPU 加权）
cost = io_cost + cpu_cost / cpu_speed
  io_cost  = blocks_read × 1.0
  cpu_cost = cpu_ops × cpu_cost_per_op (默认很小，CPU 通常不主导)

## CBO 决策依赖的关键参数
- optimizer_mode: ALL_ROWS (默认, 整体 throughput) / FIRST_ROWS (首批) / FIRST_ROWS_n
- optimizer_features_enable: 启用版本特性，可降级保险（如 11.2.0.4 让旧应用稳）
- optimizer_index_cost_adj: 0-10000, 100 默认。降低 → 更愿意走索引
- optimizer_index_caching: 索引在 buffer cache 中的比例（0-100, 默认 0），调高让 CBO 假设索引常驻
- db_file_multiblock_read_count: Seq Scan 每次 IO 读多少块，默认 128

## Join 算子选择
| 算子 | 适用条件 | cost 公式 |
|------|----------|----------|
| NESTED LOOPS | 内表小 OR 有索引 | outer_rows × inner_lookup_cost |
| HASH JOIN | 两表都大且等值 join | build + probe (受 hash_area_size / pga_aggregate_target 约束) |
| SORT MERGE JOIN | 两边已排序 OR sort 比 hash 便宜 | sort_o + sort_i + merge |

## 索引选择决策
CBO 走 INDEX RANGE SCAN 前提：
  1. 谓词命中索引前导列（最左前缀）
  2. selectivity（估算行数/表行数）< 一个阈值（典型 1-10%, 取决于 optimizer_index_cost_adj）
  3. 谓词 sargable - 列无函数包裹

不走索引常见原因：
  - 列被函数包裹（TO_CHAR(date_col, 'YYYY-MM') = '2026-01'）→ 函数索引或改写
  - 类型不匹配引起隐式转换（CHAR vs VARCHAR2, NUMBER vs VARCHAR2）→ 字面量类型对齐
  - 直方图缺失，CBO 用均匀假设 → DBMS_STATS.GATHER_TABLE_STATS METHOD_OPT 加直方图

## ⭐ 10053 trace（金标准 CBO dump）
**Oracle 是 SQL 数据库里唯一 dump 完整 CBO 推理过程的方言**。opendb 已自动采集到 cc.Trace。
关键段落:
- "BASE STATISTICAL INFORMATION" — 各表的基础统计（行数、列 NDV、直方图）
- "Access Path" — 每张表的 access path 候选与 cost
- "Join Order[N]" — join order 枚举，含 rejected paths 与原因
- "Final cost for this query block" — CBO 最终选择
比较 Access Path 中 rejected 项的 cost 与 selected 项，能精确说出"为什么没选 X"。

## bind peeking
Oracle 12c+ 默认 ACS（Adaptive Cursor Sharing）：
  - 首次执行 hard parse 时按当前 bind 值估算（peeking）
  - 后续执行如发现 bind 倾斜大，自动 sub-cursor
  - 副作用：cursor cache 中可能存多个 child cursor — V$SQL_PLAN_STATISTICS_ALL`
}

func (oraclePromptBuilder) PlanReading() string {
	return `1. 看 Plan 顶部 cost 与 cardinality（CBO 总估算）
2. 找 cost 最高的算子节点（瓶颈）
3. 看 cardinality（估算行数）vs actual rows（如有 DBMS_XPLAN.DISPLAY_CURSOR 含 +STATISTICS）:
   - 偏差 > 10× → 统计失真，DBMS_STATS.GATHER_TABLE_STATS
4. 看算子类型：
   - TABLE ACCESS FULL on 大表 → 缺索引 OR 谓词不可 sargable
   - INDEX FULL SCAN → 全索引扫，可能需要 narrower covering index
   - INDEX RANGE SCAN → 索引范围扫（最佳）
   - INDEX UNIQUE SCAN → 唯一索引（最佳，常量级）
   - NESTED LOOPS 内表是 TABLE ACCESS FULL → join 顺序错 OR 内层缺索引
   - HASH JOIN with TempSpc > 0 → hash 溢出磁盘（pga_aggregate_target 不足）
5. 看 Predicate Information:
   - access() → 走索引匹配
   - filter() → 顺序扫后过滤
   - 应走索引的谓词进 filter → sargable 问题
6. 看 V$DIAG_TRACE_FILE_CONTENTS 中 10053 trace（opendb 自动提供）：
   - 找 "Access Path: ... [Cost ... Resp ...]" 列出所有候选
   - 找 "Best so far" 看 CBO 选了哪个
   - 找 "Reject Group By Placement" 等 hint 看为什么 reject 某些 transformation`
}

func (oraclePromptBuilder) HintSyntax() string {
	return `Oracle hint 语法（注释式，紧跟 SELECT/UPDATE/...）:

  -- Access path
  SELECT /*+ INDEX(t idx_name) */ ...           -- 强制走指定索引
  SELECT /*+ NO_INDEX(t idx_name) */ ...
  SELECT /*+ FULL(t) */ ...                     -- 强制全表扫
  SELECT /*+ INDEX_FFS(t idx) */ ...            -- 索引快速全扫

  -- Join
  SELECT /*+ USE_NL(t1 t2) */ ...               -- 强制 nested loop
  SELECT /*+ USE_HASH(t1 t2) */ ...
  SELECT /*+ USE_MERGE(t1 t2) */ ...
  SELECT /*+ LEADING(t1 t2 t3) */ ...           -- 固定 join 起点顺序
  SELECT /*+ ORDERED */ ...                     -- 按 FROM 子句顺序

  -- Parallel
  SELECT /*+ PARALLEL(t 4) */ ...
  SELECT /*+ NO_PARALLEL */ ...

  -- Plan stability
  SELECT /*+ USE_HASH_AGGREGATION */ ...
  SELECT /*+ NO_USE_HASH_AGGREGATION */ ...

  -- Session-level
  ALTER SESSION SET optimizer_mode = FIRST_ROWS_10;

注意：hint 是 Oracle 调优的核心工具，PG 没原生 hint，MySQL 形式不同。`
}
