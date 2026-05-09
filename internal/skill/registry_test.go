/*-------------------------------------------------------------------------
 *
 * registry_test.go
 *	  Test cases for registry.go (skill package):
 *	  TestRegistryRegisterAndGet, TestRegistryGet_NotFound,
 *	  TestRegistryMatch_Prefix.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/registry_test.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"context"
	"testing"
)

// --- mock helpers (shared with executor_test.go) ---

type mockSkill struct {
	name          string
	secLevel      SecurityLevel
	validateErr   error
	executeResult *Result
	executeErr    error
}

func (m *mockSkill) Name() string               { return m.name }
func (m *mockSkill) Description() string         { return m.name + " description" }
func (m *mockSkill) ToolDef() ToolDef            { return ToolDef{Name: m.name} }
func (m *mockSkill) CLIDef() CLIDef              { return CLIDef{Command: m.name} }
func (m *mockSkill) Validate(_ Params) error     { return m.validateErr }
func (m *mockSkill) SecurityLevel() SecurityLevel { return m.secLevel }

func (m *mockSkill) Execute(_ context.Context, _ Params) (*Result, error) {
	return m.executeResult, m.executeErr
}

// --- Registry tests ---

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()
	s := &mockSkill{name: "sessions"}
	r.Register(s)

	got, ok := r.Get("sessions")
	if !ok {
		t.Fatal("expected skill to be found")
	}
	if got.Name() != "sessions" {
		t.Errorf("Name() = %q, want %q", got.Name(), "sessions")
	}
}

func TestRegistryGet_NotFound(t *testing.T) {
	r := NewRegistry()

	_, ok := r.Get("nonexistent")
	if ok {
		t.Error("expected ok=false for missing skill")
	}
}

func TestRegistryMatch_Prefix(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "slowsql"})
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "sloth"})

	matched := r.Match("sl")
	if len(matched) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matched))
	}
	// Sorted order: sloth, slowsql
	if matched[0].Name() != "sloth" {
		t.Errorf("matched[0] = %q, want %q", matched[0].Name(), "sloth")
	}
	if matched[1].Name() != "slowsql" {
		t.Errorf("matched[1] = %q, want %q", matched[1].Name(), "slowsql")
	}
}

func TestRegistryMatch_SingleChar(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "slowsql"})
	r.Register(&mockSkill{name: "awr"})

	matched := r.Match("s")
	if len(matched) != 2 {
		t.Fatalf("expected 2 matches, got %d", len(matched))
	}
}

func TestRegistryMatch_Empty(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "slowsql"})
	r.Register(&mockSkill{name: "awr"})

	matched := r.Match("")
	if len(matched) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matched))
	}
}

func TestRegistryMatch_NoMatch(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "slowsql"})

	matched := r.Match("xyz")
	if len(matched) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(matched))
	}
}

func TestRegistryMatch_CaseInsensitive(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "slowsql"})

	matched := r.Match("SL")
	if len(matched) != 1 {
		t.Fatalf("expected 1 match, got %d", len(matched))
	}
	if matched[0].Name() != "slowsql" {
		t.Errorf("matched[0] = %q, want %q", matched[0].Name(), "slowsql")
	}
}

func TestRegistryAll(t *testing.T) {
	r := NewRegistry()
	r.Register(&mockSkill{name: "sessions"})
	r.Register(&mockSkill{name: "awr"})
	r.Register(&mockSkill{name: "slowsql"})

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(all))
	}
	// Should be sorted by name.
	expected := []string{"awr", "sessions", "slowsql"}
	for i, want := range expected {
		if all[i].Name() != want {
			t.Errorf("all[%d].Name() = %q, want %q", i, all[i].Name(), want)
		}
	}
}
