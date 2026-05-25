/*-------------------------------------------------------------------------
 *
 * filestore.go
 *	  FileSessionStore persists sessions as JSONL files on the local
 *	  filesystem. Directory layout:
 *	  {baseDir}/{instance}/{session_id}.jsonl
 *
 *
 * Copyright 2026 Sqlrush <sqlrush@gmail.com>
 *
 * Author: Sqlrush <sqlrush@gmail.com>
 *
 * IDENTIFICATION
 *	  internal/engine/session/filestore.go
 *
 *-------------------------------------------------------------------------
 */
package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// FileSessionStore persists sessions as JSONL files on the local filesystem.
// Directory layout: {baseDir}/{instance}/{session_id}.jsonl
type FileSessionStore struct {
	baseDir string
}

// NewFileSessionStore creates a file-backed session store.
func NewFileSessionStore(baseDir string) *FileSessionStore {
	return &FileSessionStore{baseDir: baseDir}
}

// Save writes the session to a JSONL file using atomic temp+rename.
func (s *FileSessionStore) Save(_ context.Context, session *Session) error {
	session.UpdatedAt = time.Now()

	dir := filepath.Join(s.baseDir, session.Instance)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	target := filepath.Join(dir, string(session.ID)+".jsonl")

	// Atomic write: write to temp file, then rename.
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0640); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Load reads a session from disk. Returns nil, nil if the file does not exist.
func (s *FileSessionStore) Load(_ context.Context, id SessionID) (*Session, error) {
	instance := id.Instance()
	path := filepath.Join(s.baseDir, instance, string(id)+".jsonl")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var session Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// ListByInstance returns sessions for an instance sorted by UpdatedAt descending.
func (s *FileSessionStore) ListByInstance(_ context.Context, instance string, limit int) ([]*Session, error) {
	dir := filepath.Join(s.baseDir, instance)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type fileEntry struct {
		path  string
		mtime time.Time
	}

	var files []fileEntry
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileEntry{
			path:  filepath.Join(dir, e.Name()),
			mtime: info.ModTime(),
		})
	}

	// Sort by modification time descending (newest first).
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime)
	})

	if limit > 0 && len(files) > limit {
		files = files[:limit]
	}

	var sessions []*Session
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		var sess Session
		if err := json.Unmarshal(data, &sess); err != nil {
			continue
		}
		sessions = append(sessions, &sess)
	}
	return sessions, nil
}
