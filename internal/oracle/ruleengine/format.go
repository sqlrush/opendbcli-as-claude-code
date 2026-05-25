/*-------------------------------------------------------------------------
 *
 * format.go
 *	  FormatDiagOutput renders a DiagOutput into terminal-friendly text
 *	  with Chinese labels, box-drawing lines, and structured sections.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/ruleengine/format.go
 *
 *-------------------------------------------------------------------------
 */
package ruleengine

import (
	"fmt"
	"strings"
)

// FormatDiagOutput renders a DiagOutput into terminal-friendly text with
// Chinese labels, box-drawing lines, and structured sections.
func FormatDiagOutput(output *DiagOutput, cfg Config) string {
	if output == nil || output.Primary == nil {
		return "  (无规则匹配)\n"
	}

	var b strings.Builder

	// Top border.
	b.WriteString("\n── 规则诊断 ────────────────────────────────────────────────────\n\n")

	// Primary root cause.
	formatDiagnosis(&b, output.Primary, "根因", cfg)

	// Absorbed info on primary — show at most 5 related rules.
	if len(output.Primary.AbsorbedNames) > 0 {
		b.WriteString("\n  ── 关联分析 ──\n")
		shown := 0
		for _, name := range output.Primary.AbsorbedNames {
			if shown >= 5 {
				b.WriteString(fmt.Sprintf("  • ... 及其他 %d 个关联规则\n", len(output.Primary.AbsorbedNames)-5))
				break
			}
			b.WriteString(fmt.Sprintf("  • %s: 受根因影响的下游症状，解决根因后将同步缓解\n", name))
			shown++
		}
	}

	// Secondary root cause (if present).
	if output.Secondary != nil {
		b.WriteString("\n  ── 次要根因 ──────────────────────────────────────────────────\n\n")
		formatDiagnosis(&b, output.Secondary, "次因", cfg)
	}

	// Bottom border.
	b.WriteString("\n────────────────────────────────────────────────────────────────\n")

	return b.String()
}

// formatDiagnosis renders one diagnosis block (primary or secondary).
func formatDiagnosis(b *strings.Builder, d *Diagnosis, label string, cfg Config) {
	// Cause line.
	b.WriteString(fmt.Sprintf("  %s: %s\n", label, d.Cause))

	// Severity and confidence.
	confidencePct := int(d.Confidence * 100)
	if confidencePct > 100 {
		confidencePct = 100
	}
	b.WriteString(fmt.Sprintf("  严重程度: %s %s    置信度: %d%%\n",
		d.Severity.Label(), d.Severity.String(), confidencePct))

	// Findings (evidence chain) — only show real evidence, not absorbed names.
	if len(d.Findings) > 0 {
		b.WriteString("\n  ── 证据链 ──\n")
		for i, f := range d.Findings {
			b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, f.Desc))
		}
	}

	// Actions (remediation).
	if len(d.Actions) > 0 {
		b.WriteString("\n  ── 处置建议 ──\n")
		for i, a := range d.Actions {
			b.WriteString(fmt.Sprintf("  [%s] %d. %s\n", string(a.Type), i+1, a.Desc))

			// Command: show SkillCommand or RawSQL based on config.
			cmd := a.RawSQL
			if cfg.OutputMode == OutputSkill && a.SkillCommand != "" {
				cmd = a.SkillCommand
			}
			if cmd != "" {
				b.WriteString(fmt.Sprintf("         > %s\n", cmd))
			}

			if a.Risk != "" {
				b.WriteString(fmt.Sprintf("         风险: %s\n", a.Risk))
			}
		}
	}
}
