/*-------------------------------------------------------------------------
 *
 * errors.go
 *	  NotConnectedError indicates no active database connection.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/errors/errors.go
 *
 *-------------------------------------------------------------------------
 */
package errors

import "fmt"

// NotConnectedError indicates no active database connection.
type NotConnectedError struct {
	Message string
}

func (e *NotConnectedError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "not connected to any database instance"
}

// PermissionDeniedError indicates insufficient security level.
type PermissionDeniedError struct {
	Required string
	Current  string
	Action   string
}

func (e *PermissionDeniedError) Error() string {
	return fmt.Sprintf("permission denied: action %q requires %s level, current level is %s",
		e.Action, e.Required, e.Current)
}

// QueryTimeoutError indicates a query exceeded its context deadline.
type QueryTimeoutError struct {
	SQL     string
	Timeout string
}

func (e *QueryTimeoutError) Error() string {
	return fmt.Sprintf("query timed out after %s", e.Timeout)
}

// SkillNotFoundError indicates an unknown skill name.
type SkillNotFoundError struct {
	Name string
}

func (e *SkillNotFoundError) Error() string {
	return fmt.Sprintf("unknown skill: %q", e.Name)
}

// InvalidParamsError indicates skill parameter validation failure.
type InvalidParamsError struct {
	Skill   string
	Message string
}

func (e *InvalidParamsError) Error() string {
	return fmt.Sprintf("invalid params for skill %q: %s", e.Skill, e.Message)
}
