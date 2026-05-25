/*-------------------------------------------------------------------------
 *
 * openaicompat_usage_test.go
 *	  Test cases for openaicompat_usage.go (provider package):
 *	  TestOAIUsage_UnmarshalExtra, TestUsage_HitRate.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/openaicompat_usage_test.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"encoding/json"
	"testing"
)

// TestOAIUsage_UnmarshalExtra ensures vendor-specific cache fields are captured
// into Extra, including nested prompt_tokens_details.cached_tokens.
func TestOAIUsage_UnmarshalExtra(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantKey string
		wantVal int
	}{
		{
			name:    "openai/glm cached_tokens nested",
			body:    `{"prompt_tokens":100,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":40}}`,
			wantKey: "cached_tokens",
			wantVal: 40,
		},
		{
			name:    "deepseek prompt_cache_hit_tokens",
			body:    `{"prompt_tokens":100,"completion_tokens":50,"prompt_cache_hit_tokens":30,"prompt_cache_miss_tokens":70}`,
			wantKey: "prompt_cache_hit_tokens",
			wantVal: 30,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var u oaiUsage
			if err := json.Unmarshal([]byte(tc.body), &u); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if u.PromptTokens != 100 || u.CompletionTokens != 50 {
				t.Errorf("typed fields wrong: %+v", u)
			}
			if got := u.Extra[tc.wantKey]; got != tc.wantVal {
				t.Errorf("Extra[%q] = %d, want %d (all=%v)", tc.wantKey, got, tc.wantVal, u.Extra)
			}
		})
	}
}

func TestUsage_HitRate(t *testing.T) {
	cases := []struct {
		name string
		u    Usage
		want float64
	}{
		{"no cache", Usage{InputTokens: 100}, 0},
		{"half hit", Usage{CacheReadTokens: 50, CacheMissTokens: 50}, 0.5},
		{"full hit all read", Usage{CacheReadTokens: 100}, 1.0},
		{"anthropic style", Usage{InputTokens: 50, CacheReadTokens: 50}, 0.5},
	}
	for _, tc := range cases {
		if got := tc.u.HitRate(); got != tc.want {
			t.Errorf("%s: HitRate = %v, want %v", tc.name, got, tc.want)
		}
	}
}
