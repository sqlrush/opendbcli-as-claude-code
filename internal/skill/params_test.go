/*-------------------------------------------------------------------------
 *
 * params_test.go
 *	  Test cases for params.go (skill package): TestParamsFromJSON,
 *	  TestParamsStringOr, TestParamsIntOr.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/params_test.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"testing"
)

func TestParamsFromJSON(t *testing.T) {
	p, err := ParamsFromJSON([]byte(`{"db_type":"oracle","instance":"prod-01","threshold_ms":5000}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.StringOr("db_type", ""); got != "oracle" {
		t.Errorf("db_type = %q, want %q", got, "oracle")
	}
	if got, err := p.Int("threshold_ms"); err != nil || got != 5000 {
		t.Errorf("threshold_ms = %d, err = %v, want 5000", got, err)
	}
}

func TestParamsStringOr(t *testing.T) {
	p, _ := ParamsFromJSON([]byte(`{}`))
	if got := p.StringOr("missing", "default"); got != "default" {
		t.Errorf("got %q, want %q", got, "default")
	}
}

func TestParamsIntOr(t *testing.T) {
	p, _ := ParamsFromJSON([]byte(`{"limit":10}`))
	if got := p.IntOr("limit", 20); got != 10 {
		t.Errorf("got %d, want 10", got)
	}
	if got := p.IntOr("missing", 20); got != 20 {
		t.Errorf("got %d, want 20", got)
	}
}

func TestSecurityLevelString(t *testing.T) {
	tests := []struct {
		level SecurityLevel
		want  string
	}{
		{LevelReadOnly, "readonly"},
		{LevelOperator, "operator"},
		{LevelAdmin, "admin"},
		{LevelDangerous, "dangerous"},
	}
	for _, tt := range tests {
		if got := tt.level.String(); got != tt.want {
			t.Errorf("SecurityLevel(%d).String() = %q, want %q", tt.level, got, tt.want)
		}
	}
}

func TestSecurityLevelRequiresConfirmation(t *testing.T) {
	if LevelReadOnly.RequiresConfirmation() {
		t.Error("LevelReadOnly should not require confirmation")
	}
	if LevelOperator.RequiresConfirmation() {
		t.Error("LevelOperator should not require confirmation by default")
	}
	if !LevelDangerous.RequiresConfirmation() {
		t.Error("LevelDangerous must require confirmation")
	}
}

func TestSecurityLevelCanDisableConfirmation(t *testing.T) {
	if LevelDangerous.CanDisableConfirmation() {
		t.Error("LevelDangerous must NOT allow disabling confirmation")
	}
	if !LevelOperator.CanDisableConfirmation() {
		t.Error("LevelOperator should allow disabling confirmation")
	}
	if !LevelAdmin.CanDisableConfirmation() {
		t.Error("LevelAdmin should allow disabling confirmation")
	}
}
