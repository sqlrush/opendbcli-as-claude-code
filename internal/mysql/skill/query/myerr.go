/*-------------------------------------------------------------------------
 *
 * myerr.go
 *	  MErrEntry represents a single MySQL error knowledge base entry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/query/myerr.go
 *
 *-------------------------------------------------------------------------
 */
package query

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// MErrEntry represents a single MySQL error knowledge base entry.
type MErrEntry struct {
	Code     int      // 1062
	Name     string   // "ER_DUP_ENTRY"
	Severity string   // "HIGH", "MEDIUM", "LOW"
	Cause    string   // Chinese description
	DiagCmds []string // OpenDB commands for diagnosis
	Fixes    []string // Fix recommendations
}

// MyErrSkill provides MySQL error diagnosis: meaning, causes, diagnostic commands, and fix recommendations.
type MyErrSkill struct {
	driver db.Driver
}

// NewMyErrSkill creates a MyErrSkill backed by the given driver.
func NewMyErrSkill(driver db.Driver) *MyErrSkill {
	return &MyErrSkill{driver: driver}
}

func (s *MyErrSkill) Name() string        { return "myerr" }
func (s *MyErrSkill) Description() string  { return "MySQL error knowledge base (30+ entries)" }
func (s *MyErrSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *MyErrSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "myerr",
		Description: "Look up MySQL error codes or list common errors",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"args": map[string]any{
					"type":        "string",
					"description": "MySQL error code (e.g., 1062). Empty to list common errors.",
				},
			},
		},
	}
}

func (s *MyErrSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Command:  "myerr",
		Aliases:  []string{"mysqlerr"},
		Usage:    "/myerr [error_code]",
		Examples: []string{"/myerr 1062", "/myerr 2002", "/myerr"},
	}
}

func (s *MyErrSkill) Validate(_ skill.Params) error { return nil }

func (s *MyErrSkill) Execute(_ context.Context, params skill.Params) (*skill.Result, error) {
	args := strings.TrimSpace(params.StringOr("args", ""))

	if args == "" {
		return s.listCommonErrors()
	}
	return s.lookupCode(args)
}

// listCommonErrors lists all errors in the knowledge base grouped by severity.
func (s *MyErrSkill) listCommonErrors() (*skill.Result, error) {
	// Sort codes for deterministic output.
	codes := make([]int, 0, len(mysqlErrKB))
	for code := range mysqlErrKB {
		codes = append(codes, code)
	}
	sort.Ints(codes)

	var rows [][]any
	for _, code := range codes {
		entry := mysqlErrKB[code]
		rows = append(rows, []any{
			entry.Code,
			entry.Name,
			entry.Severity,
			entry.Cause,
		})
	}

	qr := &db.QueryResult{
		Columns: []string{"Code", "Name", "Severity", "Description"},
		Rows:    rows,
	}
	table := format.FormatTableOpts(qr, format.TableOptions{MaxRows: 50, TermWidth: 120})

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: fmt.Sprintf("MySQL Error Knowledge Base (%d entries)\n\n%s\nUsage: /myerr <code> for details", len(codes), table),
		Summary:  fmt.Sprintf("%d MySQL errors in knowledge base", len(codes)),
	}, nil
}

// lookupCode looks up a specific MySQL error code in the knowledge base.
func (s *MyErrSkill) lookupCode(args string) (*skill.Result, error) {
	code, err := strconv.Atoi(strings.TrimSpace(args))
	if err != nil {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("Invalid error code: %s (usage: /myerr 1062)", args),
		}, nil
	}

	entry, ok := mysqlErrKB[code]
	if !ok {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: fmt.Sprintf("MySQL error %d not in knowledge base", code),
			Summary:  fmt.Sprintf("MySQL-%d -- not found", code),
		}, nil
	}

	rendered := renderMErrEntry(entry)

	return &skill.Result{
		Type:     skill.ResultText,
		Rendered: rendered,
		Summary:  fmt.Sprintf("MySQL-%d %s (%s)", entry.Code, entry.Name, entry.Severity),
	}, nil
}

// renderMErrEntry formats a knowledge base entry as a Panel (consistent with PG /pgerr).
func renderMErrEntry(e *MErrEntry) string {
	lines := []string{
		fmt.Sprintf("Code     : %d", e.Code),
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

	if len(e.Fixes) > 0 {
		fixLines := make([]string, len(e.Fixes))
		for i, f := range e.Fixes {
			fixLines[i] = fmt.Sprintf("%d. %s", i+1, f)
		}
		sections = append(sections, format.PanelSection{
			Header: "Fix Recommendations",
			Lines:  fixLines,
		})
	}

	return format.Panel(fmt.Sprintf("MySQL-%d", e.Code), sections)
}
