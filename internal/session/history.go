/*-------------------------------------------------------------------------
 *
 * history.go
 *	  HistoryEntry represents a single command executed in a session.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/session/history.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// HistoryEntry represents a single command executed in a session.
type HistoryEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Command      string    `json:"command"`
	InstanceName string    `json:"instance_name"`
}

// SessionHistory manages command history for a specific database instance.
type SessionHistory struct {
	entries      []HistoryEntry
	historyDir   string
	instanceName string
}

// NewSessionHistory creates a new SessionHistory for the given instance.
func NewSessionHistory(historyDir string, instanceName string) *SessionHistory {
	return &SessionHistory{
		entries:      nil,
		historyDir:   historyDir,
		instanceName: instanceName,
	}
}

// Add appends a command entry with the current timestamp.
func (sh *SessionHistory) Add(command string) {
	entry := HistoryEntry{
		Timestamp:    time.Now(),
		Command:      command,
		InstanceName: sh.instanceName,
	}
	sh.entries = append(sh.entries, entry)
}

// Entries returns all history entries (newest last).
// The returned slice is a copy to prevent mutation of internal state.
func (sh *SessionHistory) Entries() []HistoryEntry {
	if len(sh.entries) == 0 {
		return nil
	}
	result := make([]HistoryEntry, len(sh.entries))
	copy(result, sh.entries)
	return result
}

// filePath returns the path to the history file for this instance.
func (sh *SessionHistory) filePath() string {
	return filepath.Join(sh.historyDir, sh.instanceName+".json")
}

// Save persists the history entries to historyDir/<instanceName>.json as a JSON array.
func (sh *SessionHistory) Save() error {
	if err := os.MkdirAll(sh.historyDir, 0755); err != nil {
		return fmt.Errorf("creating history directory: %w", err)
	}

	data, err := json.MarshalIndent(sh.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling history entries: %w", err)
	}

	if err := os.WriteFile(sh.filePath(), data, 0644); err != nil {
		return fmt.Errorf("writing history file: %w", err)
	}

	return nil
}

// Load reads history entries from file. If the file does not exist,
// it initializes with empty entries and returns nil.
func (sh *SessionHistory) Load() error {
	data, err := os.ReadFile(sh.filePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			sh.entries = nil
			return nil
		}
		return fmt.Errorf("reading history file: %w", err)
	}

	var entries []HistoryEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("unmarshaling history entries: %w", err)
	}

	sh.entries = entries
	return nil
}

// Clear removes all in-memory entries and deletes the history file.
func (sh *SessionHistory) Clear() error {
	sh.entries = nil

	err := os.Remove(sh.filePath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing history file: %w", err)
	}

	return nil
}
