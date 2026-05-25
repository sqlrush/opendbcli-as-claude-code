# OpenDB Autopilot — L4 差距分析

> 基于已完成的 Q1-Q9 架构决策和 7×24 场景推演，识别达成 L4 完全自治所需的补充设计项
> 日期：2026-04-08 | 更新：2026-04-09

---

## 零、已回答的补充决策

### Q10：Manager Agent 技术栈 ✅

**决策：全部整合到 OpenDB 单二进制，通过 role 参数区分角色**

- 同一个 `opendb` 二进制，配置 `role: manager / memory / node` 启动不同角色
- 不同 role 激活不同子系统（node 启动 Sentinel/Rule Engine/DB 连接，memory 启动区域协调，manager 启动全局策略）
- 三层都支持 CLI 接入（`opendb login`），各自有角色专属 `/` 命令
- Cerebrate 额外提供 Web UI，用于浏览器查看汇总报告
- 业界参考：Consul（`-server` / `-client`）、Nomad（server/client config）

### Q11：CLI-daemon 通信协议 ✅

**决策：gRPC 统一协议，双传输层**

- 本地 CLI ↔ Daemon：gRPC over Unix Socket（零端口冲突，文件权限认证）
- 远程 CLI ↔ Daemon/Overlord/Cerebrate：gRPC over TCP
- Overlord ↔ Drone / Cerebrate ↔ Overlord：gRPC over TCP（与 Q3 一致）
- 一套 proto 定义，一套服务接口，两种传输层
- 业界参考：containerd（gRPC over Unix Socket 本地 + TCP 远程）

### Q12：Agent 注册、发现与身份认证 ✅

**决策：静态配置 + Join Token（kubeadm 模式），两阶段认证**

注册流程：
```
opendb cluster init --role manager                              # ① 初始化 Cerebrate
opendb cluster join --role memory --cerebrate <addr> --token .. # ② 加入 Overlord
opendb cluster join --role node --overlord <addr> --token ..    # ③ 加入 Drone
```

认证：
- 初始加入：Join Token（一次性/限时，24h 过期）
- 运行时：mTLS 证书（Join 成功后 Cerebrate 签发）

首次部署策略（V1 三种方式并存）：
1. **Pull 安装**（不开 22）：Cerebrate 内置 HTTP 文件服务，节点执行 `curl http://cerebrate/install | sh` 拉取二进制 + 配置 + 启动
2. **适配 Ansible**（开了 22，用户想用通用工具）：提供标准 Ansible playbook + inventory 模板
3. **内置批量部署**（开了 22，用户想在 opendb 内完成）：`opendb cluster deploy --inventory inventory.yaml --binary ./opendb`，内置类 Ansible 的 SSH 批量分发能力

后续升级：gRPC 层级分发（Cerebrate → Overlord → Drone），不依赖 SSH

### Q14：状态持久化与崩溃恢复 ✅

**决策：memory/policy 增量同步到 Overlord，崩溃后由 Overlord 推回**

- memory = OpenDB 现有 memory 系统（5 种类型：incident/solution/preference/workload/pattern，存储在 `~/.opendb/memory/{instance}/`）
- policy = OpenDB 现有 4 级 policy 系统（Platform → Org → Instance → Session）
- 同步频率：每分钟，增量
- 崩溃丢失策略：如果没有保存到记忆文件或没有同步到 Overlord，默认丢失，不做复杂恢复
- Overlord 之间互相备份 memory/policy 数据

残留待定：正在执行的修复操作崩溃了怎么办（Q14.1）

### Q17：7×24 上下文管理 ✅

**决策：复用 OpenDB Engine V2 的完整上下文管理机制**

- 短期记忆：session（jsonl 文件），会话级上下文
- 长期记忆：memory 文件（跨会话的故障经验沉淀）
- 实例画像：PROFILE.md 每次全文加载
- 压缩：3 层（Turn Collapse → Auto Summary → Emergency Truncate）
- memory/session 共享 10GB 配额，policy 不计入配额
- 通过每分钟增量同步到 Overlord 实现跨节点持久化

---

## 一、必须设计（没有这些不可能实现 L4）

### Q13：Cerebrate / Overlord 高可用

当前缺失：Cerebrate 是单点，宕机后全局态势丧失；Overlord 宕机后区域协调丧失。

降级矩阵说明了"能降级"，但没回答"如何恢复"：
- Cerebrate 是否需要主备？还是接受短时间不可用？
- Overlord 是否需要主备？节点自动漂移到其他 Overlord？
- 故障转移是自动的还是人工恢复？
- 状态（事件日志、时序数据、待执行任务）如何在主备间同步？

---

### Q14：状态持久化与崩溃恢复

当前缺失：daemon 7×24 运行必然会遇到进程崩溃、机器重启。

需要回答的问题：
- Drone daemon 崩溃重启后如何恢复？正在执行的修复操作怎么办？
- 操作幂等性保证：崩溃后重启会不会重复执行修复？
- 事件日志存在哪里？（内存？SQLite？文件？）
- Overlord 的时序记录（SQLite）是否需要备份？
- 网络恢复后"批量上报缓存事件"——缓存在哪里？上限多大？

---

### Q15：脑裂仲裁机制

风险表中标了"需要设计的对策"，但没有给出方案。

具体场景：
- Drone 刚执行了 kill session，Overlord 随后下发指令要求保持该 session（因为是跨节点事务）
- Overlord 下发主从切换指令，Drone 同时在自主修复主库——两个操作冲突
- 需要设计：操作锁、优先级仲裁（上级指令 > 本地自治？）、操作前检查机制

---

### Q16：跨节点操作协调协议

场景 3 展示了"批量下发 I/O 限流"，但没设计更复杂的跨节点操作：
- **主从切换**的完整流程：停写→检查同步→切换→验证→更新拓扑，任一步失败如何回滚？
- **RAC 节点操作**：在一个节点做操作时如何锁定其他节点不做冲突操作？
- 分布式锁的实现方式
- 跨节点操作的事务性保证（saga 模式？两阶段提交？）

---

### Q17：7×24 上下文管理

当前 LLM 调用是无状态的（每次异常独立调用），但 L4 需要：
- **短期记忆**：最近 N 次故障的上下文（避免重复诊断相同问题、识别反复发生的问题）
- **长期知识**：历史故障模式沉淀（"这个节点上个月也出过类似问题，根因是..."）
- 上下文窗口管理：7×24 运行数月，如何压缩/归档/轮转历史上下文？
- 是否需要向量数据库做故障知识检索？

---

## 二、应该设计（没有这些 L4 可以跑但不完善）

### Q18：数据库拓扑感知与自动发现

场景推演中的拓扑信息（RAC 集群、主从关系、共享存储）从哪来？
- 主从关系是手动配置还是自动发现？
- 拓扑变更（手动切换了主从）如何自动感知和更新？
- 存储拓扑（哪些节点共享同一 SAN）如何获取？
- 这些信息存在哪一层？Drone 知道自己的主从关系？还是只有 Overlord 知道？

---

### Q19：通知与告警通道

场景中提到"推送通知"但没有设计：
- 即时报告通过什么通道送达 DBA？（邮件/短信/企微/钉钉/飞书/Webhook？）
- 告警分级：什么级别走什么通道？
- 告警升级机制：如果 30 分钟无人确认，自动升级到二线？
- 告警抑制：避免同一问题重复告警（如存储问题的 12 个节点不发 12 条）

---

### Q20：LLM Tool/Function 的具体定义

Q8 给出了 Tool 的名称列表，但没有定义：
- 每个 Tool 的输入参数和输出格式（JSON Schema）
- Tool 的权限等级（哪些 Tool 是只读诊断，哪些是写操作）
- Tool 执行超时和重试策略
- 三层各自的 System Prompt 具体内容
- LLM 结果的校验机制（LLM 说 kill session 472，如何验证 472 确实存在且是问题根源？）

---

### Q21：可观测性与自监控

Agent 自身也是需要监控的：
- Agent 进程的健康指标（CPU/内存/goroutine 数/GC 压力）
- LLM 调用指标（延迟/成功率/token 消耗/成本）
- 修复操作统计（成功率/误判率/平均修复耗时）
- gRPC 连接状态
- 是否对外暴露 Prometheus metrics 或类似端点？
- Agent 的"自监控"：谁来监控监控者？

---

### Q22：配置管理与热更新

7×24 运行意味着不能随便停服更新配置：
- 黑名单的热更新流程（Cerebrate 更新 → 如何推送到所有 Drone？）
- 规则引擎规则的动态更新
- LLM provider/model 切换是否需要停服？
- System Prompt 的版本管理
- 配置格式（YAML？TOML？）和分层覆盖规则

---

### Q23：LLM 成本控制

99 次/天/1200 节点看起来很低，但需要设计防失控机制：
- 预算上限（每天/每月最大 LLM 调用次数或 token 数）
- 按层级/区域的配额分配
- 异常检测：如果某节点一天调了 50 次 LLM 是否告警？
- 成本异常时的降级策略（自动切换到 Rule Engine only 模式？）

---

### Q24：变更审计与合规

L4 自动执行操作后，审计追踪不可少：
- 每一条自动执行的 SQL 需要完整审计（谁决策、何时、为什么、执行结果）
- 审计日志不可篡改（append-only）
- 审计日志的保留策略
- 是否需要满足合规要求（SOX/等保？）
- 操作回放能力（能否重放某次故障的完整诊断过程？）

---

## 三、可以后续补充（V2+ 迭代）

### Q25：部署与升级策略
- Agent 的批量分发（类似 Ansible/SaltStack？还是自带升级能力？）
- 滚动升级策略（先升级一个区域的 Drone，验证通过后扩大？）
- 版本兼容性（新版 Overlord + 旧版 Drone 能否共存？）

### Q26：故障经验学习
- 从历史故障中自动提取模式
- 新规则的自动生成（LLM 诊断过的问题 → 沉淀为 Rule Engine 规则？）
- 故障预测（基于趋势分析预测未来可能的故障）

### Q27：混沌工程与测试策略
- 如何测试 L4 自治行为的正确性？
- 故障注入框架（模拟各种故障验证 Agent 反应）
- LLM 响应的确定性测试（mock LLM？）

### Q28：多租户与多集群
- 是否支持多个独立的 Cerebrate 管理不同业务线？
- 租户间的隔离

---

## 四、优先级排序

### 已回答 ✅

| 编号 | 问题 | 决策 |
|------|------|------|
| Q10 | Manager Agent 技术栈 | 单二进制 + role 参数，三层都支持 CLI，Cerebrate 额外提供 Web UI |
| Q11 | CLI-daemon 通信协议 | gRPC 统一，本地 Unix Socket + 远程 TCP |
| Q12 | 注册发现与认证 | kubeadm 模式，Join Token → mTLS；部署：Pull 安装 + 适配 Ansible + 内置批量部署 |
| Q13 | Cerebrate/Overlord 高可用 | 每个角色 2 份数据镜像，Overlord 互备，Cerebrate 是特殊 Overlord，宕机后备份 Overlord 升级 |
| Q14 | 状态持久化 | memory/policy 每分钟增量同步到 Overlord，崩溃后推回，未保存的默认丢失 |
| Q14.1 | 修复操作崩溃处理 | 启动后第一次"诊断"即恢复，扫描记忆+数据库日志，能恢复就恢复，否则放弃 |
| Q15 | 脑裂仲裁 | 人 > 上级 LLM > 本地 LLM；跨节点问题 Worker 不碰，上交 Overlord |
| Q16 | 跨节点操作协调 | Worker 只处理单节点，跨节点一律上交 Overlord；Overlord 直连数据库操作，天然串行 |
| Q17 | 上下文管理 | 复用 OpenDB Engine V2（session + memory + 3 层压缩） |
| Q18 | 拓扑感知 | Worker 自动发现（DB层+OS层），Overlord 汇聚区域拓扑，Cerebrate 全局展示 |
| Q19 | 通知告警通道 | V1 不做主动推送，做 Cerebrate Web 监控大盘（拓扑+健康+报告统一入口） |
| Q20 | Tool 定义 + 安全控制 | 新增 14 个 Tool，复用 90+ 现有 skill；变更类 Tool 4 级控制（enabled/confirm/disabled/hidden），通信/记忆/报告不受限 |
| Q21 | 可观测性 | 层级互相监控（Standby→Cerebrate→Overlord→Worker），复用 gRPC 心跳，大盘展示 |
| Q22 | 配置热更新 | Cerebrate 是配置权威源，gRPC 层级推送，内存原子替换，不停服 |
| Q23 | LLM 成本控制 | V1 待办，方案已设计（三层限额+冷却+降级到 Rule Engine） |
| Q24 | 变更审计 | V1 做极简审计日志（写操作 append-only，随 memory 同步到 Overlord） |
| Q25 | 部署与升级 | 新版必须兼容旧版；回滚顺序 Worker→Overlord→Cerebrate |
| Q26 | 故障经验学习 | 修复成功后自动沉淀为 Rule 文件，frontmatter trigger 自动索引，按需匹配加载 |

| Q27 | 混沌工程与测试 | 三层测试（故障注入+LLM录制回放+集群混沌）+ 无人值守 CI/CD 流水线 |
| Q28 | 多租户与多集群 | V1 不做，先支持单集群 |

### 全部已回答 ✅ (Q1-Q28)
