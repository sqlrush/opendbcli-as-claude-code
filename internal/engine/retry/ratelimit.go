/*-------------------------------------------------------------------------
 *
 * ratelimit.go
 *	  ParseRateLimitInfo extracts rate limit info from provider-specific
 *	  headers. Returns nil if prefix is empty.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/retry/ratelimit.go
 *
 *-------------------------------------------------------------------------
 */
package retry

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// parseRetryAfter extracts the Retry-After duration from response headers.
func parseRetryAfter(headers http.Header) time.Duration {
	if headers == nil {
		return 0
	}

	v := headers.Get("Retry-After")
	if v == "" {
		return 0
	}

	// Try numeric seconds (integer or float)
	if seconds, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(seconds * float64(time.Second))
	}

	// Try HTTP-date format
	if t, err := http.ParseTime(v); err == nil {
		d := time.Until(t)
		if d < 0 {
			return 0
		}
		return d
	}

	return 0
}

// ParseRateLimitInfo extracts rate limit info from provider-specific headers.
// Returns nil if prefix is empty.
func ParseRateLimitInfo(headers http.Header, prefix string) *provider.RateLimitInfo {
	if prefix == "" {
		return nil
	}

	info := &provider.RateLimitInfo{}

	// Try both header naming conventions:
	// Anthropic: {prefix}-requests-remaining
	// OpenAI:    {prefix}-remaining-requests
	remaining := headers.Get(prefix + "-requests-remaining")
	if remaining == "" {
		remaining = headers.Get(prefix + "-remaining-requests")
	}
	if v, err := strconv.Atoi(remaining); err == nil {
		info.RemainingRequests = v
	}

	tokenRemaining := headers.Get(prefix + "-tokens-remaining")
	if tokenRemaining == "" {
		tokenRemaining = headers.Get(prefix + "-remaining-tokens")
	}
	if v, err := strconv.Atoi(tokenRemaining); err == nil {
		info.RemainingTokens = v
	}

	return info
}
