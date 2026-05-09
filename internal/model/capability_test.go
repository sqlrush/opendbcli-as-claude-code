/*-------------------------------------------------------------------------
 *
 * capability_test.go
 *	  Test cases for capability.go (model package): TestInferCapability.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/model/capability_test.go
 *
 *-------------------------------------------------------------------------
 */
package model

import "testing"

func TestInferCapability(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		modelID  string
		want     string
	}{
		// model-id based (overrides provider)
		{"gpt-4o on openai", "openai", "gpt-4o", "large"},
		{"gpt-4o-mini on openai", "openai", "gpt-4o-mini", "small"},
		{"claude opus", "openai", "claude-opus-4-7", "large"},
		{"claude haiku", "openai", "claude-haiku-4-5", "small"},
		{"gemini flash-lite", "openai", "gemini-2-flash-lite", "small"},
		{"qwen3 9b on ollama", "ollama", "qwen3.5:9b", "small"},
		{"qwen3 32b on ollama", "ollama", "qwen3.5:32b", "medium"},
		{"llama3 70b on ollama", "ollama", "llama3:70b", "large"},
		{"deepseek-r1 on openai", "openai", "deepseek-r1", "large"},
		{"deepseek-v4 on openai", "openai", "deepseek-v4", "medium"},
		{"glm-5 on openai", "openai", "glm-5", "medium"},
		{"qwen-plus on openai", "openai", "qwen3.6-plus", "medium"},
		{"kimi-k2 on openai", "openai", "kimi-k2-0905-preview", "medium"},

		// provider fallback when model id has no markers (v1.1.17: openai → medium)
		{"unknown model on openai", "openai", "something-weird", "medium"},
		{"unknown model on ollama", "ollama", "something-weird", "small"},

		// total fallback
		{"empty everything", "", "", "small"},
		{"unknown provider", "vllm", "xxx", "small"},

		// case insensitivity
		{"uppercase GPT-4", "openai", "GPT-4-turbo", "large"},
		{"uppercase provider", "OLLAMA", "qwen3:9b", "small"},

		// precedence: medium marker wins over small provider
		{"32b on ollama is medium", "ollama", "qwen3:32b", "medium"},
		// precedence: small marker wins over medium provider
		{"mini on openai is small", "openai", "gpt-4o-mini", "small"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InferCapability(tc.provider, tc.modelID)
			if got != tc.want {
				t.Errorf("InferCapability(%q, %q) = %q, want %q",
					tc.provider, tc.modelID, got, tc.want)
			}
		})
	}
}
