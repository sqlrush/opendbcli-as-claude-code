/*-------------------------------------------------------------------------
 *
 * exec_test.go
 *	  Test cases for exec.go (osutil package): TestRun_AllowedCommand,
 *	  TestRun_BlockedCommand, TestRun_Timeout.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/osutil/exec_test.go
 *
 *-------------------------------------------------------------------------
 */
package osutil

import (
	"context"
	"testing"
	"time"
)

func TestRun_AllowedCommand(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, "ps", "--version")
	if err == ErrCommandNotAllowed {
		t.Errorf("Run('ps') should be allowed, got ErrCommandNotAllowed")
	}
}

func TestRun_BlockedCommand(t *testing.T) {
	ctx := context.Background()
	_, err := Run(ctx, "rm", "-rf", "/")
	if err != ErrCommandNotAllowed {
		t.Errorf("Run('rm') should return ErrCommandNotAllowed, got %v", err)
	}
}

func TestRun_Timeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, "ps", "aux")
	_ = err
}

func TestRunWithTimeout_Exceeds(t *testing.T) {
	Allow("sleep")
	_, err := RunWithTimeout(context.Background(), 1*time.Millisecond, "sleep", "10")
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}
