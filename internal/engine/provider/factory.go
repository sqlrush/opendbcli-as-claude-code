/*-------------------------------------------------------------------------
 *
 * factory.go
 *	  AdapterConfig holds the info needed to create a ProviderAdapter.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/factory.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import "fmt"

// AdapterConfig holds the info needed to create a ProviderAdapter.
type AdapterConfig struct {
	Provider string // "anthropic" / "gemini" / "openai" / "ollama" / "vllm" / "mlx"
	Vendor   string // For openai-compat: "deepseek" / "kimi" / "qwen" / etc.
	BaseURL  string
	Model    string
	APIKey   string
}

// NewAdapter creates the appropriate ProviderAdapter based on config.
func NewAdapter(cfg AdapterConfig) (ProviderAdapter, error) {
	switch cfg.Provider {
	case "anthropic":
		return NewAnthropicAdapter(cfg.BaseURL, cfg.Model, cfg.APIKey), nil

	case "gemini":
		return NewGeminiAdapter(cfg.BaseURL, cfg.Model, cfg.APIKey), nil

	case "ollama":
		return NewOllamaAdapter(cfg.BaseURL, cfg.Model), nil

	case "vllm":
		return NewVLLMAdapter(cfg.BaseURL, cfg.Model), nil

	case "mlx":
		return NewMLXAdapter(cfg.BaseURL, cfg.Model), nil

	case "openai":
		vendor := cfg.Vendor
		if vendor == "" {
			vendor = DetectVendor(cfg.BaseURL)
		}
		return NewOpenAICompatAdapter(cfg.BaseURL, cfg.Model, cfg.APIKey, vendor), nil

	default:
		return NewOpenAICompatAdapter(cfg.BaseURL, cfg.Model, cfg.APIKey, cfg.Vendor), nil
	}
}

// MustNewAdapter creates an adapter or panics. For use in tests.
func MustNewAdapter(cfg AdapterConfig) ProviderAdapter {
	a, err := NewAdapter(cfg)
	if err != nil {
		panic(fmt.Sprintf("failed to create adapter: %v", err))
	}
	return a
}
