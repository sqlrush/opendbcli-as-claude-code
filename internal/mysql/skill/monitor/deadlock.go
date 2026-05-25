/*-------------------------------------------------------------------------
 *
 * deadlock.go
 *	  DeadlockSkill shows the latest deadlock info from SHOW ENGINE
 *	  INNODB STATUS.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/mysql/skill/monitor/deadlock.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"strings"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/format"
	"github.com/sqlrush/opendb/internal/skill"
)

// DeadlockSkill shows the latest deadlock info from SHOW ENGINE INNODB STATUS.
type DeadlockSkill struct {
	driver db.Driver
}

// NewDeadlockSkill creates a DeadlockSkill backed by the given driver.
func NewDeadlockSkill(driver db.Driver) *DeadlockSkill {
	return &DeadlockSkill{driver: driver}
}

func (s *DeadlockSkill) Name() string                       { return "deadlock" }
func (s *DeadlockSkill) Description() string                { return "最近死锁信息" }
func (s *DeadlockSkill) Category() string                   { return "monitor" }
func (s *DeadlockSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *DeadlockSkill) CLIDef() skill.CLIDef {
	return skill.CLIDef{Usage: "/deadlock", Examples: []string{"/deadlock"}}
}

func (s *DeadlockSkill) ToolDef() skill.ToolDef {
	return skill.ToolDef{
		Name:        "deadlock",
		Description: "Show the latest detected deadlock from InnoDB engine status",
	}
}

func (s *DeadlockSkill) Validate(_ skill.Params) error { return nil }

func (s *DeadlockSkill) Execute(ctx context.Context, _ skill.Params) (*skill.Result, error) {
	result, err := s.driver.Query(ctx, "SHOW ENGINE INNODB STATUS")
	if err != nil {
		return &skill.Result{Type: skill.ResultError, Summary: err.Error()}, nil
	}

	statusText := extractStatusText(result)
	if statusText == "" {
		return &skill.Result{Type: skill.ResultError, Summary: "empty INNODB STATUS output"}, nil
	}

	deadlockSection := extractDeadlockSection(statusText)
	if deadlockSection == "" {
		return &skill.Result{
			Type:     skill.ResultText,
			Rendered: "当前无死锁记录",
			Summary:  "no deadlocks detected",
		}, nil
	}

	rendered := buildDeadlockPanel(deadlockSection)
	return &skill.Result{
		Type:     skill.ResultText,
		Data:     rendered,
		Rendered: rendered,
		Summary:  "latest deadlock info",
	}, nil
}

// extractDeadlockSection pulls the LATEST DETECTED DEADLOCK section.
func extractDeadlockSection(text string) string {
	lines := strings.Split(text, "\n")
	var collecting bool
	var collected []string
	maxLines := 60

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, "LATEST DETECTED DEADLOCK") {
			collecting = true
			continue
		}

		// Stop at the next section boundary.
		if collecting && isSectionBoundary(trimmed) {
			break
		}

		if collecting {
			if strings.HasPrefix(trimmed, "---") || strings.HasPrefix(trimmed, "===") {
				continue
			}
			if len(collected) < maxLines {
				collected = append(collected, trimmed)
			}
		}
	}

	if len(collected) == 0 {
		return ""
	}
	return strings.Join(collected, "\n")
}

// isSectionBoundary checks if a line marks the start of a new InnoDB status section.
func isSectionBoundary(line string) bool {
	boundaries := []string{
		"TRANSACTIONS",
		"FILE I/O",
		"INSERT BUFFER AND ADAPTIVE HASH INDEX",
		"LOG",
		"BUFFER POOL AND MEMORY",
		"ROW OPERATIONS",
		"SEMAPHORES",
		"BACKGROUND THREAD",
	}
	upper := strings.ToUpper(line)
	for _, b := range boundaries {
		if strings.Contains(upper, b) {
			return true
		}
	}
	return false
}

func buildDeadlockPanel(section string) string {
	lines := strings.Split(section, "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}

	// Limit line length for display
	for i, line := range cleaned {
		if len(line) > 120 {
			cleaned[i] = line[:117] + "..."
		}
	}

	sections := []format.PanelSection{
		{
			Header: "LATEST DETECTED DEADLOCK",
			Lines:  cleaned,
		},
	}

	return format.Panel("Deadlock Info", sections)
}
