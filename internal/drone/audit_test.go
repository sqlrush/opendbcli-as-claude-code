/*-------------------------------------------------------------------------
 *
 * audit_test.go
 *	  Test cases for audit.go (drone package): TestAuditLogger,
 *	  TestAuditLoggerAutoKey, TestAuditLoggerResumesChain.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/audit_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLogger(t *testing.T) {
	tmpDir := t.TempDir()
	key := []byte("test-key-for-audit-logger-000001")
	logger, err := NewAuditLoggerWithKey(tmpDir, key)
	if err != nil {
		t.Fatalf("NewAuditLoggerWithKey: %v", err)
	}
	defer logger.Close()

	if err := logger.Log("worker", "Oracle-A-037", "KILL SESSION '472,38291'", "TEMP 93%, LLM diagnosis", "OK"); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if err := logger.Log("worker", "Oracle-A-037", "CREATE INDEX idx_order_date", "same issue", "OK"); err != nil {
		t.Fatalf("Log: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "audit.log"))
	if err != nil {
		t.Fatalf("read audit.log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "KILL SESSION") {
		t.Errorf("line 0 missing KILL SESSION: %s", lines[0])
	}
	if !strings.Contains(lines[1], "CREATE INDEX") {
		t.Errorf("line 1 missing CREATE INDEX: %s", lines[1])
	}
	// Verify hash suffix exists on each line.
	for i, line := range lines {
		if !strings.Contains(line, " | hash:") {
			t.Errorf("line %d missing hash suffix: %s", i, line)
		}
	}
}

func TestAuditLoggerAutoKey(t *testing.T) {
	tmpDir := t.TempDir()
	logger, err := NewAuditLogger(tmpDir)
	if err != nil {
		t.Fatalf("NewAuditLogger: %v", err)
	}
	defer logger.Close()

	// Key file should exist with 0600 permissions.
	keyPath := filepath.Join(tmpDir, "audit.key")
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat audit.key: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("audit.key perm = %o, want 0600", perm)
	}

	if err := logger.Log("worker", "db1", "SELECT 1", "test", "OK"); err != nil {
		t.Fatalf("Log: %v", err)
	}
}

func TestAuditLoggerResumesChain(t *testing.T) {
	tmpDir := t.TempDir()
	key := []byte("test-key-for-resume-chain-00001")

	// Write two entries, then close.
	logger1, err := NewAuditLoggerWithKey(tmpDir, key)
	if err != nil {
		t.Fatalf("NewAuditLoggerWithKey (1): %v", err)
	}
	if err := logger1.Log("w", "db", "op1", "r1", "OK"); err != nil {
		t.Fatal(err)
	}
	if err := logger1.Log("w", "db", "op2", "r2", "OK"); err != nil {
		t.Fatal(err)
	}
	logger1.Close()

	// Re-open and write a third entry.
	logger2, err := NewAuditLoggerWithKey(tmpDir, key)
	if err != nil {
		t.Fatalf("NewAuditLoggerWithKey (2): %v", err)
	}
	if err := logger2.Log("w", "db", "op3", "r3", "OK"); err != nil {
		t.Fatal(err)
	}
	logger2.Close()

	// Verify entire chain.
	logPath := filepath.Join(tmpDir, auditLogFile)
	keyPath := filepath.Join(tmpDir, auditKeyFile)
	// Write the key so VerifyAuditLog can read it.
	writeTestKeyFile(t, keyPath, key)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("expected valid chain after resume, got tampered lines: %v", result.TamperedLines)
	}
	if result.TotalEntries != 3 {
		t.Errorf("TotalEntries = %d, want 3", result.TotalEntries)
	}
}

func writeTestKeyFile(t *testing.T, path string, key []byte) {
	t.Helper()
	encoded := make([]byte, len(key)*2)
	for i, b := range key {
		const hextable = "0123456789abcdef"
		encoded[i*2] = hextable[b>>4]
		encoded[i*2+1] = hextable[b&0x0f]
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0600); err != nil {
		t.Fatalf("write test key file: %v", err)
	}
}
