/*-------------------------------------------------------------------------
 *
 * trace_test.go
 *	  Test cases for trace.go (monitor package):
 *	  TestPGTraceSkill_Interface, TestPGTraceSkill_Validate.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/postgres/skill/monitor/trace_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestPGTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
}

func TestPGTraceSkill_Validate(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	err := s.Validate(skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
	err = s.Validate(skill.ParamsFromMap(map[string]any{"duration": 30}))
	if err == nil {
		t.Error("expected error for duration=30")
	}
}
