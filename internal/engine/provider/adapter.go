/*-------------------------------------------------------------------------
 *
 * adapter.go
 *	  ProviderAdapter is the unified interface for all LLM providers.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/provider/adapter.go
 *
 *-------------------------------------------------------------------------
 */
package provider

import (
	"context"
	"net/http"
)

// ProviderAdapter is the unified interface for all LLM providers.
type ProviderAdapter interface {
	Chat(ctx context.Context, req *Request) (*Response, error)
	ChatStream(ctx context.Context, req *Request) (Stream, error)
	Capability() *ProviderCapability
	EnhanceRequest(req *Request)
	ParseRateLimitHeaders(headers http.Header) *RateLimitInfo
	Name() string
}

// AdapterWrapFn is a function that wraps a ProviderAdapter (e.g., for recording/replay).
type AdapterWrapFn func(ProviderAdapter) ProviderAdapter

// RateLimitInfo holds parsed rate limit data from response headers.
type RateLimitInfo struct {
	RetryAfterSeconds float64
	RemainingRequests int
	RemainingTokens   int
}
