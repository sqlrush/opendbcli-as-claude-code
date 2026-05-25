/*-------------------------------------------------------------------------
 *
 * types_test.go
 *	  Test cases for types.go (sentinel package):
 *	  TestRootCauseType_String, TestRootCauseType_IsValid,
 *	  TestDefaultConfig.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/sentinel/types_test.go
 *
 *-------------------------------------------------------------------------
 */
package sentinel

import (
	"testing"
	"time"
)

func TestRootCauseType_String(t *testing.T) {
	tests := []struct {
		cause RootCauseType
		want  string
	}{
		{CauseBadSQL, "SQL并发冲高"},
		{CauseIOSubsystem, "存储I/O冲高"},
		{CauseLatchStorm, "Latch争用冲高"},
		{CauseRedoBottleneck, "Redo冲高"},
		{CauseLockContention, "锁等待阻塞"},
		{CauseTrafficStorm, "流量冲高"},
		{CauseUnknown, "未知"},
		{RootCauseType("invalid"), "未知"},
	}
	for _, tt := range tests {
		if got := tt.cause.String(); got != tt.want {
			t.Errorf("RootCauseType(%q).String() = %q, want %q", tt.cause, got, tt.want)
		}
	}
}

func TestRootCauseType_IsValid(t *testing.T) {
	validCauses := []RootCauseType{
		CauseBadSQL, CauseIOSubsystem, CauseLatchStorm,
		CauseRedoBottleneck, CauseLockContention, CauseTrafficStorm,
		CauseUnknown,
	}
	for _, c := range validCauses {
		if !c.IsValid() {
			t.Errorf("RootCauseType(%q).IsValid() = false, want true", c)
		}
	}

	invalidCauses := []RootCauseType{"", "foo", "bad sql"}
	for _, c := range invalidCauses {
		if c.IsValid() {
			t.Errorf("RootCauseType(%q).IsValid() = true, want false", c)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollInterval != time.Second {
		t.Errorf("PollInterval = %v, want 1s", cfg.PollInterval)
	}
	if cfg.BaselineWindow != 60 {
		t.Errorf("BaselineWindow = %d, want 60", cfg.BaselineWindow)
	}
	if cfg.MinSamples != 10 {
		t.Errorf("MinSamples = %d, want 10", cfg.MinSamples)
	}
	if cfg.SigmaThreshold != 3.0 {
		t.Errorf("SigmaThreshold = %f, want 3.0", cfg.SigmaThreshold)
	}
	if cfg.BurstInterval != 200*time.Millisecond {
		t.Errorf("BurstInterval = %v, want 200ms", cfg.BurstInterval)
	}
	if cfg.BurstDuration != 30*time.Second {
		t.Errorf("BurstDuration = %v, want 30s", cfg.BurstDuration)
	}
	if cfg.BurstCalmDelay != 5*time.Second {
		t.Errorf("BurstCalmDelay = %v, want 5s", cfg.BurstCalmDelay)
	}
	if cfg.CooldownPeriod != 5*time.Minute {
		t.Errorf("CooldownPeriod = %v, want 5m", cfg.CooldownPeriod)
	}
}

func TestSentinelSample_ZeroValue(t *testing.T) {
	var s SentinelSample
	if s.ActiveNonIdle != 0 || s.OnCPU != 0 || !s.Timestamp.IsZero() {
		t.Error("zero SentinelSample should have zero values")
	}
}

func TestClassification_ZeroValue(t *testing.T) {
	var c Classification
	if c.Cause != "" || c.Confidence != 0 || len(c.Evidence) != 0 {
		t.Error("zero Classification should have empty cause and zero confidence")
	}
}
