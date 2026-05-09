/*-------------------------------------------------------------------------
 *
 * ollama.go
 *	  OllamaAdapter wraps OpenAICompatAdapter with Ollama-specific
 *	  behavior. It detects the model family (gemma4, qwen, etc.) and
 *	  returns appropriate capabilities.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/ollama.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"context"
	"net/http"
	"strings"
)

// OllamaAdapter wraps OpenAICompatAdapter with Ollama-specific behavior.
// It detects the model family (gemma4, qwen, etc.) and returns appropriate capabilities.
type OllamaAdapter struct {
	inner *OpenAICompatAdapter
	model string
}

// NewOllamaAdapter creates an adapter for local Ollama deployments.
func NewOllamaAdapter(baseURL, model string) *OllamaAdapter {
	if baseURL == "" {
		baseURL = "http://localhost:11434/v1"
	}
	return &OllamaAdapter{
		inner: NewOpenAICompatAdapter(baseURL, model, "", "ollama"),
		model: model,
	}
}

func (a *OllamaAdapter) Name() string { return "ollama" }

func (a *OllamaAdapter) Capability() *ProviderCapability {
	family := detectOllamaFamily(a.model)
	switch family {
	case "gemma4":
		return a.gemma4Capability()
	case "qwen":
		return a.qwenLocalCapability()
	case "glm":
		return a.glmLocalCapability()
	default:
		return a.defaultCapability()
	}
}

func (a *OllamaAdapter) EnhanceRequest(req *Request) {
	family := detectOllamaFamily(a.model)
	switch family {
	case "gemma4":
		// Gemma 4 uses "think": true to enable reasoning via Ollama API
		if req.Extra == nil {
			req.Extra = make(map[string]any)
		}
		req.Extra["think"] = true
	}
}

func (a *OllamaAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	return a.inner.Chat(ctx, req)
}

func (a *OllamaAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	return a.inner.ChatStream(ctx, req)
}

func (a *OllamaAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	return nil
}

// ── Model-specific capabilities ──

func (a *OllamaAdapter) gemma4Capability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "gemma4",
		MaxContextWindow: 128_000,
		MaxOutputTokens:  8_192,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
		},
		ToolCalling: ToolCallingCapability{
			Supported:        true,
			Format:           ToolFormatOpenAICompatible,
			SupportsParallel: true,
			TextFallback:     true,
		},
		RateLimit: RateLimitCapability{IsLocal: true},
	}
}

func (a *OllamaAdapter) qwenLocalCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "qwen-local",
		MaxContextWindow: 32_768,
		MaxOutputTokens:  8_192,
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
		RateLimit: RateLimitCapability{IsLocal: true},
	}
}

func (a *OllamaAdapter) defaultCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "ollama",
		MaxContextWindow: 32_768,
		MaxOutputTokens:  4096,
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
		RateLimit: RateLimitCapability{IsLocal: true},
	}
}

func (a *OllamaAdapter) glmLocalCapability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "glm-local",
		MaxContextWindow: 128_000,
		MaxOutputTokens:  8_192,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripBetweenTurns,
		},
		ToolCalling: ToolCallingCapability{
			Supported:    true,
			Format:       ToolFormatOpenAICompatible,
			TextFallback: true,
		},
		RateLimit: RateLimitCapability{IsLocal: true},
	}
}

// detectOllamaFamily identifies the model family from the Ollama model name.
func detectOllamaFamily(model string) string {
	lower := strings.ToLower(model)
	switch {
	case strings.HasPrefix(lower, "gemma4"):
		return "gemma4"
	case strings.HasPrefix(lower, "qwen"):
		return "qwen"
	case strings.HasPrefix(lower, "glm"):
		return "glm"
	default:
		return "generic"
	}
}
