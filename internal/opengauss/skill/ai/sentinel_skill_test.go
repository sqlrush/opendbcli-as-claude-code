/*-------------------------------------------------------------------------
 *
 * sentinel_skill_test.go
 *	  Test cases for sentinel_skill.go (ai package):
 *	  TestSentinelSkill_Metadata, TestSentinelSkill_Validate,
 *	  TestSentinelSkill_StatusWhenNotRunning.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/opengauss/skill/ai/sentinel_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// p builds skill.Params from a flat key/value list for test terseness.
func p(kv ...any) skill.Params {
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv)-1; i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return skill.ParamsFromMap(m)
}

func emptyParams() skill.Params {
	return skill.ParamsFromMap(map[string]any{})
}

func TestSentinelSkill_Metadata(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSentinelSkill(drv, config.SentinelConfig{})

	if got := s.Name(); got != "sentinel" {
		t.Errorf("Name() = %q, want %q", got, "sentinel")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if s.Description() == "" {
		t.Error("Description() should be non-empty")
	}

	td := s.ToolDef()
	if td.Name != "sentinel" {
		t.Errorf("ToolDef().Name = %q, want %q", td.Name, "sentinel")
	}
	if td.Description == "" {
		t.Error("ToolDef().Description should be non-empty")
	}

	cd := s.CLIDef()
	if cd.Command != "sentinel" {
		t.Errorf("CLIDef().Command = %q, want %q", cd.Command, "sentinel")
	}
	if len(cd.Examples) == 0 {
		t.Error("CLIDef().Examples should be non-empty")
	}
}

func TestSentinelSkill_Validate(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSentinelSkill(drv, config.SentinelConfig{})

	tests := []struct {
		name    string
		action  string
		wantErr bool
	}{
		{"start ok", "start", false},
		{"stop ok", "stop", false},
		{"status ok", "status", false},
		{"empty ok", "", false},
		{"unknown action errors", "bogus", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Validate(p("action", tt.action))
			if tt.wantErr && err == nil {
				t.Errorf("Validate(%q) = nil, want error", tt.action)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Validate(%q) = %v, want nil", tt.action, err)
			}
		})
	}
}

func TestSentinelSkill_StatusWhenNotRunning(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSentinelSkill(drv, config.SentinelConfig{})

	res, err := s.Execute(context.Background(), emptyParams())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("Execute returned nil result")
	}
	if !strings.Contains(res.Rendered, "未运行") {
		t.Errorf("default status should report not-running, got: %q", res.Rendered)
	}
	if s.IsRunning() {
		t.Error("IsRunning() = true before start")
	}
}

func TestSentinelSkill_LastReportNil(t *testing.T) {
	drv := mock.NewMockDriver()
	s := NewSentinelSkill(drv, config.SentinelConfig{})
	if s.LastReport() != nil {
		t.Error("LastReport() should be nil initially")
	}
	if s.ReportCount() != 0 {
		t.Errorf("ReportCount() = %d, want 0", s.ReportCount())
	}
	if s.Reports() != nil {
		t.Error("Reports() should be nil initially")
	}
	if s.AlertCh() != nil {
		t.Error("AlertCh() should be nil when sentinel not running")
	}
}
