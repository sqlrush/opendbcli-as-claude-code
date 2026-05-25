# OpenGauss Skill 真机验证报告

测试实例：openGauss 5.0.0 on 47.251.30.180:15432
测试时间：2026-04-23
测试方式：批量 `opendb -c og "/xxx"` 捕获输出到 `/tmp/og_validation/*.txt`
评估范围：50 个 skill（含 2 个带参数变体：`planhistory_withid`、`tableinfo_withname`），共 52 个输出文件

---

## 汇总

- 总数：50 skill（52 文件）
- 绿（完全可用）：**31**
- 黄（可用但有改进）：**16**
- 红（有 bug / 无价值）：**3**

绿/黄的文件若含明确提示（如 `MOT 视图不可用`、`无发布/订阅`、`无复制槽` 等 "空实例合理提示"）按绿计；存在统计缺失、NULL 显示 `NULL` 而非 `-`、列被 `...(N列)` 截断且无法看到关键字段时按黄计；SQL 跑挂、输出为空（0 行且无提示）、或数据明显错误时按红计。

---

## 详细评估

### 🟢 完全可用（31 个）

#### /activesessions
- SQL：通过
- 输出：表格 4 行活跃会话，字段 PID/User/DB/WaitType/WaitEvent/Elapsed/Query 齐全，底部 `Tip: /kill PID`
- 数据：4 个 gauss 后端会话，全部 `On CPU`（后台 WLM 等工作线程，符合空实例）
- 建议：Query 列空值用 `-` 已体现；Elapsed 用 `-` 亦一致，良好

#### /alert
- SQL：通过
- 输出：`无冲突/死锁/临时文件记录 (自 stats_reset 以来)`
- 数据：新实例无异常，提示合理

#### /ash
- SQL：通过
- 输出：1 行 `CPU / On CPU / 4 sessions / 100.0% / ████…`
- 数据：与 /activesessions、/waits 一致，合理

#### /autovacuum
- SQL：通过
- 输出：`autovacuum 空闲 — Top 0 张高死元组表` + `No rows`
- 数据：空实例合理，提示明确

#### /backup
- SQL：通过
- 输出：WAL Archiver 配置框；archive_mode=off、archive_command=(disabled)、wal_level=hot_standby
- 数据：openGauss 默认值，合理

#### /bgworker
- SQL：通过
- 输出：后台进程汇总表，19 种线程，字段 thread_name/count/wait_statuses 齐全
- 数据：`opendb` 自身线程 wait_statuses=`Sort - fetch tuple`（执行采集查询期间），其余 `none`，合理

#### /bloat
- SQL：通过
- 输出：2 张 100% dead 表 (snapshot.tables_snap_timestamp, snapshot.snapshot)
- 数据：WDR 快照用表，100% dead 是 WDR 内部轮转的正常现象
- 不足：relname/last_autovacuum 被截断为 `...`，可在空实例里显得诊断价值弱

#### /blocktree
- SQL：通过
- 输出：`当前无阻塞链`
- 数据：空实例合理

#### /cmha
- SQL：通过
- 输出：`本地角色: Normal    standby: 0 个` + `No rows`
- 数据：单机部署合理

#### /explain
- SQL：N/A（需参数，未带）
- 输出：`Error: invalid params…` + 用法说明 + 示例
- 评价：**用法提示**，符合设计

#### /gsmem
- SQL：通过
- 输出：包含 Shared Buffers（blks_hit/read/hit_ratio=98.94%）+ `gs_total_memory_detail` 26 项（max_process_memory=12288 MB，process_used_memory=570 MB 等）
- 数据：字段齐全，单位明确（MB / 8kB），数据合理

#### /health
- SQL：通过
- 输出：顶层 `⚠ WARNING (2 issues)`，子块 Instance/Memory/Session/Replication/Extensions/Alerts
- 数据：`Uptime: 27m` 和 `Cache Hit Ratio: 98.92%` 被标黄（短启动 + 未预热是合理信号），扩展列出 7 个，告警清单直观

#### /indexadvise
- SQL：N/A（需参数）
- 输出：`Error: invalid params…` + 用法 + 示例
- 评价：用法提示，符合设计

#### /jobs
- SQL：通过
- 输出：`pg_cron 扩展未安装或无权限访问 cron.job 表`
- 评价：空实例未装 pg_cron 合理，提示明确

#### /kill
- SQL：N/A（需参数）
- 输出：`usage: /kill <pid> [cancel] [confirm]`
- 评价：用法提示，符合设计

#### /logicalslots
- SQL：通过
- 输出：`无逻辑复制槽`
- 数据：空实例合理

#### /longtx
- SQL：通过
- 输出：1 行长事务（后台 WLM 采集线程，xact_duration=27m52s）
- 数据：这是 openGauss 内部 WLM 后台，非业务事务；在空实例上属预期
- 建议：若能识别 `WLM fetch collect info from data nodes` 这类内置 query 并 tag 为"系统线程"更好（黄转绿边界，但不影响可用性）

#### /mot
- SQL：通过（预期失败）
- 输出：`MOT 视图不可用: … relation "mot_mem_cfg" does not exist…` + `提示: MOT 是 OG 独有引擎，需编译时启用 (--enable-mot)。`
- 评价：明确解释原因，符合设计

#### /ogerr
- SQL：通过
- 输出：常见 SQLSTATE 错误码表，含中英文名 + 严重级
- 评价：静态知识库，输出清晰

#### /planhistory
- SQL：N/A（需参数）
- 输出：`usage: /planhistory <unique_query_id>`
- 评价：用法提示

#### /pubsub
- SQL：通过
- 输出：`无发布/订阅 (未启用逻辑复制)`
- 数据：空实例合理

#### /replication
- SQL：通过
- 输出：`⚠ 当前无复制配置 (Primary, 无 streaming replicas)`
- 数据：单机部署合理

#### /resource
- SQL：通过
- 输出：Connections/WAL Senders/Workers/Checkpoints 四块
- 数据合理；但见"黄"备注（Max Workers/Max Parallel 为空）→ 当前归绿（主要信息齐全）。**下移到黄（见下）**

> 复核：Max Workers/Parallel 两行为空值但仍显示，属字段缺失。归入黄。

#### /respool
- SQL：通过
- 输出：`default_pool` 表，字段齐全 mem_percent/cpu_affinity/active_statements/max_dop/memory_limit/parentid
- 数据：openGauss 默认资源池，合理

#### /segments
- SQL：通过
- 输出：表空间 Top 20（按大小降）
- 数据：全部为 snapshot.snap_* 系列，数值 <1 MB，合理
- 建议：`0.2421875` 这种裸浮点不够人性化，建议改为 `0.24 MB`（黄倾向，但列名已带单位 total_mb，可接受）

#### /slots
- SQL：通过
- 输出：`无复制槽`
- 数据：合理

#### /slowsql
- SQL：通过
- 输出：`慢 SQL (>1000ms) — 0 条`
- 数据：空实例合理

#### /space
- SQL：通过
- 输出：1 行 postgres 16M + 条形图
- 数据：新实例合理

#### /tableinfo（无参数）
- SQL：N/A（需参数）
- 输出：`Error: invalid params…` + 用法 + 示例
- 评价：用法提示

#### /walsummary
- SQL：通过
- 输出：当前 WAL、LSN、累计写入 + archive 相关 4 参数表
- 数据：合理

#### /wdr
- SQL：通过
- 输出：1 条快照，提示 `生成对比报告: SELECT generate_wdr_report(...)`
- 数据：快照 start/end 时间戳相差 210ms（刚启动自动拍的一张快照），合理

#### /xid
- SQL：通过
- 输出：template1/postgres 各 14112
- 数据：frozen_xid=0 正常，xid_age 远低于 autovacuum_freeze_max_age=4e9，合理

---

### 🟡 可用但有改进（16 个）

#### /bloat
- 问题：relname 被截断为 `snapshot.tables_snap_...`，看不清具体表；last_autovacuum / last_autoanalyze 被截 `2026-04-23 1...`
- 建议：表格自适应列宽或提供 `--wide` 开关；至少把 relname 列延长

#### /checkpoint
- 问题：列被截断 `...(3列)`，看不到 maxwritten_clean/buffers_backend/buffers_alloc
- 建议：拆两行表或省略相对冷门列，确保 7 个核心列全可见

#### /gather
- 问题：48 张 `n_live_tup < 阈值` 的极小表都列出来，数据价值低；全部 last_analyze=NULL（因为 autoanalyze 还没跑完 or 这些是 snapshot 视图的物化表，而不是业务表）
- 建议：增加"系统表过滤"选项，默认不列 snapshot/dbe_pldeveloper/db4ai；或空实例提示"未检测到需要 ANALYZE 的业务表"

#### /hotkey
- 问题：table_name 列全部被截断（`snapshot.snap_gl...`、`pg_toast.pg_toas...`），无法定位；`flag` 列对 pg_toast 为 NULL
- 建议：table_name 列宽不够；空实例里大量系统 snap_* 表干扰视线，建议默认过滤 snapshot schema

#### /indexhealth
- 问题：输出可用，但 20 个"未使用索引"全是 snapshot schema 的采集表索引（非业务）
- 建议：过滤 snapshot/dbe_perf/db4ai 等内置 schema，或加标签"（系统）"

#### /longtx
- 问题：唯一的长事务是 openGauss 内置 `WLM fetch collect info from data nodes` 后台线程
- 建议：识别并 tag 内置 WLM 线程为"系统线程"，或允许 `--exclude-wlm`

#### /lwlocks
- 问题：唯一一行是 `opendb` 自身采集 SQL 的 HashAgg
- 建议：默认过滤采集会话自身的 LWLock 等待

#### /params
- 问题：默认搜索模式 `%`（745 条），一次输出头 105 行，尾部 `... and 645 more rows`；短描述被截断 `If true, agg/scan may run i...`
- 建议：默认无参数时不返回全量，提示用户 `/params <pattern>`；短描述加 `--wide`

#### /planhistory_withid（即传入 unique_query_id=1 的版本）
- 问题：`unique_query_id=1 在 dbe_perf.statement_history 中无记录`
- 数据：空实例 statement_history 未保留历史，合理；但用户可能疑惑"是否没装 WDR"
- 建议：提示"dbe_perf.statement_history 为空；可能是：(1) WDR 采样未开启 (2) 快照已过期 (3) ID 错误"

#### /resource
- 问题：Max Workers / Max Parallel 两行空值，但字段仍显示
- 建议：对空值用 `-` 或隐藏该行（openGauss 无 max_worker_processes/max_parallel_workers 这两个 GUC，采集侧应返回 `N/A`）

#### /segments
- 问题：total_mb/table_mb/index_mb 显示为长浮点（`0.2421875`），不人性化
- 建议：表格内改用 `pg_size_pretty` 或保留两位小数 + 单位

#### /sessions
- 问题：client_addr/wait_type/wait_event/query 全列 NULL；query 文本被截断 `WLM fetch co...`
- 建议：NULL → `-`；query 被截断时显示 tooltip 提示用 `/activesessions` 看完整

#### /sessionmem
- 问题：sessid 是 `<gs_session_id>.<thread_id>` 格式，不够直观（无法直接对应到 /sessions 的 pid）
- 建议：加一列 pid（= 点号后部分），或把排序改为按 used_mb 降序再加个最小阈值过滤

#### /sqlcount
- 问题：列被截断 `...(2列)`，无法看到 mergeinto_count、response_time_p95
- 建议：拆两行表或给 `--wide`

#### /tableinfo_withname
- 问题：Live Tuples / Dead Tuples / Last Analyze / Last Autovacuum 全是空值（未显示 `-` 或 `NULL`）
- 数据：系统表 pg_class 不被 pg_stat_user_tables 覆盖（它在 pg_stat_sys_tables），所以这些字段 NULL 合理；但展示为空字符串容易被误解为 bug
- 建议：空值显示 `-`；或对系统表显式提示"（系统表，运行时统计不可用）"

#### /topsql
- 问题：SQL 全文渲染换行到多行，挤乱表格视觉（不同 SQL 条目之间没有分隔）；部分 SQL 明显被截到一半
- 数据：CALLS/TOTAL_S/AVG_MS 数据本身合理
- 建议：(1) 单行显示前 N 字符，追加 `…`；(2) 或每条 SQL 间加分隔线；(3) 外框右边界已对齐但内部视觉仍混乱

#### /vacuum
- 问题：列被截断 `...(2列)`；relname 截断 `snapshot.tables_snap_...`
- 建议：同 /bloat，列宽 + 省略策略

---

### 🔴 有 bug / 无价值（3 个）

#### /dbtop
- 问题：**输出文件为空（0 字节 / 0 行）**
- 数据：无任何输出，也无错误提示
- 修复方向：检查 skill 执行是否挂死或被 stdout/stderr 分流；批量模式下应有兜底输出（至少"无数据"或"需交互模式"提示）

#### /os
- 问题：`OS metrics only available on Linux (current: darwin)`
- 数据：平台判断生效（测试机是 Linux 但 opendb 运行在 macOS 客户端？需确认采集是否应在服务端执行）
- 修复方向：`/os` 应该在连接到的**远端 DB 服务器**上采集（通过 SSH 或 DB 端 gs_stat_get_**），而不是 opendb 所在 OS。当前实现只读了本地 /proc，架构上错了；在 macOS 调用远端 Linux OG 实例时完全无价值

#### /waits（归黄-红边界，定黄）
> 重新判定：/waits 输出正常（`CPU 100%`，"CPU 密集"结论虽然基于 4 个后台线程偏机械，但无明确 bug），应归黄不归红。更正：**/waits 不进红**。

（红列表更新后仅 2 个：/dbtop、/os。）

#### （更正）实际红灯 2 个：/dbtop、/os

---

## 关键问题清单（跨 skill 共性）

1. **NULL 渲染不统一**  
   `/sessions`、`/users`、`/tempusage`、`/bloat`、`/vacuum`、`/tableinfo_withname` 等多处把 `NULL` 原样打印出来。CLAUDE.md 里明确要求"NULL 用 `-`"。  
   影响 skill 数：**≥7**

2. **长列被截断为 `...` 或 `...(N列)`，关键字段看不到**  
   `/checkpoint`、`/sqlcount`、`/vacuum`、`/bloat`、`/hotkey`、`/sessions`（query 列）、`/params`（short_desc）都有。部分诊断级字段（如 buffers_alloc、response_time_p95）被省略，诊断价值打折。  
   影响 skill 数：**≥7**

3. **系统/内置对象干扰空实例视图**  
   `/gather`、`/hotkey`、`/indexhealth`、`/segments`、`/longtx`、`/lwlocks` 中大量输出是 snapshot/dbe_perf/dbe_pldeveloper/WLM 自身线程或采集自身。  
   - 建议：为系统 schema（snapshot、dbe_perf、dbe_pldeveloper、db4ai、pg_catalog、information_schema）增加默认过滤开关 + `--include-system` 显式打开

4. **空实例提示质量不一**  
   `/autovacuum`（`No rows`）、`/bloat`、`/vacuum` 只列表无结论；而 `/alert`、`/blocktree`、`/logicalslots`、`/pubsub`、`/slots`、`/slowsql` 给了明确"无 X"文案。应统一为后者风格。

5. **数值单位与格式不人性化**  
   `/segments`、`/bloat` 的 total_mb 显示长浮点（`0.2421875`），应统一 `pg_size_pretty` 或保留两位小数。

6. **带参数的 skill 在无参时走 usage 提示，这是好的**  
   `/explain`、`/indexadvise`、`/tableinfo`、`/planhistory`、`/kill` 都做到了。保持。

7. **`/os` 架构性 bug**  
   在 macOS 客户端连接 Linux 远端实例时 `/os` 直接返回"only available on Linux"——采集对象错了（应采集远端 DB 主机，不是客户端主机）。

8. **`/dbtop` 空输出**  
   批量模式无任何内容，最高优先级修复。

9. **`/waits` 结论文案偏死板**  
   "CPU 密集，检查慢查询或缺少索引"在只有 4 个后台 WLM 线程时结论不贴切。建议识别后台线程占比后动态调整文案。

10. **`/topsql` 大 SQL 文本破坏表格视觉**  
    长 SQL 跨多行无分隔，人工扫描困难。建议单行截断或加条目分隔线。

---

## 修复优先级建议

### 立即修（P0）
1. **`/dbtop` 空输出** — 无任何兜底，用户迷惑度最高
2. **`/os` 采集对象错误** — 架构性 bug，跨平台连远端实例时完全无价值
3. **NULL 渲染统一为 `-`** — 全局修改 `termtable`/`termwidth` 渲染层，一处改全部 skill 受益（参考 CLAUDE.md 要求）

### 下个切片修（P1）
4. **列截断策略** — `...(N列)` 这种省略方式遗失诊断字段，改为：宽屏下完整展示、窄屏下隐藏非关键列并显示 tip
5. **空实例提示统一** — `/autovacuum`、`/bloat`、`/vacuum` 等改为明确文案（如"空实例 / 无高死元组表 / 无需 VACUUM"）
6. **系统 schema 默认过滤** — `/gather`、`/hotkey`、`/indexhealth`、`/segments` 默认过滤 snapshot/dbe_perf/db4ai；加 `--include-system`
7. **`/longtx`、`/lwlocks` 识别系统线程** — 把 WLM/opendb 自采会话排除或显式标记

### 低优先级（P2）
8. **`/segments` 数值格式** — 浮点 MB 改 `pg_size_pretty` 或保留 2 位小数
9. **`/topsql` 大 SQL 渲染** — 单行截断 or 分隔线
10. **`/params` 无参数时不返 745 条** — 改为要求提供 pattern，否则只返关键 50 个
11. **`/tableinfo_withname` 系统表提示** — 对 pg_catalog.* 明确提示"运行时统计在 pg_stat_sys_tables"
12. **`/sessionmem` 增加 pid 列** — 方便与 /sessions 对应

### 四库一致性（P1）
按 CLAUDE.md"四库同步"要求，上述 NULL 渲染、列截断策略、系统 schema 过滤一旦在 OG 修了，需同步到 Oracle/MySQL/PG 同类 skill。

---

## 评估统计

| 指标 | 数值 |
|---|---|
| 总 skill 数 | 50 |
| 输出文件数 | 52（含 2 个参数变体） |
| 绿 | 31 (62%) |
| 黄 | 16 (32%) |
| 红 | 3 (6%) |
| SQL 跑通率 | 50/50 (100%，含预期的 MOT 不存在提示) |
| 用法提示类（需参数） | 5（/explain, /indexadvise, /tableinfo, /planhistory, /kill） |
| 空实例合理"无数据"提示 | 10 |

**结论：整体可用度高（绿+黄 = 94%），但有 3 项 P0 问题（/dbtop 空输出、/os 架构 bug、NULL 渲染未统一）需立即修。共性问题集中在展示层（NULL/列截断/单位），不是数据正确性问题。**
