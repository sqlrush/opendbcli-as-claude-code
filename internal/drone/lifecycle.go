/*-------------------------------------------------------------------------
 *
 * lifecycle.go
 *	  lifecycle.go exports PID management functions for use by
 *	  cmd/opendb/agent_mode.go.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/lifecycle.go
 *
 *-------------------------------------------------------------------------
 */
// lifecycle.go exports PID management functions for use by cmd/opendb/agent_mode.go.
package drone

import "fmt"

// CheckAndWritePID checks if an agent is already running, and writes PID file if not.
// ISSUE-007: Cleans up stale PID files from crashed processes with explicit logging.
func CheckAndWritePID(role string) error {
	pid, err := readPID(role)
	if err != nil {
		return fmt.Errorf("check existing agent: %w", err)
	}
	if pid > 0 {
		if isProcessRunning(pid) {
			return fmt.Errorf("agent already running (PID %d). Use 'opendb agent stop' first", pid)
		}
		// ISSUE-007: Stale PID file from crashed process — clean it up.
		fmt.Printf("Cleaning up stale PID file (previous PID %d no longer running)\n", pid)
		removePID(role)
	}
	if err := writePID(role); err != nil {
		return fmt.Errorf("write PID file: %w", err)
	}
	return nil
}

// RemovePIDFile removes the PID file for the given role.
func RemovePIDFile(role string) {
	removePID(role)
}

// CleanStalePID checks if a stale PID file exists for the role and removes it.
// Returns the stale PID if cleaned, 0 if no stale PID found.
func CleanStalePID(role string) int {
	pid, err := readPID(role)
	if err != nil || pid == 0 {
		return 0
	}
	if !isProcessRunning(pid) {
		removePID(role)
		return pid
	}
	return 0
}
