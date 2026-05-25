# OpenDB Autopilot — 设计问答记录

> 按讨论顺序记录所有架构决策的一问一答
> 开始日期：2026-04-04 | 更新：2026-04-09

---

## Q1：自治级别

**问题：** 自动值守的边界在哪里？

| 级别 | 行为 |
|------|------|
| L1 辅助驾驶 | 检测+诊断+建议，人工确认后执行 |
| L2 半自动 | 低风险自动，高风险等确认 |
| L3 高度自动 | 绝大多数自动，仅危险操作需确认 |
| L4 完全自动 | 全部自主决策，仅事后通知 |

**决策：L4 全自主 + 用户自定义危险操作黑名单**

- 默认：所有操作全自动执行，无需人工干预
- 例外：用户自定义的危险操作完全禁止执行（不是等确认，是直接拒绝）
- DBA 事后看报告即可

---

## Q2：系统架构

**问题：** 运行形态是什么？

**决策：Worker Agent + Manager Agent 双层架构**

- Worker Agent = 现有 OpenDB，部署在每个数据库节点上
- Manager Agent = 新增，专门管理舰队
- 两者都可独立调用 LLM，但角色/skill/提示词/上下文完全不同
- 节点 Agent 和管理者 Agent 持续信息交互

---

## Q3：通信模式

**问题：** 节点 Agent 和 Manager Agent 之间如何通信？

| 方案 | 模式 | 特点 |
|------|------|------|
| A 推送优先 | 节点主动上报 | 简单，Manager 被动 |
| B 拉取优先 | Manager 定期轮询 | Manager 主导，轮询开销大 |
| **C 双向流** | **gRPC 双向 streaming** | **实时性最好** |
| D 事件总线 | NATS/Kafka 中间件 | 最灵活，但引入外部依赖 |

**决策：gRPC 双向流，混合推拉模式**

1. 紧急上报（推）：Worker Agent 遇到紧急问题，本地处理完成后立即上报
2. 定期拉取（拉）：Manager Agent 定期主动抓取 Worker Agent 的状态信息
3. 全局报告：由 Manager Agent 统一生成

---

## Q4：三层架构

**问题：** Manager Agent 的部署形态和管理范围？

**决策：三层分级架构（Manager Agent → Memory Agent → Worker Agent）**

```
Manager Agent (Cerebrate)
  └── Memory Agent (Overlord) ≤200 节点
       └── Worker Agent (Drone) × N
```

Memory Agent 节点上限 200（默认，可配置）。瓶颈是 LLM 上下文窗口，不是 gRPC 连接数。

按数据库类型建议上限：Oracle（重型）100-150 | MySQL/PG（轻型）150-200 | 混合 150

---

## Q5：三层职责边界

**问题：** 三层之间的职责如何划分？

**决策：严格分层，各司其职**

| 职责 | Worker Agent | Memory Agent | Manager Agent |
|------|-------------|-------------|--------------|
| 实时监控 | 48 指标采集 + 异常检测 | — | — |
| 本地诊断 | 规则引擎 + LLM（单机视角） | — | — |
| 本地修复 | 自主执行（受黑名单约束） | — | — |
| 紧急上报 | → Memory Agent | — | — |
| 区域巡检 | — | 定期拉取节点状态 | — |
| 跨节点关联 | — | 区域内主从/集群分析 | — |
| 区域协调 | — | 主从切换等跨节点操作 | — |
| 全局态势 | — | — | 跨区域趋势分析 |
| 全局策略 | — | — | 统一规则/黑名单下发 |
| 全局报告 | — | — | 舰队健康总报 |

---

## Q6：Worker Agent 运行形态 — Daemon 化改造

**问题：** 当前 OpenDB 是 login 后才触发功能，要承担 Worker Agent 必须成为 Daemon 进程。CLI 模式和 Daemon 模式的关系如何？

**决策：CLI 连接 Daemon（类似 Docker 模式）**

```
opendb（单二进制）
  ├── opendb agent start       ← Daemon 模式（Worker Agent）
  │   └── 后台常驻进程
  │       ├── Sentinel 持续探测
  │       ├── 自主决策循环 (sense→diagnose→act)
  │       ├── gRPC Server（供 Memory Agent 连接）
  │       └── Unix Socket / TCP（供本地 CLI 连接）
  │
  └── opendb login             ← CLI 模式（连接本地 daemon）
      └── REPL 交互
```

---

## Q6.1：诊断优先级

**问题：** Sentinel 触发异常后，诊断的调用顺序？

**决策：LLM 优先，Rule Engine 兜底**

```
Sentinel 异常 → LLM 可用? → Yes → LLM 诊断（链式推理，最多20轮）
                           → No  → Rule Engine 兜底（273+ 规则）
```

LLM 不可用的判定：网络不可达 / API 错误 / 未配置 provider / 手动关闭

---

## Q7：危险操作黑名单

**问题：** 黑名单如何配置和管理？

**决策：分层配置，Manager Agent 设底线，区域/节点只能追加、不能放宽**

```
Manager Agent 底线（全局强制）：DROP TABLE / TRUNCATE / SHUTDOWN / DROP DATABASE
  ↓ 只能追加，不能移除
Memory Agent 追加（区域级别）
  ↓ 只能追加，不能移除
Worker Agent 追加（节点级别）
```

合并规则：节点实际黑名单 = 总底线 ∪ 区域追加 ∪ 节点追加

---

## Q8：LLM 调用策略

**问题：** 三层各用什么模型？

**决策：统一使用同一模型，差异化提示词和 Skill/Tools**

| 层级 | 模型 | 提示词角色 | Skill/Tools |
|------|------|-----------|-------------|
| Worker Agent | 统一模型 | "你是数据库节点自治代理..." | query_database, kill_session, execute_repair... |
| Memory Agent | 统一模型 | "你是区域管理代理..." | broadcast_command, get_topology, coordinate_failover... |
| Manager Agent | 统一模型 | "你是全局舰队管理代理..." | get_all_regions, push_policy, generate_fleet_report... |

核心理由：避免模型差异产生行为差异。

---

## Q9：全局报告体系

**问题：** 报告的内容、频率和层级关系？

**决策：Worker 出明细，Manager Agent 出汇总，明细全量上传**

| 频率 | 生成者 | 内容 |
|------|--------|------|
| 即时 | Memory Agent / Manager Agent | 重大事件即时报告 |
| 每日 | Memory Agent + Manager Agent | 区域日报 + 全局日报 |
| 每周 | Manager Agent | 全局周报（趋势分析、容量规划） |

报告层级：Worker 明细 → Memory Agent 聚合区域报告 → Manager Agent 汇总全局报告，支持下钻。

---

## Q10：Manager Agent 技术栈

**问题：** Go？复用 OpenDB 代码？独立 repo？

**决策：全部整合到 OpenDB 单二进制，通过 role 参数区分角色**

- 同一个 `opendb` 二进制，配置 `role: manager / memory / node` 启动不同角色
- 不同 role 激活不同子系统
- 三层都支持 CLI 接入（`opendb login`），各自有角色专属 `/` 命令
- Manager Agent (Cerebrate) 额外提供 Web UI，用于浏览器查看汇总报告
- 业界参考：Consul（`-server` / `-client`）、Nomad（server/client config）

---

## Q11：CLI-daemon 通信协议

**问题：** Unix Socket / TCP / gRPC？

**决策：gRPC 统一协议，双传输层**

| 场景 | 传输层 |
|------|--------|
| CLI ↔ 本机 Daemon | gRPC over Unix Socket |
| CLI ↔ 远程 Overlord/Cerebrate | gRPC over TCP |
| Overlord ↔ Drone / Cerebrate ↔ Overlord | gRPC over TCP |

一套 proto 定义，一套服务接口，两种传输层。业界参考：containerd。

---

## Q12：Agent 注册、发现与身份认证

**问题：** Drone 怎么找到 Overlord？身份怎么认证？如何批量部署？

**决策：静态配置 + Join Token（kubeadm 模式），两阶段认证**

注册流程：
```
opendb cluster init --role manager                              # ① 初始化 Cerebrate
opendb cluster join --role memory --cerebrate <addr> --token .. # ② 加入 Overlord
opendb cluster join --role node --overlord <addr> --token ..    # ③ 加入 Drone
```

认证：Join Token（一次性/限时，24h 过期）。内网环境不使用 mTLS（见 Q30）。

**V1 首次部署三种方式并存：**

| 方式 | 前置条件 | 说明 |
|------|---------|------|
| **Pull 安装** | 不开 22 | Cerebrate 内置 HTTP 文件服务，节点执行 `curl http://cerebrate/install \| sh` 拉取二进制 + 配置 + 启动 |
| **适配 Ansible** | 开 22，用户想用通用工具 | 提供标准 Ansible playbook + inventory 模板 |
| **内置批量部署** | 开 22，用户想在 opendb 内完成 | `opendb cluster deploy --inventory inventory.yaml --binary ./opendb`，内置类 Ansible 的 SSH 批量分发能力 |

后续升级：gRPC 层级分发（Cerebrate → Overlord → Drone），不依赖 SSH。二进制层级扇出，每个 Overlord 并行推送给自己的 Worker。

---

## Q13：Cerebrate / Overlord 高可用

**问题：** Cerebrate 和 Overlord 都是单点，宕机后怎么恢复？

**决策：数据库主备模式，每个角色 2 份数据镜像**

**Overlord HA：**
- 至少 3 个 Overlord
- 每个 Overlord 将自己的记忆复制给 2 个其他 Overlord（类似数据库主备）
- 可以互相复制（A→B, B→A）
- Overlord 宕机后，由 Cerebrate 指定持有其记忆副本的 Overlord 接管其 Worker

**Cerebrate HA：**
- Cerebrate 是特殊的 Overlord
- 2 个 Overlord 持有 Cerebrate 的全量数据备份
- Cerebrate 宕机后，备份 Overlord 升级为 Cerebrate 角色，同时保留自己原有的 Worker 管理职责（双角色）

**Cerebrate 的数据（全局映射，轻量）：**
- Worker ↔ Overlord 归属关系（包含状态：在线/宕机/维护中）
- Overlord ↔ Worker 记忆备份关系（谁持有谁的记忆）
- Overlord ↔ Overlord 互备关系
- 注意：只存映射关系，不存实际 memory/policy 数据

**Overlord 的数据（实际业务数据，重量）：**
- 管理的 Worker 清单
- Worker 的实际 memory/policy 数据
- Worker 的数据库连接串信息

**一个节点可以同时承担多个角色（如 Cerebrate + Overlord），数据各自独立管理，不冲突。**

**接管决策：**
- 正常情况：Cerebrate 指定接管者
- Cerebrate 也不在时：退化到预设优先级顺序

**状态变更同步：** 任何拓扑变化（Worker 宕机、Overlord 接管、新节点加入）都会同步更新 Overlord 和 Cerebrate 上的映射状态。

---

## Q14：状态持久化与崩溃恢复

**问题：** daemon 7×24 运行必然会遇到进程崩溃、机器重启，如何恢复状态？

**决策：memory/policy 增量同步到 Overlord，崩溃后由 Overlord 推回**

- memory = OpenDB 现有 memory 系统（5 种类型：incident/solution/preference/workload/pattern）
- policy = OpenDB 现有 4 级 policy 系统（Platform → Org → Instance → Session）
- 同步频率：每分钟，增量
- 崩溃丢失策略：如果没有保存到记忆文件或没有同步到 Overlord，默认丢失，不做复杂恢复
- Overlord 之间互相备份 memory/policy 数据
- cerebrate 项目 = opendb 的大规模集群版本，复用 OpenDB Engine V2 全套机制

---

## Q14.1：正在执行的修复操作崩溃了怎么办

**问题：** Drone 正在执行两步修复方案，执行完第一步崩溃了，重启后要不要补第二步？

**决策：把"恢复"变成启动后的第一次"诊断"**

```
Drone 启动
  ├── ① 从 Overlord 拉取 memory/policy
  ├── ② 扫描本地 memory 文件，读取上次中断前的重点记忆
  ├── ③ 扫描数据库后台日志（Oracle alert log / MySQL error log / PG log）
  │      → LLM 分析：启动前做了什么？有无半完成的操作？
  ├── ④ LLM 判断是否需要恢复动作
  │      → 需要 → 生成恢复方案 → 执行
  │      → 不需要 / 判断不了 → 跳过
  └── ⑤ 进入 Autonomy Loop（sense→diagnose→act）
```

核心原则：**能恢复就恢复，恢复不了就放弃，等问题再出现时重新处理。**

本质上是把"恢复"变成了启动后的第一次"诊断"，复用了已有的 LLM 诊断能力，零额外基础设施。

---

## Q15：脑裂仲裁机制

**问题：** Drone 自主决策和 Overlord 下发指令冲突时怎么办？

**决策：优先级仲裁（人 > 上级 LLM > 本地 LLM）**

| 优先级 | 指令来源 | 说明 |
|--------|---------|------|
| **最高** | 用户在 Drone 上直接发起，或用户激活 LLM 发起 | DBA 在场，人说了算 |
| **中** | Overlord 的 LLM 发起 | 区域视角，优于本地自治 |
| **低** | Drone 自身的 LLM 自主发起 | 仅本地视角 |

实现：Drone 本地优先级队列，不需要分布式锁。

---

## Q16：跨节点操作协调

**问题：** 主从切换、RAC 维护等跨节点操作如何协调？

**决策：Worker 只处理单节点问题，跨节点问题一律上交 Overlord**

- Worker 的 LLM 处理单节点问题（性能、慢 SQL、等待事件等）
- Worker 的 LLM 一旦发现是跨节点故障（切换、RAC 维护等），统一上报给 Memory Agent 处理
- Memory Agent 持有所有 Worker 的数据库连接串，可以直接连接数据库操作
- Memory Agent 的 LLM 在一个会话内完成所有跨节点步骤，天然串行，天然一致
- 即使 Worker 判断错误（误判为跨节点），Memory Agent 也能通过连接串直接处理

**效果：不需要分布式锁、saga 模式、两阶段提交。Overlord 就是"跨节点 DBA"。**

结合 Q15，脑裂问题完整解决：

| 操作类型 | 处理者 | 脑裂风险 |
|---------|--------|---------|
| 单节点问题 | Worker 自主处理 | 有（用优先级仲裁：人 > Overlord > Worker） |
| 跨节点问题 | Worker 不碰，上交 Overlord | 无（只有 Overlord 操作） |

---

## Q17：7×24 上下文管理

**问题：** 7×24 长期运行的 LLM 上下文如何管理？

**决策：复用 OpenDB Engine V2 的完整上下文管理机制**

- 短期记忆：session（jsonl 文件），会话级上下文
- 长期记忆：memory 文件（跨会话的故障经验沉淀）
- 实例画像：PROFILE.md 每次全文加载
- 压缩：3 层（Turn Collapse → Auto Summary → Emergency Truncate）
- memory/session 共享 10GB 配额，policy 不计入配额
- 通过每分钟增量同步到 Overlord 实现跨节点持久化

---

## Q18：数据库拓扑感知与自动发现

**问题：** 场景推演中的拓扑信息（RAC 集群、主从关系、共享存储）从哪来？

**决策：全自动发现，Worker 从数据库层 + OS 层采集，汇聚到 Overlord**

Worker 启动时自动采集：
- 数据库层：主从角色（v$dataguard_config / SHOW REPLICA STATUS / pg_stat_replication）、RAC 成员
- OS 层：共享存储（multipath / lsblk / ASM）、网络拓扑（ip addr / 子网掩码）、存储序列号/WWN

Overlord 汇聚：
- 多个 Worker 报了相同存储序列号/WWN → 自动识别共享存储分组
- 多个 Worker 在同一子网 → 自动识别网络分组
- 主从关系自动拼图
- 生成区域拓扑地图

Cerebrate：
- 汇聚所有区域拓扑 → 全局拓扑展示（Web UI 大盘）
- 用户可在 Web UI 上调整/纠正拓扑关系

拓扑变更感知：Worker 定期刷新（如每 5 分钟查一次主从状态），变化时立即上报 Overlord。

---

## Q19：通知与告警通道

**问题：** 报告生成后如何送达 DBA？

**决策：V1 不做主动推送，做 Cerebrate Web 监控大盘**

大盘功能：
- 全局拓扑关系展示（自动发现的存储/网络/主从/RAC）
- 所有节点健康状态（绿/黄/红）
- 故障节点标记，支持逐层下钻：全局 → 区域 → 节点明细 → 故障报告
- 大盘 = 拓扑 + 健康 + 报告的统一入口

主动推送（邮件/短信/企微/钉钉/飞书/Webhook）留待后续版本。

---

## Q20：LLM Tool/Function 定义 + 安全控制

**问题：** 三层各自的 Tool 清单？安全如何控制？

**决策：基于 OpenDB 现有 90+ skill，集群版只新增 14 个 Tool**

### Worker Agent (Drone) — 几乎全部复用 OpenDB 现有 skill

新增仅 1 个 Tool + 2 个基础设施：

| 新增 | 说明 |
|------|------|
| `escalate_to_overlord` | 判断为跨节点问题时上交 Overlord |
| Daemon 模式 | `opendb agent start --role worker` 常驻进程 + Autonomy Loop |
| gRPC Server | 供 Overlord 连接（用户透明，自动运行） |

其余全部复用：sql, explain, kill, alert, os, sentinel, rule, llm, health, dbtop, memory_write/recall/update, replication, standby 等 90+ skill。

### Memory Agent (Overlord) — 新增 7 个编排 Tool

| 新增 Tool | 类型 | 说明 |
|-----------|------|------|
| `get_worker_status` | 只读 | 聚合查看管辖的全部 Worker 状态 |
| `get_worker_memory` | 只读 | 读取任意 Worker 的记忆（用于跨节点联合分析） |
| `get_region_topology` | 只读 | 从 Worker 上报数据构建区域拓扑 |
| `broadcast_command` | 写操作 | 向多个 Worker 批量下发指令 |
| `coordinate_failover` | 写操作 | 编排主从切换（直连多个数据库执行，复用现有 sql skill） |
| `generate_region_report` | 输出 | 聚合生成区域健康报告 |
| `escalate_to_cerebrate` | 上报 | 重大事件上报 Cerebrate |

Overlord 通过 Worker 的连接串直连数据库后，复用 Worker 的全部 skill，不需要额外开发。

### Manager Agent (Cerebrate) — 新增 6 个全局 Tool

| 新增 Tool | 类型 | 说明 |
|-----------|------|------|
| `get_all_overlords` | 只读 | 获取所有 Overlord 状态 |
| `get_global_topology` | 只读 | 汇聚全局拓扑视图 |
| `push_policy` | 写操作 | 下发策略/黑名单更新到 Overlord → Worker |
| `schedule_maintenance` | 写操作 | 下发预防性维护计划到 Overlord |
| `generate_fleet_report` | 输出 | 生成全局舰队报告 |
| `manage_cluster` | 管理 | 集群管理（节点增删、Overlord 接管指定、升级编排） |

### 安全控制：变更类 Tool 的 4 级控制

| 控制级别 | 行为 | 适用 |
|---------|------|------|
| `enabled` | LLM 可见可调用 | 只读 Tool 默认 |
| `confirm` | LLM 可见，调用前需确认 | 数据库变更操作 |
| `disabled` | LLM 不可见，CLI 可见但不能执行 | 高危操作 |
| `hidden` | LLM 和 CLI 都不可见 | 彻底禁止 |

```yaml
security:
  tools:
    kill_session:        hidden
    execute_sql:         disabled
    coordinate_failover: confirm
    broadcast_command:   confirm
    get_worker_status:   enabled
```

**关键原则：**
- 安全控制只针对数据库变更操作
- 不限制记忆同步（memory_write/recall/update）
- 不限制上报通信（escalate_to_overlord/cerebrate、状态上报）
- 不限制报告生成（generate_region_report/fleet_report）
- 不限制只读查询

即：**管住手（数据库变更），不堵嘴（通信/记忆/报告）。**

### Overlord Tool 操作示例

**get_worker_status** — 查看 Worker 状态：
```
opendb> /workers
┌─────────────────────┬────────┬───────┬────────┬──────────────────────┐
│ Worker              │ DB Type│ Status│ Health │ Last Anomaly         │
├─────────────────────┼────────┼───────┼────────┼──────────────────────┤
│ Oracle-A-037        │ Oracle │ ✓ 在线│ 96/100 │ 02:17 TEMP 表空间    │
│ MySQL-A-102         │ MySQL  │ ✗ 宕机│ —      │ 14:22 连接丢失       │
│ ...（共 200 个）     │        │       │        │                      │
└─────────────────────┴────────┴───────┴────────┴──────────────────────┘
```

**get_worker_memory** — 读取 Worker 记忆：
```
opendb> /worker-memory Oracle-A-037
# 显示 PROFILE.md + MEMORY.md 索引，用于跨节点联合分析
```

**get_region_topology** — 区域拓扑：
```
opendb> /topology
# 展示 RAC 集群、主从关系、存储分组、网络分组的可视化拓扑图
```

**broadcast_command** — 批量下发：
```
opendb> /broadcast --target "SAN-A-PROD-01" --command "ALTER SYSTEM SET db_file_multiblock_read_count=8"
Broadcasting to 4 workers on SAN-A-PROD-01...
  Oracle-A-037: ✓ OK
  Oracle-A-038: ✓ OK
  ...
```

**coordinate_failover** — 主从切换：
```
opendb> /failover ora-dg-05
Failover Plan:
  Step 1: Check sync status... lag=0 ✓
  Step 2: Stop writes on Primary... ✓
  Step 3: Final log apply on Standby... ✓
  Step 4: Promote Standby to Primary... ✓
  Step 5: Update topology... ✓
Failover completed in 12s.
```

**generate_region_report** — 区域报告：
```
opendb> /region-report
# 聚合所有 Worker 数据生成区域日报，含事件统计、预警、建议
```

**escalate_to_cerebrate** — 上报（LLM 自动调用，非手动）：
Overlord LLM 判断为重大事件时自动调用，携带摘要、影响范围、已采取措施。

### Cerebrate Tool 操作示例

**get_all_overlords** — 查看全部 Overlord：
```
opendb> /overlords
┌──────────────┬────────┬─────────┬────────┬──────────┐
│ Overlord     │ Region │ Workers │ Online │ Health   │
├──────────────┼────────┼─────────┼────────┼──────────┤
│ Overlord-A   │ 华东   │ 300     │ 299    │ 94/100   │
│ Overlord-B   │ 华北   │ 300     │ 300    │ 82/100   │
│ Overlord-C   │ 华南   │ 300     │ 300    │ 99/100   │
│ Overlord-D   │ 海外   │ 300     │ 300    │ 97/100   │
└──────────────┴────────┴─────────┴────────┴──────────┘
```

**get_global_topology** — 全局拓扑：
```
opendb> /global-topology
# 汇聚所有区域的拓扑，含跨区域风险提示（如同型号存储）
# 同时在 Web 大盘 http://cerebrate:8080 可视化展示
```

**push_policy** — 下发策略：
```
opendb> /push-policy --type blacklist --action add --rule "ALTER SYSTEM FLUSH SHARED_POOL"
Push policy to all Overlords...
  Overlord-A: ✓ received → 300 workers updated
  ...
Blacklist updated: 5 rules (was 4)
```

**schedule_maintenance** — 下发维护计划：
```
opendb> /schedule-maintenance
Maintenance Plan (based on daily report):
  1. 华东 8 节点表空间扩容 → Overlord-A, window: 20:00-22:00
  2. 华东 3 节点密码轮换   → Overlord-A, window: 22:00-23:00
Confirm and dispatch? [y/N] y
Dispatched. ✓
```

**generate_fleet_report** — 全局舰队报告：
```
opendb> /fleet-report
# 全局日报：舰队规模、各区域评分、今日统计、本周建议、支持下钻
# 同时在 Web 大盘展示
```

**manage_cluster** — 集群管理：
```
# 查看集群状态
opendb> /cluster status

# 添加 Worker（两种写法）
opendb> /cluster add-worker --overlord Overlord-A --db-conn "user:pass@host:3306/db"   # 一步到位
opendb> /cluster add-worker --overlord Overlord-A MySQL-A-201                           # 先注册，后去节点配置

# 移除 Worker
opendb> /cluster remove-worker MySQL-A-102

# Overlord 接管
opendb> /cluster takeover --from Overlord-B --to Overlord-C

# 集群滚动升级
opendb> /cluster upgrade --binary ./opendb-v2 --strategy rolling
```

`/cluster add-worker` 本质上等价于在目标节点执行 `opendb configure` + `opendb agent start`，复用同一套连接管理代码。支持两种模式：带连接串一步到位，或先注册后去节点手动配置。

---

## Q21：可观测性与自监控

**问题：** Agent 自身的健康监控——谁来监控监控者？

**决策：层级互相监控，不需要外部监控系统**

```
Standby Manager (Overlord) ── 监控 ──► Cerebrate
Cerebrate                   ── 监控 ──► 所有 Overlord
Overlord                    ── 监控 ──► 管辖的所有 Worker
```

- 每一层都被上级盯着，Cerebrate 被自己的 Standby（Overlord）盯着，没有盲区
- 监控内容复用 gRPC 心跳和状态拉取机制——心跳超时就是"挂了"，状态数据里包含进程健康指标
- 不需要 Prometheus 等外部系统，监控数据汇聚到 Cerebrate Web 大盘统一展示

---

## Q22：配置管理与热更新

**问题：** 7×24 运行中怎么更新黑名单、规则、LLM 模型、System Prompt，不停服？

**决策：Cerebrate 是配置权威源，通过 gRPC 推送，各层热加载**

配置分两类：

**启动配置（静态，改了要重启）：**
- role、listen 地址、overlord 地址、数据库连接串等基础信息

**运行配置（动态，热更新）：**
- 黑名单、Policy、LLM 模型/Provider、System Prompt、监控阈值、巡检频率

热更新流程：
```
DBA 在 Cerebrate 修改配置（CLI 或 Web UI）
  ├── 持久化到 Cerebrate 本地（权威源）
  ├── gRPC 推送 → Overlord → Worker（层级下发）
  │     各层：持久化到本地（备份） + 内存原子替换生效
  └── 返回推送结果（成功/失败节点数）
```

同时支持手动重载（应急）：`opendb agent reload`

不使用外部配置中心（etcd/Consul/Nacos），Cerebrate 本身就是配置中心，复用已有 gRPC 通道。

---

## Q23：LLM 成本控制

**问题：** 如何防止 LLM 调用失控？

**决策：V1 待办，后续版本实现。**

方案已设计：
- 三层限额：全局每日上限 / 区域每日上限 / 单节点每小时+每日上限
- 冷却机制：同一异常 30 分钟内不重复调 LLM，连续 3 次未好转上交 Overlord
- 触达上限后优雅降级为 Rule Engine only 模式
- 成本追踪在 Cerebrate 大盘展示

---

## Q24：变更审计与合规

**问题：** L4 自动执行操作后如何审计追踪？

**决策：V1 做极简审计日志**

每次写操作（kill、alter、execute_sql 等）append 一行到本地审计日志：

```
# ~/.opendb/audit.log（append-only）
2026-04-09T02:17:06 | worker | Oracle-A-037 | KILL SESSION '472,38291' | reason: TEMP 93%, LLM诊断 | result: OK
```

- 审计日志随 memory 一起同步到 Overlord 备份
- 高级功能（防篡改签名、合规报告、操作回放）留待后续版本

---

## Q25：部署与升级策略

**问题：** 版本兼容性和回滚策略？

**决策：**

- **版本兼容**：新版必须兼容旧版，硬性策略。滚动升级过程中新旧版本可共存
- **回滚顺序**：Worker → Overlord → Cerebrate（和升级顺序反过来）
- **回滚方式**：替换二进制 + 重启，复用 gRPC 层级分发机制

首次部署和二进制升级方案见 Q12。

---

## Q26：故障经验学习

**问题：** 如何从历史故障中自动提取模式？LLM 诊断过的问题如何沉淀？

**决策：成功修复后自动沉淀为 Rule 文件，按 trigger 匹配按需加载**

### Rule 写入机制

复用 OpenDB 已有的双路径写入（和 memory 相同的时机）：

```
Sentinel 异常 → LLM 诊断 → 执行修复 → 验证结果
                                          │
                                    修复成功？
                                    ╱       ╲
                                  Yes        No
                                   │          │
                           写 memory       写 memory
                          (incident +     (incident only)
                           solution)       不写 rule
                               │          (不沉淀错误经验)
                               │
                           写 rule
                         (LLM 提炼：
                          trigger +
                          处理建议)
```

关键：**Rule 只在修复验证成功后才写，失败的经验不沉淀为规则。**

由 Engine.Run 后处理阶段触发，LLM 通过 `rule_write` 工具（新增）写入。

### Rule 文件格式

```markdown
---
trigger:
  metric: temp_tablespace_usage
  condition: "> 85%"
tags: [temp, hash_join, sort, index]
created: 2026-04-09T02:17:09
source: incident_temp_20260409.md
---
该节点 TEMP 爆满通常由 orders 表 HASH JOIN 导致。
优先检查 orders 相关 SQL，kill 大排序 session 止血，再补索引。
```

`source` 字段关联原始 memory，可追溯 rule 来源。

### Rule 索引机制

不使用 MEMORY.md 那种手动索引（rule 只增不删，会无限增长）。

**用 frontmatter 的 trigger 字段自动构建内存索引：**

```
Agent 启动 → 扫描所有 rule 文件的 frontmatter → 构建内存索引表
  { "temp_tablespace_usage" → [rule_temp_tablespace.md],
    "io_read_latency"       → [rule_io_latency.md], ... }

运行时：
  Sentinel 检测到 temp_tablespace_usage > 85%
    → 内存索引命中 rule_temp_tablespace.md
    → 只加载这一个 rule 注入 LLM 上下文
    → 其他 rule 完全不加载
```

**效果：小模型也能用——不是塞 100 万 token 的经验，而是只塞当前故障相关的几百 token 的经验。使用时间越长，匹配的 rule 越精准。**

### Rule vs Memory vs Policy 的区别

| | Policy | Memory | Rule |
|--|--------|--------|------|
| 谁写 | 人（DBA/管理员） | LLM + 代码 | LLM 自动沉淀 |
| LLM 能改 | 不能（只读） | 能（memory_write） | 能（rule_write） |
| 会删除 | 不会 | 会（配额淘汰） | 不会 |
| 加载方式 | 每次全量加载 | 索引 + 按需 recall | 按 trigger 匹配加载 |
| 用途 | 约束行为（"不许做什么"） | 记录事件（"发生过什么"） | 指导诊断（"遇到这个怎么处理"） |
| 触发写入 | 人手动 | 诊断完成（无论成败） | 修复成功且验证通过 |

### Policy 存储位置（集群版扩展）

```
Cerebrate 层面（权威源）：
  ~/.opendb/policies/
  ├── global/                   # 全局策略（下发到所有节点）
  │   ├── blacklist.md
  │   └── safety.md
  ├── region/{region_name}/     # 区域级
  └── org/                      # 组织级（OpenDB 现有）

Worker 节点上（接收推送）：
  ~/.opendb/policies/
  ├── org/                      # 从 Cerebrate 同步
  ├── {instance_name}/          # 本地实例级
  └── _pushed/                  # Cerebrate/Overlord 推送的策略
```

4 级优先级：Platform 默认 → Org → Instance → Session（OpenDB 现有机制不变）

---

## Q27：混沌工程与测试

**问题：** 如何测试 L4 自治行为的正确性？

**决策：三层测试 + 无人值守 CI/CD 流水线**

### 第一层：故障注入工具（内置 /inject 命令）

在测试环境中主动制造数据库故障，验证 Worker 能否正确诊断和修复：

```bash
opendb> /inject --fault temp_full          # TEMP 表空间爆满
opendb> /inject --fault lock_contention    # 锁等待链
opendb> /inject --fault slow_sql           # 慢 SQL
opendb> /inject --fault replication_lag     # 主从延迟
opendb> /inject --fault connection_exhaust  # 耗尽连接数
opendb> /inject --fault io_latency         # I/O 延迟
```

注入后自动验证：Sentinel 检测 → LLM 诊断 → 修复执行 → 结果验证 → 上报正确。
本质上是给 Worker 出考题。生产环境中 /inject 设为 hidden。

### 第二层：LLM 录制/回放（确定性测试）

```bash
opendb agent start --role worker --llm-record ./sessions/    # 录制真实 LLM 诊断
opendb agent start --role worker --llm-replay ./sessions/    # 回放，零 LLM 成本
```

| 模式 | 用途 | LLM 调用 |
|------|------|---------|
| 正常模式 | 生产运行 | 真实调用 |
| 录制模式 | 采集测试用例 | 真实调用 + 存盘 |
| 回放模式 | CI/CD 自动化测试 | 读盘回放，零成本 |

### 第三层：集群混沌测试

```bash
opendb cluster test --scenario worker_crash       # 随机 kill Worker
opendb cluster test --scenario overlord_crash      # kill Overlord，验证接管
opendb cluster test --scenario cerebrate_crash     # kill Cerebrate，验证升级
opendb cluster test --scenario network_partition   # 网络分区
opendb cluster test --scenario llm_unavailable     # LLM 不可用，验证 Rule Engine 降级
opendb cluster test --scenario storm               # 多 Worker 同时故障，验证关联分析
```

### 无人值守 CI/CD 流水线

7×24 持续运行，每 30 分钟一轮：

```
Build（构建）→ Test（单元+LLM回放）→ Chaos（故障注入）→ Report（报告）
     │              │                    │                  │
  失败→自动修复   失败→自动修复      失败→记录issue      生成报告
     → 重试          → 重试           → 尝试修复        → 循环继续
```

流水线配置：`.pipeline/config.yaml`
流水线报告：`.pipeline/reports/`
自动发现的问题：`.pipeline/issues/`
自动修复记录：`.pipeline/fixes/`

演进路线：
1. 有代码后立即启用 Build + Unit Test + Lint 自动修复循环
2. 录制第一批 LLM session 后加入回放测试
3. 集群功能完成后加入混沌测试
4. 稳定后加入性能回归测试

---

## Q28：多租户与多集群

**问题：** 是否支持多个独立的 Cerebrate 管理不同业务线？租户间如何隔离？

**决策：V1 不做，先支持单集群，多租户后续迭代。**

---

## Q29：LLM 本地部署集群方案

**问题：** Manager、Memory、Worker 都要调 LLM，如果 LLM 本地部署，LLM 集群如何搭建？

**决策：LLM 集群对 OpenDB 透明，OpenDB 只需配置 base_url**

OpenDB 的 LLM 调用统一通过 `config.yaml` 中的 `base_url` 发起，后端是单机还是集群对 OpenDB 代码完全透明。

LLM 部署方案选型（按规模）：

| 方案 | 部署方式 | 适合规模 | HA 方式 |
|------|---------|---------|--------|
| **单机 Ollama/vLLM** | 一台 GPU 服务器 | ≤1200 节点（99 次/天） | 备用机器冷切 |
| **Ollama 多节点** | 多台各跑 Ollama，OpenDB 配多个 base_url 轮询 | 中小规模 | 自动轮询容错 |
| **vLLM 集群 + 负载均衡** | 多 GPU 节点 + Nginx/HAProxy 前端 | 大规模生产 | 负载均衡自动摘除故障节点 |
| **API 网关** | 统一 API 网关地址，后端挂多个 LLM 实例 | 任意规模 | 网关层面 HA |

关键数据：从 24 小时场景推演看，1200 节点每天只有 99 次 LLM 调用（Node 84 + Region 12 + Global 3），这个量级**一台 GPU 服务器就够了**。LLM 集群的主要目的是**高可用**（一台挂了有备用），而非性能。

与 OpenDB 的关系：
- OpenDB 不感知 LLM 是单机还是集群，只管调 `base_url`
- 如果配了多个 model（config.yaml 的 models 列表），可以实现模型级别的 failover
- LLM 不可用时自动降级到 Rule Engine（Q6.1 已设计）

---

## Q30：集群认证方案（去除 mTLS）

**问题：** Go 1.26 的 crypto/fips140 模块在 CentOS 7 上 crash。mTLS 引入了 crypto 依赖导致二进制不兼容。用户可能使用 CentOS 7，如何处理？

**决策：不使用 mTLS，统一使用 Join Token 认证**

- 删除 `internal/cluster/tls.go`（CA 证书、节点证书功能全部移除）
- Q12 认证方案简化为：Join Token（24h 过期，一次性使用）
- 集群内部通信使用 gRPC（无加密），适合企业内网环境
- 不影响任何功能，不影响用户体验
- 保持单二进制兼容所有 OS（CentOS 7/8、Ubuntu、Debian 等）

**原因：** mTLS 会导致 CentOS 7 用户无法使用，影响用户体验，且企业数据库集群通常部署在内网，通信加密不是刚需。

---

## 命名体系

**三层 Agent 采用星际争霸 2 虫族命名：**

| 层级 | 正式名称 | 开发代号 | 星际原型 |
|------|---------|---------|---------|
| 顶层 | Manager Agent | Cerebrate（脑虫） | 虫族指挥官，统领全局 |
| 中间层 | Memory Agent | Overlord（王虫） | 虫族宿主，协助脑虫控制虫群 |
| 底层 | Worker Agent | Drone（工蜂） | 虫族执行者，负责干活 |

- 文档中使用正式名称
- 代码中函数、接口、结构体、注释必须使用开发代号
- 代码命名示例：`cerebrate/`, `overlord/`, `drone/`, `CerebrateServer`, `OverlordCoordinator`, `DroneAgent`
