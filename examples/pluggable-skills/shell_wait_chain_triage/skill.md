---
api_version: opendb.skill/v1
name: shell_wait_chain_triage
title: Shell Wait Chain Triage
description: Read-only wait, lock chain, long transaction, and session pressure triage for OpenGauss/GaussDB
kind: script
db_types: [opengauss, gaussdb]
security: read_only
timeout: 45s
command: ["./run.sh"]
parameters:
  type: object
  properties:
    min_seconds:
      type: integer
      description: Only show active/transaction sessions older than this threshold. Default 30.
    limit:
      type: integer
      description: Maximum rows per section. Default 10.
    dbcli:
      type: string
      description: Optional database client command, usually gsql or psql.
triggers:
  - current wait chain
  - lock blocking chain
  - long transaction
  - session pressure
  - 当前等待链
  - 当前阻塞链
  - 长事务
tags: [performance, lock, wait, shell, readonly]
---

生产只读等待链分诊 skill。

适用场景：

- 用户询问“当前有没有阻塞/等待链/长事务/会话压力”。
- 需要快速判断性能问题是否来自锁等待、长事务、业务会话堆积或后台线程。
- 需要在客户现场接入已有 shell 运维习惯，避免修改 DBAA Go 源码。

输出包含：

- 当前活跃会话状态分布。
- 等待事件分布。
- 未授予锁的阻塞链。
- 超过阈值的长事务/长 SQL。

安全边界：

- 只执行 SELECT。
- 不执行 kill/terminate/DDL/DML。
- 需要 `gsql` 或 `psql` 在 PATH 中，数据库认证由客户运行环境负责。

