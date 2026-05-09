/*-------------------------------------------------------------------------
 *
 * opengauss.go
 *	  OpenGaussProfile provides OpenGauss-specific knowledge to the
 *	  Engine. Based on PostgreSQL kernel with OG-specific additions (gs_
 *	  views, WDR, MOT, CM, resource pools). ToolUsageHint, ToolFilter
 *	  and DefaultMaxTurns reuse PG behavior for shared tools but
 *	  OG-specific tools get OG hints.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/opengauss.go
 *
 *-------------------------------------------------------------------------
 */
package profile

// OpenGaussProfile provides OpenGauss-specific knowledge to the Engine.
// Based on PostgreSQL kernel with OG-specific additions (gs_ views, WDR,
// MOT, CM, resource pools). ToolUsageHint, ToolFilter and DefaultMaxTurns
// reuse PG behavior for shared tools but OG-specific tools get OG hints.
type OpenGaussProfile struct {
	pg PostgresProfile
}

func (p *OpenGaussProfile) Product() string { return "opengauss" }

func (p *OpenGaussProfile) SystemPromptRules() string {
	return `# OpenGauss 数据库特定知识

## 内核基础与差异
OpenGauss 基于 PostgreSQL 内核二次开发，大部分诊断方法（MVCC/WAL/等待事件/pg_stat_*）与 PG 一致，但有以下关键差异必须知晓：
- 系统视图使用 ` + "`gs_`" + ` 前缀（gs_session_stat / gs_sql_count / gs_wlm_session_info / gs_asp 等）
- 扩展统计视图用 ` + "`dbe_perf.*`" + `（dbe_perf.statement / dbe_perf.statement_history / dbe_perf.wait_events）
- WDR（Workload Diagnosis Report）替代 Oracle AWR / PG 的 pg_stat_statements 深度分析
- MOT（Memory-Optimized Table）是 OG 独有的内存表引擎
- CM（Cluster Manager）是 OG 企业版/集群版的主控组件
- Workload Manager（WLM）资源池：gs_wlm_resource_pool / gs_respool_stat
- 双机热备架构：standby_count / replication_role / recovery_min_apply_delay

## 对象引用规则
- 标识符默认小写（同 PostgreSQL）
- 使用双引号保留大小写
- 引用任何表名/索引名/序列名前必须用 sql skill 查询 pg_class 或 gs_stat_user_tables 确认存在
- 禁止从 topsql/slowsql/pg_stat_statements 的 SQL 文本推断对象存在（SQL 可能引用已删除对象）
- 给出修复 SQL 前必须确认对象的 schema/owner/当前状态

## 等待事件速查

### LWLock 族（轻量锁 — OG 内部同步核心）
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| LWLock:BufferContent | 共享缓冲区页争用 | 并发访问同一数据页；热点表需考虑分区或加大 shared_buffers |
| LWLock:BufferMapping | 缓冲区映射表争用 | 高并发 IO 导致 NBuffers 不够，增大 shared_buffers |
| LWLock:WALInsert | WAL 插入锁争用 | 高并发写入；考虑 wal_buffers 调整或批量提交 |
| LWLock:WALWriteLock | WAL 写锁 | 磁盘 IO 慢或 commit 过频 |
| LWLock:ProcArray | 进程数组锁 | 连接数突发或 snapshot 生成频繁，查 idle in transaction |
| LWLock:CLogControlLock | 事务提交日志锁 | 短事务高频提交；考虑批量提交或增大 CLOG buffer |
| LWLock:XidGenLock | XID 分配锁 | 高并发事务产生；可能配合 XID wraparound 风险升高 |

### Lock 族（事务级锁）
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| Lock:transactionid | 事务 ID 锁 | 行锁等待；用 blocktree 查阻塞链 |
| Lock:tuple | 元组锁 | 多会话竞争更新同一行 |
| Lock:relation | 表级锁 | DDL 与 DML 冲突或 VACUUM FULL 阻塞 |
| Lock:extend | 表扩展锁 | 批量 INSERT 争用 extend 锁 |

### IO 族
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| IO:DataFileRead | 数据文件读 | 缺索引或 shared_buffers 不足 |
| IO:DataFileWrite | 数据文件写 | 脏页刷盘慢；检查 checkpoint 频率 |
| IO:WALWrite | WAL 写入 | 提交频繁或磁盘 IO 慢 |
| IO:WALSync | WAL fsync | 磁盘 fsync 慢；检查存储性能或 synchronous_commit 配置 |

### IPC / Client / Timeout
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| IPC:MessageQueueSend | 并行 worker 通信 | 并行查询跨进程同步 |
| Client:ClientRead | 等客户端发送 | Idle 等待，通常不是问题 |
| Timeout:PgSleep | pg_sleep 显式等待 | 业务代码主动 sleep |

## MVCC / XID 知识
- 死元组必须 VACUUM 清理，长事务阻止 VACUUM 回收空间（典型症状：xid_age 持续增长但 VACUUM 无进展）
- XID wraparound 阈值：age(datfrozenxid) > 15 亿预警，> 18 亿紧急，> 20 亿强制只读
- autovacuum_freeze_max_age 默认 2 亿，超过触发紧急 VACUUM
- VACUUM 受阻三类根因：长事务未结束 / 未释放的复制槽 / prepared transaction 未提交
- 查 VACUUM 阻塞源：
  - SELECT backend_xid, backend_xmin FROM pg_stat_activity WHERE state != 'idle'
  - SELECT slot_name, xmin FROM pg_replication_slots
  - SELECT gid, prepared FROM pg_prepared_xacts

## VACUUM / bloat
- 死元组/活元组比例 > 20% 视为 bloat，> 50% 紧急
- pg_stat_all_tables.n_dead_tup / n_live_tup 看单表
- pgstattuple 扩展可精确测 bloat（但扫表慢）
- VACUUM FULL 持有 AccessExclusiveLock，建议 pg_repack 替代（OG 3.0+ 原生支持）
- autovacuum 未跟上（n_dead_tup 持续增长）→ 调小 autovacuum_naptime 或加 autovacuum_max_workers

## WAL / Checkpoint / 同步复制
- WAL 写冲高 3 类根因：批量 INSERT / 大事务 COMMIT 频繁 / full_page_writes=on + checkpoint 过频
- checkpoint_timeout 默认 5min，过小会频繁 checkpoint 放大 WAL
- max_wal_size 过小导致 checkpoint 频繁
- synchronous_commit：on（默认）= 等 standby fsync；remote_write = standby OS 写入即确认；off = 不等待
- 同步复制延迟查 pg_stat_replication.write_lag / flush_lag / replay_lag

## 连接会话异常排查流程
1. 连接数异常 → 先查 pg_stat_activity 连接频率（backend_start > now - interval '1 min' 数量）
2. 短连接风暴（backend_start 都很新）→ 建议上 PgBouncer（transaction pool, pool_size = CPU*2~4）
3. 长连接占满 → 查 idle in transaction 堆积，设置 idle_in_transaction_session_timeout
4. 并行 worker 过多 → 限制 max_parallel_workers_per_gather

## gs_ 系列视图专属
- gs_session_stat / gs_session_memory_detail：OG 特有 session 维度统计
- gs_sql_count：按 SQL 类型统计
- gs_wlm_session_info：Workload Manager 活跃会话
- gs_asp：Active Session Profile（类 Oracle ASH）
- gs_respool_stat：资源池使用情况
- gs_stat_activity：扩展版 pg_stat_activity，含 WLM 标签

## WDR 报告（对标 Oracle AWR）
- create_wdr_snapshot() 手动打快照
- generate_wdr_report(begin_snap, end_snap) 生成对比报告
- 查近期 WDR snap：SELECT snapshot_id, start_ts, end_ts FROM snapshot.snapshot ORDER BY snapshot_id DESC

## MOT（Memory-Optimized Table）
- 适合 OLTP 高并发小事务
- 独立视图：mot_session_memory_detail / mot_mem_cfg / mot_jit_profile
- 不支持 VACUUM（自动回收）
- 常见问题：MOT 内存溢出、MOT checkpoint 阻塞

## CM 集群管理
- 状态查询：cm_ctl query
- 集群异常日志在 /var/log/gauss/cm/cm_server.log

## 参数修改注意
- ALTER SYSTEM SET → SELECT pg_reload_conf() 生效（重载）
- 部分参数需要重启（shared_buffers / max_connections / max_wal_size）
- pg_settings 查参数来源
- 隐含参数（_ 开头）谨慎修改，必须说明风险
- OG 特有：GUC_SUPERUSER / GUC_USERSET 权限等级

## 修复建议安全规则
- CREATE INDEX → 始终建议 CREATE INDEX CONCURRENTLY（除非空表或紧急恢复）
- REINDEX → 建议 REINDEX CONCURRENTLY（OG 3.0+）
- VACUUM FULL → 警告：AccessExclusiveLock 阻塞所有操作，建议 pg_repack
- ALTER TABLE ADD COLUMN → 提醒：非 DEFAULT 快速；带 DEFAULT 需要重写表
- 修改 shared_buffers / max_connections / max_wal_size → 需要重启
- kill 会话 → 优先用 pg_cancel_backend（取消查询）再用 pg_terminate_backend（终止连接）

## 常见 OG SQLSTATE
| SQLSTATE | 含义 | 快速检查 |
|---------|------|---------|
| 53200 | out of memory | gsmem + params max_process_memory |
| 53300 | too many connections | sessions + params max_connections |
| 54000 | program limit exceeded | params 检查相关上限 |
| 23505 | unique violation | 检查约束和数据 |
| 40001 | serialization failure | 隔离级别冲突，重试事务 |
| 42P01 | undefined table | 对象不存在；用 sql 验证 |`
}

func (p *OpenGaussProfile) ToolUsageHint(name string) string {
	// OG-specific tools get OG-tailored hints; shared tools fall through to PG.
	ogHints := map[string]string{
		"gsmem":       "内存分析：OG 会话和共享内存详情",
		"respool":     "资源池：Workload Manager 资源池使用情况",
		"wdr":         "性能报告：WDR 快照分析",
		"planhistory": "计划历史：基于 dbe_perf.statement_history 检测计划回归",
		"lwlocks":     "轻量锁分析：LWLock 争用 profile",
		"autovacuum":  "autovacuum 详情：worker 状态 / progress",
		"checkpoint":  "Checkpoint 分析：频率 / WAL 写放大 / full_page_writes 影响",
		"bgworker":    "后台进程：bgwriter / walwriter / archiver / stats collector",
		"hotkey":      "热点识别：热点表/行（高 n_tup_upd + 高 seq_scan）",
		"mot":         "MOT 内存表：MOT 引擎使用和内存状态",
		"cmha":        "集群管理：CM 双机热备 / 分布式集群健康",
		"xid":         "XID 风险：xid_age 检查 / wraparound 预警",
		"vacuum":      "MVCC 维护：VACUUM 进度 / 死元组 / autovacuum 状态",
		"bloat":       "膨胀检查：表和索引的 bloat 率",
		"longtx":      "长事务：> N 秒的运行事务",
		"slots":       "物理复制槽：slot 状态和 WAL 保留",
		"replication": "流复制：主从复制延迟 / standby 状态",
		"wal":         "WAL 状态：生成速率 / 归档状态 / LSN 位置",
		"backup":      "归档状态：WAL archiver 进程和归档积压",
		"jobs":        "定时任务：pg_cron / OG 内置 job scheduler",
		"alert":       "告警聚合：冲突 / 死锁 / 临时文件 / checkpoint 警告",
		"extensions":  "扩展清单",
		"sessionmem":  "会话内存：单 session 级别内存占用排行",
		"sharedbufs":  "共享缓冲：shared_buffers 命中率和使用",
		"sqlcount":    "SQL 类型统计：gs_sql_count 按类型聚合",
		"ogerr":       "错误知识库：OG SQLSTATE 错误码速查",
	}
	if hint, ok := ogHints[name]; ok {
		return hint
	}
	return p.pg.ToolUsageHint(name)
}

func (p *OpenGaussProfile) ToolFilter(mode string) func(string, int) bool {
	return p.pg.ToolFilter(mode)
}

func (p *OpenGaussProfile) DefaultMaxTurns(mode string) int {
	return p.pg.DefaultMaxTurns(mode)
}
