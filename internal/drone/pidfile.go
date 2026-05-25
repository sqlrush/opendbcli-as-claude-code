/*-------------------------------------------------------------------------
 *
 * pidfile.go
 *	  PID file management for the cluster drone daemon — atomic
 *	  create on start, remove on clean shutdown, stale-PID detection
 *	  so a crashed agent doesn't block the next launch.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/pidfile.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// defaultPIDDir returns the directory for PID files.
func defaultPIDDir() string {
	if dir := os.Getenv("OPENDB_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".opendb"
	}
	return filepath.Join(home, ".opendb")
}

// pidFilePath returns the PID file path for the given role.
func pidFilePath(role string) string {
	return filepath.Join(defaultPIDDir(), fmt.Sprintf("agent-%s.pid", role))
}

// writePID writes the current process PID to the PID file.
func writePID(role string) error {
	path := pidFilePath(role)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create pid dir: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0644)
}

// readPID reads the PID from the PID file. Returns 0 if not found.
func readPID(role string) (int, error) {
	data, err := os.ReadFile(pidFilePath(role))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("invalid pid file content: %w", err)
	}
	return pid, nil
}

// removePID removes the PID file.
func removePID(role string) error {
	return os.Remove(pidFilePath(role))
}

// isProcessRunning checks if a process with the given PID is still running.
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = process.Signal(syscall.Signal(0))
	return err == nil
}
