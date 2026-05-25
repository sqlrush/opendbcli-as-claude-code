# v1.2.3 验证 — GaussDB 路由 + sqltune SQL_ID 修复回归测试

**测试时间**: 2026-05-20 16:39 — 17:10 CST（~31 分钟）
**测试模型**: Qwen3.6-35B-A3B (本地 vLLM 8081, **tool_mode: prompt**)
**测试连接**: `gauss_local`（`db_type: gaussdb` → 加载 GaussDB driver → 路由到 DBType="gaussdb" → skill 复用 og 实现）
**评分基线**: v1.2.1 PromptToolAdapter 5 场景验证（318/425, 74.8%）

## 修复内容回顾（v1.2.3）

- **Bug A**：GaussDB `/sqltune <SQL_ID>` 0.0s `plan collection failed; cannot continue`
  - 根因：`internal/gaussdb/skill/query/sqltune_skill.go` 漏搬 og 的
    `looksLikeSQLID` + `fetchLiteralSQLByID` + `blindNullSubstitute`
  - 修法：GaussDB 后端全收敛到 og 实现，删 1200+ 行 gaussdb 独立 sqltune/sqltuner

## 预验证（before benchmark）

| 验证项 | 结果 |
|---|---|
| GaussDB driver 连接本地 OG 5.0.3 | ✅ |
| `/health` skill via GaussDB route | ✅ 数据完整 |
| `/sqltune 581990336`（SQL_ID 路径）| ✅ banner 出、fetchLiteralSQLByID 通、Phase A 数据全 |

## 5 场景 × 3 轮（15 轮总）

| 场景 | R1 | R2 | R3 | 备注 |
|---|---|---|---|---|
| S1 聚类诊断（模糊抱怨 → 根因）| 124s | 54s | 70s | LLM agent 跑 8 轮, 给出根因 + 紧急措施 + 长期方案 |
| S2 WDR 解读 | 0s* | 74s | 61s | R1 = `/wdr` 直查命令（预期 0s 不调 LLM）|
| S3 SQL 调优（关键场景）| 0s* | **185s** | 131s | R1 = `/topsql` 直查；**R2 agent 第 4 轮自主调用 sqltune，对 SQL_ID 3402615047 出 5 候选优化方案** |
| S4 锁阻塞排查 | 114s | 33s | 17s | LLM 收敛快，未循环 |
| S5 参数检查 | 63s | 67s | 134s | 给出 ALTER SYSTEM 具体语句 |

总计：**1127s LLM agent 时间**（不含间隙）。0s 标 `*` 的是直查 `/command`，不调 LLM。

## 关键发现

### 1. v1.2.3 核心修复验证 PASS

S3R2 prompt: "对其中耗时最高的非 fault 业务 SQL 用 /sqltune 调优"

LLM agent 路径：
```
第 1 轮 → 调用 topsql（找到 SQL_ID 3402615047）
第 2 轮 → 调用 sqlfetch（拉 SQL 文本）
第 3 轮 → 调用 explain（看执行计划）
第 4 轮 → 调用 sqltune（出 5 候选优化报告）← v1.2.2 卡死，v1.2.3 通过
```

sqltune 输出含：
- SQL Tuning Report (SQL_ID: 3402615047)
- 3 个具体方案（rewrite + hint + stats）
- EXPLAIN 验证 / 风险评估 / 回滚方案 全套

### 2. GaussDB 路由的等价性确认

测试使用 `db_type: gaussdb` 连接同一个 OpenGauss 5.0.3 实例（127.0.0.1:5433），现象：

- GaussDB driver 兼容 OG 实例（wire protocol 兼容）
- 所有 skill（health / topsql / sqltune / explain / activesessions / waits 等）输出格式与 og 完全一致
- 唯一肉眼可见差异：交互提示符显示 `GaussDB:` 而不是 `openGauss:`（来自 `internal/ui/repl.go:2004`）

**结论**：v1.2.3 的"GaussDB = og + 换驱动"决策在功能层 100% 验证通过。

### 3. 无回归 / 无 crash

- 0 panic
- 0 `plan collection failed`
- 0 `cannot continue`
- 0 session 中断

## 与 v1.2.1 基线对比

|  | v1.2.1（og 连接） | v1.2.3（gauss_local 连接）|
|---|---|---|
| 模型 | Qwen3.6-35B-A3B promptmode | 同上 |
| 场景 | 5 × 3 = 15 轮 | 同上 |
| 总时长 | ~30 min | ~31 min |
| 关键场景 sqltune | ✅ 但在 og 路径 | ✅ 在 GaussDB 路径，**修复 v1.2.2 卡死** |
| 失败/崩溃 | 0 | 0 |

## 输出 artifacts

- `scenarios/scenario1.log` (261 行) — 聚类诊断 3 轮
- `scenarios/scenario2.log` (281 行) — WDR 解读 3 轮
- `scenarios/scenario3.log` (172 行) — SQL 调优 3 轮（**含 v1.2.3 关键验证**）
- `scenarios/scenario4.log` (60 行) — 锁阻塞排查 3 轮
- `scenarios/scenario5.log` (138 行) — 参数检查 3 轮

合计 912 行真实多轮对话日志。

## 待办（不影响本次发版）

- **Bug B**（v1.2.4）：PromptToolAdapter 只接到 og /diagnose，需在 engine 层全局接入
  - 当前 dbaa 现场症状：自由对话 `sql id <N> 给出优化建议` → 12s 通用 markdown（零工具调用）
  - 见 [memory: todo-bug-prompttool-adapter-only-og-diag](file:///Users/sqlrush/.claude/projects/-Users-sqlrush-opendb/memory/todo-bug-prompttool-adapter-only-og-diag.md)
