/*-------------------------------------------------------------------------
 *
 * vllm.go
 *	  VLLMAdapter handles vLLM deployments (NVIDIA GPU,
 *	  production-grade). Uses OpenAI-compatible protocol but with unique
 *	  capabilities: prefix caching, guided_json/regex, tensor
 *	  parallelism.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/vllm.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"context"
	"net/http"
)

// VLLMAdapter handles vLLM deployments (NVIDIA GPU, production-grade).
// Uses OpenAI-compatible protocol but with unique capabilities:
// prefix caching, guided_json/regex, tensor parallelism.
type VLLMAdapter struct {
	inner *OpenAICompatAdapter
}

// NewVLLMAdapter creates an adapter for local vLLM deployments.
func NewVLLMAdapter(baseURL, model string) *VLLMAdapter {
	if baseURL == "" {
		baseURL = "http://localhost:8000/v1"
	}
	return &VLLMAdapter{
		inner: NewOpenAICompatAdapter(baseURL, model, "", "vllm"),
	}
}

func (a *VLLMAdapter) Name() string { return "vllm" }

func (a *VLLMAdapter) Capability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "vllm",
		MaxContextWindow: 32_768, // Model-dependent, configurable via --max-model-len
		MaxOutputTokens:  4096,
		Thinking: ThinkingCapability{
			Supported:       true,
			Mode:            ThinkingAutoTags,
			MultiTurnPolicy: ThinkingStripAll,
		},
		Caching: CachingCapability{
			Mode: CachingAutomatic, // vLLM prefix caching is automatic
		},
		ToolCalling: ToolCallingCapability{
			Supported: true,
			Format:    ToolFormatOpenAICompatible,
		},
		RateLimit: RateLimitCapability{
			IsLocal: true,
		},
		Output: OutputCapability{
			SupportsStructuredOutput: true, // guided_json, guided_regex
		},
	}
}

func (a *VLLMAdapter) EnhanceRequest(req *Request) {}

func (a *VLLMAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	return a.inner.Chat(ctx, req)
}

func (a *VLLMAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	return a.inner.ChatStream(ctx, req)
}

func (a *VLLMAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	return nil
}
