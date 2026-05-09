/*-------------------------------------------------------------------------
 *
 * recovery_test.go
 *	  Test cases for recovery.go (drone package):
 *	  TestScanRecentMemories_Empty, TestScanRecentMemories_WithFiles,
 *	  TestScanRecentMemories_NoDir.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/drone/recovery_test.go
 *
 *-------------------------------------------------------------------------
 */
package drone

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanRecentMemories_Empty(t *testing.T) {
	tmpDir := t.TempDir()
	instance := "test-db"
	memDir := filepath.Join(tmpDir, "memory", instance)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	got := scanRecentMemories(tmpDir, instance, 1*time.Hour)
	if len(got) != 0 {
		t.Errorf("expected 0 memories, got %d", len(got))
	}
}

func TestScanRecentMemories_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()
	instance := "test-db"
	memDir := filepath.Join(tmpDir, "memory", instance)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// recent files (within 1 hour)
	recentFiles := []string{"recent1.md", "recent2.md"}
	for _, name := range recentFiles {
		if err := os.WriteFile(filepath.Join(memDir, name), []byte("content"), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", name, err)
		}
	}

	// old file (2 hours ago)
	oldPath := filepath.Join(memDir, "old.md")
	if err := os.WriteFile(oldPath, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile old: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	got := scanRecentMemories(tmpDir, instance, 1*time.Hour)
	if len(got) != 2 {
		t.Fatalf("expected 2 recent memories, got %d", len(got))
	}

	// verify sorted newest-first
	if got[0].modTime.Before(got[1].modTime) {
		t.Errorf("expected newest first: %v should be >= %v", got[0].modTime, got[1].modTime)
	}

	// verify old file excluded
	for _, m := range got {
		if m.name == "old.md" {
			t.Error("old file should not be included")
		}
	}
}

func TestScanRecentMemories_NoDir(t *testing.T) {
	got := scanRecentMemories("/nonexistent/path", "nope", 1*time.Hour)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestBuildRecoveryQuestion(t *testing.T) {
	tmpDir := t.TempDir()
	fpath := filepath.Join(tmpDir, "diag.md")
	if err := os.WriteFile(fpath, []byte("line1\nline2\nline3"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	memories := []memoryFile{
		{name: "diag.md", path: fpath, modTime: time.Now()},
	}

	q := buildRecoveryQuestion(memories)

	for _, want := range []string{"刚重启", "diag.md", "line1", "无需恢复"} {
		if !containsStr(q, want) {
			t.Errorf("question missing %q", want)
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTruncateLines(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		n     int
		want  int
	}{
		{"less than n", []string{"a", "b"}, 5, 2},
		{"equal to n", []string{"a", "b", "c"}, 3, 3},
		{"greater than n", []string{"a", "b", "c", "d", "e"}, 3, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLines(tt.lines, tt.n)
			if len(got) != tt.want {
				t.Errorf("truncateLines(%d lines, %d) = %d lines, want %d", len(tt.lines), tt.n, len(got), tt.want)
			}
		})
	}
}

func TestRunRecovery_NoMemories(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := RecoveryConfig{
		BaseDir:  tmpDir,
		Instance: "empty-instance",
	}

	if err := RunRecovery(context.Background(), cfg); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestRunRecovery_NoExecutor(t *testing.T) {
	tmpDir := t.TempDir()
	instance := "test-db"
	memDir := filepath.Join(tmpDir, "memory", instance)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "recent.md"), []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := RecoveryConfig{
		BaseDir:  tmpDir,
		Instance: instance,
		Executor: nil,
	}

	if err := RunRecovery(context.Background(), cfg); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}
