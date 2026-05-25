# DM 专项优化方案 v6（最新）

**生成时间**: 2026-05-04
**作者**: Claude (基于 2026-05-03~04 全部 DM + OG 跨库测试)
**状态**: 审议中，**未确认前不动一行代码**
**评估期**: 6 模型 DM baseline + 3 次 DM meta + OG 跨库验证（122B/Opus/DeepSeek-V4）+ /locks bug 实验

## 🚨 v6 最重要的修订（重要程度: ★★★）

**Meta 问题不是简单"强模型 vs 弱模型"，按"问题子类型 × 模型 × 模块成熟度"三维矩阵看**：

| 模型 | OG B 类（指标计算原理）| OG A 类（数据值溯源）|
|---|---|---|
| **Opus** | **✅ 9/10**（dead_tup 公式 + 内核函数 + 自检 649 vs 949） | (未单独测) |
| **GLM** | (未测) | **✅ 自我修正 + 解释 blocktree 算法 + 给正确 PID** |
| DeepSeek | ❌ 18 轮重诊断 | (未测) |
| 122B | ❌ 重诊断 | (未测) |

**结论**：

1. **Opus 在 B 类（指标计算/原理）独一无二**（Anthropic RLHF 优势）
2. **GLM 在 A 类（数据值溯源/算法）也能做**（中文训练 + 国产 RLHF）
3. **DeepSeek/122B 在两类 meta 都 default 到"重新诊断"** — **训练目标是"做工作"，不是"答用户究竟问什么"**

**这意味着**：

1. P0-6 修复方向：**engine 层强制意图识别**（不是 prompt 层软引导）
2. 模型选型 "Meta 友好度" 维度：**Opus + GLM 是双保险，DeepSeek/122B 不可靠**
3. 即使做完 P0-6，B 类 meta 仍**强烈推荐 Opus**，A 类可以接受 GLM

---

## 0. 文档导览

```
1. 一页摘要               ← 快速决策看这章
2. 测试基础设施           ← 故障如何构造
3. 测试方法学（含衰减）   ← 为什么之前数据有偏差
4. 6 模型基线测试结果     ← 量化数据
5. Meta 问题三类区分       ← 关键洞察
6. 11 类根因分析（分层）  ← 为什么这么改
7. 优化方案 P0/P1/P2       ← 具体怎么改
8. 验收基线矩阵            ← 怎么判断改完了
9. 里程碑                  ← 什么时候交付
10. 跨项目启示             ← 不只 DM 的问题
附录 A: PromptProfile 草稿
附录 B: Meta 问题三类详解
附录 C: 故障寿命表 + 测试规范
版本变更
```

---

## 1. 一页摘要（决策视图）

### 现状

| 维度 | 数据 |
|---|---|
| DM 6 类故障识别率均值 | **1.92/6**（6 模型实测）|
| 同 122B 在 OG 主诊断 | **2-3/6**（v6 实测，OG 模块成熟拉了 +200%）|
| 同 Opus 在 OG 主诊断 | **5-6/6** |
| **主诊断差距** | **DM 模块本身**，不是模型能力 |
| Meta 问题命中率（DM）| 1/3（35B C 类成功，122B/GLM B 类失败）|
| Meta 问题命中率（OG）| 1/3（**Opus 完美**，DeepSeek/122B B 类失败）|
| **Meta 问题瓶颈** | **Opus 专属能力（v6 关键发现）** |
| 已知工具 bug | /locks (V$LOCK.THRD_ID), /tableinfo (返回 0 列) |

### 🎯 v6 诚实免责声明

| 优化项 | 期望 | 现实 |
|---|---|---|
| P0 主诊断改进 | 122B 1/6 → 4/6 | ✅ 大概率达成（OG 已证 2-3/6 是 122B 自然天花板，加 hint 能再拉到 4/6）|
| P0-6 Meta 问题改进 | 命中率 0% → 80% | ⚠ **可能仅对 Opus 有效**。DeepSeek/122B 可能 footer 都不会用 |
| **真正修 Meta** | 期望所有模型 | **现实：换 Opus 是最快的解** |

### 关键判断

1. **PromptProfile + 工具 bug + engine 不强制深挖** 三层叠加 → 模型表现远低于天花板
2. **Meta 问题分 3 类**（A/B/C），需要不同修复路径，不能一刀切
3. **测试方法学有缺陷**（故障会自然衰减），先修测试再修代码

### 投入与回报

| 阶段 | 工作量 | 平均识别率 |
|---|---|---|
| 当前 | — | 1.92/6 |
| **P0 完成** | **5 天** | **4.0/6**（+108%）|
| P1 完成 | 3 周 | 5.0/6 |
| P2 完成 | 2 月 | 5.5/6 |

**P0 是最高 ROI 阶段**，5 天投入翻倍识别率。

### 强制前置（写代码前必须做）

| 测试 | 工作量 | 决定什么 |
|---|---|---|
| **T1** 6 模型 fresh state 重测 | 1 天 | 修正基线偏差，得到准确起点 |
| **T3** Hint 引导测试 | 0.5 天 | 验证 P0-3 PromptProfile 改动方向 |

总 1.5 天，做完才能正式启动 P0。

---

## 2. 测试基础设施

### 2.1 故障构造脚本

**位置**: `faults/dm/dm_complex_local.sh`
**目标**: 本地 docker `dm8` 容器（DM Database V8, port 5236, SYSDBA/SYSDBA）

### 2.2 6 类并发故障设计

| Fault | 设计内容 | 数量 | MODULE 标签 | 自然寿命 |
|---|---|---|---|---|
| F1 | idle 连接洪水 (dbms_lock.sleep 7200) | 40 | fault_F1 | 2 小时 |
| F2 | 长事务持锁 (BEGIN+UPDATE+sleep) | 15 | fault_F2 | 2 小时 |
| F3 | CPU 饱和 (CONNECT BY+MD5, 10000 iter) | 4 | fault_F3 | ~10-15 min |
| F4 | I/O 饱和 (200K 全扫, 5000 iter) | 4 | fault_F4 | ~10-15 min |
| F5 | 行锁串行 (UPDATE 同热行 50000 iter) | 15 | fault_F5 | **数小时**（锁竞争超慢）|
| F6 | redo 压力 (INSERT+COMMIT 100000 iter) | 4 | fault_F6 | ~30 min |
| **合计** | | **82 sessions** | | |

### 2.3 关键基线对比

同模型 122B 在 OG 上识别 **6/6**（参考 `~/.claude/projects/-Users-sqlrush-opendb/memory/reference-og-local-fault-script.md`），DM 上识别 **1/6**。

**结论**：差距 100% 在 DM 模块本身。

---

## 3. 测试方法学（v5 新章）

### 3.1 关键发现：故障会衰减

各类故障的自然寿命差异巨大：

| 时刻（从 setup 算起）| 还活着的故障 |
|---|---|
| 0-15 min | F1+F2+F3+F4+F5+F6 全在 |
| 30 min | F1+F2+F4+F5（F3/F6 已退）|
| 2 hr | F5 + 残余（F1-F4/F6 都退）|
| 12 hr | 仅 F5 |

### 3.2 现有测试结果的可比性

| 模型 | 测试时刻（setup 后）| 实际故障状态 | 评分公平性 |
|---|---|---|---|
| qwen35-122b | ~10 min | 全 6 类活 | ✓ 公平 |
| opus-4.6 | ~25 min | F1+F2+F4+F5 活 | △ 略偏 |
| deepseek-v4-pro | ~50 min | F1+F2+F5 活 | △ 略偏 |
| kimi-k2.6 | ~60 min | F1+F2+F5 活 | △ 略偏 |
| glm-5.1 | ~3 hr | 主要 F5 | △ 不公平 |
| qwen36-35b-a3b | ~12 hr | 仅 F5 | ❌ 完全不可比 |

**强制规范**（写入 P0-4 benchmark 脚本）：

```bash
# 每测一个模型前必须重新 setup
bash faults/dm/dm_complex_local.sh cleanup
bash faults/dm/dm_complex_local.sh setup
sleep 30
# 然后 30 分钟内完成该模型的测试 (确保 6 类都还在)
```

---

## 4. 6 模型基线测试结果

### 4.1 完整对比表

| 维度 | 122b | opus-4.6 | deepseek-v4-pro | kimi-k2.6 | glm-5.1 | qwen36-35b-a3b |
|---|---|---|---|---|---|---|
| capability | large | large | large | medium | medium | large |
| 工具调用数 | 4 | 5 | **17** | 6 | **18** | 7 |
| 调用模式 | 1 轮并行 | 5 轮串行 | 17 轮串行 | 4 轮 | 18 轮串行 | 4 轮 |
| 耗时 | 1m25s | 1m23s | 6m5s | 4m2s | 3m52s | **57.9s** |
| **故障识别率** | **1/6** | **1/6** | **3-4/6** | **2/6** | **3/6** | **1.5/6**\* |
| 识别故障 | F1 | F1 | F1+F2+F4 | F1+F4 | F1+F2+F4 | **F5（唯一）** |
| 时间认知 | ❌ "8h" | ✅ 准 | ❌ "100h" | ❌ "8.7h" | (未明显错) | ❌ "19.4h" |
| Wait event 解读 | 罗列 | 定性区分 | 解释 commit (但错认) | 错放 buffer busy | 关注 V$TRXWAIT | 未深入 |
| MODULE 字段 | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| Worker 线程推理 | ❌ | ❌ | ✅ | ❌ | ❌ | ❌ |
| DM 专属视图 | ❌ | ❌ | 部分 | ❌ | ✅ V$TRXWAIT | ❌ |
| /locks bug 反馈 | ❌ | ✅ 主动报 | ❌ | ❌ | ❌ | ❌ |
| /tableinfo bug 反馈 | ❌ | ❌ | ❌ | ❌ | ✅ fault_io | ✅ fault_lock |
| DM 语法准确性 | ❌ Oracle fallback | ✅ | ✅ | ✅+SESSID() | ✅ FETCH FIRST | ✅ |
| 报告结构 | 表格 | 表格+二阶推理 | 表格+因果链 | **6/6 证据卡** | 表格+"扇形" | 表格 |
| 综合分 | 6/10 | 7.5/10 | **8/10** | 6.5/10 | **7/10** | 6.5/10 |

\* 35B 测试时故障已严重衰减，仅 F5 还活，所以"识别 F5"是相对当时残留状态评的

### 4.2 故障识别共性（按 fault 维度）

| 故障 | 命中率 | 说明 |
|---|---|---|
| F1 idle | 5/5（35B 测时已衰减不算）| 最容易 |
| F2 longtx | 2/5 (DeepSeek + GLM) | 中等 |
| F3 CPU | **0/6** ❌ | 缺 /cpu-top skill |
| F4 I/O | 3/5 | 中等 |
| F5 lock | 1/6（仅 35B 单独场景）| 多模型被 F1 噪音盖住 |
| F6 redo | **0/6** ❌ | 5/6 模型把 commit wait 错认成"正常 fsync" |

### 4.3 模型选型推荐（v6 加 Meta 维度，v6.1 修订）

| 场景 | 推荐 | Meta-A 类 | Meta-B 类 | 理由 |
|---|---|---|---|---|
| **Meta-B（指标计算/原理）** | **opus 最佳** | ★★★ | ★★★ | OG 实测 9/10，唯一全 B 类可靠 |
| **Meta-A（数据溯源/算法）** | **opus 或 glm** | ★★★ | ★★★ / ★★ | GLM OG 实测自我修正 + 算法解释，中文训练优势 |
| 深度主诊断 | deepseek-v4-pro | ✗ | ✗ | 8/10，唯一识别 worker 线程瓶颈 |
| 国产数据库主诊断 + 部分 meta | **glm-5.1** | **★★** | ? | 7/10 主诊断 + A 类 meta 可靠 |
| 通用主诊断 + Meta 全能 | **opus-4.6** | ★★★ | ★★★ | 7.5/10 主诊断 + 9/10 meta-B |
| 极速响应（< 1 min）| qwen36-35b-a3b | ✗ | ✗ | 6.5/10，57.9 秒最快 |
| 日常监控 | kimi-k2.6 | ✗ | ✗ | 6.5/10 + 报告结构最清晰 |
| 离线/隐私 | qwen35-122b | ✗ | ✗ | 6/10 但本地，待 P0-3 修 Oracle fallback |

**Meta 友好度评级依据**：
- ★★★ = OG 实测能正确答该子类 meta（B 类只有 Opus 验证；A 类 Opus + GLM）
- ★★ = 部分类型已证（GLM A 类已证，B 类待测）
- ? = 未测但有理由乐观（GLM 在 A 类成功，可能 B 类也行）
- ✗ = OG 或 DM 实测会"重新做诊断"绕过 meta，无视用户意图

---

## 5. Meta 问题三类区分（v5 关键洞察）

### 5.1 三次 meta 测试

| # | 模型 | 问题 | 结果 | 性质 |
|---|---|---|---|---|
| 1 | qwen35-122b | "commit wait 累计 57685 秒**怎么统计的**" | ❌ 重诊断 + Oracle 语法 fallback | B 类 |
| 2 | glm-5.1 | "29 个锁等待**是如何查询出来的**" | ❌ 18 轮重诊断 | A 类（被 /locks bug 阻挡）|
| 3 | qwen36-35b-a3b | "**这个睡眠会话是如何判断出来的**" | ✅ 1 轮 15s 答对 | C 类 |

### 5.2 三类性质区分

| 类型 | 定义 | 例子 | 修复路径 | 修复难度 |
|---|---|---|---|---|
| **A. 工具支持的聚合** | 答案能从某个 skill 反推 | "29 锁等待怎么查" → /locks 应该有原始数据 | 修工具 bug + 教模型反推 | ⭐⭐ |
| **B. 工具内部聚合** | 答案在 opendb 工具的内部 SQL | "commit wait 累计 57685 怎么算" | 加 metadata footer 或 /explain-skill | ⭐⭐ |
| **C. 工具输出可见字段** | 答案就在已显示的字段里 | "睡眠会话怎么判断" → SQL_TEXT 字段 | 不需要修，模型自己能答 | ⭐ 已解决 |

### 5.3 GLM 失败的实验证据（A 类）

跑 `dbaa -c dm /locks` 直接测试：

```
✗ 失败: dm query failed: Error -2111: 第3 行附近出现错误
```

确认 V$LOCK.THRD_ID 在 DM build 1-3-62-2023 不存在，**/locks 直接报错**。GLM 在轮 3 调 /locks 拿到错误，没有 fallback 机制告诉模型"换 /sql 直接查 V$LOCK"，于是只能依赖 /health 给的聚合数 29，无源可指。

### 5.4 系统层洞察

**单纯加 /explain-skill 工具不够**。需要：

1. **修工具 bug**（让 A 类问题有数据基础）
2. **加输出 metadata**（解决 B 类）
3. **教模型反推路径**（即使工具坏了也能用 /sql 兜底）
4. **加工具失败 fallback 机制**（错误时自动建议备用工具）

---

## 6. 11 类根因分析（v5 整合，按层分组）

### 6.1 工具层（4 项）

#### R1: /locks skill V$LOCK.THRD_ID 字段映射 bug ★★★

**证据**: Opus 主动报告 + 实验确认（错误码 -2111）。
**影响**:
- F5 锁竞争故障必然漏（无锁数据）
- A 类 meta 问题被阻断（无源可推）
**代码位置**: `internal/dm/monitor/` 下 V$LOCK 相关 skill

#### R2: /sessions /activesessions 缺 MODULE/APPNAME 输出列 ★★★

**证据**: 6/6 模型都没用 MODULE 字段（即使我们打了 fault_F1~F6 标签）。
**影响**: 模型无 fingerprint 区分多类故障来源 → F2 识别率仅 2/6。
**代码位置**: `internal/dm/monitor/sessions.go`

#### R3: 缺 /workers skill ★★

**证据**: DeepSeek 用 sql 拼凑找到 "WORKER_THREADS=16 不够 71 并发" 的关键根因。
**影响**: 应内置工具，不应每次靠模型 ad-hoc 探索；DM 是基于 worker 线程池架构（不同于 Oracle/PG 多进程）。

#### R4: 缺 /cpu-top skill ★★

**证据**: F3 CPU 饱和 6/6 模型 0 命中，因为没有"哪个 SQL/会话当前在烧 CPU"的工具。

### 6.2 工具元数据层（2 项）

#### R5: 工具输出不带 source metadata ★★

**证据**: 122B 测试 commit wait 累计 meta 问题失败。/health 输出聚合数字没附 source SQL，模型无法答"怎么算的"。
**影响**: B 类 meta 问题全部失败。

#### R6: /tableinfo skill 在 DM 上字段查询 bug ★

**证据**: GLM 报 fault_io "0列0索引"，35B 报 fault_lock "0列0索引"。**两个独立模型独立报错** = 工具真实 bug。
**影响**: 表结构相关诊断都受污染。

### 6.3 Prompt 层（4 项）

#### R7: DM PromptProfile 缺关键引导 hint ★★★

**证据**: 同 122B OG=6/6, DM=1/6。

**缺失的引导**:
1. V$SESSIONS.MODULE/APPNAME 是 fingerprint，多类故障必查
2. buffer busy wait/commit wait 高频判定规则（5/6 模型同盲点）
3. blocktree=0 但锁等待>0 = latch 竞争
4. 实例时间 vs 会话时间字段区分（4/6 模型时间认知错）
5. **指标来源反推路径**（工具坏了或没数据时，主动用 /sql 查对应 V$xxx 视图反推）
6. **DM 语法约束**（防 122B Oracle fallback）

**代码位置**: `internal/engine/profile/dm.go` 的 `SystemPromptRules()`

#### R8: /llm 单一诊断模式吞 meta 问题（系统级）★★★

**证据**: 122B + GLM 在 meta 问题上失败。35B 答对的特殊情况是 C 类（数据可见），不能用作"模式没问题"的反例。

**深层原因**:
1. system prompt 强诊断角色绑定
2. 反幻觉规则太强，把合法知识回忆也封杀
3. 中文歧义被默认成 B 解读（"如何查询" 在数据库语境倾向"如何重新查"）

#### R9: commit wait 普遍误判 ★★

**证据**: 5/6 模型把 commit wait 高频错认成"正常 fsync 等待"。
**影响**: F6 redo 故障 0/6 命中。

#### R10: 模型不会跨工具反推 ★★

**证据**: GLM 调用了 /locks（虽然报错）和 /sql（几十次），但**没主动用 /sql 直接查 V$LOCK 反推 29 的来源**。说明模型不会"已知聚合数 → 找对应系统视图反推"这种模式。

### 6.4 Engine 层（1 项）

#### R11: engine 不强制最低轮次 ★★

**证据**:
- engine.go:165-171 `MaxTurns` 是硬上限不是下限
- 122B 1 轮 4 工具就停，35B 4 轮就停
- DeepSeek/GLM 17-18 轮深挖（对照组）
**已有 TODO**: [todo-llm-early-stop-detection.md] 但未实施

### 6.5 兜底机制层（1 项 v5 新增）

#### R12: 工具失败无 fallback 引导 ★★

**证据**: GLM 调 /locks 拿到 -2111 错误，opendb 直接把错误返回模型，没建议"改用 /sql SELECT * FROM V$LOCK"。模型沉默放弃。

---

## 7. 优化方案 P0/P1/P2

### 🔴 P0 立即修（5 天 / 7 项）

#### P0-1: /locks skill 字段映射适配 ★★★（升级优先级）

| 项 | 内容 |
|---|---|
| 现状 | V$LOCK.THRD_ID 在 DM build 1-3-62-2023 不存在（实验确认 -2111）|
| 优化 | SELECT * FROM V$LOCK WHERE ROWNUM=1 看真实列，改用兼容字段 |
| 判断依据 | **双重影响**: F5 识别 + A 类 meta 问题都受阻 |
| 预期效果 | F5 识别率 0/15 → 50%+; A 类 meta 命中率 0% → 60%+ |
| 工作量 | 0.5 天 |
| 验收 | dbaa -c dm /locks 返回正常表格 + meta 问题"29 是怎么查的"能正确回答 |

#### P0-2: /activesessions /sessions 加 MODULE/APPNAME 列 ★★★

| 项 | 内容 |
|---|---|
| 现状 | 输出只有 PID/User/State/SQL，无 MODULE |
| 优化 | SELECT 增加 MODULE, APPNAME 列；表格输出加这两列 |
| 判断依据 | 6/6 模型都没看到 MODULE → 加上后立即解锁 fingerprint 推理 |
| 预期效果 | F2 识别率 2/6 → 5/6 |
| 工作量 | 1 天 |
| 验收 | dbaa -c dm /activesessions 输出含 MODULE 列且对齐 |

#### P0-3: DM PromptProfile 加 6 条核心 hint ★★★

| 项 | 内容 |
|---|---|
| 现状 | DM 的 SystemPromptRules() 缺关键引导 |
| 优化 | 加 6 条规则（代码草稿见附录 A）—— 核心、commit wait、latch、时间字段、**指标反推、DM 语法** |
| 判断依据 | 同 122B OG=6/6 vs DM=1/6 完全是 prompt 差异 |
| 预期效果 | **单这一条最高产**: 1/6 → 4-5/6 |
| 工作量 | 1 天 |
| 验收 | T3 hint 引导测试在 4 个模型上至少 3 个达 ≥ 4/6 |

#### P0-4: 把 dm_complex_local.sh 固化为 benchmark ★★

| 项 | 内容 |
|---|---|
| 现状 | 手工脚本无 CI 集成；之前测试因故障衰减失真 |
| 优化 | 包装成 `scripts/benchmark/run_dm_fault_benchmark.sh`：**每模型测试前自动 cleanup+setup**，30 分钟内完成，输出 markdown 报告 |
| 判断依据 | v5 测试方法学发现：必须强制 fresh state，否则数据失真 |
| 预期效果 | 30 分钟出 6×6 markdown 矩阵，后续优化能持续回归 |
| 工作量 | 0.5 天 |
| 验收 | bash run_dm_fault_benchmark.sh 输出 6x6 矩阵 + 时间戳 |

#### P0-5: /tableinfo skill 在 DM 上修 bug ★★

| 项 | 内容 |
|---|---|
| 现状 | 双模型双表独立报"0列0索引"（GLM 测 fault_io，35B 测 fault_lock）|
| 优化 | 检查 catalog 视图查询（应该是 SYSCOLUMNS / DBA_TAB_COLUMNS / SYSOBJECTS），修 DM 兼容 |
| 判断依据 | 双独立来源报错 = 真实 bug 不是模型幻觉 |
| 预期效果 | /tableinfo 输出真实表结构，模型能据此判索引覆盖 |
| 工作量 | 0.5 天 |
| 验收 | dbaa -c dm /tableinfo fault_io 输出 3 列 + 1 索引 (PK) |

#### P0-6（v6 重写）: engine 层强制意图识别 + 禁止诊断工具 ★★

| 项 | 内容 |
|---|---|
| 现状 | B 类 meta 问题失败。**v6 实测发现**：DeepSeek 18 轮调用全是诊断方向，从不查 pg_stat_all_tables；122B 重诊断；GLM 重诊断。**模型 default 行为是"做工作"，不是"答 meta"** |
| 旧方案（v5）| (a) 加 metadata footer (b) prompt 意图识别 → **被 v6 实测部分否定，弱效** |
| **新方案 (v6)** | **engine 层 hard intervention**：<br>1. **入口检测 meta 关键词**（"如何/怎么算/什么是/原理/哪来的"）<br>2. **强制注入 system-reminder**："禁止调诊断工具，禁止重新做诊断报告，必须直接答 SQL 公式或概念解释"<br>3. **限制工具集**：meta mode 下只允许 /sql 和 /explain-skill，禁用 health/blocktree/locks 等诊断工具<br>4. **Footer 降级到 P1**（只对 Opus 锦上添花，对其他模型可能没用）|
| 判断依据 | v6 跨库实测：3/4 模型（122B/GLM/DeepSeek）在 OG 模块成熟环境下仍 meta 失败 → prompt 层引导 ≈ 无效 → 只能 engine 层强制 |
| 预期效果 | DeepSeek/122B/GLM 命中率 0% → 50%+（强制限制后还是可能 fallback）；Opus 保持 9/10 |
| 工作量 | 2 天（intent classifier 1 天 + tool-set restriction 1 天）|
| 验收 | DeepSeek 对 "1269 万死元组怎么算的" 给出 SQL 公式，不重新诊断 |
| **诚实免责** | 即使做完 P0-6，**Opus 仍是 meta 问题最可靠选择**。这是模型架构差异，opendb 无法完全弥合 |

#### P0-7（**v5 新增**）: 工具失败 fallback 机制 ★★

| 项 | 内容 |
|---|---|
| 现状 | 工具报错（如 /locks -2111）直接把错误返给模型，无引导 |
| 优化 | engine 层包装错误：检测到工具错时，自动追加 system-reminder："/X 不可用 (原因: Y)，可改用 /sql 'SELECT * FROM Vxxx' 查同等数据" |
| 判断依据 | GLM 实验：工具坏了模型沉默放弃；有引导后能用 /sql 兜底 |
| 预期效果 | A 类 meta 问题在 P0-1 修好之前也有可用兜底 |
| 工作量 | 1 天 |
| 验收 | 故意让 /locks 返回错误，模型自动用 /sql 替代查到锁信息 |

#### P0 总计预期效果

| 模型 | 当前（待 T1 修正）| P0 后 |
|---|---|---|
| qwen35-122b | 1/6 | **4/6** |
| opus-4.6 | 1/6 | **5/6** |
| deepseek-v4-pro | 3-4/6 | **5/6** |
| kimi-k2.6 | 2/6 | **4/6** |
| glm-5.1 | 3/6 | **4-5/6** |
| qwen36-35b-a3b | 1.5/6 | **3-4/6** |
| **平均** | **1.92** | **4.0** (+108%) |
| meta 问题命中（A 类）| 0% | 60%+ |
| meta 问题命中（B 类）| 0% | 80%+ |

### 🟡 P1 重要（2-3 周）

#### P1-1: 4 类 wait event 专门 skill（3 天）

加 /latch /buffer-busy /commit-wait /lock-wait — 让 Opus 等模型走到 "buffer busy wait 高 → 推理 latch" 时有工具继续。

#### P1-2: DM /diag 集成"多类故障识别 + 4 层诊断"策略（4 天）

复用 OG /diag 的"告警主线→关联→对比→排名"4 层。OG 上 122B 用此推到 6/6。

#### P1-3: DM 字段映射全面审计（5 天）

把所有 skill 用到的 V$xxx 字段与 DM 实际 catalog 对照检查。避免下一个 V$LOCK.THRD_ID 类 bug。

#### P1-4: DM 100 场景 benchmark（1 周）

参考 OG 100 场景写 50-100 个 DM 故障案例。DM 模块成熟度 30% → 70%。

#### P1-5: engine 早停检测（2 天）

检测最终回复前的工具数 <3 时插入 system-reminder："你只调了 N 个工具，复杂故障建议至少 5+ 工具，请评估是否需要更多证据"。把 122B/35B 拉到 DeepSeek/GLM 调用深度。

#### P1-8: /workers skill — worker 线程池监控（0.5 天）

DeepSeek 用 sql 拼凑找到的根因应内置。/workers 输出 WORKER_THREADS 配置 + 当前活跃数 + 排队数。

#### P1-9: /cpu-top skill — OS / 进程级 CPU 占用（1 天）

F3 CPU 饱和 6/6 模型 0 命中 = 工具断层。/cpu-top 输出 docker stats CPU% + Top 5 CPU 密集 sessions + SQL_ID 关联。

### 🟢 P2 长期（1-2 月）

#### P2-1: DM 微调一个本地模型（2 周）

收集 DM 真实诊断案例（dbaa 历史 + 100 场景输出），SFT 微调 qwen35-122b。预期 6/10 → 8/10。

#### P2-2: DM 4 层诊断 prompt 全集（1 周）

完整 4 层框架，对齐 OG 用户体验。

#### P2-3: DM Sentinel 探针适配（1 周）

V$SESSIONS, V$WAITSTATS 实时采集 + 自适应 3σ 触发。异常自动发现，不用主动 /llm。

---

## 8. 验收基线矩阵（v6 向下校准）

固定故障脚本 `faults/dm/dm_complex_local.sh setup`，**setup 后 30 分钟内**测，固定问题 "当前数据库存在什么问题"：

| 模型 | 当前（待 T1 重测）| M1 (P0 完, +5 天) | M2 (P1 完, +3 周) | M3 (P2 完, +2 月) |
|---|---|---|---|---|
| qwen35-122b | 1/6 | 3-4/6 ⬇ | **4/6 ⬇** | **4-5/6 ⬇** |
| opus-4.6 | 1/6 | 5/6 | **5-6/6 ⬇** | 6/6 |
| deepseek-v4-pro | 3-4/6 | 5/6 | **5/6 ⬇** | 6/6 |
| kimi-k2.6 | 2/6 | 3-4/6 ⬇ | 4/6 ⬇ | 4-5/6 ⬇ |
| glm-5.1 | 3/6 | 4/6 ⬇ | 5/6 | 5-6/6 ⬇ |
| qwen36-35b-a3b | 1.5/6 | 3/6 ⬇ | 3-4/6 ⬇ | 4-5/6 ⬇ |
| **平均** | **1.92** | **3.7 ⬇** | **4.5 ⬇** | **5.0 ⬇** |

⬇ = v6 向下校准（基于 OG 实测：122B 在最成熟模块上也只能 2-3/6，DM 不可能超过 OG 上限）

**Meta 问题命中（v6 重新校准）**:

| 类型 | 当前 | M1 后 (P0) | M2 后 (P1) | 备注 |
|---|---|---|---|---|
| A 类（工具支持的聚合）| 0% | 60% | 80% | P0-1 修 /locks 后所有模型受益 |
| B 类（工具内部聚合）| 0% | **Opus 90% / 其他 30-50%** | **Opus 95% / 其他 50%** | **v6 修订**：engine 强制意图识别后非 Opus 也能部分答，但 Opus 是天花板 |
| C 类（已可见字段）| 100% | 100% | 100% | 已天然解决（35B 验证）|

---

## 9. 里程碑

```
T+0 (审议节点)
  ├─ 方案 v5 审议 ← 当前
  ├─ T+0.5 day: 用户确认方案
  │
  ├─ T+1.5 day: 跑 T1+T3 补充测试 (强制前置)
  │   决策: 评估 T1 修正后的真实基线 → 启动 P0
  │
  ├─ M1 (T+6.5 day): P0 完成 (7 项, 5 天)
  │   验收: 平均 ≥ 4/6, A/B 类 meta ≥ 60%
  │   决策: 评估 P1 投入是否值得
  │
  ├─ M2 (T+4 周): P1 完成 (7 项, 3 周)
  │   验收: 122B ≥ 5/6, Opus = 6/6, 100 场景跑通
  │   决策: 是否启动 P2 微调
  │
  └─ M3 (T+2 月): P2 完成 (3 项, 1 月)
      验收: 全部模型 ≥ 5/6, 微调模型 ≥ 8/10
```

---

## 10. 跨项目启示

DM 暴露的问题——PromptProfile 不够深 + engine 不强制最低轮次 + meta 问题被吞 + 工具失败无 fallback——**不是 DM 独有**：

1. **统一基线测试**: 把"6+ 类并发故障识别 + meta 问题三类"作为 Oracle/MySQL/PG/OG/DM/GaussDB 的统一回归
2. **三维矩阵**: 模型 × 数据库 × 故障类型
3. **季度复测**: 每季度跑全矩阵，发现退化
4. **测试方法学规范**: 每次基线测试必须 setup 后 30 分钟内（v5 新增规范）
5. **/llm 意图识别 + 工具 fallback**: P0-6 / P0-7 不是 DM 特有，应升级到 engine 层
6. **元数据 footer**: 所有库的聚合 skill 都应附 source SQL footer，统一信息透明度

---

## 附录 A: P0-3 PromptProfile 6 条 hint 代码草稿

```go
// internal/engine/profile/dm.go SystemPromptRules() 加这段:
const dmCriticalRules = `
## DM 故障诊断关键规则 (基于 v5 6 模型测试 + 3 次 meta 追问)

1. **多类故障并存识别 (必须第一步)**:
   V$SESSIONS.MODULE 和 APPNAME 是应用 fingerprint。
   诊断开始必须先跑:
       SELECT MODULE, APPNAME, COUNT(*)
       FROM V$SESSIONS
       GROUP BY MODULE, APPNAME
       ORDER BY COUNT(*) DESC;
   识别有几类不同应用来源。每类来源单独分析。

2. **commit wait + buffer busy wait 高频判定** (基于 5/6 模型误判):
   commit wait count > 100k 时**禁止**直接定性"正常 fsync"或"次要事件"。
   按以下顺序判断：

   - Step 1: 查 /sessions WHERE STATE='ACTIVE' AND
     (SQL_TEXT LIKE '%INSERT%' OR '%UPDATE%' OR '%COMMIT%')
     - 如有结果 → commit wait 来自高频写工作负载（不是 fsync）
       → 必须报告为"写压力"故障
     - 如无结果 → 检查应用是否在循环空 commit（业务 bug）

   - Step 2: 查 /sessions WHERE STATE='ACTIVE' AND
     SQL_TEXT LIKE '%dbms_lock.sleep%'
     - 如 sleep 会话占多数但 commit count 仍 100k+
       → 还有别的会话在高频 commit，用 /topsql 找出谁

   - Step 3: blocktree=0 但锁等待>0 = latch 竞争（不是行锁互锁）
     - 必须用 /waits 看具体 wait_class
     - buffer busy wait 高频 → 必查最近 SQL 找写竞争源

3. **会话时间字段区分** (基于 4/6 模型时间认知错):
   - V$SESSIONS.LAST_RECV_TIME = 该会话上次活动时间
   - V$SESSIONS.QUERY_START = 当前 SQL 开始时间 (单 SQL 运行时长用这个)
   - V$INSTANCE.START_TIME = 实例启动时间
   绝不能把实例启动时间当会话时间用。

4. **工具不熟悉就先 SELECT 看一眼**:
   不确定字段名时，先 SELECT * FROM V$xxx WHERE ROWNUM<=1 看真实列。
   不要凭印象 hardcode SQL（已知 V$LOCK.THRD_ID 在不同 build 不同）。

5. **指标来源反推路径** (v5 新增, 基于 GLM 不会反推):
   如果用户问某个 /health 等聚合工具的指标"怎么算的"或"哪来的"，应:
   - 第一步: 检查工具输出 footer 是否有 source metadata
   - 第二步: 没有的话，自己用 /sql 查对应的 V$xxx 视图反推
     例: /health 的 '锁等待会话' 对应 V$LOCK 视图
         /health 的 '活跃会话' 对应 V$SESSIONS WHERE STATE='ACTIVE'
         /health 的 'commit wait' 对应 V$WAITSTATS WHERE EVENT='commit'
   - 第三步: 跑 /sql SELECT count(*) ... 验证数字, 然后回答 "X = SQL Y"

6. **DM 语法强制约束** (基于 122B Oracle fallback):
   你正在诊断 DM (达梦) 数据库。所有 SQL 必须用 DM 原生语法。
   禁止使用 Oracle 语法:

   | Oracle (禁) | DM (用这个) |
   |---|---|
   | V$SESSION 单数 | V$SESSIONS 复数 |
   | SID, SERIAL# | SESS_ID, SESS_SEQ |
   | ALTER SYSTEM KILL SESSION 'sid,serial#' | SP_CLOSE_SESSION(sess_id) |
   | DBMS_LOCK.SLEEP | dbms_lock.sleep (DM 兼容大小写) |

   任何修复 SQL 给出前必须自检：是否纯 DM 语法。
`
```

---

## 附录 B: Meta 问题三类详解

### B.1 三类定义与例子

| 类型 | 关键特征 | 实例 | 是否需 opendb 改 |
|---|---|---|---|
| A. 工具支持的聚合 | 答案能从某个 skill 反推 | "29 锁等待怎么查" | 修工具 + 教反推 (P0-1, P0-3 hint5) |
| B. 工具内部聚合 | 答案在工具内部 SQL | "commit wait 累计 57685s 怎么算" | 加 metadata footer (P0-6) |
| C. 工具输出可见字段 | 答案在已显示字段里 | "睡眠会话怎么判断" | 不需要改 |

### B.2 三类区分依据

**判断流程**:

```
用户提 meta 问题
  → 答案是否在最近一轮工具输出的某字段里? 
        → 是 = C 类 (天然能答)
  → 答案是否能通过查某个系统视图反推? 
        → 是 = A 类 (需要工具能用)
        → 否, 答案在 opendb 内部代码 = B 类 (需要 metadata 暴露)
```

### B.3 C 类成功案例（35B 答对的）

```
上下文: activesessions 输出已包含 SQL_TEXT 字段
用户问: "睡眠会话是如何判断出来的"
35B 行为: 1 轮调 activesessions, 直接指 SQL_TEXT 字段
回答: "依据 SQL_TEXT 字段显示 CALL dbms_lock.sleep(7200)"

→ 成功. 关键: 答案在数据可见区, 模型只需指认
```

### B.4 A 类失败案例（GLM 答错的）

```
上下文: /health 输出 "锁等待会话: 29", /locks 因 V$LOCK.THRD_ID bug 报错
用户问: "29 个锁等待是如何查询出来的"
GLM 行为: 18 轮重诊断, 多次调 sql 查别的东西, 没主动查 V$LOCK
失败原因:
  1. /locks 工具坏了 (无锁数据可指)
  2. 没引导教模型 "/locks 坏了用 /sql 直接查 V$LOCK"
  3. 模型不会自发想到这个反推路径
修复: P0-1 (修 /locks) + P0-3 hint #5 (反推路径) + P0-7 (失败 fallback)
```

### B.5 B 类失败案例（122B 答错的）

```
上下文: /health 或 /waits 输出 "commit wait 累计 57685 秒"
用户问: "这个 57685 是如何统计出来的"
122B 行为: 重做诊断 + Oracle 语法 fallback
失败原因:
  1. commit wait 累计是 opendb 工具内部聚合, 没有现成系统视图能反推
  2. opendb 没把内部 SQL 暴露给模型 (无 metadata footer)
  3. 模型没办法看到工具的"实现"
修复: P0-6 (metadata footer) - 把工具 SQL 附在输出末尾
```

---

## 附录 C: 故障寿命表 + 测试规范

### C.1 6 类故障自然寿命

| Fault | 内容 | 自然寿命 | 备注 |
|---|---|---|---|
| F1 | dbms_lock.sleep(7200) | 2 小时 | sleep 完会话退出 |
| F2 | BEGIN+UPDATE+sleep(7200) | 2 小时 | 同 F1 |
| F3 | CONNECT BY+MD5 (10000 iter) | ~10-15 min | iter 跑完退出 |
| F4 | 200K 全扫 (5000 iter) | ~10-15 min | 同 F3 |
| F5 | UPDATE 同热行 (50000 iter) | **数小时** | 锁竞争超慢 |
| F6 | INSERT+COMMIT (100000 iter) | ~30 min | iter 跑完退出 |

### C.2 测试规范（写入 P0-4 benchmark）

```bash
#!/bin/bash
# 测一个模型前必须跑这个序列:
echo "[1/3] cleanup..."
bash faults/dm/dm_complex_local.sh cleanup
docker restart dm8       # 兜底，避免 SP_CLOSE_SESSION 慢
sleep 30                 # 等 DM ready

echo "[2/3] setup..."
bash faults/dm/dm_complex_local.sh setup
sleep 30                 # 等 6 类故障全部活跃

echo "[3/3] 必须 30 分钟内完成测试 (F3/F4/F6 寿命短)"
# 这里跑模型测试 ...
```

### C.3 跨模型对比公平性原则

- ✅ **同时间窗口**: 所有模型在 setup 后 5-25 分钟内完成测试
- ✅ **fresh state**: 每模型独立 setup
- ✅ **统一问题**: "当前数据库存在什么问题"
- ✅ **统一评分**: 按 6 类 fault 命中数 + 综合质量分 (10 分制)
- ❌ 禁止跨小时对比（故障状态已变）
- ❌ 禁止复用前一个模型测试时的 dm 状态

---

## 版本变更

| 版本 | 时间 | 主要变化 |
|---|---|---|
| v1 | 2026-05-03 | 122B 单模型测试初稿 |
| v2 | 2026-05-03 | 加 Opus + 4 类根因 + P0/P1/P2 三层 |
| v3 | 2026-05-03 | 加 DeepSeek + Kimi + GLM + P0-5 /tableinfo + P0-6 升级 |
| v4 | 2026-05-04 | 加 35B + 测试方法学（衰减发现）+ 6 模型完整矩阵 |
| v5 | 2026-05-04 | Meta 三类区分 + /locks bug 实验确认 + 反推路径 hint + P0-7 工具 fallback + 11 类根因分层 |
| **v6 (当前)** | **2026-05-04** | **跨库 OG 验证（122B 主诊 2-3/6, Opus meta 9/10, DeepSeek meta 失败, GLM meta A 类成功） + P0-6 重写为 engine 强制意图识别 + 验收基线向下校准 + 模型选型加 Meta-A/B 双维度 + 诚实免责声明** |

---

## 关联资源

- 测试脚本: `faults/dm/dm_complex_local.sh`
- OG 同款脚本: `faults/og/og_complex_local.sh`
- OG benchmark: `~/.claude/projects/-Users-sqlrush-opendb/memory/reference-og-classic-fault-scenario.md`
- OG 本地饱和故障: `~/.claude/projects/-Users-sqlrush-opendb/memory/reference-og-local-fault-script.md`
- Engine 早停: `~/.claude/projects/-Users-sqlrush-opendb/memory/todo-llm-early-stop-detection.md`
- /slowsql 格式: `~/.claude/projects/-Users-sqlrush-opendb/memory/todo-slowsql-output-format.md`
- DM 优化方案 memory 索引: `~/.claude/projects/-Users-sqlrush-opendb/memory/todo-dm-module-optimization.md`
