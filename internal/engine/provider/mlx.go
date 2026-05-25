/*-------------------------------------------------------------------------
 *
 * mlx.go
 *	  MLXAdapter handles Apple MLX deployments (Apple Silicon
 *	  optimized). Uses OpenAI-compatible protocol via mlx_lm.server but
 *	  with unique characteristics: unified memory, zero-copy, LoRA
 *	  fine-tuning.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/mlx.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"context"
	"net/http"
)

// MLXAdapter handles Apple MLX deployments (Apple Silicon optimized).
// Uses OpenAI-compatible protocol via mlx_lm.server but with
// unique characteristics: unified memory, zero-copy, LoRA fine-tuning.
type MLXAdapter struct {
	inner *OpenAICompatAdapter
}

// NewMLXAdapter creates an adapter for local MLX deployments.
func NewMLXAdapter(baseURL, model string) *MLXAdapter {
	if baseURL == "" {
		baseURL = "http://localhost:8080/v1"
	}
	return &MLXAdapter{
		inner: NewOpenAICompatAdapter(baseURL, model, "", "mlx"),
	}
}

func (a *MLXAdapter) Name() string { return "mlx" }

func (a *MLXAdapter) Capability() *ProviderCapability {
	return &ProviderCapability{
		Name:             "mlx",
		MaxContextWindow: 32_768, // Model-dependent
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
		RateLimit: RateLimitCapability{
			IsLocal: true,
		},
	}
}

func (a *MLXAdapter) EnhanceRequest(req *Request) {}

func (a *MLXAdapter) Chat(ctx context.Context, req *Request) (*Response, error) {
	return a.inner.Chat(ctx, req)
}

func (a *MLXAdapter) ChatStream(ctx context.Context, req *Request) (Stream, error) {
	return a.inner.ChatStream(ctx, req)
}

func (a *MLXAdapter) ParseRateLimitHeaders(headers http.Header) *RateLimitInfo {
	return nil
}
