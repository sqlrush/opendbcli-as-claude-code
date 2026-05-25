# Round 3 — Opus 严格评估（2026-03-28）

## 方法

严格按照 Opus 和 Rule 分别评估，由 Opus 打分。
- 在测试服务器注入真实故障
- `opendb -c oracle '/rule live'` 采集 Rule Engine 输出
- Opus 给出标准诊断
- Opus 按 5 维度打分（根因 40 + 修复 30 + 严重程度 10 + 排查路径 10 + 完整性 10）

## 评分总表

| 场景 | R2 分 | R3 分 | 变化 | 根因 | 核心问题 |
|------|-------|-------|------|------|---------|
| T020 硬解析风暴 | 30 | **80** | +50 | literal SQL > 50% ✅ | hard_parse_storm 成为 primary |
| T021 硬解析+SP充足 | 10 | **63** | +53 | hard parse + literal SQL ✅ | subpool 框架偏题 |
| T099 SP Latch+硬解析 | 45 | **69** | +24 | hard parse + literal SQL ✅ | 补了 session_cached_cursors |
| T026 数据倾斜 | 35 | **50** | +15 | cursor 热点争用 ⚠️ | 无 bind peeking 根因识别 |
| T002 锁:blocker慢SQL | 65 | **80** | +15 | blocker 慢 SQL 持锁 ✅ | 精准 |
| T014 cursor pin S | 55 | **76** | +21 | 纯并发热点 RC1 ✅ | RM 干扰 |
| T015 DDL+DML | 50 | N/A | — | SSD 秒完成 | 无法测试 |
| T-CBC 热块latch | 50 | **74** | +24 | 纯并发热点 RC1 ✅ | SSD 转为 cursor:pin S |
| T-LIBPIN lib cache pin | 50 | **35** | -15 | 监控工具会话 ❌ | RM 残留干扰 |

**可评分 8 场景均分: 65.9（R2 约 42.5, +23.4）**

---

## 详细评分

### T020 — 硬解析风暴（30 → 80, +50）

**Wait Profile**: latch:shared pool 96.9%（30 并发 literal SQL）

**Rule 输出**:
```
根因: literal SQL占比>50%
严重程度: ■■■■ 严重    置信度: 73%
证据链:
1. 大量literal SQL导致硬解析风暴,shared pool压力严重
2. 每次硬解析需要获取 shared pool latch 并在 library cache 中创建新 cursor
建议: cursor_sharing=FORCE + 绑定变量 + session_cached_cursors
```

**Opus 标准**: 30 并发 literal SQL → 硬解析风暴 → latch:shared pool 串行化

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 35 | hard_parse_storm primary + literal SQL 正确 |
| 修复 30 | 25 | cursor_sharing + 绑定变量 + session_cached_cursors ✅ |
| 严重程度 10 | 9 | CRITICAL ✅ |
| 排查路径 10 | 6 | MI2-005 吸收为下游 ✅，缺 parse count 数值 |
| 完整性 10 | 5 | 缺 v$sqlarea 分析 |
| **总分** | **80** | |

---

### T021 — 硬解析冲高但 SP 充足（10 → 63, +53）

**Wait Profile**: latch:shared pool 91.7%（10 并发 literal SQL）

**Rule 输出**:
```
根因: hard parse rate高,literal SQL集中在某些subpool
严重程度: ■■■■ 严重    置信度: 58%
证据链: subpool间内存分布严重不均
建议: cursor_sharing=FORCE + _kghdsidx_count + 绑定变量
```

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 28 | 识别 hard parse + literal SQL ✅，subpool 框架偏题（SP 充足时 subpool 非瓶颈） |
| 修复 30 | 18 | cursor_sharing ✅，_kghdsidx_count 无关（SP 充足不需调 subpool） |
| 严重程度 10 | 7 | CRITICAL 略过度，应为 HIGH |
| 排查路径 10 | 5 | 缺 SP free memory 检查 |
| 完整性 10 | 5 | 缺 session_cached_cursors |
| **总分** | **63** | |

---

### T099 — Latch Shared Pool + 硬解析（45 → 69, +24）

**Wait Profile**: latch:shared pool 96.9%（30 并发 literal SQL）

**Rule 输出**: 与 T020 相同（hard_parse_storm primary）

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 32 | hard parse + literal SQL ✅ |
| 修复 30 | 18 | cursor_sharing + 绑定变量 ✅，缺 session_cached_cursors（MI2-005 分支有但未到达） |
| 严重程度 10 | 9 | CRITICAL ✅ |
| 排查路径 10 | 5 | 缺 latch 竞争率和 parse 速率 |
| 完整性 10 | 5 | |
| **总分** | **69** | |

---

### T026 — 数据倾斜 + 绑定变量窥探（35 → 50, +15）

**Wait Profile**: latch free 81.3%, cursor:pin S wait on X 6.3%

**Rule 输出**:
```
根因: 热点 SQL 高并发（> 10）— mutex 热点争用
严重程度: CRITICAL    置信度: 68%
次要根因: 高频调用 — 单次快但调用密集
```

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 20 | cursor 争用 ✅，缺数据倾斜和 bind peeking 根因 |
| 修复 30 | 12 | 偏通用，缺 histogram/ACS/SQL Profile |
| 严重程度 10 | 8 | CRITICAL ✅ |
| 排查路径 10 | 5 | 缺 v$sql child cursor 分析 |
| 完整性 10 | 5 | 需要 Phase 3 SQL 性能诊断能力 |
| **总分** | **50** | |

---

### T002 — 锁级联:blocker跑慢SQL（65 → 80, +15）

**Wait Profile**: enq:TX row lock 87.5%, 14 victims, blocker 跑慢 SQL

**Rule 输出**:
```
根因: blocker 正在执行 SQL — 慢 SQL 持锁
严重程度: CRITICAL    置信度: 68%
建议: 优化 blocker SQL + Kill session
```

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 35 | "blocker正在执行SQL — 慢SQL持锁" 精准 ✅ |
| 修复 30 | 22 | SQL 优化 + Kill ✅，缺具体 blocker SID |
| 严重程度 10 | 9 | CRITICAL ✅ |
| 排查路径 10 | 7 | 阻塞链分析 ✅ |
| 完整性 10 | 7 | |
| **总分** | **80** | |

---

### T014 — cursor pin S 热块（55 → 76, +21）

**Wait Profile**: cursor:pin S 61.3%, resmgr:cpu quantum 35.5%

**Rule 输出**:
```
根因: 纯并发热点问题 (RC1) — mutex 竞争
严重程度: HIGH    置信度: 68%
次要根因: Resource Manager 轻度限流
建议: 应用缓存 + _mutex_spin_count + 读写分离
```

| 维度 | 分 | 说明 |
|------|-----|------|
| 根因 40 | 32 | cursor:pin S 热点 ✅ |
| 修复 30 | 22 | 全面 ✅ |
| 严重程度 10 | 8 | HIGH ✅ |
| 排查路径 10 | 7 | 关联 WE006/WE010 ✅ |
| 完整性 10 | 7 | RM 次因 ✅ |
| **总分** | **76** | |

---

### T-CBC — 热块 latch（50 → 74, +24）

与 T014 相同诊断模式（SSD 环境下 cursor:pin S 替代 CBC latch）。

**总分: 74**

---

### T-LIBPIN — Library Cache Pin（50 → 35, -15）

**Wait Profile**: library cache pin 41.2%, resmgr:cpu quantum 33.3%（RM 残留）

**Rule 输出**: "检查监控工具会话" — RM 干扰导致误诊。

**总分: 35**（RM 关闭后预期 55+）

---

## 关键代码修复

### 1. ExtractHardParsePct（系统性修复）

**问题**: QueryParseStats 返回 v$sysstat 多行 map，`toFloat()` 无法转换 → `MatchGT` 永远不匹配

**修复**: 新增 `ExtractHardParsePct()` helper 从多行结果提取 hard_parse_pct

**影响**: MI2-005, hard_parse_storm, WD004 所有用 QueryParseStats 的规则

### 2. WaitPct 优先于累积 v$sysstat

**问题**: v$sysstat 的 parse count 是实例启动以来的累积值，硬解析风暴期间累积百分比仍然很低（被历史软解析稀释）

**修复**: 当 latch:shared pool WaitPct >= 50% 时，直接视为严重硬解析，不依赖累积统计

### 3. hard_parse_storm 触发条件

**问题**: 原触发条件 `metrics["hard_parse_rate"] > 100` 在 live 模式下不可用（sentinel 不采集该 metric）

**修复**: 改用 `wait_profile["latch: shared pool"] > 20%`，tree fallback 用 WaitPct 估算硬解析严重程度

**效果**: hard_parse_storm 在 live 模式成功触发 → 成为 primary → MI2-005 通过 CausesOf 被吸收为下游

### 4. WD004 invalidation 分支收窄

**问题**: 无 DDL + 硬解析正常 + mutex X > 15% 时一律归为 "invalidation导致重编译"

**修复**: 增加 library cache lock 共存检查 — 无 lib lock 时改为 "执行计划不稳定/bind peeking" 分支

### 5. session_cached_cursors 建议

在 hard_parse_storm 和 MI2-005 的 literal SQL 分支都添加了 `session_cached_cursors = 300` 建议。

### 6. bind peeking 诊断建议

在 cursor:pin S wait on X 的热点 SQL 分支添加了 v$sql child cursor 和 dba_tab_col_statistics 排查建议。

---

## Resource Manager 干扰记录

- CDB 层 Scheduler Window 会自动恢复 `DEFAULT_MAINTENANCE_PLAN`
- 2026-03-28 再次关闭：`ALTER SYSTEM SET RESOURCE_MANAGER_PLAN = '' SCOPE=BOTH SID='*'` + 9 个 Window 全部 DISABLE
- 残留会话在 OTHER_GROUPS 消费组仍有 resmgr 等待，重连后消失
- **建议**: SPFILE 已清空，后续不应再恢复

## 下一步

1. **RM 彻底清除后重测 T-LIBPIN** — 预期 55+
2. **T015 替代方案** — LOCK TABLE 模拟 DDL+DML 冲突
3. **R2 <50 场景独立规则开发** — T005(死锁), T009(Seq NOCACHE), T010(HW), T012(Row Cache DDL)
4. **T021/T099 进一步优化** — MI2-005 在 SP 充足时不应走 subpool 框架
