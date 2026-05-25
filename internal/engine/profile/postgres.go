/*-------------------------------------------------------------------------
 *
 * postgres.go
 *	  PostgresProfile provides PostgreSQL-specific knowledge to the
 *	  Engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/postgres.go
 *
 *-------------------------------------------------------------------------
 */
package profile



// PostgresProfile provides PostgreSQL-specific knowledge to the Engine.
type PostgresProfile struct{}

func (p *PostgresProfile) Product() string { return "postgres" }

func (p *PostgresProfile) SystemPromptRules() string {
	return `# PostgreSQL 数据库特定知识

## 对象引用规则
- PostgreSQL 标识符默认小写
- 使用双引号保留大小写
- 引用表名/索引名前必须用 sql skill 查询 pg_class 确认存在

## 等待事件速查
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| LWLock:BufferContent | 共享缓冲区争用 | 并发访问同一数据页；可能需要增大 shared_buffers |
| Lock:transactionid | 事务 ID 锁 | 行锁等待；查 pg_locks 找阻塞源 |
| IO:DataFileRead | 数据文件读取 | 缺索引或 shared_buffers 不足 |
| IO:WALWrite | WAL 写入 | 提交频繁或磁盘 I/O 慢 |
| Lock:tuple | 元组锁 | 多会话竞争更新同一行 |

## MVCC 特有知识（PostgreSQL 独有！）
- 死元组需要 VACUUM 清理，长事务会阻止 VACUUM 回收空间
- XID wraparound 是关键风险 — 用 xid skill 检查 xid_age
- VACUUM 清理进度用 vacuum skill 查看
- bloat（膨胀）用 bloat skill 检查表和索引膨胀率
- 长事务用 longtx skill 检查

## 关键视图
- pg_stat_activity → 会话和等待状态
- pg_stat_statements → SQL 统计（需扩展）
- pg_locks → 锁信息
- pg_stat_user_tables → 表统计（seq_scan, idx_scan 等）

## 参数修改注意
- ALTER SYSTEM SET 修改 → SELECT pg_reload_conf() 生效
- 部分参数需要重启（如 shared_buffers, max_connections）
- pg_settings 视图查看参数来源（configuration file / override 等）

## 修复建议安全规则（必须遵守）
- CREATE INDEX → 始终建议 CREATE INDEX CONCURRENTLY（除非明确是空表或紧急恢复场景）
- REINDEX → 始终建议 REINDEX CONCURRENTLY（PG 12+）
- VACUUM FULL → 警告：持有 AccessExclusiveLock，阻塞所有操作包括 SELECT。建议 pg_repack 替代
- ALTER TABLE ADD COLUMN → 提醒：可能需要 AccessExclusiveLock（取决于是否有 DEFAULT），建议低峰期执行
- 修改 pg_hba.conf → 提醒：先备份原文件再修改
- 修改 shared_buffers/max_connections 等 → 提醒：需要重启数据库服务

## 连接数异常排查流程
连接数异常时，按以下流程排查：
1. 先查连接频率 → 连接创建/断开速度是否异常（短连接风暴特征：连接数正常但 backend_start 都很新）
2. 短连接风暴 → 建议引入连接池（PgBouncer transaction 模式）
3. 长连接占满 → 查是否有连接泄漏（idle 连接长时间不释放），设置 idle_in_transaction_session_timeout
4. 常见连接池配置建议：PgBouncer（transaction pooling, pool_size=CPU*2~4）或 Pgpool-II`
}

func (p *PostgresProfile) ToolUsageHint(name string) string {
	hints := map[string]string{
		"activesessions": "诊断入口：查看当前活跃会话和等待状态",
		"waits":          "诊断入口：查看等待事件分布",
		"topsql":         "SQL 问题入口：按执行时间排名的 Top SQL",
		"slowsql":        "慢查询入口：超过阈值的慢查询",
		"explain":        "深度分析：查看 SQL 执行计划",
		"locks":          "锁问题入口：查看行锁/表锁",
		"blocktree":      "锁深度分析：查看阻塞链",
		"health":         "综合巡检：数据库健康检查",
		"space":          "空间问题入口：表空间使用率",
		"sharedbufs":     "内存分析：shared_buffers 命中率和使用情况",
		"wal":            "WAL 分析：WAL 生成速率和归档状态",
		"replication":    "主从状态：流复制延迟和状态",
		"vacuum":         "MVCC 维护：VACUUM 进度和死元组",
		"xid":            "XID 风险：事务 ID 年龄和 wraparound 风险",
		"bloat":          "膨胀检查：表和索引膨胀率",
		"longtx":         "长事务：运行时间过长的事务",
		"slots":          "复制槽：复制槽状态和 WAL 保留",
		"kill":           "终止会话：pg_terminate_backend（需确认）",
		"trace":          "源码级分析：采集 OS 堆栈 + 火焰图，用于分析内核瓶颈",
		"cancel":         "取消查询：pg_cancel_backend（需确认）",
		"sql":            "最后手段：直接执行 SQL 查询",
	}
	return hints[name]
}

func (p *PostgresProfile) ToolFilter(mode string) func(string, int) bool {
	return func(name string, securityLevel int) bool {
		switch mode {
		case "playbook":
			return false
		case "assist":
			return securityLevel <= 0
		case "auto":
			return true
		default:
			return securityLevel <= 0
		}
	}
}

func (p *PostgresProfile) DefaultMaxTurns(mode string) int {
	switch mode {
	case "playbook":
		return 1
	case "assist":
		return 10
	case "auto":
		return 20
	default:
		return 10
	}
}
