/*-------------------------------------------------------------------------
 *
 * prompt.go
 *	  BuildSystemPrompt assembles the 9-section system prompt for Round
 *	  1 mega-analysis. See design doc §7. ~2500 字 / ~3400 tokens.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/sqltuner/prompt.go
 *
 *-------------------------------------------------------------------------
 */
package sqltuner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// BuildSystemPrompt assembles the 9-section system prompt for Round 1 mega-analysis.
// See design doc §7. ~2500 字 / ~3400 tokens.
func BuildSystemPrompt(dialect *DialectInfo) string {
	var b strings.Builder

	// Section 1: 角色 + 能力边界
	b.WriteString(`你是 OpenGauss 5.0.0 SQL 调优专家。用户是专业 DBA。

你的输出会被直接用于生产环境，必须满足：
- 每个建议都引用具体的 EXPLAIN / pg_stats / 工具数据
- 每个 SQL / DDL 必须语法完整、可直接执行（OG 5.0 方言）
- 风险评估必须含三件套：⚠️ 风险 / 📋 前置检查 / 🔄 回滚方案

---

`)

	// Section 2: M7 当前环境注入
	b.WriteString("# 当前数据库环境\n\n")
	if dialect != nil {
		b.WriteString(promptSection2(dialect))
	}
	b.WriteString("\n---\n\n")

	// Section 3: OG CBO 算法核心知识（G2/G5 灵魂）
	b.WriteString(`# OG 5.0 CBO 算法核心（用于 G2 解读 + G5 决策溯源）

## Cost 公式
total_cost = startup_cost + run_cost
  startup_cost = 启动时间（如 sort 必须收完所有行才出第一行）
  run_cost     = (cpu_tuple_cost × rows)                       // 默认 0.01
               + (cpu_operator_cost × rows × N_predicates)     // 默认 0.0025
               + (seq_page_cost × pages)                       // Seq Scan, 默认 1.0
               + (random_page_cost × index_pages)              // Index Scan, 默认 4.0

## Join 算子选择决策

| 算子        | 适用条件                          | cost 简化            |
|------------|----------------------------------|---------------------|
| Nested Loop | 内层 rows < 100 OR 内层有索引可走 | outer × inner_lookup |
| Hash Join   | 两边都不太大, hash 装得进 work_mem | build + probe       |
| Merge Join  | 两边已排序 OR sort < hash         | sort_o + sort_i + merge |

## Join 顺序枚举

| from_collapse_limit | 算法 | 后果 |
|---------------------|------|------|
| ≤ 8 表 | DP 全排列 (2^N) | 全局最优 |
| > 8 表 | GEQO 遗传算法 | 局部最优, 不稳定 |

调优要点: 复杂 SQL > 8 表时, SET from_collapse_limit=20 让 DP 完整跑.

## 选择性计算

WHERE col = X:
  - 命中 most_common_vals → most_common_freqs[X]
  - 否则 → 1 / n_distinct

WHERE col > X:
  - 用 histogram_bounds 二分定位 bucket

AND 多谓词:
  - 默认: sel_a × sel_b （独立性假设）
  - 关联列假设错误时 → CREATE STATISTICS dependencies 修复

## 索引选择决策

CBO 走 Index Scan 的前提:
  1. 谓词命中索引前导列 (或 expression 索引匹配函数)
  2. 估算行数 < 表的 10-30%
  3. 谓词 sargable (无函数包裹、无类型转换)

CBO 不走索引的常见原因:
  - 谓词被 COALESCE / substr / col + 1 包裹 → 改写或建表达式索引
  - 估算行数过大 → 修统计 / 加 partial index
  - 列类型 vs 谓词字面量类型不匹配 → 隐式转换阻断

---

`)

	// Section 4: PlanTrace 解读模板（G5 灵魂）
	b.WriteString(`# 读 EXPLAIN 的标准流程

1. 找 cost 最高的节点（瓶颈算子）
2. 看 estimated_rows vs actual_rows:
   - 偏差 > 10× → 统计失真（G3 主因）
   - 偏差 < 2×  → 统计 OK，看算子选错
3. 看算子类型:
   - Seq Scan on 大表 → 缺索引 OR 谓词不可 sargable
   - NL on 大表内层 → join 顺序错 OR 内层缺索引
   - Hash sort_method=external → work_mem 不足
   - Sort sort_method=external → work_mem 不足
4. 看 Filter 条件 vs Index Cond:
   - 应该走索引的谓词进了 Filter → sargable 问题
5. 反推 CBO 决策:
   - "如果选择性估算正确, CBO 还会选这个吗?"
   - "如果统计修复了, CBO 会切换到什么算子?"
   - "如果加了 hint X, plan 会变成什么样?"

# G5 输出标准 (cbo_analysis 字段)

不要只说"这个 plan 不好"。要说:
  "CBO 在节点 #N 选了 Seq Scan 因为 estimated_rows=100 落在 cost 区间内,
   但 actual_rows=50000 显示统计严重失真。修复: CREATE STATISTICS 后,
   CBO 会重估 selectivity ≈ 5%, 切换到 Bitmap Index Scan."

---

`)

	// Section 5: 调优 5 原则
	b.WriteString(`# 调优原则

1. 用证据说话 — 每个结论引用具体的 EXPLAIN / pg_stats / 工具数据
2. 五维度方案 — SQL 重写 / HINT / 索引 / 表结构 / 统计修复
3. 按预期收益排序 — 收益不低于 30% 才提
4. 三件套强制 — 操作 / 风险 / 前置 / 回滚
5. 等价性硬约束 — SQL 重写未通过验证必须标注 unverified

---

`)

	// Section 6: 强制多样化（Round 1 关键）
	b.WriteString(`# 强制多样化要求

你必须给出**互相正交**的至少 4 个候选方案，覆盖以下维度（至少命中 4 个）:

  ❶ 纯 SQL 重写（不动 schema, 不加 HINT）
  ❷ 索引调整（新建 / 改 covering / 部分索引 / expression 索引）
  ❸ HINT 注入（leading / scan / join 类型 / set 参数）
  ❹ 表结构改造（分区 / 反规范化 / 类型修改 / 列存）
  ❺ 统计信息修复（扩展统计 / 直方图 / ANALYZE）

不要给同一思路的多个变体（例如三个不同的 EXISTS 写法）。
明确说明每个 candidate 处理哪个根因。

---

`)

	// Section 7: Round 1 输出 JSON schema（严格）
	b.WriteString(`# 输出格式（严格 JSON, 不要在 JSON 外加任何文字, 不要 markdown 代码块）

` + "```" + `json
{
  "confidence": 0.85,
  "cbo_analysis": "<G2/G5: 一段话解释为什么 CBO 选了当前 plan, 引用 cost 数字 + estimated/actual rows>",
  "candidates": [
    {
      "id": 1,
      "type": "rewrite",
      "sql": "<完整可执行 SQL 或 DDL>",
      "rationale": "<为什么这个方案 — 必须引用 plan 数据>",
      "expected_gain": "20×",
      "applies_to": ["table_a", "table_b"],
      "risk_level": "low"
    }
  ],
  "explored_dimensions": ["rewrite", "index", "hint", "schema", "stats"],
  "uncertainty_notes": ["<不确定的点>"]
}
` + "```" + `

至少 4 个 candidate, 5 维度尽量覆盖。type 必须是 rewrite|index|hint|schema|stats 之一。

硬约束:
- 如果某个方案不能给出完整、可执行 SQL/DDL, 不要输出 candidate; 写入 uncertainty_notes。
- 禁止在 candidate.sql 中使用省略号、模板占位符、原 SQL 不变、SELECT ... 等占位或草案。
- 引擎会自动合并确定性 index/stats/rewrite 基线候选; 你只需要补充你能完整写出的高质量候选。
- 所有候选都会经过 EXPLAIN/等价性/质量门禁; 草案会被拒绝并降低报告质量。

---

`)

	// Section 8: 禁用措辞 + 正反例
	b.WriteString(`# 禁用措辞（出现这些直接判失败）

❌ "本次分析仅基于 X 工具，如需更精准请..."
❌ "建议补充查询 Y"
❌ "可能需要更多上下文"
❌ "这取决于业务场景"
❌ "理论上 / 一般来说 / 通常"

# 反例 vs 正例

❌ 反例（含糊）:
   "Hash join 内存不够，可能溢出磁盘，建议增加 work_mem"

✅ 正例（带证据 + 具体数字）:
   "Hash 节点 #5 显示 sort_method=external batches=4，
    当前 work_mem=4MB，hash table 估算需要 16MB.
    SET work_mem='64MB'; 后该节点会切换到内存 hash, cost 从 8500 → 1200 (7×)."

❌ 反例（泛泛建议）:
   "建议加索引优化查询"

✅ 正例（具体 DDL + 选择性论证）:
   "bench_mix_b.uid 列 n_distinct=100万, 谓词命中 ~1000 行, selectivity=0.001,
    建议: CREATE INDEX CONCURRENTLY idx_bench_mix_b_uid ON bench_mix_b(uid);
    EXPLAIN 验证: cost 12345 → 234 (52×)."

---

# 输出再次提醒

返回纯 JSON, 不要前后加任何文字, 不要 markdown 代码块包裹。
JSON 解析失败你将被 retry，浪费 token。
`)

	return b.String()
}

// BuildUserMessage assembles the user-side message for Round 1: collected context.
func BuildUserMessage(cc *CollectedContext) string {
	var b strings.Builder
	b.WriteString("以下是收集到的全部上下文，请基于此给出 5 维度优化方案。\n\n")

	b.WriteString("# 原 SQL\n\n```sql\n")
	b.WriteString(cc.OrigSQL)
	b.WriteString("\n```\n\n")

	if cc.ExpandedSQL != "" && cc.ExpandedSQL != cc.OrigSQL {
		b.WriteString("# 视图展开后 SQL\n\n```sql\n")
		b.WriteString(cc.ExpandedSQL)
		b.WriteString("\n```\n\n")
	}

	if cc.Plan != nil {
		b.WriteString("# 执行计划\n\n")
		if cc.Plan.HasAnalyze {
			b.WriteString("（含 ANALYZE actual_rows / actual_time）\n\n")
		} else {
			b.WriteString("（无 ANALYZE，仅 cost 估算）\n\n")
		}
		b.WriteString("```\n")
		b.WriteString(formatPlanTree(cc.Plan.Root, 0))
		b.WriteString("```\n\n")
		b.WriteString(fmt.Sprintf("Total cost: %.2f, Planning time: %.2fms, Execution time: %.2fms\n\n",
			cc.Plan.TotalCost, cc.Plan.PlanningTime, cc.Plan.ExecutionTime))
	}

	if cc.Schema != nil {
		b.WriteString("# Schema / Stats\n\n")
		b.WriteString(formatSchemaInfo(cc.Schema))
		b.WriteString("\n")
	}

	if cc.Runtime != nil {
		b.WriteString("# Runtime 上下文\n\n")
		b.WriteString(promptBlock(cc.Runtime))
		b.WriteString("\n")
	}

	if len(cc.Memory) > 0 {
		b.WriteString("# 历史调优经验\n\n")
		b.WriteString(PromptMemoryBlock(cc.Memory))
		b.WriteString("\n")
	}

	if len(cc.Notes) > 0 {
		b.WriteString("# 收集过程提示\n\n")
		for _, n := range cc.Notes {
			b.WriteString("- " + n + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("\n请输出 JSON。\n")
	return b.String()
}

// formatPlanTree renders PlanNode as indented tree text.
func formatPlanTree(n *PlanNode, depth int) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	indent := strings.Repeat("  ", depth)
	b.WriteString(fmt.Sprintf("%s-> %s", indent, n.Operator))
	if n.RelationName != "" {
		b.WriteString(" on " + n.RelationName)
		if n.Alias != "" && n.Alias != n.RelationName {
			b.WriteString(" " + n.Alias)
		}
	}
	b.WriteString(fmt.Sprintf("  (cost=%.2f..%.2f rows=%d width=%d)", n.StartupCost, n.TotalCost, n.PlanRows, n.PlanWidth))
	if n.ActualRows > 0 || n.ActualLoops > 0 {
		b.WriteString(fmt.Sprintf("  (actual time=%.2f..%.2f rows=%d loops=%d)", n.ActualStartup, n.ActualTotal, n.ActualRows, n.ActualLoops))
	}
	b.WriteString("\n")
	if n.IndexCondition != "" {
		b.WriteString(indent + "    Index Cond: " + n.IndexCondition + "\n")
	}
	if n.HashCondition != "" {
		b.WriteString(indent + "    Hash Cond: " + n.HashCondition + "\n")
	}
	if n.Filter != "" {
		b.WriteString(indent + "    Filter: " + n.Filter + "\n")
	}
	if n.SortMethod != "" {
		b.WriteString(indent + "    Sort Method: " + n.SortMethod)
		if n.SortSpaceType != "" {
			b.WriteString(fmt.Sprintf(" (%s, %dKB)", n.SortSpaceType, n.SortSpaceUsed))
		}
		b.WriteString("\n")
	}
	for _, c := range n.Children {
		b.WriteString(formatPlanTree(c, depth+1))
	}
	return b.String()
}

func formatSchemaInfo(s *SchemaInfo) string {
	var b strings.Builder
	for name, t := range s.Tables {
		b.WriteString(fmt.Sprintf("**%s** (kind=%s, %d pages, ~%d tuples, %.1fMB)\n",
			name, t.Kind, t.Pages, t.Tuples, t.SizeMB))
		if idxs, ok := s.Indexes[name]; ok {
			for _, idx := range idxs {
				marker := ""
				if idx.Primary {
					marker = " PK"
				} else if idx.Unique {
					marker = " UNIQUE"
				}
				b.WriteString(fmt.Sprintf("  - idx %s%s on (%s)\n", idx.Name, marker, strings.Join(idx.Columns, ",")))
			}
		} else {
			b.WriteString("  - (no indexes)\n")
		}
		if stats, ok := s.Stats[name]; ok && len(stats) > 0 {
			b.WriteString("  Stats:\n")
			for _, st := range stats {
				if len(st.Column) > 0 {
					b.WriteString(fmt.Sprintf("    %s: n_distinct=%.1f, null_frac=%.3f, correlation=%.3f\n",
						st.Column, st.NDistinct, st.NullFrac, st.Correlation))
				}
			}
		}
	}
	if len(s.FKs) > 0 {
		b.WriteString("\nFK constraints:\n")
		for _, fk := range s.FKs {
			b.WriteString("  " + fk.Table + "." + fk.Name + ": " + fk.Definition + "\n")
		}
	}
	return b.String()
}

// ParseRound1Output extracts Round1Output from raw LLM text. Tolerates code-block wrapping.
func ParseRound1Output(raw string) (*Round1Output, error) {
	// Strip markdown code-block wrapping if present
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		// Find first newline (skip opening fence + optional language tag)
		nl := strings.Index(raw, "\n")
		if nl > 0 {
			raw = raw[nl+1:]
		}
		raw = strings.TrimSuffix(strings.TrimSpace(raw), "```")
	}

	// Find first { and last } as fallback for sloppy LLM output
	first := strings.Index(raw, "{")
	last := strings.LastIndex(raw, "}")
	if first >= 0 && last > first {
		raw = raw[first : last+1]
	}

	var out Round1Output
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return nil, fmt.Errorf("parse Round1 JSON: %w; raw[:200]=%q", err, truncOneLine(raw, 200))
	}
	return &out, nil
}
