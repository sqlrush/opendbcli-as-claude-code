# v1.2.1 PromptToolAdapter 5 场景验证 — Qwen3.6 35B-A3B

**测试时间**: 2026-05-19
**测试模型**: Qwen3.6-35B-A3B (本地 llama-server, FC **关闭**)
**对比基线**: PromptToolAdapter (v1.2.1 新引入)
**评分模型**: Claude Opus 4.6

## 总分

**318/425 (74.8%)** — 达到 ≥70% 的客户生产可用门槛.

## 各场景得分

| 场景 | 得分 | 评级 |
|---|---|---|
| S1 聚类诊断 (3 轮) | 49/85 (58%) | 🔴 工具循环不收敛 |
| S2 WDR 解读 (3 轮) | **80/85 (94%)** | 🟢 优秀 |
| S3 SQL 调优 (3 轮) | 51/85 (60%) | 🟡 凭空数字 |
| S4 锁阻塞排查 (3 轮) | 64/85 (75%) | 🟡 追问放弃线索 |
| S5 参数检查 (3 轮) | **74/85 (87%)** | 🟢 简洁高效 |

## 6 维度平均

| 维度 | 平均分 |
|---|---|
| A 工具选择 | 3.6 / 5 |
| B 参数正确 | 3.8 / 5 |
| C 数据真实 | **4.1 / 5** |
| D 推理质量 | 4.0 / 5 |
| E 方案可执行 | 3.1 / 5 ← 最弱 |
| F 跨轮上下文 | **4.6 / 5** ← 最强 |

## 文件清单

- `opus-scoring-report.md` — Opus 完整评分报告（亮点 + 失分点 + 改进建议）
- `scenarios/scenario1-cluster-diag.log` (248 行) — 模糊抱怨 → 根因定位
- `scenarios/scenario2-wdr-analysis.log` (402 行) — WDR 报告深度解读
- `scenarios/scenario3-sql-tuning.log` (147 行) — 单 SQL 调优工作流
- `scenarios/scenario4-lock-block.log` (104 行) — 锁阻塞排查
- `scenarios/scenario5-config-review.log` (124 行) — 内存参数检查

合计 5 场景 × 3 轮 = **13 轮真实多轮对话** (S1R1 单轮独立跑, 其余 12 轮跨轮 session 续接).

## 测试方法

1. 切到 `qwen36-35b-promptmode` 模型 (在 config.yaml 加 `tool_mode: prompt`)
2. 每场景 3 轮 batch 调用 `opendb -c og "<prompt>"`, session 通过 `session.ResumeOrNew` 自动续接
3. 5 场景全部 log 拼成 ~30KB 输入文本
4. 切到 Opus 4.6, 通过 `opus-forwarder` 直接发 API 评分, 严格禁止编造 (评分 prompt 中明确"必须基于 log 原文")
5. Opus 按 6 维度 × 5 分制打分 + 给亮点/失分点/改进建议

详见 `opus-scoring-report.md`.

## v1.2.x 改进方向 (来自 Opus 评分建议)

1. **轮数硬限 + 工具去重检测** — 解决 S1 R2/R3 死磕 19 轮问题
2. **SQL 错误 HINT 字段回传 LLM** — 解决"字段引用错"不自修
3. **占位符兜底** — 方案含 `<...>` 必须先补齐才放出
4. **跨场景一致性校验** — sqltune 失败时引导对照 WDR 历史
5. **追问追到底** — 用户追问关联前轮非零结果时必须复用该工具
