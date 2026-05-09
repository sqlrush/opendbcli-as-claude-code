/*-------------------------------------------------------------------------
 *
 * skill.go
 *	  ErrorSkill implements the /error command.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/skill.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

// ErrorSkill implements the /error command.
//
// Usage:
//   /error                 — list all registered error codes (grouped by module)
//   /error ERR-XXYYYY      — show details for one code + live usage count
type ErrorSkill struct{}

// NewErrorSkill constructs the skill.
func NewErrorSkill() *ErrorSkill {
	return &ErrorSkill{}
}

func (s *ErrorSkill) Name() string                     { return "error" }
func (s *ErrorSkill) Description() string              { return "查看 OpenDB 错误码详情与统计" }
func (s *ErrorSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *ErrorSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "error",
		Description: "查看 OpenDB 错误码详情与统计",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "ERR-XXYYYY 格式错误码；为空则列出全部",
				},
			},
		},
	}
}

func (s *ErrorSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:     "error",
		Aliases:     []string{"err"},
		Usage:       "/error [ERR-XXYYYY]",
		Description: "查看某错误码详情，或列出全部已注册错误码",
		Examples: []string{
			"/error",
			"/error ERR-030001",
		},
	}
}

func (s *ErrorSkill) Validate(_ skill.Params) error { return nil }

func (s *ErrorSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	// CLI dispatches everything after the slash-command as `args`.
	// Tool-calling invocations use `code`. Both are accepted.
	code := strings.TrimSpace(params.StringOr("code", ""))
	if code == "" {
		code = strings.TrimSpace(params.StringOr("args", ""))
	}
	if code == "" {
		return renderList(), nil
	}

	code = strings.ToUpper(code)
	entry, known := Lookup(code)
	if !known {
		return &skill.Result{
			Type: skill.ResultText,
			Data: fmt.Sprintf("未知错误码 %s — 请确认格式为 ERR-XXYYYY", code),
		}, nil
	}
	return renderDetail(entry), nil
}

func renderDetail(e Entry) *skill.Result {
	var b strings.Builder
	fmt.Fprintf(&b, "\n  %s  %s\n", e.Code, e.Title)
	b.WriteString("  ──────────────────────────────\n")
	fmt.Fprintf(&b, "  模块:  %s (%s)\n", e.Module, Module(e.Code))
	fmt.Fprintf(&b, "  级别:  %s\n", e.Severity)
	if e.Advice != "" {
		fmt.Fprintf(&b, "  建议:  %s\n", e.Advice)
	}
	fmt.Fprintf(&b, "  次数:  %d (本进程累计)\n", Count(e.Code))
	fmt.Fprintf(&b, "  日志:  %s\n", CrashLogPath())
	return &skill.Result{
		Type:    skill.ResultText,
		Data:    b.String(),
		Summary: fmt.Sprintf("%s 本次累计 %d", e.Code, Count(e.Code)),
	}
}

func renderList() *skill.Result {
	all := AllEntries()
	var b strings.Builder
	b.WriteString("\n  OpenDB 错误码注册表\n")
	b.WriteString("  ──────────────────────────────\n")

	var curModule string
	for _, e := range all {
		if e.Module != curModule {
			curModule = e.Module
			fmt.Fprintf(&b, "\n  [%s]\n", strings.ToUpper(curModule))
		}
		cnt := Count(e.Code)
		marker := " "
		if cnt > 0 {
			marker = "●"
		}
		fmt.Fprintf(&b, "  %s %s  %-6s %s", marker, e.Code, e.Severity, e.Title)
		if cnt > 0 {
			fmt.Fprintf(&b, "  (%d 次)", cnt)
		}
		b.WriteByte('\n')
	}
	b.WriteString("\n  提示:  /error ERR-XXYYYY  查看某条详情\n")
	return &skill.Result{
		Type:    skill.ResultText,
		Data:    b.String(),
		Summary: fmt.Sprintf("%d 条已注册错误码", len(all)),
	}
}
