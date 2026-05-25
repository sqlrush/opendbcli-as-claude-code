/*-------------------------------------------------------------------------
 *
 * guard.go
 *	  Guard defines the interface for authorization checks.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/security/guard.go
 *
 *-------------------------------------------------------------------------
 */
package security

import (
	"context"
	"fmt"

	apperrors "github.com/sqlrush/opendb/internal/errors"
)

// Guard defines the interface for authorization checks.
type Guard interface {
	// Authorize checks if the given action at the specified level is allowed.
	// Returns nil if authorized, or an error if denied or confirmation is rejected.
	Authorize(ctx context.Context, level Level, action string) error

	// RequestConfirmation prompts the user to confirm a potentially dangerous action.
	// Returns true if confirmed, false if rejected.
	RequestConfirmation(ctx context.Context, action string) (bool, error)

	// MaxLevel returns the maximum security level this guard allows.
	MaxLevel() Level
}

// ConfirmFunc is a function type for requesting user confirmation.
// It receives a description of the action and returns whether the user confirmed.
type ConfirmFunc func(action string) (bool, error)

// DefaultGuard implements Guard with configurable max level and confirmation.
type DefaultGuard struct {
	maxLevel  Level
	confirmFn ConfirmFunc
}

// NewGuard creates a new DefaultGuard with the given maximum level and confirmation function.
// If confirmFn is nil, all confirmation requests will be denied.
func NewGuard(maxLevel Level, confirmFn ConfirmFunc) *DefaultGuard {
	return &DefaultGuard{
		maxLevel:  maxLevel,
		confirmFn: confirmFn,
	}
}

// Authorize checks if the action at the given level is permitted.
// LevelDangerous always requires confirmation regardless of configuration.
// Actions exceeding maxLevel are denied outright.
func (g *DefaultGuard) Authorize(ctx context.Context, level Level, action string) error {
	if level > g.maxLevel {
		return &apperrors.PermissionDeniedError{
			Required: level.String(),
			Current:  g.maxLevel.String(),
			Action:   action,
		}
	}

	if level == LevelDangerous {
		confirmed, err := g.RequestConfirmation(ctx, action)
		if err != nil {
			return fmt.Errorf("confirmation failed for action %q: %w", action, err)
		}
		if !confirmed {
			return fmt.Errorf("action %q was not confirmed by user", action)
		}
	} else if level.RequiresConfirmation() && level.CanDisableConfirmation() {
		// LevelAdmin requires confirmation by default,
		// but confirmation can be skipped based on guard configuration.
		// For now, always request confirmation for admin-level actions.
		confirmed, err := g.RequestConfirmation(ctx, action)
		if err != nil {
			return fmt.Errorf("confirmation failed for action %q: %w", action, err)
		}
		if !confirmed {
			return fmt.Errorf("action %q was not confirmed by user", action)
		}
	}

	return nil
}

// RequestConfirmation delegates to the configured confirmation function.
// Returns false if no confirmation function is configured.
func (g *DefaultGuard) RequestConfirmation(_ context.Context, action string) (bool, error) {
	if g.confirmFn == nil {
		return false, nil
	}
	return g.confirmFn(action)
}

// MaxLevel returns the maximum security level this guard allows.
func (g *DefaultGuard) MaxLevel() Level {
	return g.maxLevel
}
