/*-------------------------------------------------------------------------
 *
 * ratelimit_test.go
 *	  Test cases for ratelimit.go (retry package):
 *	  TestParseRetryAfterSeconds, TestParseRetryAfterFloat,
 *	  TestParseRetryAfterMissing.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/retry/ratelimit_test.go
 *
 *-------------------------------------------------------------------------
 */
package retry

import (
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfterSeconds(t *testing.T) {
	headers := http.Header{"Retry-After": {"5"}}
	d := parseRetryAfter(headers)
	if d != 5*time.Second {
		t.Errorf("expected 5s, got %v", d)
	}
}

func TestParseRetryAfterFloat(t *testing.T) {
	headers := http.Header{"Retry-After": {"2.5"}}
	d := parseRetryAfter(headers)
	if d != 2500*time.Millisecond {
		t.Errorf("expected 2.5s, got %v", d)
	}
}

func TestParseRetryAfterMissing(t *testing.T) {
	headers := http.Header{}
	d := parseRetryAfter(headers)
	if d != 0 {
		t.Errorf("expected 0, got %v", d)
	}
}

func TestParseRetryAfterNilHeaders(t *testing.T) {
	d := parseRetryAfter(nil)
	if d != 0 {
		t.Errorf("expected 0 for nil headers, got %v", d)
	}
}

func TestParseRateLimitInfoAnthropic(t *testing.T) {
	headers := http.Header{
		"Anthropic-Ratelimit-Requests-Remaining": {"10"},
		"Anthropic-Ratelimit-Tokens-Remaining":   {"50000"},
	}
	info := ParseRateLimitInfo(headers, "anthropic-ratelimit")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RemainingRequests != 10 {
		t.Errorf("expected 10 remaining requests, got %d", info.RemainingRequests)
	}
	if info.RemainingTokens != 50000 {
		t.Errorf("expected 50000 remaining tokens, got %d", info.RemainingTokens)
	}
}

func TestParseRateLimitInfoOpenAI(t *testing.T) {
	headers := http.Header{
		"X-Ratelimit-Remaining-Requests": {"20"},
		"X-Ratelimit-Remaining-Tokens":   {"100000"},
	}
	info := ParseRateLimitInfo(headers, "x-ratelimit")
	if info == nil {
		t.Fatal("expected non-nil info")
	}
	if info.RemainingRequests != 20 {
		t.Errorf("expected 20, got %d", info.RemainingRequests)
	}
	if info.RemainingTokens != 100000 {
		t.Errorf("expected 100000, got %d", info.RemainingTokens)
	}
}

func TestParseRateLimitInfoNoPrefix(t *testing.T) {
	info := ParseRateLimitInfo(http.Header{}, "")
	if info != nil {
		t.Error("expected nil for empty prefix")
	}
}
