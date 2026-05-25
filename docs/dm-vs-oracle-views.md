# DM vs Oracle 动态视图差异清单

**生成日期**: 2026-05-01
**DM 实例**: dm 8.1.4.200 build 03134284488-20260212-314192-20200
**对比目标**: Oracle 19c+ 主流 V$ 视图

DM 全量动态视图清单：`SELECT NAME FROM V$DYNAMIC_TABLES` → 共 **380** 个（含 GV$ 集群版本）

## 1. 设计文档 §3.3 涉及视图的真机校验结果

设计文档 v0.6 §3.3 列出的 DM 视图，**逐一在真实 DM8 实例上 SELECT 1 验证**：

| 设计文档列出 | 真机存在? | 备注 |
|---|:---:|---|
| V$INSTANCE | ✅ | |
| V$VERSION | ✅ | |
| V$SYSSTAT | ✅ | |
| V$RESOURCE_LIMIT | ✅ | |
| V$DANGER_EVENT | ✅ | |
| V$BUFFERPOOL | ✅ | |
| V$MEM_POOL | ✅ | |
| V$DICT_CACHE | ✅ | |
| V$SESSIONS | ✅ | |
| V$CONNECT | ✅ | |
| V$STMTS | ✅ | |
| V$LOCK | ✅ | |
| V$TRX | ✅ | |
| V$TRXWAIT | ✅ | |
| V$DEADLOCK_HISTORY | ✅ | |
| V$PURGE | ✅ | |
| V$EVENT_NAME | ✅ | |
| V$SESSION_WAIT_HISTORY | ✅ | |
| V$SYSTEM_EVENT | ✅ | |
| V$SESSION_EVENT | ✅ | |
| V$WAIT_HISTORY | ✅ | |
| V$SQL_HISTORY | ✅ | |
| V$LONG_EXEC_SQLS | ✅ | |
| V$SYSTEM_LONG_EXEC_SQLS | ✅ | |
| V$SQLTEXT | ✅ | |
| V$SORT_HISTORY | ✅ | |
| V$RUNTIME_ERR_HISTORY | ✅ | |
| V$DM_INI | ✅ | |
| V$PARAMETER | ✅ | |
| V$DM_ARCH_INI | ✅ | |
| V$DM_MAL_INI | ✅ | |
| V$DATABASE | ✅ | |
| V$DATAFILE | ✅ | |
| V$TABLESPACE | ✅ | |
| V$HUGE_TABLESPACE | ✅ | |
| V$RLOG | ✅ | |
| V$RLOGFILE | ✅ | |
| V$CKPT_HISTORY | ✅ | |
| V$ERR_INFO | ✅ | |

**全部 39 个设计文档列出的视图 100% 存在**。设计文档 §3.3 / §3.7 / §4.4.2 中的视图引用都准确，**无需修订**。

## 2. DM 独有的诊断/调优视图（Oracle 没有等价物）

按 Phase 0 拉到的全量列表过滤，DM 特有：

| DM 视图 | 用途 | Oracle 等价 |
|---|---|---|
| V$ACTIVE_SESSION_HISTORY | 活动会话历史（DM AWR 一部分）| Oracle ASH 类似但实现不同 |
| V$ALERTINFO | 告警信息 | 无（Oracle 用 alert.log 文件）|
| V$AP_ENV_INFO | AP（应用程序）环境 | 无 |
| V$ARCH_BACKUP_HISTORY | 归档备份历史 | 无（RMAN 管理）|
| V$ARCH_DETACH_INFO | 归档脱机信息 | 无 |
| V$ARCH_QUEUE | 归档队列 | V$ARCHIVED_LOG 部分类似 |
| V$ARCH_STATUS | 归档状态 | V$ARCHIVE_DEST_STATUS |
| V$BACKUPSET_* (多个)| 备份集详情 | RMAN 视图 |
| V$BUFFER_LRU_FIRST / V$BUFFER_UPD_LAST | Buffer LRU/dirty | 无（Oracle X$ 视图近似）|
| V$CACHEITEM, V$CACHESQL | SQL 缓存项 | V$SQLAREA 部分类似 |
| V$CKPT_HISTORY | checkpoint 历史 | V$INSTANCE_RECOVERY |
| V$CMD_HISTORY | 命令历史 | 无 |
| V$DEADLOCK_HISTORY | 死锁历史（DM 自动记录）| 需启 deadlock trace |
| V$DICT_CACHE_* | 字典缓存项 | 无 |
| V$DM_INI / V$DM_ARCH_INI / V$DM_MAL_INI | 配置文件视图 | V$PARAMETER（部分）|
| V$DPC_* | 分布式处理集群（DPC）| 无 |
| V$DSC_* | 数据共享集群（DSC）| Oracle RAC 不同实现 |
| V$ERR_INFO | 内置错误码字典 | 无（Oracle 错误信息散在 oerr）|
| V$LONG_EXEC_SQLS | 当前正在执行的长 SQL | 无（自己 join V$SESSION + V$SQL）|
| V$MEM_POOL | DM 内存池 | V$SGAINFO + V$SGASTAT 类似但分类不同 |
| V$MOT_* | 内存优化表（DM 特有）| 无 |
| V$PURGE | 回滚段清理 | DBA_UNDO_EXTENTS 部分类似 |
| V$ROLE_INFO | 集群角色信息 | 无 |
| V$SESSION_HISTORY | 会话历史 | DBA_HIST_ACTIVE_SESS_HISTORY 类似 |
| V$SQL_HISTORY | SQL 执行历史 | V$SQL（语义类似但 DM 是历史快照）|
| V$STKFRM | 栈帧 | 无 |
| V$SYSTEM_LONG_EXEC_SQLS | 系统级长 SQL | 自己 join V$SQL |
| V$WAIT_CLASS | 等待事件分类 | V$SYSTEM_WAIT_CLASS |
| V$WTHRD_HISTORY | 工作线程历史 | 无 |
| V$XBOX | DM 内部对象 | 无 |
| V$XSITE | DM 内部对象 | 无 |

## 3. Oracle 有但 DM 没有的高频视图

opendb 现有 `internal/oracle/skill/` 用到但 DM 不存在的：

| Oracle 视图 | DM 替代 | 备注 |
|---|---|---|
| V$SESSION（单数）| V$SESSIONS（复数）| 字段名也不同 |
| V$SQL（单数）| V$SQL_HISTORY | DM 是历史快照不是当前 |
| V$SQLAREA | V$CACHESQL | 缓存层级不同 |
| V$SQLSTATS | V$SYSTEM_LONG_EXEC_SQLS | 部分字段类似 |
| V$LIBRARYCACHE | V$DICT_CACHE | DM 没有 library cache 概念 |
| V$LATCH | 无（DM 用 LWLock 但视图未暴露）| |
| V$WAITSTAT | V$SYSTEM_EVENT | |
| V$BH (buffer header) | V$BUFFER_LRU_FIRST | |
| V$ROLLSTAT | V$PURGE | |
| V$LOG / V$LOGFILE | V$RLOG / V$RLOGFILE | 命名不同 |
| DBA_HIST_* (AWR) | DM 自带 AWR 但 schema 不同 | 用 SP_AWR_REPORT_* 生成 |
| V$ASH_* | V$ACTIVE_SESSION_HISTORY | 字段更少 |
| V$LOCK | V$LOCK | 字段差异大（DM 用 BLOCKED 标志）|
| V$LOCKED_OBJECT | 自己 join V$LOCK + SYSOBJECTS | DM 没现成视图 |
| V$BLOCKING_LOCKS | 自己写 self-join | DM 没 pg_blocking_pids 等价 |

## 4. PG 概念 DM 完全不存在的（避免幻觉）

LLM 容易把 PG 知识套到 DM 上。以下是 PG 有 / DM 完全没有的概念：

| PG 概念 | DM 状况 | 注意事项 |
|---|---|---|
| pg_stat_activity | 无 | 用 V$SESSIONS（复数）|
| pg_stat_statements | 无（有 V$SQL_HISTORY 但语义不同）| 历史快照模式 |
| pg_locks | 无 | 用 V$LOCK |
| pg_blocking_pids() | 无 | 自己 self-join V$LOCK |
| pg_stat_user_tables | 无（用 SYSOBJECTS + DBA_TABLES）| |
| pg_stat_replication | 无 | DM 主备看 V$DATABASE.ROLE |
| pg_buffercache | 无（用 V$BUFFERPOOL + V$BUFFER_*）| |
| autovacuum / VACUUM | **完全没有** | DM 内核自动 purge 死元组 |
| analyze (PG 命令) | 用 DBMS_STATS.GATHER_TABLE_STATS | Oracle 兼容语法 |
| pg_stat_bgwriter | 无（DM 没 bgwriter 概念）| |
| pg_terminate_backend(pid) | 无 | 用 SP_CLOSE_SESSION(sess_id) |
| ALTER SYSTEM SET / SET | 无 | 用 SP_SET_PARA_VALUE(scope, name, value) |
| pg_reload_conf() | 无 | 静态参数改 dm.ini 重启 |
| pg_class / pg_attribute | 无 | 用 SYSOBJECTS / SYSCOLUMNS |
| pg_settings | 无 | 用 V$PARAMETER + V$DM_INI |

## 5. opendb DM Phase 1 实施建议

基于真机校验：

1. **设计文档 §3.3 / §3.7 视图清单全部准确**，可直接按 §4.1 目录结构实施 25 个 skill
2. **§4.4.2 dmSpecificFacts 的 17 条硬事实全部正确**，无需修订
3. **§3.5 阻塞链 SQL** Phase 1 实施时仍要真机校验字段名（V$LOCK 的 RES_ID / TID 字段）— 本次未跑 EXPLAIN 验证 SQL 可执行
4. **§3.6 AWR procedure 调用** Phase 1 实施时再真机校验（SP_INIT_AWR_SYS / SP_AWR_REPORT_LAST_DAY）

## 6. 重要发现 — DM 驱动平台限制（设计文档需补充）

DM 官方 Go 驱动 `dm-go-driver` 包含 OS-specific 加密代码：

```
dm/security/zzg_linux.go      ← Linux 实现
dm/security/zzh_windows.go    ← Windows 实现
dm/security/zzg_darwin.go     ← 不存在!
```

**结论**：DM 官方 Go 驱动**不支持 macOS**。dbaa 必须 cross-compile 到 Linux 部署，开发时本地 Mac 上无法直接连 DM。

**影响范围**：
- dbaa Linux 二进制：✓ 完全工作（已验证）
- dbaa macOS 二进制：✗ DM 功能不可用（其他 4 库 Oracle/MySQL/PG/OG 不受影响）

**建议**：CHANGELOG / quickstart 写明"DM 仅 Linux/Windows 部署"。设计文档 §1.2 需补一条限制说明。

## 7. 视图全量清单文件

完整 380 个视图列表保存在测试机：`/tmp/dm_views.txt`

可随时重新生成：
```bash
ssh root@47.251.30.180 "bash /tmp/dump_dm_views.sh"
```
