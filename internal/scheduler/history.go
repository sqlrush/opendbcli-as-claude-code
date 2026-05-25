/*-------------------------------------------------------------------------
 *
 * history.go
 *	  HistoryEntry records one task execution.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/scheduler/history.go
 *
 *-------------------------------------------------------------------------
 */
package scheduler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const maxHistoryEntries = 100

// HistoryEntry records one task execution.
type HistoryEntry struct {
	TaskName  string    `json:"task_name"`
	Skill     string    `json:"skill"`
	StartTime time.Time `json:"start_time"`
	Duration  string    `json:"duration"`
	Success   bool      `json:"success"`
	Summary   string    `json:"summary,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// History manages a ring buffer of task execution records, persisted to disk.
type History struct {
	mu      sync.Mutex
	entries []HistoryEntry
	path    string
}

// NewHistory creates a History that persists to the given file path.
// It loads existing entries from disk if available.
func NewHistory(dir string) *History {
	h := &History{
		path: filepath.Join(dir, "history.json"),
	}
	h.load()
	return h
}

// Add appends an entry and persists to disk.
func (h *History) Add(entry HistoryEntry) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.entries = append(h.entries, entry)
	if len(h.entries) > maxHistoryEntries {
		h.entries = h.entries[len(h.entries)-maxHistoryEntries:]
	}
	h.save()
}

// Entries returns a copy of all history entries (oldest first).
func (h *History) Entries() []HistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	cp := make([]HistoryEntry, len(h.entries))
	copy(cp, h.entries)
	return cp
}

// Recent returns the last n entries (newest first).
func (h *History) Recent(n int) []HistoryEntry {
	h.mu.Lock()
	defer h.mu.Unlock()

	if n > len(h.entries) {
		n = len(h.entries)
	}
	result := make([]HistoryEntry, n)
	for i := 0; i < n; i++ {
		result[i] = h.entries[len(h.entries)-1-i]
	}
	return result
}

func (h *History) load() {
	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	var entries []HistoryEntry
	if json.Unmarshal(data, &entries) == nil {
		if len(entries) > maxHistoryEntries {
			entries = entries[len(entries)-maxHistoryEntries:]
		}
		h.entries = entries
	}
}

func (h *History) save() {
	dir := filepath.Dir(h.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}

	data, err := json.MarshalIndent(h.entries, "", "  ")
	if err != nil {
		return
	}

	// Atomic write: write temp file then rename.
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	os.Rename(tmp, h.path)
}
