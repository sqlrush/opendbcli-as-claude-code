# OpenGauss 能力优化专项

> 状态：**规划中，待对齐后开工**
> 启动日期：待定
> 预计工期：5-6 周
> 目标里程碑：v1.2.0（minor bump，代表 OG 能力达标）

---

## 1. 北极星目标

**让 OpenDB 对 OpenGauss 的诊断能力达到或超过当前对 Oracle 的支持水平。**

"达到或超过"的可量化含义：

| 维度 | Oracle 基线 | OG 目标 |
|---|---|---|
| 规则 JSON 场景覆盖 | 176 | **≥ 200**（含 PG/OG 特有场景） |
| SQL Advisor 模块 | ✅ 完整（analyzers + collector + plan_parser） | ✅ 对等实现 |
| Profile 提示词密度 | 126 行（等待事件速查/ORA 错误/参数/对象引用） | **≥ 150 行**（MVCC/XID/VACUUM/WAL/LWLock/gs_ 特有） |
| Skill 质量 | 38 个，均有单测+真机验证 | 46 个，完成质量审计，至少 40 个达 Oracle 同级 |
| 诊断四层策略 | 告警主线→关联→当前对比→综合排名 | 完整实现 |
| 模型自主诊断（large，10 轮） | 稳定可用 | 稳定可用 |
| 主流模型能力评估 | 已有 49 条实测≥70 分 23 个 | 独立评估，≥ Oracle 的 case 通过率 |

---

## 2. 当前基线差距（2026-04-23 扫描）

```
维度                Oracle    OG       缺口
──────────────────────────────────────────────
Rule JSON 场景      176       48       -128（OG 只有 Oracle 的 27%）⭐️
Rule Go 文件        32        21       -11
Profile 提示词     126 行    46 行    -80 行
Skill 数量          38        46       OG +8（但质量未审计，存疑）
子系统              8         7        缺 sqladvisor
  - agent          ✅        ✅
  - driver         ✅        ✅
  - monitor        ✅        ✅
  - register.go    ✅        ✅
  - ruleengine     ✅        ✅
  - sentinel       ✅        ✅
  - skill          ✅        ✅
  - sqladvisor     ✅        ❌ ← 完全缺失
成熟度（memory）    主线      60%
```

**关键发现**：
- OG Skill 数 46 > Oracle 38 是 **misleading** —— OG 的 Skill 多数是从 PG shell 过来的，未经真机验证和打磨
- OG Profile 文件仅 46 行，且 ToolUsageHint 直接 reuse PG 的，没有 OG 特色
- 规则场景只有 Oracle 的 27%，rule engine 兜底能力几乎为零
- 完全没有 SQL Advisor 模块

---

## 3. 专项子项拆分

### P0 地基（1 周）

#### ① Profile 提示词密度拉齐（3 天）

**现状**：46 行，20 行实质内容，ToolUsageHint 是 PG 的。
**目标**：150+ 行，OG 专属领域知识注入 LLM。

内容清单：
- [ ] 等待事件速查表（LWLock 族、Lock 族、IO 族、IPC 族、Client 族、BufferPin、Timeout，每族 5-8 条）
- [ ] MVCC/XID 专属知识（xid_age、wraparound 风险线、VACUUM 阻塞源、long-running transaction 定位）
- [ ] WAL 专属知识（WAL 写冲高的 3 类根因、同步复制延迟、checkpoint 过频）
- [ ] 死元组与 bloat 诊断（pg_stat_all_tables、pgstattuple、何时触发 VACUUM FULL）
- [ ] 连接突发（pg_stat_activity + backend_start + idle in transaction）
- [ ] gs_ 系列视图专属（gs_session_stat、gs_sql_count、gs_wlm_session_info、gs_asp）
- [ ] WDR 报告（OG 特有，类比 Oracle AWR）
- [ ] 对象引用规则（和 PG 一致但强调引用前 `/sql` 验证）
- [ ] 措辞规范（禁"风暴/瓶颈/抖动"，用"冲高"）
- [ ] ToolUsageHint 覆盖 OG 所有 skill，用 OG 语境重写（不 reuse PG）

**验收**：
- `wc -l internal/engine/profile/opengauss.go` ≥ 150
- 手工 review：每类知识都有"典型根因"而不是只有"定义"

#### ② 规范管理（1-2 天）

**目标**：和 Oracle profile 同级的数据验证规则 + 输出纪律。

内容清单：
- [ ] 数据验证规则表（每类结论数据对应必须调用的工具 —— 如 dead tuples 必查 `segments`，xid_age 必查 `sql + SELECT datname, age(datfrozenxid)`）
- [ ] 修复 SQL 给出前必须 `/sql` 验证对象
- [ ] 引用表名前必须查 pg_class
- [ ] 结论必须附来源工具（受 v1.1.x 证据校验约束）
- [ ] LLM 输出格式：原生 SQL 不用 /命令（和 feedback-llm-raw-sql 一致）

**验收**：
- OG profile 包含"数据验证规则"表格（结构同 builder.go:343-358）
- 运行时证据校验 `validateEvidenceSources` 在 OG 场景也能正确触发（跑一个人工构造的"编造"case）

---

### P1 核心能力（2 周）

#### ③ Rule JSON 场景补齐：48 → 200+（5-7 天）⭐️ 最大工作量

**现状**：OG 48 条规则（基本是 PG 48 条的同步）。
**目标**：200+ 场景，覆盖 Oracle 176 + OG 特有 30+ 场景。

拆分思路：
1. **Oracle 176 场景评估适用性**（2 天）—— 逐条看，标注：可直接迁移/需改写/OG 无对应
2. **PG/OG 特有场景补充**（3 天）：
   - MVCC snapshot too old
   - XID wraparound 预警（xid_age > 阈值）
   - VACUUM 受阻于 long transaction
   - bloat > 30% + 死元组膨胀
   - LWLock:BufferContent 争用
   - WAL 写冲高
   - checkpoint 过频
   - 同步复制延迟
   - idle in transaction 堆积
   - short connection storm
   - parallel worker 过多
   - OG 特有：WDR 快照异常、MOT 内存表问题、CM 集群状态

**规则开发流程**（按 `feedback-ruleengine-workflow.md`）：
1. 先在 `ailinkdb/data` 生成规则数据（AI 审核）
2. 再基于数据生成 `internal/opengauss/ruleengine/rules_json/*.json`
3. 绝不手写规则 Go 代码

**验收**：
- `find internal/opengauss/ruleengine -name "*.json" | wc -l` ≥ 200
- 在测试 OG 实例上构造 20+ 异常场景，规则引擎命中率 ≥ 70%

#### ④ SQL Advisor OG 实现（4-5 天）

**现状**：OG 完全没有。Oracle/PG 都有 `sqladvisor/{advisor,collector,plan_parser,analyzers/*}` 结构。
**目标**：1:1 迁移 PG 的 sqladvisor 到 OG，适配 OG 特有差异。

子任务：
- [ ] `collector.go` —— 从 pg_stat_statements 或 WDR 采集 topsql
- [ ] `plan_parser.go` —— EXPLAIN (ANALYZE, BUFFERS) 结果解析
- [ ] `analyzers/` —— 复用 PG 的 8 个 analyzer（access_path/join/predicate/rewrite/statistics/resource/plan_stability/helpers）
- [ ] `advisor.go` —— 主入口

PG → OG 差异处理：
- pg_stat_statements → 如果不可用，用 gs_sql_count 或 WDR
- EXPLAIN 输出格式基本一致
- MOT 表的 plan 不同（特殊分支）

**验收**：
- OG 实例上 `/sqladvisor TOP_SQL_ID` 能给出 access path / join / predicate / rewrite 建议
- 单测覆盖主要 analyzer
- 和 PG sqladvisor 输出格式一致

---

### P2 Skill 体系补齐 + Engine 对齐（2-2.5 周）

#### ⑤ Skill 清单变更：新增 15 + 审计 6 + 精简 3（10-15 天）⭐️ 扩容至 P1 核心

**现状**：46 个 skill，数量超 Oracle 但部分是从 PG 继承的空壳；Oracle 的若干核心能力（WDR/trace/planhistory/LWLock）完全缺失。
**目标**：新增 15 个、审计 6 个、合并 3 个，总量达约 58 个且全部经真机验证。

##### 5a. 新增 15 个 /命令

**P0（缺了诊断核心不完整，5 个）**

| 命令 | 说明 | 对应 Oracle | 数据源 |
|---|---|---|---|
| `/wdr` | WDR 快照分析（Workload Diagnosis Report） | `/awr` | `create_wdr_snapshot()` / `generate_wdr_report()` |
| `/planhistory` | 执行计划历史 | `/planhistory` | `dbe_perf.statement_history` / pg_stat_plans |
| `/trace` | OS 堆栈 + 火焰图 | `/trace` | 复用 Oracle trace 实现（OS 层） |
| `/lwlocks` | LWLock 争用 profile | `/latches`（对等） | `pg_stat_activity.wait_event_type='LWLock'` |
| `/autovacuum` | autovacuum worker 详细状态 | 无 | `pg_stat_progress_vacuum` + `pg_stat_all_tables.last_autovacuum` |

**P1（提升诊断深度，5 个）**

| 命令 | 说明 | 数据源 |
|---|---|---|
| `/checkpoint` | Checkpoint 频率 / WAL 写放大 / full_page_writes 影响 | `pg_stat_bgwriter` + `log_checkpoints` |
| `/bgworker` | bgwriter/walwriter/archiver/stats collector 后台进程 | `pg_stat_bgwriter` + `pg_stat_archiver` |
| `/hotkey` | 热点表/行识别 | `pg_stat_all_tables.n_tup_upd` + seq_scan ratio |
| `/logicalslots` | 逻辑复制槽状态（补 `/slots`） | `pg_replication_slots` WHERE slot_type='logical' |
| `/tempusage` | 临时文件监控 | `pg_stat_database.temp_bytes` + `pg_stat_activity.temp_files` |

**P2（OG 独有高级特性，5 个）**

| 命令 | 说明 | 场景 |
|---|---|---|
| `/mot` | MOT（Memory-Optimized Table）状态 | OG 独有，内存表用户 |
| `/cmha` | CM（Cluster Manager）集群健康 | OG 企业版/集群版 |
| `/pubsub` | 发布订阅状态 | 逻辑复制高级 |
| `/toasttable` | TOAST 大字段外联统计 | 宽表分析 |
| `/walsummary` | WAL 文件级 archiver 细节 | 比 `/wal` 细 |

##### 5b. 全量审计 44 个现有 skill（非仅 6 个）

**修正数据**：之前以为 46 个，实际 44 个（`rowval.go` 是辅助函数，`ogerr_kb.go` 是知识库数据文件，都不是独立 skill）。

**核心发现**：**44 个 skill 全部 0 单测**，12 个薄壳 < 60 行，1 个超 800 行上限（`/perfsnap` 775 行接近上限）。

按问题严重度分 4 级：

**[A] 薄壳需扩充（12 个，< 60 行）**

| Skill | 行数 | 风险 | 扩充方向 |
|---|---|---|---|
| `/xid` | 43 | XID wraparound 是 OG 核心诊断点，薄壳不合理 | 加风险线 + VACUUM 阻塞源关联 |
| `/gsmem` | 44 | 内存分析核心能力薄壳 | 拆 shared/process 段 + 对齐 Oracle `/sga+/pga` |
| `/sessions` | 47 | v1.1.05 刚修过口径，但可能缺字段 | 验证并补字段 |
| `/sessionmem` | 50 | 会话内存应更细 | 加 top 排序 + 阈值告警 |
| `/slots` | 54 | 可能只覆盖物理槽 | 加逻辑槽、active/inactive 分离 |
| `/users` | 55 | 只列名缺角色/权限/过期 | 补角色权限 + 密码过期 |
| `/vacuum` | 55 | autovacuum 进度/worker 可能未覆盖 | 关联 progress + worker 数 |
| `/extensions` | 56 | 信息密度低（**合并候选**） | 并入 `/health` 一段 |
| `/locks` | 58 | Advisory Lock 缺 | 加 advisory + 表锁模式 |
| `/respool` | 58 | WLM 深度不够 | 补 cgroup/io_limits |
| `/sqlcount` | 61 | 价值存疑 | 评估合并到 `/topsql` |
| `/indexhealth` | 73 | 只查未使用 | 扩 unusable/bloat/fragmented |

**[B] 中等需审 SQL 口径（23 个，60-230 行）**

`/params` `/backup` `/gather` `/jobs` `/alert` `/space` `/bloat` `/sharedbufs` `/longtx` `/topsql` `/ash` `/ogerr` `/explain` `/slowsql` `/sql` `/waits` `/os` `/segments` `/resource` `/dbtop` `/activesessions` `/wal` `/kill`

审计点：
- SQL 是否用 OG 特有视图（`gs_*`/`dbe_perf.*`/`gs_stat_session`）而非直接 PG 视图
- 输出密度对齐 Oracle 同级
- 阈值/异常提示是否到位
- 中文/无 `<nil>`/无 panic

**[C] 大型需审逻辑正确性（6 个，> 200 行）**

| Skill | 行数 | 审计重点 |
|---|---|---|
| `/perfsnap` | **775** | ⚠️ 接近 800 行上限，审是否需拆文件 |
| `/health` | 454 | 24 项和 Oracle `/OK` 对齐 |
| `/tableinfo` | 276 | 统计信息/索引/约束完整性 |
| `/alter` | 243 | 参数修改安全性（scope/需重启） |
| `/blocktree` | 229 | 阻塞链递归深度/格式 |
| `/explain` | 209 | EXPLAIN 格式适配 OG |

**[D] AI/核心模块（3 个）—— 最高优先级真机验证**

| Skill | 行数 | 审计重点 |
|---|---|---|
| `/llm` | 469 | 证据校验、防幻觉、四层诊断、上下文传递 |
| `/sentinel` | 356 | 异常检测触发、爆发采集、阈值 |
| `/rule` | 322 | 规则引擎覆盖度（规则场景 48→200 后同步验证） |

##### 5c. 合并/精简 3 个（避免冗余）

| Skill | 行动 | 理由 |
|---|---|---|
| `/cancel` | 合并进 `/kill`（加参数 `--mode=cancel|terminate`） | pg_cancel_backend 和 pg_terminate_backend 是一对，统一入口 |
| `/extensions` | 合并进 `/health`（作为一段） | 独立 skill 信息密度低 |
| `/sharedbufs` | 合并进 `/gsmem` | gsmem 已涵盖 shared buffer |

##### 5d. 通用流程（逐个 skill 过）

1. 真机跑 `/xxx` 在 OG 测试实例
2. 检查输出：对齐、无 `<nil>`、无 panic、中文正确、字段完整
3. 对比 Oracle 同名或对等 skill 的输出信息密度
4. 缺字段/口径不对 → 改 SQL
5. 加单测 + 真机验证输出保存 `docs/validation/og-<skill>.md`

##### 5e. 工作量细化

| 子任务 | 量 | 单项 | 合计 |
|---|---|---|---|
| 新增 15 个 skill | 15 | 0.6-0.8 天 | 9-12 天 |
| [A] 薄壳扩充 | 12 | 0.3 天 | 4 天 |
| [B] 中等审 SQL 口径 | 23 | 0.2 天 | 4-5 天 |
| [C] 大型审逻辑 | 6 | 0.5 天 | 3 天 |
| [D] AI/核心真机验证 | 3 | 1 天 | 3 天 |
| 合并 3 个 | 3 | 0.2 天 | 0.5 天 |
| 全量补单测 | 44 + 15 | 0.15 天 | 8-9 天 |
| `/perfsnap` 拆文件（如需） | 1 | 1 天 | 1 天 |
| **子项 ⑤ 合计** | | | **约 32-37 天（5-6 周）** |

**验收**：
- 新增 15 个 skill 全部有单测 + 真机验证输出
- 44 个现有 skill 全部过 audit 并补单测
- 合并 3 个旧 skill 删除，文档和路由更新
- 最终总数约 **44 - 3 + 15 = 56 个** OG skill
- OG skill 单测覆盖率 ≥ 60%

#### ⑥ 上下文管理对齐（3 天）

**现状**：OG 复用 PG profile，Engine 上下文注入可能不完全。
**目标**：和 Oracle Engine 同级。

内容：
- [ ] 跨诊断轮的 session 上下文（v0.9.42 已有基础设施，验证 OG 接入）
- [ ] 工具历史回注（v1.1.x 防幻觉机制，验证 OG 路径）
- [ ] max_turns 配置（assist=10, auto=20，和 Oracle 一致）

**验收**：
- 端到端跑 10 轮 `/llm` 在 OG 上，上下文正确传递
- 跨诊断会话能共享历史（比如第二轮 `/llm` 能引用第一轮的 SQL_ID）

#### ⑦ 记忆 + 画像管理（3 天）

**现状**：v0.9.42 已有 session/memory/PROFILE.md 基础设施。
**目标**：OG 场景下正确工作 + PROFILE.md 增加 OG 段。

内容：
- [ ] Session 持久化在 OG 诊断中生效
- [ ] `/memory` 命令能存 OG 诊断经验
- [ ] PROFILE.md 增加 OG 相关画像字段（常用业务模式、敏感参数、历史踩坑）

**验收**：
- 连续 3 次 OG 诊断能复用前面的上下文
- PROFILE.md 在跑过 OG 诊断后会自动增加相关条目

---

### P3 模型能力评估（1 周）

#### ⑧ 主流模型对 OG 复杂场景的诊断能力矩阵（5-7 天）

**目标**：产出一份公开可引用的 benchmark 报告。

**模型集**（6 个）：
- Claude Opus 4.7（云）
- GPT-5 / GPT-4.5（云）
- Kimi K2.5（云）
- Qwen-Max 3.5（云）
- DeepSeek-R1（云）
- Qwen3 32B Instruct（本地 large）
- Qwen3 9B（本地 small，对照组）

**Capability 配置**：每模型跑 `small`（3轮 Assist）和 `large`（10轮 Auto）两组。

**场景集**（7+ 复杂场景，每个都有"专家标准答案"）：

> **以下场景的标准答案需用户确认后才能作为评分基准**

1. **XID wraparound 逼近 + VACUUM 受阻**
   - 触发：长事务 2h + 高频 UPDATE
   - 期望诊断：定位 pg_stat_activity 中的 long transaction、说明 xid_age 风险线、给出 kill session 或 SET idle_in_transaction_session_timeout 方案
   - 评分：是否识别、是否给 SQL、是否警告 wraparound

2. **LWLock:BufferContent 严重争用**
   - 触发：热点页并发更新（一个小表高频 UPDATE 同一批行）
   - 期望：定位热点页、建议分区或 shared_buffers 调整
   
3. **死元组膨胀 + bloat 40%**
   - 触发：高频 UPDATE 无 VACUUM 数小时
   - 期望：识别 bloat，给 VACUUM 或 pg_repack 方案，说明 VACUUM FULL 的代价

4. **WAL 写冲高 + checkpoint 过频**
   - 触发：大批量 INSERT + max_wal_size 过小
   - 期望：定位 checkpoint 频率、建议 max_wal_size 调整、说明 full_page_writes 影响

5. **并行 worker 过多导致 IO 冲高**
   - 触发：并发大查询 + max_parallel_workers_per_gather=8
   - 期望：识别 parallel worker、建议限制 max_parallel_workers_per_gather 或优化查询

6. **同步复制延迟 + 主库写慢**
   - 触发：standby 网络延迟 + synchronous_commit=on
   - 期望：定位 replication lag、说明 synchronous_commit 配置影响、给 async 切换方案

7. **idle in transaction 堆积**
   - 触发：应用端忘记 commit
   - 期望：识别 idle in tx 会话、建议 idle_in_transaction_session_timeout

8. **short connection storm**
   - 触发：无连接池应用高频 connect/disconnect
   - 期望：定位 backend_start < 1min 的会话数、建议上 PgBouncer

9. **OG 特有：CM 集群状态异常 / MOT 内存溢出**
   - 视 OG 版本

**评分维度**（每场景 100 分）：
- 诊断准确性（40）：根因识别是否正确
- 证据完整性（20）：是否正确引用工具输出
- 可执行性（20）：修复 SQL 是否可直接跑
- 幻觉控制（10）：是否编造表/参数
- 输出可读性（10）：格式/措辞/长度

**验收**：
- 产出 `docs/benchmark/opengauss-2026-04.md`
- 模型排名 + 每场景详细打分
- OG 整体通过率 ≥ Oracle 对应 benchmark（若有）

---

## 4. 发版规划（专项从 v1.2.0 起步）

**里程碑口径**：
- v1.1.x 系列继续只修主线 bug，不接专项改动
- **v1.2.0 = OG 专项正式启动，P0 阶段完成的第一版切片**
- 之后沿 v1.2.x 推进，v1.3.0 为整体专项完成的里程碑版本

| 阶段 | 版本 | 内容 | ETA（专项启动后） |
|---|---|---|---|
| P0 ① + ② 完成 | **v1.2.0** | Profile 密度拉齐 + 规范管理 | +1 周 |
| P0 P0-skill 完成 | **v1.2.1** | 5 个 P0 skill（`/wdr` `/planhistory` `/trace` `/lwlocks` `/autovacuum`） | +2.5 周 |
| P1.3 完成 | **v1.2.2** | 规则场景 200+ | +4 周 |
| P1.4 完成 | **v1.2.3** | SQL Advisor OG | +5 周 |
| P1-skill 完成 | **v1.2.4** | 5 个 P1 skill（`/checkpoint` `/bgworker` `/hotkey` `/logicalslots` `/tempusage`） | +6 周 |
| 审计第一批 | **v1.2.5** | [A] 12 个薄壳扩充 + [D] 3 个 AI/核心真机验证 + 合并 3 个 | +7 周 |
| 审计第二批 | **v1.2.6** | [B] 23 个中等 + [C] 6 个大型 | +8 周 |
| P2-skill 完成 | **v1.2.7** | 5 个 P2 skill（`/mot` `/cmha` `/pubsub` `/toasttable` `/walsummary`） | +9 周 |
| Engine 对齐 | **v1.2.8** | 上下文 + 记忆 + 画像对齐 | +9.5 周 |
| P3 评估 | **v1.3.0** | 主流模型能力评估报告 + 专项整体完成 | +10-11 周 |

**发版流程区分**：
- v1.2.0、v1.3.0 是 **minor bump**，走完整流程（4 平台交叉编译 + sha256 + SCP + latest-version 更新 + 云主机）
- v1.2.x 中间 patch 默认只 push GitHub（依 `feedback-release-cadence` 偏好），除非有重大能力节点

**工作量总调整**：原估 6-7 周 → 新估 **10-11 周**（因 skill 审计从 6 个扩到全量 44 个 + 全量补单测）

---

## 5. 测试环境要求

- [ ] 有一个稳定的 OG 测试实例（版本 ≥ 3.0，最好 5.x）
- [ ] 造压脚本 `scripts/og_load_complex.sh`（参照 `pg_load_complex.sh` 改写）
- [ ] 9 个复杂场景的触发脚本（可自动化 reset + load）
- [ ] 9 场景 × 6 模型 × 2 capability = **108 次诊断**跑通（评估阶段）

**开放问题**：用户是否已经有可用的 OG 实例？没有的话可能需要先搭。

---

## 6. 需用户确认的开放问题

1. **规则场景来源策略**：
   - (a) 基于 Oracle 176 转译 + 补充 PG/OG 特有场景
   - (b) 先把 PG 48 提升到 100，再做 OG 从 PG 复制+扩展
   - (c) 从零按 OG 特性设计
   - 推荐：**(a)** —— 复用 Oracle 的场景沉淀

2. **SQL Advisor 迁移基线**：
   - (a) 从 PG sqladvisor 1:1 迁移（PG/OG 相似度高）
   - (b) 从 Oracle sqladvisor 迁移（规则体系更成熟）
   - 推荐：**(a)** —— PG 是 OG 的祖先，差异最小

3. **Skill 审计策略**：
   - OG 46 个 skill，不达标的删除还是标"实验性"保留？
   - 推荐：**删除**，避免误导用户

4. **评估场景的标准答案谁定**：
   - (a) 我起草 9 个场景的标准答案，用户 review
   - (b) 用户提供标准答案
   - (c) 用专家 LLM（Claude Opus）标注，作为 judge
   - 推荐：**(a) + (c) 组合** —— 我起草，Claude Opus 交叉验证，用户最终 review

5. **是否需要支持 GaussDB（商业版）**：
   - 之前 memory `todo-gaussdb-support.md` 提到 GaussDB 作为独立产品
   - 本专项是否包含？
   - 推荐：**本专项只做 OpenGauss**，GaussDB 作为后续专项

6. **OG 测试实例**：
   - 有现成的 OG 实例可连？版本？
   - 如果没有，是否需要先花 0.5-1 天搭一个？

---

## 7. 风险与 mitigation

| 风险 | 影响 | Mitigation |
|---|---|---|
| 规则场景数量目标过高（200+），4 库一致性难维护 | 专项延期 | 先做 OG，再同步回 Oracle/MySQL/PG（`feedback-four-db-sync`） |
| OG 特有视图（gs_, WDR, MOT）文档稀缺 | 规则难写 | 跑在 OG 实例上逐条验证，不靠文档推理 |
| SQL Advisor 跨库迁移可能遇到 plan 解析细节差异 | 延期 1 周 | 先做 MVP（3 个核心 analyzer），后续补齐 |
| 9 场景 × 6 模型评估成本高（大量 API 调用） | 评估预算 | 每场景 3 次跑，总共 162 次（非 108），预估 $50-100 API 成本 |
| 用户画像跨 OG/Oracle 污染 | PROFILE.md 混乱 | PROFILE.md 分 section：Oracle/OG 独立段 |

---

## 8. 下一步

本文档对齐后：

1. 用户回答第 6 节的开放问题
2. 补充/调整 8 个子项的 scope
3. 确认测试环境
4. 我起草 9 个评估场景的标准答案（独立文档 `docs/benchmark/opengauss-scenarios.md`），用户 review
5. 全部确认后，正式开工 P0 ①（Profile 提示词密度拉齐）

---

_本文档是讨论载体，未动代码，任何修改都欢迎直接在对话中提出。_
