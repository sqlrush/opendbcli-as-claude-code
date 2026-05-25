/*-------------------------------------------------------------------------
 *
 * login_test.go
 *	  Test cases for login.go (shared package):
 *	  TestLoginSkill_Execute_Connect,
 *	  TestLoginSkill_Execute_UnknownConnection,
 *	  TestLoginSkill_Execute_NoArgs_ReturnsPicker.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/login_test.go
 *
 *-------------------------------------------------------------------------
 */
package shared

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sqlrush/opendb/internal/config"
	"github.com/sqlrush/opendb/internal/connection"
	"github.com/sqlrush/opendb/internal/db"
	"github.com/sqlrush/opendb/internal/db/mock"
	"github.com/sqlrush/opendb/internal/skill"
)

// writeConnectionFile creates a YAML connection file in the given directory.
func writeConnectionFile(t *testing.T, dir string, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("creating dir: %v", err)
	}
	path := filepath.Join(dir, "test.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing connection file: %v", err)
	}
}

func newTestManager(t *testing.T, connYAML string) *connection.Manager {
	t.Helper()
	connDir := filepath.Join(t.TempDir(), "connections")
	if connYAML != "" {
		writeConnectionFile(t, connDir, connYAML)
	} else {
		if err := os.MkdirAll(connDir, 0755); err != nil {
			t.Fatalf("creating dir: %v", err)
		}
	}

	cfg := &config.Config{ConnectionsDir: connDir}
	factory := func(cfg db.ConnectionConfig) (db.Driver, error) {
		return mock.NewMockDriver(), nil
	}
	mgr, err := connection.NewManager(cfg, connection.WithDriverFactory(factory))
	if err != nil {
		t.Fatalf("creating manager: %v", err)
	}
	return mgr
}

const testConnectionsYAML = `group: test
connections:
  - name: dev-db
    host: dev.example.com
    port: 1521
    service: DEVDB
    user: app_user
    credential:
      value: "secret"
  - name: prod-db
    host: prod.example.com
    port: 1521
    service: PRODDB
    user: admin
    credential:
      value: "topsecret"
`

func TestLoginSkill_Execute_Connect(t *testing.T) {
	mgr := newTestManager(t, testConnectionsYAML)
	s := NewLoginSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "dev-db"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Type != skill.ResultText {
		t.Errorf("want ResultText, got %v", result.Type)
	}
	if !strings.Contains(result.Rendered, "Connected to dev-db") {
		t.Errorf("Rendered = %q, want 'Connected to dev-db'", result.Rendered)
	}
}

func TestLoginSkill_Execute_UnknownConnection(t *testing.T) {
	mgr := newTestManager(t, testConnectionsYAML)
	s := NewLoginSkill(mgr)

	_, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{"args": "nonexistent"}))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoginSkill_Execute_NoArgs_ReturnsPicker(t *testing.T) {
	mgr := newTestManager(t, testConnectionsYAML)
	s := NewLoginSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Metadata == nil || result.Metadata["action"] != PickerAction {
		t.Errorf("expected picker action, got metadata=%v", result.Metadata)
	}
}

func TestLoginSkill_Execute_NoArgs_EmptyConnections(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewLoginSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Even with no connections, returns picker signal (picker will show empty list).
	if result.Metadata == nil || result.Metadata["action"] != PickerAction {
		t.Errorf("expected picker action, got metadata=%v", result.Metadata)
	}
}

func TestLoginSkill_DynamicArgCompletions(t *testing.T) {
	mgr := newTestManager(t, testConnectionsYAML)
	s := NewLoginSkill(mgr)

	completions := s.CLIDef().ArgCompletions
	if len(completions) != 2 {
		t.Fatalf("expected 2 completions, got %d: %v", len(completions), completions)
	}

	// Should contain both connection names.
	found := map[string]bool{}
	for _, c := range completions {
		found[c] = true
	}
	if !found["dev-db"] || !found["prod-db"] {
		t.Errorf("completions should contain dev-db and prod-db, got %v", completions)
	}
}

func TestLoginSkill_RecentConnectionsFirst(t *testing.T) {
	mgr := newTestManager(t, testConnectionsYAML)

	// Connect to prod-db to make it recent.
	if err := mgr.Connect(context.Background(), "prod-db"); err != nil {
		t.Fatalf("connecting: %v", err)
	}
	if err := mgr.Disconnect(); err != nil {
		t.Fatalf("disconnecting: %v", err)
	}

	s := NewLoginSkill(mgr)
	completions := s.CLIDef().ArgCompletions

	if len(completions) < 1 {
		t.Fatal("expected at least 1 completion")
	}
	if completions[0] != "prod-db" {
		t.Errorf("first completion = %q, want prod-db (most recent)", completions[0])
	}
}

func TestLoginSkill_Metadata(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewLoginSkill(mgr)

	if s.Name() != "login" {
		t.Errorf("Name() = %q, want %q", s.Name(), "login")
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}
	if err := s.Validate(skill.ParamsFromMap(nil)); err != nil {
		t.Errorf("Validate() = %v, want nil", err)
	}
}
