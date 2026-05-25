# 达梦数据库 (DM) 支持专项设计方案

**日期**：2026-04-30
**状态**：设计阶段（未启动开发）
**版本目标**：v1.2.x（独立大版本）
**预计周期**：6-8 周到 P1 可用，半年到 PG/Oracle 同等成熟度
**资料来源**：Claude Opus 4.7 自有知识 + 达梦官方文档 + 网上技术博客（见末尾 Sources）

---

## 1. 背景与目标

### 1.1 为什么做

- 信创趋势下达梦在政府 / 金融 / 电力 / 电信场景占比快速提升
- 国产数据库三巨头（达梦 / 人大金仓 / 神舟通用），达梦市占率第一
- opendb 已支持 Oracle / MySQL / PostgreSQL / OpenGauss 四库，加 DM 形成"主流国产 + 国际"全覆盖
- 用户用 dbaa 管理达梦，目前没有同类工具（达梦官方 DEM 是商用闭源）

### 1.2 范围

**做：**
- DM 单机模式 / 主备模式诊断 + 管理
- Read-only 诊断（health/sessions/locks/sql/topsql/slowsql/blocktree/explain 等）
- 安全分级框架沿用（Level 0 read-only、Level 1 kill session、Level 2 DDL）
- LLM 诊断（基于 Oracle 兼容模式输出）
- 规则引擎兜底

**先不做（延后）：**
- DM DSC（共享存储集群）专项工具
- DM MPP（无共享并行）专项工具
- DM DPC（分布式 RAFT）专项工具
- 备份恢复管理（DMRMAN 集成）
- DEM Prometheus exporter 集成

### 1.3 验收标准

P1（v1.2.0）：
- `dbaa -c dm /sql 'select 1'` 跑通
- 所有 read-only skill 在真实 DM8 实例上输出正确
- /llm 诊断在 hot row / 锁等待 / 慢 SQL 三个标准故障场景下能给出**包含具体 PID/sql_id 的根因**
- 4 库一致性 review 通过（命名、表格风格跟 OG/Oracle 对齐）

---

## 2. 我（Claude）对 DM 的真实认知度

**严格自评**，不美化：

| 维度 | 把握度 | 备注 |
|---|---|---|
| 整体定位、市场背景 | ⭐⭐⭐⭐⭐ | 信创主力，武汉达梦 |
| Oracle 兼容模式语法（`COMPATIBLE_MODE=2`）| ⭐⭐⭐⭐ | 100+ Oracle 系统视图兼容，PL/SQL 大部分兼容 |
| 高频 V$ 视图（V$SESSIONS / V$LOCK / V$SQL_HISTORY）| ⭐⭐⭐⭐ | 通过本次搜索已确认完整列表 |
| 杀会话语法 `SP_CLOSE_SESSION(SESS_ID)` | ⭐⭐⭐⭐ | 确认 |
| 慢 SQL 配置 `SVR_LOG=1` + `sqllog.ini` | ⭐⭐⭐⭐ | 确认 |
| EXPLAIN 输出格式 `[代价ms,行数,字节数]` | ⭐⭐⭐⭐ | 确认 |
| Go 驱动接入（`gitee.com/chunanyong/dm`）| ⭐⭐⭐⭐ | 标准 database/sql，DSN 格式确认 |
| 等待事件命名（V$EVENT_NAME / V$WAIT_HISTORY）| ⭐⭐ | 知道有，具体事件名需要真机查 |
| DM 错误码体系（V$ERR_INFO 2666 个）| ⭐⭐ | 有全量，但根因映射表得自己建 |
| AWR 报告生成（`DBMS_WORKLOAD_REPOSITORY`-like）| ⭐⭐ | 知道有 AWR-like，具体 API 需要查手册 |
| DSC / MPP / DPC 集群内部 | ⭐ | 几乎没把握，本期不做 |
| dm.ini 参数大全（300+ 参数）| ⭐⭐ | 高频几十个能说出来 |

**幻觉风险高发点**（需要真机校验，不能盲信我）：
- 编造不存在的 V$ 视图名（如 V$DM_XX）
- 把 Oracle 的 hint 语法套到 DM
- 把 PG 的 vacuum / autovacuum 概念套到 DM（**DM 没有这些**）
- 等待事件分类的中文名翻译
- DM 集群相关任何细节

**结论**：我能写 80% 的代码骨架 + 大部分 SQL 适配，但**任何细节都必须配合官方手册 + 真机验证**。

---

## 3. DM 技术全景（搜索 + 自有知识汇总）

### 3.1 驱动 + 连接

**官方推荐 Go 驱动**：[gitee.com/chunanyong/dm](https://gitee.com/chunanyong/dm)

```go
import (
    "database/sql"
    _ "gitee.com/chunanyong/dm"
)

// DSN 格式
db, err := sql.Open("dm", "dm://SYSDBA:SYSDBA001@127.0.0.1:5236?schema=SYSDBA")
```

- 标准 `database/sql` 接口
- 端口默认 **5236**
- 默认用户 `SYSDBA`，初始密码 `SYSDBA` 或 `SYSDBA001`（看版本）
- 支持 schema 切换、SSL、连接池参数
- DSN 也支持 `oracle://`/JDBC 风格 URL

**备选**：[github.com/godoes/gorm-dameng](https://github.com/godoes/gorm-dameng)（GORM 适配，基于官方驱动二开）

### 3.2 兼容性模式（关键）

DM 通过参数 `COMPATIBLE_MODE` 设置兼容性：

| 值 | 兼容模式 |
|---|---|
| 0 | DM 原生（默认）|
| 1 | SQL92 标准 |
| 2 | **部分兼容 Oracle** ← 推荐 |
| 3 | 部分兼容 SQL Server |
| 4 | 部分兼容 MySQL |
| 5 | 兼容 DM6 |
| 6 | 部分兼容 Teradata |

**静态参数，需重启生效**。生产环境普遍用 `COMPATIBLE_MODE=2`，opendb 默认假设此模式。

兼容内容包括：ROWNUM、多列 IN、层次查询、外连接 `(+)`、INSTEAD OF 触发器、`%TYPE`、记录类型、100+ Oracle 系统视图。

### 3.3 动态性能视图全景（基于官方文档）

**8 大类视图**（来自 [eco.dameng.com 官方文档](https://eco.dameng.com/document/dm/zh-cn/pm/dynamic-management.html)）：

> **关于"对应 skill"列**：很多 skill 是**复合 skill**（一个 skill 并行查多个视图聚合）。
> 下面的"对应 skill"列只标该视图的**主要消费者**。Skill → 视图的正向映射见 §3.8。

#### 系统状态类
| 视图 | 用途 | opendb 对应 skill |
|---|---|---|
| V$INSTANCE | 实例信息 | /info |
| V$VERSION | 版本号 | /info |
| V$SYSSTAT | 系统统计 | /health |
| V$RESOURCE_LIMIT | 资源限制 | /resource |
| V$DANGER_EVENT | 危险事件 | /alert |

#### 内存类
| 视图 | 用途 | opendb skill |
|---|---|---|
| V$BUFFERPOOL | 缓冲池 | /buffer |
| V$MEM_POOL | 内存池 | /memory |
| V$DICT_CACHE | 字典缓存 | /cache |
| V$VMS, V$STKFRM | 虚拟内存、栈 | (略)|

#### 会话 / 锁 / 事务类（**最高优先级**）
| 视图 | 用途 | opendb skill |
|---|---|---|
| **V$SESSIONS** | 会话信息（核心） | /sessions, /activesessions |
| V$CONNECT | 连接信息 | /connections |
| V$STMTS | 当前执行 SQL | /activesessions |
| **V$LOCK** | 锁信息（含 BLOCKED 标志）| /locks |
| **V$TRX** | 事务信息 | /tx |
| **V$TRXWAIT** | 事务等待 | /blocktree |
| V$DEADLOCK_HISTORY | 历史死锁 | /alert |
| V$PURGE | 回滚段 | /undo |

#### 等待事件类
| 视图 | 用途 | opendb skill |
|---|---|---|
| V$EVENT_NAME | 等待事件字典 | (元数据) |
| **V$SESSION_WAIT_HISTORY** | 当前会话等待历史 | /waits |
| V$SYSTEM_EVENT | 系统级累计等待 | /waits |
| V$SESSION_EVENT | 会话级累计等待 | /waits |
| **V$WAIT_HISTORY** | 全局等待历史 | /waits |

#### SQL 历史类
| 视图 | 用途 | opendb skill |
|---|---|---|
| **V$SQL_HISTORY** | SQL 执行历史（兼 Oracle V$SQL）| /topsql |
| V$LONG_EXEC_SQLS | 长执行 SQL | /slowsql |
| V$SYSTEM_LONG_EXEC_SQLS | 系统级长 SQL | /slowsql |
| V$SQLTEXT | SQL 文本 | /sqlbyid |
| V$SORT_HISTORY | 排序历史 | /sort |
| V$RUNTIME_ERR_HISTORY | 运行错误 | /alert |

#### 配置 / 参数类
| 视图 | 用途 | opendb skill |
|---|---|---|
| V$DM_INI | dm.ini 参数 | /params |
| V$PARAMETER | 运行时参数 | /params |
| V$DM_ARCH_INI | 归档参数 | /archive |
| V$DM_MAL_INI | MAL（集群通信）参数 | (集群) |

#### 存储类
| 视图 | 用途 | opendb skill |
|---|---|---|
| V$DATABASE | 数据库 | /info |
| V$DATAFILE | 数据文件 | /storage |
| V$TABLESPACE | 表空间 | /storage |
| V$HUGE_TABLESPACE | 大表空间 | /storage |
| V$RLOG, V$RLOGFILE | redo 日志 | /redolog |
| V$CKPT_HISTORY | checkpoint 历史 | /checkpoint |

#### 错误信息类
| 视图 | 用途 | opendb skill |
|---|---|---|
| V$ERR_INFO | 错误码字典（2666 条）| /error |

**查询全部动态视图**：
```sql
SELECT * FROM V$DYNAMIC_TABLES;
```

### 3.4 关键运维操作 SQL

```sql
-- 杀会话（DM 不支持 Oracle 的 ALTER SYSTEM KILL SESSION）
CALL SP_CLOSE_SESSION(SESS_ID);  -- SESS_ID 来自 V$SESSIONS

-- 强制 checkpoint
CALL SP_SET_PARA_VALUE(1, 'CKPT_FLUSH_PAGES', 1000);

-- 开启慢 SQL 日志（动态生效）
CALL SP_SET_PARA_VALUE(1, 'SVR_LOG', 1);
-- 慢 SQL 写入 log/dmsql_<实例名>_<日期>.log
-- 过滤规则在 sqllog.ini

-- 收集表统计信息（Oracle 兼容）
CALL DBMS_STATS.GATHER_TABLE_STATS('SCHEMA', 'TABLE');

-- EXPLAIN 执行计划（输出 [代价ms, 行数, 字节数]）
EXPLAIN SELECT * FROM TABLE WHERE COL = 1;
```

### 3.5 阻塞链查询（无 pg_blocking_pids 等价）

DM 没有 Oracle 的 `DBA_BLOCKERS` / PG 的 `pg_blocking_pids()`，需要 join 三个视图：

```sql
SELECT
    s.SESS_ID AS blocked_sess_id,
    s.USER_NAME AS blocked_user,
    s.SQL_TEXT AS blocked_sql,
    l.TID AS blocking_trx_id,
    bs.SESS_ID AS blocker_sess_id,
    bs.USER_NAME AS blocker_user,
    bs.SQL_TEXT AS blocker_sql,
    o.NAME AS object_name
FROM V$LOCK l
JOIN V$SESSIONS s ON s.TRX_ID = l.TID
JOIN V$LOCK bl ON bl.TID != l.TID AND bl.RES_ID = l.RES_ID
JOIN V$SESSIONS bs ON bs.TRX_ID = bl.TID
LEFT JOIN SYSOBJECTS o ON o.ID = l.TABLE_ID
WHERE l.BLOCKED = 1;
```

**注意**：本会话刚修过 OG 同类 SQL 的 bug（缺 `granted` 过滤导致互联完全图，引发 OOM）。**DM 实现时必须避同样错误**：要确保 blocker 那一侧是真正持有锁的（非 ungranted waiter）。

### 3.6 慢 SQL 与 AWR

**慢 SQL** 两条路径：
1. 实时：`V$LONG_EXEC_SQLS` 查正在执行的长 SQL
2. 历史：`SVR_LOG=1` 写文件 → 用 [DM 日志分析工具] 解析

**AWR** （达梦 v8.x 内置）：
```sql
-- 启用快照
CALL SP_INIT_AWR_SYS(1);
-- 设置间隔（默认 60 分钟）
CALL DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(60, 7);
-- 手动触发快照
CALL DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT();
-- 生成报告
CALL SP_AWR_REPORT_LAST_DAY();
```

opendb /awr skill 可包装这套调用，输出 HTML 报告。

### 3.7 Skill → 视图正向索引（实现时按这个清单 grep）

§3.3 是"视图 → skill"反向索引，但很多 skill 是**复合的**——并行查多个视图聚合。
开发时应按以下正向索引知道每个 skill 要 grep 的视图：

| skill | 涉及视图（并行查询）| 输出 |
|---|---|---|
| **/info** | V$INSTANCE + V$VERSION + V$DATABASE + V$DM_INI(关键参数) | 4 视图，单表格 |
| **/health** | V$INSTANCE + V$DATABASE + V$RESOURCE_LIMIT + V$BUFFERPOOL + V$MEM_POOL + V$LOCK + V$DEADLOCK_HISTORY + V$SYSSTAT + V$LONG_EXEC_SQLS + V$DANGER_EVENT + V$RUNTIME_ERR_HISTORY + V$CKPT_HISTORY | **12 视图**，dashboard 总览 + 告警标红 |
| **/alert** | V$DEADLOCK_HISTORY + V$DANGER_EVENT + V$RUNTIME_ERR_HISTORY + V$RESOURCE_LIMIT(超限项) | 4-5 视图，告警事件清单 |
| **/sessions** | V$SESSIONS + V$CONNECT (join) | 2 视图，全部会话列表 |
| **/activesessions** | V$SESSIONS + V$STMTS + V$LOCK(BLOCKED) (join) | 3+ 视图，仅活跃会话 |
| **/locks** | V$LOCK + SYSOBJECTS(对象名) | 2 视图，所有锁清单 |
| **/blocktree** | V$LOCK self-join + V$SESSIONS + V$TRX(事务信息) | 3+ 视图，阻塞树 |
| **/waits** | V$SESSION_WAIT_HISTORY + V$SYSTEM_EVENT + V$WAIT_HISTORY | 3 视图，等待事件 TOP N |
| **/deadlock** | V$DEADLOCK_HISTORY + V$SQLTEXT(关联 SQL) | 2 视图，死锁详情 |
| **/topsql** | V$SQL_HISTORY (排序 by exec count / total time) | 1 视图，多排序模式 |
| **/slowsql** | V$LONG_EXEC_SQLS + V$SYSTEM_LONG_EXEC_SQLS | 2 视图，长 SQL 清单 |
| **/sqlbyid** | V$SQLTEXT + V$SQL_HISTORY (按 unique_sql_id 拼) | 2 视图，单 SQL 详情 |
| **/explain** | EXPLAIN ... 语句直接执行 | 0 视图，是 SQL 命令 |
| **/tableinfo** | SYSOBJECTS + SYSCOLUMNS + DBA_TABLES + DBA_INDEXES + DBA_SEGMENTS | 5 元数据视图 |
| **/tx** | V$TRX + V$TRXWAIT + V$SESSIONS(关联) | 3 视图，事务清单 |
| **/buffer** | V$BUFFERPOOL + V$BUFFER_LRU_FIRST + V$BUFFER_UPD_LAST | 3 视图，缓冲池详情 |
| **/memory** | V$MEM_POOL + V$VMS + V$STKFRM | 3 视图，内存使用 |
| **/checkpoint** | V$CKPT_HISTORY + V$DM_INI(CKPT_*) | 2 视图，检查点历史 |
| **/archive** | V$DM_ARCH_INI + V$RLOG + V$RLOGFILE | 3 视图，归档配置 |
| **/tablespace** | V$TABLESPACE + V$DATAFILE + V$HUGE_TABLESPACE | 3 视图，表空间 |
| **/params** | V$DM_INI + V$PARAMETER (合并) | 2 视图，参数清单 |
| **/error** | V$ERR_INFO + V$RUNTIME_ERR_HISTORY | 2 视图，错误码字典 + 实时错误 |
| **/awr** | DBMS_WORKLOAD_REPOSITORY + V$SNAPSHOT + AWR 相关视图 | N 个，AWR 报告生成 |
| **/kill** | V$SESSIONS(查 PID) + SP_CLOSE_SESSION(执行) | 1 视图 + 1 procedure |

**实现注意事项：**
- 复合 skill 应该用 `errgroup` 并行查所有视图，10s 内完成
- 每个视图查询独立失败兜底（某视图查不到不应让整个 skill 失败）
- 输出末尾加 `[summary]` banner（按 `docs/design-local-model-optimization.md` 原则，给小模型友好）
- 所有 skill 输出格式跟现有 4 库（OG/Oracle/PG/MySQL）保持一致

### 3.8 集群拓扑（本期不深入）

| 集群类型 | 说明 | 本期处理 |
|---|---|---|
| 单节点 | 默认 | ✅ 全力支持 |
| 主备（DataWatch / DSC）| 一主一备/多备 | ✅ 探测主备角色 |
| DSC（共享存储集群）| 多实例单库共享存储 | ⏳ P2 |
| MPP（无共享并行）| 最多 1024 节点 | ⏳ P3 |
| DPC（分布式 RAFT）| 金融级高可用 | ⏳ P3 |

---

## 4. opendb DM 模块架构设计

### 4.1 目录结构（沿用 oracle/ 模式）

```
internal/dm/
├── register.go                # 注册 DM 产品 + skill
├── driver.go                  # DM 驱动适配（gitee.com/chunanyong/dm）
├── conn.go                    # 连接池 + Compatibility 检测（COMPATIBLE_MODE）
├── version.go                 # DM 版本探测（V$VERSION）
│
├── skill/
│   ├── monitor/
│   │   ├── sessions.go        # /sessions
│   │   ├── activesessions.go  # /activesessions
│   │   ├── locks.go           # /locks
│   │   ├── blocktree.go       # /blocktree (注意避坑：granted 过滤)
│   │   ├── waits.go           # /waits
│   │   ├── deadlock.go        # /deadlock (V$DEADLOCK_HISTORY)
│   │   ├── health.go          # /health
│   │   ├── alert.go           # /alert
│   │   ├── memory.go          # /memory (V$MEM_POOL)
│   │   ├── buffer.go          # /buffer (V$BUFFERPOOL)
│   │   ├── checkpoint.go      # /checkpoint (V$CKPT_HISTORY)
│   │   ├── archive.go         # /archive (V$DM_ARCH_INI)
│   │   └── tablespace.go      # /tablespace
│   │
│   ├── query/
│   │   ├── sql.go             # /sql
│   │   ├── topsql.go          # /topsql (V$SQL_HISTORY)
│   │   ├── slowsql.go         # /slowsql (V$LONG_EXEC_SQLS)
│   │   ├── sqlbyid.go         # /sqlbyid (V$SQLTEXT)
│   │   ├── explain.go         # /explain
│   │   └── planhistory.go     # /planhistory (V$SQL_PLAN)
│   │
│   ├── schema/
│   │   ├── tableinfo.go       # /tableinfo (兼容 SYSOBJECTS / SYSCOLUMNS)
│   │   ├── indexhealth.go     # /indexhealth
│   │   ├── indexadvise.go     # /indexadvise
│   │   └── tablespace.go      # /space
│   │
│   ├── admin/
│   │   ├── kill.go            # /kill (SP_CLOSE_SESSION)
│   │   ├── params.go          # /params (V$DM_INI)
│   │   ├── error.go           # /error (V$ERR_INFO)
│   │   ├── awr.go             # /awr (SP_AWR_REPORT_LAST_DAY)
│   │   └── jobs.go            # /jobs (DBMS_JOB / DBMS_SCHEDULER)
│   │
│   └── ai/
│       ├── diag_skill.go      # /llm 诊断入口
│       ├── prompt.go          # DM 专属诊断 prompt 段（注入到通用 system prompt）
│       └── rule_skill.go      # 规则引擎兜底
│
├── ruleengine/
│   ├── engine.go              # DM 规则引擎主体
│   └── rules/                 # 由 ailinkdb/data/dm/ 生成
│       ├── lock_contention.go
│       ├── slow_sql.go
│       ├── memory_pressure.go
│       └── ...
│
└── sentinel/
    └── probes.go              # DM 探针（沿用 sentinel 框架）
```

### 4.2 Build Tags

```go
// Build tag: dm
//go:build dm || full

package dm
```

新增编译命令：

```bash
# 只编译 DM
go build -tags dm -o opendb ./cmd/opendb/

# 全量（含 DM）
go build -tags 'full dm' -o opendb ./cmd/opendb/

# dbaa 品牌 + 全量
go build -tags 'full dm dbaa' -o dbaa ./cmd/opendb/
```

### 4.3 register.go 框架

```go
//go:build dm || full

package dm

import (
    "database/sql"
    _ "gitee.com/chunanyong/dm"

    "github.com/sqlrush/opendb/internal/connection"
    "github.com/sqlrush/opendb/internal/skill"
    "github.com/sqlrush/opendb/internal/dm/skill/monitor"
    // ... etc
)

const ProductName = "dm"
const DefaultPort = 5236

func init() {
    connection.RegisterProduct(connection.Product{
        Name:           ProductName,
        DriverName:     "dm",
        DefaultPort:    DefaultPort,
        DSNTemplate:    "dm://{user}:{pass}@{host}:{port}?schema={schema}",
        DriverFactory:  newDriver,
    })
}

func RegisterAISkills(reg *skill.Registry, conn *sql.DB, modelMgr *model.Manager, ...) {
    driver := newDMDriver(conn)
    reg.Register(monitor.NewSessionsSkill(driver))
    reg.Register(monitor.NewActiveSessionsSkill(driver))
    reg.Register(monitor.NewLocksSkill(driver))
    reg.Register(monitor.NewBlockTreeSkill(driver))
    // ... etc
}
```

### 4.4 LLM Prompt 适配策略

**关键概念：区分两个不同的轴**

| 类型 | 例子 | 处理 |
|---|---|---|
| **Per-model 差异** | DeepSeek-V4 vs Qwen-27B（能力 / 思考模式）| ❌ 不写专属 prompt（参见 `docs/design-local-model-optimization.md`，维护爆炸论证） |
| **Per-database 差异** | DM vs Oracle vs PG（语法 / 视图名 / 概念缺失）| ✅ **必须**写差异注入段，否则 LLM 会用其他 DB 知识硬套 |

前者是模型层差异，模型自己能学会；后者是知识层差异，**不告诉模型一定幻觉**。

#### 4.4.1 实际 4 层 prompt 拼接架构（v1.1.23 已存在）

opendb 已经实现了完整的 per-DB knowledge layer，在 `internal/engine/context/builder.go::buildSystemPrompt()`：

```
[Block 1] universalSystemPrompt(capability)
              ├─ strict 变体 (large 模型): 6.3 KB
              └─ templated 变体 (small/medium 模型): 4.7 KB
              内容: 角色 / 推理流程 / 完成标准 / 占位符约束 / 工具选择决策
       +
[Block 2] profile.SystemPromptRules()    ← ★ per-DB 知识库（已实现，不是新东西）
              ├─ Oracle    7.6 KB  (V$/ASH/AWR/Hint)
              ├─ OG       10 KB    (gs_/dbe_perf/MOT/CM/WLM/LWLock)
              ├─ PG        4.9 KB  (MVCC/pg_stat_*/VACUUM)
              ├─ MySQL     3.1 KB  (InnoDB/MDL/info_schema)
              └─ DM        ~5 KB   (本设计新增)
       +
[Block 3] ContextAwarenessPrompt + Memory + Policy
              内容: session 上下文/记忆/规范感知
       +
[Block 4] 用户/实例级 policy (动态加载)
              内容: 运行时规则注入
```

**DM 要做的**：写 `internal/engine/profile/dm.go` 实现 `PromptProfile` 接口（详见 §4.4.5）。**沿用现有架构，不另起炉灶**。

#### 4.4.2 DM 知识库内容（dm.go::SystemPromptRules）

文件位置：`internal/engine/profile/dm.go`（沿用现有 4 库 profile 文件的位置和命名）

**风格**：参考现有 4 库（oracle.go / postgres.go / mysql.go / opengauss.go）的写法，**纯 positive 描述 DM 是什么样**，不写"❌ 不要用 X"反例（v0.5 版本曾错误地引入大量 PG/Oracle 反例，违反"positive instruction outperforms negative"的 prompt engineering 原则，v0.6 校正）。

体量对齐目标：~200 行（oracle.go 160 行 / opengauss.go 190 行 / pg.go 114 行 / mysql.go 88 行）。

```go
// internal/engine/profile/dm.go
package profile

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

## 内存结构

| 组件 | 参数 | 默认 | 用途 |
|---|---|---|---|
| 数据缓冲区 NORMAL | BUFFER | 1000M | 数据页主缓存 |
| 数据缓冲区 KEEP | KEEP | — | 常驻热数据 |
| 数据缓冲区 RECYCLE | RECYCLE | — | 一次性页 LRU |
| 数据缓冲区 FAST | FAST_POOL_PAGES | 3000 | 高速页池 |
| 共享内存池 | MEMORY_POOL | 500M | 小内存申请释放 |
| 共享池目标 | MEMORY_TARGET | 1G | 收缩回归目标 |
| 字典缓冲 | DICT_BUF_SIZE | 50M | schema/表/列字典 |
| SQL 缓冲 | CACHE_POOL_SIZE | 0 | 计划缓存 |
| 日志缓冲 | RLOG_BUF_SIZE | — | redo 日志缓冲 |
| 排序区 | SORT_BUF_SIZE | 20M | 排序操作 |
| Hash 区 | HJ_BUF_SIZE | — | hash join |
| Hash 聚合 | HAGR_HASH_SIZE | 100000 | hash 聚合 |

## 表空间结构
- SYSTEM — 系统表空间，存放数据字典
- ROLL — 回滚段（类似 Oracle UNDO）
- MAIN — 用户默认表空间
- TEMP — 临时表空间，排序/中间结果溢出
- HMAIN — 大表空间
- TEMP_SIZE / TEMP_PATH / TEMP_SPACE_LIMIT 控制 TEMP

## 日志体系
- redo 日志存储数据修改新值，每次修改产生新 LSN
- 归档模式：LOCAL（本地）/ REALTIME（实时）/ ASYNC（异步）/ TIMELY（即时）/ REMOTE（远程）
- 一台主库最多 8 个本地归档目标
- 归档配置参数：ARCH_INI（开归档）/ ARCH_DEST（路径）/ ARCH_FILE_SIZE（单文件大小）/ ARCH_SPACE_LIMIT（空间限额）

## 索引类型
- B+树索引（默认）— CREATE INDEX
- 唯一索引 — CREATE UNIQUE INDEX
- 函数索引 — CREATE INDEX ON tab(func(col))
- 位图索引 — CREATE BITMAP INDEX（针对低基数列）
- 位图连接索引 — DM 特有，跨表位图连接
- 全文索引 — 文本列上建索引

## 关键运维操作
- 杀会话：CALL SP_CLOSE_SESSION(<sess_id>)，sess_id 来自 V$SESSIONS
- 改参数：CALL SP_SET_PARA_VALUE(<scope>, '<NAME>', <value>)
  - scope: 1 = 当前+静态, 2 = 仅动态, 3 = 仅当前
- 强制 checkpoint：CALL SP_CKPT_OPER('FULL')
- 收集表统计：CALL DBMS_STATS.GATHER_TABLE_STATS('<schema>', '<table>')
- 表空间扩容：ALTER TABLESPACE <name> ADD DATAFILE '<path>' SIZE <N>
- 切换归档：关库 → mount → ALTER DATABASE ARCHIVELOG → open
- EXPLAIN <sql>：返回三元组 [代价ms, 记录行数, 字节数]

## 关键视图

### 会话/锁/事务
| 视图 | 关键字段 | 用途 |
|---|---|---|
| V$SESSIONS（注意复数）| SESS_ID, USER_NAME, SQL_TEXT, STATE, CREATE_TIME, CLNT_HOST | 会话列表 |
| V$STMTS | SESS_ID, SQL_TEXT, STATE | 当前执行语句 |
| V$CONNECT | CLNT_IP, USER_NAME | 连接信息 |
| V$LOCK | TID, RES_ID, BLOCKED, MODE | 锁信息（BLOCKED=1 阻塞中）|
| V$TRX | TRX_ID, STATUS, BEGIN_TIME | 事务信息 |
| V$TRXWAIT | WAITING_TID, BLOCKER_TID | 事务等待 |
| V$DEADLOCK_HISTORY | TIME, TID1, TID2, RES_ID | 历史死锁 |
| V$PURGE | — | 回滚段 |

### 等待事件
- V$EVENT_NAME — 等待事件字典
- V$WAIT_HISTORY — 全局等待历史
- V$SESSION_WAIT_HISTORY — 当前会话等待
- V$SYSTEM_EVENT — 系统级累计等待
- V$SESSION_EVENT — 会话级累计等待

### SQL 历史与性能
- V$SQL_HISTORY — SQL 执行历史（DM 主要 SQL 视图）
- V$LONG_EXEC_SQLS — 当前正在执行的长 SQL（实时慢查询入口）
- V$SYSTEM_LONG_EXEC_SQLS — 系统级长 SQL 累计
- V$SQLTEXT — SQL 全文
- V$SORT_HISTORY — 排序历史
- V$RUNTIME_ERR_HISTORY — 运行错误

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
- V$ERR_INFO — 错误码字典（2666 条预定义）

## 关键系统包（DBMS_*，Oracle 兼容大部分语法）
- DBMS_STATS — 统计信息收集
- DBMS_JOB — 定时任务
- DBMS_OUTPUT — 调试输出
- DBMS_LOB — LOB 操作
- DBMS_LOCK — 用户锁
- DBMS_LOGMNR — 日志挖掘
- DBMS_METADATA — 元数据导出
- DBMS_RANDOM — 随机数
- DBMS_RLS — 行级安全
- DBMS_SESSION — 会话管理
- DBMS_SPACE — 空间管理
- DBMS_SQL — 动态 SQL
- DBMS_TRANSACTION — 事务控制
- DBMS_UTILITY — 工具
- DBMS_WORKLOAD_REPOSITORY — AWR
- DBMS_XMLGEN — XML 生成
- DBMS_PIPE — 管道通信
- DBMS_ALERT — 告警

## 等待事件速查（Phase 0 真机校验后补充完整表格）

DM 等待事件分类（参考 V$WAIT_HISTORY 实际数据）：
- I/O 类：IO READING（数据页读）、IO WRITING（数据页写）
- 日志类：LOG WAITING（日志同步）、ARCH WAITING（归档等待）
- 锁类：LOCK WAIT（行/表锁）、LATCH WAIT（内部 latch 争用）
- 网络类：CLIENT WAITING（等客户端）

> 完整等待事件表 Phase 0 在真实 DM 上 SELECT NAME FROM V$EVENT_NAME 拿全量后填表。

## 性能调优关键参数

| 参数 | 类型 | 默认 | 推荐 |
|---|---|---|---|
| BUFFER | 静态 | 1000M | 物理内存 60-80% |
| MEMORY_POOL | 静态 | 500M | 物理内存 6% |
| MEMORY_TARGET | 动态 | — | MEMORY_POOL × 2 |
| WORKER_THREADS | 静态 | 16 | CPU 物理核心数 |
| TASK_THREADS | 静态 | 4 | 64 核以下 4，以上 16 |
| IO_THR_GROUPS | 静态 | 2 | TASK_THREADS / 2 |
| SORT_BUF_SIZE | 动态 | 20M | 排序密集场景调大 |
| HJ_BUF_SIZE | 动态 | — | hash join 密集场景调大 |
| DICT_BUF_SIZE | 静态 | 50M | schema 多时调大 |
| SVR_LOG | 动态 | 0 | 1 = 开慢 SQL 日志 |
| ARCH_INI | 静态 | 0 | 1 = 开归档（生产必开）|

参数属性：静态需重启，动态立即生效。

## 慢 SQL 路径
- **实时**：V$LONG_EXEC_SQLS（当前正在执行的长 SQL）
- **历史**：启用 SVR_LOG=1 → log/dmsql_<实例>_<日期>.log
- **过滤配置**：sqllog.ini（与 dm.ini 同级目录），可设阈值过滤

## AWR (Workload Repository)
- 启用快照：CALL SP_INIT_AWR_SYS(1)
- 配置间隔：CALL DBMS_WORKLOAD_REPOSITORY.MODIFY_SNAPSHOT_SETTINGS(60, 7) — 60min/保留7天
- 手动触发：CALL DBMS_WORKLOAD_REPOSITORY.CREATE_SNAPSHOT()
- 生成报告：CALL SP_AWR_REPORT_LAST_DAY()

## 备份恢复（DMRMAN，本期不做主线但需理解）
- 工具：/opt/dmdbms/bin/dmrman
- 全备：BACKUP DATABASE '<path>' TO '<backup_path>'
- 增备：BACKUP DATABASE '<path>' INCREMENT WITH BACKUPDIR '<last>' TO '<inc_path>'
- 还原：RESTORE DATABASE '<path>' FROM BACKUPSET '<backup_path>'
- 应用：RECOVER DATABASE '<path>' FROM BACKUPSET '<backup_path>'

## 修复建议安全规则（必须遵守）
- 大表 CREATE INDEX → 注意业务高峰锁影响，考虑 ONLINE 选项或在低峰期
- 死元组无需手动维护 → 内核自动 purge，不需要 VACUUM/ANALYZE
- 修改 dm.ini 静态参数 → 改完必须重启实例
- ALTER TABLE 改列类型 → DM 早期版本可能锁全表，先评估表大小
- 杀会话顺序 → 先 SP_CLOSE_SESSION，再处理留在事务中的资源
- 动态参数改完 → CALL SP_SET_PARA_VALUE 立即生效，但可能不持久（重启丢失）
- 给生产环境建议 SQL 前 → 必须用 sql skill 验证表/索引/视图存在
`
}
```

**~210 行**，跟现有 4 库体量对齐。Phase 0 完成后这份内容会根据真机验证结果细调（特别是等待事件速查表、参数默认值）。

#### 4.4.3 三个 prompt / 上下文注入入口

| 入口 | 内容 | 修改位置 |
|---|---|---|
| **Block 2 系统 prompt** | DMProfile.SystemPromptRules() | `internal/engine/profile/dm.go` |
| **环境上下文消息** | 数据库类型 / 版本 / 实例 / 时间 | 已由 `BuildInput.Product/Version` 自动注入（无需新增） |
| **工具结果 [summary] banner** | 当下数据 + 关键提示 | 每个 DM skill 的 `formatXxxPanel()` 末尾 |

#### 4.4.4 工具结果 banner 的 DM 适配

举例：DM /health 的 banner 末尾应输出：

```
[health summary]
db_type: dm 8.1.2.95
status: OPEN, role: PRIMARY
buffer_hit_rate: 98.2%
session_count: 42 / 1000 limit
deadlock_total: 134 (cumulative)
note: DM 死元组由内核自动 purge，无 vacuum 概念
hottest_session_id: 12345 (waiting on row lock 83s)
```

每条工具的 `[summary]` 都加 `note:` 行可以在工具层动态补强模型对 DM 的认知，**比改 prompt 更轻**（参见 `docs/design-local-model-optimization.md` Tier 1 原则）。

#### 4.4.5 沿用现有 PromptProfile 架构（不是新建框架）

**校正声明**：v0.4 版本本节曾错误声称 "4 库都没有 Layer 2"。**实际扫描代码后发现**：opendb 已经实现了完整的 per-DB Layer 2 架构，叫 **PromptProfile**，4 库各有独立知识库文件。DM 应**沿用**这个架构，不是建新框架。

##### 实际架构（v1.1.23 已存在）

```
internal/engine/context/builder.go::buildSystemPrompt() 实际拼 4 个 Block：

Block 1: universalSystemPrompt(capability)           ← 通用 6.3KB(strict) / 4.7KB(templated)
Block 2: profile.SystemPromptRules()                 ← per-DB 硬事实段 ★ Layer 2
Block 3: ContextAwarenessPrompt + Memory + Policy    ← 上下文/记忆/规范感知
Block 4: 用户/实例级 policy（动态）                  ← 运行时规则
```

##### 现有 4 库 Layer 2 实测体量

| 库 | 文件 | 行数 / 字节 | 内容主体 |
|---|---|---|---|
| Oracle | `internal/engine/profile/oracle.go` | 160 行 / 7.6 KB | 等待事件速查表 / 对象引用规则 / ASH/AWR 知识 / Hint 语法 |
| OpenGauss | `internal/engine/profile/opengauss.go` | 190 行 / 10 KB | 内核基础+差异 / gs_* / dbe_perf / MOT/CM/WLM / LWLock 族 |
| PostgreSQL | `internal/engine/profile/postgres.go` | 114 行 / 4.9 KB | MVCC / pg_stat_* / VACUUM / 修复建议安全规则 |
| MySQL | `internal/engine/profile/mysql.go` | 88 行 / 3.1 KB | InnoDB / MDL 锁 / `information_schema` vs `performance_schema` |

##### PromptProfile 接口（4 库已实现 + DM 要实现）

```go
// internal/engine/profile/profile.go
type PromptProfile interface {
    Product() string                                           // "oracle"/"mysql"/"postgres"/"opengauss"/"dm"
    SystemPromptRules() string                                 // ★ Layer 2 知识库
    ToolUsageHint(skillName string) string                     // 工具用途描述
    ToolFilter(mode string) func(name string, level int) bool  // 模式过滤
    DefaultMaxTurns(mode string) int                           // 默认最大轮数
}

// NewProfile 工厂
func NewProfile(product string) PromptProfile {
    switch product {
    case "oracle":    return &OracleProfile{}
    case "mysql":     return &MySQLProfile{}
    case "postgres":  return &PostgresProfile{}
    case "opengauss": return &OpenGaussProfile{}
    case "dm":        return &DMProfile{}    // ← 本设计新增
    default:          return &GenericProfile{product: product}
    }
}
```

##### DM 要做的事（最小工作量）

仅需新建一个文件 `internal/engine/profile/dm.go`，实现 4 个接口方法：

| 方法 | 工作量 | 内容来源 |
|---|---|---|
| `Product() string` | 1 行 | `return "dm"` |
| `SystemPromptRules() string` | ~150 行 | 见 §4.4.2 的 17 条硬事实 + Phase 0 真机校验后补充等待事件表 |
| `ToolUsageHint(name) string` | ~20 行 | 给 25 个 DM skill 写一句话用途（参考 oracle.go 的写法） |
| `ToolFilter(mode) func(...)` | ~10 行 | 通常按 mode (`auto`/`assist`/`playbook`) 过滤工具 |
| `DefaultMaxTurns(mode) int` | ~10 行 | `auto: 20, assist: 1, playbook: 1` |

**总工作量**：0.5 天（写完 + 接 NewProfile 工厂 + 单元测试）。

##### 不需要做的（澄清）

- ❌ 不需要 "4 库一致性升级"（4 库已经有 Layer 2，DM 是补齐者，不是开创者）
- ❌ 不需要在 engine/context/builder.go 加新的 `BuildWithFacts` helper（现有 buildSystemPrompt 已经处理）
- ❌ 不需要 5 模型 A/B 对比（4 库已经在用 Layer 2，DM 加上后保持一致即可，回归测试沿用 Phase 2 的 5 模型 benchmark）

##### 可选优化（远期）

如果 Phase 2 benchmark 发现现有 4 库的 SystemPromptRules 内容也存在幻觉（比如 Oracle 的等待事件表过时、PG 缺某些 pg_stat 视图），可以借此机会：
- 顺手刷新 4 库 Layer 2 内容
- 用 5 模型 A/B 对比刷新前后的诊断质量
- 但**这是优化不是必需**，DM 启动不依赖这个

### 4.5 知识注入 / RAG（远期）

DM 官方手册 PDF chunked 后入向量库，诊断时根据 query 检索相关章节。
**本期不做**，先验证主框架。

---

## 5. 分阶段实施计划

### Phase 0：调研 + 环境（1 周）

**测试环境**（已锁定）：
- 主机：47.251.30.180（既有 OG 测试机，复用）
- DM 端口：**5237**（避开 OG 的 15432）
- 安装方式：DM8 个人开发版（免费，1 年试用，无功能限制）
- 部署形态：原生安装（非 Docker，更接近生产环境，AWR / dmrman 等工具完整）

#### Phase 0 任务清单

- [ ] **下载 DM8**：从 [eco.dameng.com/download](https://eco.dameng.com/download/) 拿 `dm8_*_linux_64.iso` 或 `.tar.gz`（选 RHEL/CentOS 7 / X86_64 版本）
- [ ] **测试机预检**：
    - SSH 到 `root@47.251.30.180`，确认 5237 端口未占用
    - 确认 `/opt` 有 ≥ 5 GB 空闲（DM 安装包约 1 GB，运行需 2-3 GB）
    - 确认 OG 故障流量未占满 CPU/内存（必要时降故障并发）
- [ ] **创建专用账户**：
    ```bash
    groupadd dinstall
    useradd -g dinstall dmdba
    echo 'dmdba:DmDba@2026' | chpasswd
    mkdir -p /opt/dmdbms && chown dmdba:dinstall /opt/dmdbms
    ```
- [ ] **安装 DM**（命令行静默安装）：
    ```bash
    su - dmdba
    cd /tmp/dm8
    ./DMInstall.bin -i \
        --INSTALL_TYPE=Server \
        --INSTALL_PATH=/opt/dmdbms \
        --LOG_PATH=/opt/dmdbms/log
    ```
- [ ] **初始化实例**（端口 5237 + Oracle 兼容模式）：
    ```bash
    /opt/dmdbms/bin/dminit \
        path=/opt/dmdbms/data \
        db_name=DAMENG \
        instance_name=DM01 \
        port_num=5237 \
        page_size=16 \
        case_sensitive=Y \
        charset=1 \
        sysdba_pwd=SYSDBA001
    # 注意: COMPATIBLE_MODE=2 在 dm.ini 里配，需要手动改后重启
    ```
- [ ] **配置 Oracle 兼容**：
    ```bash
    # 编辑 /opt/dmdbms/data/DAMENG/dm.ini
    # 找到 COMPATIBLE_MODE 改为 2
    # 找到 SVR_LOG 改为 1（开慢 SQL 日志）
    ```
- [ ] **注册系统服务并启动**：
    ```bash
    /opt/dmdbms/script/root/dm_service_installer.sh -t dmserver \
        -dm_ini /opt/dmdbms/data/DAMENG/dm.ini -p DM01
    systemctl start DmServiceDM01
    systemctl enable DmServiceDM01
    ```
- [ ] **验证连接**：
    ```bash
    /opt/dmdbms/bin/disql SYSDBA/SYSDBA001@LOCALHOST:5237
    SQL> SELECT * FROM V$VERSION;
    SQL> SELECT * FROM V$INSTANCE;
    SQL> SELECT NAME, VALUE FROM V$PARAMETER WHERE NAME='COMPATIBLE_MODE';
    -- 期望 VALUE=2
    ```
- [ ] **创建测试用户**（不直接用 SYSDBA）：
    ```sql
    CREATE USER opendb IDENTIFIED BY "OpenDb@2026"
        DEFAULT TABLESPACE MAIN;
    GRANT DBA TO opendb;  -- 测试用 DBA，生产应细分权限
    ```
- [ ] **本地客户端测试**：本地 macOS 上用 `gitee.com/chunanyong/dm` Go 驱动写 hello world 连 47.251.30.180:5237，跑 `SELECT 1`
- [ ] **通读 DM 手册关键章节**：
    - 《DM 程序员手册》第 1 章 SQL 语法、附录 1 错误码、附录 4 系统视图
    - 《DM 系统管理员手册》第 12 章性能诊断
    - 这两份手册在 `/opt/dmdbms/doc/` 下（PDF）
- [ ] **生成 DM vs Oracle 视图差异清单**：在 DM 上 `SELECT NAME FROM V$DYNAMIC_TABLES;` 拿到全量 → 跟 Oracle 主流 V$ 视图对比 → 输出 `docs/dm-vs-oracle-views.md`
- [ ] **故障注入脚本框架**：参考 `scripts/og_load_app_antipatterns.sh` 写 `scripts/dm_load_basic.sh`，先做最简单的 hot row UPDATE 注入，验证 V$LOCK / V$TRXWAIT 能看到锁等待

**Phase 0 输出**：
- `docs/dm-vs-oracle-views.md` — 视图差异对照表（人手核对，不靠 LLM 编）
- `scripts/dm_load_basic.sh` — 基础故障注入脚本
- 可远程访问的 DM 实例：`opendb/OpenDb@2026 @ 47.251.30.180:5237`
- 验证 Go 驱动可用的 hello world 代码（本地 `/tmp/dm-hello/main.go`）

**完成标准**：本地 macOS 跑

```go
db, _ := sql.Open("dm", "dm://opendb:OpenDb@2026@47.251.30.180:5237")
rows, _ := db.Query("SELECT * FROM V$INSTANCE")
```

能拿到完整结果，**才能进 Phase 1**。

### Phase 1：骨架 + Read-Only Skills（2 周）

只做 Level 0 read-only：

- [ ] `internal/dm/` 目录结构 + register.go
- [ ] DM Driver 适配（实现 `db.Driver` 接口）
- [ ] connection.go 支持 dm:// DSN
- [ ] `dbaa configure` 增加 DM 选项
- [ ] 12 个核心 skill：
    - `/info` `/sessions` `/activesessions` `/locks` `/blocktree`
    - `/waits` `/deadlock` `/sql` `/topsql` `/slowsql`
    - `/explain` `/tableinfo` `/health` `/alert`
- [ ] **每个 skill 在真机跑过，输出对账无 panic / 字段不存在错误**
- [ ] uitest 加 DM 测试 case

**验收**：
- `dbaa -c dm /sql 'select 1'` ✓
- `dbaa -c dm /llm '当前数据库存在什么问题'` 即使没规则引擎也能通过纯 LLM 跑出报告（可能不准但不崩）

### Phase 2：故障场景 + 规则引擎（2 周）

- [ ] 构造 4 个标准故障场景（沿用 OG 反模式）：
    - F1 hot row UPDATE（验证 blocktree 不会 OOM）
    - F2 慢 SQL（缺索引全表扫）
    - F3 长事务 + 锁等待
    - F4 死锁（多表交叉 UPDATE）
- [ ] `ailinkdb/data/dm/` 写规则数据（JSON）
- [ ] `internal/dm/ruleengine/` 由数据生成 Go 代码
- [ ] 5 模型回归 benchmark（DeepSeek/GLM/Kimi/Moonshot/Qwen）
- [ ] 4 库一致性 review（命名、表格风格）

**验收**：
- 4 个故障场景下 LLM 诊断都能给出根因 + 具体 SP_CLOSE_SESSION 调用
- 规则引擎在 LLM 不可用时给出可读的兜底诊断

### Phase 3：管理类 skill + AWR（1 周）

- [ ] /kill (SP_CLOSE_SESSION，安全分级 Level 1)
- [ ] /params (V$DM_INI 查 / SP_SET_PARA_VALUE 改，Level 2)
- [ ] /error (V$ERR_INFO 错误码字典)
- [ ] /awr (启用快照、生成报告)
- [ ] /tablespace、/datafile、/checkpoint
- [ ] 安全分级测试（Level 1 二次确认 / Level 2 强制确认）

### Phase 4：Sentinel 探针 + 文档（1 周）

- [ ] 接入 Sentinel 框架（对应 4 个 OG 探针的 DM 版本）
- [ ] 写 `docs/dm-quickstart.md`、`docs/dm-skills-reference.md`
- [ ] 录制截图 / GIF 加到 README
- [ ] CHANGELOG.md 写 v1.2.0 完整说明

### Phase 5（远期）：知识注入 / 集群

- [ ] DM 手册 RAG（v1.3+）
- [ ] DSC / MPP / DPC 集群专项（v1.4+）
- [ ] DEM Prometheus exporter 集成（v1.4+）

---

## 6. 风险评估与缓解

### 6.1 高风险

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| LLM 对 DM 知识幻觉 | 高 | 用户被错诊 | (1) 规则引擎兜底 (2) 工具结果加 [summary] banner (3) 后期上 RAG |
| V$ 视图字段我记错 | 中 | SQL 跑不通 | 全部 SQL 真机校验，写 SQL 必须查官方文档 |
| 复用 OG blocktree SQL 缺 granted 过滤 | 高 | dbaa 进程 OOM | **必须从一开始就加 `granted` 过滤 + visited 兜底**（吃过这个亏） |
| Go 驱动稳定性 / 兼容性 | 中 | 连接异常 | 选 `gitee.com/chunanyong/dm` 官方推荐版，避免 fork 版 |
| 集群场景测试不到位 | 高 | 集群环境用户体验差 | 本期明确不支持，文档写清楚 |

### 6.2 中风险

| 风险 | 缓解 |
|---|---|
| DM 试用版有功能限制 | Phase 0 先验证试用版能否启用 AWR、慢 SQL 日志、SP_CLOSE_SESSION |
| AWR 报告生成 API 我记错 | 真机验证一遍每个 procedure 调用 |
| 错误码映射表难维护（2666 个）| 只维护高频 100 个的映射表，其余用 V$ERR_INFO 实时查 |

### 6.3 低风险

| 风险 | 缓解 |
|---|---|
| 4 库风格不一致 | review 流程已成熟，参考 OG 模板 |
| build tag 配置复杂 | 已有现成模板（oracle/postgres/mysql 都是这样） |

---

## 7. Open Questions

### 已决策

| 问题 | 决策 | 决策日期 |
|---|---|---|
| 测试机 | 复用 47.251.30.180，端口 5237 | 2026-04-30 |
| DM 试用版 | 个人开发版（免费 1 年，无功能限制）| 2026-04-30 |
| 部署形态 | 原生安装（非 Docker）| 2026-04-30 |

### 待决策

1. **品牌策略**：dbaa 品牌（农行版）是否需要 DM 支持？
   - 如果需要，build tag 是 `dbaa+dm`，构建矩阵 ×2
   - 影响：是否需要发 dbaa 版本的 DM 二进制
2. **集群优先级**：DSC 客户多吗？要不要把 DSC 提前到 Phase 3？
   - 默认计划：DSC/MPP/DPC 全在 Phase 5+ 远期
3. **DEM 集成**：要不要做 Prometheus exporter？
   - 默认计划：远期 v1.4+，可选 enterprise 功能
4. **验收测试模型**：5 模型回归（DeepSeek/GLM/Kimi/Moonshot/Qwen）是否够？
   - 默认沿用，跟现有 4 库测试矩阵一致

---

## 8. 与已有架构的对齐

### 8.1 不破坏的设计

- ✅ `connection.Manager` 多产品支持架构
- ✅ skill registry 按产品组织
- ✅ 安全分级（Level 0/1/2/3）
- ✅ 通用 system prompt 双变体（strict / templated）
- ✅ Sentinel 框架（44 指标 / 7 探针）
- ✅ 规则引擎流程（ailinkdb/data → 生成代码）
- ✅ 4 库一致性 review

### 8.2 沿用的最佳实践（来自 OG/Oracle/PG/MySQL 经验）

- 复用 oracle 的 SQL 适配模式（DM Oracle 兼容模式）
- blocktree 实现**必须**学 OG 修复后版本（`granted` 过滤 + visited 兜底）
- SQL Advisor 框架统一接入
- 安全分级 wrapper 统一调用

### 8.3 需要扩展的部分

- `internal/connection/products.go` 新增 DM 产品定义
- `internal/setup/install_wizard.go` 新增 DM 安装向导步骤
- `cmd/opendb/main.go` 注册 DM driver factory
- `internal/brand/` 是否需要 DM 专属文案（一般不用）

---

## 9. 工作量估算

| Phase | 工作量 | 关键产出 |
|---|---|---|
| Phase 0 调研 + 环境 | 5-7 天 | DM 实例 + 差异清单 |
| Phase 1 骨架 + read-only | 10-14 天 | 12 个 skill + 真机校验 |
| Phase 2 故障 + 规则 | 10-14 天 | 4 故障场景 + 规则引擎 + benchmark |
| Phase 3 admin + AWR | 5-7 天 | kill / params / awr |
| Phase 4 Sentinel + 文档 | 5-7 天 | 探针 + quickstart + reference |
| **小计 P1** | **35-49 天 (5-7 周)** | **v1.2.0 可发布** |
| Phase 5 远期 | 持续 | RAG + 集群 + DEM |

**单人独干**约 5-7 周到 P1，**配合 DM DBA review** 可缩至 4-5 周（关键在每条 SQL 真机校验时间）。

---

## 10. 资料来源（Sources）

### 官方文档
- [达梦官方动态视图文档](https://eco.dameng.com/document/dm/zh-cn/pm/dynamic-management.html)
- [达梦 Oracle 兼容模式文档](https://eco.dameng.com/document/dm/zh-cn/pm/oracle-compatible.html)
- [达梦从 Oracle 迁移指南](https://eco.dameng.com/document/dm/zh-cn/start/oracle_dm.html)
- [达梦运维监控工具文档](https://eco.dameng.com/document/dm/zh-cn/ops/tool-monitor.html)
- [达梦性能诊断文档](https://eco.dameng.com/document/dm/zh-cn/ops/performance-diagnosis)
- [达梦错误码 FAQ](https://eco.dameng.com/document/dm/zh-cn/faq/faq-errorcode.html)
- [达梦 JDBC 编程指南](https://eco.dameng.com/document/dm/zh-cn/pm/jdbc-rogramming-guide.html)
- [达梦数据库下载](https://eco.dameng.com/download/)

### Go 驱动
- [gitee.com/chunanyong/dm — 官方推荐 Go 驱动](https://gitee.com/chunanyong/dm)
- [github.com/godoes/gorm-dameng — GORM 适配](https://github.com/godoes/gorm-dameng)

### 技术博客（V$ 视图与诊断）
- [达梦数据库的系统视图 V$SESSION](https://blog.csdn.net/lee_vincent1/article/details/139971076)
- [达梦数据字典和动态性能视图](https://www.cnblogs.com/xuchuangye/p/16589267.html)
- [DM8 元数据查询常用 SQL](https://www.modb.pro/db/581520)
- [达梦 AWR 报告生成](https://blog.csdn.net/jiangsirx/article/details/139908965)
- [达梦慢 SQL 追踪及优化](https://eco.dameng.com/community/training/eb334ad9efcd7e17bd160b6b07b56bb0)
- [达梦数据库 kill 会话](https://blog.csdn.net/lee_vincent1/article/details/140216689)
- [达梦数据库锁表解决方案](https://www.cnblogs.com/pugang/p/16531352.html)
- [DM 性能优化与诊断实战](https://blog.csdn.net/zjshaha/article/details/127043541)
- [DM 错误代码汇总](https://www.cndba.cn/cndba/dave/article/3738)

### 集群相关
- [Dameng DMMPP 官方介绍](https://en.dameng.com/view/35.html)
- [DMRWC 共享存储集群](https://en.dameng.com/view/23.html)

### 内部参考（同仓库）
- `docs/design-local-model-optimization.md` — Prompt 框架对所有 DB 的统一原则
- `docs/CHANGELOG.md` v1.1.22 — blocktree OOM 修复（DM 实现要复用此修复）
- `internal/oracle/` — DM Oracle 兼容模式下的最近参考
- `internal/opengauss/skill/monitor/blocktree.go` — 阻塞链 SQL 的正确写法（带 granted 过滤）

---

## 11. 下一步

### 当前状态
- ✅ 设计文档完成（v0.2）
- ✅ 测试机锁定（47.251.30.180:5237 + 个人开发版）
- ⏳ **等待 design review**

### Review 通过后启动顺序

```
设计 review (1 天)
   ↓
Phase 0 调研 + 环境 (5-7 天)
   ├─ 安装 DM8 个人开发版到 47.251.30.180:5237
   ├─ 通读关键手册章节
   ├─ 输出 dm-vs-oracle-views.md
   └─ Go 驱动 hello world 跑通
   ↓
Phase 0 review (0.5 天) — 这一步不能省，确认 SQL 兼容 / 视图字段都对
   ↓
Phase 1 骨架 + read-only (10-14 天)
   ↓
Phase 2-4 (每个 Phase 完成后 review)
   ↓
v1.2.0 发布
```

### Phase 0 Review 关键点

每个 Phase 完成后我都会主动找你 review，**Phase 0 review 是最关键的一次**，重点核对：
1. DM 实例是否真能跑（连接、查询、AWR 启用）
2. dm-vs-oracle-views.md 差异清单是否准确（避免我编造视图名）
3. Go 驱动 hello world 是否稳定（连接池、超时、SSL 等）
4. 故障注入脚本是否能在 V$LOCK 看到真实锁等待

如果 Phase 0 发现重大偏差（如某关键视图字段名跟我设计文档不符），会先回到这份文档修订，再继续后续 Phase。

**目前不写任何代码**，等 design review 拍板。
