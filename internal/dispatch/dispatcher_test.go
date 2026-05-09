/*-------------------------------------------------------------------------
 *
 * dispatcher_test.go
 *	  Test cases for dispatcher.go (dispatch package):
 *	  TestParseSlashCommand, TestDispatch_SlashCommand,
 *	  TestDispatch_SlashCommand_NotFound.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/dispatch/dispatcher_test.go
 *
 *-------------------------------------------------------------------------
 */
package dispatch

import (
	"context"
	"testing"

	apperrors "github.com/sqlrush/opendb/internal/errors"
	"github.com/sqlrush/opendb/internal/security"
	"github.com/sqlrush/opendb/internal/skill"
)

// --- mock skill for dispatcher tests ---

type stubSkill struct {
	name   string
	result *skill.Result
	err    error
}

func (s *stubSkill) Name() string                  { return s.name }
func (s *stubSkill) Description() string            { return s.name + " stub" }
func (s *stubSkill) ToolDef() skill.ToolDef         { return skill.ToolDef{Name: s.name} }
func (s *stubSkill) CLIDef() skill.CLIDef           { return skill.CLIDef{Command: s.name} }
func (s *stubSkill) Validate(_ skill.Params) error  { return nil }
func (s *stubSkill) SecurityLevel() skill.SecurityLevel { return skill.LevelReadOnly }

func (s *stubSkill) Execute(_ context.Context, _ skill.Params) (*skill.Result, error) {
	return s.result, s.err
}

// --- helpers ---

func newTestDispatcher(skills ...skill.Skill) *Dispatcher {
	registry := skill.NewRegistry()
	for _, s := range skills {
		registry.Register(s)
	}
	guard := security.NewGuard(security.LevelDangerous, nil)
	executor := skill.NewExecutor(registry, guard)
	return NewDispatcher(registry, executor)
}

// --- ParseSlashCommand tests ---

func TestParseSlashCommand(t *testing.T) {
	tests := []struct {
		input    string
		wantName string
		wantArgs string
	}{
		{"/help", "help", ""},
		{"/slowsql 5000", "slowsql", "5000"},
		{"/explain SELECT * FROM t", "explain", "SELECT * FROM t"},
		{"  /help  ", "help", ""},
		{"/cmd   multiple   args", "cmd", "multiple   args"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			name, args := ParseSlashCommand(tt.input)
			if name != tt.wantName {
				t.Errorf("ParseSlashCommand(%q) name = %q, want %q", tt.input, name, tt.wantName)
			}
			if args != tt.wantArgs {
				t.Errorf("ParseSlashCommand(%q) args = %q, want %q", tt.input, args, tt.wantArgs)
			}
		})
	}
}

// --- Dispatch tests ---

func TestDispatch_SlashCommand(t *testing.T) {
	helpResult := &skill.Result{
		Type:    skill.ResultText,
		Data:    "Available commands: /help",
		Summary: "Help information",
	}
	helpSkill := &stubSkill{
		name:   "help",
		result: helpResult,
	}

	d := newTestDispatcher(helpSkill)
	result, err := d.Dispatch(context.Background(), "/help")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.Summary != "Help information" {
		t.Errorf("result.Summary = %q, want %q", result.Summary, "Help information")
	}
}

func TestDispatch_SlashCommand_NotFound(t *testing.T) {
	d := newTestDispatcher()
	_, err := d.Dispatch(context.Background(), "/nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}

	var snfErr *apperrors.SkillNotFoundError
	if !isSkillNotFoundError(err, &snfErr) {
		t.Errorf("expected SkillNotFoundError, got %T: %v", err, err)
	}
}

func TestDispatch_Empty(t *testing.T) {
	d := newTestDispatcher()

	result, err := d.Dispatch(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for empty input")
	}

	result, err = d.Dispatch(context.Background(), "   ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for whitespace input")
	}
}

func TestDispatch_NaturalLanguage_RoutesToDiag(t *testing.T) {
	diagResult := &skill.Result{
		Type:    skill.ResultText,
		Summary: "on-demand",
	}
	diagSkill := &stubSkill{
		name:   "llm",
		result: diagResult,
	}
	d := newTestDispatcher(diagSkill)
	result, err := d.Dispatch(context.Background(), "what are the slow queries")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || result.Summary != "on-demand" {
		t.Errorf("expected diag result, got %v", result)
	}
}

func TestDispatch_NaturalLanguage_NoDiagSkill(t *testing.T) {
	d := newTestDispatcher()
	_, err := d.Dispatch(context.Background(), "what are the slow queries")
	if err == nil {
		t.Fatal("expected error when diag skill not registered")
	}
	var snf *apperrors.SkillNotFoundError
	if !isSkillNotFoundError(err, &snf) {
		t.Errorf("expected SkillNotFoundError, got %T: %v", err, err)
	}
}

func TestDispatch_SQL_NoSQLSkill(t *testing.T) {
	d := newTestDispatcher()
	_, err := d.Dispatch(context.Background(), "SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for SQL without sql skill registered")
	}
	// Should get SkillNotFoundError since "sql" skill is not registered in test dispatcher
	var snf *apperrors.SkillNotFoundError
	if !isSkillNotFoundError(err, &snf) {
		t.Errorf("expected SkillNotFoundError, got %T: %v", err, err)
	}
}

// isSkillNotFoundError checks if err is a *SkillNotFoundError using type assertion.
func isSkillNotFoundError(err error, target **apperrors.SkillNotFoundError) bool {
	e, ok := err.(*apperrors.SkillNotFoundError)
	if ok && target != nil {
		*target = e
	}
	return ok
}
