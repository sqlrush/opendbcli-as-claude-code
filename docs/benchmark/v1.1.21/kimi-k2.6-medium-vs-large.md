# Kimi-K2.6 medium vs large capability 实测对比

**日期**: 2026-04-28
**版本**: v1.1.21（capability 双层补充修复后）
**模型**: Kimi-K2.6（Moonshot, thinking 模式）
**数据库**: OpenGauss 5.0.0 (47.251.30.180:15432)
**场景**: [og-classic-multi-fault](../../scenarios/og-classic-multi-fault.md) — 6 类相互关联根因

---

## 背景

v1.1.17 把 Kimi/GLM/Qwen/DeepSeek 系列从 `large` 降到 `medium`，理由是
观察到 GLM-5 在 strict prompt 下出"2 张表存在膨胀"这种空架子输出。

v1.1.21 在用户配置补全 capability 后做 A/B 对比，验证：

1. Kimi-K2.6（K2 系列最新版）是否能吃 strict prompt
2. medium / large 各自的得失
3. 当初降级判断对 K2.6 是否仍然成立

---

## 测试方法

同一数据库实例、同一时刻（故障流量持续）、同一问题
("当前数据库存在什么问题")、同一模型（kimi-k2.6），仅切换 capability 字段
跑两次。两次之间数据库状态有微小漂移（pg_sleep 在跑、autovacuum 进度
推进）但故障形态不变。

---

## 结果对比

### 量化指标

| 维度 | medium | large | 差异 |
|------|--------|-------|------|
| 总耗时 | **2m 34s** | 5m 20s | large 慢 2× |
| 轮数 | **2** | 6 | large 多 4 轮 |
| 工具调用次数 | 8 | **16+** | large 多 1× |
| 是否调 explain 验证执行计划 | ❌ topsql 推断 | ✅ **2 次** | strict 强制 |
| 是否调 tableinfo 验证索引存在 | ❌ 凭工具结果间接推断 | ✅ **3 次** | strict 强制 |
| 是否调 vacuum 多源验证膨胀 | ❌ 单一 bloat 来源 | ✅ vacuum + tableinfo | strict 强制 |
| 证据点条目数 | 12（环境快照充分）| **9**（更紧凑）| 整合度差异 |
| 因果链段数 | 3 步 | 3 类 | 同等 |
| 修复方案数 | 4 | 3（合并 VACUUM 进紧急措施）| large 更合理 |
| 三件套完整度（操作/风险/前置/回滚）| ✅ | ✅ | 同等 |

### 输出体积

- medium: ~5,000 字符（含格式）
- large: ~5,000 字符（含格式）

字符数相当 — large 用更多字符讲根因细节，medium 用更多字符讲环境信息。

---

## 关键定性差异

### 1. strict prompt 的"完成标准"自检规则真的生效

large 模式触发了 v1.1.15 加的 strict prompt 里的具体规则：

| 规则 | medium 是否遵循 | large 是否遵循 |
|------|---------------|----------------|
| 提到执行计划必须先调 explain | ❌ | ✅ 第 5 轮调 explain × 2 |
| 提到对象必须用 sql/tableinfo 验证存在 | ❌ | ✅ 第 4 轮 tableinfo × 3 |
| 提到膨胀需多源验证 | ❌ | ✅ vacuum + tableinfo + bloat 三源 |

medium 模式跳过这些验证（templated 模板没要求），靠直觉猜对结论。
large 模式每个结论都有显式工具数据支撑。

### 2. Kimi-K2.6 在 strict 下没出空架子

预期风险（v1.1.17 判断的依据）：strict 抽象规则会让中端模型丢 attention budget
导致输出空话。

实测：**Kimi-K2.6 完全没这个问题**。所有结论都填实数据：

- "死元组 1,995,980" 而不是 "膨胀严重"
- "执行 78,523 次，平均 499 ms" 而不是 "高频慢查询"
- "表大小 451 MB" 这种 medium 没要求但 strict 自检触发的额外细节

### 3. medium 在某些维度反而更全面

被 large 砍掉的环境信息：

| 信息 | medium | large |
|------|--------|-------|
| 当前活跃会话数（30 个）| ✅ 单独列项 | ❌ 没列 |
| 等待事件分布（全部 On CPU）| ✅ 单独列项 | ❌ |
| 0 锁/IO 等待 | ✅ | ❌ |
| 数据库重启 46 分钟（异常）| ✅ | ❌ |
| 建议执行顺序（流程化指导）| ✅ "先 → 再 → 然后"| ❌ |

这是 strict prompt 的副作用：**专注因果链 → 砍掉"现状快照"信息权重**。

### 4. 修复 SQL 质量完全一致

两边的 `CREATE INDEX CONCURRENTLY` / `idle_in_transaction_session_timeout`
建议完全一样，三件套（操作 / 风险 / 前置 / 回滚）都齐。
**修复建议层面 medium 和 large 没差异**。

---

## 决策矩阵

| 场景 | 推荐 capability |
|------|----------------|
| **生产事故快速止血** | medium（2 倍速，输出已完整）|
| **正式根因报告 / 写文档 / 给上级看** | large（每个结论有 explain/tableinfo 显式验证）|
| **首次诊断陌生数据库** | large（环境探查更彻底，能发现细节问题）|
| **二次诊断 / 同一问题复查** | medium（已有上下文，无需重复验证）|
| **CI / 自动化批量诊断** | medium（速度 + 成本敏感）|
| **需要给客户出审计报告** | large（证据链可追溯）|

---

## 对 v1.1.17 capability 分类的结论

**保持 Kimi-K2.6 默认 medium，但 v1.1.17 的"中端模型不能吃 strict"判断需要修正**：

- 这个判断对 GLM-5（旧）成立 — 当时实测过空架子
- **对 Kimi-K2.6（K2 系列 1T MoE 32B active）不成立** — 完全能跟上 strict
- 大概率对 GLM-5.1（新）也不成立 — 需单独 A/B 验证
- 大概率对 MiniMax-M2.7（456B MoE）也不成立 — 需单独 A/B 验证

**建议**：

1. 默认值：保持 `medium`（速度优先 + 安全默认）
2. capability 注释里加一句："对 K2 / GLM-5.1 / M2.7 这类 200B+ 中端模型，
   高严谨场景可手动改 large，输出更详尽但慢 2×"
3. 后续 benchmark：补 GLM-5.1、MiniMax-M2.7、DeepSeek-V3 的 medium vs large
   对比，验证 v1.1.17 判断对每个模型是否仍成立

---

## 副产品：bug 修复链验证

这次 A/B 测试同时验证了 v1.1.21 一系列修复都生效：

| 修复 | 体现 |
|------|------|
| streamRound side-effect content flush | 完整 5000 字输出，没被吞 |
| capability 双层补充 | 两次都有正确的 prompt 变体 + 策略 |
| strip_think 字段 | 输出干净，没有 `<think>` 标签泄漏 |
| saveSession 最终 assistant | session 完整保存（46 条 vs 之前 31 条）|
| batch progress to stderr | 6 轮进度都流到 stderr，stdout 干净 |

5 个 bug 修复**层层叠加**才让今天能跑出这个对比 — 任何一个没修，结果都会被污染或被掐断。
