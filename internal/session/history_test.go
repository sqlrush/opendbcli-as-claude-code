/*-------------------------------------------------------------------------
 *
 * history_test.go
 *	  Test cases for history.go (session package):
 *	  TestSessionHistory_AddAndEntries, TestSessionHistory_SaveAndLoad,
 *	  TestSessionHistory_Clear.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/session/history_test.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionHistory_AddAndEntries(t *testing.T) {
	sh := NewSessionHistory(t.TempDir(), "testdb")

	sh.Add("SELECT 1")
	sh.Add("SELECT 2")
	sh.Add("SELECT 3")

	entries := sh.Entries()
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}

	wantCommands := []string{"SELECT 1", "SELECT 2", "SELECT 3"}
	for i, want := range wantCommands {
		if entries[i].Command != want {
			t.Errorf("entry[%d].Command = %q, want %q", i, entries[i].Command, want)
		}
		if entries[i].InstanceName != "testdb" {
			t.Errorf("entry[%d].InstanceName = %q, want %q", i, entries[i].InstanceName, "testdb")
		}
		if entries[i].Timestamp.IsZero() {
			t.Errorf("entry[%d].Timestamp should not be zero", i)
		}
	}

	// Verify ordering: each timestamp should be >= the previous
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp.Before(entries[i-1].Timestamp) {
			t.Errorf("entry[%d] timestamp %v is before entry[%d] timestamp %v",
				i, entries[i].Timestamp, i-1, entries[i-1].Timestamp)
		}
	}
}

func TestSessionHistory_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()

	// Create and populate first instance
	sh1 := NewSessionHistory(dir, "proddb")
	sh1.Add("CREATE TABLE users (id INT)")
	sh1.Add("INSERT INTO users VALUES (1)")
	sh1.Add("SELECT * FROM users")

	if err := sh1.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify file exists
	filePath := filepath.Join(dir, "proddb.json")
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("history file not created: %v", err)
	}

	// Create a new instance and load from file
	sh2 := NewSessionHistory(dir, "proddb")
	if err := sh2.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	original := sh1.Entries()
	loaded := sh2.Entries()

	if len(loaded) != len(original) {
		t.Fatalf("loaded %d entries, want %d", len(loaded), len(original))
	}

	for i := range original {
		if loaded[i].Command != original[i].Command {
			t.Errorf("loaded[%d].Command = %q, want %q", i, loaded[i].Command, original[i].Command)
		}
		if loaded[i].InstanceName != original[i].InstanceName {
			t.Errorf("loaded[%d].InstanceName = %q, want %q", i, loaded[i].InstanceName, original[i].InstanceName)
		}
		if !loaded[i].Timestamp.Equal(original[i].Timestamp) {
			t.Errorf("loaded[%d].Timestamp = %v, want %v", i, loaded[i].Timestamp, original[i].Timestamp)
		}
	}
}

func TestSessionHistory_Clear(t *testing.T) {
	dir := t.TempDir()

	sh := NewSessionHistory(dir, "devdb")
	sh.Add("SELECT 1")
	sh.Add("SELECT 2")

	// Save so file exists
	if err := sh.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Clear
	if err := sh.Clear(); err != nil {
		t.Fatalf("Clear() error: %v", err)
	}

	// Verify in-memory entries are gone
	entries := sh.Entries()
	if entries != nil {
		t.Errorf("Entries() after Clear = %v, want nil", entries)
	}

	// Verify file is deleted
	filePath := filepath.Join(dir, "devdb.json")
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("history file should be deleted after Clear()")
	}
}

func TestSessionHistory_Load_NoFile(t *testing.T) {
	dir := t.TempDir()

	sh := NewSessionHistory(dir, "nonexistent")
	if err := sh.Load(); err != nil {
		t.Fatalf("Load() with no file should succeed, got error: %v", err)
	}

	entries := sh.Entries()
	if entries != nil {
		t.Errorf("Entries() after loading nonexistent file = %v, want nil", entries)
	}
}

func TestSessionHistory_Entries_Immutable(t *testing.T) {
	sh := NewSessionHistory(t.TempDir(), "testdb")
	sh.Add("SELECT 1")
	sh.Add("SELECT 2")

	// Get entries and mutate the returned slice
	entries := sh.Entries()
	entries[0].Command = "MUTATED"
	entries = append(entries, HistoryEntry{Command: "EXTRA"})

	// Verify internal state is unchanged
	internal := sh.Entries()
	if len(internal) != 2 {
		t.Fatalf("internal entries length = %d, want 2", len(internal))
	}
	if internal[0].Command != "SELECT 1" {
		t.Errorf("internal[0].Command = %q, want %q", internal[0].Command, "SELECT 1")
	}
	if internal[1].Command != "SELECT 2" {
		t.Errorf("internal[1].Command = %q, want %q", internal[1].Command, "SELECT 2")
	}
}
