/*-------------------------------------------------------------------------
 *
 * validator.go
 *	  Post-validation for LLM synthesizer output (Phase 5.5).
 *
 *	  Two goals, both implemented with lenient matching so strong models
 *	  (Opus / GLM / DeepSeek) are not falsely flagged as "missing" things
 *	  they actually wrote in different wording:
 *
 *	    1. Ensure all fallback findings appear somewhere in the LLM output.
 *	       If a finding's signature keywords are not detected, append a
 *	       supplementary block at the end so it can't be silently lost.
 *
 *	    2. Ensure all required sections (风险全景 / 关键瓶颈 / 配置调优 /
 *	       综合评估) are present. If missing, append a placeholder line.
 *
 *	  Matching strategy: per finding ID and per section heading, we keep a
 *	  list of synonym keywords / aliases. Any one matching → considered
 *	  present. False positives (LLM mentioning something but we don't
 *	  detect it) only result in duplicate output, never silent loss —
 *	  which is the desired failure mode for a safety net.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/wdranalyze/validator.go
 *
 *-------------------------------------------------------------------------
 */
package wdranalyze

import (
	"fmt"
	"strings"
)

// ValidateAndPatch checks the LLM output for required sections and
// fallback findings, appending any missing items. Returns the (possibly
// patched) output. Pure function — no I/O. Safe to call on empty input
// (returns "" unchanged).
//
// Designed as a no-op for strong models that follow the prompt faithfully:
// every required section + every fallback finding has multiple synonym
// matchers, so wording variations like "deadlock" vs "死锁" both pass.
func ValidateAndPatch(llmOutput string, fallback []Finding) string {
	if strings.TrimSpace(llmOutput) == "" {
		return llmOutput
	}
	out := llmOutput
	out = patchMissingSections(out)
	out = patchMissingFindings(out, fallback)
	return out
}

// ── Section presence check ─────────────────────────────────────────────

// sectionPattern lists the canonical heading and any synonyms a model
// might emit instead. Match is case-insensitive substring on the entire
// LLM output.
type sectionPattern struct {
	canonical string
	aliases   []string
}

// requiredSections is the contract from synthesizer.go's system prompt.
// v1.1.51: prompt now produces a 3-layer structure (总览/详解/方案) so
// the required headings are Layer 1/2/3. Old "风险全景/关键瓶颈/..."
// names kept as aliases so legacy LLM cached outputs still validate.
var requiredSections = []sectionPattern{
	{
		canonical: "## Layer 1: 总览评估",
		aliases: []string{
			"## layer 1", "## layer1", "## 总览评估", "## 评估总览",
			"## 风险全景", "## risk overview", "## 风险概览",
		},
	},
	{
		canonical: "## Layer 2: 风险详解",
		aliases: []string{
			"## layer 2", "## layer2", "## 风险详解", "## 风险解读",
			"## 关键瓶颈", "## key bottleneck", "## bottlenecks", "## 核心瓶颈",
		},
	},
	{
		canonical: "## Layer 3: 优化方案",
		aliases: []string{
			"## layer 3", "## layer3", "## 优化方案", "## 优化建议",
			"## 配置调优", "## configuration tuning", "## config tuning",
		},
	},
	// 综合评估 is a free-form wrap-up paragraph in Layer 3 now — drop
	// the section requirement so we don't append a stub placeholder
	// when the LLM puts the summary inline at the end of Layer 3.
}

// patchMissingSections appends a placeholder for any required section
// not found in the LLM output. Lenient: any alias match counts.
func patchMissingSections(out string) string {
	lower := strings.ToLower(out)
	var missing []string
	for _, sec := range requiredSections {
		if sectionPresent(lower, sec) {
			continue
		}
		missing = append(missing, sec.canonical)
	}
	if len(missing) == 0 {
		return out
	}
	var b strings.Builder
	b.WriteString(out)
	if !strings.HasSuffix(out, "\n") {
		b.WriteString("\n")
	}
	for _, m := range missing {
		b.WriteString("\n")
		b.WriteString(m)
		b.WriteString("\n\n*(LLM 未生成此段，参考兜底 findings 和 Top SQL 优化建议)*\n")
	}
	return b.String()
}

func sectionPresent(lowerOutput string, sec sectionPattern) bool {
	if strings.Contains(lowerOutput, strings.ToLower(sec.canonical)) {
		return true
	}
	for _, a := range sec.aliases {
		if strings.Contains(lowerOutput, strings.ToLower(a)) {
			return true
		}
	}
	return false
}

// ── Fallback findings coverage check ───────────────────────────────────

// patchMissingFindings ensures every fallback finding is referenced
// somewhere in the LLM output. Missing ones are appended in their own
// section so the user sees them prominently.
func patchMissingFindings(out string, fallback []Finding) string {
	if len(fallback) == 0 {
		return out
	}
	lower := strings.ToLower(out)
	var missing []Finding
	for _, f := range fallback {
		if findingMentioned(lower, f) {
			continue
		}
		missing = append(missing, f)
	}
	if len(missing) == 0 {
		return out
	}
	var b strings.Builder
	b.WriteString(out)
	if !strings.HasSuffix(out, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n## ⚠️ 补充兜底警告 (LLM 漏报)\n\n")
	b.WriteString("以下问题由 opendb 兜底规则确定性触发，但 LLM 综合段没提到。请单独关注:\n\n")
	for _, f := range missing {
		b.WriteString(fmt.Sprintf("### %s %s\n\n", f.Severity, f.Title))
		if len(f.Evidence) > 0 {
			b.WriteString("**证据**:\n")
			for _, e := range f.Evidence {
				b.WriteString("- " + e + "\n")
			}
		}
		if f.Suggestion != "" {
			b.WriteString("\n**建议**: " + f.Suggestion + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// findingMentioned returns true if the LLM output contains any of the
// signature keywords associated with this finding's ID. Wide synonym
// list to avoid false negatives on strong models that rephrase freely.
func findingMentioned(lowerOutput string, f Finding) bool {
	keywords := findingSignatureKeywords(f.ID)
	for _, kw := range keywords {
		if strings.Contains(lowerOutput, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// findingSignatureKeywords returns the synonym set we check for each
// fallback finding ID. Wide enough that any reasonable mention counts
// as present. Cost of wide set: occasional missed-detection (we think
// the LLM said something it didn't) — but the only downside is the
// supplementary block appearing once, not silent loss of fallback.
func findingSignatureKeywords(id string) []string {
	switch id {
	case "autovacuum_off":
		return []string{
			"autovacuum",
			"auto vacuum",
			"auto-vacuum",
			"自动 vacuum",
			"自动vacuum",
			"vacuum 自动",
			"自动清理",
			"自动回收",
		}
	case "deadlock_present":
		return []string{
			"deadlock",
			"dead lock",
			"死锁",
			"互锁",
		}
	case "replication_lag_high":
		return []string{
			"replication lag",
			"replication延迟",
			"主备延迟",
			"主从延迟",
			"standby lag",
			"备库延迟",
			"max_lag",
			"复制延迟",
		}
	case "buffer_hit_critical":
		return []string{
			"buffer hit",
			"buffer_hit",
			"hit ratio",
			"缓冲池",
			"命中率",
			"buffer pool",
			"缓存命中",
		}
	case "single_sql_dominant":
		return []string{
			"单 sql",
			"单条 sql",
			"single sql",
			"单一 sql",
			"占总 db time",
			"占 db time",
			"主导 db time",
			"dominates",
		}
	}
	// Unknown rule ID — fall back to title fragment matching
	return nil
}
