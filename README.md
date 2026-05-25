<p align="center">
  <a href="./README.md">English</a> | <a href="./README_zh.md">简体中文</a>
</p>

<p align="center">
  <pre align="center">
 ██████╗ ██████╗ ███████╗███╗   ██╗██████╗ ██████╗
██╔═══██╗██╔══██╗██╔════╝████╗  ██║██╔══██╗██╔══██╗
██║   ██║██████╔╝█████╗  ██╔██╗ ██║██║  ██║██████╔╝
██║   ██║██╔═══╝ ██╔══╝  ██║╚██╗██║██║  ██║██╔══██╗
╚██████╔╝██║     ███████╗██║ ╚████║██████╔╝██████╔╝
 ╚═════╝ ╚═╝     ╚══════╝╚═╝  ╚═══╝╚═════╝ ╚═════╝
  </pre>
  <strong>L4 级 7x24 全自动数据库运维平台</strong><br>
  <em>DB CLI Agent + 三层自治集群 -- 监控、诊断、出方案，人只需要看报告。</em>
</p>

<p align="center">
  <a href="https://github.com/sqlrush/opendb/releases"><img alt="Release" src="https://img.shields.io/github/v/release/sqlrush/opendb?style=flat-square&color=blue"></a>
  <a href="https://github.com/sqlrush/opendb/blob/main/LICENSE"><img alt="License" src="https://img.shields.io/badge/license-Apache%202.0-green?style=flat-square"></a>
  <img alt="Platform" src="https://img.shields.io/badge/platform-linux%20%7C%20macOS-lightgrey?style=flat-square">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go&logoColor=white">
</p>

---

## 项目概述

OpenDB 是一个数据库专用的 CLI Agent 和 L4 级全自动运维平台。两种运行模式统一在一个二进制中：

- **交互模式** -- 类 Claude Code 的智能 CLI，斜杠命令 + SQL + 自然语言三种输入无缝切换
- **Autopilot 模式** -- 三层 Agent 集群，7x24 小时自治运行，Sentinel 实时监控 + LLM 诊断 + 故障报告

**核心策略：LLM 只做只读查询，所有变更操作以修复方案形式写入报告，由用户手动执行。**

### 支持的数据库

| 数据库 | 版本 | 状态 |
|--------|------|------|
| Oracle | 11g / 12c / 19c / 21c / 23ai | 生产就绪 |
| MySQL | 5.7 / 8.0+ | 生产就绪 |
| PostgreSQL | 12+ | 生产就绪 |
| OpenGauss | 3.0+ | 生产就绪 |

---

## 架构

### 三层 Agent 架构

```
                    ┌─────────────────────────────────────────┐
                    │     Cerebrate (Manager Agent / 脑虫)      │
                    │                                         │
                    │  - 全局编排，跨区域趋势分析                │
                    │  - 策略下发（HotConfig 热更新）            │
                    │  - Web 大盘（全局拓扑 + 报告下钻）         │
                    │  - HA：2 个备份 Overlord 持有全量数据      │
                    │  - 端口: 9100 (gRPC) + 8080 (Web)        │
                    └───────────┬─────────────┬───────────────┘
                                │             │
                    ┌───────────▼──┐   ┌──────▼───────────┐
                    │  Overlord-1   │   │  Overlord-2       │
                    │ (Memory Agent │   │ (Memory Agent     │
                    │  / 王虫)      │   │  / 王虫)          │
                    │              │   │                   │
                    │ 区域协调      │   │ 区域协调           │
                    │ <=200 节点   │   │ <=200 节点        │
                    │ 关联分析      │   │ 关联分析           │
                    │ 直连数据库    │   │ 直连数据库         │
                    │ 端口: 9200   │   │ 端口: 9200        │
                    └──┬──┬──┬────┘   └──┬──┬──┬──────────┘
                       │  │  │           │  │  │
                    ┌──▼┐┌▼┐┌▼──┐     ┌──▼┐┌▼┐┌▼──┐
                    │W-1││W2││W-3│     │W-4││W5││W-6│
                    └───┘└──┘└───┘     └───┘└──┘└───┘
                    Drone (Worker Agent / 工蜂)
                    - 单节点自治
                    - Sentinel 实时监控 (48 指标)
                    - LLM 诊断 / Rule Engine 降级
                    - 故障分析报告生成
                    - 端口: 9300
```

### 通信方式

| 路径 | 协议 | 说明 |
|------|------|------|
| Agent 三层间 | gRPC 双向流 | 混合推拉，心跳 + 事件 |
| CLI 到本机 Daemon | gRPC over Unix Socket | 低延迟本地通信 |
| CLI 到远程 Agent | gRPC over TCP | 跨节点管理 |
| Web 大盘 | HTTP REST API | 浏览器访问 |

---

## 核心功能

### 1. Sentinel 实时监控

48 个指标 x 9 种检测策略，自适应 3-sigma 基线，异常时自动触发突发采集（Burst）。

### 2. LLM 诊断引擎

多轮 Function Calling，最多 20 轮链式推理，自动收集证据直达根因。LLM 不可用时降级到 273+ 条 Rule Engine。

### 3. 故障分析报告

类 Oracle AWR/ADDM 风格的专业报告，包含：
- 性能指标快照（触发时段 vs 基线对比）
- 根因分析（LLM 每轮查询和结论）
- 修复方案（P0 紧急止血 / P1 根因修复 / P2 长期优化）
- 可直接执行的 SQL + 验证方法

### 4. Rule Engine

273+ 条规则，5 阶段管道，毫秒级确定性诊断，离线无 LLM 也能用。修复成功后自动沉淀新规则。

### 5. Web 大盘

Cerebrate 提供全局监控大盘：
- 全局拓扑视图（Overlord / Worker 关系）
- 实时健康状态
- 故障时间线
- 报告下钻（区域 -> Worker -> 详情）

### 6. 安全控制

- **执行模式**：当前版本 `report_only`，LLM 只读查询，变更 SQL 全部放入报告
- **Tool 4 级控制**：enabled / confirm / disabled / hidden
- **审计日志**：append-only，HMAC-SHA256 链式哈希，防篡改

---

## 快速开始

### 编译

```bash
git clone https://github.com/sqlrush/opendb.git
cd opendb
go build -tags full -o opendb ./cmd/opendb/
```

> 必须使用 `-tags full` 编译，否则缺少数据库驱动。

### 验证

```bash
./opendb --version
# opendb v1.0.0 (commit: xxx, built: xxx)
```

### 交互模式

```bash
./opendb              # 进入交互式 CLI
./opendb setup        # 首次配置向导
./opendb configure    # 重新配置
```

### Autopilot 模式 -- 集群初始化

```bash
# 1. 初始化 Cerebrate（管理节点）
opendb cluster init --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080

# 2. 加入 Overlord（区域协调节点）
opendb cluster join --role memory \
  --cerebrate 192.168.1.10:9100 \
  --token <token> \
  --region china-east

# 3. 加入 Worker（工作节点）
opendb cluster join --role worker \
  --overlord 192.168.1.20:9200 \
  --token <token>
```

### 启动 Agent

```bash
# Worker（工蜂）
opendb agent start --role worker

# Overlord（王虫）
opendb agent start --role memory

# Cerebrate（脑虫）
opendb agent start --role manager --web 0.0.0.0:8080
```

### 查看集群状态

```bash
opendb cluster status
```

---

## 配置

### 主配置文件

`~/.opendb/config.yaml` -- Agent 和交互模式共用配置。

```yaml
security:
  default_level: 0
  confirm_on_dangerous: true

llm:
  provider: ollama
  model: "qwen3.5:27b"
  base_url: "http://localhost:11434"
  diagnose_mode: auto

sentinel:
  auto_start: true
  probe_interval: 1s
  trigger_mode: adaptive
  sigma: 3.0
```

### 集群配置

`~/.opendb/cluster/config.yaml` -- `cluster init/join` 自动生成。

```yaml
role: worker
node_id: drone-hostname-a1b2c3d4
listen: 0.0.0.0:9300
overlord: 192.168.1.20:9200
initialized: true
```

### 热更新配置

`~/.opendb/cluster/hotconfig.yaml` -- Cerebrate 推送，内存原子替换。

```yaml
blacklist:
  - "DROP TABLE"
  - "TRUNCATE"
llm_model: "qwen3.5:27b"
sentinel_sigma: 3.0
tool_controls:
  inject: hidden        # 生产环境隐藏故障注入
  kill_session: confirm  # 杀会话需确认
```

### 部署清单

`inventory.yaml` -- SSH 批量部署使用。

```yaml
ssh_user: opendb
ssh_key: ~/.ssh/id_rsa
cerebrate:
  host: 192.168.1.10
  name: cerebrate-main
  web: "0.0.0.0:8080"
overlords:
  - host: 192.168.1.20
    name: overlord-east
    region: china-east
drones:
  - host: 192.168.1.100
    name: worker-oracle-01
    overlord: 192.168.1.20:9200
    db_type: oracle
```

---

## CLI 命令概览

### Agent 管理

| 命令 | 说明 |
|------|------|
| `opendb agent start --role <worker\|memory\|manager>` | 启动 Agent 守护进程 |
| `opendb agent stop` | 停止当前 Agent |
| `opendb agent status` | 查看 Agent 运行状态 |

### 集群管理

| 命令 | 说明 |
|------|------|
| `opendb cluster init --role manager` | 初始化集群（生成 Join Token） |
| `opendb cluster join --role <memory\|worker>` | 加入已有集群 |
| `opendb cluster status` | 查看本节点集群状态 |
| `opendb cluster deploy --inventory <file>` | SSH 批量部署 |
| `opendb cluster test --scenario <name>` | 混沌测试 |

### 交互式命令

| 命令 | 说明 |
|------|------|
| `/health` | 综合健康巡检（20+ 项目） |
| `/dbtop` | 实时监控面板（类 Linux top） |
| `/sentinel` | 启动后台异常检测 |
| `/rule` | Rule Engine 即时诊断 |
| `/llm` | LLM 深度诊断 |
| `/slowsql [ms]` | 慢 SQL 查询 |
| `/sessions` | 查看数据库会话 |
| `/locks` | 查看锁等待 |
| `/space` | 表空间使用情况 |
| `/kill <SID>` | 终止会话（需确认） |

### 其他

| 命令 | 说明 |
|------|------|
| `opendb --version` | 显示版本信息 |
| `opendb setup` | 首次配置向导 |
| `opendb configure` | 重新配置 |
| `opendb -c <conn> <cmd>` | 批处理模式 |

---

## 数据目录

```
~/.opendb/
├── config.yaml            # 主配置
├── connections/            # 数据库连接配置
├── cluster/                # 集群配置
│   ├── config.yaml         # 节点角色和地址
│   ├── hotconfig.yaml      # 热更新配置
│   └── token.yaml          # Join Token
├── memory/                 # Agent 记忆（自动同步）
├── policies/               # 运维策略
├── rules/                  # Rule Engine 规则（自动沉淀）
├── reports/                # 故障分析报告
│   └── {instance}/         # 按实例分目录
│       └── {timestamp}_fault.md
├── audit.log               # 审计日志（防篡改）
├── audit.key               # 审计 HMAC 密钥
├── history/                # 命令历史
├── models/                 # LLM 模型配置
└── scheduler/              # 定时任务数据
```

---

## 设计文档

| 文档 | 说明 |
|------|------|
| [架构设计](docs/cerebrate/project_opendb_autopilot_brainstorm.md) | 原始 Q1-Q9 架构讨论 |
| [7x24 场景推演](docs/cerebrate/project_opendb_autopilot_24h_scenario.md) | 8 个核心运维场景 |
| [架构决策记录](docs/cerebrate/project_opendb_autopilot_qa_log.md) | Q1-Q28 完整决策 |
| [差距分析](docs/cerebrate/project_opendb_autopilot_gap_analysis.md) | 能力差距与完成状态 |
| [故障报告设计](docs/design/project_opendb_fault_report_design.md) | 报告系统详细设计 |
| [执行模式设计](docs/design/project_opendb_exec_mode_design.md) | 写操作控制三级模式 |
| [用户指南](docs/user-guide.md) | CLI 命令详细参考 |
| [部署指南](docs/deployment-guide.md) | 部署、升级、回滚 |

---

## 测试

```bash
# 单元测试
go test -tags full -race ./...

# Lint
golangci-lint run

# 混沌测试（需运行中的集群）
opendb cluster test --scenario worker_crash
opendb cluster test --scenario overlord_crash
opendb cluster test --scenario cerebrate_crash
opendb cluster test --scenario network_partition
opendb cluster test --scenario llm_unavailable
opendb cluster test --scenario storm
```

---

## License

Apache License 2.0 - 详见 [LICENSE](LICENSE) 文件。
