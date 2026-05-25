/*-------------------------------------------------------------------------
 *
 * synthesizer.go
 *	  M4: LLM synthesis layer. Takes structured WDR data + fallback
 *	  findings + Top SQL tune results, produces prose analysis: risk
 *	  overview (replacing the dropped M2 rule engine), cross-metric
 *	  causal chains, configuration tuning recommendations, and an
 *	  executive summary with a 1-2 week action plan.
 *
 *	  Failure mode: if LLM unavailable / times out / returns empty,
 *	  Synthesize returns "" with the original error. Renderer treats
 *	  empty synthesis as "skip the LLM section, just render structured
 *	  findings + sqltune output". The fallback rules ensure the report
 *	  is still useful without LLM.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/synthesizer.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/llm"
)

// Synthesize calls the LLM with WDR + fallback + sqltune as context and
// returns the prose risk analysis + config tuning advice + summary. Empty
// string on failure (with non-nil error).
//
// Token budget: WDR can be large. We don't pass the full Raw text; instead
// we pass structured summaries (Time Model, Top 10 Waits, Top 10 SQLs,
// Memory, Settings) which compresses to ~3K tokens regardless of WDR size.
// Plus the fallback findings and sqltune results.
func Synthesize(
	ctx context.Context,
	provider llm.Provider,
	report *WDRReport,
	fallback []Finding,
	sqlTunes []SQLTuneResult,
) (string, error) {
	if provider == nil {
		return "", fmt.Errorf("no LLM provider configured")
	}

	systemPrompt := buildWDRSystemPrompt()
	userMsg := buildWDRUserMessage(report, fallback, sqlTunes)

	cctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	req := llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userMsg},
		},
		// v1.1.51: 3-layer output with Layer 2 deep-dive per risk module
		// + Layer 3 optimization table needs more headroom than the old
		// "risk overview + summary" format (which usually fit in 2-3K).
		MaxTokens: 8000,
	}

	resp, err := provider.Chat(cctx, req)
	if err != nil {
		return "", fmt.Errorf("LLM chat: %w", err)
	}
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("LLM returned empty content")
	}
	return GateWDRSynthesis(resp.Content), nil
}

// buildWDRSystemPrompt encodes the v1.1.51 three-layer output contract:
//
//	Layer 1: 总览评估表 (基于 scorecard, 不重新打分)
//	Layer 2: 风险详解 (仅 🔴/🟡 项, 模板化)
//	Layer 3: 优化方案 (反向引用 R# 编号)
//
// Hard constraints prevent: hallucinating data, inventing new severity
// levels (deterministic scorecard is authoritative), and giving generic
// advice not tied to a specific risk.
func buildWDRSystemPrompt() string {
	return `你是 openGauss / GaussDB 性能诊断专家。基于已结构化的 WDR 数据 +
确定性规则引擎给出的 scorecard, 按三层结构输出诊断报告. 评级 (🔴/🟡/✅) 已经
由规则引擎给定, **你不要重新评级**, 你的任务是基于 scorecard 和 原始数据节
深入解读.

【输出格式·强制 markdown】

## Layer 1: 总览评估
逐字复制传入的 scorecard 表格 (5 行, 每行: 模块 / 评级 / 关键指标 / 风险提要).
模块顺序保持: Database Stat → Load Profile → Instance Efficiency → IO Profile → TopSQL.

(不要在 Layer 1 加任何分析/解读, 它只是数据汇总. 所有解读放 Layer 2)

## Layer 2: 风险详解
仅对 🔴 / 🟡 的模块详细分析 (✅ 跳过). 每个风险按以下模板, 编号 R1/R2/R3...

**核心原则: 先列数据 (markdown 表格), 后给分析**. 不要"现象"段堆 bullet,
要让读者先看到具体指标的表格, 再读你的解读.

### R<N>: <一句标题> — <模块名> <评级 icon>

**关键指标**

| 指标 | 实测值 | 阈值/基线 | 偏离倍数 |
|---|---|---|---|
| <指标1> | <数值带单位> | <阈值或正常区间> | <X 倍 / 超 N%> |
| <指标2> | <数值带单位> | <阈值或正常区间> | <X 倍 / 超 N%> |
| <可选 3-5 行> | ... | ... | ... |

**根因**
<1-2 段说明上表数值为什么有问题, 引用 og 行为或经验阈值>

**业务影响**
<1 段说明对业务/性能的影响>

**关联模块**
- ↔ <如果与其他 R# 关联, 写"R<X>: 简述关联点">; 没关联可写"独立风险"

(指标表至少 2 行, 必须能从 scorecard.KeyMetrics 或 RawSection 原文直接
查到; 阈值/基线列写规则引擎触发的阈值或行业经验值, 偏离倍数让读者一眼
看出严重程度)

## Layer 3: 优化方案
按 P0 → P1 → P2 排序输出一个表格, 列: 优先级 / 优化项 / 关联风险 / 操作 / 预期效果.

**关联风险** 列**必须**填具体 R# 编号 (如 "R1, R3"), 不允许填 "通用建议" 或为空.
**操作** 列给出可执行 SQL 或 GUC 配置 (含具体值), 不要泛泛"调大 work_mem".
**预期效果** 列说明哪个 scorecard 指标会改善 (如 "Temp Bytes → 0", "Soft Parse % → 95+").

输出后用一段 80-150 字 **综合评估** 收尾: 当前状态判断 + 优先做哪 1-2 件事.

【硬约束】
1. Layer 1 评级**严格**遵循传入的 scorecard, 你不能改 ✅ → 🟡 或反之.
2. 所有数值**必须**来自传入数据 (scorecard.KeyMetrics 或 RawSection 原文),
   禁止编造或外推. 如果原始数据没有, 不要展开该风险.
3. fallback findings (如果有) 必须并入 Layer 2 对应模块的风险中.
4. Layer 3 每条优化必须反向引用 R# 编号, 没有对应风险的优化不允许出现.
5. 不要重复 Layer 1 的内容到 Layer 2 (Layer 2 是展开不是复述).
6. 全文不超过 1500 字, 不要加 "## 工作负载特征" / "## Top SQL 列表" 这种由
   renderer 自己处理的章节.
7. 只有 🟡 warning 的风险不能写 P0；P0 只允许用于 🔴 或明确在线故障证据。
8. 不要写未验证的 PostgreSQL 参数/对象作为直接执行项；pg_stat_statements、pgBouncer、statement_cache_size、enable_prepared_statement 等必须标“需确认 GaussDB/openGauss 兼容”。
9. 不要用 <br>、HTML 标签或不受证据支撑的精确收益数字（例如“CPU 降低 15%”）。
`
}

// buildWDRUserMessage compresses the WDR + fallback + sqltune into a
// digestible context for the LLM. v1.1.51: also ships the deterministic
// scorecard from EvaluateSections so the LLM doesn't re-score, and the
// raw section text blocks so it has the underlying data to deep-dive.
func buildWDRUserMessage(report *WDRReport, fallback []Finding, sqlTunes []SQLTuneResult) string {
	var b strings.Builder

	b.WriteString("# WDR 数据\n\n")

	// Header
	b.WriteString("## 报告窗口\n")
	b.WriteString(fmt.Sprintf("- 时段: %s ~ %s (%s)\n",
		formatTime(report.Header.WindowStart),
		formatTime(report.Header.WindowEnd),
		formatDuration(report.Header.WindowDuration())))
	b.WriteString(fmt.Sprintf("- 实例: %s · %s\n",
		report.Header.InstanceHost, report.Header.DBVersion))
	b.WriteString("\n")

	b.WriteString("## 结构化证据输出（必须作为主证据，不要只总结表面现象）\n\n")
	b.WriteString(EvidencePromptBlock(report, fallback, sqlTunes))
	b.WriteString("\n")

	// v1.1.51: deterministic scorecard (Layer 1 input). The LLM is asked
	// to literally reproduce this as Layer 1 — don't reformat, don't re-rate.
	if len(report.SectionScores) > 0 {
		b.WriteString("## Scorecard (Layer 1, 你必须照原样输出, 不重新评级)\n\n")
		b.WriteString("| 模块 | 评级 | 关键指标 | 风险提要 |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, s := range report.SectionScores {
			metrics := formatKeyMetrics(s.KeyMetrics)
			summary := s.Summary
			if summary == "" {
				summary = "—"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				s.Name, s.Level.Icon(), metrics, summary))
		}
		b.WriteString("\n")

		// Detailed rule triggers for Layer 2 use
		b.WriteString("### 触发的规则明细 (Layer 2 引用)\n\n")
		any := false
		for _, s := range report.SectionScores {
			if len(s.Rules) == 0 {
				continue
			}
			any = true
			b.WriteString(fmt.Sprintf("**%s %s**:\n", s.Level.Icon(), s.Name))
			for _, rule := range s.Rules {
				b.WriteString(fmt.Sprintf("- [%s] `%s` 实测 %s (阈值 %s): %s\n",
					rule.Level, rule.Metric, rule.Observed, rule.Threshold, rule.Reason))
			}
			b.WriteString("\n")
		}
		if !any {
			b.WriteString("(全部 ✅, 无触发规则)\n\n")
		}
	}

	// v1.1.51: raw section text blocks (Layer 2 evidence source). Capped at
	// 8KB/section by extractor.
	if len(report.RawSections) > 0 {
		b.WriteString("## 原始数据节 (Layer 2 引用数据来源)\n\n")
		// Deterministic order for cache-friendly prompts
		order := []struct{ key, label string }{
			{SectionDatabaseStat, "Database Stat"},
			{SectionLoadProfile, "Load Profile"},
			{SectionInstanceEfficiency, "Instance Efficiency"},
			{SectionIOProfile, "IO Profile"},
			{SectionUserTables, "User Tables Stats"},
			{SectionUserIndexes, "User Index Stats"},
		}
		for _, item := range order {
			if v, ok := report.RawSections[item.key]; ok && v != "" {
				b.WriteString(fmt.Sprintf("### %s\n```\n%s\n```\n\n", item.label, v))
			}
		}
	}

	// Time Model
	tm := report.TimeModel
	if tm.DBTimeSec > 0 {
		b.WriteString("## Time Model\n")
		b.WriteString(fmt.Sprintf("- DB Time: %.0fs\n", tm.DBTimeSec))
		b.WriteString(fmt.Sprintf("- CPU Time: %.0fs (%.1f%% of DB Time)\n",
			tm.CPUTimeSec, safePct(tm.CPUTimeSec, tm.DBTimeSec)))
		b.WriteString(fmt.Sprintf("- Wait Time: %.0fs (%.1f%% of DB Time)\n",
			tm.WaitTimeSec, safePct(tm.WaitTimeSec, tm.DBTimeSec)))
		if tm.HardParseCount > 0 {
			b.WriteString(fmt.Sprintf("- Hard Parse: %d (%.1f%% of total parses)\n",
				tm.HardParseCount, tm.HardParseRatio()*100))
		}
		b.WriteString("\n")
	}

	// Top 10 wait events
	if len(report.Waits) > 0 {
		b.WriteString("## Top Wait Events\n")
		n := 10
		if len(report.Waits) < n {
			n = len(report.Waits)
		}
		for i := 0; i < n; i++ {
			w := report.Waits[i]
			b.WriteString(fmt.Sprintf("- %s (%s): %.0fms (%.1f%% of DB Time)\n",
				w.Name, w.Category, w.WaitTimeMS, w.PctOfDBTime))
		}
		b.WriteString("\n")
	}

	// Top 30 SQLs (only headers — full SQL handled by Phase 4).
	// v1.1.50: bumped 10 → 30. og short windows can have 50+ SQLs each
	// well above the floor; truncating at 10 hid most of the load.
	if len(report.TopSQLs) > 0 {
		b.WriteString("## Top SQL (按总耗时)\n")
		n := 30
		if len(report.TopSQLs) < n {
			n = len(report.TopSQLs)
		}
		for i := 0; i < n; i++ {
			s := report.TopSQLs[i]
			b.WriteString(fmt.Sprintf("- SQL_ID %s · %d calls · avg %.1fms · total %.0fms (%.1f%% of DB Time)\n",
				s.SQLID, s.Calls, s.AvgTimeMS, s.TotalTimeMS, s.PctOfDBTime(tm.DBTimeSec)))
		}
		b.WriteString("\n")
	}

	// IO
	if report.IO.BlocksHit > 0 || report.IO.BlocksRead > 0 {
		b.WriteString("## IO / Buffer\n")
		b.WriteString(fmt.Sprintf("- blocks_hit: %d, blocks_read: %d, hit ratio: %.2f%%\n",
			report.IO.BlocksHit, report.IO.BlocksRead, report.IO.BufferHitRatio()*100))
		if report.IO.WALWritesMB > 0 {
			b.WriteString(fmt.Sprintf("- WAL Written: %.1f MB\n", report.IO.WALWritesMB))
		}
		b.WriteString("\n")
	}

	// Memory
	if report.Memory.TotalMemoryMB > 0 {
		b.WriteString("## Memory\n")
		b.WriteString(fmt.Sprintf("- max_process_memory: %d MB\n", report.Memory.TotalMemoryMB))
		b.WriteString(fmt.Sprintf("- used_memory: %d MB (%.1f%%)\n",
			report.Memory.UsedMemoryMB, report.Memory.UsageRatio()*100))
		if report.Memory.SharedBuffersMB > 0 {
			b.WriteString(fmt.Sprintf("- shared_buffers: %d MB\n", report.Memory.SharedBuffersMB))
		}
		if report.Memory.WorkMemMB > 0 {
			b.WriteString(fmt.Sprintf("- work_mem: %d MB\n", report.Memory.WorkMemMB))
		}
		b.WriteString("\n")
	}

	// Locks
	if report.Locks.LockWaitCount > 0 || report.Locks.DeadlockCount > 0 {
		b.WriteString("## Locks\n")
		b.WriteString(fmt.Sprintf("- lock_wait_count: %d, lock_wait_time: %.0fms\n",
			report.Locks.LockWaitCount, report.Locks.LockWaitTimeMS))
		b.WriteString(fmt.Sprintf("- deadlock_count: %d\n", report.Locks.DeadlockCount))
		b.WriteString("\n")
	}

	// Replication
	if report.Replication.StandbyCount > 0 {
		b.WriteString("## Replication\n")
		b.WriteString(fmt.Sprintf("- standby_count: %d, max_lag: %.1fs, sync_mode: %s\n",
			report.Replication.StandbyCount, report.Replication.MaxLagSeconds, report.Replication.SyncMode))
		b.WriteString("\n")
	}

	// Settings (key GUCs)
	if len(report.Settings) > 0 {
		b.WriteString("## Key Settings\n")
		for k, v := range report.Settings {
			b.WriteString(fmt.Sprintf("- %s = %s\n", k, v))
		}
		b.WriteString("\n")
	}

	// Fallback findings (mandatory inclusion in LLM output)
	if len(fallback) > 0 {
		b.WriteString("# 已触发的兜底 findings (你的风险全景必须包含这些)\n\n")
		for _, f := range fallback {
			b.WriteString(fmt.Sprintf("- **[%s · %s] %s**\n", f.ID, f.Severity, f.Title))
			for _, e := range f.Evidence {
				b.WriteString(fmt.Sprintf("  - %s\n", e))
			}
			if f.Suggestion != "" {
				b.WriteString(fmt.Sprintf("  - 建议: %s\n", f.Suggestion))
			}
		}
		b.WriteString("\n")
	}

	// SQL tune results (summary only — full details rendered separately)
	if len(sqlTunes) > 0 {
		b.WriteString("# Top SQL sqltune 结果摘要 (你的因果链可引用)\n\n")
		for i, st := range sqlTunes {
			if st.Error != "" {
				b.WriteString(fmt.Sprintf("- #%d SQL_ID %s: sqltune 失败 (%s)\n", i+1, st.SQLID, st.Error))
				continue
			}
			label := ""
			if st.FromMemory {
				label = " (memory 命中)"
			}
			b.WriteString(fmt.Sprintf("- #%d SQL_ID %s%s: %.1f× 提升, schema=%s\n",
				i+1, st.SQLID, label, st.BestSpeedup, st.Schema))
		}
		b.WriteString("\n")
	}

	return b.String()
}

func safePct(num, denom float64) float64 {
	if denom == 0 {
		return 0
	}
	return num / denom * 100
}

// formatKeyMetrics renders a SectionScore.KeyMetrics map into one line
// suitable for the Layer-1 table cell. Sorted alphabetically for stable
// output (so re-runs produce identical prompts → cache-friendly).
func formatKeyMetrics(m map[string]string) string {
	if len(m) == 0 {
		return "—"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, " · ")
}
