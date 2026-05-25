/*-------------------------------------------------------------------------
 *
 * error.go
 *	  Package odberr provides OpenDB's structured error code system.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/error.go
 *
 *-------------------------------------------------------------------------
 */
// Package odberr provides OpenDB's structured error code system.
//
// Every runtime panic or high-frequency error is identified by an
// ERR-XXYYYY code (XX = module, YYYY = sequence). Codes are registered
// in a central registry with human-readable titles and advice.
//
// Three entry points exist for protecting code paths:
//   - RecoverFatal(code)  — deferred at main; writes crash log and exits.
//   - Guard(code, fn)     — runs fn, returns *Error on panic; crash logged.
//   - SafeGo(code, fn)    — replaces `go func(){}()`; panic → crash log.
package odberr

import (
	"fmt"
	"time"
)

// Severity categorizes an error for user-facing routing.
type Severity uint8

const (
	// SeverityWarn — degraded but continues.
	SeverityWarn Severity = iota
	// SeverityError — function failed but process survives.
	SeverityError
	// SeverityFatal — process cannot continue; caller exits.
	SeverityFatal
)

// String returns the severity label used in logs and display.
func (s Severity) String() string {
	switch s {
	case SeverityWarn:
		return "WARN"
	case SeverityError:
		return "ERROR"
	case SeverityFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// Error is OpenDB's standard coded error.
//
// Fields are immutable after construction; mutating methods return a new value.
type Error struct {
	Code     string    // "ERR-030001"
	Severity Severity  // WARN / ERROR / FATAL
	Message  string    // human-readable description
	Cause    error     // underlying cause (may be nil)
	Stack    string    // captured stack trace (panic recovery only)
	Time     time.Time // when the Error was constructed
}

// New builds an Error with the given code and message.
// If code is unregistered, it is still carried verbatim (the registry's
// Unknown entry supplies defaults at display time).
func New(code, message string) *Error {
	return &Error{
		Code:     code,
		Severity: severityOf(code),
		Message:  message,
		Time:     time.Now(),
	}
}

// Wrap builds an Error carrying an underlying cause.
// If cause is nil, Message is used alone.
func Wrap(code string, cause error, message string) *Error {
	return &Error{
		Code:     code,
		Severity: severityOf(code),
		Message:  message,
		Cause:    cause,
		Time:     time.Now(),
	}
}

// WithStack returns a copy of e with the stack field set.
// Used by Guard/SafeGo/RecoverFatal when capturing a panic.
func (e *Error) WithStack(stack string) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Stack = stack
	return &cp
}

// WithSeverity returns a copy of e with Severity overridden.
func (e *Error) WithSeverity(s Severity) *Error {
	if e == nil {
		return nil
	}
	cp := *e
	cp.Severity = s
	return &cp
}

// Error satisfies the error interface.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap exposes the Cause for errors.Is/errors.As.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Display returns a compact one-line string for UI rendering.
// Example: "⚠ [ERR-030001] 渲染异常(已恢复): slice out of range"
func (e *Error) Display() string {
	if e == nil {
		return ""
	}
	icon := icon(e.Severity)
	if e.Cause != nil {
		return fmt.Sprintf("%s [%s] %s: %v", icon, e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s [%s] %s", icon, e.Code, e.Message)
}

func icon(s Severity) string {
	switch s {
	case SeverityFatal:
		return "✗"
	case SeverityError:
		return "⚠"
	case SeverityWarn:
		return "•"
	default:
		return "•"
	}
}

// severityOf resolves severity from the registry; falls back to Error.
func severityOf(code string) Severity {
	if entry, ok := Lookup(code); ok {
		return entry.Severity
	}
	return SeverityError
}
