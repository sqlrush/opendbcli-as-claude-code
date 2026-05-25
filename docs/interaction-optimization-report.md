# 交互优化专项报告

> 审计日期: 2026-03-18
> 审计范围: 35 个 CLI 命令 (19 monitor + 6 query + 8 admin + 2 schema)
> 审计方法: 源码逐文件分析 + 格式化管线追踪 + 渲染层验证
> 测试环境: Oracle 19.3.0.0.0, orcl, root@8.160.176.23
> 批量模式终端宽度: 120 列 (实际 REPL 可能更窄, 截断会更严重)

---

## 真实终端测试发现

> 测试方法: 批量模式执行全部命令 (`TermWidth=120`), stdout+stderr 合并捕获
> 输出文件: `docs/real-terminal-output.txt`

### P0: 严重 Bug

#### P0-1: "No rows" + Summary 拼接无换行 (3 个命令)

批量模式 (`cmd/opendb/main.go:286`) 用 `fmt.Print(table)` 输出表格. 当 `FormatTableOpts` 返回 `"No rows"` 时, 字符串不含尾部换行. Summary 通过 `fmt.Fprintf(os.Stderr, ...)` 输出. 当 stdout+stderr 合并时, 两者直接拼接:

```
/locks 输出:
  No rows0 locks found

/backup 输出:
  No rows0 backup(s) in last 7 day(s)

/jobs 输出:
  No rows0 scheduler jobs
```

**修复**: `cmd/opendb/main.go:286` — 改 `fmt.Print(table)` 为 `fmt.Println(table)`, 或在 `FormatTableOpts` 返回 "No rows" 时追加 `\n`. REPL 模式不受影响 (因为 `writeOutputLine` 自带换行).

#### P0-2: /health 实例运行时间为负数

```
OK   Instance       UP (running -150 minutes)
```

`health.go:194` 用 `h.now().Sub(info.StartupTime)` 计算 uptime. 当客户端时区 (运行 opendb 的机器) 与服务器时区不一致时, `time.Now()` 与 `info.StartupTime` 不在同一时区基准上, 导致负值. 此处服务器是 UTC+8, 而 `StartupTime` 可能被解析为 UTC 或丢失了时区信息.

**修复**: 改用服务器端计算: `SELECT (SYSDATE - startup_time) * 1440 AS uptime_minutes FROM v$instance`, 直接取分钟数, 避免跨时区计算.

#### P0-3: /os 所有指标返回 0

```
OS 指标 (v$osstat):

  CPU:
    CPUs: 0  Cores: 0  Sockets: 0

  Memory:
    Physical: 0 MB  Used: 0 MB (0.0%)  Free: 0 MB
```

`os.go:75-79` 用 `rowInt64(row, 1)` 获取 value 列. `rowInt64` 只处理 `int64/float64/int` 类型. 但 Oracle 的 `v$osstat.value` 列类型是 `NUMBER`, go-ora 驱动可能返回 `string` 或 `*big.Int` 等其他类型. 所有值都落入 `default: return 0`.

**修复**: 在 `rowInt64` 中增加 `string` 类型解析: `case string: n, _ := strconv.ParseInt(v, 10, 64); return n`, 以及 `*big.Int` 等常见类型.

#### P0-4: /health Alert Log 误报: 正常消息被计为"errors"

```
FAIL Alert Log      1120 errors in last 24h
```

但 `/alert 24` 输出显示所有 100 条消息都是 `MESSAGE_LEVEL = 16` (NORMAL 级别). `health.go:36` 的 SQL 用 `message_level <= 16`, 这等于包含了**所有**级别消息 (1=CRITICAL, 2=SEVERE, 8=IMPORTANT, 16=NORMAL). 实际上 NORMAL 级别的 checkpoint/log switch 信息不应视为 "errors".

**修复**: 改为 `message_level <= 8` (只统计 IMPORTANT 及以上), 并将显示文本从 "errors" 改为 "alerts":
```sql
SELECT COUNT(*) as cnt FROM v$diag_alert_ext
WHERE message_level <= 8
AND originating_timestamp > SYSDATE - 1
```

#### P0-5: /planhistory 空结果消息重复输出

```
No AWR plan history found for SQL ID 11b9k2abc1234
No AWR plan history found for SQL ID 11b9k2abc1234
```

`planhistory.go:103` 将消息同时放入 `Rendered` 和 `Summary`, 导致批量模式先打印 `Rendered` (line 276), 再打印 `Summary` (line 291), 同一消息出现两次.

**修复**: 空结果时 `Summary` 改为简短文本 (如 `"no history for <sql_id>"`), 不与 `Rendered` 重复.

---

### P1: 格式化/显示问题

#### P1-1: /sessions — 11 列在 120 宽度下严重截断

```
│ SID  │ SERIAL# │ USERNAME │ STATUS │ OSUSER │ MACHINE  │ PROGRAM   │ SQL_ID    │ EVENT     │ WAIT_C... │ SECOND... │
│ 431  │ 28562   │ OPEND... │ ACTIVE │ oracle │ oracl... │ opendb    │ 11b9k2... │ SQL*Ne... │ Network   │ 0         │
```

- `WAIT_CLASS` 被截断为 `WAIT_C...` (列头都看不全)
- `SECONDS_IN_WAIT` 被截断为 `SECOND...` (列头都看不全)
- `USERNAME`: `OPENDB_TEST` 显示为 `OPEND...`
- `MACHINE`: `oracledb01` 显示为 `oracl...`
- `SQL_ID`: `11b9k2abc1234` 显示为 `11b9k2...` (SQL_ID 是13字符的关键标识, 截断后无法使用)
- 84 行数据中绝大多数是 `USERNAME=NULL` 的后台进程, 噪声极高

**优化**: (a) 移除 `OSUSER` 列 (与 MACHINE 冗余); (b) `MACHINE` 列只取主机名部分; (c) `SQL_ID` 保证完整显示 (min 13 字符); (d) 默认过滤后台进程 (`USERNAME IS NOT NULL`)

#### P1-2: /activesessions — 同 /sessions 的截断问题

```
│ 431  │ 57341   │ OPEND... │ ACTIVE │ oracle │ oracl... │ opendb    │ 8v7u50... │ SQL*Ne... │ Network   │ 0         │
```

与 /sessions 完全相同的 11 列布局, SQL_ID 同样被截断.

#### P1-3: /topsql — 9 列截断极其严重

```
│ SQL_ID      │ ELAPSED_S │ EXEC │ AVG_ELAPS... │ LOGICAL_R │ PHYSICAL_R │ AVG_LOGICAL │ AVG_PHYSICAL │ SQL_TEXT     │
│ b3tz7ugq... │ 0.7       │ 6    │ 0.116        │ 2         │ 0          │ 0           │ 0            │ SELECT CO... │
```

- `SQL_ID` 截断 (`b3tz7ugq...`), 无法用于后续操作 (如 `/explain <sql_id>`)
- `AVG_ELAPSED_MS` 截断为 `AVG_ELAPS...`
- `SQL_TEXT` 只剩 `SELECT CO...` (约 10 字符), 几乎无信息量

**优化**: (a) 精简列: 合并 `LOGICAL_READS/PHYSICAL_READS` 为一列或移到详情; (b) SQL_ID 保证完整 (13 字符); (c) SQL_TEXT 至少 40 字符

#### P1-4: /tempsess — 手工格式化, USER 和 SQL_ID 列全空

```
 SID    USER          TEMP_MB  SQL_ID         EVENT                          STATUS
 ────────────────────────────────────────────────────────────────────────────────
    575                     1                 class slave wait               ACTIVE
   1991                     1                 class slave wait               ACTIVE
```

- USER 列全空 (因为是后台 class slave 进程, `username IS NULL`)
- SQL_ID 列全空
- 手工 `fmt.Fprintf` 无 box-drawing, 与 ResultTable 命令风格不一致
- 分隔线硬编码 80 字符, 表头实际宽度约 86 字符, 不对齐

**优化**: 改为 `ResultTable`; 空 USER 显示进程名或 "N/A"

#### P1-5: /pga — 手工格式化, 两段表风格不统一

```
PGA 内存概览:

 指标                                 值
 ─────────────────────────────────────────────
 aggregate PGA auto target           14667.7 MB
 ...

Top PGA 会话:

 SID      USER               PGA_MB   SQL_ID          PROGRAM
 ──────────────────────────────────────────────────────────────────────
 1705     SYS                   3.0                   oracle@oracledb01 (OFSD)
 431      OPENDB_TEST           2.9   frumv2gy7n85k   opendb
```

- 概览段用 key-value 格式, 会话段用表格, 无统一 box 边框
- 分隔线长度不一致 (45 vs 71)
- 无 box-drawing 字符

**优化**: 两段都改为 ResultTable, 或统一加 box-drawing 边框

#### P1-6: /sga — 中文列头对齐偏移

```
 组件                            大小          最小          最大
 ────────────────────────────────────────────────────────────
 DEFAULT buffer cache    13120 MB    13120 MB    13120 MB
 shared pool              2048 MB     2048 MB     2048 MB
```

- "组件" 列头 (2 中文字 = 4 显示宽度), 后面的数据 "DEFAULT buffer cache" (20 字符) 对齐差异大
- `%-Ns` 按 byte 对齐而非显示宽度, 中文列头的视觉位置不准确
- 分隔线与数据行宽度不一致

**优化**: 改为 ResultTable

#### P1-7: /redo — 日志切换数据为空但表头仍显示

```
最近24小时日志切换 (按小时):

 小时     切换次数
 ────────────────

归档目标:
```

切换频率部分只有表头和分隔线, 无数据行, 也无 "无切换记录" 提示. 空表头悬挂在那里, 令人困惑.

**优化**: 无数据时显示 "最近24小时无日志切换" 而非空表

#### P1-8: /sortusage — 用户名列为空

```
 USER             类型           段数         MB
 ─────────────────────────────────────────────
                  DATA          4        4.0
```

USER 列为空 (后台进程), 显得像数据缺失. 中文列头 "用户/类型/段数" 用 `%-Ns` 对齐不精确.

**优化**: 改为 ResultTable; 空 USER 显示 "(background)" 或 "N/A"

#### P1-9: /standby — 手工格式化, 右侧对齐严重问题

```
=== Database Info ===
  DATABASE_ROLE:       PRIMARY
  DB_UNIQUE_NAME:      orcl
  PROTECTION_MODE:     MAXIMUM PERFORMANCE
  SWITCHOVER_STATUS:   NOT ALLOWED
  DATAGUARD_BROKER:    DISABLED

=== Archive Dest Status ===
DEST_NAME        STATUS           TYPE             SRL              GAP_STATUS
LOG_ARCHIVE_DEST_1  VALID            LOCAL            NO               <nil>
```

- 标题用 `=== ===` 风格, 与 Claude Code 的 `── ──` 不一致
- Archive Dest 表: `DEST_NAME` 列 (`LOG_ARCHIVE_DEST_1` = 20 字符) 超过 `%-15s` 预设的 15 字符, 导致后续列全部右移, 与列头对不齐
- `GAP_STATUS` 列显示 `<nil>` — 应显示 "N/A" 或 "-" 而非 Go 的 nil 表示
- Database Info 部分有 2 空格缩进, Archive Dest 部分无缩进, 风格不统一

**优化**: Archive Dest 改为 ResultTable; `<nil>` 替换为 "-"; 标题改用 `── ──` 风格

#### P1-10: /resize — 文件路径溢出列宽

```
 #    FILE_NAME                                 SIZE_MB  AUTO    MAX_MB
 ──────────────────────────────────────────────────────────────────────
    1 /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_.tmp      367  ON       32768
    2 /oracle/oradata/ORCL/datafile/o1_mf_temp_nsxnvlqm_02.tmp     2048  OFF          0
```

- `FILE_NAME` 列: 路径 55 字符, 但 `%-40s` 只预留 40 字符, 导致后续 SIZE_MB/AUTO/MAX_MB 列全部右移
- 第 1 行的 `367` 与第 2 行的 `2048` 在不同位置, 完全错位
- MAX_MB 的 `0` 和 `32768` 也错位

**优化**: 改为 ResultTable, 自动适配列宽

#### P1-11: /ash — 时间戳显示为 Go 原始格式

```
 98d3pbzwft6vt          1    50%  0             2026-03-18 14:49:28.316 +0000 UTC
```

`Last Seen` 列显示 `2026-03-18 14:49:28.316 +0000 UTC` — 这是 Go 的 `time.Time` 默认 `String()` 格式, 包含毫秒和 `+0000 UTC` 后缀, DBA 不需要这些细节. 且时区显示为 UTC 而非服务器本地时区.

**优化**: 格式化为 `HH:MM:SS` 或 `MM-DD HH:MM:SS`, 使用 `time.Format("15:04:05")`; 或在 SQL 中用 `TO_CHAR(sample_time, 'HH24:MI:SS')` 返回字符串

#### P1-12: /help — 新命令未归入分类

```
 Schema:
  /tableinfo        表结构信息
  /indexadvise      索引建议

  /ash              Show ASH analysis — top SQL and wait events
  /awr              Show AWR snapshot analysis
  /os               Show OS-level metrics (CPU, memory, I/O)
  /planhistory      Show SQL execution plan history from AWR
  /segments         Show top segments by size or growth
```

5 个新命令 (`/ash`, `/awr`, `/os`, `/planhistory`, `/segments`) 被列在最底部, 不属于任何分类, 且描述是英文 (其他命令都是中文).

**优化**: 将 `/ash`, `/awr`, `/planhistory` 归入 "SQL 分析"; `/os`, `/segments` 归入 "内存/存储"; 描述改为中文

#### P1-13: /os proc — PGA 内存显示为原始字节数

```
│ 99  │ oracle@oracledb01        │ 2033021      │ 2739957       │ 2739957     │
```

`PGA_USED_MEM`, `PGA_ALLOC_MEM`, `PGA_MAX_MEM` 显示为原始字节数 (如 `2033021`), 不直观. DBA 习惯看 MB.

**优化**: SQL 中改为 `ROUND(pga_used_mem/1048576, 1) AS pga_used_mb`, 或在列名中标注单位

#### P1-14: /waits — AVG_WAIT_MS 列大量显示 0

```
│ control file sequential read   │ System I/O    │ 53427       │ 31.35           │ 0           │
│ latch free                     │ Other         │ 23700       │ 4.49            │ 0           │
```

30 条中有 24 条的 `AVG_WAIT_MS` 为 `0`. 源码报告已指出 `average_wait/100` 的单位转换可能有误 (Oracle 的 `AVERAGE_WAIT` 单位是 centiseconds). 但实际输出的 "0" 更可能是因为值 < 0.005 被 ROUND 截断了.

**优化**: 增加精度 `ROUND(average_wait * 10, 3)` 或用 `time_waited_micro / NULLIF(total_waits, 0) / 1000`

#### P1-15: /alert — 海量输出无分页, 时间戳列过宽

```
│ 2026-03-18 09:20:11.609 +0800 +08:00 │ 29462957,29486181,29507616,29515240... │ 16 │
```

- 100 条记录全部输出, 无分页 (REPL 模式有 50 行限制 + F 键滚动, 但用户看到一大片刷屏)
- `ORIGINATING_TIMESTAMP` 列显示完整时区偏移 (`+0800 +08:00`), 浪费宽度
- 大量 patch bug 号列表记录混在 alert 日志中, 信息价值低

**优化**: (a) 默认 FETCH FIRST 50 ROWS; (b) SQL 中格式化时间: `TO_CHAR(originating_timestamp, 'MM-DD HH24:MI:SS')`; (c) 考虑过滤掉纯数字行 (patch IDs)

#### P1-16: /resource — "无资源超过 80% 预警线" 前面用了警告符号

```
⚠ 无资源超过 80% 预警线
```

这是一个"好消息" (无警告), 不应使用 `⚠` 符号. 会让用户误以为有问题.

**优化**: 改为 "无资源超过 80% 预警线" (不加 `⚠`), 或改为肯定句 "所有资源使用率正常"

---

### P2: 一致性/体验问题

#### P2-1: Summary 行内容与 Rendered 重复或不一致

| 命令 | Summary | 问题 |
|------|---------|------|
| `/undosess` | `no undo usage` | Rendered 已显示 "当前无活跃Undo事务.", Summary 再显示英文, 冗余且中英混用 |
| `/asm` | `no ASM disk groups` | Rendered 已显示 "此数据库未使用ASM存储.", Summary 英文重复 |
| `/planhistory` | 与 Rendered 完全相同 | 批量模式下打印两遍 (P0-5) |

#### P2-2: 空结果表述不统一

| 命令 | 空结果显示 | 语言 |
|------|-----------|------|
| `/locks` | `No rows` (自动) | 英文 |
| `/backup` | `No rows` (自动) | 英文 |
| `/jobs` | `No rows` (自动) | 英文 |
| `/undosess` | `当前无活跃Undo事务.` | 中文 |
| `/asm` | `此数据库未使用ASM存储.` | 中文 |
| `/blocktree` | `当前无阻塞链.` | 中文 |

**优化**: ResultTable 命令空结果时, 在技能层返回 ResultText + 中文提示, 而非让 `FormatTable` 返回通用 "No rows"

#### P2-3: 手工格式化命令 vs ResultTable 命令视觉差异大

手工格式化 (无 box-drawing):
```
 SID    USER          TEMP_MB  SQL_ID         EVENT                          STATUS
 ────────────────────────────────────────────────────────────────────────────────
    575                     1                 class slave wait               ACTIVE
```

ResultTable (有 box-drawing):
```
┌─────────────┬─────────────────────────────┬─────────────────┬─────────┐
│ OWNER       │ SEGMENT_NAME                │ SEGMENT_TYPE    │ SIZE_MB │
├─────────────┼─────────────────────────────┼─────────────────┼─────────┤
│ SYSTEM      │ BENCH_TEST_50COL            │ TABLE           │ 1600    │
```

两种风格差异明显, 用户体验不连贯.

**优化**: 逐步将以下 12 个手工格式化命令迁移到 ResultTable:
`/tempsess`, `/undosess`, `/pga`, `/sga`, `/redo`, `/fra`, `/asm`, `/sortusage`, `/resource`, `/standby`, `/resize`, `/awr`

---

### 逐命令优化计划

| # | 命令 | 当前格式 | 发现的问题 | 具体修改 |
|---|------|---------|-----------|---------|
| 1 | `/sessions` | ResultTable | 11列截断严重; 后台进程噪声 | 移除 OSUSER; MACHINE 取 host; SQL_ID min 13 宽; 默认过滤 `USERNAME IS NOT NULL` |
| 2 | `/activesessions` | ResultTable | 同上 | 同 /sessions 的列精简 |
| 3 | `/waits` | ResultTable | AVG_WAIT_MS 大量为 0 | 改用 `time_waited_micro/NULLIF(total_waits,0)/1000`; 增加 PCT 列 |
| 4 | `/locks` | ResultTable | "No rows" 无上下文 | 空结果改为 ResultText: "当前无锁等待" |
| 5 | `/latches` | ResultTable | 无严重问题 | 可选: 数字加千分位 |
| 6 | `/mutexes` | ResultTable | 无严重问题 | 无需修改 |
| 7 | `/health` | ResultText | P0-2 负 uptime; P0-4 Alert 误报; header 下划线用 `len()` 不用 `runewidth` | 改服务器端计算 uptime; alert SQL 改 `<= 8`; header 用 `runewidth.StringWidth` |
| 8 | `/tempsess` | 手工 Fprintf | P1-4 USER/SQL_ID 空; 无 box-drawing | 改为 ResultTable; 空 USER 显示 "N/A" |
| 9 | `/undosess` | 手工 Fprintf | 空结果中英混用 | 改为 ResultTable; Summary 改中文 |
| 10 | `/pga` | 手工 Fprintf | P1-5 两段格式不统一 | 概览保留 key-value; 会话段改为 ResultTable |
| 11 | `/sga` | 手工 Fprintf | P1-6 中文列头对齐偏移 | 改为 ResultTable |
| 12 | `/redo` | 手工 Fprintf | P1-7 空切换数据只有表头 | 空数据时显示 "无切换记录"; 三段分别改为 ResultTable |
| 13 | `/fra` | 手工 Fprintf | 无严重问题 | 可选: 改为 ResultTable |
| 14 | `/asm` | 手工 Fprintf | Summary 中英混用 | Summary 改中文 |
| 15 | `/sortusage` | 手工 Fprintf | P1-8 USER 列空 | 改为 ResultTable; 空 USER 显示 "N/A" |
| 16 | `/resource` | 手工 Fprintf | P1-16 `⚠` 误导 | 移除好消息前的 `⚠`; 改为 ResultTable |
| 17 | `/blocktree` | 手工 (树形) | 无严重问题, 树形渲染效果好 | 保留; 可选加 `/kill` 提示 |
| 18 | `/segments` | ResultTable | 无严重问题 | 无需修改 |
| 19 | `/os` | ResultText/Table | P0-3 所有值为 0; P1-13 PGA 原始字节 | 修复 `rowInt64` 类型转换; PGA 改 MB 单位 |
| 20 | `/slowsql` | ResultTable | SQL_TEXT 有效但偏短 | 无严重问题; 可选加 avg_elapsed |
| 21 | `/topsql` | ResultTable | P1-3 截断极其严重 | 减列: 合并 LOGICAL/PHYSICAL; SQL_ID 保证 13 宽; SQL_TEXT 至少 40 宽 |
| 22 | `/awr` | 手工 Fprintf | 无严重实际问题 (列表模式输出整齐) | 可选: 子表改 ResultTable |
| 23 | `/ash` | 手工 Fprintf | P1-11 时间戳 Go 原始格式 | 时间格式化为 `HH:MM:SS`; 子表改 ResultTable |
| 24 | `/planhistory` | ResultTable | P0-5 重复输出 | Summary 不重复 Rendered 内容 |
| 25 | `/space` | ResultTable | 无严重问题 | 可选: 加使用率高亮 |
| 26 | `/params` | ResultTable | 无严重问题 | 无需修改 |
| 27 | `/alert` | ResultTable | P1-15 海量输出; 时间戳过宽 | 默认 FETCH 50; 时间格式化; 过滤噪声 |
| 28 | `/backup` | ResultTable | "No rows" 无上下文 | 空结果改为 ResultText: "最近 7 天无备份记录" |
| 29 | `/standby` | 手工 Fprintf | P1-9 列错位; `<nil>` | Archive Dest 改 ResultTable; nil 替为 "-"; 标题改 `──` 风格 |
| 30 | `/resize` | 手工 Fprintf | P1-10 路径溢出 | list 模式改为 ResultTable |
| 31 | `/kill` | ResultText | 无实际输出 (未测试) | 无需修改 |
| 32 | `/alter` | ResultText | 无实际输出 (未测试) | 无需修改 |
| 33 | `/jobs` | ResultTable | "No rows" 无上下文 | 空结果改为 ResultText: "无调度作业" |
| 34 | `/help` | ResultText | P1-12 新命令未归类, 英文描述 | 将 5 个新命令归入对应分类; 描述改中文 |
| 35 | `/tableinfo` | ResultText | 未测试 | 已在源码报告中分析 |
| 36 | `/indexadvise` | ResultText | 未测试 | 已在源码报告中分析 |

### 修复优先级排序

**第一批 (P0, 立即修复)**:
1. `cmd/opendb/main.go:286` — `fmt.Print(table)` 改 `fmt.Println(table)` (修复 P0-1)
2. `monitor/health.go` — uptime 改服务器端计算; alert SQL 改 `<= 8` (修复 P0-2, P0-4)
3. `monitor/os.go` — `rowInt64` 增加 string 类型解析 (修复 P0-3)
4. `query/planhistory.go` — Summary 不重复 Rendered (修复 P0-5)

**第二批 (P1, 本周)**:
1. `/sessions` `/activesessions` — 精简列, 默认过滤后台进程
2. `/topsql` — 减列, 保证 SQL_ID 和 SQL_TEXT 可读
3. `/locks` `/backup` `/jobs` — 空结果改中文提示
4. `/standby` — nil 替为 "-", Archive Dest 改 ResultTable
5. `/resize` — list 模式改 ResultTable
6. `/ash` — 时间戳格式化
7. `/help` — 新命令归类, 描述改中文

**第三批 (P2, 下周)**:
1. 12 个手工 Fprintf 命令逐步迁移到 ResultTable
2. Summary 行中英混用统一
3. 空结果提示统一中文

---

## 总体发现

### 1. 两套格式化体系并存, 风格不统一 (P0)

项目存在两种输出路径:

| 路径 | 使用方式 | 美观度 | 命令数 |
|------|----------|--------|--------|
| **ResultTable** | 返回 `*db.QueryResult`, REPL 自动调用 `format.FormatTableOpts` 生成 box-drawing 表格 | 高 | 14 |
| **ResultText + 手工 fmt.Fprintf** | 技能自行拼接字符串, 用 `%-Ns` 硬编码列宽 | 低 | 21 |

**核心矛盾**: `FormatTable` 已经实现了自动列宽计算、box-drawing 字符、终端宽度适配、截断省略 -- 但超过一半的命令绕过它, 用 `fmt.Fprintf` 手工对齐. 结果:

- 中文字符宽度计算不正确 (CJK 占 2 列, `%-Ns` 只按 byte 数对齐)
- 列宽硬编码, 遇到长数据会错位
- 无 box-drawing 边框, 视觉上比 `ResultTable` 命令低一档
- 无终端宽度适配, 窄终端下溢出

### 2. `formatNumber` / `asString` 等工具函数重复定义 (P1)

| 函数 | 重复位置 |
|------|----------|
| `asString(v any) string` | `admin/alter.go`, `query/ash.go` (完全相同); `monitor/segments.go` (签名不同: `string` 参数) |
| `formatNumber(n int) string` | `monitor/tempsess.go` |
| `undoFormatNumber(n int) string` | `monitor/undosess.go` (与 `formatNumber` 逻辑完全一致) |
| `awrStr(v any) string` | `query/awr.go` (与 `asString` 逻辑一致) |
| `phAsString(v any) string` | `query/planhistory.go` (与 `asString` 逻辑一致) |
| `fmtVal(row []any, idx int) string` | `schema/tableinfo.go` (与 `monitor/rowval.go:rowStr` 逻辑一致) |

建议: 统一到一个 `internal/util/convert.go` 或 `internal/skill/builtin/shared/` 包.

### 3. Summary 行有但渲染不一致 (P1)

- `ResultTable` 路径: REPL 自动以 `dimStyle` 渲染 Summary -- 好
- `ResultText` 路径: Summary 存在但 REPL **不额外渲染它**, 需要命令自行在 Rendered 中包含汇总行 -- 部分命令缺失

### 4. 空结果处理参差不齐 (P2)

- 优: `tempsess`, `undosess`, `fra`, `asm`, `blocktree` 对空结果有友好中文提示
- 差: `sessions`, `waits`, `locks`, `latches`, `mutexes`, `space`, `params`, `alert`, `backup`, `jobs` 返回 ResultTable, 空数据时 `FormatTable` 仅显示 "No rows" -- 无上下文提示

### 5. 中英文混用 (P2)

- 命令标题: `tempsess` 用中文 "临时空间占用会话", `health` 用英文 "Health Report"
- 空结果: `fra` 用中文 "未配置 Flash Recovery Area", `planhistory` 用英文 "No AWR plan history found"
- 提示: `pga` 用中文 "提示: /alter pga_aggregate_target 2G", `indexadvise` 全英文
- 建议: 统一为中文 (与 `/help` 中的 `helpDescCN` 一致)

### 6. SQL 安全性 (P2)

- `/topsql` 使用 `fmt.Sprintf` 拼接 ORDER BY 子句 (`sortInfo.col`) -- 虽然 `sortInfo` 来自白名单 map, 安全无问题, 但代码审计时容易引起误解. 建议加注释说明.
- `/alter` 的 `ALTER SYSTEM SET %s = %s SCOPE=%s` 直接拼接 paramValue -- 用户可注入任意内容. 由于 `SecurityLevel` 已为 `LevelAdmin`, 风险可控, 但建议添加基本值验证 (至少过滤分号).

---

## 逐命令分析

---

### /sessions

**文件**: `internal/skill/builtin/monitor/sessions.go`

- **功能**: OK. SQL 查询 `v$session` 11 列, 覆盖 SID/Serial/Username/Status/Event 等核心字段. ORDER BY status, username 合理.
- **输出格式**: 使用 **ResultTable**, 自动 box-drawing, 无问题.
- **交互问题**:
  - 11 列在 120 列终端下会被截断 (machine/program 列通常较长), `fitWidths` 会压缩但可能难以辨认
  - 无过滤参数 (如 `/sessions active`, `/sessions SYS`)
  - `seconds_in_wait` 为 0 的行大量存在, 噪声高
- **优化方案**:
  1. 减少默认列数: 移除 `osuser` (与 `machine` 冗余), 将 `machine` 截取 host 部分
  2. 支持 `/sessions [filter]` 参数 (按 username/status 过滤)
  3. 空结果时显示 "当前无数据库会话"

---

### /activesessions

**文件**: `internal/skill/builtin/monitor/activesessions.go`

- **功能**: OK. 筛选 `status='ACTIVE' AND username IS NOT NULL`, ORDER BY seconds_in_wait DESC, 合理.
- **输出格式**: 使用 **ResultTable**, 自动 box-drawing, 无问题.
- **交互问题**:
  - 与 `/sessions` 完全相同的 11 列, 同样宽度问题
  - 无分页上限 (如果有 500 个活跃会话会全部输出)
  - 缺少 `sql_exec_start` 列 (DBA 常需知道 SQL 已执行多久)
- **优化方案**:
  1. 添加 `FETCH FIRST 50 ROWS ONLY`
  2. 增加 `ROUND((SYSDATE - sql_exec_start) * 86400) AS elapsed_sec` 列
  3. 精简: 移除 `osuser`

---

### /waits

**文件**: `internal/skill/builtin/monitor/waits.go`

- **功能**: OK. 排除 Idle 类, 按 time_waited_micro DESC 排序, FETCH FIRST 30 合理.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - `average_wait/100` 转换为 ms -- Oracle 文档中 `AVERAGE_WAIT` 单位是 centiseconds (1/100s), 除以 100 得到的是秒, 不是毫秒. **计算错误**: 应为 `ROUND(average_wait * 10, 2)` 得到 ms, 或 `ROUND(average_wait / 100, 4)` 得到秒
  - 列别名 `avg_wait_ms` 与实际单位不符
  - 无百分比列 (各事件占总等待时间的比例)
- **优化方案**:
  1. 修正: `ROUND(average_wait * 10, 2) as avg_wait_ms` 或改用 `time_waited_micro / NULLIF(total_waits, 0) / 1000`
  2. 增加 `ROUND(time_waited_micro * 100 / SUM(time_waited_micro) OVER(), 1) AS pct` 百分比列
  3. 增加参数: `/waits [top_n]` 控制行数

---

### /locks

**文件**: `internal/skill/builtin/monitor/locks.go`

- **功能**: 有问题.
  - `LEFT JOIN dba_objects o ON l.id1 = o.object_id` -- 对 TX 锁, `id1` 不是 `object_id` (TX 锁的 id1 是 rollback segment * 65536 + slot). 只有 TM 锁的 `id1` 才是 `object_id`.
  - `lmode` 和 `request` 是数字代码 (0-6), DBA 需要翻译成 None/Row-S/Row-X/Share/S/Row-X/SSX/Exclusive
  - 缺少 `seconds_in_wait` 列
- **输出格式**: 使用 **ResultTable**, 无问题.
- **优化方案**:
  1. 修正 JOIN: `LEFT JOIN dba_objects o ON l.id1 = o.object_id AND l.type = 'TM'`
  2. 增加 lock mode 翻译: `DECODE(l.lmode, 0,'None', 1,'Null', 2,'Row-S', 3,'Row-X', 4,'Share', 5,'SSX', 6,'Exclusive') AS lock_mode`
  3. 增加 `s.seconds_in_wait`
  4. 增加 `s.blocking_session` 列, 便于快速定位阻塞者

---

### /latches

**文件**: `internal/skill/builtin/monitor/latches.go`

- **功能**: OK. 筛选 `misses > 0`, 按 sleeps DESC, FETCH FIRST 30. 包含 miss_ratio 计算.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - 数字列 (gets, misses, sleeps) 可能很大, 无千分位分隔
  - miss_ratio 无百分号后缀标识
- **优化方案**: 无重大问题. 可选: 增加 `immediate_gets` 列.

---

### /mutexes

**文件**: `internal/skill/builtin/monitor/mutexes.go`

- **功能**: OK. 查询 `v$mutex_sleep`, 按 sleeps DESC.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**: 列数少 (4 列), 信息量低, 缺少 `gets` 和 `mutex_identifier` 有助于定位.
- **优化方案**: 可选: 增加 `gets` 列.

---

### /health

**文件**: `internal/skill/builtin/monitor/health.go`

- **功能**: 整体 OK, 8 项检查覆盖面广. 但有几个问题:
  - `checkWaitEvents`: 查询了 top 5 等待事件但 **永远返回 "No anomalies"**, 没有实际判断逻辑 (注释说 "always OK for MVP"). 应至少检查是否有 "enqueue" 或 "log file sync" 等待时间异常.
  - `checkSlowSQL`: `elapsed_time/1000 > 5000` -- `elapsed_time` 单位是微秒, `/1000` 得到毫秒, `> 5000` 即 > 5 秒. 但这是**累计**执行时间, 不是单次执行时间. 一个执行了 100 次、每次 51ms 的 SQL 也会被标记. 建议改为 `elapsed_time/NULLIF(executions,0)/1000 > 5000`.
  - `alertSQL`: `message_level <= 16` -- Oracle alert levels: 1=CRITICAL, 2=SEVERE, 8=IMPORTANT, 16=NORMAL. `<= 16` 基本包含了所有消息, 建议 `<= 8` 只看 IMPORTANT 及以上.
- **输出格式**: 使用 **ResultText**, 手工格式化. 格式合理 (`%-4s %-14s %s`), 但:
  - header 下划线用 `\u2501` (粗横线), `len(header)` 按 byte 计算, 如果 InstanceName 含中文会长度错误
  - 无颜色标记 (OK 绿色, WARN 黄色, FAIL 红色)
- **优化方案**:
  1. 修复 `checkWaitEvents` 增加真实判断逻辑
  2. `checkSlowSQL` 改用平均执行时间
  3. header 下划线用 `runewidth.StringWidth` 计算
  4. 在 Rendered 输出中加 ANSI 颜色 (OK=green, WARN=yellow, FAIL=red)

---

### /tempsess

**文件**: `internal/skill/builtin/monitor/tempsess.go`

- **功能**: OK. 查询 `v$sort_usage JOIN v$session`, 计算 temp_mb.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - `%-30s` 给 EVENT 列 -- 但 Oracle 事件名经常超过 30 字符 (如 "direct path read temp"), 会造成后续列错位
  - 中文标题 "临时空间占用会话" 但列名全大写英文, 风格不一致
  - 分隔线宽度 80 硬编码, 与表头实际宽度不匹配
  - 无 box-drawing 字符
- **优化方案**:
  1. 改为 **ResultTable**, 利用 FormatTable 自动对齐
  2. 或至少用 `runewidth` 替代 `%-Ns`
  3. 合计行 (totalMB) 应在表底而非表头
  4. 提示行 (`/kill SID`) 很好, 保留

---

### /undosess

**文件**: `internal/skill/builtin/monitor/undosess.go`

- **功能**: OK. 查询 `v$transaction JOIN v$session`.
- **输出格式**: 与 `/tempsess` 完全相同的问题 (手工 fmt.Fprintf, 硬编码列宽).
- **额外问题**:
  - `undoFormatNumber` 是 `formatNumber` 的完全复制 (仅函数名不同). 应复用.
- **优化方案**: 同 `/tempsess`, 改为 ResultTable.

---

### /pga

**文件**: `internal/skill/builtin/monitor/pga.go`

- **功能**: OK. 两段式: PGA 概览 + Top 10 会话.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - `nameWidth` 用 `len(item.Name)` 而非 `runewidth.StringWidth` -- PGA stat 名称是纯 ASCII 所以暂无问题, 但代码不规范
  - Top PGA 会话的 `%-16s` 对 USER 列, `%8.1f` 对 PGA_MB -- 如果用户名超过 16 字符会错位
  - 分隔线宽度 70 硬编码
  - 两个表用不同格式 (key-value 对 vs 列表), 无统一 box 边框
- **优化方案**:
  1. 概览部分保留 key-value 格式但用 box 包裹
  2. 会话部分改为 ResultTable
  3. 或统一用 ResultText 但加 box-drawing 边框

---

### /sga

**文件**: `internal/skill/builtin/monitor/sga.go`

- **功能**: OK. 查询 `v$sga_dynamic_components` + `v$sga` 总量.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题同 `/pga`:
  - `nameWidth` 用 `len(c.Component)` 而非 `runewidth.StringWidth`
  - 列头 "组件/大小/最小/最大" 是中文, 但中文字宽不等于 byte 数, `%-Ns` 对齐会错
  - "组件" 2 个中文字 = 4 显示宽度, 但 `%-Ns` 按 byte 对齐 = 6 (UTF-8 每个中文 3 byte), 会多留空白
- **优化方案**:
  1. 改为 ResultTable 或使用 `runewidth` 计算列宽
  2. 在 Total 行加粗或特殊标记

---

### /redo

**文件**: `internal/skill/builtin/monitor/redo.go`

- **功能**: OK. 三段式: Log Groups + Switch Frequency + Archive Dests. 结构优秀.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - Log Groups 表的 `%5s %6d %6s` 格式, GROUP 和 MEMBERS 是字符串但 SIZE_MB 是数字, 对齐方向混乱
  - Switch Frequency 的列头 "小时/切换次数" 是中文, `%-6s` 会对不齐
  - 分隔线宽度 50/16/50 各不相同, 视觉不统一
  - 缺少日志切换频率的警告阈值 (如每小时 > 10 次应告警)
- **优化方案**:
  1. 三个子表各自用 ResultTable 渲染会更整齐
  2. 或统一用手工格式但加 box-drawing
  3. 增加切换频率高时的警告提示

---

### /fra

**文件**: `internal/skill/builtin/monitor/fra.go`

- **功能**: OK. 两段式: 概览 + 按类型分布.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 与其他命令类似问题.
- **交互亮点**: 空结果友好 ("未配置 Flash Recovery Area"), 有可回收空间展示, 有 `/alter` 提示, 有百分比.
- **优化方案**:
  1. 增加 FRA 使用率的 bar 图 (如 `█████░░░░░ 52.3%`)
  2. 超 80% 使用率时加警告标记

---

### /asm

**文件**: `internal/skill/builtin/monitor/asm.go`

- **功能**: OK. 查询 `v$asm_diskgroup`. 非 ASM 数据库优雅降级 (返回友好提示而非报错).
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题同上.
- **交互亮点**: 有 80% 使用率警告, 有 warning emoji. 但 emoji (`⚠`) 在纯终端下可能显示异常.
- **优化方案**: 改用 ResultTable, 高使用率行可在 Summary 中提及.

---

### /sortusage

**文件**: `internal/skill/builtin/monitor/sortusage.go`

- **功能**: OK. 两段式: Sort Segments + Top Consumers.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题同上.
- **交互亮点**: 空数据有 "(无排序段数据)" 提示, 有跳转提示 (`/tempsess`).
- **交互问题**: 列头 "表空间/总块数/已用块/空闲块/使用率" 全中文, `%-12s` 对齐完全错误 (中文 2 字 = 4 显示宽, 12 byte = 4 中文字, 但 `%-12s` 按 byte 不按显示宽度).
- **优化方案**: 改为 ResultTable 或修复中文列宽计算.

---

### /resource

**文件**: `internal/skill/builtin/monitor/resource.go`

- **功能**: OK. 查询 `v$resource_limit`, 筛选非 UNLIMITED 且有使用记录的资源. 有 80% 预警.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 中文列头 "资源名/当前/最大/上限/使用率" 对齐有同样问题.
- **交互亮点**: 警告提示优秀.
- **交互问题**:
  - `usagePct` 基于 `max` (历史最大值) 而非 `current` -- 这意味着即使当前使用率很低, 如果历史曾达到高峰也会告警. 建议显示两者.
  - "无资源超过 80% 预警线" 前面有 `⚠` 符号, 但这是"无警告"的好消息, 不应用警告符号

---

### /blocktree

**文件**: `internal/skill/builtin/monitor/blocktree.go`

- **功能**: 优秀. 构建层级阻塞树, 用 box-drawing 字符 (`├─`, `└─`) 渲染树形结构. 是全项目最优的定制化输出之一.
- **输出格式**: 使用 **ResultText**, 自定义树渲染, 效果好.
- **交互问题**:
  - 使用 emoji `🔒` 作为根节点标记 -- 在某些终端下可能宽度不一致 (有的终端把 emoji 当 2 宽, 有的当 1 宽)
  - Root blocker 显示 `[ACTIVE]` 或 `[INACTIVE]`, 但 INACTIVE 的根阻塞者意味着会话已经不活跃但锁未释放, 这种情况应特别高亮
  - 缺少 `/kill SID` 提示
- **优化方案**:
  1. 将 `🔒` 改为 `[B]` 或 `■` 避免 emoji 宽度问题
  2. INACTIVE 根阻塞者加 `(建议: /kill SID)` 提示
  3. 添加 `serial#` 到显示中, 方便 kill

---

### /segments (NEW)

**文件**: `internal/skill/builtin/monitor/segments.go`

- **功能**: OK. 三种模式: Top segments / 按 owner / 按 growth (AWR). 设计优秀.
- **输出格式**: 使用 **ResultTable**, 自动 box-drawing, 无问题.
- **交互问题**:
  - Growth 模式查询 `dba_hist_seg_stat` 需要 AWR 许可证, 普通测试环境可能报 ORA-00942
  - `asString` 函数签名与其他包不同 (参数是 `string` 不是 `any`), 容易混淆
- **优化方案**:
  1. Growth 模式失败时给出友好提示 (如 "需要 Diagnostics Pack 许可证")
  2. 增加 `ROUND(bytes/1073741824, 2) AS size_gb` 对大表更友好

---

### /os (NEW)

**文件**: `internal/skill/builtin/monitor/os.go`

- **功能**: OK. 两种模式: OS stats (CPU/Memory/Swap) / Processes.
- **输出格式**: OS stats 使用 **ResultText** 自定义格式 (分段展示 CPU/Memory), 效果好; Processes 使用 **ResultTable**.
- **交互问题**:
  - `LOAD` 值除以 100 -- Oracle `v$osstat` 的 LOAD 值已经是浮点, 不需要除以 100. **可能计算错误**, 需要在真实环境验证.
  - `background IS NULL` 过滤可能过于严格, 某些用户进程也可能有 background 标记
- **优化方案**:
  1. 验证 LOAD 计算逻辑
  2. 增加 percentage bar 图形展示 CPU 和内存使用率

---

### /slowsql

**文件**: `internal/skill/builtin/query/slowsql.go`

- **功能**: 有问题.
  - `elapsed_time/1000 > :1` -- elapsed_time 单位是微秒, 除以 1000 得到毫秒. 但 threshold 默认 1000(ms), 所以 `elapsed_time/1000 > 1000` 即 `elapsed_time > 1,000,000 微秒 = 1 秒`. 逻辑正确.
  - **但**: `elapsed_time` 是**累计**执行时间, 不是单次. 一个执行了 10000 次、每次 0.1ms 的 SQL 也会超过 1000ms. 应除以 `executions`.
  - 列 `ROUND(elapsed_time/1000, 2) as elapsed_ms` -- 这是总耗时(ms), 列名暗示是"某次耗时". 容易误导.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **优化方案**:
  1. 改为平均执行时间比较: `elapsed_time / NULLIF(executions, 0) / 1000 > :1`
  2. 同时展示总耗时和平均耗时
  3. 增加 `parsing_schema_name` 列

---

### /explain

**文件**: `internal/skill/builtin/query/explain.go`

- **功能**: OK. 支持 SQL 文本和 SQL ID 两种模式.
- **输出格式**: 使用 **ResultText**, 直接输出 DBMS_XPLAN 原始格式, 合理 (执行计划本身已是格式化文本).
- **交互问题**:
  - `EXPLAIN PLAN FOR` 会在 plan_table 中留下记录, 多次执行会堆积. 无清理机制.
  - SQL 文本模式检测 (`looksLikeSQL`) 要求包含空格 -- 短语句如 `SELECT 1 FROM DUAL` 没问题, 但用户输入 SQL ID 时如果误含空格会被当作 SQL.
- **优化方案**: 无重大问题. 可选: 在执行计划后自动执行 `DELETE FROM plan_table WHERE statement_id IS NULL`.

---

### /topsql

**文件**: `internal/skill/builtin/query/topsql.go`

- **功能**: OK. 多排序维度 (el/ae/lr/pr/al/ap/ex), 支持时间范围.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - `SUBSTR(sql_text, 1, 60)` -- 60 字符太短, 很多 SQL 只能看到开头
  - 排序键用缩写 (el/ae/lr/pr/al/ap/ex), 新用户不容易记忆
  - `WHERE last_active_time > SYSDATE - :1/1440` -- 使用 bind variable 进行日期算术, 某些 Oracle 版本可能有隐式类型转换问题
- **优化方案**:
  1. `sql_text` 截取长度增加到 80 或 100
  2. Help 输出时展示排序键含义

---

### /awr (NEW)

**文件**: `internal/skill/builtin/query/awr.go`

- **功能**: 三种模式: 列出快照 / 单快照分析 / 双快照对比. 设计优秀.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - `awrSnapshotListSQL` 中 `dur_sec` 计算只用了 SECOND + MINUTE, 忽略了 HOUR/DAY 部分. 如果快照间隔超过 1 小时, 时长计算错误.
  - 列表模式分隔线用 `-` 而非 `─` (与其他命令不一致)
  - Top SQL 表和 Top Wait Events 表用 `==` 分隔符, 与 Claude Code 风格不符
- **优化方案**:
  1. 修复 `dur_sec` 计算: 增加 `EXTRACT(HOUR FROM ...) * 3600 + EXTRACT(DAY FROM ...) * 86400`
  2. 分隔线统一用 `─`
  3. 标题用 `── Top SQL (by elapsed time) ──` 而非 `== Top SQL ==`
  4. 子表改为 ResultTable

---

### /ash (NEW)

**文件**: `internal/skill/builtin/query/ash.go`

- **功能**: OK. 两种模式: 概览 (Top SQL + Top Waits) / SQL Profile. 设计优秀.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - 与 `/awr` 相同的格式化问题 (硬编码列宽, 中文对齐)
  - `asString` 函数与 `admin/alter.go` 重复定义 (同包内已有 `monitor/segments.go:asString` 但签名不同)
- **交互亮点**: 有 section divider (`── Top SQL ──`), 比 `/awr` 的 `== ==` 好.
- **优化方案**:
  1. 子表改为 ResultTable
  2. 增加 percentage bar: `Pct` 列用 `█░` 可视化

---

### /planhistory (NEW)

**文件**: `internal/skill/builtin/query/planhistory.go`

- **功能**: OK. 查询 AWR 中 SQL 的执行计划历史, 检测 plan regression.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互亮点**: 自动检测 distinct plan hash 数量并在 Summary 中报告, 便于快速判断是否有计划回退.
- **交互问题**:
  - 缺少 plan regression 的显式告警 (如 distinct plans > 1 时提示)
  - 无子命令查看具体 plan (如 `/planhistory <sql_id> plan <plan_hash>`)
- **优化方案**:
  1. distinct plans > 1 时在 Summary 中加 `⚠ 检测到计划变更` 提示
  2. 增加 `/planhistory <sql_id> <plan_hash>` 子命令, 调用 `DBMS_XPLAN.DISPLAY_AWR` 展示具体 plan

---

### /space

**文件**: `internal/skill/builtin/admin/space.go`

- **功能**: 有问题.
  - `used_space * 8192 / 1024/1024` -- 硬编码 block size 为 8192 (8KB). 但 Oracle 支持 2K/4K/8K/16K/32K block sizes. 应该查询 `v$parameter` 获取 `db_block_size`.
  - 实际上 `dba_tablespace_usage_metrics` 的 `USED_SPACE` 和 `TABLESPACE_SIZE` 已经是 block 数, 而且该视图已有 `USED_PERCENT` 列. 更好的做法是使用 `dba_tablespace_usage_metrics` 的原始列.
  - 更简洁: Oracle 12c+ 的 `dba_tablespace_usage_metrics` 已经提供了 `USED_PERCENT` 直接可用.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - 缺少 MB/GB 自适应显示
  - 缺少使用率高的警告标记
  - 缺少 `/resize` 提示
- **优化方案**:
  1. 移除硬编码 8192, 改用 `dba_data_files` 方案或在 SQL 中 JOIN `v$parameter`
  2. 增加 autoextend 信息
  3. 增加 Summary 中对使用率 > 85% 的表空间列出警告

---

### /params

**文件**: `internal/skill/builtin/admin/params.go`

- **功能**: OK. LIKE 模式匹配, 支持空参数 (列出全部).
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - `description` 列通常很长, 在窄终端下会被严重截断
  - 无参数列出全部时, 行数可能 > 400, 需要滚动浏览器
- **优化方案**:
  1. 默认不显示 `description` 列, 或将其缩短
  2. 增加 `isdefault` 列, 过滤 `/params modified` 只显示已修改的参数

---

### /alert

**文件**: `internal/skill/builtin/admin/alert.go`

- **功能**: OK. 支持自定义小时数.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**:
  - `message_text` 列可能很长 (Oracle alert 消息), 窄终端被截断
  - `message_level` 是数字, 不直观. 应翻译为 CRITICAL/SEVERE/IMPORTANT/NORMAL
  - 无按 level 过滤参数
- **优化方案**:
  1. 增加 level 翻译: `DECODE(message_level, 1,'CRITICAL', 2,'SEVERE', 8,'IMPORTANT', 16,'NORMAL') AS level_name`
  2. 支持 `/alert [hours] [level]`

---

### /backup

**文件**: `internal/skill/builtin/admin/backup.go`

- **功能**: OK. 查询 `v$rman_backup_job_details`.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**: `output_bytes_display` 和 `time_taken_display` 是 Oracle 自动格式化的字符串, 显示友好.
- **优化方案**: 可选: 增加 FAILED 状态的高亮标记.

---

### /standby

**文件**: `internal/skill/builtin/admin/standby.go`

- **功能**: OK. 查询 `v$database` 和 `v$archive_dest_status`.
- **输出格式**: 使用 **ResultText + 手工 fmt.Fprintf**, 问题:
  - 使用 `=== Database Info ===` 和 `=== Archive Dest Status ===` 分隔 -- 与 Claude Code 风格不符
  - Archive Dest 表用 `%-15s` 硬编码列宽
  - 无 box-drawing 字符
- **优化方案**:
  1. 标题改用 `── Database Info ──` 风格
  2. Archive Dest 表改为 ResultTable 或用 box-drawing
  3. 增加 lag 检测 (如果是 standby 角色, 查询 `v$dataguard_stats` 显示 apply lag)

---

### /resize

**文件**: `internal/skill/builtin/admin/resize.go`

- **功能**: 优秀. 四种子操作 (list/resize/add/autoextend), 有自动路径生成, 有 TEMP 表空间检测.
- **输出格式**: List 模式使用 **ResultText + 手工 fmt.Fprintf**, 其他模式返回成功消息.
- **交互亮点**: 底部有操作指南, 用户体验好.
- **交互问题**: list 模式的 `%-40s` 对 FILE_NAME -- 有些路径超过 40 字符, 会错位.
- **优化方案**: list 模式改为 ResultTable.

---

### /kill

**文件**: `internal/skill/builtin/admin/kill.go`

- **功能**: OK. 自动查询 serial#, 支持 immediate 模式.
- **输出格式**: 使用 **ResultText**, 简单消息, 无问题.
- **交互问题**:
  - 成功消息用 `Data` 而非 `Rendered` -- REPL 的 ResultText 路径优先使用 Rendered, 如果为空则 fallback 到 Data. 此处 `Rendered` 未设置, 依赖 fallback. 应设置 `Rendered`.
  - 无确认机制 (kill 是危险操作)
- **优化方案**:
  1. 设置 `Rendered` 字段
  2. 在 kill 前显示会话信息 (用户名/SQL_ID/程序), 让操作者确认

---

### /alter

**文件**: `internal/skill/builtin/admin/alter.go`

- **功能**: 优秀. 查看模式 (无值) 和修改模式 (有值) 二合一. 自动判断 SCOPE, 显示 SPFILE 重启提示.
- **输出格式**: 使用 **ResultText**, 格式简洁明了.
- **交互问题**: paramValue 直接拼入 SQL -- 虽然 LevelAdmin 权限可控, 但理论上可注入. 应做基本过滤.
- **优化方案**:
  1. 对 paramValue 过滤分号和注释符
  2. 修改成功后增加一行显示修改后的当前值 (重新查询验证)

---

### /jobs

**文件**: `internal/skill/builtin/admin/jobs.go`

- **功能**: OK. 排除系统 owner, 支持 `failed` 子命令.
- **输出格式**: 使用 **ResultTable**, 无问题.
- **交互问题**: `failure_count > 0` 的行没有特殊标记.
- **优化方案**: 可在 Summary 中提及失败次数 > 0 的作业数.

---

### /help

**文件**: `internal/skill/builtin/admin/help.go`

- **功能**: 优秀. 分类展示, 中文描述, ANSI 颜色, 详细帮助 (dbtop).
- **输出格式**: 使用 **ResultText**, 手工格式化但效果好.
- **交互问题**:
  - `detailedHelp` 目前只有 `dbtop` 一条, 其他命令无详细帮助
  - 新增命令 `/segments`, `/os`, `/awr`, `/ash`, `/planhistory` 未出现在分类中
  - 分类列表硬编码, 新增命令容易遗漏
- **优化方案**:
  1. 在分类中添加缺失命令:
     - "内存/存储" 加 `segments`, `os`
     - "SQL 分析" 加 `awr`, `ash`, `planhistory`
  2. 为每个命令补充 `detailedHelp`
  3. 考虑用 CLIDef 的 Usage/Examples 自动生成简单帮助

---

### /tableinfo

**文件**: `internal/skill/builtin/schema/tableinfo.go`

- **功能**: OK. 四段式: Columns + Indexes + Stats + Constraints.
- **输出格式**: 使用 **ResultText**, 但格式极其粗糙:
  - 每行输出为 `COL=val | COL=val | COL=val` -- 类似 debug 日志, 非表格, 不适合 DBA 阅读
  - 无 box-drawing, 无列对齐
  - section header 用 `=== Columns ===`, 与 Claude Code 风格不符
- **优化方案** (建议大改):
  1. Columns 改为 ResultTable 表格显示
  2. Indexes 改为 ResultTable
  3. Stats 用 key-value 格式 (`行数: 1,234,567 | Blocks: 15,420 | 最后分析: 2026-03-15`)
  4. Constraints 改为 ResultTable
  5. 用 `── Columns ──` 风格替代 `=== Columns ===`

---

### /indexadvise

**文件**: `internal/skill/builtin/schema/indexadvise.go`

- **功能**: OK. 检测全表扫描并给出建议.
- **输出格式**: 使用 **ResultText**. 执行计划部分是 DBMS_XPLAN 原生格式 (OK). 建议部分是简单文本.
- **交互问题**:
  - `displayPlanSQL` 和 `displayCursorSQL` 常量与 `query/explain.go` 中重复定义
  - 建议太笼统 ("Consider adding an index on columns used in WHERE/JOIN clauses"), 缺乏针对性
  - `isSQLText` 用空格判断, 与 `explain.go` 的 `looksLikeSQL` 逻辑不同 (后者更严谨, 检查关键字), 应统一
- **优化方案**:
  1. 复用 `query/explain.go` 的常量和 `looksLikeSQL` 函数
  2. 利用 `fullScanPredicatesSQL` 的结果给出具体建议 (如 "建议在 ORDERS.STATUS 列上创建索引")
  3. 输出具体的 `CREATE INDEX` 语句模板

---

## 系统性优化建议 (按优先级)

### P0: 修复正确性问题

| 序号 | 命令 | 问题 | 严重度 |
|------|------|------|--------|
| 1 | `/waits` | `average_wait/100` 单位转换错误 | 数据错误 |
| 2 | `/locks` | TX 锁 id1 JOIN dba_objects 逻辑错误 | 数据错误 |
| 3 | `/space` | block size 硬编码 8192 | 环境依赖 |
| 4 | `/slowsql` | 累计 elapsed_time 判断慢 SQL 而非平均 | 逻辑错误 |
| 5 | `/health:checkSlowSQL` | 同上, 累计时间判断 | 逻辑错误 |
| 6 | `/awr` | dur_sec 计算缺少 HOUR/DAY 部分 | 数据错误 |
| 7 | `/health:checkWaitEvents` | 永远返回 OK, 形同虚设 | 功能缺失 |

### P1: 格式化统一

| 序号 | 改动 | 影响范围 | 工作量 |
|------|------|----------|--------|
| 1 | 手工 `fmt.Fprintf` 命令改用 ResultTable | 约 12 个命令 | 中 |
| 2 | 消除重复工具函数 (`asString`, `formatNumber` 等) | 5 个文件 | 小 |
| 3 | 中文列头对齐修复 (用 `runewidth` 或改用 ResultTable) | 全部手工格式化命令 | 中 |

### P2: 交互增强

| 序号 | 改动 | 影响范围 |
|------|------|----------|
| 1 | `/help` 补充新增命令到分类 | help.go |
| 2 | `/sessions` 增加过滤参数 | sessions.go |
| 3 | `/activesessions` 增加 elapsed_sec 列和行数上限 | activesessions.go |
| 4 | `/health` status 加 ANSI 颜色 | health.go |
| 5 | `/locks` 增加 lock mode 翻译和 blocking_session | locks.go |
| 6 | `/alert` 增加 level 翻译 | alert.go |
| 7 | `/tableinfo` 格式全面重构 | tableinfo.go |
| 8 | `/blocktree` 增加 `/kill` 提示 | blocktree.go |
| 9 | `/kill` 设置 Rendered 字段 | kill.go |
| 10 | `/planhistory` 计划变更告警 | planhistory.go |

### P3: 建议改进 (非必须)

- `/waits` `/latches` 增加百分比列
- `/fra` `/asm` 增加 bar 图
- `/standby` 增加 apply lag 检测
- `/explain` 自动清理 plan_table
- `/indexadvise` 生成 CREATE INDEX 语句
- `/topsql` sql_text 截取长度增加到 100
- `/alter` 参数值基本校验
- 所有 ResultText 命令补充空结果中文提示

---

## 附录: 命令输出路径对照表

| 命令 | ResultType | 自定义渲染 | box-drawing | 中文标题 |
|------|-----------|------------|-------------|---------|
| /sessions | Table | N | Y (auto) | N |
| /activesessions | Table | N | Y (auto) | N |
| /waits | Table | N | Y (auto) | N |
| /locks | Table | N | Y (auto) | N |
| /latches | Table | N | Y (auto) | N |
| /mutexes | Table | N | Y (auto) | N |
| /health | Text | Y | N | 混合 |
| /tempsess | Text | Y | N | Y |
| /undosess | Text | Y | N | Y |
| /pga | Text | Y | N | Y |
| /sga | Text | Y | N | Y |
| /redo | Text | Y | N | Y |
| /fra | Text | Y | N | Y |
| /asm | Text | Y | N | Y |
| /sortusage | Text | Y | N | Y |
| /resource | Text | Y | N | Y |
| /blocktree | Text | Y | Y (tree) | Y |
| /segments | Table | N | Y (auto) | N |
| /os | Text/Table | Y (stats mode) | Y (proc mode) | Y |
| /slowsql | Table | N | Y (auto) | N |
| /explain | Text | Y | N | N |
| /topsql | Table | N | Y (auto) | N |
| /awr | Text | Y | N | Y |
| /ash | Text | Y | N | Y |
| /planhistory | Table | N | Y (auto) | N |
| /space | Table | N | Y (auto) | N |
| /params | Table | N | Y (auto) | N |
| /alert | Table | N | Y (auto) | N |
| /backup | Table | N | Y (auto) | N |
| /standby | Text | Y | N | N |
| /resize | Text | Y | N | Y |
| /kill | Text | Y | N | N |
| /alter | Text | Y | N | Y |
| /jobs | Table | N | Y (auto) | N |
| /help | Text | Y | N | Y |
| /tableinfo | Text | Y | N | N |
| /indexadvise | Text | Y | N | N |
