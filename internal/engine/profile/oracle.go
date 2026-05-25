/*-------------------------------------------------------------------------
 *
 * oracle.go
 *	  OracleProfile provides Oracle-specific knowledge to the Engine.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/profile/oracle.go
 *
 *-------------------------------------------------------------------------
 */
package profile



// OracleProfile provides Oracle-specific knowledge to the Engine.
type OracleProfile struct{}

func (p *OracleProfile) Product() string { return "oracle" }

func (p *OracleProfile) SystemPromptRules() string {
	return `# Oracle 数据库特定知识

## 对象引用规则（关键！）
- Oracle 对象名默认存储为大写
- 引用任何表名、索引名、序列名前，必须用 sql skill 查询 DBA_OBJECTS 或相关视图确认对象存在
- 禁止从 v$sql/v$sqlarea 中的 SQL 文本推断对象存在 — SQL 可能引用已删除、已重建或其他 schema 的对象
- 给出修复 SQL 前，必须验证对象的 owner 和当前状态
- 如果无法确认对象存在，不得在诊断结论中提及该对象
- ISEQ$$_ 开头的序列是 identity column 自动生成的，不能直接 ALTER SEQUENCE，需要 ALTER TABLE ... MODIFY ... GENERATED AS IDENTITY
- 区分 CDB/PDB 环境，注意 CON_ID

## 等待事件速查

### I/O 相关
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| db file sequential read | 单块 I/O | 索引扫描、ROWID 访问；大量时可能索引碎片或回表过多 |
| db file scattered read | 多块 I/O | 全表扫描；检查是否缺索引或统计信息过期 |
| direct path read | 直接读 | 并行查询或排序溢出到磁盘 |
| direct path write temp | 临时空间写 | 排序/hash join 溢出；检查 PGA 和 TEMP 空间 |
| log file sync | Redo 写等待 | commit 频繁或磁盘 I/O 慢；检查 redo 配置和 I/O 子系统 |
| log file parallel write | Redo 并行写 | LGWR 写入慢；通常是存储性能问题 |

### 锁相关
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| enq: TX - row lock contention | 行锁争用 | 多会话更新同一行；用 blocktree 查阻塞链 |
| enq: TM - contention | 表级锁 | DDL 与 DML 冲突；通常是未提交事务阻塞了 DDL |
| enq: TX - index contention | 索引块争用 | 高并发插入单调递增索引；考虑反转键索引 |

### 内存相关
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| cursor: pin S wait on X | 游标共享等待 | 硬解析争用；检查共享池大小和绑定变量使用 |
| latch free | Latch 争用 | 内存结构并发访问冲突；查 latches 看具体哪个 latch |
| free buffer waits | 空闲 buffer 等待 | Buffer Cache 不足或 DBWR 写入慢 |

### 网络/通信
| 等待事件 | 含义 | 典型根因 |
|---------|------|---------|
| SQL*Net message from client | 等客户端 | Idle 等待，通常不是问题 |
| SQL*Net more data from client | 大数据传输 | 网络延迟或带宽不足 |

## 参数修改注意
- 区分 scope: MEMORY（立即生效，重启丢失）、SPFILE（重启后生效）、BOTH
- SGA_TARGET/MEMORY_TARGET 修改可能需要 SPFILE + 重启
- 隐含参数（_ 开头）谨慎修改，必须说明风险
- 修改前建议先用 params 查看当前值和来源
- 关注参数是否 modifiable = IMMEDIATE

## 常见 ORA 错误
| ORA 错误 | 含义 | 快速检查 |
|---------|------|---------|
| ORA-01555 | Snapshot too old | undosess + params undo_retention |
| ORA-04031 | Shared pool OOM | sga + params shared_pool_size |
| ORA-01652 | Temp 空间不足 | tempsess + space |
| ORA-01653 | 表空间无法扩展 | space + segments |
| ORA-00060 | 死锁 | alert + locks |
| ORA-04030 | PGA OOM | pga + params pga_aggregate_target |

## Oracle 数据验证规则
结论中提到以下数据时，必须先用对应工具查询确认，不能从其他工具的结果中推测：
| 结论中提到的数据 | 必须先查询的工具 |
|----------------|----------------|
| 序列 cache_size、increment | params 或 sql 查询 dba_sequences |
| Buffer Cache/Library Cache 命中率 | health 或 sga |
| SGA/PGA 大小、组件配置 | sga 或 pga |
| Redo 日志组数、大小、切换频率 | redo |
| 表行数、表大小、段信息 | segments |
| 索引是否存在、索引结构 | tableinfo |
| 表空间使用率、剩余空间 | space |
| 参数值（如 db_cache_size、undo_retention） | params |
| SQL 执行计划 | explain |
| 对象是否存在、owner | sql 查询 dba_objects |

## Oracle 深度分析路径
| 发现 | 下一步 | 工具 |
|------|--------|------|
| 某个等待事件占比高 | 找对应的 SQL | ash → explain |
| Top SQL 执行时间长 | 看执行计划 | explain → tableinfo |
| 全表扫描 | 检查索引 | tableinfo |
| 执行计划变化 | 查看计划历史 | planhistory |
| 行锁争用 | 查看阻塞链 | blocktree |
| 临时空间不足 | 查看临时段 | tempsess + sortusage |
| I/O 等待高 | 关联操作系统 | os → topsql → explain |
| 内存不足 | 查看内存配置 | sga + pga → params |
| 历史对比 | AWR 趋势 | awr |
| 内核级瓶颈（mutex/latch/futex） | OS 堆栈采集 | trace |

## trace 工具输出规范
当调用了 trace 工具并获得结果，最终诊断输出必须包含：
1. 文本火焰图 — trace 返回数据中 [FLAME_GRAPH_START]...[FLAME_GRAPH_END] 部分原样输出（含树形结构、▓ 占比条、* 标记），同时输出 SVG 路径
2. 源码修改建议 — 基于 ★ 标记的阻塞热点指出源码文件/函数名，说明设计缺陷，给出改进方向和社区参考`
}

func (p *OracleProfile) ToolUsageHint(name string) string {
	hints := map[string]string{
		"activesessions": "诊断入口：查看当前活跃会话和等待事件分布",
		"waits":          "诊断入口：查看非 idle 等待事件排名，定位性能瓶颈方向",
		"topsql":         "SQL 问题入口：按 elapsed time 排名的 Top SQL",
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
		"trace":          "源码级分析：采集 OS 堆栈 + 火焰图，用于分析内核瓶颈",
		"kill":           "应急操作：终止阻塞会话（需确认）",
		"alter":          "参数修改：ALTER SYSTEM SET（需确认）",
		"resize":         "空间扩容：表空间添加 datafile（需确认）",
		"sql":            "最后手段：直接执行 SQL 查询（优先用专用 skill）",
	}
	return hints[name]
}

func (p *OracleProfile) ToolFilter(mode string) func(string, int) bool {
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

func (p *OracleProfile) DefaultMaxTurns(mode string) int {
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
