/*-------------------------------------------------------------------------
 *
 * mysql.go
 *	  MySQLProfile provides MySQL-specific knowledge to the Engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/mysql.go
 *
 *-------------------------------------------------------------------------
 */
package profile



// MySQLProfile provides MySQL-specific knowledge to the Engine.
type MySQLProfile struct{}

func (p *MySQLProfile) Product() string { return "mysql" }

func (p *MySQLProfile) SystemPromptRules() string {
	return `# MySQL 数据库特定知识

## 对象引用规则
- MySQL 大小写取决于 lower_case_table_names 设置和操作系统
- 使用反引号引用含特殊字符的标识符
- 引用表名/索引名前必须用 sql skill 查询 information_schema 确认存在

## 等待事件速查
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| Waiting for table metadata lock | MDL 等待 | DDL 被 DML 阻塞或长事务持有 MDL |
| Waiting for table flush | 表刷新等待 | FLUSH TABLE 等待活跃查询完成 |
| innodb_lock_wait | InnoDB 行锁 | 多会话更新同一行；查 innodb 锁等待 |
| Waiting for handler commit | 提交等待 | 存储引擎提交延迟 |

## 关键视图
- information_schema.PROCESSLIST → 会话列表
- performance_schema.events_waits_summary_global_by_event_name → 等待事件
- sys.innodb_lock_waits → InnoDB 锁等待详情
- information_schema.TABLES → 表信息和行数估算

## 参数修改注意
- 区分 SESSION 和 GLOBAL scope
- 持久化需要 SET PERSIST（MySQL 8.0+）
- 部分参数修改需要重启（如 innodb_buffer_pool_size）
- SHOW VARIABLES 查看当前值`
}

func (p *MySQLProfile) ToolUsageHint(name string) string {
	hints := map[string]string{
		"activesessions": "诊断入口：查看当前活跃会话和等待状态",
		"waits":          "诊断入口：查看等待事件分布",
		"topsql":         "SQL 问题入口：按执行时间排名的 Top SQL",
		"slowsql":        "慢查询入口：超过阈值的慢查询",
		"explain":        "深度分析：查看 SQL 执行计划",
		"locks":          "锁问题入口：查看 InnoDB 行锁/MDL 锁",
		"blocktree":      "锁深度分析：查看阻塞链",
		"health":         "综合巡检：数据库健康检查",
		"space":          "空间问题入口：表空间和表大小",
		"bufferpool":     "内存分析：InnoDB Buffer Pool 使用情况",
		"replication":    "主从状态：复制延迟和状态",
		"innodb":         "引擎状态：InnoDB 引擎详细信息",
		"binlog":         "日志分析：Binlog 状态和位置",
		"deadlock":       "死锁分析：最近的 InnoDB 死锁信息",
		"trace":          "源码级分析：采集 OS 堆栈 + 火焰图，用于分析内核瓶颈",
		"kill":           "应急操作：终止会话（需确认）",
		"sql":            "最后手段：直接执行 SQL 查询",
	}
	return hints[name]
}

func (p *MySQLProfile) ToolFilter(mode string) func(string, int) bool {
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

func (p *MySQLProfile) DefaultMaxTurns(mode string) int {
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
