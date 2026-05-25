/*-------------------------------------------------------------------------
 *
 * dm.go
 *	  DMProfile provides Dameng (DM) database knowledge to the Engine.
 *	  Layer 2 hard-facts injected into system prompt.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/dm.go
 *
 *-------------------------------------------------------------------------
 */
package profile

// DMProfile provides Dameng (DM) database knowledge to the Engine.
// Layer 2 hard-facts injected into system prompt.
type DMProfile struct{}

func (p *DMProfile) Product() string { return "dm" }

func (p *DMProfile) SystemPromptRules() string {
	return `# DM (达梦) 数据库特定知识

## 对象引用规则
- DM 默认大小写敏感（CASE_SENSITIVE=Y），表名/列名保留大小写
- 引用任何对象前必须用 sql skill 查询 SYSOBJECTS 确认存在
- COMPATIBLE_MODE=2（Oracle 兼容）时可引用 100+ Oracle 系统视图
- 标识符前缀：V$ 是动态视图，SYS_ 是系统对象，DBA_ 是 Oracle 兼容字典
- 给出修复 SQL 前必须确认对象的 owner 和当前状态

## 体系架构
DM 单实例核心组件：
- 工作线程（WORKER_THREADS）— SQL 执行
- 任务线程（TASK_THREADS）— 后台任务
- IO 线程组（IO_THR_GROUPS）— 异步 IO
- LSN（Log Sequence Number）— redo 日志序列号，单调递增
- TID — 事务 ID
- SESS_ID — 会话 ID

## 关键运维操作（强制语法 — 禁用 Oracle/PG 等其他 DB 语法）
- 杀会话: **必须**用 CALL SP_CLOSE_SESSION(<sess_id>), sess_id 来自 V$SESSIONS
  禁止: ALTER SYSTEM KILL SESSION (Oracle) / pg_terminate_backend (PG)
- 改参数: CALL SP_SET_PARA_VALUE(<scope>, '<NAME>', <value>)
  scope: 1 = 当前+静态, 2 = 仅动态, 3 = 仅当前
- 强制 checkpoint: CALL SP_CKPT_OPER('FULL')
- 收集表统计: CALL DBMS_STATS.GATHER_TABLE_STATS('<schema>', '<table>')
- EXPLAIN <sql>: 返回三元组 [代价ms, 记录行数, 字节数]
- 死锁检测/回滚: DM 内核**自动**完成 (V$DEADLOCK_HISTORY 记录), 不是 Oracle/MySQL 死锁机制

## 关键视图

### 会话/锁/事务
- V$SESSIONS（注意复数）— SESS_ID, USER_NAME, SQL_TEXT, STATE, CREATE_TIME, CLNT_HOST
- V$STMTS — 当前执行 SQL
- V$CONNECT — 连接信息
- V$LOCK — TID, RES_ID, LMODE, BLOCKED, TABLE_ID, ROW_IDX (BLOCKED=1 表示阻塞中)
- V$TRX — 事务信息
- V$TRXWAIT — 事务等待
- V$DEADLOCK_HISTORY — 历史死锁
- V$PURGE — 回滚段

### 等待事件
- V$EVENT_NAME — 等待事件字典
- V$WAIT_HISTORY — 全局等待历史
- V$SESSION_WAIT_HISTORY — 当前会话等待
- V$SYSTEM_EVENT — 系统级累计等待
- V$SESSION_EVENT — 会话级累计等待

### SQL 历史与性能 (注意: 累积值 vs 实时值)
- V$SQL_HISTORY — SQL 执行历史 (**累积值**自上次 reset, 含已 DROP 表的旧 SQL)
- V$LONG_EXEC_SQLS — 当前**正在执行**的长 SQL (实时入口)
- V$SYSTEM_LONG_EXEC_SQLS — 系统级长 SQL **累计**
- V$SQLTEXT — SQL 全文
- V$SORT_HISTORY — 排序历史 (累积)
- V$RUNTIME_ERR_HISTORY — 运行错误 (累积)
- V$WAIT_HISTORY / V$SYSTEM_EVENT — 等待事件 (累积)

**重要**: 区分"累积值"和"当前现场":
- topsql/slowsql/waits/alert 这类工具返回的多为累积自上次 reset 的统计
- 当前实时负载请用 activesessions / blocktree / V$SESSIONS WHERE STATE='ACTIVE'
- 不要把累积值当成"现在正在发生", 必要时 join V$SESSIONS 验证

### 内存/缓冲
- V$BUFFERPOOL — 缓冲池命中率
- V$MEM_POOL — 内存池
- V$DICT_CACHE — 字典缓存
- V$BUFFER_LRU_FIRST / V$BUFFER_UPD_LAST — buffer LRU/dirty
- V$CACHEITEM, V$CACHESQL, V$SQL_PLAN — SQL 执行计划缓存

### 系统状态
- V$INSTANCE — 实例信息（启动时间、状态）
- V$VERSION — 版本号
- V$DATABASE — 数据库（ROLE 字段标主备：PRIMARY/STANDBY）
- V$SYSSTAT — 系统统计计数器
- V$RESOURCE_LIMIT — 资源限制（会话数/连接数 vs 上限）
- V$DANGER_EVENT — 危险事件
- V$PROCESS / V$THREADS — 进程/线程

### 存储
- V$DATAFILE / V$TABLESPACE / V$HUGE_TABLESPACE — 数据文件、表空间
- V$RLOG / V$RLOGFILE — redo 日志
- V$CKPT_HISTORY — checkpoint 历史

### 配置/参数
- V$DM_INI — dm.ini 静态参数
- V$PARAMETER — 运行时参数
- V$DM_ARCH_INI — 归档参数

### 错误码
- V$ERR_INFO — 错误码字典（实测列: CODE INTEGER + ERRINFO VARCHAR, 仅 2 列, 不要用 ERR_CODE/ERR_LEVEL/ERR_TYPE/ERR_DESC 这些不存在的字段）
- V$RUNTIME_ERR_HISTORY — 运行错误历史（错误码字段是 ECPT_CODE / ECPT_DESC, 不是 ERR_CODE）
- V$DANGER_EVENT — 危险事件（时间字段是 OPTIME, 不是 HAPPEN_TIME / EVENT_TIME）

## DM 视图列名陷阱（祖传错误，必读）
基于真机 (DM 8.1.4.200) 验证暴露的列名差异，给修复 SQL 前必须遵守:

时间字段陷阱:
- V$DANGER_EVENT: 用 OPTIME (不是 HAPPEN_TIME, 容易和 V$DEADLOCK_HISTORY 混淆)
- V$DEADLOCK_HISTORY: 用 HAPPEN_TIME ✓ (这个表确实有 HAPPEN_TIME, 与 V$DANGER_EVENT 命名不一致)

错误码视图陷阱:
- V$ERR_INFO: 仅 CODE / ERRINFO 两列, 不要写 ERR_CODE/ERR_LEVEL/ERR_TYPE/ERR_DESC
- V$RUNTIME_ERR_HISTORY: 错误码字段 ECPT_CODE, 描述 ECPT_DESC (不是 ERR_CODE)

实例与数据库视图陷阱:
- V$INSTANCE: 版本字段是 SVR_VERSION 和 DB_VERSION, 不是 VERSION
- V$INSTANCE.STATUS$ / MODE$: 列名带 $ 后缀, SELECT 时用别名 STATUS$ AS STATUS
- V$DATABASE: 没有 DBID 列 (Oracle 才有). 主键标识用 NAME + LAST_STARTUP_TIME
- V$DATABASE.ROLE$: TINYINT (0=PRIMARY 1=STANDBY), 给 LLM 看时必须 CASE 翻译为字符串

不存在的视图 (Oracle 有 DM 没):
- V$RESOURCE_LIMIT: DM 没有此视图. 资源限制查 V$PARAMETER (上限) + V$SESSIONS/V$TRX/V$MEM_POOL (实时使用)
- V$SQLAREA: DM 没有. Top SQL 用 V$SQL_HISTORY GROUP BY SQL_ID
- V$OSSTAT: DM 没有. 主机指标分散在 V$INSTANCE / V$THREADS / V$PROCESS / V$MEM_POOL

视图目录 (列出 V$* 视图):
- 用 V$DYNAMIC_TABLES (380+ 项), 不是 SYSOBJECTS (只 10 项)
- 列名不确定时: SELECT * FROM V$DYNAMIC_TABLE_COLUMNS WHERE TABNAME='V$XXX' (列名是 COLNAME, 不是 NAME)

## 等待事件分析路径（DM 不像 Oracle 有 1000+ 标准事件名）
DM 等待事件相对少, 命名风格与 Oracle 不同 (常带下划线/中文混合). 不要假设事件名:
1. 字典: SELECT NAME, EVENT# FROM V$EVENT_NAME ORDER BY EVENT# (查实例当前支持的全部事件)
2. 实时: SELECT * FROM V$SESSION_WAIT_HISTORY (按 sess_id 看会话等待)
3. 累积: SELECT * FROM V$SYSTEM_EVENT ORDER BY TOTAL_WAITS DESC (系统级聚合)
4. 见到陌生事件名 → 用 sql skill 查 V$EVENT_NAME 含义, 不要凭 Oracle 经验猜测含义
5. 等待事件聚合统计推荐用 waits skill, 不要手写 SQL

## 关键系统包（DBMS_*，Oracle 兼容大部分语法）
- DBMS_STATS — 统计信息收集
- DBMS_JOB — 定时任务
- DBMS_LOB — LOB 操作
- DBMS_LOCK — 用户锁
- DBMS_SQL — 动态 SQL
- DBMS_OUTPUT — 调试输出
- DBMS_WORKLOAD_REPOSITORY — AWR

## 表空间结构
- SYSTEM — 系统表空间（数据字典）
- ROLL — 回滚段
- MAIN — 用户默认表空间
- TEMP — 临时表空间
- HMAIN — 大表空间

## 表膨胀与统计
DM 内核自动 purge 死元组，无需手动维护。
表统计走 SYSOBJECTS + DBA_TABLES + DBA_INDEXES。

## 主备状态
查 V$DATABASE 的 ROLE 字段（PRIMARY / STANDBY）。

## 慢 SQL
- 实时: V$LONG_EXEC_SQLS
- 历史: 启用 SVR_LOG=1，日志在 log/dmsql_<实例>_<日期>.log

## AWR
- 启用快照: CALL SP_INIT_AWR_SYS(1)
- 配置间隔: CALL DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(60, 7)
- 手动触发: CALL DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT()
- 生成报告: CALL SP_AWR_REPORT_LAST_DAY()

## 修复建议安全规则（必须遵守）
- 大表 CREATE INDEX → 注意业务高峰锁影响
- 死元组无需手动维护，不需要 VACUUM/ANALYZE 概念
- 修改 dm.ini 静态参数 → 改完必须重启实例
- 杀会话顺序 → 先 SP_CLOSE_SESSION，再处理事务资源
- 给生产环境建议 SQL 前 → 必须用 sql skill 验证表/索引/视图存在

## 占位符违规检查（结论中所有 ID/对象必须给具体值）
- 提到 sess_id → 必须给具体数字, 禁止 <sess_id> / <PID>
- 提到 sql_id → 必须给具体值
- 提到表名 → 必须先用 sql skill 验证存在
- 提到 SP_CLOSE_SESSION/CREATE INDEX 等命令 → 必须给完整可执行 SQL
- 不能用"可通过以下查询确认"等委托用户查的句式
`
}

// ToolUsageHint returns one-line scenario for each DM skill.
func (p *DMProfile) ToolUsageHint(name string) string {
	hints := map[string]string{
		"sessions":       "全部会话列表（V$SESSIONS）",
		"activesessions": "活跃会话列表（含 SQL_TEXT）",
		"locks":          "锁信息（V$LOCK，BLOCKED=1 是阻塞）",
		"blocktree":      "锁阻塞树（self-join V$LOCK + V$SESSIONS）",
		"waits":          "等待事件分布（V$WAIT_HISTORY / V$SYSTEM_EVENT）",
		"deadlock":       "历史死锁（V$DEADLOCK_HISTORY）",
		"sql":            "执行任意 SQL（read-only 默认）",
		"topsql":         "Top SQL（按执行次数 / 时间，V$SQL_HISTORY）",
		"slowsql":        "慢 SQL（V$LONG_EXEC_SQLS 实时）",
		"explain":        "执行计划（EXPLAIN <sql>，输出三元组）",
		"tableinfo":      "表结构 + 索引 + 段大小",
		"info":           "实例信息（V$INSTANCE / V$VERSION / V$DATABASE）",
		"health":         "健康总览（多个视图聚合）",
		"alert":          "告警事件（V$DEADLOCK_HISTORY / V$DANGER_EVENT / V$RUNTIME_ERR_HISTORY）",
		"anomalies":      "当前异常上下文快照 (诊断起手, is_anomaly+anomaly_signals+next_step_hint)",
		"errcode":        "DM 错误码字典查询 (V$ERR_INFO, /errcode 9042 或 /errcode locked)",
		"views":          "DM V$ 动态视图自动发现 (按主题分类, /views 或 /views session)",
	}
	return hints[name]
}

// ToolFilter returns a function that filters tools by mode.
// auto/assist: all tools available; playbook: only read-only.
func (p *DMProfile) ToolFilter(mode string) func(name string, securityLevel int) bool {
	switch mode {
	case "playbook":
		return func(_ string, level int) bool { return level == 0 }
	default:
		return func(_ string, _ int) bool { return true }
	}
}

// DefaultMaxTurns returns the default max turns for the given mode.
func (p *DMProfile) DefaultMaxTurns(mode string) int {
	switch mode {
	case "auto":
		return 20
	case "assist":
		return 1
	case "playbook":
		return 1
	default:
		return 20
	}
}
