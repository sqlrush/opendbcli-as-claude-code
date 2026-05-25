# OpenGauss/GaussDB 系统化优化路线图

> 记录日期: 2026-05-24
> 当前基线: dbaa v1.2.23 / Sentinel burst replay golden
> 范围: 仅 OpenGauss/GaussDB。跨数据库推广暂不做。

## 背景

最近几轮验收暴露出一个模式: 每次现场或本机测试发现一个问题, 再针对单点修复。v1.2.18 已经把 OpenGauss/GaussDB 的核心诊断入口推进到 router + golden + trace + WDR metadata 的阶段, 但还需要把剩余工作系统化, 避免继续靠人工撞问题。

## 已完成基线

- 自然语言 intent router 初版: SQL_ID 调优、WDR 列表、WDR 分析、当前诊断、阻塞、慢 SQL、会话、参数、对象统计。
- Golden/blackbox 初版: 路由、WDR footer、async done 去重、SQL/DDL 代码框不截断。
- `/trace last` 初版: input、intent、mode、route_kind、skill、params、tool_call_count、round_count、LLM 使用情况、status/error。
- WDR 链路增强: WDR 列表直达、WDR analyze 直达、函数签名兼容、结构化证据、诊断边界、证据置信度、报告元信息、保存路径一致性。
- SQLTune 一轮质量门禁: 执行计划标注、成本热点、对象证据、rejected candidates、避免未验证收益误导。
- Sentinel 事故证据链: 告警指标、baseline/current、burst 证据、当前快照、主因/次因、应急/根因/验证 SQL/回滚方案。
- Sentinel IO 根因分类: temp spill、IO wait、低缓存命中高活跃独立归因为 IO瓶颈, 不再混入慢 SQL。
- TUI 流式输出稳定性: async done 单来源, 避免尾部重复刷屏。

## 当前保留目标

用户明确要求: 除跨数据库推广外, 其余工作都继续深入做; 当前专注 OpenGauss/GaussDB。执行顺序从第 2 项开始。


## 2026-05-24 Backlog 快照

本节记录当前人工验收后的完整 backlog, 作为后续回归矩阵与发版 gate 的依据。除第 1 项跨数据库推广外, 当前阶段只聚焦 OpenGauss/GaussDB。

1. Intent router 仍是 OpenGauss/GaussDB 优先。MySQL / PostgreSQL / Oracle 还没有完全按同一套系统化路由策略重构, 暂不纳入当前迭代。
2. Golden 测试还不是完整真实场景矩阵。当前以代码级/黑盒 dispatcher 测试为主, 还没有形成“模型 × 数据库 × 场景 × 期望输出质量”的自动验收矩阵。
3. Trace 还不是完整审计日志。`/trace last` 目前是最近一次内存 trace, 还缺持久化 trace、原始 prompt 摘要、每轮 LLM token/耗时、每个 tool 原始输入输出摘要、超时/重试原因链。
4. SQLTune 还需要继续深化。候选方案生成仍部分依赖 LLM, 语义等价验证还不够强, 索引/改写/统计/参数建议还可以更结构化, Qwen3-32B 下仍需要更多 deterministic scaffold。
5. Sentinel 自动诊断还没按 WDR 这套完全重构。目标链路是: 告警指标 -> baseline/current -> burst 证据 -> 当前快照对比 -> 主因/次因 -> 应急/根因修复。
6. Perfsnap/WDR 还有报告质量校准空间。WDR 当前链路稳定了, 但阈值、置信度、历史窗口 vs 当前故障的表达还要更 DBA 化。
7. “所有模型输出都像 Opus”还没完全实现。当前策略是强模型自由发挥、小模型靠 router/evidence/template 兜底, 但还没有对 DeepSeek/GLM/Kimi/Qwen 全模型做统一 golden benchmark。

## 2. Golden 测试矩阵

目标: 把最近人工测出来的问题固化成可重复验收矩阵, 之后每次发版必须跑, 防止同类回归。

### 2.1 范围

- 数据库: OpenGauss/GaussDB。
- 入口类型: 自然语言、slash command、TUI 渲染、batch 模式。
- 模型模式: prompt mode、小模型受控证据路径、大模型自由工具路径。先记录期望行为, 自动化可分阶段接入真实模型。

### 2.2 第一批 golden cases

| ID | 输入 | 期望 |
|---|---|---|
| OG-GOLDEN-WDR-LIST-001 | 当前有哪几个wdr报告 | direct `wdr`, 不进 LLM, 输出 snapshot 表 |
| OG-GOLDEN-WDR-ANALYZE-001 | 分析 快照76和77之间生成的wdr报告 | `wdranalyze 76 77`, 不误走列表 |
| OG-GOLDEN-WDR-ANALYZE-002 | 我们分析下65到73的报告存在哪些问题 | `wdranalyze 65 73`, 输出报告元信息 |
| OG-GOLDEN-WDR-FOOTER-001 | /wdranalyze 65 73 | 不出现 `v1_`, 不出现隐藏 `WDR_REPORT_BEGIN`, 报告文件路径写入 footer |
| OG-GOLDEN-CURRENTDB-001 | 当前数据库存在什么问题 | 先采集 health/activesessions/waits/topsql/slowsql/blocktree, 再输出接近 Opus 模板质量 |
| OG-GOLDEN-SQLTUNE-001 | sql id 581990336 如何优化 | 路由 `/sqlfetch -> /sqltune`, 不跑泛化 health/topsql 诊断 |
| OG-GOLDEN-SQLTUNE-QUALITY-001 | /sqltune 581990336 | 不展示语法错误候选为正式方案; verified=0 不展示夸张收益; SQL/DDL 不省略 |
| OG-GOLDEN-TUI-001 | 长 LLM/WDR 输出 | 结束后不重复刷尾部内容 |
| OG-GOLDEN-TRACE-001 | /trace last | 能复盘 route_kind、skill、params、tool_call_count、round_count |

### 2.3 工程落地

- 建议新增 `internal/opengauss/golden/` 或 `internal/opengauss/eval/`。
- 先做无需真实 LLM 的 deterministic cases: intent router、dispatcher blackbox、renderer snapshot、trace render。
- 再做需要本机 GaussDB 的 integration cases: WDR list/analyze、SQLTune 581990336。
- 最后做模型矩阵 cases: Qwen3-32B prompt、Qwen3.6、Opus/GPT/DeepSeek/GLM/Kimi。

### 2.4 验收标准

- 所有 deterministic golden tests 纳入 `go test ./internal/opengauss/...`。
- integration golden 可用环境变量显式开启, 默认不阻塞普通 CI。
- 每个 golden case 有: input、expected route、expected tools、forbidden strings、required strings、最大耗时、是否需要 DB/LLM。

### 2.5 历史会话挖掘计划

目标: 不只靠人工记忆补 case, 而是从项目历史中抽取“LLM 特别容易反复出错”的真实输入, 形成可回放的 golden corpus。

输入源按优先级处理:

1. 当前对话中已明确复现的问题: WDR 路由、SQLTune 质量门禁、当前数据库诊断、多模型输出差异、TUI 重复尾部、trace 不可复盘。
2. 仓库文档与历史验证材料: `docs/benchmark/`, `docs/sqltune/`, `docs/wdr/`, `docs/validation/`, `docs/trace-test-scenarios.md`, `docs/engine-design/`。
3. 本机 Claude/Codex 历史中与 `opendb`、`dbaa`、`OpenGauss`、`GaussDB`、`sqltune`、`wdr`、`trace`、`tool_mode`、`prompt mode` 相关的记录。只抽取项目相关输入、失败模式和期望行为, 不把无关会话纳入测试语料。
4. 我补充的 DBA 场景输入: 慢 SQL、阻塞链、WDR 窗口、参数审查、对象统计、Sentinel 告警、历史统计污染、当前在线故障分离。

抽取结果归一为统一 case schema:

```yaml
id: OG-GOLDEN-WDR-ANALYZE-LOOSE-001
source: current_session | docs | claude_history | codex_memory | manual
database: opengauss|gaussdb
model_tier: deterministic|small_model|strong_model|all
input: 我们分析下65到73的报告存在哪些问题
mode: batch|tui|slash|natural_language
requires_db: false
requires_llm: false
expected:
  intent: wdr_analyze
  skill: wdranalyze
  args: "65 73"
  required_tools: [wdranalyze]
  forbidden_tools: [sql]
  required_strings: ["WDR 报告分析", "报告元信息"]
  forbidden_strings: ["WDR_REPORT_BEGIN", "Evidence Builder", "v1_"]
quality:
  max_latency_ms: 5000
  min_score: 90
  must_distinguish_current_vs_historical: true
```

### 2.6 初始真实问题池

第一批 corpus 从已经反复触发的问题开始, 每个问题至少固化一个 deterministic case, 有条件再扩展成 DB/model case。

| 类别 | 真实失败模式 | Golden 断言 |
|---|---|---|
| Prompt mode / no FC | 客户现场 prompt mode 未调 skill, 本机 FC 正常导致误判 | 无 FC 下仍能路由到正确 skill, 不允许直接幻觉回答 |
| SQL_ID 调优 | `sql id 581990336 如何优化` 曾走 health/topsql 泛化诊断 | 必须 `/sqlfetch -> /sqltune`, 不跑当前数据库泛诊断 |
| SQLTune 质量 | verified=0 仍展示夸张收益、语法错误候选、省略号 SQL、synthetic bind 被当真实值 | 无效候选只能进 rejected, 正式方案不得含省略号或未验证倍率 |
| WDR 列表 | `当前有哪几个wdr报告` 被 LLM 自由调用 SQL 或错误函数签名 | 直达 `wdr`, 秒级输出 snapshot 表 |
| WDR 分析 | `分析 快照76和77之间...` 或 `65到73...` 被误判成列表 | 提取 begin/end snapshot 并直达 `wdranalyze` |
| 当前诊断 | Qwen3.6 native FC 请求超过上下文, Qwen3 prompt 输出过浅 | 大模型可自由分析但控制上下文; 小模型走受控证据 LLM, 质量接近 Opus |
| 历史 vs 当前 | WDR/top SQL 历史负载被误判为当前在线故障 | 输出必须明确历史窗口/当前快照边界 |
| 强模型质量 | DeepSeek/GLM/Kimi 在受限 prompt 下输出变短变浅 | 强模型保留自由发挥, 但仍过统一质量门禁 |
| TUI 渲染 | LLM 完成后尾部内容重复刷屏 | async done 单来源, 终端不重复尾部块 |
| Trace 可复盘 | “LLM 在干嘛/为什么超时”不可解释 | `/trace last` 或持久 trace 必须含 route、round、tool、token、错误链 |

### 2.7 自动验收分层

- Tier 0: 纯 Go deterministic golden, 默认进入 `go test ./internal/opengauss/...`。覆盖 router、renderer、trace、quality gate。
- Tier 1: 本地 OpenGauss/GaussDB integration golden, 通过环境变量显式开启。覆盖 WDR list/analyze、SQLTune SQL_ID、当前诊断 evidence 采集。
- Tier 2: 模型矩阵 golden, 逐个模型跑固定场景并打分。覆盖 Qwen3-32B prompt、Qwen3.6、DeepSeek V4 Pro、GLM-5.1、Kimi、Opus、GPT-5.5。
- Tier 3: TUI/PTY golden, 验证流式进度、最终输出去重、长表格/长 SQL 渲染。

评分建议: 路由正确 25 分, 工具/证据正确 25 分, 禁止幻觉与无效建议 20 分, DBA 可执行性 20 分, 耗时与稳定性 10 分。生产级场景要求总分 >= 85, P0 场景要求关键断言全通过。

### 2.8 当前落地形态

已开始实现 Tier 0 确定性 golden 体系:

- Case schema: `internal/opengauss/golden/case.go`。
- Evaluator: `internal/opengauss/golden/evaluator.go`。
- 首批语料: `internal/opengauss/golden/testdata/tier0.yaml`。
- 本地命令: `make golden-tier0`。
- CI workflow: `.github/workflows/golden-tier0.yml`。

当前 Tier 0 只覆盖不依赖 DB/LLM 的硬规则: intent、route_mode、skill、args、required/forbidden tools、required/forbidden strings。Tier 1/Tier 2 后续在同一 schema 上扩展, 不另起一套测试体系。

### 2.9 Tier 1 DB Golden

Tier 1 已开始接入真实 OpenGauss/GaussDB 实例, 默认不在普通 CI 中执行。

- 语料: `internal/opengauss/golden/testdata/tier1.yaml`。
- Runner: `internal/opengauss/golden/integration_test.go`。
- 本地命令: `OPENDB_GOLDEN_CONN=gauss_local make golden-db`。
- 二进制: `make golden-db` 会先用当前源码构建 `bin/dbaa-golden`, 再执行 batch 命令, 避免测到旧安装版。
- LLM 场景默认跳过; 设置 `OPENDB_GOLDEN_ENABLE_LLM=1` 后才运行。

首批 Tier 1 真实 DB case 覆盖 WDR 列表、阻塞查询、WDR 65->73 分析, 并预留当前数据库诊断的 LLM gate。

### 2.10 Tier 2 Model Matrix

Tier 2 已开始接入多模型质量矩阵, 用于 release gate / benchmark, 不进入普通 PR CI。

- 语料: `internal/opengauss/golden/testdata/tier2.yaml`。
- Runner: `internal/opengauss/golden/model_matrix_test.go`。
- 本地命令: `OPENDB_GOLDEN_CONN=gauss_local OPENDB_GOLDEN_MODELS=opus,qwen3-32b-prompt make golden-models`。
- 模型切换方式: runner 为每个模型生成临时 `OPENDB_CONFIG`, 修改临时文件的 `active_model`, 不修改用户真实配置。
- 评分报告: 默认写到 `/private/tmp/opendb-golden-reports/model-matrix.md`, 可用 `OPENDB_GOLDEN_REPORT=/path/report.md` 覆盖。
- 场景筛选: 可用 `OPENDB_GOLDEN_CASES=OG-GOLDEN-MODEL-CURRENTDB-001` 只跑指定 case。

当前评分为机器硬规则 + DBA rubric: 命令状态、超时、required/forbidden strings、case 最大耗时、证据完整性、历史/当前边界、因果链、动作建议、SQLTune 质量门禁。LLM 不参与自评; 后续再补人工抽检字段。

### 2.11 Tier 3 TUI / PTY Golden

Tier 3 已开始接入 TUI/PTY 渲染回归, 分成 CI-safe 与 live 两层。

- CI-safe renderer contracts: `internal/ui/repl_tier3_golden_test.go`。
- Live PTY contracts: `internal/ui/uitest/tier3_golden_test.go`。
- PTY helper transcript: `internal/ui/uitest/helpers.go` 的 `RawOutput()`。
- 本地稳定命令: `make golden-tui`; 普通 CI 通过 `make golden` 跑 Tier 0 + Tier 3 CI-safe。
- 本机真实交互命令: `OPENDB_GOLDEN_CONN=gauss_local make golden-tui-live`。

当前覆盖:

- 异步诊断进度轮次只提交一次: 第1轮工具采集、第2轮证据报告。
- 长 LLM 输出结束后不重复刷尾部内容。
- Markdown 表格、SQL/plan code block、`[P1]`/`[P2]` 执行计划标注渲染不溢出。
- live PTY 真实 `/login` -> 自然语言诊断 -> 最终屏幕检查, 防止只测 batch 输出。

Tier 3 live 依赖本机 DB 与 PTY, 不进入普通 CI; 发版前必须在验收机跑。

## 3. Trace / Debug Last 深化

目标: 每次“LLM 在干嘛”“为什么超时”“为什么没调工具”都能复盘, 不再靠猜。

当前已落地:

- `diagtrace.SetLast` 同时写内存与磁盘。
- 最近一次持久化到 `diagtrace/last.json`, 历史追加到 `diagtrace/history.jsonl`。
- `/trace last` 在内存为空时自动从磁盘恢复最近一次诊断 trace。
- `/trace history [N]` 列出最近 N 条持久化诊断记录。
- `/trace last --json` 与 `/trace history [N] --json` 输出结构化 JSON, 便于 golden/外部脚本断言。
- trace 字段包含 route decision、intent、mode、skill、params、reason、confidence、model、prompt 摘要、LLM token、rounds、tool call 状态与耗时、错误摘要。

待做:

- 持久化更完整的每轮 LLM: tool_mode、每轮 input/output token、重试原因链。
- 记录每个 tool 输出摘要 hash/长度, 方便判断输出是否被截断或污染。

## 4. SQLTune 深化

目标: Qwen3-32B 这类小模型下也能给出可信 SQL 优化建议; 大模型只会更好, 不被硬兜底拖累。

### 4.1 SQLTune 质量门禁增量

已把人工验收中反复出现的坏候选固化为确定性拒绝规则和单测:

- 拒绝 `...` / `…` 省略号候选, 正式优化方案不得展示截断 SQL/DDL。
- 拒绝 `原 SQL 不变`、`原SQL不变`、`original sql unchanged` 等未给出完整 SQL 的候选。
- 拒绝 `<表名>`、`<PID>`、`<begin_snap_id>` 这类模板占位符, 但不误伤正常 `<` / `>` 比较谓词。
- 拒绝以 `SELECT`、`WHERE`、`JOIN`、`,`、`(` 等结尾的明显 SQL 残片。
- 被拒候选只进入“模型候选被拒绝（调试信息）”, 并带原因; 正式方案只展示可执行、可信、可解释内容。

### 4.2 SQLTune 真实 Golden 与确定性候选增量

已完成:

- Tier 1 DB golden 增加 `/sqltune 581990336` 真实 SQL_ID 用例, 并可用 `OPENDB_GOLDEN_CASES` 单独执行。
- Tier 2 model golden 增加 SQLTune 模板占位符 forbidden 断言。
- deterministic scaffold 增加 `rewrite` 维度: 识别重复 `IN ('x','x')` 字面量并生成等价去重候选。
- `verifyOne` 增加 preflight gate, 候选明显不可执行时不再访问数据库 EXPLAIN。
- Round 1 prompt 明确: 不能写完整 SQL/DDL 时不要输出 candidate, 确定性 index/stats/rewrite 基线会由引擎自动合并。

待做:

- 继续扩展候选分层: parameter / schema 两类还需要更严格的生成与验证策略。
- 语义安全深化: 重写建议除抽样等价外, 还要补更强的结构等价和结果列约束。
- 小模型路径继续收敛: 让 Qwen3-32B 更多解释已验证证据, 少输出自由候选。
- 大模型路径保留自由分析空间, 但继续过同一门禁。

## 5. Sentinel 自动诊断深化

目标: 告警触发后的自动诊断形成事故级结构, 不是让模型自由读监控输出。

已完成:

- `sentinel_evidence_builder` 已输出完整事故链: 告警指标、Baseline vs Current、Burst 时刻证据、当前快照对比、Top SQL、等待事件、阻塞链、主因/次因。
- 输出固定包含紧急措施、根因修复、验证 SQL、回滚方案。
- 当前快照对比覆盖 activesessions、waits、blocktree、topsql/slowsql 以及 health 类关键指标（cache hit、connection、dead tuple、XID、WAL、checkpoint、temp）。
- Tier 0 Sentinel golden 已覆盖 slow SQL、lock、IO、WAL、connection、XID, Sentinel 包内单测覆盖 Vacuum/XID/WAL/IO/connection/slow SQL 的原因特异验证 SQL。
- Sentinel burst replay golden 已接入 JSON payload, 覆盖 IO temp spill 与 lock chain 两类可回放异常。

待做:

- 后续可继续从真实客户现场/本机 Sentinel 告警中追加 JSON replay 语料。

## 6. WDR / Perfsnap 质量校准

目标: 长窗口报告不只“总结表面现象”, 而是给出可信历史窗口诊断, 并明确当前在线状态边界。

已完成:

- WDR 结构化证据、诊断边界、证据置信度、报告元信息。
- WDR 自然语言路由和 footer 修复。

待做:

- 阈值/置信度校准: Buffer Hit、Soft Parse、Rollback、TopSQL、IO/Lock/WAL 分类。
- WDR 与当前实时快照联合诊断模式: 历史窗口问题 vs 当前在线故障分离。
- Perfsnap 对齐 WDR 输出结构, 包含时间线、负载变化、Top SQL/Wait、动作建议。
- 报告中避免内部术语泄漏; debug 细节放 `/trace last`。

## 7. 模型输出质量矩阵

目标: 强模型保持自由发挥达到 Opus/GPT 质量; Qwen 本地模型通过 skill/evidence 补齐能力。

待做:

- 模型维度: Qwen3-32B prompt、Qwen3.6、DeepSeek V4 Pro、GLM-5.1、Kimi、Opus、GPT-5.5。
- 场景维度: 当前诊断、SQL_ID 调优、WDR 分析、Sentinel 事故、慢 SQL、阻塞。
- 指标: 是否调对工具、是否引用证据、是否误判历史为当前、是否展示无效建议、耗时、输出模板质量。
- 输出 A/B report: 每个模型每个场景给 A/B/C 评级和失败原因。

## 下一步

从第 2 项开始实施: 先建设 OpenGauss/GaussDB golden 测试矩阵骨架, 把现有 route/renderer/trace 用例收拢成统一 case 格式, 再逐步加入 DB integration 和模型矩阵。
