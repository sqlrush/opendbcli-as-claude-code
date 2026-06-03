/*-------------------------------------------------------------------------
 *
 * policy_test.go
 *	  Test cases for policy.go (retry package): TestExecuteSuccess,
 *	  TestExecuteRetryOn429, TestExecuteRetryOn529.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/retry/policy_test.go
 *
 *-------------------------------------------------------------------------
 */
package retry

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/sqlrush/opendb/internal/engine/provider"
)

func TestExecuteSuccess(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	resp, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		return &provider.Response{Content: "ok"}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestExecuteRetryOn429(t *testing.T) {
	p := NewPolicy(
		Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond},
		&provider.RateLimitCapability{HasRetryAfter: true},
	)

	calls := 0
	resp, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		if calls < 3 {
			return nil, &provider.HTTPError{StatusCode: 429, Headers: http.Header{}, Body: "rate limited"}
		}
		return &provider.Response{Content: "ok"}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestExecuteRetryOn529(t *testing.T) {
	p := NewPolicy(
		Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond},
		&provider.RateLimitCapability{OverloadCode: 529},
	)

	calls := 0
	resp, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		if calls < 2 {
			return nil, &provider.HTTPError{StatusCode: 529, Body: "overloaded"}
		}
		return &provider.Response{Content: "ok"}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("expected 'ok', got %q", resp.Content)
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestExecuteNoRetryOn400(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	_, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		return nil, &provider.HTTPError{StatusCode: 400, Body: "bad request"}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (no retry on 400), got %d", calls)
	}
}

func TestExecuteNoRetryOn413(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond}, &provider.RateLimitCapability{})

	calls := 0
	_, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		return nil, &provider.HTTPError{StatusCode: 413, Body: "payload too large"}
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (413 handled by Engine, not retry), got %d", calls)
	}
}

func TestExecuteMaxRetriesExceeded(t *testing.T) {
	p := NewPolicy(
		Config{MaxRetries: 2, BaseDelay: 10 * time.Millisecond, MaxDelay: 20 * time.Millisecond},
		&provider.RateLimitCapability{},
	)

	calls := 0
	_, err := p.Execute(context.Background(), func(ctx context.Context) (*provider.Response, error) {
		calls++
		return nil, &provider.HTTPError{StatusCode: 500, Body: "server error"}
	})

	if err == nil {
		t.Fatal("expected error after max retries")
	}
	if calls != 3 { // 1 initial + 2 retries
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestExecuteContextCanceled(t *testing.T) {
	p := NewPolicy(Config{MaxRetries: 5, BaseDelay: 1 * time.Second}, &provider.RateLimitCapability{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := p.Execute(ctx, func(ctx context.Context) (*provider.Response, error) {
		return nil, &provider.HTTPError{StatusCode: 500, Body: "error"}
	})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestClassifyError429(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{HasRetryAfter: true})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 429})
	if !info.Retryable {
		t.Error("429 should be retryable")
	}
	if !info.IsRateLimit {
		t.Error("429 should be rate limit")
	}
}

func TestClassifyError529(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{OverloadCode: 529})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 529})
	if !info.Retryable {
		t.Error("529 should be retryable when OverloadCode=529")
	}
	if !info.IsOverload {
		t.Error("529 should be overload")
	}
}

func TestClassifyError529NotConfigured(t *testing.T) {
	// Provider doesn't declare 529 as overload → treated as 5xx
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 529})
	if !info.Retryable {
		t.Error("529 as 5xx should be retryable")
	}
	if info.IsOverload {
		t.Error("529 should NOT be overload when OverloadCode not set")
	}
	if !info.IsServerError {
		t.Error("529 should be server error when OverloadCode not set")
	}
}

func TestClassifyError413(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{})
	info := p.ClassifyError(&provider.HTTPError{StatusCode: 413})
	if info.Retryable {
		t.Error("413 should NOT be retryable (Engine handles compression)")
	}
	if !info.IsContextTooLong {
		t.Error("413 should be context too long")
	}
}

func TestClassifyErrorLocal(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{IsLocal: true})
	info := p.ClassifyError(errors.New("connection refused"))
	if info.Retryable {
		t.Error("network errors should not be retryable for local providers")
	}
}

func TestClassifyErrorRemoteNetwork(t *testing.T) {
	p := NewPolicy(DefaultConfig(), &provider.RateLimitCapability{IsLocal: false})
	info := p.ClassifyError(errors.New("connection refused"))
	if !info.Retryable {
		t.Error("network errors should be retryable for remote providers")
	}
	if !info.IsNetworkError {
		t.Error("should be classified as network error")
	}
}

func TestRetryAfterHeader(t *testing.T) {
	p := NewPolicy(
		Config{MaxRetries: 3, BaseDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond},
		&provider.RateLimitCapability{},
	)

	info := p.ClassifyError(&provider.HTTPError{
		StatusCode: 429,
		Headers:    http.Header{"Retry-After": {"2"}},
	})
	if info.RetryAfter != 2*time.Second {
		t.Errorf("expected RetryAfter 2s, got %v", info.RetryAfter)
	}
}
