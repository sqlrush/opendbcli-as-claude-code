/*-------------------------------------------------------------------------
 *
 * exec.go
 *	  exec — Exposes Allow, Run, and RunWithTimeout for the osutil
 *	  package.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/osutil/exec.go
 *
 *-------------------------------------------------------------------------
 */
package osutil

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

var ErrCommandNotAllowed = errors.New("command not allowed")

var allowedCmds = map[string]bool{
	"perf":   true,
	"ps":     true,
	"pstack": true,
	"git":    true,
}

func Allow(name string) {
	allowedCmds[name] = true
}

func Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if !allowedCmds[name] {
		return nil, ErrCommandNotAllowed
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func RunWithTimeout(parent context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	out, err := Run(ctx, name, args...)
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command %q timed out after %v: %w", name, timeout, ctx.Err())
	}
	return out, err
}
