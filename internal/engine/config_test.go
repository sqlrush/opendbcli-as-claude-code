/*-------------------------------------------------------------------------
 *
 * config_test.go
 *	  Test cases for config.go (engine package): TestDefaultConfig,
 *	  TestDiagnoseModeIsValid, TestEngineResultToolsInvoked.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/config_test.go
 *
 *-------------------------------------------------------------------------
 */
package engine

import "testing"

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.DefaultMaxTurns != 20 {
		t.Errorf("expected DefaultMaxTurns 20, got %d", cfg.DefaultMaxTurns)
	}
	if cfg.DefaultMaxTokens != 80000 {
		t.Errorf("expected DefaultMaxTokens 80000, got %d", cfg.DefaultMaxTokens)
	}
	if !cfg.EnableCompression {
		t.Error("expected EnableCompression true")
	}
	if cfg.MaxOutputRecoveries != 2 {
		t.Errorf("expected MaxOutputRecoveries 2, got %d", cfg.MaxOutputRecoveries)
	}
}

func TestDiagnoseModeIsValid(t *testing.T) {
	tests := []struct {
		mode  DiagnoseMode
		valid bool
	}{
		{ModePlaybook, true},
		{ModeAssist, true},
		{ModeAuto, true},
		{DiagnoseMode("unknown"), false},
		{DiagnoseMode(""), false},
	}
	for _, tt := range tests {
		if got := tt.mode.IsValid(); got != tt.valid {
			t.Errorf("DiagnoseMode(%q).IsValid() = %v, want %v", tt.mode, got, tt.valid)
		}
	}
}

func TestEngineResultToolsInvoked(t *testing.T) {
	r := EngineResult{
		Content:      "diagnosis...",
		TurnsUsed:    3,
		ToolsInvoked: []string{"waits", "topsql", "explain"},
	}
	if len(r.ToolsInvoked) != 3 {
		t.Errorf("expected 3 tools, got %d", len(r.ToolsInvoked))
	}
}
