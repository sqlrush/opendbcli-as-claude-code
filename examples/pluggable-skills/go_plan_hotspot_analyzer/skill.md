---
api_version: opendb.skill/v1
name: go_plan_hotspot_analyzer
title: Go Plan Hotspot Analyzer
description: Analyze OpenGauss/GaussDB EXPLAIN plan text and identify high-cost operators, row-estimation errors, sort/hash risks, and indexing opportunities
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 45s
command: ["./run.sh"]
parameters:
  type: object
  properties:
    plan_text:
      type: string
      description: EXPLAIN or EXPLAIN ANALYZE plan text to analyze. Preferred because it does not require DB credentials.
    sql:
      type: string
      description: Optional SQL. If plan_text is empty, the skill runs EXPLAIN for this SQL using gsql/psql.
    top_n:
      type: integer
      description: Number of hotspots to show. Default 8.
    dbcli:
      type: string
      description: Optional database client command, usually gsql or psql.
triggers:
  - explain plan hotspot
  - plan shape analysis
  - row estimate mismatch
  - seq scan hotspot
  - nested loop hotspot
  - 执行计划热点
  - 执行计划代价热点
  - 行数估算偏差
  - 顺序扫描热点
tags: [performance, explain, sqltune, go, readonly]
---

生产只读执行计划热点分析 skill。

适用场景：

- 用户已经有一段 EXPLAIN/EXPLAIN ANALYZE 输出，希望快速找代价热点。
- 用户希望在不改 DBAA 源码的情况下，扩展一套自定义执行计划规则。
- 用户提供 SQL，且客户沙箱内有 `gsql`/`psql` 连接能力时，可由 skill 自动跑 EXPLAIN。

输出包含：

- Top cost operators。
- Seq Scan / Nested Loop / Sort / Hash Join 等高风险节点。
- estimated rows 与 actual rows 偏差提示。
- 面向 DBA 的索引、统计信息、work_mem、SQL 改写方向。

安全边界：

- 默认建议传 `plan_text`，完全不连接数据库。
- 如果传 `sql`，仅执行 `EXPLAIN`，不执行 `EXPLAIN ANALYZE`，避免 DML 副作用。
- 不自动创建索引、改参数或改写 SQL。

