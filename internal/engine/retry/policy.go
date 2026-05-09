/*-------------------------------------------------------------------------
 *
 * policy.go
 *	  Config configures retry behavior.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/retry/policy.go
 *
 *-------------------------------------------------------------------------
 */
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

// Config configures retry behavior.
type Config struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

// DefaultConfig returns sensible retry defaults.
func DefaultConfig() Config {
	return Config{
		MaxRetries: 5,
		BaseDelay:  500 * time.Millisecond,
		MaxDelay:   30 * time.Second,
	}
}

// Policy implements exponential backoff with error classification.
type Policy struct {
	maxRetries int
	baseDelay  time.Duration
	maxDelay   time.Duration
	capability *provider.RateLimitCapability
}

// NewPolicy creates a retry policy.
func NewPolicy(cfg Config, cap *provider.RateLimitCapability) *Policy {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	return &Policy{
		maxRetries: cfg.MaxRetries,
		baseDelay:  cfg.BaseDelay,
		maxDelay:   cfg.MaxDelay,
		capability: cap,
	}
}

// RetryInfo describes how an error should be handled.
type RetryInfo struct {
	Retryable         bool
	RetryAfter        time.Duration
	IsRateLimit       bool
	IsOverload        bool
	IsServerError     bool
	IsNetworkError    bool
	IsContextTooLong  bool
	IsOutputTruncated bool
}

// Execute wraps an API call with retry logic.
func (p *Policy) Execute(
	ctx context.Context,
	fn func(ctx context.Context) (*provider.Response, error),
) (*provider.Response, error) {
	var lastErr error

	for attempt := 0; attempt <= p.maxRetries; attempt++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		resp, err := fn(ctx)
		if err == nil {
			return resp, nil
		}

		info := p.ClassifyError(err)
		if !info.Retryable {
			return nil, err
		}

		lastErr = err

		if attempt == p.maxRetries {
			break
		}

		delay := p.calculateDelay(attempt, info)

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}

	return nil, fmt.Errorf("max retries (%d) exceeded: %w", p.maxRetries, lastErr)
}

// ClassifyError determines how an error should be handled.
func (p *Policy) ClassifyError(err error) RetryInfo {
	info := RetryInfo{}

	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		// Non-HTTP error (network timeout, connection refused, etc.)
		if p.capability != nil && p.capability.IsLocal {
			return info // Local: don't retry network errors (model may be thinking)
		}
		info.Retryable = true
		info.IsNetworkError = true
		return info
	}

	switch httpErr.StatusCode {
	case 429:
		info.Retryable = true
		info.IsRateLimit = true
		info.RetryAfter = parseRetryAfter(httpErr.Headers)

	case 413:
		info.IsContextTooLong = true
		// Not retryable here — Engine handles compression + retry

	case 408, 409:
		info.Retryable = true

	default:
		if p.capability != nil && httpErr.StatusCode == p.capability.OverloadCode && p.capability.OverloadCode != 0 {
			info.Retryable = true
			info.IsOverload = true
			info.RetryAfter = parseRetryAfter(httpErr.Headers)
		} else if httpErr.StatusCode >= 500 {
			info.Retryable = true
			info.IsServerError = true
		}
	}

	return info
}

func (p *Policy) calculateDelay(attempt int, info RetryInfo) time.Duration {
	if info.RetryAfter > 0 {
		return info.RetryAfter
	}

	// Exponential backoff: baseDelay * 2^attempt
	delay := p.baseDelay * time.Duration(1<<uint(attempt))
	if delay > p.maxDelay {
		delay = p.maxDelay
	}

	// Add 0-25% jitter to prevent thundering herd
	if delay > 0 {
		jitter := time.Duration(rand.Int63n(int64(delay) / 4))
		delay += jitter
	}

	return delay
}
