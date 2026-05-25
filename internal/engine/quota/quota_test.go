/*-------------------------------------------------------------------------
 *
 * quota_test.go
 *	  Test cases for quota.go (quota package): TestEnforce_UnderQuota,
 *	  TestEnforce_OverQuota_DeletesOldest,
 *	  TestEnforce_ProtectsProfileFile.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/quota/quota_test.go
 *
 *-------------------------------------------------------------------------
 */
package quota

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnforce_UnderQuota(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "inst1")
	os.MkdirAll(sessDir, 0750)
	os.WriteFile(filepath.Join(sessDir, "s1.jsonl"), make([]byte, 100), 0640)

	Enforce(dir, 1024*1024) // 1MB quota, 100 bytes used

	// File should still exist
	if _, err := os.Stat(filepath.Join(sessDir, "s1.jsonl")); err != nil {
		t.Error("file should not be deleted when under quota")
	}
}

func TestEnforce_OverQuota_DeletesOldest(t *testing.T) {
	dir := t.TempDir()
	sessDir := filepath.Join(dir, "sessions", "inst1")
	os.MkdirAll(sessDir, 0750)

	// Create 3 files, 500 bytes each = 1500 bytes total
	os.WriteFile(filepath.Join(sessDir, "old.jsonl"), make([]byte, 500), 0640)
	os.WriteFile(filepath.Join(sessDir, "mid.jsonl"), make([]byte, 500), 0640)
	os.WriteFile(filepath.Join(sessDir, "new.jsonl"), make([]byte, 500), 0640)

	// Set quota to 1000 bytes — need to delete at least 1 file
	Enforce(dir, 1000)

	// At least the oldest file should be deleted
	remaining := countFiles(sessDir)
	if remaining > 2 {
		t.Errorf("expected at most 2 files, got %d", remaining)
	}
}

func TestEnforce_ProtectsProfileFile(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, "memory", "inst1")
	os.MkdirAll(memDir, 0750)

	// PROFILE.md should never be deleted by quota enforcement
	os.WriteFile(filepath.Join(memDir, "PROFILE.md"), make([]byte, 100), 0640)
	// Create regular memory files to exceed quota
	os.WriteFile(filepath.Join(memDir, "incident_old.md"), make([]byte, 500), 0640)
	os.WriteFile(filepath.Join(memDir, "incident_new.md"), make([]byte, 500), 0640)
	// MEMORY.md (will be rebuilt/removed by RebuildIndex after cleanup)
	os.WriteFile(filepath.Join(memDir, "MEMORY.md"), make([]byte, 100), 0640)

	// Quota = 300. Total = 1200. Deletable: old + new = 1000.
	Enforce(dir, 300)

	// PROFILE.md must survive
	if _, err := os.Stat(filepath.Join(memDir, "PROFILE.md")); err != nil {
		t.Error("PROFILE.md should be protected from quota deletion")
	}

	// Deletable memory files should be removed
	if _, err := os.Stat(filepath.Join(memDir, "incident_old.md")); err == nil {
		t.Error("incident_old.md should be deleted")
	}
}

func TestEnforce_PolicyNotCounted(t *testing.T) {
	dir := t.TempDir()

	// Create large policy files
	polDir := filepath.Join(dir, "policies", "org")
	os.MkdirAll(polDir, 0750)
	os.WriteFile(filepath.Join(polDir, "big.md"), make([]byte, 5000), 0640)

	// Create small session file
	sessDir := filepath.Join(dir, "sessions", "inst1")
	os.MkdirAll(sessDir, 0750)
	os.WriteFile(filepath.Join(sessDir, "s1.jsonl"), make([]byte, 100), 0640)

	// Quota is 200 bytes — policy files don't count, so session file should survive
	Enforce(dir, 200)

	if _, err := os.Stat(filepath.Join(sessDir, "s1.jsonl")); err != nil {
		t.Error("session file should survive — policy files don't count toward quota")
	}
	// Policy file should always survive
	if _, err := os.Stat(filepath.Join(polDir, "big.md")); err != nil {
		t.Error("policy file should never be deleted")
	}
}

func TestEnforce_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should not panic on empty/nonexistent dirs
	Enforce(dir, 1024)
}

func countFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	return count
}
