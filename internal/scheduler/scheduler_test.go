/*-------------------------------------------------------------------------
 *
 * scheduler_test.go
 *	  Test cases for scheduler.go (scheduler package):
 *	  TestTaskConfigIsEnabled, TestHistoryRingBuffer,
 *	  TestHistoryPersistence.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/scheduler_test.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskConfigIsEnabled(t *testing.T) {
	// Default (nil) = enabled.
	tc := TaskConfig{Name: "test"}
	if !tc.IsEnabled() {
		t.Error("nil Enabled should default to true")
	}

	// Explicitly enabled.
	enabled := true
	tc.Enabled = &enabled
	if !tc.IsEnabled() {
		t.Error("Enabled=true should be true")
	}

	// Explicitly disabled.
	disabled := false
	tc.Enabled = &disabled
	if tc.IsEnabled() {
		t.Error("Enabled=false should be false")
	}
}

func TestHistoryRingBuffer(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)

	// Add more than max entries.
	for i := 0; i < maxHistoryEntries+20; i++ {
		h.Add(HistoryEntry{
			TaskName:  "test",
			StartTime: time.Now(),
			Duration:  "100ms",
			Success:   true,
			Summary:   "ok",
		})
	}

	entries := h.Entries()
	if len(entries) != maxHistoryEntries {
		t.Errorf("expected %d entries, got %d", maxHistoryEntries, len(entries))
	}
}

func TestHistoryPersistence(t *testing.T) {
	dir := t.TempDir()

	// Write some entries.
	h1 := NewHistory(dir)
	h1.Add(HistoryEntry{TaskName: "task1", Success: true, Summary: "done"})
	h1.Add(HistoryEntry{TaskName: "task2", Success: false, Error: "timeout"})

	// Load from disk.
	h2 := NewHistory(dir)
	entries := h2.Entries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries after reload, got %d", len(entries))
	}
	if entries[0].TaskName != "task1" {
		t.Errorf("expected task1, got %s", entries[0].TaskName)
	}
	if entries[1].Error != "timeout" {
		t.Errorf("expected 'timeout' error, got %s", entries[1].Error)
	}
}

func TestHistoryRecent(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)

	h.Add(HistoryEntry{TaskName: "old"})
	h.Add(HistoryEntry{TaskName: "middle"})
	h.Add(HistoryEntry{TaskName: "new"})

	recent := h.Recent(2)
	if len(recent) != 2 {
		t.Fatalf("expected 2, got %d", len(recent))
	}
	if recent[0].TaskName != "new" {
		t.Errorf("expected newest first, got %s", recent[0].TaskName)
	}
	if recent[1].TaskName != "middle" {
		t.Errorf("expected middle second, got %s", recent[1].TaskName)
	}

	// Request more than available.
	all := h.Recent(100)
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}
}

func TestHistoryAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	h := NewHistory(dir)
	h.Add(HistoryEntry{TaskName: "test"})

	// Verify the file exists.
	path := filepath.Join(dir, "history.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Error("history.json was not created")
	}

	// Verify no temp file left behind.
	tmpPath := path + ".tmp"
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("temp file should not exist after write")
	}
}
