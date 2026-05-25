# OpenDB 自动驾驶数据库 — 架构头脑风暴记录

> 讨论日期：2026-04-04 | 状态：进行中

---

## 一、项目背景

### 1.1 OpenDB 现状

OpenDB 是一个部署在数据库节点上的 DB CLI Agent，当前版本 v0.9.19，核心能力：

| 功能 | 说明 |
|------|------|
| `/health` | 20+ 检查项，7维度全面体检 |
| `/dbtop` | 实时性能仪表盘（1秒刷新） |
| `/sentinel` | 48 指标 × 9 种检测策略，自适应 3σ 基线 |
| `/scheduler` | Cron 定时任务调度 |
| `/rule` | 273+ 规则，5 阶段诊断管线，毫秒级确定性诊断 |
| `/llm` | LLM function calling，最多 20 轮链式推理 |

**技术栈：** Go 1.26.1 | 支持 Oracle/MySQL/PostgreSQL/OpenGauss | 单二进制部署

### 1.2 目标愿景

将 OpenDB 研发成 **7×24 全自动值守** 的自动驾驶数据库产品，实现完全无人值守，自动处理数据库遇到的各种问题。

---

## 二、关键决策记录

### Q1：自治级别

**问题：** 自动值守的边界在哪里？

| 级别 | 行为 |
|------|------|
| L1 辅助驾驶 | 检测+诊断+建议，人工确认后执行 |
| L2 半自动 | 低风险自动，高风险等确认 |
| L3 高度自动 | 绝大多数自动，仅危险操作需确认 |
| L4 完全自动 | 全部自主决策，仅事后通知 |

**决策：L4 全自主 + 用户自定义危险操作黑名单**

- 默认：所有操作全自动执行，无需人工干预
- 例外：用户自定义的危险操作**完全禁止执行**（不是等确认，是直接拒绝）
- DBA 事后看报告即可

---

### Q2：系统架构 — 双层 Agent

**问题：** 运行形态是什么？

**决策：Worker Agent + Manager Agent 双层架构**

```
┌─────────────────────────────────────────────────────────────┐
│                    Manager Agent（管理者）                    │
│  - 管理几十/上百/上千个节点                                   │
│  - 独立调用 LLM（全局视角的 skill/提示词/上下文）              │
│  - 全局管理报告输出                                          │
│  - 跨节点关联分析                                            │
└───────────┬──────────────┬──────────────┬───────────────────┘
            │              │              │
     gRPC 双向流     gRPC 双向流    gRPC 双向流
            │              │              │
┌───────────▼──┐  ┌────────▼─────┐  ┌────▼──────────────┐
│  Worker Agent  │  │  Worker Agent  │  │  Worker Agent       │
│  (OpenDB)    │  │  (OpenDB)    │  │  (OpenDB)         │
│              │  │              │  │                    │
│  部署在数据库 │  │  部署在数据库 │  │  部署在数据库      │
│  节点上      │  │  节点上       │  │  节点上            │
│              │  │              │  │                    │
│  独立调用LLM │  │  独立调用LLM │  │  独立调用LLM       │
│  本地skill   │  │  本地skill   │  │  本地skill         │
│  本地规则引擎 │  │  本地规则引擎│  │  本地规则引擎      │
└──────────────┘  └──────────────┘  └────────────────────┘
```

**核心设计原则：**
- Worker Agent = 现有 OpenDB，部署在每个数据库节点上，不变
- Manager Agent = 新增，专门管理舰队
- 两者都可独立调用 LLM，但**角色/skill/提示词/上下文完全不同**
- 节点 Agent 和管理者 Agent 持续信息交互

---

### Q3：通信模式

**问题：** 节点 Agent 和 Manager Agent 之间如何通信？

| 方案 | 模式 | 特点 |
|------|------|------|
| A 推送优先 | 节点主动上报 | 简单，Manager 被动 |
| B 拉取优先 | Manager 定期轮询 | Manager 主导，轮询开销大 |
| **C 双向流** | **gRPC 双向 streaming** | **实时性最好** |
| D 事件总线 | NATS/Kafka 中间件 | 最灵活，但引入外部依赖 |

**决策：C 方案 — gRPC 双向流，混合推拉模式**

具体行为：
1. **紧急上报（推）：** 节点 Agent 遇到紧急问题，本地处理完成后立即上报 Manager Agent
2. **定期拉取（拉）：** Manager Agent 定期主动抓取节点 Agent 的状态信息
3. **全局报告：** 由 Manager Agent 统一生成全局管理报告

---

### Q4：三层架构 — Manager Agent + Memory Agent + Worker Agent

**问题：** Manager Agent 的部署形态和管理范围？

**决策：三层分级架构**

```
┌──────────────────────────────────────────────────────────────────┐
│                        Manager Agent                                │
│  - 跨区域趋势分析、全局策略下发、舰队健康总报                      │
│  - 统一规则/黑名单管理、容量规划                                   │
└──────┬──────────────────┬──────────────────┬────────────────────┘
       │                  │                  │
┌──────▼──────┐   ┌───────▼──────┐   ┌──────▼───────┐
│ Memory Agent │   │ Memory Agent │   │ Memory Agent │
│ ≤200 节点    │   │ ≤200 节点    │   │ ≤200 节点    │
│ 区域巡检     │   │ 区域巡检     │   │ 区域巡检      │
│ 跨节点协调   │   │ 跨节点协调   │   │ 跨节点协调    │
│ 区域报告     │   │ 区域报告     │   │ 区域报告      │
└──┬───┬───┬──┘   └──┬───┬───┬──┘   └──┬───┬───┬───┘
   │   │   │         │   │   │         │   │   │
   ▼   ▼   ▼         ▼   ▼   ▼         ▼   ▼   ▼
  Node Node Node    Node Node Node    Node Node Node
  Agent Agent Agent Agent Agent Agent Agent Agent Agent
  (OpenDB×N)        (OpenDB×N)        (OpenDB×N)
```

#### Memory Agent 节点上限：200（默认，可配置）

**瓶颈分析 — 真正瓶颈是 LLM 上下文窗口，不是 gRPC 连接数：**

| 维度 | 100 节点 | 200 节点 | 500 节点 | 1000 节点 |
|------|---------|---------|---------|----------|
| gRPC 长连接内存 | ~5MB | ~10MB | ~25MB | ~50MB |
| 心跳处理 (30s/次) | 3.3 msg/s | 6.7 msg/s | 16.7 msg/s | 33.3 msg/s |
| 指标拉取 (60s/次) | 80 set/s | 160 set/s | 400 set/s | 800 set/s |
| 以上均不是瓶颈 | ✓ | ✓ | ✓ | ✓ |
| **故障风暴 (10%同时异常)** | 10 节点 | 20 节点 | 50 节点 | 100 节点 |
| **LLM 上下文** (~3K token/节点) | 30K | 60K | 150K | 300K |
| **LLM 可处理?** | 轻松 | 可以 | 吃力 | 溢出 |

**瓶颈公式：**
```
故障风暴时的并发异常节点数 × 每节点上报信息量 ≤ LLM 单次上下文容量
```

**按数据库类型的建议上限：**

| 场景 | 建议上限 | 理由 |
|------|---------|------|
| Oracle (重型) | 100-150 | 每节点信息量大（SGA/PGA/等待事件/SQL明细），3-5K token |
| MySQL/PG (轻型) | 150-200 | 每节点信息量相对少，2-3K token |
| 混合管理 | 150 | 取中间值 |

**三级负载下的降级策略：**
```
正常态:   200 节点 × 5% 异常 = 10 节点  → 逐节点 LLM 分析       ✓
��暴态:   200 节点 × 20% 异常 = 40 节点  → 按模式聚合后分析      ✓
灾难态:   200 节点 × 50% 异常 = 100 节点 → 降级为分类统计        ✓
```

---

## 三、业界参考架构

### 3.1 类似的 Worker Agent + Central Manager 产品

| 产品 | 节点 Agent | 管理者 | 通信 | 自治程度 |
|------|-----------|--------|------|----------|
| **Kubernetes** | kubelet | controller-manager | API Server | 节点自愈 + 集群编排 |
| **Oracle EM** | EM Agent | OMS | HTTPS | 监控为主 |
| **Consul** | Client Agent | Server Agent | Gossip+RPC | 服务发现自治 |
| **Datadog** | dd-agent | 中央平台 | HTTPS 推送 | 监控告警 |
| **Vitess** | VTTablet | VTGate+Orchestrator | gRPC | 拓扑自动切换 |
| **CockroachDB** | 对等节点 | 无中心(Raft) | Gossip+Raft | 完全自治 |
| **Claude Code** | Lead Agent | Teammate Agents | 进程内路由 | 各自独立调 LLM |

**关键发现：** 业界有大量成熟的 Node+Manager 模式，但**没有一个是 AI-native 的**。OpenDB 的双层 AI Agent 架构在数据库领域是全新定位。

### 3.2 Claude Code 源码的架构启示

| Claude Code 模式 | OpenDB 映射 |
|-----------------|-------------|
| **Lead + Teammate** 多 agent 协作 | Manager Agent + Worker Agent |
| **Kairos** 定时调度 + 确定性抖动 | 节点巡检调度 + 避免雷群效应 |
| **Agent Loop** (while(true) 主循环) | 节点持续 sense→diagnose→act 循环 |
| **Context Injection** 8层上下文丰富 | 节点本地上下文 vs 管理者全局上下文 |
| **Feature Gates** 多级门控 | 操作安全级别 + 危险操作黑名单 |
| **isMeta 消息** 模型可见用户不可见 | Agent 间内部通信 vs 对 DBA 展示 |
| **Compression/Trimming** 4种压缩 | 长期运行的上下文管理 |
| **Tool Schema 动态生成** | 节点 vs 管理者不同的 tool 集 |
| **Prompt Cache** 系统提示词缓存 | 高频 LLM 调用的成本优化 |

---

## 四、已识别的架构风险

| 风险 | 说明 | 需要设计的对策 |
|------|------|---------------|
| **脑裂** | 节点自主决策 vs Manager 下发指令冲突 | 优先级仲裁机制 |
| **信息爆炸** | 1000 节点同时上报异常 | Manager 的上下文压缩 + 聚合 |
| **跨节点影响** | 节点 A 修复可能影响节点 B（如主从切换） | Manager 全局锁/协调 |
| **LLM 成本** | 1000 节点各自调 LLM | 分层策略：规则引擎优先，LLM 兜底 |
| **网络分区** | Manager 不可达时节点行为 | 节点降级独立运行（L2-L3） |

---

### Q5：三层职责边界

**问题：** 三层之间的职责如何划分？

**决策：严格分层，各司其职**

| 职责 | Worker Agent | Memory Agent | Manager Agent |
|------|-----------|-------------|-----------|
| **实时监控** | 48 指标采集 + 异常检测 | — | — |
| **本地诊断** | 规则引擎 + LLM（单机视角） | — | — |
| **本地修复** | 自主执行（受黑名单约束） | — | — |
| **紧急上报** | → Memory Agent | — | — |
| **区域巡检** | — | 定期拉取节点状态 | — |
| **跨节点关联** | — | 区域内主从/集群分析 | — |
| **区域报告** | — | 区域健康报告 | — |
| **区域协调** | — | 主从切换等跨节点操作 | — |
| **全局态势** | — | — | 跨区域趋势分析 |
| **全局策略** | — | — | 统一规则/黑名单下发 |
| **全局报告** | — | — | 舰队健康总报 |
| **容量规划** | — | — | 跨区域资源调度建议 |

**信息流向：**
```
Worker Agent ──紧急上报──► Memory Agent ──聚合上报──► Manager Agent
Worker Agent ◄──指令下发── Memory Agent ◄──策略下发── Manager Agent
           ◄──定期拉取──              ◄──定期拉取──
```

---

### Q6：Worker Agent 运行形态 — Daemon 化改造

**问题：** 当前 OpenDB 是 login 后才触发功能，要承担 Worker Agent 必须成为 Daemon 进程。CLI 模式和 Daemon 模式的关系如何？

**核心矛盾：** 当前所有功能的生命周期绑定在用户终端会话上，用户退出则一切终止。

**决策：B 方案 — CLI 连接 Daemon（类似 Docker 模式）**

| 方案 | 说明 | 优劣 |
|------|------|------|
| A 完全独立 | daemon 和 CLI 各自独立，互不干扰 | 简单，但两进程同时操作数据库有冲突风险 |
| **B CLI 连接 daemon** | **daemon 常驻，CLI login 后连接本地 daemon，共享状态** | **更优雅，无冲突，类似 Docker** |

**同一二进制，双模式运行：**

```
opendb（单二进制）
  │
  ├── opendb agent start       ← Daemon 模式（Worker Agent）
  │   └── 后台常驻进程
  │       ├── Sentinel 持续探测
  │       ├── 自主决策循环 (sense���diagnose→act)
  │       ├── gRPC Server（供Memory Agent 连接）
  │       └── Unix Socket / TCP（供本地 CLI 连接）
  │
  └── opendb login             ← CLI 模式（连接本地 daemon）
      └── REPL 交互
          ├── 读取 daemon 的实时状态
          ├── 手动执行诊断/修复
          └── 查看 daemon 的操作日志
```

**业界参考：**

| 产品 | Daemon | CLI | 通信方式 |
|------|--------|-----|----------|
| Docker | `dockerd` | `docker` | Unix Socket |
| Consul | `consul agent` | `consul members` | HTTP API |
| containerd | `containerd` | `ctr` | gRPC |
| **OpenDB** | **`opendb agent start`** | **`opendb login`** | **Unix Socket / gRPC** |

**对现有代码的影响：**

| 模块 | 当前状态 | Daemon 化改造 |
|------|---------|--------------|
| `cmd/opendb/main.go` | 直接启动 REPL | 新增 `agent` 子命令分支 |
| `internal/ui/repl.go` | 独立运行 | 改为连接 daemon，读取共享状态 |
| `internal/oracle/sentinel/` | login 后启动 | 由 daemon 管理生命周期 |
| `internal/scheduler/` | 同上 | 同上 |
| `internal/connection/manager.go` | 交互式选择连接 | 配置文件指定，daemon 自动认证 |
| **新增 `internal/agent/`** | — | Daemon 核心：生命周期、自主循环、PID 管理 |
| **新增 `internal/grpc/`** | — | 对外 gRPC Server + 对内 Unix Socket |

---

### Q6.1：诊断优先级调整

**问题：** Sentinel 触发异常后，诊断的调用顺序？

**决策：LLM 优先，Rule Engine 兜底**

```
原方案（已否决）：
  Sentinel 异常 → Rule Engine → 规则无法解决 → LLM

新方案（已确认）：
  Sentinel 异常 → LLM 诊断（优先） → LLM 不可用时 → Rule Engine 兜底
```

**自主决策循环（修正版）：**

```
┌────��────────────────────────────────────────────────┐
│            Autonomy Loop (永续循环)                    │
│                                                      │
│  ┌──────────┐                                        │
│  │ Sentinel │──── 异常! ───►  LLM 可用?              │
│  │ 48指标   │                 ╱       ╲              │
│  │ 持续探测 │               Yes        No            │
│  └──────────┘                │          │            │
│                              ▼          ▼            │
│                      ┌──────────┐  ┌──────────┐     │
│                      │ LLM 诊断 │  │Rule Engine│     │
│                      │ 链式推理  │  │ 273+ 规则 ��     │
│                      │ 最多20轮  │  │ 确定性诊断│     │
│                      └─────��────┘  └─────┬���───┘     │
│                            │             │           │
│                            ▼             ▼           │
│                      ┌──────────────────────┐        │
│                      │   生成修复方案         │        │
│                      └──────────┬───────────┘        │
│                                 │                    │
│                                 ▼                    │
│                      ┌──────────────────────┐        │
│                      │   黑名单检查          │        ��
│                      │   命中黑名单? → 拒绝   │        │
│                      │   未命中? → 执行修复   │        │
│                      └────────���─┬───────────┘        │
│                                 │                    │
│                                 ▼                    │
│                      ┌──────────────────────┐        │
│                      │   执行 + 验证结果     │        │
│                      │   记录操作日志        │        │
���                      │   上报Memory Agent    │        │
│                      └──────────────────────┘        │
└────────────────────────────────────────���────────────┘
```

**LLM 优先的理由：**
- LLM 具备上下文推理能力，能关联多维度信息发现根因
- Rule Engine 是确定性匹配，覆盖范围有限
- LLM 不可用时（网络故障/API 限流/成本控制），Rule Engine 提供可靠兜底
- 这符合"AI-native"的产品定位

**LLM 不可用的判定：**
- 网络不可达
- API 返回错误（限流/超时/服务端错误）
- 配置中未配置 LLM provider
- 手动关闭 LLM（`llm.enabled: false`）

---

## 五、待讨论问题

- [x] Q1：自治级别 → L4 + 自定义危险操作黑名单
- [x] Q2：系统架构 → 双层 Agent（Node + Manager）
- [x] Q3：通信模式 → gRPC 双向流，混合推拉
- [x] Q4：部署形态 → 三层架构（Manager Agent → Memory Agent → Worker Agent），区域上限 200
- [x] Q5：职责边界 → 严格分层，Node 本地自治，Memory Agent 跨节点协调，Manager Agent 全局策略
- [x] Q6：Worker Agent 形态 → Daemon 化，CLI 连接 daemon 共享状态（Docker 模式）
- [x] Q6.1：诊断优先级 → LLM 优先，Rule Engine 兜底（LLM 不可用时降级）
- [x] Q7：危险操作黑名单 → 分层配置，Manager Agent 底线 + 区域/节点只能追加
- [x] Q8：LLM 策略 → 统一模型，差异化提示词/Skill（避免模型差异产生行为差异）
- [x] Q9：报告体系 → Node 明细 + Manager Agent 汇总，明细全量上传，可配置频率，定时+即时
- [ ] Q10：Manager Agent 技术栈（Go？复用 OpenDB 代码？独立 repo？）
- [ ] Q11：CLI 连接 daemon 的通信协议（Unix Socket / TCP / gRPC）

---

### Q7：危险操作黑名单配置方式

**问题：** 黑名单如何配置和管理？

**决策：分层配置，Manager Agent 设底线，区域/节点只能追加、不能放宽**

```
Manager Agent 底线（全局强制）：
  ├── DROP TABLE
  ├── TRUNCATE
  ├── SHUTDOWN
  └── DROP DATABASE
       │
       ▼  只能追加，不能移除
Memory Agent 追加（区域级别）：
  ├── 区域 B 追加：ALTER SYSTEM SWITCH LOGFILE
  └── 区域 D 追加：DROP INDEX
       │
       ▼  只能追加，不能移除
Worker Agent 追加（节点级别）：
  └── 某核心节点追加：ALTER SYSTEM KILL SESSION
```

**合并规则：** 节点实际黑名单 = 总底线 ∪ 区域追加 ∪ 节点追加

---

### Q8：LLM 调用策略

**问题：** 三层各用什么模型？

**决策：统一使用同一模型，保持一致**

- 所有层级（Node / Memory Agent / Manager Agent）使用完全相同的 LLM 模型
- **核心理由：避免模型差异产生行为差异**
- 不同层级的区别体现在 **提示词（System Prompt）和 Skill/Tools**，而非模型本身
- 模型在配置文件中统一指定，一处修改全局生效

| 层级 | 模型 | 提示词角色 | Skill/Tools |
|------|------|-----------|-------------|
| Worker Agent | 统一模型 | "你是数据库节点自治代理..." | query_database, kill_session, execute_repair... |
| Memory Agent | 统一模型 | "你是区域管理代理..." | broadcast_command, get_topology, coordinate_failover... |
| Manager Agent | 统一模型 | "你是全局舰队管理代理..." | get_all_regions, push_policy, generate_fleet_report... |

---

### Q9：全局报告体系

**问题：** 报告的内容、频率和层级关系？

**决策：Node 出明细，Manager Agent 出汇总，明细全量上传**

**频率：可配置，默认每日 + 每周**

| 频率 | 生成者 | 内容 |
|------|--------|------|
| 即时 | Memory Agent / Manager Agent | 重大事件即时报告（主从切换、节点宕机等） |
| 每日 | Memory Agent + Manager Agent | 区域日报 + 全局日报 |
| 每周 | Manager Agent | 全局周报（趋势分析、容量规划建议） |

**触发方式：定时 + 重大事件即时报告**

**报告层级：**

```
Worker Agent：
  ├── 每次故障生成节点明细故障报告
  ├── 明细报告完整内容上传给Memory Agent
  └── Memory Agent 转发给Manager Agent

Memory Agent：
  ├── 聚合区域内所有节点明细 → 生成区域报告
  └── 区域报告 + 全部节点明细上报Manager Agent

Manager Agent：
  ├── 聚合所有区域报告 → 生成全局汇总报告
  ├── 可查看任意节点的明细故障报告（全量存储）
  └── 汇总报告由 LLM 生成自然语言总结 + 趋势判断 + 建议
```

**Manager Agent 可查看的报告：**
- 全局汇总报告（自己生成）
- 4 份区域报告（Memory Agent 生成）
- 1200 份节点明细报告（Worker Agent 生成，全量上传）
- 支持下钻：全局 → 区域 → 节点明细

---

### 24 小时全场景推演

已完成完整的 7×24 场景推演，覆盖 8 个核心场景，详见：
`project_opendb_autopilot_24h_scenario.md`

| # | 场景 | 处理层级 | 验证结论 |
|---|------|---------|----------|
| 1 | 常态巡航 | Node Sentinel | LLM 零调用，纯本地检测 |
| 2 | 单节点故障 | Node 自主修复 | 8 秒完成，2 轮 LLM |
| 3 | 多节点关联故障 | Memory Agent 协调 | 体现三层价值：单节点看不到全貌 |
| 4 | 每日巡检 | 三层协同 | 抖动调度避免雷群 |
| 5 | DBA 手动登录 | CLI 连接 Daemon | 不影响自主循环 |
| 6 | 网络分区 | 各层独立降级 | 最差情况仍有 Rule Engine 兜底 |
| 7 | 预防性维护 | Cerebrate→Overlord→Drone | Manager Agent 决策，Worker Agent 执行 |
| 8 | 黑名单拦截 | Worker Agent | LLM 自动重新生成替代方案 |
