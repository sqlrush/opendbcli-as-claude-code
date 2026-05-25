/*-------------------------------------------------------------------------
 *
 * capability_test.go
 *	  Test cases for capability.go (provider package):
 *	  TestThinkingModeConstants, TestCachingModeConstants,
 *	  TestToolCallFormatConstants.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/capability_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import "testing"

func TestThinkingModeConstants(t *testing.T) {
	if ThinkingNone != 0 {
		t.Error("ThinkingNone should be 0")
	}
	if ThinkingAdaptive != 1 {
		t.Errorf("ThinkingAdaptive should be 1, got %d", ThinkingAdaptive)
	}
}

func TestCachingModeConstants(t *testing.T) {
	if CachingNone != 0 {
		t.Error("CachingNone should be 0")
	}
	if CachingExplicit != 1 {
		t.Errorf("CachingExplicit should be 1, got %d", CachingExplicit)
	}
}

func TestToolCallFormatConstants(t *testing.T) {
	if ToolFormatOpenAICompatible != 0 {
		t.Error("ToolFormatOpenAICompatible should be 0")
	}
	if ToolFormatAnthropicNative != 1 {
		t.Errorf("ToolFormatAnthropicNative should be 1, got %d", ToolFormatAnthropicNative)
	}
}

func TestAnthropicCapability(t *testing.T) {
	cap := ProviderCapability{
		Name:             "anthropic",
		MaxContextWindow: 1_000_000,
		MaxOutputTokens:  128_000,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAdaptive,
			MultiTurnPolicy: ThinkingPreserveAll,
			ExtractField:    "thinking_blocks",
		},
		Caching: CachingCapability{
			Mode:             CachingExplicit,
			MaxBreakpoints:   4,
			MinCacheTokens:   1024,
			CacheReadField:   "cache_read_input_tokens",
			CacheCreateField: "cache_creation_input_tokens",
		},
		ToolCalling: ToolCallingCapability{
			Supported:      true,
			Format:         ToolFormatAnthropicNative,
			SupportsStrict: true,
		},
		RateLimit: RateLimitCapability{
			HeaderPrefix:  "anthropic-ratelimit",
			HasRetryAfter: true,
			OverloadCode:  529,
		},
		Output: OutputCapability{
			SupportsEffort:     true,
			EffortLevels:       []string{"low", "medium", "high", "max"},
			SupportsSpeed:      true,
			SupportsTaskBudget: true,
		},
	}

	if cap.MaxContextWindow != 1_000_000 {
		t.Errorf("expected 1M context, got %d", cap.MaxContextWindow)
	}
	if !cap.Thinking.Supported {
		t.Error("expected thinking supported")
	}
	if cap.Caching.Mode != CachingExplicit {
		t.Errorf("expected explicit caching, got %d", cap.Caching.Mode)
	}
	if cap.RateLimit.OverloadCode != 529 {
		t.Errorf("expected overload code 529, got %d", cap.RateLimit.OverloadCode)
	}
	if len(cap.Output.EffortLevels) != 4 {
		t.Errorf("expected 4 effort levels, got %d", len(cap.Output.EffortLevels))
	}
}

func TestDeepSeekCapability(t *testing.T) {
	cap := ProviderCapability{
		Name:             "deepseek",
		MaxContextWindow: 128_000,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
			ExtractField:    "reasoning_content",
		},
		Caching: CachingCapability{
			Mode:           CachingAutomatic,
			CacheReadField: "prompt_cache_hit_tokens",
			CacheMissField: "prompt_cache_miss_tokens",
		},
		Output: OutputCapability{
			FixedTemperature: true,
		},
	}

	if cap.Thinking.Mode != ThinkingAutoTags {
		t.Errorf("expected ThinkingAutoTags, got %d", cap.Thinking.Mode)
	}
	if cap.Caching.Mode != CachingAutomatic {
		t.Errorf("expected automatic caching, got %d", cap.Caching.Mode)
	}
	if !cap.Output.FixedTemperature {
		t.Error("expected FixedTemperature true for DeepSeek reasoner")
	}
}

func TestOllamaCapability(t *testing.T) {
	cap := ProviderCapability{
		Name:             "ollama",
		MaxContextWindow: 32_768,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripAll,
		},
		ToolCalling: ToolCallingCapability{
			Supported:    true,
			Format:       ToolFormatOpenAICompatible,
			TextFallback: true,
		},
		RateLimit: RateLimitCapability{
			IsLocal: true,
		},
	}

	if !cap.RateLimit.IsLocal {
		t.Error("expected IsLocal true for Ollama")
	}
	if !cap.ToolCalling.TextFallback {
		t.Error("expected TextFallback true for Ollama")
	}
	if cap.Thinking.MultiTurnPolicy != ThinkingStripAll {
		t.Errorf("expected ThinkingStripAll, got %d", cap.Thinking.MultiTurnPolicy)
	}
}

func TestHTTPErrorMessage(t *testing.T) {
	err := &HTTPError{
		StatusCode: 429,
		Body:       "rate limit exceeded",
	}
	msg := err.Error()
	if msg != "HTTP 429: rate limit exceeded" {
		t.Errorf("unexpected error message: %s", msg)
	}
}
