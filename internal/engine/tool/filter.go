/*-------------------------------------------------------------------------
 *
 * filter.go
 *	  v1.2.0 Scene-based tool filter for PromptToolAdapter. Selects a
 *	  relevant subset of tools per round based on user intent + previous
 *	  tool calls, instead of dumping all 60+ tools into the system prompt.
 *
 *	  Why: in prompt mode, every tool description costs ~30-80 tokens. 60
 *	  tools / round = 2-5KB of pure tool listings, diluting LLM attention
 *	  and bloating each turn's input. Scene filtering cuts this to 5-15
 *	  tools, ~300-800 tokens, while keeping a fallback "always available"
 *	  set so the LLM is never stuck without options.
 *
 *	  Two-step decision:
 *	    1. Match user message against scene triggers (keyword presence)
 *	    2. Union all matching scenes' tool lists + always-available set
 *
 *	  On subsequent rounds, the previously-invoked tools are kept available
 *	  (LLM might want to re-query with different args) plus any newly
 *	  triggered scene from the user's evolving question.
 *
 *	  Filter is intentionally lenient: too-narrow a scope hurts more than
 *	  too-wide. Prefer false positives ("included an irrelevant tool") over
 *	  false negatives ("LLM wanted this tool but it wasn't in the list").
 *
 *	  Note: ToolFilter is also valuable in native FC mode (smaller tools
 *	  array → better accuracy). v1.2.x will wire it into FC path too.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/tool/filter.go
 *
 *-------------------------------------------------------------------------
 */
package tool

import (
	"strings"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// ToolFilter selects a subset of the full tool list for one LLM turn.
type ToolFilter interface {
	Filter(allTools []provider.ToolSchema, ctx FilterContext) []provider.ToolSchema
}

// FilterContext aliases provider.FilterContext so callers in this package
// can use the local name without re-defining the type. The canonical
// definition lives in provider (so PromptBuilder can reference it without
// a cyclic import — provider is the leaf package, tool depends on it).
type FilterContext = provider.FilterContext

// SceneBasedFilter matches FilterContext against a set of named Scenes,
// each defining keyword triggers + the tool subset that scene activates.
// Tools from all matching scenes are unioned and combined with an
// always-available "fallback" set.
type SceneBasedFilter struct {
	scenes  []Scene
	always  []string // tool names always included regardless of scene
}

// Scene defines a named cluster of tools and the keywords that trigger it.
type Scene struct {
	Name     string   // human-readable identifier (logs/telemetry only)
	Triggers []string // case-insensitive substring match against UserMessage
	Tools    []string // tool names to include when this scene matches
}

// NewSceneBasedFilter constructs a filter with the given scenes and
// always-available tools. Pass DefaultScenes() / DefaultAlwaysAvailable()
// for the standard opendb config.
func NewSceneBasedFilter(scenes []Scene, alwaysAvailable []string) *SceneBasedFilter {
	return &SceneBasedFilter{scenes: scenes, always: alwaysAvailable}
}

// Filter returns the relevant subset of allTools for this turn, preserving
// the input order (so prompt cache hits on consistent ordering).
//
// Selection logic:
//   1. Start with empty active set
//   2. Match scenes against ctx.UserMessage → union their Tools
//   3. Add any tool names in ctx.LastToolCalls (LLM might re-query)
//   4. Add f.always (fallback tools always present)
//   5. Filter allTools to those in the active set
//   6. If active set ended up empty (no triggers matched), return allTools
//      unchanged — better wide than miss.
func (f *SceneBasedFilter) Filter(allTools []provider.ToolSchema, ctx FilterContext) []provider.ToolSchema {
	if len(allTools) == 0 {
		return allTools
	}

	active := make(map[string]bool)

	// Step 2: scene triggers.
	lowerMsg := strings.ToLower(ctx.UserMessage)
	for _, s := range f.scenes {
		if matchesAnyTrigger(lowerMsg, s.Triggers) {
			for _, t := range s.Tools {
				active[t] = true
			}
		}
	}

	// Step 3: previously-called tools stay available.
	for _, name := range ctx.LastToolCalls {
		active[name] = true
	}

	// Step 4: always-available fallback.
	for _, t := range f.always {
		active[t] = true
	}

	// Step 6: if nothing matched, return everything (safe wide default).
	if len(active) == 0 {
		return allTools
	}

	// Step 5: filter preserving original order.
	out := make([]provider.ToolSchema, 0, len(active))
	for _, t := range allTools {
		if active[t.Name] {
			out = append(out, t)
		}
	}
	// Defensive: if filtering dropped everything (no name match — unlikely
	// but possible if config has stale names), fall back to all tools.
	if len(out) == 0 {
		return allTools
	}
	return out
}

// matchesAnyTrigger returns true if lowerMsg contains any trigger substring
// (case-insensitive match — triggers should be pre-lowercased by caller or
// here).
func matchesAnyTrigger(lowerMsg string, triggers []string) bool {
	for _, t := range triggers {
		if t == "" {
			continue
		}
		if strings.Contains(lowerMsg, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// DefaultScenes returns the standard opendb tool scenes covering the
// common diagnostic intents. Tool names refer to the registered skill
// names (across all DB products — the actual availability depends on
// which DB is active).
func DefaultScenes() []Scene {
	return []Scene{
		{
			Name:     "single_sql_tune",
			Triggers: []string{"sql_id", "sql id", "怎么优化", "怎么改", "调优", "sql 慢", "优化空间", "改写"},
			Tools:    []string{"sqltune", "sqlfetch", "explain"},
		},
		{
			Name:     "cluster_diag",
			Triggers: []string{"数据库慢", "出问题", "卡", "死锁", "性能", "响应慢", "诊断", "排查", "慢查询"},
			Tools: []string{
				"health", "alert", "activesessions", "waits",
				"blocktree", "topsql", "slowsql", "lockwait",
			},
		},
		{
			Name:     "wdr_analysis",
			Triggers: []string{"wdr", "awr", "报告", "snapshot", "快照分析"},
			Tools:    []string{"wdranalyze", "wdr_snapshot"},
		},
		{
			Name:     "memory_io",
			Triggers: []string{"内存", "缓存", "命中率", "io", "buffer", "shared_buffers", "work_mem"},
			Tools:    []string{"health", "params", "objstats", "topio"},
		},
		{
			Name:     "object_stats",
			Triggers: []string{"表", "索引", "膨胀", "vacuum", "analyze", "统计信息", "dead tuple"},
			Tools:    []string{"objstats", "tablesize", "indexstats", "vacuumstats"},
		},
		{
			Name:     "session_kill",
			Triggers: []string{"会话", "session", "kill", "中止", "终止"},
			Tools:    []string{"activesessions", "kill"},
		},
		{
			Name:     "config_review",
			Triggers: []string{"参数", "配置", "guc", "spfile", "init.ora"},
			Tools:    []string{"params", "health"},
		},
		{
			Name:     "wait_analysis",
			Triggers: []string{"等待", "wait", "wait_event"},
			Tools:    []string{"waits", "activesessions"},
		},
	}
}

// DefaultAlwaysAvailable returns the tools that should always be in the
// LLM's toolbox regardless of scene. `sql` is the catch-all escape hatch
// for arbitrary queries; `health` is the universal starting point.
func DefaultAlwaysAvailable() []string {
	return []string{"sql", "health"}
}
