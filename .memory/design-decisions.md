# OpenDB Design Decisions

## Core Design Philosophy
**最少字符，最大效果** — 让 DBA 输入更少的字符来达成复杂的任务

## Interaction Model
- 交互模式为主，完全参考 Claude Code
- `opendb` 启动进入交互命令窗口
- 无模型模式：`/` 前缀快速匹配命令（如 `/login`, `/slowsql`, `/slowsql 10`）
- 有模型模式：支持自然语言输入
- `/login` 自动带出最近连接，回车直连
- 命令尽可能快捷化，常用操作一个短命令搞定

## Tech Stack
- **语言**: Go
- **Oracle 驱动**: go-ora（纯 Go，零外部依赖）
- **通信协议**: HTTP API（OpenAI 兼容格式）
- **LLM 后端**: 多后端支持（AilinkDB/Ollama、Claude API、OpenAI API），可配置
- **支持无模型模式**: 纯 CLI 工具箱，人工直接调用

## Skill System
- **混合模式**: 核心 skill 用 Go 内置，同时支持外部插件（Python/Shell/C）
- **双入口**: 每个 skill 同时支持 AI 调用（JSON）和人工命令行调用（CLI flags）
- **基础工具**: 以 AilinkDB 的 17 个工具定义为起点，扩展更多
- **外部插件协议**: stdin JSON 输入，stdout JSON 输出

## Database Support
- MVP 先做 Oracle（最复杂，场景最多）
- 架构预留 MySQL、PostgreSQL 扩展

## Security Model
- **Level 0（默认）**: 只读 — SELECT, SHOW, 查看状态
- **Level 1 操作员**: kill_session, 修改参数 — 二次确认（可关闭）
- **Level 2 管理员**: DDL, 备份恢复 — 二次确认（可关闭）
- **Level 3 危险操作**: DROP TABLE/DATABASE 等 — 二次确认（无法关闭）

## Connection Management
- 分组文件管理: `~/.opendb/connections/` 或可配置路径
- 统一配置格式，密码策略按连接独立配置
- 密码策略支持: encrypted（本地加密）、prompt（每次输入）、vault（企业密钥管理）
- 首次使用交互式引导生成配置

## Output & Display (核心体验，需精心打造)
- **富终端渲染**: 表格、颜色、高亮、分组，层次分明，参考 Claude Code 输出风格
- **TUI 框架**: bubbletea + lipgloss（Go TUI 事实标准）
- **实时刷新**: 带刷新功能的命令持续刷新，结束后停在最后一次快照，留在屏幕上作为历史
- **智能截断**: 数据量太大时（如几万行查询结果）只展示前几行，不刷屏
- **关键信息定位**: 输出数据/日志中有关键信息时，自动跳到关键位置，上下省略
- **多格式支持**: 默认富终端，支持 --format json/csv 给脚本用

## Command System
- 扁平化命令体系，`/` 前缀触发
- 实时模糊匹配下拉（随输入字符自动筛选），参考 Claude Code
- 输入路由三种模式：`/xxx` = 快捷命令，直接 SQL = 执行查询，自然语言 = AI 对话

## Input Routing
- `/` 开头 → slash command
- SQL 关键字开头（SELECT/INSERT/UPDATE/DELETE/ALTER/CREATE 等）→ 直接执行 SQL
- 其他文本（连了 LLM）→ 自然语言 AI 对话
- 其他文本（未连 LLM）→ 提示用户使用 / 命令或输入 SQL

## MVP Commands (第一期，已确认)
- `/login` — 连接/切换实例（自动带出最近连接）
- `/logout` — 断开连接
- `/dbtop` — 实时刷新全屏监控
- `/sessions` — 所有会话
- `/activesessions` — 活跃会话
- `/waits` — 非 idle 等待事件
- `/locks` — 行锁/表锁
- `/latches` — latch 争用
- `/mutexes` — mutex 争用
- `/slowsql` — 慢查询（支持阈值参数，如 /slowsql 5000）
- `/explain` — 执行计划（支持 SQL 文本和 SQL ID）
- `/health` — 综合健康巡检（实例/空间/性能/连接/慢查询/等待/备份/告警）
- `/space` — 表空间使用
- `/params` — 数据库参数（智能关联参数）
- `/alert` — 告警日志（带上下文、相同错误收敛）
- `/backup` — 备份状态
- `/kill` — 终止会话（L1，二次确认）
- `/standby` — 备库/Data Guard 状态
- `/tableinfo` — 表详情
- `/indexadvise` — 索引推荐（支持 SQL 文本和 SQL ID）
- `/history` — 历史命令和输出
- `/help` — 命令列表
- `/config` — 查看/修改配置
- `/clear` — 清屏
- 直接输入 SQL — 执行查询（安全级别按 SQL 类型动态判断 L0-L3）

## Connection Switching & Context
- 一次连一个实例，支持在多个实例间快速跳转
- **顶部固定状态栏**: 始终显示当前连接的实例信息（实例名、IP、数据库版本等），防止误操作
- **会话恢复**: 切回某个实例时，可还原该实例之前的历史命令和输出（可配置开关）
- 每个实例维护独立的会话上下文

## Session History
- 完整会话记录：命令 + 输出结果全部保存，按实例隔离
- 刷新类命令结束后保留最后一次快照在屏幕上
- 可回溯查看历史会话

## Plugin Ecosystem (重要战略方向)
- OpenDB 是数据库工具平台，各独立项目以插件形式接入
- **已有项目**:
  - sqlparser (Python): SQL 方言转换（Oracle/MySQL/PgSQL 互转），项目在 ~/sqlparser
  - dbtop (Python): Oracle 实时监控 TUI，项目在 ~/dbtop
- **规划中**:
  - 数据同步工具（类似 OGG/Debezium），在 OpenDB 中启动同步链路
- **插件运行方式**:
  - ① 在 OpenDB 交互窗口内运行（如 sqlparser 翻译结果直接输出）
  - ② 启动独立页面/进程（如 dbtop 全屏监控）
  - 都以 opendb 作为统一入口
- 插件可用任意语言编写（Python/Shell/C/Go）

## Multi-Statement Support
- 支持一次发送多条命令，类似 sqlplus
- 不支持批量多实例操作，只对当前连接的单实例操作

## Error Handling & Reconnection
- **只读操作**: 连接断开后静默重连，用户无感知
- **写/危险操作**: 断连后提示用户确认重连
  - 明确未成功：告诉用户"操作未成功"
  - 不确定是否成功：告诉用户"不确定，请自行确认"
  - 绝不假装成功

## Cross-Project Integration
- AilinkDB (~/ailinkdb) 项目记忆已同步 OpenDB 信息
- AilinkDB 的 17 个工具定义将由 OpenDB 实现
- AilinkDB P3 阶段（Function Calling/Agent）与 OpenDB 直接对接
- 两个项目的 Claude 记忆互相引用

## Deployment & Upgrade
- 单二进制拷贝 + 环境变量即可部署
- 首次连接/使用时交互式引导完成配置
- 支持大规模 Linux 服务器批量部署
- **离线升级**: 不连公网，用户把升级包放到指定目录，执行一条升级命令即完成
- 不支持在线自动更新
