/*-------------------------------------------------------------------------
 *
 * pidfile_test.go
 *	  Test cases for pidfile.go (drone package): TestWriteAndReadPID,
 *	  TestIsProcessRunning.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/pidfile_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAndReadPID(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("OPENDB_HOME", tmpDir)

	if err := writePID("worker"); err != nil {
		t.Fatalf("writePID: %v", err)
	}

	pid, err := readPID("worker")
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("pid = %d, want %d", pid, os.Getpid())
	}

	path := filepath.Join(tmpDir, "agent-worker.pid")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("PID file does not exist")
	}

	if err := removePID("worker"); err != nil {
		t.Fatalf("removePID: %v", err)
	}
	pid, err = readPID("worker")
	if err != nil {
		t.Fatalf("readPID after remove: %v", err)
	}
	if pid != 0 {
		t.Errorf("pid after remove = %d, want 0", pid)
	}
}

func TestIsProcessRunning(t *testing.T) {
	if !isProcessRunning(os.Getpid()) {
		t.Error("current process should be running")
	}
	if isProcessRunning(0) {
		t.Error("PID 0 should not be running")
	}
}
