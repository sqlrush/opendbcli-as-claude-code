# Hermes / Charm 启发的优化待办

- 日期：2026-04-17
- 来源：
  - [../hermes-comparison-2026-04-17.md](../hermes-comparison-2026-04-17.md)（T1–T15，架构层）
  - [../charm-tui-analysis-2026-04-17.md](../charm-tui-analysis-2026-04-17.md)（T16–T21，TUI 层）
- 状态：待排期
- 优先级记号：P0=立即 / P1=本季度 / P2=下季度 / P3=机会型

---

## P0 —— 立即启动（ROI 最高，投入 < 1 人周）

### T1. SQLite + FTS5 会话存储

- **替换文件**：`internal/engine/session/filestore.go` → `sqlitestore.go`
- **参考**：hermes `hermes_state.py`（SCHEMA_VERSION、FTS_SQL、`_sanitize_fts5_query` 全可直译）
- **Schema 关键点**：
  - `sessions(id, source, instance, model, parent_session_id, started_at, ended_at, ...)`
  - `messages(session_id, role, content, tool_calls_json, ts)`
  - `messages_fts USING fts5(content, session_id UNINDEXED)`
  - WAL 模式 `PRAGMA journal_mode=WAL`
- **解锁功能**：`/search <关键字>`、`/resume <历史 session>`、跨实例聚合
- **迁移**：保留 `FileSessionStore` 作为 fallback，新增 `WithSQLiteStore(path)` option
- **验收**：1 万条消息下 FTS 查询 < 100ms；并发 2 读 1 写不崩

### T2. 大文件拆分：规则引擎先减肥

- **问题**：`internal/mysql/ruleengine/rules_extended.go` 4247 行、`postgres/ruleengine/rules_extended.go` 3141 行、`oracle/ruleengine/rules_wait_deep.go` 2957 行，违反 `CLAUDE.md` 800 行上限
- **短期（不等 T6）**：按规则 category 拆成多个 < 800 行的子文件（`rules_extended_io.go`、`rules_extended_lock.go`…）
- **长期**：见 T6（外置化）

### T3. 中央 CommandRegistry 合并

- **问题**：`internal/skill/registry.go` 只管 dispatch，help、自动补全、产品菜单各处另写
- **改法**：新增 `internal/skill/command_def.go` 定义 `CommandDef{Name, Aliases, Category, ArgsHint, CLIOnly, …}`，help、补全、产品菜单全部从它派生
- **参考**：hermes `hermes_cli/commands.py:COMMAND_REGISTRY`
- **验收**：新增一条命令或一个 alias 只改一个文件

---

## P1 —— 本季度（核心能力补齐）

### T4. Prompt Cache 不变性护栏

- **改 CLAUDE.md**：把"对话进行中不可改历史上下文 / 不可换 toolset / 不可重建 system prompt"写成硬规则
- **code 层**：`econtext.Builder` 增加 `frozen` 标志，Build 之后再 Set* 直接 panic / 返回 error
- **例外**：仅 `contextManager.Compress()` 路径允许修改
- **参考**：hermes `AGENTS.md:339-347`

### T5. Profile 多实例隔离

- **改法**：
  - 所有 `~/.opendb/` 硬编码改为 `getOpenDBHome()`
  - `main.go` 最早期解析 `--profile <name>` 或 `OPENDB_HOME` 环境变量
  - 每 profile 独立 credential / session / memory / skill 目录
  - gateway / sentinel 等需要唯一凭据的子系统用 `acquireScopedLock()`
- **参考**：hermes `_apply_profile_override()`、`AGENTS.md:368-416`
- **应用场景**：DBA 同时管 dev / pre-prod / prod 三套连接串而不互串

### T6. Markdown Skill 外置化（规则引擎 v2）

- **目标**：把 `ruleengine/rules_*.go`（Oracle/MySQL/PG/openGauss 共 20k+ 行 Go）迁到 `~/.opendb/rules/<product>/*.md` + frontmatter
- **Schema**（参考 `ailinkdb-rule-spec.md`）：
  ```
  ---
  rule_id: IO-001
  product: oracle
  category: io
  level: warning
  triggers: [db file sequential read > 10ms]
  ---
  # 规则描述 Markdown
  ```
- **加载器**：启动时扫描 → 解析 frontmatter → 构造 `Rule` 对象
- **验收**：
  - 单个规则文件 < 200 行
  - 用户不重新编译就能添加 / 修改规则
  - 现有 Go 规则行为不变（用回归测试保证）
- **配套**：沿 `CLAUDE.md` 所述"先 AI 生成规则数据，再由数据生成加载器"的流程

### T7. 凭据池 + 成本计费

- **新增包**：`internal/engine/provider/credpool/`
- **能力**：
  - 多个 API Key 轮询 + 限流隔离
  - 请求结束时按 token 计费写入 `~/.opendb/usage.db`
  - `/cost today` 命令查本日花费
- **参考**：hermes `agent/credential_pool.py`、`usage_pricing.py`

---

## P2 —— 下季度（扩展生态）

### T8. MCP Client

- **新增**：`internal/engine/mcp/client.go`
- **能力**：
  - 读 `~/.opendb/mcp_servers.yaml`，按 stdio / HTTP transport 连接
  - 发现对方 tools，注册进 `skill.Registry`
  - LLM 可透明调用外部 MCP server 的工具（postgres-mcp、slack-mcp 等）
- **参考**：hermes `tools/mcp_tool.py`（2599 行）
- **依赖**：Go 端 MCP SDK，官方暂无成熟实现，可能需要自研或用 `mark3labs/mcp-go`

### T9. MCP Server

- **新增**：`opendb mcp serve` 子命令
- **暴露 tool**：`query_sql`、`get_awr`、`cluster_status`、`kill_session`、`sentinel_alerts_poll` 等核心 skill
- **用法**：Claude Desktop / Cursor 里配 `{ "opendb": { "command": "opendb", "args": ["mcp", "serve"] } }`
- **参考**：hermes `mcp_serve.py`（867 行）、官方 `FastMCP` 模式
- **价值**：用户在主 IDE agent 里直接调 opendb，不需要切终端

### T10. 可插拔 Memory Provider

- **改 interface**：`memory.Store` → `memory.Provider`，文件实现只是 `FileProvider`
- **新增支持**：mem0、pgvector（复用 opendb 已有 PG 连接能力）、Honcho
- **配置**：`~/.opendb/config.yaml` 的 `memory.provider: file | mem0 | pgvector`
- **参考**：hermes `plugins/memory/*`

### T11. Subagent / 并行子任务

- **场景**：集群诊断时并行采集 N 个节点 AWR
- **新增 skill**：`delegate(prompt, context, ...)` → 派生子 Engine，独立上下文窗口，返回压缩后的结论
- **关键**：子 agent 结果压缩方式（只返结论，不返工具调用轨迹）
- **参考**：hermes `tools/delegate_tool.py`

---

## P3 —— 机会型（优先级最低）

### T12. Gateway：IM 消息网关

- **范围**：先 WeChat / 钉钉（DBA 实际常用），Telegram 作为二阶段
- **架构**：独立 `cmd/opendb-gateway`，与 CLI 共享同一个 SessionStore（依赖 T1）
- **场景**：sentinel 告警推 IM，DBA 在 IM 里 `@opendb 分析原因`，自动进诊断流
- **参考**：hermes `gateway/platforms/`

### T13. Skin 引擎

- **改造**：`internal/ui/repl.go` 颜色常量抽到 `~/.opendb/skins/<name>.yaml`
- **内置**：classic（现状）、mono、terminal-dark
- **命令**：`/skin <name>`
- **参考**：hermes `hermes_cli/skin_engine.py`

### T14. Cron + 自然语言任务

- **能力**：`opendb cron add "每天 08:00 总结昨日 top SQL 并发邮件"`
- **依赖**：T12 gateway 实现 delivery
- **参考**：hermes `cron/{jobs,scheduler}.py`

### T15. 轨迹导出 + 批量复放

- **动机**：为未来自训 / 蒸馏准备数据
- **新增**：`opendb trajectory export --session <id>` → 规范化 JSON
- **批量**：`opendb batch run --input tasks.jsonl --out trajectories/`
- **参考**：hermes `trajectory_compressor.py`、`batch_runner.py`

---

## 商业化 / 战略专项（来自 FIT2CLOUD 分析）

### T22. [P1] 一键安装脚本 + Docker 部署

- **问题**：现在用户要装 opendb 必须 Go 编译或下 release 二进制，没有"复制即跑"体验
- **目标**（参考 SQLBot README）：
  ```bash
  # 一行 docker
  docker run -d --name opendb -v ./data:/opt/opendb dataease/opendb

  # 一键脚本
  curl -sSL https://opendb.io/install.sh | bash
  ```
- **副产物**：`docker-compose.yaml` + helm chart（K8s 用户）
- **前置**：参考 hermes `scripts/install.sh:1-1435`、SQLBot `start.sh`、JumpServer `quick_start.sh`
- **避坑**：脚本要做非交互模式检测（`[ -t 0 ]`），见前面 git clone 调研

### T23. [P1] README 重构：差异化定位 + 社区版/企业版矩阵

- **问题**：当前 README 强调"L4 自治"，但没有显式对标谁、不展示商业化路线
- **新增内容**：
  - **顶部 hero 段**：明确写 "面向 DBA 的 AI agent，与 SQLBot/DataEase 等业务侧问数工具的差异化"
  - **三大差异化卡片**：本地推理（Ollama/MLX/vLLM）/ Go 单二进制零依赖 / 企业级凭据加密
  - **社区版 vs 企业版功能矩阵**：哪怕企业版还没做，先画矩阵图展示路线
  - **客户案例 placeholder**：留位置，哪怕是 1 家试用客户也写
- **参考**：飞致云每个产品页的 [社区版 vs 企业版] 对比表

### T24. [P2] 申请接入 1Panel 应用商店

- **目标**：opendb 在 1Panel apps 里一键安装
- **价值**：1Panel 30k+ star 用户基础是天然导流网络，DBA 是其用户中存在的角色
- **流程**：
  1. 写符合 1Panel app spec 的 metadata
  2. 提交 PR 到 [1Panel-dev/appstore](https://github.com/1Panel-dev/appstore)
  3. 等审核
- **前置**：T22（必须先有 docker 镜像）

### T25. [P0] 封闭环境部署最佳实践（白皮书 + 示例）

- **背景**：金融、政企、信创要求数据不出域，外接云端 LLM 走不通。这是 opendb vs SQLBot 的核心差异化机会
- **opendb 已具备能力**：
  - `internal/engine/provider/ollama.go`（本地推理）
  - `internal/engine/provider/mlx.go`（Apple Silicon）
  - `internal/engine/provider/vllm.go`（自托管 vLLM）
  - `internal/engine/provider/openaicompat.go`（任意 OpenAI 兼容端点）
- **交付物**：
  1. **博客文章**：`内网零外联部署 OpenDB：从笔记本到生产集群`
     - 笔记本：Ollama + Qwen-2.5-14B（5 分钟跑通）
     - 生产：vLLM + Qwen-2.5-72B + 4× A100（含 docker-compose 范例）
  2. **示例配置目录** `examples/airgap-deploy/`：
     - `ollama-quickstart/` —— 本地最小可用配置
     - `vllm-production/` —— 含模型授权说明、性能基线
     - `huawei-910b/` —— 信创路线（Ascend 910B + MindIE）
  3. **README 加链接**：从首页直接指向"封闭环境部署"小节
- **配套销售材料**（不在代码仓库）：
  - SQLBot vs OpenDB 部署成本对比（3 天 vs 3 个月，1 万 vs 100 万）
  - 等保三级合规一页纸
- **优先级**：P0——这是写完营销材料就能立刻拉客户的事



### T16. [P0] repl.go 紧急拆分

- **问题**：`internal/ui/repl.go` **1951 行**，严重超 `CLAUDE.md` "800 行上限"
- **短期方案**（不等 T17 bubbletea 迁移）：按职责拆成多个 < 500 行的子文件
  - `repl_loop.go` —— 主 read/eval/print 循环
  - `repl_prompt.go` —— prompt 绘制 + 连接信息栏
  - `repl_dispatch.go` —— 命令路由
  - `repl_signal.go` —— Ctrl+C / Ctrl+Z / SIGWINCH 处理
  - `repl_connection.go` —— 连接状态展示
  - `repl_styles.go` —— lipgloss 颜色/样式常量（现在散在 `repl.go:30-52`）
- **前置**：无
- **验收**：所有子文件 < 500 行；现有测试（`repl_input_test.go`、`repl_dropdown_*_test.go`）全绿

### T17. [P1] bubbletea 正式迁入（CLAUDE.md 声明对齐）

- **问题**：`CLAUDE.md` 技术栈写着 "bubbletea + lipgloss"，但 `internal/ui/` 下 **0 处 import bubbletea**，全部基于 `bufio.Scanner` + 原生 ANSI
- **范围**：把 repl 改造为 `tea.Model`
  - Model：session state、connection info、input buffer、alert buffer、dropdown state
  - Msg：tickMsg（dbtop 刷新）、alertMsg（sentinel）、asyncToolResultMsg、keyMsg、windowSizeMsg
  - Update：纯函数 + 命令（`tea.Cmd`）
  - View：lipgloss 渲染
- **参考**：
  - crush `internal/app/app.go`（顶层 `tea.Model` 装配）
  - crush `internal/tui/components/chat/` 模式
- **收益**：
  - 边界场景（窗口 resize、Ctrl+Z 恢复、多行粘贴）由 bubbletea 统一处理
  - 测试从"伪造终端"改为"给 Program 发 Msg"
  - `dbtop.go` / `alert_renderer.go` / `diag_renderer.go` 刷新节奏统一由 tea runtime 调度，不再各自 print 冲突
- **前置**：T16（先把 repl.go 拆到可审视的粒度）
- **风险**：工程量最大（预估 3–4 周），需保留旧 REPL 作 fallback，通过 `OPENDB_TUI=legacy` 切回

### T18. [P1] UI 组件化（参考 crush 目录结构）

- **目标**：把 `internal/ui/` 平铺布局改为组件化目录
- **目标结构**：
  ```
  internal/ui/
  ├── app/                     # 顶层 tea.Model 聚合
  ├── components/
  │   ├── chat/                # REPL 对话区
  │   ├── prompt/              # 输入框 + 自动补全 + /-命令
  │   ├── dropdown/            # 现 repl_dropdown.go
  │   ├── picker/              # 现 picker.go / connpicker.go
  │   ├── tablebrowser/        # 现 tablebrowser.go
  │   ├── dbtop/               # 现 dbtop.go
  │   ├── alert/               # 现 alert_renderer.go
  │   ├── diag/                # 现 diag_renderer.go
  │   └── markdown/            # T19 迁入 glamour 后落这里
  ├── pages/
  │   ├── chat/                # 主 REPL 页面
  │   ├── wizard_conn/         # 连接向导（由 T20 重写）
  │   └── wizard_model/        # 模型向导（由 T20 重写）
  ├── styles/                  # lipgloss 样式集中
  ├── theme/                   # T13 皮肤引擎
  └── termwidth/               # 已存在，保留
  ```
- **约束**：每个 component 独立 `tea.Model`，< 300 行为目标，违反即拆
- **前置**：T17
- **收益**：复用率大幅提高；新页面（如 T14 cron 列表、T15 trajectory 浏览）开发成本骤降

### T19. [P1] glamour 替代自写 markdown 渲染

- **问题**：`internal/ui/markdown.go` 606 行 + `markdown_test.go` 手写 Markdown → ANSI 转换
- **改法**：引入 `github.com/charmbracelet/glamour`，删除自写代码
- **白赚能力**：代码块语法高亮（基于 chroma）、表格、任务列表、引用、图片 alt 文本
- **定制**：自定义 `ansi.StyleConfig` 匹配 opendb 配色（对接 T13 skin 引擎）
- **前置**：无（可与 T17 并行）
- **风险**：诊断报告中的 SQL 代码块渲染需要验证 `sql` language id 的高亮效果
- **验收**：诊断报告、`/help`、错误提示三种场景下渲染符合预期；Go 代码减少 > 500 行

### T20. [P1] huh 替代自写 wizard

- **问题**：`connwizard.go` 750 + `modelwizard.go` 679 + `wizard.go` 257 = **1686 行**手写向导
- **改法**：引入 `github.com/charmbracelet/huh`
  - 连接向导 → `huh.NewForm` 串联 `huh.NewInput`（host/port/user/password）+ `huh.NewSelect`（数据库类型）
  - 模型向导 → `huh.NewSelect`（provider）+ `huh.NewInput`（API key / base URL）+ `huh.NewConfirm`
- **前置**：无（可与 T17 并行）
- **收益**：预计代码量降至 < 400 行；天然支持键盘导航、验证反馈、终端尺寸自适应
- **验收**：setup 流程端到端可用；现有 `wizard_test.go` 补写表单断言

### T21. [P2] 基于 bubbles 实现 /history、/search、/sessions 页面

- **依赖**：T1（SQLite 会话存储）+ T17（bubbletea 基座）
- **组件**：
  - `bubbles/list` —— 会话列表
  - `bubbles/textinput` —— 搜索输入
  - `bubbles/viewport` —— 预览会话内容
  - `bubbles/table` —— FTS 命中条目（按相关度排序）
- **命令**：
  - `/history` —— 浏览本实例历史会话
  - `/search <keyword>` —— FTS5 全文检索
  - `/resume <session_id>` —— 继续上次会话
- **前置**：T1 + T17
- **收益**：解锁 hermes "searches its own past conversations" 特性，且是原生 Go 实现

---

## 依赖关系

```
T1 (SQLite) ──┬──> T12 (Gateway)
              ├──> T15 (批量复放)
              └──> T21 (/history /search 页面)

T6 (MD skill) ──> T11 (Subagent)  # 子 agent 需要能动态加载 skill 子集

T8 (MCP client) ── 独立
T9 (MCP server) ── 独立（但会依赖 T5 profile 做实例隔离）

T2 (拆文件) ── 独立，但做了 T6 就不必了

# TUI 主线
T16 (repl.go 拆分) ──> T17 (bubbletea 迁移) ──> T18 (UI 组件化) ──> T21

T19 (glamour) ── 可与 T17 并行
T20 (huh) ── 可与 T17 并行
T13 (skin) ── 建议并入 T18 的 theme/ 子目录

# 商业化主线
T25 (封闭环境最佳实践) ──> 立即可做（已有技术能力，只缺文档/示例）
T22 (一键安装) ──> T24 (1Panel 应用商店)
T23 (README 重构) ── 独立，建议与 T25 同步交付
```

## 建议迭代节奏

- **Sprint 1（2w）**：T1 + T3 + **T16** —— 立刻见效，不动核心架构，顺手把 repl.go 拆掉
- **Sprint 2（2w）**：T4 + T5 + **T19 + T20** —— 打基础护栏 + TUI 换库（quick win）
- **Sprint 3-5（6w）**：T6 —— 规则引擎重构（最大工程量）
- **Sprint 3-5（并行）**：**T17** —— bubbletea 迁移（同样 3–4 周）
- **Sprint 6（2w）**：**T18** —— UI 组件化重排
- **Sprint 7 起**：T7–T15 + T21 按业务优先级排期

---

## 追踪方式

每个任务创建独立 plan 文件于 `docs/plans/`，命名：
```
docs/plans/2026-04-XX-T<编号>-<短标题>.md
```
启动时把当前文件的对应条目标记 `状态: → 进行中 (plan: plans/2026-04-XX-...)`。
