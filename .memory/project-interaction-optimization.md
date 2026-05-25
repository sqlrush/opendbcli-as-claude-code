---
name: 交互优化专项计划（已批准）
description: 经用户审核批准的全功能交互优化方案，含每个 skill 的 before/after 效果图和优先级
type: project
---

## 交互优化专项（用户已批准，待实施）

**Why:** 当前大部分功能的输出布局和交互不够精炼，系统性优化可显著提升产品体验
**How to apply:** 按优先级逐步实施，优化完成后再做新功能

---

## 优先级排序

| 优先级 | Skill | 理由 |
|--------|-------|------|
| **P1** | /health | 最高频命令，入口级体验 |
| **P1** | /space | 空间告警是 DBA 刚需 |
| **P1** | /waits | 性能诊断核心命令 |
| **P1** | /activesessions | 实时问题定位首要工具 |
| **P2** | /sentinel | AI 诊断入口，展示最复杂 |
| **P2** | /slowsql + /topsql | SQL 优化日常用命令 |
| **P2** | /explain | 执行计划要清晰易读 |
| **P2** | /kill | 操作类命令需明确确认 |
| **P2** | /alter | 参数修改需安全确认机制 |
| **P3** | /awr | 历史报告展示优化 |
| **P3** | /ash | 采样分析展示优化 |
| **P3** | /help | 帮助信息精简分组 |

---

## P1 详细方案

### /health — 综合健康检查

**当前问题**: 24项检查平铺罗列，没有分组，结构不清晰，告警项不突出

```
【Before】
┌─ Health Check ─────────────────────────────┐
│ buffer_hit_pct ................ 98.5%  OK  │
│ library_cache_hit_pct ......... 99.1%  OK  │
│ tablespace_system ............. 45.2%  OK  │
│ tablespace_users .............. 87.3%  WARN│
│ ...（24行平铺）                              │
└────────────────────────────────────────────┘
```

```
【After】
┌─ Database Health ──────────────────────────────────────────────┐
│  Overall: ⚠ WARNING  (2 issues found)                          │
├─ Memory ──────────────────────────────────────────────────────┤
│  Buffer Cache Hit    98.5%  ✓    Library Cache Hit  99.1%  ✓  │
│  Shared Pool Free   12.3%  ✓    PGA Usage          45.2%  ✓  │
├─ Storage ─────────────────────────────────────────────────────┤
│  SYSTEM              45.2%  ✓    USERS              87.3%  ⚠  │
│  TEMP                23.1%  ✓    UNDOTBS1           34.5%  ✓  │
├─ Session ─────────────────────────────────────────────────────┤
│  Active Sessions        15  ✓    Max Sessions          300  ✓  │
│  Long Queries            0  ✓    Blocked Sessions        0  ✓  │
├─ Backup ──────────────────────────────────────────────────────┤
│  Last Backup       2h ago  ✓    Backup Mode          NOARCHIVELOG│
├─ Alerts ──────────────────────────────────────────────────────┤
│  ⚠ USERS tablespace at 87.3% — consider adding datafile        │
│  ⚠ No recent RMAN backup detected                              │
└───────────────────────────────────────────────────────────────┘
```

**优化点**: 分组显示（Memory/Storage/Session/Backup）、Overall 汇总行、告警单独 Alerts 段

---

### /waits — 等待事件排名

**当前问题**: 数据密度低，重要指标（等待时间）没突出

```
【Before】
┌─ Wait Events ──────────────────────────────┐
│ EVENT                    COUNT    TIME_MS  │
│ db file sequential read  12543    45231    │
│ log file sync             4521    23100    │
│ ...                                        │
└────────────────────────────────────────────┘
```

```
【After】
┌─ Top Wait Events (Non-Idle) ─────────────────────────────────────────┐
│  #  Event                      Sessions  Pct    AvgMs  Category      │
│ ─────────────────────────────────────────────────────────────────── │
│  1  db file sequential read        8    52.3%   3.4ms  IO            │
│  2  log file sync                  3    18.7%   8.1ms  REDO  ⚠       │
│  3  enq: TX - row lock contention  2    12.4%  245ms   LOCK  !!      │
│  4  direct path read               2     8.3%   1.2ms  IO            │
│  5  cursor: pin S wait on X        1     4.1%  18.5ms  MEMORY        │
│ ─────────────────────────────────────────────────────────────────── │
│  Dominant: IO (60.6%)  — run /ash or /slowsql for SQL details        │
└──────────────────────────────────────────────────────────────────────┘
```

**优化点**: 增加 Category 列、Pct 进度条、AvgMs、底部诊断提示

---

### /activesessions — 活跃会话

**当前问题**: 会话信息太多，SQL 文本截断过早，状态不突出

```
【Before】
┌─ Active Sessions ──────────────────────────┐
│ SID  SERIAL  USERNAME  STATUS  SQL_ID      │
│ 123  456     APP_USER  ACTIVE  abc123def   │
│ ...                                        │
└────────────────────────────────────────────┘
```

```
【After】
┌─ Active Sessions (15 total, 3 waiting) ──────────────────────────────┐
│  SID    User        Status   Wait Event             Time  SQL Preview │
│ ──────────────────────────────────────────────────────────────────── │
│  123    APP_USER    ACTIVE   db file sequential     0.3s  SELECT c.*  │
│  456    APP_USER    WAITING  enq: TX row lock  !!  45.2s  UPDATE ord  │
│  789    BATCH_JOB   ACTIVE   —                      2.1s  INSERT INTO │
│ ──────────────────────────────────────────────────────────────────── │
│  [456] blocking: kill with /kill 456 456_serial                       │
└──────────────────────────────────────────────────────────────────────┘
```

**优化点**: 摘要行（总数/等待数）、等待时间列、阻塞提示和快捷命令提示

---

### /space — 表空间使用

**当前问题**: 只有数字，没有视觉化占用进度，高使用率不突出

```
【Before】
TABLESPACE   TOTAL_GB  USED_GB  FREE_GB  USED_PCT
SYSTEM          5.0      2.3      2.7     45.2%
USERS          20.0     17.5      2.5     87.3%
```

```
【After】
┌─ Tablespace Usage ───────────────────────────────────────────────────┐
│  Name          Used/Total      Pct   Bar                 Status      │
│ ──────────────────────────────────────────────────────────────────── │
│  SYSTEM         2.3G / 5.0G   45%   ████████░░░░░░░░░░  OK          │
│  USERS         17.5G / 20.0G  87%   ██████████████████  ⚠ WARNING   │
│  TEMP           0.5G / 10.0G   5%   █░░░░░░░░░░░░░░░░░  OK          │
│  UNDOTBS1       3.2G / 8.0G   40%   ███████░░░░░░░░░░░  OK          │
│ ──────────────────────────────────────────────────────────────────── │
│  ⚠ USERS at 87% — expand with: /resize USERS 5G                      │
└──────────────────────────────────────────────────────────────────────┘
```

**优化点**: 进度条可视化、状态图标、高使用率单独告警行 + 快捷扩容提示

---

## P2 详细方案

### /sentinel — 哨兵监控状态

**当前问题**: 监控数据展示不清晰，异常高亮不明显

```
【After — 正常状态】
┌─ Sentinel Monitor ─────────────────────────────────────────────────┐
│  Status: WATCHING  │  Uptime: 2h 34m  │  Alerts fired: 0 today     │
├─ Live Metrics (10s avg) ─────────────────────────────────────────  │
│  Active Sessions    12  ────────▌░░░░░░░░░  (baseline: 10)         │
│  DB Time/sec       0.8  █░░░░░░░░░░░░░░░░░                         │
│  Logical Reads/s  8234  ████████████░░░░░░                         │
│  Physical Reads/s  134  ██░░░░░░░░░░░░░░░░                         │
│  Redo Size/s      245K  ████░░░░░░░░░░░░░░                         │
├─ Top Wait ────────────────────────────────────────────────────────│
│  db file sequential read  8 sessions  (52%)                        │
└────────────────────────────────────────────────────────────────────┘

【After — 异常状态】
┌─ Sentinel Monitor ─── ⚠ BURST DETECTED ──────────────────────────┐
│  Status: ALERTING  │  Event: active_sessions spike  │  14:32:05    │
├─ Burst Snapshot ──────────────────────────────────────────────────│
│  Active Sessions   87  ████████████████████  (+750% vs baseline)  │
│  Top Wait: enq: TX row lock  (45 sessions, 67%)                    │
│  Blocking Chain: SID 234 → [12 waiters]                            │
├─ AI Analysis ─────────────────────────────────────────────────────│
│  Root Cause: 行锁争用冲高                                           │
│  Confidence: 87%                                                    │
│  Action: /blocktree 查看锁链, /kill 234 终止阻塞源                   │
└────────────────────────────────────────────────────────────────────┘
```

---

### /slowsql + /topsql — SQL 性能

```
【After】
┌─ Top SQL by Elapsed Time ────────────────────────────────────────────┐
│  #  SQL_ID        Executions  Avg(ms)  Total(s)  CPU%  Plan  Preview │
│ ──────────────────────────────────────────────────────────────────── │
│  1  8a4kd3xmn1      1,234     823ms    1015s    78%   FTS   SELECT * │
│  2  fz9qw2bvt8        456    1204ms     549s    45%   IDX   UPDATE o │
│  3  g7mn4pqas2         89    4521ms     402s    92%   FTS   DELETE F │
│ ──────────────────────────────────────────────────────────────────── │
│  ⚠ FTS detected on #1, #3 — run /explain <sql_id> for plan details   │
└──────────────────────────────────────────────────────────────────────┘
```

---

### /explain — 执行计划

```
【After】
┌─ Execution Plan: 8a4kd3xmn1 ────────────────────────────────────────┐
│  SQL: SELECT * FROM orders WHERE status = 'PENDING'                  │
│ ──────────────────────────────────────────────────────────────────── │
│  Id  Operation              Object           Rows   Cost  Note       │
│   0  SELECT STATEMENT                               4523             │
│   1   TABLE ACCESS FULL     ORDERS          89234   4523  ⚠ FTS     │
│ ──────────────────────────────────────────────────────────────────── │
│  ⚠ Full table scan on ORDERS (89K rows, cost=4523)                   │
│  💡 Suggestion: CREATE INDEX idx_orders_status ON orders(status)     │
│     Estimated improvement: 95%+ (index range scan, cost ~45)         │
└──────────────────────────────────────────────────────────────────────┘
```

---

### /kill — 终止会话

**优化点**: 操作类命令增加确认提示，避免误操作

```
【After】
> /kill 456

┌─ Kill Session ─────────────────────────────────┐
│  SID: 456  Serial#: 789                         │
│  User: APP_USER  Program: JDBC Thin Client      │
│  Status: WAITING (enq: TX - row lock, 45s)      │
│  SQL: UPDATE orders SET status=...              │
├─────────────────────────────────────────────────┤
│  ⚠ This will terminate the session immediately. │
│  Confirm? [y/N]:                                │
└─────────────────────────────────────────────────┘
> y
✓ Session 456,789 killed successfully.
```

---

### /alter — 参数修改

**优化点**: 显示当前值 vs 新值对比，需要重启的参数明确标注

```
【After】
> /alter sga_target 4G

┌─ Alter System Parameter ─────────────────────────────────────────────┐
│  Parameter: sga_target                                                │
│  Current:   2147483648 (2G)                                          │
│  New value: 4294967296 (4G)                                          │
│  Scope:     SPFILE  (requires restart)  ⚠                            │
├────────────────────────────────────────────────────────────────────── │
│  ⚠ This parameter requires database restart to take effect.          │
│  Confirm? [y/N]:                                                      │
└──────────────────────────────────────────────────────────────────────┘
> y
✓ Parameter sga_target updated in SPFILE.
  Restart required to apply changes.
```

---

## P3 详细方案

### /awr — AWR 分析

```
【After】
┌─ AWR Report: 14:00 → 15:00 (60min) ─────────────────────────────────┐
│  DB Time: 45.2 min  │  CPU: 38.1 min  │  Wait: 7.1 min              │
├─ Top Events ──────────────────────────────────────────────────────── │
│  1. db file sequential read   18.3%  ████░░░░░░                      │
│  2. log file sync              9.1%  ██░░░░░░░░                      │
├─ Top SQL ─────────────────────────────────────────────────────────── │
│  1. 8a4kd3xmn1   45.2% DB Time   FTS on ORDERS  ⚠                   │
├─ Period Comparison ───────────────────────────────────────────────── │
│  vs. prev hour:  DB Time +23%  ⚠   CPU +12%   Waits +45%  ⚠         │
└──────────────────────────────────────────────────────────────────────┘
```

---

### /help — 帮助系统

```
【After】
┌─ OpenDB Commands ──────────────────────────────────────────────────┐
│                                                                     │
│  MONITORING          QUERY              SPACE                       │
│  /health    巡检     /slowsql  慢SQL    /space   表空间             │
│  /sentinel  哨兵     /topsql   TopSQL   /segments 段分析            │
│  /waits     等待     /explain  执行计划  /fra     归档区             │
│  /sessions  会话     /sql      直查      /asm     磁盘组             │
│                                                                     │
│  DIAGNOSIS           OPERATIONS         AI                          │
│  /awr   AWR分析      /kill  终止会话    /diag   AI诊断              │
│  /ash   ASH采样      /alter 参数修改    /rule   规则诊断            │
│  /locks 锁分析       /resize 扩容                                   │
│                                                                     │
│  Type /help <command> for details, e.g. /help waits                │
└────────────────────────────────────────────────────────────────────┘
```

---

## /diag — AI 诊断流程

```
【After — Claude Code 风格进度展示】
> /diag

◆ Starting database diagnosis...
  ✓ Collecting active sessions (15 found)
  ✓ Analyzing wait events (top: db file sequential 52%)
  ✓ Checking SQL performance (3 slow queries detected)
  ✓ Scanning alert log (0 ORA errors)
  ✓ Evaluating space usage (USERS at 87% ⚠)
  ◆ Running AI analysis...

┌─ Diagnosis Result ──────────────────────────────────────────────────┐
│  Root Cause: I/O 压力偏高 + 表空间告警                               │
│  Confidence: 82%                                                     │
├─ Evidence ──────────────────────────────────────────────────────────│
│  • db file sequential read 占 52% 等待                              │
│  • 3 SQL 全表扫描大表（ORDERS, ITEMS）                              │
│  • USERS 表空间 87%，剩余 2.5GB                                     │
├─ Actions ───────────────────────────────────────────────────────────│
│  1. /explain 8a4kd3xmn1 — 查看全表扫描执行计划                      │
│  2. /resize USERS 5G — 扩容 USERS 表空间                           │
│  3. 考虑对 orders(status) 建立索引                                  │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 通用 UI 原则（从本方案提炼）

1. **摘要行** — 每个命令首行展示核心汇总（总数、状态、关键数字）
2. **分组展示** — 相关指标分组，不要平铺 24 行
3. **状态图标** — ✓ OK、⚠ WARNING、!! CRITICAL，颜色编码
4. **进度条** — 百分比指标配进度条（20字符宽）
5. **快捷提示** — 发现问题后，底部显示下一步命令（如 `/kill SID`, `/resize TS 5G`）
6. **操作确认** — /kill、/alter 等写操作必须展示当前状态 + 明确确认
7. **右边框对齐** — 所有 box 右边框必须对齐
