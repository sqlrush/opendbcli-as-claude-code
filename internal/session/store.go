/*-------------------------------------------------------------------------
 *
 * store.go
 *	  Store manages multiple SessionHistory instances, one per database
 *	  instance.
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/session/store.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"fmt"
	"sort"
)

// Store manages multiple SessionHistory instances, one per database instance.
type Store struct {
	sessions   map[string]*SessionHistory
	historyDir string
}

// NewStore creates a new Store that persists history files in historyDir.
func NewStore(historyDir string) *Store {
	return &Store{
		sessions:   make(map[string]*SessionHistory),
		historyDir: historyDir,
	}
}

// GetOrCreate returns an existing SessionHistory for the instance name,
// or creates a new one if it does not yet exist.
func (s *Store) GetOrCreate(instanceName string) *SessionHistory {
	if sh, ok := s.sessions[instanceName]; ok {
		return sh
	}

	sh := NewSessionHistory(s.historyDir, instanceName)
	s.sessions[instanceName] = sh
	return sh
}

// Save persists the history for a specific instance.
func (s *Store) Save(instanceName string) error {
	sh, ok := s.sessions[instanceName]
	if !ok {
		return fmt.Errorf("session not found for instance %q", instanceName)
	}
	return sh.Save()
}

// SaveAll persists all tracked sessions.
func (s *Store) SaveAll() error {
	for name, sh := range s.sessions {
		if err := sh.Save(); err != nil {
			return fmt.Errorf("saving session %q: %w", name, err)
		}
	}
	return nil
}

// List returns all instance names that have an active session history.
// The names are returned in sorted order for deterministic output.
func (s *Store) List() []string {
	names := make([]string, 0, len(s.sessions))
	for name := range s.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
