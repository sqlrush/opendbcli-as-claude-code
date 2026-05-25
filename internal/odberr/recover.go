/*-------------------------------------------------------------------------
 *
 * recover.go
 *	  RecoverFatal is deferred at the main entry. If a panic propagates
 *	  all the way up, it logs the crash, prints a user-visible message,
 *	  and exits with status 1. Never returns on panic.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/odberr/recover.go
 *
 *-------------------------------------------------------------------------
 */
package odberr

import (
	"fmt"
	"os"
	"runtime/debug"
	"time"
)

// RecoverFatal is deferred at the main entry. If a panic propagates all
// the way up, it logs the crash, prints a user-visible message, and
// exits with status 1. Never returns on panic.
//
// Usage:
//
//	func main() {
//	    defer odberr.RecoverFatal(odberr.ErrCoreMainPanic)
//	    ...
//	}
func RecoverFatal(fallbackCode string) {
	r := recover()
	if r == nil {
		return
	}
	e := fromRecover(r, fallbackCode, SeverityFatal)
	WriteCrash(e)
	Increment(e.Code)
	fmt.Fprintln(os.Stderr, e.Display())
	fmt.Fprintf(os.Stderr, "  crash log: %s\n", CrashLogPath())
	os.Exit(1)
}

// Guard runs fn with panic protection. If fn panics, the panic is
// captured as *Error, logged to crash.log, counted, and returned.
// If fn completes normally, returns nil.
//
// Use inside REPL select cases or any synchronous boundary where the
// caller wants to display a friendly message and keep the loop alive.
func Guard(fallbackCode string, fn func()) (err *Error) {
	defer func() {
		if r := recover(); r != nil {
			err = fromRecover(r, fallbackCode, SeverityError)
			WriteCrash(err)
			Increment(err.Code)
		}
	}()
	fn()
	return nil
}

// SafeGo launches fn in a new goroutine with panic protection.
// Replaces `go func(){ ... }()` throughout the codebase.
//
// On panic, the error is logged and counted; nothing is surfaced to the
// UI (goroutines have no return path). Use Guard if you need the error.
func SafeGo(fallbackCode string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				e := fromRecover(r, fallbackCode, SeverityError)
				WriteCrash(e)
				Increment(e.Code)
			}
		}()
		fn()
	}()
}

// fromRecover converts a recover() value into *Error, preserving the
// stack trace via debug.Stack().
func fromRecover(r any, fallbackCode string, sev Severity) *Error {
	stack := string(debug.Stack())

	// If the panicked value is already *Error, respect its code/message
	// but attach the fresh stack.
	if existing, ok := r.(*Error); ok && existing != nil {
		return existing.WithStack(stack)
	}

	// If it's a regular error, wrap it.
	if cause, ok := r.(error); ok {
		entry, known := Lookup(fallbackCode)
		msg := entry.Title
		if !known {
			msg = "未注册的错误"
		}
		return (&Error{
			Code:     fallbackCode,
			Severity: sev,
			Message:  msg,
			Cause:    cause,
			Stack:    stack,
		}).withTime()
	}

	// Anything else (string, int, struct…): stringify.
	entry, known := Lookup(fallbackCode)
	msg := entry.Title
	if !known {
		msg = "未注册的错误"
	}
	return (&Error{
		Code:     fallbackCode,
		Severity: sev,
		Message:  fmt.Sprintf("%s: %v", msg, r),
		Stack:    stack,
	}).withTime()
}

// withTime stamps Time if zero — internal helper used by recovery paths.
func (e *Error) withTime() *Error {
	if e == nil {
		return nil
	}
	if e.Time.IsZero() {
		cp := *e
		cp.Time = time.Now()
		return &cp
	}
	return e
}
