# DM SQL 排错与视图陷阱

DM 8.x 与 Oracle 兼容模式（COMPATIBLE_MODE=2）下，许多视图列名、表名跟 Oracle 接近但**不完全相同**。本文档记录 OpenDB / dbaa 真机验证（DM 8.1.4.200）暴露的祖传陷阱，给 DBA 和写 SQL 的同事查阅。

## 排错通用流程

DM 报 `Error -2111: 无效的列名 [XXX]` 时，**别猜列名**。直接查实际：

```sql
-- 列出某视图的全部列
SELECT * FROM V$DYNAMIC_TABLE_COLUMNS WHERE TABNAME = 'V$XXX' ORDER BY COLID;
```

注意 V$DYNAMIC_TABLE_COLUMNS 自己的列名是 `COLNAME`（不是 NAME）。

## 时间字段陷阱

DM 不同视图的时间列名**不一致**：

| 视图 | 时间列名 | 备注 |
|---|---|---|
| V$DEADLOCK_HISTORY | `HAPPEN_TIME` | 这个表确实有 HAPPEN_TIME |
| V$DANGER_EVENT | `OPTIME` | **不是 HAPPEN_TIME**！容易和上面混淆 |
| V$RUNTIME_ERR_HISTORY | `ERR_TIME` | 第三种命名 |

```sql
-- ❌ 错的（V$DANGER_EVENT）
SELECT * FROM V$DANGER_EVENT WHERE HAPPEN_TIME > SYSDATE - 1/24;
-- Error -2111: 无效的列名 [HAPPEN_TIME]

-- ✅ 对的
SELECT * FROM V$DANGER_EVENT WHERE OPTIME > SYSDATE - 1/24;
```

## 错误码视图陷阱

**V$ERR_INFO 实测只有 2 列**，不是 Oracle 风格的多列字典：

```sql
-- ❌ 错的（凭 Oracle 经验猜）
SELECT ERR_CODE, ERR_LEVEL, ERR_TYPE, ERR_DESC FROM V$ERR_INFO WHERE ERR_CODE = -2622;

-- ✅ 对的（实测列）
SELECT CODE, ERRINFO FROM V$ERR_INFO WHERE CODE = -2622;
-- → CODE: -2622  ERRINFO: 分区名与数据库对象名称冲突
```

V$RUNTIME_ERR_HISTORY 的错误码字段也跟 Oracle 不同：

```sql
-- ❌ 错的
SELECT ERR_CODE, ERR_DESC FROM V$RUNTIME_ERR_HISTORY;

-- ✅ 对的（带 ECPT_ 前缀）
SELECT ECPT_CODE, ECPT_DESC FROM V$RUNTIME_ERR_HISTORY;
```

## 实例与数据库视图陷阱

### V$INSTANCE 没有 `VERSION` 列

```sql
-- ❌ 错的
SELECT NAME, VERSION FROM V$INSTANCE;
-- Error -2111: 无效的列名 [VERSION]

-- ✅ 对的（有三个版本字段）
SELECT NAME, SVR_VERSION, DB_VERSION, BUILD_VERSION FROM V$INSTANCE;
```

### V$INSTANCE / V$DATABASE 列名带 `$` 后缀

`STATUS$` 和 `MODE$` 在 V$INSTANCE，`STATUS$` 和 `ROLE$` 在 V$DATABASE。SELECT 时记得用别名：

```sql
-- ⚠️ 容易忽略 $ 后缀
SELECT NAME, ROLE$ AS ROLE, STATUS$ AS STATUS FROM V$DATABASE;
```

### V$DATABASE 没有 `DBID` 列（Oracle 才有）

```sql
-- ❌ 错的（DM 没此列）
SELECT NAME, DBID FROM V$DATABASE;

-- ✅ 对的（用 NAME + LAST_STARTUP_TIME 标识）
SELECT NAME, LAST_STARTUP_TIME FROM V$DATABASE;
```

DM 真实列：`NAME, CREATE_TIME, ARCH_MODE, LAST_CKPT_TIME, STATUS$, ROLE$, MAX_SIZE, TOTAL_SIZE, DSC_NODES, OPEN_COUNT, STARTUP_COUNT, LAST_STARTUP_TIME`。

### ROLE$ / STATUS$ 是 TINYINT 必须翻译

```sql
SELECT
    NAME,
    CASE ROLE$
        WHEN 0 THEN 'PRIMARY'
        WHEN 1 THEN 'STANDBY'
        WHEN 2 THEN 'DBSTANDBY'
        WHEN 3 THEN 'BACKUP_PENDING'
        ELSE TO_CHAR(ROLE$)
    END AS ROLE,
    CASE STATUS$
        WHEN 1 THEN 'STARTUP'
        WHEN 2 THEN 'AFTER_REDO'
        WHEN 3 THEN 'BACKUP'
        WHEN 4 THEN 'OPEN'
        WHEN 5 THEN 'SUSPEND'
        ELSE TO_CHAR(STATUS$)
    END AS STATUS
FROM V$DATABASE;
```

## DM 没有的视图（Oracle 有）

凭 Oracle 经验猜测会失败：

| Oracle 视图 | DM 替代 |
|---|---|
| `V$RESOURCE_LIMIT` | DM 没此视图。资源限制查 V$PARAMETER（上限）+ 实时统计 V$SESSIONS / V$TRX / V$MEM_POOL |
| `V$SQLAREA` | DM 没此视图。Top SQL 用 V$SQL_HISTORY GROUP BY SQL_ID |
| `V$OSSTAT` | DM 没此视图。主机指标分散在 V$INSTANCE / V$THREADS / V$PROCESS / V$MEM_POOL |
| `V$SESSION_LONGOPS` | DM 没此视图。长操作监控用 V$LONG_EXEC_SQLS |

```sql
-- ❌ 错的
SELECT * FROM V$RESOURCE_LIMIT;

-- ✅ 对的（DM 资源限制走参数）
SELECT NAME, VALUE FROM V$PARAMETER
WHERE NAME IN ('MAX_SESSIONS', 'MEMORY_TARGET', 'BUFFER', 'WORKER_THREADS');

SELECT
    (SELECT COUNT(*) FROM V$SESSIONS) AS sessions_used,
    (SELECT COUNT(*) FROM V$SESSIONS WHERE STATE = 'ACTIVE') AS sessions_active,
    (SELECT COUNT(*) FROM V$TRX) AS trx_used
FROM DUAL;
```

## V$SQL_HISTORY 是单次执行历史，不是聚合

跟 Oracle V$SQLAREA 不同，V$SQL_HISTORY 每次 SQL 执行写一行，所以查 Top SQL 必须 GROUP BY：

```sql
-- ❌ 错的（一行一次执行，COUNT 永远 = 行数）
SELECT SQL_ID, EXECUTIONS FROM V$SQL_HISTORY ORDER BY EXECUTIONS DESC;

-- ✅ 对的
SELECT
    SQL_ID,
    COUNT(*) AS EXEC_COUNT,
    SUM(TIME_USED) AS TOTAL_TIME_MS,
    ROUND(AVG(TIME_USED), 2) AS AVG_TIME_MS,
    SUBSTR(MIN(TOP_SQL_TEXT), 1, 100) AS SAMPLE_SQL
FROM V$SQL_HISTORY
WHERE SQL_ID IS NOT NULL
GROUP BY SQL_ID
ORDER BY EXEC_COUNT DESC
LIMIT 20;
```

## V$SQL_HISTORY 是累积，V$LONG_EXEC_SQLS 是实时

V$SQL_HISTORY / V$SYSTEM_EVENT / V$WAIT_HISTORY / V$RUNTIME_ERR_HISTORY 都是 **累积自上次 reset 的统计**，不是"现在正在发生"：

```sql
-- 查"当前正在跑的长 SQL"
SELECT * FROM V$LONG_EXEC_SQLS;  -- 实时

-- 查"历史出现过的慢 SQL"
SELECT * FROM V$SQL_HISTORY ORDER BY TIME_USED DESC;  -- 累积，含已 DROP 表的旧 SQL
```

诊断时混淆这两类视图会得出错误结论（参考 dm-llm-benchmark.md 中 F3 case）。

## 视图目录陷阱

### 列出全部 V$ 视图

```sql
-- ❌ 不全（只能找到 ~10 个 注册成普通视图的）
SELECT NAME FROM SYSOBJECTS WHERE NAME LIKE 'V$%' AND TYPE$ = 'VIEW';

-- ✅ 完整（380+ 项动态视图）
SELECT NAME, SCHNAME FROM V$DYNAMIC_TABLES ORDER BY NAME;
```

### 查某视图的列

V$DYNAMIC_TABLE_COLUMNS 自身的列名是 **COLNAME**（不是 NAME）：

```sql
-- ❌ 错
SELECT NAME FROM V$DYNAMIC_TABLE_COLUMNS WHERE TABNAME = 'V$SESSIONS';
-- Error -2111: 无效的列名 [NAME]

-- ✅ 对（用 SELECT * 看全部列）
SELECT * FROM V$DYNAMIC_TABLE_COLUMNS WHERE TABNAME = 'V$SESSIONS' ORDER BY COLID;
-- 输出含 TABNAME, COLNAME, COLID, TYPE$, LENGTH$, ...
```

## 杀会话的强制语法

DM 杀会话 **必须用 SP_CLOSE_SESSION**，不能用 Oracle 语法：

```sql
-- ❌ 错的（Oracle 语法在 DM 失败）
ALTER SYSTEM KILL SESSION '140304100,2458068';

-- ✅ 对的（DM 强制语法）
CALL SP_CLOSE_SESSION(140304100);
```

参数是 V$SESSIONS.SESS_ID（一个数字，不是 'sid,serial#' 格式）。

## EXPLAIN 输出格式

DM 的 EXPLAIN 输出每行是 **三元组** `[代价ms, 估算行数, 字节数]`：

```
EXPLAIN SELECT COUNT(*) FROM bench_users WHERE status = 3;

#NSET2: [1, 100, 24]
  #PRJT2: [1, 100, 24]
    #CSCN2: [1, 100, 24]; INDEX33555476(BENCH_USERS)
```

## 常见错误码对照（部分）

| Code | 含义 | 常见原因 |
|---|---|---|
| -2007 | 语法分析出错 | SQL 拼写错 / 视图列名不对 |
| -2099 | 函数不存在 | 用了 Oracle 独有函数（如 V$RESOURCE_LIMIT.MAX_USED_AREA） |
| -2111 | 无效的列名 | **本文档主要场景**：列名跟 Oracle 不同 |
| -2622 | 分区名与数据库对象名称冲突 | 创建分区表时分区名重名 |
| -3324 | 死锁 | 多事务循环等待，DM 自动检测并回滚一方 |
| -6403 | 死锁错误码 | 应用层捕获用 |
| -7008 | 操作超时 | 锁等待 / SQL 执行超过 LOCK_WAIT_TIMEOUT |

完整查询：

```sql
SELECT CODE, ERRINFO FROM V$ERR_INFO WHERE CODE = -<your_code>;
SELECT CODE, ERRINFO FROM V$ERR_INFO WHERE UPPER(ERRINFO) LIKE '%死锁%';
```

或在 dbaa 里：

```
$ dbaa -c <connection> /errcode 2622
$ dbaa -c <connection> /errcode 死锁
```

## 进一步阅读

- DM skill 列表：[dm-skill-coverage.md](./dm-skill-coverage.md)
- DM /llm benchmark：[dm-llm-benchmark.md](./dm-llm-benchmark.md)
- DMProfile prompt（含全部陷阱）：[`internal/engine/profile/dm.go`](../internal/engine/profile/dm.go)
- 真机修复历史：[CHANGELOG.md](./CHANGELOG.md) v1.1.24/v1.1.25/v1.1.26
