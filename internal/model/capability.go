/*-------------------------------------------------------------------------
 *
 * capability.go
 *	  InferCapability derives a default capability ("small", "medium",
 *	  "large") from the model identifier and provider. v1.1.17+ adds the
 *	  "medium" tier for mid-tier cloud models (GLM/DeepSeek/Qwen/Kimi)
 *	  that have enough capability to use autonomous tool calling but
 *	  lack the abstract-rule following of frontier models — they need
 *	  the templated prompt variant.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/capability.go
 *
 *-------------------------------------------------------------------------
 */
package model

import "strings"

// InferCapability derives a default capability ("small", "medium", "large")
// from the model identifier and provider. v1.1.17+ adds the "medium" tier
// for mid-tier cloud models (GLM/DeepSeek/Qwen/Kimi) that have enough
// capability to use autonomous tool calling but lack the abstract-rule
// following of frontier models — they need the templated prompt variant.
//
// Tiers:
//
//	small  → 7B-13B local / -mini / -nano / haiku → guided + templated
//	medium → mid-tier cloud (GLM/DeepSeek/Qwen/Kimi/MiniMax)  → autonomous + templated
//	large  → frontier (Opus/Sonnet/GPT-5/Gemini-Pro/V4-Pro)   → autonomous + strict
//
// Check order: small → large → medium → provider fallback.
//
// Large is checked BEFORE medium so that specific frontier IDs win over
// generic mediumMarkers suffixes. Example: "deepseek-v4-pro" matches both
// "deepseek-v4-pro" (large) and "-pro" (medium); large wins → frontier
// reasoning model gets the strict prompt it can handle. Same for
// "gemini-1.5-pro" / "claude-opus-4-7" etc.
//
// Small is still first so compound IDs like "gpt-4o-mini" resolve to small.
func InferCapability(provider, modelID string) string {
	id := strings.ToLower(modelID)

	for _, marker := range smallMarkers {
		if strings.Contains(id, marker) {
			return "small"
		}
	}
	for _, marker := range largeMarkers {
		if strings.Contains(id, marker) {
			return "large"
		}
	}
	for _, marker := range mediumMarkers {
		if strings.Contains(id, marker) {
			return "medium"
		}
	}

	switch strings.ToLower(provider) {
	case "openai":
		return "medium" // safer default than v1.1.16's "large"; mid-tier OpenAI-compat
	case "ollama":
		return "small"
	}
	return "small"
}

// largeMarkers are substrings in the model ID that strongly indicate a
// frontier-tier model capable of internalizing abstract rules in the strict
// universal prompt. v1.1.17 narrowed this to true frontier models only;
// mid-tier cloud models moved to mediumMarkers.
var largeMarkers = []string{
	// Frontier cloud models (closed-source SOTA)
	"gpt-4", "gpt-5", "gpt-o", "o1-", "o3-", "o4-",
	"claude-3", "claude-4", "claude-opus", "claude-sonnet",
	"gemini-1.5-pro", "gemini-2", "gemini-pro",
	"deepseek-r1",      // R1 reasoning model (frontier-tier reasoning)
	"deepseek-v4-pro",  // V4 Pro: thinking mode default-enabled (frontier-tier)
	// Explicit large size markers (only really big models)
	"70b", "72b", "110b", "120b", "405b",
}

// mediumMarkers are mid-tier cloud models — capable of autonomous tool
// calling but need the templated prompt variant (concrete fill-in patterns
// instead of abstract completion criteria) because they don't internalize
// abstract rules as well as frontier models. Added in v1.1.17 after GLM-5
// was observed producing skeleton output ("2 张表存在膨胀") even when given
// the strict prompt.
var mediumMarkers = []string{
	// DeepSeek non-reasoning models
	"deepseek-v3", "deepseek-v4", "deepseek-chat",
	// Zhipu GLM family (non-reasoning)
	"glm-4", "glm-5",
	// Alibaba Qwen mid-tier
	"qwen-max", "qwen2.5-max", "qwen3-max", "qwen3-plus", "qwen3.5", "qwen3.6",
	// Moonshot Kimi
	"kimi-k2", "moonshot-v1",
	// MiniMax
	"minimax-m",
	// Xiaomi MiMo
	"mimo-v",
	// xAI Grok mid-tier
	"grok-2", "grok-3", "grok-4",
	// Mid-size weights (27B-65B, large enough for autonomous but not frontier)
	"27b", "30b", "32b", "35b", "40b", "65b",
	// Generic "max/plus/pro" suffix (mid-tier convention)
	"-max", "-plus", "-pro",
}

// smallMarkers are substrings in the model ID that indicate a model better
// served by the 3-round guided mode.
var smallMarkers = []string{
	// Size markers
	"0.5b", "1b", "1.5b", "3b", "4b", "7b", "8b", "9b", "13b",
	// Explicit small variants
	"-mini", "-nano", "-tiny", "-small",
	"haiku", "flash-lite", "flash-8b",
	"gpt-3.5", "gpt-4o-mini", "gpt-4-mini",
}
