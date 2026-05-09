/*-------------------------------------------------------------------------
 *
 * innodb.go
 *	  InnoDBSkill shows parsed sections of SHOW ENGINE INNODB STATUS.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/innodb.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// InnoDBSkill shows parsed sections of SHOW ENGINE INNODB STATUS.
type InnoDBSkill struct {
	driver db.Driver
}

// NewInnoDBSkill creates an InnoDBSkill backed by the given driver.
func NewInnoDBSkill(driver db.Driver) *InnoDBSkill {
	return &InnoDBSkill{driver: driver}
}

func (s *InnoDBSkill) Name() string                       { return "innodb" }
func (s *InnoDBSkill) Description() string                { return "InnoDB 引擎状态" }
func (s *InnoDBSkill) Category() string                   { return "monitor" }
func (s *InnoDBSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *InnoDBSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{
		Usage:          "/innodb [section]",
		Examples:       []string{"/innodb", "/innodb transactions", "/innodb log"},
		ArgCompletions: []string{"transactions", "buffer", "log", "semaphores", "all"},
	}
}

func (s *InnoDBSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "innodb",
		Description: "Show InnoDB engine status: transactions, buffer pool, log, semaphores",
		Parameters: map[string]any{
			"section": map[string]any{
				"type":        "string",
				"description": "Section to show: transactions, buffer, log, semaphores, all (default: all)",
			},
		},
	}
}

func (s *InnoDBSkill) Validate(_ skill.Params) error { return nil }

func (s *InnoDBSkill) Execute(ctx context.Context, params skill.Params) (*skill.Result, error) {
	section := strings.ToLower(params.StringOr("args", "all"))
	if section == "" {
		section = strings.ToLower(params.StringOr("section", "all"))
	}

	result, err := s.driver.Query(ctx, "SHOW ENGINE INNODB STATUS")
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	statusText := extractStatusText(result)
	if statusText == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "empty INNODB STATUS output"}, nil
	}

	parsed := parseInnoDBStatus(statusText)
	rendered := buildInnoDBPanel(parsed, section)

	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "InnoDB engine status",
	}, nil
}

// extractStatusText pulls the status text from the query result.
func extractStatusText(result *db.QueryResult) string {
	if len(result.Rows) == 0 {
		return ""
	}
	// The status text is typically in the third column (index 2).
	row := result.Rows[0]
	for i := len(row) - 1; i >= 0; i-- {
		s := asStr(row[i])
		if len(s) > 100 {
			return s
		}
	}
	if len(row) > 0 {
		return asStr(row[len(row)-1])
	}
	return ""
}

// innoDBSections holds parsed sections from INNODB STATUS.
type innoDBSections struct {
	semaphores   []string
	transactions []string
	bufferPool   []string
	log          []string
	rowOps       []string
}

// parseInnoDBStatus splits the INNODB STATUS output into named sections.
func parseInnoDBStatus(text string) innoDBSections {
	var result innoDBSections
	lines := strings.Split(text, "\n")

	currentSection := ""
	maxLinesPerSection := 30

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect section headers (lines of dashes followed by a section name).
		switch {
		case strings.Contains(trimmed, "SEMAPHORES"):
			currentSection = "semaphores"
			continue
		case strings.Contains(trimmed, "TRANSACTIONS"):
			currentSection = "transactions"
			continue
		case strings.Contains(trimmed, "BUFFER POOL AND MEMORY"):
			currentSection = "buffer"
			continue
		case strings.Contains(trimmed, "LOG"):
			currentSection = "log"
			continue
		case strings.Contains(trimmed, "ROW OPERATIONS"):
			currentSection = "rowops"
			continue
		case strings.HasPrefix(trimmed, "---"):
			continue
		case strings.HasPrefix(trimmed, "==="):
			continue
		case trimmed == "":
			continue
		}

		switch currentSection {
		case "semaphores":
			if len(result.semaphores) < maxLinesPerSection {
				result.semaphores = append(result.semaphores, trimmed)
			}
		case "transactions":
			if len(result.transactions) < maxLinesPerSection {
				result.transactions = append(result.transactions, trimmed)
			}
		case "buffer":
			if len(result.bufferPool) < maxLinesPerSection {
				result.bufferPool = append(result.bufferPool, trimmed)
			}
		case "log":
			if len(result.log) < maxLinesPerSection {
				result.log = append(result.log, trimmed)
			}
		case "rowops":
			if len(result.rowOps) < maxLinesPerSection {
				result.rowOps = append(result.rowOps, trimmed)
			}
		}
	}

	return result
}

func buildInnoDBPanel(parsed innoDBSections, section string) string {
	var sections []format.PanelSection

	showAll := section == "all" || section == ""

	if showAll || section == "semaphores" {
		lines := parsed.semaphores
		if len(lines) == 0 {
			lines = []string{"(empty)"}
		}
		sections = append(sections, format.PanelSection{Header: "SEMAPHORES", Lines: truncLines(lines, 15)})
	}

	if showAll || section == "transactions" {
		lines := parsed.transactions
		if len(lines) == 0 {
			lines = []string{"(empty)"}
		}
		sections = append(sections, format.PanelSection{Header: "TRANSACTIONS", Lines: truncLines(lines, 15)})
	}

	if showAll || section == "buffer" {
		lines := parsed.bufferPool
		if len(lines) == 0 {
			lines = []string{"(empty)"}
		}
		sections = append(sections, format.PanelSection{Header: "BUFFER POOL", Lines: truncLines(lines, 15)})
	}

	if showAll || section == "log" {
		lines := parsed.log
		if len(lines) == 0 {
			lines = []string{"(empty)"}
		}
		sections = append(sections, format.PanelSection{Header: "LOG", Lines: truncLines(lines, 15)})
	}

	if showAll {
		lines := parsed.rowOps
		if len(lines) == 0 {
			lines = []string{"(empty)"}
		}
		sections = append(sections, format.PanelSection{Header: "ROW OPERATIONS", Lines: truncLines(lines, 10)})
	}

	if len(sections) == 0 {
		sections = append(sections, format.PanelSection{
			Lines: []string{fmt.Sprintf("unknown section: %s (use: transactions, buffer, log, semaphores, all)", section)},
		})
	}

	return format.Panel("InnoDB Status", sections)
}

// truncLines limits a slice to maxN items.
func truncLines(lines []string, maxN int) []string {
	if len(lines) <= maxN {
		return lines
	}
	result := make([]string, maxN)
	copy(result, lines[:maxN])
	return append(result, fmt.Sprintf("... (%d more lines)", len(lines)-maxN))
}
