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
 *	  internal/oracle/skill/ai/sentinel_skill_test.go
 *
 *-------------------------------------------------------------------------
 */
package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/oracle/monitor/dbtop"
	"github.com/sqlrush/opendb/internal/skill"
)

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

func testCollector() *dbtop.Collector {
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{Version: "19.3.0", InstanceName: "ORCL"}
	md.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{}, nil
	}
	return dbtop.NewCollector(md)
}

func TestSentinelSkill_Metadata(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})

	if s.Name() != "sentinel" {
		t.Errorf("Name() = %q, want sentinel", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	td := s.ToolDef()
	if td.Name != "sentinel" {
		t.Errorf("ToolDef().Name = %q, want sentinel", td.Name)
	}

	cd := s.CLIDef()
	if cd.Command != "sentinel" {
		t.Errorf("CLIDef().Command = %q, want sentinel", cd.Command)
	}
}

func TestSentinelSkill_Validate(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})

	tests := []struct {
		action string
		valid  bool
	}{
		{"start", true},
		{"stop", true},
		{"status", true},
		{"", true},
		{"invalid", false},
	}

	for _, tt := range tests {
		err := s.Validate(p("action", tt.action))
		if tt.valid && err != nil {
			t.Errorf("Validate(%q) = %v, want nil", tt.action, err)
		}
		if !tt.valid && err == nil {
			t.Errorf("Validate(%q) = nil, want error", tt.action)
		}
	}
}

func TestSentinelSkill_StatusWhenNotRunning(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	result, err := s.Execute(context.Background(), emptyParams())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Rendered
	if !strings.Contains(text, "未运行") {
		t.Errorf("status should say not running, got %q", text)
	}
}

func TestSentinelSkill_StartAndStop(t *testing.T) {
	collector := testCollector()
	md := mock.NewMockDriver()
	md.ServerInfoVal = db.ServerInfo{Version: "19.3.0", InstanceName: "ORCL"}
	md.QueryFunc = func(_ context.Context, _ string, _ ...any) (*db.QueryResult, error) {
		return &db.QueryResult{}, nil
	}

	s := NewSentinelSkill(md, collector, config.SentinelConfig{})
	ctx := context.Background()

	// Start
	result, err := s.Execute(ctx, p("action", "start"))
	if err != nil {
		t.Fatalf("start error: %v", err)
	}
	text := result.Rendered
	if !strings.Contains(text, "已启动") {
		t.Errorf("start should confirm, got %q", text)
	}
	if !s.IsRunning() {
		t.Error("should be running after start")
	}

	// Double start
	result, err = s.Execute(ctx, p("action", "start"))
	if err != nil {
		t.Fatalf("double start error: %v", err)
	}
	text = result.Rendered
	if !strings.Contains(text, "已在运行") {
		t.Errorf("double start should say already running, got %q", text)
	}

	// Status while running
	result, err = s.Execute(ctx, p("action", "status"))
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	text = result.Rendered
	if !strings.Contains(text, "Sentinel") {
		t.Errorf("status should contain Sentinel header, got %q", text)
	}

	// Stop
	result, err = s.Execute(ctx, p("action", "stop"))
	if err != nil {
		t.Fatalf("stop error: %v", err)
	}
	text = result.Rendered
	if !strings.Contains(text, "已停止") {
		t.Errorf("stop should confirm, got %q", text)
	}
	if s.IsRunning() {
		t.Error("should not be running after stop")
	}

	// Double stop
	result, err = s.Execute(ctx, p("action", "stop"))
	if err != nil {
		t.Fatalf("double stop error: %v", err)
	}
	text = result.Rendered
	if !strings.Contains(text, "未在运行") {
		t.Errorf("double stop should say not running, got %q", text)
	}
}

func TestSentinelSkill_DefaultAction(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	result, err := s.Execute(context.Background(), emptyParams())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := result.Rendered
	if !strings.Contains(text, "未运行") {
		t.Errorf("default action should show status, got %q", text)
	}
}

func TestSentinelSkill_ArgsAction(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	result, err := s.Execute(context.Background(), p("args", "status"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != skill.ResultText {
		t.Errorf("type = %v, want ResultText", result.Type)
	}
}

func TestSentinelSkill_LastReport(t *testing.T) {
	s := NewSentinelSkill(nil, nil, config.SentinelConfig{})
	if s.LastReport() != nil {
		t.Error("LastReport should be nil initially")
	}
}
