# Cerebrate 实施目标

> 基于 Q1-Q28 架构决策，将 OpenDB 从单节点 CLI 扩展为 L4 级 7×24 自治集群
> 设计文档：docs/cerebrate/

---

## Phase 1: Daemon 基础设施（Worker Agent 核心）

**目标：让 OpenDB 能以 daemon 模式 7×24 常驻运行**

### 1.1 Daemon 生命周期管理
- `opendb agent start --role worker` 启动后台 daemon 进程
- `opendb agent stop` 优雅停机（完成当前操作后退出）
- `opendb agent status` 查看运行状态
- PID 文件管理、信号处理（SIGTERM/SIGINT）
- 数据库连接从配置文件自动建立（不需要交互式 login）
- 包：`internal/drone/`

### 1.2 Autonomy Loop
- sense→diagnose→act 永续循环
- Sentinel 持续运行（已有，改为 daemon 管理生命周期）
- 异常触发 LLM 诊断（复用现有 `/llm` auto 模式）
- 黑名单检查 → 执行修复 → 验证结果
- 操作审计日志（append-only `~/.opendb/audit.log`）
- 启动恢复流程：扫描记忆 + 数据库日志 → LLM 判断是否需要恢复

### 1.3 CLI 连接 Daemon
- `opendb login` 检测本地 daemon 是否运行
- 如有 daemon → 连接共享状态（gRPC over Unix Socket）
- 如无 daemon → 原有独立 REPL 模式（向后兼容）
- DBA 操作不影响 daemon 自治循环

**验收标准：**
- `opendb agent start` 启动后，Sentinel 持续监控，异常自动诊断修复
- `opendb agent stop` 优雅停机
- `opendb login` 可连接正在运行的 daemon
- daemon 崩溃重启后自动恢复

---

## Phase 2: gRPC 通信层

**目标：Worker ↔ Overlord ↔ Cerebrate 三层可以互相通信**

### 2.1 Proto 定义
- Agent 注册/注销
- 心跳/状态上报
- 指令下发
- memory/policy 增量同步
- 紧急事件推送
- 包：`internal/cluster/proto/`

### 2.2 Worker gRPC Server
- 监听端口供 Overlord 连接
- 本地 Unix Socket 供 CLI 连接
- 响应状态拉取、指令执行
- 包：在 `internal/drone/` 中扩展

### 2.3 Overlord ↔ Worker 双向流
- gRPC bidirectional streaming 长连接
- Worker 紧急上报（推）+ Overlord 定期拉取（拉）
- memory/policy 每分钟增量同步
- 心跳检测（超时 = Worker 离线）
- 包：在 `internal/overlord/` 中实现

### 2.4 Cerebrate ↔ Overlord 双向流
- 同上模式，Overlord 向 Cerebrate 上报
- Cerebrate 下发策略/维护计划
- 包：在 `internal/cerebrate/` 中实现

**验收标准：**
- Worker 启动后自动连接 Overlord
- Overlord 能拉取 Worker 状态
- Overlord 能下发指令给 Worker 并获得结果
- 网络断开后 Worker 独立运行，恢复后自动重连

---

## Phase 3: 集群管理

**目标：支持集群初始化、节点加入、批量部署**

### 3.1 集群初始化与注册
- `opendb cluster init --role manager` 初始化 Cerebrate
- `opendb cluster join --role memory` 加入 Overlord
- `opendb cluster join --role node` 加入 Worker
- Join Token 生成与验证（24h 过期）
- 包：`internal/cluster/`

### 3.2 部署工具
- Pull 安装：Cerebrate 内置 HTTP 文件服务
- 内置批量部署：`opendb cluster deploy --inventory`（SSH 推送）
- gRPC 层级升级：`opendb cluster upgrade --binary`
- 包：在 `internal/cluster/` 中扩展

### 3.3 集群状态管理
- `opendb cluster status` 查看集群全貌
- 节点增删：`/cluster add-worker`、`/cluster remove-worker`
- Overlord 接管：`/cluster takeover`
- Cerebrate 全局映射关系维护（Worker↔Overlord 归属、记忆备份关系）

**验收标准：**
- 能从零搭建一个完整集群（1 Cerebrate + 2 Overlord + N Worker）
- 新 Worker 加入后自动注册到 Overlord
- `cluster status` 显示完整集群拓扑

---

## Phase 4: Overlord 编排层

**目标：Memory Agent 能管理区域内的 Worker，处理跨节点问题**

### 4.1 Worker 管理
- `get_worker_status` — 聚合 Worker 状态
- `get_worker_memory` — 读取 Worker 记忆（跨节点联合分析）
- Worker memory/policy 备份存储
- 包：在 `internal/overlord/` 中实现

### 4.2 区域协调
- `get_region_topology` — 构建区域拓扑（DB 层 + OS 层自动发现）
- `broadcast_command` — 批量下发指令
- `coordinate_failover` — 直连数据库编排主从切换
- `escalate_to_cerebrate` — 重大事件上报
- Worker 上交的跨节点问题处理

### 4.3 区域报告
- `generate_region_report` — 聚合生成区域报告
- 定时巡检编排（Kairos 抖动调度）

### 4.4 高可用
- Overlord 间互相备份 memory/policy
- Overlord 宕机后由 Cerebrate 指定接管

**验收标准：**
- Overlord 能管理 200 个 Worker
- 多 Worker 同时异常时 Overlord 能识别关联模式
- 主从切换全流程自动完成
- Overlord 宕机后另一个 Overlord 自动接管

---

## Phase 5: Cerebrate 全局层 + Web 大盘

**目标：Manager Agent 全局编排 + 浏览器监控大盘**

### 5.1 全局编排 Tool
- `get_all_overlords` — 所有 Overlord 状态
- `get_global_topology` — 全局拓扑视图
- `push_policy` — 策略/黑名单下发
- `schedule_maintenance` — 维护计划下发
- `generate_fleet_report` — 全局舰队报告
- `manage_cluster` — 集群管理

### 5.2 Cerebrate 高可用
- 2 个 Overlord 持有 Cerebrate 全量数据
- Cerebrate 宕机后备份 Overlord 升级为 Cerebrate（双角色）

### 5.3 Web 监控大盘
- 全局拓扑可视化
- 节点健康状态（绿/黄/红）
- 故障节点标记，逐层下钻（全局→区域→节点→故障报告）
- 舰队报告展示

### 5.4 配置热更新
- Cerebrate 作为配置权威源
- gRPC 层级推送，内存原子替换，不停服

**验收标准：**
- 1200 节点规模的集群能正常管理
- Web 大盘实时展示全局拓扑和健康状态
- 策略热更新从 Cerebrate 推送到所有 Worker 秒级生效
- Cerebrate 宕机后自动恢复

---

## Phase 6: 测试与 CI/CD

**目标：三层测试体系 + 无人值守流水线**

### 6.1 故障注入
- `/inject` 命令（temp_full、lock_contention、slow_sql 等）
- 生产环境 hidden

### 6.2 LLM 录制/回放
- `--llm-record` 录制真实诊断
- `--llm-replay` 回放测试（零成本）

### 6.3 集群混沌测试
- `opendb cluster test --scenario` 各类故障场景

### 6.4 CI/CD 流水线
- RemoteTrigger 云端自动迭代
- Build → Test → Chaos → Report 循环

**验收标准：**
- 故障注入后 Worker 能自动诊断和修复
- LLM 回放测试覆盖核心诊断场景
- 集群混沌测试验证 HA 机制
