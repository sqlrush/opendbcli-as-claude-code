# /dbtop 实时监控大盘 设计文档

## 目标

在 OpenDB REPL 中实现 `/dbtop` 命令，提供 Oracle 数据库实时监控大盘。参考 [sqlrush/dbtop](https://github.com/sqlrush/dbtop) 的监控指标，适配 OpenDB 的聊天式 REPL 交互模式。

## 架构

内容区域原地刷新，固定 28 行高度。用户执行 `/dbtop` 后，输出占据内容区域一块固定区域，每秒用 ANSI 光标定位回起始行覆盖刷新。退出后最后一帧留在聊天历史中。

## 命令格式

```
/dbtop          — 默认 1 秒刷新
/dbtop 3        — 3 秒刷新
```

## 布局（固定 28 行）

```
╭─ dbtop ── oracle 19c ── mydb01 ── PRIMARY ── ● HEALTHY ── 15:32:01 ─╮  ─┐
│                                                                      │   │
│  SGA  4,096M    PGA  512M    db% ▓▓░░░░░░░░ 12.3   WTR% ▓░░░░░░░░  5.1 │   │ 头部框 5行
│  SN 142  AN 23  ASC 8  ASI 3  IDL 111 │ TPS 1,250 QPS 8,430 REDO 2.0k  │   │
│                                                                      │   │
╰──────────────────────────────────────────────────────────────────────╯  ─┘
╭─ Top Wait Events ────────────────────────────────────────────────────╮  ─┐
│                                                                      │   │
│  EVENT                          WAITS       TIME(s)      PCT         │   │ 事件框 8行
│  db file sequential read        3,210        12.5s    ▇▇▇▇▇▇▇░ 34.2%│   │
│  log file sync                  1,050         4.8s    ▇▇▇░░░░░ 13.1%│   │
│  buffer busy waits                580         2.9s    ▇▇░░░░░░  7.9%│   │
│  db file scattered read           320         1.8s    ▇░░░░░░░  4.9%│   │
│  enq: TX - row lock               150         1.1s    ▇░░░░░░░  3.0%│   │
╰──────────────────────────────────────────────────────────────────────╯  ─┘
╭─ Active Sessions (10) ───────────────────────────────────────────────╮  ─┐
│                                                                      │   │
│  SID   USR        SQLID         EVENT                  E/T  SQL      │   │
│  301   REPORT     def456abc     ON CPU                5.0s  SELECT.. │   │
│  142   APP_USER   abc123def     db file seq read      3.2s  SELECT.. │   │ 会话框 14行
│  445   APP_USER   ghi789xyz     buffer busy waits     2.3s  UPDATE.. │   │
│  287   BATCH      xyz789ghi     log file sync         1.1s  INSERT.. │   │
│  512   BATCH      jkl012mno     ON CPU                0.4s  DELETE.. │   │
│  ...                                                                 │   │
│                                          ... 还有 5 个活跃会话未显示  │   │
╰──────────────────────────────────────────────────────────────────────╯  ─┘
 q 退出 │ 刷新: 1s                                                15:32:01   ── 状态栏 1行
```

## 面板详细设计

### 1. 头部框 (DB Info + Instance) — 5 行

**DB Info 行：** 标题栏合并展示版本、实例名、角色、健康状态、时间戳。

**数据行 1：** SGA/PGA 内存 + db%/WTR% 带条形图。

**数据行 2：** 会话计数 (SN/AN/ASC/ASI/IDL) + 吞吐量 (TPS/QPS/REDO kB/s)。

**SQL 查询：**

```sql
-- 版本/实例（首帧查询一次，缓存）
SELECT banner FROM v$version WHERE ROWNUM = 1
SELECT instance_name, startup_time FROM v$instance
SELECT database_role FROM v$database

-- SGA/PGA（每帧刷新）
SELECT ROUND(SUM(value)/1024/1024, 0) FROM v$sga
SELECT ROUND(value/1024/1024, 0) FROM v$pgastat WHERE name = 'total PGA allocated'

-- db%/WTR%（delta 计算，需要两帧）
SELECT
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB CPU') AS cpu_time,
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB time') AS db_time

-- 会话计数
SELECT
  COUNT(*) AS sn,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS != 'Idle' THEN 1 END) AS an,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS NOT IN ('Idle','User I/O','System I/O') THEN 1 END) AS asc,
  COUNT(CASE WHEN s.STATUS='ACTIVE' AND s.WAIT_CLASS IN ('User I/O','System I/O') THEN 1 END) AS asi,
  COUNT(CASE WHEN s.STATUS='INACTIVE' OR s.WAIT_CLASS='Idle' THEN 1 END) AS idl
FROM v$session s WHERE s.TYPE = 'USER'

-- TPS/QPS/REDO（delta 计算，需要两帧）
SELECT name, value FROM v$sysstat
WHERE name IN ('user commits', 'user rollbacks', 'execute count', 'redo size')
```

**Delta 计算逻辑：**
- 存储上一帧的 (cpu_time, db_time, timestamp, commits, rollbacks, executes, redo_size)
- db% = cpu_delta / (time_delta_sec * CPU 核数) * 100
- WTR% = (db_delta - cpu_delta) / db_delta * 100
- TPS = (commits_delta + rollbacks_delta) / time_delta_sec
- QPS = executes_delta / time_delta_sec
- REDO = redo_delta / 1024 / time_delta_sec (kB/s)
- 第一帧：db%/WTR%/TPS/QPS/REDO 显示 "—"（无 delta 数据）

### 2. 事件框 (Top Wait Events) — 8 行

显示 Top 5 等待事件，RT (Real-Time) 模式，delta 计算。

**列：** EVENT / WAITS / TIME(s) / PCT（含条形图）

**SQL 查询：**

```sql
SELECT
  (SELECT value FROM v$sys_time_model WHERE stat_name = 'DB CPU') AS cpu_time,
  e.EVENT,
  e.TOTAL_WAITS,
  e.TIME_WAITED_MICRO,
  e.WAIT_CLASS
FROM v$system_event e
WHERE e.WAIT_CLASS != 'Idle' AND e.TOTAL_WAITS > 0
ORDER BY e.TIME_WAITED_MICRO DESC
```

**PCT 计算 (RT 模式)：**
- cpu_diff = curr_cpu - prev_cpu
- event_time_diff = curr_event_time - prev_event_time
- total_diff = sum(all_event_time_diff) + cpu_diff
- PCT = event_time_diff / total_diff * 100
- DB CPU 作为虚拟首行参与 PCT 计算

**条形图：** 8 字符宽，用 ▇ 和 ░ 填充，按 PCT 比例。

### 3. 会话框 (Active Sessions) — 14 行

固定显示 Top 10 活跃会话，按 E/T 降序排列。

**列（按优先级）：** SID / USR / SQLID / EVENT / E/T / SQL / PROG / STA

核心 6 列始终显示，宽屏（>100 列）追加 PROG、STA。SQL 列放最后，填满剩余宽度，超长截断。

**SQL 查询：**

```sql
SELECT
  s.SID,
  s.USERNAME,
  s.SQL_ID,
  NVL(s.EVENT, 'ON CPU'),
  CASE WHEN s.STATUS='ACTIVE' AND s.SQL_EXEC_START IS NOT NULL
       THEN ROUND((SYSDATE - s.SQL_EXEC_START) * 86400, 1)
       ELSE 0 END AS elapsed_sec,
  (SELECT SUBSTR(sql_text, 1, 80) FROM v$sqlarea WHERE sql_id = s.SQL_ID AND ROWNUM = 1),
  s.PROGRAM,
  s.STATUS
FROM v$session s
WHERE s.TYPE = 'USER' AND s.STATUS = 'ACTIVE' AND s.WAIT_CLASS != 'Idle'
ORDER BY elapsed_sec DESC
```

**截断提示：** 若活跃会话总数 > 10，框底部显示 "... 还有 N 个活跃会话未显示"。

### 4. 状态栏 — 1 行

正常状态：`q 退出 │ 刷新: 1s                                   15:32:01`

CRITICAL 状态：`● CRITICAL │ 输入 /health 查看详细诊断           15:32:01`

## 健康状态系统

### 三级健康状态

| 状态 | 显示 | 颜色 | 触发规则 |
|------|------|------|----------|
| HEALTHY | ● HEALTHY | 绿色 | 所有指标正常 |
| WARNING | ● WARNING | 黄色 | 任一指标达到 WARNING 阈值 |
| CRITICAL | ● CRITICAL | 红色 | 任一指标达到 CRITICAL 阈值 |

### 阈值规则

| 指标 | 正常 (白色) | WARNING (黄色) | CRITICAL (红色) |
|------|------------|----------------|-----------------|
| AN (活跃会话) | < 30 | 30 - 80 | > 80 |
| db% | < 50 | 50 - 80 | > 80 |
| WTR% | < 30 | 30 - 60 | > 60 |
| 单事件 PCT | < 30% | 30 - 50% | > 50% |
| 会话 E/T | < 300s | 300 - 600s | > 600s |
| TPS 突降 | — | 降幅 > 50% | 降幅 > 80% |

异常指标在面板中用对应颜色高亮显示。整体健康状态取所有指标中最严重的级别。

CRITICAL 时状态栏引导用户执行 `/health` 查看详细诊断。

## 颜色方案

| 元素 | 颜色 |
|------|------|
| 框边框/标签 | 暗灰 #888888 |
| 数值（正常） | 亮白 |
| db%/WTR% 条形图 | 蓝色渐变 |
| PCT 条形图 | 蓝色 |
| "ON CPU" 事件 | 绿色 |
| I/O 等待事件 | 黄色 |
| WARNING 指标 | 黄色 |
| CRITICAL 指标 | 红色 |
| HEALTHY 标记 | 绿色 |
| 状态栏提示文字 | 暗灰 |

## 刷新机制

### 原地刷新（方案 A）

1. 用户输入 `/dbtop`，第一帧通过 `writeOutputLine` 正常输出 28 行
2. 记录起始行位置 (`startRow`)
3. 后续每帧用 ANSI 光标定位 `\033[{startRow};1H` 回到起始位置，逐行覆盖
4. 刷新期间接管键盘输入（类似 tableBrowser）
5. 用户按 q/ESC/Ctrl+C 退出，最后一帧留在内容区域作为历史记录

### 数据采集

每个刷新周期并行执行 SQL 查询（使用 goroutine），减少总延迟：

- goroutine 1: SGA + PGA + db%/WTR% 数据
- goroutine 2: 会话计数 + TPS/QPS/REDO 数据
- goroutine 3: 等待事件数据
- goroutine 4: 活跃会话明细

### Delta 状态管理

```go
type dbtopState struct {
    // 上一帧的累计值（用于 delta 计算）
    prevCPUTime    int64
    prevDBTime     int64
    prevCommits    int64
    prevRollbacks  int64
    prevExecutes   int64
    prevRedoSize   int64
    prevTimestamp  time.Time
    prevEventData  map[string]eventSnapshot // 等待事件 delta
    prevTPS        float64                  // 用于 TPS 突降检测

    // 缓存（不频繁变化的数据）
    version      string
    instanceName string
    dbRole       string
}
```

### 退出流程

1. 停止刷新 goroutine
2. 最后一帧保留在内容区域（不清除）
3. 恢复正常键盘输入处理
4. 更新 contentRow 到 dbtop 输出之后的位置
5. 重新绘制输入区域

## 不包含（拆分为独立命令）

- OS 监控 (CPU%/MEM%/IO) — 需要 /proc，远程不可用
- 交互式 kill session — 使用 `/kill <sid>`
- 执行计划查看 — 使用 `/explain <sql_id>`
- 锁分析 — 使用 `/lock`
- Memory 面板 — 使用 `/sga`、`/pga`
- P95 响应时间 — 复杂度高，价值有限
- RT/C 模式切换 — MVP 只做 RT 模式
- 应急检测模块 — 通过健康状态系统简化实现
