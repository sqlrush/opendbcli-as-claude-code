# 09 — PromptProfile 设计

## 职责

PromptProfile 是 Engine 与 DB 特定逻辑之间的桥梁。
Engine 不知道 Oracle 和 MySQL 的区别，它只通过 PromptProfile 获取：

1. 系统提示中的 DB 特定规则
2. 可用工具列表和过滤策略
3. 工具使用场景提示
4. 诊断报告格式

## 接口定义

```go
package profile

type PromptProfile interface {
    // DB 标识
    Product() string  // "oracle" / "mysql" / "postgres" / "opengauss"

    // 系统提示中的 DB 特定规则
    SystemPromptRules() string

    // 工具注册表
    ToolRegistry() skill.Registry

    // 工具过滤器（按诊断模式）
    ToolFilter(mode engine.DiagnoseMode) func(skill.Skill) bool

    // 工具使用场景提示（动态工具描述增强）
    ToolUsageHint(skillName string) string

    // 诊断报告压缩（DB 特定的数据格式化）
    CompressReport(rawReport any) string

    // 默认最大轮次（不同 DB 可能不同）
    DefaultMaxTurns(mode engine.DiagnoseMode) int
}
```

## Oracle Profile 实现

```go
type OracleProfile struct {
    registry skill.Registry
}

func NewOracleProfile(registry skill.Registry) *OracleProfile

func (p *OracleProfile) Product() string { return "oracle" }

func (p *OracleProfile) SystemPromptRules() string {
    return `# Oracle 特定规则

## 对象引用
- Oracle 对象名默认大写（USER, 不是 user）
- 引用表名/索引名前必须用 sql skill 查询 DBA_OBJECTS 确认存在
- ISEQ$$_ 开头的序列是 identity column 自动生成的，需要 ALTER TABLE ... MODIFY ... GENERATED AS IDENTITY
- 注意区分 CDB/PDB 环境

## 等待事件解读
- db file sequential read → 单块 I/O（索引扫描或随机读）
- db file scattered read → 多块 I/O（全表扫描）
- log file sync → redo 写入等待（commit 频繁或 I/O 慢）
- enq: TX - row lock contention → 行锁争用（查 blocktree）
- cursor: pin S wait on X → 硬解析争用（查共享池）
- latch free → latch 争用（查 latches）
- direct path read/write temp → 临时空间排序溢出

## 关键视图
- v$session, v$active_session_history → 会话和历史
- v$sql, v$sqlarea → SQL 统计
- v$sql_plan → 执行计划
- dba_hist_sqlstat → AWR 历史 SQL
- v$lock, v$locked_object → 锁信息
- dba_segments → 段空间
- v$osstat → OS 级指标

## 参数修改注意
- 区分 MEMORY(立即生效) 和 SPFILE(需重启) scope
- SGA/PGA 参数修改可能需要重启
- 隐含参数以 _ 开头，谨慎修改`
}

func (p *OracleProfile) ToolFilter(mode engine.DiagnoseMode) func(skill.Skill) bool {
    return func(s skill.Skill) bool {
        switch mode {
        case engine.ModePlaybook:
            return false // playbook 不需要工具
        case engine.ModeAssist:
            return s.SecurityLevel() <= 0 // 只读
        case engine.ModeAuto:
            return true // 所有工具
        default:
            return s.SecurityLevel() <= 0
        }
    }
}

func (p *OracleProfile) ToolUsageHint(name string) string {
    hints := map[string]string{
        "activesessions": "诊断入口：查看当前活跃会话和等待事件分布",
        "waits":          "诊断入口：查看非 idle 等待事件排名，定位性能瓶颈方向",
        "topsql":         "SQL问题入口：按 elapsed time 排名的 Top SQL",
        "slowsql":        "慢查询入口：超过阈值的 SQL（可指定阈值如 slowsql 5000）",
        "explain":        "深度分析：查看 SQL 执行计划，需要 sql_id 参数",
        "locks":          "锁问题入口：查看行锁/表锁",
        "blocktree":      "锁深度分析：查看完整阻塞链（谁阻塞了谁）",
        "tableinfo":      "对象分析：查看表结构、索引、统计信息",
        "ash":            "历史分析：最近 N 分钟的活跃会话采样",
        "awr":            "趋势分析：历史性能对比，回答'什么时候开始慢的'",
        "planhistory":    "计划变化：检测执行计划是否发生回归",
        "space":          "空间问题入口：表空间使用率",
        "segments":       "空间深度分析：哪个表/索引占空间最多",
        "os":             "系统层面：CPU/内存/IO/网络指标",
        "alert":          "错误排查：Oracle 告警日志中的 ORA- 错误",
        "params":         "配置查询：数据库参数值和来源",
        "health":         "综合巡检：24 项健康检查指标",
        "kill":           "应急操作：终止阻塞会话（需确认）",
        "alter":          "参数修改：ALTER SYSTEM SET（需确认）",
        "resize":         "空间扩容：表空间添加 datafile（需确认）",
        "sql":            "最后手段：直接执行 SQL 查询（优先用专用 skill）",
    }
    return hints[name]
}

func (p *OracleProfile) DefaultMaxTurns(mode engine.DiagnoseMode) int {
    switch mode {
    case engine.ModePlaybook:
        return 1
    case engine.ModeAssist:
        return 10
    case engine.ModeAuto:
        return 20
    default:
        return 10
    }
}
```

## MySQL Profile 实现（示例，简化）

```go
type MySQLProfile struct {
    registry skill.Registry
}

func (p *MySQLProfile) Product() string { return "mysql" }

func (p *MySQLProfile) SystemPromptRules() string {
    return `# MySQL 特定规则

## 对象引用
- MySQL 默认大小写取决于 lower_case_table_names 设置
- 使用反引号引用含特殊字符的标识符

## 等待事件解读
- Waiting for table metadata lock → DDL 被 DML 阻塞
- Waiting for table flush → FLUSH TABLE 等待活跃查询
- innodb_lock_wait → InnoDB 行锁等待

## 关键视图
- information_schema.PROCESSLIST → 会话
- performance_schema.events_waits_summary_global_by_event_name → 等待事件
- sys.innodb_lock_waits → 锁等待
- information_schema.TABLES → 表信息

## 参数修改注意
- 区分 SESSION 和 GLOBAL scope
- 持久化需要 SET PERSIST（8.0+）
- 部分参数修改需要重启`
}
```

## PostgreSQL Profile 实现（示例，简化）

```go
type PostgresProfile struct {
    registry skill.Registry
}

func (p *PostgresProfile) Product() string { return "postgres" }

func (p *PostgresProfile) SystemPromptRules() string {
    return `# PostgreSQL 特定规则

## 对象引用
- PostgreSQL 标识符默认小写
- 使用双引号保留大小写

## 等待事件解读
- LWLock:BufferContent → 共享缓冲区争用
- Lock:transactionid → 事务 ID 锁（行锁）
- IO:DataFileRead → 数据文件读取

## 关键视图
- pg_stat_activity → 会话
- pg_stat_statements → SQL 统计
- pg_locks → 锁信息
- pg_stat_user_tables → 表统计

## 特殊注意
- MVCC: 死元组需要 VACUUM 清理
- 长事务会阻止 VACUUM 回收空间
- XID wraparound 是关键风险（检查 xid_age）
- 分区表需要特殊处理`
}
```

## Profile 工厂

```go
func NewProfile(product string, registry skill.Registry) PromptProfile {
    switch product {
    case "oracle":
        return NewOracleProfile(registry)
    case "mysql":
        return NewMySQLProfile(registry)
    case "postgres":
        return NewPostgresProfile(registry)
    case "opengauss":
        return NewOpenGaussProfile(registry)
    default:
        return NewGenericProfile(registry) // 通用 fallback
    }
}
```
