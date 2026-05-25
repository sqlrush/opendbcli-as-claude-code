---
api_version: opendb.skill/v1
name: python_table_maintenance_advisor
title: Python Table Maintenance Advisor
description: Read-only table bloat, stale statistics, sequential scan, and unused-index advisor for OpenGauss/GaussDB
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 60s
command: ["python3", "run.py"]
parameters:
  type: object
  properties:
    min_table_mb:
      type: integer
      description: Only include tables larger than this many MB unless they already have dead tuples. Default 32.
    dead_pct_warn:
      type: integer
      description: Dead tuple percentage warning threshold. Default 20.
    mod_since_analyze_warn:
      type: integer
      description: n_mod_since_analyze warning threshold. Default 50000.
    limit:
      type: integer
      description: Maximum rows per section. Default 15.
    dbcli:
      type: string
      description: Optional database client command, usually gsql or psql.
triggers:
  - table bloat
  - stale statistics
  - vacuum analyze advisor
  - sequential scan table
  - unused index
  - 表膨胀
  - 统计信息过期
  - vacuum analyze
  - 顺序扫描过多
tags: [performance, maintenance, statistics, vacuum, python, readonly]
---

生产只读表维护建议 skill。

适用场景：

- 用户询问“哪些表可能膨胀”“哪些表统计信息过期”“哪些表顺序扫描多”“是否需要 vacuum/analyze”。
- 需要把表维护类问题和 SQL 性能退化关联起来看。
- 需要输出结构化 JSON，便于 DBAA/LLM 进一步总结。

输出包含：

- 高 dead tuple 比例表。
- `n_mod_since_analyze` 较高的统计信息过期表。
- 大表顺序扫描偏高、索引扫描偏低的表。
- 大而未使用的索引候选。

安全边界：

- 只执行 SELECT。
- 不自动执行 VACUUM/ANALYZE/REINDEX/DROP INDEX。
- 只给候选建议，生产变更仍需 DBA 复核。

