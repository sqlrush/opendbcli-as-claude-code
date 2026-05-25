/*-------------------------------------------------------------------------
 *
 * prompt_assembly.go
 *	  Assembles the system prompt + user message + final markdown
 *	  report from neutral building blocks + dialect-specific
 *	  PromptBuilder contributions.
 *
 *	  Separated from orchestrator.go for readability — these functions
 *	  are pure string builders with no orchestration logic.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/sqltune/prompt_assembly.go
 *
 *-------------------------------------------------------------------------
 */
package sqltune

import (
	"fmt"
	"strings"
	"time"
)

// assembleSystemPrompt builds the 8-section system prompt by combining
// universal sections (5, 6, 7, 8) with PromptBuilder-provided dialect
// sections (1 role, 3 CBO, 4 plan reading, 6 hint syntax).
//
// Section numbering mirrors og's existing prompt.go for cross-reference.
func assembleSystemPrompt(b PromptBuilder, dialect *DialectInfo) string {
	var sb strings.Builder

	// Section 1: Role
	sb.WriteString("你是 ")
	sb.WriteString(b.RoleTag())
	sb.WriteString("。用户是专业 DBA。\n\n")
	sb.WriteString("你的输出会被直接用于生产环境，必须满足：\n")
	sb.WriteString("- 每个建议都引用具体的 EXPLAIN / 工具数据\n")
	sb.WriteString("- 每个 SQL / DDL 必须语法完整、可直接执行（本方言）\n")
	sb.WriteString("- 风险评估必须含三件套：⚠️ 风险 / 📋 前置检查 / 🔄 回滚方案\n\n")
	sb.WriteString("---\n\n")

	// Section 2: Environment (neutral — built from DialectInfo)
	sb.WriteString("# 当前数据库环境\n\n")
	sb.WriteString(formatDialectEnv(dialect))
	sb.WriteString("\n---\n\n")

	// Section 3: CBO knowledge (dialect-specific)
	if ck := b.CBOKnowledge(); ck != "" {
		sb.WriteString("# CBO 算法核心（用于 cbo_analysis 推理）\n\n")
		sb.WriteString(ck)
		sb.WriteString("\n---\n\n")
	}

	// Section 4: Plan reading (dialect-specific operators / failure modes)
	if pr := b.PlanReading(); pr != "" {
		sb.WriteString("# 读 EXPLAIN 的标准流程\n\n")
		sb.WriteString(pr)
		sb.WriteString("\n---\n\n")
	}

	// Section 5: Tuning principles (universal)
	sb.WriteString(`# 调优原则

1. 用证据说话 — 每个结论引用具体的 EXPLAIN / 统计 / 工具数据
2. 五维度方案 — SQL 重写 / HINT / 索引 / 表结构 / 统计修复
3. 按预期收益排序 — 收益不低于 30% 才提
4. 三件套强制 — 操作 / 风险 / 前置 / 回滚
5. 等价性硬约束 — SQL 重写未通过验证必须标注 unverified

---

`)

	// Section 6: Dimension diversity + hint syntax samples (mixed)
	sb.WriteString(`# 强制多样化要求

你必须给出**互相正交**的至少 4 个候选方案，覆盖以下维度（至少命中 4 个）:

  ❶ 纯 SQL 重写（不动 schema, 不加 HINT）
  ❷ 索引调整（新建 / 改 covering / 部分索引 / expression 索引）
  ❸ HINT 注入（leading / scan / join 类型 / set 参数）
  ❹ 表结构改造（分区 / 反规范化 / 类型修改 / 列存）
  ❺ 统计信息修复（扩展统计 / 直方图 / ANALYZE）

不要给同一思路的多个变体（例如三个不同的 EXISTS 写法）。
明确说明每个 candidate 处理哪个根因。

`)
	if hs := b.HintSyntax(); hs != "" {
		sb.WriteString("## 本方言 HINT 语法\n\n")
		sb.WriteString(hs)
		sb.WriteString("\n")
	}
	sb.WriteString("\n---\n\n")

	// Section 7: Output JSON schema (universal)
	sb.WriteString("# 输出格式（严格 JSON, 不要在 JSON 外加任何文字, 不要 markdown 代码块）\n\n")
	sb.WriteString("```json\n")
	sb.WriteString(`{
  "confidence": 0.85,
  "cbo_analysis": "<一段话解释为什么 CBO 选了当前 plan, 引用 cost 数字 + estimated/actual rows>",
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
}`)
	sb.WriteString("\n```\n\n")
	sb.WriteString("至少 4 个 candidate, 5 维度尽量覆盖。type 必须是 rewrite|index|hint|schema|stats 之一。\n\n---\n\n")

	// Section 8: Taboo phrases (universal)
	sb.WriteString(`# 禁用措辞（出现这些直接判失败）

❌ "本次分析仅基于 X 工具，如需更精准请..."
❌ "建议补充查询 Y"
❌ "可能需要更多上下文"
❌ "这取决于业务场景"
❌ "理论上 / 一般来说 / 通常"

# 输出再次提醒

返回纯 JSON, 不要前后加任何文字, 不要 markdown 代码块包裹。
JSON 解析失败你将被 retry，浪费 token。
`)
	return sb.String()
}

// formatDialectEnv renders DialectInfo as a markdown block for prompt Section 2.
func formatDialectEnv(d *DialectInfo) string {
	if d == nil {
		return "(未采集到 dialect snapshot)"
	}
	var sb strings.Builder
	if d.Version != "" {
		sb.WriteString("- 产品: " + d.Version + "\n")
	}
	if len(d.Extensions) > 0 {
		sb.WriteString("- 扩展: " + strings.Join(d.Extensions, ", ") + "\n")
	}
	if d.HighAvailability {
		sb.WriteString("- 高可用: 是 (主备同步, schema 变更同步备库)\n")
	}
	if d.HasPartitionedTab {
		sb.WriteString("- 已存在分区表: 是\n")
	}
	if len(d.Parameters) > 0 {
		sb.WriteString("- 关键参数:\n")
		// Stable iteration order for prompt reproducibility.
		keys := make([]string, 0, len(d.Parameters))
		for k := range d.Parameters {
			keys = append(keys, k)
		}
		// Simple insertion sort (small list, avoid sort import).
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		for _, k := range keys {
			sb.WriteString("    " + k + " = " + d.Parameters[k] + "\n")
		}
	}
	return sb.String()
}

// assembleUserMessage renders the collected Phase A context as the user
// message for Round 1. Designed to fit ~4-8K tokens for typical queries;
// token compression is M7.x territory if needed.
func assembleUserMessage(cc *CollectedContext) string {
	var sb strings.Builder
	sb.WriteString("以下是收集到的全部上下文，请基于此给出 5 维度优化方案。\n\n")

	sb.WriteString("# 原 SQL\n\n```sql\n")
	sb.WriteString(cc.OrigSQL)
	sb.WriteString("\n```\n\n")

	if cc.Plan != nil {
		sb.WriteString("# 执行计划\n\n")
		sb.WriteString(fmt.Sprintf("- 估算总成本: %.2f\n", cc.Plan.TotalCost))
		if cc.Plan.HasAnalyze {
			sb.WriteString(fmt.Sprintf("- 实际执行: planning=%.2fms execution=%.2fms\n",
				cc.Plan.PlanningTime, cc.Plan.ExecutionTime))
		}
		sb.WriteString("\n```\n")
		sb.WriteString(formatPlanTreeText(cc.Plan.Root, 0))
		sb.WriteString("```\n\n")
	}

	if cc.Trace != nil && cc.Trace.Available && cc.Trace.Body != "" {
		sb.WriteString("# CBO 决策跟踪 (")
		sb.WriteString(cc.Trace.Format)
		sb.WriteString(")\n\n")
		if cc.Trace.Truncated {
			sb.WriteString("> ⚠️ trace 已截断（>1MB 控制 token 预算）\n\n")
		}
		sb.WriteString("```\n")
		sb.WriteString(truncate(cc.Trace.Body, 100*1024)) // 100KB cap into prompt
		sb.WriteString("\n```\n\n")
	} else if cc.Trace != nil && cc.Trace.Notes != "" {
		sb.WriteString("# CBO 决策跟踪\n\n> 不可用: ")
		sb.WriteString(cc.Trace.Notes)
		sb.WriteString("\n\n")
	}

	if cc.Schema != nil {
		sb.WriteString(formatSchemaInfo(cc.Schema))
	}

	if cc.Runtime != nil && len(cc.Runtime.WaitEvents) > 0 {
		sb.WriteString("# 当前会话等待事件\n\n")
		for _, w := range cc.Runtime.WaitEvents {
			sb.WriteString(fmt.Sprintf("- %s / %s: %d\n", w.WaitEventType, w.WaitEvent, w.Count))
		}
		sb.WriteString("\n")
	}

	if len(cc.Notes) > 0 {
		sb.WriteString("# 收集警告\n\n")
		for _, n := range cc.Notes {
			sb.WriteString("- " + n + "\n")
		}
	}
	return sb.String()
}

// formatPlanTreeText recursively renders PlanNode tree as indented text.
func formatPlanTreeText(n *PlanNode, depth int) string {
	if n == nil {
		return ""
	}
	indent := strings.Repeat("  ", depth)
	var sb strings.Builder
	sb.WriteString(indent + "→ " + n.Operator)
	if n.RelationName != "" {
		sb.WriteString(" on " + n.RelationName)
	}
	if n.TotalCost > 0 {
		sb.WriteString(fmt.Sprintf(" (cost=%.2f rows=%d)", n.TotalCost, n.PlanRows))
	}
	if n.ActualRows > 0 {
		sb.WriteString(fmt.Sprintf(" actual=%d", n.ActualRows))
	}
	if n.Filter != "" {
		sb.WriteString(" filter=" + n.Filter)
	}
	if n.IndexCondition != "" {
		sb.WriteString(" idx=" + n.IndexCondition)
	}
	sb.WriteString("\n")
	for _, c := range n.Children {
		sb.WriteString(formatPlanTreeText(c, depth+1))
	}
	return sb.String()
}

// formatSchemaInfo renders tables + indexes + key column stats compactly.
func formatSchemaInfo(s *SchemaInfo) string {
	var sb strings.Builder
	if len(s.Tables) > 0 {
		sb.WriteString("# 表元数据\n\n")
		for _, ti := range s.Tables {
			sb.WriteString(fmt.Sprintf("- %s.%s: rows=%d pages=%d size=%.2f MB\n",
				ti.Schema, ti.Name, ti.Tuples, ti.Pages, ti.SizeMB))
		}
		sb.WriteString("\n")
	}
	if len(s.Indexes) > 0 {
		sb.WriteString("# 索引\n\n")
		for table, idxs := range s.Indexes {
			sb.WriteString("- " + table + ":\n")
			for _, ix := range idxs {
				marker := ""
				if ix.Primary {
					marker = " PK"
				} else if ix.Unique {
					marker = " UNIQUE"
				}
				sb.WriteString(fmt.Sprintf("  - %s%s on (%s)\n",
					ix.Name, marker, strings.Join(ix.Columns, ", ")))
			}
		}
		sb.WriteString("\n")
	}
	if len(s.Stats) > 0 {
		sb.WriteString("# 列统计 (n_distinct / null_frac / correlation)\n\n")
		for table, cols := range s.Stats {
			sb.WriteString("- " + table + ":\n")
			limit := 8
			if len(cols) < limit {
				limit = len(cols)
			}
			for _, c := range cols[:limit] {
				sb.WriteString(fmt.Sprintf("  - %s: ndv=%.0f null_frac=%.3f corr=%.2f\n",
					c.Column, c.NDistinct, c.NullFrac, c.Correlation))
			}
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// ── Markdown render for final report ───────────────────────────────────

// renderFinalReport produces the user-facing markdown after a successful
// Round 1 + verify. Includes Phase A context, candidates with verification
// results, and the LLM's cbo_analysis prose.
func renderFinalReport(cc *CollectedContext, b PromptBuilder, r1 *Round1Output, verifies []VerifyResult, stats *ReportStats) string {
	var sb strings.Builder
	sb.WriteString("# /sqltune 分析报告\n\n")
	sb.WriteString(fmt.Sprintf("> 方言: %s | 总耗时: %s | 候选: %d (已验证 %d, 最优 %.1f×)\n\n",
		b.RoleTag(),
		stats.TotalDuration.Round(time.Millisecond),
		stats.CandidateCount, stats.VerifiedCount, stats.BestSpeedup))

	if cc.Dialect != nil && cc.Dialect.Version != "" {
		sb.WriteString("## 实例环境\n\n- 版本: " + cc.Dialect.Version + "\n\n")
	}

	sb.WriteString("## SQL\n\n```sql\n" + cc.OrigSQL + "\n```\n\n")

	if r1.CBOAnalysis != "" {
		sb.WriteString("## CBO 决策分析 (LLM 综合)\n\n")
		sb.WriteString(r1.CBOAnalysis)
		sb.WriteString("\n\n")
	}

	if cc.Plan != nil {
		sb.WriteString("## 执行计划\n\n```\n")
		sb.WriteString(formatPlanTreeText(cc.Plan.Root, 0))
		sb.WriteString("```\n\n")
	}

	if len(r1.Candidates) > 0 {
		sb.WriteString("## 优化候选方案 (")
		sb.WriteString(fmt.Sprintf("%d 个)\n\n", len(r1.Candidates)))
		// Index verifies by candidate id for quick lookup.
		verifyByID := map[int]VerifyResult{}
		for _, v := range verifies {
			verifyByID[v.CandID] = v
		}
		for _, c := range r1.Candidates {
			renderCandidate(&sb, c, verifyByID[c.ID])
		}
	}

	if cc.Trace != nil && cc.Trace.Available {
		sb.WriteString("\n## CBO 跟踪原始数据\n\n")
		sb.WriteString(fmt.Sprintf("- 格式: %s, 大小: %d bytes", cc.Trace.Format, cc.Trace.Bytes))
		if cc.Trace.Truncated {
			sb.WriteString(" (已截断)")
		}
		sb.WriteString("\n\n<details>\n<summary>展开 trace</summary>\n\n```\n")
		sb.WriteString(cc.Trace.Body)
		sb.WriteString("\n```\n\n</details>\n\n")
	}

	if len(cc.Notes) > 0 {
		sb.WriteString("## 收集警告\n\n")
		for _, n := range cc.Notes {
			sb.WriteString("- " + n + "\n")
		}
	}
	return sb.String()
}

func renderCandidate(sb *strings.Builder, c Candidate, v VerifyResult) {
	sb.WriteString(fmt.Sprintf("### #%d [%s] (risk: %s, gain: %s)\n\n",
		c.ID, c.Type, c.RiskLevel, c.ExpectedGain))
	sb.WriteString("**Rationale:** " + c.Rationale + "\n\n")
	sb.WriteString("```sql\n" + c.SQL + "\n```\n\n")
	if v.Verifiable {
		sb.WriteString(fmt.Sprintf("**Verify:** cost %.2f → %.2f", v.OldCost, v.NewCost))
		if v.Speedup > 0 {
			sb.WriteString(fmt.Sprintf(" (%.1f×)", v.Speedup))
		}
		if v.EquivOK != nil {
			if *v.EquivOK {
				sb.WriteString(" ✓ 等价性已验证")
			} else {
				sb.WriteString(" ⚠️ 等价性验证失败 — 不要直接 apply")
			}
		}
		if v.Error != "" {
			sb.WriteString(" (error: " + v.Error + ")")
		}
		sb.WriteString("\n\n")
	} else if v.Note != "" {
		sb.WriteString("**Verify:** " + v.Note + "\n\n")
	}
	if len(c.AppliesTo) > 0 {
		sb.WriteString("**Applies to:** " + strings.Join(c.AppliesTo, ", ") + "\n\n")
	}
}

// renderRawPhaseA renders Phase A only (no Round 1 LLM). Used when
// LLM is unavailable or Round 1 fails — still gives DBA the raw data.
func renderRawPhaseA(cc *CollectedContext, b PromptBuilder, stats *ReportStats, banner string) string {
	var sb strings.Builder
	sb.WriteString("# /sqltune 报告 (raw Phase A only)\n\n")
	if banner != "" {
		sb.WriteString("> " + banner + "\n\n")
	}
	sb.WriteString(fmt.Sprintf("> 方言: %s | 总耗时: %s\n\n", b.RoleTag(), stats.TotalDuration.Round(time.Millisecond)))

	sb.WriteString("## SQL\n\n```sql\n" + cc.OrigSQL + "\n```\n\n")

	if cc.Plan != nil {
		sb.WriteString("## 执行计划\n\n```\n")
		sb.WriteString(formatPlanTreeText(cc.Plan.Root, 0))
		sb.WriteString("```\n\n")
	}

	if cc.Schema != nil {
		sb.WriteString(formatSchemaInfo(cc.Schema))
	}

	if cc.Trace != nil {
		sb.WriteString("## CBO 决策跟踪 (")
		sb.WriteString(cc.Trace.Format)
		sb.WriteString(")\n\n")
		if cc.Trace.Available {
			if cc.Trace.Truncated {
				sb.WriteString("> ⚠️ 已截断 (>1MB)\n\n")
			}
			sb.WriteString("```\n" + cc.Trace.Body + "\n```\n\n")
		} else {
			sb.WriteString("> 不可用: " + cc.Trace.Notes + "\n\n")
		}
	}

	if len(cc.Notes) > 0 {
		sb.WriteString("## 收集警告\n\n")
		for _, n := range cc.Notes {
			sb.WriteString("- " + n + "\n")
		}
	}
	return sb.String()
}
