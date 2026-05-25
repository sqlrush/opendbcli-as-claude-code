# Hermes-Agent vs OpenDB 架构对比

- 日期：2026-04-17
- 对比对象：[NousResearch/hermes-agent](https://github.com/NousResearch/hermes-agent)（Python，~40k 行工具 + ~24k 行 agent 核心）
- 对比目的：识别 hermes 做得比 opendb 好的架构点，沉淀可借鉴的优化方向
- 配套待办：见 [plans/2026-04-17-hermes-inspired-improvements.md](plans/2026-04-17-hermes-inspired-improvements.md)

---

## 1. 会话存储：SQLite + FTS5 vs 纯 JSONL

| 项 | hermes | opendb |
|---|---|---|
| 存储 | `state.db`（单 SQLite 文件，WAL） | per-session JSONL |
| 索引 | FTS5 虚表 `messages_fts` | 无 |
| 跨会话搜索 | 原生支持 | 需自己 grep |
| 并发 | WAL + 多读一写 | 单文件锁 |
| 会话链 | `parent_session_id`（压缩后续建） | 无 |
| 来源区分 | `source` 字段（cli/telegram/…） | 无 |

**证据：** `/tmp/hermes-agent/hermes_state.py:1-30`、`opendb/internal/engine/session/filestore.go:1-40`。

**opendb 痛点：** 无法"搜索上次 buffer busy 诊断"、无法跨实例聚合、并发访问不安全。

## 2. 记忆子系统：可插拔 Provider vs 单实现

- hermes 在 `plugins/memory/` 下提供 8 个 provider：byterover / hindsight / holographic / honcho / mem0 / openviking / retaindb / supermemory；通过 `agent/memory_provider.py` 抽象接口统一。
- opendb 只有一个基于本地 md 文件的 `internal/engine/memory/store.go`，类型枚举固定 5 种（incident/solution/preference/workload/pattern）。

**影响：** opendb 想接 mem0 / postgres 向量记忆 / 企业内部 KB，必须改核心代码。

## 3. Skills 生态：外部可扩展 vs 编译进二进制

| 维度 | hermes | opendb |
|---|---|---|
| Skill 载体 | `SKILL.md + frontmatter` | Go 源码 |
| 注册 | 文件系统扫描 `~/.hermes/skills/` | `init()` 注册到 `internal/skill/registry.go` |
| 扩展 | 用户丢文件即可 | 必须改源码重新编译 |
| 分发 | agentskills.io 开放标准 + `hermes_cli/skills_hub.py` 社区市场 | 无 |
| 自动沉淀 | 复杂任务完成后自动写 skill | 无 |
| 自我改进 | skill 使用中自我进化（README "closed learning loop"） | 无 |

**连带问题：** opendb 的 `internal/mysql/ruleengine/rules_extended.go` 4247 行、`postgres/ruleengine/rules_extended.go` 3141 行、`oracle/ruleengine/rules_wait_deep.go` 2957 行——全部违反 `CLAUDE.md` "800 行上限"。规则写成 Go 是根因。

## 4. Slash-Command 中央注册表

hermes `hermes_cli/commands.py` 的 `COMMAND_REGISTRY` 是唯一真理源，以下 6 个下游消费者全部派生自它：

- CLI dispatch（`process_command()`）
- Gateway dispatch（`gateway/run.py`）
- Telegram `BotCommand` 菜单
- Slack 子命令路由表
- 自动补全（`SlashCommandCompleter`）
- `/help` 按类别分组

加一个 alias 只需改一个 tuple。opendb `internal/skill/registry.go` 只服务 CLI，help/补全各写一遍。

**证据：** hermes `AGENTS.md:137-178`。

## 5. Prompt Cache 不变性作为硬规矩

hermes `AGENTS.md:339-347` 把"不改过去上下文 / 不中途换 toolset / 不中途重载 memory 重建 system prompt"写成贡献者守则，只有压缩时才允许改动。

opendb 有 `provider.CacheControl` 类型（`adapter.go:29-32`）但没有对应的破坏性变更审计规则。context builder 的 `memory_manager.Reload()` 触发点没有护栏。

## 6. Provider / OAuth / 成本追踪的完整度

| 能力 | hermes | opendb |
|---|---|---|
| Provider 数量 | anthropic、bedrock、gemini_cloudcode、google_code_assist、openai、copilot_acp、nous_portal、openrouter… | anthropic、gemini、openaicompat、ollama、vllm、mlx |
| OAuth 流 | `google_oauth.py` 1048 行 | 无 |
| 凭据池 | `credential_pool.py` 1418 行 | 无 |
| 限流追踪 | `rate_limit_tracker.py`、`nous_rate_guard.py` | `internal/engine/retry/` 只做指数退避 |
| 成本计费 | `usage_pricing.py` 687 行（按 token 计费） | 无 |
| 智能路由 | `smart_model_routing.py` | 无 |

## 7. Profile（多实例隔离）

hermes `_apply_profile_override()` 在任何 import 前设置 `HERMES_HOME`，119 处 `get_hermes_home()` 自动作用域化；配合 `acquire_scoped_lock()` 防止两个 profile 抢同一个 bot token。

opendb 默认 `~/.opendb/`，想跑两个隔离实例（dev/prod DBA 各一套）只能自己 `HOME=`。

**证据：** `AGENTS.md:368-416`。

## 8. Subagent Delegation / 子任务并行

hermes `tools/delegate_tool.py` 允许从 agent 里 spawn 隔离子 agent，并把子输出压缩回父上下文（README "zero-context-cost turns"）。

opendb 的 overlord/cerebrate/drone 是集群角色而非 agent 内子任务。对于"并行采集 4 个节点 AWR 后汇总"这类诊断，目前只能串行。

## 9. MCP（Model Context Protocol）双向接入

- hermes 既是 **client**（`tools/mcp_tool.py` 2599 行，连外部 server）又是 **server**（`mcp_serve.py` 867 行，通过 `hermes mcp serve` 对外暴露 10 个工具）。
- opendb 两边都没有。

**价值：** MCP client 让 opendb 直接复用社区几百个 server（filesystem/github/postgres/slack…）；MCP server 让用户在 Claude Desktop / Cursor 里直接调 opendb 诊断工具，不必切终端。

## 10. 跨平台 Gateway

hermes `gateway/platforms/` 下 telegram / discord / slack / whatsapp / signal / homeassistant / qqbot / email 共 8 个平台适配器，同一 session 可跨 CLI ↔ Telegram 续聊。

opendb CLI-only。对 DBA 场景"夜间告警推 IM、在 IM 里直接继续排障"是刚需但目前做不到。

## 11. 训练闭环（Research-ready）

hermes 自带 `environments/`（Atropos RL env）+ `trajectory_compressor.py` + `batch_runner.py` + `mini_swe_runner.py`——把 agent 输出当下一代 tool-calling 模型训练数据。

opendb 内部 LLM 调优循环走不起来，因为：没有轨迹压缩、没批量复放、没 RL env 脚手架、JSONL 会话格式也不方便批处理。

## 12. Skin / Theme 引擎

hermes `hermes_cli/skin_engine.py` 纯 YAML 数据驱动，用户丢 `~/.hermes/skins/<name>.yaml` 即可换肤；内置 default / ares / mono / slate 四套。

opendb `internal/ui/repl.go:30-52` 所有颜色硬编码在源码里，改色要重新编译。

## 13. Cron 调度器

hermes `cron/jobs.py + scheduler.py` 支持"每天早 8 点用自然语言生成昨日报告并推 Telegram"的场景。

opendb `internal/scheduler/` 是内部周期采集，不是用户可配置的自然语言任务。

---

## 非对称说明

- hermes 通用 agent、opendb 数据库专用，**跨平台 gateway / skill marketplace / MCP** 不是对等对比项，opendb 可以选择不做。
- hermes 也有大文件（`run_agent.py` 11603、`cli.py` 10275），单文件治理未必比 opendb 好。
- opendb 的 `provider.ProviderAdapter` 接口设计简洁、编译期类型安全，这点 opendb 赢。

## 最值得借鉴的三件事（ROI 排序）

1. **SQLite + FTS5 会话存储** —— 几百行 Go 就能落地，立刻解锁 `/search`、`/resume history`。
2. **Markdown Skill 外置化** —— 把 `ruleengine/rules_*.go`（现在超 20k 行 Go）迁成 `~/.opendb/rules/*.md`，顺带解决单文件超标、社区扩展、增量发布三个问题。
3. **中央 CommandRegistry** —— 合并 help / 补全 / dispatch 三处重复定义。
