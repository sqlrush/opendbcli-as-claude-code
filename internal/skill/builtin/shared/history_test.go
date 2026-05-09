/*-------------------------------------------------------------------------
 *
 * history_test.go
 *	  Test cases for history.go (shared package):
 *	  TestHistorySkill_Execute, TestHistorySkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/history_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/session"
	"github.com/sqlrush/opendb/internal/skill"
)

func TestHistorySkill_Execute(t *testing.T) {
	tests := []struct {
		name     string
		commands []string
		wantSub  string
	}{
		{
			name:     "empty history",
			commands: nil,
			wantSub:  "No history entries",
		},
		{
			name:     "single entry",
			commands: []string{"SELECT 1 FROM dual"},
			wantSub:  "SELECT 1 FROM dual",
		},
		{
			name:     "multiple entries",
			commands: []string{"cmd1", "cmd2", "cmd3"},
			wantSub:  "cmd3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hist := session.NewSessionHistory(t.TempDir(), "test-instance")
			for _, cmd := range tt.commands {
				hist.Add(cmd)
			}

			h := NewHistorySkill(hist)
			result, err := h.Execute(context.Background(), skill.ParamsFromMap(nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.Type != skill.ResultText {
				t.Errorf("want ResultText, got %v", result.Type)
			}
			data, ok := result.Data.(string)
			if !ok {
				t.Fatalf("Data is not a string: %T", result.Data)
			}
			if !strings.Contains(data, tt.wantSub) {
				t.Errorf("output %q does not contain %q", data, tt.wantSub)
			}
		})
	}
}

func TestHistorySkill_Metadata(t *testing.T) {
	hist := session.NewSessionHistory(t.TempDir(), "test")
	h := NewHistorySkill(hist)

	if h.Name() != "history" {
		t.Errorf("Name() = %q, want %q", h.Name(), "history")
	}
	if h.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", h.SecurityLevel())
	}
	if err := h.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
