/*-------------------------------------------------------------------------
 *
 * trace_test.go
 *	  Test cases for trace.go (monitor package):
 *	  TestOGTraceSkill_Interface, TestOGTraceSkill_Validate,
 *	  TestOGTraceSkill_RefusesRemoteHost.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/monitor/trace_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestOGTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.Description() == "" {
		t.Errorf("Description() is empty")
	}
	if s.ToolDef().Name != "trace" {
		t.Errorf("ToolDef().Name = %q, want 'trace'", s.ToolDef().Name)
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestOGTraceSkill_Validate(t *testing.T) {
	s := NewTraceSkill("localhost", nil)

	// Default (no args) should pass — internal default is 3 seconds.
	if err := s.Validate(skill.ParamsFromMap(map[string]any{})); err != nil {
		t.Errorf("Validate({}) unexpected error: %v", err)
	}
	// Valid range: 1..10.
	for _, dur := range []int{1, 5, 10} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"duration": dur})); err != nil {
			t.Errorf("Validate(duration=%d) unexpected error: %v", dur, err)
		}
	}
	// Out-of-range must reject: perf over 10s starves the host and under 1s
	// produces too few samples to be meaningful.
	for _, dur := range []int{0, -1, 11, 30} {
		if err := s.Validate(skill.ParamsFromMap(map[string]any{"duration": dur})); err == nil {
			t.Errorf("expected error for duration=%d", dur)
		}
	}
}

// Execute on a non-loopback host must refuse without attempting perf — the
// skill assumes OpenDB runs on the DB host. This is the single most
// important safety invariant; if it regresses the skill will hang in perf
// trying to profile the wrong machine.
func TestOGTraceSkill_RefusesRemoteHost(t *testing.T) {
	s := NewTraceSkill("10.0.0.5", nil)
	res, err := s.Execute(nil, skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Type != skill.ResultText {
		t.Errorf("expected ResultText (soft error), got %v", res.Type)
	}
	if res.Summary != "trace unavailable" {
		t.Errorf("expected summary 'trace unavailable', got %q", res.Summary)
	}
}
