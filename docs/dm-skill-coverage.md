# DM Skill 能力清单

OpenDB / dbaa 在 DM 8.1.4+ 上的 31 个 skill。所有 skill 都通过 `/<skill_name>` 在 REPL 调用，或 `dbaa -c <connection> /<skill_name>` 在批量模式调用。

适配版本：v1.1.26（2026-05-02）
真机验证：DM 8.1.4.200 单机部署
驱动：`github.com/HuaweiCloudDeveloper/dm-go-driver`（仅 linux/windows）

## 一、监控类 (`internal/dm/skill/monitor/` — 25 个)

### 会话与连接

| Skill | 数据源 | 用途 |
|---|---|---|
| **sessions** | V$SESSIONS | 全部会话列表（按状态/创建时间排序），summary 含 `kill_session_syntax` |
| **activesessions** | V$SESSIONS WHERE STATE='ACTIVE' | 活跃会话 + SQL_TEXT 头 80 字符，summary 含 `kill_oldest_cmd: CALL SP_CLOSE_SESSION(<sess_id>)` |
| **users** | DBA_USERS / DBA_SYS_PRIVS / DBA_ROLE_PRIVS | 用户列表 / 单用户详情（含权限+角色） |

### 锁与阻塞

| Skill | 数据源 | 用途 |
|---|---|---|
| **locks** | V$LOCK | 全部锁信息，summary 含 BLOCKED 计数 + LTYPE/LMODE 分布 |
| **blocktree** | V$LOCK self-join + V$SESSIONS | 阻塞链（waiter→holder），summary 含 `kill_blocker_cmd: CALL SP_CLOSE_SESSION(<id>)`。SQL 含 `BLOCKED=0` 过滤防 OOM |
| **deadlock** | V$DEADLOCK_HISTORY | 历史死锁（最近 50 条，按 HAPPEN_TIME 排序） |

### 等待事件与告警

| Skill | 数据源 | 用途 |
|---|---|---|
| **waits** | V$SYSTEM_EVENT | 等待事件 Top 20（按累计 TIME_WAITED_MICRO） |
| **alert** | V$DEADLOCK_HISTORY / V$DANGER_EVENT / V$RUNTIME_ERR_HISTORY / V$LOCK / V$LONG_EXEC_SQLS | 告警计数聚合 |
| **anomalies** | 7 个并发 SQL（V$LOCK / V$SESSIONS / V$LONG_EXEC_SQLS / V$DEADLOCK_HISTORY / V$DANGER_EVENT / V$RUNTIME_ERR_HISTORY） | 异常上下文快照（诊断起手），summary 含 `is_anomaly`/`anomaly_signals`/`next_step_hint` |

### 实例与健康

| Skill | 数据源 | 用途 |
|---|---|---|
| **info** | V$INSTANCE / V$DATABASE / V$DM_INI | 实例 + 数据库 + 关键参数总览，ROLE$ 翻译 PRIMARY/STANDBY |
| **health** | 11 个并发 SQL 聚合 | 健康总览 dashboard（实例状态/启动时间/主备角色/会话总数/活跃会话/锁等待/死锁/危险事件/错误/长 SQL/checkpoint） |
| **standby** | V$DATABASE.ROLE$ / V$RLOG / V$ARCH_SEND_INFO | 主备状态 + 当前 LSN + 归档发送，ROLE$/STATUS$ 数字翻译为字符串 |
| **cluster** | V$DSC_EP_INFO / V$MPP_INSTANCES / V$DMWATCHER_INFO / V$ARCH_STATUS | DM 集群状态（DSC/MPP/DW），单机部署正确报告"无集群" |

### 性能与资源

| Skill | 数据源 | 用途 |
|---|---|---|
| **dbtop** | V$INSTANCE / V$SESSIONS / V$LONG_EXEC_SQLS（实时刷新） | 实时 top dashboard（默认 2 秒刷新一次） |
| **perfsnap** | WRM$_SNAPSHOT / V$PARAMETER | AWR 快照状态 + 报告生成命令提示 |
| **mempool** | V$MEM_POOL / V$BUFFERPOOL / V$DICT_CACHE | 内存池 + 缓冲池命中率 + 字典缓存 |
| **resource** | V$PARAMETER / V$SESSIONS / V$TRX / V$MEM_POOL | 资源限制 + 实时使用率（DM 无 V$RESOURCE_LIMIT，用参数+计数代替） |
| **os** | V$INSTANCE / V$THREADS / V$PROCESS / V$MEM_POOL | 实例主机视角（线程类别 / 进程数 / 内存池总计） |

### 存储与归档

| Skill | 数据源 | 用途 |
|---|---|---|
| **segments** | DBA_SEGMENTS | 段空间 Top 20（按大小或 owner） |
| **redo** | V$RLOGFILE / V$RLOG | Rlog 文件 + LSN 状态 |
| **tempusage** | DBA_DATA_FILES / SF_GET_TS_USED_SPACE | 临时表空间使用 |
| **archive** | V$DM_ARCH_INI / V$ARCH_STATUS / V$ARCHIVED_LOG | 归档配置 + 状态 + 最近 10 条 |
| **indexhealth** | DBA_INDEXES / DBA_SEGMENTS / DBA_IND_COLUMNS | 失效 / 超大 / 空索引 |

### 错误码与发现

| Skill | 数据源 | 用途 |
|---|---|---|
| **errcode** | V$ERR_INFO / V$RUNTIME_ERR_HISTORY | 错误码字典查询（精确码 / 模糊描述 / 最近触发）。V$ERR_INFO 仅 CODE+ERRINFO 两列 |
| **views** | V$DYNAMIC_TABLES（380+ 项） | V$ 视图自动发现，按主题分类（session/lock/wait/sql/memory/storage/system/stats/err） |

## 二、查询类 (`internal/dm/skill/query/` — 3 个)

| Skill | 数据源 | 用途 |
|---|---|---|
| **topsql** | V$SQL_HISTORY GROUP BY SQL_ID | Top SQL（按执行次数），summary 含 `hottest_sql_id` / `hottest_avg_time_ms` |
| **slowsql** | V$LONG_EXEC_SQLS | 当前正在执行的长 SQL（实时，不是累积） |
| **explain** | EXPLAIN \<sql\> | 执行计划，输出三元组 [代价ms, 行数, 字节数] |

## 三、表结构类 (`internal/dm/skill/schema/` — 1 个)

| Skill | 数据源 | 用途 |
|---|---|---|
| **tableinfo** | ALL_TAB_COLUMNS / ALL_INDEXES / DBA_SEGMENTS（Oracle 兼容字典） | 表结构 + 列 + 索引 + 段大小 |

## 四、AI 类 (`internal/dm/skill/ai/` — 1 个)

| Skill | 用途 |
|---|---|
| **sentinel** | 异常持续采集（30s tick），阻塞>3 / 长 SQL>50 / 死锁数变化 触发 alert。`/sentinel start\|stop\|status` |

## 五、共享 skill (复用通用实现 — 1 个)

| Skill | 用途 |
|---|---|
| **sql** | 直接执行任意 SQL，read-only 默认 |

## 六、AI 诊断（来自 oracle 复用）

| Skill | 用途 |
|---|---|
| **/llm \<question\>** | LLM 主动诊断，调用工具循环（auto/assist 模式），最多 20 轮。复用 oracle DiagnoseSkill |

## DM 限制 / 不支持的 skill

DM 内核或视图层面缺失，OpenDB 暂不实现：

| 不实现 | 原因 |
|---|---|
| **vacuum / autovacuum** | DM 内核自动 purge 死元组，无需手动维护 |
| **bloat** | DM 自动管理，无 PG 那种 bloat 概念 |
| **wal slots / replication slots** | DM 用 RLOG 物理日志机制，不是 WAL |
| **MOT 内存优化表** | DM 没此特性 |
| **ASM** | DM 用文件系统直存，没 ASM |
| **rule_skill 规则引擎兜底** | 用户决定暂缓（v1.1.26） |

## 调用示例

REPL 模式：
```
$ dbaa
DM·(prod)❯ /sessions
DM·(prod)❯ /llm 当前实例存在什么问题
DM·(prod)❯ /sentinel start
```

批量模式：
```
$ dbaa -c prod /sessions
$ dbaa -c prod /errcode 2622
$ dbaa -c prod /views session
```

## 真机验证状态（DM 8.1.4.200）

| 类别 | 验证 |
|---|---|
| 25 个 monitor skill | 全部跑通 |
| 3 个 query skill | 全部跑通 |
| 1 个 schema skill | 全部跑通 |
| 1 个 sentinel skill | start/stop/status 状态机跑通 |
| dbtop ResultRefresh | 编译验证 + RenderFrame 单测 |

## 单元测试覆盖

```
ai      coverage: 89.4% (9 tests)
monitor coverage: 78.6% (96 tests)
query   coverage: 79.1% (14 tests)
schema  coverage: 95.8% (6 tests)
util    coverage: 100%  (8 tests)
Total:  133 tests, all PASS with -race
```

## 进一步阅读

- 视图陷阱 + 排错指南：[dm-troubleshooting.md](./dm-troubleshooting.md)
- DM /llm benchmark：[dm-llm-benchmark.md](./dm-llm-benchmark.md)
- DMProfile prompt：[`internal/engine/profile/dm.go`](../internal/engine/profile/dm.go)
