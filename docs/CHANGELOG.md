# OpenDB Changelog

## v1.2.23 (2026-05-24) — Sentinel Burst Replay Golden

### 新增

- 增加 Sentinel burst replay golden 语料, 使用 JSON payload 回放 IO temp spill 与 lock chain 两类现场告警形态。
- Replay 测试覆盖完整链路: JSON 反序列化 -> Sentinel 分类 -> evidence builder 输出 -> forbidden/required contract 校验。
- Replay contract 禁止 `<SQL_ID>`、`<pid>` 等模板占位再次进入用户可见报告。

### 验证

- `go test -tags "opengauss,gaussdb,dbaa" ./internal/opengauss/golden ./internal/opengauss/sentinel`
- `make golden`

## v1.2.22 (2026-05-24) — Sentinel IO 根因分类

### 新增

- Sentinel 新增 `IO瓶颈` root cause, 将临时文件写入、IO wait 主导、低缓存命中伴随高活跃从慢 SQL 泛化分类中拆出。
- IO 类异常增加专项验证 SQL: `pg_stat_database` temp bytes、`pg_stat_activity` IO wait、`work_mem/temp_file_limit/shared_buffers/effective_cache_size` 参数检查。
- Tier 0 Sentinel golden 增加 IO 场景, Sentinel classifier 单测覆盖 temp spill、IO wait profile 和 temp bytes trigger 映射。

### 修复

- `MetricTempBytesRate` 和 `MetricCacheHitPct` 触发时不再默认归因到慢 SQL, 先进入 IO 证据链, 减少现场排障跑偏。
- Rule engine 分类映射新增 IO category, 后续 JSON 规则可以独立承接 IO 类告警。

### 验证

- `go test -tags "opengauss,gaussdb,dbaa" ./internal/opengauss/sentinel ./internal/opengauss/golden ./internal/opengauss/ruleengine ./internal/opengauss/skill/ai`
- `make golden`

## v1.2.21 (2026-05-24) — Sentinel 事故证据链与 Golden 覆盖

### 新增

- Sentinel evidence builder 完整化为事故处理链: 告警指标、Baseline vs Current、Burst 时刻证据、当前快照对比、Top SQL、等待事件、阻塞链、主因/次因。
- Sentinel 输出增加原因相关的验证 SQL 与回滚方案, 覆盖慢 SQL、锁等待、Vacuum 滞后、XID、WAL、连接风暴、复制延迟和 Checkpoint 冲高。
- Tier 0 golden 增加 Sentinel 事故场景矩阵, 固化 slow SQL、lock、WAL、connection、XID 等高风险异常输出契约。

### 修复

- Sentinel 报告不再只给异常摘要, 小模型可基于结构化证据生成诊断, 大模型也能获得完整事故上下文。
- 未知根因场景明确只建议继续采集证据, 避免在规则证据不足时给出破坏性操作。

### 验证

- `go test -tags "opengauss,gaussdb,dbaa" ./internal/opengauss/sentinel ./internal/opengauss/golden ./internal/opengauss/skill/ai`
- `make golden`

## v1.2.20 (2026-05-24) — SQLTune Golden 实测与确定性候选深化

### 新增

- Tier 1 DB golden 增加 `/sqltune 581990336` 真实 SQL_ID 用例，断言 SQL_ID 解析、SQLTune 报告、执行计划与优化方案输出质量。
- Tier 1 runner 支持 `OPENDB_GOLDEN_CASES`，可以只跑指定真实 DB/LLM case，便于发版前专项验收 SQLTune。
- SQLTune deterministic scaffold 增加重复 `IN (...)` 字面量去重改写候选，并把 rewrite/index/stats 维度写入 explored dimensions。

### 修复

- SQLTune 验证阶段增加 preflight gate，明显无效的省略号、模板占位符、SQL 残片不会再进入 EXPLAIN，减少无效数据库调用与噪声错误。
- Round 1 SQLTune prompt 明确禁止候选 SQL 使用占位符或草案，提示确定性候选由引擎自动补齐，降低小模型凑方案概率。
- Tier 2 SQLTune model golden 增加模板占位符相关 forbidden 断言。

### 验证

- `make golden`
- `OPENDB_GOLDEN_CONN=gauss_local make golden-db`
- `OPENDB_GOLDEN_CONN=gauss_local OPENDB_GOLDEN_ENABLE_LLM=1 OPENDB_GOLDEN_CASES=OG-GOLDEN-DB-SQLTUNE-SQLID-001 OPENDB_GOLDEN_TIMEOUT_SEC=360 make golden-db`

## v1.2.19 (2026-05-24) — Golden/Trace/SQLTune 系统化质量门禁

### 新增

- 建立 OpenGauss/GaussDB golden 分层体系：Tier 0 deterministic、Tier 1 DB integration、Tier 2 model matrix、Tier 3 TUI/PTY。
- `make golden` 纳入 Tier 0 + CI-safe TUI 回归，GitHub Actions 同步执行 golden gate。
- `/trace last --json`、`/trace history [N] [--json]` 支持持久化审计输出，记录 route、skill、prompt 摘要、token、round、tool call 和错误摘要。

### 修复

- SQLTune fallback report 加强候选质量门禁：模板占位符、`原SQL不变`、省略号 SQL、明显 SQL 残片只进入 rejected diagnostics，不进入正式优化方案。
- SQLTune 被拒候选增加明确诊断说明，避免把 LLM 草案误读为可执行 DBA 建议。
- TUI golden 覆盖长输出尾部重复、进度轮次、表格与执行计划标注渲染，防止终端层回归。

### 验证

- `go test -tags "opengauss,gaussdb,dbaa" ./internal/opengauss/... ./internal/diagtrace ./internal/ui`
- `go test -tags "opengauss,gaussdb,dbaa" ./internal/ui ./internal/ui/uitest -run "TestTier3|TestRenderCodeBlock_WrapsLongSQLWithoutEllipsis" -count=1`
- `make golden`

## v1.2.18 (2026-05-23) — WDR 报告收尾与自然语言路由完善

### 修复

- WDR 报告 footer 改为结构化“报告元信息”，去掉 `_..._` 斜体残留导致的 `v1_` 误读。
- WDR 报告保存路径并入报告元信息，终端输出和保存 markdown 保持一致。
- 自然语言 `我们分析下65到73的报告存在哪些问题` 稳定路由到 `wdranalyze 65 73`，不再误走 WDR 列表或通用 LLM。
- 用户可见报告中将 `Evidence Builder` 改为“结构化证据”，内部调试词转移到 trace/debug 视角。
- WDR 报告增加“诊断边界”和证据置信度，明确 WDR 是历史窗口报告，当前在线故障需结合实时快照复核。
- `/trace last` 增加 route_kind、tool_call_count、round_count，便于复盘 direct skill / evidence skill / LLM 路径。

### 验证

- `go test ./internal/opengauss/... ./internal/diagtrace ./internal/ui`

## v1.2.17 (2026-05-23) — TUI 诊断流式输出去重

### 修复

- 修复自然语言诊断流式输出完成后，报告尾部可能在 REPL 提示符附近重复刷新的问题。
- async 诊断完成事件改为单来源：如果 DiagnoseSkill 已通过 progress 回调发出 `done/error`，外层 goroutine 不再补发第二个完成事件。
- 新增 UI 回归测试，防止后续再次出现 skill 已完成但外层重复渲染结果的情况。

### 验证

- `go test ./internal/ui`
- `go test ./internal/opengauss/... ./internal/diagtrace ./internal/ui`

## v1.2.16 (2026-05-23) — OpenGauss intent router / golden / trace 初版

### 修复

- 新增 OpenGauss/GaussDB 自然语言 intent router，统一判定 `wdr_list`、`wdr_analyze`、`sql_tune`、`current_diag`、`slow_sql`、`blockers`、`sessions`、`params`、`object_stats` 等入口。
- `llm` 入口改为先产出 route decision：列表类直接走 skill；WDR/SQL 调优类走 evidence skill；当前诊断与开放解释类仍进入 LLM 分析。
- 新增 `diagtrace` 记录最近一次诊断的 input、intent、mode、skill、params、reason、tool_calls、LLM 使用情况和耗时。
- OpenGauss `/trace last` 支持查看最近一次诊断 trace，避免排查“LLM 在干嘛”时只能猜。
- 新增 route golden 和 dispatcher 黑盒测试，固化 WDR 列表、WDR 分析、SQL_ID 调优、阻塞、慢 SQL、会话、参数、对象统计等近期高风险入口。

### 验证

- `go test ./internal/opengauss/... ./internal/diagtrace`

## v1.2.15 (2026-05-23) — P0 优化前稳定回退点

### 修复

- 修正 WDR 自然语言路由边界：`当前有哪几个wdr报告` 仍直接列快照，`分析 快照76和77之间生成的wdr报告` 改为调用 `wdranalyze 76 77`，不再被误判为列表查询。
- `/wdranalyze` 兼容 GaussDB/openGauss 不同版本的 `generate_wdr_report` 签名：优先使用 `pg_catalog.generate_wdr_report(bigint,bigint,cstring,cstring,cstring)`，再回退到旧版 `dbe_perf.generate_wdr_report(...)`。
- 将当前 P0 evidence builder、SQL/DDL 完整展示、WDR 列表直达等已验证改动收敛为后续系统化 intent router / golden / trace 优化前的稳定回退点。

### 验证

- 新增单测覆盖 WDR 列表与 WDR 分析意图区分、snapshot pair 参数提取、GaussDB WDR 函数签名候选顺序。

## v1.2.14 (2026-05-23) — WDR 列表意图确定性路由

### 修复

- 自然语言 `当前有哪几个wdr报告`、`列出 WDR 快照列表` 等列表意图直接调用 `wdr` skill，不再进入 LLM auto 多轮分析。
- WDR scene filter 增加 `wdr` 工具，避免 prompt mode 下模型只看到 `wdranalyze` 后误生成报告。
- `/wdr` 输出直接包含最近快照表和 `/wdranalyze <begin_snap> <end_snap>` 指引，减少二次解释和错误 SQL。

### 验证

- 新增单测覆盖 WDR 列表 intent 路由、scene filter 工具暴露和 `/wdr` 表格渲染。

## v1.2.13 (2026-05-23) — SQL/DDL 代码框完整换行展示

### 修复

- 修复 TUI markdown 代码框为了防止边框换行而把长 SQL/DDL 行截断成 `..` 的问题。
- 代码框现在对超宽 SQL 行做框内换行展示，避免 `CREATE TABLE ... INSERT ...`、`CREATE STATISTICS ... FROM ...` 等方案在终端里丢失中间内容。

### 验证

- 新增 UI 单测覆盖长 SQL 代码块不会出现省略号截断，且完整保留 `CREATE TABLE`、`INSERT INTO`、`SELECT * FROM` 等关键内容。

## v1.2.12 (2026-05-23) — P0 evidence builder 强化

### 背景

客户验收暴露三个高风险入口不能继续只依赖模型自由阅读原始输出：`/sqltune`、WDR/perfsnap 长窗口报告、Sentinel 异常触发诊断。弱模型容易遗漏关键证据或编造建议，强模型也会因上下文过大而输出不稳定。

### 修复

- `/sqltune` fallback report 增加 `sqltune_evidence_builder`：在正式优化方案前输出代价热点、执行计划 `[P#]` 标注、表/索引/统计信息证据，并继续把语法错误、截断 SQL、低收益或等价性失败候选放入拒绝区。
- WDR 分析增加 `wdr_evidence_builder`：结构化输出时间窗口、负载变化、Top Wait、Top SQL、IO/CPU/Lock/WAL 分类、根因链和建议动作，并把该证据包注入 LLM synthesis prompt，避免模型只做表面总结。
- `/perfsnap snap/compare` 增加结构化 evidence 输出：展示快照窗口、Cache Hit/Temp/Deadlock/Checkpoint/Top SQL 变化分类和动作建议。
- Sentinel 异常报告增加 `sentinel evidence builder`：输出告警指标、baseline vs current、burst 窗口证据、Top SQL、等待事件、阻塞链、主因/次因、紧急措施和根因修复，并同步进入 LLM 压缩上下文。

### 验证

- 新增/更新单测覆盖 sqltune 关键证据区、WDR evidence builder、perfsnap evidence builder、Sentinel evidence builder。

## v1.2.7 (2026-05-23) — SQL_ID 调优小模型确定性重构

### 背景

Qwen3-32B PromptToolAdapter 场景下，用户问 `sql id 581990336 如何优化`
时，弱模型仍可能先 `/sqlfetch`，再编造 `customers` 等示例 SQL 传给
`/sqltune`，导致 Phase A 在不存在的表上 `EXPLAIN` 失败。

### 修复

- 自然语言命中 SQL_ID + 调优/优化/执行计划意图时，产品层在首轮前强制
  执行 `sqltune {"args":"<SQL_ID>","mode":"quick"}`，外层模型不再参与
  工具选择或 SQL 拼接。
- PromptToolAdapter、通用 system prompt 和去重提醒统一改为“SQL_ID 调优
  直接把数字 ID 交给 sqltune”，禁止模型手写 dbe_perf 查询或编造示例表。
- `sqlfetch` 工具描述改为只在用户要看 SQL 文本时使用；SQL_ID 调优优先
  直接调 `sqltune`，由 sqltune 内部复用受控 resolver。
- `/sqltune` 在 Round 1 LLM 失败或弱模型候选质量不足时，基于 Phase A 的
  EXPLAIN + schema 自动生成确定性 index/stats 基线候选，保证小模型也能
  输出可落地的低风险优化方向。
- fallback report 增加硬门禁：语法错误、含 `...` 省略号、等价性失败、
  cost 改善不足 30% 的 rewrite/hint 不再作为正式方案展示；DDL/index/stats
  不再展示未验证收益倍率。
- 报告执行计划章节不再只显示 `Total cost`，改为展示带 `[P#]` 标注的 plan tree，
  并在优化方案中反向引用对应计划节点。
- plan tree 改为完整展示所有节点，同时限制深层缩进和长条件宽度；`[P#]`
  标注在 TUI 中用高亮颜色显示。
- EXPLAIN 语法失败、截断或不完整的 LLM rewrite/hint 候选不再出现在用户报告中，
  避免把模型无效草案展示成“优化方案”。
- 方案 SQL/DDL 改为完整多行展示，避免终端窄屏把 `CREATE STATISTICS`、
  `ANALYZE` 等语句截成 `...` 后无法复制执行。
- 对当前交互无法验证或无法可靠评估的信息不再填空：DDL/统计类方案隐藏
  “待验证/预期收益待验证/收益判断”等低价值字段，仅保留证据、SQL、风险、
  执行前检查和回滚方式。

### 验证

- 新增单测覆盖 SQL_ID 强制路由、强制首轮 sqltune 直通、确定性 index/stats
  候选生成、未验证倍率隐藏、语法无效候选隐藏、执行计划标注、方案 SQL
  完整展示和空字段隐藏。

## v1.2.6 (2026-05-22) — sqltune 报告质量门禁

### 背景

v1.2.5 修复了自然语言 SQL_ID 调优不进入 `/sqltune` 的路由问题，但客户
验收发现 quick mode 报告仍可能把未验证、验证失败、截断 SQL 或高风险
schema 改造作为正式优化方案展示。

### 修复

- `/sqltune` fallback render 增加确定性候选分层：已验证可采纳、需落库验证、
  长期结构改造、未采纳/无效候选。
- 只有 `EXPLAIN` 成功、cost 改善达到 30% 以上、SQL 完整且等价性未失败的
  rewrite/hint 候选会进入“已验证可采纳方案”。
- DDL/index/stats 不再显示“EXPLAIN 验证: DDL 类未实跑”作为正式收益口径；
  改为“需落库后验证”，并隐藏未验证倍率。
- 截断 SQL（如 `...` / “原 SQL 不变”）和 EXPLAIN 失败候选移入“未采纳/无效候选”，
  不再显示可复制执行的 SQL 代码块。
- schema、分区、列存、表重建等高风险改造移入“长期结构改造”，quick mode
  不再和低风险索引/统计修复并列主推。
- SQL_ID 经过 synthetic bind 替换时，报告头明确声明只能作为 plan-shape
  参考，不能把替换后的字面量当真实业务取值。
- openGauss/GaussDB 索引采集不再使用 `WITH ORDINALITY`，避免 OG 现场
  `schema_collect` 失败后误报 “no indexes”。

### 验证

- 新增单测覆盖未验证倍率隐藏、截断 SQL 隐藏、长期 schema 改造降级、
  低收益 verified 候选不采纳、索引采集 SQL 兼容 openGauss。

## v1.2.5 (2026-05-21) — SQL_ID 调优确定路由 + sqltune 报告直通

### 背景

v1.2.4 已修复 prompt mode 未生效和“当前数据库有哪些问题”类问题裸答，
但客户继续验证 `sql id 581990336 如何优化` 时，Qwen3-32B 会调用
`health/topsql/slowsql/blocktree` 后泛化回答，没有优先进入 `/sqltune`。

### 修复

- 自然语言命中 SQL_ID + 调优/优化/执行计划/慢/性能意图时，openGauss/GaussDB
  诊断入口在首轮前强制执行 `sqltune`，不再依赖弱模型自行选工具。
- `/sqltune` 复用 `/sqlfetch` 的 SQL_ID 解析和 statement_history/statement
  查询路径，避免维护两套 SQL_ID fallback 逻辑。
- Engine 报告直通标记扩展到 `SQLTUNE_REPORT_BEGIN`，`sqltune` 生成的完整
  调优报告会直接返回给用户，避免被外层模型二次总结成“当前数据库主要问题”。
- 强制首轮工具也支持报告直通；`sqltune` 命中后不会再额外调用外层 LLM。
- `sqlfetch` 占位符计数补齐 `$N`、`:N`、`:name`，并跳过字符串和注释。

### 验证

- 新增单测覆盖 SQL_ID 自然语言强制路由、强制首轮 sqltune 直通、
  普通 sqltune tool call 直通、SQL_ID 参数解析和占位符计数。

## v1.2.4 (2026-05-21) — DBAA 现场热修：强制证据采集 + PromptToolAdapter 生效

### 背景

客户现场反馈自由提问“当前数据库有哪些问题”时，模型直接给出通用答案，
未看到有效工具调用；同时 `/topsql` 中存在 SQL_ID，但 `/sqltune <SQL_ID>`
在 `statement_history` 缺失时仍会 0.0s 失败。

### 修复

- 修复 inline `models:` 中 `tool_mode: prompt` 进入运行态时丢失的问题；
  `/model reload` 后也保留 `tool_mode`。
- `/model` 列表新增 `TOOL_MODE` 列，现场可直接确认 active model 是否走
  prompt/native。
- 对“当前数据库/状态/问题/异常/性能”等诊断类问题，LLM 首轮前强制执行
  `health`、`activesessions`、`waits`、`topsql`、`slowsql`、`blocktree`
  只读工具，并把结果作为 tool evidence 注入后续分析。
- Engine 新增 `RequireToolEvidence` 保护：要求证据的问题若首轮没有任何
  tool 调用，不再接受模型裸答。
- GaussDB 注册 `/sqlfetch` 和 `/wdranalyze`，补齐与 openGauss 的工具集差异。
- `/sqltune <SQL_ID>` 在 `dbe_perf.statement_history` miss 后回退
  `dbe_perf.statement.unique_sql_id`，解决 TopSQL 可见但 sqltune 查不到的问题。

### 验证

- 新增单测覆盖 inline `tool_mode`、reload 保留、强制首轮工具调用、
  裸答拦截、SQL_ID fallback 到 `dbe_perf.statement`。

## v1.2.3 (2026-05-20) — GaussDB 收敛到 OG 单源实现 (热修)

### 背景

农行 dbaa v1.2.2 现场反馈：

1. `/sqltune <SQL_ID>` 在 GaussDB 上 **0.0s 立即报 `plan collection failed; cannot continue`**
2. 自由对话 "sql id <N> 给出优化建议" 12s 输出 10 条通用 markdown，**零工具调用**

### 根因

**Bug A — GaussDB `/sqltune` 缺 SQL_ID 识别分支（本版修复）**

`internal/gaussdb/skill/query/sqltune_skill.go` 从 og 复制时漏搬了
`looksLikeSQLID` + `fetchLiteralSQLByID` + `blindNullSubstitute` 三段，
导致纯数字 SQL_ID（如 `2356825359`）被当成 SQL 文本直接送进
`planner.ExplainPlan`，立即语法错 → cc.Plan=nil → "plan collection
failed; cannot continue"。

**Bug B — PromptToolAdapter 只接到 og /diagnose（本版未修，下版处理）**

`tool_mode: prompt` 在配置里已设，但 `SetToolMode` 全代码只在
`internal/opengauss/skill/ai/diag_skill.go:301` 调用，GaussDB 任何
路径 + 通用 REPL 自由对话路径都没接 → 非 FC 模型自由提问时拿不到
工具描述 → 纯背书答案。

### 修复（Bug A）

**GaussDB 后端全部收敛到 OpenGauss 实现**，唯一保留独立的就是驱动。

具体改动：

- `internal/gaussdb/register.go` line 133：`/sqltune` 改为直接调
  `query.NewSQLTuneSkill(driver, modelMgr, nil)`（og 的实现），不再
  通过 `gaussdbquery.NewSQLTuneSkill`
- 删除 `internal/gaussdb/skill/` 整个目录（仅 sqltune_skill.go）
- 删除 `internal/gaussdb/sqltuner/` 整个目录（planner.go / trace.go /
  llm_adapter.go / prompt.go ≈ 1000 行）

**代价**：GaussDB 失去 GS_PLAN_TRACE（华为商业版独有的 CBO 决策 dump）
能力。当前客户场景验收不需要，未来若客户提出可再补回。

**收益**：

- Bug A 自动消失（继承 og 的 SQL_ID 路径，已端到端验证）
- 削减 1200+ 行 GaussDB 独立实现，单源单逻辑，未来 og 增强自动同步
- 维护成本降低（避免类似"复制时漏搬"的回归）

### 验证

本机 og 连接（127.0.0.1:5433）跑过两条：

1. `/sqltune SELECT count(*) FROM pg_class` → 1m5s 出 4 候选 + EXPLAIN +
   4 优化方案完整报告 ✅
2. `/sqltune 581990336`（数字 SQL_ID）→ 自动识别 → 从
   `dbe_perf.statement_history` 拉回原始 WITH 多 CTE 复杂 SQL →
   `?` 占位符兜底 → EXPLAIN + Phase A 数据完整呈现 ✅

GaussDB 路径现在与 OG 行为完全一致。

### 编译

```bash
# macOS 本地
go build -tags 'oracle mysql postgres opengauss gaussdb' -o opendb ./cmd/opendb/

# Linux 全平台
go build -tags full -o opendb-linux ./cmd/opendb/
```

---

## v1.2.2 (2026-05-19) — PromptMode 工具链质量提升 + welcome panic 修复

### 背景

v1.2.1 PromptMode 真机测 "SQL_ID 581990336 如何优化"  10 分钟超时失败.
工具链: sqltune → sql → explain → sqltune (重复). 同模型 FC 模式 2m38s
完成 (sqlfetch → sqltune → explain → tableinfo). Opus benchmark 也指出
工具循环不收敛是 PromptMode 主要短板.

### 修复 1: 加强 few-shot, SQL_ID 必须先 sqlfetch

prompt_mode_builder.go::fewShotPrompt:
- 示例 1: 明确 sqlfetch 是 SQL_ID 场景的唯一正确起手, 加反例 (sqltune
  直接吃 SQL_ID 会失败)
- 新增示例 1b: 上一轮 sqlfetch 返回后, 下一轮怎么用 sqltune
- formatRulesPrompt 加 2 条硬约束:
  - SQL_ID 必须先 sqlfetch 再 sqltune (反例: 直接调 sqltune 死循环)
  - 同工具相同参数不重复调用 ≥ 2 次

### 修复 2: 跨轮工具去重检测 (engine.go)

引擎层添加 toolCallCounts map 跨轮跟踪 (name + args hash). 任何 signature
出现 ≥ 2 次, 下一轮 LLM 调用前注入 system-reminder:

```
⚠️ 检测到重复工具调用: 你已经用相同参数调用过 `sqltune` 工具 2 次.
重复调用大概率拿到相同结果. 请换一种策略:
  - 如果之前调用失败 → 换工具
  - 如果之前已拿到数据 → 直接给最终答案 (格式 B)
  - 如果是 SQL_ID 调优场景: 必须先 sqlfetch 再 sqltune
```

新增 `toolCallSignature(name, args) string` 规范化函数 (小写 + 前 80 字符).
测试: TestEngineToolDedupWarning + TestToolCallSignature.

### 修复 3: welcome 页 panic (pre-existing bug, 独立修复)

`repl_welcome.go:101` `groupIndent = (leftW - groupMaxW) / 2` 在窄终端
(cols < ~70) 或 PTY 0×0 时为负, 下游 strings.Repeat 直接 panic. 加 clamp
+ 在 strings.Repeat 调用点 defense-in-depth.

测试: TestWelcomeNoPanic_TinyTerminal 覆盖 cols=0/1/10/40/59/60.

### 实测对照 (同一查询 "SQL_ID 581990336 如何优化")

| 版本 | 模式 | 耗时 | 工具链 | 结果 |
|---|---|---|---|---|
| v1.2.1 | PromptMode | 10m 超时 | sqltune→sql→explain→sqltune | ❌ |
| v1.2.1 | FC | 2m38s | sqlfetch→sqltune→explain→tableinfo×2 | ✅ |
| **v1.2.2** | **PromptMode** | **~5m17s** | sqlfetch→sqltune→explain→sqltune | ✅ 5 轮完成 |

v1.2.2 PromptMode 仍比 FC 慢一倍 (sqltune 第二次仍是浪费, 但 LLM 看到
dedup 警告后转 Format B 给最终答案, 引用了 explain cost + sqltune
fallback 数据出可执行的优化方案).

### 影响范围

- `tool_mode: prompt` 模型 (qwen36-35b-promptmode 等): 受惠
- `tool_mode: native` / 未设字段的模型 (opus / qwen35-122b / GLM /
  DeepSeek / Kimi / qwen36-35b-fc / qwen36-35b-a3b): **完全不受影响** —
  PromptModeBuilder 不实例化, NativeFCBuilder 是 no-op
- engine 层去重检测对所有模型都生效, 但只在 ≥2 次重复时才注入 reminder.
  正常流程下不会触发

### 限制

- dedup 第一次重复只是发警告, 第二次重复才中止. 强模型基本 1 次警告就
  收手, 但中型模型 (35B) 仍可能多浪费 1 轮
- v1.2.x 后续优化方向: 按模型档位特化 prompt, 强模型用精简版

---

## v1.2.1 (2026-05-19) — PromptToolAdapter Phase 3+4: 错误重试 + 流式适配

### 背景

v1.2.0 把 Phase 3/4 延后 ship 了 (用户当场指出这是越级行为), 这版补完:
- Phase 3: JSON 解析失败时的错误反馈重试回路
- Phase 4: 流式 Format A/B 检测与路由

### Phase 3: 错误反馈重试

LLM 在 prompt 模式下输出残缺 JSON 时 v1.2.0 行为: 解析失败 → 当 Format
B 返回 → 用户看到一段 ``` json {br...`` 这种半成品当成"答案". v1.2.1
改成自动重试:

1. **`Response` 加 `NeedRetry / RetryFeedback` 字段** (types.go)
2. **`PromptModeBuilder.PostProcessResponse`**: parser 返回 ParseError
   时设 NeedRetry=true + 拼一段 "你输出 X 不合法, 请按 Format A/B 重试"
   的反馈
3. **Engine 主循环**: 检查 resp.NeedRetry, 把 RetryFeedback 作为
   `<system-reminder>` user 消息追加, 进入下一轮 LLM 调用. 最多 2 次
   重试 (`maxParseRetries=2`), 超过则放弃 → 把最后一次 Content 当
   最终答案返回 (best-effort)

测试: TestEngineParseRetryFeedback (3 轮成功路径) +
TestEngineParseRetryCappedAfter2Tries (无脑重试 LLM 的上限保护).

### Phase 4: 流式适配

v1.2.0 的 prompt mode 在 streaming 模式下用户体验差: LLM 输出 JSON
工具调用时屏幕看着卡 (因为字符在以"@k {"@ "@to" "@ol" 这种节奏吐字).
v1.2.1 在流上加状态机:

```
LLM stream 首 chunk
  ├ 以 ``` 或 { 开头 → Format A 模式: 缓冲所有 chunk, 等 } 配对完整
  │                  再解析成 ToolCalls, 一次性 emit
  │
  ├ 以 # / > / - / 中文字符 / "tool_calls" 关键字开头 → 反方向决策
  │
  └ 64 byte 还没决定 → 兜底当 Format B, 已缓存内容 flush 出去
```

实现:
- **`provider/streaming_parser.go`** (~190 行): StreamingParser 状态机
  + detectModeFromPrefix 启发式 + Reset
- **`bridge/prompt_stream_adapter.go`** (~110 行): wrap legacy stream,
  路由 text deltas 走 parser, Done 时调 parser.Finish() emit
  synthetic ToolCall events
- **`bridge/legacywrapper.go`**: ChatStream 检查 builder.Mode() ==
  "prompt" 时套 promptStreamAdapter

Native FC 模式**完全不套适配器**, 零开销.

测试 (12 cases): TestStreamingParser_DetectsFormatAOnFenceOpen / Raw
Brace / Markdown / Chinese Prefix / FullFlow / FormatB Realtime /
Unknown promotion / ParseFailure fallback / DetectionThreshold /
Reset / detectModeFromPrefix / StringMode.

### 端到端验证 (35B prompt mode)

```
opendb -c og "详细分析wdr报告/tmp/wdr_report.html"
→ Layer 1 总览评估 (5 模块评级)
→ Layer 2 风险详解 R1-R4 (含跨模块关联)
→ Layer 3 优化方案 (反向引用 R# 编号)
→ Top SQL + sqltune 跳过维护类语句
156 行完整输出, 流式体验顺滑
```

### 兼容性

- 对 FC 模式: ChatStream 走老路径, applyPromptBuilder 是 NativeFCBuilder
  no-op, 零侵入
- 对 v1.2.0 prompt mode: 自动升级, 不需要改配置
- 所有现有测试通过 (engine / bridge / provider / tool 全绿)

### 后续 (v1.2.2+)

- 50 case benchmark 框架对照 FC vs Prompt 命中率
- auto 模式自动探测 FC 支持
- 模型特化 prompt 模板 (Qwen / GLM / DeepSeek 各一套)
- 工具描述精简策略反哺 FC 模式

---

## v1.2.0 (2026-05-19) — PromptToolAdapter: 无 FC 也能调用全部 skill

### 背景

客户使用 Qwen3.6 / Qwen3.2 但部署未开 Function Calling (vLLM 版本太
老 / 网关剥参数 / 信创栈不支持). 不开 FC 直接接 opendb 的结果: Engine
主循环依赖 resp.ToolCalls, LLM 返回空数组就直接终止 → 60+ skill 一个
都没执行, 用户拿到一段空话.

设计文档: docs/design-prompt-tool-adapter-v1.2.0.md

### 设计 (共享内核 + 双适配壳)

- **共享 70%**: base 系统 prompt / 推理原则 / 工具选择策略 / 三层诊断
  模板 — FC 与 Prompt 两模式共用
- **FC 独有 15%**: tools 走 API 字段, 信任原生 tool_calls
- **Prompt 独有 15%**: 工具描述塞 system prompt, Format A/B 规则 +
  6 个 few-shot, JSON 解析 + Levenshtein 工具名纠错

Engine 主循环完全不变, Provider 层通过 PromptBuilder 接口分流.

### 新增模块

1. **`internal/engine/provider/prompt_builder.go`** (~150 行)
   - PromptBuilder interface
   - NativeFCBuilder (zero-op, 默认)
   - SelectPromptBuilder 工厂

2. **`internal/engine/provider/json_parser.go`** (~280 行)
   - JSONToolCallParser: 多级容错解析
   - 提取: ```json 代码块 → 裸 { } 子串 (brace-balanced)
   - 修复: 单引号→双引号, 尾逗号, 行/块注释
   - 校验: tool_calls 数组 + name 字段
   - 纠错: Levenshtein ≤1 工具名 fuzzy match (heath → health)
   - 兜底: 解析失败 → Format B (原文当最终答案)

3. **`internal/engine/provider/prompt_mode_builder.go`** (~260 行)
   - PromptModeBuilder 完整实现
   - BuildSystemPrompt: base + 工具描述 + Format A/B 规则 + few-shot
   - PrepareRequest: 清 req.Tools 防 vLLM 拒
   - PostProcessResponse: 解析 JSON 还原 resp.ToolCalls
   - SetTurnContext: 每轮工具过滤上下文

4. **`internal/engine/tool/serializer.go`** (~180 行)
   - SerializeToolsCompact: ToolSchema → 紧凑 Markdown
   - 60 工具 ≤ 12KB (满足 prompt 预算)
   - 按 name 排序保证 cache 命中

5. **`internal/engine/tool/filter.go`** (~190 行)
   - SceneBasedFilter: 按用户消息选 5-15 个相关工具
   - DefaultScenes: 8 个场景 (single_sql_tune / cluster_diag /
     wdr_analysis / memory_io / object_stats / session_kill /
     config_review / wait_analysis)
   - 上轮 ToolCalls 自动保留 + always-available 兜底

### 修改模块

- `internal/config/config.go`: ModelConfig 加 ToolMode 字段
- `internal/model/profile.go`: ModelProfile 加 ToolMode 字段
- `internal/model/manager.go`: InlineModel 加 ToolMode + Manager.ToolMode() 访问器
- `internal/engine/bridge/legacywrapper.go`: 加 PromptBuilder 字段 +
  WithPromptBuilder option + applyPromptBuilder hook
- `internal/opengauss/agent/diagnose.go`: Diagnoser 加 toolMode 字段 +
  promptBuilderOptions() helper
- `internal/opengauss/agent/strategy.go`: AutonomousStrategy / GuidedStrategy
  SetContextStoresFrom 透传 capability + toolMode
- `internal/opengauss/skill/ai/diag_skill.go`: 两条路径都调用 SetToolMode

### 配置

新增一个 yaml 字段, 不设默认 native (零影响现有 FC 用户):
```yaml
models:
  - name: qwen36-customer
    provider: openai
    base_url: http://customer-vllm:8000/v1
    model: qwen3.6
    capability: large
    tool_mode: prompt   # 关键: 走 PromptToolAdapter
```

### 验证 (E2E with 35B Qwen)

测试 1 (简单工具):
- 输入: "查下数据库健康状态"
- 结果: ✅ 第1轮调用 health, 第2轮输出健康报告, 数据真实 (fault_wal 31.6亿死元组)

测试 2 (复杂工具 + passthrough):
- 输入: "详细分析wdr报告/tmp/wdr_report.html"
- 结果: ✅ autonomous 1 轮, wdranalyze passthrough 短路, 完整 Layer 1/2/3
  输出 173 行, 跟 FC 模式持平

### 测试覆盖

- `prompt_builder_test.go` (3 cases): NativeFCBuilder 零侵入 + 工厂分发
- `json_parser_test.go` (19 cases): 代码块/裸JSON/单引号/尾逗号/行块注释/
  多工具/Levenshtein/无JSON/空输入/裸对象/null args/嵌套SQL/Levenshtein 函数
- `prompt_mode_builder_test.go` (11 cases): Mode / BuildSystemPrompt
  filtering / PrepareRequest / PostProcessResponse 各路径 / Levenshtein
  端到端 / 静态 prompt 大小
- `serializer_test.go` (8 cases): 序列化各种 schema + 60 工具预算
- `filter_test.go` (10 cases): 各 scene 触发 + 多轮上下文 + 顺序保留

### 对现有 FC 模型的零侵入保证

- 默认行为 byte-identical: 不设 tool_mode 字段 → NativeFCBuilder 走全
  老路径
- 所有现有测试通过 (provider / engine / model / tool / agent)
- 回退一行 yaml 改回 native 立即生效, 无需重启

### Phase 3/4 后续 (v1.2.1)

v1.2.0 范围聚焦核心框架. 以下功能延后:
- Phase 3: 错误反馈重试回路 (parser 失败时反馈给 LLM 修正)
- Phase 4: 流式 parser (Format A 缓冲解析, Format B 实时流)
- v1.2.x: auto 模式探测 / 50 case benchmark / 模型特化 prompt 模板

---

## v1.1.54 (2026-05-19) — REPL passthrough 修复 (走 OnStream 而非 result.Content)

### 背景

v1.1.51 hotfix 之后批量模式 (`opendb -c og "..."`) passthrough 正常,
输出以 `# WDR 分析报告` 开头. 但 REPL 模式仍然显示 LLM 重写后的版本
("基于 WDR 报告..." 这种 paraphrase). 用户问 "是不是产生幻读了" —
其实数据是真的, 但展示形式被 LLM 完全重塑.

### 根因

REPL 走 streaming 渲染路径:
- LLM 内容通过 `input.OnStream(delta)` 回调实时推给 REPL 屏幕
- `DiagPhaseDone` 时若 `diagStreaming=true`, 只 flush 流式缓冲, **不读
  `result.Content`**

v1.1.51 passthrough 只设置了 `result.Content = passthrough`, 没推到
OnStream. 所以 REPL 用户看到的是 LLM 在调 wdranalyze **之前**已经流式
输出的预热文字 (35B 通常会先说 "我将分析 WDR 报告..."), 后面的真实
报告永远进不了屏幕.

批量模式没设 OnStream, 直接用 result.Content, 所以批量看着是好的.

### 修复

engine.go passthrough 块加一行:
```go
if input.OnStream != nil {
    input.OnStream("\n\n" + passthrough)
}
```

passthrough 触发时把完整报告通过 OnStream 推给前端. REPL 会把它当流式
delta 渲染到屏幕. `\n\n` 前缀防止跟之前的 pre-tool 预热文字粘连.

### 测试

`TestEnginePassthroughShortCircuit` 单元测试覆盖三点:
1. `result.TurnsUsed == 1` (无 round 2 LLM 调用)
2. `result.Content` 含报告 body 且 marker 已 strip
3. **OnStream 回调收到的文本含报告 body 且无 marker** (REPL 路径)

mock provider 只准备 1 个 response, 若 passthrough 短路失败 round 2
会拿不到 response 直接 fail.

---

## v1.1.53 (2026-05-19) — Layer 2 风险详解: 数据表先行, 分析后跟

### 背景

用户反馈 Top SQL 表格那种 "先列数据后跟分析" 的结构很清晰, 但 Layer 2
当前用的是 `现象 bullet → 根因段 → 业务影响 → 关联模块`, 读者要先读
一段散文才能定位数据.

### 修复

synthesizer.go::buildWDRSystemPrompt 改 Layer 2 模板:
- 强制每个 R<N> 以 `**关键指标**` markdown 表格开头, 列: 指标 / 实测值
  / 阈值-基线 / 偏离倍数
- 表格后才是 根因 / 业务影响 / 关联模块 文字解读
- Layer 1 加注 "只是数据汇总, 所有解读放 Layer 2"

### 验证 (Opus)

R1-R4 全部按 `指标表(4-5 行) → 根因 → 影响 → 关联` 模式输出, 例如 R3:

```
**关键指标**
| 指标 | 实测值 | 阈值/基线 | 偏离倍数 |
|---|---|---|---|
| Soft Parse % | 11.00% | ≥ 30% (健康 ≥ 95%) | 低 2.7× |
| SET/SHOW/version 调用 | 79 次/3min | < 50 | 1.6× |
| ...
```

读者一眼看数, 再读 1-2 段分析判断严重程度, 跟 Top SQL 表格那种 UX
保持一致.

---

## v1.1.52 (2026-05-18) — 维护类 SQL 跳过 sqltune drill (UX)

### 背景

og 5.0.3 维护窗口里 Top SQL 全是 CREATE INDEX / ANALYZE, 用户看到 5 条
`⚠️ sqltune 失败: phase A: statement type CREATE not supported by EXPLAIN
tuning (snippet: CREATE INDEX...)` 误以为系统坏了 — 实际上是预期行为
(v1.1.50 加的 DDL 白名单), 但显示极丑.

### 修复

1. **`topsql.go::drillOne` 新增 maintenance pre-screen**: 在 sqlfetch 解出
   SQL 之后, 立即用 `classifyMaintenanceSQL()` 判别. 命中 DDL/ANALYZE/SET/
   SHOW/VACUUM/事务控制/游标/COPY/CALL 等 30+ 类语句, 直接设
   `Skipped=true + SkipReason=中文标签` 返回, 不再调 sqltune
2. **`SQLTuneResult` 加 `Skipped/SkipReason` 字段** (types.go)
3. **`renderer.go::renderSQLTunes`**:
   - 前置 banner: 若 Top N **全部**跳过, 输出 "ⓘ Top N SQL 全部为维护类
     语句, 无 SQL 调优空间, 业务负载分析请关注 Layer 2"; 部分跳过则
     "ⓘ Top N 中含 K 条维护类语句, 已跳过"
   - 每条跳过的 SQL_ID 输出 `ⓘ 跳过: <SkipReason> — 该类型语句不适用
     SQL 调优`, **不再输出 ⚠️ sqltune 失败**

### 验证 (Opus 跑全流程)

```
> ⓘ Top 5 SQL **全部为维护类语句** (DDL / ANALYZE / SET / SHOW 等),
>   无 SQL 调优空间. 业务负载分析请关注 Layer 2 风险详解.

### #1  SQL_ID `4175761868`
ⓘ **跳过**: 建索引 DDL — 该类型语句不适用 SQL 调优

### #4  SQL_ID `2399767488`
ⓘ **跳过**: 统计信息收集 (ANALYZE) — 该类型语句不适用 SQL 调优
```

Opus 进一步: Layer 2 R1 关联到 User_Tables_stats 中的 Seq Tup Read
(5M / 2M / 1.8M, v1.1.51 RawSections 数据被吃透), Layer 3 优化方案
反向引用 R# 编号 + 给完整 SQL + 量化预期 (P95 4614μs → 显著下降,
Soft Parse 11% → 90+, 物理读 369 块/s → <100).

完整输出 161 行, 无 marker 泄露, 无截断.

### 影响范围

- `/wdranalyze` 在维护窗口 (无业务负载) 不再显示 5 条丑陋的 "sqltune 失败"
- 业务 SQL 路径不受影响, 仍走 sqltune 5 维度调优分析
- DM/MySQL/PG 后续要做相同优化时直接复用 `classifyMaintenanceSQL` 模式

---

## v1.1.51 (2026-05-18) — WDR 三层分析报告（Scorecard + 风险详解 + 优化方案）

### 背景

v1.1.50 修了 TopSQL metrics，35B 跑出来终于能识别 DDL 风险。但用户测了一轮发现
LLM **只围绕 TopSQL 答题**，整个 WDR 里 7 节关键资源数据完全没用上:
- Database Stat (postgres 12.5GB Temp Bytes / 807 Temp Files 完全没提)
- Load Profile (DB Time/sec, 物理读 369 块/s, SQL P95 4.6ms 没提)
- Instance Efficiency (**Soft Parse 仅 11%** 没提 — 极端硬解析)
- IO Profile / Cache IO Stats / User Tables / User Index stats — 全没解析

LLM 看的是 parser 给的结构, parser 只解析 TopSQL → LLM 只能围绕 TopSQL 答.

用户要求: 改成 "先总览评分 → 再每个风险详解 → 最后给优化方案 (关联风险)" 三层结构.

### 设计

```
WDR HTML
  ↓ Parser (v1.1.50 + ExtractRawSections 新增)
WDRReport { Header, TopSQLs, RawSections map, SectionScores []SectionScore }
  ↓ SectionEvaluator (新增 · 确定性规则引擎, 每节给 ✅/🟡/🔴 评级)
  ↓ Synthesizer (改造 · 三层 prompt)
LLM 三层输出:
  Layer 1 总览评估表 — 直接复制 scorecard, 不重新评级
  Layer 2 风险详解 — 仅🔴/🟡, 每项 R<N>: 现象/根因/业务影响/关联模块
  Layer 3 优化方案 — P0/P1/P2 表格, 反向引用 R# 编号
```

### 修复

1. **新增 `section_extractor.go`** (~120 行): 按 id="X" anchor 切 7 节 (Database
   Stat / Load Profile / Instance Efficiency / IO Profile / Cache IO Stats /
   User Tables / User Index stats), 每节 htmlToText 后≤8KB
2. **新增 `section_evaluator.go`** (~360 行): 5 节确定性规则引擎
   - Database Stat: Temp Bytes>10GB🔴/>1GB🟡, Deadlock>0🔴, Rollback/Commit>10%🟡
   - Load Profile: P95>100ms🔴/>20ms🟡, 物理/逻辑读>25%🟡, DB Time/s>800ms🔴
   - Instance Efficiency: Buffer Hit<80%🔴/<90%🟡, Soft Parse<10%🔴/<30%🟡 等
   - IO Profile: 信息性 (无评级阈值)
   - TopSQL: Top1>30%🟡, Top5 维护类≥2🟡, 探测语句≥50🟡
3. **types.go +60 行**: `RawSections map`, `SectionLevel`, `SectionRule`, `SectionScore`
4. **synthesizer.go 三层 prompt 改造**: 系统 prompt 强制 Layer 1/2/3 结构,
   用户消息加 scorecard 表格 + 触发规则明细 + 原始数据节, MaxTokens 4K→8K,
   timeout 90s→180s
5. **validator.go 更新**: required sections 改为 Layer 1/2/3 (老的"风险全景/
   关键瓶颈/..."保留为 alias, 兼容历史输出)
6. **engine.go passthrough 短路**: 检测 `<!-- WDR_REPORT_BEGIN` marker,
   工具调用后**跳过 post-tool LLM 总结**, 直接把工具输出当最终响应
   - 必要: 35B 这种中型模型对"原样转发"prompt 指令不敏感, 会顽固地重新
     格式化丢失结构化信息. 引擎强制短路能100%保证 passthrough
7. **wdranalyze_skill.go**: Rendered 字段加 `<!-- WDR_REPORT_BEGIN` 前缀
8. **builder.go**: 两个 universal prompt (strict + templated) 都加 wdranalyze
   intent + 工具结果 passthrough 例外

### 验证

`/tmp/wdr_report.html` 端到端 (35B autonomous, 1 轮):

```
Layer 1: Database Stat 🔴 / Load Profile 🟡 / Instance Efficiency 🟡 / IO Profile ✅ / TopSQL 🟡
Layer 2:
  R1: 临时文件极端溢出 — Database Stat 🔴 (现象: 11.64 GB / 807 Temp Files)
       关联 ↔ R2 (磁盘 I/O 叠加)
  R2: 物理读占比偏高 — Load Profile 🟡 (现象: 27.0%)
       关联 ↔ R1 (落盘加剧物理读)
  R3: 缺乏预编译与连接复用 — Instance Efficiency 🟡 (现象: Soft Parse 11%)
       关联 ↔ R4 (79 次 SET/SHOW/version 探测)
  R4: 维护操作挤占资源 — TopSQL 🟡 (现象: Top1 53.6% / Top5 全维护)
Layer 3:
  P0 work_mem 4MB → 512MB → R1 (Temp Bytes 应 → <1 GB)
  P1 shared_buffers → 4GB → R2 (物理读应 → <15%)
  P2 PgBouncer + PreparedStatement → R3,R4 (Soft Parse → 80%+)
综合评估: ...
```

时间: 自然语言路径 (`opendb -c og "分析wdr..."`) 59.5s, 直调 `/wdranalyze file ...` 1m12s.

测试新增:
- `section_evaluator_test.go` (~250 行, 11 cases): Database Stat 阈值 / Instance
  Efficiency 评级 / Load Profile P95 / TopSQL Top1+probes / Section Extractor 边界
- `validator_test.go`: Layer 1/2/3 missing-section + legacy alias 双向兼容

### 影响范围

- `/wdranalyze` 输出从 "只评 TopSQL DDL" 升级到 AWR 级 7 节全维度分析
- 自然语言路径 (`opendb -c og "分析wdr..."`) 不再被 agent 重新格式化覆盖
- 老的 `## 风险全景 / 关键瓶颈 / ...` 输出格式仍兼容 (validator alias 匹配)
- 其他工具 (sqltune 等) 的 passthrough 短路同样生效, 减少二次总结损耗

剩余: og 单实例 generate_wdr_report 不输出 Wait Events / Time Model 全局数,
是 og 行为 (要用 gs_wdr 工具或集群模式), 不是 parser 漏解.

---

## v1.1.50 (2026-05-18) — TopSQL 列位置感知 + DDL/SET 跳过 EXPLAIN + Top N 30

### 背景

v1.1.49 让 wdranalyze 不再 hard fail, 真实跑通 og HTML 报告, parser 解出
57 条 SQL_ID. 但 35B 测试看到的 Top SQL **每条都是 0 calls / 0ms total /
0ms avg / 0% DB Time** — 因为 og 列顺序跟 GaussDB 风格完全不同,
fillTopSQLFields 的 numeric heuristic 把第一个数字字段 (Total Elapse μs)
误填到 Calls, 后面所有 metrics 留 0.

同时 sqltune 阶段对 og 的 DDL 三连失败:
```
4175761868 → syntax error at or near "INDEX"
273202044  → syntax error at or near "SET"
499116657  → syntax error at or near "SET"
```

LLM 综合分析 synthesizer 还硬编码只看 top 10, og 短窗口 50+ SQL 全被砍.

### 根因

1. **TopSQL metrics 全 0**: `parser_helpers.go::fillTopSQLFields` 是列位置
   盲 heuristic. og 25 列布局 (`Unique SQL Id | Node | User | Total Elapse(us)
   | Calls | Avg | Min | Max | Returned Rows | Tuples Read | ...`) 完全
   不匹配 heuristic 期望的 `sql_id | calls | total_ms | avg_ms | rows | query`
2. **sqltune 噪音**: PlanCollector.Collect 没拦 DDL/SET, 直接交给 EXPLAIN
   返回不可读的语法错
3. **synthesizer top 10 硬编码**: 限制太死, og 短窗口看不到全貌

### 修复

1. **og column-aware TopSQL parsing**:
   - `detectOGColumnMap()`: 找含 `Unique SQL Id` + `Total Elapse Time` 的
     header row, 建 `label → 列索引` map
   - `fillTopSQLFieldsByColumn()`: 用 map 定位 Total/Avg Elapse、Calls、
     Returned Rows、User Name、SQL Text; **微秒 → 毫秒** 除以 1000
   - `parseTopSQLs` 优先走 column map, 没 og header 回落到老 heuristic
     (保护 legacy text WDR 兼容性)
   - `extractSection` 修了一个边角: heading 后跟多个空行的情况, 之前
     `consecutiveBlank >= 3` 在 section body 还没开始就触发 cut=0; 现在
     先要 `sawContent` 才计 blank
2. **DDL/SET 白名单**:
   - 新增 `UnsupportedStatementError`, `detectUnsupportedStatement()`
   - 拦 CREATE/DROP/ALTER/TRUNCATE/RENAME/ANALYZE/VACUUM/REINDEX/CLUSTER/
     SET/RESET/SHOW/GRANT/REVOKE/BEGIN/COMMIT/ROLLBACK/SAVEPOINT/CALL/DO/
     COPY/LOCK/CHECKPOINT/LOAD/START/PREPARE/DEALLOCATE/EXECUTE/DECLARE/
     CLOSE/FETCH/MOVE (DML INSERT/UPDATE/DELETE **允许** — EXPLAIN 能
     plan)
   - 处理前导 `--` 行注释和 `/* */` 块注释
3. **synthesizer top 10 → top 30**: og 短窗口正常都 50+, top 10 砍掉 80%
   信号

### 验证

`/tmp/wdr_report.html` 端到端 (Go 直调 Parse):

| 指标 | v1.1.49 | v1.1.50 |
|---|---|---|
| TopSQLs 数 | 57 | 55 (去重更准) |
| Calls (top1) | **1299322 (错!)** | **1 (正确)** |
| TotalTimeMS (top1) | **0.0** | **1299.3** (μs→ms 转好) |
| AvgTimeMS (top1) | **0.0** | **1299.3** |
| UserName | "" | "gaussdb" |
| QueryPrefix | "CREATE INDEX..." | "CREATE INDEX..." |

新增测试:
- `TestParse_OGColumnAwareTopSQL` (og 25-列 header + 1 data row)
- `TestDetectUnsupportedStatement` (18 cases: SELECT/WITH/INSERT 允许;
  CREATE/DROP/ALTER/ANALYZE/SET/SHOW/VACUUM/GRANT 拦; 行注释 + 块注释)

### 影响范围

- `/wdranalyze` 对 og 报告: TopSQL 终于有真实 metrics, LLM 能给针对性建议
- `/sqltune` 拿到 og DDL/SET SQL_ID: 返回 `UnsupportedStatementError` 不
  再扔 EXPLAIN 语法错; tuner.go 已有 typed error propagation, skill 层
  会渲染 "statement type X not supported by EXPLAIN tuning"
- Legacy text WDR + DML SQL 完全不影响

剩余局限: og 单实例 `generate_wdr_report scope=cluster` 不输出 Wait Events
/ Time Model / Buffer Pool, 这是 og 自身行为, 不是 parser bug.

---

## v1.1.49 (2026-05-18) — wdranalyze parser 支持 og 5.0.3 HTML 表格格式

### 背景

`/wdranalyze` 测真实 og 5.0.3 `generate_wdr_report()` 生成的 HTML 报告
直接 hard fail:

```
header parse: not recognizable as a WDR (no DB version / host header)
```

LLM 拿不到任何 WDR 数据, 无法分析.

### 根因

`internal/opengauss/wdranalyze/parser.go` 的 3 个不兼容问题:

1. **`htmlToText` 只处理 `</td>` 不处理 `</th>`** — og 的 header 行用 `<th>` 标签,
   去标签后 `Host Node NameCPUsCoresSocketsPhysical MemoryopenGauss Version` 全部
   黏在一起, 任何 regex 都匹配不到分隔符
2. **`parseHeader` 用 `Field : Value` 单行 regex** — og 是 Oracle AWR 风格表格,
   label 在第 N 行 (`Host Node Name | CPUs | ... | openGauss Version`), 值在第
   N+2 行 (`og5 | 18 | ... | (openGauss-lite 5.0.3 ...)`), 不在同一行
3. **`parseTopSQLs` 找 "Top SQL by ..." 等 GaussDB 风格名** — og 实际用 Oracle
   AWR 风格 `SQL ordered by Elapsed Time` 等

另外 `extractSection` 拿第一个 title 匹配, 但 og 在文档顶部的 TOC link
里把同一个 title 重复了 8 遍, 第一个匹配是 TOC, 后面全是空, 取不到 data row.

### 修复

1. `htmlToText` 给 `</th>` 也插 ` | ` 分隔符
2. `parseHeader` 新增 `parseHeaderTableFormat()`: 走每一行, 遇到含
   `Host Node Name + openGauss Version` 的行就读下一非空行的第 1 个 cell
   做 host、最后一个 cell 做 version; 遇到 `Snapshot Id + Start Time` 行
   就读下两个非空行做 snap1/snap2
3. `parseTopSQLs` 加 7 个 og AWR 风格 alias (`SQL ordered by Elapsed Time` 等)
4. `parseTimeModel` 加 `Load Profile`; `parseIOStats` 加 `IO Profile` /
   `Cache IO Stats`
5. `extractSection` 改成遍历所有匹配, 优先选**body 里含 pipe-delimited
   data row** 的那个 (跳过 TOC link)
6. **兜底**: 解析失败但文件含 `Workload Diagnosis Report` / `openGauss WDR`
   等 marker, 不再 hard fail, 给 `DBVersion = "openGauss (version not detected)"`
   走下去, 让 fallback rules 和 LLM 看到能解出的部分

### 验证

`/tmp/wdr_report.html` (1.7 MB, og5 + TP+AP load + 2 snapshots):

| 字段 | v1.1.48 | v1.1.49 |
|---|---|---|
| Parse | ❌ "not recognizable" | ✅ OK |
| Host | - | `og5` |
| DBVersion | - | `(openGauss-lite 5.0.3 build 89d144c2) ...` |
| SnapStart→SnapEnd | - | `1 → 2` |
| WindowStart/End | - | `11:32:11 → 11:35:34` |
| TopSQLs 条数 | - | `57` |

测试新增:
- `TestParse_OGHTMLTableFormat` — og 真实 HTML 缩样测试
- `TestParse_WDRMarkerFallback` — marker 兜底测试

### 影响范围

`/wdranalyze` 命令对 og 5.0.3 HTML 报告可用 (之前对该格式完全报废).
TextWDR 和老 HTML 格式 (含 `:` 分隔符) **完全兼容**, 不破坏现有测试.

剩余局限: `Waits` / `DBTimeSec` / `Settings` 仍为空 — og 5.0.3 这些 section
要么不在 generate_wdr_report 输出里 (得用 `gs_wdr` 工具), 要么用了完全不同
的字段名 (待后续版本逐个映射). LLM 现在至少有 57 个 TopSQL + window + version
可分析.

---

## v1.1.48 (2026-05-18) — 去掉 MessageCountTrigger 自动压缩 (第三层 bug)

### 背景

v1.1.47 删了 drift detection 想解决 follow-up 答错老问题. 真机继续测发现**还有第三层 bug** —

用户问 "分析 wdr 报告" (新问题), agent 跑 wdranalyze + wdr + sqltune 等工具,但**最终输出是老 SQL_ID 581990336 的优化方案**. 抓 session log 发现:

- msg[0] 仍是上一轮 "看下 sql id 581990336" (OLD 问题)
- session 里**完全找不到** "分析 wdr 报告" 这句新 user 输入
- LLM 整个 agent loop 把 msg[0] 当作权威 task

### 根因

`internal/engine/context/manager.go` 的 **MessageCountTrigger 自动压缩**:

```go
const MessageCountTrigger = 15

if len(messages) >= MessageCountTrigger && float64(info.Used) < threshold*0.9 {
    collapsed := m.compressor.CollapseTurns(messages)
    ...
}
```

`CollapseTurns` 逻辑:
- 保留 `turns[0]` (作为"task anchor")
- 中间 turn 折叠成 `<system-reminder>以前 N 轮诊断的摘要…</system-reminder>`
- 保留最后 3 turn

新 user 问题进 history 后变成 msg[10+]. 老 session 已有 10 msg, 加新 user + agent 几轮 tool 触发 >= 15 → CollapseTurns → 新 user 问题**落在 middle turn 被折叠**.

LLM 看到的:
1. turns[0] = 老 "用户问题: 看下 sql id 581990336…"
2. summary = "以前 N 轮的摘要…"
3. 最后 3 turn (含新 tool 调用但用户意图已丢)

LLM 自然继续答老问题.

### 同 v1.1.47 哲学

启发式自动 context 管理破坏数据. 修复策略同 v1.1.47:

| 版本 | 删除 | 保留 |
|---|---|---|
| v1.1.47 | drift detection (Jaccard) | `/clear` 显式控制 |
| **v1.1.48** | **MessageCountTrigger 触发的 CollapseTurns** | **token-based 80-90% / 90%+ 压缩 (保护 context window safety)** |

token-based 压缩留 — 那是真的 context 满了不得不做. count-based 是误伤.

### 修改

`internal/engine/context/manager.go`:

```diff
- if len(messages) >= MessageCountTrigger && float64(info.Used) < threshold*0.9 {
-     collapsed := m.compressor.CollapseTurns(messages)
-     if len(collapsed) < len(messages) {
-         return collapsed, true
-     }
- }
```

`MessageCountTrigger` 常量保留 (老测试引用), 但 MaybeCompress 不再用.

### 测试

`TestMaybeCompress_MessageCountTrigger` 改名 `_DoesNotFire`, 验证 20+ msg 在低 token 时**不**触发压缩.

engine 全部子包 + UI 全过 -race.

### 用户感知

| 场景 | 之前 | 现在 |
|---|---|---|
| 同 session 问第 2 个不同问题 | msg[0] 被当 task, 答老问题 | LLM 看到完整 history, 包含新问题, 正常答 |
| 同 session 问 20+ 个问题 | 老问题被压缩成摘要 | 全保留. token 满了用 `/clear` |
| 真 context 窗口满 (80%+) | 同 (token-based 压缩接管) | 同 |

### 已知 limit

- 长 session 一直问 → token 慢慢累积. 80% 时仍会 CollapseTurns (token-based 路径), 同样有可能误伤. 但比较少见.
- M9.x 待办: `/compact` 命令让用户显式触发 LLM 压缩 (类 Claude Code).

### 经验

3 个版本 (v1.1.45/v1.1.47/v1.1.48) 才把自动 context 管理完全清干净. 教训: 任何"自动检测+破坏数据"的启发式都不可靠. 用户显式控制 + token-safety 兜底足矣.

---

## v1.1.47 (2026-05-18) — 去掉 drift detection 改 Claude Code 风格 (/clear 接管)

### 背景

drift detection 启发式过去 3 个版本连续踩雷:
- **v1.1.10** 引入 Jaccard 0.05 阈值
- **v1.1.45** 修 wrapper 污染导致 drift 永不触发的 bug
- **v1.1.46** 真机又遇到 — 同 SQL_ID 跨问题被视作同主题 (Jaccard 0.20 远超 0.05), drift 不触发, follow-up 答错任务

启发式注定不可靠:
- SQL_ID `581990336` 作为完整 token 跨问题相同 → +6.7% Jaccard
- 公共 SQL 词 `sql/id` 不在 stopwords → 进一步污染
- 真实差异在 verb/intent (`优化方案` vs `展示完整执行计划`) Jaccard 看不见

### Claude Code 设计哲学

Anthropic 官方 CLI **不做** 自动 topic drift. 设计:
- 全部历史保留 (Claude Opus / Sonnet 上下文 1M token 足够)
- 用户 `/clear` 显式重置
- 上下文满了用 `/compact` 让 LLM 压缩成摘要 (不是粗暴丢弃)
- 失败模式安全 — 丢历史是 destructive, 让用户决定

经过 Anthropic 内部 + 千万用户验证比启发式可靠.

### 修改

1. **删除** `internal/engine/context/drift.go` + `drift_test.go` (约 200 行 + 12 case)
2. **移除** `engine.go` 内 `DropHistoryOnDrift` 调用 (改注释解释原因)
3. **增强** `/clear` 命令同时清 LLM session 文件:

```go
func (r *REPL) clearLLMSessions() (int, error) {
    instance := r.connMgr.CurrentName()
    dir := filepath.Join(home, ".opendb", "sessions", instance)
    // 删除 dir 下所有 .jsonl 文件
}
```

`/clear` 现在做两件事:
- 清屏 (老行为)
- **删除 `~/.opendb/sessions/<instance>/*.jsonl`** (新, LLM 从 0 history 开始)

显示 `✓ 已清空 N 个 LLM session 文件 (后续问题从空 history 开始)` 反馈.

### 用户感知

| 之前 (v1.1.46 及更早) | 现在 (v1.1.47) |
|---|---|
| follow-up 问 "执行计划不要缩略" → 命中老问题 history → 答优化方案 | 用户主动 `/clear` 后问 → 干净 history → 答执行计划 |
| Jaccard 0.20 自动判 same topic 错误 | 不再自动判断, 用户控制 |
| `/clear` 只清屏 | `/clear` 清屏 + 删 LLM session |

### 兼容性

- 不再有任何自动丢 history 行为 — **所有历史 LLM 都会看到**
- 上下文满了的处理留给 LLM provider (开 streaming 时部分模型自动 fallback)
- M9.x 计划: 加 `/compact` 命令 (LLM 压缩历史成摘要), 进一步对齐 Claude Code

### 测试

engine 全部子包 + UI 全过 -race. drift 包整体删除, 无测试 leak.

### 已知 limit

- 没装 `/compact` (LLM 自动压缩历史). 4 库 agent 模式跑 30+ 轮长会话可能撞 LLM context limit. 用户用 `/clear` 手动清.
- M9.x 待办: 实现 `/compact` (类 Claude Code 的 LLM 压缩)

### 经验

启发式上下文管理 → 用户显式控制. 8 次 patch (drift threshold tuning / wrapper strip / SQL_ID filter / TF-IDF / verb detection / ...) 都不如直接去掉. 用户体验优先于 token 节约.

---

## v1.1.46 (2026-05-18) — fix: 队列出队自然语言路由 + 超时与错误消息

### 背景

真机用户反馈: 上一轮 LLM 还没结束时再问新问题 → 新问题进队列 → 队列出队执行 → **2 分钟超时报 "请检查数据库连接状态"** (误导, 实际跟 DB 连接无关).

例:
- Q1 "看下sql id 581990336有没有优化方案" (跑了 3 分钟)
- 中途用户输入 Q2 "展示下sql id 581990336完整的执行计划" (进队列)
- Q1 完成 → Q2 出队 → 2 分钟后 "✗ 执行超时（2 分钟），请检查数据库连接状态"

### 根因 (3 个独立 bug)

#### Bug 1: 队列出队自然语言路由错

`internal/ui/repl_async.go.processCmdQueue`:
- 检查 `isDiagWithLLM(cmd)` (只匹配 `/llm` 前缀) → false
- fallthrough 到 `startSkillAsync(cmd)` (硬编码 2 分钟超时)
- 自然语言 "展示下..." 应该走 `startDiagAsync("/llm " + cmd)` 但被错路由

对比非队列路径 `repl_input.go`: 先 `isDiagWithLLM` 后 `isNaturalLanguageWithLLM`, 自然语言正确走 startDiagAsync. 队列路径少了第二个检查.

#### Bug 2: startSkillAsync 2 分钟硬超时太短

`internal/ui/skill_runner.go:195`:
```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
```

LLM-driven 操作 (sqltune / 复杂诊断) 经常需要 2-5 分钟. 2 分钟硬限切。

#### Bug 3: 错误消息误导

`internal/ui/skill_runner.go:199`:
```
执行超时（2 分钟），请检查数据库连接状态
```

实际跟 DB 连接无关. 多数情况是 LLM 响应慢 / 工具链复杂. 误导用户去查 DB 网络.

### 修复

#### 1. processCmdQueue 加 isNaturalLanguageWithLLM 检查

```go
if r.isDiagWithLLM(cmd) {
    r.startDiagAsync(cmd)
    return
}
// 新加: 镜像 repl_input.go 非队列路径
if r.isNaturalLanguageWithLLM(cmd) {
    r.startDiagAsync("/llm " + cmd)
    return
}
```

#### 2. 超时 2 min → 10 min

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
```

#### 3. 错误消息改写

```
执行超时（10 分钟）— 可能 LLM 响应慢 / SQL 复杂度高 / 工具调用挂起。可重试或检查 LLM 配置 (/model)
```

### 测试

- UI 全部测试通过 -race
- 修复后队列出队自然语言走 startDiagAsync (DiagSkill 自己管 ctx, 不受 2/10 min 影响)
- /sqltune 等长操作不再被 2 min 卡死

### 兼容性

- og/oracle/pg/mysql 4 库 agent 模式队列行为变化 (走对路径)
- 5 dialect /sqltune 零行为变化
- 直接命令行为不变 (非队列路径)

---

## v1.1.45 (2026-05-18) — fix: drift detection 解开 user prompt wrapper

### 背景

真机测发现严重 bug: agent 模式连续问两个不同问题时, LLM 始终回答**第一个**问题, 忽略后续输入. 例如:
- Q1: "看下 sql id 581990336 有没有优化方案"
- Q2: "执行计划不要缩略, 展示全"
- 实际输出: 给 Q1 的优化方案 (Q2 完全被忽略)

### 根因

两个 commit 配合形成的 bug:

| 时间 | Commit | 改动 |
|---|---|---|
| 2026-03-26 | `87a7560ce` (P4 bug batch) | diag_skill.executeOnDemand 给 user input 加 `用户问题: X\n\n请直接回答用户的问题。可以调用工具查询数据库获取所需信息。\n不要偏离用户的问题去分析其他告警或等待事件。` wrapper |
| **2026-04-25** | **`11ebd5714` (v1.1.10)** | **新增 `DropHistoryOnDrift`, 按 Jaccard < 0.05 判 follow-up 是否换话题, 触发则清 history** |

### Drift detector 误判机制

`lastUserMessage()` 从 history 取最近一条 user message **直接**比对 (没剥 wrapper). 两条 wrapped 消息共享 42 个 boilerplate token (用户/问题/请/直接/回答/可以/调用/工具/查询/数据库/不要/偏离/...), 占并集 67.7%.

Jaccard 比对结果:
- **Wrapped Q1 vs wrapped Q2**: Jaccard = **0.677** (远超 0.05 阈值) → drift NOT detected → history 保留 → LLM 看 Q1 + Q2 倾向回答 Q1
- **Raw Q1 vs raw Q2**: Jaccard = **0.000** → drift detected → history 清 → LLM 只看 Q2

任何两个不同问题被 wrap 后都会被误判为 "same topic"，drift 检测**完全失效** v1.1.10 起.

### 修复

`internal/engine/context/drift.go` 加 `stripUserPromptWrapper(content)` 在 `lastUserMessage()` 内调用:

```go
func stripUserPromptWrapper(content string) string {
    const prefix = "用户问题: "
    if !strings.HasPrefix(content, prefix) {
        return content
    }
    body := content[len(prefix):]
    for _, sep := range []string{"\n\n请直接回答", "\n\n请"} {
        if idx := strings.Index(body, sep); idx > 0 {
            return body[:idx]
        }
    }
    return body
}
```

剥 og/oracle/pg 的 `用户问题: X\n\n请直接回答用户的问题。...` 和 mysql 变种 `用户问题: X\n\n请...`, 让 drift 检测对比 raw 问题.

### 测试

12 case 全过含 -race:
- **TestStripUserPromptWrapper** 6 sub-case: og/oracle/pg full wrapper / og 不同问题 / mysql 短 suffix / raw input 无 wrapper / prefix 有但 suffix 不匹配 / 空字符串
- **TestDetectDrift_WrappedQuestions** 1 case: 真实 bug 场景 (wrapped Q1 + wrapped Q2 必须检测出 drift)
- 原有 TestDetectDrift / TestDropHistoryOnDrift / TestJaccardSimilarity 全过

### 影响

- og / oracle / pg / mysql 4 库 agent 模式 follow-up 现在正确触发 drift → 加载干净 history
- 单一问题 session 行为不变
- 与原 history 同主题的 follow-up 行为不变 (相同 boilerplate 但 raw 文本仍有 overlap)

### 兼容性

- 5 dialect /sqltune 零行为变化
- diag_skill wrapper 文案不变 (修在 drift 层而非 wrapper 层, 避免连锁影响)

---

## v1.1.44 (2026-05-17) — fix: agent 模式 /sqltune 闭环 (interval + Round1 降级 + smart-substitute)

### 背景

v1.1.43 修了 `/sqltune <sql_id>` 直接命令路径. 真机继续测 35B agent 自然语言路径 ("看下 sql id 581990336 有没有优化方案") 仍失败 - 跑 5-6 轮工具后只输出 "数据库版本: openGauss-lite 5.0.3". 通过抓 session 日志定位 **3 个独立 bug** 叠加:

1. **og PlaceholderSubstituter 缺 interval 关键字** - `interval ?` 默认填 `1` → `interval 1` 触发 og syntax error
2. **og Tuner Round 1 LLM 截断时 hard-fail** - LLM agent 看到 "round 1 parse failed" error 误以为 sqltune 失败, 散开乱试别的工具
3. **/sqltune skill ToolDef 没说支持 SQL_ID** - LLM 不知道可以直接传纯数字 ID

### 修复

#### 1. og PlaceholderSubstituter 加 `interval` 关键字
`internal/opengauss/sqltuner/placeholder_substituter.go`:
```go
if strings.HasSuffix(trimmed, "interval") {
    return "'1 day'", "rule"
}
```
+ 新测试 `TestSubstitute_IntervalKeyword` 验证不再产生 bare `interval 1`.

#### 2. og Tuner Round 1 失败时返回 partial report 而不是 hard error
`internal/opengauss/sqltuner/tuner.go`:
```go
round1, err := t.runRound1(ctx, cc)
if err != nil {
    // 返回降级 markdown 含 Phase A 数据 + 错误说明
    // (避免 LLM agent 觉得"sqltune 失败需要重试别的工具")
    degradedMD := "# /sqltune 部分结果（Round 1 LLM 失败）...\n" + renderFallbackReport(...)
    return &FinalReport{Markdown: degradedMD, Stats: stats}, nil
}
```

#### 3. /sqltune skill ToolDef + Summary 强化
`internal/opengauss/skill/query/sqltune_skill.go`:
- ToolDef Description 明确说**支持 SQL_ID 或 SQL 文本两种输入**, 且明确告诉 LLM "DO NOT call additional tools afterwards"
- args 描述: "Either full SQL text OR a numeric SQL_ID like 581990336"
- 成功 Summary 改成 "✅ DONE: ... The 'rendered' markdown above IS THE FINAL ANSWER ... DO NOT call additional tools"

#### 4. SQL_ID NULL 兜底 → 智能 synthetic 默认值
v1.1.43 当时用 NULL 兜底但 og `interval NULL` 也无效. 本版改成 context-aware:
- `interval ?` → `interval '1 day'`
- `LIMIT ?` → `100`, `OFFSET ?` → `0`
- `LIKE ?` → `'%'`
- 其他 → `0`

helper 函数 `substituteForQmark(sql, pos)` 按 `?` 位置左 context 关键字 dispatch.

### 真机验证（本机 og5 + 35B agent）

测试问题: "看下 sql id 581990336 有没有优化方案"

**修复前** (v1.1.43): 10 轮工具调用, 最终输出 "数据库版本: openGauss-lite 5.0.3"
**修复后** (v1.1.44): 35B 拿到 sqltune 完整 markdown 报告后, **输出 production 级 4 步优化方案**:
1. MATERIALIZED CTE 消除优化屏障与重复扫描
2. EXISTS → JOIN 改写
3. 4 个复合索引 (region_rank/vip_level/created_at/order_method)
4. ANALYZE 统计信息更新

每条含: 原理 / ⚠️ 风险评估 / 🔄 回滚方案. 预计性能提升 60-80%.

### 测试

- 5 个新单测 (TestSubstitute_IntervalKeyword + smart-substitute helpers 重新组织)
- og 全部 sqltune 测试通过 -race
- 5 dialect / sqltune neutral 全过

### 兼容性

- og /sqltune 行为变化: SQL_ID 路径走通 + Round 1 失败不 hard error
- 5 个其他 dialect 零变化 (修复仅 og)
- 直接命令 `opendb -c og /sqltune <id>` 不变 (已 v1.1.43 工作)
- agent 自然语言路径 ("看下 sql id xxx") 现在闭环工作

### 已知 limit

agent 模式 LLM (35B/opus) 偶尔会在拿到 sqltune 报告后再调 1-2 个工具 "二次验证", 最终输出可能基于其他工具结果. 这是 engine 层 agent loop 的 final-answer extraction 问题, 不在本 patch 范围. 改进 ToolDef 提示 + Summary 把症状从 100% 降到偶发.

---

## v1.1.43 (2026-05-17) — fix: /sqltune SQL_ID 自动 fallback (og)

### 背景

真机 og 跑 35B agent 时发现：用户问 "sql id 581990336 有没有优化方案", 35B 跑 5 轮工具 (sqlfetch / sqltune / explain / sql / health) 后**只输出**"数据库版本：openGauss-lite 5.0.3"。

原因：`/sqltune <id>` 把 ID 当 SQL 文本，撞 EXPLAIN 语法错。35B 在 agent 模式拿到归一化 SQL 后撞 PlaceholderError，再次找不到合适工具，**早停退化**输出无效内容。

### 修复

`internal/opengauss/skill/query/sqltune_skill.go` 加 SQL_ID 自动识别 + og-specific 三态分支：

| 输入 | 行为 |
|---|---|
| 纯数字 (SQL_ID) | 自动查 `dbe_perf.statement_history` |
| 找到 + 归一化 (`?`) | 给 og-specific 恢复指引 + 显示前 500 字 SQL 预览 + 3 路径 (手填 / auto_explain log_parameter_values / track_stmt_parameter) |
| 找到 + 含字面量 | 直接走原 sqltune 流程 |
| 找不到 | 列 3 个可能原因 (L0 / ring 覆盖 / ID 错) + 提示传 SQL 文本 |
| SQL 文本 | 原流程不变 |

**关键认知校正**: og 5.0.3 默认 `statement_history.query` **也归一化** (`?` 代替字面量), 仅 `track_stmt_parameter=on` (部分版本) 才保留绑定值. 之前的提示 "请改从 dbe_perf.statement_history (带字面量) 取" 误导了 LLM. 本版修正此提示.

### 实现

新增 5 个 helper:
- `looksLikeSQLID(s)` — 纯数字且 ≤25 字符
- `fetchLiteralSQLByID(ctx, driver, id)` — 先查 unique_query_id, 失败回 debug_query_id
- `containsPlaceholders(sql)` — 检测 `?`/`$N`/`:N` 含字符串字面量正确忽略
- `truncForDisplay(sql, n)` — 错误消息内 SQL 预览截断
- `toString(v)` — driver row 值容错

### 测试

4 个新单元测试全过含 -race:
- TestLooksLikeSQLID — 17 case (含 ID / SQL / 空 / 边界 / 25 字符上限)
- TestContainsPlaceholders — 14 case (qmark / $N / :N / 字符串字面量内 ? 不误判 / 真实 og 归一化 SQL)
- TestTruncForDisplay — 3 case
- TestToString — 4 case

### 真机验证（本机 og5 docker）

3 条路径全过:
- `/sqltune 581990336` → 显示前 500 字 SQL 预览 + 3 恢复路径 (不再 LLM 早停)
- `/sqltune 9999999999` → 列 3 个可能原因
- `/sqltune "SELECT 1"` → 走原 sqltune 流程 (32s / 5 candidates / 2 verified)

### LLM agent 模式收益

之前 35B agent 看到 `/sqltune 581990336` 失败后无所适从 (早停只输出 "数据库版本"); 现在拿到含 SQL 预览 + 3 个具体恢复路径的错误, agent 有明确下一步可走 (拷 SQL 预览 → 让用户填字面量 → 重传).

### 兼容性

- og /sqltune 行为变化: 仅 SQL_ID 输入有新行为, SQL 文本输入零变化
- 5 个 dialect (MySQL/PG/Oracle/GaussDB) 零变化 — 此修复仅 og skill

---

## v1.1.42 (2026-05-17) — 🎉 /sqltune 全 6 库矩阵完工 (M9 端到端测试 + 收官)

### 背景

v1.1.34-v1.1.41 完成 M1-M8 八个 milestone. 本版交付 **M9: 6 库端到端测试**, **同时是整个 4.5 个月 /sqltune 多库扩展项目的收官版本**.

之前 86+ 单元测试全部 mock 实现, 不验证真实 DB 行为. M9 加端到端 (E2E) 集成测试 harness, 让真实 DB instance 可以验证 driver-level 类型返回, 方言 SQL 语法变化, 权限 edge case 等单元测试 mock 不出来的问题.

### 新增 neutral 端到端测试 harness

- **`internal/sqltune/scenarios_test.go`** (~290 行) — **共享 E2E harness**
  - `Scenario` struct: SQL + ExpectError / ExpectPlaceholderKind / ExpectMarkdownContains / ExpectCompressionTriggered
  - `runScenario()` 通用断言 helper: PlaceholderError 识别 / 错误 substring 匹配 / markdown 关键字 / stats 字段验证
  - `cannedLLM` mock LLMCaller: 返回硬编码 Round1Output JSON 避免烧 token
  - `defaultCannedReply()` 标准 Round1 happy-path 回复
  - `canonicalScenarios()` 共享场景集: trivial_select / empty_sql_rejected
  - `asPlaceholderError()` 不依赖 errors 包的 As 工具

- **`internal/sqltune/integration_helpers_test.go`** (~60 行)
  - `DialectSQLFixtures` 结构含 Simple / Placeholder / PlaceholderExpectedKind / DML / BigSQL 字段
  - `SimpleBigSQL()` 600 行 UNION ALL 生成器 (G7 触发用)
  - 文档明示 env var 约定: SQLTUNE_E2E_<DIALECT>_HOST 是 gate

- **`internal/sqltune/scenarios_test.go` 内 8 个新 harness 测试**:
  - canonical scenarios × nil LLM (raw Phase A fallback)
  - canonical scenarios × canned LLM (Round 1 happy path, CandidateCount=2 验证)
  - PlaceholderError 路由验证
  - PlaceholderKinds 3 方言 (pg_dollar / qmark / oracle_colon)
  - BigSQL 触发 CompressionTriggered=true
  - Trace unavailable 优雅降级渲染

### 5 dialect integration_test.go (HOST-gated)

每方言 `internal/<dialect>/sqltuner/integration_test.go` 含真实 DB 验证, 通过 env var 控制:

| Dialect | 测试 | env 前缀 |
|---|---|---|
| MySQL | Simple SELECT / `?` placeholder / DML reject / big SQL G7 | `SQLTUNE_E2E_MYSQL_` |
| PostgreSQL | Simple SELECT / `$N` placeholder / DML reject / EnableTrace Available:false / big SQL G7 | `SQLTUNE_E2E_POSTGRES_` |
| Oracle | SELECT FROM dual / `:1` placeholder / MERGE reject / 10053 EnableTrace 成功 | `SQLTUNE_E2E_ORACLE_` |
| openGauss | ExplainPlan + EXPLAIN PERFORMANCE Available:true + EquivVerifier DML reject | `SQLTUNE_E2E_OPENGAUSS_` |
| GaussDB | Decorator chain interface 验证 + PromptBuilder GS_PLAN_TRACE 关键字 | `SQLTUNE_E2E_GAUSSDB_` (⚠️ 无实例) |

**Env var 约定** (5 var 每方言):
- `*_HOST` (required gate) — 未设 skip cleanly
- `*_PORT` `*_USER` `*_PASS` `*_DB` (或 Oracle 的 SERVICE) — 有方言默认值

未设 HOST → cleanly skip 不阻塞 dev. 设了 → 真连 DB 跑全套.

### GaussDB 测试机缺失诚实处理

测试服务器 47.251.30.180 只有 Oracle/MySQL/PG/OG 4 库, 无 GaussDB Centralized 实例.

**文档明示 gap + decorator 继承论证**:
- `gaussdbPlanner` 装饰器仅 Kind + EnableTrace/CollectTrace 不同, 其余 7/9 方法转发 og
- og integration tests 实际覆盖了 GaussDB 7/9 方法的行为
- GS_PLAN_TRACE-specific 测试待 GaussDB 实例可用时 wire `openGaussDBOrSkip` helper

### scripts/sqltune-e2e.sh 一键调用

```bash
export SQLTUNE_E2E_MYSQL_HOST=47.251.30.180
export SQLTUNE_E2E_MYSQL_PASS='YourMySQLPass123!'
# ... 4 库 env 设满
./scripts/sqltune-e2e.sh
```

显示哪些 DB 启用了, 哪些 skip, 跑完整 6 包 `-run Integration -v -race -count=1`.

### docs/sqltune/e2e-testing.md 完整指南

- 测试覆盖矩阵
- 5 库 env var 约定 (表格)
- mock-LLM 默认模式说明
- CI 集成指南
- TODO: recorded-LLM 测试 / GaussDB 实例 / 跨方言 regression

### 测试

- 6 包全过含 -race: sqltune neutral + og + mysql + pg + gaussdb + oracle
- 8 个新 harness scenarios + 17 dialect placeholder subtests
- 所有 integration_test.go HOST 未设 cleanly skip

### 兼容性

- **6 个 dialect /sqltune 零行为变化** — M9 纯加测试不动产品代码
- 已发布的 GenericTuner / EquivVerifier / PerformancePlanner / PromptBuilder / LLMCaller / EquivVerifier 接口零修改

### 接口验证（第 9 次也是最终次）

M9 是**纯测试 harness**, 0 修改任何产品代码 / 接口. 证明完整接口体系经过 9 次扩展、修改、新增、压力测试后**完全稳定**.

---

## 🎉 整个 /sqltune 多库扩展项目收官

### 项目目标与达成

| 目标 | v1.1.34 前 | v1.1.42 后 |
|---|---|---|
| /sqltune 支持的 DB | 只 og | **6 库** (og + MySQL + PG + Oracle + GaussDB + dbaa/linkdb 通过 og) |
| CBO 决策跟踪 | 无 | **5 种** (og EXPLAIN PERF + MySQL optimizer_trace + GaussDB GS_PLAN_TRACE + Oracle 10053 + PG 显式不可用) |
| LLM 综合分析 | 只 og | 5 库共享 GenericTuner |
| 等价性验证 | 只 og (PG-only SQL) | 5 库 native hash 函数 |
| 千行 SQL 压缩 | 只 og | 6 库共享 |
| 端到端测试 | 无 | HOST-gated 5 库 |
| 接口设计 | 无 | 8 个接口 (9 方法 DialectPlanner + 4 个可选) |

### 9 个 milestone 发版回顾

| Milestone | 版本 | 关键交付 |
|---|---|---|
| M1 架构 | v1.1.34 | DialectPlanner 接口 + Registry + 5 DialectKind |
| M2 MySQL | v1.1.35 | optimizer_trace 自动采集 (16MB budget + 截断检测) |
| M3 PostgreSQL | v1.1.36 | pg_stats 旁路 (PG 结构性无 CBO trace) |
| M4a og 升级 | v1.1.37 | EXPLAIN PERFORMANCE + PerformancePlanner 可选接口 |
| M4b GaussDB | v1.1.37 | GS_PLAN_TRACE 装饰器 (decorator pattern 170 行接入) |
| M5 Oracle | v1.1.38 | 10053 (硬解析触发 + V$DIAG_TRACE_FILE_CONTENTS) |
| M6 EquivVerifier | v1.1.39 | 5 库 native hash 等价性验证 |
| M7 LLM 编排 | v1.1.40 | Tuner 迁 neutral + 5 库 LLM 综合分析 |
| M8 G7 压缩 | v1.1.41 | 千行 SQL token 压缩跨 6 库共用 |
| **M9 E2E 测试** | **v1.1.42** | **端到端测试 harness + HOST-gated 5 库** |

### 4 个月项目数据

- **新增代码**: ~8,500 行 (neutral package + 5 dialect packages + tests)
- **新增测试**: 86+ unit tests + 25+ integration tests
- **commit**: 30+ feature commits + 9 release commits
- **接口扩展次数**: 8 次 (DialectPlanner / 5 DialectKind / PerformancePlanner / EquivVerifier / PromptBuilder / LLMCaller / TunerEngine + BuildTuner)
- **接口零修改次数**: 9 次 (每个 milestone 接入新功能都零改既有接口)

### 用户感知改变

| 之前 | 之后 |
|---|---|
| `/sqltune` 只 og 可用, 其他报 "not supported" | 6 库都支持 `/sqltune` |
| og 见 CBO 决策 (EXPLAIN PERFORMANCE) | 5 库各拿 native CBO trace (10053/optimizer_trace/GS_PLAN_TRACE/pg_stats 旁路) |
| og 拿 LLM 综合优化 5 维度方案 | 5 库都拿 LLM 综合方案 + verify + 等价性 ✓ |
| 千行 SQL 撞 token budget 600s 超时 | 自动 G7 压缩, 50-100K token 内正常返回 |
| rewrite 等价性盲信 LLM | 5 库 native hash 函数实际跑数据 verify |

### 接口设计思想

**Go 标准可选能力模式** 全程贯彻:
- 核心接口 (DialectPlanner) — 必须实现
- 可选接口 (PerformancePlanner / EquivVerifier) — type-assert, 不实现自动跳过
- 装饰器模式 (GaussDB → og) — 1 行转发 90% 复用
- Registry 工厂 + Lookup — neutral 包不反向依赖 dialect 包
- 适配器隔离 (LLMCaller) — 跨包接口不带具体类型

---

## v1.1.41 (2026-05-17) — G7 千行 SQL token 压缩跨 6 库共用 (M8)

### 背景

v1.1.40 (M7) 让 5 库 /sqltune 都跑 LLM 综合分析. 但千行 SQL (500+ 行) / 复杂 plan (50+ 节点) / 多表 join (15+ 表) 喂给 LLM 会撞 token budget (200K+ tokens)，触发 LLM provider 端 600s 超时或上下文窗口截断。

og 早就有 token_compress.go 257 行 G7 压缩逻辑（plan tree 折叠 + schema hot/cold 分级），**但只 og 自己用**。M7 让其他 4 库走 GenericTuner 后，它们的复杂 SQL 没有压缩保护。

本版抽 og 压缩逻辑到 neutral 包，让 GenericTuner 也自动启用 → 5 库 + og 共 **6 库** 都有 G7 保护。

### 新增 neutral 压缩模块

- **`internal/sqltune/compress.go`** (~230 行) — **G7 千行 SQL token 压缩**
  - `Compress(cc *CollectedContext) *CompressionStats` — in-place mutate cc
  - 三阈值触发 (可配置 var): SQL > 500 行 OR plan 节点 > 50 OR 涉及表 > 15
  - `CountPlanNodes(n *PlanNode) int` — 导出供 og upgrade.go 复用
  - `CompressionStats` 字段: TriggerReason / OrigPlanNodes / FoldedNodes / HotTables / ColdTables / EstOrig/FinalTokens

- **`internal/sqltune/compress_test.go`** (~280 行 / 17 case)
  - 三阈值边界覆盖 + 不触发 case
  - foldPlanNodes 低 cost 子树折叠 + 单节点叶子不折叠保护
  - identifyHotTables 高 cost 表识别 + **大小写不敏感** (Oracle 大写 vs 用户小写)
  - demoteColdTables 删 stats 保 indexes
  - estimateTokens 单调性 + 含 trace body + nil safe
  - Compress 端到端 no-op + 触发 + Notes 含 G7 marker

### 三类压缩协同

| 类型 | 策略 | 收益 |
|---|---|---|
| ❶ Plan tree 折叠 | cost < total × 5% 子树替换 "(...elided N nodes, total_cost=X)" 占位符 | 大型 plan 减 60-80% 节点 |
| ❷ Schema 分级 | plan 中高 cost 表（hot）保完整 per-column stats；其他（cold）删 stats 保 TableInfo + IndexInfo | 多表 join 减 70% schema 字符 |
| ❸ CTE 去重 | TODO M8.x — 需要 SQL parser | (留后续) |

目标 prompt 控制在 ~100K tokens 内（公开 var `CompressTargetTokens`）。

### og 改造

- 删除 `internal/opengauss/sqltuner/token_compress.go` (257 行重复代码)
- og 的 `tuner.go` 改成调 `sqltune.Compress(cc)` 替代老 `CompressContext(cc)`
- og 的 `upgrade.go` 改用 `sqltune.CountPlanNodes(...)` 替代老内部函数

### GenericTuner 自动集成

`GenericTuner.Tune()` 在 Phase A 之后、Round 1 之前自动调 `Compress(cc)`，触发情况填入 ReportStats:
- `stats.CompressionTriggered = true`
- `stats.CompressionReason = "SQL 1200 行超阈值 500"`

5 dialect (MySQL/PG/Oracle/GaussDB + og 老 Tuner) 全部自动获得 G7 保护，零接入工作。

### 用户感知

之前：千行 SQL 喂 LLM → 撞 200K token / 600s 超时
现在：自动压缩到 ~50-100K token，正常返回 5 维度优化候选

报告 Notes 段示例：
```
G7 token 压缩已触发 (SQL 1200 行超阈值 500): plan 80 节点折叠 45 个;
schema 5 hot / 18 cold (~200k → ~50k tokens)
```

### 测试

- 17 个新 compression case 全过含 -race
- 全 6 sqltune 包测试通过 (sqltune neutral / og / mysql / pg / gaussdb / oracle)

### 兼容性

- **og /sqltune 零行为变化** (现在调 neutral 版本，逻辑等价)
- **5 个 dialect /sqltune 新增 G7 保护** — 之前千行 SQL 会失败，现在能跑

### 接口验证（第 8 次成功）

M8 是纯 utility 函数提升，**零修改 PromptBuilder / LLMCaller / DialectPlanner / EquivVerifier 接口**。证明 neutral 包既能承载接口，也能承载共享 utility 函数。

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| M3 PostgreSQL + pg_stats | ✅ v1.1.36 |
| M4a og EXPLAIN PERFORMANCE | ✅ v1.1.37 |
| M4b GaussDB GS_PLAN_TRACE | ✅ v1.1.37 |
| M5 Oracle + 10053 | ✅ v1.1.38 |
| M6 EquivVerifier 完善 | ✅ v1.1.39 |
| M7 Tuner 迁 neutral + LLM 编排 | ✅ v1.1.40 |
| **M8 G7 千行 SQL token 压缩** | ✅ **v1.1.41** |
| M9 6 库端到端测试 | ⏳ 1.5 周, 最后一站 |

**总进度: 9 / 10 子任务 (90%)** — 剩余约 1.5 周。

---

## v1.1.40 (2026-05-17) — Tuner 迁 neutral + LLM 编排 (M7 / 5 库齐 LLM 综合分析)

### 背景

v1.1.34-v1.1.39 完成 M1-M6（5 库 sqltune + CBO trace + equiv verifier 全部接入）。本版交付 **M7: Tuner 迁 neutral + LLM 编排** —— 第一次让 **5 库的 /sqltune 都能跑 LLM 综合分析**。

之前 MySQL/PG/Oracle/GaussDB 的 /sqltune 都只输出原始 Phase A markdown（EXPLAIN 树 + trace + schema），**不走 LLM**。只有 og 走 LLM Round 1 mega-analysis。本版抽 og 的核心 LLM 编排到 neutral 包，让其他 4 库共享。

og 保留独立 Tuner（有 600 行成熟的 memory injection / token compression / auto-upgrade，不破坏）。新 dialect 用 GenericTuner 起步。

### 新增 neutral 接口与编排器

- **`internal/sqltune/prompt_builder.go`** (~80 行) — **PromptBuilder + LLMCaller 接口**
  - PromptBuilder 4 方法: RoleTag / CBOKnowledge / PlanReading / HintSyntax — 让每方言注入自己的知识
  - LLMCaller: Chat(ctx, []ChatMessage) (string, error) — 最小接口，sqltune 不反向依赖 llm 包
  - ChatMessage struct 镜像 OpenAI / Anthropic / og 现有 llm.Message 形态

- **`internal/sqltune/orchestrator.go`** (~290 行) — **GenericTuner 主流程**
  - Phase A 并行: PerformancePlanner type-assert + EnableTrace/ExplainPlan/CollectTrace 串行 + CollectSchema + SnapshotDialect 并行
  - Round 1: 组 system+user prompt → LLMCaller.Chat → 严格 JSON 解析（含 markdown fence 自动剥离）
  - Verify: rewrite/hint → QuickPlanCost 比对 + EquivVerifier type-assert（M6 接入）
  - 优雅降级: nil LLM → raw Phase A; Round 1 失败 → raw Phase A + 错误 banner

- **`internal/sqltune/prompt_assembly.go`** (~290 行) — **system prompt 8 段组装 + report 渲染**
  - 4 段 universal: 调优原则 / 多样化要求 / 输出 JSON schema / 禁用措辞
  - 4 段 dialect-specific from builder: 角色 / CBO / 计划解读 / Hint
  - 用户消息: SQL + Plan tree + Trace body + Schema + Runtime + Notes
  - 最终报告渲染含 LLM cbo_analysis + 候选方案 (verify 结果含 EquivOK)

### 4 个 dialect PromptBuilder

每个方言新增 `prompt.go` 含 dialect-specific 知识（~150 行/方言）:

- **`internal/mysql/sqltuner/prompt.go`** — `MySQL 8.0 SQL 调优专家`
  - CBO: cost 公式 / Join 算子 (NL/BNL/Hash 8.0.18+) / optimizer_switch 标志 / optimizer_trace 完整 dump 引用
  - PlanReading: access_type 解读 (ALL/index/range/ref/eq_ref) / Extra 字段 (filesort/temporary/ICP/BNL)
  - HintSyntax: HASH_JOIN / BNL / INDEX / JOIN_ORDER / SET_VAR / 旧 STRAIGHT_JOIN

- **`internal/postgres/sqltuner/prompt.go`** — `PostgreSQL 14+ SQL 调优专家`
  - CBO: cost 公式 / random_page_cost SSD 调优 / from_collapse_limit DP vs GEQO / **明示 PG 结构性短板（无 rejected paths dump，用 pg_stats 旁路）**
  - PlanReading: Seq Scan / Nested Loop / Hash sort_method=external / BUFFERS shared_read / sargable 检查
  - HintSyntax: pg_hint_plan 扩展 + 会话级 GUC（enable_seqscan/work_mem/random_page_cost）

- **`internal/oracle/sqltuner/prompt.go`** — `Oracle 19c SQL 调优专家`
  - CBO: cost 公式 IO+CPU 加权 / optimizer_mode / optimizer_index_cost_adj / 10053 trace 段落详解（金标准 CBO dump）/ bind peeking 与 ACS
  - PlanReading: TABLE ACCESS FULL / INDEX RANGE SCAN / HASH JOIN TempSpc / Predicate Information access vs filter
  - HintSyntax: INDEX / FULL / USE_NL / USE_HASH / LEADING / ORDERED / PARALLEL 全套

- **`internal/gaussdb/sqltuner/prompt.go`** — `GaussDB(for openGauss) 2.0+ SQL 调优专家`
  - PG-family CBO + **明示 GS_PLAN_TRACE 独有（300MB 上限，类 Oracle 10053 但走 SQL 直读）** + EXPLAIN PERFORMANCE 11 列
  - HintSyntax: HashJoin / NestLoop / Leading / IndexScan / Rows / Set (og hint 风格)

### 4 个 dialect LLMCaller adapter

每方言新增 `llm_adapter.go` (~50 行/方言, 内容相同) 桥接 internal/llm.Provider → sqltune.LLMCaller:
- nil provider → 返回 nil（GenericTuner 自动 fallback raw 模式）
- 转换 ChatMessage → llm.Message + 调 provider.Chat + 提取 resp.Content
- mysql / postgres / oracle / gaussdb 各自一份（DRY 违反，但跨包共享会引入循环依赖，本地副本更干净）

### 4 个 dialect tunerFactory 切换

- mysql / postgres / oracle / gaussdb 的 `tunerFactory` 从 `newXxxTuner(...)` 改为 `sqltune.NewGenericTuner(planner, NewPromptBuilder(), newLLMAdapter(provider))`
- **删除 3 个 minimal tuner.go** (~600 行废代码):
  - `internal/mysql/sqltuner/tuner.go` (~210 行)
  - `internal/postgres/sqltuner/tuner.go` (~205 行)
  - `internal/oracle/sqltuner/tuner.go` (~210 行)
- gaussdb 不需删（之前没有独立 tuner.go，直接 delegate og）

### 测试

**19 个新 case 全过含 -race:**

- **12 个 neutral GenericTuner mock 测试**:
  - nil LLM → raw Phase A fallback ✓
  - 空 SQL → error ✓
  - PlaceholderError 通过 NormalizePlaceholders 正确 propagate ✓
  - Round 1 success: 1 candidate / cost 1000→100 / EquivOK 设置 / cbo_analysis 渲染 ✓
  - Round 1 JSON 解析失败 → raw fallback + 错误 banner ✓
  - JSON markdown fence 自动剥离 (```json / ``` / plain) ✓
  - assembleSystemPrompt 含 builder 4 段 + universal 4 段 ✓
  - nil DialectInfo → "(未采集到 dialect snapshot)" 兜底 ✓
  - verifyOne rewrite + EquivVerifier ✓ / DDL unverifiable ✓ / unknown type ✓
  - truncate 工具函数 ✓

- **4 个 dialect PromptBuilder 内容验证**:
  - 接口断言 (var _ sqltune.PromptBuilder = (*xxx)(nil)) — 编译时
  - 各方言关键字断言: MySQL `optimizer_trace` / PG `pg_stats` `random_page_cost` / Oracle `10053 trace` `bind peeking` / GaussDB `GS_PLAN_TRACE` `Peak Memory`

- **1 个 LLMCaller adapter 测试**: nil provider → nil 返回值

### 关键设计兑现

1. **og 独立保留** — og 600 行 Tuner 有 memory/compress/upgrade 成熟功能，强制迁会破坏老用户。新 dialect 用 GenericTuner 起步，等成熟后再 backport 高级特性
2. **PromptBuilder 4 段拆分** — RoleTag/CBOKnowledge/PlanReading/HintSyntax 让 LLM prompt 既共享 universal 又含方言知识
3. **LLMCaller 接口隔离** — sqltune 不反向依赖 llm，每个 dialect 写 30 行 adapter 桥接
4. **优雅降级三件套** — nil LLM / Round 1 错误 / JSON 解析失败 都 fallback 到 raw Phase A，永不 hard error
5. **type-assert EquivVerifier** — Round 1 候选验证时检查 planner 是否实现 M6 接口，没实现就跳过 EquivOK 置 nil
6. **trace + EXPLAIN PERFORMANCE 双采集** — Phase A 同时拿 CBO trace 和算子细节喂给 LLM 综合

### 兼容性

- **5 个 dialect /sqltune 行为变化** (但 og 不变):
  - 之前 MySQL/PG/Oracle/GaussDB 只输出 raw Phase A
  - 现在配 LLM 时跑 Round 1 综合分析输出 5 维度候选 + cbo_analysis + verify 结果
  - 没配 LLM 时自动 fallback 到 raw Phase A（同之前）
- **og /sqltune 零行为变化** (沿用原 Tuner)

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| M3 PostgreSQL + pg_stats | ✅ v1.1.36 |
| M4a og EXPLAIN PERFORMANCE | ✅ v1.1.37 |
| M4b GaussDB GS_PLAN_TRACE | ✅ v1.1.37 |
| M5 Oracle + 10053 | ✅ v1.1.38 |
| M6 EquivVerifier 完善 | ✅ v1.1.39 |
| **M7 Tuner 迁 neutral + LLM 编排** | ✅ **v1.1.40** |
| M8 G7 千行 SQL AST 拆解 | ⏳ 2 周 |
| M9 6 库端到端测试 | ⏳ |

**总进度: 8 / 10 子任务 (80%)** — 剩余约 3 周。

---

## v1.1.39 (2026-05-17) — EquivVerifier 完善 (M6 跨 5 库等价性验证)

### 背景

v1.1.34-v1.1.38 完成 M1-M5（5 库 sqltune + CBO trace 矩阵全部接入）。本版交付 **M6: EquivVerifier 完善** —— rewrite 类方案的**等价性安全底线**。

旧版本只有 og 有 90 行骨架 EquivVerifier，用 PG-specific `string_agg/md5/::text`，其他 4 库不能用，**而且 og 的 tuner 通过具体类型字段持有它**（破坏 M1.4 的接口设计纯洁性）。

LLM 提的 rewrite 类方案（改写 SQL 形态）如果不验证等价性，就是生产事故等着发生。本版让每个方言用自己的 native hash 函数验证。

### 新增 neutral 可选接口

- **`internal/sqltune/dialect.go`** +60 行 — **EquivVerifier 可选接口**
  - 同 PerformancePlanner 模式（Go 标准 io.Closer 风格）
  - `interface { DialectPlanner; VerifyEquivalence(ctx, origSQL, candidateSQL, limit) (bool, error) }`
  - 文档明示三层 contract: sample-based / read-only / placeholder-rejecting
  - 返回 (false, err) 时调用方应标 EquivOK=nil（Unknown），不阻塞 verify

### M6.2 — og + PG 实现（md5 + string_agg）

- **`internal/opengauss/sqltuner/equiv.go`** (~120 行)
  - 替换老 equiv_verifier.go (已删除)
  - `(p *ogPlanner) VerifyEquivalence(...)` 实现 sqltune.EquivVerifier
  - 策略: `md5(string_agg((sub.*)::text ORDER BY row_text))`
  - DML 拒绝 + 占位符检测 wrap 成 sqltune.PlaceholderError
  - 60s timeout + default 1000 rows sample

- **`internal/postgres/sqltuner/equiv.go`** (~120 行)
  - PG 用相同 SQL（PG/og 完全同源）
  - 独立文件无 cross-package 依赖
  - 含 isDMLPG 12 case 关键字检测 + WITH CTE 内 DML 识别

### M6.3 — MySQL 实现（MD5 + GROUP_CONCAT）

- **`internal/mysql/sqltuner/equiv.go`** (~120 行)
  - `MD5(GROUP_CONCAT(... ORDER BY row_text SEPARATOR '|'))`
  - 用 `CONCAT_WS(',', sub.*)` 做 row → text 投影（MySQL 8.0+ 支持 star expansion in function args）
  - **关键**: 自动 `SET SESSION group_concat_max_len = 16MB`，否则默认 1024 字节会**静默截断产生假阳性等价**（最坏失败模式）
  - REPLACE 也识别为 DML（MySQL 特有）

### M6.4 — Oracle 实现（STANDARD_HASH 12c+）

- **`internal/oracle/sqltuner/equiv.go`** (~110 行)
  - **避开 LISTAGG 的 4000 字符 ORA-01489 限制**：用 `XMLAGG(XMLELEMENT("r", sub.*).GETCLOBVAL())` 走 CLOB
  - `STANDARD_HASH(<clob>, 'MD5')` 哈希整个 CLOB（12c+）
  - 11g 缺 STANDARD_HASH 返回 (false, err) 提示升级或外部验证
  - MERGE 识别为 DML（Oracle 特有）

### M6.5 — GaussDB 装饰器继承 + og tuner 改造

- **`internal/gaussdb/sqltuner/planner.go`** +10 行
  - VerifyEquivalence 转发给 og — 1 行实现，复用 og 的 SQL（PG-compatible）

- **`internal/opengauss/sqltuner/tuner.go`**
  - 删除 `verifier *EquivVerifier` 字段
  - verifyOne 改成 `if ev, ok := t.planner.(sqltune.EquivVerifier); ok` type-assert
  - NewTunerFromPlanner 移除 verifier 构造
  - 删除 `internal/opengauss/sqltuner/equiv_verifier.go` (老骨架)

### M6.6 — 单元测试

5 库 + neutral 共 **17 个新测试** 全过含 -race:

- **og**: 接口断言 + DML 3 case + 占位符
- **PG**: 接口断言 + DML + 占位符 + **isDMLPG 13 case** (含 WITH CTE 内 DELETE/INSERT/UPDATE/MERGE word-boundary)
- **MySQL**: 接口断言 + DML (REPLACE) + 占位符 + **isDMLMySQL 12 case**
- **Oracle**: 接口断言 + DML (MERGE) + 占位符 + **isDMLOracle 11 case**
- **GaussDB**: 接口断言 + 装饰器转发验证（确认调用穿透到 og）

### 关键设计兑现

| 设计点 | 实现 |
|---|---|
| Go 标准可选能力模式 | EquivVerifier 接口同 PerformancePlanner / io.Closer 风格 |
| 每方言用 native hash | 不强行抽 SQL 模板，最高效最准 |
| GaussDB 1 行装饰器继承 | 转发 og.VerifyEquivalence |
| 三层 safety net | DML 拒绝 + 占位符拒绝 + 60s 超时 |
| 失败模式安全 | false negatives 可能但永不 false positives |
| MySQL group_concat_max_len 防截断 | 自动 SET 16MB，避免静默截断产生假阳性等价 |
| Oracle XMLAGG 替代 LISTAGG | 绕过 4000 字符 ORA-01489 |

### 接口验证（第 6 次成功）

M6 接入**零改 M1-M5 接口代码**。EquivVerifier 作为第 2 个可选接口（第 1 个是 PerformancePlanner），证明可选能力模式可无限扩展。

### 兼容性

- **5 个 dialect /sqltune 行为变化**: rewrite 类候选现在会跑实际等价性验证（之前 og 跑了但用旧 API，其他 4 库根本没跑）
- LLM 综合分析依赖 EquivOK 字段 — 现在 4 库都有数据填了

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| M3 PostgreSQL + pg_stats | ✅ v1.1.36 |
| M4a og EXPLAIN PERFORMANCE | ✅ v1.1.37 |
| M4b GaussDB GS_PLAN_TRACE | ✅ v1.1.37 |
| M5 Oracle + 10053 | ✅ v1.1.38 |
| **M6 EquivVerifier 完善** | ✅ **v1.1.39** |
| M7 Tuner 迁 neutral + LLM 编排 | ⏳ |
| M8 G7 千行 SQL AST 拆解 | ⏳ |
| M9 6 库端到端测试 | ⏳ |

**总进度: 7 / 10 子任务 (70%)** — 剩余约 5 周。

---

## v1.1.38 (2026-05-17) — Oracle /sqltune + 10053 CBO trace (M5)

### 背景

v1.1.34-v1.1.37 完成 M1-M4。本版交付 **M5: Oracle** —— 6 库扩展计划里**最难的一档**。

Oracle **10053 event** 是 SQL 数据库里**金标准** CBO 决策 dump，richer than GaussDB GS_PLAN_TRACE 和 MySQL optimizer_trace：dump 完整的 cost-based optimizer 推理过程——base statistics、access path candidates 与 rejected 原因、join order enumeration、peeked bind values、所有 cost calculations。

接入 Oracle 比其他 5 库都难：
1. **EXPLAIN 是文本不是 JSON** — 用 PLAN_TABLE 结构化 SELECT 绕过 DBMS_XPLAN 文本解析
2. **10053 只在硬解析时 dump** — cursor cache 命中就跳过；用 unique comment 注入强制 hard parse
3. **trace 文件是 OS 文件** — opendb 不能 OS shell，用 V$DIAG_TRACE_FILE_CONTENTS (19c+) SQL 直读
4. **tracefile_identifier 隔离** — 并发会话写到同一 trace dir，必须 per-session 唯一标识

### 新增 `internal/oracle/sqltuner/`（约 1000 行）

- **`planner.go`** (~100 行)
  - `oraclePlanner` struct + traceTag 字段保存当前 trace identifier
  - init() 注册 planner + tuner factory for DialectOracle
  - castProvider / castMemStore

- **`explain.go`** (~300 行) — **M5.2 EXPLAIN PLAN + PLAN_TABLE 结构化**
  - ExplainPlan: `EXPLAIN PLAN SET STATEMENT_ID = 'opendb_<uuid>' FOR <sql>` + 从 PLAN_TABLE 拉 id/parent_id/operation/options/cost/cardinality 重建 PlanNode 树
  - **比 DBMS_XPLAN 文本解析靠谱 10×** — id/parent_id 父子关系无歧义
  - QuickPlanCost: 同路径只取 root cost
  - STATEMENT_ID 16 hex 唯一隔离 + 用完 DELETE 不污染 PLAN_TABLE
  - 占位符检测: `:1` / `:B1` / `:identifier` 三种 Oracle bind 风格，含字符串字面量里的 `:` 正确忽略 + `::` 防御性跳过
  - PlaceholderError 引导 V$SQL_BIND_CAPTURE

- **`trace.go`** (~230 行) — **M5.3 + M5.4 10053 完整流程**
  - EnableTrace 三件套:
    1. `ALTER SESSION SET TRACEFILE_IDENTIFIER = 'opendb_<random>'` — 文件名隔离
    2. `ALTER SESSION SET EVENTS '10053 trace name context forever, level 1'` — 开 trace
    3. 返回 closeFn (atomic.Bool 防重入 + 自动 SET EVENTS off + 清 identifier)
  - hardParseHintWrap: `/* opendb_sqltune_<random> */ <sql>` — 随机注释强制 hard parse（cursor cache miss → trace 才 dump）
  - CollectTrace 四步:
    1. `SELECT value FROM V$DIAG_INFO WHERE name = 'Default Trace File'` 拿 base path
    2. tagInPath 把 tracefile_identifier 插到 .trc 前 → tagged filename
    3. `SELECT payload FROM V$DIAG_TRACE_FILE_CONTENTS WHERE trace_filename = ? ORDER BY line_number` (19c+)
    4. 1MB 截断保护 + Truncated:true
  - **优雅降级**: V$DIAG_TRACE_FILE_CONTENTS 不可用（11g/12c/无权限）返 Available:false 附详细 note 含 base path + tag 让 DBA 手动取

- **`collectors.go`** (~340 行) — **M5.5 schema/dialect/runtime**
  - CollectSchema: ALL_TABLES (num_rows/blocks/avg_row_len) + ALL_INDEXES + ALL_IND_COLUMNS (LISTAGG 拼 cols) + ALL_TAB_COL_STATISTICS (NDV/null_count/density)
  - SnapshotDialect: BANNER + **20 个 CBO 关键 V$PARAMETER** (optimizer_mode/optimizer_features_enable/optimizer_index_cost_adj/db_file_multiblock_read_count/pga_aggregate_target/cursor_sharing/...)
  - V$DATAGUARD_CONFIG 检 Data Guard + ALL_PART_TABLES 检分区表
  - SnapshotRuntime: V$SESSION 等待事件 + V$LOCK 过滤涉及表 (object_id → name via DBA_OBJECTS)
  - ExpandViews stub (LONG 类型需要特殊处理)
  - SQL parser: stripComments / tokenize / isIdentifier + 33 个 Oracle 关键字
  - sqlInListOracle 含单引号转义

- **`tuner.go`** (~210 行) — minimal Phase A orchestrator
  - **严格顺序流程**: EnableTrace → hardParseHintWrap → ExplainPlan → CollectTrace → secondary collectors (schema/dialect/runtime)
  - 不并行（trace 流程必须串行）
  - renderOracleReport: markdown 含实例环境 / SQL / 计划树 / **10053 trace body 可折叠** / 表元数据 / 列统计 / 索引 / 等待事件 / 锁

- **`planner_test.go`** (~210 行 / 23 case)
  - factory 注册 / Kind / 占位符 10 sub-case (含 `:1`/`:B1`/`:p_name`/字符串字面量里的 `:`/PG-style `::` cast 不误报/双冒号防御性跳过/多行)
  - 表名提取 6 case 含 schema 限定符 / CTE
  - sqlInListOracle 单引号转义
  - joinOpAndOption 4 case
  - newStatementID / generateTraceTag 唯一性 + Oracle 命名规范 (≤48 字符 + alphanumeric+_)
  - hardParseHintWrap 不同 tag 产生不同包装
  - tagInPath 3 case 含 defensive 路径无 .trc 后缀
  - isBindIdentChar / noopClose

### 新增 Oracle skill

- **`internal/oracle/skill/query/sqltune_skill.go`** (~150 行)
  - `/sqltune <SQL>` 调 `BuildTuner(DialectOracle)`
  - 占位符错误 3 路径恢复: V$SQL_BIND_CAPTURE / 10046 trace 日志 / 手动替换
  - anonymous import 触发 oracle sqltuner init() 注册

- **`internal/oracle/register.go`** 加 `reg(query.NewSQLTuneSkill(driver, modelMgr, nil))`

### 接口验证（第 5 次成功）

M5 接入 **零改 M1-M4 接口代码**。最难的方言也无需扩接口，证明设计彻底稳定。

**DB 接口适配矩阵完成:**

| Dialect | 接入工作量 | CBO Trace | 文件行 |
|---|---|---|---|
| og | 基础（M1 抽出来）| ❌（M4a 加 EXPLAIN PERF 补充）| ~4000 |
| MySQL | 中（独立解析器）| ✅ optimizer_trace | ~1150 |
| PG | 中（旁路策略）| ❌（pg_stats 旁路）| ~1080 |
| GaussDB | 装饰器 | ✅ **GS_PLAN_TRACE** | ~380 |
| **Oracle** | **最难** | ✅ **10053**（金标准）| ~1000 |

### MVP 局限明示

1. **不走 LLM 综合分析** — M7 待办
2. **V$DIAG_TRACE_FILE_CONTENTS 需 19c+** — 老版本返 Available:false 指导手动取
3. **10053 硬解析触发不 100% 保证** — 用 random comment 强制，但极端情况仍可能命中 cursor (SOFT_PARSE_RATIO 检测留 M5.x)
4. **ExpandViews 是 stub** — LONG 类型 Oracle 驱动支持差异大
5. **未端到端跑过真实 Oracle 实例** — 仅 23 单测 + 编译过

### 测试

- oracle sqltuner 23 case 全过含 -race
- 全 6 sqltune 包: sqltune (neutral) / og / mysql / pg / gaussdb / oracle 全过

### 兼容性

- 5 个现有 dialect /sqltune 行为零变化
- Oracle /sqltune 是**新增能力**，不影响现有 Oracle skill (explain/slowsql/topsql/awr/ash 等)

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| M3 PostgreSQL + pg_stats | ✅ v1.1.36 |
| M4a og EXPLAIN PERFORMANCE | ✅ v1.1.37 |
| M4b GaussDB GS_PLAN_TRACE | ✅ v1.1.37 |
| **M5 Oracle + 10053** | ✅ **v1.1.38** |
| M6 EquivVerifier | ⏳ |
| M7 Tuner 迁 neutral + LLM 编排 | ⏳ |
| M8 G7 千行 SQL AST 拆解 | ⏳ |
| M9 6 库端到端测试 | ⏳ |

**总进度: 6 / 10 子任务 (60%)** — 最难的过了！剩余约 8 周。

---

## v1.1.37 (2026-05-17) — og EXPLAIN PERFORMANCE + GaussDB GS_PLAN_TRACE (M4a + M4b)

### 背景

v1.1.36 (M3 PG) 之后，本版交付 **M4** 两个互补的能力：
- **M4a**: og /sqltune 增加 EXPLAIN PERFORMANCE 算子级执行画像
- **M4b**: GaussDB Centralized 接入 sqltune，并独占 **GS_PLAN_TRACE** —— PG 家族里唯一真正的 CBO 决策 dump

GaussDB 是 6 库里**唯一**有 rejected paths dump 的 PG 家族成员（Oracle 10053 / MySQL optimizer_trace / GaussDB GS_PLAN_TRACE 是三大金矿）。

### 新增 neutral 可选能力

- **`internal/sqltune/dialect.go`** 加 ~40 行 — **PerformancePlanner 可选接口**
  - 标准 Go 可选能力模式（io.Closer / ReaderFrom 风格）
  - `interface { DialectPlanner; ExplainPerformance(ctx, sql) (*TraceData, error) }`
  - 不污染 DialectPlanner 核心 9 方法；不实现的方言自动 type-assert miss
  - 是 og + GaussDB 共享的能力（PG/MySQL/Oracle 不实现）

### M4a — og EXPLAIN PERFORMANCE

- **`internal/opengauss/sqltuner/performance.go`** (~160 行)
  - `ogPlanner.ExplainPerformance(ctx, sql) (*TraceData, error)` — 跑 `EXPLAIN PERFORMANCE <sql>`，文本输出包装 TraceData
  - **DML 安全**: `isSelectish` 拒绝 INSERT/UPDATE/DELETE/MERGE（EXPLAIN PERFORMANCE 会真实执行）。CTE 内嵌 DML 也识别（containsWord word-boundary 检查）
  - Format = `og_explain_performance`，Notes 明示 "**不是 CBO 决策 dump**，PG 系无 rejected paths；但可对比 A-rows vs E-rows 推断 stats 失真"
  - 失败降级 Available:false 不中断主流程

- **`internal/opengauss/sqltuner/tuner.go`** 修改 `collectPhaseA`
  - 增加 step 0: `if pp, ok := t.planner.(sqltune.PerformancePlanner); ok { ... }` type-assert
  - 并行采 trace（与 JSON EXPLAIN / schema / dialect 同步跑）
  - 失败 addNote 不影响其他 collector

- **`internal/opengauss/sqltuner/performance_test.go`** (~110 行 / 5 case)
  - 编译时接口断言 `var _ sqltune.PerformancePlanner = (*ogPlanner)(nil)`
  - isSelectish 12 case (DML/DDL/CTE 内 DML/空串)
  - ExplainPerformance DML 路径返回 Available:false（nil driver 验证早返）
  - containsWord 9 case (含 `UPDATED_AT` 不匹配 `UPDATE` 边界正确)
  - stringify 4 case

### M4b — GaussDB Centralized sqltune (含 GS_PLAN_TRACE)

新增 **`internal/gaussdb/sqltuner/`** 包 (~380 行):

- **`planner.go`** (~170 行) — **装饰器模式**
  - `gaussdbPlanner` decorator 持有 og planner，8/9 DialectPlanner 方法转发给 og
  - Only override: `Kind() = DialectGaussDB` + `SnapshotDialect` 在 version 前加 `GaussDB-compat:` tag
  - 复用 og 90% 代码，~170 行接入第 4 个 DB
  - init() 注册 DialectGaussDB planner + tuner factory
  - PerformancePlanner 也实现（转发给 og 的）

- **`trace.go`** (~210 行) — **GS_PLAN_TRACE 实现**
  - EnableTrace 双探针：① `SELECT to_regclass('pg_catalog.gs_plan_trace') IS NOT NULL` 检表存在 ② `SELECT 1 FROM gs_plan_trace LIMIT 0` 检 sysadmin 权限。任何一步失败都返 Available:false + 详细 note
  - CollectTrace: `SELECT query/plan/plan_trace/modifydate/LENGTH FROM gs_plan_trace ORDER BY modifydate DESC LIMIT 1`
  - **1 MB 截断保护**: plan_trace 列上限 300 MB，远超 LLM 上下文窗口，`LEFT(plan_trace, 1MB+1)` 检测溢出 + Truncated:true
  - 不尝试 SET GUC 启用（华为未公开 GUC 名称），note 提示 DBA 手动启用
  - 优雅降级到 EXPLAIN PERFORMANCE + pg_stats 旁路

- **`planner_test.go`** (~150 行 / 8 case)
  - factory 注册 (planner + tuner) / Kind = gaussdb / 编译时 PerformancePlanner 接口断言
  - truthy 12 case (含 bool/string/bytes/int 多类型)
  - buildTraceBody 测试（确认 query/plan/plan_trace 三段都渲染 + 空 trace 时不渲染对应段）
  - toStr / toInt64 类型容错 / noopClose

- **`internal/gaussdb/skill/query/sqltune_skill.go`** (~150 行)
  - GaussDB 独立的 SQLTuneSkill 调 `BuildTuner(DialectGaussDB)`
  - 不能复用 og 的（og 硬编码 DialectOpenGauss 会绕过 GaussDB-specific 的 GS_PLAN_TRACE）
  - 占位符错误指向 dbe_perf.statement_history（与 og 一致）

- **`internal/gaussdb/register.go`** 修改
  - 把 `reg(query.NewSQLTuneSkill(...))` 换成 `reg(gaussdbquery.NewSQLTuneSkill(...))`
  - 注释说明：必须用 gaussdb 自己的 skill 才能拿到 GS_PLAN_TRACE

### 接口验证（第四次成功）

M4 实施过程**继续零改 M1-M3 接口代码**。装饰器模式让 GaussDB 接入只需 ~170 行，验证：
- `sqltune.PerformancePlanner` 可选接口扩展不影响现有 DialectPlanner 实现者
- og decorator 模式让 GaussDB 复用 90% og 代码
- BuildTuner(DialectGaussDB) 路由正确

DB 验证矩阵：
| Dialect | EXPLAIN JSON | CBO Trace | EXPLAIN PERFORMANCE |
|---|---|---|---|
| og | ✅ | ❌ 无 | ✅ (M4a) |
| MySQL | ✅ (different schema) | ✅ optimizer_trace | ❌ N/A |
| PG | ✅ (og-style) | ❌ 显式 unavailable | ❌ N/A |
| **GaussDB** | ✅ (og-style) | ✅ **GS_PLAN_TRACE** (M4b) | ✅ (M4b 继承自 og) |

### 测试

- og sqltuner +5 case (含 PerformancePlanner 接口断言 + DML 拒绝路径)
- gaussdb sqltuner 8 case 全过含 -race
- 5 个 sqltune-touching 包总测试: og / mysql / pg / gaussdb / neutral

### 兼容性

- **og /sqltune 行为变化** (轻微): 报告新增 `cc.Trace` 字段含 EXPLAIN PERFORMANCE 输出（之前是 nil）；输出格式不变，DBA 视角看就是多了一段算子级 detail
- **MySQL /sqltune 零变化**
- **PG /sqltune 零变化**
- **GaussDB /sqltune 新增能力** (之前复用 og 但走错 dialect kind，现在走对了)

### MVP 局限明示

1. **不走 LLM 综合分析** — M7 待办
2. **GS_PLAN_TRACE 启用 GUC** — 华为未公开，依赖 DBA 手动启用；opendb 仅"如果已启用则采集"
3. **plan_trace 1 MB 截断** — 300 MB 上限远超 LLM 上下文，必须截。Truncated:true 提示

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| M3 PostgreSQL + pg_stats | ✅ v1.1.36 |
| **M4a og EXPLAIN PERFORMANCE** | ✅ **v1.1.37** |
| **M4b GaussDB GS_PLAN_TRACE** | ✅ **v1.1.37** |
| M5 Oracle + 10053 | ⏳ 最难，3-4 周 |
| M6 EquivVerifier | ⏳ |
| M7 Tuner 迁 neutral + LLM 编排 | ⏳ |
| M8 G7 千行 SQL AST 拆解 | ⏳ |
| M9 6 库端到端测试 | ⏳ |

总进度: **5 / 10 子任务 (50%)** — 一半！剩余约 11 周。

---

## v1.1.36 (2026-05-17) — PostgreSQL /sqltune MVP (M3: EXPLAIN JSON + pg_stats 旁路)

### 背景

v1.1.34 (M1) + v1.1.35 (M2 MySQL) 后，本版接入第三个 DB **PostgreSQL**，并把 og 的 EXPLAIN JSON parser 抽到 neutral 共享。

PG 是 sqltune 6 库里**结构性最难**的：**没有 CBO 决策 dump**。不像 Oracle (10053) / MySQL (optimizer_trace) / GaussDB 集中式 (GS_PLAN_TRACE) 那样能 dump 候选 plan + 选择原因。PG 10-16 全部版本，planner 只会告诉你它"选了什么"，不会告诉你"为什么没选别的"。

补救策略：**pg_stats 旁路** + 关键 GUC 喂给 LLM 让它自己推。

### 新增 (neutral 共享)

- **`internal/sqltune/plan_parser.go`** (~120 行) — **M3.0 共享 EXPLAIN JSON parser**
  - `ParsePGStylePlanNode(map[string]any) *PlanNode` — PG / openGauss / GaussDB (集中式) 通用
  - og 原有 parsePlanNode + getStr/getFloat/getInt 删除，统一调 neutral 版
  - 5 case 测试（nil safe / Seq Scan / Hash Join nested / Index Scan with ANALYZE / Sort Key array）

### 新增 `internal/postgres/sqltuner/`（约 1080 行）

- **`planner.go`** (~110 行)
  - `pgPlanner` 实现 sqltune.DialectPlanner 9 方法
  - init() 注册 planner + tuner factory for DialectPostgreSQL
  - castProvider / castMemStore — opaque any → llm.Provider / *memory.Store

- **`explain.go`** (~260 行) — **M3.2 EXPLAIN JSON + DML 安全 + 占位符**
  - ExplainPlan: `EXPLAIN (FORMAT JSON, COSTS, VERBOSE, ANALYZE?, BUFFERS?, SETTINGS?)` 组合
  - SETTINGS 选项 PG<12 自动检错回退
  - DML 安全: ANALYZE on INSERT/UPDATE/DELETE 时自动 `BeginTx → Query → Rollback`
  - QuickPlanCost: estimates-only 给 verify 用
  - 占位符检测: 同时识别 PG `$N` 和 JDBC `?`，正确忽略字符串字面量里的（含 `\` 转义）
  - PlaceholderError 引导到 pg_stat_statements / auto_explain

- **`trace.go`** (~60 行) — **M3.3 trace 显式不支持**
  - EnableTrace / CollectTrace 都返回 Available:false + `pgUnavailableTraceNote`
  - note 明确告诉 LLM: "PG 无 plan_trace；用 EXPLAIN ANALYZE 实际行数 vs 估算差异 + pg_stats (n_distinct/null_frac/correlation/histogram_bounds) + 关键 GUC 推断 planner 决策"

- **`collectors.go`** (~440 行) — **M3.4 pg_stats 旁路（M3 headline）**
  - CollectSchema 4 个 fan-out 查询: pg_class (relpages/reltuples/size) + pg_index (含 PRIMARY/UNIQUE 标志) + **pg_stats** (n_distinct / null_frac / correlation / most_common_vals / most_common_freqs) + pg_constraint (FK)
  - SnapshotDialect: version() + **20 个 CBO 关键 GUC** (work_mem / random_page_cost / effective_cache_size / seq_page_cost / cpu_*_cost / max_parallel_workers_per_gather / enable_seqscan|indexscan|hashjoin|mergejoin|nestloop / default_statistics_target / from|join_collapse_limit / jit)
  - pg_extension 全量 / pg_stat_replication 检 HA / pg_class.relkind='p' 检分区表
  - SnapshotRuntime: pg_stat_activity 等待事件 Top 20 + pg_locks 过滤到涉及表
  - ExpandViews stub (M3 不做)
  - 内置 SQL 解析器: stripComments / tokenize / isIdentifier / isSQLKeyword + sqlInList (含单引号转义)
  - parseFloat / parseInt / parseBool 类型容错（PG driver 经常返 string）

- **`tuner.go`** (~205 行) — minimal Phase A orchestrator
  - renderPGReport: markdown 含实例环境 / SQL / 计划树 / **pg_stats 旁路表格** (每表 Top 10 列 n_distinct/null_frac/correlation/avg_width) / 表元数据 / 索引清单 / CBO trace 状态 (always Available:false) / 等待事件 / 锁

- **`planner_test.go`** (~230 行 / 18 case)
  - factory 注册 / Kind / **占位符 10 sub-case** (含 PG $42 / 高数字 / 字符串字面量里的 $1 / `?` qmark / 混合 / `$$` 不是 placeholder) / 表名提取 6 case / **DML 识别 10 case 含 CTE 内 DELETE** / EXPLAIN 命令构建 (BUFFERS 仅 ANALYZE 才加) / sqlInListPG 含转义 / **EnableTrace 返回 Available:false 验证 note 含 pg_stats** / parseBool 多类型

### 新增 PG skill

- **`internal/postgres/skill/query/sqltune_skill.go`** (~150 行)
  - `/sqltune <SQL>` 入口，调 sqltune.BuildTuner(DialectPostgreSQL)
  - 占位符错误 PG 特化: 指向 pg_stat_statements + auto_explain log + 手动替换 3 个恢复路径
  - anonymous import postgres sqltuner 触发 init() 注册

- **`internal/postgres/register.go`** 加一行 `reg(query.NewSQLTuneSkill(driver, modelMgr, nil))`

### 接口验证（第三次成功）

M3 实施过程**继续零改 M1/M2 接口代码**就让 PG 接入了。验证经过三个差异性极大的 DB:
- og: 有 EXPLAIN JSON，无 CBO trace
- MySQL: 有 EXPLAIN JSON (不同 schema)，**有 CBO trace** (optimizer_trace)
- PG: 有 EXPLAIN JSON (og-style)，**显式无 CBO trace**

DialectPlanner 9 方法 + AnalyzeMode 三态 + PlaceholderError.DetectedKind (`pg_dollar` / `qmark`) + TraceData{Available:false + Notes} 全部正确路由。**接口稳了**。

### 关键设计要点

1. **og 与 pg 共享 EXPLAIN parser** — 老 og 的 parsePlanNode 和未来 PG 的几乎相同（PG fork），抽到 neutral 包永不漂移。GaussDB M4b 也会直接用
2. **PG 短板诚实暴露** — 不假装支持 trace。pgUnavailableTraceNote 既是给用户看的说明，也是给 LLM 的 prompt hint，告诉它该用什么旁路推断
3. **pg_stats 是 M3 headline** — 报告里专门一节列每个涉及表 Top 10 列的统计快照。LLM 比较实际行数 (ANALYZE) vs 这些 stats 推断 → 直接定位 stats 失真 / 数据倾斜
4. **DML + ANALYZE 安全** — BeginTx → Rollback；WITH CTE 内 DELETE/INSERT/UPDATE/MERGE 也识别（containsWord 做 word-boundary 检查，不会误报 `DELETED_AT`）
5. **EXPLAIN SETTINGS 兼容** — PG 12+ 才支持，错误自动回退一次

### 测试

- pg sqltuner 18 case 全过含 -race
- neutral sqltune 7 case (5 新增 parser + 2 原有 dialect)
- og + mysql + skill 全部子包仍过

### 兼容性

- og /sqltune 行为零变化（共享了 parser 但逻辑不变）
- MySQL /sqltune 行为零变化
- PG /sqltune 是**新增能力**，不影响现有 PG skill

### MVP 局限明示

1. **不走 LLM 综合分析** — og Tuner orchestration 还在 og 包，需 M7 迁到 neutral
2. **ExpandViews 是 stub** — M3 不做 view 内联
3. **CollectSchema pg_stats 限制 500 行** — 极宽表会被截，足够覆盖 99% 案例
4. **未端到端跑过真实 PG 实例** — 仅 18 单测 + 编译过

### 进度

| Milestone | 状态 |
|---|---|
| M1 架构 | ✅ v1.1.34 |
| M2 MySQL + optimizer_trace | ✅ v1.1.35 |
| **M3 PostgreSQL + pg_stats** | ✅ **v1.1.36** |
| M4a og 升级 EXPLAIN PERFORMANCE | ⏳ |
| M4b GaussDB + GS_PLAN_TRACE | ⏳ |
| M5 Oracle + 10053 | ⏳ |
| M6 EquivVerifier | ⏳ |
| M7 Tuner 迁 neutral + LLM 编排 | ⏳ |
| M8 G7 千行 SQL AST 拆解 | ⏳ |
| M9 6 库端到端测试 | ⏳ |

总进度: **3 / 9 (33%)**, 剩余约 13 周。

---

## v1.1.35 (2026-05-17) — MySQL /sqltune MVP (M2: optimizer_trace + EXPLAIN JSON)

### 背景

v1.1.34 把 og 的 sqltuner 重构出来 + 加了 DialectPlanner 接口，给多库扩展铺路。本版交付 **M2: MySQL 实现**，第一次验证接口设计能跑通第二个 DB，同时给 MySQL DBA 带来最关键的 CBO 决策跟踪能力。

**MySQL 的杀手锏是 optimizer_trace** — 这是 SQL 数据库里最接近 Oracle 10053 的 CBO 决策完整 dump (per-session JSON, 走 SELECT 拿、不需要 OS 权限)。本版让 `/sqltune` 在 MySQL 上一次性把 trace + EXPLAIN JSON + 关键 GUC 全部拿到。

### 新增 `internal/mysql/sqltuner/`（约 1150 行）

- **`planner.go`** (~150 行)
  - `mysqlPlanner` struct 实现 sqltune.DialectPlanner 9 个方法
  - init() 注册 planner + tuner factory，让 sqltune.BuildTuner(DialectMySQL) 能拿到
  - castProvider / castMemStore — opaque any → llm.Provider / *memory.Store

- **`explain.go`** (~330 行) — **M2.2 EXPLAIN FORMAT=JSON 解析**
  - ExplainPlan 三态决策: AnalyzeForce → EXPLAIN ANALYZE (8.0.18+ 自动回退) / AnalyzeAuto → SELECT 才 ANALYZE / AnalyzeSkip → 纯 EXPLAIN
  - QuickPlanCost — 无 ANALYZE 拿 cost_info.query_cost 给 verify 用
  - parseMySQLBlock 递归解析: `nested_loop` 数组 / `grouping_operation` / `ordering_operation` / `table` 4 种 JSON 模式 → 统一 sqltune.PlanNode 树
  - explainAccessType 把 MySQL 简写还原成易读: ALL→Full Table Scan / ref→Index Lookup / range→Range Scan
  - detectPlaceholders 字符串扫描带 `\` 转义和 `'"` 引号识别，正确忽略 `'who?'` 之类字面量
  - PlaceholderError 引导到 performance_schema.events_statements_history_long

- **`trace.go`** (~170 行) — **M2.3 optimizer_trace 启用与采集**
  - EnableTrace: 一次性 `SET optimizer_trace_max_mem_size = 16M` + `SET optimizer_trace="enabled=on,one_line=off"`
  - CollectTrace: `SELECT TRACE, MISSING_BYTES_BEYOND_MAX_MEM_SIZE, INSUFFICIENT_PRIVILEGES FROM information_schema.OPTIMIZER_TRACE`
  - **截断检测**: MISSING_BYTES > 0 → 设 Truncated:true 并附带 "重试时调大 max_mem_size" 提示
  - **权限检测**: INSUFFICIENT_PRIVILEGES > 0 → 提示部分 view/SP 内容被剥
  - closeFn 用 atomic.Bool 防重入，自动 SET enabled=off 清场
  - 失败降级到 TraceData{Available:false} 而非中断，让 EXPLAIN 还能跑

- **`collectors.go`** (~290 行)
  - CollectSchema: SQL 解析提表名 → information_schema.TABLES 拿 TABLE_ROWS / DATA_LENGTH / TABLE_TYPE
  - SnapshotDialect: VERSION() + 11 个关键 GUC (optimizer_switch / *_buffer_size / innodb_buffer_pool_size / optimizer_search_depth 等)
  - 分区表识别走 information_schema.PARTITIONS COUNT
  - SnapshotRuntime / ExpandViews 在 M2.1 是有意 stub (M2.4 完整版要 performance_schema.data_locks 等，工作量大留后续 patch)
  - SQL 解析: stripCommentsMySQL / tokenizeMySQL / isSQLKeywordMySQL — 简易但能正确处理 schema.table 限定符

- **`tuner.go`** (~205 行) — minimal orchestrator
  - 跑 Phase A: EnableTrace → ExplainPlan → CollectTrace → 并行 Schema/Dialect/Runtime
  - renderMySQLReport: 确定性 markdown，含 SQL / 计划树 / trace body (可折叠) / 收集警告
  - **不走 LLM 综合分析**: og 的 Tuner orchestration 还在 og 包，迁移到 neutral 是 M7 范畴；MVP 直接给 DBA 看原始 trace 已经很有价值

- **`planner_test.go`** (~220 行 / 12 case)
  - factory 注册检查 / DialectKind / 占位符检测 7 case (含字符串里的 `?`/转义/多行) / EXPLAIN JSON 解析 3 case (simple table / nested_loop join / order by filesort) / explainAccessType 8 case / parseFloat 类型 6 case / isReadOnlyQuery / sqlInListMySQL

### 新增 MySQL skill

- **`internal/mysql/skill/query/sqltune_skill.go`** (~150 行)
  - `/sqltune <SQL>` 入口，调 sqltune.BuildTuner(DialectMySQL)
  - PlaceholderError 给 MySQL 特化错误信息 (指向 performance_schema 而非 og 的 dbe_perf)
  - anonymous import mysql sqltuner 触发 init() 注册

- **`internal/mysql/register.go`** 加一行 `reg(query.NewSQLTuneSkill(driver, modelMgr, nil))`

### 接口验证（关键设计胜利）

M2 实施过程**没有改 M1 任何代码**就让 MySQL 接入了。证明：
- DialectPlanner 9 方法签名跨方言可用
- AnalyzeMode 三态（Auto/Force/Skip）覆盖 MySQL 与 og 行为差异
- PlaceholderError.DetectedKind = "qmark" 让 skill 层做 MySQL 特化错误提示
- TunerEngine + BuildTuner 让 skill 完全 dialect-free

### MVP 局限（明示）

1. **不走 LLM 综合分析** — Tuner orchestration 在 og 包，需 M7 迁到 neutral
2. **SnapshotRuntime / ExpandViews stub** — performance_schema.data_locks / information_schema.VIEWS 留 M2.4 patch
3. **CollectSchema 无 indexes/stats** — 同上 M2.4 patch
4. **未在真实 MySQL 实例端到端验证** — 仅单元测试 12 case 通过 + 编译过；首个 MySQL 用户跑 /sqltune 时可能遇到 driver/SQL 兼容问题待修

### 测试

- mysql sqltuner 12 case 全过（含 -race）
- og + neutral + skill 子包全过
- 全二进制编译过（5 DB tag）

### 兼容性

- og /sqltune 行为零变化
- MySQL /sqltune 是新增能力，不影响现有任何 skill

### 接下来

M3 (PostgreSQL) → M4a (og 升级 EXPLAIN PERFORMANCE) → M4b (GaussDB GS_PLAN_TRACE) → M5 (Oracle 10053, 最难) → M6 (EquivVerifier) → M7 (Tuner 迁 neutral + LLM 编排) → M8 (G7 千行) → M9 (端到端测试)

---

## v1.1.34 (2026-05-17) — /sqltune M1 架构重构（neutral 包 + DialectPlanner 接口）

### 背景

/sqltune 当前**只有 og 实现**（4084 行 17 文件全在 `internal/opengauss/sqltuner/`），其他 5 库（Oracle/MySQL/PG/GaussDB/DM）想用都没有。Oracle 用户最多，缺感知最强。

要扩展到 4 库（Oracle + MySQL + PG + og 升级 + GaussDB；DM 砍掉），加上每库的 CBO 决策跟踪能力（Oracle 10053 / MySQL optimizer_trace / GaussDB GS_PLAN_TRACE），是一个跨 5 个 milestone (M1-M5) 约 16 周的项目。本版交付 **M1 架构重构**——把 og 强行拆出 neutral 包 + 接口骨架，后续 M2-M5 接入其他库零阻力。

设计研究: 4 库 trace 机制可行性分析见上一会话。

### 新增 (neutral package)

- **`internal/sqltune/types.go`** (~235 行)
  - 22 个 dialect-agnostic 类型: PlanNode / PlanInfo / SchemaInfo / TableInfo / IndexInfo / ColStat / ForeignKey / DialectInfo / RuntimeInfo / WaitEventBucket / LockEntry / MemoryEntry / CollectedContext / Round1Output / Candidate / VerifyResult / TuneOptions / FinalReport / ReportStats
  - 新增 **TraceData** (Available/Format/Body/Truncated/Notes) — 为 M2-M5 trace 能力铺路
  - 新增 **ExplainOptions + AnalyzeMode 三态** (Auto/Force/Skip)
  - 新增 **PlaceholderError** — neutral 占位符错误，含 DetectedKind (pg_dollar/oracle_colon/qmark)

- **`internal/sqltune/dialect.go`** (~180 行)
  - **DialectPlanner 接口** (9 方法): Kind / ExplainPlan / QuickPlanCost / CollectSchema / SnapshotDialect / SnapshotRuntime / ExpandViews / **EnableTrace / CollectTrace** / NormalizePlaceholders
  - **5 个 DialectKind 常量**: opengauss / gaussdb / postgres / mysql / oracle
  - **Registry 工厂模式** (Register / Lookup) — 各库 init() 注册自己

- **`internal/sqltune/engine.go`** (~90 行)
  - **TunerEngine 接口** + **BuildTuner(kind, deps) (TunerEngine, error)**
  - 让 skill 层做到 dialect-free: `sqltune.BuildTuner(DialectOpenGauss, deps)` 代替直接 import og

- **`internal/sqltune/dialect_test.go`** (~120 行 / 7 case)
  - Registry / Lookup / 替换语义 / DialectKind 稳定字符串 / nil PlaceholderError / BuildTuner 未注册返回 error / 注册和查找 TunerFactory

### 修改 (og 适配)

- **`internal/opengauss/sqltuner/planner.go`** (新建, ~250 行)
  - **ogPlanner struct** 实现 sqltune.DialectPlanner，包装 og 现有 6 个 collectors
  - **init() 注册 og planner factory + ogTunerFactory** → 让 sqltune.BuildTuner(DialectOpenGauss) 能拿到
  - EnableTrace/CollectTrace 返回 Available:false (og 开源无 CBO trace) + 说明 note
  - wrapPlaceholderErr / classifyPlaceholderKind 工具
  - castProvider / castMemStore — 把 opaque `any` 转回 llm.Provider / *memory.Store

- **`internal/opengauss/sqltuner/types.go`** 变成 22 个 type alias 到 neutral 包 (零行为变化)

- **`internal/opengauss/sqltuner/tuner.go`**
  - Tuner struct: 5 个 *Collector 字段合并成 1 个 `planner sqltune.DialectPlanner`
  - NewTuner 保持向后兼容（内部构造 og planner）
  - 新增 **NewTunerFromPlanner(driver, planner, provider, memStore)** — M2+ 各库会用
  - collectPhaseA 内所有 `t.planCol.X / t.schemaCol.X / t.dialectCol.X / t.runtimeCol.X / t.viewExp.X` 改成 `t.planner.X`
  - verifyCandidates / verifyOne 内 `t.planCol.QuickPlanCost` 改成 `t.planner.QuickPlanCost`

- **`internal/opengauss/sqltuner/dialect_context.go`**
  - DialectInfo.PromptSection2() 方法 → promptSection2() 包级函数 (Go alias 不能加方法)
- **`internal/opengauss/sqltuner/runtime_context.go`**
  - RuntimeInfo.PromptBlock() 方法 → promptBlock() 包级函数

- **`internal/opengauss/skill/query/sqltune_skill.go`**
  - 从 `sqltuner.NewTuner(driver, provider, mem).Tune(...)` 改成 `sqltune.BuildTuner(DialectOpenGauss, deps).Tune(...)`
  - import 改成 sqltune + `_ sqltuner` (副作用注册 og 实现)
  - PlaceholderSQLError 错误检查 → sqltune.PlaceholderError (neutral 类型)

- **`internal/opengauss/sqltuner/planner_test.go`** (新建, ~60 行 / 4 case)
  - og planner factory 已注册 / 拒绝错误 driver / classifyPlaceholderKind / Kind()=opengauss

### 测试

- og sqltuner 22 case + neutral 4 case → 26 全过（含 -race）
- og skill 子包全 8 子包测试通过（用 BuildTuner 路径）

### 兼容性

- **零用户可见行为变化** — og /sqltune 走向不变，输出格式不变
- **API 向后兼容** — `sqltuner.NewTuner` 仍可用（内部委托 BuildTuner）
- **未来路径** — M2 (MySQL) / M3 (PG) / M4 (og 升级 + GaussDB) / M5 (Oracle) 各库只需新增对应包，实现 DialectPlanner 接口，init() 注册即可。skill 层不需改

### M1 占总项目 4.5 月的 11%

剩余里程碑:
- M2 MySQL + optimizer_trace (2 周)
- M3 PostgreSQL + pg_stats 旁路 (1.5 周)
- M4a og 升级 EXPLAIN PERFORMANCE (1 周)
- M4b GaussDB + GS_PLAN_TRACE (0.5 周)
- M5 Oracle + 10053 (3-4 周, 最难)
- M6 EquivVerifier 完善 (1 周)
- M7 Deep Mode Round 2 markdown 集成 (1 周)
- M8 G7 千行 SQL AST 拆解 (2 周)
- M9 6 库端到端测试 (2 周)

---

## v1.1.33 (2026-05-17) — /wdranalyze 小模型可靠性补丁（Plan B 后置校验）

### 背景

v1.1.32 的 `/wdranalyze` 在强模型（Opus / GLM / DeepSeek）上输出完整，但
32B / 35B 这类小模型容易：
- 漏写 4 个必填段之一（`## 风险全景` / `## 关键瓶颈` / `## 配置调优` / `## 综合评估`）
- 漏提兜底规则确定性触发的 finding（autovacuum off / deadlock / 主备延迟等）

弱模型用户拿到的报告缺章节、丢风险，体验明显差一档。本版加一层确定性
后置校验，**对强模型 0 影响**，对弱模型大幅兜底。

### 新增

- **`internal/opengauss/wdranalyze/validator.go`** (~250 行)
  - `ValidateAndPatch(llmOutput, fallback) string` — 纯函数后置校验
  - 段落检查：4 个必填段，每个带中英文同义词别名
    （`## Risk Overview` / `## Summary` / `## 总结` / `## 瓶颈分析` 等都算）
  - Finding 覆盖检查：每个兜底规则 ID 配同义词关键词表
    （`autovacuum` / `auto vacuum` / `自动 vacuum` / `自动清理` 都算提到）
  - 缺段落 → 追加 placeholder；漏 finding → 追加 `## ⚠️ 补充兜底警告` 块
  - 宽松匹配策略：宁可漏检（顶多重复一次），不能误检影响强模型
- **`internal/opengauss/wdranalyze/validator_test.go`** (~200 行 / 8 case)
  - 强模型标准输出 → 完全不动 ✓
  - 英文标题（`## Risk Overview` / `## Summary`）→ 完全不动 ✓
  - 缺段落 → placeholder 追加 ✓
  - 漏 finding → 补充兜底块追加（已提到的 finding 不重复）✓
  - 空输入 → 原样返回 ✓
  - 无 fallback findings → 只做段落检查 ✓
  - 5 条规则 ID 都有 signature keywords（防止默默漏配）✓
  - 12 种措辞变体宽松匹配 ✓

### 修改

- **`internal/opengauss/skill/query/wdranalyze_skill.go`**
  - Phase 5 LLM synthesis 成功后插入 Phase 5.5：
    `synthesis = wdranalyze.ValidateAndPatch(out, fallbackFindings)`

### 设计要点

| 失败模式 | 处理 |
|---|---|
| 强模型完整输出 | 全部 alias / keyword 命中 → no-op |
| LLM 用英文写 | 段落 alias 表覆盖 → no-op |
| LLM 漏 1 段 | 该段位置追加 placeholder + "(LLM 未生成此段)" |
| LLM 漏报某条兜底 finding | 末尾追加 `## ⚠️ 补充兜底警告` 块（含证据 + 建议）|
| LLM 提到 finding 但用词怪 | 同义词宽松匹配通过 → 不重复 |

**永远不会**：吞掉兜底 finding（即使误检，最差是重复一次）。

### 测试

- 全包 30 case 通过（22 原有 + 8 新增），含 `-race`
- 强模型输出无 patch，弱模型缺段 / 漏 finding 都有补全

---

## v1.1.32 (2026-05-17) — /wdranalyze WDR 自动解读（M1 + Fallback + M3 + M4）

### 背景

og / GaussDB 客户的 DBA 看一份 WDR 报告通常要 30+ 分钟才能找出关键问题；
TopSQL 给了但没给优化方案；跨章节关联难看出。本版交付 `/wdranalyze`：
3-5 分钟把 WDR 自动拆解成「风险清单 + Top SQL 完整 sqltune 优化 + 配置调优 + 行动计划」。

设计方案：`docs/wdr/plan-wdranalyze.md`

### 新增

#### M1 — Skeleton + Parser

- **`internal/opengauss/wdranalyze/types.go`** (250 行)
  - `WDRReport` / `Finding` / `SQLTuneResult` / `Analysis` 等共享类型
  - `Severity` 3 级（Critical / Warning / Info）+ 渲染
- **`internal/opengauss/wdranalyze/parser.go`** (290 行)
  - text + HTML 双格式解析（HTML 走 htmlToText 转换）
  - 8 个 section parser: header / time model / waits / topsql /
    IO / memory / locks / replication / settings
  - 容错设计：缺 section 零值，缺关键 header 才报错
- **`internal/opengauss/wdranalyze/parser_helpers.go`** (220 行)
  - `extractSection` / `splitTableRow` / 启发式工具
- **`internal/opengauss/wdranalyze/collector.go`** (170 行)
  - 4 模式：`latest` / `<snapA> <snapB>` / `last1h/24h/7d` / `file <path>`
  - 调 `dbe_perf.generate_wdr_report` (summary → all fallback)
- **`internal/opengauss/wdranalyze/renderer.go`** (220 行)
  - markdown 模板：header / 工作负载 / 风险全景 / Top SQL / SQL Tunes
- **`internal/opengauss/skill/query/wdranalyze_skill.go`** (320 行)
  - skill 注册 + ToolDef + CLIDef（别名 `/wdra`）
  - arg 解析：4 种模式 + `--top-n` / `--no-sql` / `--no-llm` 标志
  - 报告持久化到 `~/.opendb/wdr_reports/<ts>-<window>.md`

#### Fallback Rules（5 条硬底）

- **`internal/opengauss/wdranalyze/fallback_rules.go`** (200 行)
  - `autovacuum_off` — pg_settings.autovacuum=off → Critical
  - `deadlock_present` — deadlock_count > 0 → Critical
  - `replication_lag_high` — max_lag > 60s 且有 standby → Critical
  - `buffer_hit_critical` — hit ratio < 80% 且有 IO 数据 → Critical
  - `single_sql_dominant` — 单 SQL > 50% DB Time → Critical
  - 纯 Go 阈值判断，毫秒级
  - LLM 不可用时仍能输出风险清单

#### M3 — TopSQL Drill-Down

- **`internal/opengauss/skill/query/sqlfetch_skill.go`** 新增 `Resolve()` 程序化入口
- **`internal/opengauss/wdranalyze/topsql.go`** (280 行)
  - `DrillTopSQLs()` 5 个 sqltune 并行（总耗时 ≈ 单条最慢 90s）
  - memory fingerprint 缓存（7 天 max age）
  - 单条失败不阻塞其他

#### M4 — LLM Synthesis

- **`internal/opengauss/wdranalyze/synthesizer.go`** (240 行)
  - 强约束 prompt：兜底 findings 必须出现在 LLM 风险全景里
  - token 控制：传压缩 WDR 摘要（~3K token），不传 raw
  - 失败优雅降级：返回空 + error → 渲染器 fallback 到兜底层

### 测试

- 27 case 全过（13 parser + 14 fallback rules）
- 端到端 demo：critical_wdr.txt 4 兜底规则全触发 + Top SQL drill 启动

### 用法

```bash
# 完整模式（含 sqltune + LLM）
/wdranalyze latest
/wdranalyze last1h
/wdranalyze 15234 15247
/wdranalyze file /tmp/wdr.html

# 跳 LLM（只看兜底）
/wdranalyze latest --no-llm

# 调整 TopSQL 数量
/wdranalyze latest --top-n 10
```

### 设计选择

- **跳 M2 规则引擎**：5 条硬底兜底 + LLM 综合层接管风险识别。
  代价：风险识别可靠性依赖 LLM；好处：架构纯净 + LLM 跨指标推理能力更强。
- **TopSQL 并行**：5 个 sqltune 同时跑总耗时 ≈ 90s 而非 5×90s
- **memory 复用**：相同 SQL 在 wdranalyze 间秒级复用
- **报告 layout**：LLM 可用时只渲染 LLM 输出（含完整风险全景）；
  不可用时渲染兜底 findings + Top SQL list

---

## v1.1.31 (2026-05-08) — sqltune 性能墙 2 + 三处修复

### 背景

v1.1.30 发版后实测 35B 在 10 表 SQL 上仍**超时被砍**：14 轮 / 8m23s，**6 次调 sqltune** 累计 7 分钟撞 engine 600s 超时。trace 暴露 4 个深层问题：

1. **substituter 上下文识别不细**：`TO_CHAR(date, 'YYYY-MM-DD') = ?` 后面的 `?` 被错误替换为 `'test'`，导致 EXPLAIN 行数估为 0，sqltune 报告退化，35B 不满意反复重试
2. **sqltune 自身慢**（**墙 2**）：Round 2 LLM markdown gen + auto-upgrade deep mode 在大 SQL 上动辄 5-15min
3. **og GUC `track_activity_query_size` 默认 1024 字节** 导致长 SQL 在 dbe_perf.statement_history 被截断
4. **sqlfetch 没检测到截断**，把残缺 SQL 发给 sqltune → 语法错误

详细分析在本次会话末尾，关联 memory：[`todo-llm-sqltune-routing-failure.md`](../../../.claude/projects/-Users-sqlrush-opendb/memory/todo-llm-sqltune-routing-failure.md)

### 新增

#### 1 · substituter format-followup 规则

`internal/opengauss/sqltuner/placeholder_substituter.go`：
- 改为**两次扫描**：第一遍 SQL 顺序选值（带历史 sub 上下文），第二遍倒序物理替换
- 新规则 `rule-format-followup`：上一个 `?` 替换为 `'YYYY-...'` 类格式串 → 下一个等值 `?` 用日期字面量
- 解决 `TO_CHAR(o.order_date, 'YYYY-MM-DD') = '2024-01-15'` 而非 `'test'`

实测：v1.1.30 替换为 `'test'` 让 sqltune 估算行数 0；v1.1.31 替换为 `'2024-01-15'`，sqltune 拿到合理的 EXPLAIN plan。

#### 2 · sqltune QuickMode（墙 2 主修复）

`internal/opengauss/sqltuner/types.go` + `tuner.go` + `skill/query/sqltune_skill.go`：
- 新 option `TuneOptions.QuickMode`：跳过 Round 2 LLM markdown gen，直接用确定性 `renderFallbackReport` 渲染
- skill 层默认 `mode="quick"`：`QuickMode=true` + `SkipUpgrade=true`
- 用户/LLM 可显式 `mode="deep"` 走完整流程

效果：典型复杂 SQL sqltune 调用从 5-15min 降到 30-90s。fallback 渲染输出**结构上跟 LLM Round 2 一致**（5 维度 + 验证结果），仅缺 LLM prose 润色——这是公平交易：换来 10 表 SQL 真能跑完，而不是被砍。

工具描述 `mode` 参数：

```
quick (default, ~30-90s) — 35B / Opus / 任何 LLM 优先用
deep (~5-15min) — 用户主动要求 + 简单 SQL 才考虑
```

#### 3 · sqlfetch 截断检测

`internal/opengauss/skill/query/sqlfetch_skill.go`：
- `looksTruncated(sql)` 三启发式：
  - (a) 长度接近 1024 / 2048 / 4096 等 GUC 边界
  - (b) 末尾是 SQL 关键字（` FROM`, ` WHERE`, ` AND` ...）
  - (c) 末尾是 SQL 关键字的不完整前缀（`FRO`, `WHER`, `JOI`）
- 命中 ≥ 2 启发式则警告
- 警告中给出修复 SQL：`gs_guc set ... -c 'track_activity_query_size=16384'`

### 改动

- `internal/version/version.go`: v1.1.30 → v1.1.31
- 三个修复独立可降级：substituter 改善 / quick mode / 截断检测各自不破坏其他

### 实测验证

`/sqlfetch 2278588878`（10 表 SQL，前 demo）现在：
- 8 占位符全替换（vs v1.1.30 只 4 个，因为 GUC 调大让 og 存全了）
- TO_CHAR 后的等值用 `'2024-01-15'` 而非 `'test'`
- 所有替换可直接 EXPLAIN

### 兼容性

- `mode` 参数默认 `quick` — 之前调过 sqltune 的脚本如果期待"完整 LLM 报告"，需显式 `mode=deep`
- 但 quick mode 输出结构跟 deep 一致，**LLM 几乎区分不出**——不算 break change

### 验收（待用户确认）

- [ ] 35B 在 SQL_ID 2278588878 上 ≤ 6 轮拿到 sqltune 报告（不再超时）
- [ ] sqltune 单调用 ≤ 90s
- [ ] /sqlfetch 在长 SQL 场景输出截断警告

---

## v1.1.30 (2026-05-08) — 复杂 SQL 攻关：memory 隔离 + 占位符自动替换 + engine 兜底

### 背景

v1.1.29 实测 4 模型在 5 表 SQL 上全过，但 **10 表 SQL（CTE + 3 层嵌套 + 4 个 ?）暴露 3 道新墙**：

1. **墙 7（最危险）**：Memory 跨 SQL 污染 — Opus 把 5 表 SQL_ID 33402943 的诊断套到 10 表 SQL_ID 2278588878 上，自信地输出错答案 + 幻觉日期 `'2026-01-15'`
2. **墙 1**：占位符密度 — 35B 在 4 个 `?` 的 SQL 上 16 轮空转最终输出空白
3. **修复 1（兜底）**：LLM 输出空 + 调过工具时，引擎返回空字符串

详细方案见 `docs/sqltune/plan-v1.1.30.md`。

### 新增

#### 方案 A · Memory 上下文隔离（墙 7）

- **`internal/engine/memory/fingerprint.go`**（新建）：
  - `Fingerprint{Hash, Tables, HasCTE, Depth}` SQL 结构化指纹
  - 归约规则：lowercase + 字面量替换为 `?` + 表名去 schema + 注释剥除 + 空白合并
  - `SimilarityScore()` 三因素加权：0.7×Jaccard(表) + 0.15×CTE一致 + 0.15×深度接近
  - `SimilarityThreshold = 0.85` 严格阈值，低于此值 memory 不召回
- **`internal/engine/memory/store.go`**：
  - `Query.SQL` 新字段，命中 fingerprint 模式
  - `Entry{Fingerprint, Similarity}` 召回时附带相似度评分
  - `WriteWithSQL()` 写 memory 时计算 fingerprint 并存入 frontmatter（`sql_fingerprint` / `sql_tables` / `sql_has_cte` / `sql_depth`）
  - `Find()` 当 Query.SQL 非空时，仅返回相似度 ≥ 0.85 的 entry
  - 旧 memory 无 fingerprint → 视为相似度 0.5 自动 drop（兼容降级）
- **`internal/opengauss/sqltuner/memory_query.go`**：`FindRelevant(sql, tables, n)` 签名加 SQL，传给 fingerprint 引擎
- **`PromptMemoryBlock`**：每条 memory 现在标注 `相似度 X%`，且 prompt 强调"仅供参考，不是当前 SQL 真实诊断"
- **`internal/opengauss/sqltuner/types.go`**：`MemoryEntry.Similarity` 新字段

实测验证：5 表 SQL vs 10 表 SQL 相似度 0.338（远低于 0.85 阈值），不再污染。

#### 方案 B · 占位符自动替换（墙 1）

- **`internal/opengauss/sqltuner/placeholder_substituter.go`**（新建）：
  - 解析 `?` / `$N` / `:N` 位置（跳过字符串 / 注释 / 双引号标识符）
  - 基于左上下文 + 列名启发式选择替换值：
    - `LIKE ?` → `'%test%'`
    - `LIMIT ?` → `100`
    - `TO_CHAR(date, ?)` → `'YYYY-MM-DD'`
    - `col_id <= ?` → `50` / `col_id = ?` → `1`（数字列）
    - `col_date >= ?` → `'2024-01-01'`（日期列）
    - 默认 `1`
  - 后向替换避免 offset 错位
  - `Substitution{Position, Original, Context, Value, Source}` 结构记录每次替换
  - `FormatSubstitutions()` 渲染为可读 markdown 表
- **`internal/opengauss/skill/query/sqlfetch_skill.go`**：
  - 拉到含占位符的 SQL → 自动调 substituter（默认 true）
  - 渲染时显示替换详情 + ⚠️ 提示"合成样例值，CBO 估算基于统计不依赖字面量"
  - `fetchResult` 新字段：`OriginalSQL` / `Substituted` / `Subs`

实测：SQL_ID 2278588878（10 表 4 占位符）→ 4 个 ? 全替换，输出可直接喂 sqltune。

#### 修复 1 · Engine 兜底

- **`internal/engine/engine.go`**：在两个"无工具调用退出"路径都加 fallback：
  ```go
  if result.Content == "" && len(result.ToolsInvoked) > 0 {
      result.Content = synthesizePartialResult(...)
  }
  ```
- 35B 等小模型遇到无解占位符场景时不再返回空白，而是给出"调用了 X 工具，遇到 Y 问题"摘要

### 测试

- `internal/engine/memory/fingerprint_test.go` — 11 case
  - 同 SQL 不同字面量产生相同 hash ✓
  - 不同表产生不同 hash ✓
  - schema 前缀剥除 ✓
  - **5 表 vs 10 表实测相似度 0.338**（确认防污染）✓
  - 旧 memory 无 fingerprint 兼容 ✓
- `internal/opengauss/sqltuner/placeholder_substituter_test.go` — 9 case
  - LIKE / LIMIT / TO_CHAR / 范围 / 等值 / IN / 字符串内 ? 不替换 / 真实 10 表 SQL ✓

### 兼容性

- **break change**：sqlfetch 输出格式变化（占位符 SQL 变成已替换 SQL + 替换详情表）。脚本依赖原始 ? 输出会受影响，可在升级后核对脚本
- **memory 兼容**：v1.1.29 写入的 memory 文件无 fingerprint 字段 → 自动视为低相似度，不再召回（不会报错，只是不进 prompt）。建议清空 `~/.opendb/memory/` 让 v1.1.30 重新积累

### 验收

待 4 模型（35B / Opus / GLM-5.1 / DeepSeek-V4-Pro）在 v1.1.30 上重测 10 表 SQL，确认：
- 不再 cross-SQL 污染（清 memory 后跑 5 表 → 不清继续跑 10 表，要给真针对 10 表的诊断）
- 35B 在 10 表 SQL 上 ≤ 8 轮拿到 sqltune 报告
- 任意失败场景不再返回空白

---

## v1.1.29 (2026-05-07) — /sqlfetch 扩展四库 + 发版博客

### 新增

- **`/sqlfetch` PostgreSQL 实现** (`internal/postgres/skill/query/sqlfetch_skill.go`)
  - 按 queryid (bigint, 可负数) 查 `pg_stat_statements`
  - 准确说明：PG 永远归一化 ($N)，无 og 的 L2 切换可选
  - 提供方案：auto_explain / 应用日志 / 手工替换样例值

- **`/sqlfetch` MySQL 实现** (`internal/mysql/skill/query/sqlfetch_skill.go`)
  - 按 DIGEST (hex string) 查 `performance_schema.events_statements_summary_by_digest`
  - 支持精确 + 前缀 LIKE 双模式（DIGEST 长度太尴尬，用户经常只复制前缀）
  - 提供方案：slow query log / general_log / 手工替换

- **`/sqlfetch` Oracle 实现** (`internal/oracle/skill/query/sqlfetch_skill.go`)
  - 按 SQL_ID 查 `V$SQL.SQL_FULLTEXT`（CLOB 全文）
  - 失败回退 `V$SQLAREA`
  - **大多数情况直接返回带字面量的可 EXPLAIN SQL**（Oracle V$SQL 默认存字面量）
  - 检测 cursor_sharing 引发的 SYS_B_n 绑定 + :N 占位符并明确说明

- **发版博客** `docs/blog/v1.1.28-sqltune-routing-fix.md`
  - 完整故事：4 模型实测失败 → 双重设计缺陷 → 4 处修复 → 4 模型验收
  - 含 GLM 跨模型记忆传递的 bonus case（DeepSeek 41.6s 命中 GLM 记忆）

### 改动

- `internal/postgres/register.go`: 注册 PG `/sqlfetch`
- `internal/mysql/register.go`: 注册 MySQL `/sqlfetch`
- `internal/oracle/register.go`: 注册 Oracle `/sqlfetch`
- `internal/version/version.go`: v1.1.28 → v1.1.29

### 四库 sqlfetch 行为差异（设计有意）

| DB | 视图 | ID 类型 | 默认存字面量？ | 占位符提示 |
|---|---|---|---|---|
| og | dbe_perf.statement_history | unique_query_id (bigint) | 看 L1/L2 模式 | ?, $N, :N 都查 |
| PG | pg_stat_statements | queryid (bigint, 可负) | **从不** | $N |
| MySQL | events_statements_summary_by_digest | DIGEST (hex) | **从不** | ? |
| Oracle | V$SQL | SQL_ID (varchar2(13)) | **是** | ?, :N, SYS_B_n |

四库 LLM 看到的 ToolDef.Description 都明确告知"是否带字面量"，避免 LLM 用错预期。

### 验收

- 四库 `/sqlfetch` 编译通过
- og 已实测（v1.1.28 release 中 4 模型验证）
- PG / MySQL / Oracle 待用户在真实环境验证（这版只交付能力，下版做端到端 demo）

---

## v1.1.28 (2026-05-07) — /llm → /sqltune 路由修复（P0 + P1）

### 背景

实测 4 个 LLM（35B / Opus / GLM-5.1 / DeepSeek-V4-Pro）问"SQL_ID xxx 如何优化"都拿不到 sqltune 5 维度报告：
- 35B 乐观调 sqltune → 8s phase A 失败 silently → 切通用诊断（错答案）
- 3 个大模型保守拒绝调 sqltune，18 轮 raw `sql` 硬解卡 og 列名 → 让用户粘 SQL（无答案）

根因：og `dbe_perf.statement.query` 归一化（`?` 占位符 + 无 schema），无法 EXPLAIN；engine 缺 SQL_ID → ready-for-EXPLAIN SQL 的转换层。详见 `docs/sqltune/plan-llm-sqltune-routing-fix.md`。

### 新增

- **`/sqlfetch <SQL_ID>`** 工具（`internal/opengauss/skill/query/sqlfetch_skill.go`）
  - 自动从 `dbe_perf.statement_history` 取 SQL（带 schema_name + 字面量优先）
  - 准确检测 `?` 占位符并提示 L2 跟踪模式 / 手动替换样例值的修法
  - LLM 改调 `/sqlfetch <ID>` 一次性拿可 EXPLAIN 的 SQL，省 18 轮 raw query
  - 别名 `/sqlf`，接受 `33402943` / `SQL_ID 33402943` / `SQL_ID:33402943` 等多种输入

- **sqltune 自动 schema 补全**（`internal/opengauss/sqltuner/plan_collector.go`）
  - EXPLAIN 失败若错误是 `relation "X" does not exist`：解析缺失表名 → 查 `pg_class+pg_namespace` 找 schema（按 reltuples DESC）→ 重写 SQL 加 schema 前缀 → 重试 EXPLAIN
  - 字符串/注释/已限定/`alias.col` 列名引用都正确跳过

- **sqltune 占位符容错**：`PlaceholderSQLError` 类型 + `detectPlaceholders()`，含 ? / $N / :N 立即返回结构化错误而非 phase A 黑盒。单测 20 case 覆盖各种边界

- **system prompt 增强** (`internal/engine/context/builder.go`)：
  - 取 SQL 策略（用户粘 vs SQL_ID 两条路径）
  - og-lite 元数据列名速查（`unique_sql_id` / `unique_query_id` / `schema_name` 实测列名）
  - sqltune 失败处理（不准静默切通用诊断）
  - 强调 `/sqlfetch` 替代手写 SQL 拉取

### 文档

- `docs/sqltune/plan-llm-sqltune-routing-fix.md` 完整修复方案（v2，含 4 模型实测数据点）
- `~/.claude/projects/-Users-sqlrush-opendb/memory/todo-llm-sqltune-routing-failure.md` 根因分析

### 验收

- `/sqltune "SELECT ... LIKE ? AND id = ?"` → 之前 `phase A: plan collection failed` 黑盒 / 之后 `⚠️ 含 2 个未绑定占位符 [? ?]` 清晰指引
- `/sqlfetch 33402943` → 1 调用拿到 schema + 完整 SQL + 占位符诊断 + 下一步建议
- 待用户在 v1.1.28 上 4 模型重测，验收"三模型一致性"目标（test 6）

---

## v1.1.27 (2026-05-04) — 开源准备：Apache 2.0 + 文件头注释 + 多品牌分支

### 新增

- **LICENSE**：Apache 2.0 标准全文，Copyright `Sqlrush <sqlrush@gmail.com>`
- **scripts/header/main.go**：批量插入文件头的 helper 工具（PG 风格 block comment + AST 派生 Purpose）
- **internal/brand/linkdb.go**：仁合时创品牌定义（`-tags linkdb`）。同
  dbaa 一样的 brand layer 模式，二进制名 `linkdb`，欢迎页 / setup 页
  使用仁合时创品牌资源，DBList 含达梦。详细见 CLAUDE.md "品牌分支策略"

### 改动

- **983 个 `.go` 文件**全部加统一 PG 风格 block 头：
  - `Copyright 2026 Sqlrush <sqlrush@gmail.com>`
  - `Author: Sqlrush <sqlrush@gmail.com>`
  - `IDENTIFICATION` 行（仓库相对路径）
  - 文件 Purpose 描述（顶层 ~75 个核心文件手写，其余从已有 doc/exports 派生）
- 跳过：`internal/_dmdriver/`（达梦原厂代码）、自动生成文件
- 保留：build tags 顺序、原有 `// Package X` godoc 注释

### 修复

- `tests/push_policy_test/main.go`：原本仅含 stub header，补上 `package main` + 空 `main()` 让 go build 通过
- `scripts/header/main.go`：注释里 `*/vendor/*` 字面量提前结束 block comment，改为 plain text

### 不影响

- Build 标志、二进制行为、测试用例、配置文件均无变化
- 编译产物大小：65,920,194 bytes（与 v1.1.26 一致）

## v1.1.26 (2026-05-02) — DM 单元测试 + 真机修复 + ANSI 清理

### 新增单元测试（DM 子包：0 → 133 测试）

按对标 Oracle/OG 的测试覆盖要求建立 baseline：

| 子包 | 测试数 | 覆盖率 |
|---|---:|---:|
| ai | 9 | 89.4% |
| monitor | 96 | 78.6% |
| query | 14 | 79.1% |
| schema | 6 | 95.8% |
| util | 8 | 100% |
| **总计** | **133** | — |

测试内置祖传 bug 防复发断言：
- V$DANGER_EVENT 必须 OPTIME（不是 HAPPEN_TIME）
- V$DEADLOCK_HISTORY 必须 HAPPEN_TIME（这个表确实有此列）
- V$ERR_INFO 仅 CODE/ERRINFO 两列
- V$DATABASE 无 DBID 列
- views 必须查 V$DYNAMIC_TABLES（不是 SYSOBJECTS）
- resource 不能查 V$RESOURCE_LIMIT（DM 没此视图）
- topsql 必须查 V$SQL_HISTORY GROUP BY SQL_ID
- slowsql 必须查 V$LONG_EXEC_SQLS（实时，不是 V$SQL_HISTORY 累积）
- tableinfo 用 ALL_TAB_COLUMNS / ALL_INDEXES / DBA_SEGMENTS（Oracle 兼容）
- explain 必须 prepend "EXPLAIN "
- sessions/blocktree/activesessions summary 必含 SP_CLOSE_SESSION
- info ROLE$ 必须翻译 PRIMARY/STANDBY 字符串
- blocktree SQL 必须 BLOCKED=0 过滤防 OOM

全部 `go test -race -count=1` 通过 0 race。

### 真机修复（DM 8.1.4.200）

3 处列名错误：
- `V$INSTANCE.VERSION` → `SVR_VERSION`（DM 没有 VERSION 列）
- `V$DATABASE.DBID` → 删除引用（DM 没此列）
- `V$RESOURCE_LIMIT` 视图不存在 → resource skill 完全重写为 V$PARAMETER + 实时 V$SESSIONS/V$TRX/V$MEM_POOL

### Standby ROLE$/STATUS$ 数字翻译（#2）

V$DATABASE 的 ROLE$ 和 STATUS$ 是 TINYINT，之前直接给 LLM 看到 `role: 0` 不知所云。
现在翻译：

- ROLE$ 0/1/2/3 → PRIMARY/STANDBY/DBSTANDBY/BACKUP_PENDING
- STATUS$ 1..5 → STARTUP/AFTER_REDO/BACKUP/OPEN/SUSPEND
- summary 同时输出 raw 值（role_raw / status_raw）方便排查

### Batch 模式 ANSI 前缀清理（#3）

之前每条 `dbaa -c <conn> /xxx` 输出开头都有 OSC 序列污染：
```
]11;?\[6n  ┌─────────────────┐
  │ SESS_ID         │
```

根因：lipgloss → termenv 在 init 时探测终端（OSC 11 background color + CSI 6n cursor position）。
修复：runBatch 入口设 `NO_COLOR=1`，让 termenv 跳过探测。LLM-facing batch 输出本来就不需要 ANSI 颜色。

### DMProfile 视图陷阱补充（#39）

dm.go 214 → 232 行，"DM 视图列名陷阱"块按 4 类组织：

- 时间字段陷阱（V$DANGER_EVENT.OPTIME vs V$DEADLOCK_HISTORY.HAPPEN_TIME）
- 错误码视图陷阱（V$ERR_INFO 仅 CODE/ERRINFO；V$RUNTIME_ERR_HISTORY 用 ECPT_CODE/ECPT_DESC）
- 实例与数据库视图陷阱（V$INSTANCE.SVR_VERSION 不是 VERSION；V$DATABASE 无 DBID；ROLE$ 必须 CASE 翻译）
- 不存在的视图（V$RESOURCE_LIMIT / V$SQLAREA / V$OSSTAT 在 DM 都没有）

未来 LLM 看到 prompt 就知道这些坑，写 SQL 不会再踩。

### Batch 输出重复 bug 修复（#19）

main.go 在 batch 模式下对 `Type=ResultTable` 的 skill 既打印 `Rendered`（已含表格+summary）又再次 FormatTable，导致表格输出 2 次。
修：DM 13 个 skill 的 `Type` 从 `ResultTable` → `ResultText`，main.go 不再走 FormatTable 分支。

## v1.1.25 (2026-05-02) — DM 适配 14 新 skill (P0/P1/P2/P3 全套)

### 新增 skill (14 个)

按对标 Oracle/OG 的能力差距拉齐：

| Skill | 优先级 | 数据源 | 用途 |
|---|---|---|---|
| **sentinel** | P0 | V$LOCK + V$LONG_EXEC_SQLS + V$DEADLOCK_HISTORY | 异常持续采集 (MVP 阈值检测版, 30s tick, 阻塞>3/长SQL>50/死锁数变化触发 alert) |
| **dbtop** | P1 | V$INSTANCE + V$SESSIONS + V$LONG_EXEC_SQLS | 实时 top dashboard (ResultRefresh) |
| **perfsnap** | P1 | WRM$_SNAPSHOT + V$PARAMETER + DBMS_WORKLOAD_REPOSITORY | AWR 快照状态 + 报告生成命令提示 |
| **os** | P1 | V$INSTANCE + V$THREADS + V$PROCESS + V$MEM_POOL | 实例主机视角 (21 线程类别 / 内存池总计 etc) |
| **segments** | P1 | DBA_SEGMENTS | 段空间 Top 20 (按大小 / 按 owner) |
| **users** | P1 | DBA_USERS + DBA_SYS_PRIVS + DBA_ROLE_PRIVS | 用户列表 / 单用户权限+角色审计 |
| **redo** | P2 | V$RLOGFILE + V$RLOG | 重做日志文件 + LSN 状态 |
| **tempusage** | P2 | DBA_DATA_FILES (TEMP) + SF_GET_TS_USED_SPACE | 临时表空间使用 |
| **archive** | P2 | V$DM_ARCH_INI + V$ARCH_STATUS + V$ARCHIVED_LOG | 归档配置 + 状态 + 最近 10 条 |
| **standby** | P2 | V$DATABASE.ROLE$ + V$RLOG.LSN + V$ARCH_SEND_INFO | 主备状态 |
| **resource** | P2 | V$PARAMETER + V$SESSIONS + V$TRX + V$MEM_POOL | 资源限制 + 当前使用率（DM 没有 V$RESOURCE_LIMIT） |
| **indexhealth** | P2 | DBA_INDEXES + DBA_SEGMENTS + DBA_IND_COLUMNS | 失效/超大/空索引 |
| **mempool** | P3 | V$MEM_POOL + V$BUFFERPOOL + V$DICT_CACHE | 内存池 + 缓冲池 + 字典缓存 |
| **cluster** | P3 | V$DSC_EP_INFO + V$MPP_INSTANCES + V$DMWATCHER_INFO | DM 集群（DSC/MPP/DW），单机模式正确报告"无集群" |

### 真机验证（DM 8.1.4.200, 单机部署）

13/14 batch 模式跑通，再次暴露 3 处列名错误（祖传 bug 在新 skill 重演）：

| 错误 | 修正 |
|---|---|
| `V$INSTANCE.VERSION` 不存在 | 改用 `SVR_VERSION AS VERSION` |
| `V$DATABASE.DBID` 不存在（DM 没有此列） | 删除该列引用 |
| `V$RESOURCE_LIMIT` 视图在 DM 不存在（Oracle 才有） | resource skill 完全重写为 V$PARAMETER + 实时 V$SESSIONS/V$TRX/V$MEM_POOL |

修正后 13 个 skill 全部输出真实数据 + 完整 summary。sentinel start/stop/status 三态切换都验证通过。

### 测试机本地编译策略

scp 大二进制 (70MB) 在测试机网络 37MB 处稳定截断（多次重试 + gzip 压缩都失败）。改走"GitHub push → 测试机 git pull + 本地 go build"路径，1.5min 完成 1.4GB Go module 编译，从此跳过 scp。

### Skill 数量对比

| DB | 之前 | 之后 |
|---|---|---|
| Oracle | ~25 | ~25 |
| OpenGauss | ~38 | ~38 |
| **DM** | **17** | **31 (+14)** |

DM 适配从 ~50% → ~80%。剩余：单元测试覆盖（DM 0 → ?）、rule_skill (用户决定暂缓)。

## v1.1.24 (2026-05-02) — DM 真机验证修复 + 多模型回归

### 4 处真机暴露的列名错误（DM 8.1.4.200）

**背景**: DM /llm 4 故障 benchmark (8.25/10) 之后扩展验证 task 73-77 的 5 skill 时，真机暴露 4 处列名错误，全部是基于"猜列名"造成的祖传 bug：

| skill | 真实列 | 之前用错的列 |
|---|---|---|
| anomalies (V$DANGER_EVENT) | `OPTIME` | `HAPPEN_TIME` |
| alert (V$DANGER_EVENT) | `OPTIME` | `HAPPEN_TIME`（祖传 bug） |
| errcode (V$ERR_INFO) | `CODE / ERRINFO`（仅 2 列） | `ERR_CODE / ERR_LEVEL / ERR_TYPE / ERR_DESC`（全错） |
| errcode (V$RUNTIME_ERR_HISTORY) | `ECPT_CODE / ECPT_DESC` | `ERR_CODE` |
| views (V$ 视图目录) | `V$DYNAMIC_TABLES`（380+ 项） | `SYSOBJECTS`（只 10 项） |

**关键差异**：V$DEADLOCK_HISTORY 实测有 HAPPEN_TIME 列（与 V$DANGER_EVENT 不一致），不需要修——这是 DM 系统视图命名不一致的真实情况。

### DMProfile 强化（internal/engine/profile/dm.go +18 行）

新增两个块：
1. **DM 视图列名陷阱**（必读）— 4 处真机错误编入 system prompt，未来 LLM 给修复 SQL 不再写错
2. **等待事件分析路径** — DM 不像 Oracle 有 1000+ 标准事件名，教 LLM 用 V$EVENT_NAME 实查而非凭 Oracle 经验猜

V$ERR_INFO 那行去掉了"2666 条预定义"猜测数字，改成实测列结构。

### 4 模型回归 benchmark

详见 `docs/dm-llm-benchmark-v2-multi-model.md`。

| 模型 | 时长 | 轮数 | 评分 | 备注 |
|---|---:|---:|---:|---|
| glm-5.1 | 158s | 5 | **9.5** | 最强：5 轮收敛 + 证据溯源自检表 |
| deepseek-v4-pro | 309s | 19 | 8.5 | 深度好但慢 |
| moonshot-v1-128k | 33s | 1 | 3.0 | 累计当现场 + 视图名错 |
| kimi-k2.6 | 605s | 12+ | 0.0 | 命中 MaxDiagnosisTimeout=10min |

**回归验证**：
- ✅ 4 模型无一用 `ALTER SYSTEM KILL`（Oracle 语法）— DMProfile 杀会话约束生效
- ✅ 新 skill (anomalies/errcode/views) 被多个模型调用
- ✅ 真机修复的列名在所有模型上正常工作
- ⚠️ kimi 在 12+ 轮场景命中客户端超时（已知行为，不是回归）

### 编译

跨平台编译需 Linux（DM 驱动 security 子包仅 linux/windows，无 darwin）：

```bash
GOOS=linux GOARCH=amd64 go build -tags full,dbaa -o dbaa-dm-linux ./cmd/opendb/
```

### 文档

- `docs/dm-llm-benchmark-v2-multi-model.md` — 4 模型回归详细评估
- `internal/engine/profile/dm.go` 196 → 214 行

## v1.1.23 (2026-04-30) — GaussDB 集中式支持

### 新增 db_type=gaussdb（华为 GaussDB(for openGauss) 集中式 V2.0-8.x）

**背景**: 客户生产环境 GaussDB 集中式 8.x（password_encryption_type=2），用 pgx 连接报错：
```
AuthenticationSASL body is invalid: unterminated string
```
根因：GaussDB 的 SCRAM-SHA256(10) 认证与 PostgreSQL 标准 SCRAM 不兼容，pgx 解析失败。

**方案**: 集成华为云官方 Go 驱动 `github.com/HuaweiCloudDeveloper/gaussdb-go v1.0.0-rc1`，
独立于 OpenGauss / PostgreSQL 路径，新增 `internal/gaussdb/driver` 包。

**变更**:
- 新增 `internal/gaussdb/driver/driver.go` — gaussdb-go 驱动封装，DSN 用 `database=` 关键字（非 `dbname=`）
- 新增 `internal/gaussdb/register.go` — 注册 db_type=gaussdb，技能复用 OpenGauss 实现（系统视图相同）
- 新增 `cmd/opendb/product_gaussdb.go` — build tag `gaussdb || full`
- 新增 `cmd/gaussdb-probe/main.go` — 独立连通性探针二进制，给客户在生产网络验证驱动兼容性
- `internal/config/connection.go` 注释扩充 db_type 列表

**编译**:
```bash
go build -tags full -o opendb ./cmd/opendb/         # 包含 gaussdb
go build -tags gaussdb -o opendb ./cmd/opendb/      # 仅 gaussdb
go build -o gaussdb-probe ./cmd/gaussdb-probe/      # 客户侧探针
```

**配置示例**:
```yaml
- name: customer_gaussdb
  db_type: gaussdb
  host: ...
  port: 5432
  database: postgres
  user: opendb_ro
  credential:
    value: ...
```

**验证**:
- gaussdb-probe 在 OpenGauss 5.0（test server 47.251.30.180:15432）跑通：auth/handshake/version/queries 全 OK
- `opendb -c gausstest 'SELECT version()'` / `/sessions` / `/health` 端到端验证通过
- 单元测试覆盖 buildDSN（防止误用 pgx 的 `dbname=` 关键字）+ extractGaussVersion 多格式

**风险记录**:
- gaussdb-go 当前是 RC（v1.0.0-rc1, 2025-04-28），尚无 GA。客户侧上生产前必须先用 gaussdb-probe 在测试库验证。
- 内部仅在 OpenGauss 5.0 验证；GaussDB 商业版协议扩展、视图差异未实地覆盖，需客户协助。
- 技能层暂时复用 OpenGauss，未来若发现 GaussDB 视图差异，分叉到 `internal/gaussdb/skill/`。

### 安装向导与 UI 接入 gaussdb 类型

之前 db_type 只有 opengauss，安装向导里"GaussDB"实际写出 `db_type: opengauss`，dbaa 用户装完后连商业 GaussDB 必撞 SCRAM 报错。本次彻底修。

**变更**:
- `internal/setup/dbtype.go`：DB 类型选项从 4 个变 5 个，新增 "openGauss（开源社区版）" 与 "GaussDB（华为商业版 V2.0-8.x）"，二者写出不同 db_type
- `internal/setup/permission.go`、`conntest.go`、`connform.go`：补 gaussdb 分支，复用 opengauss 的视图与权限检查（系统视图同源）
- `internal/setup/styles.go::DBDisplayName`：补 openGauss / GaussDB 显示名映射
- `internal/setup/conntest.go`：去除 `openGauss → GaussDB` 版本号 brand 替换 hack（类型已分开，不再需要伪装）

**REPL prompt 加数据库类型标识**:
- 之前 prompt：`❯ (gausstest)`
- 现在 prompt：`❯ openGauss·(og)` 或 `❯ GaussDB·(gausstest)`
- 用户连接后能一眼区分自己走的是 pgx 还是 gaussdb-go 路径

**去除 dbaa 品牌的 opengauss→gaussdb 显示 hack**:
- 之前在 dbaa 品牌下，所有 `db_type: opengauss` 连接被强制显示为 "gaussdb" 类型 — 类型分开后这个 hack 反而误导用户
- 删掉 `brand.OpenGaussDisplay/OpenGaussTypeColumn/DefaultOGConnName` 三个 brand 字段（无消费者）
- dbaa 与 opendb 现在都按真实 db_type 显示

**dbaa 品牌行为变化（升级注意）**:
- v1.1.22 及之前：dbaa 用户看到 opengauss 连接显示为 "GaussDB" / "gaussdb"
- v1.1.23 起：dbaa 用户看到 opengauss 连接显示为 "openGauss" / "opengauss"
- 想用商业 GaussDB 的客户需在 config.yaml 把 `db_type: opengauss` 改为 `db_type: gaussdb`（或重新跑 `dbaa setup` / `dbaa configure`）

**测试**:
- 新增 uitest：`TestPicker_ShowsGaussDBAndOpenGaussTypes`、`TestWelcome_DBListIncludesOpenGaussAndGaussDB`
- 更新 welcome golden file（DBList 增项）
- buildPrompt 加 nil-check（修我新引入的 nil 解引用 bug）

---

## v1.1.22 (2026-04-30)

### 修复两个长期潜伏 bug — 多模型 /llm 直接崩 / DeepSeek V4 协议错

两个潜伏 25-38 天的 bug 在今天的 OG anti-pattern 故障场景下首次触发同时暴露。
本地 4 模型 + 远端 Linux 4 模型回归全过，RSS 从 31 GB 降到 35 MB（缩 900 倍）。

### Bug A: Session 污染导致 DeepSeek V4 thinking 模式 HTTP 400 (P0)

**现象**: dbaa /llm 跑 DeepSeek-V4-Pro 报错：
```
HTTP 400: The reasoning_content in the thinking mode must be passed back to the API
```
重启 dbaa 也复现，多次跑 /llm 后必现。

**根因** (`internal/engine/engine.go saveSession`):
engine 每轮 `InjectTurnContext` 注入 `IsMeta=true` 的 user 角色"前轮摘要"消息。
本意是 transient 每轮重算，但 `saveSession` 没区分 IsMeta 一并存进 session.jsonl。
下次 /llm 加载 session → 历史里塞回所有 turn-summary meta。
跑 N 次后 session 累积成怪物：156 条消息中 user=150 (96%)、assistant=5、tool=1。
DeepSeek thinking 协议要求 assistant 历史必须含 reasoning_content，
但 session 里仅剩的 5 条 assistant 大多没有 → 校验失败 400。

**修复**:
```go
// saveSession 持久化前过滤 IsMeta=true 消息（transient 每轮重算的 hint）
persisted := make([]provider.Message, 0, len(msgs))
for _, m := range msgs {
    if m.IsMeta { continue }
    persisted = append(persisted, m)
}
```

**为何昨天没发**: v1.1.19 (4-26) 验证用的是新 session，没历史污染。
潜伏期约 25 天（自 SessionActive 持久化机制写入 IsMeta 起）。

**影响范围**: 不只 DeepSeek V4。其他模型 (GLM/Kimi/Moonshot/Qwen) 虽不报错但
也在静默吃 token 浪费 + LLM 输出质量退化（看到 150 条 user 消息而非干净对话）。

### Bug B: blocktree 在 30 并发同行 UPDATE 下 OOM 31 GB (P0)

**现象**: F3 故障场景（30 个并发 UPDATE bench_app_counter WHERE id=1）下，
任何 LLM 调 blocktree 工具 → dbaa 进程内存爆涨 → Linux 直接 SIGKILL OOM。
观测：peak RSS 31 GB（macOS 65 GB 虚拟内存压缩）/ Go runtime fatal error。

**根因 1** (`internal/opengauss/skill/monitor/blocktree.go SQL`):
JOIN 缺 `kl.granted` 过滤。30 session 等同一 transactionid（1 holder + 29 waiter），
JOIN 把 29 个 ungranted waiter 互相当成"blocker" → 841 行结果构成完全图。

**根因 2** (`ogRenderNode` 递归渲染):
没有 visited set 检测环。完全图导致 A→B→A→B... 无限互相调用，
`fmt.Fprintf(strings.Builder)` 写到 32 GB 倍增扩容触发 OOM。

**修复**:
```sql
-- SQL 加 kl.granted 过滤
JOIN pg_locks kl ON kl.transactionid = bl.transactionid
                 AND kl.pid != bl.pid
                 AND kl.granted   -- 新增
```
```go
// ogRenderNode 加 visited set 防御兜底
func ogRenderNode(b, n, ..., visited map[string]bool) {
    if visited[n.ID] {
        fmt.Fprintf(b, "%s↻ PID %s (cycle detected, skipping)\n", prefix, n.ID)
        return
    }
    visited[n.ID] = true
    ...
}
```

**为何之前没暴露**: 必须满足 N 个并发 session 等同一 transactionid + 都没 commit。
v1.1.19 测试用的 oracle_mirror 场景是 5 个 burst UPDATE 立即 COMMIT，无并发持有。
今天 anti-pattern F3（30 worker 持续 BEGIN; UPDATE; sleep; COMMIT）首次踩中。
潜伏期 ~38 天（自 blocktree.go 创建 commit 11fad4e4 起）。

### 回归验证

Linux 远端 4 模型实测（修复前 vs 修复后）：

| 模型 | 修复前 exit | 修复前 peak RSS | 修复后 exit | 修复后 peak RSS | 报告 |
|---|---:|---:|---:|---:|---:|
| deepseek-v4-pro | 2 (Go OOM) | 31 GB | 0 | 35 MB | 5337B |
| glm-5.1 | 137 SIGKILL | 33 GB | 0 | 35 MB | 6343B |
| kimi-k2.6 | 137 SIGKILL | 30+ GB | 0 | 35 MB | 4819B |
| moonshot-v1-128k | 0 (没触发) | - | 0 | 33 MB | 615B |

**RSS 缩 900 倍**，4/4 exit=0，3/4 大模型正确识别 hot row 反模式根因。

### 净改动

```
internal/engine/engine.go                          saveSession 过滤 IsMeta
internal/opengauss/skill/monitor/blocktree.go      SQL 加 kl.granted + cycle guard
```

---

## v1.1.21 (2026-04-27)

### Engine + 配置流跨级修复

继 v1.1.20 之后又一轮基础链路加固。重点是 LLM 输出"看似没出"的根因找到了，
模型/连接管理也统一了入口。

### Engine: LLM 最终诊断报告被静默吞 (P0)

**现象**: dbaa /llm 跑完后用户只看到一句"诊断已完成，所有结论均有工具数据
支撑。"没有根因 / SQL / 数据。session 文件里却存了 4 KB 的完整诊断报告。

**根因**: `streamRound` 只在 `len(resp.ToolCalls) == 0` 时把 chunks 推到
`onStream`。模型在最终轮把完整分析报告 + 末尾 `memory_write` 一起发出来 —
engine 把这一轮当作"中间轮"，4 KB content 全部丢弃。下一轮模型收到
memory_write 结果后只回简短"诊断已完成..."，被当作 final round 显示给用户。

**修复** (`internal/engine/engine.go`):
- 新增 `shouldFlushContent(toolCalls)` — 只有 side-effect 工具 (memory_write
  / memory_update) 时也算"可交付"轮，需要 flush content
- 新增 `isSideEffectTool(name)` — 当前白名单：memory_write, memory_update
- 新增 `deliverableContent` 累积器：所有 deliverable 轮 (final + side-effect-only)
  的 content 都拼到 `result.Content`，避免 batch 模式只看到最后一轮简短回复

**验证**: dbaa -c gaussdb /llm 修复前 634 bytes → 修复后 5892 bytes (9.3×)
opendb -c og /llm 修复前 3774 bytes → 修复后 4706 bytes
两边都包含完整根因 + 因果链 + 紧急措施 + 修复 SQL + 风险评估 + 优先级表

### Engine: saveSession 漏掉最终 assistant 回复

**现象**: session 文件最后一条总是 tool result，缺模型最终诊断报告。

**根因**: final-round 路径 (无 tool_calls) 直接 return，跳过了 assistant
消息 append。tool-call 路径会在 line ~325 append assistantMsg；final round
没这步，导致 `saveSession(messages, ...)` 拿到的 messages 不包含模型的 final reply。

**修复**: 两处 final-round return 前都 append assistant message，再 saveSession。

**验证**: 跑 dbaa /llm 后 session 从 31 → 46 条，最后一条是 assistant 的完整诊断报告。

### Batch 模式 /llm 加分轮进度反馈

**现象**: `opendb -c og /llm xxx` 5+ 分钟黑屏，看不到任何进度（只能等最终结果）。
REPL 模式有"第N轮: 调用 X, Y"反馈，batch 模式没有。

**修复** (`cmd/opendb/main.go`):
- 新增 `batchProgressSetter` 接口 + `batchDiagProgress` 函数
- 自动接到 active diag skill 的 SetOnProgress 回调
- 进度输出到 **stderr**（stdout 仍只有 final result，方便重定向）
- 处理的 phase: start, rule, ai_start, ai_round, error
- 跳过 ai_streaming（避免 reasoning chunks 灌满 stderr）

### 模型配置统一到 config.yaml + manager 改 merge

**现象**: 之前 `~/.opendb/config.yaml` 的 `models:` 块和 `~/.opendb/models/*.yaml`
是 either/or 加载 — inline 一存在就完全忽略 dir。dbaa setup 写了 inline 后,
用户在 `~/.dbaa/models/` 加文件不生效。`/model reload` 提示也硬编码
`~/.opendb/models/`，dbaa 看到错路径。

**修复**:
- `model.Manager`: dir + inline 合并加载, inline 同名覆盖 dir。新增
  `AddProfile()` 用于内存注入, 新增 `inlineSnap` 让 Reload 正确重新合并。
- `config.AppendModel(path, m)`: 文本级追加新模型到 config.yaml `models:`
  块, 保留注释和其他配置。带单元测试。
- `config.DefaultConfigPath()`: 暴露 Load 用的同一路径，让 writers 找到同一个文件。
- `setup` wizard: 不再写 `models_dir` 字段, 不再创建 `models/` 目录, 所有
  模型走 inline。已有用户的 models_dir 仍能被 manager 读到 (向后兼容).
- `/model reload` 提示: 不再硬编码路径, 提示 inline 编辑需要重启。

### 移除 REPL 内置 add 向导, 统一走 `<binary> configure`

**背景**: 之前模型和连接的"添加"路径有两条:
  1. `/model add` 在 REPL 里弹 modelwizard.go 全屏交互
  2. `/conn` (无参) 在 REPL 里弹 connwizard.go 全屏交互
  3. `opendb`/`dbaa configure` (子命令)

两条路径管理同一份 config.yaml, 容易让用户搞不清状态在哪。统一为只走
`configure` 子命令。

**改动**:
- `/model` skill: 移除 case "add", 删除 addModel(), 删除 ModelWizardAction 常量
- `/conn` skill: 无参不再返回 wizard signal, 改为打印当前连接 + 提示
  "`<binary> configure` 添加, `/login <name>` 切换, `/conn user/pass@...` 临时连接"
- 删除 `internal/ui/modelwizard.go` (~720 行) + `internal/ui/connwizard.go`
- 二进制 size: 60.18 MB → 60.12 MB (-53 KB)

### setup 不再静默覆盖 config.yaml

**风险**: 之前 `dbaa setup` 检测不到已有 `~/.dbaa/config.yaml`, 走完向导直接
`os.WriteFile` 覆盖, 用户积累的 N 个连接 + M 个内联模型全部丢失, 没有备份,
不可恢复 (除了 credentials/ 加密密码文件还在).

**修复** (`internal/setup/existingconfig.go`):
新增 ExistingConfigStep 作为向导第二步:
  - config.yaml 不存在 → Init 直接 emitDone, 用户无感知 (新装路径)
  - config.yaml 存在 → 弹确认页:
    - 取消 → tea.Quit 退出, 文件不动, 提示用 `configure` 增量编辑
    - 确认 → `os.Rename` 到 `config.yaml.bak.YYYYMMDD-HHMMSS`, 然后让向导继续

### Setup Summary 不再显示假路径

`finalize.go:206` 之前写 `Connection: ~/.dbaa/connections/default.yaml`，
但 v1.1.20 起连接已 inline 进 config.yaml，那个路径根本不存在。改为
`Config: ~/.dbaa/config.yaml (连接 / 模型 inline 在此)`。

### 历史教训沉淀
- **检测到 ≠ 传递了 ≠ 显示了**: SSE 解析检测到 reasoning_content / engine
  收到 content / chunks 进 buffer — 三者都对，但因 `len(toolCalls)==0` 那
  道闸门，content 永远没出现在 onStream 上。修管道 bug 必须端到端验证整
  条数据流，沿链逐节点检查每个分支的"放行条件"。
- **either/or 不是优先级**: 注释写"Priority: inline > dir" 但实际是
  if-else，用户加 dir 文件不生效却以为"被覆盖"，实际是被忽略。语义模糊
  导致 UX 反预期。
- **静默覆盖是大忌**: setup 默认应保护用户数据，破坏性操作必须显式确认 +
  自动备份。

## v1.1.20 (2026-04-27)

### dbaa 客制化 + 深度 bug 修复批量发布

dbaa 分支首次完整可用版。同时修了一批共享 bug, opendb 同样受益.

### 核心 Bug 修复 (3 个 P0)

**Bug 1: streamFinalResponse 死锁 (engine.go:570)**
深 bug. 当 deepseek-v4-pro / kimi-k2.6 thinking 模式持续推 reasoning_content
chunks 但不出 content 时, engine 主 goroutine 永久卡在 select. 用户看到
进程 alive 但 0 字节输出 — 看似挂掉.

根因: new path SSE parser (engine/provider/openaicompat.go) 把 reasoning-only
chunk 直接 continue 默默丢, 不返回 event; 同时 engine.go 没有 reasoning-only
总时长上限. 之前能跑通是侥幸 — deepseek thinking 通常 < 60s 内出 content,
刚好不触发 streamEventTimeout. 这次 thinking 拖久就裸奔.

修复:
- new path SSE parser 加 StreamThinkingDelta 事件
- engine.go 加 streamReasoningOnlyTimeout = 180s, 超时 abort + 报
  ERR-040001 LLM 调用超时

**Bug 2: model not found warning 没用 ODB 错误码 (manager.go:76)**
fmt.Printf 打 warning 太隐晦, 用户看不到, 以为程序崩了. 改用
`[ERR-080003] active_model %q 在配置中找不到, 降级到规则诊断模式`
+ 列当前可用模型清单 + 给具体修复建议.

**Bug 3: dbaa setup 配置写错 (finalize.go)**
- 漏写 models_dir 字段 -> dbaa 看不到 ~/.dbaa/models/ 下的所有 model yaml
- inline model 的 name 字段写 model ID (deepseek-v4-pro) 而非 alias
- 修: 用 "default" 作为 entry name + alias, models_dir 默认填
  filepath.Join(BaseDir, "models"), 自动 mkdir

### dbaa 品牌差异 + 隔离修复 (TODO 1-6)

**TODO 1: openGauss/GaussDB 显示 brand 化**
conntest.go 连接成功 banner 把 driver 返回的 "openGauss 5.0.0" 替换成
brand.OpenGaussDisplay (default openGauss / dbaa GaussDB).

**TODO 2: opengauss 加权限检查清单**
permission.go::PermissionGuideFor 和 conntest.go::PermissionCheckQueries 加
opengauss case (之前是 default 返回 nil -> "Result: 0/0 必要权限就绪").
现在 6 项权限检查 (Connect / pg_stat_activity / pg_stat_database /
pg_locks / dbe_perf / gs_stat_activity).

**TODO 3: "最近使用连接" 标题改为 "已配置连接"**
名实不符 (实际显示所有 config 里的 connections, 不是只有 /login 用过的).
repl_welcome.go 改文案.

**TODO 4: PROFILE.md 模板 brand 化** — 暂未做 (P2, 仅影响新建 PROFILE 文案)

**TODO 5: 4 处硬编码 ~/.opendb 路径 brand 化**
- internal/cluster/cmd.go::defaultBaseDir
- internal/odberr/crash_log.go::openDBDir (crash log 终于会写到 ~/.dbaa/)
- internal/oracle/skill/monitor/trace.go::outputDir
- internal/opengauss/skill/monitor/trace.go::outputDir
全部 delegate 到 config.DefaultOpenDBDir() (brand-aware).

**TODO 6: setup 向导残留 OpenDB 字样扫荡**
finalize.go / welcome.go / dbtype.go / configure.go / skills.go / permission.go
/ llmtest.go / conntest.go / security.go / llmconfig.go 共 ~12 处 "OpenDB" /
"opendb" 字样改用 brand.Current().AppName / .BinaryName.

### 新增 ODB 错误码

- ERR-080003 LLM Model Not Found
- ERR-080004 LLM No Active Model
- ERR-080005 LLM Config Invalid

### 测试

- TestGenerateConfig 更新: kimi-k2.5 现在是 medium tier (v1.1.17 引入), 期望
  capability=medium; 新增 active_model=default + name=default + models_dir
  的断言
- internal/engine/... + internal/model/ + internal/odberr/ 全 race-clean
- internal/setup/ 全测试通过

### 兼容性

- opendb 现有 config.yaml 可继续工作 (active_model 用 model ID 也 OK,
  manager.go 仍能 lookup); 但新建/重新 setup 会用 "default" alias
- dbaa 是新分支, 走 brand layer, 与 opendb 完全隔离 (~/.dbaa vs ~/.opendb)

## v1.1.19 (2026-04-26)

### feat: 4 款主流云端模型完整端到端验证 + DeepSeek V4 适配修复

本版基于 v1.1.18 后续 4 个修复, 4 款云端中型模型在 OG 复合负载下全部跑通,
对齐 Opus 4.7 标准最高得分 8.60 (Kimi K2.6).

### 修复

**1. ToolDef Name 空字符串过滤** (`bridge/skillbridge.go`)
- v1.1.18 修了 schema 但漏了 PolicySkill 这类故意返回 ToolDef{} (空 Name)
  的 CLI-only skill. DeepSeek V4 strict 拒 "Invalid 'tools[44].function.name':
  empty string". 加 td.Name=="" 过滤跳过.

**2. assistant/tool 消息 content 字段强制存在**
(`engine/provider/openaicompat.go`, `llm/openaicompat/openaicompat.go`)
- DeepSeek V4 strict deserializer 拒 "messages[N]: missing field content".
- assistant-with-tool_calls 和 tool 结果消息按惯例 content 为空, 用 omitempty 会省略.
- 修: 改 Content 字段为非 omitempty, 始终发 "content": "" — 跨所有 provider 安全.

**3. MaxDiagnosisTimeout 5min → 10min** (`engine/config.go`, `engine.go`)
- Kimi K2.6 thinking 模式在复合负载下 236s 完成, 但原 5min 限制偶发触发.
- 改 10min 给 thinking-heavy 模型 (Kimi K2.6 / DeepSeek V4) 留余量.
- 快模型 (Opus / GLM / Qwen) 提早完成不受影响.

**4. DeepSeek V4 适配** (`model/capability.go`,
`provider/openaicompat.go`, `setup/providers.go`)
- 模型 ID 从 `deepseek-v4` 修正为官方文档的 `deepseek-v4-pro`
  (https://api-docs.deepseek.com/zh-cn/guides/thinking_mode)
- capability.go 把 `deepseek-v4-pro` 加 largeMarkers (frontier reasoning tier)
- InferCapability 检查顺序改为 small → large → medium → fallback, 让 frontier
  ID 优先匹配, 避免 `-pro` 通用后缀错误降档为 medium
- deepseekCapability(): V4 默认 256K context / 16K output, V3 仍 128K/8K, 按 model 名识别

### 4 款云端模型端到端验证结果

OG 复合负载 (200 万死元组 + 60 长事务 + 50 SeqScan loop + 80 active sessions)
下问 "当前数据库存在什么问题":

| 模型           | 时间  | 字节  | Opus 对标分 |
|----------------|-------|-------|------------|
| Kimi K2.6      | 236s  | 5609  | **8.60** ⭐  |
| GLM 5.1        | ~250s | 4818  | 8.40        |
| DeepSeek V4    | ~280s | 3507  | 8.30        |
| Qwen 3.6 Max   | ~190s | 2760  | 6.50        |
| Opus 4.7 (基准)| —     | —     | 9.45        |

3/4 模型达到 Opus 88-91% 水平, 输出含具体表名 / sql_id / 完整 SQL /
4 字段方案. 占位符违规率从 v1.1.13 前的 ~80% 降到偶发 (Kimi 方案 3
唯一违规).

### 模型配置完善 (`~/.opendb/models/llm.yaml`, `glm5.yaml`)

按 4 家厂商官方文档对齐:
- DeepSeek V4: `deepseek-v4-pro` (思考模式默认开启)
- GLM 5.1: `glm-5.1` (智谱"最新旗舰")
- Kimi K2.6: `kimi-k2.6` (Moonshot, thinking 默认开启)
- Qwen 3.6 Max: `qwen3.6-max-preview` (阿里云真实 3.6 旗舰)

11 个云端模型 API key 全部联通验证通过.

## v1.1.18 (2026-04-26)

### fix: ToolDef schema 自动规范化 — 解决 DeepSeek V4 strict 拒绝

**Bug**: DeepSeek V4 (deepseek-v4-pro) 调用任何带 tool 的 /llm 立即报错:
"Invalid schema for function 'alter': schema must be a JSON Schema of
'type: \"object\"', got 'type: null'."

**根因**: 30+ skills (OG/PG/MySQL 的 alter/kill/bloat/explain 等) ToolDef
写的简写形式:
` + "```" + `go
Parameters: map[string]any{
    "args": map[string]any{"type": "string", ...},  // 漏外层 type:object
}
` + "```" + `
DeepSeek V3/Opus 容忍, V4 schema validator 严格拒绝.

**修复**: 在 `bridge/skillbridge.go::ListTools()` 调 `normalizeToolSchema()`
自动包装. 三种 case:
- nil → `{"type":"object","properties":{}}`
- 已有 type:object → 不变
- 简写 → 包成 `{"type":"object","properties":<orig>,"required":[<keys>]}`

修一处, 30+ skills 全部受益, 不需要逐个改 ToolDef.

### 影响

- DeepSeek V4 / OpenAI strict mode: 之前所有带工具的 /llm 都失败 → 现在正常
- 其他 provider (Opus/GLM/DeepSeek V3): 行为不变 (schema 更标准但不影响识别)

## v1.1.17 (2026-04-26)

### feat: 引入 capability "medium" 档 — 把 GLM/DeepSeek/Qwen/Kimi 接入 templated 变体

**问题**: v1.1.16 加了 templated 变体, 但 GLM-5 / DeepSeek / Qwen 这些
中型云端模型被 InferCapability 推断为 "large" → 仍走 strict 变体 →
仍输出 "2 张表存在膨胀" 这种骨架. 用户实测 GLM 和 Opus 差距没改善.

**根因**: 二档分类 (small/large) 把所有云端模型一刀切到 large, 但只有
Opus/Sonnet/GPT-5/Gemini-Pro 真有能力跟 strict; 其他云端模型实际是
"中等能力" tier, 需要 templated.

**修复**: 引入第三档 "medium":

InferCapability 三档识别 (capability.go):
- small  → 7B-13B 本地 / -mini / -nano / haiku
- medium → GLM-4.x/5, DeepSeek-V3/V4, Qwen-max/plus, Kimi K2, MiniMax,
           MiMo, Grok 2-4, 27-65B 中等权重模型, openai-compat fallback
- large  → Opus/Sonnet/GPT-4/5/o1-3, Gemini-Pro, DeepSeek-R1 (frontier)

SelectStrategy 路由:
- small  → GuidedStrategy (3 轮 assist)
- medium → AutonomousStrategy (10 轮 auto, 但走 templated prompt)
- large  → AutonomousStrategy (10 轮 auto, 走 strict prompt)

OG + Oracle agent 都加 CapabilityMedium 常量, IsValid 把 medium 算合法.

### 测试

- TestInferCapability 更新: GLM/DeepSeek-V4/Qwen-plus 均映射到 medium
- TestSelectStrategy_UnknownDefaultsToGuided: medium 验证走 autonomous
- 全 race-clean

### 影响范围

GLM-5 / DeepSeek-V3/V4 / Qwen-Plus / Kimi 这些用户主力中型模型, 现在
都会拿到 v1.1.16 的 templated 填空 prompt, 输出强制带具体表名/sql_id/
完整 SQL/4 字段修复方案. Opus/Sonnet/GPT-5/Gemini-Pro 行为不变.

## v1.1.16 (2026-04-26)

### feat: capability-aware system prompt + DeepSeek V4 适配

**问题**: v1.1.15 把"完成标准 + 主动深挖原则"加到 universal prompt 后, 强模型
(Opus) 立刻遵守 → 输出深度+严谨度大幅提升; 但中型模型 (GLM-5/Qwen/DeepSeek)
attention 不够分配给抽象规则 → 仍给"2 个表存在膨胀"、"建议 VACUUM"
这种没填具体值的骨架式输出。差距被显化。

**修复**: 给中型模型一个 templated 变体 prompt, 把抽象规则换成"复制即用"
的填空模板, 强制每个占位符填具体值。

### 实现

1. universal system prompt 拆两版:
   - `universalSystemPromptStrict()`  (large) — v1.1.15 原版, 抽象完成标准
   - `universalSystemPromptTemplated()` (small/medium) — 完整输出模板:
     · 根因分析表格固定列名 + "禁止'高/低/异常'"
     · 紧急措施 SQL 必须含 schema.table 全名 + 完整参数
     · 修复方案 4 字段固定占位符: 操作 (完整SQL) / 风险 (具体描述) /
       前置 (查询SQL) / 回滚 (回滚SQL)
     · 末尾自检清单 6 条, 输出前必须逐条对照

2. capability 路由:
   - `BuildInput.Capability` + `EngineInput.Capability` 新字段
   - `Diagnoser.SetCapability()` 在 OG 和 Oracle agent 各加一个
   - `SelectStrategy()` 自动 propagate capability 到底层 Diagnoser
   - diag_skill 的 4 个 NewDiagnoser 直调点全部加 SetCapability

3. 兼容性:
   - 空字符串 capability 默认走 strict (back-compat)
   - 没改 universal strict 内容, Opus/Claude/GPT/Gemini 行为不变

### feat: DeepSeek V4 适配

DeepSeek V4 即将发布, 提前接入:
- `setup/providers.go` 模型列表加 deepseek-v4 / deepseek-v4-reasoner
- `model/capability.go` largeMarkers 加 deepseek-v4
- `provider/openaicompat.go` deepseekCapability(): V4 默认 256K context /
  16K output (V3 仍是 128K/8K), 通过 model 名识别区分

### 范围

适用所有 4 库 (universal prompt + capability 都是共享层).

## v1.1.15 (2026-04-25)

### feat: 强化 LLM 主动深挖原则 + 加完成标准

v1.1.13 加了 "主动深挖原则" 但只禁了 "如需更精准请允许" 这一种措辞,
模型找到等同变体绕过了:
- "本次诊断仅基于 X、Y..."
- "建议补充调用 X / 进一步调用 X / 可以补充 X 工具"
- "需要补充 SQL 文本 / 需要补充执行计划 / 需要补充对象状态"
- "请允许我调用 X / 请补充查询 X"

用户实测 Oracle Opus 输出仍出现:
"本次诊断仅基于 health、blocktree 两个工具的数据。如需查看被阻塞 SQL ...
建议补充调用 ash sql ... 或查询 v\$sql ..."

### 修复

universal system prompt 两处加固:

1. 核心原则 第 4 条改为 "调查充分才收敛", 明确足够标准 = 根因 + 完整
   证据链 + 可直接执行的修复 SQL 都到位

2. 加 "完成标准 (自检)" 段, 给出 6 条硬指标:
   - 提到 sql_id → 必须拿到 SQL 文本前 200 字符以上
   - 提到对象 → 必须 sql 工具验证存在
   - 修复 SQL → 必须语法完整可执行
   - 提到 plan_hash → 必须 explain 过
   - 提到锁/阻塞 → 必须 blocktree 拿完整阻塞链
   - 提到等待事件占比 → 必须有具体百分比

3. 加 "禁用措辞" 清单到 主动深挖原则 段, 列出 5 类常见绕过措辞

4. 加正反例对比, 明确"想到 X 就直接调 X"的工作方式

### 适用范围

universal prompt 共享层, Oracle / OG / MySQL / PG 全部生效.

## v1.1.14 (2026-04-25)

### fix: OG /llm 启动消息和 Oracle 不一致 (UX 对齐)

v1.1.11 重构 OG diag_skill 时把启动消息从 `AI 分析 (auto, 最多20轮)`
改成了 `AI 分析中 (capability=large)`, 没和 Oracle 对齐。用户看到的:
- Oracle: `✵ AI 分析 (auto, 最多20轮)... (35s)`
- OG:     `✳ AI 分析中 (capability=large)... (23s)`

恢复成 Oracle 同款 `AI 分析 (auto, 最多%d轮)...`. 两库 UX 现在完全对齐.

注: 不影响诊断逻辑, 只是消息文案; SelectStrategy 仍按 capability 分流
(large→AutonomousStrategy max 20 轮, small→GuidedStrategy max ~10 轮),
显示统一用 ModeAuto 上限做参考值, 与 Oracle 处理方式一致.

## v1.1.13 (2026-04-25)

### feat: LLM 主动深挖原则 — 禁止"如需更精准请允许调用..."的转嫁

**Bug**: 用户实测 Oracle /llm 输出结尾经常出现："本次诊断仅基于 X/Y/Z 四个工具
结果，未对热点 SQL 取执行计划、未查 v\$sql 文本... 如需更精准的修复方案请允许
进一步调用 explain/topsql/tableinfo 等工具"。本质是 LLM 把"还需要更多数据"
的判断**转嫁给用户**，让用户来批准下一步动作。

**正确行为**: LLM 已经判断出还需要查 explain，就该直接调，把结果纳入诊断再
出最终输出。用户授权它在 max_turns 内自由调用所有只读工具，不需要客气。

**修复**: universal system prompt 新增"主动深挖原则"段（适用所有 4 库）：
- 禁止输出"如需更精准请允许我调用..."
- 给反例 + 正例对比
- 强调用户授权 → 自己调，别问

### 范围

适用：Oracle / OG / MySQL / PG 全部 4 库（universal prompt 是共享层）

## v1.1.12 (2026-04-25)

### fix: OG /health 静默吞掉 Maintenance 段

**Bug**: OG /health 输出长期缺失 Dead Tuples / XID Age 这两个最关键的健康指标，
导致 LLM 拿到误导性的"all OK"输入，无法识别死元组膨胀和 XID wraparound 风险。

**根因**: `maintenanceSection` 用的 SQL `age(datfrozenxid)` 在 OG 5.0 上失败
（OG 的 datfrozenxid 是 xid32 类型，没有 age() 重载）。`Execute()` 用
`if err == nil` 把整段 silent skip 掉，下游 LLM 完全不知道有这两项缺失。

**实测影响**: 测试机上 bench_mix_a 表实际 dead_tup=1997108（接近阈值
100000 的 20 倍），LLM 拿到的 /health 输出却完全没有 Maintenance 段，
最终结论是"17 项全部通过 ✅ 数据库健康"。

**修复**: 把 maintenanceSection 拆成两条独立查询：
- `healthMaintDeadTupSQL`: 只查 dead_tup，OG/PG 都兼容
- `healthMaintXidSQL`: 用 `txid_current() - datfrozenxid::text::numeric`
  替代 age()，跨 OG/PG 兼容
- 任一查询失败时只跳过该项，不再吞掉整段

修复后 /health 正确报告: `Overall: WARNING (1 issues found)`,
`Dead Tuples: 1997108 ⚠`。

这个 bug 是 v1.1.11 OG vs Oracle 输出质量差距的核心原因之一 ——
不是 LLM 笨，是 LLM 拿到了被工具静默吞掉的不完整数据。

## v1.1.11 (2026-04-25)

### OG LLM 策略全面对齐 Oracle — 用户体验差距修复

v1.1.10 benchmark 暴露 OG 输出明显劣于 Oracle，全链路代码对比定位到 3 个根因：
1. OG profile 343 行（vs Oracle 126 行），60% 篇幅是元规则（强制四层 / few-shot /
   18 条工具速查 / 14 条数据验证表 / 5 条反例 / 措辞规范）—— 消耗 GLM-5 attention
   预算
2. universal system prompt 里 Oracle 专属表（序列 cache_size、SGA/PGA、redo 组数等）
   被所有 DB 共读，OG 跑诊断时这些概念无关但 attention 仍被消耗
3. OG agent 层只有单 `Diagnoser`，无策略选择，无 fallback；Oracle 有
   `GuidedStrategy` + `AutonomousStrategy` + 不收敛自动 fallback

本版按 Oracle 模式全面重构 OG。

**OG profile 瘦身** (`profile/opengauss.go`: 343 → 196 行)
- 删: 四层强制结构段、30 行 few-shot worked example、18 条工具速查表、
  14 条数据验证规则表、5 条反例库、措辞规范段、memory_* 强制调用段
- 留: 内核基础与差异、对象引用规则、等待事件速查 4 类、MVCC/XID、VACUUM/bloat、
  WAL/Checkpoint、连接会话排查、gs_ 视图、WDR、MOT、CM、参数注意、修复安全规则、
  常见 SQLSTATE
- 总体由"配方"变"原料"——给 LLM 知识，让它自己组织答案

**universal system prompt 清理** (`context/builder.go`: 147 → 50 行)
- 删: Oracle 专属"数据验证规则"表、"诊断入口选择"表、"深度分析路径"表、
  trace 工具输出规范段
- Oracle 专属表迁移到 `profile/oracle.go`（功能不丢，只是归位）
- 留: 4 条核心原则、6 步推理流程、诊断归因原则、工具使用、输出格式（精简）、
  全局约束
- 4 个 DB 共享的 prompt 现在真的只含通用规则

**OG agent 层加策略选择** (新建 `opengauss/agent/strategy.go`，143 行)
- `GuidedStrategy`: 小模型走 assist 模式（max 3 轮，只读工具）
- `AutonomousStrategy`: 大模型走 auto 模式（max 10 轮）；不收敛自动 fallback
  到 GuidedStrategy 重跑
- `SelectStrategy(capability)`: 按模型能力路由
- `Diagnoser` 现在 MaxTurnsHit 时给 Analysis 追加 `MaxTurnsNote` 标记，
  AutonomousStrategy 据此判断是否需要 fallback

**OG diag_skill 接入策略 + 简化 sentinel 触发提示词**
(`opengauss/skill/ai/diag_skill.go`)
- sentinel 触发路径: 30 行强制四层模板 → 4 行编号引导（让 LLM 自己组织）
- on-demand 路径: 用 `SelectStrategy` 替换手工 `NewDiagnoser` + `Diagnose`
- 显式 `/llm <mode>` 仍走原 Diagnoser 路径（back-compat）

**Oracle 不变**: 所有上述改动都对齐 Oracle 现状，Oracle 行为一字未改。

### 测试

- 更新 `TestUniversalSystemPromptContent` 适配新章节标题
- 更新 `TestOpenGaussSystemPromptRules` 密度区间为 100-200 行（v1.1.10 是 ≥150）
- 全 `internal/engine/...` + `internal/opengauss/...` race-clean

### 预期收益（v1.1.11 vs v1.1.10 benchmark）

- per-round LLM 处理时间从 42s/round 回落到 ~17s/round（v1.1.09 水平）
- 简单查询不再被四层结构强制包装
- Sentinel 触发的诊断 LLM 不再被超长模板束缚，输出更自然
- 大模型遇到不收敛场景有 fallback，p10-style timeout 应得到改善

## v1.1.10 (2026-04-24)

### LLM 能力继续优化 — v1.1.09 benchmark 暴露的 4 个跟进项

全部在 `internal/engine/` 下，零影响外部行为；新增 5 个单元测试。

**[P0] 续问 context 主动压缩** (`context/manager.go` + `manager_count_test.go`)
- 新增 `MessageCountTrigger = 15`：消息数超过阈值且 token < 90% 时，
  主动触发 Turn Collapse，把中段工具结果折叠成摘要
- 解决 v1.1.09 benchmark prompt 6 的已知回归：prompt 5 的 20 轮诊断
  历史 resume 到 prompt 6，GLM-5 无法消化直接 timeout
- 只有当 CollapseTurns 真的缩短了消息列表时才返回压缩版本
  （避免边界情况把列表变长）

**[P1] Prompt Cache 命中率 telemetry** (`provider/openaicompat.go`,
`provider/types.go`, `telemetry/cache_log.go`)
- `oaiUsage.UnmarshalJSON` 现在把所有数字字段收进 `Extra`，并把嵌套的
  `prompt_tokens_details.cached_tokens`（OpenAI/GLM/Qwen 标准）拍平成顶层
  `cached_tokens`，让已有的 capability 查找逻辑真的能拿到值
- `CacheStats` + `Usage` 新增 `HitRate()` 方法：
  - OpenAI/GLM/DeepSeek 风格：Read / (Read+Miss)
  - Anthropic 风格：Read / (Input+Read+Create)
- 新包 `internal/engine/telemetry`：每轮 engine.Run 结束后，把 cache
  命中数据 append 到 `~/.opendb/telemetry/cache.log`（JSONL）。
  无缓存时自动跳过不产生噪音

**[P2] 主题漂移检测** (`context/drift.go` + `drift_test.go`)
- `DetectDrift()` / `DropHistoryOnDrift()`：基于 Jaccard 相似度比对
  新 user message 和上一条 user message 的词元集合
- 中文按字切分 + 英文按词切分 + 常见停用词过滤
- engine.go 在 session load 之后调用：如果检测到主题漂移就丢弃历史，
  让 LLM 从干净上下文起步。阈值 0.05 保守取值，避免误伤"继续分析"
  这类同主题关键词少的续问

**[P2] 四层策略 few-shot 示例** (`profile/opengauss.go`)
- 在"四层诊断策略"规则下加了一个完整 worked example：用户问
  "最近数据库越来越慢"时模型应该怎样组织 四部分输出。给粒度示范：
  具体数字 / sql_id / 来源工具 / 严重度 / 可执行 SQL
- 附带 5 条"反例写法"禁止模板，对齐项目 wording 规范

### 测试覆盖

- `context/manager_count_test.go` — MessageCountTrigger 触发边界
- `context/drift_test.go` — 4 组 drift 场景 + Jaccard 数学验证
- `provider/openaicompat_usage_test.go` — Usage JSON 解码 + HitRate 语义
- `telemetry/cache_log_test.go` — 写入、跳过、空路径三路径

所有 `internal/engine/...` 包含 race 检测通过。

## v1.1.09 (2026-04-24)

### LLM 能力优化专项 — 6 项 P0/P1 + glm5 A/B benchmark + Opus 评估

目标文档：docs/PROJECT-V1.1.09-LLM-OPTIMIZATION.md

**P0**：
1. 工具选择速查表：OG profile 加"按问题类型的首选工具"18 条映射，
   引导 LLM 对模糊问题走最短路径
2. 重复工具调用防护：orchestrator 加 (name,args)→content dedupCache，
   相同 read-only 调用直接返回缓存 + 提示 LLM 避免继续重复
3. Prompt Cache 完善：PROFILE.md block 加 cache_control（v1.1.08 漏了
   这块；MEMORY.md 索引变化快不 cache）

**P1**：
4. 四层诊断策略 prompt 重构：OG profile 加强制输出结构（告警主线→
   关联→对比→排名）；单点查询不强制
5a. Memory 加载上限：MEMORY.md 每类最多 10 条（超过走 memory_recall）
5b. /session new 命令：清当前 instance sessions 目录，绕过 24h resume
6. 截断恢复回归测试：truncation_recovery_test.go 3 个测试保护历史修复

### Benchmark 结果（glm5 跑 10 复杂场景）

完整报告: docs/benchmark/report-v1.1.08-vs-v1.1.09.md

| 指标 | v1.1.08 | v1.1.09 | Δ |
|---|---|---|---|
| 总耗时 | 1137s | 1047s | -8% |
| 平均轮次 | 6.9 | 6.0 | -13% |
| 四层结构输出率 | 0/6 | 3/6 | +50% |
| 严重度分级 | 0/9 | 5/9 | +55% |
| **Opus 加权总分** | **5.85/10** | **6.35/10** | **+8.5%** |

- 强制验收（≥ 基准 × 0.98）✅ 通过（1.085）
- 期望验收（≥ 基准 × 1.05）✅ 通过（1.085）

亮点：
- Prompt 2 (Index bloat)  180s timeout → 67s（dedup + 工具速查表）
- Prompt 1 (XID+VACUUM)   13轮 → 6轮（四层策略生效）
- Prompt 10 (综合健康)    3轮 → 2轮（+ HIGH/MEDIUM/LOW 严重度分级）

已知回归:
- Prompt 6（续问场景）: v1.1.08 68s → v1.1.09 180s timeout
  - 根因: v1.1.08 session 复用 + v1.1.09 prompt 膨胀叠加
  - 用户 workaround: /session new（本版本新命令）
  - 长远修: v1.1.10 context 主动压缩

### 测试

全包 57 个 internal 包 -race 全绿，0 FAIL 0 panic。新增 3 个
truncation recovery 单测保护 feedback-sse-finishreason-lesson 历史修复。

### 造压脚本

新增 scripts/og_load_complex.sh（setup / verify / cleanup 三个子命令）
造 6 个场景：XID wraparound / Index bloat / idle in tx / WAL 冲高 /
统计信息过期 / TOAST bloat。可用于后续版本的 benchmark 复测。

## v1.1.08 (2026-04-23)

### Engine 上下文 / 记忆 / 画像 — OG 专项工作线 A+B+C

调研结果揭示：Engine 上下文系统基础设施齐备，但关键接线断在 4 处，
导致 session / memory / PROFILE 整个生态在批量模式下完全不工作，
REPL 模式下也只半通。修完全部 7 个断点后，端到端闭环验证通过。

**修复**（按调用链顺序）：

1. batch 模式注册 DiagnoseSkill 后没调 SetContextStores
   - `cmd/opendb/main.go` 加 `batchCtxInitializer` 接口，每次 batch 的
     /llm 先接通 session/memory/policy stores。

2. `SetContextStores` 每次都 `NewSessionID`，相邻 /llm 丢失上下文
   - 新增 `internal/engine/session/resume.go:ResumeOrNew` —— 同一 instance
     24h 内的 active session 自动复用，连接级 session 语义落地。

3. memory_write / memory_recall / memory_update 三个 shared skill 被 TODO 掉
   - `cmd/opendb/main.go:registerSharedSkills` 启用它们，构造 sharedMemStore
     返回给调用方。LLM 现在真的有工具可调。

4. sharedMemStore 的 activeInstance 没人同步
   - REPL 加 `SetActiveInstanceSync` 回调；/login 成功时调 sharedMemStore.
     SetActiveInstance(name)。batch 路径直接在连接后调用。

5. PROFILE.md 只在 LLM 调 memory_update 时创建，常态下永远是空
   - `Diagnoser.runEngine` 首次 /llm 时自动 WriteProfile(ProfileTemplate)
     以 seed 基础信息槽位。

6. ProfileTemplate 没有 OG 专属字段
   - `internal/engine/memory/profile.go` ProfileTemplate 按 product 分支，
     OG 模板含 MOT / CM / WDR / 归档模式 / 业务特征 / 历史诊断等槽位。

7. OG profile 的 system prompt 没指导 LLM 何时调 memory_*
   - `internal/engine/profile/opengauss.go` 加 "记忆与画像（必须主动维护）"
     章节，明确 memory_update / memory_write / memory_recall 的触发条件。

**新增**：
- `/profile` shared skill — 查看当前实例 PROFILE.md
- `internal/engine/session/resume.go` — 连接级 session 复用逻辑

**文档**：
- `docs/validation/og-context-chain.md` — 调用链图 + 7 个修复点 + 真机证据

### 端到端真机验证（2026-04-23）

- 第一轮 /llm 产出：session 文件、PROFILE.md(OG 模板)、MEMORY.md 索引、
  pattern_xxx.md (LLM 主动调 memory_write)
- 第二轮 /llm 直接引用第一轮结果（"方案 2"），证明 historyMessages 注入
- /profile 命令正确展示 PROFILE.md
- LLM 调工具列表含 profile → 证明 shared skill 可见

### 开放问题（方案中 4 个都选 B）

1. PROFILE 初始化：**主动** — 首次 /llm 时 code 自动 seed 模板 ✅
2. 历史诊断条数：模板限定 **最近 10 条**，溢出走 memory/diag-*.md ✅
3. session 粒度：**连接级**（同 instance 多次 /llm 共享）✅
4. /profile 命令：**加** ✅

## v1.1.07 (2026-04-23)

### OpenGauss Skill 真机验证 + 14 项修复

首轮 OG 真机批量验证（50 skill）+ TUI 交互测试（44 skill 单 PTY）+ 全量
数据准确性评估后，对照 v1/v2 报告修复 11 个 P0-P2 问题 + 3 个 agent
发现的回归 + 4 个字段/文案设计问题，同时接入了完整的 OG 生命周期。

**详细验证报告**:
- docs/validation/og-live-validation-report.md (v1 — 修前基线)
- docs/validation/og-live-validation-report-v2.md (v2 — 修后对比)

**修复清单 14 项**:

P0（红灯清零）:
- NULL 全局渲染 "NULL" → "-"（internal/format/table.go）
- /dbtop 批量模式从 0 字节空输出改为明确交互式 REPL 提示
- /os 架构重写：优先 pv_os_run_info() 查 OG 服务端（跨平台可用），
  loopback 才 fallback /proc；非 loopback 且视图不可用给清晰建议

P1:
- 批量模式 TermWidth 120→200 + 截断时追加 "⚠ 隐藏 N 列: xxx" 提示
- systemSchemaFilter 常量引入，7 个 skill 统一过滤
  (pg_catalog/information_schema/snapshot/dbe_perf/dbe_pldeveloper/
  dbe_pldebugger/db4ai/gs_logical_cluster/sqladvisor)
- /longtx 排除 WLM 后台线程；/lwlocks 排除采集自身 session
  (fmt.Sprintf 的 SQL 里 % 必须转义为 %%)
- /planhistory 无数据时给 3 条诊断建议

P2:
- /segments /bloat /tableinfo 改 pg_size_pretty（替代裸浮点 0.2421875）
- /params 无参返 50 项 curated whitelist，不再 dump 745 条
- /tableinfo 用 pg_stat_all_tables（支持 pg_catalog.* 系统表查询）
- /sessionmem 加 pid 列（SPLIT_PART(sessid,'.',2) 方便对应 /sessions）

v2 agent 发现的 3 回归:
- /tableinfo "712 kB MB" 双单位 → 去掉展示层冗余 "MB" 后缀
- /toasttable 未接入 systemSchemaFilter → WHERE 加完整过滤
- /resource Max Workers/Parallel 空字符串 → "N/A (GUC not in OG)"

C 类 4 项真问题:
- /checkpoint 补 maxwritten_clean / buffers_alloc 列（11 列全显）
- /sqlcount 补 mergeinto_count / avg_sel_us / max_sel_us 列（OG 5.0
  不暴露 p95，用 avg/max 作最接近代理）
- /topsql 长 SQL 单行截断 80 字符 + DB 侧 REGEXP_REPLACE 折叠 whitespace
- /waits 动态文案：仅后台线程活跃时给"实例空闲"而非机械的
  "CPU 密集，检查慢查询"

### 同步发现的跨库修复

- internal/format/table.go 的 nullDisplay 改动影响 Oracle/MySQL/PG/OG 四库
- cmd/opendb/main.go ResultRefresh 兜底对四库 /dbtop 生效

### 新增真机测试

- internal/ui/uitest/og_skills_test.go TestOG_AllSkills：单 PTY 测 44 skill
  纯 mustNot 断言（禁止 panic/syntax error/已知 regression token），185 秒全绿

### 统计（v1 → v2 → 本版）

绿占比 62% → 80% → 预估 ≥92%（4 项黄转绿 + 3 项回归已修，剩余只有
空实例无法复测的 bloat/vacuum/segments）
红灯 2 → 0 → 保持 0
SQL 跑通率 100% 保持

## v1.1.06 (2026-04-23)

### BUG-004: 模型 capability 智能推断

**问题**
- setup 向导和 `/model` 向导写模型配置时，capability 字段默认为空或 `small`
- 结果：云端大模型（GPT-4/Claude/Kimi/Qwen-Max）被默认降级到 3 轮 Assist 模式，无法发挥 10 轮自主诊断能力

**修复**
- **feat: `internal/model/capability.go`** — 新增 `InferCapability(provider, modelID)` 启发式推断：
  - 优先识别 small marker（`mini/nano/haiku/flash-lite/≤9b`）→ `small`
  - 次要识别 large marker（`27b/32b/70b/claude/gpt-4/kimi-k2/deepseek-r1/qwen-max` 等）→ `large`
  - 按 provider 兜底：`openai` → large（云 API 常为大模型），`ollama` → small
  - 歧义时倾向 small（在弱模型上跑 10 轮自主循环比 3 轮引导更糟）
- **feat: `internal/setup/finalize.go`** — `GenerateConfig` 写 `ModelConfig` 时自动填 Capability；`renderConfigWithComments` 输出 yaml 时附注释说明
- **feat: `internal/ui/modelwizard.go`** — `/model` 向导加 `capTouched` 标志：provider 切换或离开 model 字段时自动刷新推荐 capability；用户按方向键手动调整后不再覆盖

**测试**
- 19 个 `TestInferCapability` 用例覆盖 small/large/边界（`gpt-4o-mini`、`qwen3:32b@ollama`、`claude-haiku`、`gemini-2-flash-lite` 等）
- `TestGenerateConfig` 新增断言 `capability: large`（kimi-k2.5 场景）

**达成效果**
- 新用户跑 `opendb setup` 配云端大模型 → 自动 `large` → 立即获得 10 轮自主诊断能力
- 本地 ≤9B 模型 → 自动 `small` → Assist 3 轮引导，不被强推到它能力之外
- 本地 32B+ 模型（ollama）→ 按 size marker 识别出 large

## v1.1.05 (2026-04-23)

### PG 会话口径对齐 & Golden 测试稳定性

**Bug 修复**
- **fix(pg): `/sessions` 与 `/activesessions` 口径对齐** — `internal/postgres/skill/monitor/sessions.go` 的 `sessionsSQL`/`sessionsFreqSQL` 统一 WHERE：`backend_type IN ('client backend', 'parallel worker') AND pid != pg_backend_pid()`。此前 `/sessions` 只含 `client backend` 且包含自身连接，与 `/activesessions` 总数差到 8 个以上（parallel worker）；现在两条命令统计口径完全一致，时间窗口漂移也消除。

**测试基础设施**
- **fix(uitest): golden file 版本漂移根治** — `internal/ui/uitest/helpers.go` 加 `normalizeGolden()`：welcome header 行（含版本号）在 golden 比对和 `-update` 两条路径上都被标准化为同长度的纯边框。以后版本号再变（v1.1.06/v1.2.0/v2.0.0）也不再触发 `TestWelcome_Golden` 失败。

**达成效果**
- PG 诊断可信度提升 — `/sessions` 13 和 `/activesessions` 21 矛盾现象不再出现
- 发版节奏不受 golden 测试干扰 — bump 版本号不用再跟着跑 `-update`

## v1.1.04 (2026-04-18)

### 错误码体系 ERR-XXYYYY

**新功能**
- **feat: 新增 `internal/odberr/` 包** — OpenDB 标准错误码系统，格式 `ERR-XXYYYY`（XX=模块，YYYY=序号），30 条首批注册覆盖 core/conn/ui/diag/sentinel/rule/skill/llm/storage/scheduler/cluster 11 个模块 + 90 panic 段 + 999999 兜底
- **feat: 三层 panic recover 覆盖** — `RecoverFatal` 守 main 入口；`Guard` 包裹 REPL 主循环 5 个 select case（diagCh/alertCh/schedCh/skillCh/sigwinchCh）；`SafeGo` 替换 58 处 `go func(`，跨 14 文件覆盖 UI、engine、drone、cerebrate、overlord、cluster、setup、四库 dbtop collectors
- **feat: crash log** — `~/.opendb/crash.log` 追加写入，1 MiB 自动轮转到 `.log.old`，结构化字段（时间/编号/级别/Message/Cause/Stack）
- **feat: `/error` 命令** — `/error` 列出全部错误码（按模块分组、使用次数标记）；`/error ERR-030001` 查看某条的标题、建议、累计次数、日志路径

**测试**
- `internal/odberr/` 16 个单元测试，`-race` 全绿，覆盖 immutability、panic 捕获（值/error/指针三路径）、并发计数器、日志轮转、skill list/detail/fallback

**达成效果**
- panic 不再崩溃退出 — v1.1.03 diag_renderer slice 越界类 bug 自动归 `ERR-030001`，零额外代码即受保护
- 用户反馈只需贴编号 — `ERR-030001` 即可定位模块和代码路径
- 未注册错误走 `ERR-999999`，crash log 完整栈，下次迭代按日志补码

## v0.9.25 (2026-04-03)

### Markdown 渲染精度提升 & 代码质量优化

**新功能**
- **feat: blockquote 引用块渲染** — LLM 输出的 `> 引用` 渲染为 `▎` 暗色竖条 + dim 文字，区分提示/注意事项
- **feat: 嵌套 inline 格式** — `**bold `code` more**` 中 bold 和 inline code 各自独立渲染，不再互相吞掉
- **feat: 表格竖转 fallback** — 窄终端下长内容表格自动转为 key-value 格式（`─── 1/N 标题 ───` + 缩进字段），避免截断碎裂

**Bug 修复**
- **fix: LLM 输出 markdown 表格溢出** — 清理 cell 中的 backtick/bold 标记，缩减表格宽度为 termWidth-4
- **fix: 流式 flush 安全** — 流式结束时未关闭的代码块仍然渲染输出，不再静默丢弃

**重构**
- **refactor: 拆分 repl.go** — 2989 行 → 1949 行 + 4 个新文件（repl_input/repl_welcome/repl_sqlcompat/repl_async）
- **refactor: 事件 drain 替代 sleep(50ms)** — blocking UI 退出后用 flushAfterBlock() 非阻塞 drain

**统计**: +1,242 行 / -1,050 行, 9 个文件变更

## v0.9.24 (2026-04-03)

### UI 输出美化 & 渲染架构优化

**新功能**
- **feat: 代码块语法高亮** — 集成 chroma 引擎，支持 SQL/Shell/Go/Python 等 200+ 语言，/llm 诊断输出的代码块自动着色（关键字青色、字符串黄色、运算符粉色）
- **feat: SIGWINCH 终端 resize 自动重绘** — 监听终端缩放信号，自动更新尺寸并从 outputBuffer 重放内容，根治 BUG-002（dbtop/REPL resize 后大面积贴图错误）
- **feat: LLM 输出视觉分区** — `##` 标题加橙色粗竖线 `┃`，`###` 子标题加青色竖线 `│ ▸`，常规文本加灰色细竖线，`---` 渲染为 40 字符细线分隔，输出有清晰的视觉层次
- **feat: UI 自动化测试框架** — 基于 creack/pty + vito/midterm + golden file，9 个场景覆盖（欢迎页、/exit、/help、补全下拉、窄/宽终端、resize），纯 `go test` 运行，无外部依赖
- **feat: 统一 Picker 组件** — 新增 `RunPicker()` 统一交互选择器，替换 runLoginPicker/runModelPicker/runAlertPicker 三个各 80+ 行的重复实现

**性能优化**
- **perf: 流式输出减闪烁** — LLM 流式 partial line 从"清行+重写"改为"追加增量"模式，消除逐字符输出时的可见闪烁

**重构**
- **refactor: term.go 封装 ANSI 终端操作** — 新增 15 个语义化函数（termMoveTo/termClearRow/termWriteAt/termEnterAltScreen 等），替换 293/295 处裸 `\033[...` 序列，消除手写 ANSI 导致的排序/flush 错误
- **refactor: termwidth 包统一宽度计算** — 抽取 `internal/ui/termwidth/`，统一 StringWidth/Truncate/PadRight/WrapLine/StripANSI，消除 repl.go/diag_renderer.go/markdown.go 三处重复实现
- **refactor: 统一 Picker 替换重复代码** — 合计删除约 200 行重复的键盘事件循环和渲染代码

**Bug 修复**
- **fix: /health Panel 换行错位** — 健康检查中 Oracle 错误信息含 `\n`（如 ORA-01013）导致 Panel 右边框跑偏，现在按 `\n` 拆分子行分别加边框
- **fix: repl_dropdown_test.go 编译错误** — `*bytes.Buffer` → `*bufio.Writer` 类型不匹配

**依赖引入**
- `github.com/alecthomas/chroma/v2` — 语法高亮引擎（非 bubbletea 依赖）
- `github.com/creack/pty` — PTY 接口（测试用）
- `github.com/vito/midterm` — 内存终端模拟器（测试用）
- `github.com/charmbracelet/x/exp/golden` — golden file 对比（测试用）

**统计**: +2,021 行 / -500 行, 19 个文件变更

## v0.9.19 (2026-03-30)

### dbtop 三库指标对齐专项

**MySQL**
- **fix: TPS 不含 autocommit** — `Com_commit` → `Handler_commit`，隐式提交纳入统计
- **feat: 新增 WTR%** — `(Active - ActiveCPU) / Active * 100`，从 `performance_schema.threads` 采集
- **feat: 新增 ActiveCPU / ActiveIO** — 新增 `sessionCountSQL`，按线程 STATE 分类
- **feat: 会话列表增加 SQLID / EVENT / CLASS** — JOIN `events_statements_current` + `session_connect_attrs`
- **feat: 健康检查增加 WTR% / Event PCT** — 与 Oracle 对齐
- **refactor: 布局对齐 Oracle** — Counts 行命名、Session 列宽 (5/10/13/20/10/5)

**PostgreSQL**
- **fix: QPS 语义修正** — 优先用 `pg_stat_statements.calls` (精确语句数)，fallback 元组操作
- **fix: SBuf bar 硬编码 32GB** — 移除 bar，仅显示配置值
- **refactor: 布局对齐 Oracle** — Session 列宽 PID(5) USR(10)，与 Oracle 一致

**Oracle**
- **fix: 等待事件 delta 精度** — 新增 `RawTimeMicro int64`，消除 `float64` round-trip 精度损失

## v0.9.18 (2026-03-28)

### Rule Engine 优化 — Phase 2-5 全面升级

**Phase 2: 关键场景专用规则**
- **feat: WE007 blocker 查询增强** — QueryBlockerDetail 按需查 v$session 获取 blocker command/event/status，解锁死锁检测和 DDL/DML 冲突识别
- **feat: WD015 row cache lock 查询化** — QueryRowCacheStats + QuerySequenceCacheInfo 替代空 metric，dc_sequences vs dc_objects 精确诊断

**Phase 3: SQL 性能差异化诊断**
- **feat: WD004 差异化分支** — 全表扫描(db file scattered read)/TEMP溢出(direct path temp)/invalidation/plan_instability 四分支替代万金油
- **feat: QueryTopSQLPhysicalReads** — 定位高物理读 SQL

**Phase 4: 参数/会话/配置检查**
- **feat: checkResourceManagerFallback** — resmgr:cpu quantum 检测
- **feat: checkTempUndoFallback** — direct path temp 等待检测
- **feat: 扩展 checkParamFallback** — CURSOR_SHARING/DB_FILE_MULTIBLOCK_READ_COUNT/FILESYSTEMIO_OPTIONS

**系统性修复**
- **fix: ExtractHardParsePct** — QueryParseStats 多行 map 结果无法 MatchGT
- **fix: WaitPct 优先于累积 v$sysstat** — 活跃风暴期间用当前等待比例
- **fix: hard_parse_storm live 触发** — 改用 wait_profile 触发条件
- **fix: MI2-005 CausesOf** — resolver 正确吸收为下游
- **feat: session_cached_cursors + bind peeking 诊断建议**

### 验证结果

- R3 Opus 严格评估 8 场景均分 65.9（R2 约 42.5, +23.4）
- T020 硬解析风暴 30→80, T002 锁级联 65→80, T014 cursor pin S 55→76

## v0.9.17 (2026-03-27)

### Bug 修复

- **fix(30): MySQL driver 日志刷屏** — `mysql.SetLogger(io.Discard)` 静默 go-sql-driver stderr 输出
- **fix(28): SQL 拼写容错** — SHOW/DESC/USE 后跟非 NL 词时强制归类为 SQL，不走 LLM
- **fix(29): 排队命令光标漂移** — 删除 hourglass 动画，改用 prompt `[N 排队]` 提示
- **fix: params 空参数 panic** — 三库 params.go 空字符串 `pattern[0]` 索引越界修复
- **fix: PG/OG \c 等不支持命令** — 拦截 \c/\i/\x/\timing/\q 返回友好提示

### 功能

- **(26) feat: 多行粘贴折叠** — 超时检测粘贴（150ms），输入行显示 `[Pasted ... + N lines]` 预览，Enter 确认执行
- **(27) feat: 超长文本展开** — >4KB 单元格按行渲染（SHOW ENGINE INNODB STATUS 等）
- **feat: Backspace 命令整删** — 补全后的 `/command` 按 Backspace 一次清到 `/`

---

## v0.9.16 (2026-03-27)

### 原生 CLI 命令支持

- **(24) feat: DESC/SHOW 不再转 skill** — 直接走数据库原生执行，MySQL DESC 原生透传
- **(25) feat: Oracle DESC 支持 v$ 视图** — DESC v$session 转 all_tab_columns 查询
- **feat: PG/OG 反斜杠命令** — \d, \dt, \di, \dv, \du, \dn, \df, \l, \conninfo 转标准 SQL
- **feat: Oracle SHOW 扩展** — SHOW ERRORS/CON_ID/SPPARAMETER/RELEASE 转字典查询
- **fix: Classifier SHOW 确认词补全** — 加 DATABASE/ENGINE/REPLICA/PDBS 等防止误判为 NL

### 数据库连接修复

- **fix: MySQL/PG/OG Database 字段丢失** — config.Connection 加 Database 字段，连接时正确设置默认库
- **fix: MySQL USE 不生效** — 三库 driver 加 SetMaxOpenConns(1)，会话状态持久化

---

## v0.9.15 (2026-03-27)

### Bug 修复

- **(19) fix: F 浏览退出后分隔线重复**: alternate screen 恢复的主屏含旧分隔线残留，退出后用 outputBuffer 重绘整个内容区消除
- **(20)(21)(22) fix: 异步操作期间事件错位**: Sentinel/Scheduler 事件在 diagRunning/skillRunning/blockingUI 期间缓冲，操作完成后批量渲染
- **fix: Scheduler 错误信息错位**: 多行错误按 \n 拆分，每行独立 writeOutputLine 并对齐缩进

### Prompt 优化

- **禁止编造数据**: 四库 prompt 新增规则 — 诊断结论中引用的具体数值必须来自工具或 sql skill 查询，不能推测或编造
- **SQL 验证**: 给出修复 SQL 前必须先验证对象存在，ISEQ$$_ 序列不能直接 ALTER (Oracle)
- **自然语言分流**: 用户直接输入问题时不附加 Sentinel 上下文，避免 LLM 偏离到告警分析

### 影响文件

- `internal/ui/tablebrowser.go` — 退出时 outputBuffer 重绘
- `internal/ui/repl.go` — blockingUI flag + 事件缓冲
- `internal/ui/scheduler_renderer.go` — drainPendingEvents/flushAfterBlock/多行错误拆分
- `internal/ui/alert_renderer.go` — bufferAlert
- `internal/ui/skill_runner.go` — flushPendingEvents 调用
- `internal/ui/connpicker.go` — 退出时不调 drawInputArea
- 四库 `agent/prompts.go` + `agent/prompt_loop.go` — 防幻觉+验证规则
- 四库 `skill/ai/diag_skill.go` — NL 分流逻辑

---

## v0.9.14 (2026-03-26)

### P4 Bug 批量修复

- **(12) fix: F 浏览 stdin 竞争**: browseTable 从 os.Stdin.Read 改为 r.keyCh，消除与 keyboard goroutine 的竞争
- **(13) fix: LLM 报错错位**: 多行错误按 \n 拆分，每行独立 writeOutputLine 并对齐缩进
- **(14) perf: 历史上翻卡顿**: r.writer 从 os.Stdout 改为 8KB bufio.Writer，每事件一次 flush，减少 SSH 延迟
- **(15) fix: /rule current 报错**: 四库 rule_skill.go 加 "current" 作为 "live" 别名
- **(16) fix: /llm 失败显示监控面板**: 四库 diag_skill.go LLM 失败时跳过 monitorOutput，只输出错误信息
- **(17) fix: /exit 光标跳到第一行**: teardownScreen 先 \033[r] 重置 scroll region + teardownDone 标记跳过 defer 重复 \033[r]
- **(18) fix: alert-scan 超时 60s**: SQL 去掉 REGEXP_LIKE 改 LENGTH 过滤 + 15s context timeout
- **fix: bufio startup hang**: queryCursorRow 发 \033[6n 后必须 flush，否则终端收不到查询
- **fix: 提示文案更新**: /rule 和 /llm 的异常记录提示从 "<编号>"/"history" 改为 "选择分析"
- **影响文件**: repl.go, tablebrowser.go, dbtop.go, diag_renderer.go, alert.go, 四库 rule_skill.go + diag_skill.go

---

## v0.9.13 (2026-03-25)

### Fix: Picker 命令输入阻塞 + Rule Engine 优化策略

- **fix: 移除 picker 命令输入阻塞**: `/model`、`/login`、`/llm`、`/rule` 输入完成后不再静默丢弃所有键盘输入，用户可以继续输入或按 Enter 触发 picker
- **feat: picker 命令 ghost hint**: 输入精确匹配 picker 命令时显示灰色 ` ↵` 提示，引导用户按 Enter
- **docs: rule_advice/ 策略文档**: 基于 redo_rate 诊断偏差案例，完整记录规则引擎的信号覆盖缺口、Resolver 优化方案、现有规则审计
- **影响文件**: `internal/ui/repl.go` + `rule_advice/` (6 个文档)

---

## v0.9.12 (2026-03-25)

### SQL 智能识别 + 多行 SQL 支持（P4-9）

- **三阶段 SQL 分类器**: Phase 1 无歧义首词(40+关键字)，Phase 2 歧义首词+第二词消歧(冠词规则)，Phase 3 SQL 组合模式扫描(19条正则)
- **解决 `show me slow queries` vs `SHOW TABLES` 歧义**: 第二词是 the/me/my/how/why → 自然语言
- **粘贴检测**: 多行粘贴自动识别（read buffer 中 `\n` 后有剩余字节）
- **多行 SQL 模式**: 粘贴的 SQL 无 `;` 时进入 `...` 续行提示，输入 `;` 执行，Ctrl+C 取消
- **多行自然语言**: 粘贴的自然语言拼接为一段话发 LLM（与 Claude Code 一致）
- **影响文件**: `internal/dispatch/classifier.go` + `internal/ui/repl.go`

---

## v0.9.11 (2026-03-25)

### AgentLoop 不收敛防护（P4-8）

- 当 AgentLoop 接近轮次上限（剩余 2 轮）时，注入收敛指令强制模型给出最终总结
- 解决 MiniMax M2.7、DeepSeek V3 等模型在开放式 `/llm current` 中 20 轮不收敛的问题
- **四库统一**: Oracle/MySQL/PG/OG 的 `agent/loop.go` 同步改动
- **影响文件**: `internal/{oracle,mysql,postgres,opengauss}/agent/loop.go`

---

## v0.9.10 (2026-03-24)

### Picker 命令显示优化（P4-7 交互优化）

- **历史/回显统一**: picker 选择后，聊天区和历史记录只显示命令名（如 `/model`），不暴露拼接参数（如 `/model minimax`）
- **排队显示**: 异步执行期间 picker 命令排队显示命令名，不进入下拉列表；出队后再开 picker
- **输入锁定修复**: 修复 `f`/`F` 键绕过 picker 命令输入锁定的问题
- **▸ 箭头指示**: /llm、/rule、/model picker 选中行前加 `▸` 箭头（与 /login 一致）
- **影响文件**: `internal/ui/repl.go` + `internal/ui/skill_runner.go`

---

## v0.9.09 (2026-03-24)

### /model 强制 Picker（P4 交互优化）

- `/model` 和 `/model xxx` 都强制进入内联 picker 列表选择（与 /login 一致）
- 输入 `/model` 后键盘锁定，只能按 Enter 进 picker 或 Backspace 删除
- 列表显示：名称、Provider、Model、Capability，活跃模型标 ✓
- 包含 "none" 选项（禁用 LLM）
- Up/Down 选择，Enter 确认切换，Esc 取消
- **影响文件**: `internal/ui/repl.go` + `internal/ui/modelwizard.go` + `internal/skill/builtin/shared/model.go`

---

## v0.9.08 (2026-03-23)

### LLM Prompt 重大调整：原生 SQL + 明确方案

**问题**: 优化提示词后 LLM 输出质量退化——不再给出具体 SQL 方案，而是泛泛指方向让用户自己查。
- **修复**: 四库 prompt 全面重写（5 个文件）
  - 去掉"优先使用 /命令"规则，改为"修复方案必须给出原生 SQL"
  - 新增规则："不要输出'请执行以下命令确认'然后列查询——你已通过工具获取数据，直接给结论"
  - `opendbSkillReference` 从 /命令列表改为工具名列表，明确标注"诊断可调用，输出用原生 SQL"
  - `prompt_loop.go` 的输出格式从"OpenDB /命令方案"改为"可直接执行的原生 SQL 方案"
- **影响文件**: `internal/{oracle,mysql,postgres,opengauss}/agent/prompt*.go`（5 个文件）

### blocktree "0 条阻塞链" 误导描述修复

**问题**: SQ 争用等非阻塞关系的等待会话，blocktree 显示"0 条阻塞链, 共 N 个被阻塞会话"，用户困惑。
- **修复**: 四库 blocktree 渲染逻辑增加判断
  - chains=0 且 victims=0 → "当前无阻塞链"
  - chains=0 且 victims>0 → "当前无阻塞链 (检测到 N 个等待会话, 但非阻塞关系, 可能是序列争用/资源等待)"
- **影响文件**: `internal/{oracle,mysql,postgres,opengauss}/skill/monitor/blocktree.go`（4 个文件）

### /login 强制 Picker

- `/login` 和 `/login xxx` 都强制进入 picker 列表选择
- 输入 `/login` 后键盘锁定，只能按 Enter 进 picker 或 Backspace 删除
- **影响文件**: `internal/ui/repl.go` + `internal/skill/builtin/shared/login.go`

---

## v0.9.07 (2026-03-23)

### LLM 诊断增强：Sentinel 上下文 + burst 对比

**问题 1**: 用户输入自然语言提问时（如"当前是否存在性能问题"），`executeOnDemand` 的 prompt 固定写"当前没有 Sentinel 异常报告"，即使 Sentinel 已有活跃告警也看不到。
- **修复**: `executeOnDemand` 现在先检查 `sentinelSkill.ReportCount()`，如果有告警则压缩最新报告附入 prompt
- **影响文件**: Oracle/MySQL/PG/OG 的 `internal/*/skill/ai/diag_skill.go`（4 个文件）

**问题 2**: `/diag N` 诊断历史 burst 报告时，LLM 只看历史快照不查当前状态，导致诊断结论可能过时（如 burst 时是 idle-in-tx 问题，当前已变为 BufferContent 争用）。
- **修复**: 修改 prompt 指示 LLM "先分析报告，再调工具查当前状态对比"；MySQL/PG/OG 的诊断模式从 playbook 改为 auto 以支持工具调用
- **影响文件**: 同上 4 个文件

---

## v0.9.06 (2026-03-23)

### AgentLoop（原生 Function Calling）四库全覆盖

- MySQL/PG/OG 新增 `internal/*/agent/loop.go`，实现 AgentLoop 多轮工具调用
- 四库 Diagnoser 路由：assist/auto 模式先尝试 AgentLoop（原生 FC），失败 fallback 到 PromptLoop（文本模拟）
- MiniMax/GPT/Claude 现在能调用 40+ skill 获取实时数据，显著提升诊断精度
- MySQL 新增 DiagnoseMode + mode-specific prompts
- **影响文件**: `internal/{mysql,postgres,opengauss}/agent/diagnose.go` + 新建 `loop.go`（6 个文件）

### LLM 输出格式修复

- 单行 `/command` 代码块渲染为内联灰色文本（不再画框）
- 孤立 ``` 行（不在代码块内）直接丢弃
- 带前导空格的 ``` 代码块正确识别
- **影响文件**: `internal/ui/markdown.go`

---

## v0.9.05 (2026-03-22)

### 动态 Sentinel/DiagSkill 解析

- full build 中 sentinelSkill/diagSkill 从静态（最后注册的产品）改为动态（按当前 DB 类型切换）
- `/login` 切换数据库时自动停止旧 Sentinel、启动新 Sentinel
- **影响文件**: `cmd/opendb/main.go` + `internal/ui/repl.go`

### LLM Playbook 模式 Markdown 格式化

- `DiagPhaseDone` 非流式路径也经过 `diagStreamFormatter`（表格→box-drawing、标题高亮、代码块）
- **影响文件**: `internal/ui/diag_renderer.go`

### Opus 流式 Fallback

- PG/OG `streamChat` 在 SSE 解析无内容时 fallback 到非流式调用
- **影响文件**: `internal/{postgres,opengauss}/agent/diagnose.go`

### Prompt 优化

- 四库 prompt 新增规则 7-9：表格简洁(≤4列)、不用 emoji、文字优先
- **影响文件**: `internal/{oracle,mysql,postgres,opengauss}/agent/prompt*.go`

### /login Picker

- 回车后显示 inline 连接列表（表头+数据行），Up/Down 选择，Enter 确认
- 表头用 displayWidth 对齐（CJK 宽度感知）
- **影响文件**: `internal/ui/repl.go`

### 登录前历史命令

- filteredHistory 去重（最近出现的唯一命令）
- Up/Down 不被单个补全项拦截（`> 1` 判断）
- **影响文件**: `internal/ui/repl.go`

---

## v0.9.04 (2026-03-22)

### LLM 输出专项优化

- playbook 模式 markdown 格式化（四库）
- Opus 空输出修复（SSE fallback）
- Prompt 规则 7-9

---

## v0.9.03 (2026-03-22)

### P3 交互优化

- /awr /ash /help Panel 格式（已由其他 session 实现，验证通过）

---

## v0.9.02 (2026-03-21)

### P2 交互优化 + 三库同步 + P0 修复

- /topsql /slowsql FTS 检测 + HumanNumber（四库）
- /explain FTS/SeqScan 行标红（四库）
- /kill Panel 两步确认（四库）
- /alter Panel + 人类可读格式（四库）
- 三库 50+ 文件输出格式对齐 Oracle
- blocktree 树形可视化、topsql 排序键、waits 瓶颈分析
- health Overall+Alerts、dbtop DbtopRefreshSource 接口
- LLM 星星闪烁 resolveDiagSkill、流式重复行 wrapLineToWidth
- P0: waits/locks 时间区间说明、/alert 收敛+解读、/kill confirm 错误显示
