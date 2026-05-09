/*-------------------------------------------------------------------------
 *
 * skillbridge.go
 *	  Package bridge connects the engine to OpenDB's existing skill and
 *	  model systems.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/bridge/skillbridge.go
 *
 *-------------------------------------------------------------------------
 */
// Package bridge connects the engine to OpenDB's existing skill and model systems.
package bridge

import (
	"context"

	"github.com/sqlrush/opendb/internal/engine/tool"
	"github.com/sqlrush/opendb/internal/skill"
)

// SkillBridge adapts OpenDB's skill.Executor to the engine's tool.SkillExecutor interface.
type SkillBridge struct {
	executor *skill.Executor
	registry *skill.Registry
}

// NewSkillBridge creates a bridge between engine and OpenDB skills.
func NewSkillBridge(executor *skill.Executor, registry *skill.Registry) *SkillBridge {
	return &SkillBridge{executor: executor, registry: registry}
}

// Execute runs a named skill, converting between engine and skill parameter formats.
func (b *SkillBridge) Execute(ctx context.Context, name string, params map[string]any) (*tool.SkillResult, error) {
	sp := skill.ParamsFromMap(params)
	result, err := b.executor.Execute(ctx, name, sp)
	if err != nil {
		return nil, err
	}
	return convertResult(result), nil
}

// SecurityLevel returns the security level of a named skill.
func (b *SkillBridge) SecurityLevel(name string) int {
	s, ok := b.registry.Get(name)
	if !ok {
		return 0
	}
	return int(s.SecurityLevel())
}

// ListTools returns tool definitions from the skill registry.
// Schemas are normalized to JSON Schema 'type: "object"' shape so they pass
// strict providers (DeepSeek V4, OpenAI strict mode). Skills that wrote
// shorthand `{"args": {"type":"string"}}` (omitting the outer object wrapper)
// get auto-wrapped — see normalizeToolSchema.
//
// Skills with an empty ToolDef.Name are deliberately excluded — that's the
// convention skill authors use to mean "CLI-only, not exposed to LLM"
// (e.g. PolicySkill returns skill.ToolDef{}). Strict providers reject any
// tool with empty function.name, so we must filter here too.
func (b *SkillBridge) ListTools() []tool.ToolInfo {
	skills := b.registry.All()
	tools := make([]tool.ToolInfo, 0, len(skills))
	for _, s := range skills {
		td := s.ToolDef()
		if td.Name == "" {
			continue // CLI-only skill, not an LLM tool
		}
		tools = append(tools, tool.ToolInfo{
			Name:          td.Name,
			Description:   td.Description,
			InputSchema:   normalizeToolSchema(td.Parameters),
			SecurityLevel: int(s.SecurityLevel()),
		})
	}
	return tools
}

// normalizeToolSchema ensures the schema is a valid JSON Schema with
// `type: "object"`. Three cases handled:
//
//  1. nil → empty object: {"type":"object","properties":{}}
//  2. already has `type: "object"` → unchanged
//  3. shorthand like {"args": {"type":"string"}} (skill author forgot the
//     outer wrapper) → wrap into {"type":"object","properties":<orig>,
//     "required":[<all keys with required-shaped values>]}
//
// This protects against a long-tail of skills (~30+ across OG/PG/MySQL)
// that wrote the shorthand form. Strict providers (DeepSeek V4) reject the
// shorthand with "schema must be a JSON Schema of 'type: \"object\"', got
// 'type: null'" — see issue surfaced by deepseek-v4-pro 2026-04-26.
func normalizeToolSchema(s any) any {
	if s == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}
	m, ok := s.(map[string]any)
	if !ok {
		return s // unknown shape — pass through, let provider error if invalid
	}
	if _, hasType := m["type"]; hasType {
		return m // already well-formed
	}
	// Shorthand: treat the whole map as `properties`. Required = all keys
	// (skill authors who use shorthand typically expect all params required).
	props := make(map[string]any, len(m))
	required := make([]string, 0, len(m))
	for k, v := range m {
		props[k] = v
		required = append(required, k)
	}
	return map[string]any{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// convertResult converts skill.Result to tool.SkillResult.
func convertResult(r *skill.Result) *tool.SkillResult {
	if r == nil {
		return &tool.SkillResult{}
	}

	sr := &tool.SkillResult{
		Rendered: r.Rendered,
		Summary:  r.Summary,
	}

	switch r.Type {
	case skill.ResultError:
		if s, ok := r.Data.(string); ok {
			sr.Error = s
		} else {
			sr.Error = r.Summary
		}
	case skill.ResultText:
		if s, ok := r.Data.(string); ok {
			sr.Text = s
		}
		sr.Data = r.Data
	case skill.ResultTable:
		sr.Data = r.Data
	default:
		sr.Data = r.Data
	}

	return sr
}
