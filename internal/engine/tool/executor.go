/*-------------------------------------------------------------------------
 *
 * executor.go
 *	  SkillExecutor is the interface that bridges Engine with OpenDB's
 *	  skill system. Engine depends only on this interface, never on the
 *	  concrete skill package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/executor.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import "context"

// SkillExecutor is the interface that bridges Engine with OpenDB's skill system.
// Engine depends only on this interface, never on the concrete skill package.
type SkillExecutor interface {
	// Execute runs a named skill with the given parameters.
	Execute(ctx context.Context, name string, params map[string]any) (*SkillResult, error)

	// SecurityLevel returns the security level of a skill (0=read-only, 1+=write).
	SecurityLevel(name string) int
}

// ToolLister provides tool schema information for LLM requests.
type ToolLister interface {
	// ListTools returns all available tools with their security levels.
	ListTools() []ToolInfo
}

// ToolInfo holds a tool's schema and security metadata.
type ToolInfo struct {
	Name          string
	Description   string
	InputSchema   any
	SecurityLevel int
}

// SkillResult holds the output of a skill execution.
type SkillResult struct {
	Text     string // Rendered text output
	Data     any    // Structured data (e.g., table rows)
	Summary  string // Brief summary
	Error    string // Error message (if failed)
	Rendered string // Pre-rendered output (preferred over Data)
}
