# OpenDB 安装手册

> **版本**: v0.8.00
> **项目地址**: https://github.com/sqlrush/opendb
> **支持平台**: Linux (amd64 / arm64) · macOS (Intel / Apple Silicon)

---

## 目录

1. [前置要求](#1-前置要求)
2. [获取源码](#2-获取源码)
3. [编译](#3-编译)
4. [目录结构初始化](#4-目录结构初始化)
5. [主配置文件](#5-主配置文件)
6. [数据库连接配置](#6-数据库连接配置)
7. [启动与基本使用](#7-启动与基本使用)
8. [AI 诊断（可选）](#8-ai-诊断可选)
9. [部署到 Linux 服务器](#9-部署到-linux-服务器)
10. [验证安装](#10-验证安装)
11. [常见问题](#11-常见问题)

---

## 1. 前置要求

### 1.1 操作系统

| 平台 | 支持架构 | 备注 |
|------|---------|------|
| Linux | amd64, arm64 | 主要部署目标（Oracle 服务器） |
| macOS | amd64 (Intel), arm64 (M1/M2/M3) | 本地开发 |
| Windows | 不支持 | — |

### 1.2 软件依赖

**编译机（只需安装一次）**:

```bash
# 检查 Go 版本，需要 1.21 或更高版本
go version
```
> 输出示例: `go version go1.22.0 darwin/arm64`
> 如未安装，访问 https://go.dev/dl/ 下载安装。OpenDB 使用纯 Go 编写，**无需** Oracle Instant Client 或任何 C 库。

```bash
# 检查 Git 是否已安装
git --version
```
> 输出示例: `git version 2.43.0`

**目标运行机（Linux/Oracle 服务器）**:

- 无需安装任何额外依赖
- 无需 Oracle Instant Client
- 无需 Java / Python / Node.js
- 只需一个二进制文件即可运行

---

## 2. 获取源码

```bash
# 将 opendb 源码克隆到本地
# git clone 会创建 opendb/ 子目录，包含完整代码
git clone https://github.com/sqlrush/opendb.git
```
> 使用 HTTPS 协议，无需配置 SSH Key。如果网络受限，可先在有网络的机器上克隆，再传输到目标机器。

```bash
# 进入项目目录
cd opendb
```
> 后续所有编译命令都在这个目录下执行。

```bash
# 查看当前代码版本（可选）
git log --oneline -5
```
> 显示最近 5 条提交记录，确认代码是最新版本。

```bash
# 下载所有 Go 模块依赖
# 依赖会缓存到 $GOPATH/pkg/mod，下次编译不需要重新下载
go mod download
```
> OpenDB 的主要依赖：
> - `github.com/sijms/go-ora/v2` — 纯 Go 实现的 Oracle 驱动，无需 Oracle Client
> - `github.com/charmbracelet/bubbletea` — 终端 TUI 框架
> - `gopkg.in/yaml.v3` — 配置文件解析

```bash
# 验证依赖完整性（可选）
go mod verify
```
> 检查本地缓存的模块与 go.sum 记录的哈希一致，确保没有被篡改。

---

## 3. 编译

### 3.1 在当前机器上直接编译

```bash
# 编译 opendb 并输出到 bin/opendb
# VERSION=dev 表示开发版本号（生产环境替换为实际版本号）
make build
```
> 等价于: `go build -ldflags "..." -o bin/opendb ./cmd/opendb`
> 编译完成后，可执行文件位于 `bin/opendb`。

```bash
# 验证编译成功，输出版本信息
./bin/opendb --version
```
> 输出示例: `opendb v0.8.00 (commit: f5b9390, built: 2026-03-19T08:00:00Z)`

### 3.2 交叉编译（在 Mac 上编译 Linux 二进制）

```bash
# 交叉编译 Linux amd64 版本（用于部署到 x86_64 服务器）
# GOOS=linux 指定目标操作系统为 Linux
# GOARCH=amd64 指定目标架构为 64位 x86
# -o opendb-linux 输出文件名为 opendb-linux（与本机 opendb 区分）
GOOS=linux GOARCH=amd64 go build -o opendb-linux ./cmd/opendb/
```
> Go 天然支持交叉编译，无需额外工具链。编译出的 opendb-linux 是静态链接的 ELF 可执行文件，可直接在 Linux 上运行。

```bash
# 编译 Linux ARM64 版本（用于 ARM 服务器，如华为鲲鹏、AWS Graviton）
GOOS=linux GOARCH=arm64 go build -o opendb-linux-arm64 ./cmd/opendb/
```

```bash
# 一键编译所有平台（输出到 bin/ 目录）
make release
```
> 会同时生成: `opendb-linux-amd64`, `opendb-linux-arm64`, `opendb-darwin-amd64`, `opendb-darwin-arm64`

### 3.3 安装到系统 PATH（可选）

```bash
# 将 opendb 安装到 $GOPATH/bin，使其可以在任意目录执行
make install
```

```bash
# 或者手动复制到系统 PATH
sudo cp bin/opendb /usr/local/bin/opendb
```

```bash
# 验证安装到 PATH
which opendb
opendb --version
```

---

## 4. 目录结构初始化

OpenDB 的所有配置文件默认存放在 `~/.opendb/` 目录下。**首次运行时会自动创建**，也可以手动提前创建：

```bash
# 创建 opendb 配置主目录
mkdir -p ~/.opendb
```
> `~` 代表当前用户的 home 目录（Linux 通常是 `/home/oracle` 或 `/root`，macOS 是 `/Users/yourname`）。

```bash
# 创建连接配置目录（存放所有数据库连接的 YAML 文件）
mkdir -p ~/.opendb/connections
```
> 每个数据库环境（生产/测试/开发）对应一个 YAML 文件，放在这里。

```bash
# 创建命令历史目录（存放每个连接的命令历史）
mkdir -p ~/.opendb/history
```
> opendb 会为每个连接单独保存命令历史，方便回溯。

```bash
# 查看目录结构确认创建成功
ls -la ~/.opendb/
```
> 预期输出：
> ```
> drwxr-xr-x  connections/
> drwxr-xr-x  history/
> ```

---

## 5. 主配置文件

主配置文件路径: `~/.opendb/config.yaml`

```bash
# 创建主配置文件
# 这是最简配置，只需修改下面步骤中需要的部分
cat > ~/.opendb/config.yaml << 'EOF'
# OpenDB 主配置文件
# 未填写的参数使用内置默认值

# 连接配置目录（每个环境一个 YAML 文件）
connections_dir: ~/.opendb/connections

# 安全设置
security:
  default_level: 0              # 0=无限制  1=只读保护  2=危险操作须确认
  confirm_on_dangerous: true    # true: /kill /alter 等操作执行前弹出确认

# 输出格式
output:
  format: terminal              # terminal=彩色表格  json=JSON  csv=CSV
  max_rows: 1000                # 单次查询最大返回行数

# 会话管理
session:
  restore_on_switch: true       # 切换连接时保留会话变量
  history_dir: ~/.opendb/history

# AI 诊断引擎（如不使用 AI 功能，保持 provider: none）
llm:
  provider: none                # none=不启用  ollama=本地 Ollama
  model: "qwen3.5:9b"          # 推荐模型（需先用 Ollama 下载）
  base_url: "http://localhost:11434"

# 哨兵（后台实时异常检测）
sentinel:
  auto_start: true              # 登录后自动启动哨兵
  trigger_mode: adaptive        # adaptive=自适应3σ  fixed=固定阈值
  sigma: 3.0                    # 3σ ≈ 99.7% 置信区间，减小可提高灵敏度
  cooldown: 5m                  # 两次告警最小间隔，防止告警风暴
EOF
```
> `cat > file << 'EOF' ... EOF` 是 heredoc 语法，一次性写入多行内容到文件。
> **单引号 `'EOF'` 很重要**：防止 shell 展开 `$` 变量，保留原始文本。

```bash
# 确认配置文件内容正确
cat ~/.opendb/config.yaml
```

---

## 6. 数据库连接配置

每个数据库环境对应一个 YAML 文件，存放在 `~/.opendb/connections/` 目录中。

### 6.1 最简示例（密码交互输入）

```bash
# 创建测试环境连接配置
# 文件名随意，推荐使用环境名，如 dev.yaml / prod.yaml / test.yaml
cat > ~/.opendb/connections/test.yaml << 'EOF'
group: 测试环境
tags:
  - test

connections:
  - name: orcl-test             # 连接名，/login 时使用这个名字
    host: 127.0.0.1             # Oracle 服务器 IP 或 主机名
    port: 1521                  # Oracle 监听端口，默认 1521
    service: orcl               # Service Name（如果用 SID 请改用 sid: orcl）
    user: opendb_test           # 登录用户名
    credential:
      provider: prompt          # prompt=每次启动时提示输入密码
EOF
```
> `group` 和 `tags` 是组织标签，方便多环境管理，不影响功能。
> `name` 是连接别名，在 opendb 内用 `/login orcl-test` 登录。
> `provider: prompt` 表示每次连接时在终端提示输入密码（最安全，密码不落盘）。

### 6.2 保存密码（加密存储）

```bash
cat > ~/.opendb/connections/dev.yaml << 'EOF'
group: 开发环境
connections:
  - name: orcl-dev
    host: 192.168.1.100
    port: 1521
    service: orcldev
    user: system
    credential:
      provider: save            # save=首次连接时提示输入密码并加密保存
                                # 密码存储在 ~/.opendb/credentials/（AES 加密）
                                # 后续不再提示输入
EOF
```

### 6.3 多数据库环境（生产配置示例）

```bash
cat > ~/.opendb/connections/prod.yaml << 'EOF'
group: 生产环境
tags:
  - prod
  - critical

connections:
  # 主库
  - name: prod-primary
    host: 10.0.1.100
    port: 1521
    service: orclprd
    user: dbadmin
    credential:
      provider: prompt

  # 备库（Data Guard）
  - name: prod-standby
    host: 10.0.1.101
    port: 1521
    service: orclprd
    user: dbadmin
    credential:
      provider: prompt

  # SYSDBA 连接（应急使用）
  - name: prod-sysdba
    host: 10.0.1.100
    port: 1521
    service: orclprd
    user: sys
    privilege: sysdba           # 以 SYSDBA 权限连接
    credential:
      provider: prompt
EOF
```

### 6.4 使用 SID 连接（老版本 Oracle）

```bash
cat > ~/.opendb/connections/legacy.yaml << 'EOF'
group: 老系统
connections:
  - name: oracle9i
    host: 192.168.1.50
    port: 1521
    sid: ORCL                   # 用 SID 而不是 Service Name（Oracle 9i/10g 常见）
    user: scott
    credential:
      provider: prompt
EOF
```

### 6.5 验证连接文件格式

```bash
# 检查 YAML 语法是否正确（opendb 启动时也会检查）
# 如果输出没有报错，格式正确
python3 -c "import yaml; yaml.safe_load(open('$HOME/.opendb/connections/test.yaml'))" && echo "YAML 格式正确"
```
> 这一步可选，但能快速发现缩进错误、冒号缺失等常见 YAML 问题。

```bash
# 查看已创建的连接文件
ls -la ~/.opendb/connections/
```

---

## 7. 启动与基本使用

### 7.1 首次启动

```bash
# 启动 opendb 交互模式
opendb
```
> 首次启动会检测配置目录，若没有配置文件会启动引导向导。
> 配置好后每次运行 `opendb` 直接进入交互界面。

### 7.2 登录数据库

```
# 在 opendb 交互界面内输入（不是 shell 命令）：

/login orcl-test
```
> `orcl-test` 是在连接配置文件中设置的 `name` 字段。
> 如果 `credential.provider: prompt`，会提示输入密码。

```
# 使用连接字符串直接登录（适合临时连接）
/login opendb_test/MyPassword@127.0.0.1:1521/orcl
```
> 连接字符串格式: `user/password@host:port/service`
> 也支持: `user@host:port/service`（会提示密码）
> 也支持: `user/password@host/service`（默认端口 1521）

```
# SYSDBA 登录
/login sys/password@127.0.0.1:1521/orcl as sysdba
```

### 7.3 常用命令速查

登录后，在 opendb 界面中输入以下命令：

```
# 综合健康检查（24项指标）
/health

# 查看活跃会话
/activesessions

# 等待事件排名
/waits

# 慢 SQL Top 10
/slowsql

# 表空间使用情况
/space

# 实时监控（全屏 dashboard，按 q 退出）
/dbtop

# 查看所有可用命令
/help

# 退出 opendb
/logout
exit
```

### 7.4 批量模式（非交互，适合脚本）

```bash
# 格式: opendb -c <连接名> <命令>
# 运行一条命令后自动退出，适合 cron job 或 shell 脚本

# 查看表空间使用率
opendb -c orcl-test /space

# 输出为 JSON 格式（方便脚本解析）
opendb -c orcl-test /sql "SELECT tablespace_name, used_percent FROM dba_tablespace_usage_metrics"
```

---

## 8. AI 诊断（可选）

AI 诊断功能需要本地运行 [Ollama](https://ollama.com) 并下载指定模型。此步骤完全可选，不影响其他功能。

### 8.1 安装 Ollama

```bash
# macOS / Linux 一键安装脚本
curl -fsSL https://ollama.com/install.sh | sh
```
> 安装完成后 Ollama 默认监听 `http://localhost:11434`。

### 8.2 下载推荐模型

```bash
# 下载 Qwen3.5 9B 模型（推荐，<10B 级别综合能力最强）
# 模型大小约 5.5GB，需要一定下载时间
ollama pull qwen3.5:9b
```
> 9B 以下模型使用 GuidedStrategy（opendb 引导 LLM 逐步诊断）。
> 27B 以上模型使用 AutonomousStrategy（LLM 自主推理，更强但更慢）。

```bash
# 验证模型已下载
ollama list
```
> 应看到 `qwen3.5:9b` 在列表中。

### 8.3 启用 AI 诊断

编辑 `~/.opendb/config.yaml`，将 `llm` 部分修改为：

```bash
# 修改配置启用 Ollama
cat > ~/.opendb/config.yaml << 'EOF'
connections_dir: ~/.opendb/connections
security:
  confirm_on_dangerous: true
output:
  format: terminal
  max_rows: 1000
session:
  history_dir: ~/.opendb/history
llm:
  provider: ollama              # 改为 ollama
  model: "qwen3.5:9b"          # 与 ollama pull 下载的模型名一致
  base_url: "http://localhost:11434"
  diagnose_mode: auto           # auto=最多10轮自主推理
  max_rounds: 10
  max_result_tokens: 4000
sentinel:
  auto_start: true
  trigger_mode: adaptive
  sigma: 3.0
  cooldown: 5m
EOF
```

```bash
# 测试 AI 诊断（在 opendb 登录后输入）
/diag
```
> `/diag` 会自动收集数据库状态并调用 LLM 进行分析，输出根因判断和处理建议。

---

## 9. 部署到 Linux 服务器

### 9.1 在 Mac 上交叉编译

```bash
# 在 Mac 上进入 opendb 源码目录
cd ~/path/to/opendb

# 编译 Linux 64位版本
GOOS=linux GOARCH=amd64 go build -o opendb-linux ./cmd/opendb/
```
> 编译产物 `opendb-linux` 是一个完全静态链接的 ELF 二进制，大小约 20-30MB。

```bash
# 查看文件类型，确认是 Linux ELF 格式
file opendb-linux
```
> 输出应包含: `ELF 64-bit LSB executable, x86-64`

### 9.2 传输到服务器

```bash
# 使用 scp 将二进制传输到服务器
# 将 root@SERVER_IP 替换为实际服务器地址
scp opendb-linux root@8.160.176.23:/home/oracle/opendb
```
> 如果不能直接连接 root，先传到可访问用户的目录，再切换到 oracle 用户操作。

### 9.3 在服务器上安装

```bash
# SSH 登录到服务器
ssh root@8.160.176.23
```

```bash
# 停止正在运行的旧版本（如果有）
pkill opendb
```
> `pkill` 按进程名终止，如果没有运行中的进程会返回 "no process found"，这是正常的。

```bash
# 等待进程完全退出
sleep 1
```

```bash
# 将二进制复制到系统 PATH
cp /home/oracle/opendb /usr/local/bin/opendb
```
> `/usr/local/bin` 通常在所有用户的 PATH 中，oracle 用户直接输入 `opendb` 即可运行。

```bash
# 添加执行权限（如果没有）
chmod +x /usr/local/bin/opendb
```

```bash
# 切换到 oracle 用户（Oracle 数据库通常以此用户运行）
su - oracle
```

### 9.4 在服务器上初始化配置

```bash
# 以下命令在 oracle 用户下执行

# 创建配置目录
mkdir -p ~/.opendb/connections
mkdir -p ~/.opendb/history
```

```bash
# 创建主配置文件
cat > ~/.opendb/config.yaml << 'EOF'
connections_dir: ~/.opendb/connections
security:
  confirm_on_dangerous: true
output:
  format: terminal
  max_rows: 1000
session:
  history_dir: ~/.opendb/history
llm:
  provider: none
sentinel:
  auto_start: true
  trigger_mode: adaptive
  sigma: 3.0
  cooldown: 5m
EOF
```

```bash
# 创建本地连接配置（Oracle 在同一台机器，使用本地 IPC 或 TCP）
cat > ~/.opendb/connections/local.yaml << 'EOF'
group: 本地数据库
connections:
  - name: orcl
    host: 127.0.0.1
    port: 1521
    service: orcl               # 替换为实际 Service Name
    user: system                # 替换为实际用户名
    credential:
      provider: prompt
EOF
```

### 9.5 在服务器上验证

```bash
# 验证版本
opendb --version
```
> 输出示例: `opendb v0.8.00 (commit: f5b9390, built: 2026-03-19T08:00:00Z)`

```bash
# 启动 opendb
opendb
```
> 进入交互界面后，输入 `/login orcl` 并输入密码，验证数据库连接是否正常。

---

## 10. 验证安装

安装完成后，按以下步骤逐项验证：

```bash
# 1. 版本确认
opendb --version
```
> ✓ 应输出版本号，如 `opendb v0.8.00`

```
# 2. 在 opendb 中登录数据库
/login <连接名>
```
> ✓ 应看到 "Connected to <数据库信息>" 的提示

```
# 3. 基本连通性测试
/sql select 1 from dual
```
> ✓ 应返回一行结果: `1`

```
# 4. 健康检查
/health
```
> ✓ 应看到 24 项健康检查结果表格

```
# 5. 哨兵状态
/sentinel
```
> ✓ 应看到 "Sentinel: WATCHING" 状态（需等待约 10 秒采集基线）

```
# 6. 帮助系统
/help
```
> ✓ 应看到所有可用命令列表

---

## 11. 常见问题

### Q: `go: command not found`

```bash
# 检查 Go 是否在 PATH 中
echo $PATH
ls /usr/local/go/bin/go
```
> 如果 Go 已安装但不在 PATH，将以下内容添加到 `~/.bash_profile` 或 `~/.zshrc`:
> ```bash
> export PATH=$PATH:/usr/local/go/bin
> ```
> 然后执行 `source ~/.bash_profile` 使其生效。

---

### Q: 连接时报 `ORA-12541: TNS:no listener`

```bash
# 检查 Oracle 监听器是否运行（在 Oracle 服务器上执行）
lsnrctl status
```
> 如果监听器未运行: `lsnrctl start`
> 确认连接配置中的 `host`、`port`、`service` 与 `tnsnames.ora` 一致。

---

### Q: 连接时报 `ORA-01017: invalid username/password`

> 检查连接配置中的 `user` 字段，以及输入的密码是否正确。
> Oracle 密码区分大小写（12c 及以上版本）。

---

### Q: 哨兵一直触发告警

```bash
# 调高 sigma 阈值降低灵敏度（编辑 config.yaml）
# sigma: 3.0 → 4.0  （4σ 更严格，减少误报）
```
> 也可以增加 `cooldown` 时间，或切换到 `trigger_mode: fixed` 使用固定阈值。

---

### Q: AI 诊断无法连接 Ollama

```bash
# 检查 Ollama 服务是否运行
curl http://localhost:11434/api/tags
```
> 如果返回 JSON 则服务正常。如果报错，用 `ollama serve` 启动服务。
> 如果 opendb 和 Ollama 不在同一台机器，修改 `config.yaml` 中的 `base_url`。

---

### Q: 修改配置后不生效

```bash
# opendb 每次启动时重新读取配置，重启即可
# 确认修改的是正确的配置文件路径
cat ~/.opendb/config.yaml

# 或者用环境变量指定配置文件路径
OPENDB_CONFIG=/path/to/custom-config.yaml opendb
```

---

## 附录：完整目录结构

安装完成后，`~/.opendb/` 的完整结构如下：

```
~/.opendb/
├── config.yaml                 # 主配置文件
├── connections/                # 数据库连接配置目录
│   ├── prod.yaml               # 生产环境连接
│   ├── dev.yaml                # 开发环境连接
│   └── test.yaml               # 测试环境连接
├── credentials/                # 加密密码存储（自动创建，provider: save 时使用）
│   └── *.enc                   # AES 加密的凭证文件
└── history/                    # 命令历史（自动创建）
    └── orcl-test.history       # 每个连接单独的历史文件
```

---

*文档更新时间: 2026-03-19 | OpenDB v0.8.00*
