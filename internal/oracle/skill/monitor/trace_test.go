/*-------------------------------------------------------------------------
 *
 * trace_test.go
 *	  Test cases for trace.go (monitor package):
 *	  TestOracleTraceSkill_Interface, TestOracleTraceSkill_Validate,
 *	  TestOracleTraceSkill_Description.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/trace_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestOracleTraceSkill_Interface(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	if s.Name() != "trace" {
		t.Errorf("Name() = %q, want 'trace'", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.CLIDef().Command != "trace" {
		t.Errorf("CLIDef().Command = %q, want 'trace'", s.CLIDef().Command)
	}
}

func TestOracleTraceSkill_Validate(t *testing.T) {
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

func TestOracleTraceSkill_Description(t *testing.T) {
	s := NewTraceSkill("localhost", nil)
	desc := s.Description()
	if desc != "OS 堆栈采集 + 火焰图分析 (Oracle)" {
		t.Errorf("Description() = %q, unexpected", desc)
	}
}
