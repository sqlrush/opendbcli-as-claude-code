/*-------------------------------------------------------------------------
 *
 * ora_kb.go
 *	  ORAEntry represents a single ORA error knowledge base entry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/ora_kb.go
 *
 *-------------------------------------------------------------------------
 */
package query

// ORAEntry represents a single ORA error knowledge base entry.
type ORAEntry struct {
	Code     string   // "04031"
	Message  string   // English Oracle message
	Meaning  string   // Chinese meaning
	Severity string   // "HIGH", "MEDIUM", "LOW"
	Category string   // "内存", "空间", "锁", "SQL", "IO", "网络", "备份", "HA", "权限"
	Causes   []string // Common causes (Chinese)
	DiagCmds []string // OpenDB commands for diagnosis (e.g., "/sga — 查看Shared Pool")
	Fixes    []string // Fix recommendations (Chinese)
}

var oraKnowledgeBase = map[string]*ORAEntry{
	// ── 内存 (5) ──────────────────────────────────────────────

	"04031": {
		Code:     "04031",
		Message:  "unable to allocate N bytes of shared memory",
		Meaning:  "Shared Pool 无法分配内存",
		Severity: "HIGH",
		Category: "内存",
		Causes: []string{
			"Shared Pool 过小",
			"大量硬解析导致碎片化",
			"未使用绑定变量",
		},
		DiagCmds: []string{
			"/sga        查看 Shared Pool 大小和空闲比例",
			"/health     检查 Shared Pool Free%",
		},
		Fixes: []string{
			"ALTER SYSTEM SET shared_pool_size = 4G",
			"ALTER SYSTEM FLUSH SHARED_POOL (临时止血)",
			"应用层改用绑定变量",
		},
	},
	"04030": {
		Code:     "04030",
		Message:  "out of process memory when trying to allocate N bytes",
		Meaning:  "PGA 内存分配失败",
		Severity: "HIGH",
		Category: "内存",
		Causes: []string{
			"PGA 目标过小",
			"大量排序/哈希操作",
			"并发会话过多",
		},
		DiagCmds: []string{
			"/pga        查看 PGA 内存使用",
			"/health     检查 PGA 指标",
		},
		Fixes: []string{
			"ALTER SYSTEM SET pga_aggregate_target = 4G",
			"优化 SQL 减少排序和哈希操作",
		},
	},
	"04036": {
		Code:     "04036",
		Message:  "PGA memory used by the instance exceeds PGA_AGGREGATE_LIMIT",
		Meaning:  "PGA 超出限制",
		Severity: "HIGH",
		Category: "内存",
		Causes: []string{
			"单条 SQL 消耗过多 PGA",
			"并发会话 PGA 总量超限",
		},
		DiagCmds: []string{
			"/pga        查看 PGA 分配详情",
			"/topsql     找出消耗 PGA 最多的 SQL",
		},
		Fixes: []string{
			"优化 SQL 减少排序/哈希连接",
			"ALTER SYSTEM SET pga_aggregate_limit = 8G",
			"增大 pga_aggregate_target",
		},
	},
	"04035": {
		Code:     "04035",
		Message:  "unable to allocate N bytes of shared memory in pool",
		Meaning:  "PGA 内存分配失败 (池级别)",
		Severity: "HIGH",
		Category: "内存",
		Causes: []string{
			"PGA 目标过小",
			"大量排序/哈希操作",
		},
		DiagCmds: []string{
			"/pga        查看 PGA 内存使用",
			"/health     检查 PGA 指标",
		},
		Fixes: []string{
			"ALTER SYSTEM SET pga_aggregate_target = 4G",
			"优化 SQL 减少排序和哈希操作",
		},
	},
	"10635": {
		Code:     "10635",
		Message:  "invalid segment or tablespace type",
		Meaning:  "无效段或表空间类型 (内存上下文)",
		Severity: "MEDIUM",
		Category: "内存",
		Causes: []string{
			"Oracle 内部 Bug",
			"内存上下文损坏",
		},
		DiagCmds: []string{
			"/alert      查看 Alert Log 详情",
		},
		Fixes: []string{
			"查询 MOS 确认 Bug 编号",
			"打对应补丁",
		},
	},

	// ── 空间 (8) ──────────────────────────────────────────────

	"01652": {
		Code:     "01652",
		Message:  "unable to extend temp segment by N in tablespace",
		Meaning:  "TEMP 表空间不足",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"大排序/哈希操作占满 TEMP",
			"TEMP 表空间过小",
			"临时段未释放",
		},
		DiagCmds: []string{
			"/tempsess    查看 TEMP 占用会话",
			"/sortusage   查看排序段使用",
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"/resize TEMP add — 扩容 TEMP 表空间",
			"/kill 占用 TEMP 最多的会话",
			"优化 SQL 减少排序",
		},
	},
	"01653": {
		Code:     "01653",
		Message:  "unable to extend table by N in tablespace",
		Meaning:  "表无法扩展, 表空间已满",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"表空间数据文件已满",
			"未开启自动扩展",
			"磁盘空间不足",
		},
		DiagCmds: []string{
			"/space       查看表空间使用率",
			"/segments    查看 Top 段空间",
		},
		Fixes: []string{
			"/resize <表空间> add — 添加数据文件",
			"清理过期数据释放空间",
			"开启数据文件自动扩展",
		},
	},
	"01654": {
		Code:     "01654",
		Message:  "unable to extend index by N in tablespace",
		Meaning:  "索引无法扩展, 表空间已满",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"表空间数据文件已满",
			"索引空间增长过快",
		},
		DiagCmds: []string{
			"/space       查看表空间使用率",
			"/segments    查看 Top 段空间",
		},
		Fixes: []string{
			"/resize <表空间> add — 添加数据文件",
			"重建索引减少碎片",
			"开启数据文件自动扩展",
		},
	},
	"01688": {
		Code:     "01688",
		Message:  "unable to extend table partition in undo tablespace",
		Meaning:  "UNDO 段无法扩展",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"长事务占用 UNDO",
			"UNDO 表空间过小",
			"undo_retention 设置过高",
		},
		DiagCmds: []string{
			"/undosess    查看 UNDO 占用会话",
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"/resize UNDO add — 扩容 UNDO 表空间",
			"/kill 长事务会话",
			"降低 undo_retention",
		},
	},
	"01691": {
		Code:     "01691",
		Message:  "unable to extend lob segment in tablespace",
		Meaning:  "LOB 段无法扩展, 表空间已满",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"LOB 数据增长过快",
			"表空间已满",
		},
		DiagCmds: []string{
			"/space       查看表空间使用率",
			"/segments    查看 Top 段空间",
		},
		Fixes: []string{
			"/resize <表空间> add — 添加数据文件",
			"清理过期 LOB 数据",
		},
	},
	"01536": {
		Code:     "01536",
		Message:  "space quota exceeded for tablespace",
		Meaning:  "超出空间配额",
		Severity: "MEDIUM",
		Category: "空间",
		Causes: []string{
			"用户表空间配额不足",
			"未授予 UNLIMITED TABLESPACE",
		},
		DiagCmds: []string{
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"ALTER USER <user> QUOTA UNLIMITED ON <tablespace>",
			"GRANT UNLIMITED TABLESPACE TO <user>",
		},
	},
	"01560": {
		Code:     "01560",
		Message:  "error in specifying segment for shrink",
		Meaning:  "段收缩错误",
		Severity: "LOW",
		Category: "空间",
		Causes: []string{
			"收缩期间有并发 DML",
			"段不支持在线收缩",
		},
		DiagCmds: []string{
			"/segments    查看段空间信息",
		},
		Fixes: []string{
			"业务低峰期重试 SHRINK SPACE",
			"先启用行移动: ALTER TABLE ENABLE ROW MOVEMENT",
		},
	},
	"30036": {
		Code:     "30036",
		Message:  "unable to extend segment by N in undo tablespace",
		Meaning:  "UNDO 表空间段无法扩展",
		Severity: "HIGH",
		Category: "空间",
		Causes: []string{
			"长事务占用 UNDO",
			"UNDO 表空间过小",
			"大批量 DML 操作",
		},
		DiagCmds: []string{
			"/undosess    查看 UNDO 占用会话",
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"/resize UNDO add — 扩容 UNDO 表空间",
			"/kill 长事务会话",
			"分批提交大事务",
		},
	},

	// ── 锁/死锁 (5) ──────────────────────────────────────────

	"00060": {
		Code:     "00060",
		Message:  "deadlock detected while waiting for resource",
		Meaning:  "检测到死锁",
		Severity: "HIGH",
		Category: "锁",
		Causes: []string{
			"应用并发设计问题",
			"无序锁定导致循环等待",
			"事务粒度过大",
		},
		DiagCmds: []string{
			"/locks       查看当前锁信息",
			"/blocktree   查看层级阻塞链",
		},
		Fixes: []string{
			"检查应用锁定顺序, 保证一致",
			"减小事务粒度",
			"缩短事务持锁时间",
		},
	},
	"00054": {
		Code:     "00054",
		Message:  "resource busy and acquire with NOWAIT specified or timeout expired",
		Meaning:  "资源繁忙, 锁等待超时",
		Severity: "HIGH",
		Category: "锁",
		Causes: []string{
			"DDL 锁冲突",
			"长事务持锁不释放",
			"并发 DDL 操作",
		},
		DiagCmds: []string{
			"/locks       查看当前锁信息",
			"/blocktree   查看层级阻塞链",
		},
		Fixes: []string{
			"/kill 阻塞者会话",
			"错峰执行 DDL 操作",
			"缩短持锁事务",
		},
	},
	"04020": {
		Code:     "04020",
		Message:  "deadlock detected while trying to lock object",
		Meaning:  "Library Cache Lock 死锁",
		Severity: "HIGH",
		Category: "锁",
		Causes: []string{
			"DDL 并发冲突",
			"包编译时有并发调用",
		},
		DiagCmds: []string{
			"/locks       查看当前锁信息",
			"/latches     查看 Latch 争用",
		},
		Fixes: []string{
			"错峰执行 DDL",
			"避免在高峰期编译包",
		},
	},
	"02049": {
		Code:     "02049",
		Message:  "timeout: distributed transaction waiting for lock",
		Meaning:  "分布式事务死锁超时",
		Severity: "HIGH",
		Category: "锁",
		Causes: []string{
			"DB Link 跨库事务死锁",
			"远端数据库响应慢",
		},
		DiagCmds: []string{
			"/locks       查看当前锁信息",
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"检查分布式事务设计",
			"减少跨库事务依赖",
			"增大 distributed_lock_timeout",
		},
	},
	"00051": {
		Code:     "00051",
		Message:  "timeout occurred while waiting for a resource",
		Meaning:  "等待资源超时",
		Severity: "MEDIUM",
		Category: "锁",
		Causes: []string{
			"锁竞争严重",
			"资源被长期占用",
		},
		DiagCmds: []string{
			"/locks       查看当前锁信息",
		},
		Fixes: []string{
			"优化并发设计",
			"/kill 长事务会话",
		},
	},

	// ── Undo/一致性 (5) ──────────────────────────────────────

	"01555": {
		Code:     "01555",
		Message:  "snapshot too old: rollback segment number N with name N too small",
		Meaning:  "快照过旧",
		Severity: "HIGH",
		Category: "Undo",
		Causes: []string{
			"UNDO 表空间过小",
			"长查询期间有频繁 DML",
			"undo_retention 设置过低",
		},
		DiagCmds: []string{
			"/undosess    查看 UNDO 占用会话",
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"增大 undo_retention (如 3600)",
			"增大 UNDO 表空间",
			"优化长查询减少执行时间",
		},
	},
	"01628": {
		Code:     "01628",
		Message:  "max # extents reached for rollback segment",
		Meaning:  "回滚段最大区间数超限",
		Severity: "MEDIUM",
		Category: "Undo",
		Causes: []string{
			"undo_retention 设置过高",
			"UNDO 表空间区间管理不当",
		},
		DiagCmds: []string{
			"/undosess    查看 UNDO 占用",
			"/params undo 查看 UNDO 参数",
		},
		Fixes: []string{
			"调整 undo_retention 为合理值",
			"扩容 UNDO 表空间",
		},
	},
	"30039": {
		Code:     "30039",
		Message:  "undo is exhausted",
		Meaning:  "UNDO 已用尽",
		Severity: "HIGH",
		Category: "Undo",
		Causes: []string{
			"UNDO 表空间已满",
			"大量并发 DML",
			"长事务占用 UNDO",
		},
		DiagCmds: []string{
			"/undosess    查看 UNDO 占用会话",
			"/space       查看表空间使用率",
		},
		Fixes: []string{
			"/resize UNDO add — 扩容 UNDO 表空间",
			"/kill 长事务会话",
			"分批提交大事务",
		},
	},
	"01578": {
		Code:     "01578",
		Message:  "ORACLE data block corrupted (file # N, block # N)",
		Meaning:  "数据块损坏",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"磁盘故障",
			"存储层错误",
			"内存损坏写入",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log 详情",
		},
		Fixes: []string{
			"RMAN BLOCKRECOVER 恢复损坏块",
			"检查存储硬件",
			"开启 DB_BLOCK_CHECKING 预防",
		},
	},
	"01110": {
		Code:     "01110",
		Message:  "data file N does not exist",
		Meaning:  "数据文件不存在",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"数据文件被误删",
			"文件被移动到其他位置",
			"存储挂载丢失",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
			"/space       查看表空间状态",
		},
		Fixes: []string{
			"从备份恢复数据文件",
			"检查存储挂载状态",
			"ALTER DATABASE CREATE DATAFILE (如有备份)",
		},
	},

	// ── SQL/解析 (8) ──────────────────────────────────────────

	"01000": {
		Code:     "01000",
		Message:  "maximum open cursors exceeded",
		Meaning:  "超过最大游标数",
		Severity: "HIGH",
		Category: "SQL",
		Causes: []string{
			"应用未关闭游标 (游标泄漏)",
			"open_cursors 参数过小",
			"PL/SQL 中循环打开游标未关闭",
		},
		DiagCmds: []string{
			"/params open_cursors  查看游标限制",
			"/resource             查看资源使用",
		},
		Fixes: []string{
			"修复应用游标泄漏",
			"ALTER SYSTEM SET open_cursors = 1000",
			"检查 PL/SQL 代码确保游标关闭",
		},
	},
	"00904": {
		Code:     "00904",
		Message:  "invalid identifier",
		Meaning:  "无效列名",
		Severity: "LOW",
		Category: "SQL",
		Causes: []string{
			"SQL 中列名拼写错误",
			"列名未加双引号 (大小写敏感)",
		},
		DiagCmds: []string{
			"/tableinfo <表名>  查看表结构确认列名",
		},
		Fixes: []string{
			"检查并修正 SQL 中的列名",
			"使用 /tableinfo 确认正确列名",
		},
	},
	"00936": {
		Code:     "00936",
		Message:  "missing expression",
		Meaning:  "缺少表达式",
		Severity: "LOW",
		Category: "SQL",
		Causes: []string{
			"SQL 语法错误",
			"SELECT 列表不完整",
			"WHERE 条件缺失表达式",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"检查 SQL 语法",
			"确认 SELECT/WHERE/ORDER BY 子句完整",
		},
	},
	"01722": {
		Code:     "01722",
		Message:  "invalid number",
		Meaning:  "无效数字 (隐式转换失败)",
		Severity: "MEDIUM",
		Category: "SQL",
		Causes: []string{
			"隐式类型转换失败",
			"字符列与数字比较",
			"数据中含有非数字字符",
		},
		DiagCmds: []string{
			"/topsql      找出相关 SQL",
		},
		Fixes: []string{
			"使用显式 TO_NUMBER 转换",
			"修正数据类型匹配",
			"清理脏数据",
		},
	},
	"01476": {
		Code:     "01476",
		Message:  "divisor is equal to zero",
		Meaning:  "除数为零",
		Severity: "LOW",
		Category: "SQL",
		Causes: []string{
			"SQL 中除数可能为零",
			"缺少零值保护",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"使用 NULLIF(divisor, 0) 防止除零",
			"添加 CASE WHEN divisor = 0 THEN NULL",
		},
	},
	"00942": {
		Code:     "00942",
		Message:  "table or view does not exist",
		Meaning:  "表或视图不存在",
		Severity: "MEDIUM",
		Category: "SQL",
		Causes: []string{
			"对象名拼写错误",
			"缺少 Schema 前缀",
			"用户无权访问该对象",
		},
		DiagCmds: []string{
			"/tableinfo <表名>  确认对象是否存在",
		},
		Fixes: []string{
			"检查对象名和 Schema 前缀",
			"GRANT SELECT ON <owner>.<table> TO <user>",
			"创建同义词: CREATE SYNONYM",
		},
	},
	"06502": {
		Code:     "06502",
		Message:  "PL/SQL: numeric or value error",
		Meaning:  "PL/SQL 数字或值错误",
		Severity: "MEDIUM",
		Category: "SQL",
		Causes: []string{
			"变量赋值溢出",
			"字符串超出声明长度",
			"数值超出精度范围",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"检查 PL/SQL 变量声明大小",
			"使用 SUBSTR 截断长字符串",
			"增大变量精度/长度",
		},
	},
	"01400": {
		Code:     "01400",
		Message:  "cannot insert NULL into (table.column)",
		Meaning:  "不能插入 NULL 到非空列",
		Severity: "LOW",
		Category: "SQL",
		Causes: []string{
			"NOT NULL 约束限制",
			"INSERT 语句缺少必填列",
		},
		DiagCmds: []string{
			"/tableinfo <表名>  查看列的 NOT NULL 约束",
		},
		Fixes: []string{
			"INSERT 时提供非 NULL 值",
			"检查是否可以修改约束: ALTER TABLE MODIFY (col NULL)",
		},
	},

	// ── IO/数据文件 (6) ──────────────────────────────────────

	"01116": {
		Code:     "01116",
		Message:  "error in opening database file",
		Meaning:  "无法打开数据文件",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"数据文件丢失或损坏",
			"文件权限不正确",
			"存储挂载丢失",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"检查文件是否存在及权限",
			"恢复数据文件",
			"检查存储挂载状态",
		},
	},
	"27037": {
		Code:     "27037",
		Message:  "unable to obtain file status",
		Meaning:  "无法获取文件状态",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"磁盘/NFS 问题",
			"文件系统故障",
			"存储路径不可达",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
			"/os          查看操作系统指标",
		},
		Fixes: []string{
			"检查存储连通性",
			"检查 NFS 挂载状态",
			"联系存储管理员",
		},
	},
	"19809": {
		Code:     "19809",
		Message:  "limit exceeded for recovery files",
		Meaning:  "超过恢复文件限制 (FRA 满)",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"FRA 空间已满",
			"归档日志积压",
			"备份未清理",
		},
		DiagCmds: []string{
			"/fra         查看 FRA 使用详情",
		},
		Fixes: []string{
			"清理过期归档: RMAN DELETE ARCHIVELOG",
			"/resize FRA — 扩容 FRA",
			"调整备份保留策略",
		},
	},
	"01187": {
		Code:     "01187",
		Message:  "cannot read from file because it failed verification tests",
		Meaning:  "无法读取文件",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"IO 错误",
			"文件校验失败",
			"磁盘故障",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"检查存储硬件",
			"从备份恢复文件",
			"运行 DBVERIFY 检查文件",
		},
	},
	// 01578 already defined above in Undo/一致性 section
	"65535": {
		Code:     "65535",
		Message:  "generic I/O error",
		Meaning:  "通用 IO 错误",
		Severity: "HIGH",
		Category: "IO",
		Causes: []string{
			"存储层错误",
			"磁盘/控制器故障",
			"NFS/SAN 超时",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
			"/os          查看操作系统指标",
		},
		Fixes: []string{
			"检查存储硬件状态",
			"检查操作系统日志 (dmesg/messages)",
			"联系存储管理员",
		},
	},

	// ── 归档/Redo (5) ────────────────────────────────────────

	"00257": {
		Code:     "00257",
		Message:  "archiver error. Connect internal only, until freed",
		Meaning:  "归档错误, 数据库挂起",
		Severity: "HIGH",
		Category: "归档",
		Causes: []string{
			"归档目录已满",
			"FRA 空间不足",
			"归档进程异常",
		},
		DiagCmds: []string{
			"/fra         查看 FRA 使用",
			"/redo        查看 Redo 日志状态",
		},
		Fixes: []string{
			"清理过期归档日志",
			"/resize FRA — 扩容 FRA",
			"检查归档目录磁盘空间",
		},
	},
	"00255": {
		Code:     "00255",
		Message:  "error archiving log, archiver is stuck",
		Meaning:  "归档日志卡住",
		Severity: "HIGH",
		Category: "归档",
		Causes: []string{
			"归档进程卡住",
			"IO 性能差导致归档慢",
			"归档目录不可达",
		},
		DiagCmds: []string{
			"/fra         查看 FRA 使用",
			"/redo        查看 Redo 日志状态",
		},
		Fixes: []string{
			"检查 IO 性能",
			"清理归档目录空间",
			"ALTER SYSTEM ARCHIVE LOG ALL",
		},
	},
	"16038": {
		Code:     "16038",
		Message:  "log sequence# gap",
		Meaning:  "Redo 日志序列号间隔 (Standby GAP)",
		Severity: "HIGH",
		Category: "归档",
		Causes: []string{
			"Standby 日志传输中断",
			"网络故障导致日志丢失",
		},
		DiagCmds: []string{
			"/standby     查看 Data Guard 状态",
		},
		Fixes: []string{
			"手动传输缺失的归档日志到 Standby",
			"ALTER DATABASE RECOVER MANAGED STANDBY DATABASE DISCONNECT",
			"检查 log_archive_dest 配置",
		},
	},
	"00312": {
		Code:     "00312",
		Message:  "online log N thread N: member corrupted",
		Meaning:  "Redo Log 损坏",
		Severity: "HIGH",
		Category: "归档",
		Causes: []string{
			"磁盘故障",
			"Redo Log 文件损坏",
		},
		DiagCmds: []string{
			"/redo        查看 Redo 日志状态",
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"ALTER DATABASE CLEAR LOGFILE GROUP N",
			"如果是当前组: ALTER DATABASE CLEAR UNARCHIVED LOGFILE",
			"检查磁盘状态",
		},
	},
	"00313": {
		Code:     "00313",
		Message:  "open failed for members of log group",
		Meaning:  "无法打开 Redo Log 文件",
		Severity: "HIGH",
		Category: "归档",
		Causes: []string{
			"Redo Log 文件丢失",
			"文件权限不正确",
		},
		DiagCmds: []string{
			"/redo        查看 Redo 日志状态",
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"恢复 Redo Log 文件",
			"ALTER DATABASE CLEAR LOGFILE GROUP N",
			"检查文件权限",
		},
	},

	// ── 连接/网络 (6) ────────────────────────────────────────

	"03113": {
		Code:     "03113",
		Message:  "end-of-file on communication channel",
		Meaning:  "通信通道 EOF (连接中断)",
		Severity: "HIGH",
		Category: "网络",
		Causes: []string{
			"网络中断",
			"服务端进程被 kill",
			"OOM 导致进程被杀",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
			"/os          查看操作系统指标",
		},
		Fixes: []string{
			"检查网络连通性",
			"检查服务器 OOM 日志",
			"配置连接心跳: SQLNET.EXPIRE_TIME",
		},
	},
	"03114": {
		Code:     "03114",
		Message:  "not connected to ORACLE",
		Meaning:  "未连接到 ORACLE",
		Severity: "MEDIUM",
		Category: "网络",
		Causes: []string{
			"连接已断开",
			"会话超时",
		},
		DiagCmds: []string{
			"/conn        查看当前连接信息",
		},
		Fixes: []string{
			"重新连接: /login",
			"检查网络状态",
		},
	},
	"12541": {
		Code:     "12541",
		Message:  "TNS:no listener",
		Meaning:  "TNS 无监听器",
		Severity: "HIGH",
		Category: "网络",
		Causes: []string{
			"Listener 未启动",
			"Listener 配置错误",
			"端口被防火墙拦截",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"lsnrctl start — 启动监听",
			"lsnrctl status — 检查监听状态",
			"检查防火墙规则",
		},
	},
	"12514": {
		Code:     "12514",
		Message:  "TNS:listener does not currently know of service requested",
		Meaning:  "TNS 监听器不识别服务名",
		Severity: "MEDIUM",
		Category: "网络",
		Causes: []string{
			"服务名配置错误",
			"数据库未注册到监听",
			"实例未启动",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"检查 tnsnames.ora 服务名",
			"ALTER SYSTEM REGISTER — 手动注册服务",
			"lsnrctl services — 查看已注册服务",
		},
	},
	"12170": {
		Code:     "12170",
		Message:  "TNS:connect timeout occurred",
		Meaning:  "TNS 连接超时",
		Severity: "HIGH",
		Category: "网络",
		Causes: []string{
			"网络不通",
			"防火墙拦截",
			"目标主机无响应",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"检查网络连通性: ping/telnet",
			"检查防火墙规则",
			"检查目标端口是否监听",
		},
	},
	"12154": {
		Code:     "12154",
		Message:  "TNS:could not resolve the connect identifier specified",
		Meaning:  "TNS 无法解析连接标识符",
		Severity: "MEDIUM",
		Category: "网络",
		Causes: []string{
			"tnsnames.ora 配置错误",
			"TNS_ADMIN 路径不正确",
			"连接字符串拼写错误",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"检查 tnsnames.ora 配置",
			"确认 TNS_ADMIN 环境变量",
			"使用 Easy Connect 直连: host:port/service",
		},
	},

	// ── 备份/恢复 (4) ────────────────────────────────────────

	"19625": {
		Code:     "19625",
		Message:  "error identifying file",
		Meaning:  "RMAN 操作错误",
		Severity: "MEDIUM",
		Category: "备份",
		Causes: []string{
			"备份脚本配置问题",
			"备份路径不可达",
		},
		DiagCmds: []string{
			"/backup      查看备份历史",
		},
		Fixes: []string{
			"检查 RMAN 备份脚本",
			"检查备份路径权限和空间",
		},
	},
	"19566": {
		Code:     "19566",
		Message:  "exceeded recovery window of N days",
		Meaning:  "超过恢复窗口",
		Severity: "MEDIUM",
		Category: "备份",
		Causes: []string{
			"备份保留策略窗口过短",
			"旧备份未及时清理",
		},
		DiagCmds: []string{
			"/backup      查看备份历史",
		},
		Fixes: []string{
			"调整保留策略: CONFIGURE RETENTION POLICY TO RECOVERY WINDOW OF N DAYS",
			"RMAN DELETE OBSOLETE 清理过期备份",
		},
	},
	"19502": {
		Code:     "19502",
		Message:  "write error on file, block number",
		Meaning:  "无法读取/写入备份",
		Severity: "HIGH",
		Category: "备份",
		Causes: []string{
			"备份文件损坏或丢失",
			"存储空间不足",
		},
		DiagCmds: []string{
			"/backup      查看备份历史",
		},
		Fixes: []string{
			"RMAN CROSSCHECK BACKUP — 校验备份",
			"重新执行备份",
			"检查存储空间",
		},
	},
	"19563": {
		Code:     "19563",
		Message:  "header validation failed for member",
		Meaning:  "备份头校验失败",
		Severity: "MEDIUM",
		Category: "备份",
		Causes: []string{
			"备份不一致",
			"文件头损坏",
		},
		DiagCmds: []string{
			"/backup      查看备份历史",
		},
		Fixes: []string{
			"RMAN CROSSCHECK BACKUP",
			"RMAN DELETE EXPIRED BACKUP",
			"重新执行完整备份",
		},
	},

	// ── HA/DG (5) ────────────────────────────────────────────

	"16014": {
		Code:     "16014",
		Message:  "log N sequence# N not archived, no available destinations",
		Meaning:  "Redo 传输失败",
		Severity: "HIGH",
		Category: "HA",
		Causes: []string{
			"Standby 不可达",
			"网络故障",
			"归档目的地配置错误",
		},
		DiagCmds: []string{
			"/standby     查看 Data Guard 状态",
		},
		Fixes: []string{
			"检查网络和 Standby 状态",
			"ALTER SYSTEM SET log_archive_dest_state_2 = ENABLE",
			"检查 log_archive_dest_N 配置",
		},
	},
	"16816": {
		Code:     "16816",
		Message:  "incorrect database link type",
		Meaning:  "Standby 报错",
		Severity: "MEDIUM",
		Category: "HA",
		Causes: []string{
			"Standby 配置问题",
			"Data Guard 状态异常",
		},
		DiagCmds: []string{
			"/standby     查看 Data Guard 状态",
		},
		Fixes: []string{
			"检查 Standby Alert Log",
			"验证 Data Guard 配置",
		},
	},
	"01034": {
		Code:     "01034",
		Message:  "ORACLE not available",
		Meaning:  "ORACLE 实例未启动",
		Severity: "HIGH",
		Category: "HA",
		Causes: []string{
			"实例未启动或已崩溃",
			"OOM 导致实例中止",
			"后台进程异常终止",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"STARTUP — 启动实例",
			"检查 Alert Log 确认崩溃原因",
			"检查操作系统 OOM 日志",
		},
	},
	"01033": {
		Code:     "01033",
		Message:  "ORACLE initialization or shutdown in progress",
		Meaning:  "实例正在初始化或关闭中",
		Severity: "MEDIUM",
		Category: "HA",
		Causes: []string{
			"实例正在启动",
			"实例正在关闭",
		},
		DiagCmds: []string{
			"/alert       查看 Alert Log",
		},
		Fixes: []string{
			"等待实例启动/关闭完成",
			"检查 Alert Log 确认状态",
		},
	},
	"12162": {
		Code:     "12162",
		Message:  "TNS:net service name is incorrectly specified",
		Meaning:  "TNS 网络服务名配置错误",
		Severity: "MEDIUM",
		Category: "HA",
		Causes: []string{
			"连接字符串格式错误",
			"tnsnames.ora 语法错误",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"检查连接字符串格式",
			"校验 tnsnames.ora 语法",
			"使用 tnsping 测试连接",
		},
	},

	// ── 权限 (4) ─────────────────────────────────────────────

	"01031": {
		Code:     "01031",
		Message:  "insufficient privileges",
		Meaning:  "权限不足",
		Severity: "MEDIUM",
		Category: "权限",
		Causes: []string{
			"缺少必要的系统/对象权限",
			"角色未授予",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"GRANT 相应权限给用户",
			"检查需要的权限: SELECT/INSERT/UPDATE/DELETE/EXECUTE",
		},
	},
	// 00942 already defined in SQL section
	"01017": {
		Code:     "01017",
		Message:  "invalid username/password; logon denied",
		Meaning:  "用户名/密码无效",
		Severity: "MEDIUM",
		Category: "权限",
		Causes: []string{
			"密码错误",
			"用户名拼写错误",
			"密码已过期",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"确认用户名和密码正确",
			"ALTER USER <user> IDENTIFIED BY <password>",
			"检查密码是否过期",
		},
	},
	"28000": {
		Code:     "28000",
		Message:  "the account is locked",
		Meaning:  "账户被锁定",
		Severity: "MEDIUM",
		Category: "权限",
		Causes: []string{
			"连续密码错误次数超限",
			"密码策略自动锁定",
		},
		DiagCmds: []string{},
		Fixes: []string{
			"ALTER USER <user> ACCOUNT UNLOCK",
			"检查 PASSWORD_LOCK_TIME 策略",
			"排查是否有恶意登录尝试",
		},
	},
}
