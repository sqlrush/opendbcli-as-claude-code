/*-------------------------------------------------------------------------
 *
 * serializer.go
 *	  v1.2.0 Tool description serializer for PromptToolAdapter. Renders
 *	  a ToolSchema list as compact Markdown that fits in a system prompt
 *	  without bloating context.
 *
 *	  Target budget: 60 tools should serialize to < 2K tokens (~6KB UTF-8).
 *	  Per-tool layout:
 *
 *	    ## <name>
 *	    <description trimmed to one line>
 *	    参数: <key>(<type>, <必填|可选>): <hint> | ...
 *	    示例: <args example if available>
 *
 *	  Description is collapsed to a single line (newlines → spaces). Long
 *	  multi-paragraph tool docs are truncated to ~150 chars to keep the
 *	  per-tool budget ~80-120 tokens.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/serializer.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// SerializeToolsCompact renders a tool list as Markdown suitable for
// PromptModeBuilder injection. Stable output (tools sorted by name) so
// prompt caching can hit consistently.
//
// Each tool occupies ~3-6 lines. 60 tools → ~250 lines / ~2KB tokens.
func SerializeToolsCompact(tools []provider.ToolSchema) string {
	if len(tools) == 0 {
		return "(无可用工具)"
	}

	// Sort by name for deterministic output → better prompt cache hits.
	sorted := make([]provider.ToolSchema, len(tools))
	copy(sorted, tools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var b strings.Builder
	for i, t := range sorted {
		if i > 0 {
			b.WriteString("\n")
		}
		serializeOne(&b, t)
	}
	return b.String()
}

// serializeOne writes one tool's compact description into b.
func serializeOne(b *strings.Builder, t provider.ToolSchema) {
	b.WriteString("## ")
	b.WriteString(t.Name)
	b.WriteString("\n")

	desc := compactDescription(t.Description)
	if desc != "" {
		b.WriteString(desc)
		b.WriteString("\n")
	}

	params := extractParams(t.InputSchema)
	if len(params) == 0 {
		b.WriteString("参数: 无\n")
		return
	}

	b.WriteString("参数: ")
	for i, p := range params {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(p.Name)
		b.WriteString("(")
		b.WriteString(p.Type)
		if p.Required {
			b.WriteString(", 必填")
		} else {
			b.WriteString(", 可选")
		}
		b.WriteString(")")
		if p.Description != "" {
			b.WriteString(": ")
			b.WriteString(compactDescription(p.Description))
		}
	}
	b.WriteString("\n")
}

// compactDescription collapses multi-line descriptions and caps length.
func compactDescription(desc string) string {
	const maxLen = 150
	// Collapse all whitespace runs to single spaces.
	s := strings.Join(strings.Fields(desc), " ")
	if len(s) <= maxLen {
		return s
	}
	// Truncate at word boundary if possible.
	cut := s[:maxLen]
	if idx := strings.LastIndexByte(cut, ' '); idx > maxLen-30 {
		cut = cut[:idx]
	}
	return cut + "..."
}

// paramInfo is an extracted JSON Schema parameter for prompt-friendly display.
type paramInfo struct {
	Name        string
	Type        string
	Required    bool
	Description string
}

// extractParams pulls property names + types from a JSON Schema object
// (whatever shape the tool's InputSchema actually has — typically
// map[string]any with "properties" / "required" keys).
//
// Returns an empty list when the schema is missing or unparseable; the
// serializer renders "参数: 无" in that case.
func extractParams(schema any) []paramInfo {
	if schema == nil {
		return nil
	}
	m, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	props, ok := m["properties"].(map[string]any)
	if !ok {
		return nil
	}
	required := requiredSet(m["required"])

	out := make([]paramInfo, 0, len(props))
	// Stable order: sort keys.
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		spec, _ := props[k].(map[string]any)
		typ := "string"
		desc := ""
		if spec != nil {
			if t, ok := spec["type"].(string); ok {
				typ = t
			}
			if d, ok := spec["description"].(string); ok {
				desc = d
			}
		}
		out = append(out, paramInfo{
			Name:        k,
			Type:        typ,
			Required:    required[k],
			Description: desc,
		})
	}
	return out
}

// requiredSet converts the JSON Schema "required" array into a set.
func requiredSet(req any) map[string]bool {
	arr, ok := req.([]any)
	if !ok {
		return nil
	}
	set := make(map[string]bool, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			set[s] = true
		}
	}
	return set
}

// SerializeToolsCompactSummary returns a one-line tool inventory used in
// banners / logs (e.g., "tools: health, alert, topsql, ..., +57 more").
func SerializeToolsCompactSummary(tools []provider.ToolSchema) string {
	if len(tools) == 0 {
		return "no tools"
	}
	const inlineMax = 5
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	if len(names) <= inlineMax {
		return fmt.Sprintf("tools: %s", strings.Join(names, ", "))
	}
	return fmt.Sprintf("tools: %s, ... +%d more",
		strings.Join(names[:inlineMax], ", "), len(names)-inlineMax)
}
