# OpenDB 用户指南

本文档覆盖 OpenDB 的所有 CLI 命令、Web API、配置参考和故障报告格式。

---

## 目录

- [Agent 命令](#agent-命令)
- [集群命令](#集群命令)
- [交互式命令](#交互式命令)
- [批处理模式](#批处理模式)
- [Web 大盘 API](#web-大盘-api)
- [配置参考](#配置参考)
- [故障报告格式](#故障报告格式)
- [LLM 配置](#llm-配置)

---

## Agent 命令

Agent 命令用于管理 OpenDB 守护进程。三种角色共用同一个二进制，通过 `--role` 区分。

### opendb agent start

启动 Agent 守护进程。

```
opendb agent start --role <worker|memory|manager> [flags]
```

**参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--role` | `worker` | Agent 角色：`worker`（工蜂）、`memory`（王虫）、`manager`（脑虫） |
| `--listen` | `0.0.0.0:9300` | gRPC 监听地址。Worker 默认 9300，Overlord 建议 9200，Cerebrate 建议 9100 |
| `--overlord` | （无） | Overlord 地址，Worker 角色必填 |
| `--web` | （无） | Web 大盘监听地址，仅 Manager 角色有效 |
| `--db-type` | （无） | 数据库类型：`oracle`、`mysql`、`postgres`、`opengauss` |
| `--db-conn` | （无） | 数据库连接字符串 |
| `--llm-record` | （无） | LLM 会话录制目录（CI/CD 用） |
| `--llm-replay` | （无） | LLM 会话回放文件（CI/CD 用） |

**示例：**

```bash
# 启动 Worker，连接到 Overlord
opendb agent start --role worker --overlord 192.168.1.20:9200

# 启动 Worker，指定数据库连接
opendb agent start --role worker \
  --overlord 192.168.1.20:9200 \
  --db-type oracle \
  --db-conn "system/password@192.168.1.100:1521/ORCL"

# 启动 Overlord
opendb agent start --role memory --listen 0.0.0.0:9200

# 启动 Cerebrate + Web 大盘
opendb agent start --role manager \
  --listen 0.0.0.0:9100 \
  --web 0.0.0.0:8080

# CI/CD：录制 LLM 会话
opendb agent start --role worker \
  --overlord 192.168.1.20:9200 \
  --llm-record /tmp/llm-sessions/

# CI/CD：回放 LLM 会话（零 LLM 成本）
opendb agent start --role worker \
  --overlord 192.168.1.20:9200 \
  --llm-replay /tmp/llm-sessions/session_001.jsonl
```

**行为说明：**

- 启动时写入 PID 文件 `~/.opendb/agent-{role}.pid`
- 如已有同角色 Agent 运行，返回错误
- 启动后自动进入 Autonomy Loop（sense -> diagnose -> act 循环）
- Worker 自动连接 `~/.opendb/connections/` 下配置的数据库
- 数据库连接失败时退化为心跳模式（仍保持与 Overlord 通信）
- 参数优先级：命令行 > `~/.opendb/cluster/config.yaml` > 默认值

### opendb agent stop

停止当前节点的 Agent 守护进程。

```
opendb agent stop
```

通过读取 PID 文件发送 SIGTERM 信号，Agent 收到后执行优雅关闭：
1. 停止 Sentinel 监控
2. 完成当前诊断（如有）
3. 断开 gRPC 连接
4. 关闭审计日志
5. 删除 PID 文件

### opendb agent status

查看当前节点的 Agent 运行状态。

```
opendb agent status
```

**输出示例：**

```
Agent: running (PID 12345)
  Role:     worker
  Uptime:   2h 15m
  Database: orcl (Oracle 19c)
  Overlord: 192.168.1.20:9200 (connected)
```

---

## 集群命令

### opendb cluster init

初始化一个新集群。在 Cerebrate 节点上执行。

```
opendb cluster init --role manager [flags]
```

**参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--role` | （必填） | 必须为 `manager` |
| `--listen` | `0.0.0.0:9100` | gRPC 监听地址 |
| `--web` | （无） | Web 大盘监听地址 |

**示例：**

```bash
opendb cluster init --role manager --listen 0.0.0.0:9100 --web 0.0.0.0:8080
```

**输出：**

```
Cerebrate initialized successfully.
  Node ID:    cerebrate-hostname-a1b2c3d4
  Listen:     0.0.0.0:9100
  Web UI:     0.0.0.0:8080
  Join token: e3f8a2b1c9d0... (expires in 24h)

Add Overlord:
  opendb cluster join --role memory --cerebrate <this_addr>:9100 --token e3f8a2b1c9d0... --region <region>

Add Worker:
  opendb cluster join --role worker --overlord <overlord_addr> --token e3f8a2b1c9d0...
```

**行为说明：**

- 生成唯一 Node ID（格式：`cerebrate-{hostname}-{random}`）
- 生成 Join Token（24 小时有效）
- 保存到 `~/.opendb/cluster/config.yaml` 和 `~/.opendb/cluster/token.yaml`
- 如果已初始化，返回错误

### opendb cluster join

加入已有集群。在 Overlord 或 Worker 节点上执行。

```
opendb cluster join --role <memory|worker> [flags]
```

**参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--role` | （必填） | `memory`（Overlord）或 `worker`（Drone） |
| `--cerebrate` | （无） | Cerebrate 地址，`memory` 角色必填 |
| `--overlord` | （无） | Overlord 地址，`worker` 角色必填 |
| `--token` | （必填） | Join Token |
| `--region` | （无） | 区域名，`memory` 角色必填 |
| `--listen` | `0.0.0.0:9100` | 本节点 gRPC 监听地址 |

**示例：**

```bash
# 加入为 Overlord
opendb cluster join --role memory \
  --cerebrate 192.168.1.10:9100 \
  --token e3f8a2b1c9d0... \
  --region china-east \
  --listen 0.0.0.0:9200

# 加入为 Worker
opendb cluster join --role worker \
  --overlord 192.168.1.20:9200 \
  --token e3f8a2b1c9d0... \
  --listen 0.0.0.0:9300
```

**输出（Worker 示例）：**

```
Joined cluster as Worker Agent (Drone).
  Node ID:  drone-hostname-c5d6e7f8
  Overlord: 192.168.1.20:9200
  Listen:   0.0.0.0:9300

Start the agent:
  opendb agent start --role worker
```

### opendb cluster status

查看本节点的集群配置状态。

```
opendb cluster status
```

**输出示例（Cerebrate）：**

```
Cluster: initialized
  Role:    manager
  Node ID: cerebrate-hostname-a1b2c3d4
  Listen:  0.0.0.0:9100
  Web UI:  0.0.0.0:8080
  Token:   e3f8a2b1c9d0... (expires 2026-04-12 10:30)
```

**输出示例（Worker）：**

```
Cluster: initialized
  Role:    worker
  Node ID: drone-hostname-c5d6e7f8
  Listen:  0.0.0.0:9300
  Overlord: 192.168.1.20:9200
```

### opendb cluster deploy

通过 SSH 批量部署集群。

```
opendb cluster deploy --inventory <file> --binary <path> [flags]
```

**参数：**

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--inventory` | （必填） | 部署清单 YAML 文件路径 |
| `--binary` | （必填） | 编译好的 opendb 二进制路径 |
| `--batch-size` | `50` | 每批并发 SSH 连接数 |
| `--remote-path` | `/usr/local/bin/opendb` | 远端安装路径 |

**部署清单格式 (`inventory.yaml`)：**

```yaml
ssh_user: opendb
ssh_key: ~/.ssh/id_rsa

cerebrate:
  host: 192.168.1.10
  name: cerebrate-main
  listen: "0.0.0.0:9100"
  web: "0.0.0.0:8080"

overlords:
  - host: 192.168.1.20
    name: overlord-east
    region: china-east
    listen: "0.0.0.0:9200"
  - host: 192.168.1.21
    name: overlord-west
    region: china-west
    listen: "0.0.0.0:9200"

drones:
  - host: 192.168.1.100
    name: worker-oracle-01
    overlord: 192.168.1.20:9200
    db_type: oracle
    db_conn: "system/pass@localhost:1521/ORCL"
  - host: 192.168.1.101
    name: worker-mysql-01
    overlord: 192.168.1.20:9200
    db_type: mysql
```

**部署流程：**

1. Phase 1: 部署 Cerebrate（1 节点）
2. Phase 2: 部署 Overlords（并行）
3. Phase 3: 部署 Workers（分批，每批 50 节点）

每个节点：SCP 传输二进制 -> chmod +x -> cluster join -> 完成。

### opendb cluster test

执行混沌测试场景。

```
opendb cluster test --scenario <name>
```

**可用场景：**

| 场景名 | 说明 |
|--------|------|
| `worker_crash` | Kill 一个 Worker，验证 Overlord 检测到心跳丢失 |
| `overlord_crash` | Kill 一个 Overlord，验证 Worker 降级到独立模式 |
| `cerebrate_crash` | Kill Cerebrate，验证备份 Overlord 提升为 Cerebrate |
| `network_partition` | 模拟网络分区（需 iptables，Linux） |
| `llm_unavailable` | 阻断 LLM API，验证 Rule Engine 降级 |
| `storm` | 多 Worker 同时注入故障，验证 Overlord 关联分析 |

**示例：**

```bash
opendb cluster test --scenario worker_crash
```

**输出：**

```
Chaos Test: worker_crash [PASS] (2.1s)
  Worker killed, Overlord heartbeat detection expected within 90s

  ✓ Find Worker PID: PID 12345
  ✓ Kill Worker: Killed PID 12345
  ✓ Verify killed: Process terminated
  ✓ Overlord detection: Overlord should detect heartbeat loss within 90s
```

### opendb audit verify

验证审计日志完整性。

```
opendb audit verify [--base-dir <path>]
```

审计日志使用 HMAC-SHA256 链式哈希。每条记录的哈希值依赖上一条记录，任何篡改（插入、删除、修改）都会被检测到。

**验证内容：**
- 每条记录的 HMAC 签名是否正确
- 链式哈希是否连续（前一条的哈希参与下一条计算）
- 是否有被删除或插入的记录

**审计日志格式：**

```
2026-04-11T10:05:00+08:00 | worker | ORCLCDB | CREATE INDEX ... | reason: LLM diagnosis | result: reported | hash:a1b2c3d4...
```

**文件位置：**
- 审计日志：`~/.opendb/audit.log`
- HMAC 密钥：`~/.opendb/audit.key`（权限 0600）

---

## 交互式命令

进入交互模式：

```bash
opendb          # 自动检测配置，进入 CLI
```

### 连接管理

| 命令 | 说明 | 示例 |
|------|------|------|
| `/login [name]` | 连接数据库 | `/login orcl-prod` |
| `/logout` | 断开当前连接 | `/logout` |
| `/conn` | 查看当前连接信息 | `/conn` |

### 查询与分析

| 命令 | 说明 | 示例 |
|------|------|------|
| SQL 直接输入 | 执行 SQL | `SELECT * FROM v$session` |
| `/slowsql [ms]` | 慢 SQL（默认 1000ms） | `/slowsql 5000` |
| `/explain <sql>` | 查看执行计划 | `/explain SELECT * FROM orders` |
| `/indexadvise <sql>` | 索引推荐 | `/indexadvise abc123def` |

### 监控

| 命令 | 说明 | 示例 |
|------|------|------|
| `/health` | 综合健康巡检 | `/health` |
| `/dbtop` | 实时监控面板 | `/dbtop` |
| `/sentinel` | 后台异常检测 | `/sentinel` |
| `/sessions` | 所有会话 | `/sessions` |
| `/activesessions` | 活跃会话 | `/activesessions` |
| `/waits` | 等待事件 | `/waits` |
| `/locks` | 锁等待 | `/locks` |
| `/latches` | Latch 争用 | `/latches` |
| `/mutexes` | Mutex 争用 | `/mutexes` |

### 管理

| 命令 | 说明 | 示例 |
|------|------|------|
| `/kill <SID>` | 终止会话 | `/kill 142 immediate` |
| `/space` | 表空间使用 | `/space` |
| `/params [kw]` | 数据库参数 | `/params sga` |
| `/alert [hours]` | 告警日志 | `/alert 48` |
| `/backup [days]` | 备份状态 | `/backup 30` |
| `/standby` | 备库状态 | `/standby` |

### Schema

| 命令 | 说明 | 示例 |
|------|------|------|
| `/tableinfo <table>` | 表详情 | `/tableinfo ORDERS` |
| `/indexadvise <sql>` | 索引推荐 | `/indexadvise SELECT * FROM orders WHERE id = 1` |

### 诊断

| 命令 | 说明 | 示例 |
|------|------|------|
| `/rule` | Rule Engine 诊断 | `/rule` |
| `/llm` | LLM 深度诊断 | `/llm` |
| `/inject <type>` | 故障注入（测试用） | `/inject temp_full` |

**故障注入类型：**

| 类型 | 说明 |
|------|------|
| `temp_full` | 模拟临时表空间满 |
| `lock_contention` | 模拟锁争用 |
| `slow_sql` | 模拟慢 SQL |

> `/inject` 在生产环境中设为 `hidden`（不可见不可用），只在测试环境启用。

### 工具

| 命令 | 说明 |
|------|------|
| `/help` | 查看所有可用命令 |
| `/history` | 命令历史 |
| `/config` | 当前配置 |
| `/model` | 切换 LLM 模型 |
| `/policy` | 查看/管理运维策略 |
| `/scheduler` | 定时任务管理 |
| `/clear` | 清屏 |

### 输入方式

OpenDB 支持三种输入模式，自动检测：

| 输入方式 | 触发规则 | 示例 |
|----------|----------|------|
| 斜杠命令 | `/` 开头 | `/sessions`、`/slowsql 5000` |
| 直接 SQL | SQL 关键字开头 | `SELECT * FROM v$session` |
| 自然语言 | 其他文本（需配置 LLM） | `查看一下慢查询` |

斜杠命令支持模糊匹配：输入 `/sl` 自动匹配到 `/slowsql`。

---

## 批处理模式

非交互执行单条命令：

```bash
opendb -c <connection_name> <command>
```

**示例：**

```bash
# 执行 SQL
opendb -c orcl-prod "SELECT COUNT(*) FROM dba_tablespaces"

# 执行斜杠命令
opendb -c orcl-prod /health

# 查看慢 SQL
opendb -c orcl-prod "/slowsql 2000"
```

---

## Web 大盘 API

Cerebrate 启动时如指定 `--web`，会在该地址提供 HTTP 服务。

### GET /api/health

健康检查。

**响应：**

```json
{
  "status": "healthy",
  "role": "manager",
  "timestamp": "2026-04-11T10:05:00+08:00"
}
```

### GET /api/fleet

集群全局状态。

**响应：**

```json
{
  "timestamp": "2026-04-11T10:05:00+08:00",
  "overlords": 6,
  "total_workers": 600,
  "online_workers": 598,
  "regions": [
    {
      "overlord_id": "overlord-east-a1b2",
      "region": "china-east",
      "workers": 100,
      "online": 99,
      "last_heartbeat": "2026-04-11T10:04:55+08:00"
    }
  ]
}
```

### GET /api/topology

全局拓扑结构（Cerebrate -> Overlord -> Worker 关系）。

**响应：** 与 `/api/fleet` 中的 `regions` 字段结构相同，包含 Overlord 和 Worker 层级关系。

### GET /api/overlords

所有 Overlord 状态列表。

**响应：** Overlord 状态数组，每个元素包含 `overlord_id`、`region`、`workers`、`online`、`last_heartbeat`。

### GET /api/timeline

最近 24 小时故障事件时间线。

**响应：**

```json
{
  "events": [
    {
      "id": "evt-20260411-100500",
      "timestamp": "2026-04-11T10:05:00+08:00",
      "severity": "critical",
      "worker_id": "drone-host1-a1b2",
      "summary": "Bad SQL (全表扫描) on ORCLCDB",
      "report_id": "rpt-20260411-100500"
    }
  ]
}
```

### GET /api/reports

报告列表（支持分页和过滤）。

**查询参数：**

| 参数 | 说明 |
|------|------|
| `severity` | 按严重级别过滤：`critical`、`warning`、`info` |
| `region` | 按区域过滤 |
| `limit` | 每页数量（默认 50） |
| `offset` | 偏移量 |

### GET /api/reports/{id}

单个报告详情（HTML 渲染）。

### GET /api/worker/{id}

Worker 详细状态（最近异常、内存使用、Sentinel 状态）。

### GET /api/report/{id}

区域汇总报告详情。

### GET /api/region/{overlord_id}

指定 Overlord/区域的详细状态（尝试实时查询，超时 5s 后退化为缓存）。

---

## 配置参考

### config.yaml 完整字段

配置文件位于 `~/.opendb/config.yaml`。未设置的参数使用默认值。

```yaml
# ── 连接管理 ──
connections_dir: ~/.opendb/connections  # 连接配置目录

# ── 安全 ──
security:
  default_level: 0              # 安全级别 (0=无限制, 1=只读, 2=危险需确认)
  confirm_on_dangerous: true    # 危险操作（DDL/DML）前确认

# ── 输出 ──
output:
  format: terminal              # terminal | json | csv
  max_rows: 1000                # 查询最大返回行数

# ── 会话 ──
session:
  restore_on_switch: true       # 切换连接时恢复会话变量
  history_dir: ~/.opendb/history

# ── LLM ──
llm:
  provider: ollama              # ollama | openai | none
  model: "qwen3.5:27b"         # 模型名称
  base_url: "http://localhost:11434"
  capability: large             # small (<=9B, 引导式) | large (>=27B, 自主式)
  diagnose_mode: auto           # playbook (单次) | assist (3轮) | auto (10轮)
  max_rounds: 0                 # 0=不限
  max_result_tokens: 4000       # 工具返回结果最大 token 数

# ── 多模型 (可选) ──
models:
  - name: local-qwen
    provider: ollama
    model: "qwen3.5:27b"
    base_url: "http://localhost:11434"
    capability: large
  - name: cloud-claude
    provider: openai
    model: "claude-sonnet-4-20250514"
    base_url: "https://api.anthropic.com/v1"
    api_key: "${ANTHROPIC_API_KEY}"
    capability: large

# ── Sentinel（哨兵）──
sentinel:
  auto_start: true              # 登录后自动启动
  probe_interval: 1s            # 轻探针间隔
  baseline_window: 60           # 基线滑动窗口（样本数）
  min_samples: 10               # 最少样本数才开始检测
  long_sql_threshold_sec: 30    # 慢 SQL 判定阈值（秒）

  trigger_mode: adaptive        # adaptive | fixed
  sigma: 3.0                    # adaptive 模式 sigma 倍数
  sustained_count: 3            # 连续 N 次超标才触发

  # 固定模式阈值（trigger_mode: fixed）
  thresholds:
    active_multiplier: 2.0      # 活跃会话超基线 N 倍
    cpu_multiplier: 2.0         # CPU 会话超基线 N 倍
    io_multiplier: 3.0          # I/O 等待超基线 N 倍
    lock_absolute: 5            # 锁等待 >= N 个
    long_sql_absolute: 3        # 慢 SQL >= N 个
    redo_multiplier: 5.0        # Redo 速率超基线 N 倍
    hard_parse_multiplier: 3.0  # 硬解析超基线 N 倍

  # Burst（突发采集）
  burst_interval: 200ms         # 突发帧间隔
  burst_max_duration: 30s       # 突发最长持续
  burst_calm_delay: 5s          # 确认恢复的等待

  cooldown: 5m                  # 两次触发间最小间隔

# ── 定时巡检 ──
scheduler:
  auto_start: true
  tasks:
    - name: space-watch
      schedule: "@every 10m"
      skill: space
      on_warn: notify
    - name: alert-scan
      schedule: "@every 15m"
      skill: alert
      params:
        hours: 0.25
      on_warn: notify
    - name: stats-check
      schedule: "@daily 02:00"
      skill: health
      on_warn: notify
    - name: backup-check
      schedule: "@daily 08:00"
      skill: backup
      on_warn: notify

# ── 堆栈追踪 ──
trace:
  auto: false                   # Sentinel 联动自动采集
  duration: 3                   # 采集秒数 (1-10)
  top_n: 20                     # 热点函数数量
  output_dir: ~/.opendb/trace/
```

### 连接配置

`~/.opendb/connections/<group>.yaml` 或 `config.yaml` 中内联。

```yaml
# 文件模式：~/.opendb/connections/production.yaml
group: production
tags:
  - oracle
  - prod
connections:
  - name: orcl-prod
    db_type: oracle
    host: 192.168.1.100
    port: 1521
    service: ORCL
    user: system
    auth_mode: prompt           # prompt | save | wallet | os
    credential:
      provider: prompt

  - name: mysql-prod
    db_type: mysql
    host: 192.168.1.200
    port: 3306
    database: production
    user: admin
    credential:
      provider: prompt
```

```yaml
# 内联模式：直接写在 config.yaml 中
connections:
  - name: orcl-test
    db_type: oracle
    host: 192.168.1.100
    port: 1521
    service: ORCL
    user: system
    credential:
      provider: prompt
```

### 集群配置

`~/.opendb/cluster/config.yaml` -- 由 `cluster init/join` 自动生成。

```yaml
role: memory                    # worker | memory | manager
node_id: overlord-host1-a1b2c3d4
listen: 0.0.0.0:9200
region: china-east              # memory 角色必填
cerebrate: 192.168.1.10:9100   # memory 角色上游地址
overlord: ""                    # worker 角色上游地址
web_listen: ""                  # manager 角色 Web 地址
join_token: e3f8a2b1c9d0...
initialized: true
```

### 热更新配置

`~/.opendb/cluster/hotconfig.yaml` -- Cerebrate 通过 gRPC 推送到所有节点。

```yaml
# 黑名单 -- 禁止 LLM 生成的危险操作
blacklist:
  - "DROP TABLE"
  - "DROP DATABASE"
  - "TRUNCATE"
  - "ALTER SYSTEM KILL"

# LLM 全局设置
llm_model: "qwen3.5:27b"
llm_base_url: "http://llm-server:11434"
llm_provider: ollama

# 监控阈值
sentinel_sigma: 3.0
heartbeat_interval_sec: 30
status_poll_interval_sec: 30

# Tool 安全控制（4 级）
# enabled  -- 正常使用
# confirm  -- 执行前需用户确认
# disabled -- 禁用（LLM 不可见，但管理员可手动调用）
# hidden   -- 完全隐藏（不出现在任何接口）
tool_controls:
  inject: hidden
  kill_session: confirm
  escalate_to_overlord: enabled
  rule_write: enabled
```

### 安全级别

| 级别 | 说明 | 操作示例 | 确认 |
|------|------|----------|------|
| L0 只读 | 默认 | SELECT, SHOW, 所有查看命令 | 无需确认 |
| L1 操作员 | 日常运维 | /kill, ALTER SYSTEM | 二次确认（可关闭） |
| L2 管理员 | DDL 操作 | CREATE TABLE, ALTER TABLE | 二次确认（可关闭） |
| L3 危险 | 破坏性操作 | DROP TABLE, TRUNCATE | 强制二次确认（不可关闭） |

---

## 故障报告格式

OpenDB 的核心交付物是故障分析报告。报告由 Worker/Overlord/Cerebrate 三层分级生成。

### 报告层级

| 层级 | 生成者 | 触发条件 | 存储位置 |
|------|--------|----------|----------|
| 节点报告 | Worker | Sentinel 检测到异常并完成诊断 | `~/.opendb/reports/{instance}/{timestamp}_fault.md` |
| 区域报告 | Overlord | 汇总多 Worker 报告 / 定时 | `~/.opendb/reports/{region}/{timestamp}_region.md` |
| 全局报告 | Cerebrate | 全局趋势 | Web 大盘 `/api/reports/{id}` |

### Worker 节点报告结构

```markdown
# 故障分析报告

## 基本信息
| 项目 | 值 |
|------|-----|
| 报告时间 | 2026-04-11 10:05:00 |
| 数据库类型 | Oracle 19c |
| 实例名称 | ORCLCDB |
| 主机 | prod-db-01 (192.168.1.10) |
| 严重级别 | Critical |

## 异常摘要
活跃会话从基线 10 飙升至 50，触发指标: active_non_idle (5x)。
根因分析置信度: 85% -- Bad SQL (SQL_ID: abc123def45)。

## 性能指标快照

### 触发时段 vs 基线对比
| 指标 | 基线 | 异常时 | 变化 |
|------|------|--------|------|
| Active Sessions | 10 | 50 | +400% |
| DB CPU (%) | 15 | 85 | +467% |

### Top 等待事件
| # | 等待事件 | 等待类 | 占比 |
|---|---------|--------|------|
| 1 | db file sequential read | User I/O | 60% |

### Top SQL
| # | SQL_ID | 执行次数 | 平均耗时(ms) |
|---|--------|---------|-------------|
| 1 | abc123def45 | 500 | 240 |

## 根因分析

### 诊断过程
1. 第1轮 -- 查询 v$session 发现 50 个活跃会话
2. 第2轮 -- 查询 v$sql 发现 SQL_ID abc123def45 占 85%
3. 第3轮 -- 查询 DBA_HIST_SQL_PLAN 发现全表扫描

### 根因结论
SQL abc123def45 缺少索引，导致全表扫描。

## 修复方案

### 紧急止血（P0）
SQL 语句... 可直接复制执行

### 根因修复（P1）
...

### 长期优化（P2）
...

## 验证方法
修复后执行的验证 SQL...
```

### 报告质量要求

1. **SQL 可直接执行**：完整、正确、可直接粘贴，不能有占位符
2. **上下文完整**：每条修复建议包含前置确认步骤
3. **预期效果明确**：每步说明预期效果（如"240ms -> 2ms"）
4. **验证方法**：每个报告包含修复后的验证 SQL
5. **优先级分层**：P0 紧急 / P1 根因 / P2 长期
6. **数据来源标注**：每个数据点标注来源视图/表
7. **诊断可追溯**：记录 LLM 每轮查询和结论

### 严重级别

| 级别 | 说明 | 示例 |
|------|------|------|
| `critical` | 数据库不可用或严重性能劣化 | 实例宕机、主键死锁、表空间 100% |
| `warning` | 性能下降但仍可用 | 慢 SQL 增多、连接池接近上限 |
| `info` | 需关注但无立即影响 | 统计信息过期、备份超过 7 天 |

---

## LLM 配置

### 模型选择

| 模型规模 | capability 值 | 诊断模式 | 说明 |
|----------|---------------|----------|------|
| <= 9B | `small` | 引导式（playbook） | LLM 按预定义 playbook 逐步执行 |
| >= 27B | `large` | 自主式（auto） | LLM 自主决定查询策略和轮次 |

### 诊断模式

| 模式 | 轮次 | 说明 |
|------|------|------|
| `playbook` | 1 轮 | 按预定义诊断 playbook 执行，适合小模型 |
| `assist` | 最多 3 轮 | LLM 辅助诊断，人机协作 |
| `auto` | 最多 10 轮 | LLM 全自主诊断，适合大模型 |

### LLM 录制/回放

用于 CI/CD 零成本确定性测试：

```bash
# 录制：将 LLM 交互保存为 JSONL
opendb agent start --role worker --llm-record /tmp/sessions/

# 回放：用录制文件替代真实 LLM 调用
opendb agent start --role worker --llm-replay /tmp/sessions/session_001.jsonl
```

录制文件格式为 JSONL，每行一个请求-响应对，包含时间戳和完整的 Function Calling 交互。

### LLM 降级策略

```
Sentinel 检测到异常
     |
     v
 尝试调用 LLM
     |
 ┌───┴───┐
 成功    失败（超时/限额/不可用）
 |        |
 v        v
LLM 诊断  Rule Engine 降级（273+ 规则）
 |        |
 v        v
生成报告  生成报告
```

LLM 不可用时自动降级到 Rule Engine。降级条件：
- LLM API 超时（默认 30s）
- LLM 预算耗尽（可配置每日限额）
- LLM 冷却中（两次调用间最小间隔）

### LLM 成本控制

在 Autonomy Loop 中，LLM 调用受以下限制：

- **每日预算**：可配置最大调用次数/token 数
- **冷却时间**：两次 LLM 诊断间的最小间隔
- **超预算降级**：自动切换到 Rule Engine
