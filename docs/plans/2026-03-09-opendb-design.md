# OpenDB 设计文档

> 日期: 2026-03-09
> 状态: 已确认

## 1. 项目定位

OpenDB 是一个数据库专用的交互式 CLI 客户端，类似 Claude Code 但专注数据库领域。

- **AilinkDB** = 大脑（AI 决策层，微调的 Qwen3.5-9B DBA 专家模型）
- **OpenDB** = 手脚（执行层，通过 skill/工具实施 AilinkDB 的决策）
- 同时支持无模型模式，人工直接通过命令行调用工具解决数据库问题

### 核心设计理念

**最少字符，最大效果** — 让 DBA 输入更少的字符来达成复杂的任务。

## 2. 技术栈

| 组件 | 选型 | 理由 |
|------|------|------|
| 语言 | Go | 单二进制，零依赖，跨平台，CLI 生态成熟 |
| Oracle 驱动 | go-ora | 纯 Go 实现，无需 Oracle Client |
| TUI 框架 | bubbletea + lipgloss | Go TUI 事实标准，支持富渲染 + 实时刷新 |
| LLM 通信 | HTTP API（OpenAI 兼容） | Ollama 原生支持，统一接口 |

## 3. 系统架构

### 3.1 架构选型：单体分层架构

```
┌──────────────────────────────────────────────────────────┐
│                    OpenDB 单二进制                        │
│                                                          │
│  ┌─────────────────────────────────────────────────────┐ │
│  │  UI 层 (bubbletea + lipgloss)                       │ │
│  │  顶部状态栏 | 命令输入 | 输出渲染 | 模糊匹配下拉     │ │
│  └──────────────────────┬──────────────────────────────┘ │
│                         │                                │
│  ┌──────────────────────▼──────────────────────────────┐ │
│  │  命令路由层 (dispatch)                               │ │
│  │  /login /slowsql /explain ...                       │ │
│  │  输入路由 + 模糊匹配 + 自然语言分发                  │ │
│  └─────┬───────────────────────────────┬───────────────┘ │
│        │ 无模型模式                     │ 有模型模式      │
│        ▼                               ▼                │
│  ┌───────────┐                 ┌──────────────────┐     │
│  │ Skill 引擎│                 │ LLM 适配层       │     │
│  │           │◄────────────────│ AilinkDB/Ollama  │     │
│  │ 内置 skill│  Function Call  │ Claude API       │     │
│  │ 外部插件  │────────────────►│ OpenAI API       │     │
│  └─────┬─────┘  tool_result   └──────────────────┘     │
│        │                                                │
│  ┌─────▼──────────────────────────────────────────────┐ │
│  │  数据库抽象层                                       │ │
│  │  Oracle (go-ora) | MySQL (预留) | PgSQL (预留)      │ │
│  └─────┬──────────────────────────────────────────────┘ │
│        │                                                │
│  ┌─────▼──────────────────────────────────────────────┐ │
│  │  基础设施                                           │ │
│  │  连接管理 | 权限控制 | 会话历史 | 插件管理 | 配置    │ │
│  └────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

### 3.2 三种运行模式

1. **AI 驱动（完整模式）**: 用户自然语言 -> AilinkDB 决策 -> OpenDB 执行 skill -> 返回结果
2. **其他 LLM 驱动**: 用户自然语言 -> Claude/GPT 决策 -> OpenDB 执行 skill -> 返回结果
3. **纯手动（无模型）**: 用户直接在命令行调用 `/` 命令或输入 SQL，无需任何 AI

### 3.3 输入路由

- `/` 开头 -> slash command（快捷命令）
- SQL 关键字开头 -> 直接执行 SQL
- 其他文本（连了 LLM）-> 自然语言 AI 对话
- 其他文本（未连 LLM）-> 提示用户使用 `/` 命令或输入 SQL

## 4. 目录结构

```
opendb/
├── cmd/opendb/main.go
├── internal/
│   ├── config/              # 全局配置加载与验证
│   ├── version/             # 构建时版本注入
│   ├── ui/                  # TUI (bubbletea + lipgloss)
│   │   ├── app.go           # 主程序
│   │   ├── statusbar.go     # 顶部固定状态栏
│   │   ├── input.go         # 命令输入 + 模糊匹配下拉
│   │   ├── output.go        # 输出渲染
│   │   └── refresh.go       # 实时刷新组件
│   ├── dispatch/            # 命令路由 + 模糊匹配
│   │   ├── dispatcher.go
│   │   └── matcher.go
│   ├── llm/                 # LLM 适配层
│   │   ├── provider.go      # Provider interface
│   │   ├── ollama.go        # AilinkDB / Ollama
│   │   ├── claude.go        # Claude API
│   │   ├── openai.go        # OpenAI API
│   │   └── prompt/          # 提示词模板
│   ├── skill/               # Skill 引擎
│   │   ├── executor.go      # 调度器
│   │   ├── registry.go      # skill 注册表
│   │   └── builtin/         # 内置 skill
│   │       ├── query/       # execute_sql, explain, slow_queries
│   │       ├── monitor/     # dbtop, health, sessions, waits, locks
│   │       ├── schema/      # tableinfo, indexadvise
│   │       └── admin/       # kill, space, params, backup, alert
│   ├── db/                  # 数据库驱动抽象
│   │   ├── driver.go        # Driver interface only
│   │   └── oracle/          # Oracle 实现 (go-ora)
│   ├── connection/          # 连接生命周期 + 切换 + 重连
│   ├── credential/          # 密码策略（独立包）
│   │   ├── encrypted.go
│   │   ├── prompt.go
│   │   └── vault.go
│   ├── security/            # 权限 + 危险 SQL 检测 + 审计
│   │   ├── permission.go    # 四级权限模型
│   │   ├── confirm.go       # 二次确认
│   │   ├── sqlguard.go      # 危险 SQL 检测
│   │   └── audit.go         # 审计日志
│   ├── format/              # 输出格式化（表格/CSV/JSON）
│   ├── session/             # 会话历史 + 恢复
│   ├── logger/              # 结构化日志
│   └── plugin/              # 插件管理
│       ├── manager.go       # 安装/加载/卸载
│       ├── manifest.go      # manifest.yaml 解析
│       ├── executor.go      # 外部插件执行
│       └── protocol.go      # 插件通信协议
├── configs/default.yaml
├── scripts/
├── docs/plans/
├── Makefile
└── go.mod
```

## 5. 核心接口

### 5.1 数据库驱动

```go
// internal/db/driver.go

// 构造函数（不在接口上，各驱动独立实现）
// func NewOracleDriver(ctx context.Context, cfg ConnectionConfig) (Driver, error)

type Driver interface {
    Close() error
    Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
    Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
    BeginTx(ctx context.Context, opts *TxOptions) (Tx, error)
    Ping(ctx context.Context) error
    ServerInfo() ServerInfo  // 连接时缓存
}

type Tx interface {
    Query(ctx context.Context, sql string, args ...any) (*QueryResult, error)
    Exec(ctx context.Context, sql string, args ...any) (*ExecResult, error)
    Commit() error
    Rollback() error
}

// 可选能力，按需断言: if insp, ok := drv.(Inspector); ok { ... }
type Inspector interface {
    Databases(ctx context.Context) ([]string, error)
    Schemas(ctx context.Context) ([]string, error)
    Tables(ctx context.Context, schema string) ([]TableInfo, error)
    Columns(ctx context.Context, schema, table string) ([]ColumnInfo, error)
}
```

### 5.2 LLM Provider

```go
// internal/llm/provider.go
type Provider interface {
    Chat(ctx context.Context, req ChatRequest) (*Response, error)
    ChatStream(ctx context.Context, req ChatRequest) (Stream, error)
    Name() string  // "ailinkdb" / "claude" / "openai"
}

type ChatRequest struct {
    Messages    []Message
    Tools       []ToolDef
    MaxTokens   int
    Temperature *float64  // nil = provider default
}

type Stream interface {
    Next() (StreamEvent, error)  // io.EOF = done
    Close() error
}
```

### 5.3 Skill

```go
// internal/skill/skill.go
type Skill interface {
    Name() string                                              // "slowsql"
    Description() string                                       // 模糊匹配时展示
    ToolDef() ToolDef                                          // 给 LLM 的 Function Calling 定义
    CLIDef() CLIDef                                            // CLI 注册信息（命令名/别名/flags/用法）
    Validate(params Params) error                              // 参数校验（执行前）
    Execute(ctx context.Context, params Params) (*Result, error) // 统一执行
    SecurityLevel() SecurityLevel                              // Level 0-3
}

type SecurityLevel uint8

const (
    LevelReadOnly  SecurityLevel = 0  // SELECT, SHOW
    LevelOperator  SecurityLevel = 1  // kill_session, 修改参数
    LevelAdmin     SecurityLevel = 2  // DDL, 备份恢复
    LevelDangerous SecurityLevel = 3  // DROP, TRUNCATE（确认不可关闭）
)

type Result struct {
    Type     ResultType         // Table / Text / Refresh / Error
    Data     any                // 具体数据
    Summary  string             // 给 LLM 的文本摘要
    Metadata map[string]string  // 行数、耗时等
}
```

### 5.4 安全守卫

```go
// internal/security/guard.go
type Guard interface {
    Authorize(ctx context.Context, level SecurityLevel, action string) error
    RequestConfirmation(ctx context.Context, action string) (bool, error)
    MaxLevel() SecurityLevel
}
```

### 5.5 插件

```go
// internal/plugin/manifest.go
type Manifest struct {
    Name           string        `yaml:"name"`
    Version        string        `yaml:"version"`
    Commands       []string      `yaml:"commands"`
    Executable     string        `yaml:"executable"`
    RunMode        RunMode       `yaml:"run_mode"`         // inline / fullscreen
    SecurityLevel  SecurityLevel `yaml:"security_level"`
    Timeout        time.Duration `yaml:"timeout"`
    DatabaseAccess bool          `yaml:"database_access"`
}
```

## 6. 安全模型

### 6.1 四级权限

| 级别 | 名称 | 操作范围 | 二次确认 |
|------|------|---------|---------|
| L0 | 只读（默认） | SELECT, SHOW, 查看状态/参数/日志 | 无 |
| L1 | 操作员 | kill_session, 修改参数, 索引操作 | 可关闭 |
| L2 | 管理员 | DDL, 备份恢复, 复制管理 | 可关闭 |
| L3 | 危险操作 | DROP TABLE/DATABASE, TRUNCATE | **不可关闭** |

### 6.2 重连安全策略

- **只读操作**: 断连后静默重连
- **写/危险操作**: 断连后提示用户确认重连
  - 明确未成功: 告诉用户"操作未成功"
  - 不确定是否成功: 告诉用户"不确定，请自行确认"

## 7. 连接管理

### 7.1 配置文件

```yaml
# ~/.opendb/connections/production.yaml
group: production
tags: [oracle]

connections:
  - name: prod-core-01
    host: 10.0.1.100
    port: 1521
    service: orcl
    user: dbadmin
    credential:
      provider: vault
      vault_path: secret/oracle/prod-core-01

  - name: prod-core-02
    host: 10.0.1.101
    port: 1521
    service: orcl
    user: dbadmin
    credential:
      provider: encrypted
      value: "enc:AES256:xxxxx"

  - name: prod-readonly
    host: 10.0.1.102
    port: 1521
    service: orcl
    user: reader
    credential:
      provider: prompt
```

### 7.2 连接特性

- `/login` 统一处理首次连接和切换（已登录时再次 `/login` = 切换实例）
- 顶部固定状态栏始终显示当前实例信息（实例名、IP、版本），防止误操作
- 切回某实例时可恢复该实例的历史命令和输出（可配置）
- 首次使用交互式引导生成配置

## 8. 输出与显示

### 8.1 设计原则（核心体验）

- **富终端渲染**: 表格、颜色、高亮、分组，层次分明，参考 Claude Code 输出风格
- **实时刷新**: `/dbtop` 等命令持续刷新，结束后停在最后一次快照作为历史
- **智能截断**: 数据量太大（如几万行查询）只展示前几行，不刷屏
- **关键信息定位**: 日志/数据中有关键信息时自动跳到关键位置，上下省略
- **错误收敛**: `/alert` 相同错误显示一次 + 发生次数，附带上下文日志

### 8.2 输出格式

- 默认: 富终端渲染
- `--format json`: JSON 格式
- `--format csv`: CSV 格式

## 9. 插件生态

### 9.1 定位

OpenDB 是数据库工具平台，独立项目以插件形式接入。

### 9.2 已有/规划项目

| 插件 | 语言 | 功能 | 运行方式 |
|------|------|------|---------|
| sqlparser | Python | SQL 方言转换（Oracle/MySQL/PgSQL 互转） | inline |
| dbtop | Python | 实时监控 TUI | fullscreen |
| 数据同步 | 规划中 | 类似 OGG/Debezium | inline |

### 9.3 插件安装

- 离线安装: 安装包放到指定目录，一条命令完成部署
- 支持 `--yes --quiet` 参数，配合 Ansible 等工具批量推送
- 安装包内含 `manifest.yaml` 自描述，OpenDB 自动注册命令

## 10. 部署与升级

- **安装**: 单二进制拷贝 + 环境变量配置
- **首次引导**: 第一次连接/使用时交互式引导生成配置文件
- **升级**: 离线模式，升级包放到指定目录，执行升级命令即完成
- **目标环境**: 不连公网的生产环境 Linux 服务器，支持大规模批量部署

## 11. MVP 命令表

| # | 命令 | 功能 | 安全级别 |
|---|------|------|---------|
| 1 | `/login` | 连接/切换数据库实例 | L0 |
| 2 | `/logout` | 断开当前连接 | L0 |
| 3 | `/dbtop` | 实时刷新全屏监控 | L0 |
| 4 | `/sessions` | 所有会话 | L0 |
| 5 | `/activesessions` | 活跃会话 | L0 |
| 6 | `/waits` | 非 idle 等待事件 | L0 |
| 7 | `/locks` | 行锁/表锁 | L0 |
| 8 | `/latches` | latch 争用 | L0 |
| 9 | `/mutexes` | mutex 争用 | L0 |
| 10 | `/slowsql` | 慢查询（支持阈值参数） | L0 |
| 11 | `/explain` | 执行计划（支持 SQL 文本和 SQL ID） | L0 |
| 12 | `/health` | 综合健康巡检 | L0 |
| 13 | `/space` | 表空间使用 | L0 |
| 14 | `/params` | 数据库参数（智能关联） | L0 |
| 15 | `/alert` | 告警日志（上下文 + 收敛） | L0 |
| 16 | `/backup` | 备份状态 | L0 |
| 17 | `/kill` | 终止会话（二次确认） | L1 |
| 18 | `/standby` | 备库/Data Guard 状态 | L0 |
| 19 | `/tableinfo` | 表详情（结构/索引/数据量） | L0 |
| 20 | `/indexadvise` | 索引推荐（支持 SQL 文本和 SQL ID） | L0 |
| 21 | `/history` | 历史命令和输出 | L0 |
| 22 | `/help` | 命令列表 | L0 |
| 23 | `/config` | 查看/修改配置 | L0 |
| 24 | `/clear` | 清屏 | L0 |
| - | 直接输入 SQL | 执行查询 | L0-L3 |

## 12. 与 AilinkDB 的集成

- AilinkDB 的 17 个工具定义（`tools/db_tools_schema.json`）由 OpenDB 实现
- 通信协议: HTTP API（Ollama OpenAI 兼容接口）
- AilinkDB P3 阶段（Function Calling/Agent）与 OpenDB 直接对接
- Agentic Loop: 用户输入 -> LLM 决策 -> Function Call -> Skill 执行 -> 结果返回 LLM -> 渲染输出
