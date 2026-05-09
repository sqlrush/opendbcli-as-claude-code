/*-------------------------------------------------------------------------
 *
 * playbook.go
 *	  PlaybookResult holds the output of a playbook diagnosis.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/agent/playbook.go
 *
 *-------------------------------------------------------------------------
 */
package agent

import (
	"context"
	"fmt"

	"github.com/sqlrush/opendb/internal/llm"
	"github.com/sqlrush/opendb/internal/oracle/sentinel"
)

// PlaybookResult holds the output of a playbook diagnosis.
type PlaybookResult struct {
	Analysis     string              // LLM analysis text
	Remediation  sentinel.Remediation // deterministic remediation templates
	Report       sentinel.BurstReport // original report for reference
	TokensUsed   llm.Usage           // LLM token usage
}

// RunPlaybook performs a single-shot playbook diagnosis:
// 1. Compress the burst report
// 2. Get deterministic remediation
// 3. Send to LLM for analysis (1 call, no tools)
// 4. Return combined result
func RunPlaybook(
	ctx context.Context,
	provider llm.Provider,
	report sentinel.BurstReport,
) (PlaybookResult, error) {
	compressed := CompressReport(report)
	remediation := sentinel.GetRemediation(report.Classification.Cause)

	prompt := SentinelAlertPrompt(compressed)
	sysPrompt := SystemPromptForDiagnose(ModePlaybook)

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return PlaybookResult{
			Remediation: remediation,
			Report:      report,
		}, fmt.Errorf("playbook LLM call failed: %w", err)
	}

	return PlaybookResult{
		Analysis:    resp.Content,
		Remediation: remediation,
		Report:      report,
		TokensUsed:  resp.Usage,
	}, nil
}

// FormatPlaybookResult renders the playbook result for CLI display.
func FormatPlaybookResult(pr PlaybookResult) string {
	var b []byte

	b = append(b, "╭─ AI 诊断结果 (playbook 模式) ─╮\n"...)

	if pr.Analysis != "" {
		b = append(b, '\n')
		b = append(b, pr.Analysis...)
		b = append(b, '\n')
	}

	b = append(b, "\n╭─ 推荐排查 SQL ─╮\n"...)
	for i, sql := range pr.Remediation.InvestigateSQL {
		b = append(b, fmt.Sprintf("  %d. %s\n", i+1, sql)...)
	}

	b = append(b, "\n╭─ 修复建议 ─╮\n"...)
	for _, s := range pr.Remediation.Suggestions {
		b = append(b, fmt.Sprintf("  • %s\n", s)...)
	}

	if len(pr.Remediation.Parameters) > 0 {
		b = append(b, "\n╭─ 相关参数 ─╮\n"...)
		for _, p := range pr.Remediation.Parameters {
			b = append(b, fmt.Sprintf("  - %s\n", p)...)
		}
	}

	if pr.TokensUsed.InputTokens > 0 {
		b = append(b, fmt.Sprintf("\n[tokens: in=%d out=%d]\n",
			pr.TokensUsed.InputTokens, pr.TokensUsed.OutputTokens)...)
	}

	return string(b)
}
