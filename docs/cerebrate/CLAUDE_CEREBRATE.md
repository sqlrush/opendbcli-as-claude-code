# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目概述

Cerebrate 是 OpenDB 的大规模集群版本——一个 L4 级 7×24 小时全自动数据库运维平台。基于 OpenDB（~/opendb）现有能力开发，所有代码整合在 OpenDB 单二进制中，通过 `--role worker/memory/manager` 参数区分角色。

## 三层 Agent 架构（星际争霸虫族命名）

```
Manager Agent (Cerebrate/脑虫) — 全局编排，跨区域趋势，策略下发，Web 大盘
  └── Memory Agent (Overlord/王虫) — 区域协调（≤200节点），跨节点操作，持有 Worker 全部记忆和连接串
       └── Worker Agent (Drone/工蜂) — 单节点自治，Sentinel 监控，LLM 诊断，自主修复
```

**代码命名必须使用开发代号**：包名 `cerebrate/`、`overlord/`、`drone/`，结构体 `CerebrateServer`、`OverlordCoordinator`、`DroneAgent`。文档中使用正式名称（Manager Agent / Memory Agent / Worker Agent）。

## 核心设计原则

- **向上兜底**：Worker 只处理单节点问题，拿不准的一律上交 Overlord。Overlord 持有所有 Worker 的 memory/policy/连接串，是完整的 Agent，可直接操作任何数据库。跨节点问题（主从切换、RAC 维护等）由 Overlord 直连数据库在一个 LLM 会话内完成，天然串行，不需要分布式锁。
- **LLM 优先，Rule Engine 兜底**：Sentinel 异常 → 先调 LLM → LLM 不可用时降级到 273+ Rule Engine。
- **脑裂仲裁**：人 > 上级 LLM > 本地 LLM。用户在 Worker 上发起的操作优先级最高。
- **崩溃恢复即首次诊断**：Worker 重启后扫描记忆 + 数据库日志，LLM 判断是否需要恢复。能恢复就恢复，否则放弃等问题再出现。零额外基础设施。
- **管住手，不堵嘴**：安全控制只针对数据库变更操作（4 级：enabled/confirm/disabled/hidden），不限制记忆同步、上报通信、报告生成、只读查询。

## 通信与协议

- 三层间通信：gRPC 双向流（混合推拉）
- CLI ↔ 本机 Daemon：gRPC over Unix Socket
- CLI ↔ 远程 Agent：gRPC over TCP
- 一套 proto 定义，一套服务接口，两种传输层

## 状态管理

- memory/policy：复用 OpenDB Engine V2 现有机制（`~/.opendb/memory/`、`~/.opendb/policies/`），每分钟增量同步到 Overlord
- Rule（经验沉淀）：修复成功后自动生成 rule 文件（`~/.opendb/rules/`），frontmatter trigger 自动构建内存索引，按异常指标匹配加载
- 审计日志：写操作 append-only 记录到 `~/.opendb/audit.log`，随 memory 同步到 Overlord
- 崩溃丢失策略：未保存的默认丢失，不做复杂恢复

## 高可用

- Overlord：至少 3 个，每个将记忆复制给 2 个其他 Overlord。宕机后由 Cerebrate 指定接管者
- Cerebrate：是特殊的 Overlord，2 个 Overlord 持有其全量数据备份。宕机后备份 Overlord 升级为 Cerebrate（双角色共存）
- Cerebrate 只存映射关系（Worker↔Overlord 归属、记忆备份关系），不存实际 memory/policy 数据

## 部署与集群管理

```bash
# 初始化集群
opendb cluster init --role manager --listen 0.0.0.0:9100

# 加入 Overlord
opendb cluster join --role memory --cerebrate <addr> --token <token> --region <name>

# 加入 Worker
opendb cluster join --role node --overlord <addr> --token <token>

# 启动 Agent
opendb agent start --role worker --overlord <addr> --db-type oracle --db-conn "..."
opendb agent start --role memory --cerebrate <addr> --region china-east
opendb agent start --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080
```

V1 首次部署三种方式：Pull 安装（不开 22）、适配 Ansible（开 22）、内置 `opendb cluster deploy`（开 22）。后续升级走 gRPC 层级分发。

## 新增开发工作量

基于 OpenDB 90+ 现有 skill，集群版新增：

**基础设施**：Daemon 模式（Autonomy Loop）、gRPC Server/Client、集群管理（cluster init/join/deploy/upgrade）

**Worker 新增 1 个 Tool**：`escalate_to_overlord`

**Overlord 新增 7 个 Tool**：`get_worker_status`、`get_worker_memory`、`get_region_topology`、`broadcast_command`、`coordinate_failover`、`generate_region_report`、`escalate_to_cerebrate`

**Cerebrate 新增 6 个 Tool**：`get_all_overlords`、`get_global_topology`、`push_policy`、`schedule_maintenance`、`generate_fleet_report`、`manage_cluster`

**Web UI**：Cerebrate 监控大盘（全局拓扑 + 健康状态 + 报告下钻）

## 测试服务器部署

**禁止 SCP 传输二进制到测试服务器。** 在测试服务器上直接 git pull + 编译：

```bash
# 测试服务器: ssh -p 2222 root@47.251.30.180
# 源码: /opt/opendb-src (feature/cerebrate 分支)
# Go: /usr/local/go/bin/go

cd /opt/opendb-src
git pull
go build -tags full -o /opt/cerebrate-test/opendb ./cmd/opendb/
cd /opt/cerebrate-test && docker build --no-cache -t opendb:cerebrate .
```

原因：SCP 传 58MB 二进制经常断连导致文件损坏。服务器上编译更快更可靠。

## 编译与测试

继承 OpenDB 的编译方式，必须用 `-tags full`：

```bash
go build -tags full -o opendb ./cmd/opendb/
go test -tags full -race ./...
golangci-lint run
```

## 关键设计文档

- `docs/design/project_opendb_autopilot_brainstorm.md` — 原始架构设计（Q1-Q9）
- `docs/design/project_opendb_autopilot_24h_scenario.md` — 7×24 场景推演（8 个核心场景）
- `docs/design/project_opendb_autopilot_qa_log.md` — Q1-Q28 完整架构决策记录
- `docs/design/project_opendb_autopilot_gap_analysis.md` — 差距分析与完成状态

## 变更纪律（必须遵守）

**不得擅自修改已确定的设计方案。** 所有架构决策（Q1-Q29）已经过充分讨论并记录在 `docs/cerebrate/project_opendb_autopilot_qa_log.md` 中。如需修改任何已定方案，必须先与用户沟通确认后再动代码。

包括但不限于：
- 三层架构设计（Manager/Memory/Worker）
- 通信协议（gRPC 双向流）
- 注册流程（cluster init/join + Join Token）
- 命名体系（Cerebrate/Overlord/Drone）
- 安全控制（4 级 Tool 控制）
- 崩溃恢复策略（首次诊断即恢复）
- 向上兜底原则（Worker 拿不准上交 Overlord）

**遇到实现困难时，在现有设计框架内寻找解决方案，而不是绕过或修改设计。**

## 版本策略

- 新版必须兼容旧版（硬性策略），滚动升级过程中新旧版本可共存
- 回滚顺序：Worker → Overlord → Cerebrate
- 配置热更新：Cerebrate 是权威源，gRPC 层级推送，内存原子替换，不停服

## 无人值守 CI/CD 流水线

本项目采用 Anthropic Managed Agents（RemoteTrigger）实现 7×24 无人值守自动迭代：设定目标后，由 Opus 在云端自动运行，持续开发、测试、修复，直至达到目标要求。不需要开着电脑。

### 自动化技术栈

| 技术 | 用途 | 说明 |
|------|------|------|
| **RemoteTrigger**（首选） | 7×24 无人值守迭代 | Anthropic 云端托管 Agent，按 cron 调度，不依赖本地电脑 |
| **CronCreate** | 本地会话内定期任务 | REPL 空闲时触发，7 天过期，适合开发期短期任务 |
| **`claude -p`** | 单次 CI 任务 | Headless 模式，适合 shell 脚本和外部 CI 系统调用 |

### RemoteTrigger 流水线（云端托管，推荐）

通过 Anthropic 的 RemoteTrigger API 创建定时触发的托管 Agent：

```
每 30 分钟自动触发：

  读取 .pipeline/goals.md（当前迭代目标）
    │
    ├── 目标未达成 → 拆解任务 → 编写代码 → 构建 → 测试
    │                              │
    │                         失败 → 自动修复 → 重试（最多 3 次）
    │                         通过 → 提交 → 继续下一个任务
    │
    └── 目标已达成 → 生成完成报告 → 等待新目标
```

创建方式：
```
# 使用 RemoteTrigger 创建定时流水线（云端运行，无需本地电脑）
RemoteTrigger create:
  cron: "7 * * * *"  # 每小时触发
  prompt: "读取 .pipeline/goals.md，执行当前迭代目标..."

# 或使用本地 CronCreate（需保持 REPL 运行，7 天过期）
CronCreate:
  cron: "*/30 * * * *"
  prompt: "执行流水线..."
```

### 流水线循环

```
Build（构建）→ Test（单元+LLM回放）→ Chaos（故障注入）→ Report（报告）
     │              │                    │                  │
  失败→自动修复   失败→自动修复      失败→记录issue      生成报告
     → 重试          → 重试           → 尝试修复        → 循环继续
```

### 运作方式

1. **设定目标**：用户在 `.pipeline/goals.md` 中描述当前迭代的目标（如"实现 Daemon 模式"、"实现 gRPC 通信层"）
2. **Opus 自动执行**：RemoteTrigger 按 cron 触发 → 读取目标 → 拆解任务 → 编写代码 → 构建 → 测试 → 失败则自动修复 → 通过则提交 → 继续下一个任务
3. **持续循环**：每轮完成后检查目标是否达成，未达成则继续迭代，已达成则生成完成报告等待新目标
4. **用户只需**：设定目标，查看报告。云端 RemoteTrigger 模式下无需开电脑

### 三层测试

- **故障注入**：内置 `/inject` 命令在测试环境制造数据库故障（temp_full、lock_contention、slow_sql、replication_lag 等），验证 Worker 自治能力。生产环境中 `/inject` 设为 hidden
- **LLM 录制/回放**：`--llm-record` 录制真实诊断过程，`--llm-replay` 回放用于 CI/CD（零 LLM 成本，确定性测试）
- **集群混沌测试**：`opendb cluster test --scenario` 验证 Worker 崩溃接管、Overlord 故障转移、网络分区降级、LLM 不可用降级等场景

### 流水线产出物

```
.pipeline/
├── goals.md             # 当前迭代目标（用户设定）
├── config.yaml          # 流水线配置
├── reports/             # 每轮报告
├── issues/              # 自动发现的问题
├── fixes/               # 自动修复记录
└── sessions/            # LLM 回放录制
```

### 演进阶段

1. **有代码后立即启用**：Build + Unit Test + Lint 自动修复循环
2. **录制首批 LLM session 后**：加入 LLM 回放测试
3. **集群功能完成后**：加入混沌测试场景
4. **稳定后**：加入性能回归测试
