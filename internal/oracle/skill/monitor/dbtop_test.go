/*-------------------------------------------------------------------------
 *
 * dbtop_test.go
 *	  Test cases for dbtop.go (monitor package):
 *	  TestDBTopSkill_Metadata, TestDBTopSkill_Execute_DefaultInterval,
 *	  TestDBTopSkill_Execute_CustomInterval.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/oracle/skill/monitor/dbtop_test.go
 *
 *-------------------------------------------------------------------------
 */
package monitor

import (
	"context"
	"testing"

	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestDBTopSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	if s.Name() != "dbtop" {
		t.Errorf("Name() = %q, want %q", s.Name(), "dbtop")
	}
	if s.Description() != "Real-time database performance monitor" {
		t.Errorf("Description() = %q, want %q", s.Description(), "Real-time database performance monitor")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.ToolDef().Name != "dbtop" {
		t.Errorf("ToolDef().Name = %q, want %q", s.ToolDef().Name, "dbtop")
	}
	if s.CLIDef().Command != "dbtop" {
		t.Errorf("CLIDef().Command = %q, want %q", s.CLIDef().Command, "dbtop")
	}
	if s.CLIDef().Usage != "/dbtop [interval_seconds]" {
		t.Errorf("CLIDef().Usage = %q, want %q", s.CLIDef().Usage, "/dbtop [interval_seconds]")
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}

func TestDBTopSkill_Execute_DefaultInterval(t *testing.T) {
	drv := mock.NewMockDriver()
	drv.ServerInfoVal = db.ServerInfo{
		Version:      "19.3.0",
		InstanceName: "ORCL",
	}

	s := NewDBTopSkill(drv)
	result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Type != skill.ResultRefresh {
		t.Errorf("Type = %v, want ResultRefresh", result.Type)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	if cfg.Driver != drv {
		t.Error("Driver should be the same instance passed to the skill")
	}
	if cfg.IntervalSec != 1 {
		t.Errorf("IntervalSec = %d, want 1", cfg.IntervalSec)
	}
}

func TestDBTopSkill_Execute_CustomInterval(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	params := skill.ParamsFromMap(map[string]any{"args": "5"})
	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	if cfg.IntervalSec != 5 {
		t.Errorf("IntervalSec = %d, want 5", cfg.IntervalSec)
	}
}

func TestDBTopSkill_Execute_IntervalWithWhitespace(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	params := skill.ParamsFromMap(map[string]any{"args": "  3  "})
	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	if cfg.IntervalSec != 3 {
		t.Errorf("IntervalSec = %d, want 3", cfg.IntervalSec)
	}
}

func TestDBTopSkill_Execute_InvalidInterval(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	params := skill.ParamsFromMap(map[string]any{"args": "abc"})
	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	// Invalid input falls back to default interval.
	if cfg.IntervalSec != 1 {
		t.Errorf("IntervalSec = %d, want 1 (default)", cfg.IntervalSec)
	}
}

func TestDBTopSkill_Execute_ZeroInterval(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	params := skill.ParamsFromMap(map[string]any{"args": "0"})
	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	// Zero is not > 0, so falls back to default.
	if cfg.IntervalSec != 1 {
		t.Errorf("IntervalSec = %d, want 1 (default)", cfg.IntervalSec)
	}
}

func TestDBTopSkill_Execute_NegativeInterval(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewDBTopSkill(drv)

	params := skill.ParamsFromMap(map[string]any{"args": "-2"})
	result, err := s.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cfg, ok := result.Data.(*DbtopRefreshConfig)
	if !ok {
		t.Fatalf("Data type = %T, want *DbtopRefreshConfig", result.Data)
	}

	// Negative is not > 0, so falls back to default.
	if cfg.IntervalSec != 1 {
		t.Errorf("IntervalSec = %d, want 1 (default)", cfg.IntervalSec)
	}
}
