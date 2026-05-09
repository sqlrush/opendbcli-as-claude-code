/*-------------------------------------------------------------------------
 *
 * providers.go
 *	  ProviderInfo describes a model provider (cloud or local)
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/setup/providers.go
 *
 *-------------------------------------------------------------------------
 */
package setup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sqlrush/opendb/internal/odberr"
)

// ProviderInfo describes a model provider (cloud or local)
type ProviderInfo struct {
	Name       string
	Vendor     string
	BaseURL    string
	NeedsKey   bool     // cloud providers need API key
	IsLocal    bool     // local deployment
	Models     []string // preset model names
}

// CloudProviders returns the list of cloud model providers
func CloudProviders() []ProviderInfo {
	return []ProviderInfo{
		{
			Name: "Anthropic", Vendor: "Anthropic",
			BaseURL: "https://api.anthropic.com/v1", NeedsKey: true,
			Models: []string{"claude-opus-4-6", "claude-sonnet-4-6", "claude-haiku-4-5"},
		},
		{
			Name: "Google (Gemini)", Vendor: "Google",
			BaseURL: "https://generativelanguage.googleapis.com/v1beta", NeedsKey: true,
			Models: []string{"gemini-3.1-pro", "gemini-3-flash", "gemini-3.1-flash-lite"},
		},
		{
			Name: "Moonshot (Kimi)", Vendor: "Moonshot",
			BaseURL: "https://api.moonshot.cn/v1", NeedsKey: true,
			Models: []string{"kimi-k2.5", "kimi-k2-turbo-preview", "kimi-k2-thinking"},
		},
		{
			Name: "DeepSeek", Vendor: "DeepSeek",
			BaseURL: "https://api.deepseek.com/v1", NeedsKey: true,
			Models: []string{"deepseek-v4", "deepseek-v4-reasoner", "deepseek-chat", "deepseek-reasoner"},
		},
		{
			Name: "MiniMax", Vendor: "MiniMax",
			BaseURL: "https://api.minimax.chat/v1", NeedsKey: true,
			Models: []string{"MiniMax-M2.7", "MiniMax-M2.5", "MiniMax-M2"},
		},
		{
			Name: "Zhipu AI (智谱)", Vendor: "Zhipu AI",
			BaseURL: "https://open.bigmodel.cn/api/paas/v4", NeedsKey: true,
			Models: []string{"glm-5", "glm-5-turbo", "glm-4.7-flash"},
		},
		{
			Name: "Xiaomi (MiMo)", Vendor: "Xiaomi",
			BaseURL: "https://api.xiaomimimo.com/v1", NeedsKey: true,
			Models: []string{"mimo-v2-pro"},
		},
		{
			Name: "Alibaba (Qwen)", Vendor: "Qwen",
			BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", NeedsKey: true,
			Models: []string{"qwen3.6-plus", "qwen-max", "qwen-turbo"},
		},
	}
}

// LocalProviders returns the list of local deployment options
func LocalProviders() []ProviderInfo {
	return []ProviderInfo{
		{
			Name: "Ollama", Vendor: "Ollama",
			BaseURL: "http://localhost:11434", IsLocal: true,
			Models: []string{},
		},
		{
			Name: "llama.cpp (llama-server)", Vendor: "llama.cpp",
			BaseURL: "http://localhost:8081", IsLocal: true,
			Models: []string{},
		},
		{
			Name: "vLLM", Vendor: "vLLM",
			BaseURL: "http://localhost:8000", IsLocal: true,
			Models: []string{},
		},
		{
			Name: "MLX (Apple Silicon)", Vendor: "MLX",
			BaseURL: "http://localhost:8080", IsLocal: true,
			Models: []string{},
		},
	}
}

// AllProviders returns cloud + local providers combined
func AllProviders() []ProviderInfo {
	return append(CloudProviders(), LocalProviders()...)
}

// FetchModelsFromAPI tries to list available models from /v1/models endpoint
func FetchModelsFromAPI(baseURL, apiKey string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := baseURL + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Parse OpenAI-compatible /v1/models response
	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	var candidates []string
	for _, m := range result.Data {
		if isChatModel(m.ID) {
			candidates = append(candidates, m.ID)
		}
	}

	// Probe each candidate concurrently to find actually usable models
	return probeModels(baseURL, apiKey, candidates), nil
}

// isChatModel filters out non-chat models (embedding, image, audio, etc.)
func isChatModel(id string) bool {
	lower := strings.ToLower(id)
	skipPrefixes := []string{"embedding", "text-embedding", "whisper", "tts", "dall", "cogview", "stable-diffusion"}
	skipContains := []string{"embedding", "image", "audio", "speech", "vision-only", "video"}
	for _, p := range skipPrefixes {
		if strings.HasPrefix(lower, p) {
			return false
		}
	}
	for _, s := range skipContains {
		if strings.Contains(lower, s) {
			return false
		}
	}
	return true
}

// probeModels sends a minimal chat request to each model concurrently.
// Returns only models that respond successfully within timeout.
func probeModels(baseURL, apiKey string, models []string) []string {
	if len(models) == 0 {
		return nil
	}

	type probeResult struct {
		model string
		ok    bool
	}

	ch := make(chan probeResult, len(models))
	for _, m := range models {
		model := m
		odberr.SafeGo(odberr.ErrCoreSetupWizard, func() {
			ok := probeOneModel(baseURL, apiKey, model)
			ch <- probeResult{model: model, ok: ok}
		})
	}

	results := make(map[string]bool, len(models))
	for range models {
		r := <-ch
		results[r.model] = r.ok
	}

	// Preserve original order, only include successful ones
	var available []string
	for _, m := range models {
		if results[m] {
			available = append(available, m)
		}
	}
	return available
}

// probeOneModel sends a minimal chat completion request to test if a model is usable.
func probeOneModel(baseURL, apiKey, model string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	payload := fmt.Sprintf(`{"model":"%s","messages":[{"role":"user","content":"hi"}],"max_tokens":1}`, model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/chat/completions", strings.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}
