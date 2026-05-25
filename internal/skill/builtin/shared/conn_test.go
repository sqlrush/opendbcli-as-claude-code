/*-------------------------------------------------------------------------
 *
 * conn_test.go
 *	  Test cases for conn.go (shared package): TestConnSkill_Metadata,
 *	  TestConnSkill_NoArgs_PointsToConfigure,
 *	  TestConnSkill_ConnectWithCredentials.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/builtin/shared/conn_test.go
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

func TestConnSkill_Metadata(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewConnSkill(mgr)

	if s.Name() != "conn" {
		t.Errorf("Name() = %q, want conn", s.Name())
	}
	if s.SecurityLevel() != skill.LevelReadOnly {
		t.Errorf("SecurityLevel() = %v, want LevelReadOnly", s.SecurityLevel())
	}

	cd := s.CLIDef()
	if cd.Command != "conn" {
		t.Errorf("CLIDef().Command = %q, want conn", cd.Command)
	}
}

func TestConnSkill_NoArgs_PointsToConfigure(t *testing.T) {
	// In-REPL conn wizard removed — /conn no-args now prints status + a
	// pointer to `<binary> configure` for adding/editing connections.
	mgr := newTestManager(t, "")
	s := NewConnSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Metadata != nil && result.Metadata["action"] != "" {
		t.Errorf("expected no metadata action (wizard removed), got: %v", result.Metadata)
	}
	if !strings.Contains(result.Rendered, "configure") {
		t.Errorf("expected output to mention `configure`, got: %q", result.Rendered)
	}
}

func TestConnSkill_ConnectWithCredentials(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewConnSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"args": "admin/secret@10.0.1.1:1521/orcl",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "Connected to") {
		t.Errorf("Rendered = %q, want 'Connected to ...'", result.Rendered)
	}
}

func TestConnSkill_InvalidConnString(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewConnSkill(mgr)

	_, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"args": "invalid",
	}))
	if err == nil {
		t.Fatal("expected error for invalid connection string")
	}
}

func TestConnSkill_OSSysdba(t *testing.T) {
	mgr := newTestManager(t, "")
	s := NewConnSkill(mgr)

	result, err := s.Execute(context.Background(), skill.ParamsFromMap(map[string]any{
		"args": "/@127.0.0.1:1521/orcl as sysdba",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Rendered, "Connected to") {
		t.Errorf("Rendered = %q, want 'Connected to ...'", result.Rendered)
	}
}
