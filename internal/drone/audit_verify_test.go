/*-------------------------------------------------------------------------
 *
 * audit_verify_test.go
 *	  Test cases for audit_verify.go (drone package):
 *	  TestVerifyValidLog, TestVerifyModifiedEntry,
 *	  TestVerifyDeletedEntry.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/audit_verify_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKey is a fixed key for deterministic tests.
var testKey = []byte("verify-test-key-32bytes-00000001")

func setupVerifyTest(t *testing.T, entries int) (logPath, keyPath string) {
	t.Helper()
	tmpDir := t.TempDir()
	logPath = filepath.Join(tmpDir, auditLogFile)
	keyPath = filepath.Join(tmpDir, auditKeyFile)

	writeTestKeyFile(t, keyPath, testKey)

	logger, err := NewAuditLoggerWithKey(tmpDir, testKey)
	if err != nil {
		t.Fatalf("NewAuditLoggerWithKey: %v", err)
	}
	for i := 0; i < entries; i++ {
		if err := logger.Log("worker", "db", "op"+string(rune('A'+i)), "reason", "OK"); err != nil {
			t.Fatalf("Log entry %d: %v", i, err)
		}
	}
	logger.Close()
	return logPath, keyPath
}

func TestVerifyValidLog(t *testing.T) {
	logPath, keyPath := setupVerifyTest(t, 5)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("expected valid, got tampered lines: %v", result.TamperedLines)
	}
	if result.TotalEntries != 5 {
		t.Errorf("TotalEntries = %d, want 5", result.TotalEntries)
	}
	if result.ValidEntries != 5 {
		t.Errorf("ValidEntries = %d, want 5", result.ValidEntries)
	}
	if result.FirstTampered != -1 {
		t.Errorf("FirstTampered = %d, want -1", result.FirstTampered)
	}
}

func TestVerifyModifiedEntry(t *testing.T) {
	logPath, keyPath := setupVerifyTest(t, 3)

	lines := readLines(t, logPath)
	// Tamper with the second line (change "opB" to "opX").
	lines[1] = strings.Replace(lines[1], "opB", "opX", 1)
	writeLines(t, logPath, lines)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if result.IsValid() {
		t.Fatal("expected tampered, got valid")
	}
	// Line 2 is tampered; line 3 also breaks because prevHash chain is broken.
	if result.FirstTampered != 2 {
		t.Errorf("FirstTampered = %d, want 2", result.FirstTampered)
	}
	if len(result.TamperedLines) < 1 {
		t.Errorf("expected at least 1 tampered line, got %d", len(result.TamperedLines))
	}
}

func TestVerifyDeletedEntry(t *testing.T) {
	logPath, keyPath := setupVerifyTest(t, 4)

	lines := readLines(t, logPath)
	// Delete the second line.
	truncated := append(lines[:1], lines[2:]...)
	writeLines(t, logPath, truncated)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if result.IsValid() {
		t.Fatal("expected tampered after deletion, got valid")
	}
	// After deleting line 2, the old line 3 (now line 2) should fail.
	if result.FirstTampered != 2 {
		t.Errorf("FirstTampered = %d, want 2", result.FirstTampered)
	}
}

func TestVerifyInsertedEntry(t *testing.T) {
	logPath, keyPath := setupVerifyTest(t, 3)

	lines := readLines(t, logPath)
	// Insert a fake line between line 1 and line 2.
	fakeHash := hex.EncodeToString(make([]byte, 32))
	fake := "2026-04-11T00:00:00Z | hacker | db | DROP TABLE | reason: evil | result: OK | hash:" + fakeHash
	inserted := make([]string, 0, len(lines)+1)
	inserted = append(inserted, lines[0])
	inserted = append(inserted, fake)
	inserted = append(inserted, lines[1:]...)
	writeLines(t, logPath, inserted)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if result.IsValid() {
		t.Fatal("expected tampered after insertion, got valid")
	}
	// The inserted fake line should fail.
	if result.FirstTampered != 2 {
		t.Errorf("FirstTampered = %d, want 2", result.FirstTampered)
	}
}

func TestVerifyEmptyLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, auditLogFile)
	keyPath := filepath.Join(tmpDir, auditKeyFile)
	writeTestKeyFile(t, keyPath, testKey)

	// Empty file.
	if err := os.WriteFile(logPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("expected valid for empty log, got tampered")
	}
	if result.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", result.TotalEntries)
	}
}

func TestVerifyNonExistentLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "does_not_exist.log")
	keyPath := filepath.Join(tmpDir, auditKeyFile)
	writeTestKeyFile(t, keyPath, testKey)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("expected valid for non-existent log")
	}
	if result.TotalEntries != 0 {
		t.Errorf("TotalEntries = %d, want 0", result.TotalEntries)
	}
}

func TestVerifyGenesisEntry(t *testing.T) {
	logPath, keyPath := setupVerifyTest(t, 1)

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("genesis entry should be valid, got tampered: %v", result.TamperedLines)
	}
	if result.TotalEntries != 1 {
		t.Errorf("TotalEntries = %d, want 1", result.TotalEntries)
	}
	if result.ValidEntries != 1 {
		t.Errorf("ValidEntries = %d, want 1", result.ValidEntries)
	}
}

func TestVerifyLegacyLinesSkipped(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, auditLogFile)
	keyPath := filepath.Join(tmpDir, auditKeyFile)
	writeTestKeyFile(t, keyPath, testKey)

	// Write legacy lines (no hash), then hashed entries.
	legacy1 := "2026-01-01T00:00:00Z | worker | db | SELECT 1 | reason: test | result: OK"
	legacy2 := "2026-01-01T00:01:00Z | worker | db | SELECT 2 | reason: test | result: OK"

	// Write legacy lines first.
	if err := os.WriteFile(logPath, []byte(legacy1+"\n"+legacy2+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Then append hashed entries.
	logger, err := NewAuditLoggerWithKey(tmpDir, testKey)
	if err != nil {
		t.Fatalf("NewAuditLoggerWithKey: %v", err)
	}
	if err := logger.Log("worker", "db", "op1", "r1", "OK"); err != nil {
		t.Fatal(err)
	}
	if err := logger.Log("worker", "db", "op2", "r2", "OK"); err != nil {
		t.Fatal(err)
	}
	logger.Close()

	result, err := VerifyAuditLog(logPath, keyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("expected valid with legacy lines skipped, tampered: %v", result.TamperedLines)
	}
	if result.SkippedLines != 2 {
		t.Errorf("SkippedLines = %d, want 2", result.SkippedLines)
	}
	if result.TotalEntries != 2 {
		t.Errorf("TotalEntries = %d, want 2 (hashed entries only)", result.TotalEntries)
	}
}

func TestVerifyWrongKey(t *testing.T) {
	logPath, _ := setupVerifyTest(t, 3)

	// Use a different key for verification.
	tmpDir := t.TempDir()
	wrongKeyPath := filepath.Join(tmpDir, auditKeyFile)
	wrongKey := []byte("wrong-key-32bytes-for-testing!!!")
	writeTestKeyFile(t, wrongKeyPath, wrongKey)

	result, err := VerifyAuditLog(logPath, wrongKeyPath)
	if err != nil {
		t.Fatalf("VerifyAuditLog: %v", err)
	}
	if result.IsValid() {
		t.Fatal("expected all tampered with wrong key, got valid")
	}
	if len(result.TamperedLines) != 3 {
		t.Errorf("TamperedLines count = %d, want 3", len(result.TamperedLines))
	}
}

// readLines reads all non-empty lines from a file.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw := strings.Split(strings.TrimSpace(string(data)), "\n")
	var lines []string
	for _, l := range raw {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// writeLines writes lines back to a file.
func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
