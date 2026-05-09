/*-------------------------------------------------------------------------
 *
 * dispatcher.go
 *	  Dispatcher routes user input to the appropriate handler based on
 *	  input classification.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dispatch/dispatcher.go
 *
 *-------------------------------------------------------------------------
 */
package dispatch

import (
	"context"
	"fmt"
	"strings"

	"github.com/sqlrush/opendb/internal/skill"
)

// Dispatcher routes user input to the appropriate handler based on input classification.
type Dispatcher struct {
	registry *skill.Registry
	executor *skill.Executor
}

// NewDispatcher creates a Dispatcher backed by the given registry and executor.
func NewDispatcher(registry *skill.Registry, executor *skill.Executor) *Dispatcher {
	return &Dispatcher{
		registry: registry,
		executor: executor,
	}
}

// Dispatch classifies the input and routes it to the appropriate handler.
//
// Returns:
//   - For slash commands: executes the skill via the executor
//   - For SQL: returns an error (no driver connected yet)
//   - For natural language: returns an error (no LLM configured)
//   - For empty input: returns nil result and nil error
func (d *Dispatcher) Dispatch(ctx context.Context, input string) (*skill.Result, error) {
	inputType := ClassifyInput(input)

	switch inputType {
	case InputEmpty:
		return nil, nil

	case InputSlashCommand:
		return d.dispatchSlashCommand(ctx, input)

	case InputSQL:
		return d.dispatchSQL(ctx, input)

	case InputNaturalLanguage:
		return d.dispatchNaturalLanguage(ctx, input)

	default:
		return nil, fmt.Errorf("unknown input type: %d", inputType)
	}
}

// dispatchSlashCommand parses and executes a slash command.
func (d *Dispatcher) dispatchSlashCommand(ctx context.Context, input string) (*skill.Result, error) {
	name, args := ParseSlashCommand(input)
	params := skill.ParamsFromMap(map[string]any{
		"args": args,
	})
	return d.executor.Execute(ctx, name, params)
}

// dispatchNaturalLanguage routes natural language to the "llm" skill for LLM processing.
func (d *Dispatcher) dispatchNaturalLanguage(ctx context.Context, input string) (*skill.Result, error) {
	params := skill.ParamsFromMap(map[string]any{
		"args": strings.TrimSpace(input),
	})
	return d.executor.Execute(ctx, "llm", params)
}

// dispatchSQL routes SQL input to the "sql" skill for execution.
func (d *Dispatcher) dispatchSQL(ctx context.Context, input string) (*skill.Result, error) {
	params := skill.ParamsFromMap(map[string]any{
		"sql": strings.TrimSpace(input),
	})
	return d.executor.Execute(ctx, "sql", params)
}

// ParseSlashCommand splits a slash command into its name and arguments.
//
// Examples:
//   - "/slowsql 5000" returns name="slowsql", args="5000"
//   - "/explain SELECT * FROM t" returns name="explain", args="SELECT * FROM t"
//   - "/help" returns name="help", args=""
func ParseSlashCommand(input string) (name string, args string) {
	trimmed := strings.TrimSpace(input)

	// Remove the leading slash.
	if strings.HasPrefix(trimmed, "/") {
		trimmed = trimmed[1:]
	}

	// Split on first whitespace to separate command name from arguments.
	spaceIdx := strings.IndexAny(trimmed, " \t")
	if spaceIdx < 0 {
		return trimmed, ""
	}

	return trimmed[:spaceIdx], strings.TrimSpace(trimmed[spaceIdx+1:])
}
