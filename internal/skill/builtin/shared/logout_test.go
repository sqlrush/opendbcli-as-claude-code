/*-------------------------------------------------------------------------
 *
 * logout_test.go
 *	  Test cases for logout.go (shared package):
 *	  TestLogoutSkill_Execute, TestLogoutSkill_Metadata.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/logout_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/skill"
)

func TestLogoutSkill_Execute(t *testing.T) {
	tests := []struct {
		name       string
		connectTo  string // connect first, then logout
		wantSub    string
	}{
		{
			name:      "logout with no connection",
			connectTo: "",
			wantSub:   "Disconnected",
		},
		{
			name:      "logout from active connection",
			connectTo: "dev-db",
			wantSub:   "Disconnected from dev-db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := newTestManager(t, testConnectionsYAML)

			if tt.connectTo != "" {
				if err := mgr.Connect(context.Background(), tt.connectTo); err != nil {
					t.Fatalf("connecting: %v", err)
				}
			}

			s := NewLogoutSkill(mgr)
			result, err := s.Execute(context.Background(), skill.ParamsFromMap(nil))
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

func TestLogoutSkill_Metadata(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewLogoutSkill(mgr)

	if s.Name() != "logout" {
		t.Errorf("Name() = %q, want %q", s.Name(), "logout")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
