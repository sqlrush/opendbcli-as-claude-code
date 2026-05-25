/*-------------------------------------------------------------------------
 *
 * renderer.go
 *	  Markdown renderer for Analysis. M1 produces the basic skeleton
 *	  (header / findings / Top SQL list). Phase 4 (sqltune integration)
 *	  and Phase 5 (LLM synthesis) outputs are appended by later milestones.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/renderer.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Render produces the final markdown report from an Analysis.
//
// Layout order (M3/M4 complete):
//  1. Header (window / instance / DB Time)
//  2. Workload Summary (CPU / Wait %, hard parse)
//  3. LLM Synthesis (M4 — risk overview + bottlenecks + config + summary)
//     OR Fallback Findings section (when LLM is unavailable)
//  4. Top SQL List + Top SQL Tunes (M3 — sqltune optimizations)
//  5. Footer
//
// LLM synthesis (when present) replaces the standalone "Findings" section
// because the LLM is instructed to include all fallback findings in its
// own "风险全景" output. When LLM is unavailable, fall back to plain
// findings rendering so the report is still useful.
func Render(a *Analysis) string {
	var b strings.Builder
	renderHeader(&b, a)
	renderDiagnosticBoundary(&b, a)
	renderWorkloadSummary(&b, a)
	renderEvidenceSection(&b, a)
	if a.LLMSynthesis != "" {
		renderLLMSection(&b, a)
	} else {
		// LLM unavailable — render fallback findings directly as a section
		renderRiskOverview(&b, a)
		renderFindings(&b, a)
	}
	renderTopSQLList(&b, a)
	if len(a.SQLTunes) > 0 {
		renderSQLTunes(&b, a)
	}
	renderFooter(&b, a)
	return b.String()
}

func renderEvidenceSection(b *strings.Builder, a *Analysis) {
	if a == nil || a.Report == nil {
		return
	}
	evidence := BuildEvidenceBundle(a.Report, a.Findings, a.SQLTunes)
	text := RenderEvidenceMarkdown(evidence)
	if strings.TrimSpace(text) == "## 结构化证据" {
		return
	}
	b.WriteString(text)
}

func renderHeader(b *strings.Builder, a *Analysis) {
	h := a.Report.Header
	b.WriteString("# WDR 分析报告\n\n")
	b.WriteString(fmt.Sprintf("> **报告窗口**: %s ~ %s",
		formatTime(h.WindowStart), formatTime(h.WindowEnd)))
	if d := h.WindowDuration(); d > 0 {
		b.WriteString(fmt.Sprintf("  (%s)", formatDuration(d)))
	}
	b.WriteString("\n")
	if h.InstanceHost != "" {
		b.WriteString(fmt.Sprintf("> **实例**: %s", h.InstanceHost))
		if h.InstanceID != "" {
			b.WriteString(" · " + h.InstanceID)
		}
		b.WriteString("\n")
	}
	if h.DBVersion != "" {
		b.WriteString("> **版本**: " + h.DBVersion + "\n")
	}
	if h.SnapshotIDStart > 0 || h.SnapshotIDEnd > 0 {
		b.WriteString(fmt.Sprintf("> **Snapshot**: %d → %d\n", h.SnapshotIDStart, h.SnapshotIDEnd))
	}
	b.WriteString(fmt.Sprintf("> **DB Time**: %s\n", formatSeconds(a.Report.TimeModel.DBTimeSec)))
	b.WriteString(fmt.Sprintf("> **生成耗时**: %s\n\n", formatDuration(a.Duration)))
}

func renderWorkloadSummary(b *strings.Builder, a *Analysis) {
	tm := a.Report.TimeModel
	if tm.DBTimeSec == 0 {
		return
	}
	cpuRatio := 0.0
	waitRatio := 0.0
	if tm.DBTimeSec > 0 {
		cpuRatio = tm.CPUTimeSec / tm.DBTimeSec * 100
		waitRatio = tm.WaitTimeSec / tm.DBTimeSec * 100
	}
	b.WriteString("## 工作负载特征\n\n")
	b.WriteString(fmt.Sprintf("- DB Time: %s\n", formatSeconds(tm.DBTimeSec)))
	if cpuRatio > 0 {
		b.WriteString(fmt.Sprintf("- CPU on DB: %s (%.1f%% of DB Time)\n", formatSeconds(tm.CPUTimeSec), cpuRatio))
	}
	if waitRatio > 0 {
		b.WriteString(fmt.Sprintf("- Wait Time: %s (%.1f%% of DB Time)\n", formatSeconds(tm.WaitTimeSec), waitRatio))
	}
	if tm.HardParseCount > 0 {
		ratio := tm.HardParseRatio() * 100
		b.WriteString(fmt.Sprintf("- Hard Parse: %d (%.1f%% of total parses)\n", tm.HardParseCount, ratio))
	}
	b.WriteString("\n")
}

func renderRiskOverview(b *strings.Builder, a *Analysis) {
	if len(a.Findings) == 0 {
		return
	}
	counts := CountBySeverity(a.Findings)
	b.WriteString("## 风险全景\n\n")
	b.WriteString("| 严重度 | 数量 |\n|---|---|\n")
	b.WriteString(fmt.Sprintf("| 🔴 严重 | %d |\n", counts[SeverityCritical]))
	b.WriteString(fmt.Sprintf("| 🟡 警告 | %d |\n", counts[SeverityWarning]))
	b.WriteString(fmt.Sprintf("| 🟢 提示 | %d |\n\n", counts[SeverityInfo]))
}

func renderFindings(b *strings.Builder, a *Analysis) {
	if len(a.Findings) == 0 {
		return
	}
	// Sort by severity desc
	sorted := make([]Finding, len(a.Findings))
	copy(sorted, a.Findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Severity > sorted[j].Severity
	})

	currentSev := Severity(-1)
	for _, f := range sorted {
		if f.Severity != currentSev {
			currentSev = f.Severity
			b.WriteString(fmt.Sprintf("\n## %s\n\n", f.Severity.String()))
		}
		b.WriteString(fmt.Sprintf("### %s\n", f.Title))
		if len(f.Evidence) > 0 {
			b.WriteString("\n**证据**:\n")
			for _, e := range f.Evidence {
				b.WriteString("- " + e + "\n")
			}
		}
		if f.Suggestion != "" {
			b.WriteString("\n**建议**: " + f.Suggestion + "\n")
		}
		b.WriteString("\n")
	}
}

func renderTopSQLList(b *strings.Builder, a *Analysis) {
	if len(a.Report.TopSQLs) == 0 {
		return
	}
	b.WriteString("## Top SQL (按总耗时)\n\n")
	b.WriteString("| # | SQL_ID | 调用 | 平均 | 总耗时 | 占 DB Time | 来源 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	maxList := 10
	if len(a.Report.TopSQLs) < maxList {
		maxList = len(a.Report.TopSQLs)
	}
	for i := 0; i < maxList; i++ {
		s := a.Report.TopSQLs[i]
		b.WriteString(fmt.Sprintf("| %d | `%s` | %d | %s | %s | %.1f%% | %s |\n",
			i+1, s.SQLID, s.Calls,
			formatMS(s.AvgTimeMS),
			formatMS(s.TotalTimeMS),
			s.PctOfDBTime(a.Report.TimeModel.DBTimeSec),
			strings.Join(s.Sources, ", "),
		))
	}
	b.WriteString("\n")
}

func renderSQLTunes(b *strings.Builder, a *Analysis) {
	b.WriteString("## Top SQL 优化建议 (sqltune 深度分析)\n\n")

	// v1.1.52: tally skipped maintenance entries up front so a window
	// dominated by DDL/ANALYZE doesn't render 5 identical "failure" blocks.
	skipped := make([]SQLTuneResult, 0)
	tunable := make([]SQLTuneResult, 0)
	for _, st := range a.SQLTunes {
		if st.Skipped {
			skipped = append(skipped, st)
		} else {
			tunable = append(tunable, st)
		}
	}
	if len(skipped) > 0 && len(tunable) == 0 {
		b.WriteString(fmt.Sprintf("> ⓘ Top %d SQL **全部为维护类语句** (DDL / ANALYZE / SET / SHOW 等), 无 SQL 调优空间. 业务负载分析请关注 Layer 2 风险详解.\n\n",
			len(skipped)))
	} else if len(skipped) > 0 {
		b.WriteString(fmt.Sprintf("> ⓘ Top %d 中含 %d 条维护类语句, 已跳过 (CREATE/ANALYZE/SET 等不适用 SQL 调优).\n\n",
			len(a.SQLTunes), len(skipped)))
	}

	for i, st := range a.SQLTunes {
		b.WriteString(fmt.Sprintf("### #%d  SQL_ID `%s`", i+1, st.SQLID))
		if st.FromMemory {
			b.WriteString("  ⭐ memory 命中")
		}
		b.WriteString("\n\n")
		if st.Skipped {
			b.WriteString(fmt.Sprintf("ⓘ **跳过**: %s — 该类型语句不适用 SQL 调优\n\n", st.SkipReason))
			continue
		}
		if st.Error != "" {
			b.WriteString(fmt.Sprintf("⚠️ sqltune 失败: %s\n\n", st.Error))
			continue
		}
		if st.BestSpeedup > 0 {
			b.WriteString(fmt.Sprintf("**EXPLAIN cost**: %.0f → %.0f (**%.1f×** 提升)\n\n",
				st.OriginalCost, st.BestNewCost, st.BestSpeedup))
		}
		if !st.HasLiterals {
			b.WriteString("> ⚠️ 字面量为 sqlfetch 合成示例值, 实施时需替换为业务真实值\n\n")
		}
		for j, c := range st.Candidates {
			if j >= 3 {
				b.WriteString(fmt.Sprintf("> ... 还有 %d 个候选, 见完整报告文件\n\n", len(st.Candidates)-j))
				break
			}
			b.WriteString(fmt.Sprintf("**方案 %d** (%s): %s\n", j+1, c.Type, c.Rationale))
			if c.SQL != "" {
				b.WriteString("```sql\n" + truncate(c.SQL, 600) + "\n```\n")
			}
			if c.Verifiable {
				b.WriteString(fmt.Sprintf("EXPLAIN: cost %.0f → %.0f (%.1f×)\n",
					c.OldCost, c.NewCost, c.Speedup))
			}
			b.WriteString("\n")
		}
	}
}

func renderLLMSection(b *strings.Builder, a *Analysis) {
	// LLM section produces multiple top-level subsections (风险全景 /
	// 关键瓶颈 / 配置调优 / 综合评估) per the system prompt contract.
	// We emit it verbatim — no wrapping ## heading because the LLM
	// itself opens with "## 风险全景".
	b.WriteString(a.LLMSynthesis)
	b.WriteString("\n\n")
}

func renderDiagnosticBoundary(b *strings.Builder, a *Analysis) {
	if a == nil || a.Report == nil {
		return
	}
	confidence, reason := evidenceConfidence(a)
	b.WriteString("## 诊断边界\n\n")
	b.WriteString("- WDR 是历史窗口报告，用于分析该时间段内的负载与风险；不能单独证明当前在线故障。\n")
	b.WriteString("- 如需确认当前是否仍有故障，应结合 `health`、`waits`、`activesessions`、`blocktree` 的当前快照复核。\n")
	b.WriteString(fmt.Sprintf("- 证据置信度: %s（%s）。\n\n", confidence, reason))
}

func evidenceConfidence(a *Analysis) (string, string) {
	if a == nil || a.Report == nil {
		return "低", "未解析到 WDR 报告"
	}
	r := a.Report
	score := 0
	var parts []string
	if !r.Header.WindowStart.IsZero() && !r.Header.WindowEnd.IsZero() {
		score += 2
		parts = append(parts, "有时间窗口")
	}
	if len(r.SectionScores) > 0 {
		score += 3
		parts = append(parts, fmt.Sprintf("%d 个评分模块", len(r.SectionScores)))
	}
	if len(r.RawSections) > 0 {
		score += 2
		parts = append(parts, fmt.Sprintf("%d 个原始数据节", len(r.RawSections)))
	}
	if len(r.TopSQLs) > 0 {
		score++
		parts = append(parts, fmt.Sprintf("%d 条 Top SQL", len(r.TopSQLs)))
	}
	if len(a.Findings) > 0 {
		score++
		parts = append(parts, fmt.Sprintf("%d 个规则发现", len(a.Findings)))
	}
	if len(parts) == 0 {
		parts = append(parts, "结构化字段不足")
	}
	switch {
	case score >= 7:
		return "高", strings.Join(parts, "、")
	case score >= 4:
		return "中", strings.Join(parts, "、")
	default:
		return "低", strings.Join(parts, "、")
	}
}

func renderFooter(b *strings.Builder, a *Analysis) {
	b.WriteString("---\n\n")
	b.WriteString("## 报告元信息\n\n")
	generatedAt := "未知"
	if a != nil && !a.GeneratedAt.IsZero() {
		generatedAt = a.GeneratedAt.Format("2006-01-02 15:04:05")
	}
	b.WriteString("- 生成时间: " + generatedAt + "\n")
	b.WriteString("- 分析工具: /wdranalyze\n")
	b.WriteString("- 报告格式: wdranalyze-report/v1\n")
	if a != nil && strings.TrimSpace(a.ReportPath) != "" {
		b.WriteString("- 报告文件: `" + a.ReportPath + "`\n")
	}
}

// ── format helpers ──

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "(未知)"
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func formatSeconds(s float64) string {
	if s == 0 {
		return "(未知)"
	}
	return formatDuration(time.Duration(s * float64(time.Second)))
}

func formatMS(ms float64) string {
	if ms < 1000 {
		return fmt.Sprintf("%.1fms", ms)
	}
	return formatDuration(time.Duration(ms * float64(time.Millisecond)))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
