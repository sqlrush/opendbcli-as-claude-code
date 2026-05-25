/*-------------------------------------------------------------------------
 *
 * tool_filter.go
 *	  tool_filter.go implements tool visibility filtering based on
 *	  HotConfig. Tools marked "hidden" are removed from LLM's tool list
 *	  entirely. Tools marked "disabled" are visible in CLI but execution
 *	  is blocked. Tools marked "confirm" require confirmation before
 *	  execution.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/cluster/tool_filter.go
 *
 *-------------------------------------------------------------------------
 */
// tool_filter.go implements tool visibility filtering based on HotConfig.
// Tools marked "hidden" are removed from LLM's tool list entirely.
// Tools marked "disabled" are visible in CLI but execution is blocked.
// Tools marked "confirm" require confirmation before execution.
package cluster

import (
	"fmt"

	"github.com/sqlrush/opendb/internal/skill"
)

// ToolFilter wraps a skill.Registry to filter tools based on HotConfig.
type ToolFilter struct {
	hotCfg *HotConfig
}

// NewToolFilter creates a new tool filter.
func NewToolFilter(hotCfg *HotConfig) *ToolFilter {
	return &ToolFilter{hotCfg: hotCfg}
}

// FilterForLLM returns tool definitions visible to LLM (excludes hidden and disabled).
func (tf *ToolFilter) FilterForLLM(tools []skill.ToolDef) []skill.ToolDef {
	filtered := make([]skill.ToolDef, 0, len(tools))
	for _, t := range tools {
		level := tf.hotCfg.GetToolControl(t.Name)
		switch level {
		case "hidden", "disabled":
			continue // LLM cannot see these
		default:
			filtered = append(filtered, t)
		}
	}
	return filtered
}

// FilterForCLI returns tool names visible in CLI (excludes only hidden).
func (tf *ToolFilter) FilterForCLI(skillNames []string) []string {
	filtered := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		level := tf.hotCfg.GetToolControl(name)
		if level != "hidden" {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

// CheckExecution validates whether a tool can be executed.
// Returns nil if allowed, error if blocked.
func (tf *ToolFilter) CheckExecution(toolName string) error {
	level := tf.hotCfg.GetToolControl(toolName)
	switch level {
	case "hidden":
		return fmt.Errorf("tool %q is not available", toolName)
	case "disabled":
		return fmt.Errorf("tool %q is disabled by policy", toolName)
	case "confirm":
		// Caller should handle confirmation prompt.
		return nil
	default:
		return nil
	}
}

// NeedsConfirmation returns true if the tool requires user confirmation.
func (tf *ToolFilter) NeedsConfirmation(toolName string) bool {
	return tf.hotCfg.GetToolControl(toolName) == "confirm"
}
