/*-------------------------------------------------------------------------
 *
 * executor_test.go
 *	  Test cases for executor.go (skill package): TestExecutor_Success,
 *	  TestExecutor_SkillNotFound, TestExecutor_ValidationFails.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/executor_test.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"context"
	"errors"
	"testing"

	apperrors "github.com/sqlrush/opendb/internal/errors"
	"github.com/sqlrush/opendb/internal/security"
)

// mockGuard implements security.Guard for testing.
type mockGuard struct {
	authorizeErr error
	confirmOK    bool
	confirmErr   error
	maxLevel     security.Level
}

func (g *mockGuard) Authorize(_ context.Context, _ security.Level, _ string) error {
	return g.authorizeErr
}

func (g *mockGuard) RequestConfirmation(_ context.Context, _ string) (bool, error) {
	return g.confirmOK, g.confirmErr
}

func (g *mockGuard) MaxLevel() security.Level { return g.maxLevel }

func TestExecutor_Success(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockSkill{
		name:          "sessions",
		secLevel:      LevelReadOnly,
		executeResult: &Result{Type: ResultTable, Summary: "ok"},
	})

	guard := &mockGuard{maxLevel: security.LevelDangerous}
	exec := NewExecutor(reg, guard)

	result, err := exec.Execute(context.Background(), "sessions", ParamsFromMap(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary != "ok" {
		t.Errorf("Summary = %q, want %q", result.Summary, "ok")
	}
}

func TestExecutor_SkillNotFound(t *testing.T) {
	reg := NewRegistry()
	guard := &mockGuard{maxLevel: security.LevelDangerous}
	exec := NewExecutor(reg, guard)

	_, err := exec.Execute(context.Background(), "nonexistent", ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var notFound *apperrors.SkillNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("expected SkillNotFoundError, got %T: %v", err, err)
	}
	if notFound.Name != "nonexistent" {
		t.Errorf("Name = %q, want %q", notFound.Name, "nonexistent")
	}
}

func TestExecutor_ValidationFails(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockSkill{
		name:        "sessions",
		secLevel:    LevelReadOnly,
		validateErr: errors.New("threshold must be positive"),
	})

	guard := &mockGuard{maxLevel: security.LevelDangerous}
	exec := NewExecutor(reg, guard)

	_, err := exec.Execute(context.Background(), "sessions", ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var invalidParams *apperrors.InvalidParamsError
	if !errors.As(err, &invalidParams) {
		t.Errorf("expected InvalidParamsError, got %T: %v", err, err)
	}
	if invalidParams.Skill != "sessions" {
		t.Errorf("Skill = %q, want %q", invalidParams.Skill, "sessions")
	}
}

func TestExecutor_PermissionDenied(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockSkill{
		name:     "killsession",
		secLevel: LevelDangerous,
	})

	guard := &mockGuard{
		authorizeErr: &apperrors.PermissionDeniedError{
			Required: "dangerous",
			Current:  "readonly",
			Action:   "killsession",
		},
		maxLevel: security.LevelReadOnly,
	}
	exec := NewExecutor(reg, guard)

	_, err := exec.Execute(context.Background(), "killsession", ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var permDenied *apperrors.PermissionDeniedError
	if !errors.As(err, &permDenied) {
		t.Errorf("expected PermissionDeniedError, got %T: %v", err, err)
	}
}

func TestExecutor_SecurityConfirmation(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&mockSkill{
		name:          "alterindex",
		secLevel:      LevelAdmin,
		executeResult: &Result{Type: ResultText, Summary: "index altered"},
	})

	// Guard denies because user did not confirm.
	guard := &mockGuard{
		authorizeErr: errors.New("action \"alterindex\" was not confirmed by user"),
		maxLevel:     security.LevelAdmin,
	}
	exec := NewExecutor(reg, guard)

	_, err := exec.Execute(context.Background(), "alterindex", ParamsFromMap(nil))
	if err == nil {
		t.Fatal("expected error when confirmation is denied")
	}

	// The error should wrap the guard's denial, but NOT be a PermissionDeniedError
	// since it's a confirmation rejection rather than a level mismatch.
	var permDenied *apperrors.PermissionDeniedError
	if errors.As(err, &permDenied) {
		t.Error("expected non-PermissionDeniedError for confirmation denial")
	}
}
