/*-------------------------------------------------------------------------
 *
 * ora.go
 *	  ORASkill provides ORA error diagnosis: meaning, causes, diagnostic
 *	  commands, and fix recommendations.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/query/ora.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

const alertScanSQL = `SELECT DISTINCT REGEXP_SUBSTR(message_text, 'ORA-\d{5}') AS ora_code,
       COUNT(*) AS occurrences,
       MAX(TO_CHAR(originating_timestamp, 'MM-DD HH24:MI')) AS last_seen
FROM v$diag_alert_ext
WHERE message_level <= 8 AND originating_timestamp > SYSDATE - 1
  AND REGEXP_LIKE(message_text, 'ORA-\d{5}')
GROUP BY REGEXP_SUBSTR(message_text, 'ORA-\d{5}')
ORDER BY COUNT(*) DESC
FETCH FIRST 10 ROWS ONLY`

var oraCodeRe = regexp.MustCompile(`^(?:ORA-)?(\d{1,5})$`)

// ORASkill provides ORA error diagnosis: meaning, causes, diagnostic commands, and fix recommendations.
type ORASkill struct {
	driver db.Driver
}

// NewORASkill creates an ORASkill backed by the given driver.
func NewORASkill(driver db.Driver) *ORASkill {
	return &ORASkill{driver: driver}
}

func (s *ORASkill) Name() string        { return "ora" }
func (s *ORASkill) Description() string  { return "ORA error knowledge base (60+ entries)" }
func (s *ORASkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ORASkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "ora",
		Description: "Look up ORA error codes or scan alert log for recent ORA errors",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "ORA error code (e.g., 04031 or ORA-04031). Empty to scan alert log.",
				},
			},
		},
	}
}

func (s *ORASkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "ora",
		Aliases:  []string{"oraerr"},
		Usage:    "/ora [error_code]",
		Examples: []string{"/ora 04031", "/ora ORA-04031", "/ora"},
	}
}

func (s *ORASkill) Validate(_ skill.Params) error { return nil }

func (s *ORASkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if args == "" {
		return s.scanAlertLog(ctx)
	}
	return s.lookupCode(args)
}

// scanAlertLog queries v$diag_alert_ext for recent ORA errors and renders a summary.
func (s *ORASkill) scanAlertLog(ctx context.Context) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, alertScanSQL)
	if err != nil {
		return nil, fmt.Errorf("alert log scan failed: %w", err)
	}

	if len(result.Rows) == 0 {
		return &skill.Result{
			Type:    skill.ResultText,
			Data:    "最近 24h 未发现 ORA 错误",
			Summary: "最近24h 0 种 ORA 错误",
		}, nil
	}

	// Render the scan table.
	tableText := format.FormatTableOpts(result, format.TableOptions{
		MaxRows:   10,
		TermWidth: 80,
	})

	// Append brief diagnosis for each ORA code found in the knowledge base.
	var b strings.Builder
	b.WriteString(tableText)

	for _, row := range result.Rows {
		if len(row) == 0 {
			continue
		}
		code := fmt.Sprintf("%v", row[0])
		normalized := normalizeORACode(code)
		if entry, ok := oraKnowledgeBase[normalized]; ok {
			b.WriteString(fmt.Sprintf("\n  ORA-%s: %s (%s) — %s",
				entry.Code, entry.Meaning, entry.Severity, entry.Category))
		}
	}
	b.WriteByte('\n')

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: b.String(),
		Summary:  fmt.Sprintf("最近24h %d 种 ORA 错误", len(result.Rows)),
	}, nil
}

// lookupCode looks up a specific ORA error code in the knowledge base.
func (s *ORASkill) lookupCode(args string) (*skill.Result, error) {
	m := oraCodeRe.FindStringSubmatch(strings.TrimSpace(args))
	if m == nil {
		return &skill.Result{
			Type: skill.ResultText,
			Data: fmt.Sprintf("无法识别 ORA 错误码: %s (用法: /ora 04031 或 /ora ORA-04031)", args),
		}, nil
	}

	code := fmt.Sprintf("%05s", m[1])
	entry, ok := oraKnowledgeBase[code]
	if !ok {
		return &skill.Result{
			Type:    skill.ResultText,
			Data:    fmt.Sprintf("ORA-%s 未收录在知识库中", code),
			Summary: fmt.Sprintf("ORA-%s — 未收录", code),
		}, nil
	}

	rendered := renderORAEntry(entry)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("ORA-%s — %s (%s)", entry.Code, entry.Meaning, entry.Severity),
	}, nil
}

// normalizeORACode strips the "ORA-" prefix and pads to 5 digits.
func normalizeORACode(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "ORA-")
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "00000"
	}
	return fmt.Sprintf("%05s", s)
}

// renderORAEntry formats a knowledge base entry as human-readable text.
func renderORAEntry(e *ORAEntry) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("  ORA-%s: %s\n\n", e.Code, e.Message))
	b.WriteString(fmt.Sprintf("    严重程度: %s | 类别: %s\n", e.Severity, e.Category))
	b.WriteString(fmt.Sprintf("    含义: %s\n", e.Meaning))

	b.WriteString("\n    ── 常见原因 ──\n")
	for i, cause := range e.Causes {
		b.WriteString(fmt.Sprintf("    %d. %s\n", i+1, cause))
	}

	if len(e.DiagCmds) > 0 {
		b.WriteString("\n    ── 诊断命令 ──\n")
		for _, cmd := range e.DiagCmds {
			b.WriteString(fmt.Sprintf("    %s\n", cmd))
		}
	}

	b.WriteString("\n    ── 修复建议 ──\n")
	for i, fix := range e.Fixes {
		b.WriteString(fmt.Sprintf("    %d. %s\n", i+1, fix))
	}

	return b.String()
}
