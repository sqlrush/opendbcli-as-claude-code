/*-------------------------------------------------------------------------
 *
 * guard_test.go
 *	  Test cases for guard.go (security package):
 *	  TestGuard_ReadOnly_NoConfirmation,
 *	  TestGuard_Operator_NoConfirmation,
 *	  TestGuard_Admin_RequiresConfirmation.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/guard_test.go
 *
 *-------------------------------------------------------------------------
 */
package security

import (
	"context"
	"testing"

	apperrors "github.com/sqlrush/opendb/internal/errors"
)

func TestGuard_ReadOnly_NoConfirmation(t *testing.T) {
	confirmCalled := false
	guard := NewGuard(LevelAdmin, func(action string) (bool, error) {
		confirmCalled = true
		return true, nil
	})

	err := guard.Authorize(context.Background(), LevelReadOnly, "SELECT * FROM users")
	if err != nil {
		t.Fatalf("expected no error for read-only action, got: %v", err)
	}
	if confirmCalled {
		t.Error("confirmation should not be called for read-only actions")
	}
}

func TestGuard_Operator_NoConfirmation(t *testing.T) {
	confirmCalled := false
	guard := NewGuard(LevelAdmin, func(action string) (bool, error) {
		confirmCalled = true
		return true, nil
	})

	err := guard.Authorize(context.Background(), LevelOperator, "ALTER SESSION SET nls_date_format='YYYY-MM-DD'")
	if err != nil {
		t.Fatalf("expected no error for operator action, got: %v", err)
	}
	if confirmCalled {
		t.Error("confirmation should not be called for operator-level actions")
	}
}

func TestGuard_Admin_RequiresConfirmation(t *testing.T) {
	confirmCalled := false
	guard := NewGuard(LevelAdmin, func(action string) (bool, error) {
		confirmCalled = true
		return true, nil
	})

	err := guard.Authorize(context.Background(), LevelAdmin, "CREATE TABLE test (id NUMBER)")
	if err != nil {
		t.Fatalf("expected no error when confirmation is granted, got: %v", err)
	}
	if !confirmCalled {
		t.Error("confirmation should be called for admin-level actions")
	}
}

func TestGuard_Admin_ConfirmationDenied(t *testing.T) {
	guard := NewGuard(LevelAdmin, func(action string) (bool, error) {
		return false, nil
	})

	err := guard.Authorize(context.Background(), LevelAdmin, "CREATE TABLE test (id NUMBER)")
	if err == nil {
		t.Fatal("expected error when confirmation is denied")
	}
}

func TestGuard_Dangerous_AlwaysConfirm(t *testing.T) {
	confirmCalled := false
	guard := NewGuard(LevelDangerous, func(action string) (bool, error) {
		confirmCalled = true
		return true, nil
	})

	err := guard.Authorize(context.Background(), LevelDangerous, "DROP TABLE users")
	if err != nil {
		t.Fatalf("expected no error when dangerous action is confirmed, got: %v", err)
	}
	if !confirmCalled {
		t.Error("confirmation must always be called for dangerous actions")
	}
}

func TestGuard_Dangerous_ConfirmationDenied(t *testing.T) {
	guard := NewGuard(LevelDangerous, func(action string) (bool, error) {
		return false, nil
	})

	err := guard.Authorize(context.Background(), LevelDangerous, "DROP TABLE users")
	if err == nil {
		t.Fatal("expected error when dangerous action confirmation is denied")
	}
}

func TestGuard_ExceedMaxLevel_Denied(t *testing.T) {
	guard := NewGuard(LevelReadOnly, func(action string) (bool, error) {
		return true, nil
	})

	err := guard.Authorize(context.Background(), LevelAdmin, "CREATE TABLE test (id NUMBER)")
	if err == nil {
		t.Fatal("expected error when action exceeds max level")
	}

	permErr, ok := err.(*apperrors.PermissionDeniedError)
	if !ok {
		t.Fatalf("expected PermissionDeniedError, got: %T", err)
	}
	if permErr.Required != "admin" {
		t.Errorf("expected required level 'admin', got %q", permErr.Required)
	}
	if permErr.Current != "readonly" {
		t.Errorf("expected current level 'readonly', got %q", permErr.Current)
	}
}

func TestGuard_NilConfirmFn_DeniesConfirmation(t *testing.T) {
	guard := NewGuard(LevelDangerous, nil)

	err := guard.Authorize(context.Background(), LevelDangerous, "DROP TABLE users")
	if err == nil {
		t.Fatal("expected error when confirm function is nil")
	}
}

func TestGuard_MaxLevel(t *testing.T) {
	guard := NewGuard(LevelOperator, nil)
	if guard.MaxLevel() != LevelOperator {
		t.Errorf("expected max level %v, got %v", LevelOperator, guard.MaxLevel())
	}
}
