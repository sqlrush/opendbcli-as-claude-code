/*-------------------------------------------------------------------------
 *
 * capability.go
 *	  ProviderCapability declares what a provider supports. Engine code
 *	  checks these fields instead of hardcoding vendor names.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/capability.go
 *
 *-------------------------------------------------------------------------
 */
package provider

// ProviderCapability declares what a provider supports.
// Engine code checks these fields instead of hardcoding vendor names.
type ProviderCapability struct {
	Name             string
	MaxContextWindow int
	MaxOutputTokens  int

	Thinking    ThinkingCapability
	Caching     CachingCapability
	ToolCalling ToolCallingCapability
	RateLimit   RateLimitCapability
	Output      OutputCapability
}

// ── Thinking ──

// ThinkingMode describes how a provider enables thinking/reasoning.
type ThinkingMode int

const (
	ThinkingNone        ThinkingMode = iota // Not supported
	ThinkingAdaptive                        // Anthropic: model decides
	ThinkingEffortLevel                     // OpenAI: reasoning.effort
	ThinkingBudget                          // Gemini: token budget
	ThinkingEnableFlag                      // Qwen/MiMo/Kimi/GLM: bool flag
	ThinkingAutoTags                        // DeepSeek/Ollama: <think> in output
	ThinkingSplit                           // MiniMax: reasoning_split
)

// ThinkingMultiTurnPolicy controls how thinking content is handled across turns.
type ThinkingMultiTurnPolicy int

const (
	ThinkingPreserveAll       ThinkingMultiTurnPolicy = iota // Anthropic/OpenAI: keep all
	ThinkingStripBetweenTurns                                // DeepSeek/Kimi/Qwen: strip on new user turn
	ThinkingStripAll                                         // Local models: strip everything
)

// ThinkingCapability describes thinking/reasoning support.
type ThinkingCapability struct {
	Supported       bool
	Mode            ThinkingMode
	MultiTurnPolicy ThinkingMultiTurnPolicy
	ExtractField    string                            // "thinking_blocks" / "reasoning_content" / "reasoning_details" / ""
	EnableParams    func(level string) map[string]any // Returns provider-specific params to enable thinking
}

// ── Caching ──

// CachingMode describes how prompt caching works.
type CachingMode int

const (
	CachingNone        CachingMode = iota // No caching
	CachingExplicit                       // Anthropic: client marks breakpoints
	CachingAutomatic                      // DeepSeek/OpenAI/GLM: server-side automatic
	CachingSeparateAPI                    // Gemini: separate caches.create() API
)

// CachingCapability describes prompt caching support.
type CachingCapability struct {
	Mode             CachingMode
	MaxBreakpoints   int    // Anthropic: 4
	MinCacheTokens   int    // Minimum tokens to trigger caching
	CacheReadField   string // Response usage field for cache hits
	CacheMissField   string // Response usage field for cache misses
	CacheCreateField string // Response usage field for cache creation
}

// ── Tool Calling ──

// ToolCallFormat describes the wire format for tool definitions and calls.
type ToolCallFormat int

const (
	ToolFormatOpenAICompatible ToolCallFormat = iota // Most providers
	ToolFormatAnthropicNative                        // Anthropic Messages API
	ToolFormatGeminiNative                           // Gemini generateContent
	ToolFormatTextSimulation                         // Weak models: ```action blocks
)

// ToolCallingCapability describes tool/function calling support.
type ToolCallingCapability struct {
	Supported           bool
	Format              ToolCallFormat
	SupportsParallel    bool     // OpenAI: parallel_tool_calls
	SupportsStrict      bool     // Strict schema validation
	TextFallback        bool     // Fall back to text simulation if native fails
	ThinkingConstraints []string // Constraints when thinking is active
}

// ── Rate Limiting ──

// RateLimitCapability describes rate limiting behavior.
type RateLimitCapability struct {
	HeaderPrefix  string // "anthropic-ratelimit" / "x-ratelimit" / ""
	HasRetryAfter bool
	OverloadCode  int  // 529 for Anthropic, 0 for others
	IsLocal       bool // Local deployments don't retry network errors
}

// ── Output Control ──

// OutputCapability describes output tuning features.
type OutputCapability struct {
	SupportsEffort           bool
	EffortLevels             []string // e.g. ["low","medium","high","max"]
	SupportsSpeed            bool
	SupportsTaskBudget       bool
	SupportsStructuredOutput bool
	SupportsPredictedOutput  bool
	SupportsSeed             bool
	FixedTemperature         bool // Some models don't allow temperature changes
}
