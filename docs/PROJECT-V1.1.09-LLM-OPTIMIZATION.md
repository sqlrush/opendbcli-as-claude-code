# OpenDB v1.1.09 — LLM 能力优化专项

> 状态：规划中
> 基准版本：v1.1.08
> 目标版本：v1.1.09
> 预计工期：5-7 天
> 评估方式：glm5 A/B benchmark + Claude Opus 4.7 量化评估

---

## 1. 背景

v1.1.08 完成 OG Engine 上下文/记忆/画像专项（7 个接线断点修复）后，
架构上通了但引入了 LLM 能力倒退的可量化风险：

1. Session 复用 → 主题漂移污染
2. LLM 写 memory 质量不受控 → 错误持久化
3. System prompt 膨胀 → 成本 + 截断风险上升

本版本目标：一次性把 P0+P1 级优化全部落地，并用 glm5 做端到端 A/B
benchmark 证明新版能力不差于且在多数维度好于基准版本。

---

## 2. 实施清单（6 项）

### P0（3 项）

#### 2.1 工具选择速查表（Profile 层，0.5d）
**问题**：LLM 对"慢 SQL"这类问题，经常先 /sessions 再 /slowsql，
浪费 1 轮。

**方案**：OG profile 加"按问题类型的工具入口速查"章节：
```
| 用户问题关键词 | 优先工具 | 理由 |
|---|---|---|
| 慢 SQL / 卡 | /topsql | 直接列 Top 耗时 SQL |
| 会话数 / 连接 | /sessions | 总览；/activesessions 只看活跃 |
| 死锁 / 阻塞 | /blocktree | 直接锁链 |
| XID / wraparound | /xid | 专用 |
| 膨胀 / 碎片 | /bloat | 专用 |
| 健康巡检 | /health | 24 项综合 |
| 内存 / buffer | /gsmem | shared buffers + engine memory |
```

**验收**：在相同 prompt 下，v1.1.09 的第一轮工具选择比 v1.1.08 更精准
（用 benchmark #9 "连接数正常吗"验证）。

#### 2.2 重复工具调用防护（Engine 层，1d）
**问题**：LLM 偶尔对同一工具连调 3 次（浪费预算）。

**方案**：在 `internal/engine/tool/orchestrator.go` 加调用去重缓存：
- 缓存 key = skill.Name + params hash
- 近 3 轮内命中 → 返回缓存结果 + 提示 LLM "已有此结果，请基于其继续分析"
- 不命中 → 正常执行并写缓存

**验收**：benchmark 中的平均工具调用数下降 10%+（新版比基准）。

#### 2.3 Prompt Cache 接入（Provider 层，2d）
**问题**：system prompt（OG profile 280 行 + PROFILE.md + MEMORY.md）
每次都全量发送，input token 浪费严重。

**方案**：
- Anthropic：在 provider adapter 把 system prompt 分段加
  `cache_control: {"type": "ephemeral"}` 标签
- DeepSeek：自动 cache（不需要显式标签）
- OpenAI：根据 OpenAI Prompt Caching 策略（> 1024 tokens 才缓存）

**验收**：
- 连续 3 次 /llm 同 instance 时，从第 2 次起 input token 降 50%+
- 响应首字节时间缩短（待实测）

### P1（3 项）

#### 2.4 四层诊断策略 system prompt 重构（1d）
**问题**：memory `feedback-diag-four-layer-strategy` 定义了策略
（告警主线 → 关联问题 → 当前对比 → 综合评估排名），但 OG profile
未强制结构化输出。

**方案**：OG profile 加输出模板示范：
```markdown
## 一、告警主线
（本次告警/异常的直接证据）

## 二、关联问题
（告警之外但值得关注的 N 个问题）

## 三、当前对比 burst
（告警时 vs 现在的差异）

## 四、综合评估排名
1. [HIGH] xxx
2. [MEDIUM] yyy
3. [LOW] zzz
```
同时加 few-shot 示例 1 个。

**验收**：benchmark 中的诊断输出 80%+ 按此结构。

#### 2.5 Memory 加载上限 + /session new（1d）
**问题**：
- MEMORY.md 索引随时间膨胀，每次注入 system prompt 成本上升
- 用户换话题时没法手动开新 session

**方案**：
- Builder 加载 MEMORY.md 索引时截断到最近 10 条（按 mtime）
- 加 `/session new` shared skill：调用后下次 /llm 强制 NewSessionID
  （绕过 ResumeOrNew）

**验收**：
- memory 条目 > 20 时仍不会爆 system prompt
- /session new 后续 /llm 无历史污染

#### 2.6 截断恢复回归测试（0.5d）
**问题**：memory `feedback-sse-finishreason-lesson` 指出截断恢复
脆弱（多路径都要改），v1.1.08 后 system prompt 膨胀放大触发率。

**方案**：加集成测试 `internal/engine/truncation_recovery_test.go`：
- 构造一个 prompt + history 接近 context 上限的 case
- mock provider 返回 `FinishReason=length` 的不完整输出
- 断言 `Result.Truncated == true` 且 `TurnsUsed > 1`（走了恢复路径）

**验收**：测试全绿；当前 v1.1.08 跑一次证明 bug 未回归。

---

## 3. Benchmark 设计

### 3.1 测试环境
- 实例：openGauss 5.0.0 on 47.251.30.180:15432
- 模型：glm5（本地 `~/.opendb/models/glm5.yaml`）
- 状态：**每次 benchmark 前清空** `~/.opendb/sessions/og/` 和
  `~/.opendb/memory/og/`（保证干净起点）

### 3.2 10 个复杂 Prompt 场景（均需造压后触发）

空实例场景太简单，glm5 级别模型一次工具调用就能解出来，区分不出
优化效果。下表每个场景都需要配套造压让 OG 实例有真实症状，prompt
故意模糊以考察 LLM 的根因定位能力。

每个 prompt 跑 **3 次取中位数**（glm5 输出不稳定，单次不能判断）。

| # | 场景 | 造压 | Prompt（故意模糊） | 预期工具链 + 根因 |
|---|---|---|---|---|
| 1 | XID wraparound + VACUUM 阻塞 | 20min 长事务 + 大表 UPDATE → xmin 持有阻塞 VACUUM，dead_tup 持续上涨 | "最近数据库越来越慢，查一下" | /xid → /longtx → 关联发现 xmin 阻塞；给 kill + 手动 VACUUM FREEZE |
| 2 | Index bloat 导致 SeqScan 退化 | 50 万行大表 + 反复 UPDATE 不 VACUUM，idx bloat > 40% | "有张表的查询越来越慢" | /topsql → /explain 看 actual_time → /indexhealth → REINDEX CONCURRENTLY |
| 3 | idle in transaction 堆积 | 50 个 BEGIN 不 COMMIT 的会话 | "数据库连接数快满了，急" | /sessions → /activesessions 看 `idle in transaction` → 给 idle_in_transaction_session_timeout + kill |
| 4 | WAL 冲高 = max_wal_size 过小 | ALTER SET max_wal_size=80MB + 大批量 INSERT | "WAL 增长很快，是不是有异常" | /wal → /checkpoint 看 req/timed 倒挂 → ALTER SYSTEM SET max_wal_size |
| 5 | 慢查询 = 统计信息过期 | 大表建索引后插 100 万行但不 ANALYZE | "这查询慢得离谱 `SELECT * FROM bench_t5 WHERE uid=123`" | /explain 看 estimated vs actual rows 巨大偏差 → /tableinfo last_analyze=NULL → ANALYZE + SQL |
| 6 | 续问（session 复用） | 紧接 #5 | "针对刚才的发现给修复 SQL 和长期建议" | 是否记得 #5 的表名和原因；给 ANALYZE + autovacuum_analyze_scale_factor |
| 7 | 主题切换（前轮污染防护） | 与 #6 无关 | "换个话题，查一下逻辑复制有没有延迟" | 不被 "统计信息" 主题粘住；路由到 /replication /slots /logicalslots |
| 8 | TOAST bloat（heap 已 VACUUM） | 宽表含大 TEXT 字段，反复 UPDATE，TOAST bloat 严重 | "我已经 VACUUM 过，为什么空间还在涨" | /bloat → /toasttable → 发现 TOAST bloat；给 pg_repack 或 VACUUM FULL |
| 9 | 多症状混淆（主因是 checkpoint 风暴） | 场景 4 + 场景 3 同时 | "系统整体变慢，CPU 使用率也在涨，找找问题" | 多工具合理使用不重复；四层策略输出；checkpoint 标 HIGH、连接泄漏 MEDIUM |
| 10 | 综合健康巡检 | 以上所有场景叠加 | "给我这个实例一份完整的健康报告，覆盖内存、WAL、复制、VACUUM、索引、会话" | 覆盖度 / 结构化输出 / 每项 SQL / 主动 memory_update 更新 PROFILE |

### 3.3 造压脚本

**新增文件**：
- `scripts/og_load_complex.sh` — 一次性造场景 1-5 + 8 的负载
- `scripts/og_load_cleanup.sh` — benchmark 完成后清理（kill 长事务/idle 会话、DROP bench_ 表、RESET max_wal_size）

核心造压逻辑：

```bash
# 场景 1：长事务 + 大表 UPDATE → VACUUM 阻塞
CREATE TABLE bench_t1 (id SERIAL, data TEXT);
INSERT INTO bench_t1 SELECT i, md5(random()::text) FROM generate_series(1,500000) i;
# 后台启动 25 分钟长事务：
(echo "BEGIN; SELECT 1; SELECT pg_sleep(1500);" | gsql -d postgres) &
UPDATE bench_t1 SET data = md5(random()::text) WHERE id < 50000;

# 场景 2：Index bloat (关闭 autovacuum 保留 dead tuples)
CREATE TABLE bench_t2 (uid INT, status INT, payload TEXT);
CREATE INDEX idx_bench_t2_uid ON bench_t2(uid);
ALTER TABLE bench_t2 SET (autovacuum_enabled = false);
INSERT INTO bench_t2 SELECT i, 1, md5(i::text) FROM generate_series(1,500000);
-- 5 次反复 UPDATE
UPDATE bench_t2 SET status = status + 1; -- x 5

# 场景 3：50 个 idle in transaction
for i in {1..50}; do
  (echo "BEGIN; SELECT 1; SELECT pg_sleep(1500);" | gsql -d postgres) &
done

# 场景 4：max_wal_size 调小 + 批量 INSERT
ALTER SYSTEM SET max_wal_size = '80MB';
SELECT pg_reload_conf();
CREATE TABLE bench_t4 (id SERIAL, data TEXT);
INSERT INTO bench_t4 SELECT i, repeat(md5(i::text),10) FROM generate_series(1,1000000);

# 场景 5：大表不 ANALYZE
CREATE TABLE bench_t5 (uid INT, name TEXT);
CREATE INDEX idx_bench_t5_uid ON bench_t5(uid);
INSERT INTO bench_t5 SELECT i, md5(i::text) FROM generate_series(1,1000000);
# 故意不跑 ANALYZE

# 场景 8：TOAST bloat (heap VACUUM 过但 TOAST 仍膨胀)
CREATE TABLE bench_t8 (id SERIAL PRIMARY KEY, content TEXT);
INSERT INTO bench_t8 SELECT i, repeat(md5(i::text),200) FROM generate_series(1,10000);
UPDATE bench_t8 SET content = repeat(md5(random()::text),200); -- x 5
VACUUM bench_t8;  # heap 清了，TOAST 仍 bloat
```

**造压验证**（造压完运行 opendb 验证症状已触发）：
- `/xid` 显示 xid_age 上升（场景 1）
- `/bloat` 显示 bench_t2 dead_pct > 40%（场景 2）
- `/sessions` 显示 `idle in transaction` 状态 ≥ 40 个（场景 3）
- `/wal` + `/checkpoint` 显示 WAL 快速增长 + req > timed（场景 4）
- `/explain SELECT * FROM bench_t5 WHERE uid=123` 显示 SeqScan（场景 5）
- `/toasttable` 显示 bench_t8 toast_size 可观（场景 8）

### 3.4 执行流程

```bash
# 1. 造压
bash scripts/og_load_complex.sh

# 2. 切换到 glm5
sed -i '' 's/active_model: .*/active_model: glm5/' ~/.opendb/config.yaml

# 3. 基准版本（v1.1.08）— 每 prompt 跑 3 次
git checkout 817e7df5
go build -tags full -o opendb ./cmd/opendb/
for run in 1 2 3; do
  rm -rf ~/.opendb/sessions/og ~/.opendb/memory/og
  for i in 1 2 3 4 5 7 8 9 10; do
    # 场景 6 是 #5 的续问，不清 session
    ./opendb -c og "/llm $(prompt $i)" > docs/benchmark/v1.1.08/run-$run/prompt-$i.txt
    [ $i -eq 5 ] && ./opendb -c og "/llm $(prompt 6)" > docs/benchmark/v1.1.08/run-$run/prompt-6.txt
  done
done

# 4. 新版本（v1.1.09）— 同样跑 3 次
git checkout main
# ... 同上

# 5. 切回原模型 + 清理造压
sed -i '' 's/active_model: .*/active_model: opus/' ~/.opendb/config.yaml
bash scripts/og_load_cleanup.sh
```

### 3.4 评估维度（Claude Opus 4.7 评分）

对每个 prompt 逐个打分（0-10）：

| 维度 | 权重 | 评分标准 |
|---|---|---|
| 准确性 | 30% | 数据是否真实 / 结论是否对 / 无幻觉 |
| 工具使用效率 | 20% | 轮次合理 / 无重复 / 工具选择是否最短路径 |
| 可执行性 | 20% | 是否给原生 SQL / 是否可直接跑 |
| 结构质量 | 15% | 是否按四层策略 / 标题清晰 / 无 NULL 渲染 |
| 信息完整性 | 15% | 字段是否齐全 / 上下文是否到位 |

### 3.5 量化指标（自动统计）

| 指标 | 从哪里取 |
|---|---|
| 平均轮次 | 输出里的 "AI 诊断 (auto, N 轮)" |
| Token 消耗 | 输出里的 "tokens xxx" 或 summary |
| 工具调用数 | 计数输出中的"调用 xxx"次数 |
| 输出字数 | wc -c |
| 用时（秒） | `time` |

---

## 4. 预期产物

```
docs/benchmark/
├── v1.1.08/
│   ├── prompt-01.txt ... prompt-10.txt
│   ├── metrics.json    ← 轮次/token/工具/字数/用时
│   └── README.md
├── v1.1.09/
│   ├── prompt-01.txt ... prompt-10.txt
│   ├── metrics.json
│   └── README.md
└── report-v1.1.08-vs-v1.1.09.md
    ├── 汇总对比表（维度得分 + 量化指标 Δ）
    ├── 每个 prompt 的逐条对比
    ├── Opus 评估结论（新版是否提升 / 提升幅度 / 回归点）
    └── 下个版本建议
```

---

## 5. 验收标准

**强制**：
- ☐ 新版 6 项全部实施并通过单测
- ☐ `go test -race ./internal/...` 全绿
- ☐ glm5 跑完两组 10 prompt（20 个输出文件）
- ☐ 量化指标：新版平均轮次 ≤ 基准，新版 token ≤ 基准 × 1.1，
      SQL 跑通率 ≥ 基准
- ☐ Opus 评分：新版加权总分 ≥ 基准 × 0.98（即倒退不超过 2%）

**期望**：
- 新版加权总分 ≥ 基准 × 1.05（即提升至少 5%）
- Prompt Cache 命中时 input token 节省 ≥ 30%

---

## 6. 风险

1. **glm5 输出不稳定**：同一 prompt 两次输出差异大 → 跑 3 次取中位数
2. **v1.1.08 基准跑不动某些新 skill**：已通过 v1.1.08 测试，理论上都可用
3. **Prompt Cache 改动破坏现有 provider 行为**：必须过完整测试套件才能合并
4. **Opus 评分主观性**：用明确的评分 rubric（见 3.4），并附每个 prompt
   的 Claude 评估理由

---

## 7. 发版

- 6 项全部完成 + benchmark 报告通过 → v1.1.09 (patch)
- 按 feedback-release-cadence：push GitHub，不发云主机
- 如果 Opus 评估"新版严重倒退"→ 回滚到 v1.1.08 重做

---

## 8. 开工步骤（本次执行顺序）

1. ☐ v1.1.09 实施 6 项优化
2. ☐ 写 benchmark 自动化脚本 `scripts/llm_benchmark.sh`
3. ☐ 配 glm5 为 active_model
4. ☐ 清 session/memory，跑 v1.1.08 (checkout) 10 prompts
5. ☐ checkout main, 跑 v1.1.09 10 prompts
6. ☐ 统计指标，Opus 评估每个 prompt
7. ☐ 写 `docs/benchmark/report-v1.1.08-vs-v1.1.09.md`
8. ☐ commit + push v1.1.09
