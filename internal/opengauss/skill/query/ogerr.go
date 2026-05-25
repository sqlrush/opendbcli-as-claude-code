/*-------------------------------------------------------------------------
 *
 * ogerr.go
 *	  OGErrEntry represents a single OpenGauss SQLSTATE error knowledge
 *	  base entry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/query/ogerr.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// OGErrEntry represents a single OpenGauss SQLSTATE error knowledge base entry.
type OGErrEntry struct {
	Code     string   // "53300"
	Name     string   // "too_many_connections"
	Severity string   // "HIGH", "MEDIUM", "LOW"
	Cause    string   // What triggers this error
	DiagCmds []string // OpenDB commands for diagnosis
	Fix      []string // Fix recommendations
}

// OGErrSkill provides OpenGauss SQLSTATE error diagnosis.
type OGErrSkill struct{}

// NewOGErrSkill creates a OGErrSkill.
func NewOGErrSkill() *OGErrSkill {
	return &OGErrSkill{}
}

func (s *OGErrSkill) Name() string                       { return "ogerr" }
func (s *OGErrSkill) Description() string                { return "OpenGauss SQLSTATE error knowledge base (30+ entries)" }
func (s *OGErrSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *OGErrSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "ogerr",
		Description: "Look up OpenGauss SQLSTATE error codes or list common errors",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "SQLSTATE code (e.g., 53300 or 23505). Empty to list common errors.",
				},
			},
		},
	}
}

func (s *OGErrSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "ogerr",
		Usage:    "/ogerr [code]",
		Examples: []string{"/ogerr 53300", "/ogerr 23505", "/ogerr"},
	}
}

func (s *OGErrSkill) Validate(_ skill.Params) error { return nil }

func (s *OGErrSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if args == "" {
		return s.listCommon()
	}
	return s.lookupCode(args)
}

// listCommon renders a summary of all entries in the knowledge base.
func (s *OGErrSkill) listCommon() (*skill.Result, error) {
	sections := []format.PanelSection{
		{
			Header: "Common OpenGauss SQLSTATE Errors",
			Lines:  buildSummaryLines(),
		},
	}

	rendered := format.Panel("ogerr", sections)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("%d SQLSTATE errors in knowledge base", len(ogErrKnowledgeBase)),
	}, nil
}

// buildSummaryLines returns a line per entry: "CODE  name  (severity)"
func buildSummaryLines() []string {
	// Preserve a stable display order.
	order := []string{
		"23502", "23503", "23505",
		"25006", "25P02",
		"28P01",
		"3D000",
		"40001", "40P01", "40P02",
		"42601", "42703", "42P01", "42P07", "42710",
		"53100", "53200", "53300",
		"54000",
		"55000", "55P03",
		"57014", "57P01", "57P02", "57P03",
		"58030", "58P01",
		"08003", "08006",
		"XX000",
	}

	lines := make([]string, 0, len(order))
	for _, code := range order {
		entry, ok := ogErrKnowledgeBase[code]
		if !ok {
			continue
		}
		lines = append(lines, fmt.Sprintf("%-6s %-30s [%s]", entry.Code, entry.Name, entry.Severity))
	}
	return lines
}

// lookupCode looks up a specific SQLSTATE code in the knowledge base.
func (s *OGErrSkill) lookupCode(args string) (*skill.Result, error) {
	code := strings.TrimSpace(args)

	entry, ok := ogErrKnowledgeBase[code]
	if !ok {
		return &skill.Result{
			Type:    skill.ResultText,
			Rendered: fmt.Sprintf("SQLSTATE %s not found in knowledge base. Use /ogerr to list all.", code),
			Summary: fmt.Sprintf("SQLSTATE %s -- not found", code),
		}, nil
	}

	rendered := renderOGErrEntry(entry)
	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("SQLSTATE %s -- %s (%s)", entry.Code, entry.Name, entry.Severity),
	}, nil
}

// renderOGErrEntry formats a knowledge base entry as a Panel.
func renderOGErrEntry(e *OGErrEntry) string {
	lines := []string{
		fmt.Sprintf("Code     : %s", e.Code),
		fmt.Sprintf("Name     : %s", e.Name),
		fmt.Sprintf("Severity : %s", e.Severity),
		fmt.Sprintf("Cause    : %s", e.Cause),
	}

	sections := []format.PanelSection{
		{Header: "Error Detail", Lines: lines},
	}

	if len(e.DiagCmds) > 0 {
		diagLines := make([]string, len(e.DiagCmds))
		for i, cmd := range e.DiagCmds {
			diagLines[i] = cmd
		}
		sections = append(sections, format.PanelSection{
			Header: "Diagnostic Commands",
			Lines:  diagLines,
		})
	}

	if len(e.Fix) > 0 {
		fixLines := make([]string, len(e.Fix))
		for i, f := range e.Fix {
			fixLines[i] = fmt.Sprintf("%d. %s", i+1, f)
		}
		sections = append(sections, format.PanelSection{
			Header: "Fix Recommendations",
			Lines:  fixLines,
		})
	}

	return format.Panel(fmt.Sprintf("SQLSTATE %s", e.Code), sections)
}
