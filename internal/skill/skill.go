/*-------------------------------------------------------------------------
 *
 * skill.go
 *	  SubCommandDef describes a sub-command of a skill.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/skill.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import "context"

type ResultType uint8

const (
	ResultTable   ResultType = iota // Tabular data
	ResultText                      // Plain/rich text
	ResultRefresh                   // Real-time refresh stream
	ResultError                     // Error display
)

type Result struct {
	Type     ResultType
	Data     any               // structured data (struct/slice for LLM JSON consumption)
	Rendered string            // pre-rendered human-readable text (REPL prefers this over Data)
	Summary  string
	Metadata map[string]string
}

type CLIDef struct {
	Command        string
	Aliases        []string
	Flags          []FlagDef
	Usage          string
	Description    string          // detailed description shown in /help <command>
	Examples       []string
	ArgCompletions []string        // suggested sub-commands for argument-level tab completion
	SubCommands    []SubCommandDef // sub-command definitions for /help <command> <sub>
}

// SubCommandDef describes a sub-command of a skill.
type SubCommandDef struct {
	Name        string
	Usage       string   // e.g. "add <org|instance> <name> [file]"
	Description string
	Examples    []string
}

type FlagDef struct {
	Name         string
	Short        string
	Description  string
	DefaultValue string
	Required     bool
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Skill interface {
	Name() string
	Description() string
	ToolDef() ToolDef
	CLIDef() CLIDef
	Validate(params Params) error
	Execute(ctx context.Context, params Params) (*Result, error)
	SecurityLevel() SecurityLevel
}

// DbtopRefreshSource is returned by dbtop skills in Result.Data.
// Each product (Oracle/MySQL/PG/OG) implements this interface in its
// DbtopRefreshConfig so the REPL can run the dbtop loop without
// importing product-specific packages.
type DbtopRefreshSource interface {
	DbtopInterval() int
	NewDbtopLoop() DbtopLoop
}

// DbtopLoop renders dbtop frames with stateful delta computation.
// Created once per dbtop session; RenderFrame is called repeatedly.
type DbtopLoop interface {
	RenderFrame(ctx context.Context, cols, intervalSec int) []string
}
