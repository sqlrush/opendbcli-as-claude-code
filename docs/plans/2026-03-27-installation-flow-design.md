# OpenDB Installation Flow Design

> Date: 2026-03-27
> Status: Draft
> Author: SQLRush

## 1. Overview

OpenDB 的安装流程采用 **方案 A（单脚本 + 应用内向导）**，参考 OpenClaw 的安装体验：

- **Phase 1**: `install.sh`（bash 脚本）— 检测环境、下载二进制、安装到 PATH
- **Phase 2**: `opendb --setup`（Go TUI 向导）— 交互式配置，生成配置文件

整个过程对用户来说是一条命令触发，全程交互式引导，无需手动执行任何命令。

```
curl -fsSL https://opendb.ai/install.sh | bash
```

## 2. Brand Identity

| 项目 | 内容 |
|------|------|
| 产品名 | OpenDB |
| 副标题 | DB CLI Agent as Claude Code |
| 主 slogan | 最少交互，最优诊断 \| Less input. More insight. |
| 开发者 | SQLRush |
| 官网 | https://opendb.ai（待购买） |
| 邮箱 | sqlrush@gmail.com |
| GitHub | https://github.com/sqlrush/opendb |
| 协议 | Apache 2.0 |

## 3. Phase 1: install.sh

### 3.1 支持平台

| OS | Arch | 二进制名 |
|----|------|---------|
| Linux | amd64 | opendb-linux-amd64 |
| Linux | arm64 | opendb-linux-arm64 |
| macOS | amd64 | opendb-darwin-amd64 |
| macOS | arm64 | opendb-darwin-arm64 |

### 3.2 二进制托管

- 国内节点 + 国外节点各一套，部署在云主机上
- 安装脚本通过测速或 IP 地理位置自动选择下载源

### 3.3 脚本流程

```
  ┌──────────────────────────────────────────┐
  │            OpenDB Installer              │
  │  DB CLI Agent as Claude Code             │
  │  最少交互，最优诊断 | Less input. More insight. │
  └──────────────────────────────────────────┘

✓ Detected: linux/amd64

Install plan
  OS:        linux
  Arch:      amd64
  Version:   v0.9.17 (latest)
  Install to: /usr/local/bin/opendb

[1/3] Downloading
  · Selecting mirror...
  ✓ Using China mirror
  · Downloading opendb v0.9.17...
  ✓ Download complete (43 MB)

[2/3] Installing
  · Installing to /usr/local/bin/opendb...
  ✓ OpenDB installed

[3/3] Verifying
  · Checking installation...
  ✓ opendb v0.9.17 ready

🚀 Starting setup wizard...
  Run 'opendb --setup' anytime to reconfigure.
```

### 3.4 脚本关键逻辑

1. **检测 OS/Arch** — `uname -s` + `uname -m`
2. **自动选源** — 测速或 IP 地理位置判断，国内走国内镜像，国外走国外节点
3. **下载二进制** — 带进度条，校验 sha256
4. **安装** — 默认 `/usr/local/bin/opendb`，需要 root 权限时用 `sudo`
5. **启动向导** — 自动执行 `opendb --setup`

## 4. Phase 2: opendb --setup（交互式向导）

### 4.1 技术实现

- 基于现有 bubbletea TUI 框架
- 参考 OpenClaw 的配色和页面风格（多色彩、边框面板、✓ 标记）
- 每个配置环节前穿插产品功能介绍，借安装流程让用户了解产品特色

### 4.2 完整流程（9 步）

```
 1. 欢迎页 + 品牌展示
 2. Setup 模式选择 (QuickStart / Custom)
 3. 数据库连接（类型选择 + 权限说明 + 配置 + 连通性测试）
 4. Sentinel 说明 + 配置
 5. LLM 说明 + 配置 + 连通性测试
 6. Rule Engine 说明 + 配置
 7. /命令技能展示
 8. 安全配置
 9. 配置文件生成 + 试运行 (help + health)
```

### 4.3 QuickStart vs Custom

| 步骤 | QuickStart | Custom |
|------|-----------|--------|
| 1. 欢迎页 | ✅ | ✅ |
| 2. 模式选择 | ✅ | ✅ |
| 3. 数据库连接 | ✅ 完整配置 | ✅ 完整配置 |
| 4. Sentinel | 跳过，默认值（自动启动，1s 间隔） | ✅ 完整配置 |
| 5. LLM | ✅ 完整配置 | ✅ 完整配置 |
| 6. Rule Engine | 跳过，默认开启 | ✅ 完整配置 |
| 7. /命令展示 | ✅ 展示 | ✅ 展示 |
| 8. 安全配置 | 跳过，默认值（confirm_on_dangerous=true） | ✅ 完整配置 |
| 9. 生成 + 试运行 | ✅ | ✅ |

### 4.4 Step 1: 欢迎页

展示内容：
- ASCII art 大型 Logo
- 副标题: DB CLI Agent as Claude Code
- 主 slogan: 最少交互，最优诊断 | Less input. More insight.
- 版本号、开发者、协议、官网、GitHub、联系方式
- 产品核心能力简介：
  - 多数据库支持 — Oracle / MySQL / PostgreSQL
  - LLM 智能诊断 — 自然语言描述问题，LLM 给出方案
  - Sentinel 实时监控 — 自动检测异常，秒级响应
  - Rule Engine — LLM 不可用时的兜底决策引擎
  - 技能系统 — /开头的命令，一键完成复杂操作
  - 单二进制零依赖 — 开箱即用

### 4.5 Step 2: Setup 模式选择

```
◆  Setup mode
│  ● QuickStart — 只配数据库连接和 LLM，其余用默认值（推荐）
│  ○ Custom    — 完整配置（数据库 + Sentinel + LLM + Rule + 安全）
```

### 4.6 Step 3: 数据库连接

#### 4.6.1 数据库类型选择

附产品介绍：OpenDB 用同一套交互体验覆盖主流数据库，无论哪种数据库，诊断、监控、技能命令都保持一致。

```
◆  Select your database type
│  ● Oracle
│  ○ MySQL
│  ○ PostgreSQL
```

#### 4.6.2 账号权限说明

根据数据库类型动态展示不同的权限说明面板：

- **推荐权限（最小必要集）** — 按数据库类型列出具体 GRANT 语句
- **不建议使用的权限** — 如 SYSDBA/DBA/root
- **建议执行的 SQL** — 创建专用账号的完整示例

允许用户选择"我需要更多时间准备账号"退出，后续用 `opendb --setup` 继续。

#### 4.6.3 连接配置

交互式表单：Connection name / Host / Port / Service(SID/DBName) / Username / Authentication method

支持的认证方式（7 种）：
1. `prompt` — 每次连接时输入密码
2. `save` — 加密保存到本地（AES-256-GCM）
3. `wallet` — Oracle Wallet 自动登录
4. `os` — 操作系统认证
5. `ldap` — LDAP/AD 认证
6. `kerberos` — Kerberos 认证
7. `token` — OAuth2 令牌认证

#### 4.6.4 连通性测试 + 权限校验

配置完成后自动执行：

1. **连接测试** — 验证网络连通性和认证
2. **权限校验** — 逐项检查必要权限是否具备
3. **权限过大检测** — 检测是否有 DBA/SYSDBA 等过大权限，给出风险提示
4. **结果汇总** — 显示通过/缺失/警告项，允许用户选择继续或重新配置

### 4.7 Step 4: Sentinel 说明 + 配置（Custom 模式）

功能说明面板：
- Sentinel 是实时异常检测引擎
- 后台每秒一次轻量采集数据库核心指标（活跃会话、CPU、IO、锁等待、慢 SQL、Redo 速率、硬解析率）
- 异常冲高时自动进入高频采集模式（200ms），捕获问题现场，生成根因分析
- 对数据库性能影响 < 0.1%，可安全用于生产环境

配置项：
- 是否自动启动（默认 Yes）
- 采集间隔（1s / 3s / 5s / 10s）

### 4.8 Step 5: LLM 说明 + 配置

功能说明面板：
- OpenDB 内置 LLM 诊断引擎
- 用自然语言描述数据库问题，LLM 分析根因并给出可直接执行的 SQL 修复方案
- 支持 Ollama 本地部署，数据不出内网

配置项：
- 是否配置 LLM / 跳过
- Ollama API 地址
- 模型名称

连通性测试：
1. 测试 API 地址是否可达
2. 测试模型名是否存在
3. 测试基本推理是否正常返回
4. 失败时提示具体原因，允许修改或跳过

### 4.9 Step 6: Rule Engine 说明 + 配置（Custom 模式）

功能说明面板：
- Rule Engine 是 OpenDB 的规则决策引擎
- 当 LLM 无法工作时（网络不通、模型不可用），由 Rule Engine 承担诊断决策
- 内置数十条经过验证的数据库诊断规则，覆盖常见性能问题

配置项：
- 是否开启 Rule Engine（默认开启）

### 4.10 Step 7: /命令技能展示

展示 OpenDB 的 /命令列表，让用户了解功能全貌。按分类分组展示：

- **监控类** — /top, /session, /lock, /redo 等
- **查询类** — /sql, /plan, /tablespace 等
- **管理类** — /kill, /param 等
- **诊断类** — /diag, /llm, /health 等
- **配置类** — /conn, /model 等

### 4.11 Step 8: 安全配置（Custom 模式）

配置项：
- 危险操作确认开关（默认开启）

### 4.12 Step 9: 配置生成 + 试运行

1. **生成配置文件** — 显示生成的文件路径
   - `~/.opendb/config.yaml`
   - `~/.opendb/connections/<name>.yaml`
2. **试运行** — 自动执行 `help` 和 `health` 命令，展示实际输出
3. **完成提示** — 常用命令提示 + 后续配置方式

```
  Useful commands:
  · opendb                  — 启动交互式 REPL
  · opendb -c <connection>  — 连接到指定数据库
  · opendb configure        — 添加更多连接或修改配置
  · opendb --setup          — 重新运行完整配置向导
  · opendb --version        — 查看版本
```

## 5. opendb configure（后续配置）

安装完成后，用户通过 `opendb configure` 进入配置管理，交互式菜单：

```
◆  What would you like to configure?
│  ● Add/Edit database connection
│  ○ Sentinel settings
│  ○ LLM settings
│  ○ Rule Engine settings
│  ○ Security settings
│  ○ Full setup (reconfigure all)
```

与 `opendb --setup` 共用同一套 TUI 组件，但只做配置，不展示欢迎页和产品介绍。

## 6. Design Constraints

- **术语规范**: 所有界面文字使用"LLM"而非"AI"
- **视觉风格**: 参考 OpenClaw 的配色和页面布局（多色彩、彩色边框面板、✓/⚠ 标记），具体配色方案实现时确定
- **数据库权限**: 配置连接串时必须说明最小权限集 + 连通性测试 + 权限过大过小提示
- **LLM 连通性**: LLM 配置完成后必须执行连通性测试（地址/模型/推理）
- **多连接支持**: 初始化只配一个数据库 + 一个 LLM，后续通过 `opendb configure` 添加更多
- **数据库类型**: 本版只支持 Oracle / MySQL / PostgreSQL（不含 OpenGauss）
