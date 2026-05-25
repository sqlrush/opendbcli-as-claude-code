# OpenDB 项目规则

## 项目概述

OpenDB 是数据库专用 CLI 客户端（类似 Claude Code 之于代码开发，OpenDB 之于数据库管理）。
Go 语言编写，单二进制零依赖部署。支持 Oracle、MySQL、PostgreSQL、OpenGauss。

## 技术栈

- 语言: Go
- Oracle 驱动: go-ora（纯 Go，无需 Oracle Client）
- TUI: bubbletea + lipgloss
- LLM 通信: OpenAI 兼容格式 + Ollama
- 部署: 单二进制拷贝 + 环境变量配置

## 代码规范

### 不可变数据

始终创建新对象，不要修改已有对象。函数返回新值而非原地修改。

### 文件组织

- 200-400 行为宜，800 行上限
- 按 feature/domain 组织，不按 type 组织
- 高内聚低耦合

### 错误处理

- 每层显式处理，不吞错误
- 用户侧给友好提示，日志侧记详细上下文
- 绝不假装成功：不确定是否成功就告诉用户"不确定"

### 函数规模

- 单函数不超过 50 行
- 不超过 4 层嵌套

## 架构原则

### 模型无关性

换模型只改配置不改代码。`llm.Provider` 接口隔离模型差异。

### 诊断三层分离

```
Layer 1: 探针层（永远 OpenDB 做）→ 数据采集、异常检测、爆发采集
Layer 2: 规则兜底（无 LLM 时）→ 规则分类，不出结论只陈述事实
Layer 3: LLM 诊断（有 LLM 时）→ 链式推理，根因分析，可执行方案
```

规则层只陈述事实不下结论。所有结论由 LLM 给出。

### 安全分级

- Level 0: 只读（SELECT, 查看状态）
- Level 1: 操作（kill session，二次确认）
- Level 2: 管理（DDL, ALTER SYSTEM，二次确认）
- Level 3: 危险（DROP TABLE/DATABASE，强制确认不可关闭）

## 措辞规范

根因描述禁止夸张用词：
- 禁用: "风暴""瓶颈""抖动""问题"
- 统一用: "xxx 冲高"（如"硬解析冲高""I/O 冲高"）
- 仅超阈值不等于数据库到极限，不能用定性用词

## LLM 诊断输出规则

- 修复建议暂时用**原生 SQL**，不用 opendb /命令（skill 覆盖率不够时容易出错）
- 诊断结论必须基于工具查询结果，**禁止编造数据**
- 引用对象名前必须用 sql skill 查询确认存在

## 规则引擎开发流程

1. 先在 ailinkdb/data 中生成规则数据（由 AI 审核）
2. 再基于规则数据生成 opendb ruleengine Go 代码
3. **绝不直接手写规则代码**，必须走数据→代码的流程

## 测试要求

### 基本验证

所有功能必须在 Oracle 测试服务器上以人的视角验证真实输出：
- `opendb -c oratest <command>` 批量模式捕获输出
- 检查: 表格对齐、无 `<nil>`、无 panic、中文正确
- 以终端真实输出为准，不是源码推理
- 验证结果保存到 docs/ 目录

### UI 变更验证（严格遵守）

**展示问题是最高优先级**，所有 UI 变更必须在真实终端验证，不能只跑 headless 测试：

1. **贴图错误零容忍**: 所有边框、表格的右边框必须对齐，不能换行溢出
2. **修改共用组件时必须验证所有调用方**: 不能只测目标场景（教训：改 picker.go 影响了 /login、/model、/llm 三个命令）
3. **验证所有路径**: 包括正常路径、空结果、错误路径
4. **scroll 模式验证**: 内容超过终端高度后，输入框固定底部、partial line 不残留、不覆盖已有内容
5. **宽度安全**: 避免 East_Asian_Width=Ambiguous 的 Unicode 字符，必须设置 `runewidth.EastAsianWidth = false`
6. **渲染方式**: 初始渲染（欢迎页等）用光标绝对定位，不用 `\r\n` 滚动（防空行 bug）
7. **全程禁止清屏**: 启动不清屏、退出不清屏、只有 `/clear` 才清屏

### LLM 行为相关改动（重大改动，需谨慎）

涉及以下领域的改动属于重大改动，必须充分测试后才能部署：
- Streaming 策略（Chat vs ChatStream、降级逻辑）
- 截断检测与恢复（finish_reason 传递、recoverTruncatedOutput）
- diagCh 事件发送机制（Done/Error 送达保障）
- OnStream 回调路径（flush 时机、chunk 数量）
- max_tokens / token 限制配置

**教训：streaming 策略改动引入了竞态条件（Done 事件丢弃），表现为间歇性故障（短响应正常，长响应卡住），极难排查。LLM 行为链路的任何改动都必须用多个模型、多种响应长度充分测试。**

**教训：截断恢复失效 — 数据管道 bug 必须端到端验证。**
SSE 流解析器（openaicompat.go）检测到 `finish_reason != nil` 后返回 `StreamEvent`，但遗漏了将值写入 `FinishReason` 字段，导致 engine 永远检测不到截断、恢复逻辑永远不触发。修复时只修了 legacywrapper（非流式路径），漏了 sseStream（流式路径）——同一字段的多条产出路径必须全部验证。具体经验：
1. **检测到不等于传递了**: `if x != nil` 只是条件判断，必须确认值被写入了返回结构体的对应字段
2. **修一条路径不等于修了全部**: 同一个字段（如 FinishReason）可能有流式/非流式/fallback 多条产出路径，每条都必须验证
3. **端到端验证，不能只看编译通过**: 修完应构造真实截断场景（如长输出诊断），确认 `resp.Truncated == true` 且恢复逻辑触发
4. **画完整数据流图再动手**: `LLM API → SSE解析 → StreamEvent → engine循环 → resp.Truncated → 恢复逻辑`，沿链逐节点检查字段传递

### UI 自动化测试

UI 变更后必须使用 PTY + midterm 终端模拟器 + golden file 测试框架验证：

```bash
# 跑全部 UI 测试
go test -count=1 -timeout 60s ./internal/ui/uitest/ -v

# 更新 golden file（版本号、连接名等预期变化时）
go test -count=1 ./internal/ui/uitest/ -run TestWelcome_Golden -args -update
```

测试框架位于 `internal/ui/uitest/`，用法：
- `NewTestTerminal(t, rows, cols)` — 在 PTY 中启动 opendb
- `tt.SendLine("/model")` — 模拟用户输入
- `tt.SendKey([]byte{0x1b, '[', 'B'})` — 模拟方向键
- `tt.WaitFor("pattern", timeout)` — 等待屏幕出现内容
- `tt.AssertContains(t, "text")` — 断言屏幕包含文本
- `tt.AssertNoOverflow(t)` — 断言无行溢出
- `tt.RequireGolden(t)` — golden file 对比

**改了 UI 就必须跑这个测试，不能只编译通过就部署。**

### UI 共用组件清单

修改以下文件时，必须验证所有使用方：
- `picker.go` → /login, /model, /llm (alert picker)
- `markdown.go` → 所有 LLM 输出渲染
- `term.go` → 所有 ANSI 操作
- `termwidth/` → 所有宽度计算
- `repl.go` → 输入框、滚动���partial line

## 编译

**Linux 用 `-tags full`，macOS 用 `-tags 'oracle mysql postgres opengauss gaussdb'`（去掉 dm）**

```bash
# Linux 本地编译（含全部 5 种 DB 驱动: Oracle/MySQL/PostgreSQL/OpenGauss/GaussDB/DM）
go build -tags full -o opendb ./cmd/opendb/

# macOS 本地编译（DM 驱动不支持 darwin，必须显式列其余 5 种 tag）
go build -tags 'oracle mysql postgres opengauss gaussdb' -o opendb ./cmd/opendb/

# 本地部署：/usr/local/bin 是 root:wheel，需 sudo
sudo cp opendb /usr/local/bin/opendb
# 或装到 ~/.local/bin（已在 PATH，无需 sudo，优先级更高）
cp opendb ~/.local/bin/opendb

# 交叉编译 Linux（dbaa for DM 必须走这条路）
GOOS=linux GOARCH=amd64 go build -tags full -o opendb-linux ./cmd/opendb/
```

**macOS DM 限制**：`internal/_dmdriver/security/` 只有 `zzg_linux.go` / `zzh_windows.go`，没有 darwin 实现（参见 `internal/dm/driver/driver.go` 顶部注释）。本机用 `-tags full` 在 macOS 上必报 `undefined: cipher*` 编译错。DM 客户端用法：交叉编译 Linux 二进制部署到目标机。

Build tags 说明:
- 无 tag 或 `-tags oracle`: 只包含 Oracle
- `-tags mysql`: 只包含 MySQL
- `-tags postgres`: 只包含 PostgreSQL
- `-tags gaussdb`: 只包含 GaussDB（华为 GaussDB(for openGauss) 集中式）
- `-tags dm`: 只包含 DM（达梦，仅 Linux/Windows）
- `-tags full`: 包含全部（Oracle + MySQL + PostgreSQL + OpenGauss + GaussDB + DM，仅 Linux/Windows 可成功）

GaussDB 与 OpenGauss 的区别：
- OpenGauss 用 `pgx/stdlib`（开源 OG，标准 SCRAM 兼容）
- GaussDB 用 `HuaweiCloudDeveloper/gaussdb-go`（华为商业版，SCRAM-SHA256(10) 自有协议）
- 两者技能层暂复用，未来需要分叉再说

## 配置

配置文件: `~/.opendb/config.yaml`

### 连接密码

连接配置中的密码有两种方式:
1. **明文 value**（开发测试用）: 不设 `auth_mode` 和 `provider`，只写 `credential.value`
2. **加密存储**（生产用）: 通过 `/login` 交互式输入密码，自动加密保存到 `~/.opendb/credentials/`

```yaml
# 方式 1: 明文（不设 auth_mode/provider，直接用 value）
connections:
  - name: oracle
    db_type: oracle
    host: 47.251.30.180
    port: 1521
    service: orclpdb1
    user: system
    credential:
      value: MyPassword123

# 方式 2: 加密（设了 auth_mode: save 就去 credentials 目录找加密文件）
  - name: oracle
    db_type: oracle
    auth_mode: save
    credential:
      provider: save
```

**注意: `auth_mode: save` + `credential.provider: save` 不会读 value 字段！** 它只读 credentials 目录的加密文件。如果用明文密码就不要设这两个字段。

### 模型配置

模型可以 inline 在 config.yaml 中，也可以放在 `models_dir` 目录下的 yaml 文件中:
- inline: config.yaml 中的 `models:` 数组
- 目录: `models_dir` 指向的目录下的 *.yaml 文件
- inline 优先于目录

## 部署

- 测试服务器: root@47.251.30.180
- 本地: 直接编译运行
- 启动命令: `opendb` / `dbaa`（在 PATH 中）
- 安装位置：
  - 推荐 `~/.local/bin/`（用户可写，已在 PATH，优先级高于 `/usr/local/bin`）
  - `/usr/local/bin/` 是 root:wheel，需 `sudo cp`

## 品牌分支策略（必须遵守）

opendb 通过 build tag 机制支持多个品牌定制分支。当前在维护：

| 分支 | build tag | 客户 | 二进制名 |
|---|---|---|---|
| `main` | （无）| 开源版 | `opendb` |
| `dbaa` | `-tags dbaa` | 中国农业银行 | `dbaa` |
| `linkdb` | `-tags linkdb` | 仁合时创 | `linkdb` |

**所有品牌分支与 main 同源同功能，仅品牌资源不同。**

### 同步规则

- **版本号同步**：所有品牌分支与 main 版本号始终一致。每次 main 发版时
  各品牌分支跟着 bump 同样的 patch。不允许独立维护版本号
- **功能同步**：opendb main 上的所有功能改动（feat/fix/refactor）必须
  同步到所有品牌分支。品牌分支不引入独立的功能开发
- **代码同步**：所有 `.go` 源代码、配置 schema、文档（除分支特有的
  `CLAUDE.md` 段落外）在所有分支应内容完全一致

### 唯一允许的差异

品牌分支与 opendb 仅在以下品牌资源上有差异，其他**任何代码差异都是 bug**：

1. **二进制名 / 显示名**：通过 `internal/brand/<brand>.go` (`-tags <brand>`)
   切换 `BinaryName`、`AppName`、`ConfigDirName` 等
2. **logo**：每个品牌分支用各自的 logo（welcome 页 + setup 页）
3. **欢迎页**（welcome 页）：显示对应品牌的欢迎语和数据库列表
4. **安装页**（setup wizard）：引导文案带对应品牌的术语

### 实施约束

- **禁止在分支硬改字符串**：所有品牌切换走 `internal/brand/` 的 build-tag
  分发，每个品牌一个 `<brand>.go` 文件，绝不在分支直接改
  `cmd/opendb/main.go` 等共用文件里的字符串
- **build tag 互斥**：每个品牌文件 build tag 都要排除其他品牌（如
  `default.go` 是 `!dbaa && !linkdb`），保证同时只有一个品牌 init() 激活
- **同步流程**：opendb main 合并新功能后，各品牌分支通过 `git merge main`
  或 `git checkout main -- <files>` 拉取，仅冲突项（CLAUDE.md 等）人工合并
- **CHANGELOG**：品牌分支的 `docs/CHANGELOG.md` 头部标 `(<brand> branch)`，
  内容跟 main 一致，每个版本注明 "与 main vX.Y.Z 同步"

### 检测分支差异（开源前/发版前必跑）

```bash
# 列出真实内容差异（应剩 0 个 .go 文件，部分 doc 文件如 CLAUDE.md 允许差异）
git diff --numstat origin/main origin/dbaa   -- '*.go' | awk '$1 > 0 && $2 > 0'
git diff --numstat origin/main origin/linkdb -- '*.go' | awk '$1 > 0 && $2 > 0'

# 期望输出: 空（0 个 .go 文件有内容差异）
```

### 新增品牌分支的步骤

1. `git checkout main && git checkout -b <brand>`
2. 创建 `internal/brand/<brand>.go`（参考 `dbaa.go` / `linkdb.go`）
3. 把所有现有品牌文件的 build tag 加上 `&& !<brand>`（互斥）
4. 测试编译：`go build -tags '<brand> oracle mysql postgres opengauss gaussdb' ./cmd/opendb/`
5. 更新本节"品牌分支策略"表格，加入新行
6. 同步到所有现有品牌分支（保持 brand 文件全局一致）

## 核心功能验证流程（必须遵守）

核心功能（如上下文系统、记忆系统、规范系统等跨模块集成功能）必须按以下流程验证：

1. **写完** → 编译通过
2. **单元测试** → 所有新包测试通过
3. **追踪调用链** → 用 grep 从入口到出口逐层确认代码存在、有调用方、参数传递正确
4. **端到端验证** → 在真实环境中跑通完整场景，不能只看编译和单元测试

**教训**：v0.9.42 上下文系统写了 session 加载/保存逻辑，单元测试通过，但：
- auto-save checkpoint 覆盖了 engine.go 的改动（代码丢失未发现）
- DiagnoseSkill 每次 new 新 Diagnoser，SetContextStores 从未被调用（接入层断裂）
- 结果：核心功能完全不工作，用户测试才发现

## Bug 处理流程（必须遵守）

遇到 bug **不要马上改代码**，按以下流程：

1. **分析原因** — 沿调用链 grep 定位断点，搞清楚数据在哪丢的
2. **和用户对齐** — 把原因和修复方案说清楚，确认后再动手
3. **修改代码** — 确认方案后才开始改
4. **验证** — 编译 → 测试 → 追踪调用链 → 端到端验证

**绝不能**：看到报错就直接改代码、改完才告诉用户改了什么。

## 架构与功能问答规范（必须遵守）

回答任何关于 opendb 当前架构、功能、实现状态的问题前，**必须先扫描代码**，不要凭记忆马上回答。

### 流程

1. **不要凭印象回答** — 哪怕觉得自己刚看过、记得清楚
2. **先 grep / read 代码** — 找到当前真实状态
3. **基于扫描结果回答** — 引用具体文件路径、行号、函数名作为证据
4. **如果扫描发现和自己原有认知不一致**：
   - 立即承认错误（致歉 + 修正）
   - 把扫描结果记录到 `docs/` 下的文档（如果该话题有相关设计文档，更新；否则新建 `docs/code-state-correction-YYYY-MM-DD-<topic>.md`）
   - 记录内容包括：当时错误的认知、实际代码状态、关键证据（文件路径 + 关键代码片段）

### 反例（这次会话的真实教训）

错误：用户问"其他库是否共享一套 prompt"，我没扫代码就答"是的，4 库都没有 per-DB facts"。

实际：grep 后发现 `internal/engine/profile/` 下 4 个文件（oracle.go / postgres.go / mysql.go / opengauss.go）每个都有 100+ 行 `SystemPromptRules()` 实现，叫法是 PromptProfile 不是 specificFacts。我用错了关键字 grep，导致结论错。

### 教训

- 关键字搜索失败不代表功能不存在 — 试 2-3 个不同关键字 + 看接口定义
- 看 `interface{}` / `Profile` 等抽象层，往往就是被找的功能
- 拿不准就读 `builder.go` / `register.go` 这种 wiring 层的代码，看它实际调了什么

## Commit 规范

```
<type>: <description>
```

Types: feat, fix, refactor, docs, test, chore, perf, ci

---

# 关联项目（OpenDB 生态全景 · 2026-05-03 整合）

OpenDB 不是孤立项目，它是一个生态主仓。以下 3 个项目共享 OpenDB 的代码、记忆和规则。**在 opendb 工作时如果触及 LLM 通信链路、UI 渲染或集群版功能，必须先翻对应项目的记忆**（已整合到 `~/.claude/projects/-Users-sqlrush-opendb/memory/` 下，前缀 `cerebrate-` / `opendbllm-` / `opendbui-`）。

## 🐝 Cerebrate · 集群版 · L4 自治平台（独立仓 sqlrush/cerebrate · ~/cerebrate）

**定位**：OpenDB 的大规模集群版本，L4 级 7×24 全自动数据库运维平台，1200+ 节点。**所有代码整合在 OpenDB 单二进制中**，通过 `--role worker/memory/manager` 区分角色。

### 三层 Agent 架构（星际虫族命名）
```
Manager Agent (Cerebrate/脑虫) — 全局编排，跨区域趋势，策略下发，Web 大盘
  └── Memory Agent (Overlord/王虫) — 区域协调（≤200节点），跨节点操作，持有 Worker 全部记忆和连接串
       └── Worker Agent (Drone/工蜂) — 单节点自治，Sentinel 监控，LLM 诊断，自主修复
```

**代码命名规则**：包名 `cerebrate/`、`overlord/`、`drone/`，结构体 `CerebrateServer`、`OverlordCoordinator`、`DroneAgent`。文档中用正式名称（Manager / Memory / Worker）。

### 核心设计原则
- **LLM 只查不改，用户可做任何事（产品核心策略）**：LLM 发起的数据库操作全是只读查询，变更操作只放在分析报告里；用户通过 / 命令发起的不受限制，但高危操作必须确认。预留接口 `exec_mode: report_only | confirm_exec | auto_exec`
- **向上兜底**：Worker 处理单节点；拿不准上交 Overlord；Overlord 持有所有 Worker 的 memory/policy/连接串
- **LLM 优先，Rule Engine 兜底**：Sentinel 异常 → 先调 LLM → 不可用降级 273+ Rule Engine
- **脑裂仲裁**：人 > 上级 LLM > 本地 LLM
- **崩溃恢复即首次诊断**：Worker 重启后扫记忆 + 数据库日志，LLM 判断当前状态生成恢复建议
- **管住手不堵嘴**：只读查询；记忆同步、上报通信、报告生成不受限

### 通信、状态、HA
- 三层间 gRPC 双向流（混合推拉）；CLI↔Daemon 走 Unix Socket，CLI↔Agent 走 TCP，一套 proto
- memory/policy 复用 OpenDB Engine V2（`~/.opendb/memory/`、`~/.opendb/policies/`），每分钟增量同步
- Rule（经验沉淀）：修复成功后自动生成 `~/.opendb/rules/*.md`，frontmatter trigger 自动建索引
- 审计日志：写操作 append-only 到 `~/.opendb/audit.log`，随 memory 同步
- Overlord ≥3，每个备份给 2 个其他 Overlord；Cerebrate 是特殊 Overlord（双角色共存）；Cerebrate 只存映射关系不存实际数据

### 集群部署命令
```bash
opendb cluster init  --role manager --listen 0.0.0.0:9100
opendb cluster join  --role memory  --cerebrate <addr> --token <token> --region <name>
opendb cluster join  --role node    --overlord  <addr> --token <token>
opendb agent start   --role worker  --overlord  <addr> --db-type oracle --db-conn "..."
opendb agent start   --role memory  --cerebrate <addr> --region china-east
opendb agent start   --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080
```

### 新增工具（基于 OpenDB 90+ 现有 skill）
- Worker 新增 1 个：`escalate_to_overlord`
- Overlord 新增 7 个：`get_worker_status` / `get_worker_memory` / `get_region_topology` / `broadcast_command` / `coordinate_failover` / `generate_region_report` / `escalate_to_cerebrate`
- Cerebrate 新增 6 个：`get_all_overlords` / `get_global_topology` / `push_policy` / `schedule_maintenance` / `generate_fleet_report` / `manage_cluster`
- Web UI：Cerebrate 监控大盘（全局拓扑 + 健康状态 + 报告下钻）

### 测试可观测性原则（硬性要求）
所有测试判断必须基于真实输出，禁止猜测。无论输出在管道、日志文件还是网页，Agent 都必须通过工具拿到真实内容再判断：

| 输出位置 | 获取方式 |
|---|---|
| 命令管道 | Bash 读 stdout/stderr |
| 日志文件 | Read / Grep |
| HTTP API | curl + jq |
| Web UI | Playwright 截图 + 断言 |
| 数据库状态 | SQL 查询 |
| TUI 交互 | bubbletea Model 测试 / go-expect / tmux |

每轮 CI/CD "通过"必须满足三条件：① 输出无 ERROR/FAIL/panic ② 截图渲染正确数据非占位符 ③ 数据完全符合预期（不允许"元素存在就算通过"）

### 三通道测试（强制）
- **Web UI**：REST API（httptest）+ E2E（curl+jq）+ Playwright 截图
- **故障恢复**：mock DB 单元 + Docker chaos 集成 + 真实 SQL 验证
- **TUI 交互**：Model 单元（直接调 Update/View）+ PTY 黑盒（uitest+midterm）+ Golden file

### 无人值守 CI/CD
- **RemoteTrigger**（首选 7×24 云端托管）：每 30 分钟读 `.pipeline/goals.md` → 拆解任务 → 编码 → 构建 → 测试 → 失败自修复 → 通过提交
- **CronCreate**（本地会话内，7 天过期）
- **`claude -p`**（单次 headless）
- 三层测试：故障注入（`/inject` 内置命令）、LLM 录制/回放（`--llm-record`/`--llm-replay`）、集群混沌测试

### 关键设计文档
- `~/cerebrate/docs/design/project_opendb_autopilot_brainstorm.md` — Q1-Q9 原始架构
- `~/cerebrate/docs/design/project_opendb_autopilot_24h_scenario.md` — 8 个核心 7×24 场景
- `~/cerebrate/docs/design/project_opendb_autopilot_qa_log.md` — Q1-Q28 完整决策记录
- `~/cerebrate/docs/design/project_opendb_autopilot_gap_analysis.md` — 差距分析

### 当前状态（截至 2026-04-15）
130 场景测试完成（F01-F80: 73/80 ✓ / C01-C30: 13/30 / C31-C50: 17/20 / LLM 诊断 90%）；5 项修复已合并 main；4 项 Rule 待办；CI/CD 8 里程碑 M1-M8。

---

## 🧠 opendbllm · LLM 通信优化专项（无 git · ~/opendbllm）

**定位**：LLM 通信链路深度优化专项工作，对应 ~/opendb 的 `feature/engine-v2` 分支（从 v0.9.23 拉出）。**严禁合入主干**——分支隔离是产品策略。

### 核心成果（已固化到 v0.9.42）
- 上下文/记忆/规范系统（参考 Claude Code）：跨会话上下文 + 实例记忆 + 多层规范 + prompt 引导 LLM 自管理
- PROFILE.md 实例画像（每实例一个，持续更新负载/问题特征，会话启动优先全量加载）
- /policy + /help 三层帮助命令

### 关键 Bug 教训（重要 · 改 LLM 链路前必读）
- **流式截断真正根因**：`diagCh` 256-buffer 溢出**静默丢弃**，**不是 engine/模型问题**。修 bug 不能停在表象层
- **截断恢复失效**：SSE 解析器检测到 `finish_reason != nil` 但**漏写入 FinishReason 字段**，engine 永远检测不到截断、恢复永不触发。教训：检测到 ≠ 传递了；修一条路径 ≠ 修了全部
- **engine-v2 回归 6 类**：Tools 未传、无流式、截断过激、prompt 数据缺失、picker 异常等

### 排查方法论
- 不猜。分层加日志，验证数据在哪丢的
- 画完整数据流图：`LLM API → SSE 解析 → StreamEvent → engine 循环 → resp.Truncated → 恢复逻辑`，沿链逐节点检查字段传递

### 编译/配置约束
- 必须 `-tags full`（不加 tag 只有 Oracle 驱动）
- 连接密码用明文 value 时**不设** `auth_mode` / `provider`，否则只读 credentials 加密文件
- 诊断命令是 `/llm` 不是 `/diag`（/diag 已弃用）

---

## 🎨 opendbui · UI/TUI 设计源仓（独立 working tree · ~/opendbui）

**定位**：OpenDB 的 TUI/UI 设计文档源仓库；v0.9.24-v0.9.25 UI 重构已发版完成。**Bug 修复仍归 opendb 主项目执行**——这里是设计源，不是代码源。

### 已完成（v0.9.24-v0.9.25）
- 15 项 UI 改动：测试框架 + chroma 高亮 + ANSI 封装 + SIGWINCH + Picker 统一 + 拆文件
- /health 表格换行错位（v0.9.24 修复）
- LLM 输出渲染表格错位 + SQL 渲染（v0.9.25 修复，表格竖转 + flush 安全）

### 5 条核心复盘原则（不换框架改 UI）
1. **先建测试**：UI 变更前先建 PTY+midterm+golden file 测试框架
2. **根因驱动**：渲染 bug 找根因不堆补丁
3. **借鉴理念不借鉴实现**：bubbletea 重构失败 → 渐进增强成功
4. **每步可发版**：每个改动独立可发版
5. **验证用户看到的**：以终端真实输出为准，不是源码推理

### 流程规范（已固化）
- 每个版本必须写 release 文档（`docs/CHANGELOG.md`，v0.9.24 起严格执行）
- UI 变动（渲染/交互/输出格式）必须跑 `internal/ui/uitest/`，新功能补用例

### 待修复 Bug（归 opendb 主项目）
- LLM 多轮只输出增量 — prompt 问题，需让最终输出自包含
- 补全列表触发时光标跳底部 — 输入 / 补全时视图强制滚到底部，不应跳动

---

## 🗺️ 跨项目工作流速查

| 当我要做… | 应该读… |
|---|---|
| OpenDB 主线 bug / feature | `MEMORY.md` 顶部的 96 个 opendb-原生记忆 |
| 改 LLM 通信 / streaming / 截断 | `opendbllm-*.md` 全部（尤其 truncation_root_cause、engine_v2_regression）|
| 改 TUI 渲染 / 表格 / 滚动 | `opendbui-*.md` + `feedback-no-framework-rewrite.md` + `debugging-alternate-screen.md` |
| 集群版 / 三层 Agent / gRPC | `cerebrate-*.md`（尤其 architecture_decisions、code_architecture、design_principle_escalation）|
| 测试可观测性 / CI/CD | `cerebrate-feedback_test_observability.md` + `feedback-test-before-deliver.md` |
| 上下文/记忆/规范系统 | `opendbllm-project_context_memory_system.md` + `opendbllm-project_instance_profile.md` |

源记忆仍保留在各自项目的 `~/.claude/projects/-Users-sqlrush-{opendbui,opendbllm,cerebrate}/memory/`，本目录是带前缀的整合副本。
