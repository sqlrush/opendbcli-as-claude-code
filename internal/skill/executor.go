/*-------------------------------------------------------------------------
 *
 * executor.go
 *	  Executor looks up skills in a Registry, validates parameters,
 *	  checks authorization via a security.Guard, and then runs the
 *	  skill.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/skill/executor.go
 *
 *-------------------------------------------------------------------------
 */
package skill

import (
	"context"
	"fmt"
	"strings"

	apperrors "github.com/sqlrush/opendb/internal/errors"
	"github.com/sqlrush/opendb/internal/security"
)

// Executor looks up skills in a Registry, validates parameters, checks
// authorization via a security.Guard, and then runs the skill.
type Executor struct {
	registry *Registry
	guard    security.Guard
}

// NewExecutor creates an Executor backed by the given registry and guard.
func NewExecutor(registry *Registry, guard security.Guard) *Executor {
	return &Executor{
		registry: registry,
		guard:    guard,
	}
}

// Execute runs the named skill with the supplied params.
//
// It returns:
//   - *apperrors.SkillNotFoundError  when the skill does not exist
//   - *apperrors.InvalidParamsError   when Validate fails
//   - *apperrors.PermissionDeniedError when the guard denies authorization
//   - any error propagated from the skill's own Execute method
func (e *Executor) Execute(ctx context.Context, name string, params Params) (*Result, error) {
	// 1. Look up skill.
	s, ok := e.registry.Get(name)
	if !ok {
		return nil, &apperrors.SkillNotFoundError{Name: name}
	}

	// 2. Validate params — append usage hint from CLIDef on failure.
	if err := s.Validate(params); err != nil {
		msg := err.Error()
		if usage := formatUsageHint(s); usage != "" {
			msg = msg + "\n\n" + usage
		}
		return nil, &apperrors.InvalidParamsError{
			Skill:   name,
			Message: msg,
		}
	}

	// 3. Check security.
	if err := e.guard.Authorize(ctx, s.SecurityLevel(), name); err != nil {
		// If the guard already returned a PermissionDeniedError, pass it through.
		if _, ok := err.(*apperrors.PermissionDeniedError); ok {
			return nil, err
		}
		// Wrap other authorization errors (e.g. confirmation denied) as permission denied.
		return nil, fmt.Errorf("authorization failed for skill %q: %w", name, err)
	}

	// 4. Execute the skill.
	return s.Execute(ctx, params)
}

// formatUsageHint builds a brief usage hint from CLIDef for display after
// validation errors. Returns empty string if no useful info is available.
func formatUsageHint(s Skill) string {
	def := s.CLIDef()
	if def.Usage == "" && len(def.Examples) == 0 {
		return ""
	}
	var parts []string
	if def.Usage != "" {
		parts = append(parts, "  用法: "+def.Usage)
	}
	if len(def.Examples) > 0 {
		parts = append(parts, "  示例:")
		for _, ex := range def.Examples {
			parts = append(parts, "    "+ex)
		}
	}
	return strings.Join(parts, "\n")
}
